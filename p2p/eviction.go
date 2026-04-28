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
	"sort"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/util/mclock"
)

// Protection counts. Mirrors Bitcoin Core's eviction.cpp constants
// (src/node/eviction.cpp:178-240). Tunable via Server config when
// operator wants different anti-eclipse trade-offs.
const (
	// evictProtectNetGroup peers with diverse network groups are
	// preserved before any quality-based round runs. Bitcoin Core
	// uses 4 (eviction.cpp:188).
	evictProtectNetGroup = 4
	// evictProtectFastestPing peers with the lowest min-ping RTT
	// are preserved. Core uses 8 (eviction.cpp:191).
	evictProtectFastestPing = 8
	// evictProtectNewestTx peers with the most-recent tx relay
	// are preserved (only counts tx-relay-enabled peers). Core
	// uses 4 (eviction.cpp:194).
	evictProtectNewestTx = 4
	// evictProtectBlockRelay block-relay-only peers with newest
	// blocks are preserved. Core uses 8 (eviction.cpp:196).
	evictProtectBlockRelay = 8
	// evictProtectNewestBlock peers with the most-recent block
	// announce are preserved. Core uses 4 (eviction.cpp:201).
	evictProtectNewestBlock = 4
	// evictMinAge is the minimum connection age before a peer can
	// be evicted. Bitcoin Core's MINIMUM_CONNECT_TIME = 30s
	// (src/net_processing.cpp:112).
	evictMinAge = 30 * time.Second
)

// evictionCandidates filters the full peer set down to the slice of
// inbound peers that may be evicted: trusted, static, and
// recently-connected peers are excluded.
//
// Mirrors Bitcoin Core's AttemptToEvictConnection candidate-build
// at src/net.cpp:1684-1731. Outbound peers are excluded by the
// "remove non-INBOUND" pass at eviction.cpp:96; trusted peers by
// "remove m_noban" at eviction.cpp:87.
func evictionCandidates(peers map[enode.ID]*Peer, now mclock.AbsTime) []*Peer {
	out := make([]*Peer, 0, len(peers))
	minAgeNs := int64(evictMinAge)
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
		if int64(now)-int64(p.created) < minAgeNs {
			continue
		}
		out = append(out, p)
	}
	return out
}

// protectByMin removes the n peers with the smallest values
// returned by metric. Used for "lowest min ping" preservation
// (and other "low-is-better" rounds). Returns the surviving slice.
//
// If n >= len(candidates) returns an empty slice (everyone
// protected). Stable: ties don't reorder beyond what sort.Slice
// guarantees.
func protectByMin(candidates []*Peer, n int, metric func(*Peer) int64) []*Peer {
	if n <= 0 {
		return candidates
	}
	if n >= len(candidates) {
		return candidates[:0]
	}
	sorted := make([]*Peer, len(candidates))
	copy(sorted, candidates)
	sort.SliceStable(sorted, func(i, j int) bool {
		return metric(sorted[i]) < metric(sorted[j])
	})
	// Drop the first n (lowest values) — they are protected.
	return sorted[n:]
}

// protectByMax removes the n peers with the largest values
// returned by metric. Used for "newest" preservation rounds
// (high-is-better). Returns the surviving slice.
func protectByMax(candidates []*Peer, n int, metric func(*Peer) int64) []*Peer {
	if n <= 0 {
		return candidates
	}
	if n >= len(candidates) {
		return candidates[:0]
	}
	sorted := make([]*Peer, len(candidates))
	copy(sorted, candidates)
	sort.SliceStable(sorted, func(i, j int) bool {
		return metric(sorted[i]) > metric(sorted[j])
	})
	return sorted[n:]
}

// protectByMaxConditional is protectByMax restricted to peers for
// which the conditional predicate returns true. Peers that fail
// the predicate are NOT protected by this round but stay in the
// candidate pool for subsequent rounds. Used for "newest tx among
// tx-relayers" and "newest block among block-relay-only with
// services" passes (eviction.cpp:194 and :196).
func protectByMaxConditional(candidates []*Peer, n int, metric func(*Peer) int64, cond func(*Peer) bool) []*Peer {
	if n <= 0 {
		return candidates
	}
	// Partition: indices that pass cond go into eligible; the
	// others go into excluded. Sort eligible by metric, take the
	// top n. Survivors = excluded + (eligible minus top n).
	eligible := make([]*Peer, 0, len(candidates))
	excluded := make([]*Peer, 0, len(candidates))
	for _, p := range candidates {
		if cond(p) {
			eligible = append(eligible, p)
		} else {
			excluded = append(excluded, p)
		}
	}
	if n >= len(eligible) {
		return excluded
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return metric(eligible[i]) > metric(eligible[j])
	})
	survivors := make([]*Peer, 0, len(excluded)+len(eligible)-n)
	survivors = append(survivors, excluded...)
	survivors = append(survivors, eligible[n:]...)
	return survivors
}

// protectByNetGroupKeyed preserves up to n peers chosen for
// network-group diversity. The simplification vs Bitcoin Core's
// keyed-hash approach (src/node/eviction.cpp:188 with
// CompareNetGroupKeyed) is to pick the n peers from the most
// distinct groups; ties broken by youngest connection (so older
// peers in the same group lose out to a younger peer in a
// previously-unrepresented group).
//
// The intent matches Core: an attacker can't fill our inbound
// pool from a single network group and starve eviction of
// "diverse" candidates to keep.
func protectByNetGroupKeyed(candidates []*Peer, n int) []*Peer {
	if n <= 0 {
		return candidates
	}
	if n >= len(candidates) {
		return candidates[:0]
	}
	// Sort by (group-frequency-ascending, connection-age-ascending):
	// peers in rare groups sort first, so the head of the list is
	// the most diverse subset. Then peek the first n as protected.
	freq := map[string]int{}
	for _, p := range candidates {
		freq[string(p.NetworkGroup())]++
	}
	sorted := make([]*Peer, len(candidates))
	copy(sorted, candidates)
	sort.SliceStable(sorted, func(i, j int) bool {
		fi := freq[string(sorted[i].NetworkGroup())]
		fj := freq[string(sorted[j].NetworkGroup())]
		if fi != fj {
			return fi < fj
		}
		return sorted[i].Created() < sorted[j].Created()
	})
	return sorted[n:]
}

// protectByRatio mirrors Bitcoin Core's
// ProtectEvictionCandidatesByRatio (src/node/eviction.cpp:105-176):
// reserve ~50% of the candidate pool for protection by uptime,
// with up to 25% set aside for under-represented privacy networks
// (Tor / I2P / CJDNS / localhost — currently unused in Parallax;
// the plumbing is here for future).
//
// Today's implementation: protect the oldest 50% by Connection
// Age, dropping them from the candidate pool. The privacy-network
// reservation is a no-op until Parallax speaks those networks; the
// peers slice has no tag for them yet.
func protectByRatio(candidates []*Peer) []*Peer {
	half := len(candidates) / 2
	if half <= 0 {
		return candidates
	}
	sorted := make([]*Peer, len(candidates))
	copy(sorted, candidates)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Created() < sorted[j].Created()
	})
	// Drop the oldest `half` — they are protected.
	return sorted[half:]
}

// pickEvictionVictim is the final step after all protection rounds.
// Mirrors src/node/eviction.cpp:217-239:
//   - group survivors by network group;
//   - find the most-populated group (ties → group with youngest
//     representative connection);
//   - within that group, evict the youngest peer.
//
// Returns nil if candidates is empty (caller's no-op signal).
func pickEvictionVictim(candidates []*Peer) *Peer {
	if len(candidates) == 0 {
		return nil
	}
	groups := map[string][]*Peer{}
	for _, p := range candidates {
		key := string(p.NetworkGroup())
		groups[key] = append(groups[key], p)
	}
	var bestGroup []*Peer
	bestCount := 0
	var bestYoungest mclock.AbsTime
	for _, members := range groups {
		count := len(members)
		// Find youngest in this group.
		youngest := members[0].Created()
		for _, m := range members {
			if m.Created() > youngest {
				youngest = m.Created()
			}
		}
		switch {
		case count > bestCount:
			bestGroup = members
			bestCount = count
			bestYoungest = youngest
		case count == bestCount && youngest > bestYoungest:
			bestGroup = members
			bestYoungest = youngest
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
	candidates := evictionCandidates(peers, mclock.Now())
	if len(candidates) == 0 {
		return false
	}

	// Round 1: preserve network-group diversity.
	candidates = protectByNetGroupKeyed(candidates, evictProtectNetGroup)
	// Round 2: preserve fastest pings (lowest minPing).
	candidates = protectByMin(candidates, evictProtectFastestPing,
		func(p *Peer) int64 { return p.minPing.Load() })
	// Round 3: preserve newest tx-relay among tx-relayers.
	candidates = protectByMaxConditional(candidates, evictProtectNewestTx,
		func(p *Peer) int64 { return p.lastTxRx.Load() },
		func(p *Peer) bool { return p.relayTxs.Load() })
	// Round 4: preserve newest blocks among block-relay-only with
	// the NodeNetwork service flag (mirrors Core's "block-relay
	// peers with services" round). For now, just gate on
	// !relayTxs since service-flag plumbing happens in phase 4.
	candidates = protectByMaxConditional(candidates, evictProtectBlockRelay,
		func(p *Peer) int64 { return p.lastBlockRx.Load() },
		func(p *Peer) bool { return !p.relayTxs.Load() })
	// Round 5: preserve newest block announces overall.
	candidates = protectByMax(candidates, evictProtectNewestBlock,
		func(p *Peer) int64 { return p.lastBlockRx.Load() })
	// Round 6: preserve oldest 50%.
	candidates = protectByRatio(candidates)

	victim := pickEvictionVictim(candidates)
	if victim == nil {
		return false
	}
	victim.Disconnect(DiscTooManyPeers)
	return true
}
