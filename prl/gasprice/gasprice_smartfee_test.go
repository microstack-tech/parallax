// Copyright 2024 The Parallax Authors
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

package gasprice

// This file ports Bitcoin Core's policyestimator_tests.cpp BlockPolicyEstimates
// test case to Parallax. Our smart-fee estimator is a 1:1 port of Bitcoin
// Core's CBlockPolicyEstimator, so the canonical Bitcoin test should also pass
// on our port. The test exercises the underlying confirmation-statistics
// machinery (medStats with DOUBLE_SUCCESS_PCT) — i.e. the moral equivalent of
// CBlockPolicyEstimator::estimateFee — which is what estimateSmartFee is
// built on top of.
//
// In addition to the direct port we add an integration sanity test that
// drives the same dataset through the public EstimateSmartFee API to verify
// the wrapper logic (halfEst / actualEst / doubleEst / conservative) does not
// regress.

import (
	"context"
	"math"
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/common"
	"github.com/ParallaxProtocol/parallax/core/types"
)

const (
	// Bitcoin Core's test parameters: basefee = 2000, deltaFee tolerance = 100,
	// 10 fee tiers spaced linearly as basefee*(j+1).
	btcBasefee  = int64(2000)
	btcDeltaFee = int64(100)
	btcNumTiers = 10
)

// =============================================================================
// White-box simulation harness
// =============================================================================

// simState is a synthetic mempool/chain harness that drives the smart-fee
// estimator's internal state directly. We bypass processTransaction /
// processNewBlock because they require *types.Block plumbing and a real
// mempool subscription — neither of which the unit-test backend provides.
//
// Operations mirror what processTransaction and processNewBlock do in
// production: addTx -> shortStats/medStats/longStats.newTx, mineBlock ->
// nBestSeenHeight++ + clearCurrent + updateMovingAverages + per-tx removeTx
// and record.
type simState struct {
	oracle    *Oracle
	txsByTier [][]simTx // unmined transactions, indexed by fee tier
}

// simTx is a tracked unmined transaction in the simulation harness.
type simTx struct {
	feeRate     float64
	entryHeight uint64
	bucketIdx   int
}

// newSimState builds an Oracle configured for the BTC port test: bucket
// bounds covering 1000..100000 (the test's fee range is 2000..20000), a
// throwaway default price so no tx is filtered, and smart-fee enabled.
func newSimState(t *testing.T, numTiers int) *simState {
	cfg := Config{
		Default:                 big.NewInt(1),
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee / 2),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	backend := newTestBackend(t, nil, false)
	return &simState{
		oracle:    NewOracle(backend, nil, cfg),
		txsByTier: make([][]simTx, numTiers),
	}
}

func (s *simState) close() { s.oracle.Close() }

// addTx records one new mempool transaction at the given fee tier. Entry
// height is the current best-seen block height — i.e. the chain tip at the
// time of mempool insertion, exactly as Bitcoin's TransactionAddedToMempool
// records it.
func (s *simState) addTx(tier int, feeRate float64) {
	h := s.oracle.nBestSeenHeight
	bIdx := s.oracle.shortStats.newTx(h, feeRate)
	s.oracle.medStats.newTx(h, feeRate)
	s.oracle.longStats.newTx(h, feeRate)
	s.txsByTier[tier] = append(s.txsByTier[tier], simTx{
		feeRate:     feeRate,
		entryHeight: h,
		bucketIdx:   bIdx,
	})
}

// drainTier removes and returns all currently unmined transactions for the
// given tier. Used when building the next block to mine.
func (s *simState) drainTier(tier int) []simTx {
	out := s.txsByTier[tier]
	s.txsByTier[tier] = nil
	return out
}

// mineBlock advances the chain tip by one and confirms the listed
// transactions. Pass nil for an empty block. The transactions must already
// have been added via addTx — this matches Bitcoin's removeForBlock contract.
func (s *simState) mineBlock(confirmed []simTx) {
	s.oracle.stateLock.Lock()
	defer s.oracle.stateLock.Unlock()

	s.oracle.nBestSeenHeight++
	height := s.oracle.nBestSeenHeight

	// Mirror processNewBlock: roll the circular buffer, decay all averages,
	// then process confirmations.
	s.oracle.shortStats.clearCurrent(height)
	s.oracle.medStats.clearCurrent(height)
	s.oracle.longStats.clearCurrent(height)
	s.oracle.shortStats.updateMovingAverages()
	s.oracle.medStats.updateMovingAverages()
	s.oracle.longStats.updateMovingAverages()

	counted := 0
	for _, tx := range confirmed {
		s.oracle.shortStats.removeTx(tx.entryHeight, height, tx.bucketIdx, true)
		s.oracle.medStats.removeTx(tx.entryHeight, height, tx.bucketIdx, true)
		s.oracle.longStats.removeTx(tx.entryHeight, height, tx.bucketIdx, true)

		blocks := int(height) - int(tx.entryHeight)
		if blocks <= 0 {
			continue
		}
		s.oracle.shortStats.record(blocks, tx.feeRate)
		s.oracle.medStats.record(blocks, tx.feeRate)
		s.oracle.longStats.record(blocks, tx.feeRate)
		counted++
	}
	if s.oracle.firstRecordedHeight == 0 && counted > 0 {
		s.oracle.firstRecordedHeight = height
	}

	// Drop the per-target estimate cache so the next EstimateSmartFee call
	// recomputes against fresh state.
	s.oracle.cacheLock.Lock()
	s.oracle.cachedEstimates = make(map[int]*big.Int)
	s.oracle.cacheLock.Unlock()
}

// estimateFeeRaw is the Parallax equivalent of Bitcoin Core's
// CBlockPolicyEstimator::estimateFee: a direct medStats lookup at
// DOUBLE_SUCCESS_PCT (0.95). Bitcoin's estimateFee returns CFeeRate(0) for
// confTarget <= 1 and when no estimate exists; we mirror that with int64(0).
//
// estimateSmartFee is a wrapper that takes the max over multiple
// estimateRawFee calls plus a conservative one. The Bitcoin test exercises
// estimateFee directly because that's the canonical surface for asserting
// behaviour against analytical expectations — the wrapper is harder to pin
// down but inherits its correctness from the underlying machinery, which
// estimateFee tests.
func estimateFeeRaw(o *Oracle, target int) int64 {
	if target <= 1 {
		return 0
	}
	o.stateLock.RLock()
	defer o.stateLock.RUnlock()
	median := o.medStats.estimateMedianVal(
		target, sufficientFeeTxs, doubleSuccessPct, o.nBestSeenHeight,
	)
	if median < 0 {
		return 0
	}
	return int64(math.Round(median))
}

// =============================================================================
// Bitcoin Core BlockPolicyEstimates port
// =============================================================================

// TestPolicyEstimatorBlockPolicyEstimates is a 1:1 port of Bitcoin Core's
// BlockPolicyEstimates test from src/test/policyestimator_tests.cpp.
//
// The simulation runs in five phases. Per block: 4 transactions per fee tier
// (40 txs total) are added to the mempool. Higher tiers get included more
// often: tier 9 in 10/10 blocks, tier 8 in 9/10, ..., tier 0 in 1/10. After
// 200 blocks the analytical confirmation rate within target i (even, scale 2)
// is (T+i)/10 for tier T, so the lowest tier passing the 95% threshold gives
// estimateFee(i) ≈ (11-i)*basefee.
//
// Phases 3-5 then verify decay/stability, congestion, drain, and fast-mining
// behaviour against the baseline captured in phase 2.
func TestPolicyEstimatorBlockPolicyEstimates(t *testing.T) {
	sim := newSimState(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]float64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = float64(btcBasefee * int64(j+1))
	}

	// ----- Phase 1: 200 blocks of mixed-rate confirmation -----
	for o.nBestSeenHeight < 200 {
		// Add 4 txs at each fee tier.
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
		}
		// Mine the top (blocknum%10 + 1) tiers (Bitcoin's `for h=0; h<=blocknum%10`).
		blocknum := o.nBestSeenHeight
		var block []simTx
		for h := 0; h <= int(blocknum%10); h++ {
			block = append(block, sim.drainTier(9-h)...)
		}
		sim.mineBlock(block)

		// Early sanity check at block 3: with only 3 blocks of data the
		// estimator must combine the top three buckets (tiers 9,8,7 at
		// 100/100/90% within target 2) to reach the sufficient-data
		// threshold. The combined range still passes 95%, so the lowest
		// passing range is the tier-8 bucket → 9*basefee.
		if o.nBestSeenHeight == 3 {
			if got := estimateFeeRaw(o, 1); got != 0 {
				t.Errorf("phase1@blk3 estimateFee(1) = %d, want 0", got)
			}
			got := estimateFeeRaw(o, 2)
			want := int64(9) * btcBasefee
			if got >= want+btcDeltaFee || got <= want-btcDeltaFee {
				t.Errorf("phase1@blk3 estimateFee(2) = %d, want %d ± %d",
					got, want, btcDeltaFee)
			}
		}
	}

	// ----- Phase 2: capture per-target baseline estimates -----
	// At steady state, tier T has confirmation probability (T+i)/10 within
	// target i (even, scale 2). The lowest tier passing 95% gives an estimate
	// of (11-i)*basefee. Target 1 is hardcoded to fail.
	origFeeEst := make([]int64, 0, 49)
	for i := 1; i < 10; i++ {
		est := estimateFeeRaw(o, i)
		origFeeEst = append(origFeeEst, est)
		// Estimates must be monotonically non-increasing past target 2.
		if i > 2 && origFeeEst[i-1] > origFeeEst[i-2] {
			t.Errorf("phase2 estimateFee(%d) = %d should be <= estimateFee(%d) = %d",
				i, origFeeEst[i-1], i-1, origFeeEst[i-2])
		}
		if i%2 == 0 {
			want := int64(11-i) * btcBasefee
			if est >= want+btcDeltaFee || est <= want-btcDeltaFee {
				t.Errorf("phase2 estimateFee(%d) = %d, want %d ± %d",
					i, est, want, btcDeltaFee)
			}
		}
	}
	// Pad out targets 10..48 so we can index by i later.
	for i := 10; i <= 48; i++ {
		origFeeEst = append(origFeeEst, estimateFeeRaw(o, i))
	}

	// ----- Phase 3: 50 empty blocks; estimates should remain stable -----
	// 0.9952^50 ≈ 0.787 — the bucket counts decay only modestly and there
	// are no new confirmations to shift the rates, so per-target estimates
	// must remain within deltaFee of their baseline.
	for o.nBestSeenHeight < 250 {
		sim.mineBlock(nil)
	}
	if got := estimateFeeRaw(o, 1); got != 0 {
		t.Errorf("phase3 estimateFee(1) = %d, want 0", got)
	}
	for i := 2; i < 10; i++ {
		got := estimateFeeRaw(o, i)
		if got >= origFeeEst[i-1]+btcDeltaFee || got <= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase3 estimateFee(%d) = %d, want %d ± %d",
				i, got, origFeeEst[i-1], btcDeltaFee)
		}
	}

	// ----- Phase 4: 15 blocks of congestion (txs added, none mined) -----
	// Unconfirmed txs accumulate in `extraNum`, inflating each bucket's
	// denominator and dragging the success rate down. Estimates may fail
	// (return 0) or shift to higher tiers — but they must never drop below
	// the baseline.
	for o.nBestSeenHeight < 265 {
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
		}
		sim.mineBlock(nil)
	}
	for i := 1; i < 10; i++ {
		got := estimateFeeRaw(o, i)
		if got != 0 && got <= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase4 estimateFee(%d) = %d, want 0 or > %d",
				i, got, origFeeEst[i-1]-btcDeltaFee)
		}
	}

	// ----- Phase 4b: drain the entire mempool in a single block -----
	// All accumulated txs from phase 4 land in one block at very high
	// latencies. The new high-latency confirmations push the data into
	// later confAvg slots without immediately collapsing the early-target
	// estimates, so estimates should still not drop below the baseline.
	var drain []simTx
	for j := 0; j < btcNumTiers; j++ {
		drain = append(drain, sim.drainTier(j)...)
	}
	sim.mineBlock(drain)
	if got := estimateFeeRaw(o, 1); got != 0 {
		t.Errorf("phase4b estimateFee(1) = %d, want 0", got)
	}
	for i := 2; i < 10; i++ {
		got := estimateFeeRaw(o, i)
		if got != 0 && got <= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase4b estimateFee(%d) = %d, want 0 or > %d",
				i, got, origFeeEst[i-1]-btcDeltaFee)
		}
	}

	// ----- Phase 5: 400 blocks of fast-mining (every tx confirmed at lat 1) -----
	// 0.9952^400 ≈ 0.146 — the old (high-fee-required) statistics decay to
	// ~15% while new lat-1 confirmations dominate. Lower tiers pass the 95%
	// threshold and estimates fall well below the baseline at every target
	// where the baseline had headroom.
	for o.nBestSeenHeight < 665 {
		var block []simTx
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
			block = append(block, sim.drainTier(j)...)
		}
		sim.mineBlock(block)
	}
	if got := estimateFeeRaw(o, 1); got != 0 {
		t.Errorf("phase5 estimateFee(1) = %d, want 0", got)
	}
	// Stop at i=9: at scale 2 the i=9 baseline was already at the floor and
	// cannot decrease further (Bitcoin's test makes the same exclusion).
	for i := 2; i < 9; i++ {
		got := estimateFeeRaw(o, i)
		if got >= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase5 estimateFee(%d) = %d, want < %d",
				i, got, origFeeEst[i-1]-btcDeltaFee)
		}
	}
}

// =============================================================================
// Public API integration sanity test
// =============================================================================

// TestEstimateSmartFeeIntegration drives the same Phase-1 dataset through the
// public EstimateSmartFee API and verifies the wrapper produces sane bounded
// estimates. estimateSmartFee combines halfEst / actualEst / doubleEst /
// conservative, so its results are not strictly equal to the underlying
// estimateFeeRaw values, but they must stay within the fee range present in
// the dataset and be bounded by the configured floor and cap.
func TestEstimateSmartFeeIntegration(t *testing.T) {
	sim := newSimState(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]float64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = float64(btcBasefee * int64(j+1))
	}

	// 200 blocks of Phase-1 mixed-rate confirmation.
	for o.nBestSeenHeight < 200 {
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
		}
		blocknum := o.nBestSeenHeight
		var block []simTx
		for h := 0; h <= int(blocknum%10); h++ {
			block = append(block, sim.drainTier(9-h)...)
		}
		sim.mineBlock(block)
	}

	floor := big.NewInt(1)                             // configured Default
	ceil := big.NewInt(20*btcBasefee + 10*btcDeltaFee) // top tier + slack
	for _, target := range []int{2, 4, 6, 8, 12, 24, 48} {
		got, _, err := o.EstimateSmartFee(context.Background(), target)
		if err != nil {
			t.Fatalf("EstimateSmartFee(%d) error: %v", target, err)
		}
		if got.Cmp(floor) < 0 {
			t.Errorf("EstimateSmartFee(%d) = %v, below floor %v", target, got, floor)
		}
		if got.Cmp(ceil) > 0 {
			t.Errorf("EstimateSmartFee(%d) = %v, above ceiling %v", target, got, ceil)
		}
	}

	// EstimateSmartFee with target 1 should be clamped to target 2 and yield
	// the same result on a freshly cleared cache.
	o.cacheLock.Lock()
	o.cachedEstimates = make(map[int]*big.Int)
	o.cacheLock.Unlock()
	got1, _, err := o.EstimateSmartFee(context.Background(), 1)
	if err != nil {
		t.Fatalf("EstimateSmartFee(1) error: %v", err)
	}
	o.cacheLock.Lock()
	o.cachedEstimates = make(map[int]*big.Int)
	o.cacheLock.Unlock()
	got2, _, err := o.EstimateSmartFee(context.Background(), 2)
	if err != nil {
		t.Fatalf("EstimateSmartFee(2) error: %v", err)
	}
	if got1.Cmp(got2) != 0 {
		t.Errorf("EstimateSmartFee(1)=%v should equal EstimateSmartFee(2)=%v", got1, got2)
	}
}

// TestEstimateSmartFeeFastMineConverges verifies the Phase-5 scenario in
// isolation through the public API: when every transaction is confirmed in
// one block at every fee tier, the recommended fee converges toward the
// lowest tier we ever observe (since lower fees are no longer correlated
// with slower confirmation).
func TestEstimateSmartFeeFastMineConverges(t *testing.T) {
	sim := newSimState(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]float64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = float64(btcBasefee * int64(j+1))
	}

	// 600 blocks of fast-mine (well past long-horizon convergence).
	for o.nBestSeenHeight < 600 {
		var block []simTx
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
			block = append(block, sim.drainTier(j)...)
		}
		sim.mineBlock(block)
	}

	got, _, err := o.EstimateSmartFee(context.Background(), 2)
	if err != nil {
		t.Fatalf("EstimateSmartFee(2) error: %v", err)
	}
	// With every tier at 100% lat-1 confirmation, the lowest passing tier is
	// tier 0 → btcBasefee. Allow a generous tolerance because estimateSmartFee
	// also evaluates conservative thresholds at 2*target which may bias up
	// slightly when the highest-fee bucket dominates the median calculation.
	upperBound := big.NewInt(3 * btcBasefee)
	if got.Cmp(upperBound) > 0 {
		t.Errorf("EstimateSmartFee(2) = %v after fast-mine, want <= %v", got, upperBound)
	}
}

// =============================================================================
// Real-pipeline simulation harness — drives processTransaction + processNewBlock
// =============================================================================
//
// Whereas simState above pokes the per-horizon stats directly, pipelineSim
// goes through the public ingestion API: every transaction is constructed as a
// real *types.Transaction and ingested via Oracle.processTransaction; every
// block is constructed as a real *types.Block (number + transactions) and
// processed via Oracle.processNewBlock. This exercises hash-based dedup, the
// defaultPrice filter, the pendingTxs map, and the per-block decay/cleanup
// pipeline — i.e. everything Bitcoin Core's test exercises through the
// real CTxMemPool plus removeForBlock.
//
// processNewBlock only reads block.NumberU64() and block.Transactions(), and
// processBlockTx only reads tx.Hash() and tx.GasPrice(), so we can construct
// minimal headers + unsigned legacy txs.

type pipelineSim struct {
	oracle    *Oracle
	blockNum  uint64
	nonce     uint64
	txsByTier [][]*types.Transaction
}

func newPipelineSim(t *testing.T, numTiers int) *pipelineSim {
	cfg := Config{
		Default:                 big.NewInt(1),
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee / 2),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	backend := newTestBackend(t, nil, false)
	return &pipelineSim{
		oracle:    NewOracle(backend, nil, cfg),
		txsByTier: make([][]*types.Transaction, numTiers),
	}
}

func (s *pipelineSim) close() { s.oracle.Close() }

// addTx constructs a real *types.Transaction at the given gas price and
// ingests it through Oracle.processTransaction. The unique nonce ensures a
// unique tx hash so that processTransaction's dedup map records each one.
func (s *pipelineSim) addTx(tier int, gasPrice int64) {
	tx := types.NewTransaction(
		s.nonce,
		common.Address{},
		big.NewInt(0),
		21000,
		big.NewInt(gasPrice),
		nil,
	)
	s.nonce++
	s.oracle.processTransaction([]*types.Transaction{tx})
	s.txsByTier[tier] = append(s.txsByTier[tier], tx)
}

func (s *pipelineSim) drainTier(tier int) []*types.Transaction {
	out := s.txsByTier[tier]
	s.txsByTier[tier] = nil
	return out
}

// mineBlock constructs a *types.Block with the given transactions and the
// next block number, then drives it through Oracle.processNewBlock. Pass nil
// for an empty block.
func (s *pipelineSim) mineBlock(txs []*types.Transaction) {
	s.blockNum++
	header := &types.Header{Number: new(big.Int).SetUint64(s.blockNum)}
	block := types.NewBlockWithHeader(header).WithBody(txs)
	s.oracle.processNewBlock(block)
}

// estimateFeeRawPipe is identical to estimateFeeRaw but reads through the
// pipelineSim's Oracle. Same surface — Bitcoin Core's CBlockPolicyEstimator::estimateFee.
func estimateFeeRawPipe(o *Oracle, target int) int64 {
	return estimateFeeRaw(o, target)
}

// TestPolicyEstimatorBlockPolicyEstimatesPipeline is the same 1:1 port of
// Bitcoin Core's BlockPolicyEstimates test as TestPolicyEstimatorBlockPolicyEstimates,
// but every transaction and block flows through the real public ingestion
// pipeline (processTransaction / processNewBlock) instead of poking
// per-horizon stats directly. This catches regressions in the hash-keyed
// pendingTxs map, the defaultPrice filter, the per-block decay/cleanup, and
// the dedup behaviour — i.e. the layer that the white-box test bypasses.
func TestPolicyEstimatorBlockPolicyEstimatesPipeline(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]int64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = btcBasefee * int64(j+1)
	}

	// ----- Phase 1: 200 blocks of mixed-rate confirmation -----
	for sim.blockNum < 200 {
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
		}
		blocknum := sim.blockNum
		var block []*types.Transaction
		for h := 0; h <= int(blocknum%10); h++ {
			block = append(block, sim.drainTier(9-h)...)
		}
		sim.mineBlock(block)

		if sim.blockNum == 3 {
			if got := estimateFeeRawPipe(o, 1); got != 0 {
				t.Errorf("phase1@blk3 estimateFee(1) = %d, want 0", got)
			}
			got := estimateFeeRawPipe(o, 2)
			want := int64(9) * btcBasefee
			if got >= want+btcDeltaFee || got <= want-btcDeltaFee {
				t.Errorf("phase1@blk3 estimateFee(2) = %d, want %d ± %d",
					got, want, btcDeltaFee)
			}
		}
	}

	// ----- Phase 2: capture per-target baseline estimates -----
	origFeeEst := make([]int64, 0, 49)
	for i := 1; i < 10; i++ {
		est := estimateFeeRawPipe(o, i)
		origFeeEst = append(origFeeEst, est)
		if i > 2 && origFeeEst[i-1] > origFeeEst[i-2] {
			t.Errorf("phase2 estimateFee(%d) = %d should be <= estimateFee(%d) = %d",
				i, origFeeEst[i-1], i-1, origFeeEst[i-2])
		}
		if i%2 == 0 {
			want := int64(11-i) * btcBasefee
			if est >= want+btcDeltaFee || est <= want-btcDeltaFee {
				t.Errorf("phase2 estimateFee(%d) = %d, want %d ± %d",
					i, est, want, btcDeltaFee)
			}
		}
	}
	for i := 10; i <= 48; i++ {
		origFeeEst = append(origFeeEst, estimateFeeRawPipe(o, i))
	}

	// ----- Phase 3: 50 empty blocks -----
	for sim.blockNum < 250 {
		sim.mineBlock(nil)
	}
	if got := estimateFeeRawPipe(o, 1); got != 0 {
		t.Errorf("phase3 estimateFee(1) = %d, want 0", got)
	}
	for i := 2; i < 10; i++ {
		got := estimateFeeRawPipe(o, i)
		if got >= origFeeEst[i-1]+btcDeltaFee || got <= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase3 estimateFee(%d) = %d, want %d ± %d",
				i, got, origFeeEst[i-1], btcDeltaFee)
		}
	}

	// ----- Phase 4: 15 blocks of congestion -----
	for sim.blockNum < 265 {
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
		}
		sim.mineBlock(nil)
	}
	for i := 1; i < 10; i++ {
		got := estimateFeeRawPipe(o, i)
		if got != 0 && got <= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase4 estimateFee(%d) = %d, want 0 or > %d",
				i, got, origFeeEst[i-1]-btcDeltaFee)
		}
	}

	// ----- Phase 4b: drain in one block -----
	var drain []*types.Transaction
	for j := 0; j < btcNumTiers; j++ {
		drain = append(drain, sim.drainTier(j)...)
	}
	sim.mineBlock(drain)
	if got := estimateFeeRawPipe(o, 1); got != 0 {
		t.Errorf("phase4b estimateFee(1) = %d, want 0", got)
	}
	for i := 2; i < 10; i++ {
		got := estimateFeeRawPipe(o, i)
		if got != 0 && got <= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase4b estimateFee(%d) = %d, want 0 or > %d",
				i, got, origFeeEst[i-1]-btcDeltaFee)
		}
	}

	// ----- Phase 5: 400 blocks of fast-mining -----
	for sim.blockNum < 665 {
		var block []*types.Transaction
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
			block = append(block, sim.drainTier(j)...)
		}
		sim.mineBlock(block)
	}
	if got := estimateFeeRawPipe(o, 1); got != 0 {
		t.Errorf("phase5 estimateFee(1) = %d, want 0", got)
	}
	for i := 2; i < 9; i++ {
		got := estimateFeeRawPipe(o, i)
		if got >= origFeeEst[i-1]-btcDeltaFee {
			t.Errorf("phase5 estimateFee(%d) = %d, want < %d",
				i, got, origFeeEst[i-1]-btcDeltaFee)
		}
	}
}

// TestPolicyEstimatorPipelineDedup verifies that processTransaction's hash
// dedup actually fires: re-ingesting the same transaction twice must not
// double-count it in the bucket statistics.
func TestPolicyEstimatorPipelineDedup(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	tx := types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(5*btcBasefee), nil)

	o.processTransaction([]*types.Transaction{tx})
	o.processTransaction([]*types.Transaction{tx})
	o.processTransaction([]*types.Transaction{tx})

	if got := len(o.pendingTxs); got != 1 {
		t.Errorf("len(pendingTxs) = %d after 3 ingests of the same tx, want 1", got)
	}
}

// TestPolicyEstimatorPipelineDefaultPriceFilter verifies that
// processTransaction silently drops transactions whose gas price is below
// the configured Default — these should never be tracked or affect estimates.
func TestPolicyEstimatorPipelineDefaultPriceFilter(t *testing.T) {
	cfg := Config{
		Default:                 big.NewInt(10 * btcBasefee), // 20000
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	backend := newTestBackend(t, nil, false)
	o := NewOracle(backend, nil, cfg)
	defer o.Close()

	// Below default — must be dropped.
	below := types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(btcBasefee), nil)
	// At default — must be tracked.
	at := types.NewTransaction(1, common.Address{}, big.NewInt(0), 21000, big.NewInt(10*btcBasefee), nil)
	// Above default — must be tracked.
	above := types.NewTransaction(2, common.Address{}, big.NewInt(0), 21000, big.NewInt(20*btcBasefee), nil)

	o.processTransaction([]*types.Transaction{below, at, above})

	if got := len(o.pendingTxs); got != 2 {
		t.Errorf("len(pendingTxs) = %d, want 2 (below-default tx must be filtered)", got)
	}
	if _, ok := o.pendingTxs[below.Hash()]; ok {
		t.Error("below-default tx was tracked, want filtered")
	}
	if _, ok := o.pendingTxs[at.Hash()]; !ok {
		t.Error("at-default tx was filtered, want tracked")
	}
	if _, ok := o.pendingTxs[above.Hash()]; !ok {
		t.Error("above-default tx was filtered, want tracked")
	}
}

// TestPolicyEstimatorPipelineReorgIgnored verifies that processNewBlock
// ignores blocks with height ≤ nBestSeenHeight (side chains and reorgs).
func TestPolicyEstimatorPipelineReorgIgnored(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	// Advance to height 5.
	for sim.blockNum < 5 {
		sim.mineBlock(nil)
	}
	if o.nBestSeenHeight != 5 {
		t.Fatalf("nBestSeenHeight = %d, want 5", o.nBestSeenHeight)
	}

	// Replay block 3 — must be ignored.
	header := &types.Header{Number: big.NewInt(3)}
	stale := types.NewBlockWithHeader(header)
	o.processNewBlock(stale)
	if o.nBestSeenHeight != 5 {
		t.Errorf("nBestSeenHeight = %d after stale block, want 5", o.nBestSeenHeight)
	}

	// A new block at height 5 — also ignored (≤ best).
	sameHeight := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(5)})
	o.processNewBlock(sameHeight)
	if o.nBestSeenHeight != 5 {
		t.Errorf("nBestSeenHeight = %d after same-height block, want 5", o.nBestSeenHeight)
	}

	// A block at height 6 — must advance.
	sim.mineBlock(nil)
	if o.nBestSeenHeight != 6 {
		t.Errorf("nBestSeenHeight = %d after new block, want 6", o.nBestSeenHeight)
	}
}

// =============================================================================
// Legacy fallback tests
// =============================================================================

// TestLegacyFallbackOnColdStart verifies that EstimateSmartFee falls back to
// the legacy percentile oracle when the smart fee estimator has no data (cold
// start). The result should be a market-aware value from block history, not
// the configured minimum, and EstimateMeta.LegacyFallback must be true.
func TestLegacyFallbackOnColdStart(t *testing.T) {
	backend := newTestBackend(t, nil, false)
	config := Config{
		Blocks:                  3,
		Percentile:              60,
		Default:                 big.NewInt(1), // very low floor
		MaxPrice:                big.NewInt(100 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	oracle := NewOracle(backend, nil, config)
	defer oracle.Close()

	// EstimateSmartFee on cold start: smart fee has no data → fallback.
	got, meta, err := oracle.EstimateSmartFee(context.Background(), 2)
	if err != nil {
		t.Fatalf("EstimateSmartFee error: %v", err)
	}
	if !meta.LegacyFallback {
		t.Error("EstimateMeta.LegacyFallback = false on cold start, want true")
	}
	// The test backend has txs at 1..32 GWei. The legacy oracle with
	// Blocks=3, Percentile=60 returns ~30 GWei — far above the 1 wei floor.
	floor := big.NewInt(1)
	if got.Cmp(floor) <= 0 {
		t.Errorf("Cold start fallback returned %v, want > %v (should be market-aware)", got, floor)
	}

	// SuggestTipCap should also fall back.
	tip, err := oracle.SuggestTipCap(context.Background())
	if err != nil {
		t.Fatalf("SuggestTipCap error: %v", err)
	}
	if tip.Cmp(floor) <= 0 {
		t.Errorf("SuggestTipCap cold start returned %v, want > %v", tip, floor)
	}
}

// TestNoFallbackWithSufficientData verifies that once the smart fee estimator
// has accumulated enough data, the fallback does NOT fire — the result comes
// from the smart fee algorithm with LegacyFallback = false.
func TestNoFallbackWithSufficientData(t *testing.T) {
	sim := newSimState(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]float64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = float64(btcBasefee * int64(j+1))
	}

	// Run 200 blocks of Phase-1 mixed-rate confirmation to populate data.
	for o.nBestSeenHeight < 200 {
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 4; k++ {
				sim.addTx(j, feeV[j])
			}
		}
		blocknum := o.nBestSeenHeight
		var block []simTx
		for h := 0; h <= int(blocknum%10); h++ {
			block = append(block, sim.drainTier(9-h)...)
		}
		sim.mineBlock(block)
	}

	// With 200 blocks of data, smart fee should produce a real estimate.
	got, meta, err := o.EstimateSmartFee(context.Background(), 2)
	if err != nil {
		t.Fatalf("EstimateSmartFee error: %v", err)
	}
	if meta.LegacyFallback {
		t.Error("EstimateMeta.LegacyFallback = true with 200 blocks of data, want false")
	}
	// The estimate should be a real value (well above the 1 wei default).
	if got.Cmp(big.NewInt(1)) <= 0 {
		t.Errorf("EstimateSmartFee(2) = %v with full data, want a real estimate", got)
	}
}

// TestLegacyFallbackMatchesLegacyOracle verifies that when the fallback fires,
// the returned value matches what a pure legacy oracle would return for the
// same backend and config — i.e. the fallback is a real legacy computation,
// not a stale or default value.
func TestLegacyFallbackMatchesLegacyOracle(t *testing.T) {
	backend := newTestBackend(t, nil, false)
	config := Config{
		Blocks:     3,
		Percentile: 60,
		Default:    big.NewInt(1),
	}

	// Create a pure legacy oracle.
	legacyOracle := NewOracle(backend, nil, config)
	defer legacyOracle.Close()
	legacyTip, err := legacyOracle.SuggestTipCap(context.Background())
	if err != nil {
		t.Fatalf("legacy SuggestTipCap error: %v", err)
	}

	// Create a smart fee oracle with the same config.
	smartConfig := config
	smartConfig.EnableSmartFeeEstimator = true
	smartOracle := NewOracle(backend, nil, smartConfig)
	defer smartOracle.Close()
	smartTip, err := smartOracle.SuggestTipCap(context.Background())
	if err != nil {
		t.Fatalf("smart SuggestTipCap error: %v", err)
	}

	// On cold start, the smart oracle should fall back to legacy and produce
	// the same result.
	if smartTip.Cmp(legacyTip) != 0 {
		t.Errorf("Smart fee fallback = %v, legacy oracle = %v — should match", smartTip, legacyTip)
	}
}
