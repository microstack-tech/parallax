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
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
)

func TestCrawlStateRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	in := &CrawlState{
		Nodes: map[string]*CrawlNode{
			"1/1.2.3.4/32110": {
				NetworkID:    disc.NetIPv4,
				IP:           "1.2.3.4",
				TCPPort:      32110,
				KeyType:      disc.KeyTypeNone,
				FirstSeen:    now,
				LastSuccess:  now.Add(time.Hour),
				LastAttempt:  now.Add(time.Hour),
				SuccessCount: 5,
				FailCount:    1,
				Capabilities: []string{"parallax/66", "parallax-disc/1"},
				Stat2H:       AddrStat{Weight: 0.9, Count: 3.2, Reliability: 0.97},
				Stat1W:       AddrStat{Weight: 0.5, Count: 12, Reliability: 0.48},
			},
			"2/2001:db8::1/32110": {
				NetworkID: disc.NetIPv6,
				IP:        "2001:db8::1",
				TCPPort:   32110,
				KeyType:   disc.KeyTypeNone,
				FirstSeen: now,
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := saveState(path, in); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	out, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if len(out.Nodes) != len(in.Nodes) {
		t.Fatalf("node count: got %d, want %d", len(out.Nodes), len(in.Nodes))
	}
	for k, want := range in.Nodes {
		got, ok := out.Nodes[k]
		if !ok {
			t.Errorf("missing node %q", k)
			continue
		}
		// UpdatedAt is set fresh by saveState; compare nodes only.
		if !reflect.DeepEqual(want, got) {
			t.Errorf("node %q mismatch:\n  want %+v\n  got  %+v", k, want, got)
		}
	}
}

func TestLoadStateMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.json")
	st, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState on missing file: %v", err)
	}
	if st == nil || st.Nodes == nil {
		t.Fatalf("expected non-nil empty state, got %+v", st)
	}
	if len(st.Nodes) != 0 {
		t.Errorf("expected zero nodes, got %d", len(st.Nodes))
	}
}

func TestLoadStateCorruptIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := writeFile(path, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	_, err := loadState(path)
	if err == nil {
		t.Fatal("expected error on corrupt state file, got nil")
	}
}

func TestRegisterAndEnqueueDedup(t *testing.T) {
	w := &walker{
		state:  &CrawlState{Nodes: map[string]*CrawlNode{}},
		todoCh: make(chan *CrawlNode, 8),
	}
	ctx := context.Background()
	n1 := &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.2.3.4", TCPPort: 32110}
	n2 := &CrawlNode{NetworkID: disc.NetIPv4, IP: "1.2.3.4", TCPPort: 32110}

	w.registerAndEnqueue(ctx, n1)
	w.registerAndEnqueue(ctx, n2) // same key — should dedup

	if got := atomic.LoadInt64(&w.outstanding); got != 1 {
		t.Errorf("outstanding = %d, want 1 (dedup failed)", got)
	}
	if got := len(w.todoCh); got != 1 {
		t.Errorf("queue depth = %d, want 1 (second enqueue should be dropped)", got)
	}
	if got := len(w.state.Nodes); got != 1 {
		t.Errorf("state size = %d, want 1", got)
	}
}

func TestRegisterAndEnqueueRefreshesIdentity(t *testing.T) {
	// A node that previously appeared as legacy (KeyType=0x01 with NodeID)
	// migrates to v2 (KeyType=0x00). The walker must keep the same key
	// (so accumulated stats survive) but refresh KeyType/NodeID.
	w := &walker{
		state: &CrawlState{Nodes: map[string]*CrawlNode{
			"1/1.2.3.4/32110": {
				NetworkID:    disc.NetIPv4,
				IP:           "1.2.3.4",
				TCPPort:      32110,
				KeyType:      disc.KeyTypeSecp256k1,
				NodeID:       "deadbeef",
				SuccessCount: 42,
			},
		}},
		todoCh: make(chan *CrawlNode, 8),
	}
	ctx := context.Background()
	migrated := &CrawlNode{
		NetworkID: disc.NetIPv4,
		IP:        "1.2.3.4",
		TCPPort:   32110,
		KeyType:   disc.KeyTypeNone,
	}
	w.registerAndEnqueue(ctx, migrated)

	got := w.state.Nodes["1/1.2.3.4/32110"]
	if got.KeyType != disc.KeyTypeNone {
		t.Errorf("KeyType not refreshed: got %d, want %d", got.KeyType, disc.KeyTypeNone)
	}
	if got.NodeID != "" {
		t.Errorf("NodeID not cleared: got %q", got.NodeID)
	}
	if got.SuccessCount != 42 {
		t.Errorf("SuccessCount lost on identity refresh: got %d, want 42", got.SuccessCount)
	}
}

func TestRegisterAndEnqueueNeverDowngradesIdentity(t *testing.T) {
	// A v2-native entry (operator-seeded or probe-verified) re-learned
	// via gossip carrying a stale legacy identity must stay v2 — v1.x
	// peers gossip dual-stack nodes as secp256k1 indefinitely.
	w := &walker{
		state: &CrawlState{Nodes: map[string]*CrawlNode{
			"1/1.2.3.4/32110": {
				NetworkID:    disc.NetIPv4,
				IP:           "1.2.3.4",
				TCPPort:      32110,
				KeyType:      disc.KeyTypeNone,
				SuccessCount: 42,
			},
		}},
		todoCh: make(chan *CrawlNode, 8),
	}
	ctx := context.Background()
	gossiped := &CrawlNode{
		NetworkID: disc.NetIPv4,
		IP:        "1.2.3.4",
		TCPPort:   32110,
		KeyType:   disc.KeyTypeSecp256k1,
		NodeID:    "deadbeef",
	}
	w.registerAndEnqueue(ctx, gossiped)

	got := w.state.Nodes["1/1.2.3.4/32110"]
	if got.KeyType != disc.KeyTypeNone {
		t.Errorf("v2 entry downgraded to KeyType %d by gossip", got.KeyType)
	}
	if got.NodeID != "" {
		t.Errorf("v2 entry picked up gossiped NodeID %q", got.NodeID)
	}
}

// TestIsTerribleClauses walks each clause of the Bitcoin Core
// AddrInfo::IsTerrible port.
func TestIsTerribleClauses(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		node CrawlNode
		want bool
	}{
		{"tried-last-minute-protected", CrawlNode{LastAttempt: now.Add(-30 * time.Second), FailStreak: 100}, false},
		{"never-succeeded-3-attempts", CrawlNode{LastAttempt: now.Add(-2 * time.Hour), FailStreak: 3}, true},
		{"never-succeeded-2-attempts", CrawlNode{LastAttempt: now.Add(-2 * time.Hour), FailStreak: 2}, false},
		{"week-dead-10-failures", CrawlNode{LastAttempt: now.Add(-2 * time.Hour), LastSuccess: now.Add(-8 * 24 * time.Hour), FailStreak: 10}, true},
		{"week-dead-9-failures", CrawlNode{LastAttempt: now.Add(-2 * time.Hour), LastSuccess: now.Add(-8 * 24 * time.Hour), FailStreak: 9}, false},
		{"recent-success-10-failures", CrawlNode{LastAttempt: now.Add(-2 * time.Hour), LastSuccess: now.Add(-time.Hour), FailStreak: 10}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.isTerrible(now); got != tc.want {
				t.Errorf("isTerrible() = %v, want %v (node %+v)", got, tc.want, tc.node)
			}
		})
	}
}

func TestRequeueAllEvictsTerrible(t *testing.T) {
	now := time.Now()
	terrible := &CrawlNode{
		NetworkID:   disc.NetIPv4,
		IP:          "9.9.9.9",
		TCPPort:     32110,
		LastAttempt: now.Add(-15 * time.Minute),
		FailStreak:  5,
	}
	healthy := &CrawlNode{
		NetworkID:   disc.NetIPv4,
		IP:          "1.2.3.4",
		TCPPort:     32110,
		LastAttempt: now.Add(-15 * time.Minute),
		LastSuccess: now.Add(-15 * time.Minute),
	}
	w := &walker{
		state: &CrawlState{Nodes: map[string]*CrawlNode{
			nodeKey(terrible): terrible,
			nodeKey(healthy):  healthy,
		}},
		todoCh: make(chan *CrawlNode, 8),
	}
	w.requeueAll(context.Background())

	if _, ok := w.state.Nodes[nodeKey(terrible)]; ok {
		t.Error("terrible node not evicted from state")
	}
	if _, ok := w.state.Nodes[nodeKey(healthy)]; !ok {
		t.Error("healthy node evicted from state")
	}
	if got := len(w.todoCh); got != 1 {
		t.Errorf("queue depth = %d, want 1 (only the healthy node requeued)", got)
	}
}

// writeFile is a tiny helper to keep test imports lean.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
