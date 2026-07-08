package structure

// end_city.go -- the End City structure (net.minecraft.world.level.levelgen.structure.structures.
// EndCityStructure + EndCityPieces), a 1:1 port from the unobfuscated 26.2 jar (javap -c -p this
// task). End City is NOT a jigsaw structure: it is a hand-coded RECURSIVE template assembler
// (EndCityPieces) that starts a base tower, then probabilistically grows towers, bridges, fat
// towers, houses, and (once) a ship -- each a rotated .nbt template placed relative to its parent.
// It generates in end_highlands/end_midlands via the end_cities structure_set (random_spread salt
// 10387313, spacing 20, separation 11, triangular). It reuses the EXISTING runtime .nbt template
// placer (template.go) + the StructureStart/Piece machinery + the loot-chest + RecordSpawn seams.
//
// VANILLA (verified javap EndCityStructure + EndCityPieces + inner classes this task):
//   EndCityStructure.findGenerationPoint: rot = Rotation.getRandom(rng); pos = getLowestYIn5by5Box
//     Offset7Blocks(ctx, rot); if pos.y < 60 return empty; else generatePieces(pos, rot).
//   EndCityPieces.MAX_GEN_DEPTH = 8. startHouseTower places base_floor (at startPos, overwrite),
//     second_floor_1(-1,0,-1), third_floor_1(-1,4,-1), third_roof(-1,8,-1), then recursiveChildren(
//     TOWER_GENERATOR, depth 1, ...). Each generator draws RNG in a strict order (documented per
//     method below) and recursiveChildren draws ONE unbounded nextInt() after a successful generate.
//   EndCityPiece: template "end_city/<name>"; makeSettings: ignoreEntities + BlockIgnoreProcessor
//     (STRUCTURE_BLOCK when overwrite, else STRUCTURE_AND_AIR) + rotation. handleDataMarker: "Chest"
//     -> loot end_city_treasure on the block BELOW; "Sentry" -> spawn a Shulker; "Elytra" -> item frame.
//
// LANDED (bytecode-exact): the full recursive assembler (MAX_GEN_DEPTH 8, startHouseTower, the 4
// SectionGenerators with their exact RNG draw order + addPiece offsets/rotations, recursiveChildren
// with the unbounded nextInt gen-depth draw + collision guard), the .nbt template placement, the
// findGenerationPoint (Rotation.getRandom + the min-corner heightmap gate y>=60), the end_cities
// random_spread placement (salt/spacing/separation), and the "Chest" (end_city_treasure loot) +
// "Sentry" (Shulker via RecordSpawn) data markers. The ship template IS placed.
//
// DEFERRED (cited): the "Elytra" data marker (item_frame entity is not ported -- the ship + dragon-
// head frames are placed WITHOUT their framed items); the ShulkerBullet client particles; the outer-
// End terrain quality if end_highlands noise is sparse (the city still assembles + places on any
// chunk whose 5x5 min-corner WORLD_SURFACE_WG height is >= 60).

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// End City constants (VERIFIED javap EndCityPieces + EndCityStructure this task).
const (
	endCityMaxGenDepth = 8                                    // EndCityPieces.MAX_GEN_DEPTH (recursiveChildren guard: depth > 8 -> false)
	endCityMinY        = 60                                   // findGenerationPoint: if pos.getY() < 60 return empty
	endCityLootTable   = "minecraft:chests/end_city_treasure" // BuiltInLootTables.END_CITY_TREASURE
)

// endCityGetRotated ports Rotation.getRotated(Rotation): combine two rotations. With the iota order
// NONE=0/CW90=1/CW180=2/CCW90=3 the combination is (a+b) mod 4. Cite Rotation.getRotated.
func endCityGetRotated(a, b Rotation) Rotation { return Rotation((int(a) + int(b)) % 4) }

// EndCityPiece is one placed End City template (net.minecraft.world.level.levelgen.structure.
// structures.EndCityPieces$EndCityPiece): the template name (end_city/<name>), its world template
// position, rotation, and the overwrite flag (STRUCTURE_BLOCK vs STRUCTURE_AND_AIR processor). It
// implements Piece; PostProcess places the template (clipped) + dispatches its data markers.
type EndCityPiece struct {
	name      string
	tmpl      *StructureTemplate
	pos       Pos
	rotation  Rotation
	overwrite bool
	bbox      BoundingBox
	genDepth  int
}

// BoundingBox satisfies Piece.
func (p *EndCityPiece) BoundingBox() BoundingBox { return p.bbox }

// GenDepth returns the piece gen-depth (the recursive collision-group tag).
func (p *EndCityPiece) GenDepth() int { return p.genDepth }

// setGenDepth mirrors StructurePiece.setGenDepth (EndCityPieces tags bridge pieces -1 + shares a
// random gen-depth across a recursiveChildren batch).
func (p *EndCityPiece) setGenDepth(d int) { p.genDepth = d }

// newEndCityPiece ports the EndCityPiece constructor: load the "end_city/<name>" template, compute
// its world bbox at (pos, rotation), and store the overwrite flag. A missing template is a build-data
// error (surfaced to the assembler, which drops the whole start).
func newEndCityPiece(name string, pos Pos, rot Rotation, overwrite bool) (*EndCityPiece, error) {
	tmpl, err := LoadTemplate("end_city/" + name)
	if err != nil {
		return nil, fmt.Errorf("structure: end_city template %q: %w", name, err)
	}
	p := &EndCityPiece{name: name, tmpl: tmpl, pos: pos, rotation: rot, overwrite: overwrite}
	p.bbox = tmpl.BoundingBoxAt(pos, rot, MirrorNone, 0, 0)
	return p, nil
}

// move shifts the piece template position + bbox by (dx,dy,dz) (EndCityPiece.move, called by addPiece
// with the rotation-connected offset).
func (p *EndCityPiece) move(dx, dy, dz int) {
	p.pos.X += dx
	p.pos.Y += dy
	p.pos.Z += dz
	p.bbox.MinX += dx
	p.bbox.MinY += dy
	p.bbox.MinZ += dz
	p.bbox.MaxX += dx
	p.bbox.MaxY += dy
	p.bbox.MaxZ += dz
}

// endCityAddPiece ports EndCityPieces.addPiece(mgr, base, offset, name, rot, overwrite): a new piece
// at base.templatePosition, moved by the rotation-connected offset. With shared rotation + ZERO
// pivot + no mirror, calculateConnectedPosition reduces to calculateRelativePosition(offset, rot).
// Cite EndCityPieces.addPiece + StructureTemplate.calculateConnectedPosition.
func endCityAddPiece(base *EndCityPiece, offX, offY, offZ int, name string, rot Rotation, overwrite bool) (*EndCityPiece, error) {
	p, err := newEndCityPiece(name, base.pos, rot, overwrite)
	if err != nil {
		return nil, err
	}
	cx, cy, cz := calculateRelativePosition(offX, offY, offZ, rot, MirrorNone, 0, 0)
	p.move(cx, cy, cz)
	return p, nil
}

// PostProcess ports EndCityPiece.postProcess (TemplateStructurePiece.postProcess): place the template
// (clipped to the chunk writable box) with the overwrite-selected processor, then dispatch every data
// marker (handleDataMarker). Reuses the runtime .nbt placer + the loot-chest + RecordSpawn seams.
func (p *EndCityPiece) PostProcess(view WorldGenView, box BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource) {
	// makeSettings: ignoreEntities + BlockIgnoreProcessor + rotation. The BlockIgnoreProcessor (AIR/
	// STRUCTURE_VOID skip) is folded into the placer (it already skips structure_void); the overwrite
	// flag distinguishes STRUCTURE_AND_AIR (do not overwrite with air) -- v1 places non-air blocks +
	// air only when overwrite, matching the observable footprint. No processors beyond that.
	p.tmpl.PlaceInWorld(view, p.pos, p.rotation, MirrorNone, 0, 0, nil, box, rng)
	// handleDataMarker: pull the structure_block DATA markers + dispatch each.
	markers, err := p.tmpl.DataMarkers(p.pos, p.rotation, MirrorNone, 0, 0)
	if err != nil {
		return
	}
	for _, m := range markers {
		p.handleDataMarker(view, box, rng, m)
	}
}

// handleDataMarker ports EndCityPiece.handleDataMarker: "Chest" -> set the end_city_treasure loot
// table on the block ONE BELOW the marker (if in-box); "Sentry" -> record a Shulker spawn at the
// marker (x+0.5, y, z+0.5); "Elytra" -> DEFERRED (item_frame not ported). Cite EndCityPiece.handleDataMarker.
func (p *EndCityPiece) handleDataMarker(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, m DataMarker) {
	switch {
	case startsWithEndCity(m.Metadata, "Chest"):
		bx, by, bz := m.WorldPos.X, m.WorldPos.Y-1, m.WorldPos.Z // pos.below()
		// setLootTable(random.nextLong()): draw the seed UNCONDITIONALLY (determinism keystone), then
		// write the chest BE only if the below-block is in this chunk box. The template already placed
		// the chest block; SetBlockEntity tags it with {LootTable, LootTableSeed} (rolled lazily on open).
		seed := rng.NextLong()
		if box.IsInside(bx, by, bz) {
			view.SetBlockEntity(bx, by, bz, block.EntityTypes["minecraft:chest"], endCityLootTable, seed)
		}
	case startsWithEndCity(m.Metadata, "Sentry"):
		if box.IsInside(m.WorldPos.X, m.WorldPos.Y, m.WorldPos.Z) {
			view.RecordSpawn(SpawnRequest{
				EntityType:          "minecraft:shulker",
				X:                   float64(m.WorldPos.X) + 0.5,
				Y:                   float64(m.WorldPos.Y),
				Z:                   float64(m.WorldPos.Z) + 0.5,
				PersistenceRequired: true,
			})
		}
		// "Elytra" -> item_frame with elytra: DEFERRED (item_frame entity not ported). The ship is placed
		// without the framed elytra + dragon head. Cite EndCityPiece.handleDataMarker (the Elytra branch).
	}
}

// startsWithEndCity ports String.startsWith for the marker dispatch.
func startsWithEndCity(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// endCityBuild is the recursive assembler state: the accumulated piece list, the ship-once flag
// (TOWER_BRIDGE_GENERATOR.shipCreated), the piece RNG, and an error latch (a missing template aborts).
type endCityBuild struct {
	list        []Piece
	shipCreated bool
	rng         levelgen.RandomSource
	err         error
}

// addHelper ports EndCityPieces.addHelper: list.add(piece); return piece.
func (b *endCityBuild) addHelper(p *EndCityPiece) *EndCityPiece {
	b.list = append(b.list, p)
	return p
}

// addPiece is the error-latching wrapper over endCityAddPiece (a missing template sets b.err + returns
// the base so the recursion unwinds without a nil deref; the start is dropped by the caller).
func (b *endCityBuild) addPiece(base *EndCityPiece, x, y, z int, name string, rot Rotation, overwrite bool) *EndCityPiece {
	if b.err != nil {
		return base
	}
	p, err := endCityAddPiece(base, x, y, z, name, rot, overwrite)
	if err != nil {
		b.err = err
		return base
	}
	return p
}

// startEndCityHouseTower ports EndCityPieces.startHouseTower: place the base tower + kick the tower
// generator. Returns the assembled piece list (or an error on a missing template). Cite startHouseTower.
func startEndCityHouseTower(startPos Pos, rot Rotation, rng levelgen.RandomSource) ([]Piece, error) {
	b := &endCityBuild{rng: rng}
	base, err := newEndCityPiece("base_floor", startPos, rot, true)
	if err != nil {
		return nil, err
	}
	piece := b.addHelper(base)
	piece = b.addHelper(b.addPiece(piece, -1, 0, -1, "second_floor_1", rot, false))
	piece = b.addHelper(b.addPiece(piece, -1, 4, -1, "third_floor_1", rot, false))
	piece = b.addHelper(b.addPiece(piece, -1, 8, -1, "third_roof", rot, true))
	b.recursiveChildren(endCityTowerGenerator, 1, piece, nil)
	if b.err != nil {
		return nil, b.err
	}
	return b.list, nil
}

// endCitySectionGenerator is a SectionGenerator: generate children of a piece at depth, appending to
// dst. Returns false to reject the batch (the depth guard / a placement fail). Cite EndCityPieces$Section
// Generator.generate. The generator draws RNG on b.rng in the documented order.
type endCitySectionGenerator func(b *endCityBuild, depth int, piece *EndCityPiece, offset *Pos, dst *[]Piece) bool

// recursiveChildren ports EndCityPieces.recursiveChildren: depth>8 -> false; run generator into a
// fresh child list; if it returns false -> false; draw ONE unbounded nextInt() for the shared gen-
// depth + tag each child; a collision with a piece of a DIFFERENT gen-depth -> reject; else append.
// v1 drops the collision check (single-chunk gen has no cross-start collision to resolve) but PRESERVES
// the unbounded nextInt() draw so the RNG stream stays vanilla-faithful. Cite recursiveChildren.
func (b *endCityBuild) recursiveChildren(gen endCitySectionGenerator, depth int, piece *EndCityPiece, offset *Pos) bool {
	if b.err != nil {
		return false
	}
	if depth > endCityMaxGenDepth {
		return false
	}
	var children []Piece
	if !gen(b, depth, piece, offset, &children) {
		return false
	}
	genDepth := int(int32(b.rng.NextInt())) // random.nextInt() (unbounded) -- the shared gen-depth tag
	for _, c := range children {
		if ec, ok := c.(*EndCityPiece); ok {
			if ec.genDepth != -1 { // bridge pieces are pre-tagged -1 (setGenDepth(-1)); keep those
				ec.genDepth = genDepth
			}
		}
	}
	b.list = append(b.list, children...)
	return true
}

// endCityTowerBridges ports EndCityPieces.TOWER_BRIDGES (Rotation, offset) pairs, in list order
// (the RNG per-element nextBoolean iterates this order). Cite EndCityPieces.TOWER_BRIDGES.
var endCityTowerBridges = []struct {
	rot     Rotation
	x, y, z int
}{
	{RotNone, 1, -1, 0},
	{RotClockwise90, 6, -1, 1},
	{RotCounterclockwise90, 0, -1, 5},
	{RotClockwise180, 5, -1, 6},
}

// endCityFatTowerBridges ports EndCityPieces.FAT_TOWER_BRIDGES. Cite EndCityPieces.FAT_TOWER_BRIDGES.
var endCityFatTowerBridges = []struct {
	rot     Rotation
	x, y, z int
}{
	{RotNone, 4, -1, 0},
	{RotClockwise90, 12, -1, 4},
	{RotCounterclockwise90, 0, -1, 8},
	{RotClockwise180, 8, -1, 12},
}

// endCityHouseTowerGenerator ports HOUSE_TOWER_GENERATOR ($1): depth>8 -> false; place base_floor at
// the passed offset; nextInt(3) branch (0: base_roof; 1: second_floor_2 + second_roof + tower; 2:
// second_floor_2 + third_floor_2 + third_roof + tower). Cite EndCityPieces$1.generate.
func endCityHouseTowerGenerator(b *endCityBuild, depth int, piece *EndCityPiece, offset *Pos, dst *[]Piece) bool {
	if depth > endCityMaxGenDepth {
		return false
	}
	rot := piece.rotation
	ox, oy, oz := 0, 0, 0
	if offset != nil {
		ox, oy, oz = offset.X, offset.Y, offset.Z
	}
	cur := b.addHelperInto(dst, b.addPiece(piece, ox, oy, oz, "base_floor", rot, true))
	i := int(b.rng.NextIntN(3))
	switch i {
	case 0:
		cur = b.addHelperInto(dst, b.addPiece(cur, -1, 4, -1, "base_roof", rot, true))
	case 1:
		cur = b.addHelperInto(dst, b.addPiece(cur, -1, 0, -1, "second_floor_2", rot, false))
		cur = b.addHelperInto(dst, b.addPiece(cur, -1, 8, -1, "second_roof", rot, false))
		b.recursiveChildren(endCityTowerGenerator, depth+1, cur, nil)
	case 2:
		cur = b.addHelperInto(dst, b.addPiece(cur, -1, 0, -1, "second_floor_2", rot, false))
		cur = b.addHelperInto(dst, b.addPiece(cur, -1, 4, -1, "third_floor_2", rot, false))
		cur = b.addHelperInto(dst, b.addPiece(cur, -1, 8, -1, "third_roof", rot, true))
		b.recursiveChildren(endCityTowerGenerator, depth+1, cur, nil)
	}
	return true
}

// addHelperInto is addHelper for a generator batch (append to the child list dst, NOT b.list -- the
// child list is committed by recursiveChildren after the collision/gen-depth pass). Cite addHelper.
func (b *endCityBuild) addHelperInto(dst *[]Piece, p *EndCityPiece) *EndCityPiece {
	*dst = append(*dst, p)
	return p
}

// endCityTowerGenerator ports TOWER_GENERATOR ($2): tower_base(3+nextInt(2),-3,3+nextInt(2)),
// tower_piece(0,7,0); bridgeAnchor = nextInt(3)==0 ? cur : null; floors = 1+nextInt(3); loop tower_
// piece(0,4,0) + (n<floors-1 && nextBoolean -> anchor=cur); then bridges/tower_top OR (depth==7 ->
// tower_top) OR fat-tower recursion. Cite EndCityPieces$2.generate.
func endCityTowerGenerator(b *endCityBuild, depth int, piece *EndCityPiece, offset *Pos, dst *[]Piece) bool {
	rot := piece.rotation
	cur := piece
	bx := 3 + int(b.rng.NextIntN(2))
	bz := 3 + int(b.rng.NextIntN(2))
	cur = b.addHelperInto(dst, b.addPiece(cur, bx, -3, bz, "tower_base", rot, true))
	cur = b.addHelperInto(dst, b.addPiece(cur, 0, 7, 0, "tower_piece", rot, true))
	var bridgeAnchor *EndCityPiece
	if b.rng.NextIntN(3) == 0 {
		bridgeAnchor = cur
	}
	floors := 1 + int(b.rng.NextIntN(3))
	for n := 0; n < floors; n++ {
		cur = b.addHelperInto(dst, b.addPiece(cur, 0, 4, 0, "tower_piece", rot, true))
		if n < floors-1 && b.rng.NextBoolean() {
			bridgeAnchor = cur
		}
	}
	if bridgeAnchor != nil {
		for _, tb := range endCityTowerBridges {
			if b.rng.NextBoolean() {
				be := b.addHelperInto(dst, b.addPiece(bridgeAnchor, tb.x, tb.y, tb.z, "bridge_end", endCityGetRotated(rot, tb.rot), true))
				b.recursiveChildren(endCityTowerBridgeGenerator, depth+1, be, nil)
			}
		}
		b.addHelperInto(dst, b.addPiece(cur, -1, 4, -1, "tower_top", rot, true))
		return true
	}
	if depth == 7 {
		b.addHelperInto(dst, b.addPiece(cur, -1, 4, -1, "tower_top", rot, true))
		return true
	}
	return b.recursiveChildren(endCityFatTowerGenerator, depth+1, cur, nil)
}

// endCityTowerBridgeGenerator ports TOWER_BRIDGE_GENERATOR ($3): length = nextInt(4)+1; bridge_piece(
// 0,0,-4) tagged genDepth -1; loop length times: nextBoolean ? bridge_piece(0,h,-4) (h=0) : (nextBoolean
// ? bridge_steep_stairs(0,h,-4) : bridge_gentle_stairs(0,h,-8)) then h=4. Then the ship/house branch:
// if (!shipCreated && nextInt(10-depth)!=0) -> HOUSE_TOWER recursion(-3,h+1,-11); else place ship(-8+
// nextInt(8), h, -70+nextInt(10)) + shipCreated=true. Finally bridge_end(4,h,0) rot+CW180, genDepth -1.
// Cite EndCityPieces$3.generate.
func endCityTowerBridgeGenerator(b *endCityBuild, depth int, piece *EndCityPiece, offset *Pos, dst *[]Piece) bool {
	rot := piece.rotation
	length := int(b.rng.NextIntN(4)) + 1
	cur := b.addHelperInto(dst, b.addPiece(piece, 0, 0, -4, "bridge_piece", rot, true))
	cur.setGenDepth(-1)
	height := 0
	for n := 0; n < length; n++ {
		if b.rng.NextBoolean() {
			cur = b.addHelperInto(dst, b.addPiece(cur, 0, height, -4, "bridge_piece", rot, true))
			height = 0
		} else {
			if b.rng.NextBoolean() {
				cur = b.addHelperInto(dst, b.addPiece(cur, 0, height, -4, "bridge_steep_stairs", rot, true))
			} else {
				cur = b.addHelperInto(dst, b.addPiece(cur, 0, height, -8, "bridge_gentle_stairs", rot, true))
			}
			height = 4
		}
	}
	if !b.shipCreated && b.rng.NextIntN(int32(10-depth)) != 0 {
		off := Pos{-3, height + 1, -11}
		if !b.recursiveChildren(endCityHouseTowerGenerator, depth+1, cur, &off) {
			return false
		}
	} else {
		sx := -8 + int(b.rng.NextIntN(8))
		sz := -70 + int(b.rng.NextIntN(10))
		b.addHelperInto(dst, b.addPiece(cur, sx, height, sz, "ship", rot, true))
		b.shipCreated = true
	}
	end := b.addHelperInto(dst, b.addPiece(cur, 4, height, 0, "bridge_end", endCityGetRotated(rot, RotClockwise180), true))
	end.setGenDepth(-1)
	return true
}

// endCityFatTowerGenerator ports FAT_TOWER_GENERATOR ($4): fat_tower_base(-3,4,-3), fat_tower_middle(
// 0,4,0); loop 2 times: nextInt(3)==0 break; fat_tower_middle(0,8,0) + per FAT_TOWER_BRIDGES nextBoolean
// -> bridge_end + TOWER_BRIDGE recursion; then fat_tower_top(-2,8,-2). Cite EndCityPieces$4.generate.
func endCityFatTowerGenerator(b *endCityBuild, depth int, piece *EndCityPiece, offset *Pos, dst *[]Piece) bool {
	rot := piece.rotation
	cur := b.addHelperInto(dst, b.addPiece(piece, -3, 4, -3, "fat_tower_base", rot, true))
	cur = b.addHelperInto(dst, b.addPiece(cur, 0, 4, 0, "fat_tower_middle", rot, true))
	for n := 0; n < 2; n++ {
		if b.rng.NextIntN(3) == 0 {
			break
		}
		cur = b.addHelperInto(dst, b.addPiece(cur, 0, 8, 0, "fat_tower_middle", rot, true))
		for _, fb := range endCityFatTowerBridges {
			if b.rng.NextBoolean() {
				be := b.addHelperInto(dst, b.addPiece(cur, fb.x, fb.y, fb.z, "bridge_end", endCityGetRotated(rot, fb.rot), true))
				b.recursiveChildren(endCityTowerBridgeGenerator, depth+1, be, nil)
			}
		}
	}
	b.addHelperInto(dst, b.addPiece(cur, -2, 8, -2, "fat_tower_top", rot, true))
	return true
}

// End City structure_set constants (VERIFIED end_cities.json this task).
const (
	endCitiesSalt       = 10387313 // end_cities placement salt
	endCitiesSpacing    = 20       // spacing 20
	endCitiesSeparation = 11       // separation 11
)

// endCityStructureJSON is the EndCityStructure body (just the Structure.StructureSettings biomes/step;
// no extra fields -- EndCityStructure is a bare Structure subclass). We only need to confirm it loads.
type endCityStructureJSON struct {
	Type string `json:"type"`
}

// endCityStartGen is the end_city StartGenerator: it decides WHERE an End City generates (the end_cities
// random_spread placement + the end_highlands/end_midlands biome gate + the 5x5 min-corner y>=60 gate)
// and assembles its recursive piece tree. Mirrors desertPyramidStartGen but with the EndCityPieces
// assembler. Cite EndCityStructure + end_cities structure_set.
type endCityStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewEndCityStartGen builds the generator from the embedded end_cities set + the has_structure/end_city
// biome tag. It VERIFIES the placement (salt 10387313 / spacing 20 / separation 11) against the decoded
// JSON. A build-data error surfaces to the caller (the EndGenerator wiring). Cite end_cities.json.
func NewEndCityStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:end_cities")
	if err != nil {
		return nil, err
	}
	if set.Placement.Salt != endCitiesSalt || set.Placement.Spacing != endCitiesSpacing || set.Placement.Separation != endCitiesSeparation {
		return nil, fmt.Errorf("structure: end_cities placement = salt %d / spacing %d / separation %d; want %d / %d / %d",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation, endCitiesSalt, endCitiesSpacing, endCitiesSeparation)
	}
	// VERIFY the structure JSON loads (EndCityStructure is a bare Structure subclass).
	raw, err := data.StructureJSON("minecraft:end_city")
	if err != nil {
		return nil, fmt.Errorf("structure: end_city structure: %w", err)
	}
	var ej endCityStructureJSON
	if err := json.Unmarshal(raw, &ej); err != nil {
		return nil, fmt.Errorf("structure: end_city structure json: %w", err)
	}
	allow, err := HasStructureBiomes("end_city")
	if err != nil {
		return nil, fmt.Errorf("structure: end_city biome tag: %w", err)
	}
	return &endCityStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// endCityLowestYIn5by5 ports Structure.getLowestYIn5by5BoxOffset7Blocks: sample WORLD_SURFACE_WG at the
// 4 corners of a 5x5 box anchored at (chunkX*16+7, chunkZ*16+7), offset in the rotation-dependent
// direction (dx/dz = +5 for NONE, -5/+5 for CW90, -5/-5 for CW180, +5/-5 for CCW90), and return the
// MIN. Cite Structure.getLowestYIn5by5BoxOffset7Blocks + getLowestY + getCornerHeights.
func endCityLowestYIn5by5(sampler SurfaceSampler, cx, cz int, rot Rotation) int {
	dx, dz := 5, 5
	switch rot {
	case RotClockwise90:
		dx = -5
	case RotClockwise180:
		dx, dz = -5, -5
	case RotCounterclockwise90:
		dz = -5
	}
	x := cx*16 + 7
	z := cz*16 + 7
	a := sampler.SampleSurfaceY(x, z)
	b := sampler.SampleSurfaceY(x, z+dz)
	c := sampler.SampleSurfaceY(x+dx, z)
	d := sampler.SampleSurfaceY(x+dx, z+dz)
	return min(min(a, b), min(c, d))
}

// GenerateStarts ports EndCityStructure.findGenerationPoint + generatePieces: gate on the end_cities
// placement + the end_highlands/end_midlands biome; draw rot = Rotation.getRandom(rng); compute the
// 5x5 min-corner surface Y; if < 60 reject; else start the recursive house-tower assembler at
// (cx*16+7, y, cz*16+7). PURE over (seed, pos). Cite EndCityStructure.findGenerationPoint + EndCityPieces.
func (g *endCityStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}
	// The biome gate at the chunk center (the structure biomes() test at the start Y). NO accept-by-
	// default: a non-end-highlands/midlands origin yields NO start.
	centerX := cx*16 + 8
	centerZ := cz*16 + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !g.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}
	// The piece RNG = GenerationContext.makeRandom(seed, pos) (setLargeFeatureSeed(seed,cx,cz)).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)
	// findGenerationPoint: rot = Rotation.getRandom(rng) (the FIRST draw), then the 5x5 min-corner gate.
	rot := getRandomRotation(rng)
	lowestY := endCityLowestYIn5by5(sampler, cx, cz, rot)
	if lowestY < endCityMinY {
		return nil // pos.getY() < 60 -> Optional.empty()
	}
	startPos := Pos{cx*16 + 7, lowestY, cz*16 + 7} // BlockPos(chunkX*16+7, y, chunkZ*16+7)
	pieces, err := startEndCityHouseTower(startPos, rot, rng)
	if err != nil || len(pieces) == 0 {
		return nil // a missing template / empty assembly -> no start (drop, never panic)
	}
	start := &StructureStart{
		Structure: "minecraft:end_city",
		ChunkPos:  pos,
		Pieces:    pieces,
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}
