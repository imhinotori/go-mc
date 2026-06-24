package carver

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// --- test doubles ---

// solidChunk is a test CarveChunk: a 16x16xH column pre-filled with a solid
// replaceable block (stone), bounded to the target chunk's footprint. Set writes
// outside the 16x16 column are dropped (mirroring the real generator's footprint
// bound). It records carved positions for assertions.
type solidChunk struct {
	pos    level.ChunkPos
	minY   int
	height int
	blocks map[[3]int]block.StateID
	stone  block.StateID
	bedrock block.StateID
}

func newSolidChunk(pos level.ChunkPos, minY, height int) *solidChunk {
	c := &solidChunk{
		pos:     pos,
		minY:    minY,
		height:  height,
		blocks:  map[[3]int]block.StateID{},
		stone:   block.ToStateID[block.Stone{}],
		bedrock: block.ToStateID[block.Bedrock{}],
	}
	return c
}

func (c *solidChunk) Pos() level.ChunkPos { return c.pos }
func (c *solidChunk) MinY() int           { return c.minY }
func (c *solidChunk) Height() int          { return c.height }

func (c *solidChunk) inFootprint(wx, wz int) bool {
	baseX := int(c.pos[0]) * 16
	baseZ := int(c.pos[1]) * 16
	return wx >= baseX && wx < baseX+16 && wz >= baseZ && wz < baseZ+16
}

func (c *solidChunk) Get(wx, wy, wz int) block.StateID {
	if st, ok := c.blocks[[3]int{wx, wy, wz}]; ok {
		return st
	}
	if wy == c.minY {
		return c.bedrock // a bedrock floor that must NOT be carved
	}
	return c.stone // solid replaceable default
}

func (c *solidChunk) Set(wx, wy, wz int, state block.StateID) {
	if !c.inFootprint(wx, wz) {
		return // drop out-of-footprint writes (only the target chunk is edited)
	}
	c.blocks[[3]int{wx, wy, wz}] = state
}

// countAir returns how many blocks in the target footprint were carved to air/cave_air.
func (c *solidChunk) countCarvedTo(states ...block.StateID) int {
	want := map[block.StateID]bool{}
	for _, s := range states {
		want[s] = true
	}
	n := 0
	for _, st := range c.blocks {
		if want[st] {
			n++
		}
	}
	return n
}

// dryFluid is a FluidSource that always returns air (above the water table).
type dryFluid struct{}

func (dryFluid) CarveFluid(wx, wy, wz int) (block.StateID, bool) { return 0, false }

// floodBelow is a FluidSource with a flat water table at level: any carve below
// `level` floods with water, at/above is air.
type floodBelow struct {
	level int
	water block.StateID
}

func (f floodBelow) CarveFluid(wx, wy, wz int) (block.StateID, bool) {
	if wy < f.level {
		return f.water, true
	}
	return 0, false
}

const testSeed = int64(123456789)

// --- Task 1 tests ---

func TestParseCarverConfigs(t *testing.T) {
	cave, err := ParseCarverConfig("minecraft:cave")
	if err != nil {
		t.Fatalf("parse cave: %v", err)
	}
	if cave.Kind != KindCave {
		t.Errorf("cave kind = %v, want KindCave", cave.Kind)
	}
	if cave.Probability != 0.15 {
		t.Errorf("cave probability = %v, want 0.15", cave.Probability)
	}
	if cave.HorizontalRadiusMultiplier.kind != "uniform" {
		t.Errorf("cave horizontalRadiusMultiplier kind = %q, want uniform", cave.HorizontalRadiusMultiplier.kind)
	}

	canyon, err := ParseCarverConfig("minecraft:canyon")
	if err != nil {
		t.Fatalf("parse canyon: %v", err)
	}
	if canyon.Kind != KindCanyon {
		t.Errorf("canyon kind = %v, want KindCanyon", canyon.Kind)
	}
	if canyon.Probability != 0.01 {
		t.Errorf("canyon probability = %v, want 0.01", canyon.Probability)
	}
	// canyon y is uniform absolute 10..67.
	if got := canyon.Y.min.resolveY(-64); got != 10 {
		t.Errorf("canyon y.min = %d, want 10", got)
	}
	if got := canyon.Y.max.resolveY(-64); got != 67 {
		t.Errorf("canyon y.max = %d, want 67", got)
	}
	if canyon.Shape.thickness.kind != "trapezoid" {
		t.Errorf("canyon shape.thickness kind = %q, want trapezoid", canyon.Shape.thickness.kind)
	}
	if canyon.Shape.widthSmoothness != 3 {
		t.Errorf("canyon shape.widthSmoothness = %d, want 3", canyon.Shape.widthSmoothness)
	}

	// cave_extra_underground must also parse (it's in the plains carver list).
	if _, err := ParseCarverConfig("minecraft:cave_extra_underground"); err != nil {
		t.Fatalf("parse cave_extra_underground: %v", err)
	}
}

func TestCanReplaceGate(t *testing.T) {
	rep, err := ParseReplaceables()
	if err != nil {
		t.Fatalf("parse replaceables: %v", err)
	}
	if rep.Size() == 0 {
		t.Fatal("replaceables set is empty")
	}
	cc := &carveContext{rep: rep}

	stone := block.ToStateID[block.Stone{}]
	deepslate := block.ToStateID[block.Deepslate{Axis: block.Y}]
	bedrock := block.ToStateID[block.Bedrock{}]

	if !cc.canReplaceBlock(stone) {
		t.Error("stone must be replaceable")
	}
	if !cc.canReplaceBlock(deepslate) {
		t.Error("deepslate must be replaceable (base_stone_overworld)")
	}
	if cc.canReplaceBlock(bedrock) {
		t.Error("bedrock must NOT be replaceable")
	}
}

func TestApplyCarversSeeded(t *testing.T) {
	carvers, err := LoadOverworldCarvers()
	if err != nil {
		t.Fatalf("load carvers: %v", err)
	}
	rep, err := ParseReplaceables()
	if err != nil {
		t.Fatalf("parse replaceables: %v", err)
	}

	run := func() *solidChunk {
		ch := newSolidChunk(level.ChunkPos{0, 0}, -64, 384)
		ApplyCarvers(testSeed, ch, dryFluid{}, carvers, rep)
		return ch
	}

	a := run()
	b := run()
	// Determinism: same seed + chunk -> identical carve.
	if len(a.blocks) != len(b.blocks) {
		t.Fatalf("non-deterministic carve: %d vs %d carved blocks", len(a.blocks), len(b.blocks))
	}
	for k, v := range a.blocks {
		if b.blocks[k] != v {
			t.Fatalf("non-deterministic carve at %v: %v vs %v", k, v, b.blocks[k])
		}
	}
}

func TestCarverAquiferAware(t *testing.T) {
	// Inject a trivial test carver that carves a single column straddling the water
	// table, so the aquifer rule is isolated from the cave/canyon walk (ported in
	// Task 2). Below the table -> water; at/above -> cave_air.
	rep, err := ParseReplaceables()
	if err != nil {
		t.Fatalf("parse replaceables: %v", err)
	}
	water := block.ToStateID[block.Water{Level: 0}]
	caveAir := block.ToStateID[block.CaveAir{}]

	ch := newSolidChunk(level.ChunkPos{0, 0}, -64, 384)
	mask := newCarvingMask(ch.MinY(), ch.Height())
	cfg := &CarverConfig{LavaLevel: verticalAnchor{aboveBottom: true, value: -100000}} // lava far below
	cc := &carveContext{
		chunk: ch, mask: mask, rep: rep,
		fluid:   floodBelow{level: 30, water: water},
		air:     block.ToStateID[block.Air{}],
		caveAir: caveAir,
		water:   water,
		lava:    block.ToStateID[block.Lava{Level: 0}],
		minGenY: ch.MinY(),
	}

	// Carve y=20 (below table -> water) and y=40 (above -> cave_air).
	if !cc.carveBlock(cfg, 5, 20, 5) {
		t.Fatal("expected carve at y=20")
	}
	if !cc.carveBlock(cfg, 5, 40, 5) {
		t.Fatal("expected carve at y=40")
	}
	if got := ch.Get(5, 20, 5); got != water {
		t.Errorf("y=20 below water table: got %v, want water %v", got, water)
	}
	if got := ch.Get(5, 40, 5); got != caveAir {
		t.Errorf("y=40 above water table: got %v, want cave_air %v", got, caveAir)
	}
}
