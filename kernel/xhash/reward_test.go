package xhash

import (
	"math/big"
	"testing"
)

func B(n int64) *big.Int { return big.NewInt(n) }

// helper: Initial / 2^k
func initialDiv2(k uint) *big.Int {
	d := new(big.Int).Lsh(big1, k) // 2^k
	out := new(big.Int).Set(InitialBlockRewardWei)
	out.Div(out, d)
	return out
}

func TestCalcBlockReward_GenesisZero(t *testing.T) {
	if got := calcBlockReward(0); got.Sign() != 0 {
		t.Fatalf("genesis reward must be 0, got %v", got)
	}
}

func TestCalcBlockReward_FirstEra_NoHalving(t *testing.T) {
	// Any height in (0, HalvingIntervalBlocks) yields initial reward.
	h := uint64(1)
	got := calcBlockReward(h)
	want := new(big.Int).Set(InitialBlockRewardWei)
	if got.Cmp(want) != 0 {
		t.Fatalf("block %d: expected %v, got %v", h, want, got)
	}

	h = HalvingIntervalBlocks - 1
	got = calcBlockReward(h)
	if got.Cmp(want) != 0 {
		t.Fatalf("block %d: expected %v, got %v", h, want, got)
	}
}

func TestCalcBlockReward_AtFirstHalvingBoundary(t *testing.T) {
	// Exactly at the boundary the reward halves.
	h := HalvingIntervalBlocks
	got := calcBlockReward(h)
	want := initialDiv2(1) // Initial / 2
	if got.Cmp(want) != 0 {
		t.Fatalf("block %d (first halving): expected %v, got %v", h, want, got)
	}
}

func TestCalcBlockReward_SecondHalving(t *testing.T) {
	// One era later: 2 halvings → Initial / 4
	h := 2 * HalvingIntervalBlocks
	got := calcBlockReward(h)
	want := initialDiv2(2) // Initial / 4
	if got.Cmp(want) != 0 {
		t.Fatalf("block %d (second halving): expected %v, got %v", h, want, got)
	}
}

func TestCalcBlockReward_SixtyThreeHalvings(t *testing.T) {
	// halvings == 63 should still compute Initial / 2^63 (non-zero).
	h := 63 * HalvingIntervalBlocks
	got := calcBlockReward(h)
	want := initialDiv2(63)
	if got.Cmp(want) != 0 {
		t.Fatalf("block %d (63 halvings): expected %v, got %v", h, want, got)
	}
	if got.Sign() == 0 {
		t.Fatalf("block %d (63 halvings): expected non-zero reward, got 0", h)
	}
}

func TestCalcBlockReward_OverflowGuard_64HalvingsAndBeyond(t *testing.T) {
	// halvings > 63 returns 0 to avoid Lsh overflow concerns.
	h := 64 * HalvingIntervalBlocks
	if got := calcBlockReward(h); got.Sign() != 0 {
		t.Fatalf("block %d (64 halvings): expected 0, got %v", h, got)
	}
	h = 100 * HalvingIntervalBlocks
	if got := calcBlockReward(h); got.Sign() != 0 {
		t.Fatalf("block %d (100 halvings): expected 0, got %v", h, got)
	}
}

func TestCalcBlockReward_MonotonicAroundBoundary(t *testing.T) {
	// Just before boundary vs at boundary: reward must not increase.
	pre := HalvingIntervalBlocks - 1
	at := HalvingIntervalBlocks

	rPre := calcBlockReward(pre)
	rAt := calcBlockReward(at)

	if rAt.Cmp(rPre) >= 0 {
		t.Fatalf("monotonicity violated: reward at boundary (%v) should be < pre-boundary (%v)", rAt, rPre)
	}
}
