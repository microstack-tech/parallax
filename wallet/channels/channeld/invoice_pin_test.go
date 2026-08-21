package channeld

import (
	"context"
	"errors"
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
	linkChannelAt(t, a, b, key2)
	return key2
}

// TestCreateInvoiceFailsClosedOnAmbiguousPin: an invoice pin is a bare
// channel id, which coexisting registries both number from 1. Resolving the
// pin by first match stamps the wrong reg= on the URI — the customer's
// ChannelForRequest then filters on that registry, finds no match, and a
// perfectly valid pinned invoice becomes unpayable. Minting must fail closed
// on an ambiguous pin, and an unambiguous pin must stamp its channel's
// registry regardless of config iteration order.
func TestCreateInvoiceFailsClosedOnAmbiguousPin(t *testing.T) {
	h := newHub()
	merchant := newTestNodeWith(t, h, func(cfg *Config) {
		cfg.Registries["v2"] = []RegistryEntry{{Address: decoyKey.Registry.Hex(), ChainID: 2110}}
	})
	customer := newTestNode(t, h, nil)
	linkChannel(t, merchant, customer) // channel 1 on the v1 registry
	addDecoyChannel(t, merchant)       // channel 1 on the v2 registry

	if _, _, err := merchant.CreateInvoice(big.NewInt(1e9), "", time.Hour, 1); !errors.Is(err, ErrAmbiguousChannel) {
		t.Fatalf("ambiguous pin minted an invoice: %v", err)
	}

	// Channel 7 exists only on the v2 registry: the pin resolves uniquely
	// and the URI must carry v2, whatever order the config map iterates in.
	only := decoyKey
	only.ChannelID = 7
	err := merchant.Store.CreateChannel(proofstore.ChannelMeta{
		Key:         only,
		Role:        proofstore.RoleA,
		Status:      proofstore.StatusOpen,
		PeerNpub:    customer.SelfPub,
		PeerAddress: customer.Signer.Address(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, uri, err := merchant.CreateInvoice(big.NewInt(1e9), "", time.Hour, 7)
	if err != nil {
		t.Fatal(err)
	}
	req, err := ParsePaymentURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if req.Registry != only.Registry {
		t.Fatalf("pinned URI stamped registry %s, want %s", req.Registry.Hex(), only.Registry.Hex())
	}
}

// TestUnpinnedRequestIgnoresRegistryHint: for an unpinned invoice the URI's
// reg= parameter is a bootstrap hint (which registry to open a channel on),
// not a constraint — the merchant accepts payment on any shared open
// channel. A multi-registry merchant stamps ONE registry on the URI, so
// filtering by it would reject a customer whose only open channel with the
// merchant lives on the other registry.
func TestUnpinnedRequestIgnoresRegistryHint(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob) // alice's only channel with bob, on the v1 registry

	req := PaymentRequest{
		Merchant:  bob.Signer.Address(),
		AmountWei: big.NewInt(1e9),
		Registry:  decoyKey.Registry, // URI hint names the other registry
	}
	key, err := alice.ChannelForRequest(req)
	if err != nil {
		t.Fatalf("unpinned payment rejected over the URI's registry hint: %v", err)
	}
	if key != e2eKey {
		t.Fatalf("wrong channel selected: %s", key)
	}
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
