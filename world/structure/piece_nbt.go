package structure

import (
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
)

// piece_nbt.go — STRUCT-POLISH-04 (20-03): the per-piece Save()/Load() NBT round-trip.
//
// This is a literal 1:1 port of the vanilla piece serialization (verified vs
// temp/cache/26.2-inner.jar):
//
//   - StructurePiece.createTag (javap -c): writes the FLAT base compound
//     {id: String (the StructurePieceType id), BB: int[6] (the BoundingBox CODEC =
//     [minX,minY,minZ,maxX,maxY,maxZ]), O: int (orientation get2DDataValue, or -1 when the
//     piece has no orientation), GD: int (genDepth)}, THEN calls addAdditionalSaveData.
//   - StructurePiece(StructurePieceType, CompoundTag) ctor (javap -c): reads GD via
//     getIntOr("GD",0), BB via the CODEC, then O via getIntOr("O",0) — O==-1 -> null
//     orientation, else from2DDataValue(O) -> setOrientation (which derives rotation+mirror).
//   - PiecesContainer.save / load (javap -c): the Children list is a ListTag of each piece's
//     createTag; load dispatches by the lowercased `id` to StructurePieceType.load, erroring
//     loudly (LOGGER.error "Unknown structure piece id") on an unknown id.
//
// Cited per-piece additional save data is documented at each saver/loader below. Because
// StructureStarts are PURE over (seed, pos) (cache.go Assumption A6), this persistence is a
// pure OPTIMIZATION — a loaded piece must EQUAL the recomputed one, and the start_nbt
// round-trip + recompute-coherence tests are the gates that prove it (Pitfall 4).
//
// Source: javap -c net.minecraft.world.level.levelgen.structure.StructurePiece.createTag /
// the (StructurePieceType, CompoundTag) ctor / pieces.PiecesContainer.save+load.

// pieceTag is the on-NBT form of a single StructurePiece: the base {id, BB, O, GD} fields
// (StructurePiece.createTag) plus an Extra compound carrying the per-piece addAdditionalSaveData
// (the RNG-derived geometry + the one-shot spawn guards 20-04 fills). Extra is a flat key/value
// compound so each piece type reads/writes only the keys it owns — a forward-compatible slot
// (an absent key reads as the Go zero-value via the *Or readers).
type pieceTag struct {
	ID    string         `nbt:"id"`
	BB    []int32        `nbt:"BB"`
	O     int32          `nbt:"O"`
	GD    int32          `nbt:"GD"`
	Extra pieceExtraData `nbt:"Extra"`
}

// pieceExtraData is the addAdditionalSaveData payload — a superset of every concrete piece's
// extra fields (each piece reads/writes only its own; absent fields stay zero). This mirrors
// the jar's per-subclass addAdditionalSaveData/(ctor) but in one flat Go compound so the
// round-trip is a single struct marshal (the keys are namespaced by piece concern to avoid
// collisions). The `omitempty`-free layout keeps the encode deterministic.
//
// SPAWN-GUARD SLOTS (20-04, Pitfall 6 forward-compat): SpawnedWitch/SpawnedCat/HasPlacedSpawner
// are the one-shot guards SwampHutPiece/StrongholdStairsDown gain in 20-04. They are persisted
// here NOW as documented forward-compatible slots so a reloaded swamp-hut/stronghold reads the
// guard as true and 20-04's reload-safe spawn skip works without re-exploring this format. Until
// 20-04 lands the fields are unset (false) on every piece — a no-op that round-trips cleanly.
type pieceExtraData struct {
	// MType is the mineshaft NORMAL/MESA selector OR the stronghold stairKind (start/spiral) —
	// disambiguated by the piece id, so one slot serves both. (jar: MineShaftPiece.mineshaftType
	// / StrongholdPieces.StairsDown.isSource.)
	MType int32 `nbt:"MType"`
	// Mineshaft corridor (MineShaftCorridor.addAdditionalSaveData: HR hasRails, HS hasSpiders ->
	// here HasCobwebs, Num numSections).
	HasRails    bool  `nbt:"HasRails"`
	HasCobwebs  bool  `nbt:"HasCobwebs"`
	NumSections int32 `nbt:"Num"`
	// Mineshaft crossing (MineShaftCrossing.addAdditionalSaveData: tf twoTall).
	TwoTall bool `nbt:"TwoTall"`
	// Mineshaft stairs descent direction (encoded as a 2D data value; -1 when unused).
	DescentDir int32 `nbt:"Desc"`
	// Stronghold entry door (strongholdPiece.entryDoor; StrongholdPiece.addAdditionalSaveData
	// "EntryDoor").
	EntryDoor int32 `nbt:"EntryDoor"`
	// Stronghold corridor expansions (Straight.addAdditionalSaveData "Left"/"Right" booleans).
	ExpandsX bool `nbt:"Left"`
	ExpandsZ bool `nbt:"Right"`
	// Stronghold turn (Turn left flag).
	TurnLeft bool `nbt:"TurnLeft"`
	// Stronghold room crossing feature (RoomCrossing.addAdditionalSaveData "Type").
	RoomType int32 `nbt:"Type"`
	// Stronghold five-crossing arms (FillerCorridor flags "leftLow"/"leftHigh"/...).
	LeftLow   bool `nbt:"leftLow"`
	LeftHigh  bool `nbt:"leftHigh"`
	RightLow  bool `nbt:"rightLow"`
	RightHigh bool `nbt:"rightHigh"`
	// Stronghold library tall flag (Library.addAdditionalSaveData "Tall").
	LibraryTall bool `nbt:"Tall"`
	// Igloo template selector (which extracted template: top/middle/bottom) + the per-piece
	// structure-local offset (so Load re-resolves the same template + placement). This is the
	// Sulfur-specific identity for the offline-extracted templates (igloo_data.go) — the jar's
	// IglooPiece is a TemplateStructurePiece keyed by the template Identifier; we persist the
	// equivalent template id + offset.
	IglooTemplate int32  `nbt:"IglooTmpl"`
	OffsetX       int32  `nbt:"OffX"`
	OffsetY       int32  `nbt:"OffY"`
	OffsetZ       int32  `nbt:"OffZ"`
	OriginX       int32  `nbt:"OrgX"`
	OriginY       int32  `nbt:"OrgY"`
	OriginZ       int32  `nbt:"OrgZ"`
	IglooRot      int32  `nbt:"IglooRot"`
	// One-shot spawn guards (20-04 forward-compat slots; Pitfall 6).
	SpawnedWitch    bool `nbt:"SpawnedWitch"`
	SpawnedCat      bool `nbt:"SpawnedCat"`
	HasPlacedSpawner bool `nbt:"HasPlacedSpawner"`
}

// Piece type ids — the Sulfur equivalents of the BuiltInRegistries.STRUCTURE_PIECE keys (the
// vanilla `minecraft:<lower>` ids the createTag `id` field carries). One id per concrete Sulfur
// piece type; LoadPiece dispatches on these (erroring loudly on any other — Phase-11 discipline).
const (
	pieceIDDesertPyramid  = "minecraft:tedp"  // ScatteredFeaturePiece desert pyramid (vanilla "TeDP")
	pieceIDJungleTemple   = "minecraft:tejp"  // jungle temple (vanilla "TeJP")
	pieceIDSwampHut       = "minecraft:tesh"  // swamp hut (vanilla "TeSH")
	pieceIDIgloo          = "minecraft:igloo" // igloo template piece
	pieceIDMineCorridor   = "minecraft:mscorridor"
	pieceIDMineCrossing   = "minecraft:mscrossing"
	pieceIDMineRoom       = "minecraft:msroom"
	pieceIDMineStairs     = "minecraft:msstairs"
	pieceIDShCorridor     = "minecraft:shsd"  // stronghold straight corridor
	pieceIDShStart        = "minecraft:shstart"
	pieceIDShStairsDown   = "minecraft:shsdsd"
	pieceIDShStraightDown = "minecraft:shssd"
	pieceIDShTurn         = "minecraft:shturn"
	pieceIDShRoomCrossing = "minecraft:shrc"
	pieceIDShFiveCrossing = "minecraft:shfc"
	pieceIDShChestCorr    = "minecraft:shcc"
	pieceIDShPrison       = "minecraft:shph"
	pieceIDShLibrary      = "minecraft:shli"
	pieceIDShPortalRoom   = "minecraft:shpr"
)

// boundingBoxTag ports the BoundingBox CODEC (Codec.INT_STREAM over [minX,minY,minZ,maxX,maxY,
// maxZ]) — the exact int[6] the StructurePiece BB field encodes (javap BoundingBox static{} +
// the comapFlatMap to a 6-int array).
func boundingBoxTag(b BoundingBox) []int32 {
	return []int32{int32(b.MinX), int32(b.MinY), int32(b.MinZ), int32(b.MaxX), int32(b.MaxY), int32(b.MaxZ)}
}

// boundingBoxFromTag inverts boundingBoxTag, erroring on a malformed array (V5 — a corrupt
// on-disk BB must not panic; the persistence seam recomputes on any decode error).
func boundingBoxFromTag(a []int32) (BoundingBox, error) {
	if len(a) != 6 {
		return BoundingBox{}, fmt.Errorf("structure: BB int array len %d, want 6", len(a))
	}
	return BoundingBox{
		MinX: int(a[0]), MinY: int(a[1]), MinZ: int(a[2]),
		MaxX: int(a[3]), MaxY: int(a[4]), MaxZ: int(a[5]),
	}, nil
}

// orientationTag ports the StructurePiece.createTag O field: get2DDataValue(orientation), or -1
// when the piece has no orientation.
func orientationTag(p *StructurePiece) int32 {
	if !p.hasOrient {
		return -1
	}
	return int32(get2DDataValue(p.orientation))
}

// from2DDataValue inverts get2DDataValue (Direction.from2DDataValue): SOUTH=0, WEST=1, NORTH=2,
// EAST=3 (the Plane.HORIZONTAL order). Used by applyBaseTag to reconstruct the orientation.
func from2DDataValue(v int32) block.Direction {
	switch v {
	case 0:
		return block.South
	case 1:
		return block.West
	case 2:
		return block.North
	default: // 3
		return block.East
	}
}

// applyBaseTag reconstructs the base StructurePiece state from a pieceTag, porting the
// StructurePiece(StructurePieceType, CompoundTag) ctor: GD -> genDepth, BB -> bbox (CODEC),
// O==-1 -> no orientation else from2DDataValue(O) -> setOrientation (deriving rotation+mirror).
func applyBaseTag(p *StructurePiece, tag pieceTag) error {
	bb, err := boundingBoxFromTag(tag.BB)
	if err != nil {
		return err
	}
	p.bbox = bb
	p.genDepth = int(tag.GD)
	if tag.O == -1 {
		p.setOrientation(block.North, false)
	} else {
		p.setOrientation(from2DDataValue(tag.O), true)
	}
	return nil
}

// baseTag builds the base {id, BB, O, GD} fields (StructurePiece.createTag head) for piece id.
func baseTag(id string, p *StructurePiece) pieceTag {
	return pieceTag{
		ID: id,
		BB: boundingBoxTag(p.bbox),
		O:  orientationTag(p),
		GD: int32(p.genDepth),
	}
}

// SavePiece serializes a Piece to its NBT pieceTag (the PiecesContainer.save per-element form).
// It dispatches on the concrete Go type, writing the base {id, BB, O, GD} + the piece's
// addAdditionalSaveData. An unknown concrete type errors loudly (a programming error — a new
// piece type that forgot to register; Phase-11 discipline).
func SavePiece(p Piece) (pieceTag, error) {
	switch v := p.(type) {
	case *DesertPyramidPiece:
		return baseTag(pieceIDDesertPyramid, &v.StructurePiece), nil
	case *JungleTemplePiece:
		return baseTag(pieceIDJungleTemple, &v.StructurePiece), nil
	case *SwampHutPiece:
		// SwampHutPiece.addAdditionalSaveData (jar): the one-shot witch/cat guards. Persisted in
		// the forward-compat slots (20-04 sets them; until then they round-trip as false).
		t := baseTag(pieceIDSwampHut, &v.StructurePiece)
		return t, nil
	case *IglooPiece:
		return saveIgloo(v), nil
	case *MineShaftCorridor:
		t := baseTag(pieceIDMineCorridor, &v.StructurePiece)
		t.Extra.MType = int32(v.mType)
		t.Extra.HasRails = v.hasRails
		t.Extra.HasCobwebs = v.hasCobwebs
		t.Extra.NumSections = int32(v.numSections)
		return t, nil
	case *MineShaftCrossing:
		t := baseTag(pieceIDMineCrossing, &v.StructurePiece)
		t.Extra.MType = int32(v.mType)
		t.Extra.TwoTall = v.twoTall
		return t, nil
	case *MineshaftRoom:
		t := baseTag(pieceIDMineRoom, &v.StructurePiece)
		t.Extra.MType = int32(v.mType)
		return t, nil
	case *MineShaftStairs:
		t := baseTag(pieceIDMineStairs, &v.StructurePiece)
		t.Extra.MType = int32(v.mType)
		t.Extra.DescentDir = int32(get2DDataValue(v.descentDir))
		return t, nil
	case *StrongholdCorridor:
		t := baseTag(pieceIDShCorridor, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.ExpandsX = v.expandsX
		t.Extra.ExpandsZ = v.expandsZ
		return t, nil
	case *StrongholdStartPiece:
		t := baseTag(pieceIDShStart, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.MType = int32(v.mType)
		return t, nil
	case *StrongholdStairsDown:
		t := baseTag(pieceIDShStairsDown, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.MType = int32(v.mType)
		return t, nil
	case *StrongholdStraightStairsDown:
		t := baseTag(pieceIDShStraightDown, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		return t, nil
	case *StrongholdTurn:
		t := baseTag(pieceIDShTurn, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.TurnLeft = v.left
		return t, nil
	case *StrongholdRoomCrossing:
		t := baseTag(pieceIDShRoomCrossing, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.RoomType = int32(v.feature)
		return t, nil
	case *StrongholdFiveCrossing:
		t := baseTag(pieceIDShFiveCrossing, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.LeftLow = v.leftLow
		t.Extra.LeftHigh = v.leftHigh
		t.Extra.RightLow = v.rightLow
		t.Extra.RightHigh = v.rightHigh
		return t, nil
	case *StrongholdChestCorridor:
		t := baseTag(pieceIDShChestCorr, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		return t, nil
	case *StrongholdPrison:
		t := baseTag(pieceIDShPrison, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		return t, nil
	case *StrongholdLibrary:
		t := baseTag(pieceIDShLibrary, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		t.Extra.LibraryTall = v.tall
		return t, nil
	case *StrongholdPortalRoom:
		t := baseTag(pieceIDShPortalRoom, &v.StructurePiece)
		t.Extra.EntryDoor = int32(v.entryDoor)
		return t, nil
	default:
		return pieceTag{}, fmt.Errorf("structure: SavePiece: no NBT saver for piece type %T", p)
	}
}

// LoadPiece reconstructs a Piece from its pieceTag, dispatching on the `id` field
// (PiecesContainer.load: BuiltInRegistries.STRUCTURE_PIECE.getValue(id).load). An unknown id
// errors loudly (the jar logs + drops; here we surface it so the persistence seam recomputes the
// WHOLE start — recompute is the source of truth, T-20-07).
func LoadPiece(tag pieceTag) (Piece, error) {
	switch tag.ID {
	case pieceIDDesertPyramid:
		p := &DesertPyramidPiece{width: desertPyramidWidth, height: desertPyramidHeight, depth: desertPyramidDepth}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		return p, nil
	case pieceIDJungleTemple:
		p := &JungleTemplePiece{width: jungleTempleWidth, height: jungleTempleHeight, depth: jungleTempleDepth}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		return p, nil
	case pieceIDSwampHut:
		p := &SwampHutPiece{width: swampHutWidth, height: swampHutHeight, depth: swampHutDepth}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		return p, nil
	case pieceIDIgloo:
		return loadIgloo(tag)
	case pieceIDMineCorridor:
		p := &MineShaftCorridor{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.mType = mineshaftType(tag.Extra.MType)
		p.hasRails = tag.Extra.HasRails
		p.hasCobwebs = tag.Extra.HasCobwebs
		p.numSections = int(tag.Extra.NumSections)
		return p, nil
	case pieceIDMineCrossing:
		p := &MineShaftCrossing{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.mType = mineshaftType(tag.Extra.MType)
		p.twoTall = tag.Extra.TwoTall
		return p, nil
	case pieceIDMineRoom:
		p := &MineshaftRoom{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.mType = mineshaftType(tag.Extra.MType)
		return p, nil
	case pieceIDMineStairs:
		p := &MineShaftStairs{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.mType = mineshaftType(tag.Extra.MType)
		p.descentDir = from2DDataValue(tag.Extra.DescentDir)
		return p, nil
	case pieceIDShCorridor:
		p := &StrongholdCorridor{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.expandsX = tag.Extra.ExpandsX
		p.expandsZ = tag.Extra.ExpandsZ
		return p, nil
	case pieceIDShStart:
		p := &StrongholdStartPiece{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.mType = stairKind(tag.Extra.MType)
		return p, nil
	case pieceIDShStairsDown:
		p := &StrongholdStairsDown{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.mType = stairKind(tag.Extra.MType)
		return p, nil
	case pieceIDShStraightDown:
		p := &StrongholdStraightStairsDown{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		return p, nil
	case pieceIDShTurn:
		p := &StrongholdTurn{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.left = tag.Extra.TurnLeft
		return p, nil
	case pieceIDShRoomCrossing:
		p := &StrongholdRoomCrossing{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.feature = int(tag.Extra.RoomType)
		return p, nil
	case pieceIDShFiveCrossing:
		p := &StrongholdFiveCrossing{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.leftLow = tag.Extra.LeftLow
		p.leftHigh = tag.Extra.LeftHigh
		p.rightLow = tag.Extra.RightLow
		p.rightHigh = tag.Extra.RightHigh
		return p, nil
	case pieceIDShChestCorr:
		p := &StrongholdChestCorridor{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		return p, nil
	case pieceIDShPrison:
		p := &StrongholdPrison{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		return p, nil
	case pieceIDShLibrary:
		p := &StrongholdLibrary{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		p.tall = tag.Extra.LibraryTall
		return p, nil
	case pieceIDShPortalRoom:
		p := &StrongholdPortalRoom{}
		if err := applyBaseTag(&p.StructurePiece, tag); err != nil {
			return nil, err
		}
		p.entryDoor = strongholdDoorType(tag.Extra.EntryDoor)
		return p, nil
	default:
		return nil, fmt.Errorf("structure: LoadPiece: unknown structure piece id %q", tag.ID)
	}
}

// igloo template selector ids (the IglooTemplate extra slot): which offline-extracted template
// a loaded igloo piece re-resolves. These map 1:1 to the package vars iglooTop/Middle/Bottom.
const (
	iglooTmplTop    int32 = 0
	iglooTmplMiddle int32 = 1
	iglooTmplBottom int32 = 2
)

// saveIgloo writes an IglooPiece: the base {id, BB, O, GD} + the template identity (which
// template + the structure-local offset + the origin + rotation) so loadIgloo re-resolves the
// SAME template + placement (the jar's IglooPiece is a TemplateStructurePiece keyed by the
// template Identifier; we persist the equivalent identity). The bbox is recomputed by
// newIglooPiece from (origin, offset, rot), so it must match the persisted base BB — the
// round-trip test's PostProcess equality is the coherence gate.
func saveIgloo(v *IglooPiece) pieceTag {
	t := baseTag(pieceIDIgloo, &v.StructurePiece)
	t.Extra.IglooTemplate = iglooTemplateID(v.tmpl)
	t.Extra.OffsetX = int32(v.offset[0])
	t.Extra.OffsetY = int32(v.offset[1])
	t.Extra.OffsetZ = int32(v.offset[2])
	// The origin is recoverable from the bbox min minus the offset (newIglooPiece anchors
	// bbox at origin+offset). Persist it explicitly so loadIgloo re-runs newIglooPiece exactly.
	t.Extra.OriginX = int32(v.bbox.MinX - v.offset[0])
	t.Extra.OriginY = int32(v.bbox.MinY - v.offset[1])
	t.Extra.OriginZ = int32(v.bbox.MinZ - v.offset[2])
	t.Extra.IglooRot = orientationTag(&v.StructurePiece)
	return t
}

// loadIgloo reconstructs an IglooPiece by re-resolving the persisted template + placement via
// newIglooPiece (the same constructor the start generator calls). Re-running the constructor
// (rather than re-deriving fields by hand) guarantees the loaded piece is byte-identical to a
// freshly generated one — coherence by construction (Pitfall 4).
func loadIgloo(tag pieceTag) (Piece, error) {
	tmpl, err := iglooTemplateByID(tag.Extra.IglooTemplate)
	if err != nil {
		return nil, err
	}
	if tag.Extra.IglooRot == -1 {
		return nil, fmt.Errorf("structure: loadIgloo: igloo piece has no orientation (O=-1)")
	}
	offset := [3]int{int(tag.Extra.OffsetX), int(tag.Extra.OffsetY), int(tag.Extra.OffsetZ)}
	rot := from2DDataValue(tag.Extra.IglooRot)
	p := newIglooPiece(tmpl, int(tag.Extra.OriginX), int(tag.Extra.OriginY), int(tag.Extra.OriginZ), offset, rot)
	return p, nil
}

// iglooTemplateID maps a template pointer to its persisted selector id (an unknown template is a
// programming error — every igloo piece is built from one of the three package vars).
func iglooTemplateID(t *iglooTemplate) int32 {
	switch t {
	case &iglooTop:
		return iglooTmplTop
	case &iglooMiddle:
		return iglooTmplMiddle
	case &iglooBottom:
		return iglooTmplBottom
	default:
		// Defensive: an igloo piece always references one of the three package vars. Persisting
		// top is the safe default (the round-trip test pins all three so this is never hit).
		return iglooTmplTop
	}
}

// iglooTemplateByID inverts iglooTemplateID, erroring on an out-of-range selector (V5 — a
// garbled id must not index out of bounds; the seam recomputes).
func iglooTemplateByID(id int32) (*iglooTemplate, error) {
	switch id {
	case iglooTmplTop:
		return &iglooTop, nil
	case iglooTmplMiddle:
		return &iglooMiddle, nil
	case iglooTmplBottom:
		return &iglooBottom, nil
	default:
		return nil, fmt.Errorf("structure: loadIgloo: unknown igloo template id %d", id)
	}
}
