package protocol

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

// Signer produces the wallet's EIP-712 signatures in the on-chain wire
// format: 65-byte (r,s,v) with v in {27,28} and low-s (Part 1 §5).
type Signer interface {
	Address() util.Address
	SignDigest(digest util.Hash) ([]byte, error)
}

// KeySigner signs with an in-memory private key (CLI and tests; the
// keystore-backed signer wraps the same interface).
type KeySigner struct {
	priv *ecdsa.PrivateKey
	addr util.Address
}

func NewKeySigner(priv *ecdsa.PrivateKey) *KeySigner {
	return &KeySigner{priv: priv, addr: crypto.PubkeyToAddress(priv.PublicKey)}
}

func (k *KeySigner) Address() util.Address { return k.addr }

func (k *KeySigner) SignDigest(digest util.Hash) ([]byte, error) {
	sig, err := crypto.Sign(digest.Bytes(), k.priv) // v in {0,1}, low-s
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}

// RecoverSigner recovers the address behind a 65-byte (r,s,v) signature over
// an EIP-712 digest, enforcing v in {27,28} and low-s so that any accepted
// signature is byte-for-byte submittable on-chain.
func RecoverSigner(digest util.Hash, sig []byte) (util.Address, error) {
	if len(sig) != 65 {
		return util.Address{}, fmt.Errorf("protocol: signature length %d", len(sig))
	}
	v := sig[64]
	if v != 27 && v != 28 {
		return util.Address{}, fmt.Errorf("protocol: bad v %d", v)
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])
	if !crypto.ValidateSignatureValues(v-27, r, s, true) { // homestead: low-s
		return util.Address{}, errors.New("protocol: signature not low-s canonical")
	}

	rec := make([]byte, 65)
	copy(rec, sig[:64])
	rec[64] = v - 27
	pub, err := crypto.SigToPub(digest.Bytes(), rec)
	if err != nil {
		return util.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// VerifySignedBy checks that sig over digest recovers to expected.
func VerifySignedBy(digest util.Hash, sig []byte, expected util.Address) error {
	got, err := RecoverSigner(digest, sig)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("protocol: signer %s, expected %s", got.Hex(), expected.Hex())
	}
	return nil
}
