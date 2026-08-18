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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ParallaxProtocol/parallax/v2/cmd/utils"
	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/node"
	"github.com/ParallaxProtocol/parallax/v2/wallet/keystore"
	"gopkg.in/urfave/cli.v1"
)

// getPassphrase obtains a passphrase from --passwordfile, or prompts the user.
func getPassphrase(ctx *cli.Context, confirmation bool) string {
	if passphraseFile := ctx.String(passphraseFlag.Name); passphraseFile != "" {
		content, err := os.ReadFile(passphraseFile)
		if err != nil {
			utils.Fatalf("Failed to read password file '%s': %v", passphraseFile, err)
		}
		return strings.TrimRight(string(content), "\r\n")
	}
	return utils.GetPassPhrase("", confirmation)
}

// signHash hashes a message using the EIP-191 personal_sign prefix.
func signHash(data []byte) []byte {
	msg := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(data), data)
	return crypto.Keccak256([]byte(msg))
}

// mustPrintJSON pretty-prints any object as JSON, exiting on error.
func mustPrintJSON(jsonObject any) {
	str, err := json.MarshalIndent(jsonObject, "", "  ")
	if err != nil {
		utils.Fatalf("Failed to marshal JSON object: %v", err)
	}
	fmt.Println(string(str))
}

// resolveKeystoreDir returns the keystore directory to operate on, honouring
// --keystore first, then <datadir>/keystore, falling back to the default
// data directory under $HOME. The directory is created if it doesn't exist
// yet so that `new`/`import` can stream a fresh keyfile into it.
func resolveKeystoreDir(ctx *cli.Context) string {
	if ks := ctx.String(keyStoreDirFlag.Name); ks != "" {
		if err := os.MkdirAll(ks, 0o700); err != nil {
			utils.Fatalf("Could not create keystore directory %s: %v", ks, err)
		}
		abs, err := filepath.Abs(ks)
		if err != nil {
			utils.Fatalf("Could not resolve keystore path: %v", err)
		}
		return abs
	}
	datadir := ctx.String(dataDirFlag.Name)
	if datadir == "" {
		datadir = node.DefaultDataDir()
	}
	if datadir == "" {
		utils.Fatalf("Could not determine data directory; pass --datadir or --keystore")
	}
	ks := filepath.Join(datadir, "keystore")
	if err := os.MkdirAll(ks, 0o700); err != nil {
		utils.Fatalf("Could not create keystore directory %s: %v", ks, err)
	}
	abs, err := filepath.Abs(ks)
	if err != nil {
		utils.Fatalf("Could not resolve keystore path: %v", err)
	}
	return abs
}

// scryptParams returns the KDF parameters to use, tightened unless --lightkdf.
func scryptParams(ctx *cli.Context) (int, int) {
	if ctx.Bool(lightKDFFlag.Name) {
		return keystore.LightScryptN, keystore.LightScryptP
	}
	return keystore.StandardScryptN, keystore.StandardScryptP
}

// openKeystore builds a KeyStore rooted at the resolved directory. It does
// not touch the network or spawn a node — it's a thin wrapper around
// keystore.NewKeyStore with our flag-driven KDF choice.
func openKeystore(ctx *cli.Context) (*keystore.KeyStore, string) {
	dir := resolveKeystoreDir(ctx)
	scryptN, scryptP := scryptParams(ctx)
	return keystore.NewKeyStore(dir, scryptN, scryptP), dir
}

// passwordList returns the passphrases from the subcommand's --password
// file. It mirrors utils.MakePasswordList but reads the flag out of the
// subcommand scope rather than the global one, so we don't depend on
// urfave/cli flag migration.
func passwordList(ctx *cli.Context) []string {
	path := ctx.String(utils.PasswordFileFlag.Name)
	if path == "" {
		return nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		utils.Fatalf("Failed to read password file: %v", err)
	}
	lines := strings.Split(string(text), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}
