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

package hexutil

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzHexutilJSON fuzzes the UnmarshalJSON implementations of the hexutil
// JSON wrapper types. The kind byte selects the target type:
//
//	kind%4 == 0: Big
//	kind%4 == 1: Uint64
//	kind%4 == 2: Uint
//	kind%4 == 3: Bytes
//
// Invariants checked per iteration:
//   - no panic on arbitrary input
//   - if UnmarshalJSON succeeds, MarshalJSON (via MarshalText) must succeed
//     and unmarshalling the marshalled form must succeed and yield an equal
//     value (canonical round-trip)
func FuzzHexutilJSON(f *testing.F) {
	seeds := []string{
		// Unquoted forms (not valid JSON strings, must error cleanly).
		`0x0`,
		`0x`,
		``,
		// Quoted variants.
		`"0x0"`,
		`"0x"`,
		`""`,
		// Boundary values.
		`"0xffffffffffffffff"`,
		`"0x1ffffffffffffffff"`,
		// A 300 hex digit value (way past the 256 bit limit of Big).
		`"0x` + strings.Repeat("f", 300) + `"`,
		// Leading zero cases.
		`"0x0400"`,
		`"0x00"`,
		`"0x012"`,
		// Odd length and bad nibbles.
		`"0xfff"`,
		`"0xzz"`,
		`"0X12"`,
	}
	for _, s := range seeds {
		for kind := uint8(0); kind < 4; kind++ {
			f.Add([]byte(s), kind)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte, kind uint8) {
		switch kind % 4 {
		case 0:
			var v Big
			if err := v.UnmarshalJSON(data); err != nil {
				return
			}
			enc, err := v.MarshalText()
			if err != nil {
				t.Fatalf("Big: marshal after successful unmarshal of %q failed: %v", data, err)
			}
			var v2 Big
			if err := v2.UnmarshalJSON(quote(enc)); err != nil {
				t.Fatalf("Big: re-unmarshal of %q (from %q) failed: %v", enc, data, err)
			}
			if v.ToInt().Cmp(v2.ToInt()) != 0 {
				t.Fatalf("Big: round-trip mismatch for %q: %s != %s", data, v.String(), v2.String())
			}
		case 1:
			var v Uint64
			if err := v.UnmarshalJSON(data); err != nil {
				return
			}
			enc, err := v.MarshalText()
			if err != nil {
				t.Fatalf("Uint64: marshal after successful unmarshal of %q failed: %v", data, err)
			}
			var v2 Uint64
			if err := v2.UnmarshalJSON(quote(enc)); err != nil {
				t.Fatalf("Uint64: re-unmarshal of %q (from %q) failed: %v", enc, data, err)
			}
			if v != v2 {
				t.Fatalf("Uint64: round-trip mismatch for %q: %d != %d", data, v, v2)
			}
		case 2:
			var v Uint
			if err := v.UnmarshalJSON(data); err != nil {
				return
			}
			enc, err := v.MarshalText()
			if err != nil {
				t.Fatalf("Uint: marshal after successful unmarshal of %q failed: %v", data, err)
			}
			var v2 Uint
			if err := v2.UnmarshalJSON(quote(enc)); err != nil {
				t.Fatalf("Uint: re-unmarshal of %q (from %q) failed: %v", enc, data, err)
			}
			if v != v2 {
				t.Fatalf("Uint: round-trip mismatch for %q: %d != %d", data, v, v2)
			}
		case 3:
			var v Bytes
			if err := v.UnmarshalJSON(data); err != nil {
				return
			}
			enc, err := v.MarshalText()
			if err != nil {
				t.Fatalf("Bytes: marshal after successful unmarshal of %q failed: %v", data, err)
			}
			var v2 Bytes
			if err := v2.UnmarshalJSON(quote(enc)); err != nil {
				t.Fatalf("Bytes: re-unmarshal of %q (from %q) failed: %v", enc, data, err)
			}
			if !bytes.Equal(v, v2) {
				t.Fatalf("Bytes: round-trip mismatch for %q: %x != %x", data, []byte(v), []byte(v2))
			}
		}
	})
}

// quote wraps marshalled text in JSON string quotes, matching what
// encoding/json does with an encoding.TextMarshaler value.
func quote(text []byte) []byte {
	out := make([]byte, 0, len(text)+2)
	out = append(out, '"')
	out = append(out, text...)
	out = append(out, '"')
	return out
}
