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

package addrman

import (
	"time"

	"github.com/ParallaxProtocol/parallax/support/metrics"
)

// Metric names match the specification in PIP-0006 Phase 3 exactly so
// downstream dashboards (Grafana) don't need relabeling. Source-tagged
// gauges are registered lazily on first observation.
var (
	triedGauge   = metrics.NewRegisteredGauge("p2p/addrman/tried_count", nil)
	newGauge     = metrics.NewRegisteredGauge("p2p/addrman/new_count", nil)
	sourceGauges = map[Source]metrics.Gauge{}
)

// selectLatencyHist is registered lazily on first use so the histogram
// stays inert when metrics are disabled at the binary level.
var selectLatencyHist = metrics.GetOrRegisterHistogramLazy(
	"p2p/addrman/select_latency", nil,
	func() metrics.Sample {
		return metrics.ResettingSample(metrics.NewExpDecaySample(1028, 0.015))
	},
)

// RefreshMetrics updates the gauges from m's current state. Cheap — a
// handful of map reads and atomic writes. Callers invoke on a ticker
// (every few seconds is enough for operational visibility).
func (m *AddrMan) RefreshMetrics() {
	m.mu.Lock()
	defer m.mu.Unlock()

	triedGauge.Update(int64(m.nTried))
	newGauge.Update(int64(m.nNew))
	for tag, count := range m.sourceCounts {
		g, ok := sourceGauges[tag]
		if !ok {
			g = metrics.NewRegisteredGauge("p2p/addrman/source/"+tag.String(), nil)
			sourceGauges[tag] = g
		}
		g.Update(int64(count))
	}
}

// RecordSelectLatency samples the time a single Select call took. Only
// useful when the caller is interested in the tail — we don't sample on
// every call inside Select itself because the benchmark shows each call
// is hundreds of nanoseconds, and the histogram update cost would
// dominate.
func RecordSelectLatency(d time.Duration) {
	selectLatencyHist.Update(int64(d))
}
