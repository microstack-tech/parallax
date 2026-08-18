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

package p2p

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
)

// fakeResolver is a deterministic stand-in for net.DefaultResolver.
type fakeResolver struct {
	mu      sync.Mutex
	results map[string][]string
	errOn   map[string]error
	calls   int
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err, ok := f.errOn[host]; ok {
		return nil, err
	}
	return f.results[host], nil
}

func (f *fakeResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestResolveAndIngestHappyPath(t *testing.T) {
	m, err := addrman.New()
	if err != nil {
		t.Fatalf("addrman.New: %v", err)
	}
	r := &fakeResolver{
		results: map[string][]string{
			// 2606:4700:4700::1111 is Cloudflare's public DNS. We
			// can't use 2001:db8::/32 here because addrman rejects
			// the documentation prefix in isRoutableIPv6 — same
			// IsRoutable check Bitcoin Core applies.
			"seed.example.test": {"1.2.3.4", "5.6.7.8", "2606:4700:4700::1111"},
		},
	}
	resolveAndIngest(context.Background(), r, []string{"seed.example.test"}, m, 32110, testLogger())

	if r.callCount() != 1 {
		t.Errorf("LookupHost call count = %d, want 1", r.callCount())
	}
	counts := m.CountsBySource()
	if got := counts[addrman.SourceDNSSeed]; got != 3 {
		t.Errorf("SourceDNSSeed count = %d, want 3", got)
	}
}

func TestResolveAndIngestFiltersUndialable(t *testing.T) {
	m, err := addrman.New()
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{
		results: map[string][]string{
			"seed.example.test": {
				"127.0.0.1",   // loopback — drop
				"0.0.0.0",     // unspecified — drop
				"169.254.1.1", // link-local — drop
				"224.0.0.1",   // multicast — drop
				"::1",         // ipv6 loopback — drop
				"1.2.3.4",     // dialable — keep
			},
		},
	}
	resolveAndIngest(context.Background(), r, []string{"seed.example.test"}, m, 32110, testLogger())

	counts := m.CountsBySource()
	if got := counts[addrman.SourceDNSSeed]; got != 1 {
		t.Errorf("SourceDNSSeed count = %d, want 1 (only 1.2.3.4 should pass)", got)
	}
}

func TestResolveAndIngestSwallowsErrors(t *testing.T) {
	m, err := addrman.New()
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{
		results: map[string][]string{
			"good.example.test": {"1.2.3.4"},
		},
		errOn: map[string]error{
			"bad.example.test": errors.New("simulated DNS failure"),
		},
	}
	// Both hosts in one pass — the bad one must not abort the good one.
	resolveAndIngest(context.Background(), r, []string{"bad.example.test", "good.example.test"}, m, 32110, testLogger())

	counts := m.CountsBySource()
	if got := counts[addrman.SourceDNSSeed]; got != 1 {
		t.Errorf("SourceDNSSeed count = %d, want 1 (good host should still ingest)", got)
	}
}

func TestDNSSeedLoopRespectsContextCancel(t *testing.T) {
	m, err := addrman.New()
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{
		results: map[string][]string{"seed.example.test": {"1.2.3.4"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		// Use a tiny interval so we'd otherwise tick many times.
		dnsSeedLoop(ctx, r, []string{"seed.example.test"}, m, 32110, 50*time.Millisecond, testLogger())
		close(done)
	}()
	// Cancel before the first-tick delay even fires.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dnsSeedLoop did not return after ctx cancel")
	}
	// Either zero or a small number of resolutions are acceptable —
	// the cancel is racy with the first-tick fire. The point of this
	// test is that the loop returns promptly.
}

func TestDNSSeedLoopEmptyHostsIsNoop(t *testing.T) {
	m, err := addrman.New()
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeResolver{}
	// Should return immediately without spawning anything.
	done := make(chan struct{})
	go func() {
		dnsSeedLoop(context.Background(), r, nil, m, 32110, time.Second, testLogger())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dnsSeedLoop did not return for empty hosts")
	}
	if r.callCount() != 0 {
		t.Errorf("resolver was called %d times, want 0", r.callCount())
	}
}

// testLogger is a thin alias around logging.Root() so tests don't have
// to implement the full Logger interface. Output is captured by the
// test runner's discard handler.
func testLogger() logging.Logger { return logging.Root() }
