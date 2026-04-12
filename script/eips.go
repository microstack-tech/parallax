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
	"fmt"
	"sort"

	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/holiman/uint256"
)

var activators = map[int]func(*JumpTable){
	3855: enable3855,
	3529: enable3529,
	3198: enable3198,
	2929: enable2929,
	2200: enable2200,
	1884: enable1884,
	1344: enable1344,
}

// EnableEIP enables the given EIP on the config.
// This operation writes in-place, and callers need to ensure that the globally
// defined jump tables are not polluted.
func EnableEIP(eipNum int, jt *JumpTable) error {
	enablerFn, ok := activators[eipNum]
	if !ok {
		return fmt.Errorf("undefined eip %d", eipNum)
	}
	enablerFn(jt)
	return nil
}

func ValidEip(eipNum int) bool {
	_, ok := activators[eipNum]
	return ok
}

func ActivateableEips() []string {
	var nums []string
	for k := range activators {
		nums = append(nums, fmt.Sprintf("%d", k))
	}
	sort.Strings(nums)
	return nums
}

// enable1884 applies EIP-1884 to the given jump table:
// - Increase cost of BALANCE to 700
// - Increase cost of EXTCODEHASH to 700
// - Increase cost of SLOAD to 800
// - Define SELFBALANCE, with cost GasFastStep (5)
func enable1884(jt *JumpTable) {
	// Gas cost changes
	jt[SLOAD].constantGas = chainparams.SloadGasEIP1884
	jt[BALANCE].constantGas = chainparams.BalanceGasEIP1884
	jt[EXTCODEHASH].constantGas = chainparams.ExtcodeHashGasEIP1884

	// New opcode
	jt[SELFBALANCE] = &operation{
		execute:     opSelfBalance,
		constantGas: GasFastStep,
		minStack:    minStack(0, 1),
		maxStack:    maxStack(0, 1),
	}
}

func opSelfBalance(pc *uint64, interpreter *PVMInterpreter, scope *ScopeContext) ([]byte, error) {
	balance, _ := uint256.FromBig(interpreter.pvm.StateDB.GetBalance(scope.Contract.Address()))
	scope.Stack.push(balance)
	return nil, nil
}

// enable1344 applies EIP-1344 (ChainID Opcode)
// - Adds an opcode that returns the current chain’s EIP-155 unique identifier
func enable1344(jt *JumpTable) {
	// New opcode
	jt[CHAINID] = &operation{
		execute:     opChainID,
		constantGas: GasQuickStep,
		minStack:    minStack(0, 1),
		maxStack:    maxStack(0, 1),
	}
}

// opChainID implements CHAINID opcode
func opChainID(pc *uint64, interpreter *PVMInterpreter, scope *ScopeContext) ([]byte, error) {
	chainId, _ := uint256.FromBig(interpreter.pvm.chainConfig.ChainID)
	scope.Stack.push(chainId)
	return nil, nil
}

// enable2200 applies EIP-2200 (Rebalance net-metered SSTORE)
func enable2200(jt *JumpTable) {
	jt[SLOAD].constantGas = chainparams.SloadGasEIP2200
	jt[SSTORE].dynamicGas = gasSStoreEIP2200
}

// enable2929 enables "EIP-2929: Gas cost increases for state access opcodes"
// https://eips.ethereum.org/EIPS/eip-2929
func enable2929(jt *JumpTable) {
	jt[SSTORE].dynamicGas = gasSStoreEIP2929

	jt[SLOAD].constantGas = 0
	jt[SLOAD].dynamicGas = gasSLoadEIP2929

	jt[EXTCODECOPY].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[EXTCODECOPY].dynamicGas = gasExtCodeCopyEIP2929

	jt[EXTCODESIZE].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[EXTCODESIZE].dynamicGas = gasEip2929AccountCheck

	jt[EXTCODEHASH].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[EXTCODEHASH].dynamicGas = gasEip2929AccountCheck

	jt[BALANCE].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[BALANCE].dynamicGas = gasEip2929AccountCheck

	jt[CALL].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[CALL].dynamicGas = gasCallEIP2929

	jt[CALLCODE].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[CALLCODE].dynamicGas = gasCallCodeEIP2929

	jt[STATICCALL].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[STATICCALL].dynamicGas = gasStaticCallEIP2929

	jt[DELEGATECALL].constantGas = chainparams.WarmStorageReadCostEIP2929
	jt[DELEGATECALL].dynamicGas = gasDelegateCallEIP2929

	// This was previously part of the dynamic cost, but we're using it as a constantGas
	// factor here
	jt[SELFDESTRUCT].constantGas = chainparams.SelfdestructGasEIP150
	jt[SELFDESTRUCT].dynamicGas = gasSelfdestructEIP2929
}

// enable3529 enabled "EIP-3529: Reduction in refunds":
// - Removes refunds for selfdestructs
// - Reduces refunds for SSTORE
// - Reduces max refunds to 20% gas
func enable3529(jt *JumpTable) {
	jt[SSTORE].dynamicGas = gasSStoreEIP3529
	jt[SELFDESTRUCT].dynamicGas = gasSelfdestructEIP3529
}

// enable3198 applies EIP-3198 (BASEFEE Opcode)
// - Adds an opcode that returns the current block's base fee.
func enable3198(jt *JumpTable) {
	// New opcode
	jt[BASEFEE] = &operation{
		execute:     opBaseFee,
		constantGas: GasQuickStep,
		minStack:    minStack(0, 1),
		maxStack:    maxStack(0, 1),
	}
}

// opBaseFee implements BASEFEE opcode
func opBaseFee(pc *uint64, interpreter *PVMInterpreter, scope *ScopeContext) ([]byte, error) {
	baseFee, _ := uint256.FromBig(interpreter.pvm.Context.BaseFee)
	scope.Stack.push(baseFee)
	return nil, nil
}

// enable3855 applies EIP-3855 (PUSH0 opcode)
func enable3855(jt *JumpTable) {
	// New opcode
	jt[PUSH0] = &operation{
		execute:     opPush0,
		constantGas: GasQuickStep,
		minStack:    minStack(0, 1),
		maxStack:    maxStack(0, 1),
	}
}

// opPush0 implements the PUSH0 opcode
func opPush0(pc *uint64, interpreter *PVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int))
	return nil, nil
}
