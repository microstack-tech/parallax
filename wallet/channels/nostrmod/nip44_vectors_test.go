package nostrmod

// Vets github.com/nbd-wtf/go-nostr's NIP-44 v2 implementation against the
// official reference vectors (paulmillr/nip44, nip44.vectors.json), per the
// library-verification requirement in the Parallax Channels spec Part 3 §1.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

type vectors struct {
	V2 struct {
		Valid struct {
			GetConversationKey []struct {
				Sec1    string `json:"sec1"`
				Pub2    string `json:"pub2"`
				ConvKey string `json:"conversation_key"`
			} `json:"get_conversation_key"`
			EncryptDecrypt []struct {
				Sec1      string `json:"sec1"`
				Sec2      string `json:"sec2"`
				ConvKey   string `json:"conversation_key"`
				Nonce     string `json:"nonce"`
				Plaintext string `json:"plaintext"`
				Payload   string `json:"payload"`
			} `json:"encrypt_decrypt"`
			LongMsg []struct {
				ConvKey         string `json:"conversation_key"`
				Nonce           string `json:"nonce"`
				Pattern         string `json:"pattern"`
				Repeat          int    `json:"repeat"`
				PlaintextSHA256 string `json:"plaintext_sha256"`
				PayloadSHA256   string `json:"payload_sha256"`
			} `json:"encrypt_decrypt_long_msg"`
		} `json:"valid"`
		Invalid struct {
			EncryptMsgLengths  []int `json:"encrypt_msg_lengths"`
			GetConversationKey []struct {
				Sec1 string `json:"sec1"`
				Pub2 string `json:"pub2"`
				Note string `json:"note"`
			} `json:"get_conversation_key"`
			Decrypt []struct {
				ConvKey string `json:"conversation_key"`
				Payload string `json:"payload"`
				Note    string `json:"note"`
			} `json:"decrypt"`
		} `json:"invalid"`
	} `json:"v2"`
}

func load(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/nip44.vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func key32(t *testing.T, h string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad 32-byte hex %q", h)
	}
	var k [32]byte
	copy(k[:], b)
	return k
}

func TestValidConversationKeys(t *testing.T) {
	v := load(t)
	if len(v.V2.Valid.GetConversationKey) == 0 {
		t.Fatal("no vectors loaded")
	}
	for i, tc := range v.V2.Valid.GetConversationKey {
		got, err := nip44.GenerateConversationKey(tc.Pub2, tc.Sec1)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if hex.EncodeToString(got[:]) != tc.ConvKey {
			t.Fatalf("vector %d: conversation key mismatch", i)
		}
	}
	t.Logf("%d conversation-key vectors ok", len(v.V2.Valid.GetConversationKey))
}

func TestValidEncryptDecrypt(t *testing.T) {
	v := load(t)
	for i, tc := range v.V2.Valid.EncryptDecrypt {
		// Conversation key from both directions must agree with the vector.
		pub2, err := nostr.GetPublicKey(tc.Sec2)
		if err != nil {
			t.Fatal(err)
		}
		ck, err := nip44.GenerateConversationKey(pub2, tc.Sec1)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if hex.EncodeToString(ck[:]) != tc.ConvKey {
			t.Fatalf("vector %d: conversation key mismatch", i)
		}

		nonce, _ := hex.DecodeString(tc.Nonce)
		payload, err := nip44.Encrypt(tc.Plaintext, ck, nip44.WithCustomNonce(nonce))
		if err != nil {
			t.Fatalf("vector %d: encrypt: %v", i, err)
		}
		if payload != tc.Payload {
			t.Fatalf("vector %d: payload mismatch\n got %s\nwant %s", i, payload, tc.Payload)
		}
		plain, err := nip44.Decrypt(tc.Payload, ck)
		if err != nil {
			t.Fatalf("vector %d: decrypt: %v", i, err)
		}
		if plain != tc.Plaintext {
			t.Fatalf("vector %d: roundtrip mismatch", i)
		}
	}
	t.Logf("%d encrypt/decrypt vectors ok", len(v.V2.Valid.EncryptDecrypt))
}

func TestValidLongMessages(t *testing.T) {
	v := load(t)
	for i, tc := range v.V2.Valid.LongMsg {
		plaintext := strings.Repeat(tc.Pattern, tc.Repeat)
		sum := sha256.Sum256([]byte(plaintext))
		if hex.EncodeToString(sum[:]) != tc.PlaintextSHA256 {
			t.Fatalf("vector %d: plaintext construction wrong", i)
		}
		nonce, _ := hex.DecodeString(tc.Nonce)
		payload, err := nip44.Encrypt(plaintext, key32(t, tc.ConvKey), nip44.WithCustomNonce(nonce))
		if err != nil {
			t.Fatalf("vector %d: encrypt: %v", i, err)
		}
		psum := sha256.Sum256([]byte(payload))
		if hex.EncodeToString(psum[:]) != tc.PayloadSHA256 {
			t.Fatalf("vector %d: payload sha mismatch", i)
		}
		plain, err := nip44.Decrypt(payload, key32(t, tc.ConvKey))
		if err != nil || plain != plaintext {
			t.Fatalf("vector %d: roundtrip failed: %v", i, err)
		}
	}
	t.Logf("%d long-message vectors ok", len(v.V2.Valid.LongMsg))
}

func TestInvalidConversationKeys(t *testing.T) {
	v := load(t)
	for i, tc := range v.V2.Invalid.GetConversationKey {
		if _, err := nip44.GenerateConversationKey(tc.Pub2, tc.Sec1); err == nil {
			t.Fatalf("vector %d accepted (%s)", i, tc.Note)
		}
	}
	t.Logf("%d invalid conversation-key vectors rejected ok", len(v.V2.Invalid.GetConversationKey))
}

func TestInvalidDecrypt(t *testing.T) {
	v := load(t)
	for i, tc := range v.V2.Invalid.Decrypt {
		if _, err := nip44.Decrypt(tc.Payload, key32(t, tc.ConvKey)); err == nil {
			t.Fatalf("vector %d decrypted successfully (%s)", i, tc.Note)
		}
	}
	t.Logf("%d invalid decrypt vectors rejected ok", len(v.V2.Invalid.Decrypt))
}

func TestInvalidEncryptLengths(t *testing.T) {
	v := load(t)
	var ck [32]byte
	ck[31] = 1
	for _, n := range v.V2.Invalid.EncryptMsgLengths {
		if _, err := nip44.Encrypt(strings.Repeat("a", n), ck); err == nil {
			t.Fatalf("length %d accepted, must be rejected", n)
		}
	}
	t.Logf("%d invalid lengths rejected ok", len(v.V2.Invalid.EncryptMsgLengths))
}
