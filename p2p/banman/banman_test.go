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
	"math/rand"
	"net"
	"path/filepath"
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
