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

package banman

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
)

// TestBanRoundTripIPv4 — Ban / IsBanned / Unban / IsBanned cycle.
func TestBanRoundTripIPv4(t *testing.T) {
	t.Parallel()
	bm, err := New("", logging.Root())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ip := net.IPv4(192, 0, 2, 17)
	if bm.IsBanned(ip) {
		t.Fatalf("fresh BanMan should not report ip as banned")
	}
	if err := bm.Ban(ip, time.Hour, ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if !bm.IsBanned(ip) {
		t.Fatalf("after Ban, IsBanned must return true")
	}
	ok, err := bm.Unban(ip)
	if err != nil {
		t.Fatalf("Unban: %v", err)
	}
	if !ok {
		t.Fatalf("Unban returned ok=false for an active ban")
	}
	if bm.IsBanned(ip) {
		t.Fatalf("after Unban, IsBanned must return false")
	}
}

// TestBanSubnetCoversChildIP — banning 10.0.0.0/24 must cause
// IsBanned to return true for any /32 inside that range. Mirrors
// Bitcoin Core's CSubNet::Match (src/netbase.cpp).
func TestBanSubnetCoversChildIP(t *testing.T) {
	t.Parallel()
	bm, _ := New("", logging.Root())
	_, subnet, _ := net.ParseCIDR("10.0.0.0/24")
	if err := bm.BanSubnet(subnet, time.Hour, ReasonManual); err != nil {
		t.Fatalf("BanSubnet: %v", err)
	}
	for _, oct := range []byte{1, 17, 254} {
		ip := net.IPv4(10, 0, 0, oct)
		if !bm.IsBanned(ip) {
			t.Errorf("expected %s banned via subnet 10.0.0.0/24", ip)
		}
	}
	// Outside the subnet: not banned.
	if bm.IsBanned(net.IPv4(10, 0, 1, 5)) {
		t.Errorf("10.0.1.5 should not match 10.0.0.0/24")
	}
}

// TestBanExpiry — banlist.json stores Unix seconds so the
// minimum ban duration is 1s. Ban for 1s, sleep past it, verify
// IsBanned returns false and the entry is pruned from
// ListBanned.
func TestBanExpiry(t *testing.T) {
	t.Parallel()
	bm, _ := New("", logging.Root())
	ip := net.IPv4(203, 0, 113, 9)
	if err := bm.Ban(ip, time.Second, ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if !bm.IsBanned(ip) {
		t.Fatalf("freshly-banned ip should be banned")
	}
	time.Sleep(1500 * time.Millisecond)
	if bm.IsBanned(ip) {
		t.Errorf("expired ban should not match")
	}
	if list := bm.ListBanned(); len(list) != 0 {
		t.Errorf("expired entry should be pruned from ListBanned, got %v", list)
	}
}

// TestBanIPv6 — Ban / IsBanned for an IPv6 address. The /128
// implied subnet must round-trip through the file representation.
func TestBanIPv6(t *testing.T) {
	t.Parallel()
	bm, _ := New("", logging.Root())
	ip := net.ParseIP("2001:db8::1")
	if err := bm.Ban(ip, time.Hour, ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if !bm.IsBanned(ip) {
		t.Fatalf("ipv6 ban should match")
	}
}

// TestBanPersistence — write banlist.json from one BanMan, load
// it from another, verify entries reload.
func TestBanPersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")

	bm1, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 1: %v", err)
	}
	if err := bm1.Ban(net.IPv4(10, 1, 2, 3), time.Hour, ReasonManual); err != nil {
		t.Fatalf("Ban 1: %v", err)
	}
	_, subnet, _ := net.ParseCIDR("198.51.100.0/24")
	if err := bm1.BanSubnet(subnet, 2*time.Hour, ReasonNodeMisbehavior); err != nil {
		t.Fatalf("BanSubnet 1: %v", err)
	}

	bm2, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 2 (load): %v", err)
	}
	if !bm2.IsBanned(net.IPv4(10, 1, 2, 3)) {
		t.Errorf("loaded banlist did not include 10.1.2.3")
	}
	if !bm2.IsBanned(net.IPv4(198, 51, 100, 99)) {
		t.Errorf("loaded banlist did not include 198.51.100.0/24")
	}
}

// TestBanPersistenceDropsExpiredOnLoad — entries whose
// banned_until is in the past at load time are dropped silently.
// Stops a stale banlist.json from a long-stopped node from
// surfacing already-expired bans.
func TestBanPersistenceDropsExpiredOnLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")

	bm1, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 1: %v", err)
	}
	if err := bm1.Ban(net.IPv4(10, 1, 2, 3), time.Second, ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	bm2, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 2: %v", err)
	}
	if list := bm2.ListBanned(); len(list) != 0 {
		t.Errorf("expired entry should not load, got %v", list)
	}
}

// TestBanV4MappedSubnet — a subnet given in IPv4-mapped IPv6 form
// ("::ffff:1.2.3.0/120") must normalize to its true IPv4 form
// ("1.2.3.0/24"), match both the plain-IPv4 and v4-mapped forms of
// covered addresses, and survive a save/Load round trip. Mirrors
// Bitcoin Core's CSubNet, which stores v4-mapped addresses as IPv4
// (src/netaddress.cpp).
func TestBanV4MappedSubnet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")

	bm1, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 1: %v", err)
	}
	_, subnet, err := net.ParseCIDR("::ffff:1.2.3.0/120")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	if err := bm1.BanSubnet(subnet, time.Hour, ReasonManual); err != nil {
		t.Fatalf("BanSubnet: %v", err)
	}
	if !bm1.IsBanned(net.IPv4(1, 2, 3, 4)) {
		t.Errorf("1.2.3.4 should be banned via ::ffff:1.2.3.0/120")
	}
	if !bm1.IsBanned(net.ParseIP("::ffff:1.2.3.4")) {
		t.Errorf("::ffff:1.2.3.4 should be banned via ::ffff:1.2.3.0/120")
	}
	list := bm1.ListBanned()
	if len(list) != 1 || list[0].Subnet != "1.2.3.0/24" {
		t.Errorf("expected normalized entry [1.2.3.0/24], got %v", list)
	}

	// Round trip: the entry must reload as an active ban.
	bm2, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 2 (load): %v", err)
	}
	if !bm2.IsBanned(net.IPv4(1, 2, 3, 4)) {
		t.Errorf("reloaded banlist lost the v4-mapped subnet ban")
	}
	if !bm2.IsBanned(net.ParseIP("::ffff:1.2.3.4")) {
		t.Errorf("reloaded banlist does not match the v4-mapped form")
	}
}

// TestDumpConcurrentMutators — mutators auto-Dump, and concurrent
// admin RPCs must not race on the shared tmp file (spurious rename
// errors, stale snapshot renamed over a newer one). Run with -race.
func TestDumpConcurrentMutators(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")

	bm, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const goroutines = 8
	const opsPerGoroutine = 25
	errCh := make(chan error, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				ip := net.IPv4(10, 0, byte(g), byte(i))
				if err := bm.Ban(ip, time.Hour, ReasonManual); err != nil {
					errCh <- err
					return
				}
				if i%2 == 1 {
					if _, err := bm.Unban(ip); err != nil {
						errCh <- err
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent mutator: %v", err)
	}

	// The on-disk state must parse and match the in-memory state:
	// the last snapshot written must reflect every completed
	// mutation, not a stale intermediate one.
	bm2, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 2 (load): %v", err)
	}
	want := bm.ListBanned()
	got := bm2.ListBanned()
	if len(got) != len(want) {
		t.Fatalf("on-disk banlist has %d entries, in-memory has %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Subnet != want[i].Subnet {
			t.Errorf("entry %d: on-disk %q != in-memory %q", i, got[i].Subnet, want[i].Subnet)
		}
	}
}

// TestCorruptBanlistRecreated — an unparseable banlist.json must not
// prevent construction: New starts with an empty ban map and the
// next Dump rewrites the file. Bitcoin Core logs "Recreating the
// banlist database" and proceeds (src/banman.cpp:41-45).
func TestCorruptBanlistRecreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bm, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New must tolerate a corrupt banlist, got: %v", err)
	}
	if list := bm.ListBanned(); len(list) != 0 {
		t.Errorf("corrupt banlist should load as empty, got %v", list)
	}

	// The next mutation must rewrite the file with valid content.
	if err := bm.Ban(net.IPv4(10, 1, 2, 3), time.Hour, ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	bm2, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New 2 (reload after rewrite): %v", err)
	}
	if !bm2.IsBanned(net.IPv4(10, 1, 2, 3)) {
		t.Errorf("rewritten banlist did not reload the new entry")
	}
}

// TestClearBanned — wipes every entry; IsBanned returns false for
// previously-banned IPs.
func TestClearBanned(t *testing.T) {
	t.Parallel()
	bm, _ := New("", logging.Root())
	for i := 1; i <= 5; i++ {
		_ = bm.Ban(net.IPv4(10, 0, 0, byte(i)), time.Hour, ReasonManual)
	}
	if got := len(bm.ListBanned()); got != 5 {
		t.Fatalf("setup: ListBanned size = %d, want 5", got)
	}
	if err := bm.ClearBanned(); err != nil {
		t.Fatalf("ClearBanned: %v", err)
	}
	if got := len(bm.ListBanned()); got != 0 {
		t.Errorf("after ClearBanned, ListBanned size = %d, want 0", got)
	}
}

// TestDiscourageBloomMembership — Insert + Contains for an IPv4
// address. Same address under net.IPv4 (16-byte form) and a
// raw 4-byte slice should hit the same bit set.
func TestDiscourageBloomMembership(t *testing.T) {
	t.Parallel()
	bm, _ := New("", logging.Root())
	ip := net.IPv4(192, 0, 2, 17)
	if bm.IsDiscouraged(ip) {
		t.Fatalf("fresh BanMan should not report ip as discouraged")
	}
	bm.Discourage(ip)
	if !bm.IsDiscouraged(ip) {
		t.Errorf("after Discourage, IsDiscouraged must return true")
	}
	// Different IP: should be false (with overwhelming probability).
	other := net.IPv4(8, 8, 8, 8)
	if bm.IsDiscouraged(other) {
		t.Errorf("unrelated IP unexpectedly marked discouraged")
	}
}

// TestDiscourageBloomFalsePositiveRate — sample 50k random
// addresses and assert the false-positive rate stays bounded.
// We're running below the configured 1e-6 nominal but at this
// sample size the variance is large; we accept up to 1e-3 to
// avoid a flaky bound. The test guards against an order-of-
// magnitude regression (e.g., a bad hash that collapses output).
func TestDiscourageBloomFalsePositiveRate(t *testing.T) {
	t.Parallel()
	bm, _ := New("", logging.Root())
	r := rand.New(rand.NewSource(0xb100))
	const insertions = 40_000
	const probes = 5_000

	// Insert a deterministic block of addresses.
	for i := 0; i < insertions; i++ {
		var ip [4]byte
		r.Read(ip[:])
		bm.Discourage(net.IP(ip[:]))
	}
	// Sanity: all inserted should hit (no false negatives).
	r2 := rand.New(rand.NewSource(0xb100))
	for i := 0; i < insertions; i++ {
		var ip [4]byte
		r2.Read(ip[:])
		if !bm.IsDiscouraged(net.IP(ip[:])) {
			t.Fatalf("false negative at insertion %d", i)
		}
	}
	// Probe with addresses NOT in the inserted set (different RNG
	// state that doesn't replay the inserted sequence).
	r3 := rand.New(rand.NewSource(0xb1ad))
	hits := 0
	for i := 0; i < probes; i++ {
		var ip [4]byte
		r3.Read(ip[:])
		if bm.IsDiscouraged(net.IP(ip[:])) {
			hits++
		}
	}
	rate := float64(hits) / float64(probes)
	if rate > 0.001 {
		t.Errorf("Bloom fp rate too high: %d/%d = %.4f (want <= 1e-3)", hits, probes, rate)
	}
}

// TestReBanOnlyExtends — re-banning an active subnet with a shorter
// duration must leave the existing ban untouched; a longer duration
// replaces it. Core's BanMan::Ban semantics: bans only ever extend.
func TestReBanOnlyExtends(t *testing.T) {
	t.Parallel()
	bm, err := New("", logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	ip := net.IP{203, 0, 113, 9}
	if err := bm.Ban(ip, 10*time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	till := func() int64 {
		for _, e := range bm.ListBanned() {
			return e.BannedTill
		}
		t.Fatal("no ban entry")
		return 0
	}
	long := till()

	// A shorter automatic re-ban must not cut the operator ban short.
	if err := bm.Ban(ip, time.Minute, ReasonNodeMisbehavior); err != nil {
		t.Fatal(err)
	}
	if got := till(); got != long {
		t.Fatalf("shorter re-ban changed expiry: %d -> %d", long, got)
	}
	// A longer re-ban extends.
	if err := bm.Ban(ip, 48*time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	if got := till(); got <= long {
		t.Fatalf("longer re-ban did not extend expiry: %d -> %d", long, got)
	}
}

// TestLoadCanonicalizesHandEditedEntries — a hand-edited banlist entry
// with host bits set ("1.2.3.4/24") must load under its canonical key
// ("1.2.3.0/24") so setban remove can find and delete it.
func TestLoadCanonicalizesHandEditedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "banlist.json")
	till := time.Now().Add(time.Hour).Unix()
	raw := fmt.Sprintf(`{"banned_nets":[{"address":"1.2.3.4/24","ban_created":%d,"banned_until":%d,"reason":"manual"}]}`,
		time.Now().Unix(), till)
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	bm, err := New(file, logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	if !bm.IsBanned(net.IP{1, 2, 3, 200}) {
		t.Fatal("hand-edited subnet not effective after load")
	}
	list := bm.ListBanned()
	if len(list) != 1 || list[0].Subnet != "1.2.3.0/24" {
		t.Fatalf("entry not canonicalized: %+v", list)
	}
	_, subnet, _ := net.ParseCIDR("1.2.3.4/24")
	ok, err := bm.UnbanSubnet(subnet)
	if err != nil || !ok {
		t.Fatalf("canonical remove failed: ok=%v err=%v", ok, err)
	}
	if bm.IsBanned(net.IP{1, 2, 3, 200}) {
		t.Fatal("subnet still banned after remove")
	}
}

// TestListBannedDurationFields — listbanned carries Bitcoin Core's
// derived ban_duration and time_remaining fields.
func TestListBannedDurationFields(t *testing.T) {
	bm, err := New("", logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := bm.Ban(net.IPv4(10, 9, 8, 7), 2*time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	list := bm.ListBanned()
	if len(list) != 1 {
		t.Fatalf("listbanned len = %d, want 1", len(list))
	}
	e := list[0]
	if e.BanDuration != e.BannedTill-e.BanCreated {
		t.Fatalf("ban_duration = %d, want banned_until-ban_created = %d", e.BanDuration, e.BannedTill-e.BanCreated)
	}
	if e.BanDuration != int64((2 * time.Hour).Seconds()) {
		t.Fatalf("ban_duration = %d, want 7200", e.BanDuration)
	}
	if e.TimeRemaining <= 0 || e.TimeRemaining > e.BanDuration {
		t.Fatalf("time_remaining = %d, want in (0, %d]", e.TimeRemaining, e.BanDuration)
	}
}

// TestLoadToleratesUnreadableFile — an unreadable banlist path (here:
// a directory) must not fail construction; Core recreates the banlist
// database on any load failure.
func TestLoadToleratesUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "banlist.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	bm, err := New(path, logging.Root())
	if err != nil {
		t.Fatalf("New with unreadable banlist = %v, want nil (recreate semantics)", err)
	}
	if got := len(bm.ListBanned()); got != 0 {
		t.Fatalf("banlist not empty after recreate: %d entries", got)
	}
}

// TestSweepBannedPrunesExpired — the periodic sweep drops expired
// entries from the in-memory map without waiting for a query.
func TestSweepBannedPrunesExpired(t *testing.T) {
	bm, err := New("", logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := bm.Ban(net.IPv4(10, 4, 4, 4), time.Millisecond, ReasonManual); err != nil {
		t.Fatal(err)
	}
	if err := bm.Ban(net.IPv4(10, 5, 5, 5), time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	// Sub-second bans round up to 1s; wait out the short one.
	time.Sleep(1100 * time.Millisecond)
	bm.SweepBanned()
	bm.mu.Lock()
	n := len(bm.banned)
	bm.mu.Unlock()
	if n != 1 {
		t.Fatalf("banned map has %d entries after sweep, want 1", n)
	}
}
