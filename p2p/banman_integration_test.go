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
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/banman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/util/mclock"
)

// TestCheckInboundConnRejectsBannedIP — checkInboundConn returns
// non-nil when the source IP is in the BanList. Mirrors Bitcoin
// Core's accept-loop ban check at src/net.cpp:1800.
func TestCheckInboundConnRejectsBannedIP(t *testing.T) {
	t.Parallel()
	bm, err := banman.New("", logging.Root())
	if err != nil {
		t.Fatalf("banman.New: %v", err)
	}
	bannedIP := net.IPv4(192, 0, 2, 99)
	if err := bm.Ban(bannedIP, 0, banman.ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	srv := &Server{
		Config: Config{
			MaxPeers: 4,
			BanList:  bm,
			Logger:   logging.Root(),
			clock:    mclock.System{},
		},
	}
	if err := srv.checkInboundConn(bannedIP); err == nil {
		t.Fatalf("checkInboundConn allowed a banned IP")
	}
	// Non-banned IP passes (LAN exception in checkInboundConn means
	// IPs Bitcoin Core would call private get a free pass; pick a
	// public one).
	if err := srv.checkInboundConn(net.IPv4(8, 8, 8, 8)); err != nil {
		t.Fatalf("checkInboundConn rejected unbanned IP: %v", err)
	}
}

// TestDialV2RejectsBannedIP — the shared v2 outbound dial path
// refuses to connect to a banned or discouraged address before
// opening any socket, so every caller (DialV2, feeler, addrfetch,
// runV2Dialer, anchor replay) is covered. Mirrors Bitcoin Core's
// CConnman::OpenNetworkConnection ban gate.
func TestDialV2RejectsBannedIP(t *testing.T) {
	t.Parallel()
	bm, err := banman.New("", logging.Root())
	if err != nil {
		t.Fatalf("banman.New: %v", err)
	}
	bannedIP := net.IPv4(192, 0, 2, 77)
	if err := bm.Ban(bannedIP, 0, banman.ReasonManual); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	discouragedIP := net.IPv4(192, 0, 2, 88)
	bm.Discourage(discouragedIP)

	srv := &Server{
		Config: Config{BanList: bm, Logger: logging.Root()},
	}
	srv.log = logging.Root()

	if err := srv.DialV2(testNetAddr(t, &net.TCPAddr{IP: bannedIP, Port: 32110})); !errors.Is(err, errV2DialBanned) {
		t.Fatalf("DialV2 to banned IP = %v, want errV2DialBanned", err)
	}
	if err := srv.DialV2(testNetAddr(t, &net.TCPAddr{IP: discouragedIP, Port: 32110})); !errors.Is(err, errV2DialBanned) {
		t.Fatalf("DialV2 to discouraged IP = %v, want errV2DialBanned", err)
	}
}

// TestPeerMisbehavingForFlagsAndDisconnects — MisbehavingFor sets
// the discourage flag and triggers disconnect. ShouldDiscourage
// reports true; DiscourageReason captures the first reason.
func TestPeerMisbehavingForFlagsAndDisconnects(t *testing.T) {
	t.Parallel()
	pipe, _ := net.Pipe()
	defer pipe.Close()
	peer := NewPeerForTest(uintID(0x42), "test", nil, pipe)

	if peer.ShouldDiscourage() {
		t.Fatalf("fresh peer should not be flagged")
	}
	peer.MisbehavingFor("test-violation")
	if !peer.ShouldDiscourage() {
		t.Fatalf("ShouldDiscourage = false after MisbehavingFor")
	}
	if got := peer.DiscourageReason(); got != "test-violation" {
		t.Errorf("reason = %q, want %q", got, "test-violation")
	}
	// Idempotent: second call doesn't overwrite the reason.
	peer.MisbehavingFor("second-reason")
	if got := peer.DiscourageReason(); got != "test-violation" {
		t.Errorf("idempotency lost: reason = %q after second MisbehavingFor", got)
	}
}

// TestPostHandshakeRejectsFreshlyBanned — a setban issued while a
// handshake is in flight must reject the connection at the
// post-handshake checkpoint. The accept-loop ban check runs before
// the handshake starts and setban only disconnects registered peers,
// so without the re-check a connection straddling the ban would be
// admitted and survive for the ban's whole lifetime. Trusted conns
// bypass (NoBan permission parity).
func TestPostHandshakeRejectsFreshlyBanned(t *testing.T) {
	bm, err := banman.New("", logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	bannedIP := net.IPv4(192, 0, 2, 77)
	if err := bm.Ban(bannedIP, time.Hour, banman.ReasonManual); err != nil {
		t.Fatal(err)
	}

	srv := newSelfEndpointServer(t, nil, 0)
	srv.BanList = bm
	srv.Config.MaxPeers = 10

	mkConn := func(flags connFlag) *conn {
		pipe, _ := net.Pipe()
		t.Cleanup(func() { pipe.Close() })
		fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: bannedIP, Port: 32110}}
		return &conn{
			fd:    fake,
			flags: flags,
			node:  enode.SignNull(new(enr.Record), randomID()),
		}
	}

	if err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 0, mkConn(inboundConn)); !errors.Is(err, DiscUselessPeer) {
		t.Fatalf("inbound banned conn: err = %v, want DiscUselessPeer", err)
	}
	if err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 0, mkConn(dynDialedConn)); !errors.Is(err, DiscUselessPeer) {
		t.Fatalf("outbound banned conn: err = %v, want DiscUselessPeer", err)
	}
	if err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 0, mkConn(inboundConn|trustedConn)); err != nil {
		t.Fatalf("trusted banned conn: err = %v, want nil (trusted bypass)", err)
	}
}

// TestDiscourageTargetExemptions — the disconnect-time discourage
// stamp skips trusted, static, and loopback peers, mirroring Bitcoin
// Core's MaybeDiscourageAndDisconnect exemptions (NoBan permission,
// manual connections, local addresses). A plain misbehaving inbound
// peer is stamped with its remote IP.
func TestDiscourageTargetExemptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		opts      evictionOpts
		misbehave bool
		want      bool
	}{
		{"misbehaving inbound", evictionOpts{inbound: true, ip: net.IPv4(192, 0, 2, 10)}, true, true},
		{"well-behaved inbound", evictionOpts{inbound: true, ip: net.IPv4(192, 0, 2, 11)}, false, false},
		{"trusted", evictionOpts{inbound: true, trusted: true, ip: net.IPv4(192, 0, 2, 12)}, true, false},
		{"static", evictionOpts{static: true, ip: net.IPv4(192, 0, 2, 13)}, true, false},
		{"loopback", evictionOpts{inbound: true, ip: net.IPv4(127, 0, 0, 1)}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := makeEvictionPeer(t, tc.opts)
			if tc.misbehave {
				p.MisbehavingFor("test-violation")
			}
			ip, ok := discourageTarget(p)
			if ok != tc.want {
				t.Fatalf("discourageTarget ok = %v, want %v", ok, tc.want)
			}
			if ok && !ip.Equal(tc.opts.ip) {
				t.Fatalf("discourageTarget ip = %v, want %v", ip, tc.opts.ip)
			}
		})
	}
}
