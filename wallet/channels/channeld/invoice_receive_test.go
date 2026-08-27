package channeld

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

func invoiceMsgFrom(merchant *Node, id, amount string) protocol.InvoiceMsg {
	return protocol.InvoiceMsg{
		V:          1,
		InvoiceID:  id,
		Amount:     amount,
		EVMAddress: strings.ToLower(merchant.Signer.Address().Hex()),
		Registry:   strings.ToLower(e2eKey.Registry.Hex()),
		ChainID:    e2eKey.ChainID,
		ExpiresAt:  strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
	}
}

// TestHandleInvoiceRejectsMalformedPin: an unparsable channel pin was
// silently discarded and the invoice recorded as pinned to an arbitrary
// first-match channel — PayInvoice then proposes on a channel the merchant
// NACKs after the payer's irrevocable journal write. A pin that cannot be
// parsed must reject the whole invoice.
func TestHandleInvoiceRejectsMalformedPin(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	msg := invoiceMsgFrom(bob, "badpin", "1000")
	msg.ChannelID = "not-a-number"
	if err := alice.handleInvoice(msg, bob.SelfPub); err == nil {
		t.Fatal("malformed channel pin accepted")
	}
	if _, err := alice.Store.Invoice("badpin"); !errors.Is(err, proofstore.ErrNotFound) {
		t.Fatalf("malformed-pin invoice stored: %v", err)
	}

	// A pin naming a channel this wallet does not hold with the merchant is
	// equally unpayable — fail closed, the retransmission retries later.
	msg = invoiceMsgFrom(bob, "nosuch", "1000")
	msg.ChannelID = "9"
	if err := alice.handleInvoice(msg, bob.SelfPub); err == nil {
		t.Fatal("pin to an unknown channel accepted")
	}
}

// TestHandleInvoiceKeepsUnpinnedInvoicesUnpinned: an invoice the merchant
// left unpinned was recorded as pinned to whichever channel listed first —
// if that one is closed, paying fails even though another open channel with
// the merchant exists.
func TestHandleInvoiceKeepsUnpinnedInvoicesUnpinned(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)               // channel 1 (sorts first)
	key2 := linkSecondChannel(t, alice, bob) // channel 2, stays open

	// Channel 1 settled: only channel 2 can pay.
	if err := alice.Store.UpdateMeta(e2eKey, func(m *proofstore.ChannelMeta) {
		m.Status = proofstore.StatusSettled
	}); err != nil {
		t.Fatal(err)
	}

	if err := alice.handleInvoice(invoiceMsgFrom(bob, "unpinned", "1000"), bob.SelfPub); err != nil {
		t.Fatal(err)
	}
	inv, err := alice.Store.Invoice("unpinned")
	if err != nil {
		t.Fatal(err)
	}
	if inv.ChannelID != 0 {
		t.Fatalf("unpinned invoice recorded pinned to channel %d", inv.ChannelID)
	}
	if err := alice.PayInvoice(context.Background(), "unpinned"); err != nil {
		t.Fatalf("unpinned invoice unpayable: %v", err)
	}
	if journal, _ := alice.Store.SelfSigned(key2); len(journal) != 1 {
		t.Fatalf("payment not journaled on the open channel: %+v", journal)
	}
}

// TestHandleInvoicePinQualifiedByRegistry: with the same merchant holding
// same-id channels on coexisting registries, the stored pin must carry the
// message's registry qualifier and PayInvoice must resolve it — not the
// first store entry with that bare id.
func TestHandleInvoicePinQualifiedByRegistry(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob) // e2eKey: id 1 on 0x…21ff
	twin := decoyKey           // id 1 on 0x…1111, sorts first
	linkChannelAt(t, alice, bob, twin)

	// The pin names e2eKey's registry — the one that does NOT sort first.
	msg := invoiceMsgFrom(bob, "qualified", "1000")
	msg.ChannelID = "1"
	if err := alice.handleInvoice(msg, bob.SelfPub); err != nil {
		t.Fatal(err)
	}
	inv, err := alice.Store.Invoice("qualified")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Registry != e2eKey.Registry {
		t.Fatalf("pin qualifier not stored: %+v", inv)
	}
	if err := alice.PayInvoice(context.Background(), "qualified"); err != nil {
		t.Fatal(err)
	}
	if journal, _ := alice.Store.SelfSigned(e2eKey); len(journal) != 1 {
		t.Fatal("payment not journaled on the pinned channel")
	}
	if journal, _ := alice.Store.SelfSigned(twin); len(journal) != 0 {
		t.Fatal("payment journaled on the same-id twin — merchant will NACK it and the channel is poisoned")
	}
}
