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
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/internal/testlog"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
)

// anchorNA converts an (ip, port) pair into the NetAddr form the
// anchors API takes.
func anchorNA(t *testing.T, ip net.IP, port uint16) addrman.NetAddr {
	t.Helper()
	na, ok := netAddrFromTCP(&net.TCPAddr{IP: ip, Port: int(port)})
	if !ok {
		t.Fatalf("bad anchor ip %v", ip)
	}
	return na
}

// TestAnchorsRoundTripIPv4 — write 2 IPv4 (IP, port) entries to
// anchors.dat, read them back, verify exact equality. Pin the
// schema-version byte so a downgraded binary won't silently accept
// a future-schema file.
func TestAnchorsRoundTripIPv4(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	in := []addrman.NetAddr{
		anchorNA(t, net.IPv4(192, 0, 2, 17), 32110),
		anchorNA(t, net.IPv4(198, 51, 100, 9), 32111),
	}
	if err := saveAnchors(path, in); err != nil {
		t.Fatalf("saveAnchors: %v", err)
	}
	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("loaded %d entries, want %d", len(got), len(in))
	}
	for i := range in {
		if !got[i].Equal(in[i]) {
			t.Errorf("entry %d: got %v, want %v", i, got[i], in[i])
		}
	}
}

// TestAnchorsRoundTripIPv6 — same round trip for IPv6.
func TestAnchorsRoundTripIPv6(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	v6 := net.ParseIP("2001:db8::42").To16()
	in := []addrman.NetAddr{anchorNA(t, v6, 32110)}
	if err := saveAnchors(path, in); err != nil {
		t.Fatalf("saveAnchors: %v", err)
	}
	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d entries, want 1", len(got))
	}
	if !got[0].Equal(anchorNA(t, v6, 32110)) {
		t.Errorf("entry: got %v, want %v:%d", got[0], v6, 32110)
	}
}

// TestAnchorsCappedAtMax — saveAnchors trims input above
// MaxBlockRelayAnchors. Bitcoin Core's MAX_BLOCK_RELAY_ONLY_ANCHORS
// = 2 (src/net.cpp:57). A bug that lets the cap leak (unbounded
// anchor list) is a startup-amp DoS vector.
func TestAnchorsCappedAtMax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	in := []addrman.NetAddr{
		anchorNA(t, net.IPv4(1, 0, 0, 1), 32110),
		anchorNA(t, net.IPv4(2, 0, 0, 1), 32110),
		anchorNA(t, net.IPv4(3, 0, 0, 1), 32110),
		anchorNA(t, net.IPv4(4, 0, 0, 1), 32110),
	}
	if err := saveAnchors(path, in); err != nil {
		t.Fatalf("saveAnchors: %v", err)
	}
	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != MaxBlockRelayAnchors {
		t.Fatalf("loaded %d entries, want %d (cap)", len(got), MaxBlockRelayAnchors)
	}
}

// TestAnchorsLoadMissingFileNoError — fresh-install path: file
// does not exist. loadAnchors must return (nil, nil), not an
// error. The Server startup path treats any error as "skip
// replay", so an error here would still be safe — but quiet
// startup is preferable.
func TestAnchorsLoadMissingFileNoError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing-anchors.dat")
	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors on missing file returned err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice, got %v", got)
	}
}

// TestAnchorsSaveEmptyRemovesFile — saveAnchors with an empty
// list deletes any pre-existing file so the next startup doesn't
// replay stale entries from a previous run. Important for the
// "I had 2 BR peers, then ran without BR for a while, then turned
// BR back on" sequence.
func TestAnchorsSaveEmptyRemovesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	// First save 1 entry.
	if err := saveAnchors(path, []addrman.NetAddr{anchorNA(t, net.IPv4(1, 2, 3, 4), 32110)}); err != nil {
		t.Fatalf("first saveAnchors: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after non-empty save: %v", err)
	}

	// Then save empty.
	if err := saveAnchors(path, nil); err != nil {
		t.Fatalf("empty saveAnchors: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should be removed after empty save; stat err: %v", err)
	}
}

// TestPersistAnchorsSnapshotsBlockRelayPeers — persistAnchors must
// write the (IP, port) of the currently-connected block-relay-only
// outbound peers it is given, and leave full-relay peers out.
// Regression: persistAnchors used to read srv.Peers(), which
// returns nil once the run loop has closed quit, so every shutdown
// wrote an empty file (and thereby deleted anchors.dat). It now
// takes the live peer set captured during spindown.
func TestPersistAnchorsSnapshotsBlockRelayPeers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")
	srv := &Server{Config: Config{AnchorsPath: path}}
	srv.log = testlog.Logger(t, logging.LvlCrit)

	// One block-relay-only outbound peer (should be persisted) and
	// one ordinary outbound peer (should not).
	br := newOutboundPeerAt(t, net.IPv4(203, 0, 113, 7), 32110)
	br.SetBlockRelayOnly(true)
	full := newOutboundPeerAt(t, net.IPv4(198, 51, 100, 4), 32110)

	srv.persistAnchors(peerSet(br, full))

	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("persisted %d anchors, want 1 (only the block-relay peer)", len(got))
	}
	if !got[0].Equal(anchorNA(t, net.IPv4(203, 0, 113, 7), 32110)) {
		t.Fatalf("persisted anchor = %v, want 203.0.113.7:32110", got[0])
	}
}

// TestPersistAnchorsEmptyPeerSetLeavesNoFile — with no block-relay
// peers there is nothing to persist; an existing anchors.dat is
// removed so a crash-restart doesn't replay stale anchors. (This is
// the branch that silently ran on EVERY shutdown before the fix.)
func TestPersistAnchorsEmptyPeerSetLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")
	if err := saveAnchors(path, []addrman.NetAddr{anchorNA(t, net.IPv4(1, 2, 3, 4), 32110)}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Config: Config{AnchorsPath: path}}
	srv.log = testlog.Logger(t, logging.LvlCrit)

	srv.persistAnchors(map[enode.ID]*Peer{})

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchors.dat should be removed when no block-relay peers; stat err: %v", err)
	}
}

// newOutboundPeerAt builds a synthetic outbound peer whose remote
// address is (ip, port). peerListenAddr returns the remote addr
// directly for outbound peers, so this is enough for persistAnchors.
func newOutboundPeerAt(t *testing.T, ip net.IP, port int) *Peer {
	t.Helper()
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: ip, Port: port}}
	t.Cleanup(func() { _ = fake.Close() })
	p := NewPeerForTest(randomID(), "anchor-test", nil, fake)
	// Outbound (dynDialedConn): peerListenAddr uses RemoteAddr directly.
	p.rw.set(dynDialedConn, true)
	return p
}

// TestAnchorsOnionRoundTrip — Tor v3 anchors persist and reload
// (PIP-0007 §4). The file format was BIP155-shaped from v1, so this
// needs no schema bump; pre-Tor binaries skip the onion row.
func TestAnchorsOnionRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "anchors.dat")

	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	in := []addrman.NetAddr{onion, anchorNA(t, net.IPv4(203, 0, 113, 7), 32110)}
	if err := saveAnchors(path, in); err != nil {
		t.Fatalf("saveAnchors: %v", err)
	}
	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != 2 || !got[0].Equal(onion) || !got[1].Equal(in[1]) {
		t.Fatalf("round trip: got %v, want %v", got, in)
	}
}

// TestPersistAnchorsUsesDialedTarget — a proxied or onion block-relay
// peer's anchor is its dialed target, never the socket's RemoteAddr
// (which is the SOCKS5 proxy).
func TestPersistAnchorsUsesDialedTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.dat")
	srv := &Server{Config: Config{AnchorsPath: path}}
	srv.log = testlog.Logger(t, logging.LvlCrit)

	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	// RemoteAddr says "the proxy"; dialedTarget says the onion.
	br := newOutboundPeerAt(t, net.IPv4(127, 0, 0, 1), 9050)
	br.SetBlockRelayOnly(true)
	br.rw.setDialTarget(onion)

	srv.persistAnchors(peerSet(br))

	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != 1 || !got[0].Equal(onion) {
		t.Fatalf("persisted %v, want the onion dialed target", got)
	}
}

// TestAnchorsRemoveAnchors — removeAnchors deletes the file when
// it exists, returns nil when it doesn't.
func TestAnchorsRemoveAnchors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	// Missing-file case: nil error.
	if err := removeAnchors(path); err != nil {
		t.Fatalf("removeAnchors on missing file: %v", err)
	}
	// Existing-file case: file is deleted.
	if err := saveAnchors(path, []addrman.NetAddr{anchorNA(t, net.IPv4(5, 6, 7, 8), 32110)}); err != nil {
		t.Fatalf("saveAnchors: %v", err)
	}
	if err := removeAnchors(path); err != nil {
		t.Fatalf("removeAnchors: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should be deleted; stat err: %v", err)
	}
}

// TestAnchorsRejectsFutureSchema — a file written with a schema
// byte newer than this binary recognizes returns
// errAnchorFutureSchema. Caller (Server.replayAnchors) logs and
// continues without replay rather than crashing.
func TestAnchorsRejectsFutureSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	// Hand-craft a file with schema = anchorSchemaV1 + 1 (a hypothetical v2).
	bad := []byte{anchorSchemaV1 + 1, 0xc0} // 0xc0 = empty RLP list.
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := loadAnchors(path); !errors.Is(err, errAnchorFutureSchema) {
		t.Fatalf("expected errAnchorFutureSchema, got %v", err)
	}
}

// TestAnchorsSkipsZeroPortEntries — entries with port=0 or
// nil-IP are dropped silently in saveAnchors. They have no useful
// dial target. Caller is expected to pre-trim above
// MaxBlockRelayAnchors (Server.persistAnchors does), so this test
// stays at MaxBlockRelayAnchors entries to avoid colliding with
// the cap-trim path.
func TestAnchorsSkipsZeroPortEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	in := []addrman.NetAddr{
		anchorNA(t, net.IPv4(5, 6, 7, 8), 1234), // keep
		{},                                      // zero value → drop
	}
	if err := saveAnchors(path, in); err != nil {
		t.Fatalf("saveAnchors: %v", err)
	}
	got, err := loadAnchors(path)
	if err != nil {
		t.Fatalf("loadAnchors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d entries, want 1 (nil filtered)", len(got))
	}
	if got[0].Port != 1234 {
		t.Errorf("entry: port=%d, want 1234", got[0].Port)
	}

	// Same idea, port=0 case:
	path2 := filepath.Join(dir, "anchors2.dat")
	in2 := []addrman.NetAddr{
		anchorNA(t, net.IPv4(9, 8, 7, 6), 32110), // keep
		anchorNA(t, net.IPv4(1, 2, 3, 4), 0),     // zero port → drop
	}
	if err := saveAnchors(path2, in2); err != nil {
		t.Fatalf("saveAnchors2: %v", err)
	}
	got2, err := loadAnchors(path2)
	if err != nil {
		t.Fatalf("loadAnchors2: %v", err)
	}
	if len(got2) != 1 || got2[0].Port != 32110 {
		t.Fatalf("port=0 not filtered: got %+v", got2)
	}
}
