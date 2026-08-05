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
	// bantime=0 with absolute resolves to the epoch: an error (Core:
	// "Absolute timestamp is in the past"), never the 24h default.
	zero := int64(0)
	if _, err := api.Setban("192.0.2.7", "add", &zero, &abs); err == nil {
		t.Fatal("absolute bantime of zero did not error")
	}
	if _, err := api.Setban("192.0.2.7", "add", nil, &abs); err == nil {
		t.Fatal("absolute with omitted bantime did not error")
	}
	if _, err := api.Setban("not-an-ip", "add", nil, nil); err == nil {
		t.Fatal("malformed subnet did not error")
	}
	if _, err := api.Setban("::ffff:1.2.3.0/64", "add", nil, nil); err == nil {
		t.Fatal("v4-mapped subnet with /33-/95 prefix did not error")
	}
}

// TestSetbanHugeRelativeBantime — the "permanent ban" idiom of a huge
// relative bantime (Bitcoin operators commonly pass 9999999999) must
// keep its intent. The naive seconds-to-Duration multiplication
// overflows int64 nanoseconds for anything above ~292 years, going
// negative — which BanSubnet would silently replace with the 24h
// default while the RPC reports success.
func TestSetbanHugeRelativeBantime(t *testing.T) {
	api := startBanTestNode(t)

	huge := int64(9999999999)
	if ok, err := api.Setban("192.0.2.7", "add", &huge, nil); err != nil || !ok {
		t.Fatalf("setban add with huge bantime: ok=%v err=%v", ok, err)
	}
	list, err := api.Listbanned()
	if err != nil {
		t.Fatalf("listbanned: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("listbanned = %d entries, want 1", len(list))
	}
	// Anything at least a century out preserves the operator's
	// "permanent" intent; the 24h-default failure mode is orders of
	// magnitude short of this.
	const century = int64(100 * 365 * 24 * 60 * 60)
	if got := list[0].BannedTill - list[0].BanCreated; got < century {
		t.Fatalf("huge bantime collapsed: ban lasts %d seconds, want >= %d", got, century)
	}
}

// TestSetbanV4MappedCIDR — an IPv4-mapped IPv6 CIDR bans the embedded
// IPv4 subnet (Core's CSubNet semantics), not a huge IPv6 range.
func TestSetbanV4MappedCIDR(t *testing.T) {
	api := startBanTestNode(t)

	if ok, err := api.Setban("::ffff:1.2.3.0/24", "add", nil, nil); err != nil || !ok {
		t.Fatalf("setban add v4-mapped /24: ok=%v err=%v", ok, err)
	}
	// Core rejects any prefix above /32 for a v4-mapped target,
	// including the IPv6-style /96../128 forms.
	if _, err := api.Setban("::ffff:4.5.6.0/120", "add", nil, nil); err == nil {
		t.Fatal("v4-mapped /120 prefix did not error")
	}
	list, _ := api.Listbanned()
	var subnets []string
	for _, e := range list {
		subnets = append(subnets, e.Subnet)
	}
	joined := strings.Join(subnets, ",")
	if !strings.Contains(joined, "1.2.3.0/24") {
		t.Fatalf("listbanned subnets = %v, want 1.2.3.0/24", subnets)
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

// TestSetbanAddAlreadyBanned — re-adding an actively banned subnet is
// an error (Bitcoin parity: RPC_CLIENT_NODE_ALREADY_ADDED); the
// operator must remove the ban first. Without the error, a
// "shortening" re-ban would report success while the extend-only rule
// silently kept the longer expiry. A wider covering ban does not
// block an exact-subnet add (Core's check is an exact banmap lookup),
// and remove-then-add works.
func TestSetbanAddAlreadyBanned(t *testing.T) {
	api := startBanTestNode(t)

	if ok, err := api.Setban("192.0.2.9", "add", nil, nil); err != nil || !ok {
		t.Fatalf("setban add: ok=%v err=%v", ok, err)
	}
	short := int64(3600)
	if _, err := api.Setban("192.0.2.9", "add", &short, nil); err == nil {
		t.Fatal("re-adding an active ban did not error")
	} else if !strings.Contains(err.Error(), "already banned") {
		t.Fatalf("unexpected error for re-add: %v", err)
	}
	// A covering wider ban is a different banmap entry: exact-subnet
	// add still succeeds.
	if ok, err := api.Setban("192.0.2.0/24", "add", nil, nil); err != nil || !ok {
		t.Fatalf("adding covering subnet: ok=%v err=%v", ok, err)
	}
	// Remove-then-add is the sanctioned way to change a ban's expiry.
	if ok, err := api.Setban("192.0.2.9", "remove", nil, nil); err != nil || !ok {
		t.Fatalf("setban remove: ok=%v err=%v", ok, err)
	}
	if ok, err := api.Setban("192.0.2.9", "add", &short, nil); err != nil || !ok {
		t.Fatalf("re-add after remove: ok=%v err=%v", ok, err)
	}
}
