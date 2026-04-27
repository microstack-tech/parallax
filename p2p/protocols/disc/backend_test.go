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
	"net"
	"testing"

	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/simulations/pipes"
)

// TestSelfTCPFromEntry — projects PeerEntry onto the *net.TCPAddr
// shape IsSelfFunc consumes. IPv4/IPv6 succeed; other networks and
// malformed addr lengths return ok=false.
func TestSelfTCPFromEntry(t *testing.T) {
	cases := []struct {
		name   string
		e      PeerEntry
		want   *net.TCPAddr
		wantOk bool
	}{
		{
			name:   "ipv4",
			e:      PeerEntry{NetworkID: NetIPv4, Addr: []byte{45, 236, 49, 58}, TCPPort: 32110},
			want:   &net.TCPAddr{IP: net.IPv4(45, 236, 49, 58), Port: 32110},
			wantOk: true,
		},
		{
			name:   "ipv6",
			e:      PeerEntry{NetworkID: NetIPv6, Addr: net.ParseIP("2001:db8::1").To16(), TCPPort: 32110},
			want:   &net.TCPAddr{IP: net.ParseIP("2001:db8::1").To16(), Port: 32110},
			wantOk: true,
		},
		{
			name:   "ipv4 wrong length",
			e:      PeerEntry{NetworkID: NetIPv4, Addr: []byte{1, 2, 3}, TCPPort: 32110},
			wantOk: false,
		},
		{
			name:   "unknown net",
			e:      PeerEntry{NetworkID: 0xEE, Addr: []byte{1, 2, 3, 4}, TCPPort: 32110},
			wantOk: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selfTCPFromEntry(tc.e)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if !ok {
				return
			}
			if got.Port != tc.want.Port {
				t.Fatalf("port = %d, want %d", got.Port, tc.want.Port)
			}
			if !got.IP.Equal(tc.want.IP) {
				t.Fatalf("ip = %v, want %v", got.IP, tc.want.IP)
			}
		})
	}
}

// TestAddrmanBackendHandlePeersFiltersSelf — when an incoming Peers
// message contains an entry that matches the local node's advertised
// endpoint, AddrmanBackend.HandlePeers must drop it before writing
// to addrman. Otherwise the node's own external IP — propagated via
// our own self-advertise on outbound sessions — comes back as a
// gossip entry and is stored as a v2 dial candidate that survives
// across restarts via addrbook.rlp.
func TestAddrmanBackendHandlePeersFiltersSelf(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(11))
	if err != nil {
		t.Fatal(err)
	}
	selfIP := net.IPv4(45, 236, 49, 58)
	const selfPort = 32110

	isSelf := func(addr *net.TCPAddr) bool {
		return addr.Port == selfPort && addr.IP.Equal(selfIP)
	}
	b := NewAddrmanBackend(m, nil, nil, isSelf, nil)

	// Peer with a real TCP RemoteAddr so peerNetworkGroup succeeds.
	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "test", nil, a)

	entries := []PeerEntry{
		// Self entry — must be dropped.
		{NetworkID: NetIPv4, Addr: []byte{45, 236, 49, 58}, TCPPort: selfPort, KeyType: KeyTypeNone},
		// Foreign entry — must reach addrman.
		{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 32110, KeyType: KeyTypeNone},
	}
	b.HandlePeers(peer, entries)

	if got := m.Size(nil, nil); got != 1 {
		t.Fatalf("addrman size after ingest = %d, want 1 (self should be filtered)", got)
	}
	selfNetAddr, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{45, 236, 49, 58}, selfPort)
	if info := m.Lookup(selfNetAddr); info != nil {
		t.Fatalf("self entry leaked into addrman: %+v", info)
	}
	otherNetAddr, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{8, 8, 8, 8}, 32110)
	if info := m.Lookup(otherNetAddr); info == nil {
		t.Fatalf("foreign entry missing from addrman")
	}
}

// TestLocalHelloDefaultWithNilProvider — when no helloProvider is
// configured, LocalHello returns a zero-ish Hello with ProtoVersion
// at the minimum so callers can still send a valid greeting.
func TestLocalHelloDefaultWithNilProvider(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)
	h := b.LocalHello()
	if h.ProtoVersion != HelloMinProtoVersion {
		t.Fatalf("ProtoVersion = %d, want %d", h.ProtoVersion, HelloMinProtoVersion)
	}
	if h.Nonce != 0 || h.ListenPort != 0 || h.Services != 0 {
		t.Fatalf("non-zero fields with nil provider: %+v", h)
	}
}

// TestLocalHelloUsesProvider — LocalHello forwards the provider's
// output verbatim. Wires the Server.HelloNonce/listen port plumbing
// from node.go.
func TestLocalHelloUsesProvider(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(21))
	if err != nil {
		t.Fatal(err)
	}
	want := Hello{
		ProtoVersion: 1,
		Nonce:        0xCAFEBABE,
		ListenPort:   32110,
		Services:     ServiceNodeNetwork | ServiceRelayTx,
	}
	b := NewAddrmanBackend(m, nil, nil, nil, func() Hello { return want })
	if got := b.LocalHello(); got.Nonce != want.Nonce || got.ListenPort != want.ListenPort || got.Services != want.Services {
		t.Fatalf("LocalHello() = %+v, want %+v", got, want)
	}
}

// TestHandleHelloStoresAndLooksUp — HandleHello writes peerHello;
// PeerHello reads it back. Cross-dial dedup in phase 2 depends on
// this round-trip.
func TestHandleHelloStoresAndLooksUp(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(22))
	if err != nil {
		t.Fatal(err)
	}
	local := Hello{ProtoVersion: 1, Nonce: 0x1111}
	b := NewAddrmanBackend(m, nil, nil, nil, func() Hello { return local })

	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "test", nil, a)

	in := Hello{ProtoVersion: 1, Nonce: 0x2222, ListenPort: 32110, Services: ServiceNodeNetwork}
	if err := b.HandleHello(peer, in); err != nil {
		t.Fatalf("HandleHello: %v", err)
	}
	got, ok := b.PeerHello(peerKeyFor(peer))
	if !ok {
		t.Fatal("PeerHello returned ok=false after HandleHello stored")
	}
	if got.Nonce != in.Nonce || got.ListenPort != in.ListenPort || got.Services != in.Services {
		t.Fatalf("PeerHello returned %+v, want %+v", got, in)
	}
}

// TestHandleHelloDetectsSelfConnect — when the peer's nonce equals
// our own LocalHello().Nonce, HandleHello returns errSelfConnect and
// does NOT store the entry. The handler will end the session with
// DiscSelf upstream.
func TestHandleHelloDetectsSelfConnect(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(23))
	if err != nil {
		t.Fatal(err)
	}
	const sharedNonce uint64 = 0xDEADBEEF
	b := NewAddrmanBackend(m, nil, nil, nil, func() Hello {
		return Hello{ProtoVersion: 1, Nonce: sharedNonce}
	})

	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "test", nil, a)

	echoed := Hello{ProtoVersion: 1, Nonce: sharedNonce, ListenPort: 32110}
	err = b.HandleHello(peer, echoed)
	if !errors.Is(err, errSelfConnect) {
		t.Fatalf("HandleHello err = %v, want errSelfConnect", err)
	}
	if _, ok := b.PeerHello(peerKeyFor(peer)); ok {
		t.Fatal("self-connect Hello must NOT be stored in peerHello")
	}
}

// TestHandleHelloNilProviderSkipsSelfCheck — when no helloProvider
// is configured, the self-connect check is bypassed and Hello is
// stored normally. Lets test backends without a provider exercise
// the round-trip.
func TestHandleHelloNilProviderSkipsSelfCheck(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(24))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)

	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "test", nil, a)

	in := Hello{ProtoVersion: 1, Nonce: 1, ListenPort: 32110}
	if err := b.HandleHello(peer, in); err != nil {
		t.Fatalf("HandleHello: %v", err)
	}
	if _, ok := b.PeerHello(peerKeyFor(peer)); !ok {
		t.Fatal("PeerHello not populated when helloProvider is nil")
	}
}

// TestPeerDisconnectedClearsHello — closing the session purges the
// peer's recorded Hello; PeerHello returns ok=false afterward.
func TestPeerDisconnectedClearsHello(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(25))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)

	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "test", nil, a)

	in := Hello{ProtoVersion: 1, Nonce: 1, ListenPort: 32110}
	if err := b.HandleHello(peer, in); err != nil {
		t.Fatalf("HandleHello: %v", err)
	}
	b.PeerDisconnected(peer)
	if _, ok := b.PeerHello(peerKeyFor(peer)); ok {
		t.Fatal("PeerHello still present after PeerDisconnected")
	}
}

// TestAddrmanBackendHandlePeersNilSelfFn — passing a nil IsSelfFunc
// must not panic and must let all entries through. Lets tests / fuzz
// targets construct a backend without inventing a self-fn.
func TestAddrmanBackendHandlePeersNilSelfFn(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(12))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)

	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "test", nil, a)

	entries := []PeerEntry{
		{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 32110, KeyType: KeyTypeNone},
	}
	b.HandlePeers(peer, entries)

	if got := m.Size(nil, nil); got != 1 {
		t.Fatalf("addrman size = %d, want 1", got)
	}
}
