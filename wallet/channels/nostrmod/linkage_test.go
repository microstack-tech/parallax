package nostrmod

import (
	"strings"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

func TestLinkageEventRoundtrip(t *testing.T) {
	evmPriv, _ := crypto.GenerateKey()
	signer := protocol.NewKeySigner(evmPriv)
	nostrPriv, nostrPub := newPair(t)

	ev, err := BuildLinkageEvent(signer, nostrPriv)
	if err != nil {
		t.Fatal(err)
	}
	npub, addr, err := VerifyLinkageEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	if npub != nostrPub || addr != signer.Address() {
		t.Fatalf("bound pair wrong: %s %s", npub, addr.Hex())
	}
	if tag := ev.Tags.Find("d"); tag == nil || tag[1] != strings.ToLower(signer.Address().Hex()) {
		t.Fatalf("d-tag: %v", ev.Tags)
	}
}

func TestLinkageRejectsForgery(t *testing.T) {
	evmPriv, _ := crypto.GenerateKey()
	signer := protocol.NewKeySigner(evmPriv)
	nostrPriv, _ := newPair(t)
	otherNostrPriv, otherPub := newPair(t)

	ev, err := BuildLinkageEvent(signer, nostrPriv)
	if err != nil {
		t.Fatal(err)
	}

	// Mallory republishes alice's content under her own npub: the EVM sig
	// binds alice's npub, so verification must fail.
	forged := ev
	forged.PubKey = otherPub
	if err := forged.Sign(otherNostrPriv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyLinkageEvent(forged); err == nil {
		t.Fatal("replayed linkage accepted under a different npub")
	}

	// Tampered d-tag pointing at someone else's address.
	tampered := ev
	tampered.Tags = tampered.Tags[:0:0]
	tampered.Tags = append(tampered.Tags, []string{"d", "0x00000000000000000000000000000000000000aa"})
	if err := tampered.Sign(nostrPriv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyLinkageEvent(tampered); err == nil {
		t.Fatal("d-tag mismatch accepted")
	}

	// Broken Nostr signature.
	broken := ev
	broken.Sig = strings.Repeat("00", 64)
	if _, _, err := VerifyLinkageEvent(broken); err == nil {
		t.Fatal("unsigned linkage accepted")
	}
}

func TestLinkageRevocation(t *testing.T) {
	evmPriv, _ := crypto.GenerateKey()
	signer := protocol.NewKeySigner(evmPriv)
	nostrPriv, _ := newPair(t)

	rev, err := BuildLinkageRevocation(signer, nostrPriv)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyLinkageEvent(rev); err == nil {
		t.Fatal("revoked linkage verified as a binding")
	}
}

func TestVerifyLinkageDirect(t *testing.T) {
	evmPriv, _ := crypto.GenerateKey()
	signer := protocol.NewKeySigner(evmPriv)
	_, nostrPub := newPair(t)

	sig, err := SignLinkage(signer, nostrPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLinkage(nostrPub, sig, signer.Address()); err != nil {
		t.Fatal(err)
	}
	// Wrong address.
	if err := VerifyLinkage(nostrPub, sig, util.HexToAddress("0x00000000000000000000000000000000000000aa")); err == nil {
		t.Fatal("wrong address accepted")
	}
	// Wrong npub (the same sig cannot bind a different key).
	_, otherPub := newPair(t)
	if err := VerifyLinkage(otherPub, sig, signer.Address()); err == nil {
		t.Fatal("wrong npub accepted")
	}
}
