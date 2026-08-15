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

package validation

import (
	"math/big"
	mrand "math/rand"
	"testing"

	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/util"
)

// mockTdChainReader is a minimal ChainReader stub returning canned total
// difficulties keyed by block hash.
type mockTdChainReader struct {
	tds map[util.Hash]*big.Int
}

func (m *mockTdChainReader) Config() *chainparams.ChainConfig { return chainparams.TestChainConfig }

func (m *mockTdChainReader) GetTd(hash util.Hash, number uint64) *big.Int {
	return m.tds[hash] // nil for unknown hashes
}

// makeTdHeader creates a header with the given number and an extra byte so
// that headers with equal numbers still hash differently.
func makeTdHeader(number uint64, extra byte) *types.Header {
	return &types.Header{
		Number:     new(big.Int).SetUint64(number),
		Difficulty: big.NewInt(1),
		Extra:      []byte{extra},
	}
}

func TestForkChoiceReorgNeededTd(t *testing.T) {
	tests := []struct {
		name      string
		localNum  uint64
		externNum uint64
		localTd   int64
		externTd  int64
		want      bool
	}{
		{"extern higher td", 10, 10, 100, 101, true},
		{"extern lower td", 10, 10, 100, 99, false},
		// Equal td: the lower block number wins (shorter chain with the
		// same work is preferred, reducing selfish mining exposure).
		{"equal td extern lower number", 10, 9, 100, 100, true},
		{"equal td extern higher number", 10, 11, 100, 100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := makeTdHeader(tt.localNum, 1)
			extern := makeTdHeader(tt.externNum, 2)
			reader := &mockTdChainReader{tds: map[util.Hash]*big.Int{
				current.Hash(): big.NewInt(tt.localTd),
				extern.Hash():  big.NewInt(tt.externTd),
			}}
			fc := NewForkChoice(reader, nil)
			reorg, err := fc.ReorgNeeded(current, extern)
			if err != nil {
				t.Fatalf("ReorgNeeded returned error: %v", err)
			}
			if reorg != tt.want {
				t.Errorf("ReorgNeeded = %v, want %v", reorg, tt.want)
			}
		})
	}
}

func TestForkChoiceReorgNeededPreserveTiebreak(t *testing.T) {
	tests := []struct {
		name            string
		preserveCurrent bool
		preserveExtern  bool
		want            bool
	}{
		// If the local block is preserved (e.g. locally mined), never reorg.
		{"preserve current only", true, false, false},
		{"preserve both", true, true, false},
		// If only the extern block is preserved, always reorg.
		{"preserve extern only", false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := makeTdHeader(10, 1)
			extern := makeTdHeader(10, 2)
			reader := &mockTdChainReader{tds: map[util.Hash]*big.Int{
				current.Hash(): big.NewInt(100),
				extern.Hash():  big.NewInt(100),
			}}
			currentHash := current.Hash()
			preserve := func(header *types.Header) bool {
				if header.Hash() == currentHash {
					return tt.preserveCurrent
				}
				return tt.preserveExtern
			}
			fc := NewForkChoice(reader, preserve)
			reorg, err := fc.ReorgNeeded(current, extern)
			if err != nil {
				t.Fatalf("ReorgNeeded returned error: %v", err)
			}
			if reorg != tt.want {
				t.Errorf("ReorgNeeded = %v, want %v", reorg, tt.want)
			}
		})
	}
}

func TestForkChoiceReorgNeededRandomTiebreak(t *testing.T) {
	// Equal td, equal number and no preserve preference: the decision falls
	// through to the seeded random source. Inject a deterministic source and
	// pin the outcome against an identically seeded generator.
	preserves := map[string]func(*types.Header) bool{
		"nil preserve":   nil,
		"preserve false": func(*types.Header) bool { return false },
	}
	for name, preserve := range preserves {
		t.Run(name, func(t *testing.T) {
			for seed := int64(0); seed < 10; seed++ {
				current := makeTdHeader(10, 1)
				extern := makeTdHeader(10, 2)
				reader := &mockTdChainReader{tds: map[util.Hash]*big.Int{
					current.Hash(): big.NewInt(100),
					extern.Hash():  big.NewInt(100),
				}}
				fc := NewForkChoice(reader, preserve)
				fc.rand = mrand.New(mrand.NewSource(seed))
				want := mrand.New(mrand.NewSource(seed)).Float64() < 0.5

				reorg, err := fc.ReorgNeeded(current, extern)
				if err != nil {
					t.Fatalf("seed %d: ReorgNeeded returned error: %v", seed, err)
				}
				if reorg != want {
					t.Errorf("seed %d: ReorgNeeded = %v, want %v", seed, reorg, want)
				}
			}
		})
	}
}

func TestForkChoiceReorgNeededMissingTd(t *testing.T) {
	current := makeTdHeader(10, 1)
	extern := makeTdHeader(11, 2)
	knownTd := map[util.Hash]*big.Int{
		current.Hash(): big.NewInt(100),
		extern.Hash():  big.NewInt(101),
	}
	tests := []struct {
		name string
		omit []util.Hash
	}{
		{"missing local td", []util.Hash{current.Hash()}},
		{"missing extern td", []util.Hash{extern.Hash()}},
		{"missing both tds", []util.Hash{current.Hash(), extern.Hash()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tds := make(map[util.Hash]*big.Int)
			for hash, td := range knownTd {
				tds[hash] = td
			}
			for _, hash := range tt.omit {
				delete(tds, hash)
			}
			fc := NewForkChoice(&mockTdChainReader{tds: tds}, nil)
			reorg, err := fc.ReorgNeeded(current, extern)
			if err == nil {
				t.Fatal("ReorgNeeded returned no error for missing td")
			}
			if err.Error() != "missing td" {
				t.Errorf("unexpected error: %v", err)
			}
			if reorg {
				t.Error("ReorgNeeded = true on error, want false")
			}
		})
	}
}
