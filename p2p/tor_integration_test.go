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
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/internal/testlog"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
)

// TestTorIntegration — the PIP-0007 end-to-end path against a real
// Tor daemon: node A creates an onion service via the control port,
// node B dials A's onion address through Tor's SOCKS listener, the v2
// handshake completes over the circuit, and both sides classify the
// session correctly.
//
// Deliberately opt-in: it needs a tor binary, live network access,
// and a full Tor bootstrap (typically 30-90s). Run with:
//
//	PARALLAX_TOR_TEST=1 go test ./p2p/ -run TestTorIntegration -v -timeout 10m
func TestTorIntegration(t *testing.T) {
	if os.Getenv("PARALLAX_TOR_TEST") == "" {
		t.Skip("set PARALLAX_TOR_TEST=1 to run (needs a tor binary and network access)")
	}
	torBin, err := exec.LookPath("tor")
	if err != nil {
		t.Skip("no tor binary on PATH")
	}

	controlPort := freeTCPPort(t)
	socksPort := freeTCPPort(t)
	dataDir := t.TempDir()

	// Ignore the system torrc — it may carry directives (User,
	// pre-bound ports) that fail for an unprivileged test run. Tor
	// insists on a readable regular file, so hand it an empty one.
	torrc := filepath.Join(dataDir, "torrc")
	if err := os.WriteFile(torrc, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tor := exec.Command(torBin,
		"-f", torrc,
		"--DataDirectory", dataDir,
		"--ControlPort", fmt.Sprint(controlPort),
		"--CookieAuthentication", "1",
		"--SocksPort", fmt.Sprint(socksPort),
		"--Log", "notice stderr",
	)
	if err := tor.Start(); err != nil {
		t.Fatalf("starting tor: %v", err)
	}
	t.Cleanup(func() {
		tor.Process.Kill()
		tor.Wait()
	})

	controlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	socksAddr := fmt.Sprintf("127.0.0.1:%d", socksPort)
	waitForListener(t, controlAddr, 60*time.Second)

	// Node A: listener + onion service.
	onionReady := make(chan addrman.NetAddr, 1)
	a := &Server{Config: Config{
		Name:           "tor-integration-a",
		MaxPeers:       10,
		NoDial:         true,
		NoDiscovery:    true,
		ListenAddr:     "127.0.0.1:0",
		PrivateKey:     newkey(),
		ListenOnion:    true,
		TorControlAddr: controlAddr,
		OnionKeyPath:   filepath.Join(dataDir, "onion_v3_private_key"),
		OnOnionService: func(na addrman.NetAddr) {
			select {
			case onionReady <- na:
			default:
			}
		},
		Logger: testlog.Logger(t, logging.LvlDebug),
	}}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	var onion addrman.NetAddr
	select {
	case onion = <-onionReady:
		t.Logf("onion service: %s", onion)
	case <-time.After(2 * time.Minute):
		t.Fatal("onion service never established (tor control port unresponsive?)")
	}

	// Node B: dials through Tor's SOCKS listener.
	b := &Server{Config: Config{
		Name:           "tor-integration-b",
		MaxPeers:       10,
		NoDiscovery:    true,
		NoDial:         true,
		PrivateKey:     newkey(),
		OnionProxyAddr: socksAddr,
		Logger:         testlog.Logger(t, logging.LvlDebug),
	}}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()

	// The rendezvous needs Tor bootstrapped and the descriptor
	// published; retry the dial until the deadline.
	deadline := time.Now().Add(5 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := b.DialV2Manual(onion); err != nil {
			lastErr = err
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}
	var outbound *Peer
	for time.Now().Before(deadline) && outbound == nil {
		for _, p := range b.Peers() {
			outbound = p
		}
		time.Sleep(200 * time.Millisecond)
	}
	if outbound == nil {
		t.Fatalf("no peer established over Tor (last dial error: %v)", lastErr)
	}
	if !outbound.OnionPeer() || !outbound.ProxiedConn() {
		t.Errorf("outbound peer flags: onion=%v proxied=%v, want both", outbound.OnionPeer(), outbound.ProxiedConn())
	}

	// A's side: an inbound peer, classified onion (loopback stream
	// from the Tor daemon while the service is active).
	var inbound *Peer
	for time.Now().Before(deadline) && inbound == nil {
		for _, p := range a.Peers() {
			inbound = p
		}
		time.Sleep(200 * time.Millisecond)
	}
	if inbound == nil {
		t.Fatal("service side never saw the inbound peer")
	}
	if !inbound.Inbound() || !inbound.OnionPeer() {
		t.Errorf("inbound peer flags: inbound=%v onion=%v, want both", inbound.Inbound(), inbound.OnionPeer())
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForListener(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return
		}
	}
	t.Fatalf("nothing listening at %s after %v", addr, timeout)
}
