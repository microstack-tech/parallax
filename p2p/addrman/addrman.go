package addrman

import (
	"crypto/rand"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"sync"
	"time"
)

// AddrMan is the Bitcoin-style stochastic address manager. See doc.go for
// the design overview. All exported methods are safe for concurrent use.
type AddrMan struct {
	mu sync.Mutex

	// nKey is the 256-bit per-node bucket-randomization key. Generated
	// once on first load via crypto/rand and persisted inside the same
	// addrbook.rlp file. Never auto-rotated — bucket stability across
	// restarts is what makes Test-before-evict and eviction history
	// meaningful. Only `parallax-cli addrbook reset-key` (Phase 6)
	// regenerates it, and it clears the tried table in the same atomic
	// write.
	nKey [32]byte

	// Internal id allocator. Bitcoin uses nid_type = int64_t to avoid
	// the pre-2024 overflow vulnerability described in
	// https://bitcoincore.org/en/2024/07/31/disclose-addrman-int-overflow/.
	nextID int64

	// mapInfo: id -> entry. We use a map with monotonically increasing
	// ids so we can reuse Bitcoin's bucket-table layout (slot values are
	// ids, -1 sentinel for empty).
	mapInfo map[int64]*AddrInfo
	// mapAddr: canonical address key -> id. Key is the raw bytes of
	// NetAddr.serviceKey, fed through a string conversion to use map
	// hashing; equivalent to Bitcoin's unordered_map<CService, ...>.
	mapAddr map[string]int64

	// vRandom is the set of all ids in insertion order, used by GetAddr
	// to draw without-replacement samples. Entries are swapped to the
	// back as they're drawn; AddrInfo.randomPos tracks the current
	// position.
	vRandom []int64

	// vvNew / vvTried: bucket tables. Slot values are ids or -1 for
	// empty. Sized at construction to avoid allocation on every op.
	vvNew   [newBucketCount][bucketSize]int64
	vvTried [triedBucketCount][bucketSize]int64

	nNew   int
	nTried int

	// tryCollisions holds entries that, on Good(), wanted to move into
	// a tried bucket that was already full. Resolved by ResolveCollisions
	// after we attempt a test-connect to the would-be-evicted entry.
	tryCollisions map[int64]struct{}

	// lastGood is the last time Good() was called on any entry —
	// gates m_last_count_attempt updates so Attempt() only increments
	// the per-entry attempt counter when the attempt is more recent
	// than the last Good(). Initialized to 1s since epoch so "never"
	// is strictly worse, matching Bitcoin.
	lastGood time.Time

	// rng is the source of randomness for bucket selection in Select()
	// and randrange in Add's reference-count stochastic test. Separate
	// from crypto/rand (which feeds nKey) — this one is hot-path and
	// does not need cryptographic strength.
	rng *mrand.Rand

	// networkCounts tracks per-network new/tried sizes for Size(net, ...)
	// and for the Phase-5 "warn when legacy_udp dominates" logging.
	networkCounts map[NetID]newTriedCount

	// Source-tag counts for metrics wiring in Phase 3.
	sourceCounts map[Source]int

	// deterministic disables crypto/rand for nKey and makes rng
	// reproducible. Only for tests.
	deterministic bool
}

type newTriedCount struct {
	new   int
	tried int
}

// Option tunes AddrMan construction.
type Option func(*options)

type options struct {
	deterministic bool
	seed          uint64
}

// Deterministic makes the AddrMan reproducible: nKey is all-zero, the RNG
// is seeded from `seed`. Only for tests.
func Deterministic(seed uint64) Option {
	return func(o *options) {
		o.deterministic = true
		o.seed = seed
	}
}

// New builds an empty AddrMan with a fresh nKey. Persistence is separate —
// call Load to populate from an existing addrbook.rlp, Save to flush.
func New(opts ...Option) (*AddrMan, error) {
	var cfg options
	for _, o := range opts {
		o(&cfg)
	}
	m := &AddrMan{
		mapInfo:       make(map[int64]*AddrInfo),
		mapAddr:       make(map[string]int64),
		tryCollisions: make(map[int64]struct{}),
		networkCounts: make(map[NetID]newTriedCount),
		sourceCounts:  make(map[Source]int),
		deterministic: cfg.deterministic,
	}
	for i := range m.vvNew {
		for j := range m.vvNew[i] {
			m.vvNew[i][j] = -1
		}
	}
	for i := range m.vvTried {
		for j := range m.vvTried[i] {
			m.vvTried[i][j] = -1
		}
	}
	if cfg.deterministic {
		m.rng = mrand.New(mrand.NewPCG(cfg.seed, cfg.seed^0x9e3779b97f4a7c15))
	} else {
		var s1, s2 [8]byte
		if _, err := rand.Read(s1[:]); err != nil {
			return nil, fmt.Errorf("addrman: seed rng: %w", err)
		}
		if _, err := rand.Read(s2[:]); err != nil {
			return nil, fmt.Errorf("addrman: seed rng: %w", err)
		}
		m.rng = mrand.New(mrand.NewPCG(leUint64(s1[:]), leUint64(s2[:])))
		if _, err := rand.Read(m.nKey[:]); err != nil {
			return nil, fmt.Errorf("addrman: seed nKey: %w", err)
		}
	}
	// Bitcoin initializes m_last_good to 1s since epoch so "never" is
	// strictly worse than any real timestamp in Attempt()'s comparison.
	m.lastGood = time.Unix(1, 0)
	return m, nil
}

// Size returns the total entry count, optionally filtered by network and/or
// table. Passing nil for either dimension means "all". Matches
// AddrMan::Size in src/addrman.cpp:1168.
func (m *AddrMan) Size(net *NetID, inNew *bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sizeLocked(net, inNew)
}

func (m *AddrMan) sizeLocked(net *NetID, inNew *bool) int {
	if net == nil {
		if inNew == nil {
			return len(m.vRandom)
		}
		if *inNew {
			return m.nNew
		}
		return m.nTried
	}
	c := m.networkCounts[*net]
	if inNew == nil {
		return c.new + c.tried
	}
	if *inNew {
		return c.new
	}
	return c.tried
}

// Add inserts addrs, attributing them to source for bucket selection and
// sourceTag for later eviction priority. timePenalty is applied to each
// addr's LastSeen. Returns true if at least one address was newly added or
// gained an additional bucket reference.
//
// Mirrors AddrMan::Add (src/addrman.cpp:1177) plus AddSingle
// (src/addrman.cpp:550-624).
func (m *AddrMan) Add(addrs []NetAddr, addrTimes []time.Time, source NetAddr, sourceTag Source, timePenalty time.Duration) bool {
	if len(addrs) == 0 {
		return false
	}
	if len(addrTimes) != 0 && len(addrTimes) != len(addrs) {
		// Caller error — treat as empty LastSeen to avoid a panic.
		addrTimes = nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	added := 0
	now := time.Now()
	for i, a := range addrs {
		var t time.Time
		if addrTimes != nil {
			t = addrTimes[i]
		} else {
			t = now
		}
		if m.addSingleLocked(a, t, source, sourceTag, timePenalty, now) {
			added++
		}
	}
	return added > 0
}

// AddOne is a convenience helper for single-address ingest.
func (m *AddrMan) AddOne(addr NetAddr, lastSeen time.Time, source NetAddr, sourceTag Source, timePenalty time.Duration) bool {
	return m.Add([]NetAddr{addr}, []time.Time{lastSeen}, source, sourceTag, timePenalty)
}

// addSingleLocked mirrors AddrManImpl::AddSingle (src/addrman.cpp:550-624).
//
// The stochastic-test branch ("2^N times harder to increase refcount") is
// critical — without it, a single address source can push an address into
// many new buckets and skew Select() toward it.
func (m *AddrMan) addSingleLocked(addr NetAddr, lastSeen time.Time, source NetAddr, sourceTag Source, timePenalty time.Duration, now time.Time) bool {
	if !addr.Valid() {
		return false
	}

	// Do not penalize self-announcements (source == addr).
	if addr.Equal(withPort(source, addr.Port)) {
		timePenalty = 0
	}

	id, pinfo := m.findLocked(addr)
	if pinfo != nil {
		// Periodically refresh LastSeen. The update cadence is 1h if
		// the entry looks currently-online, 24h otherwise. See
		// src/addrman.cpp:567-571.
		currentlyOnline := now.Sub(pinfo.LastSeen) < 24*time.Hour
		updateInterval := 24 * time.Hour
		if currentlyOnline {
			updateInterval = time.Hour
		}
		if pinfo.LastSeen.Before(lastSeen.Add(-updateInterval - timePenalty)) {
			penalized := lastSeen.Add(-timePenalty)
			if penalized.Before(time.Unix(0, 0)) {
				penalized = time.Unix(0, 0)
			}
			pinfo.LastSeen = penalized
		}

		if !lastSeen.After(pinfo.LastSeen) {
			return false
		}
		if pinfo.InTried {
			return false
		}
		if pinfo.RefCount == newBucketsPerAddress {
			return false
		}
		// Stochastic test: previous refcount N means 2^N times harder
		// to increase it. (src/addrman.cpp:590-593.)
		if pinfo.RefCount > 0 {
			factor := uint64(1) << pinfo.RefCount
			if m.rng.Uint64N(factor) != 0 {
				return false
			}
		}
	} else {
		id, pinfo = m.createLocked(addr, source, sourceTag)
		penalized := lastSeen.Add(-timePenalty)
		if penalized.Before(time.Unix(0, 0)) {
			penalized = time.Unix(0, 0)
		}
		pinfo.LastSeen = penalized
	}

	ubucket := newBucket(m.nKey, pinfo.Addr, source)
	upos := bucketPosition(m.nKey, true, ubucket, pinfo.Addr)
	insert := m.vvNew[ubucket][upos] == -1
	if m.vvNew[ubucket][upos] != id {
		if !insert {
			existing := m.mapInfo[m.vvNew[ubucket][upos]]
			if existing.IsTerrible(now) || (existing.RefCount > 1 && pinfo.RefCount == 0) {
				insert = true
			}
		}
		if insert {
			m.clearNewLocked(ubucket, upos)
			pinfo.RefCount++
			m.vvNew[ubucket][upos] = id
		} else if pinfo.RefCount == 0 {
			m.deleteLocked(id)
			return false
		}
	}
	return insert
}

// Good marks addr as successfully connected. Promotes it from new to tried
// unless the target tried bucket is already full, in which case the entry
// is added to tryCollisions for ResolveCollisions to handle.
// Returns true iff the entry moved into tried.
//
// Mirrors AddrMan::Good -> Good_ (src/addrman.cpp:1186, 626-679).
func (m *AddrMan) Good(addr NetAddr, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.goodLocked(addr, true, now)
}

func (m *AddrMan) goodLocked(addr NetAddr, testBeforeEvict bool, now time.Time) bool {
	id, pinfo := m.findLocked(addr)
	if pinfo == nil {
		return false
	}

	m.lastGood = now
	pinfo.LastSuccess = now
	pinfo.LastTry = now
	pinfo.Attempts = 0
	// LastSeen intentionally NOT updated: see Bitcoin's comment that
	// updating it would leak "currently connected" topology information.

	if pinfo.InTried {
		return false
	}
	if pinfo.RefCount <= 0 {
		// Defensive — the entry should be in at least one new bucket.
		return false
	}

	tBucket := triedBucket(m.nKey, pinfo.Addr)
	tPos := bucketPosition(m.nKey, false, tBucket, pinfo.Addr)

	if testBeforeEvict && m.vvTried[tBucket][tPos] != -1 {
		if len(m.tryCollisions) < addrmanTriedCollisionSize {
			m.tryCollisions[id] = struct{}{}
		}
		return false
	}
	m.makeTriedLocked(pinfo, id)
	return true
}

// Attempt records a connect attempt (successful or not). If countFailure is
// true the per-entry attempt counter increments, but only if the last
// counted attempt was before the most recent Good() — this keeps short
// outages from inflating the counter unboundedly.
//
// Mirrors Attempt_ (src/addrman.cpp:693-711).
func (m *AddrMan) Attempt(addr NetAddr, countFailure bool, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, info := m.findLocked(addr)
	if info == nil {
		return
	}
	info.LastTry = now
	if countFailure && info.LastCountAttempt.Before(m.lastGood) {
		info.LastCountAttempt = now
		info.Attempts++
	}
}

// Connected refreshes an entry's LastSeen. Callers should invoke this only
// at disconnect time, per Bitcoin's net_processing discipline — calling it
// on every keep-alive leaks topology.
//
// Mirrors Connected_ (src/addrman.cpp:877-894).
func (m *AddrMan) Connected(addr NetAddr, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, info := m.findLocked(addr)
	if info == nil {
		return
	}
	if now.Sub(info.LastSeen) > 20*time.Minute {
		info.LastSeen = now
	}
}

// Select picks an address to connect to. With newOnly=true, draws only
// from the new table (Bitcoin's "feeler" mode). Passing a non-empty
// networks slice restricts to those BIP155 networks.
//
// Returns (addr, lastTry, ok). Mirrors Select_ (src/addrman.cpp:713-793).
func (m *AddrMan) Select(newOnly bool, networks []NetID) (NetAddr, time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.vRandom) == 0 {
		return NetAddr{}, time.Time{}, false
	}

	newCount, triedCount := m.nNew, m.nTried
	var netSet map[NetID]struct{}
	if len(networks) > 0 {
		netSet = make(map[NetID]struct{}, len(networks))
		for _, n := range networks {
			netSet[n] = struct{}{}
		}
		newCount, triedCount = 0, 0
		for n := range netSet {
			c := m.networkCounts[n]
			newCount += c.new
			triedCount += c.tried
		}
	}

	if newOnly && newCount == 0 {
		return NetAddr{}, time.Time{}, false
	}
	if newCount+triedCount == 0 {
		return NetAddr{}, time.Time{}, false
	}

	var searchTried bool
	switch {
	case newOnly || triedCount == 0:
		searchTried = false
	case newCount == 0:
		searchTried = true
	default:
		// 50/50 (Bitcoin: insecure_rand.randbool()).
		searchTried = m.rng.IntN(2) == 0
	}

	bucketCount := newBucketCount
	if searchTried {
		bucketCount = triedBucketCount
	}

	now := time.Now()
	chanceFactor := 1.0
	// Hard upper bound on iterations so a poorly-populated table
	// doesn't spin forever. The typical Bitcoin call exits in <10 tries.
	for iter := 0; iter < 64_000; iter++ {
		bucket := m.rng.IntN(bucketCount)
		initial := m.rng.IntN(bucketSize)
		var id int64
		i := 0
		for ; i < bucketSize; i++ {
			pos := (initial + i) % bucketSize
			id = m.entryAt(searchTried, bucket, pos)
			if id == -1 {
				continue
			}
			if netSet == nil {
				break
			}
			if _, ok := netSet[m.mapInfo[id].Addr.Network]; ok {
				break
			}
		}
		if i == bucketSize {
			continue
		}
		info := m.mapInfo[id]
		// 30-bit precision, matches Bitcoin: randbits<30>() <
		// chance_factor * chance * (1 << 30).
		if float64(m.rng.Uint32N(1<<30)) < chanceFactor*info.chance(now)*float64(int64(1)<<30) {
			return info.Addr, info.LastTry, true
		}
		chanceFactor *= 1.2
	}
	return NetAddr{}, time.Time{}, false
}

// GetAddr returns a random sample of up to maxAddresses addresses (or
// maxPct percent of the table, whichever is smaller). Addresses failing
// IsTerrible are skipped when filtered is true. Used by parallax-disc/1
// `Peers` responses in Phase 4.
//
// Mirrors GetAddr_ (src/addrman.cpp:812-851).
func (m *AddrMan) GetAddr(maxAddresses, maxPct int, network *NetID, filtered bool) []NetAddr {
	m.mu.Lock()
	defer m.mu.Unlock()

	nNodes := len(m.vRandom)
	if maxPct != 0 {
		if maxPct > 100 {
			maxPct = 100
		}
		nNodes = maxPct * nNodes / 100
	}
	if maxAddresses != 0 && nNodes > maxAddresses {
		nNodes = maxAddresses
	}

	now := time.Now()
	out := make([]NetAddr, 0, nNodes)
	for n := 0; n < len(m.vRandom); n++ {
		if len(out) >= nNodes {
			break
		}
		// Partial Fisher-Yates: swap vRandom[n] with a random index
		// in [n, len). Matches src/addrman.cpp:834.
		rndPos := n + m.rng.IntN(len(m.vRandom)-n)
		m.swapRandomLocked(n, rndPos)
		info := m.mapInfo[m.vRandom[n]]
		if network != nil && info.Addr.Network != *network {
			continue
		}
		if filtered && info.IsTerrible(now) {
			continue
		}
		out = append(out, info.Addr)
	}
	return out
}

// ResolveCollisions walks tryCollisions and promotes or evicts according
// to Bitcoin's Test-before-evict window. Invoke periodically from the
// dialer (Phase 3 wiring).
//
// Mirrors ResolveCollisions_ (src/addrman.cpp:912-973).
func (m *AddrMan) ResolveCollisions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id := range m.tryCollisions {
		erase := false
		infoNew, ok := m.mapInfo[id]
		if !ok {
			erase = true
		} else {
			tBucket := triedBucket(m.nKey, infoNew.Addr)
			tPos := bucketPosition(m.nKey, false, tBucket, infoNew.Addr)
			switch {
			case !infoNew.Addr.Valid():
				erase = true
			case m.vvTried[tBucket][tPos] != -1:
				oldID := m.vvTried[tBucket][tPos]
				infoOld := m.mapInfo[oldID]
				switch {
				case !infoOld.LastSuccess.IsZero() && now.Sub(infoOld.LastSuccess) < addrmanReplacement:
					erase = true
				case !infoOld.LastTry.IsZero() && now.Sub(infoOld.LastTry) < addrmanReplacement:
					if now.Sub(infoOld.LastTry) > time.Minute {
						m.goodLocked(infoNew.Addr, false, now)
						erase = true
					}
				case !infoNew.LastSuccess.IsZero() && now.Sub(infoNew.LastSuccess) > addrmanTestWindow:
					m.goodLocked(infoNew.Addr, false, now)
					erase = true
				}
			default:
				// Collision resolved elsewhere.
				m.goodLocked(infoNew.Addr, false, now)
				erase = true
			}
		}
		if erase {
			delete(m.tryCollisions, id)
		}
	}
}

// SelectTriedCollision returns a random entry the collision set wants to
// probe (test-before-evict). Dialer calls Attempt on the returned address;
// ResolveCollisions then promotes-or-evicts.
//
// Mirrors SelectTriedCollision_ (src/addrman.cpp:975-1001).
func (m *AddrMan) SelectTriedCollision() (NetAddr, time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.tryCollisions) == 0 {
		return NetAddr{}, time.Time{}, false
	}
	// Random map iteration yields a different element each call in Go
	// (sufficiently random for this use — Bitcoin uses std::advance
	// with insecure_rand, which is also not crypto-strength).
	pick := m.rng.IntN(len(m.tryCollisions))
	var idNew int64
	i := 0
	for id := range m.tryCollisions {
		if i == pick {
			idNew = id
			break
		}
		i++
	}
	newInfo, ok := m.mapInfo[idNew]
	if !ok {
		delete(m.tryCollisions, idNew)
		return NetAddr{}, time.Time{}, false
	}
	tBucket := triedBucket(m.nKey, newInfo.Addr)
	tPos := bucketPosition(m.nKey, false, tBucket, newInfo.Addr)
	oldID := m.vvTried[tBucket][tPos]
	if oldID == -1 {
		return NetAddr{}, time.Time{}, false
	}
	oldInfo := m.mapInfo[oldID]
	return oldInfo.Addr, oldInfo.LastTry, true
}

// FindAddressPosition returns the (tried, bucket, position, multiplicity)
// location of addr. Test-only. Matches FindAddressEntry_
// (src/addrman.cpp:1003-1024).
type AddressPosition struct {
	Tried        bool
	Multiplicity int
	Bucket       int
	Position     int
}

func (m *AddrMan) FindAddressPosition(addr NetAddr) (AddressPosition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, info := m.findLocked(addr)
	if info == nil {
		return AddressPosition{}, false
	}
	if info.InTried {
		b := triedBucket(m.nKey, info.Addr)
		return AddressPosition{
			Tried:        true,
			Multiplicity: 1,
			Bucket:       b,
			Position:     bucketPosition(m.nKey, false, b, info.Addr),
		}, true
	}
	b := newBucket(m.nKey, info.Addr, info.Source)
	return AddressPosition{
		Tried:        false,
		Multiplicity: info.RefCount,
		Bucket:       b,
		Position:     bucketPosition(m.nKey, true, b, info.Addr),
	}, true
}

// CountsBySource returns a shallow copy of the per-source entry counts.
// Used by Phase-3 metrics wiring; zero-alloc on the hot path is not a
// concern because this is read infrequently.
func (m *AddrMan) CountsBySource() map[Source]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[Source]int, len(m.sourceCounts))
	for k, v := range m.sourceCounts {
		out[k] = v
	}
	return out
}

// ---- private helpers ------------------------------------------------------

func (m *AddrMan) findLocked(addr NetAddr) (int64, *AddrInfo) {
	id, ok := m.mapAddr[string(addr.serviceKey())]
	if !ok {
		return -1, nil
	}
	return id, m.mapInfo[id]
}

func (m *AddrMan) createLocked(addr NetAddr, source NetAddr, tag Source) (int64, *AddrInfo) {
	id := m.nextID
	m.nextID++
	info := &AddrInfo{
		Addr:      addr,
		Source:    withPort(source, 0),
		SourceTag: tag,
		randomPos: len(m.vRandom),
	}
	m.mapInfo[id] = info
	m.mapAddr[string(addr.serviceKey())] = id
	m.vRandom = append(m.vRandom, id)
	m.nNew++
	c := m.networkCounts[addr.Network]
	c.new++
	m.networkCounts[addr.Network] = c
	m.sourceCounts[tag]++
	return id, info
}

func (m *AddrMan) deleteLocked(id int64) {
	info := m.mapInfo[id]
	if info.InTried || info.RefCount != 0 {
		// Defensive — matches Bitcoin's assert. Silently refuse.
		return
	}
	m.swapRandomLocked(info.randomPos, len(m.vRandom)-1)
	m.vRandom = m.vRandom[:len(m.vRandom)-1]
	delete(m.mapAddr, string(info.Addr.serviceKey()))
	delete(m.mapInfo, id)
	m.nNew--
	c := m.networkCounts[info.Addr.Network]
	c.new--
	m.networkCounts[info.Addr.Network] = c
	m.sourceCounts[info.SourceTag]--
}

func (m *AddrMan) swapRandomLocked(i, j int) {
	if i == j {
		return
	}
	id1 := m.vRandom[i]
	id2 := m.vRandom[j]
	m.mapInfo[id1].randomPos = j
	m.mapInfo[id2].randomPos = i
	m.vRandom[i], m.vRandom[j] = id2, id1
}

func (m *AddrMan) clearNewLocked(ubucket, upos int) {
	id := m.vvNew[ubucket][upos]
	if id == -1 {
		return
	}
	info := m.mapInfo[id]
	info.RefCount--
	m.vvNew[ubucket][upos] = -1
	if info.RefCount == 0 {
		m.deleteLocked(id)
	}
}

// makeTriedLocked moves an entry from the new table(s) to the tried table,
// evicting whatever was already in the target tried slot (that evictee
// moves back to new). Mirrors MakeTried (src/addrman.cpp:491-548).
func (m *AddrMan) makeTriedLocked(info *AddrInfo, id int64) {
	startBucket := newBucket(m.nKey, info.Addr, info.Source)
	for n := 0; n < newBucketCount; n++ {
		bucket := (startBucket + n) % newBucketCount
		pos := bucketPosition(m.nKey, true, bucket, info.Addr)
		if m.vvNew[bucket][pos] == id {
			m.vvNew[bucket][pos] = -1
			info.RefCount--
			if info.RefCount == 0 {
				break
			}
		}
	}
	m.nNew--
	c := m.networkCounts[info.Addr.Network]
	c.new--
	m.networkCounts[info.Addr.Network] = c

	tBucket := triedBucket(m.nKey, info.Addr)
	tPos := bucketPosition(m.nKey, false, tBucket, info.Addr)

	if evictID := m.vvTried[tBucket][tPos]; evictID != -1 {
		evict := m.mapInfo[evictID]
		evict.InTried = false
		m.vvTried[tBucket][tPos] = -1
		m.nTried--
		ec := m.networkCounts[evict.Addr.Network]
		ec.tried--
		m.networkCounts[evict.Addr.Network] = ec

		uBucket := newBucket(m.nKey, evict.Addr, evict.Source)
		uPos := bucketPosition(m.nKey, true, uBucket, evict.Addr)
		m.clearNewLocked(uBucket, uPos)
		evict.RefCount = 1
		m.vvNew[uBucket][uPos] = evictID
		m.nNew++
		ec2 := m.networkCounts[evict.Addr.Network]
		ec2.new++
		m.networkCounts[evict.Addr.Network] = ec2
	}
	m.vvTried[tBucket][tPos] = id
	m.nTried++
	info.InTried = true
	tc := m.networkCounts[info.Addr.Network]
	tc.tried++
	m.networkCounts[info.Addr.Network] = tc
}

func (m *AddrMan) entryAt(tried bool, bucket, pos int) int64 {
	if tried {
		return m.vvTried[bucket][pos]
	}
	return m.vvNew[bucket][pos]
}

// withPort returns a copy of a with its port replaced. Used to normalize
// source-address comparisons in addSingleLocked (Bitcoin compares CAddress
// to CNetAddr, ignoring port on the source side).
func withPort(a NetAddr, port uint16) NetAddr {
	out := a
	out.Port = port
	return out
}

func leUint64(b []byte) uint64 {
	_ = b[7]
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// Sentinel errors for consumers — not used internally.
var (
	ErrClosed = errors.New("addrman: closed")
)
