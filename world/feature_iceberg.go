package world

// feature_iceberg.go ports IcebergFeature.place and ALL its helpers — the `iceberg`
// configured feature (net.minecraft.world.level.levelgen.feature.IcebergFeature, verified
// via `javap -c -p` + CFR against temp/cache/26.2-inner.jar). It raises a noise-shaped body
// of the configured ice block (packed_ice for iceberg_packed, blue_ice for iceberg_blue)
// with an optional snow cap above sea level and a matching submerged base, then smooths the
// exposed edges and optionally carves an interior cut-out.
//
// Config is BlockStateConfiguration ({"state": {"Name": ...}}) — the single main block.
//
// Sources (javap -c + CFR, 26.2-inner.jar):
//   - IcebergFeature.place(FeaturePlaceContext)  (origin re-anchored to chunkGenerator().getSeaLevel())
//   - IcebergFeature.generateCutOut / carve / removeFloatingSnowLayer
//   - IcebergFeature.generateIcebergBlock / setIcebergBlock / getEllipseC
//   - IcebergFeature.signedDistanceCircle / signedDistanceEllipse
//   - IcebergFeature.heightDependentRadiusRound / heightDependentRadiusEllipse / heightDependentRadiusSteep
//   - IcebergFeature.isIcebergState / belowIsAir / smooth
//
// CRITICAL BYTECODE NOTE (verified against `javap -c` place tail, not CFR): the cut-out gate
// is a SINGLE local written in BOTH ternary branches, then tested:
//     cutout = isEllipse ? (nextDouble() > 0.1) : (nextDouble() > 0.7); if (cutout) generateCutOut(...)
// CFR mis-decompiles this as a `doCutOut` that stays false for the ellipse branch. The jar
// draws exactly one nextDouble() here and DOES run the cut-out for ellipse icebergs 90% of
// the time. This port follows the bytecode.
//
// The RNG draw order is a literal mirror of the bytecode throughout (place head draws:
// nextDouble (snowOnTop), nextDouble (shapeAngle), nextInt(5), nextInt(3), nextDouble
// (isEllipse), the overWaterHeight draw(s), the tall-berg draw, underWaterHeight, width;
// then per-cell draws inside generateIcebergBlock / heightDependentRadius*).

import (
	"encoding/json"
	"math"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("iceberg", icebergBody) }

// ---- shared direction machinery (also used by feature_blue_ice.go / feature_underwater_magma.go) ----

// icebergDir is a Direction ordinal in the vanilla Direction.values() order
// (DOWN, UP, NORTH, SOUTH, WEST, EAST — level/block.Direction).
type icebergDir = block.Direction

const (
	dirDown  = block.Down
	dirUp    = block.Up
	dirNorth = block.North
	dirSouth = block.South
	dirWest  = block.West
	dirEast  = block.East
)

// icebergDirections is Direction.values() (DOWN, UP, NORTH, SOUTH, WEST, EAST). The
// BlueIceFeature / UnderwaterMagma neighbour scans iterate this exact order.
var icebergDirections = [...]icebergDir{dirDown, dirUp, dirNorth, dirSouth, dirWest, dirEast}

// icebergHorizontal is Direction.Plane.HORIZONTAL (NORTH, EAST, SOUTH, WEST) —
// UnderwaterMagma.isValidPlacement iterates it.
var icebergHorizontal = [...]icebergDir{dirNorth, dirEast, dirSouth, dirWest}

// icebergStep returns the unit step vector of a Direction (BlockPos.relative).
func icebergStep(d icebergDir) (int, int, int) {
	switch d {
	case dirDown:
		return 0, -1, 0
	case dirUp:
		return 0, 1, 0
	case dirNorth:
		return 0, 0, -1
	case dirSouth:
		return 0, 0, 1
	case dirWest:
		return -1, 0, 0
	case dirEast:
		return 1, 0, 0
	}
	return 0, 0, 0
}

// icebergRelative ports BlockPos.relative(Direction): pos + unit step.
func icebergRelative(p placement.BlockPos, d icebergDir) placement.BlockPos {
	dx, dy, dz := icebergStep(d)
	return placement.BlockPos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
}

// icebergOpposite ports Direction.getOpposite() (XOR the low bit — DOWN<->UP, NORTH<->SOUTH,
// WEST<->EAST, matching the values() pairing).
func icebergOpposite(d icebergDir) icebergDir { return d ^ 1 }

// icebergIsBlockType reports state.is(Blocks.X) as a block-type check: true iff the concrete
// Block backing st is of type T. A waterlogged/other-property variant of the SAME concrete
// type still matches (the jar's `is` compares the Block, not the full state). Shared by the
// three ice/ocean bodies. Out-of-range ids read false.
func icebergIsBlockType[T block.Block](st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(T)
	return ok
}

// ---- iceberg body ----

// icebergBody ports IcebergFeature.place. The origin is re-anchored to sea level
// (chunkGenerator().getSeaLevel(), == bctx.seaLevelOr(63)) at the origin (x,z), matching the jar.
func icebergBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	mainBlock, err := feature.ResolveBlockStateJSON(icebergConfigState(cf))
	if err != nil {
		// A malformed / unresolved state cannot place a faithful iceberg; skip (no-op)
		// rather than panic — never crash decoration (LESSON from the sugar_cane batch).
		return false
	}

	seaLevel := bctx.seaLevelOr(63)
	origin := placement.BlockPos{X: pos.X, Y: seaLevel, Z: pos.Z}

	// place() head draws — order is the determinism contract (javap-verified).
	snowOnTop := rng.NextDouble() > 0.7
	shapeAngle := rng.NextDouble() * 2.0 * math.Pi
	shapeEllipseA := 11 - int(rng.NextIntN(5))
	shapeEllipseC := 3 + int(rng.NextIntN(3))
	isEllipse := rng.NextDouble() > 0.7

	overWaterHeight := 0
	if isEllipse {
		overWaterHeight = int(rng.NextIntN(6)) + 6
	} else {
		overWaterHeight = int(rng.NextIntN(15)) + 3
	}
	if !isEllipse && rng.NextDouble() > 0.9 {
		overWaterHeight += int(rng.NextIntN(19)) + 7
	}
	underWaterHeight := imin(overWaterHeight+int(rng.NextIntN(11)), 18)
	width := imin(overWaterHeight+int(rng.NextIntN(7))-int(rng.NextIntN(5)), 11)

	a := 11
	if isEllipse {
		a = shapeEllipseA
	}

	// Over-water body: nested xo/zo/yOff loops (xo,zo in [-a, a), yOff in [0, overWaterHeight)).
	for xo := -a; xo < a; xo++ {
		for zo := -a; zo < a; zo++ {
			for yOff := 0; yOff < overWaterHeight; yOff++ {
				var radius int
				if isEllipse {
					radius = icebergHeightDependentRadiusEllipse(yOff, overWaterHeight, width)
				} else {
					radius = icebergHeightDependentRadiusRound(rng, yOff, overWaterHeight, width)
				}
				if !isEllipse && xo >= radius {
					continue
				}
				icebergGenerateBlock(bctx, rng, origin, overWaterHeight, xo, yOff, zo, radius, a,
					isEllipse, shapeEllipseC, shapeAngle, snowOnTop, mainBlock)
			}
		}
	}

	icebergSmooth(bctx, origin, width, overWaterHeight, isEllipse, shapeEllipseA)

	// Under-water base: yOff runs -1 downto -(underWaterHeight-1).
	for xo := -a; xo < a; xo++ {
		for zo := -a; zo < a; zo++ {
			for yOff := -1; yOff > -underWaterHeight; yOff-- {
				newA := a
				if isEllipse {
					newA = mthCeil(float32(a) * (1.0 -
						float32(math.Pow(float64(yOff), 2.0))/(float32(underWaterHeight)*8.0)))
				}
				radius := icebergHeightDependentRadiusSteep(rng, -yOff, underWaterHeight, width)
				if xo >= radius {
					continue
				}
				icebergGenerateBlock(bctx, rng, origin, underWaterHeight, xo, yOff, zo, radius, newA,
					isEllipse, shapeEllipseC, shapeAngle, snowOnTop, mainBlock)
			}
		}
	}

	// Cut-out gate — SINGLE local written in BOTH branches, then tested (javap-verified).
	var cutout bool
	if isEllipse {
		cutout = rng.NextDouble() > 0.1
	} else {
		cutout = rng.NextDouble() > 0.7
	}
	if cutout {
		icebergGenerateCutOut(bctx, rng, width, overWaterHeight, origin, isEllipse,
			shapeEllipseA, shapeAngle, shapeEllipseC)
	}
	return true
}

// icebergGenerateCutOut ports IcebergFeature.generateCutOut.
func icebergGenerateCutOut(
	bctx *bodyContext, rng levelgen.RandomSource, width, height int,
	globalOrigin placement.BlockPos, isEllipse bool, shapeEllipseA int, shapeAngle float64, shapeEllipseC int,
) {
	randomSignX := 1
	if rng.NextBoolean() {
		randomSignX = -1
	}
	randomSignZ := 1
	if rng.NextBoolean() {
		randomSignZ = -1
	}
	xOff := int(rng.NextIntN(int32(imax(width/2-2, 1))))
	if rng.NextBoolean() {
		xOff = width/2 + 1 - int(rng.NextIntN(int32(imax(width-width/2-1, 1))))
	}
	zOff := int(rng.NextIntN(int32(imax(width/2-2, 1))))
	if rng.NextBoolean() {
		zOff = width/2 + 1 - int(rng.NextIntN(int32(imax(width-width/2-1, 1))))
	}
	if isEllipse {
		v := int(rng.NextIntN(int32(imax(shapeEllipseA-5, 1))))
		xOff = v
		zOff = v
	}
	localOrigin := placement.BlockPos{X: randomSignX * xOff, Y: 0, Z: randomSignZ * zOff}

	var angle float64
	if isEllipse {
		angle = shapeAngle + math.Pi/2.0 // 1.5707963267948966
	} else {
		angle = rng.NextDouble() * 2.0 * math.Pi
	}

	for yOff := 0; yOff < height-3; yOff++ {
		radius := icebergHeightDependentRadiusRound(rng, yOff, height, width)
		icebergCarve(bctx, radius, yOff, globalOrigin, false, angle, localOrigin, shapeEllipseA, shapeEllipseC)
	}
	// NOTE: the loop bound reads nextInt(5) each time it re-evaluates the condition — the jar
	// re-evaluates `-height + nextInt(5)` on every iteration (a fresh draw per check). Mirror it.
	for yOff := -1; yOff > -height+int(rng.NextIntN(5)); yOff-- {
		radius := icebergHeightDependentRadiusSteep(rng, -yOff, height, width)
		icebergCarve(bctx, radius, yOff, globalOrigin, true, angle, localOrigin, shapeEllipseA, shapeEllipseC)
	}
}

// icebergCarve ports IcebergFeature.carve.
func icebergCarve(
	bctx *bodyContext, radius, yOff int, globalOrigin placement.BlockPos,
	underWater bool, angle float64, localOrigin placement.BlockPos, shapeEllipseA, shapeEllipseC int,
) {
	a := radius + 1 + shapeEllipseA/3
	c := imin(radius-3, 3) + shapeEllipseC/2 - 1
	for xo := -a; xo < a; xo++ {
		for zo := -a; zo < a; zo++ {
			signedDist := icebergSignedDistanceEllipse(xo, zo, localOrigin, a, c, angle)
			if signedDist >= 0.0 {
				continue
			}
			pos := placement.BlockPos{X: globalOrigin.X + xo, Y: globalOrigin.Y + yOff, Z: globalOrigin.Z + zo}
			state := bctx.getState(pos)
			if !icebergIsIcebergState(state) && !icebergIsSnowBlock(state) {
				continue
			}
			if underWater {
				bctx.placeState(pos, icebergWaterStateID)
				continue
			}
			bctx.placeState(pos, bctx.airState())
			icebergRemoveFloatingSnowLayer(bctx, pos)
		}
	}
}

// icebergRemoveFloatingSnowLayer ports IcebergFeature.removeFloatingSnowLayer: if the block
// above pos is a SNOW layer, replace it with air.
func icebergRemoveFloatingSnowLayer(bctx *bodyContext, pos placement.BlockPos) {
	above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if icebergIsSnowLayer(bctx.getState(above)) {
		bctx.placeState(above, bctx.airState())
	}
}

// icebergGenerateBlock ports IcebergFeature.generateIcebergBlock.
func icebergGenerateBlock(
	bctx *bodyContext, rng levelgen.RandomSource, origin placement.BlockPos, height,
	xo, yOff, zo, radius, a int, isEllipse bool, shapeEllipseC int, shapeAngle float64,
	snowOnTop bool, mainBlockState block.StateID,
) {
	var signedDist float64
	if isEllipse {
		signedDist = icebergSignedDistanceEllipse(xo, zo, icebergZero, a,
			icebergGetEllipseC(yOff, height, shapeEllipseC), shapeAngle)
	} else {
		signedDist = icebergSignedDistanceCircle(xo, zo, icebergZero, radius, rng)
	}
	if signedDist >= 0.0 {
		return
	}
	pos := placement.BlockPos{X: origin.X + xo, Y: origin.Y + yOff, Z: origin.Z + zo}
	var compareVal float64
	if isEllipse {
		compareVal = -0.5
	} else {
		compareVal = float64(-6 - int(rng.NextIntN(3)))
	}
	if signedDist > compareVal && rng.NextDouble() > 0.9 {
		return
	}
	icebergSetBlock(bctx, pos, rng, height-yOff, height, isEllipse, snowOnTop, mainBlockState)
}

// icebergSetBlock ports IcebergFeature.setIcebergBlock.
func icebergSetBlock(
	bctx *bodyContext, pos placement.BlockPos, rng levelgen.RandomSource,
	hDiff, height int, isEllipse, snowOnTop bool, mainBlockState block.StateID,
) {
	state := bctx.getState(pos)
	if !(block.IsAir(state) || icebergIsSnowBlock(state) || icebergIsIce(state) || icebergIsWater(state)) {
		return
	}
	randomness := !isEllipse || rng.NextDouble() > 0.05
	divisor := 2
	if isEllipse {
		divisor = 3
	}
	if snowOnTop && !icebergIsWater(state) &&
		float64(hDiff) <= float64(int(rng.NextIntN(int32(imax(1, height/divisor)))))+float64(height)*0.6 &&
		randomness {
		bctx.placeState(pos, icebergSnowBlockStateID)
	} else {
		bctx.placeState(pos, mainBlockState)
	}
}

// icebergGetEllipseC ports IcebergFeature.getEllipseC.
func icebergGetEllipseC(yOff, height, shapeEllipseC int) int {
	c := shapeEllipseC
	if yOff > 0 && height-yOff <= 3 {
		c -= 4 - (height - yOff)
	}
	return c
}

// icebergSignedDistanceCircle ports IcebergFeature.signedDistanceCircle. NOTE: it draws one
// nextFloat() (RNG) — the draw happens on every non-ellipse cell.
//
//	off = 10.0f * Mth.clamp(nextFloat(), 0.2f, 0.8f) / (float)radius
//	return (double)off + pow(xo-ox,2) + pow(zo-oz,2) - pow(radius,2)
func icebergSignedDistanceCircle(xo, zo int, origin placement.BlockPos, radius int, rng levelgen.RandomSource) float64 {
	off := 10.0 * mthClampF(rng.NextFloat(), 0.2, 0.8) / float32(radius)
	return float64(off) +
		math.Pow(float64(xo-origin.X), 2.0) +
		math.Pow(float64(zo-origin.Z), 2.0) -
		math.Pow(float64(radius), 2.0)
}

// icebergSignedDistanceEllipse ports IcebergFeature.signedDistanceEllipse (rotated ellipse,
// all double math).
func icebergSignedDistanceEllipse(xo, zo int, origin placement.BlockPos, a, c int, angle float64) float64 {
	dx := float64(xo - origin.X)
	dz := float64(zo - origin.Z)
	return math.Pow((dx*math.Cos(angle)-dz*math.Sin(angle))/float64(a), 2.0) +
		math.Pow((dx*math.Sin(angle)+dz*math.Cos(angle))/float64(c), 2.0) -
		1.0
}

// icebergHeightDependentRadiusRound ports IcebergFeature.heightDependentRadiusRound. Draws
// nextFloat() first (k), and a second nextFloat via nextInt(5) inside the height>15+... test,
// plus nextInt(6) inside the tempYOff branch — all in bytecode order.
func icebergHeightDependentRadiusRound(rng levelgen.RandomSource, yOff, height, width int) int {
	k := 3.5 - rng.NextFloat()
	scale := (1.0 - float32(math.Pow(float64(yOff), 2.0))/(float32(height)*k)) * float32(width)
	if height > 15+int(rng.NextIntN(5)) {
		tempYOff := yOff
		if yOff < 3+int(rng.NextIntN(6)) {
			tempYOff = yOff / 2
		}
		scale = (1.0 - float32(tempYOff)/(float32(height)*k*0.4)) * float32(width)
	}
	return mthCeil(scale / 2.0)
}

// icebergHeightDependentRadiusEllipse ports IcebergFeature.heightDependentRadiusEllipse (no RNG).
func icebergHeightDependentRadiusEllipse(yOff, height, width int) int {
	scale := (1.0 - float32(math.Pow(float64(yOff), 2.0))/(float32(height)*1.0)) * float32(width)
	return mthCeil(scale / 2.0)
}

// icebergHeightDependentRadiusSteep ports IcebergFeature.heightDependentRadiusSteep. Draws
// one nextFloat() (k).
func icebergHeightDependentRadiusSteep(rng levelgen.RandomSource, yOff, height, width int) int {
	k := 1.0 + rng.NextFloat()/2.0
	scale := (1.0 - float32(yOff)/(float32(height)*k)) * float32(width)
	return mthCeil(scale / 2.0)
}

// icebergSmooth ports IcebergFeature.smooth (no RNG). Sweeps a solid-block window and shaves
// overhang / mostly-exposed ice into air.
func icebergSmooth(bctx *bodyContext, origin placement.BlockPos, width, height int, isEllipse bool, shapeEllipseA int) {
	a := width / 2
	if isEllipse {
		a = shapeEllipseA
	}
	for x := -a; x <= a; x++ {
		for z := -a; z <= a; z++ {
			for yOff := 0; yOff <= height; yOff++ {
				pos := placement.BlockPos{X: origin.X + x, Y: origin.Y + yOff, Z: origin.Z + z}
				state := bctx.getState(pos)
				if !icebergIsIcebergState(state) && !icebergIsSnowLayer(state) {
					continue
				}
				if icebergBelowIsAir(bctx, pos) {
					bctx.placeState(pos, bctx.airState())
					above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
					bctx.placeState(above, bctx.airState())
					continue
				}
				if !icebergIsIcebergState(state) {
					continue
				}
				counter := 0
				sides := [...]placement.BlockPos{
					{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, // west
					{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, // east
					{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, // north
					{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, // south
				}
				for _, sp := range sides {
					if icebergIsIcebergState(bctx.getState(sp)) {
						continue
					}
					counter++
				}
				if counter < 3 {
					continue
				}
				bctx.placeState(pos, bctx.airState())
			}
		}
	}
}

// icebergBelowIsAir ports IcebergFeature.belowIsAir.
func icebergBelowIsAir(bctx *bodyContext, pos placement.BlockPos) bool {
	below := placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	return block.IsAir(bctx.getState(below))
}

// ---- states / predicates (cited) ----

var (
	icebergWaterStateID     = block.ToStateID[block.Water{Level: 0}] // Blocks.WATER.defaultBlockState()
	icebergSnowBlockStateID = block.ToStateID[block.SnowBlock{}]     // Blocks.SNOW_BLOCK.defaultBlockState()
	icebergZero             = placement.BlockPos{X: 0, Y: 0, Z: 0}   // BlockPos.ZERO
)

// icebergIsIcebergState ports IcebergFeature.isIcebergState: PACKED_ICE || SNOW_BLOCK || BLUE_ICE.
func icebergIsIcebergState(st block.StateID) bool {
	return icebergIsBlockType[block.PackedIce](st) ||
		icebergIsSnowBlock(st) ||
		icebergIsBlockType[block.BlueIce](st)
}

// icebergIsWater ports state.is(Blocks.WATER).
func icebergIsWater(st block.StateID) bool { return icebergIsBlockType[block.Water](st) }

// icebergIsIce ports state.is(Blocks.ICE).
func icebergIsIce(st block.StateID) bool { return icebergIsBlockType[block.Ice](st) }

// icebergIsSnowBlock ports state.is(Blocks.SNOW_BLOCK) (the full snow_block, NOT the snow layer).
func icebergIsSnowBlock(st block.StateID) bool { return icebergIsBlockType[block.SnowBlock](st) }

// icebergIsSnowLayer ports state.is(Blocks.SNOW) (the SnowLayerBlock layer, any Layers value).
func icebergIsSnowLayer(st block.StateID) bool { return icebergIsBlockType[block.Snow](st) }

// icebergConfigState returns the raw config `state` object for the iceberg configured feature
// ({"state": {"Name": ...}} — BlockStateConfiguration).
func icebergConfigState(cf *feature.ConfiguredFeature) json.RawMessage {
	var j struct {
		State json.RawMessage `json:"state"`
	}
	if raw := configRaw(cf); len(raw) > 0 {
		_ = json.Unmarshal(raw, &j)
	}
	return j.State
}

// ---- small int/float helpers (Java-faithful) ----

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// mthClampF ports Mth.clamp(float, min, max): f < min ? min : Math.min(f, max) (javap-verified).
func mthClampF(f, minv, maxv float32) float32 {
	if f < minv {
		return minv
	}
	if maxv < f {
		return maxv
	}
	return f
}
