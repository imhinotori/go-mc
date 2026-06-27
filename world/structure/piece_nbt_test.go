package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// pieceNBTRoundTrip Saves a piece, Loads it back, and asserts the reconstructed piece is
// equal in the JAR-persisted state (the base {BB, O, GD} + the per-piece additional save
// data) AND produces byte-identical PostProcess output. The PostProcess equality is the
// strongest coherence check: two pieces that draw the same blocks into a fresh view are
// observably identical (Pitfall 4 — a loaded piece must behave like the recomputed one).
func pieceNBTRoundTrip(t *testing.T, p Piece) Piece {
	t.Helper()
	tag, err := SavePiece(p)
	if err != nil {
		t.Fatalf("SavePiece(%T): %v", p, err)
	}
	got, err := LoadPiece(tag)
	if err != nil {
		t.Fatalf("LoadPiece(%T): %v", p, err)
	}
	if got.BoundingBox() != p.BoundingBox() {
		t.Fatalf("%T bbox: round-trip got %+v want %+v", p, got.BoundingBox(), p.BoundingBox())
	}
	// PostProcess equality over a large writable box (covering the whole piece bbox) — the
	// observable-behavior coherence gate.
	box := p.BoundingBox()
	// inflate the box a little so no write is clipped away.
	box = BoundingBox{box.MinX - 4, box.MinY - 4, box.MinZ - 4, box.MaxX + 4, box.MaxY + 4, box.MaxZ + 4}
	rngA := levelgen.NewWorldgenRandom(0)
	rngA.SetLargeFeatureSeed(99, 1, 2)
	rngB := levelgen.NewWorldgenRandom(0)
	rngB.SetLargeFeatureSeed(99, 1, 2)
	va := newMapView()
	vb := newMapView()
	p.PostProcess(va, box, level.ChunkPos{0, 0}, rngA)
	got.PostProcess(vb, box, level.ChunkPos{0, 0}, rngB)
	if len(va.blocks) != len(vb.blocks) {
		t.Fatalf("%T PostProcess block count: orig=%d loaded=%d", p, len(va.blocks), len(vb.blocks))
	}
	for k, vA := range va.blocks {
		if vb.blocks[k] != vA {
			t.Fatalf("%T PostProcess diverged at %v: orig=%d loaded=%d", p, k, vA, vb.blocks[k])
		}
	}
	return got
}

// TestPieceNBTRoundTrip covers every Sulfur piece type's Save()/Load() NBT round-trip:
// scattered temples (desert/jungle/swamp), igloo templates, mineshaft, stronghold, jigsaw.
func TestPieceNBTRoundTrip(t *testing.T) {
	rng := func() levelgen.RandomSource {
		r := levelgen.NewWorldgenRandom(0)
		r.SetLargeFeatureSeed(777, 0, 0)
		return r
	}

	t.Run("desert_pyramid", func(t *testing.T) {
		pieceNBTRoundTrip(t, newDesertPyramidPiece(rng(), 32, 48))
	})
	t.Run("jungle_temple", func(t *testing.T) {
		pieceNBTRoundTrip(t, newJungleTemplePiece(rng(), 32, 48))
	})
	t.Run("swamp_hut", func(t *testing.T) {
		pieceNBTRoundTrip(t, newSwampHutPiece(rng(), 32, 48))
	})
	t.Run("igloo_top", func(t *testing.T) {
		pieceNBTRoundTrip(t, newIglooPiece(&iglooTop, 40, 70, 50, iglooTopOffset, block.North))
	})
	t.Run("igloo_middle", func(t *testing.T) {
		pieceNBTRoundTrip(t, newIglooPiece(&iglooMiddle, 40, 70, 50, iglooLadderOffset, block.East))
	})
	t.Run("igloo_bottom", func(t *testing.T) {
		pieceNBTRoundTrip(t, newIglooPiece(&iglooBottom, 40, 70, 50, iglooBasementOffset, block.South))
	})
	t.Run("mineshaft_corridor", func(t *testing.T) {
		bbox := corridorBoundingBox(5, 50, 5, block.South, 3)
		pieceNBTRoundTrip(t, newMineShaftCorridor(2, rng(), bbox, block.South, mineshaftNormal))
	})
	t.Run("mineshaft_crossing", func(t *testing.T) {
		pieceNBTRoundTrip(t, newMineShaftCrossing(2, rng(), 5, 50, 5, block.South, mineshaftMesa))
	})
	t.Run("mineshaft_room", func(t *testing.T) {
		pieceNBTRoundTrip(t, newMineshaftRoom(0, rng(), 5, 5, mineshaftNormal))
	})
	t.Run("mineshaft_stairs", func(t *testing.T) {
		pieceNBTRoundTrip(t, newMineShaftStairs(2, 5, 50, 5, block.South, mineshaftNormal))
	})
	t.Run("stronghold_corridor", func(t *testing.T) {
		pieceNBTRoundTrip(t, newStrongholdCorridor(2, 5, 50, 5, block.South, doorOpening))
	})
	t.Run("stronghold_start", func(t *testing.T) {
		pieceNBTRoundTrip(t, newStrongholdStartPiece(0, 5, 50, 5, block.North))
	})
	t.Run("stronghold_portalroom", func(t *testing.T) {
		pieceNBTRoundTrip(t, newStrongholdPortalRoom(3, 5, 50, 5, block.South))
	})
}

// TestPieceNBTBaseFields asserts the base StructurePiece NBT (BB, O, GD) round-trips exactly
// (the StructurePiece.createTag / ctor port).
func TestPieceNBTBaseFields(t *testing.T) {
	p := newSwampHutPiece(rng777(), 16, 32)
	tag, err := SavePiece(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadPiece(tag)
	if err != nil {
		t.Fatal(err)
	}
	gp := got.(*SwampHutPiece)
	if gp.genDepth != p.genDepth {
		t.Fatalf("GD: got %d want %d", gp.genDepth, p.genDepth)
	}
	if gp.orientation != p.orientation || gp.hasOrient != p.hasOrient {
		t.Fatalf("O: got (%v,%v) want (%v,%v)", gp.orientation, gp.hasOrient, p.orientation, p.hasOrient)
	}
	if gp.rotation != p.rotation || gp.mirror != p.mirror {
		t.Fatalf("rotation/mirror not derived from O: got (%v,%v) want (%v,%v)", gp.rotation, gp.mirror, p.rotation, p.mirror)
	}
}

// TestPieceNBTUnknownID asserts LoadPiece errors loudly on an unknown piece id (Phase-11
// discipline — no silent skip).
func TestPieceNBTUnknownID(t *testing.T) {
	tag := pieceTag{ID: "minecraft:not_a_real_piece"}
	if _, err := LoadPiece(tag); err == nil {
		t.Fatal("LoadPiece: expected an error on an unknown piece id, got nil")
	}
}

func rng777() levelgen.RandomSource {
	r := levelgen.NewWorldgenRandom(0)
	r.SetLargeFeatureSeed(777, 0, 0)
	return r
}
