package channeld

import (
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/qrenc"
)

// TestQRInvoiceResolvesPinnedChannelDeployment: QRInvoice resolved the pin
// with mustChainID + firstRegistry — two INDEPENDENT map iterations over
// the registries config, so a multi-registry merchant could pair one
// deployment's chain id with the other's address (or just pick the wrong
// deployment outright) and either fail to render a valid invoice or stamp
// the QR with a registry the channel does not live on. The pin must resolve
// to the pinned channel's own deployment, deterministically.
func TestQRInvoiceResolvesPinnedChannelDeployment(t *testing.T) {
	h := newHub()
	merchant := newTestNodeWith(t, h, func(cfg *Config) {
		// A second deployment under its own label, on a DIFFERENT chain.
		cfg.Registries["v2"] = []RegistryEntry{{Address: decoyKey.Registry.Hex(), ChainID: 3000}}
	})
	customer := newTestNode(t, h, nil)

	// The merchant's only channel lives on the v2 deployment (chain 3000).
	only := proofstore.ChannelKey{ChainID: "3000", Registry: decoyKey.Registry, ChannelID: 7}
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

	inv, _, err := merchant.CreateInvoice(big.NewInt(2e9), "coffee", time.Hour, 7)
	if err != nil {
		t.Fatal(err)
	}

	// Map iteration order changes per call: every render must still name
	// the pinned channel's own deployment.
	for i := 0; i < 30; i++ {
		code, err := merchant.QRInvoice(inv)
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		env, err := qrenc.Decode(code)
		if err != nil {
			t.Fatal(err)
		}
		if env.Registry != only.Registry || strconv.FormatUint(env.ChainID, 10) != only.ChainID {
			t.Fatalf("render %d stamped %s (chain %d), want %s (chain %s)",
				i, env.Registry.Hex(), env.ChainID, only.Registry.Hex(), only.ChainID)
		}
	}
}
