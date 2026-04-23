package addrman

import "fmt"

// Source tags where an address entry first arrived from. Used by later
// PIP-0006 phases for Select() weighting (tcp_gossip preferred over
// legacy_udp) and source-aware bucket eviction. Bucketing math itself does
// not consult this field — it uses the originating peer's CNetAddr, which is
// carried separately on AddrInfo.
type Source uint8

const (
	// SourceTCPGossip — learned from a parallax-disc/1 Peers message.
	// Highest confidence during v2.x: a v2.0 peer explicitly gossiped it.
	SourceTCPGossip Source = 1

	// SourceLegacyUDP — learned from a discv4 UDP response. Treated as
	// lower-confidence during the v2.x deprecation window; evicted first
	// on bucket overflow (Phase 5).
	SourceLegacyUDP Source = 2

	// SourceDNSSeed — learned from a configured DNS seed (plain A/AAAA or
	// PIP-0003 enrtree consumer, and also the one-shot ingest from the
	// `--bootnodes` CLI flag at startup).
	SourceDNSSeed Source = 3

	// SourceManual — operator-pinned via `parallax-cli addnode`. Persists
	// across restarts, dialed before any other source, exempt from
	// source-aware eviction. Mirrors Bitcoin Core's `addnode` semantics.
	SourceManual Source = 4

	// SourceSelfAdvertised — a locally-observed self-address that reached
	// quorum from peer YourAddr reports. The tag is local-only; gossip
	// strips it before relay so receivers just see a regular entry.
	SourceSelfAdvertised Source = 5
)

// String returns the lower_snake_case metric label used by
// p2p/addrman/source/{tcp_gossip,legacy_udp,dns_seed,manual,self_advertised}.
func (s Source) String() string {
	switch s {
	case SourceTCPGossip:
		return "tcp_gossip"
	case SourceLegacyUDP:
		return "legacy_udp"
	case SourceDNSSeed:
		return "dns_seed"
	case SourceManual:
		return "manual"
	case SourceSelfAdvertised:
		return "self_advertised"
	}
	return fmt.Sprintf("source(%d)", s)
}

// valid reports whether s is a defined Source. Used during deserialize to
// refuse addrbook files written by a future version that introduced a new
// source tag we don't know.
func (s Source) valid() bool {
	return s >= SourceTCPGossip && s <= SourceSelfAdvertised
}

// priority returns a relative ranking used by bucket-overflow eviction
// and Select() weighting. Higher = more valuable, less likely to be
// evicted, more likely to be selected. The values are tunables — the
// ordering matters more than the magnitudes:
//
//   manual > self_advertised ≥ tcp_gossip > dns_seed > legacy_udp
//
// Rationale:
//   - manual: operator intent, never overridable by gossip.
//   - self_advertised: our own observation, implicitly trusted.
//   - tcp_gossip: verified v2.0 peer told us — primary v2.x source.
//   - dns_seed: bootstrap path; fresh data but unauthenticated.
//   - legacy_udp: discv4 UDP; lowest confidence during deprecation.
//
// Values are also fed into Select()'s chance multiplier (see
// sourceChanceMultiplier) so dialing preference follows eviction
// preference — the Phase 5 defense is gradient, not modal.
func (s Source) priority() int {
	switch s {
	case SourceManual:
		return 5
	case SourceSelfAdvertised:
		return 4
	case SourceTCPGossip:
		return 3
	case SourceDNSSeed:
		return 2
	case SourceLegacyUDP:
		return 1
	}
	return 0
}

// chanceMultiplier returns the Select() bias for a given source. The
// returned value is multiplied into AddrInfo.chance() so a tcp_gossip
// entry is roughly 2× as likely to be drawn as a legacy_udp entry with
// identical age/attempts. Manual entries are given the strongest pull
// — an operator-pinned peer is dialed before anything else.
func (s Source) chanceMultiplier() float64 {
	switch s {
	case SourceManual:
		return 4.0
	case SourceSelfAdvertised:
		return 1.5
	case SourceTCPGossip:
		return 1.0
	case SourceDNSSeed:
		return 0.75
	case SourceLegacyUDP:
		return 0.5
	}
	return 1.0
}
