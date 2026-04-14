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

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"

	"github.com/ParallaxProtocol/parallax/internal/api"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/rpc"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/util/hexutil"
	"github.com/ParallaxProtocol/parallax/wallet"
	"github.com/ParallaxProtocol/parallax/wallet/keystore"
	"github.com/ParallaxProtocol/parallax/wallet/scwallet"
	"github.com/ParallaxProtocol/parallax/wallet/signer/core/apitypes"
	"github.com/ParallaxProtocol/parallax/wallet/signer/storage"
	"github.com/ParallaxProtocol/parallax/wallet/usbwallet"
)

const (
	// numberOfAccountsToDerive For hardware wallets, the number of accounts to derive
	numberOfAccountsToDerive = 10
	// ExternalAPIVersion -- see extapi_changelog.md
	ExternalAPIVersion = "6.1.0"
	// InternalAPIVersion -- see intapi_changelog.md
	InternalAPIVersion = "7.0.1"
)

// ExternalAPI defines the external API through which signing requests are made.
type ExternalAPI interface {
	// List available accounts
	List(ctx context.Context) ([]util.Address, error)
	// New request to create a new account
	New(ctx context.Context) (util.Address, error)
	// SignTransaction request to sign the specified transaction
	SignTransaction(ctx context.Context, args apitypes.SendTxArgs, methodSelector *string) (*api.SignTransactionResult, error)
	// SignData - request to sign the given data (plus prefix)
	SignData(ctx context.Context, contentType string, addr util.MixedcaseAddress, data any) (hexutil.Bytes, error)
	// SignTypedData - request to sign the given structured data (plus prefix)
	SignTypedData(ctx context.Context, addr util.MixedcaseAddress, data apitypes.TypedData) (hexutil.Bytes, error)
	// EcRecover - recover public key from given message and signature
	EcRecover(ctx context.Context, data hexutil.Bytes, sig hexutil.Bytes) (util.Address, error)
	// Version info about the APIs
	Version(ctx context.Context) (string, error)
	// SignGnosisSafeTransaction signs/confirms a gnosis-safe multisig transaction
	SignGnosisSafeTx(ctx context.Context, signerAddress util.MixedcaseAddress, gnosisTx GnosisSafeTx, methodSelector *string) (*GnosisSafeTx, error)
}

// UIClientAPI specifies what method a UI needs to implement to be able to be used as a
// UI for the signer
type UIClientAPI interface {
	// ApproveTx prompt the user for confirmation to request to sign Transaction
	ApproveTx(request *SignTxRequest) (SignTxResponse, error)
	// ApproveSignData prompt the user for confirmation to request to sign data
	ApproveSignData(request *SignDataRequest) (SignDataResponse, error)
	// ApproveListing prompt the user for confirmation to list accounts
	// the list of accounts to list can be modified by the UI
	ApproveListing(request *ListRequest) (ListResponse, error)
	// ApproveNewAccount prompt the user for confirmation to create new Account, and reveal to caller
	ApproveNewAccount(request *NewAccountRequest) (NewAccountResponse, error)
	// ShowError displays error message to user
	ShowError(message string)
	// ShowInfo displays info message to user
	ShowInfo(message string)
	// OnApprovedTx notifies the UI about a transaction having been successfully signed.
	// This method can be used by a UI to keep track of e.g. how much has been sent to a particular recipient.
	OnApprovedTx(tx api.SignTransactionResult)
	// OnSignerStartup is invoked when the signer boots, and tells the UI info about external API location and version
	// information
	OnSignerStartup(info StartupInfo)
	// OnInputRequired is invoked when clef requires user input, for example master password or
	// pin-code for unlocking hardware wallets
	OnInputRequired(info UserInputRequest) (UserInputResponse, error)
	// RegisterUIServer tells the UI to use the given UIServerAPI for ui->clef communication
	RegisterUIServer(api *UIServerAPI)
}

// Validator defines the methods required to validate a transaction against some
// sanity defaults as well as any underlying 4byte method database.
//
// Use fourbyte.Database as an implementation. It is separated out of this package
// to allow pieces of the signer package to be used without having to load the
// 7MB embedded 4byte dump.
type Validator interface {
	// ValidateTransaction does a number of checks on the supplied transaction, and
	// returns either a list of warnings, or an error (indicating that the transaction
	// should be immediately rejected).
	ValidateTransaction(selector *string, tx *apitypes.SendTxArgs) (*apitypes.ValidationMessages, error)
}

// SignerAPI defines the actual implementation of ExternalAPI
type SignerAPI struct {
	chainID     *big.Int
	am          *wallet.Manager
	UI          UIClientAPI
	validator   Validator
	rejectMode  bool
	credentials storage.Storage
}

// Metadata about a request
type Metadata struct {
	Remote    string `json:"remote"`
	Local     string `json:"local"`
	Scheme    string `json:"scheme"`
	UserAgent string `json:"User-Agent"`
	Origin    string `json:"Origin"`
}

func StartClefAccountManager(ksLocation string, nousb, lightKDF bool, scpath string) *wallet.Manager {
	var (
		backends []wallet.Backend
		n, p     = keystore.StandardScryptN, keystore.StandardScryptP
	)
	if lightKDF {
		n, p = keystore.LightScryptN, keystore.LightScryptP
	}
	// support password based accounts
	if len(ksLocation) > 0 {
		backends = append(backends, keystore.NewKeyStore(ksLocation, n, p))
	}
	if !nousb {
		// Start a USB hub for Ledger hardware wallets
		if ledgerhub, err := usbwallet.NewLedgerHub(); err != nil {
			logging.Warn(fmt.Sprintf("Failed to start Ledger hub, disabling: %v", err))
		} else {
			backends = append(backends, ledgerhub)
			logging.Debug("Ledger support enabled")
		}
		// Start a USB hub for Trezor hardware wallets (HID version)
		if trezorhub, err := usbwallet.NewTrezorHubWithHID(); err != nil {
			logging.Warn(fmt.Sprintf("Failed to start HID Trezor hub, disabling: %v", err))
		} else {
			backends = append(backends, trezorhub)
			logging.Debug("Trezor support enabled via HID")
		}
		// Start a USB hub for Trezor hardware wallets (WebUSB version)
		if trezorhub, err := usbwallet.NewTrezorHubWithWebUSB(); err != nil {
			logging.Warn(fmt.Sprintf("Failed to start WebUSB Trezor hub, disabling: %v", err))
		} else {
			backends = append(backends, trezorhub)
			logging.Debug("Trezor support enabled via WebUSB")
		}
	}

	// Start a smart card hub
	if len(scpath) > 0 {
		// Sanity check that the smartcard path is valid
		fi, err := os.Stat(scpath)
		if err != nil {
			logging.Info("Smartcard socket file missing, disabling", "err", err)
		} else {
			if fi.Mode()&os.ModeType != os.ModeSocket {
				logging.Error("Invalid smartcard socket file type", "path", scpath, "type", fi.Mode().String())
			} else {
				if schub, err := scwallet.NewHub(scpath, scwallet.Scheme, ksLocation); err != nil {
					logging.Warn(fmt.Sprintf("Failed to start smart card hub, disabling: %v", err))
				} else {
					backends = append(backends, schub)
				}
			}
		}
	}

	// Clef doesn't allow insecure http account unlock.
	return wallet.NewManager(&wallet.Config{InsecureUnlockAllowed: false}, backends...)
}

// MetadataFromContext extracts Metadata from a given context.Context
func MetadataFromContext(ctx context.Context) Metadata {
	info := rpc.PeerInfoFromContext(ctx)

	m := Metadata{"NA", "NA", "NA", "", ""} // batman

	if info.Transport != "" {
		if info.Transport == "http" {
			m.Scheme = info.HTTP.Version
		}
		m.Scheme = info.Transport
	}
	if info.RemoteAddr != "" {
		m.Remote = info.RemoteAddr
	}
	if info.HTTP.Host != "" {
		m.Local = info.HTTP.Host
	}
	m.Origin = info.HTTP.Origin
	m.UserAgent = info.HTTP.UserAgent
	return m
}

// String implements Stringer interface
func (m Metadata) String() string {
	s, err := json.Marshal(m)
	if err == nil {
		return string(s)
	}
	return err.Error()
}

// types for the requests/response types between signer and UI
type (
	// SignTxRequest contains info about a Transaction to sign
	SignTxRequest struct {
		Transaction apitypes.SendTxArgs       `json:"transaction"`
		Callinfo    []apitypes.ValidationInfo `json:"call_info"`
		Meta        Metadata                  `json:"meta"`
	}
	// SignTxResponse result from SignTxRequest
	SignTxResponse struct {
		// The UI may make changes to the TX
		Transaction apitypes.SendTxArgs `json:"transaction"`
		Approved    bool                `json:"approved"`
	}
	SignDataRequest struct {
		ContentType string                    `json:"content_type"`
		Address     util.MixedcaseAddress     `json:"address"`
		Rawdata     []byte                    `json:"raw_data"`
		Messages    []*apitypes.NameValueType `json:"messages"`
		Callinfo    []apitypes.ValidationInfo `json:"call_info"`
		Hash        hexutil.Bytes             `json:"hash"`
		Meta        Metadata                  `json:"meta"`
	}
	SignDataResponse struct {
		Approved bool `json:"approved"`
	}
	NewAccountRequest struct {
		Meta Metadata `json:"meta"`
	}
	NewAccountResponse struct {
		Approved bool `json:"approved"`
	}
	ListRequest struct {
		Accounts []wallet.Account `json:"accounts"`
		Meta     Metadata         `json:"meta"`
	}
	ListResponse struct {
		Accounts []wallet.Account `json:"accounts"`
	}
	Message struct {
		Text string `json:"text"`
	}
	StartupInfo struct {
		Info map[string]any `json:"info"`
	}
	UserInputRequest struct {
		Title      string `json:"title"`
		Prompt     string `json:"prompt"`
		IsPassword bool   `json:"isPassword"`
	}
	UserInputResponse struct {
		Text string `json:"text"`
	}
)

var ErrRequestDenied = errors.New("request denied")

// NewSignerAPI creates a new API that can be used for Account management.
// ksLocation specifies the directory where to store the password protected private
// key that is generated when a new Account is created.
// noUSB disables USB support that is required to support hardware devices such as
// ledger and trezor.
func NewSignerAPI(am *wallet.Manager, chainID int64, noUSB bool, ui UIClientAPI, validator Validator, advancedMode bool, credentials storage.Storage) *SignerAPI {
	if advancedMode {
		logging.Info("Clef is in advanced mode: will warn instead of reject")
	}
	signer := &SignerAPI{big.NewInt(chainID), am, ui, validator, !advancedMode, credentials}
	if !noUSB {
		signer.startUSBListener()
	}
	return signer
}

func (s *SignerAPI) openTrezor(url wallet.URL) {
	resp, err := s.UI.OnInputRequired(UserInputRequest{
		Prompt: "Pin required to open Trezor wallet\n" +
			"Look at the device for number positions\n\n" +
			"7 | 8 | 9\n" +
			"--+---+--\n" +
			"4 | 5 | 6\n" +
			"--+---+--\n" +
			"1 | 2 | 3\n\n",
		IsPassword: true,
		Title:      "Trezor unlock",
	})
	if err != nil {
		logging.Warn("failed getting trezor pin", "err", err)
		return
	}
	// We're using the URL instead of the pointer to the
	// Wallet -- perhaps it is not actually present anymore
	w, err := s.am.Wallet(url.String())
	if err != nil {
		logging.Warn("wallet unavailable", "url", url)
		return
	}
	err = w.Open(resp.Text)
	if err != nil {
		logging.Warn("failed to open wallet", "wallet", url, "err", err)
		return
	}
}

// startUSBListener starts a listener for USB events, for hardware wallet interaction
func (s *SignerAPI) startUSBListener() {
	eventCh := make(chan wallet.WalletEvent, 16)
	am := s.am
	am.Subscribe(eventCh)
	// Open any wallets already attached
	for _, wallet := range am.Wallets() {
		if err := wallet.Open(""); err != nil {
			logging.Warn("Failed to open wallet", "url", wallet.URL(), "err", err)
			if err == usbwallet.ErrTrezorPINNeeded {
				go s.openTrezor(wallet.URL())
			}
		}
	}
	go s.derivationLoop(eventCh)
}

// derivationLoop listens for wallet events
func (s *SignerAPI) derivationLoop(events chan wallet.WalletEvent) {
	// Listen for wallet event till termination
	for event := range events {
		switch event.Kind {
		case wallet.WalletArrived:
			if err := event.Wallet.Open(""); err != nil {
				logging.Warn("New wallet appeared, failed to open", "url", event.Wallet.URL(), "err", err)
				if err == usbwallet.ErrTrezorPINNeeded {
					go s.openTrezor(event.Wallet.URL())
				}
			}
		case wallet.WalletOpened:
			status, _ := event.Wallet.Status()
			logging.Info("New wallet appeared", "url", event.Wallet.URL(), "status", status)
			derive := func(limit int, next func() wallet.DerivationPath) {
				// Derive first N accounts, hardcoded for now
				for i := 0; i < limit; i++ {
					path := next()
					if acc, err := event.Wallet.Derive(path, true); err != nil {
						logging.Warn("Account derivation failed", "error", err)
					} else {
						logging.Info("Derived account", "address", acc.Address, "path", path)
					}
				}
			}
			logging.Info("Deriving default paths")
			derive(numberOfAccountsToDerive, wallet.DefaultIterator(wallet.DefaultBaseDerivationPath))
			if event.Wallet.URL().Scheme == "ledger" {
				logging.Info("Deriving ledger legacy paths")
				derive(numberOfAccountsToDerive, wallet.DefaultIterator(wallet.LegacyLedgerBaseDerivationPath))
				logging.Info("Deriving ledger live paths")
				// For ledger live, since it's based off the same (DefaultBaseDerivationPath)
				// as one we've already used, we need to step it forward one step to avoid
				// hitting the same path again
				nextFn := wallet.LedgerLiveIterator(wallet.DefaultBaseDerivationPath)
				nextFn()
				derive(numberOfAccountsToDerive, nextFn)
			}
		case wallet.WalletDropped:
			logging.Info("Old wallet dropped", "url", event.Wallet.URL())
			event.Wallet.Close()
		}
	}
}

// List returns the set of wallet this signer manages. Each wallet can contain
// multiple accounts.
func (s *SignerAPI) List(ctx context.Context) ([]util.Address, error) {
	accs := make([]wallet.Account, 0)
	// accs is initialized as empty list, not nil. We use 'nil' to signal
	// rejection, as opposed to an empty list.
	for _, wallet := range s.am.Wallets() {
		accs = append(accs, wallet.Accounts()...)
	}
	result, err := s.UI.ApproveListing(&ListRequest{Accounts: accs, Meta: MetadataFromContext(ctx)})
	if err != nil {
		return nil, err
	}
	if result.Accounts == nil {
		return nil, ErrRequestDenied
	}
	addresses := make([]util.Address, 0)
	for _, acc := range result.Accounts {
		addresses = append(addresses, acc.Address)
	}
	return addresses, nil
}

// New creates a new password protected Account. The private key is protected with
// the given password. Users are responsible to backup the private key that is stored
// in the keystore location thas was specified when this API was created.
func (s *SignerAPI) New(ctx context.Context) (util.Address, error) {
	if be := s.am.Backends(keystore.KeyStoreType); len(be) == 0 {
		return util.Address{}, errors.New("password based accounts not supported")
	}
	if resp, err := s.UI.ApproveNewAccount(&NewAccountRequest{MetadataFromContext(ctx)}); err != nil {
		return util.Address{}, err
	} else if !resp.Approved {
		return util.Address{}, ErrRequestDenied
	}
	return s.newAccount()
}

// newAccount is the internal method to create a new account. It should be used
// _after_ user-approval has been obtained
func (s *SignerAPI) newAccount() (util.Address, error) {
	be := s.am.Backends(keystore.KeyStoreType)
	if len(be) == 0 {
		return util.Address{}, errors.New("password based accounts not supported")
	}
	// Three retries to get a valid password
	for i := 0; i < 3; i++ {
		resp, err := s.UI.OnInputRequired(UserInputRequest{
			"New account password",
			fmt.Sprintf("Please enter a password for the new account to be created (attempt %d of 3)", i),
			true,
		})
		if err != nil {
			logging.Warn("error obtaining password", "attempt", i, "error", err)
			continue
		}
		if pwErr := ValidatePasswordFormat(resp.Text); pwErr != nil {
			s.UI.ShowError(fmt.Sprintf("Account creation attempt #%d failed due to password requirements: %v", i+1, pwErr))
		} else {
			// No error
			acc, err := be[0].(*keystore.KeyStore).NewAccount(resp.Text)
			logging.Info("Your new key was generated", "address", acc.Address)
			logging.Warn("Please backup your key file!", "path", acc.URL.Path)
			logging.Warn("Please remember your password!")
			return acc.Address, err
		}
	}
	// Otherwise fail, with generic error message
	return util.Address{}, errors.New("account creation failed")
}

// logDiff logs the difference between the incoming (original) transaction and the one returned from the signer.
// it also returns 'true' if the transaction was modified, to make it possible to configure the signer not to allow
// UI-modifications to requests
func logDiff(original *SignTxRequest, new *SignTxResponse) bool {
	intPtrModified := func(a, b *hexutil.Big) bool {
		aBig := (*big.Int)(a)
		bBig := (*big.Int)(b)
		if aBig != nil && bBig != nil {
			return aBig.Cmp(bBig) != 0
		}
		// One or both of them are nil
		return a != b
	}

	modified := false
	if f0, f1 := original.Transaction.From, new.Transaction.From; !reflect.DeepEqual(f0, f1) {
		logging.Info("Sender-account changed by UI", "was", f0, "is", f1)
		modified = true
	}
	if t0, t1 := original.Transaction.To, new.Transaction.To; !reflect.DeepEqual(t0, t1) {
		logging.Info("Recipient-account changed by UI", "was", t0, "is", t1)
		modified = true
	}
	if g0, g1 := original.Transaction.Gas, new.Transaction.Gas; g0 != g1 {
		modified = true
		logging.Info("Gas changed by UI", "was", g0, "is", g1)
	}
	if a, b := original.Transaction.GasPrice, new.Transaction.GasPrice; intPtrModified(a, b) {
		logging.Info("GasPrice changed by UI", "was", a, "is", b)
		modified = true
	}
	if a, b := original.Transaction.MaxPriorityFeePerGas, new.Transaction.MaxPriorityFeePerGas; intPtrModified(a, b) {
		logging.Info("maxPriorityFeePerGas changed by UI", "was", a, "is", b)
		modified = true
	}
	if a, b := original.Transaction.MaxFeePerGas, new.Transaction.MaxFeePerGas; intPtrModified(a, b) {
		logging.Info("maxFeePerGas changed by UI", "was", a, "is", b)
		modified = true
	}
	if v0, v1 := big.Int(original.Transaction.Value), big.Int(new.Transaction.Value); v0.Cmp(&v1) != 0 {
		modified = true
		logging.Info("Value changed by UI", "was", v0, "is", v1)
	}
	if d0, d1 := original.Transaction.Data, new.Transaction.Data; d0 != d1 {
		d0s := ""
		d1s := ""
		if d0 != nil {
			d0s = hexutil.Encode(*d0)
		}
		if d1 != nil {
			d1s = hexutil.Encode(*d1)
		}
		if d1s != d0s {
			modified = true
			logging.Info("Data changed by UI", "was", d0s, "is", d1s)
		}
	}
	if n0, n1 := original.Transaction.Nonce, new.Transaction.Nonce; n0 != n1 {
		modified = true
		logging.Info("Nonce changed by UI", "was", n0, "is", n1)
	}
	return modified
}

func (s *SignerAPI) lookupPassword(address util.Address) (string, error) {
	return s.credentials.Get(address.Hex())
}

func (s *SignerAPI) lookupOrQueryPassword(address util.Address, title, prompt string) (string, error) {
	// Look up the password and return if available
	if pw, err := s.lookupPassword(address); err == nil {
		return pw, nil
	}
	// Password unavailable, request it from the user
	pwResp, err := s.UI.OnInputRequired(UserInputRequest{title, prompt, true})
	if err != nil {
		logging.Warn("error obtaining password", "error", err)
		// We'll not forward the error here, in case the error contains info about the response from the UI,
		// which could leak the password if it was malformed json or something
		return "", errors.New("internal error")
	}
	return pwResp.Text, nil
}

// SignTransaction signs the given Transaction and returns it both as json and rlp-encoded form
func (s *SignerAPI) SignTransaction(ctx context.Context, args apitypes.SendTxArgs, methodSelector *string) (*api.SignTransactionResult, error) {
	var (
		err    error
		result SignTxResponse
	)
	msgs, err := s.validator.ValidateTransaction(methodSelector, &args)
	if err != nil {
		return nil, err
	}
	// If we are in 'rejectMode', then reject rather than show the user warnings
	if s.rejectMode {
		if err := msgs.GetWarnings(); err != nil {
			return nil, err
		}
	}
	if args.ChainID != nil {
		requestedChainId := (*big.Int)(args.ChainID)
		if s.chainID.Cmp(requestedChainId) != 0 {
			logging.Error("Signing request with wrong chain id", "requested", requestedChainId, "configured", s.chainID)
			return nil, fmt.Errorf("requested chainid %d does not match the configuration of the signer",
				requestedChainId)
		}
	}
	req := SignTxRequest{
		Transaction: args,
		Meta:        MetadataFromContext(ctx),
		Callinfo:    msgs.Messages,
	}
	// Process approval
	result, err = s.UI.ApproveTx(&req)
	if err != nil {
		return nil, err
	}
	if !result.Approved {
		return nil, ErrRequestDenied
	}
	// Log changes made by the UI to the signing-request
	logDiff(&req, &result)
	var (
		acc wallet.Account
		w   wallet.Wallet
	)
	acc = wallet.Account{Address: result.Transaction.From.Address()}
	w, err = s.am.Find(acc)
	if err != nil {
		return nil, err
	}
	// Convert fields into a real transaction
	unsignedTx := result.Transaction.ToTransaction()
	// Get the password for the transaction
	pw, err := s.lookupOrQueryPassword(acc.Address, "Account password",
		fmt.Sprintf("Please enter the password for account %s", acc.Address.String()))
	if err != nil {
		return nil, err
	}
	// The one to sign is the one that was returned from the UI
	signedTx, err := w.SignTxWithPassphrase(acc, pw, unsignedTx, s.chainID)
	if err != nil {
		s.UI.ShowError(err.Error())
		return nil, err
	}

	data, err := signedTx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	response := api.SignTransactionResult{Raw: data, Tx: signedTx}

	// Finally, send the signed tx to the UI
	s.UI.OnApprovedTx(response)
	// ...and to the external caller
	return &response, nil
}

func (s *SignerAPI) SignGnosisSafeTx(ctx context.Context, signerAddress util.MixedcaseAddress, gnosisTx GnosisSafeTx, methodSelector *string) (*GnosisSafeTx, error) {
	// Do the usual validations, but on the last-stage transaction
	args := gnosisTx.ArgsForValidation()
	msgs, err := s.validator.ValidateTransaction(methodSelector, args)
	if err != nil {
		return nil, err
	}
	// If we are in 'rejectMode', then reject rather than show the user warnings
	if s.rejectMode {
		if err := msgs.GetWarnings(); err != nil {
			return nil, err
		}
	}
	typedData := gnosisTx.ToTypedData()
	signature, preimage, err := s.signTypedData(ctx, signerAddress, typedData, msgs)
	if err != nil {
		return nil, err
	}
	checkSummedSender, _ := util.NewMixedcaseAddressFromString(signerAddress.Address().Hex())

	gnosisTx.Signature = signature
	gnosisTx.SafeTxHash = util.BytesToHash(preimage)
	gnosisTx.Sender = *checkSummedSender // Must be checksumed to be accepted by relay

	return &gnosisTx, nil
}

// Returns the external api version. This method does not require user acceptance. Available methods are
// available via enumeration anyway, and this info does not contain user-specific data
func (s *SignerAPI) Version(ctx context.Context) (string, error) {
	return ExternalAPIVersion, nil
}
