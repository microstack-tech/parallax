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
	"strings"
	"testing"
)

// FuzzParseOnion — arbitrary hostname strings through the onion
// decoder (PIP-0007). Asserts:
//
//   - No input panics; every failure surfaces as ErrNotOnion or
//     ErrBadOnion.
//   - A successful parse is canonical: re-encoding yields a hostname
//     that parses to an equal NetAddr, and equals the lowercased
//     input.
func FuzzParseOnion(f *testing.F) {
	f.Add("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion")
	f.Add("EXPYUZZ4WQQYQHJN.ONION")
	f.Add("x.onion")
	f.Add(strings.Repeat("a", 56) + ".onion")
	f.Add("203.0.113.7")
	f.Add(".onion")
	f.Add("\x00\xff.onion")

	f.Fuzz(func(t *testing.T, host string) {
		na, err := ParseOnion(host, 32110)
		if err != nil {
			return
		}
		render := na.OnionHostname()
		if render != strings.ToLower(strings.TrimSuffix(host, ".")) {
			t.Fatalf("accepted non-canonical form %q (canonical %q)", host, render)
		}
		back, err := ParseOnion(render, 32110)
		if err != nil {
			t.Fatalf("re-parse of own rendering %q failed: %v", render, err)
		}
		if !back.Equal(na) {
			t.Fatalf("round trip changed address: %v != %v", back, na)
		}
	})
}
