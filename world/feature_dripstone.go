package world

// feature_dripstone.go ports the three speleothem/dripstone feature bodies 1:1 from the
// unobfuscated Minecraft 26.2 jar (temp/cache/26.2-inner.jar, read via CFR / javap -c):
//
//   - net.minecraft.world.level.levelgen.feature.SpeleothemFeature.place        → "speleothem"
//       (single pointed dripstone; 26.2 renamed PointedDripstoneFeature → SpeleothemFeature)
//   - net.minecraft.world.level.levelgen.feature.SpeleothemClusterFeature.place → "speleothem_cluster"
//       (the dripstone_cluster feature; 26.2 renamed DripstoneClusterFeature → SpeleothemClusterFeature)
//   - net.minecraft.world.level.levelgen.feature.LargeDripstoneFeature.place    → "large_dripstone"
//   - net.minecraft.world.level.levelgen.feature.SpeleothemUtils.{getSpeleothemHeight,
//       isCircleMostlyEmbeddedInStone, isEmptyOrWater, isEmptyOrWaterOrLava, buildBaseToTipColumn,
//       growSpeleothem, placeBaseBlockIfPossible, createPointedBlock, isBaseOrLava, isBase,
//       isNeitherEmptyNorWater}
//   - net.minecraft.world.level.levelgen.Column.{scan,scanDirection,create,Range,Ray,Line}
//   - the configurations Speleothem/SpeleothemCluster/LargeDripstone (record + codec defaults)
//   - net.minecraft.util.valueproviders.{UniformFloat,ClampedNormalFloat,ClampedFloat,
//       TrapezoidFloat,ConstantFloat}.sample (the FloatProvider set)
//   - net.minecraft.util.Mth.{sin,cos,clampedMap,randomBetween,randomBetweenInclusive,sqrt,clamp}
//
// "speleothem" is the sub-feature of pointed_dripstone.json's simple_random_selector; before
// this port it was an unregistered no-op, so pointed_dripstone never generated. All three
// register via registerFeatureBody in init().
//
// The RNG-DRAW ORDER is the determinism contract (Pitfall 4) and is reproduced exactly; the
// column scans + solidity reads (isEmptyOrWater / isBase / Column.scan) read the live 3x3
// Neighborhood, which in a real cave has floors + ceilings but in synthetic empty test chunks
// finds none (so the placement branches short-circuit) — but the draw sequence for the config
// samplers is always executed jar-exact. Every write goes through bctx.placeState.

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("speleothem", speleothemBody)
	registerFeatureBody("speleothem_cluster", speleothemClusterBody)
	registerFeatureBody("large_dripstone", largeDripstoneBody)
}

// dripstoneGaussianSource is the nextGaussian draw a clamped_normal FloatProvider needs. The
// decoration rng is a *WorldgenRandom (embeds *LegacyRandomSource → NextGaussian), so the
// assertion holds at every real call site (placement.gaussianSource precedent).
type dripstoneGaussianSource interface {
	NextGaussian() float64
}

// ===========================================================================================
//  SpeleothemUtils (shared)
// ===========================================================================================

// speleothemHeight ports SpeleothemUtils.getSpeleothemHeight. All double math, exact.
func speleothemHeight(xzDistanceFromCenter, speleothemRadius, scale, bluntness float64) float64 {
	if xzDistanceFromCenter < bluntness {
		xzDistanceFromCenter = bluntness
	}
	const cutoff = 0.384
	r := xzDistanceFromCenter / speleothemRadius * cutoff
	part1 := 0.75 * math.Pow(r, 1.3333333333333333)
	part2 := math.Pow(r, 0.6666666666666666)
	part3 := 0.3333333333333333 * math.Log(r)
	h := scale * (part1 - part2 - part3)
	if h < 0.0 {
		h = 0.0
	}
	return h / cutoff * speleothemRadius
}

// isEmptyOrWater ports SpeleothemUtils.isEmptyOrWater(state): air || water.
func (b *bodyContext) isEmptyOrWater(pos placement.BlockPos) bool {
	st := b.getState(pos)
	return block.IsAir(st) || st == dripstoneWaterState
}

// isEmptyOrWaterOrLava ports SpeleothemUtils.isEmptyOrWaterOrLava(state): air || water || lava.
func (b *bodyContext) isEmptyOrWaterOrLava(pos placement.BlockPos) bool {
	st := b.getState(pos)
	return block.IsAir(st) || st == dripstoneWaterState || dripstoneIsLava(st)
}

// isNeitherEmptyNorWater ports SpeleothemUtils.isNeitherEmptyNorWater(state): !air && !water.
func (b *bodyContext) isNeitherEmptyNorWater(pos placement.BlockPos) bool {
	st := b.getState(pos)
	return !block.IsAir(st) && st != dripstoneWaterState
}

// isCircleMostlyEmbeddedInStone ports SpeleothemUtils.isCircleMostlyEmbeddedInStone: the
// center + a ring of `xzRadius` sampled at Mth-table angle increments must ALL be non-empty
// (not air/water/lava). No rng draws. Uses the Mth sine table so the integer offsets match
// vanilla cell-for-cell.
func (b *bodyContext) isCircleMostlyEmbeddedInStone(center placement.BlockPos, xzRadius int) bool {
	if b.isEmptyOrWaterOrLava(center) {
		return false
	}
	angleIncrement := float32(6.0) / float32(xzRadius)
	for angle := float32(0.0); angle < float32(math.Pi)*2; angle += angleIncrement {
		dx := int(mthCos(float64(angle)) * float32(xzRadius))
		dz := int(mthSin(float64(angle)) * float32(xzRadius))
		if b.isEmptyOrWaterOrLava(placement.BlockPos{X: center.X + dx, Y: center.Y, Z: center.Z + dz}) {
			return false
		}
	}
	return true
}

// isBase ports SpeleothemUtils.isBase(state): state.is(baseBlock) || state.is(replaceable).
func (b *bodyContext) speleothemIsBase(st block.StateID, baseBlock block.StateID, replaceable map[block.StateID]bool) bool {
	if dripstoneSameBlock(st, baseBlock) {
		return true
	}
	return replaceable[st]
}

// isBaseOrLava ports SpeleothemUtils.isBaseOrLava.
func (b *bodyContext) speleothemIsBaseOrLava(st block.StateID, baseBlock block.StateID, replaceable map[block.StateID]bool) bool {
	return b.speleothemIsBase(st, baseBlock, replaceable) || dripstoneIsLava(st)
}

// placeBaseBlockIfPossible ports SpeleothemUtils.placeBaseBlockIfPossible: if the block at
// pos is in `replaceable`, set it to baseBlock's default state and return true.
func (b *bodyContext) placeBaseBlockIfPossible(pos placement.BlockPos, baseBlock block.StateID, replaceable map[block.StateID]bool) bool {
	if replaceable[b.getState(pos)] {
		b.placeState(pos, baseBlock)
		return true
	}
	return false
}

// growSpeleothem ports SpeleothemUtils.growSpeleothem: if the block behind startPos (opposite
// the tip direction) is a base block, build a base-to-tip pointed-dripstone column of `height`
// blocks from startPos along tipDirection. The waterlogged flag on each pointed block is set
// from level.isWaterAt(pos). No rng draws.
func (b *bodyContext) growSpeleothem(startPos placement.BlockPos, tipDir geodeDir, height int, mergedTip bool, baseBlock block.StateID, pointedBlock block.StateID, replaceable map[block.StateID]bool) {
	behind := placement.BlockPos{X: startPos.X - tipDir.dx, Y: startPos.Y - tipDir.dy, Z: startPos.Z - tipDir.dz}
	if !b.speleothemIsBase(b.getState(behind), baseBlock, replaceable) {
		return
	}
	pos := startPos
	b.buildBaseToTipColumn(tipDir.d, height, mergedTip, pointedBlock, func(thickness block.SpeleothemThickness) {
		// createPointedBlock: pointedBlock.default with TIP_DIRECTION=dir, THICKNESS=thickness,
		// then waterlogged = isWaterAt(pos).
		st := dripstonePointedState(tipDir.d, thickness, b.getState(pos) == dripstoneWaterState)
		b.placeState(pos, st)
		pos = placement.BlockPos{X: pos.X + tipDir.dx, Y: pos.Y + tipDir.dy, Z: pos.Z + tipDir.dz}
	})
}

// buildBaseToTipColumn ports SpeleothemUtils.buildBaseToTipColumn: emit BASE, (length-3)×
// MIDDLE, FRUSTUM, TIP/TIP_MERGE thickness values in order for a column of totalLength.
func (b *bodyContext) buildBaseToTipColumn(_ block.Direction, totalLength int, mergedTip bool, _ block.StateID, emit func(block.SpeleothemThickness)) {
	if totalLength >= 3 {
		emit(block.SpeleothemThicknessBase)
		for i := 0; i < totalLength-3; i++ {
			emit(block.SpeleothemThicknessMiddle)
		}
	}
	if totalLength >= 2 {
		emit(block.SpeleothemThicknessFrustum)
	}
	if totalLength >= 1 {
		if mergedTip {
			emit(block.SpeleothemThicknessTipMerge)
		} else {
			emit(block.SpeleothemThicknessTip)
		}
	}
}

// ===========================================================================================
//  Column.scan (shared)
// ===========================================================================================

// dripstoneColumn ports net.minecraft.world.level.levelgen.Column: a scanned cave column with
// an optional floor + ceiling Y. hasFloor/hasCeiling model OptionalInt presence.
type dripstoneColumn struct {
	hasFloor, hasCeiling bool
	floor, ceiling       int
}

func (c dripstoneColumn) height() (int, bool) {
	// Column.Range.height() = ceiling - floor - 1 (only defined when both present).
	if c.hasFloor && c.hasCeiling {
		return c.ceiling - c.floor - 1, true
	}
	return 0, false
}

// dripstoneColumnScan ports Column.scan(level, pos, searchRange, insideColumn, validEdge):
// require the origin to be inside-column, then scan UP for the ceiling edge and DOWN for the
// floor edge. Returns ok=false when the origin is not inside-column (Optional.empty).
func (b *bodyContext) dripstoneColumnScan(pos placement.BlockPos, searchRange int, inside, validEdge func(placement.BlockPos) bool) (dripstoneColumn, bool) {
	if !inside(pos) {
		return dripstoneColumn{}, false
	}
	ceilY, hasCeil := b.dripstoneScanDirection(pos, searchRange, inside, validEdge, geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0})
	floorY, hasFloor := b.dripstoneScanDirection(pos, searchRange, inside, validEdge, geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0})
	return dripstoneColumn{hasFloor: hasFloor, floor: floorY, hasCeiling: hasCeil, ceiling: ceilY}, true
}

// dripstoneScanDirection ports Column.scanDirection: walk from nearestEmptyY along `dir` while
// inside-column (up to searchRange), then report the reached Y iff it is a valid edge.
func (b *bodyContext) dripstoneScanDirection(pos placement.BlockPos, searchRange int, inside, validEdge func(placement.BlockPos) bool, dir geodeDir) (int, bool) {
	cur := placement.BlockPos{X: pos.X, Y: pos.Y, Z: pos.Z}
	for i := 1; i < searchRange && inside(cur); i++ {
		cur = placement.BlockPos{X: cur.X + dir.dx, Y: cur.Y + dir.dy, Z: cur.Z + dir.dz}
	}
	if validEdge(cur) {
		return cur.Y, true
	}
	return 0, false
}

// ===========================================================================================
//  SpeleothemFeature (single pointed dripstone)
// ===========================================================================================

// speleothemConfig is the decoded SpeleothemConfiguration (defaults from the codec:
// chance_of_taller_generation=0.2, chance_of_directional_spread=0.7, chance_of_spread_radius2
// =0.5, chance_of_spread_radius3=0.5). pointed_dripstone.json omits all four → defaults.
type speleothemConfig struct {
	baseBlock             block.StateID
	pointedBlock          block.StateID
	replaceable           map[block.StateID]bool
	chanceOfTaller        float32
	chanceOfDirectional   float32
	chanceOfSpreadRadius2 float32
	chanceOfSpreadRadius3 float32
	err                   error
}

var speleothemCache sync.Map // map[*feature.ConfiguredFeature]*speleothemConfig

func decodeSpeleothemCached(cf *feature.ConfiguredFeature) *speleothemConfig {
	if v, ok := speleothemCache.Load(cf); ok {
		return v.(*speleothemConfig)
	}
	d := &speleothemConfig{
		chanceOfTaller:        0.2,
		chanceOfDirectional:   0.7,
		chanceOfSpreadRadius2: 0.5,
		chanceOfSpreadRadius3: 0.5,
	}
	var j struct {
		BaseBlock             json.RawMessage `json:"base_block"`
		PointedBlock          json.RawMessage `json:"pointed_block"`
		ReplaceableBlocks     string          `json:"replaceable_blocks"`
		ChanceOfTaller        *float32        `json:"chance_of_taller_generation"`
		ChanceOfDirectional   *float32        `json:"chance_of_directional_spread"`
		ChanceOfSpreadRadius2 *float32        `json:"chance_of_spread_radius2"`
		ChanceOfSpreadRadius3 *float32        `json:"chance_of_spread_radius3"`
	}
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: speleothem config %q: %w", cf.ID, err)
		speleothemCache.Store(cf, d)
		return d
	}
	base, err := feature.ResolveBlockStateJSON(j.BaseBlock)
	if err != nil {
		d.err = fmt.Errorf("world: speleothem base_block %q: %w", cf.ID, err)
		speleothemCache.Store(cf, d)
		return d
	}
	pointed, err := feature.ResolveBlockStateJSON(j.PointedBlock)
	if err != nil {
		d.err = fmt.Errorf("world: speleothem pointed_block %q: %w", cf.ID, err)
		speleothemCache.Store(cf, d)
		return d
	}
	d.baseBlock = base
	d.pointedBlock = pointed
	d.replaceable = dripstoneReplaceableSet(j.ReplaceableBlocks)
	if j.ChanceOfTaller != nil {
		d.chanceOfTaller = *j.ChanceOfTaller
	}
	if j.ChanceOfDirectional != nil {
		d.chanceOfDirectional = *j.ChanceOfDirectional
	}
	if j.ChanceOfSpreadRadius2 != nil {
		d.chanceOfSpreadRadius2 = *j.ChanceOfSpreadRadius2
	}
	if j.ChanceOfSpreadRadius3 != nil {
		d.chanceOfSpreadRadius3 = *j.ChanceOfSpreadRadius3
	}
	speleothemCache.Store(cf, d)
	return d
}

// speleothemBody ports SpeleothemFeature.place. Draw order:
//  1. getTipDirection: nextBoolean ONLY when both above+below are base (the DOWN/UP pick).
//  2. createPatchOfBaseBlocks: per horizontal dir [N,E,S,W]: nextFloat (spread); if pass,
//     nextFloat (radius2) + Direction.getRandom; if pass, nextFloat (radius3) + getRandom.
//  3. height: nextFloat() < chanceOfTaller && isEmptyOrWater(pos+tip) ? 2 : 1.
//  4. growSpeleothem: 0 draws.
func speleothemBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg := decodeSpeleothemCached(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}

	tip, ok := bctx.speleothemTipDirection(cfg, pos, rng)
	if !ok {
		return false
	}
	rootPos := placement.BlockPos{X: pos.X - tip.dx, Y: pos.Y - tip.dy, Z: pos.Z - tip.dz}
	bctx.speleothemCreatePatch(cfg, rng, rootPos)

	tipTarget := placement.BlockPos{X: pos.X + tip.dx, Y: pos.Y + tip.dy, Z: pos.Z + tip.dz}
	height := 1
	if rng.NextFloat() < cfg.chanceOfTaller && bctx.isEmptyOrWater(tipTarget) {
		height = 2
	}
	bctx.growSpeleothem(pos, tip, height, false, cfg.baseBlock, cfg.pointedBlock, cfg.replaceable)
	return true
}

// speleothemTipDirection ports SpeleothemFeature.getTipDirection: DOWN if only-above is base,
// UP if only-below is base, a nextBoolean pick (DOWN/UP) if BOTH, empty otherwise.
func (b *bodyContext) speleothemTipDirection(cfg *speleothemConfig, pos placement.BlockPos, rng levelgen.RandomSource) (geodeDir, bool) {
	above := b.getState(placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
	below := b.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	canAbove := b.speleothemIsBase(above, cfg.baseBlock, cfg.replaceable)
	canBelow := b.speleothemIsBase(below, cfg.baseBlock, cfg.replaceable)
	if canAbove && canBelow {
		// nextBoolean ? DOWN : UP.
		if dripstoneNextBoolean(rng) {
			return geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0}, true
		}
		return geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0}, true
	}
	if canAbove {
		return geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0}, true
	}
	if canBelow {
		return geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0}, true
	}
	return geodeDir{}, false
}

// speleothemCreatePatch ports SpeleothemFeature.createPatchOfBaseBlocks: place a base block at
// the root, then for each HORIZONTAL direction [N,E,S,W] roll the spread chances, drawing a
// random direction (of all 6) for each successful radius-2/radius-3 spread.
func (b *bodyContext) speleothemCreatePatch(cfg *speleothemConfig, rng levelgen.RandomSource, pos placement.BlockPos) {
	b.placeBaseBlockIfPossible(pos, cfg.baseBlock, cfg.replaceable)
	for _, dir := range dripstoneHorizontal {
		if rng.NextFloat() > cfg.chanceOfDirectional {
			continue
		}
		pos1 := placement.BlockPos{X: pos.X + dir.dx, Y: pos.Y + dir.dy, Z: pos.Z + dir.dz}
		b.placeBaseBlockIfPossible(pos1, cfg.baseBlock, cfg.replaceable)
		if rng.NextFloat() > cfg.chanceOfSpreadRadius2 {
			continue
		}
		d2 := dripstoneRandomDirection(rng)
		pos2 := placement.BlockPos{X: pos1.X + d2.dx, Y: pos1.Y + d2.dy, Z: pos1.Z + d2.dz}
		b.placeBaseBlockIfPossible(pos2, cfg.baseBlock, cfg.replaceable)
		if rng.NextFloat() > cfg.chanceOfSpreadRadius3 {
			continue
		}
		d3 := dripstoneRandomDirection(rng)
		pos3 := placement.BlockPos{X: pos2.X + d3.dx, Y: pos2.Y + d3.dy, Z: pos2.Z + d3.dz}
		b.placeBaseBlockIfPossible(pos3, cfg.baseBlock, cfg.replaceable)
	}
}

// ===========================================================================================
//  SpeleothemClusterFeature (dripstone_cluster)
// ===========================================================================================

// speleothemClusterConfig is the decoded SpeleothemClusterConfiguration (all fields required
// by the codec; dripstone_cluster.json supplies every one).
type speleothemClusterConfig struct {
	baseBlock                block.StateID
	pointedBlock             block.StateID
	replaceable              map[block.StateID]bool
	floorToCeilingSearchRange int
	height                   *dripstoneIntProvider
	radius                   *dripstoneIntProvider
	maxStalagmiteStalactiteHeightDiff int
	heightDeviation          int
	layerThickness           *dripstoneIntProvider
	density                  *dripstoneFloatProvider
	wetness                  *dripstoneFloatProvider
	chanceAtMaxDistance      float32
	maxDistFromEdge          int
	maxDistFromCenterHeight  int
	err                      error
}

var speleothemClusterCache sync.Map // map[*feature.ConfiguredFeature]*speleothemClusterConfig

func decodeSpeleothemClusterCached(cf *feature.ConfiguredFeature) *speleothemClusterConfig {
	if v, ok := speleothemClusterCache.Load(cf); ok {
		return v.(*speleothemClusterConfig)
	}
	d := &speleothemClusterConfig{}
	var j struct {
		BaseBlock         json.RawMessage `json:"base_block"`
		PointedBlock      json.RawMessage `json:"pointed_block"`
		ReplaceableBlocks string          `json:"replaceable_blocks"`
		FloorToCeiling    int             `json:"floor_to_ceiling_search_range"`
		Height            json.RawMessage `json:"height"`
		Radius            json.RawMessage `json:"radius"`
		MaxHeightDiff     int             `json:"max_stalagmite_stalactite_height_diff"`
		HeightDeviation   int             `json:"height_deviation"`
		LayerThickness    json.RawMessage `json:"speleothem_block_layer_thickness"`
		Density           json.RawMessage `json:"density"`
		Wetness           json.RawMessage `json:"wetness"`
		ChanceAtMax       float32         `json:"chance_of_speleothem_at_max_distance_from_center"`
		MaxDistFromEdge   int             `json:"max_distance_from_edge_affecting_chance_of_speleothem"`
		MaxDistFromCenter int             `json:"max_distance_from_center_affecting_height_bias"`
	}
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster config %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	var err error
	if d.baseBlock, err = feature.ResolveBlockStateJSON(j.BaseBlock); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster base_block %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	if d.pointedBlock, err = feature.ResolveBlockStateJSON(j.PointedBlock); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster pointed_block %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	d.replaceable = dripstoneReplaceableSet(j.ReplaceableBlocks)
	d.floorToCeilingSearchRange = j.FloorToCeiling
	if d.height, err = parseDripstoneIntProvider(j.Height); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster height %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	if d.radius, err = parseDripstoneIntProvider(j.Radius); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster radius %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	d.maxStalagmiteStalactiteHeightDiff = j.MaxHeightDiff
	d.heightDeviation = j.HeightDeviation
	if d.layerThickness, err = parseDripstoneIntProvider(j.LayerThickness); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster speleothem_block_layer_thickness %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	if d.density, err = parseDripstoneFloatProvider(j.Density); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster density %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	if d.wetness, err = parseDripstoneFloatProvider(j.Wetness); err != nil {
		d.err = fmt.Errorf("world: speleothem_cluster wetness %q: %w", cf.ID, err)
		speleothemClusterCache.Store(cf, d)
		return d
	}
	d.chanceAtMaxDistance = j.ChanceAtMax
	d.maxDistFromEdge = j.MaxDistFromEdge
	d.maxDistFromCenterHeight = j.MaxDistFromCenter
	speleothemClusterCache.Store(cf, d)
	return d
}

// (renamed field access helper to keep the struct field name legible)
func (c *speleothemClusterConfig) chanceOfSpeleothemAtMaxDistance() float32 { return c.chanceAtMaxDistance }

// speleothemClusterBody ports SpeleothemClusterFeature.place. Draw order:
//
//	if !isEmptyOrWater(origin): return false
//	height  = height.sample(random)      [IntProvider]
//	wetness = wetness.sample(random)     [FloatProvider — clamped_normal ⇒ 1 gaussian]
//	density = density.sample(random)     [FloatProvider — uniform ⇒ 1 nextFloat]
//	xRadius = radius.sample(random)      [IntProvider]
//	zRadius = radius.sample(random)      [IntProvider]
//	for dx in [-xRadius..xRadius]: for dz in [-zRadius..zRadius]: placeColumn(...)  (dx outer)
func speleothemClusterBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg := decodeSpeleothemClusterCached(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}
	if !bctx.isEmptyOrWater(origin) {
		return false
	}
	height := cfg.height.sample(rng)
	wetness := cfg.wetness.sample(rng)
	density := cfg.density.sample(rng)
	xRadius := cfg.radius.sample(rng)
	zRadius := cfg.radius.sample(rng)
	for dx := -xRadius; dx <= xRadius; dx++ {
		for dz := -zRadius; dz <= zRadius; dz++ {
			chance := speleothemClusterChanceOfStalagmiteOrStalactite(xRadius, zRadius, dx, dz, cfg)
			pos := placement.BlockPos{X: origin.X + dx, Y: origin.Y, Z: origin.Z + dz}
			bctx.speleothemClusterPlaceColumn(cfg, rng, pos, dx, dz, wetness, chance, height, density)
		}
	}
	return true
}

// speleothemClusterPlaceColumn ports SpeleothemClusterFeature.placeColumn (draw order below).
func (b *bodyContext) speleothemClusterPlaceColumn(cfg *speleothemClusterConfig, rng levelgen.RandomSource, pos placement.BlockPos, dx, dz int, chanceOfWater float32, chanceOfStal float64, clusterHeight int, density float32) {
	col, ok := b.dripstoneColumnScan(pos, cfg.floorToCeilingSearchRange, b.isEmptyOrWater, b.isNeitherEmptyNorWater)
	if !ok {
		return
	}
	if !col.hasCeiling && !col.hasFloor {
		return
	}

	// wantPool = nextFloat() < chanceOfWater.
	wantPool := rng.NextFloat() < chanceOfWater
	if wantPool && col.hasFloor && b.speleothemCanPlacePool(cfg, placement.BlockPos{X: pos.X, Y: col.floor, Z: pos.Z}) {
		baseFloorY := col.floor
		b.placeState(placement.BlockPos{X: pos.X, Y: baseFloorY, Z: pos.Z}, dripstoneWaterState)
		col.floor = baseFloorY - 1
		// col.floor still present (withFloor keeps floor present).
	}
	floorPresent := col.hasFloor
	floorY := col.floor

	// wantStalactite = nextDouble() < chanceOfStal.
	wantStalactite := rng.NextDouble() < chanceOfStal
	stalactiteHeight := 0
	if col.hasCeiling && wantStalactite && !b.dripstoneIsLavaAt(placement.BlockPos{X: pos.X, Y: col.ceiling, Z: pos.Z}) {
		ceilingThickness := cfg.layerThickness.sample(rng)
		b.speleothemReplaceWithBase(cfg, placement.BlockPos{X: pos.X, Y: col.ceiling, Z: pos.Z}, ceilingThickness, geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0})
		maxHeightForThisColumn := clusterHeight
		if floorPresent {
			if v := col.ceiling - floorY; v < clusterHeight {
				maxHeightForThisColumn = v
			}
		}
		stalactiteHeight = b.speleothemGetHeight(cfg, rng, dx, dz, density, maxHeightForThisColumn)
	}

	// wantStalagmite = nextDouble() < chanceOfStal.
	wantStalagmite := rng.NextDouble() < chanceOfStal
	stalagmiteHeight := 0
	if floorPresent && wantStalagmite && !b.dripstoneIsLavaAt(placement.BlockPos{X: pos.X, Y: floorY, Z: pos.Z}) {
		floorThickness := cfg.layerThickness.sample(rng)
		b.speleothemReplaceWithBase(cfg, placement.BlockPos{X: pos.X, Y: floorY, Z: pos.Z}, floorThickness, geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0})
		if col.hasCeiling {
			diff := mthRandomBetweenInclusive(rng, -cfg.maxStalagmiteStalactiteHeightDiff, cfg.maxStalagmiteStalactiteHeightDiff)
			stalagmiteHeight = stalactiteHeight + diff
			if stalagmiteHeight < 0 {
				stalagmiteHeight = 0
			}
		} else {
			stalagmiteHeight = b.speleothemGetHeight(cfg, rng, dx, dz, density, clusterHeight)
		}
	}

	actualStalactiteHeight := stalactiteHeight
	actualStalagmiteHeight := stalagmiteHeight
	if col.hasCeiling && floorPresent && col.ceiling-stalactiteHeight <= floorY+stalagmiteHeight {
		ceilingY := col.ceiling
		lowestStalactiteBottom := max(ceilingY-stalactiteHeight, floorY+1)
		highestStalagmiteTop := min(floorY+stalagmiteHeight, ceilingY-1)
		actualStalactiteBottom := mthRandomBetweenInclusive(rng, lowestStalactiteBottom, highestStalagmiteTop+1)
		actualStalagmiteTop := actualStalactiteBottom - 1
		actualStalactiteHeight = ceilingY - actualStalactiteBottom
		actualStalagmiteHeight = actualStalagmiteTop - floorY
	}

	// mergeTips = nextBoolean() && actualStalactiteHeight>0 && actualStalagmiteHeight>0 &&
	//             column.getHeight().isPresent() && stalactite+stalagmite == column.height.
	colHeight, colHeightPresent := col.height()
	mergeTips := dripstoneNextBoolean(rng) &&
		actualStalactiteHeight > 0 && actualStalagmiteHeight > 0 &&
		colHeightPresent && (actualStalactiteHeight+actualStalagmiteHeight == colHeight)

	if col.hasCeiling {
		b.growSpeleothem(placement.BlockPos{X: pos.X, Y: col.ceiling - 1, Z: pos.Z},
			geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0}, actualStalactiteHeight, mergeTips,
			cfg.baseBlock, cfg.pointedBlock, cfg.replaceable)
	}
	if floorPresent {
		b.growSpeleothem(placement.BlockPos{X: pos.X, Y: floorY + 1, Z: pos.Z},
			geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0}, actualStalagmiteHeight, mergeTips,
			cfg.baseBlock, cfg.pointedBlock, cfg.replaceable)
	}
}

// speleothemGetHeight ports SpeleothemClusterFeature.getSpeleothemHeight:
//
//	if nextFloat() > density: return 0
//	distanceFromCenter = |dx| + |dz|
//	heightMean = clampedMap(distanceFromCenter, 0, maxDistFromCenterHeight, maxHeight/2, 0)
//	return (int) ClampedNormalFloat.sample(random, 0, maxHeight, heightMean, heightDeviation)
func (b *bodyContext) speleothemGetHeight(cfg *speleothemClusterConfig, rng levelgen.RandomSource, dx, dz int, density float32, maxHeight int) int {
	if rng.NextFloat() > density {
		return 0
	}
	distanceFromCenter := abs(dx) + abs(dz)
	heightMean := float32(mthClampedMapD(float64(distanceFromCenter), 0.0, float64(cfg.maxDistFromCenterHeight), float64(maxHeight)/2.0, 0.0))
	// randomBetweenBiased(random, 0, maxHeight, heightMean, heightDeviation) =
	// ClampedNormalFloat.sample(rng, mean=heightMean, dev=heightDeviation, min=0, max=maxHeight).
	v := clampedNormalFloatSample(rng, heightMean, float32(cfg.heightDeviation), 0.0, float32(maxHeight))
	return int(v)
}

// speleothemReplaceWithBase ports SpeleothemClusterFeature.replaceBlocksWithBaseBlocks:
// place up to maxCount base blocks from firstPos along direction, stopping at the first
// non-replaceable cell. No rng draws.
func (b *bodyContext) speleothemReplaceWithBase(cfg *speleothemClusterConfig, firstPos placement.BlockPos, maxCount int, dir geodeDir) {
	pos := firstPos
	for i := 0; i < maxCount; i++ {
		if !b.placeBaseBlockIfPossible(pos, cfg.baseBlock, cfg.replaceable) {
			return
		}
		pos = placement.BlockPos{X: pos.X + dir.dx, Y: pos.Y + dir.dy, Z: pos.Z + dir.dz}
	}
}

// speleothemCanPlacePool ports SpeleothemClusterFeature.canPlacePool.
func (b *bodyContext) speleothemCanPlacePool(cfg *speleothemClusterConfig, pos placement.BlockPos) bool {
	st := b.getState(pos)
	if st == dripstoneWaterState || dripstoneSameBlock(st, cfg.baseBlock) || dripstoneSameBlock(st, cfg.pointedBlock) {
		return false
	}
	// getBlockState(pos.above()).getFluidState().is(WATER) → water source above disqualifies.
	if b.getState(placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}) == dripstoneWaterState {
		return false
	}
	for _, dir := range dripstoneHorizontal {
		if !b.speleothemCanBeAdjacentToWater(placement.BlockPos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}) {
			return false
		}
	}
	return b.speleothemCanBeAdjacentToWater(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
}

// speleothemCanBeAdjacentToWater ports SpeleothemClusterFeature.canBeAdjacentToWater:
// state.is(#base_stone_overworld) || state.getFluidState().is(WATER).
func (b *bodyContext) speleothemCanBeAdjacentToWater(pos placement.BlockPos) bool {
	st := b.getState(pos)
	return dripstoneBaseStoneOverworldSet[st] || st == dripstoneWaterState
}

// speleothemClusterChanceOfStalagmiteOrStalactite ports getChanceOfStalagmiteOrStalactite:
// clampedMap(min(xEdgeDist, zEdgeDist), 0, maxDistFromEdge, chanceAtMaxDistance, 1.0).
func speleothemClusterChanceOfStalagmiteOrStalactite(xRadius, zRadius, dx, dz int, cfg *speleothemClusterConfig) float64 {
	xDistanceFromEdge := xRadius - abs(dx)
	zDistanceFromEdge := zRadius - abs(dz)
	distanceFromEdge := min(xDistanceFromEdge, zDistanceFromEdge)
	// Mth.clampedMap(float,float,float,float,float) — float overload; the args widen from int.
	return float64(mthClampedMapF(float32(distanceFromEdge), 0.0, float32(cfg.maxDistFromEdge), cfg.chanceOfSpeleothemAtMaxDistance(), 1.0))
}

// ===========================================================================================
//  LargeDripstoneFeature
// ===========================================================================================

// largeDripstoneConfig is the decoded LargeDripstoneConfiguration (floor_to_ceiling_search_range
// defaults to 30; large_dripstone.json omits it → default).
type largeDripstoneConfig struct {
	replaceable                map[block.StateID]bool
	floorToCeilingSearchRange  int
	columnRadius               *dripstoneIntProvider
	heightScale                *dripstoneFloatProvider
	maxColumnRadiusToCaveRatio float32
	stalactiteBluntness        *dripstoneFloatProvider
	stalagmiteBluntness        *dripstoneFloatProvider
	windSpeed                  *dripstoneFloatProvider
	minRadiusForWind           int
	minBluntnessForWind        float32
	err                        error
}

var largeDripstoneCache sync.Map // map[*feature.ConfiguredFeature]*largeDripstoneConfig

func decodeLargeDripstoneCached(cf *feature.ConfiguredFeature) *largeDripstoneConfig {
	if v, ok := largeDripstoneCache.Load(cf); ok {
		return v.(*largeDripstoneConfig)
	}
	d := &largeDripstoneConfig{floorToCeilingSearchRange: 30}
	var j struct {
		ReplaceableBlocks   string          `json:"replaceable_blocks"`
		FloorToCeiling      *int            `json:"floor_to_ceiling_search_range"`
		ColumnRadius        json.RawMessage `json:"column_radius"`
		HeightScale         json.RawMessage `json:"height_scale"`
		MaxRatio            float32         `json:"max_column_radius_to_cave_height_ratio"`
		StalactiteBluntness json.RawMessage `json:"stalactite_bluntness"`
		StalagmiteBluntness json.RawMessage `json:"stalagmite_bluntness"`
		WindSpeed           json.RawMessage `json:"wind_speed"`
		MinRadiusForWind    int             `json:"min_radius_for_wind"`
		MinBluntnessForWind float32         `json:"min_bluntness_for_wind"`
	}
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: large_dripstone config %q: %w", cf.ID, err)
		largeDripstoneCache.Store(cf, d)
		return d
	}
	d.replaceable = dripstoneReplaceableSet(j.ReplaceableBlocks)
	if j.FloorToCeiling != nil {
		d.floorToCeilingSearchRange = *j.FloorToCeiling
	}
	var err error
	if d.columnRadius, err = parseDripstoneIntProvider(j.ColumnRadius); err != nil {
		d.err = fmt.Errorf("world: large_dripstone column_radius %q: %w", cf.ID, err)
		largeDripstoneCache.Store(cf, d)
		return d
	}
	if d.heightScale, err = parseDripstoneFloatProvider(j.HeightScale); err != nil {
		d.err = fmt.Errorf("world: large_dripstone height_scale %q: %w", cf.ID, err)
		largeDripstoneCache.Store(cf, d)
		return d
	}
	d.maxColumnRadiusToCaveRatio = j.MaxRatio
	if d.stalactiteBluntness, err = parseDripstoneFloatProvider(j.StalactiteBluntness); err != nil {
		d.err = fmt.Errorf("world: large_dripstone stalactite_bluntness %q: %w", cf.ID, err)
		largeDripstoneCache.Store(cf, d)
		return d
	}
	if d.stalagmiteBluntness, err = parseDripstoneFloatProvider(j.StalagmiteBluntness); err != nil {
		d.err = fmt.Errorf("world: large_dripstone stalagmite_bluntness %q: %w", cf.ID, err)
		largeDripstoneCache.Store(cf, d)
		return d
	}
	if d.windSpeed, err = parseDripstoneFloatProvider(j.WindSpeed); err != nil {
		d.err = fmt.Errorf("world: large_dripstone wind_speed %q: %w", cf.ID, err)
		largeDripstoneCache.Store(cf, d)
		return d
	}
	d.minRadiusForWind = j.MinRadiusForWind
	d.minBluntnessForWind = j.MinBluntnessForWind
	largeDripstoneCache.Store(cf, d)
	return d
}

// largeDripstone models the LargeDripstone inner record.
type largeDripstone struct {
	root       placement.BlockPos
	pointingUp bool
	radius     int
	bluntness  float64
	scale      float64
}

func (l *largeDripstone) getHeight() int { return l.getHeightAtRadius(0.0) }
func (l *largeDripstone) getHeightAtRadius(checkRadius float32) int {
	return int(speleothemHeight(float64(checkRadius), float64(l.radius), l.scale, l.bluntness))
}

// windOffsetter models LargeDripstoneFeature.WindOffsetter.
type windOffsetter struct {
	originY int
	hasWind bool
	windX   float64
	windZ   float64
	maxOff  int
}

func (w windOffsetter) offset(pos placement.BlockPos) placement.BlockPos {
	if !w.hasWind {
		return pos
	}
	dy := w.originY - pos.Y
	totalX := w.windX * float64(dy)
	totalZ := w.windZ * float64(dy)
	dx := mthClampI(mthFloor(totalX), -w.maxOff, w.maxOff)
	dz := mthClampI(mthFloor(totalZ), -w.maxOff, w.maxOff)
	return placement.BlockPos{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz}
}

// largeDripstoneBody ports LargeDripstoneFeature.place. Draw order:
//
//	if !isEmptyOrWater(origin): return false
//	Column.scan (0 draws) → require a Range with height >= 4
//	radius = randomBetweenInclusive(random, columnRadius.min, maxColumnRadius)  [1 draw]
//	stalactite = makeDripstone(...): bluntness.sample + heightScale.sample       [2 draws]
//	stalagmite = makeDripstone(...): bluntness.sample + heightScale.sample       [2 draws]
//	if suitableForWind (both): WindOffsetter(originY, random, windSpeed, 16-radius):
//	    windSpeed.sample + randomBetween(0, PI)                                  [2 draws]
//	moveBack (0 draws), placeBlocks: per placed column cell, nextFloat + maybe randomBetween.
func largeDripstoneBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg := decodeLargeDripstoneCached(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}
	if !bctx.isEmptyOrWater(origin) {
		return false
	}
	col, ok := bctx.dripstoneColumnScan(origin, cfg.floorToCeilingSearchRange, bctx.isEmptyOrWater,
		func(p placement.BlockPos) bool {
			return bctx.speleothemIsBaseOrLava(bctx.getState(p), dripstoneBlockState, cfg.replaceable)
		})
	// Require a Range (both floor + ceiling present) with height >= 4.
	if !ok || !col.hasFloor || !col.hasCeiling {
		return false
	}
	colHeight := col.ceiling - col.floor - 1
	if colHeight < 4 {
		return false
	}

	maxColumnRadiusBasedOnColumnHeight := int(float32(colHeight) * cfg.maxColumnRadiusToCaveRatio)
	maxColumnRadius := mthClampI(maxColumnRadiusBasedOnColumnHeight, cfg.columnRadius.minInclusive(), cfg.columnRadius.maxInclusive())
	radius := mthRandomBetweenInclusive(rng, cfg.columnRadius.minInclusive(), maxColumnRadius)

	// makeDripstone(root, pointingUp, radius, bluntness.sample, heightScale.sample).
	stalactite := largeDripstone{
		root:       placement.BlockPos{X: origin.X, Y: col.ceiling - 1, Z: origin.Z},
		pointingUp: false,
		radius:     radius,
		bluntness:  float64(cfg.stalactiteBluntness.sample(rng)),
		scale:      float64(cfg.heightScale.sample(rng)),
	}
	stalagmite := largeDripstone{
		root:       placement.BlockPos{X: origin.X, Y: col.floor + 1, Z: origin.Z},
		pointingUp: true,
		radius:     radius,
		bluntness:  float64(cfg.stalagmiteBluntness.sample(rng)),
		scale:      float64(cfg.heightScale.sample(rng)),
	}

	var wind windOffsetter
	if largeDripstoneSuitableForWind(&stalactite, cfg) && largeDripstoneSuitableForWind(&stalagmite, cfg) {
		// WindOffsetter(originY, random, windSpeed, 16 - radius).
		speed := cfg.windSpeed.sample(rng)
		direction := mthRandomBetweenF(rng, 0.0, float32(math.Pi))
		wind = windOffsetter{
			originY: origin.Y,
			hasWind: true,
			windX:   float64(mthCos(float64(direction)) * speed),
			windZ:   float64(mthSin(float64(direction)) * speed),
			maxOff:  16 - radius,
		}
	}

	stalactiteEmbedded := bctx.largeDripstoneMoveBack(&stalactite, wind)
	stalagmiteEmbedded := bctx.largeDripstoneMoveBack(&stalagmite, wind)
	if stalactiteEmbedded {
		bctx.largeDripstonePlaceBlocks(&stalactite, ctx, rng, wind)
	}
	if stalagmiteEmbedded {
		bctx.largeDripstonePlaceBlocks(&stalagmite, ctx, rng, wind)
	}
	return true
}

// largeDripstoneSuitableForWind ports LargeDripstone.isSuitableForWind.
func largeDripstoneSuitableForWind(l *largeDripstone, cfg *largeDripstoneConfig) bool {
	return l.radius >= cfg.minRadiusForWind && l.bluntness >= float64(cfg.minBluntnessForWind)
}

// largeDripstoneMoveBack ports LargeDripstone.moveBackUntilBaseIsInsideStoneAndShrinkRadius
// IfNecessary. No rng draws.
func (b *bodyContext) largeDripstoneMoveBack(l *largeDripstone, wind windOffsetter) bool {
	for l.radius > 1 {
		newRoot := l.root
		maxTries := min(10, l.getHeight())
		for i := 0; i < maxTries; i++ {
			if dripstoneIsLava(b.getState(newRoot)) {
				return false
			}
			if b.isCircleMostlyEmbeddedInStone(wind.offset(newRoot), l.radius) {
				l.root = newRoot
				return true
			}
			if l.pointingUp {
				newRoot = placement.BlockPos{X: newRoot.X, Y: newRoot.Y - 1, Z: newRoot.Z}
			} else {
				newRoot = placement.BlockPos{X: newRoot.X, Y: newRoot.Y + 1, Z: newRoot.Z}
			}
		}
		l.radius /= 2
	}
	return false
}

// largeDripstonePlaceBlocks ports LargeDripstone.placeBlocks. Draw order per column cell that
// has a positive height: nextFloat() < 0.2 → randomBetween(0.8,1.0) (an extra draw), else no
// extra draw. Writes dripstone_block through isEmptyOrWaterOrLava cells.
func (b *bodyContext) largeDripstonePlaceBlocks(l *largeDripstone, ctx placement.PlacementContext, rng levelgen.RandomSource, wind windOffsetter) {
	for dx := -l.radius; dx <= l.radius; dx++ {
		for dz := -l.radius; dz <= l.radius; dz++ {
			currentRadius := mthSqrtF(float32(dx*dx + dz*dz))
			if currentRadius > float32(l.radius) {
				continue
			}
			height := l.getHeightAtRadius(currentRadius)
			if height <= 0 {
				continue
			}
			// nextFloat() < 0.2 → height *= randomBetween(0.8,1.0).
			if rng.NextFloat() < 0.2 {
				height = int(float32(height) * mthRandomBetweenF(rng, 0.8, 1.0))
			}
			pos := placement.BlockPos{X: l.root.X + dx, Y: l.root.Y, Z: l.root.Z + dz}
			hasBeenOutOfStone := false
			maxY := math.MaxInt32
			if l.pointingUp {
				maxY = ctx.GetHeight(placement.WorldSurfaceWG, pos.X, pos.Z)
			}
			for i := 0; i < height && pos.Y < maxY; i++ {
				windAdjusted := wind.offset(pos)
				if b.isEmptyOrWaterOrLava(windAdjusted) {
					hasBeenOutOfStone = true
					b.placeState(windAdjusted, dripstoneBlockState)
				} else if hasBeenOutOfStone && dripstoneBaseStoneOverworldSet[b.getState(windAdjusted)] {
					break
				}
				if l.pointingUp {
					pos = placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
				} else {
					pos = placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
				}
			}
		}
	}
}

// ===========================================================================================
//  block-state + tag helpers
// ===========================================================================================

// dripstonePointedState builds a pointed_dripstone state with the given tip direction,
// thickness, and waterlogged flag (createPointedBlock + the growSpeleothem waterlogging).
func dripstonePointedState(dir block.Direction, thickness block.SpeleothemThickness, waterlogged bool) block.StateID {
	return block.ToStateID[block.PointedDripstone{
		Thickness:         thickness,
		VerticalDirection: dir,
		Waterlogged:       block.Boolean(waterlogged),
	}]
}

// dripstoneSameBlock reports whether state `st` is any state of the same block as `other`.
func dripstoneSameBlock(st, other block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) || int(other) < 0 || int(other) >= len(block.StateList) {
		return false
	}
	return block.StateList[st].ID() == block.StateList[other].ID()
}

// dripstoneIsLava reports whether the state is any lava state.
func dripstoneIsLava(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	return block.StateList[st].ID() == "minecraft:lava"
}

// dripstoneIsLavaAt reads the view and tests lava.
func (b *bodyContext) dripstoneIsLavaAt(pos placement.BlockPos) bool { return dripstoneIsLava(b.getState(pos)) }

// dripstoneNextBoolean draws RandomSource.nextBoolean off the decoration rng.
func dripstoneNextBoolean(rng levelgen.RandomSource) bool {
	if bs, ok := rng.(booleanSource); ok {
		return bs.NextBoolean()
	}
	return false
}

// dripstoneRandomDirection ports Direction.getRandom(random) = VALUES[nextInt(6)] over the
// enum order DOWN,UP,NORTH,SOUTH,WEST,EAST.
func dripstoneRandomDirection(rng levelgen.RandomSource) geodeDir {
	return geodeDirections[int(rng.NextIntN(6))]
}

// dripstoneHorizontal is Direction.Plane.HORIZONTAL = [NORTH,EAST,SOUTH,WEST].
var dripstoneHorizontal = [4]geodeDir{
	{d: block.North, dx: 0, dy: 0, dz: -1},
	{d: block.East, dx: 1, dy: 0, dz: 0},
	{d: block.South, dx: 0, dy: 0, dz: 1},
	{d: block.West, dx: -1, dy: 0, dz: 0},
}

// dripstoneReplaceableSet resolves the #dripstone_replaceable_blocks tag (= #base_stone_overworld)
// to its member state set (constant-for-constant, jar tag def). An unknown tag yields nil.
func dripstoneReplaceableSet(tag string) map[block.StateID]bool {
	switch tag {
	case "#minecraft:dripstone_replaceable_blocks":
		return dripstoneBaseStoneOverworldSet
	default:
		return nil
	}
}

// dripstoneBaseStoneOverworldSet is #minecraft:base_stone_overworld flattened (JAR-CONFIRMED,
// 26.2): stone, granite, diorite, andesite, tuff, deepslate. Used by both the
// dripstone_replaceable_blocks tag and the cluster's canBeAdjacentToWater check.
var dripstoneBaseStoneOverworldSet = buildDripstoneStateSet([]string{
	"minecraft:stone", "minecraft:granite", "minecraft:diorite",
	"minecraft:andesite", "minecraft:tuff", "minecraft:deepslate",
})

func buildDripstoneStateSet(ids []string) map[block.StateID]bool {
	want := idSet(ids)
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// cached base states.
var (
	dripstoneWaterState = block.ToStateID[block.Water{Level: 0}]
	dripstoneBlockState = block.ToStateID[block.DripstoneBlock{}]
)

// ===========================================================================================
//  IntProvider / FloatProvider (constant + uniform + clamped + clamped_normal + trapezoid)
// ===========================================================================================

// dripstoneIntProvider is the IntProvider subset the dripstone configs use: constant, uniform,
// clamped (over an inner source). (height/radius/layer_thickness are uniform; column_radius is
// clamped over uniform.)
type dripstoneIntProvider struct {
	kind     int // 0 const, 1 uniform, 2 clamped
	value    int
	min, max int
	source   *dripstoneIntProvider
}

func (p *dripstoneIntProvider) sample(rng levelgen.RandomSource) int {
	switch p.kind {
	case 0:
		return p.value
	case 1:
		// UniformInt.sample = min + nextInt(max-min+1). 1 draw.
		return p.min + int(rng.NextIntN(int32(p.max-p.min+1)))
	case 2:
		// ClampedInt.sample = clamp(source.sample, min, max). source draws.
		v := p.source.sample(rng)
		if v < p.min {
			return p.min
		}
		if v > p.max {
			return p.max
		}
		return v
	}
	return 0
}

// minInclusive / maxInclusive expose the bounds the LargeDripstone place() reads directly off
// the column_radius provider (a clamped provider's bounds are its clamp bounds).
func (p *dripstoneIntProvider) minInclusive() int {
	if p.kind == 0 {
		return p.value
	}
	return p.min
}
func (p *dripstoneIntProvider) maxInclusive() int {
	if p.kind == 0 {
		return p.value
	}
	return p.max
}

func parseDripstoneIntProvider(raw json.RawMessage) (*dripstoneIntProvider, error) {
	if len(raw) == 0 {
		return &dripstoneIntProvider{kind: 0, value: 0}, nil
	}
	if jsonFirstByte(raw) != '{' {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("constant int: %w", err)
		}
		return &dripstoneIntProvider{kind: 0, value: v}, nil
	}
	var obj struct {
		Type         string          `json:"type"`
		Value        *int            `json:"value"`
		MinInclusive int             `json:"min_inclusive"`
		MaxInclusive int             `json:"max_inclusive"`
		Source       json.RawMessage `json:"source"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("int provider: %w", err)
	}
	switch stripNS(obj.Type) {
	case "constant":
		v := 0
		if obj.Value != nil {
			v = *obj.Value
		}
		return &dripstoneIntProvider{kind: 0, value: v}, nil
	case "uniform":
		if obj.MaxInclusive < obj.MinInclusive {
			return nil, fmt.Errorf("uniform int: max %d < min %d", obj.MaxInclusive, obj.MinInclusive)
		}
		return &dripstoneIntProvider{kind: 1, min: obj.MinInclusive, max: obj.MaxInclusive}, nil
	case "clamped":
		src, err := parseDripstoneIntProvider(obj.Source)
		if err != nil {
			return nil, fmt.Errorf("clamped int source: %w", err)
		}
		return &dripstoneIntProvider{kind: 2, min: obj.MinInclusive, max: obj.MaxInclusive, source: src}, nil
	default:
		return nil, fmt.Errorf("world: unported dripstone IntProvider type %q", obj.Type)
	}
}

// dripstoneFloatProvider is the FloatProvider subset the dripstone configs use: constant,
// uniform, clamped_normal, trapezoid.
type dripstoneFloatProvider struct {
	kind            int // 0 const, 1 uniform, 2 clamped_normal, 3 trapezoid
	value           float32
	min, max        float32 // uniform: [min_inclusive, max_exclusive); clamped_normal: clamp bounds; trapezoid: min/max
	mean, deviation float32 // clamped_normal
	plateau         float32 // trapezoid
}

func (p *dripstoneFloatProvider) sample(rng levelgen.RandomSource) float32 {
	switch p.kind {
	case 0:
		return p.value
	case 1:
		// UniformFloat.sample = Mth.randomBetween(rng, min, max) = min + nextFloat()*(max-min).
		return mthRandomBetweenF(rng, p.min, p.max)
	case 2:
		// ClampedNormalFloat.sample = clamp(Mth.normal(rng, mean, dev), min, max).
		return clampedNormalFloatSample(rng, p.mean, p.deviation, p.min, p.max)
	case 3:
		// TrapezoidFloat.sample (JAR-exact):
		//   range = max - min
		//   plateauStart = (range - plateau) / 2
		//   plateauEnd = range - plateauStart
		//   return min + nextFloat()*plateauEnd + nextFloat()*plateauStart   (2 draws, in order)
		rng2 := p.max - p.min
		plateauStart := (rng2 - p.plateau) / 2.0
		plateauEnd := rng2 - plateauStart
		return p.min + rng.NextFloat()*plateauEnd + rng.NextFloat()*plateauStart
	}
	return 0
}

func parseDripstoneFloatProvider(raw json.RawMessage) (*dripstoneFloatProvider, error) {
	if len(raw) == 0 {
		return &dripstoneFloatProvider{kind: 0, value: 0}, nil
	}
	if jsonFirstByte(raw) != '{' {
		var v float32
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("constant float: %w", err)
		}
		return &dripstoneFloatProvider{kind: 0, value: v}, nil
	}
	var obj struct {
		Type         string   `json:"type"`
		Value        *float32 `json:"value"`
		MinInclusive *float32 `json:"min_inclusive"`
		MaxExclusive *float32 `json:"max_exclusive"`
		Mean         *float32 `json:"mean"`
		Deviation    *float32 `json:"deviation"`
		Min          *float32 `json:"min"`
		Max          *float32 `json:"max"`
		Plateau      *float32 `json:"plateau"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("float provider: %w", err)
	}
	f32 := func(p *float32) float32 {
		if p == nil {
			return 0
		}
		return *p
	}
	switch stripNS(obj.Type) {
	case "constant":
		return &dripstoneFloatProvider{kind: 0, value: f32(obj.Value)}, nil
	case "uniform":
		return &dripstoneFloatProvider{kind: 1, min: f32(obj.MinInclusive), max: f32(obj.MaxExclusive)}, nil
	case "clamped_normal":
		return &dripstoneFloatProvider{kind: 2, mean: f32(obj.Mean), deviation: f32(obj.Deviation), min: f32(obj.Min), max: f32(obj.Max)}, nil
	case "trapezoid":
		return &dripstoneFloatProvider{kind: 3, min: f32(obj.Min), max: f32(obj.Max), plateau: f32(obj.Plateau)}, nil
	default:
		return nil, fmt.Errorf("world: unported dripstone FloatProvider type %q", obj.Type)
	}
}

// clampedNormalFloatSample ports ClampedNormalFloat.sample = Mth.clamp(Mth.normal(rng, mean,
// dev), min, max). Mth.normal(rng, mean, dev) = mean + (float)rng.nextGaussian()*dev.
func clampedNormalFloatSample(rng levelgen.RandomSource, mean, deviation, minv, maxv float32) float32 {
	g, ok := rng.(dripstoneGaussianSource)
	if !ok {
		panic("world: clamped_normal FloatProvider sampled with a non-Gaussian RandomSource")
	}
	v := mean + float32(g.NextGaussian())*deviation
	if v < minv {
		return minv
	}
	if v > maxv {
		return maxv
	}
	return v
}

// ===========================================================================================
//  Mth helpers
// ===========================================================================================

// mthRandomBetweenInclusive ports Mth.randomBetweenInclusive(rng, min, max) = min +
// nextInt(max-min+1). 1 draw.
func mthRandomBetweenInclusive(rng levelgen.RandomSource, minv, maxv int) int {
	return minv + int(rng.NextIntN(int32(maxv-minv+1)))
}

// mthRandomBetweenF ports Mth.randomBetween(rng, min, max) = min + nextFloat()*(max-min). 1 draw.
func mthRandomBetweenF(rng levelgen.RandomSource, minv, maxv float32) float32 {
	return minv + rng.NextFloat()*(maxv-minv)
}

// mthClampedMapD ports Mth.clampedMap(double,double,double,double,double) =
// clampedLerp(inverseLerp(v, a, b), c, d).
func mthClampedMapD(v, a, b, c, d float64) float64 {
	return mthClampedLerpD(mthInverseLerpD(v, a, b), c, d)
}

func mthInverseLerpD(v, a, b float64) float64 { return (v - a) / (b - a) }
func mthClampedLerpD(t, a, b float64) float64 {
	if t < 0.0 {
		return a
	}
	if t > 1.0 {
		return b
	}
	return a + t*(b-a)
}

// mthClampedMapF ports Mth.clampedMap(float,float,float,float,float).
func mthClampedMapF(v, a, b, c, d float32) float32 {
	return mthClampedLerpF(mthInverseLerpF(v, a, b), c, d)
}
func mthInverseLerpF(v, a, b float32) float32 { return (v - a) / (b - a) }
func mthClampedLerpF(t, a, b float32) float32 {
	if t < 0.0 {
		return a
	}
	if t > 1.0 {
		return b
	}
	return a + t*(b-a)
}

// mthClampI ports Mth.clamp(int,int,int).
func mthClampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// mthSqrtF ports Mth.sqrt(float) = (float)Math.sqrt((double)f).
func mthSqrtF(f float32) float32 { return float32(math.Sqrt(float64(f))) }

// abs is |x| for ints.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---- Mth sine table (Mth.sin/Mth.cos) ----

// mthSinTable is Mth.SIN: SIN[i] = (float)Math.sin(i / 10430.378350470453) for i in [0,65536).
// Ported so the speleothem embedding-circle scan + the large-dripstone wind offset use the
// vanilla table-lookup values (which differ from exact sin at some angles), keeping the
// integer cell offsets byte-identical to the jar.
var mthSinTable = func() [65536]float32 {
	var t [65536]float32
	for i := 0; i < 65536; i++ {
		t[i] = float32(math.Sin(float64(i) / 10430.378350470453))
	}
	return t
}()

// mthSin ports Mth.sin(double) = SIN[(int)((long)(x*SIN_SCALE) & 0xFFFF)].
func mthSin(x float64) float32 {
	return mthSinTable[int64(x*10430.378350470453)&0xFFFF]
}

// mthCos ports Mth.cos(double) = SIN[(int)((long)(x*SIN_SCALE + 16384.0) & 0xFFFF)].
func mthCos(x float64) float32 {
	return mthSinTable[int64(x*10430.378350470453+16384.0)&0xFFFF]
}
