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

package fourbyte

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/wallet/signer/core/apitypes"
)

// FuzzFourbyteValidateCallData drives Database.ValidateCallData with arbitrary
// call data and an optional custom method selector. It checks that validation
// never panics and never produces an unbounded number of messages.
//
// Note: wallet/signer/core/testdata/fuzzing contains legacy crash artifacts, but
// they are EIP-712 typed-data JSON aimed at the signer/core typed-data path, not
// 4byte call data, so they are not applicable here and are not used as seeds.
func FuzzFourbyteValidateCallData(f *testing.F) {
	// Build the embedded database once; the fuzz body only reads from it (and
	// may append custom selectors, which is bounded by the 4-byte prefix space).
	db, err := New()
	if err != nil {
		f.Fatalf("failed to build 4byte database: %v", err)
	}

	// selector for transfer(address,uint256) with a well-formed 68-byte payload.
	transfer := append(util.FromHex("0xa9059cbb"), make([]byte, 64)...)
	f.Add(transfer, "transfer(address,uint256)")
	f.Add(transfer, "")
	f.Add([]byte{}, "")
	f.Add([]byte{0x01, 0x02, 0x03}, "")                 // shorter than a selector
	f.Add(util.FromHex("0xa9059cbb"), "")               // selector only, no args
	f.Add(append(util.FromHex("0xa9059cbb"), 0x00), "") // args not a multiple of 32
	f.Add(transfer, "not a valid selector(")
	f.Add(transfer, "\x00\xff nonsense")

	f.Fuzz(func(t *testing.T, data []byte, selector string) {
		messages := new(apitypes.ValidationMessages)
		var sel *string
		if selector != "" {
			sel = &selector
		}
		db.ValidateCallData(sel, data, messages)

		// Output must stay bounded: ValidateCallData emits only a handful of
		// messages regardless of input size.
		if n := len(messages.Messages); n > 16 {
			t.Fatalf("ValidateCallData produced an unbounded number of messages: %d", n)
		}
	})
}
