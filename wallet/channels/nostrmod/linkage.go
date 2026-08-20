package nostrmod

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

// KindLinkage is the public npub↔address linkage event (Part 2 §1.1),
// published bare and addressable. Merchants SHOULD publish it; payers SHOULD
// NOT, delivering the same object privately inside the handshake instead.
const KindLinkage = 31910

// linkagePrefix is the exact ASCII prefix of the EIP-191 signed string.
const linkagePrefix = "parallax-nostr-linkage-v1:"

// LinkageContent is the event content / handshake linkage object.
type LinkageContent struct {
	V          int    `json:"v"`
	EVMAddress string `json:"evmAddress,omitempty"`
	EVMSig     string `json:"evmSig,omitempty"`
	Revoked    bool   `json:"revoked,omitempty"`
}

// SignLinkage produces the EIP-191 personal_sign binding of an npub by the
// EVM key: sign("parallax-nostr-linkage-v1:" + <64 lowercase hex of npub>).
func SignLinkage(signer protocol.Signer, npubHex string) (string, error) {
	npubHex = strings.ToLower(npubHex)
	if len(npubHex) != 64 {
		return "", fmt.Errorf("nostrmod: npub must be 64 hex chars, got %d", len(npubHex))
	}
	digest := util.BytesToHash(wallet.TextHash([]byte(linkagePrefix + npubHex)))
	sig, err := signer.SignDigest(digest)
	if err != nil {
		return "", err
	}
	return "0x" + util.Bytes2Hex(sig), nil
}

// VerifyLinkage checks the EVM-side binding: evmSig over the linkage string
// for npubHex recovers to evmAddress.
func VerifyLinkage(npubHex, evmSigHex string, evmAddress util.Address) error {
	npubHex = strings.ToLower(npubHex)
	if len(npubHex) != 64 {
		return fmt.Errorf("nostrmod: npub must be 64 hex chars")
	}
	sig := util.FromHex(evmSigHex)
	if len(sig) != 65 {
		return errors.New("nostrmod: linkage signature must be 65 bytes")
	}
	digest := util.BytesToHash(wallet.TextHash([]byte(linkagePrefix + npubHex)))
	return protocol.VerifySignedBy(digest, sig, evmAddress)
}

// BuildLinkageEvent builds and signs the bare kind-31910 event for
// publication (merchant side).
func BuildLinkageEvent(signer protocol.Signer, nostrPriv string) (nostr.Event, error) {
	selfPub, err := nostr.GetPublicKey(nostrPriv)
	if err != nil {
		return nostr.Event{}, err
	}
	evmSig, err := SignLinkage(signer, selfPub)
	if err != nil {
		return nostr.Event{}, err
	}
	addr := strings.ToLower(signer.Address().Hex())
	content, err := json.Marshal(LinkageContent{V: 1, EVMAddress: addr, EVMSig: evmSig})
	if err != nil {
		return nostr.Event{}, err
	}
	ev := nostr.Event{
		Kind:      KindLinkage,
		CreatedAt: nostr.Now(), // bare event: real timestamp (addressable semantics)
		Tags:      nostr.Tags{nostr.Tag{"d", addr}},
		Content:   string(content),
	}
	if err := ev.Sign(nostrPriv); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// BuildLinkageRevocation builds the addressable replacement that revokes a
// published linkage (same d-tag, later created_at).
func BuildLinkageRevocation(signer protocol.Signer, nostrPriv string) (nostr.Event, error) {
	content, err := json.Marshal(LinkageContent{V: 1, Revoked: true})
	if err != nil {
		return nostr.Event{}, err
	}
	ev := nostr.Event{
		Kind:      KindLinkage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"d", strings.ToLower(signer.Address().Hex())}},
		Content:   string(content),
	}
	if err := ev.Sign(nostrPriv); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

// VerifyLinkageEvent checks a received kind-31910 event in both directions
// (Part 2 §1.1): (a) the Nostr signature is valid for the event's pubkey;
// (b) the EVM signature over that pubkey recovers to the content address,
// which must equal the d-tag. Returns the bound pair.
func VerifyLinkageEvent(ev nostr.Event) (npubHex string, evmAddress util.Address, err error) {
	if ev.Kind != KindLinkage {
		return "", util.Address{}, fmt.Errorf("nostrmod: not a linkage event (kind %d)", ev.Kind)
	}
	if ok, serr := ev.CheckSignature(); !ok || serr != nil {
		return "", util.Address{}, errors.New("nostrmod: invalid linkage event signature")
	}
	var content LinkageContent
	if err := json.Unmarshal([]byte(ev.Content), &content); err != nil {
		return "", util.Address{}, err
	}
	if content.Revoked {
		return "", util.Address{}, errors.New("nostrmod: linkage revoked")
	}
	dTag := ev.Tags.Find("d")
	if dTag == nil || !util.IsHexAddress(content.EVMAddress) || !strings.EqualFold(dTag[1], content.EVMAddress) {
		return "", util.Address{}, errors.New("nostrmod: linkage d-tag / address mismatch")
	}
	addr := util.HexToAddress(content.EVMAddress)
	if err := VerifyLinkage(ev.PubKey, content.EVMSig, addr); err != nil {
		return "", util.Address{}, err
	}
	return ev.PubKey, addr, nil
}
