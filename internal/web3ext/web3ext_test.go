// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package web3ext

import (
	"fmt"
	"strings"
	"testing"
)

// TestAdminConsoleMethodsRegistered — every admin RPC documented with
// a console invocation must be registered in AdminJs, or the console
// call fails with "no member". admin.uptime()/admin.stop() shipped
// documented-but-unregistered once; this pins the full documented set.
func TestAdminConsoleMethodsRegistered(t *testing.T) {
	documented := []string{
		"addnode", "removenode", "dialV2",
		"setban", "listbanned", "clearbanned",
		"addrbookStatus", "addrbookResetKey",
		"uptime", "stop",
	}
	for _, name := range documented {
		if !strings.Contains(Modules["admin"], fmt.Sprintf("name: '%s'", name)) {
			t.Errorf("admin console method %q is not registered in AdminJs", name)
		}
	}
}
