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
	"bytes"
	"sync"
	"time"
)

// QuorumThreshold is the minimum number of distinct address-group peers
// that must agree on a reported external address before it becomes our
// self-advertised address. Fixed at 3 per PIP-0006 Phase 4 — raising it
// costs propagation latency, lowering it weakens sybil resistance.
const QuorumThreshold = 3

// QuorumEvictAfter caps how long a peer's YourAddr report stays in the
// tally. Reports are refreshed on re-connect, so anything older than
// this is a disconnected peer that shouldn't count.
const QuorumEvictAfter = 3 * time.Hour

// PeerKey uniquely identifies a peer within the quorum tally. We want
// per-peer reports (one peer, one vote) but across sessions a single
// peer might reconnect — using the RemoteAddr string is the simplest
// stable identifier during the lifetime of a session. Replaced on
// reconnect.
type PeerKey string

// reportedAddr is a (NetID, Addr bytes, port) triple. Stored as the
// service-key byte form so it's usable as a map key.
type reportedAddr string

// makeReportedAddr packs (net, addr, port) into the reportedAddr form.
func makeReportedAddr(net uint8, addr []byte, port uint16) reportedAddr {
	b := make([]byte, 0, 1+len(addr)+2)
	b = append(b, net)
	b = append(b, addr...)
	b = append(b, byte(port>>8), byte(port))
	return reportedAddr(b)
}

// unpackReportedAddr returns the components of a reportedAddr key.
// Length validation is done before packing so this cannot fail on a
// key that round-tripped through addRule/remove.
func unpackReportedAddr(r reportedAddr) (net uint8, addr []byte, port uint16) {
	b := []byte(r)
	if len(b) < 3 {
		return 0, nil, 0
	}
	net = b[0]
	addr = b[1 : len(b)-2]
	port = uint16(b[len(b)-2])<<8 | uint16(b[len(b)-1])
	return
}

// Quorum tracks peer reports about our external address. Threadsafe.
//
// Reports are stored per (address, peer-key); quorum is reached when
// reports from ≥3 distinct address groups back the same address. The
// distinct-group check is what gives sybil resistance — a small
// colluding set in one /16 cannot outvote a single honest peer from
// another network.
//
// Not persisted: on restart we wait for fresh reports to re-establish
// quorum. That's the plan's design, and it's what keeps an attacker
// who briefly controlled our peers from carrying a bogus advertisement
// across reboots.
type Quorum struct {
	mu sync.Mutex

	// reports[addr][peerKey] = (group, receivedAt).
	reports map[reportedAddr]map[PeerKey]reportEntry

	// overrideAddr is set when the operator configured --nat extip:<IP>
	// or UPnP/PMP resolved one. Short-circuits quorum entirely.
	overrideAddr    reportedAddr
	overrideWinning bool
}

type reportEntry struct {
	group      []byte
	receivedAt time.Time
}

// NewQuorum returns an empty tally.
func NewQuorum() *Quorum {
	return &Quorum{reports: make(map[reportedAddr]map[PeerKey]reportEntry)}
}

// SetOverride configures an operator-supplied external address. All
// subsequent Winner() calls return this address regardless of tally.
// Pass empty (net==0) to clear.
func (q *Quorum) SetOverride(net uint8, addr []byte, port uint16) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if net == 0 || len(addr) == 0 {
		q.overrideAddr = ""
		q.overrideWinning = false
		return
	}
	q.overrideAddr = makeReportedAddr(net, addr, port)
	q.overrideWinning = true
}

// Report records a peer's YourAddr claim. group is the peer's network
// group (passed in by the caller so Quorum doesn't depend on addrman's
// grouping primitive). Returns the address and ok=true if this report
// caused quorum to be reached.
func (q *Quorum) Report(peerKey PeerKey, net uint8, addr []byte, port uint16, group []byte) (winningNet uint8, winningAddr []byte, winningPort uint16, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.evictStaleLocked(time.Now())

	key := makeReportedAddr(net, addr, port)
	byPeer, exists := q.reports[key]
	if !exists {
		byPeer = make(map[PeerKey]reportEntry)
		q.reports[key] = byPeer
	}
	byPeer[peerKey] = reportEntry{group: append([]byte(nil), group...), receivedAt: time.Now()}

	return q.winnerLocked(key)
}

// Disconnect removes all of peerKey's reports. Called on session close.
func (q *Quorum) Disconnect(peerKey PeerKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for key, byPeer := range q.reports {
		delete(byPeer, peerKey)
		if len(byPeer) == 0 {
			delete(q.reports, key)
		}
	}
}

// Winner returns the currently-agreed-upon external address, or ok=false
// if no address has quorum. An operator override is always returned
// first.
func (q *Quorum) Winner() (net uint8, addr []byte, port uint16, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.evictStaleLocked(time.Now())

	if q.overrideWinning {
		n, a, p := unpackReportedAddr(q.overrideAddr)
		return n, append([]byte(nil), a...), p, true
	}

	// Check every tallied address and return the first with quorum.
	// A well-connected node will typically have exactly one address at
	// quorum; map-iteration order is fine for tie-breaking.
	for key := range q.reports {
		n, a, p, winOK := q.winnerLocked(key)
		if winOK {
			return n, a, p, true
		}
	}
	return 0, nil, 0, false
}

// winnerLocked checks whether the specified address has quorum.
func (q *Quorum) winnerLocked(key reportedAddr) (uint8, []byte, uint16, bool) {
	if q.overrideWinning {
		n, a, p := unpackReportedAddr(q.overrideAddr)
		return n, append([]byte(nil), a...), p, true
	}
	byPeer, ok := q.reports[key]
	if !ok {
		return 0, nil, 0, false
	}
	// Count distinct groups. O(N) over reporters — small N.
	var groups [][]byte
outer:
	for _, entry := range byPeer {
		if len(entry.group) == 0 {
			// Skip malformed groups. The plan calls out
			// "addresses with group = 0 or unparseable groups
			// never contribute to quorum".
			continue
		}
		for _, existing := range groups {
			if bytes.Equal(existing, entry.group) {
				continue outer
			}
		}
		groups = append(groups, entry.group)
		if len(groups) >= QuorumThreshold {
			break
		}
	}
	if len(groups) < QuorumThreshold {
		return 0, nil, 0, false
	}
	n, a, p := unpackReportedAddr(key)
	return n, append([]byte(nil), a...), p, true
}

// evictStaleLocked removes reports older than QuorumEvictAfter. O(N).
// Called opportunistically — quorum state is small enough that a full
// sweep on every Report/Winner is fine.
func (q *Quorum) evictStaleLocked(now time.Time) {
	for key, byPeer := range q.reports {
		for pk, entry := range byPeer {
			if now.Sub(entry.receivedAt) > QuorumEvictAfter {
				delete(byPeer, pk)
			}
		}
		if len(byPeer) == 0 {
			delete(q.reports, key)
		}
	}
}

// Stats returns the current tally for operator diagnostics. Dumps (addr,
// distinct-group count). Used by `parallax-cli addrbook status` in
// Phase 6.
func (q *Quorum) Stats() []QuorumStat {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.evictStaleLocked(time.Now())

	out := make([]QuorumStat, 0, len(q.reports))
	for key, byPeer := range q.reports {
		n, a, p := unpackReportedAddr(key)
		seen := make(map[string]struct{}, len(byPeer))
		for _, entry := range byPeer {
			seen[string(entry.group)] = struct{}{}
		}
		out = append(out, QuorumStat{
			NetworkID:     n,
			Addr:          append([]byte(nil), a...),
			TCPPort:       p,
			Reporters:     len(byPeer),
			DistinctGroup: len(seen),
		})
	}
	return out
}

// QuorumStat is one row of Stats output. Stable shape — consumed by
// operator tooling.
type QuorumStat struct {
	NetworkID     uint8
	Addr          []byte
	TCPPort       uint16
	Reporters     int
	DistinctGroup int
}
