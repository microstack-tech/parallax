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

// TestRoundTripHello — full struct round-trip with no Tail. Tail
// asymmetry is exercised by TestHelloForwardCompat below: encoding
// an extended struct and decoding into the current one.
func TestRoundTripHello(t *testing.T) {
	in := Hello{
		ProtoVersion: 1,
		Nonce:        0xDEADBEEFCAFEBABE,
		ListenPort:   32110,
		Services:     ServiceNodeNetwork | ServiceRelayTx,
	}
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Hello
	if err := rlp.Decode(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if out.ProtoVersion != in.ProtoVersion || out.Nonce != in.Nonce ||
		out.ListenPort != in.ListenPort || out.Services != in.Services {
		t.Fatalf("round trip altered fields: %+v vs %+v", in, out)
	}
}

// TestRoundTripHelloEmptyTail — common case (no extension fields)
// must round-trip cleanly. The rlp:"tail" decoder accepts an absent
// trailing list as zero-length; assert that.
func TestRoundTripHelloEmptyTail(t *testing.T) {
	in := Hello{ProtoVersion: 1, Nonce: 1, ListenPort: 32110, Services: ServiceNodeNetwork}
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Hello
	if err := rlp.Decode(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if out.ProtoVersion != in.ProtoVersion || out.Nonce != in.Nonce ||
		out.ListenPort != in.ListenPort || out.Services != in.Services {
		t.Fatalf("round trip altered fields: %+v vs %+v", in, out)
	}
	if len(out.Tail) != 0 {
		t.Fatalf("empty Tail decoded as non-empty: %x", out.Tail)
	}
}

// TestHelloForwardCompat — a future Hello with extra trailing fields
// must decode into the current struct without error, with the extra
// bytes captured in Tail. This is the load-bearing wire-evolution
// invariant.
func TestHelloForwardCompat(t *testing.T) {
	type futureHello struct {
		ProtoVersion uint16
		Nonce        uint64
		ListenPort   uint16
		Services     uint32
		FutureField1 uint64
		FutureField2 []byte
	}
	future := futureHello{
		ProtoVersion: 1,
		Nonce:        0xCAFE,
		ListenPort:   32110,
		Services:     ServiceNodeNetwork,
		FutureField1: 0xABCD1234,
		FutureField2: []byte("greetings from v3"),
	}
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, future); err != nil {
		t.Fatal(err)
	}
	var current Hello
	if err := rlp.Decode(&buf, &current); err != nil {
		t.Fatalf("forward-compat decode failed: %v", err)
	}
	if current.ProtoVersion != future.ProtoVersion || current.Nonce != future.Nonce ||
		current.ListenPort != future.ListenPort || current.Services != future.Services {
		t.Fatalf("known fields not preserved: %+v vs source %+v", current, future)
	}
	if len(current.Tail) == 0 {
		t.Fatalf("Tail empty after decoding future-shaped Hello; rlp:\"tail\" should capture extras")
	}
}

// TestHelloValidate — version floor + tail-byte-budget limit.
func TestHelloValidate(t *testing.T) {
	bigTail := []rlp.RawValue{rlp.RawValue(bytes.Repeat([]byte{0xAA}, HelloMaxTailSize+1))}
	smallTail := []rlp.RawValue{rlp.RawValue(bytes.Repeat([]byte{0xAA}, HelloMaxTailSize/2)),
		rlp.RawValue(bytes.Repeat([]byte{0xBB}, HelloMaxTailSize/2))}
	cases := []struct {
		name    string
		h       Hello
		wantErr error
	}{
		{"ok", Hello{ProtoVersion: 1, Nonce: 1, ListenPort: 32110}, nil},
		{"ok-with-tail-at-limit", Hello{ProtoVersion: 1, Tail: smallTail}, nil},
		{"ok-listen-port-zero", Hello{ProtoVersion: 1, ListenPort: 0}, nil},
		{"ok-unknown-services", Hello{ProtoVersion: 1, Services: 0xFFFFFFFF}, nil},
		{"version-zero-fails", Hello{ProtoVersion: 0, Nonce: 1, ListenPort: 32110}, ErrHelloVersion},
		{"tail-budget-exceeded-fails", Hello{ProtoVersion: 1, Tail: bigTail}, ErrHelloTailSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want wrapping %v", err, tc.wantErr)
			}
		})
	}
}

// TestHelloMsgCodeIsThree — pin the wire code so a refactor can't
// silently shift it. Bumping requires intentional protocol change.
func TestHelloMsgCodeIsThree(t *testing.T) {
	if HelloMsg != 0x03 {
		t.Fatalf("HelloMsg = 0x%02x, want 0x03", HelloMsg)
	}
}

// TestHelloServiceFlagsLayout — pin bit positions to match the
// documented Bitcoin-Core-aligned layout. Operators reading peer
// state across nodes depend on this alignment.
func TestHelloServiceFlagsLayout(t *testing.T) {
	if ServiceNodeNetwork != 1<<0 {
		t.Errorf("ServiceNodeNetwork = 0x%x, want 0x1", ServiceNodeNetwork)
	}
	if ServiceNodeBloom != 1<<1 {
		t.Errorf("ServiceNodeBloom = 0x%x, want 0x2", ServiceNodeBloom)
	}
	if ServiceRelayTx != 1<<2 {
		t.Errorf("ServiceRelayTx = 0x%x, want 0x4", ServiceRelayTx)
	}
}
