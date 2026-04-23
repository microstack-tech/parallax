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
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
	"gopkg.in/urfave/cli.v1"
)

// parallax-disc crawl connects to a seed node via RLPx, negotiates
// parallax-disc/1, and fetches its addrbook sample. Single-shot for
// now; a multi-hop walk can be layered on top of this primitive. The
// output schema matches discv4 crawl so downstream analysis keeps
// working during the PIP-0006 Phase 6 transition.

var (
	parallaxDiscCommand = cli.Command{
		Name:  "parallax-disc",
		Usage: "Parallax PIP-0006 discovery tools (crawl, probe)",
		Subcommands: []cli.Command{
			parallaxDiscCrawlCommand,
		},
	}

	parallaxDiscCrawlCommand = cli.Command{
		Name:      "crawl",
		Usage:     "Probe a seed node over parallax-disc/1 and emit the returned Peers sample as JSON",
		ArgsUsage: "<enode>",
		Action:    parallaxDiscCrawl,
	}
)

// crawlResult mirrors discv4-crawl's nodeset output but with the
// parallax-disc PeerEntry fields surfaced. One row per learned peer.
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

func parallaxDiscCrawl(ctx *cli.Context) error {
	if ctx.NArg() != 1 {
		return fmt.Errorf("usage: parallax-disc crawl <enode>")
	}
	n, err := enode.Parse(enode.ValidSchemes, ctx.Args().First())
	if err != nil {
		return fmt.Errorf("invalid enode: %w", err)
	}
	if n.IP() == nil || n.TCP() == 0 {
		return fmt.Errorf("enode missing ip or tcp port")
	}

	entries, err := crawlOne(n)
	if err != nil {
		return err
	}

	// Sort for stable output.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Network != entries[j].Network {
			return entries[i].Network < entries[j].Network
		}
		if entries[i].IP != entries[j].IP {
			return entries[i].IP < entries[j].IP
		}
		return entries[i].TCPPort < entries[j].TCPPort
	})

	out := crawlResult{
		Seed:    n.URLv4(),
		RanAt:   time.Now(),
		Entries: entries,
	}
	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(enc))
	return nil
}

// crawlOne dials n, does the RLPx + devp2p handshake advertising only
// the parallax-disc/1 capability, sends a YourAddr + GetPeers, reads
// the Peers response, and returns the entries.
//
// Timeouts are short (20s total) because this is a one-shot probe —
// crawlers running against many seeds layer concurrency on top.
func crawlOne(n *enode.Node) ([]crawlEntry, error) {
	deadline := time.Now().Add(20 * time.Second)
	fd, err := net.DialTimeout("tcp", fmt.Sprintf("%v:%d", n.IP(), n.TCP()), 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer fd.Close()
	_ = fd.SetDeadline(deadline)

	conn := rlpx.NewConn(fd, n.Pubkey())
	ourKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Handshake(ourKey); err != nil {
		return nil, fmt.Errorf("rlpx handshake: %w", err)
	}

	// Send devp2p Hello advertising only parallax-disc/1 (plus a
	// dummy parallax/66 cap so the remote doesn't immediately
	// disconnect for lack of a shared base protocol). Base protocol
	// codes occupy 0..15; subprotocol blocks start at 16, assigned
	// in alphabetical order by name. With our cap set
	// [parallax/66, parallax-disc/1], parallax gets 16..16+17-1
	// and parallax-disc gets the block right after.
	hello := &devp2pHello{
		Version:    5,
		Name:       "parallax-disc-crawl",
		Caps:       []p2p.Cap{{Name: "parallax", Version: 66}, {Name: "parallax-disc", Version: 1}},
		ListenPort: 0,
		ID:         crypto.FromECDSAPub(&ourKey.PublicKey)[1:],
	}
	if err := writeMsg(conn, helloCode, hello); err != nil {
		return nil, fmt.Errorf("write hello: %w", err)
	}
	// Read their Hello to learn the negotiated offset.
	code, data, _, err := conn.Read()
	if err != nil {
		return nil, fmt.Errorf("read hello: %w", err)
	}
	if code != helloCode {
		return nil, fmt.Errorf("expected Hello (code 0), got %d", code)
	}
	var theirHello devp2pHello
	if err := rlp.DecodeBytes(data, &theirHello); err != nil {
		return nil, fmt.Errorf("decode hello: %w", err)
	}
	if theirHello.Version >= 5 {
		conn.SetSnappy(true)
	}

	// Compute the parallax-disc subprotocol code offset. devp2p sorts
	// (caps ∩ their caps) by name and assigns contiguous blocks
	// starting at baseProtocolLength=16. parallax/66 has length 17,
	// parallax-disc/1 has length 3. Alphabetical → parallax first.
	const baseProtocolLength = 16
	const parallaxProtocolLength = 17
	discOffset := -1
	var matched []p2p.Cap
	for _, theirs := range theirHello.Caps {
		if theirs.Name == "parallax" || theirs.Name == "parallax-disc" {
			matched = append(matched, theirs)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	off := uint64(baseProtocolLength)
	for _, c := range matched {
		if c.Name == "parallax-disc" {
			discOffset = int(off)
			break
		}
		if c.Name == "parallax" {
			off += parallaxProtocolLength
		}
	}
	if discOffset == -1 {
		return nil, fmt.Errorf("peer does not advertise parallax-disc/1 (got caps: %v)", theirHello.Caps)
	}

	// Send YourAddr — mandatory as the first parallax-disc/1 message
	// after negotiation. Zero-filled since we aren't dialable from
	// their perspective during a crawl.
	yourAddr := disc.YourAddr{}
	if err := writeMsg(conn, uint64(discOffset)+disc.YourAddrMsg, yourAddr); err != nil {
		return nil, fmt.Errorf("write YourAddr: %w", err)
	}
	// Send GetPeers.
	if err := writeMsg(conn, uint64(discOffset)+disc.GetPeersMsg, disc.GetPeers{}); err != nil {
		return nil, fmt.Errorf("write GetPeers: %w", err)
	}

	// Read responses until we get Peers or time out. Drop anything
	// else (their YourAddr or pings) silently.
	for {
		code, data, _, err := conn.Read()
		if err != nil {
			return nil, fmt.Errorf("read reply: %w", err)
		}
		switch {
		case code == uint64(discOffset)+disc.PeersMsg:
			var pkt disc.Peers
			if err := rlp.DecodeBytes(data, &pkt); err != nil {
				return nil, fmt.Errorf("decode Peers: %w", err)
			}
			return translateEntries(pkt.Entries), nil
		case code == disconnectCode:
			return nil, fmt.Errorf("peer disconnected during crawl")
		default:
			// YourAddr / Ping / Pong / other subprotocol
			// messages — ignore.
		}
	}
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
			// Tor/I2P/CJDNS — emit as hex so downstream tooling
			// can at least tag them.
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

// writeMsg RLP-encodes v and writes it at `code`.
func writeMsg(conn *rlpx.Conn, code uint64, v any) error {
	payload, err := rlp.EncodeToBytes(v)
	if err != nil {
		return err
	}
	_, err = conn.Write(code, payload)
	return err
}
