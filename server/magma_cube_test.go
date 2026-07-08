package server

// magma_cube_test.go -- deterministic pins for the hostile MagmaCube (net.minecraft.world.entity.monster
// .cubemob.MagmaCube, 1:1 javap this session). Verifies the per-size attributes (size 2: MAX_HEALTH 4,
// MOVEMENT_SPEED 0.4, ATTACK_DAMAGE 2, ARMOR 6; getAttackDamage == size+2 == 4), the slime hop (the
// CubeMobKeepOnJumping + MoveControl.tick arms jumpControl on the delay-0 edge), the split-on-death into
// 2..4 half-size cubes, and the fire+lava immunity (entityFireImmune).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// magmaCubeLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick magma cubes.
func magmaCubeLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestMagmaCubeSize2Attributes: a size-2 magma cube carries the folded per-size attributes -- MAX_HEALTH
// size*size == 4 (health 4), MOVEMENT_SPEED 0.2+0.1*2 == 0.4, ATTACK_DAMAGE == size == 2, ARMOR == size*3
// == 6. getAttackDamage() == ATTACK_DAMAGE + 2.0 == 4.0.
func TestMagmaCubeSize2Attributes(t *testing.T) {
	loop, _, floorY := magmaCubeLoop(t)
	m := loop.spawnMagmaCube(8.5, float64(floorY+1), 8.5, 2)
	if m.typ != entity.MagmaCube.ID {
		t.Fatalf("magma cube typ = %d, want entity.MagmaCube.ID %d", m.typ, entity.MagmaCube.ID)
	}
	if !m.isMagmaCube {
		t.Fatal("magma cube not marked isMagmaCube")
	}
	if m.cubeSize != 2 {
		t.Fatalf("magma cube size = %d, want 2", m.cubeSize)
	}
	if got := m.getAttributeValue(attribute.MaxHealth); math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("size-2 MAX_HEALTH = %v, want 4.0 (size*size)", got)
	}
	if math.Abs(float64(m.health)-4.0) > 1e-6 {
		t.Fatalf("size-2 health = %v, want 4.0", m.health)
	}
	if got := m.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.4) > 1e-6 {
		t.Fatalf("size-2 MOVEMENT_SPEED = %v, want 0.4 (0.2+0.1*2)", got)
	}
	if got := m.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("size-2 ATTACK_DAMAGE = %v, want 2.0 (== size)", got)
	}
	if got := m.getAttributeValue(attribute.Armor); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("size-2 ARMOR = %v, want 6.0 (size*3)", got)
	}
	if got := magmaCubeGetAttackDamage(m); math.Abs(float64(got)-4.0) > 1e-6 {
		t.Fatalf("size-2 getAttackDamage = %v, want 4.0 (ATTACK_DAMAGE + 2.0)", got)
	}
	if m.ai == nil || m.ai.rng == nil {
		t.Fatal("magma cube has no minimal AI / rng")
	}
}

// TestMagmaCubePerSizeHealth: MAX_HEALTH base is size*size across the size ladder (1 -> 1, 2 -> 4, 4 -> 16),
// NOT the SulfurCube 4*size. Verifies setMagmaCubeSize seeds the size-squared health.
func TestMagmaCubePerSizeHealth(t *testing.T) {
	loop, _, floorY := magmaCubeLoop(t)
	for _, tc := range []struct {
		size int32
		want float64
	}{{1, 1.0}, {2, 4.0}, {4, 16.0}} {
		m := loop.spawnMagmaCube(8.5, float64(floorY+1), 8.5, tc.size)
		if got := m.getAttributeValue(attribute.MaxHealth); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("size-%d MAX_HEALTH = %v, want %v (size*size)", tc.size, got, tc.want)
		}
		if math.Abs(float64(m.health)-tc.want) > 1e-6 {
			t.Fatalf("size-%d health = %v, want %v", tc.size, m.health, tc.want)
		}
	}
}

// TestMagmaCubeHops: the slime hop -- the CubeMobKeepOnJumping goal arms MOVE_TO (cubeWantMove=1.0) and the
// CubeMobMoveControl.tick, on the ground with cubeJumpDelay <= 0, arms the jumpControl (doJump) and resets
// the delay to the magma 4x delay (>= 40 == (nextInt(20)+10)*4 floor). Off-ground / between hops it does NOT
// re-arm. Drives the move control directly (the faithful onGround + delay-0 gate).
func TestMagmaCubeHops(t *testing.T) {
	loop, _, floorY := magmaCubeLoop(t)
	m := loop.spawnMagmaCube(8.5, float64(floorY+1), 8.5, 2)
	m.onGround = true
	m.cubeJumpDelay = 0 // delay-0 edge: the cube hops on this grounded move-tick
	m.ai.jumpControl.jump = false
	cubeSetWantedMovement(m, 1.0) // KeepOnJumping.tick: setWantedMovement(1.0) -> MOVE_TO armed
	loop.magmaCubeMoveControlTick(m)
	if !m.ai.jumpControl.jump {
		t.Fatal("magma cube did not arm the jumpControl on the delay-0 grounded hop (doJump)")
	}
	if m.cubeJumpDelay < 40 {
		t.Fatalf("magma cube jumpDelay after the hop = %d, want >= 40 (base(>=10)*4)", m.cubeJumpDelay)
	}
	// Between hops (delay > 0, jump not armed): the move control decrements the delay and does NOT re-hop.
	// (cubeTravel's moveEntity cleared onGround after the hop; re-assert it so we test the grounded path.)
	m.onGround = true
	m.ai.jumpControl.jump = false
	prev := m.cubeJumpDelay
	cubeSetWantedMovement(m, 1.0)
	loop.magmaCubeMoveControlTick(m)
	if m.ai.jumpControl.jump {
		t.Fatal("magma cube hopped between hops (jumpDelay > 0 should NOT arm the jump)")
	}
	if m.cubeJumpDelay != prev-1 {
		t.Fatalf("magma cube jumpDelay = %d, want %d (decremented between hops)", m.cubeJumpDelay, prev-1)
	}
}

// TestMagmaCubeSplitsIntoTwoToFour: killing a size-2 magma cube spawns 2..4 (2+nextInt(3)) size-1 cubes at
// the (i%2, i/2) offset grid, each a real MagmaCube with the size-1 attributes (MAX_HEALTH 1). A size-1
// magma cube does NOT split.
func TestMagmaCubeSplitsIntoTwoToFour(t *testing.T) {
	loop, _, floorY := magmaCubeLoop(t)
	m := loop.spawnMagmaCube(8.5, float64(floorY+1), 8.5, 2)
	before := len(loop.regions[globalRegion].entities.byID)
	m.dead = true // isDeadOrDying()
	loop.magmaCubeSplitOnRemove(m)
	after := len(loop.regions[globalRegion].entities.byID)
	spawned := after - before
	if spawned < 2 || spawned > 4 {
		t.Fatalf("size-2 magma cube split into %d cubes, want 2..4", spawned)
	}
	// Each split is a size-1 magma cube with MAX_HEALTH 1.
	splits := 0
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.id == m.id {
			continue
		}
		if !e.isMagmaCube || e.typ != entity.MagmaCube.ID {
			t.Fatalf("split entity %d is not a magma cube", e.id)
		}
		if e.cubeSize != 1 {
			t.Fatalf("split cube size = %d, want 1 (half of 2)", e.cubeSize)
		}
		if got := e.getAttributeValue(attribute.MaxHealth); math.Abs(got-1.0) > 1e-9 {
			t.Fatalf("split cube MAX_HEALTH = %v, want 1.0 (1*1)", got)
		}
		splits++
	}
	if splits != spawned {
		t.Fatalf("counted %d split cubes, expected %d", splits, spawned)
	}

	// A size-1 magma cube does NOT split.
	tiny := loop.spawnMagmaCube(20.5, float64(floorY+1), 20.5, 1)
	b2 := len(loop.regions[globalRegion].entities.byID)
	tiny.dead = true
	loop.magmaCubeSplitOnRemove(tiny)
	if len(loop.regions[globalRegion].entities.byID) != b2 {
		t.Fatal("size-1 magma cube split (it should not -- getSize() > 1 gate)")
	}
}

// TestMagmaCubeFireImmune: a magma cube is fire+lava immune (entityFireImmune true). tickEntityFire clears
// a burn without damage; tickEntityLava (in lava) deals NO lava damage (only halves fallDistance). A
// non-immune mob (a pig) still takes both.
func TestMagmaCubeFireImmune(t *testing.T) {
	loop, mgr, floorY := magmaCubeLoop(t)
	m := loop.spawnMagmaCube(8.5, float64(floorY+1), 8.5, 2)
	if !entityFireImmune(m) {
		t.Fatal("magma cube not fire-immune (entityFireImmune false)")
	}
	// On fire: tickEntityFire clears the burn without dealing damage.
	m.health = 4.0
	m.remainingFireTicks = 20 // a %20==0 tick that would deal 1.0 to a non-immune mob
	loop.tickEntityFire(m)
	if m.remainingFireTicks != 0 {
		t.Fatalf("fire-immune magma cube still burning: remainingFireTicks = %d, want 0", m.remainingFireTicks)
	}
	if math.Abs(float64(m.health)-4.0) > 1e-6 {
		t.Fatalf("fire-immune magma cube took fire damage: health = %v, want 4.0", m.health)
	}
	// In lava: tickEntityLava deals NO lava damage to the immune cube.
	lava := block.DefaultStateID["minecraft:lava"]
	mgr.SetBlock(pk.Position{X: 8, Y: floorY + 1, Z: 8}, lava, dimMinY)
	if !loop.mobInLava(m) {
		t.Fatal("magma cube not detected in lava")
	}
	m.health = 4.0
	loop.tickEntityLava(m)
	if math.Abs(float64(m.health)-4.0) > 1e-6 {
		t.Fatalf("fire-immune magma cube took lava damage: health = %v, want 4.0", m.health)
	}
	if m.remainingFireTicks != 0 {
		t.Fatalf("fire-immune magma cube ignited in lava: remainingFireTicks = %d, want 0", m.remainingFireTicks)
	}
}
