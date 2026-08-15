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

package netutil

import (
	"net"
	"testing"
)

// FuzzParseNetlist — arbitrary strings fed to ParseNetlist must never
// panic (errors are fine), and any successfully parsed Netlist must
// answer Contains without panicking for IPv4, IPv6, nil, and
// wrong-length IP inputs.
func FuzzParseNetlist(f *testing.F) {
	f.Add("")
	f.Add("10.0.0.0/8")
	f.Add("10.0.0.0/8, 192.168.0.0/16")
	f.Add("::/0")
	f.Add("2001:db8::/32,\n\t172.16.0.0/12")
	f.Add("0.0.0.0/0,255.255.255.255/32")
	f.Add("not-a-cidr")
	f.Add("300.300.300.300/40")
	f.Add("10.0.0.0/33")
	f.Add(",,, ,\n,")

	probeIPs := []net.IP{
		net.ParseIP("192.168.1.1"),
		net.ParseIP("8.8.8.8").To4(),
		net.ParseIP("2001:db8::1"),
		net.IPv6loopback,
		nil,             // nil-ish input
		net.IP{1, 2, 3}, // bogus length
	}

	f.Fuzz(func(t *testing.T, s string) {
		l, err := ParseNetlist(s)
		if err != nil {
			return
		}
		if l == nil {
			t.Fatal("ParseNetlist returned nil list with nil error")
		}
		for _, ip := range probeIPs {
			_ = l.Contains(ip)
		}
	})
}
