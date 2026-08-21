package channeld

import (
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/qrenc"
)

// TestQRCloseCountersignRejectsExpiredClose: the offline scanner's
// confirmed-scan watermark is a lower bound on the real head, so a close
// whose expiry is at or below it is already expired — countersigning it
// re-arms a freeze the chain has already released. The watermark cannot
// falsely reject a live close (expiry <= watermark <= head), so the check is
// conservative even though the horizon check must still be skipped offline.
func TestQRCloseCountersignRejectsExpiredClose(t *testing.T) {
	h := newHub()
	alice := newTestNode(t, h, nil)
	bob := newTestNode(t, h, nil)
	linkChannel(t, alice, bob)

	// Alice last scanned block 100 before going offline.
	dep, err := alice.Store.Deposits(e2eKey)
	if err != nil {
		t.Fatal(err)
	}
	dep.LastScannedBlock = 100
	if err := alice.Store.PutDeposits(e2eKey, dep); err != nil {
		t.Fatal(err)
	}

	// Bob (offline, nowBlock 0) proposes a close that expired at block 50.
	msg, err := bob.Engine.ProposeCoopClose(e2eKey, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	balA, _ := new(big.Int).SetString(msg.BalanceA, 10)
	balB, _ := new(big.Int).SetString(msg.BalanceB, 10)
	env := qrenc.Envelope{
		Type:      qrenc.TypeCoopCloseProposal,
		Registry:  e2eKey.Registry,
		ChainID:   2110,
		ChannelID: e2eKey.ChannelID,
		Expiry:    50,
		BalA:      balA,
		BalB:      balB,
		Sig1:      util.FromHex(msg.Sig),
	}
	if _, err := alice.qrCloseCountersign(env); err == nil {
		t.Fatal("countersigned a close that expired below the scan watermark")
	}
	meta, err := alice.Store.Meta(e2eKey)
	if err != nil {
		t.Fatal(err)
	}
	if meta.FrozenUntilBlock != 0 || meta.PendingClose != nil {
		t.Fatalf("expired close froze the channel: frozen until block %d", meta.FrozenUntilBlock)
	}
}
