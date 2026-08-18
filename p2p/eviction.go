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

package p2p

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"net"
	"sort"

	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
	"github.com/ParallaxProtocol/parallax/v2/util/mclock"
)

// Protection counts. Mirrors Bitcoin Core's eviction.cpp constants
// (src/node/eviction.cpp:178-240).
const (
	// evictProtectNetGroup peers with the highest keyed network-group
	// hash are preserved before any quality-based round runs.
	// Bitcoin Core uses 4 (eviction.cpp:188).
	evictProtectNetGroup = 4
	// evictProtectFastestPing peers with the lowest min-ping RTT
	// are preserved. Core uses 8 (eviction.cpp:191).
	evictProtectFastestPing = 8
	// evictProtectNewestTx peers with the most-recent tx relay
	// are preserved. Core uses 4 (eviction.cpp:194).
	evictProtectNewestTx = 4
	// evictProtectBlockRelay block-relay-only peers with newest
	// blocks are preserved. Core uses 8 (eviction.cpp:196).
	evictProtectBlockRelay = 8
	// evictProtectNewestBlock peers with the most-recent block
	// announce are preserved. Core uses 4 (eviction.cpp:201).
	evictProtectNewestBlock = 4
)

// evictNetGroupSalt is a per-process random salt for the keyed
// network-group hash. Bitcoin Core salts nKeyedNetGroup with a
// deterministic per-node randomizer (net.cpp CalculateKeyedNetGroup)
// precisely so an attacker cannot predict which network groups the
// diversity round will protect.
var evictNetGroupSalt = func() [16]byte {
	var s [16]byte
	if _, err := crand.Read(s[:]); err != nil {
		panic("p2p: eviction netgroup salt init: " + err.Error())
	}
	return s
}()

// keyedNetGroup returns the salted hash of p's network group.
// All peers in one /16 (or IPv6 /32) share a value; the value
// itself is unpredictable to an attacker. Peers with no derivable
// group (non-TCP transports) hash their node ID instead, preserving
// the "nil is a unique singleton group" contract of NetworkGroup.
func keyedNetGroup(p *Peer) uint64 {
	group := p.NetworkGroup()
	if group == nil {
		id := p.ID()
		group = id[:]
	}
	h := sha256.New()
	h.Write(evictNetGroupSalt[:])
	h.Write(group)
	var sum [sha256.Size]byte
	return binary.BigEndian.Uint64(h.Sum(sum[:0]))
}

// evictMinPing returns the peer's minimum measured ping RTT, or
// MaxInt64 when no pong has been received yet. Bitcoin Core
// initializes m_min_ping_time to microseconds::max() (src/net.h)
// so an unmeasured peer sorts as the WORST candidate in the
// lowest-ping protection round; a zero default would instead make
// a peer that simply never answers our pings look like the fastest
// peer we have and grant it permanent protection.
func evictMinPing(p *Peer) int64 {
	if v := p.minPing.Load(); v > 0 {
		return v
	}
	return math.MaxInt64
}

// evictionCandidates filters the full peer set down to the slice of
// inbound peers that may be evicted: trusted (Core: NoBan), static,
// outbound, and already-disconnecting peers are excluded.
//
// Mirrors Bitcoin Core's AttemptToEvictConnection candidate-build
// at src/net.cpp:1684-1711: the fDisconnect skip corresponds to our
// discRequested flag — a peer whose teardown is in flight must not
// be picked again, or concurrent admissions would all "pay" with
// the same victim and over-admit past the inbound cap. Core has no
// minimum-age filter and neither do we: brand-new connections are
// valid victims, which is what makes inbound churn attacks pay for
// themselves (the attacker's own newest connection is the natural
// pick of the youngest-in-largest-group rule).
func evictionCandidates(peers map[enode.ID]*Peer) []*Peer {
	out := make([]*Peer, 0, len(peers))
	for _, p := range peers {
		if p.rw.is(trustedConn) {
			continue
		}
		if p.rw.is(staticDialedConn) {
			continue
		}
		if !p.rw.is(inboundConn) {
			continue
		}
		if p.discRequested.Load() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// protectLastK mirrors Bitcoin Core's EraseLastKElements
// (src/node/eviction.cpp:76-84): stable-sort candidates ascending
// by less — so the peers most deserving protection land at the END
// of the slice — then remove (protect) the members of the last-k
// window for which keep returns true. Peers inside the window that
// fail the predicate stay candidates, and the window does NOT
// extend to compensate; that asymmetry is Core behavior.
//
// A nil keep predicate protects the whole window.
func protectLastK(candidates []*Peer, k int, less func(a, b *Peer) bool, keep func(*Peer) bool) []*Peer {
	if k <= 0 || len(candidates) == 0 {
		return candidates
	}
	sorted := make([]*Peer, len(candidates))
	copy(sorted, candidates)
	sort.SliceStable(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })
	if k > len(sorted) {
		k = len(sorted)
	}
	windowStart := len(sorted) - k
	out := sorted[:windowStart:windowStart]
	if keep != nil {
		for _, p := range sorted[windowStart:] {
			if !keep(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// Comparators, ascending = least deserving of protection first.
// Each is a direct port of the corresponding Bitcoin Core
// comparator in src/node/eviction.cpp, including the tie-break
// chains: equal-metric ties fall through to "longer connected is
// more protected" (a.m_connected > b.m_connected), which keeps the
// rounds deterministic instead of leaking Go map iteration order.

// lessNetGroupKeyed — CompareNetGroupKeyed (eviction.cpp:26).
func lessNetGroupKeyed(a, b *Peer) bool {
	return keyedNetGroup(a) < keyedNetGroup(b)
}

// lessMinPingReverse — ReverseCompareNodeMinPingTime
// (eviction.cpp:16): highest ping first, lowest ping protected.
func lessMinPingReverse(a, b *Peer) bool {
	return evictMinPing(a) > evictMinPing(b)
}

// lessTxTime — CompareNodeTXTime (eviction.cpp:38): oldest tx time
// first; ties prefer protecting tx-relayers, then longest-connected.
func lessTxTime(a, b *Peer) bool {
	at, bt := a.lastTxRx.Load(), b.lastTxRx.Load()
	if at != bt {
		return at < bt
	}
	ar, br := a.relayTxs.Load(), b.relayTxs.Load()
	if ar != br {
		return br
	}
	return a.Created() > b.Created()
}

// lessBlockRelayOnlyTime — CompareNodeBlockRelayOnlyTime
// (eviction.cpp:48): tx-relayers first (least protected), then
// oldest block time, then longest-connected protected.
func lessBlockRelayOnlyTime(a, b *Peer) bool {
	ar, br := a.relayTxs.Load(), b.relayTxs.Load()
	if ar != br {
		return ar
	}
	at, bt := a.lastBlockRx.Load(), b.lastBlockRx.Load()
	if at != bt {
		return at < bt
	}
	return a.Created() > b.Created()
}

// lessBlockTime — CompareNodeBlockTime (eviction.cpp:30): oldest
// block time first; ties protect longest-connected. (Core's
// fRelevantServices middle tie-break has no Parallax equivalent
// yet — every peer speaks the full protocol — so it is omitted
// rather than approximated.)
func lessBlockTime(a, b *Peer) bool {
	at, bt := a.lastBlockRx.Load(), b.lastBlockRx.Load()
	if at != bt {
		return at < bt
	}
	return a.Created() > b.Created()
}

// protectByRatio mirrors Bitcoin Core's
// ProtectEvictionCandidatesByRatio (src/node/eviction.cpp:105-176):
// reserve ~50% of the candidate pool for protection by uptime, with
// up to half of those slots (25% of candidates) set aside for
// disadvantaged-network peers first. Localhost is the only
// disadvantaged "network" Parallax accepts inbound (there is no
// Tor/I2P/CJDNS transport), and it needs the reservation badly:
// keyedNetGroup buckets every 127.x peer into one group, so without
// it, co-hosted peers on a multi-node machine are *preferential*
// victims of the largest-group round — the opposite of Core.
func protectByRatio(candidates []*Peer) []*Peer {
	total := len(candidates) / 2
	if total <= 0 {
		return candidates
	}
	// Localhost reservation: protect up to total/2 longest-connected
	// local peers. protectLastK's keep predicate skips non-local
	// window members without extending the window — the same
	// asymmetry as Core's EraseLastKElements.
	before := len(candidates)
	if maxByNetwork := total / 2; maxByNetwork > 0 {
		candidates = protectLastK(candidates, maxByNetwork, lessLocalNetworkTime, isLocalPeer)
	}
	// Protect the remainder of the 50% budget by uptime.
	remaining := total - (before - len(candidates))
	return protectLastK(candidates, remaining, lessUptime, nil)
}

// isLocalPeer reports whether the peer connected from localhost —
// Core's NodeEvictionCandidate.m_is_local (addr.IsLocal()).
func isLocalPeer(p *Peer) bool {
	ra, ok := p.RemoteAddr().(*net.TCPAddr)
	return ok && ra.IP.IsLoopback()
}

// lessLocalNetworkTime — CompareNodeNetworkTime(is_local=true)
// (eviction.cpp:39): localhost peers sort after everyone else, and
// among themselves the longest-connected land at the end (most
// protected).
func lessLocalNetworkTime(a, b *Peer) bool {
	al, bl := isLocalPeer(a), isLocalPeer(b)
	if al != bl {
		return !al
	}
	return a.Created() > b.Created()
}

// lessUptime — ReverseCompareNodeTimeConnected (eviction.cpp:26):
// longest-connected peers land at the end (most protected).
func lessUptime(a, b *Peer) bool {
	return a.Created() > b.Created()
}

// preferEvict narrows the surviving candidates to peers flagged for
// discouragement, when any exist. Mirrors Core's prefer_evict
// filter (eviction.cpp:209-215): a peer we already caught
// misbehaving — this session (ShouldDiscourage) or before admission
// (PreferEvict, the discourage-filter membership stamped at accept,
// Core's m_prefer_evict) — should absorb the eviction before any
// well-behaved survivor does. Runs after the protection rounds on
// purpose — if a misbehaving peer is genuinely our best block
// source, Core prefers keeping it anyway, and so do we.
func preferEvict(candidates []*Peer) []*Peer {
	flagged := make([]*Peer, 0, len(candidates))
	for _, p := range candidates {
		if p.ShouldDiscourage() || p.PreferEvict() {
			flagged = append(flagged, p)
		}
	}
	if len(flagged) == 0 {
		return candidates
	}
	return flagged
}

// pickEvictionVictim is the final step after all protection rounds.
// Mirrors src/node/eviction.cpp:217-239:
//   - group survivors by keyed network group;
//   - find the most-populated group (ties → group with youngest
//     representative connection);
//   - within that group, evict the youngest peer.
//
// Returns nil if candidates is empty (caller's no-op signal).
func pickEvictionVictim(candidates []*Peer) *Peer {
	if len(candidates) == 0 {
		return nil
	}
	groups := map[uint64][]*Peer{}
	for _, p := range candidates {
		key := keyedNetGroup(p)
		groups[key] = append(groups[key], p)
	}
	var bestGroup []*Peer
	bestCount := 0
	var bestYoungest mclock.AbsTime
	var bestKey uint64
	for key, members := range groups {
		count := len(members)
		// Find youngest in this group.
		youngest := members[0].Created()
		for _, m := range members {
			if m.Created() > youngest {
				youngest = m.Created()
			}
		}
		// The final key comparison exists only to keep the pick
		// deterministic when two groups tie on both count and
		// youngest-connection time — Go map iteration order is
		// random, whereas Core's banked std::map walks groups in
		// key order and always resolves such ties the same way.
		switch {
		case count > bestCount:
			bestGroup, bestCount, bestYoungest, bestKey = members, count, youngest, key
		case count == bestCount && youngest > bestYoungest:
			bestGroup, bestYoungest, bestKey = members, youngest, key
		case count == bestCount && youngest == bestYoungest && bestGroup != nil && key > bestKey:
			bestGroup, bestKey = members, key
		}
	}
	// Within bestGroup, return the youngest member.
	victim := bestGroup[0]
	for _, m := range bestGroup {
		if m.Created() > victim.Created() {
			victim = m
		}
	}
	return victim
}

// evictInbound runs the full Bitcoin-Core-equivalent eviction
// pipeline against the current peer set and disconnects the
// selected victim. Returns true if an eviction was performed
// (success → caller can optimistically accept a new peer);
// false if no candidate survived all protection rounds (caller
// should hard-reject).
//
// Lifts the structure from src/node/eviction.cpp:178-240
// SelectNodeToEvict. The caller (postHandshakeChecks) passes
// the current peers map; this function does NOT acquire any
// peer-map lock — the run loop owns the map and invokes us
// holding ownership.
func (srv *Server) evictInbound(peers map[enode.ID]*Peer) bool {
	candidates := evictionCandidates(peers)
	if len(candidates) == 0 {
		return false
	}

	// Round 1: preserve network-group diversity (keyed hash, so an
	// attacker cannot predict which groups win the slots).
	candidates = protectLastK(candidates, evictProtectNetGroup, lessNetGroupKeyed, nil)
	// Round 2: preserve fastest pings (lowest measured minPing;
	// unmeasured counts as worst).
	candidates = protectLastK(candidates, evictProtectFastestPing, lessMinPingReverse, nil)
	// Round 3: preserve newest tx activity.
	candidates = protectLastK(candidates, evictProtectNewestTx, lessTxTime, nil)
	// Round 4: preserve newest blocks among non-tx-relay peers.
	// Core's extra fRelevantServices gate has no equivalent here.
	candidates = protectLastK(candidates, evictProtectBlockRelay, lessBlockRelayOnlyTime,
		func(p *Peer) bool { return !p.relayTxs.Load() })
	// Round 5: preserve newest block announces overall.
	candidates = protectLastK(candidates, evictProtectNewestBlock, lessBlockTime, nil)
	// Round 6: preserve oldest 50%.
	candidates = protectByRatio(candidates)
	// If any survivor is already flagged for discouragement, evict
	// among those first (Core prefer_evict).
	candidates = preferEvict(candidates)

	victim := pickEvictionVictim(candidates)
	if victim == nil {
		return false
	}
	srv.log.Debug("Evicting inbound peer to admit new connection",
		"id", victim.ID(), "addr", victim.RemoteAddr(),
		"discouraged", victim.ShouldDiscourage())
	victim.Disconnect(DiscTooManyPeers)
	return true
}
