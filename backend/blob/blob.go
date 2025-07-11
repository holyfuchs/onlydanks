package blob

import (
	"context"
	"errors"
	"math"
	"math/big"
	"proto-dankmessaging/backend/dependencies"
	"proto-dankmessaging/backend/dependencies/queries/dbgen"
	"time"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/uint256"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"
)

type Blob struct {
	dep         *dependencies.Dependencies
	queries     *dbgen.Queries
	key         *keystore.Key
	client      *ethclient.Client
	blockHeight int64
}

func NewBlob(dep *dependencies.Dependencies) (*Blob, error) {
	privateKey, err := crypto.HexToECDSA(dep.Config.PrivateKey)
	if err != nil {
		return nil, errors.New("failed to parse private key: " + err.Error())
	}
	key := &keystore.Key{
		Address:    crypto.PubkeyToAddress(privateKey.PublicKey),
		PrivateKey: privateKey,
	}

	rpcClient, err := rpc.Dial(dep.Config.RpcUrl)
	if err != nil {
		return nil, errors.New("failed to connect to the Ethereum client: " + err.Error())
	}
	client := ethclient.NewClient(rpcClient)

	queries := dbgen.New(dep.DB.Pool())
	blockHeight, err := queries.GetBlobUpdate(context.Background())
	if err != nil {
		err = queries.SetBlobUpdate(context.Background(), 8698539)
		if err != nil {
			return nil, errors.New("failed to set blob update: " + err.Error())
		}
	}
	return &Blob{
		dep:         dep,
		queries:     queries,
		key:         key,
		client:      client,
		blockHeight: int64(blockHeight),
	}, nil
}

var blobMsgMagicBytes = []byte{0x2f, 0x39, 0x4d, 0x21}

// should keep listening for new blobs and add them to the database
func (b *Blob) Start(ctx context.Context) error {
	submitterTicker := time.NewTicker(1 * time.Second)
	var updateTicker *time.Ticker
	if b.dep.Config.BlobUpdate {
		updateTicker = time.NewTicker(20 * time.Second)
		b.updateBlob()
	} else {
		updateTicker = time.NewTicker(time.Duration(math.MaxInt64))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-submitterTicker.C:
			err := b.generateAndSubmitBlob()
			if err != nil {
				log.Error().Err(err).Msg("failed to generate and submit blob")
			}
		case <-updateTicker.C:
			err := b.updateBlob()
			if err != nil {
				log.Error().Err(err).Msg("failed to update blob")
			}
		}
	}
}

func (b *Blob) generateAndSubmitBlob() error {
	msgs, err := b.queries.GetBlobSubmissions(context.Background())
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	blob := &BlobContent{
		Messages: []*Message{},
	}
	for _, msg := range msgs {
		blob.Messages = append(blob.Messages, &Message{
			EphemeralPubkey: msg.Pubkey,
			SearchIndex:     msg.Index,
			Message:         msg.Message,
		})
	}
	blobBytes, err := proto.Marshal(blob)
	if err != nil {
		return err
	}
	blobBytes = append(blobMsgMagicBytes, blobBytes...)
	log.Info().Bytes("blob", blobBytes).Msg("submitting blob to the chain")
	err = b.submitBlob(context.Background(), blobBytes)
	if err != nil {
		log.Error().Err(err).Msg("failed to submit blob")
		return err
	}
	for _, msg := range msgs {
		err = b.queries.RemoveBlobSubmission(context.Background(), msg.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *Blob) submitBlob(ctx context.Context, blobBytes []byte) error {
	signer := types.NewPragueSigner(big.NewInt(int64(b.dep.Config.ChainId)))
	nonce, err := b.client.PendingNonceAt(ctx, b.key.Address)
	if err != nil {
		return errors.New("failed to get nonce: " + err.Error())
	}
	blob, err := EncodeDataToBlob(blobBytes)
	if err != nil {
		return errors.New("failed to encode data to blob: " + err.Error())
	}
	blobCommitment, err := kzg4844.BlobToCommitment(blob)
	if err != nil {
		return errors.New("failed to compute blob commitment: " + err.Error())
	}
	blobProof, err := kzg4844.ComputeBlobProof(blob, blobCommitment)
	if err != nil {
		return errors.New("failed to compute blob proof: " + err.Error())
	}
	sidecar := types.BlobTxSidecar{
		Blobs:       []kzg4844.Blob{*blob},
		Commitments: []kzg4844.Commitment{blobCommitment},
		Proofs:      []kzg4844.Proof{blobProof},
	}

	tx := types.NewTx(&types.BlobTx{
		ChainID:    uint256.NewInt(b.dep.Config.ChainId),
		Nonce:      nonce,
		GasTipCap:  uint256.NewInt(10_000_000_000),
		GasFeeCap:  uint256.NewInt(10_000_000_000),
		Gas:        21000,
		To:         common.HexToAddress("0x0000000000000000000000000000000000000000"),
		Value:      uint256.NewInt(0),
		Data:       nil,
		BlobFeeCap: uint256.NewInt(3e10),
		BlobHashes: sidecar.BlobHashes(),
		Sidecar:    &sidecar,
	})

	signedTx, err := types.SignTx(tx, signer, b.key.PrivateKey)
	if err != nil {
		return errors.New("failed to sign transaction: " + err.Error())
	}
	if err = b.client.SendTransaction(ctx, signedTx); err != nil {
		return errors.New("failed to send transaction: " + err.Error())
	}
	txHash := signedTx.Hash().Hex()
	log.Info().Str("tx_hash", txHash).Msg("submitted blob to the chain")
	return nil
}
