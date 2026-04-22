package addrman

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestMan(t *testing.T) *AddrMan {
	t.Helper()
	m, err := New(Deterministic(0xDEAD_BEEF_CAFE_BABE))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func addr4(a, b, c, d byte, port uint16) NetAddr {
	return mkIPv4([4]byte{a, b, c, d}, port)
}

// TestAddNewEntryLandsInNewTable — basic Add path.
func TestAddNewEntryLandsInNewTable(t *testing.T) {
	m := newTestMan(t)
	addr := addr4(8, 8, 8, 8, 30303)
	src := addr4(1, 2, 3, 4, 30303)
	if !m.AddOne(addr, time.Now(), src, SourceTCPGossip, 0) {
		t.Fatal("AddOne returned false on fresh entry")
	}
	if got, want := m.Size(nil, nil), 1; got != want {
		t.Fatalf("Size() = %d, want %d", got, want)
	}
	inNew := true
	if got, want := m.Size(nil, &inNew), 1; got != want {
		t.Fatalf("Size(new) = %d, want %d", got, want)
	}
	pos, ok := m.FindAddressPosition(addr)
	if !ok {
		t.Fatal("FindAddressPosition: not found")
	}
	if pos.Tried {
		t.Error("fresh entry should be in new, not tried")
	}
	if pos.Multiplicity != 1 {
		t.Errorf("fresh entry multiplicity = %d, want 1", pos.Multiplicity)
	}
}

// TestAddRejectsUnroutable — RFC1918 addresses must never enter addrman.
func TestAddRejectsUnroutable(t *testing.T) {
	m := newTestMan(t)
	src := addr4(1, 2, 3, 4, 30303)
	if m.AddOne(addr4(10, 0, 0, 1, 30303), time.Now(), src, SourceTCPGossip, 0) {
		t.Fatal("unroutable address was accepted")
	}
	if m.Size(nil, nil) != 0 {
		t.Fatal("addrman should still be empty")
	}
}

// TestGoodPromotesNewToTried — Good() on a new entry moves it into tried.
func TestGoodPromotesNewToTried(t *testing.T) {
	m := newTestMan(t)
	addr := addr4(9, 9, 9, 9, 30303)
	src := addr4(2, 3, 4, 5, 30303)
	m.AddOne(addr, time.Now(), src, SourceTCPGossip, 0)
	if !m.Good(addr, time.Now()) {
		t.Fatal("Good returned false")
	}
	pos, ok := m.FindAddressPosition(addr)
	if !ok {
		t.Fatal("not found after Good")
	}
	if !pos.Tried {
		t.Fatal("entry not in tried after Good")
	}
	newFlag := true
	if got := m.Size(nil, &newFlag); got != 0 {
		t.Errorf("Size(new) = %d after promotion, want 0", got)
	}
	triedFlag := false
	if got := m.Size(nil, &triedFlag); got != 1 {
		t.Errorf("Size(tried) = %d after promotion, want 1", got)
	}
}

// TestGoodOnUnknownAddrIsNoOp — Good on an addr we've never seen returns
// false and changes nothing.
func TestGoodOnUnknownAddrIsNoOp(t *testing.T) {
	m := newTestMan(t)
	if m.Good(addr4(1, 2, 3, 4, 30303), time.Now()) {
		t.Fatal("Good returned true for unknown addr")
	}
}

// TestAttemptIncrementsCounter — after a Good(), Attempt with countFailure
// should bump Attempts.
func TestAttemptIncrementsCounter(t *testing.T) {
	m := newTestMan(t)
	addr := addr4(5, 6, 7, 8, 30303)
	src := addr4(2, 3, 4, 5, 30303)
	m.AddOne(addr, time.Now(), src, SourceTCPGossip, 0)
	m.Good(addr, time.Now())
	// Attempt counter should be 0 right after Good.
	if info := findForTest(m, addr); info == nil || info.Attempts != 0 {
		t.Fatal("attempts not zero after Good")
	}
	// Two countFailure attempts, post-Good, should each increment once
	// since LastCountAttempt is less than lastGood initially. After the
	// first Attempt updates LastCountAttempt, the next one needs
	// LastCountAttempt < lastGood, which fails — so only the first
	// counts. That's the correct Bitcoin semantics.
	m.Attempt(addr, true, time.Now())
	m.Attempt(addr, true, time.Now())
	info := findForTest(m, addr)
	if info.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (only first post-Good failure counts)", info.Attempts)
	}
}

// findForTest exposes an AddrInfo pointer via the lock. Only for tests —
// read-only copy.
func findForTest(m *AddrMan, addr NetAddr) *AddrInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, info := m.findLocked(addr)
	if info == nil {
		return nil
	}
	cp := *info
	return &cp
}

// TestSelectReturnsStoredAddress — after populating the table, Select
// returns something that round-trips.
func TestSelectReturnsStoredAddress(t *testing.T) {
	m := newTestMan(t)
	src := addr4(2, 3, 4, 5, 30303)
	for i := 0; i < 64; i++ {
		m.AddOne(addr4(byte(i|0x80), byte(i), 1, 1, 30303), time.Now(), src, SourceTCPGossip, 0)
	}
	if got := m.Size(nil, nil); got == 0 {
		t.Fatal("table empty after population")
	}
	found := false
	for i := 0; i < 50; i++ {
		_, _, ok := m.Select(false, nil)
		if ok {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Select returned nothing in 50 attempts")
	}
}

// TestSelectEmptyReturnsFalse — no entries means no selection.
func TestSelectEmptyReturnsFalse(t *testing.T) {
	m := newTestMan(t)
	if _, _, ok := m.Select(false, nil); ok {
		t.Fatal("Select on empty addrman returned true")
	}
}

// TestGetAddrRespectsLimits — GetAddr caps output at maxAddresses and
// drops IsTerrible entries when filtered.
func TestGetAddrRespectsLimits(t *testing.T) {
	m := newTestMan(t)
	src := addr4(2, 3, 4, 5, 30303)
	now := time.Now()
	for i := 0; i < 50; i++ {
		m.AddOne(addr4(byte(0x80|i), 1, 2, 3, 30303), now, src, SourceTCPGossip, 0)
	}
	out := m.GetAddr(10, 0, nil, true)
	if len(out) > 10 {
		t.Fatalf("GetAddr returned %d, want <= 10", len(out))
	}
	out2 := m.GetAddr(0, 100, nil, false)
	if len(out2) == 0 {
		t.Fatal("GetAddr(0, 100%) returned nothing")
	}
}

// TestRoundTripSerialization — acceptance criterion for Phase 1:
// Serialize → Deserialize is a fixed point.
func TestRoundTripSerialization(t *testing.T) {
	m := newTestMan(t)
	src := addr4(2, 3, 4, 5, 30303)
	now := time.Now().Truncate(time.Second)
	// Seed a mix of new and tried entries.
	for i := 0; i < 30; i++ {
		m.AddOne(addr4(byte(0x80|i), byte(i), 2, 3, 30303), now, src, SourceTCPGossip, 0)
	}
	for i := 0; i < 5; i++ {
		addr := addr4(byte(0x80|i), byte(i), 2, 3, 30303)
		m.Good(addr, now)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "addrbook.rlp")
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	m2, err := New(Deterministic(1)) // different seed — only the loaded state matters
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := m2.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := m2.Size(nil, nil), m.Size(nil, nil); got != want {
		t.Errorf("Size mismatch after round trip: got %d, want %d", got, want)
	}
	// nKey must match: eviction history and bucket positions depend on it.
	if m.nKey != m2.nKey {
		t.Error("nKey not preserved across round trip")
	}
	// Every entry in m should also be findable in m2, at the same
	// tried/new assignment.
	for id, info := range m.mapInfo {
		pos1, ok := m.FindAddressPosition(info.Addr)
		if !ok {
			continue
		}
		pos2, ok := m2.FindAddressPosition(info.Addr)
		if !ok {
			t.Errorf("entry %d missing in reloaded addrman", id)
			continue
		}
		if pos1.Tried != pos2.Tried {
			t.Errorf("entry %s: Tried %v vs %v after round trip", info.Addr, pos1.Tried, pos2.Tried)
		}
	}
}

// TestLoadMissingFileGeneratesNKey — starting fresh with a path that does
// not exist should yield an empty AddrMan with a non-zero nKey.
func TestLoadMissingFileGeneratesNKey(t *testing.T) {
	m, err := New() // non-deterministic — nKey should already be set by New
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	orig := m.nKey
	if err := m.Load(filepath.Join(t.TempDir(), "does-not-exist.rlp")); err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if m.nKey != orig {
		t.Error("Load on missing file overwrote nKey set by New")
	}
	var zero [32]byte
	if m.nKey == zero {
		t.Error("nKey should be non-zero after New")
	}
}

// TestLoadFutureSchemaRefuses — a file with an unknown newer version must
// not load and must not be deleted/truncated.
func TestLoadFutureSchemaRefuses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "addrbook.rlp")
	// Write a single byte with a far-future version.
	if err := writeFile(path, []byte{0xFF}); err != nil {
		t.Fatal(err)
	}
	m := newTestMan(t)
	err := m.Load(path)
	if err == nil {
		t.Fatal("Load on future-version file returned nil")
	}
	if !isFutureSchemaErr(err) {
		t.Errorf("expected ErrFutureSchema, got %v", err)
	}
	// File must still exist byte-for-byte.
	raw, err := readFile(path)
	if err != nil {
		t.Fatalf("file gone: %v", err)
	}
	if len(raw) != 1 || raw[0] != 0xFF {
		t.Errorf("file was modified: %x", raw)
	}
}
