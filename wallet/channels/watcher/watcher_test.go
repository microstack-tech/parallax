package watcher

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind/backends"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/validation"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

var (
	ether  = big.NewInt(1e18)
	refund = big.NewInt(1e16)
)

type simEnv struct {
	backend  *backends.SimulatedBackend
	contract *registry.ChannelRegistry
	regAddr  util.Address

	alicePriv, bobPriv *ecdsa.PrivateKey
	aliceAuth, bobAuth *bind.TransactOpts
	alice, bob         util.Address

	store *proofstore.Store // bob's wallet store (the honest victim)
	key   proofstore.ChannelKey
}

func mustAuth(t *testing.T, priv *ecdsa.PrivateKey) *bind.TransactOpts {
	t.Helper()
	auth, err := bind.NewKeyedTransactorWithChainID(priv, big.NewInt(1337))
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func lax(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), ether) }

// setupSim deploys the real registry to a simulated chain, opens channel 1
// (alice=A: 10 LAX, bob=B: 5 LAX), and prepares bob's wallet store.
func setupSim(t *testing.T) *simEnv {
	t.Helper()
	e := &simEnv{}
	var err error
	e.alicePriv, _ = crypto.GenerateKey()
	e.bobPriv, _ = crypto.GenerateKey()
	e.alice = crypto.PubkeyToAddress(e.alicePriv.PublicKey)
	e.bob = crypto.PubkeyToAddress(e.bobPriv.PublicKey)
	e.aliceAuth = mustAuth(t, e.alicePriv)
	e.bobAuth = mustAuth(t, e.bobPriv)

	e.backend = backends.NewSimulatedBackend(validation.GenesisAlloc{
		e.alice: {Balance: lax(100)},
		e.bob:   {Balance: lax(100)},
	}, 30_000_000)
	t.Cleanup(func() { e.backend.Close() })

	e.regAddr, _, e.contract, err = registry.DeployChannelRegistry(e.aliceAuth, e.backend, refund)
	if err != nil {
		t.Fatal(err)
	}
	e.backend.Commit()

	e.aliceAuth.Value = lax(10)
	if _, err := e.contract.Open(e.aliceAuth, e.bob, 36); err != nil {
		t.Fatal(err)
	}
	e.aliceAuth.Value = nil
	e.backend.Commit()

	e.bobAuth.Value = lax(5)
	if _, err := e.contract.Deposit(e.bobAuth, big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	e.bobAuth.Value = nil
	e.backend.Commit()

	e.key = proofstore.ChannelKey{ChainID: "1337", Registry: e.regAddr, ChannelID: 1}
	e.store, err = proofstore.Open(filepath.Join(t.TempDir(), "bob.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.store.Close() })
	err = e.store.CreateChannel(proofstore.ChannelMeta{
		Key:             e.key,
		Role:            proofstore.RoleB,
		Status:          proofstore.StatusOpen,
		PeerNpub:        "alice-npub",
		PeerAddress:     e.alice,
		ChallengePeriod: 36,
		OpenedAtBlock:   0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// dualSigned builds a complete state signed by both on-chain keys.
func (e *simEnv) dualSigned(t *testing.T, seq uint64, tAB, tBA *big.Int) proofstore.SignedState {
	t.Helper()
	st := proofstore.SignedState{
		Key:             e.key,
		Seq:             seq,
		TransferredAtoB: proofstore.NewU256(tAB),
		TransferredBtoA: proofstore.NewU256(tBA),
		LockedAmount:    proofstore.NewU256(nil),
	}
	digest, err := st.Digest()
	if err != nil {
		t.Fatal(err)
	}
	st.SigA, err = protocol.NewKeySigner(e.alicePriv).SignDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	st.SigB, err = protocol.NewKeySigner(e.bobPriv).SignDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (e *simEnv) commit(n int) {
	for i := 0; i < n; i++ {
		e.backend.Commit()
	}
}

func (e *simEnv) proofArg(st proofstore.SignedState) registry.ParallaxChannelRegistryBalanceProof {
	return registry.ParallaxChannelRegistryBalanceProof{
		ChannelId:       new(big.Int).SetUint64(st.Key.ChannelID),
		Seq:             st.Seq,
		TransferredAtoB: st.TransferredAtoB.BigInt(),
		TransferredBtoA: st.TransferredBtoA.BigInt(),
		LocksRoot:       st.LocksRoot,
		LockedAmount:    st.LockedAmount.BigInt(),
	}
}

func newWatcher(t *testing.T, e *simEnv) (*Watcher, *[]string) {
	t.Helper()
	w, err := New(Config{ChainID: "1337", Registry: e.regAddr, Confirmations: 3}, e.store, e.backend, e.bobAuth)
	if err != nil {
		t.Fatal(err)
	}
	alarms := &[]string{}
	w.Alarm = func(format string, args ...any) {
		*alarms = append(*alarms, fmt.Sprintf(format, args...))
	}
	return w, alarms
}

func TestDepositCreditingAtConfirmationDepth(t *testing.T) {
	e := setupSim(t)
	w, _ := newWatcher(t, e)
	ctx := context.Background()

	// Bob's deposit landed in the most recent block: not yet 3-confirmed.
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	dep, _ := e.store.Deposits(e.key)
	if dep.DepositB.BigInt().Sign() != 0 {
		t.Fatalf("unconfirmed deposit credited: %+v", dep)
	}

	e.commit(2) // now ≥3 deep
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	dep, _ = e.store.Deposits(e.key)
	if dep.DepositA.BigInt().Cmp(lax(10)) != 0 || dep.DepositB.BigInt().Cmp(lax(5)) != 0 {
		t.Fatalf("deposits not credited: %+v", dep)
	}
	if dep.LastScannedBlock == 0 {
		t.Fatal("watermark not advanced")
	}
}

func TestAutoChallengeAndSettleAgainstCheater(t *testing.T) {
	e := setupSim(t)
	w, alarms := newWatcher(t, e)
	ctx := context.Background()

	// The channel's true latest state: seq 5, alice has paid bob 3 LAX.
	latest := e.dualSigned(t, 5, lax(3), new(big.Int))
	if err := e.store.PutComplete(latest); err != nil {
		t.Fatal(err)
	}
	// Alice cheats: closes at stale seq 2 where she had paid only 1 LAX.
	stale := e.dualSigned(t, 2, lax(1), new(big.Int))
	if _, err := e.contract.StartClose(e.aliceAuth, big.NewInt(1), e.proofArg(stale), stale.SigA, stale.SigB); err != nil {
		t.Fatal(err)
	}
	e.commit(3) // confirm CloseStarted

	// Bob's watcher: detects the stale close, alarms, challenges with seq 5.
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	meta, _ := e.store.Meta(e.key)
	if meta.Status != proofstore.StatusClosing {
		t.Fatalf("status: %s", meta.Status)
	}
	if len(*alarms) == 0 {
		t.Fatal("no alarm on stale CloseStarted")
	}
	e.commit(1) // mine the challenge

	onchain, err := e.contract.GetChannel(nil, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if onchain.ClosingSeq != 5 || onchain.LastChallenger != e.bob {
		t.Fatalf("challenge not effective: seq %d challenger %s", onchain.ClosingSeq, onchain.LastChallenger.Hex())
	}

	// A second tick must not resubmit (already at our seq).
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	// Roll past the deadline; the watcher settles.
	e.commit(40)
	aliceBefore, _ := e.backend.BalanceAt(ctx, e.alice, nil)
	bobBefore, _ := e.backend.BalanceAt(ctx, e.bob, nil)
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.commit(1) // mine settle

	onchain, _ = e.contract.GetChannel(nil, big.NewInt(1))
	if onchain.State != stateNonExistent {
		t.Fatalf("not settled on-chain: state %d", onchain.State)
	}

	// Penalty accounting: alice claimed balA=9 at seq 2, truth is balA=7.
	// D=2, P=0.4 LAX deducted from alice; refund 0.01 to bob (challenger);
	// burn 0.39 to address(0). Alice pays no gas at settle, so her delta is
	// exact; bob's is net of the settle gas.
	aliceAfter, _ := e.backend.BalanceAt(ctx, e.alice, nil)
	bobAfter, _ := e.backend.BalanceAt(ctx, e.bob, nil)
	aliceGot := new(big.Int).Sub(aliceAfter, aliceBefore)
	wantAlice := new(big.Int).Sub(lax(7), new(big.Int).Div(lax(4), big.NewInt(10))) // 6.6
	if aliceGot.Cmp(wantAlice) != 0 {
		t.Fatalf("cheater payout: got %s want %s", aliceGot, wantAlice)
	}
	bobGot := new(big.Int).Sub(bobAfter, bobBefore)
	wantBobMin := new(big.Int).Sub(new(big.Int).Add(lax(8), refund), lax(1) /*gas headroom*/)
	if bobGot.Cmp(wantBobMin) < 0 || bobGot.Cmp(new(big.Int).Add(lax(8), refund)) > 0 {
		t.Fatalf("victim payout out of range: %s", bobGot)
	}
	regBal, _ := e.backend.BalanceAt(ctx, e.regAddr, nil)
	if regBal.Sign() != 0 {
		t.Fatalf("registry not drained: %s", regBal)
	}

	// Local status converges once Settled is confirmed.
	e.commit(3)
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	meta, _ = e.store.Meta(e.key)
	if meta.Status != proofstore.StatusSettled {
		t.Fatalf("status: %s", meta.Status)
	}
}

func TestHonestCloseNoChallengeJustSettle(t *testing.T) {
	e := setupSim(t)
	w, alarms := newWatcher(t, e)
	ctx := context.Background()

	latest := e.dualSigned(t, 5, lax(3), new(big.Int))
	if err := e.store.PutComplete(latest); err != nil {
		t.Fatal(err)
	}
	// Alice closes honestly at the latest state.
	if _, err := e.contract.StartClose(e.aliceAuth, big.NewInt(1), e.proofArg(latest), latest.SigA, latest.SigB); err != nil {
		t.Fatal(err)
	}
	e.commit(3)

	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*alarms) != 0 {
		t.Fatalf("alarm on honest close: %v", *alarms)
	}
	onchain, _ := e.contract.GetChannel(nil, big.NewInt(1))
	if onchain.LastChallenger != (util.Address{}) {
		t.Fatal("challenged an honest close")
	}

	e.commit(40)
	aliceBefore, _ := e.backend.BalanceAt(ctx, e.alice, nil)
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.commit(1)
	onchain, _ = e.contract.GetChannel(nil, big.NewInt(1))
	if onchain.State != stateNonExistent {
		t.Fatal("not settled")
	}
	// No penalty on the honest path: alice receives her full balance
	// (10 - 3 = 7), exact since bob's key sent the settle.
	aliceAfter, _ := e.backend.BalanceAt(ctx, e.alice, nil)
	if got := new(big.Int).Sub(aliceAfter, aliceBefore); got.Cmp(lax(7)) != 0 {
		t.Fatalf("honest closer payout: got %s want %s", got, lax(7))
	}
}

func TestWithdrawCreditingAndFreezeClearOnCoopClose(t *testing.T) {
	e := setupSim(t)
	w, _ := newWatcher(t, e)
	ctx := context.Background()

	// A signed cooperative withdraw of 4 LAX to alice.
	expiry := uint64(200)
	digest, err := e.contract.HashWithdraw(nil, big.NewInt(1), e.alice, lax(4), expiry)
	if err != nil {
		t.Fatal(err)
	}
	sigA, _ := protocol.NewKeySigner(e.alicePriv).SignDigest(digest)
	sigB, _ := protocol.NewKeySigner(e.bobPriv).SignDigest(digest)
	if _, err := e.contract.CooperativeWithdraw(e.aliceAuth, big.NewInt(1), e.alice, lax(4), expiry, sigA, sigB); err != nil {
		t.Fatal(err)
	}
	e.commit(3)
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	dep, _ := e.store.Deposits(e.key)
	if dep.WithdrawnA.BigInt().Cmp(lax(4)) != 0 {
		t.Fatalf("withdrawal not credited: %+v", dep)
	}

	// Freeze bob locally (pending coop close), then settle cooperatively
	// on-chain: available = 15 - 4 = 11, split 6/5.
	if err := e.store.UpdateMeta(e.key, func(m *proofstore.ChannelMeta) {
		m.FrozenUntilBlock = 10_000
		m.PendingClose = &proofstore.PendingCoopClose{ExpiryBlock: 10_000}
	}); err != nil {
		t.Fatal(err)
	}
	cdig, err := e.contract.HashCooperativeClose(nil, big.NewInt(1), lax(6), lax(5), expiry)
	if err != nil {
		t.Fatal(err)
	}
	csigA, _ := protocol.NewKeySigner(e.alicePriv).SignDigest(cdig)
	csigB, _ := protocol.NewKeySigner(e.bobPriv).SignDigest(cdig)
	if _, err := e.contract.CooperativeClose(e.bobAuth, big.NewInt(1), lax(6), lax(5), expiry, csigA, csigB); err != nil {
		t.Fatal(err)
	}
	e.commit(3)
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	meta, _ := e.store.Meta(e.key)
	if meta.Status != proofstore.StatusSettled || meta.FrozenUntilBlock != 0 || meta.PendingClose != nil {
		t.Fatalf("coop close not reconciled: %+v", meta)
	}
}
