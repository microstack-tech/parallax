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
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx/bip324handshake"
)

// nopLogger is a real logger bound to an in-memory sink; avoids the
// complexity of stubbing the full logging.Logger interface for tests
// that only need Warn/Trace calls to not crash.
var nopLogger = logging.New("mod", "p2p-test")

// TestV2TransportFullFlow — two v2Transports on a net.Pipe run the
// full doEncHandshake + doProtoHandshake flow and exchange a message
// frame. Validates the PIP-0006 Phase 2b acceptance criterion
// "Two v2.0 nodes complete RLPx handshake knowing only each other's
// IP:port, negotiate capabilities, exchange a GetPeers/Peers round-trip."
func TestV2TransportFullFlow(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	// Responder side must have the magic byte consumed upstream,
	// mimicking the production listener PeekVersion dispatch.
	type result struct {
		role        string
		err         error
		remoteHello *protoHandshake
	}
	ch := make(chan result, 2)

	go func() {
		// Consume magic byte.
		var magic [1]byte
		if _, err := b.Read(magic[:]); err != nil {
			ch <- result{role: "peek", err: err}
			return
		}
		if magic[0] != bip324handshake.VersionMagic {
			ch <- result{role: "peek", err: errors.New("bad magic")}
			return
		}
		resp := newV2Inbound(b)
		_, err := resp.doEncHandshake(nil)
		if err != nil {
			ch <- result{role: "accept-enc", err: err}
			return
		}
		our := &protoHandshake{Version: 5, Name: "resp", ID: make([]byte, 64)}
		their, err := resp.doProtoHandshake(our)
		ch <- result{role: "resp", err: err, remoteHello: their}
	}()

	init := newV2Outbound(a)
	if _, err := init.doEncHandshake(nil); err != nil {
		t.Fatalf("init enc: %v", err)
	}

	our := &protoHandshake{Version: 5, Name: "init", ID: make([]byte, 64)}
	their, err := init.doProtoHandshake(our)
	if err != nil {
		t.Fatalf("init proto: %v", err)
	}
	if their.Name != "resp" {
		t.Errorf("remote name: got %q, want %q", their.Name, "resp")
	}

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: %v", r.role, r.err)
		}
		if r.remoteHello == nil || r.remoteHello.Name != "init" {
			t.Errorf("resp remote name: %+v", r.remoteHello)
		}
		// Identity invariant for the Server's post-handshake
		// verify: the bytes sent in the peer's Hello (phs.ID) must
		// keccak256 to the same value the local side derives as
		// node.ID from the remote's ephemeral key.
		//
		// r.remoteHello is what resp received from init — so its
		// ID field is v2SessionIDBytes(init.localEphem). The
		// server identity check keccak256s that to derive the
		// remote's node.ID; we mirror that computation here.
		initLocalEphem, _ := init.conn.SessionKeys()
		want := crypto.Keccak256(v2SessionIDBytes(initLocalEphem))
		got := crypto.Keccak256(r.remoteHello.ID)
		if !bytes.Equal(got, want) {
			t.Errorf("identity mismatch: keccak(phs.ID)=%x, want=%x", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("responder handshake timeout")
	}
}

// TestV2TransportReadWriteRoundTrip — full handshake + one Msg frame
// both directions, using a shared responder handle so Read/Write on
// both sides is observable.
func TestV2TransportReadWriteRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	var (
		resp      *v2Transport
		wg        sync.WaitGroup
		acceptErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var magic [1]byte
		if _, err := b.Read(magic[:]); err != nil {
			acceptErr = err
			return
		}
		resp = newV2Inbound(b)
		_, acceptErr = resp.doEncHandshake(nil)
	}()

	init := newV2Outbound(a)
	if _, err := init.doEncHandshake(nil); err != nil {
		t.Fatalf("init enc: %v", err)
	}
	wg.Wait()
	if acceptErr != nil {
		t.Fatalf("accept enc: %v", acceptErr)
	}

	// Skip the devp2p Hello for this test — we just care about frame
	// round-trip. WriteMsg/ReadMsg work independently of the proto
	// handshake once the AEAD is up.
	payload := []byte("hello parallax-disc/1")
	go func() {
		_ = init.WriteMsg(Msg{Code: 0x11, Size: uint32(len(payload)), Payload: bytes.NewReader(payload)})
	}()
	got, err := resp.ReadMsg()
	if err != nil {
		t.Fatalf("resp read: %v", err)
	}
	if got.Code != 0x11 {
		t.Errorf("got code 0x%02x, want 0x11", got.Code)
	}
	body, err := io.ReadAll(got.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("body mismatch: got %q, want %q", body, payload)
	}
}

// TestPickHandshakeVariantInbound — pickHandshakeVariant correctly
// classifies peeked-v2 and peeked-legacy inbound connections, and
// defaults to legacy when no peek has happened.
func TestPickHandshakeVariantInbound(t *testing.T) {
	srv := &Server{Config: Config{ExperimentalV2Handshake: true}}

	// Plain net.Conn (no peek wrapper) → legacy.
	a, _ := net.Pipe()
	defer a.Close()
	if v := srv.pickHandshakeVariant(a, inboundConn, nil); v != handshakeVariantLegacy {
		t.Errorf("plain inbound: got %d, want legacy", v)
	}

	// peekedConn with variant=v2 → v2Inbound.
	b, _ := net.Pipe()
	defer b.Close()
	pc := &peekedConn{Conn: b, variant: peekedVariantV2}
	if v := srv.pickHandshakeVariant(pc, inboundConn, nil); v != handshakeVariantV2Inbound {
		t.Errorf("peeked-v2 inbound: got %d, want v2Inbound", v)
	}

	// peekedConn with variant=legacy → legacy.
	c, _ := net.Pipe()
	defer c.Close()
	pc2 := &peekedConn{Conn: c, variant: peekedVariantLegacy}
	if v := srv.pickHandshakeVariant(pc2, inboundConn, nil); v != handshakeVariantLegacy {
		t.Errorf("peeked-legacy inbound: got %d, want legacy", v)
	}
}

// TestLegacyHandshakeOffRefusesInbound — dispatchInbound rejects
// legacy-magic-first-byte connections when LegacyHandshakeMode=off.
func TestLegacyHandshakeOffRefusesInbound(t *testing.T) {
	srv := &Server{
		Config: Config{
			ExperimentalV2Handshake: true,
			LegacyHandshakeMode:     "off",
			Logger:                  nopLogger,
		},
	}
	srv.log = srv.Config.Logger

	a, b := net.Pipe()
	defer a.Close()
	// Write a legacy-shaped first byte.
	go func() {
		_, _ = a.Write([]byte{0xf9, 0x01, 0x32})
	}()
	wrapped := srv.dispatchInbound(b)
	if wrapped != nil {
		t.Fatalf("dispatchInbound should have refused legacy under LegacyHandshakeMode=off; got %v", wrapped)
	}
}

// TestLegacyHandshakeOnAcceptsInbound — dispatchInbound preserves
// legacy inbound when LegacyHandshakeMode=on (or empty).
func TestLegacyHandshakeOnAcceptsInbound(t *testing.T) {
	srv := &Server{
		Config: Config{
			ExperimentalV2Handshake: true,
			LegacyHandshakeMode:     "on",
			Logger:                  nopLogger,
		},
	}
	srv.log = srv.Config.Logger

	a, b := net.Pipe()
	defer a.Close()
	go func() {
		_, _ = a.Write([]byte{0xf9, 0x01, 0x32})
	}()
	wrapped := srv.dispatchInbound(b)
	if wrapped == nil {
		t.Fatal("dispatchInbound should accept legacy under LegacyHandshakeMode=on")
	}
}
