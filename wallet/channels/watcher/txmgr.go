package watcher

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

// TxBackend is the chain access the manager needs.
type TxBackend interface {
	bind.ContractBackend
	bind.DeployBackend // receipt lookups
}

// Fee-bump policy: resubmit an unmined transaction with a 25% higher gas
// price after bumpAfterBlocks, capped at maxBumps (the final price is
// ~9.3x the starting one — beyond that something other than fees is wrong
// and the alarm has long fired).
const (
	bumpAfterBlocks = 2
	bumpPercent     = 25
	maxBumps        = 10
)

// pendingTx is one tracked intent.
type pendingTx struct {
	build       func(*bind.TransactOpts) (*types.Transaction, error)
	nonce       uint64
	gasPrice    *big.Int
	txHash      util.Hash
	submittedAt uint64 // head at last (re)submission
	bumps       int
	deadline    uint64 // 0 = none; alarm fires as head approaches it
}

// TxManager tracks keyed transaction intents until they are mined,
// resubmitting with escalating fees (Part 3 §7: the challenge path is the
// one loss-capable failure, so it never fire-and-forgets).
type TxManager struct {
	backend TxBackend
	key     *ecdsa.PrivateKey
	from    util.Address
	chainID *big.Int

	// Alarm receives operator-grade alerts. Optional.
	Alarm func(format string, args ...any)

	mu      sync.Mutex
	pending map[string]*pendingTx

	// submitMu serializes nonce allocation-to-send across Submit calls, and
	// nextNonce tracks the nonce past the manager's own broadcasts:
	// PendingNonceAt alone lags behind a broadcast the pool has not yet
	// registered, and two intents allocated against that stale view would
	// pick the same nonce — one silently replacing the other.
	submitMu   sync.Mutex
	nextNonce  uint64
	nonceKnown bool
}

func NewTxManager(backend TxBackend, key *ecdsa.PrivateKey, chainID *big.Int) *TxManager {
	return &TxManager{
		backend: backend,
		key:     key,
		from:    crypto.PubkeyToAddress(key.PublicKey),
		chainID: chainID,
		pending: make(map[string]*pendingTx),
	}
}

func (m *TxManager) alarm(format string, args ...any) {
	if m.Alarm != nil {
		m.Alarm(format, args...)
	}
}

// Pending reports whether an intent is being tracked.
func (m *TxManager) Pending(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pending[id]
	return ok
}

// Done drops an intent whose effect was observed on-chain by other means
// (e.g. the counterparty's transaction achieved the same outcome).
func (m *TxManager) Done(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, id)
}

// Submit registers an intent and sends its first transaction. Idempotent
// while the intent is pending. deadline (0 = none) drives the near-deadline
// alarm; the caller stops caring via Done or a mined receipt.
func (m *TxManager) Submit(ctx context.Context, id string, head, deadline uint64,
	build func(*bind.TransactOpts) (*types.Transaction, error)) error {
	m.mu.Lock()
	if _, ok := m.pending[id]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	m.submitMu.Lock()
	defer m.submitMu.Unlock()

	nonce, err := m.backend.PendingNonceAt(ctx, m.from)
	if err != nil {
		return err
	}
	// The higher of the chain's pending view and our own allocation counter:
	// the chain view wins after external transactions or mined history, the
	// counter wins while our last broadcast is still propagating.
	if m.nonceKnown && m.nextNonce > nonce {
		nonce = m.nextNonce
	}
	gasPrice, err := m.backend.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}
	p := &pendingTx{build: build, nonce: nonce, gasPrice: gasPrice, deadline: deadline}
	if err := m.send(ctx, id, p, head); err != nil {
		return err
	}
	m.nextNonce, m.nonceKnown = nonce+1, true
	m.mu.Lock()
	m.pending[id] = p
	m.mu.Unlock()
	return nil
}

func (m *TxManager) send(ctx context.Context, id string, p *pendingTx, head uint64) error {
	auth, err := bind.NewKeyedTransactorWithChainID(m.key, m.chainID)
	if err != nil {
		return err
	}
	auth.Context = ctx
	auth.Nonce = new(big.Int).SetUint64(p.nonce)
	auth.GasPrice = new(big.Int).Set(p.gasPrice)
	tx, err := p.build(auth)
	if err != nil {
		return fmt.Errorf("tx %s: %w", id, err)
	}
	p.txHash = tx.Hash()
	p.submittedAt = head
	return nil
}

// Tick drives every pending intent: reap mined ones, fee-bump stale ones,
// and alarm when a deadline closes in.
func (m *TxManager) Tick(ctx context.Context, head uint64) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.mu.Lock()
		p, ok := m.pending[id]
		m.mu.Unlock()
		if !ok {
			continue
		}

		if receipt, err := m.backend.TransactionReceipt(ctx, p.txHash); err == nil && receipt != nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				m.alarm("transaction %s reverted (tx %s); intent dropped, caller will re-evaluate", id, p.txHash.Hex())
			}
			m.Done(id)
			continue
		}

		if p.deadline > 0 && head+alarmBlocksBeforeDeadline > p.deadline {
			m.alarm("transaction %s still unmined with %d blocks to deadline (tx %s, %d bumps)",
				id, int64(p.deadline)-int64(head), p.txHash.Hex(), p.bumps)
		}

		if head >= p.submittedAt+bumpAfterBlocks && p.bumps < maxBumps {
			// Same nonce, 25% higher price: replaces the stuck transaction.
			p.gasPrice.Add(p.gasPrice, new(big.Int).Div(new(big.Int).Mul(p.gasPrice, big.NewInt(bumpPercent)), big.NewInt(100)))
			p.bumps++
			if err := m.send(ctx, id, p, head); err != nil {
				// "already known" / "underpriced" races are benign; anything
				// else waits for the next tick.
				if !benignResubmitError(err) {
					m.alarm("fee-bump for %s failed: %v", id, err)
				}
				continue
			}
		}
	}
}

func benignResubmitError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "already known") ||
		strings.Contains(s, "underpriced") ||
		strings.Contains(s, "nonce too low") // mined between receipt check and resubmit
}
