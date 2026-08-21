package channeld

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

// erroringBackend simulates an online node whose RPC is transiently failing:
// every chain call errors. Paths not exercised panic on the nil embedded
// interface.
type erroringBackend struct{ Backend }

var errRPCDown = errors.New("rpc down")

func (erroringBackend) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return nil, errRPCDown
}

func (erroringBackend) PendingNonceAt(ctx context.Context, account util.Address) (uint64, error) {
	return 0, errRPCDown
}

func (erroringBackend) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return nil, errRPCDown
}

// TestCoopCloseProposalDeferredWhenHeadUnavailable: countersigning freezes
// the channel until the proposal's expiry, and only the head can judge the
// expiry and freeze-horizon checks. An ONLINE node whose RPC hiccups must
// defer the proposal, not process it with nowBlock 0 — that skips both
// checks and auto-countersigns a near-forever freeze the security model says
// only offline QR scanners may skip the horizon for. The peer retransmits,
// so a deferred proposal is retried against a healthy RPC.
func TestCoopCloseProposalDeferredWhenHeadUnavailable(t *testing.T) {
	h := newHub()
	bob := newTestNode(t, h, nil) // proposer (offline node: no backend)

	cfg := DefaultConfig()
	cfg.Registries = map[string][]RegistryEntry{
		"v1": {{Address: e2eKey.Registry.Hex(), ChainID: 2110}},
	}
	cfg.Nostr.Relays = []string{"wss://hub"}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	alice, err := New(cfg, t.TempDir(), key, erroringBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { alice.Close() })
	alice.Pool = newHubPool(h, alice.SelfPub)
	alice.Transmitter = newHubTransmitter(alice)
	linkChannel(t, alice, bob)

	// A freeze grenade: expiry absurdly far in the future. The offline
	// proposer cannot judge the horizon (nowBlock 0 skips it by design).
	msg, err := bob.Engine.ProposeCoopClose(e2eKey, 1<<40, 0)
	if err != nil {
		t.Fatal(err)
	}
	rumor := encodeRumor(t, protocol.KindCoopCloseProposal, msg)
	if err := alice.handleRumor(context.Background(), rumor, bob.SelfPub); err == nil {
		t.Fatal("far-future close processed with no head view")
	}
	meta, err := alice.Store.Meta(e2eKey)
	if err != nil {
		t.Fatal(err)
	}
	if meta.FrozenUntilBlock != 0 || meta.PendingClose != nil {
		t.Fatalf("countersigned a far-future close during an RPC outage: frozen until block %d", meta.FrozenUntilBlock)
	}
}
