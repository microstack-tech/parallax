// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package registry

import (
	"errors"
	"math/big"
	"strings"

	parallax "github.com/ParallaxProtocol/parallax/v2"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/script/abi"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/support/event"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

// Reference imports to suppress errors if they are not otherwise used.
var (
	_ = errors.New
	_ = big.NewInt
	_ = strings.NewReader
	_ = parallax.ErrNotFound
	_ = bind.Bind
	_ = util.Big1
	_ = types.BloomLookup
	_ = event.NewSubscription
)

// ParallaxChannelRegistryBalanceProof is an auto generated low-level Go binding around an user-defined struct.
type ParallaxChannelRegistryBalanceProof struct {
	ChannelId       *big.Int
	Seq             uint64
	TransferredAtoB *big.Int
	TransferredBtoA *big.Int
	LocksRoot       [32]byte
	LockedAmount    *big.Int
}

// ParallaxChannelRegistryChannel is an auto generated low-level Go binding around an user-defined struct.
type ParallaxChannelRegistryChannel struct {
	ParticipantA          util.Address
	State                 uint8
	ChallengePeriodBlocks uint32
	OpenedAtBlock         *big.Int
	ParticipantB          util.Address
	CloseInitiatedAtBlock *big.Int
	DepositA              *big.Int
	DepositB              *big.Int
	WithdrawnA            *big.Int
	WithdrawnB            *big.Int
	ClosingTransferredAB  *big.Int
	ClosingTransferredBA  *big.Int
	CloserClaimedBalance  *big.Int
	Closer                util.Address
	ClosingSeq            uint64
	LastChallenger        util.Address
}

// ChannelRegistryMetaData contains all meta data concerning the ChannelRegistry contract.
var ChannelRegistryMetaData = &bind.MetaData{
	ABI: "[{\"type\":\"constructor\",\"inputs\":[{\"name\":\"challengeRefund\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"CHALLENGE_REFUND\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"DOMAIN_SEPARATOR\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"MAX_CHALLENGE_PERIOD\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint32\",\"internalType\":\"uint32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"MIN_CHALLENGE_PERIOD\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint32\",\"internalType\":\"uint32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"PENALTY_BPS\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"SEND_GAS_CAP\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"challenge\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"proof\",\"type\":\"tuple\",\"internalType\":\"structParallaxChannelRegistry.BalanceProof\",\"components\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"seq\",\"type\":\"uint64\",\"internalType\":\"uint64\"},{\"name\":\"transferredAtoB\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"transferredBtoA\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"locksRoot\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"lockedAmount\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]},{\"name\":\"sigA\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"sigB\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"cooperativeClose\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"balanceA\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"balanceB\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"expiryBlock\",\"type\":\"uint64\",\"internalType\":\"uint64\"},{\"name\":\"sigA\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"sigB\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"cooperativeWithdraw\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"participant\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"totalWithdrawn\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"expiryBlock\",\"type\":\"uint64\",\"internalType\":\"uint64\"},{\"name\":\"sigA\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"sigB\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"deposit\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"getChannel\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[{\"name\":\"\",\"type\":\"tuple\",\"internalType\":\"structParallaxChannelRegistry.Channel\",\"components\":[{\"name\":\"participantA\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"state\",\"type\":\"uint8\",\"internalType\":\"uint8\"},{\"name\":\"challengePeriodBlocks\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"openedAtBlock\",\"type\":\"uint40\",\"internalType\":\"uint40\"},{\"name\":\"participantB\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"closeInitiatedAtBlock\",\"type\":\"uint40\",\"internalType\":\"uint40\"},{\"name\":\"depositA\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"depositB\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"withdrawnA\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"withdrawnB\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"closingTransferredAB\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"closingTransferredBA\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"closerClaimedBalance\",\"type\":\"uint128\",\"internalType\":\"uint128\"},{\"name\":\"closer\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"closingSeq\",\"type\":\"uint64\",\"internalType\":\"uint64\"},{\"name\":\"lastChallenger\",\"type\":\"address\",\"internalType\":\"address\"}]}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"hashBalanceProof\",\"inputs\":[{\"name\":\"p\",\"type\":\"tuple\",\"internalType\":\"structParallaxChannelRegistry.BalanceProof\",\"components\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"seq\",\"type\":\"uint64\",\"internalType\":\"uint64\"},{\"name\":\"transferredAtoB\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"transferredBtoA\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"locksRoot\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"lockedAmount\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]}],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"hashCooperativeClose\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"balanceA\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"balanceB\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"expiryBlock\",\"type\":\"uint64\",\"internalType\":\"uint64\"}],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"hashWithdraw\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"participant\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"totalWithdrawn\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"expiryBlock\",\"type\":\"uint64\",\"internalType\":\"uint64\"}],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"nextChannelId\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"open\",\"inputs\":[{\"name\":\"counterparty\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"challengePeriodBlocks\",\"type\":\"uint32\",\"internalType\":\"uint32\"}],\"outputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"pendingPayouts\",\"inputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"settle\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"settlementPreview\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"transferredAtoB\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"transferredBtoA\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[{\"name\":\"balA\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"balB\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"startClose\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"proof\",\"type\":\"tuple\",\"internalType\":\"structParallaxChannelRegistry.BalanceProof\",\"components\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"seq\",\"type\":\"uint64\",\"internalType\":\"uint64\"},{\"name\":\"transferredAtoB\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"transferredBtoA\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"locksRoot\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"lockedAmount\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]},{\"name\":\"sigA\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"sigB\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"startCloseNoProof\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"withdrawPending\",\"inputs\":[],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"event\",\"name\":\"Challenged\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"challenger\",\"type\":\"address\",\"indexed\":false,\"internalType\":\"address\"},{\"name\":\"newSeq\",\"type\":\"uint64\",\"indexed\":false,\"internalType\":\"uint64\"},{\"name\":\"transferredAtoB\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"transferredBtoA\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"ChannelDeposit\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"participant\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"newTotal\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"ChannelOpened\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"participantA\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"participantB\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"challengePeriodBlocks\",\"type\":\"uint32\",\"indexed\":false,\"internalType\":\"uint32\"},{\"name\":\"deposit\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"ChannelWithdraw\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"participant\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"delta\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"totalWithdrawn\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"CloseStarted\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"closer\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"seq\",\"type\":\"uint64\",\"indexed\":false,\"internalType\":\"uint64\"},{\"name\":\"transferredAtoB\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"transferredBtoA\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"deadlineBlock\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"CooperativeClosed\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"balanceA\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"balanceB\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"PayoutDeferred\",\"inputs\":[{\"name\":\"to\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"Settled\",\"inputs\":[{\"name\":\"channelId\",\"type\":\"uint256\",\"indexed\":true,\"internalType\":\"uint256\"},{\"name\":\"balanceA\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"balanceB\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"penaltyBurned\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"refundPaid\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"error\",\"name\":\"AmountTooLarge\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"BadChallengePeriod\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"BadCounterparty\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"BadParticipant\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"BadSignatureA\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"BadSignatureB\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"BalancesMismatch\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"ChallengeWindowClosed\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"ChallengeWindowStillOpen\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"ChannelIdMismatch\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"ECDSAInvalidSignature\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"ECDSAInvalidSignatureLength\",\"inputs\":[{\"name\":\"length\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]},{\"type\":\"error\",\"name\":\"ECDSAInvalidSignatureS\",\"inputs\":[{\"name\":\"s\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}]},{\"type\":\"error\",\"name\":\"Expired\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"InsufficientChannelFunds\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"LocksNotSupported\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"NotClosing\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"NotOpen\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"NotParticipant\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"ReentrancyGuardReentrantCall\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"SeqNotHigher\",\"inputs\":[]},{\"type\":\"error\",\"name\":\"WithdrawNotIncreasing\",\"inputs\":[]}]",
}

// ChannelRegistryABI is the input ABI used to generate the binding from.
// Deprecated: Use ChannelRegistryMetaData.ABI instead.
var ChannelRegistryABI = ChannelRegistryMetaData.ABI

// ChannelRegistry is an auto generated Go binding around an Parallax contract.
type ChannelRegistry struct {
	ChannelRegistryCaller     // Read-only binding to the contract
	ChannelRegistryTransactor // Write-only binding to the contract
	ChannelRegistryFilterer   // Log filterer for contract events
}

// ChannelRegistryCaller is an auto generated read-only Go binding around an Parallax contract.
type ChannelRegistryCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ChannelRegistryTransactor is an auto generated write-only Go binding around an Parallax contract.
type ChannelRegistryTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ChannelRegistryFilterer is an auto generated log filtering Go binding around an Parallax contract events.
type ChannelRegistryFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ChannelRegistrySession is an auto generated Go binding around an Parallax contract,
// with pre-set call and transact options.
type ChannelRegistrySession struct {
	Contract     *ChannelRegistry  // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// ChannelRegistryCallerSession is an auto generated read-only Go binding around an Parallax contract,
// with pre-set call options.
type ChannelRegistryCallerSession struct {
	Contract *ChannelRegistryCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts          // Call options to use throughout this session
}

// ChannelRegistryTransactorSession is an auto generated write-only Go binding around an Parallax contract,
// with pre-set transact options.
type ChannelRegistryTransactorSession struct {
	Contract     *ChannelRegistryTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts          // Transaction auth options to use throughout this session
}

// ChannelRegistryRaw is an auto generated low-level Go binding around an Parallax contract.
type ChannelRegistryRaw struct {
	Contract *ChannelRegistry // Generic contract binding to access the raw methods on
}

// ChannelRegistryCallerRaw is an auto generated low-level read-only Go binding around an Parallax contract.
type ChannelRegistryCallerRaw struct {
	Contract *ChannelRegistryCaller // Generic read-only contract binding to access the raw methods on
}

// ChannelRegistryTransactorRaw is an auto generated low-level write-only Go binding around an Parallax contract.
type ChannelRegistryTransactorRaw struct {
	Contract *ChannelRegistryTransactor // Generic write-only contract binding to access the raw methods on
}

// NewChannelRegistry creates a new instance of ChannelRegistry, bound to a specific deployed contract.
func NewChannelRegistry(address util.Address, backend bind.ContractBackend) (*ChannelRegistry, error) {
	contract, err := bindChannelRegistry(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistry{ChannelRegistryCaller: ChannelRegistryCaller{contract: contract}, ChannelRegistryTransactor: ChannelRegistryTransactor{contract: contract}, ChannelRegistryFilterer: ChannelRegistryFilterer{contract: contract}}, nil
}

// NewChannelRegistryCaller creates a new read-only instance of ChannelRegistry, bound to a specific deployed contract.
func NewChannelRegistryCaller(address util.Address, caller bind.ContractCaller) (*ChannelRegistryCaller, error) {
	contract, err := bindChannelRegistry(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryCaller{contract: contract}, nil
}

// NewChannelRegistryTransactor creates a new write-only instance of ChannelRegistry, bound to a specific deployed contract.
func NewChannelRegistryTransactor(address util.Address, transactor bind.ContractTransactor) (*ChannelRegistryTransactor, error) {
	contract, err := bindChannelRegistry(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryTransactor{contract: contract}, nil
}

// NewChannelRegistryFilterer creates a new log filterer instance of ChannelRegistry, bound to a specific deployed contract.
func NewChannelRegistryFilterer(address util.Address, filterer bind.ContractFilterer) (*ChannelRegistryFilterer, error) {
	contract, err := bindChannelRegistry(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryFilterer{contract: contract}, nil
}

// bindChannelRegistry binds a generic wrapper to an already deployed contract.
func bindChannelRegistry(address util.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := abi.JSON(strings.NewReader(ChannelRegistryABI))
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_ChannelRegistry *ChannelRegistryRaw) Call(opts *bind.CallOpts, result *[]any, method string, params ...any) error {
	return _ChannelRegistry.Contract.ChannelRegistryCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_ChannelRegistry *ChannelRegistryRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.ChannelRegistryTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_ChannelRegistry *ChannelRegistryRaw) Transact(opts *bind.TransactOpts, method string, params ...any) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.ChannelRegistryTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_ChannelRegistry *ChannelRegistryCallerRaw) Call(opts *bind.CallOpts, result *[]any, method string, params ...any) error {
	return _ChannelRegistry.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_ChannelRegistry *ChannelRegistryTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_ChannelRegistry *ChannelRegistryTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...any) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.contract.Transact(opts, method, params...)
}

// CHALLENGEREFUND is a free data retrieval call binding the contract method 0x68ad57d8.
//
// Solidity: function CHALLENGE_REFUND() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCaller) CHALLENGEREFUND(opts *bind.CallOpts) (*big.Int, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "CHALLENGE_REFUND")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// CHALLENGEREFUND is a free data retrieval call binding the contract method 0x68ad57d8.
//
// Solidity: function CHALLENGE_REFUND() view returns(uint256)
func (_ChannelRegistry *ChannelRegistrySession) CHALLENGEREFUND() (*big.Int, error) {
	return _ChannelRegistry.Contract.CHALLENGEREFUND(&_ChannelRegistry.CallOpts)
}

// CHALLENGEREFUND is a free data retrieval call binding the contract method 0x68ad57d8.
//
// Solidity: function CHALLENGE_REFUND() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCallerSession) CHALLENGEREFUND() (*big.Int, error) {
	return _ChannelRegistry.Contract.CHALLENGEREFUND(&_ChannelRegistry.CallOpts)
}

// DOMAINSEPARATOR is a free data retrieval call binding the contract method 0x3644e515.
//
// Solidity: function DOMAIN_SEPARATOR() view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCaller) DOMAINSEPARATOR(opts *bind.CallOpts) ([32]byte, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "DOMAIN_SEPARATOR")

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// DOMAINSEPARATOR is a free data retrieval call binding the contract method 0x3644e515.
//
// Solidity: function DOMAIN_SEPARATOR() view returns(bytes32)
func (_ChannelRegistry *ChannelRegistrySession) DOMAINSEPARATOR() ([32]byte, error) {
	return _ChannelRegistry.Contract.DOMAINSEPARATOR(&_ChannelRegistry.CallOpts)
}

// DOMAINSEPARATOR is a free data retrieval call binding the contract method 0x3644e515.
//
// Solidity: function DOMAIN_SEPARATOR() view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCallerSession) DOMAINSEPARATOR() ([32]byte, error) {
	return _ChannelRegistry.Contract.DOMAINSEPARATOR(&_ChannelRegistry.CallOpts)
}

// MAXCHALLENGEPERIOD is a free data retrieval call binding the contract method 0x80c1e706.
//
// Solidity: function MAX_CHALLENGE_PERIOD() view returns(uint32)
func (_ChannelRegistry *ChannelRegistryCaller) MAXCHALLENGEPERIOD(opts *bind.CallOpts) (uint32, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "MAX_CHALLENGE_PERIOD")

	if err != nil {
		return *new(uint32), err
	}

	out0 := *abi.ConvertType(out[0], new(uint32)).(*uint32)

	return out0, err

}

// MAXCHALLENGEPERIOD is a free data retrieval call binding the contract method 0x80c1e706.
//
// Solidity: function MAX_CHALLENGE_PERIOD() view returns(uint32)
func (_ChannelRegistry *ChannelRegistrySession) MAXCHALLENGEPERIOD() (uint32, error) {
	return _ChannelRegistry.Contract.MAXCHALLENGEPERIOD(&_ChannelRegistry.CallOpts)
}

// MAXCHALLENGEPERIOD is a free data retrieval call binding the contract method 0x80c1e706.
//
// Solidity: function MAX_CHALLENGE_PERIOD() view returns(uint32)
func (_ChannelRegistry *ChannelRegistryCallerSession) MAXCHALLENGEPERIOD() (uint32, error) {
	return _ChannelRegistry.Contract.MAXCHALLENGEPERIOD(&_ChannelRegistry.CallOpts)
}

// MINCHALLENGEPERIOD is a free data retrieval call binding the contract method 0x156ed7e9.
//
// Solidity: function MIN_CHALLENGE_PERIOD() view returns(uint32)
func (_ChannelRegistry *ChannelRegistryCaller) MINCHALLENGEPERIOD(opts *bind.CallOpts) (uint32, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "MIN_CHALLENGE_PERIOD")

	if err != nil {
		return *new(uint32), err
	}

	out0 := *abi.ConvertType(out[0], new(uint32)).(*uint32)

	return out0, err

}

// MINCHALLENGEPERIOD is a free data retrieval call binding the contract method 0x156ed7e9.
//
// Solidity: function MIN_CHALLENGE_PERIOD() view returns(uint32)
func (_ChannelRegistry *ChannelRegistrySession) MINCHALLENGEPERIOD() (uint32, error) {
	return _ChannelRegistry.Contract.MINCHALLENGEPERIOD(&_ChannelRegistry.CallOpts)
}

// MINCHALLENGEPERIOD is a free data retrieval call binding the contract method 0x156ed7e9.
//
// Solidity: function MIN_CHALLENGE_PERIOD() view returns(uint32)
func (_ChannelRegistry *ChannelRegistryCallerSession) MINCHALLENGEPERIOD() (uint32, error) {
	return _ChannelRegistry.Contract.MINCHALLENGEPERIOD(&_ChannelRegistry.CallOpts)
}

// PENALTYBPS is a free data retrieval call binding the contract method 0x1efe5321.
//
// Solidity: function PENALTY_BPS() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCaller) PENALTYBPS(opts *bind.CallOpts) (*big.Int, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "PENALTY_BPS")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// PENALTYBPS is a free data retrieval call binding the contract method 0x1efe5321.
//
// Solidity: function PENALTY_BPS() view returns(uint256)
func (_ChannelRegistry *ChannelRegistrySession) PENALTYBPS() (*big.Int, error) {
	return _ChannelRegistry.Contract.PENALTYBPS(&_ChannelRegistry.CallOpts)
}

// PENALTYBPS is a free data retrieval call binding the contract method 0x1efe5321.
//
// Solidity: function PENALTY_BPS() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCallerSession) PENALTYBPS() (*big.Int, error) {
	return _ChannelRegistry.Contract.PENALTYBPS(&_ChannelRegistry.CallOpts)
}

// SENDGASCAP is a free data retrieval call binding the contract method 0x42beb8e6.
//
// Solidity: function SEND_GAS_CAP() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCaller) SENDGASCAP(opts *bind.CallOpts) (*big.Int, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "SEND_GAS_CAP")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// SENDGASCAP is a free data retrieval call binding the contract method 0x42beb8e6.
//
// Solidity: function SEND_GAS_CAP() view returns(uint256)
func (_ChannelRegistry *ChannelRegistrySession) SENDGASCAP() (*big.Int, error) {
	return _ChannelRegistry.Contract.SENDGASCAP(&_ChannelRegistry.CallOpts)
}

// SENDGASCAP is a free data retrieval call binding the contract method 0x42beb8e6.
//
// Solidity: function SEND_GAS_CAP() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCallerSession) SENDGASCAP() (*big.Int, error) {
	return _ChannelRegistry.Contract.SENDGASCAP(&_ChannelRegistry.CallOpts)
}

// GetChannel is a free data retrieval call binding the contract method 0x10df54a0.
//
// Solidity: function getChannel(uint256 channelId) view returns((address,uint8,uint32,uint40,address,uint40,uint128,uint128,uint128,uint128,uint128,uint128,uint128,address,uint64,address))
func (_ChannelRegistry *ChannelRegistryCaller) GetChannel(opts *bind.CallOpts, channelId *big.Int) (ParallaxChannelRegistryChannel, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "getChannel", channelId)

	if err != nil {
		return *new(ParallaxChannelRegistryChannel), err
	}

	out0 := *abi.ConvertType(out[0], new(ParallaxChannelRegistryChannel)).(*ParallaxChannelRegistryChannel)

	return out0, err

}

// GetChannel is a free data retrieval call binding the contract method 0x10df54a0.
//
// Solidity: function getChannel(uint256 channelId) view returns((address,uint8,uint32,uint40,address,uint40,uint128,uint128,uint128,uint128,uint128,uint128,uint128,address,uint64,address))
func (_ChannelRegistry *ChannelRegistrySession) GetChannel(channelId *big.Int) (ParallaxChannelRegistryChannel, error) {
	return _ChannelRegistry.Contract.GetChannel(&_ChannelRegistry.CallOpts, channelId)
}

// GetChannel is a free data retrieval call binding the contract method 0x10df54a0.
//
// Solidity: function getChannel(uint256 channelId) view returns((address,uint8,uint32,uint40,address,uint40,uint128,uint128,uint128,uint128,uint128,uint128,uint128,address,uint64,address))
func (_ChannelRegistry *ChannelRegistryCallerSession) GetChannel(channelId *big.Int) (ParallaxChannelRegistryChannel, error) {
	return _ChannelRegistry.Contract.GetChannel(&_ChannelRegistry.CallOpts, channelId)
}

// HashBalanceProof is a free data retrieval call binding the contract method 0x752f800b.
//
// Solidity: function hashBalanceProof((uint256,uint64,uint256,uint256,bytes32,uint256) p) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCaller) HashBalanceProof(opts *bind.CallOpts, p ParallaxChannelRegistryBalanceProof) ([32]byte, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "hashBalanceProof", p)

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// HashBalanceProof is a free data retrieval call binding the contract method 0x752f800b.
//
// Solidity: function hashBalanceProof((uint256,uint64,uint256,uint256,bytes32,uint256) p) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistrySession) HashBalanceProof(p ParallaxChannelRegistryBalanceProof) ([32]byte, error) {
	return _ChannelRegistry.Contract.HashBalanceProof(&_ChannelRegistry.CallOpts, p)
}

// HashBalanceProof is a free data retrieval call binding the contract method 0x752f800b.
//
// Solidity: function hashBalanceProof((uint256,uint64,uint256,uint256,bytes32,uint256) p) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCallerSession) HashBalanceProof(p ParallaxChannelRegistryBalanceProof) ([32]byte, error) {
	return _ChannelRegistry.Contract.HashBalanceProof(&_ChannelRegistry.CallOpts, p)
}

// HashCooperativeClose is a free data retrieval call binding the contract method 0xf33e0e93.
//
// Solidity: function hashCooperativeClose(uint256 channelId, uint256 balanceA, uint256 balanceB, uint64 expiryBlock) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCaller) HashCooperativeClose(opts *bind.CallOpts, channelId *big.Int, balanceA *big.Int, balanceB *big.Int, expiryBlock uint64) ([32]byte, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "hashCooperativeClose", channelId, balanceA, balanceB, expiryBlock)

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// HashCooperativeClose is a free data retrieval call binding the contract method 0xf33e0e93.
//
// Solidity: function hashCooperativeClose(uint256 channelId, uint256 balanceA, uint256 balanceB, uint64 expiryBlock) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistrySession) HashCooperativeClose(channelId *big.Int, balanceA *big.Int, balanceB *big.Int, expiryBlock uint64) ([32]byte, error) {
	return _ChannelRegistry.Contract.HashCooperativeClose(&_ChannelRegistry.CallOpts, channelId, balanceA, balanceB, expiryBlock)
}

// HashCooperativeClose is a free data retrieval call binding the contract method 0xf33e0e93.
//
// Solidity: function hashCooperativeClose(uint256 channelId, uint256 balanceA, uint256 balanceB, uint64 expiryBlock) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCallerSession) HashCooperativeClose(channelId *big.Int, balanceA *big.Int, balanceB *big.Int, expiryBlock uint64) ([32]byte, error) {
	return _ChannelRegistry.Contract.HashCooperativeClose(&_ChannelRegistry.CallOpts, channelId, balanceA, balanceB, expiryBlock)
}

// HashWithdraw is a free data retrieval call binding the contract method 0xe3458cf1.
//
// Solidity: function hashWithdraw(uint256 channelId, address participant, uint256 totalWithdrawn, uint64 expiryBlock) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCaller) HashWithdraw(opts *bind.CallOpts, channelId *big.Int, participant util.Address, totalWithdrawn *big.Int, expiryBlock uint64) ([32]byte, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "hashWithdraw", channelId, participant, totalWithdrawn, expiryBlock)

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// HashWithdraw is a free data retrieval call binding the contract method 0xe3458cf1.
//
// Solidity: function hashWithdraw(uint256 channelId, address participant, uint256 totalWithdrawn, uint64 expiryBlock) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistrySession) HashWithdraw(channelId *big.Int, participant util.Address, totalWithdrawn *big.Int, expiryBlock uint64) ([32]byte, error) {
	return _ChannelRegistry.Contract.HashWithdraw(&_ChannelRegistry.CallOpts, channelId, participant, totalWithdrawn, expiryBlock)
}

// HashWithdraw is a free data retrieval call binding the contract method 0xe3458cf1.
//
// Solidity: function hashWithdraw(uint256 channelId, address participant, uint256 totalWithdrawn, uint64 expiryBlock) view returns(bytes32)
func (_ChannelRegistry *ChannelRegistryCallerSession) HashWithdraw(channelId *big.Int, participant util.Address, totalWithdrawn *big.Int, expiryBlock uint64) ([32]byte, error) {
	return _ChannelRegistry.Contract.HashWithdraw(&_ChannelRegistry.CallOpts, channelId, participant, totalWithdrawn, expiryBlock)
}

// NextChannelId is a free data retrieval call binding the contract method 0xf4606f00.
//
// Solidity: function nextChannelId() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCaller) NextChannelId(opts *bind.CallOpts) (*big.Int, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "nextChannelId")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// NextChannelId is a free data retrieval call binding the contract method 0xf4606f00.
//
// Solidity: function nextChannelId() view returns(uint256)
func (_ChannelRegistry *ChannelRegistrySession) NextChannelId() (*big.Int, error) {
	return _ChannelRegistry.Contract.NextChannelId(&_ChannelRegistry.CallOpts)
}

// NextChannelId is a free data retrieval call binding the contract method 0xf4606f00.
//
// Solidity: function nextChannelId() view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCallerSession) NextChannelId() (*big.Int, error) {
	return _ChannelRegistry.Contract.NextChannelId(&_ChannelRegistry.CallOpts)
}

// PendingPayouts is a free data retrieval call binding the contract method 0x784712f2.
//
// Solidity: function pendingPayouts(address ) view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCaller) PendingPayouts(opts *bind.CallOpts, arg0 util.Address) (*big.Int, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "pendingPayouts", arg0)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// PendingPayouts is a free data retrieval call binding the contract method 0x784712f2.
//
// Solidity: function pendingPayouts(address ) view returns(uint256)
func (_ChannelRegistry *ChannelRegistrySession) PendingPayouts(arg0 util.Address) (*big.Int, error) {
	return _ChannelRegistry.Contract.PendingPayouts(&_ChannelRegistry.CallOpts, arg0)
}

// PendingPayouts is a free data retrieval call binding the contract method 0x784712f2.
//
// Solidity: function pendingPayouts(address ) view returns(uint256)
func (_ChannelRegistry *ChannelRegistryCallerSession) PendingPayouts(arg0 util.Address) (*big.Int, error) {
	return _ChannelRegistry.Contract.PendingPayouts(&_ChannelRegistry.CallOpts, arg0)
}

// SettlementPreview is a free data retrieval call binding the contract method 0x804f4a07.
//
// Solidity: function settlementPreview(uint256 channelId, uint256 transferredAtoB, uint256 transferredBtoA) view returns(uint256 balA, uint256 balB)
func (_ChannelRegistry *ChannelRegistryCaller) SettlementPreview(opts *bind.CallOpts, channelId *big.Int, transferredAtoB *big.Int, transferredBtoA *big.Int) (struct {
	BalA *big.Int
	BalB *big.Int
}, error) {
	var out []any
	err := _ChannelRegistry.contract.Call(opts, &out, "settlementPreview", channelId, transferredAtoB, transferredBtoA)

	outstruct := new(struct {
		BalA *big.Int
		BalB *big.Int
	})
	if err != nil {
		return *outstruct, err
	}

	outstruct.BalA = *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)
	outstruct.BalB = *abi.ConvertType(out[1], new(*big.Int)).(**big.Int)

	return *outstruct, err

}

// SettlementPreview is a free data retrieval call binding the contract method 0x804f4a07.
//
// Solidity: function settlementPreview(uint256 channelId, uint256 transferredAtoB, uint256 transferredBtoA) view returns(uint256 balA, uint256 balB)
func (_ChannelRegistry *ChannelRegistrySession) SettlementPreview(channelId *big.Int, transferredAtoB *big.Int, transferredBtoA *big.Int) (struct {
	BalA *big.Int
	BalB *big.Int
}, error) {
	return _ChannelRegistry.Contract.SettlementPreview(&_ChannelRegistry.CallOpts, channelId, transferredAtoB, transferredBtoA)
}

// SettlementPreview is a free data retrieval call binding the contract method 0x804f4a07.
//
// Solidity: function settlementPreview(uint256 channelId, uint256 transferredAtoB, uint256 transferredBtoA) view returns(uint256 balA, uint256 balB)
func (_ChannelRegistry *ChannelRegistryCallerSession) SettlementPreview(channelId *big.Int, transferredAtoB *big.Int, transferredBtoA *big.Int) (struct {
	BalA *big.Int
	BalB *big.Int
}, error) {
	return _ChannelRegistry.Contract.SettlementPreview(&_ChannelRegistry.CallOpts, channelId, transferredAtoB, transferredBtoA)
}

// Challenge is a paid mutator transaction binding the contract method 0xe26ffbf2.
//
// Solidity: function challenge(uint256 channelId, (uint256,uint64,uint256,uint256,bytes32,uint256) proof, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactor) Challenge(opts *bind.TransactOpts, channelId *big.Int, proof ParallaxChannelRegistryBalanceProof, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "challenge", channelId, proof, sigA, sigB)
}

// Challenge is a paid mutator transaction binding the contract method 0xe26ffbf2.
//
// Solidity: function challenge(uint256 channelId, (uint256,uint64,uint256,uint256,bytes32,uint256) proof, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistrySession) Challenge(channelId *big.Int, proof ParallaxChannelRegistryBalanceProof, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Challenge(&_ChannelRegistry.TransactOpts, channelId, proof, sigA, sigB)
}

// Challenge is a paid mutator transaction binding the contract method 0xe26ffbf2.
//
// Solidity: function challenge(uint256 channelId, (uint256,uint64,uint256,uint256,bytes32,uint256) proof, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) Challenge(channelId *big.Int, proof ParallaxChannelRegistryBalanceProof, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Challenge(&_ChannelRegistry.TransactOpts, channelId, proof, sigA, sigB)
}

// CooperativeClose is a paid mutator transaction binding the contract method 0x47b1f79c.
//
// Solidity: function cooperativeClose(uint256 channelId, uint256 balanceA, uint256 balanceB, uint64 expiryBlock, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactor) CooperativeClose(opts *bind.TransactOpts, channelId *big.Int, balanceA *big.Int, balanceB *big.Int, expiryBlock uint64, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "cooperativeClose", channelId, balanceA, balanceB, expiryBlock, sigA, sigB)
}

// CooperativeClose is a paid mutator transaction binding the contract method 0x47b1f79c.
//
// Solidity: function cooperativeClose(uint256 channelId, uint256 balanceA, uint256 balanceB, uint64 expiryBlock, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistrySession) CooperativeClose(channelId *big.Int, balanceA *big.Int, balanceB *big.Int, expiryBlock uint64, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.CooperativeClose(&_ChannelRegistry.TransactOpts, channelId, balanceA, balanceB, expiryBlock, sigA, sigB)
}

// CooperativeClose is a paid mutator transaction binding the contract method 0x47b1f79c.
//
// Solidity: function cooperativeClose(uint256 channelId, uint256 balanceA, uint256 balanceB, uint64 expiryBlock, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) CooperativeClose(channelId *big.Int, balanceA *big.Int, balanceB *big.Int, expiryBlock uint64, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.CooperativeClose(&_ChannelRegistry.TransactOpts, channelId, balanceA, balanceB, expiryBlock, sigA, sigB)
}

// CooperativeWithdraw is a paid mutator transaction binding the contract method 0x3bc4314d.
//
// Solidity: function cooperativeWithdraw(uint256 channelId, address participant, uint256 totalWithdrawn, uint64 expiryBlock, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactor) CooperativeWithdraw(opts *bind.TransactOpts, channelId *big.Int, participant util.Address, totalWithdrawn *big.Int, expiryBlock uint64, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "cooperativeWithdraw", channelId, participant, totalWithdrawn, expiryBlock, sigA, sigB)
}

// CooperativeWithdraw is a paid mutator transaction binding the contract method 0x3bc4314d.
//
// Solidity: function cooperativeWithdraw(uint256 channelId, address participant, uint256 totalWithdrawn, uint64 expiryBlock, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistrySession) CooperativeWithdraw(channelId *big.Int, participant util.Address, totalWithdrawn *big.Int, expiryBlock uint64, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.CooperativeWithdraw(&_ChannelRegistry.TransactOpts, channelId, participant, totalWithdrawn, expiryBlock, sigA, sigB)
}

// CooperativeWithdraw is a paid mutator transaction binding the contract method 0x3bc4314d.
//
// Solidity: function cooperativeWithdraw(uint256 channelId, address participant, uint256 totalWithdrawn, uint64 expiryBlock, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) CooperativeWithdraw(channelId *big.Int, participant util.Address, totalWithdrawn *big.Int, expiryBlock uint64, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.CooperativeWithdraw(&_ChannelRegistry.TransactOpts, channelId, participant, totalWithdrawn, expiryBlock, sigA, sigB)
}

// Deposit is a paid mutator transaction binding the contract method 0xb6b55f25.
//
// Solidity: function deposit(uint256 channelId) payable returns()
func (_ChannelRegistry *ChannelRegistryTransactor) Deposit(opts *bind.TransactOpts, channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "deposit", channelId)
}

// Deposit is a paid mutator transaction binding the contract method 0xb6b55f25.
//
// Solidity: function deposit(uint256 channelId) payable returns()
func (_ChannelRegistry *ChannelRegistrySession) Deposit(channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Deposit(&_ChannelRegistry.TransactOpts, channelId)
}

// Deposit is a paid mutator transaction binding the contract method 0xb6b55f25.
//
// Solidity: function deposit(uint256 channelId) payable returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) Deposit(channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Deposit(&_ChannelRegistry.TransactOpts, channelId)
}

// Open is a paid mutator transaction binding the contract method 0x36ad73bb.
//
// Solidity: function open(address counterparty, uint32 challengePeriodBlocks) payable returns(uint256 channelId)
func (_ChannelRegistry *ChannelRegistryTransactor) Open(opts *bind.TransactOpts, counterparty util.Address, challengePeriodBlocks uint32) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "open", counterparty, challengePeriodBlocks)
}

// Open is a paid mutator transaction binding the contract method 0x36ad73bb.
//
// Solidity: function open(address counterparty, uint32 challengePeriodBlocks) payable returns(uint256 channelId)
func (_ChannelRegistry *ChannelRegistrySession) Open(counterparty util.Address, challengePeriodBlocks uint32) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Open(&_ChannelRegistry.TransactOpts, counterparty, challengePeriodBlocks)
}

// Open is a paid mutator transaction binding the contract method 0x36ad73bb.
//
// Solidity: function open(address counterparty, uint32 challengePeriodBlocks) payable returns(uint256 channelId)
func (_ChannelRegistry *ChannelRegistryTransactorSession) Open(counterparty util.Address, challengePeriodBlocks uint32) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Open(&_ChannelRegistry.TransactOpts, counterparty, challengePeriodBlocks)
}

// Settle is a paid mutator transaction binding the contract method 0x8df82800.
//
// Solidity: function settle(uint256 channelId) returns()
func (_ChannelRegistry *ChannelRegistryTransactor) Settle(opts *bind.TransactOpts, channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "settle", channelId)
}

// Settle is a paid mutator transaction binding the contract method 0x8df82800.
//
// Solidity: function settle(uint256 channelId) returns()
func (_ChannelRegistry *ChannelRegistrySession) Settle(channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Settle(&_ChannelRegistry.TransactOpts, channelId)
}

// Settle is a paid mutator transaction binding the contract method 0x8df82800.
//
// Solidity: function settle(uint256 channelId) returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) Settle(channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.Settle(&_ChannelRegistry.TransactOpts, channelId)
}

// StartClose is a paid mutator transaction binding the contract method 0xa0d109c2.
//
// Solidity: function startClose(uint256 channelId, (uint256,uint64,uint256,uint256,bytes32,uint256) proof, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactor) StartClose(opts *bind.TransactOpts, channelId *big.Int, proof ParallaxChannelRegistryBalanceProof, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "startClose", channelId, proof, sigA, sigB)
}

// StartClose is a paid mutator transaction binding the contract method 0xa0d109c2.
//
// Solidity: function startClose(uint256 channelId, (uint256,uint64,uint256,uint256,bytes32,uint256) proof, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistrySession) StartClose(channelId *big.Int, proof ParallaxChannelRegistryBalanceProof, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.StartClose(&_ChannelRegistry.TransactOpts, channelId, proof, sigA, sigB)
}

// StartClose is a paid mutator transaction binding the contract method 0xa0d109c2.
//
// Solidity: function startClose(uint256 channelId, (uint256,uint64,uint256,uint256,bytes32,uint256) proof, bytes sigA, bytes sigB) returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) StartClose(channelId *big.Int, proof ParallaxChannelRegistryBalanceProof, sigA []byte, sigB []byte) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.StartClose(&_ChannelRegistry.TransactOpts, channelId, proof, sigA, sigB)
}

// StartCloseNoProof is a paid mutator transaction binding the contract method 0x53b7b6bd.
//
// Solidity: function startCloseNoProof(uint256 channelId) returns()
func (_ChannelRegistry *ChannelRegistryTransactor) StartCloseNoProof(opts *bind.TransactOpts, channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "startCloseNoProof", channelId)
}

// StartCloseNoProof is a paid mutator transaction binding the contract method 0x53b7b6bd.
//
// Solidity: function startCloseNoProof(uint256 channelId) returns()
func (_ChannelRegistry *ChannelRegistrySession) StartCloseNoProof(channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.StartCloseNoProof(&_ChannelRegistry.TransactOpts, channelId)
}

// StartCloseNoProof is a paid mutator transaction binding the contract method 0x53b7b6bd.
//
// Solidity: function startCloseNoProof(uint256 channelId) returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) StartCloseNoProof(channelId *big.Int) (*types.Transaction, error) {
	return _ChannelRegistry.Contract.StartCloseNoProof(&_ChannelRegistry.TransactOpts, channelId)
}

// WithdrawPending is a paid mutator transaction binding the contract method 0x7edbceb1.
//
// Solidity: function withdrawPending() returns()
func (_ChannelRegistry *ChannelRegistryTransactor) WithdrawPending(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _ChannelRegistry.contract.Transact(opts, "withdrawPending")
}

// WithdrawPending is a paid mutator transaction binding the contract method 0x7edbceb1.
//
// Solidity: function withdrawPending() returns()
func (_ChannelRegistry *ChannelRegistrySession) WithdrawPending() (*types.Transaction, error) {
	return _ChannelRegistry.Contract.WithdrawPending(&_ChannelRegistry.TransactOpts)
}

// WithdrawPending is a paid mutator transaction binding the contract method 0x7edbceb1.
//
// Solidity: function withdrawPending() returns()
func (_ChannelRegistry *ChannelRegistryTransactorSession) WithdrawPending() (*types.Transaction, error) {
	return _ChannelRegistry.Contract.WithdrawPending(&_ChannelRegistry.TransactOpts)
}

// ChannelRegistryChallengedIterator is returned from FilterChallenged and is used to iterate over the raw logs and unpacked data for Challenged events raised by the ChannelRegistry contract.
type ChannelRegistryChallengedIterator struct {
	Event *ChannelRegistryChallenged // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryChallengedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryChallenged)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryChallenged)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryChallengedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryChallengedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryChallenged represents a Challenged event raised by the ChannelRegistry contract.
type ChannelRegistryChallenged struct {
	ChannelId       *big.Int
	Challenger      util.Address
	NewSeq          uint64
	TransferredAtoB *big.Int
	TransferredBtoA *big.Int
	Raw             types.Log // Blockchain specific contextual infos
}

// FilterChallenged is a free log retrieval operation binding the contract event 0x545056f3a6c672fa185c42e14b7929d0c068555314e7f6ac2569b9f8b33c78e5.
//
// Solidity: event Challenged(uint256 indexed channelId, address challenger, uint64 newSeq, uint256 transferredAtoB, uint256 transferredBtoA)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterChallenged(opts *bind.FilterOpts, channelId []*big.Int) (*ChannelRegistryChallengedIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "Challenged", channelIdRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryChallengedIterator{contract: _ChannelRegistry.contract, event: "Challenged", logs: logs, sub: sub}, nil
}

// WatchChallenged is a free log subscription operation binding the contract event 0x545056f3a6c672fa185c42e14b7929d0c068555314e7f6ac2569b9f8b33c78e5.
//
// Solidity: event Challenged(uint256 indexed channelId, address challenger, uint64 newSeq, uint256 transferredAtoB, uint256 transferredBtoA)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchChallenged(opts *bind.WatchOpts, sink chan<- *ChannelRegistryChallenged, channelId []*big.Int) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "Challenged", channelIdRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryChallenged)
				if err := _ChannelRegistry.contract.UnpackLog(event, "Challenged", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseChallenged is a log parse operation binding the contract event 0x545056f3a6c672fa185c42e14b7929d0c068555314e7f6ac2569b9f8b33c78e5.
//
// Solidity: event Challenged(uint256 indexed channelId, address challenger, uint64 newSeq, uint256 transferredAtoB, uint256 transferredBtoA)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseChallenged(log types.Log) (*ChannelRegistryChallenged, error) {
	event := new(ChannelRegistryChallenged)
	if err := _ChannelRegistry.contract.UnpackLog(event, "Challenged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistryChannelDepositIterator is returned from FilterChannelDeposit and is used to iterate over the raw logs and unpacked data for ChannelDeposit events raised by the ChannelRegistry contract.
type ChannelRegistryChannelDepositIterator struct {
	Event *ChannelRegistryChannelDeposit // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryChannelDepositIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryChannelDeposit)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryChannelDeposit)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryChannelDepositIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryChannelDepositIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryChannelDeposit represents a ChannelDeposit event raised by the ChannelRegistry contract.
type ChannelRegistryChannelDeposit struct {
	ChannelId   *big.Int
	Participant util.Address
	Amount      *big.Int
	NewTotal    *big.Int
	Raw         types.Log // Blockchain specific contextual infos
}

// FilterChannelDeposit is a free log retrieval operation binding the contract event 0x3b743d1872806d5c12a3e2342322dc5ce75c67282621b15c54833ea3a034ecde.
//
// Solidity: event ChannelDeposit(uint256 indexed channelId, address indexed participant, uint256 amount, uint256 newTotal)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterChannelDeposit(opts *bind.FilterOpts, channelId []*big.Int, participant []util.Address) (*ChannelRegistryChannelDepositIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var participantRule []any
	for _, participantItem := range participant {
		participantRule = append(participantRule, participantItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "ChannelDeposit", channelIdRule, participantRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryChannelDepositIterator{contract: _ChannelRegistry.contract, event: "ChannelDeposit", logs: logs, sub: sub}, nil
}

// WatchChannelDeposit is a free log subscription operation binding the contract event 0x3b743d1872806d5c12a3e2342322dc5ce75c67282621b15c54833ea3a034ecde.
//
// Solidity: event ChannelDeposit(uint256 indexed channelId, address indexed participant, uint256 amount, uint256 newTotal)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchChannelDeposit(opts *bind.WatchOpts, sink chan<- *ChannelRegistryChannelDeposit, channelId []*big.Int, participant []util.Address) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var participantRule []any
	for _, participantItem := range participant {
		participantRule = append(participantRule, participantItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "ChannelDeposit", channelIdRule, participantRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryChannelDeposit)
				if err := _ChannelRegistry.contract.UnpackLog(event, "ChannelDeposit", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseChannelDeposit is a log parse operation binding the contract event 0x3b743d1872806d5c12a3e2342322dc5ce75c67282621b15c54833ea3a034ecde.
//
// Solidity: event ChannelDeposit(uint256 indexed channelId, address indexed participant, uint256 amount, uint256 newTotal)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseChannelDeposit(log types.Log) (*ChannelRegistryChannelDeposit, error) {
	event := new(ChannelRegistryChannelDeposit)
	if err := _ChannelRegistry.contract.UnpackLog(event, "ChannelDeposit", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistryChannelOpenedIterator is returned from FilterChannelOpened and is used to iterate over the raw logs and unpacked data for ChannelOpened events raised by the ChannelRegistry contract.
type ChannelRegistryChannelOpenedIterator struct {
	Event *ChannelRegistryChannelOpened // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryChannelOpenedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryChannelOpened)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryChannelOpened)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryChannelOpenedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryChannelOpenedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryChannelOpened represents a ChannelOpened event raised by the ChannelRegistry contract.
type ChannelRegistryChannelOpened struct {
	ChannelId             *big.Int
	ParticipantA          util.Address
	ParticipantB          util.Address
	ChallengePeriodBlocks uint32
	Deposit               *big.Int
	Raw                   types.Log // Blockchain specific contextual infos
}

// FilterChannelOpened is a free log retrieval operation binding the contract event 0x75c285a23d828c8a98d61a4e97c54784967994e4a229e17305fd751a821c697c.
//
// Solidity: event ChannelOpened(uint256 indexed channelId, address indexed participantA, address indexed participantB, uint32 challengePeriodBlocks, uint256 deposit)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterChannelOpened(opts *bind.FilterOpts, channelId []*big.Int, participantA []util.Address, participantB []util.Address) (*ChannelRegistryChannelOpenedIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var participantARule []any
	for _, participantAItem := range participantA {
		participantARule = append(participantARule, participantAItem)
	}
	var participantBRule []any
	for _, participantBItem := range participantB {
		participantBRule = append(participantBRule, participantBItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "ChannelOpened", channelIdRule, participantARule, participantBRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryChannelOpenedIterator{contract: _ChannelRegistry.contract, event: "ChannelOpened", logs: logs, sub: sub}, nil
}

// WatchChannelOpened is a free log subscription operation binding the contract event 0x75c285a23d828c8a98d61a4e97c54784967994e4a229e17305fd751a821c697c.
//
// Solidity: event ChannelOpened(uint256 indexed channelId, address indexed participantA, address indexed participantB, uint32 challengePeriodBlocks, uint256 deposit)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchChannelOpened(opts *bind.WatchOpts, sink chan<- *ChannelRegistryChannelOpened, channelId []*big.Int, participantA []util.Address, participantB []util.Address) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var participantARule []any
	for _, participantAItem := range participantA {
		participantARule = append(participantARule, participantAItem)
	}
	var participantBRule []any
	for _, participantBItem := range participantB {
		participantBRule = append(participantBRule, participantBItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "ChannelOpened", channelIdRule, participantARule, participantBRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryChannelOpened)
				if err := _ChannelRegistry.contract.UnpackLog(event, "ChannelOpened", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseChannelOpened is a log parse operation binding the contract event 0x75c285a23d828c8a98d61a4e97c54784967994e4a229e17305fd751a821c697c.
//
// Solidity: event ChannelOpened(uint256 indexed channelId, address indexed participantA, address indexed participantB, uint32 challengePeriodBlocks, uint256 deposit)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseChannelOpened(log types.Log) (*ChannelRegistryChannelOpened, error) {
	event := new(ChannelRegistryChannelOpened)
	if err := _ChannelRegistry.contract.UnpackLog(event, "ChannelOpened", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistryChannelWithdrawIterator is returned from FilterChannelWithdraw and is used to iterate over the raw logs and unpacked data for ChannelWithdraw events raised by the ChannelRegistry contract.
type ChannelRegistryChannelWithdrawIterator struct {
	Event *ChannelRegistryChannelWithdraw // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryChannelWithdrawIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryChannelWithdraw)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryChannelWithdraw)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryChannelWithdrawIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryChannelWithdrawIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryChannelWithdraw represents a ChannelWithdraw event raised by the ChannelRegistry contract.
type ChannelRegistryChannelWithdraw struct {
	ChannelId      *big.Int
	Participant    util.Address
	Delta          *big.Int
	TotalWithdrawn *big.Int
	Raw            types.Log // Blockchain specific contextual infos
}

// FilterChannelWithdraw is a free log retrieval operation binding the contract event 0x665d00f8caaca53826b87aacea1a2efbc275b906035a1a7fae45efa81b7a16ad.
//
// Solidity: event ChannelWithdraw(uint256 indexed channelId, address indexed participant, uint256 delta, uint256 totalWithdrawn)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterChannelWithdraw(opts *bind.FilterOpts, channelId []*big.Int, participant []util.Address) (*ChannelRegistryChannelWithdrawIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var participantRule []any
	for _, participantItem := range participant {
		participantRule = append(participantRule, participantItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "ChannelWithdraw", channelIdRule, participantRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryChannelWithdrawIterator{contract: _ChannelRegistry.contract, event: "ChannelWithdraw", logs: logs, sub: sub}, nil
}

// WatchChannelWithdraw is a free log subscription operation binding the contract event 0x665d00f8caaca53826b87aacea1a2efbc275b906035a1a7fae45efa81b7a16ad.
//
// Solidity: event ChannelWithdraw(uint256 indexed channelId, address indexed participant, uint256 delta, uint256 totalWithdrawn)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchChannelWithdraw(opts *bind.WatchOpts, sink chan<- *ChannelRegistryChannelWithdraw, channelId []*big.Int, participant []util.Address) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var participantRule []any
	for _, participantItem := range participant {
		participantRule = append(participantRule, participantItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "ChannelWithdraw", channelIdRule, participantRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryChannelWithdraw)
				if err := _ChannelRegistry.contract.UnpackLog(event, "ChannelWithdraw", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseChannelWithdraw is a log parse operation binding the contract event 0x665d00f8caaca53826b87aacea1a2efbc275b906035a1a7fae45efa81b7a16ad.
//
// Solidity: event ChannelWithdraw(uint256 indexed channelId, address indexed participant, uint256 delta, uint256 totalWithdrawn)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseChannelWithdraw(log types.Log) (*ChannelRegistryChannelWithdraw, error) {
	event := new(ChannelRegistryChannelWithdraw)
	if err := _ChannelRegistry.contract.UnpackLog(event, "ChannelWithdraw", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistryCloseStartedIterator is returned from FilterCloseStarted and is used to iterate over the raw logs and unpacked data for CloseStarted events raised by the ChannelRegistry contract.
type ChannelRegistryCloseStartedIterator struct {
	Event *ChannelRegistryCloseStarted // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryCloseStartedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryCloseStarted)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryCloseStarted)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryCloseStartedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryCloseStartedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryCloseStarted represents a CloseStarted event raised by the ChannelRegistry contract.
type ChannelRegistryCloseStarted struct {
	ChannelId       *big.Int
	Closer          util.Address
	Seq             uint64
	TransferredAtoB *big.Int
	TransferredBtoA *big.Int
	DeadlineBlock   *big.Int
	Raw             types.Log // Blockchain specific contextual infos
}

// FilterCloseStarted is a free log retrieval operation binding the contract event 0xb8da688f7e959ea30eb2d21583893c73a9852ad15ec35b630ac22aad6397e2ae.
//
// Solidity: event CloseStarted(uint256 indexed channelId, address indexed closer, uint64 seq, uint256 transferredAtoB, uint256 transferredBtoA, uint256 deadlineBlock)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterCloseStarted(opts *bind.FilterOpts, channelId []*big.Int, closer []util.Address) (*ChannelRegistryCloseStartedIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var closerRule []any
	for _, closerItem := range closer {
		closerRule = append(closerRule, closerItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "CloseStarted", channelIdRule, closerRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryCloseStartedIterator{contract: _ChannelRegistry.contract, event: "CloseStarted", logs: logs, sub: sub}, nil
}

// WatchCloseStarted is a free log subscription operation binding the contract event 0xb8da688f7e959ea30eb2d21583893c73a9852ad15ec35b630ac22aad6397e2ae.
//
// Solidity: event CloseStarted(uint256 indexed channelId, address indexed closer, uint64 seq, uint256 transferredAtoB, uint256 transferredBtoA, uint256 deadlineBlock)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchCloseStarted(opts *bind.WatchOpts, sink chan<- *ChannelRegistryCloseStarted, channelId []*big.Int, closer []util.Address) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}
	var closerRule []any
	for _, closerItem := range closer {
		closerRule = append(closerRule, closerItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "CloseStarted", channelIdRule, closerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryCloseStarted)
				if err := _ChannelRegistry.contract.UnpackLog(event, "CloseStarted", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseCloseStarted is a log parse operation binding the contract event 0xb8da688f7e959ea30eb2d21583893c73a9852ad15ec35b630ac22aad6397e2ae.
//
// Solidity: event CloseStarted(uint256 indexed channelId, address indexed closer, uint64 seq, uint256 transferredAtoB, uint256 transferredBtoA, uint256 deadlineBlock)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseCloseStarted(log types.Log) (*ChannelRegistryCloseStarted, error) {
	event := new(ChannelRegistryCloseStarted)
	if err := _ChannelRegistry.contract.UnpackLog(event, "CloseStarted", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistryCooperativeClosedIterator is returned from FilterCooperativeClosed and is used to iterate over the raw logs and unpacked data for CooperativeClosed events raised by the ChannelRegistry contract.
type ChannelRegistryCooperativeClosedIterator struct {
	Event *ChannelRegistryCooperativeClosed // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryCooperativeClosedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryCooperativeClosed)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryCooperativeClosed)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryCooperativeClosedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryCooperativeClosedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryCooperativeClosed represents a CooperativeClosed event raised by the ChannelRegistry contract.
type ChannelRegistryCooperativeClosed struct {
	ChannelId *big.Int
	BalanceA  *big.Int
	BalanceB  *big.Int
	Raw       types.Log // Blockchain specific contextual infos
}

// FilterCooperativeClosed is a free log retrieval operation binding the contract event 0xaa4632b454c1a4540b117fdb9c3ff92ba145bba29b9810f91310152395da1b0c.
//
// Solidity: event CooperativeClosed(uint256 indexed channelId, uint256 balanceA, uint256 balanceB)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterCooperativeClosed(opts *bind.FilterOpts, channelId []*big.Int) (*ChannelRegistryCooperativeClosedIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "CooperativeClosed", channelIdRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryCooperativeClosedIterator{contract: _ChannelRegistry.contract, event: "CooperativeClosed", logs: logs, sub: sub}, nil
}

// WatchCooperativeClosed is a free log subscription operation binding the contract event 0xaa4632b454c1a4540b117fdb9c3ff92ba145bba29b9810f91310152395da1b0c.
//
// Solidity: event CooperativeClosed(uint256 indexed channelId, uint256 balanceA, uint256 balanceB)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchCooperativeClosed(opts *bind.WatchOpts, sink chan<- *ChannelRegistryCooperativeClosed, channelId []*big.Int) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "CooperativeClosed", channelIdRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryCooperativeClosed)
				if err := _ChannelRegistry.contract.UnpackLog(event, "CooperativeClosed", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseCooperativeClosed is a log parse operation binding the contract event 0xaa4632b454c1a4540b117fdb9c3ff92ba145bba29b9810f91310152395da1b0c.
//
// Solidity: event CooperativeClosed(uint256 indexed channelId, uint256 balanceA, uint256 balanceB)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseCooperativeClosed(log types.Log) (*ChannelRegistryCooperativeClosed, error) {
	event := new(ChannelRegistryCooperativeClosed)
	if err := _ChannelRegistry.contract.UnpackLog(event, "CooperativeClosed", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistryPayoutDeferredIterator is returned from FilterPayoutDeferred and is used to iterate over the raw logs and unpacked data for PayoutDeferred events raised by the ChannelRegistry contract.
type ChannelRegistryPayoutDeferredIterator struct {
	Event *ChannelRegistryPayoutDeferred // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistryPayoutDeferredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistryPayoutDeferred)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistryPayoutDeferred)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistryPayoutDeferredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistryPayoutDeferredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistryPayoutDeferred represents a PayoutDeferred event raised by the ChannelRegistry contract.
type ChannelRegistryPayoutDeferred struct {
	To     util.Address
	Amount *big.Int
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterPayoutDeferred is a free log retrieval operation binding the contract event 0x0e54be18cea4b7c02dcb455aa29944656f9f30fbb0ba328fe8a75d10e52511dd.
//
// Solidity: event PayoutDeferred(address indexed to, uint256 amount)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterPayoutDeferred(opts *bind.FilterOpts, to []util.Address) (*ChannelRegistryPayoutDeferredIterator, error) {

	var toRule []any
	for _, toItem := range to {
		toRule = append(toRule, toItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "PayoutDeferred", toRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistryPayoutDeferredIterator{contract: _ChannelRegistry.contract, event: "PayoutDeferred", logs: logs, sub: sub}, nil
}

// WatchPayoutDeferred is a free log subscription operation binding the contract event 0x0e54be18cea4b7c02dcb455aa29944656f9f30fbb0ba328fe8a75d10e52511dd.
//
// Solidity: event PayoutDeferred(address indexed to, uint256 amount)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchPayoutDeferred(opts *bind.WatchOpts, sink chan<- *ChannelRegistryPayoutDeferred, to []util.Address) (event.Subscription, error) {

	var toRule []any
	for _, toItem := range to {
		toRule = append(toRule, toItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "PayoutDeferred", toRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistryPayoutDeferred)
				if err := _ChannelRegistry.contract.UnpackLog(event, "PayoutDeferred", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParsePayoutDeferred is a log parse operation binding the contract event 0x0e54be18cea4b7c02dcb455aa29944656f9f30fbb0ba328fe8a75d10e52511dd.
//
// Solidity: event PayoutDeferred(address indexed to, uint256 amount)
func (_ChannelRegistry *ChannelRegistryFilterer) ParsePayoutDeferred(log types.Log) (*ChannelRegistryPayoutDeferred, error) {
	event := new(ChannelRegistryPayoutDeferred)
	if err := _ChannelRegistry.contract.UnpackLog(event, "PayoutDeferred", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ChannelRegistrySettledIterator is returned from FilterSettled and is used to iterate over the raw logs and unpacked data for Settled events raised by the ChannelRegistry contract.
type ChannelRegistrySettledIterator struct {
	Event *ChannelRegistrySettled // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  parallax.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *ChannelRegistrySettledIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ChannelRegistrySettled)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(ChannelRegistrySettled)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *ChannelRegistrySettledIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ChannelRegistrySettledIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ChannelRegistrySettled represents a Settled event raised by the ChannelRegistry contract.
type ChannelRegistrySettled struct {
	ChannelId     *big.Int
	BalanceA      *big.Int
	BalanceB      *big.Int
	PenaltyBurned *big.Int
	RefundPaid    *big.Int
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterSettled is a free log retrieval operation binding the contract event 0xa973b04048409154dba42dd56f3d2787fcfae851d65aa083ef6231bf57e53669.
//
// Solidity: event Settled(uint256 indexed channelId, uint256 balanceA, uint256 balanceB, uint256 penaltyBurned, uint256 refundPaid)
func (_ChannelRegistry *ChannelRegistryFilterer) FilterSettled(opts *bind.FilterOpts, channelId []*big.Int) (*ChannelRegistrySettledIterator, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}

	logs, sub, err := _ChannelRegistry.contract.FilterLogs(opts, "Settled", channelIdRule)
	if err != nil {
		return nil, err
	}
	return &ChannelRegistrySettledIterator{contract: _ChannelRegistry.contract, event: "Settled", logs: logs, sub: sub}, nil
}

// WatchSettled is a free log subscription operation binding the contract event 0xa973b04048409154dba42dd56f3d2787fcfae851d65aa083ef6231bf57e53669.
//
// Solidity: event Settled(uint256 indexed channelId, uint256 balanceA, uint256 balanceB, uint256 penaltyBurned, uint256 refundPaid)
func (_ChannelRegistry *ChannelRegistryFilterer) WatchSettled(opts *bind.WatchOpts, sink chan<- *ChannelRegistrySettled, channelId []*big.Int) (event.Subscription, error) {

	var channelIdRule []any
	for _, channelIdItem := range channelId {
		channelIdRule = append(channelIdRule, channelIdItem)
	}

	logs, sub, err := _ChannelRegistry.contract.WatchLogs(opts, "Settled", channelIdRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ChannelRegistrySettled)
				if err := _ChannelRegistry.contract.UnpackLog(event, "Settled", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseSettled is a log parse operation binding the contract event 0xa973b04048409154dba42dd56f3d2787fcfae851d65aa083ef6231bf57e53669.
//
// Solidity: event Settled(uint256 indexed channelId, uint256 balanceA, uint256 balanceB, uint256 penaltyBurned, uint256 refundPaid)
func (_ChannelRegistry *ChannelRegistryFilterer) ParseSettled(log types.Log) (*ChannelRegistrySettled, error) {
	event := new(ChannelRegistrySettled)
	if err := _ChannelRegistry.contract.UnpackLog(event, "Settled", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
