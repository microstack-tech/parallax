package nostrmod

import (
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

func nip44ConversationKey(pub, priv string) ([32]byte, error) {
	return nip44.GenerateConversationKey(pub, priv)
}

func encrypt44(plaintext, pub, priv string) (string, error) {
	key, err := nip44.GenerateConversationKey(pub, priv)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, key)
}

func decrypt44(cipher, pub, priv string) (string, error) {
	key, err := nip44.GenerateConversationKey(pub, priv)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(cipher, key)
}

// rewrap gift-wraps an arbitrary (possibly tampered) seal to the recipient,
// bypassing Wrap's construction — the attacker's tool for forgery tests.
func rewrap(seal nostr.Event, recipientPub string) (nostr.Event, error) {
	eph := nostr.GeneratePrivateKey()
	cipher, err := encrypt44(seal.String(), recipientPub, eph)
	if err != nil {
		return nostr.Event{}, err
	}
	wrap := nostr.Event{
		Kind:      nostr.KindGiftWrap,
		Content:   cipher,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"p", recipientPub}},
	}
	if err := wrap.Sign(eph); err != nil {
		return nostr.Event{}, err
	}
	return wrap, nil
}
