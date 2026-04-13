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

package light

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/dbstore"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/support/event"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
	"github.com/ParallaxProtocol/parallax/validation/state"
)

const (
	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10
)

// txPermanent is the number of mined blocks after a mined transaction is
// considered permanent and no rollback is expected
var txPermanent = uint64(500)

// TxPool implements the transaction pool for light clients, which keeps track
// of the status of locally created transactions, detecting if they are included
// in a block (mined) or rolled back. There are no queued transactions since we
// always receive all locally signed transactions in the same order as they are
// created.
type TxPool struct {
	config       *chainparams.ChainConfig
	signer       types.Signer
	quit         chan bool
	txFeed       event.Feed
	scope        event.SubscriptionScope
	chainHeadCh  chan validation.ChainHeadEvent
	chainHeadSub event.Subscription
	mu           sync.RWMutex
	chain        *LightChain
	odr          OdrBackend
	chainDb      dbstore.Database
	relay        TxRelayBackend
	head         util.Hash
	nonce        map[util.Address]uint64            // "pending" nonce
	pending      map[util.Hash]*types.Transaction   // pending transactions by tx hash
	mined        map[util.Hash][]*types.Transaction // mined transactions by block hash
	clearIdx     uint64                             // earliest block nr that can contain mined tx info

	istanbul bool // Fork indicator whether we are in the istanbul stage.
	eip2718  bool // Fork indicator whether we are in the eip2718 stage.
}

// TxRelayBackend provides an interface to the mechanism that forwards transacions
// to the PRL network. The implementations of the functions should be non-blocking.
//
// Send instructs backend to forward new transactions
// NewHead notifies backend about a new head after processed by the tx pool,
//
//	including  mined and rolled back transactions since the last event
//
// Discard notifies backend about transactions that should be discarded either
//
//	because they have been replaced by a re-send or because they have been mined
//	long ago and no rollback is expected
type TxRelayBackend interface {
	Send(txs types.Transactions)
	NewHead(head util.Hash, mined []util.Hash, rollback []util.Hash)
	Discard(hashes []util.Hash)
}

// NewTxPool creates a new light transaction pool
func NewTxPool(config *chainparams.ChainConfig, chain *LightChain, relay TxRelayBackend) *TxPool {
	pool := &TxPool{
		config:      config,
		signer:      types.LatestSigner(config),
		nonce:       make(map[util.Address]uint64),
		pending:     make(map[util.Hash]*types.Transaction),
		mined:       make(map[util.Hash][]*types.Transaction),
		quit:        make(chan bool),
		chainHeadCh: make(chan validation.ChainHeadEvent, chainHeadChanSize),
		chain:       chain,
		relay:       relay,
		odr:         chain.Odr(),
		chainDb:     chain.Odr().Database(),
		head:        chain.CurrentHeader().Hash(),
		clearIdx:    chain.CurrentHeader().Number.Uint64(),
	}
	// Subscribe events from blockchain
	pool.chainHeadSub = pool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)
	go pool.eventLoop()

	return pool
}

// currentState returns the light state of the current head header
func (pool *TxPool) currentState(ctx context.Context) *state.StateDB {
	return NewState(ctx, pool.chain.CurrentHeader(), pool.odr)
}

// GetNonce returns the "pending" nonce of a given address. It always queries
// the nonce belonging to the latest header too in order to detect if another
// client using the same key sent a transaction.
func (pool *TxPool) GetNonce(ctx context.Context, addr util.Address) (uint64, error) {
	state := pool.currentState(ctx)
	nonce := state.GetNonce(addr)
	if state.Error() != nil {
		return 0, state.Error()
	}
	sn, ok := pool.nonce[addr]
	if ok && sn > nonce {
		nonce = sn
	}
	if !ok || sn < nonce {
		pool.nonce[addr] = nonce
	}
	return nonce, nil
}

// txStateChanges stores the recent changes between pending/mined states of
// transactions. True means mined, false means rolled back, no entry means no change
type txStateChanges map[util.Hash]bool

// setState sets the status of a tx to either recently mined or recently rolled back
func (txc txStateChanges) setState(txHash util.Hash, mined bool) {
	val, ent := txc[txHash]
	if ent && (val != mined) {
		delete(txc, txHash)
	} else {
		txc[txHash] = mined
	}
}

// getLists creates lists of mined and rolled back tx hashes
func (txc txStateChanges) getLists() (mined []util.Hash, rollback []util.Hash) {
	for hash, val := range txc {
		if val {
			mined = append(mined, hash)
		} else {
			rollback = append(rollback, hash)
		}
	}
	return
}

// checkMinedTxs checks newly added blocks for the currently pending transactions
// and marks them as mined if necessary. It also stores block position in the db
// and adds them to the received txStateChanges map.
func (pool *TxPool) checkMinedTxs(ctx context.Context, hash util.Hash, number uint64, txc txStateChanges) error {
	// If no transactions are pending, we don't care about anything
	if len(pool.pending) == 0 {
		return nil
	}
	block, err := GetBlock(ctx, pool.odr, hash, number)
	if err != nil {
		return err
	}
	// Gather all the local transaction mined in this block
	list := pool.mined[hash]
	for _, tx := range block.Transactions() {
		if _, ok := pool.pending[tx.Hash()]; ok {
			list = append(list, tx)
		}
	}
	// If some transactions have been mined, write the needed data to disk and update
	if list != nil {
		// Retrieve all the receipts belonging to this block and write the loopup table
		if _, err := GetBlockReceipts(ctx, pool.odr, hash, number); err != nil { // ODR caches, ignore results
			return err
		}
		rawdb.WriteTxLookupEntriesByBlock(pool.chainDb, block)

		// Update the transaction pool's state
		for _, tx := range list {
			delete(pool.pending, tx.Hash())
			txc.setState(tx.Hash(), true)
		}
		pool.mined[hash] = list
	}
	return nil
}

// rollbackTxs marks the transactions contained in recently rolled back blocks
// as rolled back. It also removes any positional lookup entries.
func (pool *TxPool) rollbackTxs(hash util.Hash, txc txStateChanges) {
	batch := pool.chainDb.NewBatch()
	if list, ok := pool.mined[hash]; ok {
		for _, tx := range list {
			txHash := tx.Hash()
			rawdb.DeleteTxLookupEntry(batch, txHash)
			pool.pending[txHash] = tx
			txc.setState(txHash, false)
		}
		delete(pool.mined, hash)
	}
	batch.Write()
}

// reorgOnNewHead sets a new head header, processing (and rolling back if necessary)
// the blocks since the last known head and returns a txStateChanges map containing
// the recently mined and rolled back transaction hashes. If an error (context
// timeout) occurs during checking new blocks, it leaves the locally known head
// at the latest checked block and still returns a valid txStateChanges, making it
// possible to continue checking the missing blocks at the next chain head event
func (pool *TxPool) reorgOnNewHead(ctx context.Context, newHeader *types.Header) (txStateChanges, error) {
	txc := make(txStateChanges)
	oldh := pool.chain.GetHeaderByHash(pool.head)
	newh := newHeader
	// find common ancestor, create list of rolled back and new block hashes
	var oldHashes, newHashes []util.Hash
	for oldh.Hash() != newh.Hash() {
		if oldh.Number.Uint64() >= newh.Number.Uint64() {
			oldHashes = append(oldHashes, oldh.Hash())
			oldh = pool.chain.GetHeader(oldh.ParentHash, oldh.Number.Uint64()-1)
		}
		if oldh.Number.Uint64() < newh.Number.Uint64() {
			newHashes = append(newHashes, newh.Hash())
			newh = pool.chain.GetHeader(newh.ParentHash, newh.Number.Uint64()-1)
			if newh == nil {
				// happens when CHT syncing, nothing to do
				newh = oldh
			}
		}
	}
	if oldh.Number.Uint64() < pool.clearIdx {
		pool.clearIdx = oldh.Number.Uint64()
	}
	// roll back old blocks
	for _, hash := range oldHashes {
		pool.rollbackTxs(hash, txc)
	}
	pool.head = oldh.Hash()
	// check mined txs of new blocks (array is in reversed order)
	for i := len(newHashes) - 1; i >= 0; i-- {
		hash := newHashes[i]
		if err := pool.checkMinedTxs(ctx, hash, newHeader.Number.Uint64()-uint64(i), txc); err != nil {
			return txc, err
		}
		pool.head = hash
	}

	// clear old mined tx entries of old blocks
	if idx := newHeader.Number.Uint64(); idx > pool.clearIdx+txPermanent {
		idx2 := idx - txPermanent
		if len(pool.mined) > 0 {
			for i := pool.clearIdx; i < idx2; i++ {
				hash := rawdb.ReadCanonicalHash(pool.chainDb, i)
				if list, ok := pool.mined[hash]; ok {
					hashes := make([]util.Hash, len(list))
					for i, tx := range list {
						hashes[i] = tx.Hash()
					}
					pool.relay.Discard(hashes)
					delete(pool.mined, hash)
				}
			}
		}
		pool.clearIdx = idx2
	}

	return txc, nil
}

// blockCheckTimeout is the time limit for checking new blocks for mined
// transactions. Checking resumes at the next chain head event if timed out.
const blockCheckTimeout = time.Second * 3

// eventLoop processes chain head events and also notifies the tx relay backend
// about the new head hash and tx state changes
func (pool *TxPool) eventLoop() {
	for {
		select {
		case ev := <-pool.chainHeadCh:
			pool.setNewHead(ev.Block.Header())
			// hack in order to avoid hogging the lock; this part will
			// be replaced by a subsequent PR.
			time.Sleep(time.Millisecond)

		// System stopped
		case <-pool.chainHeadSub.Err():
			return
		}
	}
}

func (pool *TxPool) setNewHead(head *types.Header) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), blockCheckTimeout)
	defer cancel()

	txc, _ := pool.reorgOnNewHead(ctx, head)
	m, r := txc.getLists()
	pool.relay.NewHead(pool.head, m, r)

	// Update fork indicator by next pending block number
	next := new(big.Int).Add(head.Number, big.NewInt(1))
	pool.istanbul = pool.config.IsIstanbul(next)
	pool.eip2718 = pool.config.IsBerlin(next)
}

// Stop stops the light transaction pool
func (pool *TxPool) Stop() {
	// Unsubscribe all subscriptions registered from txpool
	pool.scope.Close()
	// Unsubscribe subscriptions registered from blockchain
	pool.chainHeadSub.Unsubscribe()
	close(pool.quit)
	logging.Info("Transaction pool stopped")
}

// SubscribeNewTxsEvent registers a subscription of core.NewTxsEvent and
// starts sending event to the given channel.
func (pool *TxPool) SubscribeNewTxsEvent(ch chan<- validation.NewTxsEvent) event.Subscription {
	return pool.scope.Track(pool.txFeed.Subscribe(ch))
}

// Stats returns the number of currently pending (locally created) transactions
func (pool *TxPool) Stats() (pending int) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	pending = len(pool.pending)
	return
}

// validateTx checks whether a transaction is valid according to the consensus rules.
func (pool *TxPool) validateTx(ctx context.Context, tx *types.Transaction) error {
	// Validate sender
	var (
		from util.Address
		err  error
	)

	// Validate the transaction sender and it's sig. Throw
	// if the from fields is invalid.
	if from, err = types.Sender(pool.signer, tx); err != nil {
		return validation.ErrInvalidSender
	}
	// Last but not least check for nonce errors
	currentState := pool.currentState(ctx)
	if n := currentState.GetNonce(from); n > tx.Nonce() {
		return validation.ErrNonceTooLow
	}

	// Check the transaction doesn't exceed the current
	// block limit gas.
	header := pool.chain.GetHeaderByHash(pool.head)
	if header.GasLimit < tx.Gas() {
		return validation.ErrGasLimit
	}

	// Transactions can't be negative. This may never happen
	// using RLP decoded transactions but may occur if you create
	// a transaction using the RPC for example.
	if tx.Value().Sign() < 0 {
		return validation.ErrNegativeValue
	}

	// Transactor should have enough funds to cover the costs
	// cost == V + GP * GL
	if b := currentState.GetBalance(from); b.Cmp(tx.Cost()) < 0 {
		return validation.ErrInsufficientFunds
	}

	// Should supply enough intrinsic gas
	gas, err := validation.IntrinsicGas(tx.Data(), tx.AccessList(), tx.To() == nil, true, pool.istanbul)
	if err != nil {
		return err
	}
	if tx.Gas() < gas {
		return validation.ErrIntrinsicGas
	}
	return currentState.Error()
}

// add validates a new transaction and sets its state pending if processable.
// It also updates the locally stored nonce if necessary.
func (pool *TxPool) add(ctx context.Context, tx *types.Transaction) error {
	hash := tx.Hash()

	if pool.pending[hash] != nil {
		return fmt.Errorf("Known transaction (%x)", hash[:4])
	}
	err := pool.validateTx(ctx, tx)
	if err != nil {
		return err
	}

	if _, ok := pool.pending[hash]; !ok {
		pool.pending[hash] = tx

		nonce := tx.Nonce() + 1

		addr, _ := types.Sender(pool.signer, tx)
		if nonce > pool.nonce[addr] {
			pool.nonce[addr] = nonce
		}

		// Notify the subscribers. This event is posted in a goroutine
		// because it's possible that somewhere during the post "Remove transaction"
		// gets called which will then wait for the global tx pool lock and deadlock.
		go pool.txFeed.Send(validation.NewTxsEvent{Txs: types.Transactions{tx}})
	}

	// Print a log message if low enough level is set
	logging.Debug("Pooled new transaction", "hash", hash, "from", logging.Lazy{Fn: func() util.Address { from, _ := types.Sender(pool.signer, tx); return from }}, "to", tx.To())
	return nil
}

// Add adds a transaction to the pool if valid and passes it to the tx relay
// backend
func (pool *TxPool) Add(ctx context.Context, tx *types.Transaction) error {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	data, err := tx.MarshalBinary()
	if err != nil {
		return err
	}

	if err := pool.add(ctx, tx); err != nil {
		return err
	}
	// fmt.Println("Send", tx.Hash())
	pool.relay.Send(types.Transactions{tx})

	pool.chainDb.Put(tx.Hash().Bytes(), data)
	return nil
}

// AddTransactions adds all valid transactions to the pool and passes them to
// the tx relay backend
func (pool *TxPool) AddBatch(ctx context.Context, txs []*types.Transaction) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	var sendTx types.Transactions

	for _, tx := range txs {
		if err := pool.add(ctx, tx); err == nil {
			sendTx = append(sendTx, tx)
		}
	}
	if len(sendTx) > 0 {
		pool.relay.Send(sendTx)
	}
}

// GetTransaction returns a transaction if it is contained in the pool
// and nil otherwise.
func (pool *TxPool) GetTransaction(hash util.Hash) *types.Transaction {
	// check the txs first
	if tx, ok := pool.pending[hash]; ok {
		return tx
	}
	return nil
}

// Get returns a transaction by hash, or nil if not present in the pool.
// Wraps the pending map under pool.mu to satisfy gasprice.TxPoolAccessor.
func (pool *TxPool) Get(hash util.Hash) *types.Transaction {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	if tx, ok := pool.pending[hash]; ok {
		return tx
	}
	return nil
}

// GetTransactions returns all currently processable transactions.
// The returned slice may be modified by the caller.
func (pool *TxPool) GetTransactions() (txs types.Transactions, err error) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	txs = make(types.Transactions, len(pool.pending))
	i := 0
	for _, tx := range pool.pending {
		txs[i] = tx
		i++
	}
	return txs, nil
}

// Content retrieves the data content of the transaction pool, returning all the
// pending as well as queued transactions, grouped by account and nonce.
func (pool *TxPool) Content() (map[util.Address]types.Transactions, map[util.Address]types.Transactions) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	// Retrieve all the pending transactions and sort by account and by nonce
	pending := make(map[util.Address]types.Transactions)
	for _, tx := range pool.pending {
		account, _ := types.Sender(pool.signer, tx)
		pending[account] = append(pending[account], tx)
	}
	// There are no queued transactions in a light pool, just return an empty map
	queued := make(map[util.Address]types.Transactions)
	return pending, queued
}

// ContentFrom retrieves the data content of the transaction pool, returning the
// pending as well as queued transactions of this address, grouped by nonce.
func (pool *TxPool) ContentFrom(addr util.Address) (types.Transactions, types.Transactions) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	// Retrieve the pending transactions and sort by nonce
	var pending types.Transactions
	for _, tx := range pool.pending {
		account, _ := types.Sender(pool.signer, tx)
		if account != addr {
			continue
		}
		pending = append(pending, tx)
	}
	// There are no queued transactions in a light pool, just return an empty map
	return pending, types.Transactions{}
}

// RemoveTransactions removes all given transactions from the pool.
func (pool *TxPool) RemoveTransactions(txs types.Transactions) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	var hashes []util.Hash
	batch := pool.chainDb.NewBatch()
	for _, tx := range txs {
		hash := tx.Hash()
		delete(pool.pending, hash)
		batch.Delete(hash.Bytes())
		hashes = append(hashes, hash)
	}
	batch.Write()
	pool.relay.Discard(hashes)
}

// RemoveTx removes the transaction with the given hash from the pool.
func (pool *TxPool) RemoveTx(hash util.Hash) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	// delete from pending pool
	delete(pool.pending, hash)
	pool.chainDb.Delete(hash[:])
	pool.relay.Discard([]util.Hash{hash})
}
