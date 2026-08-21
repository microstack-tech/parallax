package channeld

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// linkSecondChannel gives alice and bob a second open channel (id 2) on the
// same registry.
func linkSecondChannel(t *testing.T, a, b *Node) proofstore.ChannelKey {
	t.Helper()
	key2 := e2eKey
	key2.ChannelID = 2
	deposits := proofstore.Deposits{
		DepositA:         proofstore.NewU256(big.NewInt(10e9)),
		DepositB:         proofstore.NewU256(big.NewInt(5e9)),
		LastScannedBlock: 1,
	}
	for _, p := range []struct {
		n    *Node
		role proofstore.Role
		peer *Node
	}{{a, proofstore.RoleA, b}, {b, proofstore.RoleB, a}} {
		err := p.n.Store.CreateChannel(proofstore.ChannelMeta{
			Key:             key2,
			Role:            p.role,
			Status:          proofstore.StatusOpen,
			PeerNpub:        p.peer.SelfPub,
			PeerAddress:     p.peer.Signer.Address(),
			ChallengePeriod: 144,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := p.n.Store.PutDeposits(key2, deposits); err != nil {
			t.Fatal(err)
		}
	}
	return key2
}

// TestPayURIHonorsPinnedInvoice: merchants mint channel-pinned invoices, so
// the payment URI must carry the pin and the payer must select that channel.
// Picking any open channel with the merchant makes the merchant NACK a
// well-formed purchase after the payer already journaled an irrevocable
// state — poisoning a healthy channel.
func TestPayURIHonorsPinnedInvoice(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNodeWith(t, h, func(cfg *Config) {
		cfg.Merchant.PushPayments = false // merchant policy: invoices required
	})
	linkChannel(t, alice, bob)
	key2 := linkSecondChannel(t, alice, bob)

	// Bob pins the invoice to channel 2 and hands alice the URI.
	inv, uri, err := bob.CreateInvoice(big.NewInt(2e9), "coffee", time.Hour, key2.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	req, err := ParsePaymentURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	key, err := alice.ChannelForRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go alice.Run(ctx, time.Hour)
	go bob.Run(ctx, time.Hour)
	waitUntil(t, 3*time.Second, "relays connected", func() bool {
		return alice.Pool.Healthy() == 1 && bob.Pool.Healthy() == 1
	})

	if err := alice.Pay(ctx, key, req.AmountWei, req.InvoiceID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "pinned payment completion", func() bool {
		latest, err := bob.Store.LatestState(key2)
		return err == nil && latest.Seq == 1
	})
	got, err := bob.Store.Invoice(inv.ID)
	if err != nil || !got.Paid {
		t.Fatalf("invoice not paid: %+v %v", got, err)
	}
	// The healthy unpinned channel is untouched — not poisoned.
	meta, _ := alice.Store.Meta(e2eKey)
	if meta.Poisoned {
		t.Fatal("channel 1 poisoned by a purchase pinned to channel 2")
	}
}
