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

package les

import (
	"context"
	"errors"
	"math/big"
	"time"

	parallax "github.com/ParallaxProtocol/parallax"
	"github.com/ParallaxProtocol/parallax/dbstore"
	"github.com/ParallaxProtocol/parallax/kernel"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/p2p/netparams"
	"github.com/ParallaxProtocol/parallax/policy/fees"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/rpc"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/support/event"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation"
	"github.com/ParallaxProtocol/parallax/validation/bloombits"
	"github.com/ParallaxProtocol/parallax/validation/light"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
	"github.com/ParallaxProtocol/parallax/validation/state"
	"github.com/ParallaxProtocol/parallax/wallet"
)

type LesApiBackend struct {
	extRPCEnabled       bool
	allowUnprotectedTxs bool
	prl                 *LightParallax
	gpo                 *fees.Oracle
}

func (b *LesApiBackend) ChainConfig() *chainparams.ChainConfig {
	return b.prl.chainConfig
}

func (b *LesApiBackend) CurrentBlock() *types.Block {
	return types.NewBlockWithHeader(b.prl.BlockChain().CurrentHeader())
}

func (b *LesApiBackend) SetHead(number uint64) {
	b.prl.handler.downloader.Cancel()
	b.prl.blockchain.SetHead(number)
}

func (b *LesApiBackend) HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error) {
	// Return the latest current as the pending one since there
	// is no pending notion in the light client. TODO(rjl493456442)
	// unify the behavior of `HeaderByNumber` and `PendingBlockAndReceipts`.
	if number == rpc.PendingBlockNumber {
		return b.prl.blockchain.CurrentHeader(), nil
	}
	if number == rpc.LatestBlockNumber {
		return b.prl.blockchain.CurrentHeader(), nil
	}
	return b.prl.blockchain.GetHeaderByNumberOdr(ctx, uint64(number))
}

func (b *LesApiBackend) HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.HeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header, err := b.HeaderByHash(ctx, hash)
		if err != nil {
			return nil, err
		}
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.prl.blockchain.GetCanonicalHash(header.Number.Uint64()) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		return header, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *LesApiBackend) HeaderByHash(ctx context.Context, hash util.Hash) (*types.Header, error) {
	return b.prl.blockchain.GetHeaderByHash(hash), nil
}

func (b *LesApiBackend) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error) {
	header, err := b.HeaderByNumber(ctx, number)
	if header == nil || err != nil {
		return nil, err
	}
	return b.BlockByHash(ctx, header.Hash())
}

func (b *LesApiBackend) BlockByHash(ctx context.Context, hash util.Hash) (*types.Block, error) {
	return b.prl.blockchain.GetBlockByHash(ctx, hash)
}

func (b *LesApiBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.BlockByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		block, err := b.BlockByHash(ctx, hash)
		if err != nil {
			return nil, err
		}
		if block == nil {
			return nil, errors.New("header found, but block body is missing")
		}
		if blockNrOrHash.RequireCanonical && b.prl.blockchain.GetCanonicalHash(block.NumberU64()) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		return block, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *LesApiBackend) PendingBlockAndReceipts() (*types.Block, types.Receipts) {
	return nil, nil
}

func (b *LesApiBackend) StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.Header, error) {
	header, err := b.HeaderByNumber(ctx, number)
	if err != nil {
		return nil, nil, err
	}
	if header == nil {
		return nil, nil, errors.New("header not found")
	}
	return light.NewState(ctx, header, b.prl.odr), header, nil
}

func (b *LesApiBackend) StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.StateAndHeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := b.prl.blockchain.GetHeaderByHash(hash)
		if header == nil {
			return nil, nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.prl.blockchain.GetCanonicalHash(header.Number.Uint64()) != hash {
			return nil, nil, errors.New("hash is not currently canonical")
		}
		return light.NewState(ctx, header, b.prl.odr), header, nil
	}
	return nil, nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *LesApiBackend) GetReceipts(ctx context.Context, hash util.Hash) (types.Receipts, error) {
	if number := rawdb.ReadHeaderNumber(b.prl.chainDb, hash); number != nil {
		return light.GetBlockReceipts(ctx, b.prl.odr, hash, *number)
	}
	return nil, nil
}

func (b *LesApiBackend) GetLogs(ctx context.Context, hash util.Hash) ([][]*types.Log, error) {
	if number := rawdb.ReadHeaderNumber(b.prl.chainDb, hash); number != nil {
		return light.GetBlockLogs(ctx, b.prl.odr, hash, *number)
	}
	return nil, nil
}

func (b *LesApiBackend) GetTd(ctx context.Context, hash util.Hash) *big.Int {
	if number := rawdb.ReadHeaderNumber(b.prl.chainDb, hash); number != nil {
		return b.prl.blockchain.GetTdOdr(ctx, hash, *number)
	}
	return nil
}

func (b *LesApiBackend) GetPVM(ctx context.Context, msg validation.Message, state *state.StateDB, header *types.Header, vmConfig *script.Config) (*script.PVM, func() error, error) {
	if vmConfig == nil {
		vmConfig = new(script.Config)
	}
	txContext := validation.NewPVMTxContext(msg)
	context := validation.NewPVMBlockContext(header, b.prl.blockchain, nil)
	return script.NewPVM(context, txContext, state, b.prl.chainConfig, *vmConfig), state.Error, nil
}

func (b *LesApiBackend) SendTx(ctx context.Context, signedTx *types.Transaction) error {
	return b.prl.txPool.Add(ctx, signedTx)
}

func (b *LesApiBackend) RemoveTx(txHash util.Hash) {
	b.prl.txPool.RemoveTx(txHash)
}

func (b *LesApiBackend) GetPoolTransactions() (types.Transactions, error) {
	return b.prl.txPool.GetTransactions()
}

func (b *LesApiBackend) GetPoolTransaction(txHash util.Hash) *types.Transaction {
	return b.prl.txPool.GetTransaction(txHash)
}

func (b *LesApiBackend) GetTransaction(ctx context.Context, txHash util.Hash) (*types.Transaction, util.Hash, uint64, uint64, error) {
	return light.GetTransaction(ctx, b.prl.odr, txHash)
}

func (b *LesApiBackend) GetPoolNonce(ctx context.Context, addr util.Address) (uint64, error) {
	return b.prl.txPool.GetNonce(ctx, addr)
}

func (b *LesApiBackend) Stats() (pending int, queued int) {
	return b.prl.txPool.Stats(), 0
}

func (b *LesApiBackend) TxPoolContent() (map[util.Address]types.Transactions, map[util.Address]types.Transactions) {
	return b.prl.txPool.Content()
}

func (b *LesApiBackend) TxPoolContentFrom(addr util.Address) (types.Transactions, types.Transactions) {
	return b.prl.txPool.ContentFrom(addr)
}

func (b *LesApiBackend) SubscribeNewTxsEvent(ch chan<- validation.NewTxsEvent) event.Subscription {
	return b.prl.txPool.SubscribeNewTxsEvent(ch)
}

func (b *LesApiBackend) SubscribeChainEvent(ch chan<- validation.ChainEvent) event.Subscription {
	return b.prl.blockchain.SubscribeChainEvent(ch)
}

func (b *LesApiBackend) SubscribeChainHeadEvent(ch chan<- validation.ChainHeadEvent) event.Subscription {
	return b.prl.blockchain.SubscribeChainHeadEvent(ch)
}

func (b *LesApiBackend) SubscribeChainSideEvent(ch chan<- validation.ChainSideEvent) event.Subscription {
	return b.prl.blockchain.SubscribeChainSideEvent(ch)
}

func (b *LesApiBackend) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return b.prl.blockchain.SubscribeLogsEvent(ch)
}

func (b *LesApiBackend) SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return event.NewSubscription(func(quit <-chan struct{}) error {
		<-quit
		return nil
	})
}

func (b *LesApiBackend) SubscribeRemovedLogsEvent(ch chan<- validation.RemovedLogsEvent) event.Subscription {
	return b.prl.blockchain.SubscribeRemovedLogsEvent(ch)
}

func (b *LesApiBackend) SyncProgress() parallax.SyncProgress {
	return b.prl.Downloader().Progress()
}

// Synced reports whether initial sync is complete. Light clients have no
// acceptTxs flag, and smart fee is disabled by default on light clients
// (LightClientGPO.EnableSmartFeeEstimator = false), so this method is
// defensive: it returns true once the downloader is idle.
func (b *LesApiBackend) Synced() bool {
	return !b.prl.Downloader().Synchronising()
}

func (b *LesApiBackend) ProtocolVersion() int {
	return b.prl.LesVersion() + 10000
}

func (b *LesApiBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return b.gpo.SuggestTipCap(ctx)
}

func (b *LesApiBackend) FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (firstBlock *big.Int, reward [][]*big.Int, baseFee []*big.Int, gasUsedRatio []float64, err error) {
	return b.gpo.FeeHistory(ctx, blockCount, lastBlock, rewardPercentiles)
}

func (b *LesApiBackend) EstimateSmartFee(ctx context.Context, confTarget int) (*big.Int, *fees.EstimateMeta, error) {
	return b.gpo.EstimateSmartFee(ctx, confTarget)
}

func (b *LesApiBackend) ChainDb() dbstore.Database {
	return b.prl.chainDb
}

func (b *LesApiBackend) AccountManager() *wallet.Manager {
	return b.prl.accountManager
}

func (b *LesApiBackend) ExtRPCEnabled() bool {
	return b.extRPCEnabled
}

func (b *LesApiBackend) UnprotectedAllowed() bool {
	return b.allowUnprotectedTxs
}

func (b *LesApiBackend) RPCGasCap() uint64 {
	return b.prl.config.RPCGasCap
}

func (b *LesApiBackend) RPCPVMTimeout() time.Duration {
	return b.prl.config.RPCPVMTimeout
}

func (b *LesApiBackend) RPCTxFeeCap() float64 {
	return b.prl.config.RPCTxFeeCap
}

func (b *LesApiBackend) BloomStatus() (uint64, uint64) {
	if b.prl.bloomIndexer == nil {
		return 0, 0
	}
	sections, _, _ := b.prl.bloomIndexer.Sections()
	return netparams.BloomBitsBlocksClient, sections
}

func (b *LesApiBackend) ServiceFilter(ctx context.Context, session *bloombits.MatcherSession) {
	for i := 0; i < bloomFilterThreads; i++ {
		go session.Multiplex(bloomRetrievalBatch, bloomRetrievalWait, b.prl.bloomRequests)
	}
}

func (b *LesApiBackend) Engine() kernel.Engine {
	return b.prl.engine
}

func (b *LesApiBackend) CurrentHeader() *types.Header {
	return b.prl.blockchain.CurrentHeader()
}

func (b *LesApiBackend) StateAtBlock(ctx context.Context, block *types.Block, reexec uint64, base *state.StateDB, checkLive bool, preferDisk bool) (*state.StateDB, error) {
	return b.prl.stateAtBlock(ctx, block, reexec)
}

func (b *LesApiBackend) StateAtTransaction(ctx context.Context, block *types.Block, txIndex int, reexec uint64) (validation.Message, script.BlockContext, *state.StateDB, error) {
	return b.prl.stateAtTransaction(ctx, block, txIndex, reexec)
}
