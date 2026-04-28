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
	"net"
	"slices"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/internal/testlog"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/util/mclock"
)

// makeEvictionPeer constructs a synthetic *Peer suitable for the
// eviction test surface: inbound (default), addressable for network
// group computation, with caller-controlled telemetry. Use opts to
// flip flags (trusted, static, outbound) and set telemetry values.
type evictionOpts struct {
	id         enode.ID
	ip         net.IP
	createdAge time.Duration // age relative to now; 0 = "just connected"
	minPing    int64         // ns
	lastBlock  int64         // mclock.AbsTime ns
	lastTx     int64         // mclock.AbsTime ns
	relayTxs   bool
	inbound    bool
	trusted    bool
	static     bool
}

func makeEvictionPeer(t *testing.T, opts evictionOpts) *Peer {
	t.Helper()
	if opts.id == (enode.ID{}) {
		opts.id = randomID()
	}
	if opts.ip == nil {
		opts.ip = net.IPv4(1, 2, 3, 4)
	}
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: opts.ip, Port: 32110}}
	t.Cleanup(func() { _ = fake.Close() })
	p := NewPeerForTest(opts.id, "fake", nil, fake)
	p.computeAndCacheNetworkGroup()

	if opts.inbound {
		p.rw.set(inboundConn, true)
	}
	if opts.trusted {
		p.rw.set(trustedConn, true)
	}
	if opts.static {
		p.rw.set(staticDialedConn, true)
	}
	// Override created so the evictMinAge gate is testable. We set
	// the field directly because there's no public mutator.
	p.created = mclock.Now() - mclock.AbsTime(opts.createdAge)

	p.minPing.Store(opts.minPing)
	p.lastBlockRx.Store(opts.lastBlock)
	p.lastTxRx.Store(opts.lastTx)
	p.relayTxs.Store(opts.relayTxs)
	return p
}

func peerSet(peers ...*Peer) map[enode.ID]*Peer {
	out := make(map[enode.ID]*Peer, len(peers))
	for _, p := range peers {
		out[p.ID()] = p
	}
	return out
}

func contains(slice []*Peer, target *Peer) bool {
	return slices.Contains(slice, target)
}

// TestEvictionCandidatesExcludesTrusted — trustedConn peers are
// never candidates regardless of their telemetry.
func TestEvictionCandidatesExcludesTrusted(t *testing.T) {
	good := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute})
	trusted := makeEvictionPeer(t, evictionOpts{inbound: true, trusted: true, createdAge: time.Minute})

	got := evictionCandidates(peerSet(good, trusted), mclock.Now())
	if contains(got, trusted) {
		t.Fatal("trustedConn peer must not be an eviction candidate")
	}
	if !contains(got, good) {
		t.Fatal("regular inbound peer should be a candidate")
	}
}

// TestEvictionCandidatesExcludesStatic — staticDialedConn peers are
// never candidates.
func TestEvictionCandidatesExcludesStatic(t *testing.T) {
	good := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute})
	static := makeEvictionPeer(t, evictionOpts{inbound: true, static: true, createdAge: time.Minute})

	got := evictionCandidates(peerSet(good, static), mclock.Now())
	if contains(got, static) {
		t.Fatal("staticDialedConn peer must not be an eviction candidate")
	}
}

// TestEvictionCandidatesExcludesOutbound — outbound peers are never
// candidates (eviction is inbound-only; outbounds are managed by
// the dial scheduler).
func TestEvictionCandidatesExcludesOutbound(t *testing.T) {
	in := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute})
	out := makeEvictionPeer(t, evictionOpts{inbound: false, createdAge: time.Minute})

	got := evictionCandidates(peerSet(in, out), mclock.Now())
	if contains(got, out) {
		t.Fatal("outbound peer must not be an eviction candidate")
	}
	if !contains(got, in) {
		t.Fatal("inbound peer should be a candidate")
	}
}

// TestEvictionCandidatesExcludesYoungerThanMinAge — peers connected
// less than evictMinAge ago are protected.
func TestEvictionCandidatesExcludesYoungerThanMinAge(t *testing.T) {
	young := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 1 * time.Second})
	old := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute})

	got := evictionCandidates(peerSet(young, old), mclock.Now())
	if contains(got, young) {
		t.Fatal("young peer must be excluded by evictMinAge")
	}
	if !contains(got, old) {
		t.Fatal("old peer should be a candidate")
	}
}

// TestProtectByMinFastestPing — protects the n peers with the
// smallest minPing values.
func TestProtectByMinFastestPing(t *testing.T) {
	a := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, minPing: 5})
	b := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, minPing: 10})
	c := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, minPing: 15})
	candidates := []*Peer{c, b, a}

	got := protectByMin(candidates, 2, func(p *Peer) int64 { return p.minPing.Load() })
	if len(got) != 1 {
		t.Fatalf("survivors = %d, want 1", len(got))
	}
	if got[0] != c {
		t.Fatalf("survivor = peer with ping %d, want %d", got[0].minPing.Load(), c.minPing.Load())
	}
}

// TestProtectByMaxNewestBlock — protects the n peers with the
// largest lastBlockRx.
func TestProtectByMaxNewestBlock(t *testing.T) {
	a := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, lastBlock: 100})
	b := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, lastBlock: 200})
	c := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, lastBlock: 300})
	candidates := []*Peer{a, b, c}

	got := protectByMax(candidates, 2, func(p *Peer) int64 { return p.lastBlockRx.Load() })
	if len(got) != 1 {
		t.Fatalf("survivors = %d, want 1", len(got))
	}
	if got[0] != a {
		t.Fatalf("survivor = peer with block %d, want %d", got[0].lastBlockRx.Load(), a.lastBlockRx.Load())
	}
}

// TestProtectByMaxConditionalSkipsNonRelayers — protectByMax with
// "relayTxs only" condition: peers that don't relay tx remain in
// the candidate pool regardless of their lastTxRx.
func TestProtectByMaxConditionalSkipsNonRelayers(t *testing.T) {
	relayer := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, lastTx: 100, relayTxs: true})
	nonrelayer := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, lastTx: 999, relayTxs: false})
	candidates := []*Peer{relayer, nonrelayer}

	got := protectByMaxConditional(candidates, 1,
		func(p *Peer) int64 { return p.lastTxRx.Load() },
		func(p *Peer) bool { return p.relayTxs.Load() })

	// Relayer (top tx-relayer) is protected; non-relayer survives in
	// the pool even though its lastTx is higher (they're not
	// eligible for THIS protection round).
	if contains(got, relayer) {
		t.Fatal("relayer should be protected, but is in survivors")
	}
	if !contains(got, nonrelayer) {
		t.Fatal("non-relayer should survive (not eligible for this round)")
	}
}

// TestProtectByNetGroupKeyedDistinctGroups — peers in distinct
// groups are preferred over peers sharing groups.
func TestProtectByNetGroupKeyedDistinctGroups(t *testing.T) {
	// Three groups: A x 1, B x 2, C x 1. n=2 should protect one
	// from A and one from C (the two singletons), leaving both Bs
	// in the candidate pool.
	a := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, ip: net.IPv4(10, 0, 0, 1)})
	b1 := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, ip: net.IPv4(11, 0, 0, 1)})
	b2 := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, ip: net.IPv4(11, 0, 0, 2)})
	c := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: time.Minute, ip: net.IPv4(12, 0, 0, 1)})
	candidates := []*Peer{a, b1, b2, c}

	got := protectByNetGroupKeyed(candidates, 2)
	if len(got) != 2 {
		t.Fatalf("survivors = %d, want 2", len(got))
	}
	// b1 and b2 share group → both should remain.
	if !contains(got, b1) || !contains(got, b2) {
		t.Fatalf("expected both Bs in survivors, got %v", got)
	}
}

// TestProtectByRatioProtectsHalfOldest — exactly half the candidate
// pool (rounded down) is protected; the protected set is the
// oldest by Created().
func TestProtectByRatioProtectsHalfOldest(t *testing.T) {
	// Younger first so the array order doesn't accidentally pass.
	young := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 31 * time.Second})
	mid := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 60 * time.Second})
	older := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 90 * time.Second})
	oldest := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 120 * time.Second})
	candidates := []*Peer{young, mid, older, oldest}

	got := protectByRatio(candidates)
	if len(got) != 2 {
		t.Fatalf("survivors = %d, want 2 (half of 4)", len(got))
	}
	if contains(got, oldest) || contains(got, older) {
		t.Fatalf("oldest two should be protected, got survivors %v", got)
	}
	if !contains(got, young) || !contains(got, mid) {
		t.Fatalf("youngest two should survive, got %v", got)
	}
}

// TestPickEvictionVictimYoungestInLargestGroup — final pick rule.
func TestPickEvictionVictimYoungestInLargestGroup(t *testing.T) {
	// Group A has 1 member; group B has 3 members. Victim must
	// come from group B; among B, the youngest.
	a := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 100 * time.Second, ip: net.IPv4(10, 0, 0, 1)})
	b1 := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 90 * time.Second, ip: net.IPv4(20, 0, 0, 1)})
	b2 := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 60 * time.Second, ip: net.IPv4(20, 0, 0, 2)})
	b3 := makeEvictionPeer(t, evictionOpts{inbound: true, createdAge: 30 * time.Second, ip: net.IPv4(20, 0, 0, 3)})
	candidates := []*Peer{a, b1, b2, b3}

	victim := pickEvictionVictim(candidates)
	if victim != b3 {
		t.Fatalf("victim = %v, want b3 (youngest in largest group)", victim.ID())
	}
}

// TestPickEvictionVictimEmptyReturnsNil — no candidates → nil.
func TestPickEvictionVictimEmptyReturnsNil(t *testing.T) {
	if v := pickEvictionVictim(nil); v != nil {
		t.Fatalf("victim = %v, want nil for empty input", v)
	}
}

// TestEvictInboundFullPipelineWithLargePool — with enough
// candidates to survive every protection round (28+ in the
// default constants), evictInbound succeeds and returns true.
// Asserts the pipeline composes correctly end-to-end.
func TestEvictInboundFullPipelineWithLargePool(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}

	// 40 inbound peers: 30 in distinct singleton groups + 10 in
	// one big group. After all protection rounds at least one
	// candidate from the big group will survive.
	peers := map[enode.ID]*Peer{}
	for i := range 30 {
		p := makeEvictionPeer(t, evictionOpts{
			inbound: true, createdAge: time.Duration(60+i) * time.Second,
			ip: net.IPv4(byte(10+i), 0, 0, 1), minPing: int64(i + 1),
			lastBlock: int64(1000 + i), relayTxs: true,
		})
		peers[p.ID()] = p
	}
	for i := range 10 {
		p := makeEvictionPeer(t, evictionOpts{
			inbound: true, createdAge: time.Duration(45+i) * time.Second,
			ip: net.IPv4(192, 168, 0, byte(i)), minPing: int64(500 + i),
			lastBlock: int64(50 + i), relayTxs: true,
		})
		peers[p.ID()] = p
	}

	if !srv.evictInbound(peers) {
		t.Fatal("evictInbound returned false despite a 40-peer pool; protection rounds over-applied")
	}
}

// TestEvictInboundReturnsFalseWhenNoCandidates — empty pool → no-op.
func TestEvictInboundReturnsFalseWhenNoCandidates(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	if srv.evictInbound(map[enode.ID]*Peer{}) {
		t.Fatal("evictInbound returned true on empty map")
	}
}

// TestEvictInboundReturnsFalseWhenEverythingProtected — when all
// candidates are protected by the various rounds, eviction is a
// no-op.
func TestEvictInboundReturnsFalseWhenEverythingProtected(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}

	// Add only trusted/static peers — eviction should find no
	// candidates and return false.
	peers := peerSet(
		makeEvictionPeer(t, evictionOpts{inbound: true, trusted: true, createdAge: time.Minute}),
		makeEvictionPeer(t, evictionOpts{inbound: true, static: true, createdAge: time.Minute}),
	)
	if srv.evictInbound(peers) {
		t.Fatal("evictInbound returned true with only trusted/static peers")
	}
}

// TestPostHandshakeChecksTriggersEviction — when the inbound pool
// is saturated, postHandshakeChecks triggers evictInbound instead
// of hard-rejecting, and accepts the new peer optimistically when
// eviction succeeds.
func TestPostHandshakeChecksTriggersEviction(t *testing.T) {
	srv := &Server{
		log:    testlog.Logger(t, logging.LvlTrace),
		Config: Config{MaxPeers: 50, NoDiscovery: true, NoDial: true},
	}

	// Build 40 inbound candidates so the eviction pipeline can
	// land on a victim.
	peers := map[enode.ID]*Peer{}
	for i := range 40 {
		p := makeEvictionPeer(t, evictionOpts{
			inbound:    true,
			createdAge: time.Duration(60+i) * time.Second,
			ip:         net.IPv4(byte(10+i), 0, 0, 1),
			minPing:    int64(i + 1),
			lastBlock:  int64(1000 + i),
			relayTxs:   true,
		})
		peers[p.ID()] = p
	}

	// Set localnode so the c.node.ID() == localnode.ID() comparison
	// has a valid lhs. We don't need an actual local key; just a
	// non-nil localnode.
	if err := srv.initHelloNonce(); err != nil {
		t.Fatal(err)
	}

	// Synthesize a new inbound conn that triggers saturation.
	// maxInboundConns by default returns MaxPeers - dialedConns
	// = 50 - 16 (default DialRatio=3, so 16 outbound). But the
	// test config explicitly drives saturation by passing a high
	// inboundCount.
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: net.IPv4(99, 99, 99, 99), Port: 32110}}
	defer fake.Close()
	newConn := &conn{
		fd:        fake,
		transport: &v2Transport{},
		node:      enode.SignNull(new(enr.Record), randomID()),
		flags:     inboundConn,
	}

	err := srv.postHandshakeChecks(peers, srv.maxInboundConns(), newConn)
	if err != nil {
		t.Fatalf("postHandshakeChecks returned %v; expected nil after successful eviction", err)
	}
}

// TestNodeNetworkGroupKeyExemptsLoopback — loopback / link-local
// addresses bypass the diversity gate so dev-loopback tests don't
// degrade to one peer total.
func TestNodeNetworkGroupKeyExemptsLoopback(t *testing.T) {
	if k := ipNetworkGroupKey(net.ParseIP("127.0.0.1")); k != "" {
		t.Errorf("loopback yielded non-empty key %q", k)
	}
	if k := ipNetworkGroupKey(net.ParseIP("169.254.1.1")); k != "" {
		t.Errorf("link-local yielded non-empty key %q", k)
	}
	if k := ipNetworkGroupKey(net.IPv4(8, 8, 8, 8)); k == "" {
		t.Error("public IPv4 yielded empty key")
	}
}

// TestPostHandshakeChecksHardRejectsWhenEvictionFails — when the
// inbound pool is saturated AND every peer is protected from
// eviction (trusted/static), postHandshakeChecks falls through to
// DiscTooManyPeers.
func TestPostHandshakeChecksHardRejectsWhenEvictionFails(t *testing.T) {
	srv := &Server{
		log:    testlog.Logger(t, logging.LvlTrace),
		Config: Config{MaxPeers: 4, NoDiscovery: true, NoDial: true},
	}
	if err := srv.initHelloNonce(); err != nil {
		t.Fatal(err)
	}

	// Two trusted inbound peers — neither is evictable.
	peers := peerSet(
		makeEvictionPeer(t, evictionOpts{inbound: true, trusted: true, createdAge: time.Minute}),
		makeEvictionPeer(t, evictionOpts{inbound: true, trusted: true, createdAge: time.Minute}),
	)

	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: net.IPv4(99, 99, 99, 99), Port: 32110}}
	defer fake.Close()
	newConn := &conn{
		fd:        fake,
		transport: &v2Transport{},
		node:      enode.SignNull(new(enr.Record), randomID()),
		flags:     inboundConn,
	}

	// Pass inboundCount >= maxInboundConns to force the saturation
	// branch.
	err := srv.postHandshakeChecks(peers, srv.maxInboundConns(), newConn)
	if err != DiscTooManyPeers {
		t.Fatalf("postHandshakeChecks = %v, want DiscTooManyPeers", err)
	}
}
