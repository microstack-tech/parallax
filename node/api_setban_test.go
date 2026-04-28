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

package node

import (
	"net"
	"testing"
)

// TestParseBanSubnetSingleIP — bare "1.2.3.4" expands to /32.
// Bitcoin Core's setban accepts the same shorthand.
func TestParseBanSubnetSingleIP(t *testing.T) {
	t.Parallel()
	subnet, err := parseBanSubnet("1.2.3.4")
	if err != nil {
		t.Fatalf("parseBanSubnet: %v", err)
	}
	ones, bits := subnet.Mask.Size()
	if ones != 32 || bits != 32 {
		t.Fatalf("mask = /%d (bits=%d), want /32", ones, bits)
	}
	if !subnet.IP.Equal(net.IPv4(1, 2, 3, 4).To4()) {
		t.Errorf("IP = %v, want 1.2.3.4", subnet.IP)
	}
}

// TestParseBanSubnetCIDR — "10.0.0.0/24" parses as a /24 IPNet.
func TestParseBanSubnetCIDR(t *testing.T) {
	t.Parallel()
	subnet, err := parseBanSubnet("10.0.0.0/24")
	if err != nil {
		t.Fatalf("parseBanSubnet: %v", err)
	}
	ones, _ := subnet.Mask.Size()
	if ones != 24 {
		t.Fatalf("mask = /%d, want /24", ones)
	}
	if !subnet.Contains(net.IPv4(10, 0, 0, 99)) {
		t.Errorf("subnet does not contain expected IP")
	}
}

// TestParseBanSubnetIPv6 — bare IPv6 expands to /128.
func TestParseBanSubnetIPv6(t *testing.T) {
	t.Parallel()
	subnet, err := parseBanSubnet("2001:db8::42")
	if err != nil {
		t.Fatalf("parseBanSubnet: %v", err)
	}
	ones, bits := subnet.Mask.Size()
	if ones != 128 || bits != 128 {
		t.Fatalf("mask = /%d (bits=%d), want /128", ones, bits)
	}
}

// TestParseBanSubnetRejectsEmpty — empty string is an error
// (Bitcoin Core's setban rejects empty subnet too).
func TestParseBanSubnetRejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := parseBanSubnet("   "); err == nil {
		t.Fatalf("expected error for empty subnet")
	}
}

// TestParseBanSubnetRejectsGarbage — random non-IP-shaped input
// returns an error.
func TestParseBanSubnetRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := parseBanSubnet("not.an.ip"); err == nil {
		t.Fatalf("expected error for non-IP input")
	}
	if _, err := parseBanSubnet("1.2.3.4/notanint"); err == nil {
		t.Fatalf("expected error for malformed CIDR")
	}
}
