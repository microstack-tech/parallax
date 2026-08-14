// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of parallax.
//
// parallax is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// parallax is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with parallax. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx/bip324handshake"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"gopkg.in/urfave/cli.v1"
)

// parallax-disc {crawl,probe} sit on top of a single probeOne primitive
// that speaks parallax-disc/1 over either v2 (BIP324) or legacy RLPx,
// branching on the seed's KeyType. crawl is the multi-hop stateful
// walker (see parallaxdisc_walk.go); probe is a single-shot diagnostic.
// The seed format is auto-detected: `ip:port` → v2 dial (KeyType=0x00);
// `enode://...` → legacy dial (KeyType=0x01) — matching admin_addPeer.

var (
	parallaxDiscCommand = cli.Command{
		Name:  "parallax-disc",
		Usage: "Parallax PIP-0006 discovery tools (crawl, probe)",
		Subcommands: []cli.Command{
			parallaxDiscCrawlerCommand,
			parallaxDiscProbeCommand,
		},
	}

	parallaxDiscProbeCommand = cli.Command{
		Name: "probe",
		Usage: "Single-shot probe of one node over parallax-disc/1 — emits the returned Peers " +
			"sample as JSON. Accepts ip:port (v2) or enode://... (legacy).",
		ArgsUsage: "<addr>",
		Action:    parallaxDiscProbe,
	}
)

// crawlResult mirrors discv4-crawl's nodeset output but with the
// parallax-disc PeerEntry fields surfaced. One row per learned peer.
// Returned by `probe` (single-shot); the walker uses CrawlState.
type crawlResult struct {
	Seed    string       `json:"seed"`
	RanAt   time.Time    `json:"ranAt"`
	Entries []crawlEntry `json:"entries"`
}

type crawlEntry struct {
	Network  uint8  `json:"network"` // BIP155 tag
	IP       string `json:"ip"`
	TCPPort  uint16 `json:"tcpPort"`
	KeyType  uint8  `json:"keyType"`
	NodeID   string `json:"nodeId,omitempty"` // hex, 64 bytes when KeyType=0x01
	LastSeen uint64 `json:"lastSeen"`
}

// CrawlNode identifies one peer the crawler probes and carries the
// per-node statistics tracked across runs. KeyType/NodeID record the
// identity as gossiped (or as given in an enode:// seed); only the
// single-shot probe command dispatches the handshake variant on them
// (KeyType=0x00 → v2 BIP324, KeyType=0x01 → legacy RLPx with the
// NodeID-derived pubkey). The walker ignores them for dialing and
// always probes v2 — legacy crawling is a separate tool. The stats are
// only populated by the walker; single-shot probes leave them zero.
type CrawlNode struct {
	NetworkID uint8  `json:"network"` // BIP155 tag (only IPv4/IPv6 are dialable)
	IP        string `json:"ip"`      // text form ("1.2.3.4" / "2001:db8::1")
	TCPPort   uint16 `json:"tcpPort"`
	KeyType   uint8  `json:"keyType"`
	NodeID    string `json:"nodeId,omitempty"` // hex, 64 bytes when KeyType=0x01; empty otherwise

	FirstSeen    time.Time `json:"firstSeen,omitempty"`
	LastSuccess  time.Time `json:"lastSuccess,omitempty"`
	LastAttempt  time.Time `json:"lastAttempt,omitempty"`
	SuccessCount uint64    `json:"successCount,omitempty"`
	FailCount    uint64    `json:"failCount,omitempty"`
	LastError    string    `json:"lastError,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
}

func (n *CrawlNode) tcpAddr() string {
	return net.JoinHostPort(n.IP, strconv.Itoa(int(n.TCPPort)))
}

// parallaxDiscProbe is the `parallax-disc probe <addr>` action: dial
// once, ask GetPeers, write the response as JSON. <addr> is either
// `ip:port` (v2 dial, KeyType=0x00) or `enode://...` (legacy dial,
// KeyType=0x01) — same convention as admin_addPeer.
func parallaxDiscProbe(ctx *cli.Context) error {
	if ctx.NArg() != 1 {
		return fmt.Errorf("usage: parallax-disc probe <addr>")
	}
	node, err := parseSeed(ctx.Args().First())
	if err != nil {
		return err
	}
	entries, _, err := probeOne(context.Background(), node)
	if err != nil {
		return err
	}
	cells := translateEntries(entries)
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Network != cells[j].Network {
			return cells[i].Network < cells[j].Network
		}
		if cells[i].IP != cells[j].IP {
			return cells[i].IP < cells[j].IP
		}
		return cells[i].TCPPort < cells[j].TCPPort
	})
	out := crawlResult{
		Seed:    node.tcpAddr(),
		RanAt:   time.Now(),
		Entries: cells,
	}
	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(enc))
	return nil
}

// parseSeed accepts a seed in either `ip:port` (v2) or `enode://...`
// (legacy) form and returns a CrawlNode populated with the right
// KeyType and (for legacy) hex NodeID. Mirrors admin_addPeer's
// branching so operators can paste either format anywhere a seed is
// asked for.
func parseSeed(s string) (*CrawlNode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty seed")
	}
	if strings.HasPrefix(s, "enode://") {
		n, err := enode.Parse(enode.ValidSchemes, s)
		if err != nil {
			return nil, fmt.Errorf("invalid enode: %w", err)
		}
		if n.IP() == nil || n.TCP() == 0 {
			return nil, fmt.Errorf("enode missing ip or tcp port")
		}
		net4 := disc.NetIPv4
		ipBytes := n.IP().To4()
		if ipBytes == nil {
			net4 = disc.NetIPv6
			ipBytes = n.IP().To16()
		}
		return &CrawlNode{
			NetworkID: net4,
			IP:        ipBytes.String(),
			TCPPort:   uint16(n.TCP()),
			KeyType:   disc.KeyTypeSecp256k1,
			NodeID:    hex.EncodeToString(crypto.FromECDSAPub(n.Pubkey())[1:]),
		}, nil
	}
	// ip:port — v2.
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q (expected ip:port or enode://...): %w", s, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP %q", host)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("invalid port %q", portStr)
	}
	netID := disc.NetIPv4
	ipBytes := ip.To4()
	if ipBytes == nil {
		netID = disc.NetIPv6
		ipBytes = ip.To16()
	}
	return &CrawlNode{
		NetworkID: netID,
		IP:        ipBytes.String(),
		TCPPort:   uint16(port),
		KeyType:   disc.KeyTypeNone,
	}, nil
}

// probeOne dials one node, runs the appropriate handshake, negotiates
// parallax-disc/1, sends YourAddr + GetPeers, and returns the Peers
// reply along with the peer's advertised capabilities. The capabilities
// let callers tag the source node (the walker stores them per node so
// the publisher can confirm parallax-disc/1 support).
//
// Total timeout is 20s — the walker layers concurrency (and a higher
// per-run budget) on top.
func probeOne(_ context.Context, node *CrawlNode) (peers []disc.PeerEntry, caps []p2p.Cap, err error) {
	deadline := time.Now().Add(20 * time.Second)
	fd, err := net.DialTimeout("tcp", node.tcpAddr(), 10*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("dial: %w", err)
	}
	defer fd.Close()
	_ = fd.SetDeadline(deadline)

	wc, ourID, err := dialAndAuth(fd, node)
	if err != nil {
		return nil, nil, err
	}

	hello := &devp2pHello{
		Version:    5,
		Name:       "parallax-disc-crawl",
		Caps:       crawlerCaps,
		ListenPort: 0,
		ID:         ourID,
	}
	helloPayload, err := rlp.EncodeToBytes(hello)
	if err != nil {
		return nil, nil, err
	}
	if err := wc.WriteMsg(helloCode, helloPayload); err != nil {
		return nil, nil, fmt.Errorf("write hello: %w", err)
	}
	code, data, err := wc.ReadMsg()
	if err != nil {
		return nil, nil, fmt.Errorf("read hello: %w", err)
	}
	if code != helloCode {
		return nil, nil, fmt.Errorf("expected Hello (code 0), got %d", code)
	}
	var theirHello devp2pHello
	if err := rlp.DecodeBytes(data, &theirHello); err != nil {
		return nil, nil, fmt.Errorf("decode hello: %w", err)
	}
	// Snappy negotiation only applies to legacy. v2 frames are already
	// AEAD-sealed and the framing carries no Snappy bit.
	if lc, ok := wc.(*legacyWireConn); ok && theirHello.Version >= 5 {
		lc.c.SetSnappy(true)
	}

	discOffset, err := computeDiscOffset(theirHello.Caps)
	if err != nil {
		return nil, nil, err
	}

	// Hello is the first parallax-disc/1 message the peer expects on
	// every session. Sending anything else first trips the server's
	// "msg 0x?? before Hello" gate and ends the session immediately.
	// We have no listen port (we don't accept inbound) and no services
	// to advertise; the random nonce is just there to satisfy the
	// peer's self-connect check (they compare it against their own
	// nonce, which we obviously don't share).
	helloMsgPayload, err := rlp.EncodeToBytes(disc.Hello{
		ProtoVersion: disc.HelloMinProtoVersion,
		Nonce:        randomDiscHelloNonce(),
	})
	if err != nil {
		return nil, nil, err
	}
	if err := wc.WriteMsg(uint64(discOffset)+disc.HelloMsg, helloMsgPayload); err != nil {
		return nil, nil, fmt.Errorf("write disc Hello: %w", err)
	}
	// YourAddr follows Hello on every session. Zero-filled — we are
	// not dialable from the peer's perspective during a crawl.
	yourAddrPayload, err := rlp.EncodeToBytes(disc.YourAddr{})
	if err != nil {
		return nil, nil, err
	}
	if err := wc.WriteMsg(uint64(discOffset)+disc.YourAddrMsg, yourAddrPayload); err != nil {
		return nil, nil, fmt.Errorf("write YourAddr: %w", err)
	}
	getPeersPayload, err := rlp.EncodeToBytes(disc.GetPeers{})
	if err != nil {
		return nil, nil, err
	}
	if err := wc.WriteMsg(uint64(discOffset)+disc.GetPeersMsg, getPeersPayload); err != nil {
		return nil, nil, fmt.Errorf("write GetPeers: %w", err)
	}

	// The daemon jitters its GetPeers response (Poisson mean 2s, cap
	// 6s) and may push unsolicited single-entry relay messages in the
	// meantime — taking the first Peers message as "the" answer would
	// truncate the sample to one relayed address. Accumulate: any
	// multi-entry message is the solicited response (relay pushes are
	// always single-entry) and completes the probe; otherwise collect
	// until the jitter window closes and return the union.
	var (
		collected []disc.PeerEntry
		window    = time.Now().Add(8 * time.Second) // jitter cap 6s + margin
	)
	for {
		if window.Before(deadline) {
			_ = fd.SetReadDeadline(window)
		}
		code, data, err := wc.ReadMsg()
		if err != nil {
			if len(collected) > 0 && errors.Is(err, os.ErrDeadlineExceeded) {
				return collected, theirHello.Caps, nil
			}
			return nil, nil, fmt.Errorf("read reply: %w", err)
		}
		// The daemon rejects messages over disc.MaxMessageSize before
		// decoding; mirror it so a hostile node can't force outsized
		// transient allocations (the frame caps alone allow ~48x more).
		if len(data) > disc.MaxMessageSize {
			return nil, nil, fmt.Errorf("oversized message: %d > %d", len(data), disc.MaxMessageSize)
		}
		switch {
		case code == uint64(discOffset)+disc.PeersMsg:
			var pkt disc.Peers
			if err := rlp.DecodeBytes(data, &pkt); err != nil {
				return nil, nil, fmt.Errorf("decode Peers: %w", err)
			}
			// Enforce the same shape limits the daemon handler
			// applies: a hostile probed node must not feed an
			// oversized or malformed fan-out into the crawl queue.
			if err := pkt.Validate(); err != nil {
				return nil, nil, fmt.Errorf("invalid Peers: %w", err)
			}
			if len(pkt.Entries) > 1 {
				return append(collected, pkt.Entries...), theirHello.Caps, nil
			}
			collected = append(collected, pkt.Entries...)
		case code == disconnectCode:
			return nil, nil, fmt.Errorf("peer disconnected during crawl")
		default:
			// YourAddr / Ping / Pong / other subprotocol messages —
			// ignore.
		}
	}
}

// dialAndAuth runs the encryption handshake matching node.KeyType and
// returns a wire-level conn plus the 64-byte ID we'll put in our Hello.
// For v2 the ID is `ephem || sha256(ephem)` (matches v2Transport's
// identity derivation in p2p/transport_v2.go); for legacy it's the
// secp256k1 pubkey x||y (matches the rlpx Hello).
func dialAndAuth(fd net.Conn, node *CrawlNode) (wireConn, []byte, error) {
	switch node.KeyType {
	case disc.KeyTypeNone:
		bc := bip324handshake.NewConn(fd)
		if err := bc.DialHandshake(); err != nil {
			return nil, nil, fmt.Errorf("v2 handshake: %w", err)
		}
		localEphem, _ := bc.SessionKeys()
		if len(localEphem) != 32 {
			return nil, nil, fmt.Errorf("v2 handshake produced empty session key")
		}
		return &v2WireConn{c: bc}, v2SessionIDBytes(localEphem), nil

	case disc.KeyTypeSecp256k1:
		nodeIDBytes, err := hex.DecodeString(node.NodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid hex NodeID: %w", err)
		}
		if len(nodeIDBytes) != 64 {
			return nil, nil, fmt.Errorf("legacy entry has wrong NodeID length: %d (want 64)", len(nodeIDBytes))
		}
		// SEC1 uncompressed prefix.
		pub, err := crypto.UnmarshalPubkey(append([]byte{0x04}, nodeIDBytes...))
		if err != nil {
			return nil, nil, fmt.Errorf("decode legacy NodeID into pubkey: %w", err)
		}
		conn := rlpx.NewConn(fd, pub)
		ourKey, err := crypto.GenerateKey()
		if err != nil {
			return nil, nil, err
		}
		if _, err := conn.Handshake(ourKey); err != nil {
			return nil, nil, fmt.Errorf("legacy handshake: %w", err)
		}
		// Hello.ID for legacy is the secp256k1 pubkey x||y (no 0x04 prefix).
		return &legacyWireConn{c: conn, fd: fd}, crypto.FromECDSAPub(&ourKey.PublicKey)[1:], nil

	default:
		return nil, nil, fmt.Errorf("unknown KeyType: %d", node.KeyType)
	}
}

// crawlerCaps is the capability set the crawler offers in its devp2p
// Hello. computeDiscOffset negotiates against this same set — the two
// must never diverge, or the crawler and the server compute different
// message-code layouts.
//
// Deliberately parallax-disc only: the crawler never speaks the prl
// block/tx protocol, and advertising parallax/66 without sending its
// Status message armed the daemon's 5s prl handshake timeout — which
// raced the disc response's 2-6s Poisson jitter and spuriously tore
// down a measurable fraction of probes.
var crawlerCaps = []p2p.Cap{
	{Name: "parallax-disc", Version: 1},
}

// crawlerCapLengths maps each (name, version) the crawler speaks to
// its message-code Length. parallax-disc's Length comes from the
// protocol package so a future bump can't leave this table stale.
var crawlerCapLengths = map[p2p.Cap]uint64{
	{Name: "parallax-disc", Version: 1}: disc.ProtocolLength,
}

// computeDiscOffset returns the parallax-disc subprotocol's message-code
// base after devp2p capability negotiation against the peer's Hello.
//
// Mirrors the server's negotiation exactly: only capabilities BOTH
// sides offer count (per name, the highest mutual version), laid out
// alphabetically by name in contiguous blocks starting at
// baseProtocolLength=16. Matching by name alone would silently desync
// the layouts the moment the daemon ships a parallax version we don't
// speak, and counting every advertised version would double-count a
// peer offering two parallax versions.
func computeDiscOffset(theirCaps []p2p.Cap) (int, error) {
	const baseProtocolLength = 16
	negotiated := make(map[string]p2p.Cap)
	for _, ours := range crawlerCaps {
		for _, theirs := range theirCaps {
			if theirs.Name != ours.Name || theirs.Version != ours.Version {
				continue
			}
			if cur, ok := negotiated[ours.Name]; !ok || ours.Version > cur.Version {
				negotiated[ours.Name] = ours
			}
		}
	}
	if _, ok := negotiated["parallax-disc"]; !ok {
		return -1, fmt.Errorf("no mutual parallax-disc version with peer (got caps: %v)", theirCaps)
	}
	names := make([]string, 0, len(negotiated))
	for name := range negotiated {
		names = append(names, name)
	}
	sort.Strings(names)
	off := uint64(baseProtocolLength)
	for _, name := range names {
		if name == "parallax-disc" {
			return int(off), nil
		}
		off += crawlerCapLengths[negotiated[name]]
	}
	return -1, fmt.Errorf("no mutual parallax-disc version with peer (got caps: %v)", theirCaps)
}

func translateEntries(entries []disc.PeerEntry) []crawlEntry {
	out := make([]crawlEntry, 0, len(entries))
	for _, e := range entries {
		skip, err := e.Validate()
		if skip || err != nil {
			continue
		}
		var ip string
		switch e.NetworkID {
		case disc.NetIPv4:
			ip = net.IP(e.Addr).String()
		case disc.NetIPv6:
			ip = net.IP(e.Addr).String()
		default:
			// Tor/I2P/CJDNS — emit as hex so downstream tooling can
			// at least tag them.
			ip = fmt.Sprintf("%x", e.Addr)
		}
		ce := crawlEntry{
			Network:  e.NetworkID,
			IP:       ip,
			TCPPort:  e.TCPPort,
			KeyType:  e.KeyType,
			LastSeen: e.LastSeen,
		}
		if len(e.NodeID) > 0 {
			ce.NodeID = fmt.Sprintf("%x", e.NodeID)
		}
		out = append(out, ce)
	}
	return out
}

// devp2pHello mirrors the base p2p.Hello message so we don't need to
// import p2p's (unexported) protoHandshake. Shape must match exactly.
type devp2pHello struct {
	Version    uint64
	Name       string
	Caps       []p2p.Cap
	ListenPort uint64
	ID         []byte

	// Ignore additional fields for forward compat (devp2p spec says
	// this must be tolerated).
	Rest []rlp.RawValue `rlp:"tail"`
}

const (
	helloCode      = 0
	disconnectCode = 1
)

// wireConn abstracts the post-handshake message transport so probeOne
// is identical for v2 and legacy paths. Both implementations return
// the same (code, payload) pair where payload is whatever the peer put
// after the RLP-encoded code prefix.
type wireConn interface {
	WriteMsg(code uint64, payload []byte) error
	ReadMsg() (code uint64, payload []byte, err error)
	Close() error
}

// legacyWireConn wraps an rlpx.Conn (legacy ECIES handshake established).
type legacyWireConn struct {
	c  *rlpx.Conn
	fd net.Conn
}

func (l *legacyWireConn) WriteMsg(code uint64, payload []byte) error {
	_, err := l.c.Write(code, payload)
	return err
}
func (l *legacyWireConn) ReadMsg() (uint64, []byte, error) {
	code, data, _, err := l.c.Read()
	return code, data, err
}
func (l *legacyWireConn) Close() error { return l.fd.Close() }

// v2WireConn wraps a bip324handshake.Conn. The wire shape inside each
// AEAD frame matches what p2p/transport_v2.go produces:
// `RLP(code) || raw_payload_bytes`. probeOne writes already-encoded
// payloads and decodes the code via rlp.SplitUint64 on the way back.
type v2WireConn struct {
	c *bip324handshake.Conn
}

func (v *v2WireConn) WriteMsg(code uint64, payload []byte) error {
	buf := rlp.AppendUint64(nil, code)
	buf = append(buf, payload...)
	return v.c.Write(buf)
}
func (v *v2WireConn) ReadMsg() (uint64, []byte, error) {
	plain, err := v.c.Read()
	if err != nil {
		return 0, nil, err
	}
	code, rest, err := rlp.SplitUint64(plain)
	if err != nil {
		return 0, nil, fmt.Errorf("v2 invalid message code: %w", err)
	}
	return code, rest, nil
}
func (v *v2WireConn) Close() error { return v.c.Close() }

// randomDiscHelloNonce returns a fresh 64-bit value for the
// disc/1 Hello.Nonce field. The crawler sends a one-shot Hello,
// so per-process randomness is enough — there's no equivalent of
// the daemon's per-startup persistent nonce. crypto/rand never
// fails on a healthy host; on the off chance it does we fall back
// to a non-zero sentinel rather than crash a probe run.
func randomDiscHelloNonce() uint64 {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return 0xDEADBEEFCAFE0001
	}
	n := binary.BigEndian.Uint64(b[:])
	if n == 0 {
		// The daemon compares nonces for equality only (self-connect
		// check); avoid 0 anyway so the field never looks unset in
		// captures or logs.
		n = 1
	}
	return n
}

// v2SessionIDBytes mirrors p2p/transport_v2.go's identity derivation:
// the Hello.ID for a v2 peer is the local X25519 ephemeral pubkey
// followed by SHA-256 of itself (32+32 = 64 bytes). The remote takes
// keccak256 of this to derive the per-session enode.ID. We replicate
// it here to keep cmd/devp2p free of a hard dep on the p2p package.
func v2SessionIDBytes(ephem []byte) []byte {
	h := sha256.Sum256(ephem)
	out := make([]byte, 64)
	copy(out[:32], ephem)
	copy(out[32:], h[:])
	return out
}
