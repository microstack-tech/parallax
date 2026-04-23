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
	"bytes"
	"testing"

	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// FuzzPeerEntryDecode — arbitrary bytes fed to rlp.Decode(PeerEntry) must
// never panic and the resulting entry must either round-trip through
// Validate() cleanly (skip or err, but no panic) or surface a decode
// error. Covers malformed RLP, oversize fields, and inconsistent lengths.
func FuzzPeerEntryDecode(f *testing.F) {
	// Seed with a few structurally-valid and invalid samples.
	valid := PeerEntry{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeNone, NodeID: []byte{}, LastSeen: 1700000000}
	var vbuf bytes.Buffer
	_ = rlp.Encode(&vbuf, valid)
	f.Add(vbuf.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0xc0})                          // empty RLP list
	f.Add([]byte{0xff, 0xff, 0xff})              // malformed
	f.Add(bytes.Repeat([]byte{0xff}, 100_000))   // oversize
	f.Add(bytes.Repeat([]byte{0x00}, 1_000_000)) // all-zero mega-input

	f.Fuzz(func(t *testing.T, data []byte) {
		var e PeerEntry
		_ = rlp.DecodeBytes(data, &e)
		// Regardless of decode outcome, calling Validate on whatever
		// half-decoded state must not panic.
		_, _ = e.Validate()
	})
}

// FuzzPeersDecode — same, for a full Peers packet. Must handle 0..N
// entries, malformed trailers, and pathological nesting. Any panic here
// is a remote DoS.
func FuzzPeersDecode(f *testing.F) {
	ok := Peers{Entries: []PeerEntry{
		{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeNone},
		{NetworkID: NetIPv6, Addr: bytes.Repeat([]byte{0xab}, 16), TCPPort: 30303, KeyType: KeyTypeNone},
	}}
	var okbuf bytes.Buffer
	_ = rlp.Encode(&okbuf, ok)
	f.Add(okbuf.Bytes())
	f.Add([]byte{0xc0})
	f.Add([]byte{0xc1, 0xc0})
	f.Add(bytes.Repeat([]byte{0x80}, 10_000))

	f.Fuzz(func(t *testing.T, data []byte) {
		var p Peers
		if err := rlp.DecodeBytes(data, &p); err != nil {
			return
		}
		if err := p.Validate(); err != nil {
			return
		}
		for i := range p.Entries {
			_, _ = p.Entries[i].Validate()
		}
	})
}

// FuzzYourAddrDecode — YourAddr is the first post-negotiation message;
// a crash on decode is directly reachable from any peer that can open a
// session. Anti-DoS coverage.
func FuzzYourAddrDecode(f *testing.F) {
	ok := YourAddr{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303}
	var okbuf bytes.Buffer
	_ = rlp.Encode(&okbuf, ok)
	f.Add(okbuf.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		var y YourAddr
		_ = rlp.DecodeBytes(data, &y)
		_, _ = y.Validate()
	})
}
