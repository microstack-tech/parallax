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
	"sync"
	"sync/atomic"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/discover"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// parallaxENREntry is used to check for the "parallax" key in ENR records.
type parallaxENREntry struct {
	Rest []rlp.RawValue `rlp:"tail"`
}

func (e parallaxENREntry) ENRKey() string { return "parallax" }

// pipv2ENREntry tests for the "pipv2" key — the v2-transport
// capability flag set by nodes that accept BIP324-style handshakes.
// Used by the crawler to report v2 adoption across the discovered
// network.
type pipv2ENREntry struct {
	Rest []rlp.RawValue `rlp:"tail"`
}

func (e pipv2ENREntry) ENRKey() string { return "pipv2" }

// hasPipV2 reports whether n's ENR advertises v2-transport support.
func hasPipV2(n *enode.Node) bool {
	if n == nil {
		return false
	}
	var e pipv2ENREntry
	return n.Load(&e) == nil
}

type crawler struct {
	input     nodeSet
	output    nodeSet
	disc      resolver
	iters     []enode.Iterator
	inputIter enode.Iterator
	ch        chan *enode.Node
	closed    chan struct{}

	// settings
	revalidateInterval time.Duration
	mu                 sync.RWMutex
}

const (
	nodeRemoved = iota
	nodeSkipRecent
	nodeSkipIncompat
	nodeAdded
	nodeUpdated
)

type resolver interface {
	RequestENR(*enode.Node) (*enode.Node, error)
}

func newCrawler(input nodeSet, disc resolver, iters ...enode.Iterator) *crawler {
	c := &crawler{
		input:     input,
		output:    make(nodeSet, len(input)),
		disc:      disc,
		iters:     iters,
		inputIter: enode.IterNodes(input.nodes()),
		ch:        make(chan *enode.Node),
		closed:    make(chan struct{}),
	}
	c.iters = append(c.iters, c.inputIter)
	// Copy input to output initially. Any nodes that fail validation
	// will be dropped from output during the run.
	for id, n := range input {
		c.output[id] = n
	}
	return c
}

func (c *crawler) run(timeout time.Duration, nthreads int) nodeSet {
	var (
		timeoutTimer = time.NewTimer(timeout)
		timeoutCh    <-chan time.Time
		statusTicker = time.NewTicker(time.Second * 8)
		doneCh       = make(chan enode.Iterator, len(c.iters))
		liveIters    = len(c.iters)
	)
	if nthreads < 1 {
		nthreads = 1
	}
	defer timeoutTimer.Stop()
	defer statusTicker.Stop()
	for _, it := range c.iters {
		go c.runIterator(doneCh, it)
	}

	var (
		added   uint64
		updated uint64
		skipped uint64
		recent  uint64
		removed uint64
		wg      sync.WaitGroup
	)
	wg.Add(nthreads)
	for i := 0; i < nthreads; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case n := <-c.ch:
					switch c.updateNode(n) {
					case nodeSkipIncompat:
						atomic.AddUint64(&skipped, 1)
					case nodeSkipRecent:
						atomic.AddUint64(&recent, 1)
					case nodeRemoved:
						atomic.AddUint64(&removed, 1)
					case nodeAdded:
						atomic.AddUint64(&added, 1)
					default:
						atomic.AddUint64(&updated, 1)
					}
				case <-c.closed:
					return
				}
			}
		}()
	}

loop:
	for {
		select {
		case it := <-doneCh:
			if it == c.inputIter {
				// Enable timeout when we're done revalidating the input nodes.
				logging.Info("Revalidation of input set is done", "len", len(c.input))
				if timeout > 0 {
					timeoutCh = timeoutTimer.C
				}
			}
			if liveIters--; liveIters == 0 {
				break loop
			}
		case <-timeoutCh:
			break loop
		case <-statusTicker.C:
			logging.Info("Crawling in progress",
				"added", atomic.LoadUint64(&added),
				"updated", atomic.LoadUint64(&updated),
				"removed", atomic.LoadUint64(&removed),
				"ignored(recent)", atomic.LoadUint64(&recent),
				"ignored(incompatible)", atomic.LoadUint64(&skipped),
			)
		}
	}

	close(c.closed)
	for _, it := range c.iters {
		it.Close()
	}
	for ; liveIters > 0; liveIters-- {
		<-doneCh
	}
	wg.Wait()

	var v2Count int
	for _, n := range c.output {
		if hasPipV2(n.N) {
			v2Count++
		}
	}
	logging.Info("Crawl complete",
		"total", len(c.output),
		"pipv2", v2Count,
		"v1_only", len(c.output)-v2Count)

	return c.output
}

func (c *crawler) runIterator(done chan<- enode.Iterator, it enode.Iterator) {
	defer func() { done <- it }()
	for it.Next() {
		select {
		case c.ch <- it.Node():
		case <-c.closed:
			return
		}
	}
}

func (c *crawler) updateNode(n *enode.Node) int {
	c.mu.RLock()
	node, ok := c.output[n.ID()]
	c.mu.RUnlock()

	// Skip validation of recently-seen nodes.
	if ok && time.Since(node.LastCheck) < c.revalidateInterval {
		return nodeSkipRecent
	}

	// Request the node record.
	status := nodeUpdated
	node.LastCheck = truncNow()
	if nn, err := c.disc.RequestENR(n); err != nil {
		if node.Score == 0 {
			// Node doesn't implement EIP-868.
			logging.Debug("Skipping node", "id", n.ID())
			return nodeSkipIncompat
		}
		node.Score /= 2
	} else {
		// Filter out nodes that don't advertise the "parallax" protocol.
		var prlEntry parallaxENREntry
		if nn.Load(&prlEntry) != nil {
			logging.Debug("Skipping non-Parallax node", "id", n.ID(), "ip", n.IP())
			return nodeSkipIncompat
		}
		logging.Info("Found Parallax node", "id", n.ID(), "ip", n.IP())
		node.N = nn
		node.Seq = nn.Seq()
		node.Score++
		if node.FirstResponse.IsZero() {
			node.FirstResponse = node.LastCheck
			status = nodeAdded
		}
		node.LastResponse = node.LastCheck
	}

	// Store/update node in output set.
	c.mu.Lock()
	defer c.mu.Unlock()
	if node.Score <= 0 {
		logging.Debug("Removing node", "id", n.ID())
		delete(c.output, n.ID())
		return nodeRemoved
	}
	logging.Debug("Updating node", "id", n.ID(), "seq", n.Seq(), "score", node.Score)
	c.output[n.ID()] = node
	return status
}

func truncNow() time.Time {
	return time.Now().UTC().Truncate(1 * time.Second)
}

// parallaxBFSIterator is a discv4 iterator that performs a BFS walk over
// Parallax-only nodes by repeatedly issuing FINDNODE to known Parallax peers
// and yielding only ENR-verified Parallax nodes. It bypasses the local
// routing table entirely and is therefore immune to cross-chain pollution
// from incoming pings.
//
// The iterator never self-terminates: when its queue empties it sleeps for
// emptyRoundDelay and re-seeds from the original bootnode set, so the crawl
// keeps probing the network until Close is called (typically when the
// crawler hits its --timeout).
type parallaxBFSIterator struct {
	disc       *discover.UDPv4
	bootnodes  []*enode.Node
	targetsPer int

	out       chan *enode.Node
	closed    chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	cur      *enode.Node
	queue    []*enode.Node
	enqueued map[enode.ID]bool
	yielded  map[enode.ID]bool
}

const (
	bfsTargetsPerNode = 3
	bfsEmptyRoundWait = 5 * time.Second
)

// newParallaxBFSIterator constructs a BFS iterator seeded with the given
// bootnodes plus any input nodes that already carry the parallax ENR entry.
func newParallaxBFSIterator(disc *discover.UDPv4, bootnodes []*enode.Node, input nodeSet) *parallaxBFSIterator {
	it := &parallaxBFSIterator{
		disc:       disc,
		bootnodes:  bootnodes,
		targetsPer: bfsTargetsPerNode,
		out:        make(chan *enode.Node),
		closed:     make(chan struct{}),
		enqueued:   make(map[enode.ID]bool),
		yielded:    make(map[enode.ID]bool),
	}
	for _, b := range bootnodes {
		it.enqueueLocked(b)
	}
	for _, n := range input {
		if n.N == nil {
			continue
		}
		var entry parallaxENREntry
		if n.N.Load(&entry) == nil {
			it.enqueueLocked(n.N)
		}
	}
	go it.run()
	return it
}

// enqueueLocked appends a node to the queue if it has not been enqueued yet.
// Caller need not hold mu since the iterator is single-producer until run()
// is started; new appends after that take mu via enqueue.
func (it *parallaxBFSIterator) enqueueLocked(n *enode.Node) {
	if it.enqueued[n.ID()] {
		return
	}
	it.enqueued[n.ID()] = true
	it.queue = append(it.queue, n)
}

func (it *parallaxBFSIterator) enqueue(n *enode.Node) {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.enqueueLocked(n)
}

func (it *parallaxBFSIterator) popNext() (*enode.Node, bool) {
	it.mu.Lock()
	defer it.mu.Unlock()
	if len(it.queue) == 0 {
		return nil, false
	}
	n := it.queue[0]
	it.queue = it.queue[1:]
	return n, true
}

func (it *parallaxBFSIterator) reseed() {
	it.mu.Lock()
	defer it.mu.Unlock()
	// Allow bootnodes to be re-probed in the next round.
	for _, b := range it.bootnodes {
		delete(it.enqueued, b.ID())
		it.enqueueLocked(b)
	}
}

// run is the BFS goroutine. It probes Parallax nodes one at a time, sends
// FINDNODE for several random targets to discover more, and pushes verified
// Parallax nodes onto the out channel. It exits only when closed is closed.
func (it *parallaxBFSIterator) run() {
	defer close(it.out)
	for {
		// Pop next node, or sleep+re-seed if empty.
		next, ok := it.popNext()
		if !ok {
			select {
			case <-it.closed:
				return
			case <-time.After(bfsEmptyRoundWait):
			}
			it.reseed()
			continue
		}

		// Verify the candidate is actually a Parallax node by fetching its
		// fresh ENR. This single check protects against stale FINDNODE
		// entries and against the seed bootnodes lacking an ENR record.
		nn, err := it.disc.RequestENR(next)
		if err != nil {
			logging.Debug("BFS: RequestENR failed", "id", next.ID(), "ip", next.IP(), "err", err)
			continue
		}
		var entry parallaxENREntry
		if nn.Load(&entry) != nil {
			logging.Debug("BFS: skipping non-Parallax candidate", "id", nn.ID(), "ip", nn.IP())
			continue
		}

		// Yield (only on first sight). Subsequent re-seeds may revisit the
		// same node; we don't want to spam the consumer.
		it.mu.Lock()
		first := !it.yielded[nn.ID()]
		if first {
			it.yielded[nn.ID()] = true
		}
		it.mu.Unlock()
		if first {
			select {
			case it.out <- nn:
			case <-it.closed:
				return
			}
		}

		// Crawl: ask this Parallax node for its neighbors via FINDNODE for
		// targetsPer random keys. Each random target hits a different bucket
		// in the remote node's routing table.
		for i := 0; i < it.targetsPer; i++ {
			select {
			case <-it.closed:
				return
			default:
			}
			key, err := crypto.GenerateKey()
			if err != nil {
				continue
			}
			results, err := it.disc.FindNeighbors(nn, &key.PublicKey)
			if err != nil {
				logging.Debug("BFS: FindNeighbors failed", "id", nn.ID(), "ip", nn.IP(), "err", err)
				continue
			}
			for _, r := range results {
				it.enqueue(r)
			}
		}
	}
}

// Next implements enode.Iterator. It blocks until a verified Parallax node
// is available, the iterator is closed, or the BFS goroutine exits.
func (it *parallaxBFSIterator) Next() bool {
	select {
	case n, ok := <-it.out:
		if !ok {
			return false
		}
		it.mu.Lock()
		it.cur = n
		it.mu.Unlock()
		return true
	case <-it.closed:
		return false
	}
}

// Node implements enode.Iterator.
func (it *parallaxBFSIterator) Node() *enode.Node {
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.cur
}

// Close implements enode.Iterator. It signals the BFS goroutine to stop and
// causes Next to return false.
func (it *parallaxBFSIterator) Close() {
	it.closeOnce.Do(func() {
		close(it.closed)
	})
}
