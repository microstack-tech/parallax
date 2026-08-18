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
	"net"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
)

// TestOutboundGroupOccupiedIn — a full-relay outbound peer occupies
// its /16, an inbound peer does not (we don't choose inbound source
// networks), and a feeler probe does not (it is transient).
func TestOutboundGroupOccupiedIn(t *testing.T) {
	outbound := newOutboundPeerAt(t, net.IPv4(8, 8, 1, 1), 32110)

	inbound := newOutboundPeerAt(t, net.IPv4(9, 9, 1, 1), 32110)
	inbound.rw.set(inboundConn, true)

	feeler := newOutboundPeerAt(t, net.IPv4(10, 10, 1, 1), 32110)
	feeler.rw.set(feelerConn, true)

	peers := peerSet(outbound, inbound, feeler)

	// Same /16 as the outbound peer -> occupied.
	if !outboundGroupOccupiedIn(peers, ipNetworkGroupKey(net.IPv4(8, 8, 9, 9))) {
		t.Error("group of an existing full-relay outbound peer must be occupied")
	}
	// A distinct group -> not occupied.
	if outboundGroupOccupiedIn(peers, ipNetworkGroupKey(net.IPv4(1, 2, 3, 4))) {
		t.Error("unrelated group must not be occupied")
	}
	// The inbound peer's group must not count.
	if outboundGroupOccupiedIn(peers, ipNetworkGroupKey(net.IPv4(9, 9, 2, 2))) {
		t.Error("inbound peer's group must not be treated as occupied")
	}
	// The feeler peer's group must not count.
	if outboundGroupOccupiedIn(peers, ipNetworkGroupKey(net.IPv4(10, 10, 2, 2))) {
		t.Error("feeler peer's group must not be treated as occupied")
	}
}

// TestBlockRelayOutboundCountIn — counts only outbound block-relay
// peers.
func TestBlockRelayOutboundCountIn(t *testing.T) {
	br1 := newOutboundPeerAt(t, net.IPv4(1, 0, 0, 1), 32110)
	br1.rw.set(blockRelayConn, true)
	br2 := newOutboundPeerAt(t, net.IPv4(2, 0, 0, 1), 32110)
	br2.rw.set(blockRelayConn, true)
	full := newOutboundPeerAt(t, net.IPv4(3, 0, 0, 1), 32110)
	inboundBR := newOutboundPeerAt(t, net.IPv4(4, 0, 0, 1), 32110)
	inboundBR.rw.set(blockRelayConn, true)
	inboundBR.rw.set(inboundConn, true)

	if got := blockRelayOutboundCountIn(peerSet(br1, br2, full, inboundBR)); got != 2 {
		t.Fatalf("blockRelayOutboundCountIn = %d, want 2", got)
	}
}

// TestDialSchedFeelerExcludedFromBudget — a feeler peer must not
// consume an outbound slot or occupy a network-group slot, so a
// same-/16 candidate is still dialed and the dial budget is intact.
func TestDialSchedFeelerExcludedFromBudget(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	runDialTest(t, config, []dialTestRound{
		{
			// A feeler peer in 8.8.x.x is connected. Because feelers
			// are excluded from the group budget, a candidate in the
			// same /16 must still be dialed.
			peersAdded: []*conn{
				{flags: dynDialedConn | feelerConn, node: newNode(uintID(0x01), "8.8.1.1:32110")},
			},
			discovered: []*enode.Node{
				newNode(uintID(0x20), "8.8.2.1:32110"),
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x20), "8.8.2.1:32110"),
			},
		},
	})
}
