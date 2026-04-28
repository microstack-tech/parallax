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
)

// TestAnchorsRoundTripIPv4 — write 2 IPv4 (IP, port) entries to
// anchors.dat, read them back, verify exact equality. Pin the
// schema-version byte so a downgraded binary won't silently accept
// a future-schema file.
func TestAnchorsRoundTripIPv4(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.dat")

	in := []*net.TCPAddr{
		{IP: net.IPv4(192, 0, 2, 17), Port: 32110},
		{IP: net.IPv4(198, 51, 100, 9), Port: 32111},
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
		if !got[i].IP.Equal(in[i].IP) || got[i].Port != in[i].Port {
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
	in := []*net.TCPAddr{{IP: v6, Port: 32110}}
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
	if !got[0].IP.Equal(v6) || got[0].Port != 32110 {
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

	in := []*net.TCPAddr{
		{IP: net.IPv4(1, 0, 0, 1), Port: 32110},
		{IP: net.IPv4(2, 0, 0, 1), Port: 32110},
		{IP: net.IPv4(3, 0, 0, 1), Port: 32110},
		{IP: net.IPv4(4, 0, 0, 1), Port: 32110},
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
	if err := saveAnchors(path, []*net.TCPAddr{{IP: net.IPv4(1, 2, 3, 4), Port: 32110}}); err != nil {
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
	if err := saveAnchors(path, []*net.TCPAddr{{IP: net.IPv4(5, 6, 7, 8), Port: 32110}}); err != nil {
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

	in := []*net.TCPAddr{
		{IP: net.IPv4(5, 6, 7, 8), Port: 1234}, // keep
		{IP: nil, Port: 32110},                  // nil ip → drop
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
	in2 := []*net.TCPAddr{
		{IP: net.IPv4(9, 8, 7, 6), Port: 32110}, // keep
		{IP: net.IPv4(1, 2, 3, 4), Port: 0},     // zero port → drop
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
