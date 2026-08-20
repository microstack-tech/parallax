package channeld

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind/backends"
	"github.com/ParallaxProtocol/parallax/v2/validation"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// TestEndToEndWithdraw drives the full 21911/21912 negotiation over the
// relay and the resulting cooperativeWithdraw on the real contract: bob
// (participant B) pulls 2 LAX out of the channel while it stays open.
func TestEndToEndWithdraw(t *testing.T) {
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
	regAddr, _, contract, err := registry.DeployChannelRegistry(auth, backend, big.NewInt(1e16))
	if err != nil {
		t.Fatal(err)
	}
	backend.Commit()

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

	key, err := nodeA.OpenChannel(ctx, 1337, regAddr, bob, nodeB.SelfPub, lax(10), 144)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "handshake accepted", func() bool {
		_, err := nodeB.Store.Meta(key)
		return err == nil
	})
	if err := nodeB.Deposit(ctx, key, lax(5)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "deposits credited", func() bool {
		da, _ := nodeA.Store.Deposits(key)
		db, _ := nodeB.Store.Deposits(key)
		return da.DepositB.BigInt().Cmp(lax(5)) == 0 && db.DepositB.BigInt().Cmp(lax(5)) == 0
	})

	// A payment shifts entitlements first: alice pays bob 1 LAX.
	if err := nodeA.Pay(ctx, key, lax(1), ""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "payment completion", func() bool {
		lb, err := nodeB.Store.LatestState(key)
		return err == nil && lb.Seq == 1
	})

	// Bob withdraws 2 LAX (entitlement: 5 + 1 = 6).
	bobBefore, _ := backend.BalanceAt(ctx, bob, nil)
	if err := nodeB.Withdraw(ctx, key, lax(2)); err != nil {
		t.Fatal(err)
	}

	// The negotiation crosses the relay, alice countersigns, bob submits,
	// and both watchers credit the confirmed withdrawal.
	waitUntil(t, 15*time.Second, "withdrawal confirmed on both sides", func() bool {
		da, _ := nodeA.Store.Deposits(key)
		db, _ := nodeB.Store.Deposits(key)
		return da.WithdrawnB.BigInt().Cmp(lax(2)) == 0 && db.WithdrawnB.BigInt().Cmp(lax(2)) == 0
	})

	onchain, err := contract.GetChannel(nil, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if onchain.WithdrawnB.Cmp(lax(2)) != 0 || onchain.State != 1 {
		t.Fatalf("on-chain: withdrawnB %s state %d", onchain.WithdrawnB, onchain.State)
	}
	bobAfter, _ := backend.BalanceAt(ctx, bob, nil)
	got := new(big.Int).Sub(bobAfter, bobBefore)
	// Bob received 2 LAX minus the submission gas.
	if got.Cmp(new(big.Int).Sub(lax(2), lax(1))) < 0 || got.Cmp(lax(2)) > 0 {
		t.Fatalf("bob delta out of range: %s", got)
	}

	// The pending record sweeps once the confirmed figures catch up.
	waitUntil(t, 10*time.Second, "pending withdraw swept", func() bool {
		meta, err := nodeB.Store.Meta(key)
		return err == nil && meta.PendingWithdraw == nil
	})

	// Channel balances still reconcile: available dropped to 14, bob's
	// entitlement now 4, and a further payment still flows.
	if err := nodeA.Pay(ctx, key, lax(1), ""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "post-withdraw payment", func() bool {
		lb, err := nodeB.Store.LatestState(key)
		return err == nil && lb.Seq == 2
	})
	balA, balB, err := nodeB.Engine.CloseBalances(key)
	if err != nil {
		t.Fatal(err)
	}
	if balA.Cmp(lax(8)) != 0 || balB.Cmp(lax(5)) != 0 {
		t.Fatalf("post-withdraw balances: %s / %s", balA, balB)
	}
}
