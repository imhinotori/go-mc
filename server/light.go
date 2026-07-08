package server

import (
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
func (t *TickLoop) relightOnEdit(p *tickPlayer, pos pk.Position, oldState, newState block.StateID) {
	if !world.LightPropertiesDiffer(oldState, newState) {
		return // light properties unchanged: no checkBlock, no relight (the hasDifferentLightProperties gate)
	}
	// NETHER: relight the editor's-dimension world at that dimension's geometry so a nether edit
	// re-propagates the nether world's light (not the overworld's). dimWorld(p) picks the manager;
	// dimMinYFor/dimSecsFor give the section geometry.
	w := t.dimWorld(p)
	if w == nil {
		return
	}
	minSec := lightMinSectionY
	secs := lightSectionCount
	if p != nil && p.dimension == dimNether {
		minSec = dimNetherMinY >> 4
		secs = dimNetherSecs
	}
	if p != nil && p.dimension == dimEnd {
		minSec = dimEndMinY >> 4
		secs = dimEndSecs
	}
	air := block.ToStateID[block.Air{}]
	changed := w.RelightEdit(pos, minSec, secs, air)
	for _, cl := range changed {
		packet := world.WriteLightUpdate(cl)
		col := chunkCenterOf(cl.Pos[0]*16, cl.Pos[1]*16)
		for _, pl := range t.players {
			if pl.client == nil {
				continue
			}
			if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
				pl.client.Send(packet)
			}
		}
	}
}

// server light READ seams — the tick-side getRawBrightness / getMaxLocalRawBrightness now backed by
// the real per-section light the LevelLightEngine computes at chunk finalize (world/light.go),
// replacing the former cited-constant 15. CITE: net.minecraft.world.level.lighting.LevelLightEngine
// .getRawBrightness + net.minecraft.world.level.Level.getMaxLocalRawBrightness.

// skyDarkenDay is the ambient-darkness term at full DAY: Level.getSkyDarken() = 15 -
// skyLightLevel(dimension env attribute); at day the sky light level is 15 so skyDarken == 0. The
// day/night env-attribute clock is a not-yet-built subsystem, so this is a CITED CONSTANT equal to
// the vanilla DAY default, structured to become a real getSkyDarken() read once that clock exists —
// never baked away. CITE: Level.updateSkyBrightness (skyDarken = 15 - SKY_LIGHT_LEVEL); at day == 0.
//
//	[DEFERRED: getSkyDarken() — no day/night sky-light-level env attribute in v1. CITE:
//	 Level.getMaxLocalRawBrightness(pos) = getRawBrightness(pos, getSkyDarken()). Follow-up: swap for
//	 the real skyDarken once the environment-attribute time clock lands.]
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
// getSkyDarken()). The X/Z far-bounds guard (>= 30000000 => 15) is mirrored for parity. CITE:
// Level.getMaxLocalRawBrightness.
func (t *TickLoop) maxLocalRawBrightness(pos pk.Position) int {
	if pos.X < -30000000 || pos.Z < -30000000 || pos.X >= 30000000 || pos.Z >= 30000000 {
		return 15
	}
	return t.rawBrightness(pos, skyDarkenDay)
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
