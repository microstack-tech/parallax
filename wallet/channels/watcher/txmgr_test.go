package watcher

import (
	"context"
	"math/big"
	"sync"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// droppingBackend swallows the first N SendTransaction calls (the mempool
// "loses" them) while recording every attempted gas price.
type droppingBackend struct {
	TxBackend
	mu        sync.Mutex
	dropFirst int
	attempts  []*big.Int
}

func (d *droppingBackend) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attempts = append(d.attempts, new(big.Int).Set(tx.GasPrice()))
	if len(d.attempts) <= d.dropFirst {
		return nil // accepted but never mined
	}
	return d.TxBackend.SendTransaction(ctx, tx)
}

func (d *droppingBackend) attemptCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.attempts)
}

// TestTxManagerFeeBumpsUntilMined: the first two submissions vanish; the
// manager escalates the gas price on the same nonce until one lands.
func TestTxManagerFeeBumpsUntilMined(t *testing.T) {
	e := setupSim(t)
	dropping := &droppingBackend{TxBackend: e.backend, dropFirst: 2}
	mgr := NewTxManager(dropping, e.bobPriv, big.NewInt(1337))
	var alarms []string
	mgr.Alarm = func(format string, args ...any) { alarms = append(alarms, format) }

	// Bind the contract through the dropping backend: bind sends via the
	// contract's own backend, so interception must happen there.
	contract, err := registry.NewChannelRegistry(e.regAddr, dropping)
	if err != nil {
		t.Fatal(err)
	}
	head := uint64(10)
	// Intent: bob deposits into channel 1 (any state-changing call works).
	err = mgr.Submit(context.Background(), "deposit:test", head, 0,
		func(auth *bind.TransactOpts) (*types.Transaction, error) {
			auth.Value = lax(1)
			return contract.Deposit(auth, big.NewInt(1))
		})
	if err != nil {
		t.Fatal(err)
	}
	if dropping.attemptCount() != 1 {
		t.Fatalf("attempts after submit: %d", dropping.attemptCount())
	}

	// Two ticks past the bump threshold: two escalations; the third attempt
	// reaches the real backend.
	mgr.Tick(context.Background(), head+bumpAfterBlocks)
	mgr.Tick(context.Background(), head+2*bumpAfterBlocks)
	if dropping.attemptCount() != 3 {
		t.Fatalf("attempts after bumps: %d", dropping.attemptCount())
	}
	dropping.mu.Lock()
	p0, p1, p2 := dropping.attempts[0], dropping.attempts[1], dropping.attempts[2]
	dropping.mu.Unlock()
	if p1.Cmp(p0) <= 0 || p2.Cmp(p1) <= 0 {
		t.Fatalf("gas price not escalating: %s %s %s", p0, p1, p2)
	}
	want := new(big.Int).Div(new(big.Int).Mul(p0, big.NewInt(125)), big.NewInt(100))
	if p1.Cmp(want) != 0 {
		t.Fatalf("bump 1: got %s want %s", p1, want)
	}

	// Mine it; the next tick reaps the receipt.
	e.backend.Commit()
	mgr.Tick(context.Background(), head+2*bumpAfterBlocks+1)
	if mgr.Pending("deposit:test") {
		t.Fatal("intent still pending after mining")
	}
	onchain, err := e.contract.GetChannel(nil, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if onchain.DepositB.Cmp(lax(6)) != 0 { // 5 from setup + 1 now
		t.Fatalf("deposit not applied: %s", onchain.DepositB)
	}
}

// TestTxManagerDeadlineAlarm: an unmined intent alarms as its deadline
// approaches.
func TestTxManagerDeadlineAlarm(t *testing.T) {
	e := setupSim(t)
	dropping := &droppingBackend{TxBackend: e.backend, dropFirst: 1000} // never lands
	mgr := NewTxManager(dropping, e.bobPriv, big.NewInt(1337))
	var alarmed bool
	mgr.Alarm = func(format string, args ...any) { alarmed = true }

	contract, err := registry.NewChannelRegistry(e.regAddr, dropping)
	if err != nil {
		t.Fatal(err)
	}
	head := uint64(10)
	deadline := head + 20
	err = mgr.Submit(context.Background(), "challenge:test", head, deadline,
		func(auth *bind.TransactOpts) (*types.Transaction, error) {
			auth.Value = lax(1)
			return contract.Deposit(auth, big.NewInt(1))
		})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Tick(context.Background(), head+2)
	if alarmed {
		t.Fatal("alarmed too early")
	}
	mgr.Tick(context.Background(), deadline-alarmBlocksBeforeDeadline+1)
	if !alarmed {
		t.Fatal("no alarm near deadline")
	}
}
