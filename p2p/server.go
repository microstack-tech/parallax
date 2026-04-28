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

// Package p2p implements the Parallax p2p network protocols.
package p2p

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/banman"
	"github.com/ParallaxProtocol/parallax/p2p/discover"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/p2p/nat"
	"github.com/ParallaxProtocol/parallax/p2p/netutil"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx/bip324handshake"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"github.com/ParallaxProtocol/parallax/support/event"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/util/mclock"
)

const (
	defaultDialTimeout = 15 * time.Second

	// This is the fairness knob for the discovery mixer. When looking for peers, we'll
	// wait this long for a single source of candidates before moving on and trying other
	// sources.
	discmixTimeout = 5 * time.Second

	// Connectivity defaults.
	defaultMaxPendingPeers = 50
	defaultDialRatio       = 3

	// defaultMaxBlockRelayPeers is the default number of outbound
	// slots reserved for block-relay-only peers. Bitcoin Core's
	// MAX_BLOCK_RELAY_ONLY_CONNECTIONS (src/net.h:73). Operators can
	// override via Config.MaxBlockRelayPeers.
	defaultMaxBlockRelayPeers = 2

	// defaultMinLegacyPeers is the number of peer slots withheld from
	// tcp_gossip-sourced candidates while in v2.x. Bounds the blast
	// radius of a v2.0-specific bug by preventing 100% concentration
	// on the new code path. Removed in v3.0 alongside the legacy
	// transports. PIP-0006 §Phase 5.
	defaultMinLegacyPeers = 2

	// This time limits inbound connection attempts per source IP.
	inboundThrottleTime = 30 * time.Second

	// maxInboundConnAttemptsPerIP caps how many distinct inbound TCP
	// attempts from the same source IP are tolerated within
	// inboundThrottleTime. Set above 1 so legitimate co-located
	// scenarios work (e.g. an operator running parallax-disc-crawl
	// on the same host as their parallaxd, which puts the crawler
	// and the daemon behind the same public-NAT IP from any remote
	// peer's POV). Still strict enough to make per-IP flooding
	// expensive — an attacker has to scale across IPs as before, with
	// only a 4x relaxation per IP.
	maxInboundConnAttemptsPerIP = 4

	// Maximum time allowed for reading a complete message.
	// This is effectively the amount of time a connection can be idle.
	frameReadTimeout = 30 * time.Second

	// Maximum amount of time allowed for writing a complete message.
	frameWriteTimeout = 20 * time.Second
)

var errServerStopped = errors.New("server stopped")

// Config holds Server options.
type Config struct {
	// This field must be set to a valid secp256k1 private key.
	PrivateKey *ecdsa.PrivateKey `toml:"-"`

	// MaxPeers is the maximum number of peers that can be
	// connected. It must be greater than zero.
	MaxPeers int

	// MinLegacyPeers is the number of peer slots withheld from
	// tcp_gossip-sourced candidates so the node retains at least
	// this many peers from non-tcp_gossip sources (legacy_udp,
	// dns_seed, manual, self_advertised) when such peers are
	// reachable. Defaults to defaultMinLegacyPeers when zero. Set
	// to a negative value to disable the floor entirely.
	//
	// Rationale: during v2.x, prevents a v2.0 node from ending up
	// with all peers running the same new code — bounds the blast
	// radius of a v2.0-specific bug. Not a defense against any
	// adversary; robustness for early-rollout population
	// concentration. Removed in v3.0 alongside the legacy
	// transports. PIP-0006 §Phase 5.
	MinLegacyPeers int `toml:",omitempty"`

	// MaxPendingPeers is the maximum number of peers that can be pending in the
	// handshake phase, counted separately for inbound and outbound connections.
	// Zero defaults to preset values.
	MaxPendingPeers int `toml:",omitempty"`

	// DialRatio controls the ratio of inbound to dialed connections.
	// Example: a DialRatio of 2 allows 1/2 of connections to be dialed.
	// Setting DialRatio to zero defaults it to 3.
	DialRatio int `toml:",omitempty"`

	// NoDiscovery can be used to disable the peer discovery mechanism.
	// Disabling is useful for protocol debugging (manual topology).
	NoDiscovery bool

	// Name sets the node name of this server.
	// Use util.MakeName to create a name that follows existing conventions.
	Name string `toml:"-"`

	// BootstrapNodes are the NodeID-carrying bootstrap peers used to
	// seed connectivity with the rest of the network. Consumed by
	// discv4's routing table (v1.x-compat peers) and ingested into
	// addrman with KeyType=0x01 via IngestNode.
	BootstrapNodes []*enode.Node

	// BootstrapNodesV2 are the plain-ip:port bootstrap peers used by
	// the Parallax v2.0 BIP324-style handshake. No NodeID / enode URL
	// is required to reach them — the handshake authenticates against
	// whoever answered on that ip:port. Ingested into addrman with
	// source=dns_seed and KeyType=0x00 via IngestV2Addr.
	BootstrapNodesV2 []*net.TCPAddr

	// DNSSeeds are hostnames the node resolves at DNSSeedDefaultInterval
	// (24h, Bitcoin parity) to bootstrap addrman with v2.0-native peers
	// on DNSSeedDefaultPort. Empty disables the resolver loop entirely.
	// Populated from netparams.MainnetDNSSeeds (or testnet equivalent)
	// gated by --dnsseed / --nodiscover.
	DNSSeeds []string `toml:",omitempty"`

	// Static nodes are used as pre-configured connections which are always
	// maintained and re-connected on disconnects.
	StaticNodes []*enode.Node

	// Trusted nodes are used as pre-configured connections which are always
	// allowed to connect, even above the peer limit.
	TrustedNodes []*enode.Node

	// Connectivity can be restricted to certain IP networks.
	// If this option is set to a non-nil value, only hosts which match one of the
	// IP networks contained in the list are considered.
	NetRestrict *netutil.Netlist `toml:",omitempty"`

	// NodeDatabase is the path to the database containing the previously seen
	// live nodes in the network.
	NodeDatabase string `toml:",omitempty"`

	// Protocols should contain the protocols supported
	// by the server. Matching protocols are launched for
	// each peer.
	Protocols []Protocol `toml:"-"`

	// If ListenAddr is set to a non-nil address, the server
	// will listen for incoming connections.
	//
	// If the port is zero, the operating system will pick a port. The
	// ListenAddr field will be updated with the actual address when
	// the server is started.
	ListenAddr string

	// If set to a non-nil value, the given NAT port mapper
	// is used to make the listening port available to the
	// Internet.
	NAT nat.Interface `toml:",omitempty"`

	// If Dialer is set to a non-nil value, the given Dialer
	// is used to dial outbound peer connections.
	Dialer NodeDialer `toml:"-"`

	// If NoDial is true, the server will not dial any peers.
	NoDial bool `toml:",omitempty"`

	// If EnableMsgEvents is set then the server will emit PeerEvents
	// whenever a message is sent to or received from a peer
	EnableMsgEvents bool

	// NodeFilter is an optional function for filtering discovery nodes.
	// If set, nodes that don't pass this filter are evicted from the
	// discovery routing table during revalidation.
	NodeFilter func(*enode.Node) bool `toml:"-"`

	// LegacyDiscoveryMode is the single operator knob controlling
	// this node's compatibility with the v1.x transport stack. It
	// drives three subsystems in lockstep because they all reflect
	// the same v1.x identity model (persistent secp256k1, enode/ENR,
	// legacy RLPx, UDP discovery):
	//
	//   "auto" (default) — discv4 UDP responder-only (answers inbound
	//                      PING/FINDNODE but doesn't drive dialing);
	//                      both legacy and v2 handshakes accepted;
	//                      addrman is the primary dial source.
	//   "on"             — discv4 fully active as a dial candidate
	//                      source; both handshakes accepted. Matches
	//                      pre-PIP-0006 operator expectations.
	//   "off"            — no UDP socket; listener rejects legacy
	//                      RLPx; dialer refuses KeyType=0x01
	//                      (legacy-enode) addrman entries. Every
	//                      peer session uses the v2 handshake. The
	//                      enode URL emitted on startup is
	//                      diagnostic-only.
	//
	// Empty / invalid values fall back to "auto" with a warning.
	// The v2 handshake code path and addrman are always present —
	// this flag only controls whether the legacy v1.x surface is
	// exposed alongside them.
	LegacyDiscoveryMode string `toml:",omitempty"`

	// MaxBlockRelayPeers is the count of outbound peers reserved for
	// block-relay-only slots (Bitcoin Core's
	// MAX_BLOCK_RELAY_ONLY_CONNECTIONS, src/net.h:73 = 2). Block-
	// relay-only peers do not relay transactions or addresses;
	// they're anti-eclipse insurance over and above full-relay.
	// Counted against the existing outbound budget (DialRatio), so
	// raising this lowers the full-relay slot count by the same
	// amount. Zero disables the block-relay-only bucket entirely;
	// every outbound dial becomes full-relay.
	MaxBlockRelayPeers int `toml:",omitempty"`

	// BanList is the BanMan instance the inbound-accept path consults
	// to reject banned and (under saturation) discouraged source IPs.
	// Operator-controlled persistence lives in p2p/banman; the Server
	// only reads. nil disables ban / discourage gating — useful in
	// ephemeral tests. The node layer constructs and assigns this
	// before Server.Start using BanListPath; tests can preset.
	BanList *banman.BanMan `toml:"-"`

	// BanListPath is where banlist.json persists across restarts.
	// Defaults to <datadir>/banlist.json via the node layer. Empty
	// keeps the BanMan in-memory only (fine for ephemeral tests).
	// Honored only when BanList is nil at Start time.
	BanListPath string `toml:",omitempty"`

	// AnchorsPath is the location of anchors.dat. On clean shutdown
	// the (IP, listen-port) of currently-connected block-relay-only
	// outbound peers are persisted there (capped at
	// MaxBlockRelayAnchors); on next startup those peers are
	// redialed as block-relay-only and the file is deleted. Mirrors
	// Bitcoin Core's m_anchors / anchors.dat (src/net.cpp:57).
	// Empty disables anchor persistence — useful for ephemeral
	// tests and for nodes that don't run any block-relay-only
	// peers (MaxBlockRelayPeers=0).
	AnchorsPath string `toml:",omitempty"`

	// AddrBookPath is where the addrbook persists across restarts.
	// Defaults to <datadir>/addrbook.rlp via the node layer. If
	// empty, the addrman still runs in-memory but nothing is
	// persisted on shutdown — useful for ephemeral tests.
	AddrBookPath string `toml:",omitempty"`

	// AddrManager, when non-nil, is an already-initialized address
	// manager supplied by the caller. Server skips internal Load and
	// just adopts this instance — useful when the caller (node.Node)
	// needs the addrman available before Server.Start so it can wire
	// the parallax-disc/1 subprotocol backend against it. Save-on-stop
	// still happens if AddrBookPath is also set.
	AddrManager *addrman.AddrMan `toml:"-"`

	// Logger is a custom logger to use with the p2p.Server.
	Logger logging.Logger `toml:",omitempty"`

	clock mclock.Clock
}

// Server manages all peer connections.
type Server struct {
	// Config fields may not be modified while the server is running.
	Config

	// Hooks for testing. These are useful because we can inhibit
	// the whole protocol stack.
	newTransport func(net.Conn, *ecdsa.PublicKey) transport
	newPeerHook  func(*Peer)
	listenFunc   func(network, addr string) (net.Listener, error)

	lock    sync.Mutex // protects running
	running bool

	listener     net.Listener
	ourHandshake *protoHandshake
	loopWG       sync.WaitGroup // loop, listenLoop
	peerFeed     event.Feed
	log          logging.Logger

	nodedb    *enode.DB
	localnode *enode.LocalNode
	ntab      *discover.UDPv4
	discmix   *enode.FairMix
	dialsched *dialScheduler

	// helloNonce is a 64-bit random value generated once per Server
	// lifetime and embedded in every parallax-disc/1 Hello we send.
	// Receiving our own nonce back identifies a self-connect (the
	// protocol-level analog of Bitcoin Core's nLocalHostNonce, src/
	// net.cpp PushNodeVersion). Read-only after setupLocalNode; safe
	// for concurrent reads from peer goroutines without locking.
	helloNonce uint64

	// peerListenLookup is the disc-protocol's per-peer Hello cache
	// projected as "given this peer ID, what listen port did they
	// disclose?". Used by peerListenAddr to dedup inbound peers
	// (whose RemoteAddr port is ephemeral) against outbound dials.
	// Set once at node setup before Start; nil during tests that
	// don't wire the disc backend.
	peerListenLookup PeerListenPortLookup

	// addrbook is the PIP-0006 address manager. Populated only when
	// Config.ExperimentalAddrMan is true. Feeds the dialer as an
	// additional FairMix source and receives discv4/bootnode entries
	// via teeIter wrappers. Exposed via AddrBook() so upstream code
	// can register subprotocols against it without pulling p2p into
	// an import cycle with p2p/protocols/*.
	addrbook     *addrman.AddrMan
	addrbookIter *addrman.NodeIter
	v2Iter       *addrman.V2Iter

	// v2DialRecent is a per-(ip:port) cooldown keyed by tcp addr
	// string. Gates DialV2 so every caller — runV2Dialer, the v2-ENR
	// branch in the dial scheduler, admin RPC — shares a single
	// throttle. Serialized by v2DialRecentMu.
	v2DialRecentMu sync.Mutex
	v2DialRecent   map[string]time.Time

	// Channels into the run loop.
	quit                    chan struct{}
	addtrusted              chan *enode.Node
	removetrusted           chan *enode.Node
	peerOp                  chan peerOpFunc
	peerOpDone              chan struct{}
	delpeer                 chan peerDrop
	checkpointPostHandshake chan *conn
	checkpointAddPeer       chan *conn

	// State of run loop and listenLoop.
	inboundHistory expHeap
}

type peerOpFunc func(map[enode.ID]*Peer)

type peerDrop struct {
	*Peer
	err       error
	requested bool // true if signaled by the peer
}

// enrV2Transport is the ENR entry a node sets to signal that its
// listening TCP endpoint accepts the BIP324-style v2 handshake.
// Peers that see this on an enode's ENR dial v2 from the start and
// skip the v1 RLPx path, avoiding the "connect v1, tear down, redial
// v2" promotion dance. No payload — presence is the signal.
type enrV2Transport struct {
	Rest []rlp.RawValue `rlp:"tail"`
}

func (enrV2Transport) ENRKey() string { return "pipv2" }

// hasV2TransportENR reports whether n's ENR advertises v2-transport
// support.
func hasV2TransportENR(n *enode.Node) bool {
	if n == nil {
		return false
	}
	var e enrV2Transport
	return n.Load(&e) == nil
}

type connFlag int32

const (
	dynDialedConn connFlag = 1 << iota
	staticDialedConn
	inboundConn
	trustedConn
	// v2DialedConn marks a connection initiated via Server.DialV2.
	// pickHandshakeVariant reads this bit to route outbound v2
	// dials — the legacy dial path never sets it, so existing
	// code paths keep their original semantics after the Phase 2b
	// changes.
	v2DialedConn
	// blockRelayConn marks an outbound peer occupying a block-relay-
	// only slot in the dial scheduler. Block-relay-only peers do not
	// relay transactions or addresses (Bitcoin Core's
	// MAX_BLOCK_RELAY_ONLY_CONNECTIONS, src/net.h:73). The bit
	// propagates to *Peer.blockRelayOnly at attach time so the prl
	// and disc protocol handlers can suppress tx and address gossip.
	blockRelayConn
)

// conn wraps a network connection with information gathered
// during the two handshakes.
type conn struct {
	fd net.Conn
	transport
	node  *enode.Node
	flags connFlag
	cont  chan error // The run loop uses cont to signal errors to SetupConn.
	caps  []Cap      // valid after the protocol handshake
	name  string     // valid after the protocol handshake
}

type transport interface {
	// The two handshakes.
	doEncHandshake(prv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error)
	doProtoHandshake(our *protoHandshake) (*protoHandshake, error)
	// The MsgReadWriter can only be used after the encryption
	// handshake has completed. The code uses conn.id to track this
	// by setting it to a non-nil value after the encryption handshake.
	MsgReadWriter
	// transports must provide Close because we use MsgPipe in some of
	// the tests. Closing the actual network connection doesn't do
	// anything in those tests because MsgPipe doesn't use it.
	close(err error)
}

func (c *conn) String() string {
	s := c.flags.String()
	if (c.node.ID() != enode.ID{}) {
		s += " " + c.node.ID().String()
	}
	s += " " + c.fd.RemoteAddr().String()
	return s
}

func (f connFlag) String() string {
	s := ""
	if f&trustedConn != 0 {
		s += "-trusted"
	}
	if f&dynDialedConn != 0 {
		s += "-dyndial"
	}
	if f&staticDialedConn != 0 {
		s += "-staticdial"
	}
	if f&inboundConn != 0 {
		s += "-inbound"
	}
	if f&blockRelayConn != 0 {
		s += "-blockrelay"
	}
	if s != "" {
		s = s[1:]
	}
	return s
}

func (c *conn) is(f connFlag) bool {
	flags := connFlag(atomic.LoadInt32((*int32)(&c.flags)))
	return flags&f != 0
}

func (c *conn) set(f connFlag, val bool) {
	for {
		oldFlags := connFlag(atomic.LoadInt32((*int32)(&c.flags)))
		flags := oldFlags
		if val {
			flags |= f
		} else {
			flags &= ^f
		}
		if atomic.CompareAndSwapInt32((*int32)(&c.flags), int32(oldFlags), int32(flags)) {
			return
		}
	}
}

// LocalNode returns the local node record.
func (srv *Server) LocalNode() *enode.LocalNode {
	return srv.localnode
}

// HelloNonce returns the per-startup random nonce embedded in every
// outgoing parallax-disc/1 Hello. Bitcoin Core analog: nLocalHostNonce
// (src/net.cpp PushNodeVersion). Stable for the Server's lifetime;
// safe for concurrent reads.
func (srv *Server) HelloNonce() uint64 {
	return srv.helloNonce
}

// PeerListenPortLookup is the minimal surface peerListenAddr needs
// to consult an external per-peer listen-port store. The
// parallax-disc/1 AddrmanBackend records a peer's claimed listen
// port from their Hello message and implements this interface so
// the Server can resolve inbound peers' true listen address (their
// kernel RemoteAddr port is ephemeral, not their listen port).
//
// Returning ok=false means the peer hasn't disclosed a port (yet)
// or doesn't speak the disc protocol — peerListenAddr in turn
// reports unknown for that peer, which behaves correctly in the
// dedup paths (no false positives).
type PeerListenPortLookup interface {
	PeerListenPort(id enode.ID) (uint16, bool)
}

// SetPeerListenPortLookup wires the disc backend (or any other
// per-peer listen-port store) into the Server. Called once at node
// setup before Start; subsequent calls overwrite. Concurrent reads
// from peer goroutines see the latest value via plain field access
// — node setup runs before any peer goroutine, so the
// happens-before is established by Start's startup barrier.
func (srv *Server) SetPeerListenPortLookup(l PeerListenPortLookup) {
	srv.peerListenLookup = l
}

// initHelloNonce draws a 64-bit value from crypto/rand. Called once
// from setupLocalNode before any peer can be accepted.
func (srv *Server) initHelloNonce() error {
	var buf [8]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return fmt.Errorf("hello nonce init: %w", err)
	}
	srv.helloNonce = binary.BigEndian.Uint64(buf[:])
	return nil
}

// Peers returns all connected peers.
func (srv *Server) Peers() []*Peer {
	var ps []*Peer
	srv.doPeerOp(func(peers map[enode.ID]*Peer) {
		for _, p := range peers {
			ps = append(ps, p)
		}
	})
	return ps
}

// PeerCount returns the number of connected peers.
func (srv *Server) PeerCount() int {
	var count int
	srv.doPeerOp(func(ps map[enode.ID]*Peer) {
		count = len(ps)
	})
	return count
}

// AddPeer adds the given node to the static node set. When there is room in the peer set,
// the server will connect to the node. If the connection fails for any reason, the server
// will attempt to reconnect the peer.
func (srv *Server) AddPeer(node *enode.Node) {
	srv.dialsched.addStatic(node)
}

// RemovePeer removes a node from the static node set. It also disconnects from the given
// node if it is currently connected as a peer.
//
// This method blocks until all protocols have exited and the peer is removed. Do not use
// RemovePeer in protocol implementations, call Disconnect on the Peer instead.
func (srv *Server) RemovePeer(node *enode.Node) {
	var (
		ch  chan *PeerEvent
		sub event.Subscription
	)
	// Disconnect the peer on the main loop.
	srv.doPeerOp(func(peers map[enode.ID]*Peer) {
		srv.dialsched.removeStatic(node)
		if peer := peers[node.ID()]; peer != nil {
			ch = make(chan *PeerEvent, 1)
			sub = srv.peerFeed.Subscribe(ch)
			peer.Disconnect(DiscRequested)
		}
	})
	// Wait for the peer connection to end.
	if ch != nil {
		defer sub.Unsubscribe()
		for ev := range ch {
			if ev.Peer == node.ID() && ev.Type == PeerEventTypeDrop {
				return
			}
		}
	}
}

// AddTrustedPeer adds the given node to a reserved trusted list which allows the
// node to always connect, even if the slot are full.
func (srv *Server) AddTrustedPeer(node *enode.Node) {
	select {
	case srv.addtrusted <- node:
	case <-srv.quit:
	}
}

// RemoveTrustedPeer removes the given node from the trusted peer set.
func (srv *Server) RemoveTrustedPeer(node *enode.Node) {
	select {
	case srv.removetrusted <- node:
	case <-srv.quit:
	}
}

// SubscribeEvents subscribes the given channel to peer events
func (srv *Server) SubscribeEvents(ch chan *PeerEvent) event.Subscription {
	return srv.peerFeed.Subscribe(ch)
}

// Self returns the local node's endpoint information.
func (srv *Server) Self() *enode.Node {
	srv.lock.Lock()
	ln := srv.localnode
	srv.lock.Unlock()

	if ln == nil {
		return enode.NewV4(&srv.PrivateKey.PublicKey, net.ParseIP("0.0.0.0"), 0, 0)
	}
	return ln.Node()
}

// Stop terminates the server and all active peer connections.
// It blocks until all active connections have been closed.
func (srv *Server) Stop() {
	srv.lock.Lock()
	if !srv.running {
		srv.lock.Unlock()
		return
	}
	srv.running = false
	if srv.listener != nil {
		// this unblocks listener Accept
		srv.listener.Close()
	}
	if srv.addrbookIter != nil {
		srv.addrbookIter.Close()
	}
	if srv.v2Iter != nil {
		srv.v2Iter.Close()
	}
	close(srv.quit)
	srv.lock.Unlock()
	srv.loopWG.Wait()

	// Persist addrbook on shutdown. Done after loopWG so no inflight
	// Good/Attempt/Add calls race the Save. Failures are logged, not
	// propagated — a save error on shutdown must not crash the node.
	if srv.addrbook != nil && srv.AddrBookPath != "" {
		if err := srv.addrbook.Save(srv.AddrBookPath); err != nil {
			srv.log.Warn("addrbook save failed on shutdown", "path", srv.AddrBookPath, "err", err)
		} else {
			srv.log.Info("addrbook saved", "path", srv.AddrBookPath, "entries", srv.addrbook.Size(nil, nil))
		}
	}
	// Persist block-relay-only anchors so the next startup can
	// re-attach to the same peers in the same role. Bitcoin Core
	// m_anchors / anchors.dat (src/net.cpp:57). Disabled when
	// AnchorsPath is empty.
	srv.persistAnchors()
}

// Start starts running the server.
// Servers can not be re-used after stopping.
func (srv *Server) Start() (err error) {
	srv.lock.Lock()
	defer srv.lock.Unlock()
	if srv.running {
		return errors.New("server already running")
	}
	srv.running = true
	srv.log = srv.Config.Logger
	if srv.log == nil {
		srv.log = logging.Root()
	}
	if srv.clock == nil {
		srv.clock = mclock.System{}
	}
	if srv.NoDial && srv.ListenAddr == "" {
		srv.log.Warn("P2P server will be useless, neither dialing nor listening")
	}

	// static fields
	if srv.PrivateKey == nil {
		return errors.New("Server.PrivateKey must be set to a non-nil key")
	}
	if srv.legacyDiscoveryMode() == legacyDiscoveryOff {
		// --legacy-discovery=off implies v2-only: legacy RLPx is
		// refused at the listener, dialer refuses KeyType=0x01
		// entries, and the enode URL emitted on startup becomes a
		// diagnostic artifact rather than a dialable identifier.
		// The secp256k1 private key is still loaded because
		// LocalNode uses it to derive a stable placeholder for
		// logs and metrics; no peer handshake consumes it in this
		// mode.
		srv.log.Info("--legacy-discovery=off: legacy RLPx refused, enode URL diagnostic-only")
	}
	if srv.newTransport == nil {
		srv.newTransport = newRLPX
	}
	if srv.listenFunc == nil {
		srv.listenFunc = net.Listen
	}
	srv.quit = make(chan struct{})
	srv.delpeer = make(chan peerDrop)
	srv.checkpointPostHandshake = make(chan *conn)
	srv.checkpointAddPeer = make(chan *conn)
	srv.addtrusted = make(chan *enode.Node)
	srv.removetrusted = make(chan *enode.Node)
	srv.peerOp = make(chan peerOpFunc)
	srv.peerOpDone = make(chan struct{})

	if err := srv.setupLocalNode(); err != nil {
		return err
	}
	if srv.ListenAddr != "" {
		if err := srv.setupListening(); err != nil {
			return err
		}
	}
	srv.applyLegacyDiscoveryMode()
	if err := srv.setupAddrMan(); err != nil {
		return err
	}
	if err := srv.setupDiscovery(); err != nil {
		return err
	}
	srv.setupDialScheduler()
	srv.replayAnchors()

	srv.loopWG.Add(1)
	go srv.run()
	return nil
}

// setupAddrMan initializes the address manager. Always runs — the
// addrman is the v2 design and is not operator-optional anymore.
//
// Flow:
//
//   - If Config.AddrManager is supplied (the node layer's typical path,
//     wired before Start so parallax-disc/1 can register), adopt it.
//   - Otherwise construct an empty addrman, Load addrbook.rlp if
//     Config.AddrBookPath is non-empty (skipping persistence when it's
//     empty — useful for ephemeral tests), and ingest BootstrapNodes
//     with source=dns_seed.
func (srv *Server) setupAddrMan() error {
	var m *addrman.AddrMan
	if srv.AddrManager != nil {
		m = srv.AddrManager
	} else {
		var err error
		m, err = addrman.New()
		if err != nil {
			return fmt.Errorf("addrman: new: %w", err)
		}
		if srv.AddrBookPath != "" {
			if err := m.Load(srv.AddrBookPath); err != nil {
				if errors.Is(err, addrman.ErrFutureSchema) {
					srv.log.Warn("addrbook schema is from a newer binary; proceeding with empty addrbook", "path", srv.AddrBookPath, "err", err)
				} else {
					srv.log.Warn("failed to load addrbook; proceeding empty", "path", srv.AddrBookPath, "err", err)
				}
			}
		}
		now := time.Now()
		for _, n := range srv.BootstrapNodes {
			addrman.IngestNode(m, n, addrman.SourceDNSSeed, now)
		}
		for _, addr := range srv.BootstrapNodesV2 {
			addrman.IngestV2Addr(m, addr, addrman.SourceDNSSeed, now)
		}
	}
	srv.addrbook = m
	srv.log.Info("addrman enabled", "path", srv.AddrBookPath, "entries", m.Size(nil, nil))

	// Periodic metrics refresh and legacy_udp dominance log. Cheap —
	// Size() is O(1) and sourceCounts is a small map. Tied to quit so
	// Stop() tears it down cleanly.
	srv.loopWG.Add(1)
	go func() {
		defer srv.loopWG.Done()
		metricsTick := time.NewTicker(5 * time.Second)
		defer metricsTick.Stop()
		dominanceTick := time.NewTicker(15 * time.Minute)
		defer dominanceTick.Stop()
		for {
			select {
			case <-srv.quit:
				return
			case <-metricsTick.C:
				srv.addrbook.RefreshMetrics()
			case <-dominanceTick.C:
				srv.warnOnLegacyUDPDominance()
			}
		}
	}()

	// DNS-seed resolver loop. Plain A/AAAA at DNSSeedDefaultInterval,
	// each IP paired with DNSSeedDefaultPort and ingested into addrman
	// with source=dns_seed. Empty Config.DNSSeeds disables it (matches
	// --nodiscover semantics).
	if len(srv.DNSSeeds) > 0 {
		seedCtx, seedCancel := context.WithCancel(context.Background())
		srv.loopWG.Add(2)
		go func() {
			defer srv.loopWG.Done()
			<-srv.quit
			seedCancel()
		}()
		go func() {
			defer srv.loopWG.Done()
			dnsSeedLoop(
				seedCtx,
				net.DefaultResolver,
				srv.DNSSeeds,
				srv.addrbook,
				DNSSeedDefaultPort,
				DNSSeedDefaultInterval,
				srv.log,
			)
		}()
		srv.log.Info("DNS-seed resolver enabled", "hosts", srv.DNSSeeds, "interval", DNSSeedDefaultInterval, "port", DNSSeedDefaultPort)
	}
	return nil
}

// warnOnLegacyUDPDominance logs at warn level if legacy_udp entries
// account for more than 50% of the addrbook. PIP-0006 Phase 5 flags
// this as a signal of poor v2.0 network share — the operator should
// investigate whether they've partitioned onto v1.x peers only.
func (srv *Server) warnOnLegacyUDPDominance() {
	if srv.addrbook == nil {
		return
	}
	total := srv.addrbook.Size(nil, nil)
	if total < 20 {
		// Too few entries for the ratio to be meaningful.
		return
	}
	counts := srv.addrbook.CountsBySource()
	legacy := counts[addrman.SourceLegacyUDP]
	if legacy*2 > total {
		srv.log.Warn("addrbook dominated by legacy_udp entries — v2.0 network share may be low",
			"legacy_udp", legacy, "total", total)
	}
}

func (srv *Server) setupLocalNode() error {
	// Generate the per-startup self-connect nonce. Drawn from
	// crypto/rand so adversaries can't predict it; uint64 space is
	// large enough that collision probability across the network is
	// negligible (birthday bound ~2^32 distinct nodes before any
	// pair shares a nonce, and even then only matters if those two
	// nodes happen to dial each other).
	if err := srv.initHelloNonce(); err != nil {
		return err
	}

	// Create the devp2p handshake.
	pubkey := crypto.FromECDSAPub(&srv.PrivateKey.PublicKey)
	srv.ourHandshake = &protoHandshake{Version: baseProtocolVersion, Name: srv.Name, ID: pubkey[1:]}
	for _, p := range srv.Protocols {
		srv.ourHandshake.Caps = append(srv.ourHandshake.Caps, p.cap())
	}
	sort.Sort(capsByNameAndVersion(srv.ourHandshake.Caps))

	// Create the local node.
	db, err := enode.OpenDB(srv.Config.NodeDatabase)
	if err != nil {
		return err
	}
	srv.nodedb = db
	srv.localnode = enode.NewLocalNode(db, srv.PrivateKey)
	srv.localnode.SetFallbackIP(net.IP{127, 0, 0, 1})
	// Advertise v2-transport acceptance in our ENR. Peers that
	// resolve our enode (via enrtree or discv4) will see this and
	// dial v2 directly instead of v1-then-promote.
	srv.localnode.Set(enrV2Transport{})
	// TODO: check conflicts
	for _, p := range srv.Protocols {
		for _, e := range p.Attributes {
			srv.localnode.Set(e)
		}
	}
	switch srv.NAT.(type) {
	case nil:
		srv.log.Debug("NAT disabled, no port mapping will be performed")
	case nat.ExtIP:
		// ExtIP doesn't block, set the IP right away.
		ip, _ := srv.NAT.ExternalIP()
		srv.log.Info("Using static external IP for NAT", "ip", ip)
		srv.localnode.SetStaticIP(ip)
	default:
		// Ask the router about the IP. This takes a while and blocks startup,
		// do it in the background.
		srv.log.Info("NAT enabled, discovering external IP in background", "mechanism", srv.NAT)
		srv.loopWG.Add(1)
		go func() {
			defer srv.loopWG.Done()
			if ip, err := srv.NAT.ExternalIP(); err == nil {
				srv.log.Info("NAT external IP discovered", "ip", ip)
				srv.localnode.SetStaticIP(ip)
			} else {
				srv.log.Warn("NAT external IP discovery failed", "err", err)
			}
		}()
	}
	return nil
}

func (srv *Server) setupDiscovery() error {
	srv.discmix = enode.NewFairMix(discmixTimeout)

	// Add protocol-specific discovery sources. Tee each into addrman
	// (when present) with source=dns_seed so the enrtree-delivered
	// enodes become addrman entries — otherwise addrman sees only
	// the static MainnetBootnodes ingest and has no view of the rest
	// of the network, which leaves stale KeyType=0x00 entries
	// dominating V2Iter with nothing to balance them out.
	added := make(map[string]bool)
	for _, proto := range srv.Protocols {
		if proto.DialCandidates != nil && !added[proto.Name] {
			src := proto.DialCandidates
			if srv.addrbook != nil {
				src = addrman.NewTeeIter(src, srv.addrbook, addrman.SourceDNSSeed)
			}
			srv.discmix.AddSource(src)
			added[proto.Name] = true
		}
	}

	// Don't listen on UDP endpoint if DHT is disabled.
	if srv.NoDiscovery {
		return nil
	}

	addr, err := net.ResolveUDPAddr("udp", srv.ListenAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	realaddr := conn.LocalAddr().(*net.UDPAddr)
	srv.log.Debug("UDP listener up", "addr", realaddr)
	if srv.NAT != nil {
		if !realaddr.IP.IsLoopback() {
			srv.log.Info("Setting up NAT port mapping for UDP discovery", "port", realaddr.Port)
			srv.loopWG.Add(1)
			go func() {
				nat.Map(srv.NAT, srv.quit, "udp", realaddr.Port, realaddr.Port, "parallax discovery", func(extport uint16) {
					// The router may have remapped our requested port to a
					// different external one (IGDv2 AddAnyPortMapping or our
					// random-port retry path). Update the local node so the
					// ENR advertises the actually-reachable UDP port.
					if int(extport) != realaddr.Port {
						srv.log.Info("NAT remapped UDP discovery port",
							"intport", realaddr.Port, "extport", extport)
					}
					srv.localnode.SetFallbackUDP(int(extport))
				})
				srv.loopWG.Done()
			}()
		} else {
			srv.log.Debug("Skipping NAT mapping for loopback UDP address", "addr", realaddr)
		}
	}
	srv.localnode.SetFallbackUDP(realaddr.Port)

	// Discovery V4
	// discv4 seeds its routing table with BootstrapNodes (NodeID-
	// carrying entries). BootstrapNodesV2 (ip:port only) are not
	// usable here — discv4 is NodeID-keyed — and reach the v2
	// handshake path through addrman ingest in setupAddrMan.
	cfg := discover.Config{
		PrivateKey:  srv.PrivateKey,
		NetRestrict: srv.NetRestrict,
		Bootnodes:   srv.BootstrapNodes,
		Log:         srv.log,
		NodeFilter:  srv.NodeFilter,
	}
	ntab, err := discover.ListenV4(conn, srv.localnode, cfg)
	if err != nil {
		return err
	}
	srv.ntab = ntab
	// PIP-0006 Phase 5: legacy discovery mode gates whether
	// discv4 drives the dial path. Modes:
	//   on    — discv4 is a full dial source (v1.x compat).
	//   auto  — discv4 responds but is NOT plumbed to the
	//           dialer; addrman is the source of truth.
	//   off   — this branch isn't reached because NoDiscovery
	//           is already true (see setLegacyDiscoveryDefaults).
	//
	// discv4's own periodic table refresh still runs in auto
	// mode so inbound PING/FINDNODE continues to work and the
	// routing table stays warm — we only skip using RandomNodes
	// as a dial candidate iterator.
	mode := srv.legacyDiscoveryMode()
	if mode == legacyDiscoveryOn {
		src := ntab.RandomNodes()
		if srv.addrbook != nil {
			// Tee discv4 discoveries into addrman with
			// source=legacy_udp. Original node passes
			// through to the dialer unchanged.
			src = addrman.NewTeeIter(src, srv.addrbook, addrman.SourceLegacyUDP)
		}
		srv.discmix.AddSource(src)
	}
	return nil
}

func (srv *Server) setupDialScheduler() {
	config := dialConfig{
		self:           srv.localnode.ID(),
		maxDialPeers:   srv.maxDialedConns(),
		maxActiveDials: srv.MaxPendingPeers,
		log:            srv.Logger,
		netRestrict:    srv.NetRestrict,
		dialer:         srv.Dialer,
		clock:          srv.clock,
		v2Predicate:    hasV2TransportENR,
		v2Dial:         srv.DialV2,
		maxBlockRelay:  srv.maxBlockRelayDial(),
	}
	if srv.ntab != nil {
		config.resolver = srv.ntab
	}
	if config.dialer == nil {
		config.dialer = tcpDialer{&net.Dialer{Timeout: defaultDialTimeout}}
	}
	// When addrman is enabled, add its NodeIter as an additional FairMix
	// source. discmix hands the dialer candidates round-robin across
	// sources, so this lets addrman feed dials without displacing
	// discv4 during the transition window.
	if srv.addrbook != nil {
		srv.addrbookIter = addrman.NewNodeIter(srv.addrbook, 250*time.Millisecond)
		srv.discmix.AddSource(srv.addrbookIter)
		// v2 dialer is always spawned — v2 handshake is not
		// operator-optional. V2Iter yields KeyType=0x00 entries;
		// when the addrbook has none (e.g., a freshly-installed
		// v1.x-only network), the goroutine idles on its internal
		// backoff.
		srv.v2Iter = addrman.NewV2Iter(srv.addrbook, 250*time.Millisecond, srv.IsSelfEndpoint)
		srv.loopWG.Add(1)
		go srv.runV2Dialer()

		// Background feeler. Periodically tests one addrman entry
		// for reachability without occupying an outbound slot —
		// Bitcoin Core's anti-eclipse / addrman-freshness feeler
		// (src/net.cpp:2796-2810). Refreshes LastSuccess on hits,
		// records failures otherwise.
		srv.loopWG.Add(1)
		go srv.runFeeler()

		// Cold-start addrfetch. One-shot bootstrap-only dial when
		// the addrman is below addrFetchThreshold so a fresh
		// install can populate addrbook from a few peers' GetPeers
		// responses (Bitcoin Core ConnectionType::ADDR_FETCH,
		// src/net.cpp:2422). No-op once the addrman has filled up.
		if len(srv.BootstrapNodesV2) > 0 {
			srv.loopWG.Add(1)
			go srv.runAddrFetch()
		}
	}
	srv.dialsched = newDialScheduler(config, srv.discmix, srv.SetupConn)
	for _, n := range srv.StaticNodes {
		srv.dialsched.addStatic(n)
	}
}

// legacyDiscoveryMode parses Config.LegacyDiscoveryMode into a
// stable enum. Unknown / empty values map to auto.
type legacyDiscoveryMode int

const (
	legacyDiscoveryAuto legacyDiscoveryMode = iota
	legacyDiscoveryOn
	legacyDiscoveryOff
)

func (srv *Server) legacyDiscoveryMode() legacyDiscoveryMode {
	switch srv.LegacyDiscoveryMode {
	case "on":
		return legacyDiscoveryOn
	case "off":
		return legacyDiscoveryOff
	case "", "auto":
		return legacyDiscoveryAuto
	}
	srv.log.Warn("unknown --legacy-discovery value; defaulting to auto", "value", srv.LegacyDiscoveryMode)
	return legacyDiscoveryAuto
}

// applyLegacyDiscoveryMode rewrites NoDiscovery for mode=off. Must be
// called before setupDiscovery. mode=auto/on leave NoDiscovery alone —
// auto still listens to answer inbound, it just doesn't drive the
// dial path (see setupDiscovery).
func (srv *Server) applyLegacyDiscoveryMode() {
	if srv.legacyDiscoveryMode() == legacyDiscoveryOff {
		srv.NoDiscovery = true
	}
}

// runV2Dialer drains v2-native addrman entries and launches a Server
// DialV2 for each. Rate-limited to one outstanding dial at a time so
// a large addrbook doesn't burst-dial the network.
func (srv *Server) runV2Dialer() {
	defer srv.loopWG.Done()

	// No local cooldown: DialV2 itself gates per-addr timing via
	// v2DialCooldownCheckAndMark. If the cooldown rejects a draw we
	// back off briefly to stop the iterator from looping hot.
	const cooldownRejectPause = 1 * time.Second

	var prev time.Time
	for srv.v2Iter.Next() {
		select {
		case <-srv.quit:
			return
		default:
		}
		cand := srv.v2Iter.Candidate()
		addrPort, ok := cand.Addr.AddrPort()
		if !ok {
			continue
		}
		tcp := &net.TCPAddr{IP: addrPort.Addr().AsSlice(), Port: int(addrPort.Port())}
		now := time.Now()
		var sincePrev time.Duration
		if !prev.IsZero() {
			sincePrev = now.Sub(prev)
		}
		srv.log.Trace("pip6: runV2Dialer iter", "addr", tcp.String(), "sincePrev", sincePrev)
		prev = now
		if err := srv.DialV2(tcp); err != nil {
			srv.log.Trace("v2 dial failed", "addr", tcp, "err", err)
			if errors.Is(err, errV2DialCooldown) {
				select {
				case <-srv.quit:
					return
				case <-time.After(cooldownRejectPause):
				}
			}
		}
	}
}

// DialV2 opens a v2-handshake TCP connection to the supplied address
// and hands the resulting peer to the normal run-loop checkpoints.
// Called by the v2 dial goroutine and by admin_dialV2 for operator
// testing.
//
// Skips the dial if any existing peer is already connected on the
// same (IP, TCP port). v2 sessions derive node.ID from ephemeral
// keys, so the Server's node.ID-keyed peer map can't dedupe on its
// own — short-circuit here before the TCP connection spends kernel
// resources on a duplicate handshake.
func (srv *Server) DialV2(addr *net.TCPAddr) error {
	return srv.dialV2WithFlags(addr, 0)
}

// DialV2BlockRelay opens a v2-handshake TCP connection like DialV2
// but tags the resulting conn with the block-relay-only flag. Used
// at startup to replay anchors.dat — anchor peers are persisted
// outbound block-relay-only peers, so they should reattach in the
// same role.
func (srv *Server) DialV2BlockRelay(addr *net.TCPAddr) error {
	return srv.dialV2WithFlags(addr, blockRelayConn)
}

// dialV2WithFlags is the shared implementation. extra is OR'd into
// the standard (dynDialedConn|v2DialedConn) flag set so the caller
// can request block-relay-only or other future variants without
// duplicating the cooldown / self-endpoint / dedup checks.
func (srv *Server) dialV2WithFlags(addr *net.TCPAddr, extra connFlag) error {
	if addr == nil {
		return errors.New("v2 dial: nil address")
	}
	if !srv.v2DialCooldownCheckAndMark(addr) {
		return fmt.Errorf("v2 dial %s: %w", addr, errV2DialCooldown)
	}
	if srv.IsSelfEndpoint(addr) {
		srv.log.Debug("v2 dial skipped (self endpoint)", "addr", addr.String())
		return fmt.Errorf("v2 dial %s: %w", addr, errV2DialSelf)
	}
	already := srv.alreadyConnectedTo(addr)
	peerCount := len(srv.Peers())
	srv.log.Trace("pip6: DialV2 enter", "addr", addr.String(), "alreadyConnected", already, "peers", peerCount, "extra", extra)
	if already {
		// Refresh LastTry without counting a failure. addrman's
		// Select chance weighting drops ~100x for 10 min once
		// LastTry is recent, so the iterator stops burning cycles
		// re-picking an endpoint we already peer with via v1.
		srv.addrmanAttemptByTCP(addr, false)
		return fmt.Errorf("v2 dial %s: already connected", addr)
	}
	fd, err := net.DialTimeout("tcp", addr.String(), defaultDialTimeout)
	if err != nil {
		srv.addrmanAttemptByTCP(addr, true)
		return fmt.Errorf("v2 dial %s: %w", addr, err)
	}
	// Flags: dynDialedConn so the run loop slots it correctly, plus
	// v2DialedConn so pickHandshakeVariant picks the v2 transport.
	// extra carries blockRelayConn for anchor replays.
	if err := srv.SetupConn(fd, dynDialedConn|v2DialedConn|extra, nil); err != nil {
		// v2 handshake / protocol negotiation failed before a Peer
		// object was constructed, so the delpeer path never runs
		// and addrman never learns the entry is unreachable. Record
		// it here so IsTerrible can eventually evict it.
		srv.addrmanAttemptByTCP(addr, true)
		return err
	}
	return nil
}

// addrmanAttemptByTCP records a dial attempt in addrman, keyed by
// (IP, port). countFailure=true bumps the failure counter so
// IsTerrible can eventually evict unreachable entries; pass false to
// update only LastTry (throttles re-selection without signalling
// unreachability — used when we short-circuit a dial because we're
// already peered with the endpoint via a different transport).
func (srv *Server) addrmanAttemptByTCP(addr *net.TCPAddr, countFailure bool) {
	if srv.addrbook == nil || addr == nil || addr.IP == nil {
		return
	}
	var netID addrman.NetID
	var bytes []byte
	if v4 := addr.IP.To4(); v4 != nil {
		netID, bytes = addrman.NetIPv4, v4
	} else {
		netID, bytes = addrman.NetIPv6, addr.IP.To16()
	}
	na, err := addrman.NewNetAddr(netID, bytes, uint16(addr.Port))
	if err != nil {
		return
	}
	srv.addrbook.Attempt(na, countFailure, time.Now())
}

// v2DialCooldown is the minimum interval between successive v2 dial
// attempts to the same (IP, port). Select's chanceFactor ramp
// guarantees addrman returns a candidate regardless of LastTry, so
// chance-based throttling isn't a rate-limit when the KeyType=0
// cohort is small; this is the authoritative gate.
const v2DialCooldown = 30 * time.Second

// errV2DialCooldown is the sentinel returned by DialV2 when the
// per-addr cooldown rejects an attempt. Callers check with
// errors.Is to back off instead of treating the rejection as a
// real failure.
var errV2DialCooldown = errors.New("v2 dial cooldown")

// errV2DialSelf is the sentinel returned by DialV2 when the dial
// target matches this node's own advertised (IP, TCP) endpoint.
// v2 sessions derive node.ID from ephemeral X25519 keys, so the
// node.ID-keyed self-check in postHandshakeChecks cannot fire for
// v2 peers — without this guard, a node behind a hairpinning NAT
// dials its own external IP and accepts the looped-back TCP
// connection as a peer.
var errV2DialSelf = errors.New("v2 dial self")

// v2DialCooldownCheckAndMark returns true and records addr's dial
// timestamp if the cooldown has elapsed; returns false otherwise.
// Serialized so concurrent callers (runV2Dialer, dial-scheduler v2
// branch, admin RPC) agree on the decision.
func (srv *Server) v2DialCooldownCheckAndMark(addr *net.TCPAddr) bool {
	key := addr.String()
	now := time.Now()
	srv.v2DialRecentMu.Lock()
	defer srv.v2DialRecentMu.Unlock()
	if srv.v2DialRecent == nil {
		srv.v2DialRecent = make(map[string]time.Time)
	}
	if last, ok := srv.v2DialRecent[key]; ok && now.Sub(last) < v2DialCooldown {
		return false
	}
	srv.v2DialRecent[key] = now
	// Opportunistic purge — cheap because the map is small.
	for k, t := range srv.v2DialRecent {
		if now.Sub(t) >= v2DialCooldown {
			delete(srv.v2DialRecent, k)
		}
	}
	return true
}

// IsSelfEndpoint reports whether addr matches this node's own
// advertised (IP, TCP) endpoint. Exported so external wiring (the
// disc-protocol AddrmanBackend, the addrman V2Iter) can consult
// the same source of truth as the dial / handshake guards. Used
// to short-circuit v2 dials that would hairpin through the NAT
// and arrive back at our own listener, and as a defense-in-depth
// check in postHandshakeChecks for v2 connections that bypass the
// dial guard.
//
// A dial counts as self when ANY of these hold:
//
//   - the address (IP, port) matches localnode's advertised TCP
//     endpoint (the ordinary case once setupListening publishes
//     the port);
//   - the address is loopback (127.0.0.0/8, ::1) and the port
//     matches our listen port (a localhost dial to our own
//     listener);
//   - the address is loopback and our listen port is not yet
//     published — during the brief Start()-window before
//     setupListening assigns the TCP port to localnode, any
//     loopback dial is treated as self because no legitimate
//     caller (admin RPC, anchor replay, feeler, runV2Dialer)
//     ever wants to dial loopback at this stage.
//
// The loopback-only fallback in the port-0 branch closes the
// startup-window gap without false-positiving real external
// peers: a dial to a remote host's IP cannot match loopback.
func (srv *Server) IsSelfEndpoint(addr *net.TCPAddr) bool {
	if addr == nil || srv.localnode == nil {
		return false
	}
	n := srv.localnode.Node()
	selfPort := n.TCP()
	// Pre-listening window: only loopback can be self with certainty.
	// A loopback dial is never legitimate before the listener exists.
	if selfPort == 0 {
		return addr.IP.IsLoopback()
	}
	if addr.Port != selfPort {
		return false
	}
	// Same port: self if the IP matches the advertised one OR is
	// loopback (127.0.0.1 -> own listener regardless of advertised IP).
	return addr.IP.Equal(n.IP()) || addr.IP.IsLoopback()
}

// peerListenAddr returns the peer's effective (IP, listen-port) for
// dedup. For outbound peers, RemoteAddr is the dial target — its port
// IS the peer's listen port. For inbound peers, RemoteAddr's port is
// the ephemeral source they connected from; substitute the listen
// port the peer disclosed via parallax-disc/1 Hello when available.
// Returns ok=false in two cases:
//   - non-TCP RemoteAddr (test pipes, tunneled transports);
//   - inbound peer with no disclosed listen port yet (pre-Hello window
//     or v1 peer that doesn't speak the extension).
//
// Callers using this for dedup must treat ok=false as "no signal" —
// it's not safe to assume the peer's listen address.
func (srv *Server) peerListenAddr(p *Peer) (*net.TCPAddr, bool) {
	pra, ok := p.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil, false
	}
	if !p.rw.is(inboundConn) {
		return pra, true
	}
	if srv.peerListenLookup == nil {
		return nil, false
	}
	port, ok := srv.peerListenLookup.PeerListenPort(p.ID())
	if !ok || port == 0 {
		return nil, false
	}
	return &net.TCPAddr{IP: pra.IP, Port: int(port)}, true
}

// PeerListenAddr is the exported wrapper around peerListenAddr.
// Used by the parallax-disc/1 AddrmanBackend's cross-dial dedup
// hook so the disc package doesn't need to duplicate the
// outbound/inbound branching logic.
func (srv *Server) PeerListenAddr(p *Peer) (*net.TCPAddr, bool) {
	return srv.peerListenAddr(p)
}

// alreadyConnectedTo reports whether any current peer's effective
// listen-addr matches addr's (IP, port). Used by DialV2 to dedupe v2
// dial targets that can't be caught by node.ID-keyed matching.
//
// Inbound peers contribute to this check only after disclosing their
// listen port via Hello; in the brief window between TCP-up and Hello
// receipt, an outbound dial to the same logical peer can race through.
// The post-Hello cross-dial dedup hook in AddrmanBackend.HandleHello
// resolves the resulting duplicate by dropping the inbound side.
func (srv *Server) alreadyConnectedTo(addr *net.TCPAddr) bool {
	for _, p := range srv.Peers() {
		la, ok := srv.peerListenAddr(p)
		if !ok {
			continue
		}
		if la.Port == addr.Port && la.IP.Equal(addr.IP) {
			return true
		}
	}
	return false
}

// FindCrossDialDup looks for an existing peer (other than newPeer)
// whose effective listen-addr matches (newPeer.IP, listenPort).
// Returns nil if none. Called by AddrmanBackend.HandleHello after
// a peer discloses its listen port: if a duplicate exists, one of
// the two connections must be torn down to avoid double-counting
// the logical neighbor. Tie-break logic lives at the call site.
func (srv *Server) FindCrossDialDup(newPeer *Peer, listenPort uint16) *Peer {
	return srv.findCrossDialDupIn(srv.Peers(), newPeer, listenPort)
}

// findCrossDialDupIn is the testable core: same logic but operates
// over a caller-supplied peer slice instead of pulling the current
// set through the run loop. Tests synthesize fake peers and drive
// it directly without standing up Start().
func (srv *Server) findCrossDialDupIn(peers []*Peer, newPeer *Peer, listenPort uint16) *Peer {
	if newPeer == nil || listenPort == 0 {
		return nil
	}
	pra, ok := newPeer.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil
	}
	target := &net.TCPAddr{IP: pra.IP, Port: int(listenPort)}
	for _, p := range peers {
		if p == newPeer {
			continue
		}
		la, ok := srv.peerListenAddr(p)
		if !ok {
			continue
		}
		if la.Port == target.Port && la.IP.Equal(target.IP) {
			return p
		}
	}
	return nil
}

// logStartup emits the node's starting address as plain ip:port
// rather than a full enode URL. The URL form hard-couples to the v1.x
// identity model, which misleads operators running in v2-only mode
// (where the persistent secp256k1 key doesn't participate in any
// handshake). For v1.x-compatible modes the enode URL is still worth
// having — we emit it at debug level as a secondary line.
func (srv *Server) logStartup() {
	n := srv.localnode.Node()
	srv.log.Info("Started P2P networking",
		"address", formatAddr(n.IP(), n.TCP()),
		"mode", srv.startupModeString())
	if srv.legacyHandshakeMode() != legacyHandshakeOff {
		srv.log.Debug("Legacy enode URL", "self", n.URLv4())
	}
}

// formatAddr renders an ip/port pair as "ip:port", bracketing IPv6
// to keep the colon separator unambiguous.
func formatAddr(ip net.IP, port int) string {
	return (&net.TCPAddr{IP: ip, Port: port}).String()
}

// startupModeString describes the handshake/discovery posture for the
// startup banner.
func (srv *Server) startupModeString() string {
	switch srv.legacyDiscoveryMode() {
	case legacyDiscoveryOff:
		return "v2-only"
	case legacyDiscoveryOn:
		return "legacy+v2 (discv4-full)"
	}
	return "legacy+v2 (discv4-responder)"
}

// watchLocalAddrChanges polls the LocalNode's advertised IP/port and
// logs a follow-up line when they change — typically when NAT/UPnP
// resolves the public IP or the ENR is refreshed by a peer observation.
// The poll cadence matches the LocalNode's internal refresh rate
// closely enough; precise hooks would require a subscription API on
// LocalNode that doesn't exist yet.
func (srv *Server) watchLocalAddrChanges() {
	prevIP := srv.localnode.Node().IP()
	prevPort := srv.localnode.Node().TCP()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-srv.quit:
			return
		case <-tick.C:
			n := srv.localnode.Node()
			ip, port := n.IP(), n.TCP()
			if !ip.Equal(prevIP) || port != prevPort {
				srv.log.Info("P2P external address updated", "address", formatAddr(ip, port))
				prevIP, prevPort = ip, port
			}
		}
	}
}

// LegacyHandshakeRefused reports whether this Server is in v2-only
// mode (--legacy-discovery=off). Exposed for RPC handlers that want
// to branch on the transport posture — e.g., admin_addPeer rejects
// enode:// targets in this mode because the legacy handshake is
// refused both inbound and outbound.
func (srv *Server) LegacyHandshakeRefused() bool {
	return srv.legacyHandshakeMode() == legacyHandshakeOff
}

// DisconnectByAddr finds the first connected peer whose RemoteAddr
// matches the given TCP address and disconnects it. Returns true if
// a matching peer was found. Used by admin_removePeer in v2 mode
// because v2 peer identities are session-ephemeral and can't be used
// as stable lookup keys.
func (srv *Server) DisconnectByAddr(addr *net.TCPAddr) bool {
	if addr == nil {
		return false
	}
	for _, p := range srv.Peers() {
		ra, ok := p.RemoteAddr().(*net.TCPAddr)
		if !ok {
			continue
		}
		if ra.Port == addr.Port && ra.IP.Equal(addr.IP) {
			p.Disconnect(DiscRequested)
			return true
		}
	}
	return false
}

// AddrBook returns the server's address manager, or nil when
// ExperimentalAddrMan is not enabled. Upstream packages register the
// parallax-disc/1 subprotocol against this book — doing the
// registration here would create an import cycle with
// p2p/protocols/disc.
func (srv *Server) AddrBook() *addrman.AddrMan { return srv.addrbook }

// addrmanGood marks the peer's address as verified in the addrman.
// No-op when ExperimentalAddrMan is off. Called from the run-loop
// right after a peer joins the peers map.
//
// Note on KeyType: we deliberately do NOT upgrade an existing entry's
// KeyType on success. A remote that accepts a v1 RLPx handshake may
// also accept v2 BIP324 on the same endpoint — v1-success does not
// imply v2-failure. Changing 0x00 → 0x01 here would hide a legitimate
// dual-stack peer from V2Iter. Short-circuit / dial noise on v2-only
// entries that only speak v1 is instead handled via:
//   - Attempt(countFailure=true) on real dial failures,
//   - Attempt(countFailure=false) on alreadyConnectedTo short-circuit
//     (refreshes LastTry, decays Select chance to ~1% for 10 min),
//   - V2Iter skip on IsTerrible entries (closes the stale-entry loop).
func (srv *Server) addrmanGood(p *Peer) {
	if srv.addrbook == nil {
		return
	}
	addr, ok := peerAdvertisedAddr(p)
	if !ok {
		return
	}
	srv.log.Trace("pip6: addrmanGood",
		"addr", addr.String(),
		"isV2", p.UsingV2Handshake(),
		"id", p.ID())
	srv.addrbook.Good(addr, time.Now())
}

// peerAdvertisedAddr returns the addrman.NetAddr form of a Peer's
// advertised listening endpoint — (IP, TCP) from p.Node() rather than
// the TCP socket's RemoteAddr. Inbound v1 peers' RemoteAddr carries
// the ephemeral source port, which doesn't match the addrman entry
// keyed by the peer's listening port; p.Node() is rebuilt in
// SetupConn for inbound v1 to hold the handshake-advertised port.
// Outbound v1 peers have c.node = dialDest, which already carries
// the correct listening port.
func peerAdvertisedAddr(p *Peer) (addrman.NetAddr, bool) {
	n := p.Node()
	if n == nil {
		return addrman.NetAddr{}, false
	}
	ip := n.IP()
	port := n.TCP()
	if ip == nil || port == 0 {
		return peerRemoteAddr(p)
	}
	if v4 := ip.To4(); v4 != nil {
		a, err := addrman.NewNetAddr(addrman.NetIPv4, v4, uint16(port))
		if err != nil {
			return addrman.NetAddr{}, false
		}
		return a, true
	}
	if v6 := ip.To16(); v6 != nil {
		a, err := addrman.NewNetAddr(addrman.NetIPv6, v6, uint16(port))
		if err != nil {
			return addrman.NetAddr{}, false
		}
		return a, true
	}
	return addrman.NetAddr{}, false
}

// replayAnchors reads the persisted block-relay-only anchor list
// (anchors.dat) and re-dials each entry as block-relay-only. The
// file is deleted immediately after a successful read so a crash
// during startup doesn't replay the same anchors twice (matches
// Bitcoin Core src/net.cpp:2715-2716 post-read delete).
//
// Each replay dial runs in its own goroutine so a slow DNS / dial
// doesn't block server startup. Failures are logged but never
// propagated — anchors are best-effort hints.
func (srv *Server) replayAnchors() {
	if srv.AnchorsPath == "" || srv.NoDial {
		return
	}
	addrs, err := loadAnchors(srv.AnchorsPath)
	if err != nil {
		srv.log.Warn("anchors: load failed; skipping replay", "path", srv.AnchorsPath, "err", err)
		return
	}
	// Delete the file unconditionally, even on a clean read, so a
	// crash mid-startup doesn't double-dial. Bitcoin parity.
	if err := removeAnchors(srv.AnchorsPath); err != nil {
		srv.log.Warn("anchors: remove after load failed", "path", srv.AnchorsPath, "err", err)
	}
	if len(addrs) == 0 {
		return
	}
	srv.log.Info("anchors: replaying block-relay-only peers", "count", len(addrs))
	for _, a := range addrs {
		srv.loopWG.Add(1)
		go func() {
			defer srv.loopWG.Done()
			if err := srv.DialV2BlockRelay(a); err != nil {
				srv.log.Trace("anchor dial failed", "addr", a, "err", err)
			}
		}()
	}
}

// persistAnchors snapshots the (IP, listen-port) of currently-
// connected block-relay-only outbound peers (capped at
// MaxBlockRelayAnchors) to anchors.dat. Called from Stop after
// loopWG so the peer set is settled. Best-effort: errors are
// logged, never propagated.
func (srv *Server) persistAnchors() {
	if srv.AnchorsPath == "" {
		return
	}
	addrs := make([]*net.TCPAddr, 0, MaxBlockRelayAnchors)
	for _, p := range srv.Peers() {
		if !p.BlockRelayOnly() {
			continue
		}
		la, ok := srv.peerListenAddr(p)
		if !ok {
			continue
		}
		addrs = append(addrs, la)
		if len(addrs) >= MaxBlockRelayAnchors {
			break
		}
	}
	if err := saveAnchors(srv.AnchorsPath, addrs); err != nil {
		srv.log.Warn("anchors: save failed on shutdown", "path", srv.AnchorsPath, "err", err)
		return
	}
	srv.log.Info("anchors: saved", "path", srv.AnchorsPath, "count", len(addrs))
}

// addrmanAttempt records a failed connection attempt in the addrman.
// No-op when ExperimentalAddrMan is off.
func (srv *Server) addrmanAttempt(p *Peer) {
	if srv.addrbook == nil {
		return
	}
	addr, ok := peerRemoteAddr(p)
	if !ok {
		return
	}
	srv.addrbook.Attempt(addr, true, time.Now())
}

// peerRemoteAddr extracts the addrman.NetAddr form of a Peer's
// RemoteAddr. Returns ok=false for non-TCP or unresolvable connections
// (test pipes, Unix sockets, etc.).
func peerRemoteAddr(p *Peer) (addrman.NetAddr, bool) {
	ra := p.RemoteAddr()
	if ra == nil {
		return addrman.NetAddr{}, false
	}
	tcp, ok := ra.(*net.TCPAddr)
	if !ok {
		return addrman.NetAddr{}, false
	}
	if v4 := tcp.IP.To4(); v4 != nil {
		a, err := addrman.NewNetAddr(addrman.NetIPv4, v4, uint16(tcp.Port))
		if err != nil {
			return addrman.NetAddr{}, false
		}
		return a, true
	}
	if v6 := tcp.IP.To16(); v6 != nil {
		a, err := addrman.NewNetAddr(addrman.NetIPv6, v6, uint16(tcp.Port))
		if err != nil {
			return addrman.NetAddr{}, false
		}
		return a, true
	}
	return addrman.NetAddr{}, false
}

func (srv *Server) maxInboundConns() int {
	return srv.MaxPeers - srv.maxDialedConns()
}

// minLegacyPeers returns the configured floor of non-tcp_gossip
// peer slots, with the v2.x default applied when MinLegacyPeers is
// zero. A negative value disables the floor.
func (srv *Server) minLegacyPeers() int {
	if srv.MinLegacyPeers < 0 {
		return 0
	}
	if srv.MinLegacyPeers == 0 {
		return defaultMinLegacyPeers
	}
	return srv.MinLegacyPeers
}

// connSourceTag returns the addrman source tag for c's remote
// endpoint, or 0 if no addrman entry exists or the addrbook is
// disabled. Looks up by RemoteAddr; for v1 inbound peers this is the
// ephemeral source port and won't match the addrman entry keyed by
// listen port — those connections fall through unclassified, which
// is the conservative choice (the floor only fires on classifiable
// tcp_gossip peers).
func (srv *Server) connSourceTag(c *conn) addrman.Source {
	if srv.addrbook == nil {
		return 0
	}
	tcp, ok := c.fd.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	var na addrman.NetAddr
	var err error
	if v4 := tcp.IP.To4(); v4 != nil {
		na, err = addrman.NewNetAddr(addrman.NetIPv4, v4, uint16(tcp.Port))
	} else if v6 := tcp.IP.To16(); v6 != nil {
		na, err = addrman.NewNetAddr(addrman.NetIPv6, v6, uint16(tcp.Port))
	} else {
		return 0
	}
	if err != nil {
		return 0
	}
	info := srv.addrbook.Lookup(na)
	if info == nil {
		return 0
	}
	return info.SourceTag
}

// hasNonTCPGossipAlternatives reports whether the addrbook holds
// any entry from a source other than tcp_gossip. Used to short-
// circuit the legacy-floor reject when no non-tcp_gossip candidate
// could ever fill the reserved slots.
func (srv *Server) hasNonTCPGossipAlternatives() bool {
	if srv.addrbook == nil {
		return false
	}
	for src, n := range srv.addrbook.CountsBySource() {
		if src != addrman.SourceTCPGossip && n > 0 {
			return true
		}
	}
	return false
}

func (srv *Server) maxDialedConns() (limit int) {
	if srv.NoDial || srv.MaxPeers == 0 {
		return 0
	}
	if srv.DialRatio == 0 {
		limit = srv.MaxPeers / defaultDialRatio
	} else {
		limit = srv.MaxPeers / srv.DialRatio
	}
	if limit == 0 {
		limit = 1
	}
	return limit
}

// maxBlockRelayDial returns the count of outbound slots reserved
// for block-relay-only peers. Capped at maxDialedConns()/2 so a
// pathological config can't push every outbound dial into the
// block-relay bucket and starve full-relay traffic.
func (srv *Server) maxBlockRelayDial() int {
	if srv.NoDial || srv.MaxPeers == 0 {
		return 0
	}
	want := srv.MaxBlockRelayPeers
	if want == 0 {
		want = defaultMaxBlockRelayPeers
	}
	if want < 0 {
		return 0
	}
	if cap := srv.maxDialedConns() / 2; want > cap {
		want = cap
	}
	return want
}

func (srv *Server) setupListening() error {
	// Launch the listener.
	listener, err := srv.listenFunc("tcp", srv.ListenAddr)
	if err != nil {
		return err
	}
	srv.listener = listener
	srv.ListenAddr = listener.Addr().String()

	// Update the local node record and map the TCP listening port if NAT is configured.
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok {
		srv.localnode.Set(enr.TCP(tcp.Port))
		if !tcp.IP.IsLoopback() && srv.NAT != nil {
			srv.log.Info("Setting up NAT port mapping for TCP p2p", "port", tcp.Port)
			srv.loopWG.Add(1)
			go func() {
				nat.Map(srv.NAT, srv.quit, "tcp", tcp.Port, tcp.Port, "parallax p2p", func(extport uint16) {
					// The router may have remapped our requested port to a
					// different external one. Update the ENR's TCP entry so
					// remote peers dial the reachable port.
					if int(extport) != tcp.Port {
						srv.log.Info("NAT remapped TCP p2p port",
							"intport", tcp.Port, "extport", extport)
					}
					srv.localnode.Set(enr.TCP(int(extport)))
				})
				srv.loopWG.Done()
			}()
		} else if srv.NAT == nil {
			srv.log.Debug("NAT not configured, skipping TCP port mapping")
		}
	}

	srv.loopWG.Add(1)
	go srv.listenLoop()
	return nil
}

// doPeerOp runs fn on the main loop.
func (srv *Server) doPeerOp(fn peerOpFunc) {
	select {
	case srv.peerOp <- fn:
		<-srv.peerOpDone
	case <-srv.quit:
	}
}

// run is the main loop of the server.
func (srv *Server) run() {
	srv.logStartup()
	go srv.watchLocalAddrChanges()
	defer srv.loopWG.Done()
	defer srv.nodedb.Close()
	defer srv.discmix.Close()
	defer srv.dialsched.stop()

	var (
		peers        = make(map[enode.ID]*Peer)
		inboundCount = 0
		// tcpGossipPeers counts currently-connected peers whose
		// addrman entry was tagged source=tcp_gossip when first
		// learned. Drives the MinLegacyPeers floor in
		// postHandshakeChecks so we don't end up with all peers on
		// the new code path during early v2.x. PIP-0006 §Phase 5.
		tcpGossipPeers = 0
		trusted        = make(map[enode.ID]bool, len(srv.TrustedNodes))
	)
	// Put trusted nodes into a map to speed up checks.
	// Trusted peers are loaded on startup or added via AddTrustedPeer RPC.
	for _, n := range srv.TrustedNodes {
		trusted[n.ID()] = true
	}

running:
	for {
		select {
		case <-srv.quit:
			// The server was stopped. Run the cleanup logic.
			break running

		case n := <-srv.addtrusted:
			// This channel is used by AddTrustedPeer to add a node
			// to the trusted node set.
			srv.log.Trace("Adding trusted node", "node", n)
			trusted[n.ID()] = true
			if p, ok := peers[n.ID()]; ok {
				p.rw.set(trustedConn, true)
			}

		case n := <-srv.removetrusted:
			// This channel is used by RemoveTrustedPeer to remove a node
			// from the trusted node set.
			srv.log.Trace("Removing trusted node", "node", n)
			delete(trusted, n.ID())
			if p, ok := peers[n.ID()]; ok {
				p.rw.set(trustedConn, false)
			}

		case op := <-srv.peerOp:
			// This channel is used by Peers and PeerCount.
			op(peers)
			srv.peerOpDone <- struct{}{}

		case c := <-srv.checkpointPostHandshake:
			// A connection has passed the encryption handshake so
			// the remote identity is known (but hasn't been verified yet).
			if trusted[c.node.ID()] {
				// Ensure that the trusted flag is set before checking against MaxPeers.
				c.flags |= trustedConn
			}
			// TODO: track in-progress inbound node IDs (pre-Peer) to avoid dialing them.
			c.cont <- srv.postHandshakeChecks(peers, inboundCount, tcpGossipPeers, c)

		case c := <-srv.checkpointAddPeer:
			// At this point the connection is past the protocol handshake.
			// Its capabilities are known and the remote identity is verified.
			err := srv.addPeerChecks(peers, inboundCount, tcpGossipPeers, c)
			if err == nil {
				// The handshakes are done and it passed all checks.
				p := srv.launchPeer(c)
				peers[c.node.ID()] = p
				srv.log.Debug("Adding p2p peer", "peercount", len(peers), "id", p.ID(), "conn", c.flags, "addr", p.RemoteAddr(), "name", p.Name())
				srv.dialsched.peerAdded(c)
				if p.Inbound() {
					inboundCount++
				}
				if srv.connSourceTag(c) == addrman.SourceTCPGossip {
					tcpGossipPeers++
					p.tcpGossipSourced = true
				}
				// addrman.Good: mark this peer's address as verified.
				// Callers to addrman learn from our successes this
				// way — matches CAddrMan::Good in src/addrman.cpp.
				srv.addrmanGood(p)
			}
			c.cont <- err

		case pd := <-srv.delpeer:
			// A peer disconnected.
			d := util.PrettyDuration(mclock.Now() - pd.created)
			delete(peers, pd.ID())
			srv.log.Debug("Removing p2p peer", "peercount", len(peers), "id", pd.ID(), "duration", d, "req", pd.requested, "err", pd.err)
			srv.dialsched.peerRemoved(pd.rw)
			if pd.Inbound() {
				inboundCount--
			}
			if pd.tcpGossipSourced && tcpGossipPeers > 0 {
				tcpGossipPeers--
			}
			// addrman.Attempt: log the dial failure so IsTerrible
			// eventually evicts unreachable entries. Only count
			// failures on outbound-dial sessions — inbound
			// disconnects tell us nothing about our view of the
			// peer's reachability.
			if pd.err != nil && !pd.Inbound() {
				srv.addrmanAttempt(pd.Peer)
			}
			// Discourage hook: if the peer was flagged for
			// misbehavior during the session, add its source IP to
			// the ephemeral discourage filter. The filter is
			// consulted by postHandshakeChecks to reject the same
			// source if it tries to reconnect into a saturated
			// inbound pool. Bitcoin Core src/net_processing.cpp
			// MaybeDiscourageAndDisconnect.
			if srv.BanList != nil && pd.ShouldDiscourage() {
				if remote, ok := pd.RemoteAddr().(*net.TCPAddr); ok {
					srv.BanList.Discourage(remote.IP)
					srv.log.Debug("discouraging misbehaving peer",
						"id", pd.ID(),
						"addr", remote.IP,
						"reason", pd.DiscourageReason())
				}
			}
		}
	}

	srv.log.Trace("P2P networking is spinning down")

	// Terminate discovery. If there is a running lookup it will terminate soon.
	if srv.ntab != nil {
		srv.ntab.Close()
	}
	// Disconnect all peers.
	for _, p := range peers {
		p.Disconnect(DiscQuitting)
	}
	// Wait for peers to shut down. Pending connections and tasks are
	// not handled here and will terminate soon-ish because srv.quit
	// is closed.
	for len(peers) > 0 {
		p := <-srv.delpeer
		p.log.Trace("<-delpeer (spindown)")
		delete(peers, p.ID())
	}
}

func (srv *Server) postHandshakeChecks(peers map[enode.ID]*Peer, inboundCount, tcpGossipPeers int, c *conn) error {
	// Saturation handling for inbound: instead of hard-rejecting,
	// run the Bitcoin-Core-style eviction algorithm to free a slot
	// by dropping the lowest-quality existing inbound peer. If
	// eviction succeeds, accept the new peer optimistically — the
	// run loop processes the loser's delpeer asynchronously, so
	// inboundCount transiently exceeds the cap.
	//
	// MaxPeers (total) saturation still hard-rejects: total cap is
	// a system-resource ceiling, not subject to eviction. Inbound-
	// only is what the algorithm targets.
	// Discouraged-at-saturation rejection (Bitcoin Core
	// src/net.cpp:1808). At inbound saturation we'd normally evict
	// to make room. If the candidate's IP is in our discourage
	// filter (an in-memory record of recent misbehavior),
	// hard-reject before evicting — there's no point displacing a
	// well-behaved peer to admit one we already know is misbehaving.
	// Trusted peers bypass.
	if srv.BanList != nil && !c.is(trustedConn) && c.is(inboundConn) && inboundCount >= srv.maxInboundConns() {
		if remote, ok := c.fd.RemoteAddr().(*net.TCPAddr); ok && srv.BanList.IsDiscouraged(remote.IP) {
			return DiscTooManyPeers
		}
	}
	// MinLegacyPeers floor (PIP-0006 §Phase 5): hard-cap tcp_gossip
	// peers at MaxPeers - MinLegacyPeers so a v2.0-specific bug
	// can't take down 100% of our peers. Applies on both directions
	// once the peer's source is classifiable. Trusted peers bypass.
	// Skipped when the addrbook holds no non-tcp_gossip alternatives —
	// no point reserving slots that nothing can fill.
	if floor := srv.minLegacyPeers(); floor > 0 && !c.is(trustedConn) {
		cap := srv.MaxPeers - floor
		if cap < 0 {
			cap = 0
		}
		if tcpGossipPeers >= cap && srv.connSourceTag(c) == addrman.SourceTCPGossip && srv.hasNonTCPGossipAlternatives() {
			return DiscTooManyPeers
		}
	}
	switch {
	case !c.is(trustedConn) && len(peers) >= srv.MaxPeers:
		return DiscTooManyPeers
	case !c.is(trustedConn) && c.is(inboundConn) && inboundCount >= srv.maxInboundConns():
		if !srv.evictInbound(peers) {
			return DiscTooManyPeers
		}
	case peers[c.node.ID()] != nil:
		return DiscAlreadyConnected
	case c.node.ID() == srv.localnode.ID():
		return DiscSelf
	}
	// Phase 2b dedup: v2 sessions derive node.ID from ephemeral
	// X25519 keys, so reconnecting to the same remote yields a
	// fresh-looking ID that the map above can't flag. Fall back to
	// (IP, TCP port) matching — if a peer on the same address is
	// already connected, treat this as a duplicate.
	//
	// Also catch v2 self-connections that the node.ID check above
	// can't see: the kernel-level remote of an outbound dial is
	// the dial target, so if it matches our own advertised endpoint
	// the connection is a hairpin. DialV2 already rejects this at
	// the dial site; the check here covers the narrow window where
	// localnode's IP updates between the dial and this checkpoint,
	// and any future code path that bypasses DialV2.
	if _, isV2 := c.transport.(*v2Transport); isV2 {
		if remote, ok := c.fd.RemoteAddr().(*net.TCPAddr); ok {
			if srv.IsSelfEndpoint(remote) {
				return DiscSelf
			}
			for _, p := range peers {
				pra, ok := p.RemoteAddr().(*net.TCPAddr)
				if !ok {
					continue
				}
				if pra.Port == remote.Port && pra.IP.Equal(remote.IP) {
					return DiscAlreadyConnected
				}
			}
		}
	}
	return nil
}

func (srv *Server) addPeerChecks(peers map[enode.ID]*Peer, inboundCount, tcpGossipPeers int, c *conn) error {
	// Drop connections with no matching protocols.
	if len(srv.Protocols) > 0 && countMatchingProtocols(srv.Protocols, c.caps) == 0 {
		return DiscUselessPeer
	}
	// Repeat the post-handshake checks because the
	// peer set might have changed since those checks were performed.
	return srv.postHandshakeChecks(peers, inboundCount, tcpGossipPeers, c)
}

// listenLoop runs in its own goroutine and accepts
// inbound connections.
func (srv *Server) listenLoop() {
	srv.log.Debug("TCP listener up", "addr", srv.listener.Addr())

	// The slots channel limits accepts of new connections.
	tokens := defaultMaxPendingPeers
	if srv.MaxPendingPeers > 0 {
		tokens = srv.MaxPendingPeers
	}
	slots := make(chan struct{}, tokens)
	for i := 0; i < tokens; i++ {
		slots <- struct{}{}
	}

	// Wait for slots to be returned on exit. This ensures all connection goroutines
	// are down before listenLoop returns.
	defer srv.loopWG.Done()
	defer func() {
		for i := 0; i < cap(slots); i++ {
			<-slots
		}
	}()

	for {
		// Wait for a free slot before accepting.
		<-slots

		var (
			fd      net.Conn
			err     error
			lastLog time.Time
		)
		for {
			fd, err = srv.listener.Accept()
			if netutil.IsTemporaryError(err) {
				if time.Since(lastLog) > 1*time.Second {
					srv.log.Debug("Temporary read error", "err", err)
					lastLog = time.Now()
				}
				time.Sleep(time.Millisecond * 200)
				continue
			} else if err != nil {
				srv.log.Debug("Read error", "err", err)
				slots <- struct{}{}
				return
			}
			break
		}

		remoteIP := netutil.AddrIP(fd.RemoteAddr())
		if err := srv.checkInboundConn(remoteIP); err != nil {
			srv.log.Debug("Rejected inbound connection", "addr", fd.RemoteAddr(), "err", err)
			fd.Close()
			slots <- struct{}{}
			continue
		}
		if remoteIP != nil {
			var addr *net.TCPAddr
			if tcp, ok := fd.RemoteAddr().(*net.TCPAddr); ok {
				addr = tcp
			}
			fd = newMeteredConn(fd, true, addr)
			srv.log.Trace("Accepted connection", "addr", fd.RemoteAddr())
		}
		go func() {
			// PIP-0006 Phase 2b: peek the first byte to classify the
			// inbound connection as legacy ECIES or v2 AEAD. The
			// peekedConn wrapper replays the byte for the legacy
			// path and consumes it for the v2 path. peeked-variant
			// is read by pickHandshakeVariant in SetupConn.
			wrapped := srv.dispatchInbound(fd)
			if wrapped != nil {
				srv.SetupConn(wrapped, inboundConn, nil)
			}
			slots <- struct{}{}
		}()
	}
}

// dispatchInbound inspects the first byte of fd to classify the
// handshake variant. Returns nil after closing fd if the peek failed
// or the byte doesn't match a supported variant under the current
// configuration.
//
// Rules (v2 handshake is always available in this build):
//   - Peek the first byte.
//   - 0xA0 → v2 handshake (magic consumed by the peek).
//   - Legacy RLPx magic → accepted only when legacyHandshakeMode == on.
//   - Anything else → reject.
func (srv *Server) dispatchInbound(fd net.Conn) net.Conn {
	// Give the peek a short deadline so a silent client doesn't
	// burn a goroutine forever.
	_ = fd.SetReadDeadline(time.Now().Add(handshakeTimeout))
	variant, peeked, err := bip324handshake.PeekVersion(fd)
	// Reset read deadline — individual handshakes manage their own.
	_ = fd.SetReadDeadline(time.Time{})
	if err != nil {
		srv.log.Trace("Peek failed on inbound connection", "addr", fd.RemoteAddr(), "err", err)
		fd.Close()
		return nil
	}
	wrapped := &peekedConn{Conn: fd, peeked: peeked}
	switch variant {
	case bip324handshake.VariantV2:
		wrapped.variant = peekedVariantV2
		return wrapped
	case bip324handshake.VariantLegacy:
		if srv.legacyHandshakeMode() == legacyHandshakeOff {
			srv.log.Trace("Rejecting legacy inbound (LegacyHandshakeMode=off)", "addr", fd.RemoteAddr())
			fd.Close()
			return nil
		}
		wrapped.variant = peekedVariantLegacy
		return wrapped
	default:
		srv.log.Trace("Rejecting unknown-handshake inbound", "addr", fd.RemoteAddr())
		fd.Close()
		return nil
	}
}

// peekedConn wraps a net.Conn that's had its first byte(s) peeked by
// bip324handshake.PeekVersion. The v2 path has already consumed the
// magic byte; the legacy path replays it.
type peekedConn struct {
	net.Conn
	peeked  *bip324handshake.PeekedConn
	variant peekedVariant
}

// Read delegates to the PeekedConn's Read, which handles replay
// transparently for the legacy path.
func (p *peekedConn) Read(b []byte) (int, error) { return p.peeked.Read(b) }

type peekedVariant int

const (
	peekedVariantLegacy peekedVariant = iota
	peekedVariantV2
)

func (srv *Server) checkInboundConn(remoteIP net.IP) error {
	if remoteIP == nil {
		return nil
	}
	// Reject connections that do not match NetRestrict.
	if srv.NetRestrict != nil && !srv.NetRestrict.Contains(remoteIP) {
		return fmt.Errorf("not in netrestrict list")
	}
	// Hard-reject banned IPs (Bitcoin Core src/net.cpp:1800
	// AcceptConnection ban check). Trusted-list status would
	// override this in Bitcoin Core's CNode permissions, but at
	// this stage of the connection we don't yet know whether the
	// remote is one of our trusted nodes — if an operator wants
	// a banned subnet to still reach trusted peers they should
	// unban, not work around it here.
	if srv.BanList != nil && srv.BanList.IsBanned(remoteIP) {
		return fmt.Errorf("banned")
	}
	// Reject Internet peers that try too often. Allow up to
	// maxInboundConnAttemptsPerIP concurrent in-window attempts from
	// the same source IP — anything over that is rate-limited.
	now := srv.clock.Now()
	srv.inboundHistory.expire(now, nil)
	if !netutil.IsLAN(remoteIP) && srv.inboundHistory.count(remoteIP.String()) >= maxInboundConnAttemptsPerIP {
		return fmt.Errorf("too many attempts")
	}
	srv.inboundHistory.add(remoteIP.String(), now.Add(inboundThrottleTime))
	return nil
}

// SetupConn runs the handshakes and attempts to add the connection
// as a peer. It returns when the connection has been added as a peer
// or the handshakes have failed.
func (srv *Server) SetupConn(fd net.Conn, flags connFlag, dialDest *enode.Node) error {
	c := &conn{fd: fd, flags: flags, cont: make(chan error)}
	variant := srv.pickHandshakeVariant(fd, flags, dialDest)
	switch variant {
	case handshakeVariantV2:
		// Outbound v2 dial: the TCP connection is freshly open; the
		// v2Transport's DialHandshake will write the magic byte.
		c.transport = newV2Outbound(fd)
	case handshakeVariantV2Inbound:
		// Inbound v2: the listener-side PeekVersion has already
		// consumed the magic byte. Wrap the peeked connection.
		c.transport = newV2Inbound(fd)
	default:
		// Legacy RLPx path (unchanged).
		if dialDest == nil {
			c.transport = srv.newTransport(fd, nil)
		} else {
			c.transport = srv.newTransport(fd, dialDest.Pubkey())
		}
	}

	err := srv.setupConn(c, flags, dialDest)
	if err != nil {
		c.close(err)
	}
	return err
}

// handshakeVariant is the per-connection choice between legacy RLPx
// ECIES and the Phase 2b v2 handshake. Chosen by pickHandshakeVariant
// at connection setup time.
type handshakeVariant int

const (
	handshakeVariantLegacy handshakeVariant = iota
	handshakeVariantV2
	handshakeVariantV2Inbound
)

// pickHandshakeVariant decides which handshake path to use for a given
// connection. The three axes that matter:
//
//   - Is this an outbound dial or an inbound accept?
//   - Is ExperimentalV2Handshake enabled?
//   - For outbound: does the dial target carry a legacy NodeID (its
//     pubkey resolves to a usable secp256k1 point) or is it a
//     v2-native entry (dialDest is nil / marker)?
//
// For inbound, the caller (listener goroutine) has already dispatched
// via bip324handshake.PeekVersion; pickHandshakeVariant reads a
// per-connection flag recorded on the fd.
func (srv *Server) pickHandshakeVariant(fd net.Conn, flags connFlag, dialDest *enode.Node) handshakeVariant {
	// Inbound: the peek-result is hung off the connection via a
	// *peekedConn wrapper. If peek said v2, the wrapper signals it
	// through the peekedVariant field.
	if flags&inboundConn != 0 {
		if pc, ok := fd.(*peekedConn); ok && pc.variant == peekedVariantV2 {
			return handshakeVariantV2Inbound
		}
		return handshakeVariantLegacy
	}
	// Outbound: the v2 dial path sets v2DialedConn explicitly, so we
	// key off that flag rather than guessing from dialDest. This
	// preserves the legacy outbound contract (dialDest==nil still
	// means "unspecified target, use legacy"), which existing tests
	// and callers rely on.
	if flags&v2DialedConn != 0 {
		return handshakeVariantV2
	}
	// --legacy-discovery=off forbids legacy outbound: anything that
	// reaches here without the v2-dial flag is a bug, but we route it
	// to v2 rather than silently use the legacy path.
	if srv.legacyHandshakeMode() == legacyHandshakeOff {
		return handshakeVariantV2
	}
	return handshakeVariantLegacy
}

// legacyHandshakeMode is a derived view of LegacyDiscoveryMode — UDP
// discovery and legacy RLPx handshake are the two halves of the same
// v1.x identity model, so they share a single operator knob.
type legacyHandshakeMode int

const (
	legacyHandshakeOn legacyHandshakeMode = iota
	legacyHandshakeOff
)

func (srv *Server) legacyHandshakeMode() legacyHandshakeMode {
	if srv.legacyDiscoveryMode() == legacyDiscoveryOff {
		return legacyHandshakeOff
	}
	return legacyHandshakeOn
}

func (srv *Server) setupConn(c *conn, flags connFlag, dialDest *enode.Node) error {
	// Prevent leftover pending conns from entering the handshake.
	srv.lock.Lock()
	running := srv.running
	srv.lock.Unlock()
	if !running {
		return errServerStopped
	}

	// If dialing, figure out the remote public key.
	if dialDest != nil {
		dialPubkey := new(ecdsa.PublicKey)
		if err := dialDest.Load((*enode.Secp256k1)(dialPubkey)); err != nil {
			err = errors.New("dial destination doesn't have a secp256k1 public key")
			srv.log.Trace("Setting up connection failed", "addr", c.fd.RemoteAddr(), "conn", c.flags, "err", err)
			return err
		}
	}

	// Run the RLPx handshake.
	remotePubkey, err := c.doEncHandshake(srv.PrivateKey)
	if err != nil {
		srv.log.Trace("Failed RLPx handshake", "addr", c.fd.RemoteAddr(), "conn", c.flags, "err", err)
		return err
	}
	// For v2 transports there is no persistent peer identity; we
	// derive c.node from the remote ephemeral X25519 key via
	// v2NodeFromConn, which uses enode.SignNull to assign a
	// session-scoped ID. Any dialDest we started with is replaced so
	// the run-loop's peers map is keyed by the real session identity.
	if v2t, v2 := c.transport.(*v2Transport); v2 {
		c.node = v2NodeFromConn(v2t.remoteEphem, c.fd)
	} else if dialDest == nil {
		c.node = nodeFromConn(remotePubkey, c.fd)
	} else {
		c.node = dialDest
	}
	clog := srv.log.New("id", c.node.ID(), "addr", c.fd.RemoteAddr(), "conn", c.flags)
	err = srv.checkpoint(c, srv.checkpointPostHandshake)
	if err != nil {
		clog.Trace("Rejected peer", "err", err)
		return err
	}

	// Run the capability negotiation handshake.
	phs, err := c.doProtoHandshake(srv.ourHandshake)
	if err != nil {
		clog.Trace("Failed p2p handshake", "err", err)
		return err
	}
	if id := c.node.ID(); !bytes.Equal(crypto.Keccak256(phs.ID), id[:]) {
		clog.Trace("Wrong devp2p handshake identity", "phsid", hex.EncodeToString(phs.ID))
		return DiscUnexpectedIdentity
	}
	c.caps, c.name = phs.Caps, phs.Name
	// Inbound v1 peers: nodeFromConn built c.node with the ephemeral
	// source port from the TCP socket, which won't match any addrman
	// entry keyed by the peer's advertised listening port. Rebuild
	// c.node using phs.ListenPort so addrmanGood resolves to the
	// correct service-key.
	if _, isV2 := c.transport.(*v2Transport); !isV2 && c.is(inboundConn) && phs.ListenPort != 0 {
		if tcp, ok := c.fd.RemoteAddr().(*net.TCPAddr); ok {
			if pub := c.node.Pubkey(); pub != nil {
				c.node = enode.NewV4(pub, tcp.IP, int(phs.ListenPort), int(phs.ListenPort))
			}
		}
	}
	err = srv.checkpoint(c, srv.checkpointAddPeer)
	if err != nil {
		clog.Trace("Rejected peer", "err", err)
		return err
	}

	return nil
}

func nodeFromConn(pubkey *ecdsa.PublicKey, conn net.Conn) *enode.Node {
	var ip net.IP
	var port int
	if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		ip = tcp.IP
		port = tcp.Port
	}
	return enode.NewV4(pubkey, ip, port, port)
}

// checkpoint sends the conn to run, which performs the
// post-handshake checks for the stage (posthandshake, addpeer).
func (srv *Server) checkpoint(c *conn, stage chan<- *conn) error {
	select {
	case stage <- c:
	case <-srv.quit:
		return errServerStopped
	}
	return <-c.cont
}

func (srv *Server) launchPeer(c *conn) *Peer {
	p := newPeer(srv.log, c, srv.Protocols)
	if srv.EnableMsgEvents {
		// If message events are enabled, pass the peerFeed
		// to the peer.
		p.events = &srv.peerFeed
	}
	// Cache the peer's network-group bytes once. Eviction passes
	// read this on every candidate without recomputing.
	p.computeAndCacheNetworkGroup()
	// Block-relay-only is a sticky outbound-slot tag set by the dial
	// scheduler. Mirrors Bitcoin Core's m_relays_txs=false on
	// block-relay-only peers (src/net_processing.cpp:3681): no tx
	// traffic, no address gossip. Inbound peers never carry the bit.
	if c.is(blockRelayConn) {
		p.SetBlockRelayOnly(true)
		p.SetRelayTxs(false)
	}
	go srv.runPeer(p)
	return p
}

// runPeer runs in its own goroutine for each peer.
func (srv *Server) runPeer(p *Peer) {
	if srv.newPeerHook != nil {
		srv.newPeerHook(p)
	}
	srv.peerFeed.Send(&PeerEvent{
		Type:          PeerEventTypeAdd,
		Peer:          p.ID(),
		RemoteAddress: p.RemoteAddr().String(),
		LocalAddress:  p.LocalAddr().String(),
	})

	// Run the per-peer main loop.
	remoteRequested, err := p.run()

	// Announce disconnect on the main loop to update the peer set.
	// The main loop waits for existing peers to be sent on srv.delpeer
	// before returning, so this send should not select on srv.quit.
	srv.delpeer <- peerDrop{p, err, remoteRequested}

	// Broadcast peer drop to external subscribers. This needs to be
	// after the send to delpeer so subscribers have a consistent view of
	// the peer set (i.e. Server.Peers() doesn't include the peer when the
	// event is received.
	srv.peerFeed.Send(&PeerEvent{
		Type:          PeerEventTypeDrop,
		Peer:          p.ID(),
		Error:         err.Error(),
		RemoteAddress: p.RemoteAddr().String(),
		LocalAddress:  p.LocalAddr().String(),
	})
}

// NodeInfo represents a short summary of the information known about the host.
//
// Enode and ENR are pointer types so they marshal to JSON null when the
// node is running in v2-only mode (--legacy-discovery=off) — the
// persistent secp256k1 identity has no peer-visible use in that mode,
// and emitting its URL as if it were a dialable identifier would
// mislead operators.
type NodeInfo struct {
	ID    string  `json:"id"`    // Unique node identifier (also the encryption key)
	Name  string  `json:"name"`  // Name of the node, including client type, version, OS, custom data
	Enode *string `json:"enode"` // Enode URL for adding this peer from remote peers; null in v2-only mode
	ENR   *string `json:"enr"`   // Parallax Node Record; null in v2-only mode
	IP    string  `json:"ip"`    // IP address of the node
	Ports struct {
		// Discovery is the UDP listening port for legacy discv4. In
		// v2-only mode (--legacy-discovery=off) there is no UDP
		// socket, and this field reports the TCP listener port
		// instead — discovery on a v2-only node happens entirely
		// over TCP via parallax-disc/1 gossip.
		Discovery int `json:"discovery"`
		Listener  int `json:"listener"` // TCP listening port for RLPx
	} `json:"ports"`
	ListenAddr string         `json:"listenAddr"`
	Protocols  map[string]any `json:"protocols"`
}

// NodeInfo gathers and returns a collection of metadata known about the host.
func (srv *Server) NodeInfo() *NodeInfo {
	// Gather and assemble the generic node infos
	node := srv.Self()
	info := &NodeInfo{
		Name:       srv.Name,
		ID:         node.ID().String(),
		IP:         node.IP().String(),
		ListenAddr: srv.ListenAddr,
		Protocols:  make(map[string]any),
	}
	info.Ports.Listener = node.TCP()
	if srv.legacyHandshakeMode() == legacyHandshakeOff {
		// v2-only: no persistent identity is dialable, no UDP exists.
		// Enode/ENR null, discovery port mirrors the TCP port.
		info.Ports.Discovery = node.TCP()
	} else {
		enode := node.URLv4()
		enr := node.String()
		info.Enode = &enode
		info.ENR = &enr
		info.Ports.Discovery = node.UDP()
	}

	// Gather all the running protocol infos (only once per protocol type)
	for _, proto := range srv.Protocols {
		if _, ok := info.Protocols[proto.Name]; !ok {
			nodeInfo := any("unknown")
			if query := proto.NodeInfo; query != nil {
				nodeInfo = proto.NodeInfo()
			}
			info.Protocols[proto.Name] = nodeInfo
		}
	}
	return info
}

// PeersInfo returns an array of metadata objects describing connected peers.
func (srv *Server) PeersInfo() []*PeerInfo {
	// Gather all the generic and sub-protocol specific infos
	infos := make([]*PeerInfo, 0, srv.PeerCount())
	for _, peer := range srv.Peers() {
		if peer != nil {
			infos = append(infos, peer.Info())
		}
	}
	// Sort the result array by node identifier where available,
	// falling back to RemoteAddress for v2 peers (whose ID is nil).
	keyOf := func(p *PeerInfo) string {
		if p.ID != nil {
			return *p.ID
		}
		return p.Network.RemoteAddress
	}
	for i := 0; i < len(infos); i++ {
		for j := i + 1; j < len(infos); j++ {
			if keyOf(infos[i]) > keyOf(infos[j]) {
				infos[i], infos[j] = infos[j], infos[i]
			}
		}
	}
	return infos
}
