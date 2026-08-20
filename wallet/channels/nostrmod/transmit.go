package nostrmod

import (
	"context"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// Publisher is what the transmitter needs from the pool (tests fake it).
type Publisher interface {
	Publish(ctx context.Context, ev nostr.Event) int
}

// Retransmission backoff: 2s, 4s, 8s, … capped at 60s (Part 2 §7.2).
// Variables only so the chaos harness can compress time; production code
// never touches them.
var (
	backoffBase int64 = 2  // seconds
	backoffCap  int64 = 60 // seconds
)

// SetRetransmitBackoffForTesting compresses the retransmission schedule and
// returns a restore function. Test-only.
func SetRetransmitBackoffForTesting(baseSeconds, capSeconds int64) (restore func()) {
	prevBase, prevCap := backoffBase, backoffCap
	backoffBase, backoffCap = baseSeconds, capSeconds
	return func() { backoffBase, backoffCap = prevBase, prevCap }
}

func nextBackoff(attempts int) int64 {
	d := backoffBase << uint(attempts)
	if d > backoffCap || d <= 0 {
		return backoffCap
	}
	return d
}

// Transmitter drains the persistent outbound queue: each due item is
// re-wrapped (identical rumor, fresh seal/wrap per attempt) and fanned out;
// undelivered items stay queued with exponential backoff until removed by a
// dedupe key (ACK/NACK/receipt arrived) or expired (give-up).
type Transmitter struct {
	store *proofstore.Store
	pool  Publisher
	priv  string // this wallet's Nostr private key, hex

	// Now is the clock, swappable in tests.
	Now func() int64
}

func NewTransmitter(store *proofstore.Store, pool Publisher, nostrPriv string) *Transmitter {
	return &Transmitter{store: store, pool: pool, priv: nostrPriv, Now: func() int64 { return time.Now().Unix() }}
}

// Tick performs one queue pass. It returns the items that expired this pass
// (gave up) — for a 21902 the caller MUST mark the channel poisoned
// (Part 3 §5) — and how many items were published to at least one relay.
func (t *Transmitter) Tick(ctx context.Context) (published int, gaveUp []proofstore.OutboundItem, err error) {
	now := t.Now()
	due, expired, err := t.store.DueOutbound(now, 64)
	if err != nil {
		return 0, nil, err
	}

	for _, item := range due {
		rumor := nostr.Event{
			Kind:      item.Kind,
			Content:   item.Content,
			CreatedAt: nostr.Timestamp(item.RumorTime),
		}
		for _, tag := range item.Tags {
			rumor.Tags = append(rumor.Tags, nostr.Tag(tag))
		}

		wrap, werr := Wrap(rumor, item.ToNpub, t.priv)
		if werr != nil {
			// Malformed item: unrecoverable, drop it rather than loop.
			_ = t.store.RemoveOutbound(item.ID)
			continue
		}
		n := t.pool.Publish(ctx, wrap)
		if n > 0 {
			published++
		}
		// Reschedule regardless: delivery to a relay is not delivery to the
		// counterparty; only the dedupe-key removal (ACK arrived) or expiry
		// ends retransmission.
		if rerr := t.store.RescheduleOutbound(item.ID, item.Attempts+1, now+nextBackoff(item.Attempts)); rerr != nil {
			return published, expired, rerr
		}
	}
	return published, expired, nil
}

// Run ticks until ctx is done.
func (t *Transmitter) Run(ctx context.Context, interval time.Duration, onGiveUp func(proofstore.OutboundItem)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, gaveUp, err := t.Tick(ctx)
			if err != nil {
				continue
			}
			for _, item := range gaveUp {
				if onGiveUp != nil {
					onGiveUp(item)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
