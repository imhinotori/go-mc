package world

// feature_nether_extra.go ports four more Nether configured-feature bodies 1:1 from the
// unobfuscated Minecraft 26.2 jar (temp/cache/26.2-inner.jar, read via javap -c -p):
//
//   1. basalt_columns -> net.minecraft.world.level.levelgen.feature.BasaltColumnsFeature
//   2. basalt_pillar  -> net.minecraft.world.level.levelgen.feature.BasaltPillarFeature
//   3. delta_feature  -> net.minecraft.world.level.levelgen.feature.DeltaFeature
//   4. huge_fungus    -> net.minecraft.world.level.levelgen.feature.HugeFungusFeature
//
// RNG-DRAW ORDER is the determinism contract and is mirrored EXACTLY from the bytecode.
// Every write goes through bctx.placeState (Neighborhood.SetBlock). Each body registers
// under its namespace-stripped configured-feature type via registerFeatureBody in init().

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("basalt_columns", basaltColumnsBody)
	registerFeatureBody("basalt_pillar", basaltPillarBody)
	registerFeatureBody("delta_feature", deltaFeatureBody)
	registerFeatureBody("huge_fungus", hugeFungusBody)
}

// isLavaBlock reports state.is(Blocks.LAVA) -- the block TYPE is lava, any fluid level.
func isLavaBlock(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.Lava)
	return ok
}

func blockIDInSet(st block.StateID, set map[string]bool) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	return set[block.StateList[st].ID()]
}

func blockStateHasID(st block.StateID, id string) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	return block.StateList[st].ID() == id
}

// ---- BasaltColumnsFeature static predicates ----

var basaltCannotPlaceOnIDs = map[string]bool{
	"minecraft:lava":                true,
	"minecraft:bedrock":             true,
	"minecraft:magma_block":         true,
	"minecraft:soul_sand":           true,
	"minecraft:nether_bricks":       true,
	"minecraft:nether_brick_fence":  true,
	"minecraft:nether_brick_stairs": true,
	"minecraft:nether_wart":         true,
	"minecraft:chest":               true,
	"minecraft:spawner":             true,
}

func basaltCannotPlaceOn(st block.StateID) bool { return blockIDInSet(st, basaltCannotPlaceOnIDs) }

// basaltIsAirOrLavaOcean ports BasaltColumnsFeature.isAirOrLavaOcean(level, seaLevel, pos):
// state.isAir() || (state.is(LAVA) && pos.getY() <= seaLevel).
func basaltIsAirOrLavaOcean(bctx *bodyContext, seaLevel int, pos placement.BlockPos) bool {
	st := bctx.getState(pos)
	if block.IsAir(st) {
		return true
	}
	return isLavaBlock(st) && pos.Y <= seaLevel
}

// basaltCanPlaceAt ports BasaltColumnsFeature.canPlaceAt(level, seaLevel, pos):
// isAirOrLavaOcean(pos) && !state(pos.below()).isAir() && !CANNOT_PLACE_ON.contains(below).
func basaltCanPlaceAt(bctx *bodyContext, seaLevel int, pos placement.BlockPos) bool {
	if !basaltIsAirOrLavaOcean(bctx, seaLevel, pos) {
		return false
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	return !block.IsAir(below) && !basaltCannotPlaceOn(below)
}

// basaltFindSurface ports BasaltColumnsFeature.findSurface(level, seaLevel, pos, dist):
// while pos.getY() > minY+1 && dist>0: dist--; if canPlaceAt(pos) return pos; move DOWN.
func basaltFindSurface(bctx *bodyContext, ctx placement.PlacementContext, seaLevel int, pos placement.BlockPos, dist int) (placement.BlockPos, bool) {
	minY := minBuildY(bctx, ctx)
	for pos.Y > minY+1 && dist > 0 {
		dist--
		if basaltCanPlaceAt(bctx, seaLevel, pos) {
			return pos, true
		}
		pos.Y--
	}
	return placement.BlockPos{}, false
}

// basaltFindAir ports BasaltColumnsFeature.findAir(level, pos, dist):
// while pos.getY() <= maxY && dist>0: dist--; state=state(pos);
// if CANNOT_PLACE_ON.contains(state.block) return null; if state.isAir return pos; move UP.
func basaltFindAir(bctx *bodyContext, ctx placement.PlacementContext, pos placement.BlockPos, dist int) (placement.BlockPos, bool) {
	maxY := maxBuildY(bctx, ctx)
	for pos.Y <= maxY && dist > 0 {
		dist--
		st := bctx.getState(pos)
		if basaltCannotPlaceOn(st) {
			return placement.BlockPos{}, false
		}
		if block.IsAir(st) {
			return pos, true
		}
		pos.Y++
	}
	return placement.BlockPos{}, false
}

// ---- basalt_columns (BasaltColumnsFeature) ----

type jsonColumnConfig struct {
	Height json.RawMessage `json:"height"`
	Reach  json.RawMessage `json:"reach"`
}

type basaltColumnsDecoded struct {
	height feature.IntProvider
	reach  feature.IntProvider
	err    error
}

var basaltColumnsCache sync.Map // map[*feature.ConfiguredFeature]*basaltColumnsDecoded

func decodeBasaltColumns(cf *feature.ConfiguredFeature) *basaltColumnsDecoded {
	if v, ok := basaltColumnsCache.Load(cf); ok {
		return v.(*basaltColumnsDecoded)
	}
	d := &basaltColumnsDecoded{}
	var j jsonColumnConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			d.err = fmt.Errorf("world: basalt_columns config: %w", err)
			basaltColumnsCache.Store(cf, d)
			return d
		}
	}
	h, err := feature.ParseIntProvider(j.Height)
	if err != nil {
		d.err = fmt.Errorf("world: basalt_columns height: %w", err)
		basaltColumnsCache.Store(cf, d)
		return d
	}
	r, err := feature.ParseIntProvider(j.Reach)
	if err != nil {
		d.err = fmt.Errorf("world: basalt_columns reach: %w", err)
		basaltColumnsCache.Store(cf, d)
		return d
	}
	d.height, d.reach = h, r
	basaltColumnsCache.Store(cf, d)
	return d
}

// basaltColumnsBody ports BasaltColumnsFeature.place. Constants: CLUSTERED_REACH=50,
// CLUSTERED_SIZE=5, UNCLUSTERED_REACH=15, UNCLUSTERED_SIZE=8.
func basaltColumnsBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeBasaltColumns(cf)
	if d.err != nil {
		panic(d.err)
	}
	seaLevel := bctx.seaLevelOr(63)
	if !basaltCanPlaceAt(bctx, seaLevel, pos) {
		return false
	}
	height := d.height.Sample(rng)
	clustered := rng.NextFloat() < 0.9 // fcmpg vs float 0.9f
	size := height
	if clustered {
		if size > 5 {
			size = 5
		}
	} else if size > 8 {
		size = 8
	}
	columns := 15
	if clustered {
		columns = 50
	}
	placed := false
	// randomBetweenClosed(rng, columns, x-size, y, z-size, x+size, y, z+size): per iter draws
	// nextInt(width), nextInt(yspan), nextInt(depth) (BlockPos$2).
	width := (pos.X + size) - (pos.X - size) + 1
	yspan := 1
	depth := (pos.Z + size) - (pos.Z - size) + 1
	minX := pos.X - size
	minY := pos.Y
	minZ := pos.Z - size
	for c := 0; c < columns; c++ {
		bx := minX + int(rng.NextIntN(int32(width)))
		by := minY + int(rng.NextIntN(int32(yspan)))
		bz := minZ + int(rng.NextIntN(int32(depth)))
		p := placement.BlockPos{X: bx, Y: by, Z: bz}
		k := height - distManhattan(p, pos)
		if k >= 0 {
			reach := d.reach.Sample(rng)
			if basaltPlaceColumn(bctx, ctx, seaLevel, p, k, reach) {
				placed = true
			}
		}
	}
	return placed
}

// basaltPlaceColumn ports BasaltColumnsFeature.placeColumn. No rng draws. Iterates
// betweenClosed(x-reach,y,z-reach, x+reach,y,z+reach) X-fastest then Y then Z (BlockPos$4).
func basaltPlaceColumn(bctx *bodyContext, ctx placement.PlacementContext, seaLevel int, pos placement.BlockPos, height, reach int) bool {
	placed := false
	for z := pos.Z - reach; z <= pos.Z+reach; z++ {
		for x := pos.X - reach; x <= pos.X+reach; x++ {
			p2 := placement.BlockPos{X: x, Y: pos.Y, Z: z}
			dist := distManhattan(p2, pos)
			var pos2 placement.BlockPos
			var ok bool
			if basaltIsAirOrLavaOcean(bctx, seaLevel, p2) {
				pos2, ok = basaltFindSurface(bctx, ctx, seaLevel, p2, dist)
			} else {
				pos2, ok = basaltFindAir(bctx, ctx, p2, dist)
			}
			if !ok {
				continue
			}
			l := height - dist/2
			for l >= 0 {
				if basaltIsAirOrLavaOcean(bctx, seaLevel, pos2) {
					bctx.placeState(pos2, basaltID)
					pos2.Y++
					placed = true
				} else if bctx.getState(pos2) == basaltID {
					pos2.Y++
				} else {
					break
				}
				l--
			}
		}
	}
	return placed
}

// ---- basalt_pillar (BasaltPillarFeature) ----

// basaltPillarBody ports BasaltPillarFeature.place (NoneFeatureConfiguration).
func basaltPillarBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	// if (!isEmptyBlock(origin) || isEmptyBlock(origin.above())) return false.
	if !block.IsAir(bctx.getState(pos)) || block.IsAir(bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})) {
		return false
	}
	cur := pos
	north, south, west, east := true, true, true, true
	for block.IsAir(bctx.getState(cur)) {
		if outsideBuildHeight(bctx, ctx, cur.Y) {
			return true
		}
		bctx.placeState(cur, basaltID)
		north = north && basaltPlaceHangOff(bctx, rng, placement.BlockPos{X: cur.X, Y: cur.Y, Z: cur.Z - 1})
		south = south && basaltPlaceHangOff(bctx, rng, placement.BlockPos{X: cur.X, Y: cur.Y, Z: cur.Z + 1})
		west = west && basaltPlaceHangOff(bctx, rng, placement.BlockPos{X: cur.X - 1, Y: cur.Y, Z: cur.Z})
		east = east && basaltPlaceHangOff(bctx, rng, placement.BlockPos{X: cur.X + 1, Y: cur.Y, Z: cur.Z})
		cur.Y--
	}
	_, _, _, _ = north, south, west, east
	cur.Y++ // move UP to the last basalt-filled cell
	basaltPlaceBaseHangOff(bctx, rng, placement.BlockPos{X: cur.X, Y: cur.Y, Z: cur.Z - 1})
	basaltPlaceBaseHangOff(bctx, rng, placement.BlockPos{X: cur.X, Y: cur.Y, Z: cur.Z + 1})
	basaltPlaceBaseHangOff(bctx, rng, placement.BlockPos{X: cur.X - 1, Y: cur.Y, Z: cur.Z})
	basaltPlaceBaseHangOff(bctx, rng, placement.BlockPos{X: cur.X + 1, Y: cur.Y, Z: cur.Z})
	cur.Y--
	for i := -3; i < 4; i++ {
		for j := -3; j < 4; j++ {
			k := abs(i) * abs(j)
			if int(rng.NextIntN(10)) < 10-k {
				mp := placement.BlockPos{X: cur.X + i, Y: cur.Y, Z: cur.Z + j}
				l := 3
				for block.IsAir(bctx.getState(placement.BlockPos{X: mp.X, Y: mp.Y - 1, Z: mp.Z})) {
					mp.Y--
					l--
					if l <= 0 {
						break
					}
				}
				if !block.IsAir(bctx.getState(placement.BlockPos{X: mp.X, Y: mp.Y - 1, Z: mp.Z})) {
					bctx.placeState(mp, basaltID)
				}
			}
		}
	}
	return true
}

// basaltPlaceHangOff ports BasaltPillarFeature.placeHangOff: if nextInt(10)!=0 {setBlock; true} else false.
func basaltPlaceHangOff(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos) bool {
	if int(rng.NextIntN(10)) != 0 {
		bctx.placeState(pos, basaltID)
		return true
	}
	return false
}

// basaltPlaceBaseHangOff ports BasaltPillarFeature.placeBaseHangOff: if nextBoolean() setBlock.
func basaltPlaceBaseHangOff(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos) {
	if rng.NextBoolean() {
		bctx.placeState(pos, basaltID)
	}
}

// ---- delta_feature (DeltaFeature) ----

type jsonDeltaConfig struct {
	Contents json.RawMessage `json:"contents"`
	Rim      json.RawMessage `json:"rim"`
	Size     json.RawMessage `json:"size"`
	RimSize  json.RawMessage `json:"rim_size"`
}

type deltaDecoded struct {
	contents   block.StateID
	contentsID string
	rim        block.StateID
	size       feature.IntProvider
	rimSize    feature.IntProvider
	err        error
}

var deltaCache sync.Map // map[*feature.ConfiguredFeature]*deltaDecoded

var deltaCannotReplaceIDs = map[string]bool{
	"minecraft:bedrock":             true,
	"minecraft:nether_bricks":       true,
	"minecraft:nether_brick_fence":  true,
	"minecraft:nether_brick_stairs": true,
	"minecraft:nether_wart":         true,
	"minecraft:chest":               true,
	"minecraft:spawner":             true,
}

func decodeDelta(cf *feature.ConfiguredFeature) *deltaDecoded {
	if v, ok := deltaCache.Load(cf); ok {
		return v.(*deltaDecoded)
	}
	d := &deltaDecoded{}
	var j jsonDeltaConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			d.err = fmt.Errorf("world: delta_feature config: %w", err)
			deltaCache.Store(cf, d)
			return d
		}
	}
	contents, err := feature.ResolveBlockStateJSON(j.Contents)
	if err != nil {
		d.err = fmt.Errorf("world: delta_feature contents: %w", err)
		deltaCache.Store(cf, d)
		return d
	}
	rim, err := feature.ResolveBlockStateJSON(j.Rim)
	if err != nil {
		d.err = fmt.Errorf("world: delta_feature rim: %w", err)
		deltaCache.Store(cf, d)
		return d
	}
	sz, err := feature.ParseIntProvider(j.Size)
	if err != nil {
		d.err = fmt.Errorf("world: delta_feature size: %w", err)
		deltaCache.Store(cf, d)
		return d
	}
	rsz, err := feature.ParseIntProvider(j.RimSize)
	if err != nil {
		d.err = fmt.Errorf("world: delta_feature rim_size: %w", err)
		deltaCache.Store(cf, d)
		return d
	}
	d.contents = contents
	d.contentsID = block.StateList[contents].ID()
	d.rim = rim
	d.size = sz
	d.rimSize = rsz
	deltaCache.Store(cf, d)
	return d
}

// deltaDirections is Direction.values(): DOWN, UP, NORTH, SOUTH, WEST, EAST.
var deltaDirections = []struct {
	dx, dy, dz int
	up         bool
}{
	{0, -1, 0, false},
	{0, 1, 0, true},
	{0, 0, -1, false},
	{0, 0, 1, false},
	{-1, 0, 0, false},
	{1, 0, 0, false},
}

// deltaIsClear ports DeltaFeature.isClear(level, pos, config).
func deltaIsClear(bctx *bodyContext, pos placement.BlockPos, contentsID string) bool {
	st := bctx.getState(pos)
	if contentsID != "" && blockStateHasID(st, contentsID) {
		return false
	}
	if blockIDInSet(st, deltaCannotReplaceIDs) {
		return false
	}
	for _, dir := range deltaDirections {
		air := block.IsAir(bctx.getState(placement.BlockPos{X: pos.X + dir.dx, Y: pos.Y + dir.dy, Z: pos.Z + dir.dz}))
		if (air && !dir.up) || (!air && dir.up) {
			return false
		}
	}
	return true
}

// withinManhattan ports BlockPos.withinManhattan(origin, reachX, reachY, reachZ) (BlockPos$3):
// currentDepth 0..reachX+reachY; x in [-min(reachX,depth), +]; y in [-maxY, +maxY],
// maxY=min(reachY, depth-|x|); z=depth-|x|-|y|; if z<=reachZ yield origin+(x,y,z) and,
// if z!=0, immediately the z-mirrored origin+(x,y,-z).
func withinManhattan(origin placement.BlockPos, reachX, reachY, reachZ int) []placement.BlockPos {
	maxDepth := reachX + reachY
	out := make([]placement.BlockPos, 0)
	for depth := 0; depth <= maxDepth; depth++ {
		maxX := min(reachX, depth)
		for x := -maxX; x <= maxX; x++ {
			maxY := min(reachY, depth-abs(x))
			for y := -maxY; y <= maxY; y++ {
				z := depth - abs(x) - abs(y)
				if z <= reachZ {
					out = append(out, placement.BlockPos{X: origin.X + x, Y: origin.Y + y, Z: origin.Z + z})
					if z != 0 {
						out = append(out, placement.BlockPos{X: origin.X + x, Y: origin.Y + y, Z: origin.Z - z})
					}
				}
			}
		}
	}
	return out
}

// deltaFeatureBody ports DeltaFeature.place. RIM_SPAWN_CHANCE=0.9. Draw order: nextDouble()
// (< 0.9 -> bl); if bl {rimSize.sample; rimSize.sample}; size.sample; size.sample.
func deltaFeatureBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeDelta(cf)
	if d.err != nil {
		panic(d.err)
	}
	placed := false
	bl := rng.NextDouble() < 0.9
	rimX, rimZ := 0, 0
	if bl {
		rimX = d.rimSize.Sample(rng)
		rimZ = d.rimSize.Sample(rng)
	}
	bl2 := bl && rimX != 0 && rimZ != 0
	i := d.size.Sample(rng)
	j := d.size.Sample(rng)
	k := max(i, j)
	for _, p := range withinManhattan(pos, i, 0, j) {
		if distManhattan(p, pos) > k {
			break
		}
		if deltaIsClear(bctx, p, d.contentsID) {
			if bl2 {
				placed = true
				bctx.placeState(p, d.rim)
			}
			p2 := placement.BlockPos{X: p.X + rimX, Y: p.Y, Z: p.Z + rimZ}
			if deltaIsClear(bctx, p2, d.contentsID) {
				placed = true
				bctx.placeState(p2, d.contents)
			}
		}
	}
	return placed
}

// ---- huge_fungus (HugeFungusFeature) ----

type jsonHugeFungusConfig struct {
	ValidBaseBlock    json.RawMessage `json:"valid_base_block"`
	StemState         json.RawMessage `json:"stem_state"`
	HatState          json.RawMessage `json:"hat_state"`
	DecorState        json.RawMessage `json:"decor_state"`
	ReplaceableBlocks json.RawMessage `json:"replaceable_blocks"`
	Planted           bool            `json:"planted"`
}

type hugeFungusDecoded struct {
	validBaseID          string
	stemState            block.StateID
	hatState             block.StateID
	hatIsNetherWartBlock bool
	decorState           block.StateID
	replaceable          placement.BlockPredicate
	planted              bool
	err                  error
}

var hugeFungusCache sync.Map // map[*feature.ConfiguredFeature]*hugeFungusDecoded

var hugeFungusReplaceableIDs = map[string]bool{
	"minecraft:air":                  true,
	"minecraft:cave_air":             true,
	"minecraft:void_air":             true,
	"minecraft:water":                true,
	"minecraft:lava":                 true,
	"minecraft:fire":                 true,
	"minecraft:soul_fire":            true,
	"minecraft:crimson_fungus":       true,
	"minecraft:warped_fungus":        true,
	"minecraft:crimson_roots":        true,
	"minecraft:warped_roots":         true,
	"minecraft:nether_sprouts":       true,
	"minecraft:weeping_vines":        true,
	"minecraft:weeping_vines_plant":  true,
	"minecraft:twisting_vines":       true,
	"minecraft:twisting_vines_plant": true,
}

func blockIDFromStateJSON(raw json.RawMessage) (string, error) {
	var ref struct {
		Name string `json:"Name"`
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("empty block ref")
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", err
	}
	if ref.Name == "" {
		return "", fmt.Errorf("block ref has no Name")
	}
	return ref.Name, nil
}

func decodeHugeFungus(cf *feature.ConfiguredFeature) *hugeFungusDecoded {
	if v, ok := hugeFungusCache.Load(cf); ok {
		return v.(*hugeFungusDecoded)
	}
	d := &hugeFungusDecoded{}
	var j jsonHugeFungusConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			d.err = fmt.Errorf("world: huge_fungus config: %w", err)
			hugeFungusCache.Store(cf, d)
			return d
		}
	}
	baseID, err := blockIDFromStateJSON(j.ValidBaseBlock)
	if err != nil {
		d.err = fmt.Errorf("world: huge_fungus valid_base_block: %w", err)
		hugeFungusCache.Store(cf, d)
		return d
	}
	stem, err := feature.ResolveBlockStateJSON(j.StemState)
	if err != nil {
		d.err = fmt.Errorf("world: huge_fungus stem_state: %w", err)
		hugeFungusCache.Store(cf, d)
		return d
	}
	hat, err := feature.ResolveBlockStateJSON(j.HatState)
	if err != nil {
		d.err = fmt.Errorf("world: huge_fungus hat_state: %w", err)
		hugeFungusCache.Store(cf, d)
		return d
	}
	decor, err := feature.ResolveBlockStateJSON(j.DecorState)
	if err != nil {
		d.err = fmt.Errorf("world: huge_fungus decor_state: %w", err)
		hugeFungusCache.Store(cf, d)
		return d
	}
	pred, err := placement.ParsePredicate(j.ReplaceableBlocks)
	if err != nil {
		d.err = fmt.Errorf("world: huge_fungus replaceable_blocks: %w", err)
		hugeFungusCache.Store(cf, d)
		return d
	}
	d.validBaseID = baseID
	d.stemState = stem
	d.hatState = hat
	d.hatIsNetherWartBlock = blockStateHasID(hat, "minecraft:nether_wart_block")
	d.decorState = decor
	d.replaceable = pred
	d.planted = j.Planted
	hugeFungusCache.Store(cf, d)
	return d
}

func hugeFungusCanBeReplaced(st block.StateID) bool { return blockIDInSet(st, hugeFungusReplaceableIDs) }

func hugeFungusIsReplaceable(bctx *bodyContext, ctx placement.PlacementContext, d *hugeFungusDecoded, pos placement.BlockPos, useConfig bool) bool {
	if hugeFungusCanBeReplaced(bctx.getState(pos)) {
		return true
	}
	if useConfig {
		return d.replaceable.Test(ctx, pos.X, pos.Y, pos.Z)
	}
	return false
}

func mthNextIntRange(rng levelgen.RandomSource, minV, maxV int) int {
	if minV >= maxV {
		return minV
	}
	return int(rng.NextIntN(int32(maxV-minV+1))) + minV
}

func stateBlockID(st block.StateID) string {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return ""
	}
	return block.StateList[st].ID()
}

// hugeFungusBody ports HugeFungusFeature.place. HUGE_PROBABILITY=0.06f. Draw order:
// Mth.nextInt(rng,4,13); nextInt(12)==0 -> height*=2; if !planted nextFloat()<0.06 -> huge.
func hugeFungusBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeHugeFungus(cf)
	if d.err != nil {
		panic(d.err)
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if !blockStateHasID(below, d.validBaseID) {
		return false
	}
	base := pos
	height := mthNextIntRange(rng, 4, 13)
	if int(rng.NextIntN(12)) == 0 {
		height *= 2
	}
	if !d.planted {
		genDepthTop := maxBuildY(bctx, ctx) + 1
		if base.Y+height+1 >= genDepthTop {
			return false
		}
	}
	huge := false
	if !d.planted {
		huge = rng.NextFloat() < 0.06 // fcmpg vs float 0.06f
	}
	bctx.placeState(pos, bctx.airState())
	hugeFungusPlaceStem(bctx, ctx, rng, d, base, height, huge)
	hugeFungusPlaceHat(bctx, ctx, rng, d, base, height, huge)
	return true
}

// hugeFungusPlaceStem ports HugeFungusFeature.placeStem.
func hugeFungusPlaceStem(bctx *bodyContext, ctx placement.PlacementContext, rng levelgen.RandomSource, d *hugeFungusDecoded, pos placement.BlockPos, height int, huge bool) {
	i := 0
	if huge {
		i = 1
	}
	for k := -i; k <= i; k++ {
		for l := -i; l <= i; l++ {
			corner := huge && abs(k) == i && abs(l) == i
			for m := 0; m < height; m++ {
				cursor := placement.BlockPos{X: pos.X + k, Y: pos.Y + m, Z: pos.Z + l}
				if !hugeFungusIsReplaceable(bctx, ctx, d, cursor, true) {
					continue
				}
				if d.planted {
					if !block.IsAir(bctx.getState(placement.BlockPos{X: cursor.X, Y: cursor.Y - 1, Z: cursor.Z})) {
						bctx.placeState(cursor, bctx.airState())
					}
					bctx.placeState(cursor, d.stemState)
				} else if corner {
					if rng.NextFloat() < 0.1 { // fcmpg vs float 0.1f
						bctx.placeState(cursor, d.stemState)
					}
				} else {
					bctx.placeState(cursor, d.stemState)
				}
			}
		}
	}
}

// hugeFungusPlaceHat ports HugeFungusFeature.placeHat.
func hugeFungusPlaceHat(bctx *bodyContext, ctx placement.PlacementContext, rng levelgen.RandomSource, d *hugeFungusDecoded, pos placement.BlockPos, height int, huge bool) {
	bl := d.hatIsNetherWartBlock
	i := min(int(rng.NextIntN(int32(1+height/3)))+5, height)
	j := height - i
	for k := j; k <= height; k++ {
		var bl2 int
		if k < height-int(rng.NextIntN(3)) {
			bl2 = 2
		} else {
			bl2 = 1
		}
		if i > 8 && k < j+4 {
			bl2 = 3
		}
		if huge {
			bl2++
		}
		for l := -bl2; l <= bl2; l++ {
			for m := -bl2; m <= bl2; m++ {
				bl3 := l == -bl2 || l == bl2
				bl4 := m == -bl2 || m == bl2
				bl5 := !bl3 && !bl4 && k != height
				bl6 := bl3 && bl4
				bl7 := k < j+3
				cursor := placement.BlockPos{X: pos.X + l, Y: pos.Y + k, Z: pos.Z + m}
				if !hugeFungusIsReplaceable(bctx, ctx, d, cursor, false) {
					continue
				}
				if d.planted && !block.IsAir(bctx.getState(placement.BlockPos{X: cursor.X, Y: cursor.Y - 1, Z: cursor.Z})) {
					bctx.placeState(cursor, bctx.airState())
				}
				if bl7 {
					if !bl5 {
						hugeFungusPlaceHatDropBlock(bctx, rng, cursor, d.hatState, bl)
					}
				} else if bl5 {
					third := float32(0)
					if bl {
						third = 0.1
					}
					hugeFungusPlaceHatBlock(bctx, rng, d, cursor, 0.1, 0.2, third)
				} else if bl6 {
					third := float32(0)
					if bl {
						third = 0.083
					}
					hugeFungusPlaceHatBlock(bctx, rng, d, cursor, 0.01, 0.7, third)
				} else {
					third := float32(0)
					if bl {
						third = 0.07
					}
					hugeFungusPlaceHatBlock(bctx, rng, d, cursor, 5.0e-4, 0.98, third)
				}
			}
		}
	}
}

// hugeFungusPlaceHatBlock ports HugeFungusFeature.placeHatBlock.
func hugeFungusPlaceHatBlock(bctx *bodyContext, rng levelgen.RandomSource, d *hugeFungusDecoded, pos placement.BlockPos, decorChance, hatChance, weepingChance float32) {
	if rng.NextFloat() < decorChance {
		bctx.placeState(pos, d.decorState)
		return
	}
	if rng.NextFloat() < hatChance {
		bctx.placeState(pos, d.hatState)
		if rng.NextFloat() < weepingChance {
			hugeFungusTryPlaceWeepingVines(bctx, rng, pos)
		}
	}
}

// hugeFungusPlaceHatDropBlock ports HugeFungusFeature.placeHatDropBlock.
func hugeFungusPlaceHatDropBlock(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos, st block.StateID, weeping bool) {
	belowMatches := blockStateHasID(bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}), stateBlockID(st))
	if belowMatches {
		bctx.placeState(pos, st)
		return
	}
	if float64(rng.NextFloat()) < 0.15 {
		bctx.placeState(pos, st)
		if weeping && int(rng.NextIntN(11)) == 0 {
			hugeFungusTryPlaceWeepingVines(bctx, rng, pos)
		}
	}
}

// hugeFungusTryPlaceWeepingVines ports HugeFungusFeature.tryPlaceWeepingVines: blockPos =
// pos.mutable().move(DOWN); if !isEmptyBlock(blockPos) return; i = Mth.nextInt(rng,1,5);
// if nextInt(7)==0 i*=2; WeepingVinesFeature.placeWeepingVinesColumn(level, rng, blockPos, i, 23, 25).
func hugeFungusTryPlaceWeepingVines(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos) {
	col := placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if !block.IsAir(bctx.getState(col)) {
		return
	}
	i := mthNextIntRange(rng, 1, 5)
	if int(rng.NextIntN(7)) == 0 {
		i *= 2
	}
	placeWeepingVinesColumn(bctx, rng, col, i, 23, 25)
}
