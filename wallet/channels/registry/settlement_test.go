package registry

import (
	"math/big"
	"testing"
)

func wei(ether int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(ether), big.NewInt(1e18))
}

func TestSettlementBasic(t *testing.T) {
	// depA=10, depB=10, tAB=6, tBA=1 => balA = 10+1-6 = 5, balB = 15.
	balA, balB := Settlement(wei(10), wei(10), wei(0), wei(0), wei(6), wei(1))
	if balA.Cmp(wei(5)) != 0 || balB.Cmp(wei(15)) != 0 {
		t.Fatalf("got (%s, %s)", balA, balB)
	}
}

func TestSettlementClampLow(t *testing.T) {
	// A "spent" more than everything: balA clamps to 0.
	balA, balB := Settlement(wei(10), wei(5), wei(0), wei(0), wei(100), wei(0))
	if balA.Sign() != 0 || balB.Cmp(wei(15)) != 0 {
		t.Fatalf("got (%s, %s)", balA, balB)
	}
}

func TestSettlementClampHigh(t *testing.T) {
	// A "received" an absurd amount: balA clamps to available.
	balA, balB := Settlement(wei(10), wei(5), wei(0), wei(0), wei(0), wei(100))
	if balA.Cmp(wei(15)) != 0 || balB.Sign() != 0 {
		t.Fatalf("got (%s, %s)", balA, balB)
	}
}

func TestSettlementWithWithdrawals(t *testing.T) {
	// depA=10, wA=4: available=6+depB. tAB=2 => balA = 10-4-2 = 4.
	balA, balB := Settlement(wei(10), wei(3), wei(4), wei(1), wei(2), wei(0))
	if balA.Cmp(wei(4)) != 0 || balB.Cmp(wei(4)) != 0 {
		t.Fatalf("got (%s, %s)", balA, balB)
	}
}

func TestPenaltyScenarios(t *testing.T) {
	refundCap := big.NewInt(1e16)

	// Over-claim of 5 LAX: P = 1 LAX, refund = cap, burn = P - cap.
	p, r, b := Penalty(wei(10), wei(5), refundCap)
	if p.Cmp(wei(1)) != 0 || r.Cmp(refundCap) != 0 || b.Cmp(new(big.Int).Sub(wei(1), refundCap)) != 0 {
		t.Fatalf("got P=%s refund=%s burn=%s", p, r, b)
	}

	// Under-claim (honest-crash direction): no penalty.
	p, r, b = Penalty(wei(5), wei(9), refundCap)
	if p.Sign() != 0 || r.Sign() != 0 || b.Sign() != 0 {
		t.Fatalf("got P=%s refund=%s burn=%s", p, r, b)
	}

	// Cap at the closer's final balance: D=9 => 1.8, capped to 1.
	p, _, _ = Penalty(wei(10), wei(1), refundCap)
	if p.Cmp(wei(1)) != 0 {
		t.Fatalf("got P=%s", p)
	}

	// Dust over-claim: P below the refund cap is fully refunded, none burned.
	p, r, b = Penalty(big.NewInt(1e16+10_000), big.NewInt(1e16), refundCap)
	if b.Sign() != 0 || r.Cmp(p) != 0 {
		t.Fatalf("got P=%s refund=%s burn=%s", p, r, b)
	}
}

// FuzzSettlement checks the §8 safety properties for arbitrary inputs:
// conservation (balA + balB == available) and range (0 <= bal <= available),
// with withdrawals bounded by deposits as the contract guarantees on-chain.
func FuzzSettlement(f *testing.F) {
	f.Add(uint64(10), uint64(5), uint64(2), uint64(1), uint64(7), uint64(3))
	f.Add(uint64(0), uint64(0), uint64(0), uint64(0), uint64(1), uint64(2))
	f.Fuzz(func(t *testing.T, depA, depB, wA, wB, tAB, tBA uint64) {
		if wA > depA || wB > depB {
			t.Skip()
		}
		balA, balB := Settlement(
			new(big.Int).SetUint64(depA), new(big.Int).SetUint64(depB),
			new(big.Int).SetUint64(wA), new(big.Int).SetUint64(wB),
			new(big.Int).SetUint64(tAB), new(big.Int).SetUint64(tBA),
		)
		available := new(big.Int).SetUint64(depA + depB - wA - wB)
		if balA.Sign() < 0 || balB.Sign() < 0 {
			t.Fatalf("negative balance: (%s, %s)", balA, balB)
		}
		if new(big.Int).Add(balA, balB).Cmp(available) != 0 {
			t.Fatalf("conservation violated: %s + %s != %s", balA, balB, available)
		}
	})
}

// FuzzPenalty checks the §9.2 bounds: 0 <= P <= closerFinal, refund <= min(P,
// cap), burn = P - refund.
func FuzzPenalty(f *testing.F) {
	f.Add(uint64(10), uint64(5), uint64(1))
	f.Fuzz(func(t *testing.T, claimed, final, cap uint64) {
		p, r, b := Penalty(
			new(big.Int).SetUint64(claimed),
			new(big.Int).SetUint64(final),
			new(big.Int).SetUint64(cap),
		)
		if p.Sign() < 0 || p.Cmp(new(big.Int).SetUint64(final)) > 0 {
			t.Fatalf("penalty out of range: %s", p)
		}
		if r.Cmp(p) > 0 || r.Cmp(new(big.Int).SetUint64(cap)) > 0 {
			t.Fatalf("refund out of range: %s (P=%s cap=%d)", r, p, cap)
		}
		if new(big.Int).Add(r, b).Cmp(p) != 0 {
			t.Fatalf("refund + burn != penalty: %s + %s != %s", r, b, p)
		}
	})
}
