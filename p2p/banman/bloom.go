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

package banman

import (
	"hash/maphash"
	"math"
)

// Discourage Bloom defaults. Mirror Bitcoin Core's discourage filter
// (src/banman.h: CRollingBloomFilter m_discouraged{50000, 0.000001}) —
// 50k distinct addresses with a 1e-6 false-positive rate.
const (
	discourageBloomCap uint    = 50_000
	discourageBloomFP  float64 = 1e-6
)

// rollingBloomFilter is a port of Bitcoin Core's CRollingBloomFilter
// (src/common/bloom.cpp). Unlike a plain Bloom set, it remembers only
// the last nElements to 1.5*nElements inserted entries: entries are
// stamped with a 2-bit generation number, and once a generation fills
// up ((nElements+1)/2 inserts) the oldest of the three generations is
// wiped. That bounds the false-positive rate regardless of uptime — a
// plain set's rate climbs monotonically, and since discouragement
// gates outbound dials as well as inbound accepts, a saturated filter
// (drivable by a distinct-IP flood from one IPv6 /64) would freeze
// the node's connectivity until restart.
//
// Layout matches Core: the bit array is stored as pairs of uint64
// words; logical bit position b of pair p keeps its generation in
// (data[p] bit b, data[p+1] bit b). Generation 0 means empty.
//
// Hashing differs only in primitive: Core uses MurmurHash3 with a
// random tweak, we use Go's stdlib maphash with nHashFuncs
// independent per-process seeds. The filter is in-memory only and
// never serialized, so per-process seed randomness is fine.
type rollingBloomFilter struct {
	data                   []uint64 // pairs of words, ((nFilterBits+63)/64)*2 total
	nHashFuncs             int
	nEntriesPerGeneration  int
	nEntriesThisGeneration int
	nGeneration            int
	seeds                  []maphash.Seed
}

func newRollingBloomFilter(nElements uint, fpRate float64) *rollingBloomFilter {
	if nElements == 0 {
		nElements = 1
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.001
	}
	// Sizing math straight from Core's constructor: k hash funcs for
	// the target rate, then a bit count that keeps the rate when the
	// filter holds its maximum of 3 generations.
	logFpRate := math.Log(fpRate)
	nHashFuncs := int(math.Round(logFpRate / math.Log(0.5)))
	if nHashFuncs > 50 {
		nHashFuncs = 50
	}
	if nHashFuncs < 1 {
		nHashFuncs = 1
	}
	nEntriesPerGeneration := (int(nElements) + 1) / 2
	nMaxElements := nEntriesPerGeneration * 3
	nFilterBits := uint64(math.Ceil(-1.0 * float64(nHashFuncs) * float64(nMaxElements) /
		math.Log(1.0-math.Exp(logFpRate/float64(nHashFuncs)))))
	words := (nFilterBits + 63) / 64
	if words < 1 {
		words = 1
	}
	f := &rollingBloomFilter{
		data:                  make([]uint64, words*2),
		nHashFuncs:            nHashFuncs,
		nEntriesPerGeneration: nEntriesPerGeneration,
		seeds:                 make([]maphash.Seed, nHashFuncs),
	}
	for i := range f.seeds {
		f.seeds[i] = maphash.MakeSeed()
	}
	f.reset()
	return f
}

// Insert adds the bytes-keyed entry, rotating out the oldest
// generation first when the current one is full.
func (f *rollingBloomFilter) Insert(key []byte) {
	if f.nEntriesThisGeneration == f.nEntriesPerGeneration {
		f.nEntriesThisGeneration = 0
		f.nGeneration++
		if f.nGeneration > 3 {
			f.nGeneration = 1
		}
		// Wipe every entry stamped with the incoming generation
		// number — those are the oldest survivors, about to be
		// overwritten by new inserts.
		var genMask1, genMask2 uint64
		if f.nGeneration&1 != 0 {
			genMask1 = ^uint64(0)
		}
		if f.nGeneration>>1 != 0 {
			genMask2 = ^uint64(0)
		}
		for p := 0; p < len(f.data); p += 2 {
			p1, p2 := f.data[p], f.data[p+1]
			mask := (p1 ^ genMask1) | (p2 ^ genMask2)
			f.data[p] = p1 & mask
			f.data[p+1] = p2 & mask
		}
	}
	f.nEntriesThisGeneration++
	for i := 0; i < f.nHashFuncs; i++ {
		h := f.hash(i, key)
		bit, pos := slotOf(h, len(f.data))
		// Stamp the current generation into the bit pair.
		f.data[pos] = (f.data[pos] &^ (1 << bit)) | uint64(f.nGeneration&1)<<bit
		f.data[pos+1] = (f.data[pos+1] &^ (1 << bit)) | uint64(f.nGeneration>>1)<<bit
	}
}

// Contains reports whether the entry is present in any live
// generation (with the configured false-positive rate). Entries older
// than ~1.5*nElements inserts have been rotated out and report false.
func (f *rollingBloomFilter) Contains(key []byte) bool {
	for i := 0; i < f.nHashFuncs; i++ {
		h := f.hash(i, key)
		bit, pos := slotOf(h, len(f.data))
		if (f.data[pos]|f.data[pos+1])>>bit&1 == 0 {
			return false
		}
	}
	return true
}

// slotOf maps one hash value to its (bit, word-pair) slot. The bit
// index takes the low 6 bits; the pair position must come from the
// REMAINING bits — Core gets this independence for free by using
// FastRange32 (high bits) for the position. Deriving both from
// overlapping low bits would collapse the joint (pos, bit) space to
// lcm(words, 64) reachable slots and multiply the effective
// false-positive rate by orders of magnitude.
func slotOf(h uint64, words int) (bit, pos uint64) {
	bit = h & 0x3F
	pos = ((h >> 6) % uint64(words)) &^ 1
	return bit, pos
}

// reset empties the filter and restarts generation numbering.
func (f *rollingBloomFilter) reset() {
	f.nEntriesThisGeneration = 0
	f.nGeneration = 1
	for i := range f.data {
		f.data[i] = 0
	}
}

func (f *rollingBloomFilter) hash(idx int, key []byte) uint64 {
	var h maphash.Hash
	h.SetSeed(f.seeds[idx])
	h.Write(key)
	return h.Sum64()
}
