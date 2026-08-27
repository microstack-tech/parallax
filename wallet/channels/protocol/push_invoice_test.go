package protocol

import (
	"math/big"
	"testing"
)

// TestPushPaymentsStillValidatesKnownInvoices: push-payments mode waives the
// invoice REQUIREMENT (a proposal with no invoice is fine) — it must not
// waive validation of an invoice the proposal does reference. A merchant
// running push mode with an open 2-LAX invoice must not have it flipped to
// Paid (webhook fired, goods shipped) by a 1-wei proposal naming its id.
func TestPushPaymentsStillValidatesKnownInvoices(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{PushPayments: true})
	invoiceFor(t, bob, "inv1", wei2)

	prop, err := alice.engine.ProposePayment(key, big.NewInt(1), "inv1", nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res, err := bob.engine.HandleProposal(*prop, alice.npub, nowBlock, nowUnix)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nack == nil || res.Nack.Reason != NackPolicy {
		t.Fatalf("mismatched amount against a known invoice accepted under push payments: %+v", res)
	}
	if inv, _ := bob.store.Invoice("inv1"); inv.Paid {
		t.Fatal("invoice marked paid by a proposal that does not pay it")
	}

	// The push freedoms survive: cure the poisoned journal, then pay with no
	// invoice at all, and with an id this merchant never minted.
	super, err := alice.engine.ProposeNoOpSupersession(key)
	if err != nil {
		t.Fatal(err)
	}
	res, err = bob.engine.HandleProposal(*super, alice.npub, nowBlock, nowUnix)
	if err != nil || res.Nack != nil {
		t.Fatalf("supersession refused: %+v %v", res, err)
	}
	if _, err := alice.engine.HandleAck(key, *res.Ack); err != nil {
		t.Fatal(err)
	}

	pay(t, alice, bob, key, wei1, "") // no invoice: push accepts

	prop, err = alice.engine.ProposePayment(key, wei1, "not-minted-here", nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res, err = bob.engine.HandleProposal(*prop, alice.npub, nowBlock, nowUnix)
	if err != nil || res.Nack != nil {
		t.Fatalf("unknown invoice id refused under push payments: %+v %v", res, err)
	}
	if _, err := alice.engine.HandleAck(key, *res.Ack); err != nil {
		t.Fatal(err)
	}

	// And the genuine payment against the invoice still completes and marks
	// it paid exactly once.
	pay(t, alice, bob, key, wei2, "inv1")
	if inv, _ := bob.store.Invoice("inv1"); !inv.Paid {
		t.Fatal("genuine invoice payment not credited")
	}
}
