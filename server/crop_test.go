package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// crop_test.go — CROP GROWTH + FARMLAND MOISTURE gate (crop_block.go). It proves:
//   - the per-crop MAX_AGE / AGE helpers match the jar (wheat/carrots/potatoes 7, beetroots 3);
//   - getGrowthSpeed reproduces the jar formula exactly across the on-farmland/moist/same-crop cases;
//   - the growth roll bound is (int)(25/speed)+1;
//   - a wheat crop on hydrated farmland under (cited) light>=9 advances its age toward 7 via the
//     DRIVER (tickChunk), NOT a direct cropRandomTick call;
//   - farmland moisture drops by 1 per random tick when not near water and not raining.

// wheat / beetroots state helpers for tests.
func wheat(age int) block.StateID {
	s, ok := block.CropStateForAge(block.ToStateID[block.Wheat{}], age)
	if !ok {
		panic("no wheat state")
	}
	return s
}

func beetroots(age int) block.StateID {
	s, ok := block.CropStateForAge(block.ToStateID[block.Beetroots{}], age)
	if !ok {
		panic("no beetroot state")
	}
	return s
}

func farmland(moisture int) block.StateID {
	s, ok := block.FarmlandState(moisture)
	if !ok {
		panic("no farmland state")
	}
	return s
}

// TestCropMaxAgeMatchesJar: MAX_AGE per family (CropBlock.getMaxAge==7, BeetrootBlock.getMaxAge==3).
func TestCropMaxAgeMatchesJar(t *testing.T) {
	cases := []struct {
		name string
		s    block.StateID
		want int
	}{
		{"wheat", wheat(0), 7},
		{"carrots", block.ToStateID[block.Carrots{}], 7},
		{"potatoes", block.ToStateID[block.Potatoes{}], 7},
		{"beetroots", beetroots(0), 3},
	}
	for _, c := range cases {
		if got := block.CropMaxAge(c.s); got != c.want {
			t.Errorf("%s CropMaxAge = %d, want %d", c.name, got, c.want)
		}
	}
	// A max-age crop is NOT randomly ticking (CropBlock.isRandomlyTicking == !isMaxAge).
	if block.IsRandomlyTicking(wheat(7)) {
		t.Error("wheat age 7 (max) must NOT be randomly ticking")
	}
	if !block.IsRandomlyTicking(wheat(6)) {
		t.Error("wheat age 6 (<max) must be randomly ticking")
	}
	if block.IsRandomlyTicking(beetroots(3)) {
		t.Error("beetroots age 3 (max) must NOT be randomly ticking")
	}
	if !block.IsRandomlyTicking(farmland(0)) {
		t.Error("farmland must be randomly ticking (any moisture)")
	}
}

// refGrowthSpeed is an INDEPENDENT transcription of CropBlock.getGrowthSpeed from the jar bytecode,
// so the production cropGrowthSpeed is cross-checked rather than tautologically re-run. below/around
// reads come from a small (pos -> stateID) map; a missing cell reads as air (state 0).
func refGrowthSpeed(get func(p pk.Position) block.StateID, self block.StateID, pos pk.Position) float32 {
	var f float32 = 1.0
	below := pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			var g float32 = 0.0
			cs := get(pk.Position{X: below.X + i, Y: below.Y, Z: below.Z + j})
			if block.GrowsCrops(cs) {
				g = 1.0
				if block.FarmlandMoisture(cs) > 0 {
					g = 3.0
				}
			}
			if i != 0 || j != 0 {
				g /= 4.0
			}
			f += g
		}
	}
	same := func(p pk.Position) bool { return block.SameCropBlock(self, get(p)) }
	n := pk.Position{X: pos.X, Y: pos.Y, Z: pos.Z - 1}
	s := pk.Position{X: pos.X, Y: pos.Y, Z: pos.Z + 1}
	w := pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z}
	e := pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z}
	flagWE := same(w) || same(e)
	flagNS := same(n) || same(s)
	if flagWE && flagNS {
		f /= 2.0
	} else if same(pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z - 1}) ||
		same(pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z - 1}) ||
		same(pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z + 1}) ||
		same(pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z + 1}) {
		f /= 2.0
	}
	return f
}

// TestCropGrowthSpeedMatchesJar drives cropGrowthSpeed against the reference for a set of layouts:
// bare ground (speed 1), dry farmland below (2), moist farmland below (4), and a boxed-in same-crop
// neighbourhood (penalty). It builds each layout in a real chunk and compares to refGrowthSpeed.
func TestCropGrowthSpeedMatchesJar(t *testing.T) {
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	self := wheat(0)

	type layout struct {
		name  string
		build func(m *world.ChunkManager)
		want  float32
	}
	layouts := []layout{
		{
			name:  "bare_ground",
			build: func(m *world.ChunkManager) {},
			want:  1.0, // no farmland anywhere -> f stays 1.0
		},
		{
			name: "dry_farmland_below",
			build: func(m *world.ChunkManager) {
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, farmland(0), dimMinY)
			},
			want: 1.0 + 1.0, // center farmland (dry) g=1.0, no /4 -> f = 2.0
		},
		{
			name: "moist_farmland_below",
			build: func(m *world.ChunkManager) {
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, farmland(7), dimMinY)
			},
			want: 1.0 + 3.0, // center farmland (moist) g=3.0 -> f = 4.0
		},
		{
			name: "moist_center_plus_dry_side",
			build: func(m *world.ChunkManager) {
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, farmland(7), dimMinY)
				// a side farmland (dry): g=1.0 then /4 -> +0.25
				m.SetBlock(pk.Position{X: pos.X + 1, Y: pos.Y - 1, Z: pos.Z}, farmland(0), dimMinY)
			},
			want: 1.0 + 3.0 + 0.25,
		},
		{
			name: "boxed_same_crop_ns_we",
			build: func(m *world.ChunkManager) {
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, farmland(7), dimMinY)
				// same crop on all four cardinals -> flagWE && flagNS -> f /= 2
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, wheat(0), dimMinY)
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, wheat(0), dimMinY)
				m.SetBlock(pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, wheat(0), dimMinY)
				m.SetBlock(pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, wheat(0), dimMinY)
			},
			want: (1.0 + 3.0) / 2.0,
		},
		{
			name: "diagonal_same_crop_penalty",
			build: func(m *world.ChunkManager) {
				m.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, farmland(7), dimMinY)
				// a single diagonal same-crop -> not (flagWE && flagNS), but flagDiag -> f /= 2
				m.SetBlock(pk.Position{X: pos.X - 1, Y: pos.Y, Z: pos.Z - 1}, wheat(1), dimMinY)
			},
			want: (1.0 + 3.0) / 2.0,
		},
	}

	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			loop, mgr, _ := newRandomTickLoop()
			l.build(mgr)
			got := loop.cropGrowthSpeed(self, pos)
			ref := refGrowthSpeed(func(p pk.Position) block.StateID {
				s, ok := mgr.GetBlock(p, dimMinY)
				if !ok {
					return 0
				}
				return s
			}, self, pos)
			if got != ref {
				t.Fatalf("cropGrowthSpeed = %v, reference = %v", got, ref)
			}
			if got != l.want {
				t.Fatalf("cropGrowthSpeed = %v, want %v (jar formula)", got, l.want)
			}
		})
	}
}

// TestCropGrowthRollBound asserts the growth roll bound is (int)(25.0F/speed)+1 for representative
// speeds — the exact jar expression `random.nextInt((int)(25.0F / speed) + 1)`.
func TestCropGrowthRollBound(t *testing.T) {
	cases := []struct {
		speed float32
		want  int32
	}{
		{1.0, 26},  // 25/1 = 25 -> +1 = 26
		{2.0, 13},  // 25/2 = 12.5 -> (int)12 -> +1 = 13
		{4.0, 7},   // 25/4 = 6.25 -> 6 -> +1 = 7
		{25.0, 2},  // 25/25 = 1.0 -> 1 -> +1 = 2
	}
	for _, c := range cases {
		got := int32(float32(cropGrowthSpeedDivisor)/c.speed) + 1
		if got != c.want {
			t.Errorf("bound(speed=%v) = %d, want %d", c.speed, got, c.want)
		}
	}
}

// TestDriverGrowsWheat: a wheat crop at age 0 on hydrated farmland (speed 4 -> roll bound 7) grows
// toward MAX_AGE 7 when the random-tick DRIVER (tickChunk) samples it over many ticks. levelRandom
// and randValue are seeded for a fully deterministic run; the crop reaches max age within the budget.
func TestDriverGrowsWheat(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()

	// Seed both RNG streams deterministically. randValue drives WHERE the driver samples; levelRandom
	// drives the growth roll.
	loop.only().randValue = 12345
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xC0FFEE)

	// Layout: moist farmland below the wheat so speed == 4.0 (roll bound 7). A water block within the
	// farmland's isNearWater box (9x2x9 around the farmland) keeps it re-hydrated to moisture 7 each
	// farmland random tick, so the growth speed stays 4.0 for the whole run (the farmland never dries
	// and never turns to dirt). A single center farmland gives f = 1 + 3 = 4.0.
	cropPos := pk.Position{X: 7, Y: 65, Z: 9}
	fpos := pk.Position{X: cropPos.X, Y: cropPos.Y - 1, Z: cropPos.Z}
	mgr.SetBlock(fpos, farmland(7), dimMinY)
	mgr.SetBlock(pk.Position{X: fpos.X + 2, Y: fpos.Y, Z: fpos.Z}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(cropPos, wheat(0), dimMinY)

	if got := block.CropAge(mustGet(t, mgr, cropPos)); got != 0 {
		t.Fatalf("precondition wheat age = %d, want 0", got)
	}

	// Drive the crop's chunk many times. Each tickChunk samples randomTickSpeed positions per ticking
	// section; over enough ticks the crop cell is sampled repeatedly and (roll==0) advances its age.
	// The run is deterministic (fixed seeds), so this terminates at max age well within the budget.
	const maxTicks = 1000000
	grew := false
	for i := 0; i < maxTicks; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
		age := block.CropAge(mustGet(t, mgr, cropPos))
		if age > 0 {
			grew = true
		}
		if age >= 7 {
			break
		}
	}
	final := block.CropAge(mustGet(t, mgr, cropPos))
	if !grew {
		t.Fatalf("wheat never advanced past age 0 over %d driver ticks", maxTicks)
	}
	if final != 7 {
		t.Fatalf("wheat reached age %d, want 7 (MAX_AGE) within %d ticks", final, maxTicks)
	}
	// Once at max age the crop is no longer randomly ticking.
	if block.IsRandomlyTicking(mustGet(t, mgr, cropPos)) {
		t.Fatal("wheat at max age must stop being randomly ticking")
	}
}

// TestDriverBeetrootCapsAtThree: beetroots grow via the driver but cap at MAX_AGE 3 (never 7).
func TestDriverBeetrootCapsAtThree(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	loop.only().randValue = 4242
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xBEE7)

	cropPos := pk.Position{X: 5, Y: 65, Z: 6}
	fpos := pk.Position{X: cropPos.X, Y: cropPos.Y - 1, Z: cropPos.Z}
	mgr.SetBlock(fpos, farmland(7), dimMinY)
	// Keep the farmland hydrated (water in its isNearWater box) so speed stays 4.0; beetroot's extra
	// 2/3 nextInt(3) gate already slows growth, so a steady high speed keeps the budget reasonable.
	mgr.SetBlock(pk.Position{X: fpos.X + 2, Y: fpos.Y, Z: fpos.Z}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(cropPos, beetroots(0), dimMinY)

	const maxTicks = 1000000
	for i := 0; i < maxTicks; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
		if block.CropAge(mustGet(t, mgr, cropPos)) >= 3 {
			break
		}
	}
	final := block.CropAge(mustGet(t, mgr, cropPos))
	if final != 3 {
		t.Fatalf("beetroot reached age %d, want 3 (MAX_AGE) within %d ticks", final, maxTicks)
	}
	// Run more ticks: it must never exceed 3 (isRandomlyTicking is false at max age).
	for i := 0; i < 10000; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
	}
	if got := block.CropAge(mustGet(t, mgr, cropPos)); got != 3 {
		t.Fatalf("beetroot grew past max age to %d, want capped at 3", got)
	}
}

// TestFarmlandMoistureDropsWithoutWater: a farmland with no water in range and no rain loses one
// moisture per random tick (FarmlandBlock.randomTick dry branch: m>0 -> m-1). Driven by tickChunk.
func TestFarmlandMoistureDropsWithoutWater(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	loop.only().randValue = 999
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(1)

	fpos := pk.Position{X: 8, Y: 65, Z: 8}
	mgr.SetBlock(fpos, farmland(7), dimMinY)

	before := block.FarmlandMoisture(mustGet(t, mgr, fpos))
	if before != 7 {
		t.Fatalf("precondition moisture = %d, want 7", before)
	}

	// Drive until the farmland is sampled at least once and moisture drops. Deterministic run.
	dropped := false
	const maxTicks = 200000
	for i := 0; i < maxTicks; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
		if block.FarmlandMoisture(mustGet(t, mgr, fpos)) < before {
			dropped = true
			break
		}
	}
	if !dropped {
		t.Fatalf("farmland moisture never dropped over %d driver ticks (no water, no rain)", maxTicks)
	}
	after := block.FarmlandMoisture(mustGet(t, mgr, fpos))
	if after != before-1 {
		t.Fatalf("farmland moisture dropped to %d, want exactly %d (one step per random tick)", after, before-1)
	}
}

// TestFarmlandDriesToDirt: a moisture-0 farmland with no water, no rain, and NO maintaining block
// above turns to dirt on its next random tick (FarmlandBlock.randomTick: m==0 &&
// !shouldMaintainFarmland -> turnToDirt).
func TestFarmlandDriesToDirt(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	loop.only().randValue = 7777
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(2)

	fpos := pk.Position{X: 8, Y: 65, Z: 8}
	mgr.SetBlock(fpos, farmland(0), dimMinY) // dry, nothing above (air is not MAINTAINS_FARMLAND)

	became := false
	const maxTicks = 200000
	for i := 0; i < maxTicks; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
		if block.IsAir(mustGet(t, mgr, fpos)) || !block.IsFarmland(mustGet(t, mgr, fpos)) {
			became = true
			break
		}
	}
	if !became {
		t.Fatalf("dry unmaintained farmland never turned to dirt over %d ticks", maxTicks)
	}
	if got := mustGet(t, mgr, fpos); got != block.DefaultStateID["minecraft:dirt"] {
		t.Fatalf("farmland turned into state %d, want dirt %d", got, block.DefaultStateID["minecraft:dirt"])
	}
}

// TestFarmlandMaintainedByCropAbove: dry (moisture 0) farmland with WHEAT above is NOT turned to
// dirt (shouldMaintainFarmland: wheat is in #maintains_farmland).
func TestFarmlandMaintainedByCropAbove(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	loop.only().randValue = 314159
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(3)

	fpos := pk.Position{X: 8, Y: 65, Z: 8}
	mgr.SetBlock(fpos, farmland(0), dimMinY)
	mgr.SetBlock(above(fpos), wheat(2), dimMinY) // wheat above maintains the farmland

	// Drive many ticks; the farmland must remain farmland (never turns to dirt) at moisture 0.
	for i := 0; i < 50000; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
	}
	if got := mustGet(t, mgr, fpos); !block.IsFarmland(got) {
		t.Fatalf("maintained farmland turned into state %d, want it to stay farmland", got)
	}
}
