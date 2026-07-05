package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// icebergCF builds an iceberg configured feature with the given main block name
// ({"state":{"Name":name}} — BlockStateConfiguration).
func icebergCF(name string) *feature.ConfiguredFeature {
	raw := []byte(`{"state":{"Name":"` + name + `"}}`)
	return &feature.ConfiguredFeature{Type: "iceberg", Config: &feature.ParsedConfig{Raw: raw}}
}

// buildIcebergSea fills the 3x3 with water up to sea level over a stone floor — the frozen-ocean
// surface an iceberg rises from.
func buildIcebergSea(minY, height, seaLevel int) *Neighborhood {
	view := build3x3([2]int{0, 0}, minY, height)
	stone := block.ToStateID[block.Stone{}]
	water := block.ToStateID[block.Water{Level: 0}]
	for wx := -16; wx <= 31; wx++ {
		for wz := -16; wz <= 31; wz++ {
			for y := minY; y <= 30; y++ {
				view.SetBlock(wx, y, wz, stone)
			}
			for y := 31; y <= seaLevel; y++ {
				view.SetBlock(wx, y, wz, water)
			}
		}
	}
	return view
}

// countIceberg counts placed iceberg blocks (packed_ice/blue_ice/snow_block) across the view.
func countIceberg(view *Neighborhood, minY, seaLevel, top int) (packed, blue, snowBlk int) {
	for wx := -16; wx <= 31; wx++ {
		for wz := -16; wz <= 31; wz++ {
			for y := minY; y <= top; y++ {
				switch view.GetBlock(wx, y, wz) {
				case block.ToStateID[block.PackedIce{}]:
					packed++
				case block.ToStateID[block.BlueIce{}]:
					blue++
				case block.ToStateID[block.SnowBlock{}]:
					snowBlk++
				}
			}
		}
	}
	return
}

// TestIcebergPlacesPackedIce: an iceberg_packed rises from the frozen ocean, placing packed_ice
// (and, over many seeds, snow_block). We assert at least one packed_ice + snow_block appears
// across a small seed sweep (the shape/snow are RNG-gated per-seed).
func TestIcebergPlacesPackedIce(t *testing.T) {
	const minY, height, seaLevel = -64, 384, 63

	totalPacked, totalSnow := 0, 0
	for seed := int64(1); seed <= 8; seed++ {
		view := buildIcebergSea(minY, height, seaLevel)
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
		bctx := &bodyContext{view: view, seaLevel: seaLevel}
		ok := icebergBody(bctx, icebergCF("minecraft:packed_ice"), ctx,
			levelgen.NewLegacyRandomSource(seed), placement.BlockPos{X: 0, Y: 0, Z: 0})
		if !ok {
			t.Fatalf("icebergBody returned false (always returns true in the jar)")
		}
		p, _, s := countIceberg(view, minY, seaLevel, seaLevel+60)
		totalPacked += p
		totalSnow += s
	}
	if totalPacked == 0 {
		t.Fatalf("no packed_ice placed across the seed sweep")
	}
	if totalSnow == 0 {
		t.Fatalf("no snow_block cap placed across the seed sweep")
	}
}

// TestIcebergBlueVariant: iceberg_blue places blue_ice as its main block.
func TestIcebergBlueVariant(t *testing.T) {
	const minY, height, seaLevel = -64, 384, 63
	totalBlue := 0
	for seed := int64(1); seed <= 8; seed++ {
		view := buildIcebergSea(minY, height, seaLevel)
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
		bctx := &bodyContext{view: view, seaLevel: seaLevel}
		icebergBody(bctx, icebergCF("minecraft:blue_ice"), ctx,
			levelgen.NewLegacyRandomSource(seed), placement.BlockPos{X: 0, Y: 0, Z: 0})
		_, b, _ := countIceberg(view, minY, seaLevel, seaLevel+60)
		totalBlue += b
	}
	if totalBlue == 0 {
		t.Fatalf("no blue_ice placed for iceberg_blue across the seed sweep")
	}
}

// TestIcebergDeterministic: identical seed -> byte-identical placement over the whole view.
func TestIcebergDeterministic(t *testing.T) {
	const minY, height, seaLevel = -64, 384, 63
	run := func() *Neighborhood {
		view := buildIcebergSea(minY, height, seaLevel)
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
		bctx := &bodyContext{view: view, seaLevel: seaLevel}
		icebergBody(bctx, icebergCF("minecraft:packed_ice"), ctx,
			levelgen.NewLegacyRandomSource(0x1CEB), placement.BlockPos{X: 0, Y: 0, Z: 0})
		return view
	}
	v1, v2 := run(), run()
	for wx := -16; wx <= 31; wx++ {
		for wz := -16; wz <= 31; wz++ {
			for y := minY; y <= seaLevel+60; y++ {
				if v1.GetBlock(wx, y, wz) != v2.GetBlock(wx, y, wz) {
					t.Fatalf("iceberg not deterministic at (%d,%d,%d)", wx, y, wz)
				}
			}
		}
	}
}
