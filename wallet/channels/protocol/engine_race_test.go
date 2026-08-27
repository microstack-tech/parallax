package protocol

import (
	"errors"
	"math/big"
	"sync"
	"testing"
)

// TestConcurrentProposalsSerialized: Engine methods are called from several
// goroutines in practice (dispatcher, watcher loop, transmitter give-up
// callback, merchantd HTTP handlers, CLI verbs), so the engine must
// serialize itself. Two racing ProposePayment calls each observe an empty
// journal and both try to sign seq S+1: the store's W3 guard stops the
// double-journal, but the caller-visible contract breaks — either both
// "succeed" or one fails with an equivocation error for a perfectly
// ordinary second payment. Exactly one may win; the other must see the
// one-in-flight rule (ErrProposalPending).
func TestConcurrentProposalsSerialized(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{PushPayments: true})

	amounts := []*big.Int{big.NewInt(1e9), big.NewInt(2e9)}
	for round := 0; round < 100; round++ {
		start := make(chan struct{})
		errs := make([]error, len(amounts))
		var wg sync.WaitGroup
		for i, amt := range amounts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = alice.engine.ProposePayment(key, amt, "", nowBlock)
			}()
		}
		close(start)
		wg.Wait()

		wins := 0
		for _, err := range errs {
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrProposalPending):
			default:
				t.Fatalf("round %d: racing proposal saw %v, want ErrProposalPending", round, err)
			}
		}
		if wins != 1 {
			t.Fatalf("round %d: %d of 2 racing proposals succeeded, want exactly 1", round, wins)
		}
		journal, err := alice.store.SelfSigned(key)
		if err != nil || len(journal) != 1 {
			t.Fatalf("round %d: journal %+v %v", round, journal, err)
		}

		// Complete the winner so the next round starts clean.
		prop := ProposalMsg{V: 1, State: ToWire(journal[0]), ProposerRole: "A"}
		res, err := bob.engine.HandleProposal(prop, alice.npub, nowBlock, nowUnix)
		if err != nil || res.Ack == nil {
			t.Fatalf("round %d: countersign %+v %v", round, res, err)
		}
		if _, err := alice.engine.HandleAck(key, *res.Ack); err != nil {
			t.Fatal(err)
		}
	}
}
