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
func Fill(nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier, set func(localX, worldY, localZ int, state block.StateID)) {
	stone := block.ToStateID[block.Stone{}]
	deepslate := block.ToStateID[block.Deepslate{Axis: block.Y}]
	bedrock := block.ToStateID[block.Bedrock{}]

	minY := nc.MinY()
	maxY := minY + nc.Height()

	for lx := 0; lx < 16; lx++ {
		wx := nc.WorldX(lx)
		for lz := 0; lz < 16; lz++ {
			wz := nc.WorldZ(lz)
			for y := minY; y < maxY; y++ {
				set(lx, y, lz, blockState(nc, aq, ov, lx, y, lz, wx, wz, stone, deepslate, bedrock))
			}
		}
	}
}

// blockState resolves one block's state via the ported rule chain (see Fill).
func blockState(
	nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier,
	lx, y, lz, wx, wz int,
	stone, deepslate, bedrock block.StateID,
) block.StateID {
	// Bedrock floor (provisional; the real RandomBedrockFloor is a Wave-7 surface concern).
	if y == nc.MinY() {
		return bedrock
	}

	d := nc.FinalDensity(lx, y, lz)

	// Rule 1: the aquifer base rule. For a non-solid block it returns a real fluid
	// (water/lava) or "air" (reported as not-a-fluid here). A returned fluid wins.
	if st, isFluid := aq.computeSubstance(wx, y, wz, d); isFluid {
		return st
	}

	if d > 0 {
		// SOLID: Rule 2 = ore veinifier (a vein block overrides the default rock).
		if st, ok := ov.vein(wx, y, wz); ok {
			return st
		}
		// Rule 3 = default solid block: stone, deepslate at/below the transition.
		if y <= deepslateTopY {
			return deepslate
		}
		return stone
	}

	// NON-SOLID with no aquifer fluid -> air (dry cave / above the water table).
	return nc.air
}

// FillChunk is the convenience that drives Fill into a renderable *level.Chunk, mirroring
// FillProvisional's finishing (plains biome, full sky light, the 3 CLIENT heightmaps) so
// the Wave-8 Generator can hand the column to the wire path. It is the real replacement for
// FillProvisional: same finishing, real aquifer + ore-vein placement.
func FillChunk(nc *NoiseChunk, aq *Aquifer, ov *OreVeinifier) *level.Chunk {
	secs := nc.height / 16
	ch := level.EmptyChunk(secs)

	// heights holds the absolute world-Y of the first block ABOVE each column's highest
	// non-air, non-fluid (solid) block — the WorldSurface/MotionBlocking heightmap input.
	heights := make([]int, 16*16)
	for i := range heights {
		heights[i] = nc.minY // default: floor (nothing solid)
	}

	Fill(nc, aq, ov, func(lx, y, lz int, st block.StateID) {
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
	})

	nc.finishChunk(ch, heights)
	return ch
}
