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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
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
// per-node statistics tracked across runs. The identity fields
// (NetworkID, IP, TCPPort, KeyType, NodeID) are enough to dispatch the
// right handshake variant: KeyType=0x00 → v2 (BIP324), KeyType=0x01 →
// legacy RLPx with NodeID-derived pubkey. The stats are only populated
// by the walker; single-shot probes leave them zero.
type CrawlNode struct {
	NetworkID uint8  `json:"network"`           // BIP155 tag (only IPv4/IPv6 are dialable)
	IP        string `json:"ip"`                // text form ("1.2.3.4" / "2001:db8::1")
	TCPPort   uint16 `json:"tcpPort"`
	KeyType   uint8  `json:"keyType"`
	NodeID    string `json:"nodeId,omitempty"`  // hex, 64 bytes when KeyType=0x01; empty otherwise

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
			IP:        net.IP(ipBytes).String(),
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
		IP:        net.IP(ipBytes).String(),
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
		Caps:       []p2p.Cap{{Name: "parallax", Version: 66}, {Name: "parallax-disc", Version: 1}},
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

	// YourAddr is mandatory as the first parallax-disc/1 message after
	// negotiation. Zero-filled — we are not dialable from the peer's
	// perspective during a crawl.
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

	for {
		code, data, err := wc.ReadMsg()
		if err != nil {
			return nil, nil, fmt.Errorf("read reply: %w", err)
		}
		switch {
		case code == uint64(discOffset)+disc.PeersMsg:
			var pkt disc.Peers
			if err := rlp.DecodeBytes(data, &pkt); err != nil {
				return nil, nil, fmt.Errorf("decode Peers: %w", err)
			}
			return pkt.Entries, theirHello.Caps, nil
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

// computeDiscOffset returns the parallax-disc subprotocol's message-code
// base after devp2p capability negotiation against the peer's Hello.
//
// devp2p sorts (our caps ∩ their caps) by name and assigns contiguous
// blocks starting at baseProtocolLength=16. parallax/66 has length 17,
// parallax-disc/1 has length 3. Alphabetical → parallax first if both
// matched.
func computeDiscOffset(theirCaps []p2p.Cap) (int, error) {
	const baseProtocolLength = 16
	const parallaxProtocolLength = 17
	var matched []p2p.Cap
	for _, theirs := range theirCaps {
		if theirs.Name == "parallax" || theirs.Name == "parallax-disc" {
			matched = append(matched, theirs)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	off := uint64(baseProtocolLength)
	for _, c := range matched {
		switch c.Name {
		case "parallax-disc":
			return int(off), nil
		case "parallax":
			off += parallaxProtocolLength
		}
	}
	return -1, fmt.Errorf("peer does not advertise parallax-disc/1 (got caps: %v)", theirCaps)
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

// writeMsg is kept for symmetry with the previous file's API; new code
// should use wireConn.WriteMsg directly.
func writeMsg(conn *rlpx.Conn, code uint64, v any) error {
	payload, err := rlp.EncodeToBytes(v)
	if err != nil {
		return err
	}
	_, err = conn.Write(code, payload)
	return err
}
