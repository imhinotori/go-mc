package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// mapView is a fake WorldGenView backing block writes/reads with a map, for the piece
// machinery tests. Unlike the real Neighborhood it imposes NO 3x3 window — so a test can
// assert exactly which cells placeBlock's writable-box clip let through (isolating the clip
// from the Neighborhood's own radius drop). Out-of-store reads return air.
type mapView struct {
	blocks   map[[3]int]block.StateID
	writes   int
	spawners map[[3]int]string // SetSpawner captures: world pos -> spawned entity id
	spawns   []SpawnRequest    // RecordSpawn captures: the structure-inhabitant requests
}

func newMapView() *mapView { return &mapView{blocks: map[[3]int]block.StateID{}} }

func (m *mapView) SetBlock(wx, wy, wz int, st block.StateID) {
	m.blocks[[3]int{wx, wy, wz}] = st
	m.writes++
}

func (m *mapView) GetBlock(wx, wy, wz int) block.StateID {
	if st, ok := m.blocks[[3]int{wx, wy, wz}]; ok {
		return st
	}
	return stateAir
}

// SetBlockEntity satisfies WorldGenView for the piece tests that don't care about block
// entities (the recordingView in chest_be_test.go overrides this to capture chest BEs).
func (m *mapView) SetBlockEntity(wx, wy, wz int, typ block.EntityType, lootTable string, lootSeed int64) {
}

// SetSpawner satisfies WorldGenView. The recordingView/spawnView override it to capture the
// stronghold silverfish spawner BE; the base mapView records the spawner-set as a write so a
// test can assert the spawner block landed without a dedicated capture.
func (m *mapView) SetSpawner(wx, wy, wz int, entityID string) {
	if m.spawners == nil {
		m.spawners = map[[3]int]string{}
	}
	m.spawners[[3]int{wx, wy, wz}] = entityID
}

// RecordSpawn satisfies WorldGenView. The base mapView buffers the requests so a piece test can
// assert the witch/cat/villager spawn records without a server-side store.
func (m *mapView) RecordSpawn(req SpawnRequest) {
	m.spawns = append(m.spawns, req)
}

func sandstoneID(t *testing.T) block.StateID {
	t.Helper()
	id, ok := block.ToStateID[block.Sandstone{}]
	if !ok {
		t.Fatal("block.ToStateID[block.Sandstone{}] missing — block registry not loaded")
	}
	return id
}

// newTestPiece builds an unoriented (NONE) base piece whose bbox is the world-coord box —
// the identity placement path (local coords == world coords).
func newTestPiece(bb BoundingBox) *StructurePiece {
	p := &StructurePiece{bbox: bb}
	p.setOrientation(block.North, false) // NONE: rotation/mirror identity
	return p
}

// TestPlaceBlockClips: a placeBlock whose world pos is OUTSIDE the writable box is DROPPED;
// inside, it is SetBlock'd. This is the cross-chunk clip (Pitfall #2).
func TestPlaceBlockClips(t *testing.T) {
	sand := sandstoneID(t)
	// Piece bbox spans x[0..31] (two chunks wide); the writable box is only chunk (0,0)'s
	// 16-wide column. A placeBlock at local x=20 (world x=20) must be DROPPED.
	piece := newTestPiece(BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 31, MaxY: 80, MaxZ: 15})
	box := WritableArea(level.ChunkPos{0, 0}, 64, 32) // x[0..15], y[64..95], z[0..15]
	view := newMapView()

	piece.placeBlock(view, sand, 5, 64, 5, box) // inside -> placed
	piece.placeBlock(view, sand, 20, 64, 5, box) // x=20 outside box -> dropped
	piece.placeBlock(view, sand, 5, 200, 5, box) // y=200 above box -> dropped

	if got := view.GetBlock(5, 64, 5); got != sand {
		t.Fatalf("in-box placeBlock did not write: got %v want %v", got, sand)
	}
	if got := view.GetBlock(20, 64, 5); !block.IsAir(got) {
		t.Fatalf("out-of-box placeBlock was NOT dropped at x=20: got %v", got)
	}
	if got := view.GetBlock(5, 200, 5); !block.IsAir(got) {
		t.Fatalf("out-of-box placeBlock was NOT dropped at y=200: got %v", got)
	}
	if view.writes != 1 {
		t.Fatalf("expected exactly 1 in-box write, got %d", view.writes)
	}
}

// TestGenerateBox: a generateBox fills the local sub-box; cells outside the writable box are
// clipped. Asserts every in-box cell is filled and the count matches.
func TestGenerateBox(t *testing.T) {
	sand := sandstoneID(t)
	piece := newTestPiece(BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 15, MaxY: 80, MaxZ: 15})
	box := WritableArea(level.ChunkPos{0, 0}, 64, 32)
	view := newMapView()

	// A 3x2x3 box (x 2..4, y 64..65, z 2..4) = 18 cells, all in-box.
	piece.generateBox(view, box, 2, 64, 2, 4, 65, 4, sand, sand, false)
	if view.writes != 18 {
		t.Fatalf("generateBox wrote %d cells, want 18", view.writes)
	}
	for x := 2; x <= 4; x++ {
		for y := 64; y <= 65; y++ {
			for z := 2; z <= 4; z++ {
				if got := view.GetBlock(x, y, z); got != sand {
					t.Fatalf("generateBox cell (%d,%d,%d) = %v, want sandstone", x, y, z, got)
				}
			}
		}
	}
}

// TestGenerateBoxEdgeFill: generateBox places edge vs fill correctly (the face/interior
// distinction) — a hollow box has air interior when fill is air.
func TestGenerateBoxEdgeFill(t *testing.T) {
	sand := sandstoneID(t)
	piece := newTestPiece(BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 15, MaxY: 80, MaxZ: 15})
	box := WritableArea(level.ChunkPos{0, 0}, 64, 32)
	view := newMapView()

	// 3x3x3 box edge=sand, fill=air. The single interior cell (center) must be air.
	piece.generateBox(view, box, 1, 65, 1, 3, 67, 3, sand, stateAir, false)
	if got := view.GetBlock(2, 66, 2); !block.IsAir(got) {
		t.Fatalf("generateBox interior cell should be fill(air), got %v", got)
	}
	if got := view.GetBlock(1, 65, 1); got != sand {
		t.Fatalf("generateBox corner cell should be edge(sandstone), got %v", got)
	}
}

// TestFillColumnDown: fillColumnDown writes downward through air until a solid block (or the
// floor). A pre-placed solid stops the fill.
func TestFillColumnDown(t *testing.T) {
	sand := sandstoneID(t)
	piece := newTestPiece(BoundingBox{MinX: 0, MinY: -64, MinZ: 0, MaxX: 15, MaxY: 80, MaxZ: 15})
	box := WritableArea(level.ChunkPos{0, 0}, -64, 384) // full column
	view := newMapView()

	// A solid floor at y=60 stops the downward fill from y=70.
	view.SetBlock(5, 60, 5, sand)
	piece.fillColumnDown(view, sand, 5, 70, 5, box, -64)

	for y := 70; y >= 61; y-- {
		if got := view.GetBlock(5, y, 5); got != sand {
			t.Fatalf("fillColumnDown should have filled y=%d, got %v", y, got)
		}
	}
	// y=60 was already solid (not overwritten by the fill, the loop stops above it).
	if got := view.GetBlock(5, 60, 5); got != sand {
		t.Fatalf("fillColumnDown clobbered the floor at y=60: %v", got)
	}
	// y=59 below the floor must be untouched (air).
	if got := view.GetBlock(5, 59, 5); !block.IsAir(got) {
		t.Fatalf("fillColumnDown wrote below the floor at y=59: %v", got)
	}
}

// TestRotationMirrorIdentity: orientation NORTH (NONE/NONE) leaves a facing block's facing +
// the coordinate mapping unchanged (the identity path the temples exercise).
func TestRotationMirrorIdentity(t *testing.T) {
	piece := newTestPiece(BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 15, MaxY: 80, MaxZ: 15})
	if piece.rotation != RotNone || piece.mirror != MirrorNone {
		t.Fatalf("NONE orientation must be RotNone/MirrorNone, got rot=%v mir=%v", piece.rotation, piece.mirror)
	}
	stair := block.SandstoneStairs{Facing: block.North, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false}
	id, ok := block.ToStateID[stair]
	if !ok {
		t.Fatal("default north sandstone stairs state missing")
	}
	if got := transformState(id, piece.mirror, piece.rotation); got != id {
		t.Fatalf("identity transform changed the state: %v -> %v", id, got)
	}
	// getWorldX/Y/Z identity: local == world (no orientation).
	if piece.getWorldX(7, 3) != 7 || piece.getWorldY(5) != 5 || piece.getWorldZ(7, 3) != 3 {
		t.Fatalf("NONE getWorldX/Y/Z is not the identity")
	}
}

// TestRotationTransform: ROT_90 maps a north-facing stair's facing to east AND the EAST
// orientation maps the local coords through the bbox (the transform path Phase-16 reuses).
func TestRotationTransform(t *testing.T) {
	north := block.SandstoneStairs{Facing: block.North, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false}
	east := block.SandstoneStairs{Facing: block.East, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false}
	nID := block.ToStateID[north]
	eID := block.ToStateID[east]

	// CLOCKWISE_90 turns NORTH -> EAST.
	if got := transformState(nID, MirrorNone, RotClockwise90); got != eID {
		t.Fatalf("ROT_90(north stair) = %v, want east %v", got, eID)
	}

	// EAST orientation: rotation CW90, mirror NONE; coords map via getWorldX/Z (z->minX+z,
	// x->minZ+x). Build an oriented piece + check a known mapping.
	p := &StructurePiece{bbox: BoundingBox{MinX: 100, MinY: 64, MinZ: 200, MaxX: 120, MaxY: 78, MaxZ: 220}}
	p.setOrientation(block.East, true)
	if p.rotation != RotClockwise90 || p.mirror != MirrorNone {
		t.Fatalf("EAST orientation must be CW90/NONE, got rot=%v mir=%v", p.rotation, p.mirror)
	}
	// EAST: getWorldX = minX + z ; getWorldZ = minZ + x ; getWorldY = y + minY.
	if gx := p.getWorldX(3, 5); gx != 100+5 {
		t.Fatalf("EAST getWorldX(3,5) = %d, want %d", gx, 105)
	}
	if gz := p.getWorldZ(3, 5); gz != 200+3 {
		t.Fatalf("EAST getWorldZ(3,5) = %d, want %d", gz, 203)
	}
	if gy := p.getWorldY(7); gy != 64+7 {
		t.Fatalf("EAST getWorldY(7) = %d, want %d", gy, 71)
	}
}

// TestMirrorTransform: SOUTH orientation (mirror LEFT_RIGHT) flips a north-facing stair to
// south (the Z-axis mirror), exercising the mirror path.
func TestMirrorTransform(t *testing.T) {
	north := block.SandstoneStairs{Facing: block.North, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false}
	south := block.SandstoneStairs{Facing: block.South, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false}
	nID := block.ToStateID[north]
	sID := block.ToStateID[south]
	if got := transformState(nID, MirrorLeftRight, RotNone); got != sID {
		t.Fatalf("MIRROR_LEFT_RIGHT(north stair) = %v, want south %v", got, sID)
	}
}

// TestFindCollisionPiece: returns the first piece whose bbox intersects box, nil if none.
func TestFindCollisionPiece(t *testing.T) {
	a := testBBoxPiece{BoundingBox{0, 64, 0, 10, 74, 10}}
	b := testBBoxPiece{BoundingBox{20, 64, 20, 30, 74, 30}}
	pieces := []Piece{a, b}

	hit := FindCollisionPiece(pieces, BoundingBox{5, 64, 5, 8, 70, 8})
	if hit == nil || hit.BoundingBox() != a.BoundingBox() {
		t.Fatalf("FindCollisionPiece should return piece a, got %v", hit)
	}
	miss := FindCollisionPiece(pieces, BoundingBox{40, 64, 40, 50, 70, 50})
	if miss != nil {
		t.Fatalf("FindCollisionPiece should return nil for a non-overlapping box, got %v", miss)
	}
}

// TestGetRandomHorizontalDirectionOrder pins the orientation draw: faces[nextInt(4)] over
// [NORTH, EAST, SOUTH, WEST] — the load-bearing draw the desert pyramid makes at construction.
func TestGetRandomHorizontalDirectionOrder(t *testing.T) {
	want := [4]block.Direction{block.North, block.East, block.South, block.West}
	for i := int32(0); i < 4; i++ {
		rng := &fixedIntRNG{val: i}
		if got := getRandomHorizontalDirection(rng); got != want[i] {
			t.Fatalf("getRandomHorizontalDirection with nextInt=%d = %v, want %v", i, got, want[i])
		}
	}
}

// testBBoxPiece is a bbox-only Piece for the collision test (PostProcess is a no-op).
type testBBoxPiece struct{ bb BoundingBox }

func (p testBBoxPiece) BoundingBox() BoundingBox { return p.bb }
func (p testBBoxPiece) PostProcess(WorldGenView, BoundingBox, level.ChunkPos, levelgen.RandomSource) {
}

// fixedIntRNG is a RandomSource whose NextIntN always returns a fixed value (for the
// orientation-order test). Only NextIntN is used; the rest panic if hit unexpectedly.
type fixedIntRNG struct{ val int32 }

func (f *fixedIntRNG) NextIntN(int32) int32                     { return f.val }
func (f *fixedIntRNG) NextInt() int32                           { return f.val }
func (f *fixedIntRNG) NextLong() int64                          { return int64(f.val) }
func (f *fixedIntRNG) NextDouble() float64                      { return 0 }
func (f *fixedIntRNG) NextFloat() float32                       { return 0 }
func (f *fixedIntRNG) NextBoolean() bool                        { return false }
func (f *fixedIntRNG) ConsumeCount(int)                         {}
func (f *fixedIntRNG) Fork() levelgen.RandomSource              { return f }
func (f *fixedIntRNG) ForkPositional() levelgen.PositionalRandomFactory { return nil }
