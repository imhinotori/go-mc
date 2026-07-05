package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_blob_test.go pins the LIVE blob bodies:
//   - block_blob             (BlockBlobFeature)     — the forest_rock mossy-cobble mound
//   - netherrack_replace_blobs (ReplaceBlobsFeature) — the basalt/blackstone nether blobs
// javap -c, 26.2-inner.jar. Positive placement + determinism (post-place rng reproducible).

func TestBlockBlobPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCF(t, "minecraft:forest_rock")
	// origin above a stone surface: BlockBlobFeature lowers until can_place_on (base_stone_
	// overworld) holds for below, then mounds mossy_cobblestone.
	surfaceTop := 40
	origin := placement.BlockPos{X: 8, Y: 46, Z: 8}
	mossy := block.ToStateID[block.FromID["minecraft:mossy_cobblestone"]]

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		fillSolidStone(view, center, minY, surfaceTop)
		return view
	}

	const seed = int64(0xB10B01)
	// The mound size depends on the 3 nextInt(2) draws (f10 = sum*0.333+0.5); some seeds place
	// only the center cell. Sweep seeds to guarantee a visible mound.
	placedAny := false
	for s := int64(0); s < 40 && !placedAny; s++ {
		view := build()
		bctx := &bodyContext{view: view}
		ctx := newPlacementContext(view, minY, height, nil)
		rng := levelgen.NewWorldgenRandom(seed + s)
		if !blockBlobBody(bctx, cf, ctx, rng, origin) {
			t.Fatalf("block_blob rejected a valid stone surface (seed %d)", seed+s)
		}
		for dx := -3; dx <= 3; dx++ {
			for dy := -4; dy <= 2; dy++ {
				for dz := -3; dz <= 3; dz++ {
					if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) == mossy {
						placedAny = true
					}
				}
			}
		}
	}
	if !placedAny {
		t.Fatalf("block_blob never placed mossy_cobblestone across 40 seeds")
	}

	// Determinism.
	view := build()
	bctx := &bodyContext{view: view}
	rng := levelgen.NewWorldgenRandom(seed)
	blockBlobBody(bctx, cf, newPlacementContext(view, minY, height, nil), rng, origin)
	view2 := build()
	bctx2 := &bodyContext{view: view2}
	rng2 := levelgen.NewWorldgenRandom(seed)
	blockBlobBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), rng2, origin)
	for dx := -4; dx <= 4; dx++ {
		for dy := -6; dy <= 4; dy++ {
			for dz := -4; dz <= 4; dz++ {
				if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) != view2.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) {
					t.Fatalf("non-deterministic block_blob at (%d,%d,%d)", dx, dy, dz)
				}
			}
		}
	}
	if rng.NextLong() != rng2.NextLong() {
		t.Fatalf("post-place rng fingerprint diverged")
	}
}

func TestReplaceBlobsPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCF(t, "minecraft:blackstone_blobs")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	blackstone := block.ToStateID[block.FromID["minecraft:blackstone"]]

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		fillNetherrack(view, center, minY, origin.Y+16)
		return view
	}

	const seed = int64(0xB1AC57)
	placedAny := false
	for s := int64(0); s < 20 && !placedAny; s++ {
		view := build()
		bctx := &bodyContext{view: view}
		ctx := newPlacementContext(view, minY, height, nil)
		rng := levelgen.NewWorldgenRandom(seed + s)
		if !replaceBlobsBody(bctx, cf, ctx, rng, origin) {
			t.Fatalf("netherrack_replace_blobs rejected an all-netherrack column (seed %d)", seed+s)
		}
		for dx := -8; dx <= 8; dx++ {
			for dy := -8; dy <= 8; dy++ {
				for dz := -8; dz <= 8; dz++ {
					if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) == blackstone {
						placedAny = true
					}
				}
			}
		}
	}
	if !placedAny {
		t.Fatalf("netherrack_replace_blobs never placed blackstone across 20 seeds")
	}

	// Determinism.
	view := build()
	bctx := &bodyContext{view: view}
	rng := levelgen.NewWorldgenRandom(seed)
	replaceBlobsBody(bctx, cf, newPlacementContext(view, minY, height, nil), rng, origin)
	view2 := build()
	bctx2 := &bodyContext{view: view2}
	rng2 := levelgen.NewWorldgenRandom(seed)
	replaceBlobsBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), rng2, origin)
	for dx := -8; dx <= 8; dx++ {
		for dy := -8; dy <= 8; dy++ {
			for dz := -8; dz <= 8; dz++ {
				if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) != view2.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) {
					t.Fatalf("non-deterministic replace_blobs at (%d,%d,%d)", dx, dy, dz)
				}
			}
		}
	}
	if rng.NextLong() != rng2.NextLong() {
		t.Fatalf("post-place rng fingerprint diverged")
	}
}
