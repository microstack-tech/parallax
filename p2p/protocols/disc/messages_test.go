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
	"errors"
	"reflect"
	"testing"

	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

func TestRoundTripGetPeers(t *testing.T) {
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, GetPeers{}); err != nil {
		t.Fatal(err)
	}
	var got GetPeers
	if err := rlp.Decode(&buf, &got); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTripYourAddr(t *testing.T) {
	in := YourAddr{NetworkID: NetIPv4, Addr: []byte{203, 0, 113, 5}, TCPPort: 30303}
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out YourAddr
	if err := rlp.Decode(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch: %+v vs %+v", in, out)
	}
}

func TestRoundTripPeers(t *testing.T) {
	in := Peers{Entries: []PeerEntry{
		{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: KeyTypeNone, NodeID: []byte{}, LastSeen: 1_700_000_000},
		{NetworkID: NetIPv6, Addr: bytes.Repeat([]byte{0xCA}, 16), TCPPort: 30303, KeyType: KeyTypeNone, NodeID: []byte{}, LastSeen: 1_700_000_001},
		{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeSecp256k1, NodeID: bytes.Repeat([]byte{0x99}, 64), LastSeen: 1_700_000_002},
	}}
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Peers
	if err := rlp.Decode(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != len(in.Entries) {
		t.Fatalf("entry count: got %d, want %d", len(out.Entries), len(in.Entries))
	}
	for i := range in.Entries {
		if !reflect.DeepEqual(in.Entries[i], out.Entries[i]) {
			t.Errorf("entry %d round trip: %+v vs %+v", i, in.Entries[i], out.Entries[i])
		}
	}
}

func TestPeerEntryValidate(t *testing.T) {
	cases := []struct {
		name    string
		e       PeerEntry
		wantSkp bool
		wantErr error
	}{
		{"ipv4-v2-ok", PeerEntry{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: KeyTypeNone}, false, nil},
		{"ipv4-legacy-ok", PeerEntry{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: KeyTypeSecp256k1, NodeID: bytes.Repeat([]byte{0x99}, 64)}, false, nil},
		{"unknown-network-skip", PeerEntry{NetworkID: 0xFE, Addr: []byte{0}, TCPPort: 30303}, true, nil},
		{"torv2-skip", PeerEntry{NetworkID: NetTorV2, Addr: bytes.Repeat([]byte{1}, 10), TCPPort: 30303, KeyType: KeyTypeNone}, true, nil},
		{"unknown-keytype-skip", PeerEntry{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: 0xEE}, true, nil},
		{"addr-len-mismatch-disconnect", PeerEntry{NetworkID: NetIPv4, Addr: []byte{1, 2}, TCPPort: 30303, KeyType: KeyTypeNone}, false, ErrEntryAddrLen},
		{"nodeid-len-mismatch-disconnect", PeerEntry{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: KeyTypeSecp256k1, NodeID: []byte{1}}, false, ErrEntryNodeIDLen},
		{"zero-port-disconnect", PeerEntry{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 0, KeyType: KeyTypeNone}, false, ErrEntryZeroPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skip, err := tc.e.Validate()
			if skip != tc.wantSkp {
				t.Errorf("skip = %v, want %v", skip, tc.wantSkp)
			}
			if (err == nil) != (tc.wantErr == nil) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func TestPeersValidateCap(t *testing.T) {
	ok := Peers{Entries: make([]PeerEntry, MaxPeersPerMessage)}
	if err := ok.Validate(); err != nil {
		t.Errorf("at-cap should validate, got %v", err)
	}
	over := Peers{Entries: make([]PeerEntry, MaxPeersPerMessage+1)}
	if err := over.Validate(); err == nil {
		t.Error("over-cap should fail")
	} else if !errors.Is(err, ErrPeersTooLarge) {
		t.Errorf("got %v, want ErrPeersTooLarge", err)
	}
}

func TestYourAddrUnknownPortZeroLegal(t *testing.T) {
	y := YourAddr{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 0}
	if skip, err := y.Validate(); skip || err != nil {
		t.Fatalf("YourAddr with port=0 should be valid: skip=%v err=%v", skip, err)
	}
}
