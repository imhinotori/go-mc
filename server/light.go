package server

import (
	"sort"
	"sync"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// lightMinSectionY / lightSectionCount are the overworld light geometry the incremental relight passes to
// ChunkManager.RelightEdit — the same minY>>4 / height>>4 the generator uses to seal a fresh chunk's light
// (world/generator.go: minY>>4, height>>4 == -4, 24). v1 single-dimension overworld; a future
// multi-dimension wiring threads the real per-dimension geometry here (mirrors dimMinY's note).
const (
	lightMinSectionY  = dimMinY >> 4 // -64 >> 4 == -4
	lightSectionCount = 384 >> 4     // 24 block sections (overworld height 384)
)

// relightQueue is the race-safe, deterministic fan-in behind the per-tick batched relight
// (server/light.go flushRelight). It replaces the pre-fix plain `map[int]map[level.ChunkPos]
// pk.Position` whose mutation ran on the tick goroutine while the N=2 region fan-out
// (server/region_coordinator.go tickOnce) ran in PARALLEL — snowGolemAiStep, fluid SetBlock, etc.
// call ChunkManager.SetBlock from a region goroutine, the central hook fired relightChanged, and
// two regions writing the same map produced concurrent-map-write races (CR-01).
//
// The queue is a tiny three-method abstraction:
//   - add:    WRITES from any goroutine under the mutex; coalesces duplicate columns per dimension.
//   - drain:  ATOMICALLY swaps the pending map for an empty one and returns the snapshot —
//     the lock is held only for the swap (cheap), never across the expensive RelightColumns /
//     packet send.
//   - mergeCarry: merges budget-deferred columns back into the queue (deduped against any writes that
//     arrived during processing) so the next flushRelight processes them.
//
// Set semantics are preserved by using a map per dimension (a column seen multiple times inside a
// tick collapses to a single entry, exactly as before). Order is enforced DOWNSTREAM in flushRelight
// (sort dim, then sort (cx, cz)) so the queue itself stays a small, lock-friendly set-of-fifos.
type relightQueue struct {
	mu              sync.Mutex
	nextSequence    uint64
	pending         map[int]map[level.ChunkPos]pk.Position
	pendingSequence map[int]map[level.ChunkPos]uint64
}

// newRelightQueue constructs a ready-to-use queue with the inner map allocated so the hot
// path (add on the first edit) does not lazily allocate while holding the mutex.
func newRelightQueue() *relightQueue {
	return &relightQueue{
		pending:         make(map[int]map[level.ChunkPos]pk.Position),
		pendingSequence: make(map[int]map[level.ChunkPos]uint64),
	}
}

// add records a light-affecting edit at (dim, col). The representative block position is
// retained so flushRelight can carry it back if the column is budget-deferred this tick.
// Coalesces duplicate columns in the current tick window (a column seen N times adds once).
// Cheap under the mutex: O(M) over the current dimension's column set, where M is small
// (single-digit to low-double-digit in steady state; bounded by the carry-over from prior
// ticks when fluid bursts happen). Race-safe across the N=2 fan-out (CR-01 closure).
func (q *relightQueue) add(dim int, col level.ChunkPos, pos pk.Position) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending == nil {
		q.pending = make(map[int]map[level.ChunkPos]pk.Position)
	}
	if q.pendingSequence == nil {
		q.pendingSequence = make(map[int]map[level.ChunkPos]uint64)
	}
	byCol, ok := q.pending[dim]
	if !ok {
		byCol = make(map[level.ChunkPos]pk.Position)
		q.pending[dim] = byCol
	}
	if _, seen := byCol[col]; !seen {
		byCol[col] = pos
		bySequence := q.pendingSequence[dim]
		if bySequence == nil {
			bySequence = make(map[level.ChunkPos]uint64)
			q.pendingSequence[dim] = bySequence
		}
		bySequence[col] = q.nextSequence
		q.nextSequence++
	}
}

// drain atomically detaches the current pending FIFOs and returns them as a snapshot. The
// returned map is OWNED by the caller — flushRelight sorts/walks it freely without the lock.
// New writes arriving on any goroutine after drain() land in the freshly-allocated empty
// pending map; the eventual mergeCarry() folds the carry (deferred columns) back into that
// live queue, deduped against the in-flight arrivals.
func (q *relightQueue) drain() map[int]map[level.ChunkPos]pk.Position {
	snap, _ := q.drainBatch()
	return snap
}

func (q *relightQueue) drainBatch() (map[int]map[level.ChunkPos]pk.Position, map[int]map[level.ChunkPos]uint64) {
	if q == nil {
		return nil, nil
	}
	q.mu.Lock()
	snap := q.pending
	sequence := q.pendingSequence
	q.pending = make(map[int]map[level.ChunkPos]pk.Position)
	q.pendingSequence = make(map[int]map[level.ChunkPos]uint64)
	q.mu.Unlock()
	return snap, sequence
}

// mergeCarry folds the budget-deferred columns from this tick back into the live queue. Each
// deferred column is inserted only if it is NOT already pending — a column re-edited during
// the (expensive) processing window stays a single entry across the snap + carry merge. A
// nil/empty carry is a no-op.
func (q *relightQueue) mergeCarry(carry map[int]map[level.ChunkPos]pk.Position) {
	for dim, byCol := range carry {
		for col, pos := range byCol {
			q.add(dim, col, pos)
		}
	}
}

func (q *relightQueue) mergeCarryBatch(carry map[int]map[level.ChunkPos]pk.Position, carrySequence map[int]map[level.ChunkPos]uint64) {
	if q == nil || len(carry) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending == nil {
		q.pending = make(map[int]map[level.ChunkPos]pk.Position)
	}
	if q.pendingSequence == nil {
		q.pendingSequence = make(map[int]map[level.ChunkPos]uint64)
	}
	for dim, byCol := range carry {
		existing, ok := q.pending[dim]
		if !ok {
			// Copy carry into pending so the next flushRelight sees exactly the deferred set.
			clone := make(map[level.ChunkPos]pk.Position, len(byCol))
			for c, p := range byCol {
				clone[c] = p
			}
			q.pending[dim] = clone
			sequenceClone := make(map[level.ChunkPos]uint64, len(byCol))
			for c := range byCol {
				sequenceClone[c] = carrySequence[dim][c]
			}
			q.pendingSequence[dim] = sequenceClone
			continue
		}
		existingSequence := q.pendingSequence[dim]
		if existingSequence == nil {
			existingSequence = make(map[level.ChunkPos]uint64)
			q.pendingSequence[dim] = existingSequence
		}
		for c, p := range byCol {
			carrySeq := carrySequence[dim][c]
			pendingSeq, seen := existingSequence[c]
			if !seen {
				existing[c] = p
				existingSequence[c] = carrySeq
			} else if carrySeq < pendingSeq {
				// A re-edit while relighting must not make older deferred work young again.
				existingSequence[c] = carrySeq
			}
		}
	}
}

// relightOnEdit is the incremental-relight seam that fires after a light-affecting block edit — the Go
// realization of LevelChunk.setBlockState's `if (LightEngine.hasDifferentLightProperties(old, new))
// getLightEngine().checkBlock(pos)` followed by the ThreadedLevelLightEngine emitting
// ClientboundLightUpdatePacket to the column's trackers. It gates on LightPropertiesDiffer (no relight when
// dampening/emission/occlusion are unchanged), recomputes the affected columns' light over the live loaded
// chunks (world.RelightEdit), and pushes a ClientboundLightUpdate for every column whose light actually
// changed to every player tracking that column. Tick-owned (runs on the tick goroutine). CITE:
// LevelChunk.setBlockState (hasDifferentLightProperties -> checkBlock) + ChunkMap's light-update broadcast.
// installRelightHook registers relightChanged as the CENTRAL block-change callback on a dimension's
// ChunkManager (world.SetBlockChangeHook). After this, EVERY changed SetBlock in that dimension --
// player build/break, fluid flow (lava emits 15), fire, explosions, pistons, growth, dispensers,
// silverfish, enderman/ravager/snow-golem, command setblock -- re-propagates light and broadcasts the
// update, exactly as vanilla funnels every LevelChunk.setBlockState through checkBlock. Called once
// per dimension at world wiring on the tick goroutine. CITE: LevelChunk.setBlockState -> checkBlock.
func (t *TickLoop) installRelightHook(w *world.ChunkManager, dim int) {
	if w == nil {
		return
	}
	w.SetBlockChangeHook(func(pos pk.Position, oldState, newState block.StateID) {
		t.relightChanged(dim, w, pos, oldState, newState)
	})
}

// relightChanged appends a light-affecting edit to the per-dimension dirty queue. CALLED FROM
// ANY GOROUTINE — the N=2 fan-out runs region.tick concurrently (server/region_coordinator.go
// tickOnce) and a region's AI/physics SetBlock funnels through here on that region's goroutine
// (snowGolemAiStep, fluid flow, enderman carry, ...). The pre-fix plain map mutated under the
// same hot path raced the parallel regions (CR-01); the queue's mutex makes add() race-safe
// (a coalesced enqueue, never an inline relight).
func (t *TickLoop) relightChanged(dim int, w *world.ChunkManager, pos pk.Position, oldState, newState block.StateID) {
	if !world.LightPropertiesDiffer(oldState, newState) {
		return // light properties unchanged: no checkBlock, no relight (the hasDifferentLightProperties gate)
	}
	if w == nil {
		return
	}
	// BATCH (do NOT recompute + broadcast inline): enqueue the EDITED column into the per-dimension
	// dirty queue. flushRelight (coordinator, once per tick) recomputes the union of affected columns
	// and broadcasts. Fluid simulation writes thousands of light-affecting blocks per tick; the former
	// inline RelightEdit recomputed 9 full chunk columns via the light engine per SetBlock, a 100-140ms
	// stall per fluid cell. Deduping by column collapses those thousands of writes to at most one entry
	// per column, and flushRelight recomputes each affected column exactly ONCE over the FINAL block
	// states -- byte-identical light (last recompute wins), one ClientboundLightUpdate per column per
	// tick, exactly as vanilla batches checkBlock -> runLightUpdates -> ClientboundLightUpdate.
	// CITE: LevelChunk.setBlockState (checkBlock) + ThreadedLevelLightEngine.runLightUpdates.
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	t.dirtyRelight.add(dim, col, pos)
}

// lightGeometryFor returns (minSectionY, sectionCount, hasSkyLight) for a dimension — the
// per-dim geometry the relight engine consumes (mirrors dimMinYFor/dimSecsFor for the
// streaming path). Lifted out of flushRelight so the helper is testable in isolation.
func lightGeometryFor(dim int) (int, int, bool) {
	switch dim {
	case dimNether:
		return dimNetherMinY >> 4, dimNetherSecs, false
	case dimEnd:
		return dimEndMinY >> 4, dimEndSecs, false
	default:
		return lightMinSectionY, lightSectionCount, true
	}
}

// flushRelight drains the per-tick batched-relight dirty queue: for each dimension with dirty
// edited columns it builds the UNION of (each dirty edited column + its 8 neighbors) -- the
// full set of columns whose stored light an edit in a dirty column can change (light
// propagates up to 15 blocks, so a border edit changes a neighbor column too) -- dedups that
// union, recomputes EACH affected column exactly once over the live (post-edit, FINAL) block
// states via ChunkManager.RelightColumns (snapshot-before / recompute-each-once / diff), and
// broadcasts one world.WriteLightUpdate ClientboundLightUpdate for every column whose light
// actually CHANGED to every player tracking that column in that dimension. It then re-queues
// any budget-deferred columns and merges them back into the live queue.
//
// This is the coordinator-side, once-per-tick realization of vanilla's
// ThreadedLevelLightEngine: LevelChunk.setBlockState enqueues checkBlock(pos) nodes, the engine
// coalesces them and runs runLightUpdates ONCE per tick, and ChunkMap emits each affected
// column's ClientboundLightUpdate once per tick. Recomputing a column once at end-of-tick over
// the final states yields the SAME light as recomputing after every intermediate edit (the
// redundant intermediate recomputes are elided), so this is a pure OPTIMIZATION: identical
// observable light, one packet per column per tick instead of thousands. Runs on the
// coordinator (single-threaded, post-barrier) -- the same quiescent window as the other global
// post-phases. Nether/End dirty columns relight in THEIR ChunkManager (dimWorldByID), at their
// own geometry. CITE: LevelChunk.setBlockState checkBlock ->
// ThreadedLevelLightEngine.runLightUpdates -> ChunkMap ClientboundLightUpdate (once per tick).
//
// maxRelightColumnsPerTick caps the size of the deduped affected union per tick. A SINGLE dirty
// edit necessarily drags in a 3x3 (9 columns), so the budget is expressed in multiples of 9 —
// the audit's "coherent budget that never splits that required neighborhood" contract (WR-01).
// 1 block (9 cols) is the minimum coherent budget; the previous value of 8 fit zero disjoint
// edits and silently deferred every column from the second onward (the len(affected)>=8 gate
// fired AFTER the first 3x3 was already in `affected`). Keep the effective old ceiling at one
// full 3x3 rather than doubling its worst-case work. Adjacent
// dirty columns whose 3x3 overlaps the already-accepted union add ZERO new columns and are
// accepted while they fit; far columns carry to the next tick in FIFO sequence order.
const maxRelightColumnsPerTick = 9

// planColumn iterates the snapshot's edited columns in DETERMINISTIC order (dim ascending,
// then (cx, cz) ascending) and partitions them into the deduped union of accepted columns
// plus a per-dim carry of budget-deferred columns. NEVER mutates the snapshot. The split
// rule per edited column: if adding its 3x3 would push the union beyond maxRelightColumnsPerTick
// (counting ONLY new columns), the entire edited column is carried; otherwise the 3x3 is
// folded in (overlapping cols are deduped, so adjacent edits cost zero extra columns).
// Exposed so tests can pin the deterministic plan/order without driving the full manager.
func planRelightForDim(snap map[level.ChunkPos]pk.Position, maxUnion int) ([]level.ChunkPos, map[level.ChunkPos]pk.Position) {
	sequence := make(map[level.ChunkPos]uint64, len(snap))
	cols := make([]level.ChunkPos, 0, len(snap))
	for col := range snap {
		cols = append(cols, col)
	}
	sort.Slice(cols, func(i, j int) bool {
		if cols[i][0] != cols[j][0] {
			return cols[i][0] < cols[j][0]
		}
		return cols[i][1] < cols[j][1]
	})
	for i, col := range cols {
		sequence[col] = uint64(i)
	}
	accepted, carry, _ := planRelightForDimFIFO(snap, sequence, maxUnion)
	return accepted, carry
}

func planRelightForDimFIFO(snap map[level.ChunkPos]pk.Position, sequence map[level.ChunkPos]uint64, maxUnion int) (accepted []level.ChunkPos, carry map[level.ChunkPos]pk.Position, carrySequence map[level.ChunkPos]uint64) {
	if len(snap) == 0 {
		return nil, nil, nil
	}
	cols := make([]level.ChunkPos, 0, len(snap))
	for c := range snap {
		cols = append(cols, c)
	}
	sort.Slice(cols, func(i, j int) bool {
		if sequence[cols[i]] != sequence[cols[j]] {
			return sequence[cols[i]] < sequence[cols[j]]
		}
		if cols[i][0] != cols[j][0] {
			return cols[i][0] < cols[j][0]
		}
		return cols[i][1] < cols[j][1]
	})
	union := make(map[level.ChunkPos]struct{}, maxUnion+9)
	accepted = make([]level.ChunkPos, 0, maxUnion+9)
	for _, c := range cols {
		// Count NEW columns this 3x3 contributes to the union.
		newCount := 0
		for dx := int32(-1); dx <= 1; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				nc := level.ChunkPos{c[0] + dx, c[1] + dz}
				if _, seen := union[nc]; !seen {
					newCount++
				}
			}
		}
		if len(union)+newCount > maxUnion {
			// Coherent budget: defer the WHOLE edited column (its 3x3 is unsplittable).
			if carry == nil {
				carry = make(map[level.ChunkPos]pk.Position)
				carrySequence = make(map[level.ChunkPos]uint64)
			}
			carry[c] = snap[c]
			carrySequence[c] = sequence[c]
			continue
		}
		for dx := int32(-1); dx <= 1; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				nc := level.ChunkPos{c[0] + dx, c[1] + dz}
				if _, seen := union[nc]; seen {
					continue
				}
				union[nc] = struct{}{}
				accepted = append(accepted, nc)
			}
		}
	}
	sort.Slice(accepted, func(i, j int) bool {
		if accepted[i][0] != accepted[j][0] {
			return accepted[i][0] < accepted[j][0]
		}
		return accepted[i][1] < accepted[j][1]
	})
	return accepted, carry, carrySequence
}

func (t *TickLoop) flushRelight() {
	snap, sequence := t.dirtyRelight.drainBatch()
	if len(snap) == 0 {
		return
	}
	air := block.ToStateID[block.Air{}]
	// DETERMINISTIC DIMENSION ORDER: iterate snapshot dims in ascending numeric order so the
	// plan/order is reproducible across runs (a snapshot is a map, range order is random).
	dims := make([]int, 0, len(snap))
	for dim := range snap {
		dims = append(dims, dim)
	}
	sort.Ints(dims)

	carry := make(map[int]map[level.ChunkPos]pk.Position)
	carrySequence := make(map[int]map[level.ChunkPos]uint64)
	for _, dim := range dims {
		byCol := snap[dim]
		if len(byCol) == 0 {
			continue
		}
		w := t.dimWorldByID(dim)
		if w == nil {
			continue
		}
		// Per-dimension section geometry + sky engine (matches the old inline relightChanged exactly):
		// overworld minSec=lightMinSectionY secs=lightSectionCount hasSkyLight=true; nether/end their own.
		minSec, secs, hasSkyLight := lightGeometryFor(dim)

		// PER-TICK RECOMPUTE BUDGET (perf, simulation-preserving): a large fluid-settling / chunk-load
		// burst can dirty many EDITED columns in one tick, and each edited column drags in a 3x3 recompute
		// (self + 8 neighbors, each a full ComputeChunkLight over 24 sections ~= 4ms). Recomputing the whole
		// union in one tick was a ~230ms flushRelight stall. The budget caps the union at
		// maxRelightColumnsPerTick columns (>= one full 3x3) and DEFERs any edited column whose 3x3 would
		// overflow it; the deferred columns carry to the next tick via mergeCarry. Adjacent edits whose
		// 3x3 overlaps the already-accepted union cost ZERO new columns and are accepted for free, so
		// bursty locality costs nothing extra; far edits are carried in deterministic (cx, cz) order.
		// Light is a pure function of the FINAL block states, so a column relit one-or-more ticks later
		// converges to the identical value -- only spread across ticks, never wrong.
		affected, deferredCols, deferredSequence := planRelightForDimFIFO(byCol, sequence[dim], maxRelightColumnsPerTick)
		changed := w.RelightColumns(affected, minSec, secs, air, hasSkyLight)
		for _, cl := range changed {
			packet := world.WriteLightUpdate(cl)
			col := chunkCenterOf(cl.Pos[0]*16, cl.Pos[1]*16)
			for _, pl := range t.players {
				if pl.client == nil {
					continue
				}
				if pl.dimension != dim {
					continue // light update is for THIS dimension's column; a same-coord player in another dimension must not receive it
				}
				if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
					pl.client.Send(packet)
				}
			}
		}
		if len(deferredCols) > 0 {
			carry[dim] = deferredCols
			carrySequence[dim] = deferredSequence
		}
	}
	// Re-queue any budget-deferred columns; writes that arrived during the (expensive) RelightColumns
	// + packet send window live in the queue's freshly-allocated pending map and are merged with
	// the carry via dedup (a column edited twice in the same window stays a single entry).
	t.dirtyRelight.mergeCarryBatch(carry, carrySequence)
}

// server light READ seams — the tick-side getRawBrightness / getMaxLocalRawBrightness now backed by
// the real per-section light the LevelLightEngine computes at chunk finalize (world/light.go),
// replacing the former cited-constant 15. CITE: net.minecraft.world.level.lighting.LevelLightEngine
//.getRawBrightness + net.minecraft.world.level.Level.getMaxLocalRawBrightness.

// skyDarkenDay is the ambient-darkness term at full DAY (Level.getSkyDarken() == 0 when the sky light
// level is 15). getSkyDarken is now driven by the real SKY_LIGHT_LEVEL timeline (env_timeline.go) and
// maxLocalRawBrightness reads t.getSkyDarken() directly, so this constant is NO LONGER the getSkyDarken
// stand-in. It survives only as the DAY-pinned ambient term the DaylightDetector approximation still
// subtracts (redstone_blocks.go), whose faithful path is the separately-deferred SUN_ANGLE /
// getEffectiveSkyBrightness read (DaylightDetectorBlock.updateSignalStrength), not getSkyDarken. CITE:
// Level.updateSkyBrightness (skyDarken = 15 - SKY_LIGHT_LEVEL); at day == 0.
const skyDarkenDay = 0

// rawBrightness ports LevelLightEngine.getRawBrightness(pos, ambientDarkness) over the tick-owned
// world. A nil world (unit-test loop with no chunk manager) falls back to full brightness (15) so
// the light gates behave exactly as the previous cited-constant stub did in tests. CITE:
// LevelLightEngine.getRawBrightness.
func (t *TickLoop) rawBrightness(pos pk.Position, ambientDarkness int) int {
	w := t.world()
	if w == nil {
		return 15
	}
	return w.RawBrightness(pos, ambientDarkness, dimMinY)
}

// maxLocalRawBrightness ports Level.getMaxLocalRawBrightness(pos) = getRawBrightness(pos,
// getSkyDarken()). getSkyDarken() is now the REAL day/night sky-darkness value from the SKY_LIGHT_LEVEL
// timeline (env_timeline.go), not the former DAY-pinned constant, so this darkens surface light at
// night exactly as vanilla. The X/Z far-bounds guard (>= 30000000 => 15) is mirrored for parity. CITE:
// Level.getMaxLocalRawBrightness.
func (t *TickLoop) maxLocalRawBrightness(pos pk.Position) int {
	if pos.X < -30000000 || pos.Z < -30000000 || pos.X >= 30000000 || pos.Z >= 30000000 {
		return 15
	}
	return t.rawBrightness(pos, t.getSkyDarken())
}

// getBrightnessSky ports BlockAndLightGetter.getBrightness(LightLayer.SKY, pos) =
// getLightEngine().getLayerListener(SKY).getLightValue(pos): the RAW stored sky-light value at pos
// (NOT sky-darkened -- day/night darkening is applied separately via getSkyDarken() in
// getRawBrightness). At a surface cell open to the sky this is 15 regardless of time of day; deep
// underground it is 0. A nil world (unit-test loop with no chunk manager) falls back to 15 so the
// spawn light gate behaves as it did under the previous cited-constant stub. CITE:
// net.minecraft.world.level.BlockAndLightGetter.getBrightness(LightLayer, BlockPos).
func (t *TickLoop) getBrightnessSky(pos pk.Position) int {
	w := t.world()
	if w == nil {
		return 15
	}
	return w.SkyBrightness(pos, dimMinY)
}

// getBrightnessBlock ports BlockAndLightGetter.getBrightness(LightLayer.BLOCK, pos): the stored
// block-light value at pos (torches/lava/etc.), independent of sky light and day/night. A nil world
// falls back to 0 (no block light) so the spawn light gate's overworld BLOCK-limit branch (limit 0:
// any block light blocks the spawn) does not spuriously reject in a manager-less unit test. CITE:
// net.minecraft.world.level.BlockAndLightGetter.getBrightness(LightLayer, BlockPos).
func (t *TickLoop) getBrightnessBlock(pos pk.Position) int {
	w := t.world()
	if w == nil {
		return 0
	}
	return w.BlockBrightness(pos, dimMinY)
}
