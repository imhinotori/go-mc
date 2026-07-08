package server

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid_direction_test.go pins the DIRECTION-DISPATCHED replaceability model (the fix to the
// wrong home-grown "stronger amount replaces weaker" gate). Every case is a jar-behavior gate on
// canBeReplacedWith (WaterFluid/LavaFluid/EmptyFluid) + the per-direction getNewLiquid threaded
// through spread/getSpread. CITE: net.minecraft.world.level.material.WaterFluid.canBeReplacedWith
// / LavaFluid.canBeReplacedWith / FlowingFluid.getSpread.

// TestCanBeReplacedWithDirectionModel locks the pure predicate against the three concrete-fluid
// overrides -- NO amount comparison anywhere.
func TestCanBeReplacedWithDirectionModel(t *testing.T) {
	water := fluidState{isWater: true, amount: 4}
	waterFull := fluidState{isWater: true, amount: 8}
	lavaIn := fluidState{isLava: true, amount: 8}
	waterIn := fluidState{isWater: true, amount: 1}
	air := fluidState{}

	// EmptyFluid.canBeReplacedWith -> always true (any direction).
	if !canBeReplacedWith(air, waterIn, dirHoriz) || !canBeReplacedWith(air, lavaIn, dirDown) {
		t.Fatal("air must be replaceable in any direction")
	}

	// WaterFluid: replaceable ONLY by a non-water fluid flowing DOWN.
	if canBeReplacedWith(water, waterIn, dirHoriz) {
		t.Fatal("horizontal water must NOT replace existing water (old amount model was wrong)")
	}
	if canBeReplacedWith(water, waterIn, dirDown) {
		t.Fatal("water flowing DOWN into water must NOT replace (incoming.is(WATER))")
	}
	if !canBeReplacedWith(water, lavaIn, dirDown) {
		t.Fatal("lava flowing DOWN into water MUST replace (dir==DOWN && !incoming.is(WATER))")
	}
	if canBeReplacedWith(water, lavaIn, dirHoriz) {
		t.Fatal("lava beside water horizontally must NOT replace via canBeReplacedWith")
	}
	if canBeReplacedWith(water, waterFull, dirHoriz) {
		t.Fatal("a fuller incoming water flow must NOT replace (amount comparison removed)")
	}

	// LavaFluid: replaceable ONLY by WATER when the lava height >= 4/9.
	tallLava := fluidState{isLava: true, amount: 8}
	shortLava := fluidState{isLava: true, amount: 2}
	if !canBeReplacedWith(tallLava, waterIn, dirHoriz) {
		t.Fatal("water must replace tall lava (height>=4/9)")
	}
	if canBeReplacedWith(shortLava, waterIn, dirHoriz) {
		t.Fatal("water must NOT replace short lava (height<4/9)")
	}
	if canBeReplacedWith(tallLava, lavaIn, dirDown) {
		t.Fatal("lava must NOT replace lava (incoming.is(WATER) is false)")
	}
}

// TestHorizontalWaterDoesNotClobberSource: a source with a lower-level flowing neighbor keeps its
// source; a horizontal flow never replaces the source.
func TestHorizontalWaterDoesNotClobberSource(t *testing.T) {
	loop, mgr := newFluidLoop()
	src := pk.Position{X: 4, Y: 64, Z: 4}
	setSolid(mgr, below(src))
	setSolid(mgr, pk.Position{X: 5, Y: 63, Z: 4})
	setWater(mgr, src, 0)
	setWater(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 1)
	loop.gametime = 10
	loop.scheduleFluidTick(src)
	loop.scheduleFluidTick(pk.Position{X: 5, Y: 64, Z: 4})
	drainAll(loop)
	if lv, ok := levelAt(mgr, src); !ok || lv != 0 {
		t.Fatalf("source clobbered: got level=%d ok=%v, want 0", lv, ok)
	}
}

// TestSourceRemovalStillDrains: after removing a source the whole flowing pool drains to air (the
// diminish cascade + getSpreadDelay reschedule). Guards termination through the direction refactor.
func TestSourceRemovalStillDrains(t *testing.T) {
	loop, mgr := newFluidLoop()
	buildBasin(mgr, 4, 64, 4, 3)
	src := pk.Position{X: 4, Y: 64, Z: 4}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)
	before := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if _, ok := levelAt(mgr, pk.Position{X: 4 + dx, Y: 64, Z: 4 + dz}); ok {
				before++
			}
		}
	}
	if before < 2 {
		t.Fatalf("precondition: source did not spread (only %d cells)", before)
	}
	mgr.SetBlock(src, airStateID(), dimMinY)
	loop.scheduleFluidNeighborsOnEdit(src)
	drainAll(loop)
	remaining := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if _, ok := levelAt(mgr, pk.Position{X: 4 + dx, Y: 64, Z: 4 + dz}); ok {
				remaining++
			}
		}
	}
	if remaining != 0 {
		t.Fatalf("pool did not fully drain after source removal: %d cells remain", remaining)
	}
}

// TestLavaOntoWaterFormationDown: lava flowing DOWN onto water forms STONE and fizzes
// (LavaFluid.spreadTo DOWN override), routed through spreadToDir(DOWN).
func TestLavaOntoWaterFormationDown(t *testing.T) {
	loop, mgr := newFluidLoop()
	pos := pk.Position{X: 4, Y: 64, Z: 4}
	fizzed := false
	loop.fizzHook = func(_ pk.Position, ev int) {
		if ev == lavaExtinguishEvent {
			fizzed = true
		}
	}
	setWater(mgr, pos, 0)
	loop.spreadToDir(pos, dirDown, fluidState{isLava: true, falling: true, amount: waterSourceAmount})
	if _, isW := levelAt(mgr, pos); isW {
		t.Fatal("water was not consumed by lava-onto-water DOWN")
	}
	if !fizzed {
		t.Fatal("lava-onto-water DOWN did not fizz (levelEvent 1501)")
	}
}
