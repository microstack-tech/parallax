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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/ParallaxProtocol/parallax/cmd/utils"
	"gopkg.in/urfave/cli.v1"
)

// daemonEnvSentinel is set in the environment of the child process after
// daemonization so it can distinguish itself from the parent and skip the
// re-exec step.
const daemonEnvSentinel = "PARALLAX_DAEMONIZED"

var (
	DaemonFlag = cli.BoolFlag{
		Name:  "daemon",
		Usage: "Detach and run the node as a background daemon (logs redirected to <datadir>/parallax.log)",
	}
	PIDFileFlag = cli.StringFlag{
		Name:  "pid",
		Usage: "Path to the PID file when running with --daemon (default: <datadir>/parallax.pid)",
	}
)

// daemonFlags is the group of flags related to daemonization.
var daemonFlags = []cli.Flag{
	DaemonFlag,
	PIDFileFlag,
}

// resolveDataDir returns the effective data directory honoring --datadir and
// the --testnet subdirectory convention used throughout the codebase.
func resolveDataDir(ctx *cli.Context) string {
	path := ctx.GlobalString(utils.DataDirFlag.Name)
	if path == "" {
		return ""
	}
	if ctx.GlobalBool(utils.TestnetFlag.Name) {
		path = filepath.Join(path, "testnet")
	}
	return path
}

// resolvePIDPath returns the PID file path, honoring --pid if set, else
// <datadir>/parallax.pid. Returns an error if no path can be determined.
func resolvePIDPath(ctx *cli.Context) (string, error) {
	if p := ctx.GlobalString(PIDFileFlag.Name); p != "" {
		return p, nil
	}
	datadir := resolveDataDir(ctx)
	if datadir == "" {
		return "", errors.New("cannot determine PID file location: set --pid or --datadir")
	}
	return filepath.Join(datadir, "parallax.pid"), nil
}

// resolveLogPath returns the log file path for daemon-mode stdout/stderr
// redirection: <datadir>/parallax.log.
func resolveLogPath(ctx *cli.Context) (string, error) {
	datadir := resolveDataDir(ctx)
	if datadir == "" {
		return "", errors.New("cannot determine log file location: set --datadir")
	}
	return filepath.Join(datadir, "parallax.log"), nil
}

// isDaemonChild reports whether the current process is the forked daemon
// child (i.e., it has the sentinel env var set by its parent).
func isDaemonChild() bool {
	return os.Getenv(daemonEnvSentinel) == "1"
}

// pidAlive reports whether the given PID corresponds to a running process.
// On Unix, signal 0 is used to test existence without actually signalling.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// On Windows, FindProcess succeeds for any PID; there's no cheap
		// aliveness check without opening the process. Treat as alive and
		// let downstream errors surface if the PID is stale.
		return true
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// readPIDFile returns the PID recorded in the given file, or (0, nil) if
// the file does not exist. Malformed files produce an error.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(string(trim(data)))
	if err != nil {
		return 0, fmt.Errorf("malformed PID file %s: %v", path, err)
	}
	return pid, nil
}

// writePIDFile writes the given PID to the given path with 0644 perms,
// creating parent directories as needed.
func writePIDFile(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

func trim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}

// daemonize forks the current process into the background. It must be called
// from the parent process (isDaemonChild() == false). On success it does not
// return: the parent exits with status 0 once the child has opened its IPC
// socket (readiness signal), or with a non-zero status on failure.
//
// The daemonization pattern is portable: re-exec self with the same argv,
// setting a sentinel env var so the child knows to skip this function. The
// child's stdout/stderr are redirected to <datadir>/parallax.log; on Unix
// it is detached into its own session with Setsid.
func daemonize(ctx *cli.Context) error {
	// Resolve log + PID paths before forking so we can surface errors to the
	// user's terminal while we still have one.
	logPath, err := resolveLogPath(ctx)
	if err != nil {
		return err
	}
	pidPath, err := resolvePIDPath(ctx)
	if err != nil {
		return err
	}

	// Bail out early if an existing daemon is running. Stale PID files are
	// silently overwritten so a crashed node doesn't block restarts.
	if existingPID, err := readPIDFile(pidPath); err != nil {
		return err
	} else if existingPID > 0 && pidAlive(existingPID) {
		return fmt.Errorf("parallax daemon already running (pid %d, pidfile %s)", existingPID, pidPath)
	}

	// Ensure the datadir exists so the child can open the log file.
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create datadir: %v", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %v", logPath, err)
	}
	defer logFile.Close()

	// Build the child command: same argv, sentinel env, log file for
	// stdout/stderr, /dev/null for stdin, detached session on Unix.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %v", err)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), daemonEnvSentinel+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon child: %v", err)
	}

	// Print a message on the parent's terminal before it exits.
	fmt.Fprintf(os.Stdout, "parallax daemon started (pid %d, logs: %s)\n", cmd.Process.Pid, logPath)

	// Release the child so it is not reaped as a zombie if the parent
	// lingers. On Linux the session detachment via Setsid also decouples
	// signal handling from the parent's controlling terminal.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release daemon child: %v", err)
	}

	// Parent exits. The child is responsible for writing its own PID file
	// (see writeDaemonPIDFile) once it is ready.
	os.Exit(0)
	return nil
}

// writeDaemonPIDFile is called from the child process after the node has
// started so that the PID file reflects a running daemon. It returns a
// cleanup function that removes the PID file; callers should defer it.
func writeDaemonPIDFile(ctx *cli.Context) (func(), error) {
	pidPath, err := resolvePIDPath(ctx)
	if err != nil {
		return func() {}, err
	}
	if err := writePIDFile(pidPath, os.Getpid()); err != nil {
		return func() {}, fmt.Errorf("write PID file %s: %v", pidPath, err)
	}
	return func() {
		// Best effort: only remove if we still own it.
		if pid, err := readPIDFile(pidPath); err == nil && pid == os.Getpid() {
			_ = os.Remove(pidPath)
		}
	}, nil
}
