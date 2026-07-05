package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// biomeType resolves a biome id to a biome.Type for the freeze/lake tests.
func biomeType(t *testing.T, id string) biome.Type {
	t.Helper()
	var bt biome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		t.Fatalf("biome %q: %v", id, err)
	}
	return bt
}

// TestFreezeIcesWaterInColdBiome: a water surface column in a COLD biome becomes ICE; the same
// column in a WARM biome stays water (SnowAndFreezeFeature -> Biome.shouldFreeze).
func TestFreezeIcesWaterInColdBiome(t *testing.T) {
	const minY, height = -64, 384
	water := block.ToStateID[block.Water{Level: 0}]
	ice := block.ToStateID[block.Ice{}]

	run := func(biomeID string) block.StateID {
		center := [2]int{0, 0}
		view := build3x3(center, minY, height)
		// Fill a water surface at y=63 across the chunk so MOTION_BLOCKING tops at y=64 and
		// pos2 (topY-1) lands on the water.
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				view.SetBlock(lx, 63, lz, water)
			}
		}
		bt := biomeType(t, biomeID)
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return bt })
		bctx := &bodyContext{view: view, seaLevel: 63}
		// The freeze origin is the chunk corner (0,*,0); the body sweeps dx,dz in [0..15].
		freezeTopLayerBody(bctx, nil, ctx, levelgen.NewLegacyRandomSource(1), placement.BlockPos{X: 0, Y: 0, Z: 0})
		return view.GetBlock(8, 63, 8)
	}

	if got := run("minecraft:snowy_plains"); got != ice {
		t.Fatalf("cold biome water surface is %d, want ICE %d", got, ice)
	}
	if got := run("minecraft:plains"); got != water {
		t.Fatalf("warm biome water surface is %d, want water %d (no freeze)", got, water)
	}
}

// TestFreezeSnowsGroundInColdBiome: a solid grass floor in a COLD, precipitating biome gets a
// snow layer on top and the grass turns snowy; a warm biome gets neither.
func TestFreezeSnowsGroundInColdBiome(t *testing.T) {
	const minY, height = -64, 384
	grass := block.ToStateID[block.GrassBlock{Snowy: false}]
	grassSnowy := block.ToStateID[block.GrassBlock{Snowy: true}]
	snow := block.ToStateID[block.Snow{Layers: 1}]

	run := func(biomeID string) (top block.StateID, ground block.StateID) {
		center := [2]int{0, 0}
		view := build3x3(center, minY, height)
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				view.SetBlock(lx, 63, lz, grass) // solid grass surface at y=63
			}
		}
		bt := biomeType(t, biomeID)
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return bt })
		bctx := &bodyContext{view: view, seaLevel: 63}
		freezeTopLayerBody(bctx, nil, ctx, levelgen.NewLegacyRandomSource(1), placement.BlockPos{X: 0, Y: 0, Z: 0})
		// MOTION_BLOCKING tops at y=64 (above the grass), so snow lands at y=64, ground at y=63.
		return view.GetBlock(8, 64, 8), view.GetBlock(8, 63, 8)
	}

	top, ground := run("minecraft:snowy_plains")
	if top != snow {
		t.Fatalf("cold biome top is %d, want SNOW layer %d", top, snow)
	}
	if ground != grassSnowy {
		t.Fatalf("cold biome ground is %d, want snowy grass %d", ground, grassSnowy)
	}
	wTop, wGround := run("minecraft:plains")
	if wTop == snow {
		t.Fatalf("warm biome placed snow; want none")
	}
	if wGround != grass {
		t.Fatalf("warm biome ground is %d, want plain grass %d (no snowy)", wGround, grass)
	}
}

// TestFreezeDeterministic: freeze places identically across two runs (0 rng draws — pure
// per-column geometry + biome climate).
func TestFreezeDeterministic(t *testing.T) {
	const minY, height = -64, 384
	water := block.ToStateID[block.Water{Level: 0}]
	build := func() (*Neighborhood, placement.PlacementContext) {
		center := [2]int{0, 0}
		view := build3x3(center, minY, height)
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				view.SetBlock(lx, 63, lz, water)
			}
		}
		bt := biomeType(t, "minecraft:snowy_plains")
		return view, newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return bt })
	}
	v1, c1 := build()
	v2, c2 := build()
	freezeTopLayerBody(&bodyContext{view: v1, seaLevel: 63}, nil, c1, levelgen.NewLegacyRandomSource(3), placement.BlockPos{})
	freezeTopLayerBody(&bodyContext{view: v2, seaLevel: 63}, nil, c2, levelgen.NewLegacyRandomSource(3), placement.BlockPos{})
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			if v1.GetBlock(lx, 63, lz) != v2.GetBlock(lx, 63, lz) {
				t.Fatalf("freeze not deterministic at (%d,63,%d)", lx, lz)
			}
		}
	}
}
