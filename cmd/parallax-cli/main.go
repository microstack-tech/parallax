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

// parallax-cli is the JSON-RPC command-line client for a running parallaxd
// daemon. It mirrors the command surface of bitcoin-cli: a minimal set of
// global flags (--datadir, --testnet, --rpc) and a large family of sugar
// commands (info, peers, balance, sendraw, ...) that each dial the node's
// IPC or HTTP endpoint and print a human-friendly result.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/ParallaxProtocol/parallax/v2/cmd/utils"
	"github.com/ParallaxProtocol/parallax/v2/internal/flags"

	"gopkg.in/urfave/cli.v1"
)

var (
	gitCommit = ""
	gitDate   = ""
	app       = flags.NewApp(gitCommit, gitDate, "the Parallax JSON-RPC command-line client")
)

func init() {
	app.HideVersion = true
	app.Copyright = "Copyright 2025-2026 The Parallax Protocol Authors"
	app.EnableBashCompletion = true
	app.Commands = append([]cli.Command{}, clientSugarCommands...)
	sort.Sort(cli.CommandsByName(app.Commands))

	// Globals recognised by every sugar command. Keeping the set small is
	// the whole point of the split — users who just want `parallax-cli
	// info` shouldn't be faced with the 100+ node flags.
	app.Flags = []cli.Flag{
		utils.DataDirFlag,
		utils.TestnetFlag,
		clientRPCFlag,
	}

	// No default action: if no subcommand is given, print help and exit
	// non-zero so shell scripts notice the mistake.
	app.Action = func(ctx *cli.Context) error {
		_ = cli.ShowAppHelp(ctx)
		return fmt.Errorf("no subcommand specified")
	}
}

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
