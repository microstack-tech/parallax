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

package xhash

import (
	"math/big"

	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/primitives/types"
)

const (
	// Target block spacing in seconds
	BlockTargetSpacingSeconds = uint64(600)
)

// CalcNakamotoDifficulty computes the next difficulty using Bitcoin’s rule:
//
// - Keep difficulty constant within each retarget interval
// - On boundary: new = old * targetTimespan / actualTimespan
// - Clamp actualTimespan into [minTimespan, maxTimespan]
// - Ensure result >= 1
func CalcNakamotoDifficulty(config *chainparams.ChainConfig, parent *types.Header) *big.Int {
	nextHeight := new(big.Int).Add(parent.Number, big1).Uint64()
	var r uint64

	if config.XHash == nil {
		// If no xhash config is given, fall back to Parallax's original difficulty
		// adjustment scheme (which is basically Bitcoin's with a 10-minute target).
		r = 2016
	} else {
		r = config.XHash.RetargetIntervalBlocks
	}

	if r == 0 || (nextHeight%r) != 0 {
		return new(big.Int).Set(parent.Difficulty)
	}

	target := BlockTargetSpacingSeconds * r
	minT := target / 4
	maxT := target * 4

	actual := parent.Time - parent.EpochStartTime
	if actual < minT {
		actual = minT
	} else if actual > maxT {
		actual = maxT
	}

	old := new(big.Int).Set(parent.Difficulty)
	num := new(big.Int).Mul(old, new(big.Int).SetUint64(target))
	den := new(big.Int).SetUint64(actual)
	out := num.Div(num, den)

	if out.Sign() <= 0 {
		out.SetUint64(1)
	}

	return out
}
