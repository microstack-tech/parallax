// Copyright 2026 The Parallax Protocol Authors
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

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

// proxiedPeerAt builds a fake outbound peer whose socket remote is the
// SOCKS5 proxy but whose recorded dial target is the given address —
// the shape every proxied peer has in production.
func proxiedPeerAt(t *testing.T, target addrman.NetAddr) *Peer {
	t.Helper()
	p := newOutboundPeerAt(t, net.IPv4(127, 0, 0, 1), 9050)
	p.rw.setDialTarget(target)
	return p
}

// TestProxiedPeersDontCrossDialDedup — regression for the proxied
// peer-count collapse: every proxied conn's RemoteAddr is the proxy,
// so cross-dial dedup used to match each new proxied peer against
// every existing one and tear down a healthy connection as a
// "duplicate". Dedup must key on the dialed target.
func TestProxiedPeersDontCrossDialDedup(t *testing.T) {
	srv := &Server{}

	a := proxiedPeerAt(t, testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 1), Port: 32110}))
	b := proxiedPeerAt(t, testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 2), Port: 32110}))
	peers := []*Peer{a, b}

	// A third proxied peer with a distinct target: no duplicate.
	c := proxiedPeerAt(t, testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 3), Port: 32110}))
	if dup := srv.findCrossDialDupIn(peers, c, 32110); dup != nil {
		t.Fatalf("distinct proxied targets flagged as duplicates: %v", dup)
	}
	// The same target twice IS a duplicate, proxy or not.
	dupB := proxiedPeerAt(t, testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 2), Port: 32110}))
	if dup := srv.findCrossDialDupIn(peers, dupB, 32110); dup != b {
		t.Fatalf("re-dialed proxied target not deduped: got %v, want peer b", dup)
	}
	// Onion peers are exempt from address dedup entirely.
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	o1, o2 := proxiedPeerAt(t, onion), proxiedPeerAt(t, onion)
	if dup := srv.findCrossDialDupIn([]*Peer{o1}, o2, 32110); dup != nil {
		t.Fatalf("onion peers must not address-dedup: %v", dup)
	}
}

// TestPeerAddrHelpersPreferDialedTarget — addrman feedback (Good /
// Connected / Attempt) and listen-addr dedup must see the dialed
// target, never the proxy's socket address.
func TestPeerAddrHelpersPreferDialedTarget(t *testing.T) {
	srv := &Server{}
	target := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 32110})
	p := proxiedPeerAt(t, target)

	if got, ok := peerRemoteAddr(p); !ok || !got.Equal(target) {
		t.Fatalf("peerRemoteAddr = %v/%v, want the dialed target", got, ok)
	}
	if got, ok := peerAdvertisedAddr(p); !ok || !got.Equal(target) {
		t.Fatalf("peerAdvertisedAddr = %v/%v, want the dialed target", got, ok)
	}
	la, ok := srv.peerListenAddr(p)
	if !ok || !la.IP.Equal(net.IPv4(198, 51, 100, 7)) || la.Port != 32110 {
		t.Fatalf("peerListenAddr = %v/%v, want the dialed target", la, ok)
	}

	// Onion peers: addrman feedback lands on the onion entry, and
	// there is no ip:port listen addr to dedup on.
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	po := proxiedPeerAt(t, onion)
	if got, ok := peerRemoteAddr(po); !ok || !got.Equal(onion) {
		t.Fatalf("onion peerRemoteAddr = %v/%v", got, ok)
	}
	if _, ok := srv.peerListenAddr(po); ok {
		t.Fatal("onion peer must have no ip:port listen addr")
	}

	// A plain direct peer keeps the socket-derived behavior.
	direct := newOutboundPeerAt(t, net.IPv4(192, 0, 2, 9), 32110)
	if got, ok := peerRemoteAddr(direct); !ok || got.String() != "192.0.2.9:32110" {
		t.Fatalf("direct peerRemoteAddr = %v/%v", got, ok)
	}
}

// TestAdoptDialTargetAndConnectedTo — the nonce dedup's survivor
// inherits the dropped outbound leg's dial target, and the dial path
// then treats that address as connected (the onion equivalent of
// alreadyConnectedTo).
func TestAdoptDialTargetAndConnectedTo(t *testing.T) {
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	outbound := proxiedPeerAt(t, onion)
	inbound := newOutboundPeerAt(t, net.IPv4(127, 0, 0, 1), 53230)
	inbound.rw.set(dynDialedConn, false)
	inbound.rw.set(inboundConn, true)

	inbound.AdoptDialTargetFrom(outbound)
	if got := inbound.rw.dialTarget(); !got.Equal(onion) {
		t.Fatalf("adopted target = %v, want the onion", got)
	}
	// A pre-existing target is never overwritten.
	other := proxiedPeerAt(t, testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 32110}))
	inbound.AdoptDialTargetFrom(other)
	if got := inbound.rw.dialTarget(); !got.Equal(onion) {
		t.Fatalf("adoption overwrote an existing target: %v", got)
	}

	// connectedToDialTarget consults live peers' targets; feelers are
	// ignored. Exercised through the pure peer scan the server method
	// wraps (the run loop isn't up in this test).
	peers := map[enode.ID]*Peer{randomID(): inbound}
	scan := func(target addrman.NetAddr) bool {
		for _, p := range peers {
			if p.rw.is(feelerConn) {
				continue
			}
			if pt := p.rw.dialTarget(); pt.Network != 0 && pt.Equal(target) {
				return true
			}
		}
		return false
	}
	if !scan(onion) {
		t.Fatal("adopted target not visible to the dial-suppression scan")
	}
	inbound.rw.set(feelerConn, true)
	if scan(onion) {
		t.Fatal("feeler target suppressed a dial")
	}
}
