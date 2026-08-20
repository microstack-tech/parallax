package nostrmod

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

var backupKey = proofstore.ChannelKey{
	ChainID:   "2110",
	Registry:  util.HexToAddress("0x00000000000000000000000000000000000021ff"),
	ChannelID: 7,
}

func seedStore(t *testing.T, latestSeq uint64) *proofstore.Store {
	t.Helper()
	s := openTestStore(t)
	err := s.CreateChannel(proofstore.ChannelMeta{
		Key:             backupKey,
		Role:            proofstore.RoleA,
		Status:          proofstore.StatusOpen,
		PeerNpub:        "peer",
		PeerAddress:     util.HexToAddress("0xbb00000000000000000000000000000000000bb0"),
		ChallengePeriod: 144,
		Poisoned:        true, // flags must survive backup/restore
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 65)
	for i := range sig {
		sig[i] = 0xaa
	}
	complete := proofstore.SignedState{
		Key:             backupKey,
		Seq:             latestSeq,
		TransferredAtoB: proofstore.NewU256(big.NewInt(int64(latestSeq) * 100)),
		TransferredBtoA: proofstore.NewU256(nil),
		LockedAmount:    proofstore.NewU256(nil),
		SigA:            sig,
		SigB:            sig,
	}
	if err := s.PutComplete(complete); err != nil {
		t.Fatal(err)
	}
	pending := complete
	pending.Seq = latestSeq + 1
	pending.SigB = nil
	pending.TransferredAtoB = proofstore.NewU256(big.NewInt(int64(latestSeq)*100 + 5))
	if err := s.PutSelfSigned(pending); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDeposits(backupKey, proofstore.Deposits{
		DepositA:         proofstore.NewU256(big.NewInt(1e18)),
		LastScannedBlock: 123,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBackupRestoreRoundtrip(t *testing.T) {
	src := seedStore(t, 4)
	priv, _ := newPair(t)
	pub := &capturePublisher{accept: true}

	if n, err := PublishBackup(context.Background(), src, pub, priv); err != nil || n != 1 {
		t.Fatalf("publish: %d %v", n, err)
	}

	// The published wrap round-trips through ParseBackup...
	pub.mu.Lock()
	wrap := pub.wraps[0]
	pub.mu.Unlock()
	snap, err := ParseBackup(wrap, priv)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Channels) != 1 {
		t.Fatalf("channels: %d", len(snap.Channels))
	}

	// ...and restores byte-identically into a wiped store.
	dst := openTestStore(t)
	n, err := RestoreSnapshots(dst, []Snapshot{snap})
	if err != nil || n != 1 {
		t.Fatalf("restore: %d %v", n, err)
	}

	meta, err := dst.Meta(backupKey)
	if err != nil || !meta.Poisoned || meta.Role != proofstore.RoleA {
		t.Fatalf("meta: %+v %v", meta, err)
	}
	latest, err := dst.LatestState(backupKey)
	if err != nil || latest.Seq != 4 || latest.TransferredAtoB.BigInt().Int64() != 400 {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	journal, err := dst.SelfSigned(backupKey)
	if err != nil || len(journal) != 1 || journal[0].Seq != 5 {
		t.Fatalf("journal: %+v %v", journal, err)
	}
	dep, err := dst.Deposits(backupKey)
	if err != nil || dep.DepositA.BigInt().Int64() != 1e18 || dep.LastScannedBlock != 123 {
		t.Fatalf("deposits: %+v %v", dep, err)
	}

	// W3 protection resumes: signing different content at the restored
	// journal seq is refused.
	bad := journal[0]
	bad.TransferredAtoB = proofstore.NewU256(big.NewInt(999))
	if err := dst.PutSelfSigned(bad); err == nil {
		t.Fatal("equivocation accepted after restore")
	}
}

func TestRestoreTakesMaxSeqSnapshot(t *testing.T) {
	stale := seedStore(t, 2)
	fresh := seedStore(t, 9)

	snapStale, err := BuildSnapshot(stale, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapFresh, err := BuildSnapshot(fresh, 50) // older wall-clock, higher seq
	if err != nil {
		t.Fatal(err)
	}

	dst := openTestStore(t)
	if _, err := RestoreSnapshots(dst, []Snapshot{snapStale, snapFresh}); err != nil {
		t.Fatal(err)
	}
	latest, err := dst.LatestState(backupKey)
	if err != nil || latest.Seq != 9 {
		t.Fatalf("restored stale snapshot: %+v %v", latest, err)
	}
}

func TestRestoreNeverOverwritesLiveStore(t *testing.T) {
	live := seedStore(t, 9)
	staleSnap, err := BuildSnapshot(seedStore(t, 2), 100)
	if err != nil {
		t.Fatal(err)
	}
	n, err := RestoreSnapshots(live, []Snapshot{staleSnap})
	if err != nil || n != 0 {
		t.Fatalf("restore into live store: %d %v", n, err)
	}
	latest, _ := live.LatestState(backupKey)
	if latest.Seq != 9 {
		t.Fatalf("live state clobbered: %+v", latest)
	}
}

func TestParseBackupRejectsForeignSeal(t *testing.T) {
	src := seedStore(t, 1)
	myPriv, myPub := newPair(t)
	otherPriv, _ := newPair(t)

	// A snapshot sealed by someone else's key, addressed to me.
	snap, err := BuildSnapshot(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := snapJSON(snap)
	rumor := nostr.Event{Kind: KindSelfBackup, Content: content, CreatedAt: 1}
	wrap, err := Wrap(rumor, myPub, otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBackup(wrap, myPriv); err == nil {
		t.Fatal("foreign-sealed backup accepted")
	}
}

func snapJSON(s Snapshot) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}
