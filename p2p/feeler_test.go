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
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
)

// TestPickFeelerAddrPrefersTriedCollision — when SelectTriedCollision
// has an entry available it must be returned without falling back to
// Select(newOnly). Mirrors Bitcoin Core src/net.cpp:2796.
//
// Building a real tryCollision requires hammering Good() into a full
// tried bucket, which is brittle for a unit test. This test exercises
// the Selectable-fallback branch instead: with no collision and no
// new entries, pickFeelerAddr returns ok=false.
func TestPickFeelerAddrEmpty(t *testing.T) {
	t.Parallel()
	book, err := addrman.New(addrman.Deterministic(0xfee1))
	if err != nil {
		t.Fatalf("addrman.New: %v", err)
	}
	if _, ok := pickFeelerAddr(book); ok {
		t.Fatalf("empty addrman should not yield a feeler addr")
	}
}

// TestPickFeelerAddrFallsBackToSelectNewOnly — with no collisions
// and one new entry, pickFeelerAddr returns the new entry.
func TestPickFeelerAddrFallsBackToSelectNewOnly(t *testing.T) {
	t.Parallel()
	book, err := addrman.New(addrman.Deterministic(0xfee2))
	if err != nil {
		t.Fatalf("addrman.New: %v", err)
	}
	addr, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{1, 2, 3, 4}, 32110)
	src, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{5, 6, 7, 8}, 0)
	if !book.AddOne(addr, 0, nil, time.Now(), src, addrman.SourceDNSSeed, 0) {
		t.Fatalf("AddOne failed")
	}
	got, ok := pickFeelerAddr(book)
	if !ok {
		t.Fatalf("pickFeelerAddr returned ok=false; expected the new entry")
	}
	if !got.Equal(addr) {
		t.Fatalf("pickFeelerAddr returned %v; want %v", got, addr)
	}
}

// TestTcpFromNetAddrIPv4 — addrman.NetAddr → *net.TCPAddr round trip
// for IPv4. Feeler dial uses this to project addrman entries into the
// dial API (which takes *net.TCPAddr).
func TestTcpFromNetAddrIPv4(t *testing.T) {
	t.Parallel()
	addr, _ := addrman.NewNetAddr(addrman.NetIPv4, []byte{198, 51, 100, 7}, 32110)
	tcp := tcpFromNetAddr(addr)
	if tcp == nil {
		t.Fatalf("tcpFromNetAddr returned nil for IPv4")
	}
	if !tcp.IP.Equal(net.IPv4(198, 51, 100, 7).To4()) {
		t.Fatalf("tcpFromNetAddr.IP = %v; want 198.51.100.7", tcp.IP)
	}
	if tcp.Port != 32110 {
		t.Fatalf("tcpFromNetAddr.Port = %d; want 32110", tcp.Port)
	}
}

// TestTcpFromNetAddrIPv6 — same round trip for IPv6.
func TestTcpFromNetAddrIPv6(t *testing.T) {
	t.Parallel()
	v6 := net.ParseIP("2001:db8::42").To16()
	addr, _ := addrman.NewNetAddr(addrman.NetIPv6, v6, 32110)
	tcp := tcpFromNetAddr(addr)
	if tcp == nil {
		t.Fatalf("tcpFromNetAddr returned nil for IPv6")
	}
	if !tcp.IP.Equal(v6) {
		t.Fatalf("tcpFromNetAddr.IP = %v; want 2001:db8::42", tcp.IP)
	}
}

// TestTcpFromNetAddrTorReturnsNil — Tor / I2P / CJDNS aren't
// dialable via *net.TCPAddr. Project must return nil so the feeler
// short-circuits.
func TestTcpFromNetAddrTorReturnsNil(t *testing.T) {
	t.Parallel()
	tor := make([]byte, 32)
	addr, err := addrman.NewNetAddr(addrman.NetTorV3, tor, 32110)
	if err != nil {
		t.Fatalf("NewNetAddr Tor: %v", err)
	}
	if tcp := tcpFromNetAddr(addr); tcp != nil {
		t.Fatalf("tcpFromNetAddr returned non-nil for Tor: %v", tcp)
	}
}
