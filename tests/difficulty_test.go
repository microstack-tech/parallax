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

package tests

import (
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

var mainnetChainConfig = chainparams.ChainConfig{
	ChainID:        big.NewInt(1),
	HomesteadBlock: big.NewInt(1150000),
	EIP150Block:    big.NewInt(2463000),
	EIP150Hash:     util.HexToHash("0x2086799aeebeae135c246c65021c82b4e15a2c451340993aacfd2751886514f0"),
	EIP155Block:    big.NewInt(2675000),
	EIP158Block:    big.NewInt(2675000),
	ByzantiumBlock: big.NewInt(4370000),
}

func TestDifficulty(t *testing.T) {
	t.Parallel()

	// The upstream BasicTests difficulty fixtures encode the ethash
	// per-block difficulty adjustment. Parallax replaces that with epoch
	// based Nakamoto/ASERT retargeting (difficulty is flat within a
	// retarget epoch), so these vectors cannot apply. The native retarget
	// algorithm has its own vector suite in kernel/xhash (see
	// testdata/aserti3-2d and difficulty_test.go there).
	t.Skip("upstream ethash difficulty fixtures do not apply to nakamoto/asert retargeting")

	dt := new(testMatcher)
	// Not difficulty-tests
	dt.skipLoad("hexencodetest.*")
	dt.skipLoad("crypto.*")
	dt.skipLoad("blockgenesistest\\.json")
	dt.skipLoad("genesishashestest\\.json")
	dt.skipLoad("keyaddrtest\\.json")
	dt.skipLoad("txtest\\.json")

	// files are 2 years old, contains strange values
	dt.skipLoad("difficultyCustomHomestead\\.json")
	dt.skipLoad("difficultyMorden\\.json")
	dt.skipLoad("difficultyOlimpic\\.json")

	dt.config("Testnet", *chainparams.TestnetChainConfig)
	dt.config("Morden", *chainparams.TestnetChainConfig)
	dt.config("Frontier", chainparams.ChainConfig{})

	dt.config("Homestead", chainparams.ChainConfig{
		HomesteadBlock: big.NewInt(0),
	})

	dt.config("Byzantium", chainparams.ChainConfig{
		ByzantiumBlock: big.NewInt(0),
	})

	dt.config("Frontier", *chainparams.TestnetChainConfig)
	dt.config("MainNetwork", mainnetChainConfig)
	dt.config("CustomMainNetwork", mainnetChainConfig)
	dt.config("Constantinople", chainparams.ChainConfig{
		ConstantinopleBlock: big.NewInt(0),
	})
	dt.config("difficulty.json", mainnetChainConfig)

	dt.walk(t, difficultyTestDir, func(t *testing.T, name string, test *DifficultyTest) {
		cfg := dt.findConfig(t)
		if test.ParentDifficulty.Cmp(chainparams.MinimumDifficulty) < 0 {
			t.Skip("difficulty below minimum")
			return
		}
		if err := dt.checkFailure(t, test.Run(cfg)); err != nil {
			t.Error(err)
		}
	})
}
