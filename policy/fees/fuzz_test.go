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

import (
	"math"
	"testing"
)

// fuzzTx is the shadow record of one tracked unconfirmed transaction, so the
// op stream can reference plausible entry heights and bucket indices the way
// the mempool bookkeeping in the Oracle does.
type fuzzTx struct {
	entryHeight uint64
	bucketIdx   int
	feerate     float64
}

// FuzzTxConfirmStatsOps — feeds arbitrary operation sequences into a
// txConfirmStats instance (the port of Bitcoin Core's TxConfirmStats) and
// asserts structural invariants survive after every op:
//
//   - No panic on any interleaving, including redundant removals and
//     negative-blocksAgo removals that must hit the guards.
//   - No NaN or Inf ever appears in txCtAvg, feeRateAvg, confAvg, or failAvg,
//     and none of them go negative.
//   - Unconfirmed counters (unconfTxs, oldUnconfTxs) never go negative.
//   - estimateMedianVal returns either -1 (no estimate) or a finite value
//     inside [first bucket bound, last finite bucket bound]; the harness only
//     feeds feerates in that range, so any escape is a real bug.
//
// The op stream is consumed in (opcode, data) byte pairs; the opcode's low
// three bits select the operation and the data byte parameterises it. The
// seed selects the bucket set and block-grouping scale; decay and period
// count are the real production constants.
func FuzzTxConfirmStatsOps(f *testing.F) {
	// newTx, newTx, advance, in-block remove, record, estimate, decay, double remove.
	f.Add(uint8(0), []byte{0, 128, 0, 200, 4, 0, 1, 0, 3, 5, 6, 10, 5, 0, 7, 0})
	// newTx, two block rolls, evicted remove, estimate, negative-blocksAgo remove.
	f.Add(uint8(3), []byte{0, 255, 4, 0, 4, 0, 2, 0, 6, 2, 7, 1})
	// Small buckets, scale 2, aging past maxConfirms then removal via oldUnconfTxs.
	f.Add(uint8(7), []byte{0, 30, 4, 0, 4, 0, 4, 0, 4, 0, 4, 0, 4, 0, 2, 0, 6, 47, 5, 0})
	f.Add(uint8(255), []byte{})

	f.Fuzz(func(t *testing.T, seed uint8, ops []byte) {
		if len(ops) > 256 {
			ops = ops[:256]
		}

		// Configuration derived from the seed: bucket set (realistic
		// 1.05-spaced bounds with the Inf catch-all, or the hand-sized set)
		// and scale vary; medDecay/medBlockPeriods are the real constants.
		bounds := testBucketBounds(1000, 100000)
		if seed&1 != 0 {
			bounds = smallBuckets()
		}
		scale := 1
		if seed&2 != 0 {
			scale = medScale
		}
		s := newTxConfirmStats(bounds, medBlockPeriods, medDecay, scale)

		minFee := bounds[0]
		maxFee := bounds[len(bounds)-2] // last finite bound

		// feerateFor maps a data byte onto [minFee, maxFee] so every recorded
		// feerate stays inside the finite bucket range and the
		// estimateMedianVal bound check below is exact.
		feerateFor := func(b byte) float64 {
			return minFee + (maxFee-minFee)*float64(b)/255
		}

		height := uint64(seed) // starting chain height
		var live []fuzzTx      // tracked unconfirmed txs
		var dead []fuzzTx      // already-removed txs, for redundant-remove ops

		i := 0
		next := func() byte {
			if i >= len(ops) {
				return 0
			}
			b := ops[i]
			i++
			return b
		}

		for i < len(ops) {
			opcode := next() % 8
			data := next() // zero once the stream is exhausted

			switch opcode {
			case 0: // newTx enters the mempool at the current height.
				fr := feerateFor(data)
				b := s.newTx(height, fr)
				live = append(live, fuzzTx{entryHeight: height, bucketIdx: b, feerate: fr})

			case 1: // removeTx(inBlock=true): a tracked tx confirms.
				if len(live) > 0 {
					j := int(data) % len(live)
					tx := live[j]
					live = append(live[:j], live[j+1:]...)
					s.removeTx(tx.entryHeight, height, tx.bucketIdx, true)
					dead = append(dead, tx)
				}

			case 2: // removeTx(inBlock=false): a tracked tx is evicted.
				if len(live) > 0 {
					j := int(data) % len(live)
					tx := live[j]
					live = append(live[:j], live[j+1:]...)
					s.removeTx(tx.entryHeight, height, tx.bucketIdx, false)
					dead = append(dead, tx)
				}

			case 3: // record a confirmation; -1 and 0 exercise the <1 guard.
				blocksToConfirm := int(data)%(s.getMaxConfirms()+3) - 1
				s.record(blocksToConfirm, feerateFor(data))

			case 4: // advance the chain one block and roll the circular buffer.
				height++
				s.clearCurrent(height)

			case 5: // apply exponential decay.
				s.updateMovingAverages()

			case 6: // estimate; targets past getMaxConfirms probe the period guard.
				target := 1 + int(data)%(s.getMaxConfirms()+4)
				got := s.estimateMedianVal(target, sufficientFeeTxs, successPct, height)
				if got != -1 {
					if math.IsNaN(got) || math.IsInf(got, 0) {
						t.Fatalf("estimateMedianVal(target=%d) = %g, want -1 or finite", target, got)
					}
					const slack = 1e-6
					if got < minFee*(1-slack) || got > maxFee*(1+slack) {
						t.Fatalf("estimateMedianVal(target=%d) = %g outside bucket range [%g, %g]",
							target, got, minFee, maxFee)
					}
				}

			case 7: // guard exercises: redundant remove / negative blocksAgo.
				if data&1 == 0 && len(dead) > 0 {
					// Double remove of an already-removed tx must not
					// underflow the counters.
					tx := dead[int(data)%len(dead)]
					s.removeTx(tx.entryHeight, height, tx.bucketIdx, data&2 == 0)
				} else {
					// Entry height above best seen: blocksAgo is negative
					// (or clamped to 0 when height is 0) and must be safe.
					s.removeTx(height+1+uint64(data), height, int(data)%len(bounds), false)
				}
			}

			checkStatsInvariants(t, s)
		}
	})
}

// checkStatsInvariants walks every counter array in the stats instance and
// fails the test if any decay-weighted average is NaN, Inf, or negative, or
// if any unconfirmed counter has gone negative.
func checkStatsInvariants(t *testing.T, s *txConfirmStats) {
	t.Helper()

	checkFloat := func(name string, i, j int, v float64) {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			t.Fatalf("%s[%d][%d] = %g, want finite and non-negative", name, i, j, v)
		}
	}

	for j := range s.buckets {
		checkFloat("txCtAvg", 0, j, s.txCtAvg[j])
		checkFloat("feeRateAvg", 0, j, s.feeRateAvg[j])
		if s.oldUnconfTxs[j] < 0 {
			t.Fatalf("oldUnconfTxs[%d] = %d, want >= 0", j, s.oldUnconfTxs[j])
		}
	}
	for i := range s.confAvg {
		for j := range s.buckets {
			checkFloat("confAvg", i, j, s.confAvg[i][j])
			checkFloat("failAvg", i, j, s.failAvg[i][j])
		}
	}
	for i := range s.unconfTxs {
		for j := range s.buckets {
			if s.unconfTxs[i][j] < 0 {
				t.Fatalf("unconfTxs[%d][%d] = %d, want >= 0", i, j, s.unconfTxs[i][j])
			}
		}
	}
}
