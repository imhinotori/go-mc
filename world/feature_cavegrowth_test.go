package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_cavegrowth_test.go pins the deep-dark / cave-growth bodies ported in
// feature_sculk.go, feature_multiface.go, feature_root_system.go:
//   - sculk_patch      (SculkPatchFeature + SculkSpreader)
//   - multiface_growth (MultifaceGrowthFeature: glow_lichen / sculk_vein over cave walls)
//   - root_system      (RootSystemFeature: rooted_dirt column under an azalea tree)
// Each test asserts (a) at least one target block is placed over a synthetic cave, and
// (b) the body is deterministic (a seed-matched re-run is bit-identical).

func resolveCaveCF(t *testing.T, id string) *feature.ConfiguredFeature {
	t.Helper()
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured(id)
	if err != nil {
		t.Fatalf("ResolveConfigured(%s): %v", id, err)
	}
	return cf
}

// snapshotView captures every block state in the 3x3 window over [yLo,yHi] for a bit-exact
// determinism comparison.
func snapshotView(view *Neighborhood, center [2]int, yLo, yHi int) map[[3]int]block.StateID {
	out := map[[3]int]block.StateID{}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := yLo; y <= yHi; y++ {
						x, z := bx+lx, bz+lz
						out[[3]int{x, y, z}] = view.GetBlock(x, y, z)
					}
				}
			}
		}
	}
	return out
}

func sameSnapshot(a, b map[[3]int]block.StateID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// fillStone3x3 sets the whole 3x3 to stone over [minY, topY].
func fillStone3x3(view *Neighborhood, center [2]int, minY, topY int) {
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

// ===========================================================================================
//  sculk_patch
// ===========================================================================================

func TestSculkPatchPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCaveCF(t, "minecraft:sculk_patch_deep_dark")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0x5C0_1CAB)

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		// A stone floor under the origin: origin sits in air on a #sculk_replaceable_world_gen
		// (stone) floor, so canSpreadFrom passes (adjacent full-collision block) and the sculk
		// carpet can grow across the floor.
		fillStone3x3(view, center, minY, origin.Y-1)
		return view
	}

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := sculkPatchBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("sculk_patch body returned false over a stone floor")
	}

	// Count the sculk family placed anywhere in the window: SCULK, SCULK_VEIN, SCULK_CATALYST,
	// SCULK_SENSOR, SCULK_SHRIEKER.
	sculkCount := 0
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			for y := origin.Y - 2; y <= origin.Y+3; y++ {
				st := view.GetBlock(origin.X+dx, y, origin.Z+dz)
				if isSculkBehaviour(st) {
					sculkCount++
				}
			}
		}
	}
	if sculkCount == 0 {
		t.Fatalf("sculk_patch placed no sculk-family blocks")
	}
	t.Logf("sculk_patch placed %d sculk-family blocks", sculkCount)

	// Determinism: a seed-matched re-run is bit-identical.
	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	sculkPatchBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !sameSnapshot(snapshotView(view, center, origin.Y-2, origin.Y+4), snapshotView(view2, center, origin.Y-2, origin.Y+4)) {
		t.Fatalf("sculk_patch is not deterministic across a seed-matched re-run")
	}
}

func TestSculkPatchAncientCityParses(t *testing.T) {
	// The ancient_city config uses a uniform IntProvider extra_rare_growths — exercise its
	// decode + a full run so the IntProvider path is covered (no panic).
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCaveCF(t, "minecraft:sculk_patch_ancient_city")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	view := build3x3(center, minY, height)
	fillStone3x3(view, center, minY, origin.Y-1)
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	if !sculkPatchBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(0x0AC17), origin) {
		t.Fatalf("sculk_patch_ancient_city returned false over a stone floor")
	}
}

// ===========================================================================================
//  multiface_growth (glow_lichen)
// ===========================================================================================

func TestMultifaceGrowthGlowLichenAttaches(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCaveCF(t, "minecraft:glow_lichen")
	// origin is an air cell with a stone WALL to the NORTH — glow_lichen should attach to that
	// wall (place on the origin with its NORTH face set true, since can_place_on_wall=true).
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0x11C4E_01)

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		stone := block.ToStateID[block.Stone{}]
		// A solid stone slab to the north (and a small block of stone) so canBePlacedOn +
		// canAttachTo hold in at least one placement direction.
		view.SetBlock(origin.X, origin.Y, origin.Z-1, stone)
		return view
	}

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := multifaceGrowthBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("multiface_growth (glow_lichen) returned false with a stone wall present")
	}
	// The origin should now be a glow_lichen with at least one face set.
	lichenCount := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			for dy := -2; dy <= 2; dy++ {
				st := view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				if multifaceIsPlaceBlock(multifaceGlowLichen, st) {
					lichenCount++
				}
			}
		}
	}
	if lichenCount == 0 {
		t.Fatalf("multiface_growth placed no glow_lichen")
	}
	t.Logf("multiface_growth placed %d glow_lichen", lichenCount)

	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	multifaceGrowthBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !sameSnapshot(snapshotView(view, center, origin.Y-3, origin.Y+3), snapshotView(view2, center, origin.Y-3, origin.Y+3)) {
		t.Fatalf("multiface_growth is not deterministic across a seed-matched re-run")
	}
}

func TestMultifaceGrowthSculkVeinParses(t *testing.T) {
	// sculk_vein reuses the same MultifaceSpreader machinery but with the vein config +
	// chance_of_spreading=1.0 (so it always spreads). Exercise it over a wall.
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCaveCF(t, "minecraft:sculk_vein")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	view := build3x3(center, minY, height)
	stone := block.ToStateID[block.Stone{}]
	// A stone ceiling above so can_place_on_ceiling attaches.
	view.SetBlock(origin.X, origin.Y+1, origin.Z, stone)
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	if !multifaceGrowthBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(0x5E1_00), origin) {
		t.Fatalf("multiface_growth (sculk_vein) returned false under a stone ceiling")
	}
	found := false
	for dx := -2; dx <= 2 && !found; dx++ {
		for dz := -2; dz <= 2 && !found; dz++ {
			for dy := -2; dy <= 2; dy++ {
				if multifaceIsPlaceBlock(multifaceSculkVein, view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)) {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("multiface_growth placed no sculk_vein")
	}
}

// ===========================================================================================
//  root_system
// ===========================================================================================

func TestRootSystemPlacesRootedDirt(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCaveCF(t, "minecraft:rooted_azalea_tree")
	// origin is air; the block below is dirt (an #azalea_grows_on member) so the
	// allowed_tree_position predicate can pass at the origin, and the rooted column of
	// rooted_dirt is laid through the surrounding #azalea_root_replaceable stone.
	origin := placement.BlockPos{X: 8, Y: 60, Z: 8}
	const seed = int64(0xACE_1EA)

	// A lush-cave layout: a stone body up to origin.Y-1 (the #azalea_root_replaceable substrate
	// the rooted column threads through), a dirt cap directly below the origin (#azalea_grows_on),
	// an air cavern [origin.Y, ceilY) for the azalea tree, and a stone ceiling at ceilY so the
	// WORLD_SURFACE heightmap stays above the working column as placeDirtAndTree walks up.
	const ceilY = 80
	const topY = 90
	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		stone := block.ToStateID[block.Stone{}]
		dirt := block.ToStateID[block.Dirt{}]
		air := block.ToStateID[block.Air{}]
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						x, z := bx+lx, bz+lz
						for y := minY; y <= topY; y++ {
							switch {
							case y < origin.Y:
								view.SetBlock(x, y, z, stone) // solid substrate below the floor
							case y >= ceilY:
								view.SetBlock(x, y, z, stone) // stone ceiling → WORLD_SURFACE stays high
							default:
								view.SetBlock(x, y, z, air) // the cavern the azalea tree grows in
							}
						}
					}
				}
			}
		}
		// A dirt shelf a few cells up the column: placeDirtAndTree walks workingPos UP from
		// origin+1; at workingPos = shelfY+1 the block below is this dirt (#azalea_grows_on), so
		// the predicate + solid-floor gate pass and the azalea tree places there, laying the
		// rooted_dirt column from the origin up to that height.
		shelfY := origin.Y + 2
		view.SetBlock(origin.X, shelfY, origin.Z, dirt)
		return view
	}

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := rootSystemBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("root_system body returned false (should always return true when origin is air)")
	}

	// Determinism: a seed-matched re-run is bit-identical over the whole window.
	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	rootSystemBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !sameSnapshot(snapshotView(view, center, minY, origin.Y+8), snapshotView(view2, center, minY, origin.Y+8)) {
		t.Fatalf("root_system is not deterministic across a seed-matched re-run")
	}

	// Count rooted_dirt placed anywhere in the column window.
	rootedDirt := 0
	hanging := 0
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			for y := origin.Y - 2; y <= origin.Y+8; y++ {
				st := view.GetBlock(origin.X+dx, y, origin.Z+dz)
				if int(st) >= 0 && int(st) < len(block.StateList) {
					switch block.StateList[st].ID() {
					case "minecraft:rooted_dirt":
						rootedDirt++
					case "minecraft:hanging_roots":
						hanging++
					}
				}
			}
		}
	}
	t.Logf("root_system placed rooted_dirt=%d hanging_roots=%d", rootedDirt, hanging)
	// The rooted column is laid only after the azalea tree sub-feature lands (placeDirtAndTree),
	// exercising the full recursion → tree body → rooted_dirt column path. Under this lush-cave
	// layout the tree lands on the dirt shelf, so at least one rooted_dirt must be placed.
	if rootedDirt == 0 {
		t.Fatalf("root_system placed no rooted_dirt (the azalea tree sub-feature + rooted column did not run)")
	}
}
