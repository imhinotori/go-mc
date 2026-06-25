package structure

// template.go — STRUCT-05 part 1: the RUNTIME .nbt StructureTemplate parser + placer.
//
// This GENERALIZES the 14-03 igloo OFFLINE-extraction shortcut (igloo_data.go baked
// the igloo .nbt palette/blocks into Go literals at build time) into a runtime parser:
// it reads the SAME schema (size / palette of {Name,Properties} / blocks of
// {pos,state,nbt} / entities) from the embedded .nbt bytes, and adds rotation/mirror/
// offset at place time + the block-replace processors villages need.
//
// Ported (idiomatic Go, no GPL paste) from CFR
// net.minecraft.world.level.levelgen.structure.templatesystem.StructureTemplate:
//   - load()                    -> ParseTemplate (the size/palette/blocks/entities tags)
//   - transform(BlockPos,...)   -> transformPos (mirror-then-rotate about a pivot)
//   - calculateRelativePosition -> calculateRelativePosition (transform about the
//                                  settings rotationPivot, default BlockPos.ZERO)
//   - placeInWorld(...)         -> PlaceInWorld (the place loop + processors + clip)
//   - getBoundingBox(...)       -> BoundingBoxAt (the rotated/mirrored extent)
//   - getJigsaws(...)           -> Jigsaws (the jigsaw-block extraction for 16-02)
//
// The rotation/mirror is applied AT PLACE TIME, never pre-baked (Pitfall #7). The
// palette resolves to StateIDs ONCE at load (resolveTemplateState, the igloo.go
// resolveIglooState pattern generalized) — an unknown block name FAILS LOUDLY (T-16-02).

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// Pos is a template/world block position (a bare integer triple). The placer works in
// these rather than introducing a new vector type — the structure package already keeps
// everything in int coords.
type Pos struct{ X, Y, Z int }

// jigsawBlockName is the palette name of a jigsaw block — replaced WITH AIR at place
// time per LegacySinglePoolElement (a load-bearing village behavior). structure_void +
// the structure_block DATA markers are likewise skipped (vanilla handleDataMarker).
const (
	jigsawBlockName    = "minecraft:jigsaw"
	structureVoidName  = "minecraft:structure_void"
	structureBlockName = "minecraft:structure_block"
)

// templateBlock is one placed cell: a template-LOCAL position, the palette index, and
// (optionally) the block-entity NBT (jigsaw blocks carry their pool/target/joint here).
// Mirrors the igloo_data.go iglooBlock shape, populated at RUNTIME from the parsed .nbt.
type templateBlock struct {
	Pos   [3]int         `nbt:"pos"`
	State int            `nbt:"state"`
	NBT   nbt.RawMessage `nbt:"nbt"`
}

// rawEntity is a template entity entry. Entities are STORED but NOT placed (entities are
// a v3 deferral, matching the 14/15 precedent) — kept so 16-02/v3 can find them.
type rawEntity struct {
	Pos      []float64      `nbt:"pos"`
	BlockPos [3]int         `nbt:"blockPos"`
	NBT      nbt.RawMessage `nbt:"nbt"`
}

// rawTemplate is the on-disk .nbt schema (CFR StructureTemplate.load tag constants):
// size (3-int list), palette OR palettes (village uses single `palette`), blocks, entities.
type rawTemplate struct {
	Size     []int           `nbt:"size"`
	Palette  []block.State   `nbt:"palette"`
	Palettes [][]block.State `nbt:"palettes"`
	Blocks   []templateBlock `nbt:"blocks"`
	Entities []rawEntity     `nbt:"entities"`
}

// StructureTemplate is the parsed + palette-resolved template: the size, the palette
// resolved to StateIDs once (indexed by palette index), the block list (LOCAL coords +
// palette index + optional block-entity nbt), and the stored-but-unplaced entities.
type StructureTemplate struct {
	Size     [3]int
	palette  []block.StateID // palette index -> resolved StateID (resolved once)
	names    []string        // palette index -> block name (for jigsaw/void/marker checks)
	blocks   []templateBlock
	entities []rawEntity
}

// LoadTemplate resolves a template id (e.g. "village/plains/houses/plains_small_house_1")
// from the embedded .nbt FS and parses it. The id is the path under structure/ without
// the .nbt suffix.
func LoadTemplate(id string) (*StructureTemplate, error) {
	gz, err := data.StructureTemplateNBT(id)
	if err != nil {
		return nil, fmt.Errorf("structure: load template %q: %w", id, err)
	}
	return ParseTemplate(gz)
}

// ParseTemplate gunzips + NBT-decodes the .nbt bytes into a StructureTemplate, resolving
// the palette to StateIDs once. CFR StructureTemplate.load. An unknown palette block name
// is a FATAL build-data error (T-16-02): a silent air-fill would corrupt village geometry.
func ParseTemplate(gz []byte) (*StructureTemplate, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("structure: template gunzip: %w", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("structure: template read: %w", err)
	}
	var rt rawTemplate
	if _, err := nbt.NewDecoder(bytes.NewReader(raw)).Decode(&rt); err != nil {
		return nil, fmt.Errorf("structure: template nbt decode: %w", err)
	}
	if len(rt.Size) != 3 {
		return nil, fmt.Errorf("structure: template size = %v; want 3 ints", rt.Size)
	}

	// Village templates ship a single `palette`. If only `palettes` is present (a
	// variant template), pick palette[0] — villages never use variant palettes, but
	// the fallback keeps the parser robust to the broader schema.
	pal := rt.Palette
	if len(pal) == 0 && len(rt.Palettes) > 0 {
		pal = rt.Palettes[0]
	}

	t := &StructureTemplate{
		Size:     [3]int{rt.Size[0], rt.Size[1], rt.Size[2]},
		palette:  make([]block.StateID, len(pal)),
		names:    make([]string, len(pal)),
		blocks:   rt.Blocks,
		entities: rt.Entities,
	}
	for i, ps := range pal {
		t.names[i] = ps.Name
		id, err := resolveTemplateState(ps)
		if err != nil {
			return nil, fmt.Errorf("structure: template palette[%d] %q: %w", i, ps.Name, err)
		}
		t.palette[i] = id
	}
	return t, nil
}

// resolveTemplateState resolves a palette entry (Name + bare Properties compound) to its
// StateID — the igloo.go resolveIglooState pattern, generalized to runtime. The .nbt
// palette stores Properties as the BARE compound payload (string-valued props), which is
// exactly what block.State.Properties wants; the nbt decoder already captured it as a
// RawMessage, so no header-strip is needed (unlike igloo.go, which re-marshalled from a
// Go map). An unknown block name returns an error (FAIL LOUD, T-16-02).
//
// Source: CFR StructureTemplate palette load + StructureTemplate.Palette state resolve.
func resolveTemplateState(ps block.State) (block.StateID, error) {
	b, err := ps.Block()
	if err != nil {
		return 0, err
	}
	id, ok := block.ToStateID[b]
	if !ok {
		return 0, fmt.Errorf("block %q has no state id", ps.Name)
	}
	return id, nil
}

// transformPos ports StructureTemplate.transform(BlockPos, mirror, rotation, pivot): the
// template-PIVOT transform — mirror is applied FIRST (LEFT_RIGHT flips Z, FRONT_BACK
// flips X), then the rotation rotates about the (px,pz) pivot. The formulas are the exact
// jar bytecode (the $SwitchMap maps CCW90->1, CW90->2, CW180->3; verified via javap -c):
//
//	CCW90 : ( px - pz + z, y, px + pz - x )
//	CW90  : ( px + pz - z, y, pz - px + x )
//	CW180 : ( 2*px - x,    y, 2*pz - z     )
//	NONE  : ( x, y, z )
//
// This is DISTINCT from piece.go's getWorldX/Y/Z (the scattered-piece orientation
// transform). The .nbt placer needs THIS pivot rotation, never that one.
//
// Source: CFR StructureTemplate.transform(BlockPos, Mirror, Rotation, BlockPos).
func transformPos(x, y, z int, mir Mirror, rot Rotation, pivotX, pivotZ int) (int, int, int) {
	// Mirror first.
	switch mir {
	case MirrorLeftRight:
		z = -z
	case MirrorFrontBack:
		x = -x
	}
	px, pz := pivotX, pivotZ
	switch rot {
	case RotCounterclockwise90:
		return px - pz + z, y, px + pz - x
	case RotClockwise90:
		return px + pz - z, y, pz - px + x
	case RotClockwise180:
		return 2*px - x, y, 2*pz - z
	default: // RotNone
		return x, y, z
	}
}

// calculateRelativePosition ports StructureTemplate.calculateRelativePosition(settings,
// pos) = transform(pos, mirror, rotation, settings.getRotationPivot()). The default
// StructurePlaceSettings rotationPivot is BlockPos.ZERO (verified via javap), so a caller
// that does not set a pivot passes (0,0). 16-02's jigsaw placer sets the pivot.
//
// Source: CFR StructureTemplate.calculateRelativePosition.
func calculateRelativePosition(localX, localY, localZ int, rot Rotation, mir Mirror, pivotX, pivotZ int) (int, int, int) {
	return transformPos(localX, localY, localZ, mir, rot, pivotX, pivotZ)
}

// TemplateProcessor is the place-time block-replace hook (CFR StructureProcessor). It is
// declared in template_processor.go; PlaceInWorld runs each in order over every cell.

// PlaceInWorld ports StructureTemplate.placeInWorld + StructurePlaceSettings: for each
// template block, compute its world position (calculateRelativePosition about the pivot,
// then translate by origin), clip to the writable box, transform its STATE (mirror-then-
// rotate via the 14-02 transformState — REUSE), run the processor chain, and SetBlock.
//
// LegacySinglePoolElement semantics: a jigsaw block is replaced WITH AIR; structure_void
// and the structure_block DATA markers are skipped (handleDataMarker). The pivot defaults
// to BlockPos.ZERO (pivotX=pivotZ=0) for the standalone placer; 16-02 supplies a pivot.
//
// Source: CFR StructureTemplate.placeInWorld (the processBlockInfos loop) +
// StructurePlaceSettings (getMirror/getRotation/getRotationPivot/getProcessors).
func (t *StructureTemplate) PlaceInWorld(view WorldGenView, origin Pos, rot Rotation, mir Mirror, pivotX, pivotZ int, processors []TemplateProcessor, box BoundingBox, rng levelgen.RandomSource) {
	for _, blk := range t.blocks {
		name := t.names[blk.State]

		// structure_void + structure_block DATA markers are not visible blocks: skipped.
		if name == structureVoidName || name == structureBlockName {
			continue
		}

		// World position: pivot rotation, then translate by origin.
		rx, ry, rz := calculateRelativePosition(blk.Pos[0], blk.Pos[1], blk.Pos[2], rot, mir, pivotX, pivotZ)
		wx, wy, wz := origin.X+rx, origin.Y+ry, origin.Z+rz
		if !box.IsInside(wx, wy, wz) {
			continue // the cross-chunk clip (Pitfall #2)
		}

		// Jigsaw block -> air (LegacySinglePoolElement). The jigsaw's pool/target lives in
		// its block-entity nbt, consumed by 16-02 via Jigsaws; here it leaves AIR.
		var st block.StateID
		if name == jigsawBlockName {
			st = stateAir
		} else {
			// Transform the STATE: mirror-then-rotate the facing (REUSE transformState).
			st = transformState(t.palette[blk.State], mir, rot)
		}

		// Run the processor chain (each may replace or skip the block).
		keep := true
		for _, proc := range processors {
			var ns block.StateID
			ns, keep = proc.Process(view, wx, wy, wz, st, rng)
			if !keep {
				break
			}
			st = ns
		}
		if !keep {
			continue
		}

		view.SetBlock(wx, wy, wz, st)
	}
}

// BoundingBoxAt ports StructureTemplate.getBoundingBox(offset, rotation, pivot, mirror,
// size): the rotated/mirrored WORLD extent. corner1 = transform(ZERO), corner2 =
// transform(size-1), the box is fromCorners(corner1, corner2) moved by offset. 16-02's
// collision check consumes this.
//
// Source: CFR StructureTemplate.getBoundingBox(BlockPos, Rotation, BlockPos, Mirror, Vec3i).
func (t *StructureTemplate) BoundingBoxAt(origin Pos, rot Rotation, mir Mirror, pivotX, pivotZ int) BoundingBox {
	c1x, c1y, c1z := transformPos(0, 0, 0, mir, rot, pivotX, pivotZ)
	c2x, c2y, c2z := transformPos(t.Size[0]-1, t.Size[1]-1, t.Size[2]-1, mir, rot, pivotX, pivotZ)
	bb := BoundingBox{
		MinX: min(c1x, c2x), MinY: min(c1y, c2y), MinZ: min(c1z, c2z),
		MaxX: max(c1x, c2x), MaxY: max(c1y, c2y), MaxZ: max(c1z, c2z),
	}
	bb.MinX += origin.X
	bb.MinY += origin.Y
	bb.MinZ += origin.Z
	bb.MaxX += origin.X
	bb.MaxY += origin.Y
	bb.MaxZ += origin.Z
	return bb
}

// JigsawBlockInfo is one jigsaw connector extracted from a placed template (CFR
// StructureTemplate.JigsawBlockInfo / StructurePoolElement.getShuffledJigsawBlocks): the
// jigsaw's WORLD position after rotation, its source pool's name, the target pool it
// connects to, the target jigsaw name it joins, the joint type, and its front/top
// orientation (from the jigsaw block-entity nbt). 16-02 consumes this to grow the jigsaw.
type JigsawBlockInfo struct {
	WorldPos    Pos
	Name        string // this jigsaw's name
	Pool        string // the target pool to expand into
	Target      string // the target jigsaw name to join against
	FinalState  string // the block to leave once connected (LegacySinglePool leaves air)
	Joint       string // "aligned" | "rollable"
	FrontFacing block.Direction
}

// jigsawNBT is the jigsaw block-entity nbt schema (CFR JigsawBlockEntity tags).
type jigsawNBT struct {
	Name       string `nbt:"name"`
	Pool       string `nbt:"pool"`
	Target     string `nbt:"target"`
	FinalState string `nbt:"final_state"`
	Joint      string `nbt:"joint"`
}

// Jigsaws ports StructureTemplate.getJigsaws: extract every jigsaw block's world position
// (after the pivot rotation + origin translate), its pool/target/joint from the block-
// entity nbt, and its oriented front face. The orientation prop on the jigsaw palette
// state gives the front facing; the rotation rotates it. 16-02 consumes these connectors.
//
// Source: CFR StructureTemplate.getJigsaws + JigsawBlockEntity.
func (t *StructureTemplate) Jigsaws(origin Pos, rot Rotation, mir Mirror, pivotX, pivotZ int) ([]JigsawBlockInfo, error) {
	var out []JigsawBlockInfo
	for _, blk := range t.blocks {
		if t.names[blk.State] != jigsawBlockName {
			continue
		}
		rx, ry, rz := calculateRelativePosition(blk.Pos[0], blk.Pos[1], blk.Pos[2], rot, mir, pivotX, pivotZ)
		info := JigsawBlockInfo{
			WorldPos: Pos{origin.X + rx, origin.Y + ry, origin.Z + rz},
		}
		if blk.NBT.Type == nbt.TagCompound {
			var jn jigsawNBT
			if err := blk.NBT.Unmarshal(&jn); err != nil {
				return nil, fmt.Errorf("structure: jigsaw block-entity nbt: %w", err)
			}
			info.Name = jn.Name
			info.Pool = jn.Pool
			info.Target = jn.Target
			info.FinalState = jn.FinalState
			info.Joint = jn.Joint
		}
		// The jigsaw's front face = the orientation prop on its palette state, rotated.
		// The orientation prop (e.g. "north_up") encodes a front direction; we derive the
		// front from the resolved block's Jigsaw orientation and rotate it. 16-02 uses the
		// front face to align the next pool element. The base facing is read from the block.
		front := jigsawFrontFacing(t.palette[blk.State])
		info.FrontFacing = rot.rotateDirection(mir.mirrorDirection(front))
		out = append(out, info)
	}
	return out, nil
}

// jigsawOrientationFront maps a jigsaw FrontAndTop orientation to its FRONT direction
// (the connector's facing). FrontAndTop encodes front_top (e.g. north_up = front North,
// top Up; down_east = front Down, top East). 16-02 aligns the next pool element to this
// front face.
//
// Source: CFR net.minecraft.core.FrontAndTop.front().
func jigsawOrientationFront(o block.FrontAndTop) block.Direction {
	switch o {
	case block.DownEast, block.DownNorth, block.DownSouth, block.DownWest:
		return block.Down
	case block.UpEast, block.UpNorth, block.UpSouth, block.UpWest:
		return block.Up
	case block.WestUp:
		return block.West
	case block.EastUp:
		return block.East
	case block.NorthUp:
		return block.North
	case block.SouthUp:
		return block.South
	}
	return block.North
}

// jigsawFrontFacing returns the front-facing direction encoded in a jigsaw block's
// orientation property. The jigsaw orientation is a FrontAndTop (e.g. north_up); the
// FRONT component is the connector's facing. Defaults to North if the state is not a
// jigsaw (defensive — Jigsaws only calls this for jigsaw palette states).
func jigsawFrontFacing(st block.StateID) block.Direction {
	if st < 0 || int(st) >= len(block.StateList) {
		return block.North
	}
	if jb, ok := block.StateList[st].(block.Jigsaw); ok {
		return jigsawOrientationFront(jb.Orientation)
	}
	return block.North
}
