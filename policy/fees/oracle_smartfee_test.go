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

package fees

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/rpc"
	"github.com/ParallaxProtocol/parallax/v2/support/event"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/validation"
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
	s.oracle.cachedEstimates = make(map[int]cachedSmartEstimate)
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
	o.cachedEstimates = make(map[int]cachedSmartEstimate)
	o.cacheLock.Unlock()
	got1, _, err := o.EstimateSmartFee(context.Background(), 1)
	if err != nil {
		t.Fatalf("EstimateSmartFee(1) error: %v", err)
	}
	o.cacheLock.Lock()
	o.cachedEstimates = make(map[int]cachedSmartEstimate)
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

// mockTxPool is a TxPoolAccessor backed by an in-memory map. It models a
// real mempool only for the eviction sweep: tests insert real txs with add()
// and remove evicted txs with remove(). The SubscribeNewTxsEvent
// implementation returns a no-op subscription so the oracle's txLoop can
// start without panicking; tests drive ingestion via processTransaction
// directly rather than through the channel.
type mockTxPool struct {
	mu  sync.Mutex
	txs map[util.Hash]*types.Transaction
}

func newMockTxPool() *mockTxPool {
	return &mockTxPool{txs: make(map[util.Hash]*types.Transaction)}
}

func (m *mockTxPool) SubscribeNewTxsEvent(ch chan<- validation.NewTxsEvent) event.Subscription {
	return event.NewSubscription(func(quit <-chan struct{}) error {
		<-quit
		return nil
	})
}

func (m *mockTxPool) Get(hash util.Hash) *types.Transaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.txs[hash]
}

func (m *mockTxPool) add(tx *types.Transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs[tx.Hash()] = tx
}

func (m *mockTxPool) remove(hash util.Hash) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.txs, hash)
}

type pipelineSim struct {
	oracle    *Oracle
	pool      *mockTxPool
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
	pool := newMockTxPool()
	return &pipelineSim{
		oracle:    NewOracle(backend, pool, cfg),
		pool:      pool,
		txsByTier: make([][]*types.Transaction, numTiers),
	}
}

func (s *pipelineSim) close() { s.oracle.Close() }

// addTx constructs a real *types.Transaction at the given gas price, inserts
// it into the mock txpool, and ingests it through Oracle.processTransaction.
// This is the "real tx" path: the tx is in BOTH the pool and the oracle's
// tracking, so the per-block sweep will not flag it as a ghost.
//
// Tests that want to inject ghost-like state should NOT call addTx. They
// should construct the tx and call o.processTransaction directly without
// touching mockTxPool — the next mineBlock will sweep those entries out.
func (s *pipelineSim) addTx(tier int, gasPrice int64) {
	tx := types.NewTransaction(
		s.nonce,
		util.Address{},
		big.NewInt(0),
		21000,
		big.NewInt(gasPrice),
		nil,
	)
	s.nonce++
	s.pool.add(tx)
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
// for an empty block. Confirmed txs are removed from the mock pool first to
// mirror the real txpool's reset/demote behaviour after a block.
//
// In production the eviction sweep runs asynchronously via sweepLoop. Tests
// drive it synchronously here so assertions about pendingTxs after a block
// are deterministic.
func (s *pipelineSim) mineBlock(txs []*types.Transaction) {
	for _, tx := range txs {
		s.pool.remove(tx.Hash())
	}
	s.blockNum++
	header := &types.Header{Number: new(big.Int).SetUint64(s.blockNum)}
	block := types.NewBlockWithHeader(header).WithBody(txs)
	s.oracle.processNewBlock(block)
	s.oracle.sweepEvictedTxs()
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

	tx := types.NewTransaction(0, util.Address{}, big.NewInt(0), 21000, big.NewInt(5*btcBasefee), nil)

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
	below := types.NewTransaction(0, util.Address{}, big.NewInt(0), 21000, big.NewInt(btcBasefee), nil)
	// At default — must be tracked.
	at := types.NewTransaction(1, util.Address{}, big.NewInt(0), 21000, big.NewInt(10*btcBasefee), nil)
	// Above default — must be tracked.
	above := types.NewTransaction(2, util.Address{}, big.NewInt(0), 21000, big.NewInt(20*btcBasefee), nil)

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

// TestPolicyEstimatorPipelineReorgHandling exercises the gate logic in
// processNewBlock for the various non-forward block scenarios:
//
//   - Duplicate (same hash): silently no-op.
//   - Same-height shallow reorg (different hash): skipped with log,
//     lastBlockHash advances.
//   - Lower-height (chain rewind via SetHead or deep reorg): triggers
//     resetSmartFeeStateLocked, then processes the new block from a
//     clean slate.
//   - Forward block at higher height: normal processing, even if the
//     parent does not match (forward reorg, logged).
func TestPolicyEstimatorPipelineReorgHandling(t *testing.T) {
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
	headAt5 := o.lastBlockHash

	// Duplicate (same hash) — silently no-op.
	dupHeader := &types.Header{Number: big.NewInt(5)}
	_ = dupHeader
	// Re-process the actual current block (same hash) — must be a no-op.
	currentBlock := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(int64(o.nBestSeenHeight))})
	if currentBlock.Hash() == headAt5 {
		o.processNewBlock(currentBlock)
		if o.nBestSeenHeight != 5 {
			t.Errorf("duplicate block: nBestSeenHeight = %d, want 5", o.nBestSeenHeight)
		}
	}

	// Same-height different-hash shallow reorg: skipped, but lastBlockHash advances.
	shallowReorg := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(5), Extra: []byte("alt")})
	if shallowReorg.Hash() == headAt5 {
		t.Fatal("shallowReorg accidentally matches headAt5")
	}
	o.processNewBlock(shallowReorg)
	if o.nBestSeenHeight != 5 {
		t.Errorf("shallow reorg: nBestSeenHeight = %d, want 5 (height should not change)", o.nBestSeenHeight)
	}
	if o.lastBlockHash != shallowReorg.Hash() {
		t.Errorf("shallow reorg: lastBlockHash should advance to new hash")
	}

	// Chain rewind to height 3 — triggers state reset, then processes the
	// rewound block. After: nBestSeenHeight should be 3, pendingTxs empty,
	// lastBlockHash matching the rewound block.
	rewindBlock := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(3)})
	o.processNewBlock(rewindBlock)
	if o.nBestSeenHeight != 3 {
		t.Errorf("rewind: nBestSeenHeight = %d, want 3 (state should reset and process new block)", o.nBestSeenHeight)
	}
	if o.lastBlockHash != rewindBlock.Hash() {
		t.Errorf("rewind: lastBlockHash should match the rewound block")
	}
	if len(o.pendingTxs) != 0 {
		t.Errorf("rewind: pendingTxs should be empty after reset, got %d", len(o.pendingTxs))
	}

	// A block at height 4 — normal forward progress from the rewound state.
	sim.blockNum = 3
	sim.mineBlock(nil)
	if o.nBestSeenHeight != 4 {
		t.Errorf("forward after rewind: nBestSeenHeight = %d, want 4", o.nBestSeenHeight)
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

// =============================================================================
// Ghost-entry regression tests
// =============================================================================
//
// These tests started life as bug reproductions for two distinct sources of
// "ghost entries" in the smart fee estimator's pendingTxs map:
//
//  1. Block-before-mempool race: a tx is included in a block before its
//     NewTxsEvent reaches the oracle. processBlockTx skipped the unknown tx
//     and the late processTransaction call then created a permanent ghost.
//
//  2. Unsubscribed mempool eviction: a tx that was tracked entered the pool,
//     then got evicted/replaced/expired. The oracle was never notified and
//     the entry sat in pendingTxs indefinitely.
//
// Both sources are now fixed:
//
//  - processBlockTx records unknown block txs as one-block confirmations and
//    remembers their hashes in recentlyConfirmed. processTransaction drops
//    later events for hashes in recentlyConfirmed.
//  - processNewBlock sweeps pendingTxs against the txpool every block and
//    removes entries that are no longer present.
//
// The tests are kept in inverted form so any regression that re-introduces
// either ghost source will fail loudly here.

// TestRegression_BlockBeforeMempool exercises the race where a block arrives
// before the corresponding NewTxsEvent. With the fix, the unknown tx is
// recorded as a one-block confirmation and the late processTransaction call
// is dropped via the recentlyConfirmed dedup map.
func TestRegression_BlockBeforeMempool(t *testing.T) {
	sim := newPipelineSim(t, 1)
	defer sim.close()
	o := sim.oracle

	// Advance a few blocks so the estimator has a baseline height.
	for i := 0; i < 5; i++ {
		sim.mineBlock(nil)
	}

	// Construct a tx that arrives in the block BEFORE the mempool gossip.
	tx := types.NewTransaction(
		sim.nonce, util.Address{}, big.NewInt(0), 21000,
		big.NewInt(5*btcBasefee), nil,
	)
	sim.nonce++

	// Mine a block containing the tx without first calling processTransaction.
	sim.mineBlock([]*types.Transaction{tx})

	// processBlockTx should have taken the unknown-tx path: confirmation
	// recorded, hash remembered, no entry in pendingTxs.
	o.stateLock.RLock()
	pendingBefore := len(o.pendingTxs)
	firstRecorded := o.firstRecordedHeight
	_, remembered := o.recentlyConfirmed[tx.Hash()]
	o.stateLock.RUnlock()

	if pendingBefore != 0 {
		t.Fatalf("pendingTxs should be empty after unknown-tx confirmation, got %d", pendingBefore)
	}
	if firstRecorded == 0 {
		t.Fatalf("firstRecordedHeight should advance when an unknown block tx is recorded")
	}
	if !remembered {
		t.Fatalf("recentlyConfirmed should contain the confirmed tx hash")
	}

	// Late mempool gossip arrives — must be dropped, not re-tracked.
	o.processTransaction([]*types.Transaction{tx})

	o.stateLock.RLock()
	pendingAfter := len(o.pendingTxs)
	o.stateLock.RUnlock()

	if pendingAfter != 0 {
		t.Fatalf("late processTransaction created a ghost: pendingTxs = %d, want 0", pendingAfter)
	}

	// Mine 200 more empty blocks. The dedup entry will eventually be pruned
	// from recentlyConfirmed but the absence in pendingTxs must persist.
	for i := 0; i < 200; i++ {
		sim.mineBlock(nil)
	}

	o.stateLock.RLock()
	pendingFinal := len(o.pendingTxs)
	o.stateLock.RUnlock()

	if pendingFinal != 0 {
		t.Fatalf("pendingTxs should remain empty after 200 blocks, got %d", pendingFinal)
	}
}

// TestRegression_GhostsAreSweptAndEstimateStable mirrors the production
// scenario: a steady stream of low-fee txs enter the mempool, are tracked
// by the estimator, then get evicted (RBF/expiry/replacement) without a
// removal notification. With the per-block sweep in place, those ghosts are
// removed promptly and the medStats estimate stays near the steady-state
// baseline instead of drifting upward.
func TestRegression_GhostsAreSweptAndEstimateStable(t *testing.T) {
	const numTiers = 5
	sim := newPipelineSim(t, numTiers)
	defer sim.close()
	o := sim.oracle

	// Fee tiers: 2000, 4000, 6000, 8000, 10000.
	fees := make([]int64, numTiers)
	for j := 0; j < numTiers; j++ {
		fees[j] = btcBasefee * int64(j+1)
	}

	const txsPerTierPerBlock = 2

	// Helper that builds a real tx, registers it through both the mock pool
	// and the oracle, and returns it. Mirrors what production traffic would
	// produce: every ingested tx is in the pool when the oracle sees it.
	mineRealTx := func() *types.Transaction { return nil } // dummy for closure capture
	_ = mineRealTx

	addRealTx := func(fee int64) *types.Transaction {
		tx := types.NewTransaction(
			sim.nonce, util.Address{}, big.NewInt(0), 21000,
			big.NewInt(fee), nil,
		)
		sim.nonce++
		sim.pool.add(tx)
		o.processTransaction([]*types.Transaction{tx})
		return tx
	}

	// addGhost ingests a tx through processTransaction WITHOUT registering it
	// in the mock pool. This models the unsubscribed-eviction scenario: the
	// oracle saw the tx via NewTxsEvent at some point but the pool no longer
	// has it. The next mineBlock sweep should remove it.
	addGhost := func(fee int64) {
		tx := types.NewTransaction(
			sim.nonce, util.Address{}, big.NewInt(0), 21000,
			big.NewInt(fee), nil,
		)
		sim.nonce++
		o.processTransaction([]*types.Transaction{tx})
	}

	// Phase 1: 300 blocks of clean steady-state. Real txs only.
	for i := 0; i < 300; i++ {
		var txs []*types.Transaction
		for j := 0; j < numTiers; j++ {
			for k := 0; k < txsPerTierPerBlock; k++ {
				txs = append(txs, addRealTx(fees[j]))
			}
		}
		sim.mineBlock(txs)
	}

	baselineEst := estimateFeeRaw(o, 2)
	if baselineEst <= 0 {
		t.Fatalf("baseline estimate should be positive after 300 blocks, got %d", baselineEst)
	}
	t.Logf("Phase 1 baseline: estimateFee(2) = %d", baselineEst)

	// Phase 2: 200 blocks. Real txs at every tier confirmed in 1 block, plus
	// ghost injections at the lowest tier each block.
	const ghostsPerBlock = 2
	for i := 0; i < 200; i++ {
		var txs []*types.Transaction
		for j := 0; j < numTiers; j++ {
			for k := 0; k < txsPerTierPerBlock; k++ {
				txs = append(txs, addRealTx(fees[j]))
			}
		}
		for k := 0; k < ghostsPerBlock; k++ {
			addGhost(fees[0])
		}
		sim.mineBlock(txs) // ghosts NOT in the block, sweep should remove them
	}

	finalEst := estimateFeeRaw(o, 2)

	o.stateLock.RLock()
	pendingCount := len(o.pendingTxs)
	o.stateLock.RUnlock()

	t.Logf("Phase 2: estimateFee(2) = %d, pendingTxs = %d", finalEst, pendingCount)

	// The sweep should leave pendingTxs essentially empty after each block.
	if pendingCount > numTiers*txsPerTierPerBlock {
		t.Errorf("ghost entries leaked into pendingTxs: got %d, expected ~0", pendingCount)
	}

	// The estimate must stay near the baseline. medStats has scale=2, so a
	// 1-block-old ghost is removed from the unconfirmed counters without a
	// failure recording — it should not bias the estimator at all.
	if finalEst <= 0 {
		t.Errorf("estimate collapsed to %d — sweep did not protect medStats from ghost contamination", finalEst)
	}
	if abs64(finalEst-baselineEst) > btcDeltaFee {
		t.Errorf("estimate drifted from baseline %d to %d (delta %d > %d)",
			baselineEst, finalEst, abs64(finalEst-baselineEst), btcDeltaFee)
	}
}

// TestRegression_GhostStreamDoesNotInflateEstimate is the long-running version
// of the previous test: 800 blocks with 1 ghost per block. With the fix, the
// estimate must remain stable across the full window and pendingTxs must
// stay near zero.
func TestRegression_GhostStreamDoesNotInflateEstimate(t *testing.T) {
	const numTiers = 5
	sim := newPipelineSim(t, numTiers)
	defer sim.close()
	o := sim.oracle

	fees := make([]int64, numTiers)
	for j := 0; j < numTiers; j++ {
		fees[j] = btcBasefee * int64(j+1)
	}

	const txsPerTierPerBlock = 2

	addRealTx := func(fee int64) *types.Transaction {
		tx := types.NewTransaction(
			sim.nonce, util.Address{}, big.NewInt(0), 21000,
			big.NewInt(fee), nil,
		)
		sim.nonce++
		sim.pool.add(tx)
		o.processTransaction([]*types.Transaction{tx})
		return tx
	}
	addGhost := func(fee int64) {
		tx := types.NewTransaction(
			sim.nonce, util.Address{}, big.NewInt(0), 21000,
			big.NewInt(fee), nil,
		)
		sim.nonce++
		o.processTransaction([]*types.Transaction{tx})
	}

	// Warm up: 200 clean blocks.
	for i := 0; i < 200; i++ {
		var txs []*types.Transaction
		for j := 0; j < numTiers; j++ {
			for k := 0; k < txsPerTierPerBlock; k++ {
				txs = append(txs, addRealTx(fees[j]))
			}
		}
		sim.mineBlock(txs)
	}

	baselineEst := estimateFeeRaw(o, 2)
	if baselineEst <= 0 {
		t.Fatalf("no baseline estimate after warm up")
	}
	t.Logf("Baseline: %d", baselineEst)

	// 800 blocks with one persistent ghost per block at the lowest tier.
	for phase := 0; phase < 4; phase++ {
		for i := 0; i < 200; i++ {
			var txs []*types.Transaction
			for j := 0; j < numTiers; j++ {
				for k := 0; k < txsPerTierPerBlock; k++ {
					txs = append(txs, addRealTx(fees[j]))
				}
			}
			addGhost(fees[0])
			sim.mineBlock(txs)
		}
		est := estimateFeeRaw(o, 2)
		t.Logf("After %d ghosted blocks: estimateFee(2) = %d", (phase+1)*200, est)
		if est <= 0 {
			t.Errorf("estimate collapsed at phase %d", phase)
		}
		if abs64(est-baselineEst) > btcDeltaFee {
			t.Errorf("estimate drifted at phase %d: baseline=%d got=%d", phase, baselineEst, est)
		}
	}

	o.stateLock.RLock()
	pendingCount := len(o.pendingTxs)
	o.stateLock.RUnlock()

	// At end-of-block the sweep should have removed every ghost.
	if pendingCount > numTiers*txsPerTierPerBlock {
		t.Errorf("expected ~0 leftover pending txs, got %d", pendingCount)
	}
}

// TestRegression_BlockBeforeMempool_Scale is the high-volume version of
// TestRegression_BlockBeforeMempool: every tx in 200 consecutive blocks
// arrives in the block before the mempool. With the fix, every tx is
// recorded as a one-block confirmation, the late mempool gossip is dropped
// by the dedup map, and pendingTxs stays empty. firstRecordedHeight must
// advance immediately on the first block.
func TestRegression_BlockBeforeMempool_Scale(t *testing.T) {
	cfg := Config{
		Default:                 big.NewInt(1),
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee / 2),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	backend := newTestBackend(t, nil, false)
	o := NewOracle(backend, nil, cfg)
	defer o.Close()

	var blockNum uint64
	var nonce uint64
	const uniformFee = 5 * btcBasefee
	const txsPerBlock = 4

	for i := 0; i < 200; i++ {
		var txs []*types.Transaction
		for k := 0; k < txsPerBlock; k++ {
			tx := types.NewTransaction(
				nonce, util.Address{}, big.NewInt(0), 21000,
				big.NewInt(uniformFee), nil,
			)
			nonce++
			txs = append(txs, tx)
		}

		blockNum++
		header := &types.Header{Number: new(big.Int).SetUint64(blockNum)}
		block := types.NewBlockWithHeader(header).WithBody(txs)
		o.processNewBlock(block)

		// Late mempool gossip — must be dropped by recentlyConfirmed.
		o.processTransaction(txs)
	}

	o.stateLock.RLock()
	pendingCount := len(o.pendingTxs)
	height := o.nBestSeenHeight
	firstRecorded := o.firstRecordedHeight
	o.stateLock.RUnlock()

	if pendingCount != 0 {
		t.Errorf("expected 0 pending entries, got %d (ghost regression)", pendingCount)
	}
	if height != 200 {
		t.Errorf("nBestSeenHeight = %d, want 200", height)
	}
	if firstRecorded == 0 {
		t.Errorf("firstRecordedHeight = 0, want >0 (unknown-tx confirmations should set it)")
	}

	// With 200 blocks of recorded one-block confirmations, the medStats
	// estimate at target 2 should be a real number near the uniform fee.
	est := estimateFeeRaw(o, 2)
	if est <= 0 {
		t.Errorf("estimateFee(2) = %d, want > 0 after 200 recorded confirmations", est)
	}
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// =============================================================================
// New positive tests for the fix
// =============================================================================

// TestEvictionSweepRemovesUnconfirmedGhost verifies the per-block sweep
// removes a tracked tx that has been evicted from the pool.
func TestEvictionSweepRemovesUnconfirmedGhost(t *testing.T) {
	sim := newPipelineSim(t, 1)
	defer sim.close()
	o := sim.oracle

	// Add a real tx through the full path so it ends up in BOTH pool and
	// oracle.pendingTxs.
	sim.addTx(0, 5*btcBasefee)
	tx := sim.txsByTier[0][0]

	o.stateLock.RLock()
	if _, ok := o.pendingTxs[tx.Hash()]; !ok {
		o.stateLock.RUnlock()
		t.Fatal("tx should be tracked after addTx")
	}
	o.stateLock.RUnlock()

	// Simulate eviction: remove from pool only.
	sim.pool.remove(tx.Hash())

	// Mine an empty block — the sweep should pick up the missing entry.
	sim.mineBlock(nil)

	o.stateLock.RLock()
	_, stillPending := o.pendingTxs[tx.Hash()]
	o.stateLock.RUnlock()

	if stillPending {
		t.Error("evicted tx still tracked in pendingTxs after sweep")
	}
}

// TestRecentlyConfirmedPruned verifies that the dedup map is bounded: entries
// older than recentlyConfirmedRetention blocks are removed.
func TestRecentlyConfirmedPruned(t *testing.T) {
	sim := newPipelineSim(t, 1)
	defer sim.close()
	o := sim.oracle

	// Mine an unknown tx so it lands in recentlyConfirmed.
	tx := types.NewTransaction(
		sim.nonce, util.Address{}, big.NewInt(0), 21000,
		big.NewInt(5*btcBasefee), nil,
	)
	sim.nonce++
	sim.mineBlock([]*types.Transaction{tx})

	o.stateLock.RLock()
	_, present := o.recentlyConfirmed[tx.Hash()]
	o.stateLock.RUnlock()
	if !present {
		t.Fatal("hash should be in recentlyConfirmed after mining unknown tx")
	}

	// Mine recentlyConfirmedRetention + 5 more empty blocks.
	for i := 0; i < recentlyConfirmedRetention+5; i++ {
		sim.mineBlock(nil)
	}

	o.stateLock.RLock()
	_, stillPresent := o.recentlyConfirmed[tx.Hash()]
	o.stateLock.RUnlock()
	if stillPresent {
		t.Errorf("recentlyConfirmed entry should have been pruned after %d blocks",
			recentlyConfirmedRetention+5)
	}
}

// gateBackend wraps a testBackend and exposes a toggleable Synced flag for
// testing the sync gate in processNewBlock.
type gateBackend struct {
	*testBackend
	syncedFlag *atomic.Bool
}

func (b *gateBackend) Synced() bool { return b.syncedFlag.Load() }

// TestSyncGateSkipsBlocksWhileUnsynced verifies that processNewBlock advances
// nBestSeenHeight but does not record any state while the backend reports
// Synced() == false. After flipping Synced() = true, normal processing
// resumes.
func TestSyncGateSkipsBlocksWhileUnsynced(t *testing.T) {
	syncedFlag := &atomic.Bool{}
	backend := &gateBackend{
		testBackend: newTestBackend(t, nil, false),
		syncedFlag:  syncedFlag,
	}
	cfg := Config{
		Default:                 big.NewInt(1),
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee / 2),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	pool := newMockTxPool()
	o := NewOracle(backend, pool, cfg)
	defer o.Close()

	// Synced=false: feed several blocks containing real txs. Nothing should
	// be recorded; only nBestSeenHeight should advance.
	for h := uint64(1); h <= 5; h++ {
		tx := types.NewTransaction(
			h, util.Address{}, big.NewInt(0), 21000,
			big.NewInt(5*btcBasefee), nil,
		)
		header := &types.Header{Number: new(big.Int).SetUint64(h)}
		block := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx})
		o.processNewBlock(block)
	}

	o.stateLock.RLock()
	heightDuringSync := o.nBestSeenHeight
	firstRecordedDuringSync := o.firstRecordedHeight
	pendingDuringSync := len(o.pendingTxs)
	recentDuringSync := len(o.recentlyConfirmed)
	o.stateLock.RUnlock()

	if heightDuringSync != 5 {
		t.Errorf("nBestSeenHeight should advance to 5 while syncing, got %d", heightDuringSync)
	}
	if firstRecordedDuringSync != 0 {
		t.Errorf("firstRecordedHeight should stay 0 while syncing, got %d", firstRecordedDuringSync)
	}
	if pendingDuringSync != 0 {
		t.Errorf("pendingTxs should stay empty while syncing, got %d", pendingDuringSync)
	}
	if recentDuringSync != 0 {
		t.Errorf("recentlyConfirmed should stay empty while syncing, got %d", recentDuringSync)
	}

	// Flip the gate. The next block must be processed normally — its tx
	// counted via the unknown-tx path, firstRecordedHeight advanced.
	syncedFlag.Store(true)
	tx := types.NewTransaction(
		99, util.Address{}, big.NewInt(0), 21000,
		big.NewInt(5*btcBasefee), nil,
	)
	header := &types.Header{Number: new(big.Int).SetUint64(6)}
	block := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx})
	o.processNewBlock(block)

	o.stateLock.RLock()
	firstRecordedAfter := o.firstRecordedHeight
	heightAfter := o.nBestSeenHeight
	_, dedupAfter := o.recentlyConfirmed[tx.Hash()]
	o.stateLock.RUnlock()

	if heightAfter != 6 {
		t.Errorf("nBestSeenHeight should be 6 after post-sync block, got %d", heightAfter)
	}
	if firstRecordedAfter != 6 {
		t.Errorf("firstRecordedHeight should be 6 after the first post-sync block, got %d", firstRecordedAfter)
	}
	if !dedupAfter {
		t.Error("recentlyConfirmed should contain the post-sync confirmed tx")
	}
}

// TestSyncGateBlocksProcessTransaction verifies that processTransaction is a
// no-op while Synced() returns false. Without this gate, txs received during
// the sync window get anchored to a stale nBestSeenHeight, and the eventual
// known-path confirmation produces a record(blocksToConfirm > maxConfirms)
// call that bumps txCtAvg/feeRateAvg without ever updating confAvg —
// silently dragging the bucket's success rate down.
func TestSyncGateBlocksProcessTransaction(t *testing.T) {
	syncedFlag := &atomic.Bool{}
	backend := &gateBackend{
		testBackend: newTestBackend(t, nil, false),
		syncedFlag:  syncedFlag,
	}
	cfg := Config{
		Default:                 big.NewInt(1),
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee / 2),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	pool := newMockTxPool()
	o := NewOracle(backend, pool, cfg)
	defer o.Close()

	// Synced=false: feed several txs through processTransaction. Nothing
	// should be tracked.
	syncedFlag.Store(false)
	for i := uint64(0); i < 10; i++ {
		tx := types.NewTransaction(
			i, util.Address{}, big.NewInt(0), 21000,
			big.NewInt(5*btcBasefee), nil,
		)
		pool.add(tx)
		o.processTransaction([]*types.Transaction{tx})
	}

	o.stateLock.RLock()
	pending := len(o.pendingTxs)
	o.stateLock.RUnlock()
	if pending != 0 {
		t.Errorf("pendingTxs should stay empty while syncing, got %d", pending)
	}

	// Flip the gate; subsequent processTransaction calls should track normally.
	syncedFlag.Store(true)
	tx := types.NewTransaction(
		99, util.Address{}, big.NewInt(0), 21000,
		big.NewInt(5*btcBasefee), nil,
	)
	pool.add(tx)
	o.processTransaction([]*types.Transaction{tx})

	o.stateLock.RLock()
	_, tracked := o.pendingTxs[tx.Hash()]
	o.stateLock.RUnlock()
	if !tracked {
		t.Error("processTransaction should track txs once Synced() returns true")
	}
}

// TestRewindResetsState builds up a non-trivial smart-fee state, then
// simulates a chain rewind (debug_setHead or deep reorg) and verifies the
// oracle resets cleanly and continues processing forward from the rewound
// height instead of being stranded.
func TestRewindResetsState(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]int64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = btcBasefee * int64(j+1)
	}

	// Build up state across many blocks: pendingTxs, recentlyConfirmed,
	// firstRecordedHeight should all be populated.
	for sim.blockNum < 50 {
		var txs []*types.Transaction
		for j := 0; j < btcNumTiers; j++ {
			for k := 0; k < 2; k++ {
				sim.addTx(j, feeV[j])
			}
			txs = append(txs, sim.drainTier(j)...)
		}
		sim.mineBlock(txs)
	}

	o.stateLock.RLock()
	if o.nBestSeenHeight != 50 {
		t.Fatalf("nBestSeenHeight = %d, want 50", o.nBestSeenHeight)
	}
	if o.firstRecordedHeight == 0 {
		t.Fatal("firstRecordedHeight should be non-zero after warm-up")
	}
	preRewindHead := o.lastBlockHash
	o.stateLock.RUnlock()

	// Add a couple of unconfirmed pending entries.
	sim.addTx(0, feeV[0])
	sim.addTx(2, feeV[2])
	o.stateLock.RLock()
	prePendingCount := len(o.pendingTxs)
	o.stateLock.RUnlock()
	if prePendingCount == 0 {
		t.Fatal("expected pendingTxs to be populated")
	}

	// Simulate the rewind: a new block at height 30 (lower than 50) with a
	// hash different from the existing one at that height.
	rewindBlock := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(30), Extra: []byte("rewound")})
	if rewindBlock.Hash() == preRewindHead {
		t.Fatal("rewind block accidentally matches old head")
	}
	o.processNewBlock(rewindBlock)

	o.stateLock.RLock()
	defer o.stateLock.RUnlock()

	if o.nBestSeenHeight != 30 {
		t.Errorf("nBestSeenHeight = %d after rewind, want 30", o.nBestSeenHeight)
	}
	if o.firstRecordedHeight != 0 {
		t.Errorf("firstRecordedHeight = %d after rewind, want 0 (stats reset)", o.firstRecordedHeight)
	}
	if len(o.pendingTxs) != 0 {
		t.Errorf("pendingTxs should be empty after rewind, got %d", len(o.pendingTxs))
	}
	if len(o.recentlyConfirmed) != 0 {
		t.Errorf("recentlyConfirmed should be empty after rewind, got %d", len(o.recentlyConfirmed))
	}
	if o.lastBlockHash != rewindBlock.Hash() {
		t.Errorf("lastBlockHash should be the rewound block")
	}
	// Stats should be re-initialized: txCtAvg all zero.
	for j := range o.shortStats.txCtAvg {
		if o.shortStats.txCtAvg[j] != 0 {
			t.Errorf("shortStats.txCtAvg[%d] = %v after reset, want 0", j, o.shortStats.txCtAvg[j])
			break
		}
	}
}

// TestForwardReorgProcessesNewTip exercises the forward-reorg path: a new
// block at strictly higher height whose parent is not our last head. The
// new tip is processed normally; we accept the bias from missing the
// intermediate fork blocks (same as Bitcoin Core).
func TestForwardReorgProcessesNewTip(t *testing.T) {
	sim := newPipelineSim(t, 1)
	defer sim.close()
	o := sim.oracle

	for sim.blockNum < 5 {
		sim.mineBlock(nil)
	}
	if o.nBestSeenHeight != 5 {
		t.Fatalf("nBestSeenHeight = %d, want 5", o.nBestSeenHeight)
	}

	// Construct a block at height 7 whose parent is some unrelated hash —
	// simulates a 2-block forward reorg replacing blocks 6 and 7.
	parentHash := util.HexToHash("0xdeadbeef")
	header := &types.Header{Number: big.NewInt(7), ParentHash: parentHash}
	tx := types.NewTransaction(0, util.Address{}, big.NewInt(0), 21000, big.NewInt(5*btcBasefee), nil)
	block := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx})
	o.processNewBlock(block)

	o.stateLock.RLock()
	defer o.stateLock.RUnlock()
	if o.nBestSeenHeight != 7 {
		t.Errorf("forward reorg: nBestSeenHeight = %d, want 7", o.nBestSeenHeight)
	}
	if o.lastBlockHash != block.Hash() {
		t.Error("forward reorg: lastBlockHash should be the new tip")
	}
	if _, ok := o.recentlyConfirmed[tx.Hash()]; !ok {
		t.Error("forward reorg: tx in new tip should be recorded as confirmed")
	}
}

// TestSameHeightReorgSkipped verifies a same-height different-hash block
// is skipped (logged) without re-decaying the stats. lastBlockHash advances
// so a future duplicate of the new block is also skipped.
func TestSameHeightReorgSkipped(t *testing.T) {
	sim := newPipelineSim(t, 1)
	defer sim.close()
	o := sim.oracle

	for sim.blockNum < 5 {
		sim.mineBlock(nil)
	}
	preHead := o.lastBlockHash

	// Capture short stats decay state.
	preTxCt := make([]float64, len(o.shortStats.txCtAvg))
	copy(preTxCt, o.shortStats.txCtAvg)

	// Same-height block with different hash.
	altBlock := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(5), Extra: []byte("alt")})
	if altBlock.Hash() == preHead {
		t.Fatal("alt block accidentally matches existing head")
	}
	o.processNewBlock(altBlock)

	if o.nBestSeenHeight != 5 {
		t.Errorf("same-height reorg: nBestSeenHeight = %d, want 5 (unchanged)", o.nBestSeenHeight)
	}
	if o.lastBlockHash != altBlock.Hash() {
		t.Error("same-height reorg: lastBlockHash should advance to new hash")
	}
	// Stats should NOT have been decayed again (no double-decay).
	for j := range preTxCt {
		if o.shortStats.txCtAvg[j] != preTxCt[j] {
			t.Errorf("same-height reorg: shortStats.txCtAvg[%d] changed from %v to %v (double decay?)",
				j, preTxCt[j], o.shortStats.txCtAvg[j])
			break
		}
	}
}

// TestAsyncSweepRunsViaSignalChannel verifies that the async eviction
// sweep, signaled via blockLoop, eventually removes ghost entries without
// blocking the caller of processNewBlock. This exercises sweepLoop end to
// end (signal → drain → sweep) rather than the synchronous helper that
// pipelineSim.mineBlock invokes.
func TestAsyncSweepRunsViaSignalChannel(t *testing.T) {
	cfg := Config{
		Default:                 big.NewInt(1),
		MaxPrice:                big.NewInt(100 * btcBasefee),
		MinBucketFee:            big.NewInt(btcBasefee / 2),
		MaxBucketFee:            big.NewInt(50 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	backend := newTestBackend(t, nil, false)
	pool := newMockTxPool()
	o := NewOracle(backend, pool, cfg)
	defer o.Close()

	// Add a tracked tx via the public ingestion path.
	tx := types.NewTransaction(0, util.Address{}, big.NewInt(0), 21000, big.NewInt(5*btcBasefee), nil)
	pool.add(tx)
	o.processTransaction([]*types.Transaction{tx})

	o.stateLock.RLock()
	if _, tracked := o.pendingTxs[tx.Hash()]; !tracked {
		o.stateLock.RUnlock()
		t.Fatal("tx should be tracked after processTransaction")
	}
	o.stateLock.RUnlock()

	// Evict the tx from the mock pool. The oracle does not know yet.
	pool.remove(tx.Hash())

	// Signal the async sweep directly (mimicking what blockLoop would do
	// after processNewBlock). Use a non-blocking send so the test never
	// hangs if the channel is full.
	select {
	case o.sweepCh <- struct{}{}:
	default:
	}

	// Poll for the sweep to remove the ghost. We expect the sweepLoop
	// goroutine to drain the signal and run sweepEvictedTxs within a
	// handful of milliseconds.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		o.stateLock.RLock()
		_, stillTracked := o.pendingTxs[tx.Hash()]
		o.stateLock.RUnlock()
		if !stillTracked {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("async sweep did not remove the ghost within the deadline")
}

// TestCacheKeyClampedSharing verifies that callers passing different
// confTarget inputs that all clamp to the same effective target share a
// single cache entry. Previously each input had its own cache slot.
func TestCacheKeyClampedSharing(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	// Warm up so the smart fee path produces real results.
	feeV := make([]int64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = btcBasefee * int64(j+1)
	}
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
	}

	// Clear the cache to start fresh.
	o.cacheLock.Lock()
	o.cachedEstimates = make(map[int]cachedSmartEstimate)
	o.cacheLock.Unlock()

	// Targets 0 and 1 both clamp to 2; target 2 also clamps to 2. After
	// querying all three, only one cache entry should exist.
	for _, target := range []int{0, 1, 2} {
		_, _, err := o.EstimateSmartFee(context.Background(), target)
		if err != nil {
			t.Fatalf("EstimateSmartFee(%d) error: %v", target, err)
		}
	}

	o.cacheLock.RLock()
	count := len(o.cachedEstimates)
	_, has2 := o.cachedEstimates[2]
	o.cacheLock.RUnlock()

	if count != 1 {
		t.Errorf("expected 1 cache entry after querying targets 0, 1, 2 (all clamp to 2); got %d", count)
	}
	if !has2 {
		t.Error("cache entry should be keyed under the clamped target (2)")
	}
}

// TestCacheHitReturnsPopulatedMeta verifies that a cache hit returns the
// same EstimateMeta as the original cache-miss computation, instead of an
// empty struct.
func TestCacheHitReturnsPopulatedMeta(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	feeV := make([]int64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = btcBasefee * int64(j+1)
	}
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
	}

	o.cacheLock.Lock()
	o.cachedEstimates = make(map[int]cachedSmartEstimate)
	o.cacheLock.Unlock()

	// First call: cache miss, computes fresh meta.
	_, missMeta, err := o.EstimateSmartFee(context.Background(), 4)
	if err != nil {
		t.Fatalf("EstimateSmartFee miss error: %v", err)
	}
	if missMeta.DataBlocks == 0 {
		t.Errorf("cache-miss meta.DataBlocks = 0, expected >0 with %d blocks of warm-up", sim.blockNum)
	}
	if missMeta.SuccessRate == 0 {
		t.Error("cache-miss meta.SuccessRate = 0, expected one of the threshold values")
	}

	// Second call: cache hit. Meta should match.
	_, hitMeta, err := o.EstimateSmartFee(context.Background(), 4)
	if err != nil {
		t.Fatalf("EstimateSmartFee hit error: %v", err)
	}
	if hitMeta.DataBlocks != missMeta.DataBlocks {
		t.Errorf("cache-hit meta.DataBlocks = %d, expected %d (same as miss)",
			hitMeta.DataBlocks, missMeta.DataBlocks)
	}
	if hitMeta.SuccessRate != missMeta.SuccessRate {
		t.Errorf("cache-hit meta.SuccessRate = %v, expected %v (same as miss)",
			hitMeta.SuccessRate, missMeta.SuccessRate)
	}
	if hitMeta.LegacyFallback != missMeta.LegacyFallback {
		t.Errorf("cache-hit meta.LegacyFallback = %v, expected %v",
			hitMeta.LegacyFallback, missMeta.LegacyFallback)
	}
}

// TestEstimateMetaSuccessRate verifies that EstimateMeta.SuccessRate is
// populated with one of the four success thresholds (60% / 85% / 95%) once
// the estimator has data, and is 0 on cold-start fallback.
func TestEstimateMetaSuccessRate(t *testing.T) {
	// Cold start: fallback path should report SuccessRate=0.
	cold := newPipelineSim(t, btcNumTiers)
	defer cold.close()
	_, meta, err := cold.oracle.EstimateSmartFee(context.Background(), 2)
	if err != nil {
		t.Fatalf("EstimateSmartFee error: %v", err)
	}
	if !meta.LegacyFallback {
		t.Errorf("cold start should be a fallback")
	}
	if meta.SuccessRate != 0 {
		t.Errorf("cold start SuccessRate = %v, want 0", meta.SuccessRate)
	}

	// Warm: drive a Phase-1 dataset, then check SuccessRate is one of the
	// known thresholds.
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	feeV := make([]int64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = btcBasefee * int64(j+1)
	}
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
	}

	_, meta, err = sim.oracle.EstimateSmartFee(context.Background(), 4)
	if err != nil {
		t.Fatalf("EstimateSmartFee error: %v", err)
	}
	if meta.LegacyFallback {
		t.Fatal("expected smart fee result, got fallback")
	}
	known := map[float64]bool{halfSuccessPct: true, successPct: true, doubleSuccessPct: true}
	if !known[meta.SuccessRate] {
		t.Errorf("SuccessRate = %v, want one of {%v, %v, %v}",
			meta.SuccessRate, halfSuccessPct, successPct, doubleSuccessPct)
	}
}

// TestCacheCoherencyUnderConcurrentReadsAndBlocks hammers
// estimateSmartFeeBitcoin from many goroutines concurrently with
// processNewBlock, then sanity-checks that no read returned a value below
// the floor or above the cap. The point of this test is to be run under
// -race; the lock-ordering claim in the function godoc says reads cannot
// observe stale writes (because both readers and the writer share
// stateLock). Any data race the detector flags here would falsify that
// claim.
func TestCacheCoherencyUnderConcurrentReadsAndBlocks(t *testing.T) {
	sim := newPipelineSim(t, btcNumTiers)
	defer sim.close()
	o := sim.oracle

	// Warm up the estimator with a Phase-1 dataset so the smart fee path
	// has real data and isn't constantly falling back to the legacy oracle.
	feeV := make([]int64, btcNumTiers)
	for j := 0; j < btcNumTiers; j++ {
		feeV[j] = btcBasefee * int64(j+1)
	}
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
	}

	floor := big.NewInt(1)
	ceil := big.NewInt(100 * btcBasefee)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Reader goroutines: hammer EstimateSmartFee at varied targets.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(target int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, _, err := o.EstimateSmartFee(context.Background(), target)
				if err != nil {
					t.Errorf("EstimateSmartFee error: %v", err)
					return
				}
				if got.Cmp(floor) < 0 || got.Cmp(ceil) > 0 {
					t.Errorf("estimate %v outside [%v, %v]", got, floor, ceil)
					return
				}
			}
		}(2 + (i % 5))
	}

	// Writer goroutine: continuously mines blocks (which clears the cache).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			select {
			case <-stop:
				return
			default:
			}
			var block []*types.Transaction
			for j := 0; j < btcNumTiers; j++ {
				for k := 0; k < 2; k++ {
					sim.addTx(j, feeV[j])
				}
			}
			block = append(block, sim.drainTier(9)...)
			sim.mineBlock(block)
		}
	}()

	// Let the goroutines race for a short window.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestLegacyFallbackAfterProcessNewBlock guards against a regression where
// processNewBlock would write oracle.lastHead, causing the legacy oracle's
// cache check (`headHash == lastHead`) to short-circuit and return the
// initial defaultPrice forever. The bug surfaced only when processNewBlock
// fired before the first legacy fallback call — exactly what happens in
// production cold-start.
func TestLegacyFallbackAfterProcessNewBlock(t *testing.T) {
	backend := newTestBackend(t, nil, false)
	cfg := Config{
		Blocks:                  3,
		Percentile:              60,
		Default:                 big.NewInt(1), // very low so it can't masquerade as a real estimate
		MaxPrice:                big.NewInt(100 * btcBasefee),
		EnableSmartFeeEstimator: true,
	}
	o := NewOracle(backend, nil, cfg)
	defer o.Close()

	// Drive a single block through processNewBlock so lastHead would have
	// been updated by the buggy code. Use a block from the test backend so
	// the head hash matches what suggestTipCapLegacy will see.
	headBlock, err := backend.BlockByNumber(context.Background(), rpc.LatestBlockNumber)
	if err != nil || headBlock == nil {
		t.Fatalf("could not fetch head block from test backend: %v", err)
	}
	o.processNewBlock(headBlock)

	// Now exercise the legacy fallback. With the bug, the legacy oracle
	// would see lastHead == headBlock.Hash() (set by processNewBlock) and
	// return the cached lastPrice (= initial Default = 1 wei). With the fix,
	// the legacy oracle recomputes from block samples and returns a real
	// market-aware tip (~30 GWei for the test backend).
	tip, _, err := o.EstimateSmartFee(context.Background(), 2)
	if err != nil {
		t.Fatalf("EstimateSmartFee error: %v", err)
	}
	floor := big.NewInt(1)
	if tip.Cmp(floor) <= 0 {
		t.Errorf("legacy fallback returned %v after processNewBlock; expected market-aware value > %v", tip, floor)
	}
}
