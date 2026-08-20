package nostrmod

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func newPair(t *testing.T) (priv, pub string) {
	t.Helper()
	priv = nostr.GeneratePrivateKey()
	pub, err := nostr.GetPublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func testRumor(content string) nostr.Event {
	return nostr.Event{
		Kind:      21902, // proof proposal
		CreatedAt: nostr.Now(),
		Content:   content,
		Tags:      nostr.Tags{nostr.Tag{"ch", "17"}},
	}
}

func TestWrapUnwrapRoundtrip(t *testing.T) {
	senderPriv, senderPub := newPair(t)
	recipientPriv, recipientPub := newPair(t)

	wrap, err := Wrap(testRumor(`{"v":1,"channelId":"17"}`), recipientPub, senderPriv)
	if err != nil {
		t.Fatal(err)
	}
	if wrap.Kind != nostr.KindGiftWrap {
		t.Fatalf("wrap kind %d", wrap.Kind)
	}
	if wrap.PubKey == senderPub {
		t.Fatal("wrap signed by sender key, not an ephemeral key")
	}
	if tag := wrap.Tags.Find("p"); tag == nil || tag[1] != recipientPub {
		t.Fatalf("missing recipient p-tag: %v", wrap.Tags)
	}
	if strings.Contains(wrap.Content, "channelId") {
		t.Fatal("rumor content visible in wrap")
	}

	rumor, gotSender, err := Unwrap(wrap, recipientPriv)
	if err != nil {
		t.Fatal(err)
	}
	if gotSender != senderPub {
		t.Fatalf("sender: got %s want %s", gotSender, senderPub)
	}
	if rumor.PubKey != senderPub || rumor.Content != `{"v":1,"channelId":"17"}` || rumor.Kind != 21902 {
		t.Fatalf("rumor mismatch: %+v", rumor)
	}
	if rumor.Sig != "" {
		t.Fatal("rumor must be unsigned")
	}
	if tag := rumor.Tags.Find("ch"); tag == nil || tag[1] != "17" {
		t.Fatalf("rumor tags lost: %v", rumor.Tags)
	}
}

func TestWrapDistinctEphemeralKeys(t *testing.T) {
	senderPriv, _ := newPair(t)
	_, recipientPub := newPair(t)

	w1, err := Wrap(testRumor("a"), recipientPub, senderPriv)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := Wrap(testRumor("a"), recipientPub, senderPriv)
	if err != nil {
		t.Fatal(err)
	}
	if w1.PubKey == w2.PubKey {
		t.Fatal("ephemeral wrap key reused")
	}
}

func TestBackdatingWindow(t *testing.T) {
	senderPriv, _ := newPair(t)
	recipientPriv, recipientPub := newPair(t)

	var minOffset, maxOffset int64 = 1 << 62, -1
	for i := 0; i < 200; i++ {
		now := int64(nostr.Now())
		wrap, err := Wrap(testRumor("x"), recipientPub, senderPriv)
		if err != nil {
			t.Fatal(err)
		}
		for _, ts := range []int64{int64(wrap.CreatedAt), sealCreatedAt(t, wrap, recipientPriv)} {
			offset := now - ts
			if offset < 0 || offset > maxBackdate+2 { // +2s scheduling slack
				t.Fatalf("timestamp outside [now-48h, now]: offset %d", offset)
			}
			if offset < minOffset {
				minOffset = offset
			}
			if offset > maxOffset {
				maxOffset = offset
			}
		}
	}
	// 400 uniform samples over 48 h: spread far beyond go-nostr's ~10 h cap
	// proves the full window is actually used (P(all < 24h) ≈ 2^-400).
	if maxOffset < maxBackdate/2 {
		t.Fatalf("backdating not using the full window: max offset %d", maxOffset)
	}
	if minOffset > maxBackdate/2 {
		t.Fatalf("backdating never recent: min offset %d", minOffset)
	}
}

// sealCreatedAt opens the wrap only far enough to read the seal timestamp.
func sealCreatedAt(t *testing.T, wrap nostr.Event, recipientPriv string) int64 {
	t.Helper()
	sealJSON, err := decrypt44(wrap.Content, wrap.PubKey, recipientPriv)
	if err != nil {
		t.Fatal(err)
	}
	var seal nostr.Event
	if err := seal.UnmarshalJSON([]byte(sealJSON)); err != nil {
		t.Fatal(err)
	}
	return int64(seal.CreatedAt)
}

func TestUnwrapRejectsWrongRecipient(t *testing.T) {
	senderPriv, _ := newPair(t)
	_, recipientPub := newPair(t)
	otherPriv, _ := newPair(t)

	wrap, err := Wrap(testRumor("secret"), recipientPub, senderPriv)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Unwrap(wrap, otherPriv); err == nil {
		t.Fatal("unwrapped with the wrong key")
	}
}

func TestUnwrapRejectsWrongKind(t *testing.T) {
	_, recipientPriv := "", nostr.GeneratePrivateKey()
	ev := nostr.Event{Kind: 1, Content: "hi"}
	if _, _, err := Unwrap(ev, recipientPriv); err == nil {
		t.Fatal("non-1059 accepted")
	}
}

func TestUnwrapRejectsForgedSeal(t *testing.T) {
	senderPriv, _ := newPair(t)
	recipientPriv, recipientPub := newPair(t)

	wrap, err := Wrap(testRumor("x"), recipientPub, senderPriv)
	if err != nil {
		t.Fatal(err)
	}

	// Re-wrap a seal whose signature has been stripped/broken: decrypt the
	// seal, corrupt it, re-encrypt under a fresh ephemeral key.
	sealJSON, err := decrypt44(wrap.Content, wrap.PubKey, recipientPriv)
	if err != nil {
		t.Fatal(err)
	}
	var seal nostr.Event
	if err := seal.UnmarshalJSON([]byte(sealJSON)); err != nil {
		t.Fatal(err)
	}
	seal.Sig = strings.Repeat("00", 64) // broken signature

	forged, err := rewrap(seal, recipientPub)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Unwrap(forged, recipientPriv); err == nil {
		t.Fatal("forged seal accepted")
	}
}

func TestUnwrapRejectsRumorPubkeySpoof(t *testing.T) {
	senderPriv, _ := newPair(t)
	recipientPriv, recipientPub := newPair(t)
	_, victimPub := newPair(t)

	// A malicious sender seals (with its own valid signature) a rumor that
	// claims to be authored by someone else.
	rumor := testRumor("spoof")
	rumor.PubKey = victimPub
	rumor.ID = rumor.GetID()

	cipher, err := encrypt44(rumor.String(), recipientPub, senderPriv)
	if err != nil {
		t.Fatal(err)
	}
	seal := nostr.Event{Kind: nostr.KindSeal, Content: cipher, CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	if err := seal.Sign(senderPriv); err != nil {
		t.Fatal(err)
	}
	wrap, err := rewrap(seal, recipientPub)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Unwrap(wrap, recipientPriv); err == nil {
		t.Fatal("spoofed rumor author accepted")
	}
}
