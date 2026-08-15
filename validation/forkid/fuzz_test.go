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

package forkid

import (
	"math"
	"testing"

	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
)

// FuzzForkIDValidation fuzzes the EIP-2124 fork ID validation filter with
// arbitrary remotely-announced IDs against the mainnet configuration.
//
// Invariants checked per iteration:
//   - the filter never panics
//   - the result is nil, ErrRemoteStale, or ErrLocalIncompatibleOrStale
//   - NewID is deterministic for a fixed (config, genesis, head)
func FuzzForkIDValidation(f *testing.F) {
	// The local mainnet fork ID hash at genesis (see TestCreation).
	local := checksumToBytes(0xff004b1f)
	f.Add([]byte{local[0], local[1], local[2], local[3]}, uint64(0))
	f.Add([]byte{local[0], local[1], local[2], local[3]}, uint64(math.MaxUint64))
	f.Add([]byte{local[0], local[1], local[2], local[3]}, uint64(1))
	// The testnet genesis checksum: an incompatible chain.
	testnet := checksumToBytes(0x55049867)
	f.Add([]byte{testnet[0], testnet[1], testnet[2], testnet[3]}, uint64(0))
	// Garbage checksums.
	f.Add([]byte{0x00, 0x00, 0x00, 0x00}, uint64(0))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff}, uint64(math.MaxUint64))
	f.Add([]byte{0xde, 0xad, 0xbe, 0xef}, uint64(1337))

	// Validate against a few different local head heights to exercise all
	// rule branches of the filter.
	heads := []uint64{0, 1, 100000, math.MaxUint64 / 2}
	filters := make([]Filter, len(heads))
	for i, head := range heads {
		filters[i] = newFilter(chainparams.MainnetChainConfig, chainparams.MainnetGenesisHash, func() uint64 { return head })
	}

	f.Fuzz(func(t *testing.T, hash []byte, next uint64) {
		var id ID
		copy(id.Hash[:], hash)
		id.Next = next

		for i, filter := range filters {
			err := filter(id)
			switch err {
			case nil, ErrRemoteStale, ErrLocalIncompatibleOrStale:
			default:
				t.Fatalf("head %d: unexpected validation error for id %x/%d: %v", heads[i], id.Hash, id.Next, err)
			}
		}

		// NewID must be deterministic.
		id1 := NewID(chainparams.MainnetChainConfig, chainparams.MainnetGenesisHash, next)
		id2 := NewID(chainparams.MainnetChainConfig, chainparams.MainnetGenesisHash, next)
		if id1 != id2 {
			t.Fatalf("NewID not deterministic at head %d: %x/%d != %x/%d", next, id1.Hash, id1.Next, id2.Hash, id2.Next)
		}
	})
}
