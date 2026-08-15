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

package fees

// Unit tests for txConfirmStats, the port of Bitcoin Core's TxConfirmStats
// class (src/policy/fees.cpp). These tests pin Core-faithful behaviour of the
// low-level machinery: bucket lookup via lower_bound semantics, cumulative
// confAvg recording, the in-block vs evicted removeTx paths, exponential
// decay, circular-buffer rollover, and old-unconfirmed aging.
//
// The higher-level estimator behaviour (success thresholds, horizon
// combination, smart-fee wrapper) is covered by the BlockPolicyEstimates port
// in oracle_smartfee_test.go.

import (
	"math"
	"testing"
)

// testBucketBounds mirrors the bucket construction in NewOracle: bounds spaced
// by feeSpacing (1.05) from minFee to maxFee, with a catch-all +Inf bucket.
func testBucketBounds(minFee, maxFee float64) []float64 {
	var bounds []float64
	for boundary := minFee; boundary <= maxFee; boundary *= feeSpacing {
		bounds = append(bounds, boundary)
	}
	return append(bounds, math.Inf(1))
}

// smallBuckets is a hand-sized bucket set for tests that assert exact indices.
func smallBuckets() []float64 {
	return []float64{100, 200, 300, 400, math.Inf(1)}
}

func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func TestBucketIndex(t *testing.T) {
	s := newTxConfirmStats(smallBuckets(), medBlockPeriods, medDecay, medScale)

	// Core semantics: bucketMap.lower_bound(feerate)->second, i.e. the index
	// of the first bucket bound >= feerate.
	cases := []struct {
		feerate float64
		want    int
	}{
		{0, 0},    // below first bucket
		{50, 0},   // below first bucket
		{100, 0},  // exactly on first boundary
		{101, 1},  // just above first boundary
		{150, 1},  // between buckets
		{200, 1},  // exactly on boundary maps to that bucket
		{250, 2},  // between buckets
		{300, 2},  // exactly on boundary
		{400, 3},  // exactly on last finite boundary
		{401, 4},  // above last finite bound falls into the Inf bucket
		{1e12, 4}, // far above everything falls into the Inf bucket
	}
	for _, c := range cases {
		got := s.bucketIndex(c.feerate)
		if got != c.want {
			t.Errorf("bucketIndex(%g) = %d, want %d", c.feerate, got, c.want)
		}
	}

	// Monotonicity and range over a realistic bucket set: as the feerate
	// sweeps upward the index must never decrease and must stay in range.
	bounds := testBucketBounds(1000, 100000)
	s2 := newTxConfirmStats(bounds, medBlockPeriods, medDecay, medScale)
	prev := -1
	for feerate := 1.0; feerate < 1e7; feerate *= 1.13 {
		idx := s2.bucketIndex(feerate)
		if idx < 0 || idx >= len(bounds) {
			t.Fatalf("bucketIndex(%g) = %d out of range [0, %d)", feerate, idx, len(bounds))
		}
		if idx < prev {
			t.Fatalf("bucketIndex(%g) = %d decreased below previous %d", feerate, idx, prev)
		}
		prev = idx
	}
	if prev != len(bounds)-1 {
		t.Errorf("sweep never reached the Inf bucket: last index %d, want %d", prev, len(bounds)-1)
	}
}

func TestRecordAndEstimate(t *testing.T) {
	// scale 1 so blocks == periods and the k vs k-1 distinction is exact
	// (this is the shortStats configuration in Bitcoin Core).
	bounds := testBucketBounds(1000, 100000)
	s := newTxConfirmStats(bounds, medBlockPeriods, medDecay, 1)

	const (
		n       = 100    // enough txs to clear sufficientTxVal/(1-decay) ~ 20.8
		k       = 5      // every tx confirms in exactly k blocks
		feerate = 5000.0 // sits strictly inside a bucket
	)
	for i := 0; i < n; i++ {
		s.record(k, feerate)
	}

	blockHeight := uint64(100)

	// At target k: all n txs confirmed within k blocks, success rate 100%,
	// so the estimate must succeed and equal the bucket's average feerate,
	// which is exactly feerate since every tx paid the same rate.
	got := s.estimateMedianVal(k, sufficientFeeTxs, successPct, blockHeight)
	if got < 0 {
		t.Fatalf("estimateMedianVal(target=%d) = %g, want success near %g", k, got, feerate)
	}
	if !almostEqual(got, feerate, 1e-9) {
		t.Errorf("estimateMedianVal(target=%d) = %g, want %g", k, got, feerate)
	}

	// At target k-1: no tx confirmed that fast, success rate 0%, so no
	// estimate can be made (-1 per Core's EstimateMedianVal contract).
	if got := s.estimateMedianVal(k-1, sufficientFeeTxs, successPct, blockHeight); got != -1 {
		t.Errorf("estimateMedianVal(target=%d) = %g, want -1 (no tx confirmed that fast)", k-1, got)
	}

	// confAvg recording is cumulative: targets beyond k must also succeed.
	got = s.estimateMedianVal(k+1, sufficientFeeTxs, successPct, blockHeight)
	if !almostEqual(got, feerate, 1e-9) {
		t.Errorf("estimateMedianVal(target=%d) = %g, want %g (cumulative confAvg)", k+1, got, feerate)
	}

	// record with blocksToConfirm < 1 must be ignored (Core returns early).
	before := s.txCtAvg[s.bucketIndex(feerate)]
	s.record(0, feerate)
	s.record(-3, feerate)
	after := s.txCtAvg[s.bucketIndex(feerate)]
	if before != after {
		t.Errorf("record with blocksToConfirm < 1 modified txCtAvg: %g -> %g", before, after)
	}
}

func TestRemoveTxInBlockVsEvicted(t *testing.T) {
	buckets := smallBuckets()

	// --- In-block path: decrement unconfirmed count, never touch failAvg ---
	s := newTxConfirmStats(buckets, 5, medDecay, 1)
	bins := len(s.unconfTxs)

	entry := uint64(100)
	b := s.newTx(entry, 150)
	slot := int(entry) % bins
	if s.unconfTxs[slot][b] != 1 {
		t.Fatalf("unconfTxs[%d][%d] = %d after newTx, want 1", slot, b, s.unconfTxs[slot][b])
	}

	s.removeTx(entry, entry+1, b, true)
	if s.unconfTxs[slot][b] != 0 {
		t.Errorf("unconfTxs[%d][%d] = %d after in-block removeTx, want 0", slot, b, s.unconfTxs[slot][b])
	}
	for i := range s.failAvg {
		if s.failAvg[i][b] != 0 {
			t.Errorf("failAvg[%d][%d] = %g after in-block removal, want 0", i, b, s.failAvg[i][b])
		}
	}

	// Double remove must not underflow or panic.
	s.removeTx(entry, entry+1, b, true)
	if s.unconfTxs[slot][b] != 0 {
		t.Errorf("unconfTxs[%d][%d] = %d after double remove, want 0 (no underflow)", slot, b, s.unconfTxs[slot][b])
	}

	// --- Evicted path: decrement plus failAvg for each full period waited ---
	s2 := newTxConfirmStats(buckets, 12, medDecay, 1)
	b2 := s2.newTx(entry, 150)
	best := entry + 5 // waited 5 blocks, scale 1 => 5 periods
	s2.removeTx(entry, best, b2, false)

	slot2 := int(entry) % len(s2.unconfTxs)
	if s2.unconfTxs[slot2][b2] != 0 {
		t.Errorf("unconfTxs[%d][%d] = %d after evicted removeTx, want 0", slot2, b2, s2.unconfTxs[slot2][b2])
	}
	for i := 0; i < len(s2.failAvg); i++ {
		want := 0.0
		if i < 5 {
			want = 1.0
		}
		if s2.failAvg[i][b2] != want {
			t.Errorf("failAvg[%d][%d] = %g, want %g", i, b2, s2.failAvg[i][b2], want)
		}
	}

	// Evicted before a full period elapsed: no failure recorded (scale 2,
	// waited 1 block < scale).
	s3 := newTxConfirmStats(buckets, 5, medDecay, 2)
	b3 := s3.newTx(entry, 150)
	s3.removeTx(entry, entry+1, b3, false)
	for i := range s3.failAvg {
		if s3.failAvg[i][b3] != 0 {
			t.Errorf("failAvg[%d][%d] = %g for sub-period eviction, want 0", i, b3, s3.failAvg[i][b3])
		}
	}

	// Negative blocksAgo (entry above best seen) must be a no-op.
	s4 := newTxConfirmStats(buckets, 5, medDecay, 1)
	b4 := s4.newTx(200, 150)
	s4.removeTx(200, 100, b4, false)
	slot4 := 200 % len(s4.unconfTxs)
	if s4.unconfTxs[slot4][b4] != 1 {
		t.Errorf("removeTx with negative blocksAgo modified state: unconfTxs = %d, want 1", s4.unconfTxs[slot4][b4])
	}
	for i := range s4.failAvg {
		if s4.failAvg[i][b4] != 0 {
			t.Errorf("removeTx with negative blocksAgo recorded failure at period %d", i)
		}
	}

	// bestSeenHeight == 0 forces blocksAgo to 0 (estimator has seen no
	// blocks yet): removal succeeds without recording a failure.
	s5 := newTxConfirmStats(buckets, 5, medDecay, 1)
	b5 := s5.newTx(0, 150)
	s5.removeTx(0, 0, b5, false)
	if s5.unconfTxs[0][b5] != 0 {
		t.Errorf("removeTx with bestSeenHeight=0 did not decrement: got %d, want 0", s5.unconfTxs[0][b5])
	}
	for i := range s5.failAvg {
		if s5.failAvg[i][b5] != 0 {
			t.Errorf("removeTx with bestSeenHeight=0 recorded failure at period %d", i)
		}
	}
}

func TestDecay(t *testing.T) {
	buckets := smallBuckets()
	s := newTxConfirmStats(buckets, 5, medDecay, 1)

	const (
		feerate = 250.0
		txCount = 8
		k       = 2
	)
	for i := 0; i < txCount; i++ {
		s.record(k, feerate)
	}
	// Manufacture a failure so failAvg decay is exercised too.
	b := s.newTx(10, feerate)
	s.removeTx(10, 13, b, false) // 3 periods of failure

	initTxCt := s.txCtAvg[b]
	initFeeAvg := s.feeRateAvg[b]
	initConf := s.confAvg[k-1][b]
	initFail := s.failAvg[0][b]
	if initTxCt == 0 || initConf == 0 || initFail == 0 {
		t.Fatalf("setup failed: txCtAvg=%g confAvg=%g failAvg=%g", initTxCt, initConf, initFail)
	}

	const n = 25
	for i := 0; i < n; i++ {
		s.updateMovingAverages()
	}

	factor := math.Pow(medDecay, n)
	const eps = 1e-9
	if !almostEqual(s.txCtAvg[b], initTxCt*factor, eps) {
		t.Errorf("txCtAvg = %g after %d decays, want %g", s.txCtAvg[b], n, initTxCt*factor)
	}
	if !almostEqual(s.feeRateAvg[b], initFeeAvg*factor, eps) {
		t.Errorf("feeRateAvg = %g after %d decays, want %g", s.feeRateAvg[b], n, initFeeAvg*factor)
	}
	if !almostEqual(s.confAvg[k-1][b], initConf*factor, eps) {
		t.Errorf("confAvg = %g after %d decays, want %g", s.confAvg[k-1][b], n, initConf*factor)
	}
	if !almostEqual(s.failAvg[0][b], initFail*factor, eps) {
		t.Errorf("failAvg = %g after %d decays, want %g", s.failAvg[0][b], n, initFail*factor)
	}

	// Buckets that never saw a tx must stay at zero.
	other := (b + 1) % len(buckets)
	if s.txCtAvg[other] != 0 {
		t.Errorf("txCtAvg[%d] = %g for untouched bucket, want 0", other, s.txCtAvg[other])
	}
}

func TestClearCurrent(t *testing.T) {
	buckets := smallBuckets()
	s := newTxConfirmStats(buckets, 5, medDecay, 1)
	bins := len(s.unconfTxs)

	// Populate the decay-weighted averages; clearCurrent must not touch them.
	s.record(2, 250)
	s.record(3, 250)
	b := s.bucketIndex(250)
	wantTxCt := s.txCtAvg[b]
	wantFeeAvg := s.feeRateAvg[b]
	wantConf := s.confAvg[1][b]

	// Two unconfirmed txs enter at height 5; the circular buffer wraps back
	// to their slot when the chain reaches height 5+bins.
	entry := uint64(5)
	ub := s.newTx(entry, 150)
	s.newTx(entry, 150)
	slot := int(entry) % bins
	if s.unconfTxs[slot][ub] != 2 {
		t.Fatalf("unconfTxs[%d][%d] = %d, want 2", slot, ub, s.unconfTxs[slot][ub])
	}

	s.clearCurrent(entry + uint64(bins))

	if s.unconfTxs[slot][ub] != 0 {
		t.Errorf("unconfTxs[%d][%d] = %d after clearCurrent, want 0 (slot reset)", slot, ub, s.unconfTxs[slot][ub])
	}
	if s.oldUnconfTxs[ub] != 2 {
		t.Errorf("oldUnconfTxs[%d] = %d after clearCurrent, want 2 (moved to old)", ub, s.oldUnconfTxs[ub])
	}

	// Averages preserved exactly.
	if s.txCtAvg[b] != wantTxCt {
		t.Errorf("txCtAvg = %g after clearCurrent, want %g", s.txCtAvg[b], wantTxCt)
	}
	if s.feeRateAvg[b] != wantFeeAvg {
		t.Errorf("feeRateAvg = %g after clearCurrent, want %g", s.feeRateAvg[b], wantFeeAvg)
	}
	if s.confAvg[1][b] != wantConf {
		t.Errorf("confAvg = %g after clearCurrent, want %g", s.confAvg[1][b], wantConf)
	}

	// Clearing an already-empty slot is a no-op.
	s.clearCurrent(entry + uint64(bins))
	if s.oldUnconfTxs[ub] != 2 {
		t.Errorf("oldUnconfTxs[%d] = %d after second clearCurrent, want 2", ub, s.oldUnconfTxs[ub])
	}
}

func TestOldUnconfirmedAging(t *testing.T) {
	buckets := smallBuckets()
	const maxPeriods = 5
	s := newTxConfirmStats(buckets, maxPeriods, medDecay, 1)
	maxConf := s.getMaxConfirms()
	if maxConf != maxPeriods {
		t.Fatalf("getMaxConfirms() = %d, want %d (scale 1)", maxConf, maxPeriods)
	}

	entry := uint64(10)
	b := s.newTx(entry, 150)

	// Simulate maxConf blocks passing; the rollover at entry+maxConf moves
	// the tx into the old-unconfirmed bucket.
	for h := entry + 1; h <= entry+uint64(maxConf); h++ {
		s.clearCurrent(h)
	}
	if s.oldUnconfTxs[b] != 1 {
		t.Fatalf("oldUnconfTxs[%d] = %d after aging past maxConfirms, want 1", b, s.oldUnconfTxs[b])
	}
	slot := int(entry) % maxConf
	if s.unconfTxs[slot][b] != 0 {
		t.Fatalf("unconfTxs[%d][%d] = %d after aging, want 0", slot, b, s.unconfTxs[slot][b])
	}

	// The aged tx counts toward extraNum, so an otherwise-passing estimate
	// setup at this point must reflect the stuck tx (indirectly verified via
	// removeTx below; the estimator path is covered in oracle_smartfee_test.go).

	// Removing the aged tx must take the oldUnconfTxs path.
	best := entry + uint64(maxConf) // blocksAgo == maxConf >= len(unconfTxs)
	s.removeTx(entry, best, b, false)
	if s.oldUnconfTxs[b] != 0 {
		t.Errorf("oldUnconfTxs[%d] = %d after removeTx, want 0", b, s.oldUnconfTxs[b])
	}
	// Failures capped at len(failAvg) periods.
	for i := 0; i < len(s.failAvg); i++ {
		if s.failAvg[i][b] != 1 {
			t.Errorf("failAvg[%d][%d] = %g, want 1 (evicted after %d blocks)", i, b, s.failAvg[i][b], maxConf)
		}
	}

	// Double remove of an aged tx must not underflow.
	s.removeTx(entry, best, b, false)
	if s.oldUnconfTxs[b] != 0 {
		t.Errorf("oldUnconfTxs[%d] = %d after double remove, want 0 (no underflow)", b, s.oldUnconfTxs[b])
	}
}
