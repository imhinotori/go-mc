package server

import (
	"context"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// world_stream_test.go covers Plan 04-03: the off-tick chunk worker rejoining the
// tick through the Phase-3 applyAsyncResults seam (WORLD-01 tick side) and the
// center-out view-distance ring streaming with batch framing (WORLD-05). The
// rejoin tests are run under the Docker -race gate to prove that only the immutable
// ChunkResult crosses the off-tick boundary and the tick is the sole mutator.

// newStreamWorld builds a wired TickLoop with a real Superflat-backed Worker and an
// empty ChunkManager, plus the cancel that stops the adapter + worker goroutines. The
// worker always generates (regionDir ""), so a Request deterministically yields a
// solid superflat chunk.
func newStreamWorld(t *testing.T) (*TickLoop, *world.ChunkManager, *world.Worker, context.CancelFunc) {
	t.Helper()
	gen := world.NewSuperflat(24, -64, -16)
	worker := world.NewWorker(gen, "", 256)
	mgr := world.NewChunkManager()

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)

	loop := NewTickLoop(newFakeClock())
	loop.SetWorld(mgr, worker)
	return loop, mgr, worker, cancel
}

// TestChunkReadyRejoin proves WORLD-01 (tick side): a chunkReady carrying an
// immutable ChunkResult, applied through applyAsyncResults on the OWNER goroutine,
// inserts the chunk into the tick-owned ChunkManager and marks the holder Ready. It
// drives a real worker so the generated chunk crosses the boundary exactly as in
// production; the test runs under -race to prove the only crossing is the immutable
// result.
func TestChunkReadyRejoin(t *testing.T) {
	loop, mgr, worker, cancel := newStreamWorld(t)
	defer cancel()

	pos := level.ChunkPos{3, 7}

	// The tick is the sole mutator: it marks Loading, then requests off-tick.
	mgr.MarkLoading(pos)
	worker.Request(pos)

	// Drain the rejoin on the owner goroutine (the same call tickOnce makes) until the
	// result lands. The worker generates asynchronously, so poll a bounded number of
	// times — applyAsyncResults is non-blocking, so we re-drain rather than busy-park.
	deadline := time.Now().Add(2 * time.Second)
	for {
		loop.applyAsyncResults() // owner-goroutine drain (the Phase-3 seam, unchanged)
		if _, ok := mgr.Get(pos); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("chunk was not inserted into the manager via the rejoin within 2s")
		}
		time.Sleep(time.Millisecond)
	}

	ch, ok := mgr.Get(pos)
	if !ok {
		t.Fatal("manager.Get returned not-ready after the rejoin inserted the chunk")
	}
	if ch == nil {
		t.Fatal("rejoin inserted a nil chunk")
	}
}

// TestChunkReadyApplyToInserts proves the unit-level contract of chunkReady.applyTo:
// a successful ChunkResult is inserted on the owner; an error result is NOT inserted
// (and must not panic on a nil chunk), leaving the holder so tickChunks can retry.
func TestChunkReadyApplyToInserts(t *testing.T) {
	mgr := world.NewChunkManager()
	loop := NewTickLoop(newFakeClock())
	loop.world = mgr

	okPos := level.ChunkPos{1, 1}
	ch := level.EmptyChunk(24)
	chunkReady{res: world.ChunkResult{Pos: okPos, Chunk: ch}}.applyTo(loop)
	if got, ok := mgr.Get(okPos); !ok || got != ch {
		t.Fatalf("applyTo did not insert the immutable chunk: got=%v ok=%v", got, ok)
	}

	// Error result: must skip insert (no nil chunk stranded as Ready) and not panic.
	errPos := level.ChunkPos{2, 2}
	mgr.MarkLoading(errPos)
	chunkReady{res: world.ChunkResult{Pos: errPos, Err: context.Canceled}}.applyTo(loop)
	if _, ok := mgr.Get(errPos); ok {
		t.Fatal("applyTo inserted a chunk for an error result; the error path must not insert")
	}
}

// TestRejoinNoopWithoutWorld preserves the Phase-3 contract: a TickLoop constructed
// WITHOUT SetWorld has a nil asyncIn, so applyAsyncResults is a pure no-op (proven
// alongside TestApplyAsyncResultsNoop, but asserted here for the streaming path too).
func TestRejoinNoopWithoutWorld(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	if loop.asyncIn != nil {
		t.Fatal("a TickLoop without SetWorld must keep asyncIn nil (Phase-3 no-op seam)")
	}
	loop.tickChunks()    // no world wired: must be a cheap no-op, never panic
	loop.flushOutbound() // no world wired: must be a cheap no-op, never panic
}

// drainClientPackets reads up to want packets from the client end of a pipe within a
// bounded deadline, returning what it collected. Used by the streaming tests to assert
// the exact clientbound sequence the flush enqueues.
func drainClientPackets(t *testing.T, client clientReader, want int) []pk.Packet {
	t.Helper()
	got := make([]pk.Packet, 0, want)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for len(got) < want {
			var p pk.Packet
			if err := client.ReadPacket(&p); err != nil {
				return
			}
			got = append(got, p)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out reading clientbound packets: got %d of %d", len(got), want)
	}
	return got
}

// clientReader is the minimal read surface of the piped client end.
type clientReader interface {
	ReadPacket(p *pk.Packet) error
}

// TestViewRingCenterOut asserts centerOutRing returns the Chebyshev square ordered
// center-first with non-decreasing distance (WORLD-05 center-out): the player's own
// chunk arrives before the surrounding ring.
func TestViewRingCenterOut(t *testing.T) {
	center := level.ChunkPos{5, 5}
	ring := centerOutRing(center, 2)

	if len(ring) != 25 {
		t.Fatalf("ring for r=2 must have (2*2+1)^2=25 chunks, got %d", len(ring))
	}
	if ring[0] != center {
		t.Fatalf("center-out ring must start at the center %v, got %v", center, ring[0])
	}
	cheb := func(p level.ChunkPos) int {
		dx := int(p[0] - center[0])
		if dx < 0 {
			dx = -dx
		}
		dz := int(p[1] - center[1])
		if dz < 0 {
			dz = -dz
		}
		if dx > dz {
			return dx
		}
		return dz
	}
	prev := -1
	seen := make(map[level.ChunkPos]bool, len(ring))
	for _, p := range ring {
		d := cheb(p)
		if d > 2 {
			t.Fatalf("ring contained pos %v outside radius 2 (cheb=%d)", p, d)
		}
		if d < prev {
			t.Fatalf("ring not ordered center-out: %v (cheb %d) followed a cheb-%d chunk", p, d, prev)
		}
		if seen[p] {
			t.Fatalf("ring contained duplicate pos %v", p)
		}
		seen[p] = true
		prev = d
	}
}

// TestViewDistanceClamp proves the DoS control (T-4-01): a huge requested view is
// capped to serverViewDistance and a tiny request is floored to a usable minimum, so
// the needed-ring size (2r+1)^2 is bounded by the server, never by the client.
func TestViewDistanceClamp(t *testing.T) {
	if got := clampViewDistance(100); got != serverViewDistance {
		t.Fatalf("clampViewDistance(100) = %d, want serverViewDistance=%d", got, serverViewDistance)
	}
	if got := clampViewDistance(1 << 20); got != serverViewDistance {
		t.Fatalf("clampViewDistance(huge) = %d, want serverViewDistance=%d", got, serverViewDistance)
	}
	if got := clampViewDistance(0); got < 2 {
		t.Fatalf("clampViewDistance(0) = %d, want floored to >= 2", got)
	}
	if got := clampViewDistance(-50); got < 2 {
		t.Fatalf("clampViewDistance(-50) = %d, want floored to >= 2", got)
	}
	// The needed-ring is bounded by the clamp regardless of the request.
	r := clampViewDistance(1 << 20)
	bound := (2*r + 1) * (2*r + 1)
	if got := len(centerOutRing(level.ChunkPos{0, 0}, r)); got != bound {
		t.Fatalf("clamped needed-ring size = %d, want bounded (2r+1)^2 = %d", got, bound)
	}
}

// TestTickChunksRequestsRing proves tickChunks marks the bounded ring Loading and
// issues exactly one worker request per Empty chunk, and that a second tickChunks
// issues no new requests (idempotent over the bounded ring — threat T-4-06).
func TestTickChunksRequestsRing(t *testing.T) {
	loop, mgr, _, cancel := newStreamWorld(t)
	defer cancel()

	p := &tickPlayer{center: level.ChunkPos{0, 0}, viewDist: serverViewDistance, sentChunks: map[level.ChunkPos]bool{}}
	loop.players = []*tickPlayer{p}

	ring := centerOutRing(p.center, p.viewDist)

	loop.tickChunks()

	// Every ring chunk must now have a holder (Empty -> Loading creates one), so the
	// manager holds exactly the bounded ring of columns.
	if n := mgr.Len(); n != len(ring) {
		t.Fatalf("tickChunks created %d holders, want one per ring chunk = %d", n, len(ring))
	}

	// A second tickChunks must not re-mark anything (no Empty chunks remain to request),
	// so the column count is unchanged (idempotent over the bounded ring).
	loop.tickChunks()
	if n := mgr.Len(); n != len(ring) {
		t.Fatalf("second tickChunks changed holder count to %d, want stable %d (idempotent)", n, len(ring))
	}
}

// TestPositionSpamBounded proves T-4-06: repeatedly re-walking the same center is
// O(ring) cheap and never enlarges the needed set. We run tickChunks many times and
// assert the manager never holds more than the bounded ring size of columns.
func TestPositionSpamBounded(t *testing.T) {
	loop, mgr, _, cancel := newStreamWorld(t)
	defer cancel()

	p := &tickPlayer{center: level.ChunkPos{0, 0}, viewDist: serverViewDistance, sentChunks: map[level.ChunkPos]bool{}}
	loop.players = []*tickPlayer{p}

	bound := (2*serverViewDistance + 1) * (2*serverViewDistance + 1)
	for i := 0; i < 1000; i++ {
		loop.tickChunks()
		if n := mgr.Len(); n > bound {
			t.Fatalf("position spam grew the manager to %d columns, want <= bounded %d", n, bound)
		}
	}
}

// TestFlushOutboundBatches proves the per-player center-out batched send (WORLD-05):
// SetChunkCacheCenter + SetChunkCacheRadius once, then ChunkBatchStart -> N
// LevelChunkWithLight (center-out) -> ChunkBatchFinished(N), each chunk sent once.
func TestFlushOutboundBatches(t *testing.T) {
	loop, mgr, _, cancel := newStreamWorld(t)
	defer cancel()

	server, client := newPipe(t)
	c := NewClient(server, 256)
	c.Start(make(chan Intent, 16)) // start the writeLoop so enqueued packets reach the pipe

	p := &tickPlayer{client: c, center: level.ChunkPos{0, 0}, viewDist: serverViewDistance, sentChunks: map[level.ChunkPos]bool{}}
	loop.players = []*tickPlayer{p}

	// Make the whole ring Ready directly on the manager (the owner) so the flush has
	// chunks to send without waiting on the worker timing.
	ring := centerOutRing(p.center, p.viewDist)
	for _, pos := range ring {
		mgr.Insert(pos, level.EmptyChunk(24))
	}

	loop.flushOutbound()

	// Expected: SetChunkCacheCenter, SetChunkCacheRadius, ChunkBatchStart, N chunks,
	// ChunkBatchFinished => 3 framing + 1 + N.
	want := 3 + len(ring) + 1
	pkts := drainClientPackets(t, client, want)
	if len(pkts) != want {
		t.Fatalf("flushOutbound sent %d packets, want %d", len(pkts), want)
	}
	if pkts[0].ID != int32(packetid.ClientboundSetChunkCacheCenter) {
		t.Fatalf("first packet = id %#x, want SetChunkCacheCenter", pkts[0].ID)
	}
	if pkts[1].ID != int32(packetid.ClientboundSetChunkCacheRadius) {
		t.Fatalf("second packet = id %#x, want SetChunkCacheRadius", pkts[1].ID)
	}
	if pkts[2].ID != int32(packetid.ClientboundChunkBatchStart) {
		t.Fatalf("third packet = id %#x, want ChunkBatchStart", pkts[2].ID)
	}
	for i := 0; i < len(ring); i++ {
		if pkts[3+i].ID != int32(packetid.ClientboundLevelChunkWithLight) {
			t.Fatalf("packet %d = id %#x, want LevelChunkWithLight", 3+i, pkts[3+i].ID)
		}
	}
	if last := pkts[want-1]; last.ID != int32(packetid.ClientboundChunkBatchFinished) {
		t.Fatalf("last packet = id %#x, want ChunkBatchFinished", last.ID)
	}

	// A second flush must send NOTHING (every ring chunk is already in the sent-set and
	// the center was already sent). Drain with a short deadline expecting zero.
	loop.flushOutbound()
	if extra := tryDrainOne(client); extra {
		t.Fatal("second flushOutbound re-sent packets; each chunk must be sent at most once")
	}
}

// tryDrainOne reports whether one more packet is readable within a short window.
func tryDrainOne(client clientReader) bool {
	got := make(chan bool, 1)
	go func() {
		var p pk.Packet
		err := client.ReadPacket(&p)
		got <- err == nil
	}()
	select {
	case ok := <-got:
		return ok
	case <-time.After(150 * time.Millisecond):
		return false
	}
}
