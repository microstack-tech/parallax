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
	"os"
	"testing"

	"github.com/ParallaxProtocol/parallax/internal/cmdtest"
	"github.com/moby/sys/reexec"
)

type testParallaxWallet struct {
	*cmdtest.TestCmd
}

// runParallaxWallet re-execs the test binary as if it were the
// parallax-wallet command, forwarding args through cmdtest.
func runParallaxWallet(t *testing.T, args ...string) *testParallaxWallet {
	tt := new(testParallaxWallet)
	tt.TestCmd = cmdtest.NewTestCmd(t, tt)
	tt.Run("parallax-wallet-test", args...)
	return tt
}

func TestMain(m *testing.M) {
	reexec.Register("parallax-wallet-test", func() {
		if err := app.Run(os.Args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	})
	if reexec.Init() {
		return
	}
	os.Exit(m.Run())
}
