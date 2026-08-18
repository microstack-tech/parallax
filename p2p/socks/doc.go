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

// Package socks implements the SOCKS5 client used for proxied outbound
// connections (PIP-0007). It is a port of Bitcoin Core's Socks5() and
// ConnectThroughProxy() from src/netbase.cpp, including the Tor extension
// reply codes (https://spec.torproject.org/socks-extensions.html) and the
// stream-isolation credential generator that places each connection on a
// distinct Tor circuit.
//
// The target is always sent as a DOMAINNAME (ATYP 0x03), even for IP
// literals — the proxy performs any name resolution, so no DNS query ever
// leaves the local host. This mirrors Core, which formats every destination
// as a string.
//
// Pinned reference: Bitcoin Core tag v31.0.
package socks
