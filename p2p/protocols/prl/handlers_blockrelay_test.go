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

package prl

import (
	"errors"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

// trackingDecoder records whether Decode was called. handleTransactions
// and friends should NEVER attempt to decode a tx-bearing message from
// a block-relay-only peer (Bitcoin Core src/net_processing.cpp:3681 —
// m_relay_txs=false drops the message before any payload work).
type trackingDecoder struct {
	called bool
}

func (d *trackingDecoder) Decode(val any) error {
	d.called = true
	return errors.New("decode should not have been called for block-relay-only peer")
}

func (d *trackingDecoder) Time() time.Time { return time.Time{} }

// blockRelayPeer constructs a *Peer whose BlockRelayOnly() reports
// true, ready to be fed to the prl handlers under test.
func blockRelayPeer(t *testing.T) *Peer {
	t.Helper()
	var id enode.ID
	id[0] = 0xab
	p2pPeer := p2p.NewPeer(id, "br-test", nil)
	p2pPeer.SetBlockRelayOnly(true)
	p2pPeer.SetRelayTxs(false)
	return NewPeer(Parallax66, p2pPeer, nil, nil)
}

// TestBlockRelayOnlyDropsTxRelay covers the four tx-bearing message
// handlers in handlers.go: each must return nil without invoking the
// decoder when the peer is flagged block-relay-only. The decoder
// tracks the call so a regression that re-orders the BR check below
// the Decode line trips the test.
func TestBlockRelayOnlyDropsTxRelay(t *testing.T) {
	t.Parallel()

	peer := blockRelayPeer(t)
	defer peer.Close()
	if !peer.BlockRelayOnly() {
		t.Fatal("test peer should be block-relay-only")
	}

	cases := []struct {
		name string
		fn   func(Backend, Decoder, *Peer) error
	}{
		{"handleTransactions", handleTransactions},
		{"handleNewPooledTransactionHashes", handleNewPooledTransactionHashes},
		{"handlePooledTransactions66", handlePooledTransactions66},
		{"handleGetPooledTransactions66", handleGetPooledTransactions66},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &trackingDecoder{}
			// Backend is intentionally nil — a regression that passes
			// the BR gate would also nil-deref on backend access,
			// distinguishing the bug from a silent decode-then-drop.
			if err := tc.fn(nil, d, peer); err != nil {
				t.Fatalf("%s returned err: %v", tc.name, err)
			}
			if d.called {
				t.Fatalf("%s decoded a tx message from block-relay-only peer", tc.name)
			}
		})
	}
}

// TestFeelerDropsTxRelay — feelers are short-lived liveness probes
// and take no part in tx relay (Bitcoin: RejectIncomingTxs covers
// ConnectionType::FEELER). Each tx-bearing handler must drop the
// message before any payload work, exactly like block-relay-only.
func TestFeelerDropsTxRelay(t *testing.T) {
	t.Parallel()

	var id enode.ID
	id[0] = 0xfe
	p2pPeer := p2p.NewPeer(id, "feeler-test", nil)
	p2pPeer.MarkFeelerForTest()
	peer := NewPeer(Parallax66, p2pPeer, nil, nil)
	defer peer.Close()
	if !peer.Feeler() {
		t.Fatal("test peer should be a feeler")
	}

	cases := []struct {
		name string
		fn   func(Backend, Decoder, *Peer) error
	}{
		{"handleTransactions", handleTransactions},
		{"handleNewPooledTransactionHashes", handleNewPooledTransactionHashes},
		{"handlePooledTransactions66", handlePooledTransactions66},
		{"handleGetPooledTransactions66", handleGetPooledTransactions66},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &trackingDecoder{}
			if err := tc.fn(nil, d, peer); err != nil {
				t.Fatalf("%s returned err: %v", tc.name, err)
			}
			if d.called {
				t.Fatalf("%s decoded a tx message from a feeler", tc.name)
			}
		})
	}
}
