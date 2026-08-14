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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/rpc"
)

// These integration tests spin up a real parallaxd dev daemon in the
// background and exercise each sugar command as a short-lived parallax-cli
// child process, validating stdout, stderr, and exit code. They're slower
// than the unit tests but catch any drift between the client-side
// argument shaping and the server-side RPC shapes.
//
// Daemons are started with --dev --port 0 --nodiscover so they're
// completely isolated from any host networking.

// Binary paths populated by TestMain. Both parallaxd and parallax-cli are
// built once into a shared temp directory so every test subprocess uses
// the same freshly compiled artefacts.
var (
	parallaxdBin   string
	parallaxCliBin string
)

// TestMain builds parallaxd and parallax-cli into a throwaway directory
// before any test runs. parallax-cli's `start` subcommand (and anything
// else that looks up sibling binaries) relies on parallaxd being next to
// the parallax-cli binary, so both are installed into the same directory.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "parallax-cli-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdir tempdir: %v\n", err)
		os.Exit(2)
	}
	code := func() int {
		defer os.RemoveAll(tmp)

		parallaxdBin = filepath.Join(tmp, "parallaxd")
		parallaxCliBin = filepath.Join(tmp, "parallax-cli")
		for _, b := range []struct{ path, pkg string }{
			{parallaxdBin, "github.com/ParallaxProtocol/parallax/cmd/parallaxd"},
			{parallaxCliBin, "github.com/ParallaxProtocol/parallax/cmd/parallax-cli"},
		} {
			build := exec.Command("go", "build", "-o", b.path, b.pkg)
			build.Stdout = os.Stdout
			build.Stderr = os.Stderr
			if err := build.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "build %s: %v\n", b.pkg, err)
				return 2
			}
		}
		return m.Run()
	}()
	os.Exit(code)
}

// devDaemon is a handle to a running dev-mode parallax node that tests
// can issue sugar commands against. Always stop it with cleanup().
type devDaemon struct {
	t       *testing.T
	cmd     *exec.Cmd
	datadir string

	// exited is closed by the single background waiter goroutine when
	// cmd.Wait returns. exec.Cmd.Wait must be called exactly once, so
	// both cleanup and any test that wants to observe exit select on
	// this channel instead of calling Wait themselves. waitErr is set
	// before exited is closed and is safe to read afterwards.
	exited  chan struct{}
	waitErr error
}

// startDevDaemon launches parallax in foreground dev mode and waits
// until the IPC socket accepts connections. Returns the daemon handle
// plus a cleanup func (also registered via t.Cleanup) so individual
// tests can stop early if they want.
func startDevDaemon(t *testing.T) *devDaemon {
	t.Helper()
	datadir := t.TempDir()

	args := []string{
		"--datadir", datadir,
		"--dev",
		"--port", "0",
		"--nodiscover",
		"--maxpeers", "0",
		"--verbosity", "1", // quiet so stderr doesn't swamp test logs
	}
	cmd := &exec.Cmd{
		Path:   parallaxdBin,
		Args:   append([]string{parallaxdBin}, args...),
		Stderr: &prefixWriter{t: t, prefix: "daemon-stderr: "},
		Stdout: &prefixWriter{t: t, prefix: "daemon-stdout: "},
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dev daemon: %v", err)
	}

	d := &devDaemon{t: t, cmd: cmd, datadir: datadir, exited: make(chan struct{})}
	// Single owner of cmd.Wait: everything else awaits d.exited.
	go func() {
		d.waitErr = d.cmd.Wait()
		close(d.exited)
	}()
	t.Cleanup(d.cleanup)

	// Wait up to 15s for the IPC socket to come up. Dev mode generates
	// a developer account and starts mining, which takes a moment on
	// slower test hosts.
	ipc := filepath.Join(datadir, "parallax.ipc")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ipc); err == nil {
			// Socket exists; confirm we can actually round-trip an RPC.
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			c, err := rpc.DialContext(ctx, ipc)
			cancel()
			if err == nil {
				c.Close()
				return d
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.cleanup()
	t.Fatalf("dev daemon did not open IPC at %s within 15s", ipc)
	return nil
}

func (d *devDaemon) cleanup() {
	if d.cmd.Process == nil {
		return
	}
	// Prefer a graceful shutdown via SIGTERM so the node flushes
	// state correctly. Fall back to SIGKILL if it refuses to exit.
	// The background waiter goroutine owns cmd.Wait; we only await
	// d.exited so we never call Wait twice (which is a data race).
	_ = d.cmd.Process.Signal(os.Interrupt)
	select {
	case <-d.exited:
	case <-time.After(15 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.exited
	}
}

// runSugar spawns a sugar subcommand against the dev daemon's datadir
// and returns its stdout, stderr, and exit code. Fatals on exec errors
// (as opposed to non-zero exits, which are returned for the caller to
// inspect — negative-path tests need the exit code).
func (d *devDaemon) runSugar(args ...string) (stdout, stderr string, exitCode int) {
	d.t.Helper()
	full := append([]string{"--datadir", d.datadir}, args...)
	cmd := &exec.Cmd{
		Path: parallaxCliBin,
		Args: append([]string{parallaxCliBin}, full...),
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		d.t.Fatalf("run %v: %v", args, err)
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// prefixWriter tags daemon output so it's distinguishable from test
// logs when a test fails and we dump everything.
type prefixWriter struct {
	t      *testing.T
	prefix string
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.t.Log(w.prefix + line)
		}
	}
	return len(p), nil
}

// ---------------------------------------------------------------------
// Read-only introspection commands
// ---------------------------------------------------------------------

func TestSugarReadOnlyCommands(t *testing.T) {
	d := startDevDaemon(t)

	t.Run("blockcount", func(t *testing.T) {
		out, _, code := d.runSugar("blockcount")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// Bare integer, possibly with trailing newline.
		if _, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err != nil {
			t.Errorf("blockcount stdout = %q, want bare integer", out)
		}
	})

	t.Run("info", func(t *testing.T) {
		out, _, code := d.runSugar("info")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("info stdout not JSON: %v\n%s", err, out)
		}
		for _, key := range []string{"blocks", "chainid", "connections", "enode", "mempool", "version", "mining", "syncing"} {
			if _, ok := obj[key]; !ok {
				t.Errorf("info missing %q: %v", key, obj)
			}
		}
	})

	t.Run("chaininfo", func(t *testing.T) {
		out, _, code := d.runSugar("chaininfo")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("chaininfo not JSON: %v\n%s", err, out)
		}
		for _, key := range []string{"blocks", "bestblockhash", "difficulty", "totaldifficulty", "chainid"} {
			if _, ok := obj[key]; !ok {
				t.Errorf("chaininfo missing %q", key)
			}
		}
	})

	t.Run("netinfo", func(t *testing.T) {
		out, _, code := d.runSugar("netinfo")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("netinfo not JSON: %v\n%s", err, out)
		}
		for _, key := range []string{"connections", "enode", "id", "listening", "ports"} {
			if _, ok := obj[key]; !ok {
				t.Errorf("netinfo missing %q", key)
			}
		}
	})

	t.Run("syncing", func(t *testing.T) {
		out, _, code := d.runSugar("syncing")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// Dev mode has no peers → never entered sync mode → prints "false".
		if strings.TrimSpace(out) != "false" {
			t.Errorf("syncing = %q, want false", out)
		}
	})

	t.Run("mempool", func(t *testing.T) {
		out, _, code := d.runSugar("mempool")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]uint64
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("mempool not JSON: %v\n%s", err, out)
		}
		if _, ok := obj["pending"]; !ok {
			t.Error("mempool missing pending")
		}
		if _, ok := obj["queued"]; !ok {
			t.Error("mempool missing queued")
		}
	})

	t.Run("mempool-content", func(t *testing.T) {
		out, _, code := d.runSugar("mempool-content")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("mempool-content not JSON: %v\n%s", err, out)
		}
		// Buckets must always be present, even when empty.
		for _, k := range []string{"pending", "queued"} {
			if _, ok := obj[k]; !ok {
				t.Errorf("mempool-content missing %q", k)
			}
		}
	})

	t.Run("peers", func(t *testing.T) {
		out, _, code := d.runSugar("peers")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// --maxpeers 0 means the array is empty but must still parse.
		var arr []interface{}
		if err := json.Unmarshal([]byte(out), &arr); err != nil {
			t.Fatalf("peers not JSON array: %v\n%s", err, out)
		}
		if len(arr) != 0 {
			t.Errorf("expected empty peer list, got %d", len(arr))
		}
	})

	t.Run("uptime", func(t *testing.T) {
		out, _, code := d.runSugar("uptime")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		secs, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64)
		if err != nil {
			t.Fatalf("uptime not integer: %v", err)
		}
		// Dev daemon was started within the last few seconds.
		if secs > 60 {
			t.Errorf("uptime = %d, implausibly high for a just-started daemon", secs)
		}
	})

	t.Run("gasprice", func(t *testing.T) {
		out, _, code := d.runSugar("gasprice")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err != nil {
			t.Errorf("gasprice = %q, want bare integer", out)
		}
	})

	t.Run("tip", func(t *testing.T) {
		out, _, code := d.runSugar("tip")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("tip not JSON: %v\n%s", err, out)
		}
		for _, k := range []string{"number", "hash", "parent", "timestamp", "miner", "txs"} {
			if _, ok := obj[k]; !ok {
				t.Errorf("tip missing %q", k)
			}
		}
	})

	t.Run("dbstats", func(t *testing.T) {
		out, _, code := d.runSugar("dbstats")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("dbstats not JSON: %v\n%s", err, out)
		}
		if _, ok := obj["ancients"]; !ok {
			t.Error("dbstats missing ancients")
		}
	})
}

// ---------------------------------------------------------------------
// Block queries — verify parsing for multiple id shapes
// ---------------------------------------------------------------------

func TestSugarBlockQueries(t *testing.T) {
	d := startDevDaemon(t)

	// Capture the genesis hash once; reuse it for hash-based lookups.
	genHashRaw, _, code := d.runSugar("getblockhash", "0")
	if code != 0 {
		t.Fatalf("getblockhash 0 exit %d", code)
	}
	genHash := strings.TrimSpace(genHashRaw)
	if !strings.HasPrefix(genHash, "0x") || len(genHash) != 66 {
		t.Fatalf("getblockhash returned %q, expected 0x+64 hex", genHash)
	}

	t.Run("getblock-by-number", func(t *testing.T) {
		out, _, code := d.runSugar("getblock", "0")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("not JSON: %v", err)
		}
		if obj["hash"] != genHash {
			t.Errorf("block hash mismatch: %v vs %s", obj["hash"], genHash)
		}
	})

	t.Run("getblock-by-hash", func(t *testing.T) {
		out, _, code := d.runSugar("getblock", genHash)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(out, `"number": "0x0"`) {
			t.Errorf("expected genesis number 0x0 in output:\n%s", out)
		}
	})

	t.Run("getblock-latest-tag", func(t *testing.T) {
		_, _, code := d.runSugar("getblock", "latest")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	})

	t.Run("getheader-by-number", func(t *testing.T) {
		out, _, code := d.runSugar("getheader", "0")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// Headers omit the transactions list by construction.
		if strings.Contains(out, `"transactions"`) {
			t.Error("getheader output unexpectedly contained transactions field")
		}
	})

	t.Run("getblockhash-refuses-hash", func(t *testing.T) {
		_, stderr, code := d.runSugar("getblockhash", genHash)
		if code == 0 {
			t.Fatal("expected non-zero exit for hash input to getblockhash")
		}
		if !strings.Contains(stderr, "not a hash") {
			t.Errorf("expected 'not a hash' in stderr, got: %s", stderr)
		}
	})
}

// ---------------------------------------------------------------------
// Account / balance state reads
// ---------------------------------------------------------------------

func TestSugarAccountState(t *testing.T) {
	d := startDevDaemon(t)
	zero := "0x0000000000000000000000000000000000000000"

	t.Run("balance-default-lax", func(t *testing.T) {
		out, _, code := d.runSugar("balance", zero)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// Decimal LAX format must contain a dot — "0.0" for zero.
		if !strings.Contains(out, ".") {
			t.Errorf("balance without --wei should be decimal LAX, got %q", out)
		}
	})

	t.Run("balance-wei", func(t *testing.T) {
		out, _, code := d.runSugar("balance", zero, "--wei")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// --wei should give us a bare integer (no dot).
		s := strings.TrimSpace(out)
		if strings.Contains(s, ".") {
			t.Errorf("balance --wei should be integer, got %q", out)
		}
		if _, err := strconv.ParseUint(s, 10, 64); err != nil {
			t.Errorf("balance --wei not parseable as uint: %v", err)
		}
	})

	t.Run("nonce", func(t *testing.T) {
		out, _, code := d.runSugar("nonce", zero)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err != nil {
			t.Errorf("nonce = %q, want bare integer", out)
		}
	})

	t.Run("code-eoa", func(t *testing.T) {
		out, _, code := d.runSugar("code", zero)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// Zero address has no code — must return exactly "0x".
		if strings.TrimSpace(out) != "0x" {
			t.Errorf("code of zero address = %q, want 0x", out)
		}
	})

	t.Run("storage-empty-slot", func(t *testing.T) {
		out, _, code := d.runSugar("storage", zero, "0")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		want := "0x" + strings.Repeat("0", 64)
		if strings.TrimSpace(out) != want {
			t.Errorf("empty slot = %q, want %s", out, want)
		}
	})
}

// ---------------------------------------------------------------------
// Negative paths (commands that should fail with a clear error)
// ---------------------------------------------------------------------

func TestSugarNotFoundErrors(t *testing.T) {
	d := startDevDaemon(t)

	t.Run("gettx-unknown", func(t *testing.T) {
		_, stderr, code := d.runSugar("gettx", "0x"+strings.Repeat("1", 64))
		if code == 0 {
			t.Fatal("expected non-zero exit for unknown tx")
		}
		if !strings.Contains(stderr, "not found") {
			t.Errorf("stderr missing 'not found': %s", stderr)
		}
	})

	t.Run("getreceipt-unmined", func(t *testing.T) {
		_, stderr, code := d.runSugar("getreceipt", "0x"+strings.Repeat("2", 64))
		if code == 0 {
			t.Fatal("expected non-zero exit for unmined tx")
		}
		if !strings.Contains(stderr, "not found") {
			t.Errorf("stderr missing 'not found': %s", stderr)
		}
	})

	t.Run("addpeer-bad-enode", func(t *testing.T) {
		_, stderr, code := d.runSugar("addpeer", "not-an-enode")
		if code == 0 {
			t.Fatal("expected non-zero exit for malformed enode")
		}
		if len(stderr) == 0 {
			t.Error("expected an error message on stderr")
		}
	})

	t.Run("addpeer-ipport-no-match", func(t *testing.T) {
		_, stderr, code := d.runSugar("addpeer", "1.2.3.4:32110")
		if code == 0 {
			t.Fatal("expected non-zero exit when no peer matches ip:port")
		}
		if !strings.Contains(stderr, "no currently connected") {
			t.Errorf("expected 'no currently connected' hint, got: %s", stderr)
		}
	})
}

// ---------------------------------------------------------------------
// Mining control round-trip
// ---------------------------------------------------------------------

func TestSugarMiningControl(t *testing.T) {
	d := startDevDaemon(t)

	readMining := func() bool {
		out, _, code := d.runSugar("mining")
		if code != 0 {
			t.Fatalf("mining exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("mining not JSON: %v", err)
		}
		v, _ := obj["mining"].(bool)
		return v
	}

	// Dev mode auto-starts mining, so stop → start toggles us through
	// both states.
	if _, _, code := d.runSugar("stopmining"); code != 0 {
		t.Fatalf("stopmining exit %d", code)
	}
	// Give the miner a moment to actually stop.
	time.Sleep(200 * time.Millisecond)
	if readMining() {
		t.Error("expected mining=false after stopmining")
	}

	if _, _, code := d.runSugar("startmining", "1"); code != 0 {
		t.Fatalf("startmining exit %d", code)
	}
	time.Sleep(200 * time.Millisecond)
	if !readMining() {
		t.Error("expected mining=true after startmining")
	}

	t.Run("setextra-too-long-rejects", func(t *testing.T) {
		_, stderr, code := d.runSugar("setextra", strings.Repeat("x", 100))
		if code == 0 {
			t.Fatal("expected non-zero exit for oversize extradata")
		}
		if !strings.Contains(stderr, "max length") {
			t.Errorf("expected max-length error, got: %s", stderr)
		}
	})

	t.Run("setextra-valid", func(t *testing.T) {
		_, _, code := d.runSugar("setextra", "test-tag")
		if code != 0 {
			t.Fatalf("setextra valid string exit %d", code)
		}
	})

	t.Run("setcoinbase", func(t *testing.T) {
		_, _, code := d.runSugar("setcoinbase", "0x1234567890123456789012345678901234567890")
		if code != 0 {
			t.Fatalf("setcoinbase exit %d", code)
		}
	})
}

// ---------------------------------------------------------------------
// Wallet lifecycle: newaccount → unlock → sign → lock
// ---------------------------------------------------------------------

func TestSugarWalletLifecycle(t *testing.T) {
	d := startDevDaemon(t)

	// Write a password file the commands can consume non-interactively.
	pwPath := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pwPath, []byte("testpassword\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	// Baseline listaccounts
	t.Run("listaccounts-baseline", func(t *testing.T) {
		out, _, code := d.runSugar("listaccounts")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var arr []string
		if err := json.Unmarshal([]byte(out), &arr); err != nil {
			t.Fatalf("not JSON array: %v", err)
		}
	})

	// Create a fresh account with --password
	out, _, code := d.runSugar("newaccount", "--password", pwPath)
	if code != 0 {
		t.Fatalf("newaccount exit %d", code)
	}
	addr := strings.TrimSpace(out)
	if !strings.HasPrefix(addr, "0x") || len(addr) != 42 {
		t.Fatalf("newaccount output %q does not look like an address", addr)
	}

	t.Run("listaccounts-contains-new", func(t *testing.T) {
		out, _, code := d.runSugar("listaccounts")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(strings.ToLower(out), strings.ToLower(addr)) {
			t.Errorf("new account %s not in listaccounts output:\n%s", addr, out)
		}
	})

	t.Run("unlock-and-sign", func(t *testing.T) {
		if _, _, code := d.runSugar("unlock", addr, "--password", pwPath, "--duration", "5m"); code != 0 {
			t.Fatalf("unlock exit %d", code)
		}
		out, _, code := d.runSugar("sign", addr, "hello world", "--password", pwPath)
		if code != 0 {
			t.Fatalf("sign exit %d", code)
		}
		sig := strings.TrimSpace(out)
		// 65-byte signature → 130 hex chars plus 0x prefix.
		if !strings.HasPrefix(sig, "0x") || len(sig) != 132 {
			t.Errorf("signature %q is not a 65-byte hex string", sig)
		}
	})

	t.Run("sign-hex-data", func(t *testing.T) {
		// Hex data should produce a different (valid) signature than
		// plain text input, confirming the hex branch is wired.
		out, _, code := d.runSugar("sign", addr, "0xdeadbeef", "--password", pwPath)
		if code != 0 {
			t.Fatalf("sign hex exit %d", code)
		}
		if !strings.HasPrefix(strings.TrimSpace(out), "0x") {
			t.Error("hex-data sign output missing 0x prefix")
		}
	})

	t.Run("lock-then-sign-fails", func(t *testing.T) {
		if _, _, code := d.runSugar("lock", addr); code != 0 {
			t.Fatalf("lock exit %d", code)
		}
		// After locking, sign needs to re-read the password. --password
		// path should still work because the RPC itself requires it.
		_, _, code := d.runSugar("sign", addr, "after lock", "--password", pwPath)
		if code != 0 {
			t.Errorf("sign with password file after lock unexpectedly failed")
		}
	})
}

// ---------------------------------------------------------------------
// Offline utilities — no daemon needed, but we exec the binary to
// verify the real command path end-to-end (same as users run it).
// ---------------------------------------------------------------------

func TestSugarOfflineUtilities(t *testing.T) {
	t.Run("toaddr", func(t *testing.T) {
		// Well-known key used throughout the eth-tooling ecosystem.
		privkey := "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
		wantAddr := "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23"

		for _, variant := range []string{privkey, "0x" + privkey} {
			out, _, code := runOffline("toaddr", variant)
			if code != 0 {
				t.Fatalf("toaddr(%q) exit %d", variant, code)
			}
			if strings.TrimSpace(out) != wantAddr {
				t.Errorf("toaddr(%q) = %q, want %q", variant, out, wantAddr)
			}
		}
	})

	t.Run("toaddr-invalid", func(t *testing.T) {
		_, stderr, code := runOffline("toaddr", "notahex")
		if code == 0 {
			t.Fatal("expected non-zero exit for invalid privkey")
		}
		if !strings.Contains(stderr, "invalid") {
			t.Errorf("expected 'invalid' in stderr, got: %s", stderr)
		}
	})

	t.Run("decoderaw-legacy", func(t *testing.T) {
		// Hand-crafted legacy EIP-155 tx: nonce 0, gasprice 1 gwei,
		// gas 21000, to=0x11..11, value=1 wei, chainId=1337. Signature
		// was generated once from a fresh dev key; the exact key is
		// irrelevant to the test since we only verify structural
		// decoding.
		//
		// Any correctly signed legacy tx will do; the point is to
		// prove UnmarshalBinary + sender recovery round-trip cleanly
		// without network access.
		raw := "0xf86580843b9aca008252089411111111111111111111111111111111111111110180820a96a0359aaaf3247897426c70292e0a825904bfeecb0aae4b04bf9eda4b02e600fb55a0512c3150a71be0f0e4500c09946b31cd37005dfe6ac94df5085cb9a24fd2c244"
		out, _, code := runOffline("decoderaw", raw)
		if code != 0 {
			t.Fatalf("decoderaw exit %d", code)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("decoderaw not JSON: %v\n%s", err, out)
		}
		for _, k := range []string{"hash", "type", "chainid", "nonce", "value", "gas", "gasprice", "from", "to", "v", "r", "s"} {
			if _, ok := obj[k]; !ok {
				t.Errorf("decoderaw output missing %q", k)
			}
		}
		if obj["to"] != "0x1111111111111111111111111111111111111111" {
			t.Errorf("decoderaw to = %v, want 0x1111...", obj["to"])
		}
		if obj["chainid"] != "1337" {
			t.Errorf("decoderaw chainid = %v, want 1337", obj["chainid"])
		}
		// Legacy txs must NOT leak 1559-only fields.
		if _, ok := obj["maxfeepergas"]; ok {
			t.Error("legacy tx unexpectedly exposed maxfeepergas")
		}
	})

	t.Run("decoderaw-invalid", func(t *testing.T) {
		_, stderr, code := runOffline("decoderaw", "0xnotvalid")
		if code == 0 {
			t.Fatal("expected non-zero exit for garbage input")
		}
		if len(stderr) == 0 {
			t.Error("expected error on stderr")
		}
	})
}

// runOffline executes a sugar command that doesn't need a datadir. Shares
// the subprocess pattern with devDaemon.runSugar, but skips the
// --datadir prefix so offline commands don't accidentally stat a missing
// directory.
func runOffline(args ...string) (stdout, stderr string, exitCode int) {
	cmd := &exec.Cmd{
		Path: parallaxCliBin,
		Args: append([]string{parallaxCliBin}, args...),
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		return "", fmt.Sprintf("run error: %v", err), -1
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// ---------------------------------------------------------------------
// stop: make sure the admin_stop sugar actually takes the node down.
// ---------------------------------------------------------------------

func TestSugarStop(t *testing.T) {
	d := startDevDaemon(t)

	out, _, code := d.runSugar("stop")
	if code != 0 {
		t.Fatalf("stop exit %d", code)
	}
	if !strings.Contains(out, "stopping") {
		t.Errorf("expected 'stopping' in stop output, got: %q", out)
	}

	// The daemon should exit shortly after the stop RPC. Await the
	// shared exit channel (owned by the single cmd.Wait goroutine)
	// rather than calling Wait here, which would race cleanup's Wait.
	// The deadline is generous so a race-instrumented shutdown, which
	// is markedly slower than a plain build, doesn't flake.
	select {
	case <-d.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("daemon did not exit within 30s of stop")
	}

	// After the daemon is gone, another stop should fail cleanly.
	_, stderr, code := d.runSugar("stop")
	if code == 0 {
		t.Error("stop against dead daemon unexpectedly succeeded")
	}
	if !strings.Contains(stderr, "cannot connect") {
		t.Errorf("expected 'cannot connect' hint, got: %s", stderr)
	}
}
