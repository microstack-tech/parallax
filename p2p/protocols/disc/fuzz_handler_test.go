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
	"bytes"
	"io"
	"testing"

	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// FuzzHandlerDispatch — streams arbitrary (code, payload) pairs into the
// dispatcher and asserts the invariants PIP-0006 calls out for the handler
// state machine:
//
//   - No panic on any input.
//   - Per-peer counters stay bounded (atomic counters monotonic ≥ 0, never
//     wrap because the state struct caps them implicitly via ignore-repeat
//     logic).
//   - No crash on malformed RLP, oversize payload, or unknown message code
//     (unknown code just returns an error to the caller — dispatcher
//     surfaces it, session ends, no stale state).
//
// The handler is tested via a synthetic reader that yields the fuzzed
// bytes one message at a time. Each iteration is a fresh state — this is
// a dispatch fuzzer, not a full session replay.
func FuzzHandlerDispatch(f *testing.F) {
	// Seed corpus: representative codes with short payloads.
	f.Add(uint8(GetPeersMsg), []byte{0xc0})
	f.Add(uint8(PeersMsg), []byte{0xc0})
	f.Add(uint8(YourAddrMsg), []byte{0xc0})
	f.Add(uint8(HelloMsg), []byte{0xc0})
	f.Add(uint8(0xFF), []byte{})
	f.Add(uint8(PeersMsg), bytes.Repeat([]byte{0xff}, 256))
	f.Add(uint8(HelloMsg), bytes.Repeat([]byte{0xff}, 256))

	f.Fuzz(func(t *testing.T, code uint8, payload []byte) {
		if len(payload) > 16*1024 {
			payload = payload[:16*1024]
		}
		backend := &testBackend{obsOK: true}
		st := &state{}

		// Synthetic MsgReadWriter that serves exactly one message.
		rw := &fuzzRW{
			msg: p2p.Msg{
				Code:    uint64(code),
				Size:    uint32(len(payload)),
				Payload: bytes.NewReader(payload),
			},
		}
		// Must not panic for ANY input.
		_ = handleOne(backend, nil, rw, st)

		// Counter invariants: atomic counters never wrap, always ≤ a
		// small upper bound (handler increments by ≤ 1 per call, so
		// after a single dispatch each counter is in [0, 1]).
		if v := st.getPeersReceived.Load(); v > 1 {
			t.Errorf("getPeersReceived = %d, want ≤1 after one dispatch", v)
		}
		if v := st.getPeersSent.Load(); v > 1 {
			t.Errorf("getPeersSent = %d, want ≤1 after one dispatch", v)
		}
	})
}

// fuzzRW wraps a single p2p.Msg into the MsgReadWriter interface.
// WriteMsg discards (for Peers responses during fuzz). ReadMsg returns
// the preloaded message once, then io.EOF.
type fuzzRW struct {
	msg   p2p.Msg
	read  bool
	drain bytes.Buffer
}

func (f *fuzzRW) ReadMsg() (p2p.Msg, error) {
	if f.read {
		return p2p.Msg{}, io.EOF
	}
	f.read = true
	return f.msg, nil
}

func (f *fuzzRW) WriteMsg(m p2p.Msg) error {
	if m.Payload != nil && m.Size > 0 {
		_, _ = io.Copy(&f.drain, m.Payload)
	}
	return nil
}

// FuzzHandlerSessionDispatch — drives a whole session against ONE shared
// state: a valid Hello first, then two arbitrary fuzzed messages. The
// single-message fuzzer above can never get past the Hello-first gate for
// non-Hello codes, so without this the post-Hello bodies of handleGetPeers /
// handlePeers / handleYourAddr (and the double-Hello rejection) were never
// fuzzed at all.
//
// Invariants:
//
//   - No panic on any input at any point in the session.
//   - gotHello stays latched once set (a second Hello errors, never resets).
//   - Per-session counters advance by at most one per dispatch.
func FuzzHandlerSessionDispatch(f *testing.F) {
	f.Add(uint8(GetPeersMsg), []byte{0xc0}, uint8(PeersMsg), []byte{0xc0})
	f.Add(uint8(PeersMsg), []byte{0xc0}, uint8(YourAddrMsg), []byte{0xc0})
	f.Add(uint8(HelloMsg), []byte{0xc0}, uint8(GetPeersMsg), []byte{0xc0}) // double-Hello then request
	f.Add(uint8(YourAddrMsg), bytes.Repeat([]byte{0xff}, 256), uint8(0xFF), []byte{})
	f.Add(uint8(PeersMsg), bytes.Repeat([]byte{0xff}, 1024), uint8(PeersMsg), []byte{0xc0})

	validHello, err := rlp.EncodeToBytes(&Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 7})
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, code1 uint8, p1 []byte, code2 uint8, p2 []byte) {
		if len(p1) > 16*1024 {
			p1 = p1[:16*1024]
		}
		if len(p2) > 16*1024 {
			p2 = p2[:16*1024]
		}
		backend := &testBackend{obsOK: true}
		st := &state{}
		// A real pipe-backed peer, unlike the nil peer the single-
		// message fuzzer gets away with: post-Hello handlers read
		// peer state (BlockRelayOnly, ID) beyond the nil-safe
		// MisbehavingFor.
		peer := p2p.NewPeer(enode.ID{0xfa}, "fuzz", nil)

		dispatch := func(code uint8, payload []byte) {
			rw := &fuzzRW{
				msg: p2p.Msg{
					Code:    uint64(code),
					Size:    uint32(len(payload)),
					Payload: bytes.NewReader(payload),
				},
			}
			_ = handleOne(backend, peer, rw, st)
		}

		// Establish the session: a valid Hello unlocks the post-Hello
		// handlers for the fuzzed dispatches that follow.
		dispatch(uint8(HelloMsg), validHello)
		if !st.gotHello.Load() {
			t.Fatal("valid Hello did not latch gotHello")
		}
		dispatch(code1, p1)
		dispatch(code2, p2)

		if !st.gotHello.Load() {
			t.Error("gotHello was reset mid-session")
		}
		if v := st.getPeersReceived.Load(); v > 2 {
			t.Errorf("getPeersReceived = %d, want ≤2 after two fuzzed dispatches", v)
		}
		if v := st.peersReceived.Load(); v > 2 {
			t.Errorf("peersReceived = %d, want ≤2 after two fuzzed dispatches", v)
		}
	})
}
