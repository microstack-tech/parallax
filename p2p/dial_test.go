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

package p2p

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/internal/testlog"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/netutil"
	"github.com/ParallaxProtocol/parallax/util/mclock"
)

// This test checks that dynamic dials are launched from discovery results.
func TestDialSchedDynDial(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   4,
	}
	runDialTest(t, config, []dialTestRound{
		// 3 out of 4 peers are connected, leaving 2 dial slots.
		// 9 nodes are discovered, but only 2 are dialed.
		{
			peersAdded: []*conn{
				{flags: staticDialedConn, node: newNode(uintID(0x00), "")},
				{flags: dynDialedConn, node: newNode(uintID(0x01), "")},
				{flags: dynDialedConn, node: newNode(uintID(0x02), "")},
			},
			discovered: []*enode.Node{
				newNode(uintID(0x00), "127.0.0.1:32110"), // not dialed because already connected as static peer
				newNode(uintID(0x02), "127.0.0.1:32110"), // ...
				newNode(uintID(0x03), "127.0.0.1:32110"),
				newNode(uintID(0x04), "127.0.0.1:32110"),
				newNode(uintID(0x05), "127.0.0.1:32110"), // not dialed because there are only two slots
				newNode(uintID(0x06), "127.0.0.1:32110"), // ...
				newNode(uintID(0x07), "127.0.0.1:32110"), // ...
				newNode(uintID(0x08), "127.0.0.1:32110"), // ...
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x03), "127.0.0.1:32110"),
				newNode(uintID(0x04), "127.0.0.1:32110"),
			},
		},

		// One dial completes, freeing one dial slot.
		{
			failed: []enode.ID{
				uintID(0x04),
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x05), "127.0.0.1:32110"),
			},
		},

		// Dial to 0x03 completes, filling the last remaining peer slot.
		{
			succeeded: []enode.ID{
				uintID(0x03),
			},
			failed: []enode.ID{
				uintID(0x05),
			},
			discovered: []*enode.Node{
				newNode(uintID(0x09), "127.0.0.1:32110"), // not dialed because there are no free slots
			},
		},

		// 3 peers drop off, creating 6 dial slots. Check that 5 of those slots
		// (i.e. up to maxActiveDialTasks) are used.
		{
			peersRemoved: []enode.ID{
				uintID(0x00),
				uintID(0x01),
				uintID(0x02),
			},
			discovered: []*enode.Node{
				newNode(uintID(0x0a), "127.0.0.1:32110"),
				newNode(uintID(0x0b), "127.0.0.1:32110"),
				newNode(uintID(0x0c), "127.0.0.1:32110"),
				newNode(uintID(0x0d), "127.0.0.1:32110"),
				newNode(uintID(0x0f), "127.0.0.1:32110"),
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x06), "127.0.0.1:32110"),
				newNode(uintID(0x07), "127.0.0.1:32110"),
				newNode(uintID(0x08), "127.0.0.1:32110"),
				newNode(uintID(0x09), "127.0.0.1:32110"),
				newNode(uintID(0x0a), "127.0.0.1:32110"),
			},
		},
	})
}

// This test checks that candidates that do not match the netrestrict list are not dialed.
func TestDialSchedNetRestrict(t *testing.T) {
	t.Parallel()

	nodes := []*enode.Node{
		newNode(uintID(0x01), "127.0.0.1:32110"),
		newNode(uintID(0x02), "127.0.0.2:32110"),
		newNode(uintID(0x03), "127.0.0.3:32110"),
		newNode(uintID(0x04), "127.0.0.4:32110"),
		newNode(uintID(0x05), "127.0.2.5:32110"),
		newNode(uintID(0x06), "127.0.2.6:32110"),
		newNode(uintID(0x07), "127.0.2.7:32110"),
		newNode(uintID(0x08), "127.0.2.8:32110"),
	}
	config := dialConfig{
		netRestrict:    new(netutil.Netlist),
		maxActiveDials: 10,
		maxDialPeers:   10,
	}
	config.netRestrict.Add("127.0.2.0/24")
	runDialTest(t, config, []dialTestRound{
		{
			discovered:   nodes,
			wantNewDials: nodes[4:8],
		},
		{
			succeeded: []enode.ID{
				nodes[4].ID(),
				nodes[5].ID(),
				nodes[6].ID(),
				nodes[7].ID(),
			},
		},
	})
}

// This test checks that static dials work and obey the limits.
func TestDialSchedStaticDial(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   4,
	}
	runDialTest(t, config, []dialTestRound{
		// Static dials are launched for the nodes that
		// aren't yet connected.
		{
			peersAdded: []*conn{
				{flags: dynDialedConn, node: newNode(uintID(0x01), "127.0.0.1:32110")},
				{flags: dynDialedConn, node: newNode(uintID(0x02), "127.0.0.2:32110")},
			},
			update: func(d *dialScheduler) {
				// These two are not dialed because they're already connected
				// as dynamic peers.
				d.addStatic(newNode(uintID(0x01), "127.0.0.1:32110"))
				d.addStatic(newNode(uintID(0x02), "127.0.0.2:32110"))
				// These nodes will be dialed:
				d.addStatic(newNode(uintID(0x03), "127.0.0.3:32110"))
				d.addStatic(newNode(uintID(0x04), "127.0.0.4:32110"))
				d.addStatic(newNode(uintID(0x05), "127.0.0.5:32110"))
				d.addStatic(newNode(uintID(0x06), "127.0.0.6:32110"))
				d.addStatic(newNode(uintID(0x07), "127.0.0.7:32110"))
				d.addStatic(newNode(uintID(0x08), "127.0.0.8:32110"))
				d.addStatic(newNode(uintID(0x09), "127.0.0.9:32110"))
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x03), "127.0.0.3:32110"),
				newNode(uintID(0x04), "127.0.0.4:32110"),
				newNode(uintID(0x05), "127.0.0.5:32110"),
				newNode(uintID(0x06), "127.0.0.6:32110"),
			},
		},
		// Dial to 0x03 completes, filling a peer slot. One slot remains,
		// two dials are launched to attempt to fill it.
		{
			succeeded: []enode.ID{
				uintID(0x03),
			},
			failed: []enode.ID{
				uintID(0x04),
				uintID(0x05),
				uintID(0x06),
			},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x04): nil,
				uintID(0x05): nil,
				uintID(0x06): nil,
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x08), "127.0.0.8:32110"),
				newNode(uintID(0x09), "127.0.0.9:32110"),
			},
		},
		// Peer 0x01 drops and 0x07 connects as inbound peer.
		// Only 0x01 is dialed.
		{
			peersAdded: []*conn{
				{flags: inboundConn, node: newNode(uintID(0x07), "127.0.0.7:32110")},
			},
			peersRemoved: []enode.ID{
				uintID(0x01),
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x01), "127.0.0.1:32110"),
			},
		},
	})
}

// This test checks that removing static nodes stops connecting to them.
func TestDialSchedRemoveStatic(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 1,
		maxDialPeers:   1,
	}
	runDialTest(t, config, []dialTestRound{
		// Add static nodes.
		{
			update: func(d *dialScheduler) {
				d.addStatic(newNode(uintID(0x01), "127.0.0.1:32110"))
				d.addStatic(newNode(uintID(0x02), "127.0.0.2:32110"))
				d.addStatic(newNode(uintID(0x03), "127.0.0.3:32110"))
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x01), "127.0.0.1:32110"),
			},
		},
		// Dial to 0x01 fails.
		{
			failed: []enode.ID{
				uintID(0x01),
			},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x01): nil,
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x02), "127.0.0.2:32110"),
			},
		},
		// All static nodes are removed. 0x01 is in history, 0x02 is being
		// dialed, 0x03 is in staticPool.
		{
			update: func(d *dialScheduler) {
				d.removeStatic(newNode(uintID(0x01), "127.0.0.1:32110"))
				d.removeStatic(newNode(uintID(0x02), "127.0.0.2:32110"))
				d.removeStatic(newNode(uintID(0x03), "127.0.0.3:32110"))
			},
			failed: []enode.ID{
				uintID(0x02),
			},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x02): nil,
			},
		},
		// Since all static nodes are removed, they should not be dialed again.
		{},
		{},
		{},
	})
}

// This test checks that static dials are selected at random.
func TestDialSchedManyStaticNodes(t *testing.T) {
	t.Parallel()

	config := dialConfig{maxDialPeers: 2}
	runDialTest(t, config, []dialTestRound{
		{
			peersAdded: []*conn{
				{flags: dynDialedConn, node: newNode(uintID(0xFFFE), "")},
				{flags: dynDialedConn, node: newNode(uintID(0xFFFF), "")},
			},
			update: func(d *dialScheduler) {
				for id := uint16(0); id < 2000; id++ {
					n := newNode(uintID(id), "127.0.0.1:32110")
					d.addStatic(n)
				}
			},
		},
		{
			peersRemoved: []enode.ID{
				uintID(0xFFFE),
				uintID(0xFFFF),
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x0085), "127.0.0.1:32110"),
				newNode(uintID(0x02dc), "127.0.0.1:32110"),
				newNode(uintID(0x0285), "127.0.0.1:32110"),
				newNode(uintID(0x00cb), "127.0.0.1:32110"),
			},
		},
	})
}

// TestPickDynDialFlagsBlockRelayBucket — full-relay slots fill
// first; once full-relay is at target, picker switches to
// block-relay. Once block-relay is at target, picker stays on
// dynDialedConn (the next free slot is a full-relay one again).
//
// Mirrors Bitcoin Core's ThreadOpenConnections type-priority order
// (src/net.cpp:2715-2765): full-relay before block-relay-only.
func TestPickDynDialFlagsBlockRelayBucket(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		maxDial          int
		maxBR            int
		dialPeers        int
		blockRelayDialed int
		want             connFlag
	}{
		{name: "br-disabled", maxDial: 10, maxBR: 0, dialPeers: 0, want: dynDialedConn},
		{name: "first-dial-fr", maxDial: 10, maxBR: 2, dialPeers: 0, want: dynDialedConn},
		{name: "fr-not-yet-full", maxDial: 10, maxBR: 2, dialPeers: 5, want: dynDialedConn},
		{name: "fr-just-filled-br-empty", maxDial: 10, maxBR: 2, dialPeers: 8, want: dynDialedConn | blockRelayConn},
		{name: "fr-full-br-half", maxDial: 10, maxBR: 2, dialPeers: 9, blockRelayDialed: 1, want: dynDialedConn | blockRelayConn},
		{name: "fr-full-br-full", maxDial: 10, maxBR: 2, dialPeers: 10, blockRelayDialed: 2, want: dynDialedConn},
		{name: "br-target-larger-than-budget-clamped", maxDial: 4, maxBR: 4, dialPeers: 0, want: dynDialedConn | blockRelayConn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &dialScheduler{
				dialConfig:       dialConfig{maxDialPeers: tc.maxDial, maxBlockRelay: tc.maxBR},
				dialPeers:        tc.dialPeers,
				blockRelayDialed: tc.blockRelayDialed,
			}
			if got := d.pickDynDialFlags(); got != tc.want {
				t.Fatalf("pickDynDialFlags = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDialSchedBlockRelayBucketFills — end-to-end through the
// scheduler: a maxDial=4 / maxBR=2 scheduler dials 4 nodes; the
// first two are tagged dynDialedConn (full-relay), the last two
// dynDialedConn|blockRelayConn. Verifies blockRelayDialed counts
// match, and that the BR flag flows through to the conn.
func TestDialSchedBlockRelayBucketFills(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 4,
		maxDialPeers:   4,
		maxBlockRelay:  2,
	}
	var (
		clock    = new(mclock.Simulated)
		iterator = newDialTestIterator()
		dialer   = newDialTestDialer()
		resolver = new(dialTestResolver)
		setupCh  = make(chan *conn, 16)
	)
	config.clock = clock
	config.dialer = dialer
	config.resolver = resolver
	config.log = testlog.Logger(t, logging.LvlTrace)
	config.rand = rand.New(rand.NewSource(0x1111))

	var dialsched *dialScheduler
	setup := func(fd net.Conn, f connFlag, node *enode.Node) error {
		c := &conn{flags: f, node: node}
		dialsched.peerAdded(c)
		setupCh <- c
		return nil
	}
	dialsched = newDialScheduler(config, iterator, setup)
	defer dialsched.stop()

	nodes := []*enode.Node{
		newNode(uintID(0x11), "1.0.0.1:32110"),
		newNode(uintID(0x12), "2.0.0.1:32110"),
		newNode(uintID(0x13), "3.0.0.1:32110"),
		newNode(uintID(0x14), "4.0.0.1:32110"),
	}
	iterator.addNodes(nodes)
	ids := []enode.ID{nodes[0].ID(), nodes[1].ID(), nodes[2].ID(), nodes[3].ID()}
	if err := dialer.waitForDials(nodes); err != nil {
		t.Fatalf("waitForDials: %v", err)
	}
	if err := dialer.completeDials(ids, nil); err != nil {
		t.Fatalf("completeDials: %v", err)
	}

	got := make(map[enode.ID]connFlag)
	for i := 0; i < len(nodes); i++ {
		select {
		case c := <-setupCh:
			got[c.node.ID()] = c.flags
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for setup of dial %d", i)
		}
	}

	frCount, brCount := 0, 0
	for _, f := range got {
		if f&blockRelayConn != 0 {
			brCount++
		} else {
			frCount++
		}
	}
	if frCount != 2 || brCount != 2 {
		t.Fatalf("flag distribution wrong: fr=%d br=%d (want 2/2). Map=%v", frCount, brCount, got)
	}
}

// TestDialSchedNetworkGroupDiversity — once an outbound peer in a
// /16 IPv4 group is established, new candidates in the same group
// are skipped. Different groups still dial. Mirrors Bitcoin Core's
// outbound_ipv46_peer_netgroups guard (src/net.cpp:2685).
func TestDialSchedNetworkGroupDiversity(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	runDialTest(t, config, []dialTestRound{
		// Round 0: an outbound peer in 8.8.x.x is already
		// connected. Discover candidates in the same group
		// (8.8.0.0/16) and candidates in distinct groups. Only
		// the distinct-group candidates should be dialed.
		{
			peersAdded: []*conn{
				{flags: dynDialedConn, node: newNode(uintID(0x01), "8.8.1.1:32110")},
			},
			discovered: []*enode.Node{
				newNode(uintID(0x10), "8.8.2.1:32110"),  // same /16 → skip
				newNode(uintID(0x11), "8.8.99.1:32110"), // same /16 → skip
				newNode(uintID(0x20), "1.2.3.4:32110"),  // distinct
				newNode(uintID(0x21), "5.6.7.8:32110"),  // distinct
				newNode(uintID(0x22), "9.0.0.1:32110"),  // distinct
				newNode(uintID(0x23), "10.0.0.1:32110"), // distinct
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x20), "1.2.3.4:32110"),
				newNode(uintID(0x21), "5.6.7.8:32110"),
				newNode(uintID(0x22), "9.0.0.1:32110"),
				newNode(uintID(0x23), "10.0.0.1:32110"),
			},
		},
	})
}

// TestDialSchedSkipsInboundInProgress — when an inbound conn is
// mid-handshake for some NodeID (registered via inboundProgressBegin),
// the dial scheduler must NOT pick that ID from the discovery
// iterator. Closes the symmetric-handshake race that previously had
// us spend a full encryption + protocol handshake just to hit
// DiscAlreadyConnected at the second checkpointAddPeer. PIP-0006
// review item A6.
func TestDialSchedSkipsInboundInProgress(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	racing := newNode(uintID(0x42), "1.2.3.4:32110")
	other := newNode(uintID(0x43), "5.6.7.8:32110")
	runDialTest(t, config, []dialTestRound{
		// Round 0: inbound for racing.ID() is mid-handshake. Both
		// candidates appear from the iterator — only `other` should
		// be dialed; `racing` is suppressed by inboundProgress.
		{
			update: func(d *dialScheduler) {
				d.inboundProgressBegin(racing.ID())
				// Drain the channel synchronously by triggering a
				// loop turn — sleep-free wait by sending a static
				// add and removing it (a no-op the loop must handle
				// before it picks from nodesIn).
			},
			discovered:   []*enode.Node{racing, other},
			wantNewDials: []*enode.Node{other},
		},
		// Round 1: the inbound finishes and unregisters. `racing`
		// should now be eligible.
		{
			update: func(d *dialScheduler) {
				d.inboundProgressEnd(racing.ID())
			},
			discovered:   []*enode.Node{racing},
			wantNewDials: []*enode.Node{racing},
		},
	})
}

// TestDialSchedSkipsBannedAddresses — the scheduler must not dial a
// node whose IP is banned or discouraged, mirroring Bitcoin Core's
// outbound ban gate. Without it, an operator ban is trivially
// bypassed: the banned peer stays in the addrbook and gets redialed
// as an outbound connection.
func TestDialSchedSkipsBannedAddresses(t *testing.T) {
	t.Parallel()

	banned := map[string]bool{"6.6.6.6": true}
	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
		isBanned: func(ip net.IP) bool {
			return banned[ip.String()]
		},
	}
	runDialTest(t, config, []dialTestRound{
		{
			discovered: []*enode.Node{
				newNode(uintID(0x30), "6.6.6.6:32110"), // banned → skip
				newNode(uintID(0x31), "1.2.3.4:32110"), // ok
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x31), "1.2.3.4:32110"),
			},
		},
	})
}

// TestDialSchedGroupLimitInFlight — the one-outbound-per-group rule
// counts in-flight dials, not just attached peers. A burst of
// same-/16 candidates discovered back-to-back must produce exactly
// one dial; the group frees again when the dial fails or the
// attached peer disconnects.
func TestDialSchedGroupLimitInFlight(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	nodeA := newNode(uintID(0x60), "44.55.1.1:32110")
	nodeB := newNode(uintID(0x61), "44.55.2.2:32110")
	nodeC := newNode(uintID(0x62), "44.55.3.3:32110")
	runDialTest(t, config, []dialTestRound{
		// A and B share 44.55.0.0/16: only A is dialed, because A's
		// in-flight dial already occupies the group when B arrives.
		{
			discovered:   []*enode.Node{nodeA, nodeB},
			wantNewDials: []*enode.Node{nodeA},
		},
		// A's dial fails, releasing the in-flight slot.
		{
			failed: []enode.ID{uintID(0x60)},
		},
		// The rediscovered B is dialable again.
		{
			discovered:   []*enode.Node{nodeB},
			wantNewDials: []*enode.Node{nodeB},
		},
		// B attaches; the group moves from in-flight to attached
		// and C stays blocked.
		{
			succeeded:  []enode.ID{uintID(0x61)},
			discovered: []*enode.Node{nodeC},
		},
		// B disconnects, freeing the attached slot; C is dialable
		// after rediscovery.
		{
			peersRemoved: []enode.ID{uintID(0x61)},
			discovered:   []*enode.Node{nodeC},
			wantNewDials: []*enode.Node{nodeC},
		},
	})
}

// TestDialSchedGroupFreedAfterResolveMove — a static task whose dial
// fails and whose re-resolve lands in a different /16 must release the
// group charged at launch, not the group of the resolved endpoint.
// Regression test: the doneCh decrement used to read the resolved
// dest, stranding the original group's in-flight count forever and
// blacking out dynamic dials to that /16 for the process lifetime.
func TestDialSchedGroupFreedAfterResolveMove(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	staticNode := newNode(uintID(0x70), "44.66.1.1:32110")
	moved := newNode(uintID(0x70), "44.77.1.1:32110")
	dynNode := newNode(uintID(0x71), "44.66.9.9:32110")
	runDialTest(t, config, []dialTestRound{
		// The static dial launches, charging group 44.66/16.
		{
			update: func(d *dialScheduler) {
				d.addStatic(staticNode)
			},
			wantNewDials: []*enode.Node{staticNode},
		},
		// The dial fails; the task re-resolves to 44.77/16 and
		// redials in-run (no fresh startDial accounting).
		{
			failed: []enode.ID{uintID(0x70)},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x70): moved,
			},
			wantNewDials: []*enode.Node{moved},
		},
		// The redial fails too; task completion must free 44.66/16,
		// the group recorded at launch. Drop the static so history
		// expiry doesn't relaunch it in the next round.
		{
			failed: []enode.ID{uintID(0x70)},
			update: func(d *dialScheduler) {
				d.removeStatic(staticNode)
			},
		},
		// A dynamic candidate in 44.66/16 is dialable again.
		{
			discovered:   []*enode.Node{dynNode},
			wantNewDials: []*enode.Node{dynNode},
		},
	})
}

// TestDialSchedV2HandoffAccounting — v2-ENR handoffs run as tracked
// dial tasks: an in-flight v2 dial occupies its network group (so a
// same-/16 candidate is not dialed concurrently) and frees it on
// completion. Regression test: the handoff used to be a bare
// goroutine with no d.dialing / dialingGroups registration, so
// same-group v2 candidates dialed in parallel and shutdown never
// waited for the goroutine.
func TestDialSchedV2HandoffAccounting(t *testing.T) {
	t.Parallel()

	var (
		started = make(chan string, 8)
		release = make(chan error)
	)
	config := dialConfig{
		self:           uintID(0xff),
		maxActiveDials: 5,
		maxDialPeers:   5,
		log:            testlog.Logger(t, logging.LvlTrace),
		clock:          new(mclock.Simulated),
		rand:           rand.New(rand.NewSource(0x2222)),
		dialer:         newDialTestDialer(),
		v2Predicate:    func(*enode.Node) bool { return true },
		v2Dial: func(addr addrman.NetAddr) error {
			started <- addr.String()
			return <-release
		},
	}
	it := newDialTestIterator()
	d := newDialScheduler(config, it, func(net.Conn, connFlag, *enode.Node) error { return nil })
	defer d.stop()
	// Unblock any still-parked v2Dial before stop() drains the task
	// (a closed channel yields a nil error). Registered after stop's
	// defer so it runs first.
	defer close(release)

	nodeA := newNode(uintID(0x80), "77.88.1.1:32110")
	nodeB := newNode(uintID(0x81), "77.88.2.2:32110") // same /16 as A
	it.addNodes([]*enode.Node{nodeA, nodeB})

	if got := <-started; got != "77.88.1.1:32110" {
		t.Fatalf("first v2 dial = %s, want 77.88.1.1:32110", got)
	}
	// B shares A's /16: no second dial while A is in flight.
	select {
	case got := <-started:
		t.Fatalf("second same-group v2 dial launched concurrently: %s", got)
	case <-time.After(200 * time.Millisecond):
	}

	// A's dial fails; the group frees and the rediscovered B dials.
	// Wait for the loop to process the task completion before
	// rediscovering B, or B races the doneCh handling and is
	// discarded against the still-charged group.
	release <- errors.New("connection refused")
	for i := 0; d.probeCheckDial(nodeB) != nil; i++ {
		if i > 500 {
			t.Fatal("group never freed after v2 dial completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	it.addNodes([]*enode.Node{nodeB})
	select {
	case got := <-started:
		if got != "77.88.2.2:32110" {
			t.Fatalf("post-release v2 dial = %s, want 77.88.2.2:32110", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("same-group v2 dial not launched after the group freed")
	}
}

// TestDialSchedDiscouragedGate — discouragement blocks dynamic dials
// but not static ones. A ban blocks both; discouragement is stamped
// automatically on misbehavior and has no clearing RPC, so honoring
// it on statics would strand an operator-chosen peering until
// restart. Core's manual connections likewise bypass discouragement.
func TestDialSchedDiscouragedGate(t *testing.T) {
	t.Parallel()

	discouraged := map[string]bool{"7.7.7.7": true, "8.8.8.8": true}
	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
		isDiscouraged: func(ip net.IP) bool {
			return discouraged[ip.String()]
		},
	}
	staticNode := newNode(uintID(0x50), "7.7.7.7:32110")
	runDialTest(t, config, []dialTestRound{
		{
			update: func(d *dialScheduler) {
				d.addStatic(staticNode)
			},
			discovered: []*enode.Node{
				newNode(uintID(0x51), "8.8.8.8:32110"), // discouraged dynamic → skip
				newNode(uintID(0x52), "1.2.3.4:32110"), // ok
			},
			wantNewDials: []*enode.Node{
				staticNode, // discouraged but static → dialed
				newNode(uintID(0x52), "1.2.3.4:32110"),
			},
		},
	})
}

// TestDialSchedInboundProgressRefcount — two inbound conns claiming
// the same NodeID register independently; the dial scheduler must
// keep blocking until BOTH unregister. Defends against a peer that
// opens a second inbound socket while the first is still mid-
// handshake.
func TestDialSchedInboundProgressRefcount(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	racing := newNode(uintID(0x44), "9.10.11.12:32110")
	runDialTest(t, config, []dialTestRound{
		{
			update: func(d *dialScheduler) {
				d.inboundProgressBegin(racing.ID())
				d.inboundProgressBegin(racing.ID())
			},
			discovered:   []*enode.Node{racing},
			wantNewDials: nil, // suppressed (refcount=2)
		},
		{
			update: func(d *dialScheduler) {
				d.inboundProgressEnd(racing.ID())
			},
			discovered:   []*enode.Node{racing},
			wantNewDials: nil, // still suppressed (refcount=1)
		},
		{
			update: func(d *dialScheduler) {
				d.inboundProgressEnd(racing.ID())
			},
			discovered:   []*enode.Node{racing},
			wantNewDials: []*enode.Node{racing},
		},
	})
}

// This test checks that past dials are not retried for some time.
func TestDialSchedHistory(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 3,
		maxDialPeers:   3,
	}
	runDialTest(t, config, []dialTestRound{
		{
			update: func(d *dialScheduler) {
				d.addStatic(newNode(uintID(0x01), "127.0.0.1:32110"))
				d.addStatic(newNode(uintID(0x02), "127.0.0.2:32110"))
				d.addStatic(newNode(uintID(0x03), "127.0.0.3:32110"))
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x01), "127.0.0.1:32110"),
				newNode(uintID(0x02), "127.0.0.2:32110"),
				newNode(uintID(0x03), "127.0.0.3:32110"),
			},
		},
		// No new tasks are launched in this round because all static
		// nodes are either connected or still being dialed.
		{
			succeeded: []enode.ID{
				uintID(0x01),
				uintID(0x02),
			},
			failed: []enode.ID{
				uintID(0x03),
			},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x03): nil,
			},
		},
		// Nothing happens in this round because we're waiting for
		// node 0x3's history entry to expire.
		{},
		// The cache entry for node 0x03 has expired and is retried.
		{
			wantNewDials: []*enode.Node{
				newNode(uintID(0x03), "127.0.0.3:32110"),
			},
		},
	})
}

func TestDialSchedResolve(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 1,
		maxDialPeers:   1,
	}
	node := newNode(uintID(0x01), "")
	resolved := newNode(uintID(0x01), "127.0.0.1:32110")
	resolved2 := newNode(uintID(0x01), "127.0.0.55:32110")
	runDialTest(t, config, []dialTestRound{
		{
			update: func(d *dialScheduler) {
				d.addStatic(node)
			},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x01): resolved,
			},
			wantNewDials: []*enode.Node{
				resolved,
			},
		},
		{
			failed: []enode.ID{
				uintID(0x01),
			},
			wantResolves: map[enode.ID]*enode.Node{
				uintID(0x01): resolved2,
			},
			wantNewDials: []*enode.Node{
				resolved2,
			},
		},
	})
}

// -------
// Code below here is the framework for the tests above.

type dialTestRound struct {
	peersAdded   []*conn
	peersRemoved []enode.ID
	update       func(*dialScheduler) // called at beginning of round
	discovered   []*enode.Node        // newly discovered nodes
	succeeded    []enode.ID           // dials which succeed this round
	failed       []enode.ID           // dials which fail this round
	wantResolves map[enode.ID]*enode.Node
	wantNewDials []*enode.Node // dials that should be launched in this round
}

func runDialTest(t *testing.T, config dialConfig, rounds []dialTestRound) {
	var (
		clock    = new(mclock.Simulated)
		iterator = newDialTestIterator()
		dialer   = newDialTestDialer()
		resolver = new(dialTestResolver)
		peers    = make(map[enode.ID]*conn)
		setupCh  = make(chan *conn)
	)

	// Override config.
	config.clock = clock
	config.dialer = dialer
	config.resolver = resolver
	config.log = testlog.Logger(t, logging.LvlTrace)
	config.rand = rand.New(rand.NewSource(0x1111))

	// Set up the dialer. The setup function below runs on the dialTask
	// goroutine and adds the peer.
	var dialsched *dialScheduler
	setup := func(fd net.Conn, f connFlag, node *enode.Node) error {
		conn := &conn{flags: f, node: node}
		dialsched.peerAdded(conn)
		setupCh <- conn
		return nil
	}
	dialsched = newDialScheduler(config, iterator, setup)
	defer dialsched.stop()

	for i, round := range rounds {
		// Apply peer set updates.
		for _, c := range round.peersAdded {
			if peers[c.node.ID()] != nil {
				t.Fatalf("round %d: peer %v already connected", i, c.node.ID())
			}
			dialsched.peerAdded(c)
			peers[c.node.ID()] = c
		}
		for _, id := range round.peersRemoved {
			c := peers[id]
			if c == nil {
				t.Fatalf("round %d: can't remove non-existent peer %v", i, id)
			}
			dialsched.peerRemoved(c)
		}

		// Init round.
		t.Logf("round %d (%d peers)", i, len(peers))
		resolver.setAnswers(round.wantResolves)
		if round.update != nil {
			round.update(dialsched)
		}
		iterator.addNodes(round.discovered)

		// Unblock dialTask goroutines.
		if err := dialer.completeDials(round.succeeded, nil); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		for range round.succeeded {
			conn := <-setupCh
			peers[conn.node.ID()] = conn
		}
		if err := dialer.completeDials(round.failed, errors.New("oops")); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}

		// Wait for new tasks.
		if err := dialer.waitForDials(round.wantNewDials); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if !resolver.checkCalls() {
			t.Fatalf("unexpected calls to Resolve: %v", resolver.calls)
		}

		clock.Run(16 * time.Second)
	}
}

// dialTestIterator is the input iterator for dialer tests. This works a bit like a channel
// with infinite buffer: nodes are added to the buffer with addNodes, which unblocks Next
// and returns them from the iterator.
type dialTestIterator struct {
	cur *enode.Node

	mu     sync.Mutex
	buf    []*enode.Node
	cond   *sync.Cond
	closed bool
}

func newDialTestIterator() *dialTestIterator {
	it := &dialTestIterator{}
	it.cond = sync.NewCond(&it.mu)
	return it
}

// addNodes adds nodes to the iterator buffer and unblocks Next.
func (it *dialTestIterator) addNodes(nodes []*enode.Node) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.buf = append(it.buf, nodes...)
	it.cond.Signal()
}

// Node returns the current node.
func (it *dialTestIterator) Node() *enode.Node {
	return it.cur
}

// Next moves to the next node.
func (it *dialTestIterator) Next() bool {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.cur = nil
	for len(it.buf) == 0 && !it.closed {
		it.cond.Wait()
	}
	if it.closed {
		return false
	}
	it.cur = it.buf[0]
	copy(it.buf[:], it.buf[1:])
	it.buf = it.buf[:len(it.buf)-1]
	return true
}

// Close ends the iterator, unblocking Next.
func (it *dialTestIterator) Close() {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.closed = true
	it.buf = nil
	it.cond.Signal()
}

// dialTestDialer is the NodeDialer used by runDialTest.
type dialTestDialer struct {
	init    chan *dialTestReq
	blocked map[enode.ID]*dialTestReq
}

type dialTestReq struct {
	n       *enode.Node
	unblock chan error
}

func newDialTestDialer() *dialTestDialer {
	return &dialTestDialer{
		init:    make(chan *dialTestReq),
		blocked: make(map[enode.ID]*dialTestReq),
	}
}

// Dial implements NodeDialer.
func (d *dialTestDialer) Dial(ctx context.Context, n *enode.Node) (net.Conn, error) {
	req := &dialTestReq{n: n, unblock: make(chan error, 1)}
	select {
	case d.init <- req:
		select {
		case err := <-req.unblock:
			pipe, _ := net.Pipe()
			return pipe, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// waitForDials waits for calls to Dial with the given nodes as argument.
// Those calls will be held blocking until completeDials is called with the same nodes.
func (d *dialTestDialer) waitForDials(nodes []*enode.Node) error {
	waitset := make(map[enode.ID]*enode.Node)
	for _, n := range nodes {
		waitset[n.ID()] = n
	}
	timeout := time.NewTimer(1 * time.Second)
	defer timeout.Stop()

	for len(waitset) > 0 {
		select {
		case req := <-d.init:
			want, ok := waitset[req.n.ID()]
			if !ok {
				return fmt.Errorf("attempt to dial unexpected node %v", req.n.ID())
			}
			if !reflect.DeepEqual(req.n, want) {
				return fmt.Errorf("ENR of dialed node %v does not match test", req.n.ID())
			}
			delete(waitset, req.n.ID())
			d.blocked[req.n.ID()] = req
		case <-timeout.C:
			var waitlist []enode.ID
			for id := range waitset {
				waitlist = append(waitlist, id)
			}
			return fmt.Errorf("timed out waiting for dials to %v", waitlist)
		}
	}

	return d.checkUnexpectedDial()
}

func (d *dialTestDialer) checkUnexpectedDial() error {
	select {
	case req := <-d.init:
		return fmt.Errorf("attempt to dial unexpected node %v", req.n.ID())
	case <-time.After(150 * time.Millisecond):
		return nil
	}
}

// completeDials unblocks calls to Dial for the given nodes.
func (d *dialTestDialer) completeDials(ids []enode.ID, err error) error {
	for _, id := range ids {
		req := d.blocked[id]
		if req == nil {
			return fmt.Errorf("can't complete dial to %v", id)
		}
		req.unblock <- err
	}
	return nil
}

// dialTestResolver tracks calls to resolve.
type dialTestResolver struct {
	mu      sync.Mutex
	calls   []enode.ID
	answers map[enode.ID]*enode.Node
}

func (t *dialTestResolver) setAnswers(m map[enode.ID]*enode.Node) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.answers = m
	t.calls = nil
}

func (t *dialTestResolver) checkCalls() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, id := range t.calls {
		if _, ok := t.answers[id]; !ok {
			return false
		}
	}
	return true
}

func (t *dialTestResolver) Resolve(n *enode.Node) *enode.Node {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.calls = append(t.calls, n.ID())
	return t.answers[n.ID()]
}

// TestDialSchedStaticIgnoresGroupOccupancy — static (addnode-style)
// dials are operator-chosen and exempt from the outbound network-group
// diversity rule, as Core's manual connections are. Before the
// exemption, a dynamic peer occupying the static node's /16 (the
// docker-bridge 172.17.0.0/16 case) pushed the static task out of the
// pool with no event to ever bring it back.
func TestDialSchedStaticIgnoresGroupOccupancy(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	runDialTest(t, config, []dialTestRound{
		{
			peersAdded: []*conn{
				{flags: dynDialedConn, node: newNode(uintID(0x01), "172.17.0.2:32110")},
			},
			update: func(d *dialScheduler) {
				d.addStatic(newNode(uintID(0x02), "172.17.0.3:32110"))
			},
			wantNewDials: []*enode.Node{
				newNode(uintID(0x02), "172.17.0.3:32110"),
			},
		},
	})
}

// TestDialSchedStaticRecoversAfterBanExpiry — a static node rejected
// by the ban gate must be redialed once the ban lifts. Ban expiry
// produces no scheduler event, so recovery rides on the periodic
// static-pool resweep (staticPoolResweepInterval); each test round
// advances the simulated clock 16s, so the 60s resweep fires during
// round 3's clock run and the dial surfaces in round 4.
func TestDialSchedStaticRecoversAfterBanExpiry(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		banned = true
	)
	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
		isBanned: func(ip net.IP) bool {
			mu.Lock()
			defer mu.Unlock()
			return banned && ip.String() == "7.7.7.7"
		},
	}
	staticNode := newNode(uintID(0x50), "7.7.7.7:32110")
	runDialTest(t, config, []dialTestRound{
		// Round 0: the static node is banned — no dial.
		{
			update: func(d *dialScheduler) {
				d.addStatic(staticNode)
			},
		},
		// Round 1: the ban lifts silently. Still no dial until the
		// resweep timer fires.
		{
			update: func(*dialScheduler) {
				mu.Lock()
				banned = false
				mu.Unlock()
			},
		},
		{},
		{},
		// Round 4: the resweep re-pooled the task; it gets dialed.
		{
			wantNewDials: []*enode.Node{staticNode},
		},
	})
}

// TestDialSchedStaticRecoversAfterInboundFailure — a static task
// rejected with errInboundProgress must be redialed when the inbound
// handshake FAILS. A failed inbound never becomes a peer, so no
// peer-removed event fires for the ID; recovery rides on the
// inbound-progress clear hook re-checking the static pool.
func TestDialSchedStaticRecoversAfterInboundFailure(t *testing.T) {
	t.Parallel()

	config := dialConfig{
		maxActiveDials: 5,
		maxDialPeers:   5,
	}
	staticNode := newNode(uintID(0x51), "9.9.9.9:32110")
	runDialTest(t, config, []dialTestRound{
		// Round 0: an inbound conn claiming the static node's ID is
		// mid-handshake, so the freshly-added static task is rejected
		// out of the pool.
		{
			update: func(d *dialScheduler) {
				d.inboundProgressBegin(staticNode.ID())
				d.addStatic(staticNode)
			},
		},
		// Round 1: the inbound handshake fails (unregisters without
		// ever having produced a peer). The static task must come
		// back and get dialed.
		{
			update: func(d *dialScheduler) {
				d.inboundProgressEnd(staticNode.ID())
			},
			wantNewDials: []*enode.Node{staticNode},
		},
	})
}
