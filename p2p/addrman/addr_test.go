package addrman

import (
	"bytes"
	"net/netip"
	"testing"
)

func TestNetIDAddrLen(t *testing.T) {
	cases := map[NetID]int{
		NetIPv4:  4,
		NetIPv6:  16,
		NetTorV2: 10,
		NetTorV3: 32,
		NetI2P:   32,
		NetCJDNS: 16,
		NetID(0): -1,
		NetID(7): -1,
	}
	for net, want := range cases {
		if got := net.addrLen(); got != want {
			t.Errorf("%s.addrLen() = %d, want %d", net, got, want)
		}
	}
}

func TestNewNetAddrLengthMismatch(t *testing.T) {
	if _, err := NewNetAddr(NetIPv4, []byte{1, 2, 3}, 30303); err == nil {
		t.Fatal("expected length mismatch error")
	}
	if _, err := NewNetAddr(NetIPv4, []byte{1, 2, 3, 4}, 30303); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := NewNetAddr(NetID(99), []byte{1, 2, 3, 4}, 30303); err == nil {
		t.Fatal("expected unknown network error")
	}
}

func TestFromAddrPortIPv4Mapped(t *testing.T) {
	ap := netip.MustParseAddrPort("[::ffff:1.2.3.4]:30303")
	a, ok := FromAddrPort(ap)
	if !ok {
		t.Fatal("FromAddrPort failed on IPv4-mapped v6")
	}
	if a.Network != NetIPv4 {
		t.Fatalf("expected NetIPv4, got %s", a.Network)
	}
	if !bytes.Equal(a.Bytes(), []byte{1, 2, 3, 4}) {
		t.Fatalf("wrong address bytes: %v", a.Bytes())
	}
}

func TestValidRejectsUnroutable(t *testing.T) {
	type c struct {
		name  string
		addr  NetAddr
		valid bool
	}
	mk := func(net NetID, b []byte, port uint16) NetAddr {
		a, err := NewNetAddr(net, b, port)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return a
	}
	cases := []c{
		{"ipv4-public", mk(NetIPv4, []byte{8, 8, 8, 8}, 30303), true},
		{"ipv4-rfc1918-10", mk(NetIPv4, []byte{10, 0, 0, 1}, 30303), false},
		{"ipv4-rfc1918-192.168", mk(NetIPv4, []byte{192, 168, 1, 1}, 30303), false},
		{"ipv4-loopback", mk(NetIPv4, []byte{127, 0, 0, 1}, 30303), false},
		{"ipv4-link-local", mk(NetIPv4, []byte{169, 254, 0, 1}, 30303), false},
		{"ipv4-cgnat", mk(NetIPv4, []byte{100, 64, 0, 1}, 30303), false},
		{"ipv4-multicast", mk(NetIPv4, []byte{224, 0, 0, 1}, 30303), false},
		{"ipv4-zero-port", mk(NetIPv4, []byte{8, 8, 8, 8}, 0), false},
		{"ipv6-loopback", mk(NetIPv6, append(make([]byte, 15), 1), 30303), false},
		{"ipv6-link-local", mk(NetIPv6, append([]byte{0xfe, 0x80}, make([]byte, 14)...), 30303), false},
	}
	for _, tc := range cases {
		if got := tc.addr.Valid(); got != tc.valid {
			t.Errorf("%s: Valid() = %v, want %v", tc.name, got, tc.valid)
		}
	}
}

func TestGroupIPv4Slash16(t *testing.T) {
	a, _ := NewNetAddr(NetIPv4, []byte{8, 8, 4, 4}, 30303)
	b, _ := NewNetAddr(NetIPv4, []byte{8, 8, 9, 9}, 30303)
	c, _ := NewNetAddr(NetIPv4, []byte{8, 9, 0, 0}, 30303)
	if !bytes.Equal(a.group(), b.group()) {
		t.Error("same /16 should share group")
	}
	if bytes.Equal(a.group(), c.group()) {
		t.Error("different /16 should have different group")
	}
}

func TestGroupIPv6Slash32(t *testing.T) {
	mk := func(prefix string) NetAddr {
		ip := netip.MustParseAddr(prefix)
		a, _ := NewNetAddr(NetIPv6, ip.AsSlice(), 30303)
		return a
	}
	a := mk("2001:db00:1234:5678::")
	b := mk("2001:db00:9999:9999::")
	c := mk("2001:db01:0000:0000::")
	if !bytes.Equal(a.group(), b.group()) {
		t.Error("same /32 should share group")
	}
	if bytes.Equal(a.group(), c.group()) {
		t.Error("different /32 should have different group")
	}
}

func TestGroupTorV3TopNibble(t *testing.T) {
	addr32 := func(first byte) NetAddr {
		b := make([]byte, 32)
		b[0] = first
		a, _ := NewNetAddr(NetTorV3, b, 30303)
		return a
	}
	// Bitcoin nBits=4 → the first 4 bits select the group. 0x0X and
	// 0x0Y collapse to the same group value (0x0F), but 0x10 and 0x0F
	// separate.
	a := addr32(0x01)
	b := addr32(0x0E)
	c := addr32(0x10)
	if !bytes.Equal(a.group(), b.group()) {
		t.Error("Tor v3 addresses with same top nibble should share group")
	}
	if bytes.Equal(a.group(), c.group()) {
		t.Error("Tor v3 addresses with different top nibble should differ")
	}
}
