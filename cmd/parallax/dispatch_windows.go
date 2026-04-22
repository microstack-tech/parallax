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

//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// dispatch spawns the target binary as a child process, wiring the
// wrapper's standard streams to it and propagating the exit code. Windows
// lacks execvp, so the wrapper necessarily remains in the process tree
// for the duration of the child's lifetime. Ctrl-C/signal forwarding is
// best-effort; long-running invocations are better served by calling the
// companion binary (parallaxd or parallax-cli) directly.
func dispatch(name string, args []string) {
	exe, err := findSibling(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parallax: %v\n", err)
		os.Exit(127)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	fmt.Fprintf(os.Stderr, "parallax: run %s: %v\n", exe, err)
	os.Exit(126)
}
