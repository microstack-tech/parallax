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

	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
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

// TestConnectedToDialTargetScan — the dial-suppression scan matches
// live peers by recorded dial target (the only handle onion peers
// offer) and ignores feeler probes.
func TestConnectedToDialTargetScan(t *testing.T) {
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	peer := proxiedPeerAt(t, onion)
	peers := map[enode.ID]*Peer{randomID(): peer}
	// Mirrors the scan inside Server.connectedToDialTarget, which
	// needs a run loop this test doesn't stand up.
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
		t.Fatal("onion dial target not matched")
	}
	other := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 32110})
	if scan(other) {
		t.Fatal("unrelated target matched")
	}
	peer.rw.set(feelerConn, true)
	if scan(onion) {
		t.Fatal("feeler target suppressed a dial")
	}
}

// TestDisconnectMatchesDialTarget — peer-targeting RPCs (removePeer,
// setban's kick) must find a peer by the address it was dialed at.
// Regression: matching on RemoteAddr meant a proxied peer could not be
// removed by its real address, while passing the proxy's address
// disconnected an arbitrary peer sharing that proxy.
func TestDisconnectMatchesDialTarget(t *testing.T) {
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	real := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 32110})
	proxyAddr := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9050})

	onionPeer := proxiedPeerAt(t, onion)
	ipPeer := proxiedPeerAt(t, real) // socket remote is the proxy
	peers := []*Peer{onionPeer, ipPeer}

	// Mirrors DisconnectMatching's selection, which needs a run loop
	// this test doesn't stand up.
	match := func(target addrman.NetAddr) []*Peer {
		var hit []*Peer
		for _, p := range peers {
			cand := p.rw.dialTarget()
			if cand.Network == 0 && !p.rw.is(proxiedConn) {
				if ra, ok := p.RemoteAddr().(*net.TCPAddr); ok {
					if na, ok := netAddrFromTCP(ra); ok {
						cand = na
					}
				}
			}
			if cand.Network != 0 && cand.Equal(target) {
				hit = append(hit, p)
			}
		}
		return hit
	}

	if got := match(onion); len(got) != 1 || got[0] != onionPeer {
		t.Fatalf("onion address matched %d peers, want exactly the onion peer", len(got))
	}
	if got := match(real); len(got) != 1 || got[0] != ipPeer {
		t.Fatalf("real address matched %d peers, want exactly the proxied IP peer", len(got))
	}
	if got := match(proxyAddr); len(got) != 0 {
		t.Fatalf("the proxy's own address matched %d peers, want none", len(got))
	}
}

// TestV2ConnDuplicateUnderProxy — the post-handshake duplicate check
// must identify a v2 peer by the address it was dialed at. Regression
// found in live testing: it compared socket addresses, which under
// --proxy are all the SOCKS5 proxy, so the second proxied peer was
// always rejected as a duplicate of the first and the node could never
// hold more than one proxied peer.
func TestV2ConnDuplicateUnderProxy(t *testing.T) {
	proxyRemote := func() net.Conn {
		return &fakeAddrConn{remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9050}}
	}
	first := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 1), Port: 32110})
	second := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(203, 0, 113, 2), Port: 32110})

	existing := proxiedPeerAt(t, first)
	peers := map[enode.ID]*Peer{randomID(): existing}

	// A different target through the same proxy is not a duplicate.
	c := &conn{fd: proxyRemote(), flags: dynDialedConn | proxiedConn}
	c.setDialTarget(second)
	if v2ConnDuplicate(peers, c) {
		t.Fatal("distinct proxied targets treated as duplicates")
	}
	// The same target is.
	dup := &conn{fd: proxyRemote(), flags: dynDialedConn | proxiedConn}
	dup.setDialTarget(first)
	if !v2ConnDuplicate(peers, dup) {
		t.Fatal("re-dial of a connected target not detected")
	}
	// A proxied conn with no target (hostname seed fetch) is
	// unidentifiable and never a duplicate.
	anon := &conn{fd: proxyRemote(), flags: dynDialedConn | proxiedConn}
	if v2ConnDuplicate(peers, anon) {
		t.Fatal("unidentifiable proxied conn treated as a duplicate")
	}
}

// TestFeelerNeverBlocksRealConnection — a feeler probe must not make a
// real connection to the same address look like a duplicate. On a cold
// start the addr-fetch probes exactly the bootstrap addresses the
// dialer needs; with a single onion bootnode that is the only address
// it has, and the node ended up with no real peers at all.
func TestFeelerNeverBlocksRealConnection(t *testing.T) {
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	probe := proxiedPeerAt(t, onion)
	probe.rw.set(feelerConn, true)
	peers := map[enode.ID]*Peer{randomID(): probe}

	real := &conn{
		fd:    &fakeAddrConn{remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9050}},
		flags: dynDialedConn | proxiedConn | onionConn,
	}
	real.setDialTarget(onion)
	if v2ConnDuplicate(peers, real) {
		t.Fatal("feeler probe blocked a real connection to the same address")
	}

	// Once the real peer is attached, a further dial IS a duplicate.
	attached := proxiedPeerAt(t, onion)
	peers[randomID()] = attached
	if !v2ConnDuplicate(peers, real) {
		t.Fatal("duplicate of an attached real peer not detected")
	}
}
