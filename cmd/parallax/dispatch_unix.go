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

//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// dispatch replaces the wrapper process with the target binary using
// syscall.Exec, matching Bitcoin Core's execvp-based dispatcher. After a
// successful exec the wrapper is no longer in the process tree, so signal
// handling, terminal attachment and exit codes all behave as if the user
// had invoked the companion binary directly.
func dispatch(name string, args []string) {
	exe, err := findSibling(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parallax: %v\n", err)
		os.Exit(127)
	}
	full := append([]string{exe}, args...)
	if err := syscall.Exec(exe, full, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "parallax: exec %s: %v\n", exe, err)
		os.Exit(126)
	}
}
