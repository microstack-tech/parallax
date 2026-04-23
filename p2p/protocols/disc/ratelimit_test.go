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

func TestTokenBucketBurstThenRefill(t *testing.T) {
	tb := newTokenBucket(outboundRate, outboundBurst)
	start := time.Now()
	// Should allow ~burst tokens instantly.
	taken := 0
	for range 20 {
		if tb.Take(start) {
			taken++
		}
	}
	if float64(taken) < outboundBurst-0.5 || float64(taken) > outboundBurst+0.5 {
		t.Errorf("burst: took %d, want ~%.0f", taken, outboundBurst)
	}
	// After 1 second, rate=1/s means exactly 1 additional token.
	if !tb.Take(start.Add(time.Second)) {
		t.Error("expected token after 1s refill")
	}
	if tb.Take(start.Add(time.Second)) {
		t.Error("bucket drained but Take returned true")
	}
}

func TestTokenBucketInboundSlow(t *testing.T) {
	tb := newTokenBucket(inboundRate, inboundBurst)
	now := time.Now()
	// Burst=1 means exactly one token immediately.
	if !tb.Take(now) {
		t.Fatal("expected initial burst token")
	}
	if tb.Take(now) {
		t.Fatal("second take on burst=1 at t=0 should fail")
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
