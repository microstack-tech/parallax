package channeld

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind/backends"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/validation"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// laggingRecordingBackend serves PendingNonceAt from a stale snapshot (the
// pool has not yet registered prior broadcasts) and records the nonce of
// every transaction actually sent.
type laggingRecordingBackend struct {
	Backend
	staleNonce uint64

	mu     sync.Mutex
	nonces []uint64
}

func (b *laggingRecordingBackend) PendingNonceAt(ctx context.Context, account util.Address) (uint64, error) {
	return b.staleNonce, nil
}

func (b *laggingRecordingBackend) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	b.mu.Lock()
	b.nonces = append(b.nonces, tx.Nonce())
	b.mu.Unlock()
	return b.Backend.SendTransaction(ctx, tx)
}

func (b *laggingRecordingBackend) sent() []uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]uint64(nil), b.nonces...)
}

// TestVerbsShareManagerNonceAllocation: node.go declares that everything
// submitting with the node's key on a chain MUST go through the shared
// per-chain manager. The on-chain verbs (Deposit here; open/close/withdraw
// take the same path) must therefore allocate nonces through it too — bind's
// auto-nonce reads PendingNonceAt directly, and against a lagging pool view
// it reuses the nonce of a challenge the manager just broadcast, one
// transaction silently replacing the other.
func TestVerbsShareManagerNonceAllocation(t *testing.T) {
	alicePriv, _ := crypto.GenerateKey()
	nodePriv, _ := crypto.GenerateKey()
	alice := crypto.PubkeyToAddress(alicePriv.PublicKey)
	nodeAddr := crypto.PubkeyToAddress(nodePriv.PublicKey)

	backend := backends.NewSimulatedBackend(validation.GenesisAlloc{
		alice:    {Balance: lax(100)},
		nodeAddr: {Balance: lax(100)},
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
	auth.Value = lax(10)
	if _, err := contract.Open(auth, nodeAddr, 40); err != nil {
		t.Fatal(err)
	}
	auth.Value = nil
	backend.Commit()

	base, err := backend.PendingNonceAt(context.Background(), nodeAddr)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &laggingRecordingBackend{Backend: backend, staleNonce: base}

	cfg := DefaultConfig()
	cfg.Registries = map[string][]RegistryEntry{
		"v1": {{Address: regAddr.Hex(), ChainID: 1337}},
	}
	cfg.Nostr.Relays = []string{"wss://hub"}
	n, err := New(cfg, t.TempDir(), nodePriv, wrapped, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	// The watcher path holds an unmined challenge-style intent at the base
	// nonce; the pool "has not registered it yet" (staleNonce never moves).
	mgr := n.TxManagerFor("1337")
	if mgr == nil {
		t.Fatal("no shared manager for chain 1337")
	}
	mgrContract, err := registry.NewChannelRegistry(regAddr, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	err = mgr.Submit(context.Background(), "challenge:test", 10, 0,
		func(auth *bind.TransactOpts) (*types.Transaction, error) {
			auth.Value = lax(1)
			return mgrContract.Deposit(auth, big.NewInt(1))
		})
	if err != nil {
		t.Fatal(err)
	}

	// Mine in the background so the verb's WaitMined returns.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
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

	key := proofstore.ChannelKey{ChainID: "1337", Registry: regAddr, ChannelID: 1}
	if err := n.Deposit(ctx, key, lax(1)); err != nil {
		t.Fatalf("deposit raced the manager's pending challenge: %v", err)
	}

	nonces := wrapped.sent()
	if len(nonces) != 2 {
		t.Fatalf("expected two broadcasts, got %d (%v)", len(nonces), nonces)
	}
	if nonces[0] == nonces[1] {
		t.Fatalf("nonce collision: verb reused the pending challenge's nonce %d", nonces[0])
	}
	if nonces[1] != nonces[0]+1 {
		t.Fatalf("nonces not sequential: %v", nonces)
	}
}
