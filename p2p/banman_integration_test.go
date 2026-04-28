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
	"net"
	"testing"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/banman"
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
