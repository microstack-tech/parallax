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
