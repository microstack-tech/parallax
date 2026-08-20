package nostrmod

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

// maxBackdate is the NIP-59 timestamp randomization window (Part 2 §2:
// wrap and seal created_at MUST be randomized up to 48 h in the past; all
// ordering logic uses inner payload fields, never envelope timestamps).
const maxBackdate = 48 * 60 * 60 // seconds

// Wrap encrypts a rumor to the recipient per Part 2 §2: rumor (unsigned) →
// seal (kind 13, signed by the sender, NIP-44 encrypted to the recipient) →
// gift wrap (kind 1059, signed by a single-use random key, NIP-44 encrypted
// to the recipient, p-tagged). This replaces go-nostr's nip59.GiftWrap,
// whose backdating window (~10 h) falls short of the required 48 h.
func Wrap(rumor nostr.Event, recipientPub, senderPriv string) (nostr.Event, error) {
	senderPub, err := nostr.GetPublicKey(senderPriv)
	if err != nil {
		return nostr.Event{}, err
	}

	// Rumors are unsigned per NIP-59; the seal authenticates the sender.
	rumor.Sig = ""
	rumor.PubKey = senderPub
	rumor.ID = rumor.GetID()

	sealKey, err := nip44.GenerateConversationKey(recipientPub, senderPriv)
	if err != nil {
		return nostr.Event{}, err
	}
	rumorCipher, err := nip44.Encrypt(rumor.String(), sealKey)
	if err != nil {
		return nostr.Event{}, err
	}

	seal := nostr.Event{
		Kind:      nostr.KindSeal,
		Content:   rumorCipher,
		CreatedAt: backdated(),
		Tags:      nostr.Tags{},
	}
	if err := seal.Sign(senderPriv); err != nil {
		return nostr.Event{}, err
	}

	ephemeralPriv := nostr.GeneratePrivateKey() // crypto/rand, single-use
	if ephemeralPriv == "" {
		return nostr.Event{}, errors.New("nostrmod: ephemeral key generation failed")
	}
	wrapKey, err := nip44.GenerateConversationKey(recipientPub, ephemeralPriv)
	if err != nil {
		return nostr.Event{}, err
	}
	sealCipher, err := nip44.Encrypt(seal.String(), wrapKey)
	if err != nil {
		return nostr.Event{}, err
	}

	wrap := nostr.Event{
		Kind:      nostr.KindGiftWrap,
		Content:   sealCipher,
		CreatedAt: backdated(),
		Tags:      nostr.Tags{nostr.Tag{"p", recipientPub}},
	}
	if err := wrap.Sign(ephemeralPriv); err != nil {
		return nostr.Event{}, err
	}
	return wrap, nil
}

// Unwrap reverses Wrap: decrypts the gift wrap, verifies and decrypts the
// seal, and returns the rumor together with the seal's (sender's) pubkey.
// The caller MUST verify the returned sender pubkey against the expected
// counterparty npub for the channel before processing the payload
// (Part 2 §2, Part 3 §6) — Unwrap authenticates, the caller authorizes.
func Unwrap(wrap nostr.Event, recipientPriv string) (rumor nostr.Event, senderPub string, err error) {
	if wrap.Kind != nostr.KindGiftWrap {
		return rumor, "", fmt.Errorf("nostrmod: not a gift wrap (kind %d)", wrap.Kind)
	}

	wrapKey, err := nip44.GenerateConversationKey(wrap.PubKey, recipientPriv)
	if err != nil {
		return rumor, "", err
	}
	sealJSON, err := nip44.Decrypt(wrap.Content, wrapKey)
	if err != nil {
		return rumor, "", fmt.Errorf("nostrmod: wrap decrypt: %w", err)
	}

	var seal nostr.Event
	if err := seal.UnmarshalJSON([]byte(sealJSON)); err != nil {
		return rumor, "", fmt.Errorf("nostrmod: seal parse: %w", err)
	}
	if seal.Kind != nostr.KindSeal {
		return rumor, "", fmt.Errorf("nostrmod: not a seal (kind %d)", seal.Kind)
	}
	// The seal's signature is the sender authentication for the whole
	// envelope; nothing inside is trusted before this check.
	if ok, err := seal.CheckSignature(); !ok || err != nil {
		return rumor, "", errors.New("nostrmod: invalid seal signature")
	}

	sealKey, err := nip44.GenerateConversationKey(seal.PubKey, recipientPriv)
	if err != nil {
		return rumor, "", err
	}
	rumorJSON, err := nip44.Decrypt(seal.Content, sealKey)
	if err != nil {
		return rumor, "", fmt.Errorf("nostrmod: rumor decrypt: %w", err)
	}
	if err := rumor.UnmarshalJSON([]byte(rumorJSON)); err != nil {
		return rumor, "", fmt.Errorf("nostrmod: rumor parse: %w", err)
	}
	// A rumor claiming a different author than the seal's signer is a
	// spoofing attempt: the seal proves who sealed it, and only that
	// identity may appear as the rumor's author.
	if rumor.PubKey != "" && rumor.PubKey != seal.PubKey {
		return nostr.Event{}, "", errors.New("nostrmod: rumor pubkey does not match seal signer")
	}
	rumor.PubKey = seal.PubKey
	rumor.ID = rumor.GetID()
	return rumor, seal.PubKey, nil
}

// backdated returns now minus a uniform random offset in [0, 48 h].
func backdated() nostr.Timestamp {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable process state; backdating by
		// the full window is the safe degradation (still valid per spec).
		return nostr.Now() - maxBackdate
	}
	offset := binary.BigEndian.Uint64(b[:]) % (maxBackdate + 1)
	return nostr.Now() - nostr.Timestamp(offset)
}
