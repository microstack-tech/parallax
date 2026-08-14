// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of parallax.
//
// parallax is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// parallax is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with parallax. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
)

// reliableWindows is the shape a healthy node's stats reach within one
// crawl pass: the 2h window satisfied (reliability > 0.85, count > 2).
func reliableWindows(n *CrawlNode) *CrawlNode {
	n.Stat2H = AddrStat{Weight: 0.9, Count: 3, Reliability: 0.95}
	return n
}

// decayedWindows is a node whose reliability has decayed below every
// IsGood clause despite plenty of attempts in each window.
func decayedWindows(n *CrawlNode) *CrawlNode {
	bad := AddrStat{Weight: 0.9, Count: 40, Reliability: 0.2}
	n.Stat2H, n.Stat8H, n.Stat1D, n.Stat1W, n.Stat1M = bad, bad, bad, bad, bad
	return n
}

// fixtureCrawlState builds a CrawlState covering every filter axis the
// compile step has to deal with.
func fixtureCrawlState(now time.Time) *CrawlState {
	mk := func(net uint8, ip string, port uint16, kt uint8, lastSucc time.Time, succ, fail uint64) *CrawlNode {
		return &CrawlNode{
			NetworkID:    net,
			IP:           ip,
			TCPPort:      port,
			KeyType:      kt,
			LastSuccess:  lastSucc,
			LastAttempt:  lastSucc,
			SuccessCount: succ,
			FailCount:    fail,
		}
	}
	st := &CrawlState{Nodes: map[string]*CrawlNode{}}
	add := func(n *CrawlNode) { st.Nodes[nodeKey(n)] = n }

	// Pass: v2, default port, fresh, reliable windows.
	add(reliableWindows(mk(disc.NetIPv4, "1.2.3.4", 32110, disc.KeyTypeNone, now, 10, 1)))
	add(reliableWindows(mk(disc.NetIPv4, "5.6.7.8", 32110, disc.KeyTypeNone, now, 5, 0)))
	add(reliableWindows(mk(disc.NetIPv6, "2001:db8::1", 32110, disc.KeyTypeNone, now, 4, 0)))

	// Drop: legacy KeyType.
	add(reliableWindows(mk(disc.NetIPv4, "9.9.9.9", 32110, disc.KeyTypeSecp256k1, now, 5, 0)))
	// Drop: non-default port.
	add(reliableWindows(mk(disc.NetIPv4, "4.4.4.4", 12345, disc.KeyTypeNone, now, 5, 0)))
	// Drop: long history but no window stats yet (pre-migration state
	// file, or a node whose windows never accumulated) — the bootstrap
	// clause only covers total <= 3.
	add(mk(disc.NetIPv4, "7.7.7.7", 32110, disc.KeyTypeNone, now, 6000, 6000))
	// Drop: stale.
	add(reliableWindows(mk(disc.NetIPv4, "3.3.3.3", 32110, disc.KeyTypeNone, now.Add(-48*time.Hour), 5, 0)))
	// Drop: reliability decayed below every window clause.
	add(decayedWindows(mk(disc.NetIPv4, "2.2.2.2", 32110, disc.KeyTypeNone, now, 30, 70)))
	// Drop: loopback (defense-in-depth — should never reach this stage,
	// but isDialableIP skips it on the consume side too).
	add(reliableWindows(mk(disc.NetIPv4, "127.0.0.1", 32110, disc.KeyTypeNone, now, 5, 0)))

	return st
}

func defaultFilters() compileFilters {
	return compileFilters{
		Name:        "seed.example.test",
		DefaultPort: 32110,
		MaxAge:      24 * time.Hour,
		MinRecords:  3,
	}
}

func TestCompileFiltersAndSorts(t *testing.T) {
	now := time.Now()
	st := fixtureCrawlState(now)

	z, tally, err := compileSeedZone(st, defaultFilters())
	if err != nil {
		t.Fatalf("compileSeedZone: %v", err)
	}
	wantTally := compileTally{Legacy: 1, WrongPort: 1, Stale: 1, Unreliable: 2, Undialable: 1}
	if tally != wantTally {
		t.Errorf("drop tally = %+v, want %+v", tally, wantTally)
	}

	wantIPs := []string{"1.2.3.4", "5.6.7.8", "2001:db8::1"}
	gotIPs := make([]string, 0, len(z.Records))
	for _, r := range z.Records {
		gotIPs = append(gotIPs, r.IP)
	}
	sort.Strings(wantIPs)
	sort.Strings(gotIPs)
	if !reflect.DeepEqual(gotIPs, wantIPs) {
		t.Errorf("compiled IPs = %v, want %v", gotIPs, wantIPs)
	}

	// Ordering check: A before AAAA, then by IP within family.
	for i := 1; i < len(z.Records); i++ {
		prev, cur := z.Records[i-1], z.Records[i]
		if prev.Family > cur.Family {
			t.Errorf("records not sorted by family: %v before %v", prev, cur)
		}
		if prev.Family == cur.Family && prev.IP > cur.IP {
			t.Errorf("records within family not sorted by IP: %v before %v", prev, cur)
		}
	}

	if z.Name != "seed.example.test" {
		t.Errorf("Name = %q, want seed.example.test", z.Name)
	}
}

func TestCompileRefusesNearEmpty(t *testing.T) {
	now := time.Now()
	st := fixtureCrawlState(now)
	f := defaultFilters()
	f.MinRecords = 100 // higher than what fixture can satisfy
	if _, _, err := compileSeedZone(st, f); err == nil {
		t.Fatal("expected error on near-empty compile, got nil")
	}
}

func TestCompileEachFilterAxis(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		node *CrawlNode
		want bool
	}{
		{"v2-fresh-reliable", reliableWindows(&CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5}), true},
		{"bootstrap-short-history", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 2, FailCount: 1}, true},
		{"legacy-keytype-rejected", reliableWindows(&CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeSecp256k1, LastSuccess: now, SuccessCount: 5}), false},
		{"wrong-port-rejected", reliableWindows(&CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 22, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5}), false},
		{"stale-rejected", reliableWindows(&CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now.Add(-25 * time.Hour), SuccessCount: 5}), false},
		{"windowless-long-history", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5000, FailCount: 5000}, false},
		{"decayed-reliability", decayedWindows(&CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 30, FailCount: 100}), false},
		{"undialable-loopback", reliableWindows(&CrawlNode{NetworkID: disc.NetIPv4, IP: "127.0.0.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5}), false},
	}

	f := defaultFilters()
	f.MinRecords = 0 // we want to see compiled output even if empty
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &CrawlState{Nodes: map[string]*CrawlNode{nodeKey(tc.node): tc.node}}
			z, _, err := compileSeedZone(st, f)
			if err != nil {
				t.Fatalf("compileSeedZone: %v", err)
			}
			passed := len(z.Records) == 1
			if passed != tc.want {
				t.Errorf("passes=%v, want %v (records: %+v)", passed, tc.want, z.Records)
			}
		})
	}
}

// TestAddrStatUpdate pins the EWMA behavior ported from bitcoin-seeder's
// CAddrStat: sustained success converges reliability toward 1, sustained
// failure decays it toward 0, and a long gap between attempts mostly
// forgets the window.
func TestAddrStatUpdate(t *testing.T) {
	const tau = 2 * 3600.0

	// First-ever update (age = minRetryAge) on success.
	var s AddrStat
	s.Update(true, minRetryAge, tau)
	f := math.Exp(-minRetryAge / tau)
	if got, want := s.Reliability, 1.0-f; math.Abs(got-want) > 1e-12 {
		t.Errorf("first update reliability = %v, want %v", got, want)
	}
	if got := s.Count; got != 1 {
		t.Errorf("first update count = %v, want 1", got)
	}

	// Sustained success at a 15-minute cadence: reliability climbs above
	// the 2h clause threshold once ~2*tau of history has accumulated
	// (rel after n all-success updates is 1 - exp(-sum(ages)/tau)).
	s = AddrStat{}
	for i := 0; i < 20; i++ {
		s.Update(true, 900, tau)
	}
	if s.Reliability <= 0.85 {
		t.Errorf("sustained success reliability = %v, want > 0.85", s.Reliability)
	}
	if s.Count <= 2 {
		t.Errorf("sustained success count = %v, want > 2", s.Count)
	}

	// Sustained failure decays reliability toward 0 while count keeps
	// registering the attempts.
	for i := 0; i < 20; i++ {
		s.Update(false, 900, tau)
	}
	if s.Reliability >= 0.1 {
		t.Errorf("post-outage reliability = %v, want < 0.1", s.Reliability)
	}

	// A gap much longer than tau forgets the window: one success after
	// a 10*tau silence dominates what came before.
	s.Update(true, 10*tau, tau)
	if s.Reliability <= 0.85 {
		t.Errorf("post-gap reliability = %v, want > 0.85 (window should have decayed)", s.Reliability)
	}
}

// TestIsGoodClauses walks each clause of the bitcoin-seeder IsGood()
// port. Thresholds are strict (>): sitting exactly on one must fail.
func TestIsGoodClauses(t *testing.T) {
	// Skip the bootstrap clause in window cases via a long history.
	base := CrawlNode{SuccessCount: 50, FailCount: 50}
	cases := []struct {
		name string
		mod  func(n *CrawlNode)
		want bool
	}{
		{"bootstrap-2of3", func(n *CrawlNode) { n.SuccessCount, n.FailCount = 2, 1 }, true},
		{"bootstrap-1of3", func(n *CrawlNode) { n.SuccessCount, n.FailCount = 1, 2 }, false},
		{"2h-clause", func(n *CrawlNode) { n.Stat2H = AddrStat{Reliability: 0.86, Count: 2.1} }, true},
		{"2h-reliability-on-threshold", func(n *CrawlNode) { n.Stat2H = AddrStat{Reliability: 0.85, Count: 3} }, false},
		{"2h-count-on-threshold", func(n *CrawlNode) { n.Stat2H = AddrStat{Reliability: 0.99, Count: 2} }, false},
		{"8h-clause", func(n *CrawlNode) { n.Stat8H = AddrStat{Reliability: 0.71, Count: 4.1} }, true},
		{"1d-clause", func(n *CrawlNode) { n.Stat1D = AddrStat{Reliability: 0.56, Count: 8.1} }, true},
		{"1w-clause", func(n *CrawlNode) { n.Stat1W = AddrStat{Reliability: 0.46, Count: 16.1} }, true},
		{"1m-clause", func(n *CrawlNode) { n.Stat1M = AddrStat{Reliability: 0.36, Count: 32.1} }, true},
		{"no-clause", func(n *CrawlNode) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := base
			tc.mod(&n)
			if got := n.isGood(); got != tc.want {
				t.Errorf("isGood() = %v, want %v (node %+v)", got, tc.want, n)
			}
		})
	}
}

func TestSeedZoneRoundTrip(t *testing.T) {
	z := &SeedZone{
		Name:      "seed.example.test",
		UpdatedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		Seq:       1700000000,
		Records: []SeedRecord{
			{Family: "A", IP: "1.2.3.4"},
			{Family: "A", IP: "5.6.7.8"},
			{Family: "AAAA", IP: "2001:db8::1"},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "z.json")
	if err := saveSeedZone(path, z); err != nil {
		t.Fatalf("saveSeedZone: %v", err)
	}
	got, err := loadSeedZone(path)
	if err != nil {
		t.Fatalf("loadSeedZone: %v", err)
	}
	if !reflect.DeepEqual(z, got) {
		t.Errorf("round-trip mismatch:\n want %+v\n got  %+v", z, got)
	}
}

func TestLoadSeedZoneRejectsEmptyName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "z.json")
	// Valid JSON but missing Name.
	if err := writeFile(path, []byte(`{"records":[{"family":"A","ip":"1.2.3.4"}]}`)); err != nil {
		t.Fatal(err)
	}
	_, err := loadSeedZone(path)
	if err == nil {
		t.Fatal("expected error for SeedZone with empty Name, got nil")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error message missing 'name': %v", err)
	}
}

func TestZonefileGoldenOutput(t *testing.T) {
	z := &SeedZone{
		Name:      "seed.example.test",
		UpdatedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		Seq:       1700000000,
		Records: []SeedRecord{
			{Family: "A", IP: "1.2.3.4"},
			{Family: "A", IP: "5.6.7.8"},
			{Family: "AAAA", IP: "2001:db8::1"},
		},
	}
	got := renderZonefile(z, 3600)
	want := strings.Join([]string{
		"; parallax-disc DNS seed zone",
		"; generated by `devp2p dns-seed to-zonefile` at 2026-04-23T12:00:00Z",
		"; 3 records, seq=1700000000",
		"$ORIGIN seed.example.test.",
		"$TTL 3600",
		"@\t3600\tIN\tA\t1.2.3.4",
		"@\t3600\tIN\tA\t5.6.7.8",
		"@\t3600\tIN\tAAAA\t2001:db8::1",
		"",
	}, "\n")
	if got != want {
		t.Errorf("zonefile mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}
