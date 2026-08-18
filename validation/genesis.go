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
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/dbstore"
	"github.com/ParallaxProtocol/parallax/v2/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/util/hexutil"
	"github.com/ParallaxProtocol/parallax/v2/util/math"
	"github.com/ParallaxProtocol/parallax/v2/validation/rawdb"
	"github.com/ParallaxProtocol/parallax/v2/validation/state"
	"github.com/ParallaxProtocol/parallax/v2/validation/trie"
)

//go:generate go run github.com/fjl/gencodec -type Genesis -field-override genesisSpecMarshaling -out gen_genesis.go
//go:generate go run github.com/fjl/gencodec -type GenesisAccount -field-override genesisAccountMarshaling -out gen_genesis_account.go

var errGenesisNoConfig = errors.New("genesis has no chain configuration")

// Genesis specifies the header fields, state of a genesis block. It also defines hard
// fork switch-over blocks through the chain configuration.
type Genesis struct {
	Config         *chainparams.ChainConfig `json:"config"`
	Nonce          uint64                   `json:"nonce"`
	Timestamp      uint64                   `json:"timestamp"`
	EpochStartTime uint64                   `json:"epochStartTime"`
	ExtraData      []byte                   `json:"extraData"`
	GasLimit       uint64                   `json:"gasLimit"   gencodec:"required"`
	Difficulty     *big.Int                 `json:"difficulty" gencodec:"required"`
	Mixhash        util.Hash                `json:"mixHash"`
	Coinbase       util.Address             `json:"coinbase"`
	Alloc          GenesisAlloc             `json:"alloc"      gencodec:"required"`

	// These fields are used for consensus tests. Please don't use them
	// in actual genesis blocks.
	Number     uint64    `json:"number"`
	GasUsed    uint64    `json:"gasUsed"`
	ParentHash util.Hash `json:"parentHash"`
	BaseFee    *big.Int  `json:"baseFeePerGas"`
}

// GenesisAlloc specifies the initial state that is part of the genesis block.
type GenesisAlloc map[util.Address]GenesisAccount

func (ga *GenesisAlloc) UnmarshalJSON(data []byte) error {
	m := make(map[util.UnprefixedAddress]GenesisAccount)
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*ga = make(GenesisAlloc)
	for addr, a := range m {
		(*ga)[util.Address(addr)] = a
	}
	return nil
}

// flush adds allocated genesis accounts into a fresh new statedb and
// commit the state changes into the given database handler.
func (ga *GenesisAlloc) flush(db dbstore.Database) (util.Hash, error) {
	statedb, err := state.New(util.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		return util.Hash{}, err
	}
	for addr, account := range *ga {
		statedb.AddBalance(addr, account.Balance)
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}
	root, err := statedb.Commit(false)
	if err != nil {
		return util.Hash{}, err
	}
	err = statedb.Database().TrieDB().Commit(root, true, nil)
	if err != nil {
		return util.Hash{}, err
	}
	return root, nil
}

// write writes the json marshaled genesis state into database
// with the given block hash as the unique identifier.
func (ga *GenesisAlloc) write(db dbstore.KeyValueWriter, hash util.Hash) error {
	blob, err := json.Marshal(ga)
	if err != nil {
		return err
	}
	rawdb.WriteGenesisState(db, hash, blob)
	return nil
}

// CommitGenesisState loads the stored genesis state with the given block
// hash and commits them into the given database handler.
func CommitGenesisState(db dbstore.Database, hash util.Hash) error {
	var alloc GenesisAlloc
	blob := rawdb.ReadGenesisState(db, hash)
	if len(blob) != 0 {
		if err := alloc.UnmarshalJSON(blob); err != nil {
			return err
		}
	} else {
		// Genesis allocation is missing and there are several possibilities:
		// the node is legacy which doesn't persist the genesis allocation or
		// the persisted allocation is just lost.
		// - supported networks(mainnet, testnets), recover with defined allocations
		// - private network, can't recover
		var genesis *Genesis
		switch hash {
		case chainparams.MainnetGenesisHash:
			genesis = DefaultGenesisBlock()
		case chainparams.TestnetGenesisHash:
			genesis = DefaultTestnetGenesisBlock()
		}
		if genesis != nil {
			alloc = genesis.Alloc
		} else {
			return errors.New("not found")
		}
	}
	_, err := alloc.flush(db)
	return err
}

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	Code       []byte                  `json:"code,omitempty"`
	Storage    map[util.Hash]util.Hash `json:"storage,omitempty"`
	Balance    *big.Int                `json:"balance" gencodec:"required"`
	Nonce      uint64                  `json:"nonce,omitempty"`
	PrivateKey []byte                  `json:"secretKey,omitempty"` // for tests
}

// field type overrides for gencodec
type genesisSpecMarshaling struct {
	Nonce          math.HexOrDecimal64
	Timestamp      math.HexOrDecimal64
	EpochStartTime math.HexOrDecimal64
	ExtraData      hexutil.Bytes
	GasLimit       math.HexOrDecimal64
	GasUsed        math.HexOrDecimal64
	Number         math.HexOrDecimal64
	Difficulty     *math.HexOrDecimal256
	BaseFee        *math.HexOrDecimal256
	Alloc          map[util.UnprefixedAddress]GenesisAccount
}

type genesisAccountMarshaling struct {
	Code       hexutil.Bytes
	Balance    *math.HexOrDecimal256
	Nonce      math.HexOrDecimal64
	Storage    map[storageJSON]storageJSON
	PrivateKey hexutil.Bytes
}

// storageJSON represents a 256 bit byte array, but allows less than 256 bits when
// unmarshaling from hex.
type storageJSON util.Hash

func (h *storageJSON) UnmarshalText(text []byte) error {
	text = bytes.TrimPrefix(text, []byte("0x"))
	if len(text) > 64 {
		return fmt.Errorf("too many hex characters in storage key/value %q", text)
	}
	offset := len(h) - len(text)/2 // pad on the left
	if _, err := hex.Decode(h[offset:], text); err != nil {
		fmt.Println(err)
		return fmt.Errorf("invalid hex storage key/value %q", text)
	}
	return nil
}

func (h storageJSON) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// GenesisMismatchError is raised when trying to overwrite an existing
// genesis block with an incompatible one.
type GenesisMismatchError struct {
	Stored, New util.Hash
}

func (e *GenesisMismatchError) Error() string {
	return fmt.Sprintf("database contains incompatible genesis (have %x, new %x)", e.Stored, e.New)
}

// SetupGenesisBlock writes or updates the genesis block in db.
// The block that will be used is:
//
//	                     genesis == nil       genesis != nil
//	                  +------------------------------------------
//	db has no genesis |  main-net default  |  genesis
//	db has genesis    |  from DB           |  genesis (if compatible)
//
// The stored chain configuration will be updated if it is compatible (i.e. does not
// specify a fork block below the local head block). In case of a conflict, the
// error is a *params.ConfigCompatError and the new, unwritten config is returned.
//
// The returned chain configuration is never nil.
func SetupGenesisBlock(db dbstore.Database, genesis *Genesis) (*chainparams.ChainConfig, util.Hash, error) {
	return SetupGenesisBlockWithOverride(db, genesis)
}

func SetupGenesisBlockWithOverride(db dbstore.Database, genesis *Genesis) (*chainparams.ChainConfig, util.Hash, error) {
	if genesis != nil && genesis.Config == nil {
		return chainparams.AllXHashProtocolChanges, util.Hash{}, errGenesisNoConfig
	}
	// Just commit the new block if there is no stored genesis block.
	stored := rawdb.ReadCanonicalHash(db, 0)
	if (stored == util.Hash{}) {
		if genesis == nil {
			logging.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			logging.Info("Writing custom genesis block")
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, util.Hash{}, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// We have the genesis block in database(perhaps in ancient database)
	// but the corresponding state is missing.
	header := rawdb.ReadHeader(db, stored, 0)
	if _, err := state.New(header.Root, state.NewDatabaseWithConfig(db, nil), nil); err != nil {
		if genesis == nil {
			genesis = DefaultGenesisBlock()
		}
		// Ensure the stored genesis matches with the given one.
		hash := genesis.ToBlock(nil).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, hash, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// Check whether the genesis block is already written.
	if genesis != nil {
		hash := genesis.ToBlock(nil).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
	}
	// Get the existing chain configuration.
	newcfg := genesis.configOrDefault(stored)
	if err := newcfg.CheckConfigForkOrder(); err != nil {
		return newcfg, util.Hash{}, err
	}
	storedcfg := rawdb.ReadChainConfig(db, stored)
	if storedcfg == nil {
		logging.Warn("Found genesis block without chain config")
		rawdb.WriteChainConfig(db, stored, newcfg)
		return newcfg, stored, nil
	}
	// Special case: if a private network is being used (no genesis and also no
	// mainnet hash in the database), we must not apply the `configOrDefault`
	// chain config as that would be AllProtocolChanges (applying any new fork
	// on top of an existing private network genesis block). In that case, only
	// apply the overrides.
	if genesis == nil && stored != chainparams.MainnetGenesisHash {
		newcfg = storedcfg
	}
	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	height := rawdb.ReadHeaderNumber(db, rawdb.ReadHeadHeaderHash(db))
	if height == nil {
		return newcfg, stored, fmt.Errorf("missing block number for head header hash")
	}
	compatErr := storedcfg.CheckCompatible(newcfg, *height)
	if compatErr != nil && *height != 0 && compatErr.RewindTo != 0 {
		return newcfg, stored, compatErr
	}
	rawdb.WriteChainConfig(db, stored, newcfg)
	return newcfg, stored, nil
}

func (g *Genesis) configOrDefault(ghash util.Hash) *chainparams.ChainConfig {
	switch {
	case g != nil:
		return g.Config
	case ghash == chainparams.MainnetGenesisHash:
		return chainparams.MainnetChainConfig
	case ghash == chainparams.TestnetGenesisHash:
		return chainparams.TestnetChainConfig
	default:
		return chainparams.AllXHashProtocolChanges
	}
}

// ToBlock creates the genesis block and writes state of a genesis specification
// to the given database (or discards it if nil).
func (g *Genesis) ToBlock(db dbstore.Database) *types.Block {
	if db == nil {
		db = rawdb.NewMemoryDatabase()
	}
	root, err := g.Alloc.flush(db)
	if err != nil {
		panic(err)
	}
	head := &types.Header{
		Number:         new(big.Int).SetUint64(g.Number),
		Nonce:          types.EncodeNonce(g.Nonce),
		Time:           g.Timestamp,
		EpochStartTime: g.EpochStartTime,
		ParentHash:     g.ParentHash,
		Extra:          g.ExtraData,
		GasLimit:       g.GasLimit,
		GasUsed:        g.GasUsed,
		BaseFee:        g.BaseFee,
		Difficulty:     g.Difficulty,
		MixDigest:      g.Mixhash,
		Coinbase:       g.Coinbase,
		Root:           root,
	}
	if g.GasLimit == 0 {
		head.GasLimit = chainparams.GenesisGasLimit
	}
	if g.Difficulty == nil && g.Mixhash == (util.Hash{}) {
		head.Difficulty = chainparams.GenesisDifficulty
	}
	if g.Config != nil && g.Config.IsLondon(util.Big0) {
		if g.BaseFee != nil {
			head.BaseFee = g.BaseFee
		} else {
			head.BaseFee = new(big.Int).SetUint64(chainparams.InitialBaseFee)
		}
	}
	return types.NewBlock(head, nil, nil, nil, trie.NewStackTrie(nil))
}

// Commit writes the block and state of a genesis specification to the database.
// The block is committed as the canonical head block.
func (g *Genesis) Commit(db dbstore.Database) (*types.Block, error) {
	block := g.ToBlock(db)
	if block.Number().Sign() != 0 {
		return nil, errors.New("can't commit genesis block with number > 0")
	}
	config := g.Config
	if config == nil {
		config = chainparams.AllXHashProtocolChanges
	}
	if err := config.CheckConfigForkOrder(); err != nil {
		return nil, err
	}
	if config.Clique != nil && len(block.Extra()) < 32+crypto.SignatureLength {
		return nil, errors.New("can't start clique chain without signers")
	}
	if err := g.Alloc.write(db, block.Hash()); err != nil {
		return nil, err
	}
	rawdb.WriteTd(db, block.Hash(), block.NumberU64(), block.Difficulty())
	rawdb.WriteBlock(db, block)
	rawdb.WriteReceipts(db, block.Hash(), block.NumberU64(), nil)
	rawdb.WriteCanonicalHash(db, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(db, block.Hash())
	rawdb.WriteHeadFastBlockHash(db, block.Hash())
	rawdb.WriteHeadHeaderHash(db, block.Hash())
	rawdb.WriteChainConfig(db, block.Hash(), config)
	return block, nil
}

// MustCommit writes the genesis block and state to db, panicking on error.
// The block is committed as the canonical head block.
func (g *Genesis) MustCommit(db dbstore.Database) *types.Block {
	block, err := g.Commit(db)
	if err != nil {
		panic(err)
	}
	return block
}

// GenesisBlockForTesting creates and writes a block in which addr has the given wei balance.
func GenesisBlockForTesting(db dbstore.Database, addr util.Address, balance *big.Int) *types.Block {
	g := Genesis{
		Alloc:   GenesisAlloc{addr: {Balance: balance}},
		BaseFee: big.NewInt(chainparams.InitialBaseFee),
	}
	return g.MustCommit(db)
}

// DefaultGenesisBlock returns the Parallax mainnet genesis block.
func DefaultGenesisBlock() *Genesis {
	return &Genesis{
		Config:         chainparams.MainnetChainConfig,
		Nonce:          2110,
		ExtraData:      []byte("The Times 28/Oct/2025 Trump tells Japan: Any favours you need, we’ll be there"),
		Timestamp:      1761678000,
		EpochStartTime: 1761678000,
		GasLimit:       600000000,
		Difficulty:     big.NewInt(0x400000000),
		Alloc: GenesisAlloc{
			util.HexToAddress("0x0000000000000000000000000000000000000042"): {
				Balance: big.NewInt(1), // Init lockbox address. Required by coinbase maturity impl
			},
		},
	}
}

// DefaultTestnetGenesisBlock returns the test network genesis block.
func DefaultTestnetGenesisBlock() *Genesis {
	return &Genesis{
		Config:         chainparams.TestnetChainConfig,
		Nonce:          2111,
		ExtraData:      hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:       600000000,
		Difficulty:     big.NewInt(0xA000000),
		Timestamp:      1759695695,
		EpochStartTime: 1759695695,
		Alloc: GenesisAlloc{
			util.HexToAddress("0x0000000000000000000000000000000000000042"): {
				Balance: big.NewInt(1), // Init lockbox address. Required by coinbase maturity impl
			},
		},
	}
}

// DeveloperGenesisBlock returns the 'parallax --dev' genesis block.
func DeveloperGenesisBlock(period uint64, gasLimit uint64, faucet util.Address) *Genesis {
	// Override the default period to the user requested one
	config := *chainparams.AllCliqueProtocolChanges
	config.Clique = &chainparams.CliqueConfig{
		Period: period,
		Epoch:  config.Clique.Epoch,
	}

	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet[:]...), make([]byte, crypto.SignatureLength)...),
		GasLimit:   gasLimit,
		BaseFee:    big.NewInt(chainparams.InitialBaseFee),
		Difficulty: big.NewInt(1),
		Alloc: map[util.Address]GenesisAccount{
			util.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			util.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			util.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			util.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			util.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			util.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			util.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			util.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			util.BytesToAddress([]byte{9}): {Balance: big.NewInt(1)}, // BLAKE2b
			faucet:                         {Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))},
		},
	}
}
