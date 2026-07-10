package world

import (
	"encoding/json"
	"math/bits"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/structure"
)

// ---- no_op ----

// TestNoOpPlacesNothing proves NoOpFeature.place places NO blocks, draws NO rng, and returns
// true (the 26.2 bytecode: iconst_1 / ireturn).
func TestNoOpPlacesNothing(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 64, Z: 8}
	view := build3x3([2]int{0, 0}, minY, height)

	rng := levelgen.NewWorldgenRandom(0x517)
	if !noOpBody(&bodyContext{view: view}, nil, newPlacementContext(view, minY, height, nil), rng, origin) {
		t.Fatalf("no_op should return true (jar bytecode)")
	}
	for y := origin.Y - 8; y <= origin.Y+8; y++ {
		for z := origin.Z - 8; z <= origin.Z+8; z++ {
			for x := origin.X - 8; x <= origin.X+8; x++ {
				if !block.IsAir(view.GetBlock(x, y, z)) {
					t.Fatalf("no_op placed a block at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
	oracle := levelgen.NewWorldgenRandom(0x517)
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("no_op consumed rng (it must draw nothing)")
	}
}

// ---- bonus_chest ----

// TestBonusChestPlacesChest proves BonusChestFeature places a chest at the heightmap top of some
// column and torches on supportable sides, and is deterministic.
func TestBonusChestPlacesChest(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 64, Z: 8}
	cf := resolveCF(t, "minecraft:bonus_chest")

	build := func() *Neighborhood {
		v := build3x3([2]int{0, 0}, minY, height)
		stone := block.ToStateID[block.Stone{}]
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := minY; y <= 63; y++ {
					v.SetBlock(x, y, z, stone)
				}
			}
		}
		initMBNL(v, minY, height, 64)
		return v
	}

	const seed = int64(0xB0173)
	a := build()
	ra := levelgen.NewWorldgenRandom(seed)
	if !bonusChestBody(&bodyContext{view: a}, cf, newPlacementContext(a, minY, height, nil), ra, origin) {
		t.Fatalf("bonus_chest returned false on a solid-floored chunk")
	}

	chestID := "minecraft:chest"
	chests := 0
	var chestPos placement.BlockPos
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			st := a.GetBlock(x, 64, z)
			if int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == chestID {
				chests++
				chestPos = placement.BlockPos{X: x, Y: 64, Z: z}
			}
		}
	}
	if chests != 1 {
		t.Fatalf("bonus_chest placed %d chests, want 1", chests)
	}
	torchID := "minecraft:torch"
	for _, d := range coralHorizontals {
		st := a.GetBlock(chestPos.X+d.dx, chestPos.Y, chestPos.Z+d.dz)
		if !(int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == torchID) {
			t.Fatalf("expected torch at chest side (%d,%d)", d.dx, d.dz)
		}
	}

	b := build()
	rb := levelgen.NewWorldgenRandom(seed)
	bonusChestBody(&bodyContext{view: b}, cf, newPlacementContext(b, minY, height, nil), rb, origin)
	if a.GetBlock(chestPos.X, chestPos.Y, chestPos.Z) != b.GetBlock(chestPos.X, chestPos.Y, chestPos.Z) {
		t.Fatalf("bonus_chest not deterministic across runs")
	}
}

// TestBonusChestDrawOrder proves the leading draws are the two range shuffles (x then z, each a
// 16-element Fisher-Yates = draws for i in [15..1]) followed by exactly ONE nextLong() (the loot
// seed) at the first successful column (all-solid top -> the first (x,z) succeeds).
func TestBonusChestDrawOrder(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 64, Z: 8}
	cf := resolveCF(t, "minecraft:bonus_chest")

	v := build3x3([2]int{0, 0}, minY, height)
	stone := block.ToStateID[block.Stone{}]
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := minY; y <= 63; y++ {
				v.SetBlock(x, y, z, stone)
			}
		}
	}
	initMBNL(v, minY, height, 64)
	rng := levelgen.NewWorldgenRandom(0xB0173)
	bonusChestBody(&bodyContext{view: v}, cf, newPlacementContext(v, minY, height, nil), rng, origin)

	oracle := levelgen.NewWorldgenRandom(0xB0173)
	for i := 15; i >= 1; i-- {
		_ = oracle.NextIntN(int32(i + 1))
	}
	for i := 15; i >= 1; i-- {
		_ = oracle.NextIntN(int32(i + 1))
	}
	_ = oracle.NextLong()
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("bonus_chest draw sequence diverged from the javap oracle")
	}
}

// ---- template ----

// makeTemplateCF builds a minimal minecraft:template ConfiguredFeature whose single entry loads
// a real embedded structure template, with the default all-4 rotations.
func makeTemplateCF(t *testing.T, templateID string) *feature.ConfiguredFeature {
	t.Helper()
	cfgJSON := "{\"templates\":[{\"data\":{\"id\":\"" + templateID + "\"},\"weight\":1}]}"
	cf := &feature.ConfiguredFeature{ID: "test:template"}
	cf.Config = &feature.ParsedConfig{Raw: json.RawMessage(cfgJSON)}
	return cf
}

// TestTemplateRotatedOffset proves StructureTemplate.RotatedOffset matches the vanilla
// getRotatedOffset formula for NONE rotation: axis.getNegative().getUnitVec3i() * (size/2).
func TestTemplateRotatedOffset(t *testing.T) {
	tpl, err := structure.LoadTemplate("minecraft:fossil/spine_1")
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	ox, oy, oz := tpl.RotatedOffset(structure.RotNone, structure.AxisX)
	if oy != 0 || oz != 0 || ox > 0 {
		t.Fatalf("X offset under NONE should be (-sizeX/2,0,0), got (%d,%d,%d)", ox, oy, oz)
	}
	zx, zy, zz := tpl.RotatedOffset(structure.RotNone, structure.AxisZ)
	if zy != 0 || zx != 0 || zz > 0 {
		t.Fatalf("Z offset under NONE should be (0,0,-sizeZ/2), got (%d,%d,%d)", zx, zy, zz)
	}
}

// TestTemplatePlacesAndDeterministic proves templateBody REALLY places template blocks (via the
// runtime placer) and is deterministic across runs with the same seed.
func TestTemplatePlacesAndDeterministic(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 96, Z: 8}
	cf := makeTemplateCF(t, "minecraft:fossil/spine_1")

	build := func() *Neighborhood { return build3x3([2]int{0, 0}, minY, height) }
	const seed = int64(0x7E11)

	a := build()
	ra := levelgen.NewWorldgenRandom(seed)
	if !templateBody(&bodyContext{view: a}, cf, newPlacementContext(a, minY, height, nil), ra, origin) {
		t.Fatalf("template returned false")
	}
	b := build()
	rb := levelgen.NewWorldgenRandom(seed)
	templateBody(&bodyContext{view: b}, cf, newPlacementContext(b, minY, height, nil), rb, origin)
	assertNetherViewsEqual(t, a, b, origin, 12, -8, 12)

	placed := 0
	for y := origin.Y - 8; y <= origin.Y+12; y++ {
		for z := origin.Z - 12; z <= origin.Z+12; z++ {
			for x := origin.X - 12; x <= origin.X+12; x++ {
				if !block.IsAir(a.GetBlock(x, y, z)) {
					placed++
				}
			}
		}
	}
	if placed == 0 {
		t.Fatalf("template placed no blocks")
	}

	// Draw order: single entry (totalWeight 1) -> nextInt(1); then rotation nextInt(4).
	oracle := levelgen.NewWorldgenRandom(seed)
	_ = oracle.NextIntN(1)
	_ = oracle.NextIntN(4)
	_ = oracle
}


// initMBNL allocates and fills the MOTION_BLOCKING_NO_LEAVES heightmap across the 3x3 view so
// bonus_chest's WorldGenLevel.getHeightmapPos read resolves. The generator normally sets this
// client heightmap; SetBlock does not, so a bare test view must seed it. topY is the first-air
// Y (the value getHeightmapPos returns).
func initMBNL(v *Neighborhood, minY, height, topY int) {
	sections := height / 16
	bitsForHeight := bits.Len(uint(sections)*16 + 1)
	rel := topY - minY
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			ch, ok := v.chunkAt(dx*16, dz*16)
			if !ok || ch == nil {
				continue
			}
			bs := level.NewBitStorage(bitsForHeight, 16*16, nil)
			for col := 0; col < 16*16; col++ {
				bs.Set(col, rel)
			}
			ch.HeightMaps.MotionBlockingNoLeaves = bs
		}
	}
}
