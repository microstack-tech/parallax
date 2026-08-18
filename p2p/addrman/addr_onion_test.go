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
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

// torProjectOnion is the Tor Project's public v3 onion service for
// www.torproject.org — a fixed external vector proving conformance
// with rend-spec-v3 (same class of vector Core uses in
// netbase_tests.cpp).
const torProjectOnion = "2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion"

func TestOnionRoundTrip(t *testing.T) {
	for range 32 {
		var pub [32]byte
		if _, err := rand.Read(pub[:]); err != nil {
			t.Fatal(err)
		}
		na, err := NewNetAddr(NetTorV3, pub[:], 32110)
		if err != nil {
			t.Fatal(err)
		}
		host := na.OnionHostname()
		if len(host) != onionV3HostLen+len(".onion") || !strings.HasSuffix(host, ".onion") {
			t.Fatalf("hostname %q has wrong shape", host)
		}
		back, err := ParseOnion(host, 32110)
		if err != nil {
			t.Fatalf("ParseOnion(%q): %v", host, err)
		}
		if !back.Equal(na) {
			t.Fatalf("round trip changed the address: %v != %v", back, na)
		}
	}
}

func TestOnionKnownVector(t *testing.T) {
	na, err := ParseOnion(torProjectOnion, 32110)
	if err != nil {
		t.Fatalf("known-good address rejected: %v", err)
	}
	if na.Network != NetTorV3 || na.Port != 32110 {
		t.Fatalf("parsed as %v", na)
	}
	if got := na.OnionHostname(); got != torProjectOnion {
		t.Errorf("re-encoded to %q, want %q", got, torProjectOnion)
	}
	// Uppercase input is accepted, canonical output is lowercase.
	if _, err := ParseOnion(strings.ToUpper(torProjectOnion), 1); err != nil {
		t.Errorf("uppercase form rejected: %v", err)
	}
	// String() renders the onion form for logs.
	if got := na.String(); got != torProjectOnion+":32110" {
		t.Errorf("String() = %q", got)
	}
}

func TestOnionParseRejections(t *testing.T) {
	if _, err := ParseOnion("203.0.113.7", 1); !errors.Is(err, ErrNotOnion) {
		t.Errorf("non-onion host: got %v, want ErrNotOnion", err)
	}
	cases := []string{
		// v2 (16-char) names are decode-only wire-side and never
		// parseable as dial targets.
		"expyuzz4wqqyqhjn.onion",
		// Corrupted checksum: flip the first character.
		"x" + torProjectOnion[1:],
		// Bad alphabet (base32 has no '0' or '1').
		strings.Repeat("0", onionV3HostLen) + ".onion",
		// Truncated.
		torProjectOnion[10:],
	}
	for _, c := range cases {
		if _, err := ParseOnion(c, 1); !errors.Is(err, ErrBadOnion) {
			t.Errorf("ParseOnion(%q): got %v, want ErrBadOnion", c, err)
		}
	}
}

func TestOnionHostnameNonTor(t *testing.T) {
	na, err := NewNetAddr(NetIPv4, []byte{1, 2, 3, 4}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := na.OnionHostname(); got != "" {
		t.Errorf("OnionHostname on ipv4 = %q, want empty", got)
	}
}
