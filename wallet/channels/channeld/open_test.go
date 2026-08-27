package channeld

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind/backends"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/validation"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

func lax(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e18))
}

// newChainNode builds a Node against a simulated chain and the relay hub.
func newChainNode(t *testing.T, h *hub, key *ecdsa.PrivateKey, backend Backend, regAddr util.Address) *Node {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Registries = map[string][]RegistryEntry{
		"v1": {{Address: regAddr.Hex(), ChainID: 1337}},
	}
	cfg.Nostr.Relays = []string{"wss://hub"}
	cfg.Merchant.PushPayments = true
	cfg.Backup.Enabled = false // keep the hub log readable
	// The auto-miner commits every 20ms (~50 blocks/s): block-denominated
	// validity windows must scale with block time (Part 3 §11 note), and so
	// must the horizon that bounds them.
	cfg.Channels.CoopCloseValidityBlocks = 10_000
	cfg.Channels.WithdrawValidityBlocks = 10_000
	cfg.Channels.CoopCloseHorizonBlocks = 20_000

	n, err := New(cfg, t.TempDir(), key, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close() })
	n.Pool = newHubPool(h, n.SelfPub)
	n.Transmitter = newHubTransmitter(n)
	return n
}

// TestEndToEndOpenHandshakePay is the full Phase B stack in one flow:
// on-chain open (real contract, simulated chain) -> 21908 handshake over the
// relay -> consent checks against the chain -> watcher deposit crediting at
// 3 conf on both sides -> a payment completing over the relay.
func TestEndToEndOpenHandshakePay(t *testing.T) {
	alicePriv, _ := crypto.GenerateKey()
	bobPriv, _ := crypto.GenerateKey()
	alice := crypto.PubkeyToAddress(alicePriv.PublicKey)
	bob := crypto.PubkeyToAddress(bobPriv.PublicKey)

	backend := backends.NewSimulatedBackend(validation.GenesisAlloc{
		alice: {Balance: lax(100)},
		bob:   {Balance: lax(100)},
	}, 30_000_000)
	defer backend.Close()

	auth, err := bind.NewKeyedTransactorWithChainID(alicePriv, big.NewInt(1337))
	if err != nil {
		t.Fatal(err)
	}
	regAddr, _, _, err := registry.DeployChannelRegistry(auth, backend, big.NewInt(1e16))
	if err != nil {
		t.Fatal(err)
	}
	backend.Commit()

	// Auto-miner: keeps WaitMined and confirmation depth moving.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				backend.Commit()
			case <-ctx.Done():
				return
			}
		}
	}()

	h := newHub()
	nodeA := newChainNode(t, h, alicePriv, backend, regAddr)
	nodeB := newChainNode(t, h, bobPriv, backend, regAddr)
	go nodeA.Run(ctx, 50*time.Millisecond)
	go nodeB.Run(ctx, 50*time.Millisecond)
	waitUntil(t, 3*time.Second, "relays connected", func() bool {
		return nodeA.Pool.Healthy() == 1 && nodeB.Pool.Healthy() == 1
	})

	// Alice opens on-chain with 10 LAX and hands Bob the handshake.
	key, err := nodeA.OpenChannel(ctx, 1337, regAddr, bob, nodeB.SelfPub, lax(10), 144)
	if err != nil {
		t.Fatal(err)
	}

	// Bob's node accepts the channel only after the on-chain consent checks.
	waitUntil(t, 10*time.Second, "handshake accepted", func() bool {
		meta, err := nodeB.Store.Meta(key)
		return err == nil && meta.Role == proofstore.RoleB && meta.PeerAddress == alice
	})

	// Both watchers credit Alice's deposit at 3 confirmations.
	waitUntil(t, 10*time.Second, "deposits credited", func() bool {
		da, _ := nodeA.Store.Deposits(key)
		db, _ := nodeB.Store.Deposits(key)
		return da.DepositA.BigInt().Cmp(lax(10)) == 0 && db.DepositA.BigInt().Cmp(lax(10)) == 0
	})

	// A real payment completes across the relay.
	if err := nodeA.Pay(ctx, key, lax(2), ""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "payment completion", func() bool {
		la, errA := nodeA.Store.LatestState(key)
		lb, errB := nodeB.Store.LatestState(key)
		return errA == nil && errB == nil && la.Seq == 1 && lb.Seq == 1 &&
			la.TransferredAtoB.BigInt().Cmp(lax(2)) == 0
	})
}

// TestHandshakeRejectedOnPolicyOrForgery covers the consent checks: hostile
// challenge periods and unbound npubs never create a channel.
func TestHandshakeRejectedOnPolicyOrForgery(t *testing.T) {
	alicePriv, _ := crypto.GenerateKey()
	bobPriv, _ := crypto.GenerateKey()
	alice := crypto.PubkeyToAddress(alicePriv.PublicKey)
	bob := crypto.PubkeyToAddress(bobPriv.PublicKey)

	backend := backends.NewSimulatedBackend(validation.GenesisAlloc{
		alice: {Balance: lax(100)},
	}, 30_000_000)
	defer backend.Close()
	auth, _ := bind.NewKeyedTransactorWithChainID(alicePriv, big.NewInt(1337))
	regAddr, _, contract, err := registry.DeployChannelRegistry(auth, backend, big.NewInt(1e16))
	if err != nil {
		t.Fatal(err)
	}
	backend.Commit()

	h := newHub()
	nodeA := newChainNode(t, h, alicePriv, backend, regAddr)
	nodeB := newChainNode(t, h, bobPriv, backend, regAddr)
	ctx := context.Background()

	// Channel 1: hostile 4320-block period (above bob's accept_max 1008).
	auth.Value = lax(1)
	if _, err := contract.Open(auth, bob, 4320); err != nil {
		t.Fatal(err)
	}
	// Channel 2: fine period, used for the forgery case.
	if _, err := contract.Open(auth, bob, 144); err != nil {
		t.Fatal(err)
	}
	auth.Value = nil
	backend.Commit()

	// Build handshakes as alice would...
	if err := nodeA.sendHandshake(proofstore.ChannelKey{ChainID: "1337", Registry: regAddr, ChannelID: 1}, nodeB.SelfPub); err != nil {
		t.Fatal(err)
	}
	due, _, _ := nodeA.Store.DueOutbound(time.Now().Unix(), 10)
	if len(due) != 1 {
		t.Fatalf("queued: %d", len(due))
	}

	// Deliver the hostile-period handshake directly to bob's handler.
	msg := decodeHandshake(t, due[0].Content)
	if err := nodeB.handleHandshake(ctx, msg, nodeA.SelfPub); err == nil {
		t.Fatal("hostile challenge period accepted")
	}

	// Forgery: mallory replays alice's linkage on channel 2 from her own
	// npub — the linkage binds alice's npub, not mallory's.
	msg2 := msg
	msg2.ChannelID = "2"
	if err := nodeB.handleHandshake(ctx, msg2, "6d616c6c6f72796d616c6c6f72796d616c6c6f72796d616c6c6f72796d616c6c"); err == nil {
		t.Fatal("forged sender accepted")
	}

	// The genuine channel-2 handshake from alice passes.
	if err := nodeB.handleHandshake(ctx, msg2, nodeA.SelfPub); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeB.Store.Meta(proofstore.ChannelKey{ChainID: "1337", Registry: regAddr, ChannelID: 2}); err != nil {
		t.Fatal("genuine handshake did not create the channel")
	}
}
