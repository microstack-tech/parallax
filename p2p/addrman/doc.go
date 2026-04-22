// Package addrman is a Go port of Bitcoin Core's address manager (addrman).
//
// Pinned reference: Bitcoin Core tag v31.0.
// Files ported: src/addrman.h, src/addrman.cpp, src/addrman_impl.h and the
// network-group primitives from src/netgroup.cpp::NetGroupManager::GetGroup.
//
// The package implements two stochastic bucket tables — "tried" (256 buckets
// × 64 entries) and "new" (1024 buckets × 64 entries) — keyed by a random
// per-node 256-bit nKey. Bucket selection hashes the originating peer's
// network group with the address's own network group, which is the property
// that makes the table resistant to Heilman-style eclipse attacks; see
//
//   - Heilman et al., "Eclipse Attacks on Bitcoin's Peer-to-Peer Network"
//     (USENIX Security 2015).
//   - Amiti Uttarwar's address-relay work in Bitcoin Core PRs #17326, #18991,
//     #21528.
//
// This port intentionally diverges from the upstream in a few small ways,
// none of which affect bucketing math:
//
//   - BIP155 network IDs are stored on the Address type instead of being
//     derived from the socket family (Parallax wire format carries them).
//   - asmap is not supported in Phase 1. The grouping fallback uses /16 for
//     IPv4 and /32 for IPv6 unconditionally, matching the Bitcoin Core path
//     taken when no asmap file is loaded. The plan (PIP-0006) can revisit
//     asmap support after v2.0 if operators request it.
//   - A Parallax-specific `Source` tag is carried alongside the originating
//     peer's address. The tag influences eviction priority and Select()
//     weighting in Phases 3 and 5 of PIP-0006. It does not affect bucket
//     assignment.
//
// On-disk format (addrbook.rlp) is Parallax's own, not Bitcoin's peers.dat.
// The file is an RLP-encoded envelope; see persist.go for the versioned
// layout and migration rules. Do not append fields to a released version —
// bump the version and write a migrate_vN_to_vN+1 function instead.
package addrman
