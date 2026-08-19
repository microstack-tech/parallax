package registry

import "math/big"

// Settlement mirrors the contract's §8 math. All inputs are cumulative wei
// amounts; outputs are the final channel balances at settlement time.
//
//	available = depositA + depositB − withdrawnA − withdrawnB
//	creditA   = depositA + transferredBtoA
//	debitA    = withdrawnA + transferredAtoB
//	balA      = clamp(creditA − debitA, 0, available)
//	balB      = available − balA
//
// Clamping means no signed amounts, however inconsistent, can extract more
// than available or drive a payout negative; an over-spent state costs the
// over-spender's counterparty, which is why the wallet validates balance
// sufficiency before countersigning (SPEC-003 §8).
func Settlement(depositA, depositB, withdrawnA, withdrawnB, transferredAtoB, transferredBtoA *big.Int) (balA, balB *big.Int) {
	available := new(big.Int).Add(depositA, depositB)
	available.Sub(available, withdrawnA)
	available.Sub(available, withdrawnB)

	creditA := new(big.Int).Add(depositA, transferredBtoA)
	debitA := new(big.Int).Add(withdrawnA, transferredAtoB)

	balA = new(big.Int)
	if creditA.Cmp(debitA) > 0 {
		balA.Sub(creditA, debitA)
	}
	if balA.Cmp(available) > 0 {
		balA.Set(available)
	}
	balB = new(big.Int).Sub(available, balA)
	return balA, balB
}

// PenaltyBPS is the fraud penalty in basis points (SPEC-001 §9.3).
const PenaltyBPS = 2000

// Penalty mirrors the contract's §9.2 mechanics, applied at settle iff the
// closer's submission was superseded by a challenge:
//
//	D      = max(0, closerClaimedBalance − closerFinalBalance)
//	P      = min(D·20%, closerFinalBalance)
//	refund = min(P, challengeRefund)   → paid to the last challenger
//	burn   = P − refund                → transferred to address(0)
//
// The returned penalty is the total deduction from the closer's final
// balance; burn is the portion destroyed.
func Penalty(closerClaimedBalance, closerFinalBalance, challengeRefund *big.Int) (penalty, refund, burn *big.Int) {
	overClaim := new(big.Int)
	if closerClaimedBalance.Cmp(closerFinalBalance) > 0 {
		overClaim.Sub(closerClaimedBalance, closerFinalBalance)
	}

	penalty = new(big.Int).Mul(overClaim, big.NewInt(PenaltyBPS))
	penalty.Div(penalty, big.NewInt(10_000))
	if penalty.Cmp(closerFinalBalance) > 0 {
		penalty.Set(closerFinalBalance)
	}

	refund = new(big.Int).Set(penalty)
	if refund.Cmp(challengeRefund) > 0 {
		refund.Set(challengeRefund)
	}
	burn = new(big.Int).Sub(penalty, refund)
	return penalty, refund, burn
}
