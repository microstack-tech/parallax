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

	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/simulations/pipes"
)

// TestHandlePeersTorStoredWhenReachable is the PIP-0007 counterpart of
// TestHandlePeersUnreachableNotStored: once the reachability predicate
// says the node has an onion route, gossiped Tor v3 entries are stored
// in addrman alongside IP entries.
func TestHandlePeersTorStoredWhenReachable(t *testing.T) {
	m, err := addrman.New(addrman.Deterministic(20))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)
	b.SetReachableFunc(func(n addrman.NetID) bool {
		return n == addrman.NetIPv4 || n == addrman.NetIPv6 || n == addrman.NetTorV3
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

	fresh := uint64(time.Now().Unix())
	torAddr := make([]byte, 32)
	torAddr[0] = 0xAB
	i2pAddr := make([]byte, 32)
	i2pAddr[0] = 0xCD
	entries := []PeerEntry{
		{NetworkID: NetTorV3, Addr: torAddr, TCPPort: 32110, KeyType: KeyTypeNone, LastSeen: fresh},
		{NetworkID: NetIPv4, Addr: []byte{8, 8, 8, 8}, TCPPort: 32110, KeyType: KeyTypeNone, LastSeen: fresh},
		// I2P stays unreachable under this predicate — only the
		// networks the policy names are admitted.
		{NetworkID: NetI2P, Addr: i2pAddr, TCPPort: 32110, KeyType: KeyTypeNone, LastSeen: fresh},
	}
	b.ingestBucketFor(peer).Credit(10)
	b.HandlePeers(peer, entries)

	if got := m.Size(nil, nil); got != 2 {
		t.Fatalf("addrman size = %d, want 2 (ipv4 + tor_v3)", got)
	}
	torNetAddr, _ := addrman.NewNetAddr(addrman.NetTorV3, torAddr, 32110)
	if info := m.Lookup(torNetAddr); info == nil {
		t.Fatal("reachable Tor v3 entry missing from addrman")
	} else if info.KeyType != KeyTypeNone {
		t.Fatalf("Tor v3 entry stored with KeyType %d, want KeyTypeNone", info.KeyType)
	}
	i2pNetAddr, _ := addrman.NewNetAddr(addrman.NetI2P, i2pAddr, 32110)
	if info := m.Lookup(i2pNetAddr); info != nil {
		t.Fatalf("I2P entry stored despite being unreachable: %+v", info)
	}
}
