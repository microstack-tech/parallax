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
	"errors"
	"fmt"
	"strings"

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/p2p/socks"
)

// netPolicy resolves the operator's proxy configuration into per-network
// reachability and outbound routing. It is the equivalent of Bitcoin
// Core's g_reachable_nets + proxyInfo tables (src/net.cpp SetReachable /
// IsReachable, src/init.cpp proxy setup) collapsed into one immutable
// value built at Server start.
//
// PIP-0007 §1: a network is reachable iff the node has a route to it —
// IPv4/IPv6 by default (minus --onlynet exclusions), onion iff --onion or
// --proxy provides one.
type netPolicy struct {
	reachable map[addrman.NetID]bool
	proxies   map[addrman.NetID]*socks.Proxy

	// name is the proxy used for hostname targets (DNS-free seed
	// fetches). Set only by --proxy, never by --onion — Core's
	// SetNameProxy has the same asymmetry: a clearnet node using Tor
	// solely for onion peers still resolves seed hostnames locally.
	name *socks.Proxy

	// onionAllowed records whether --onlynet permits the onion
	// network (or no restriction was given). The torcontrol
	// auto-proxy flips onion reachable only when this holds —
	// Core's onion_allowed_by_onlynet gate.
	onionAllowed bool
}

// Config validation errors, matching Core's init-time messages in intent.
var (
	errOnlyNetUnknown   = errors.New("p2p: unknown --onlynet network")
	errOnlyNetNoRoute   = errors.New("p2p: outbound restricted to a network with no route (onion needs --proxy, --onion or --listenonion)")
	errNoNetReachable   = errors.New("p2p: configuration leaves no network reachable")
	errOnionProxyFormat = errors.New("p2p: invalid --onion value")
)

// onlyNetName parses one --onlynet token. Core accepts "ipv4", "ipv6",
// "onion", "i2p", "cjdns"; Parallax dials only the first three (PIP-0007
// non-goals exclude i2p/cjdns).
func onlyNetName(s string) (addrman.NetID, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ipv4":
		return addrman.NetIPv4, true
	case "ipv6":
		return addrman.NetIPv6, true
	case "onion", "tor":
		return addrman.NetTorV3, true
	}
	return 0, false
}

// newNetPolicy resolves cfg into a netPolicy. Called once at Server
// start; a config error here aborts startup, mirroring Core's InitError
// on inconsistent proxy flags.
func newNetPolicy(cfg *Config) (*netPolicy, error) {
	p := &netPolicy{
		reachable: map[addrman.NetID]bool{
			addrman.NetIPv4: true,
			addrman.NetIPv6: true,
		},
		proxies:      make(map[addrman.NetID]*socks.Proxy),
		onionAllowed: true,
	}

	// One shared isolation generator across all proxies, as in Core
	// (the generator is process-global there).
	var iso *socks.IsolationGenerator
	if !cfg.ProxyNoRandomize && (cfg.ProxyAddr != "" || onionProxyEnabled(cfg.OnionProxyAddr)) {
		var err error
		if iso, err = socks.NewIsolationGenerator(); err != nil {
			return nil, err
		}
	}

	// --proxy: route every network through it, which also makes onion
	// reachable (Core: -proxy sets the proxy for all networks
	// including NET_ONION).
	if cfg.ProxyAddr != "" {
		pr := &socks.Proxy{Addr: cfg.ProxyAddr, Isolation: iso}
		p.proxies[addrman.NetIPv4] = pr
		p.proxies[addrman.NetIPv6] = pr
		p.proxies[addrman.NetTorV3] = pr
		p.reachable[addrman.NetTorV3] = true
		p.name = pr
	}

	// --onion: override the onion route specifically. "0" disables
	// onion outbound even when --proxy is set (Core: -noonion /
	// -onion=0).
	switch {
	case cfg.OnionProxyAddr == "":
		// No override.
	case cfg.OnionProxyAddr == "0":
		delete(p.proxies, addrman.NetTorV3)
		p.reachable[addrman.NetTorV3] = false
	case strings.Contains(cfg.OnionProxyAddr, ":"):
		p.proxies[addrman.NetTorV3] = &socks.Proxy{Addr: cfg.OnionProxyAddr, Isolation: iso}
		p.reachable[addrman.NetTorV3] = true
	default:
		return nil, fmt.Errorf("%w: %q", errOnionProxyFormat, cfg.OnionProxyAddr)
	}

	// --onlynet: restrict outbound to the named networks. Everything
	// else becomes unreachable (Core: SetReachable(net, false) for all
	// nets not listed).
	if len(cfg.OnlyNet) > 0 {
		keep := make(map[addrman.NetID]bool)
		for _, name := range cfg.OnlyNet {
			id, ok := onlyNetName(name)
			if !ok {
				return nil, fmt.Errorf("%w: %q", errOnlyNetUnknown, name)
			}
			keep[id] = true
		}
		for id := range p.reachable {
			if !keep[id] {
				p.reachable[id] = false
			}
		}
		p.onionAllowed = keep[addrman.NetTorV3]
		// Core errors out when -onlynet=onion leaves no usable onion
		// route — unless --listenonion is set, in which case the
		// torcontrol thread may auto-configure the proxy from the
		// daemon's SOCKS listener later (init.cpp parity). An
		// explicit --onion=0 forbids the route outright and always
		// errors.
		if keep[addrman.NetTorV3] && p.proxies[addrman.NetTorV3] == nil {
			if cfg.OnionProxyAddr == "0" || !cfg.ListenOnion {
				return nil, errOnlyNetNoRoute
			}
		}
	}

	any := false
	for _, ok := range p.reachable {
		if ok {
			any = true
			break
		}
	}
	// onionPending: no network reachable yet, but --listenonion may
	// deliver the onion route once the control port answers.
	onionPending := cfg.ListenOnion && p.onionAllowed &&
		p.proxies[addrman.NetTorV3] == nil && cfg.OnionProxyAddr != "0"
	if !any && !onionPending {
		return nil, errNoNetReachable
	}
	return p, nil
}

// withAutoOnionProxy clones p with the torcontrol-discovered SOCKS
// listener as the onion route — Core's get_socks_cb SetProxy +
// SetReachable. Stream isolation is always on for the auto proxy,
// regardless of --proxyrandomize (Core hardcodes tor_stream_isolation
// there: the daemon's own listener supports IsolateSOCKSAuth by
// default and correlation resistance matters most on Tor). Onion
// becomes reachable only when --onlynet permits it.
func (p *netPolicy) withAutoOnionProxy(addr string) (*netPolicy, error) {
	iso, err := socks.NewIsolationGenerator()
	if err != nil {
		return nil, err
	}
	next := &netPolicy{
		reachable:    make(map[addrman.NetID]bool, len(p.reachable)),
		proxies:      make(map[addrman.NetID]*socks.Proxy, len(p.proxies)+1),
		name:         p.name,
		onionAllowed: p.onionAllowed,
	}
	for k, v := range p.reachable {
		next.reachable[k] = v
	}
	for k, v := range p.proxies {
		next.proxies[k] = v
	}
	next.proxies[addrman.NetTorV3] = &socks.Proxy{Addr: addr, Isolation: iso}
	if p.onionAllowed {
		next.reachable[addrman.NetTorV3] = true
	}
	return next, nil
}

// isReachable reports whether AUTOMATIC outbound connections to net
// are allowed: the node has a route and --onlynet permits it. This is
// the gate for dial candidates the node picks itself (addrman draws,
// feelers, anchors, bootstrap ingest).
func (p *netPolicy) isReachable(net addrman.NetID) bool {
	return p != nil && p.reachable[net]
}

// hasRoute reports whether the node can physically reach net at all,
// ignoring --onlynet. Clearnet is always routable (directly or
// through --proxy); onion needs a configured or discovered proxy.
// Operator-initiated and static dials are gated on this and not on
// isReachable, mirroring Core, where -onlynet restricts automatic
// connections while addnode/connect targets are dialed regardless.
func (p *netPolicy) hasRoute(net addrman.NetID) bool {
	if p == nil {
		return net == addrman.NetIPv4 || net == addrman.NetIPv6
	}
	switch net {
	case addrman.NetIPv4, addrman.NetIPv6:
		return true
	case addrman.NetTorV3:
		return p.proxies[addrman.NetTorV3] != nil
	}
	return false
}

// proxyFor returns the SOCKS5 proxy for net, or nil for a direct dial.
// A nil result with isReachable()==true means plain TCP.
func (p *netPolicy) proxyFor(net addrman.NetID) *socks.Proxy {
	if p == nil {
		return nil
	}
	return p.proxies[net]
}

// clearnetReachable reports whether any IP network is dialable. When
// false the node is onion-only: the discv4 UDP socket, NAT traversal,
// and system-resolver DNS seeding are all suppressed (PIP-0007 §1.4).
func (p *netPolicy) clearnetReachable() bool {
	return p.isReachable(addrman.NetIPv4) || p.isReachable(addrman.NetIPv6)
}

// proxied reports whether any outbound route goes through a proxy.
func (p *netPolicy) proxied() bool {
	return p != nil && len(p.proxies) > 0
}

// nameProxy returns the proxy hostname targets are sent through, or nil
// when names resolve locally. Non-nil only under --proxy (§1.4).
func (p *netPolicy) nameProxy() *socks.Proxy {
	if p == nil {
		return nil
	}
	return p.name
}

// onionProxyEnabled reports whether s names a real onion proxy rather
// than being empty or the "0" disable sentinel.
func onionProxyEnabled(s string) bool {
	return s != "" && s != "0"
}
