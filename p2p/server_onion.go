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
	"fmt"
	"net"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/torcontrol"
)

// startTorControl launches the controller that maintains our onion
// service (PIP-0007 §3). Called from Start once the listener is bound;
// the service targets the live listen port, while the advertised
// virtual port stays the network default so non-standard-port nodes
// aren't decloaked (Core parity).
func (srv *Server) startTorControl() {
	tcpAddr, ok := srv.listener.Addr().(*net.TCPAddr)
	if !ok {
		srv.log.Warn("torcontrol: listener has no TCP address; onion service disabled")
		return
	}
	virtualPort := srv.OnionVirtualPort
	if virtualPort == 0 {
		virtualPort = DNSSeedDefaultPort
	}
	srv.torControl = torcontrol.New(torcontrol.Config{
		ControlAddr: srv.TorControlAddr,
		Password:    srv.TorPassword,
		KeyFile:     srv.OnionKeyPath,
		VirtualPort: virtualPort,
		Target:      fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port),
		// Auto-configure the onion proxy from the daemon's SOCKS
		// listener only when --onion named none — Core gates the
		// GETINFO on -onion being unset, so an explicit --onion (or
		// the --onion=0 refusal) always wins.
		FetchSocks:      srv.OnionProxyAddr == "",
		OnSocksListener: srv.setAutoOnionProxy,
		OnService:       func(id string) { srv.onOnionService(id, virtualPort) },
		OnDisconnected:  srv.onOnionLost,
		Log:             srv.log,
	})
	srv.torControl.Start()
}

// setAutoOnionProxy installs the torcontrol-discovered SOCKS listener
// as the onion route (PIP-0007 §1.3 auto-proxy, Core's get_socks_cb).
// Copy-on-write onto the atomic policy holder: readers never see a
// half-updated policy. Once set it is never revoked on control-port
// loss — Core keeps the proxy too, and dials just fail naturally if
// the daemon is gone. Runs on the torcontrol goroutine, which is the
// only writer after Start.
func (srv *Server) setAutoOnionProxy(addr string) {
	cur := srv.policy()
	if cur == nil {
		return
	}
	if pr := cur.proxyFor(addrman.NetTorV3); pr != nil && pr.Addr == addr {
		// Reconnected session reporting the same listener — the
		// route (and its isolation generator) stays as it is.
		return
	}
	next, err := cur.withAutoOnionProxy(addr)
	if err != nil {
		srv.log.Warn("torcontrol: onion proxy auto-configuration failed", "addr", addr, "err", err)
		return
	}
	srv.netpol.Store(next)
	srv.log.Info("torcontrol: onion proxy auto-configured",
		"proxy", addr, "reachable", next.isReachable(addrman.NetTorV3))
	if next.isReachable(addrman.NetTorV3) && !cur.isReachable(addrman.NetTorV3) {
		srv.onNetworkNowReachable(addrman.NetTorV3)
	}
}

// onNetworkNowReachable replays the bootstrap work that Start had to
// skip because the network had no route yet. The auto-proxy resolves
// asynchronously — after setupAddrMan, replayAnchors and runAddrFetch
// have already run — so in the default configuration (--listenonion,
// no explicit --onion) every onion bootnode and every onion anchor
// would otherwise be discarded, leaving an onion-capable node with
// nothing to dial. Safe to run repeatedly: addrman ingest dedupes and
// the addrfetch is threshold-gated.
func (srv *Server) onNetworkNowReachable(net addrman.NetID) {
	if srv.addrbook != nil {
		now := time.Now()
		ingested := 0
		for _, addr := range srv.BootstrapNodesV2 {
			if addr.Network != net {
				continue
			}
			if addrman.IngestV2NetAddr(srv.addrbook, addr, addrman.SourceDNSSeed, now) {
				ingested++
			}
		}
		if ingested > 0 {
			srv.log.Info("ingested bootstrap nodes for newly reachable network",
				"network", net, "count", ingested)
		}
	}

	srv.pendingAnchorsMu.Lock()
	var replay []addrman.NetAddr
	kept := srv.pendingAnchors[:0]
	for _, a := range srv.pendingAnchors {
		if a.Network == net {
			replay = append(replay, a)
		} else {
			kept = append(kept, a)
		}
	}
	srv.pendingAnchors = kept
	srv.pendingAnchorsMu.Unlock()

	for _, a := range replay {
		if srv.NoDial {
			break
		}
		srv.log.Info("anchors: replaying deferred anchor", "addr", a)
		srv.loopWG.Add(1)
		go func() {
			defer srv.loopWG.Done()
			if err := srv.DialV2BlockRelay(a); err != nil {
				srv.log.Trace("deferred anchor dial failed", "addr", a, "err", err)
			}
		}()
	}

	// Cold start: the one-shot addrfetch already ran (and skipped
	// this network's bootnodes), so give it another pass now that
	// they are dialable. runAddrFetch re-checks the threshold and
	// returns immediately once the addrbook has filled.
	if srv.addrbook != nil && len(srv.BootstrapNodesV2) > 0 {
		srv.loopWG.Add(1)
		go srv.runAddrFetch()
	}
}

// onOnionService records the established service address and notifies
// the wiring layer. Runs on the torcontrol goroutine.
func (srv *Server) onOnionService(serviceID string, virtualPort uint16) {
	na, err := addrman.ParseOnion(serviceID+".onion", virtualPort)
	if err != nil {
		srv.log.Warn("torcontrol: unparseable service ID from control port", "service", serviceID, "err", err)
		return
	}
	srv.onionSelfMu.Lock()
	srv.onionSelf = na
	srv.onionSelfSet = true
	srv.onionSelfMu.Unlock()
	if srv.OnOnionService != nil {
		srv.OnOnionService(na)
	}
}

// onOnionLost clears the service address when the control connection
// drops — Tor discards the ephemeral service with it, so continuing to
// advertise would gossip a dead endpoint.
func (srv *Server) onOnionLost() {
	srv.onionSelfMu.Lock()
	srv.onionSelf = addrman.NetAddr{}
	srv.onionSelfSet = false
	srv.onionSelfMu.Unlock()
	if srv.OnOnionLost != nil {
		srv.OnOnionLost()
	}
}

// OnionService returns the node's onion service address while one is
// established.
func (srv *Server) OnionService() (addrman.NetAddr, bool) {
	srv.onionSelfMu.Lock()
	defer srv.onionSelfMu.Unlock()
	return srv.onionSelf, srv.onionSelfSet
}

// isSelfOnion reports whether addr names our own onion service.
// Port-insensitive: dialing our own service on any port still loops
// back to our listener.
func (srv *Server) isSelfOnion(addr addrman.NetAddr) bool {
	if addr.Network != addrman.NetTorV3 {
		return false
	}
	srv.onionSelfMu.Lock()
	defer srv.onionSelfMu.Unlock()
	if !srv.onionSelfSet {
		return false
	}
	self := srv.onionSelf
	self.Port = addr.Port
	return self.Equal(addr)
}

// classifyInboundNetwork stamps the onion flag on inbound connections
// arriving from loopback while our onion service is active — the Tor
// daemon delivers rendezvous streams from 127.0.0.1, and
// distinguishing them from genuine co-hosted peers is impossible at
// the address level, so the active-service heuristic is the best
// available signal (Core's CNode::ConnectedThroughNetwork). PIP-0007
// §3.2: onion-classified peers keep localhost eviction protection and
// their YourAddr observations never feed the self-address quorum.
func (srv *Server) classifyInboundNetwork(fd net.Conn, flags connFlag) connFlag {
	if flags&inboundConn == 0 {
		return flags
	}
	ra, ok := fd.RemoteAddr().(*net.TCPAddr)
	if !ok || !ra.IP.IsLoopback() {
		return flags
	}
	if _, active := srv.OnionService(); active {
		flags |= onionConn
	}
	return flags
}
