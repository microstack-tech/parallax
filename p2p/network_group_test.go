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
	"bytes"
	"net"
	"testing"
)

// TestNetworkGroupForIPv4 — /16 prefix with the IPv4 tag byte.
func TestNetworkGroupForIPv4(t *testing.T) {
	g := NetworkGroupForIP(net.IPv4(1, 2, 3, 4))
	want := []byte{netGroupTagIPv4, 1, 2}
	if !bytes.Equal(g, want) {
		t.Fatalf("group = %x, want %x", g, want)
	}
}

// TestNetworkGroupForIPv6 — /32 prefix with the IPv6 tag byte.
func TestNetworkGroupForIPv6(t *testing.T) {
	g := NetworkGroupForIP(net.ParseIP("2001:db8::1"))
	want := []byte{netGroupTagIPv6, 0x20, 0x01, 0x0d, 0xb8}
	if !bytes.Equal(g, want) {
		t.Fatalf("group = %x, want %x", g, want)
	}
}

// TestNetworkGroupV4MappedV6Collapses — ::ffff:1.2.3.4 must
// produce the same group as 1.2.3.4. Otherwise dedup logic could
// see one peer as two distinct groups.
func TestNetworkGroupV4MappedV6Collapses(t *testing.T) {
	v4 := NetworkGroupForIP(net.IPv4(1, 2, 3, 4))
	mapped := NetworkGroupForIP(net.ParseIP("::ffff:1.2.3.4"))
	if !bytes.Equal(v4, mapped) {
		t.Fatalf("v4 group %x != mapped %x", v4, mapped)
	}
}

// TestNetworkGroupNilIP — nil IP yields the unknown tag.
func TestNetworkGroupNilIP(t *testing.T) {
	g := NetworkGroupForIP(nil)
	if !bytes.Equal(g, []byte{netGroupTagUnknown}) {
		t.Fatalf("nil IP group = %x, want %x", g, []byte{netGroupTagUnknown})
	}
}

// TestNetworkGroupSamePrefixSameGroup — addresses sharing a /16
// (IPv4) or /32 (IPv6) belong to the same group.
func TestNetworkGroupSamePrefixSameGroup(t *testing.T) {
	a := NetworkGroupForIP(net.IPv4(1, 2, 3, 4))
	b := NetworkGroupForIP(net.IPv4(1, 2, 99, 88))
	if !bytes.Equal(a, b) {
		t.Fatalf("same /16 should yield same group: %x vs %x", a, b)
	}
	c := NetworkGroupForIP(net.IPv4(1, 3, 0, 0))
	if bytes.Equal(a, c) {
		t.Fatalf("different /16 should differ: %x vs %x", a, c)
	}
}

// TestNetworkGroupV4V6CollideTags — clearnet v4 group and
// equivalent-prefix v6 group should NEVER compare equal because
// the tag byte differs. (Cross-network impersonation guard.)
func TestNetworkGroupV4V6CollideTags(t *testing.T) {
	v4 := NetworkGroupForIP(net.IPv4(1, 2, 3, 4))
	// Pick a v6 whose first 4 bytes happen to match the v4 prefix
	// when stripped of the tag. Even so, the tag byte differs.
	v6 := NetworkGroupForIP(net.ParseIP("0102::"))
	if bytes.Equal(v4, v6) {
		t.Fatalf("v4 and v6 groups collided: %x == %x", v4, v6)
	}
}

// TestSameNetworkGroupNilIsSingleton — nil compared to anything
// (including another nil) returns false. Each nil-group peer is
// its own singleton for eviction purposes.
func TestSameNetworkGroupNilIsSingleton(t *testing.T) {
	if SameNetworkGroup(nil, nil) {
		t.Error("nil-vs-nil should NOT be same group")
	}
	g := NetworkGroupForIP(net.IPv4(1, 2, 3, 4))
	if SameNetworkGroup(nil, g) {
		t.Error("nil-vs-real should NOT be same group")
	}
	if SameNetworkGroup(g, nil) {
		t.Error("real-vs-nil should NOT be same group")
	}
}

// TestSameNetworkGroupEqual — two non-nil byte-equal groups
// compare equal.
func TestSameNetworkGroupEqual(t *testing.T) {
	a := NetworkGroupForIP(net.IPv4(8, 8, 8, 8))
	b := NetworkGroupForIP(net.IPv4(8, 8, 0, 0))
	if !SameNetworkGroup(a, b) {
		t.Fatalf("byte-equal groups should compare equal: %x vs %x", a, b)
	}
}

// TestPeerNetworkGroupCachedAtAttach — driving a fake conn
// through computeAndCacheNetworkGroup populates the cache;
// NetworkGroup() returns the expected value.
func TestPeerNetworkGroupCachedAtAttach(t *testing.T) {
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: net.IPv4(45, 236, 49, 58), Port: 32110}}
	p := NewPeerForTest(randomID(), "test", nil, fake)
	if p.NetworkGroup() != nil {
		t.Fatal("NetworkGroup should be nil before computeAndCache")
	}
	p.computeAndCacheNetworkGroup()
	g := p.NetworkGroup()
	want := []byte{netGroupTagIPv4, 45, 236}
	if !bytes.Equal(g, want) {
		t.Fatalf("cached group = %x, want %x", g, want)
	}
}

// TestPeerNetworkGroupNilForNonTCPConn — net.Pipe (default
// NewPeer) yields nil because RemoteAddr isn't a *net.TCPAddr.
func TestPeerNetworkGroupNilForNonTCPConn(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	p.computeAndCacheNetworkGroup()
	if p.NetworkGroup() != nil {
		t.Fatalf("net.Pipe peer NetworkGroup = %x, want nil", p.NetworkGroup())
	}
}

// 6to4 and Teredo addresses tunnel through an IPv4 endpoint; the
// group must be the embedded IPv4 /16, matching Core's GetLinkedIPv4
// unwrapping, so one v4 host can't fan out across IPv6 /32 groups.
func TestNetworkGroupTunneledIPv4(t *testing.T) {
	// 6to4: 2002:0102:0304:: embeds 1.2.3.4.
	sixToFour := net.ParseIP("2002:102:304::1")
	want := NetworkGroupForIP(net.ParseIP("1.2.3.4"))
	if got := NetworkGroupForIP(sixToFour); !SameNetworkGroup(got, want) {
		t.Fatalf("6to4 group = %x, want the embedded v4 group %x", got, want)
	}
	// Teredo: 2001:0:x:x:x:x:fefd:fcfb embeds 1.2.3.4 (bit-inverted
	// in the last four bytes).
	teredo := net.ParseIP("2001:0:4136:e378:8000:63bf:fefd:fcfb")
	if got := NetworkGroupForIP(teredo); !SameNetworkGroup(got, want) {
		t.Fatalf("teredo group = %x, want the embedded v4 group %x", got, want)
	}
	// A same-prefix v4 host must land in the same group as the
	// tunneled forms.
	if got := NetworkGroupForIP(net.ParseIP("1.2.200.200")); !SameNetworkGroup(got, want) {
		t.Fatalf("v4 /16 sibling group = %x, want %x", got, want)
	}
	// Plain IPv6 must not be unwrapped.
	plain := NetworkGroupForIP(net.ParseIP("2a00:1450::1"))
	if SameNetworkGroup(plain, want) {
		t.Fatalf("plain IPv6 wrongly grouped with v4")
	}
}
