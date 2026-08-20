package channeld

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/keys"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/nostrmod"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/watcher"
)

// Backend is the chain access a Node needs; *client.Client satisfies it, and
// so does the simulated backend in tests.
type Backend interface {
	bind.ContractBackend
	bind.DeployBackend // receipt lookups for WaitMined
}

// Node wires the channel subsystem together for one wallet identity.
type Node struct {
	Cfg    Config
	Store  *proofstore.Store
	Engine *protocol.Engine
	Signer protocol.Signer

	NostrPriv string // hex
	SelfPub   string // x-only hex

	Pool        *nostrmod.Pool
	Transmitter *nostrmod.Transmitter
	Watchers    []*watcher.Watcher

	// TransmitInterval paces the outbound queue drain (default 1s; the
	// per-item backoff still governs retransmission).
	TransmitInterval time.Duration

	// OnPayment fires when an inbound payment completes against an invoice
	// (merchant webhooks, Part 3 §9). Called on the dispatcher goroutine.
	OnPayment func(invoiceID string, state *proofstore.SignedState)

	// DelegationHandler, when set (tower mode), processes inbound 21906
	// messages and returns the 21907 receipt.
	DelegationHandler func(ctx context.Context, msg protocol.TowerDelegationMsg, sender string) (*protocol.TowerReceiptMsg, error)

	backend Backend
	evmKey  *ecdsa.PrivateKey // retained for on-chain verbs (open/close/withdraw)
	log     *slog.Logger
}

// EVMKey exposes the wallet key for on-chain transaction building.
func (n *Node) EVMKey() *ecdsa.PrivateKey { return n.evmKey }

// Backend exposes the chain backend (nil for offline nodes).
func (n *Node) Backend() Backend { return n.backend }

// New assembles a node from configuration, the EVM key, and a data
// directory holding the proof store. The Nostr identity is derived from the
// EVM key (Part 3 §3); backend MAY be nil for offline verbs (QR, restore).
func New(cfg Config, dataDir string, evmPriv *ecdsa.PrivateKey, backend Backend, logger *slog.Logger) (*Node, error) {
	if logger == nil {
		logger = slog.Default()
	}
	store, err := proofstore.Open(filepath.Join(dataDir, "channels.db"))
	if err != nil {
		return nil, err
	}

	nk, err := keys.DeriveNostrKey(crypto.FromECDSA(evmPriv))
	if err != nil {
		store.Close()
		return nil, err
	}
	nostrPriv := hex.EncodeToString(nk.Priv[:])
	selfPub := hex.EncodeToString(nk.PubX[:])

	signer := protocol.NewKeySigner(evmPriv)
	maxInflight, _ := cfg.MaxInflight()
	engine := protocol.New(store, signer, protocol.Config{
		PushPayments:   cfg.Merchant.PushPayments,
		MaxInflightWei: maxInflight,
	})

	n := &Node{
		Cfg:       cfg,
		Store:     store,
		Engine:    engine,
		Signer:    signer,
		NostrPriv: nostrPriv,
		SelfPub:   selfPub,
		backend:   backend,
		evmKey:    evmPriv,
		log:       logger,
	}

	n.Pool = nostrmod.NewPool(nostrmod.GoNostrDialer, cfg.AllRelays(), selfPub, nostrmod.PoolConfig{})
	n.Transmitter = nostrmod.NewTransmitter(store, n.Pool, nostrPriv)

	if backend != nil {
		for label, entries := range cfg.Registries {
			for _, e := range entries {
				w, err := watcher.New(watcher.Config{
					ChainID:       strconv.FormatUint(e.ChainID, 10),
					Registry:      util.HexToAddress(e.Address),
					Confirmations: cfg.Node.Confirmations,
				}, store, backend, evmPriv)
				if err != nil {
					store.Close()
					return nil, fmt.Errorf("channeld: registry %s: %w", label, err)
				}
				w.Alarm = func(format string, args ...any) {
					logger.Error("channel alarm", "msg", fmt.Sprintf(format, args...))
				}
				n.Watchers = append(n.Watchers, w)
			}
		}
	}
	return n, nil
}

// Close releases the store.
func (n *Node) Close() error {
	return n.Store.Close()
}

// Run starts the long-lived loops: relay pool, transmitter, watcher ticks,
// and the inbound dispatcher. Blocks until ctx is done.
func (n *Node) Run(ctx context.Context, watcherInterval time.Duration) {
	transmitInterval := n.TransmitInterval
	if transmitInterval == 0 {
		transmitInterval = time.Second
	}
	go n.Pool.Run(ctx)
	go n.Transmitter.Run(ctx, transmitInterval, func(item proofstore.OutboundItem) {
		n.log.Warn("outbound gave up", "kind", item.Kind, "dedupe", item.DedupeKey)
		if item.Kind == protocol.KindProposal {
			// Give-up on a proposal leaves the channel poisoned (Part 3 §5).
			if key, ok := channelKeyFromDedupe(item.DedupeKey); ok {
				if err := n.Engine.MarkPoisoned(key); err != nil {
					n.log.Error("mark poisoned", "err", err)
				}
			}
		}
	})
	if len(n.Watchers) > 0 {
		go n.watchLoop(ctx, watcherInterval)
	}
	if n.Cfg.Backup.Enabled {
		// Reconnect catch-up (Part 2 §11.2): state that completed offline
		// (QR) gets parked on the relays as soon as we are back.
		go func() {
			for i := 0; i < 30; i++ {
				if n.Pool.Healthy() > 0 {
					n.afterCompletion(ctx, nil)
					return
				}
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	n.dispatchLoop(ctx)
}

func (n *Node) watchLoop(ctx context.Context, interval time.Duration) {
	tick := func() {
		for _, w := range n.Watchers {
			head, err := w.Tick(ctx)
			if err != nil {
				n.log.Error("watcher tick", "err", err)
				continue
			}
			n.unfreezeExpired(head)
		}
	}
	tick() // sync immediately at start: one-shot CLI verbs rely on it
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			tick()
		case <-ctx.Done():
			return
		}
	}
}

func (n *Node) unfreezeExpired(head uint64) {
	metas, err := n.Store.ListChannels()
	if err != nil {
		return
	}
	for _, meta := range metas {
		if meta.FrozenUntilBlock != 0 && head > meta.FrozenUntilBlock {
			if err := n.Engine.Unfreeze(meta.Key, head); err != nil {
				n.log.Error("unfreeze", "channel", meta.Key.String(), "err", err)
			}
		}
		if meta.PendingWithdraw != nil {
			if err := n.Engine.SweepWithdraw(meta.Key, head); err != nil {
				n.log.Error("withdraw sweep", "channel", meta.Key.String(), "err", err)
			}
		}
	}
}

// dispatchLoop unwraps inbound gift wraps and routes rumors to the engine.
func (n *Node) dispatchLoop(ctx context.Context) {
	for {
		select {
		case wrap := <-n.Pool.Events():
			rumor, sender, err := nostrmod.Unwrap(wrap, n.NostrPriv)
			if err != nil {
				continue // not for us / garbage; relays are untrusted
			}
			if err := n.handleRumor(ctx, rumor, sender); err != nil {
				n.log.Warn("rumor rejected", "kind", rumor.Kind, "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (n *Node) handleRumor(ctx context.Context, rumor nostr.Event, sender string) error {
	nowBlock := n.headBlock(ctx)
	switch rumor.Kind {
	case protocol.KindProposal:
		var msg protocol.ProposalMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		res, err := n.Engine.HandleProposal(msg, sender, nowBlock, time.Now().Unix())
		if err != nil {
			return err
		}
		if res.Completed != nil && msg.InvoiceID != "" && n.OnPayment != nil {
			n.OnPayment(msg.InvoiceID, res.Completed)
		}
		return n.reactToResult(ctx, res, sender, msg.State.ChannelID, protocol.KindAck)

	case protocol.KindAck:
		var msg protocol.AckMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		key, ok := n.channelByID(msg.ChannelID)
		if !ok {
			return fmt.Errorf("ack for unknown channel %s", msg.ChannelID)
		}
		complete, err := n.Engine.HandleAck(key, msg)
		if err != nil {
			return err
		}
		n.settleOutbound(protocol.KindProposal, key, complete.Seq)
		n.afterCompletion(ctx, complete)
		return nil

	case protocol.KindNack:
		var msg protocol.NackMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		key, ok := n.channelByID(msg.ChannelID)
		if !ok {
			return nil
		}
		poisoned, err := n.Engine.HandleNack(key, msg)
		if poisoned {
			exposure, _ := n.Engine.PoisonedExposure(key)
			n.log.Warn("channel poisoned by NACK", "channel", key.String(),
				"reason", msg.Reason, "exposureWei", exposure)
		}
		return err

	case protocol.KindCoopCloseProposal:
		var msg protocol.CoopCloseProposalMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		res, ready, err := n.Engine.HandleCoopCloseProposal(msg, sender, nowBlock)
		if err != nil {
			return err
		}
		// Settle on-chain first: we hold the complete pair, and submission
		// must not depend on the relay accepting our countersignature.
		if ready != nil {
			n.submitCoopClose(ctx, ready)
		}
		return n.reactToResult(ctx, res, sender, msg.ChannelID, protocol.KindCoopCloseAck)

	case protocol.KindCoopCloseAck:
		var msg protocol.AckMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		key, ok := n.channelByID(msg.ChannelID)
		if !ok {
			return nil
		}
		ready, err := n.Engine.HandleCoopCloseAck(key, msg)
		if err != nil {
			return err
		}
		n.settleOutbound(protocol.KindCoopCloseProposal, key, 0)
		n.submitCoopClose(ctx, ready)
		return nil

	case protocol.KindInvoice:
		var msg protocol.InvoiceMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		return n.handleInvoice(msg, sender)

	case protocol.KindWithdrawProposal:
		var msg protocol.WithdrawProposalMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		res, ready, err := n.Engine.HandleWithdrawProposal(msg, sender, nowBlock)
		if err != nil {
			return err
		}
		// The proposer is the payee and submits; the responder only returns
		// the countersignature. (ready is the responder's copy of the pair,
		// unused here.)
		_ = ready
		return n.reactToResult(ctx, res, sender, msg.ChannelID, protocol.KindWithdrawAck)

	case protocol.KindWithdrawAck:
		var msg protocol.AckMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		key, ok := n.channelByID(msg.ChannelID)
		if !ok {
			return nil
		}
		ready, err := n.Engine.HandleWithdrawAck(key, msg)
		if err != nil {
			return err
		}
		n.settleOutbound(protocol.KindWithdrawProposal, key, 0)
		if n.backend != nil {
			if err := n.SubmitWithdraw(ctx, ready); err != nil {
				// Not loss-capable: retry on the next ack retransmission or
				// re-propose after expiry.
				n.log.Error("withdraw submission", "err", err)
			}
		}
		return nil

	case protocol.KindTowerDelegation:
		if n.DelegationHandler == nil {
			return nil // not running a tower
		}
		var msg protocol.TowerDelegationMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		receipt, err := n.DelegationHandler(ctx, msg, sender)
		if err != nil {
			return err
		}
		return n.sendDirect(ctx, sender, protocol.KindTowerReceipt, receipt, msg.ChannelID)

	case protocol.KindTowerReceipt:
		var msg protocol.TowerReceiptMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		key, ok := n.channelByID(msg.ChannelID)
		if !ok {
			return nil
		}
		seq, err := strconv.ParseUint(msg.Seq, 10, 64)
		if err != nil {
			return nil
		}
		// Receipts for seq >= our delegation settle its retransmission.
		for s := seq; ; s-- {
			if removed, _ := n.Store.RemoveOutboundByDedupe(watcher.DedupeKeyFor(protocol.KindTowerDelegation, key, s)); removed == 0 && s != seq {
				break
			}
			if s == 0 {
				break
			}
		}
		return nil

	case protocol.KindHandshake:
		var msg protocol.HandshakeMsg
		if err := json.Unmarshal([]byte(rumor.Content), &msg); err != nil {
			return err
		}
		return n.handleHandshake(ctx, msg, sender)

	default:
		return nil // invoices/tower kinds arrive with later phases
	}
}

// reactToResult transmits the engine's response (direct sends, no queue:
// ACKs and NACKs are re-issued idempotently on duplicate proposals instead
// of retransmitted).
func (n *Node) reactToResult(ctx context.Context, res protocol.Result, peer string, channelID string, ackKind int) error {
	if res.Completed != nil {
		n.afterCompletion(ctx, res.Completed)
	}
	if res.AdoptedTiebreak && res.Completed != nil {
		// Our own conflicting proposal at this seq is dead (A-wins tiebreak,
		// Part 2 §7.5); stop retransmitting it. The abandoned payment intent
		// is the caller's to re-issue at a fresh seq.
		n.settleOutbound(protocol.KindProposal, res.Completed.Key, res.Completed.Seq)
		n.log.Info("tiebreak: adopted counterparty state; local intent needs rebase",
			"channel", res.Completed.Key.String(), "seq", res.Completed.Seq)
	}
	var payload any
	var kind int
	switch {
	case res.Ack != nil:
		payload, kind = res.Ack, ackKind
	case res.Nack != nil:
		n.log.Warn("nacking inbound message", "channel", channelID,
			"re", res.Nack.Re, "reason", res.Nack.Reason, "detail", res.Nack.Detail)
		payload, kind = res.Nack, protocol.KindNack
	default:
		return nil
	}
	return n.sendDirect(ctx, peer, kind, payload, channelID)
}

func (n *Node) sendDirect(ctx context.Context, peer string, kind int, payload any, channelID string) error {
	content, err := protocol.EncodePayload(payload)
	if err != nil {
		return err
	}
	rumor := nostr.Event{
		Kind:      kind,
		Content:   content,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"ch", channelID}},
	}
	wrap, err := nostrmod.Wrap(rumor, peer, n.NostrPriv)
	if err != nil {
		return err
	}
	if n.Pool.Publish(ctx, wrap) == 0 {
		return fmt.Errorf("no relay accepted kind %d", kind)
	}
	return nil
}

// afterCompletion runs after every completed state change: self-backup
// (Part 2 §6.10) and tower delegation (Part 2 §9). state MAY be nil (bulk
// catch-up: delegate the latest state of every channel).
func (n *Node) afterCompletion(ctx context.Context, state *proofstore.SignedState) {
	if n.Cfg.Backup.Enabled {
		if _, err := nostrmod.PublishBackup(ctx, n.Store, n.Pool, n.NostrPriv); err != nil {
			n.log.Error("self-backup", "err", err)
		}
	}
	if len(n.Cfg.Channels.Towers.Npubs) == 0 {
		return
	}
	if state != nil {
		n.delegate(*state)
		return
	}
	metas, err := n.Store.ListChannels()
	if err != nil {
		return
	}
	for _, meta := range metas {
		if meta.Status == proofstore.StatusSettled {
			continue
		}
		if latest, err := n.Store.LatestState(meta.Key); err == nil {
			n.delegate(latest)
		}
	}
}

// delegate queues a 21906 for every configured tower; retransmission stops
// on the 21907 receipt.
func (n *Node) delegate(st proofstore.SignedState) {
	msg := protocol.TowerDelegationMsg{
		V:         1,
		Registry:  strings.ToLower(st.Key.Registry.Hex()),
		ChainID:   st.Key.ChainID,
		ChannelID: strconv.FormatUint(st.Key.ChannelID, 10),
		State:     protocol.ToWire(st),
	}
	content, err := protocol.EncodePayload(msg)
	if err != nil {
		n.log.Error("delegation encode", "err", err)
		return
	}
	now := time.Now().Unix()
	for _, npub := range n.Cfg.Channels.Towers.Npubs {
		_, err := n.Store.EnqueueOutbound(proofstore.OutboundItem{
			DedupeKey: watcher.DedupeKeyFor(protocol.KindTowerDelegation, st.Key, st.Seq),
			ToNpub:    npub,
			Kind:      protocol.KindTowerDelegation,
			Content:   content,
			Tags:      [][]string{{"ch", msg.ChannelID}},
			RumorTime: now,
			ExpiresAt: now + 24*3600,
		})
		if err != nil {
			n.log.Error("delegation enqueue", "err", err)
		}
	}
}

func (n *Node) settleOutbound(kind int, key proofstore.ChannelKey, seq uint64) {
	if _, err := n.Store.RemoveOutboundByDedupe(watcher.DedupeKeyFor(kind, key, seq)); err != nil {
		n.log.Error("outbound cleanup", "err", err)
	}
}

func (n *Node) submitCoopClose(ctx context.Context, ready *protocol.CoopCloseReady) {
	n.log.Info("cooperative close fully signed",
		"channel", ready.Key.String(), "balanceA", ready.BalanceA, "balanceB", ready.BalanceB)
	if n.backend == nil {
		return // offline node: the counterparty submits, or the CLI does later
	}
	if err := n.SubmitCoopClose(ctx, ready); err != nil {
		// Not loss-capable: the counterparty holds the same pair and the
		// freeze holds until expiry either way.
		n.log.Error("cooperative close submission", "err", err)
	}
}

// channelByID resolves a bare wire channel id against the store. Ambiguity
// across registries is impossible for ids the store knows (composite keys).
func (n *Node) channelByID(channelID string) (proofstore.ChannelKey, bool) {
	id, err := strconv.ParseUint(channelID, 10, 64)
	if err != nil {
		return proofstore.ChannelKey{}, false
	}
	metas, err := n.Store.ListChannels()
	if err != nil {
		return proofstore.ChannelKey{}, false
	}
	for _, meta := range metas {
		if meta.Key.ChannelID == id {
			return meta.Key, true
		}
	}
	return proofstore.ChannelKey{}, false
}

func (n *Node) headBlock(ctx context.Context) uint64 {
	if n.backend == nil {
		return 0
	}
	header, err := n.backend.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0
	}
	return header.Number.Uint64()
}

// channelKeyFromDedupe parses watcher.DedupeKeyFor's
// "<kind>:<chainId>:<registry>:<channelId>:<seq>" format.
func channelKeyFromDedupe(dedupe string) (proofstore.ChannelKey, bool) {
	parts := strings.Split(dedupe, ":")
	if len(parts) != 5 || !util.IsHexAddress(parts[2]) {
		return proofstore.ChannelKey{}, false
	}
	channelID, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return proofstore.ChannelKey{}, false
	}
	return proofstore.ChannelKey{
		ChainID:   parts[1],
		Registry:  util.HexToAddress(parts[2]),
		ChannelID: channelID,
	}, true
}
