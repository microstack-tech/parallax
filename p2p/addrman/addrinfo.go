package addrman

import (
	"math"
	"time"
)

// AddrInfo is the internal record addrman keeps per known address. Fields
// mirror Bitcoin Core's AddrInfo (src/addrman_impl.h:45) plus Parallax's
// Source tag. The AddrInfo is returned by GetEntries for testing and
// inspection, but callers should treat it as read-only.
type AddrInfo struct {
	// Addr is the stored address (equivalent to Bitcoin's CAddress).
	Addr NetAddr

	// LastSeen mirrors CAddress::nTime — Unix seconds from the origin
	// claim, clamped on ingest per PIP-0006 §Phase 2 (`[now - 10min,
	// now + 10min]`, minus 2-hour penalty for gossip-sourced entries).
	LastSeen time.Time

	// Source is the originating peer's NetAddr (no port) — Bitcoin's
	// AddrInfo::source in src/addrman_impl.h:55. Used only for
	// computing newBucket; the port is ignored.
	Source NetAddr

	// SourceTag is the Parallax source classification (tcp_gossip,
	// legacy_udp, ...). Used by later PIP-0006 phases for eviction
	// priority and Select() weighting. Bucket math does not consult it.
	SourceTag Source

	// LastTry is the last connect attempt time.
	LastTry time.Time
	// LastSuccess is the last successful connect time.
	LastSuccess time.Time
	// LastCountAttempt is the last attempt that counted toward Attempts
	// (matches Bitcoin's m_last_count_attempt — only the first failure
	// after a Good() call increments the counter, so transient outages
	// don't pile on).
	LastCountAttempt time.Time

	// Attempts is the number of failed connect attempts since the last
	// successful connect.
	Attempts int

	// RefCount is the number of new-bucket positions this entry occupies.
	// 0 iff InTried == true.
	RefCount int

	// InTried is true when the entry lives in the tried table.
	InTried bool

	// randomPos is the index of this entry in AddrMan.vRandom, used only
	// by GetAddr's on-the-fly shuffle. Not exported.
	randomPos int
}

// IsTerrible reports whether the entry is too stale / too many failures to
// keep. Ported from AddrInfo::IsTerrible (src/addrman.cpp:69-92).
//
// The thresholds are NOT arbitrary:
//
//   - 1-minute grace so a flaky peer we just tried isn't instantly evicted.
//   - 10-minute future-dating cap: clock-skew attackers shouldn't be able
//     to keep junk alive by advertising a future LastSeen.
//   - 30-day horizon: anything we haven't seen in a month is stale.
//   - 3 attempts with never-a-success: give up.
//   - 10 failures in the last week: give up.
func (i *AddrInfo) IsTerrible(now time.Time) bool {
	if !i.LastTry.IsZero() && now.Sub(i.LastTry) <= time.Minute {
		return false
	}
	if i.LastSeen.After(now.Add(10 * time.Minute)) {
		return true
	}
	if now.Sub(i.LastSeen) > addrmanHorizon {
		return true
	}
	if i.LastSuccess.IsZero() && i.Attempts >= addrmanRetries {
		return true
	}
	if !i.LastSuccess.IsZero() && now.Sub(i.LastSuccess) > addrmanMinFail && i.Attempts >= addrmanMaxFailures {
		return true
	}
	return false
}

// chance returns the per-entry Select() weighting. Ported from
// AddrInfo::GetChance (src/addrman.cpp:94-107).
//
//	1.0  baseline
//	* 0.01 if we tried this entry in the last 10 minutes
//	* 0.66 ** min(attempts, 8) to dampen repeated failures, capped at 8
//	  so an outage of many days doesn't zero the entry out entirely.
func (i *AddrInfo) chance(now time.Time) float64 {
	c := 1.0
	if !i.LastTry.IsZero() && now.Sub(i.LastTry) < 10*time.Minute {
		c *= 0.01
	}
	c *= math.Pow(0.66, float64(min(i.Attempts, 8)))
	return c
}

// Tunables mirroring Bitcoin Core constants in src/addrman.cpp:34-46.
const (
	addrmanHorizon            = 30 * 24 * time.Hour
	addrmanRetries            = 3
	addrmanMaxFailures        = 10
	addrmanMinFail            = 7 * 24 * time.Hour
	addrmanReplacement        = 4 * time.Hour
	addrmanTriedCollisionSize = 10
	addrmanTestWindow         = 40 * time.Minute
)
