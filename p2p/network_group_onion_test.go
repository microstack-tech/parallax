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

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
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

// TestGroupKeyForNetAddr — onion targets group by the top 4 bits of
// the service pubkey (addrman group() parity); IP targets keep the
// ipNetworkGroupKey rules including the loopback exemption.
func TestGroupKeyForNetAddr(t *testing.T) {
	// Same top nibble → same group, despite differing low bits.
	a := onionAddrWithFirstByte(t, 0x42)
	b := onionAddrWithFirstByte(t, 0x4F)
	if groupKeyForNetAddr(a) != groupKeyForNetAddr(b) {
		t.Error("onion addresses sharing the top nibble must share a group")
	}
	// Different top nibble → different group.
	c := onionAddrWithFirstByte(t, 0x52)
	if groupKeyForNetAddr(a) == groupKeyForNetAddr(c) {
		t.Error("onion addresses with different top nibbles must not share a group")
	}
	// Onion groups never collide with IP groups.
	ip := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(66, 1, 2, 3), Port: 32110})
	if groupKeyForNetAddr(a) == groupKeyForNetAddr(ip) {
		t.Error("onion group collided with an IPv4 group")
	}
	// Loopback IP targets stay exempt (empty key).
	loop := testNetAddr(t, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 32110})
	if got := groupKeyForNetAddr(loop); got != "" {
		t.Errorf("loopback target group = %q, want exempt", got)
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
	if got := outboundGroupKey(co); got != groupKeyForNetAddr(onion) {
		t.Errorf("onion conn group = %x, want the onion target group", got)
	}
}
