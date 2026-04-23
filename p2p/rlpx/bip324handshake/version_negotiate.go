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

package bip324handshake

import (
	"bytes"
	"io"
	"net"
	"sync"
)

// Variant is the inferred handshake type of an inbound connection,
// returned by PeekVersion.
type Variant int

const (
	// VariantUnknown — the first byte didn't match any known magic.
	// Caller should disconnect; no partial-handshake state is leaked.
	VariantUnknown Variant = iota
	// VariantLegacy — the first byte is inside the legacy RLPx v4
	// ECIES prefix range. Caller should replay the byte via the
	// returned PeekedConn and hand it to the legacy Handshake path.
	VariantLegacy
	// VariantV2 — the first byte matched VersionMagic (0xA0). The
	// byte has been consumed; caller should wrap the PeekedConn in
	// bip324handshake.NewConn and call AcceptHandshake.
	VariantV2
)

// PeekVersion reads exactly one byte from conn to choose a handshake
// variant, then returns a wrapper that replays the byte if the variant
// is VariantLegacy. The wrapper is safe for concurrent use from one
// reader + one writer.
//
// Callers should set a read deadline before calling; a hostile client
// that never sends any bytes would otherwise hold the goroutine
// indefinitely.
func PeekVersion(conn net.Conn) (Variant, *PeekedConn, error) {
	var b [1]byte
	if _, err := io.ReadFull(conn, b[:]); err != nil {
		return VariantUnknown, &PeekedConn{Conn: conn}, err
	}
	switch {
	case b[0] == VersionMagic:
		// v2: byte is version tag, not payload. Consume it.
		return VariantV2, &PeekedConn{Conn: conn}, nil
	case isLegacyRLPxFirstByte(b[0]):
		// Legacy: byte is part of the ECIES auth packet. Replay.
		return VariantLegacy, &PeekedConn{Conn: conn, prefix: []byte{b[0]}}, nil
	}
	return VariantUnknown, &PeekedConn{Conn: conn, prefix: []byte{b[0]}}, nil
}

// isLegacyRLPxFirstByte reports whether b is a plausible first byte of
// a legacy RLPx v4 ECIES auth packet. The packet is RLP-encoded, so
// the first byte is an RLP list-length prefix. Legacy auth packets
// fall into the 307-byte range (v4 plaintext after encryption headroom),
// which makes the first byte begin with 0xf9 followed by a two-byte
// length. The bytes 0xf9..0xfa cover the relevant size range.
//
// VersionMagic (0xA0) is outside this range, so the two are disjoint.
// If ever a future legacy format lands in the 0xA0 range, the
// dispatcher must be revisited before the conflicting byte is
// accepted.
func isLegacyRLPxFirstByte(b byte) bool {
	return b == 0xf8 || b == 0xf9 || b == 0xfa
}

// PeekedConn is a net.Conn view that replays a small buffer of bytes
// read during version negotiation. Only the read side is wrapped; all
// other methods forward directly to the underlying Conn.
type PeekedConn struct {
	net.Conn
	mu     sync.Mutex
	prefix []byte
}

// Read returns replayed bytes first, then falls through to the
// underlying connection. The replay buffer is drained in first-in
// order and then discarded.
func (p *PeekedConn) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// UnreadLen reports how many bytes are still in the replay buffer.
// Test-only; not part of the public net.Conn surface.
func (p *PeekedConn) UnreadLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prefix)
}

// compile-time check: PeekedConn satisfies the net.Conn interface.
var _ net.Conn = (*PeekedConn)(nil)

// bytesLegacyMagics is referenced by tests to confirm the
// dispatcher's legacy range stays in sync with the RLPx v4 format.
// Exposed through a helper to keep the internal slice out of the API.
func bytesLegacyMagics() [][]byte {
	return [][]byte{{0xf8}, {0xf9}, {0xfa}}
}

var _ = bytes.Equal // retain "bytes" import if future dispatch grows richer
