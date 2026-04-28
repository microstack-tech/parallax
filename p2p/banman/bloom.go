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

// Discourage Bloom defaults. Mirror Bitcoin Core's
// CRollingBloomFilter sized for misbehavior tracking
// (src/common/bloom.h DEFAULT_BLOOM_FILTER_PARAMETERS) — 50k
// distinct addresses with a 1e-6 false-positive rate.
const (
	discourageBloomCap uint = 50_000
	discourageBloomFP       = 1e-6
)

// bloomFilter is a non-cryptographic Bloom set. We use Go's stdlib
// maphash with k independent seeds to produce k bit positions per
// item. maphash is fast and stable for the duration of one
// process — the filter is in-memory only and never serialized, so
// per-process seed randomness is fine.
//
// Sizing math (standard Bloom):
//
//	m = -n * ln(p) / (ln(2) ^ 2)   // bit-array size
//	k = (m / n) * ln(2)             // hash functions
//
// where n = capacity, p = target false-positive rate.
//
// Membership: Insert flips k bits. Contains tests them; all-set
// → present (with fp). Reset never — ephemeral by design (Bitcoin
// Core resets the rolling-bloom variant; we use a non-rolling
// shape because the filter is restart-cleared anyway).
type bloomFilter struct {
	bits  []uint64 // m bits total, 64 per uint64
	mBits uint64   // exact bit count (bits len * 64 may be >= m)
	k     uint64   // hash function count
	seeds []maphash.Seed
}

func newBloomFilter(n uint, p float64) *bloomFilter {
	if n == 0 {
		n = 1
	}
	if p <= 0 || p >= 1 {
		p = 0.001
	}
	mBits := uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
	if mBits < 64 {
		mBits = 64
	}
	k := uint64(math.Ceil(float64(mBits) / float64(n) * math.Ln2))
	if k < 1 {
		k = 1
	}
	if k > 32 {
		// Cap k to keep insert/lookup cheap. 32 hashes at 1e-12 fp is
		// already overkill for our 1e-6 target.
		k = 32
	}
	bf := &bloomFilter{
		bits:  make([]uint64, (mBits+63)/64),
		mBits: mBits,
		k:     k,
		seeds: make([]maphash.Seed, k),
	}
	// maphash.MakeSeed is process-stable but per-call random. The k
	// seeds are independent, which is what we want — different hash
	// functions for the k bit positions.
	for i := range bf.seeds {
		bf.seeds[i] = maphash.MakeSeed()
	}
	return bf
}

// Insert adds the bytes-keyed entry to the filter.
func (f *bloomFilter) Insert(key []byte) {
	for i := uint64(0); i < f.k; i++ {
		bit := f.hashBit(i, key)
		f.bits[bit/64] |= 1 << (bit % 64)
	}
}

// Contains reports whether the entry has been inserted (with the
// configured false-positive rate).
func (f *bloomFilter) Contains(key []byte) bool {
	for i := uint64(0); i < f.k; i++ {
		bit := f.hashBit(i, key)
		if f.bits[bit/64]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

func (f *bloomFilter) hashBit(idx uint64, key []byte) uint64 {
	var h maphash.Hash
	h.SetSeed(f.seeds[idx])
	h.Write(key)
	return h.Sum64() % f.mBits
}
