package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// insideBlocksLoop builds a physics loop with a single all-air chunk at (0,0) plus a stone floor, and
// returns the loop + chunk manager. Blocks are placed with mgr.SetBlock into the mob's feet cell.
func insideBlocksLoop(t *testing.T) (*TickLoop, *level.Chunk) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, 63) // solid floor at y=63; the mob stands at y=64
	return loop, ch
}

// mobAt adds a living Zombie (a LivingEntity, not FOX/BEE) at (x,y,z) with full health and a valid AI/rng
// so the damage/effect paths run. Returns the entity.
func mobAt(loop *TickLoop, id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Zombie, x, y, z)
	e.health = 20
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	loop.only().entities.add(e)
	return e
}

func mgrOf(loop *TickLoop) interface {
	SetBlock(pk.Position, block.StateID, int) bool
} {
	return loop.only().world
}

// TestCactusEntityInsideDamages: a mob overlapping a cactus cell takes 1.0 cactus contact damage per
// checkInsideBlocks pass (CactusBlock.entityInside: hurt(cactus(), 1.0F), unconditional).
func TestCactusEntityInsideDamages(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.Cactus{}], dimMinY)

	e := mobAt(loop, 1, 8.5, 64.0, 8.5)
	before := e.health
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(e) })
	if e.health >= before {
		t.Fatalf("cactus: health did not drop (before=%v after=%v) -- CactusBlock.entityInside 1.0 damage did not fire", before, e.health)
	}

	// A mob NOT in a cactus (air around it) takes nothing.
	dry := mobAt(loop, 2, 2.5, 64.0, 2.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(dry) })
	if dry.health != 20 {
		t.Fatalf("cactus: dry mob took damage (health=%v) -- non-cactus block must be a no-op", dry.health)
	}
}

// TestCobwebEntityInsideSlows: a mob overlapping a cobweb cell gets the stuck-speed multiplier
// (0.25, 0.05, 0.25) armed (WebBlock.entityInside -> makeStuckInBlock), and moveEntity consumes it on the
// next move (scaling the requested delta by that vector).
func TestCobwebEntityInsideSlows(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.Cobweb{}], dimMinY)

	e := mobAt(loop, 1, 8.5, 64.0, 8.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(e) })
	if !e.stuck {
		t.Fatalf("cobweb: stuck flag not armed -- WebBlock.entityInside makeStuckInBlock did not run")
	}
	if e.stuckSpeedMultiplierX != 0.25 || e.stuckSpeedMultiplierY != 0.05000000074505806 || e.stuckSpeedMultiplierZ != 0.25 {
		t.Fatalf("cobweb: stuck multiplier = (%v,%v,%v), want (0.25, 0.05, 0.25)",
			e.stuckSpeedMultiplierX, e.stuckSpeedMultiplierY, e.stuckSpeedMultiplierZ)
	}
	// fallDistance is reset by makeStuckInBlock.
	e.fallDistance = 3.0
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(e) })
	if e.fallDistance != 0 {
		t.Fatalf("cobweb: fallDistance not reset (=%v) -- makeStuckInBlock resetFallDistance did not run", e.fallDistance)
	}

	// moveEntity consumes the multiplier: a 1.0-block request in X collapses to ~0.25.
	e.stuck = true
	e.stuckSpeedMultiplierX, e.stuckSpeedMultiplierY, e.stuckSpeedMultiplierZ = 0.25, 0.05, 0.25
	startX := e.x
	loop.withRegion(loop.only(), func() { loop.moveEntity(e, 1.0, 0, 0) })
	moved := e.x - startX
	if moved > 0.3 || moved < 0.2 {
		t.Fatalf("cobweb: moveEntity moved %v in X for a 1.0 request, want ~0.25 (stuck-speed consumed)", moved)
	}
	if e.stuck {
		t.Fatalf("cobweb: stuck flag not cleared after moveEntity consumed the multiplier")
	}
}

// TestSweetBerryBushSlowAndDamage: a LivingEntity in a GROWN (AGE!=0) berry bush is slowed (0.8,0.75,0.8)
// and, if it moved horizontally (>=0.003), takes 1.0 sweet_berry_bush damage. A stationary mob is only
// slowed (no damage). AGE==0 is only slow, never damage.
func TestSweetBerryBushSlowAndDamage(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.SweetBerryBush{Age: 3}], dimMinY)

	// Moving mob: e.vx above the 0.003 threshold -> slow + damage.
	moving := mobAt(loop, 1, 8.5, 64.0, 8.5)
	moving.vx = 0.1
	before := moving.health
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(moving) })
	if !moving.stuck || moving.stuckSpeedMultiplierX != 0.800000011920929 {
		t.Fatalf("berry: moving mob not slowed (stuck=%v mult=%v)", moving.stuck, moving.stuckSpeedMultiplierX)
	}
	if moving.health >= before {
		t.Fatalf("berry: moving mob took no damage (before=%v after=%v) -- grown bush + movement must hurt 1.0", before, moving.health)
	}

	// Stationary mob (no horizontal movement) -> slow only, no damage.
	still := mobAt(loop, 2, 8.5, 64.0, 8.5)
	still.vx, still.vz = 0, 0
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(still) })
	if !still.stuck {
		t.Fatalf("berry: stationary mob not slowed")
	}
	if still.health != 20 {
		t.Fatalf("berry: stationary mob took damage (health=%v) -- no horizontal movement must not hurt", still.health)
	}

	// AGE==0 bush: slow only, never damage even when moving.
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.SweetBerryBush{Age: 0}], dimMinY)
	young := mobAt(loop, 3, 8.5, 64.0, 8.5)
	young.vx = 0.5
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(young) })
	if !young.stuck {
		t.Fatalf("berry (age 0): mob not slowed -- the slow is unconditional on a LivingEntity")
	}
	if young.health != 20 {
		t.Fatalf("berry (age 0): mob took damage (health=%v) -- AGE==0 must never hurt", young.health)
	}
}

// TestWitherRoseEntityInsideEffect: a LivingEntity in a wither rose on non-PEACEFUL gets WITHER for 40
// ticks (WitherRoseBlock.entityInside -> addEffect(new MobEffectInstance(WITHER, 40))). PEACEFUL is a no-op.
func TestWitherRoseEntityInsideEffect(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.WitherRose{}], dimMinY)

	loop.levelDifficulty = difficultyEasy
	e := mobAt(loop, 1, 8.5, 64.0, 8.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(e) })
	if e.mobEffects == nil || e.mobEffects[effectWither] == nil {
		t.Fatalf("wither rose: WITHER effect not applied on non-PEACEFUL")
	}
	if e.mobEffects[effectWither].duration != 40 {
		t.Fatalf("wither rose: WITHER duration = %d, want 40", e.mobEffects[effectWither].duration)
	}
	if e.mobEffects[effectWither].amplifier != 0 {
		t.Fatalf("wither rose: WITHER amplifier = %d, want 0", e.mobEffects[effectWither].amplifier)
	}

	// PEACEFUL -> no effect.
	loop.levelDifficulty = difficultyPeaceful
	peaceful := mobAt(loop, 2, 8.5, 64.0, 8.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(peaceful) })
	if peaceful.mobEffects != nil && peaceful.mobEffects[effectWither] != nil {
		t.Fatalf("wither rose: WITHER applied on PEACEFUL -- must be gated off")
	}
}

// TestTripwireEntityInsideTriggers: a mob standing on an unpowered tripwire presses it (POWERED true) via
// checkInsideBlocks -> TripWireBlock.entityInside -> checkPressed.
func TestTripwireEntityInsideTriggers(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	unpowered := block.ToStateID[block.Tripwire{}]
	mgrOf(loop).SetBlock(pos, unpowered, dimMinY)
	if block.TripwirePowered(unpowered) {
		t.Fatalf("precondition: default tripwire must be unpowered")
	}

	e := mobAt(loop, 1, 8.5, 64.0, 8.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(e) })

	state, ok := loop.only().world.GetBlock(pos, dimMinY)
	if !ok {
		t.Fatalf("tripwire: block missing after checkInsideBlocks")
	}
	if !block.TripwirePowered(state) {
		t.Fatalf("tripwire: wire not POWERED after a mob stood on it -- TripWireBlock.entityInside checkPressed did not trigger")
	}
}