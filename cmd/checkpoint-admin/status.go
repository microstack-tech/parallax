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
	"fmt"

	"github.com/ParallaxProtocol/parallax/cmd/utils"
	"github.com/ParallaxProtocol/parallax/util"
	"gopkg.in/urfave/cli.v1"
)

var commandStatus = cli.Command{
	Name:  "status",
	Usage: "Fetches the signers and checkpoint status of the oracle contract",
	Flags: []cli.Flag{
		nodeURLFlag,
	},
	Action: utils.MigrateFlags(status),
}

// status fetches the admin list of specified registrar contract.
func status(ctx *cli.Context) error {
	// Create a wrapper around the checkpoint oracle contract
	addr, oracle := newContract(newRPCClient(ctx.GlobalString(nodeURLFlag.Name)))
	fmt.Printf("Oracle => %s\n", addr.Hex())
	fmt.Println()

	// Retrieve the list of authorized signers (admins)
	admins, err := oracle.Contract().GetAllAdmin(nil)
	if err != nil {
		return err
	}
	for i, admin := range admins {
		fmt.Printf("Admin %d => %s\n", i+1, admin.Hex())
	}
	fmt.Println()

	// Retrieve the latest checkpoint
	index, checkpoint, height, err := oracle.Contract().GetLatestCheckpoint(nil)
	if err != nil {
		return err
	}
	fmt.Printf("Checkpoint (published at #%d) %d => %s\n", height, index, util.Hash(checkpoint).Hex())

	return nil
}
