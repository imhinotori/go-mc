package noisechunk

import (
	"math"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// Aquifer ports net.minecraft.world.level.levelgen.Aquifer$NoiseBasedAquifer (javap -c,
// 26.2-inner.jar — the 16KB heart of the cave-water system). aquifers_enabled is true in
// overworld.json, so for every NON-SOLID block (final_density<=0 — caves + the deep
// underground) the doFill asks computeSubstance whether the block is water, lava, or air:
//
//   - The world is sampled on a COARSE aquifer grid (X_SPACING=16, Y_SPACING=12,
//     Z_SPACING=16). Each grid cell holds one randomly-jittered aquifer "center"; each
//     center carries a FluidStatus (a fluid LEVEL + a fluid type) computed once from the
//     fluid_level_floodedness / fluid_level_spread / lava noises + the preliminary
//     surface level. The per-cell FluidStatus is CACHED (T-9-14: not recomputed per block).
//   - For a block, the 4 closest aquifer centers are found (by squared distance) and a
//     barrier pressure is computed between the closest pairs (calculatePressure consults
//     barrier_noise) to decide which aquifer's fluid wins — interpolating the boundary so
//     adjacent aquifers don't hard-edge.
//
// The net effect REPLACES Plan 09-04's provisional sea-level placeholder: caves below the
// local water table flood with water, the deep underground gets lava pools, and caves
// above the table stay dry (air). The global FluidPicker still gives ocean water up to
// sea level (63) and lava below y=-54 as the baseline each aquifer perturbs.
//
// shouldScheduleFluidUpdate (a flowing-water bookkeeping flag in vanilla) is irrelevant to
// static block PLACEMENT (we don't schedule fluid ticks at gen time), so the flag's writes
// are dropped; the substance RETURN value — the only thing the fill needs — is ported
// faithfully, constant-for-constant. It is an algorithmic port, NOT a copy of Mojang source.
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.Aquifer$NoiseBasedAquifer  (<init>, computeSubstance,
//     getIndex, similarity, calculatePressure, grid*, getAquiferStatus, computeFluid,
//     computeSurfaceLevel, computeRandomizedFluidSurfaceLevel, computeFluidType)
//   - net.minecraft.world.level.levelgen.Aquifer$FluidStatus        (at(y), the record)
//   - net.minecraft.world.level.levelgen.NoiseBasedChunkGenerator.createFluidPicker (the global picker)
//   - net.minecraft.world.level.biome.OverworldBiomeBuilder.isDeepDarkRegion
type Aquifer struct {
	nc *NoiseChunk

	barrierNoise          density.Function
	fluidLevelFloodedness density.Function
	fluidLevelSpread      density.Function
	lavaNoise             density.Function
	erosion               density.Function
	depth                 density.Function

	posRandom levelgen.PositionalRandomFactory

	// aquiferCache/aquiferLocationCache memoize per-grid-cell results (the parity-perf
	// hinge T-9-14): location = the jittered center BlockPos (packed long), status = its
	// FluidStatus. Indexed by getIndex (gridX/Y/Z minus the grid origin).
	aquiferCache         []*fluidStatus
	aquiferLocationCache []int64

	minGridX, minGridY, minGridZ int
	gridSizeX, gridSizeZ         int
	skipSamplingAboveY           int

	// global FluidPicker (overworld createFluidPicker).
	lavaFluid  fluidStatus
	waterFluid fluidStatus
	airFluid   fluidStatus
	seaLevel   int

	// resolved block ids for the substance return.
	water block.StateID
	lava  block.StateID
	air   block.StateID
}

// fluidStatus ports Aquifer$FluidStatus: a fluid level + a fluid type id. at(y) returns
// the fluid type below the level, air at/above it.
type fluidStatus struct {
	fluidLevel int
	fluidType  block.StateID // the source fluid (water/lava) or air
}

// at ports FluidStatus.at(y): y < fluidLevel ? fluidType : air.
func (a *Aquifer) statusAt(fs fluidStatus, y int) block.StateID {
	if y < fs.fluidLevel {
		return fs.fluidType
	}
	return a.air
}

// aquifer grid constants, read constant-for-constant from the NoiseBasedAquifer
// ConstantValue attributes (javap -v):
//
//	X_RANGE/Y_RANGE/Z_RANGE             = 10/9/10   (unused directly; the search box is +/-1 cells)
//	X/Y/Z_SPACING                       = 16/12/16  (grid cell size in blocks)
//	X/Z_SPACING_SHIFT                   = 4/4       (gridX/Z = x>>4; gridY = floorDiv(y,12))
//	MAX_REASONABLE_DISTANCE_TO_CENTER   = 11
//	SAMPLE_OFFSET_X/Y/Z                 = -5/1/-5   (the block->grid sample offset)
//	MIN/MAX_CELL_SAMPLE                 = (0,-1,0)/(1,1,1)  (the 2x3x2 neighbour search box)
const (
	aqYSpacing      = 12
	aqSampleOffsetX = -5
	aqSampleOffsetY = 1
	aqSampleOffsetZ = -5
)

// gridX ports gridX(int) = x >> 4 (X_SPACING_SHIFT).
func gridX(x int) int { return x >> 4 }

// fromGridX ports fromGridX(gx, off) = (gx << 4) + off.
func fromGridX(gx, off int) int { return (gx << 4) + off }

// gridY ports gridY(int) = Math.floorDiv(y, 12).
func gridY(y int) int { return floorDiv(y, aqYSpacing) }

// fromGridY ports fromGridY(gy, off) = gy*12 + off.
func fromGridY(gy, off int) int { return gy*aqYSpacing + off }

// gridZ ports gridZ(int) = z >> 4.
func gridZ(z int) int { return z >> 4 }

// fromGridZ ports fromGridZ(gz, off) = (gz << 4) + off.
func fromGridZ(gz, off int) int { return (gz << 4) + off }

// NOTE: vanilla's FLOWING_UPDATE_SIMULARITY = similarity(Mth.square(10), Mth.square(12))
// only gates shouldScheduleFluidUpdate (a flowing-water flag), which does not affect static
// block PLACEMENT — so it is intentionally omitted from this placement-only port.

// surfaceSamplingOffsetsInChunks ports SURFACE_SAMPLING_OFFSETS_IN_CHUNKS — the 13
// section-offset probes computeFluid scans for a nearby exposed surface (the static block).
var surfaceSamplingOffsetsInChunks = [13][2]int{
	{0, 0}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-3, 0},
	{-2, 0}, {-1, 0}, {1, 0}, {-2, 1}, {-1, 1}, {0, 1}, {1, 1},
}

// locUnset is the Long.MAX_VALUE sentinel the location cache fills with (a not-yet-computed
// grid cell). Java uses Long.MAX_VALUE; a real packed BlockPos never equals it here.
const locUnset = int64(math.MaxInt64)

// NewAquifer builds the NoiseBasedAquifer for a chunk: it binds the router's aquifer
// noises, derives the grid span covering the chunk (+SAMPLE_OFFSET margins), allocates the
// per-cell caches, and computes skipSamplingAboveY from the max preliminary surface level
// (above which everything is air and the aquifer can be skipped).
func NewAquifer(r *router.Router, nc *NoiseChunk, pos level.ChunkPos) *Aquifer {
	nr := r.NoiseRouter
	minY := nc.MinY()
	height := nc.Height()

	a := &Aquifer{
		nc:                    nc,
		barrierNoise:          nr.Barrier,
		fluidLevelFloodedness: nr.FluidLevelFloodedness,
		fluidLevelSpread:      nr.FluidLevelSpread,
		lavaNoise:             nr.Lava,
		erosion:               nr.Erosion,
		depth:                 nr.Depth,
		posRandom:             r.Random.AquiferRandom(),
		seaLevel:              nc.SeaLevel(),
		water:                 block.ToStateID[block.Water{Level: 0}],
		lava:                  block.ToStateID[block.Lava{Level: 0}],
		air:                   block.ToStateID[block.Air{}],
	}

	// Global FluidPicker (NoiseBasedChunkGenerator.createFluidPicker):
	//   lava  = FluidStatus(-54, LAVA)
	//   water = FluidStatus(seaLevel, default_fluid=water)
	//   air   = FluidStatus(MIN_Y*2, AIR)   (MIN_Y=-64 -> -128)
	//   pick(x,y,z): y < min(-54, seaLevel) ? air : water
	a.lavaFluid = fluidStatus{fluidLevel: -54, fluidType: a.lava}
	a.waterFluid = fluidStatus{fluidLevel: a.seaLevel, fluidType: a.water}
	a.airFluid = fluidStatus{fluidLevel: dimensionMinY * 2, fluidType: a.air}

	// Grid span (ctor): the chunk's block bounds shifted by SAMPLE_OFFSET, mapped to grid.
	minBlockX := int(pos[0]) * 16
	maxBlockX := minBlockX + 15
	minBlockZ := int(pos[1]) * 16
	maxBlockZ := minBlockZ + 15

	a.minGridX = gridX(minBlockX+aqSampleOffsetX) + 0
	gridEndX := gridX(maxBlockX+aqSampleOffsetX) + 1
	a.gridSizeX = gridEndX - a.minGridX + 1

	a.minGridY = gridY(minY+1) - 1
	gridEndY := gridY(minY+height+1) + 1
	gridSizeY := gridEndY - a.minGridY + 1

	a.minGridZ = gridZ(minBlockZ+aqSampleOffsetZ) + 0
	gridEndZ := gridZ(maxBlockZ+aqSampleOffsetZ) + 1
	a.gridSizeZ = gridEndZ - a.minGridZ + 1

	cells := a.gridSizeX * gridSizeY * a.gridSizeZ
	a.aquiferCache = make([]*fluidStatus, cells)
	a.aquiferLocationCache = make([]int64, cells)
	for i := range a.aquiferLocationCache {
		a.aquiferLocationCache[i] = locUnset
	}

	// skipSamplingAboveY: above the max preliminary surface (+ a margin) the column is all
	// air, so the aquifer can short-circuit. maxPreliminarySurfaceLevel over the grid span.
	maxSurface := nc.maxPreliminarySurfaceLevel(
		fromGridX(a.minGridX, 0), fromGridZ(a.minGridZ, 0),
		fromGridX(gridEndX, 9), fromGridZ(gridEndZ, 9),
	)
	adjustedSurface := a.adjustSurfaceLevel(maxSurface)
	a.skipSamplingAboveY = fromGridY(gridY(adjustedSurface+12)-(-1), 11) - 1
	return a
}

// dimensionMinY is DimensionType.MIN_Y for the overworld (-64). MIN_Y*2 = the air fluid
// status's fluid level (everything is above it, so the global air status returns air).
const dimensionMinY = -64

// wayBelowMinY is DimensionType.WAY_BELOW_MIN_Y — the "no fluid here" sentinel a dry column
// returns from computeSurfaceLevel (Mth.floor(MIN_Y*2 ...) ~ a value far below any block).
const wayBelowMinY = dimensionMinY * 2

// getIndex ports getIndex(gx,gy,gz): the flat cache index over the grid box.
func (a *Aquifer) getIndex(gx, gy, gz int) int {
	x := gx - a.minGridX
	y := gy - a.minGridY
	z := gz - a.minGridZ
	return (y*a.gridSizeZ+z)*a.gridSizeX + x
}

// adjustSurfaceLevel ports adjustSurfaceLevel(y) = y + 8.
func (a *Aquifer) adjustSurfaceLevel(y int) int { return y + 8 }

// globalComputeFluid ports the global FluidPicker lambda: y < min(-54, seaLevel) ? air : water.
func (a *Aquifer) globalComputeFluid(y int) fluidStatus {
	if y < minInt(-54, a.seaLevel) {
		return a.airFluid
	}
	return a.waterFluid
}

// computeSubstance ports NoiseBasedAquifer.computeSubstance(ctx, density). For a SOLID
// block (density>0) it returns (0,false) — the caller places stone/deepslate/ore. For a
// NON-SOLID block it samples the surrounding aquifer grid, picks/interpolates the winning
// fluid status, and returns its at(y) substance. A returned air substance is reported as
// (air,false)-equivalent: we return (state, true) ONLY when the substance is a real fluid
// (water/lava); air results return (0,false) so the fill keeps default air. This matches
// vanilla returning AIR vs a fluid BlockState (the fill writes AIR either way).
func (a *Aquifer) computeSubstance(blockX, blockY, blockZ int, dens float64) (block.StateID, bool) {
	if dens > 0 {
		return 0, false
	}

	global := a.globalComputeFluid(blockY)

	if blockY > a.skipSamplingAboveY {
		// Above the surface: just the global status; no aquifer sampling.
		return a.fluidResult(a.statusAt(global, blockY))
	}

	// If the global status itself is lava at this y, it is lava (DEBUG off path).
	if a.statusAt(global, blockY) == a.lava {
		return a.fluidResult(a.lava)
	}

	gx := gridX(blockX + aqSampleOffsetX)
	gy := gridY(blockY + aqSampleOffsetY)
	gz := gridZ(blockZ + aqSampleOffsetZ)

	// Find the 4 closest aquifer centers by squared distance (dist1<=dist2<=dist3<=dist4),
	// tracking their cache indices (idx1..idx4). Mirrors the unrolled insertion in the
	// bytecode over the (-1..1)x(-1..1)x(0..1) neighbour box.
	dist1, dist2, dist3, dist4 := math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32
	idx1, idx2, idx3, idx4 := 0, 0, 0, 0

	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := 0; dz <= 1; dz++ {
				cgx := gx + dx
				cgy := gy + dy
				cgz := gz + dz
				cacheIdx := a.getIndex(cgx, cgy, cgz)

				loc := a.aquiferLocationCache[cacheIdx]
				if loc == locUnset {
					rnd := a.posRandom.At(cgx, cgy, cgz)
					px := fromGridX(cgx, int(rnd.NextIntN(10)))
					py := fromGridY(cgy, int(rnd.NextIntN(9)))
					pz := fromGridZ(cgz, int(rnd.NextIntN(10)))
					loc = packBlockPos(px, py, pz)
					a.aquiferLocationCache[cacheIdx] = loc
				}

				ux := unpackX(loc) - blockX
				uy := unpackY(loc) - blockY
				uz := unpackZ(loc) - blockZ
				sq := ux*ux + uy*uy + uz*uz

				switch {
				case dist1 >= sq:
					idx4, idx3, idx2, idx1 = idx3, idx2, idx1, cacheIdx
					dist4, dist3, dist2, dist1 = dist3, dist2, dist1, sq
				case dist2 >= sq:
					idx4, idx3, idx2 = idx3, idx2, cacheIdx
					dist4, dist3, dist2 = dist3, dist2, sq
				case dist3 >= sq:
					idx4, idx3 = idx3, cacheIdx
					dist4, dist3 = dist3, sq
				case dist4 >= sq:
					idx4 = cacheIdx
					dist4 = sq
				}
			}
		}
	}

	status1 := a.getAquiferStatus(idx1)
	sim12 := similarity(dist1, dist2)
	sub1 := a.statusAt(status1, blockY)

	if sim12 <= 0 {
		// The block is solidly inside aquifer-1's region — its status wins outright.
		return a.fluidResult(sub1)
	}

	// Water-over-lava boundary: a water aquifer immediately above lava stays water.
	if sub1 == a.water {
		below := a.globalComputeFluid(blockY - 1)
		if a.statusAt(below, blockY-1) == a.lava {
			return a.fluidResult(sub1)
		}
	}

	// Otherwise interpolate the barrier pressure between the closest aquifers.
	var barrier mutableDouble
	barrier.set(math.NaN())
	status2 := a.getAquiferStatus(idx2)

	pressure12 := sim12 * a.calculatePressure(blockX, blockY, blockZ, &barrier, status1, status2)
	if dens+pressure12 > 0 {
		return a.fluidResult(0) // air wins (no fluid)
	}

	status3 := a.getAquiferStatus(idx3)
	sim13 := similarity(dist1, dist3)
	if sim13 > 0 {
		pressure13 := sim12 * sim13 * a.calculatePressure(blockX, blockY, blockZ, &barrier, status1, status3)
		if dens+pressure13 > 0 {
			return a.fluidResult(0)
		}
	}

	sim23 := similarity(dist2, dist3)
	if sim23 > 0 {
		pressure23 := sim12 * sim23 * a.calculatePressure(blockX, blockY, blockZ, &barrier, status2, status3)
		if dens+pressure23 > 0 {
			return a.fluidResult(0)
		}
	}

	// idx4 (the 4th-closest aquifer cache index) is tracked by the grid search to keep the
	// insertion faithful, but only feeds vanilla's shouldScheduleFluidUpdate flag (omitted
	// here) — not the placement decision; explicitly discarded.
	_ = idx4
	return a.fluidResult(sub1)
}

// fluidResult maps a substance state id to the (state, isFluid) the fill consumes: a real
// fluid (water/lava) -> (state, true); air (or the 0 marker) -> (0, false) so the caller
// keeps default air. computeSubstance internally passes 0 to mean "air wins".
func (a *Aquifer) fluidResult(state block.StateID) (block.StateID, bool) {
	if state == a.water || state == a.lava {
		return state, true
	}
	return 0, false
}

// similarity ports similarity(distA, distB) = 1 - |distB - distA| / 25.0. Note the
// bytecode computes 1.0 - (b - a)/25.0 (distances are squared, already sorted a<=b).
func similarity(distA, distB int) float64 {
	return 1.0 - float64(distB-distA)/25.0
}

// getAquiferStatus ports getAquiferStatus(idx): the cached FluidStatus for a grid cell, or
// compute it once from the cell's jittered center (computeFluid) and cache it (T-9-14).
func (a *Aquifer) getAquiferStatus(idx int) fluidStatus {
	if a.aquiferCache[idx] != nil {
		return *a.aquiferCache[idx]
	}
	loc := a.aquiferLocationCache[idx]
	fs := a.computeFluid(unpackX(loc), unpackY(loc), unpackZ(loc))
	a.aquiferCache[idx] = &fs
	return fs
}

// computeFluid ports computeFluid(x,y,z): start from the global status; scan the 13
// SURFACE_SAMPLING_OFFSETS probes for a nearby exposed surface (so aquifers under solid
// ground don't leak into open caves), then compute the local fluid SURFACE level (from the
// floodedness/spread noise) and the fluid TYPE (water, or lava deep down).
func (a *Aquifer) computeFluid(x, y, z int) fluidStatus {
	global := a.globalComputeFluid(y)
	minSurface := math.MaxInt32
	aboveSurfaceFlooded := false
	yPlus := y + 12
	yMinus := y - 12

	for _, off := range surfaceSamplingOffsetsInChunks {
		sx := x + sectionToBlock(off[0])
		sz := z + sectionToBlock(off[1])
		prelim := a.nc.preliminarySurfaceLevel(sx, sz)
		adjusted := a.adjustSurfaceLevel(prelim)

		isCenter := off[0] == 0 && off[1] == 0
		if isCenter && yMinus > adjusted {
			return global
		}

		surfaceAbove := yPlus > adjusted
		if surfaceAbove || isCenter {
			localStatus := a.globalComputeFluid(adjusted)
			if !a.isAir(a.statusAt(localStatus, adjusted)) {
				if isCenter {
					aboveSurfaceFlooded = true
				}
				if surfaceAbove {
					return localStatus
				}
			}
		}
		minSurface = minInt(minSurface, prelim)
	}

	surfaceLevel := a.computeSurfaceLevel(x, y, z, global, minSurface, aboveSurfaceFlooded)
	return fluidStatus{fluidLevel: surfaceLevel, fluidType: a.computeFluidType(x, y, z, global, surfaceLevel)}
}

// isAir reports whether a substance id is air (or the 0 marker).
func (a *Aquifer) isAir(state block.StateID) bool { return state == a.air || state == 0 }

// computeSurfaceLevel ports computeSurfaceLevel(x,y,z, global, minSurface, aboveSurface).
// It blends two floodedness terms (from fluid_level_floodedness) over the deep-dark check
// to decide whether this column floods to the global level, a randomized perched level, or
// stays dry (WAY_BELOW_MIN_Y).
func (a *Aquifer) computeSurfaceLevel(x, y, z int, global fluidStatus, minSurface int, aboveSurface bool) int {
	ctx := density.Context{X: x, Y: y, Z: z}

	var d8, d10 float64
	if a.isDeepDarkRegion(ctx) {
		d8, d10 = -1.0, -1.0
	} else {
		// outFromSurface = clampedMap(minSurface+8 - y, 0, 64, 1, 0) if aboveSurface else 0
		var outFromSurface float64
		if aboveSurface {
			outFromSurface = clampedMap(float64(minSurface+8-y), 0.0, 64.0, 1.0, 0.0)
		}
		floodedness := clampD(a.fluidLevelFloodedness.Compute(ctx), -1.0, 1.0)
		spread1 := mthMap(outFromSurface, 1.0, 0.0, -0.3, 0.8)
		spread2 := mthMap(outFromSurface, 1.0, 0.0, -0.8, 0.4)
		d8 = floodedness - spread2
		d10 = floodedness - spread1
	}

	if d10 > 0 {
		return global.fluidLevel
	}
	if d8 > 0 {
		return a.computeRandomizedFluidSurfaceLevel(x, y, z, minSurface)
	}
	return wayBelowMinY
}

// computeRandomizedFluidSurfaceLevel ports computeRandomizedFluidSurfaceLevel: a coarse
// (16x40x16) cell, the fluid_level_spread noise quantized to multiples of 3, added to a
// y-band base, capped at minSurface — gives the perched water-table height variation.
func (a *Aquifer) computeRandomizedFluidSurfaceLevel(x, y, z, minSurface int) int {
	gx := floorDiv(x, 16)
	gy := floorDiv(y, 40)
	gz := floorDiv(z, 16)
	base := gy*40 + 20

	spread := a.fluidLevelSpread.Compute(density.Context{X: gx, Y: gy, Z: gz}) * 10.0
	quantized := mthQuantize(spread, 3)
	level := base + quantized
	return minInt(minSurface, level)
}

// computeFluidType ports computeFluidType: water by default; lava if y is in the deep band
// (-10 >= y > WAY_BELOW_MIN_Y) AND the global fluid type is water AND |lava_noise| > 0.3.
func (a *Aquifer) computeFluidType(x, y, z int, global fluidStatus, surfaceLevel int) block.StateID {
	fluidType := global.fluidType
	if surfaceLevel <= -10 && surfaceLevel != wayBelowMinY && global.fluidType == a.water {
		gx := floorDiv(x, 64)
		gy := floorDiv(y, 40)
		gz := floorDiv(z, 64)
		lavaN := a.lavaNoise.Compute(density.Context{X: gx, Y: gy, Z: gz})
		if math.Abs(lavaN) > 0.3 {
			fluidType = a.lava
		}
	}
	return fluidType
}

// calculatePressure ports calculatePressure(ctx, barrier, statusA, statusB): the barrier
// pressure between two aquifer fluid statuses. Lava-vs-water boundaries get a fixed 2.0;
// otherwise it blends the level difference against the barrier_noise (sampled lazily once
// via the MutableDouble, mirroring the bytecode's NaN-guarded cache).
func (a *Aquifer) calculatePressure(x, y, z int, barrier *mutableDouble, statusA, statusB fluidStatus) float64 {
	stA := a.statusAt(statusA, y)
	stB := a.statusAt(statusB, y)

	if (stA == a.lava && stB == a.water) || (stA == a.water && stB == a.lava) {
		return 2.0
	}

	levelDiff := absInt(statusA.fluidLevel - statusB.fluidLevel)
	if levelDiff == 0 {
		return 0.0
	}

	avgLevel := 0.5 * float64(statusA.fluidLevel+statusB.fluidLevel)
	relY := float64(y) + 0.5 - avgLevel
	halfDiff := float64(levelDiff) / 2.0

	var pressure float64
	dd := halfDiff - math.Abs(relY)
	if relY > 0 {
		t := 0.0 + dd
		if t > 0 {
			pressure = t / 1.5
		} else {
			pressure = t / 2.5
		}
	} else {
		t := 3.0 + dd
		if t > 0 {
			pressure = t / 3.0
		} else {
			pressure = t / 10.0
		}
	}

	var barrierVal float64
	if pressure < -2.0 || pressure > 2.0 {
		barrierVal = 0.0
	} else {
		bv := barrier.value()
		if math.IsNaN(bv) {
			bv = a.barrierNoise.Compute(density.Context{X: x, Y: y, Z: z})
			barrier.set(bv)
		}
		barrierVal = bv
	}
	return 2.0 * (barrierVal + pressure)
}

// mutableDouble ports org.apache.commons.lang3.mutable.MutableDouble use in
// calculatePressure: a lazily-set barrier noise cache (NaN = not yet sampled).
type mutableDouble struct{ v float64 }

func (m *mutableDouble) value() float64 { return m.v }
func (m *mutableDouble) set(v float64)  { m.v = v }

// ---- small Mth + BlockPos helpers (ported constant-for-constant) ----

// sectionToBlock ports SectionPos.sectionToBlockCoord(s) = s << 4.
func sectionToBlock(s int) int { return s << 4 }

// packBlockPos / unpack* port BlockPos.asLong / getX/Y/Z (26-bit X/Z, 12-bit Y, the vanilla
// packing). Used as the aquifer location cache key (the jittered center position).
const (
	bpBitsXZ = 26
	bpBitsY  = 12
	bpMaskXZ = (int64(1) << bpBitsXZ) - 1
	bpMaskY  = (int64(1) << bpBitsY) - 1
	bpYShift = 0
	bpZShift = bpBitsY
	bpXShift = bpBitsY + bpBitsXZ
)

func packBlockPos(x, y, z int) int64 {
	return (int64(x)&bpMaskXZ)<<bpXShift | (int64(y)&bpMaskY)<<bpYShift | (int64(z)&bpMaskXZ)<<bpZShift
}

func unpackX(l int64) int { return int(signExtend(l<<(64-bpXShift-bpBitsXZ)>>(64-bpBitsXZ), bpBitsXZ)) }
func unpackY(l int64) int { return int(signExtend((l<<(64-bpYShift-bpBitsY))>>(64-bpBitsY), bpBitsY)) }
func unpackZ(l int64) int {
	return int(signExtend((l<<(64-bpZShift-bpBitsXZ))>>(64-bpBitsXZ), bpBitsXZ))
}

// signExtend sign-extends the low `bits` of v.
func signExtend(v int64, bits int) int64 {
	shift := 64 - bits
	return (v << shift) >> shift
}

// mthMap ports Mth.map(v,a,b,c,d) = lerp(inverseLerp(v,a,b), c, d), lerp(t,a,b)=a+t*(b-a).
func mthMap(v, a, b, c, d float64) float64 {
	t := (v - a) / (b - a)
	return c + t*(d-c)
}

// mthQuantize ports Mth.quantize(d,n) = floor(d/n)*n.
func mthQuantize(d float64, n int) int {
	return mthFloor(d/float64(n)) * n
}

// clampD ports Mth.clamp(v,lo,hi) = max(lo, min(v,hi)).
func clampD(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// absInt is Math.abs(int).
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// isDeepDarkRegion ports OverworldBiomeBuilder.isDeepDarkRegion(erosion, depth, ctx) =
// erosion.compute(ctx) < -0.225 && depth.compute(ctx) > 0.9.
func (a *Aquifer) isDeepDarkRegion(ctx density.Context) bool {
	return a.erosion.Compute(ctx) < -0.22499999403953552 && a.depth.Compute(ctx) > 0.8999999761581421
}
