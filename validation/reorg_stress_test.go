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

package validation

import (
	"math/big"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/kernel/xhash"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
)

// Note on chain heaviness: the Nakamoto difficulty rule used by the test chain
// configs keeps difficulty constant between retarget boundaries and, at a
// boundary, derives the new difficulty exclusively from the parent's timestamp
// and epoch start time. Blocks produced by GenerateChain use a fixed 600s
// spacing, so every branch has identical per-height difficulty. A chain is
// therefore strictly heavier if and only if it is longer, which is the same
// mechanism TestChainTxReorgs relies on (its 5-block fork beats the 3-block
// original). The stress tests below make side chains heavier by making them
// longer.

// checkChainInvariants walks the canonical chain backwards from the current
// head to genesis and asserts:
//   - the walk reaches genesis in exactly CurrentBlock().NumberU64() steps
//   - for every height on the walk, the canonical hash mapping agrees with
//     the parent-hash walk, and GetBlockByNumber resolves to the same block
func checkChainInvariants(t *testing.T, bc *BlockChain, tag string) {
	t.Helper()

	head := bc.CurrentBlock()
	if head == nil {
		t.Fatalf("%s: nil current block", tag)
	}
	genesisHash := bc.Genesis().Hash()

	steps := uint64(0)
	maxSteps := head.NumberU64() + 1 // hard bound on the walk
	for cur := head; cur.Hash() != genesisHash; {
		if steps >= maxSteps {
			t.Fatalf("%s: parent walk exceeded %d steps without reaching genesis", tag, maxSteps)
		}
		num := cur.NumberU64()
		if canon := bc.GetCanonicalHash(num); canon != cur.Hash() {
			t.Fatalf("%s: canonical hash mismatch at height %d: walk has %x, index has %x", tag, num, cur.Hash(), canon)
		}
		if blk := bc.GetBlockByNumber(num); blk == nil || blk.Hash() != cur.Hash() {
			t.Fatalf("%s: GetBlockByNumber(%d) does not match canonical walk", tag, num)
		}
		parent := bc.GetBlockByHash(cur.ParentHash())
		if parent == nil {
			t.Fatalf("%s: missing parent %x of block %d", tag, cur.ParentHash(), num)
		}
		if parent.NumberU64() != num-1 {
			t.Fatalf("%s: parent of block %d has number %d", tag, num, parent.NumberU64())
		}
		cur = parent
		steps++
	}
	if steps != head.NumberU64() {
		t.Fatalf("%s: reached genesis in %d steps, want %d", tag, steps, head.NumberU64())
	}
	if canon := bc.GetCanonicalHash(0); canon != genesisHash {
		t.Fatalf("%s: canonical hash at 0 is %x, want genesis %x", tag, canon, genesisHash)
	}
}

// TestStressDeepReorg builds a 256-block canonical chain carrying transactions,
// then reorgs it onto a 260-block side chain forking at block 8 (new head at
// height 268). It verifies head switch, total difficulty growth, canonical
// hash mappings on both sides of the fork point, and transaction lookup and
// receipt cleanup for the dropped chain.
func TestStressDeepReorg(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	var (
		key1, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		key2, _ = crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
		addr1   = crypto.PubkeyToAddress(key1.PublicKey)
		addr2   = crypto.PubkeyToAddress(key2.PublicKey)
		db      = rawdb.NewMemoryDatabase()
		gspec   = &Genesis{
			Config:   chainparams.TestChainConfig,
			GasLimit: 3141592,
			Alloc: GenesisAlloc{
				addr1: {Balance: big.NewInt(1000000000000000000)},
				addr2: {Balance: big.NewInt(1000000000000000000)},
			},
		}
		genesis = gspec.MustCommit(db)
		signer  = types.LatestSigner(gspec.Config)
	)
	const (
		canonLen = 256
		forkAt   = 8   // side chain forks off the canonical block at this height
		sideLen  = 260 // side head lands at height forkAt+sideLen = 268 > 256
	)

	// Canonical chain: one tx from addr1 every 16th block. All tx-bearing
	// blocks are above the fork point, so every canonical tx is dropped by
	// the reorg.
	var canonTxs []*types.Transaction
	canonChain, _ := GenerateChain(gspec.Config, genesis, xhash.NewFaker(), db, canonLen, func(i int, gen *BlockGen) {
		if (i+1)%16 == 0 {
			tx, err := types.SignTx(types.NewTransaction(gen.TxNonce(addr1), addr1, big.NewInt(1000), chainparams.TxGas, gen.header.BaseFee, nil), signer, key1)
			if err != nil {
				t.Fatalf("failed to sign canonical tx: %v", err)
			}
			gen.AddTx(tx)
			canonTxs = append(canonTxs, tx)
		}
	})

	blockchain, err := NewBlockChain(db, nil, gspec.Config, xhash.NewFaker(), script.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create blockchain: %v", err)
	}
	defer blockchain.Stop()

	if n, err := blockchain.InsertChain(canonChain); err != nil {
		t.Fatalf("failed to insert canonical chain at block %d: %v", n, err)
	}
	oldHead := blockchain.CurrentBlock()
	if oldHead.NumberU64() != canonLen {
		t.Fatalf("canonical head at %d, want %d", oldHead.NumberU64(), canonLen)
	}
	oldTd := blockchain.GetTd(oldHead.Hash(), oldHead.NumberU64())
	if oldTd == nil {
		t.Fatal("missing total difficulty for canonical head")
	}
	// Canonical tx lookups must exist before the reorg.
	for i, tx := range canonTxs {
		if txn, _, _, _ := rawdb.ReadTransaction(db, tx.Hash()); txn == nil {
			t.Fatalf("canonical tx %d: lookup missing before reorg", i)
		}
	}

	// Side chain: forks at canonical block #forkAt, carries txs from addr2.
	var sideTxs []*types.Transaction
	sideChain, _ := GenerateChain(gspec.Config, canonChain[forkAt-1], xhash.NewFaker(), db, sideLen, func(i int, gen *BlockGen) {
		number := uint64(forkAt + i + 1)
		if number%16 == 0 {
			tx, err := types.SignTx(types.NewTransaction(gen.TxNonce(addr2), addr2, big.NewInt(1000), chainparams.TxGas, gen.header.BaseFee, nil), signer, key2)
			if err != nil {
				t.Fatalf("failed to sign side tx: %v", err)
			}
			gen.AddTx(tx)
			sideTxs = append(sideTxs, tx)
		}
	})
	if n, err := blockchain.InsertChain(sideChain); err != nil {
		t.Fatalf("failed to insert side chain at block %d: %v", n, err)
	}

	// Head must have switched to the side chain tip.
	newHead := blockchain.CurrentBlock()
	sideTip := sideChain[len(sideChain)-1]
	if newHead.Hash() != sideTip.Hash() {
		t.Fatalf("head not switched: have %x at %d, want side tip %x at %d",
			newHead.Hash(), newHead.NumberU64(), sideTip.Hash(), sideTip.NumberU64())
	}
	// Total difficulty of the new head must exceed the old head's.
	newTd := blockchain.GetTd(newHead.Hash(), newHead.NumberU64())
	if newTd == nil {
		t.Fatal("missing total difficulty for new head")
	}
	if newTd.Cmp(oldTd) <= 0 {
		t.Fatalf("new head td %v not greater than old head td %v", newTd, oldTd)
	}
	// Canonical mapping below and at the fork point must still be the shared
	// prefix; above it, every height must map to the side chain.
	for h := uint64(1); h <= uint64(forkAt); h++ {
		if canon := blockchain.GetCanonicalHash(h); canon != canonChain[h-1].Hash() {
			t.Fatalf("height %d: canonical hash %x, want shared prefix %x", h, canon, canonChain[h-1].Hash())
		}
	}
	for i, block := range sideChain {
		h := uint64(forkAt + i + 1)
		if canon := blockchain.GetCanonicalHash(h); canon != block.Hash() {
			t.Fatalf("height %d: canonical hash %x, want side chain %x", h, canon, block.Hash())
		}
	}
	// Heights beyond the new head must not map to anything.
	if canon := blockchain.GetCanonicalHash(uint64(forkAt+sideLen) + 1); canon != (util.Hash{}) {
		t.Fatalf("height beyond head still canonical: %x", canon)
	}
	// Tx lookups and receipts of the dropped chain must be gone; the side
	// chain's must exist.
	for i, tx := range canonTxs {
		if txn, _, _, _ := rawdb.ReadTransaction(db, tx.Hash()); txn != nil {
			t.Errorf("dropped tx %d: lookup still present after reorg", i)
		}
		if rcpt, _, _, _ := rawdb.ReadReceipt(db, tx.Hash(), blockchain.Config()); rcpt != nil {
			t.Errorf("dropped tx %d: receipt still present after reorg", i)
		}
	}
	for i, tx := range sideTxs {
		if txn, _, _, _ := rawdb.ReadTransaction(db, tx.Hash()); txn == nil {
			t.Errorf("side tx %d: lookup missing after reorg", i)
		}
		if rcpt, _, _, _ := rawdb.ReadReceipt(db, tx.Hash(), blockchain.Config()); rcpt == nil {
			t.Errorf("side tx %d: receipt missing after reorg", i)
		}
	}
	checkChainInvariants(t, blockchain, "deep reorg")
}

// TestStressRepeatedShallowReorgs performs 100 flip-flop iterations. Each
// iteration extends the current head with a 2-block branch A, then reorgs it
// out with a heavier 3-block branch B built on the same parent, asserting the
// head after every insertion. Afterwards it verifies the goroutine count
// returned to its starting level (bounded retry, no long sleeps).
func TestStressRepeatedShallowReorgs(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	startGoroutines := runtime.NumGoroutine()

	db := rawdb.NewMemoryDatabase()
	genesis := (&Genesis{Config: chainparams.TestChainConfig, BaseFee: big.NewInt(chainparams.InitialBaseFee)}).MustCommit(db)

	blockchain, err := NewBlockChain(db, nil, chainparams.TestChainConfig, xhash.NewFaker(), script.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create blockchain: %v", err)
	}

	// Small deterministic base chain so the fork parent is not genesis.
	base := makeBlockChain(genesis, 4, xhash.NewFaker(), db, 11)
	if _, err := blockchain.InsertChain(base); err != nil {
		blockchain.Stop()
		t.Fatalf("failed to insert base chain: %v", err)
	}

	const iterations = 100
	parent := blockchain.CurrentBlock()
	for it := 0; it < iterations; it++ {
		// Branch A: 2 blocks on the current head. It extends the head, so it
		// must become canonical.
		branchA := makeBlockChain(parent, 2, xhash.NewFaker(), db, 101)
		if n, err := blockchain.InsertChain(branchA); err != nil {
			blockchain.Stop()
			t.Fatalf("iter %d: failed to insert branch A at block %d: %v", it, n, err)
		}
		if head := blockchain.CurrentBlock(); head.Hash() != branchA[1].Hash() {
			blockchain.Stop()
			t.Fatalf("iter %d: head %x after branch A, want %x", it, head.Hash(), branchA[1].Hash())
		}
		// Branch B: 3 blocks on the same parent. One block longer at equal
		// per-height difficulty, hence strictly heavier: it must reorg A out.
		branchB := makeBlockChain(parent, 3, xhash.NewFaker(), db, 202)
		if n, err := blockchain.InsertChain(branchB); err != nil {
			blockchain.Stop()
			t.Fatalf("iter %d: failed to insert branch B at block %d: %v", it, n, err)
		}
		if head := blockchain.CurrentBlock(); head.Hash() != branchB[2].Hash() {
			blockchain.Stop()
			t.Fatalf("iter %d: head %x after branch B, want %x", it, head.Hash(), branchB[2].Hash())
		}
		// The evicted branch A blocks must no longer be canonical.
		if canon := blockchain.GetCanonicalHash(branchA[0].NumberU64()); canon == branchA[0].Hash() {
			blockchain.Stop()
			t.Fatalf("iter %d: evicted branch A block still canonical at %d", it, branchA[0].NumberU64())
		}
		parent = blockchain.CurrentBlock()
	}
	checkChainInvariants(t, blockchain, "shallow reorgs")
	blockchain.Stop()

	// Goroutine count must settle back near the starting level. Retry in
	// small bounded steps instead of one long sleep.
	const tolerance = 4
	var final int
	for i := 0; i < 100; i++ {
		final = runtime.NumGoroutine()
		if final <= startGoroutines+tolerance {
			break
		}
		runtime.GC()
		time.Sleep(2 * time.Millisecond)
	}
	if final > startGoroutines+tolerance {
		t.Errorf("goroutine leak: started with %d, ended with %d (tolerance %d)", startGoroutines, final, tolerance)
	}
}

// TestStressSideChainImportBattery imports 50 seeded-random side chains (some
// shorter and some longer than the canonical chain) forking off a 64-block
// canonical chain at random heights, and verifies chain invariants after each
// import: the parent walk from the head reaches genesis in exactly
// head.Number steps and the canonical number-to-hash index matches the walk
// at every height.
func TestStressSideChainImportBattery(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	db := rawdb.NewMemoryDatabase()
	genesis := (&Genesis{Config: chainparams.TestChainConfig, BaseFee: big.NewInt(chainparams.InitialBaseFee)}).MustCommit(db)

	blockchain, err := NewBlockChain(db, nil, chainparams.TestChainConfig, xhash.NewFaker(), script.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create blockchain: %v", err)
	}
	defer blockchain.Stop()

	const canonLen = 64
	canonChain := makeBlockChain(genesis, canonLen, xhash.NewFaker(), db, 1)
	if _, err := blockchain.InsertChain(canonChain); err != nil {
		t.Fatalf("failed to insert canonical chain: %v", err)
	}
	checkChainInvariants(t, blockchain, "canonical")

	// parentAt returns the block of the ORIGINAL canonical chain at a height.
	parentAt := func(h int) *types.Block {
		if h == 0 {
			return genesis
		}
		return canonChain[h-1]
	}

	rng := rand.New(rand.NewSource(0x5EED5))
	const sidechains = 50
	for k := 0; k < sidechains; k++ {
		forkHeight := 1 + rng.Intn(canonLen-1) // in [1, 63]
		length := 1 + rng.Intn(80)             // tip heights in [forkHeight+1, forkHeight+80]

		// Unique coinbase seed per side chain so every generated block is
		// distinct across the battery.
		seed := 100 + k
		side, _ := GenerateChain(chainparams.TestChainConfig, parentAt(forkHeight), xhash.NewFaker(), db, length, func(i int, b *BlockGen) {
			b.SetCoinbase(util.Address{0: byte(seed), 18: byte(k), 19: byte(i)})
		})
		if n, err := blockchain.InsertChain(side); err != nil {
			t.Fatalf("side chain %d (fork %d, len %d): insert failed at block %d: %v", k, forkHeight, length, n, err)
		}

		// A side chain strictly heavier than the current head must have
		// become the head. (Equal-TD ties at equal height are broken by a
		// coin flip in ForkChoice, so only assert the strict case.)
		tip := side[len(side)-1]
		head := blockchain.CurrentBlock()
		tipTd := blockchain.GetTd(tip.Hash(), tip.NumberU64())
		headTd := blockchain.GetTd(head.Hash(), head.NumberU64())
		if tipTd == nil || headTd == nil {
			t.Fatalf("side chain %d: missing td (tip %v, head %v)", k, tipTd, headTd)
		}
		if tipTd.Cmp(headTd) > 0 {
			t.Fatalf("side chain %d: tip td %v exceeds head td %v but head is %x", k, tipTd, headTd, head.Hash())
		}
		checkChainInvariants(t, blockchain, "battery")
	}
}
