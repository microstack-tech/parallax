// Package keys implements the deterministic EVM -> Nostr key derivation for
// Parallax channels (Parallax Channels spec, Part 3 §3).
//
// The Nostr identity is derived from the EVM private key via HKDF-SHA256, so
// seed-only recovery reproduces the npub, yet the npub is not derivable from
// the EVM address or public key by third parties (opt-in linkage privacy,
// Part 2 §1). No single key is ever used under both ECDSA and Schnorr.
package keys

import (
	"crypto/sha256"
	"errors"
	"io"
	"math/big"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"golang.org/x/crypto/hkdf"
)

// hkdfInfo is the HKDF-Expand info prefix; a single counter byte is appended
// (0x00 for the first attempt) so the vanishingly rare out-of-range scalar
// re-derives deterministically.
const hkdfInfo = "parallax/nostr/v1"

// ErrDerivationFailed is returned if no valid scalar is found within the
// single-byte counter space. Each attempt fails with p ~= 2^-128, so hitting
// this in practice indicates a broken input, not bad luck.
var ErrDerivationFailed = errors.New("keys: nostr key derivation failed for all counter values")

// NostrKey is a derived BIP-340 key pair: a 32-byte scalar normalized to an
// even-Y public point, and the x-only public key Nostr uses as the identity.
type NostrKey struct {
	Priv [32]byte // big-endian scalar, even-Y normalized
	PubX [32]byte // x-only public key ("npub" payload form, hex in messages)
}

// DeriveNostrKey derives the Nostr identity from a 32-byte EVM secp256k1
// private key:
//
//	prk = HKDF-Extract(SHA-256, salt="", ikm=evmPriv)
//	okm = HKDF-Expand(SHA-256, prk, info="parallax/nostr/v1"||counter, 32)
//	k   = okm as big-endian; if k == 0 or k >= n, bump counter and re-derive
//	if lift_x(k*G) has odd Y, use n - k   (BIP-340 even-Y normalization)
func DeriveNostrKey(evmPriv []byte) (NostrKey, error) {
	if len(evmPriv) != 32 {
		return NostrKey{}, errors.New("keys: evm private key must be 32 bytes")
	}

	n := crypto.S256().Params().N
	prk := hkdf.Extract(sha256.New, evmPriv, nil)

	for counter := 0; counter <= 0xff; counter++ {
		info := append([]byte(hkdfInfo), byte(counter))
		okm := make([]byte, 32)
		if _, err := io.ReadFull(hkdf.Expand(sha256.New, prk, info), okm); err != nil {
			return NostrKey{}, err
		}

		k := new(big.Int).SetBytes(okm)
		if k.Sign() == 0 || k.Cmp(n) >= 0 {
			continue
		}

		priv, err := crypto.ToECDSA(okm)
		if err != nil {
			continue // unreachable given the range check; defensive
		}
		if priv.PublicKey.Y.Bit(0) == 1 { // odd Y: negate the scalar, same X
			k.Sub(n, k)
		}

		var key NostrKey
		k.FillBytes(key.Priv[:])
		priv.PublicKey.X.FillBytes(key.PubX[:])
		return key, nil
	}
	return NostrKey{}, ErrDerivationFailed
}
