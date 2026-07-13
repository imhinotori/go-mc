package server

import (
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

func (t *TickLoop) relightChanged(dim int, w *world.ChunkManager, pos pk.Position, oldState, newState block.StateID) {
	if !world.LightPropertiesDiffer(oldState, newState) {
		return // light properties unchanged: no checkBlock, no relight (the hasDifferentLightProperties gate)
	}
	if w == nil {
		return
	}
	// BATCH (do NOT recompute + broadcast inline): record the EDITED column into the per-dimension
	// dirty set. flushRelight (coordinator, once per tick) recomputes the union of affected columns and
	// broadcasts. Fluid simulation writes thousands of light-affecting blocks per tick; the former
	// inline RelightEdit recomputed 9 full chunk columns via the light engine per SetBlock, a 100-140ms
	// stall per fluid cell. Deduping by column collapses those thousands of writes to at most one entry
	// per column, and flushRelight recomputes each affected column exactly ONCE over the FINAL block
	// states -- byte-identical light (last recompute wins), one ClientboundLightUpdate per column per
	// tick, exactly as vanilla batches checkBlock -> runLightUpdates -> ClientboundLightUpdate.
	// CITE: LevelChunk.setBlockState (checkBlock) + ThreadedLevelLightEngine.runLightUpdates.
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	if t.dirtyRelight == nil {
		t.dirtyRelight = make(map[int]map[level.ChunkPos]pk.Position)
	}
	byCol := t.dirtyRelight[dim]
	if byCol == nil {
		byCol = make(map[level.ChunkPos]pk.Position)
		t.dirtyRelight[dim] = byCol
	}
	// One representative block pos per edited column is enough (RelightColumns works per column, not per
	// exact block); keep the first-seen so re-edits in the same column stay a single entry.
	if _, ok := byCol[col]; !ok {
		byCol[col] = pos
	}
}

// flushRelight drains the per-tick batched-relight dirty set: for each dimension with dirty edited
// columns it builds the UNION of (each dirty edited column + its 8 neighbors) -- the full set of
// columns whose stored light an edit in a dirty column can change (light propagates up to 15 blocks,
// so a border edit changes a neighbor column too) -- dedups that union, recomputes EACH affected
// column exactly once over the live (post-edit, FINAL) block states via ChunkManager.RelightColumns
// (snapshot-before / recompute-each-once / diff), and broadcasts one world.WriteLightUpdate
// ClientboundLightUpdate for every column whose light actually CHANGED to every player tracking that
// column in that dimension. It then clears the dirty set.
//
// This is the coordinator-side, once-per-tick realization of vanilla's ThreadedLevelLightEngine:
// LevelChunk.setBlockState enqueues checkBlock(pos) nodes, the engine coalesces them and runs
// runLightUpdates ONCE per tick, and ChunkMap emits each affected column's ClientboundLightUpdate once
// per tick. Recomputing a column once at end-of-tick over the final states yields the SAME light as
// recomputing after every intermediate edit (the redundant intermediate recomputes are elided), so
// this is a pure OPTIMIZATION: identical observable light, one packet per column per tick instead of
// thousands. Runs on the coordinator (single-threaded, post-barrier) -- the same quiescent window as
// the other global post-phases. Nether/End dirty columns relight in THEIR ChunkManager
// (dimWorldByID), at their own geometry. CITE: LevelChunk.setBlockState checkBlock ->
// ThreadedLevelLightEngine.runLightUpdates -> ChunkMap ClientboundLightUpdate (once per tick).
// maxRelightColumnsPerTick caps how many columns flushRelight actually RECOMPUTES per tick (the size of
// the deduped affected union, each a full ComputeChunkLight over 24 sections ~= 4ms). A large fluid-
// settling / chunk-load burst can dirty many columns; recomputing the whole union in one tick was a
// ~230ms stall. 8 recomputes ~= 32ms worst case keeps flushRelight inside the tick budget; overflow
// edited columns defer to the next tick. Light is a pure function of the FINAL block states, so a
// deferred column converges to the identical value — the same simulation-preserving bound the fluid
// pass uses (a settling burst spreads its light updates over a few ticks, invisible to the client).
const maxRelightColumnsPerTick = 8

func (t *TickLoop) flushRelight() {
	if len(t.dirtyRelight) == 0 {
		return
	}
	air := block.ToStateID[block.Air{}]
	carry := make(map[int]map[level.ChunkPos]pk.Position)
	for dim, byCol := range t.dirtyRelight {
		if len(byCol) == 0 {
			continue
		}
		w := t.dimWorldByID(dim)
		if w == nil {
			continue
		}
		// Per-dimension section geometry + sky engine (matches the old inline relightChanged exactly):
		// overworld minSec=lightMinSectionY secs=lightSectionCount hasSkyLight=true; nether/end their own.
		minSec := lightMinSectionY
		secs := lightSectionCount
		hasSkyLight := true
		if dim == dimNether {
			minSec = dimNetherMinY >> 4
			secs = dimNetherSecs
			hasSkyLight = false
		}
		if dim == dimEnd {
			minSec = dimEndMinY >> 4
			secs = dimEndSecs
			hasSkyLight = false
		}
		// PER-TICK RECOMPUTE BUDGET (perf, simulation-preserving): a large fluid-settling / chunk-load
		// burst can dirty many EDITED columns in one tick, and each edited column drags in a 3x3 recompute
		// (self + 8 neighbors, each a full ComputeChunkLight over 24 sections ~= 4ms). Recomputing the whole
		// union in one tick was a ~230ms flushRelight stall. Bound the actual RECOMPUTES per tick at
		// maxRelightColumnsPerTick by taking edited columns until the deduped union reaches the cap, and
		// DEFER every remaining edited column to the next tick (re-inserted into the fresh dirty set below).
		// Light is a pure function of the FINAL block states, so a column relit one-or-more ticks later
		// converges to the identical value — only spread across ticks, never wrong (the fluid pass's bound).
		var deferredCols map[level.ChunkPos]pk.Position
		union := make(map[level.ChunkPos]struct{}, maxRelightColumnsPerTick+9)
		affected := make([]level.ChunkPos, 0, maxRelightColumnsPerTick+9)
		for c, rep := range byCol {
			if len(affected) >= maxRelightColumnsPerTick {
				// Budget spent: defer this edited column (and, by the loop, all remaining) to next tick.
				if deferredCols == nil {
					deferredCols = make(map[level.ChunkPos]pk.Position)
				}
				deferredCols[c] = rep
				continue
			}
			for dx := int32(-1); dx <= 1; dx++ {
				for dz := int32(-1); dz <= 1; dz++ {
					nc := level.ChunkPos{c[0] + dx, c[1] + dz}
					if _, seen := union[nc]; seen {
						continue
					}
					union[nc] = struct{}{}
					affected = append(affected, nc)
				}
			}
		}
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
		// Carry any budget-deferred edited columns into the next tick's dirty set.
		if len(deferredCols) > 0 {
			carry[dim] = deferredCols
		}
	}
	// Replace the dirty set with only the budget-deferred columns (empty when nothing was deferred),
	// so processed columns are cleared and overflow relights next tick.
	t.dirtyRelight = carry
}

// server light READ seams — the tick-side getRawBrightness / getMaxLocalRawBrightness now backed by
// the real per-section light the LevelLightEngine computes at chunk finalize (world/light.go),
// replacing the former cited-constant 15. CITE: net.minecraft.world.level.lighting.LevelLightEngine
// .getRawBrightness + net.minecraft.world.level.Level.getMaxLocalRawBrightness.

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
