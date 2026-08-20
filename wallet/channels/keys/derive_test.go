package keys

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

var update = flag.Bool("update", false, "regenerate golden derivation vectors")

func TestDeriveDeterministic(t *testing.T) {
	ikm := crypto.Keccak256([]byte("determinism"))
	a, err := DeriveNostrKey(ikm)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveNostrKey(ikm)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("derivation not deterministic")
	}

	other, err := DeriveNostrKey(crypto.Keccak256([]byte("other")))
	if err != nil {
		t.Fatal(err)
	}
	if a.PubX == other.PubX {
		t.Fatal("distinct ikms produced the same npub")
	}
}

func TestDeriveRejectsBadLength(t *testing.T) {
	if _, err := DeriveNostrKey(make([]byte, 31)); err == nil {
		t.Fatal("31-byte ikm accepted")
	}
	if _, err := DeriveNostrKey(nil); err == nil {
		t.Fatal("nil ikm accepted")
	}
}

// TestDeriveMatchesManualHKDF recomputes HKDF-SHA256 from raw HMAC calls
// (independent of x/crypto) and the public point via btcec, so the
// derivation formula in the spec is checked against two implementations.
func TestDeriveMatchesManualHKDF(t *testing.T) {
	ikm := crypto.Keccak256([]byte("manual-hkdf"))

	// HKDF-Extract with empty salt: prk = HMAC(zero-key, ikm).
	mac := hmac.New(sha256.New, make([]byte, 32))
	mac.Write(ikm)
	prk := mac.Sum(nil)

	// HKDF-Expand first block: T(1) = HMAC(prk, info || 0x01).
	info := append([]byte("parallax/nostr/v1"), 0x00) // counter 0x00
	mac = hmac.New(sha256.New, prk)
	mac.Write(info)
	mac.Write([]byte{0x01})
	okm := mac.Sum(nil)

	got, err := DeriveNostrKey(ikm)
	if err != nil {
		t.Fatal(err)
	}

	// The scalar is okm or n-okm depending on Y parity; check via btcec.
	priv, pub := btcec.PrivKeyFromBytes(okm)
	want := okm
	if pub.Y().Bit(0) == 1 {
		negated := priv.Key.Negate().Bytes() // n - okm (btcec negates mod n)
		want = negated[:]
	}
	if !bytes.Equal(got.Priv[:], want) {
		t.Fatalf("scalar mismatch:\n got %x\nwant %x", got.Priv, want)
	}
	if !bytes.Equal(got.PubX[:], pub.X().FillBytes(make([]byte, 32))) {
		t.Fatalf("pubkey X mismatch: got %x want %x", got.PubX, pub.X())
	}
}

// TestDerivedKeyIsValidBIP340 signs and verifies a Schnorr signature with the
// derived pair — exactly what Nostr does with it — proving even-Y
// normalization is consistent.
func TestDerivedKeyIsValidBIP340(t *testing.T) {
	key, err := DeriveNostrKey(crypto.Keccak256([]byte("bip340")))
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := btcec.PrivKeyFromBytes(key.Priv[:])
	msg := sha256.Sum256([]byte("nostr event id"))
	sig, err := schnorr.Sign(priv, msg[:])
	if err != nil {
		t.Fatal(err)
	}
	pub, err := schnorr.ParsePubKey(key.PubX[:])
	if err != nil {
		t.Fatalf("derived PubX is not a valid x-only key: %v", err)
	}
	if !sig.Verify(msg[:], pub) {
		t.Fatal("schnorr signature does not verify against derived x-only pubkey")
	}
}

type vector struct {
	IKM  string `json:"ikm"`
	Priv string `json:"nostrPriv"`
	PubX string `json:"npubHex"`
}

// TestGoldenVectors pins the published derivation vectors
// (testdata/derivation_vectors.json). Independent client implementations
// must reproduce these exactly. Regenerate with: go test -run Golden -update
func TestGoldenVectors(t *testing.T) {
	const path = "testdata/derivation_vectors.json"

	ikms := [][]byte{
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		bytes.Repeat([]byte{0xff}, 32),
		crypto.Keccak256([]byte("parallax-nostr-vector-1")),
		crypto.Keccak256([]byte("parallax-nostr-vector-2")),
		crypto.Keccak256([]byte("parallax-nostr-vector-3")),
	}

	if *update {
		var vs []vector
		for _, ikm := range ikms {
			k, err := DeriveNostrKey(ikm)
			if err != nil {
				t.Fatal(err)
			}
			vs = append(vs, vector{
				IKM:  hex.EncodeToString(ikm),
				Priv: hex.EncodeToString(k.Priv[:]),
				PubX: hex.EncodeToString(k.PubX[:]),
			})
		}
		out, _ := json.MarshalIndent(vs, "", "  ")
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden vectors (run with -update to generate): %v", err)
	}
	var vs []vector
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) != len(ikms) {
		t.Fatalf("vector count mismatch: file has %d, test expects %d", len(vs), len(ikms))
	}
	for i, v := range vs {
		ikm, _ := hex.DecodeString(v.IKM)
		k, err := DeriveNostrKey(ikm)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(k.Priv[:]) != v.Priv || hex.EncodeToString(k.PubX[:]) != v.PubX {
			t.Fatalf("vector %d mismatch:\n got  priv=%x pub=%x\n want priv=%s pub=%s",
				i, k.Priv, k.PubX, v.Priv, v.PubX)
		}
	}
}
