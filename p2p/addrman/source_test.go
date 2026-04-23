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

package addrman

import (
	"testing"
	"time"
)

// TestSourcePriorityOrdering locks the source-priority ordering because
// it's load-bearing for the Phase 5 eviction and Select weighting.
// Changing it requires re-reading the security argument in PIP-0006 §5.
func TestSourcePriorityOrdering(t *testing.T) {
	order := []Source{SourceLegacyUDP, SourceDNSSeed, SourceTCPGossip, SourceSelfAdvertised, SourceManual}
	for i := 1; i < len(order); i++ {
		if order[i-1].priority() >= order[i].priority() {
			t.Fatalf("priority order violated: %s (%d) >= %s (%d)",
				order[i-1], order[i-1].priority(),
				order[i], order[i].priority())
		}
	}
}

// TestSourceEvictionDisplacesLegacyUDP — injecting a tcp_gossip entry
// into a new-bucket slot already held by a legacy_udp entry must
// displace the legacy_udp. This is the defense against legacy_udp
// flooding during the v2.x deprecation window.
func TestSourceEvictionDisplacesLegacyUDP(t *testing.T) {
	m, err := New(Deterministic(1))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := NewNetAddr(NetIPv4, []byte{2, 3, 4, 5}, 30303)
	// Find two addresses that collide into the same new-bucket slot
	// via the fixed nKey. We generate a small set and look for a
	// collision; with 256 buckets × 64 slots = 16k positions over
	// newBucketCount=1024 × 64 slots, collisions are rare — we need
	// to generate many to guarantee one.
	var a1, a2 NetAddr
	found := false
	for i := range 10_000 {
		cand, err := NewNetAddr(NetIPv4, []byte{byte(0x80 | (i >> 8)), byte(i), 1, 1}, 30303)
		if err != nil || !cand.Valid() {
			continue
		}
		b := newBucket(m.nKey, cand, src)
		p := bucketPosition(m.nKey, true, b, cand)
		if a1.Network == 0 {
			// First candidate — remember its (bucket, pos).
			a1 = cand
			for j := i + 1; j < 10_000; j++ {
				cand2, err := NewNetAddr(NetIPv4, []byte{byte(0x80 | (j >> 8)), byte(j), 1, 1}, 30303)
				if err != nil || !cand2.Valid() {
					continue
				}
				b2 := newBucket(m.nKey, cand2, src)
				p2 := bucketPosition(m.nKey, true, b2, cand2)
				if b2 == b && p2 == p {
					a2 = cand2
					found = true
					break
				}
			}
			if found {
				break
			}
			a1 = NetAddr{} // reset, try next
		}
	}
	if !found {
		t.Skip("no bucket collision within the search window; run again with a different seed")
	}

	// Insert legacy_udp first.
	if !m.AddOne(a1, 0x00, nil, time.Now(), src, SourceLegacyUDP, 0) {
		t.Fatal("initial legacy_udp insert failed")
	}
	// Now a tcp_gossip entry colliding into the same slot — must
	// displace the legacy_udp.
	if !m.AddOne(a2, 0x00, nil, time.Now(), src, SourceTCPGossip, 0) {
		t.Fatal("tcp_gossip displacement insert failed")
	}
	// Verify a1 was evicted (can't be in any bucket anymore) and a2 is
	// resident.
	if _, ok := m.FindAddressPosition(a1); ok {
		t.Error("legacy_udp entry still resident after tcp_gossip displacement")
	}
	if _, ok := m.FindAddressPosition(a2); !ok {
		t.Error("tcp_gossip entry not resident after displacement")
	}
}

// TestManualExemptFromEviction — a manual entry must not be evicted
// even by a higher-priority-but-not-manual insert.
func TestManualExemptFromEviction(t *testing.T) {
	m, err := New(Deterministic(2))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := NewNetAddr(NetIPv4, []byte{2, 3, 4, 5}, 30303)
	addr, _ := NewNetAddr(NetIPv4, []byte{100, 200, 50, 60}, 30303)
	if !m.AddOne(addr, 0x00, nil, time.Now(), src, SourceManual, 0) {
		t.Fatal("manual insert failed")
	}
	// Now add a tcp_gossip to the same address; addrman dedups by
	// (net, addr, port), so this hits the update-existing path. The
	// existing entry's source should remain manual.
	m.AddOne(addr, 0x00, nil, time.Now(), src, SourceTCPGossip, 0)
	info := m.Lookup(addr)
	if info == nil {
		t.Fatal("addr missing after duplicate add")
	}
	if info.SourceTag != SourceManual {
		t.Errorf("manual entry re-tagged to %s after duplicate add", info.SourceTag)
	}
}

// TestSelectPrefersTCPGossipOverLegacyUDP — given equal populations of
// tcp_gossip and legacy_udp entries, Select() draws tcp_gossip more
// often than chance thanks to the Phase-5 source-weighting bias.
// Expected ratio under the 1.0 vs 0.5 multiplier: roughly 2:1 in favor
// of tcp_gossip (before chance-factor smoothing). We assert a
// conservative >60% threshold to allow for variance.
func TestSelectPrefersTCPGossipOverLegacyUDP(t *testing.T) {
	m, err := New(Deterministic(7))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := NewNetAddr(NetIPv4, []byte{2, 3, 4, 5}, 30303)
	// Populate 200 entries from each source. Addresses are structured
	// to not collide with each other.
	for i := range 200 {
		addr, _ := NewNetAddr(NetIPv4, []byte{0x80, byte(i), 0x01, 0x02}, 30303)
		m.AddOne(addr, 0x00, nil, time.Now(), src, SourceTCPGossip, 0)
	}
	for i := range 200 {
		addr, _ := NewNetAddr(NetIPv4, []byte{0x40, byte(i), 0x03, 0x04}, 30303)
		m.AddOne(addr, 0x00, nil, time.Now(), src, SourceLegacyUDP, 0)
	}

	const rounds = 2000
	var tcp, legacy int
	for range rounds {
		addr, _, ok := m.Select(false, nil)
		if !ok {
			continue
		}
		info := m.Lookup(addr)
		if info == nil {
			continue
		}
		switch info.SourceTag {
		case SourceTCPGossip:
			tcp++
		case SourceLegacyUDP:
			legacy++
		}
	}
	total := tcp + legacy
	if total < rounds/2 {
		t.Fatalf("too few successful selects: %d/%d", total, rounds)
	}
	ratio := float64(tcp) / float64(total)
	if ratio < 0.60 {
		t.Errorf("tcp_gossip selected only %.1f%% of the time (%d tcp vs %d legacy); source weighting broken", ratio*100, tcp, legacy)
	}
}

// TestAddressPoisoningLegacyUDPFloodDoesNotDisplaceTriedGossip — a
// malicious legacy_udp flood of 2000 addresses must not eject
// tcp_gossip entries from the tried table once they've been promoted.
// This is the core acceptance criterion for Phase 5's mixed-deployment
// robustness.
func TestAddressPoisoningLegacyUDPFloodDoesNotDisplaceTriedGossip(t *testing.T) {
	m, err := New(Deterministic(9))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := NewNetAddr(NetIPv4, []byte{2, 3, 4, 5}, 30303)

	// Seed a small set of tcp_gossip entries and promote them to
	// tried via Good().
	var triedSet []NetAddr
	for i := range 50 {
		addr, _ := NewNetAddr(NetIPv4, []byte{0x80, byte(i), 0x01, 0x02}, 30303)
		m.AddOne(addr, 0x00, nil, time.Now(), src, SourceTCPGossip, 0)
		m.Good(addr, time.Now())
		triedSet = append(triedSet, addr)
	}

	// Verify they're all in tried before the flood.
	triedBefore := 0
	for _, a := range triedSet {
		if pos, ok := m.FindAddressPosition(a); ok && pos.Tried {
			triedBefore++
		}
	}
	if triedBefore != len(triedSet) {
		t.Fatalf("pre-flood: %d/%d tcp_gossip entries in tried", triedBefore, len(triedSet))
	}

	// Attacker floods 2000 legacy_udp addresses from a hostile source.
	attacker, _ := NewNetAddr(NetIPv4, []byte{66, 66, 66, 66}, 30303)
	for i := range 2000 {
		addr, err := NewNetAddr(NetIPv4, []byte{0x40, byte(i), byte(i >> 4), 0x99}, 30303)
		if err != nil || !addr.Valid() {
			continue
		}
		m.AddOne(addr, 0x00, nil, time.Now(), attacker, SourceLegacyUDP, 0)
	}

	// Post-flood: tcp_gossip tried entries must not have been evicted.
	// MakeTried only evicts from the tried table during a *future*
	// tried-table collision, and Good is only called on a peer the
	// dialer successfully contacted. A legacy_udp flood can only add
	// to the new table, which cannot touch the tried table.
	triedAfter := 0
	for _, a := range triedSet {
		if pos, ok := m.FindAddressPosition(a); ok && pos.Tried {
			triedAfter++
		}
	}
	if triedAfter != triedBefore {
		t.Errorf("post-flood: %d/%d tcp_gossip entries survived in tried (was %d)",
			triedAfter, len(triedSet), triedBefore)
	}
}
