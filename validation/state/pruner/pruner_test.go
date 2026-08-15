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

package pruner

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/dbstore"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
)

// Tests that bloomFilterName output round-trips through isBloomFilter and
// that the embedded state root is recovered intact.
func TestBloomFilterNameRoundTrip(t *testing.T) {
	hashes := []util.Hash{
		{},
		util.HexToHash("0xdeadbeef"),
		util.HexToHash("0x0102030405060708091011121314151617181920212223242526272829303132"),
		util.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
	}
	for _, hash := range hashes {
		name := bloomFilterName(filepath.Join("some", "datadir"), hash)
		ok, recovered := isBloomFilter(name)
		if !ok {
			t.Errorf("filename %q not recognized as bloom filter", name)
			continue
		}
		if recovered != hash {
			t.Errorf("filename %q: recovered hash %x, want %x", name, recovered, hash)
		}
	}
}

// Tests that filenames not produced by bloomFilterName are rejected.
func TestIsBloomFilterRejectsJunk(t *testing.T) {
	junk := []string{
		"",
		"foo",
		"statebloom",
		"bf.gz",
		"statebloom.0xdeadbeef",           // missing suffix
		"foo.0xdeadbeef.bf.gz",            // wrong prefix
		"statebloom.0xdeadbeef.bf.gz.tmp", // in-flight temp file
		"chaindata",
		"LOCK",
		"nodekey",
	}
	for _, name := range junk {
		if ok, hash := isBloomFilter(name); ok {
			t.Errorf("junk filename %q accepted as bloom filter (hash %x)", name, hash)
		}
	}
}

// Tests findBloomFilter against directories containing zero, one and
// multiple committed bloom filter files.
func TestFindBloomFilter(t *testing.T) {
	// Empty directory: nothing to find, no error.
	dir := t.TempDir()
	path, root, err := findBloomFilter(dir)
	if err != nil {
		t.Fatalf("unexpected error on empty datadir: %v", err)
	}
	if path != "" || root != (util.Hash{}) {
		t.Fatalf("empty datadir: got path %q root %x, want empty", path, root)
	}

	// Directory with junk files only: still nothing to find.
	for _, name := range []string{"LOCK", "nodekey", "statebloom.0xdead.bf.gz.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path, root, err = findBloomFilter(dir)
	if err != nil {
		t.Fatalf("unexpected error on junk-only datadir: %v", err)
	}
	if path != "" || root != (util.Hash{}) {
		t.Fatalf("junk-only datadir: got path %q root %x, want empty", path, root)
	}

	// Single bloom file next to the junk: it is found and the root recovered.
	hashA := util.HexToHash("0x0100000000000000000000000000000000000000000000000000000000000000")
	nameA := bloomFilterName(dir, hashA)
	if err := os.WriteFile(nameA, []byte("bloom"), 0600); err != nil {
		t.Fatal(err)
	}
	path, root, err = findBloomFilter(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != nameA {
		t.Fatalf("got path %q, want %q", path, nameA)
	}
	if root != hashA {
		t.Fatalf("got root %x, want %x", root, hashA)
	}

	// Multiple bloom files: filepath.Walk visits in lexical order, so the
	// lexicographically last filename wins.
	hashB := util.HexToHash("0xff00000000000000000000000000000000000000000000000000000000000000")
	nameB := bloomFilterName(dir, hashB)
	if err := os.WriteFile(nameB, []byte("bloom"), 0600); err != nil {
		t.Fatal(err)
	}
	path, root, err = findBloomFilter(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != nameB {
		t.Fatalf("got path %q, want %q", path, nameB)
	}
	if root != hashB {
		t.Fatalf("got root %x, want %x", root, hashB)
	}
}

// Tests the state bloom Put/Contain semantics for trie node keys and
// contract code keys, and the rejection of malformed keys.
func TestStateBloomPutContain(t *testing.T) {
	bloom, err := newStateBloomWithSize(1)
	if err != nil {
		t.Fatal(err)
	}
	// Plain 32-byte trie node hash.
	nodeHash := util.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	if err := bloom.Put(nodeHash.Bytes(), nil); err != nil {
		t.Fatalf("failed to put trie node key: %v", err)
	}
	if ok, _ := bloom.Contain(nodeHash.Bytes()); !ok {
		t.Fatal("trie node key not contained after Put")
	}
	// New-scheme contract code key: the code hash is stripped and stored.
	codeHash := util.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	codeKey := append(append([]byte{}, rawdb.CodePrefix...), codeHash.Bytes()...)
	if err := bloom.Put(codeKey, nil); err != nil {
		t.Fatalf("failed to put code key: %v", err)
	}
	if ok, _ := bloom.Contain(codeHash.Bytes()); !ok {
		t.Fatal("code hash not contained after putting code key")
	}
	// Unrelated key must (almost surely) not be contained.
	other := util.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333")
	if ok, _ := bloom.Contain(other.Bytes()); ok {
		t.Fatal("unrelated key reported as contained")
	}
	// Malformed keys are rejected.
	for _, key := range [][]byte{nil, {0x01}, make([]byte, 31), make([]byte, 33)} {
		if err := bloom.Put(key, nil); err == nil {
			t.Errorf("expected error putting malformed key of length %d", len(key))
		}
	}
}

// Tests that a committed bloom filter can be reloaded from disk with its
// content intact, and that the temporary file is cleaned up by the rename.
func TestStateBloomCommitReload(t *testing.T) {
	bloom, err := newStateBloomWithSize(1)
	if err != nil {
		t.Fatal(err)
	}
	keys := []util.Hash{
		util.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		util.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
	}
	for _, key := range keys {
		if err := bloom.Put(key.Bytes(), nil); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	root := util.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	filename := bloomFilterName(dir, root)
	if err := bloom.Commit(filename, filename+stateBloomFileTempSuffix); err != nil {
		t.Fatalf("failed to commit bloom filter: %v", err)
	}
	if _, err := os.Stat(filename + stateBloomFileTempSuffix); !os.IsNotExist(err) {
		t.Fatal("temporary bloom file still present after commit")
	}
	// The committed file is discoverable through findBloomFilter.
	path, found, err := findBloomFilter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != filename || found != root {
		t.Fatalf("committed bloom not found: got path %q root %x", path, found)
	}
	// Reload from disk and verify the content survived.
	reloaded, err := NewStateBloomFromDisk(filename)
	if err != nil {
		t.Fatalf("failed to reload bloom filter: %v", err)
	}
	for _, key := range keys {
		if ok, _ := reloaded.Contain(key.Bytes()); !ok {
			t.Errorf("key %x lost across commit/reload", key)
		}
	}
	other := util.HexToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	if ok, _ := reloaded.Contain(other.Bytes()); ok {
		t.Error("unrelated key reported as contained after reload")
	}
}

// Tests that extractGenesis commits the genesis state entries into the
// bloom filter, including contract code hashes.
func TestExtractGenesis(t *testing.T) {
	var (
		db    = rawdb.NewMemoryDatabase()
		code  = []byte{0x60, 0x00, 0x60, 0x00, 0xfd} // PUSH0 PUSH0 REVERT
		gspec = &validation.Genesis{
			Alloc: validation.GenesisAlloc{
				util.HexToAddress("0x0000000000000000000000000000000000000001"): {
					Balance: big.NewInt(1000000),
				},
				util.HexToAddress("0x0000000000000000000000000000000000000002"): {
					Balance: big.NewInt(1),
					Code:    code,
					Storage: map[util.Hash]util.Hash{
						{0x01}: {0x02},
					},
				},
			},
		}
	)
	block := gspec.MustCommit(db)

	bloom, err := newStateBloomWithSize(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := extractGenesis(db, bloom); err != nil {
		t.Fatalf("extractGenesis failed: %v", err)
	}
	// The genesis state root node must be marked as live.
	if ok, _ := bloom.Contain(block.Root().Bytes()); !ok {
		t.Fatal("genesis state root not contained in bloom")
	}
	// The contract code hash must be marked as live.
	codeHash := crypto.Keccak256(code)
	if ok, _ := bloom.Contain(codeHash); !ok {
		t.Fatal("genesis contract code hash not contained in bloom")
	}
	// An unrelated hash must (almost surely) not be contained.
	other := util.HexToHash("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	if ok, _ := bloom.Contain(other.Bytes()); ok {
		t.Fatal("unrelated hash reported as contained")
	}
}

// Tests that extractGenesis fails cleanly when the genesis is absent or
// only partially present.
func TestExtractGenesisMissing(t *testing.T) {
	bloom, err := newStateBloomWithSize(1)
	if err != nil {
		t.Fatal(err)
	}
	// Entirely empty database: no canonical genesis hash.
	db := rawdb.NewMemoryDatabase()
	if err := extractGenesis(db, bloom); err == nil {
		t.Fatal("expected error for missing genesis hash")
	}
	// Canonical hash present but the block body is missing.
	db = rawdb.NewMemoryDatabase()
	rawdb.WriteCanonicalHash(db, util.HexToHash("0xdeadbeef"), 0)
	if err := extractGenesis(db, bloom); err == nil {
		t.Fatal("expected error for missing genesis block")
	}
}

// Tests that RecoverPruning is a no-op when the datadir carries no
// committed bloom filter, leaving the database untouched.
func TestRecoverPruningNoop(t *testing.T) {
	var (
		db    = rawdb.NewMemoryDatabase()
		gspec = &validation.Genesis{
			Alloc: validation.GenesisAlloc{
				util.HexToAddress("0x0000000000000000000000000000000000000001"): {
					Balance: big.NewInt(1000000),
				},
			},
		}
	)
	block := gspec.MustCommit(db)

	datadir := t.TempDir()
	// Junk and in-flight temp files must not trigger a recovery.
	for _, name := range []string{"LOCK", "statebloom.0xdead.bf.gz.tmp"} {
		if err := os.WriteFile(filepath.Join(datadir, name), []byte("junk"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := countDatabaseEntries(t, db)
	if err := RecoverPruning(datadir, db, filepath.Join(datadir, "triecache")); err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
	after := countDatabaseEntries(t, db)
	if before != after {
		t.Fatalf("database modified by no-op recovery: %d entries before, %d after", before, after)
	}
	if head := rawdb.ReadHeadBlock(db); head == nil || head.Hash() != block.Hash() {
		t.Fatal("head block lost after no-op recovery")
	}
}

// Tests that the pruner constructor rejects a database without a head
// block instead of panicking.
func TestNewPrunerMissingHeadBlock(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	pruner, err := NewPruner(db, t.TempDir(), "", 256)
	if err == nil {
		t.Fatal("expected error for missing head block")
	}
	if pruner != nil {
		t.Fatal("expected nil pruner on error")
	}
}

// countDatabaseEntries returns the number of key-value pairs in the database.
func countDatabaseEntries(t *testing.T, db dbstore.Database) int {
	t.Helper()
	it := db.NewIterator(nil, nil)
	defer it.Release()

	var count int
	for it.Next() {
		count++
	}
	if err := it.Error(); err != nil {
		t.Fatal(err)
	}
	return count
}
