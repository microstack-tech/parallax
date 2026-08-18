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

	"github.com/ParallaxProtocol/parallax/v2/cmd/utils"
	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/wallet"
	"github.com/ParallaxProtocol/parallax/v2/wallet/keystore"
	"gopkg.in/urfave/cli.v1"
)

var commandList = cli.Command{
	Name:      "list",
	Usage:     "print a summary of existing accounts",
	ArgsUsage: " ",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
	},
	Description: `
List every account found in the keystore directory, with its address and
on-disk URL. The keystore directory defaults to <datadir>/keystore; use
--keystore to override.`,
	Action: accountList,
}

var commandNew = cli.Command{
	Name:      "new",
	Usage:     "create a new account in the keystore",
	ArgsUsage: " ",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
		utils.PasswordFileFlag,
		lightKDFFlag,
	},
	Description: `
Creates a new account and stores it in the keystore, encrypted with a
password. You will be prompted for the password unless --password points
at a file.

Keep the password safe — without it, the account cannot be unlocked.`,
	Action: accountCreate,
}

var commandUpdate = cli.Command{
	Name:      "update",
	Usage:     "update or re-encrypt an existing account",
	ArgsUsage: "<address>",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
		lightKDFFlag,
	},
	Description: `
Update an existing account. You are prompted first for the current
password, then for a new one. This command is also used to migrate a
keyfile stored in a deprecated format to the current format.`,
	Action: accountUpdate,
}

var commandImport = cli.Command{
	Name:      "import",
	Usage:     "import a raw private key into a new account",
	ArgsUsage: "<keyfile>",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
		utils.PasswordFileFlag,
		lightKDFFlag,
	},
	Description: `
Import an unencrypted private key from <keyfile> (hex-encoded) and
create a new keystore account for it. The keyfile must contain only the
hex-encoded private key.

Note: to transfer an existing encrypted account between nodes, just copy
the keystore file directly — no import is needed.`,
	Action: accountImport,
}

func accountList(ctx *cli.Context) error {
	ks, _ := openKeystore(ctx)
	var index int
	for _, w := range ks.Wallets() {
		for _, account := range w.Accounts() {
			fmt.Printf("Account #%d: {%x} %s\n", index, account.Address, &account.URL)
			index++
		}
	}
	return nil
}

func accountCreate(ctx *cli.Context) error {
	dir := resolveKeystoreDir(ctx)
	scryptN, scryptP := scryptParams(ctx)

	password := utils.GetPassPhraseWithList(
		"Your new account is locked with a password. Please give a password. Do not forget this password.",
		true, 0, passwordList(ctx),
	)

	account, err := keystore.StoreKey(dir, password, scryptN, scryptP)
	if err != nil {
		utils.Fatalf("Failed to create account: %v", err)
	}
	fmt.Printf("\nYour new key was generated\n\n")
	fmt.Printf("Public address of the key:   %s\n", account.Address.Hex())
	fmt.Printf("Path of the secret key file: %s\n\n", account.URL.Path)
	fmt.Printf("- You can share your public address with anyone. Others need it to interact with you.\n")
	fmt.Printf("- You must NEVER share the secret key with anyone! The key controls access to your funds!\n")
	fmt.Printf("- You must BACKUP your key file! Without the key, it's impossible to access account funds!\n")
	fmt.Printf("- You must REMEMBER your password! Without the password, it's impossible to decrypt the key!\n\n")
	return nil
}

func accountUpdate(ctx *cli.Context) error {
	if len(ctx.Args()) == 0 {
		utils.Fatalf("No accounts specified to update")
	}
	ks, _ := openKeystore(ctx)
	for _, addr := range ctx.Args() {
		account, oldPassword := unlockAccount(ks, addr, 0, nil)
		newPassword := utils.GetPassPhraseWithList(
			"Please give a new password. Do not forget this password.",
			true, 0, nil,
		)
		if err := ks.Update(account, oldPassword, newPassword); err != nil {
			utils.Fatalf("Could not update the account: %v", err)
		}
	}
	return nil
}

func accountImport(ctx *cli.Context) error {
	keyfile := ctx.Args().First()
	if len(keyfile) == 0 {
		utils.Fatalf("keyfile must be given as argument")
	}
	key, err := crypto.LoadECDSA(keyfile)
	if err != nil {
		utils.Fatalf("Failed to load the private key: %v", err)
	}
	ks, _ := openKeystore(ctx)
	passphrase := utils.GetPassPhraseWithList(
		"Your new account is locked with a password. Please give a password. Do not forget this password.",
		true, 0, passwordList(ctx),
	)
	acct, err := ks.ImportECDSA(key, passphrase)
	if err != nil {
		utils.Fatalf("Could not create the account: %v", err)
	}
	fmt.Printf("Address: {%x}\n", acct.Address)
	return nil
}

// unlockAccount prompts up to three times to unlock the given account.
// This mirrors the historical parallax account update behaviour so
// scripted passphrase files keep working unchanged.
func unlockAccount(ks *keystore.KeyStore, address string, i int, passwords []string) (wallet.Account, string) {
	account, err := utils.MakeAddress(ks, address)
	if err != nil {
		utils.Fatalf("Could not list accounts: %v", err)
	}
	for trials := 0; trials < 3; trials++ {
		prompt := fmt.Sprintf("Unlocking account %s | Attempt %d/%d", address, trials+1, 3)
		password := utils.GetPassPhraseWithList(prompt, false, i, passwords)
		err = ks.Unlock(account, password)
		if err == nil {
			logging.Info("Unlocked account", "address", account.Address.Hex())
			return account, password
		}
		if err, ok := err.(*keystore.AmbiguousAddrError); ok {
			logging.Info("Unlocked account", "address", account.Address.Hex())
			return ambiguousAddrRecovery(ks, err, password), password
		}
		if err != keystore.ErrDecrypt {
			break
		}
	}
	utils.Fatalf("Failed to unlock account %s (%v)", address, err)
	return wallet.Account{}, ""
}

func ambiguousAddrRecovery(ks *keystore.KeyStore, err *keystore.AmbiguousAddrError, auth string) wallet.Account {
	fmt.Printf("Multiple key files exist for address %x:\n", err.Addr)
	for _, a := range err.Matches {
		fmt.Println("  ", a.URL)
	}
	fmt.Println("Testing your password against all of them...")
	var match *wallet.Account
	for _, a := range err.Matches {
		if err := ks.Unlock(a, auth); err == nil {
			match = &a
			break
		}
	}
	if match == nil {
		utils.Fatalf("None of the listed files could be unlocked.")
	}
	fmt.Printf("Your password unlocked %s\n", match.URL)
	fmt.Println("In order to avoid this warning, you need to remove the following duplicate key files:")
	for _, a := range err.Matches {
		if a != *match {
			fmt.Println("  ", a.URL)
		}
	}
	return *match
}
