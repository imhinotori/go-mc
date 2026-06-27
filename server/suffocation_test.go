package server

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/level/block"
)

// suffocation_test covers the IN_WALL branch of LivingEntity.baseTick: a solid block at the
// player's EYE position deals 1.0 suffocation damage per tick (through applyDamage/i-frames);
// air or fluid at the eye deals none. suffoPlayer reuses breathPlayer (a confirmed,
// capturing-client player) — eye at y+1.62 is the suffocation sample point, same as breath.
func suffoPlayer(x, y, z float64) *tickPlayer { return breathPlayer(x, y, z) }

// TestSuffocationSolidBlockDamages: a solid (stone) block at the eye position → 1.0 IN_WALL damage.
func TestSuffocationSolidBlockDamages(t *testing.T) {
	loop, mgr := newFluidLoop()
	// player feet at y=64, eye at 64+1.62=65.62 → eye block y=65.
	p := suffoPlayer(8.5, 64.0, 8.5)
	loop.players = append(loop.players, p)
	mgr.SetBlock(pk.Position{X: 8, Y: 65, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)

	if !loop.isInWall(p) {
		t.Fatal("player with a stone block at eye level must be isInWall")
	}
	loop.tickSuffocation()
	if !floatNear(float64(p.health), float64(maxHealth-suffocationDamage), 1e-6) {
		t.Fatalf("health after suffocation = %v, want %v (1.0 IN_WALL damage)", p.health, maxHealth-suffocationDamage)
	}
}

// TestSuffocationAirNoDamage: clear air at the eye → not isInWall → no damage.
func TestSuffocationAirNoDamage(t *testing.T) {
	loop, _ := newFluidLoop() // empty world, no blocks
	p := suffoPlayer(8.5, 64.0, 8.5)
	loop.players = append(loop.players, p)

	if loop.isInWall(p) {
		t.Fatal("player in open air must NOT be isInWall")
	}
	loop.tickSuffocation()
	if !floatNear(float64(p.health), float64(maxHealth), 1e-6) {
		t.Fatalf("health = %v, want %v (no suffocation in air)", p.health, maxHealth)
	}
}

// TestSuffocationWaterNoDamage: water at the eye is NOT suffocating (LiquidBlock is not a full
// cube) — drowning is the air branch's job, not IN_WALL.
func TestSuffocationWaterNoDamage(t *testing.T) {
	loop, mgr := newFluidLoop()
	p := suffoPlayer(8.5, 64.0, 8.5)
	loop.players = append(loop.players, p)
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)

	if loop.isInWall(p) {
		t.Fatal("water at the eye must NOT count as isInWall (fluid is not a suffocating full cube)")
	}
	loop.tickSuffocation()
	if !floatNear(float64(p.health), float64(maxHealth), 1e-6) {
		t.Fatalf("health = %v, want %v (water does not suffocate)", p.health, maxHealth)
	}
}
