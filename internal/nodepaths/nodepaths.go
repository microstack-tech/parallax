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

// Package nodepaths centralises the filesystem conventions shared by the
// parallaxd daemon and the parallax-cli client, so the two binaries cannot
// drift on where the datadir, IPC socket, PID file or log file live.
package nodepaths

import (
	"errors"
	"path/filepath"

	"gopkg.in/urfave/cli.v1"
)

// DaemonEnvSentinel is set on the daemon child's environment after
// re-exec so the child can distinguish itself from the parent and skip
// the forking step.
const DaemonEnvSentinel = "PARALLAX_DAEMONIZED"

// Flag names recognised by both binaries. Duplicated as string constants
// (rather than imported from cmd/utils) so this package stays dependency-free.
const (
	dataDirFlagName = "datadir"
	testnetFlagName = "testnet"
	pidFlagName     = "pid"
)

// File names under the datadir.
const (
	IPCFileName = "parallax.ipc"
	PIDFileName = "parallax.pid"
	LogFileName = "parallax.log"
)

// DataDir returns the effective data directory for the current invocation,
// honouring --datadir and the --testnet subdirectory convention. Returns
// the empty string when --datadir is unset and no sensible default exists;
// callers must decide what to do with that (commands that mutate on-disk
// state fail loudly, read-only commands may substitute ".").
func DataDir(ctx *cli.Context) string {
	path := ctx.GlobalString(dataDirFlagName)
	if path == "" {
		path = ctx.String(dataDirFlagName)
	}
	if path == "" {
		return ""
	}
	if ctx.GlobalBool(testnetFlagName) || ctx.Bool(testnetFlagName) {
		path = filepath.Join(path, "testnet")
	}
	return path
}

// IPCPath returns the default IPC socket path for the current invocation:
// <datadir>/parallax.ipc. Returns the empty string when no datadir can be
// resolved — typically the client layer then treats it as "no endpoint
// available" and errors with a helpful message.
func IPCPath(ctx *cli.Context) string {
	dir := DataDir(ctx)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, IPCFileName)
}

// PIDPath returns the PID file path, honouring --pid when set, else
// <datadir>/parallax.pid. Returns an error if neither can be determined.
func PIDPath(ctx *cli.Context) (string, error) {
	if p := ctx.GlobalString(pidFlagName); p != "" {
		return p, nil
	}
	if p := ctx.String(pidFlagName); p != "" {
		return p, nil
	}
	dir := DataDir(ctx)
	if dir == "" {
		return "", errors.New("cannot determine PID file location: set --pid or --datadir")
	}
	return filepath.Join(dir, PIDFileName), nil
}

// LogPath returns the log file path for daemon-mode stdout/stderr
// redirection: <datadir>/parallax.log.
func LogPath(ctx *cli.Context) (string, error) {
	dir := DataDir(ctx)
	if dir == "" {
		return "", errors.New("cannot determine log file location: set --datadir")
	}
	return filepath.Join(dir, LogFileName), nil
}
