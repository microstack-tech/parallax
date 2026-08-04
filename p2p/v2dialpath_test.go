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
	"errors"
	"net"
	"testing"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/p2p/netutil"
)

// TestDialV2RejectsNetRestrict — the shared v2 dial path enforces the
// operator's --netrestrict allowlist before opening any socket, for
// every caller class (plain, block-relay, feeler). Without this gate
// a node locked to a private range still dialed arbitrary public IPs
// drawn from addrman and DNS seeds.
func TestDialV2RejectsNetRestrict(t *testing.T) {
	t.Parallel()
	restrict := new(netutil.Netlist)
	restrict.Add("10.0.0.0/8")

	srv := &Server{
		Config: Config{NetRestrict: restrict, Logger: logging.Root()},
	}
	srv.log = logging.Root()

	outside := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 50), Port: 32110}
	if err := srv.DialV2(outside); !errors.Is(err, errV2DialRestricted) {
		t.Fatalf("DialV2 outside netrestrict = %v, want errV2DialRestricted", err)
	}
	if err := srv.DialV2BlockRelay(outside); !errors.Is(err, errV2DialRestricted) {
		t.Fatalf("DialV2BlockRelay outside netrestrict = %v, want errV2DialRestricted", err)
	}
	if err := srv.DialV2Feeler(outside); !errors.Is(err, errV2DialRestricted) {
		t.Fatalf("DialV2Feeler outside netrestrict = %v, want errV2DialRestricted", err)
	}
}

// TestDialedOutboundCountIn — the v2 dialer's budget counts live
// dynamically-dialed peers (block-relay included) and excludes
// feeler probes, inbound peers, and static dials, mirroring the dial
// scheduler's maxDialPeers accounting.
func TestDialedOutboundCountIn(t *testing.T) {
	dyn := newOutboundPeerAt(t, net.IPv4(1, 0, 0, 1), 32110)
	br := newOutboundPeerAt(t, net.IPv4(2, 0, 0, 1), 32110)
	br.rw.set(blockRelayConn, true)
	feeler := newOutboundPeerAt(t, net.IPv4(3, 0, 0, 1), 32110)
	feeler.rw.set(feelerConn, true)
	inbound := newOutboundPeerAt(t, net.IPv4(4, 0, 0, 1), 32110)
	inbound.rw.set(dynDialedConn, false)
	inbound.rw.set(inboundConn, true)
	static := newOutboundPeerAt(t, net.IPv4(5, 0, 0, 1), 32110)
	static.rw.set(dynDialedConn, false)
	static.rw.set(staticDialedConn, true)

	if got := dialedOutboundCountIn(peerSet(dyn, br, feeler, inbound, static)); got != 2 {
		t.Fatalf("dialedOutboundCountIn = %d, want 2 (dyn + block-relay)", got)
	}
}

// TestPostHandshakeFeelerExemptFromMaxPeers — feeler probes connect
// regardless of the MaxPeers ceiling, as Core's feelers do; a regular
// outbound dial at saturation is still hard-rejected. Rejecting
// feelers at saturation would silently stop tried-table maintenance
// exactly when the node is busiest.
func TestPostHandshakeFeelerExemptFromMaxPeers(t *testing.T) {
	srv := newSelfEndpointServer(t, net.ParseIP("1.2.3.4"), 30303) // MaxPeers: 1

	peers := map[enode.ID]*Peer{}
	occupant := NewPeer(randomID(), "occupant", nil)
	peers[occupant.ID()] = occupant

	node := enode.SignNull(new(enr.Record), randomID())

	full := &conn{flags: dynDialedConn | v2DialedConn, node: node}
	if err := srv.postHandshakeChecks(peers, 0, 0, full); !errors.Is(err, DiscTooManyPeers) {
		t.Fatalf("full-relay dial at MaxPeers = %v, want DiscTooManyPeers", err)
	}
	probe := &conn{flags: dynDialedConn | v2DialedConn | feelerConn, node: node}
	if err := srv.postHandshakeChecks(peers, 0, 0, probe); err != nil {
		t.Fatalf("feeler at MaxPeers = %v, want nil (exempt)", err)
	}
}

// TestV2DialGroupLimitExemptions — pins the exemption set of the
// outbound network-group rule on the v2 dial path. Only feelers are
// exempt; block-relay-only and anchor dials (blockRelayConn) must be
// group-limited. The original port exempted every dial with a
// non-zero extra flag, so this exact assertion is the F8 regression.
func TestV2DialGroupLimitExemptions(t *testing.T) {
	if !v2DialSubjectToGroupLimit(0) {
		t.Error("plain full-relay dial must be subject to the group limit")
	}
	if !v2DialSubjectToGroupLimit(blockRelayConn) {
		t.Error("block-relay/anchor dial must be subject to the group limit")
	}
	if v2DialSubjectToGroupLimit(feelerConn) {
		t.Error("feeler probe must be exempt from the group limit")
	}
}

// TestV2DialWantsBlockRelay — the addrman-driven v2 dialer fills
// full-relay slots before the block-relay-only bucket, mirroring
// pickDynDialFlags and Bitcoin Core's ThreadOpenConnections order.
// Defaults for context: MaxPeers=100, DialRatio=3 -> maxDialed=33,
// maxBlockRelay=2 -> full-relay target 31.
func TestV2DialWantsBlockRelay(t *testing.T) {
	cases := []struct {
		name                   string
		dialed, br, max, maxBR int
		want                   bool
	}{
		{"fresh node dials full-relay first", 0, 0, 33, 2, false},
		{"below full-relay target stays full-relay", 30, 0, 33, 2, false},
		{"full-relay target met fills block-relay", 31, 0, 33, 2, true},
		{"one block-relay short of budget", 32, 1, 33, 2, true},
		{"block-relay budget full", 33, 2, 33, 2, false},
		{"block-relay disabled", 31, 0, 33, 0, false},
		{"tiny budget: full-relay keeps priority", 0, 0, 2, 1, false},
		{"tiny budget: block-relay after full-relay", 1, 0, 2, 1, true},
	}
	for _, tc := range cases {
		if got := v2DialWantsBlockRelay(tc.dialed, tc.br, tc.max, tc.maxBR); got != tc.want {
			t.Errorf("%s: v2DialWantsBlockRelay(%d, %d, %d, %d) = %v, want %v",
				tc.name, tc.dialed, tc.br, tc.max, tc.maxBR, got, tc.want)
		}
	}
}
