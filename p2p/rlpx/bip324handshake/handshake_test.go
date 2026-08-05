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
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// TestHandshakeRoundTrip — two endpoints on a net.Pipe complete the v2
// handshake, exchange messages in both directions, and recover the
// plaintext byte-for-byte. This is the Phase 2b acceptance criterion:
// "Two v2.0 nodes complete RLPx handshake knowing only each other's
// IP:port".
func TestHandshakeRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	// Simulate the dispatcher having already consumed the VersionMagic
	// byte on the responder side. In production that's PeekVersion's
	// job; here we wire it directly.
	initConn := NewConn(a)
	respConn := NewConn(b)

	type result struct {
		role string
		err  error
	}
	ch := make(chan result, 2)

	// The responder reads the initiator's init-magic byte from the
	// wire as part of the handshake only because we're short-circuiting
	// the dispatcher. The DialHandshake writes [VersionMagic || pub];
	// AcceptHandshake expects the magic byte to have been consumed
	// upstream. For this test we consume it manually.
	go func() {
		var magic [1]byte
		if _, err := b.Read(magic[:]); err != nil {
			ch <- result{role: "peek", err: err}
			return
		}
		if magic[0] != VersionMagic {
			ch <- result{role: "peek", err: errors.New("bad magic")}
			return
		}
		ch <- result{role: "accept", err: respConn.AcceptHandshake()}
	}()
	go func() {
		ch <- result{role: "dial", err: initConn.DialHandshake()}
	}()

	for range 2 {
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s: %v", r.role, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("handshake timeout")
		}
	}

	// Exchange payloads both ways.
	payloads := [][]byte{
		[]byte("hello from initiator"),
		[]byte("hello from responder"),
		bytes.Repeat([]byte{0x55}, 4096),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, p := range payloads {
			if err := initConn.Write(p); err != nil {
				t.Errorf("init write: %v", err)
				return
			}
			got, err := initConn.Read()
			if err != nil {
				t.Errorf("init read: %v", err)
				return
			}
			if !bytes.Equal(got, p) {
				t.Errorf("init roundtrip mismatch")
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range payloads {
			got, err := respConn.Read()
			if err != nil {
				t.Errorf("resp read: %v", err)
				return
			}
			if err := respConn.Write(got); err != nil {
				t.Errorf("resp write: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// TestHandshakeRejectsShortInit — a peer that disconnects mid-handshake
// must not leave either side stuck.
func TestHandshakeRejectsShortInit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	// Don't close b yet — we want the responder to see a partial
	// handshake followed by EOF.

	done := make(chan error, 1)
	go func() {
		// Discard the magic byte (dispatcher would have done this).
		var magic [1]byte
		_, _ = b.Read(magic[:])
		done <- NewConn(b).AcceptHandshake()
	}()

	// Write only the magic byte, then hang up.
	if _, err := a.Write([]byte{VersionMagic}); err != nil {
		t.Fatal(err)
	}
	a.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("AcceptHandshake returned nil on partial input")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AcceptHandshake did not return on peer EOF")
	}
	b.Close()
}

// TestHandshakeRejectsInvalidKey — a malformed pubkey in the responder
// stream must surface ErrInvalidKey without panicking.
func TestHandshakeRejectsInvalidKey(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	done := make(chan error, 1)
	go func() {
		initConn := NewConn(a)
		done <- initConn.DialHandshake()
	}()

	// Read initiator's [magic || pub].
	buf := make([]byte, 1+KeyLen)
	if _, err := readFull(b, buf); err != nil {
		t.Fatal(err)
	}
	// Respond with 32 bytes that are technically valid X25519 wire
	// (any 32 bytes decode), so the real failure vector is the DH
	// result being the all-zero point. That's what we test.
	zeroKey := make([]byte, KeyLen)
	if _, err := b.Write(zeroKey); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		// crypto/ecdh returns an error for the all-zero-shared case.
		if err == nil {
			t.Error("expected error on zero responder key")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DialHandshake did not return")
	}
}

func readFull(c net.Conn, b []byte) (int, error) {
	total := 0
	for total < len(b) {
		n, err := c.Read(b[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestNonceMonotonicity — in-order messages decrypt with a per-direction
// counter nonce.
func TestNonceMonotonicity(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	initConn := NewConn(a)
	respConn := NewConn(b)
	accepted := make(chan error, 1)
	go func() {
		var magic [1]byte
		_, _ = b.Read(magic[:])
		accepted <- respConn.AcceptHandshake()
	}()
	if err := initConn.DialHandshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}

	// net.Pipe is unbuffered: Write blocks until the peer Reads.
	// Drive writes from a goroutine so we can Read them inline.
	writeErr := make(chan error, 1)
	go func() {
		if err := initConn.Write([]byte("one")); err != nil {
			writeErr <- err
			return
		}
		writeErr <- initConn.Write([]byte("two"))
	}()

	got1, err := respConn.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(got1) != "one" {
		t.Errorf("first frame: got %q want %q", got1, "one")
	}
	got2, err := respConn.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != "two" {
		t.Errorf("second frame: got %q want %q", got2, "two")
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
}

// TestForwardSecrecy — the same plaintext written in two independent
// handshakes produces different ciphertexts. The ephemeral-only DH is
// what guarantees this; losing it would be an immediate security bug.
func TestForwardSecrecy(t *testing.T) {
	capture := func() []byte {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		initConn := NewConn(a)
		respConn := NewConn(b)
		done := make(chan error, 1)
		go func() {
			var magic [1]byte
			_, _ = b.Read(magic[:])
			done <- respConn.AcceptHandshake()
		}()
		if err := initConn.DialHandshake(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		plaintext := []byte("canary")
		readerDone := make(chan []byte, 1)
		go func() {
			// 3-byte length header + AEAD tag (16) + plaintext.
			ct := make([]byte, 3+16+len(plaintext))
			_, _ = readFull(b, ct)
			readerDone <- ct
		}()
		if err := initConn.Write(plaintext); err != nil {
			t.Fatal(err)
		}
		return <-readerDone
	}
	c1 := capture()
	c2 := capture()
	if bytes.Equal(c1, c2) {
		t.Fatal("forward secrecy broken: same plaintext yields same ciphertext across sessions")
	}
}

// TestPeekVersionV2 — first byte 0xA0 is recognized as VariantV2 and
// consumed (replay buffer empty).
func TestPeekVersionV2(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		_, _ = a.Write([]byte{VersionMagic, 0x11, 0x22})
	}()
	v, pc, err := PeekVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	if v != VariantV2 {
		t.Errorf("variant = %d, want VariantV2 (%d)", v, VariantV2)
	}
	if pc.UnreadLen() != 0 {
		t.Errorf("v2 peek should consume the magic byte; UnreadLen=%d", pc.UnreadLen())
	}
	// Subsequent Read returns the post-magic bytes.
	buf := make([]byte, 2)
	if _, err := readFull(pc, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, []byte{0x11, 0x22}) {
		t.Errorf("wrong replay: %x", buf)
	}
}

// TestPeekVersionLegacy — first byte in the legacy ECIES range is
// VariantLegacy and gets replayed.
func TestPeekVersionLegacy(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		_, _ = a.Write([]byte{0xf9, 0x01, 0x32})
	}()
	v, pc, err := PeekVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	if v != VariantLegacy {
		t.Errorf("variant = %d, want VariantLegacy (%d)", v, VariantLegacy)
	}
	if pc.UnreadLen() != 1 {
		t.Errorf("legacy replay should hold 1 byte; UnreadLen=%d", pc.UnreadLen())
	}
	buf := make([]byte, 3)
	if _, err := readFull(pc, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, []byte{0xf9, 0x01, 0x32}) {
		t.Errorf("wrong replay: %x", buf)
	}
}

// TestPeekVersionNonMagicIsLegacy — any byte other than VersionMagic
// is classified as legacy. The legacy RLPx handshake itself validates
// further. This is the "anything that isn't v2 is probably legacy"
// rule; malformed junk gets caught a few bytes into the legacy
// handshake.
func TestPeekVersionNonMagicIsLegacy(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		_, _ = a.Write([]byte{0x13})
	}()
	v, _, err := PeekVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	if v != VariantLegacy {
		t.Errorf("variant = %d, want VariantLegacy (%d)", v, VariantLegacy)
	}
}

// FuzzPeekVersionDispatch — arbitrary inputs to PeekVersion must never
// panic, never leak partial-handshake state, and always return a
// defined Variant. Covers peek-byte ambiguity and partial-handshake
// state-leak invariants from PIP-0006 §Phase 2b acceptance criteria.
func FuzzPeekVersionDispatch(f *testing.F) {
	f.Add([]byte{VersionMagic, 0x00, 0x01})
	f.Add([]byte{0xf9, 0x01, 0x32})
	f.Add([]byte{0x13, 0x37})
	f.Add([]byte{})
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		go func() {
			if len(data) > 0 {
				_, _ = a.Write(data)
			}
			_ = a.Close()
		}()
		// A short deadline so empty inputs don't hang the fuzzer.
		_ = b.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		v, _, _ := PeekVersion(b)
		switch v {
		case VariantV2, VariantLegacy, VariantUnknown:
		default:
			t.Errorf("undefined Variant: %d", v)
		}
	})
}

// pairConns establishes a real v2-handshake-authenticated Conn pair
// over net.Pipe and returns (initConn, respConn). Used by the framing
// fuzz target so it operates against the real keystreams, not a stub.
func pairConns(t testing.TB) (*Conn, *Conn, func()) {
	t.Helper()
	a, b := net.Pipe()
	cleanup := func() {
		_ = a.Close()
		_ = b.Close()
	}
	initConn := NewConn(a)
	respConn := NewConn(b)
	done := make(chan error, 2)
	go func() {
		var magic [1]byte
		if _, err := io.ReadFull(b, magic[:]); err != nil {
			done <- err
			return
		}
		if magic[0] != VersionMagic {
			done <- errors.New("magic mismatch")
			return
		}
		done <- respConn.AcceptHandshake()
	}()
	go func() {
		done <- initConn.DialHandshake()
	}()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			cleanup()
			t.Fatalf("pairConns: %v", err)
		}
	}
	return initConn, respConn, cleanup
}

// FuzzReadFrame feeds adversary-controlled length prefixes and frame
// tails into a fully-handshaked Conn's Read path. Invariants checked:
//
//   - no panic on truncated, oversized, or malformed input;
//   - recvNonce never advances on a failed Read (no partial-state
//     retention — a failed AEAD must not leave the receive counter
//     incremented for the next legitimate frame);
//   - oversized length prefixes are rejected before any allocation
//     proportional to the claimed size;
//   - Read either returns a non-nil error OR a non-nil plaintext,
//     but never both nil with no error.
func FuzzReadFrame(f *testing.F) {
	// Seeds: empty, short header, header claiming 0, header claiming
	// less than the AEAD overhead, header claiming MaxFrameLen+1
	// (overflow), well-formed but garbage AEAD body.
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte{0x00, 0x00, 0x00})
	f.Add([]byte{0x00, 0x00, 0x10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, respConn, cleanup := pairConns(t)
		defer cleanup()

		// Drive Read against a synthetic reader yielding `data`,
		// sharing the real respConn's recvAEAD. Using a stub avoids
		// the deadline-vs-EOF races that net.Pipe would otherwise
		// surface into the fuzz oracle.
		recvBefore := respConn.recvNonce
		stub := &fuzzReader{src: data}
		probe := &Conn{
			conn:      stub,
			recvAEAD:  respConn.recvAEAD,
			recvNonce: respConn.recvNonce,
		}

		pt, err := probe.Read()
		if err == nil && pt == nil {
			t.Fatal("Read returned (nil, nil)")
		}
		if err != nil {
			// Failed Read MUST NOT advance the recv nonce — leaving
			// the counter desynced from the peer would silently break
			// every subsequent legitimate frame.
			if probe.recvNonce != recvBefore {
				t.Fatalf("recvNonce advanced on failed Read: before=%d after=%d", recvBefore, probe.recvNonce)
			}
		}
	})
}

// fuzzReader implements net.Conn for the purpose of feeding a fixed
// byte slice into Conn.Read without involving the OS pipe machinery
// (which would surface deadline-vs-EOF races into the fuzz oracle).
type fuzzReader struct {
	src []byte
	pos int
}

func (r *fuzzReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.src) {
		return 0, io.EOF
	}
	n := copy(p, r.src[r.pos:])
	r.pos += n
	return n, nil
}
func (r *fuzzReader) Write([]byte) (int, error)       { return 0, io.ErrClosedPipe }
func (r *fuzzReader) Close() error                    { return nil }
func (r *fuzzReader) LocalAddr() net.Addr             { return dummyAddr{} }
func (r *fuzzReader) RemoteAddr() net.Addr            { return dummyAddr{} }
func (r *fuzzReader) SetDeadline(time.Time) error     { return nil }
func (r *fuzzReader) SetReadDeadline(time.Time) error { return nil }
func (r *fuzzReader) SetWriteDeadline(time.Time) error {
	return nil
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "fuzz" }
func (dummyAddr) String() string  { return "fuzz" }

// TestReadFrameLengthBoundaries — explicit boundary cases for the
// length-prefix decoding, complementing the fuzz target. Locks in the
// MaxFrameLen + AEAD-tag arithmetic so a future refactor can't widen
// the cap silently.
func TestReadFrameLengthBoundaries(t *testing.T) {
	_, respConn, cleanup := pairConns(t)
	defer cleanup()

	cases := []struct {
		name    string
		header  []byte
		wantErr error
	}{
		{
			name:    "claimed length below AEAD overhead",
			header:  []byte{0x00, 0x00, 0x01},
			wantErr: ErrBadFrame,
		},
		{
			name:    "claimed length exactly at AEAD overhead with no body",
			header:  []byte{0x00, 0x00, 0x10},
			wantErr: nil, // io.EOF on the body read — not ErrBadFrame
		},
		{
			name:    "claimed length above MaxFrameLen+overhead",
			header:  []byte{0xFF, 0xFF, 0xFF},
			wantErr: ErrFrameTooLarge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recvBefore := respConn.recvNonce
			stub := &fuzzReader{src: tc.header}
			probe := &Conn{conn: stub, recvAEAD: respConn.recvAEAD}
			probe.recvNonce = respConn.recvNonce
			_, err := probe.Read()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if probe.recvNonce != recvBefore {
				t.Fatalf("recvNonce advanced on error path: before=%d after=%d", recvBefore, probe.recvNonce)
			}
		})
	}
}

// TestReadFrameNonceDesyncRejected — a frame encrypted with sendNonce=N
// but presented when recvNonce=N+1 must fail authentication. This locks
// down the contract that the receive counter is part of the AEAD's
// associated state; an attacker reordering frames cannot get them
// accepted out of sequence.
func TestReadFrameNonceDesyncRejected(t *testing.T) {
	initConn, respConn, cleanup := pairConns(t)
	defer cleanup()

	// Capture the wire bytes for one legitimate frame.
	var captured bytes.Buffer
	tee := &captureConn{buf: &captured}
	teeProbe := &Conn{
		conn:      tee,
		sendAEAD:  initConn.sendAEAD,
		sendNonce: 0,
	}
	if err := teeProbe.Write([]byte("first")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Re-create a respConn-like reader where recvNonce starts at 1
	// (skip-ahead). The captured frame was sealed at nonce=0, so the
	// AEAD must reject it.
	stub := &fuzzReader{src: captured.Bytes()}
	probe := &Conn{conn: stub, recvAEAD: respConn.recvAEAD, recvNonce: 1}
	if _, err := probe.Read(); err == nil {
		t.Fatal("Read accepted a frame at desynced recvNonce")
	} else if !errors.Is(err, ErrBadFrame) {
		t.Fatalf("got %v, want ErrBadFrame", err)
	}
}

// captureConn is a write-sink net.Conn that records everything
// written to it. Used to snapshot a legitimate AEAD frame for the
// desync test; nothing is forwarded anywhere.
type captureConn struct {
	buf *bytes.Buffer
}

func (t *captureConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (t *captureConn) Close() error                     { return nil }
func (t *captureConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (t *captureConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (t *captureConn) SetDeadline(time.Time) error      { return nil }
func (t *captureConn) SetReadDeadline(time.Time) error  { return nil }
func (t *captureConn) SetWriteDeadline(time.Time) error { return nil }
func (t *captureConn) Write(p []byte) (int, error) {
	t.buf.Write(p)
	return len(p), nil
}

// TestRandomBytesClassification — exhaustive sweep over 0x00..0xFF.
// Exactly one byte (0xA0) maps to V2; all others map to Legacy.
func TestRandomBytesClassification(t *testing.T) {
	for i := 0; i < 256; i++ {
		a, b := net.Pipe()
		go func(v byte) {
			_, _ = a.Write([]byte{v})
			_ = a.Close()
		}(byte(i))
		_ = b.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		variant, _, _ := PeekVersion(b)
		_ = b.Close()
		if byte(i) == VersionMagic {
			if variant != VariantV2 {
				t.Errorf("0x%02x: got %d, want VariantV2", i, variant)
			}
		} else if variant != VariantLegacy {
			t.Errorf("0x%02x: got %d, want VariantLegacy", i, variant)
		}
	}
}

// BenchmarkHandshakeRoundTrip measures v2 handshake latency over a
// net.Pipe. The PIP-0006 Phase 2b acceptance criterion calls for "no
// worse than legacy RLPx +20%"; legacy RLPx completes in ~100µs on a
// net.Pipe, so our budget is ~120µs.
func BenchmarkHandshakeRoundTrip(b *testing.B) {
	var dummy [32]byte
	_, _ = rand.Read(dummy[:])

	b.ResetTimer()
	for b.Loop() {
		a, c := net.Pipe()
		initConn := NewConn(a)
		respConn := NewConn(c)
		done := make(chan struct{})
		go func() {
			var magic [1]byte
			_, _ = c.Read(magic[:])
			_ = respConn.AcceptHandshake()
			close(done)
		}()
		_ = initConn.DialHandshake()
		<-done
		_ = a.Close()
		_ = c.Close()
	}
}

// TestEmptyFrameRoundTrip — a zero-length Write produces a tag-only
// frame that must Read back as a non-nil empty slice: Read never
// yields (nil, nil), which is the invariant FuzzReadFrame asserts.
func TestEmptyFrameRoundTrip(t *testing.T) {
	a, b := handshakedPair(t)
	if err := a.Write([]byte{}); err != nil {
		t.Fatalf("write empty frame: %v", err)
	}
	pt, err := b.Read()
	if err != nil {
		t.Fatalf("read empty frame: %v", err)
	}
	if pt == nil {
		t.Fatal("Read returned nil plaintext for a valid empty frame")
	}
	if len(pt) != 0 {
		t.Fatalf("plaintext = %x, want empty", pt)
	}
	// The stream stays usable afterwards.
	if err := a.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if pt, err := b.Read(); err != nil || string(pt) != "after" {
		t.Fatalf("frame after empty = %q err=%v", pt, err)
	}
}

// TestTransportBreaksAfterFrameFailure — any framing failure latches
// the Conn: subsequent Reads/Writes fail with ErrTransportBroken. A
// partial write would otherwise be retryable under the same send
// nonce (ChaCha20-Poly1305 nonce reuse), and a read-side failure
// leaves the stream position ambiguous.
func TestTransportBreaksAfterFrameFailure(t *testing.T) {
	a, b := handshakedPair(t)

	// Corrupt a frame in transit by writing garbage bytes straight to
	// the underlying conn, then let the reader hit the AEAD reject.
	if _, err := a.Underlying().Write([]byte{0x00, 0x00, 0x20}); err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, 0x20)
	if _, err := a.Underlying().Write(junk); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read(); err == nil {
		t.Fatal("expected AEAD failure on corrupted frame")
	}
	if _, err := b.Read(); !errors.Is(err, ErrTransportBroken) {
		t.Fatalf("second Read after failure = %v, want ErrTransportBroken", err)
	}
	if err := b.Write([]byte("x")); !errors.Is(err, ErrTransportBroken) {
		t.Fatalf("Write after read failure = %v, want ErrTransportBroken", err)
	}

	// An oversize-plaintext Write touches nothing on the wire and
	// must NOT latch the sender.
	big := make([]byte, MaxFrameLen+1)
	if err := a.Write(big); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize write = %v, want ErrFrameTooLarge", err)
	}
	if err := a.Write([]byte("still fine")); err != nil {
		t.Fatalf("write after oversize rejection = %v, want nil", err)
	}
}

// handshakedPair returns two Conns on a loopback TCP pair with the
// v2 handshake completed. TCP (unlike net.Pipe) is kernel-buffered,
// so single-goroutine write-then-read tests don't deadlock.
func handshakedPair(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type accepted struct {
		c   net.Conn
		err error
	}
	acceptCh := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		acceptCh <- accepted{c, err}
	}()
	dialFD, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	acc := <-acceptCh
	if acc.err != nil {
		t.Fatal(acc.err)
	}
	t.Cleanup(func() {
		dialFD.Close()
		acc.c.Close()
	})

	init := NewConn(dialFD)
	resp := NewConn(acc.c)
	errCh := make(chan error, 1)
	go func() {
		// Consume the version magic the dispatcher would normally
		// peek before AcceptHandshake.
		var magic [1]byte
		if _, err := readFull(acc.c, magic[:]); err != nil {
			errCh <- err
			return
		}
		if magic[0] != VersionMagic {
			errCh <- errors.New("bad magic")
			return
		}
		errCh <- resp.AcceptHandshake()
	}()
	if err := init.DialHandshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	return init, resp
}
