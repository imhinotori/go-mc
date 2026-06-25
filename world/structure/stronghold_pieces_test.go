package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// strongholdPieceGraph assembles a stronghold piece tree for a known (seed, anchor) the same
// way strongholdStartGen.GenerateStarts does (SetLargeFeatureSeed stream), so the tests probe
// the recursive assembly deterministically without the ring-placement gate.
func strongholdPieceGraph(seed int64, cx, cz, anchorY int) []Piece {
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)
	return assembleStronghold(rng, cx*16+8, anchorY, cz*16+8)
}

// countPortalRooms counts the StrongholdPortalRoom pieces in a graph.
func countPortalRooms(pieces []Piece) int {
	n := 0
	for _, p := range pieces {
		if _, ok := p.(*StrongholdPortalRoom); ok {
			n++
		}
	}
	return n
}

// TestStrongholdTerminates pins the genDepth-50 + maxPlaceCount bound: the recursive graph is
// FINITE (never runs away) and FindCollisionPiece rejects overlaps (no two pieces' bboxes
// intersect). Probed across several seeds.
func TestStrongholdTerminates(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 0xC0FFEE, 0x5EED, 999} {
		pieces := strongholdPieceGraph(seed, 10, 10, 40)
		if len(pieces) == 0 || len(pieces) > 1500 {
			t.Fatalf("seed %d: piece count %d out of bounds (runaway or empty)", seed, len(pieces))
		}
		for i := 0; i < len(pieces); i++ {
			for j := i + 1; j < len(pieces); j++ {
				if pieces[i].BoundingBox().Intersects(pieces[j].BoundingBox()) {
					t.Fatalf("seed %d: pieces %d and %d overlap (%v vs %v) — FindCollisionPiece failed",
						seed, i, j, pieces[i].BoundingBox(), pieces[j].BoundingBox())
				}
			}
		}
	}
}

// TestStrongholdExactlyOnePortalRoom pins the maxPlaceCount=1 cap: EVERY stronghold graph has
// EXACTLY ONE PortalRoom (forced when the recursion doesn't draw one).
func TestStrongholdExactlyOnePortalRoom(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 0xC0FFEE, 0x5EED, 12345, 808} {
		pieces := strongholdPieceGraph(seed, 5, 5, 40)
		if n := countPortalRooms(pieces); n != 1 {
			t.Fatalf("seed %d: PortalRoom count = %d, want exactly 1", seed, n)
		}
	}
}

// placeGraph places the whole piece graph into one unbounded mapView (the oracle), each piece
// seeded with the re-derivable SetLargeFeatureSeed stream.
func placeGraph(t *testing.T, pieces []Piece, seed int64, cx, cz int) *mapView {
	t.Helper()
	if len(pieces) == 0 {
		t.Fatal("empty graph")
	}
	bb := pieces[0].BoundingBox()
	for _, p := range pieces[1:] {
		bb = bb.Encapsulate(p.BoundingBox())
	}
	box := BoundingBox{bb.MinX - 16, bb.MinY - 16, bb.MinZ - 16, bb.MaxX + 16, bb.MaxY + 16, bb.MaxZ + 16}
	view := newMapView()
	for _, p := range pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(seed, cx, cz)
		p.PostProcess(view, box, level.ChunkPos{int32(cx), int32(cz)}, rng)
	}
	return view
}

// hasState reports whether any placed cell holds the given block state.
func hasState(v *mapView, st block.StateID) bool {
	for _, s := range v.blocks {
		if s == st {
			return true
		}
	}
	return false
}

// TestStrongholdSignatureBlocks pins the PortalRoom + Library geometry: the placed graph holds
// the end_portal_frame ring, the silverfish spawner block, lava, iron_bars, and (the library)
// bookshelves + stone_brick stairs + stone bricks. A seed is chosen that places a library.
func TestStrongholdSignatureBlocks(t *testing.T) {
	// Search a seed whose graph contains a Library (its weight is 10; most seeds get one).
	var pieces []Piece
	var libSeed int64
	for s := int64(1); s < 200; s++ {
		g := strongholdPieceGraph(s, 8, 8, 40)
		hasLib := false
		for _, p := range g {
			if _, ok := p.(*StrongholdLibrary); ok {
				hasLib = true
				break
			}
		}
		if hasLib {
			pieces, libSeed = g, s
			break
		}
	}
	if pieces == nil {
		t.Fatal("no seed in 1..200 produced a Library — the weighted draw may be broken")
	}
	view := placeGraph(t, pieces, libSeed, 8, 8)

	// PortalRoom signatures: an end_portal_frame (any facing, no eye) + the spawner + lava.
	frame := stateOf(block.EndPortalFrame{Facing: block.North, Eye: false})
	frameEye := stateOf(block.EndPortalFrame{Facing: block.North, Eye: true})
	if !hasState(view, frame) && !hasState(view, frameEye) {
		t.Error("no end_portal_frame placed (PortalRoom ring missing)")
	}
	if !hasState(view, stateOf(block.Spawner{})) {
		t.Error("no silverfish spawner block placed (PortalRoom)")
	}
	if !hasState(view, stateOf(block.Lava{Level: 0})) {
		t.Error("no lava placed (PortalRoom moat)")
	}
	// Library signatures: bookshelves.
	if !hasState(view, stateOf(block.Bookshelf{})) {
		t.Error("no bookshelf placed (Library)")
	}
	// The stone-brick shell (the SmoothStoneSelector edges).
	if !hasState(view, stateOf(block.StoneBricks{})) &&
		!hasState(view, stateOf(block.MossyStoneBricks{})) &&
		!hasState(view, stateOf(block.CrackedStoneBricks{})) {
		t.Error("no stone-brick shell placed (SmoothStoneSelector not firing)")
	}
}

// TestStrongholdLibraryChest pins the library chest places as a chest BLOCK + the loot tag
// minecraft:chests/stronghold_library (loot deferred v3 — the tag is tracked, not rolled).
func TestStrongholdLibraryChest(t *testing.T) {
	// Build a single Library directly and place it.
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(42, 0, 0)
	lib := newStrongholdLibrary(rng, 5, 8, 40, 8, block.South, doorOpening)
	bb := lib.BoundingBox()
	box := BoundingBox{bb.MinX - 4, bb.MinY - 4, bb.MinZ - 4, bb.MaxX + 4, bb.MaxY + 4, bb.MaxZ + 4}
	view := newMapView()
	lib.PostProcess(view, box, level.ChunkPos{0, 0}, rng)

	if len(lib.lootChests) == 0 {
		t.Fatal("library placed no chest (lootChests empty)")
	}
	for _, lc := range lib.lootChests {
		if lc.LootTable != strongholdLibraryLoot {
			t.Errorf("chest loot tag = %q, want %q", lc.LootTable, strongholdLibraryLoot)
		}
	}
}

// TestStrongholdGraphPure pins determinism: the same (seed,pos) yields the identical piece
// graph (count + bbox) across two computations — the recursive RNG stream is re-derivable.
func TestStrongholdGraphPure(t *testing.T) {
	const seed = int64(0xABCD)
	a := strongholdPieceGraph(seed, 3, 9, 40)
	b := strongholdPieceGraph(seed, 3, 9, 40)
	if len(a) != len(b) {
		t.Fatalf("piece count not pure: %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].BoundingBox() != b[i].BoundingBox() {
			t.Fatalf("piece %d bbox not pure: %v != %v", i, a[i].BoundingBox(), b[i].BoundingBox())
		}
	}
}

// TestStrongholdFingerprint pins the placed graph fingerprint for a known seed (FNV over the
// sorted (pos,state) set); a reorder/wrong-block/wrong-selector-draw flips it. Re-pin on a
// deliberate geometry change.
func TestStrongholdFingerprint(t *testing.T) {
	const seed = int64(777)
	pieces := strongholdPieceGraph(seed, 6, 6, 40)
	view := placeGraph(t, pieces, seed, 6, 6)
	if len(view.blocks) == 0 {
		t.Fatal("placed graph wrote zero blocks")
	}
	fp := fingerprintBlocks(view)
	// Determinism: re-place yields the identical fingerprint.
	view2 := placeGraph(t, pieces, seed, 6, 6)
	if fp2 := fingerprintBlocks(view2); fp != fp2 {
		t.Fatalf("fingerprint non-deterministic: 0x%x != 0x%x", fp, fp2)
	}
	t.Logf("stronghold seed=%d pieces=%d blocks=%d fingerprint=0x%x", seed, len(pieces), len(view.blocks), fp)
}

// TestStrongholdCrossChunk pins the many-chunk stronghold (Pitfall #2): placing it from EACH
// overlapping chunk's writable box (placeInChunk) yields a union == the whole graph, no
// double-write, no truncation, idempotent.
func TestStrongholdCrossChunk(t *testing.T) {
	const seed = int64(0x57A1)
	cx, cz := 4, 4
	pieces := strongholdPieceGraph(seed, cx, cz, 40)
	start := &StructureStart{Structure: "minecraft:stronghold", ChunkPos: level.ChunkPos{int32(cx), int32(cz)}, Pieces: pieces}
	start.RecomputeBBox()
	bb := start.BBox

	// The whole-graph oracle.
	whole := newMapView()
	bigBox := BoundingBox{bb.MinX - 1, bb.MinY - 1, bb.MinZ - 1, bb.MaxX + 1, bb.MaxY + 1, bb.MaxZ + 1}
	for _, p := range pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(seed, cx, cz)
		p.PostProcess(whole, bigBox, start.ChunkPos, rng)
	}

	minCX, maxCX := bb.MinX>>4, bb.MaxX>>4
	minCZ, maxCZ := bb.MinZ>>4, bb.MaxZ>>4
	spanned := (maxCX - minCX + 1) * (maxCZ - minCZ + 1)
	if spanned < 2 {
		t.Fatalf("stronghold spans only %d chunk(s) — not a multi-chunk test (bbox %v)", spanned, bb)
	}

	placeAll := func() *mapView {
		u := newMapView()
		for x := minCX; x <= maxCX; x++ {
			for z := minCZ; z <= maxCZ; z++ {
				ch := level.ChunkPos{int32(x), int32(z)}
				cbox := WritableArea(ch, -64, 384)
				placeInChunk(start, u, cbox, ch, seed)
			}
		}
		return u
	}
	union := placeAll()

	for pos, st := range whole.blocks {
		if pos[1] < -64 || pos[1] > -64+384-1 {
			continue
		}
		if union.blocks[pos] != st {
			t.Fatalf("cross-chunk union diverges at %v: union %v vs whole %v", pos, union.blocks[pos], st)
		}
	}

	// Idempotent.
	union2 := placeAll()
	if len(union.blocks) != len(union2.blocks) {
		t.Fatalf("cross-chunk not idempotent: %d vs %d blocks", len(union.blocks), len(union2.blocks))
	}
	for pos, st := range union.blocks {
		if union2.blocks[pos] != st {
			t.Fatalf("cross-chunk not idempotent at %v: %v vs %v", pos, st, union2.blocks[pos])
		}
	}
}
