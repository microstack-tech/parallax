// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package disc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

// IsSelfFunc reports whether addr matches the local node's own
// advertised endpoint. AddrmanBackend consults it before writing
// gossiped entries to addrman so a peer can't (accidentally or
// otherwise) cause us to ingest our own external IP as a peer.
// nil disables the check.
type IsSelfFunc func(addr *net.TCPAddr) bool

// HelloProvider returns the local node's outgoing Hello (nonce,
// listen port, services). Called once per outbound session by the
// handler before sending. nil disables Hello sending entirely (used
// by test backends that don't need the v2 greeting).
type HelloProvider func() Hello

// CrossDialHost is the minimal slice of *p2p.Server that
// AddrmanBackend.HandleHello calls to resolve cross-dial duplicates
// after a peer discloses its listen port. Defined as an interface
// (rather than taking *p2p.Server directly) so backend tests can
// drive the dedup hook with a stub.
type CrossDialHost interface {
	FindCrossDialDup(newPeer *p2p.Peer, listenPort uint16) *p2p.Peer
}

// errSelfConnect is returned from HandleHello when the remote's
// echoed nonce matches our own. The session ends; the connection is
// torn down with DiscSelf upstream.
var errSelfConnect = errors.New("disc: self-connect detected via Hello nonce")

// AddrmanBackend is the production Backend implementation. It routes
// parallax-disc/1 traffic into an addrman.AddrMan for storage and
// maintains the external-address Quorum tally. Per-peer rate-limit
// state is kept in handler.go's state struct; the Backend provides the
// buckets on demand via NewIngestBucket.
type AddrmanBackend struct {
	m               *addrman.AddrMan
	Q               *Quorum
	log             logging.Logger
	isSelf          IsSelfFunc
	helloProv       HelloProvider
	crossDialHost   CrossDialHost
	crossDialHostMu sync.RWMutex

	mu          sync.Mutex
	peerBuckets map[PeerKey]*tokenBucket
	// handshakeByID maps peer enode IDs to the human-readable
	// handshake variant ("v2" / "legacy+v2") for admin.peers output.
	// Populated on session start by TrackHandshake, purged on
	// PeerDisconnected.
	handshakeByID map[enode.ID]string
	// peerHello is the per-session record of each peer's Hello.
	// Populated on Hello receipt, purged in PeerDisconnected.
	// Looked up by Server.peerListenAddr to dedup cross-dial pairs
	// (phase 2) and by future eviction telemetry (phase 3).
	peerHello map[PeerKey]Hello

	// peerOutboxes is the per-peer relay channel registered by
	// handler.Run. RelayAddress fans newly-learned addresses into
	// the chosen subset of these. See relay.go for the picker.
	peerOutboxes map[PeerKey]*peerRelayState

	// relayKey is the per-process secret used as the keyed-hash key
	// for the address-relay PRF. 32 random bytes from crypto/rand at
	// backend construction; never persisted, never rotated. Rotating
	// per-process means an observer can't predict our pick set across
	// restarts; rotating per-process-not-per-day means the daily
	// pick rotation is the work of the day-bucket input, not the key.
	relayKey [32]byte
}

// NewAddrmanBackend wraps an addrman and a quorum tally into the
// Backend interface used by Run. isSelf may be nil when the host
// has no notion of self (tests, fuzzing) — in production it should
// be Server.IsSelfEndpoint so a quorum-confirmed self-IP echoed
// back to us via gossip is dropped at the ingest boundary.
// helloProvider may be nil when the host doesn't speak the Hello
// extension (legacy tests). In production it returns the Hello the
// Server wants to advertise.
func NewAddrmanBackend(m *addrman.AddrMan, q *Quorum, log logging.Logger, isSelf IsSelfFunc, helloProvider HelloProvider) *AddrmanBackend {
	if q == nil {
		q = NewQuorum()
	}
	if log == nil {
		log = logging.Root()
	}
	b := &AddrmanBackend{
		m:             m,
		Q:             q,
		log:           log,
		isSelf:        isSelf,
		helloProv:     helloProvider,
		peerBuckets:   make(map[PeerKey]*tokenBucket),
		handshakeByID: make(map[enode.ID]string),
		peerHello:     make(map[PeerKey]Hello),
		peerOutboxes:  make(map[PeerKey]*peerRelayState),
	}
	if _, err := rand.Read(b.relayKey[:]); err != nil {
		// crypto/rand failures are catastrophic (see crypto.io's
		// unfailing-by-design guarantee on linux/getrandom). Panic
		// rather than ship with a zero-key relay PRF.
		panic("disc: cannot read randomness for relayKey: " + err.Error())
	}
	return b
}

// TrackHandshake records the handshake variant used for this session.
// Called by handler.Run once per peer on session start. Used by
// PeerInfo to answer admin.peers' "is this peer v2-only or
// legacy+v2".
func (b *AddrmanBackend) TrackHandshake(peer *p2p.Peer, usingV2 bool) {
	variant := "legacy+v2"
	if usingV2 {
		variant = "v2"
	}
	b.mu.Lock()
	b.handshakeByID[peer.ID()] = variant
	b.mu.Unlock()
}

// PeerHandshake returns the handshake variant previously recorded for
// id, or the empty string if the peer is not currently tracked.
func (b *AddrmanBackend) PeerHandshake(id enode.ID) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.handshakeByID[id]
}

func (b *AddrmanBackend) Log() logging.Logger { return b.log }

// ObserveTheirSource extracts the remote TCP source so the peer's
// YourAddr report can feed quorum on their side. Falls back to
// (0, nil, 0, false) for peers without a resolvable RemoteAddr (test
// pipes, tunneled transports) — the handler sends an all-zero YourAddr
// in that case and the peer ignores it during quorum.
func (b *AddrmanBackend) ObserveTheirSource(peer *p2p.Peer) (uint8, []byte, uint16, bool) {
	ra := peer.RemoteAddr()
	if ra == nil {
		return 0, nil, 0, false
	}
	tcp, ok := ra.(*net.TCPAddr)
	if !ok {
		return 0, nil, 0, false
	}
	if v4 := tcp.IP.To4(); v4 != nil {
		return NetIPv4, v4, uint16(tcp.Port), true
	}
	return NetIPv6, tcp.IP.To16(), uint16(tcp.Port), true
}

// HandleYourAddr feeds a peer's claim about our external address into
// the quorum. The peer's network group for the distinct-group test is
// derived from the peer's own RemoteAddr, NOT from the reported addr —
// the attack being mitigated is one group sybil-voting for a single
// address.
func (b *AddrmanBackend) HandleYourAddr(peer *p2p.Peer, net uint8, addr []byte, port uint16) {
	if net == 0 || len(addr) == 0 {
		// Peer couldn't resolve our address (common behind NAT, or
		// for test pipes). Nothing to feed quorum.
		return
	}
	peerNet, peerAddr, ok := peerNetworkGroup(peer)
	if !ok {
		return
	}
	group := computeGroup(peerNet, peerAddr)
	if len(group) == 0 {
		return
	}
	b.Q.Report(peerKeyFor(peer), net, addr, port, group)
}

// HandlePeers ingests a batch of gossiped PeerEntry records into
// addrman with source=tcp_gossip. The 2-hour gossip penalty on
// LastSeen (PIP-0006 Phase 2 rule: "Subtract a 2-hour penalty when the
// source is gossip rather than direct observation") is applied here.
// Rate limiting is enforced per-peer via the ingest bucket.
func (b *AddrmanBackend) HandlePeers(peer *p2p.Peer, entries []PeerEntry) {
	if b.m == nil || len(entries) == 0 {
		return
	}
	bucket := b.ingestBucketFor(peer)
	sourceNet, sourceAddr, ok := peerNetworkGroup(peer)
	if !ok {
		// Can't bucket the source — addrman needs a CNetAddr for
		// the source-group portion of newBucket. Drop the whole
		// batch; per-peer loss is acceptable.
		return
	}
	source, err := addrman.NewNetAddr(addrmanNetID(sourceNet), sourceAddr, 0)
	if err != nil {
		return
	}

	now := time.Now()
	for _, e := range entries {
		if !bucket.Take(now) {
			// Rate-limit drop — silent (Bitcoin parity).
			continue
		}
		net := addrmanNetID(e.NetworkID)
		naddr, err := addrman.NewNetAddr(net, e.Addr, e.TCPPort)
		if err != nil {
			continue
		}
		// Drop any entry that names our own advertised endpoint.
		// Once our quorum-confirmed external IP propagates to peers
		// (we self-advertise it on every outbound session) it can
		// come back to us via gossip; without this filter we'd
		// write a self-loop into addrman that survives across
		// restarts via addrbook.rlp.
		if b.isSelf != nil {
			if tcp, ok := selfTCPFromEntry(e); ok && b.isSelf(tcp) {
				continue
			}
		}
		// Clamp LastSeen to [now-10min, now+10min] per PIP-0006
		// Phase 2. Future-dating is rejected by falling back to now
		// — matches Bitcoin's ingest, which never trusts a
		// forward-dated address.
		claimed := time.Unix(int64(e.LastSeen), 0)
		if claimed.Before(now.Add(-10 * time.Minute)) {
			claimed = now.Add(-10 * time.Minute)
		}
		if claimed.After(now.Add(10 * time.Minute)) {
			claimed = now
		}
		// Plus the 2-hour gossip penalty applied by the Add path.
		added := b.m.AddOne(naddr, e.KeyType, e.NodeID, claimed, source, addrman.SourceTCPGossip, 2*time.Hour)
		// Bitcoin §RelayAddress: only newly-learned addresses get
		// gossiped onward; if AddOne reports the entry was already
		// known, we'd be amplifying without benefit. Reachability is
		// a rough heuristic — we treat any entry that addrman accepted
		// (passed the IsTerrible-style filters in IngestV2/IngestNode)
		// as reachable for relay purposes, matching Bitcoin's "fReachable
		// from caller" pattern. v2-native (KeyType=0) and legacy
		// (KeyType=1) are both relayable; unknown/invalid would have
		// been rejected upstream.
		if added {
			b.RelayAddress(peer, e, true)
		}
	}
}

// SamplePeers returns up to max entries for a GetPeers response. Draws
// from addrman.GetAddr with filtered=true so IsTerrible entries are
// dropped. Maps each entry back to the PeerEntry wire format with the
// stored KeyType/NodeID.
func (b *AddrmanBackend) SamplePeers(_ *p2p.Peer, max int) []PeerEntry {
	if b.m == nil || max <= 0 {
		return nil
	}
	// Ask addrman for up to max*2 entries so we have slack after
	// filtering out entries that can't be serialized on the wire.
	sample := b.m.GetAddr(max*2, 0, nil, true)
	out := make([]PeerEntry, 0, len(sample))
	for _, addr := range sample {
		if len(out) >= max {
			break
		}
		info := b.m.Lookup(addr)
		if info == nil {
			continue
		}
		// KeyType=0x00 entries gossip with empty NodeID; KeyType=0x01
		// carry 64-byte pubkey.
		out = append(out, PeerEntry{
			NetworkID: uint8(addr.Network),
			Addr:      addr.Bytes(),
			TCPPort:   addr.Port,
			KeyType:   info.KeyType,
			NodeID:    append([]byte(nil), info.NodeID...),
			LastSeen:  uint64(info.LastSeen.Unix()),
		})
	}
	return out
}

// SelfEntry returns the PeerEntry we should advertise to newly-connected
// outbound peers (Bitcoin's addr(self) + getaddr sequence). Empty if no
// self-address has quorum or an override. Called by handler.Run on
// outbound sessions.
func (b *AddrmanBackend) SelfEntry(listenPort uint16) (PeerEntry, bool) {
	net, addr, port, ok := b.Q.Winner()
	if !ok {
		return PeerEntry{}, false
	}
	// If the quorum returned a port of 0 (observation without a port
	// hint), substitute our listen port.
	if port == 0 {
		port = listenPort
	}
	return PeerEntry{
		NetworkID: net,
		Addr:      addr,
		TCPPort:   port,
		KeyType:   KeyTypeNone, // v2.0-native self
		NodeID:    nil,
		LastSeen:  uint64(time.Now().Unix()),
	}, true
}

// ingestBucketFor returns (creating if needed) the per-peer token
// bucket for parallax-disc/1 address ingest.
func (b *AddrmanBackend) ingestBucketFor(peer *p2p.Peer) *tokenBucket {
	key := peerKeyFor(peer)
	b.mu.Lock()
	defer b.mu.Unlock()
	bk, ok := b.peerBuckets[key]
	if ok {
		return bk
	}
	rate, burst := inboundRate, inboundBurst
	if !peer.Inbound() {
		rate, burst = outboundRate, outboundBurst
	}
	bk = newTokenBucket(rate, burst)
	b.peerBuckets[key] = bk
	return bk
}

// PeerDisconnected is a hook Server invokes on session close. Cleans
// per-peer state from the backend's maps so they don't leak.
func (b *AddrmanBackend) PeerDisconnected(peer *p2p.Peer) {
	key := peerKeyFor(peer)
	b.mu.Lock()
	delete(b.peerBuckets, key)
	delete(b.handshakeByID, peer.ID())
	delete(b.peerHello, key)
	b.mu.Unlock()
	b.Q.Disconnect(key)
}

// connectedPeerKeys snapshots the set of peers currently tracked by the
// backend (those that have completed Hello and not yet disconnected).
// Used by RunQuorumRefreshLoop to reconcile Quorum's report tally
// against the actually-connected set — the "drop reports for peers no
// longer connected" half of PIP-0006 §Phase 4's quorum re-eval.
func (b *AddrmanBackend) connectedPeerKeys() map[PeerKey]struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[PeerKey]struct{}, len(b.peerHello))
	for k := range b.peerHello {
		out[k] = struct{}{}
	}
	return out
}

// RunQuorumRefreshLoop is the 1h periodic backstop that recomputes
// Quorum's tally — runs evictStaleLocked plus a connected-peer-set
// reconciliation. Returns when stop fires; caller is responsible for
// goroutine lifetime. Safe to call exactly once per backend.
//
// In production Server.Start spawns this as part of the parallax-disc/1
// wiring. Tests invoke it directly with a synthetic stop chan, or
// exercise Quorum.Refresh in isolation.
func (b *AddrmanBackend) RunQuorumRefreshLoop(stop <-chan struct{}) {
	b.RunQuorumRefreshLoopWithInterval(stop, QuorumRefreshInterval)
}

// RunQuorumRefreshLoopWithInterval is the test-facing variant of
// RunQuorumRefreshLoop. Internal to the package.
func (b *AddrmanBackend) RunQuorumRefreshLoopWithInterval(stop <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			dropped := b.Q.Refresh(now, b.connectedPeerKeys())
			if dropped > 0 {
				b.log.Debug("parallax-disc/1: quorum refresh dropped reports from disconnected peers",
					"dropped", dropped)
			}
		}
	}
}

// LocalHello returns the local node's outgoing Hello (the value sent
// on every outbound parallax-disc/1 session). Returns the zero
// Hello with ProtoVersion=HelloMinProtoVersion if no provider is
// configured — keeps tests usable without a full Server context.
func (b *AddrmanBackend) LocalHello() Hello {
	if b.helloProv == nil {
		return Hello{ProtoVersion: HelloMinProtoVersion}
	}
	return b.helloProv()
}

// HandleHello stores the peer's claimed Hello and runs the
// self-connect check. Returns errSelfConnect when the peer's nonce
// matches our own — the handler propagates this to end the session
// with DiscSelf upstream.
//
// After storing the Hello (and only when a CrossDialHost is wired
// — production: the Server; tests: a stub), runs the cross-dial
// dedup hook: if any other peer's effective listen-addr matches
// the IP+listen-port the new peer just disclosed, those two
// connections are duplicates of the same logical neighbor.
// Tie-break (matches Bitcoin Core's preference for outbound):
//   - if exactly one of the two is inbound, drop it;
//   - same-direction tie: keep the older (smaller p.created).
//
// Disconnection is asynchronous (Disconnect just closes the peer);
// Server's run loop processes delpeer in the next iteration.
func (b *AddrmanBackend) HandleHello(peer *p2p.Peer, h Hello) error {
	if b.helloProv != nil {
		local := b.helloProv()
		var hbuf, lbuf [8]byte
		binary.BigEndian.PutUint64(hbuf[:], h.Nonce)
		binary.BigEndian.PutUint64(lbuf[:], local.Nonce)
		if subtle.ConstantTimeCompare(hbuf[:], lbuf[:]) == 1 {
			return errSelfConnect
		}
	}
	// Record the peer's disclosed tx-relay intent on the Peer object
	// so the eviction algorithm and the tx-broadcast path can see it.
	// A peer that connected to us block-relay-only clears
	// ServiceRelayTx in its Hello (sendHello), and we must not push
	// transactions to it nor mis-protect it in the tx-relay eviction
	// round. Bitcoin Core tracks this as CNode::m_relays_txs, set
	// from the version message's fRelay bit.
	peer.SetRelayTxs(h.Services&ServiceRelayTx != 0)

	key := peerKeyFor(peer)
	b.mu.Lock()
	b.peerHello[key] = h
	b.mu.Unlock()

	b.crossDialHostMu.RLock()
	host := b.crossDialHost
	b.crossDialHostMu.RUnlock()
	if host == nil || h.ListenPort == 0 {
		return nil
	}
	dup := host.FindCrossDialDup(peer, h.ListenPort)
	if dup == nil {
		return nil
	}
	loser := selectCrossDialLoser(dup, peer)
	b.log.Debug("parallax-disc/1: cross-dial dup detected, dropping loser",
		"loser", loser.RemoteAddr(), "loserInbound", loser.Inbound(),
		"keep", winnerOf(dup, peer, loser).RemoteAddr())
	loser.Disconnect(p2p.DiscAlreadyConnected)
	return nil
}

// selectCrossDialLoser picks which of (existing, incoming) to drop
// when both represent the same logical neighbor. Prefers outbound
// retention; on same-direction ties keeps the older connection.
// Exported only to the test scope via the test file in this
// package (lowercase keeps the surface narrow).
func selectCrossDialLoser(existing, incoming *p2p.Peer) *p2p.Peer {
	existingIn := existing.Inbound()
	incomingIn := incoming.Inbound()
	if existingIn != incomingIn {
		// Drop whichever IS inbound.
		if existingIn {
			return existing
		}
		return incoming
	}
	// Same direction. Drop the younger. p.Created() is mclock.AbsTime
	// (monotonic, smaller=older).
	if existing.Created() < incoming.Created() {
		return incoming
	}
	return existing
}

// winnerOf returns the survivor of the (a,b) pair given which one
// was selected as the loser. Used only for log formatting.
func winnerOf(a, b, loser *p2p.Peer) *p2p.Peer {
	if loser == a {
		return b
	}
	return a
}

// PeerHello returns the Hello previously recorded for id, or
// (zero, false) if no Hello has been received on the peer's session.
// Used by Server.peerListenAddr (phase 2) to project an inbound
// peer's effective listen endpoint for cross-dial dedup.
func (b *AddrmanBackend) PeerHello(key PeerKey) (Hello, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h, ok := b.peerHello[key]
	return h, ok
}

// PeerListenPort returns the listen port the peer disclosed in its
// Hello, or (0, false) if no Hello has been received yet (or the
// peer disclosed port=0, "unknown"). Implements
// p2p.PeerListenPortLookup so the Server can dedup inbound peers in
// alreadyConnectedTo and friends.
func (b *AddrmanBackend) PeerListenPort(id enode.ID) (uint16, bool) {
	h, ok := b.PeerHello(PeerKey(id.String()))
	if !ok || h.ListenPort == 0 {
		return 0, false
	}
	return h.ListenPort, true
}

// SetCrossDialHost wires the Server (or a test stub) for the
// cross-dial dedup path that runs in HandleHello. Called once at
// node setup; nil disables the dedup (Hello is still stored, but
// the hook doesn't fire).
func (b *AddrmanBackend) SetCrossDialHost(h CrossDialHost) {
	b.crossDialHostMu.Lock()
	b.crossDialHost = h
	b.crossDialHostMu.Unlock()
}

// peerKeyFor returns the stable PeerKey for the session lifetime. We
// use the enode.ID hex because it's unique per connection.
func peerKeyFor(peer *p2p.Peer) PeerKey {
	id := peer.ID()
	return PeerKey(id.String())
}

// peerNetworkGroup returns the peer's (BIP155 net, raw addr) pair from
// their RemoteAddr. Used by the quorum to tag reports with the
// reporter's network group.
func peerNetworkGroup(peer *p2p.Peer) (uint8, []byte, bool) {
	ra := peer.RemoteAddr()
	if ra == nil {
		return 0, nil, false
	}
	tcp, ok := ra.(*net.TCPAddr)
	if !ok {
		return 0, nil, false
	}
	if v4 := tcp.IP.To4(); v4 != nil {
		return NetIPv4, v4, true
	}
	if v6 := tcp.IP.To16(); v6 != nil {
		return NetIPv6, v6, true
	}
	return 0, nil, false
}

// computeGroup derives the canonical network-group bytes for a peer's
// address. Mirrors addrman's group() — /16 for IPv4, /32 for IPv6.
// Kept local so this package doesn't import addrman's internal helper.
func computeGroup(net uint8, addr []byte) []byte {
	switch net {
	case NetIPv4:
		if len(addr) != 4 {
			return nil
		}
		return []byte{net, addr[0], addr[1]}
	case NetIPv6:
		if len(addr) != 16 {
			return nil
		}
		return []byte{net, addr[0], addr[1], addr[2], addr[3]}
	}
	return nil
}

// addrmanNetID maps the disc-package NetID to addrman's NetID. They're
// structurally identical (same BIP155 codes) but Go's type system
// requires the conversion.
func addrmanNetID(n uint8) addrman.NetID { return addrman.NetID(n) }

// selfTCPFromEntry projects a PeerEntry onto the *net.TCPAddr shape
// IsSelfFunc consumes. Returns ok=false for non-IPv4/IPv6 entries —
// those can never be the local node's listen endpoint.
func selfTCPFromEntry(e PeerEntry) (*net.TCPAddr, bool) {
	switch e.NetworkID {
	case NetIPv4:
		if len(e.Addr) != 4 {
			return nil, false
		}
		return &net.TCPAddr{IP: net.IPv4(e.Addr[0], e.Addr[1], e.Addr[2], e.Addr[3]), Port: int(e.TCPPort)}, true
	case NetIPv6:
		if len(e.Addr) != 16 {
			return nil, false
		}
		return &net.TCPAddr{IP: append(net.IP(nil), e.Addr...), Port: int(e.TCPPort)}, true
	}
	return nil, false
}
