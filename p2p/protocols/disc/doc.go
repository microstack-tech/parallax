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

// Package disc implements the `parallax-disc/1` subprotocol — Bitcoin-style
// peer discovery over TCP gossip, carried as a devp2p subprotocol on top of
// the existing RLPx transport.
//
// Wire-format overview (codes are local to this subprotocol, not global):
//
//	0x00  GetPeers{}                               — empty payload
//	0x01  Peers{ Entries []PeerEntry }             — max 1000 entries
//	0x02  YourAddr{ NetworkID, Addr, TCPPort }     — observed remote source
//
// Messages 0x00 and 0x01 mirror Bitcoin's `getaddr`/`addr`. 0x02 is a
// structural deviation: Bitcoin piggybacks observed-address reports on the
// `version` handshake, but devp2p negotiates capabilities before any
// subprotocol message is exchanged, so we send YourAddr as the first
// `parallax-disc/1` message each side writes after negotiation.
//
// PeerEntry carries a BIP155 NetworkID tag (0x01=IPv4, 0x02=IPv6,
// 0x03=Tor v2 decode-only, 0x04=Tor v3, 0x05=I2P, 0x06=CJDNS). A KeyType
// field distinguishes v2.0-native peers (0x00, dial via BIP324-style
// handshake on IP:port alone) from legacy enode peers (0x01, 64-byte
// secp256k1 pubkey, dial via legacy RLPx). Unknown NetworkID or KeyType
// values are skipped silently — forward compat for future additions.
//
// LastSeen is carried as Unix seconds but treated as an unverified origin
// claim. Ingest clamps to [now-10min, now+10min] and subtracts a 2-hour
// penalty for gossip-sourced entries so directly-observed addresses rank
// fresher. This is Bitcoin's `AdjustedTime` + penalty discipline.
//
// Phase 2 scope (this package in its initial form): capability negotiation,
// message encode/decode, and a handler skeleton that logs receipt and
// validates payload shape. The handler does NOT yet populate addrman —
// that wiring lands in Phase 4. Rate limits and DoS protection are
// scaffolded with TODO markers; their production values are specified in
// PIP-0006 §Phase 4 (1 unsolicited Peers / 24h, token bucket 0.1/sec,
// bloom-filter per-peer known-address set, etc.).
package disc
