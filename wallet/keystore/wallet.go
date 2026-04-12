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
	"math/big"

	parallax "github.com/ParallaxProtocol/parallax"
	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/wallet"
)

// keystoreWallet implements the accounts.Wallet interface for the original
// keystore.
type keystoreWallet struct {
	account  wallet.Account // Single account contained in this wallet
	keystore *KeyStore      // Keystore where the account originates from
}

// URL implements accounts.Wallet, returning the URL of the account within.
func (w *keystoreWallet) URL() wallet.URL {
	return w.account.URL
}

// Status implements accounts.Wallet, returning whether the account held by the
// keystore wallet is unlocked or not.
func (w *keystoreWallet) Status() (string, error) {
	w.keystore.mu.RLock()
	defer w.keystore.mu.RUnlock()

	if _, ok := w.keystore.unlocked[w.account.Address]; ok {
		return "Unlocked", nil
	}
	return "Locked", nil
}

// Open implements accounts.Wallet, but is a noop for plain wallets since there
// is no connection or decryption step necessary to access the list of accounts.
func (w *keystoreWallet) Open(passphrase string) error { return nil }

// Close implements accounts.Wallet, but is a noop for plain wallets since there
// is no meaningful open operation.
func (w *keystoreWallet) Close() error { return nil }

// Accounts implements accounts.Wallet, returning an account list consisting of
// a single account that the plain keystore wallet contains.
func (w *keystoreWallet) Accounts() []wallet.Account {
	return []wallet.Account{w.account}
}

// Contains implements accounts.Wallet, returning whether a particular account is
// or is not wrapped by this wallet instance.
func (w *keystoreWallet) Contains(account wallet.Account) bool {
	return account.Address == w.account.Address && (account.URL == (wallet.URL{}) || account.URL == w.account.URL)
}

// Derive implements accounts.Wallet, but is a noop for plain wallets since there
// is no notion of hierarchical account derivation for plain keystore accounts.
func (w *keystoreWallet) Derive(path wallet.DerivationPath, pin bool) (wallet.Account, error) {
	return wallet.Account{}, wallet.ErrNotSupported
}

// SelfDerive implements accounts.Wallet, but is a noop for plain wallets since
// there is no notion of hierarchical account derivation for plain keystore accounts.
func (w *keystoreWallet) SelfDerive(bases []wallet.DerivationPath, chain parallax.ChainStateReader) {
}

// signHash attempts to sign the given hash with
// the given account. If the wallet does not wrap this particular account, an
// error is returned to avoid account leakage (even though in theory we may be
// able to sign via our shared keystore backend).
func (w *keystoreWallet) signHash(account wallet.Account, hash []byte) ([]byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		return nil, wallet.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignHash(account, hash)
}

// SignData signs keccak256(data). The mimetype parameter describes the type of data being signed.
func (w *keystoreWallet) SignData(account wallet.Account, mimeType string, data []byte) ([]byte, error) {
	return w.signHash(account, crypto.Keccak256(data))
}

// SignDataWithPassphrase signs keccak256(data). The mimetype parameter describes the type of data being signed.
func (w *keystoreWallet) SignDataWithPassphrase(account wallet.Account, passphrase, mimeType string, data []byte) ([]byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		return nil, wallet.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignHashWithPassphrase(account, passphrase, crypto.Keccak256(data))
}

// SignText implements accounts.Wallet, attempting to sign the hash of
// the given text with the given account.
func (w *keystoreWallet) SignText(account wallet.Account, text []byte) ([]byte, error) {
	return w.signHash(account, wallet.TextHash(text))
}

// SignTextWithPassphrase implements accounts.Wallet, attempting to sign the
// hash of the given text with the given account using passphrase as extra authentication.
func (w *keystoreWallet) SignTextWithPassphrase(account wallet.Account, passphrase string, text []byte) ([]byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		return nil, wallet.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignHashWithPassphrase(account, passphrase, wallet.TextHash(text))
}

// SignTx implements accounts.Wallet, attempting to sign the given transaction
// with the given account. If the wallet does not wrap this particular account,
// an error is returned to avoid account leakage (even though in theory we may
// be able to sign via our shared keystore backend).
func (w *keystoreWallet) SignTx(account wallet.Account, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		return nil, wallet.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignTx(account, tx, chainID)
}

// SignTxWithPassphrase implements accounts.Wallet, attempting to sign the given
// transaction with the given account using passphrase as extra authentication.
func (w *keystoreWallet) SignTxWithPassphrase(account wallet.Account, passphrase string, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		return nil, wallet.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignTxWithPassphrase(account, passphrase, tx, chainID)
}
