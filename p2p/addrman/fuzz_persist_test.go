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

// FuzzPersistDecode — feeds arbitrary bytes to the addrbook.rlp load
// path and asserts:
//
//   - Arbitrary input never panics; any structural problem must surface
//     as an error from Load.
//   - When Load succeeds, Save followed by re-Load preserves the
//     address count and the per-bucket assignment (compared by address
//     service key, since numeric ids are instance-local).
//
// Seeds: the bytes of a real addrbook saved from a deterministic
// AddrMan populated with ~50 addresses, plus truncations and header
// (version byte) mutations of that file.
//
// Note for long campaigns: this body is file-IO heavy (~5k execs/sec),
// so the coordinator's default 60s-per-input minimization budget can
// make the execs counter appear stalled whenever new coverage is found
// (minimization execs are not displayed). Pass -fuzzminimizetime=5s to
// keep throughput visible; this is Go tooling behavior, not a hang.
func FuzzPersistDecode(f *testing.F) {
	valid := savedAddrbookBytes(f)
	f.Add(valid)
	// Truncations — header only, mid-body, one byte short.
	if len(valid) > 0 {
		f.Add(valid[:1])
	}
	if len(valid) > 2 {
		f.Add(valid[:len(valid)/2])
		f.Add(valid[:len(valid)-1])
	}
	// Header mutations — version 0 (unknown older), 2 (future schema),
	// 255 (far future), and a corrupted first body byte.
	for _, v := range []byte{0, 2, 255} {
		mut := append([]byte{v}, valid[1:]...)
		f.Add(mut)
	}
	if len(valid) > 2 {
		mut := append([]byte(nil), valid...)
		mut[1] ^= 0xff
		f.Add(mut)
	}
	f.Add([]byte{})
	f.Add([]byte{schemaV1})
	f.Add([]byte{schemaV1, 0xc0})

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "addrbook.rlp")
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
		m, err := New(Deterministic(1))
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Load(path); err != nil {
			// Errors on malformed input are fine; only panics are bugs.
			return
		}

		// Successful load: the state must survive a save/re-load cycle.
		resaved := filepath.Join(dir, "resaved.rlp")
		if err := m.Save(resaved); err != nil {
			t.Fatalf("Save after successful Load: %v", err)
		}
		m2, err := New(Deterministic(1))
		if err != nil {
			t.Fatal(err)
		}
		if err := m2.Load(resaved); err != nil {
			t.Fatalf("re-Load of just-saved addrbook: %v", err)
		}
		if got, want := m2.Size(nil, nil), m.Size(nil, nil); got != want {
			t.Errorf("address count changed across save/re-load: %d vs %d", got, want)
		}

		// Bucket-assignment invariant: every slot in both tables must
		// hold the same address (by service key) in both instances.
		m.mu.Lock()
		m2.mu.Lock()
		keyAt := func(mm *AddrMan, id int64) string {
			if id == -1 {
				return ""
			}
			return string(mm.mapInfo[id].Addr.serviceKey())
		}
		for b := range newBucketCount {
			for p := range bucketSize {
				k1 := keyAt(m, m.vvNew[b][p])
				k2 := keyAt(m2, m2.vvNew[b][p])
				if k1 != k2 {
					t.Errorf("new bucket %d pos %d differs after round-trip: %q vs %q", b, p, k1, k2)
				}
			}
		}
		for b := range triedBucketCount {
			for p := range bucketSize {
				k1 := keyAt(m, m.vvTried[b][p])
				k2 := keyAt(m2, m2.vvTried[b][p])
				if k1 != k2 {
					t.Errorf("tried bucket %d pos %d differs after round-trip: %q vs %q", b, p, k1, k2)
				}
			}
		}
		m2.mu.Unlock()
		m.mu.Unlock()
	})
}

// savedAddrbookBytes builds a deterministic AddrMan with ~50 routable
// IPv4 addresses (a third promoted to tried), saves it, and returns the
// raw file bytes for use as the primary fuzz seed.
func savedAddrbookBytes(f *testing.F) []byte {
	f.Helper()
	m, err := New(Deterministic(42))
	if err != nil {
		f.Fatal(err)
	}
	src, _ := NewNetAddr(NetIPv4, []byte{2, 3, 4, 5}, 30303)
	now := time.Now()
	for i := range 50 {
		// First octet in 128..191 keeps the addresses publicly routable.
		addr, err := NewNetAddr(NetIPv4, []byte{byte(0x80 | (i & 0x3f)), byte(i), byte(i * 5), 7}, 30303)
		if err != nil {
			continue
		}
		m.AddOne(addr, 0x00, nil, now, src, SourceTCPGossip, 0)
		if i%3 == 0 {
			m.Good(addr, now)
		}
	}
	path := filepath.Join(f.TempDir(), "addrbook.rlp")
	if err := m.Save(path); err != nil {
		f.Fatal(err)
	}
	raw, err := readFile(path)
	if err != nil {
		f.Fatal(err)
	}
	return raw
}
