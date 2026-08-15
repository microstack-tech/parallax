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

package abi

import (
	"reflect"
	"strings"
	"testing"
)

// reassembleSelector rebuilds a canonical selector signature string from a
// parsed SelectorMarshaling. The parser only ever produces elementary type
// strings, "tuple", or "tuple[]", so those are the only cases handled.
func reassembleSelector(sel SelectorMarshaling) (string, error) {
	types, err := reassembleArgTypes(sel.Inputs)
	if err != nil {
		return "", err
	}
	return sel.Name + "(" + strings.Join(types, ",") + ")", nil
}

func reassembleArgTypes(args []ArgumentMarshaling) ([]string, error) {
	types := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg.Type {
		case "tuple", "tuple[]":
			subTypes, err := reassembleArgTypes(arg.Components)
			if err != nil {
				return nil, err
			}
			s := "(" + strings.Join(subTypes, ",") + ")"
			if arg.Type == "tuple[]" {
				s += "[]"
			}
			types = append(types, s)
		default:
			types = append(types, arg.Type)
		}
	}
	return types, nil
}

// FuzzParseSelector fuzzes ParseSelector with arbitrary strings.
//
// Invariants checked per iteration:
//   - no panic on arbitrary input
//   - if parsing succeeds, reassembling the signature from the parsed result
//     and reparsing it must succeed and yield a deeply equal structure
func FuzzParseSelector(f *testing.F) {
	seeds := []string{
		// Valid selectors from selector_parser_test.go.
		"noargs()",
		"simple(uint256,uint256,uint256)",
		"other(uint256,address)",
		"withArray(uint256[],address[2],uint8[4][][5])",
		"singleNest(bytes32,uint8,(uint256,uint256),address)",
		"multiNest(address,(uint256[],uint256),((address,bytes32),uint256))",
		"arrayNest((uint256,uint256)[],bytes32)",
		"multiArrayNest((uint256,uint256)[],(uint256,uint256)[])",
		"singleArrayNestAndArray((uint256,uint256)[],bytes32[])",
		"singleArrayNestWithArrayAndArray((uint256[],address[2],uint8[4][][5])[],bytes32[])",
		// Identifier symbols and malformed inputs.
		"$_ident(uint256)",
		"transfer(address,uint256",
		"f(,)",
		"f(uint256))",
		"",
		"(",
		// Note: a bare identifier with no parenthesis at all (e.g. "f") is
		// a known crasher, kept as a regression seed in
		// testdata/fuzz/FuzzParseSelector/ rather than here. See the bug
		// note on parseCompositeType in selector_parser.go:88.
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		sel, err := ParseSelector(input)
		if err != nil {
			return
		}
		sig, err := reassembleSelector(sel)
		if err != nil {
			t.Fatalf("failed to reassemble signature from parse of %q: %v", input, err)
		}
		sel2, err := ParseSelector(sig)
		if err != nil {
			t.Fatalf("reparse of reassembled signature %q (from %q) failed: %v", sig, input, err)
		}
		if !reflect.DeepEqual(sel, sel2) {
			t.Fatalf("reparse of %q (from %q) yielded a different structure:\n first: %#v\nsecond: %#v", sig, input, sel, sel2)
		}
	})
}
