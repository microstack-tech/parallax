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
	"reflect"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/dbstore"
	"github.com/ParallaxProtocol/parallax/v2/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/v2/kernel/xhash"
	"github.com/ParallaxProtocol/parallax/v2/script"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/validation/rawdb"
	"github.com/davecgh/go-spew/spew"
)

func TestSetupGenesis(t *testing.T) {
	var (
		customghash = util.HexToHash("0x86d15358716b9184d1a2fc8d6b1813ff36d418e88ad87d86757df3d8ad26990e")
		customg     = Genesis{
			Config: &chainparams.ChainConfig{HomesteadBlock: big.NewInt(3), XHash: &chainparams.XHashConfig{CoinbaseMaturityBlocks: 0, RetargetIntervalBlocks: 10}},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1), Storage: map[util.Hash]util.Hash{{1}: {1}}},
			},
		}
		oldcustomg = customg
	)
	oldcustomg.Config = &chainparams.ChainConfig{HomesteadBlock: big.NewInt(2), XHash: &chainparams.XHashConfig{CoinbaseMaturityBlocks: 0, RetargetIntervalBlocks: 10}}
	tests := []struct {
		name       string
		fn         func(dbstore.Database) (*chainparams.ChainConfig, util.Hash, error)
		wantConfig *chainparams.ChainConfig
		wantHash   util.Hash
		wantErr    error
	}{
		{
			name: "genesis without ChainConfig",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				return SetupGenesisBlock(db, new(Genesis))
			},
			wantErr:    errGenesisNoConfig,
			wantConfig: chainparams.AllXHashProtocolChanges,
		},
		{
			name: "no block in DB, genesis == nil",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   chainparams.MainnetGenesisHash,
			wantConfig: chainparams.MainnetChainConfig,
		},
		{
			name: "mainnet block in DB, genesis == nil",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				DefaultGenesisBlock().MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   chainparams.MainnetGenesisHash,
			wantConfig: chainparams.MainnetChainConfig,
		},
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "custom block in DB, genesis == testnet",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, DefaultTestnetGenesisBlock())
			},
			wantErr:    &GenesisMismatchError{Stored: customghash, New: chainparams.TestnetGenesisHash},
			wantHash:   chainparams.TestnetGenesisHash,
			wantConfig: chainparams.TestnetChainConfig,
		},
		{
			name: "compatible config in DB",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				oldcustomg.MustCommit(db)
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "incompatible config in DB",
			fn: func(db dbstore.Database) (*chainparams.ChainConfig, util.Hash, error) {
				// Commit the 'old' genesis block with Homestead transition at #2.
				// Advance to block #4, past the homestead transition block of customg.
				genesis := oldcustomg.MustCommit(db)

				bc, _ := NewBlockChain(db, nil, oldcustomg.Config, xhash.NewFullFaker(), script.Config{}, nil, nil)
				defer bc.Stop()

				blocks, _ := GenerateChain(oldcustomg.Config, genesis, xhash.NewFaker(), db, 4, nil)
				bc.InsertChain(blocks)
				bc.CurrentBlock()
				// This should return a compatibility error.
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
			wantErr: &chainparams.ConfigCompatError{
				What:         "Homestead fork block",
				StoredConfig: big.NewInt(2),
				NewConfig:    big.NewInt(3),
				RewindTo:     1,
			},
		},
	}

	for _, test := range tests {
		db := rawdb.NewMemoryDatabase()
		config, hash, err := test.fn(db)
		// Check the return values.
		if !reflect.DeepEqual(err, test.wantErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(err), spew.NewFormatter(test.wantErr))
		}
		if !reflect.DeepEqual(config, test.wantConfig) {
			t.Errorf("%s:\nreturned %v\nwant     %v", test.name, config, test.wantConfig)
		}
		if hash != test.wantHash {
			t.Errorf("%s: returned hash %s, want %s", test.name, hash.Hex(), test.wantHash.Hex())
		} else if err == nil {
			// Check database content.
			stored := rawdb.ReadBlock(db, test.wantHash, 0)
			if stored.Hash() != test.wantHash {
				t.Errorf("%s: block in DB has hash %s, want %s", test.name, stored.Hash(), test.wantHash)
			}
		}
	}
}

// TestGenesisHashes checks the congruity of default genesis data to
// corresponding hardcoded genesis hash values.
func TestGenesisHashes(t *testing.T) {
	for i, c := range []struct {
		genesis *Genesis
		want    util.Hash
	}{
		{DefaultGenesisBlock(), chainparams.MainnetGenesisHash},
		{DefaultTestnetGenesisBlock(), chainparams.TestnetGenesisHash},
	} {
		// Test via MustCommit
		if have := c.genesis.MustCommit(rawdb.NewMemoryDatabase()).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
		// Test via ToBlock
		if have := c.genesis.ToBlock(nil).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
	}
}

func TestGenesis_Commit(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(chainparams.InitialBaseFee),
		Config:  chainparams.TestChainConfig,
	}

	db := rawdb.NewMemoryDatabase()
	genesisBlock, err := genesis.Commit(db)
	if err != nil {
		t.Fatal(err)
	}

	if genesis.Difficulty != nil {
		t.Fatalf("assumption wrong")
	}

	// This value should have been set as default in the ToBlock method.
	if genesisBlock.Difficulty().Cmp(chainparams.GenesisDifficulty) != 0 {
		t.Errorf("assumption wrong: want: %d, got: %v", chainparams.GenesisDifficulty, genesisBlock.Difficulty())
	}

	// Expect the stored total difficulty to be the difficulty of the genesis block.
	stored := rawdb.ReadTd(db, genesisBlock.Hash(), genesisBlock.NumberU64())

	if stored.Cmp(genesisBlock.Difficulty()) != 0 {
		t.Errorf("inequal difficulty; stored: %v, genesisBlock: %v", stored, genesisBlock.Difficulty())
	}
}

func TestReadWriteGenesisAlloc(t *testing.T) {
	var (
		db    = rawdb.NewMemoryDatabase()
		alloc = &GenesisAlloc{
			{1}: {Balance: big.NewInt(1), Storage: map[util.Hash]util.Hash{{1}: {1}}},
			{2}: {Balance: big.NewInt(2), Storage: map[util.Hash]util.Hash{{2}: {2}}},
		}
		hash = util.HexToHash("0xdeadbeef")
	)
	alloc.write(db, hash)

	var reload GenesisAlloc
	err := reload.UnmarshalJSON(rawdb.ReadGenesisState(db, hash))
	if err != nil {
		t.Fatalf("Failed to load genesis state %v", err)
	}
	if len(reload) != len(*alloc) {
		t.Fatal("Unexpected genesis allocation")
	}
	for addr, account := range reload {
		want, ok := (*alloc)[addr]
		if !ok {
			t.Fatal("Account is not found")
		}
		if !reflect.DeepEqual(want, account) {
			t.Fatal("Unexpected account")
		}
	}
}
