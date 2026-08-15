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
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/wallet"
	"github.com/google/uuid"
	"golang.org/x/crypto/pbkdf2"
)

// creates a Key and stores that in the given KeyStore by decrypting a wallet key JSON
func importWalletKey(keyStore keyStore, keyJSON []byte, password string) (wallet.Account, *Key, error) {
	key, err := decryptWalletKey(keyJSON, password)
	if err != nil {
		return wallet.Account{}, nil, err
	}
	key.Id, err = uuid.NewRandom()
	if err != nil {
		return wallet.Account{}, nil, err
	}
	a := wallet.Account{
		Address: key.Address,
		URL: wallet.URL{
			Scheme: KeyStoreScheme,
			Path:   keyStore.JoinPath(keyFileName(key.Address)),
		},
	}
	err = keyStore.StoreKey(a.URL.Path, key, password)
	return a, key, err
}

func decryptWalletKey(fileContent []byte, password string) (key *Key, err error) {
	walletKeyStruct := struct {
		EncSeed string
		EthAddr string
		Email   string
		BtcAddr string
	}{}
	err = json.Unmarshal(fileContent, &walletKeyStruct)
	if err != nil {
		return nil, err
	}
	encSeedBytes, err := hex.DecodeString(walletKeyStruct.EncSeed)
	if err != nil {
		return nil, errors.New("invalid hex in encSeed")
	}
	if len(encSeedBytes) < 16 {
		return nil, errors.New("invalid encSeed, too short")
	}
	iv := encSeedBytes[:16]
	cipherText := encSeedBytes[16:]
	/*
		See https://github.com/ethereum/pyethsaletool

		pyethsaletool generates the encryption key from password by
		2000 rounds of PBKDF2 with HMAC-SHA-256 using password as salt (:().
		16 byte key length within PBKDF2 and resulting key is used as AES key
	*/
	passBytes := []byte(password)
	derivedKey := pbkdf2.Key(passBytes, passBytes, 2000, 16, sha256.New)
	plainText, err := aesCBCDecrypt(derivedKey, cipherText, iv)
	if err != nil {
		return nil, err
	}
	ethPriv := crypto.Keccak256(plainText)
	ecKey := crypto.ToECDSAUnsafe(ethPriv)

	key = &Key{
		Id:         uuid.UUID{},
		Address:    crypto.PubkeyToAddress(ecKey.PublicKey),
		PrivateKey: ecKey,
	}
	derivedAddr := hex.EncodeToString(key.Address.Bytes()) // needed because .Hex() gives leading "0x"
	expectedAddr := walletKeyStruct.EthAddr
	if derivedAddr != expectedAddr {
		err = fmt.Errorf("decrypted addr '%s' not equal to expected addr '%s'", derivedAddr, expectedAddr)
	}
	return key, err
}

func aesCTRXOR(key, inText, iv []byte) ([]byte, error) {
	// AES-128 is selected due to size of encryptKey.
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aesBlock.BlockSize() {
		return nil, errors.New("invalid IV length")
	}
	stream := cipher.NewCTR(aesBlock, iv)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	return outText, err
}

func aesCBCDecrypt(key, cipherText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != aesBlock.BlockSize() {
		return nil, errors.New("invalid IV length")
	}
	if len(cipherText) == 0 || len(cipherText)%aesBlock.BlockSize() != 0 {
		return nil, errors.New("invalid ciphertext length")
	}
	decrypter := cipher.NewCBCDecrypter(aesBlock, iv)
	paddedPlaintext := make([]byte, len(cipherText))
	decrypter.CryptBlocks(paddedPlaintext, cipherText)
	plaintext := pkcs7Unpad(paddedPlaintext)
	if plaintext == nil {
		return nil, ErrDecrypt
	}
	return plaintext, err
}

// From https://leanpub.com/gocrypto/read#leanpub-auto-block-cipher-modes
func pkcs7Unpad(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}

	padding := in[len(in)-1]
	if int(padding) > len(in) || padding > aes.BlockSize {
		return nil
	} else if padding == 0 {
		return nil
	}

	for i := len(in) - 1; i > len(in)-int(padding)-1; i-- {
		if in[i] != padding {
			return nil
		}
	}
	return in[:len(in)-int(padding)]
}
