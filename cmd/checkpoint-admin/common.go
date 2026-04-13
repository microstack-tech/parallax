// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of parallax.
//
// parallax is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// parallax is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with parallax. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"strconv"

	"github.com/ParallaxProtocol/parallax/abi/bind"
	"github.com/ParallaxProtocol/parallax/cmd/utils"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/net/les/contracts/checkpointoracle"
	"github.com/ParallaxProtocol/parallax/rpc"
	client "github.com/ParallaxProtocol/parallax/rpc/client"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/wallet"
	"github.com/ParallaxProtocol/parallax/wallet/external"
	"gopkg.in/urfave/cli.v1"
)

// newClient creates a client with specified remote URL.
func newClient(ctx *cli.Context) *client.Client {
	c, err := client.Dial(ctx.GlobalString(nodeURLFlag.Name))
	if err != nil {
		utils.Fatalf("Failed to connect to Parallax node: %v", err)
	}
	return c
}

// newRPCClient creates a rpc client with specified node URL.
func newRPCClient(url string) *rpc.Client {
	client, err := rpc.Dial(url)
	if err != nil {
		utils.Fatalf("Failed to connect to Parallax node: %v", err)
	}
	return client
}

// getContractAddr retrieves the register contract address through
// rpc request.
func getContractAddr(client *rpc.Client) util.Address {
	var addr string
	if err := client.Call(&addr, "les_getCheckpointContractAddress"); err != nil {
		utils.Fatalf("Failed to fetch checkpoint oracle address: %v", err)
	}
	return util.HexToAddress(addr)
}

// getCheckpoint retrieves the specified checkpoint or the latest one
// through rpc request.
func getCheckpoint(ctx *cli.Context, client *rpc.Client) *chainparams.TrustedCheckpoint {
	var checkpoint *chainparams.TrustedCheckpoint

	if ctx.GlobalIsSet(indexFlag.Name) {
		var result [3]string
		index := uint64(ctx.GlobalInt64(indexFlag.Name))
		if err := client.Call(&result, "les_getCheckpoint", index); err != nil {
			utils.Fatalf("Failed to get local checkpoint %v, please ensure the les API is exposed", err)
		}
		checkpoint = &chainparams.TrustedCheckpoint{
			SectionIndex: index,
			SectionHead:  util.HexToHash(result[0]),
			CHTRoot:      util.HexToHash(result[1]),
			BloomRoot:    util.HexToHash(result[2]),
		}
	} else {
		var result [4]string
		err := client.Call(&result, "les_latestCheckpoint")
		if err != nil {
			utils.Fatalf("Failed to get local checkpoint %v, please ensure the les API is exposed", err)
		}
		index, err := strconv.ParseUint(result[0], 0, 64)
		if err != nil {
			utils.Fatalf("Failed to parse checkpoint index %v", err)
		}
		checkpoint = &chainparams.TrustedCheckpoint{
			SectionIndex: index,
			SectionHead:  util.HexToHash(result[1]),
			CHTRoot:      util.HexToHash(result[2]),
			BloomRoot:    util.HexToHash(result[3]),
		}
	}
	return checkpoint
}

// newContract creates a registrar contract instance with specified
// contract address or the default contracts for mainnet or testnet.
func newContract(conn *rpc.Client) (util.Address, *checkpointoracle.CheckpointOracle) {
	addr := getContractAddr(conn)
	if addr == (util.Address{}) {
		utils.Fatalf("No specified registrar contract address")
	}
	contract, err := checkpointoracle.NewCheckpointOracle(addr, client.NewClient(conn))
	if err != nil {
		utils.Fatalf("Failed to setup registrar contract %s: %v", addr, err)
	}
	return addr, contract
}

// newClefSigner sets up a clef backend and returns a clef transaction signer.
func newClefSigner(ctx *cli.Context) *bind.TransactOpts {
	clef, err := external.NewExternalSigner(ctx.String(clefURLFlag.Name))
	if err != nil {
		utils.Fatalf("Failed to create clef signer %v", err)
	}
	return bind.NewClefTransactor(clef, wallet.Account{Address: util.HexToAddress(ctx.String(signerFlag.Name))})
}
