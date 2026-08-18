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
		ControlAddr:    srv.TorControlAddr,
		Password:       srv.TorPassword,
		KeyFile:        srv.OnionKeyPath,
		VirtualPort:    virtualPort,
		Target:         fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port),
		OnService:      func(id string) { srv.onOnionService(id, virtualPort) },
		OnDisconnected: srv.onOnionLost,
		Log:            srv.log,
	})
	srv.torControl.Start()
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
