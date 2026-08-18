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
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
)

// Onion bans (PIP-0007 §4): exact-host rows in the same banlist,
// keyed by the canonical lowercased "<base32>.onion" hostname. There
// is no subnet concept for onion space — Core's CSubNet holds single
// non-IP addresses the same way. Onion bans gate outbound dials and
// addrman-driven candidate selection; inbound onion streams all
// arrive from the local Tor daemon at loopback and cannot be
// attributed to an onion address at accept time.

// isOnionKey reports whether a banlist key names an onion host.
func isOnionKey(key string) bool {
	return strings.HasSuffix(strings.ToLower(key), ".onion")
}

// normalizeOnionHost validates host as a Tor v3 onion hostname and
// returns its canonical (lowercase) form.
func normalizeOnionHost(host string) (string, error) {
	na, err := addrman.ParseOnion(host, 1)
	if err != nil {
		return "", err
	}
	return na.OnionHostname(), nil
}

// BanHost adds or extends a ban on an onion host. Same only-extend
// semantics as BanSubnet: a shorter re-ban never cuts an existing ban
// short.
func (b *BanMan) BanHost(host string, duration time.Duration, reason string) error {
	key, err := normalizeOnionHost(host)
	if err != nil {
		return fmt.Errorf("banman: unbannable host %q: %w", host, err)
	}
	if duration <= 0 {
		duration = DefaultBanDuration
	}
	now := time.Now()
	secs := int64(math.Ceil(duration.Seconds()))
	if secs < 1 {
		secs = 1
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
	b.banned[key] = bannedNet{entry: entry}
	b.markDirtyLocked()
	b.mu.Unlock()
	b.dumpAndLog()
	return nil
}

// UnbanHost removes an onion-host ban. Returns ok=true iff the entry
// existed.
func (b *BanMan) UnbanHost(host string) (bool, error) {
	key, err := normalizeOnionHost(host)
	if err != nil {
		return false, fmt.Errorf("banman: %w", err)
	}
	b.mu.Lock()
	_, existed := b.banned[key]
	delete(b.banned, key)
	if existed {
		b.markDirtyLocked()
	}
	b.mu.Unlock()
	if !existed {
		return false, nil
	}
	b.dumpAndLog()
	return true, nil
}

// IsBannedHost reports whether the onion host has an active ban.
// Exact match — the check used both by the outbound dial gate and as
// the setban add-precondition.
func (b *BanMan) IsBannedHost(host string) bool {
	key, err := normalizeOnionHost(host)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.banned[key]
	return ok && now < e.entry.BannedTill
}
