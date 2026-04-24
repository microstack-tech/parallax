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
	"path/filepath"
	"testing"
)

// TestMessageSignVerify drives the full generate -> signmessage ->
// verifymessage cycle through the compiled binary, exercising stdin
// prompting and the EIP-191 signature path end-to-end.
func TestMessageSignVerify(t *testing.T) {
	tmpdir := t.TempDir()

	keyfile := filepath.Join(tmpdir, "the-keyfile")
	message := "test message"

	generate := runParallaxWallet(t, "generate", "--lightkdf", keyfile)
	generate.Expect(`
!! Unsupported terminal, password will be echoed.
Password: {{.InputLine "foobar"}}
Repeat password: {{.InputLine "foobar"}}
`)
	_, matches := generate.ExpectRegexp(`Address: (0x[0-9a-fA-F]{40})\n`)
	address := matches[1]
	generate.ExpectExit()

	sign := runParallaxWallet(t, "signmessage", keyfile, message)
	sign.Expect(`
!! Unsupported terminal, password will be echoed.
Password: {{.InputLine "foobar"}}
`)
	_, matches = sign.ExpectRegexp(`Signature: ([0-9a-f]+)\n`)
	signature := matches[1]
	sign.ExpectExit()

	verify := runParallaxWallet(t, "verifymessage", address, signature, message)
	_, matches = verify.ExpectRegexp(`
Signature verification successful!
Recovered public key: [0-9a-f]+
Recovered address: (0x[0-9a-fA-F]{40})
`)
	recovered := matches[1]
	verify.ExpectExit()

	if recovered != address {
		t.Error("recovered address doesn't match generated key")
	}
}
