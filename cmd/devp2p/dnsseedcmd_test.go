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
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
)

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

	// Pass: v2, default port, fresh, healthy.
	add(mk(disc.NetIPv4, "1.2.3.4", 32110, disc.KeyTypeNone, now, 10, 1))
	add(mk(disc.NetIPv4, "5.6.7.8", 32110, disc.KeyTypeNone, now, 5, 0))
	add(mk(disc.NetIPv6, "2001:db8::1", 32110, disc.KeyTypeNone, now, 4, 0))

	// Drop: legacy KeyType.
	add(mk(disc.NetIPv4, "9.9.9.9", 32110, disc.KeyTypeSecp256k1, now, 5, 0))
	// Drop: non-default port.
	add(mk(disc.NetIPv4, "4.4.4.4", 12345, disc.KeyTypeNone, now, 5, 0))
	// Drop: too few successes.
	add(mk(disc.NetIPv4, "7.7.7.7", 32110, disc.KeyTypeNone, now, 1, 0))
	// Drop: stale.
	add(mk(disc.NetIPv4, "3.3.3.3", 32110, disc.KeyTypeNone, now.Add(-48*time.Hour), 5, 0))
	// Drop: success rate below threshold.
	add(mk(disc.NetIPv4, "2.2.2.2", 32110, disc.KeyTypeNone, now, 3, 7))
	// Drop: loopback (defense-in-depth — should never reach this stage,
	// but isDialableIP skips it on the consume side too).
	add(mk(disc.NetIPv4, "127.0.0.1", 32110, disc.KeyTypeNone, now, 5, 0))

	return st
}

func defaultFilters() compileFilters {
	return compileFilters{
		Name:           "seed.example.test",
		DefaultPort:    32110,
		MaxAge:         24 * time.Hour,
		MinSuccesses:   3,
		MinSuccessRate: 0.5,
		MinRecords:     3,
	}
}

func TestCompileFiltersAndSorts(t *testing.T) {
	now := time.Now()
	st := fixtureCrawlState(now)

	z, err := compileSeedZone(st, defaultFilters())
	if err != nil {
		t.Fatalf("compileSeedZone: %v", err)
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
	if _, err := compileSeedZone(st, f); err == nil {
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
		{"v2-fresh-healthy", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5}, true},
		{"legacy-keytype-rejected", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeSecp256k1, LastSuccess: now, SuccessCount: 5}, false},
		{"wrong-port-rejected", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 22, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5}, false},
		{"stale-rejected", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now.Add(-25 * time.Hour), SuccessCount: 5}, false},
		{"too-few-successes", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 2}, false},
		{"low-success-rate", &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.1.1.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 3, FailCount: 100}, false},
		{"undialable-loopback", &CrawlNode{NetworkID: disc.NetIPv4, IP: "127.0.0.1", TCPPort: 32110, KeyType: disc.KeyTypeNone, LastSuccess: now, SuccessCount: 5}, false},
	}

	f := defaultFilters()
	f.MinRecords = 0 // we want to see compiled output even if empty
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &CrawlState{Nodes: map[string]*CrawlNode{nodeKey(tc.node): tc.node}}
			z, err := compileSeedZone(st, f)
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
