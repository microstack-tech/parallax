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
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
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

	// rejectCachePerIPMax bounds how many live entries a single source
	// bucket (IPv4 address, or IPv6 /64 — one host routinely controls
	// a whole /64) can hold. Node IDs are free to generate, so without
	// this a burst of (freshID, oneIP) entries fills the whole cache
	// in minutes and starves the legitimate suppressions the cache
	// exists for. Real endpoints produce at most a handful of distinct
	// dead IDs per address (a NAT fronting several nodes); 32 is
	// generous for that. Note the entry IPs come from untrusted
	// FINDNODE advertisements, so the cap bounds damage per *named*
	// source bucket, not per attacker — an attacker naming many
	// distinct IPs is instead bounded by the global cap, where the
	// worst case is dropped newcomers (lost suppression, never false
	// rejection: Contains cannot false-positive).
	rejectCachePerIPMax = 32
)

// rejectKey identifies one rejected verification target. Keyed by
// (node ID, claimed IP), not node ID alone: FINDNODE neighbors are
// untrusted, so a malicious neighbor returning goodID@attackerIP must
// not be able to poison the cache for goodID@goodIP — the failed
// RequestENR only proves the (ID, endpoint) pair it was sent to is
// bad. The anti-storm property is preserved because the storm case is
// the same dead/non-Parallax node at its one real endpoint being
// echoed by every neighbor.
type rejectKey struct {
	id enode.ID
	ip [16]byte // 16-byte form; zero for nodes without an IP
}

func makeRejectKey(id enode.ID, ip net.IP) rejectKey {
	k := rejectKey{id: id}
	if ip16 := ip.To16(); ip16 != nil {
		copy(k.ip[:], ip16)
	}
	return k
}

// rejectCache is a TTL+capacity-bounded set of (node ID, IP) pairs that
// recently failed verification (RequestENR error or nodeFilter rejection).
// Hits short-circuit the verifyAndAdd / lookup-time RequestENR round-trip,
// suppressing the storm of repeat ENR fetches caused by non-Parallax peers
// being returned on every neighbor's FINDNODE response.
//
// Best-effort: at capacity, expired entries are swept and — if the cache
// is still full of live entries — the NEWCOMER is dropped rather than a
// live entry evicted. Evicting live entries would let an attacker with
// cheaply-generated failing keys churn the cache and flush the
// legitimate suppressions, restoring the very RequestENR storm the
// cache exists to prevent; a full cache, by contrast, is already doing
// suppression work, and the dropped newcomer costs at most one extra
// round-trip per occurrence for the TTL window. The sweep is O(n) but
// runs only at the cap boundary.
type rejectCache struct {
	mu       sync.Mutex
	entries  map[rejectKey]time.Time // value = absolute expiry
	perIP    map[[16]byte]int        // live entries per source bucket (see rejectCachePerIPMax)
	ttl      time.Duration
	max      int
	perIPCap int
}

// quotaKeyOf buckets an entry's IP for the per-source quota: the full
// address for IPv4, the /64 prefix for IPv6. A single host routinely
// holds an entire routed /64, so keying the quota by full IPv6
// address would hand it unbounded distinct sources — Bitcoin Core
// aggregates per-source limits by netgroup for the same reason.
func quotaKeyOf(ip [16]byte) [16]byte {
	if net.IP(ip[:]).To4() != nil {
		return ip
	}
	var k [16]byte
	copy(k[:8], ip[:8])
	return k
}

func newRejectCache() *rejectCache {
	return &rejectCache{
		entries:  make(map[rejectKey]time.Time),
		perIP:    make(map[[16]byte]int),
		ttl:      rejectCacheTTL,
		max:      rejectCacheMax,
		perIPCap: rejectCachePerIPMax,
	}
}

// Add records (id, ip) as recently rejected. Re-adding refreshes the
// TTL. When the cache is full of live entries, or the IP already holds
// its per-IP quota, the newcomer is dropped (see the type comment for
// why live entries are never evicted).
func (c *rejectCache) Add(id enode.ID, ip net.IP) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	key := makeRejectKey(id, ip)
	if _, exists := c.entries[key]; !exists {
		qk := quotaKeyOf(key.ip)
		if len(c.entries) >= c.max || c.perIP[qk] >= c.perIPCap {
			c.sweepExpiredLocked(now)
			if len(c.entries) >= c.max || c.perIP[qk] >= c.perIPCap {
				return
			}
		}
		c.perIP[qk]++
	}
	c.entries[key] = now.Add(c.ttl)
}

// Contains reports whether (id, ip) is in the cache and not yet expired.
// Expired entries are removed lazily as they are encountered.
func (c *rejectCache) Contains(id enode.ID, ip net.IP) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := makeRejectKey(id, ip)
	exp, ok := c.entries[key]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		c.deleteLocked(key)
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

// deleteLocked removes an entry and releases its per-source quota slot.
func (c *rejectCache) deleteLocked(key rejectKey) {
	delete(c.entries, key)
	qk := quotaKeyOf(key.ip)
	if n := c.perIP[qk]; n <= 1 {
		delete(c.perIP, qk)
	} else {
		c.perIP[qk] = n - 1
	}
}

// sweepExpiredLocked drops expired entries. Called only at the cap
// boundaries, so the O(n) scan stays off the handlePing hot path.
func (c *rejectCache) sweepExpiredLocked(now time.Time) {
	for key, exp := range c.entries {
		if now.After(exp) {
			c.deleteLocked(key)
		}
	}
}
