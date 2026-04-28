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
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

const (
	// rejectCacheTTL is how long a node ID stays in the negative cache after
	// a verification failure. Long enough to suppress the FINDNODE-driven
	// re-verification storm (every neighbor that knows the dead/non-Parallax
	// peer returns it on every random-target lookup), short enough that a
	// peer which legitimately rejoined the network with a fresh ENR is not
	// punished forever.
	rejectCacheTTL = 5 * time.Minute

	// rejectCacheMax bounds memory use. At a steady-state of one Ethereum-
	// network ID per FINDNODE response and ~30 lookups/min, 4096 covers more
	// than 2 hours of rejected-ID history.
	rejectCacheMax = 4096
)

// rejectCache is a TTL+capacity-bounded set of node IDs that recently failed
// verification (RequestENR error or nodeFilter rejection). Hits short-circuit
// the verifyAndAdd / lookup-time RequestENR round-trip, suppressing the storm
// of repeat ENR fetches caused by non-Parallax peers being returned on every
// neighbor's FINDNODE response.
//
// Best-effort: when over capacity, the entry with the soonest expiry is
// evicted. The eviction scan is O(n) but only runs at the cap boundary.
type rejectCache struct {
	mu      sync.Mutex
	entries map[enode.ID]time.Time // value = absolute expiry
	ttl     time.Duration
	max     int
}

func newRejectCache() *rejectCache {
	return &rejectCache{
		entries: make(map[enode.ID]time.Time),
		ttl:     rejectCacheTTL,
		max:     rejectCacheMax,
	}
}

// Add records id as recently rejected. Re-adding refreshes the TTL.
func (c *rejectCache) Add(id enode.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = time.Now().Add(c.ttl)
	if len(c.entries) > c.max {
		c.evictLocked()
	}
}

// Contains reports whether id is in the cache and not yet expired. Expired
// entries are removed lazily as they are encountered.
func (c *rejectCache) Contains(id enode.ID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.entries[id]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(c.entries, id)
		return false
	}
	return true
}

// Len returns the current number of (not-yet-lazily-expired) entries. Useful
// for tests and metrics; do not branch on this in hot paths.
func (c *rejectCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// evictLocked drops expired entries first, then if still over cap removes the
// entry with the soonest expiry. Called only when len exceeds max.
func (c *rejectCache) evictLocked() {
	now := time.Now()
	for id, exp := range c.entries {
		if now.After(exp) {
			delete(c.entries, id)
		}
	}
	if len(c.entries) <= c.max {
		return
	}
	var oldestID enode.ID
	var oldestExp time.Time
	first := true
	for id, exp := range c.entries {
		if first || exp.Before(oldestExp) {
			oldestID = id
			oldestExp = exp
			first = false
		}
	}
	if !first {
		delete(c.entries, oldestID)
	}
}
