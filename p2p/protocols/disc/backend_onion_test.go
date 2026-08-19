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

package disc

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/p2p"
	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
	"github.com/ParallaxProtocol/parallax/v2/p2p/simulations/pipes"
)

func testPeerOnPipe(t *testing.T) *p2p.Peer {
	t.Helper()
	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	t.Cleanup(func() { a.Close(); d.Close() })
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return p2p.NewPeerForTest(id, "test", nil, a)
}

func testOnionAddr(t *testing.T) addrman.NetAddr {
	t.Helper()
	var pub [32]byte
	pub[0] = 0x42
	na, err := addrman.NewNetAddr(addrman.NetTorV3, pub[:], 32110)
	if err != nil {
		t.Fatal(err)
	}
	return na
}

// TestSelfEntriesPerNetwork — PIP-0007 §3.3: onion peers are told only
// the onion address (never the IP), clearnet peers get IP + onion.
func TestSelfEntriesPerNetwork(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)
	// All networks reachable — the default posture without --onlynet.
	b.SetReachableFunc(func(addrman.NetID) bool { return true })
	// An operator override stands in for a quorum-confirmed IP.
	b.Q.SetOverride(NetIPv4, []byte{198, 51, 100, 7}, 32110)

	clearnetPeer := testPeerOnPipe(t)
	onionPeer := testPeerOnPipe(t)
	onionPeer.MarkOnionForTest()

	// No onion service yet: clearnet gets the IP, the onion peer gets
	// nothing (the IP must not cross a Tor circuit).
	if got := b.SelfEntries(clearnetPeer, 32110); len(got) != 1 || got[0].NetworkID != NetIPv4 {
		t.Fatalf("clearnet, no onion service: %v", got)
	}
	if got := b.SelfEntries(onionPeer, 32110); len(got) != 0 {
		t.Fatalf("onion peer must not learn our IP: %v", got)
	}

	// With the service up: clearnet gets both, the onion peer gets
	// the onion address only.
	onion := testOnionAddr(t)
	b.SetOnionService(onion)
	got := b.SelfEntries(clearnetPeer, 32110)
	if len(got) != 2 || got[0].NetworkID != NetIPv4 || got[1].NetworkID != NetTorV3 {
		t.Fatalf("clearnet with onion service: %v", got)
	}
	if string(got[1].Addr) != string(onion.Bytes()) || got[1].TCPPort != 32110 {
		t.Fatalf("onion entry mismatch: %+v", got[1])
	}
	if got[1].KeyType != KeyTypeNone {
		t.Fatalf("onion self entry KeyType = %d, want KeyTypeNone", got[1].KeyType)
	}
	if got := b.SelfEntries(onionPeer, 32110); len(got) != 1 || got[0].NetworkID != NetTorV3 {
		t.Fatalf("onion peer with onion service: %v", got)
	}

	// Service lost: back to the initial state.
	b.ClearOnionService()
	if got := b.SelfEntries(clearnetPeer, 32110); len(got) != 1 || got[0].NetworkID != NetIPv4 {
		t.Fatalf("clearnet after ClearOnionService: %v", got)
	}
}

// TestSelfEntriesReachabilityGate — Core's AddLocal refuses addresses
// on networks --onlynet excluded (net.cpp), so an --onlynet=ipv4 node
// with a live onion service never advertises it, and an --onlynet=onion
// node never advertises its IP claim.
func TestSelfEntriesReachabilityGate(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)
	b.Q.SetOverride(NetIPv4, []byte{198, 51, 100, 7}, 32110)
	b.SetOnionService(testOnionAddr(t))

	clearnetPeer := testPeerOnPipe(t)
	onionPeer := testPeerOnPipe(t)
	onionPeer.MarkOnionForTest()

	// --onlynet=ipv4: onion unreachable. The service stays up (inbound
	// is unaffected) but must not be gossiped anywhere.
	b.SetReachableFunc(func(n addrman.NetID) bool { return n == addrman.NetIPv4 })
	if got := b.SelfEntries(clearnetPeer, 32110); len(got) != 1 || got[0].NetworkID != NetIPv4 {
		t.Fatalf("onion unreachable, clearnet peer: %v, want IP claim only", got)
	}
	if got := b.SelfEntries(onionPeer, 32110); len(got) != 0 {
		t.Fatalf("onion unreachable, onion peer: %v, want nothing", got)
	}

	// --onlynet=onion: IPv4 unreachable. The IP claim is suppressed,
	// the onion address still propagates.
	b.SetReachableFunc(func(n addrman.NetID) bool { return n == addrman.NetTorV3 })
	if got := b.SelfEntries(clearnetPeer, 32110); len(got) != 1 || got[0].NetworkID != NetTorV3 {
		t.Fatalf("ipv4 unreachable, clearnet peer: %v, want onion only", got)
	}
	if got := b.SelfEntries(onionPeer, 32110); len(got) != 1 || got[0].NetworkID != NetTorV3 {
		t.Fatalf("ipv4 unreachable, onion peer: %v, want onion only", got)
	}
}

// TestYourAddrExcludesOnionAndProxiedPeers — PIP-0007 §3.2: peers
// whose transport hides our address (onion streams from the local Tor
// daemon, anything dialed through a proxy) never feed the self-address
// quorum.
func TestYourAddrExcludesOnionAndProxiedPeers(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)

	claim := []byte{203, 0, 113, 50}

	onionPeer := testPeerOnPipe(t)
	onionPeer.MarkOnionForTest()
	b.HandleYourAddr(onionPeer, NetIPv4, claim, 32110)

	proxiedPeer := testPeerOnPipe(t)
	proxiedPeer.MarkProxiedForTest()
	b.HandleYourAddr(proxiedPeer, NetIPv4, claim, 32110)

	if stats := b.Q.Stats(); len(stats) != 0 {
		t.Fatalf("onion/proxied reports reached quorum tally: %+v", stats)
	}

	// A plain peer's report still lands.
	plain := testPeerOnPipe(t)
	b.HandleYourAddr(plain, NetIPv4, claim, 32110)
	if stats := b.Q.Stats(); len(stats) != 1 {
		t.Fatalf("plain peer's report missing from tally: %+v", stats)
	}
}

// TestObserveTheirSourceProxied — a proxied conn's RemoteAddr is the
// proxy; YourAddr must go out all-zero instead of naming our SOCKS5
// endpoint.
func TestObserveTheirSourceProxied(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)

	plain := testPeerOnPipe(t)
	if _, _, _, ok := b.ObserveTheirSource(plain); !ok {
		t.Fatal("plain TCP peer must be observable")
	}
	proxied := testPeerOnPipe(t)
	proxied.MarkProxiedForTest()
	if _, _, _, ok := b.ObserveTheirSource(proxied); ok {
		t.Fatal("proxied peer must not be observable — RemoteAddr is the proxy")
	}
}

// TestOwnOnionAddressNotStored — our own onion address is advertised
// on outbound clearnet sessions, so it propagates and returns through
// gossip. Regression: the self-filter projected entries onto
// *net.TCPAddr, which onion entries have none of, so the node stored
// its own onion address in its own addrbook — a self-loop that
// survived restarts via addrbook.rlp.
func TestOwnOnionAddressNotStored(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)
	b.SetReachableFunc(func(n addrman.NetID) bool {
		return n == addrman.NetIPv4 || n == addrman.NetTorV3
	})
	self := testOnionAddr(t)
	b.SetOnionService(self)

	other, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	peer := testPeerOnPipe(t)
	fresh := uint64(time.Now().Unix())
	b.ingestBucketFor(peer).Credit(10)
	b.HandlePeers(peer, []PeerEntry{
		// Our own service, echoed back on a different port than we
		// advertise — still us.
		{NetworkID: NetTorV3, Addr: self.Bytes(), TCPPort: 33000, KeyType: KeyTypeNone, LastSeen: fresh},
		{NetworkID: NetTorV3, Addr: other.Bytes(), TCPPort: 32110, KeyType: KeyTypeNone, LastSeen: fresh},
	})

	selfEchoed, err := addrman.NewNetAddr(addrman.NetTorV3, self.Bytes(), 33000)
	if err != nil {
		t.Fatal(err)
	}
	if info := m.Lookup(selfEchoed); info != nil {
		t.Fatal("node stored its own onion address from gossip")
	}
	if info := m.Lookup(other); info == nil {
		t.Fatal("a different peer's onion address was dropped")
	}
}
