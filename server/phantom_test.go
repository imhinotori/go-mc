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
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world"
)

// phantomLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick phantoms.
func phantomLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	// Tick the chunk through the real light engine so the column carries full sky-light nibbles
	// (the deterministic tickMobSunBurn path gates on maxLocalRawBrightness >= 14). Pass the
	// center column's chunk as its own neighbor so the engine has the heightmap/sources it needs.
	neighbors := map[[2]int]*level.Chunk{{0, 0}: ch}
	world.ComputeChunkLight(level.ChunkPos{0, 0}, neighbors, dimMinY>>4, 384>>4, block.ToStateID[block.Air{}])
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
	if !ph.isSunSensitive() {
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

// TestPhantomCircleToSwoop: PhantomAttackStrategyGoal starts in CIRCLE (nextSweepTick = adjustedTickDelay(10, true))
// and, after the sweep timer expires, flips to SWOOP + re-arms the timer. Cite Phantom$PhantomAttackStrategyGoal.
func TestPhantomCircleToSwoop(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	py := float64(floorY + 20)
	ph := loop.spawnPhantom(8.5, py, 8.5)
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 7001)
	ph.ai.attackTargetID = p.entityID

	// The first call runs start() (nextSweepTick = adjustedTickDelay(10, true)) then the CIRCLE tick decrement
	// in the same folded frame, so the timer reads adjustedTickDelay(10, true)-1 == 9 and is still armed positive.
	loop.phantomAttackStrategyGoal(ph)
	if ph.phantom.attackPhase != phantomPhaseCircle {
		t.Fatalf("after start, phase = %d, want CIRCLE", ph.phantom.attackPhase)
	}
	if ph.phantom.nextSweepTick <= 0 || ph.phantom.nextSweepTick > int32(adjustedTickDelay(10, true)) {
		t.Fatalf("after start, nextSweepTick = %d, want in (0, %d] (armed CIRCLE timer)", ph.phantom.nextSweepTick, adjustedTickDelay(10, true))
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

// TestPhantomTargetsHighestYPlayer: PhantomAttackPlayerTargetGoal.canUse sorts the nearby players by
// Comparator.comparing(Entity::getY).reversed() and takes the FIRST canAttack -- the HIGHEST-Y player, NOT
// the nearest. With two survival players in range (one high, one low-and-closer), the phantom must acquire
// the higher one. Cite Phantom$PhantomAttackPlayerTargetGoal.canUse.
func TestPhantomTargetsHighestYPlayer(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	ph := loop.spawnPhantom(8.5, float64(floorY+30), 8.5)
	ph.phantom.nextScanTick = 0 // arm an immediate scan

	// A LOW player right under the phantom (nearest by distance), and a HIGH player farther away in XZ but
	// well within the inflate(16,64,16) box. The nearest-by-distance is the low one; highest-Y is the high one.
	low := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 7101)
	high := combatTestPlayer(loop, 14.5, float64(floorY+40), 14.5, 7102)

	loop.phantomAcquireTarget(ph)
	if ph.ai.attackTargetID != high.entityID {
		t.Fatalf("phantom targeted %d, want the HIGHEST-Y player %d (not the nearest %d)", ph.ai.attackTargetID, high.entityID, low.entityID)
	}
}

// TestPhantomSkipsCreativeAndSpectator: TargetingConditions.forCombat excludes creative (invulnerable ->
// !canBeSeenAsEnemy) and spectator (!canBeSeenByAnyone) players. With the only in-range player in creative,
// the scan sets no target; likewise spectator. A survival player is acquired. Cite TargetingConditions
// .forCombat/test + LivingEntity.canBeSeenByAnyone/canBeSeenAsEnemy.
func TestPhantomSkipsCreativeAndSpectator(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	ph := loop.spawnPhantom(8.5, float64(floorY+30), 8.5)

	creative := combatTestPlayer(loop, 8.5, float64(floorY+35), 8.5, 7111)
	creative.gameMode = gameModeCreative
	ph.phantom.nextScanTick = 0
	loop.phantomAcquireTarget(ph)
	if ph.ai.attackTargetID != 0 {
		t.Fatalf("phantom targeted a CREATIVE player (%d), want none (canBeSeenAsEnemy false)", ph.ai.attackTargetID)
	}

	creative.gameMode = gameModeSpectator
	ph.phantom.nextScanTick = 0
	loop.phantomAcquireTarget(ph)
	if ph.ai.attackTargetID != 0 {
		t.Fatalf("phantom targeted a SPECTATOR player (%d), want none (canBeSeenByAnyone false)", ph.ai.attackTargetID)
	}

	// Flip to survival -> now attackable.
	creative.gameMode = gameModeSurvival
	ph.phantom.nextScanTick = 0
	loop.phantomAcquireTarget(ph)
	if ph.ai.attackTargetID != creative.entityID {
		t.Fatalf("phantom did not target the now-survival player (got %d, want %d)", ph.ai.attackTargetID, creative.entityID)
	}
}

// TestPhantomTargetDroppedWhenTargetGoesCreative: canContinueToUse keeps the target only while canAttack
// (DEFAULT) holds. A locked target that switches to creative mid-flight is DROPPED (not kept as "still
// alive"). Cite Phantom$PhantomAttackPlayerTargetGoal.canContinueToUse.
func TestPhantomTargetDroppedWhenTargetGoesCreative(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	ph := loop.spawnPhantom(8.5, float64(floorY+30), 8.5)
	p := combatTestPlayer(loop, 8.5, float64(floorY+35), 8.5, 7121)
	ph.ai.attackTargetID = p.entityID

	p.gameMode = gameModeCreative
	loop.phantomAcquireTarget(ph)
	if ph.ai.attackTargetID != 0 {
		t.Fatalf("phantom kept a now-CREATIVE target (%d), want it dropped", ph.ai.attackTargetID)
	}
}

// TestPhantomSweepStopClearsTarget: after a swoop ends the goal stops -- setTarget(null); phase = CIRCLE.
// Driving the folded SWOOP path with the canContinueToUse gate failing (here: target lost) must both clear
// the target id AND re-set the phase to CIRCLE. Cite Phantom$PhantomSweepAttackGoal.stop.
func TestPhantomSweepStopClearsTarget(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	py := float64(floorY + 5)
	p := combatTestPlayer(loop, 8.5, py, 8.5, 7131)
	ph := loop.spawnPhantom(8.5, py, 8.5)
	ph.ai.attackTargetID = p.entityID
	ph.phantom.attackPhase = phantomPhaseSwoop
	ph.phantom.strategyRunning = true // an ACTIVE swoop mid-flight (strategy start() already ran)

	// The target goes creative -> canContinueToUse returns false -> phantomAiStep runs the sweep stop().
	p.gameMode = gameModeCreative
	loop.phantomAiStep(ph)
	if ph.ai.attackTargetID != 0 {
		t.Fatalf("after sweep stop(), target = %d, want 0 (setTarget(null))", ph.ai.attackTargetID)
	}
	if ph.phantom.attackPhase != phantomPhaseCircle {
		t.Fatalf("after sweep stop(), phase = %d, want CIRCLE", ph.phantom.attackPhase)
	}
}

// TestPhantomCatAbortsSwoop: PhantomSweepAttackGoal.canContinueToUse scans for a live Cat within
// getBoundingBox().inflate(16) and, if one is present, sets isScaredOfCat and aborts the swoop (return
// false -> stop(): setTarget(null); phase = CIRCLE). A Cat 3 blocks away must abort. Cite
// Phantom$PhantomSweepAttackGoal.canContinueToUse.
func TestPhantomCatAbortsSwoop(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	py := float64(floorY + 5)
	p := combatTestPlayer(loop, 40.5, py, 40.5, 7141) // player far away so the swoop cannot land a hit this tick
	ph := loop.spawnPhantom(8.5, py, 8.5)
	ph.ai.attackTargetID = p.entityID
	ph.phantom.attackPhase = phantomPhaseSwoop
	ph.phantom.strategyRunning = true // an ACTIVE swoop mid-flight (strategy start() already ran)

	// A live Cat 3 blocks from the phantom, inside inflate(16). Add it to the region store so the scan finds it.
	cat := NewEntity(7142, entity.Cat, 11.5, py, 8.5)
	cat.health = 10.0
	loop.only().entities.add(cat)

	if !loop.phantomCatNearby(ph) {
		t.Fatal("phantomCatNearby did not see a Cat 3 blocks away (inflate(16) scan)")
	}
	loop.phantomAiStep(ph)
	if !ph.phantom.isScaredOfCat {
		t.Fatal("phantom not marked isScaredOfCat with a Cat in range")
	}
	if ph.ai.attackTargetID != 0 {
		t.Fatalf("cat-scared phantom kept target %d, want 0 (swoop aborted -> stop())", ph.ai.attackTargetID)
	}
	if ph.phantom.attackPhase != phantomPhaseCircle {
		t.Fatalf("cat-scared phantom phase = %d, want CIRCLE (swoop aborted)", ph.phantom.attackPhase)
	}
}

// TestPhantomFinalizeSpawnAnchor: spawnPhantom seeds the finalizeSpawn anchor (anchorPoint =
// blockPosition().above(5)) so the phantom starts with a real orbit anchor instead of lazily anchoring on
// the first circle selectNext. Cite Phantom.finalizeSpawn.
func TestPhantomFinalizeSpawnAnchor(t *testing.T) {
	loop, _, floorY := phantomLoop(t)
	ph := loop.spawnPhantom(8.5, float64(floorY+12), 8.5)
	if !ph.phantom.hasAnchor {
		t.Fatal("spawnPhantom did not seed the finalizeSpawn anchor (hasAnchor false)")
	}
	wantY := floorY + 12 + 5
	if ph.phantom.anchorX != 8 || ph.phantom.anchorZ != 8 || ph.phantom.anchorY != wantY {
		t.Fatalf("anchor = (%d,%d,%d), want (8,%d,8) (blockPosition().above(5))", ph.phantom.anchorX, ph.phantom.anchorY, ph.phantom.anchorZ, wantY)
	}
}
