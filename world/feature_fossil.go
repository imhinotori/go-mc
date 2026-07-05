package world

// feature_fossil.go ports FossilFeature — the buried skeleton/coal(diamond) fossils
// (net.minecraft.world.level.levelgen.feature.FossilFeature, javap -c against
// temp/cache/26.2-inner.jar). This is a StructureTemplate feature: it picks one of 8
// spine/skull templates, rotates it randomly, drops it to a depth under the OCEAN_FLOOR_WG
// height, rejects if too many corners are exposed, then places the fossil template AND an
// overlay template (the coal/diamond ore veins), each through its processor chain.
//
// The 16-01 runtime StructureTemplate placer (world/structure/template.go) is REUSED for
// the .nbt load + rotation + place; the two fossil processor kinds not in the STRUCT-05
// village set (minecraft:block_rot, minecraft:protected_blocks) are ported here as
// structure.TemplateProcessor impls that draw from the PLACE-TIME rng — matching
// StructurePlaceSettings.getRandom(pos) returning settings.random when setRandom(rng) was
// called (FossilFeature DOES call setRandom, so every processor's per-block RandomSource IS
// the shared place-time rng). The 16 fossil .nbt templates were extracted from the jar into
// world/levelgen/data/structure/fossil/ (embedded via the data package's //go:embed).
//
// RNG DRAW ORDER (javap -c FossilFeature.place) — the determinism contract:
//
//	rotation = Rotation.getRandom(rng)            // values()[nextInt(4)]                  (1 draw)
//	i7 = nextInt(fossilStructures.size())         // pick index (size 8 -> nextInt(8))     (1 draw)
//	... height scan over OCEAN_FLOOR_WG (no draws) ...
//	i17 = max(i16 - 15 - nextInt(10), minY + 10)  // the drop depth                        (1 draw)
//	countEmptyCorners(...) ; if > maxEmptyCornersAllowed: return false                     (no draws)
//	placeInWorld(fossilTemplate, blockPos18, fossilProcessors, rng)  // block_rot draws per block
//	placeInWorld(overlayTemplate, blockPos18, overlayProcessors, rng) // block_rot draws per block
//	return true
//
// So the LEADING draws (rotation, index, depth) are exact; then each template's block_rot
// processor draws one nextFloat PER template block (in .nbt block order, the same order
// vanilla's block-outer/processor-inner processBlockInfos loop uses — verified via javap).
//
// Source mapping (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.FossilFeature.place / countEmptyCorners
//   - net.minecraft.world.level.block.Rotation.getRandom = values()[nextInt(4)]
//   - StructureTemplate.getSize(Rotation) / getZeroPositionWithTransform (world/structure)
//   - StructureTemplate.placeInWorld (world/structure PlaceInWorld — REUSED)
//   - BlockRotProcessor.processBlock / ProtectedBlockProcessor.processBlock (ported below)

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/structure"
)

func init() { registerFeatureBody("fossil", fossilBody) }

// fossilConfig is FossilFeatureConfiguration: the two template id lists (fossil = the bone
// structure, overlay = the ore vein), their processor list ids, and the empty-corner cap.
type fossilConfig struct {
	FossilStructures     []string `json:"fossil_structures"`
	OverlayStructures    []string `json:"overlay_structures"`
	FossilProcessors     string   `json:"fossil_processors"`
	OverlayProcessors    string   `json:"overlay_processors"`
	MaxEmptyCornersAllow int      `json:"max_empty_corners_allowed"`
}

// fossilDecoded memoizes the parsed config + the loaded templates + processor chains per
// ConfiguredFeature (oreConfigCache pattern). Templates and processor lists are immutable
// after load, so the cached value is shared across placements.
type fossilDecoded struct {
	cfg               fossilConfig
	fossilTemplates   []*structure.StructureTemplate
	overlayTemplates  []*structure.StructureTemplate
	fossilProcessors  []structure.TemplateProcessor
	overlayProcessors []structure.TemplateProcessor
	err               error
}

var fossilCache sync.Map // map[*feature.ConfiguredFeature]*fossilDecoded

func decodeFossilCached(cf *feature.ConfiguredFeature) *fossilDecoded {
	if v, ok := fossilCache.Load(cf); ok {
		return v.(*fossilDecoded)
	}
	d := &fossilDecoded{}
	if err := json.Unmarshal(configRaw(cf), &d.cfg); err != nil {
		d.err = fmt.Errorf("world: fossil config %q: %w", cf.ID, err)
		fossilCache.Store(cf, d)
		return d
	}
	if len(d.cfg.FossilStructures) == 0 || len(d.cfg.OverlayStructures) == 0 {
		d.err = fmt.Errorf("world: fossil config %q has empty structure list", cf.ID)
		fossilCache.Store(cf, d)
		return d
	}
	if len(d.cfg.FossilStructures) != len(d.cfg.OverlayStructures) {
		// FossilFeature indexes BOTH lists with the same i7; they must be parallel.
		d.err = fmt.Errorf("world: fossil config %q fossil/overlay lists differ in length", cf.ID)
		fossilCache.Store(cf, d)
		return d
	}
	for _, id := range d.cfg.FossilStructures {
		t, err := structure.LoadTemplate(id)
		if err != nil {
			d.err = fmt.Errorf("world: fossil template %q: %w", id, err)
			fossilCache.Store(cf, d)
			return d
		}
		d.fossilTemplates = append(d.fossilTemplates, t)
	}
	for _, id := range d.cfg.OverlayStructures {
		t, err := structure.LoadTemplate(id)
		if err != nil {
			d.err = fmt.Errorf("world: fossil overlay template %q: %w", id, err)
			fossilCache.Store(cf, d)
			return d
		}
		d.overlayTemplates = append(d.overlayTemplates, t)
	}
	fp, err := loadFossilProcessorList(d.cfg.FossilProcessors)
	if err != nil {
		d.err = fmt.Errorf("world: fossil processors %q: %w", d.cfg.FossilProcessors, err)
		fossilCache.Store(cf, d)
		return d
	}
	d.fossilProcessors = fp
	op, err := loadFossilProcessorList(d.cfg.OverlayProcessors)
	if err != nil {
		d.err = fmt.Errorf("world: fossil overlay processors %q: %w", d.cfg.OverlayProcessors, err)
		fossilCache.Store(cf, d)
		return d
	}
	d.overlayProcessors = op
	fossilCache.Store(cf, d)
	return d
}

// fossilBody ports FossilFeature.place. Returns false when the drop rejects (too many empty
// corners), true once both templates place.
func fossilBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	d := decodeFossilCached(cf)
	if d.err != nil {
		// A build-data error (missing template / unported processor) — panic so it surfaces
		// during generation rather than silently producing no fossil (T-12-07).
		panic(d.err.Error())
	}
	if bctx.view == nil {
		// A bare unit-test bodyContext has no writable view; nothing to place.
		return false
	}

	// rotation = Rotation.getRandom(rng) = values()[nextInt(4)] (1 draw).
	rot := fossilRotations[int(rng.NextIntN(4))]

	// i7 = nextInt(fossilStructures.size()) (1 draw) — indexes BOTH parallel lists.
	idx := int(rng.NextIntN(int32(len(d.fossilTemplates))))
	fossilTpl := d.fossilTemplates[idx]
	overlayTpl := d.overlayTemplates[idx]

	// size = fossilTemplate.getSize(rotation) (the rotated extent).
	sizeX, _, sizeZ := fossilTpl.RotatedSize(rot)

	// blockPos15 = origin.offset(-sizeX/2, 0, -sizeZ/2). (Integer division of the NEGATED
	// extent, per the bytecode: ineg iconst_2 idiv.)
	baseX := origin.X + (-sizeX)/2
	baseZ := origin.Z + (-sizeZ)/2

	// i16 = min over the OCEAN_FLOOR_WG heights across the rotated footprint (start origin.Y).
	i16 := origin.Y
	for dx := 0; dx < sizeX; dx++ {
		for dz := 0; dz < sizeZ; dz++ {
			h := ctx.GetHeight(placement.OceanFloorWG, baseX+dx, baseZ+dz)
			if h < i16 {
				i16 = h
			}
		}
	}

	// i17 = max(i16 - 15 - nextInt(10), minY + 10) (1 draw).
	depth := i16 - 15 - int(rng.NextIntN(10))
	if floor := ctx.MinY() + 10; depth < floor {
		depth = floor
	}

	// blockPos18 = getZeroPositionWithTransform(blockPos15.atY(i17), Mirror.NONE, rotation).
	// The anchor is (baseX, i17, baseZ) shifted by the rotation's zero-position offset so the
	// rotated template's min corner lands there.
	zdx, zdy, zdz := fossilTpl.ZeroPositionWithTransform(structure.MirrorNone, rot)
	anchor := structure.Pos{X: baseX + zdx, Y: depth + zdy, Z: baseZ + zdz}

	// countEmptyCorners over the fossil template's placed bounding box; reject if too many.
	box := fossilTpl.BoundingBoxAt(anchor, rot, structure.MirrorNone, 0, 0)
	if fossilCountEmptyCorners(bctx, box) > d.cfg.MaxEmptyCornersAllow {
		return false
	}

	// The full-chunk-plus-margin writable box (jar: chunkMin-16 .. chunkMax+16, minY..maxY).
	// The Neighborhood clips out-of-window writes regardless; this matches the vanilla clip.
	place := fossilWritableBox(ctx, origin)

	// place the fossil template, then the overlay template — each with its processors, each
	// consuming the shared rng via block_rot (in .nbt block order). MirrorNone, pivot 0,0.
	fossilTpl.PlaceInWorld(bctx.view, anchor, rot, structure.MirrorNone, 0, 0, d.fossilProcessors, place, rng)
	overlayTpl.PlaceInWorld(bctx.view, anchor, rot, structure.MirrorNone, 0, 0, d.overlayProcessors, place, rng)
	return true
}

// fossilRotations is Rotation.values() order [NONE, CLOCKWISE_90, CLOCKWISE_180,
// COUNTERCLOCKWISE_90] — getRandom draws nextInt(4) and indexes this.
var fossilRotations = [4]structure.Rotation{
	structure.RotNone,
	structure.RotClockwise90,
	structure.RotClockwise180,
	structure.RotCounterclockwise90,
}

// fossilWritableBox ports the FossilFeature place box: [chunkMinX-16, minY, chunkMinZ-16] ..
// [chunkMaxX+16, maxY, chunkMaxZ+16] around the origin's chunk. This is the StructurePlaceSettings
// bounding box the placer clips to; the Neighborhood ALSO clips to its 3x3 window, so a fossil
// crossing a chunk edge spills into the neighbor exactly as vanilla.
func fossilWritableBox(ctx placement.PlacementContext, origin placement.BlockPos) structure.BoundingBox {
	chunkMinX := (origin.X >> 4) << 4
	chunkMinZ := (origin.Z >> 4) << 4
	return structure.BoundingBox{
		MinX: chunkMinX - 16,
		MinY: ctx.MinY(),
		MinZ: chunkMinZ - 16,
		MaxX: chunkMinX + 15 + 16,
		MaxY: ctx.MinY() + ctx.Height() - 1,
		MaxZ: chunkMinZ + 15 + 16,
	}
}

// fossilCountEmptyCorners ports FossilFeature.countEmptyCorners: over the 8 corners of the
// box, count those whose block is air, lava, or water. BoundingBox.forAllCorners visits all
// 8 min/max combinations; the count is order-independent.
func fossilCountEmptyCorners(bctx *bodyContext, box structure.BoundingBox) int {
	count := 0
	xs := [2]int{box.MinX, box.MaxX}
	ys := [2]int{box.MinY, box.MaxY}
	zs := [2]int{box.MinZ, box.MaxZ}
	for _, x := range xs {
		for _, y := range ys {
			for _, z := range zs {
				st := bctx.getState(placement.BlockPos{X: x, Y: y, Z: z})
				if block.IsAir(st) || fossilIsLava(st) || fossilIsWater(st) {
					count++
				}
			}
		}
	}
	return count
}

// fossilLavaID / fossilWaterID are the block ids the empty-corner check treats as "empty"
// (BlockState.is(Blocks.LAVA) / is(Blocks.WATER) — property-agnostic block-id matches).
func fossilIsLava(st block.StateID) bool  { return stateBlockIDWorld(st) == "minecraft:lava" }
func fossilIsWater(st block.StateID) bool { return stateBlockIDWorld(st) == "minecraft:water" }

// stateBlockIDWorld returns the block id of a StateID (property-agnostic), or "" out of range.
func stateBlockIDWorld(st block.StateID) string {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return ""
	}
	b := block.StateList[st]
	if b == nil {
		return ""
	}
	return b.ID()
}

// ---- fossil processor loading (block_rot + protected_blocks + rule) ----

// loadFossilProcessorList loads a processor_list JSON by id and parses it into a chain that
// supports the fossil processor kinds: minecraft:block_rot, minecraft:protected_blocks, and
// minecraft:rule (fossil_diamonds' coal_ore -> deepslate_diamond_ore substitution). The
// structure package's LoadProcessorList only handles rule, so the fossil-specific kinds are
// parsed here.
func loadFossilProcessorList(id string) ([]structure.TemplateProcessor, error) {
	raw, err := data.ProcessorListJSON(id)
	if err != nil {
		return nil, err
	}
	return parseFossilProcessorList(raw)
}

// parseFossilProcessorList parses a {"processors":[...]} list into a TemplateProcessor chain,
// dispatching block_rot / protected_blocks here and delegating minecraft:rule to the
// structure package's rule parser.
func parseFossilProcessorList(raw []byte) ([]structure.TemplateProcessor, error) {
	var pl struct {
		Processors []json.RawMessage `json:"processors"`
	}
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, fmt.Errorf("processor list json: %w", err)
	}
	out := make([]structure.TemplateProcessor, 0, len(pl.Processors))
	for i, pr := range pl.Processors {
		var head struct {
			ProcessorType string          `json:"processor_type"`
			Integrity     float32         `json:"integrity"`
			Value         string          `json:"value"`
			RottableTag   json.RawMessage `json:"rottable_blocks"`
		}
		if err := json.Unmarshal(pr, &head); err != nil {
			return nil, fmt.Errorf("processor[%d] head: %w", i, err)
		}
		switch stripNS(head.ProcessorType) {
		case "block_rot":
			// BlockRotProcessor(integrity) — fossil lists carry no rottable_blocks set, so
			// EVERY block is subject to the integrity roll (matching the no-set constructor).
			out = append(out, &blockRotProcessor{integrity: head.Integrity})
		case "protected_blocks":
			// ProtectedBlockProcessor(#features_cannot_replace): drop a template block when the
			// EXISTING world block is protected. The `value` is the tag id.
			out = append(out, &protectedBlockProcessor{protected: fossilProtectedSet(head.Value)})
		case "rule":
			rp, err := structure.ParseProcessorList(wrapSingleProcessor(pr))
			if err != nil {
				return nil, fmt.Errorf("processor[%d] rule: %w", i, err)
			}
			out = append(out, rp...)
		default:
			return nil, fmt.Errorf("unported fossil processor_type %q "+
				"(add it constant-for-constant from the jar StructureProcessor def)", head.ProcessorType)
		}
	}
	return out, nil
}

// wrapSingleProcessor wraps one processor JSON object into a {"processors":[obj]} list so it
// can be fed to structure.ParseProcessorList (which parses a processor LIST).
func wrapSingleProcessor(pr json.RawMessage) []byte {
	return []byte(`{"processors":[` + string(pr) + `]}`)
}

// blockRotProcessor ports BlockRotProcessor.processBlock: draw one nextFloat from the PER-BLOCK
// RandomSource; keep the block iff nextFloat() <= integrity, else drop it. FossilFeature calls
// setRandom(rng), so StructurePlaceSettings.getRandom(pos) returns the SHARED place-time rng
// (NOT a per-position seed) — hence this processor draws from the passed-in place-time rng, in
// template block order. With no rottable_blocks set, EVERY block is subject to the roll.
type blockRotProcessor struct{ integrity float32 }

func (p *blockRotProcessor) Process(_ structure.WorldGenView, _, _, _ int, state block.StateID, rng levelgen.RandomSource) (block.StateID, bool) {
	if rng.NextFloat() <= p.integrity {
		return state, true
	}
	return state, false
}

// protectedBlockProcessor ports ProtectedBlockProcessor.processBlock: read the EXISTING world
// block; if it is(cannotReplace) (#features_cannot_replace) DROP the template block (return
// keep=false), else keep. NO rng draws.
type protectedBlockProcessor struct{ protected map[block.StateID]bool }

func (p *protectedBlockProcessor) Process(view structure.WorldGenView, wx, wy, wz int, state block.StateID, _ levelgen.RandomSource) (block.StateID, bool) {
	existing := view.GetBlock(wx, wy, wz)
	if p.protected[existing] {
		return state, false
	}
	return state, true
}

// fossilProtectedSet resolves the protected-blocks tag value to a StateID set. The only value
// fossil uses is "#minecraft:features_cannot_replace" (the same tag feature_dungeon.go's
// safeSet consults), so it REUSES that resolved set. An unexpected value FAILS LOUD so a jar
// bump that changes it is caught.
func fossilProtectedSet(value string) map[block.StateID]bool {
	switch value {
	case "#minecraft:features_cannot_replace", "minecraft:features_cannot_replace":
		return featuresCannotReplace
	default:
		panic(fmt.Sprintf("world: fossil protected_blocks unexpected value %q "+
			"(only #minecraft:features_cannot_replace is used by the fossil processors)", value))
	}
}
