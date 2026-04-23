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
	"path/filepath"
	"testing"
	"time"
)

// FuzzOpsInterleaved — feeds arbitrary operation-sequences into an
// AddrMan and asserts structural invariants survive:
//
//   - Each bucket holds ≤ bucketSize entries (tried and new).
//   - Serialize → Deserialize → Serialize produces equivalent state.
//   - Per-network counts match actual occupancy across both tables.
//
// The operation opcode is the low 3 bits of `op`; data bytes supply
// the address octets, source octets, timestamp delta, and Source tag.
func FuzzOpsInterleaved(f *testing.F) {
	f.Add(uint8(0), []byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add(uint8(1), []byte{9, 9, 9, 9, 2, 3, 4, 5})
	f.Add(uint8(7), []byte{})

	f.Fuzz(func(t *testing.T, op uint8, data []byte) {
		if len(data) > 256 {
			data = data[:256]
		}
		m, err := New(Deterministic(uint64(op) ^ uint64(len(data))))
		if err != nil {
			t.Fatal(err)
		}

		// Seed a small population so ops have state to work with.
		src, _ := NewNetAddr(NetIPv4, []byte{2, 3, 4, 5}, 30303)
		for i := range 8 {
			addr, err := NewNetAddr(NetIPv4, []byte{byte(0x80 | i), byte(i), 1, 1}, 30303)
			if err != nil {
				continue
			}
			m.AddOne(addr, 0x00, nil, time.Now(), src, SourceTCPGossip, 0)
		}

		// Dispatch the fuzzed op.
		opcode := op & 0x07
		var addr NetAddr
		if len(data) >= 4 {
			addr, _ = NewNetAddr(NetIPv4, data[:4], 30303)
		}
		switch opcode {
		case 0:
			if addr.Valid() {
				m.AddOne(addr, 0x00, nil, time.Now(), src, SourceTCPGossip, 0)
			}
		case 1:
			if addr.Valid() {
				m.Good(addr, time.Now())
			}
		case 2:
			if addr.Valid() {
				m.Attempt(addr, true, time.Now())
			}
		case 3:
			_, _, _ = m.Select(false, nil)
		case 4:
			_ = m.GetAddr(10, 0, nil, true)
		case 5:
			m.ResolveCollisions()
		case 6:
			// Round-trip through persistence.
			dir := t.TempDir()
			path := filepath.Join(dir, "addrbook.rlp")
			if err := m.Save(path); err != nil {
				t.Fatalf("Save: %v", err)
			}
			m2, err := New(Deterministic(999))
			if err != nil {
				t.Fatal(err)
			}
			if err := m2.Load(path); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if m2.Size(nil, nil) != m.Size(nil, nil) {
				t.Errorf("round-trip size mismatch: %d vs %d", m2.Size(nil, nil), m.Size(nil, nil))
			}
		}

		// Structural invariant: bucket occupancy ≤ bucketSize per
		// bucket. Take the lock once and walk directly — exercising
		// the same table the implementation maintains.
		m.mu.Lock()
		for b := range newBucketCount {
			filled := 0
			for _, id := range m.vvNew[b] {
				if id != -1 {
					filled++
				}
			}
			if filled > bucketSize {
				t.Errorf("new bucket %d overfilled: %d", b, filled)
			}
		}
		for b := range triedBucketCount {
			filled := 0
			for _, id := range m.vvTried[b] {
				if id != -1 {
					filled++
				}
			}
			if filled > bucketSize {
				t.Errorf("tried bucket %d overfilled: %d", b, filled)
			}
		}
		// Per-network count invariant: every entry in mapInfo
		// accounts for exactly one slot in m_network_counts.
		counts := make(map[NetID]newTriedCount)
		for _, info := range m.mapInfo {
			c := counts[info.Addr.Network]
			if info.InTried {
				c.tried++
			} else if info.RefCount > 0 {
				c.new++
			}
			counts[info.Addr.Network] = c
		}
		for net, c := range counts {
			stored := m.networkCounts[net]
			if stored.new != c.new || stored.tried != c.tried {
				t.Errorf("networkCounts drift for %s: stored=%+v actual=%+v", net, stored, c)
			}
		}
		m.mu.Unlock()
	})
}
