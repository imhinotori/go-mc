package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// crop_block.go — CROP GROWTH + FARMLAND MOISTURE random-tick handlers, ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.level.block.CropBlock / BeetrootBlock / FarmlandBlock).
// They are the second family (after sugar cane) wired into the random-tick driver's
// dispatchRandomTick. Growth is driven by the world's random-tick pass (random_tick.go), NOT by a
// direct handler call.
//
// CITE (temp/cache/26.2-inner.jar, `javap -c -p` this session):
//   CropBlock.randomTick(state, level, pos, random):
//       if (level.getRawBrightness(pos, 0) >= 9) {
//           int age = getAge(state);
//           if (age < getMaxAge()) {
//               float speed = getGrowthSpeed(this, level, pos);
//               if (random.nextInt((int)(25.0F / speed) + 1) == 0) {
//                   level.setBlock(pos, getStateForAge(age + 1), 2);
//               }
//           }
//       }
//   BeetrootBlock.randomTick(state, level, pos, random):
//       if (random.nextInt(3) != 0) super.randomTick(state, level, pos, random);  // CropBlock.randomTick
//   FarmlandBlock.randomTick(state, level, pos, random):
//       int m = state.getValue(MOISTURE);
//       if (!isNearWater(level, pos) && !level.isRainingAt(pos.above())) {
//           if (m > 0) setBlock(pos, state.setValue(MOISTURE, m-1), 2);
//           else if (!shouldMaintainFarmland(level, pos)) turnToDirt(null, state, level, pos);
//       } else if (m < 7) {
//           setBlock(pos, state.setValue(MOISTURE, 7), 2);
//       }

// CropBlock.hasSufficientLight is now a REAL light read: cropGrow gates growth on
// t.rawBrightness(pos, 0) >= 9, backed by the LevelLightEngine-computed per-section light
// (world/light.go, server/light.go). CITE: CropBlock.hasSufficientLight (getRawBrightness(pos,0)>=9).

// cropGrowthSpeedDivisor is the 25.0F numerator in CropBlock.randomTick's growth roll:
// random.nextInt((int)(25.0F / speed) + 1). CITE: CropBlock.randomTick (ldc 25.0f).
const cropGrowthSpeedDivisor = 25.0

// cropRandomTick is CropBlock.randomTick (and, for beetroots, BeetrootBlock.randomTick which wraps
// it in an extra nextInt(3) gate). r is the owning region: r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly):
//   - Beetroots: FIRST draw random.nextInt(3); only if != 0 does the CropBlock body run (and possibly
//     draw its own nextInt for the growth roll). BeetrootBlock.randomTick.
//   - CropBlock body: after the light gate + age<maxAge gate, draw random.nextInt((int)(25/speed)+1);
//     grow iff == 0.
//
// CITE: BeetrootBlock.randomTick; CropBlock.randomTick.
func (t *TickLoop) cropRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	// BeetrootBlock.randomTick: `if (random.nextInt(3) != 0) super.randomTick(...)`. The nextInt(3) is
	// drawn FIRST and UNCONDITIONALLY for beetroots (before the light/age gates), exactly as the
	// bytecode invokes it at the method head; on a 0 result beetroot does nothing this tick.
	if block.IsBeetroots(state) {
		if r.levelRandom.NextIntN(3) == 0 {
			return
		}
	}
	t.cropGrow(r, state, pos)
}

// cropGrow is the shared CropBlock.randomTick body: light gate -> age gate -> growth-speed roll ->
// setBlock(getStateForAge(age+1), 2). Called directly for wheat/carrots/potatoes and (via
// cropRandomTick's nextInt(3) gate) for beetroots. CITE: CropBlock.randomTick.
func (t *TickLoop) cropGrow(r *region, state block.StateID, pos pk.Position) {
	// `if (level.getRawBrightness(pos, 0) >= 9)` — REAL sky/block light now (world/light.go); ambient
	// darkness is the literal 0 the vanilla call passes. CITE: CropBlock.hasSufficientLight.
	if t.rawBrightness(pos, 0) < 9 {
		return
	}
	age := block.CropAge(state)
	maxAge := block.CropMaxAge(state)
	if age < 0 || maxAge < 0 {
		return // not a ported crop (defensive)
	}
	// `if (age < getMaxAge())` — a max-age crop does not grow (and IsRandomlyTicking already excludes
	// it, so this is the mirrored guard).
	if age >= maxAge {
		return
	}
	speed := t.cropGrowthSpeed(state, pos)
	// random.nextInt((int)(25.0F / speed) + 1): the (int) cast TRUNCATES toward zero (Java f2i), and
	// speed is always >= 1.0f so the bound is >= 1. Grow iff the roll is 0.
	bound := int32(float32(cropGrowthSpeedDivisor)/speed) + 1
	if r.levelRandom.NextIntN(bound) != 0 {
		return
	}
	// level.setBlock(pos, getStateForAge(age+1), 2): flag 2 == UPDATE_CLIENTS (no neighbor notify),
	// mirrored as SetBlock + broadcastBlockUpdate (the same flag-2 shape used across the block layer).
	grown, ok := block.CropStateForAge(state, age+1)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, grown, dimMinY) {
		t.broadcastBlockUpdate(pos, grown)
	}
}

// cropGrowthSpeed is the 1:1 port of CropBlock.getGrowthSpeed(block, level, pos) — the load-bearing
// growth math. It walks the 3x3 of cells around pos.below() awarding an on-farmland bonus (1.0
// base, 3.0 when that farmland is moist>0), quartered for the 8 non-center cells; then applies a
// same-crop-neighbor penalty (halved when the crop is boxed in on both axes, or diagonally). `block`
// in the vanilla signature is the crop's Block, so the neighbor `.is(block)` compares are same-crop
// (ignore AGE) — SameCropBlock(state, neighbor). An unreadable neighbor reads as "not a support /
// not the crop" (getBlockState on an unloaded chunk returns air), the same non-destructive default
// the sugar-cane port uses. CITE: CropBlock.getGrowthSpeed.
func (t *TickLoop) cropGrowthSpeed(state block.StateID, pos pk.Position) float32 {
	var f float32 = 1.0
	below := pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	// for (int i = -1; i <= 1; ++i) for (int j = -1; j <= 1; ++j) — the 3x3 around the cell BELOW.
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			var g float32 = 0.0
			cell := pk.Position{X: below.X + i, Y: below.Y, Z: below.Z + j}
			if cs, ok := t.world().GetBlock(cell, dimMinY); ok && block.GrowsCrops(cs) {
				g = 1.0
				// bs.getValueOrElse(FarmlandBlock.MOISTURE, 0) > 0 ? 3.0 : 1.0. GrowsCrops == farmland,
				// so FarmlandMoisture is the getValueOrElse(MOISTURE, 0) read (>= 0 for farmland).
				if block.FarmlandMoisture(cs) > 0 {
					g = 3.0
				}
			}
			// if (i != 0 || j != 0) g /= 4.0F — the 8 non-center cells contribute a quarter.
			if i != 0 || j != 0 {
				g /= 4.0
			}
			f += g
		}
	}
	// Same-crop-neighbor penalty. north/south/west/east and the four diagonals compare `.is(block)`.
	north := pk.Position{X: pos.X, Y: pos.Y, Z: pos.Z - 1}
	south := pk.Position{X: pos.X, Y: pos.Y, Z: pos.Z + 1}
	west := pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z}
	east := pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z}
	flagWE := t.sameCropAt(state, west) || t.sameCropAt(state, east)
	flagNS := t.sameCropAt(state, north) || t.sameCropAt(state, south)
	if flagWE && flagNS {
		f /= 2.0
	} else {
		// west.north(), east.north(), east.south(), west.south() — the four diagonals off pos.
		flagDiag := t.sameCropAt(state, pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z - 1}) ||
			t.sameCropAt(state, pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z - 1}) ||
			t.sameCropAt(state, pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z + 1}) ||
			t.sameCropAt(state, pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z + 1})
		if flagDiag {
			f /= 2.0
		}
	}
	return f
}

// sameCropAt reports whether the block at pos is the SAME crop family as state (the vanilla
// `level.getBlockState(pos).is(block)` where block is the crop's Block). An unreadable cell is not
// the crop. CITE: CropBlock.getGrowthSpeed (BlockState.is(Block)).
func (t *TickLoop) sameCropAt(state block.StateID, pos pk.Position) bool {
	ns, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	return block.SameCropBlock(state, ns)
}

// farmlandRandomTick is the 1:1 port of FarmlandBlock.randomTick — moisture hydration/decay and the
// dry-out turnToDirt. r is the owning region (unused for RNG — this handler draws none). NO RNG draw
// keeps the levelRandom stream untouched. CITE: FarmlandBlock.randomTick.
func (t *TickLoop) farmlandRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	moisture := block.FarmlandMoisture(state)
	if moisture < 0 {
		return // not farmland (defensive)
	}
	// if (!isNearWater(level, pos) && !level.isRainingAt(pos.above())) { dry branch } else { hydrate }.
	if !t.farmlandNearWater(pos) && !t.isRainingAt(above(pos)) {
		// DRY: if (m > 0) m-- ; else if (!shouldMaintainFarmland) turnToDirt.
		if moisture > 0 {
			if next, ok := block.FarmlandState(moisture - 1); ok {
				if t.world().SetBlock(pos, next, dimMinY) {
					t.broadcastBlockUpdate(pos, next)
				}
			}
			return
		}
		// m == 0: shouldMaintainFarmland == getBlockState(pos.above()).is(MAINTAINS_FARMLAND). If a
		// crop/stem/fence-gate sits above, the farmland is kept; otherwise it reverts to dirt.
		if !t.farmlandShouldMaintain(pos) {
			t.farmlandTurnToDirt(pos)
		}
		return
	}
	// HYDRATE: near water or raining -> set MOISTURE to 7 if not already at 7.
	if moisture < 7 {
		if wet, ok := block.FarmlandState(7); ok {
			if t.world().SetBlock(pos, wet, dimMinY) {
				t.broadcastBlockUpdate(pos, wet)
			}
		}
	}
}

// farmlandNearWater is FarmlandBlock.isNearWater(level, pos): scan the box
// betweenClosed(pos.offset(-4,0,-4), pos.offset(4,1,4)) — a 9x2x9 volume — for any cell whose
// getFluidState().is(FluidTags.WATER). Returns true on the first water cell found. An unreadable
// cell is skipped (no water there). CITE: FarmlandBlock.isNearWater.
func (t *TickLoop) farmlandNearWater(pos pk.Position) bool {
	for dx := -4; dx <= 4; dx++ {
		for dy := 0; dy <= 1; dy++ {
			for dz := -4; dz <= 4; dz++ {
				cell := pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
				cs, ok := t.world().GetBlock(cell, dimMinY)
				if !ok {
					continue
				}
				if block.IsWaterFluid(cs) {
					return true
				}
			}
		}
	}
	return false
}

// farmlandShouldMaintain is FarmlandBlock.shouldMaintainFarmland(level, pos):
// getBlockState(pos.above()).is(BlockTags.MAINTAINS_FARMLAND). An unreadable cell above is not a
// maintaining block. CITE: FarmlandBlock.shouldMaintainFarmland.
func (t *TickLoop) farmlandShouldMaintain(pos pk.Position) bool {
	as, ok := t.world().GetBlock(above(pos), dimMinY)
	if !ok {
		return false
	}
	return block.MaintainsFarmland(as)
}

// farmlandTurnToDirt is the moisture-driven half of FarmlandBlock.turnToDirt(null, state, level,
// pos): replace the farmland with dirt (default state) and broadcast. The vanilla turnToDirt also
// runs pushEntitiesUp (nudge an entity standing in the now-full dirt cell) and a BLOCK_CHANGE
// gameEvent — both DEFERRED here: no entity-push seam / gameEvent bus is wired for a block edit in
// this path, and neither changes the block outcome. The separate fallOn trample-to-dirt (an entity
// fall path, not randomTick) is likewise a follow-up. CITE: FarmlandBlock.turnToDirt / randomTick.
func (t *TickLoop) farmlandTurnToDirt(pos pk.Position) {
	dirt := block.DefaultStateID["minecraft:dirt"]
	if t.world().SetBlock(pos, dirt, dimMinY) {
		t.broadcastBlockUpdate(pos, dirt)
	}
}

// isRainingAt ports Level.isRainingAt(BlockPos): precipitationAt(pos) == Biome.Precipitation.RAIN. With
// the weather cycle now real (weather.go), the farmland dry-out path hydrates from rain exactly when
// vanilla does. It delegates to precipitationAt below.
//
//	[VERIFIED javap Level.isRainingAt: precipitationAt(pos) == Biome$Precipitation.RAIN.]
func (t *TickLoop) isRainingAt(pos pk.Position) bool {
	return t.precipitationAt(pos) == precipitationRAIN
}

// precipitation is the net.minecraft.world.level.biome.Biome$Precipitation enum port (NONE/RAIN/SNOW).
// v1 wires only the NONE/RAIN distinction the isRainingAt gate needs; SNOW is a cited biome-data
// deferral (below).
type precipitation int

const (
	precipitationNONE precipitation = iota
	precipitationRAIN
)

// precipitationAt ports Level.precipitationAt(BlockPos): the open-sky/heightmap/biome gate that turns
// the world-global isRaining() into a per-position rain check:
//
//	if (!isRaining())                                   return NONE;
//	if (!canSeeSky(pos))                                return NONE;
//	if (getHeightmapPos(MOTION_BLOCKING, pos).getY() > pos.getY()) return NONE; // under the surface top
//	return getBiome(pos).getPrecipitationAt(pos, seaLevel);
//
// The biome branch is a CITED deferral: no biome precipitation data is wired in v1 (the same gap the
// spawn/precipitation callers cite), so an overworld surface cell that clears the isRaining + open-sky
// + heightmap gates is treated as RAIN (the overworld default precipitation; SNOW/NONE-by-biome lands
// with real biome data). The isRaining + canSeeSky + heightmap gates themselves are REAL.
//
//	[VERIFIED javap Level.precipitationAt: isRaining -> canSeeSky -> MOTION_BLOCKING heightmap top vs
//	 pos.Y -> biome.getPrecipitationAt(pos, seaLevel). DEFERRED: per-biome precipitation (SNOW vs RAIN
//	 vs NONE) pending biome data; overworld surface defaults to RAIN.]
func (t *TickLoop) precipitationAt(pos pk.Position) precipitation {
	if !t.isRaining() {
		return precipitationNONE
	}
	if !t.canSeeSkyAt(pos.Y) {
		return precipitationNONE
	}
	// getHeightmapPos(MOTION_BLOCKING, pos).getY() == getFirstAvailable(x,z) == top solid + 1 (the first
	// air above the surface). If that is ABOVE pos.Y, pos is under the surface top -> no rain reaches it.
	firstAvailableY := t.ghastMotionBlockingTop(pos.X, pos.Z) + 1
	if firstAvailableY > pos.Y {
		return precipitationNONE
	}
	// biome.getPrecipitationAt(pos, seaLevel): overworld default RAIN (biome-precipitation deferral).
	return precipitationRAIN
}
