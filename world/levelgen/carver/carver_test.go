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

// carveBox is the bounding box of the carved positions in a solidChunk.
type carveBox struct {
	minX, maxX, minY, maxY, minZ, maxZ int
	count                              int
}

func boundingBox(c *solidChunk) carveBox {
	bb := carveBox{minX: 1 << 30, minY: 1 << 30, minZ: 1 << 30, maxX: -(1 << 30), maxY: -(1 << 30), maxZ: -(1 << 30)}
	for k := range c.blocks {
		x, y, z := k[0], k[1], k[2]
		if x < bb.minX {
			bb.minX = x
		}
		if x > bb.maxX {
			bb.maxX = x
		}
		if y < bb.minY {
			bb.minY = y
		}
		if y > bb.maxY {
			bb.maxY = y
		}
		if z < bb.minZ {
			bb.minZ = z
		}
		if z > bb.maxZ {
			bb.maxZ = z
		}
		bb.count++
	}
	return bb
}

// runCarverDirect drives one carver's carve() over a target chunk from a source
// chunk, seeded directly (bypassing the probability roll so the carve is forced for
// the shape tests). Returns the chunk for inspection.
func runCarverDirect(t *testing.T, conf *ConfiguredCarver, seed int64, target, src level.ChunkPos, fluid FluidSource) *solidChunk {
	t.Helper()
	rep, err := ParseReplaceables()
	if err != nil {
		t.Fatalf("parse replaceables: %v", err)
	}
	ch := newSolidChunk(target, -64, 384)
	mask := newCarvingMask(ch.MinY(), ch.Height())
	cc := &carveContext{
		chunk: ch, mask: mask, rep: rep, fluid: fluid,
		air:     block.ToStateID[block.Air{}],
		caveAir: block.ToStateID[block.CaveAir{}],
		water:   block.ToStateID[block.Water{Level: 0}],
		lava:    block.ToStateID[block.Lava{Level: 0}],
		minGenY: ch.MinY(),
	}
	rng := newLegacyRandom(0)
	rng.setLargeFeatureSeed(seed, int(src[0]), int(src[1]))
	conf.carver.carve(conf.cfg, cc, rng, src)
	return ch
}

// findCarvingSeed searches seeds 0..limit for one where the carver carves at least
// minBlocks into the target chunk from src.
func findCarvingSeed(t *testing.T, conf *ConfiguredCarver, target, src level.ChunkPos, fluid FluidSource, minBlocks, limit int) (int64, *solidChunk) {
	t.Helper()
	for s := int64(0); s < int64(limit); s++ {
		ch := runCarverDirect(t, conf, s, target, src, fluid)
		if len(ch.blocks) >= minBlocks {
			return s, ch
		}
	}
	t.Fatalf("no carving seed found in 0..%d (min %d blocks)", limit, minBlocks)
	return 0, nil
}

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

// --- Task 2 tests ---

// caveConf / canyonConf load a single ConfiguredCarver for the shape tests.
func caveConf(t *testing.T) *ConfiguredCarver {
	t.Helper()
	cfg, err := ParseCarverConfig("minecraft:cave")
	if err != nil {
		t.Fatalf("parse cave: %v", err)
	}
	return NewConfiguredCarver(cfg)
}

func canyonConf(t *testing.T) *ConfiguredCarver {
	t.Helper()
	cfg, err := ParseCarverConfig("minecraft:canyon")
	if err != nil {
		t.Fatalf("parse canyon: %v", err)
	}
	return NewConfiguredCarver(cfg)
}

func TestCaveCarves(t *testing.T) {
	conf := caveConf(t)
	target := level.ChunkPos{0, 0}
	// The cave starts in the target chunk itself; force-carve and require a
	// non-trivial connected tunnel of air.
	_, ch := findCarvingSeed(t, conf, target, target, dryFluid{}, 50, 5000)

	caveAir := block.ToStateID[block.CaveAir{}]
	if n := ch.countCarvedTo(caveAir); n < 50 {
		t.Fatalf("cave carved %d cave_air blocks, want >= 50", n)
	}
	// Bedrock floor (y=minY) must never be carved.
	for k := range ch.blocks {
		if k[1] == ch.MinY() {
			t.Fatalf("cave carved the bedrock floor at %v", k)
		}
	}
}

func TestCanyonCarvesRavine(t *testing.T) {
	conf := canyonConf(t)
	target := level.ChunkPos{0, 0}
	_, ch := findCarvingSeed(t, conf, target, target, dryFluid{}, 80, 20000)

	bb := boundingBox(ch)
	height := bb.maxY - bb.minY + 1
	widthX := bb.maxX - bb.minX + 1
	widthZ := bb.maxZ - bb.minZ + 1
	width := widthX
	if widthZ > width {
		width = widthZ
	}
	// The ravine signature: it is TALLER than it is WIDE within a chunk footprint.
	if height <= width {
		t.Fatalf("canyon not ravine-shaped: height %d, width %d (want height > width)", height, width)
	}
	t.Logf("ravine bbox: height=%d widthX=%d widthZ=%d count=%d", height, widthX, widthZ, bb.count)
}

func TestCarveDeterministic(t *testing.T) {
	conf := caveConf(t)
	target := level.ChunkPos{0, 0}
	seed, _ := findCarvingSeed(t, conf, target, target, dryFluid{}, 50, 5000)

	a := runCarverDirect(t, conf, seed, target, target, dryFluid{})
	b := runCarverDirect(t, conf, seed, target, target, dryFluid{})
	if len(a.blocks) != len(b.blocks) {
		t.Fatalf("non-deterministic: %d vs %d blocks", len(a.blocks), len(b.blocks))
	}
	for k, v := range a.blocks {
		if b.blocks[k] != v {
			t.Fatalf("non-deterministic at %v: %v vs %v", k, v, b.blocks[k])
		}
	}

	// Canyon determinism too.
	cn := canyonConf(t)
	cseed, _ := findCarvingSeed(t, cn, target, target, dryFluid{}, 80, 20000)
	ca := runCarverDirect(t, cn, cseed, target, target, dryFluid{})
	cb := runCarverDirect(t, cn, cseed, target, target, dryFluid{})
	for k, v := range ca.blocks {
		if cb.blocks[k] != v {
			t.Fatalf("canyon non-deterministic at %v: %v vs %v", k, v, cb.blocks[k])
		}
	}
}

func TestCarveContinuousAcrossChunks(t *testing.T) {
	conf := caveConf(t)
	target := level.ChunkPos{0, 0}
	// A carve seeded in a NEIGHBOR source chunk must still reach into the target
	// chunk (the [-8,8] carving range). Search for a seed where the cave, started in
	// neighbor (1,0), carves blocks that land inside target (0,0)'s footprint.
	src := level.ChunkPos{1, 0}
	found := false
	for s := int64(0); s < 30000 && !found; s++ {
		ch := runCarverDirect(t, conf, s, target, src, dryFluid{})
		if len(ch.blocks) > 0 {
			// All recorded blocks are in the target footprint (Set drops others), so
			// any carve from the neighbor source proves cross-chunk reach.
			found = true
			t.Logf("cross-chunk carve from src %v into target %v: %d blocks (seed %d)", src, target, len(ch.blocks), s)
		}
	}
	if !found {
		t.Fatal("no cross-chunk carve found: a carve seeded in a neighbor chunk never reached the target (chunk-edge seam bug)")
	}
}
