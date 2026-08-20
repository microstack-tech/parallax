// Package chantest provides in-process test fixtures for the channel stack:
// a hub relay (publishes route by p-tag, retained history replays on
// subscribe) and a linked two-node pair. Test-only.
package chantest

import (
	"context"
	"crypto/ecdsa"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/channeld"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/nostrmod"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// Hub is an in-process relay.
type Hub struct {
	mu   sync.Mutex
	subs map[string][]chan nostr.Event
	all  []nostr.Event
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string][]chan nostr.Event)}
}

// Dialer returns a nostrmod dialer serving this hub.
func (h *Hub) Dialer() nostrmod.RelayDialer {
	return func(ctx context.Context, url string) (nostrmod.RelayConn, error) {
		return &hubConn{h: h}, nil
	}
}

type hubConn struct {
	h *Hub
}

func (c *hubConn) Subscribe(ctx context.Context, selfPub string, since nostr.Timestamp) (<-chan nostr.Event, error) {
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	var history []nostr.Event
	for _, ev := range c.h.all {
		if tag := ev.Tags.Find("p"); tag != nil && tag[1] == selfPub {
			history = append(history, ev)
		}
	}
	ch := make(chan nostr.Event, len(history)+64)
	for _, ev := range history {
		ch <- ev
	}
	c.h.subs[selfPub] = append(c.h.subs[selfPub], ch)
	return ch, nil
}

func (c *hubConn) Publish(ctx context.Context, ev nostr.Event) error {
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	c.h.all = append(c.h.all, ev)
	if tag := ev.Tags.Find("p"); tag != nil {
		for _, ch := range c.h.subs[tag[1]] {
			select {
			case ch <- ev:
			default:
			}
		}
	}
	return nil
}

func (c *hubConn) Close() error { return nil }

// Party is one wallet node in the fixture.
type Party struct {
	Node *channeld.Node
	Key  *ecdsa.PrivateKey
}

// Env is a linked two-node pair sharing one channel over one hub.
// Alice is participant A (the payer in merchant scenarios, push payments
// allowed); Bob is participant B (the merchant, invoices required).
type Env struct {
	Hub        *Hub
	Alice, Bob *Party
	Key        proofstore.ChannelKey
}

var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// NewPair builds the fixture: channel 1, A deposited 10e9, B 5e9, both
// confirmed.
func NewPair(t *testing.T) *Env {
	t.Helper()
	env := &Env{
		Hub: NewHub(),
		Key: proofstore.ChannelKey{
			ChainID:   "2110",
			Registry:  util.HexToAddress("0x00000000000000000000000000000000000021ff"),
			ChannelID: 1,
		},
	}
	env.Alice = env.newParty(t, true)
	env.Bob = env.newParty(t, false)

	deposits := proofstore.Deposits{
		DepositA:         proofstore.NewU256(big.NewInt(10e9)),
		DepositB:         proofstore.NewU256(big.NewInt(5e9)),
		LastScannedBlock: 1,
	}
	for _, p := range []struct {
		who  *Party
		role proofstore.Role
		peer *Party
	}{{env.Alice, proofstore.RoleA, env.Bob}, {env.Bob, proofstore.RoleB, env.Alice}} {
		err := p.who.Node.Store.CreateChannel(proofstore.ChannelMeta{
			Key:             env.Key,
			Role:            p.role,
			Status:          proofstore.StatusOpen,
			PeerNpub:        p.peer.Node.SelfPub,
			PeerAddress:     p.peer.Node.Signer.Address(),
			ChallengePeriod: 144,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := p.who.Node.Store.PutDeposits(env.Key, deposits); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func (e *Env) newParty(t *testing.T, pushPayments bool) *Party {
	t.Helper()
	cfg := channeld.DefaultConfig()
	cfg.Registries = map[string][]channeld.RegistryEntry{
		"v1": {{Address: e.Key.Registry.Hex(), ChainID: 2110}},
	}
	cfg.Nostr.Relays = []string{"wss://hub"}
	cfg.Merchant.PushPayments = pushPayments
	cfg.Backup.Enabled = false

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	node, err := channeld.New(cfg, t.TempDir(), key, nil, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	node.Pool = nostrmod.NewPool(e.Hub.Dialer(), []string{"wss://hub"}, node.SelfPub, nostrmod.PoolConfig{})
	node.Transmitter = nostrmod.NewTransmitter(node.Store, node.Pool, node.NostrPriv)
	node.TransmitInterval = 5 * time.Millisecond
	return &Party{Node: node, Key: key}
}

// WaitConnected blocks until both pools hold their relay connection.
func (e *Env) WaitConnected(t *testing.T) {
	t.Helper()
	e.WaitUntil(t, "relays connected", func() bool {
		return e.Alice.Node.Pool.Healthy() == 1 && e.Bob.Node.Pool.Healthy() == 1
	})
}

// WaitUntil polls cond until true or a 15s deadline.
func (e *Env) WaitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
