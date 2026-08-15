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

package bitutil

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/tests/fuzzers/fuzzutil"
)

// FuzzBitutilCompress is the native fuzzing entry point wrapping the legacy
// go-fuzz target, which round-trips the bitset compression codec. The first
// input byte selects the encode or decode direction.
func FuzzBitutilCompress(f *testing.F) {
	fuzzutil.SeedFromDir(f, "corpus")
	f.Add([]byte{0x00})                               // encode path, empty payload
	f.Add([]byte{0x01})                               // decode path, empty payload
	f.Add([]byte{0x00, 0xde, 0xad, 0xbe, 0xef, 0x00}) // encode round-trip
	f.Add([]byte{0x01, 0xde, 0xad, 0xbe, 0xef, 0x00}) // decode round-trip
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
