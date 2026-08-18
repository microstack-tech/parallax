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
	"net"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/addrman"
)

// DNSSeedDefaultInterval is the cadence between A/AAAA resolutions of
// each configured DNS seed host. 24h matches Bitcoin Core's
// CConnman::ThreadDNSAddressSeed schedule and keeps load on the seed
// service trivial.
const DNSSeedDefaultInterval = 24 * time.Hour

// DNSSeedDefaultPort is the TCP port we pair every resolved A/AAAA
// record with — Parallax's default v2 listener port. The DNS seed
// publisher in cmd/devp2p only emits records for nodes on this port
// (Bitcoin parity for default-port discoverability).
const DNSSeedDefaultPort = 32110

// dnsSeedFirstTickDelay is the wait before the first resolution after
// the loop starts. Gives the listener and addrman a moment to settle
// so the first ingest doesn't race with Server.Start's other setup.
const dnsSeedFirstTickDelay = 30 * time.Second

// DNSSeedResolver is the small slice of net.Resolver we depend on.
// Injectable so tests can swap in a fake without touching the real
// resolver. *net.Resolver satisfies it directly.
type DNSSeedResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// dnsSeedLoop resolves each host in `hosts` every `interval` and
// ingests every (IP, defaultPort) tuple into addrman with
// source=SourceDNSSeed. Cancellation is via ctx; the loop returns
// promptly on ctx.Done.
//
// Errors are logged and not returned — DNS hiccups shouldn't take down
// the node.
func dnsSeedLoop(
	ctx context.Context,
	resolver DNSSeedResolver,
	hosts []string,
	addrbook *addrman.AddrMan,
	defaultPort uint16,
	interval time.Duration,
	log logging.Logger,
) {
	if len(hosts) == 0 || addrbook == nil {
		return
	}
	if log == nil {
		log = logging.Root()
	}
	if interval <= 0 {
		interval = DNSSeedDefaultInterval
	}

	first := time.NewTimer(dnsSeedFirstTickDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	resolveAndIngest(ctx, resolver, hosts, addrbook, defaultPort, log)

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			resolveAndIngest(ctx, resolver, hosts, addrbook, defaultPort, log)
		}
	}
}

// proxiedSeedLoop is the --proxy replacement for dnsSeedLoop: instead
// of resolving seed hostnames (which would leak through the system
// resolver), it hands each hostname to the SOCKS5 proxy as a CONNECT
// target on the same cadence and lets the disc protocol's outbound
// greeting warm the addrbook. Port of Bitcoin Core's
// ThreadDNSAddressSeed HaveNameProxy() branch (AddAddrFetch(seed)).
// PIP-0007 §1.4.
func (srv *Server) proxiedSeedLoop(ctx context.Context, hosts []string, defaultPort uint16, interval time.Duration) {
	if len(hosts) == 0 {
		return
	}
	if interval <= 0 {
		interval = DNSSeedDefaultInterval
	}
	first := time.NewTimer(dnsSeedFirstTickDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	srv.fetchSeedsViaProxy(ctx, hosts, defaultPort)

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			srv.fetchSeedsViaProxy(ctx, hosts, defaultPort)
		}
	}
}

// fetchSeedsViaProxy runs one addr-fetch pass: each seed hostname is
// dialed through the name proxy on the default port, tagged as a
// feeler so it neither occupies an outbound slot nor records addrman
// failures, and torn down after addrFetchLifetime.
func (srv *Server) fetchSeedsViaProxy(ctx context.Context, hosts []string, defaultPort uint16) {
	pr := srv.policy().nameProxy()
	if pr == nil {
		return
	}
	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return
		default:
		}
		fd, err := pr.Dial(ctx, host, defaultPort)
		if err != nil {
			srv.log.Debug("proxied seed fetch failed", "host", host, "err", err)
			continue
		}
		// proxiedConn matters even though these are feelers: without
		// it the seed peer's YourAddr — which describes the proxy or
		// Tor exit, not us — would feed the self-address quorum, and
		// the session would dedup against every other seed session
		// through the same proxy.
		if err := srv.SetupConn(fd, dynDialedConn|v2DialedConn|feelerConn|proxiedConn, nil); err != nil {
			srv.log.Debug("proxied seed handshake failed", "host", host, "err", err)
			continue
		}
		srv.log.Info("proxied seed fetch connected", "host", host)
		// The peer's endpoint is unknown to us (the proxy resolved
		// it), so the feeler teardown can't match by address —
		// closing the fd tears the session down instead.
		timer := time.AfterFunc(addrFetchLifetime, func() { fd.Close() })
		go func() {
			<-ctx.Done()
			timer.Stop()
		}()
	}
}

// resolveAndIngest performs one resolution pass over hosts. Each host's
// failure is logged at warn and skipped — other hosts still run.
func resolveAndIngest(
	ctx context.Context,
	resolver DNSSeedResolver,
	hosts []string,
	addrbook *addrman.AddrMan,
	defaultPort uint16,
	log logging.Logger,
) {
	now := time.Now()
	for _, host := range hosts {
		ips, err := resolver.LookupHost(ctx, host)
		if err != nil {
			log.Warn("DNS seed resolution failed", "host", host, "err", err)
			continue
		}
		ingested := 0
		for _, ipStr := range ips {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			// Skip undialable addresses defensively. The seeder
			// publisher already filters these, but DNS responses
			// can come from anywhere — don't blindly trust them.
			if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
				continue
			}
			tcp := &net.TCPAddr{IP: ip, Port: int(defaultPort)}
			if addrman.IngestV2Addr(addrbook, tcp, addrman.SourceDNSSeed, now) {
				ingested++
			}
		}
		log.Info("DNS seed resolved", "host", host, "addresses", len(ips), "ingested", ingested)
	}
}
