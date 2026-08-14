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
	"encoding/binary"
	"testing"
)

func bloomKey(i int) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(i))
	return b[:]
}

// The rolling filter must always remember the last nElements inserts —
// the CRollingBloomFilter contract Core's discourage filter relies on.
func TestRollingBloomRecentAlwaysRemembered(t *testing.T) {
	const n = 100
	f := newRollingBloomFilter(n, 1e-6)
	for i := 0; i < 4*n; i++ {
		f.Insert(bloomKey(i))
		lo := i - n + 1
		if lo < 0 {
			lo = 0
		}
		for j := lo; j <= i; j++ {
			if !f.Contains(bloomKey(j)) {
				t.Fatalf("key %d forgotten after insert %d (window %d..%d)", j, i, lo, i)
			}
		}
	}
}

// Old entries must rotate out: a plain (non-rolling) set would keep
// every insert forever and its false-positive rate would climb
// monotonically — the failure mode this filter exists to prevent.
func TestRollingBloomOldEntriesRotateOut(t *testing.T) {
	const n = 100
	f := newRollingBloomFilter(n, 1e-6)
	for i := 0; i < 10*n; i++ {
		f.Insert(bloomKey(i))
	}
	// Everything older than 2*nEntriesPerGeneration inserts ago is
	// guaranteed wiped (three generations, oldest dropped on rotate).
	// With fp=1e-6 a surviving hit is a real bug, not filter noise.
	evicted := 0
	for i := 0; i < 8*n; i++ {
		if !f.Contains(bloomKey(i)) {
			evicted++
		}
	}
	if evicted < 8*n-1 {
		t.Fatalf("only %d of %d old entries rotated out", evicted, 8*n)
	}
}

// The false-positive rate on never-inserted keys must stay near the
// configured target even after sustained insert pressure well past
// capacity — the distinct-IP-flood scenario.
func TestRollingBloomFalsePositiveBound(t *testing.T) {
	const n = 1000
	f := newRollingBloomFilter(n, 1e-3)
	for i := 0; i < 20*n; i++ {
		f.Insert(bloomKey(i))
	}
	hits := 0
	const probes = 20000
	for i := 0; i < probes; i++ {
		if f.Contains(bloomKey((1 << 40) + i)) {
			hits++
		}
	}
	// Allow 10x the target rate for statistical slack: 1e-3 * 20000
	// probes = ~20 expected; a plain saturated set would hit ~100%.
	if hits > 200 {
		t.Fatalf("false-positive hits %d/%d, filter effectively saturated", hits, probes)
	}
}
