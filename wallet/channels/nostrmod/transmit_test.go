package nostrmod

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

type capturePublisher struct {
	mu     sync.Mutex
	wraps  []nostr.Event
	accept bool
}

func (c *capturePublisher) Publish(ctx context.Context, ev nostr.Event) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wraps = append(c.wraps, ev)
	if c.accept {
		return 1
	}
	return 0
}

func (c *capturePublisher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.wraps)
}

func openTestStore(t *testing.T) *proofstore.Store {
	t.Helper()
	s, err := proofstore.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTransmitterRetransmitsWithBackoff(t *testing.T) {
	store := openTestStore(t)
	pub := &capturePublisher{accept: true}
	senderPriv, _ := newPair(t)
	recipientPriv, recipientPub := newPair(t)

	tx := NewTransmitter(store, pub, senderPriv)
	now := int64(1_000_000)
	tx.Now = func() int64 { return now }

	_, err := store.EnqueueOutbound(proofstore.OutboundItem{
		DedupeKey: "21902:test:1",
		ToNpub:    recipientPub,
		Kind:      21902,
		Content:   `{"v":1,"seq":"1"}`,
		Tags:      [][]string{{"ch", "17"}},
		RumorTime: now,
		ExpiresAt: now + 3600,
	})
	if err != nil {
		t.Fatal(err)
	}

	// First tick sends immediately.
	if n, _, err := tx.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("first tick: %d %v", n, err)
	}
	// Next tick is inside the 2s backoff: nothing due.
	now++
	if n, _, err := tx.Tick(context.Background()); err != nil || n != 0 {
		t.Fatalf("backoff not respected: %d %v", n, err)
	}
	// 2s after the first attempt it retransmits; then 4s, capped at 60s.
	now += 2
	if n, _, _ := tx.Tick(context.Background()); n != 1 {
		t.Fatal("no retransmission at 2s")
	}
	now += 4
	if n, _, _ := tx.Tick(context.Background()); n != 1 {
		t.Fatal("no retransmission at +4s")
	}

	// Every attempt is the identical rumor under a fresh seal/wrap.
	if pub.count() != 3 {
		t.Fatalf("wraps: %d", pub.count())
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	seenWrapIDs := map[string]bool{}
	for _, wrap := range pub.wraps {
		if seenWrapIDs[wrap.ID] {
			t.Fatal("wrap reused across attempts")
		}
		seenWrapIDs[wrap.ID] = true
		rumor, _, err := Unwrap(wrap, recipientPriv)
		if err != nil {
			t.Fatal(err)
		}
		if rumor.Kind != 21902 || rumor.Content != `{"v":1,"seq":"1"}` {
			t.Fatalf("rumor mutated: %+v", rumor)
		}
		if tag := rumor.Tags.Find("ch"); tag == nil || tag[1] != "17" {
			t.Fatalf("rumor tags lost: %v", rumor.Tags)
		}
	}
}

func TestTransmitterStopsOnDedupeRemoval(t *testing.T) {
	store := openTestStore(t)
	pub := &capturePublisher{accept: true}
	senderPriv, _ := newPair(t)
	_, recipientPub := newPair(t)
	tx := NewTransmitter(store, pub, senderPriv)
	now := int64(1_000_000)
	tx.Now = func() int64 { return now }

	if _, err := store.EnqueueOutbound(proofstore.OutboundItem{
		DedupeKey: "21902:test:5", ToNpub: recipientPub, Kind: 21902,
		Content: "{}", RumorTime: now, ExpiresAt: now + 3600,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tx.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The ACK arrived: the wallet removes by dedupe key; nothing retransmits.
	removed, err := store.RemoveOutboundByDedupe("21902:test:5")
	if err != nil || removed != 1 {
		t.Fatalf("removed %d %v", removed, err)
	}
	now += 120
	if n, _, _ := tx.Tick(context.Background()); n != 0 {
		t.Fatal("retransmitted after removal")
	}
	if qlen, _ := store.OutboundLen(); qlen != 0 {
		t.Fatalf("queue not empty: %d", qlen)
	}
}

func TestTransmitterGivesUpAtExpiry(t *testing.T) {
	store := openTestStore(t)
	pub := &capturePublisher{accept: true}
	senderPriv, _ := newPair(t)
	_, recipientPub := newPair(t)
	tx := NewTransmitter(store, pub, senderPriv)
	now := int64(1_000_000)
	tx.Now = func() int64 { return now }

	if _, err := store.EnqueueOutbound(proofstore.OutboundItem{
		DedupeKey: "21902:test:9", ToNpub: recipientPub, Kind: 21902,
		Content: "{}", RumorTime: now, ExpiresAt: now + 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tx.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	now += 11
	_, gaveUp, err := tx.Tick(context.Background())
	if err != nil || len(gaveUp) != 1 || gaveUp[0].DedupeKey != "21902:test:9" {
		t.Fatalf("give-up: %+v %v", gaveUp, err)
	}
	if qlen, _ := store.OutboundLen(); qlen != 0 {
		t.Fatalf("expired item still queued: %d", qlen)
	}
}

func TestQueueSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.db")
	s, err := proofstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueOutbound(proofstore.OutboundItem{
		DedupeKey: "d", ToNpub: "n", Kind: 21906, Content: "{}",
		RumorTime: 1, ExpiresAt: 1 << 40,
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := proofstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	due, _, err := s2.DueOutbound(100, 10)
	if err != nil || len(due) != 1 || due[0].DedupeKey != "d" {
		t.Fatalf("queue lost across reopen: %+v %v", due, err)
	}
}
