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
	"sync/atomic"

	"github.com/ParallaxProtocol/parallax/kernel"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/validation/state"
)

// statePrefetcher is a basic Prefetcher, which blindly executes a block on top
// of an arbitrary state with the goal of prefetching potentially useful state
// data from disk before the main block processor start executing.
type statePrefetcher struct {
	config *chainparams.ChainConfig // Chain configuration options
	bc     *BlockChain              // Canonical block chain
	engine kernel.Engine            // Consensus engine used for block rewards
}

// newStatePrefetcher initialises a new statePrefetcher.
func newStatePrefetcher(config *chainparams.ChainConfig, bc *BlockChain, engine kernel.Engine) *statePrefetcher {
	return &statePrefetcher{
		config: config,
		bc:     bc,
		engine: engine,
	}
}

// Prefetch processes the state changes according to the Parallax rules by running
// the transaction messages using the statedb, but any changes are discarded. The
// only goal is to pre-cache transaction signatures and state trie nodes.
func (p *statePrefetcher) Prefetch(block *types.Block, statedb *state.StateDB, cfg script.Config, interrupt *uint32) {
	var (
		header       = block.Header()
		gaspool      = new(GasPool).AddGas(block.GasLimit())
		blockContext = NewPVMBlockContext(header, p.bc, nil)
		pvm          = script.NewPVM(blockContext, script.TxContext{}, statedb, p.config, cfg)
		signer       = types.MakeSigner(p.config, header.Number)
	)
	// Iterate over and process the individual transactions
	byzantium := p.config.IsByzantium(block.Number())
	for i, tx := range block.Transactions() {
		// If block precaching was interrupted, abort
		if interrupt != nil && atomic.LoadUint32(interrupt) == 1 {
			return
		}
		// Convert the transaction into an executable message and pre-cache its sender
		msg, err := tx.AsMessage(signer, header.BaseFee)
		if err != nil {
			return // Also invalid block, bail out
		}
		statedb.Prepare(tx.Hash(), i)
		if err := precacheTransaction(msg, p.config, gaspool, statedb, header, pvm); err != nil {
			return // Ugh, something went horribly wrong, bail out
		}
		// If we're pre-byzantium, pre-load trie nodes for the intermediate root
		if !byzantium {
			statedb.IntermediateRoot(true)
		}
	}
	// If were post-byzantium, pre-load trie nodes for the final root hash
	if byzantium {
		statedb.IntermediateRoot(true)
	}
}

// precacheTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. The goal is not to execute
// the transaction successfully, rather to warm up touched data slots.
func precacheTransaction(msg types.Message, config *chainparams.ChainConfig, gaspool *GasPool, statedb *state.StateDB, header *types.Header, pvm *script.PVM) error {
	// Update the pvm with the new transaction context.
	pvm.Reset(NewPVMTxContext(msg), statedb)
	// Add addresses to access list if applicable
	_, err := ApplyMessage(pvm, msg, gaspool)
	return err
}
