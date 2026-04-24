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

// parallax is a multi-call wrapper that dispatches to parallaxd (the node
// daemon), parallax-cli (the JSON-RPC client) or parallax-wallet (the
// offline wallet tool), mirroring the `bitcoin` wrapper binary in
// Bitcoin Core. Users can either invoke the companion binaries directly
// or go through the wrapper:
//
//	parallax node   --datadir /var/lib/parallax --daemon   (equivalent to parallaxd …)
//	parallax rpc    info                                   (equivalent to parallax-cli info)
//	parallax wallet list                                   (equivalent to parallax-wallet list)
//
// The wrapper carries no domain logic of its own. It just resolves the
// right companion binary — preferring one installed next to itself and
// falling back to $PATH — and hands control over.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const usage = `parallax: multi-call wrapper for the Parallax client suite.

Usage:
    parallax node   [args...]   run the parallaxd full-node daemon
    parallax rpc    [args...]   send an RPC command via parallax-cli
    parallax wallet [args...]   run an offline wallet command via parallax-wallet
    parallax help               show this help
    parallax version            print version information

Each subcommand forwards the remaining arguments to the companion binary
unchanged. See 'parallax node --help', 'parallax rpc --help' and
'parallax wallet --help' for the full set of flags and commands.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "node":
		dispatch("parallaxd", os.Args[2:])
	case "rpc":
		dispatch("parallax-cli", os.Args[2:])
	case "wallet":
		dispatch("parallax-wallet", os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "-v", "--version", "version":
		dispatch("parallaxd", []string{"version"})
	default:
		fmt.Fprintf(os.Stderr, "parallax: unknown subcommand %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

// findSibling resolves a companion binary, preferring one installed next
// to the wrapper and falling back to $PATH. Symlinks are resolved so that
// `go run` builds (which live under a per-build tempdir) and
// symlink-based installs both end up looking in the right directory.
func findSibling(name string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	candidate := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("cannot locate %s (tried %s and $PATH)", name, candidate)
}
