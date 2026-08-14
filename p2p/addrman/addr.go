package addrman

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// NetID is a BIP155 network identifier. Values match BIP155 verbatim so the
// same byte encoding flows through addrman, parallax-disc/1 wire format, and
// persistence without translation.
type NetID uint8

const (
	NetIPv4  NetID = 0x01 // 4-byte address
	NetIPv6  NetID = 0x02 // 16-byte address
	NetTorV2 NetID = 0x03 // 10 bytes; decode-only per PIP-0006, never relayed
	NetTorV3 NetID = 0x04 // 32 bytes
	NetI2P   NetID = 0x05 // 32 bytes
	NetCJDNS NetID = 0x06 // 16 bytes
)

// addrLen returns the required byte length for n, or -1 if n is not a
// recognized BIP155 ID.
func (n NetID) addrLen() int {
	switch n {
	case NetIPv4:
		return 4
	case NetIPv6:
		return 16
	case NetTorV2:
		return 10
	case NetTorV3:
		return 32
	case NetI2P:
		return 32
	case NetCJDNS:
		return 16
	}
	return -1
}

// known reports whether n is one of the BIP155 IDs this implementation
// understands. Unknown IDs in received `Peers` messages are silently skipped
// per PIP-0006 (forward-compat for future BIP155 additions).
func (n NetID) known() bool { return n.addrLen() >= 0 }

// routable reports whether addresses in this network are considered for
// storage and dialing. Tor v2 is decode-only; addresses in this network are
// not inserted into addrman or relayed.
func (n NetID) routable() bool {
	return n == NetIPv4 || n == NetIPv6 || n == NetTorV3 || n == NetI2P || n == NetCJDNS
}

// String returns a short label suitable for logs and metrics.
func (n NetID) String() string {
	switch n {
	case NetIPv4:
		return "ipv4"
	case NetIPv6:
		return "ipv6"
	case NetTorV2:
		return "tor_v2"
	case NetTorV3:
		return "tor_v3"
	case NetI2P:
		return "i2p"
	case NetCJDNS:
		return "cjdns"
	}
	return fmt.Sprintf("net(%d)", n)
}

// NetAddr is the address+port tuple addrman stores. It's equivalent to
// Bitcoin Core's CService — a CNetAddr with a port.
//
// NetAddr is a value type and safe to copy.
type NetAddr struct {
	Network NetID
	// Addr is the raw address bytes, length determined by Network. Must
	// match Network.addrLen() or the value is invalid.
	Addr [32]byte
	// Port is the TCP port. Zero means "unknown" (only legal in a source
	// address passed to Add, never in a stored entry).
	Port uint16
}

// NewNetAddr builds a NetAddr from (network, raw bytes, port). Returns an
// error if the address length doesn't match the declared network.
func NewNetAddr(net NetID, addr []byte, port uint16) (NetAddr, error) {
	want := net.addrLen()
	if want < 0 {
		return NetAddr{}, fmt.Errorf("addrman: unknown BIP155 network id %d", net)
	}
	if len(addr) != want {
		return NetAddr{}, fmt.Errorf("addrman: %s address wants %d bytes, got %d", net, want, len(addr))
	}
	var out NetAddr
	out.Network = net
	copy(out.Addr[:], addr)
	out.Port = port
	return out, nil
}

// FromAddrPort derives a NetAddr from a net/netip AddrPort. This is the
// fast path for IPv4/IPv6 entries coming out of the Go standard library.
// Returns the zero value with ok=false for an invalid or unmapped input.
func FromAddrPort(ap netip.AddrPort) (NetAddr, bool) {
	if !ap.IsValid() {
		return NetAddr{}, false
	}
	ip := ap.Addr()
	if ip.Is4() || ip.Is4In6() {
		b := ip.As4()
		out, err := NewNetAddr(NetIPv4, b[:], ap.Port())
		if err != nil {
			return NetAddr{}, false
		}
		return out, true
	}
	if ip.Is6() {
		b := ip.As16()
		out, err := NewNetAddr(NetIPv6, b[:], ap.Port())
		if err != nil {
			return NetAddr{}, false
		}
		return out, true
	}
	return NetAddr{}, false
}

// Bytes returns the address portion (length == Network.addrLen()). The
// returned slice aliases the NetAddr's internal buffer — do not retain it
// past the NetAddr's lifetime or mutate it.
func (a NetAddr) Bytes() []byte {
	n := a.Network.addrLen()
	if n < 0 {
		return nil
	}
	return a.Addr[:n]
}

// AddrPort returns the netip.AddrPort view of a, valid only for IPv4/IPv6.
// ok=false for Tor/I2P/CJDNS.
func (a NetAddr) AddrPort() (netip.AddrPort, bool) {
	switch a.Network {
	case NetIPv4:
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte{a.Addr[0], a.Addr[1], a.Addr[2], a.Addr[3]}), a.Port), true
	case NetIPv6:
		var b [16]byte
		copy(b[:], a.Addr[:16])
		return netip.AddrPortFrom(netip.AddrFrom16(b), a.Port), true
	}
	return netip.AddrPort{}, false
}

// Equal reports whether a and b refer to the same (Network, Addr, Port).
func (a NetAddr) Equal(b NetAddr) bool {
	if a.Network != b.Network || a.Port != b.Port {
		return false
	}
	n := a.Network.addrLen()
	if n < 0 {
		return false
	}
	return bytes.Equal(a.Addr[:n], b.Addr[:n])
}

// Valid reports whether a is well-formed and worth storing. This is the
// rough equivalent of Bitcoin's CService::IsValid() && IsRoutable(). Tor v2
// is rejected per PIP-0006 (decode-only).
func (a NetAddr) Valid() bool {
	if !a.Network.routable() {
		return false
	}
	if a.Port == 0 {
		return false
	}
	switch a.Network {
	case NetIPv4:
		return a.isRoutableIPv4()
	case NetIPv6:
		return a.isRoutableIPv6()
	}
	// Tor v3 / I2P / CJDNS: length is guaranteed by NewNetAddr, and the
	// network tag itself means routable.
	return true
}

// String renders a as "network:ip:port" for logs. Not a canonical encoding.
func (a NetAddr) String() string {
	if ap, ok := a.AddrPort(); ok {
		return ap.String()
	}
	return fmt.Sprintf("%s:%x:%d", a.Network, a.Bytes(), a.Port)
}

// serviceKey returns the identity bytes used for hash-bucketing. Equivalent
// to Bitcoin's CService::GetKey: address bytes followed by port in big
// endian. Network tag is implicit in addrLen and is included as a prefix to
// avoid cross-network collisions.
func (a NetAddr) serviceKey() []byte {
	n := a.Network.addrLen()
	out := make([]byte, 0, 1+n+2)
	out = append(out, byte(a.Network))
	out = append(out, a.Addr[:n]...)
	out = binary.BigEndian.AppendUint16(out, a.Port)
	return out
}

// group returns the canonical network-group bytes for a — Bitcoin's
// GetGroup equivalent without asmap. Addresses with the same group share
// bucket selection risk; the grouping is the eclipse-resistance lever, so
// the rules below are load-bearing. Do not simplify without re-deriving
// the security argument.
func (a NetAddr) group() []byte {
	out := []byte{byte(a.Network)}
	switch a.Network {
	case NetIPv4:
		// /16 — Bitcoin netgroup.cpp:48
		return append(out, a.Addr[0], a.Addr[1])
	case NetIPv6:
		// /32 — Bitcoin netgroup.cpp:65
		// (HE.net /36 special case is not ported; the plan can revisit
		// this after v2.0 if ISP-level concentration becomes a concern.)
		return append(out, a.Addr[0], a.Addr[1], a.Addr[2], a.Addr[3])
	case NetTorV3, NetI2P:
		// Tor v3 / I2P: first 4 bits — Bitcoin netgroup.cpp:52-53.
		// Mask the low 4 bits of the first byte with 1s so the group
		// value for address 0x?X and 0x?Y differs only on the top
		// nibble. The `(1 << (8 - nBits)) - 1` mask from Bitcoin Core.
		return append(out, a.Addr[0]|0x0F)
	case NetCJDNS:
		// CJDNS: skip the constant prefix byte, then 4 bits — Bitcoin
		// netgroup.cpp:54-59. 12 bits total = 1 byte prefix + 4 bits.
		return append(out, a.Addr[1]|0x0F)
	}
	// Unknown networks: single-group catch-all. Callers should never
	// pass an unknown network to group() because Valid() gates ingest.
	return out
}

// isRoutableIPv4 approximates CNetAddr::IsRoutable for IPv4 — rejects
// private, loopback, link-local, and test-only ranges. The constants are
// from RFC 1918/2544/3927/3849/5737/6598.
func (a NetAddr) isRoutableIPv4() bool {
	ip := a.Addr[:4]
	switch {
	case ip[0] == 10: // 10.0.0.0/8 (RFC 1918)
		return false
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31: // 172.16.0.0/12
		return false
	case ip[0] == 192 && ip[1] == 168: // 192.168.0.0/16
		return false
	case ip[0] == 127: // loopback
		return false
	case ip[0] == 0: // "this network"
		return false
	case ip[0] == 169 && ip[1] == 254: // link-local
		return false
	case ip[0] == 192 && ip[1] == 0 && ip[2] == 2: // TEST-NET-1
		return false
	case ip[0] == 198 && (ip[1] == 18 || ip[1] == 19): // RFC 2544 (198.18.0.0/15)
		return false
	case ip[0] == 198 && ip[1] == 51 && ip[2] == 100: // TEST-NET-2
		return false
	case ip[0] == 203 && ip[1] == 0 && ip[2] == 113: // TEST-NET-3
		return false
	case ip[0] >= 100 && ip[0] <= 100 && ip[1] >= 64 && ip[1] <= 127: // CGNAT 100.64.0.0/10
		return false
	case ip[0] >= 224: // 224.0.0.0/4 multicast, 240.0.0.0/4 reserved
		return false
	}
	return true
}

// isRoutableIPv6 approximates CNetAddr::IsRoutable for IPv6.
func (a NetAddr) isRoutableIPv6() bool {
	ip := a.Addr[:16]
	// ::/128 unspecified, ::1/128 loopback
	if bytes.Equal(ip, make([]byte, 16)) {
		return false
	}
	loopback := make([]byte, 16)
	loopback[15] = 1
	if bytes.Equal(ip, loopback) {
		return false
	}
	// fe80::/10 link-local
	if ip[0] == 0xfe && ip[1]&0xc0 == 0x80 {
		return false
	}
	// fc00::/7 unique local
	if ip[0]&0xfe == 0xfc {
		return false
	}
	// 2001:db8::/32 documentation
	if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
		return false
	}
	// ff00::/8 multicast
	if ip[0] == 0xff {
		return false
	}
	return true
}

// Errors surfaced by the addr helpers — exported so callers can match.
var (
	ErrMalformedAddr = errors.New("addrman: malformed address")
)
