package server

// phantom_test.go -- deterministic pins for the Phantom (net.minecraft.world.entity.monster.Phantom,
// 1:1 javap this task). Verifies the spawn attributes (MAX_HEALTH 20 / ATTACK_DAMAGE 6 at size 0), the
// size bbox + damage scaling, the daylight burn (isSunSensitive -> sunBurnTick ignite), the CIRCLE->SWOOP
// attack-phase transition, and the swoop dive-bomb dealing ATTACK_DAMAGE 6 on the intersect.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world"
)

// phantomLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick phantoms.
func phantomLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestPhantomSpawnDefaults: spawnPhantom builds a phantom rendering as entity.Phantom.ID with the jar
// attributes (MAX_HEALTH 20 -> health 20, ATTACK_DAMAGE 6.0 via updatePhantomSizeInfo at size 0), the
// default (size-0) box (0.9 x 0.5), the CIRCLE start phase, and a per-entity rng.
func TestPhantomSpawnDefaults(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	ph := loop.spawnPhantom(8.5, float64(floorY+12), 8.5)
	if ph.typ != entity.Phantom.ID {
		t.Fatalf("phantom typ = %d, want entity.Phantom.ID %d", ph.typ, entity.Phantom.ID)
	}
	if ph.phantom == nil {
		t.Fatal("phantom has no phantomState (e.phantom nil -- the per-type gate would never fire)")
	}
	if ph.phantom.attackPhase != phantomPhaseCircle {
		t.Fatalf("phantom start phase = %d, want CIRCLE %d (ctor default)", ph.phantom.attackPhase, phantomPhaseCircle)
	}
	if math.Abs(float64(ph.health)-20.0) > 1e-6 {
		t.Fatalf("phantom health = %v, want 20.0 (MAX_HEALTH default)", ph.health)
	}
	if got := ph.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("phantom MAX_HEALTH = %v, want 20.0", got)
	}
	if got := ph.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("phantom ATTACK_DAMAGE = %v, want 6.0 (updatePhantomSizeInfo: 6 + size, size 0)", got)
	}
	if math.Abs(ph.width-0.9) > 1e-6 || math.Abs(ph.height-0.5) > 1e-6 {
		t.Fatalf("phantom box = %v x %v, want 0.9 x 0.5 (size 0 scale 1.0)", ph.width, ph.height)
	}
	if ph.ai == nil || ph.ai.rng == nil {
		t.Fatal("phantom has no minimal AI / rng (mobRandom would not be per-entity seeded)")
	}
	if !phantomIsFlyer(ph) {
		t.Fatal("phantom must be a flyer (phantomIsFlyer -> the no-gravity + 0.91-drift physics branch)")
	}
}

// TestPhantomSizeScaling: setPhantomSize(size) scales the box by (1.0 + 0.15*size) and sets ATTACK_DAMAGE
// to (6 + size). At size 3: box == default * 1.45, ATTACK_DAMAGE 9. Cite updatePhantomSizeInfo.
func TestPhantomSizeScaling(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	ph := loop.spawnPhantom(8.5, float64(floorY+12), 8.5)
	loop.setPhantomSize(ph, 3)
	wantScale := 1.0 + 0.15*3.0 // 1.45
	if math.Abs(ph.width-0.9*wantScale) > 1e-6 || math.Abs(ph.height-0.5*wantScale) > 1e-6 {
		t.Fatalf("size-3 box = %v x %v, want %v x %v", ph.width, ph.height, 0.9*wantScale, 0.5*wantScale)
	}
	if got := ph.getAttributeValue(attribute.AttackDamage); math.Abs(got-9.0) > 1e-9 {
		t.Fatalf("size-3 ATTACK_DAMAGE = %v, want 9.0 (6 + 3)", got)
	}
	loop.setPhantomSize(ph, -5)
	if got := ph.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("size -5 (clamped 0) ATTACK_DAMAGE = %v, want 6.0", got)
	}
	loop.setPhantomSize(ph, 100)
	if got := ph.getAttributeValue(attribute.AttackDamage); math.Abs(got-70.0) > 1e-9 {
		t.Fatalf("size 100 (clamped 64) ATTACK_DAMAGE = %v, want 70.0 (6 + 64)", got)
	}
}

// TestPhantomDaylightBurn: the Phantom is sun-sensitive (in BURN_IN_DAYLIGHT), so a phantom in open sky
// during the day catches fire via the shared sunBurnTick (isSunBurnTick -> igniteForSeconds(8)). Drive
// sunBurnTick until it ignites (the ~4% per-tick day roll). A shaded phantom never burns.
func TestPhantomDaylightBurn(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	loop.spawnSurfaceY = floorY + 1 // e.y at/above surface -> canSeeSky true
	loop.gametime = 6000            // noon -> isDay true
	ph := loop.spawnPhantom(8.5, float64(floorY+1), 8.5)
	if !isSunSensitive(ph) {
		t.Fatal("phantom must be sun-sensitive (in EntityTypeTags.BURN_IN_DAYLIGHT)")
	}
	ignited := false
	for i := 0; i < 5000; i++ {
		loop.sunBurnTick(ph)
		if ph.remainingFireTicks > 0 {
			ignited = true
			break
		}
	}
	if !ignited {
		t.Fatal("phantom never caught fire in open daylight (sunBurnTick should ignite it like the undead)")
	}
	if ph.remainingFireTicks != 160 {
		t.Fatalf("phantom daylight ignite = %d ticks, want 160 (igniteForSeconds(8))", ph.remainingFireTicks)
	}
	shaded := loop.spawnPhantom(2.5, float64(floorY-5), 2.5)
	loop.spawnSurfaceY = floorY + 100 // now the phantom is well below the surface -> canSeeSky false
	for i := 0; i < 200; i++ {
		loop.sunBurnTick(shaded)
	}
	if shaded.remainingFireTicks > 0 {
		t.Fatalf("shaded phantom caught fire (remainingFireTicks %d, want 0 -- !canSeeSky guard)", shaded.remainingFireTicks)
	}
}

// TestPhantomCircleToSwoop: PhantomAttackStrategyGoal starts in CIRCLE (nextSweepTick = adjustedTickDelay(10))
// and, after the sweep timer expires, flips to SWOOP + re-arms the timer. Cite Phantom$PhantomAttackStrategyGoal.
func TestPhantomCircleToSwoop(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	py := float64(floorY + 20)
	ph := loop.spawnPhantom(8.5, py, 8.5)
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 7001)
	ph.ai.attackTargetID = p.entityID

	// The first call runs start() (nextSweepTick = adjustedTickDelay(10)) then the CIRCLE tick decrement
	// in the same folded frame, so the timer reads adjustedTickDelay(10)-1 == 9 and is still armed positive.
	loop.phantomAttackStrategyGoal(ph)
	if ph.phantom.attackPhase != phantomPhaseCircle {
		t.Fatalf("after start, phase = %d, want CIRCLE", ph.phantom.attackPhase)
	}
	if ph.phantom.nextSweepTick <= 0 || ph.phantom.nextSweepTick > int32(adjustedTickDelay(10)) {
		t.Fatalf("after start, nextSweepTick = %d, want in (0, %d] (armed CIRCLE timer)", ph.phantom.nextSweepTick, adjustedTickDelay(10))
	}
	flipped := false
	for i := 0; i < 100; i++ {
		loop.phantomAttackStrategyGoal(ph)
		if ph.phantom.attackPhase == phantomPhaseSwoop {
			flipped = true
			break
		}
	}
	if !flipped {
		t.Fatal("phantom never flipped CIRCLE -> SWOOP (the strategy sweep timer should expire)")
	}
	if ph.phantom.nextSweepTick <= 0 {
		t.Fatalf("after the SWOOP flip, nextSweepTick = %d, want > 0 (re-armed)", ph.phantom.nextSweepTick)
	}
}

// TestPhantomSwoopDealsSix: while SWOOP-ing, once the phantom's inflated box intersects the target's box the
// PhantomSweepAttackGoal.tick runs doHurtTarget (ATTACK_DAMAGE 6) and flips back to CIRCLE. Place the phantom
// ON the player (guaranteed intersect) and run the sweep tick. Cite Phantom$PhantomSweepAttackGoal.tick.
func TestPhantomSwoopDealsSix(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	py := float64(floorY + 5)
	p := combatTestPlayer(loop, 8.5, py, 8.5, 7002)
	p.health = 20.0
	start := p.health
	ph := loop.spawnPhantom(8.5, py, 8.5) // co-located with the player -> boxes intersect
	ph.ai.attackTargetID = p.entityID
	ph.phantom.attackPhase = phantomPhaseSwoop

	loop.phantomSweepAttackGoal(ph)
	dealt := start - p.health
	if math.Abs(float64(dealt)-6.0) > 1e-6 {
		t.Fatalf("swoop dealt %v damage, want 6.0 (ATTACK_DAMAGE, doHurtTarget)", dealt)
	}
	if ph.phantom.attackPhase != phantomPhaseCircle {
		t.Fatalf("after a landed swoop, phase = %d, want CIRCLE (the climb-back)", ph.phantom.attackPhase)
	}
}
