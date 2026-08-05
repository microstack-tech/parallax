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

package protocol

import (
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/node/fullnode/downloader"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/protocols/prl"
	"github.com/ParallaxProtocol/parallax/p2p/protocols/snap"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation/forkid"
)

// Tests that snap sync is disabled after a successful sync cycle.
func TestSnapSyncDisabling66(t *testing.T) { testSnapSyncDisabling(t, prl.Parallax66, snap.SNAP1) }

// Tests that snap sync gets disabled as soon as a real block is successfully
// imported into the blockchain.
func testSnapSyncDisabling(t *testing.T, prlVer uint, snapVer uint) {
	t.Parallel()

	// Create an empty handler and ensure it's in snap sync mode
	empty := newTestHandler()
	if atomic.LoadUint32(&empty.handler.snapSync) == 0 {
		t.Fatalf("snap sync disabled on pristine blockchain")
	}
	defer empty.close()

	// Create a full handler and ensure snap sync ends up disabled
	full := newTestHandlerWithBlocks(1024)
	if atomic.LoadUint32(&full.handler.snapSync) == 1 {
		t.Fatalf("snap sync not disabled on non-empty blockchain")
	}
	defer full.close()

	// Sync up the two handlers via both `parallax` and `parallax-snap`
	caps := []p2p.Cap{{Name: "parallax", Version: prlVer}, {Name: "parallax-snap", Version: snapVer}}

	emptyPipeEth, fullPipeEth := p2p.MsgPipe()
	defer emptyPipeEth.Close()
	defer fullPipeEth.Close()

	emptyPeerEth := prl.NewPeer(prlVer, p2p.NewPeer(enode.ID{1}, "", caps), emptyPipeEth, empty.txpool)
	fullPeerEth := prl.NewPeer(prlVer, p2p.NewPeer(enode.ID{2}, "", caps), fullPipeEth, full.txpool)
	defer emptyPeerEth.Close()
	defer fullPeerEth.Close()

	go empty.handler.runParallaxPeer(emptyPeerEth, func(peer *prl.Peer) error {
		return prl.Handle((*prlHandler)(empty.handler), peer)
	})
	go full.handler.runParallaxPeer(fullPeerEth, func(peer *prl.Peer) error {
		return prl.Handle((*prlHandler)(full.handler), peer)
	})

	emptyPipeSnap, fullPipeSnap := p2p.MsgPipe()
	defer emptyPipeSnap.Close()
	defer fullPipeSnap.Close()

	emptyPeerSnap := snap.NewPeer(snapVer, p2p.NewPeer(enode.ID{1}, "", caps), emptyPipeSnap)
	fullPeerSnap := snap.NewPeer(snapVer, p2p.NewPeer(enode.ID{2}, "", caps), fullPipeSnap)

	go empty.handler.runSnapExtension(emptyPeerSnap, func(peer *snap.Peer) error {
		return snap.Handle((*snapHandler)(empty.handler), peer)
	})
	go full.handler.runSnapExtension(fullPeerSnap, func(peer *snap.Peer) error {
		return snap.Handle((*snapHandler)(full.handler), peer)
	})
	// Wait a bit for the above handlers to start
	time.Sleep(250 * time.Millisecond)

	// Check that snap sync was disabled
	op := peerToSyncOp(downloader.SnapSync, empty.handler.peers.peerWithHighestTD())
	if err := empty.handler.doSync(op); err != nil {
		t.Fatal("sync failed:", err)
	}
	if atomic.LoadUint32(&empty.handler.snapSync) == 1 {
		t.Fatalf("snap sync not disabled after successful synchronisation")
	}
}

// Tests that the initial pool announcement at registration respects the
// tx-relay class of the link: a block-relay-only peer must receive no
// NewPooledTransactionHashes for the pending pool, while a full-relay
// peer must.
func TestSyncTransactionsSkipsBlockRelay66(t *testing.T) {
	testSyncTransactionsRelayGate(t, prl.Parallax66, true)
}

func TestSyncTransactionsAnnouncesFullRelay66(t *testing.T) {
	testSyncTransactionsRelayGate(t, prl.Parallax66, false)
}

func testSyncTransactionsRelayGate(t *testing.T, protocol uint, blockRelay bool) {
	t.Parallel()

	handler := newTestHandler()
	defer handler.close()

	// Seed the pool so registration has something to announce.
	tx := types.NewTransaction(0, util.Address{}, big.NewInt(0), 100000, big.NewInt(0), nil)
	tx, _ = types.SignTx(tx, types.HomesteadSigner{}, testKey)
	handler.txpool.AddRemotes([]*types.Transaction{tx})

	p2pSrc, p2pSink := p2p.MsgPipe()
	defer p2pSrc.Close()
	defer p2pSink.Close()

	sinkP2P := p2p.NewPeerPipe(enode.ID{2}, "", nil, p2pSink)
	if blockRelay {
		sinkP2P.SetBlockRelayOnly(true)
		sinkP2P.SetRelayTxs(false)
	}
	src := prl.NewPeer(protocol, p2p.NewPeerPipe(enode.ID{1}, "", nil, p2pSrc), p2pSrc, handler.txpool)
	sink := prl.NewPeer(protocol, sinkP2P, p2pSink, handler.txpool)
	defer src.Close()
	defer sink.Close()

	go handler.handler.runParallaxPeer(sink, func(peer *prl.Peer) error {
		return prl.Handle((*prlHandler)(handler.handler), peer)
	})
	var (
		genesis = handler.chain.Genesis()
		head    = handler.chain.CurrentBlock()
		td      = handler.chain.GetTd(head.Hash(), head.NumberU64())
	)
	if err := src.Handshake(1, td, head.Hash(), genesis.Hash(), forkid.NewIDWithChain(handler.chain), forkid.NewFilter(handler.chain)); err != nil {
		t.Fatalf("failed to run protocol handshake: %v", err)
	}
	msgs := make(chan p2p.Msg, 1)
	go func() {
		if msg, err := p2pSrc.ReadMsg(); err == nil {
			msgs <- msg
		}
	}()
	if blockRelay {
		select {
		case msg := <-msgs:
			t.Fatalf("block-relay-only peer received message %#x at registration", msg.Code)
		case <-time.After(250 * time.Millisecond):
		}
	} else {
		select {
		case msg := <-msgs:
			if msg.Code != prl.NewPooledTransactionHashesMsg {
				t.Fatalf("unexpected message %#x, want NewPooledTransactionHashes", msg.Code)
			}
		case <-time.After(time.Second):
			t.Fatal("full-relay peer received no pool announcement")
		}
	}
}
