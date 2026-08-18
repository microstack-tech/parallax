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
)

func onionAddrWithFirstByte(t *testing.T, b byte) addrman.NetAddr {
	t.Helper()
	var pub [32]byte
	pub[0] = b
	pub[1] = 0x99
	na, err := addrman.NewNetAddr(addrman.NetTorV3, pub[:], 32110)
	if err != nil {
		t.Fatal(err)
	}
	return na
}

// TestOnionExemptFromOutboundGroupLimit — the one-peer-per-group
// outbound limit covers IPv4/IPv6 only, as in Core, whose
// ThreadOpenConnections inserts just those into
// outbound_ipv46_peer_netgroups. The onion group rule is the top 4
// bits of the service key — 16 values for the whole address space —
// so applying the limit there would cap a node at 16 onion peers and
// reject most candidates well before that.
func TestOnionExemptFromOutboundGroupLimit(t *testing.T) {
	for _, b := range []byte{0x00, 0x42, 0xF0} {
		if got := groupKeyForNetAddr(onionAddrWithFirstByte(t, b)); got != "" {
			t.Errorf("onion target 0x%02x has group key %x, want exempt", b, got)
		}
	}
	// IP targets keep their groups, including the loopback exemption.
	ip := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(66, 1, 2, 3), Port: 32110})
	if groupKeyForNetAddr(ip) == "" {
		t.Error("ipv4 target must still be grouped")
	}
	loop := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 32110})
	if got := groupKeyForNetAddr(loop); got != "" {
		t.Errorf("loopback target group = %q, want exempt", got)
	}
	// Eviction diversity still groups onion peers (Core's GetGroup
	// covers onion; only the dial-time limit is IP-only).
	a := networkGroupForOnion(onionAddrWithFirstByte(t, 0x42))
	c := networkGroupForOnion(onionAddrWithFirstByte(t, 0x52))
	if len(a) == 0 || SameNetworkGroup(a, c) {
		t.Error("onion eviction groups must still distinguish top nibbles")
	}
}

// TestOutboundGroupUsesDialedTarget — a conn carrying a dialed target
// derives its diversity group from the target, not the socket's
// RemoteAddr (which for proxied conns is the SOCKS5 proxy).
func TestOutboundGroupUsesDialedTarget(t *testing.T) {
	target := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 32110})
	c := &conn{
		fd:    &fakeAddrConn{remoteAddr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 9050}},
		flags: dynDialedConn,
	}
	c.setDialTarget(target)
	want := ipNetworkGroupKey(net.IPv4(198, 51, 100, 7))
	if got := outboundGroupKey(c); got != want {
		t.Errorf("outboundGroupKey = %x, want the target's group %x", got, want)
	}

	onion := onionAddrWithFirstByte(t, 0x42)
	co := &conn{
		fd:    &fakeAddrConn{remoteAddr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 9050}},
		flags: dynDialedConn,
	}
	co.setDialTarget(onion)
	// Exempt from the outbound limit — and crucially not keyed on the
	// proxy's address, which would collide with every other proxied
	// peer.
	if got := outboundGroupKey(co); got != "" {
		t.Errorf("onion conn group = %x, want exempt", got)
	}
}
