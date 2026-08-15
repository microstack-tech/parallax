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

package enr

import (
	"bytes"
	"testing"

	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// FuzzENRDecode feeds raw RLP bytes to Record decoding. Records arrive
// from the network (discovery ENRResponse packets, DNS trees), so
// decoding must never panic. When decoding succeeds, re-encoding must
// reproduce the input byte-for-byte (records keep their canonical
// encoding), and the accessors must be safe to call.
func FuzzENRDecode(f *testing.F) {
	// A valid record signed with the package's test identity scheme.
	var r Record
	r.Set(IPv4{127, 0, 0, 1})
	r.Set(UDP(30303))
	if err := signTest([]byte{5}, &r); err != nil {
		f.Fatalf("cannot sign test record: %v", err)
	}
	blob, err := rlp.EncodeToBytes(r)
	if err != nil {
		f.Fatalf("cannot encode test record: %v", err)
	}
	f.Add(blob)

	// A mutated copy with a corrupted signature byte. The signature is
	// the first list element, so flip a byte near the front.
	bad := bytes.Clone(blob)
	bad[3] ^= 0x01
	f.Add(bad)

	// Structural edge cases from TestDecodeIncomplete.
	f.Add([]byte{})
	f.Add([]byte{0xC0})                          // empty list
	f.Add([]byte{0xC1, 0x1})                     // sig only
	f.Add([]byte{0xC2, 0x1, 0x2})                // minimal valid structure
	f.Add([]byte{0xC3, 0x1, 0x2, 0x3})           // dangling key
	f.Add([]byte{0xC5, 0x1, 0x2, 0x3, 0x4, 0x5}) // incomplete pair

	f.Fuzz(func(t *testing.T, data []byte) {
		var rec Record
		if err := rlp.DecodeBytes(data, &rec); err != nil {
			return
		}
		// Accessors must not panic on any successfully decoded record.
		_ = rec.Seq()
		_ = rec.Signature()
		_ = rec.IdentityScheme()

		// Re-encoding must be byte-identical to the input.
		enc, err := rlp.EncodeToBytes(rec)
		if err != nil {
			t.Fatalf("cannot re-encode decoded record: %v", err)
		}
		if !bytes.Equal(enc, data) {
			t.Fatalf("re-encoding is not canonical:\ninput  %x\noutput %x", data, enc)
		}
	})
}
