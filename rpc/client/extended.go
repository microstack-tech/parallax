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

package client

import (
	"context"
	"math/big"
	"runtime"
	"runtime/debug"

	parallax "github.com/ParallaxProtocol/parallax"
	"github.com/ParallaxProtocol/parallax/net/p2p"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/rpc"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/util/hexutil"
)

// AccountResult is the result of a GetProof operation.
type AccountResult struct {
	Address      util.Address    `json:"address"`
	AccountProof []string        `json:"accountProof"`
	Balance      *big.Int        `json:"balance"`
	CodeHash     util.Hash       `json:"codeHash"`
	Nonce        uint64          `json:"nonce"`
	StorageHash  util.Hash       `json:"storageHash"`
	StorageProof []StorageResult `json:"storageProof"`
}

// StorageResult provides a proof for a key-value pair.
type StorageResult struct {
	Key   string   `json:"key"`
	Value *big.Int `json:"value"`
	Proof []string `json:"proof"`
}

// OverrideAccount specifies the state of an account to be overridden.
type OverrideAccount struct {
	Nonce     uint64                  `json:"nonce"`
	Code      []byte                  `json:"code"`
	Balance   *big.Int                `json:"balance"`
	State     map[util.Hash]util.Hash `json:"state"`
	StateDiff map[util.Hash]util.Hash `json:"stateDiff"`
}

// CreateAccessList tries to create an access list for a specific transaction based on the
// current pending state of the blockchain.
func (ec *Client) CreateAccessList(ctx context.Context, msg parallax.CallMsg) (*types.AccessList, uint64, string, error) {
	type accessListResult struct {
		Accesslist *types.AccessList `json:"accessList"`
		Error      string            `json:"error,omitempty"`
		GasUsed    hexutil.Uint64    `json:"gasUsed"`
	}
	var result accessListResult
	if err := ec.c.CallContext(ctx, &result, "eth_createAccessList", toCallArg(msg)); err != nil {
		return nil, 0, "", err
	}
	return result.Accesslist, uint64(result.GasUsed), result.Error, nil
}

// GetProof returns the account and storage values of the specified account including the Merkle-proof.
// The block number can be nil, in which case the value is taken from the latest known block.
func (ec *Client) GetProof(ctx context.Context, account util.Address, keys []string, blockNumber *big.Int) (*AccountResult, error) {
	type storageResult struct {
		Key   string       `json:"key"`
		Value *hexutil.Big `json:"value"`
		Proof []string     `json:"proof"`
	}

	type accountResult struct {
		Address      util.Address    `json:"address"`
		AccountProof []string        `json:"accountProof"`
		Balance      *hexutil.Big    `json:"balance"`
		CodeHash     util.Hash       `json:"codeHash"`
		Nonce        hexutil.Uint64  `json:"nonce"`
		StorageHash  util.Hash       `json:"storageHash"`
		StorageProof []storageResult `json:"storageProof"`
	}

	var res accountResult
	err := ec.c.CallContext(ctx, &res, "eth_getProof", account, keys, toBlockNumArg(blockNumber))
	// Turn hexutils back to normal datatypes
	storageResults := make([]StorageResult, 0, len(res.StorageProof))
	for _, st := range res.StorageProof {
		storageResults = append(storageResults, StorageResult{
			Key:   st.Key,
			Value: st.Value.ToInt(),
			Proof: st.Proof,
		})
	}
	result := AccountResult{
		Address:      res.Address,
		AccountProof: res.AccountProof,
		Balance:      res.Balance.ToInt(),
		Nonce:        uint64(res.Nonce),
		CodeHash:     res.CodeHash,
		StorageHash:  res.StorageHash,
		StorageProof: storageResults,
	}
	return &result, err
}

// CallContractWithOverrides executes a message call transaction with state overrides.
//
// blockNumber selects the block height at which the call runs. It can be nil, in which
// case the code is taken from the latest known block.
//
// overrides specifies a map of contract states that should be overwritten before executing
// the message call.
func (ec *Client) CallContractWithOverrides(ctx context.Context, msg parallax.CallMsg, blockNumber *big.Int, overrides *map[util.Address]OverrideAccount) ([]byte, error) {
	var hex hexutil.Bytes
	err := ec.c.CallContext(
		ctx, &hex, "eth_call", toCallArg(msg),
		toBlockNumArg(blockNumber), toOverrideMap(overrides),
	)
	return hex, err
}

// GCStats retrieves the current garbage collection stats from a node.
func (ec *Client) GCStats(ctx context.Context) (*debug.GCStats, error) {
	var result debug.GCStats
	err := ec.c.CallContext(ctx, &result, "debug_gcStats")
	return &result, err
}

// MemStats retrieves the current memory stats from a node.
func (ec *Client) MemStats(ctx context.Context) (*runtime.MemStats, error) {
	var result runtime.MemStats
	err := ec.c.CallContext(ctx, &result, "debug_memStats")
	return &result, err
}

// SetHead sets the current head of the local chain by block number.
// Note, this is a destructive action and may severely damage your chain.
// Use with extreme caution.
func (ec *Client) SetHead(ctx context.Context, number *big.Int) error {
	return ec.c.CallContext(ctx, nil, "debug_setHead", toBlockNumArg(number))
}

// GetNodeInfo retrieves the node info of a node.
func (ec *Client) GetNodeInfo(ctx context.Context) (*p2p.NodeInfo, error) {
	var result p2p.NodeInfo
	err := ec.c.CallContext(ctx, &result, "admin_nodeInfo")
	return &result, err
}

// SubscribePendingTransactions subscribes to new pending transactions.
func (ec *Client) SubscribePendingTransactions(ctx context.Context, ch chan<- util.Hash) (*rpc.ClientSubscription, error) {
	return ec.c.ParallaxSubscribe(ctx, ch, "newPendingTransactions")
}

func toOverrideMap(overrides *map[util.Address]OverrideAccount) any {
	if overrides == nil {
		return nil
	}
	type overrideAccount struct {
		Nonce     hexutil.Uint64          `json:"nonce"`
		Code      hexutil.Bytes           `json:"code"`
		Balance   *hexutil.Big            `json:"balance"`
		State     map[util.Hash]util.Hash `json:"state"`
		StateDiff map[util.Hash]util.Hash `json:"stateDiff"`
	}
	result := make(map[util.Address]overrideAccount)
	for addr, override := range *overrides {
		result[addr] = overrideAccount{
			Nonce:     hexutil.Uint64(override.Nonce),
			Code:      override.Code,
			Balance:   (*hexutil.Big)(override.Balance),
			State:     override.State,
			StateDiff: override.StateDiff,
		}
	}
	return &result
}
