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

package disc

import (
	"github.com/ParallaxProtocol/parallax/v2/p2p"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enr"
)

// ProtocolName is the devp2p capability name for this subprotocol.
const ProtocolName = "parallax-disc"

// ProtocolVersion is the only supported version of parallax-disc today.
//
// Flag-day constraint: ProtocolLength changed from 3 to 4 within this
// same version 1 when HelloMsg was added (v2.0.0-rc1 shipped Length 3,
// no Hello; rc2 onward ship Length 4). devp2p assumes (name, version)
// uniquely determines Length — two peers advertising parallax-disc/1
// with different Lengths compute different per-protocol code offsets and
// silently misroute the message codes of every capability sorting after
// "parallax-disc" (parallax-snap; the base "parallax" capability sorts
// before it and is unaffected). This is therefore safe ONLY
// under a coordinated flag-day upgrade in which no rc1 (Length 3) node
// ever connects to an rc2+ (Length 4) node. If a mixed population is
// ever possible, bump this to 2 instead of changing Length again.
const ProtocolVersion uint = 1

// ProtocolLength is the number of message codes in parallax-disc/1. Must
// be exactly one past the highest code used. Currently HelloMsg = 0x03,
// so 4. p2p's protoRW.WriteMsg rejects msg.Code >= rw.Length with
// "invalid message code: not handled" — a stale value here silently
// drops outgoing Hello messages and partitions the network from
// patched peers. See the flag-day note on ProtocolVersion before
// changing this without a version bump.
const ProtocolLength uint64 = 4

// MaxMessageSize caps the size of a single inbound message. Chosen so
// that a full 1000-entry `Peers` message fits comfortably:
//
//	1000 entries × (max PeerEntry ≈ 1+32+2+1+64+8 = 108 bytes + RLP
//	overhead ≈ 115 bytes) ≈ 115 kB, plus list framing.
//
// 256 kB gives us ~2× headroom, still well below the RLPx frame cap.
const MaxMessageSize = 256 * 1024

// MakeProtocol builds the devp2p Protocol spec for parallax-disc/1. The
// backend is invoked to construct the per-peer handler state.
//
// Callers should pass the result to p2p.Server.Protocols alongside the
// existing `parallax` and `parallax-snap` protocols.
func MakeProtocol(backend Backend) p2p.Protocol {
	return p2p.Protocol{
		Name:    ProtocolName,
		Version: ProtocolVersion,
		Length:  ProtocolLength,
		Run: func(peer *p2p.Peer, rw p2p.MsgReadWriter) error {
			return Run(backend, peer, rw)
		},
		NodeInfo: func() any {
			return NodeInfo{Version: ProtocolVersion}
		},
		PeerInfo: func(id enode.ID) any {
			return PeerInfo{
				Version:   ProtocolVersion,
				Handshake: backend.PeerHandshake(id),
			}
		},
		Attributes: []enr.Entry{enrEntry{Version: ProtocolVersion}},
	}
}

// NodeInfo is the admin-API surface for parallax-disc/1. Extended in
// later phases with counts of gossiped addresses, quorum state, etc.
type NodeInfo struct {
	Version uint `json:"version"`
}

// PeerInfo is the per-peer shape reported under admin.peers.protocols.
//
// Handshake values:
//
//	"v2"        — session is authenticated via the BIP324-style v2
//	              handshake. The remote definitely supports v2; we
//	              can't tell whether it ALSO supports legacy RLPx
//	              without trying.
//	"legacy+v2" — session is on legacy RLPx AND the remote advertises
//	              parallax-disc/1 in its capability list. Both
//	              handshake variants work with this peer.
type PeerInfo struct {
	Version   uint   `json:"version"`
	Handshake string `json:"handshake"`
}

// enrEntry is the ENR key/value pair advertised by nodes that support
// parallax-disc/1. Key is "parallax-disc", value is the version integer.
// Transitional — ENR itself is slated for removal in v3.0.
type enrEntry struct {
	Version uint
	// Ignore trailing fields so future versions can extend the ENR
	// entry without breaking older parsers.
	_ []byte `rlp:"tail"`
}

func (enrEntry) ENRKey() string { return "parallax-disc" }
