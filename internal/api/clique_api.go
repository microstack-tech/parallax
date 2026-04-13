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

package api

import (
	"encoding/json"
	"fmt"

	"github.com/ParallaxProtocol/parallax/kernel"
	"github.com/ParallaxProtocol/parallax/kernel/clique"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/rpc"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/util/hexutil"
)

// CliqueAPI is a user facing RPC API to allow controlling the signer and voting
// mechanisms of the proof-of-authority scheme.
type CliqueAPI struct {
	chain  kernel.ChainHeaderReader
	clique *clique.Clique
}

// NewCliqueAPI creates a new Clique API instance.
func NewCliqueAPI(chain kernel.ChainHeaderReader, c *clique.Clique) *CliqueAPI {
	return &CliqueAPI{chain: chain, clique: c}
}

// GetSnapshot retrieves the state snapshot at a given block.
func (api *CliqueAPI) GetSnapshot(number *rpc.BlockNumber) (*clique.Snapshot, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, clique.ErrUnknownBlock
	}
	return api.clique.GetSnapshot(api.chain, header.Number.Uint64(), header.Hash())
}

// GetSnapshotAtHash retrieves the state snapshot at a given block.
func (api *CliqueAPI) GetSnapshotAtHash(hash util.Hash) (*clique.Snapshot, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, clique.ErrUnknownBlock
	}
	return api.clique.GetSnapshot(api.chain, header.Number.Uint64(), header.Hash())
}

// GetSigners retrieves the list of authorized signers at the specified block.
func (api *CliqueAPI) GetSigners(number *rpc.BlockNumber) ([]util.Address, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, clique.ErrUnknownBlock
	}
	snap, err := api.clique.GetSnapshot(api.chain, header.Number.Uint64(), header.Hash())
	if err != nil {
		return nil, err
	}
	return snap.SignerList(), nil
}

// GetSignersAtHash retrieves the list of authorized signers at the specified block.
func (api *CliqueAPI) GetSignersAtHash(hash util.Hash) ([]util.Address, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, clique.ErrUnknownBlock
	}
	snap, err := api.clique.GetSnapshot(api.chain, header.Number.Uint64(), header.Hash())
	if err != nil {
		return nil, err
	}
	return snap.SignerList(), nil
}

// Proposals returns the current proposals the node tries to uphold and vote on.
func (api *CliqueAPI) Proposals() map[util.Address]bool {
	return api.clique.GetProposals()
}

// Propose injects a new authorization proposal that the signer will attempt to
// push through.
func (api *CliqueAPI) Propose(address util.Address, auth bool) {
	api.clique.SetProposal(address, auth)
}

// Discard drops a currently running proposal, stopping the signer from casting
// further votes (either for or against).
func (api *CliqueAPI) Discard(address util.Address) {
	api.clique.DeleteProposal(address)
}

type cliqueStatus struct {
	InturnPercent float64              `json:"inturnPercent"`
	SigningStatus map[util.Address]int `json:"sealerActivity"`
	NumBlocks     uint64               `json:"numBlocks"`
}

// Status returns the status of the last N blocks.
func (api *CliqueAPI) Status() (*cliqueStatus, error) {
	var (
		numBlocks = uint64(64)
		header    = api.chain.CurrentHeader()
		optimals  = 0
	)
	snap, err := api.clique.GetSnapshot(api.chain, header.Number.Uint64(), header.Hash())
	if err != nil {
		return nil, err
	}
	var (
		signers = snap.SignerList()
		end     = header.Number.Uint64()
		start   = end - numBlocks
	)
	if numBlocks > end {
		start = 1
		numBlocks = end - start
	}
	signStatus := make(map[util.Address]int)
	for _, s := range signers {
		signStatus[s] = 0
	}
	for n := start; n < end; n++ {
		h := api.chain.GetHeaderByNumber(n)
		if h == nil {
			return nil, fmt.Errorf("missing block %d", n)
		}
		if h.Difficulty.Cmp(clique.DiffInTurn) == 0 {
			optimals++
		}
		sealer, err := api.clique.Author(h)
		if err != nil {
			return nil, err
		}
		signStatus[sealer]++
	}
	return &cliqueStatus{
		InturnPercent: float64(100*optimals) / float64(numBlocks),
		SigningStatus: signStatus,
		NumBlocks:     numBlocks,
	}, nil
}

type cliqueBlockNumberOrHashOrRLP struct {
	*rpc.BlockNumberOrHash
	RLP hexutil.Bytes `json:"rlp,omitempty"`
}

func (sb *cliqueBlockNumberOrHashOrRLP) UnmarshalJSON(data []byte) error {
	bnOrHash := new(rpc.BlockNumberOrHash)
	if err := bnOrHash.UnmarshalJSON(data); err == nil {
		sb.BlockNumberOrHash = bnOrHash
		return nil
	}
	var input string
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	blob, err := hexutil.Decode(input)
	if err != nil {
		return err
	}
	sb.RLP = blob
	return nil
}

// GetSigner returns the signer for a specific clique block.
func (api *CliqueAPI) GetSigner(rlpOrBlockNr *cliqueBlockNumberOrHashOrRLP) (util.Address, error) {
	if len(rlpOrBlockNr.RLP) == 0 {
		blockNrOrHash := rlpOrBlockNr.BlockNumberOrHash
		var header *types.Header
		if blockNrOrHash == nil {
			header = api.chain.CurrentHeader()
		} else if hash, ok := blockNrOrHash.Hash(); ok {
			header = api.chain.GetHeaderByHash(hash)
		} else if number, ok := blockNrOrHash.Number(); ok {
			header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
		}
		if header == nil {
			return util.Address{}, fmt.Errorf("missing block %v", blockNrOrHash.String())
		}
		return api.clique.Author(header)
	}
	block := new(types.Block)
	if err := rlp.DecodeBytes(rlpOrBlockNr.RLP, block); err == nil {
		return api.clique.Author(block.Header())
	}
	header := new(types.Header)
	if err := rlp.DecodeBytes(rlpOrBlockNr.RLP, header); err != nil {
		return util.Address{}, err
	}
	return api.clique.Author(header)
}
