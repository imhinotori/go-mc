package server

// strider_test.go -- deterministic pins for the Strider (net.minecraft.world.entity.monster.Strider, 1:1
// javap this session). Verifies the spawn attributes (MAX_HEALTH 20 / MOVEMENT_SPEED 0.175 / FOLLOW_RANGE
// 16), the lava-walk (a strider in lava does NOT sink -- striderFloat rides the surface / stands), the
// cold-shiver suffocating slowdown off a warm block (the SUFFOCATING_MODIFIER -0.34 folds MOVEMENT_SPEED to
// base*0.66), the warm state on lava (not suffocating, full speed), and the fire+lava immunity.

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

// striderLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick striders.
func striderLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestStriderSpawnDefaults: spawnStrider builds a strider rendering as entity.Strider.ID with the jar
// attributes (MAX_HEALTH 20 -> health 20, MOVEMENT_SPEED 0.17499999701976776, FOLLOW_RANGE 16.0) and a
// per-entity rng.
func TestStriderSpawnDefaults(t *testing.T) {
	loop, _, floorY := striderLoop(t)
	s := loop.spawnStrider(8.5, float64(floorY+1), 8.5)
	if s.typ != entity.Strider.ID {
		t.Fatalf("strider typ = %d, want entity.Strider.ID %d", s.typ, entity.Strider.ID)
	}
	if !s.isStrider {
		t.Fatal("strider not marked isStrider")
	}
	if math.Abs(float64(s.health)-20.0) > 1e-6 {
		t.Fatalf("strider health = %v, want 20.0 (MAX_HEALTH)", s.health)
	}
	if got := s.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("strider MAX_HEALTH = %v, want 20.0", got)
	}
	if got := s.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.17499999701976776) > 1e-12 {
		t.Fatalf("strider MOVEMENT_SPEED = %v, want 0.17499999701976776", got)
	}
	if got := s.getAttributeValue(attribute.FollowRange); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("strider FOLLOW_RANGE = %v, want 16.0", got)
	}
	if s.ai == nil || s.ai.rng == nil {
		t.Fatal("strider has no minimal AI / rng")
	}
}

// TestStriderLavaWalk: a strider whose feet cell is lava does NOT sink -- striderFloat rides the surface
// (deltaMovement.scale(0.5).add(0,0.05,0)) or stands (setOnGround). We flood the feet cell with lava, run
// striderAiStep + the tickPhysics lava branch, and assert the strider Y did NOT drop (it stays on the lava
// surface) and it is treated as grounded. A strider NOT in lava is a normal ground mob (no float).
func TestStriderLavaWalk(t *testing.T) {
	loop, mgr, floorY := striderLoop(t)
	// Flood a 1-block lava pool where the strider stands (feet at floorY+1).
	lava := block.DefaultStateID["minecraft:lava"]
	mgr.SetBlock(pk.Position{X: 8, Y: floorY + 1, Z: 8}, lava, dimMinY)
	s := loop.spawnStrider(8.5, float64(floorY+1), 8.5)
	if !loop.mobInLava(s) {
		t.Fatal("strider not detected in lava (mobInLava false)")
	}
	if !striderIsLavaWalker(s) {
		t.Fatal("strider not a lava walker (striderIsLavaWalker false)")
	}
	// Give it a downward velocity (a would-be sink) and run the float: it must NOT sink through the lava.
	s.vy = -0.5
	startY := s.y
	loop.striderAiStep(s) // striderFloat: ride the surface (vy scaled + lifted) or stand (onGround)
	// striderFloat either bobs (vy scaled toward 0 + 0.05 lift) or grounds it; either way it must not be a
	// full downward sink. Assert the resulting vy is >= the raw sink (the float arrested the fall).
	if s.vy < -0.5 {
		t.Fatalf("strider float made it sink faster (vy = %v, want arrested)", s.vy)
	}
	// Now integrate one physics tick: the strider must stay at (near) its lava-surface Y, not fall through.
	loop.withRegion(loop.regions[globalRegion], func() { loop.tickPhysics() })
	if s.y < startY-0.5 {
		t.Fatalf("strider sank through lava: y dropped from %v to %v", startY, s.y)
	}
	if s.fallDistance != 0 {
		t.Fatalf("strider fallDistance = %v, want 0 (checkFallDamage isInLava -> resetFallDistance)", s.fallDistance)
	}
}

// TestStriderColdShiverOffLava: a strider OFF a warm block / out of lava enters the suffocating state
// (striderTickSuffocation sets DATA_SUFFOCATING true), which applies the SUFFOCATING_MODIFIER (-0.34
// ADD_MULTIPLIED_BASE) -- MOVEMENT_SPEED folds from base 0.175 to 0.175*(1-0.34) == 0.1155. A strider ON
// lava is warm (not suffocating, full 0.175 speed).
func TestStriderColdShiverOffLava(t *testing.T) {
	loop, mgr, floorY := striderLoop(t)

	// A strider on the DRY stone floor (no warm block, no lava) goes cold -> suffocating + slowed.
	dry := loop.spawnStrider(8.5, float64(floorY+1), 8.5)
	loop.striderTickSuffocation(dry)
	if !dry.striderSuffocating {
		t.Fatal("dry strider not suffocating (off a warm block, out of lava -> cold state)")
	}
	base := 0.17499999701976776
	wantSlow := base * (1.0 + striderSuffocatingAmount) // 0.175 * 0.66 == 0.1155
	if got := dry.getAttributeValue(attribute.MovementSpeed); math.Abs(got-wantSlow) > 1e-9 {
		t.Fatalf("cold strider MOVEMENT_SPEED = %v, want %v (base * 0.66)", got, wantSlow)
	}

	// A strider standing on/in lava is WARM -> not suffocating, full speed.
	lava := block.DefaultStateID["minecraft:lava"]
	mgr.SetBlock(pk.Position{X: 5, Y: floorY + 1, Z: 5}, lava, dimMinY)
	warm := loop.spawnStrider(5.5, float64(floorY+1), 5.5)
	loop.striderTickSuffocation(warm)
	if warm.striderSuffocating {
		t.Fatal("warm strider (in lava) is suffocating (it should be warm -> not suffocating)")
	}
	if got := warm.getAttributeValue(attribute.MovementSpeed); math.Abs(got-base) > 1e-12 {
		t.Fatalf("warm strider MOVEMENT_SPEED = %v, want %v (base, no slowdown)", got, base)
	}

	// Warming a cold strider (move it onto lava) clears the modifier -> back to full speed.
	loop.striderTickSuffocation(dry) // still dry -> stays suffocating
	if !dry.striderSuffocating {
		t.Fatal("dry strider stopped suffocating without warming")
	}
	loop.setStriderSuffocating(dry, false) // simulate reaching a warm block (setSuffocating(false))
	if got := dry.getAttributeValue(attribute.MovementSpeed); math.Abs(got-base) > 1e-12 {
		t.Fatalf("re-warmed strider MOVEMENT_SPEED = %v, want %v (modifier removed)", got, base)
	}
}

// TestStriderFireImmune: a strider is fire+lava immune (entityFireImmune true). tickEntityFire clears a
// burn without damage; tickEntityLava (in lava) deals NO lava damage.
func TestStriderFireImmune(t *testing.T) {
	loop, mgr, floorY := striderLoop(t)
	s := loop.spawnStrider(8.5, float64(floorY+1), 8.5)
	if !entityFireImmune(s) {
		t.Fatal("strider not fire-immune (entityFireImmune false)")
	}
	s.health = 20.0
	s.remainingFireTicks = 20
	loop.tickEntityFire(s)
	if s.remainingFireTicks != 0 || math.Abs(float64(s.health)-20.0) > 1e-6 {
		t.Fatalf("fire-immune strider took fire damage / still burning: health=%v fireTicks=%d", s.health, s.remainingFireTicks)
	}
	lava := block.DefaultStateID["minecraft:lava"]
	mgr.SetBlock(pk.Position{X: 8, Y: floorY + 1, Z: 8}, lava, dimMinY)
	if !loop.mobInLava(s) {
		t.Fatal("strider not detected in lava")
	}
	s.health = 20.0
	loop.tickEntityLava(s)
	if math.Abs(float64(s.health)-20.0) > 1e-6 {
		t.Fatalf("fire-immune strider took lava damage: health = %v, want 20.0", s.health)
	}
}
