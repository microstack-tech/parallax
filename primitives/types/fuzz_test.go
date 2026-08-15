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

package types

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"github.com/ParallaxProtocol/parallax/util"
)

// fuzzSeedTxs builds one signed legacy and one signed dynamic fee transaction
// for use as fuzz corpus seeds. The chain ID matches Parallax mainnet (2110).
func fuzzSeedTxs(f *testing.F) []*Transaction {
	key, err := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	if err != nil {
		f.Fatalf("failed to load test key: %v", err)
	}
	var (
		chainID = big.NewInt(2110)
		signer  = LatestSignerForChainID(chainID)
		to      = util.HexToAddress("0xb94f5374fce5edbc8e2a8697c15331677e6ebf0b")
	)
	legacy, err := SignNewTx(key, signer, &LegacyTx{
		Nonce:    3,
		To:       &to,
		Value:    big.NewInt(10),
		Gas:      25000,
		GasPrice: big.NewInt(1),
		Data:     util.FromHex("5544"),
	})
	if err != nil {
		f.Fatalf("failed to sign legacy tx: %v", err)
	}
	dynamic, err := SignNewTx(key, signer, &DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     4,
		To:        &to,
		Value:     big.NewInt(10),
		Gas:       25000,
		GasFeeCap: big.NewInt(500),
		GasTipCap: big.NewInt(2),
		Data:      util.FromHex("aabb"),
		AccessList: AccessList{{
			Address:     to,
			StorageKeys: []util.Hash{{0: 0x01}},
		}},
	})
	if err != nil {
		f.Fatalf("failed to sign dynamic fee tx: %v", err)
	}
	return []*Transaction{legacy, dynamic}
}

// FuzzTransactionUnmarshalJSON fuzzes Transaction.UnmarshalJSON with arbitrary
// JSON input.
//
// Invariants checked per iteration:
//   - no panic on arbitrary input
//   - if UnmarshalJSON succeeds, MarshalJSON must succeed, unmarshalling the
//     marshalled form must succeed, and the round-tripped transaction must
//     have the same hash
func FuzzTransactionUnmarshalJSON(f *testing.F) {
	for _, tx := range fuzzSeedTxs(f) {
		enc, err := tx.MarshalJSON()
		if err != nil {
			f.Fatalf("failed to marshal seed tx: %v", err)
		}
		f.Add(enc)
	}
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"type":"0x2"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var tx Transaction
		if err := tx.UnmarshalJSON(data); err != nil {
			return
		}
		enc, err := tx.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal after successful unmarshal of %q failed: %v", data, err)
		}
		var tx2 Transaction
		if err := tx2.UnmarshalJSON(enc); err != nil {
			t.Fatalf("re-unmarshal of %q (from %q) failed: %v", enc, data, err)
		}
		if tx.Hash() != tx2.Hash() {
			t.Fatalf("hash mismatch after JSON round-trip of %q: %x != %x", data, tx.Hash(), tx2.Hash())
		}
	})
}

// FuzzTransactionRLPDecode fuzzes the two binary decoding paths of
// Transaction: UnmarshalBinary (canonical typed-tx envelope) and
// rlp.DecodeBytes (RLP with typed txs wrapped as byte strings).
//
// Invariants checked per iteration:
//   - no panic on arbitrary input
//   - if decoding succeeds, re-encoding with the matching encoder must
//     succeed and be byte-identical to the input
//   - sender recovery with the latest signer for chain ID 2110 must either
//     return an error or an address, never panic
func FuzzTransactionRLPDecode(f *testing.F) {
	for _, tx := range fuzzSeedTxs(f) {
		bin, err := tx.MarshalBinary()
		if err != nil {
			f.Fatalf("failed to binary-marshal seed tx: %v", err)
		}
		f.Add(bin)
		enc, err := rlp.EncodeToBytes(tx)
		if err != nil {
			f.Fatalf("failed to rlp-encode seed tx: %v", err)
		}
		f.Add(enc)
	}
	f.Add([]byte{0x01})
	f.Add([]byte{0x02})
	f.Add([]byte{0xc0})

	signer := LatestSignerForChainID(big.NewInt(2110))

	f.Fuzz(func(t *testing.T, data []byte) {
		var tx Transaction
		if err := tx.UnmarshalBinary(data); err == nil {
			enc, err := tx.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary after successful UnmarshalBinary of %x failed: %v", data, err)
			}
			if !bytes.Equal(enc, data) {
				t.Fatalf("UnmarshalBinary round-trip mismatch: decoded %x, re-encoded %x", data, enc)
			}
			// Sender recovery must never panic, error or not.
			_, _ = Sender(signer, &tx)
		}

		var tx2 Transaction
		if err := rlp.DecodeBytes(data, &tx2); err == nil {
			enc, err := rlp.EncodeToBytes(&tx2)
			if err != nil {
				t.Fatalf("rlp re-encode after successful decode of %x failed: %v", data, err)
			}
			if !bytes.Equal(enc, data) {
				t.Fatalf("rlp round-trip mismatch: decoded %x, re-encoded %x", data, enc)
			}
			_, _ = Sender(signer, &tx2)
		}
	})
}
