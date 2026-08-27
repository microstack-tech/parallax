package protocol

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/util"
)

// TestHandleProposalRejectsOverwideAmount: a proposal whose amount does not
// fit a uint256 word must be rejected at the wire layer, not reach the
// EIP-712 encoder — big.Int.FillBytes panics on a value wider than its
// 32-byte destination, which would let any counterparty crash the node
// before a single balance check runs.
func TestHandleProposalRejectsOverwideAmount(t *testing.T) {
	alice, bob, key := setupPair(t, Config{}, Config{})
	invoiceFor(t, bob, "inv1", wei1)

	huge := new(big.Int).Lsh(big.NewInt(1), 256) // 2^256
	msg := ProposalMsg{
		V:         1,
		InvoiceID: "inv1",
		State: WireState{
			ChannelID:       "1",
			Registry:        strings.ToLower(key.Registry.Hex()),
			ChainID:         key.ChainID,
			Seq:             "1",
			TransferredAtoB: huge.String(),
			TransferredBtoA: "0",
			LocksRoot:       util.Hash{}.Hex(),
			LockedAmount:    "0",
		},
		ProposerRole: "A",
	}
	res, err := bob.engine.HandleProposal(msg, alice.npub, nowBlock, nowUnix)
	if err == nil && res.Nack == nil {
		t.Fatalf("overwide amount not refused: %+v", res)
	}
	if res.Completed != nil {
		t.Fatal("overwide amount countersigned")
	}

	// The largest representable uint256 still parses (and is then refused by
	// the balance checks, not the codec).
	max256 := new(big.Int).Sub(huge, big.NewInt(1))
	if _, err := parseWei(max256.String()); err != nil {
		t.Fatalf("2^256-1 must parse: %v", err)
	}
	if _, err := parseWei(huge.String()); err == nil {
		t.Fatal("2^256 parsed as a U256")
	}
}
