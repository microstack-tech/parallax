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

	"github.com/ParallaxProtocol/parallax/internal/testlog"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/nat"
)

// TestOnionOnlyMutesClearnetSurface verifies PIP-0007 §1.4: a node
// restricted to onion outbound opens no discv4 UDP socket and performs
// no NAT traversal, regardless of --legacy-discovery.
func TestOnionOnlyMutesClearnetSurface(t *testing.T) {
	srv := &Server{Config: Config{
		Name:           "onion-only",
		MaxPeers:       10,
		NoDial:         true,
		ListenAddr:     "127.0.0.1:0",
		PrivateKey:     newkey(),
		OnlyNet:        []string{"onion"},
		OnionProxyAddr: "127.0.0.1:9050",
		NAT:            nat.ExtIP(net.IP{198, 51, 100, 1}),
		Logger:         testlog.Logger(t, logging.LvlTrace),
	}}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	if srv.ntab != nil {
		t.Error("onion-only node opened a discv4 UDP socket")
	}
	if srv.NAT != nil {
		t.Error("onion-only node kept its NAT mapper")
	}
	if srv.netpol.clearnetReachable() {
		t.Error("policy reports clearnet reachable under --onlynet=onion")
	}
}

// TestDefaultKeepsClearnetSurface pins the inverse: without onion
// restrictions the discv4 socket still comes up (legacy-discovery auto).
func TestDefaultKeepsClearnetSurface(t *testing.T) {
	srv := &Server{Config: Config{
		Name:       "clearnet",
		MaxPeers:   10,
		NoDial:     true,
		ListenAddr: "127.0.0.1:0",
		PrivateKey: newkey(),
		Logger:     testlog.Logger(t, logging.LvlTrace),
	}}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	if srv.ntab == nil {
		t.Error("default config must keep the discv4 responder up")
	}
}
