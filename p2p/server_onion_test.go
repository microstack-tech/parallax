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
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/internal/testlog"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
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
	if srv.policy().clearnetReachable() {
		t.Error("policy reports clearnet reachable under --onlynet=onion")
	}
}

const testOnionService = "2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid"

// startFakeTorControl runs a NULL-auth control port that grants every
// ADD_ONION with a fixed service ID. Returns its address.
func startFakeTorControl(t *testing.T) string {
	return startFakeTorControlSocks(t, "")
}

// startFakeTorControlSocks is startFakeTorControl with a configurable
// net/listeners/socks value; empty answers GETINFO with 510.
func startFakeTorControlSocks(t *testing.T, socksReply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				br := bufio.NewReader(conn)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimRight(line, "\r\n")
					switch {
					case cmd == "PROTOCOLINFO 1":
						fmt.Fprintf(conn, "250-PROTOCOLINFO 1\r\n250-AUTH METHODS=NULL\r\n250 OK\r\n")
					case strings.HasPrefix(cmd, "AUTHENTICATE"):
						fmt.Fprintf(conn, "250 OK\r\n")
					case strings.HasPrefix(cmd, "ADD_ONION "):
						fmt.Fprintf(conn, "250-ServiceID=%s\r\n250-PrivateKey=ED25519-V3:X\r\n250 OK\r\n", testOnionService)
					case cmd == "GETINFO net/listeners/socks" && socksReply != "":
						fmt.Fprintf(conn, "250-net/listeners/socks=%s\r\n250 OK\r\n", socksReply)
					default:
						fmt.Fprintf(conn, "510 Unrecognized command\r\n")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// TestListenOnionEstablishesService — full server wiring (PIP-0007
// §3): with --listenonion and a responsive control port, the service
// address is recorded, the wiring hook fires, our own onion address
// is recognized as self by the dial path, and loopback inbound gets
// classified as onion while the service is active.
func TestListenOnionEstablishesService(t *testing.T) {
	control := startFakeTorControl(t)

	var hookAddr addrman.NetAddr
	hookFired := make(chan struct{})
	srv := &Server{Config: Config{
		Name:           "onion-listener",
		MaxPeers:       10,
		NoDial:         true,
		NoDiscovery:    true,
		ListenAddr:     "127.0.0.1:0",
		PrivateKey:     newkey(),
		ListenOnion:    true,
		TorControlAddr: control,
		OnOnionService: func(na addrman.NetAddr) {
			hookAddr = na
			close(hookFired)
		},
		Logger: testlog.Logger(t, logging.LvlTrace),
	}}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	select {
	case <-hookFired:
	case <-time.After(5 * time.Second):
		t.Fatal("onion service never established")
	}
	self, ok := srv.OnionService()
	if !ok {
		t.Fatal("OnionService() not set after establishment")
	}
	if self.OnionHostname() != testOnionService+".onion" {
		t.Fatalf("service hostname = %s", self.OnionHostname())
	}
	if self.Port != DNSSeedDefaultPort {
		t.Fatalf("virtual port = %d, want the default port %d (decloaking guard)", self.Port, DNSSeedDefaultPort)
	}
	if !hookAddr.Equal(self) {
		t.Fatalf("wiring hook saw %v, server records %v", hookAddr, self)
	}

	// Our own onion address is self — the dial path must refuse it
	// before opening any socket, on any port.
	if !srv.isSelfNetAddr(self) {
		t.Fatal("own onion address not recognized as self")
	}
	if err := srv.DialV2(self); !errors.Is(err, errV2DialSelf) {
		t.Fatalf("DialV2(own onion) = %v, want errV2DialSelf", err)
	}

	// Loopback inbound is classified as onion while the service is
	// active; non-loopback inbound is not.
	loop := &fakeAddrConn{remoteAddr: &net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: 40000}}
	if flags := srv.classifyInboundNetwork(loop, inboundConn); flags&onionConn == 0 {
		t.Fatal("loopback inbound not classified as onion while service active")
	}
	remote := &fakeAddrConn{remoteAddr: &net.TCPAddr{IP: net.IP{203, 0, 113, 9}, Port: 40000}}
	if flags := srv.classifyInboundNetwork(remote, inboundConn); flags&onionConn != 0 {
		t.Fatal("non-loopback inbound classified as onion")
	}

	// After the service drops, loopback inbound is plain again.
	srv.onOnionLost()
	if flags := srv.classifyInboundNetwork(loop, inboundConn); flags&onionConn != 0 {
		t.Fatal("loopback inbound classified as onion after service loss")
	}
	if _, ok := srv.OnionService(); ok {
		t.Fatal("OnionService() still set after loss")
	}
}

// TestAutoOnionProxy — the closing PIP-0007 piece: with --listenonion
// and no --onion, the server learns the daemon's SOCKS listener via
// GETINFO, onion becomes reachable at runtime, and onion dials route
// through the discovered proxy. An explicit --onion suppresses the
// auto-configuration entirely.
func TestAutoOnionProxy(t *testing.T) {
	socks := startFakeSocksProxy(t)
	control := startFakeTorControlSocks(t, fmt.Sprintf("%q", socks.ln.Addr().String()))

	hookFired := make(chan struct{})
	srv := &Server{Config: Config{
		Name:           "auto-proxy",
		MaxPeers:       10,
		NoDial:         true,
		NoDiscovery:    true,
		ListenAddr:     "127.0.0.1:0",
		PrivateKey:     newkey(),
		ListenOnion:    true,
		TorControlAddr: control,
		OnOnionService: func(addrman.NetAddr) { close(hookFired) },
		Logger:         testlog.Logger(t, logging.LvlTrace),
	}}
	// Before the control port answers, onion has no route.
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	select {
	case <-hookFired:
	case <-time.After(5 * time.Second):
		t.Fatal("onion service never established")
	}
	// The SOCKS listener is fetched before ADD_ONION, so by now the
	// policy swap has happened.
	if !srv.NetworkReachable(addrman.NetTorV3) {
		t.Fatal("onion not reachable after auto-proxy configuration")
	}
	pr := srv.policy().proxyFor(addrman.NetTorV3)
	if pr == nil || pr.Addr != socks.ln.Addr().String() {
		t.Fatalf("onion proxy = %+v, want the discovered listener", pr)
	}
	if pr.Isolation == nil {
		t.Fatal("auto-proxy missing stream isolation")
	}

	// And it actually routes: an onion dial's CONNECT lands on the
	// discovered SOCKS listener. (Not our own service — that's self.)
	onion, err := addrman.ParseOnion("duckduckgogg42xjoc72x3sjasowoarfbgcmvfimaftt6twagswzczad.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.DialV2(onion)
	select {
	case dest := <-socks.destC:
		if dest != onion.OnionHostname() {
			t.Errorf("proxy saw %q, want %q", dest, onion.OnionHostname())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onion dial never reached the auto-configured proxy")
	}
}

// TestAutoOnionProxySuppressedByExplicitOnion — an operator-supplied
// --onion always wins: no GETINFO-driven override.
func TestAutoOnionProxySuppressedByExplicitOnion(t *testing.T) {
	socks := startFakeSocksProxy(t)
	control := startFakeTorControlSocks(t, fmt.Sprintf("%q", socks.ln.Addr().String()))

	hookFired := make(chan struct{})
	srv := &Server{Config: Config{
		Name:           "explicit-onion",
		MaxPeers:       10,
		NoDial:         true,
		NoDiscovery:    true,
		ListenAddr:     "127.0.0.1:0",
		PrivateKey:     newkey(),
		ListenOnion:    true,
		TorControlAddr: control,
		OnionProxyAddr: "127.0.0.1:19050",
		OnOnionService: func(addrman.NetAddr) { close(hookFired) },
		Logger:         testlog.Logger(t, logging.LvlTrace),
	}}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	select {
	case <-hookFired:
	case <-time.After(5 * time.Second):
		t.Fatal("onion service never established")
	}
	pr := srv.policy().proxyFor(addrman.NetTorV3)
	if pr == nil || pr.Addr != "127.0.0.1:19050" {
		t.Fatalf("onion proxy = %+v, want the explicit --onion value untouched", pr)
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
