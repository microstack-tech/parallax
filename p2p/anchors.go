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
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// MaxBlockRelayAnchors is the cap on the number of (IP, listen-port)
// entries persisted to / replayed from anchors.dat. Mirrors Bitcoin
// Core's MAX_BLOCK_RELAY_ONLY_ANCHORS = 2 (src/net.cpp:57).
const MaxBlockRelayAnchors = 2

// anchorSchemaV1 marks anchors.dat written by this codebase. Bumping
// this is a wire-incompatible change to the file format; older
// binaries can't read newer files.
const anchorSchemaV1 byte = 0x01

// anchorEntry is the on-disk shape of one anchor (BIP155
// network-id + raw addr bytes + TCP port). Matches the structure
// of disc.PeerEntry minus the parallax-protocol-only KeyType /
// NodeID / LastSeen fields — anchors are pure (address, port)
// tuples. The format was BIP155-shaped from v1, so Tor v3 anchors
// (PIP-0007 §4) need no schema bump: a pre-Tor binary reading a
// file with an onion row skips the unrecognized network id and
// keeps the rest.
//
// The Tail field reserves forward-compat space for new fields
// without bumping the schema byte.
type anchorEntry struct {
	Network uint8
	Addr    []byte
	Port    uint16
	Tail    []rlp.RawValue `rlp:"tail"`
}

// anchorsBody is the top-level RLP shape — a tagged list with a
// future-extensible tail. List capacity is unbounded by RLP but
// we cap reads at MaxBlockRelayAnchors to defend against a
// crafted file from a downgraded / hostile binary.
type anchorsBody struct {
	Entries []anchorEntry
	Tail    []rlp.RawValue `rlp:"tail"`
}

// errAnchorFutureSchema is returned from loadAnchors when the
// file's schema byte is newer than this binary recognizes.
// Caller logs and continues with no anchor replay.
var errAnchorFutureSchema = errors.New("anchors: file schema newer than this binary")

// saveAnchors atomically writes the supplied (IP, port) list to
// path. Atomicity: write to `path.tmp`, rename. RENAME is atomic
// on POSIX and NTFS, so a crash mid-write either leaves the old
// file untouched or writes the full new one — never a partial.
//
// Caller is the Server.Stop path; it builds the entry list from
// currently-connected block-relay-only outbound peers' effective
// listen-addrs, capped at MaxBlockRelayAnchors.
func saveAnchors(path string, addrs []addrman.NetAddr) error {
	if len(addrs) > MaxBlockRelayAnchors {
		addrs = addrs[:MaxBlockRelayAnchors]
	}
	body := anchorsBody{Entries: make([]anchorEntry, 0, len(addrs))}
	for _, a := range addrs {
		if len(a.Bytes()) == 0 || a.Port == 0 {
			continue
		}
		body.Entries = append(body.Entries, anchorEntry{
			Network: uint8(a.Network),
			Addr:    append([]byte(nil), a.Bytes()...),
			Port:    a.Port,
		})
	}
	if len(body.Entries) == 0 {
		// No anchors to persist — remove any pre-existing file so
		// the next startup doesn't replay stale entries from a
		// previous run.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("anchors: remove stale %s: %w", path, err)
		}
		return nil
	}
	buf := bytes.NewBuffer(make([]byte, 0, 256))
	buf.WriteByte(anchorSchemaV1)
	if err := rlp.Encode(buf, &body); err != nil {
		return fmt.Errorf("anchors: rlp encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("anchors: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := writeFileSync(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("anchors: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("anchors: rename: %w", err)
	}
	return nil
}

// writeFileSync is os.WriteFile plus an fsync before close. Without
// the sync, a crash shortly after the rename can leave an empty or
// truncated file at the final path on journaled filesystems — the
// rename is durable before the data is.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// loadAnchors reads anchors.dat and returns the persisted (IP,
// port) list. Returns (nil, nil) when the file does not exist —
// the common case on a fresh install.
//
// Bitcoin Core deletes the file immediately after reading
// (src/net.cpp:2715-2716) so a crash mid-startup doesn't replay
// the same potentially-malicious anchors twice. We mirror that
// here via removeAnchors.
func loadAnchors(path string) ([]addrman.NetAddr, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("anchors: read %s: %w", path, err)
	}
	if len(raw) < 1 {
		return nil, fmt.Errorf("anchors: file too short")
	}
	version := raw[0]
	if version > anchorSchemaV1 {
		return nil, errAnchorFutureSchema
	}
	if version != anchorSchemaV1 {
		return nil, fmt.Errorf("anchors: unsupported schema 0x%02x", version)
	}
	var body anchorsBody
	if err := rlp.DecodeBytes(raw[1:], &body); err != nil {
		return nil, fmt.Errorf("anchors: decode body: %w", err)
	}
	if len(body.Entries) > MaxBlockRelayAnchors {
		// Defense against a crafted file. Real anchors files have
		// at most MaxBlockRelayAnchors entries; trim the rest.
		body.Entries = body.Entries[:MaxBlockRelayAnchors]
	}
	out := make([]addrman.NetAddr, 0, len(body.Entries))
	for _, e := range body.Entries {
		// Unknown network ids and length mismatches are skipped, not
		// errors — that's what lets a pre-Tor binary read a file
		// carrying onion rows, and this binary read files from a
		// future one.
		na, err := addrman.NewNetAddr(addrman.NetID(e.Network), e.Addr, e.Port)
		if err != nil || na.Port == 0 {
			continue
		}
		out = append(out, na)
	}
	return out, nil
}

// removeAnchors deletes the anchors file. Called immediately after
// a successful load so a crash during startup doesn't replay the
// same anchors (mirrors src/net.cpp post-read delete).
func removeAnchors(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("anchors: remove %s: %w", path, err)
	}
	return nil
}

