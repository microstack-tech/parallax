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

func FuzzG1Add(f *testing.F)      { wrapZip(f, fuzzG1Add, "fuzz_g1_add") }
func FuzzG1Mul(f *testing.F)      { wrapZip(f, fuzzG1Mul, "fuzz_g1_mul") }
func FuzzG1MultiExp(f *testing.F) { wrapZip(f, fuzzG1MultiExp, "fuzz_g1_multiexp") }
func FuzzG2Add(f *testing.F)      { wrapZip(f, fuzzG2Add, "fuzz_g2_add") }
func FuzzG2Mul(f *testing.F)      { wrapZip(f, fuzzG2Mul, "fuzz_g2_mul") }
func FuzzG2MultiExp(f *testing.F) { wrapZip(f, fuzzG2MultiExp, "fuzz_g2_multiexp") }
func FuzzPairing(f *testing.F)    { wrapZip(f, fuzzPairing, "fuzz_pairing") }
func FuzzMapG1(f *testing.F)      { wrapZip(f, fuzzMapG1, "fuzz_map_g1") }
func FuzzMapG2(f *testing.F)      { wrapZip(f, fuzzMapG2, "fuzz_map_g2") }

// The cross targets consume the same precompile input encodings as their
// primary counterparts, so they reuse the matching seed corpora.
func FuzzCrossPairing(f *testing.F)    { wrapZip(f, fuzzCrossPairing, "fuzz_pairing") }
func FuzzCrossG1Add(f *testing.F)      { wrapZip(f, fuzzCrossG1Add, "fuzz_g1_add") }
func FuzzCrossG2Add(f *testing.F)      { wrapZip(f, fuzzCrossG2Add, "fuzz_g2_add") }
func FuzzCrossG1MultiExp(f *testing.F) { wrapZip(f, fuzzCrossG1MultiExp, "fuzz_g1_multiexp") }

func wrapZip(f *testing.F, fn func([]byte) int, corpus string) {
	f.Helper()
	fuzzutil.SeedFromZip(f, "testdata/"+corpus+"_seed_corpus.zip")
	f.Fuzz(func(t *testing.T, data []byte) {
		fn(data)
	})
}
