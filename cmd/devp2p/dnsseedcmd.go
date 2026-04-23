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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/p2p/protocols/disc"
	"gopkg.in/urfave/cli.v1"
)

// parallax-disc-style DNS seeds: plain A/AAAA records under a single
// hostname, listing tcp_gossip-verified nodes on the default port 32110
// (Bitcoin parity — non-default-port nodes get reduced DNS-seed
// discoverability and rely on gossip for propagation). The pipeline:
//
//   parallax-disc crawl  → CrawlState JSON
//   dns-seed compile     → SeedZone JSON   (filters + reliability gate)
//   dns-seed to-zonefile → BIND snippet    (operator-managed DNS)
//   dns-seed to-cloudflare / to-route53    (idempotent reconcile)
//
// The intermediate SeedZone JSON exists for auditability: operators can
// review the candidate list, diff between runs, and rerun only the
// deploy step on transient API failures.

const (
	defaultParallaxTCPPort = 32110
	defaultMinSuccesses    = 3
	defaultMaxAge          = 24 * time.Hour
	defaultMinSuccessRate  = 0.5
	defaultMinRecords      = 5
)

var (
	dnsSeedCommand = cli.Command{
		Name:  "dns-seed",
		Usage: "Bitcoin-style plain A/AAAA DNS seed publisher",
		Subcommands: []cli.Command{
			dnsSeedCompileCommand,
			dnsSeedToZonefileCommand,
			dnsSeedToCloudflareCommand,
			dnsSeedToRoute53Command,
		},
	}

	dnsSeedCompileCommand = cli.Command{
		Name:      "compile",
		Usage:     "Filter a CrawlState JSON into a publishable SeedZone JSON",
		ArgsUsage: "<state-file> <out-file>",
		Action:    dnsSeedCompile,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "name",
				Usage: "DNS name the SeedZone is bound to (e.g. seed.prlxdisc.org)",
				Value: "seed.prlxdisc.org",
			},
			cli.IntFlag{
				Name:  "default-port",
				Usage: "Only entries advertising this TCP port are published. Bitcoin parity: non-default-port nodes accept reduced DNS-seed discoverability.",
				Value: defaultParallaxTCPPort,
			},
			cli.DurationFlag{
				Name:  "max-age",
				Usage: "Drop entries whose LastSuccess is older than this.",
				Value: defaultMaxAge,
			},
			cli.UintFlag{
				Name:  "min-successes",
				Usage: "Drop entries with fewer than this many successful probes.",
				Value: defaultMinSuccesses,
			},
			cli.Float64Flag{
				Name:  "min-success-rate",
				Usage: "Drop entries with success/(success+fail) below this ratio.",
				Value: defaultMinSuccessRate,
			},
			cli.IntFlag{
				Name:  "min-records",
				Usage: "Refuse to write a SeedZone with fewer records than this. Defense against the empty-deploy footgun.",
				Value: defaultMinRecords,
			},
		},
	}

	dnsSeedToZonefileCommand = cli.Command{
		Name:      "to-zonefile",
		Usage:     "Emit a BIND zone snippet from a SeedZone JSON. Stdout when out-file is - or omitted.",
		ArgsUsage: "<seed-zone-file> [<out-file>]",
		Action:    dnsSeedToZonefile,
		Flags: []cli.Flag{
			cli.IntFlag{
				Name:  "ttl",
				Usage: "TTL (in seconds) on each emitted A/AAAA record.",
				Value: 60 * 60,
			},
		},
	}

	dnsSeedToCloudflareCommand = cli.Command{
		Name:      "to-cloudflare",
		Usage:     "Deploy a SeedZone's A/AAAA records to Cloudflare. Idempotent reconcile.",
		ArgsUsage: "<seed-zone-file>",
		Action:    dnsSeedToCloudflare,
		Flags: []cli.Flag{
			cloudflareTokenFlag,
			cloudflareZoneIDFlag,
			cli.IntFlag{
				Name:  "ttl",
				Usage: "TTL (in seconds) on each record.",
				Value: 60 * 60,
			},
		},
	}

	dnsSeedToRoute53Command = cli.Command{
		Name:      "to-route53",
		Usage:     "Deploy a SeedZone to Route53 as one A RRSet (all IPv4) and one AAAA RRSet (all IPv6).",
		ArgsUsage: "<seed-zone-file>",
		Action:    dnsSeedToRoute53,
		Flags: []cli.Flag{
			route53AccessKeyFlag,
			route53AccessSecretFlag,
			route53ZoneIDFlag,
			route53RegionFlag,
			cli.IntFlag{
				Name:  "ttl",
				Usage: "TTL (in seconds) on each RRSet.",
				Value: 60 * 60,
			},
		},
	}
)

// SeedZone is the publishable form: a flat list of (family, IP) records
// derived from a vetted subset of the crawler's CrawlState. The
// publisher is intentionally dumb — all reliability and freshness
// decisions live in the compile step.
type SeedZone struct {
	Name      string       `json:"name"`
	UpdatedAt time.Time    `json:"updatedAt"`
	Seq       uint64       `json:"seq"`
	Records   []SeedRecord `json:"records"`
}

type SeedRecord struct {
	Family string `json:"family"` // "A" or "AAAA"
	IP     string `json:"ip"`
}

// loadSeedZone reads a SeedZone JSON file. Missing file → error
// (deploys must not silently no-op).
func loadSeedZone(path string) (*SeedZone, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read seed zone %q: %w", path, err)
	}
	// Sniff the first non-whitespace byte so operators who hand us a
	// BIND zone file (from `dns-seed to-zonefile`) get a useful hint
	// instead of a raw JSON parse error.
	head := bytes.TrimLeft(data, " \t\r\n")
	if len(head) > 0 {
		switch head[0] {
		case ';', '$':
			return nil, fmt.Errorf("parse seed zone %q: looks like a BIND zone file, not SeedZone JSON. to-cloudflare / to-route53 expect the compiled JSON produced by `dns-seed compile`; `dns-seed to-zonefile` output is for manual DNS management", path)
		case '<':
			return nil, fmt.Errorf("parse seed zone %q: looks like XML/HTML, not SeedZone JSON", path)
		}
	}
	var z SeedZone
	if err := json.Unmarshal(data, &z); err != nil {
		return nil, fmt.Errorf("parse seed zone %q: %w", path, err)
	}
	if z.Name == "" {
		return nil, errors.New("seed zone has empty name")
	}
	return &z, nil
}

// saveSeedZone writes a SeedZone JSON file atomically. "-" → stdout.
func saveSeedZone(path string, z *SeedZone) error {
	enc, err := json.MarshalIndent(z, "", "  ")
	if err != nil {
		return err
	}
	if path == "" || path == "-" {
		_, err = os.Stdout.Write(enc)
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// compileFilters carries the tunable knobs of the compile step. Pulled
// out so compileSeedZone is callable from tests without going through
// urfave/cli's Context.
type compileFilters struct {
	Name           string
	DefaultPort    uint16
	MaxAge         time.Duration
	MinSuccesses   uint64
	MinSuccessRate float64
	MinRecords     int
}

// compileSeedZone applies the four filters described on dnsSeedCompile
// to st and returns the resulting SeedZone, or an error if the result
// has fewer than f.MinRecords entries.
func compileSeedZone(st *CrawlState, f compileFilters) (*SeedZone, error) {
	now := time.Now()
	cutoff := now.Add(-f.MaxAge)
	zone := &SeedZone{
		Name:      f.Name,
		UpdatedAt: now,
		Seq:       uint64(now.Unix()),
	}
	for _, n := range st.Nodes {
		if n.KeyType != disc.KeyTypeNone {
			continue
		}
		if n.TCPPort != f.DefaultPort {
			continue
		}
		if n.NetworkID != disc.NetIPv4 && n.NetworkID != disc.NetIPv6 {
			continue
		}
		if n.LastSuccess.Before(cutoff) {
			continue
		}
		if n.SuccessCount < f.MinSuccesses {
			continue
		}
		total := n.SuccessCount + n.FailCount
		if total == 0 || float64(n.SuccessCount)/float64(total) < f.MinSuccessRate {
			continue
		}
		ip := net.ParseIP(n.IP)
		if !isDialableIP(ip) {
			continue
		}
		family := "A"
		if n.NetworkID == disc.NetIPv6 {
			family = "AAAA"
		}
		zone.Records = append(zone.Records, SeedRecord{Family: family, IP: ip.String()})
	}
	// Sort A first, then AAAA, then by IP — stable diffs across runs.
	sort.Slice(zone.Records, func(i, j int) bool {
		if zone.Records[i].Family != zone.Records[j].Family {
			return zone.Records[i].Family < zone.Records[j].Family
		}
		return zone.Records[i].IP < zone.Records[j].IP
	})
	if len(zone.Records) < f.MinRecords {
		return nil, fmt.Errorf("compiled zone has %d records, below --min-records=%d threshold (refusing to publish a near-empty zone — likely a crawler outage)",
			len(zone.Records), f.MinRecords)
	}
	return zone, nil
}

// dnsSeedCompile reads a CrawlState, applies four filters, and writes a
// SeedZone. Filter ordering matches plan.md:
//
//  1. KeyType == KeyTypeNone (v2.0-native only — DNS seed is the v2
//     bootstrap path; legacy enode entries reach v1.x peers via enrtree)
//  2. TCPPort == --default-port (Bitcoin parity)
//  3. NetworkID ∈ {IPv4, IPv6} (DNS can't resolve Tor/I2P/CJDNS)
//  4. LastSuccess within --max-age, SuccessCount ≥ --min-successes,
//     success rate ≥ --min-success-rate
//
// If the result has fewer than --min-records entries, exit non-zero
// without writing — protects against publishing an empty zone after a
// crawler outage.
func dnsSeedCompile(ctx *cli.Context) error {
	if ctx.NArg() != 2 {
		return fmt.Errorf("usage: dns-seed compile <state-file> <out-file>")
	}
	stateFile := ctx.Args().Get(0)
	outFile := ctx.Args().Get(1)

	state, err := loadState(stateFile)
	if err != nil {
		return err
	}
	f := compileFilters{
		Name:           ctx.String("name"),
		DefaultPort:    uint16(ctx.Int("default-port")),
		MaxAge:         ctx.Duration("max-age"),
		MinSuccesses:   uint64(ctx.Uint("min-successes")),
		MinSuccessRate: ctx.Float64("min-success-rate"),
		MinRecords:     ctx.Int("min-records"),
	}
	zone, err := compileSeedZone(state, f)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "compiled %d records (filter: keyType=0, port=%d, age<=%s, successCount>=%d, rate>=%.2f)\n",
		len(zone.Records), f.DefaultPort, f.MaxAge, f.MinSuccesses, f.MinSuccessRate)
	return saveSeedZone(outFile, zone)
}

// renderZonefile produces the BIND-format snippet for z. ttl applies to
// every emitted A/AAAA record.
func renderZonefile(z *SeedZone, ttl int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; parallax-disc DNS seed zone\n")
	fmt.Fprintf(&b, "; generated by `devp2p dns-seed to-zonefile` at %s\n", z.UpdatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "; %d records, seq=%d\n", len(z.Records), z.Seq)
	fmt.Fprintf(&b, "$ORIGIN %s.\n", strings.TrimSuffix(z.Name, "."))
	fmt.Fprintf(&b, "$TTL %d\n", ttl)
	for _, r := range z.Records {
		// Empty owner = the zone apex.
		fmt.Fprintf(&b, "@\t%d\tIN\t%s\t%s\n", ttl, r.Family, r.IP)
	}
	return b.String()
}

// dnsSeedToZonefile emits a BIND-format $ORIGIN snippet. Each record
// becomes one A/AAAA line at the apex of the zone.
func dnsSeedToZonefile(ctx *cli.Context) error {
	if ctx.NArg() < 1 || ctx.NArg() > 2 {
		return fmt.Errorf("usage: dns-seed to-zonefile <seed-zone-file> [<out-file>]")
	}
	z, err := loadSeedZone(ctx.Args().Get(0))
	if err != nil {
		return err
	}
	out := "-"
	if ctx.NArg() == 2 {
		out = ctx.Args().Get(1)
	}
	body := renderZonefile(z, ctx.Int("ttl"))

	if out == "-" {
		_, err = os.Stdout.WriteString(body)
		return err
	}
	return os.WriteFile(out, []byte(body), 0o644)
}

// dnsSeedToCloudflare and dnsSeedToRoute53 live in dns_cloudflare.go
// and dns_route53.go respectively, alongside their existing siblings.
// Defined here as thin wrappers that load the zone and dispatch.

func dnsSeedToCloudflare(ctx *cli.Context) error {
	if ctx.NArg() != 1 {
		return fmt.Errorf("usage: dns-seed to-cloudflare <seed-zone-file>")
	}
	z, err := loadSeedZone(ctx.Args().Get(0))
	if err != nil {
		return err
	}
	c := newCloudflareClient(ctx)
	return c.deploySeedZone(z, ctx.Int("ttl"))
}

func dnsSeedToRoute53(ctx *cli.Context) error {
	if ctx.NArg() != 1 {
		return fmt.Errorf("usage: dns-seed to-route53 <seed-zone-file>")
	}
	z, err := loadSeedZone(ctx.Args().Get(0))
	if err != nil {
		return err
	}
	c := newRoute53Client(ctx)
	return c.deploySeedZone(z, int64(ctx.Int("ttl")))
}
