package registry

import (
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

// The golden vectors in testdata/differential_vectors.json are produced by
// the contract itself (GenerateVectors.t.sol in the parallax-channels repo):
// digests come from the deployed hash* views and settlement rows from
// settlementPreview on real channels. Inputs are re-derived here from the
// same deterministic formula, so any divergence between this package and the
// contract fails these tests byte-for-byte.
//
// Derivation, for row i and field index f:
//
//	h        = keccak256(abi.encode(uint256(i)))
//	field(f) = keccak256(abi.encode(h, uint8(f)))

type vectorFile struct {
	ChainID             uint64   `json:"chainId"`
	Registry            string   `json:"registry"`
	DomainSeparator     string   `json:"domainSeparator"`
	Count               int      `json:"count"`
	BalanceProofDigests []string `json:"balanceProofDigests"`
	WithdrawDigests     []string `json:"withdrawDigests"`
	CoopCloseDigests    []string `json:"coopCloseDigests"`
	Settlements         []string `json:"settlements"`
}

func loadVectors(t *testing.T) (vectorFile, Domain) {
	t.Helper()
	raw, err := os.ReadFile("testdata/differential_vectors.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	d := Domain{
		ChainID:  new(big.Int).SetUint64(v.ChainID),
		Registry: util.HexToAddress(v.Registry),
	}
	return v, d
}

func rowBase(i int) []byte {
	var word [32]byte
	big.NewInt(int64(i)).FillBytes(word[:])
	return crypto.Keccak256(word[:])
}

func field(h []byte, f byte) []byte {
	var word [32]byte
	word[31] = f
	return crypto.Keccak256(h, word[:])
}

func fieldBig(h []byte, f byte) *big.Int {
	return new(big.Int).SetBytes(field(h, f))
}

func fieldUint64(h []byte, f byte) uint64 {
	b := field(h, f)
	return new(big.Int).SetBytes(b[24:]).Uint64()
}

func fieldAddress(h []byte, f byte) util.Address {
	return util.BytesToAddress(field(h, f)[12:])
}

func TestDifferentialDomainSeparator(t *testing.T) {
	v, d := loadVectors(t)
	if got := d.Separator().Hex(); !strings.EqualFold(got, v.DomainSeparator) {
		t.Fatalf("domain separator mismatch: go %s, contract %s", got, v.DomainSeparator)
	}
}

func TestDifferentialBalanceProofDigests(t *testing.T) {
	v, d := loadVectors(t)
	for i := 0; i < v.Count; i++ {
		h := rowBase(i)
		p := BalanceProof{
			ChannelID:       fieldBig(h, 0),
			Seq:             fieldUint64(h, 1),
			TransferredAtoB: fieldBig(h, 2),
			TransferredBtoA: fieldBig(h, 3),
			LocksRoot:       util.BytesToHash(field(h, 4)),
			LockedAmount:    fieldBig(h, 5),
		}
		if got := d.HashBalanceProof(p).Hex(); !strings.EqualFold(got, v.BalanceProofDigests[i]) {
			t.Fatalf("row %d: balance proof digest mismatch: go %s, contract %s", i, got, v.BalanceProofDigests[i])
		}
	}
}

func TestDifferentialWithdrawDigests(t *testing.T) {
	v, d := loadVectors(t)
	for i := 0; i < v.Count; i++ {
		h := rowBase(i)
		got := d.HashWithdraw(fieldBig(h, 0), fieldAddress(h, 6), fieldBig(h, 7), fieldUint64(h, 8)).Hex()
		if !strings.EqualFold(got, v.WithdrawDigests[i]) {
			t.Fatalf("row %d: withdraw digest mismatch: go %s, contract %s", i, got, v.WithdrawDigests[i])
		}
	}
}

func TestDifferentialCooperativeCloseDigests(t *testing.T) {
	v, d := loadVectors(t)
	for i := 0; i < v.Count; i++ {
		h := rowBase(i)
		got := d.HashCooperativeClose(fieldBig(h, 0), fieldBig(h, 9), fieldBig(h, 10), fieldUint64(h, 8)).Hex()
		if !strings.EqualFold(got, v.CoopCloseDigests[i]) {
			t.Fatalf("row %d: coop close digest mismatch: go %s, contract %s", i, got, v.CoopCloseDigests[i])
		}
	}
}

func TestDifferentialSettlement(t *testing.T) {
	v, _ := loadVectors(t)
	for i, row := range v.Settlements {
		parts := strings.Split(row, "|")
		if len(parts) != 8 {
			t.Fatalf("row %d: malformed settlement row %q", i, row)
		}
		nums := make([]*big.Int, 8)
		for j, s := range parts {
			n, ok := new(big.Int).SetString(s, 10)
			if !ok {
				t.Fatalf("row %d: bad number %q", i, s)
			}
			nums[j] = n
		}
		balA, balB := Settlement(nums[0], nums[1], nums[2], nums[3], nums[4], nums[5])
		if balA.Cmp(nums[6]) != 0 || balB.Cmp(nums[7]) != 0 {
			t.Fatalf("row %d: settlement mismatch: go (%s, %s), contract (%s, %s)",
				i, balA, balB, nums[6], nums[7])
		}
	}
}
