package server

// axolotl_test.go -- deterministic pins for the Axolotl (net.minecraft.world.entity.animal.axolotl.Axolotl,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 14 / MOVEMENT_SPEED 1.0 / ATTACK_DAMAGE
// 2 / STEP_HEIGHT 1.0) and the default (lucy) variant.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func axolotlLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestAxolotlSpawnDefaults: spawnAxolotl builds an axolotl rendering as entity.Axolotl.ID with the jar
// attributes, including the STEP_HEIGHT 1.0 override and the default lucy variant.
func TestAxolotlSpawnDefaults(t *testing.T) {
	loop, floorY := axolotlLoop(t)
	a := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
	if a.typ != entity.Axolotl.ID {
		t.Fatalf("axolotl typ = %d, want entity.Axolotl.ID %d", a.typ, entity.Axolotl.ID)
	}
	if !a.isAxolotl {
		t.Fatal("axolotl not marked isAxolotl")
	}
	if math.Abs(float64(a.health)-14.0) > 1e-6 {
		t.Fatalf("axolotl health = %v, want 14.0 (MAX_HEALTH)", a.health)
	}
	if got := a.getAttributeValue(attribute.MaxHealth); math.Abs(got-14.0) > 1e-9 {
		t.Fatalf("axolotl MAX_HEALTH = %v, want 14.0", got)
	}
	if got := a.getAttributeValue(attribute.MovementSpeed); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("axolotl MOVEMENT_SPEED = %v, want 1.0", got)
	}
	if got := a.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("axolotl ATTACK_DAMAGE = %v, want 2.0", got)
	}
	if got := a.getAttributeValue(attribute.StepHeight); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("axolotl STEP_HEIGHT = %v, want 1.0 (override of the 0.6 base)", got)
	}
	// Axolotl.finalizeSpawn draws a COMMON-variant pick (never BLUE, the rare breeding mutation) on
	// level.getRandom(); the observable invariant is variant in {LUCY,WILD,GOLD,CYAN}.
	if a.axolotlVariant == axolotlVariantBlue {
		t.Fatalf("axolotl variant = BLUE %d, but finalizeSpawn only draws COMMON variants", axolotlVariantBlue)
	}
	if a.axolotlVariant < axolotlVariantLucy || a.axolotlVariant > axolotlVariantCyan {
		t.Fatalf("axolotl variant = %d, want a common variant in [%d,%d]", a.axolotlVariant, axolotlVariantLucy, axolotlVariantCyan)
	}
	if a.ai == nil || a.ai.rng == nil {
		t.Fatal("axolotl has no minimal AI / rng")
	}
}

// TestAxolotlSpawnVariantCommonDistribution: over many spawns every axolotl draws one of the four COMMON
// variants (LUCY/WILD/GOLD/CYAN) and BLUE never appears (getCommonSpawnVariant excludes it). Confirms the
// nextInt(4) common-array pick on level.getRandom(). Cite Axolotl$Variant.getCommonSpawnVariant.
func TestAxolotlSpawnVariantCommonDistribution(t *testing.T) {
	loop, floorY := axolotlLoop(t)
	seen := map[int]int{}
	for i := 0; i < 400; i++ {
		a := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
		if a.axolotlVariant == axolotlVariantBlue {
			t.Fatalf("spawn %d drew BLUE, but common spawns never draw the rare mutation", i)
		}
		seen[a.axolotlVariant]++
	}
	for _, v := range []int{axolotlVariantLucy, axolotlVariantWild, axolotlVariantGold, axolotlVariantCyan} {
		if seen[v] == 0 {
			t.Fatalf("common variant %d never drawn over 400 spawns", v)
		}
	}
}
