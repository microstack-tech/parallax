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

package snap

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/util"
)

// Tests that given a starting hash and a density, the hash ranger can correctly
// split up the remaining hash space into a fixed number of chunks.
func TestHashRanges(t *testing.T) {
	tests := []struct {
		head   util.Hash
		chunks uint64
		starts []util.Hash
		ends   []util.Hash
	}{
		// Simple test case to split the entire hash range into 4 chunks
		{
			head:   util.Hash{},
			chunks: 4,
			starts: []util.Hash{
				{},
				util.HexToHash("0x4000000000000000000000000000000000000000000000000000000000000000"),
				util.HexToHash("0x8000000000000000000000000000000000000000000000000000000000000000"),
				util.HexToHash("0xc000000000000000000000000000000000000000000000000000000000000000"),
			},
			ends: []util.Hash{
				util.HexToHash("0x3fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
				util.HexToHash("0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
				util.HexToHash("0xbfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
				util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			},
		},
		// Split a divisible part of the hash range up into 2 chunks
		{
			head:   util.HexToHash("0x2000000000000000000000000000000000000000000000000000000000000000"),
			chunks: 2,
			starts: []util.Hash{
				{},
				util.HexToHash("0x9000000000000000000000000000000000000000000000000000000000000000"),
			},
			ends: []util.Hash{
				util.HexToHash("0x8fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
				util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			},
		},
		// Split the entire hash range into a non divisible 3 chunks
		{
			head:   util.Hash{},
			chunks: 3,
			starts: []util.Hash{
				{},
				util.HexToHash("0x5555555555555555555555555555555555555555555555555555555555555556"),
				util.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaac"),
			},
			ends: []util.Hash{
				util.HexToHash("0x5555555555555555555555555555555555555555555555555555555555555555"),
				util.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"),
				util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			},
		},
		// Split a part of hash range into a non divisible 3 chunks
		{
			head:   util.HexToHash("0x2000000000000000000000000000000000000000000000000000000000000000"),
			chunks: 3,
			starts: []util.Hash{
				{},
				util.HexToHash("0x6aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"),
				util.HexToHash("0xb555555555555555555555555555555555555555555555555555555555555556"),
			},
			ends: []util.Hash{
				util.HexToHash("0x6aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
				util.HexToHash("0xb555555555555555555555555555555555555555555555555555555555555555"),
				util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			},
		},
		// Split a part of hash range into a non divisible 3 chunks, but with a
		// meaningful space size for manual verification.
		//   - The head being 0xff...f0, we have 14 hashes left in the space
		//   - Chunking up 14 into 3 pieces is 4.(6), but we need the ceil of 5 to avoid a micro-last-chunk
		//   - Since the range is not divisible, the last interval will be shrter, capped at 0xff...f
		//   - The chunk ranges thus needs to be [..0, ..5], [..6, ..b], [..c, ..f]
		{
			head:   util.HexToHash("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff0"),
			chunks: 3,
			starts: []util.Hash{
				{},
				util.HexToHash("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff6"),
				util.HexToHash("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffc"),
			},
			ends: []util.Hash{
				util.HexToHash("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff5"),
				util.HexToHash("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffb"),
				util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			},
		},
	}
	for i, tt := range tests {
		r := newHashRange(tt.head, tt.chunks)

		var (
			starts = []util.Hash{{}}
			ends   = []util.Hash{r.End()}
		)
		for r.Next() {
			starts = append(starts, r.Start())
			ends = append(ends, r.End())
		}
		if len(starts) != len(tt.starts) {
			t.Errorf("test %d: starts count mismatch: have %d, want %d", i, len(starts), len(tt.starts))
		}
		for j := 0; j < len(starts) && j < len(tt.starts); j++ {
			if starts[j] != tt.starts[j] {
				t.Errorf("test %d, start %d: hash mismatch: have %x, want %x", i, j, starts[j], tt.starts[j])
			}
		}
		if len(ends) != len(tt.ends) {
			t.Errorf("test %d: ends count mismatch: have %d, want %d", i, len(ends), len(tt.ends))
		}
		for j := 0; j < len(ends) && j < len(tt.ends); j++ {
			if ends[j] != tt.ends[j] {
				t.Errorf("test %d, end %d: hash mismatch: have %x, want %x", i, j, ends[j], tt.ends[j])
			}
		}
	}
}
