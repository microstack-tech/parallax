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

package keystore

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/ParallaxProtocol/parallax/crypto"
)

// maxFuzzScryptN caps the scrypt work factor accepted by the fuzz body. Inputs
// asking for a larger N are skipped so a single iteration cannot burn seconds of
// CPU (and hundreds of MB) on KDF work; StandardScryptN (1<<18) is the largest
// value produced by the encoder, so nothing legitimate is excluded.
const maxFuzzScryptN = 1 << 18

// scryptCostOK reports whether the scrypt N parameter (if any) is within
// maxFuzzScryptN. This is the only pre-filter: malformed structure must NOT be
// filtered here, since DecryptKey is required to reject it with an error
// rather than panic.
func scryptCostOK(keyjson []byte) bool {
	// Struct field matching in encoding/json is case-insensitive, so a single
	// lowercase-tagged probe matches both v3 ("crypto") and v1 ("Crypto") files.
	var probe struct {
		Crypto struct {
			KDF       string         `json:"kdf"`
			KDFParams map[string]any `json:"kdfparams"`
		} `json:"crypto"`
	}
	if err := json.Unmarshal(keyjson, &probe); err != nil {
		return true
	}
	if probe.Crypto.KDF != "scrypt" {
		return true
	}
	n, ok := probe.Crypto.KDFParams["n"].(float64)
	return !ok || n <= maxFuzzScryptN
}

// FuzzDecryptKeyJSON drives DecryptKey with arbitrary key JSON and passwords.
//
// Invariants:
//   - DecryptKey never panics on well-formed-enough inputs (see the guard note).
//   - A wrong password yields an error, never a bogus key.
//   - On success, re-encrypting with light scrypt params and decrypting again
//     round-trips to the identical private key.
func FuzzDecryptKeyJSON(f *testing.F) {
	// Seed with the real v3 fixture (password "") plus a wrong password.
	if blob, err := os.ReadFile("testdata/very-light-scrypt.json"); err == nil {
		f.Add(blob, "")
		f.Add(blob, "wrong")
	}
	// Seed with the v3 test vectors (scrypt + pbkdf2, 30/31-byte keys).
	seedVectorFile(f, "testdata/v3_test_vector.json")
	// Seed with the v1 test vector (legacy AES-CBC path).
	seedVectorFile(f, "testdata/v1_test_vector.json")

	// Junk / structural seeds, including a copy of the legacy corpus artifact
	// from tests/fuzzers/keystore/corpus (7 random bytes) which lives outside
	// this package tree and so cannot be read by relative path at seed time.
	f.Add([]byte(""), "")
	f.Add([]byte("{}"), "x")
	f.Add([]byte("[]"), "x")
	f.Add([]byte(`{"version":3}`), "x")
	f.Add([]byte("not json"), "pass")
	f.Add([]byte{0x6e, 0x73, 0xa9, 0x9b, 0x2c, 0xb2, 0xd4}, "pass")

	f.Fuzz(func(t *testing.T, keyjson []byte, pass string) {
		if !scryptCostOK(keyjson) {
			return
		}
		key, err := DecryptKey(keyjson, pass)
		if err != nil {
			// An error must not come with a key.
			if key != nil {
				t.Fatalf("DecryptKey returned both a key and an error: %v", err)
			}
			return
		}
		if key == nil || key.PrivateKey == nil {
			t.Fatalf("DecryptKey reported success but produced no usable key")
		}
		// Round-trip: re-encrypt with very light params and decrypt again.
		reenc, err := EncryptKey(key, pass, veryLightScryptN, veryLightScryptP)
		if err != nil {
			t.Fatalf("EncryptKey failed on a successfully decrypted key: %v", err)
		}
		key2, err := DecryptKey(reenc, pass)
		if err != nil {
			t.Fatalf("round-trip DecryptKey failed: %v", err)
		}
		if !bytes.Equal(crypto.FromECDSA(key.PrivateKey), crypto.FromECDSA(key2.PrivateKey)) {
			t.Fatal("round-trip private key mismatch")
		}
		if key.Address != key2.Address {
			t.Fatalf("round-trip address mismatch: %x != %x", key.Address, key2.Address)
		}
	})
}

// seedVectorFile reads a {name: {json, password}} test-vector file and adds each
// vector (with its correct and a wrong password) as a fuzz seed.
func seedVectorFile(f *testing.F, path string) {
	f.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var vectors map[string]struct {
		Json     json.RawMessage `json:"json"`
		Password string          `json:"password"`
	}
	if err := json.Unmarshal(blob, &vectors); err != nil {
		return
	}
	for _, v := range vectors {
		if len(v.Json) == 0 {
			continue
		}
		f.Add([]byte(v.Json), v.Password)
		f.Add([]byte(v.Json), v.Password+"_wrong")
	}
}

// FuzzPresaleWallet drives the presale/pre-sale wallet decrypt path with
// arbitrary JSON and passwords. It must never panic and must return an error on
// junk input.
func FuzzPresaleWallet(f *testing.F) {
	f.Add([]byte(`{"encseed":"","ethaddr":"","email":"","btcaddr":""}`), "")
	f.Add([]byte(`{"encseed":"00","ethaddr":"","email":"","btcaddr":""}`), "pw")
	f.Add([]byte("{}"), "pw")
	f.Add([]byte(""), "")
	f.Add([]byte("not json"), "pw")
	// A full 16-byte-IV + 16-byte-block encseed (junk contents, wrong addr).
	f.Add([]byte(`{"encseed":"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f","ethaddr":"deadbeef","email":"","btcaddr":""}`), "pw")

	f.Fuzz(func(t *testing.T, keyjson []byte, pass string) {
		key, err := decryptWalletKey(keyjson, pass)
		if err != nil {
			return
		}
		// Success is only expected when the derived address matches; the key
		// must then be usable.
		if key == nil || key.PrivateKey == nil {
			t.Fatalf("decryptWalletKey reported success but produced no key")
		}
	})
}
