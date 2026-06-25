package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// igloo placement constants (cross-checked vs the embedded structure_set igloos.json +
// has_structure/igloo tag): salt 14357618, spacing 32, separation 8, LINEAR spread; biome
// allow-set {snowy_taiga, snowy_plains, snowy_slopes}.
const (
	iglooSalt       = 14357618
	iglooSpacing    = 32
	iglooSeparation = 8
	iglooFloor      = 64

	// iglooBasementLoot is the chest loot-table id (BuiltInLootTables.IGLOO_CHEST_CHEST in
	// the laboratory). LOOT DEFERRED v3 (the chest block is placed by the template; no loot is
	// rolled). Recorded for the v3 resolver.
	iglooBasementLoot = "minecraft:chests/igloo_chest"
)

// iglooBasementOffset / iglooLadderOffset are the per-piece OFFSETS (javap -c IglooPieces
// OFFSETS static map): the igloo top dome is at (0,0,0) relative to the structure origin; the
// ladder (middle) at (2,-3,4); the laboratory (bottom basement) at (0,-3,-2). These translate
// each template's local frame into the shared structure-local frame.
var (
	iglooTopOffset      = [3]int{0, 0, 0}
	iglooLadderOffset   = [3]int{2, -3, 4}
	iglooBasementOffset = [3]int{0, -3, -2}
)

// iglooStartGen is the igloo StartGenerator. Like the swamp hut, IglooStructure extends the
// base Structure (onTopOfChunkCenter, NO sea-level gate) — only the biome gate + surface
// project. IglooPieces.addPieces then assembles the dome ALWAYS + the ladder+basement
// PROBABILISTICALLY (nextDouble < 0.5) via the addChildren multi-piece hook.
//
// Source: javap -c IglooStructure.findGenerationPoint + IglooPieces.addPieces.
type iglooStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewIglooStartGen builds the generator from the embedded structure_set + biome tag.
func NewIglooStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:igloos")
	if err != nil {
		return nil, err
	}
	allow, err := HasStructureBiomes("igloo")
	if err != nil {
		return nil, err
	}
	return &iglooStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// GenerateStarts ports IglooStructure's start decision for chunk pos (pure over (seed,pos)):
// isStructureChunk gate, the chunk-center biome gate (snowy allow-set, no accept-by-default),
// the SampleSurfaceY project, and IglooPieces.addPieces — the first use of the 14-02 multi-
// piece addChildren hook: the dome IglooPiece is ALWAYS added; with probability nextDouble<0.5
// a ladder + a basement (laboratory) IglooPiece are added too (a nextInt(8) draw follows for
// the basement, part of the determinism contract). The pieces are assembled into ONE start.
func (g *iglooStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	minBlockX := cx * 16
	minBlockZ := cz * 16

	// Biome gate at chunk-center: a non-snowy origin yields NO start (no accept-by-default).
	centerX := minBlockX + 8
	centerZ := minBlockZ + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !g.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}

	// The piece RNG = GenerationContext.makeRandom(seed, pos).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// The igloo structure origin: onTopOfChunkCenter places the origin at the chunk-center
	// surface (the dome's pivot sits there). We anchor the structure-local frame at
	// (centerX, surfaceY, centerZ); the offsets translate each piece into world coords.
	originX := centerX
	originZ := centerZ
	originY := centerSurfaceY

	// addPieces draw order (jar-exact): a random Rotation is drawn FIRST (in generatePieces, by
	// Rotation.getRandom(random)), THEN nextDouble() for the basement, THEN nextInt(8) for the
	// basement ladder length. We draw in that exact order so the placement is RNG-faithful.
	rot := getRandomHorizontalDirection(rng) // the structure rotation (drawn as a horizontal dir)

	var pieces []Piece
	// The dome is ALWAYS present.
	dome := newIglooPiece(&iglooTop, originX, originY, originZ, iglooTopOffset, rot)
	hasBasement := rng.NextDouble() < 0.5
	if hasBasement {
		_ = rng.NextIntN(8) // the basement ladder-length draw (geometry uses the fixed template; the draw is part of the determinism contract)
		basement := newIglooPiece(&iglooBottom, originX, originY, originZ, iglooBasementOffset, rot)
		ladder := newIglooPiece(&iglooMiddle, originX, originY, originZ, iglooLadderOffset, rot)
		// addChildren (the 14-02 multi-piece hook): the dome is the parent; the ladder +
		// basement are its children (a fixed 1-3 piece set, NOT recursion — Phase 15 owns that).
		pieces = append(pieces, dome, basement, ladder)
	} else {
		pieces = append(pieces, dome)
	}

	start := &StructureStart{
		Structure: "minecraft:igloo",
		ChunkPos:  pos,
		Pieces:    pieces,
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// IglooPiece is one placed igloo template (dome / ladder / laboratory). It holds the resolved
// palette + the template block list + the structure-local placement, and writes each block via
// the 14-02 placeBlock (clipped to the chunk writable box + orientation-transformed). The
// geometry is the offline-extracted .nbt template (igloo_data.go), so there is 0 .nbt at
// runtime.
//
// Source: javap -c IglooPieces$IglooPiece (TemplateStructurePiece placing the igloo/* template
// with the OFFSETS translation + the structure rotation).
type IglooPiece struct {
	StructurePiece
	tmpl   *iglooTemplate
	states []block.StateID // palette index -> resolved StateID
	offset [3]int          // structure-local offset of this template's local origin
	// LootChests records the basement chest (loot deferred v3).
	LootChests []LootChest
}

// newIglooPiece resolves the template palette to StateIDs once + computes the world bbox of the
// placed template (anchored at the structure origin + the per-piece offset, oriented). The bbox
// is the rotated template footprint at world coords.
func newIglooPiece(tmpl *iglooTemplate, originX, originY, originZ int, offset [3]int, rot block.Direction) *IglooPiece {
	p := &IglooPiece{tmpl: tmpl, offset: offset}
	p.states = make([]block.StateID, len(tmpl.palette))
	for i, ps := range tmpl.palette {
		p.states[i] = resolveIglooState(ps)
	}
	// The placed template spans [origin+offset .. origin+offset+size-1] before rotation; under
	// a horizontal rotation the X/Z footprint may swap. The bbox is anchored so getWorldX/Y/Z
	// maps the template-local coords into world. We build a Z-or-X-facing bbox like the
	// scattered pieces (makeScatteredBoundingBox) so the orientation transform is consistent
	// with the 14-02 machinery.
	baseX := originX + offset[0]
	baseY := originY + offset[1]
	baseZ := originZ + offset[2]
	p.bbox = makeScatteredBoundingBox(baseX, baseY, baseZ, rot, tmpl.sizeX, tmpl.sizeY, tmpl.sizeZ)
	p.setOrientation(rot, true)
	return p
}

// resolveIglooState resolves a palette (name, props) to its StateID via block.State (the same
// name+properties -> Block resolver the chunk loader uses), then block.ToStateID. A missing
// state is a build-data bug (panic, matching the other generators' resolves).
func resolveIglooState(ps iglooState) block.StateID {
	st := block.State{Name: ps.name}
	if len(ps.props) > 0 {
		props := make(map[string]string, len(ps.props))
		for _, pr := range ps.props {
			props[pr.key] = pr.val
		}
		data, err := nbt.Marshal(props)
		if err != nil {
			panic("structure: igloo prop marshal " + ps.name + ": " + err.Error())
		}
		// nbt.Marshal emits a full document: [tag 0x0A][nameLen u16 = 0][compound payload].
		// block.State.Properties wants the bare compound PAYLOAD (no root tag/name header), so
		// strip the 3-byte header. (The chunk loader's block_states.nbt Properties are stored
		// the same way — as the raw compound payload.)
		st.Properties = nbt.RawMessage{Type: nbt.TagCompound, Data: data[3:]}
	}
	b, err := st.Block()
	if err != nil {
		panic("structure: igloo state resolve " + ps.name + ": " + err.Error())
	}
	id, ok := block.ToStateID[b]
	if !ok {
		panic("structure: igloo block has no state id: " + ps.name)
	}
	return id
}

// PostProcess writes the template blocks into the chunk writable box, each via placeBlock
// (clipped + orientation-transformed). The chest data marker's loot + the villager/zombie-
// villager entity markers are DEFERRED v3; the chest BLOCK + the visible lab geometry are
// placed. The igloo template carries no per-block RNG draw (unlike the jungle moss selector),
// so the rng arg is unused here — the basement-present probability was drawn at START time.
func (p *IglooPiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, _ levelgen.RandomSource) {
	for _, blk := range p.tmpl.blocks {
		st := p.states[blk.state]
		// Template coords are local to the template; apply the per-piece offset is already
		// baked into the bbox anchor (newIglooPiece), so placeBlock's getWorldX/Y/Z maps the
		// template-local (x,y,z) to world via the bbox + orientation.
		if isIglooChestState(p.tmpl, blk.state) {
			// Record the chest as a loot chest (loot deferred); the chest block is still placed.
			wx := p.getWorldX(blk.x, blk.z)
			wy := p.getWorldY(blk.y)
			wz := p.getWorldZ(blk.x, blk.z)
			if box.IsInside(wx, wy, wz) {
				p.LootChests = append(p.LootChests, LootChest{X: wx, Y: wy, Z: wz, LootTable: iglooBasementLoot})
			}
		}
		p.placeBlock(view, st, blk.x, blk.y, blk.z, box)
	}
}

// isIglooChestState reports whether a palette index is the basement chest (for loot tracking).
func isIglooChestState(tmpl *iglooTemplate, idx int) bool {
	return tmpl.palette[idx].name == "minecraft:chest"
}
