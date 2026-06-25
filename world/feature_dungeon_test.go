package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_dungeon_test.go pins the LIVE "monster_room" body (MonsterRoomFeature.place,
// javap -c, 26.2-inner.jar): a valid solid-stone candidate with a cavern adjacency carves
// a cobblestone room with a mossy-speckled floor, a center spawner, and 1-2 wall chests;
// an INVALID candidate (no cavern / no solid floor) returns false with NO partial room;
// and a room at a chunk edge spills its shell into the neighbor. The determinism contract:
// the body is a pure function of (rng, view) — a re-run on a seed-matched view is
// bit-identical, and the post-place rng fingerprint is reproducible.

// fillSolidStone fills the entire 3x3 with stone from minY up to (and including) topY, so
// a carved room has a solid floor + ceiling + walls to brick.
func fillSolidStone(view *Neighborhood, center [2]int, minY, topY int) {
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y <= topY; y++ {
						view.SetBlock(bx+lx, y, bz+lz, stone)
					}
				}
			}
		}
	}
}

// carveAirColumn clears a vertical air column at world (x,z) over [yLo,yHi], creating a
// cavern the dungeon's open-side check can adjoin.
func carveAirColumn(view *Neighborhood, x, z, yLo, yHi int) {
	air := block.ToStateID[block.Air{}]
	for y := yLo; y <= yHi; y++ {
		view.SetBlock(x, y, z, air)
	}
}

// newDungeonCF builds the empty-config monster_room configured feature (the geometry is
// hardcoded; the config is {}).
func newDungeonCF(t *testing.T) *feature.ConfiguredFeature {
	t.Helper()
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:monster_room")
	if err != nil {
		t.Fatalf("ResolveConfigured(monster_room): %v", err)
	}
	return cf
}

// TestMonsterRoomPlaces: a valid solid candidate with an adjacent cavern carves a cobble
// room. The origin (room center) becomes a spawner; the floor ring (y = origin.Y-1) is
// cobblestone/mossy_cobblestone; the interior is air. The body is deterministic (a re-run
// is bit-identical + the post-place rng fingerprint matches).
func TestMonsterRoomPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := newDungeonCF(t)

	// origin in the center chunk, well inside so a 3-half-extent room stays in the 3x3.
	origin := placement.BlockPos{X: 8, Y: 20, Z: 8}

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		// Solid stone from minY up to a ceiling above the room (room spans origin.Y-1 ..
		// origin.Y+4, so a ceiling at origin.Y+8 leaves the ceiling ring solid).
		fillSolidStone(view, center, minY, origin.Y+8)
		// Carve a cavern next to the WEST wall of the room at floor level (an open side):
		// the room's west wall is at x = origin.X - (xr+1); with xr<=3 that is x>=4. Carve a
		// tall air pocket at x=3 (just outside the widest possible room) so a wall cell at
		// j==0 sees air at it + above. The open-side check reads the cell AT the wall ring,
		// which the carve at x=3 makes air for a max-size room; for smaller rooms x=3 is
		// outside, so also carve x=4..5 to guarantee >=1 (and <=5) open sides.
		for x := 3; x <= 5; x++ {
			carveAirColumn(view, x, origin.Z, origin.Y, origin.Y+2)
		}
		return view
	}

	const seed = int64(0xD0463E0)
	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	rng := levelgen.NewWorldgenRandom(seed)
	if !monsterRoomBody(bctx, cf, nil, rng, origin) {
		t.Fatalf("monster_room rejected a valid solid candidate with an adjacent cavern")
	}

	// The center is a spawner.
	if view.GetBlock(origin.X, origin.Y, origin.Z) != dungeonSpawner {
		t.Fatalf("room center is not a spawner: %v", view.GetBlock(origin.X, origin.Y, origin.Z))
	}

	// The floor ring (y = origin.Y-1) is cobblestone or mossy_cobblestone across the room
	// footprint — count both.
	floorY := origin.Y - 1
	cobbleLike := 0
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			st := view.GetBlock(origin.X+dx, floorY, origin.Z+dz)
			if st == dungeonCobble || st == dungeonMossyCobble {
				cobbleLike++
			}
		}
	}
	if cobbleLike == 0 {
		t.Fatalf("no cobblestone/mossy floor placed")
	}

	// At least one mossy_cobblestone speckle exists in the floor (3/4 of solid floor cells
	// become mossy, so over a multi-cell floor at least one is overwhelmingly likely).
	mossy := 0
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			if view.GetBlock(origin.X+dx, floorY, origin.Z+dz) == dungeonMossyCobble {
				mossy++
			}
		}
	}
	if mossy == 0 {
		t.Fatalf("no mossy_cobblestone speckle on the floor (the nextInt(4) speckle draw missing?)")
	}

	// The interior just above the floor is air (carved) at the center-adjacent cells.
	if !block.IsAir(view.GetBlock(origin.X, origin.Y+1, origin.Z)) {
		t.Fatalf("room interior above the spawner is not air")
	}

	// Determinism: a fresh seed-matched run is bit-identical over the room footprint, and
	// the post-place rng fingerprint matches.
	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	rng2 := levelgen.NewWorldgenRandom(seed)
	monsterRoomBody(bctx2, cf, nil, rng2, origin)
	for dx := -6; dx <= 6; dx++ {
		for dy := -2; dy <= 6; dy++ {
			for dz := -6; dz <= 6; dz++ {
				a := view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				b := view2.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				if a != b {
					t.Fatalf("non-deterministic dungeon at (%d,%d,%d): %v vs %v",
						origin.X+dx, origin.Y+dy, origin.Z+dz, a, b)
				}
			}
		}
	}
	if rng.NextLong() != rng2.NextLong() {
		t.Fatalf("post-place rng fingerprint diverged across identical runs")
	}
}

// TestMonsterRoomRejectsNoPartial: a candidate with NO solid floor (all air below) is
// rejected — the body returns false and writes ZERO blocks (no partial room, T-13-10).
func TestMonsterRoomRejectsNoPartial(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := newDungeonCF(t)
	origin := placement.BlockPos{X: 8, Y: 20, Z: 8}

	// An entirely-empty 3x3 (all air): the floor ring (j==-1) is air -> not solid -> reject.
	view := build3x3(center, minY, height)
	// Snapshot every block in the room footprint before.
	type cell struct{ x, y, z int }
	var before []block.StateID
	var cells []cell
	for dx := -6; dx <= 6; dx++ {
		for dy := -2; dy <= 6; dy++ {
			for dz := -6; dz <= 6; dz++ {
				cells = append(cells, cell{origin.X + dx, origin.Y + dy, origin.Z + dz})
				before = append(before, view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz))
			}
		}
	}

	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	rng := levelgen.NewWorldgenRandom(0x1234)
	if monsterRoomBody(bctx, cf, nil, rng, origin) {
		t.Fatalf("monster_room placed on an all-air (no-floor) candidate — should reject")
	}
	// No block changed.
	for i, c := range cells {
		if view.GetBlock(c.x, c.y, c.z) != before[i] {
			t.Fatalf("rejected dungeon wrote a block at (%d,%d,%d) — partial room", c.x, c.y, c.z)
		}
	}
}

// TestMonsterRoomCrossChunkEdge: a dungeon rooted at the +x chunk edge spills its cobble
// shell into the +x neighbor chunk (the Neighborhood write proxy crosses the boundary).
func TestMonsterRoomCrossChunkEdge(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := newDungeonCF(t)

	// origin at the east edge of the center chunk (x=14): the room's east shell (x up to
	// origin.X + (xr+1) = 14+4 = 18) lands in the +x neighbor (x>=16).
	origin := placement.BlockPos{X: 14, Y: 20, Z: 8}

	view := build3x3(center, minY, height)
	fillSolidStone(view, center, minY, origin.Y+8)
	// A cavern adjacency on the west side to satisfy the open-side check.
	for x := origin.X - 5; x <= origin.X - 3; x++ {
		carveAirColumn(view, x, origin.Z, origin.Y, origin.Y+2)
	}

	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	if !monsterRoomBody(bctx, cf, nil, levelgen.NewWorldgenRandom(0xEDED), origin) {
		t.Fatalf("monster_room rejected the edge candidate")
	}

	// Scan the +x neighbor (x>=16) over the room's Y band for any dungeon shell block —
	// the cross-chunk write must have landed there.
	spilled := 0
	for x := 16; x <= origin.X+4; x++ {
		for y := origin.Y - 1; y <= origin.Y+4; y++ {
			for dz := -4; dz <= 4; dz++ {
				st := view.GetBlock(x, y, origin.Z+dz)
				if st == dungeonCobble || st == dungeonMossyCobble {
					spilled++
				}
			}
		}
	}
	if spilled == 0 {
		t.Fatalf("dungeon shell did not spill into the +x neighbor (cross-chunk write dropped?)")
	}

	// Confirm the cross-chunk write path is live: a direct write into the neighbor lands.
	cobble := dungeonCobble
	view.SetBlock(16, origin.Y, origin.Z, cobble)
	if view.GetBlock(16, origin.Y, origin.Z) != cobble {
		t.Fatalf("cross-chunk write into the +x neighbor was dropped")
	}
}

// TestMonsterRoomMobRollConsumed: the deferred spawner mob roll is STILL consumed (the
// final nextInt(4)), so the post-place rng state is jar-faithful. dungeonRandomMobRoll
// draws exactly one nextInt(4); assert it returns a valid index AND advances the rng (a
// guard that the deferral did not silently drop the draw, which would desync the global
// decoration sequence after a dungeon).
func TestMonsterRoomMobRollConsumed(t *testing.T) {
	const seed = int64(0x999)
	rng := levelgen.NewWorldgenRandom(seed)
	idx := dungeonRandomMobRoll(rng)
	if idx < 0 || idx >= 4 {
		t.Fatalf("mob roll index out of range: %d", idx)
	}
	// The post-roll fingerprint must equal an oracle that drew the same single nextInt(4),
	// and must DIFFER from a fresh rng (proving a draw was consumed).
	oracle := levelgen.NewWorldgenRandom(seed)
	oracle.NextIntN(4)
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("mob roll did not consume exactly one nextInt(4) draw (fingerprint mismatch)")
	}
}
