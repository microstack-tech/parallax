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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/netparams"
	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
	"gopkg.in/urfave/cli.v1"
)

// parallaxDiscCrawlerCommand drives the multi-hop crawler. It loads
// state from --state, seeds the work queue from that state plus
// --bootnodes (each entry being either ip:port or enode://... —
// auto-detected), runs --parallelism workers calling probeOne, ingests
// every Peers reply back into the queue, and saves state every
// --save-interval (and at exit). Designed to be run as a long-lived
// service writing to the same JSON file across restarts.
//
// The walker probes the v2 (BIP324) transport exclusively — entries
// gossiped with a legacy secp256k1 identity are still dialed as v2,
// with the gossiped identity kept in state as data. Crawling the
// legacy v1.x network is the discv4 crawler's job.
var parallaxDiscCrawlerCommand = cli.Command{
	Name: "crawl",
	Usage: "Multi-hop crawl of the parallax-disc/1 network. Loads state from --state, " +
		"probes each known node + every peer learned via gossip, saves stats per node.",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "state",
			Usage: "Path to the crawl state JSON file (loaded on start, written at --save-interval and on exit). \"-\" for stdout-only at exit.",
			Value: "parallax-disc.json",
		},
		cli.StringFlag{
			Name: "bootnodes",
			Usage: "Comma-separated seed addresses (ip:port or enode://...). Added to the queue alongside loaded state. " +
				"Defaults to netparams.MainnetBootnodesV2 when unset and --state has no entries.",
			Value: "",
		},
		cli.IntFlag{
			Name:  "parallelism",
			Usage: "Number of concurrent probe workers.",
			Value: 16,
		},
		cli.DurationFlag{
			Name:  "timeout",
			Usage: "Maximum total wall-clock time for the crawl. The walker also exits early if the queue drains.",
			Value: 30 * time.Minute,
		},
		cli.DurationFlag{
			Name:  "save-interval",
			Usage: "How often to flush state to disk during the run.",
			Value: 5 * time.Minute,
		},
		cli.DurationFlag{
			Name: "reprobe-interval",
			Usage: "How long to wait after the queue drains before re-enqueueing all known nodes for another pass. " +
				"Set to 0 to exit on the first drain (one-shot mode).",
			Value: 30 * time.Second,
		},
	},
	Action: parallaxDiscWalk,
}

// CrawlState is the on-disk state for the multi-hop walker, keyed by
// (network/ip/port) so v2 nodes (which lack a stable NodeID) and
// legacy nodes share one address space.
type CrawlState struct {
	UpdatedAt time.Time             `json:"updatedAt"`
	Nodes     map[string]*CrawlNode `json:"nodes"`
}

// nodeKey is the canonical string key under which a CrawlNode lives in
// CrawlState.Nodes. Stable form: "<netID>/<ip>/<port>". netID lets us
// keep IPv4 and IPv6 separated cleanly even when their textual forms
// would collide for some addresses.
func nodeKey(n *CrawlNode) string {
	return fmt.Sprintf("%d/%s/%d", n.NetworkID, n.IP, n.TCPPort)
}

// loadState reads a CrawlState from path. Missing file → empty state
// (cold-start). Returns an error only if the file exists but cannot be
// parsed; callers should treat that as fatal so a corrupt file isn't
// silently overwritten.
func loadState(path string) (*CrawlState, error) {
	if path == "" || path == "-" {
		return &CrawlState{Nodes: map[string]*CrawlNode{}}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &CrawlState{Nodes: map[string]*CrawlNode{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %q: %w", path, err)
	}
	if len(data) == 0 {
		return &CrawlState{Nodes: map[string]*CrawlNode{}}, nil
	}
	var st CrawlState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state %q: %w", path, err)
	}
	if st.Nodes == nil {
		st.Nodes = map[string]*CrawlNode{}
	}
	return &st, nil
}

// saveState writes the CrawlState atomically (write to temp + rename),
// matching the pattern in cmd/devp2p/nodeset.go. path "-" writes to
// stdout. The serialized form is sorted by node key for deterministic
// diffs across runs.
func saveState(path string, st *CrawlState) error {
	st.UpdatedAt = time.Now()
	enc, err := marshalSorted(st)
	if err != nil {
		return err
	}
	if path == "" || path == "-" {
		_, err = os.Stdout.Write(enc)
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o644); err != nil {
		return fmt.Errorf("write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %q: %w", path, err)
	}
	return nil
}

// marshalSorted marshals st with node keys sorted, so successive runs
// produce a diffable file.
func marshalSorted(st *CrawlState) ([]byte, error) {
	keys := make([]string, 0, len(st.Nodes))
	for k := range st.Nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type sortedState struct {
		UpdatedAt time.Time              `json:"updatedAt"`
		Nodes     []*serializedCrawlNode `json:"nodes"`
	}
	out := sortedState{UpdatedAt: st.UpdatedAt}
	for _, k := range keys {
		n := st.Nodes[k]
		out.Nodes = append(out.Nodes, &serializedCrawlNode{
			Key:       k,
			CrawlNode: n,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// serializedCrawlNode wraps CrawlNode with its key so the on-disk form
// is a stable array (vs a map whose iteration order Go does not pin).
// On load we reconstruct the map; saved files round-trip cleanly via
// loadState's UnmarshalJSON path.
type serializedCrawlNode struct {
	Key string `json:"key"`
	*CrawlNode
}

// UnmarshalJSON for CrawlState rebuilds the Nodes map from the sorted
// array form produced by marshalSorted.
func (s *CrawlState) UnmarshalJSON(data []byte) error {
	var raw struct {
		UpdatedAt time.Time              `json:"updatedAt"`
		Nodes     []*serializedCrawlNode `json:"nodes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.UpdatedAt = raw.UpdatedAt
	s.Nodes = make(map[string]*CrawlNode, len(raw.Nodes))
	for _, sn := range raw.Nodes {
		if sn == nil || sn.CrawlNode == nil {
			continue
		}
		s.Nodes[sn.Key] = sn.CrawlNode
	}
	return nil
}

// walker drives the multi-hop crawl. One walker per `parallax-disc
// crawl` invocation; not safe to run two against the same state file.
type walker struct {
	state *CrawlState
	stMu  sync.Mutex // guards state.Nodes mutation

	seen   sync.Map        // string key -> struct{} (dedup across the run)
	todoCh chan *CrawlNode // bounded buffer; overflow drops and warns

	outstanding int64 // atomic — pending probes (queued + in-flight)

	// Per-pass counters, reset by requeueAll. Atomics — bumped from
	// worker goroutines outside stMu.
	passStart  time.Time
	passProbes int64
	passOK     int64
	passFail   int64
	passNew    int64

	parallelism     int
	saveInterval    time.Duration
	reprobeInterval time.Duration // 0 = one-shot, exit on first drain
	stateFile       string
}

// logPassSummary emits the one-line operator view of a completed pass:
// probe outcomes, queue growth, and how much of the state currently
// clears the seeder reliability gate (the number dns-seed compile will
// see, before its address-shape filters).
func (w *walker) logPassSummary() {
	w.stMu.Lock()
	total := len(w.state.Nodes)
	good := 0
	for _, n := range w.state.Nodes {
		if n.isGood() {
			good++
		}
	}
	w.stMu.Unlock()
	logging.Info("parallax-disc pass complete",
		"probed", atomic.LoadInt64(&w.passProbes),
		"ok", atomic.LoadInt64(&w.passOK),
		"failed", atomic.LoadInt64(&w.passFail),
		"new", atomic.LoadInt64(&w.passNew),
		"nodes", total,
		"good", good,
		"elapsed", time.Since(w.passStart).Round(time.Second))
}

// run executes the crawl. Returns when ctx is cancelled, the timeout
// fires, or the queue fully drains.
func (w *walker) run(ctx context.Context) error {
	var workersWG sync.WaitGroup
	idleCh := make(chan struct{}, 1) // workers ping when outstanding hits 0

	// Workers.
	for i := 0; i < w.parallelism; i++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			w.workerLoop(ctx, idleCh)
		}()
	}

	// Periodic save.
	saveTicker := time.NewTicker(w.saveInterval)
	defer saveTicker.Stop()

	// Main loop: wait for either the queue to drain (via idleCh + recheck),
	// for the periodic save tick, or for ctx cancellation.
	for {
		select {
		case <-ctx.Done():
			workersWG.Wait()
			_ = w.flush()
			w.logPassSummary()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil // soft exit on timeout
			}
			return ctx.Err()
		case <-saveTicker.C:
			_ = w.flush()
		case <-idleCh:
			// Re-check under no-lock. outstanding is monotonic w.r.t.
			// the in-flight cycle; we just want to confirm the drain
			// is real (not a transient race where a worker enqueues
			// new work between the decrement and our read).
			if atomic.LoadInt64(&w.outstanding) != 0 {
				continue
			}
			// Give workers a brief grace window in case the last
			// reply enqueues new work.
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
			if atomic.LoadInt64(&w.outstanding) != 0 {
				continue
			}
			// Drain confirmed. In one-shot mode (reprobeInterval==0)
			// exit now. Otherwise sleep for reprobeInterval, then
			// re-seen-clear and re-enqueue every known node so the
			// walker keeps probing for the full --timeout window.
			w.logPassSummary()
			if w.reprobeInterval <= 0 {
				close(w.todoCh)
				workersWG.Wait()
				return w.flush()
			}
			_ = w.flush() // flush before the sleep so on-disk state is fresh
			logging.Debug("parallax-disc queue drained",
				"reprobeIn", w.reprobeInterval)
			select {
			case <-ctx.Done():
				workersWG.Wait()
				_ = w.flush()
				return nil
			case <-time.After(w.reprobeInterval):
			}
			w.requeueAll(ctx)
		}
	}
}

// requeueAll clears the per-run dedup set and re-enqueues every node
// in state. Used by the run loop's drain path when reprobeInterval > 0
// — keeps the walker probing until the timeout fires.
func (w *walker) requeueAll(ctx context.Context) {
	w.seen = sync.Map{}
	w.passStart = time.Now()
	atomic.StoreInt64(&w.passProbes, 0)
	atomic.StoreInt64(&w.passOK, 0)
	atomic.StoreInt64(&w.passFail, 0)
	atomic.StoreInt64(&w.passNew, 0)
	w.stMu.Lock()
	nodes := make([]*CrawlNode, 0, len(w.state.Nodes))
	for _, n := range w.state.Nodes {
		nodes = append(nodes, n)
	}
	w.stMu.Unlock()
	logging.Info("parallax-disc reprobing all known nodes", "nodes", len(nodes))
	for _, n := range nodes {
		w.registerAndEnqueue(ctx, n)
	}
}

// workerLoop pulls from todoCh until the channel is closed or ctx
// fires.
func (w *walker) workerLoop(ctx context.Context, idleCh chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-w.todoCh:
			if !ok {
				return // channel closed by main loop on drain
			}
			w.probeAndUpdate(ctx, n)
			if atomic.AddInt64(&w.outstanding, -1) == 0 {
				select {
				case idleCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

// probeAndUpdate runs a single probe and writes results back into
// state. New entries learned from the Peers reply are deduped via
// `seen` and enqueued for further probing.
func (w *walker) probeAndUpdate(ctx context.Context, n *CrawlNode) {
	now := time.Now()

	// Stamp the attempt before we start so timeouts/panics still record
	// activity. We take the lock briefly and release before the network
	// I/O so other workers can update other nodes concurrently.
	w.stMu.Lock()
	cur := w.state.Nodes[nodeKey(n)]
	if cur == nil {
		// Should not happen — registerNode runs before enqueue — but
		// defend against it.
		cur = n
		w.state.Nodes[nodeKey(n)] = cur
	}
	if cur.FirstSeen.IsZero() {
		cur.FirstSeen = now
	}
	// The reliability windows age on the gap between attempts, so the
	// previous attempt time must be captured before this one stamps it.
	prevAttempt := cur.LastAttempt
	cur.LastAttempt = now
	w.stMu.Unlock()

	// The walker measures the v2 disc network only — legacy RLPx
	// crawling is a separate tool (discv4). Gossiped KeyType/NodeID
	// stay in state as observed data, but every dial goes through the
	// v2 handshake, which needs no prior identity. Dialing legacy here
	// also breaks on stale gossip: relayed secp256k1 entries outlive a
	// node's re-key or v2-only switch, and the RLPx handshake then
	// fails forever with EOF against a perfectly reachable node.
	target := *n
	target.KeyType = disc.KeyTypeNone
	target.NodeID = ""
	logging.Debug("parallax-disc probing",
		"addr", n.tcpAddr(),
		"gossipKeyType", n.KeyType)
	atomic.AddInt64(&w.passProbes, 1)
	peers, caps, err := probeOne(ctx, &target)

	w.stMu.Lock()
	cur = w.state.Nodes[nodeKey(n)]
	if err != nil {
		cur.FailCount++
		cur.updateStats(false, now, prevAttempt)
		cur.LastError = err.Error()
		rel2h := cur.Stat2H.Reliability
		w.stMu.Unlock()
		atomic.AddInt64(&w.passFail, 1)
		logging.Info("parallax-disc probe failed",
			"addr", n.tcpAddr(), "rel2h", rel2h, "err", err)
		return
	}
	cur.SuccessCount++
	cur.updateStats(true, now, prevAttempt)
	cur.LastSuccess = time.Now()
	cur.LastError = ""
	// The probe just completed a v2 (BIP324) handshake first-hand, so
	// record the identity as v2-native regardless of how the entry was
	// gossiped. Stale secp256k1 gossip otherwise pins a dual-stack node
	// to KeyType=0x01 and keeps it out of the v2 DNS seed zone forever.
	cur.KeyType = disc.KeyTypeNone
	cur.NodeID = ""
	if len(caps) > 0 {
		cs := make([]string, 0, len(caps))
		for _, c := range caps {
			cs = append(cs, c.String())
		}
		cur.Capabilities = cs
	}
	rel2h := cur.Stat2H.Reliability
	w.stMu.Unlock()
	atomic.AddInt64(&w.passOK, 1)

	logging.Info("parallax-disc probe ok",
		"addr", n.tcpAddr(), "rel2h", rel2h,
		"peers", len(peers), "caps", len(caps))

	enqueued, skipped := 0, 0
	for _, e := range peers {
		cn, ok := peerEntryToCrawlNode(e)
		if !ok {
			skipped++
			continue
		}
		if !isDialableIP(net.ParseIP(cn.IP)) {
			skipped++
			continue
		}
		w.registerAndEnqueue(ctx, cn)
		enqueued++
	}
	if enqueued+skipped > 0 {
		logging.Debug("parallax-disc fanout",
			"from", n.tcpAddr(),
			"enqueued", enqueued, "skipped", skipped)
	}
}

// registerAndEnqueue adds cn to state (if new) and pushes it to the
// queue. Dedup is keyed on nodeKey(cn).
func (w *walker) registerAndEnqueue(ctx context.Context, cn *CrawlNode) {
	key := nodeKey(cn)
	if _, loaded := w.seen.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	w.stMu.Lock()
	if existing, ok := w.state.Nodes[key]; ok {
		// Already known across a prior run — keep its stats, but
		// refresh identity fields in case (KeyType, NodeID) have
		// drifted (e.g. node migrated from v1.x to v2.x). One-way
		// only: never downgrade a v2-native entry back to legacy on
		// gossip's say-so. v2 capability was asserted by the operator
		// (ip:port seed) or verified first-hand by a successful probe,
		// while relayed secp256k1 entries outlive a node's v2 switch —
		// dual-stack nodes are permanently gossiped as legacy by their
		// v1.x peers.
		existing.NetworkID = cn.NetworkID
		existing.IP = cn.IP
		existing.TCPPort = cn.TCPPort
		if existing.KeyType != disc.KeyTypeNone {
			existing.KeyType = cn.KeyType
			existing.NodeID = cn.NodeID
		}
		cn = existing
	} else {
		w.state.Nodes[key] = cn
		atomic.AddInt64(&w.passNew, 1)
		logging.Debug("parallax-disc new node", "addr", cn.tcpAddr(),
			"gossipKeyType", cn.KeyType)
	}
	w.stMu.Unlock()

	atomic.AddInt64(&w.outstanding, 1)
	select {
	case <-ctx.Done():
		atomic.AddInt64(&w.outstanding, -1)
	case w.todoCh <- cn:
	default:
		// Queue overflow — drop. The node is already in state; a
		// future run will re-probe it.
		atomic.AddInt64(&w.outstanding, -1)
		logging.Warn("parallax-disc queue full, dropping",
			"addr", cn.tcpAddr(), "depth", len(w.todoCh))
	}
}

// flush writes the current state to disk (if --state is a real path).
// Holds the state mutex so the snapshot is consistent.
func (w *walker) flush() error {
	w.stMu.Lock()
	defer w.stMu.Unlock()
	if err := saveState(w.stateFile, w.state); err != nil {
		logging.Error("parallax-disc state save failed",
			"path", w.stateFile, "err", err)
		return err
	}
	if w.stateFile != "" && w.stateFile != "-" {
		logging.Info("parallax-disc state saved",
			"path", w.stateFile, "nodes", len(w.state.Nodes))
	}
	return nil
}

// peerEntryToCrawlNode converts a gossiped PeerEntry to a CrawlNode the
// walker can probe. Skips entries we can't dial (Tor/I2P/CJDNS, invalid
// fields). Returns ok=false on skip.
func peerEntryToCrawlNode(e disc.PeerEntry) (*CrawlNode, bool) {
	skip, err := e.Validate()
	if skip || err != nil {
		return nil, false
	}
	var ip string
	switch e.NetworkID {
	case disc.NetIPv4, disc.NetIPv6:
		ip = net.IP(e.Addr).String()
	default:
		// Tor v3 / I2P / CJDNS — not dialable from a generic crawler.
		return nil, false
	}
	cn := &CrawlNode{
		NetworkID: e.NetworkID,
		IP:        ip,
		TCPPort:   e.TCPPort,
		KeyType:   e.KeyType,
	}
	if e.KeyType == disc.KeyTypeSecp256k1 && len(e.NodeID) == 64 {
		cn.NodeID = hex.EncodeToString(e.NodeID)
	}
	return cn, true
}

// isDialableIP returns false for addresses we should never queue:
// unspecified, loopback (avoids self-probing in a single-host setup),
// link-local, multicast.
func isDialableIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return true
}

// parallaxDiscWalk is the `parallax-disc walk` action.
func parallaxDiscWalk(ctx *cli.Context) error {
	stateFile := ctx.String("state")
	state, err := loadState(stateFile)
	if err != nil {
		return err
	}

	w := &walker{
		state:           state,
		todoCh:          make(chan *CrawlNode, 65536),
		parallelism:     ctx.Int("parallelism"),
		saveInterval:    ctx.Duration("save-interval"),
		reprobeInterval: ctx.Duration("reprobe-interval"),
		stateFile:       stateFile,
	}
	if w.parallelism < 1 {
		w.parallelism = 1
	}
	w.passStart = time.Now()

	logging.Info("parallax-disc crawl starting",
		"state", stateFile,
		"known", len(state.Nodes),
		"parallelism", w.parallelism,
		"timeout", ctx.Duration("timeout"),
		"reprobe", w.reprobeInterval,
		"save", w.saveInterval)

	parentCtx, cancel := context.WithTimeout(context.Background(), ctx.Duration("timeout"))
	defer cancel()

	// Seed from existing state.
	for _, n := range state.Nodes {
		w.registerAndEnqueue(parentCtx, n)
	}
	// Seed from --bootnodes. When the flag is unset and the loaded
	// state is empty, fall back to the compiled-in v2 mainnet bootnode
	// list so a fresh crawler can cold-start without operator config —
	// the same list the daemon's v2 dial scheduler uses.
	bootRaw := ctx.String("bootnodes")
	bootList := strings.Split(bootRaw, ",")
	if strings.TrimSpace(bootRaw) == "" && len(state.Nodes) == 0 {
		bootList = netparams.MainnetBootnodesV2
		logging.Info("parallax-disc using default bootnodes",
			"source", "MainnetBootnodesV2", "count", len(bootList))
	}
	for _, raw := range bootList {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		seed, err := parseSeed(raw)
		if err != nil {
			logging.Warn("parallax-disc skipping invalid bootnode",
				"bootnode", raw, "err", err)
			continue
		}
		w.registerAndEnqueue(parentCtx, seed)
	}

	if atomic.LoadInt64(&w.outstanding) == 0 {
		return fmt.Errorf("no seeds: --bootnodes parsed empty, --state has no entries, and MainnetBootnodesV2 is empty")
	}

	if err := w.run(parentCtx); err != nil {
		return err
	}
	logging.Info("parallax-disc crawl finished", "nodes", len(state.Nodes))
	return nil
}
