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

package node

import (
	"strings"
	"testing"
)

// startBanTestNode returns a started node and its admin API. The ban
// subsystem runs in-memory (no BanListPath configured).
func startBanTestNode(t *testing.T) *privateAdminAPI {
	t.Helper()
	stack, err := New(testNodeConfig())
	if err != nil {
		t.Fatalf("failed to create protocol stack: %v", err)
	}
	if err := stack.Start(); err != nil {
		t.Fatalf("failed to start stack: %v", err)
	}
	t.Cleanup(func() { stack.Close() })
	return &privateAdminAPI{stack}
}

// TestSetbanAddListRemove — the setban/listbanned RPC handlers
// (not just the parse helper) round-trip a single-IP ban and a
// subnet ban.
func TestSetbanAddListRemove(t *testing.T) {
	api := startBanTestNode(t)

	if ok, err := api.Setban("192.0.2.7", "add", nil, nil); err != nil || !ok {
		t.Fatalf("setban add IP: ok=%v err=%v", ok, err)
	}
	if ok, err := api.Setban("10.1.0.0/16", "add", nil, nil); err != nil || !ok {
		t.Fatalf("setban add subnet: ok=%v err=%v", ok, err)
	}
	list, err := api.Listbanned()
	if err != nil {
		t.Fatalf("listbanned: %v", err)
	}
	got := map[string]bool{}
	for _, e := range list {
		got[e.Subnet] = true
		if e.BannedTill <= e.BanCreated {
			t.Fatalf("entry %s has non-positive duration: %+v", e.Subnet, e)
		}
	}
	if !got["192.0.2.7/32"] || !got["10.1.0.0/16"] {
		t.Fatalf("listbanned = %v, want 192.0.2.7/32 and 10.1.0.0/16", got)
	}

	if ok, err := api.Setban("192.0.2.7", "remove", nil, nil); err != nil || !ok {
		t.Fatalf("setban remove: ok=%v err=%v", ok, err)
	}
	// Removing a never-banned subnet errors (Bitcoin parity).
	if _, err := api.Setban("198.51.100.1", "remove", nil, nil); err == nil {
		t.Fatal("setban remove of unbanned subnet did not error")
	}
	list, _ = api.Listbanned()
	if len(list) != 1 || list[0].Subnet != "10.1.0.0/16" {
		t.Fatalf("after remove, listbanned = %+v, want only 10.1.0.0/16", list)
	}
}

// TestSetbanArgumentValidation — bad commands and bantimes are
// rejected by the handler with errors, not silently accepted.
func TestSetbanArgumentValidation(t *testing.T) {
	api := startBanTestNode(t)

	if _, err := api.Setban("192.0.2.7", "frobnicate", nil, nil); err == nil {
		t.Fatal("invalid command did not error")
	}
	neg := int64(-5)
	if _, err := api.Setban("192.0.2.7", "add", &neg, nil); err == nil {
		t.Fatal("negative relative bantime did not error")
	}
	past := int64(1000)
	abs := true
	if _, err := api.Setban("192.0.2.7", "add", &past, &abs); err == nil {
		t.Fatal("absolute bantime in the past did not error")
	}
	if _, err := api.Setban("not-an-ip", "add", nil, nil); err == nil {
		t.Fatal("malformed subnet did not error")
	}
	if _, err := api.Setban("::ffff:1.2.3.0/64", "add", nil, nil); err == nil {
		t.Fatal("v4-mapped subnet with /33-/95 prefix did not error")
	}
}

// TestSetbanV4MappedCIDR — an IPv4-mapped IPv6 CIDR bans the embedded
// IPv4 subnet (Core's CSubNet semantics), not a huge IPv6 range.
func TestSetbanV4MappedCIDR(t *testing.T) {
	api := startBanTestNode(t)

	if ok, err := api.Setban("::ffff:1.2.3.0/24", "add", nil, nil); err != nil || !ok {
		t.Fatalf("setban add v4-mapped /24: ok=%v err=%v", ok, err)
	}
	if ok, err := api.Setban("::ffff:4.5.6.0/120", "add", nil, nil); err != nil || !ok {
		t.Fatalf("setban add v4-mapped /120: ok=%v err=%v", ok, err)
	}
	list, _ := api.Listbanned()
	var subnets []string
	for _, e := range list {
		subnets = append(subnets, e.Subnet)
	}
	joined := strings.Join(subnets, ",")
	if !strings.Contains(joined, "1.2.3.0/24") || !strings.Contains(joined, "4.5.6.0/24") {
		t.Fatalf("listbanned subnets = %v, want 1.2.3.0/24 and 4.5.6.0/24", subnets)
	}
	if !api.node.Server().BanList.IsBanned([]byte{1, 2, 3, 99}) {
		t.Fatal("host inside v4-mapped /24 ban not banned")
	}
}

// TestClearbanned — clears every entry through the RPC handler.
func TestClearbanned(t *testing.T) {
	api := startBanTestNode(t)

	if _, err := api.Setban("192.0.2.7", "add", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Setban("10.1.0.0/16", "add", nil, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := api.Clearbanned(); err != nil || !ok {
		t.Fatalf("clearbanned: ok=%v err=%v", ok, err)
	}
	if list, _ := api.Listbanned(); len(list) != 0 {
		t.Fatalf("after clearbanned, listbanned = %+v, want empty", list)
	}
}
