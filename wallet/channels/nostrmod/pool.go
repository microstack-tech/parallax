package nostrmod

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// RelayConn is one live relay connection. Production connections wrap
// go-nostr; tests substitute fakes. Relays are untrusted queues —
// availability only, never integrity or ordering (Part 2 §12).
type RelayConn interface {
	// Subscribe opens the inbox subscription: kind 1059 wraps p-tagged to
	// selfPub, looking back to since (≥72 h for NIP-59 backdating, Part 2
	// §2). The returned channel closes when the connection dies.
	Subscribe(ctx context.Context, selfPub string, since nostr.Timestamp) (<-chan nostr.Event, error)
	Publish(ctx context.Context, ev nostr.Event) error
	Close() error
}

// RelayDialer opens a connection to one relay URL.
type RelayDialer func(ctx context.Context, url string) (RelayConn, error)

// GoNostrDialer dials with the go-nostr client.
func GoNostrDialer(ctx context.Context, url string) (RelayConn, error) {
	r, err := nostr.RelayConnect(ctx, url)
	if err != nil {
		return nil, err
	}
	return &goNostrConn{relay: r}, nil
}

type goNostrConn struct {
	relay *nostr.Relay
}

func (c *goNostrConn) Subscribe(ctx context.Context, selfPub string, since nostr.Timestamp) (<-chan nostr.Event, error) {
	sub, err := c.relay.Subscribe(ctx, nostr.Filters{{
		Kinds: []int{nostr.KindGiftWrap},
		Tags:  nostr.TagMap{"p": []string{selfPub}},
		Since: &since,
	}})
	if err != nil {
		return nil, err
	}
	out := make(chan nostr.Event)
	go func() {
		defer close(out)
		for ev := range sub.Events {
			if ev == nil {
				return
			}
			select {
			case out <- *ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *goNostrConn) Publish(ctx context.Context, ev nostr.Event) error {
	return c.relay.Publish(ctx, ev)
}

func (c *goNostrConn) Close() error {
	return c.relay.Close()
}

// PoolConfig tunes the pool; zero values take the defaults noted.
type PoolConfig struct {
	Lookback         time.Duration // subscription since-window; default 72 h
	ReconnectBase    time.Duration // first-retry backoff; default 1 s
	ReconnectCap     time.Duration // backoff cap; default 60 s
	PublishTimeout   time.Duration // per-relay publish deadline; default 10 s
	DedupeWindowSize int           // recent event-id ring; default 4096
}

func (c *PoolConfig) defaults() {
	if c.Lookback == 0 {
		c.Lookback = 72 * time.Hour
	}
	if c.ReconnectBase == 0 {
		c.ReconnectBase = time.Second
	}
	if c.ReconnectCap == 0 {
		c.ReconnectCap = 60 * time.Second
	}
	if c.PublishTimeout == 0 {
		c.PublishTimeout = 10 * time.Second
	}
	if c.DedupeWindowSize == 0 {
		c.DedupeWindowSize = 4096
	}
}

// Pool maintains one connection per configured relay with jittered
// exponential reconnect, fans publishes out to every healthy relay, and
// merges inbound wraps into a single stream deduplicated by event id
// (Part 3 §6).
type Pool struct {
	cfg     PoolConfig
	dial    RelayDialer
	urls    []string
	selfPub string

	events chan nostr.Event

	mu    sync.Mutex
	conns map[string]RelayConn

	seen     map[string]struct{}
	seenRing []string
	seenIdx  int
}

func NewPool(dial RelayDialer, urls []string, selfPub string, cfg PoolConfig) *Pool {
	cfg.defaults()
	return &Pool{
		cfg:      cfg,
		dial:     dial,
		urls:     urls,
		selfPub:  selfPub,
		events:   make(chan nostr.Event, 64),
		conns:    make(map[string]RelayConn),
		seen:     make(map[string]struct{}, cfg.DedupeWindowSize),
		seenRing: make([]string, cfg.DedupeWindowSize),
	}
}

// Events is the merged, deduplicated inbound wrap stream.
func (p *Pool) Events() <-chan nostr.Event { return p.events }

// Healthy reports how many relays are currently connected.
func (p *Pool) Healthy() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// Run manages all relay connections until ctx is done. Blocks.
func (p *Pool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, url := range p.urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			p.manage(ctx, url)
		}(url)
	}
	wg.Wait()
}

// manage keeps one relay connected, subscribed, and pumped.
func (p *Pool) manage(ctx context.Context, url string) {
	backoff := p.cfg.ReconnectBase
	for ctx.Err() == nil {
		conn, err := p.dial(ctx, url)
		if err == nil {
			since := nostr.Timestamp(time.Now().Add(-p.cfg.Lookback).Unix())
			var sub <-chan nostr.Event
			sub, err = conn.Subscribe(ctx, p.selfPub, since)
			if err == nil {
				p.mu.Lock()
				p.conns[url] = conn
				p.mu.Unlock()
				backoff = p.cfg.ReconnectBase

				p.pump(ctx, sub) // returns when the subscription dies

				p.mu.Lock()
				delete(p.conns, url)
				p.mu.Unlock()
			}
			conn.Close()
		}
		if ctx.Err() != nil {
			return
		}
		// Jittered exponential backoff (Part 2 §12).
		jitter := time.Duration(rand.Int63n(int64(backoff)/2 + 1))
		select {
		case <-time.After(backoff + jitter):
		case <-ctx.Done():
			return
		}
		if backoff *= 2; backoff > p.cfg.ReconnectCap {
			backoff = p.cfg.ReconnectCap
		}
	}
}

func (p *Pool) pump(ctx context.Context, sub <-chan nostr.Event) {
	for {
		select {
		case ev, ok := <-sub:
			if !ok {
				return
			}
			if p.markSeen(ev.ID) {
				continue // duplicate across relays
			}
			select {
			case p.events <- ev:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// markSeen returns true if the id was already delivered; otherwise records
// it in the bounded ring.
func (p *Pool) markSeen(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.seen[id]; dup {
		return true
	}
	if old := p.seenRing[p.seenIdx]; old != "" {
		delete(p.seen, old)
	}
	p.seenRing[p.seenIdx] = id
	p.seenIdx = (p.seenIdx + 1) % len(p.seenRing)
	p.seen[id] = struct{}{}
	return false
}

// Publish fans the event out to every currently connected relay and returns
// how many accepted it. Zero means the event went nowhere — callers keep the
// item queued (the persistent queue is the durability, not the relays).
func (p *Pool) Publish(ctx context.Context, ev nostr.Event) int {
	p.mu.Lock()
	conns := make([]RelayConn, 0, len(p.conns))
	for _, c := range p.conns {
		conns = append(conns, c)
	}
	p.mu.Unlock()

	var wg sync.WaitGroup
	var cmu sync.Mutex
	count := 0
	for _, c := range conns {
		wg.Add(1)
		go func(c RelayConn) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, p.cfg.PublishTimeout)
			defer cancel()
			if err := c.Publish(pctx, ev); err == nil {
				cmu.Lock()
				count++
				cmu.Unlock()
			}
		}(c)
	}
	wg.Wait()
	return count
}
