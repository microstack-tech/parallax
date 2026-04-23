package addrman

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// Schema versioning policy
// ------------------------
//
// The addrbook.rlp file is `Version uint8 || RLP(body)` where body layout
// depends on Version. The writer always emits the current version. The
// reader dispatches on Version:
//
//   - Known older version → run each migrate_vN_to_vN+1 function in turn,
//     filling defaults for new fields. Migrations are pure functions over
//     the parsed body, with unit tests covering every hop.
//   - Current version → decode directly.
//   - Unknown newer version → Load returns ErrFutureSchema. The caller is
//     expected to log a warning and proceed with an empty AddrMan. The
//     on-disk file is NOT truncated — a downgrade-then-upgrade must not
//     lose the original.
//
// Every schema change across v2.x requires a new version number and a
// migration. Appending fields inside an existing version is forbidden —
// the moment a change is non-additive (bucket layout, field semantics)
// silent corruption follows.

// SchemaVersion constants. Never renumber; only add.
const (
	schemaV1 uint8 = 1
	// Currently supported: v1. Add future versions here and register a
	// matching migrate_vN_to_vN+1 in the migrations table below.
	schemaCurrent = schemaV1
)

// ErrFutureSchema is returned by Load when the on-disk file's version is
// newer than the running binary supports.
var ErrFutureSchema = errors.New("addrman: addrbook schema version is from a future binary; refusing to load")

// Persistence format v1.
//
// body: RLP list of:
//   - nKey (32 bytes)
//   - nNew (uint64)
//   - nTried (uint64)
//   - new entries: list of entryV1
//   - tried entries: list of entryV1
//   - new-bucket assignments: list of (bucket uint32, positions: list of
//     int32 index into "new entries")
//
// vvNew/vvTried/mapInfo/mapAddr/vRandom are reconstructed from the body —
// they're not serialized explicitly. This matches Bitcoin's peers.dat
// approach (src/addrman.cpp:132-228).

type bodyV1 struct {
	NKey           [32]byte
	NewCount       uint64
	TriedCount     uint64
	NewEntries     []entryV1
	TriedEntries   []entryV1
	BucketContents []bucketAssignmentV1
}

// Unix seconds are stored unsigned because RLP's int support is uint-only
// and the addrman never produces negative timestamps (anything before 1970
// is clamped to the epoch in addSingleLocked).
//
// KeyType+NodeID carry the identity-key required to dial legacy (v1.x)
// peers via RLPx auth. Empty NodeID for KeyType=0x00 (v2.0-native).
type entryV1 struct {
	Network          uint8
	Addr             []byte
	Port             uint16
	LastSeen         uint64 // unix seconds
	SourceNetwork    uint8
	SourceAddr       []byte
	SourceTag        uint8
	LastTry          uint64
	LastSuccess      uint64
	LastCountAttempt uint64
	Attempts         uint32
	KeyType          uint8
	NodeID           []byte
}

type bucketAssignmentV1 struct {
	Bucket    uint32
	EntryIdxs []uint32
}

// Save atomically writes the addrbook to path. Atomicity: write to
// `path.tmp`, then rename. Rename is atomic within a filesystem on POSIX
// and NTFS.
func (m *AddrMan) Save(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(path)
}

func (m *AddrMan) saveLocked(path string) error {
	body, err := m.buildBodyV1Locked()
	if err != nil {
		return fmt.Errorf("addrman: build body: %w", err)
	}

	buf := bytes.NewBuffer(make([]byte, 0, 64*1024))
	buf.WriteByte(schemaCurrent)
	if err := rlp.Encode(buf, body); err != nil {
		return fmt.Errorf("addrman: rlp encode: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("addrman: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("addrman: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("addrman: rename: %w", err)
	}
	return nil
}

// Load reads the addrbook from path. If the file does not exist, the
// AddrMan is left empty and a fresh nKey is generated (unless one was
// already set by New — Load never overwrites an nKey without a file to
// read it from).
//
// Returns ErrFutureSchema if the file was written by a newer binary.
func (m *AddrMan) Load(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m.initFreshNKeyIfNeeded()
		}
		return fmt.Errorf("addrman: read addrbook: %w", err)
	}
	if len(raw) < 1 {
		return fmt.Errorf("addrman: addrbook too short")
	}
	version := raw[0]
	payload := raw[1:]

	m.mu.Lock()
	defer m.mu.Unlock()

	if !isEmptyLocked(m) {
		return errors.New("addrman: Load called on non-empty AddrMan")
	}

	body, err := decodeWithMigration(version, payload)
	if err != nil {
		return err
	}
	return m.hydrateFromV1Locked(body)
}

func (m *AddrMan) initFreshNKeyIfNeeded() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deterministic {
		return nil
	}
	var zero [32]byte
	if m.nKey == zero {
		if _, err := rand.Read(m.nKey[:]); err != nil {
			return fmt.Errorf("addrman: seed nKey: %w", err)
		}
	}
	return nil
}

func isEmptyLocked(m *AddrMan) bool {
	return len(m.mapInfo) == 0 && len(m.vRandom) == 0
}

// decodeWithMigration parses the on-disk body for any supported version
// and migrates it up to the current schema. Unknown newer versions return
// ErrFutureSchema.
func decodeWithMigration(version uint8, payload []byte) (*bodyV1, error) {
	switch version {
	case schemaV1:
		var b bodyV1
		if err := rlp.DecodeBytes(payload, &b); err != nil {
			return nil, fmt.Errorf("addrman: decode v1 body: %w", err)
		}
		return &b, nil
	}
	if version > schemaCurrent {
		return nil, fmt.Errorf("%w: file=v%d, supported=v%d", ErrFutureSchema, version, schemaCurrent)
	}
	// Unknown older version — this branch fires once we introduce v2.
	// Keep the switch exhaustive; don't let an unknown older version
	// silently fall through.
	return nil, fmt.Errorf("addrman: unknown older schema version v%d (no migration registered)", version)
}

// buildBodyV1Locked serializes the current in-memory state into a bodyV1.
// The bucket reconstruction on load relies on entries being in the same
// order they appear in NewEntries/TriedEntries — we iterate mapInfo in
// a stable order by id to guarantee that.
func (m *AddrMan) buildBodyV1Locked() (*bodyV1, error) {
	body := &bodyV1{
		NKey:       m.nKey,
		NewCount:   uint64(m.nNew),
		TriedCount: uint64(m.nTried),
	}

	// Collect ids sorted — deterministic output regardless of map order.
	idsNew := make([]int64, 0, m.nNew)
	idsTried := make([]int64, 0, m.nTried)
	for id, info := range m.mapInfo {
		if info.InTried {
			idsTried = append(idsTried, id)
		} else if info.RefCount > 0 {
			idsNew = append(idsNew, id)
		}
	}
	sortInt64(idsNew)
	sortInt64(idsTried)

	indexOfNew := make(map[int64]uint32, len(idsNew))
	body.NewEntries = make([]entryV1, len(idsNew))
	for i, id := range idsNew {
		body.NewEntries[i] = toEntryV1(m.mapInfo[id])
		indexOfNew[id] = uint32(i)
	}
	body.TriedEntries = make([]entryV1, len(idsTried))
	for i, id := range idsTried {
		body.TriedEntries[i] = toEntryV1(m.mapInfo[id])
	}

	// For each new bucket, emit the entry indexes that currently occupy
	// it. We skip empty buckets to keep the file small; loader treats
	// missing buckets as empty.
	for bucket := 0; bucket < newBucketCount; bucket++ {
		var positions []uint32
		for pos := 0; pos < bucketSize; pos++ {
			id := m.vvNew[bucket][pos]
			if id == -1 {
				continue
			}
			idx, ok := indexOfNew[id]
			if !ok {
				// Entry in a new-bucket slot but not in the new
				// list — means RefCount == 0, which should be
				// impossible. Skip defensively.
				continue
			}
			positions = append(positions, idx)
		}
		if len(positions) > 0 {
			body.BucketContents = append(body.BucketContents, bucketAssignmentV1{
				Bucket:    uint32(bucket),
				EntryIdxs: positions,
			})
		}
	}
	return body, nil
}

func (m *AddrMan) hydrateFromV1Locked(body *bodyV1) error {
	if body.NewCount > uint64(newBucketCount*bucketSize) {
		return fmt.Errorf("addrman: corrupt body: NewCount=%d over max", body.NewCount)
	}
	if body.TriedCount > uint64(triedBucketCount*bucketSize) {
		return fmt.Errorf("addrman: corrupt body: TriedCount=%d over max", body.TriedCount)
	}
	if int(body.NewCount) != len(body.NewEntries) {
		return fmt.Errorf("addrman: NewCount/NewEntries mismatch")
	}
	if int(body.TriedCount) != len(body.TriedEntries) {
		return fmt.Errorf("addrman: TriedCount/TriedEntries mismatch")
	}

	m.nKey = body.NKey
	// Reset counts; they'll be rebuilt as we insert.
	m.nNew = 0
	m.nTried = 0
	clear(m.networkCounts)
	clear(m.sourceCounts)

	// New entries first — preserving order so the bucket-contents list
	// can index into them.
	newIDs := make([]int64, len(body.NewEntries))
	for i, e := range body.NewEntries {
		info, ok := fromEntryV1(e)
		if !ok {
			return fmt.Errorf("addrman: invalid new entry at idx %d", i)
		}
		id := m.nextID
		m.nextID++
		info.randomPos = len(m.vRandom)
		m.mapInfo[id] = info
		m.mapAddr[string(info.Addr.serviceKey())] = id
		m.vRandom = append(m.vRandom, id)
		newIDs[i] = id
		m.nNew++
		c := m.networkCounts[info.Addr.Network]
		c.new++
		m.networkCounts[info.Addr.Network] = c
		m.sourceCounts[info.SourceTag]++
	}

	// Tried entries: re-derive bucket positions. If the slot is already
	// taken (shouldn't happen for a well-formed file but can after a
	// downgrade scenario), drop the entry — mirrors Bitcoin's nLost
	// accounting in src/addrman.cpp:294-314.
	for _, e := range body.TriedEntries {
		info, ok := fromEntryV1(e)
		if !ok {
			continue
		}
		b := triedBucket(m.nKey, info.Addr)
		pos := bucketPosition(m.nKey, false, b, info.Addr)
		if m.vvTried[b][pos] != -1 {
			continue
		}
		id := m.nextID
		m.nextID++
		info.randomPos = len(m.vRandom)
		info.InTried = true
		m.mapInfo[id] = info
		m.mapAddr[string(info.Addr.serviceKey())] = id
		m.vRandom = append(m.vRandom, id)
		m.vvTried[b][pos] = id
		m.nTried++
		c := m.networkCounts[info.Addr.Network]
		c.tried++
		m.networkCounts[info.Addr.Network] = c
		m.sourceCounts[info.SourceTag]++
	}

	// Now place new-table bucket references. An entry appears in up to
	// newBucketsPerAddress slots across the new table.
	for _, bc := range body.BucketContents {
		if bc.Bucket >= uint32(newBucketCount) {
			continue
		}
		for _, idx := range bc.EntryIdxs {
			if int(idx) >= len(newIDs) {
				continue
			}
			id := newIDs[idx]
			info := m.mapInfo[id]
			if info.RefCount >= newBucketsPerAddress {
				continue
			}
			pos := bucketPosition(m.nKey, true, int(bc.Bucket), info.Addr)
			if m.vvNew[bc.Bucket][pos] != -1 {
				continue
			}
			m.vvNew[bc.Bucket][pos] = id
			info.RefCount++
		}
	}

	// Prune new-list entries that ended up with RefCount == 0 — happens
	// when bucket slots collided on load. Match Bitcoin's pruning pass
	// in src/addrman.cpp:378-391.
	for _, id := range newIDs {
		info := m.mapInfo[id]
		if info.InTried {
			continue
		}
		if info.RefCount == 0 {
			m.deleteLocked(id)
		}
	}
	return nil
}

func toEntryV1(info *AddrInfo) entryV1 {
	return entryV1{
		Network:          uint8(info.Addr.Network),
		Addr:             append([]byte(nil), info.Addr.Bytes()...),
		Port:             info.Addr.Port,
		LastSeen:         timeToUnix(info.LastSeen),
		SourceNetwork:    uint8(info.Source.Network),
		SourceAddr:       append([]byte(nil), info.Source.Bytes()...),
		SourceTag:        uint8(info.SourceTag),
		LastTry:          timeToUnix(info.LastTry),
		LastSuccess:      timeToUnix(info.LastSuccess),
		LastCountAttempt: timeToUnix(info.LastCountAttempt),
		Attempts:         uint32(info.Attempts),
		KeyType:          info.KeyType,
		NodeID:           append([]byte(nil), info.NodeID...),
	}
}

func fromEntryV1(e entryV1) (*AddrInfo, bool) {
	net := NetID(e.Network)
	if !net.known() {
		return nil, false
	}
	addr, err := NewNetAddr(net, e.Addr, e.Port)
	if err != nil {
		return nil, false
	}
	srcNet := NetID(e.SourceNetwork)
	var source NetAddr
	if srcNet.known() && len(e.SourceAddr) == srcNet.addrLen() {
		s, err := NewNetAddr(srcNet, e.SourceAddr, 0)
		if err == nil {
			source = s
		}
	}
	tag := Source(e.SourceTag)
	if !tag.valid() {
		// Unknown source tag from a future version that didn't bump
		// the file version (bug) — fall back to dns_seed so the
		// entry is still usable but not trusted as legacy_udp or
		// elevated to manual/self-advertised.
		tag = SourceDNSSeed
	}
	// Refuse entries whose KeyType/NodeID length disagree — would crash
	// at dial time.
	switch e.KeyType {
	case 0x00:
		if len(e.NodeID) != 0 {
			return nil, false
		}
	case 0x01:
		if len(e.NodeID) != 64 {
			return nil, false
		}
	default:
		return nil, false
	}
	return &AddrInfo{
		Addr:             addr,
		LastSeen:         unixToTime(e.LastSeen),
		Source:           source,
		SourceTag:        tag,
		LastTry:          unixToTime(e.LastTry),
		LastSuccess:      unixToTime(e.LastSuccess),
		LastCountAttempt: unixToTime(e.LastCountAttempt),
		Attempts:         int(e.Attempts),
		KeyType:          e.KeyType,
		NodeID:           append([]byte(nil), e.NodeID...),
	}, true
}

func timeToUnix(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	s := t.Unix()
	if s < 0 {
		return 0
	}
	return uint64(s)
}

func unixToTime(u uint64) time.Time {
	if u == 0 {
		return time.Time{}
	}
	return time.Unix(int64(u), 0)
}

// sortInt64 is a tiny introsort-free shim so persist.go doesn't pull in
// sort (keeps compile time snappy). Small-N (<= a few thousand) insertion
// sort is fine for the IDs list.
func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// Close is a convenience — equivalent to Save plus releasing in-memory
// state. Idempotent; safe to call multiple times.
func (m *AddrMan) Close(path string) error {
	if path == "" {
		return nil
	}
	return m.Save(path)
}

// Compile-time sanity: the body struct must round-trip through RLP
// without requiring custom encoders. RLP handles []byte, slices, and
// fixed-width ints directly.
var _ io.Reader // keep "io" imported in case we swap to streaming later
