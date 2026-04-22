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
	"sync/atomic"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p"
)

// Backend is the host-integration surface. The handler calls into Backend
// for observed-address reports, addrbook ingest, and addrbook sampling.
// Phase 2 keeps this interface minimal — the real addrman wiring lands
// in Phase 4 when we give the Backend implementation a real addrman.
type Backend interface {
	// ObserveTheirSource records the observed remote TCP source of an
	// inbound or outbound connection. Used to compose our outgoing
	// YourAddr message and, on the peer's side, to feed quorum.
	ObserveTheirSource(peer *p2p.Peer) (network uint8, addr []byte, port uint16, ok bool)

	// HandleYourAddr feeds a peer's reported view of our external
	// address into the quorum tally.
	HandleYourAddr(peer *p2p.Peer, net uint8, addr []byte, port uint16)

	// HandlePeers ingests gossiped entries. Implementations apply the
	// per-peer rate limits and addrman ingest (Phase 4); Phase 2's
	// no-op implementation just logs.
	HandlePeers(peer *p2p.Peer, entries []PeerEntry)

	// SamplePeers returns up to max entries for a GetPeers response,
	// subject to reachability filtering. Phase 2 may return nil; the
	// handler still sends a valid (empty) Peers message.
	SamplePeers(peer *p2p.Peer, max int) []PeerEntry

	// Log returns the logger to use for protocol-level events.
	Log() logging.Logger
}

// state holds per-peer handler state. Rate-limit token buckets and the
// rolling known-address bloom filter land here in Phase 4 — for Phase 2
// we only track whether we've negotiated YourAddr and how many Peers
// messages we've seen.
type state struct {
	// sentYourAddr: we've written our YourAddr message for this
	// session. Each side sends exactly one, as the first message after
	// capability negotiation.
	sentYourAddr atomic.Bool

	// gotYourAddr: we've seen the peer's YourAddr for this session.
	gotYourAddr atomic.Bool

	// peersReceived counts Peers messages received on this session.
	// Used by unsolicited-rate enforcement (Phase 4).
	peersReceived atomic.Uint32

	// getPeersSent counts GetPeers requests we've issued to this peer.
	// One-request-per-session is the rule (Bitcoin parity); repeated
	// GetPeers from the peer are ignored silently.
	getPeersSent atomic.Uint32

	// getPeersReceived — same, but for GetPeers from the peer. Phase 4
	// replies once per session and ignores further requests.
	getPeersReceived atomic.Uint32
}

// Run is the per-peer entry point. Called by p2p.Server once the
// subprotocol has been negotiated.
func Run(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter) error {
	log := backend.Log().New("peer", peer.ID())
	log.Trace("parallax-disc/1 session starting")

	st := &state{}

	// First action on both sides: send our YourAddr report about the
	// remote. Order-independent because RLPx is multiplexed — either
	// message may arrive first at the receiver.
	if err := sendYourAddr(backend, peer, rw, st); err != nil {
		// Failing to send YourAddr is non-fatal at this layer; we log
		// and continue. Quorum is best-effort.
		log.Debug("parallax-disc/1: YourAddr send failed", "err", err)
	}

	for {
		if err := handleOne(backend, peer, rw, st); err != nil {
			log.Debug("parallax-disc/1: session ending", "err", err)
			return err
		}
	}
}

// handleOne reads and dispatches one inbound message. Returns on read
// error, oversized payload, or protocol violation — the caller closes
// the session.
func handleOne(backend Backend, peer *p2p.Peer, rw p2p.MsgReadWriter, st *state) error {
	msg, err := rw.ReadMsg()
	if err != nil {
		return err
	}
	defer msg.Discard()

	if msg.Size > MaxMessageSize {
		return fmt.Errorf("disc: message too large: %d > %d", msg.Size, MaxMessageSize)
	}

	switch msg.Code {
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
		return fmt.Errorf("disc: GetPeers decode: %w", err)
	}
	count := st.getPeersReceived.Add(1)
	if count > 1 {
		// Bitcoin parity: repeat GetPeers in the same session is a
		// silent no-op. Don't even log at info — this is expected
		// under adversarial probing.
		backend.Log().Trace("parallax-disc/1: ignoring repeat GetPeers", "peer", peer.ID())
		return nil
	}
	sample := backend.SamplePeers(peer, MaxPeersPerMessage)
	if sample == nil {
		sample = []PeerEntry{}
	}
	if len(sample) > MaxPeersPerMessage {
		sample = sample[:MaxPeersPerMessage]
	}
	return p2p.Send(rw, PeersMsg, Peers{Entries: sample})
}

func handlePeers(backend Backend, peer *p2p.Peer, st *state, msg p2p.Msg) error {
	var pkt Peers
	if err := msg.Decode(&pkt); err != nil {
		return fmt.Errorf("disc: Peers decode: %w", err)
	}
	if err := pkt.Validate(); err != nil {
		return err
	}
	st.peersReceived.Add(1)

	// Filter out skippable entries; disconnect on any shape violation.
	kept := pkt.Entries[:0]
	for i := range pkt.Entries {
		skip, err := pkt.Entries[i].Validate()
		if err != nil {
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
		return fmt.Errorf("disc: YourAddr decode: %w", err)
	}
	skip, err := y.Validate()
	if err != nil {
		return err
	}
	if !st.gotYourAddr.CompareAndSwap(false, true) {
		// Bitcoin's version message is single-shot; we mirror that —
		// a second YourAddr is a protocol violation.
		return errors.New("disc: multiple YourAddr messages from one peer")
	}
	if skip {
		return nil
	}
	backend.HandleYourAddr(peer, y.NetworkID, y.Addr, y.TCPPort)
	return nil
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
// dropped silently.
func RequestPeers(st *state, rw p2p.MsgReadWriter) error {
	if st.getPeersSent.Load() >= 1 {
		return nil
	}
	st.getPeersSent.Add(1)
	return p2p.Send(rw, GetPeersMsg, GetPeers{})
}
