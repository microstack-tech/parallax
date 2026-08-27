// Package protocol implements the per-channel payment state machines
// (Part 2 §7-8): proposer and responder flows, the A-wins tiebreak,
// poisoned-channel handling, and cooperative-close negotiation with the
// freeze rule. The engine is transport-agnostic: inputs are decoded messages
// plus watcher facts, outputs are messages to transmit — the Nostr relay
// pool and the QR codec are interchangeable carriers.
package protocol

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// Rumor kinds (Part 2 §3).
const (
	KindInvoice           = 21901
	KindProposal          = 21902
	KindAck               = 21903
	KindCoopCloseProposal = 21904
	KindCoopCloseAck      = 21905
	KindTowerDelegation   = 21906
	KindTowerReceipt      = 21907
	KindHandshake         = 21908
	KindNack              = 21909
	KindSelfBackup        = 21910
	KindWithdrawProposal  = 21911
	KindWithdrawAck       = 21912
)

// NACK reasons (Part 2 §6.9).
const (
	NackExpiredInvoice      = "expired-invoice"
	NackBadSeq              = "bad-seq"
	NackInsufficientBalance = "insufficient-balance"
	NackPolicy              = "policy"
	NackLocksNonzero        = "locks-nonzero"
	NackUnknownChannel      = "unknown-channel"
	NackFrozen              = "frozen"
)

// WireState is the canonical state object (Part 2 §5) in wire form: decimal
// strings for integers, 0x-hex for addresses/hashes/signatures.
type WireState struct {
	ChannelID       string `json:"channelId"`
	Registry        string `json:"registry"`
	ChainID         string `json:"chainId"`
	Seq             string `json:"seq"`
	TransferredAtoB string `json:"transferredAtoB"`
	TransferredBtoA string `json:"transferredBtoA"`
	LocksRoot       string `json:"locksRoot"`
	LockedAmount    string `json:"lockedAmount"`
	SigA            string `json:"sigA,omitempty"`
	SigB            string `json:"sigB,omitempty"`
}

// ProposalMsg is kind 21902.
type ProposalMsg struct {
	V            int       `json:"v"`
	InvoiceID    string    `json:"invoiceId,omitempty"`
	State        WireState `json:"state"`
	ProposerRole string    `json:"proposerRole"` // "A" | "B"
	Relays       []string  `json:"relays,omitempty"`
}

// AckMsg is kind 21903 (and, with the coop-close digest, the shape of 21905).
type AckMsg struct {
	V         int    `json:"v"`
	ChannelID string `json:"channelId"`
	Seq       string `json:"seq"`
	StateHash string `json:"stateHash"`
	Sig       string `json:"sig"`
	InvoiceID string `json:"invoiceId,omitempty"`
}

// NackMsg is kind 21909. Advisory only (Part 2 §7.4).
type NackMsg struct {
	V         int    `json:"v"`
	ChannelID string `json:"channelId"`
	// Registry and ChainID qualify the bare channel id across coexisting
	// registries: a NACK carries no signed state, so without them a receiver
	// holding the same id (and an outstanding proposal at the same seq) with
	// the same peer on another registry cannot tell which channel is meant.
	Registry string `json:"registry,omitempty"`
	ChainID  string `json:"chainId,omitempty"`
	Re       string `json:"re"` // kind being nacked, e.g. "21902"
	Seq      string `json:"seq"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

// CoopCloseProposalMsg is kind 21904.
type CoopCloseProposalMsg struct {
	V            int      `json:"v"`
	ChannelID    string   `json:"channelId"`
	Registry     string   `json:"registry"`
	ChainID      string   `json:"chainId"`
	BalanceA     string   `json:"balanceA"`
	BalanceB     string   `json:"balanceB"`
	ExpiryBlock  string   `json:"expiryBlock"`
	Sig          string   `json:"sig"` // proposer's EIP-712 CooperativeClose signature
	ProposerRole string   `json:"proposerRole"`
	Relays       []string `json:"relays,omitempty"`
}

// HandshakeMsg is kind 21908 (Part 2 §6.8), sent by the opener after the
// on-chain open; the linkage privately binds the opener's npub to its EVM
// address without a public 31910.
type HandshakeMsg struct {
	V          int    `json:"v"`
	ChannelID  string `json:"channelId"`
	Registry   string `json:"registry"`
	ChainID    string `json:"chainId"`
	EVMAddress string `json:"evmAddress"`
	Linkage    struct {
		EVMSig string `json:"evmSig"`
	} `json:"linkage"`
	Relays []string `json:"relays,omitempty"`
}

// WithdrawProposalMsg is kind 21911 (Part 2 §6.10): the proposer signs the
// on-chain Withdraw struct for its own address.
type WithdrawProposalMsg struct {
	V              int      `json:"v"`
	ChannelID      string   `json:"channelId"`
	Registry       string   `json:"registry"`
	ChainID        string   `json:"chainId"`
	Participant    string   `json:"participant"`
	TotalWithdrawn string   `json:"totalWithdrawn"`
	ExpiryBlock    string   `json:"expiryBlock"`
	Sig            string   `json:"sig"`
	Relays         []string `json:"relays,omitempty"`
}

// TowerDelegationMsg is kind 21906 (Part 2 §6.6): the complete dual-signed
// state entrusted to a tower for watch-and-challenge.
type TowerDelegationMsg struct {
	V         int       `json:"v"`
	Registry  string    `json:"registry"`
	ChainID   string    `json:"chainId"`
	ChannelID string    `json:"channelId"`
	State     WireState `json:"state"`
	Note      string    `json:"note,omitempty"`
}

// TowerReceiptMsg is kind 21907. Registry and ChainID qualify the bare
// channel id across coexisting registries; older towers omit them.
type TowerReceiptMsg struct {
	V         int    `json:"v"`
	ChannelID string `json:"channelId"`
	Registry  string `json:"registry,omitempty"`
	ChainID   string `json:"chainId,omitempty"`
	Seq       string `json:"seq"`
	OK        bool   `json:"ok"`
}

// InvoiceMsg is kind 21901.
type InvoiceMsg struct {
	V          int      `json:"v"`
	InvoiceID  string   `json:"invoiceId"`
	Amount     string   `json:"amount"`
	EVMAddress string   `json:"evmAddress"`
	Registry   string   `json:"registry"`
	ChainID    string   `json:"chainId"`
	ChannelID  string   `json:"channelId,omitempty"`
	Memo       string   `json:"memo,omitempty"`
	ExpiresAt  string   `json:"expiresAt"`
	Relays     []string `json:"relays,omitempty"`
}

// Outbound is one message the engine wants transmitted to the counterparty.
type Outbound struct {
	Kind    int
	Payload any // one of the *Msg types; marshal with EncodePayload
}

// EncodePayload renders a message payload as canonical JSON content.
func EncodePayload(msg any) (string, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---------------------------------------------------------- conversions

// ToWire converts a stored state to wire form.
func ToWire(st proofstore.SignedState) WireState {
	w := WireState{
		ChannelID:       strconv.FormatUint(st.Key.ChannelID, 10),
		Registry:        strings.ToLower(st.Key.Registry.Hex()),
		ChainID:         st.Key.ChainID,
		Seq:             strconv.FormatUint(st.Seq, 10),
		TransferredAtoB: st.TransferredAtoB.BigInt().String(),
		TransferredBtoA: st.TransferredBtoA.BigInt().String(),
		LocksRoot:       st.LocksRoot.Hex(),
		LockedAmount:    st.LockedAmount.BigInt().String(),
	}
	if len(st.SigA) > 0 {
		w.SigA = "0x" + hex.EncodeToString(st.SigA)
	}
	if len(st.SigB) > 0 {
		w.SigB = "0x" + hex.EncodeToString(st.SigB)
	}
	return w
}

// FromWire parses a wire state and validates its scalar encodings. It does
// not verify signatures — that is the engine's job.
func FromWire(w WireState) (proofstore.SignedState, error) {
	var st proofstore.SignedState
	channelID, err := strconv.ParseUint(w.ChannelID, 10, 64)
	if err != nil {
		return st, fmt.Errorf("protocol: bad channelId %q", w.ChannelID)
	}
	seq, err := strconv.ParseUint(w.Seq, 10, 64)
	if err != nil {
		return st, fmt.Errorf("protocol: bad seq %q", w.Seq)
	}
	if !strings.HasPrefix(w.Registry, "0x") || len(w.Registry) != 42 {
		return st, fmt.Errorf("protocol: bad registry %q", w.Registry)
	}
	if _, err := strconv.ParseUint(w.ChainID, 10, 64); err != nil {
		return st, fmt.Errorf("protocol: bad chainId %q", w.ChainID)
	}

	st.Key = proofstore.ChannelKey{
		ChainID:   w.ChainID,
		Registry:  util.HexToAddress(w.Registry),
		ChannelID: channelID,
	}
	st.Seq = seq
	if st.TransferredAtoB, err = parseWei(w.TransferredAtoB); err != nil {
		return st, err
	}
	if st.TransferredBtoA, err = parseWei(w.TransferredBtoA); err != nil {
		return st, err
	}
	if st.LockedAmount, err = parseWei(w.LockedAmount); err != nil {
		return st, err
	}
	st.LocksRoot = util.HexToHash(w.LocksRoot)
	if st.SigA, err = parseSig(w.SigA); err != nil {
		return st, err
	}
	if st.SigB, err = parseSig(w.SigB); err != nil {
		return st, err
	}
	return st, nil
}

func parseWei(s string) (proofstore.U256, error) {
	var u proofstore.U256
	if err := u.UnmarshalJSON([]byte(`"` + s + `"`)); err != nil {
		return u, fmt.Errorf("protocol: bad amount %q", s)
	}
	return u, nil
}

func parseSig(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != 65 {
		return nil, fmt.Errorf("protocol: bad signature %q", s)
	}
	return b, nil
}
