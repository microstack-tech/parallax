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

package rlp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/primitives/rlp"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
)

// fuzzDecodeEncode decodes input into val and, if decoding succeeds, checks
// that re-encoding round-trips to the identical bytes.
func fuzzDecodeEncode(t *testing.T, input []byte, val any, i int) {
	if err := rlp.DecodeBytes(input, val); err == nil {
		output, err := rlp.EncodeToBytes(val)
		if err != nil {
			t.Fatalf("case %d: re-encode failed after successful decode: %v", i, err)
		}
		if !bytes.Equal(input, output) {
			t.Fatalf("case %d: encode-decode is not equal,\ninput : %x\noutput: %x", i, input, output)
		}
	}
}

// FuzzRLP is the native port of the legacy go-fuzz target from
// tests/fuzzers/rlp. It exercises the low-level split/count helpers, stream
// decoding into interfaces, and decode/re-encode round-trips through a range
// of struct shapes and the consensus types.
func FuzzRLP(f *testing.F) {
	// Seed with the legacy go-fuzz corpus (one raw input per file).
	seeds, err := os.ReadDir(filepath.Join("testdata", "fuzz-seeds"))
	if err != nil {
		f.Fatalf("reading seed dir: %v", err)
	}
	for _, entry := range seeds {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("testdata", "fuzz-seeds", entry.Name()))
		if err != nil {
			f.Fatalf("reading seed %s: %v", entry.Name(), err)
		}
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) == 0 {
			return
		}

		var i int
		{
			rlp.Split(input)
		}
		{
			if elems, _, err := rlp.SplitList(input); err == nil {
				rlp.CountValues(elems)
			}
		}
		{
			rlp.NewStream(bytes.NewReader(input), 0).Decode(new(any))
		}
		{
			fuzzDecodeEncode(t, input, new(any), i)
			i++
		}
		{
			var v struct {
				Int    uint
				String string
				Bytes  []byte
			}
			fuzzDecodeEncode(t, input, &v, i)
			i++
		}
		{
			type Types struct {
				Bool  bool
				Raw   rlp.RawValue
				Slice []*Types
				Iface []any
			}
			var v Types
			fuzzDecodeEncode(t, input, &v, i)
			i++
		}
		{
			type AllTypes struct {
				Int    uint
				String string
				Bytes  []byte
				Bool   bool
				Raw    rlp.RawValue
				Slice  []*AllTypes
				Array  [3]*AllTypes
				Iface  []any
			}
			var v AllTypes
			fuzzDecodeEncode(t, input, &v, i)
			i++
		}
		{
			// Kept as a non-pointer target to mirror the legacy fuzzer; the
			// decoder rejects non-pointer targets, so this exercises only the
			// error path.
			fuzzDecodeEncode(t, input, [10]byte{}, i)
			i++
		}
		{
			var v struct {
				Byte [10]byte
				Rool [10]bool
			}
			fuzzDecodeEncode(t, input, &v, i)
			i++
		}
		{
			var h types.Header
			fuzzDecodeEncode(t, input, &h, i)
			i++
			var b types.Block
			fuzzDecodeEncode(t, input, &b, i)
			i++
			var tx types.Transaction
			fuzzDecodeEncode(t, input, &tx, i)
			i++
			var txs types.Transactions
			fuzzDecodeEncode(t, input, &txs, i)
			i++
			var rs types.Receipts
			fuzzDecodeEncode(t, input, &rs, i)
		}
	})
}
