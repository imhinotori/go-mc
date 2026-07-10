package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// stem_test.go -- the STEM (pumpkin + melon) random-tick gate (stem.go). It proves:
//   - the growth roll advances AGE below 7 at the right light + a 0 growth roll;
//   - at AGE 7 a SECOND draw (getRandomDirection == nextInt(4)) picks the fruit-spawn direction and
//     the fruit + attached stem (with FACING) are placed on a #supports_stem_fruit ground with air
//     in front;
//   - the light gate below 9 blocks growth entirely (no draw).
// It uses the shared newRandomTickLoop / mustGet helpers.

func pumpkinStemState(age int) block.StateID {
	s, ok := block.StemWithAge(block.ToStateID[block.PumpkinStem{}], age)
	if !ok {
		panic("no pumpkin stem state")
	}
	return s
}

// stemGrowthBound recomputes StemBlock.randomTick's growth-roll bound for the given position so the
// test can find a seed whose FIRST nextInt(bound) draw is 0 (== grow). It mirrors the handler's
// (int)(25.0F/speed)+1 exactly. CITE: StemBlock.randomTick.
func stemGrowthBound(loop *TickLoop, state block.StateID, pos pk.Position) int32 {
	speed := loop.cropGrowthSpeed(state, pos)
	return int32(float32(cropGrowthSpeedDivisor)/speed) + 1
}

// findStemGrowSeed returns a seed whose FIRST NextIntN(bound) draw is 0 (so the growth roll grows).
func findStemGrowSeed(bound int32) int64 {
	for seed := int64(1); seed < 100000; seed++ {
		r := levelgen.NewLegacyRandomSource(seed)
		if r.NextIntN(bound) == 0 {
			return seed
		}
	}
	panic("no grow seed found")
}

// TestStemGrowthRollAdvancesAge: a pumpkin stem below AGE 7, lit (open-sky sky 15 => rawBrightness
// >= 9), advances AGE by one on a 0 growth roll. CITE: StemBlock.randomTick (age<7 branch).
func TestStemGrowthRollAdvancesAge(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	stem := pk.Position{X: 5, Y: 65, Z: 5}
	// Moist farmland directly below the stem raises getGrowthSpeed and shrinks the bound.
	mgr.SetBlock(below(stem), farmland(7), dimMinY)
	mgr.SetBlock(stem, pumpkinStemState(3), dimMinY)

	bound := stemGrowthBound(loop, pumpkinStemState(3), stem)
	seed := findStemGrowSeed(bound)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	loop.stemRandomTick(r, pumpkinStemState(3), stem)

	if got := block.StemAge(mustGet(t, mgr, stem)); got != 4 {
		t.Fatalf("after a 0 growth roll the stem AGE = %d, want 4", got)
	}
}

// TestStemLightGateBlocksGrowth: below light 9 the handler returns BEFORE the growth roll -- no draw,
// no AGE change. We darken the cell by capping sky light to 0 in the stem's section. CITE:
// StemBlock.randomTick (getRawBrightness(pos, 0) < 9 return).
func TestStemLightGateBlocksGrowth(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	r := loop.only()
	// Zero the sky light in every section so rawBrightness < 9 everywhere.
	for i := range ch.Sections {
		ch.Sections[i].SkyLight = make([]byte, 2048)
	}
	const seed = int64(1234)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	stem := pk.Position{X: 6, Y: 65, Z: 6}
	mgr.SetBlock(below(stem), farmland(7), dimMinY)
	mgr.SetBlock(stem, pumpkinStemState(2), dimMinY)

	loop.stemRandomTick(r, pumpkinStemState(2), stem)

	if got := block.StemAge(mustGet(t, mgr, stem)); got != 2 {
		t.Fatalf("dark stem AGE = %d, want 2 (light gate must block growth with no draw)", got)
	}
	// The light gate returns before ANY draw, so the stream is untouched.
	if r.levelRandom.NextIntN(1000000) != levelgen.NewLegacyRandomSource(seed).NextIntN(1000000) {
		t.Fatal("light-gated stem drew levelRandom; it must draw nothing")
	}
}

// TestStemAge7SpawnsFruitSecondDraw: at AGE 7, a 0 growth roll then a SECOND draw
// (getRandomDirection == nextInt(4)) spawns the fruit in the picked horizontal direction (air cell
// with #supports_stem_fruit ground below) and replaces the stem with the attached stem facing that
// direction. CITE: StemBlock.randomTick (age==7 fruit-spawn path).
func TestStemAge7SpawnsFruitSecondDraw(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	stem := pk.Position{X: 8, Y: 65, Z: 8}
	mgr.SetBlock(below(stem), farmland(7), dimMinY)
	mgr.SetBlock(stem, pumpkinStemState(7), dimMinY)
	// Surround the stem with dirt ground one below the four horizontal neighbours (so whichever
	// direction the second draw picks, the fruit-support + air-in-front gates are satisfied). The
	// neighbour cells at stem.Y are air (EmptyChunk), and dirt is in #supports_vegetation.
	for _, f := range stemHorizontalFaces {
		ground := pk.Position{X: stem.X + f.dx, Y: stem.Y - 1, Z: stem.Z + f.dz}
		mgr.SetBlock(ground, block.DefaultStateID["minecraft:dirt"], dimMinY)
	}

	// Find a seed whose FIRST draw (growth roll, bound at AGE7) is 0 so the fruit path runs; the
	// second draw then picks the direction.
	bound := stemGrowthBound(loop, pumpkinStemState(7), stem)
	seed := findStemGrowSeed(bound)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	// Independently compute the direction the SECOND draw will pick (nextInt(4) into the faces array).
	ref := levelgen.NewLegacyRandomSource(seed)
	ref.NextIntN(bound) // consume the growth roll
	idx := ref.NextIntN(4)
	wantFace := stemHorizontalFaces[idx]
	wantFruit := pk.Position{X: stem.X + wantFace.dx, Y: stem.Y, Z: stem.Z + wantFace.dz}

	loop.stemRandomTick(r, pumpkinStemState(7), stem)

	// The fruit cell must now hold a pumpkin.
	if !isPumpkinState(mustGet(t, mgr, wantFruit)) {
		t.Fatalf("AGE-7 stem did not spawn a pumpkin at %+v (idx=%d); got state %d", wantFruit, idx, mustGet(t, mgr, wantFruit))
	}
	// The stem cell must now hold an attached pumpkin stem facing the picked direction.
	got := mustGet(t, mgr, stem)
	facing, ok := attachedPumpkinStemFacing(got)
	if !ok {
		t.Fatalf("AGE-7 stem did not convert to an attached pumpkin stem; got state %d", got)
	}
	if facing != wantFace.dir {
		t.Fatalf("attached stem FACING = %v, want %v (direction of the second draw)", facing, wantFace.dir)
	}
}

// isPumpkinState reports whether a state id is Blocks.PUMPKIN. Local test helper.
func isPumpkinState(s block.StateID) bool {
	want, ok := block.ToStateID[block.Pumpkin{}]
	return ok && s == want
}

// attachedPumpkinStemFacing returns the FACING of an attached-pumpkin-stem state, ok=false otherwise.
func attachedPumpkinStemFacing(s block.StateID) (block.Direction, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return 0, false
	}
	if a, ok := block.StateList[s].(block.AttachedPumpkinStem); ok {
		return a.Facing, true
	}
	return 0, false
}
