package protocol

import (
	"errors"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// TestSupersessionRespectsStatusAndFreeze: the no-op supersession signs and
// journals a NEW self-signed state, so it is bound by the same guards as
// ProposePayment — a frozen channel signs nothing (Part 1 §7.4) and a
// settled one has no seq space left. Skipping them journals one more
// irrevocable state the counterparty will never countersign, growing the
// poisoned exposure the cure was meant to shrink.
func TestSupersessionRespectsStatusAndFreeze(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	_ = bob
	if _, err := alice.engine.ProposePayment(key, wei1, "", nowBlock); err != nil {
		t.Fatal(err)
	}

	if err := alice.store.UpdateMeta(key, func(m *proofstore.ChannelMeta) {
		m.FrozenUntilBlock = nowBlock + 10
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.engine.ProposeNoOpSupersession(key, nowBlock); !errors.Is(err, ErrFrozen) {
		t.Fatalf("supersession signed on a frozen channel: %v", err)
	}

	if err := alice.store.UpdateMeta(key, func(m *proofstore.ChannelMeta) {
		m.FrozenUntilBlock = 0
		m.Status = proofstore.StatusSettled
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.engine.ProposeNoOpSupersession(key, nowBlock); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("supersession signed on a settled channel: %v", err)
	}

	// Back to open: the cure works as before.
	if err := alice.store.UpdateMeta(key, func(m *proofstore.ChannelMeta) {
		m.Status = proofstore.StatusOpen
	}); err != nil {
		t.Fatal(err)
	}
	prop, err := alice.engine.ProposeNoOpSupersession(key, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	if prop.State.Seq != "2" {
		t.Fatalf("supersession seq %s, want 2", prop.State.Seq)
	}
}
