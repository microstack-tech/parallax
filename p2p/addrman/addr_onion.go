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

package addrman

import (
	"bytes"
	"crypto/sha3"
	"encoding/base32"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Tor v3 onion service address codec (rend-spec-v3.txt §6):
//
//	onion_address = base32(PUBKEY | CHECKSUM | VERSION) + ".onion"
//	CHECKSUM = H(".onion checksum" | PUBKEY | VERSION)[:2]
//
// where PUBKEY is the 32-byte ed25519 service key, VERSION is 0x03
// and H is SHA3-256. BIP155 (and NetAddr) stores only PUBKEY; the
// checksum and version are reconstructed when forming the hostname.
// Same round-trip as Bitcoin Core's CNetAddr::ToStringAddr /
// ParseOnionAddress (src/netaddress.cpp, pinned v31.0).

const (
	onionV3Version   = 0x03
	onionV3HostLen   = 56 // base32(32+2+1 bytes), no padding
	onionChecksumTag = ".onion checksum"
)

// onionBase32 is lowercase RFC 4648 base32 without padding, the
// canonical onion hostname alphabet.
var onionBase32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

var (
	// ErrNotOnion: the string is not a .onion hostname at all.
	ErrNotOnion = errors.New("addrman: not a .onion address")
	// ErrBadOnion: it is a .onion name but not a valid v3 one —
	// wrong length (v2 names are 16 chars and are rejected here),
	// bad alphabet, checksum mismatch, or unknown version byte.
	ErrBadOnion = errors.New("addrman: malformed v3 .onion address")
)

// onionChecksum computes CHECKSUM for a 32-byte service pubkey.
func onionChecksum(pubkey []byte) [2]byte {
	h := sha3.New256()
	h.Write([]byte(onionChecksumTag))
	h.Write(pubkey)
	h.Write([]byte{onionV3Version})
	var sum [2]byte
	copy(sum[:], h.Sum(nil)[:2])
	return sum
}

// OnionHostname renders a NetTorV3 address as its canonical
// "<base32>.onion" hostname (no port). Empty string for any other
// network — callers must check.
func (a NetAddr) OnionHostname() string {
	if a.Network != NetTorV3 {
		return ""
	}
	pub := a.Bytes()
	sum := onionChecksum(pub)
	raw := make([]byte, 0, 35)
	raw = append(raw, pub...)
	raw = append(raw, sum[0], sum[1], onionV3Version)
	return onionBase32.EncodeToString(raw) + ".onion"
}

// ParseHostPort parses a "host:port" dial target into a NetAddr,
// accepting both "ip:port" and "<base32>.onion:port" forms — the
// shared parser behind bootnode lists, admin RPC targets, and
// operator flags (PIP-0007).
func ParseHostPort(s string) (NetAddr, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return NetAddr{}, fmt.Errorf("addrman: invalid host:port %q: %w", s, err)
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port64 == 0 {
		return NetAddr{}, fmt.Errorf("addrman: invalid port in %q", s)
	}
	port := uint16(port64)
	na, oerr := ParseOnion(host, port)
	switch {
	case oerr == nil:
		return na, nil
	case !errors.Is(oerr, ErrNotOnion):
		// It named a .onion but a malformed one — surface that
		// instead of a confusing invalid-ip error.
		return NetAddr{}, oerr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return NetAddr{}, fmt.Errorf("addrman: invalid host %q", host)
	}
	if v4 := ip.To4(); v4 != nil {
		return NewNetAddr(NetIPv4, v4, port)
	}
	return NewNetAddr(NetIPv6, ip.To16(), port)
}

// ParseOnion parses a "<base32>.onion" hostname into a NetTorV3
// NetAddr with the given port. Case-insensitive. Returns ErrNotOnion
// when the suffix is missing (so callers can fall through to IP
// parsing) and ErrBadOnion for a .onion name that fails validation.
func ParseOnion(host string, port uint16) (NetAddr, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	base, ok := strings.CutSuffix(host, ".onion")
	if !ok {
		return NetAddr{}, ErrNotOnion
	}
	if len(base) != onionV3HostLen {
		return NetAddr{}, fmt.Errorf("%w: %d-char label", ErrBadOnion, len(base))
	}
	raw, err := onionBase32.DecodeString(base)
	if err != nil {
		return NetAddr{}, fmt.Errorf("%w: %v", ErrBadOnion, err)
	}
	if len(raw) != 35 {
		return NetAddr{}, fmt.Errorf("%w: %d decoded bytes", ErrBadOnion, len(raw))
	}
	pub, sum, ver := raw[:32], raw[32:34], raw[34]
	if ver != onionV3Version {
		return NetAddr{}, fmt.Errorf("%w: version %d", ErrBadOnion, ver)
	}
	want := onionChecksum(pub)
	if !bytes.Equal(sum, want[:]) {
		return NetAddr{}, fmt.Errorf("%w: checksum mismatch", ErrBadOnion)
	}
	return NewNetAddr(NetTorV3, pub, port)
}
