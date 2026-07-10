package world

// feature_misc_extra.go ports three more configured-feature bodies, 1:1 with Minecraft Java
// 26.2 (verified via javap -c against temp/cache/26.2-inner.jar):
//
//   - net.minecraft.world.level.levelgen.feature.NoOpFeature.place
//   - net.minecraft.world.level.levelgen.feature.BonusChestFeature.place
//   - net.minecraft.world.level.levelgen.feature.TemplateFeature.place + getRotatedOffset
//     + net.minecraft.world.level.levelgen.feature.configurations.TemplateFeatureConfiguration
//
// RNG DRAW ORDER is the determinism contract; every nextInt/nextLong/shuffle is reproduced in
// the exact jar order.

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/structure"
)

func init() {
	registerFeatureBody("no_op", noOpBody)
	registerFeatureBody("bonus_chest", bonusChestBody)
	registerFeatureBody("template", templateBody)
}

// ---- no_op (NoOpFeature) ----

// noOpBody ports NoOpFeature.place: the entire method body is a bare return of true (bytecode:
// iconst_1 / ireturn). It places NO blocks and draws NO rng. It MUST be registered (not left
// to the unregistered no-op fallback) so a roster entry of type minecraft:no_op is a real,
// intentional success rather than a silently-skipped unknown type.
//
// NOTE: the task brief said return false; the 26.2 bytecode returns TRUE. The 1:1 mandate is
// absolute, so this follows the jar (return true). Observable effect is identical either way
// (no blocks placed); the return only feeds ConfiguredFeature.place placed-something bool.
func noOpBody(
	_ *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	_ levelgen.RandomSource,
	_ placement.BlockPos,
) bool {
	return true
}

// ---- bonus_chest (BonusChestFeature) ----

// bonusChestLootTable is BuiltInLootTables.SPAWN_BONUS_CHEST as a resource id. This is the loot
// table the placed chest block entity references; it is rolled LAZILY on first open
// (server/chest_loot.go), so recording the ref here changes NO block placement -- it only sets
// the chest BE LootTable NBT, exactly as RandomizableContainer.setBlockEntityLootTable does.
const bonusChestLootTable = "minecraft:chests/spawn_bonus_chest"

// bonusChestBody ports BonusChestFeature.place (javap -c):
//
//	chunkPos = ChunkPos.containing(origin)
//	xs = Util.toShuffledList(IntStream.rangeClosed(minBlockX, maxBlockX), rng)   // shuffle draws
//	zs = Util.toShuffledList(IntStream.rangeClosed(minBlockZ, maxBlockZ), rng)   // shuffle draws
//	for x in xs:
//	    for z in zs:
//	        top = getHeightmapPos(MOTION_BLOCKING_NO_LEAVES, (x,0,z))
//	        if isEmptyBlock(top) || getBlockState(top).getCollisionShape(...).isEmpty():
//	            setBlock(top, CHEST.defaultBlockState(), flag 2)
//	            setBlockEntityLootTable(level, rng, top, SPAWN_BONUS_CHEST)  // nextLong()
//	            for dir in Direction.Plane.HORIZONTAL:      // ordinal order [N,E,S,W]
//	                rel = top.relative(dir)
//	                if TORCH.canSurvive(level, rel):  setBlock(rel, TORCH.default, flag 2)
//	            return true
//	return false
//
// Util.toShuffledList over an IntStream collects the range into an IntArrayList then Util.shuffle
// (Fisher-Yates from the end, swap(i, nextInt(i+1))). The x range is shuffled first, then z.
//
// STUB NOTES (documented, no faked placement):
//   - collisionShape.isEmpty(): the worldgen block model has no VoxelShape subsystem, so the
//     SECONDARY placement gate (a non-air block whose collision shape is empty) is not evaluable.
//     We use isEmptyBlock (air) only -- the conservative-read convention used elsewhere
//     (block_pile.mayPlaceOn isFaceSturdy). No draw sits inside that gate, so determinism holds.
//   - canSurvive for the torch is ported as TorchBlock.canSurvive = canSupportCenter(below, UP)
//     (block.IsFaceSturdy(belowState, UP, SupportCenter)) -- the exact vanilla rule, no draw.
func bonusChestBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	if bctx.view == nil {
		return false
	}
	chunkMinX := (origin.X >> 4) << 4
	chunkMinZ := (origin.Z >> 4) << 4
	xs := bonusShuffledRange(chunkMinX, chunkMinX+15, rng)
	zs := bonusShuffledRange(chunkMinZ, chunkMinZ+15, rng)

	for _, x := range xs {
		for _, z := range zs {
			topY := bctx.view.HeightmapMBNL(x, z)
			top := placement.BlockPos{X: x, Y: topY, Z: z}
			if !block.IsAir(bctx.getState(top)) {
				continue
			}
			bctx.placeState(top, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle, Waterlogged: false}])
			lootSeed := rng.NextLong()
			bctx.view.SetBlockEntity(top.X, top.Y, top.Z, block.EntityTypes["minecraft:chest"], bonusChestLootTable, lootSeed)

			torch := block.ToStateID[block.Torch{}]
			for _, d := range coralHorizontals {
				rel := placement.BlockPos{X: top.X + d.dx, Y: top.Y, Z: top.Z + d.dz}
				if bonusTorchCanSurvive(bctx, rel) {
					bctx.placeState(rel, torch)
				}
			}
			return true
		}
	}
	return false
}

// bonusShuffledRange ports Util.toShuffledList(IntStream.rangeClosed(lo, hi), rng): materialize
// [lo..hi] in order, then Util.shuffle (Fisher-Yates from the end). One draw per i in
// [size-1 .. 1].
func bonusShuffledRange(lo, hi int, rng levelgen.RandomSource) []int {
	n := hi - lo + 1
	out := make([]int, n)
	for i := 0; i < n; i++ {
		out[i] = lo + i
	}
	for i := n - 1; i > 0; i-- {
		j := int(rng.NextIntN(int32(i + 1)))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// bonusTorchCanSurvive ports TorchBlock/BaseTorchBlock.canSurvive =
// canSupportCenter(level, pos.below(), UP): the below block presents a sturdy CENTER support on
// its UP face. No rng draw.
func bonusTorchCanSurvive(bctx *bodyContext, pos placement.BlockPos) bool {
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	return block.IsFaceSturdy(below, block.Up, block.SupportCenter)
}

// ---- template (TemplateFeature + TemplateFeatureConfiguration) ----

// templateEntry is one TemplateFeatureConfiguration.TemplateEntry: the template id (JSON key
// "id"), its allowed rotations (JSON key "rotations", default all four), and the WeightedList
// weight of the wrapping Weighted<TemplateEntry> (JSON key "weight", default 1).
type templateEntry struct {
	id        string
	rotations []structure.Rotation
	weight    int
}

// templateConfigRaw mirrors the on-disk TemplateFeatureConfiguration JSON: a "templates"
// weighted list of {"data": {"id","rotations"?}, "weight"}.
type templateConfigRaw struct {
	Templates []struct {
		Data struct {
			ID        string   `json:"id"`
			Rotations []string `json:"rotations"`
		} `json:"data"`
		Weight int `json:"weight"`
	} `json:"templates"`
}

// templateDecoded memoizes the parsed config + loaded templates per ConfiguredFeature.
type templateDecoded struct {
	entries     []templateEntry
	templates   []*structure.StructureTemplate // parallel to entries
	totalWeight int
	err         error
}

var templateCache sync.Map // map[*feature.ConfiguredFeature]*templateDecoded

// templateRotationByName parses the JSON rotation enum names to structure.Rotation. The names
// match net.minecraft.world.level.block.Rotation SerializedName codec keys.
var templateRotationByName = map[string]structure.Rotation{
	"none":                structure.RotNone,
	"clockwise_90":        structure.RotClockwise90,
	"180":                 structure.RotClockwise180,
	"counterclockwise_90": structure.RotCounterclockwise90,
}

// templateAllRotations is Rotation.values() order [NONE, CW90, CW180, CCW90] -- the default the
// rotations optionalFieldOf uses when the JSON omits "rotations".
var templateAllRotations = []structure.Rotation{
	structure.RotNone,
	structure.RotClockwise90,
	structure.RotClockwise180,
	structure.RotCounterclockwise90,
}

func decodeTemplateCached(cf *feature.ConfiguredFeature) *templateDecoded {
	if v, ok := templateCache.Load(cf); ok {
		return v.(*templateDecoded)
	}
	d := &templateDecoded{}
	var raw templateConfigRaw
	if err := json.Unmarshal(configRaw(cf), &raw); err != nil {
		d.err = fmt.Errorf("world: template config %q: %w", cf.ID, err)
		templateCache.Store(cf, d)
		return d
	}
	for _, t := range raw.Templates {
		w := t.Weight
		if w <= 0 {
			// Weighted.codec defaults weight to 1 when absent.
			w = 1
		}
		rots := templateAllRotations
		if len(t.Data.Rotations) > 0 {
			rots = make([]structure.Rotation, 0, len(t.Data.Rotations))
			for _, name := range t.Data.Rotations {
				r, ok := templateRotationByName[name]
				if !ok {
					d.err = fmt.Errorf("world: template %q unknown rotation %q", cf.ID, name)
					templateCache.Store(cf, d)
					return d
				}
				rots = append(rots, r)
			}
		}
		tpl, err := structure.LoadTemplate(t.Data.ID)
		if err != nil {
			d.err = fmt.Errorf("world: template %q load %q: %w", cf.ID, t.Data.ID, err)
			templateCache.Store(cf, d)
			return d
		}
		d.entries = append(d.entries, templateEntry{id: t.Data.ID, rotations: rots, weight: w})
		d.templates = append(d.templates, tpl)
		d.totalWeight += w
	}
	if len(d.entries) == 0 {
		d.err = fmt.Errorf("world: template config %q has no templates", cf.ID)
	}
	templateCache.Store(cf, d)
	return d
}

// templateBody ports TemplateFeature.place (javap -c):
//
//	entry    = config.templates().getRandomOrThrow(rng)     // nextInt(totalWeight)
//	rotation = Util.getRandom(entry.rotations(), rng)        // rotations[nextInt(size)]
//	template = structureManager.getOrCreate(entry.template())
//	vec3iX   = getRotatedOffset(rotation, Axis.X, template)
//	vec3iZ   = getRotatedOffset(rotation, Axis.Z, template)
//	blockPos = origin.offset(vec3iX).offset(vec3iZ)
//	settings = new StructurePlaceSettings().setRotation(rotation).setRandom(rng)
//	return template.placeInWorld(level, blockPos, blockPos, settings, rng, 3)
//
// The two getRotatedOffset calls (X then Z) draw NO rng; the draws are exactly getRandomOrThrow
// (1) then Util.getRandom over the rotations list (1), in that order. placeInWorld then places
// every template block (no processors, pivot 0,0) -- REAL placement via the runtime template
// placer (world/structure), NOT a stub.
func templateBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	d := decodeTemplateCached(cf)
	if d.err != nil {
		panic(d.err.Error())
	}
	if bctx.view == nil {
		return false
	}

	// getRandomOrThrow(rng): selector.get(nextInt(totalWeight)) = cumulative-weight walk.
	r := int(rng.NextIntN(int32(d.totalWeight)))
	idx := 0
	for i := range d.entries {
		r -= d.entries[i].weight
		if r < 0 {
			idx = i
			break
		}
	}
	entry := d.entries[idx]
	tpl := d.templates[idx]

	// Util.getRandom(entry.rotations(), rng) = list.get(nextInt(size)).
	rot := entry.rotations[int(rng.NextIntN(int32(len(entry.rotations))))]

	// blockPos = origin.offset(getRotatedOffset(rot, X)).offset(getRotatedOffset(rot, Z)).
	ox, oy, oz := tpl.RotatedOffset(rot, structure.AxisX)
	zx, zy, zz := tpl.RotatedOffset(rot, structure.AxisZ)
	anchor := structure.Pos{X: origin.X + ox + zx, Y: origin.Y + oy + zy, Z: origin.Z + oz + zz}

	// StructurePlaceSettings: rotation set, random set, mirror NONE, pivot (0,0), no processors.
	// placeInWorld(level, blockPos, blockPos, settings, rng, 3): the second blockPos is the clip
	// anchor; vanilla default settings bounding box is infinite, so we pass a full box and let
	// the Neighborhood apply the real chunk-window clip (as FossilFeature does).
	box := structure.BoundingBox{
		MinX: math.MinInt32, MinY: math.MinInt32, MinZ: math.MinInt32,
		MaxX: math.MaxInt32, MaxY: math.MaxInt32, MaxZ: math.MaxInt32,
	}
	tpl.PlaceInWorld(bctx.view, anchor, rot, structure.MirrorNone, 0, 0, nil, box, rng)
	_ = ctx
	return true
}
