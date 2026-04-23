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

package node

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/internal/debug"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/rpc"
	"github.com/ParallaxProtocol/parallax/util/hexutil"
)

// apis returns the collection of built-in RPC APIs.
func (n *Node) apis() []rpc.API {
	return []rpc.API{
		{
			Namespace: "admin",
			Version:   "1.0",
			Service:   &privateAdminAPI{n},
		}, {
			Namespace: "admin",
			Version:   "1.0",
			Service:   &publicAdminAPI{n},
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   debug.Handler,
		}, {
			Namespace: "web3",
			Version:   "1.0",
			Service:   &publicWeb3API{n},
			Public:    true,
		},
	}
}

// privateAdminAPI is the collection of administrative API methods exposed only
// over a secure RPC channel.
type privateAdminAPI struct {
	node *Node // Node interfaced by this API
}

// AddPeer requests connecting to a remote node, and also maintaining the new
// connection at all times, even reconnecting if it is lost.
func (api *privateAdminAPI) AddPeer(url string) (bool, error) {
	// Make sure the server is running, fail otherwise
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	// Try to add the url as a static peer and return
	node, err := enode.Parse(enode.ValidSchemes, url)
	if err != nil {
		return false, fmt.Errorf("invalid enode: %v", err)
	}
	server.AddPeer(node)
	return true, nil
}

// RemovePeer disconnects from a remote node if the connection exists
func (api *privateAdminAPI) RemovePeer(url string) (bool, error) {
	// Make sure the server is running, fail otherwise
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	// Try to remove the url as a static peer and return
	node, err := enode.Parse(enode.ValidSchemes, url)
	if err != nil {
		return false, fmt.Errorf("invalid enode: %v", err)
	}
	server.RemovePeer(node)
	return true, nil
}

// AddTrustedPeer allows a remote node to always connect, even if slots are full
func (api *privateAdminAPI) AddTrustedPeer(url string) (bool, error) {
	// Make sure the server is running, fail otherwise
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	node, err := enode.Parse(enode.ValidSchemes, url)
	if err != nil {
		return false, fmt.Errorf("invalid enode: %v", err)
	}
	server.AddTrustedPeer(node)
	return true, nil
}

// Addnode ingests an address into the addrman as an operator-pinned
// peer (source=manual). Accepts either plain `ip:port` (v2.0-native,
// KeyType=0x00) or the legacy `enode://<nodeID>@ip:port` form (v1.x,
// KeyType=0x01). Returns true if the entry was inserted or updated.
//
// PIP-0006 Phase 6 — mirrors Bitcoin Core's `addnode` RPC semantics.
// Manual entries persist across restarts, are exempt from the
// source-aware eviction, and are dialed before any other source in
// Select() via the manual chanceMultiplier.
func (api *privateAdminAPI) Addnode(address string) (bool, error) {
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	book := server.AddrBook()
	if book == nil {
		return false, errors.New("addrman is not enabled; start with --experimental-addrman")
	}
	entry, err := parseAddrbookAddress(address)
	if err != nil {
		return false, err
	}
	return book.Add([]addrman.Entry{entry}, entry.Addr, addrman.SourceManual, 0), nil
}

// Removenode drops an address from the addrman, regardless of which
// table holds it. PIP-0006 Phase 6 — inverse of Addnode.
func (api *privateAdminAPI) Removenode(address string) (bool, error) {
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	book := server.AddrBook()
	if book == nil {
		return false, errors.New("addrman is not enabled; start with --experimental-addrman")
	}
	entry, err := parseAddrbookAddress(address)
	if err != nil {
		return false, err
	}
	return book.Remove(entry.Addr), nil
}

// AddrbookStatus returns a Status snapshot for operator diagnostics.
// Read-only; PIP-0006 Phase 6.
func (api *privateAdminAPI) AddrbookStatus() (*addrman.Status, error) {
	server := api.node.Server()
	if server == nil {
		return nil, ErrNodeStopped
	}
	book := server.AddrBook()
	if book == nil {
		return nil, errors.New("addrman is not enabled; start with --experimental-addrman")
	}
	s := book.Snapshot()
	return &s, nil
}

// AddrbookResetKey regenerates the addrman's nKey and clears the tried
// table atomically. Operator-only; intended for cases where an nKey
// leak is credibly suspected. PIP-0006 Phase 6.
func (api *privateAdminAPI) AddrbookResetKey() (bool, error) {
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	book := server.AddrBook()
	if book == nil {
		return false, errors.New("addrman is not enabled; start with --experimental-addrman")
	}
	if err := book.ResetKey(); err != nil {
		return false, err
	}
	return true, nil
}

// parseAddrbookAddress accepts either a plain `ip:port` (v2.0-native)
// or the legacy `enode://<hex>@ip:port` form. Branches on the enode://
// prefix, matching the input format Bitcoin Core's addnode accepts.
func parseAddrbookAddress(s string) (addrman.Entry, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "enode://") {
		n, err := enode.ParseV4(s)
		if err != nil {
			return addrman.Entry{}, fmt.Errorf("invalid enode: %w", err)
		}
		ip := n.IP()
		if ip == nil || n.TCP() == 0 {
			return addrman.Entry{}, errors.New("enode missing ip or tcp port")
		}
		var net addrman.NetID
		var addrBytes []byte
		if v4 := ip.To4(); v4 != nil {
			net = addrman.NetIPv4
			addrBytes = v4
		} else {
			net = addrman.NetIPv6
			addrBytes = ip
		}
		naddr, err := addrman.NewNetAddr(net, addrBytes, uint16(n.TCP()))
		if err != nil {
			return addrman.Entry{}, err
		}
		pub := n.Pubkey()
		if pub == nil {
			return addrman.Entry{}, errors.New("enode missing pubkey")
		}
		return addrman.Entry{
			Addr:     naddr,
			KeyType:  0x01,
			NodeID:   pubkeyToNodeID(pub),
			LastSeen: time.Now(),
		}, nil
	}
	// Plain ip:port form.
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return addrman.Entry{}, fmt.Errorf("invalid address %q: %w", s, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return addrman.Entry{}, fmt.Errorf("invalid ip %q", host)
	}
	port, err := parsePort(portStr)
	if err != nil {
		return addrman.Entry{}, err
	}
	var netID addrman.NetID
	var addrBytes []byte
	if v4 := ip.To4(); v4 != nil {
		netID = addrman.NetIPv4
		addrBytes = v4
	} else {
		netID = addrman.NetIPv6
		addrBytes = ip
	}
	naddr, err := addrman.NewNetAddr(netID, addrBytes, port)
	if err != nil {
		return addrman.Entry{}, err
	}
	return addrman.Entry{Addr: naddr, KeyType: 0x00, LastSeen: time.Now()}, nil
}

// pubkeyToNodeID returns the 64-byte (x || y) encoding used by discv4
// and parallax-disc/1 for KeyType=0x01 entries. Matches the format
// produced by elliptic.Marshal minus the 0x04 prefix.
func pubkeyToNodeID(pub *ecdsa.PublicKey) []byte {
	//nolint:staticcheck // elliptic.Marshal remains the canonical
	// encoder for discv4/enode NodeIDs; secp256k1 is not provided by
	// crypto/ecdh so the linter's suggested replacement doesn't apply.
	b := elliptic.Marshal(pub.Curve, pub.X, pub.Y)
	if len(b) != 65 || b[0] != 0x04 {
		return nil
	}
	return b[1:]
}

func parsePort(s string) (uint16, error) {
	var p uint16
	if _, err := fmt.Sscanf(s, "%d", &p); err != nil || p == 0 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return p, nil
}

// RemoveTrustedPeer removes a remote node from the trusted peer set, but it
// does not disconnect it automatically.
func (api *privateAdminAPI) RemoveTrustedPeer(url string) (bool, error) {
	// Make sure the server is running, fail otherwise
	server := api.node.Server()
	if server == nil {
		return false, ErrNodeStopped
	}
	node, err := enode.Parse(enode.ValidSchemes, url)
	if err != nil {
		return false, fmt.Errorf("invalid enode: %v", err)
	}
	server.RemoveTrustedPeer(node)
	return true, nil
}

// Uptime returns the number of seconds the node has been running since its
// most recent Start. Returns 0 if the node is not yet running. Mirrors
// bitcoin-cli's `uptime` and backs the `parallax uptime` sugar command.
func (api *privateAdminAPI) Uptime() (uint64, error) {
	api.node.lock.Lock()
	startedAt := api.node.startedAt
	state := api.node.state
	api.node.lock.Unlock()
	if state != runningState || startedAt.IsZero() {
		return 0, nil
	}
	return uint64(time.Since(startedAt).Seconds()), nil
}

// Stop requests a graceful shutdown of the node. It returns immediately; the
// actual shutdown runs in a background goroutine so that the RPC response can
// be delivered to the caller before the RPC server is torn down.
func (api *privateAdminAPI) Stop() (bool, error) {
	go func() {
		// Give the RPC server a short grace period to flush the response.
		time.Sleep(100 * time.Millisecond)
		if err := api.node.Close(); err != nil && err != ErrNodeStopped {
			logging.Error("admin_stop: node close failed", "err", err)
		}
	}()
	return true, nil
}

// PeerEvents creates an RPC subscription which receives peer events from the
// node's p2p.Server
func (api *privateAdminAPI) PeerEvents(ctx context.Context) (*rpc.Subscription, error) {
	// Make sure the server is running, fail otherwise
	server := api.node.Server()
	if server == nil {
		return nil, ErrNodeStopped
	}

	// Create the subscription
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return nil, rpc.ErrNotificationsUnsupported
	}
	rpcSub := notifier.CreateSubscription()

	go func() {
		events := make(chan *p2p.PeerEvent)
		sub := server.SubscribeEvents(events)
		defer sub.Unsubscribe()

		for {
			select {
			case event := <-events:
				notifier.Notify(rpcSub.ID, event)
			case <-sub.Err():
				return
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// StartHTTP starts the HTTP RPC API server.
func (api *privateAdminAPI) StartHTTP(host *string, port *int, cors *string, apis *string, vhosts *string) (bool, error) {
	api.node.lock.Lock()
	defer api.node.lock.Unlock()

	// Determine host and port.
	if host == nil {
		h := DefaultHTTPHost
		if api.node.config.HTTPHost != "" {
			h = api.node.config.HTTPHost
		}
		host = &h
	}
	if port == nil {
		port = &api.node.config.HTTPPort
	}

	// Determine config.
	config := httpConfig{
		CorsAllowedOrigins: api.node.config.HTTPCors,
		Vhosts:             api.node.config.HTTPVirtualHosts,
		Modules:            api.node.config.HTTPModules,
	}
	if cors != nil {
		config.CorsAllowedOrigins = nil
		for _, origin := range strings.Split(*cors, ",") {
			config.CorsAllowedOrigins = append(config.CorsAllowedOrigins, strings.TrimSpace(origin))
		}
	}
	if vhosts != nil {
		config.Vhosts = nil
		for _, vhost := range strings.Split(*host, ",") {
			config.Vhosts = append(config.Vhosts, strings.TrimSpace(vhost))
		}
	}
	if apis != nil {
		config.Modules = nil
		for _, m := range strings.Split(*apis, ",") {
			config.Modules = append(config.Modules, strings.TrimSpace(m))
		}
	}

	if err := api.node.http.setListenAddr(*host, *port); err != nil {
		return false, err
	}
	if err := api.node.http.enableRPC(api.node.rpcAPIs, config); err != nil {
		return false, err
	}
	if err := api.node.http.start(); err != nil {
		return false, err
	}
	return true, nil
}

// StartRPC starts the HTTP RPC API server.
// Deprecated: use StartHTTP instead.
func (api *privateAdminAPI) StartRPC(host *string, port *int, cors *string, apis *string, vhosts *string) (bool, error) {
	logging.Warn("Deprecation warning", "method", "admin.StartRPC", "use-instead", "admin.StartHTTP")
	return api.StartHTTP(host, port, cors, apis, vhosts)
}

// StopHTTP shuts down the HTTP server.
func (api *privateAdminAPI) StopHTTP() (bool, error) {
	api.node.http.stop()
	return true, nil
}

// StopRPC shuts down the HTTP server.
// Deprecated: use StopHTTP instead.
func (api *privateAdminAPI) StopRPC() (bool, error) {
	logging.Warn("Deprecation warning", "method", "admin.StopRPC", "use-instead", "admin.StopHTTP")
	return api.StopHTTP()
}

// StartWS starts the websocket RPC API server.
func (api *privateAdminAPI) StartWS(host *string, port *int, allowedOrigins *string, apis *string) (bool, error) {
	api.node.lock.Lock()
	defer api.node.lock.Unlock()

	// Determine host and port.
	if host == nil {
		h := DefaultWSHost
		if api.node.config.WSHost != "" {
			h = api.node.config.WSHost
		}
		host = &h
	}
	if port == nil {
		port = &api.node.config.WSPort
	}

	// Determine config.
	config := wsConfig{
		Modules: api.node.config.WSModules,
		Origins: api.node.config.WSOrigins,
		// ExposeAll: api.node.config.WSExposeAll,
	}
	if apis != nil {
		config.Modules = nil
		for _, m := range strings.Split(*apis, ",") {
			config.Modules = append(config.Modules, strings.TrimSpace(m))
		}
	}
	if allowedOrigins != nil {
		config.Origins = nil
		for _, origin := range strings.Split(*allowedOrigins, ",") {
			config.Origins = append(config.Origins, strings.TrimSpace(origin))
		}
	}

	// Enable WebSocket on the server.
	server := api.node.wsServerForPort(*port, false)
	if err := server.setListenAddr(*host, *port); err != nil {
		return false, err
	}
	openApis, _ := api.node.GetAPIs()
	if err := server.enableWS(openApis, config); err != nil {
		return false, err
	}
	if err := server.start(); err != nil {
		return false, err
	}
	api.node.http.log.Info("WebSocket endpoint opened", "url", api.node.WSEndpoint())
	return true, nil
}

// StopWS terminates all WebSocket servers.
func (api *privateAdminAPI) StopWS() (bool, error) {
	api.node.http.stopWS()
	api.node.ws.stop()
	return true, nil
}

// publicAdminAPI is the collection of administrative API methods exposed over
// both secure and unsecure RPC channels.
type publicAdminAPI struct {
	node *Node // Node interfaced by this API
}

// Peers retrieves all the information we know about each individual peer at the
// protocol granularity.
func (api *publicAdminAPI) Peers() ([]*p2p.PeerInfo, error) {
	server := api.node.Server()
	if server == nil {
		return nil, ErrNodeStopped
	}
	return server.PeersInfo(), nil
}

// NodeInfo retrieves all the information we know about the host node at the
// protocol granularity.
func (api *publicAdminAPI) NodeInfo() (*p2p.NodeInfo, error) {
	server := api.node.Server()
	if server == nil {
		return nil, ErrNodeStopped
	}
	return server.NodeInfo(), nil
}

// Datadir retrieves the current data directory the node is using.
func (api *publicAdminAPI) Datadir() string {
	return api.node.DataDir()
}

// publicWeb3API offers helper utils
type publicWeb3API struct {
	stack *Node
}

// ClientVersion returns the node name
func (s *publicWeb3API) ClientVersion() string {
	return s.stack.Server().Name
}

// Sha3 applies the parallax sha3 implementation on the input.
// It assumes the input is hex encoded.
func (s *publicWeb3API) Sha3(input hexutil.Bytes) hexutil.Bytes {
	return crypto.Keccak256(input)
}
