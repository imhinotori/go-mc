package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
)

// world_stream.go is the per-player view-distance chunk streaming (Plan 04-03,
// WORLD-05): a center-out neighbor-ring computation, the server view-distance clamp
// (the load-bearing DoS control), and the player chunk-center helper. The tick phases
// tickChunks (request the bounded ring off-tick) and flushOutbound (send newly-Ready
// chunks center-out, batched) live in tick_phases.go and call into these helpers. All
// of it runs on the tick owner goroutine over tick-owned state.

// serverViewDistance is the FIXED server clamp for v1 (radius 2 chunks). It bounds the
// needed-ring to (2r+1)^2 = 25 columns regardless of what a client requests, so an
// untrusted client cannot make the chunk-generation/send set unbounded (threat T-4-01).
// Radius 2 satisfies "the player stands on solid ground with a surrounding ring"
// (04-RESEARCH A4); a later phase can raise it once load is profiled.
const serverViewDistance = 2

// minViewDistance is the floor a (future) client-supplied view distance is raised to,
// so a client asking for 0/1 still gets a usable square around itself.
const minViewDistance = 2

// clampViewDistance maps a (possibly untrusted, future client-supplied) requested view
// distance to the server-enforced range [minViewDistance, serverViewDistance]. The
// clamp is the DoS control: the needed-ring size (2r+1)^2 is bounded by the server,
// never by the request (threat T-4-01, asserted by TestViewDistanceClamp).
func clampViewDistance(req int) int {
	if req < minViewDistance {
		return minViewDistance
	}
	if req > serverViewDistance {
		return serverViewDistance
	}
	return req
}

// centerOutRing returns every column within Chebyshev distance r of center, ordered by
// INCREASING Chebyshev (ring) distance so the player's own chunk is first and each
// surrounding ring follows — the "stand-in chunk plus the surrounding ring before
// render" order (WORLD-05). The result is exactly (2r+1)^2 distinct columns. r is the
// already-clamped view distance, so the slice is bounded by the server.
func centerOutRing(center level.ChunkPos, r int) []level.ChunkPos {
	if r < 0 {
		r = 0
	}
	side := 2*r + 1
	ring := make([]level.ChunkPos, 0, side*side)
	// Emit ring by ring, distance d = 0..r. Distance 0 is the center; distance d is the
	// square shell at max(|dx|,|dz|) == d. This yields a center-out (non-decreasing
	// Chebyshev) order without an explicit sort.
	for d := 0; d <= r; d++ {
		if d == 0 {
			ring = append(ring, center)
			continue
		}
		// Top and bottom edges (dz == -d and dz == +d), full width.
		for dx := -d; dx <= d; dx++ {
			ring = append(ring, level.ChunkPos{center[0] + int32(dx), center[1] - int32(d)})
		}
		for dx := -d; dx <= d; dx++ {
			ring = append(ring, level.ChunkPos{center[0] + int32(dx), center[1] + int32(d)})
		}
		// Left and right edges (dx == -d and dx == +d), excluding the corners already
		// emitted by the top/bottom edges (dz in (-d, d)).
		for dz := -d + 1; dz <= d-1; dz++ {
			ring = append(ring, level.ChunkPos{center[0] - int32(d), center[1] + int32(dz)})
		}
		for dz := -d + 1; dz <= d-1; dz++ {
			ring = append(ring, level.ChunkPos{center[0] + int32(d), center[1] + int32(dz)})
		}
	}
	return ring
}

// chunkCenterOf maps a player's block position to its chunk column (floor-div by 16).
// Phase 4 has no real position yet (spawn is PLAY-01/03 / Phase 5), so registration
// defaults the center to {0,0}; this helper is the seam Phase 5 calls when movement
// lands. Kept here so the center math lives in one place.
func chunkCenterOf(blockX, blockZ int32) level.ChunkPos {
	return level.ChunkPos{floorDiv16(blockX), floorDiv16(blockZ)}
}

// floorDiv16 is arithmetic floor-division by 16 (chunk width) that is correct for
// negative coordinates (Go's / truncates toward zero, which is wrong west/north of 0).
func floorDiv16(v int32) int32 {
	return int32(int(v) >> 4)
}

// recenterRing makes the Phase-4 view-distance ring FOLLOW the player after a
// chunk-column crossing (PLAY-04). The streamer (tickChunks/flushOutbound) already keys
// on p.center and is idempotent, so re-centering is small and does NOT rebuild it:
//
//  1. p.center = newCenter — the next tickChunks requests the new ring center-out.
//  2. p.centerSent = false — flushOutbound re-emits SetChunkCacheCenter once for the
//     new center (it sends the framing under !centerSent).
//  3. Prune p.sentChunks of every column that left the new window, sending one
//     world.ForgetLevelChunk per dropped column so the client frees it (closing the
//     void-on-walk failure mode; the column re-streams if the player walks back).
//
// Bounded by the server clamp: the needed-ring is centerOutRing(newCenter, viewDist),
// so a wild coordinate cannot enlarge the send/forget set (T-5-04). Runs on the tick
// owner over tick-owned state; p.client.Send goes through the bounded outbound queue,
// keeping the writeLoop the sole socket writer (TICK-05 / T-5-06). Nil-guards p.client
// and p.sentChunks so unit tests can omit either.
func (t *TickLoop) recenterRing(p *tickPlayer, newCenter level.ChunkPos) {
	p.center = newCenter
	p.centerSent = false

	if p.sentChunks == nil {
		return // nothing streamed yet: just the center move + framing re-emit
	}

	// Build the new needed-set (server-clamped ring around the new center).
	needed := make(map[level.ChunkPos]bool, len(p.sentChunks))
	for _, pos := range centerOutRing(newCenter, p.viewDist) {
		needed[pos] = true
	}

	// Forget + drop every previously-sent column no longer in the window.
	for pos := range p.sentChunks {
		if needed[pos] {
			continue
		}
		delete(p.sentChunks, pos)
		if p.client != nil {
			p.client.Send(world.ForgetLevelChunk(pos[0], pos[1]))
		}
	}
}
