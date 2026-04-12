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

// Package prlapi implements the general Parallax API functions.
package prlapi

import (
	"context"
	"math/big"
	"time"

	"github.com/ParallaxProtocol/parallax"
	"github.com/ParallaxProtocol/parallax/dbstore"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/kernel/consensus"
	"github.com/ParallaxProtocol/parallax/node/protocol/gasprice"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/rpc"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/support/event"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation"
	"github.com/ParallaxProtocol/parallax/validation/bloombits"
	"github.com/ParallaxProtocol/parallax/validation/state"
	"github.com/ParallaxProtocol/parallax/wallet"
)

// Backend interface provides the common API services (that are provided by
// both full and light clients) with access to necessary functions.
type Backend interface {
	// General Parallax API
	SyncProgress() parallax.SyncProgress

	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error)
	EstimateSmartFee(ctx context.Context, confTarget int) (*big.Int, *gasprice.EstimateMeta, error)
	ChainDb() dbstore.Database
	AccountManager() *wallet.Manager
	ExtRPCEnabled() bool
	RPCGasCap() uint64            // global gas cap for eth_call over rpc: DoS protection
	RPCPVMTimeout() time.Duration // global timeout for eth_call over rpc: DoS protection
	RPCTxFeeCap() float64         // global tx fee cap for all transaction related APIs
	UnprotectedAllowed() bool     // allows only for EIP155 transactions.

	// Blockchain API
	SetHead(number uint64)
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
	HeaderByHash(ctx context.Context, hash util.Hash) (*types.Header, error)
	HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Header, error)
	CurrentHeader() *types.Header
	CurrentBlock() *types.Block
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error)
	BlockByHash(ctx context.Context, hash util.Hash) (*types.Block, error)
	BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error)
	StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.Header, error)
	StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error)
	GetReceipts(ctx context.Context, hash util.Hash) (types.Receipts, error)
	GetTd(ctx context.Context, hash util.Hash) *big.Int
	GetPVM(ctx context.Context, msg validation.Message, state *state.StateDB, header *types.Header, vmConfig *script.Config) (*script.PVM, func() error, error)
	SubscribeChainEvent(ch chan<- validation.ChainEvent) event.Subscription
	SubscribeChainHeadEvent(ch chan<- validation.ChainHeadEvent) event.Subscription
	SubscribeChainSideEvent(ch chan<- validation.ChainSideEvent) event.Subscription

	// Transaction pool API
	SendTx(ctx context.Context, signedTx *types.Transaction) error
	GetTransaction(ctx context.Context, txHash util.Hash) (*types.Transaction, util.Hash, uint64, uint64, error)
	GetPoolTransactions() (types.Transactions, error)
	GetPoolTransaction(txHash util.Hash) *types.Transaction
	GetPoolNonce(ctx context.Context, addr util.Address) (uint64, error)
	Stats() (pending int, queued int)
	TxPoolContent() (map[util.Address]types.Transactions, map[util.Address]types.Transactions)
	TxPoolContentFrom(addr util.Address) (types.Transactions, types.Transactions)
	SubscribeNewTxsEvent(chan<- validation.NewTxsEvent) event.Subscription

	// Filter API
	BloomStatus() (uint64, uint64)
	GetLogs(ctx context.Context, blockHash util.Hash) ([][]*types.Log, error)
	ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribeRemovedLogsEvent(ch chan<- validation.RemovedLogsEvent) event.Subscription

	ChainConfig() *chainparams.ChainConfig
	Engine() consensus.Engine
}

func GetAPIs(apiBackend Backend) []rpc.API {
	nonceLock := new(AddrLocker)
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicParallaxAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicBlockChainAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicTransactionPoolAPI(apiBackend, nonceLock),
			Public:    true,
		}, {
			Namespace: "txpool",
			Version:   "1.0",
			Service:   NewPublicTxPoolAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(apiBackend),
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicAccountAPI(apiBackend.AccountManager()),
			Public:    true,
		}, {
			Namespace: "personal",
			Version:   "1.0",
			Service:   NewPrivateAccountAPI(apiBackend, nonceLock),
			Public:    false,
		},
	}
}
