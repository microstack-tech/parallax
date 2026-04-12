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

package light

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/dbstore"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/kernel/xhash"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/script"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/util/math"
	"github.com/ParallaxProtocol/parallax/validation"
	"github.com/ParallaxProtocol/parallax/validation/rawdb"
	"github.com/ParallaxProtocol/parallax/validation/state"
	"github.com/ParallaxProtocol/parallax/validation/trie"
)

var (
	testBankKey, _  = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	testBankAddress = crypto.PubkeyToAddress(testBankKey.PublicKey)
	testBankFunds   = big.NewInt(1_000_000_000_000_000_000)

	acc1Key, _ = crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
	acc2Key, _ = crypto.HexToECDSA("49a7b37aa6f6645917e7b807e9d1c00d4fa71f18343b0d4122a4d2df64dd6fee")
	acc1Addr   = crypto.PubkeyToAddress(acc1Key.PublicKey)
	acc2Addr   = crypto.PubkeyToAddress(acc2Key.PublicKey)

	testContractCode = util.Hex2Bytes("606060405260cc8060106000396000f360606040526000357c01000000000000000000000000000000000000000000000000000000009004806360cd2685146041578063c16431b914606b57603f565b005b6055600480803590602001909190505060a9565b6040518082815260200191505060405180910390f35b60886004808035906020019091908035906020019091905050608a565b005b80600060005083606481101560025790900160005b50819055505b5050565b6000600060005082606481101560025790900160005b5054905060c7565b91905056")
	testContractAddr util.Address
)

type testOdr struct {
	OdrBackend
	indexerConfig *IndexerConfig
	sdb, ldb      dbstore.Database
	disable       bool
}

func (odr *testOdr) Database() dbstore.Database {
	return odr.ldb
}

var ErrOdrDisabled = errors.New("ODR disabled")

func (odr *testOdr) Retrieve(ctx context.Context, req OdrRequest) error {
	if odr.disable {
		return ErrOdrDisabled
	}
	switch req := req.(type) {
	case *BlockRequest:
		number := rawdb.ReadHeaderNumber(odr.sdb, req.Hash)
		if number != nil {
			req.Rlp = rawdb.ReadBodyRLP(odr.sdb, req.Hash, *number)
		}
	case *ReceiptsRequest:
		number := rawdb.ReadHeaderNumber(odr.sdb, req.Hash)
		if number != nil {
			req.Receipts = rawdb.ReadRawReceipts(odr.sdb, req.Hash, *number)
		}
	case *TrieRequest:
		t, _ := trie.New(req.Id.Root, trie.NewDatabase(odr.sdb))
		nodes := NewNodeSet()
		t.Prove(req.Key, 0, nodes)
		req.Proof = nodes
	case *CodeRequest:
		req.Data = rawdb.ReadCode(odr.sdb, req.Hash)
	}
	req.StoreResult(odr.ldb)
	return nil
}

func (odr *testOdr) IndexerConfig() *IndexerConfig {
	return odr.indexerConfig
}

type odrTestFn func(ctx context.Context, db dbstore.Database, bc *validation.BlockChain, lc *LightChain, bhash util.Hash) ([]byte, error)

func TestOdrGetBlockLes2(t *testing.T) { testChainOdr(t, 1, odrGetBlock) }

func odrGetBlock(ctx context.Context, db dbstore.Database, bc *validation.BlockChain, lc *LightChain, bhash util.Hash) ([]byte, error) {
	var block *types.Block
	if bc != nil {
		block = bc.GetBlockByHash(bhash)
	} else {
		block, _ = lc.GetBlockByHash(ctx, bhash)
	}
	if block == nil {
		return nil, nil
	}
	rlp, _ := rlp.EncodeToBytes(block)
	return rlp, nil
}

func TestOdrGetReceiptsLes2(t *testing.T) { testChainOdr(t, 1, odrGetReceipts) }

func odrGetReceipts(ctx context.Context, db dbstore.Database, bc *validation.BlockChain, lc *LightChain, bhash util.Hash) ([]byte, error) {
	var receipts types.Receipts
	if bc != nil {
		number := rawdb.ReadHeaderNumber(db, bhash)
		if number != nil {
			receipts = rawdb.ReadReceipts(db, bhash, *number, bc.Config())
		}
	} else {
		number := rawdb.ReadHeaderNumber(db, bhash)
		if number != nil {
			receipts, _ = GetBlockReceipts(ctx, lc.Odr(), bhash, *number)
		}
	}
	if receipts == nil {
		return nil, nil
	}
	rlp, _ := rlp.EncodeToBytes(receipts)
	return rlp, nil
}

func TestOdrAccountsLes2(t *testing.T) { testChainOdr(t, 1, odrAccounts) }

func odrAccounts(ctx context.Context, db dbstore.Database, bc *validation.BlockChain, lc *LightChain, bhash util.Hash) ([]byte, error) {
	dummyAddr := util.HexToAddress("1234567812345678123456781234567812345678")
	acc := []util.Address{testBankAddress, acc1Addr, acc2Addr, dummyAddr}

	var st *state.StateDB
	if bc == nil {
		header := lc.GetHeaderByHash(bhash)
		st = NewState(ctx, header, lc.Odr())
	} else {
		header := bc.GetHeaderByHash(bhash)
		st, _ = state.New(header.Root, state.NewDatabase(db), nil)
	}

	var res []byte
	for _, addr := range acc {
		bal := st.GetBalance(addr)
		rlp, _ := rlp.EncodeToBytes(bal)
		res = append(res, rlp...)
	}
	return res, st.Error()
}

func TestOdrContractCallLes2(t *testing.T) { testChainOdr(t, 1, odrContractCall) }

type callmsg struct {
	types.Message
}

func (callmsg) CheckNonce() bool { return false }

func odrContractCall(ctx context.Context, db dbstore.Database, bc *validation.BlockChain, lc *LightChain, bhash util.Hash) ([]byte, error) {
	data := util.Hex2Bytes("60CD26850000000000000000000000000000000000000000000000000000000000000000")
	config := chainparams.TestChainConfig

	var res []byte
	for i := 0; i < 3; i++ {
		data[35] = byte(i)

		var (
			st     *state.StateDB
			header *types.Header
			chain  validation.ChainContext
		)
		if bc == nil {
			chain = lc
			header = lc.GetHeaderByHash(bhash)
			st = NewState(ctx, header, lc.Odr())
		} else {
			chain = bc
			header = bc.GetHeaderByHash(bhash)
			st, _ = state.New(header.Root, state.NewDatabase(db), nil)
		}

		// Perform read-only call.
		st.SetBalance(testBankAddress, math.MaxBig256)
		msg := callmsg{types.NewMessage(testBankAddress, &testContractAddr, 0, new(big.Int), 1000000, big.NewInt(chainparams.InitialBaseFee), big.NewInt(chainparams.InitialBaseFee), new(big.Int), data, nil, true)}
		txContext := validation.NewPVMTxContext(msg)
		context := validation.NewPVMBlockContext(header, chain, nil)
		vmenv := script.NewPVM(context, txContext, st, config, script.Config{NoBaseFee: true})
		gp := new(validation.GasPool).AddGas(math.MaxUint64)
		result, _ := validation.ApplyMessage(vmenv, msg, gp)
		res = append(res, result.Return()...)
		if st.Error() != nil {
			return res, st.Error()
		}
	}
	return res, nil
}

func testChainGen(i int, block *validation.BlockGen) {
	signer := types.HomesteadSigner{}
	switch i {
	case 0:
		// In block 1, the test bank sends account #1 some laxes.
		tx, _ := types.SignTx(types.NewTransaction(block.TxNonce(testBankAddress), acc1Addr, big.NewInt(10_000_000_000_000_000), chainparams.TxGas, block.BaseFee(), nil), signer, testBankKey)
		block.AddTx(tx)
	case 1:
		// In block 2, the test bank sends some more laxes to account #1.
		// acc1Addr passes it on to account #2.
		// acc1Addr creates a test contract.
		tx1, _ := types.SignTx(types.NewTransaction(block.TxNonce(testBankAddress), acc1Addr, big.NewInt(1_000_000_000_000_000), chainparams.TxGas, block.BaseFee(), nil), signer, testBankKey)
		nonce := block.TxNonce(acc1Addr)
		tx2, _ := types.SignTx(types.NewTransaction(nonce, acc2Addr, big.NewInt(1_000_000_000_000_000), chainparams.TxGas, block.BaseFee(), nil), signer, acc1Key)
		nonce++
		tx3, _ := types.SignTx(types.NewContractCreation(nonce, big.NewInt(0), 1000000, block.BaseFee(), testContractCode), signer, acc1Key)
		testContractAddr = crypto.CreateAddress(acc1Addr, nonce)
		block.AddTx(tx1)
		block.AddTx(tx2)
		block.AddTx(tx3)
	case 2:
		// Block 3 is empty but was mined by account #2.
		block.SetCoinbase(acc2Addr)
		block.SetExtra([]byte("yeehaw"))
		data := util.Hex2Bytes("C16431B900000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000001")
		tx, _ := types.SignTx(types.NewTransaction(block.TxNonce(testBankAddress), testContractAddr, big.NewInt(0), 100000, block.BaseFee(), data), signer, testBankKey)
		block.AddTx(tx)
	case 3:
		// Block 4 includes blocks 2 and 3 as uncle headers (with modified extra data).
		b2 := block.PrevBlock(1).Header()
		b2.Extra = []byte("foo")
		block.AddUncle(b2)
		b3 := block.PrevBlock(2).Header()
		b3.Extra = []byte("foo")
		block.AddUncle(b3)
		data := util.Hex2Bytes("C16431B900000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000002")
		tx, _ := types.SignTx(types.NewTransaction(block.TxNonce(testBankAddress), testContractAddr, big.NewInt(0), 100000, block.BaseFee(), data), signer, testBankKey)
		block.AddTx(tx)
	}
}

func testChainOdr(t *testing.T, protocol int, fn odrTestFn) {
	var (
		sdb   = rawdb.NewMemoryDatabase()
		ldb   = rawdb.NewMemoryDatabase()
		gspec = validation.Genesis{
			Alloc:   validation.GenesisAlloc{testBankAddress: {Balance: testBankFunds}},
			BaseFee: big.NewInt(chainparams.InitialBaseFee),
		}
		genesis = gspec.MustCommit(sdb)
	)
	gspec.MustCommit(ldb)
	// Assemble the test environment
	blockchain, _ := validation.NewBlockChain(sdb, nil, chainparams.TestChainConfig, xhash.NewFullFaker(), script.Config{}, nil, nil)
	gchain, _ := validation.GenerateChain(chainparams.TestChainConfig, genesis, xhash.NewFaker(), sdb, 4, testChainGen)
	if _, err := blockchain.InsertChain(gchain); err != nil {
		t.Fatal(err)
	}

	odr := &testOdr{sdb: sdb, ldb: ldb, indexerConfig: TestClientIndexerConfig}
	lightchain, err := NewLightChain(odr, chainparams.TestChainConfig, xhash.NewFullFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	headers := make([]*types.Header, len(gchain))
	for i, block := range gchain {
		headers[i] = block.Header()
	}
	if _, err := lightchain.InsertHeaderChain(headers, 1); err != nil {
		t.Fatal(err)
	}

	test := func(expFail int) {
		for i := uint64(0); i <= blockchain.CurrentHeader().Number.Uint64(); i++ {
			bhash := rawdb.ReadCanonicalHash(sdb, i)
			b1, err := fn(NoOdr, sdb, blockchain, nil, bhash)
			if err != nil {
				t.Fatalf("error in full-node test for block %d: %v", i, err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			exp := i < uint64(expFail)
			b2, err := fn(ctx, ldb, nil, lightchain, bhash)
			if err != nil && exp {
				t.Errorf("error in ODR test for block %d: %v", i, err)
			}

			eq := bytes.Equal(b1, b2)
			if exp && !eq {
				t.Errorf("ODR test output for block %d doesn't match full node", i)
			}
		}
	}

	// expect retrievals to fail (except genesis block) without a les peer
	t.Log("checking without ODR")
	odr.disable = true
	test(1)

	// expect all retrievals to pass with ODR enabled
	t.Log("checking with ODR")
	odr.disable = false
	test(len(gchain))

	// still expect all retrievals to pass, now data should be cached locally
	t.Log("checking without ODR, should be cached")
	odr.disable = true
	test(len(gchain))
}
