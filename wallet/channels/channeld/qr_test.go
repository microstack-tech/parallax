package channeld

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/util"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/qrenc"
)

// TestOfflineThreeQRSale is the Phase C exit gate (Part 2 §11.2): a complete
// sale with zero network on either side — no pool running, no chain backend,
// three QR payloads exchanged as strings:
//
//  1. merchant displays  QR[invoice]
//  2. payer scans, signs, PERSISTS, displays QR[proposal]
//  3. merchant scans, validates, countersigns, PERSISTS, displays QR[ack]
//  4. payer scans, verifies, PERSISTS -> payment COMPLETE
func TestOfflineThreeQRSale(t *testing.T) {
	h := newHub() // never connected: nodes stay fully offline
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	// Step 1: the merchant (bob) mints a pinned invoice and renders it.
	inv, _, err := bob.CreateInvoice(big.NewInt(777), "flat white", 10*time.Minute, e2eKey.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	invoiceQR, err := bob.QRInvoice(inv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(invoiceQR, qrenc.Prefix) {
		t.Fatalf("bad code: %s", invoiceQR)
	}

	// Step 2: the payer scans it, signs seq 1, and produces the proposal.
	res, err := alice.ScanQR(invoiceQR)
	if err != nil {
		t.Fatal(err)
	}
	if res.Next == "" || res.Complete != nil {
		t.Fatalf("payer step: %+v", res)
	}
	// W1 held: the proposal is journaled before the code exists.
	journal, _ := alice.Store.SelfSigned(e2eKey)
	if len(journal) != 1 || journal[0].Seq != 1 {
		t.Fatalf("W1 journal: %+v", journal)
	}
	proposalQR := res.Next

	// Step 3: the merchant scans, validates against last-known confirmed
	// deposits, countersigns, and produces the receipt.
	res, err = bob.ScanQR(proposalQR)
	if err != nil {
		t.Fatal(err)
	}
	if res.Next == "" || res.Complete == nil {
		t.Fatalf("merchant step: %+v", res)
	}
	// W2 held: bob's complete state is on disk before the ack code exists.
	lb, err := bob.Store.LatestState(e2eKey)
	if err != nil || lb.Seq != 1 || !lb.Complete() {
		t.Fatalf("W2 state: %+v %v", lb, err)
	}
	paid, _ := bob.Store.Invoice(inv.ID)
	if !paid.Paid || paid.PaidSeq != 1 {
		t.Fatalf("invoice not marked paid: %+v", paid)
	}
	ackQR := res.Next

	// Step 4: the payer scans the receipt — only now is it COMPLETE.
	res, err = alice.ScanQR(ackQR)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete == nil || res.Complete.Seq != 1 || res.Next != "" {
		t.Fatalf("payer completion: %+v", res)
	}
	la, _ := alice.Store.LatestState(e2eKey)
	if la.TransferredAtoB.BigInt().Int64() != 777 {
		t.Fatalf("final state: %+v", la)
	}
	if j, _ := alice.Store.SelfSigned(e2eKey); len(j) != 0 {
		t.Fatalf("journal not cleared: %+v", j)
	}

	// A re-scan of the same receipt is idempotent (double-scan at POS).
	if res, err := alice.ScanQR(ackQR); err != nil || res.Complete == nil {
		t.Fatalf("receipt re-scan: %+v %v", res, err)
	}
	// A re-scan of the proposal re-issues the identical receipt.
	res2, err := bob.ScanQR(proposalQR)
	if err != nil || res2.Next != ackQR {
		t.Fatalf("proposal re-scan not idempotent: %v", err)
	}

	// Reconnect catch-up: alice comes back online; her completed state is
	// parked on the relay as a self-backup without any new payment.
	alice.Cfg.Backup.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go alice.Run(ctx, time.Hour)
	waitUntil(t, 5*time.Second, "reconnect backup published", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, ev := range h.all {
			if tag := ev.Tags.Find("p"); tag != nil && tag[1] == alice.SelfPub {
				return true
			}
		}
		return false
	})
}

// TestOfflineStepThreeNeverHappens: the merchant never countersigns — the
// ordinary poisoned case (Part 2 §11.2). The payer's exposure is exactly
// 0.2 x the in-flight amount and the channel refuses further sends.
func TestOfflineStepThreeNeverHappens(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	inv, _, err := bob.CreateInvoice(big.NewInt(500), "", 10*time.Minute, e2eKey.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	invoiceQR, err := bob.QRInvoice(inv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.ScanQR(invoiceQR); err != nil {
		t.Fatal(err)
	}

	// The merchant walks away. Alice's wallet is poisoned by her own
	// signature: no new sends, exact exposure surfaced.
	if err := alice.Engine.MarkPoisoned(e2eKey); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Engine.ProposePayment(e2eKey, big.NewInt(1), "", 0); err == nil {
		t.Fatal("poisoned channel accepted a new payment")
	}
	exposure, err := alice.Engine.PoisonedExposure(e2eKey)
	if err != nil || exposure.Int64() != 100 { // 20% of 500
		t.Fatalf("exposure: %s %v", exposure, err)
	}
}

// TestOfflineCoopCloseQR drives the type-4/5 exchange: both sides end up
// holding the fully signed close for submission once back online.
func TestOfflineCoopCloseQR(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	// Alice proposes the close offline (engine-level; balances from the
	// zero state: 10e9 / 5e9).
	prop, err := alice.Engine.ProposeCoopClose(e2eKey, 5000, 0)
	if err != nil {
		t.Fatal(err)
	}
	balA, _ := new(big.Int).SetString(prop.BalanceA, 10)
	balB, _ := new(big.Int).SetString(prop.BalanceB, 10)
	sig1 := util.FromHex(prop.Sig)
	closeQR, err := qrenc.Encode(qrenc.Envelope{
		Type:      qrenc.TypeCoopCloseProposal,
		Registry:  e2eKey.Registry,
		ChainID:   2110,
		ChannelID: e2eKey.ChannelID,
		Expiry:    5000,
		BalA:      balA,
		BalB:      balB,
		Sig1:      sig1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Bob scans, countersigns, and both freeze.
	res, err := bob.ScanQR(closeQR)
	if err != nil || res.Next == "" {
		t.Fatalf("close countersign: %+v %v", res, err)
	}
	metaB, _ := bob.Store.Meta(e2eKey)
	if metaB.FrozenUntilBlock == 0 || metaB.PendingClose == nil || len(metaB.PendingClose.PeerSig) != 65 {
		t.Fatalf("bob not frozen with full pair: %+v", metaB)
	}

	// Alice scans the countersign: her pending close now holds both sigs.
	if _, err := alice.ScanQR(res.Next); err != nil {
		t.Fatal(err)
	}
	metaA, _ := alice.Store.Meta(e2eKey)
	if metaA.PendingClose == nil || len(metaA.PendingClose.PeerSig) != 65 {
		t.Fatalf("alice missing countersign: %+v", metaA)
	}
	// Frozen: no payments on either side until settled or expired.
	if _, err := alice.Engine.ProposePayment(e2eKey, big.NewInt(1), "", 100); err == nil {
		t.Fatal("frozen channel accepted a payment")
	}
}
