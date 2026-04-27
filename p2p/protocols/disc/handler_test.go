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

package disc

import (
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

// testBackend captures handler callbacks so tests can assert on them.
type testBackend struct {
	mu        sync.Mutex
	sample    []PeerEntry
	gotAddrs  []YourAddr
	gotPeers  [][]PeerEntry
	gotHellos []Hello
	obsOK     bool
	self      *PeerEntry // if non-nil, SelfEntry returns (*self, true)
	// localHello is the Hello returned from LocalHello(). Tests that
	// drive Hello flows can set the nonce or other fields.
	localHello Hello
	// helloErr, when non-nil, is returned from HandleHello so tests
	// can simulate a self-connect rejection.
	helloErr error
}

func (b *testBackend) Log() logging.Logger { return logging.New("mod", "disc-test") }

func (b *testBackend) ObserveTheirSource(_ *p2p.Peer) (uint8, []byte, uint16, bool) {
	if !b.obsOK {
		return 0, nil, 0, false
	}
	return NetIPv4, []byte{1, 2, 3, 4}, 30303, true
}

func (b *testBackend) HandleYourAddr(_ *p2p.Peer, net uint8, addr []byte, port uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gotAddrs = append(b.gotAddrs, YourAddr{NetworkID: net, Addr: append([]byte(nil), addr...), TCPPort: port})
}

func (b *testBackend) HandlePeers(_ *p2p.Peer, entries []PeerEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]PeerEntry, len(entries))
	copy(cp, entries)
	b.gotPeers = append(b.gotPeers, cp)
}

func (b *testBackend) SamplePeers(_ *p2p.Peer, max int) []PeerEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sample) > max {
		return b.sample[:max]
	}
	return b.sample
}

func (b *testBackend) SelfEntry(_ uint16) (PeerEntry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.self == nil {
		return PeerEntry{}, false
	}
	return *b.self, true
}

func (b *testBackend) TrackHandshake(*p2p.Peer, bool) {}
func (b *testBackend) PeerHandshake(enode.ID) string  { return "" }

func (b *testBackend) LocalHello() Hello {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.localHello
}

func (b *testBackend) HandleHello(_ *p2p.Peer, h Hello) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.helloErr != nil {
		return b.helloErr
	}
	b.gotHellos = append(b.gotHellos, h)
	return nil
}

// runHandler spins up Run on one side of a MsgPipe and returns the other
// end so the test can send messages. The session loop returns when the
// app side closes the pipe.
func runHandler(t *testing.T, backend Backend) (app *p2p.MsgPipeRW, done <-chan error) {
	t.Helper()
	// Disable Poisson jitter for the duration of the test — a 2s
	// mean per response wrecks suite runtime.
	prev := peersResponseJitterMean
	SetPeersResponseJitterMean(0)
	t.Cleanup(func() { SetPeersResponseJitterMean(prev) })
	appRW, netRW := p2p.MsgPipe()
	var id enode.ID
	_, _ = rand.Read(id[:])
	peer := p2p.NewPeer(id, "test", nil)
	ch := make(chan error, 1)
	go func() {
		ch <- Run(backend, peer, netRW)
	}()
	t.Cleanup(func() {
		appRW.Close()
	})
	return appRW, ch
}

// TestHandlerSendsHelloThenYourAddr — both sides write Hello first
// (carrying nonce + listen port) then YourAddr (peer's view of our
// remote source). Pinning the order so a refactor can't accidentally
// reverse it without also updating peers that expect Hello first.
func TestHandlerSendsHelloThenYourAddr(t *testing.T) {
	b := &testBackend{
		obsOK: true,
		localHello: Hello{
			ProtoVersion: HelloMinProtoVersion,
			Nonce:        0xCAFEBABE,
			ListenPort:   32110,
			Services:     ServiceNodeNetwork | ServiceRelayTx,
		},
	}
	app, _ := runHandler(t, b)

	// First: Hello.
	msg, err := app.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg #1: %v", err)
	}
	if msg.Code != HelloMsg {
		t.Fatalf("first msg code = 0x%02x, want Hello 0x%02x", msg.Code, HelloMsg)
	}
	var hello Hello
	if err := msg.Decode(&hello); err != nil {
		t.Fatalf("decode Hello: %v", err)
	}
	if hello.Nonce != b.localHello.Nonce || hello.ListenPort != b.localHello.ListenPort ||
		hello.Services != b.localHello.Services {
		t.Errorf("Hello contents unexpected: %+v vs source %+v", hello, b.localHello)
	}

	// Second: YourAddr.
	msg, err = app.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg #2: %v", err)
	}
	if msg.Code != YourAddrMsg {
		t.Fatalf("second msg code = 0x%02x, want YourAddr 0x%02x", msg.Code, YourAddrMsg)
	}
	var got YourAddr
	if err := msg.Decode(&got); err != nil {
		t.Fatalf("decode YourAddr: %v", err)
	}
	if got.NetworkID != NetIPv4 || got.TCPPort != 30303 {
		t.Errorf("YourAddr contents unexpected: %+v", got)
	}
}

// TestHandlerRejectsMessageBeforeHello — sending YourAddr / Peers /
// GetPeers before our peer has sent Hello is a protocol violation
// and ends the session.
func TestHandlerRejectsMessageBeforeHello(t *testing.T) {
	cases := []struct {
		name string
		send func(p2p.MsgWriter) error
	}{
		{"YourAddr", func(w p2p.MsgWriter) error {
			return p2p.Send(w, YourAddrMsg, YourAddr{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303})
		}},
		{"Peers", func(w p2p.MsgWriter) error {
			return p2p.Send(w, PeersMsg, Peers{Entries: []PeerEntry{{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: KeyTypeNone}}})
		}},
		{"GetPeers", func(w p2p.MsgWriter) error {
			return p2p.Send(w, GetPeersMsg, GetPeers{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &testBackend{obsOK: true}
			app, done := runHandler(t, b)
			drainGreeting(t, app)
			if err := tc.send(app); err != nil {
				t.Fatalf("send %s: %v", tc.name, err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("expected handler error after pre-Hello %s", tc.name)
				}
			case <-time.After(time.Second):
				t.Fatalf("handler didn't exit after pre-Hello %s", tc.name)
			}
		})
	}
}

// TestHandlerRejectsDoubleHello — second Hello on same session ends
// the session.
func TestHandlerRejectsDoubleHello(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)
	drainGreeting(t, app)

	first := Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 0x11, ListenPort: 32110}
	if err := p2p.Send(app, HelloMsg, first); err != nil {
		t.Fatalf("first Hello send: %v", err)
	}
	second := Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 0x22, ListenPort: 32110}
	if err := p2p.Send(app, HelloMsg, second); err != nil {
		t.Fatalf("second Hello send: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected handler error after double Hello")
		}
	case <-time.After(time.Second):
		t.Fatalf("handler didn't exit after double Hello")
	}
}

// TestHandlerHelloDecodeError — malformed Hello payload ends the
// session with a decode error.
func TestHandlerHelloDecodeError(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)
	drainGreeting(t, app)

	// Send a Hello whose payload is wrong-typed (a single byte
	// string rather than a struct list). Decode must fail.
	if err := p2p.Send(app, HelloMsg, []byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected handler decode error")
		}
	case <-time.After(time.Second):
		t.Fatalf("handler didn't exit on decode error")
	}
}

// TestHandlerHelloValidationError — an invalid Hello (ProtoVersion=0)
// ends the session.
func TestHandlerHelloValidationError(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)
	drainGreeting(t, app)

	bad := Hello{ProtoVersion: 0, Nonce: 1, ListenPort: 32110}
	if err := p2p.Send(app, HelloMsg, bad); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected handler validation error")
		}
	case <-time.After(time.Second):
		t.Fatalf("handler didn't exit on validation error")
	}
}

// TestHandlerHelloEndsSessionOnSelfNonce — when the testBackend
// returns errSelfConnect from HandleHello (simulating a peer
// echoing our own nonce), the handler ends the session.
func TestHandlerHelloEndsSessionOnSelfNonce(t *testing.T) {
	b := &testBackend{
		obsOK:    true,
		helloErr: errSelfConnect,
	}
	app, done := runHandler(t, b)
	drainGreeting(t, app)

	in := Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 1, ListenPort: 32110}
	if err := p2p.Send(app, HelloMsg, in); err != nil {
		t.Fatalf("send Hello: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errSelfConnect) {
			t.Fatalf("session ended with %v, want errSelfConnect", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler didn't exit after self-nonce Hello")
	}
}

// TestHandlerHelloPropagatesToBackend — a valid Hello reaches
// backend.HandleHello and is captured.
func TestHandlerHelloPropagatesToBackend(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, _ := runHandler(t, b)
	drainGreeting(t, app)

	in := Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 0xABCD, ListenPort: 32110, Services: ServiceNodeNetwork}
	if err := p2p.Send(app, HelloMsg, in); err != nil {
		t.Fatalf("send: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := len(b.gotHellos)
		b.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.gotHellos) != 1 {
		t.Fatalf("backend got %d Hellos, want 1", len(b.gotHellos))
	}
	if b.gotHellos[0].Nonce != in.Nonce || b.gotHellos[0].ListenPort != in.ListenPort {
		t.Fatalf("backend got %+v, want %+v", b.gotHellos[0], in)
	}
}

// TestHandlerAcceptsValidPeersMessage — a well-formed Peers packet ends
// up in HandlePeers, entries with skippable tags are filtered out.
func TestHandlerAcceptsValidPeersMessage(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)

	// Drain our outgoing greeting and send the peer's Hello so the
	// handler will accept further messages.
	drainAndOpen(t, app)

	in := Peers{Entries: []PeerEntry{
		{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 30303, KeyType: KeyTypeNone},
		{NetworkID: 0xEE, Addr: []byte{0x00}, TCPPort: 30303}, // unknown net → skip
	}}
	if err := p2p.Send(app, PeersMsg, in); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForSample(t, b, 1, 500*time.Millisecond)
	b.mu.Lock()
	got := b.gotPeers[0]
	b.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("HandlePeers got %d entries, want 1 (after skip)", len(got))
	}
	app.Close()
	// Sess should end cleanly on pipe close.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not exit on pipe close")
	}
}

// TestHandlerRejectsOversizedPeersMessage — sending a Peers with too many
// entries disconnects. The pipe close is the session end signal.
func TestHandlerRejectsOversizedPeersMessage(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)
	drainAndOpen(t, app)

	big := Peers{Entries: make([]PeerEntry, MaxPeersPerMessage+1)}
	for i := range big.Entries {
		big.Entries[i] = PeerEntry{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeNone}
	}
	if err := p2p.Send(app, PeersMsg, big); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error on oversize Peers")
		} else if !errors.Is(err, ErrPeersTooLarge) {
			t.Errorf("expected ErrPeersTooLarge, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not exit on oversize Peers")
	}
}

// TestHandlerRejectsDoubleYourAddr — YourAddr is single-shot; a second
// one is a protocol violation.
func TestHandlerRejectsDoubleYourAddr(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)
	drainAndOpen(t, app)

	for range 2 {
		if err := p2p.Send(app, YourAddrMsg, YourAddr{NetworkID: NetIPv4, Addr: []byte{5, 6, 7, 8}, TCPPort: 30303}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error on second YourAddr")
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not exit on repeat YourAddr")
	}
}

// TestHandlerAnswersGetPeers — a GetPeers gets a Peers response with the
// backend's sample.
func TestHandlerAnswersGetPeers(t *testing.T) {
	b := &testBackend{
		obsOK: true,
		sample: []PeerEntry{
			{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeNone, LastSeen: 1700000000},
		},
	}
	app, _ := runHandler(t, b)
	drainAndOpen(t, app)

	if err := p2p.Send(app, GetPeersMsg, GetPeers{}); err != nil {
		t.Fatal(err)
	}
	msg, err := app.ReadMsg()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Code != PeersMsg {
		t.Fatalf("got code 0x%02x, want Peers", msg.Code)
	}
	var out Peers
	if err := msg.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(out.Entries))
	}
}

// TestHandlerIgnoresRepeatGetPeers — Bitcoin parity: one response per
// session. Second GetPeers yields no response.
func TestHandlerIgnoresRepeatGetPeers(t *testing.T) {
	b := &testBackend{
		obsOK:  true,
		sample: []PeerEntry{{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeNone}},
	}
	app, _ := runHandler(t, b)
	drainAndOpen(t, app)

	_ = p2p.Send(app, GetPeersMsg, GetPeers{})
	drainOne(t, app) // first response

	_ = p2p.Send(app, GetPeersMsg, GetPeers{})

	// Second request should produce no response; set a short read
	// deadline by racing against a timer.
	read := make(chan p2p.Msg, 1)
	go func() {
		if msg, err := app.ReadMsg(); err == nil {
			read <- msg
		}
	}()
	select {
	case <-read:
		t.Fatal("handler responded to second GetPeers")
	case <-time.After(150 * time.Millisecond):
	}
}

// drainOne reads a single message and discards the payload so MsgPipe's
// sender WriteMsg unblocks. The p2p.MsgPipe contract is that WriteMsg
// stays blocked until the receiver fully consumes the payload reader.
func drainOne(t *testing.T, rw p2p.MsgReader) {
	t.Helper()
	msg, err := rw.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	_ = msg.Discard()
}

// drainGreeting consumes the Hello + YourAddr + GetPeers that an
// outbound handler writes on session start. (p2p.NewPeer yields conn
// flags of 0, which Inbound() reports as false — so every test-harness
// handler takes the outbound branch.)
func drainGreeting(t *testing.T, rw p2p.MsgReader) {
	t.Helper()
	drainOne(t, rw) // Hello
	drainOne(t, rw) // YourAddr
	drainOne(t, rw) // GetPeers
}

// sendTestHello sends a benign Hello on the test pipe so the
// handler's "msg before Hello" gate opens. Used by tests that
// exercise post-Hello message paths (Peers, YourAddr, GetPeers).
func sendTestHello(t *testing.T, w p2p.MsgWriter) {
	t.Helper()
	h := Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 0xFEEDFACE, ListenPort: 32110, Services: ServiceNodeNetwork}
	if err := p2p.Send(w, HelloMsg, h); err != nil {
		t.Fatalf("send Hello: %v", err)
	}
}

// drainAndOpen is the standard test fixture: consume the handler's
// outgoing greeting, then send a benign Hello so the handler accepts
// subsequent messages. Tests that specifically exercise pre-Hello
// rejection should call drainGreeting alone instead.
func drainAndOpen(t *testing.T, app p2p.MsgReadWriter) {
	t.Helper()
	drainGreeting(t, app)
	sendTestHello(t, app)
}

func waitForSample(t *testing.T, b *testBackend, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := len(b.gotPeers)
		b.mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d HandlePeers calls, got timeout", want)
}
