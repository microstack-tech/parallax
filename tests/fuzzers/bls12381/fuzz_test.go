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

package bls

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/tests/fuzzers/fuzzutil"
)

func FuzzG1Add(f *testing.F)      { wrap(f, fuzzG1Add) }
func FuzzG1Mul(f *testing.F)      { wrap(f, fuzzG1Mul) }
func FuzzG1MultiExp(f *testing.F) { wrap(f, fuzzG1MultiExp) }
func FuzzG2Add(f *testing.F)      { wrap(f, fuzzG2Add) }
func FuzzG2Mul(f *testing.F)      { wrap(f, fuzzG2Mul) }
func FuzzG2MultiExp(f *testing.F) { wrap(f, fuzzG2MultiExp) }
func FuzzPairing(f *testing.F)    { wrap(f, fuzzPairing) }
func FuzzMapG1(f *testing.F)      { wrap(f, fuzzMapG1) }
func FuzzMapG2(f *testing.F)      { wrap(f, fuzzMapG2) }

func FuzzCrossPairing(f *testing.F)    { wrap(f, fuzzCrossPairing) }
func FuzzCrossG1Add(f *testing.F)      { wrap(f, fuzzCrossG1Add) }
func FuzzCrossG2Add(f *testing.F)      { wrap(f, fuzzCrossG2Add) }
func FuzzCrossG1MultiExp(f *testing.F) { wrap(f, fuzzCrossG1MultiExp) }

func wrap(f *testing.F, fn func([]byte) int) {
	f.Helper()
	fuzzutil.SeedFromDir(f, "corpus")
	f.Fuzz(func(t *testing.T, data []byte) {
		fn(data)
	})
}
