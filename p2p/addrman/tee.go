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

package addrman

import (
	"crypto/elliptic"
	"net"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
)

// TeeIter wraps an upstream enode.Iterator and, for each node it yields,
// ingests the node into an AddrMan under the supplied Source tag. The
// original node is passed through to the caller unchanged.
//
// Used in Server.setupDiscovery to capture discv4 discoveries into
// addrman with source=legacy_udp, and DNS-seed / enrtree results with
// source=dns_seed, without changing the existing dial path.
type TeeIter struct {
	upstream enode.Iterator
	m        *AddrMan
	tag      Source
}

// NewTeeIter builds a TeeIter. The upstream iterator is owned — calling
// Close on TeeIter will close the upstream too.
func NewTeeIter(upstream enode.Iterator, m *AddrMan, tag Source) *TeeIter {
	return &TeeIter{upstream: upstream, m: m, tag: tag}
}

func (t *TeeIter) Next() bool {
	if !t.upstream.Next() {
		return false
	}
	n := t.upstream.Node()
	if n != nil {
		t.ingestLocked(n)
	}
	return true
}

func (t *TeeIter) Node() *enode.Node { return t.upstream.Node() }

func (t *TeeIter) Close() { t.upstream.Close() }

// IngestV2Addr feeds a plain (ip, port) into m with KeyType=0x00 and
// the supplied source tag. Used by bootnode ingest when the entry
// comes from the v2-native ip:port shape rather than a legacy enode
// URL. Returns true if the address was inserted or gained an
// additional bucket reference.
func IngestV2Addr(m *AddrMan, addr *net.TCPAddr, tag Source, lastSeen time.Time) bool {
	if m == nil || addr == nil {
		return false
	}
	ip := addr.IP
	if ip == nil || addr.Port == 0 {
		return false
	}
	var netID NetID
	var addrBytes []byte
	if v4 := ip.To4(); v4 != nil {
		netID = NetIPv4
		addrBytes = v4
	} else {
		netID = NetIPv6
		addrBytes = ip.To16()
	}
	naddr, err := NewNetAddr(netID, addrBytes, uint16(addr.Port))
	if err != nil {
		return false
	}
	return m.AddOne(naddr, 0x00, nil, lastSeen, naddr, tag, 0)
}

// IngestV2NetAddr is IngestV2Addr for callers that already hold the
// BIP155 form — bootnode entries can be onion addresses (PIP-0007).
func IngestV2NetAddr(m *AddrMan, addr NetAddr, tag Source, lastSeen time.Time) bool {
	if m == nil || len(addr.Bytes()) == 0 || addr.Port == 0 {
		return false
	}
	return m.AddOne(addr, 0x00, nil, lastSeen, addr, tag, 0)
}

// IngestNode feeds a single enode.Node into m with the given Source tag.
// Exported so callers (e.g., bootnode ingest) can use it directly without
// constructing a one-shot iterator.
func IngestNode(m *AddrMan, n *enode.Node, tag Source, lastSeen time.Time) bool {
	if m == nil || n == nil {
		return false
	}
	ip := n.IP()
	if ip == nil || n.TCP() == 0 {
		return false
	}
	var net NetID
	var addrBytes []byte
	if v4 := ip.To4(); v4 != nil {
		net = NetIPv4
		addrBytes = v4
	} else {
		net = NetIPv6
		addrBytes = ip
	}
	addr, err := NewNetAddr(net, addrBytes, uint16(n.TCP()))
	if err != nil {
		return false
	}
	nodeID, err := pubkeyBytes(n)
	if err != nil {
		return false
	}
	return m.AddOne(addr, 0x01, nodeID, lastSeen, addr, tag, 0)
}

func (t *TeeIter) ingestLocked(n *enode.Node) {
	IngestNode(t.m, n, t.tag, time.Now())
}

// PubkeyBytes returns the 64-byte (x || y) uncompressed form of n's
// secp256k1 public key — the format addrman stores and the wire format
// for parallax-disc/1 KeyType=0x01 entries. Exported so callers
// outside this package can supply the NodeID payload for
// UpgradeIdentity.
func PubkeyBytes(n *enode.Node) ([]byte, error) { return pubkeyBytes(n) }

// pubkeyBytes is the unexported implementation reused by tee.go's
// IngestNode and by the exported wrapper above.
func pubkeyBytes(n *enode.Node) ([]byte, error) {
	pub := n.Pubkey()
	if pub == nil {
		return nil, ErrMalformedAddr
	}
	// Marshal and strip the 0x04 prefix — matches crypto.FromECDSAPub
	// semantics but keeps this package from depending on that helper's
	// exact behavior across crypto package revisions.
	b := elliptic.Marshal(pub.Curve, pub.X, pub.Y) //nolint:staticcheck // deprecated but exact bytes match discv4
	if len(b) != 65 || b[0] != 0x04 {
		return nil, ErrMalformedAddr
	}
	return b[1:], nil
}

// Compile-time reference so `crypto` stays imported when pubkeyBytes is
// the only consumer and an unused-import checker might otherwise gripe.
var _ = crypto.UnmarshalPubkey
