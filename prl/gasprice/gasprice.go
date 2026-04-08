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
	"sort"
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

// sampleNumber is the number of transactions sampled per block in the
// legacy percentile algorithm.
const sampleNumber = 3

// Constants for the Bitcoin Core-style fee estimator (used when
// EnableSmartFeeEstimator is true). Since Parallax has the same ~10 minute
// block target as Bitcoin, these parameters apply directly.
const (
	// FEE_SPACING in Bitcoin Core: 1.05 (5% geometric spacing).
	feeSpacing = 1.05

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
	DefaultMaxPrice    = big.NewInt(1000 * parampkg.GWei)
	DefaultIgnorePrice = big.NewInt(2 * parampkg.Wei)
)

// Config contains the configuration for the gas price oracle.
type Config struct {
	// Legacy percentile algorithm parameters.
	Blocks           int
	Percentile       int
	MaxHeaderHistory int
	MaxBlockHistory  int
	Default          *big.Int `toml:",omitempty"`
	MaxPrice         *big.Int `toml:",omitempty"`
	IgnorePrice      *big.Int `toml:",omitempty"`

	// EnableSmartFeeEstimator selects the fee estimation algorithm:
	//   false (default) — legacy percentile-based oracle (geth original).
	//   true             — Bitcoin Core-style estimateSmartFee.
	EnableSmartFeeEstimator bool `toml:",omitempty"`

	// Bucket range configuration for the smart fee estimator
	// (spacing is always 1.05 per Bitcoin Core).
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

// TxPoolAccessor provides mempool event access to the smart fee estimator.
// Pass nil for light clients or when smart fee is disabled.
type TxPoolAccessor interface {
	SubscribeNewTxsEvent(chan<- core.NewTxsEvent) event.Subscription
}

// EstimateMeta contains metadata about a fee estimation result.
type EstimateMeta struct {
	DataBlocks  int     // approximate number of blocks of data used
	SuccessRate float64 // estimated confirmation probability at the returned fee
}

// txStatsInfo records mempool entry info for a tracked transaction
// (used by the smart fee estimator).
type txStatsInfo struct {
	blockHeight uint64
	bucketIndex int
}

// Oracle recommends gas prices using either the legacy percentile algorithm
// (geth original) or the Bitcoin Core-style estimateSmartFee algorithm,
// depending on the EnableSmartFeeEstimator config.
type Oracle struct {
	backend OracleBackend
	txPool  TxPoolAccessor // may be nil

	enableSmartFee bool

	// Common price bounds.
	maxPrice     *big.Int
	defaultPrice *big.Int

	// --- Legacy percentile algorithm fields ---
	checkBlocks int
	percentile  int
	ignorePrice *big.Int
	fetchLock   sync.Mutex

	// --- Smart fee algorithm fields ---
	bucketBounds []float64 // upper bounds, including +Inf as last element
	shortStats   *txConfirmStats
	medStats     *txConfirmStats
	longStats    *txConfirmStats

	stateLock           sync.RWMutex
	pendingTxs          map[common.Hash]txStatsInfo
	nBestSeenHeight     uint64
	firstRecordedHeight uint64

	// Fields for FeeHistory backward compatibility.
	maxHeaderHistory, maxBlockHistory int
	historyCache                      *lru.Cache

	// Cached estimation results (used by both algorithms).
	cacheLock       sync.RWMutex
	lastHead        common.Hash
	lastPrice       *big.Int         // legacy single-value cache
	cachedEstimates map[int]*big.Int // smart fee per-target cache

	// Lifecycle.
	closeCh chan struct{}
}

// NewOracle returns a new gas price oracle.
// The txPool parameter is optional; pass nil for light clients or when
// smart fee estimation is disabled (EnableSmartFeeEstimator = false).
func NewOracle(backend OracleBackend, txPool TxPoolAccessor, params Config) *Oracle {
	// Sanitize legacy percentile algorithm fields.
	blocks := params.Blocks
	if blocks < 1 {
		blocks = 1
		log.Warn("Sanitizing invalid gasprice oracle sample blocks", "provided", params.Blocks, "updated", blocks)
	}
	percent := params.Percentile
	if percent < 0 {
		percent = 0
		log.Warn("Sanitizing invalid gasprice oracle sample percentile", "provided", params.Percentile, "updated", percent)
	} else if percent > 100 {
		percent = 100
		log.Warn("Sanitizing invalid gasprice oracle sample percentile", "provided", params.Percentile, "updated", percent)
	}
	maxHeaderHistory := params.MaxHeaderHistory
	if maxHeaderHistory < 1 {
		maxHeaderHistory = 1
	}
	maxBlockHistory := params.MaxBlockHistory
	if maxBlockHistory < 1 {
		maxBlockHistory = 1
	}

	// Sanitize prices.
	maxPrice := params.MaxPrice
	if maxPrice == nil || maxPrice.Int64() <= 0 {
		maxPrice = DefaultMaxPrice
		log.Warn("Sanitizing invalid gasprice oracle price cap", "provided", params.MaxPrice, "updated", maxPrice)
	}
	ignorePrice := params.IgnorePrice
	if ignorePrice == nil || ignorePrice.Int64() <= 0 {
		ignorePrice = DefaultIgnorePrice
	}
	defaultPrice := params.Default
	if defaultPrice == nil || defaultPrice.Sign() <= 0 {
		defaultPrice = big.NewInt(1 * parampkg.GWei)
	}

	cache, _ := lru.New(2048)
	oracle := &Oracle{
		backend:          backend,
		txPool:           txPool,
		enableSmartFee:   params.EnableSmartFeeEstimator,
		maxPrice:         maxPrice,
		defaultPrice:     defaultPrice,
		ignorePrice:      ignorePrice,
		checkBlocks:      blocks,
		percentile:       percent,
		maxHeaderHistory: maxHeaderHistory,
		maxBlockHistory:  maxBlockHistory,
		historyCache:     cache,
		lastPrice:        new(big.Int).Set(defaultPrice),
		closeCh:          make(chan struct{}),
	}

	if params.EnableSmartFeeEstimator {
		// Initialize smart fee estimator state.
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

		// Generate bucket boundaries with FEE_SPACING = 1.05.
		minFee := float64(minBucketFee.Int64())
		maxFee := float64(maxBucketFee.Int64())
		var bucketBounds []float64
		for boundary := minFee; boundary <= maxFee; boundary *= feeSpacing {
			bucketBounds = append(bucketBounds, boundary)
		}
		bucketBounds = append(bucketBounds, math.Inf(1)) // INF bucket

		oracle.bucketBounds = bucketBounds
		oracle.shortStats = newTxConfirmStats(bucketBounds, shortBlockPeriods, shortDecay, shortScale)
		oracle.medStats = newTxConfirmStats(bucketBounds, medBlockPeriods, medDecay, medScale)
		oracle.longStats = newTxConfirmStats(bucketBounds, longBlockPeriods, longDecay, longScale)
		oracle.pendingTxs = make(map[common.Hash]txStatsInfo)
		oracle.cachedEstimates = make(map[int]*big.Int)

		log.Info("Gas price oracle initialized (Bitcoin Core smart fee algorithm)",
			"buckets", len(bucketBounds),
			"spacing", feeSpacing,
			"minBucket", minBucketFee,
			"maxBucket", maxBucketFee,
			"shortPeriods", shortBlockPeriods,
			"medPeriods", medBlockPeriods,
			"longPeriods", longBlockPeriods,
		)
	} else {
		log.Info("Gas price oracle initialized (legacy percentile algorithm)",
			"blocks", blocks, "percentile", percent,
			"maxPrice", maxPrice, "ignorePrice", ignorePrice,
		)
	}

	// Subscribe to chain head events. The block loop runs in both modes:
	// - Legacy mode: only invalidates the historyCache on reorgs.
	// - Smart fee mode: also runs the bucket tracking pipeline.
	headEvent := make(chan core.ChainHeadEvent, 1)
	backend.SubscribeChainHeadEvent(headEvent)
	go oracle.blockLoop(headEvent)

	// Subscribe to mempool tx events only when smart fee is enabled and we
	// have a tx pool to subscribe to.
	if params.EnableSmartFeeEstimator && txPool != nil {
		txEvent := make(chan core.NewTxsEvent, 64)
		txPool.SubscribeNewTxsEvent(txEvent)
		go oracle.txLoop(txEvent)
	}

	return oracle
}

// Close shuts down the oracle goroutines.
func (oracle *Oracle) Close() {
	close(oracle.closeCh)
}

// blockLoop processes new blocks. In legacy mode it only invalidates the
// historyCache on reorgs. In smart fee mode it also runs the bucket tracking.
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
			if oracle.enableSmartFee {
				oracle.processNewBlock(block)
			}
		case <-oracle.closeCh:
			return
		}
	}
}

// txLoop processes new mempool transactions (smart fee mode only).
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

//
// =============================================================================
// Legacy percentile algorithm (original geth implementation)
// =============================================================================
//

// suggestTipCapLegacy is the original geth percentile-based gas price oracle.
// It samples sampleNumber transactions from each of the last checkBlocks blocks,
// sorts them by effective gas tip, and returns the configured percentile.
func (oracle *Oracle) suggestTipCapLegacy(ctx context.Context) (*big.Int, error) {
	head, _ := oracle.backend.HeaderByNumber(ctx, rpc.LatestBlockNumber)
	headHash := head.Hash()

	// Return cached price if still valid.
	oracle.cacheLock.RLock()
	lastHead, lastPrice := oracle.lastHead, oracle.lastPrice
	oracle.cacheLock.RUnlock()
	if headHash == lastHead {
		return new(big.Int).Set(lastPrice), nil
	}
	oracle.fetchLock.Lock()
	defer oracle.fetchLock.Unlock()

	// Re-check cache after acquiring fetch lock.
	oracle.cacheLock.RLock()
	lastHead, lastPrice = oracle.lastHead, oracle.lastPrice
	oracle.cacheLock.RUnlock()
	if headHash == lastHead {
		return new(big.Int).Set(lastPrice), nil
	}

	var (
		sent, exp int
		number    = head.Number.Uint64()
		result    = make(chan results, oracle.checkBlocks)
		quit      = make(chan struct{})
		txValues  []*big.Int
	)
	for sent < oracle.checkBlocks && number > 0 {
		go oracle.getBlockValues(ctx, types.MakeSigner(oracle.backend.ChainConfig(), big.NewInt(int64(number))), number, sampleNumber, oracle.ignorePrice, result, quit)
		sent++
		exp++
		number--
	}
	for exp > 0 {
		res := <-result
		if res.err != nil {
			close(quit)
			return new(big.Int).Set(lastPrice), res.err
		}
		exp--
		// Empty block or only miner txs: use last cached price as filler.
		if len(res.values) == 0 {
			res.values = []*big.Int{lastPrice}
		}
		// If too little data, query more blocks (up to 2*checkBlocks).
		if len(res.values) == 1 && len(txValues)+1+exp < oracle.checkBlocks*2 && number > 0 {
			go oracle.getBlockValues(ctx, types.MakeSigner(oracle.backend.ChainConfig(), big.NewInt(int64(number))), number, sampleNumber, oracle.ignorePrice, result, quit)
			sent++
			exp++
			number--
		}
		txValues = append(txValues, res.values...)
	}
	price := lastPrice
	if len(txValues) > 0 {
		sort.Sort(bigIntArray(txValues))
		price = txValues[(len(txValues)-1)*oracle.percentile/100]
	}
	if price.Cmp(oracle.maxPrice) > 0 {
		price = new(big.Int).Set(oracle.maxPrice)
	}
	oracle.cacheLock.Lock()
	oracle.lastHead = headHash
	oracle.lastPrice = price
	oracle.cacheLock.Unlock()

	return new(big.Int).Set(price), nil
}

type results struct {
	values []*big.Int
	err    error
}

type txSorter struct {
	txs     []*types.Transaction
	baseFee *big.Int
}

func newSorter(txs []*types.Transaction, baseFee *big.Int) *txSorter {
	return &txSorter{txs: txs, baseFee: baseFee}
}

func (s *txSorter) Len() int { return len(s.txs) }
func (s *txSorter) Swap(i, j int) {
	s.txs[i], s.txs[j] = s.txs[j], s.txs[i]
}

func (s *txSorter) Less(i, j int) bool {
	tip1, _ := s.txs[i].EffectiveGasTip(s.baseFee)
	tip2, _ := s.txs[j].EffectiveGasTip(s.baseFee)
	return tip1.Cmp(tip2) < 0
}

// getBlockValues samples up to `limit` transactions from a block, sorted
// ascending by effective tip, and sends them on the result channel.
func (oracle *Oracle) getBlockValues(ctx context.Context, signer types.Signer, blockNum uint64, limit int, ignoreUnder *big.Int, result chan results, quit chan struct{}) {
	block, err := oracle.backend.BlockByNumber(ctx, rpc.BlockNumber(blockNum))
	if block == nil {
		select {
		case result <- results{nil, err}:
		case <-quit:
		}
		return
	}
	txs := make([]*types.Transaction, len(block.Transactions()))
	copy(txs, block.Transactions())
	sorter := newSorter(txs, block.BaseFee())
	sort.Sort(sorter)

	var prices []*big.Int
	for _, tx := range sorter.txs {
		tip, _ := tx.EffectiveGasTip(block.BaseFee())
		if ignoreUnder != nil && tip.Cmp(ignoreUnder) == -1 {
			continue
		}
		sender, err := types.Sender(signer, tx)
		if err == nil && sender != block.Coinbase() {
			prices = append(prices, tip)
			if len(prices) >= limit {
				break
			}
		}
	}
	select {
	case result <- results{prices, nil}:
	case <-quit:
	}
}

type bigIntArray []*big.Int

func (s bigIntArray) Len() int           { return len(s) }
func (s bigIntArray) Less(i, j int) bool { return s[i].Cmp(s[j]) < 0 }
func (s bigIntArray) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

//
// =============================================================================
// Bitcoin Core-style smart fee algorithm
// =============================================================================
//

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

// processNewBlock handles a new block: updates circular buffers, applies decay,
// and processes confirmed transactions.
// Equivalent to Bitcoin Core's CBlockPolicyEstimator::processBlock.
func (oracle *Oracle) processNewBlock(block *types.Block) {
	oracle.stateLock.Lock()
	defer oracle.stateLock.Unlock()

	blockHeight := block.NumberU64()

	if blockHeight <= oracle.nBestSeenHeight {
		return // ignore side chains and reorgs
	}

	oracle.nBestSeenHeight = blockHeight

	oracle.shortStats.clearCurrent(blockHeight)
	oracle.medStats.clearCurrent(blockHeight)
	oracle.longStats.clearCurrent(blockHeight)

	oracle.shortStats.updateMovingAverages()
	oracle.medStats.updateMovingAverages()
	oracle.longStats.updateMovingAverages()

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
				oracle.removeTx(hash, false)
			}
		}
	}

	log.Debug("Fee estimator: block processed",
		"block", blockHeight,
		"confirmedTracked", countedTxs,
		"blockTxs", len(block.Transactions()),
		"pendingTracked", len(oracle.pendingTxs),
	)

	oracle.cacheLock.Lock()
	oracle.lastHead = block.Hash()
	oracle.cachedEstimates = make(map[int]*big.Int)
	oracle.cacheLock.Unlock()
}

func (oracle *Oracle) blockSpan() uint64 {
	if oracle.firstRecordedHeight == 0 {
		return 0
	}
	return oracle.nBestSeenHeight - oracle.firstRecordedHeight
}

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
			if confTarget > oracle.medStats.getMaxConfirms() {
				medMax := oracle.medStats.estimateMedianVal(oracle.medStats.getMaxConfirms(), sufficientFeeTxs, successThreshold, oracle.nBestSeenHeight)
				if medMax > 0 && (estimate == -1 || medMax < estimate) {
					estimate = medMax
				}
			}
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

// estimateSmartFeeBitcoin returns the max of fee estimates calculated with:
//   - 60% threshold at target/2
//   - 85% threshold at target
//   - 95% threshold at 2*target
//   - conservative 95% at 2*target across longer horizons
//
// This is a direct port of Bitcoin Core's CBlockPolicyEstimator::estimateSmartFee.
func (oracle *Oracle) estimateSmartFeeBitcoin(ctx context.Context, confTarget int) (*big.Int, *EstimateMeta, error) {
	log.Info("Fee estimator: EstimateSmartFee called (smart fee)", "confTarget", confTarget)

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

	if confTarget == 1 {
		confTarget = 2
	}

	maxUsable := oracle.maxUsableEstimate()
	if confTarget > maxUsable {
		confTarget = maxUsable
	}

	if confTarget <= 1 {
		log.Info("Fee estimator: insufficient data, returning default", "default", oracle.defaultPrice)
		return new(big.Int).Set(oracle.defaultPrice), &EstimateMeta{}, nil
	}

	median := float64(-1)

	halfEst := oracle.estimateCombinedFee(confTarget/2, halfSuccessPct, true)
	if halfEst > median {
		median = halfEst
	}

	actualEst := oracle.estimateCombinedFee(confTarget, successPct, true)
	if actualEst > median {
		median = actualEst
	}

	doubleEst := oracle.estimateCombinedFee(2*confTarget, doubleSuccessPct, true)
	if doubleEst > median {
		median = doubleEst
	}

	consEst := oracle.estimateConservativeFee(2 * confTarget)
	if consEst > median {
		median = consEst
	}

	log.Info("Fee estimator: sub-estimates",
		"halfEst", halfEst, "actualEst", actualEst,
		"doubleEst", doubleEst, "consEst", consEst,
		"median", median,
	)

	var result *big.Int
	if median < 0 {
		result = new(big.Int).Set(oracle.defaultPrice)
		log.Info("Fee estimator: no estimate available, using default", "default", oracle.defaultPrice)
	} else {
		result = new(big.Int).SetInt64(int64(math.Round(median)))
	}

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

	oracle.cacheLock.Lock()
	oracle.cachedEstimates[confTarget] = new(big.Int).Set(result)
	oracle.cacheLock.Unlock()

	return new(big.Int).Set(result), &EstimateMeta{
		DataBlocks:  int(oracle.blockSpan()),
		SuccessRate: 0,
	}, nil
}

//
// =============================================================================
// Public API (dispatches based on enableSmartFee flag)
// =============================================================================
//

// SuggestTipCap returns a tip cap so that newly created transactions have a
// high chance of being included in the next few blocks.
//
// When EnableSmartFeeEstimator is false (default), uses the legacy percentile
// algorithm. When true, uses the Bitcoin Core-style smart fee estimator with
// a 2-block confirmation target.
func (oracle *Oracle) SuggestTipCap(ctx context.Context) (*big.Int, error) {
	if !oracle.enableSmartFee {
		return oracle.suggestTipCapLegacy(ctx)
	}
	estimate, _, err := oracle.estimateSmartFeeBitcoin(ctx, 2)
	if err != nil {
		log.Error("Fee estimator: SuggestTipCap failed", "err", err)
		return nil, err
	}
	return estimate, nil
}

// EstimateSmartFee returns a recommended gas price for a transaction to be
// confirmed within confTarget blocks, along with metadata.
//
// When EnableSmartFeeEstimator is true, this uses the Bitcoin Core algorithm.
// When false, the confTarget is ignored and the legacy percentile result is
// returned (since the legacy algorithm has no concept of confirmation targets).
func (oracle *Oracle) EstimateSmartFee(ctx context.Context, confTarget int) (*big.Int, *EstimateMeta, error) {
	if oracle.enableSmartFee {
		return oracle.estimateSmartFeeBitcoin(ctx, confTarget)
	}
	// Legacy mode: ignore confTarget, return the percentile-based suggestion.
	tip, err := oracle.suggestTipCapLegacy(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tip, &EstimateMeta{}, nil
}
