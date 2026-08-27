package channeld

import (
	"context"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

// completeOnePayment drives one full payment (payer bob -> merchant alice)
// and delivers the ACK to bob through handleRumor — the dispatcher path
// that triggers the self-backup.
func completeOnePayment(t *testing.T, alice, bob *Node) {
	t.Helper()
	if err := bob.Pay(context.Background(), e2eKey, big.NewInt(1e9), ""); err != nil {
		t.Fatal(err)
	}
	journal, _ := bob.Store.SelfSigned(e2eKey)
	prop := protocol.ProposalMsg{V: 1, State: protocol.ToWire(journal[len(journal)-1]), ProposerRole: "B"}
	res, err := alice.Engine.HandleProposal(prop, bob.SelfPub, 0, time.Now().Unix())
	if err != nil || res.Ack == nil {
		t.Fatalf("countersign: %+v %v", res, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- bob.handleRumor(context.Background(), encodeRumor(t, protocol.KindAck, res.Ack), alice.SelfPub)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher blocked behind the synchronous self-backup")
	}
}

// TestBackupDoesNotBlockDispatcher: the self-backup marshals the whole
// store and round-trips the relays — per completed payment, synchronously,
// on the dispatcher goroutine. Message processing must not sit behind it,
// and completions arriving while one backup is in flight coalesce into a
// single trailing snapshot (the backup is a whole-store snapshot; the last
// one wins anyway).
func TestBackupDoesNotBlockDispatcher(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNodeWith(t, h, func(cfg *Config) {
		cfg.Backup.Enabled = true
	})
	linkChannel(t, alice, bob)

	var runs atomic.Int64
	release := make(chan struct{})
	bob.publishBackup = func(ctx context.Context) error {
		runs.Add(1)
		<-release
		return nil
	}

	// Three payments complete while the first backup hangs on the relay.
	for i := 0; i < 3; i++ {
		completeOnePayment(t, alice, bob)
	}
	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runs.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// First run hung; the two completions behind it coalesce into one
	// trailing snapshot.
	if got := runs.Load(); got != 2 {
		t.Fatalf("backup runs = %d, want 2 (first + one coalesced trailer)", got)
	}
}
