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
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
)

// makeLegacyNode builds an enode.Node with a fresh secp256k1 key plus
// the given IP and TCP port. The returned 64-byte NodeID is what
// addrman stores internally (x || y, no 0x04 prefix).
func makeLegacyNode(t *testing.T, ip net.IP, tcp int) (*enode.Node, []byte) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // elliptic.Marshal is deprecated for general use but
	// remains the canonical encoder for discv4/enode NodeIDs (64 bytes,
	// x || y without the 0x04 prefix). secp256k1 is not provided by
	// crypto/ecdh, so the alternative the linter recommends doesn't exist.
	id := elliptic.Marshal(key.PublicKey.Curve, key.PublicKey.X, key.PublicKey.Y)
	return enode.NewV4(&key.PublicKey, ip, tcp, tcp), id[1:]
}

// TestNodeIterYieldsStoredEntry — a legacy (KeyType=0x01) entry added to
// addrman surfaces through NodeIter.
func TestNodeIterYieldsStoredEntry(t *testing.T) {
	m, err := New(Deterministic(42))
	if err != nil {
		t.Fatal(err)
	}
	ip := net.IPv4(8, 8, 8, 8)
	n, nodeID := makeLegacyNode(t, ip, 30303)
	addr, _ := NewNetAddr(NetIPv4, n.IP().To4(), uint16(n.TCP()))
	src := addr
	if !m.AddOne(addr, 0x01, nodeID, time.Now(), src, SourceLegacyUDP, 0) {
		t.Fatal("AddOne failed")
	}

	it := NewNodeIter(m, 10*time.Millisecond)
	defer it.Close()

	done := make(chan *enode.Node, 1)
	go func() {
		if it.Next() {
			done <- it.Node()
		} else {
			done <- nil
		}
	}()
	select {
	case got := <-done:
		if got == nil {
			t.Fatal("NodeIter.Next returned false")
		}
		if got.ID() != n.ID() {
			t.Errorf("got %s, want %s", got.ID(), n.ID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NodeIter did not yield")
	}
}

// TestNodeIterSkipsV2NativeEntries — KeyType=0x00 entries have no NodeID,
// so NodeIter (which constructs legacy enodes) must skip them.
func TestNodeIterSkipsV2NativeEntries(t *testing.T) {
	m, err := New(Deterministic(42))
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := NewNetAddr(NetIPv4, []byte{8, 8, 4, 4}, 30303)
	if !m.AddOne(addr, 0x00, nil, time.Now(), addr, SourceTCPGossip, 0) {
		t.Fatal("AddOne v2-native failed")
	}

	it := NewNodeIter(m, 10*time.Millisecond)
	defer it.Close()

	done := make(chan struct{})
	go func() {
		_ = it.Next() // would block forever if Close didn't fire
		close(done)
	}()
	// Close quickly — Next() must not yield the v2-native entry.
	time.Sleep(40 * time.Millisecond)
	it.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("NodeIter did not stop on Close")
	}
}

// TestIngestNodeRoundTrip — IngestNode feeds a legacy enode into addrman
// and NodeIter reconstructs an equivalent *enode.Node.
func TestIngestNodeRoundTrip(t *testing.T) {
	m, err := New(Deterministic(7))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := makeLegacyNode(t, net.IPv4(1, 2, 3, 4), 30303)
	if !IngestNode(m, n, SourceDNSSeed, time.Now()) {
		t.Fatal("IngestNode returned false")
	}

	it := NewNodeIter(m, 10*time.Millisecond)
	defer it.Close()
	done := make(chan *enode.Node, 1)
	go func() {
		if it.Next() {
			done <- it.Node()
		} else {
			done <- nil
		}
	}()
	select {
	case got := <-done:
		if got == nil || got.ID() != n.ID() {
			t.Fatalf("round trip failed: got=%v want=%s", got, n.ID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// TestTeeIterFeedsAddrman — wrapping an enode.IterNodes with TeeIter
// populates addrman while passing nodes through to the caller.
func TestTeeIterFeedsAddrman(t *testing.T) {
	m, err := New(Deterministic(99))
	if err != nil {
		t.Fatal(err)
	}
	n1, _ := makeLegacyNode(t, net.IPv4(9, 9, 9, 9), 30303)
	n2, _ := makeLegacyNode(t, net.IPv4(4, 4, 4, 4), 30303)
	tee := NewTeeIter(enode.IterNodes([]*enode.Node{n1, n2}), m, SourceLegacyUDP)
	defer tee.Close()

	for tee.Next() {
		// Consume; effect is in addrman.
	}
	if got := m.Size(nil, nil); got != 2 {
		t.Errorf("after tee, addrman size = %d, want 2", got)
	}
	counts := m.CountsBySource()
	if counts[SourceLegacyUDP] != 2 {
		t.Errorf("source counts = %v, want 2 legacy_udp", counts)
	}
}

// TestV2IterSkipsSelf — when isSelf reports a stored KeyType=0x00
// entry as the local node, V2Iter must skip it and emit the next
// one. Without this gate, the disc protocol's quorum-confirmed
// self-IP is selected forever and the v2 dialer burns its dial
// cycles on a guarded self-dial.
func TestV2IterSkipsSelf(t *testing.T) {
	m, err := New(Deterministic(13))
	if err != nil {
		t.Fatal(err)
	}
	selfAddr, _ := NewNetAddr(NetIPv4, []byte{45, 236, 49, 58}, 32110)
	otherAddr, _ := NewNetAddr(NetIPv4, []byte{8, 8, 4, 4}, 32110)
	if !m.AddOne(selfAddr, 0x00, nil, time.Now(), selfAddr, SourceTCPGossip, 0) {
		t.Fatal("AddOne self failed")
	}
	if !m.AddOne(otherAddr, 0x00, nil, time.Now(), otherAddr, SourceTCPGossip, 0) {
		t.Fatal("AddOne other failed")
	}

	selfTCP := &net.TCPAddr{IP: net.IPv4(45, 236, 49, 58), Port: 32110}
	isSelf := func(addr *net.TCPAddr) bool {
		return addr.Port == selfTCP.Port && addr.IP.Equal(selfTCP.IP)
	}

	it := NewV2Iter(m, 10*time.Millisecond, isSelf)
	defer it.Close()

	got := make(chan NetAddr, 1)
	go func() {
		if it.Next() {
			got <- it.Candidate().Addr
		} else {
			got <- NetAddr{}
		}
	}()

	select {
	case cand := <-got:
		if !cand.Valid() {
			t.Fatal("V2Iter.Next returned false")
		}
		if cand.Equal(selfAddr) {
			t.Fatalf("V2Iter emitted self entry %v", cand)
		}
		if !cand.Equal(otherAddr) {
			t.Fatalf("V2Iter emitted unexpected entry: got %v want %v", cand, otherAddr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("V2Iter did not yield")
	}
}

// TestV2IterNilSelfFn — passing a nil IsSelfFunc must disable the
// filter, not panic. Lets unit tests against a bare AddrMan use
// V2Iter without inventing a self-fn.
func TestV2IterNilSelfFn(t *testing.T) {
	m, err := New(Deterministic(14))
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := NewNetAddr(NetIPv4, []byte{1, 2, 3, 4}, 32110)
	if !m.AddOne(addr, 0x00, nil, time.Now(), addr, SourceTCPGossip, 0) {
		t.Fatal("AddOne failed")
	}

	it := NewV2Iter(m, 10*time.Millisecond, nil)
	defer it.Close()

	got := make(chan NetAddr, 1)
	go func() {
		if it.Next() {
			got <- it.Candidate().Addr
		} else {
			got <- NetAddr{}
		}
	}()

	select {
	case cand := <-got:
		if !cand.Equal(addr) {
			t.Fatalf("got %v want %v", cand, addr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("V2Iter did not yield with nil isSelf")
	}
}
