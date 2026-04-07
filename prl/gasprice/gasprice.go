// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package gasprice

import (
	"context"
	"math"
	"math/big"
	"sync"

	"github.com/ParallaxProtocol/parallax/common"
	"github.com/ParallaxProtocol/parallax/core"
	"github.com/ParallaxProtocol/parallax/core/types"
	"github.com/ParallaxProtocol/parallax/event"
	"github.com/ParallaxProtocol/parallax/log"
	parampkg "github.com/ParallaxProtocol/parallax/params"
	"github.com/ParallaxProtocol/parallax/rpc"
	lru "github.com/hashicorp/golang-lru"
)

// Constants matching Bitcoin Core's CBlockPolicyEstimator exactly.
// Since Parallax has the same ~10 minute block target as Bitcoin,
// these parameters apply directly.
const (
	// Bucket generation parameters.
	// FEE_SPACING in Bitcoin Core: 1.05 (5% geometric spacing).
	feeSpacing = 1.05

	// Horizon parameters: periods, scale, and decay.
	// SHORT: decay 0.962, half-life ~18 blocks (~3 hours).
	shortBlockPeriods = 12
	shortScale        = 1
	shortDecay        = 0.962

	// MED: decay 0.9952, half-life ~144 blocks (~1 day).
	medBlockPeriods = 24
	medScale        = 2
	medDecay        = 0.9952

	// LONG: decay 0.99931, half-life ~1008 blocks (~1 week).
	longBlockPeriods = 42
	longScale        = 24
	longDecay        = 0.99931

	// Success thresholds for estimateSmartFee.
	halfSuccessPct   = 0.6  // 60% required at target/2
	successPct       = 0.85 // 85% required at target
	doubleSuccessPct = 0.95 // 95% required at 2*target

	// Minimum data thresholds.
	sufficientFeeTxs  = 0.1 // for medium and long horizons
	sufficientTxShort = 0.5 // for short horizon (needs more data due to faster decay)

	// OLDEST_ESTIMATE_HISTORY: max blocks of history before data is considered stale.
	oldestEstimateHistory = 6 * 1008 // ~6 weeks
)

var (
	DefaultMaxPrice    = big.NewInt(500 * parampkg.GWei)
	DefaultIgnorePrice = big.NewInt(2 * parampkg.Wei) // kept for backward compat
)

// Config contains the configuration for the gas price oracle.
type Config struct {
	// Legacy fields (kept for FeeHistory backward compatibility).
	Blocks           int
	Percentile       int
	MaxHeaderHistory int
	MaxBlockHistory  int
	Default          *big.Int `toml:",omitempty"`
	MaxPrice         *big.Int `toml:",omitempty"`
	IgnorePrice      *big.Int `toml:",omitempty"`

	// Bucket range configuration (spacing is always 1.05 per Bitcoin Core).
	MinBucketFee *big.Int `toml:",omitempty"`
	MaxBucketFee *big.Int `toml:",omitempty"`
}

// OracleBackend includes all necessary background APIs for oracle.
type OracleBackend interface {
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error)
	GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error)
	PendingBlockAndReceipts() (*types.Block, types.Receipts)
	ChainConfig() *parampkg.ChainConfig
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
}

// TxPoolAccessor provides mempool event access to the oracle.
// Both core.TxPool and light.TxPool satisfy this interface.
// Pass nil for light clients (oracle will still track block confirmations).
type TxPoolAccessor interface {
	SubscribeNewTxsEvent(chan<- core.NewTxsEvent) event.Subscription
}

// EstimateMeta contains metadata about a fee estimation result.
type EstimateMeta struct {
	DataBlocks  int     // approximate number of blocks of data used
	SuccessRate float64 // estimated confirmation probability at the returned fee
}

// txStatsInfo records mempool entry info for a tracked transaction.
type txStatsInfo struct {
	blockHeight uint64
	bucketIndex int
}

// Oracle recommends gas prices based on historical confirmation data,
// using Bitcoin Core's fee estimation algorithm (CBlockPolicyEstimator).
type Oracle struct {
	backend OracleBackend
	txPool  TxPoolAccessor // may be nil

	// Price bounds.
	maxPrice     *big.Int
	defaultPrice *big.Int // miner's --miner.gasprice; used as both floor and ignore threshold

	// Fee rate buckets (as float64 for bucket operations).
	bucketBounds []float64 // upper bounds, including +Inf as last element

	// Three time horizons (matching Bitcoin Core's shortStats, feeStats, longStats).
	shortStats *txConfirmStats
	medStats   *txConfirmStats
	longStats  *txConfirmStats

	// Legacy fields for FeeHistory backward compatibility.
	maxHeaderHistory, maxBlockHistory int
	historyCache                      *lru.Cache

	// State (protected by stateLock).
	stateLock         sync.RWMutex
	pendingTxs        map[common.Hash]txStatsInfo
	nBestSeenHeight   uint64
	firstRecordedHeight uint64

	// Cached estimation results (protected by cacheLock).
	cacheLock       sync.RWMutex
	lastHead        common.Hash
	lastPrice       *big.Int
	cachedEstimates map[int]*big.Int

	// Lifecycle.
	closeCh chan struct{}
}

// NewOracle returns a new gas price oracle using Bitcoin Core's fee estimation algorithm.
// The txPool parameter is optional; pass nil for light clients.
func NewOracle(backend OracleBackend, txPool TxPoolAccessor, params Config) *Oracle {
	// Sanitize legacy fields.
	maxHeaderHistory := params.MaxHeaderHistory
	if maxHeaderHistory < 1 {
		maxHeaderHistory = 1
	}
	maxBlockHistory := params.MaxBlockHistory
	if maxBlockHistory < 1 {
		maxBlockHistory = 1
	}

	maxPrice := params.MaxPrice
	if maxPrice == nil || maxPrice.Int64() <= 0 {
		maxPrice = DefaultMaxPrice
		log.Warn("Sanitizing invalid gasprice oracle price cap", "provided", params.MaxPrice, "updated", maxPrice)
	}
	defaultPrice := params.Default
	if defaultPrice == nil || defaultPrice.Sign() <= 0 {
		defaultPrice = big.NewInt(1 * parampkg.GWei)
	}

	minBucketFee := params.MinBucketFee
	if minBucketFee == nil || minBucketFee.Sign() <= 0 {
		// Default to the miner's minimum gas price — no point generating
		// buckets below fees that will never be mined.
		minBucketFee = defaultPrice
	}
	maxBucketFee := params.MaxBucketFee
	if maxBucketFee == nil || maxBucketFee.Sign() <= 0 {
		maxBucketFee = maxPrice
	}

	// Generate bucket boundaries with FEE_SPACING = 1.05, matching Bitcoin Core.
	minFee := float64(minBucketFee.Int64())
	maxFee := float64(maxBucketFee.Int64())
	var bucketBounds []float64
	for boundary := minFee; boundary <= maxFee; boundary *= feeSpacing {
		bucketBounds = append(bucketBounds, boundary)
	}
	bucketBounds = append(bucketBounds, math.Inf(1)) // INF bucket

	// Create the three stats instances (matching Bitcoin Core's constructor).
	shortStats := newTxConfirmStats(bucketBounds, shortBlockPeriods, shortDecay, shortScale)
	medStats := newTxConfirmStats(bucketBounds, medBlockPeriods, medDecay, medScale)
	longStats := newTxConfirmStats(bucketBounds, longBlockPeriods, longDecay, longScale)

	cache, _ := lru.New(2048)
	oracle := &Oracle{
		backend:          backend,
		txPool:           txPool,
		maxPrice:         maxPrice,
		defaultPrice:     defaultPrice,
		bucketBounds:     bucketBounds,
		shortStats:       shortStats,
		medStats:         medStats,
		longStats:        longStats,
		maxHeaderHistory: maxHeaderHistory,
		maxBlockHistory:  maxBlockHistory,
		historyCache:     cache,
		pendingTxs:       make(map[common.Hash]txStatsInfo),
		lastPrice:        new(big.Int).Set(defaultPrice),
		cachedEstimates:  make(map[int]*big.Int),
		closeCh:          make(chan struct{}),
	}

	// Subscribe to chain head events.
	headEvent := make(chan core.ChainHeadEvent, 1)
	backend.SubscribeChainHeadEvent(headEvent)
	go oracle.blockLoop(headEvent)

	// Subscribe to new transaction events if txPool is available.
	if txPool != nil {
		txEvent := make(chan core.NewTxsEvent, 64)
		txPool.SubscribeNewTxsEvent(txEvent)
		go oracle.txLoop(txEvent)
	}

	log.Info("Gas price oracle initialized (Bitcoin Core algorithm)",
		"buckets", len(bucketBounds),
		"spacing", feeSpacing,
		"minBucket", minBucketFee,
		"maxBucket", maxBucketFee,
		"shortPeriods", shortBlockPeriods,
		"medPeriods", medBlockPeriods,
		"longPeriods", longBlockPeriods,
	)

	return oracle
}

// Close shuts down the oracle goroutines.
func (oracle *Oracle) Close() {
	close(oracle.closeCh)
}

// blockLoop processes new blocks.
func (oracle *Oracle) blockLoop(headEvent chan core.ChainHeadEvent) {
	var lastHead common.Hash
	for {
		select {
		case ev := <-headEvent:
			block := ev.Block
			if block.ParentHash() != lastHead {
				oracle.historyCache.Purge()
			}
			lastHead = block.Hash()
			oracle.processNewBlock(block)
		case <-oracle.closeCh:
			return
		}
	}
}

// txLoop processes new mempool transactions.
func (oracle *Oracle) txLoop(txEvent chan core.NewTxsEvent) {
	for {
		select {
		case ev := <-txEvent:
			oracle.processTransaction(ev.Txs)
		case <-oracle.closeCh:
			return
		}
	}
}

// processTransaction records new mempool transactions for tracking.
// Equivalent to Bitcoin Core's CBlockPolicyEstimator::processTransaction.
func (oracle *Oracle) processTransaction(txs []*types.Transaction) {
	oracle.stateLock.Lock()
	defer oracle.stateLock.Unlock()

	for _, tx := range txs {
		hash := tx.Hash()
		if _, exists := oracle.pendingTxs[hash]; exists {
			continue
		}

		gasPrice := tx.GasPrice()
		if gasPrice.Cmp(oracle.defaultPrice) < 0 {
			continue
		}

		feerate := float64(gasPrice.Int64())
		blockHeight := oracle.nBestSeenHeight

		bucketIdx := oracle.shortStats.newTx(blockHeight, feerate)
		oracle.medStats.newTx(blockHeight, feerate)
		oracle.longStats.newTx(blockHeight, feerate)

		oracle.pendingTxs[hash] = txStatsInfo{
			blockHeight: blockHeight,
			bucketIndex: bucketIdx,
		}

		log.Debug("Fee estimator: tracking new mempool tx",
			"tx", hash.Hex()[:10], "gasPrice", gasPrice,
			"bucket", bucketIdx, "entryBlock", blockHeight,
		)
	}
}

// removeTx removes a transaction from tracking.
func (oracle *Oracle) removeTx(hash common.Hash, inBlock bool) bool {
	info, exists := oracle.pendingTxs[hash]
	if !exists {
		return false
	}

	oracle.shortStats.removeTx(info.blockHeight, oracle.nBestSeenHeight, info.bucketIndex, inBlock)
	oracle.medStats.removeTx(info.blockHeight, oracle.nBestSeenHeight, info.bucketIndex, inBlock)
	oracle.longStats.removeTx(info.blockHeight, oracle.nBestSeenHeight, info.bucketIndex, inBlock)
	delete(oracle.pendingTxs, hash)
	return true
}

// processBlockTx handles a single confirmed transaction from a block.
// Returns true if the transaction was tracked.
func (oracle *Oracle) processBlockTx(blockHeight uint64, tx *types.Transaction) bool {
	hash := tx.Hash()
	info, exists := oracle.pendingTxs[hash]
	if !exists {
		return false
	}

	// Remove from tracking (as confirmed).
	oracle.removeTx(hash, true)

	blocksToConfirm := int(blockHeight) - int(info.blockHeight)
	if blocksToConfirm <= 0 {
		log.Debug("Fee estimator: blocksToConfirm <= 0, ignoring", "hash", hash.Hex()[:10])
		return false
	}

	feerate := float64(tx.GasPrice().Int64())
	oracle.shortStats.record(blocksToConfirm, feerate)
	oracle.medStats.record(blocksToConfirm, feerate)
	oracle.longStats.record(blocksToConfirm, feerate)

	log.Debug("Fee estimator: tracked tx confirmed",
		"tx", hash.Hex()[:10],
		"blocksToConfirm", blocksToConfirm,
		"gasPrice", tx.GasPrice(),
	)
	return true
}

// processBlock handles a new block: updates circular buffers, applies decay,
// and processes confirmed transactions.
// Equivalent to Bitcoin Core's CBlockPolicyEstimator::processBlock.
func (oracle *Oracle) processNewBlock(block *types.Block) {
	oracle.stateLock.Lock()
	defer oracle.stateLock.Unlock()

	blockHeight := block.NumberU64()

	if blockHeight <= oracle.nBestSeenHeight {
		// Ignore side chains and reorgs.
		return
	}

	oracle.nBestSeenHeight = blockHeight

	// Roll circular buffers.
	oracle.shortStats.clearCurrent(blockHeight)
	oracle.medStats.clearCurrent(blockHeight)
	oracle.longStats.clearCurrent(blockHeight)

	// Decay all exponential averages.
	oracle.shortStats.updateMovingAverages()
	oracle.medStats.updateMovingAverages()
	oracle.longStats.updateMovingAverages()

	// Process confirmed transactions.
	countedTxs := 0
	for _, tx := range block.Transactions() {
		if oracle.processBlockTx(blockHeight, tx) {
			countedTxs++
		}
	}

	if oracle.firstRecordedHeight == 0 && countedTxs > 0 {
		oracle.firstRecordedHeight = blockHeight
		log.Info("Fee estimator: first recorded height", "height", blockHeight)
	}

	// Clean up very old pending entries as failures.
	if blockHeight > oldestEstimateHistory {
		cutoff := blockHeight - oldestEstimateHistory
		for hash, info := range oracle.pendingTxs {
			if info.blockHeight < cutoff {
				oracle.removeTx(hash, false) // counts as failure
			}
		}
	}

	log.Debug("Fee estimator: block processed",
		"block", blockHeight,
		"confirmedTracked", countedTxs,
		"blockTxs", len(block.Transactions()),
		"pendingTracked", len(oracle.pendingTxs),
	)

	// Invalidate cached estimates.
	oracle.cacheLock.Lock()
	oracle.lastHead = block.Hash()
	oracle.cachedEstimates = make(map[int]*big.Int)
	oracle.cacheLock.Unlock()
}

// blockSpan returns the number of blocks we've been tracking.
func (oracle *Oracle) blockSpan() uint64 {
	if oracle.firstRecordedHeight == 0 {
		return 0
	}
	return oracle.nBestSeenHeight - oracle.firstRecordedHeight
}

// maxUsableEstimate returns the maximum confirmation target we can meaningfully estimate.
// We need at least 2x the target in block history to have enough potential failure data.
func (oracle *Oracle) maxUsableEstimate() int {
	maxConf := oracle.longStats.getMaxConfirms()
	span := int(oracle.blockSpan())
	usable := span / 2
	if usable > maxConf {
		return maxConf
	}
	if usable < 1 {
		return 1
	}
	return usable
}

// estimateCombinedFee estimates the fee from the shortest applicable horizon.
// If checkShorterHorizon is true, also checks shorter horizons at their max
// target to maintain monotonically increasing estimates.
func (oracle *Oracle) estimateCombinedFee(confTarget int, successThreshold float64, checkShorterHorizon bool) float64 {
	estimate := float64(-1)

	if confTarget >= 1 && confTarget <= oracle.longStats.getMaxConfirms() {
		if confTarget <= oracle.shortStats.getMaxConfirms() {
			estimate = oracle.shortStats.estimateMedianVal(confTarget, sufficientTxShort, successThreshold, oracle.nBestSeenHeight)
		} else if confTarget <= oracle.medStats.getMaxConfirms() {
			estimate = oracle.medStats.estimateMedianVal(confTarget, sufficientFeeTxs, successThreshold, oracle.nBestSeenHeight)
		} else {
			estimate = oracle.longStats.estimateMedianVal(confTarget, sufficientFeeTxs, successThreshold, oracle.nBestSeenHeight)
		}

		if checkShorterHorizon {
			// Check if medium horizon at its max target gives a lower answer.
			if confTarget > oracle.medStats.getMaxConfirms() {
				medMax := oracle.medStats.estimateMedianVal(oracle.medStats.getMaxConfirms(), sufficientFeeTxs, successThreshold, oracle.nBestSeenHeight)
				if medMax > 0 && (estimate == -1 || medMax < estimate) {
					estimate = medMax
				}
			}
			// Check if short horizon at its max target gives a lower answer.
			if confTarget > oracle.shortStats.getMaxConfirms() {
				shortMax := oracle.shortStats.estimateMedianVal(oracle.shortStats.getMaxConfirms(), sufficientTxShort, successThreshold, oracle.nBestSeenHeight)
				if shortMax > 0 && (estimate == -1 || shortMax < estimate) {
					estimate = shortMax
				}
			}
		}
	}
	return estimate
}

// estimateConservativeFee ensures the DOUBLE_SUCCESS_PCT is met at 2*target
// for longer time horizons.
func (oracle *Oracle) estimateConservativeFee(doubleTarget int) float64 {
	estimate := float64(-1)

	if doubleTarget <= oracle.shortStats.getMaxConfirms() {
		estimate = oracle.medStats.estimateMedianVal(doubleTarget, sufficientFeeTxs, doubleSuccessPct, oracle.nBestSeenHeight)
	}
	if doubleTarget <= oracle.medStats.getMaxConfirms() {
		longEst := oracle.longStats.estimateMedianVal(doubleTarget, sufficientFeeTxs, doubleSuccessPct, oracle.nBestSeenHeight)
		if longEst > estimate {
			estimate = longEst
		}
	}
	return estimate
}

// EstimateSmartFee returns the max of fee estimates calculated with:
//   - 60% threshold at target/2
//   - 85% threshold at target
//   - 95% threshold at 2*target
//
// Each calculation uses the shortest applicable time horizon.
// Conservative mode additionally requires 95% at 2*target across longer horizons.
//
// This is a direct port of Bitcoin Core's CBlockPolicyEstimator::estimateSmartFee.
func (oracle *Oracle) EstimateSmartFee(ctx context.Context, confTarget int) (*big.Int, *EstimateMeta, error) {
	log.Info("Fee estimator: EstimateSmartFee called", "confTarget", confTarget)

	// Check cache first (outside state lock).
	oracle.cacheLock.RLock()
	if cached, ok := oracle.cachedEstimates[confTarget]; ok {
		oracle.cacheLock.RUnlock()
		log.Info("Fee estimator: returning cached estimate", "confTarget", confTarget, "gasPrice", cached)
		return new(big.Int).Set(cached), &EstimateMeta{}, nil
	}
	oracle.cacheLock.RUnlock()

	oracle.stateLock.RLock()
	defer oracle.stateLock.RUnlock()

	maxTarget := oracle.longStats.getMaxConfirms()
	if confTarget <= 0 || confTarget > maxTarget {
		if confTarget <= 0 {
			confTarget = 2
		} else {
			confTarget = maxTarget
		}
	}

	// Bitcoin Core: "It's not possible to get reasonable estimates for confTarget of 1"
	if confTarget == 1 {
		confTarget = 2
	}

	// Limit to max usable estimate based on data we've seen.
	maxUsable := oracle.maxUsableEstimate()
	if confTarget > maxUsable {
		confTarget = maxUsable
	}

	if confTarget <= 1 {
		// Not enough data yet.
		log.Info("Fee estimator: insufficient data, returning default", "default", oracle.defaultPrice)
		return new(big.Int).Set(oracle.defaultPrice), &EstimateMeta{}, nil
	}

	median := float64(-1)

	// Sub-estimate 1: 60% success at target/2.
	halfEst := oracle.estimateCombinedFee(confTarget/2, halfSuccessPct, true)
	if halfEst > median {
		median = halfEst
	}

	// Sub-estimate 2: 85% success at target.
	actualEst := oracle.estimateCombinedFee(confTarget, successPct, true)
	if actualEst > median {
		median = actualEst
	}

	// Sub-estimate 3: 95% success at 2*target.
	// For non-conservative: also check shorter horizons (true).
	doubleEst := oracle.estimateCombinedFee(2*confTarget, doubleSuccessPct, true)
	if doubleEst > median {
		median = doubleEst
	}

	// Conservative check: require 95% at 2*target across longer horizons.
	consEst := oracle.estimateConservativeFee(2 * confTarget)
	if consEst > median {
		median = consEst
	}

	log.Info("Fee estimator: sub-estimates",
		"halfEst", halfEst, "actualEst", actualEst,
		"doubleEst", doubleEst, "consEst", consEst,
		"median", median,
	)

	// Convert to *big.Int result.
	var result *big.Int
	if median < 0 {
		result = new(big.Int).Set(oracle.defaultPrice)
		log.Info("Fee estimator: no estimate available, using default", "default", oracle.defaultPrice)
	} else {
		result = new(big.Int).SetInt64(int64(math.Round(median)))
	}

	// Clamp to [defaultPrice, maxPrice].
	if result.Cmp(oracle.defaultPrice) < 0 {
		log.Info("Fee estimator: clamping to minimum", "result", result, "floor", oracle.defaultPrice)
		result = new(big.Int).Set(oracle.defaultPrice)
	}
	if result.Cmp(oracle.maxPrice) > 0 {
		log.Info("Fee estimator: clamping to maximum", "result", result, "cap", oracle.maxPrice)
		result = new(big.Int).Set(oracle.maxPrice)
	}

	log.Info("Fee estimator: final result",
		"confTarget", confTarget, "gasPrice", result,
		"lastBlock", oracle.nBestSeenHeight,
		"blockSpan", oracle.blockSpan(),
		"pendingTracked", len(oracle.pendingTxs),
	)

	// Cache the result.
	oracle.cacheLock.Lock()
	oracle.cachedEstimates[confTarget] = new(big.Int).Set(result)
	oracle.cacheLock.Unlock()

	return new(big.Int).Set(result), &EstimateMeta{
		DataBlocks:  int(oracle.blockSpan()),
		SuccessRate: 0,
	}, nil
}

// SuggestTipCap returns a tip cap so that newly created transaction can have a
// very high chance to be included in the following blocks.
func (oracle *Oracle) SuggestTipCap(ctx context.Context) (*big.Int, error) {
	log.Info("Fee estimator: SuggestTipCap called (eth_gasPrice / eth_maxPriorityFeePerGas)")
	estimate, _, err := oracle.EstimateSmartFee(ctx, 2)
	if err != nil {
		log.Error("Fee estimator: SuggestTipCap failed", "err", err)
		return nil, err
	}
	log.Info("Fee estimator: SuggestTipCap result", "gasPrice", estimate)
	return estimate, nil
}
