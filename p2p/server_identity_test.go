// Copyright 2026 The Parallax Protocol Authors
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

package p2p

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
)

// identityTestPeer builds an outbound peer whose rw.node carries a real
// secp256k1 pubkey (so addrman.PubkeyBytes works) and whose transport
// is v2 or legacy per the flag.
func identityTestPeer(t *testing.T, ip net.IP, port uint16, v2 bool) (*Peer, []byte) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	node := enode.NewV4(&key.PublicKey, ip, int(port), 0)
	fd, _ := net.Pipe()
	c := &conn{fd: fd, node: node, name: "identity-test"}
	if v2 {
		c.transport = &v2Transport{}
	}
	p := newPeer(logging.Root(), c, nil)
	close(p.closed)
	pub := crypto.FromECDSAPub(&key.PublicKey)[1:]
	return p, pub
}

func identityTestServer(t *testing.T) (*Server, *addrman.AddrMan, addrman.NetAddr) {
	t.Helper()
	book, err := addrman.New()
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{log: logging.Root(), addrbook: book}
	addr, err := addrman.NewNetAddr(addrman.NetIPv4, []byte{8, 8, 8, 8}, 30303)
	if err != nil {
		t.Fatal(err)
	}
	return srv, book, addr
}

// A successful outbound v2 session must overwrite a stale gossiped
// secp256k1 identity with KeyType 0x00 — first-hand knowledge
// overwrites, mirroring Bitcoin Core's SetServices-on-VERSION.
func TestUpgradeAddrIdentityV2OverwritesLegacy(t *testing.T) {
	srv, book, addr := identityTestServer(t)
	stale := bytes.Repeat([]byte{0x42}, 64)
	if !book.AddOne(addr, 0x01, stale, time.Now(), addr, addrman.SourceTCPGossip, 0) {
		t.Fatal("seed AddOne failed")
	}

	p, _ := identityTestPeer(t, net.IP{8, 8, 8, 8}, 30303, true)
	srv.upgradeAddrIdentity(p, addr)

	info := book.Lookup(addr)
	if info == nil {
		t.Fatal("entry vanished")
	}
	if info.KeyType != 0x00 || len(info.NodeID) != 0 {
		t.Fatalf("want KeyType 0x00 with empty NodeID, got KeyType %#x NodeID len %d", info.KeyType, len(info.NodeID))
	}
}

// A successful outbound legacy session against an already-legacy entry
// refreshes the stored NodeID to the key the RLPx handshake verified.
func TestUpgradeAddrIdentityLegacyRefreshesStaleKey(t *testing.T) {
	srv, book, addr := identityTestServer(t)
	stale := bytes.Repeat([]byte{0x42}, 64)
	if !book.AddOne(addr, 0x01, stale, time.Now(), addr, addrman.SourceTCPGossip, 0) {
		t.Fatal("seed AddOne failed")
	}

	p, pub := identityTestPeer(t, net.IP{8, 8, 8, 8}, 30303, false)
	srv.upgradeAddrIdentity(p, addr)

	info := book.Lookup(addr)
	if info == nil {
		t.Fatal("entry vanished")
	}
	if info.KeyType != 0x01 {
		t.Fatalf("want KeyType 0x01, got %#x", info.KeyType)
	}
	if !bytes.Equal(info.NodeID, pub) {
		t.Fatalf("NodeID not refreshed to handshake-verified key")
	}
}

// A legacy session must never downgrade a v2-native (0x00) entry to
// 0x01 — v1-success does not imply v2-failure, and the downgrade would
// hide a dual-stack peer from V2Iter.
func TestUpgradeAddrIdentityLegacyNeverDowngradesV2(t *testing.T) {
	srv, book, addr := identityTestServer(t)
	if !book.AddOne(addr, 0x00, nil, time.Now(), addr, addrman.SourceTCPGossip, 0) {
		t.Fatal("seed AddOne failed")
	}

	p, _ := identityTestPeer(t, net.IP{8, 8, 8, 8}, 30303, false)
	srv.upgradeAddrIdentity(p, addr)

	info := book.Lookup(addr)
	if info == nil {
		t.Fatal("entry vanished")
	}
	if info.KeyType != 0x00 || len(info.NodeID) != 0 {
		t.Fatalf("v2 entry downgraded: KeyType %#x NodeID len %d", info.KeyType, len(info.NodeID))
	}
}

// Unknown addresses are a no-op for the legacy path (no entry to
// refresh) and must not create entries via the v2 path either.
func TestUpgradeAddrIdentityUnknownAddrNoop(t *testing.T) {
	srv, book, addr := identityTestServer(t)

	pLegacy, _ := identityTestPeer(t, net.IP{8, 8, 8, 8}, 30303, false)
	srv.upgradeAddrIdentity(pLegacy, addr)
	pV2, _ := identityTestPeer(t, net.IP{8, 8, 8, 8}, 30303, true)
	srv.upgradeAddrIdentity(pV2, addr)

	if info := book.Lookup(addr); info != nil {
		t.Fatalf("entry created out of thin air: %+v", info)
	}
}
