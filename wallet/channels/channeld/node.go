package channeld

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
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

	backend Backend
	evmKey  *ecdsa.PrivateKey // retained for on-chain verbs (open/close/withdraw)
	log     *slog.Logger
}

// EVMKey exposes the wallet key for on-chain transaction building.
func (n *Node) EVMKey() *ecdsa.PrivateKey { return n.evmKey }

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
		auth := protocolAuth(evmPriv)
		for label, entries := range cfg.Registries {
			for _, e := range entries {
				w, err := watcher.New(watcher.Config{
					ChainID:       strconv.FormatUint(e.ChainID, 10),
					Registry:      util.HexToAddress(e.Address),
					Confirmations: cfg.Node.Confirmations,
				}, store, backend, auth(e.ChainID))
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

func protocolAuth(priv *ecdsa.PrivateKey) func(chainID uint64) *bind.TransactOpts {
	return func(chainID uint64) *bind.TransactOpts {
		auth, err := bind.NewKeyedTransactorWithChainID(priv, new(big.Int).SetUint64(chainID))
		if err != nil {
			return nil // unreachable: chainID is validated non-zero
		}
		return auth
	}
}

// Close releases the store.
func (n *Node) Close() error {
	return n.Store.Close()
}

// Run starts the long-lived loops: relay pool, transmitter, watcher ticks,
// and the inbound dispatcher. Blocks until ctx is done.
func (n *Node) Run(ctx context.Context, watcherInterval time.Duration) {
	go n.Pool.Run(ctx)
	go n.Transmitter.Run(ctx, time.Second, func(item proofstore.OutboundItem) {
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
	n.dispatchLoop(ctx)
}

func (n *Node) watchLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, w := range n.Watchers {
				head, err := w.Tick(ctx)
				if err != nil {
					n.log.Error("watcher tick", "err", err)
					continue
				}
				n.unfreezeExpired(head)
			}
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
		return n.reactToResult(ctx, res, sender, msg.State.ChannelID)

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
		n.afterCompletion(ctx)
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
		if err := n.reactToResult(ctx, res, sender, msg.ChannelID); err != nil {
			return err
		}
		if ready != nil {
			n.submitCoopClose(ctx, ready)
		}
		return nil

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
func (n *Node) reactToResult(ctx context.Context, res protocol.Result, peer string, channelID string) error {
	if res.Completed != nil {
		n.afterCompletion(ctx)
	}
	var payload any
	var kind int
	switch {
	case res.Ack != nil:
		payload, kind = res.Ack, protocol.KindAck
	case res.Nack != nil:
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

// afterCompletion publishes the self-backup after every completed state
// change (Part 2 §6.10). Tower delegation joins this hook in the tower
// phase.
func (n *Node) afterCompletion(ctx context.Context) {
	if !n.Cfg.Backup.Enabled {
		return
	}
	if _, err := nostrmod.PublishBackup(ctx, n.Store, n.Pool, n.NostrPriv); err != nil {
		n.log.Error("self-backup", "err", err)
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
