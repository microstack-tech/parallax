// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package netparams

import (
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/util"
)

// MainnetBootnodes are the addresses of the P2P bootstrap nodes on the
// main Parallax network, in plain "ip:port" form.
//
// Parallax v2.0 bootnodes run only the BIP324-style v2 handshake (with
// parallax-disc/1 and the rest of the subprotocols on top); no NodeID
// / enode URL is required or accepted. The v2 handshake authenticates
// against "whoever answered on that ip:port", which is exactly what a
// bootnode is.
var MainnetBootnodes = []string{
	// Parallax Foundation Go Bootnodes
	// us-boston
	"168.231.74.175:32110",
	// eu-frankfurt
	"72.61.186.233:32110",
	// br-sao-paulo
	"69.62.94.166:32110",
}

// TestnetBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// test network.
var TestnetBootnodes = []string{}

var V5Bootnodes = []string{
	// Teku team's bootnode
	// "enr:-KG4QOtcP9X1FbIMOe17QNMKqDxCpm14jcX5tiOE4_TyMrFqbmhPZHK_ZPG2Gxb1GE2xdtodOfx9-cgvNtxnRyHEmC0ghGV0aDKQ9aX9QgAAAAD__________4JpZIJ2NIJpcIQDE8KdiXNlY3AyNTZrMaEDhpehBDbZjM_L9ek699Y7vhUJ-eAdMyQW_Fil522Y0fODdGNwgiMog3VkcIIjKA",
	// "enr:-KG4QDyytgmE4f7AnvW-ZaUOIi9i79qX4JwjRAiXBZCU65wOfBu-3Nb5I7b_Rmg3KCOcZM_C3y5pg7EBU5XGrcLTduQEhGV0aDKQ9aX9QgAAAAD__________4JpZIJ2NIJpcIQ2_DUbiXNlY3AyNTZrMaEDKnz_-ps3UUOfHWVYaskI5kWYO_vtYMGYCQRAR3gHDouDdGNwgiMog3VkcIIjKA",
}

const dnsPrefix = "enrtree://AJCNZNPUXUUJASCE35AIA2FFLGBQBF2SKWBUVXHTZLJCJZFHMJHII@"

// KnownDNSNetwork returns the address of a public DNS-based node list for the given
// genesis hash and protocol. See https://github.com/ParallaxProtocol/parallax-dns-lists for more
// information.
func KnownDNSNetwork(genesis util.Hash, protocol string) string {
	var net string
	switch genesis {
	case chainparams.MainnetGenesisHash:
		net = "mainnet"
	default:
		return ""
	}
	return dnsPrefix + protocol + "." + net + ".prlxdisc.org"
}
