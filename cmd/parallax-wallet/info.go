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

	"github.com/ParallaxProtocol/parallax/wallet/keystore"
	"gopkg.in/urfave/cli.v1"
)

type infoOutput struct {
	KeystorePath string   `json:"keystore_path"`
	AccountCount int      `json:"account_count"`
	KDF          string   `json:"kdf"`
	ScryptN      int      `json:"scrypt_n"`
	ScryptP      int      `json:"scrypt_p"`
	Accounts     []string `json:"accounts"`
}

var commandInfo = cli.Command{
	Name:      "info",
	Usage:     "print summary information about the keystore",
	ArgsUsage: " ",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
		jsonFlag,
		lightKDFFlag,
	},
	Description: `
Display keystore metadata: path, number of accounts, KDF parameters that
will be used for new keys, and the list of existing account addresses.

This command is read-only; it does not prompt for any passwords.`,
	Action: func(ctx *cli.Context) error {
		ks, dir := openKeystore(ctx)

		var addrs []string
		for _, w := range ks.Wallets() {
			for _, a := range w.Accounts() {
				addrs = append(addrs, a.Address.Hex())
			}
		}

		scryptN, scryptP := scryptParams(ctx)
		out := infoOutput{
			KeystorePath: dir,
			AccountCount: len(addrs),
			KDF:          "scrypt",
			ScryptN:      scryptN,
			ScryptP:      scryptP,
			Accounts:     addrs,
		}
		if out.Accounts == nil {
			out.Accounts = []string{}
		}

		if ctx.Bool(jsonFlag.Name) {
			mustPrintJSON(out)
			return nil
		}

		kdfLabel := "standard"
		if scryptN == keystore.LightScryptN {
			kdfLabel = "light"
		}
		fmt.Printf("Keystore path:    %s\n", out.KeystorePath)
		fmt.Printf("Accounts:         %d\n", out.AccountCount)
		fmt.Printf("KDF:              scrypt (%s: N=%d, P=%d)\n", kdfLabel, out.ScryptN, out.ScryptP)
		if len(addrs) == 0 {
			fmt.Println("No accounts present.")
			return nil
		}
		fmt.Println("Addresses:")
		for i, a := range addrs {
			fmt.Printf("  #%d %s\n", i, a)
		}
		return nil
	},
}
