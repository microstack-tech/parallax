package addrman

import (
	"crypto/sha256"
	"encoding/binary"
)

// Bucket-layout constants — mirror Bitcoin Core src/addrman_impl.h:26-33.
// Do not tune these without re-reading the Heilman et al. eclipse-attack
// analysis; the 256 × 64 tried / 1024 × 64 new layout, combined with the
// per-source group bucketing, is what bounds an attacker's ability to
// monopolize a victim's peer table.
const (
	triedBucketCountLog2 = 8
	triedBucketCount     = 1 << triedBucketCountLog2 // 256

	newBucketCountLog2 = 10
	newBucketCount     = 1 << newBucketCountLog2 // 1024

	bucketSizeLog2 = 6
	bucketSize     = 1 << bucketSizeLog2 // 64

	// Over how many tried buckets entries from one source group can
	// appear. src/addrman.cpp:28.
	triedBucketsPerGroup = 8
	// Over how many new buckets entries from one source group can appear.
	// src/addrman.cpp:30.
	newBucketsPerSourceGroup = 64
	// Max times a single address can occupy positions in the new table.
	// src/addrman.cpp:32.
	newBucketsPerAddress = 8
)

// cheapHash hashes a concatenation of byte slices with SHA-256 keyed by
// nKey and returns the first 8 bytes as a little-endian uint64. Matches
// Bitcoin Core's HashWriter::GetCheapHash semantics in src/hash.h — a
// truncated SHA-256 is cryptographically strong enough for bucket
// assignment and nKey protects the hash from offline precomputation by
// adversaries who do not know the victim's nKey.
func cheapHash(nKey [32]byte, parts ...[]byte) uint64 {
	h := sha256.New()
	h.Write(nKey[:])
	for _, p := range parts {
		h.Write(p)
	}
	sum := h.Sum(nil)
	return binary.LittleEndian.Uint64(sum[:8])
}

// triedBucket returns which of the 256 tried buckets addr belongs in.
// Ported from AddrInfo::GetTriedBucket (src/addrman.cpp:48-53).
//
//	hash1 = CheapHash(nKey || addr.serviceKey())
//	hash2 = CheapHash(nKey || group(addr) || (hash1 % 8))
//	bucket = hash2 % 256
func triedBucket(nKey [32]byte, addr NetAddr) int {
	h1 := cheapHash(nKey, addr.serviceKey())
	h1mod := u8(h1 % triedBucketsPerGroup)
	h2 := cheapHash(nKey, addr.group(), h1mod)
	return int(h2 % triedBucketCount)
}

// newBucket returns which of the 1024 new buckets addr belongs in given a
// source peer's network group. Ported from AddrInfo::GetNewBucket
// (src/addrman.cpp:55-61).
//
//	hash1 = CheapHash(nKey || group(addr) || group(src))
//	hash2 = CheapHash(nKey || group(src) || (hash1 % 64))
//	bucket = hash2 % 1024
func newBucket(nKey [32]byte, addr NetAddr, src NetAddr) int {
	srcGroup := src.group()
	h1 := cheapHash(nKey, addr.group(), srcGroup)
	h1mod := u8(h1 % newBucketsPerSourceGroup)
	h2 := cheapHash(nKey, srcGroup, h1mod)
	return int(h2 % newBucketCount)
}

// bucketPosition returns the in-bucket slot (0..63) for addr in a bucket
// keyed by isNew. Ported from AddrInfo::GetBucketPosition
// (src/addrman.cpp:63-67). The 'N'/'K' magic byte keeps new-table and
// tried-table positions independent even when bucket indices collide.
func bucketPosition(nKey [32]byte, isNew bool, bucket int, addr NetAddr) int {
	tag := byte('K')
	if isNew {
		tag = 'N'
	}
	bucketBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(bucketBytes, uint32(bucket))
	h := cheapHash(nKey, []byte{tag}, bucketBytes, addr.serviceKey())
	return int(h % bucketSize)
}

// u8 wraps a scalar as a single byte slice for cheapHash inputs. Bitcoin's
// HashWriter serializes small unsigned integers as a single byte when the
// value fits, which is the case for every use here (mod constants are 8,
// 64, and 1 respectively).
func u8(v uint64) []byte { return []byte{byte(v)} }
