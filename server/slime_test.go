package server

// slime_test.go -- deterministic pins for the hostile Slime (net.minecraft.world.entity.monster.cubemob
// .Slime, 1:1 javap this session). Verifies the per-size attributes (setSize: MAX_HEALTH size*size,
// MOVEMENT_SPEED 0.2+0.1*size, ATTACK_DAMAGE size, NO armor), the size-gated isDealsDamage (a size-1 slime
// deals NO damage), the BASE getAttackDamage (== size, NO +2.0), the slime hop (the shared
// CubeMobMoveControl.tick arms jumpControl with the BASE getJumpDelay nextInt(20)+10, NO 4x), the
// split-on-death of a size-4 slime into 2..4 size-2 slimes at the offset grid, and the per-size XP (size).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world"
)

// slimeLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick slimes.
func slimeLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestSlimePerSizeAttributes: the folded per-size attributes across the size ladder. MAX_HEALTH size*size
// (1->1, 2->4, 4->16), MOVEMENT_SPEED 0.2+0.1*size (2->0.4, 4->0.6), ATTACK_DAMAGE == size (NOT size+2 like
// the MagmaCube), and NO armor is set (Slime.setSize does not touch ARMOR -- it stays the default 0).
func TestSlimePerSizeAttributes(t *testing.T) {
	loop, _, floorY := slimeLoop(t)
	for _, tc := range []struct {
		size                         int32
		health, speed, damage, armor float64
	}{
		{1, 1.0, 0.3, 1.0, 0.0},
		{2, 4.0, 0.4, 2.0, 0.0},
		{4, 16.0, 0.6, 4.0, 0.0},
	} {
		m := loop.spawnSlime(8.5, float64(floorY+1), 8.5, tc.size)
		if m.typ != entity.Slime.ID {
			t.Fatalf("slime typ = %d, want entity.Slime.ID %d", m.typ, entity.Slime.ID)
		}
		if !m.isSlime {
			t.Fatal("slime not marked isSlime")
		}
		if m.cubeSize != tc.size {
			t.Fatalf("slime size = %d, want %d", m.cubeSize, tc.size)
		}
		if got := m.getAttributeValue(attribute.MaxHealth); math.Abs(got-tc.health) > 1e-9 {
			t.Fatalf("size-%d MAX_HEALTH = %v, want %v (size*size)", tc.size, got, tc.health)
		}
		if math.Abs(float64(m.health)-tc.health) > 1e-6 {
			t.Fatalf("size-%d health = %v, want %v", tc.size, m.health, tc.health)
		}
		if got := m.getAttributeValue(attribute.MovementSpeed); math.Abs(got-tc.speed) > 1e-6 {
			t.Fatalf("size-%d MOVEMENT_SPEED = %v, want %v (0.2+0.1*size)", tc.size, got, tc.speed)
		}
		if got := m.getAttributeValue(attribute.AttackDamage); math.Abs(got-tc.damage) > 1e-9 {
			t.Fatalf("size-%d ATTACK_DAMAGE = %v, want %v (== size, NOT size+2)", tc.size, got, tc.damage)
		}
		if got := m.getAttributeValue(attribute.Armor); math.Abs(got-tc.armor) > 1e-9 {
			t.Fatalf("size-%d ARMOR = %v, want %v (Slime sets NO armor)", tc.size, got, tc.armor)
		}
		if got := slimeGetAttackDamage(m); math.Abs(float64(got)-tc.damage) > 1e-6 {
			t.Fatalf("size-%d getAttackDamage = %v, want %v (== ATTACK_DAMAGE, NO +2.0)", tc.size, got, tc.damage)
		}
	}
}

// TestSlimeSizeGatedDealsDamage: isDealsDamage is !isTiny() && isEffectiveAi(). A size-1 (tiny) slime deals
// NO damage; a size-2/4 slime does. This is the Slime-vs-MagmaCube divergence (MagmaCube always deals).
func TestSlimeSizeGatedDealsDamage(t *testing.T) {
	loop, _, floorY := slimeLoop(t)
	tiny := loop.spawnSlime(8.5, float64(floorY+1), 8.5, 1)
	if slimeIsDealsDamage(tiny) {
		t.Fatal("size-1 (tiny) slime deals damage -- it must NOT (isTiny gate)")
	}
	for _, sz := range []int32{2, 4} {
		m := loop.spawnSlime(8.5, float64(floorY+1), 8.5, sz)
		if !slimeIsDealsDamage(m) {
			t.Fatalf("size-%d slime does NOT deal damage -- it must (size>1)", sz)
		}
	}
}

// TestSlimeHops: the slime hop -- the shared CubeMobMoveControl.tick, on the ground with cubeJumpDelay <= 0,
// arms jumpControl (doJump) and resets the delay to the BASE getJumpDelay (nextInt(20)+10 in [10,29], NO 4x
// like the MagmaCube). Between hops (delay > 0) it decrements and does NOT re-hop.
func TestSlimeHops(t *testing.T) {
	loop, _, floorY := slimeLoop(t)
	m := loop.spawnSlime(8.5, float64(floorY+1), 8.5, 2)
	m.onGround = true
	m.cubeJumpDelay = 0 // delay-0 edge: the slime hops on this grounded move-tick
	m.ai.jumpControl.jump = false
	cubeSetWantedMovement(m, 1.0) // KeepOnJumping.tick: setWantedMovement(1.0) -> MOVE_TO armed
	loop.cubeMoveControlTick(m)
	if !m.ai.jumpControl.jump {
		t.Fatal("slime did not arm the jumpControl on the delay-0 grounded hop (doJump)")
	}
	// BASE getJumpDelay == nextInt(20)+10 -> [10,29]. NOT the MagmaCube 4x ([40,116]).
	if m.cubeJumpDelay < 10 || m.cubeJumpDelay > 29 {
		t.Fatalf("slime jumpDelay after hop = %d, want in [10,29] (base nextInt(20)+10, NO 4x)", m.cubeJumpDelay)
	}
	// Between hops (delay > 0): decrement, do NOT re-hop.
	m.onGround = true
	m.ai.jumpControl.jump = false
	prev := m.cubeJumpDelay
	cubeSetWantedMovement(m, 1.0)
	loop.cubeMoveControlTick(m)
	if m.ai.jumpControl.jump {
		t.Fatal("slime hopped between hops (jumpDelay > 0 should NOT arm the jump)")
	}
	if m.cubeJumpDelay != prev-1 {
		t.Fatalf("slime jumpDelay = %d, want %d (decremented between hops)", m.cubeJumpDelay, prev-1)
	}
}

// TestSlimeSize4SplitsIntoSize2: killing a size-4 slime spawns 2..4 (2+nextInt(3)) size-2 slimes at the
// (i%2, i/2) offset grid, each a real Slime with the size-2 attributes (MAX_HEALTH 4). Verifies the exact
// child size (size/2 == 2), the offset placement, and the 2..4 count.
func TestSlimeSize4SplitsIntoSize2(t *testing.T) {
	loop, _, floorY := slimeLoop(t)
	m := loop.spawnSlime(8.5, float64(floorY+1), 8.5, 4)
	before := len(loop.regions[globalRegion].entities.byID)
	m.dead = true // isDeadOrDying()
	loop.slimeSplitOnRemove(m)
	after := len(loop.regions[globalRegion].entities.byID)
	spawned := after - before
	if spawned < 2 || spawned > 4 {
		t.Fatalf("size-4 slime split into %d slimes, want 2..4", spawned)
	}
	splits := 0
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.id == m.id {
			continue
		}
		if !e.isSlime || e.typ != entity.Slime.ID {
			t.Fatalf("split entity %d is not a slime", e.id)
		}
		if e.cubeSize != 2 {
			t.Fatalf("split slime size = %d, want 2 (half of 4)", e.cubeSize)
		}
		if got := e.getAttributeValue(attribute.MaxHealth); math.Abs(got-4.0) > 1e-9 {
			t.Fatalf("split slime MAX_HEALTH = %v, want 4.0 (2*2)", got)
		}
		// Offset placement: each child within one live-width of the parent (xzOffset == width/2).
		if math.Abs(e.x-m.x) > m.width || math.Abs(e.z-m.z) > m.width {
			t.Fatalf("split slime at (%.2f,%.2f), parent (%.2f,%.2f) -- offset too far", e.x, e.z, m.x, m.z)
		}
		splits++
	}
	if splits != spawned {
		t.Fatalf("counted %d split slimes, expected %d", splits, spawned)
	}
	// A size-1 slime does NOT split.
	tiny := loop.spawnSlime(20.5, float64(floorY+1), 20.5, 1)
	b2 := len(loop.regions[globalRegion].entities.byID)
	tiny.dead = true
	loop.slimeSplitOnRemove(tiny)
	if len(loop.regions[globalRegion].entities.byID) != b2 {
		t.Fatal("size-1 slime split (it should not -- getSize() > 1 gate)")
	}
}

// TestSlimeXpRewardIsSize: Slime does NOT override getBaseExperienceReward, so it reads xpReward == size
// (Slime.setSize). A size-2 slime yields 2 XP, size-4 yields 4 -- NO nextInt draw (unlike the Animal path).
func TestSlimeXpRewardIsSize(t *testing.T) {
	loop, _, floorY := slimeLoop(t)
	for _, sz := range []int32{1, 2, 4} {
		m := loop.spawnSlime(8.5, float64(floorY+1), 8.5, sz)
		if m.slimeXpReward != sz {
			t.Fatalf("size-%d slimeXpReward = %d, want %d", sz, m.slimeXpReward, sz)
		}
		if got := loop.entityBaseExperienceReward(m); got != int(sz) {
			t.Fatalf("size-%d entityBaseExperienceReward = %d, want %d (== size, NO draw)", sz, got, sz)
		}
	}
}
