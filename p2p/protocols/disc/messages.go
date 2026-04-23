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
	"errors"
	"fmt"
)

// Message codes for parallax-disc/1. Local to this subprotocol.
const (
	GetPeersMsg uint64 = 0x00
	PeersMsg    uint64 = 0x01
	YourAddrMsg uint64 = 0x02
)

// Wire limits — these numbers are load-bearing for DoS resistance. See
// security considerations in PIP-0006 §5 before changing. The 1000-entry
// cap matches Bitcoin's `MAX_ADDR_TO_SEND`; pushing it higher gives an
// attacker a larger single-message memory-amp ratio.
const (
	MaxPeersPerMessage = 1000
)

// BIP155 network IDs. Kept here (rather than imported from p2p/addrman)
// so the wire format is the single source of truth and this package has
// no dependency on the addrman package.
const (
	NetIPv4  uint8 = 0x01
	NetIPv6  uint8 = 0x02
	NetTorV2 uint8 = 0x03 // decode-only per PIP-0006 — do not relay
	NetTorV3 uint8 = 0x04
	NetI2P   uint8 = 0x05
	NetCJDNS uint8 = 0x06
)

// KeyType tags the identity-key scheme for a PeerEntry. See package doc
// for dial-model semantics.
const (
	KeyTypeNone      uint8 = 0x00 // v2.0-native; NodeID MUST be zero-length
	KeyTypeSecp256k1 uint8 = 0x01 // legacy enode; NodeID is 64 bytes (x||y)
)

// addrLenFor returns the required Addr byte length for a BIP155 NetworkID,
// or -1 if the ID is unknown.
func addrLenFor(net uint8) int {
	switch net {
	case NetIPv4:
		return 4
	case NetIPv6:
		return 16
	case NetTorV2:
		return 10
	case NetTorV3:
		return 32
	case NetI2P:
		return 32
	case NetCJDNS:
		return 16
	}
	return -1
}

// nodeIDLenFor returns the required NodeID byte length for a KeyType, or
// -1 if the KeyType is unknown (entry should be skipped).
func nodeIDLenFor(kt uint8) int {
	switch kt {
	case KeyTypeNone:
		return 0
	case KeyTypeSecp256k1:
		return 64
	}
	return -1
}

// GetPeers is the `parallax-disc/1` request for a peer's addrbook sample.
// Empty payload on the wire — matches Bitcoin's `getaddr`.
type GetPeers struct{}

// Peers is the response to GetPeers (or a one-shot self-advertise on
// outbound connection start; see PIP-0006 §Phase 4).
type Peers struct {
	Entries []PeerEntry
}

// YourAddr reports the observed TCP source of the remote peer back to
// them. Sent once per session immediately after capability negotiation;
// receivers feed these into the external-address quorum (Phase 4).
type YourAddr struct {
	NetworkID uint8
	Addr      []byte
	TCPPort   uint16
}

// PeerEntry is a single advertised peer in a `Peers` message. See package
// doc for KeyType dial semantics and LastSeen clamping rules.
type PeerEntry struct {
	NetworkID uint8
	Addr      []byte
	TCPPort   uint16
	KeyType   uint8
	NodeID    []byte
	LastSeen  uint64
}

// Validation errors — peers are disconnected on any of these.
var (
	ErrEntryAddrLen    = errors.New("disc: PeerEntry address length mismatches NetworkID")
	ErrEntryNodeIDLen  = errors.New("disc: PeerEntry NodeID length mismatches KeyType")
	ErrEntryZeroPort   = errors.New("disc: PeerEntry has zero TCPPort")
	ErrPeersTooLarge   = errors.New("disc: Peers message exceeds MaxPeersPerMessage")
	ErrYourAddrShape   = errors.New("disc: YourAddr malformed")
	ErrNodeIDForbidden = errors.New("disc: PeerEntry NodeID not permitted for KeyType=0x00")
)

// Skippable returns true if e should be silently dropped on ingest
// (unknown NetworkID or KeyType — forward compat) rather than triggering
// a disconnect. Callers that receive (skip=true, err=nil) must not treat
// it as an error; callers that receive (skip=false, err!=nil) MUST
// disconnect the peer.
//
// Tor v2 is skippable: per PIP-0006 we decode but never store or relay.
func (e *PeerEntry) Validate() (skip bool, err error) {
	wantAddrLen := addrLenFor(e.NetworkID)
	if wantAddrLen < 0 {
		// Unknown NetworkID — forward-compat skip.
		return true, nil
	}
	if len(e.Addr) != wantAddrLen {
		return false, fmt.Errorf("%w: net=%d want=%d got=%d", ErrEntryAddrLen, e.NetworkID, wantAddrLen, len(e.Addr))
	}
	if e.TCPPort == 0 {
		return false, ErrEntryZeroPort
	}
	wantNodeIDLen := nodeIDLenFor(e.KeyType)
	if wantNodeIDLen < 0 {
		// Unknown KeyType — forward-compat skip.
		return true, nil
	}
	if len(e.NodeID) != wantNodeIDLen {
		return false, fmt.Errorf("%w: keytype=%d want=%d got=%d", ErrEntryNodeIDLen, e.KeyType, wantNodeIDLen, len(e.NodeID))
	}
	if e.NetworkID == NetTorV2 {
		return true, nil
	}
	return false, nil
}

// Validate on YourAddr applies the same network/address-length rules as
// PeerEntry plus a nonzero-port check. Unknown NetworkID is still
// skippable (peer knows a network tag we don't).
func (y *YourAddr) Validate() (skip bool, err error) {
	wantAddrLen := addrLenFor(y.NetworkID)
	if wantAddrLen < 0 {
		return true, nil
	}
	if len(y.Addr) != wantAddrLen {
		return false, fmt.Errorf("%w: net=%d want=%d got=%d", ErrYourAddrShape, y.NetworkID, wantAddrLen, len(y.Addr))
	}
	// TCPPort == 0 is valid for YourAddr — PIP-0006 says "0 if unknown".
	return false, nil
}

// Validate on Peers enforces the size cap. Per-entry validation is the
// caller's responsibility (they need the skip/disconnect distinction).
func (p *Peers) Validate() error {
	if len(p.Entries) > MaxPeersPerMessage {
		return fmt.Errorf("%w: got=%d max=%d", ErrPeersTooLarge, len(p.Entries), MaxPeersPerMessage)
	}
	return nil
}
