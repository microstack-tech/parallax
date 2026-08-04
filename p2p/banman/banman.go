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

// Package banman implements the persistent ban list and ephemeral
// discourage filter that the inbound-accept path consults to reject
// misbehaving peers.
//
// Two storage tiers, mirroring Bitcoin Core's BanMan
// (src/banman.h:63):
//
//   - Banned: persistent JSON map (banlist.json), operator-controlled
//     via setban / unban / clearbanned RPC. Default duration 24h
//     (DEFAULT_MISBEHAVING_BANTIME, src/banman.h:19). Subnet-aware:
//     "1.2.3.0/24" bans the whole /24.
//   - Discouraged: in-memory rolling Bloom filter, 50k capacity /
//     1e-6 false-positive rate (Bitcoin Core defaults, src/banman.h).
//     Populated automatically from peer Misbehaving() calls. Oldest
//     entries rotate out as new ones arrive; no persistence.
//
// Modern Bitcoin Core (v28+) dropped the "ban score" point system;
// this package follows that model. Misbehaving() is an immediate
// discourage trigger with no point accumulation, no
// DISCOURAGEMENT_THRESHOLD.
package banman

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
)

// DefaultBanDuration is the default time a manually-banned subnet
// stays banned. Bitcoin Core's DEFAULT_MISBEHAVING_BANTIME = 86400s
// (src/banman.h:19).
const DefaultBanDuration = 24 * time.Hour

// Reasons recorded in banlist.json. Free-form strings; these
// constants are the canonical values used by the RPC layer.
const (
	ReasonManual          = "manual"
	ReasonNodeMisbehavior = "node-misbehavior"
)

// banEntry is one row of banlist.json. Times stored as Unix
// seconds for forward compatibility with Bitcoin Core's
// BanlistAddrFromJSON (src/addrdb.cpp).
type banEntry struct {
	Subnet     string `json:"address"`
	BanCreated int64  `json:"ban_created"`
	BannedTill int64  `json:"banned_until"`
	Reason     string `json:"reason,omitempty"`
}

// banlistFile is the on-disk shape. The single "banned_nets" key
// matches Bitcoin Core's modern format (src/addrdb.h:34).
type banlistFile struct {
	BannedNets []banEntry `json:"banned_nets"`
}

// BanMan is the persistent-ban + ephemeral-discourage tracker.
//
// Concurrency: every public method takes the internal mutex so
// callers can use BanMan from multiple goroutines (RPC handlers,
// peer-misbehavior callbacks, accept-loop check) without external
// synchronization.
type BanMan struct {
	mu          sync.Mutex
	banned      map[string]bannedNet // canonical subnet string → entry
	discouraged *rollingBloomFilter
	file        string     // banlist.json path; empty disables persistence
	dumpMu      sync.Mutex // serializes Dump's snapshot+write+rename
	log         logging.Logger
}

// bannedNet pairs a banlist row with its parsed subnet so the
// accept/dial hot path (IsBanned) never re-parses CIDR strings.
type bannedNet struct {
	entry banEntry
	ipnet *net.IPNet
}

// New constructs an empty BanMan. If file is non-empty, the path is
// loaded immediately; a missing file is not an error (fresh-install
// path). Caller is responsible for invoking Dump on shutdown.
func New(file string, log logging.Logger) (*BanMan, error) {
	if log == nil {
		log = logging.Root()
	}
	bm := &BanMan{
		banned:      make(map[string]bannedNet),
		discouraged: newRollingBloomFilter(discourageBloomCap, discourageBloomFP),
		file:        file,
		log:         log,
	}
	if file == "" {
		return bm, nil
	}
	if err := bm.Load(); err != nil {
		return nil, err
	}
	return bm, nil
}

// Ban adds or extends a single-IP ban (CIDR /32 for IPv4, /128 for
// IPv6). duration <= 0 falls back to DefaultBanDuration. reason is
// stored verbatim in the JSON file; pass ReasonManual or
// ReasonNodeMisbehavior for canonical values.
func (b *BanMan) Ban(addr net.IP, duration time.Duration, reason string) error {
	subnet, err := singleIPSubnet(addr)
	if err != nil {
		return err
	}
	return b.BanSubnet(subnet, duration, reason)
}

// BanSubnet adds or extends a subnet ban. If duration <= 0 the
// default is used. Re-banning an already-banned subnet only ever
// extends the ban: a request whose expiry would land before the
// existing one leaves the entry untouched (Bitcoin Core
// BanMan::Ban only replaces when the new ban lasts longer), so an
// automatic short ban can't cut a long operator ban short.
func (b *BanMan) BanSubnet(subnet *net.IPNet, duration time.Duration, reason string) error {
	if subnet == nil {
		return errors.New("banman: nil subnet")
	}
	if duration <= 0 {
		duration = DefaultBanDuration
	}
	now := time.Now()
	// Round duration up to whole seconds so sub-second bans don't
	// collapse to BannedTill == BanCreated (which IsBanned would
	// then treat as already-expired). banlist.json stores Unix
	// seconds for Bitcoin Core compatibility, so the granularity
	// is intrinsic to the format.
	secs := int64(math.Ceil(duration.Seconds()))
	if secs < 1 {
		secs = 1
	}
	key := normalizeSubnet(subnet)
	parsed, err := parseSubnetKey(key)
	if err != nil {
		return fmt.Errorf("banman: unbannable subnet %q: %w", key, err)
	}
	entry := banEntry{
		Subnet:     key,
		BanCreated: now.Unix(),
		BannedTill: now.Unix() + secs,
		Reason:     reason,
	}
	b.mu.Lock()
	if old, ok := b.banned[key]; ok && old.entry.BannedTill >= entry.BannedTill && old.entry.BannedTill > now.Unix() {
		b.mu.Unlock()
		return nil
	}
	b.banned[key] = bannedNet{entry: entry, ipnet: parsed}
	b.mu.Unlock()
	return b.Dump()
}

// Unban removes a single-IP ban. Returns ok=true iff the entry
// existed (and is removed). Idempotent on repeat calls.
func (b *BanMan) Unban(addr net.IP) (bool, error) {
	subnet, err := singleIPSubnet(addr)
	if err != nil {
		return false, err
	}
	return b.UnbanSubnet(subnet)
}

// UnbanSubnet removes a subnet ban. Returns ok=true iff the entry
// existed.
func (b *BanMan) UnbanSubnet(subnet *net.IPNet) (bool, error) {
	if subnet == nil {
		return false, errors.New("banman: nil subnet")
	}
	key := normalizeSubnet(subnet)
	b.mu.Lock()
	_, existed := b.banned[key]
	delete(b.banned, key)
	b.mu.Unlock()
	if !existed {
		return false, nil
	}
	return true, b.Dump()
}

// IsBannedSubnet reports whether exactly this subnet has an active
// ban entry. Unlike IsBanned, containment in a wider ban does not
// count — this is the setban add-precondition check, mirroring
// Bitcoin Core's BanMan::IsBanned(CSubNet) exact banmap lookup.
func (b *BanMan) IsBannedSubnet(subnet *net.IPNet) bool {
	if subnet == nil {
		return false
	}
	key := normalizeSubnet(subnet)
	now := time.Now().Unix()
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.banned[key]
	return ok && now < e.entry.BannedTill
}

// IsBanned reports whether addr is covered by any active (non-
// expired) banned subnet. Expired entries are pruned lazily on
// query. Used by the accept-loop ban check.
func (b *BanMan) IsBanned(addr net.IP) bool {
	if addr == nil {
		return false
	}
	now := time.Now().Unix()
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, bn := range b.banned {
		if bn.entry.BannedTill <= now {
			delete(b.banned, key)
			continue
		}
		if bn.ipnet.Contains(addr) {
			return true
		}
	}
	return false
}

// Discourage adds addr to the discourage Bloom filter. Bitcoin Core's
// CConnman::AddDiscouragedAddress (src/banman.cpp).
func (b *BanMan) Discourage(addr net.IP) {
	if addr == nil {
		return
	}
	b.mu.Lock()
	b.discouraged.Insert(canonicalIP(addr))
	b.mu.Unlock()
}

// IsDiscouraged reports whether the address is in the discourage
// filter. False positives are bounded by discourageBloomFP; the most
// recent discourageBloomCap addresses are always remembered, older
// ones rotate out.
func (b *BanMan) IsDiscouraged(addr net.IP) bool {
	if addr == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.discouraged.Contains(canonicalIP(addr))
}

// ClearBanned removes every banned entry. Mirrors clearbanned RPC
// (src/rpc/net.cpp:868). Does NOT clear the discourage filter — that
// surface is restart-only.
func (b *BanMan) ClearBanned() error {
	b.mu.Lock()
	b.banned = make(map[string]bannedNet)
	b.mu.Unlock()
	return b.Dump()
}

// BanInfo is the shape returned from ListBanned, suitable for RPC
// JSON marshaling.
type BanInfo struct {
	Subnet     string `json:"address"`
	BanCreated int64  `json:"ban_created"`
	BannedTill int64  `json:"banned_until"`
	Reason     string `json:"reason,omitempty"`
}

// ListBanned returns a sorted snapshot of all currently-active
// (non-expired) banned subnets. Sorted by subnet string for stable
// RPC output. Mirrors listbanned (src/rpc/net.cpp:820).
func (b *BanMan) ListBanned() []BanInfo {
	now := time.Now().Unix()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]BanInfo, 0, len(b.banned))
	for key, bn := range b.banned {
		if bn.entry.BannedTill <= now {
			delete(b.banned, key)
			continue
		}
		out = append(out, BanInfo{
			Subnet:     key,
			BanCreated: bn.entry.BanCreated,
			BannedTill: bn.entry.BannedTill,
			Reason:     bn.entry.Reason,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subnet < out[j].Subnet })
	return out
}

// Dump writes the current banlist to disk. Atomic: write tmp, rename.
// Called automatically from Ban / Unban / ClearBanned. No-op when
// the file path is empty.
//
// dumpMu serializes the whole snapshot+write+rename sequence:
// concurrent mutators would otherwise race on the shared tmp path
// (spurious rename errors) or rename a stale snapshot over a newer
// one. Holding dumpMu across the snapshot guarantees the last dump
// to run reflects every mutation that preceded it. b.mu is only
// held for the in-memory copy so the accept-path IsBanned never
// blocks on file I/O. Bitcoin Core serializes the same way in
// BanMan::DumpBanlist (src/banman.cpp).
func (b *BanMan) Dump() error {
	if b.file == "" {
		return nil
	}
	b.dumpMu.Lock()
	defer b.dumpMu.Unlock()
	b.mu.Lock()
	body := banlistFile{BannedNets: make([]banEntry, 0, len(b.banned))}
	for _, bn := range b.banned {
		body.BannedNets = append(body.BannedNets, bn.entry)
	}
	b.mu.Unlock()
	sort.Slice(body.BannedNets, func(i, j int) bool {
		return body.BannedNets[i].Subnet < body.BannedNets[j].Subnet
	})
	raw, err := json.MarshalIndent(&body, "", "  ")
	if err != nil {
		return fmt.Errorf("banman: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(b.file), 0o755); err != nil {
		return fmt.Errorf("banman: mkdir: %w", err)
	}
	tmp := b.file + ".tmp"
	if err := writeFileSync(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("banman: write tmp: %w", err)
	}
	if err := os.Rename(tmp, b.file); err != nil {
		return fmt.Errorf("banman: rename: %w", err)
	}
	return nil
}

// writeFileSync is os.WriteFile plus an fsync before close, so the
// data is durable before the rename that publishes it — a crash
// can't leave an empty banlist at the final path.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Load reads banlist.json. Missing file → empty banlist (the
// fresh-install path). Expired entries are dropped during load so
// they don't pollute IsBanned scans.
func (b *BanMan) Load() error {
	if b.file == "" {
		return nil
	}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("banman: read %s: %w", b.file, err)
	}
	var body banlistFile
	if err := json.Unmarshal(raw, &body); err != nil {
		// A corrupt banlist must not stop the node from starting.
		// Bitcoin Core logs "Recreating the banlist database" and
		// proceeds with an empty banmap (src/banman.cpp:41-45);
		// the next Dump rewrites the file with valid content.
		b.log.Warn("banman: banlist file corrupt, recreating", "file", b.file, "err", err)
		return nil
	}
	now := time.Now().Unix()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range body.BannedNets {
		if e.BannedTill <= now {
			continue
		}
		// Reject malformed CIDR; the file is operator-edited so
		// this is a defense against a typo, not adversarial input.
		_, parsed, err := net.ParseCIDR(e.Subnet)
		if err != nil {
			b.log.Warn("banman: dropping invalid banlist entry", "subnet", e.Subnet, "err", err)
			continue
		}
		// Re-key under the canonical CIDR form. A hand-edited entry
		// like "1.2.3.4/24" (host bits set) would otherwise be stored
		// verbatim and never match the canonical key setban remove
		// computes, making it unremovable except by clearbanned.
		key := normalizeSubnet(parsed)
		e.Subnet = key
		canon, err := parseSubnetKey(key)
		if err != nil {
			b.log.Warn("banman: dropping non-canonicalizable banlist entry", "subnet", e.Subnet, "err", err)
			continue
		}
		if old, ok := b.banned[key]; ok && old.entry.BannedTill >= e.BannedTill {
			continue
		}
		b.banned[key] = bannedNet{entry: e, ipnet: canon}
	}
	return nil
}

// parseSubnetKey parses a canonical subnet key back into its net.IPNet
// for the IsBanned containment cache.
func parseSubnetKey(key string) (*net.IPNet, error) {
	_, parsed, err := net.ParseCIDR(key)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

// singleIPSubnet wraps a single IP as a /32 (IPv4) or /128 (IPv6)
// IPNet so the Ban / Unban API can share code with the subnet
// variants.
func singleIPSubnet(addr net.IP) (*net.IPNet, error) {
	if addr == nil {
		return nil, errors.New("banman: nil ip")
	}
	if v4 := addr.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, nil
	}
	if v6 := addr.To16(); v6 != nil {
		return &net.IPNet{IP: v6, Mask: net.CIDRMask(128, 128)}, nil
	}
	return nil, fmt.Errorf("banman: unrecognized ip family for %s", addr)
}

// normalizeSubnet returns the canonical CIDR string for a subnet so
// repeated Ban calls with structurally-equivalent inputs collapse
// to one entry.
func normalizeSubnet(subnet *net.IPNet) string {
	masked := subnet.IP.Mask(subnet.Mask)
	ones, bits := subnet.Mask.Size()
	if v4 := masked.To4(); v4 != nil {
		// An IPv4-mapped IPv6 subnet ("::ffff:1.2.3.0/120")
		// collapses to its true IPv4 form ("1.2.3.0/24"): the
		// prefix length shifts down by the 96 bits of the
		// ::ffff:0:0/96 mapping. Mirrors Bitcoin Core's CSubNet,
		// which stores v4-mapped addresses as IPv4
		// (src/netaddress.cpp). Without this the rendered key
		// ("1.2.3.0/120") is not a valid CIDR, so it never matches
		// in IsBanned and is dropped by Load.
		if bits == 128 {
			ones -= 96
		}
		return fmt.Sprintf("%s/%d", v4.String(), ones)
	}
	return fmt.Sprintf("%s/%d", masked.To16().String(), ones)
}

// canonicalIP returns the address bytes used to key the discourage
// Bloom filter. IPv4-mapped IPv6 collapses to its v4 form so the
// same IP under different representations yields the same Bloom
// hash.
func canonicalIP(addr net.IP) []byte {
	if v4 := addr.To4(); v4 != nil {
		return v4
	}
	return addr.To16()
}
