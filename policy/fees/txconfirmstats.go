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
	"sort"

	"github.com/ParallaxProtocol/parallax/logging"
)

// txConfirmStats tracks transaction confirmation statistics for one time horizon.
// This is a direct port of Bitcoin Core's TxConfirmStats class.
type txConfirmStats struct {
	// Bucket upper bounds (shared reference, not owned).
	buckets []float64

	// For each bucket X:
	// txCtAvg[X] = decay-weighted count of total txs in bucket X.
	txCtAvg []float64
	// confAvg[Y][X] = decay-weighted count of txs in bucket X confirmed within Y+1 periods.
	confAvg [][]float64
	// failAvg[Y][X] = decay-weighted count of txs in bucket X that failed after Y+1 periods.
	failAvg [][]float64
	// feeRateAvg[X] = decay-weighted sum of fee rates in bucket X (for computing average).
	feeRateAvg []float64

	decay float64
	scale int // block grouping factor (1 for short, 2 for medium, 24 for long)

	// In-memory unconfirmed tracking (circular buffer).
	// unconfTxs[blockHeight % maxConfirms][bucket] = count of unconfirmed txs.
	unconfTxs [][]int
	// Transactions unconfirmed for longer than maxConfirms.
	oldUnconfTxs []int
}

// newTxConfirmStats creates a new TxConfirmStats instance.
// maxPeriods is the number of periods to track (e.g., 12, 24, 42).
// decay is the per-block exponential decay factor.
// scale is the block grouping factor.
func newTxConfirmStats(buckets []float64, maxPeriods int, decay float64, scale int) *txConfirmStats {
	numBuckets := len(buckets)
	s := &txConfirmStats{
		buckets:    buckets,
		txCtAvg:    make([]float64, numBuckets),
		feeRateAvg: make([]float64, numBuckets),
		decay:      decay,
		scale:      scale,
	}

	s.confAvg = make([][]float64, maxPeriods)
	s.failAvg = make([][]float64, maxPeriods)
	for i := 0; i < maxPeriods; i++ {
		s.confAvg[i] = make([]float64, numBuckets)
		s.failAvg[i] = make([]float64, numBuckets)
	}

	maxConfirms := scale * maxPeriods
	s.unconfTxs = make([][]int, maxConfirms)
	for i := 0; i < maxConfirms; i++ {
		s.unconfTxs[i] = make([]int, numBuckets)
	}
	s.oldUnconfTxs = make([]int, numBuckets)

	return s
}

// getMaxConfirms returns the maximum number of blocks this stats instance tracks.
func (s *txConfirmStats) getMaxConfirms() int {
	return s.scale * len(s.confAvg)
}

// bucketIndex returns the bucket index for a given fee rate using binary search.
// Equivalent to bucketMap.lower_bound(feerate)->second in Bitcoin Core.
func (s *txConfirmStats) bucketIndex(feerate float64) int {
	idx := sort.SearchFloat64s(s.buckets, feerate)
	if idx >= len(s.buckets) {
		idx = len(s.buckets) - 1
	}
	return idx
}

// clearCurrent rolls the circular buffer for unconfirmed txs.
// Called at the beginning of each new block.
func (s *txConfirmStats) clearCurrent(blockHeight uint64) {
	slot := int(blockHeight) % len(s.unconfTxs)
	for j := 0; j < len(s.buckets); j++ {
		s.oldUnconfTxs[j] += s.unconfTxs[slot][j]
		s.unconfTxs[slot][j] = 0
	}
}

// record records a confirmed transaction.
// blocksToConfirm is 1-based (minimum value is 1).
func (s *txConfirmStats) record(blocksToConfirm int, feerate float64) {
	if blocksToConfirm < 1 {
		return
	}
	periodsToConfirm := (blocksToConfirm + s.scale - 1) / s.scale
	bucketIdx := s.bucketIndex(feerate)
	for i := periodsToConfirm; i <= len(s.confAvg); i++ {
		s.confAvg[i-1][bucketIdx]++
	}
	s.txCtAvg[bucketIdx]++
	s.feeRateAvg[bucketIdx] += feerate
}

// newTx records a new transaction entering the mempool.
// Returns the bucket index.
func (s *txConfirmStats) newTx(blockHeight uint64, feerate float64) int {
	bucketIdx := s.bucketIndex(feerate)
	slot := int(blockHeight) % len(s.unconfTxs)
	s.unconfTxs[slot][bucketIdx]++
	return bucketIdx
}

// removeTx removes a transaction from unconfirmed tracking.
// If inBlock is false and the tx waited long enough, it counts as a failure.
func (s *txConfirmStats) removeTx(entryHeight, bestSeenHeight uint64, bucketIdx int, inBlock bool) {
	blocksAgo := int(bestSeenHeight) - int(entryHeight)
	if bestSeenHeight == 0 {
		blocksAgo = 0
	}
	if blocksAgo < 0 {
		logging.Debug("Fee estimator: blocksAgo is negative, ignoring", "entryHeight", entryHeight, "bestSeenHeight", bestSeenHeight)
		return
	}

	maxConf := len(s.unconfTxs)
	if blocksAgo >= maxConf {
		if s.oldUnconfTxs[bucketIdx] > 0 {
			s.oldUnconfTxs[bucketIdx]--
		}
	} else {
		slot := int(entryHeight) % maxConf
		if s.unconfTxs[slot][bucketIdx] > 0 {
			s.unconfTxs[slot][bucketIdx]--
		}
	}

	// Count as failure if not confirmed and waited at least one full period.
	if !inBlock && blocksAgo >= s.scale {
		periodsAgo := blocksAgo / s.scale
		for i := 0; i < periodsAgo && i < len(s.failAvg); i++ {
			s.failAvg[i][bucketIdx]++
		}
	}
}

// updateMovingAverages applies exponential decay to all counters.
func (s *txConfirmStats) updateMovingAverages() {
	for j := 0; j < len(s.buckets); j++ {
		for i := 0; i < len(s.confAvg); i++ {
			s.confAvg[i][j] *= s.decay
			s.failAvg[i][j] *= s.decay
		}
		s.feeRateAvg[j] *= s.decay
		s.txCtAvg[j] *= s.decay
	}
}

// estimateMedianVal calculates a fee rate estimate.
// Returns -1 if no estimate could be made.
//
// This is a direct port of Bitcoin Core's TxConfirmStats::EstimateMedianVal.
// It scans from highest to lowest fee bucket, accumulating data until there's
// enough to evaluate, then checks if the success rate meets the threshold.
func (s *txConfirmStats) estimateMedianVal(confTarget int, sufficientTxVal float64,
	successBreakPoint float64, blockHeight uint64) float64 {
	var nConf float64    // confirmed within target
	var totalNum float64 // total ever confirmed
	var extraNum int     // still in mempool past target
	var failNum float64  // failed (left mempool unconfirmed)

	periodTarget := (confTarget + s.scale - 1) / s.scale
	maxBucketIdx := len(s.buckets) - 1

	curNearBucket := maxBucketIdx
	bestNearBucket := maxBucketIdx
	curFarBucket := maxBucketIdx //nolint:ineffassign // set initial value, overwritten in loop
	bestFarBucket := maxBucketIdx

	var partialNum float64
	foundAnswer := false
	bins := len(s.unconfTxs)
	newBucketRange := true
	passing := true

	// Scan from highest fee bucket to lowest.
	for bucket := maxBucketIdx; bucket >= 0; bucket-- {
		if newBucketRange {
			curNearBucket = bucket
			newBucketRange = false
		}
		curFarBucket = bucket

		if periodTarget-1 < len(s.confAvg) {
			nConf += s.confAvg[periodTarget-1][bucket]
		}
		partialNum += s.txCtAvg[bucket]
		totalNum += s.txCtAvg[bucket]
		if periodTarget-1 < len(s.failAvg) {
			failNum += s.failAvg[periodTarget-1][bucket]
		}

		for confct := confTarget; confct < s.getMaxConfirms(); confct++ {
			slot := (int(blockHeight) - confct) % bins
			if slot < 0 {
				slot += bins
			}
			extraNum += s.unconfTxs[slot][bucket]
		}
		extraNum += s.oldUnconfTxs[bucket]

		// Check if we have enough data in this range of buckets.
		if partialNum < sufficientTxVal/(1-s.decay) {
			continue
		}

		// Enough data accumulated — evaluate success rate.
		partialNum = 0

		denom := totalNum + failNum + float64(extraNum)
		if denom <= 0 {
			continue
		}
		curPct := nConf / denom

		if curPct < successBreakPoint {
			if passing {
				// First failure — record it.
				passing = false
			}
			continue
		}

		// Passing — record as best answer and reset counters.
		foundAnswer = true
		passing = true
		nConf = 0
		totalNum = 0
		failNum = 0
		extraNum = 0
		bestNearBucket = curNearBucket
		bestFarBucket = curFarBucket
		newBucketRange = true
	}

	// Calculate the median fee rate of the best passing bucket range.
	if !foundAnswer {
		return -1
	}

	minBucket := bestFarBucket
	maxBucket := bestNearBucket
	if minBucket > maxBucket {
		minBucket, maxBucket = maxBucket, minBucket
	}

	var txSum float64
	for j := minBucket; j <= maxBucket; j++ {
		txSum += s.txCtAvg[j]
	}
	if txSum == 0 {
		return -1
	}

	halfTxSum := txSum / 2
	for j := minBucket; j <= maxBucket; j++ {
		if s.txCtAvg[j] < halfTxSum {
			halfTxSum -= s.txCtAvg[j]
		} else {
			// Found the median bucket — return average fee rate.
			if s.txCtAvg[j] > 0 {
				median := s.feeRateAvg[j] / s.txCtAvg[j]
				logging.Debug("Fee estimator: EstimateMedianVal result",
					"confTarget", confTarget, "periodTarget", periodTarget,
					"median", median, "bucket", j,
					"bucketBound", s.buckets[j],
					"scale", s.scale,
				)
				return median
			}
			break
		}
	}

	return -1
}

// inf is used as the upper bound of the last (catch-all) bucket.
var inf = math.Inf(1) //nolint:unused // will be used by fee estimator

// Compile-time assertions to suppress unused warnings for WIP code.
// These will be removed when the fee estimator is fully integrated.
var _ = newTxConfirmStats
var _ = (*txConfirmStats).getMaxConfirms
var _ = (*txConfirmStats).bucketIndex
var _ = (*txConfirmStats).clearCurrent
var _ = (*txConfirmStats).record
var _ = (*txConfirmStats).newTx
var _ = (*txConfirmStats).removeTx
var _ = (*txConfirmStats).updateMovingAverages
var _ = (*txConfirmStats).estimateMedianVal
