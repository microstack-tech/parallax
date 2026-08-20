package proofstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// U256 is a non-negative big integer that marshals to a decimal string, per
// the wire convention (Part 2 §4): JSON numbers lose precision past 2^53.
type U256 struct {
	*big.Int
}

func NewU256(x *big.Int) U256 {
	if x == nil {
		x = new(big.Int)
	}
	return U256{new(big.Int).Set(x)}
}

func U256FromUint64(x uint64) U256 {
	return U256{new(big.Int).SetUint64(x)}
}

func (u U256) MarshalJSON() ([]byte, error) {
	if u.Int == nil {
		return []byte(`"0"`), nil
	}
	if u.Sign() < 0 {
		return nil, errors.New("proofstore: negative U256")
	}
	return json.Marshal(u.String())
}

func (u *U256) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	x, ok := new(big.Int).SetString(s, 10)
	if !ok || x.Sign() < 0 {
		return fmt.Errorf("proofstore: invalid U256 %q", s)
	}
	u.Int = x
	return nil
}

// BigInt returns the wrapped value, never nil.
func (u U256) BigInt() *big.Int {
	if u.Int == nil {
		return new(big.Int)
	}
	return u.Int
}

// Role identifies which on-chain participant this wallet is in a channel.
type Role string

const (
	RoleA Role = "A" // opener
	RoleB Role = "B"
)

// ChannelKey uniquely identifies a channel across coexisting registries and
// chains. The raw on-chain channelId alone is ambiguous when multiple
// registry deployments are configured (Part 3 §11).
type ChannelKey struct {
	ChainID   string       // decimal
	Registry  util.Address //
	ChannelID uint64       // registry ids are a monotonic counter; 2^64 is unreachable
}

func (k ChannelKey) String() string {
	return fmt.Sprintf("%s:%s:%d", k.ChainID, k.Registry.Hex(), k.ChannelID)
}

func (k ChannelKey) domain() (registry.Domain, error) {
	chainID, ok := new(big.Int).SetString(k.ChainID, 10)
	if !ok {
		return registry.Domain{}, fmt.Errorf("proofstore: invalid chain id %q", k.ChainID)
	}
	return registry.Domain{ChainID: chainID, Registry: k.Registry}, nil
}

// Status is the watcher-owned view of the channel's on-chain state,
// recomputed from canonical logs (Part 3 §7).
type Status string

const (
	StatusOpen    Status = "open"
	StatusClosing Status = "closing"
	StatusSettled Status = "settled"
)

// ChannelMeta is the per-channel metadata record (Part 3 §4.1). Poisoned and
// FrozenUntilBlock are persisted so both survive restarts (Part 3 §5).
type ChannelMeta struct {
	Key              ChannelKey   `json:"key"`
	Role             Role         `json:"role"`
	Status           Status       `json:"status"`
	PeerNpub         string       `json:"peerNpub"` // 64-char x-only hex
	PeerAddress      util.Address `json:"peerAddress"`
	ChallengePeriod  uint32       `json:"challengePeriodBlocks"`
	OpenedAtBlock    uint64       `json:"openedAtBlock"`
	FrozenUntilBlock uint64       `json:"frozenUntilBlock"` // coop-close freeze; 0 = unfrozen
	Poisoned         bool         `json:"poisoned"`
	Towers           []string     `json:"towers,omitempty"` // tower npubs
}

// SignedState is the canonical off-chain state object (Part 2 §5). A state
// is complete when both signatures are present; a proposal carries exactly
// one.
type SignedState struct {
	Key             ChannelKey `json:"key"`
	Seq             uint64     `json:"seq"`
	TransferredAtoB U256       `json:"transferredAtoB"`
	TransferredBtoA U256       `json:"transferredBtoA"`
	LocksRoot       util.Hash  `json:"locksRoot"`
	LockedAmount    U256       `json:"lockedAmount"`
	SigA            []byte     `json:"sigA,omitempty"` // 65-byte (r,s,v), participantA
	SigB            []byte     `json:"sigB,omitempty"`
}

// Digest returns the EIP-712 digest for this state, computed by the same
// mirror that is differentially tested against the contract. Two states are
// "the same payload" iff their digests are equal.
func (s *SignedState) Digest() (util.Hash, error) {
	d, err := s.Key.domain()
	if err != nil {
		return util.Hash{}, err
	}
	return d.HashBalanceProof(registry.BalanceProof{
		ChannelID:       new(big.Int).SetUint64(s.Key.ChannelID),
		Seq:             s.Seq,
		TransferredAtoB: s.TransferredAtoB.BigInt(),
		TransferredBtoA: s.TransferredBtoA.BigInt(),
		LocksRoot:       s.LocksRoot,
		LockedAmount:    s.LockedAmount.BigInt(),
	}), nil
}

// Complete reports whether both signatures are present.
func (s *SignedState) Complete() bool {
	return len(s.SigA) == 65 && len(s.SigB) == 65
}

// SigOf returns the signature slot for the given role.
func (s *SignedState) SigOf(r Role) []byte {
	if r == RoleA {
		return s.SigA
	}
	return s.SigB
}

// Deposits is the confirmed on-chain funding view used for balance
// sufficiency (Part 3 §8): only ≥3-conf figures, plus the scan watermark.
type Deposits struct {
	DepositA         U256   `json:"depositA"`
	DepositB         U256   `json:"depositB"`
	WithdrawnA       U256   `json:"withdrawnA"`
	WithdrawnB       U256   `json:"withdrawnB"`
	LastScannedBlock uint64 `json:"lastScannedBlock"`
}

// Invoice is the merchant-mode invoice record (Part 2 §6.1).
type Invoice struct {
	ID        string       `json:"invoiceId"` // 16-byte hex
	AmountWei U256         `json:"amount"`
	Memo      string       `json:"memo,omitempty"`
	ExpiresAt int64        `json:"expiresAt"` // unix
	ChannelID uint64       `json:"channelId,omitempty"`
	Paid      bool         `json:"paid"`
	PaidBy    *ChannelKey  `json:"paidBy,omitempty"`
	PaidSeq   uint64       `json:"paidSeq,omitempty"`
	Merchant  util.Address `json:"evmAddress"`
}

// Delegation is a tower-mode record: the max-seq complete state a delegator
// has entrusted for watch-and-challenge (Part 2 §6.6).
type Delegation struct {
	State          SignedState `json:"state"`
	DelegatorNpub  string      `json:"delegatorNpub"`
	ReceivedAtUnix int64       `json:"receivedAt"`
}
