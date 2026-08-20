package protocol

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

func TestCoopCloseHappyPath(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	invoiceFor(t, bob, "inv1", wei1)
	pay(t, alice, bob, key, wei1, "inv1")

	expiry := nowBlock + 18
	prop, err := alice.engine.ProposeCoopClose(key, expiry, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	// Balances: A = 10 - 1 = 9, B = 5 + 1 = 6.
	if prop.BalanceA != new(big.Int).Mul(big.NewInt(9), wei1).String() ||
		prop.BalanceB != new(big.Int).Mul(big.NewInt(6), wei1).String() {
		t.Fatalf("balances wrong: %+v", prop)
	}

	// Proposer is frozen immediately.
	if _, err := alice.engine.ProposePayment(key, wei1, "x", nowBlock); !errors.Is(err, ErrFrozen) {
		t.Fatalf("proposer not frozen: %v", err)
	}

	res, ready, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock)
	if err != nil || res.Ack == nil || ready == nil {
		t.Fatalf("countersign failed: %+v %v", res, err)
	}
	// Responder frozen from countersigning.
	if _, err := bob.engine.ProposePayment(key, wei1, "x", nowBlock); !errors.Is(err, ErrFrozen) {
		t.Fatalf("responder not frozen: %v", err)
	}

	readyA, err := alice.engine.HandleCoopCloseAck(key, *res.Ack)
	if err != nil {
		t.Fatal(err)
	}

	// Both sides assembled the identical submittable pair, sigs in fixed
	// positional order (sigA = participant A's).
	if readyA.BalanceA.Cmp(ready.BalanceA) != 0 || readyA.BalanceB.Cmp(ready.BalanceB) != 0 {
		t.Fatal("pairs disagree on balances")
	}
	if !bytes.Equal(readyA.SigA, ready.SigA) || !bytes.Equal(readyA.SigB, ready.SigB) {
		t.Fatal("pairs disagree on signatures")
	}
	digest, err := coopCloseDigest(key, readyA.BalanceA, readyA.BalanceB, readyA.ExpiryBlock)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedBy(digest, readyA.SigA, alice.signer.Address()); err != nil {
		t.Fatalf("sigA: %v", err)
	}
	if err := VerifySignedBy(digest, readyA.SigB, bob.signer.Address()); err != nil {
		t.Fatalf("sigB: %v", err)
	}
}

func TestCoopCloseBalanceMismatchRejected(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	prop, err := alice.engine.ProposeCoopClose(key, nowBlock+18, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	m := *prop
	m.BalanceA = new(big.Int).Mul(big.NewInt(11), wei1).String() // grab an extra LAX
	m.BalanceB = new(big.Int).Mul(big.NewInt(4), wei1).String()
	res, ready, err := bob.engine.HandleCoopCloseProposal(m, alice.npub, nowBlock)
	if err != nil || ready != nil || res.Nack == nil || res.Nack.Reason != NackPolicy {
		t.Fatalf("mismatched balances accepted: %+v %v", res, err)
	}
	// Bob signed nothing: not frozen.
	meta, _ := bob.store.Meta(key)
	if meta.FrozenUntilBlock != 0 || meta.PendingClose != nil {
		t.Fatalf("responder froze on a rejected close: %+v", meta)
	}
}

func TestCoopCloseExpiryUnfreezes(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	expiry := nowBlock + 18
	prop, err := alice.engine.ProposeCoopClose(key, expiry, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock); err != nil {
		t.Fatal(err)
	}

	// Still frozen at the expiry block itself.
	if err := alice.engine.Unfreeze(key, expiry); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.engine.ProposePayment(key, wei1, "x", expiry); !errors.Is(err, ErrFrozen) {
		t.Fatal("unfrozen at expiry block")
	}
	// Expiry passed without submission: resume normally.
	for _, p := range []*party{alice, bob} {
		if err := p.engine.Unfreeze(key, expiry+1); err != nil {
			t.Fatal(err)
		}
		meta, _ := p.store.Meta(key)
		if meta.FrozenUntilBlock != 0 || meta.PendingClose != nil {
			t.Fatalf("freeze not cleared: %+v", meta)
		}
	}
	invoiceFor(t, bob, "inv1", wei1)
	pay(t, alice, bob, key, wei1, "inv1")
}

func TestCoopCloseAckVerification(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	prop, err := alice.engine.ProposeCoopClose(key, nowBlock+18, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock)
	if err != nil {
		t.Fatal(err)
	}

	bad := *res.Ack
	bad.Sig = "0x" + string(bytes.Repeat([]byte("00"), 65))
	if _, err := alice.engine.HandleCoopCloseAck(key, bad); err == nil {
		t.Fatal("garbage countersign accepted")
	}
	if _, err := alice.engine.HandleCoopCloseAck(key, *res.Ack); err != nil {
		t.Fatal(err)
	}
}

func TestCoopCloseWhileClosingAllowed(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	// Watcher marked the channel closing (counterparty force-closed).
	for _, p := range []*party{alice, bob} {
		err := p.store.UpdateMeta(key, func(m *proofstore.ChannelMeta) { m.Status = proofstore.StatusClosing })
		if err != nil {
			t.Fatal(err)
		}
	}
	prop, err := alice.engine.ProposeCoopClose(key, nowBlock+18, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res, ready, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock)
	if err != nil || res.Ack == nil || ready == nil {
		t.Fatalf("coop close during Closing refused: %+v %v", res, err)
	}
}
