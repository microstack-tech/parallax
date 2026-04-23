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

// MainnetBootnodes are the enode URLs of the P2P bootstrap nodes on the
// main Parallax network. These carry a persistent secp256k1 NodeID and
// are consumed by NodeID-keyed tooling (discv4 crawler, resolve, etc.)
// and any v1.x-compat peer that bonds via PING/PONG.
//
// The v2 handshake bootstrap path does not use this slice; it uses
// MainnetBootnodesV2 instead.
var MainnetBootnodes = []string{
	// Parallax Foundation Go Bootnodes
	// us-boston
	"enode://34957ea19a9c8170892a41633f7ec05c3ca7d13d64fd155c485985c850f8cad72d5fa6ffcba62038580671565b76bd38b61cbc8145a203aa174f1069a3e10eb2@168.231.74.175:32110",
	// eu-frankfurt
	"enode://2060e01e74e46fd944e172373dc18eb1478ec050d9c2d66a4486347c215c5fc5a8f72cb8549419828d61e4f9ff75d31ced7977fc89967546e389ff821a5dc10e@72.61.186.233:32110",
	// br-sao-paulo
	"enode://7fcacf55ab8ffb8bd7bc722ba2336b6a4b304a2fc76fa65aadab4e17d196261793287f2cac80d10a25a351f06a038e73cca1170b2007af076bf82eb33e85d2f3@69.62.94.166:32110",
}

// MainnetBootnodesV2 are the addresses of P2P bootstrap nodes on the
// main Parallax network, in plain "ip:port" form. Consumed by the
// BIP324-style v2 handshake path (addrman KeyType=0x00); operators
// who run a v2-native bootnode register its endpoint here.
//
// The Foundation bootnodes in MainnetBootnodes serve v1 RLPx only
// and are intentionally omitted here — listing a v1-only endpoint
// produces a TCP RST on every dial-scheduler tick.
var MainnetBootnodesV2 = []string{
	"72.61.137.32:32110",
}

// TestnetBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// test network.
var TestnetBootnodes = []string{}

// TestnetBootnodesV2 are the plain "ip:port" bootstrap addresses used
// by the v2 handshake bootstrap path on testnet.
var TestnetBootnodesV2 = []string{}

// MainnetDNSSeeds are the DNS hostnames the node resolves at a 24h
// cadence (Bitcoin parity) to bootstrap its addrbook with v2.0-native
// (KeyType=0x00) peers on the default port 32110. Each A/AAAA record
// returned is paired with the default port and ingested into addrman
// with source=dns_seed. Plain DNS — no enrtree — so it works in
// v2-only posture (`--legacy-discovery=off`) where enrtree's legacy
// NodeIDs are useless.
var MainnetDNSSeeds = []string{
	"seed.prlxdisc.org",
}

// TestnetDNSSeeds is empty by default; testnet operators set their own
// via --dnsseed flag.
var TestnetDNSSeeds = []string{}

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
