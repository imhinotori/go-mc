package server

import (
	"runtime"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
)

// ai_goals_panic_test.go — MOB-GATE-01 (Phase 31-01): the PanicGoal@1 regression pair. PanicGoal is a
// thin consumer of the Phase-29 lastDamageSource keystone + the Phase-30.1 candidate/snap machinery —
// a hurt pig (panic_causes damage) flees; a pig hurt by a NON-panic source (minecraft:fall) does not.
//
//   - TestPanicGoalFleesOnPanicDamage   — a player_attack hit (a panic_causes member) makes the pig
//                                         acquire a flee target (hasTarget true) via the reused
//                                         generateRandomDirection(5,4) -> setWantCandidates ->
//                                         snapStrollWant path.
//   - TestPanicGoalIgnoresNonPanicDamage — a minecraft:fall hit (id 10, NOT in panic_causes) leaves
//                                         shouldPanic false, so panicGoal.canUse returns false on its
//                                         first line (zero RNG draws) and the pig does not panic.

// TestPanicGoalFleesOnPanicDamage drives the FULL tick (loop.advance via a fake clock, mirroring
// TestPigStrollsWithoutWedging) so the async A* path rejoins. A panic-causing hit (player_attack)
// sets lastDamageSource + hasLastDamage=true, so PanicGoal@1 fires, draws the 30-nextInt
// DefaultRandomPos(5,4) stream, hands 10 candidates to the runtime, and the pig acquires a flee
// target (hasTarget true).
func TestPanicGoalFleesOnPanicDamage(t *testing.T) {
	const (
		floorY = 63
		startY = 64.0
		x, z   = 8.5, 8.5
		ticks  = 200
	)
	clock := newFakeClock()
	loop := NewTickLoop(clock)
	mgr := world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = mgr
	}
	installVanillaPigRegistry(loop)
	loop.start(clock.Now())

	for cx := -2; cx <= 2; cx++ {
		for cz := -2; cz <= 2; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			// A multi-layer ground column (as TestPigStrollsWithoutWedging) so candidate ground-snap
			// (isStableDestination) succeeds for the flee target the snap commits.
			for y := floorY - 8; y <= floorY; y++ {
				fillFloor(ch, y)
			}
		}
	}
	pig := NewEntity(4343, entity.Pig, x, startY, z)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.health = 10.0 // a live pig (else applyDamageEntity's isDeadOrDying guard returns early)
	pig.onGround = true
	loop.only().entities.add(pig)

	// Deal a panic-causing hit (player_attack IS a panic_causes member) — this sets lastDamageSource +
	// hasLastDamage=true so PanicGoal.shouldPanic is true (the keystone consumer). Mirror
	// TestLastDamageSource's injection via applyDamageEntity.
	loop.applyDamageEntity(pig, damageSourcePlayerAttack(77), 2.0)
	if !pig.hasLastDamage {
		t.Fatal("applyDamageEntity did not set hasLastDamage (the not-null keystone signal)")
	}

	acquired := false
	for i := 0; i < ticks; i++ {
		runtime.Gosched()
		clock.add(50 * time.Millisecond)
		loop.advance(clock.Now())
		if pig.ai.hasTarget {
			acquired = true
			break
		}
	}
	if !acquired {
		t.Fatalf("panic-hit pig never acquired a flee target over %d ticks — PanicGoal did not fire "+
			"(shouldPanic = hasLastDamage && is(panic_causes))", ticks)
	}
}

// TestPanicGoalIgnoresNonPanicDamage asserts a NON-panic damage type does not trigger panic. A
// minecraft:fall hit (id 10, NOT in panic_causes) sets hasLastDamage=true but is("panic_causes")=false,
// so shouldPanic is false and panicGoal.canUse returns false on its first line — drawing ZERO RNG (the
// oracle contract). We assert canUse DIRECTLY (deterministic; no stroll interference).
func TestPanicGoalIgnoresNonPanicDamage(t *testing.T) {
	clock := newFakeClock()
	loop := NewTickLoop(clock)
	mgr := world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = mgr
	}
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(clock.Now())

	pig := NewEntity(4444, entity.Pig, 8.5, float64(floorY+1), 8.5)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.onGround = true
	loop.only().entities.add(pig)

	// minecraft:fall is NOT in panic_causes, so a real source recorded (hasLastDamage=true) still leaves
	// shouldPanic false.
	pig.hasLastDamage = true
	pig.lastDamageSource = damageSourceOf(damageTypeFall)
	if pig.lastDamageSource.is("panic_causes") {
		t.Fatal("test premise broken: minecraft:fall must NOT be a panic_causes member")
	}

	g := newPanicGoal(panicSpeedModifier)
	if g.canUse(loop, pig) {
		t.Fatal("panicGoal.canUse returned true for a NON-panic (minecraft:fall) source — shouldPanic " +
			"must gate on is(panic_causes), not merely on a recorded source")
	}

	// And a sanity check: a fresh pig with NO damage at all also does not panic (zero draws).
	fresh := NewEntity(4445, entity.Pig, 8.5, float64(floorY+1), 8.5)
	fresh.ai = newPigAI()
	reseedMobAI(fresh.ai, fresh.id)
	loop.only().entities.add(fresh)
	if newPanicGoal(panicSpeedModifier).canUse(loop, fresh) {
		t.Fatal("panicGoal.canUse returned true for an unhurt pig — shouldPanic must be false (no source)")
	}
}
