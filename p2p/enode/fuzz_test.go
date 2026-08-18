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

package enode

import (
	"bytes"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/p2p/enr"
	"github.com/ParallaxProtocol/parallax/v2/primitives/rlp"
)

// FuzzParseV4URL feeds arbitrary strings to ParseV4. Node URLs come from
// config files, command line flags and RPC calls, so parsing must never
// panic. When parsing succeeds, the node's URLv4 form must parse back to
// a node with the same ID.
//
// DNS lookups are stubbed out by the init function in urlv4_test.go, so
// hostname inputs never touch the network.
func FuzzParseV4URL(f *testing.F) {
	// Valid URLs covering complete, incomplete, IPv6 and discport forms.
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@127.0.0.1:52150")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@[::]:52150")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@[2001:db8:3c4d:15::abcd:ef12]:52150")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@127.0.0.1:52150?discport=22334")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439")
	f.Add("1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@node.example.org:3")

	// Invalid inputs exercising the error paths.
	f.Add("")
	f.Add("enode://01010101@123.124.125.126:3")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@127.0.0.1:foo")
	f.Add("enode://1dd9d65c4552b5eb43d5ad55a2ee3f56c6cbc1c64a5c8d659f51fcd51bace24351232b8d7821617d2b29b54b81cdefb9b3e9c37d7fd5f63270bcc9e1a6f6a439@127.0.0.1:3?discport=foo")
	f.Add("http://foobar")
	f.Add("://foo")

	f.Fuzz(func(t *testing.T, rawurl string) {
		n, err := ParseV4(rawurl)
		if err != nil {
			return
		}
		reparsed, err := ParseV4(n.URLv4())
		if err != nil {
			t.Fatalf("URLv4 output %q of accepted input %q does not reparse: %v", n.URLv4(), rawurl, err)
		}
		if reparsed.ID() != n.ID() {
			t.Fatalf("node ID changed in URL round trip: got %v, want %v (input %q)", reparsed.ID(), n.ID(), rawurl)
		}
	})
}

// FuzzSignedRecordDecode exercises the identity scheme verification path
// on mutated signed-record bytes. Records with invalid signatures must be
// rejected with an error, never a panic. Records that verify must survive
// an encode/decode/verify round trip with the same node ID.
func FuzzSignedRecordDecode(f *testing.F) {
	var r enr.Record
	r.Set(enr.IPv4{127, 0, 0, 1})
	r.Set(enr.UDP(30303))
	if err := SignV4(&r, privkey); err != nil {
		f.Fatalf("cannot sign record: %v", err)
	}
	blob, err := rlp.EncodeToBytes(r)
	if err != nil {
		f.Fatalf("cannot encode record: %v", err)
	}
	f.Add(blob)

	// Corrupt bytes inside the signature (the first list element) so the
	// record still decodes but signature verification must fail.
	for _, i := range []int{3, 20, 60} {
		bad := bytes.Clone(blob)
		bad[i] ^= 0x01
		f.Add(bad)
	}
	// Truncations and structural garbage.
	f.Add(blob[:len(blob)/2])
	f.Add([]byte{})
	f.Add([]byte{0xC0})

	f.Fuzz(func(t *testing.T, data []byte) {
		var rec enr.Record
		if err := rlp.DecodeBytes(data, &rec); err != nil {
			return
		}
		n, err := New(ValidSchemes, &rec)
		if err != nil {
			// Verification rejected the record; an error (not a panic)
			// is the expected outcome for invalid signatures.
			return
		}
		// A verified record must round trip with a stable node ID.
		enc, err := rlp.EncodeToBytes(n.Record())
		if err != nil {
			t.Fatalf("cannot re-encode verified record: %v", err)
		}
		var rec2 enr.Record
		if err := rlp.DecodeBytes(enc, &rec2); err != nil {
			t.Fatalf("cannot re-decode verified record: %v", err)
		}
		n2, err := New(ValidSchemes, &rec2)
		if err != nil {
			t.Fatalf("re-decoded record fails verification: %v", err)
		}
		if n2.ID() != n.ID() {
			t.Fatalf("node ID changed in round trip: got %v, want %v", n2.ID(), n.ID())
		}
	})
}
