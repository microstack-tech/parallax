package protocol

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// Setup recap: A deposited 10 LAX, B 5 LAX (setupPair). Payments adjust
// entitlements at the latest complete state.

func TestWithdrawHappyPath(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	invoiceFor(t, bob, "inv1", wei1)
	pay(t, alice, bob, key, wei1, "inv1") // A paid B 1: entitlements 9 / 6

	expiry := nowBlock + 18
	prop, err := bob.engine.ProposeWithdraw(key, wei2, expiry, nowBlock) // B takes 2 of its 6
	if err != nil {
		t.Fatal(err)
	}
	if prop.TotalWithdrawn != wei2.String() || prop.Participant != "0x"+util.Bytes2Hex(bob.signer.Address().Bytes()) {
		t.Fatalf("proposal fields: %+v", prop)
	}

	res, ready, err := alice.engine.HandleWithdrawProposal(*prop, bob.npub, nowBlock)
	if err != nil || res.Ack == nil || ready == nil {
		t.Fatalf("countersign: %+v %v", res, err)
	}
	readyB, err := bob.engine.HandleWithdrawAck(key, *res.Ack)
	if err != nil {
		t.Fatal(err)
	}

	// Both assembled pairs agree; sigs verify in fixed positional order
	// over the on-chain Withdraw digest.
	if !bytes.Equal(ready.SigA, readyB.SigA) || !bytes.Equal(ready.SigB, readyB.SigB) {
		t.Fatal("pairs disagree")
	}
	digest, err := withdrawDigest(key, bob.signer.Address(), wei2, expiry)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedBy(digest, ready.SigA, alice.signer.Address()); err != nil {
		t.Fatalf("sigA: %v", err)
	}
	if err := VerifySignedBy(digest, ready.SigB, bob.signer.Address()); err != nil {
		t.Fatalf("sigB: %v", err)
	}

	// Duplicate proposal re-ACKs with the identical countersignature
	// (deterministic ECDSA).
	res2, _, err := alice.engine.HandleWithdrawProposal(*prop, bob.npub, nowBlock)
	if err != nil || res2.Ack == nil || res2.Ack.Sig != res.Ack.Sig {
		t.Fatalf("duplicate not idempotent: %+v %v", res2, err)
	}
}

func TestWithdrawEntitlementCeiling(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	invoiceFor(t, bob, "inv1", wei1)
	pay(t, alice, bob, key, wei1, "inv1") // A's entitlement is now 9

	// A tries to withdraw 10 (more than 10 - 1 paid): refused locally.
	ten := new(big.Int).Mul(big.NewInt(10), wei1)
	if _, err := alice.engine.ProposeWithdraw(key, ten, nowBlock+18, nowBlock); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("over-entitlement proposal allowed: %v", err)
	}

	// A forged over-entitlement proposal is NACKed by the responder.
	nine := new(big.Int).Mul(big.NewInt(9), wei1)
	prop, err := alice.engine.ProposeWithdraw(key, nine, nowBlock+18, nowBlock) // exactly at ceiling: fine
	if err != nil {
		t.Fatal(err)
	}
	forged := *prop
	forged.TotalWithdrawn = ten.String()
	res, ready, err := bob.engine.HandleWithdrawProposal(forged, alice.npub, nowBlock)
	if err != nil || ready != nil || res.Nack == nil || res.Nack.Reason != NackInsufficientBalance {
		t.Fatalf("forged total accepted: %+v %v", res, err)
	}
	// The genuine one passes.
	res, ready, err = bob.engine.HandleWithdrawProposal(*prop, alice.npub, nowBlock)
	if err != nil || res.Ack == nil || ready == nil {
		t.Fatalf("genuine refused: %+v %v", res, err)
	}
}

func TestWithdrawRejectsWhilePoisonedOrForPeerAddress(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	invoiceFor(t, bob, "inv1", wei1)

	// Outstanding self-signed state blocks proposing and countersigning.
	if _, err := alice.engine.ProposePayment(key, wei1, "inv1", nowBlock); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.engine.ProposeWithdraw(key, wei1, nowBlock+18, nowBlock); !errors.Is(err, ErrProposalPending) {
		t.Fatalf("withdraw allowed with journal outstanding: %v", err)
	}
	prop, err := bob.engine.ProposeWithdraw(key, wei1, nowBlock+18, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := alice.engine.HandleWithdrawProposal(*prop, bob.npub, nowBlock)
	if err != nil || res.Nack == nil || res.Nack.Reason != NackPolicy {
		t.Fatalf("countersigned with own journal outstanding: %+v %v", res, err)
	}

	// A proposal naming the RESPONDER's address (not the proposer's own) is
	// refused: you only withdraw to yourself.
	fresh := *prop
	fresh.Participant = "0x" + util.Bytes2Hex(alice.signer.Address().Bytes())
	res, _, err = bob.engine.HandleWithdrawProposal(fresh, alice.npub, nowBlock)
	if err != nil || res.Nack == nil {
		t.Fatalf("withdraw-to-peer accepted: %+v %v", res, err)
	}
}

func TestWithdrawExpiryAndSweep(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	expiry := nowBlock + 18
	prop, err := alice.engine.ProposeWithdraw(key, wei1, expiry, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	// A second proposal while one is outstanding is refused...
	if _, err := alice.engine.ProposeWithdraw(key, wei1, expiry, nowBlock); !errors.Is(err, ErrWithdrawPending) {
		t.Fatalf("second withdraw allowed: %v", err)
	}
	// ...until the sweep clears it after expiry.
	if err := alice.engine.SweepWithdraw(key, expiry+1); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.engine.ProposeWithdraw(key, wei1, expiry+40, expiry+1); err != nil {
		t.Fatalf("post-expiry proposal refused: %v", err)
	}

	// Late-arriving proposal past expiry is NACKed.
	res, _, err := bob.engine.HandleWithdrawProposal(*prop, alice.npub, expiry+1)
	if err != nil || res.Nack == nil {
		t.Fatalf("expired proposal accepted: %+v %v", res, err)
	}

	// Sweep also clears once confirmed on-chain figures catch up.
	dep, _ := alice.store.Deposits(key)
	dep.WithdrawnA = proofstore.NewU256(wei1)
	if err := alice.store.PutDeposits(key, dep); err != nil {
		t.Fatal(err)
	}
	if err := alice.engine.SweepWithdraw(key, expiry+2); err != nil {
		t.Fatal(err)
	}
	meta, _ := alice.store.Meta(key)
	if meta.PendingWithdraw != nil {
		t.Fatal("fulfilled withdraw not swept")
	}
}

func TestWithdrawAckVerification(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	prop, err := alice.engine.ProposeWithdraw(key, wei1, nowBlock+18, nowBlock)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := bob.engine.HandleWithdrawProposal(*prop, alice.npub, nowBlock)
	if err != nil || res.Ack == nil {
		t.Fatal(err)
	}

	bad := *res.Ack
	bad.StateHash = util.Hash{0x02}.Hex()
	if _, err := alice.engine.HandleWithdrawAck(key, bad); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if _, err := alice.engine.HandleWithdrawAck(key, *res.Ack); err != nil {
		t.Fatal(err)
	}
}
