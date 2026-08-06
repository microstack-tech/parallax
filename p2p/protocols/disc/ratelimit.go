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

package disc

import (
	"encoding/binary"
	"hash/fnv"
	"sync"
	"time"
)

// Token bucket constants — mirror Bitcoin Core's m_addr_token_bucket
// semantics in src/net_processing.cpp exactly. One model for every
// peer, inbound and outbound alike:
//
//	MAX_ADDR_RATE_PER_SECOND         = 0.1 tokens/s
//	MAX_ADDR_PROCESSING_TOKEN_BUCKET = 1000 (soft cap, = MAX_ADDR_TO_SEND)
//	initial fill                     = 1.0
//
// The 1000-token soft cap is what lets an idle session absorb an
// honest gossip burst: at 0.1/s the bucket accumulates ~360 tokens an
// hour, so a peer that has been quiet all day can deliver a large
// batch at once, while a peer streaming addresses is held to the
// steady 0.1/s. Addresses over the rate are dropped silently, not
// disconnected — rate-exceed-as-disconnect is a DoS vector against
// honest peers under load.
const (
	addrRatePerSecond   = 0.1
	addrTokenBucketCap  = 1000.0
	addrTokenBucketInit = 1.0

	// BloomSize / BloomHashes are the per-peer known-address filter
	// sizing. 5000 elements at 0.001 false-positive rate → ~72k bits,
	// 10 hashes per insert. We implement a simple FNV-based bloom
	// rather than pulling in a dependency — any inaccuracy is
	// acceptable (a false positive just means we occasionally fail to
	// relay one address to one peer, which is harmless).
	bloomBits   = 73984 // ~72 kbit, byte-aligned
	bloomBytes  = bloomBits / 8
	bloomHashes = 10

	// bloomGenerationCap is the insert budget of one rolling-filter
	// generation. Three generations at (n+1)/2 inserts each guarantee
	// the filter remembers at least the n=5000 most recent keys —
	// the same scheme as Bitcoin's CRollingBloomFilter{5000, 0.001}
	// backing m_addr_known.
	bloomGenerationCap = (5000 + 1) / 2
)

// tokenBucket is a classic leaky-bucket rate limiter. Refill happens
// lazily on Take; no background goroutine.
type tokenBucket struct {
	mu       sync.Mutex
	rate     float64 // tokens/sec
	burst    float64
	level    float64
	lastFill time.Time
}

// newTokenBucket returns a bucket with the given refill rate, soft
// cap, and initial fill. The initial fill is 1.0 in production
// (Core's m_addr_token_bucket{1.0}): a brand-new session gets one
// address through immediately and earns the rest at the refill rate.
func newTokenBucket(rate, burst, initial float64) *tokenBucket {
	return &tokenBucket{
		rate:     rate,
		burst:    burst,
		level:    initial,
		lastFill: time.Now(),
	}
}

// Take attempts to draw one token. Returns true if a token was
// available. Thread-safe; called once per inbound address.
func (b *tokenBucket) Take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed > 0 {
		// Refill tops the level toward burst but never past it, and
		// never touches a level already above burst — a Credit for a
		// solicited response may legitimately hold the level there,
		// and clamping would destroy the credit (Bitcoin: "Don't
		// increment bucket if it's already full").
		if b.level < b.burst {
			b.level += elapsed * b.rate
			if b.level > b.burst {
				b.level = b.burst
			}
		}
		b.lastFill = now
	}
	if b.level >= 1.0 {
		b.level -= 1.0
		return true
	}
	return false
}

// Credit adds n tokens, allowing the level to exceed burst. Used when
// soliciting a GetPeers response: the reply may carry a full
// MaxPeersPerMessage batch, which must bypass the steady-state gossip
// rate (Bitcoin: peer.m_addr_token_bucket += MAX_ADDR_TO_SEND on
// getaddr send). The excess drains through Take; lazy refill never
// raises the level past burst on its own.
func (b *tokenBucket) Credit(n float64) {
	b.mu.Lock()
	b.level += n
	b.mu.Unlock()
}

// Level returns the current fill level. Read-only; for tests.
func (b *tokenBucket) Level() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.level
}

// bloomFilter is a fixed-size counting-free bloom filter. NOT
// thread-safe on its own — it is a generation inside rollingBloom,
// which serializes access under its mutex.
type bloomFilter struct {
	bits [bloomBytes]byte
}

// Contains checks whether key may have been seen. False positives are
// possible; false negatives are not.
func (f *bloomFilter) Contains(key []byte) bool {
	for i := 0; i < bloomHashes; i++ {
		pos := bloomHash(key, uint32(i)) % uint32(bloomBits)
		if f.bits[pos/8]&(1<<(pos%8)) == 0 {
			return false
		}
	}
	return true
}

// Add marks key as seen.
func (f *bloomFilter) Add(key []byte) {
	for i := 0; i < bloomHashes; i++ {
		pos := bloomHash(key, uint32(i)) % uint32(bloomBits)
		f.bits[pos/8] |= 1 << (pos % 8)
	}
}

// rollingBloom keeps the most recent ~5000 keys by cycling three
// bloom generations: inserts land in the current generation, and when
// it reaches its insert budget the oldest generation is dropped. A
// session that relays addresses for weeks therefore never saturates
// the filter (a fixed filter's false-positive rate climbs toward 1
// and silently stops all relay to that peer — worst on the long-lived
// backbone links). Mirrors Bitcoin's CRollingBloomFilter behavior for
// m_addr_known. The zero value is ready to use.
type rollingBloom struct {
	mu   sync.Mutex
	gens [3]bloomFilter
	// cur indexes the generation receiving inserts; curCount is the
	// number of inserts it has absorbed.
	cur      int
	curCount int
}

// Contains checks whether key may have been seen in any live
// generation. False positives possible, false negatives not (within
// the retention window).
func (r *rollingBloom) Contains(key []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.gens {
		if r.gens[i].Contains(key) {
			return true
		}
	}
	return false
}

// Add marks key as seen, rotating generations when the current one
// is full.
func (r *rollingBloom) Add(key []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gens[r.cur].Add(key)
	r.curCount++
	if r.curCount >= bloomGenerationCap {
		r.cur = (r.cur + 1) % len(r.gens)
		r.gens[r.cur] = bloomFilter{}
		r.curCount = 0
	}
}

func bloomHash(key []byte, seed uint32) uint32 {
	h := fnv.New32a()
	var sbuf [4]byte
	binary.LittleEndian.PutUint32(sbuf[:], seed)
	_, _ = h.Write(sbuf[:])
	_, _ = h.Write(key)
	return h.Sum32()
}

// addressKey produces the canonical byte-key for bloom and token-bucket
// deduplication — (net, addr, port) packed the same way Quorum's
// reportedAddr uses.
func addressKey(net uint8, addr []byte, port uint16) []byte {
	b := make([]byte, 0, 1+len(addr)+2)
	b = append(b, net)
	b = append(b, addr...)
	b = append(b, byte(port>>8), byte(port))
	return b
}
