package server

import (
	"sort"
	"sync"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// debug_bandwidth.go is an ULTRA_DEBUG-only outbound byte accounting tool: it tallies bytes
// written per clientbound packet ID across ALL connections and dumps the top senders every
// dumpEvery packets, so a bandwidth flood ("Sent 1470 MB at login") can be attributed to the
// exact packet ID that is being re-sent. No-op (single atomic-free branch) unless
// SULFUR_ULTRA_DEBUG=1. Wired in Client.writeLoop (the single per-connection socket writer).

var (
	bwMu      sync.Mutex
	bwBytes   = map[int32]int64{} // packet ID -> total payload bytes written
	bwCount   = map[int32]int64{} // packet ID -> number of writes
	bwTotal   int64
	bwSince   int64
	dumpEvery = int64(300) // dump the per-ID table every N packets
)

// debugCountOutbound tallies one written packet by ID and periodically dumps the table. The
// byte count is len(p.Data) (the packet body) plus a fixed ~5-byte frame estimate (varint id +
// length) — the relative ranking is what matters for finding the flood, not the exact wire size.
func debugCountOutbound(p pk.Packet) {
	if !udebugEnabled {
		return
	}
	bwMu.Lock()
	n := int64(len(p.Data)) + 5
	bwBytes[p.ID] += n
	bwCount[p.ID]++
	bwTotal += n
	bwSince++
	if bwSince >= dumpEvery {
		bwSince = 0
		dumpBandwidthLocked()
	}
	bwMu.Unlock()
}

// dumpBandwidthLocked prints the per-ID byte table sorted by total bytes desc. Caller holds bwMu.
func dumpBandwidthLocked() {
	type row struct {
		id    int32
		bytes int64
		count int64
	}
	rows := make([]row, 0, len(bwBytes))
	for id, b := range bwBytes {
		rows = append(rows, row{id, b, bwCount[id]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].bytes > rows[j].bytes })
	udebug("bandwidth", "=== outbound totals: %.2f MB across %d ids ===", float64(bwTotal)/1e6, len(rows))
	for i, r := range rows {
		if i >= 12 {
			break
		}
		udebug("bandwidth", "  id=0x%02X  %.2f MB  count=%d  avg=%d B",
			r.id, float64(r.bytes)/1e6, r.count, r.bytes/maxInt64(r.count, 1))
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
