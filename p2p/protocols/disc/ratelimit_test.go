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
	"testing"
	"time"
)

// TestTokenBucketCoreSemantics — Core's m_addr_token_bucket model:
// initial fill 1.0, refill 0.1/s, soft cap 1000. A fresh session gets
// exactly one address through and earns the rest at the refill rate.
func TestTokenBucketCoreSemantics(t *testing.T) {
	tb := newTokenBucket(addrRatePerSecond, addrTokenBucketCap, addrTokenBucketInit)
	now := time.Now()
	// Initial fill 1.0 means exactly one token immediately.
	if !tb.Take(now) {
		t.Fatal("expected initial token")
	}
	if tb.Take(now) {
		t.Fatal("second take at t=0 should fail (initial fill is 1.0, not the cap)")
	}
	// After 9s, rate=0.1/s → still below the 1-token refill threshold.
	if tb.Take(now.Add(9 * time.Second)) {
		t.Error("9s at 0.1/s refill should not yield a full token yet")
	}
	// After 10s, exactly one token accumulated.
	if !tb.Take(now.Add(10 * time.Second)) {
		t.Error("10s at 0.1/s should yield one token")
	}
}

// TestTokenBucketSoftCapAccumulation — an idle session accumulates
// toward the 1000-token soft cap and can then absorb a large honest
// burst; the cap bounds the accumulation.
func TestTokenBucketSoftCapAccumulation(t *testing.T) {
	tb := newTokenBucket(addrRatePerSecond, addrTokenBucketCap, addrTokenBucketInit)
	start := time.Now()
	// 200s idle at 0.1/s → 1 + 20 = 21 tokens.
	at := start.Add(200 * time.Second)
	taken := 0
	for range 40 {
		if tb.Take(at) {
			taken++
		}
	}
	if taken != 21 {
		t.Errorf("after 200s idle: took %d, want 21", taken)
	}
	// A very long idle period is bounded by the soft cap.
	tb2 := newTokenBucket(addrRatePerSecond, addrTokenBucketCap, addrTokenBucketInit)
	at2 := start.Add(1000 * time.Hour)
	taken = 0
	for range 1500 {
		if tb2.Take(at2) {
			taken++
		}
	}
	if taken != int(addrTokenBucketCap) {
		t.Errorf("after long idle: took %d, want the %v cap", taken, addrTokenBucketCap)
	}
}

func TestBloomFilterBasic(t *testing.T) {
	f := &bloomFilter{}
	keys := [][]byte{
		[]byte("addr-1"),
		[]byte("addr-2"),
		[]byte("addr-3"),
	}
	for _, k := range keys {
		if f.Contains(k) {
			t.Errorf("unseen key reported present: %s", k)
		}
	}
	for _, k := range keys {
		f.Add(k)
	}
	for _, k := range keys {
		if !f.Contains(k) {
			t.Errorf("added key not found: %s", k)
		}
	}
}

func TestBloomFilterLowFalsePositiveRate(t *testing.T) {
	f := &bloomFilter{}
	// Add a known 100 keys, then probe 10000 never-added ones and
	// count false positives. We expect ~0.1% with bloomSize=72kbit
	// and 10 hashes at 100-item load; allow 5% safety margin.
	for i := range 100 {
		f.Add([]byte{byte(i), byte(i >> 8), 0x01})
	}
	fps := 0
	for i := range 10_000 {
		key := []byte{byte(i), byte(i >> 8), 0x02}
		if f.Contains(key) {
			fps++
		}
	}
	if fps > 50 {
		t.Errorf("bloom false-positive rate too high: %d/10000", fps)
	}
}

// TestRollingBloomRotates — the known-address filter must keep the
// most recent keys reliably while forgetting old generations, so a
// weeks-long session can't saturate it into a filter whose false
// positives silently stop all relay to the peer.
func TestRollingBloomRotates(t *testing.T) {
	var r rollingBloom
	key := func(i int) []byte {
		return []byte{byte(i >> 16), byte(i >> 8), byte(i), 0xAB}
	}
	const total = 4 * bloomGenerationCap
	for i := 0; i < total; i++ {
		r.Add(key(i))
	}
	// The retention guarantee: at least the last n=5000 (two full
	// generations) inserts are remembered.
	for i := total - 2*bloomGenerationCap; i < total; i++ {
		if !r.Contains(key(i)) {
			t.Fatalf("recent key %d missing from rolling bloom", i)
		}
	}
	// Rotation actually forgets: the earliest generation is gone.
	// Allow for false positives, but the bulk must be absent.
	present := 0
	for i := 0; i < bloomGenerationCap; i++ {
		if r.Contains(key(i)) {
			present++
		}
	}
	if present > bloomGenerationCap/10 {
		t.Fatalf("%d/%d oldest keys still present; rotation is not forgetting", present, bloomGenerationCap)
	}
}
