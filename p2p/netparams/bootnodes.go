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
var MainnetBootnodes = []string{}

// MainnetBootnodesV2 are the addresses of P2P bootstrap nodes on the
// main Parallax network, in plain "ip:port" form. Consumed by the
// BIP324-style v2 handshake path (addrman KeyType=0x00); operators
// who run a v2-native bootnode register its endpoint here.
//
// The Foundation bootnodes below are the v2.0 flag-day endpoints:
// they must be running the v2 transport before this release is
// deployed, since a v1-only endpoint here answers every v2 dial
// with a failed handshake and — with MainnetBootnodes empty — no
// other cold-start bootstrap path exists besides the DNS seeds.
var MainnetBootnodesV2 = []string{
	"168.231.74.175:32110",
	"72.61.186.233:32110",
	"69.62.94.166:32110",
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
