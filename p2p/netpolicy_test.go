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
	"testing"

	"github.com/ParallaxProtocol/parallax/p2p/addrman"
)

func TestNetPolicyDefaults(t *testing.T) {
	p, err := newNetPolicy(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.isReachable(addrman.NetIPv4) || !p.isReachable(addrman.NetIPv6) {
		t.Error("clearnet must be reachable by default")
	}
	if p.isReachable(addrman.NetTorV3) {
		t.Error("onion must be unreachable without a proxy")
	}
	if p.proxyFor(addrman.NetIPv4) != nil {
		t.Error("no proxy configured, expected direct dial")
	}
	if p.proxied() || !p.clearnetReachable() {
		t.Error("defaults: proxied=false, clearnetReachable=true")
	}
}

func TestNetPolicyProxyAll(t *testing.T) {
	p, err := newNetPolicy(&Config{ProxyAddr: "127.0.0.1:9050"})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []addrman.NetID{addrman.NetIPv4, addrman.NetIPv6, addrman.NetTorV3} {
		if !p.isReachable(n) {
			t.Errorf("%s unreachable under --proxy", n)
		}
		pr := p.proxyFor(n)
		if pr == nil || pr.Addr != "127.0.0.1:9050" {
			t.Errorf("%s not routed through the proxy", n)
		}
		if pr != nil && pr.Isolation == nil {
			t.Errorf("%s proxy missing stream isolation (must default on)", n)
		}
	}
}

func TestNetPolicyOnionOverride(t *testing.T) {
	p, err := newNetPolicy(&Config{ProxyAddr: "127.0.0.1:9999", OnionProxyAddr: "127.0.0.1:9050"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.proxyFor(addrman.NetTorV3).Addr; got != "127.0.0.1:9050" {
		t.Errorf("onion proxy = %s, want the --onion override", got)
	}
	if got := p.proxyFor(addrman.NetIPv4).Addr; got != "127.0.0.1:9999" {
		t.Errorf("ipv4 proxy = %s, want --proxy", got)
	}

	// --onion alone: onion reachable, clearnet stays direct.
	p, err = newNetPolicy(&Config{OnionProxyAddr: "127.0.0.1:9050"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.isReachable(addrman.NetTorV3) || p.proxyFor(addrman.NetIPv4) != nil {
		t.Error("--onion alone: onion via proxy, clearnet direct")
	}

	// --onion=0 disables onion even under --proxy.
	p, err = newNetPolicy(&Config{ProxyAddr: "127.0.0.1:9999", OnionProxyAddr: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if p.isReachable(addrman.NetTorV3) {
		t.Error("--onion=0 must make onion unreachable")
	}

	if _, err := newNetPolicy(&Config{OnionProxyAddr: "garbage"}); !errors.Is(err, errOnionProxyFormat) {
		t.Errorf("bad --onion value: got %v", err)
	}
}

func TestNetPolicyOnlyNet(t *testing.T) {
	p, err := newNetPolicy(&Config{OnlyNet: []string{"ipv4"}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.isReachable(addrman.NetIPv4) || p.isReachable(addrman.NetIPv6) {
		t.Error("--onlynet=ipv4 must leave only ipv4 reachable")
	}

	// onlynet=onion without a route is a config error, as in Core.
	if _, err := newNetPolicy(&Config{OnlyNet: []string{"onion"}}); !errors.Is(err, errOnlyNetNoRoute) {
		t.Errorf("onlynet=onion without proxy: got %v", err)
	}

	// With a route it yields an onion-only node.
	p, err = newNetPolicy(&Config{OnlyNet: []string{"onion"}, OnionProxyAddr: "127.0.0.1:9050"})
	if err != nil {
		t.Fatal(err)
	}
	if p.clearnetReachable() {
		t.Error("onlynet=onion: clearnet must be unreachable")
	}
	if !p.isReachable(addrman.NetTorV3) {
		t.Error("onlynet=onion: onion must be reachable")
	}

	if _, err := newNetPolicy(&Config{OnlyNet: []string{"ipx"}}); !errors.Is(err, errOnlyNetUnknown) {
		t.Errorf("unknown onlynet: got %v", err)
	}
}

func TestNetPolicyListenOnionPending(t *testing.T) {
	// --onlynet=onion with no proxy is legal when --listenonion may
	// auto-configure one (Core init.cpp parity)...
	p, err := newNetPolicy(&Config{OnlyNet: []string{"onion"}, ListenOnion: true, ListenAddr: ":32110"})
	if err != nil {
		t.Fatal(err)
	}
	if p.isReachable(addrman.NetTorV3) {
		t.Error("onion must stay unreachable until the auto-proxy arrives")
	}
	if !p.onionAllowed {
		t.Error("onlynet=onion must mark onion allowed")
	}
	// ...but an explicit --onion=0 forbids the route outright.
	if _, err := newNetPolicy(&Config{OnlyNet: []string{"onion"}, ListenOnion: true, ListenAddr: ":32110", OnionProxyAddr: "0"}); !errors.Is(err, errOnlyNetNoRoute) {
		t.Errorf("onlynet=onion with --onion=0: got %v", err)
	}
	// --listenonion only excuses the missing route when the node
	// actually listens; otherwise the controller never runs and the
	// route can never arrive.
	if _, err := newNetPolicy(&Config{OnlyNet: []string{"onion"}, ListenOnion: true}); !errors.Is(err, errOnlyNetNoRoute) {
		t.Errorf("onlynet=onion, --listenonion, no listener: got %v", err)
	}
	// And without --listenonion the original error stands.
	if _, err := newNetPolicy(&Config{OnlyNet: []string{"onion"}}); !errors.Is(err, errOnlyNetNoRoute) {
		t.Errorf("onlynet=onion without any route: got %v", err)
	}
}

func TestNetPolicyWithAutoOnionProxy(t *testing.T) {
	// Default posture: auto-proxy adds the route and flips onion
	// reachable, with stream isolation forced on (Core hardcodes it
	// for the daemon's own listener).
	p, err := newNetPolicy(&Config{ProxyNoRandomize: true})
	if err != nil {
		t.Fatal(err)
	}
	next, err := p.withAutoOnionProxy("127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	if !next.isReachable(addrman.NetTorV3) {
		t.Error("auto-proxy must make onion reachable")
	}
	pr := next.proxyFor(addrman.NetTorV3)
	if pr == nil || pr.Addr != "127.0.0.1:9050" {
		t.Fatalf("onion proxy = %+v", pr)
	}
	if pr.Isolation == nil {
		t.Error("auto-proxy must force stream isolation even under ProxyNoRandomize")
	}
	// The original policy is untouched (copy-on-write).
	if p.isReachable(addrman.NetTorV3) || p.proxyFor(addrman.NetTorV3) != nil {
		t.Error("withAutoOnionProxy mutated the source policy")
	}

	// --onlynet excluding onion keeps it unreachable even with the
	// proxy configured (Core's onion_allowed_by_onlynet gate).
	p, err = newNetPolicy(&Config{OnlyNet: []string{"ipv4"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err = p.withAutoOnionProxy("127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	if next.isReachable(addrman.NetTorV3) {
		t.Error("onlynet=ipv4 must keep onion unreachable despite the auto-proxy")
	}
	if next.proxyFor(addrman.NetTorV3) == nil {
		t.Error("the proxy itself is still recorded (Core sets it unconditionally)")
	}
}

func TestNetPolicyNoRandomize(t *testing.T) {
	p, err := newNetPolicy(&Config{ProxyAddr: "127.0.0.1:9050", ProxyNoRandomize: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.proxyFor(addrman.NetIPv4).Isolation != nil {
		t.Error("ProxyNoRandomize must drop the isolation generator")
	}
}

func TestNetPolicyNilReceiver(t *testing.T) {
	// The nil receiver must be inert; the connector layer supplies the
	// direct-dial fallback for servers constructed without Start.
	var p *netPolicy
	if p.isReachable(addrman.NetIPv4) || p.proxyFor(addrman.NetIPv4) != nil || p.proxied() {
		t.Error("nil policy must report nothing reachable and no proxies")
	}
}
