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
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/p2p"
)

// RelayInterval is the rotation period for the deterministic
// relay-destination pick (Bitcoin's ROTATE_ADDR_RELAY_DEST_INTERVAL,
// 24h). A given (address, day) pair always selects the same N peers
// across the day, so the m_addr_known dedup on the receiving side
// stops repeats and an attacker can't bias the pick by re-sending.
const RelayInterval = 24 * time.Hour

// RelayFanOutMax is the maximum peer fan-out per relayed address.
// Reachable addresses are relayed to 2 peers; unreachable to 1 or 2
// (50/50 by deterministic coin flip), matching Bitcoin's RelayAddress
// in src/net_processing.cpp:2302-2303.
const RelayFanOutMax = 2

// peerRelayState is per-peer relay-side bookkeeping owned by the
// backend. The outbox channel is fed by RelayAddress and drained by
// the peer's handler.Run goroutine. stop is closed by
// UnregisterPeerOutbox so the drain goroutine exits; the outbox
// channel itself is NEVER closed by the backend — closing it would
// race with concurrent in-flight RelayAddress sends that already hold
// the channel pointer via snapshotRelayCandidates and would panic the
// daemon under peer churn.
type peerRelayState struct {
	outbox chan<- PeerEntry
	stop   chan struct{}
}

// RegisterPeerOutbox records a peer's relay outbox channel. The
// handler.Run goroutine spawns a drain sub-goroutine that consumes
// from outbox and emits Peers messages to the peer (gated on the
// peer's known-addr bloom). Returns the stop channel the drain must
// select on so it exits when UnregisterPeerOutbox runs. Idempotent on
// re-register: an existing entry's stop is closed first so the prior
// drain unwinds.
func (b *AddrmanBackend) RegisterPeerOutbox(key PeerKey, outbox chan<- PeerEntry) <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if prev, ok := b.peerOutboxes[key]; ok {
		select {
		case <-prev.stop:
		default:
			close(prev.stop)
		}
	}
	st := &peerRelayState{outbox: outbox, stop: make(chan struct{})}
	b.peerOutboxes[key] = st
	return st.stop
}

// UnregisterPeerOutbox is the matching cleanup. Called from
// handler.Run's defer chain on session close with the stop channel
// that session's Register returned: the registration is only removed
// when it still belongs to that session. Without the ownership check,
// a session replaced via re-register would — in its deferred
// cleanup — tear down the REPLACEMENT session's outbox, silently
// dropping that peer from relay fan-out for its whole lifetime.
// Closes the stop channel (signalling the drain to exit) but NEVER
// closes the outbox itself — any in-flight RelayAddress send that
// already snapshotted this peer's state must complete (or hit the
// non-blocking default) without hitting a closed channel. Idempotent.
func (b *AddrmanBackend) UnregisterPeerOutbox(key PeerKey, stop <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.peerOutboxes[key]
	if !ok || st.stop != stop {
		// Unknown key, or the registration belongs to a different
		// session (including callers that never registered and pass
		// nil): nothing of ours to clean up.
		return
	}
	delete(b.peerOutboxes, key)
	select {
	case <-st.stop:
	default:
		close(st.stop)
	}
}

// RelayAddress fans out a freshly-ingested address to 1-2 peers
// chosen via a per-process secret + daily-rotating SipHash-equivalent
// (we use a keyed SHA-256 PRF; the security argument is identical and
// avoids pulling in a SipHash dependency). Originator is excluded from
// the candidate set.
//
// The pick is deterministic per (address, day), so the same ~2 peers
// receive a given address all day. Combined with the peer-side
// known-addr bloom (Bitcoin's m_addr_known), this means a given
// address is forwarded at most twice per day across the entire local
// peer set — bounding gossip amplification.
//
// Send is non-blocking: a peer whose outbox is full is skipped (the
// drain goroutine fell behind, probably because the underlying
// connection is slow). Bitcoin's behavior is the same — drops over
// blocking the writer.
func (b *AddrmanBackend) RelayAddress(originator *p2p.Peer, entry PeerEntry, reachable bool) {
	now := time.Now().UTC()
	b.relayAddressAt(originator, entry, reachable, now)
}

// relayAddressAt is the testable variant of RelayAddress with a
// caller-supplied clock. Internal to the package.
func (b *AddrmanBackend) relayAddressAt(originator *p2p.Peer, entry PeerEntry, reachable bool, now time.Time) {
	addrKey := addressKey(entry.NetworkID, entry.Addr, entry.TCPPort)
	addrHash := relayAddrHash(addrKey)
	dayBucket := relayDayBucket(now, addrHash)

	originKey := PeerKey("")
	if originator != nil {
		originKey = peerKeyFor(originator)
	}

	candidates := b.snapshotRelayCandidates(originKey)
	if len(candidates) == 0 {
		return
	}

	picks, fanout := pickRelayPeers(b.relayKey[:], addrHash, dayBucket, reachable, candidates)
	if fanout == 0 {
		return
	}
	for _, key := range picks {
		state := candidates[key]
		if state == nil {
			continue
		}
		// Three-way select: drain has exited (stop closed) wins
		// before the send, so a torn-down peer doesn't accumulate
		// dead entries; channel-full hits default and drops. The
		// outbox is never closed, so a select-send to it can't panic.
		select {
		case <-state.stop:
		case state.outbox <- entry:
		default:
			b.log.Trace("parallax-disc/1: relay outbox full, dropping",
				"peer", key)
		}
	}
}

// snapshotRelayCandidates copies the currently-registered outboxes
// excluding originator. Holds b.mu only for the snapshot duration —
// the picker runs unlocked against the snapshot. Returns a map keyed
// by PeerKey so RelayAddress can look up the chosen states.
func (b *AddrmanBackend) snapshotRelayCandidates(originator PeerKey) map[PeerKey]*peerRelayState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[PeerKey]*peerRelayState, len(b.peerOutboxes))
	for k, st := range b.peerOutboxes {
		if k == originator {
			continue
		}
		out[k] = st
	}
	return out
}

// relayAddrHash hashes the address key into a stable 64-bit integer.
// Used both as a per-address jitter for the day-bucket and as input
// to the per-peer pick PRF.
func relayAddrHash(addrKey []byte) uint64 {
	sum := sha256.Sum256(append([]byte("parallax-disc/relay-addr/"), addrKey...))
	return binary.BigEndian.Uint64(sum[:8])
}

// relayDayBucket returns the rotating-day index for a given address.
// The +addrHash jitter means each address rotates at a different
// instant within the day, so an attacker observing a wave of
// "everyone gets a new pick at midnight" can't time their messages
// to land on the rotation boundary.
func relayDayBucket(now time.Time, addrHash uint64) uint64 {
	return (uint64(now.Unix()) + addrHash) / uint64(RelayInterval/time.Second)
}

// pickRelayPeers performs the per-peer keyed-hash pick. Returns the
// chosen peer keys (length 1 or 2 = fanout) and the fanout used.
//
// The PRF is sha256(relayKey || "v1" || addrHashBE || dayBucketBE ||
// peerKey). Strictly stronger than Bitcoin's SipHash-2-4 with a
// 16-byte key; throughput is plenty (each pick is one SHA-256 round
// per peer, and we run this on newly-learned addresses at gossip
// cadence — well under 100/sec).
//
// Fanout selection: 2 if reachable; otherwise 1 or 2 by the low bit
// of a base PRF over (addrHash, dayBucket). Matches Bitcoin's
// RelayAddress nRelayNodes coin-flip.
func pickRelayPeers(relayKey []byte, addrHash, dayBucket uint64, reachable bool, candidates map[PeerKey]*peerRelayState) ([]PeerKey, int) {
	if len(candidates) == 0 {
		return nil, 0
	}

	// Fanout coin flip uses a base PRF over (addrHash, dayBucket)
	// keyed by relayKey — independent of any per-peer pick.
	base := relayPRF(relayKey, addrHash, dayBucket, "")
	fanout := 1
	if reachable || (base&1) == 1 {
		fanout = RelayFanOutMax
	}
	if fanout > len(candidates) {
		fanout = len(candidates)
	}

	type scored struct {
		key  PeerKey
		hash uint64
	}
	scoredAll := make([]scored, 0, len(candidates))
	for k := range candidates {
		scoredAll = append(scoredAll, scored{
			key:  k,
			hash: relayPRF(relayKey, addrHash, dayBucket, k),
		})
	}
	// Sort descending so top-N are first. Tie-break by peer key for
	// determinism (extremely unlikely with a 64-bit PRF, but cheap).
	sort.Slice(scoredAll, func(i, j int) bool {
		if scoredAll[i].hash != scoredAll[j].hash {
			return scoredAll[i].hash > scoredAll[j].hash
		}
		return scoredAll[i].key < scoredAll[j].key
	})
	out := make([]PeerKey, 0, fanout)
	for i := 0; i < fanout; i++ {
		out = append(out, scoredAll[i].key)
	}
	return out, fanout
}

// relayPRF is the keyed hash used both for fanout selection (peer="")
// and per-peer picks (peer=PeerKey). Encoding is length-prefix-free
// because each input is fixed-width or distinguishable by domain
// label — a peer key string can't collide with the empty fanout slot.
func relayPRF(relayKey []byte, addrHash, dayBucket uint64, peer PeerKey) uint64 {
	h := sha256.New()
	_, _ = h.Write(relayKey)
	_, _ = h.Write([]byte("|relay/v1|"))
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], addrHash)
	binary.BigEndian.PutUint64(buf[8:16], dayBucket)
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte("|peer|"))
	_, _ = h.Write([]byte(peer))
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}
