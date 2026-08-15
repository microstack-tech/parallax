package xhash

import (
	"math/big"
	"testing"
)

// FuzzASERTNextTarget fuzzes the integer-only aserti3-2d implementation in
// asert.go. Inputs are encoded compactly and expanded into the big.Int /
// int64 domain of ASERTNextTarget:
//
//	anchorBits - compact (nBits) encoding of the anchor target
//	heightDiff - evalHeight - anchorHeight (blocks)
//	timeDiff   - evalTime - anchorParentTime (seconds)
//
// The harness fixes anchorHeight=1 and anchorParentTime=0, which is lossless
// because the algorithm only depends on the height and time deltas (the unit
// tests against the BCHN vectors rely on the same property).
//
// Domain restrictions (early returns, not failures):
//   - anchorTarget must be a positive value no larger than the pow limit
//     (asertMaxBits), i.e. a target that can actually occur on a chain.
//   - heightDiff is capped at 2^32 blocks.
//   - |timeDiff| is capped at 2^46 seconds (~2.2 million years) so that the
//     implementation's int64 fixed-point exponent math cannot overflow;
//     inputs outside that range are outside the algorithm's documented
//     domain, matching the BCH reference implementation's assumptions.
//
// Invariants checked per iteration:
//   - no panic
//   - result is within [1, maxTarget]
//   - determinism: the same inputs twice yield an identical result
//   - monotonicity: timeDiff+600 (slower blocks) never yields a smaller
//     (harder) target than timeDiff
//   - the anchorTarget argument is not mutated by the call
func FuzzASERTNextTarget(f *testing.F) {
	// Rows from the BCHN aserti3-2d vectors (testdata/aserti3-2d/run*),
	// re-expressed as deltas from each run's anchor.
	f.Add(uint32(0x1d00ffff), uint64(1), int64(1200))  // run01 iter 1: steady 600s blocks at pow limit
	f.Add(uint32(0x1d00ffff), uint64(10), int64(6600)) // run01 iter 10
	f.Add(uint32(0x1d00ffff), uint64(1), int64(0))     // run05 iter 1: height jump, no time increment
	f.Add(uint32(0x1d00ffff), uint64(289), int64(0))   // run05 iter 2: halflife height jump
	f.Add(uint32(0x1802aee8), uint64(1), int64(900))   // run09 iter 1: 300s blocks near 2^31 height
	f.Add(uint32(0x1802aee8), uint64(4), int64(1800))  // run09 iter 4
	f.Add(uint32(0x1802aee8), uint64(1), int64(1200))  // run11 iter 1: random solvetimes run
	f.Add(uint32(0x1802aee8), uint64(2), int64(1199))  // run12 iter 2: blocks going back in time

	// Extremes.
	f.Add(uint32(0x1d00ffff), uint64(0), int64(600))         // heightDiff 0
	f.Add(uint32(0x1d00ffff), uint64(1)<<32, int64(600))     // heightDiff 2^32 (domain edge)
	f.Add(uint32(0x01010000), uint64(1), int64(600))         // minimal anchor target (1)
	f.Add(uint32(0x1d00ffff), uint64(1), int64(1)<<62)       // huge positive timeDiff (out of domain)
	f.Add(uint32(0x1d00ffff), uint64(1), -(int64(1) << 62))  // huge negative timeDiff (out of domain)
	f.Add(uint32(0x1d00ffff), uint64(1), (int64(1)<<46)-600) // largest in-domain timeDiff
	f.Add(uint32(0x1d00ffff), uint64(1), -(int64(1) << 46))  // most negative in-domain timeDiff

	maxTarget := bitsToTargetBCH(asertMaxBits)

	f.Fuzz(func(t *testing.T, anchorBits uint32, heightDiff uint64, timeDiff int64) {
		anchorTarget := bitsToTargetBCH(anchorBits)

		// Domain: anchor target must be a positive on-chain target.
		if anchorTarget.Sign() <= 0 || anchorTarget.Cmp(maxTarget) > 0 {
			return
		}
		// Domain: bounded height delta.
		if heightDiff > uint64(1)<<32 {
			return
		}
		// Domain: bounded time delta, leaving headroom for the +600
		// monotonicity probe. Keeps the int64 exponent math overflow-free:
		// |timeDelta - 600*numBlocks| * 2^16 stays well below 2^63.
		const timeBound = int64(1) << 46
		if timeDiff < -timeBound || timeDiff > timeBound-600 {
			return
		}

		const anchorHeight = int64(1)
		const anchorParentTime = int64(0)
		evalHeight := anchorHeight + int64(heightDiff)
		evalTime := anchorParentTime + timeDiff

		anchorBefore := new(big.Int).Set(anchorTarget)

		got := ASERTNextTarget(anchorHeight, anchorParentTime, anchorTarget,
			evalHeight, evalTime, maxTarget)

		// Result range: [1, maxTarget].
		if got.Sign() <= 0 {
			t.Fatalf("target %v not positive (anchorBits=0x%08x heightDiff=%d timeDiff=%d)",
				got, anchorBits, heightDiff, timeDiff)
		}
		if got.Cmp(maxTarget) > 0 {
			t.Fatalf("target %v exceeds maxTarget %v (anchorBits=0x%08x heightDiff=%d timeDiff=%d)",
				got, maxTarget, anchorBits, heightDiff, timeDiff)
		}

		// Argument must not be mutated.
		if anchorTarget.Cmp(anchorBefore) != 0 {
			t.Fatalf("anchorTarget mutated: before=%v after=%v (anchorBits=0x%08x heightDiff=%d timeDiff=%d)",
				anchorBefore, anchorTarget, anchorBits, heightDiff, timeDiff)
		}

		// Determinism: identical inputs, identical output.
		again := ASERTNextTarget(anchorHeight, anchorParentTime, anchorTarget,
			evalHeight, evalTime, maxTarget)
		if got.Cmp(again) != 0 {
			t.Fatalf("non-deterministic: first=%v second=%v (anchorBits=0x%08x heightDiff=%d timeDiff=%d)",
				got, again, anchorBits, heightDiff, timeDiff)
		}

		// Monotonicity in time: slower blocks (larger timeDiff) must yield an
		// easier or equal target, never a harder one.
		later := ASERTNextTarget(anchorHeight, anchorParentTime, anchorTarget,
			evalHeight, evalTime+600, maxTarget)
		if later.Cmp(got) < 0 {
			t.Fatalf("monotonicity violated: target(timeDiff=%d)=%v > target(timeDiff=%d)=%v (anchorBits=0x%08x heightDiff=%d)",
				timeDiff, got, timeDiff+600, later, anchorBits, heightDiff)
		}
	})
}
