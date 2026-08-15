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
	"reflect"
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

// FuzzMessagesRoundTrip — covers the disc messages the fuzzers above do
// not: Hello is the only one with a payload (GetPeers is empty on the
// wire). Arbitrary bytes fed to rlp.Decode(Hello) must never panic;
// Validate on the half-decoded state must never panic; and any Hello
// that decoded successfully must re-encode and decode back to an equal
// value, Tail included.
func FuzzMessagesRoundTrip(f *testing.F) {
	ok := Hello{ProtoVersion: 1, Nonce: 0xdeadbeef, ListenPort: 30303, Services: ServiceNodeNetwork | ServiceRelayTx}
	var okbuf bytes.Buffer
	_ = rlp.Encode(&okbuf, &ok)
	f.Add(okbuf.Bytes())
	tailed := Hello{ProtoVersion: 2, Nonce: 1, ListenPort: 0, Services: 0,
		Tail: []rlp.RawValue{{0x01}, {0x83, 0xaa, 0xbb, 0xcc}}}
	var tbuf bytes.Buffer
	_ = rlp.Encode(&tbuf, &tailed)
	f.Add(tbuf.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0xc0})                        // empty RLP list
	f.Add([]byte{0xff, 0xff, 0xff})            // malformed
	f.Add(bytes.Repeat([]byte{0x80}, 10_000))  // oversize tail spam
	f.Add(bytes.Repeat([]byte{0x00}, 100_000)) // all-zero mega-input

	f.Fuzz(func(t *testing.T, data []byte) {
		var h Hello
		if err := rlp.DecodeBytes(data, &h); err != nil {
			return
		}
		_ = h.Validate()
		var buf bytes.Buffer
		if err := rlp.Encode(&buf, &h); err != nil {
			t.Fatalf("re-encode of successfully decoded Hello failed: %v", err)
		}
		var back Hello
		if err := rlp.DecodeBytes(buf.Bytes(), &back); err != nil {
			t.Fatalf("re-decode of re-encoded Hello failed: %v", err)
		}
		if !reflect.DeepEqual(h, back) {
			t.Fatalf("Hello round-trip mismatch:\n first=%+v\nsecond=%+v", h, back)
		}
		_ = back.Validate()
	})
}
