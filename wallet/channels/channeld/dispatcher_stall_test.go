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
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// TestInboundSubmissionDoesNotStallDispatcher: countersigning an inbound
// coop-close ends in an on-chain submission that waits for the transaction
// to mine — at 10-minute blocks that is >=10 minutes of wall clock, and any
// counterparty can trigger it at will. The dispatcher goroutine must keep
// processing other channels' messages meanwhile: here the close's
// transaction never mines (the miner stopped), and a payment on a second
// channel still has to complete.
func TestInboundSubmissionDoesNotStallDispatcher(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	minerCtx, stopMiner := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				backend.Commit()
			case <-minerCtx.Done():
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

	// Channel 1 lives on-chain (the coop close must be submittable).
	key, err := nodeA.OpenChannel(ctx, 1337, regAddr, bob, nodeB.SelfPub, lax(10), 144)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "handshake accepted", func() bool {
		_, err := nodeB.Store.Meta(key)
		return err == nil
	})
	waitUntil(t, 10*time.Second, "deposit credited", func() bool {
		da, _ := nodeA.Store.Deposits(key)
		return da.DepositA.BigInt().Cmp(lax(10)) == 0
	})

	// Channel 2 is store-only: payments need no chain.
	key2 := key
	key2.ChannelID = 2
	linkChannelAt(t, nodeA, nodeB, key2)

	// The miner stops: whatever alice submits from here on never mines.
	stopMiner()
	time.Sleep(50 * time.Millisecond)

	// Bob proposes the coop close on channel 1; alice countersigns and
	// starts the on-chain submission, which cannot complete.
	if err := nodeB.CoopClose(ctx, key); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "alice countersigned the close", func() bool {
		meta, err := nodeA.Store.Meta(key)
		return err == nil && meta.PendingClose != nil
	})

	// A payment on channel 2 must still flow through alice's dispatcher.
	if err := nodeB.Pay(ctx, key2, big.NewInt(1e9), ""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 8*time.Second, "payment on the second channel while the close submission is pending", func() bool {
		latest, err := nodeB.Store.LatestState(key2)
		return err == nil && latest.Seq == 1
	})
	if _, err := nodeA.Store.LatestState(key2); err != nil {
		t.Fatal(err)
	}
	// The close stayed unmined the whole time — the channel is still open
	// on-chain, only frozen locally.
	if meta, _ := nodeA.Store.Meta(key); meta.Status != proofstore.StatusOpen {
		t.Fatalf("close mined unexpectedly: %s", meta.Status)
	}
}
