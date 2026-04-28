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
		tc := tc
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
