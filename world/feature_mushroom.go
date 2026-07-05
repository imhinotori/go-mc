package world

// feature_mushroom.go ports the two huge-mushroom feature bodies 1:1 from the
// unobfuscated Minecraft 26.2 jar (temp/cache/26.2-inner.jar, read via javap -c / CFR):
//
//   - net.minecraft.world.level.levelgen.feature.AbstractHugeMushroomFeature
//       {place, getTreeHeight, isValidPosition, placeTrunk, placeMushroomBlock}
//   - net.minecraft.world.level.levelgen.feature.HugeRedMushroomFeature
//       {makeCap, getTreeRadiusForHeight}
//   - net.minecraft.world.level.levelgen.feature.HugeBrownMushroomFeature
//       {makeCap, getTreeRadiusForHeight}
//   - net.minecraft.world.level.levelgen.feature.configurations.HugeMushroomFeatureConfiguration
//
// Both register under their configured-feature type ("huge_red_mushroom" /
// "huge_brown_mushroom") via registerFeatureBody in init(). Until now these types were
// unregistered → the recordable no-op path (they never generated). The RNG-DRAW ORDER is
// the determinism contract (Pitfall 4): getTreeHeight draws nextInt(3) then nextInt(12);
// the cap + trunk placement take NO further draws (simple_state_provider.getState is 0
// draws). Every write goes through bctx.placeState (Neighborhood.SetBlock — cross-chunk +
// live worldgen heightmaps). The face booleans on each cap/stem block are RECOMPUTED per
// position exactly as makeCap does (setValue over HugeMushroomBlock.{UP,DOWN,N,S,E,W}),
// NOT taken verbatim from the config's baked state.

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
	registerFeatureBody("huge_red_mushroom", hugeRedMushroomBody)
	registerFeatureBody("huge_brown_mushroom", hugeBrownMushroomBody)
}

// ---- HugeMushroomFeatureConfiguration decode ----

// hugeMushroomConfig is the decoded HugeMushroomFeatureConfiguration: the cap + stem
// providers, the foliage radius, and the can_place_on predicate (a block-tag set).
// foliage_radius defaults to 2 (the AbstractHugeMushroomFeature codec default; the red
// config omits it, the brown config sets 3).
type hugeMushroomConfig struct {
	cap           feature.BlockStateProvider
	stem          feature.BlockStateProvider
	foliageRadius int
	canPlaceOn    map[block.StateID]bool // matching_block_tag member states; nil ⇒ never
	// radiusFn is the red/brown getTreeRadiusForHeight, bound by the per-type body
	// wrapper before the shared isValidPosition scan runs.
	radiusFn func(a, b, foliageRadius, height int) int
	err      error
}

// jsonHugeMushroomConfig is the on-disk shape (verified huge_red_mushroom.json /
// huge_brown_mushroom.json).
type jsonHugeMushroomConfig struct {
	CapProvider   json.RawMessage `json:"cap_provider"`
	StemProvider  json.RawMessage `json:"stem_provider"`
	FoliageRadius *int            `json:"foliage_radius"`
	CanPlaceOn    json.RawMessage `json:"can_place_on"`
}

// hugeMushroomCache memoizes the decoded config per ConfiguredFeature (oreConfigCache
// pattern): the can_place_on tag resolution scans all block states, far too costly to
// repeat per placement. Keyed by the stable *ConfiguredFeature pointer (parser DAG dedup).
var hugeMushroomCache sync.Map // map[*feature.ConfiguredFeature]*hugeMushroomConfig

func decodeHugeMushroomCached(cf *feature.ConfiguredFeature) *hugeMushroomConfig {
	if v, ok := hugeMushroomCache.Load(cf); ok {
		return v.(*hugeMushroomConfig)
	}
	d := &hugeMushroomConfig{foliageRadius: 2}
	var j jsonHugeMushroomConfig
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: huge_mushroom config %q: %w", cf.ID, err)
		hugeMushroomCache.Store(cf, d)
		return d
	}
	cap, err := feature.ParseProvider(j.CapProvider)
	if err != nil {
		d.err = fmt.Errorf("world: huge_mushroom cap_provider %q: %w", cf.ID, err)
		hugeMushroomCache.Store(cf, d)
		return d
	}
	stem, err := feature.ParseProvider(j.StemProvider)
	if err != nil {
		d.err = fmt.Errorf("world: huge_mushroom stem_provider %q: %w", cf.ID, err)
		hugeMushroomCache.Store(cf, d)
		return d
	}
	d.cap = cap
	d.stem = stem
	if j.FoliageRadius != nil {
		d.foliageRadius = *j.FoliageRadius
	}
	// can_place_on is a matching_block_tag BlockPredicate. Resolve the tag to its member
	// state set constant-for-constant (jar tag defs — carver/ore precedent). Only the
	// tag form appears in the two vanilla configs; a richer predicate errors loudly.
	set, err := hugeMushroomCanPlaceOnSet(j.CanPlaceOn)
	if err != nil {
		d.err = err
		hugeMushroomCache.Store(cf, d)
		return d
	}
	d.canPlaceOn = set
	hugeMushroomCache.Store(cf, d)
	return d
}

// jsonMushroomPredicate is the can_place_on BlockPredicate envelope (matching_block_tag).
type jsonMushroomPredicate struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// hugeMushroomCanPlaceOnSet resolves the can_place_on matching_block_tag to a member-state
// set. Both vanilla configs use #minecraft:huge_red/brown_mushroom_can_place_on (identical
// membership). The tag is flattened constant-for-constant from the jar tag def.
func hugeMushroomCanPlaceOnSet(raw json.RawMessage) (map[block.StateID]bool, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("world: huge_mushroom missing can_place_on")
	}
	var j jsonMushroomPredicate
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("world: huge_mushroom can_place_on: %w", err)
	}
	if stripNS(j.Type) != "matching_block_tag" {
		return nil, fmt.Errorf("world: unported huge_mushroom can_place_on predicate %q "+
			"(only matching_block_tag appears in the vanilla configs)", j.Type)
	}
	ids, ok := mushroomCanPlaceOnTagIDs[j.Tag]
	if !ok {
		return nil, fmt.Errorf("world: unported huge_mushroom can_place_on tag %q "+
			"(add it constant-for-constant from the jar tag def)", j.Tag)
	}
	set := map[block.StateID]bool{}
	idset := idSet(ids)
	for sid, b := range block.StateList {
		if idset[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set, nil
}

// mushroomCanPlaceOnTagIDs maps the huge-mushroom can_place_on tag ids to their flattened
// member block ids (JAR-CONFIRMED, data/minecraft/tags/block/*.json, 26.2; nested tags
// #substrate_overworld → #dirt + #mud + #moss_blocks + #grass_blocks expanded):
//
//	#huge_{red,brown}_mushroom_can_place_on =
//	    #substrate_overworld + mycelium + podzol + crimson_nylium + warped_nylium
//	#substrate_overworld = #dirt + #mud + #moss_blocks + #grass_blocks
//	#dirt = dirt + coarse_dirt + rooted_dirt   (per moss_replaceable expansion)
//	#mud = mud + muddy_mangrove_roots
//	#moss_blocks = moss_block + pale_moss_block
//	#grass_blocks = grass_block + podzol + mycelium
//
// Both red and brown reference the same membership.
// NOTE: a matching_block_tag predicate's `tag` field carries the bare tag id (no leading
// '#'), unlike a HolderSet tag reference (e.g. the geode cannot_replace) which is '#'-prefixed.
var mushroomCanPlaceOnTagIDs = map[string][]string{
	"minecraft:huge_red_mushroom_can_place_on":   substrateOverworldPlusNylium,
	"minecraft:huge_brown_mushroom_can_place_on": substrateOverworldPlusNylium,
}

// substrateOverworldPlusNylium is the flattened can_place_on membership.
var substrateOverworldPlusNylium = []string{
	// #substrate_overworld → #dirt
	"minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
	// #mud
	"minecraft:mud", "minecraft:muddy_mangrove_roots",
	// #moss_blocks
	"minecraft:moss_block", "minecraft:pale_moss_block",
	// #grass_blocks
	"minecraft:grass_block", "minecraft:podzol", "minecraft:mycelium",
	// direct members
	"minecraft:crimson_nylium", "minecraft:warped_nylium",
}

// ---- shared mushroom body (AbstractHugeMushroomFeature) ----

// hugeMushroomBody is the shared AbstractHugeMushroomFeature.place: draw the height,
// validate the position, make the cap (red/brown geometry via capFn), then the trunk.
// Returns true unconditionally when the position validates (matching the bytecode which
// returns iconst_1 after makeCap+placeTrunk), false if the position is invalid.
func hugeMushroomBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
	capFn func(b *bodyContext, cfg *hugeMushroomConfig, rng levelgen.RandomSource, origin placement.BlockPos, height int),
) bool {
	cfg := decodeHugeMushroomCached(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}
	// getTreeHeight: h = nextInt(3) + 4; if nextInt(12) == 0 → h *= 2. (2 draws, in order.)
	height := int(rng.NextIntN(3)) + 4
	if rng.NextIntN(12) == 0 {
		height *= 2
	}
	if !bctx.mushroomValidPosition(cfg, pos, height) {
		return false
	}
	capFn(bctx, cfg, rng, pos, height)
	bctx.mushroomPlaceTrunk(cfg, rng, pos, height)
	return true
}

// mushroomValidPosition ports AbstractHugeMushroomFeature.isValidPosition: the origin must
// be within [minY+1, maxY-height-1], the block below must satisfy can_place_on, and every
// cell in the cap's foliage footprint (per getTreeRadiusForHeight over the column) must be
// air or leaves. No rng draws. capRadiusFn is the red/brown getTreeRadiusForHeight.
func (b *bodyContext) mushroomValidPosition(cfg *hugeMushroomConfig, origin placement.BlockPos, height int) bool {
	y := origin.Y
	if y < b.minY()+1 || y+height+1 > b.maxY() {
		return false
	}
	// can_place_on.test(level, origin.below())
	below := b.getState(placement.BlockPos{X: origin.X, Y: origin.Y - 1, Z: origin.Z})
	if cfg.canPlaceOn == nil || !cfg.canPlaceOn[below] {
		return false
	}
	// Cap footprint scan: for j in [0..height], radius = getTreeRadiusForHeight(-1,-1,
	// foliageRadius, j); for dx,dz in [-radius..radius]: the cell at origin+(dx,j,dz)
	// must be air or a leaf.
	for j := 0; j <= height; j++ {
		radius := cfg.getTreeRadiusForHeight(-1, -1, cfg.foliageRadius, j)
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				st := b.getState(placement.BlockPos{X: origin.X + dx, Y: origin.Y + j, Z: origin.Z + dz})
				if !block.IsAir(st) && !leavesTagSet[st] {
					return false
				}
			}
		}
	}
	return true
}

// mushroomPlaceTrunk ports AbstractHugeMushroomFeature.placeTrunk: place a stem block at
// origin+UP*i for i in [0..height). stemProvider.getState is 0 draws (simple provider).
// The stem's face booleans come from the config's baked state (placeTrunk does NOT
// recompute them — only makeCap does), so the provider state is written verbatim.
func (b *bodyContext) mushroomPlaceTrunk(cfg *hugeMushroomConfig, rng levelgen.RandomSource, origin placement.BlockPos, height int) {
	for i := 0; i < height; i++ {
		p := placement.BlockPos{X: origin.X, Y: origin.Y + i, Z: origin.Z}
		st := cfg.stem.GetState(rng, p.X, p.Y, p.Z)
		b.mushroomPlaceBlock(p, st)
	}
}

// mushroomPlaceBlock ports AbstractHugeMushroomFeature.placeMushroomBlock: write `st` at
// pos only when the existing block is air OR in #minecraft:replaceable_by_mushrooms.
func (b *bodyContext) mushroomPlaceBlock(pos placement.BlockPos, st block.StateID) {
	existing := b.getState(pos)
	if !block.IsAir(existing) && !replaceableByMushroomsSet[existing] {
		return
	}
	b.placeState(pos, st)
}

// getTreeRadiusForHeight dispatches to the red/brown geometry stored on the config. It is
// set by the body's registration wrapper (redRadiusForHeight / brownRadiusForHeight).
func (cfg *hugeMushroomConfig) getTreeRadiusForHeight(a, b, foliageRadius, j int) int {
	return cfg.radiusFn(a, b, foliageRadius, j)
}

// ---- HugeRedMushroomFeature ----

// hugeRedMushroomBody wires the shared body with the RED makeCap + getTreeRadiusForHeight.
func hugeRedMushroomBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	// Bind the radius fn onto the (cached) config so isValidPosition uses the red geometry.
	cfg := decodeHugeMushroomCached(cf)
	if cfg.err == nil {
		cfg.radiusFn = redRadiusForHeight
	}
	return hugeMushroomBody(bctx, cf, ctx, rng, pos, redMakeCap)
}

// redRadiusForHeight ports HugeRedMushroomFeature.getTreeRadiusForHeight:
//
//	int r = 0;
//	if (height < treeHeight && height >= treeHeight - 3) r = foliageRadius;
//	else if (height == treeHeight) r = foliageRadius;
//	return r;
//
// (Params: a=-1 treeHeight-ignored? No — the signature is (int a0, int a1, int foliageRadius,
// int height) but the RED impl reads (a1=treeHeight, foliageRadius, height=a3). CFR/bytecode
// index mapping: iload_2 = treeHeight, iload_3 = foliageRadius, iload 4 = height.)
func redRadiusForHeight(_ int, treeHeight, foliageRadius, height int) int {
	r := 0
	if height < treeHeight && height >= treeHeight-3 {
		r = foliageRadius
	} else if height == treeHeight {
		r = foliageRadius
	}
	return r
}

// redMakeCap ports HugeRedMushroomFeature.makeCap: the domed cap over the top 4 layers.
// For j in [treeHeight-3 .. treeHeight]: radius k = (j < treeHeight ? foliageRadius :
// foliageRadius-1); inner l = foliageRadius-2. Skip cell when j < treeHeight AND
// edgeX == edgeZ (bytecode offsets 162/169: place iff j >= treeHeight OR edgeX != edgeZ —
// this carves the domed silhouette, leaving the flat interior of the lower layers empty and
// the corners of those layers empty too). Each placed cap block RECOMPUTES its
// {up,west,east,north,south} booleans from dx/dz/j (down stays false). No rng draws.
func redMakeCap(b *bodyContext, cfg *hugeMushroomConfig, rng levelgen.RandomSource, origin placement.BlockPos, treeHeight int) {
	foliageRadius := cfg.foliageRadius
	for j := treeHeight - 3; j <= treeHeight; j++ {
		var k int
		if j < treeHeight {
			k = foliageRadius
		} else {
			k = foliageRadius - 1
		}
		inner := foliageRadius - 2
		for dx := -k; dx <= k; dx++ {
			for dz := -k; dz <= k; dz++ {
				edgeX := dx == -k || dx == k
				edgeZ := dz == -k || dz == k
				// place iff j >= treeHeight OR edgeX != edgeZ (skip when j<treeHeight &&
				// edgeX == edgeZ).
				if j < treeHeight && edgeX == edgeZ {
					continue
				}
				p := placement.BlockPos{X: origin.X + dx, Y: origin.Y + j, Z: origin.Z + dz}
				base := cfg.cap.GetState(rng, origin.X, origin.Y, origin.Z)
				st, ok := recomputeRedMushroomCap(base, dx, dz, j, treeHeight, inner)
				if !ok {
					// Provider is not a red_mushroom_block (no faces to set) — place verbatim.
					b.mushroomPlaceBlock(p, base)
					continue
				}
				b.mushroomPlaceBlock(p, st)
			}
		}
	}
}

// recomputeRedMushroomCap ports the per-cell setValue chain in HugeRedMushroomFeature.makeCap
// (bytecode offsets 260..386):
//
//	up    = (j >= treeHeight - 1)
//	west  = (dx <  -inner)
//	east  = (dx >   inner)
//	north = (dz <  -inner)
//	south = (dz >   inner)
//
// where inner = foliageRadius - 2. The red makeCap sets UP + the 4 horizontal faces but does
// NOT set DOWN, so down is PRESERVED from the provider (red config: down=false). Returns the
// recomputed state + true iff `base` is a red_mushroom_block; else place `base` unchanged.
func recomputeRedMushroomCap(base block.StateID, dx, dz, j, treeHeight, inner int) (block.StateID, bool) {
	if int(base) < 0 || int(base) >= len(block.StateList) {
		return base, false
	}
	baseBlk, ok := block.StateList[base].(block.RedMushroomBlock)
	if !ok {
		return base, false
	}
	st := block.RedMushroomBlock{
		Down:  baseBlk.Down, // preserved (red makeCap never sets DOWN)
		Up:    block.Boolean(j >= treeHeight-1),
		West:  block.Boolean(dx < -inner),
		East:  block.Boolean(dx > inner),
		North: block.Boolean(dz < -inner),
		South: block.Boolean(dz > inner),
	}
	return block.ToStateID[st], true
}

// ---- HugeBrownMushroomFeature ----

// hugeBrownMushroomBody wires the shared body with the BROWN makeCap + getTreeRadiusForHeight.
func hugeBrownMushroomBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg := decodeHugeMushroomCached(cf)
	if cfg.err == nil {
		cfg.radiusFn = brownRadiusForHeight
	}
	return hugeMushroomBody(bctx, cf, ctx, rng, pos, brownMakeCap)
}

// brownRadiusForHeight ports HugeBrownMushroomFeature.getTreeRadiusForHeight:
//
//	return (height <= 3) ? 0 : foliageRadius;
//
// (bytecode: iload 4 = height, iconst_3, if_icmpgt → iload_3 = foliageRadius else 0.)
func brownRadiusForHeight(_ int, _ int, foliageRadius, height int) int {
	if height <= 3 {
		return 0
	}
	return foliageRadius
}

// brownMakeCap ports HugeBrownMushroomFeature.makeCap: the FLAT cap layer at j = treeHeight.
// For dx,dz in [-foliageRadius..foliageRadius]: skip the 4 corners; recompute the block's
// {west,east,north,south,up} booleans from the flat-plate edge geometry; place. No rng draws.
func brownMakeCap(b *bodyContext, cfg *hugeMushroomConfig, rng levelgen.RandomSource, origin placement.BlockPos, treeHeight int) {
	radius := cfg.foliageRadius
	j := treeHeight
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			westEdge := dx == -radius
			eastEdge := dx == radius
			northEdge := dz == -radius
			southEdge := dz == radius
			cornerX := westEdge || eastEdge
			cornerZ := northEdge || southEdge
			// Skip the 4 corners (a plus-shaped plate: cornerX && cornerZ ⇒ skip).
			if cornerX && cornerZ {
				continue
			}
			p := placement.BlockPos{X: origin.X + dx, Y: origin.Y + j, Z: origin.Z + dz}
			base := cfg.cap.GetState(rng, origin.X, origin.Y, origin.Z)
			st, ok := recomputeBrownMushroomCap(base, dx, dz, radius, westEdge, eastEdge, northEdge, southEdge)
			if !ok {
				b.mushroomPlaceBlock(p, base)
				continue
			}
			b.mushroomPlaceBlock(p, st)
		}
	}
}

// recomputeBrownMushroomCap ports the per-cell setValue chain in HugeBrownMushroomFeature.
// makeCap (bytecode istore 16..19). With cornerX = westEdge||eastEdge, cornerZ =
// northEdge||southEdge, radius R:
//
//	setWest  = westEdge  || (cornerZ && dx == 1 - R)
//	setEast  = eastEdge  || (cornerZ && dx == R - 1)
//	setNorth = northEdge || (cornerX && dz == 1 - R)
//	setSouth = southEdge || (cornerX && dz == R - 1)
//
// The brown makeCap sets ONLY the 4 horizontal faces (it does NOT set UP or DOWN), so
// up/down are PRESERVED from the provider's baked state (brown config: up=true, down=false).
// Returns the recomputed state + true iff base is a brown_mushroom_block; else place base.
func recomputeBrownMushroomCap(base block.StateID, dx, dz, radius int, westEdge, eastEdge, northEdge, southEdge bool) (block.StateID, bool) {
	baseBlk, ok := block.StateList[base].(block.BrownMushroomBlock)
	if int(base) < 0 || int(base) >= len(block.StateList) || !ok {
		return base, false
	}
	cornerX := westEdge || eastEdge
	cornerZ := northEdge || southEdge
	setWest := westEdge || (cornerZ && dx == 1-radius)
	setEast := eastEdge || (cornerZ && dx == radius-1)
	setNorth := northEdge || (cornerX && dz == 1-radius)
	setSouth := southEdge || (cornerX && dz == radius-1)
	st := block.BrownMushroomBlock{
		Down:  baseBlk.Down, // preserved (brown makeCap never sets DOWN)
		Up:    baseBlk.Up,   // preserved (brown makeCap never sets UP)
		West:  block.Boolean(setWest),
		East:  block.Boolean(setEast),
		North: block.Boolean(setNorth),
		South: block.Boolean(setSouth),
	}
	return block.ToStateID[st], true
}

// ---- maxY + tag sets ----

// mushroomMaxY is the exclusive world ceiling (WorldGenLevel.getMaxY() = minY + height)
// the isValidPosition top-bound check compares against.
func (b *bodyContext) maxY() int {
	if b.view == nil {
		return 320
	}
	return b.view.minY + b.view.height
}

// leavesTagSet is #minecraft:leaves flattened (isValidPosition's air-or-leaf gate),
// JAR-CONFIRMED (data/minecraft/tags/block/leaves.json, 26.2).
var leavesTagSet = buildMushroomStateSet([]string{
	"minecraft:jungle_leaves", "minecraft:oak_leaves", "minecraft:spruce_leaves",
	"minecraft:pale_oak_leaves", "minecraft:dark_oak_leaves", "minecraft:acacia_leaves",
	"minecraft:birch_leaves", "minecraft:azalea_leaves", "minecraft:flowering_azalea_leaves",
	"minecraft:mangrove_leaves", "minecraft:cherry_leaves",
})

// replaceableByMushroomsSet is #minecraft:replaceable_by_mushrooms flattened (the
// placeMushroomBlock replaceable gate), JAR-CONFIRMED (data/minecraft/tags/block/
// replaceable_by_mushrooms.json, 26.2; nested #leaves + #small_flowers expanded).
var replaceableByMushroomsSet = buildMushroomStateSet(replaceableByMushroomsIDs())

// replaceableByMushroomsIDs assembles the flattened member list.
func replaceableByMushroomsIDs() []string {
	ids := []string{
		// #leaves
		"minecraft:jungle_leaves", "minecraft:oak_leaves", "minecraft:spruce_leaves",
		"minecraft:pale_oak_leaves", "minecraft:dark_oak_leaves", "minecraft:acacia_leaves",
		"minecraft:birch_leaves", "minecraft:azalea_leaves", "minecraft:flowering_azalea_leaves",
		"minecraft:mangrove_leaves", "minecraft:cherry_leaves",
		// #small_flowers
		"minecraft:dandelion", "minecraft:open_eyeblossom", "minecraft:poppy",
		"minecraft:blue_orchid", "minecraft:allium", "minecraft:azure_bluet",
		"minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip",
		"minecraft:pink_tulip", "minecraft:oxeye_daisy", "minecraft:cornflower",
		"minecraft:lily_of_the_valley", "minecraft:wither_rose", "minecraft:torchflower",
		"minecraft:closed_eyeblossom", "minecraft:golden_dandelion",
		// direct members
		"minecraft:pale_moss_carpet", "minecraft:short_grass", "minecraft:fern",
		"minecraft:dead_bush", "minecraft:vine", "minecraft:glow_lichen",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:tall_grass", "minecraft:large_fern", "minecraft:hanging_roots",
		"minecraft:pitcher_plant", "minecraft:water", "minecraft:seagrass",
		"minecraft:tall_seagrass", "minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:brown_mushroom_block", "minecraft:red_mushroom_block",
		"minecraft:warped_roots", "minecraft:nether_sprouts", "minecraft:crimson_roots",
		"minecraft:leaf_litter", "minecraft:short_dry_grass", "minecraft:tall_dry_grass",
		"minecraft:bush", "minecraft:firefly_bush",
	}
	return ids
}

// buildMushroomStateSet builds a StateID presence set from a block-id slice (all states of
// each named block). Unknown ids are skipped (a jar bump may add a member this port lacks;
// missing a member only makes the gate marginally stricter, never crashes).
func buildMushroomStateSet(ids []string) map[block.StateID]bool {
	want := idSet(ids)
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}
