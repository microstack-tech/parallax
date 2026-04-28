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

package p2p

import (
	"net"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/internal/testlog"
)

// floorTestSetup builds a Server wired with a deterministic addrbook
// holding (a) one tcp_gossip entry whose remote IP/port matches the
// candidate conn so connSourceTag classifies it as tcp_gossip, and
// (b) zero or more non-tcp_gossip entries to populate the alternatives
// check. Returns the server and a candidate inbound *conn.
func floorTestSetup(t *testing.T, candidateIP net.IP, candidatePort uint16, withAlternative bool) (*Server, *conn) {
	t.Helper()
	book, err := addrman.New(addrman.Deterministic(0xf100))
	if err != nil {
		t.Fatalf("addrman.New: %v", err)
	}
	candidate, err := addrman.NewNetAddr(addrman.NetIPv4, candidateIP.To4(), candidatePort)
	if err != nil {
		t.Fatalf("NewNetAddr candidate: %v", err)
	}
	src, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{5, 5, 5, 5}, 0)
	if !book.AddOne(candidate, 0, nil, time.Now(), src, addrman.SourceTCPGossip, 0) {
		t.Fatalf("AddOne candidate failed")
	}
	if withAlternative {
		alt, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{1, 2, 99, 9}, 32110)
		if !book.AddOne(alt, 0, nil, time.Now(), src, addrman.SourceDNSSeed, 0) {
			t.Fatalf("AddOne alt failed")
		}
	}
	db, err := enode.OpenDB("")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(db.Close)
	srv := &Server{
		log:       testlog.Logger(t, logging.LvlError),
		Config:    Config{MaxPeers: 8, NoDiscovery: true, NoDial: true},
		addrbook:  book,
		localnode: enode.NewLocalNode(db, newkey()),
	}
	if err := srv.initHelloNonce(); err != nil {
		t.Fatalf("initHelloNonce: %v", err)
	}
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: candidateIP, Port: int(candidatePort)}}
	t.Cleanup(func() { fake.Close() })
	return srv, &conn{
		fd:        fake,
		transport: &v2Transport{},
		node:      enode.SignNull(new(enr.Record), randomID()),
		flags:     inboundConn,
	}
}

// TestLegacyFloorRejectsTCPGossipAtCap — with 6 tcp_gossip peers
// (MaxPeers=8, MinLegacyPeers=2 → cap=6) and an addrbook entry from
// dns_seed available as an alternative, a 7th tcp_gossip candidate
// is rejected with DiscTooManyPeers.
func TestLegacyFloorRejectsTCPGossipAtCap(t *testing.T) {
	t.Parallel()
	srv, c := floorTestSetup(t, net.IPv4(1, 2, 3, 7), 32110, true)
	srv.MinLegacyPeers = 2

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 6, c)
	if err != DiscTooManyPeers {
		t.Fatalf("postHandshakeChecks = %v; want DiscTooManyPeers", err)
	}
}

// TestLegacyFloorAdmitsBelowCap — with 5 tcp_gossip peers (cap=6),
// a 6th tcp_gossip candidate is admitted.
func TestLegacyFloorAdmitsBelowCap(t *testing.T) {
	t.Parallel()
	srv, c := floorTestSetup(t, net.IPv4(1, 2, 3, 8), 32110, true)
	srv.MinLegacyPeers = 2

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 5, c)
	if err != nil {
		t.Fatalf("postHandshakeChecks = %v; want nil (under cap)", err)
	}
}

// TestLegacyFloorSkippedWithoutAlternatives — the floor short-
// circuits when the addrbook has no non-tcp_gossip entries. With
// only the tcp_gossip candidate present, the cap is unreachable so
// admission proceeds.
func TestLegacyFloorSkippedWithoutAlternatives(t *testing.T) {
	t.Parallel()
	srv, c := floorTestSetup(t, net.IPv4(1, 2, 3, 9), 32110, false)
	srv.MinLegacyPeers = 2

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 6, c)
	if err != nil {
		t.Fatalf("postHandshakeChecks = %v; want nil (no alternatives)", err)
	}
}

// TestLegacyFloorDisabled — MinLegacyPeers<0 disables the floor;
// even with alternatives and tcp_gossip count above the would-be
// cap, the candidate is admitted.
func TestLegacyFloorDisabled(t *testing.T) {
	t.Parallel()
	srv, c := floorTestSetup(t, net.IPv4(1, 2, 3, 10), 32110, true)
	srv.MinLegacyPeers = -1

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 7, c)
	if err != nil {
		t.Fatalf("postHandshakeChecks = %v; want nil (floor disabled)", err)
	}
}

// TestLegacyFloorTrustedBypass — trusted peers bypass the floor
// even at saturation with alternatives present.
func TestLegacyFloorTrustedBypass(t *testing.T) {
	t.Parallel()
	srv, c := floorTestSetup(t, net.IPv4(1, 2, 3, 11), 32110, true)
	srv.MinLegacyPeers = 2
	c.flags |= trustedConn

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 6, c)
	if err != nil {
		t.Fatalf("postHandshakeChecks = %v; want nil (trusted bypass)", err)
	}
}

// TestLegacyFloorIgnoresUnclassifiedSource — a candidate whose
// remote endpoint isn't in the addrbook (source unknown) is not
// counted against the cap; it's neither tcp_gossip nor an
// alternative, so the floor doesn't fire.
func TestLegacyFloorIgnoresUnclassifiedSource(t *testing.T) {
	t.Parallel()
	srv, c := floorTestSetup(t, net.IPv4(1, 2, 3, 12), 32110, true)
	srv.MinLegacyPeers = 2

	// Replace the conn with one whose RemoteAddr doesn't match
	// any addrbook entry.
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: net.IPv4(8, 7, 6, 5), Port: 65000}}
	t.Cleanup(func() { fake.Close() })
	c.fd = fake

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 6, c)
	if err != nil {
		t.Fatalf("postHandshakeChecks = %v; want nil (unclassified candidate)", err)
	}
}
