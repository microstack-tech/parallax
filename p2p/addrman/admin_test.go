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

func TestRemoveNewEntry(t *testing.T) {
	m := newTestMan(t)
	src := addr4(2, 3, 4, 5, 30303)
	addr := addr4(9, 9, 9, 9, 30303)
	m.AddOne(addr, 0, nil, time.Now(), src, SourceTCPGossip, 0)
	if m.Size(nil, nil) != 1 {
		t.Fatalf("setup: size = %d", m.Size(nil, nil))
	}
	if !m.Remove(addr) {
		t.Fatal("Remove returned false")
	}
	if m.Size(nil, nil) != 0 {
		t.Fatalf("post-Remove size = %d, want 0", m.Size(nil, nil))
	}
	if _, ok := m.FindAddressPosition(addr); ok {
		t.Fatal("Lookup still finds the removed entry")
	}
}

func TestRemoveTriedEntry(t *testing.T) {
	m := newTestMan(t)
	src := addr4(2, 3, 4, 5, 30303)
	addr := addr4(9, 9, 9, 9, 30303)
	m.AddOne(addr, 0, nil, time.Now(), src, SourceTCPGossip, 0)
	m.Good(addr, time.Now())
	if pos, ok := m.FindAddressPosition(addr); !ok || !pos.Tried {
		t.Fatal("setup: entry not in tried")
	}
	if !m.Remove(addr) {
		t.Fatal("Remove returned false")
	}
	if m.Size(nil, nil) != 0 {
		t.Fatalf("post-Remove size = %d, want 0", m.Size(nil, nil))
	}
}

func TestRemoveUnknownReturnsFalse(t *testing.T) {
	m := newTestMan(t)
	if m.Remove(addr4(1, 2, 3, 4, 30303)) {
		t.Fatal("Remove on unknown addr returned true")
	}
}

func TestResetKeyOnDeterministicRefuses(t *testing.T) {
	m := newTestMan(t)
	if err := m.ResetKey(); err == nil {
		t.Fatal("ResetKey should refuse on deterministic instance")
	}
}

func TestResetKeyNonDeterministic(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	src := addr4(2, 3, 4, 5, 30303)
	for i := range 30 {
		addr := addr4(byte(0x80|i), byte(i), 1, 2, 30303)
		m.AddOne(addr, 0, nil, time.Now(), src, SourceTCPGossip, 0)
	}
	// Promote half to tried.
	for i := range 15 {
		addr := addr4(byte(0x80|i), byte(i), 1, 2, 30303)
		m.Good(addr, time.Now())
	}
	before := m.nKey
	if err := m.ResetKey(); err != nil {
		t.Fatalf("ResetKey: %v", err)
	}
	if m.nKey == before {
		t.Error("nKey did not change")
	}
	// tried must be cleared (entries moved to new or dropped).
	if m.Size(nil, new(bool)) != 0 {
		// Pointer-to-false means in_new=false, i.e., tried count.
		// Actually ambiguous — let me use explicit vars.
	}
	triedFlag := false
	if got := m.Size(nil, &triedFlag); got != 0 {
		t.Errorf("tried count after ResetKey = %d, want 0", got)
	}
}

func TestSnapshotShape(t *testing.T) {
	m := newTestMan(t)
	src := addr4(2, 3, 4, 5, 30303)
	addr := addr4(9, 9, 9, 9, 30303)
	m.AddOne(addr, 0, nil, time.Now(), src, SourceTCPGossip, 0)
	s := m.Snapshot()
	if s.Total != 1 || s.New != 1 || s.Tried != 0 {
		t.Errorf("Snapshot counts wrong: %+v", s)
	}
	if s.PerSource["tcp_gossip"] != 1 {
		t.Errorf("PerSource[tcp_gossip] = %d, want 1", s.PerSource["tcp_gossip"])
	}
}
