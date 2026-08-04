// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package discover

import (
	"net"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

func newTestRejectCache(ttl time.Duration, max int) *rejectCache {
	return &rejectCache{
		entries: make(map[enode.ID]time.Time),
		ttl:     ttl,
		max:     max,
	}
}

func mkID(b byte) enode.ID {
	var id enode.ID
	id[0] = b
	return id
}

func TestRejectCacheAddContains(t *testing.T) {
	c := newTestRejectCache(time.Minute, 16)
	id := mkID(1)
	if c.Contains(id) {
		t.Fatalf("empty cache should not contain id")
	}
	c.Add(id)
	if !c.Contains(id) {
		t.Fatalf("cache should contain id after Add")
	}
	if c.Contains(mkID(2)) {
		t.Fatalf("cache should not contain unrelated id")
	}
}

func TestRejectCacheTTLExpiry(t *testing.T) {
	// 5ms TTL — short enough for a fast test, long enough to dodge scheduler jitter.
	c := newTestRejectCache(5*time.Millisecond, 16)
	id := mkID(7)
	c.Add(id)
	if !c.Contains(id) {
		t.Fatalf("entry should be live immediately after Add")
	}
	time.Sleep(20 * time.Millisecond)
	if c.Contains(id) {
		t.Fatalf("entry should have expired after TTL")
	}
	// Lazy delete should have removed it from the map.
	if c.Len() != 0 {
		t.Fatalf("expected empty cache after expiry+Contains, got len=%d", c.Len())
	}
}

func TestRejectCacheReAddRefreshesTTL(t *testing.T) {
	c := newTestRejectCache(40*time.Millisecond, 16)
	id := mkID(3)
	c.Add(id)
	time.Sleep(25 * time.Millisecond)
	c.Add(id) // refresh
	time.Sleep(25 * time.Millisecond)
	// Original TTL would have expired (50ms total since first Add), but the
	// refresh at 25ms means the entry is only 25ms old now.
	if !c.Contains(id) {
		t.Fatalf("re-Add should refresh TTL; entry expired prematurely")
	}
}

func TestRejectCacheCapEviction(t *testing.T) {
	const max = 8
	c := newTestRejectCache(time.Hour, max)
	// Insert max+5 distinct IDs. The cap should keep us at <= max.
	for i := 0; i < max+5; i++ {
		c.Add(mkID(byte(i + 1)))
	}
	if c.Len() > max {
		t.Fatalf("cache exceeded cap: len=%d, max=%d", c.Len(), max)
	}
	// The most recent insert must still be present.
	if !c.Contains(mkID(byte(max + 5))) {
		t.Fatalf("most recent insert should be retained")
	}
}

// newTestFilterTable creates a table with an accept-all node filter so the
// verifyAndAdd / lookup-time ENR verification paths are exercised.
func newTestFilterTable(t transport) (*Table, *enode.DB) {
	db, _ := enode.OpenDB("")
	tab, _ := newTable(t, db, nil, func(*enode.Node) bool { return true }, logging.Root())
	go tab.loop()
	return tab, db
}

// Regression: a node whose verified ENR is in the positive nodedb cache must
// not be dropped because of a stale reject-cache entry. Before the positive
// cache was consulted first, a single transient RequestENR failure (one
// dropped UDP packet) blacklisted a known-good node for rejectCacheTTL.
func TestVerifyAndAddPrefersNodedbOverRejectCache(t *testing.T) {
	transport := newPingRecorder()
	tab, db := newTestFilterTable(transport)
	defer db.Close()
	defer tab.close()

	n := nodeAtDistance(tab.self().ID(), 250, net.IP{203, 0, 113, 1})
	// The node has been verified before: its record is in the nodedb.
	if err := db.UpdateNode(unwrapNode(n)); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	// A transient RequestENR failure put it in the negative cache.
	tab.rejects.Add(n.ID())

	// The transport has no record for n, so any RequestENR round-trip would
	// fail; the add can only succeed through the nodedb fast path.
	tab.verifyAndAdd(n)

	if tab.getNode(n.ID()) == nil {
		t.Fatalf("nodedb-verified node was dropped due to reject-cache entry")
	}
}

// Regression: same as above, but for the lookup-time verification path in
// lookup.query.
func TestLookupPrefersNodedbOverRejectCache(t *testing.T) {
	transport := newPingRecorder()
	tab, db := newTestFilterTable(transport)
	defer db.Close()
	defer tab.close()

	target := nodeAtDistance(tab.self().ID(), 250, net.IP{203, 0, 113, 2})
	if err := db.UpdateNode(unwrapNode(target)); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	tab.rejects.Add(target.ID())

	it := &lookup{
		tab:       tab,
		queryfunc: func(*node) ([]*node, error) { return []*node{target}, nil },
	}
	reply := make(chan []*node, 1)
	asked := nodeAtDistance(tab.self().ID(), 240, net.IP{203, 0, 113, 3})
	it.query(asked, reply)

	for _, n := range <-reply {
		if n.ID() == target.ID() {
			return
		}
	}
	t.Fatalf("nodedb-verified node missing from lookup reply due to reject-cache entry")
}

func TestRejectCacheCapPrefersExpiredEviction(t *testing.T) {
	const max = 4
	c := newTestRejectCache(10*time.Millisecond, max)
	// Fill cap with short-TTL entries.
	for i := 0; i < max; i++ {
		c.Add(mkID(byte(i + 1)))
	}
	// Wait for them all to expire.
	time.Sleep(25 * time.Millisecond)
	// Adding past cap should sweep expired entries first; no live-entry
	// eviction needed. After this, the cache should contain only the new id.
	c.Add(mkID(99))
	if !c.Contains(mkID(99)) {
		t.Fatalf("newly added id missing")
	}
	if c.Len() != 1 {
		t.Fatalf("expected cache to drop expired entries on cap-overflow, got len=%d", c.Len())
	}
}
