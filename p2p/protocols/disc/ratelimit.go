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
// semantics in src/net_processing.cpp.
//
// Bitcoin's numbers: inbound 0.1 addr/s with burst 1, outbound 1.0
// addr/s with burst 10. Addresses over the rate are dropped silently,
// not disconnected — rate-exceed-as-disconnect is a DoS vector against
// honest peers under load.
const (
	inboundRate   = 0.1 // tokens per second
	inboundBurst  = 1.0
	outboundRate  = 1.0
	outboundBurst = 10.0

	// BloomSize / BloomHashes are the per-peer known-address filter
	// sizing. 5000 elements at 0.001 false-positive rate → ~72k bits,
	// 10 hashes per insert. We implement a simple FNV-based bloom
	// rather than pulling in a dependency — any inaccuracy is
	// acceptable (a false positive just means we occasionally fail to
	// relay one address to one peer, which is harmless).
	bloomBits   = 73984 // ~72 kbit, byte-aligned
	bloomBytes  = bloomBits / 8
	bloomHashes = 10
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

func newTokenBucket(rate, burst float64) *tokenBucket {
	return &tokenBucket{
		rate:     rate,
		burst:    burst,
		level:    burst,
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

// bloomFilter is a fixed-size counting-free bloom filter. Thread-safe.
// One per peer. Cleared on session start (fresh allocation).
type bloomFilter struct {
	mu   sync.Mutex
	bits [bloomBytes]byte
}

// Contains checks whether key may have been seen. False positives are
// possible; false negatives are not.
func (f *bloomFilter) Contains(key []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < bloomHashes; i++ {
		pos := bloomHash(key, uint32(i)) % uint32(bloomBits)
		f.bits[pos/8] |= 1 << (pos % 8)
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
