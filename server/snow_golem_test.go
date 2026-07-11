package server

// snow_golem_test.go -- deterministic pins for the SnowGolem (net.minecraft.world.entity.animal.golem.
// SnowGolem, 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 4 / MOVEMENT_SPEED 0.2),
// the default pumpkin state + shear, and that the snow-trail aiStep leaves snow layers on a solid floor.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

func snowGolemLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestSnowGolemSpawnDefaults: spawnSnowGolem builds a golem rendering as entity.SnowGolem.ID with the jar
// attributes (MAX_HEALTH 4, MOVEMENT_SPEED 0.2) and the default pumpkin.
func TestSnowGolemSpawnDefaults(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	if g.typ != entity.SnowGolem.ID {
		t.Fatalf("snow_golem typ = %d, want entity.SnowGolem.ID %d", g.typ, entity.SnowGolem.ID)
	}
	if !g.isSnowGolem {
		t.Fatal("snow_golem not marked isSnowGolem")
	}
	if math.Abs(float64(g.health)-4.0) > 1e-6 {
		t.Fatalf("snow_golem health = %v, want 4.0 (MAX_HEALTH)", g.health)
	}
	if got := g.getAttributeValue(attribute.MaxHealth); math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("snow_golem MAX_HEALTH = %v, want 4.0", got)
	}
	if got := g.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.20000000298023224) > 1e-12 {
		t.Fatalf("snow_golem MOVEMENT_SPEED = %v, want 0.20000000298023224", got)
	}
	if !g.snowGolemPumpkin {
		t.Fatal("snow_golem should spawn wearing a pumpkin (DATA_PUMPKIN_ID default true)")
	}
}

// TestSnowGolemShear: readyForShearing == isAlive() && hasPumpkin(); shear clears the pumpkin once and is
// idempotent afterward (a pumpkin-less golem cannot be sheared again). Cite SnowGolem.shear.
func TestSnowGolemShear(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	if !loop.snowGolemShear(g) {
		t.Fatal("first shear should succeed (golem has a pumpkin)")
	}
	if g.snowGolemPumpkin {
		t.Fatal("shear should clear the pumpkin")
	}
	if loop.snowGolemShear(g) {
		t.Fatal("second shear should fail (no pumpkin to remove)")
	}
}

// TestSnowGolemSnowTrail: with MOB_GRIEFING on, snowGolemAiStep leaves at least one snow layer around the
// golem's feet where the block is air with a solid floor below. Cite SnowGolem.aiStep.
func TestSnowGolemSnowTrail(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	// Stand the golem ON the floor (feet at floorY+1) so the four offsets land at floorY+1 (air) with the
	// solid floor at floorY below -> canSurvive.
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	snow, ok := block.DefaultStateID["minecraft:snow"]
	if !ok {
		t.Skip("no minecraft:snow default state in this build")
	}
	loop.snowGolemAiStep(g)
	// Check the four candidate offsets; at least one should now be a snow layer.
	placed := 0
	for i := 0; i < 4; i++ {
		bx := mthFloor(8.5 + float64((i%2)*2-1)*snowGolemTrailOffset)
		by := mthFloor(float64(floorY + 1))
		bz := mthFloor(8.5 + float64((i/2%2)*2-1)*snowGolemTrailOffset)
		if s, _ := loop.world().GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY); s == snow {
			placed++
		}
	}
	if placed == 0 {
		t.Fatal("snowGolemAiStep placed no snow trail on a solid floor")
	}
}

// snowGolemEnemyMob builds a live AI hostile mob at (x,y,z) and adds it to the golem's region store so the
// golem can target + hit it. reseedMobAI keeps its per-entity rng deterministic.
func snowGolemEnemyMob(loop *TickLoop, e *Entity) *Entity {
	e.ai = &mobAI{}
	reseedMobAI(e.ai, e.id)
	initSpawnHealth(e)
	loop.only().entities.add(e)
	return e
}

// snowballCountInStore counts the live snowball throwables in the golem's region store.
func snowballCountInStore(loop *TickLoop) int {
	n := 0
	for _, e := range loop.only().entities.all() {
		if e.isThrowable && e.throwableKind == throwSnowball {
			n++
		}
	}
	return n
}

// TestSnowGolemThrowsSnowballAtHostile: a snow golem with a hostile (zombie) in range fires a snowball on
// the RangedAttackGoal's 20-tick cadence; the snowball flies to the target, deals 0 damage to the zombie
// (Snowball.onHitEntity: non-Blaze -> 0), and knocks it back (the shared dealDefaultKnockback recoil).
func TestSnowGolemThrowsSnowballAtHostile(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)

	// A zombie two blocks away, well within the 10-block attack radius + FOLLOW_RANGE.
	z := snowGolemEnemyMob(loop, NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 10.5, float64(floorY+1), 8.5))
	zHealth0 := z.health
	zVx0, zVy0, zVz0 := z.vx, z.vy, z.vz

	// The golem's RangedAttackGoal ticks via its goalSelector; drive the goal directly with the target set
	// (mirrors how vanilla's targetSelector would have committed the Enemy target). Fire cadence is 20.
	g.ai.setTarget(z.id)
	goal := newSnowGolemRangedAttackGoal()
	fired := false
	for i := 0; i < 30 && !fired; i++ {
		goal.tick(loop, g)
		if snowballCountInStore(loop) > 0 {
			fired = true
		}
	}
	if !fired {
		t.Fatal("snow golem never fired a snowball at a hostile within 30 ticks (expected fire at ~tick 20)")
	}

	// Fly the snowball until it lands (hits the zombie or a block) -- it should reach the 2-block-away target.
	for i := 0; i < 40 && snowballCountInStore(loop) > 0; i++ {
		loop.tickThrowables()
	}
	if snowballCountInStore(loop) > 0 {
		t.Fatal("the snowball never resolved (never reached the target or a block)")
	}

	// The zombie takes 0 damage (Snowball.onHitEntity non-Blaze) but is knocked back (velocity changed).
	if math.Abs(float64(z.health)-float64(zHealth0)) > 1e-6 {
		t.Fatalf("zombie health changed = %v -> %v; a snowball deals 0 to a non-blaze", zHealth0, z.health)
	}
	if z.vx == zVx0 && z.vy == zVy0 && z.vz == zVz0 {
		t.Fatal("zombie was not knocked back by the snowball hit (velocity unchanged)")
	}
}

// TestSnowballHitsBlazeForThree: Snowball.onHitEntity deals 3 to a Blaze (else 0). Drive the snowball
// hit directly against a blaze victim to pin the 3-damage branch.
func TestSnowballHitsBlazeForThree(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	b := loop.spawnBlaze(10.5, float64(floorY+1), 8.5)
	bHealth0 := b.health

	// Spawn a snow-golem snowball right at the blaze and resolve the hit.
	sb := loop.spawnThrowable(g.id, throwSnowball, b.x, b.y+1.0, b.z, 0, 0, 0)
	sb.snowballHitsMobs = true
	loop.snowballOnHitMob(sb, b)

	if diff := float64(bHealth0) - float64(b.health); math.Abs(diff-3.0) > 1e-6 {
		t.Fatalf("blaze took %v damage from a snowball, want 3.0 (Snowball.onHitEntity blaze branch)", diff)
	}
}

// TestSnowballZeroDamageToZombie: Snowball.onHitEntity deals 0 to a non-Blaze mob (the zombie). Pin the
// else branch directly.
func TestSnowballZeroDamageToZombie(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	z := snowGolemEnemyMob(loop, NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 10.5, float64(floorY+1), 8.5))
	zHealth0 := z.health

	sb := loop.spawnThrowable(g.id, throwSnowball, z.x, z.y+1.0, z.z, 0, 0, 0)
	sb.snowballHitsMobs = true
	loop.snowballOnHitMob(sb, z)

	if math.Abs(float64(z.health)-float64(zHealth0)) > 1e-6 {
		t.Fatalf("zombie took %v -> %v; a snowball deals 0 to a non-blaze", zHealth0, z.health)
	}
}

// TestSnowGolemInertWithoutHostile: a snow golem with NO hostile in range acquires no target and never
// fires a snowball (canUse false, tick a no-op) -- and no throwable is spawned.
func TestSnowGolemInertWithoutHostile(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)

	// The target selector finds no Enemy mob -> canUse false.
	tgtGoal := newSnowGolemEnemyTargetGoal()
	tgtGoal.forceTrigger = true // bypass the RNG gate so the scan runs deterministically
	if tgtGoal.canUse(loop, g) {
		t.Fatal("snow golem must not acquire a target with no hostile in range")
	}

	// The ranged goal cannot use (no target) and ticks a no-op; no snowball spawns.
	goal := newSnowGolemRangedAttackGoal()
	if goal.canUse(loop, g) {
		t.Fatal("ranged goal canUse must be false with no target")
	}
	for i := 0; i < 30; i++ {
		goal.tick(loop, g)
	}
	if n := snowballCountInStore(loop); n != 0 {
		t.Fatalf("an inert snow golem fired %d snowball(s), want 0", n)
	}
}
