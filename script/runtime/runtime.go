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

package runtime

import (
	"math"
	"math/big"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
	"github.com/ParallaxProtocol/parallax/validation/state"
)

// Config is a basic type specifying certain configuration flags for running
// the PVM.
type Config struct {
	ChainConfig *chainparams.ChainConfig
	Difficulty  *big.Int
	Origin      util.Address
	Coinbase    util.Address
	BlockNumber *big.Int
	Time        *big.Int
	GasLimit    uint64
	GasPrice    *big.Int
	Value       *big.Int
	Debug       bool
	PVMConfig   script.Config
	BaseFee     *big.Int

	State     *state.StateDB
	GetHashFn func(n uint64) util.Hash
}

// sets defaults on the config
func setDefaults(cfg *Config) {
	if cfg.ChainConfig == nil {
		cfg.ChainConfig = &chainparams.ChainConfig{
			ChainID:             big.NewInt(1),
			HomesteadBlock:      new(big.Int),
			EIP150Block:         new(big.Int),
			EIP150Hash:          util.Hash{},
			EIP155Block:         new(big.Int),
			EIP158Block:         new(big.Int),
			ByzantiumBlock:      new(big.Int),
			ConstantinopleBlock: new(big.Int),
			PetersburgBlock:     new(big.Int),
			IstanbulBlock:       new(big.Int),
			BerlinBlock:         new(big.Int),
			LondonBlock:         new(big.Int),
		}
	}

	if cfg.Difficulty == nil {
		cfg.Difficulty = new(big.Int)
	}
	if cfg.Time == nil {
		cfg.Time = big.NewInt(time.Now().Unix())
	}
	if cfg.GasLimit == 0 {
		cfg.GasLimit = math.MaxUint64
	}
	if cfg.GasPrice == nil {
		cfg.GasPrice = new(big.Int)
	}
	if cfg.Value == nil {
		cfg.Value = new(big.Int)
	}
	if cfg.BlockNumber == nil {
		cfg.BlockNumber = new(big.Int)
	}
	if cfg.GetHashFn == nil {
		cfg.GetHashFn = func(n uint64) util.Hash {
			return util.BytesToHash(crypto.Keccak256([]byte(new(big.Int).SetUint64(n).String())))
		}
	}
	if cfg.BaseFee == nil {
		cfg.BaseFee = big.NewInt(chainparams.InitialBaseFee)
	}
}

// Execute executes the code using the input as call data during the execution.
// It returns the PVM's return value, the new state and an error if it failed.
//
// Execute sets up an in-memory, temporary, environment for the execution of
// the given code. It makes sure that it's restored to its original state afterwards.
func Execute(code, input []byte, cfg *Config) ([]byte, *state.StateDB, error) {
	if cfg == nil {
		cfg = new(Config)
	}
	setDefaults(cfg)

	if cfg.State == nil {
		cfg.State, _ = state.New(util.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	}
	var (
		address = util.BytesToAddress([]byte("contract"))
		vmenv   = NewEnv(cfg)
		sender  = script.AccountRef(cfg.Origin)
	)
	if rules := cfg.ChainConfig.Rules(vmenv.Context.BlockNumber, vmenv.Context.Random != nil); rules.IsBerlin {
		cfg.State.PrepareAccessList(cfg.Origin, &address, script.ActivePrecompiles(rules), nil)
	}
	cfg.State.CreateAccount(address)
	// set the receiver's (the executing contract) code for execution.
	cfg.State.SetCode(address, code)
	// Call the code with the given configuration.
	ret, _, err := vmenv.Call(
		sender,
		util.BytesToAddress([]byte("contract")),
		input,
		cfg.GasLimit,
		cfg.Value,
	)

	return ret, cfg.State, err
}

// Create executes the code using the PVM create method
func Create(input []byte, cfg *Config) ([]byte, util.Address, uint64, error) {
	if cfg == nil {
		cfg = new(Config)
	}
	setDefaults(cfg)

	if cfg.State == nil {
		cfg.State, _ = state.New(util.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	}
	var (
		vmenv  = NewEnv(cfg)
		sender = script.AccountRef(cfg.Origin)
	)
	if rules := cfg.ChainConfig.Rules(vmenv.Context.BlockNumber, vmenv.Context.Random != nil); rules.IsBerlin {
		cfg.State.PrepareAccessList(cfg.Origin, nil, script.ActivePrecompiles(rules), nil)
	}
	// Call the code with the given configuration.
	code, address, leftOverGas, err := vmenv.Create(
		sender,
		input,
		cfg.GasLimit,
		cfg.Value,
	)
	return code, address, leftOverGas, err
}

// Call executes the code given by the contract's address. It will return the
// PVM's return value or an error if it failed.
//
// Call, unlike Execute, requires a config and also requires the State field to
// be set.
func Call(address util.Address, input []byte, cfg *Config) ([]byte, uint64, error) {
	setDefaults(cfg)

	vmenv := NewEnv(cfg)

	sender := cfg.State.GetOrNewStateObject(cfg.Origin)
	statedb := cfg.State

	if rules := cfg.ChainConfig.Rules(vmenv.Context.BlockNumber, vmenv.Context.Random != nil); rules.IsBerlin {
		statedb.PrepareAccessList(cfg.Origin, &address, script.ActivePrecompiles(rules), nil)
	}
	// Call the code with the given configuration.
	ret, leftOverGas, err := vmenv.Call(
		sender,
		address,
		input,
		cfg.GasLimit,
		cfg.Value,
	)
	return ret, leftOverGas, err
}
