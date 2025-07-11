// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.13;

import {Script, console} from "forge-std/Script.sol";
import {L2Registrar} from "../src/L2Registrar.sol";

contract Deploy is Script {
    L2Registrar public l2Registrar;

    function setUp() public {}

    function run() public {
        vm.startBroadcast();

        l2Registrar = new L2Registrar(0xc3a4eB979e9035486b54Fe8b57D36aEF9519eAc6);

        vm.stopBroadcast();
    }
}


contract TestGetName is Script {
    L2Registrar public l2Registrar;

    function setUp() public {
        l2Registrar = L2Registrar(0x1468386e6ABb1874c0d9fD43899EbD21A12470A6);
    }

    function run() public {
        // console.log("Registry: %s", address(l2Registrar.registry()));
        vm.startBroadcast();

        l2Registrar.register("small", 0x522F3038F78d91dADA58F8A768be7611134767D5);

        vm.stopBroadcast();
    }
}
