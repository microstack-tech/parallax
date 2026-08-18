// Copyright 2026 The Parallax Protocol Authors
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

package banman

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/logging"
)

const testOnionHost = "2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion"

func TestOnionBanLifecycle(t *testing.T) {
	bm, err := New("", logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	if bm.IsBannedHost(testOnionHost) {
		t.Fatal("fresh banman reports the host banned")
	}
	if err := bm.BanHost(testOnionHost, time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	// Case-insensitive match on the canonical key.
	if !bm.IsBannedHost(strings.ToUpper(testOnionHost)) {
		t.Fatal("uppercase form not matched")
	}
	// Onion rows never affect IP containment checks.
	if bm.IsBanned(net.IPv4(127, 0, 0, 1)) || bm.IsBanned(net.IPv4(203, 0, 113, 5)) {
		t.Fatal("onion ban leaked into IP containment")
	}
	// Listbanned includes the row.
	found := false
	for _, e := range bm.ListBanned() {
		if e.Subnet == testOnionHost {
			found = true
		}
	}
	if !found {
		t.Fatal("onion ban missing from ListBanned")
	}
	// Extend-only: a shorter re-ban keeps the longer expiry.
	before := bm.ListBanned()
	if err := bm.BanHost(testOnionHost, time.Minute, ReasonManual); err != nil {
		t.Fatal(err)
	}
	after := bm.ListBanned()
	if after[0].BannedTill < before[0].BannedTill {
		t.Fatal("shorter re-ban cut the existing ban short")
	}

	if ok, err := bm.UnbanHost(testOnionHost); err != nil || !ok {
		t.Fatalf("UnbanHost = %v, %v", ok, err)
	}
	if bm.IsBannedHost(testOnionHost) {
		t.Fatal("host still banned after unban")
	}
	if ok, _ := bm.UnbanHost(testOnionHost); ok {
		t.Fatal("second unban reported ok")
	}

	// Malformed hosts are rejected.
	if err := bm.BanHost("notonion.onion", time.Hour, ReasonManual); err == nil {
		t.Fatal("malformed onion host accepted")
	}
}

func TestOnionBanPersistence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "banlist.json")
	bm, err := New(file, logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := bm.BanHost(testOnionHost, time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	if err := bm.Ban(net.IPv4(203, 0, 113, 9), time.Hour, ReasonManual); err != nil {
		t.Fatal(err)
	}
	if err := bm.Dump(); err != nil {
		t.Fatal(err)
	}

	re, err := New(file, logging.Root())
	if err != nil {
		t.Fatal(err)
	}
	if !re.IsBannedHost(testOnionHost) {
		t.Fatal("onion ban did not survive the round trip")
	}
	if !re.IsBanned(net.IPv4(203, 0, 113, 9)) {
		t.Fatal("IP ban did not survive alongside the onion row")
	}
	if got := len(re.ListBanned()); got != 2 {
		t.Fatalf("reloaded banlist has %d rows, want 2", got)
	}
}
