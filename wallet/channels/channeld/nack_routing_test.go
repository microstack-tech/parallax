package channeld

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

// TestNackPoisonsOnlyTheNamedRegistryChannel: with the SAME peer on two
// coexisting registries that share the bare channel id, and outstanding
// seq-1 proposals on both (typical for young channels), a NACK against one
// channel must not poison the other — the sender scoping cannot tell them
// apart and HandleNack matches on seq alone, so the NACK itself has to name
// the registry it belongs to.
func TestNackPoisonsOnlyTheNamedRegistryChannel(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)
	twinKey := proofstore.ChannelKey{
		ChainID:   e2eKey.ChainID,
		Registry:  util.HexToAddress("0x0000000000000000000000000000000000001111"),
		ChannelID: e2eKey.ChannelID,
	}
	linkChannelAt(t, alice, bob, twinKey)

	// Outstanding seq-1 proposals on both channels.
	if _, err := alice.Engine.ProposePayment(twinKey, big.NewInt(1e9), "", 0); err != nil {
		t.Fatal(err)
	}
	prop, err := alice.Engine.ProposePayment(e2eKey, big.NewInt(1e9), "", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Bob's copy of the e2eKey channel is frozen, so his engine NACKs the
	// proposal — a NACK that belongs to e2eKey and no other channel.
	if err := bob.Store.UpdateMeta(e2eKey, func(m *proofstore.ChannelMeta) {
		m.FrozenUntilBlock = 1000
	}); err != nil {
		t.Fatal(err)
	}
	res, err := bob.Engine.HandleProposal(*prop, alice.SelfPub, 0, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if res.Nack == nil {
		t.Fatalf("frozen channel did not nack: %+v", res)
	}

	rumor := encodeRumor(t, protocol.KindNack, res.Nack)
	if err := alice.handleRumor(context.Background(), rumor, bob.SelfPub); err != nil {
		t.Fatal(err)
	}

	meta, _ := alice.Store.Meta(e2eKey)
	if !meta.Poisoned {
		t.Fatal("the nacked channel was not poisoned")
	}
	twinMeta, _ := alice.Store.Meta(twinKey)
	if twinMeta.Poisoned {
		t.Fatal("NACK against one registry's channel poisoned its same-id twin on the coexisting registry")
	}
}
