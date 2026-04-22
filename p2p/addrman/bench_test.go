package addrman

import (
	"testing"
	"time"
)

// BenchmarkSelect10k measures Select() latency with 10k entries. PIP-0006
// Phase 1 acceptance criterion: <1µs per call. Bitcoin's own Select is a
// few hundred nanoseconds in practice; the Go port should be in the same
// order of magnitude.
func BenchmarkSelect10k(b *testing.B) {
	m, err := New(Deterministic(42))
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	src := mkIPv4([4]byte{2, 3, 4, 5}, 30303)
	now := time.Now()
	for i := 0; i < 10_000; i++ {
		ip := [4]byte{
			byte(0x80 | (i >> 8 & 0x7F)),
			byte(i & 0xFF),
			byte((i >> 4) & 0xFF),
			0x37,
		}
		addr, err := NewNetAddr(NetIPv4, ip[:], 30303)
		if err != nil {
			b.Fatal(err)
		}
		m.AddOne(addr, now, src, SourceTCPGossip, 0)
	}
	// Promote ~500 entries into tried so Select actually has tried
	// table content to walk.
	pr := 0
	for id, info := range m.mapInfo {
		_ = id
		if pr >= 500 {
			break
		}
		m.Good(info.Addr, now)
		pr++
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = m.Select(false, nil)
	}
}

// BenchmarkAdd measures Add() cost on a warm table — relevant for ingest
// rate under gossip (Phase 4 target: 1–10 addresses/sec per peer).
func BenchmarkAdd(b *testing.B) {
	m, err := New(Deterministic(42))
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	src := mkIPv4([4]byte{2, 3, 4, 5}, 30303)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ip := [4]byte{
			byte(0x80 | (i >> 8 & 0x7F)),
			byte(i & 0xFF),
			byte((i >> 16) & 0xFF),
			byte((i >> 24) & 0xFF),
		}
		addr, _ := NewNetAddr(NetIPv4, ip[:], 30303)
		m.AddOne(addr, time.Now(), src, SourceTCPGossip, 0)
	}
}
