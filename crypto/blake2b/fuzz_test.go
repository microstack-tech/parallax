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

//go:build amd64 && !gccgo && !appengine

package blake2b

import (
	"encoding/binary"
	"testing"
)

// FuzzF cross-checks the assembly implementations of the blake2b compression
// function against the pure Go one. The EIP-152 precompile calls F directly,
// so a divergence between instruction-set paths would be a consensus split
// between nodes on different hardware.
func FuzzF(f *testing.F) {
	// A zero state, an all-ones state, and a mid-range rounds count.
	f.Add(make([]byte, 211))
	ones := make([]byte, 211)
	for i := range ones {
		ones[i] = 0xff
	}
	f.Add(ones)
	twelve := make([]byte, 211)
	binary.BigEndian.PutUint16(twelve[0:2], 12)
	twelve[210] = 1
	f.Add(twelve)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) != 211 {
			return
		}
		var (
			rounds = binary.BigEndian.Uint16(data[0:2])

			h  [8]uint64
			m  [16]uint64
			c  [2]uint64
			fl uint64
		)
		for i := range 8 {
			offset := 2 + i*8
			h[i] = binary.LittleEndian.Uint64(data[offset : offset+8])
		}
		for i := range 16 {
			offset := 66 + i*8
			m[i] = binary.LittleEndian.Uint64(data[offset : offset+8])
		}
		c[0] = binary.LittleEndian.Uint64(data[194:202])
		c[1] = binary.LittleEndian.Uint64(data[202:210])

		if data[210]%2 == 1 { // Avoid spinning the fuzzer to hit 0/1
			fl = 0xFFFFFFFFFFFFFFFF
		}

		// Run the compression on every instruction set and cross reference.
		want := h
		fGeneric(&want, &m, c[0], c[1], fl, uint64(rounds))

		have := h
		fSSE4(&have, &m, c[0], c[1], fl, uint64(rounds))
		if have != want {
			t.Fatal("SSE4 mismatches generic algo")
		}
		have = h
		fAVX(&have, &m, c[0], c[1], fl, uint64(rounds))
		if have != want {
			t.Fatal("AVX mismatches generic algo")
		}
		have = h
		fAVX2(&have, &m, c[0], c[1], fl, uint64(rounds))
		if have != want {
			t.Fatal("AVX2 mismatches generic algo")
		}
	})
}
