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

package rawdb

import (
	"github.com/ParallaxProtocol/parallax/v2/dbstore"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

// ReadPreimage retrieves a single preimage of the provided hash.
func ReadPreimage(db dbstore.KeyValueReader, hash util.Hash) []byte {
	data, _ := db.Get(preimageKey(hash))
	return data
}

// ReadCode retrieves the contract code of the provided code hash.
func ReadCode(db dbstore.KeyValueReader, hash util.Hash) []byte {
	// Try with the prefixed code scheme first, if not then try with legacy
	// scheme.
	data := ReadCodeWithPrefix(db, hash)
	if len(data) != 0 {
		return data
	}
	data, _ = db.Get(hash.Bytes())
	return data
}

// ReadCodeWithPrefix retrieves the contract code of the provided code hash.
// The main difference between this function and ReadCode is this function
// will only check the existence with latest scheme(with prefix).
func ReadCodeWithPrefix(db dbstore.KeyValueReader, hash util.Hash) []byte {
	data, _ := db.Get(codeKey(hash))
	return data
}

// ReadTrieNode retrieves the trie node of the provided hash.
func ReadTrieNode(db dbstore.KeyValueReader, hash util.Hash) []byte {
	data, _ := db.Get(hash.Bytes())
	return data
}

// HasCode checks if the contract code corresponding to the
// provided code hash is present in the db.
func HasCode(db dbstore.KeyValueReader, hash util.Hash) bool {
	// Try with the prefixed code scheme first, if not then try with legacy
	// scheme.
	if ok := HasCodeWithPrefix(db, hash); ok {
		return true
	}
	ok, _ := db.Has(hash.Bytes())
	return ok
}

// HasCodeWithPrefix checks if the contract code corresponding to the
// provided code hash is present in the db. This function will only check
// presence using the prefix-scheme.
func HasCodeWithPrefix(db dbstore.KeyValueReader, hash util.Hash) bool {
	ok, _ := db.Has(codeKey(hash))
	return ok
}

// HasTrieNode checks if the trie node with the provided hash is present in db.
func HasTrieNode(db dbstore.KeyValueReader, hash util.Hash) bool {
	ok, _ := db.Has(hash.Bytes())
	return ok
}

// WritePreimages writes the provided set of preimages to the database.
func WritePreimages(db dbstore.KeyValueWriter, preimages map[util.Hash][]byte) {
	for hash, preimage := range preimages {
		if err := db.Put(preimageKey(hash), preimage); err != nil {
			logging.Crit("Failed to store trie preimage", "err", err)
		}
	}
	preimageCounter.Inc(int64(len(preimages)))
	preimageHitCounter.Inc(int64(len(preimages)))
}

// WriteCode writes the provided contract code database.
func WriteCode(db dbstore.KeyValueWriter, hash util.Hash, code []byte) {
	if err := db.Put(codeKey(hash), code); err != nil {
		logging.Crit("Failed to store contract code", "err", err)
	}
}

// WriteTrieNode writes the provided trie node database.
func WriteTrieNode(db dbstore.KeyValueWriter, hash util.Hash, node []byte) {
	if err := db.Put(hash.Bytes(), node); err != nil {
		logging.Crit("Failed to store trie node", "err", err)
	}
}

// DeleteCode deletes the specified contract code from the database.
func DeleteCode(db dbstore.KeyValueWriter, hash util.Hash) {
	if err := db.Delete(codeKey(hash)); err != nil {
		logging.Crit("Failed to delete contract code", "err", err)
	}
}

// DeleteTrieNode deletes the specified trie node from the database.
func DeleteTrieNode(db dbstore.KeyValueWriter, hash util.Hash) {
	if err := db.Delete(hash.Bytes()); err != nil {
		logging.Crit("Failed to delete trie node", "err", err)
	}
}
