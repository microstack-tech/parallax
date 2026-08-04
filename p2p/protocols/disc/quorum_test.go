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
	"testing"
	"time"
)

// TestQuorumReachedAtThreshold — three reports from distinct groups
// activate quorum; fewer don't.
func TestQuorumReachedAtThreshold(t *testing.T) {
	q := NewQuorum()
	addr := []byte{203, 0, 113, 42}

	// Two reports from same group — no quorum.
	q.Report("peer-1", NetIPv4, addr, 30303, []byte{NetIPv4, 8, 8})
	q.Report("peer-2", NetIPv4, addr, 30303, []byte{NetIPv4, 8, 8})
	if _, _, _, ok := q.Winner(); ok {
		t.Error("quorum reached with only one distinct group")
	}

	// Third peer from a new group — still not enough (only 2 distinct).
	q.Report("peer-3", NetIPv4, addr, 30303, []byte{NetIPv4, 1, 1})
	if _, _, _, ok := q.Winner(); ok {
		t.Error("quorum reached with two distinct groups (threshold=3)")
	}

	// Fourth peer from a third group — quorum.
	q.Report("peer-4", NetIPv4, addr, 30303, []byte{NetIPv4, 9, 9})
	net, a, p, ok := q.Winner()
	if !ok {
		t.Fatal("quorum not reached at 3 distinct groups")
	}
	if net != NetIPv4 || string(a) != string(addr) || p != 30303 {
		t.Errorf("wrong winner: got (%d, %x, %d)", net, a, p)
	}
}

// TestQuorumRejectsGroupZero — empty/malformed groups must not count.
func TestQuorumRejectsGroupZero(t *testing.T) {
	q := NewQuorum()
	addr := []byte{1, 2, 3, 4}
	// Three reports with empty group — should never reach quorum.
	for i := range 5 {
		q.Report(PeerKey(string(rune('a'+i))), NetIPv4, addr, 30303, nil)
	}
	if _, _, _, ok := q.Winner(); ok {
		t.Error("empty-group reports contributed to quorum")
	}
}

// TestQuorumDistinctGroupMonotonic — adding reports within the same
// peer's session never decreases distinct-group count (invariant the
// handler depends on for stability under flapping peers).
func TestQuorumDistinctGroupMonotonic(t *testing.T) {
	q := NewQuorum()
	addr := []byte{1, 2, 3, 4}

	groups := [][]byte{
		{NetIPv4, 1, 1},
		{NetIPv4, 2, 2},
		{NetIPv4, 3, 3},
		{NetIPv4, 4, 4},
	}
	winners := 0
	for i, g := range groups {
		q.Report(PeerKey(string(rune('a'+i))), NetIPv4, addr, 30303, g)
		if _, _, _, ok := q.Winner(); ok {
			winners++
		}
	}
	// Quorum must activate at ≥3 and stay active.
	if winners < 2 {
		t.Errorf("winners should stay set after quorum reached; got %d transitions", winners)
	}
}

// TestQuorumOverrideShortCircuits — SetOverride always wins.
func TestQuorumOverrideShortCircuits(t *testing.T) {
	q := NewQuorum()
	// Empty tally — normally no winner.
	q.SetOverride(NetIPv4, []byte{10, 20, 30, 40}, 30303)
	net, a, p, ok := q.Winner()
	if !ok {
		t.Fatal("override should always win")
	}
	if net != NetIPv4 || p != 30303 || a[0] != 10 {
		t.Errorf("wrong override winner: (%d, %x, %d)", net, a, p)
	}

	// Clear override — tally now rules (empty, no winner).
	q.SetOverride(0, nil, 0)
	if _, _, _, ok := q.Winner(); ok {
		t.Error("Winner still ok after clearing override")
	}
}

// TestQuorumDisconnectRemovesVotes — peer reports are purged on
// disconnect, and previously-reached quorum can drop back below the
// threshold.
func TestQuorumDisconnectRemovesVotes(t *testing.T) {
	q := NewQuorum()
	addr := []byte{1, 2, 3, 4}
	q.Report("p1", NetIPv4, addr, 30303, []byte{NetIPv4, 1, 1})
	q.Report("p2", NetIPv4, addr, 30303, []byte{NetIPv4, 2, 2})
	q.Report("p3", NetIPv4, addr, 30303, []byte{NetIPv4, 3, 3})
	if _, _, _, ok := q.Winner(); !ok {
		t.Fatal("quorum not reached initially")
	}
	q.Disconnect("p3")
	if _, _, _, ok := q.Winner(); ok {
		t.Error("quorum still ok after disconnecting one of three groups")
	}
}

// TestQuorumRefreshDropsDisconnectedPeers — Refresh's
// connected-peer reconciliation drops reports from peers absent
// from the supplied connected set, even when their receivedAt is
// fresh. The "and on peer churn" half of PIP-0006 §Phase 4's
// quorum re-eval, here exercised as the periodic backstop in case
// Disconnect() didn't fire.
func TestQuorumRefreshDropsDisconnectedPeers(t *testing.T) {
	q := NewQuorum()
	addr := []byte{198, 51, 100, 7}

	q.Report("p1", NetIPv4, addr, 30303, []byte{NetIPv4, 1, 1})
	q.Report("p2", NetIPv4, addr, 30303, []byte{NetIPv4, 2, 2})
	q.Report("p3", NetIPv4, addr, 30303, []byte{NetIPv4, 3, 3})
	if _, _, _, ok := q.Winner(); !ok {
		t.Fatal("quorum not reached initially")
	}

	// p3's session vanished (handler.Run returned without firing
	// PeerDisconnected). Refresh with the truly-connected set must
	// drop the orphaned report and quorum must follow.
	connected := map[PeerKey]struct{}{"p1": {}, "p2": {}}
	dropped := q.Refresh(time.Now(), func() map[PeerKey]struct{} { return connected })
	if dropped != 1 {
		t.Errorf("Refresh dropped %d, want 1", dropped)
	}
	if _, _, _, ok := q.Winner(); ok {
		t.Error("quorum still ok after Refresh dropped a contributing peer")
	}
}

// TestQuorumRefreshNilConnectedKeepsReports — passing nil for the
// connected set is the "skip connected-set reconciliation" mode
// (used by tests that exercise only the time-eviction path). Reports
// must remain intact.
func TestQuorumRefreshNilConnectedKeepsReports(t *testing.T) {
	q := NewQuorum()
	addr := []byte{198, 51, 100, 8}
	q.Report("p1", NetIPv4, addr, 30303, []byte{NetIPv4, 1, 1})
	q.Report("p2", NetIPv4, addr, 30303, []byte{NetIPv4, 2, 2})
	q.Report("p3", NetIPv4, addr, 30303, []byte{NetIPv4, 3, 3})

	if dropped := q.Refresh(time.Now(), nil); dropped != 0 {
		t.Errorf("nil connected set dropped %d reports, want 0", dropped)
	}
	if _, _, _, ok := q.Winner(); !ok {
		t.Error("quorum lost after Refresh(nil)")
	}
}

// TestQuorumAgedReportSurvivesWhileConnected — DEFECT 1 regression.
// YourAddr is single-shot per session, so a long-lived peer's report
// ages past QuorumEvictAfter while the peer is still connected. The age
// sweep must NOT drop such a report (doing so silently collapses quorum
// on a stable topology); it may only reap the stale report of a peer
// that is genuinely gone from the connected set.
func TestQuorumAgedReportSurvivesWhileConnected(t *testing.T) {
	q := NewQuorum()
	addr := []byte{198, 51, 100, 9}

	q.Report("p1", NetIPv4, addr, 30303, []byte{NetIPv4, 1, 1})
	q.Report("p2", NetIPv4, addr, 30303, []byte{NetIPv4, 2, 2})
	q.Report("p3", NetIPv4, addr, 30303, []byte{NetIPv4, 3, 3})

	// Age every report well past the cap — the steady state on a stable
	// peer set, where no reconnect ever refreshes receivedAt.
	q.mu.Lock()
	for _, byPeer := range q.reports {
		for pk, entry := range byPeer {
			entry.receivedAt = time.Now().Add(-QuorumEvictAfter - time.Minute)
			byPeer[pk] = entry
		}
	}
	q.mu.Unlock()

	// All three peers are still connected: their aged votes must survive.
	all := map[PeerKey]struct{}{"p1": {}, "p2": {}, "p3": {}}
	q.Refresh(time.Now(), func() map[PeerKey]struct{} { return all })
	if _, _, _, ok := q.Winner(); !ok {
		t.Fatal("aged reports from still-connected peers were wrongly evicted")
	}

	// p3's session vanished without firing Disconnect. Now the age sweep
	// — driven by the reconciled connected set — reaps only that absent
	// peer's stale vote, dropping quorum below threshold.
	live := map[PeerKey]struct{}{"p1": {}, "p2": {}}
	q.Refresh(time.Now(), func() map[PeerKey]struct{} { return live })
	if _, _, _, ok := q.Winner(); ok {
		t.Error("quorum still ok after the gone peer's stale vote was reaped")
	}
}

// TestQuorumRefreshSnapshotLazyKeepsFreshVote — DEFECT 2 regression.
// The connected snapshot must be evaluated lazily, under the report
// lock, inside Refresh. A peer that completes Hello+YourAddr in the
// window between an eager snapshot and the reconciliation is present in
// the tally but absent from a stale eager snapshot, so it would be
// deleted — and its single-shot vote lost for the whole session. This
// test contrasts the two evaluation strategies over identical tallies.
func TestQuorumRefreshSnapshotLazyKeepsFreshVote(t *testing.T) {
	addr := []byte{198, 51, 100, 12}
	build := func() *Quorum {
		q := NewQuorum()
		q.Report("p1", NetIPv4, addr, 30303, []byte{NetIPv4, 1, 1})
		q.Report("p2", NetIPv4, addr, 30303, []byte{NetIPv4, 2, 2})
		// p3 is the racing peer: its YourAddr has already committed.
		q.Report("p3", NetIPv4, addr, 30303, []byte{NetIPv4, 3, 3})
		return q
	}

	// Hazard: an eager snapshot captured before p3 connected omits p3;
	// feeding it drops p3's committed vote and collapses quorum. This
	// documents exactly the failure the lazy snapshot avoids.
	qBug := build()
	stale := map[PeerKey]struct{}{"p1": {}, "p2": {}}
	qBug.Refresh(time.Now(), func() map[PeerKey]struct{} { return stale })
	if _, _, _, ok := qBug.Winner(); ok {
		t.Fatal("setup invariant: a stale snapshot missing p3 should drop its vote")
	}

	// Fixed: the snapshot is evaluated at reconciliation time, when p3's
	// Report (and, in production, the Hello that precedes it) is already
	// visible, so p3 is in the live set and its vote survives.
	qFix := build()
	qFix.Refresh(time.Now(), func() map[PeerKey]struct{} {
		return map[PeerKey]struct{}{"p1": {}, "p2": {}, "p3": {}}
	})
	if _, _, _, ok := qFix.Winner(); !ok {
		t.Fatal("fresh vote dropped despite the peer being present at reconcile time")
	}
}

// FuzzQuorumReports streams arbitrary (peerKey, net, addr, port, group)
// reports and asserts: no panic; distinct-group count is consistent
// (all Winner-true addresses have ≥3 distinct non-empty groups); no
// partial state leaks.
func FuzzQuorumReports(f *testing.F) {
	f.Add(NetIPv4, []byte{1, 2, 3, 4}, uint16(30303), "peer-1", []byte{NetIPv4, 1, 1})
	f.Add(NetIPv4, []byte{1, 2, 3, 4}, uint16(30303), "peer-2", []byte{NetIPv4, 2, 2})
	f.Add(NetIPv4, []byte{1, 2, 3, 4}, uint16(30303), "peer-3", []byte{NetIPv4, 3, 3})

	q := NewQuorum()
	f.Fuzz(func(t *testing.T, net uint8, addr []byte, port uint16, peerKey string, group []byte) {
		if len(addr) > 64 {
			addr = addr[:64]
		}
		if len(group) > 8 {
			group = group[:8]
		}
		q.Report(PeerKey(peerKey), net, addr, port, group)
		_, _, _, _ = q.Winner()
		q.Stats()
		// Sanity invariant: override takes precedence over tally.
		q.SetOverride(NetIPv4, []byte{1, 1, 1, 1}, 9999)
		if _, a, p, ok := q.Winner(); !ok || p != 9999 || len(a) != 4 {
			t.Errorf("override broken: ok=%v addr=%x port=%d", ok, a, p)
		}
		q.SetOverride(0, nil, 0)
	})
}
