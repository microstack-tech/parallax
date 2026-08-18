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
	"errors"
	mrand "math/rand"
	"net"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
)

// Feeler / addrfetch defaults. Mirror Bitcoin Core constants.
const (
	// feelerInterval is the cadence of background feeler dials.
	// Bitcoin Core's FEELER_INTERVAL = 120s (src/net.h:61).
	feelerInterval = 2 * time.Minute

	// feelerLifetime is how long we keep a feeler peer connected
	// before disconnecting. Long enough for the v2 handshake +
	// disc-protocol Hello round-trip + addrman.Good ingest at
	// peer-attach time; short enough that the feeler doesn't
	// occupy a real outbound slot. Bitcoin Core's feeler does the
	// version exchange and disconnects immediately after; we
	// model the same behavior with a short timer.
	feelerLifetime = 10 * time.Second

	// addrFetchThreshold is the addrman size below which the
	// startup-time addrfetch warmup runs against bootstrap peers.
	// Mirrors Bitcoin Core's "if you have <1000 addrs, do
	// addrfetch" heuristic in src/net.cpp:2422 (ConnectionType::
	// ADDR_FETCH).
	addrFetchThreshold = 1000

	// addrFetchLifetime is how long an addrfetch peer is kept
	// connected to receive their `Peers` reply. The disc protocol
	// times out the GetPeers / Peers exchange in well under this.
	addrFetchLifetime = 30 * time.Second
)

// runFeeler is the background loop that periodically dials a single
// addrman entry to verify reachability without occupying an outbound
// slot. Kept addresses get their LastSuccess refreshed via
// addrman.Good (which runs at peer attach in the run loop, so the
// feeler doesn't need to call it directly). Failed dials get
// Attempt(failure=true).
//
// Mirrors Bitcoin Core's feeler logic in
// src/net.cpp:2796-2810 (selection: tried-collision first, then
// new-only) and the post-dial collision resolution at
// src/addrman.cpp ResolveCollisions_.
func (srv *Server) runFeeler() {
	defer srv.loopWG.Done()

	if srv.addrbook == nil || srv.NoDial {
		return
	}

	timer := time.NewTimer(feelerInterval + feelerJitter())
	defer timer.Stop()

	for {
		select {
		case <-srv.quit:
			return
		case <-timer.C:
			// Core only makes feeler connections once the regular
			// outbound targets are met (ThreadOpenConnections sets
			// fFeeler only in the slots-full branch) — below target
			// the effort goes to real connections, which test
			// addresses just as well. The tick still fires so the
			// cadence resumes as soon as the slots fill.
			if srv.outboundTargetsMet() {
				srv.runOneFeeler()
			}
			timer.Reset(feelerInterval + feelerJitter())
		}
	}
}

// outboundTargetsMet reports whether the full-relay and block-relay
// outbound slots are both at target, i.e. the node is in the state
// where Core would divert its connection effort to feeler probes.
func (srv *Server) outboundTargetsMet() bool {
	return srv.dialedOutboundCount() >= srv.maxDialedConns() &&
		srv.blockRelayOutboundCount() >= srv.maxBlockRelayDial()
}

// runOneFeeler picks one address and dials it. Caller is the
// feeler loop; safe to invoke directly from tests.
func (srv *Server) runOneFeeler() {
	if srv.addrbook == nil {
		return
	}
	// ResolveCollisions first so a stale collision tryset doesn't
	// shadow the actual Select draw. Bitcoin Core also runs this
	// on each ThreadOpenConnections iteration when the feeler tick
	// fires (src/net.cpp).
	srv.addrbook.ResolveCollisions()

	addr, ok := pickFeelerAddr(srv.addrbook)
	if !ok {
		return
	}
	// Skip networks the proxy policy has no route to — Core's feeler
	// also draws again when Select hands it an unreachable address.
	if !srv.NetworkReachable(addr.Network) {
		return
	}
	if srv.isSelfNetAddr(addr) {
		return
	}
	// Skip candidates a live peer already represents — by listen addr
	// (IP targets) or by dial target (any network). Core's feeler
	// also aborts on AlreadyConnectedToAddress.
	if tcp := tcpFromNetAddr(addr); tcp != nil && srv.alreadyConnectedTo(tcp) {
		// Refresh LastTry without counting a failure: we already
		// peer with this endpoint.
		srv.addrbook.Attempt(addr, false, time.Now())
		return
	}
	if srv.connectedToDialTarget(addr) {
		srv.addrbook.Attempt(addr, false, time.Now())
		return
	}

	// DialV2Feeler handles the full v2 handshake, runs the
	// post-handshake checks, calls addrmanGood at peer-attach time on
	// success, and records Attempt(failure=true) on a dial failure.
	// The feeler flag excludes the resulting peer from the dial
	// budget and suppresses the false addrman failure that its
	// deliberate disconnect would otherwise record.
	fd, err := srv.DialV2Feeler(addr)
	if err != nil {
		// Feeler dial failure isn't a hard error from the operator's
		// POV — the whole point is to probe addrs that may be
		// unreachable. Trace-level only.
		if !errors.Is(err, errV2DialCooldown) && !errors.Is(err, errV2DialSelf) {
			srv.log.Trace("feeler dial failed", "addr", addr, "err", err)
		}
		return
	}
	// Successful peer-attach already invoked addrmanGood. Schedule
	// a teardown so the feeler doesn't squat on an outbound slot.
	srv.closeFeelerAfter(fd, feelerLifetime)
}

// pickFeelerAddr returns one address to feeler-dial. Prefers
// tryCollision entries (test-before-evict) before falling back to
// a Select(new-only) draw. Mirrors Bitcoin Core's
// src/net.cpp:2796-2810 selection ordering.
func pickFeelerAddr(book *addrman.AddrMan) (addrman.NetAddr, bool) {
	if a, _, ok := book.SelectTriedCollision(); ok {
		return a, true
	}
	if a, _, ok := book.Select(true /* newOnly */, nil); ok {
		return a, true
	}
	return addrman.NetAddr{}, false
}

// closeFeelerAfter tears a feeler probe down after its lifetime by
// closing its transport conn. Closing the fd is the one teardown that
// works on every network: a proxied onion peer exposes no (IP,
// listen-port) an address-keyed disconnect could match, and the
// feelerConn flag already suppresses the addrman failure the abrupt
// close would otherwise record. Cancelled on server stop (the peer
// teardown path owns the conn then).
func (srv *Server) closeFeelerAfter(fd net.Conn, after time.Duration) {
	timer := time.AfterFunc(after, func() { fd.Close() })
	go func() {
		select {
		case <-srv.quit:
			timer.Stop()
		case <-time.After(after):
		}
	}()
}

// feelerJitter returns a small random offset added to feelerInterval
// so a fleet of peers running on the same wall-clock tick don't all
// feeler-dial the network at the same instant. Bitcoin Core uses
// PoissonNextSend; we use a uniform [0, 30s) which is good enough
// for 2-minute spacing.
func feelerJitter() time.Duration {
	return time.Duration(mrand.Int63n(int64(30 * time.Second)))
}

// tcpFromNetAddr projects an addrman NetAddr onto a *net.TCPAddr for
// the IP-keyed helper surface (netrestrict, banlist, self-endpoint,
// dedup, netgroup). Returns nil for non-IP networks — callers treat
// nil as "this check does not apply".
func tcpFromNetAddr(addr addrman.NetAddr) *net.TCPAddr {
	switch addr.Network {
	case addrman.NetIPv4:
		b := addr.Bytes()
		if len(b) != 4 {
			return nil
		}
		return &net.TCPAddr{IP: net.IPv4(b[0], b[1], b[2], b[3]), Port: int(addr.Port)}
	case addrman.NetIPv6:
		b := addr.Bytes()
		if len(b) != 16 {
			return nil
		}
		return &net.TCPAddr{IP: append(net.IP(nil), b...), Port: int(addr.Port)}
	}
	return nil
}

// runAddrFetch is the cold-start one-shot variant of feeler:
// when the addrman has fewer than addrFetchThreshold entries on
// startup, dial the bootstrap peers (BootstrapNodesV2) and let the
// disc protocol's outbound-greeting GetPeers warm the addrbook.
// Disconnects after addrFetchLifetime.
//
// Mirrors Bitcoin Core src/net.cpp:2422 ConnectionType::ADDR_FETCH:
// "very-short-lived connections used for testing addresses".
func (srv *Server) runAddrFetch() {
	defer srv.loopWG.Done()

	if srv.addrbook == nil || srv.NoDial {
		return
	}
	if srv.addrbook.Size(nil, nil) >= addrFetchThreshold {
		return
	}
	// Bootstrap-only fetch — DNSSeeds resolution runs separately
	// in setupAddrMan and feeds gossip via the regular dial path.
	for _, na := range srv.BootstrapNodesV2 {
		select {
		case <-srv.quit:
			return
		default:
		}
		// Onion bootnodes ride the same loop once a Tor route
		// exists; unreachable networks are skipped like everywhere
		// else on the dial side.
		if !srv.NetworkReachable(na.Network) || srv.isSelfNetAddr(na) {
			continue
		}
		if tcp := tcpFromNetAddr(na); tcp != nil && srv.alreadyConnectedTo(tcp) {
			continue
		}
		// Best-effort: ignore errors. Each successful dial yields
		// a Peers exchange via the disc protocol's outbound
		// greeting (RequestPeers), which warms addrbook. Tagged as a
		// feeler so it neither occupies a real outbound slot nor
		// records a false addrman failure on its short-lived
		// disconnect.
		fd, err := srv.DialV2Feeler(na)
		if err != nil {
			srv.log.Trace("addrfetch dial failed", "addr", na, "err", err)
			continue
		}
		srv.closeFeelerAfter(fd, addrFetchLifetime)
	}
}
