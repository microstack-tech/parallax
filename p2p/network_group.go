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

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
)

// Network-group prefix lengths. Mirrors Bitcoin Core's
// CNetAddr::GetGroup defaults (src/netaddress.cpp): /16 for IPv4,
// /32 for IPv6. Tor / I2P / CJDNS get distinct prefix tags so
// clearnet addresses never collide with privacy-network addresses
// in the same group.
const (
	netGroupPrefixIPv4 = 2 // first 2 bytes of v4 = /16
	netGroupPrefixIPv6 = 4 // first 4 bytes of v6 = /32
)

// Network-group tag bytes prepended to the address prefix to
// disambiguate networks. Operators reading group bytes in admin
// output can tell IPv4 from IPv6 by inspection. Values are
// arbitrary but stable; do not renumber.
const (
	netGroupTagIPv4    byte = 0x01
	netGroupTagIPv6    byte = 0x02
	netGroupTagOnion   byte = 0x04 // matches the BIP155 network id
	netGroupTagUnknown byte = 0x00
)

// NetworkGroupForIP returns the canonical network-group bytes for
// an IP address. Used by the eviction algorithm's
// "preserve-network-diversity" protection rounds and by the
// outbound dial scheduler's anti-eclipse filter.
//
// IPv4-mapped IPv6 addresses (::ffff:a.b.c.d) collapse to their v4
// representation so the same address never produces two different
// groups. Unknown / non-IP networks (test pipes) yield a stable
// "unknown" group so two such peers compare equal.
func NetworkGroupForIP(ip net.IP) []byte {
	if ip == nil {
		return []byte{netGroupTagUnknown}
	}
	if v4 := ip.To4(); v4 != nil {
		out := make([]byte, 1+netGroupPrefixIPv4)
		out[0] = netGroupTagIPv4
		copy(out[1:], v4[:netGroupPrefixIPv4])
		return out
	}
	if v6 := ip.To16(); v6 != nil {
		// 6to4 / Teredo tunnels route through an IPv4 endpoint, so
		// the peer's real network position is the embedded IPv4 /16 —
		// Core's GetGroup unwraps these via GetLinkedIPv4 for the
		// same reason. Without the unwrap, one IPv4 host could
		// spread across many apparent IPv6 /32 groups.
		if v4 := tunneledIPv4(v6); v4 != nil {
			out := make([]byte, 1+netGroupPrefixIPv4)
			out[0] = netGroupTagIPv4
			copy(out[1:], v4[:netGroupPrefixIPv4])
			return out
		}
		out := make([]byte, 1+netGroupPrefixIPv6)
		out[0] = netGroupTagIPv6
		copy(out[1:], v6[:netGroupPrefixIPv6])
		return out
	}
	return []byte{netGroupTagUnknown}
}

// tunneledIPv4 extracts the IPv4 address embedded in a 6to4
// (2002::/16) or Teredo (2001::/32) IPv6 address, or nil when the
// address is neither. 6to4 carries the v4 address in bytes 2-5;
// Teredo stores the client's v4 address bit-inverted in the last
// four bytes (RFC 4380 §4). Mirrors CNetAddr::GetLinkedIPv4
// (src/netaddress.cpp).
func tunneledIPv4(v6 net.IP) net.IP {
	if v6[0] == 0x20 && v6[1] == 0x02 {
		return net.IP{v6[2], v6[3], v6[4], v6[5]}
	}
	if v6[0] == 0x20 && v6[1] == 0x01 && v6[2] == 0x00 && v6[3] == 0x00 {
		return net.IP{v6[12] ^ 0xFF, v6[13] ^ 0xFF, v6[14] ^ 0xFF, v6[15] ^ 0xFF}
	}
	return nil
}

// networkGroupForOnion returns the group bytes for a Tor v3 address:
// the top 4 bits of the service pubkey, mirroring addrman's group()
// (Bitcoin netgroup.cpp:52-53). Onion addresses are cheap to mint, so
// the grouping is weaker than IP space — the same trade-off Core
// accepts.
func networkGroupForOnion(a addrman.NetAddr) []byte {
	return []byte{netGroupTagOnion, a.Addr[0] | 0x0F}
}

// groupKeyForNetAddr renders the dial-target network-group key used
// by the outbound diversity accounting. IP targets share
// ipNetworkGroupKey's loopback/link-local exemption; onion targets
// use the top-4-bits rule. Empty for exempt or ungroupable targets.
func groupKeyForNetAddr(a addrman.NetAddr) string {
	if tcp := tcpFromNetAddr(a); tcp != nil {
		return ipNetworkGroupKey(tcp.IP)
	}
	if a.Network == addrman.NetTorV3 {
		return string(networkGroupForOnion(a))
	}
	return ""
}

// NetworkGroup returns this peer's cached network-group bytes,
// computed once at attach time from the peer's RemoteAddr.IP.
// Returns nil for peers without a TCP RemoteAddr (test pipes,
// tunneled transports) — eviction code that consumes this
// treats nil as a unique singleton group.
func (p *Peer) NetworkGroup() []byte {
	v := p.networkGroup.Load()
	if v == nil {
		return nil
	}
	return *v
}

// computeAndCacheNetworkGroup populates p.networkGroup once.
// Called from server.launchPeer after the conn's RemoteAddr is
// known. Idempotent: a second call with the same address is a
// no-op (the field stores the same bytes).
//
// Outbound conns prefer the dialed target over the socket's
// RemoteAddr: a proxied conn's socket peer is the SOCKS5 proxy, and
// without this every proxied peer would collapse into the proxy's
// group — Core computes eviction/diversity groups from CNode::addr
// (the target) for the same reason. Onion targets group by the
// top-4-bits rule; inbound onion streams keep the loopback group the
// Tor daemon delivers them from, as in Core.
func (p *Peer) computeAndCacheNetworkGroup() {
	if t := p.rw.dialTarget(); t.Network != 0 {
		var g []byte
		if t.Network == addrman.NetTorV3 {
			g = networkGroupForOnion(t)
		} else if tcp := tcpFromNetAddr(t); tcp != nil {
			g = NetworkGroupForIP(tcp.IP)
		}
		if g != nil {
			p.networkGroup.Store(&g)
			return
		}
	}
	pra, ok := p.rw.fd.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return
	}
	g := NetworkGroupForIP(pra.IP)
	p.networkGroup.Store(&g)
}

// SameNetworkGroup reports whether two byte-slices represent the
// same network group. Treats nil as a singleton — two nil groups
// are NOT equal (they're each their own unique group). This keeps
// peers with unresolvable RemoteAddr from clustering together in
// eviction passes.
func SameNetworkGroup(a, b []byte) bool {
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a, b)
}
