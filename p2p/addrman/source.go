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
