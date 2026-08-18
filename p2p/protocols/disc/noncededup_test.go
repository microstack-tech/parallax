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

package disc

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
)

// TestSelectNonceDupLoserSymmetry — both endpoints of a mutual
// connection pair must independently drop the SAME underlying TCP
// connection. Model: node A (nonce a) and node B (nonce b) hold
// C1 = A→B and C2 = B→A. At A, C1 is outbound and C2 inbound; at B
// the directions flip. Whatever the nonce ordering, the two loser
// picks must name the same C.
func TestSelectNonceDupLoserSymmetry(t *testing.T) {
	mkPair := func() (aOut, aIn, bIn, bOut *p2p.Peer) {
		// A's view of C1/C2, then B's view of C1/C2.
		aOut = testPeerOnPipe(t) // C1 at A (outbound)
		aIn = testPeerOnPipe(t)  // C2 at A (inbound)
		aIn.MarkInboundForTest()
		bIn = testPeerOnPipe(t) // C1 at B (inbound)
		bIn.MarkInboundForTest()
		bOut = testPeerOnPipe(t) // C2 at B (outbound)
		return
	}
	for _, tc := range []struct {
		name           string
		nonceA, nonceB uint64
	}{
		{"a-lower", 5, 10},
		{"b-lower", 10, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aOut, aIn, bIn, bOut := mkPair()
			loserA := selectNonceDupLoser(aOut, aIn, tc.nonceA, tc.nonceB)
			loserB := selectNonceDupLoser(bIn, bOut, tc.nonceB, tc.nonceA)
			// Map each pick back onto C1/C2.
			aDropsC1 := loserA == aOut
			bDropsC1 := loserB == bIn
			if aDropsC1 != bDropsC1 {
				t.Fatalf("split-brain: A drops C1=%v, B drops C1=%v", aDropsC1, bDropsC1)
			}
			// And the surviving connection is the one initiated by the
			// lower-nonce node.
			lowerIsA := tc.nonceA < tc.nonceB
			if lowerIsA && aDropsC1 {
				t.Fatal("lower-nonce initiator's connection (C1) was dropped")
			}
			if !lowerIsA && !aDropsC1 {
				t.Fatal("lower-nonce initiator's connection (C2) was dropped")
			}
		})
	}
}

// TestHandleHelloNonceDedup — two sessions carrying the same remote
// nonce resolve to one, the survivor inherits the outbound leg's dial
// target, and feelers never participate.
func TestHandleHelloNonceDedup(t *testing.T) {
	onion, err := addrman.ParseOnion("2gzyxa5ihm7nsggfxnu52rck2vv4rvmdlkiu3zzui5du4xyclen53wid.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	newBackend := func(ourNonce uint64) *AddrmanBackend {
		m, err := addrman.New(addrman.Deterministic(20))
		if err != nil {
			t.Fatal(err)
		}
		return NewAddrmanBackend(m, nil, nil, nil, func() Hello {
			return Hello{ProtoVersion: HelloMinProtoVersion, Nonce: ourNonce}
		})
	}
	const theirNonce = uint64(500)

	// Case 1: our nonce below theirs — our outbound leg survives, the
	// inbound twin is dropped.
	b := newBackend(100)
	out := testPeerOnPipe(t)
	out.SetDialTargetForTest(onion)
	if err := b.HandleHello(out, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce}); err != nil {
		t.Fatal(err)
	}
	in := testPeerOnPipe(t)
	in.MarkInboundForTest()
	if err := b.HandleHello(in, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce}); err != nil {
		t.Fatal(err)
	}
	if !in.DisconnectRequested() || out.DisconnectRequested() {
		t.Fatalf("our-nonce-lower: dropped in=%v out=%v, want the inbound twin dropped",
			in.DisconnectRequested(), out.DisconnectRequested())
	}

	// Case 2: our nonce above theirs — their dial survives (our
	// inbound leg), our outbound leg is dropped and its target is
	// adopted so the dialer sees the address as connected.
	b = newBackend(900)
	out2 := testPeerOnPipe(t)
	out2.SetDialTargetForTest(onion)
	if err := b.HandleHello(out2, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce}); err != nil {
		t.Fatal(err)
	}
	in2 := testPeerOnPipe(t)
	in2.MarkInboundForTest()
	if err := b.HandleHello(in2, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce}); err != nil {
		t.Fatal(err)
	}
	if !out2.DisconnectRequested() || in2.DisconnectRequested() {
		t.Fatalf("our-nonce-higher: dropped in=%v out=%v, want the outbound leg dropped",
			in2.DisconnectRequested(), out2.DisconnectRequested())
	}

	// Same-direction pairs are dual-stack siblings (two addresses of
	// one node, both dialed by us) and are kept — Core keeps both
	// such connections, and dropping one would only churn.
	b = newBackend(100)
	sibA, sibB := testPeerOnPipe(t), testPeerOnPipe(t)
	sibA.SetDialTargetForTest(onion)
	b.HandleHello(sibA, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce})
	b.HandleHello(sibB, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce})
	if sibA.DisconnectRequested() || sibB.DisconnectRequested() {
		t.Fatal("dual-stack sibling sessions were deduped")
	}

	// Distinct nonces never dedup.
	b = newBackend(100)
	p1, p2 := testPeerOnPipe(t), testPeerOnPipe(t)
	b.HandleHello(p1, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 111})
	b.HandleHello(p2, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: 222})
	if p1.DisconnectRequested() || p2.DisconnectRequested() {
		t.Fatal("distinct nonces triggered dedup")
	}

	// Feelers are exempt on either side.
	b = newBackend(100)
	real := testPeerOnPipe(t)
	b.HandleHello(real, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce})
	probe := testPeerOnPipe(t)
	probe.MarkFeelerForTest()
	b.HandleHello(probe, Hello{ProtoVersion: HelloMinProtoVersion, Nonce: theirNonce})
	if real.DisconnectRequested() || probe.DisconnectRequested() {
		t.Fatal("feeler participated in nonce dedup")
	}
}
