package protocol

import (
	"bytes"
	"errors"
	"math/big"
	"strconv"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/util"
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

func TestCoopCloseDuplicateProposalReAcks(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	prop, err := alice.engine.ProposeCoopClose(key, nowBlock+18, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res1, ready1, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock)
	if err != nil || res1.Ack == nil {
		t.Fatalf("first: %+v %v", res1, err)
	}

	// The retransmitted proposal must re-ACK with the same countersignature,
	// not NACK the responder's own freeze (Part 2 §7.2).
	res2, ready2, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock)
	if err != nil || res2.Ack == nil || ready2 == nil {
		t.Fatalf("duplicate: %+v %v", res2, err)
	}
	if res2.Ack.Sig != res1.Ack.Sig || res2.Ack.StateHash != res1.Ack.StateHash {
		t.Fatal("duplicate coop-close ack differs")
	}
	if ready2.BalanceA.Cmp(ready1.BalanceA) != 0 {
		t.Fatal("ready pairs disagree")
	}

	// A different close while frozen still NACKs.
	other := *prop
	other.ExpiryBlock = "2020" // differs from the pending close, within horizon
	res3, _, err := bob.engine.HandleCoopCloseProposal(other, alice.npub, nowBlock)
	if err != nil || res3.Nack == nil || res3.Nack.Reason != NackFrozen {
		t.Fatalf("different close while frozen accepted: %+v %v", res3, err)
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

// TestCoopCloseExpiryHorizonBounded: a close proposal's expiry needs an
// upper bound, or an authenticated peer can freeze the channel against new
// payments essentially forever (FrozenUntilBlock = 2^63) at zero cost.
func TestCoopCloseExpiryHorizonBounded(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})

	// Alice hand-builds a correctly signed proposal for the exact current
	// balances but with an absurd expiry (the engine itself must refuse to
	// build one, so go under the hood).
	balA, balB, err := alice.engine.CloseBalances(key)
	if err != nil {
		t.Fatal(err)
	}
	farExpiry := uint64(1) << 62
	digest, err := coopCloseDigest(key, balA, balB, farExpiry)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := alice.signer.SignDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	prop := &CoopCloseProposalMsg{
		V:            1,
		ChannelID:    "1",
		Registry:     key.Registry.Hex(),
		ChainID:      key.ChainID,
		BalanceA:     balA.String(),
		BalanceB:     balB.String(),
		ExpiryBlock:  strconv.FormatUint(farExpiry, 10),
		Sig:          "0x" + util.Bytes2Hex(sig),
		ProposerRole: "A",
	}

	res, ready, err := bob.engine.HandleCoopCloseProposal(*prop, alice.npub, nowBlock)
	if err != nil || ready != nil || res.Nack == nil {
		t.Fatalf("far-expiry close not refused: %+v ready=%v err=%v", res, ready, err)
	}
	meta, _ := bob.store.Meta(key)
	if meta.FrozenUntilBlock != 0 || meta.PendingClose != nil {
		t.Fatalf("responder froze on a far-expiry close: %+v", meta)
	}

	// The proposer side enforces the same bound.
	if _, err := alice.engine.ProposeCoopClose(key, farExpiry, nowBlock); err == nil {
		t.Fatal("engine proposed a far-expiry close")
	}
	// The horizon boundary itself is accepted.
	prop2, err := alice.engine.ProposeCoopClose(key, nowBlock+DefaultCoopCloseHorizonBlocks, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res2, ready2, err := bob.engine.HandleCoopCloseProposal(*prop2, alice.npub, nowBlock)
	if err != nil || ready2 == nil || res2.Ack == nil {
		t.Fatalf("boundary-expiry close refused: %+v %v", res2, err)
	}
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
