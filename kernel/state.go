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

package kernel

import (
	"math/big"

	"github.com/ParallaxProtocol/parallax/util"
)

// StateAccessor defines the minimal state interface needed by consensus engines
// during block finalization. This allows the kernel layer to remain independent
// of the concrete state implementation in validation/state.
type StateAccessor interface {
	// AddBalance adds amount to the account associated with addr.
	AddBalance(addr util.Address, amount *big.Int)

	// GetState retrieves a value from the given account's storage trie.
	GetState(addr util.Address, key util.Hash) util.Hash

	// SetState sets a value in the given account's storage trie.
	SetState(addr util.Address, key util.Hash, value util.Hash)

	// IntermediateRoot computes the current root hash of the state trie.
	IntermediateRoot(deleteEmptyObjects bool) util.Hash
}
