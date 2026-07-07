package noisechunk

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// Fill ports the per-block placement of NoiseBasedChunkGenerator.doFill (javap -c,
// 26.2-inner.jar) wired to the real Aquifer + OreVeinifier — REPLACING Plan 09-04's
// provisional sea-level FillProvisional. For every (localX, worldY, localZ) in the chunk
// it queries the trilerped final_density (Wave 4) and runs vanilla's block-state rule
// chain (NoiseChunk.blockStateRule = MaterialRuleList[ aquiferBaseRule, oreVeinifier ],
// which returns the FIRST non-null filler):
//
//  1. aquifer.computeSubstance(pos, density): for a NON-SOLID block (density<=0) this
//     returns the aquifer's fluid — water/lava up to the local fluid level, else air. For
//     a SOLID block (density>0) vanilla returns null here, falling through to (2).
//  2. oreVeinifier.vein(pos): for a SOLID block in an ore-vein region, returns the
//     copper/iron ore / raw / filler block; else null, falling through to (3).
//  3. default: the solid default block — stone, with the provisional deepslate band
//     at/below y=0 and a bedrock floor at minY (these last two are the same provisional
//     stratification Plan 09-04 used; the REAL deepslate/bedrock surface rules land in
//     Wave 7 — this keeps the column renderable in the meantime).
//
// The cave payoff: caves (the density<=0 regions underground) are now correctly filled by
// the aquifer (flooded with water below the table, lava deep, dry air above) instead of
// 09-04's blanket air, and the rock carries copper/iron veins. Fill emits each placed
// state through set(localX, worldY, localZ, state) — the Generator (Wave 8) supplies a
// set that writes into the chunk section; FillChunk below is the convenience that builds a
// renderable *level.Chunk directly (mirroring FillProvisional's finishing).
//
// It is an algorithmic port of the doFill loop + the block-state rule chain, NOT a copy of
// Mojang source.
// mark is the fill's post-process hook: it is called with (localX, worldY, localZ) for each
// fluid cell the aquifer flagged shouldScheduleFluidUpdate during fill, mirroring
// NoiseBasedChunkGenerator.fillFromNoise -> ChunkAccess.markPosForPostProcessing. nil is a
// no-op (test/standalone callers that don't collect post-process marks).
func Fill(nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier, set func(localX, worldY, localZ int, state block.StateID), mark func(localX, worldY, localZ int)) {
	FillWith(nc, aq, ov, overworldFillParams(), set, mark)
}

// FillParams carries the per-dimension solid-fill blocks: the default solid block (settings
// default_block), the deepslate band (nil-equivalent when the dimension has none), the bedrock floor,
// and whether the deepslate transition applies. The overworld uses stone+deepslate; the nether uses
// netherrack with NO deepslate band. CITE: NoiseGeneratorSettings.defaultBlock + the deepslate
// surface rule (overworld only).
type FillParams struct {
	defaultBlock block.StateID
	deepslate    block.StateID
	bedrock      block.StateID
	hasDeepslate bool
}

// overworldFillParams is the stone/deepslate/bedrock set the original Fill hardcoded.
func overworldFillParams() FillParams {
	return FillParams{
		defaultBlock: block.ToStateID[block.Stone{}],
		deepslate:    block.ToStateID[block.Deepslate{Axis: block.Y}],
		bedrock:      block.ToStateID[block.Bedrock{}],
		hasDeepslate: true,
	}
}

// NetherFillParams is the nether solid-fill set: netherrack (default_block), no deepslate band, a
// bedrock floor. CITE: nether.json default_block == netherrack; the nether has no deepslate.
func NetherFillParams() FillParams {
	return FillParams{
		defaultBlock: block.ToStateID[block.Netherrack{}],
		bedrock:      block.ToStateID[block.Bedrock{}],
		hasDeepslate: false,
	}
}

// EndFillParams is the End solid-fill set: end_stone (default_block), no deepslate band, and
// NO bedrock floor -- the End has no bedrock layer (the central + outer islands are pure
// end_stone floating in the void). The bedrock field is set to end_stone so the minY row
// (the bedrock seam in blockState) is end_stone, not bedrock, matching a bedrock-free End.
// CITE: end.json default_block == end_stone, default_fluid == air, aquifers_enabled == false;
// the End LevelStem has no RandomBedrockFloor.
func EndFillParams() FillParams {
	return FillParams{
		defaultBlock: block.ToStateID[block.EndStone{}],
		bedrock:      block.ToStateID[block.EndStone{}],
		hasDeepslate: false,
	}
}

// FillWith is Fill parameterized by the dimension's solid-fill blocks (FillParams).

func FillWith(nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier, fp FillParams, set func(localX, worldY, localZ int, state block.StateID), mark func(localX, worldY, localZ int)) {
	minY := nc.MinY()
	maxY := minY + nc.Height()

	for lx := 0; lx < 16; lx++ {
		wx := nc.WorldX(lx)
		for lz := 0; lz < 16; lz++ {
			wz := nc.WorldZ(lz)
			for y := minY; y < maxY; y++ {
				set(lx, y, lz, blockState(nc, aq, ov, lx, y, lz, wx, wz, fp, mark))
			}
		}
	}
}

// beardAt returns the structure-Beardifier additive contribution at the world block
// (STRUCT-POLISH-03). It is the BeardifierMarker substitution term threaded into the
// final_density summation (nc.fill), the additive NON-interpolated per-block contribution
// (A5) that raises terrain to meet a village (beard_thin) or digs to bury a stronghold
// (bury). A nil beard (no adapting structure influences this chunk) returns 0 -> the fill is
// byte-identical to a beard-free chunk (the NONE regression guard, TestNonAdaptingUnchanged).
// The Beardifier itself gates the contribution on its affectedBox (no double-apply). Cite:
// NoiseChunk ctor beardifier wrap of DensityFunctions$BeardifierMarker.
func (nc *NoiseChunk) beardAt(wx, wy, wz int) float64 {
	if nc.beard == nil {
		return 0
	}
	return nc.beard(wx, wy, wz)
}

// blockState resolves one block's state via the ported rule chain (see Fill).
func blockState(
	nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier,
	lx, y, lz, wx, wz int,
	fp FillParams,
	mark func(localX, worldY, localZ int),
) block.StateID {
	// Bedrock floor (provisional; the real RandomBedrockFloor is a Wave-7 surface concern).
	if y == nc.MinY() {
		return fp.bedrock
	}

	d := nc.FinalDensity(lx, y, lz)

	// Rule 1: the aquifer base rule. For a non-solid block it returns a real fluid
	// (water/lava) or "air" (reported as not-a-fluid here). A returned fluid wins.
	if st, isFluid := aq.computeSubstance(wx, y, wz, d); isFluid {
		// fillFromNoise marks the cell for post-processing when the aquifer flagged it as an
		// unstable fluid border (shouldScheduleFluidUpdate) AND the placed state is a fluid —
		// the one-shot FluidState.tick the chunk runs when it goes live.
		if mark != nil && aq.ShouldScheduleFluidUpdate() {
			mark(lx, y, lz)
		}
		return st
	}

	if d > 0 {
		// SOLID: Rule 2 = ore veinifier (a vein block overrides the default rock). The nether passes a
		// disabled veinifier (ore_veins_enabled:false), so vein() never fires there.
		if st, ok := ov.vein(wx, y, wz); ok {
			return st
		}
		// Rule 3 = default solid block: the settings default_block (stone/netherrack), with the
		// deepslate band at/below the transition ONLY where the dimension has deepslate (overworld).
		if fp.hasDeepslate && y <= deepslateTopY {
			return fp.deepslate
		}
		return fp.defaultBlock
	}

	// NON-SOLID with no aquifer fluid -> air (dry cave / above the water table).
	return nc.air
}

// FillChunk is the convenience that drives Fill into a renderable *level.Chunk, mirroring
// FillProvisional's finishing (plains biome, full sky light, the 3 CLIENT heightmaps) so
// the Wave-8 Generator can hand the column to the wire path. It is the real replacement for
// FillProvisional: same finishing, real aquifer + ore-vein placement.
func FillChunk(nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier) *level.Chunk {
	return FillChunkWith(nc, aq, ov, overworldFillParams())
}

// FillChunkWith is FillChunk parameterized by the dimension's solid-fill blocks (the nether passes
// NetherFillParams()).
func FillChunkWith(nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier, fp FillParams) *level.Chunk {
	secs := nc.height / 16
	ch := level.EmptyChunk(secs)

	// heights holds the absolute world-Y of the first block ABOVE each column's highest
	// non-air, non-fluid (solid) block — the WorldSurface/MotionBlocking heightmap input.
	heights := make([]int, 16*16)
	for i := range heights {
		heights[i] = nc.minY // default: floor (nothing solid)
	}

	FillWith(nc, aq, ov, fp, func(lx, y, lz int, st block.StateID) {
		if st == nc.air {
			return
		}
		sec := (y - nc.minY) >> 4
		if sec < 0 || sec >= len(ch.Sections) {
			return
		}
		local := (y&15)<<8 | (lz&15)<<4 | (lx & 15)
		ch.Sections[sec].SetBlock(local, st)
		if st != nc.water && y+1 > heights[lz<<4|lx] {
			heights[lz<<4|lx] = y + 1
		}
	}, func(lx, y, lz int) {
		// markPosForPostProcessing: pack the LOCAL position (localY = worldY - minY) onto the
		// chunk's post-process list. The server runs FluidState.tick once per entry when the
		// chunk goes live (LevelChunk.postProcessGeneration).
		localY := y - nc.minY
		ch.PostProcessFluids = append(ch.PostProcessFluids, uint32(localY)<<8|uint32(lz&15)<<4|uint32(lx&15))
	})

	nc.finishChunk(ch, heights)
	return ch
}
