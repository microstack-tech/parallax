// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package disc

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/simulations/pipes"
)

// helper: build a backend with a deterministic relayKey so tests can
// reproduce picks across runs. Production rolls a fresh key from
// crypto/rand on construction.
func newRelayBackendForTest(t *testing.T, key [32]byte) *AddrmanBackend {
	t.Helper()
	m, err := addrman.New(addrman.Deterministic(99))
	if err != nil {
		t.Fatal(err)
	}
	b := NewAddrmanBackend(m, nil, nil, nil, nil)
	b.relayKey = key
	return b
}

func makeCandidates(n int) map[PeerKey]*peerRelayState {
	out := make(map[PeerKey]*peerRelayState, n)
	for i := 0; i < n; i++ {
		key := PeerKey(fmt.Sprintf("peer-%02d", i))
		ch := make(chan PeerEntry, 1)
		out[key] = &peerRelayState{outbox: ch}
	}
	return out
}

// TestPickRelayPeersDeterministic — same (relayKey, addrHash,
// dayBucket) yields the same pick across calls. Locks down the
// determinism property RelayAddress depends on for the m_addr_known
// dedup to work across the day window.
func TestPickRelayPeersDeterministic(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i)
	}
	cands := makeCandidates(8)
	picksA, fanoutA := pickRelayPeers(key[:], 0xCAFE, 100, true, cands)
	picksB, fanoutB := pickRelayPeers(key[:], 0xCAFE, 100, true, cands)

	if fanoutA != fanoutB {
		t.Fatalf("fanout differs: %d vs %d", fanoutA, fanoutB)
	}
	if len(picksA) != len(picksB) {
		t.Fatalf("pick length differs: %d vs %d", len(picksA), len(picksB))
	}
	for i := range picksA {
		if picksA[i] != picksB[i] {
			t.Fatalf("pick[%d] differs: %q vs %q", i, picksA[i], picksB[i])
		}
	}
}

// TestPickRelayPeersDayRotation — the same (addr, peer set, key)
// across day-bucket transitions must produce DIFFERENT picks at least
// once. Catches a degenerate PRF that ignores the day input.
func TestPickRelayPeersDayRotation(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = 0xA0
	}
	cands := makeCandidates(8)
	prev, _ := pickRelayPeers(key[:], 0x1234, 1, true, cands)
	differed := false
	for day := uint64(2); day < 32; day++ {
		picks, _ := pickRelayPeers(key[:], 0x1234, day, true, cands)
		for i := range picks {
			if picks[i] != prev[i] {
				differed = true
				break
			}
		}
		if differed {
			break
		}
	}
	if !differed {
		t.Fatal("pick set never changed across 30 day-bucket transitions; PRF likely ignores the day input")
	}
}

// TestPickRelayPeersFanout — reachable always picks 2; unreachable
// picks 1 or 2 by deterministic coin flip. Covers the
// "fReachable || (hasher.Finalize() & 1)" branch from Bitcoin's
// RelayAddress.
func TestPickRelayPeersFanout(t *testing.T) {
	var key [32]byte
	cands := makeCandidates(10)

	for trial := uint64(0); trial < 64; trial++ {
		picks, fanout := pickRelayPeers(key[:], trial, 0, true, cands)
		if fanout != RelayFanOutMax || len(picks) != RelayFanOutMax {
			t.Fatalf("reachable trial=%d picked %d / fanout %d, want %d both",
				trial, len(picks), fanout, RelayFanOutMax)
		}
	}

	saw1, saw2 := false, false
	for trial := uint64(0); trial < 64; trial++ {
		_, fanout := pickRelayPeers(key[:], trial, 0, false, cands)
		if fanout == 1 {
			saw1 = true
		}
		if fanout == 2 {
			saw2 = true
		}
	}
	if !saw1 || !saw2 {
		t.Fatalf("unreachable fanout never both: saw1=%v saw2=%v", saw1, saw2)
	}
}

// TestPickRelayPeersSmallCandidateSet — fanout caps at len(candidates)
// when the local peer set has fewer than 2 peers. Defends against an
// out-of-range slice index in the picker.
func TestPickRelayPeersSmallCandidateSet(t *testing.T) {
	var key [32]byte
	for size := 0; size <= 3; size++ {
		cands := makeCandidates(size)
		picks, fanout := pickRelayPeers(key[:], 0xDEAD, 0, true, cands)
		if size == 0 {
			if len(picks) != 0 || fanout != 0 {
				t.Fatalf("size=0: got %d picks fanout=%d, want 0/0", len(picks), fanout)
			}
			continue
		}
		want := size
		if want > RelayFanOutMax {
			want = RelayFanOutMax
		}
		if len(picks) != want || fanout != want {
			t.Fatalf("size=%d: picks=%d fanout=%d, want %d", size, len(picks), fanout, want)
		}
	}
}

// TestRelayAddressExcludesOriginator — RelayAddress must never relay
// an address back to the peer it came from. Sybil-resistance: the
// originator's bloom filter would deduplicate anyway, but skipping
// before the wire-write also saves bandwidth.
func TestRelayAddressExcludesOriginator(t *testing.T) {
	var key [32]byte
	b := newRelayBackendForTest(t, key)

	// Build five fake peers each backed by a TCP pipe (so peerKeyFor
	// produces a stable key) and register each with a distinct outbox.
	type fake struct {
		peer *p2p.Peer
		ch   chan PeerEntry
		key  PeerKey
		conn []interface{ Close() error }
	}
	peers := make([]fake, 5)
	defer func() {
		for _, f := range peers {
			for _, c := range f.conn {
				_ = c.Close()
			}
		}
	}()
	for i := range peers {
		a, d, err := pipes.TCPPipe()
		if err != nil {
			t.Fatalf("TCPPipe: %v", err)
		}
		var id enode.ID
		if _, err := rand.Read(id[:]); err != nil {
			t.Fatal(err)
		}
		p := p2p.NewPeerForTest(id, fmt.Sprintf("p%d", i), nil, a)
		ch := make(chan PeerEntry, 4)
		k := peerKeyFor(p)
		peers[i] = fake{peer: p, ch: ch, key: k, conn: []interface{ Close() error }{a, d}}
		b.RegisterPeerOutbox(k, ch)
	}

	entry := PeerEntry{NetworkID: NetIPv4, Addr: []byte{198, 51, 100, 7}, TCPPort: 30303, KeyType: KeyTypeNone}

	originator := peers[0].peer
	b.RelayAddress(originator, entry, true)

	// Originator must not have received it.
	select {
	case got := <-peers[0].ch:
		t.Fatalf("originator received its own relayed entry: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	// Some other peer(s) must have received it (fanout=2 reachable).
	received := 0
	for i := 1; i < len(peers); i++ {
		select {
		case <-peers[i].ch:
			received++
		default:
		}
	}
	if received == 0 {
		t.Fatal("no non-originator peer received the relayed entry")
	}
	if received > RelayFanOutMax {
		t.Fatalf("fanout exceeded: %d > %d", received, RelayFanOutMax)
	}
}

// TestRelayAddressNonBlockingOnFullOutbox — a peer whose outbox is
// full must NOT block RelayAddress. Bitcoin's behavior is to drop
// the relay rather than stall the protocol thread.
func TestRelayAddressNonBlockingOnFullOutbox(t *testing.T) {
	var key [32]byte
	b := newRelayBackendForTest(t, key)

	// Single peer, capacity-0 outbox so any send drops.
	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	p := p2p.NewPeerForTest(id, "p", nil, a)
	ch := make(chan PeerEntry) // unbuffered, no reader
	b.RegisterPeerOutbox(peerKeyFor(p), ch)

	done := make(chan struct{})
	go func() {
		entry := PeerEntry{NetworkID: NetIPv4, Addr: []byte{1, 2, 3, 4}, TCPPort: 30303, KeyType: KeyTypeNone}
		b.RelayAddress(nil, entry, true)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RelayAddress blocked on a full outbox")
	}
}

// TestHandlePeersFansOutNewlyLearned — a Peers message containing a
// brand-new address triggers RelayAddress, which lands in some
// peer's outbox. Duplicate ingest does NOT re-relay (addrman.AddOne
// returns false).
func TestHandlePeersFansOutNewlyLearned(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	b := newRelayBackendForTest(t, key)

	// Build the originator + several relay candidates.
	type fake struct {
		peer *p2p.Peer
		ch   chan PeerEntry
		key  PeerKey
		conn []interface{ Close() error }
	}
	mk := func(name string) fake {
		a, d, err := pipes.TCPPipe()
		if err != nil {
			t.Fatalf("TCPPipe: %v", err)
		}
		var id enode.ID
		if _, err := rand.Read(id[:]); err != nil {
			t.Fatal(err)
		}
		p := p2p.NewPeerForTest(id, name, nil, a)
		ch := make(chan PeerEntry, 4)
		k := peerKeyFor(p)
		return fake{peer: p, ch: ch, key: k, conn: []interface{ Close() error }{a, d}}
	}
	originator := mk("origin")
	candidates := []fake{mk("c1"), mk("c2"), mk("c3"), mk("c4"), mk("c5")}
	defer func() {
		for _, c := range originator.conn {
			_ = c.Close()
		}
		for _, f := range candidates {
			for _, c := range f.conn {
				_ = c.Close()
			}
		}
	}()

	for _, f := range candidates {
		b.RegisterPeerOutbox(f.key, f.ch)
	}
	// Originator also has an outbox, so RelayAddress's exclude-
	// originator path is exercised end-to-end.
	originatorBox := make(chan PeerEntry, 4)
	b.RegisterPeerOutbox(originator.key, originatorBox)

	// Use a publicly-routable address: TEST-NET ranges (203.0.113.0/24,
	// 198.51.100.0/24) are rejected by addrman's IsRoutable filter.
	entry := PeerEntry{
		NetworkID: NetIPv4,
		Addr:      []byte{8, 8, 8, 8},
		TCPPort:   30303,
		KeyType:   KeyTypeNone,
		LastSeen:  uint64(time.Now().Unix()),
	}
	b.HandlePeers(originator.peer, []PeerEntry{entry})

	// Originator outbox stays empty; some candidates received it.
	select {
	case got := <-originatorBox:
		t.Fatalf("originator received its own relayed entry: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	received := 0
	for _, f := range candidates {
		select {
		case <-f.ch:
			received++
		default:
		}
	}
	if received == 0 {
		t.Fatal("HandlePeers did not fan-out a newly-learned address")
	}
	if received > RelayFanOutMax {
		t.Fatalf("fan-out too wide: %d > %d", received, RelayFanOutMax)
	}

	// Re-ingest the same entry: addrman.AddOne returns false (already
	// known), so RelayAddress must NOT fire again.
	for _, f := range candidates {
		// Drain any leftover from the first round.
		select {
		case <-f.ch:
		default:
		}
	}
	b.HandlePeers(originator.peer, []PeerEntry{entry})

	for _, f := range candidates {
		select {
		case got := <-f.ch:
			t.Fatalf("re-relay on duplicate ingest: peer=%s entry=%+v", f.key, got)
		default:
		}
	}
}

// TestRelayAddressEmptyCandidatesNoOp — relaying with no other peers
// connected is a no-op. Defends against a panic on an empty pick.
func TestRelayAddressEmptyCandidatesNoOp(t *testing.T) {
	var key [32]byte
	b := newRelayBackendForTest(t, key)

	entry := PeerEntry{NetworkID: NetIPv4, Addr: []byte{9, 8, 7, 6}, TCPPort: 30303, KeyType: KeyTypeNone}
	b.RelayAddress(nil, entry, true) // must not panic
}

// TestRelayDayBucketRotates — the day-bucket function rolls forward
// once per RelayInterval. The "within" check uses a t0 chosen so the
// (now + addrHash) sum sits mid-bucket; that way subtracting one
// second can't accidentally cross the rollover.
func TestRelayDayBucketRotates(t *testing.T) {
	addrHash := uint64(42)
	intervalSec := uint64(RelayInterval / time.Second)

	// Anchor t0 so (t0.Unix() + addrHash) is exactly at a bucket
	// boundary. Then t0+RelayInterval lands one bucket later, and
	// t0+RelayInterval-1s stays in the same bucket as t0.
	const baseTime = 1_800_000_000 // arbitrary 2027-01-15 UTC-ish
	rem := (baseTime + addrHash) % intervalSec
	t0 := time.Unix(baseTime-int64(rem), 0).UTC()

	bucket0 := relayDayBucket(t0, addrHash)
	bucket1 := relayDayBucket(t0.Add(RelayInterval), addrHash)
	if bucket1 != bucket0+1 {
		t.Fatalf("RelayInterval did not advance bucket: bucket0=%d bucket1=%d", bucket0, bucket1)
	}
	bucketWithin := relayDayBucket(t0.Add(RelayInterval-time.Second), addrHash)
	if bucketWithin != bucket0 {
		t.Fatalf("bucket advanced before RelayInterval: bucket0=%d within=%d", bucket0, bucketWithin)
	}
}

// TestRelayDrainSkipsKnownAddr — if the per-peer known-addr bloom
// already contains the entry's key (e.g., the peer just sent it to
// us), the drain goroutine must NOT re-relay. Bitcoin's m_addr_known
// dedup contract.
func TestRelayDrainSkipsKnownAddr(t *testing.T) {
	a, d, err := pipes.TCPPipe()
	if err != nil {
		t.Fatalf("TCPPipe: %v", err)
	}
	defer a.Close()
	defer d.Close()
	var id enode.ID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	peer := p2p.NewPeerForTest(id, "p", nil, a)

	// We'll wrap a in a MsgPipeRW so we can observe writes without
	// running a full Run goroutine.
	rwInner, rwPeer := p2p.MsgPipe()
	defer rwInner.Close()
	defer rwPeer.Close()

	st := &state{}
	entry := PeerEntry{NetworkID: NetIPv4, Addr: []byte{10, 0, 0, 1}, TCPPort: 30303, KeyType: KeyTypeNone}
	st.knownAddr.Add(addressKey(entry.NetworkID, entry.Addr, entry.TCPPort))

	outbox := make(chan PeerEntry, 1)
	outbox <- entry
	close(outbox)

	done := make(chan struct{})
	go func() {
		runRelayDrain(peer, rwInner, st, outbox, logging.Root())
		close(done)
	}()

	// Read with a deadline; the drain MUST skip the known entry, so
	// no message arrives and the read times out.
	gotMsg := make(chan p2p.Msg, 1)
	gotErr := make(chan error, 1)
	go func() {
		msg, err := rwPeer.ReadMsg()
		if err != nil {
			gotErr <- err
			return
		}
		gotMsg <- msg
	}()

	select {
	case msg := <-gotMsg:
		t.Fatalf("drain re-sent a known entry: code=%d", msg.Code)
	case err := <-gotErr:
		// Pipe closed because outbox closed → drain returned →
		// rwInner closed by defer in Run. Acceptable shutdown shape.
		_ = err
	case <-time.After(200 * time.Millisecond):
		// Outcome we want: drain consumed the entry, didn't write,
		// then closed (no message was emitted).
	}
	<-done
}

