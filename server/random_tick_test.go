package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// random_tick_test.go — the RANDOM-TICK DRIVER gate (random_tick.go). It proves:
//   - getBlockRandomPos reproduces the jar's int-LCG bit math EXACTLY (against a hand-computed
//     reference of Level.getBlockRandomPos);
//   - the driver (via tickOnce -> tickWorld -> tickRandomBlocks, NOT a direct sugarCaneRandomTick
//     call) grows a sugar cane in a loaded, randomly-ticking chunk;
//   - a chunk with NO randomly-ticking block samples nothing (no state change, no RNG drawn);
//   - tickChunk draws EXACTLY randomTickSpeed positions per randomly-ticking section.

// newRandomTickLoop wires a TickLoop with one ready all-air chunk at (0,0) and a block-tick container,
// mirroring newSugarCaneLoop. The random-tick driver reads the SHARED world via the coordinator.
func newRandomTickLoop() (*TickLoop, *world.ChunkManager, *level.Chunk) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	// Open-sky test column: every section is fully sky-lit (level 15). These fixtures place a
	// handful of blocks (crops/farmland) into otherwise-empty air, so an open cell reads sky 15 —
	// exactly what the real LevelLightEngine computes for an unobstructed column. The light-read
	// seams (crop/growth getRawBrightness) now consult this, so the fixtures must carry the light
	// their real generated counterparts would. CITE: SkyLightEngine (open column => 15).
	for i := range ch.Sections {
		sky := make([]byte, 2048)
		for j := range sky {
			sky[j] = 0xFF
		}
		ch.Sections[i].SkyLight = sky
	}
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	return loop, mgr, ch
}

// refGetBlockRandomPos is an INDEPENDENT reference implementation of
// net.minecraft.world.level.Level.getBlockRandomPos, written straight from the jar bytecode (a
// separate transcription from the production nextBlockRandomPos so the test cross-checks the port
// rather than tautologically re-running it). It returns the new randValue and the sampled pos.
//
//	randValue = randValue*3 + 1013904223;  int val = randValue >> 2;
//	x = xo + (val & 15); y = yo + (val >> 16 & yMask); z = zo + (val >> 8 & 15);
func refGetBlockRandomPos(randValue int32, xo, yo, zo, yMask int) (int32, pk.Position) {
	rv := randValue*3 + 1013904223
	val := rv >> 2
	return rv, pk.Position{
		X: xo + int(val&0xF),
		Y: yo + int((val>>16)&int32(yMask)),
		Z: zo + int((val>>8)&0xF),
	}
}

// TestGetBlockRandomPosMatchesJar asserts the production nextBlockRandomPos reproduces the jar's
// getBlockRandomPos LCG bit math EXACTLY across a sweep of seeds and offsets — including the
// arithmetic-shift sign behavior for a negative randValue and int32 overflow of the multiply.
func TestGetBlockRandomPosMatchesJar(t *testing.T) {
	seeds := []int32{0, 1, -1, 12345, -999999, 2147483647, -2147483648, 0x5DEECE66}
	offsets := []struct{ xo, yo, zo int }{
		{0, 0, 0},
		{16, -64, 32},
		{-48, 240, -16},
	}
	for _, seed := range seeds {
		for _, off := range offsets {
			r := &region{randValue: seed}
			got := r.nextBlockRandomPos(off.xo, off.yo, off.zo, 15)
			wantRV, want := refGetBlockRandomPos(seed, off.xo, off.yo, off.zo, 15)
			if got != want {
				t.Fatalf("nextBlockRandomPos(seed=%d, %d,%d,%d): got %+v, want %+v (jar getBlockRandomPos)",
					seed, off.xo, off.yo, off.zo, got, want)
			}
			// The region's randValue must have advanced to the jar LCG value (side-effect fidelity).
			if r.randValue != wantRV {
				t.Fatalf("nextBlockRandomPos(seed=%d): randValue advanced to %d, want %d", seed, r.randValue, wantRV)
			}
			// Offsets must stay within the sampled 16x16x16 section (0..15 on X/Z, 0..15 off yo).
			if got.X < off.xo || got.X > off.xo+15 || got.Z < off.zo || got.Z > off.zo+15 ||
				got.Y < off.yo || got.Y > off.yo+15 {
				t.Fatalf("nextBlockRandomPos(seed=%d) escaped the 16-cube: %+v (min %d,%d,%d)",
					seed, got, off.xo, off.yo, off.zo)
			}
		}
	}
}

// firstSamplePos computes the FIRST position the seeded LCG samples for a section whose min block
// coords are (minX, minYInSection, minZ) — used to place a sugar cane exactly where tick 1's first
// sample lands, so growth is deterministic. It mirrors the driver's first draw for that seed.
func firstSamplePos(seed int32, minX, minYInSection, minZ int) pk.Position {
	_, pos := refGetBlockRandomPos(seed, minX, minYInSection, minZ, 15)
	return pos
}

// TestRandomTickGrowsSugarCane: a sugar cane on sand+water in a loaded chunk grows via the DRIVER
// (tickOnce -> tickWorld -> tickRandomBlocks), NOT a direct sugarCaneRandomTick call. The region's
// randValue is seeded so tick 1's first sample lands on the cane, so the AGE advances deterministically
// on the first tick.
func TestRandomTickGrowsSugarCane(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()

	// Seed the region's randValue so getBlockRandomPos is deterministic, then find where tick 1's
	// FIRST sample lands within the cane's section (section index for y in [64,79] is (64-(-64))/16 = 8,
	// minYInSection = -64 + 8*16 = 64; minX = minZ = 0 for column (0,0)).
	const seed = int32(777)
	loop.only().randValue = seed
	const minX, minZ = 0, 0
	const minYInSection = 64 // section 8 of the overworld world (dimMinY -64)
	target := firstSamplePos(seed, minX, minYInSection, minZ)

	// The sample cell must be growable: cell above empty + column < 3. Place sand two below (support),
	// water adjacent to the cell BELOW the cane, and the cane AT the sampled cell.
	basePos := pk.Position{X: target.X, Y: target.Y - 1, Z: target.Z}
	mgr.SetBlock(basePos, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(pk.Position{X: basePos.X + 1, Y: basePos.Y, Z: basePos.Z}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(target, sugarCane(0), dimMinY)
	// Ensure the cell above the cane is air (EmptyChunk is all air, but assert the target isn't at the
	// section top where the "above" cell leaves the section — reseat if so is unnecessary here since
	// EmptyChunk sections span the whole column; the above cell exists and is air).

	if got := block.SugarCaneAge(mustGet(t, mgr, target)); got != 0 {
		t.Fatalf("precondition: cane AGE = %d, want 0", got)
	}

	// Drive ONE full tick through the coordinator. tickWorld -> tickRandomBlocks samples the section;
	// the first sample (this seed) hits the cane and dispatchRandomTick -> sugarCaneRandomTick advances AGE.
	loop.tickOnce()

	if got := block.SugarCaneAge(mustGet(t, mgr, target)); got != 1 {
		t.Fatalf("after one driver tick the cane AGE = %d, want 1 (driver did not random-tick the cane)", got)
	}
}

// TestRandomTickNoRandomlyTickingSamplesNothing: a loaded chunk with NO randomly-ticking block is
// skipped by the driver — no section is sampled, no block state changes, and (critically) the region's
// randValue is NOT advanced (isRandomlyTicking gates the whole per-section draw loop).
func TestRandomTickNoRandomlyTickingSamplesNothing(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()

	// Fill a floor of stone (NOT randomly ticking) so the chunk has content but nothing to random-tick.
	fillFloor(ch, 64)

	const seed = int32(4242)
	loop.only().randValue = seed

	// Snapshot the floor state so we can prove nothing changed.
	floorPos := pk.Position{X: 3, Y: 64, Z: 5}
	before := mustGet(t, mgr, floorPos)

	loop.tickOnce()

	if loop.only().randValue != seed {
		t.Fatalf("randValue advanced to %d (want %d) — the driver sampled a non-randomly-ticking chunk (isRandomlyTicking gate broken)",
			loop.only().randValue, seed)
	}
	if got := mustGet(t, mgr, floorPos); got != before {
		t.Fatalf("floor block changed (%d -> %d) — the driver must not touch a non-randomly-ticking chunk", before, got)
	}
}

// TestRandomTickSpeedCountPerSection: tickChunk draws EXACTLY randomTickSpeed positions per
// randomly-ticking section. We place a single randomly-ticking block (sugar cane) in ONE section, seed
// randValue, run tickChunk once, and assert randValue advanced EXACTLY randomTickSpeed LCG steps (the
// getBlockRandomPos count) — proving the `for i := 0; i < tickSpeed; i++` loop runs tickSpeed times for
// the one ticking section and ZERO times for every non-ticking section.
func TestRandomTickSpeedCountPerSection(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()

	// One sugar cane in section 8 (y 64) makes exactly ONE section randomly-ticking; every other
	// section is all-air (not ticking). The cane need not be growable for the count test — the draw
	// happens regardless of whether the sampled cell is the cane.
	canePos := pk.Position{X: 7, Y: 64, Z: 9}
	mgr.SetBlock(canePos, sugarCane(0), dimMinY)

	const seed = int32(31337)
	loop.only().randValue = seed

	// Expected: randomTickSpeed LCG advances for the single ticking section. Compute the reference
	// randValue after exactly that many steps.
	wantRV := seed
	for i := 0; i < randomTickSpeed; i++ {
		wantRV, _ = refGetBlockRandomPos(wantRV, 0, 64, 0, 15)
	}

	loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)

	if loop.only().randValue != wantRV {
		t.Fatalf("tickChunk advanced randValue to %d, want %d (exactly %d draws for the one ticking section)",
			loop.only().randValue, wantRV, randomTickSpeed)
	}
}

// TestRandomTickSpeedZeroDisables: with tickSpeed 0 the driver draws nothing (the vanilla
// `if (tickSpeed > 0)` guard), even for a randomly-ticking section.
func TestRandomTickSpeedZeroDisables(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	mgr.SetBlock(pk.Position{X: 2, Y: 64, Z: 2}, sugarCane(0), dimMinY)
	const seed = int32(9)
	loop.only().randValue = seed

	loop.tickChunk(level.ChunkPos{0, 0}, ch, 0)

	if loop.only().randValue != seed {
		t.Fatalf("tickChunk with tickSpeed 0 advanced randValue to %d, want %d (must draw nothing)", loop.only().randValue, seed)
	}
}

// TestLavaRandomTickIgnitesFlammableNeighbour drives the wired fluid random-tick branch: a lava cell
// surrounded by a ring of flammable oak_planks (with air directly above each) ignites fire on the
// LavaFluid.randomTick side-ignite branch (i == random.nextInt(3) == 0 -> for k<3: m =
// pos.offset(nextInt(3)-1, 0, nextInt(3)-1); if isEmptyBlock(m.above()) && isFlammable(m) ->
// setBlockAndUpdate(m.above(), fire)). Seed 10 makes the first draw i==0 and the first side offset
// (-1,-1), so m == the planks at (7,64,7) and fire lands at (7,65,7). CITE: LavaFluid.randomTick
// (side-ignite branch) / hasFlammableNeighbours / isFlammable; BaseFireBlock.getState.
func TestLavaRandomTickIgnitesFlammableNeighbour(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(10)

	lavaPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(lavaPos, block.ToStateID[block.Lava{}], dimMinY) // source lava

	// Ring the lava with oak_planks at its own y-level, leaving air directly above each plank so the
	// side-ignite branch (ignite the cell ABOVE a flammable m) can place fire.
	planks := block.ToStateID[block.OakPlanks{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue // the lava cell itself
			}
			mgr.SetBlock(pk.Position{X: 8 + dx, Y: 64, Z: 8 + dz}, planks, dimMinY)
			// (8+dx, 65, 8+dz) stays air (EmptyChunk default) so fire can be placed there.
		}
	}

	// Sanity: hasFlammableNeighbours must see the plank ring around the lava (used by the above-air
	// branch, and confirms the fire ignite table backs isFlammable).
	if !loop.lavaHasFlammableNeighbours(lavaPos) {
		t.Fatal("lava should have flammable neighbours (the oak_planks ring)")
	}

	// Drive the wired dispatch exactly as tickChunk does: state at the sampled pos -> fluid branch.
	loop.dispatchFluidRandomTick(loop.only(), block.ToStateID[block.Lava{}], lavaPos)

	// Seed 10 -> first side offset (-1,-1): m == (7,64,7) (a plank), ignite (7,65,7).
	ignited := pk.Position{X: 7, Y: 65, Z: 7}
	got, _ := mgr.GetBlock(ignited, dimMinY)
	if !block.IsFire(got) {
		t.Fatalf("lava randomTick did not ignite the flammable neighbour above (7,64,7): state at %v is not fire", ignited)
	}
}

// TestDispatchFluidRandomTickNonLavaDrawsNothing pins the pig-oracle safety property: the wired fluid
// branch only ticks LAVA (Fluid.isRandomlyTicking is overridden true only by LavaFluid). For a water
// or non-fluid sampled state it draws ZERO levelRandom and mutates nothing, so a lava-free column's
// random-tick RNG draw order is identical to before the fluid branch was wired. CITE:
// FluidState.isRandomlyTicking -> Fluid.isRandomlyTicking; WaterFluid inherits FlowingFluid false.
func TestDispatchFluidRandomTickNonLavaDrawsNothing(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(12345)

	// Snapshot the levelRandom stream before the dispatch.
	before := loop.only().levelRandom.NextLong()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(12345)

	waterPos := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(waterPos, block.ToStateID[block.Water{}], dimMinY)
	stonePos := pk.Position{X: 5, Y: 64, Z: 5}
	mgr.SetBlock(stonePos, block.ToStateID[block.Stone{}], dimMinY)

	// Water and a non-fluid solid: both must be no-ops that draw no RNG.
	loop.dispatchFluidRandomTick(loop.only(), block.ToStateID[block.Water{}], waterPos)
	loop.dispatchFluidRandomTick(loop.only(), block.ToStateID[block.Stone{}], stonePos)

	after := loop.only().levelRandom.NextLong()
	if before != after {
		t.Fatalf("non-lava fluid dispatch perturbed levelRandom: first draw before=%d after=%d (want equal)", before, after)
	}
}
