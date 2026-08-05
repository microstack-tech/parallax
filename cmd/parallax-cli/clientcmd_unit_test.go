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
	"math/big"
	"strings"
	"testing"
)

// These tests cover the pure, side-effect-free helpers that back the sugar
// subcommands. They do not dial RPC, do not touch the filesystem, and do
// not spawn subprocesses — they're the fast inner loop for catching
// regressions in hex/number/JSON plumbing.

func TestHexDecodeRoundtrip(t *testing.T) {
	cases := []string{"", "00", "ff", "deadbeef", "0123456789abcdef"}
	for _, hex := range cases {
		b, err := hexDecode(hex)
		if err != nil {
			t.Fatalf("hexDecode(%q): %v", hex, err)
		}
		if got := hexEncode(b); got != strings.ToLower(hex) {
			t.Fatalf("roundtrip %q → %q", hex, got)
		}
	}
}

func TestHexDecodeOddLength(t *testing.T) {
	// Odd-length inputs are left-padded with a zero nibble so users can
	// casually type "0x1" instead of forcing "0x01".
	b, err := hexDecode("f")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b) != 1 || b[0] != 0x0f {
		t.Fatalf("got %x, want 0f", b)
	}
}

func TestHexDecodeInvalid(t *testing.T) {
	if _, err := hexDecode("nothex"); err == nil {
		t.Fatal("expected error on non-hex input")
	}
}

func TestHexToBigInt(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0x0", "0"},
		{"0x", "0"},
		{"", "0"},
		{"0xff", "255"},
		{"0x10000000000000000", "18446744073709551616"}, // > uint64
		{"ff", "255"}, // missing 0x prefix
		{"nothex", "0"},
	}
	for _, c := range cases {
		if got := hexToBigInt(c.in).String(); got != c.want {
			t.Errorf("hexToBigInt(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestHexToUint(t *testing.T) {
	if got := hexToUint("0x2a"); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	if got := hexToUint(""); got != 0 {
		t.Errorf("empty input should yield 0, got %d", got)
	}
}

func TestWeiToLAX(t *testing.T) {
	cases := []struct {
		wei  string
		want string
	}{
		{"0", "0.0"},                          // zero preserves a fractional digit
		{"1", "0.000000000000000001"},         // min wei
		{"1000000000000000000", "1.0"},        // 1 LAX
		{"1234567890000000000", "1.23456789"}, // trailing zeros trimmed
		{"100000000000000000", "0.1"},         // 0.1 LAX
		{"1000000000000000001", "1.000000000000000001"},
		{"-500000000000000000", "-0.5"}, // negative preserved
	}
	for _, c := range cases {
		wei, _ := new(big.Int).SetString(c.wei, 10)
		if got := weiToLAX(wei); got != c.want {
			t.Errorf("weiToLAX(%s) = %q, want %q", c.wei, got, c.want)
		}
	}
}

func TestWeiToLAXNil(t *testing.T) {
	// Defensive: nil input must not panic — command output paths pass
	// whatever hexToBigInt returned, which can be a fresh zero-valued
	// big.Int but also, in some branches, nil.
	if got := weiToLAX(nil); got != "0.0" {
		t.Errorf("nil input = %q, want 0.0", got)
	}
}

func TestToHexAmount(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0x0"},
		{"42", "0x2a"},
		{"0xff", "0xff"}, // hex passthrough
		{"0XFF", "0XFF"}, // upper-case prefix passthrough
		{"1000000000000000000", "0xde0b6b3a7640000"},
		{"garbage", "0x0"}, // unparseable → 0x0; RPC surfaces any real error
	}
	for _, c := range cases {
		if got := toHexAmount(c.in); got != c.want {
			t.Errorf("toHexAmount(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveBlockID(t *testing.T) {
	cases := []struct {
		in         string
		wantByHash bool
		wantParam  string
		wantErr    bool
	}{
		// 32-byte hex hash
		{"0x" + strings.Repeat("a", 64), true, "0x" + strings.Repeat("a", 64), false},
		// Named tags pass through
		{"latest", false, "latest", false},
		{"earliest", false, "earliest", false},
		{"pending", false, "pending", false},
		{"safe", false, "safe", false},
		{"finalized", false, "finalized", false},
		// Decimal numbers convert to 0x-hex
		{"0", false, "0x0", false},
		{"42", false, "0x2a", false},
		{"1234567", false, "0x12d687", false},
		// Garbage fails
		{"notanumber", false, "", true},
		{"0xabc", false, "", true}, // hex-looking but not 66 chars and not decimal
	}
	for _, c := range cases {
		byHash, param, err := resolveBlockID(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolveBlockID(%q): expected error, got param=%q", c.in, param)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveBlockID(%q): unexpected error: %v", c.in, err)
			continue
		}
		if byHash != c.wantByHash || param != c.wantParam {
			t.Errorf("resolveBlockID(%q) = (%v, %q), want (%v, %q)",
				c.in, byHash, param, c.wantByHash, c.wantParam)
		}
	}
}

func TestFilterByAddress(t *testing.T) {
	pool := map[string]interface{}{
		"pending": map[string]interface{}{
			"0xABCD": map[string]interface{}{"42": "tx-from-abcd"},
			"0x1234": map[string]interface{}{"7": "tx-from-1234"},
		},
		"queued": map[string]interface{}{
			"0xabcd": map[string]interface{}{"99": "queued-from-abcd"},
		},
	}

	// Lower-case query should match the upper-case pending entry
	// (case-insensitive) AND the lower-case queued entry.
	got := filterByAddress(pool, "0xabcd")
	pending := got["pending"].(map[string]interface{})
	queued := got["queued"].(map[string]interface{})
	if _, ok := pending["0xABCD"]; !ok {
		t.Error("expected 0xABCD in pending after lowercase query")
	}
	if _, ok := pending["0x1234"]; ok {
		t.Error("0x1234 should have been filtered out")
	}
	if _, ok := queued["0xabcd"]; !ok {
		t.Error("expected 0xabcd in queued")
	}
}

func TestIsConnectionClosed(t *testing.T) {
	// These are the specific geth/RPC-transport error shapes that
	// stop's success-detection path treats as a successful shutdown.
	nonClosed := []string{"timeout", "foo", "bar"}
	closed := []string{
		"unexpected EOF",
		"read tcp: connection reset by peer",
		"write: broken pipe",
		"use of closed network connection",
	}
	for _, msg := range nonClosed {
		if isConnectionClosed(fakeErr(msg)) {
			t.Errorf("%q should not be classified as closed", msg)
		}
	}
	for _, msg := range closed {
		if !isConnectionClosed(fakeErr(msg)) {
			t.Errorf("%q should be classified as closed", msg)
		}
	}
}

// fakeErr is a minimal error type for isConnectionClosed's string-matching
// path. Using errors.New is equivalent; spelled out for test clarity.
type fakeErr string

func (e fakeErr) Error() string { return string(e) }

func TestClientSugarCommandsRegistered(t *testing.T) {
	// Sanity check: every sugar command must have a Name, an Action, and
	// the "CLIENT COMMANDS" category so it renders under the right group
	// in `parallax --help`. Catches accidental copy-paste errors quickly.
	for _, c := range clientSugarCommands {
		if c.Name == "" {
			t.Error("command with empty Name")
		}
		if c.Action == nil {
			t.Errorf("command %s has nil Action", c.Name)
		}
		if c.Category != "CLIENT COMMANDS" {
			t.Errorf("command %s category = %q, want CLIENT COMMANDS", c.Name, c.Category)
		}
	}
}

// TestBanCommandsRegistered — sanity-check that the three ban
// commands are wired into the sugar list with the right shape.
func TestBanCommandsRegistered(t *testing.T) {
	want := map[string]bool{
		"setban":      false,
		"listbanned":  false,
		"clearbanned": false,
	}
	for _, c := range clientSugarCommands {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("ban command %q not registered in clientSugarCommands", name)
		}
	}
}
