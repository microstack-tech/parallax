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

package abi

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/tests/fuzzers/fuzzutil"
)

// FuzzAbi is the native fuzzing entry point wrapping the legacy go-fuzz
// target, which pack/unpack round-trips ABI arguments derived from the input.
func FuzzAbi(f *testing.F) {
	fuzzutil.SeedFromDir(f, "corpus")
	f.Add([]byte("\x20\x20\x20\x20\x20\x20\x20\x20\x80\x00\x00\x00\x20\x20\x20\x20\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
