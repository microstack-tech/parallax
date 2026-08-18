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

package script

import (
	"math/big"

	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

// StateDB is an PVM database for full state querying.
type StateDB interface {
	CreateAccount(util.Address)

	SubBalance(util.Address, *big.Int)
	AddBalance(util.Address, *big.Int)
	GetBalance(util.Address) *big.Int

	GetNonce(util.Address) uint64
	SetNonce(util.Address, uint64)

	GetCodeHash(util.Address) util.Hash
	GetCode(util.Address) []byte
	SetCode(util.Address, []byte)
	GetCodeSize(util.Address) int

	AddRefund(uint64)
	SubRefund(uint64)
	GetRefund() uint64

	GetCommittedState(util.Address, util.Hash) util.Hash
	GetState(util.Address, util.Hash) util.Hash
	SetState(util.Address, util.Hash, util.Hash)

	Suicide(util.Address) bool
	HasSuicided(util.Address) bool

	// Exist reports whether the given account exists in state.
	// Notably this should also return true for suicided accounts.
	Exist(util.Address) bool
	// Empty returns whether the given account is empty. Empty
	// is defined according to EIP161 (balance = nonce = code = 0).
	Empty(util.Address) bool

	PrepareAccessList(sender util.Address, dest *util.Address, precompiles []util.Address, txAccesses types.AccessList)
	AddressInAccessList(addr util.Address) bool
	SlotInAccessList(addr util.Address, slot util.Hash) (addressOk bool, slotOk bool)
	// AddAddressToAccessList adds the given address to the access list. This operation is safe to perform
	// even if the feature/fork is not active yet
	AddAddressToAccessList(addr util.Address)
	// AddSlotToAccessList adds the given (address,slot) to the access list. This operation is safe to perform
	// even if the feature/fork is not active yet
	AddSlotToAccessList(addr util.Address, slot util.Hash)

	RevertToSnapshot(int)
	Snapshot() int

	AddLog(*types.Log)
	AddPreimage(util.Hash, []byte)

	ForEachStorage(util.Address, func(util.Hash, util.Hash) bool) error
}

// CallContext provides a basic interface for the PVM calling conventions. The PVM
// depends on this context being implemented for doing subcalls and initialising new PVM contracts.
type CallContext interface {
	// Call another contract
	Call(env *PVM, me ContractRef, addr util.Address, data []byte, gas, value *big.Int) ([]byte, error)
	// Take another's contract code and execute within our own context
	CallCode(env *PVM, me ContractRef, addr util.Address, data []byte, gas, value *big.Int) ([]byte, error)
	// Same as CallCode except sender and value is propagated from parent to child scope
	DelegateCall(env *PVM, me ContractRef, addr util.Address, data []byte, gas *big.Int) ([]byte, error)
	// Create a new contract
	Create(env *PVM, me ContractRef, data []byte, gas, value *big.Int) ([]byte, util.Address, error)
}
