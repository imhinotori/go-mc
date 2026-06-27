package structure

import (
	"fmt"

	"github.com/imhinotori/sulfur/level"
)

// start_nbt.go — STRUCT-POLISH-04 (20-03): StructureStart.createTag / loadStaticStart.
//
// A literal 1:1 port of the vanilla StructureStart NBT serialization (verified vs
// temp/cache/26.2-inner.jar):
//
//   - StructureStart.createTag(ctx, chunkPos) (javap -c): a FLAT compound. If !isValid()
//     (no pieces) it writes ONLY {id: "INVALID"} and returns. Else it writes
//     {id: <structure id string>, ChunkX: int, ChunkZ: int, references: int,
//     Children: PiecesContainer.save() (a ListTag of each piece's createTag)}.
//   - StructureStart.loadStaticStart(ctx, tag, seed) (javap -c): reads id via
//     getStringOr("id",""); "INVALID" -> INVALID_START (an empty start). Else reads
//     ChunkX/ChunkZ via getIntOr(...,0), references via getIntOr("references",0), and
//     Children via getListOrEmpty("Children") -> PiecesContainer.load.
//
// Because StructureStarts are PURE over (seed, pos) (cache.go Assumption A6), this NBT form is
// a pure OPTIMIZATION: LoadStaticStart(CreateTag(s)) MUST equal s, and a loaded start MUST equal
// what ComputeStarts(seed,pos) would produce (the recompute-coherence gate, Pitfall 4 / T-20-09).
// recompute is always the source of truth — a garbled tag falls back to recompute, never panics.
//
// Source: javap -c net.minecraft.world.level.levelgen.structure.StructureStart.createTag /
// loadStaticStart + pieces.PiecesContainer.save/load.

// invalidStartID ports StructureStart.INVALID_START_ID — the id an empty start writes (and the
// sentinel loadStaticStart maps back to an empty start).
const invalidStartID = "INVALID"

// maxChildrenPieces bounds the Children list on decode (T-20-08 / V5 DoS): a garbled huge count
// must error -> recompute, not allocate unboundedly. Real structures stay far under this — the
// largest (the village jigsaw) is capped at 1000 pieces by the placer (16-02 decision), and the
// recursive mineshaft/stronghold are bounded by their genDepth caps. 4096 is a generous ceiling
// that no faithful start reaches but a corrupt array cannot exceed without erroring.
const maxChildrenPieces = 4096

// startTag is the on-NBT form of a StructureStart: the flat {id, ChunkX, ChunkZ, references,
// Children} compound (StructureStart.createTag). An INVALID start carries only id="INVALID"
// (the other fields stay zero / Children nil) — CreateTag writes nothing else, and LoadStaticStart
// keys solely off id, so the zero values are never read for an invalid start.
type startTag struct {
	ID         string    `nbt:"id"`
	ChunkX     int32     `nbt:"ChunkX"`
	ChunkZ     int32     `nbt:"ChunkZ"`
	References int32     `nbt:"references"`
	Children   []pieceTag `nbt:"Children"`
}

// CreateTag ports StructureStart.createTag: the flat compound. An INVALID (empty) start writes
// only {id:"INVALID"}; a valid start writes id + ChunkX/Z + references + the Children piece list
// (PiecesContainer.save). The pos arg is the OWNING chunk (the createTag(ctx, chunkPos) param) —
// it is the start's anchor, written as ChunkX/ChunkZ.
func (s *StructureStart) CreateTag(pos level.ChunkPos) (startTag, error) {
	if !s.IsValid() {
		// !isValid() -> {id:"INVALID"} only (the createTag early-return at bytecode offset 48).
		return startTag{ID: invalidStartID}, nil
	}
	children := make([]pieceTag, 0, len(s.Pieces))
	for _, pc := range s.Pieces {
		pt, err := SavePiece(pc)
		if err != nil {
			return startTag{}, err
		}
		children = append(children, pt)
	}
	return startTag{
		ID:         s.Structure,
		ChunkX:     pos[0],
		ChunkZ:     pos[1],
		References: int32(s.References),
		Children:   children,
	}, nil
}

// LoadStaticStart ports StructureStart.loadStaticStart: read id; "INVALID" -> an empty start
// (the INVALID_START sentinel). Else rebuild {Structure, ChunkPos, References, Pieces} from the
// tag, loading each Children piece via LoadPiece. A bad Children count or an unknown/garbled
// piece is surfaced as an error so the caller recomputes (recompute is the source of truth,
// T-20-07) — never a panic, never a silent partial start.
//
// The returned start's BBox is recomputed from the loaded pieces (RecomputeBBox) — the jar caches
// the bbox lazily via getBoundingBox(); here we materialize it eagerly so the loaded start is
// field-complete and equal to a freshly-generated one.
func LoadStaticStart(tag startTag) (*StructureStart, error) {
	if tag.ID == invalidStartID {
		// INVALID_START — an empty, invalid start (loadStaticStart returns the shared sentinel).
		return &StructureStart{}, nil
	}
	if len(tag.Children) > maxChildrenPieces {
		return nil, fmt.Errorf("structure: LoadStaticStart: Children count %d exceeds bound %d", len(tag.Children), maxChildrenPieces)
	}
	pieces := make([]Piece, 0, len(tag.Children))
	for _, ct := range tag.Children {
		pc, err := LoadPiece(ct)
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, pc)
	}
	start := &StructureStart{
		Structure:  tag.ID,
		ChunkPos:   level.ChunkPos{tag.ChunkX, tag.ChunkZ},
		Pieces:     pieces,
		References: int(tag.References),
	}
	start.RecomputeBBox()
	return start, nil
}
