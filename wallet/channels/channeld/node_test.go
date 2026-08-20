package channeld

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/nostrmod"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

// hub is an in-process relay: publishes route to subscribers by p-tag.
type hub struct {
	mu          sync.Mutex
	subs        map[string][]chan nostr.Event // recipient pubkey -> inboxes
	all         []nostr.Event                 // everything published (retention)
	maxRetained int                           // 0 = unlimited; chaos runs cap replay cost
}

func newHub() *hub {
	return &hub{subs: make(map[string][]chan nostr.Event)}
}

func (h *hub) dialer() nostrmod.RelayDialer {
	return func(ctx context.Context, url string) (nostrmod.RelayConn, error) {
		return &hubConn{h: h}, nil
	}
}

type hubConn struct {
	h *hub
}

func (c *hubConn) Subscribe(ctx context.Context, selfPub string, since nostr.Timestamp) (<-chan nostr.Event, error) {
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	// Replay retained history (lookback), then live. The channel must hold
	// the full replay: the consumer only starts after Subscribe returns.
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
	if c.h.maxRetained > 0 && len(c.h.all) > c.h.maxRetained {
		c.h.all = append([]nostr.Event(nil), c.h.all[len(c.h.all)-c.h.maxRetained:]...)
	}
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

var e2eKey = proofstore.ChannelKey{
	ChainID:   "2110",
	Registry:  util.HexToAddress("0x00000000000000000000000000000000000021ff"),
	ChannelID: 1,
}

// newTestNode builds a Node on the hub with no chain backend. A nil key
// generates a fresh identity.
func newTestNode(t *testing.T, h *hub, key *ecdsa.PrivateKey) *Node {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Registries = map[string][]RegistryEntry{
		"v1": {{Address: e2eKey.Registry.Hex(), ChainID: 2110}},
	}
	cfg.Nostr.Relays = []string{"wss://hub"}
	cfg.Merchant.PushPayments = true

	if key == nil {
		var err error
		key, err = crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
	}
	n, err := New(cfg, t.TempDir(), key, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close() })
	// Swap the production dialer for the hub.
	n.Pool = newHubPool(h, n.SelfPub)
	n.Transmitter = newHubTransmitter(n)
	return n
}

func newHubPool(h *hub, selfPub string) *nostrmod.Pool {
	return nostrmod.NewPool(h.dialer(), []string{"wss://hub"}, selfPub, nostrmod.PoolConfig{})
}

func newHubTransmitter(n *Node) *nostrmod.Transmitter {
	return nostrmod.NewTransmitter(n.Store, n.Pool, n.NostrPriv)
}

func decodeHandshake(t *testing.T, content string) protocol.HandshakeMsg {
	t.Helper()
	var msg protocol.HandshakeMsg
	if err := json.Unmarshal([]byte(content), &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func linkChannel(t *testing.T, a, b *Node) {
	t.Helper()
	deposits := proofstore.Deposits{
		DepositA:         proofstore.NewU256(big.NewInt(10e9)),
		DepositB:         proofstore.NewU256(big.NewInt(5e9)),
		LastScannedBlock: 1,
	}
	for _, p := range []struct {
		n    *Node
		role proofstore.Role
		peer *Node
	}{{a, proofstore.RoleA, b}, {b, proofstore.RoleB, a}} {
		err := p.n.Store.CreateChannel(proofstore.ChannelMeta{
			Key:             e2eKey,
			Role:            p.role,
			Status:          proofstore.StatusOpen,
			PeerNpub:        p.peer.SelfPub,
			PeerAddress:     p.peer.Signer.Address(),
			ChallengePeriod: 144,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := p.n.Store.PutDeposits(e2eKey, deposits); err != nil {
			t.Fatal(err)
		}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestEndToEndPaymentOverRelay drives one payment through the entire stack
// on both sides: engine -> W1 persist -> queue -> transmitter -> wrap ->
// relay -> pool -> unwrap -> responder validation -> W2 persist -> ACK ->
// completion -> queue cleanup -> self-backup.
func TestEndToEndPaymentOverRelay(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go alice.Run(ctx, time.Hour)
	go bob.Run(ctx, time.Hour)
	waitUntil(t, 3*time.Second, "relays connected", func() bool {
		return alice.Pool.Healthy() == 1 && bob.Pool.Healthy() == 1
	})

	amount := big.NewInt(3e9)
	if err := alice.Pay(ctx, e2eKey, amount, ""); err != nil {
		t.Fatal(err)
	}

	// Both sides converge on the completed state.
	waitUntil(t, 10*time.Second, "payment completion", func() bool {
		la, errA := alice.Store.LatestState(e2eKey)
		lb, errB := bob.Store.LatestState(e2eKey)
		return errA == nil && errB == nil && la.Seq == 1 && lb.Seq == 1 &&
			la.TransferredAtoB.BigInt().Cmp(amount) == 0 &&
			lb.TransferredAtoB.BigInt().Cmp(amount) == 0
	})

	// The proposal's queue entry is cleaned up by the ACK.
	waitUntil(t, 5*time.Second, "queue drained", func() bool {
		n, _ := alice.Store.OutboundLen()
		return n == 0
	})
	// No journal residue, no poison.
	journal, _ := alice.Store.SelfSigned(e2eKey)
	if len(journal) != 0 {
		t.Fatalf("journal residue: %+v", journal)
	}
	meta, _ := alice.Store.Meta(e2eKey)
	if meta.Poisoned {
		t.Fatal("poisoned after clean payment")
	}

	// Self-backups were published to the relay (wraps addressed to self).
	waitUntil(t, 5*time.Second, "self-backup published", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		selfWraps := 0
		for _, ev := range h.all {
			if tag := ev.Tags.Find("p"); tag != nil &&
				(tag[1] == alice.SelfPub || tag[1] == bob.SelfPub) {
				selfWraps++
			}
		}
		return selfWraps >= 4 // proposal + ack + >=2 backups
	})
}

// TestEndToEndRestoreFromRelayBackup wipes alice and restores her state from
// the hub's retained self-backup.
func TestEndToEndRestoreFromRelayBackup(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go alice.Run(ctx, time.Hour)
	go bob.Run(ctx, time.Hour)
	waitUntil(t, 3*time.Second, "relays connected", func() bool {
		return alice.Pool.Healthy() == 1 && bob.Pool.Healthy() == 1
	})

	if err := alice.Pay(ctx, e2eKey, big.NewInt(3e9), ""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "payment completion", func() bool {
		la, err := alice.Store.LatestState(e2eKey)
		return err == nil && la.Seq == 1
	})
	// Wait for a third wrap addressed to alice: the ACK, the startup
	// backup, and the post-completion backup (which carries seq 1).
	waitUntil(t, 5*time.Second, "backup on relay", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		toAlice := 0
		for _, ev := range h.all {
			if tag := ev.Tags.Find("p"); tag != nil && tag[1] == alice.SelfPub {
				toAlice++
			}
		}
		return toAlice >= 3
	})
	cancel() // alice's machine dies

	// Total storage loss: a fresh node with the same EVM key (hence the same
	// derived npub) restores from the relay-retained backup, then W3 and all
	// flags resume.
	aliceKey := alice.EVMKey()
	alice2 := newTestNode(t, h, aliceKey)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go alice2.Pool.Run(ctx2)
	waitUntil(t, 3*time.Second, "restore relay connected", func() bool {
		return alice2.Pool.Healthy() == 1
	})

	n, err := alice2.RestoreFromBackups(ctx2, 2*time.Second)
	if err != nil || n != 1 {
		t.Fatalf("restore: %d %v", n, err)
	}
	latest, err := alice2.Store.LatestState(e2eKey)
	if err != nil || latest.Seq != 1 || latest.TransferredAtoB.BigInt().Int64() != 3e9 {
		t.Fatalf("restored state wrong: %+v %v", latest, err)
	}
	meta, err := alice2.Store.Meta(e2eKey)
	if err != nil || meta.Role != proofstore.RoleA || meta.PeerNpub != bob.SelfPub {
		t.Fatalf("restored meta wrong: %+v %v", meta, err)
	}
}
