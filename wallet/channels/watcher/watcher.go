// Package watcher is the chain-facing loop (Part 3 §7): it credits deposits
// and withdrawals at confirmation depth, tracks channel status from
// canonical logs, auto-challenges stale closes with the latest complete
// state, and settles own channels after the deadline.
//
// v1 scope: polling scan against a bind.ContractBackend (fine at 10-minute
// blocks), single-shot transaction submission with per-tick re-evaluation.
// The keyed tx manager with fee-bumping and the reusable watchcore
// extraction arrive with the tower work.
package watcher

import (
	"context"
	"fmt"
	"math/big"
	"strconv"

	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// ChannelState mirrors the contract enum.
const (
	stateNonExistent = 0
	stateOpen        = 1
	stateClosing     = 2
)

// alarmBlocksBeforeDeadline is the hard-alarm threshold: a challenge still
// unmined this close to the deadline is the one loss-capable failure
// (Part 3 §13).
const alarmBlocksBeforeDeadline = 12

// Config wires one watcher to one registry deployment.
type Config struct {
	ChainID       string // decimal, must match the stored ChannelKeys
	Registry      util.Address
	Confirmations uint64 // act on logs at this depth (default 3)
}

// Watcher watches one registry for every channel in the store that belongs
// to it. Auth MAY be nil for a watch-only instance (no challenge/settle).
type Watcher struct {
	cfg      Config
	store    *proofstore.Store
	backend  bind.ContractBackend
	contract *registry.ChannelRegistry
	auth     *bind.TransactOpts

	// Alarm receives operator-grade alerts (stale close observed, challenge
	// window nearly exhausted). Optional.
	Alarm func(format string, args ...any)

	// submitted dedupes in-flight transactions per channel until their
	// effect is visible on-chain.
	submitted map[proofstore.ChannelKey]string
}

func New(cfg Config, store *proofstore.Store, backend bind.ContractBackend, auth *bind.TransactOpts) (*Watcher, error) {
	if cfg.Confirmations == 0 {
		cfg.Confirmations = 3
	}
	contract, err := registry.NewChannelRegistry(cfg.Registry, backend)
	if err != nil {
		return nil, err
	}
	return &Watcher{
		cfg:       cfg,
		store:     store,
		backend:   backend,
		contract:  contract,
		auth:      auth,
		submitted: make(map[proofstore.ChannelKey]string),
	}, nil
}

func (w *Watcher) alarm(format string, args ...any) {
	if w.Alarm != nil {
		w.Alarm(format, args...)
	}
}

// Tick performs one scan-and-act pass and returns the confirmed head it
// acted on.
func (w *Watcher) Tick(ctx context.Context) (uint64, error) {
	header, err := w.backend.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	head := header.Number.Uint64()
	if head+1 < w.cfg.Confirmations {
		return head, nil
	}
	cutoff := head - (w.cfg.Confirmations - 1) // logs at or below are confirmed

	channels, err := w.store.ListChannels()
	if err != nil {
		return head, err
	}
	for _, meta := range channels {
		if meta.Key.ChainID != w.cfg.ChainID || meta.Key.Registry != w.cfg.Registry {
			continue
		}
		if meta.Status == proofstore.StatusSettled {
			continue
		}
		if err := w.scanChannel(ctx, meta, cutoff); err != nil {
			return head, fmt.Errorf("scan channel %s: %w", meta.Key, err)
		}
		if err := w.actOnChannel(ctx, meta.Key, head); err != nil {
			return head, fmt.Errorf("act on channel %s: %w", meta.Key, err)
		}
	}
	return head, nil
}

// scanChannel advances the confirmed-log watermark, crediting funding
// figures idempotently (cumulative totals are set, never added — a re-scan
// recomputes, never patches).
func (w *Watcher) scanChannel(ctx context.Context, meta proofstore.ChannelMeta, cutoff uint64) error {
	dep, err := w.store.Deposits(meta.Key)
	if err != nil {
		return err
	}
	from := dep.LastScannedBlock + 1
	if dep.LastScannedBlock == 0 {
		from = meta.OpenedAtBlock
	}
	if from > cutoff {
		return nil
	}
	opts := &bind.FilterOpts{Context: ctx, Start: from, End: &cutoff}
	id := []*big.Int{new(big.Int).SetUint64(meta.Key.ChannelID)}

	opened, err := w.contract.FilterChannelOpened(opts, id, nil, nil)
	if err != nil {
		return err
	}
	for opened.Next() {
		dep.DepositA = proofstore.NewU256(opened.Event.Deposit)
	}

	// participantIsA resolves an event participant to a column: the only
	// addresses the store knows are the peer's and, by elimination, ours.
	participantIsA := func(participant util.Address) bool {
		if participant == meta.PeerAddress {
			return meta.Role == proofstore.RoleB
		}
		return meta.Role == proofstore.RoleA
	}

	deposits, err := w.contract.FilterChannelDeposit(opts, id, nil)
	if err != nil {
		return err
	}
	for deposits.Next() {
		ev := deposits.Event
		if participantIsA(ev.Participant) {
			dep.DepositA = proofstore.NewU256(ev.NewTotal)
		} else {
			dep.DepositB = proofstore.NewU256(ev.NewTotal)
		}
	}

	withdraws, err := w.contract.FilterChannelWithdraw(opts, id, nil)
	if err != nil {
		return err
	}
	for withdraws.Next() {
		ev := withdraws.Event
		if participantIsA(ev.Participant) {
			dep.WithdrawnA = proofstore.NewU256(ev.TotalWithdrawn)
		} else {
			dep.WithdrawnB = proofstore.NewU256(ev.TotalWithdrawn)
		}
	}

	closes, err := w.contract.FilterCloseStarted(opts, id, nil)
	if err != nil {
		return err
	}
	for closes.Next() {
		ev := closes.Event
		if err := w.store.UpdateMeta(meta.Key, func(m *proofstore.ChannelMeta) {
			m.Status = proofstore.StatusClosing
		}); err != nil {
			return err
		}
		// Always alert on a stale close, even when auto-challenge will
		// succeed (Part 3 §13).
		if latest, lerr := w.store.LatestState(meta.Key); lerr == nil && ev.Seq < latest.Seq {
			w.alarm("stale CloseStarted on %s: on-chain seq %d < local %d (closer %s)",
				meta.Key, ev.Seq, latest.Seq, ev.Closer.Hex())
		}
	}

	settled, err := w.contract.FilterSettled(opts, id)
	if err != nil {
		return err
	}
	coop, err := w.contract.FilterCooperativeClosed(opts, id)
	if err != nil {
		return err
	}
	if settled.Next() || coop.Next() {
		if err := w.store.UpdateMeta(meta.Key, func(m *proofstore.ChannelMeta) {
			m.Status = proofstore.StatusSettled
			m.FrozenUntilBlock = 0
			m.PendingClose = nil
		}); err != nil {
			return err
		}
	}

	dep.LastScannedBlock = cutoff
	return w.store.PutDeposits(meta.Key, dep)
}

// actOnChannel reads the authoritative contract state and, when the channel
// is disputing, challenges with the latest complete state and settles after
// the deadline.
func (w *Watcher) actOnChannel(ctx context.Context, key proofstore.ChannelKey, head uint64) error {
	meta, err := w.store.Meta(key)
	if err != nil {
		return err
	}
	if meta.Status != proofstore.StatusClosing || w.auth == nil {
		return nil
	}

	onchain, err := w.contract.GetChannel(&bind.CallOpts{Context: ctx}, new(big.Int).SetUint64(key.ChannelID))
	if err != nil {
		return err
	}
	switch onchain.State {
	case stateNonExistent:
		// Deleted on-chain: settled (or coop-closed) and already handled by
		// the log scan once confirmed; nothing to act on.
		return nil
	case stateClosing:
	default:
		return nil
	}

	deadline := onchain.CloseInitiatedAtBlock.Uint64() + uint64(onchain.ChallengePeriodBlocks)

	latest, err := w.store.LatestState(key)
	if err != nil {
		// No complete state: nothing to challenge with; settle when due.
		latest = proofstore.SignedState{}
	}

	if latest.Seq > onchain.ClosingSeq && head <= deadline {
		if w.pending(key, "challenge") {
			if head+alarmBlocksBeforeDeadline > deadline {
				w.alarm("challenge for %s still unconfirmed with %d blocks to deadline", key, deadline-head)
			}
			return nil
		}
		tx, err := w.contract.Challenge(w.auth,
			new(big.Int).SetUint64(key.ChannelID),
			registry.ParallaxChannelRegistryBalanceProof{
				ChannelId:       new(big.Int).SetUint64(key.ChannelID),
				Seq:             latest.Seq,
				TransferredAtoB: latest.TransferredAtoB.BigInt(),
				TransferredBtoA: latest.TransferredBtoA.BigInt(),
				LocksRoot:       latest.LocksRoot,
				LockedAmount:    latest.LockedAmount.BigInt(),
			},
			latest.SigA, latest.SigB)
		if err != nil {
			return fmt.Errorf("challenge: %w", err)
		}
		w.markPending(key, "challenge", tx.Hash().Hex())
		return nil
	}
	if latest.Seq <= onchain.ClosingSeq {
		w.clearPending(key, "challenge")
	}

	if head > deadline {
		if w.pending(key, "settle") {
			return nil
		}
		tx, err := w.contract.Settle(w.auth, new(big.Int).SetUint64(key.ChannelID))
		if err != nil {
			return fmt.Errorf("settle: %w", err)
		}
		w.markPending(key, "settle", tx.Hash().Hex())
	}
	return nil
}

func pendingKey(key proofstore.ChannelKey, action string) proofstore.ChannelKey {
	// Piggyback the action into the ChainID field of a copy — cheap composite
	// map key without a new type.
	key.ChainID = key.ChainID + "/" + action
	return key
}

func (w *Watcher) pending(key proofstore.ChannelKey, action string) bool {
	_, ok := w.submitted[pendingKey(key, action)]
	return ok
}

func (w *Watcher) markPending(key proofstore.ChannelKey, action, txHash string) {
	w.submitted[pendingKey(key, action)] = txHash
}

func (w *Watcher) clearPending(key proofstore.ChannelKey, action string) {
	delete(w.submitted, pendingKey(key, action))
}

// DedupeKeyFor names outbound-queue entries the watcher settles implicitly.
func DedupeKeyFor(kind int, key proofstore.ChannelKey, seq uint64) string {
	return strconv.Itoa(kind) + ":" + key.String() + ":" + strconv.FormatUint(seq, 10)
}
