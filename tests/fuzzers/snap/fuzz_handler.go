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

package snap

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"
	"time"

	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/kernel/xhash"
	"github.com/ParallaxProtocol/parallax/net/p2p"
	"github.com/ParallaxProtocol/parallax/net/p2p/enode"
	"github.com/ParallaxProtocol/parallax/node/fullnode/protocols/snap"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
	fuzz "github.com/google/gofuzz"
)

var trieRoot util.Hash

func getChain() *validation.BlockChain {
	db := rawdb.NewMemoryDatabase()
	ga := make(validation.GenesisAlloc, 1000)
	a := make([]byte, 20)
	mkStorage := func(k, v int) (util.Hash, util.Hash) {
		kB := make([]byte, 32)
		vB := make([]byte, 32)
		binary.LittleEndian.PutUint64(kB, uint64(k))
		binary.LittleEndian.PutUint64(vB, uint64(v))
		return util.BytesToHash(kB), util.BytesToHash(vB)
	}
	storage := make(map[util.Hash]util.Hash)
	for i := 0; i < 10; i++ {
		k, v := mkStorage(i, i)
		storage[k] = v
	}
	for i := 0; i < 1000; i++ {
		binary.LittleEndian.PutUint64(a, uint64(i+0xff))
		acc := validation.GenesisAccount{Balance: big.NewInt(int64(i))}
		if i%2 == 1 {
			acc.Storage = storage
		}
		ga[util.BytesToAddress(a)] = acc
	}
	gspec := validation.Genesis{
		Config: chainparams.TestChainConfig,
		Alloc:  ga,
	}
	genesis := gspec.MustCommit(db)
	blocks, _ := validation.GenerateChain(gspec.Config, genesis, xhash.NewFaker(), db, 2,
		func(i int, gen *validation.BlockGen) {})
	cacheConf := &validation.CacheConfig{
		TrieCleanLimit:      0,
		TrieDirtyLimit:      0,
		TrieTimeLimit:       5 * time.Minute,
		TrieCleanNoPrefetch: true,
		TrieCleanRejournal:  0,
		SnapshotLimit:       100,
		SnapshotWait:        true,
	}
	trieRoot = blocks[len(blocks)-1].Root()
	bc, _ := validation.NewBlockChain(db, cacheConf, gspec.Config, xhash.NewFaker(), script.Config{}, nil, nil)
	if _, err := bc.InsertChain(blocks); err != nil {
		panic(err)
	}
	return bc
}

type dummyBackend struct {
	chain *validation.BlockChain
}

func (d *dummyBackend) Chain() *validation.BlockChain          { return d.chain }
func (d *dummyBackend) RunPeer(*snap.Peer, snap.Handler) error { return nil }
func (d *dummyBackend) PeerInfo(enode.ID) any                  { return "Foo" }
func (d *dummyBackend) Handle(*snap.Peer, snap.Packet) error   { return nil }

type dummyRW struct {
	code       uint64
	data       []byte
	writeCount int
}

func (d *dummyRW) ReadMsg() (p2p.Msg, error) {
	return p2p.Msg{
		Code:       d.code,
		Payload:    bytes.NewReader(d.data),
		ReceivedAt: time.Now(),
		Size:       uint32(len(d.data)),
	}, nil
}

func (d *dummyRW) WriteMsg(msg p2p.Msg) error {
	d.writeCount++
	return nil
}

func doFuzz(input []byte, obj any, code int) int {
	if len(input) > 1024*4 {
		return -1
	}
	bc := getChain()
	defer bc.Stop()
	backend := &dummyBackend{bc}
	fuzz.NewFromGoFuzz(input).Fuzz(obj)
	var data []byte
	switch p := obj.(type) {
	case *snap.GetTrieNodesPacket:
		p.Root = trieRoot
		data, _ = rlp.EncodeToBytes(obj)
	default:
		data, _ = rlp.EncodeToBytes(obj)
	}
	cli := &dummyRW{
		code: uint64(code),
		data: data,
	}
	peer := snap.NewFakePeer(65, "gazonk01", cli)
	err := snap.HandleMessage(backend, peer)
	switch {
	case err == nil && cli.writeCount != 1:
		panic(fmt.Sprintf("Expected 1 response, got %d", cli.writeCount))
	case err != nil && cli.writeCount != 0:
		panic(fmt.Sprintf("Expected 0 response, got %d", cli.writeCount))
	}
	return 1
}

// To run a fuzzer, do
// $ CGO_ENABLED=0 go-fuzz-build -func FuzzTrieNodes
// $ go-fuzz

func FuzzARange(input []byte) int {
	return doFuzz(input, &snap.GetAccountRangePacket{}, snap.GetAccountRangeMsg)
}

func FuzzSRange(input []byte) int {
	return doFuzz(input, &snap.GetStorageRangesPacket{}, snap.GetStorageRangesMsg)
}

func FuzzByteCodes(input []byte) int {
	return doFuzz(input, &snap.GetByteCodesPacket{}, snap.GetByteCodesMsg)
}

func FuzzTrieNodes(input []byte) int {
	return doFuzz(input, &snap.GetTrieNodesPacket{}, snap.GetTrieNodesMsg)
}
