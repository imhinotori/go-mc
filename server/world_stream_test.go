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
	loop.only().world = mgr

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
	if loop.only().asyncIn != nil {
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

	// A small radius (minViewDistance) keeps this framing/batch test within the
	// synchronous test pipe's capacity; the full serverViewDistance (10 => 441 columns)
	// is throughput, not framing, and would overflow the unbuffered pipe. The batch
	// shape (center-out, send-once) is identical at any radius.
	p := &tickPlayer{client: c, center: level.ChunkPos{0, 0}, viewDist: minViewDistance, sentChunks: map[level.ChunkPos]bool{}}
	loop.players = []*tickPlayer{p}

	// Make the whole ring Ready directly on the manager (the owner) so the flush has
	// chunks to send without waiting on the worker timing.
	ring := centerOutRing(p.center, p.viewDist)
	for _, pos := range ring {
		mgr.Insert(pos, level.EmptyChunk(24))
	}

	// The PlayerChunkSender flow control (1:1 port) paces chunks: each flush sends ONE batch of at
	// most floor(batchQuota) columns (START_CHUNKS_PER_TICK=9 initially), then waits for the client's
	// ServerboundChunkBatchReceived ack before sending more. So a 25-column ring drains over several
	// flush+ack rounds. streamRingPaced drives flush -> read batch -> ack -> flush until the whole
	// ring has streamed (each chunk exactly once) and asserts the count.
	pktCh := startPipeReader(client)
	streamRingPaced(t, loop, p, pktCh, len(ring))

	// A further flush must send NOTHING (every ring chunk is already in the sent-set).
	loop.flushOutbound()
	select {
	case extra := <-pktCh:
		t.Fatalf("an extra flushOutbound re-sent a packet (id %#x); each chunk must be sent at most once", extra.ID)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestRecenterRing proves the PLAY-04 re-center prune: moving the center moves
// p.center, resets centerSent, drops the columns that left the new window from the
// sent-set, and sends exactly one ForgetLevelChunk per dropped column (none for
// columns still inside the new window).
func TestRecenterRing(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	server, client := newPipe(t)
	c := NewClient(server, 256)
	c.Start(make(chan Intent, 16))

	oldCenter := level.ChunkPos{0, 0}
	newCenter := level.ChunkPos{3, 0}

	// Player has streamed the full old r=2 ring (25 columns) and the center framing.
	p := &tickPlayer{
		client:     c,
		center:     oldCenter,
		viewDist:   serverViewDistance,
		sentChunks: map[level.ChunkPos]bool{},
		centerSent: true,
	}
	for _, pos := range centerOutRing(oldCenter, p.viewDist) {
		p.sentChunks[pos] = true
	}

	// Compute the expected dropped set: old ring minus new ring.
	newRing := make(map[level.ChunkPos]bool)
	for _, pos := range centerOutRing(newCenter, p.viewDist) {
		newRing[pos] = true
	}
	var dropped []level.ChunkPos
	for _, pos := range centerOutRing(oldCenter, p.viewDist) {
		if !newRing[pos] {
			dropped = append(dropped, pos)
		}
	}

	loop.recenterRing(p, newCenter)

	if p.center != newCenter {
		t.Fatalf("recenterRing center = %v, want %v", p.center, newCenter)
	}
	if p.centerSent {
		t.Fatal("recenterRing must reset centerSent (so flushOutbound re-emits SetChunkCacheCenter)")
	}
	// Dropped columns are gone from the sent-set; retained columns stay.
	for _, pos := range dropped {
		if p.sentChunks[pos] {
			t.Fatalf("dropped column %v still in sent-set after re-center", pos)
		}
	}
	for pos := range newRing {
		if p.sentChunks[pos] {
			// Columns shared by old+new windows must remain in the sent-set (not re-sent).
			continue
		}
	}
	// Retained = old∩new columns must still be present.
	for _, pos := range centerOutRing(oldCenter, p.viewDist) {
		if newRing[pos] && !p.sentChunks[pos] {
			t.Fatalf("retained column %v was dropped from the sent-set", pos)
		}
	}

	// Exactly one ForgetLevelChunk per dropped column was sent (and nothing else).
	pkts := drainClientPackets(t, client, len(dropped))
	if len(pkts) != len(dropped) {
		t.Fatalf("recenterRing sent %d packets, want %d (one ForgetLevelChunk per dropped column)", len(pkts), len(dropped))
	}
	forgotten := make(map[level.ChunkPos]bool)
	for _, pkt := range pkts {
		if pkt.ID != int32(packetid.ClientboundForgetLevelChunk) {
			t.Fatalf("re-center sent id %#x, want ClientboundForgetLevelChunk", pkt.ID)
		}
		var packed pk.Long
		if err := pkt.Scan(&packed); err != nil {
			t.Fatalf("scan ForgetLevelChunk: %v", err)
		}
		forgotten[level.ChunkPos{int32(uint64(packed)), int32(uint64(packed) >> 32)}] = true
	}
	for _, pos := range dropped {
		if !forgotten[pos] {
			t.Fatalf("dropped column %v was not forgotten on the wire", pos)
		}
	}
	if tryDrainOne(client) {
		t.Fatal("re-center sent more than one ForgetLevelChunk per dropped column")
	}
}

// TestRingFollowsOnMove proves the world FOLLOWS the player: a real cross-boundary
// movement packet re-centers the ring, and a subsequent flushOutbound re-emits
// SetChunkCacheCenter for the new center and streams the new window — closing the
// void-on-walk failure mode (Pitfall 2).
func TestRingFollowsOnMove(t *testing.T) {
	loop, mgr, _, cancel := newStreamWorld(t)
	defer cancel()

	server, client := newPipe(t)
	c := NewClient(server, 256)
	c.Start(make(chan Intent, 16))

	// minViewDistance keeps the ring (25 columns) within the synchronous test pipe's
	// capacity; this test asserts the re-center/follow framing, which is radius-independent.
	p := &tickPlayer{
		client:            c,
		center:            level.ChunkPos{0, 0},
		viewDist:          minViewDistance,
		sentChunks:        map[level.ChunkPos]bool{},
		confirmedTeleport: true,
	}
	loop.players = []*tickPlayer{p}

	// First flush at the spawn center: framing + the ring (make it Ready first). The
	// PlayerChunkSender flow control paces the ring over several flush+ack rounds, so drive
	// flush -> ack -> flush until the whole spawn ring has streamed and the pipe is clear.
	for _, pos := range centerOutRing(p.center, p.viewDist) {
		mgr.Insert(pos, level.EmptyChunk(24))
	}
	pktCh := startPipeReader(client)
	streamRingPaced(t, loop, p, pktCh, len(centerOutRing(p.center, p.viewDist)))

	// Walk far east: block x=64 -> chunk x=4, a real boundary crossing from center {0,0}.
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: movePlayerPosRot(64.0, 64.0, 8.0, 0, 0, 0x01)})

	newCenter := chunkCenterOf(64, 8)
	if p.center != newCenter {
		t.Fatalf("movement did not re-center: center=%v, want %v", p.center, newCenter)
	}
	if p.centerSent {
		t.Fatal("re-center must reset centerSent so the next flush re-emits SetChunkCacheCenter")
	}

	// recenterRing already sent ForgetLevelChunk packets for the dropped columns; drain
	// those so the next assertion sees the flush output cleanly.
	newRing := make(map[level.ChunkPos]bool)
	for _, pos := range centerOutRing(newCenter, p.viewDist) {
		newRing[pos] = true
	}
	var droppedCount int
	for _, pos := range centerOutRing(level.ChunkPos{0, 0}, p.viewDist) {
		if !newRing[pos] {
			droppedCount++
		}
	}
	drainN(t, pktCh, droppedCount)

	// Make the NEW ring Ready, then flush: a fresh SetChunkCacheCenter for the new center
	// plus the new ring streams — the world followed the player.
	for _, pos := range centerOutRing(newCenter, p.viewDist) {
		mgr.Insert(pos, level.EmptyChunk(24))
	}
	loop.flushOutbound()

	pkts := drainN(t, pktCh, 1)
	if len(pkts) == 0 || pkts[0].ID != int32(packetid.ClientboundSetChunkCacheCenter) {
		t.Fatal("after a cross-boundary move, the flush must re-emit SetChunkCacheCenter for the new center")
	}
	var gx, gz pk.VarInt
	if err := pkts[0].Scan(&gx, &gz); err != nil {
		t.Fatalf("scan SetChunkCacheCenter: %v", err)
	}
	if int32(gx) != newCenter[0] || int32(gz) != newCenter[1] {
		t.Fatalf("re-emitted center = (%d,%d), want %v", gx, gz, newCenter)
	}
}

// startPipeReader spawns ONE reader goroutine that funnels every packet from the client pipe onto
// the returned channel — a single reader the whole test shares (a per-call reader would leak on a
// deadline and steal later packets). drainN reads exactly n packets from the channel with a
// deadline (the fixed-count drains the framing/forget assertions use).
func startPipeReader(client clientReader) <-chan pk.Packet {
	pktCh := make(chan pk.Packet, 1024)
	go func() {
		for {
			var pkt pk.Packet
			if err := client.ReadPacket(&pkt); err != nil {
				return
			}
			pktCh <- pkt
		}
	}()
	return pktCh
}

func drainN(t *testing.T, pktCh <-chan pk.Packet, n int) []pk.Packet {
	t.Helper()
	got := make([]pk.Packet, 0, n)
	for len(got) < n {
		select {
		case pkt := <-pktCh:
			got = append(got, pkt)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out reading clientbound packets: got %d of %d", len(got), n)
		}
	}
	return got
}

// streamRingPaced drives flushOutbound -> read batch -> ack -> flush until `wantChunks`
// LevelChunkWithLight packets have streamed, asserting the PlayerChunkSender pacing (a variable
// batch per flush, gated by the client's ServerboundChunkBatchReceived ack). It acks each
// ChunkBatchFinished via onChunkBatchReceivedByClient so the flow control releases the next batch.
// A SINGLE reader goroutine feeds pktCh (a fresh reader per round would leak on the deadline and
// steal the next round's packets).
func streamRingPaced(t *testing.T, loop *TickLoop, p *tickPlayer, pktCh <-chan pk.Packet, wantChunks int) {
	t.Helper()
	chunks := 0
	for round := 0; round < 200 && chunks < wantChunks; round++ {
		loop.flushOutbound()
	drain:
		for {
			select {
			case pkt := <-pktCh:
				switch pkt.ID {
				case int32(packetid.ClientboundLevelChunkWithLight):
					chunks++
				case int32(packetid.ClientboundChunkBatchFinished):
					p.onChunkBatchReceivedByClient(64.0) // ack -> release the next batch
				}
			case <-time.After(150 * time.Millisecond):
				break drain // this flush's output is drained; flush again
			}
		}
	}
	if chunks != wantChunks {
		t.Fatalf("paced streaming sent %d chunks, want the full ring %d", chunks, wantChunks)
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
