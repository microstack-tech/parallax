package nostrmod

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// fakeConn is a controllable RelayConn.
type fakeConn struct {
	mu        sync.Mutex
	sub       chan nostr.Event
	published []nostr.Event
	pubErr    error
}

func (f *fakeConn) Subscribe(ctx context.Context, selfPub string, since nostr.Timestamp) (<-chan nostr.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sub, nil
}

func (f *fakeConn) Publish(ctx context.Context, ev nostr.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pubErr != nil {
		return f.pubErr
	}
	f.published = append(f.published, ev)
	return nil
}

func (f *fakeConn) Close() error { return nil }

func (f *fakeConn) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

// dialerFor returns a dialer serving the given conns by URL; dial attempts
// beyond the per-URL failure budget succeed.
func dialerFor(conns map[string]*fakeConn, failures map[string]*atomic.Int64) RelayDialer {
	return func(ctx context.Context, url string) (RelayConn, error) {
		if failures != nil {
			if n, ok := failures[url]; ok && n.Add(-1) >= 0 {
				return nil, errors.New("dial refused")
			}
		}
		c, ok := conns[url]
		if !ok {
			return nil, errors.New("unknown relay")
		}
		return c, nil
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func testEvent(id string) nostr.Event {
	return nostr.Event{ID: id, Kind: nostr.KindGiftWrap, Content: "x"}
}

func TestPoolFanOutAndDedupe(t *testing.T) {
	conns := map[string]*fakeConn{
		"wss://a": {sub: make(chan nostr.Event, 8)},
		"wss://b": {sub: make(chan nostr.Event, 8)},
		"wss://c": {sub: make(chan nostr.Event, 8)},
	}
	pool := NewPool(dialerFor(conns, nil), []string{"wss://a", "wss://b", "wss://c"}, "self", PoolConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Run(ctx)
	waitFor(t, 2*time.Second, func() bool { return pool.Healthy() == 3 })

	// Publish fans out to all healthy relays.
	if n := pool.Publish(ctx, testEvent("ev-1")); n != 3 {
		t.Fatalf("published to %d relays", n)
	}
	for url, c := range conns {
		if c.publishedCount() != 1 {
			t.Fatalf("relay %s got %d events", url, c.publishedCount())
		}
	}

	// The same wrap arriving on two relays is delivered exactly once.
	conns["wss://a"].sub <- testEvent("dup")
	conns["wss://b"].sub <- testEvent("dup")
	conns["wss://c"].sub <- testEvent("other")

	got := map[string]int{}
	for len(got) < 2 {
		select {
		case ev := <-pool.Events():
			got[ev.ID]++
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out; got %v", got)
		}
	}
	select {
	case ev := <-pool.Events():
		t.Fatalf("unexpected extra event %q", ev.ID)
	case <-time.After(50 * time.Millisecond):
	}
	if got["dup"] != 1 || got["other"] != 1 {
		t.Fatalf("delivery counts: %v", got)
	}
}

func TestPoolReconnectsAfterFailures(t *testing.T) {
	conns := map[string]*fakeConn{"wss://a": {sub: make(chan nostr.Event, 1)}}
	var failures atomic.Int64
	failures.Store(3) // first three dials refused
	pool := NewPool(dialerFor(conns, map[string]*atomic.Int64{"wss://a": &failures}),
		[]string{"wss://a"}, "self",
		PoolConfig{ReconnectBase: 5 * time.Millisecond, ReconnectCap: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return pool.Healthy() == 1 })
}

func TestPoolResubscribesWhenSubscriptionDies(t *testing.T) {
	first := make(chan nostr.Event, 1)
	c := &fakeConn{sub: first}
	pool := NewPool(dialerFor(map[string]*fakeConn{"wss://a": c}, nil),
		[]string{"wss://a"}, "self",
		PoolConfig{ReconnectBase: 5 * time.Millisecond, ReconnectCap: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Run(ctx)
	waitFor(t, 2*time.Second, func() bool { return pool.Healthy() == 1 })

	// Kill the subscription; the pool must reconnect and resubscribe.
	second := make(chan nostr.Event, 8)
	c.mu.Lock()
	c.sub = second
	c.mu.Unlock()
	close(first)

	i := 0
	waitFor(t, 2*time.Second, func() bool {
		if pool.Healthy() != 1 {
			return false
		}
		// Distinct ids per probe: dedupe must not eat retries.
		i++
		select {
		case second <- testEvent("alive-" + string(rune('a'+i))):
		default:
		}
		select {
		case <-pool.Events():
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	})
}

func TestPublishWithNoRelaysReturnsZero(t *testing.T) {
	pool := NewPool(dialerFor(nil, nil), nil, "self", PoolConfig{})
	if n := pool.Publish(context.Background(), testEvent("x")); n != 0 {
		t.Fatalf("published to %d", n)
	}
}
