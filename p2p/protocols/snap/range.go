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

package snap

import (
	"math/big"

	"github.com/ParallaxProtocol/parallax/util"
	"github.com/holiman/uint256"
)

// hashRange is a utility to handle ranges of hashes, Split up the
// hash-space into sections, and 'walk' over the sections
type hashRange struct {
	current *uint256.Int
	step    *uint256.Int
}

// newHashRange creates a new hashRange, initiated at the start position,
// and with the step set to fill the desired 'num' chunks
func newHashRange(start util.Hash, num uint64) *hashRange {
	left := new(big.Int).Sub(hashSpace, start.Big())
	step := new(big.Int).Div(
		new(big.Int).Add(left, new(big.Int).SetUint64(num-1)),
		new(big.Int).SetUint64(num),
	)
	step256 := new(uint256.Int)
	step256.SetFromBig(step)

	return &hashRange{
		current: new(uint256.Int).SetBytes32(start[:]),
		step:    step256,
	}
}

// Next pushes the hash range to the next interval.
func (r *hashRange) Next() bool {
	next, overflow := new(uint256.Int).AddOverflow(r.current, r.step)
	if overflow {
		return false
	}
	r.current = next
	return true
}

// Start returns the first hash in the current interval.
func (r *hashRange) Start() util.Hash {
	return r.current.Bytes32()
}

// End returns the last hash in the current interval.
func (r *hashRange) End() util.Hash {
	// If the end overflows (non divisible range), return a shorter interval
	next, overflow := new(uint256.Int).AddOverflow(r.current, r.step)
	if overflow {
		return util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	}
	return next.SubUint64(next, 1).Bytes32()
}

// incHash returns the next hash, in lexicographical order (a.k.a plus one)
func incHash(h util.Hash) util.Hash {
	var a uint256.Int
	a.SetBytes32(h[:])
	a.AddUint64(&a, 1)
	return util.Hash(a.Bytes32())
}
