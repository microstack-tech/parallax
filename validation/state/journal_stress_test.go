// Copyright 2026 The Parallax Protocol Authors
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

package state

import (
	"bytes"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
)

// stressSnapshotTest is a heavier variant of snapshotTest (statedb_test.go):
// ~500 journal operations per sequence, 20-50 nested snapshots, and reverts to
// a random strictly decreasing chain of snapshot depths instead of unwinding
// every snapshot. All randomness is drawn from a locally seeded source so
// that sequences are fully deterministic; actions come from the shared
// newTestAction catalogue in statedb_test.go.
type stressSnapshotTest struct {
	addrs     []util.Address // all account addresses actions may touch
	actions   []testAction   // modifications applied to the state
	snapshots []int          // action indexes at which a snapshot is taken
	reverts   []int          // snapshot ordinals to revert to, strictly decreasing
}

// generateStressSnapshotTest builds one deterministic heavy sequence from r.
func generateStressSnapshotTest(r *rand.Rand) *stressSnapshotTest {
	const nactions = 500

	addrs := make([]util.Address, 50)
	for i := range addrs {
		addrs[i][0] = byte(i)
	}
	actions := make([]testAction, nactions)
	for i := range actions {
		addr := addrs[r.Intn(len(addrs))]
		actions[i] = newTestAction(addr, r)
	}
	// Place 20-50 snapshots at distinct random action indexes. None of them
	// is popped while the actions run, so up to 50 revisions are nested at
	// the deepest point.
	nsnapshots := 20 + r.Intn(31)
	seen := make(map[int]bool, nsnapshots)
	snapshots := make([]int, 0, nsnapshots)
	for len(snapshots) < nsnapshots {
		idx := r.Intn(nactions)
		if !seen[idx] {
			seen[idx] = true
			snapshots = append(snapshots, idx)
		}
	}
	sort.Ints(snapshots)

	// Pick a random strictly decreasing chain of snapshot ordinals to revert
	// to. Reverting to snapshot k invalidates every revision taken at or
	// after it (RevertToSnapshot truncates validRevisions), so a decreasing
	// chain is exactly the set of monotonically legal revert orders.
	reverts := make([]int, 0, nsnapshots)
	for k := nsnapshots - 1; k >= 0; k-- {
		if r.Intn(2) == 0 {
			reverts = append(reverts, k)
		}
	}
	if len(reverts) == 0 {
		reverts = append(reverts, nsnapshots-1)
	}
	return &stressSnapshotTest{addrs: addrs, actions: actions, snapshots: snapshots, reverts: reverts}
}

// String renders the sequence for failure reports, in the same layout as
// snapshotTest.String.
func (test *stressSnapshotTest) String() string {
	out := new(bytes.Buffer)
	sindex := 0
	for i, action := range test.actions {
		if len(test.snapshots) > sindex && i == test.snapshots[sindex] {
			fmt.Fprintf(out, "---- snapshot %d ----\n", sindex)
			sindex++
		}
		fmt.Fprintf(out, "%4d: %s\n", i, action.name)
	}
	fmt.Fprintf(out, "revert chain (snapshot ordinals): %v\n", test.reverts)
	return out.String()
}

// run applies all actions while taking the scheduled snapshots, then reverts
// along the chain. After every revert the state must be indistinguishable
// (through all public accessors) from a fresh state that replayed only the
// actions preceding that snapshot. The comparison logic is reused from
// snapshotTest.checkEqual in statedb_test.go, which only consults its addrs
// field.
func (test *stressSnapshotTest) run() error {
	var (
		state, _     = New(util.Hash{}, NewDatabase(rawdb.NewMemoryDatabase()), nil)
		snapshotRevs = make([]int, len(test.snapshots))
		sindex       = 0
	)
	for i, action := range test.actions {
		if len(test.snapshots) > sindex && i == test.snapshots[sindex] {
			snapshotRevs[sindex] = state.Snapshot()
			sindex++
		}
		action.fn(action, state)
	}
	cmp := &snapshotTest{addrs: test.addrs}
	for _, k := range test.reverts {
		state.RevertToSnapshot(snapshotRevs[k])

		checkstate, _ := New(util.Hash{}, state.Database(), nil)
		for _, action := range test.actions[:test.snapshots[k]] {
			action.fn(action, checkstate)
		}
		if err := cmp.checkEqual(state, checkstate); err != nil {
			return fmt.Errorf("state mismatch after revert to snapshot %d (action %d)\n%v",
				k, test.snapshots[k], err)
		}
	}
	return nil
}

// TestStressSnapshotRevertChains runs a fixed number of deterministic heavy
// snapshot/revert sequences (seeded loop rather than quick.Check, so the
// heavier profile does not depend on quick's size parameter and every failure
// is reproducible from the sequence index alone).
func TestStressSnapshotRevertChains(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	const (
		seed       = int64(0x5eed_4a11 ^ 0x2110) // deterministic; 2110 = chain id
		nsequences = 200
	)
	r := rand.New(rand.NewSource(seed))
	for i := range nsequences {
		test := generateStressSnapshotTest(r)
		if err := test.run(); err != nil {
			t.Fatalf("sequence %d (seed %#x): %v\n%s", i, seed, err, test)
		}
	}
}

// TestStressConcurrentReadsDuringCommit exercises the production sharing
// boundary for concurrent state access.
//
// StateDB itself is NOT safe for concurrent use (it has no internal locking
// on its journal, object map, or tries), so this test does not hammer a
// single StateDB from multiple goroutines. The boundary that production
// actually relies on is the state.Database: NewDatabase is documented as
// "safe for concurrent use" (database.go), and BlockChain.StateAt
// (validation/blockchain_reader.go) hands out a fresh StateDB per caller over
// the shared bc.stateCache while block import commits new states through the
// same cache. This test reproduces exactly that: one writer goroutine
// committing successive states (and periodically flushing the shared trie
// database to disk), while reader goroutines each open their own StateDB over
// a frozen committed root and read a disjoint account set through the shared
// Database. The race detector is the primary assertion; read values are also
// checked against the committed expectations.
func TestStressConcurrentReadsDuringCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	const (
		naccounts = 100 // per side (reader set and writer set are disjoint)
		nreaders  = 8
		ncommits  = 40
	)
	db := NewDatabase(rawdb.NewMemoryDatabase())

	readerAddr := func(i int) util.Address {
		return util.Address{0xaa, byte(i >> 8), byte(i)}
	}
	writerAddr := func(i int) util.Address {
		return util.Address{0xbb, byte(i >> 8), byte(i)}
	}
	slotKey := func(i int) util.Hash {
		return util.Hash{0x51, byte(i >> 8), byte(i)}
	}

	// Seed a base state containing the reader accounts and commit it. The
	// resulting root is frozen: nothing ever modifies it again, readers only
	// ever open StateDBs at this root.
	base, err := New(util.Hash{}, db, nil)
	if err != nil {
		t.Fatalf("failed to create base state: %v", err)
	}
	expectBalance := make([]*big.Int, naccounts)
	expectSlot := make([]util.Hash, naccounts)
	expectCode := make([][]byte, naccounts)
	for i := range naccounts {
		addr := readerAddr(i)
		expectBalance[i] = big.NewInt(int64(1000 + i))
		expectSlot[i] = util.Hash{0x0a, byte(i >> 8), byte(i)}
		expectCode[i] = []byte{0xc0, 0xde, byte(i >> 8), byte(i)}

		base.SetBalance(addr, new(big.Int).Set(expectBalance[i]))
		base.SetNonce(addr, uint64(i))
		base.SetState(addr, slotKey(i), expectSlot[i])
		base.SetCode(addr, expectCode[i])
	}
	frozenRoot, err := base.Commit(false)
	if err != nil {
		t.Fatalf("failed to commit base state: %v", err)
	}

	var (
		done    atomic.Bool
		wg      sync.WaitGroup
		readErr = make(chan error, nreaders)
	)

	// Writer: the block-import role. Repeatedly opens a StateDB at the latest
	// root, mutates the disjoint writer accounts, finalises and commits, and
	// periodically flushes the shared trie database to disk, all while the
	// readers below resolve nodes and code through the very same Database.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer done.Store(true)
		root := frozenRoot
		for n := range ncommits {
			st, err := New(root, db, nil)
			if err != nil {
				t.Errorf("writer: failed to open state at %x: %v", root, err)
				return
			}
			for i := range naccounts {
				addr := writerAddr(i)
				st.SetBalance(addr, big.NewInt(int64(n*naccounts+i)))
				st.SetNonce(addr, uint64(n))
				st.SetState(addr, slotKey(i), util.Hash{byte(n), byte(i)})
				code := []byte{0xff, byte(n), byte(i >> 8), byte(i)}
				st.SetCode(addr, code)
			}
			st.Finalise(true)
			st.IntermediateRoot(true)
			newRoot, err := st.Commit(true)
			if err != nil {
				t.Errorf("writer: commit %d failed: %v", n, err)
				return
			}
			if n%8 == 7 {
				if err := db.TrieDB().Commit(newRoot, false, nil); err != nil {
					t.Errorf("writer: triedb commit %d failed: %v", n, err)
					return
				}
			}
			root = newRoot
		}
	}()

	// Readers: the RPC/StateAt role. Each goroutine opens its own StateDB
	// over the frozen root (a fresh one per iteration, matching one StateAt
	// call per served request) and reads the reader-only account set.
	for g := range nreaders {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// Keep reading until the writer is done, with a floor so the
			// test still reads even if the writer finishes first.
			for it := 0; it < 25 || !done.Load(); it++ {
				st, err := New(frozenRoot, db, nil)
				if err != nil {
					readErr <- fmt.Errorf("reader %d: failed to open state: %v", g, err)
					return
				}
				for i := range naccounts {
					addr := readerAddr(i)
					if bal := st.GetBalance(addr); bal.Cmp(expectBalance[i]) != 0 {
						readErr <- fmt.Errorf("reader %d: balance of %x = %v, want %v", g, addr, bal, expectBalance[i])
						return
					}
					if val := st.GetState(addr, slotKey(i)); val != expectSlot[i] {
						readErr <- fmt.Errorf("reader %d: slot of %x = %x, want %x", g, addr, val, expectSlot[i])
						return
					}
					if code := st.GetCode(addr); !bytes.Equal(code, expectCode[i]) {
						readErr <- fmt.Errorf("reader %d: code of %x = %x, want %x", g, addr, code, expectCode[i])
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(readErr)
	for err := range readErr {
		t.Error(err)
	}
}
