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
	mu       sync.Mutex
	sample   []PeerEntry
	gotAddrs []YourAddr
	gotPeers [][]PeerEntry
	obsOK    bool
	self     *PeerEntry // if non-nil, SelfEntry returns (*self, true)
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

// TestHandlerSendsInitialYourAddr — both sides must write YourAddr as the
// first message after negotiation.
func TestHandlerSendsInitialYourAddr(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, _ := runHandler(t, b)
	msg, err := app.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if msg.Code != YourAddrMsg {
		t.Fatalf("first msg code = 0x%02x, want YourAddr 0x%02x", msg.Code, YourAddrMsg)
	}
	var got YourAddr
	if err := msg.Decode(&got); err != nil {
		t.Fatalf("decode YourAddr: %v", err)
	}
	if got.NetworkID != NetIPv4 || got.TCPPort != 30303 {
		t.Errorf("YourAddr contents unexpected: %+v", got)
	}
}

// TestHandlerAcceptsValidPeersMessage — a well-formed Peers packet ends
// up in HandlePeers, entries with skippable tags are filtered out.
func TestHandlerAcceptsValidPeersMessage(t *testing.T) {
	b := &testBackend{obsOK: true}
	app, done := runHandler(t, b)

	// Drain our outgoing YourAddr so the pipe isn't backpressured.
	drainGreeting(t, app)

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
	drainGreeting(t, app)

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
	drainGreeting(t, app)

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
	drainGreeting(t, app)

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
	drainGreeting(t, app)

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

// drainGreeting consumes the YourAddr + GetPeers that an outbound
// handler writes on session start. (p2p.NewPeer yields conn flags of 0,
// which Inbound() reports as false — so every test-harness handler takes
// the outbound branch.)
func drainGreeting(t *testing.T, rw p2p.MsgReader) {
	t.Helper()
	drainOne(t, rw) // YourAddr
	drainOne(t, rw) // GetPeers
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
