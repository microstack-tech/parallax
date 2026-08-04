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
	"errors"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

// Backend is the host-integration surface. The handler calls into Backend
// for observed-address reports, addrbook ingest, addrbook sampling, and
// the self-entry used during the outbound greeting sequence.
//
// The production implementation is AddrmanBackend; tests can supply
// their own.
type Backend interface {
	// ObserveTheirSource records the observed remote TCP source of an
	// inbound or outbound connection. Used to compose our outgoing
	// YourAddr message and, on the peer's side, to feed quorum.
	ObserveTheirSource(peer *p2p.Peer) (network uint8, addr []byte, port uint16, ok bool)

	// HandleYourAddr feeds a peer's reported view of our external
	// address into the quorum tally.
	HandleYourAddr(peer *p2p.Peer, net uint8, addr []byte, port uint16)

	// HandlePeers ingests gossiped entries, applying any per-peer rate
	// limits and the 2-hour gossip LastSeen penalty.
	HandlePeers(peer *p2p.Peer, entries []PeerEntry)

	// SamplePeers returns up to max entries for a GetPeers response,
	// subject to reachability filtering. May return nil; the handler
	// still sends a valid (empty) Peers message.
	SamplePeers(peer *p2p.Peer, max int) []PeerEntry

	// SelfEntry returns the PeerEntry we should advertise on outbound
	// sessions, or ok=false if no self-address has reached quorum and
	// no override is configured. listenPort is the TCP port we listen
	// on (used when the quorum winner has port=0).
	SelfEntry(listenPort uint16) (PeerEntry, bool)

	// TrackHandshake records which handshake variant a peer session
	// was established with. The handler calls this once on session
	// start. Used by admin.peers to show whether a peer is
	// v2-authenticated or legacy+v2.
	TrackHandshake(peer *p2p.Peer, usingV2 bool)

	// PeerHandshake returns the handshake variant recorded for a
	// peer by TrackHandshake, or an empty string if the peer isn't
	// known (connection torn down, never parallax-disc/1-negotiated).
	PeerHandshake(id enode.ID) string

	// LocalHello returns the Hello the handler should send on the
	// outgoing greeting. Carries the local nonce, listen port, and
	// services flag. Bitcoin Core analog: PushNodeVersion.
	LocalHello() Hello

	// HandleHello consumes the peer's Hello on receipt. Must run the
	// self-connect nonce check (returning an error to end the
	// session if the peer's nonce matches our own) and may store
	// the Hello for later lookup.
	HandleHello(peer *p2p.Peer, h Hello) error

	// Log returns the logger to use for protocol-level events.
	Log() logging.Logger
}

// state holds per-peer handler state — one struct per session.
type state struct {
	// sentHello / gotHello track the v2 Hello exchange. Hello is the
	// first message in both directions; receiving any other message
	// before Hello is a protocol violation. Once gotHello is set,
	// it's never reset — a second Hello is also a violation.
	sentHello atomic.Bool
	gotHello  atomic.Bool

	// sentYourAddr: we've written our YourAddr message for this
	// session. Each side sends exactly one, immediately after Hello.
	sentYourAddr atomic.Bool

	// gotYourAddr: we've seen the peer's YourAddr for this session.
	gotYourAddr atomic.Bool

	// peersReceived counts Peers messages received on this session.
	// Retained for diagnostics; no longer gates disconnection, since
	// address relay legitimately pushes unsolicited Peers messages
	// throughout a session (see handlePeers).
	peersReceived atomic.Uint32

	// getPeersSent counts GetPeers requests we've issued to this peer.
	// One-request-per-session is the rule (Bitcoin parity).
	getPeersSent atomic.Uint32

	// getPeersReceived counts GetPeers from the peer. Bitcoin parity:
	// one response per session; further requests are silently ignored.
	getPeersReceived atomic.Uint32

	// knownAddr is the rolling bloom filter tracking addresses we've
	// sent to this peer. Used to skip re-relaying addresses the peer
	// already has (Phase 4 RelayAddress discipline).
	knownAddr bloomFilter
}

// Run is the per-peer entry point. Called by p2p.Server once the
// subprotocol has been negotiated.
func Run(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter) error {
	log := backend.Log().New("peer", peer.ID())
	log.Trace("parallax-disc/1 session starting")

	backend.TrackHandshake(peer, peer.UsingV2Handshake())

	st := &state{}

	// First action on both sides: send Hello carrying the local
	// nonce, listen port, and services flag. Receivers compare the
	// nonce against their own to detect self-connect, and use the
	// listen port to dedup cross-dial pairs (phase 2). Bitcoin Core
	// analog: PushNodeVersion (src/net.cpp).
	if err := sendHello(backend, peer, rw, st); err != nil {
		log.Debug("parallax-disc/1: Hello send failed", "err", err)
	}

	// YourAddr follows Hello: peer's view of our remote source for
	// quorum. Order-independent on the wire because RLPx is
	// multiplexed — but logically Hello must precede YourAddr.
	if err := sendYourAddr(backend, peer, rw, st); err != nil {
		log.Debug("parallax-disc/1: YourAddr send failed", "err", err)
	}

	// Bitcoin's address-relay discipline is direction-sensitive:
	// outbound peers get addr(self) + getaddr, inbound peers get
	// nothing unsolicited. The distinction matters because an inbound
	// peer could be an adversary probing our addrbook.
	//
	// Block-relay-only outbound peers also skip both: by spec they
	// don't participate in address gossip (Bitcoin Core
	// src/net_processing.cpp:3681 — fRelayTxes=false implies no
	// addr or addr_v2 relay). Self-advertise leaks our endpoint to
	// a peer that won't gossip it; GetPeers solicits gossip from a
	// peer we shouldn't accept gossip from.
	if !peer.Inbound() && !peer.BlockRelayOnly() {
		if err := sendSelfAdvertise(backend, rw); err != nil {
			log.Debug("parallax-disc/1: self-advertise send failed", "err", err)
		}
		if err := RequestPeers(backend, peer, st, rw); err != nil {
			log.Debug("parallax-disc/1: GetPeers send failed", "err", err)
		}
	}

	// Per-peer relay outbox: RelayAddress fans newly-learned
	// addresses into our outbox; the drain goroutine writes them on
	// the wire (gated by the per-peer known-addr bloom so we don't
	// repeat what the peer already told us). Buffer 16 means we drop
	// at most a small burst if the underlying connection is slow —
	// Bitcoin's behavior.
	//
	// Lifecycle: RegisterPeerOutbox returns a stop channel the drain
	// selects on; UnregisterPeerOutbox closes it. The outbox itself is
	// NEVER closed by either side — closing would race with concurrent
	// in-flight RelayAddress sends that snapshotted the outbox pointer
	// before we unregistered, and Go panics on send to a closed channel
	// even inside a select-with-default (default fires only on full,
	// not on closed). The drain exits on stop OR on a wire-write error.
	type outboxRegistrar interface {
		RegisterPeerOutbox(PeerKey, chan<- PeerEntry) <-chan struct{}
		UnregisterPeerOutbox(PeerKey)
	}
	var (
		outbox chan PeerEntry
		stop   <-chan struct{}
	)
	// Block-relay-only peers are excluded from the relay fan-out for
	// the same reason they get no self-advertise or GetPeers above:
	// by spec they don't participate in address gossip. Registering
	// their outbox would both leak addrbook entries to a peer that
	// committed not to gossip and waste a fan-out slot per address.
	if reg, ok := backend.(outboxRegistrar); ok && !peer.BlockRelayOnly() {
		outbox = make(chan PeerEntry, relayOutboxBuffer)
		stop = reg.RegisterPeerOutbox(peerKeyFor(peer), outbox)
	}

	relayDone := make(chan struct{})
	if outbox != nil {
		go func() {
			defer close(relayDone)
			runRelayDrain(peer, rw, st, outbox, stop, log)
		}()
	} else {
		close(relayDone)
	}

	defer func() {
		// Release per-peer state from the backend's maps on session
		// close. AddrmanBackend exposes PeerDisconnected; other
		// backends may not, so check via type assertion.
		// UnregisterPeerOutbox closes the stop channel, which the
		// drain selects on; we then wait for the drain to exit. We do
		// NOT close the outbox — see the lifecycle comment above.
		if reg, ok := backend.(outboxRegistrar); ok {
			reg.UnregisterPeerOutbox(peerKeyFor(peer))
		}
		<-relayDone
		if cleaner, ok := backend.(interface{ PeerDisconnected(*p2p.Peer) }); ok {
			cleaner.PeerDisconnected(peer)
		}
	}()

	for {
		if err := handleOne(backend, peer, rw, st); err != nil {
			log.Debug("parallax-disc/1: session ending", "err", err)
			return err
		}
	}
}

// relayOutboxBuffer is the per-peer relay outbox depth. Sized so a
// small burst of new addresses doesn't drop while the drain
// goroutine is mid-WriteMsg, but kept tight enough that a peer with
// a slow underlying TCP connection doesn't pile up megabytes of
// queued entries. Bitcoin's outbound rate of 1.0 addr/s + burst 10
// means 16 covers ~16s of worst-case backlog before drops.
const relayOutboxBuffer = 16

// runRelayDrain is the per-peer goroutine that pulls entries from
// the relay outbox and writes them to the wire. Skips entries the
// peer already knows about (per the rolling known-addr bloom in
// st.knownAddr), which is the m_addr_known dedup half of Bitcoin's
// RelayAddress contract.
//
// Returns when stop fires (UnregisterPeerOutbox closed it on session
// teardown) or when a wire write fails. The outbox is never closed,
// so the drain cannot rely on a range-loop termination; the explicit
// stop-channel select is what unwinds the goroutine.
func runRelayDrain(peer *p2p.Peer, rw p2p.MsgReadWriter, st *state, outbox <-chan PeerEntry, stop <-chan struct{}, log logging.Logger) {
	for {
		var entry PeerEntry
		select {
		case <-stop:
			return
		case entry = <-outbox:
		}
		key := addressKey(entry.NetworkID, entry.Addr, entry.TCPPort)
		if st.knownAddr.Contains(key) {
			// Peer already knows. Bitcoin: m_addr_known dedup.
			continue
		}
		st.knownAddr.Add(key)
		if err := p2p.Send(rw, PeersMsg, Peers{Entries: []PeerEntry{entry}}); err != nil {
			log.Trace("parallax-disc/1: relay write failed; ending drain", "peer", peer.ID(), "err", err)
			return
		}
	}
}

// sendSelfAdvertise writes a 1-entry Peers message containing our
// current self-address claim to an outbound peer. Mirrors Bitcoin's
// addr(self) sequence on outbound-full-relay peers. Skipped silently
// if no self-address is available (no quorum, no override).
func sendSelfAdvertise(backend Backend, rw p2p.MsgReadWriter) error {
	self, ok := backend.SelfEntry(0)
	if !ok {
		return nil
	}
	return p2p.Send(rw, PeersMsg, Peers{Entries: []PeerEntry{self}})
}

// handleOne reads and dispatches one inbound message. Returns on read
// error, oversized payload, or protocol violation — the caller closes
// the session. Protocol-discipline violations (oversized payload,
// pre-Hello message, malformed decode) also flag the peer for
// discourage via MisbehavingFor so the BanMan layer's accept-time
// check can hard-reject reconnects from this source under inbound
// saturation.
func handleOne(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter, st *state) error {
	msg, err := rw.ReadMsg()
	if err != nil {
		return err
	}
	defer msg.Discard()

	if msg.Size > MaxMessageSize {
		peer.MisbehavingFor("disc-oversized-msg")
		return fmt.Errorf("disc: message too large: %d > %d", msg.Size, MaxMessageSize)
	}

	// Enforce the protocol's first-message ordering: Hello must be
	// the first message on every session. Anything else before Hello
	// is a violation. Once gotHello is true, the rule is satisfied.
	// The Hello message itself bypasses the gate (it's allowed to
	// be the first thing we see).
	if msg.Code != HelloMsg && !st.gotHello.Load() {
		peer.MisbehavingFor("disc-pre-hello-msg")
		return fmt.Errorf("disc: msg 0x%02x before Hello", msg.Code)
	}

	switch msg.Code {
	case HelloMsg:
		return handleHello(backend, peer, st, msg)
	case GetPeersMsg:
		return handleGetPeers(backend, peer, rw, st, msg)
	case PeersMsg:
		return handlePeers(backend, peer, st, msg)
	case YourAddrMsg:
		return handleYourAddr(backend, peer, st, msg)
	}
	return fmt.Errorf("disc: unknown msg code 0x%02x", msg.Code)
}

func handleGetPeers(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter, st *state, msg p2p.Msg) error {
	var req GetPeers
	if err := msg.Decode(&req); err != nil {
		// GetPeers has no payload; anything is a decode error.
		peer.MisbehavingFor("disc-getpeers-decode")
		return fmt.Errorf("disc: GetPeers decode: %w", err)
	}
	// Block-relay-only outbound peers don't participate in address
	// gossip (Bitcoin Core src/net_processing.cpp:3681). Silently
	// drop the request without responding — answering would leak
	// our addrbook sample to a peer that committed not to gossip.
	if peer.BlockRelayOnly() {
		return nil
	}
	count := st.getPeersReceived.Add(1)
	if count > 1 {
		// Bitcoin parity: repeat GetPeers in the same session is a
		// silent no-op. Don't even log at info — this is expected
		// under adversarial probing.
		backend.Log().Trace("parallax-disc/1: ignoring repeat GetPeers", "peer", peer.ID())
		return nil
	}
	// Apply Poisson jitter so the response doesn't arrive on a
	// predictable cadence — matches Bitcoin's PoissonNextSend
	// scheduling on address trickle. Mean is tunable so tests can
	// drop it to zero; production keeps the 2s mean. The max-delay
	// cap is a practical truncation against Poisson's long tail (a
	// natural draw can exceed 10× mean; we cap at 3× to bound worst
	// case).
	if mean := peersResponseJitterMean; mean > 0 {
		delay := poissonDelay(mean)
		if delay > 3*mean {
			delay = 3 * mean
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	sample := backend.SamplePeers(peer, MaxPeersPerMessage)
	if sample == nil {
		sample = []PeerEntry{}
	}
	if len(sample) > MaxPeersPerMessage {
		sample = sample[:MaxPeersPerMessage]
	}
	// Mark these addresses as sent-to-this-peer so RelayAddress
	// doesn't re-relay them later in the session.
	for _, e := range sample {
		st.knownAddr.Add(addressKey(e.NetworkID, e.Addr, e.TCPPort))
	}
	return p2p.Send(rw, PeersMsg, Peers{Entries: sample})
}

// peersResponseJitterMean is the mean Poisson delay applied to
// GetPeers responses. Tests override via the SetPeersResponseJitter
// helper; production value matches Bitcoin's 2-second address-trickle
// cadence.
var peersResponseJitterMean = 2 * time.Second

// SetPeersResponseJitterMean overrides the Poisson mean used in the
// response delay. Exposed for tests; pass 0 to disable jitter.
func SetPeersResponseJitterMean(d time.Duration) { peersResponseJitterMean = d }

// poissonDelay returns a Poisson-distributed delay with the given mean.
// Follows Bitcoin's PoissonNextSend(mean) helper: draw U∈(0,1],
// delay = -ln(U) * mean.
func poissonDelay(mean time.Duration) time.Duration {
	u := 1.0 - mrand.Float64() // open interval (0, 1]
	return time.Duration(-math.Log(u) * float64(mean))
}

func handlePeers(backend Backend, peer *p2p.Peer, st *state, msg p2p.Msg) error {
	var pkt Peers
	if err := msg.Decode(&pkt); err != nil {
		peer.MisbehavingFor("disc-peers-decode")
		return fmt.Errorf("disc: Peers decode: %w", err)
	}
	if err := pkt.Validate(); err != nil {
		peer.MisbehavingFor("disc-peers-validate")
		return err
	}
	st.peersReceived.Add(1)

	// Unsolicited Peers messages are expected on an ongoing basis:
	// address relay (RelayAddress) pushes freshly-learned addresses to
	// its fan-out targets as single-entry Peers messages throughout a
	// session, exactly like Bitcoin Core's unsolicited ADDR pushes.
	// We therefore do NOT disconnect a peer for sending more than one
	// unsolicited Peers message. DoS is bounded the way Bitcoin Core
	// bounds it: MaxPeersPerMessage caps entries per message (oversize
	// is a disconnect via Validate above), and the per-peer ingest
	// token bucket in HandlePeers drops entries that exceed the addr
	// ingest rate. An earlier revision capped unsolicited messages at
	// one, which made honest relaying peers disconnect and discourage
	// each other and broke gossip between upgraded nodes.

	// Filter out skippable entries; disconnect on any shape violation.
	kept := pkt.Entries[:0]
	for i := range pkt.Entries {
		skip, err := pkt.Entries[i].Validate()
		if err != nil {
			peer.MisbehavingFor("disc-peerentry-shape")
			return err
		}
		if skip {
			continue
		}
		kept = append(kept, pkt.Entries[i])
	}
	backend.HandlePeers(peer, kept)
	return nil
}

func handleYourAddr(backend Backend, peer *p2p.Peer, st *state, msg p2p.Msg) error {
	var y YourAddr
	if err := msg.Decode(&y); err != nil {
		peer.MisbehavingFor("disc-youraddr-decode")
		return fmt.Errorf("disc: YourAddr decode: %w", err)
	}
	skip, err := y.Validate()
	if err != nil {
		peer.MisbehavingFor("disc-youraddr-shape")
		return err
	}
	if !st.gotYourAddr.CompareAndSwap(false, true) {
		// Bitcoin's version message is single-shot; we mirror that —
		// a second YourAddr is a protocol violation.
		peer.MisbehavingFor("disc-double-youraddr")
		return errors.New("disc: multiple YourAddr messages from one peer")
	}
	if skip {
		return nil
	}
	backend.HandleYourAddr(peer, y.NetworkID, y.Addr, y.TCPPort)
	return nil
}

// sendHello writes the local node's Hello (nonce, listen port,
// services). Idempotent via the CAS on sentHello — runs at most
// once per session, even if Run is restarted by a higher layer.
//
// On block-relay-only outbound sessions, ServiceRelayTx is cleared
// so the peer treats us as a block-relay-only neighbor on their
// side too (mirrors Bitcoin Core's PushNodeVersion which sets
// fRelay=false on m_block_relay_only outbound; src/net.cpp).
func sendHello(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter, st *state) error {
	if !st.sentHello.CompareAndSwap(false, true) {
		return nil
	}
	h := backend.LocalHello()
	if peer.BlockRelayOnly() {
		h.Services &^= ServiceRelayTx
	}
	return p2p.Send(rw, HelloMsg, h)
}

// handleHello processes the peer's Hello on the receive side.
// Single-shot per session via gotHello — a second Hello is a
// protocol violation. Decode + Validate + delegate to backend; the
// backend runs the self-connect nonce check and stores the entry
// for later cross-dial dedup lookups.
func handleHello(backend Backend, peer *p2p.Peer, st *state, msg p2p.Msg) error {
	var h Hello
	if err := msg.Decode(&h); err != nil {
		peer.MisbehavingFor("disc-hello-decode")
		return fmt.Errorf("disc: Hello decode: %w", err)
	}
	if !st.gotHello.CompareAndSwap(false, true) {
		peer.MisbehavingFor("disc-double-hello")
		return errors.New("disc: multiple Hello messages from one peer")
	}
	if err := h.Validate(); err != nil {
		peer.MisbehavingFor("disc-hello-validate")
		return err
	}
	return backend.HandleHello(peer, h)
}

// sendYourAddr is the handshake-time "here's what I see as your source"
// message. Idempotent — repeated calls in the same session are no-ops
// thanks to the CAS on sentYourAddr.
func sendYourAddr(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter, st *state) error {
	if !st.sentYourAddr.CompareAndSwap(false, true) {
		return nil
	}
	net, addr, port, ok := backend.ObserveTheirSource(peer)
	if !ok {
		// We can't resolve the peer's apparent source — common in
		// tests. Send an all-zero YourAddr so the peer knows we
		// support the subprotocol but has nothing actionable to
		// feed quorum. Matches the PIP-0006 "0 if unknown" rule
		// for TCPPort.
		return p2p.Send(rw, YourAddrMsg, YourAddr{})
	}
	return p2p.Send(rw, YourAddrMsg, YourAddr{
		NetworkID: net,
		Addr:      addr,
		TCPPort:   port,
	})
}

// RequestPeers sends a GetPeers on the session. Callers (the dialer in
// Phase 4) invoke this once per outbound session. Repeated calls are
// dropped silently. Backends that track solicited responses (the
// production AddrmanBackend) are notified so the response bypasses
// the ingest rate limit and its entries are excluded from onward
// relay — Bitcoin's m_getaddr_sent + getaddr bucket-credit pattern.
func RequestPeers(backend Backend, peer *p2p.Peer, st *state, rw p2p.MsgReadWriter) error {
	if !st.getPeersSent.CompareAndSwap(0, 1) {
		return nil
	}
	if noter, ok := backend.(interface{ NoteGetPeersSent(*p2p.Peer) }); ok {
		noter.NoteGetPeersSent(peer)
	}
	return p2p.Send(rw, GetPeersMsg, GetPeers{})
}
