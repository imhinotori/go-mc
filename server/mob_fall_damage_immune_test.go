package server

// mob_fall_damage_immune_test.go -- deterministic pins for the EntityTypeTags.FALL_DAMAGE_IMMUNE tag
// wired into the mob fall path (LivingEntity.calculateFallDamage's `if getType().is(FALL_DAMAGE_IMMUNE)
// return 0` first branch, VERIFIED javap this task). A fall-damage-immune mob (breeze, ghast, iron_golem,
// ...) takes ZERO fall damage -- the reported symptom (a breeze hurting itself on its own jumps) is gone.
// A non-immune mob (cow) is unaffected: it still takes fall damage from the SAME chain.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// fallImmuneLoop builds a physics loop with a flat floor (mirrors armadilloLoop).
func fallImmuneLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestFallDamageImmuneTagMembership pins the exact 18 members of
// data/minecraft/tags/entity_type/fall_damage_immune.json (26.2 jar), and that the pig/cow/player-relevant
// non-members are NOT immune.
func TestFallDamageImmuneTagMembership(t *testing.T) {
	immune := []entity.ID{
		entity.CopperGolem.ID, entity.IronGolem.ID, entity.SnowGolem.ID, entity.Shulker.ID,
		entity.Allay.ID, entity.Bat.ID, entity.Bee.ID, entity.Blaze.ID, entity.Cat.ID,
		entity.Chicken.ID, entity.Ghast.ID, entity.HappyGhast.ID, entity.Phantom.ID,
		entity.MagmaCube.ID, entity.Ocelot.ID, entity.Parrot.ID, entity.Wither.ID, entity.Breeze.ID,
	}
	for _, id := range immune {
		if !isFallDamageImmuneType(id) {
			t.Fatalf("entity id %d expected in FALL_DAMAGE_IMMUNE tag, got not-immune", id)
		}
	}
	notImmune := []entity.ID{entity.Pig.ID, entity.Cow.ID, entity.Zombie.ID, entity.Sheep.ID}
	for _, id := range notImmune {
		if isFallDamageImmuneType(id) {
			t.Fatalf("entity id %d must NOT be in FALL_DAMAGE_IMMUNE tag", id)
		}
	}
}

// TestFallDamageImmuneMobTakesNoFall: a breeze (FALL_DAMAGE_IMMUNE) with a large accumulated fallDistance
// takes ZERO fall damage -- causeFallDamageEntity returns false and health is unchanged.
func TestFallDamageImmuneMobTakesNoFall(t *testing.T) {
	loop, floorY := fallImmuneLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	before := b.health
	// A 20-block descent would ordinarily be lethal-ish; the immune tag zeroes it.
	dealt := loop.causeFallDamageEntity(b, 20.0, 1.0)
	if dealt {
		t.Fatal("breeze (FALL_DAMAGE_IMMUNE) took fall damage: causeFallDamageEntity returned true")
	}
	if math.Abs(float64(b.health-before)) > 1e-6 {
		t.Fatalf("breeze health changed by fall damage: before=%v after=%v", before, b.health)
	}
}

// TestFallDamageNonImmuneMobTakesFall: a cow (NOT in the tag) still takes fall damage from a 20-block
// descent through the SAME chain -- the immune wiring did not break the ordinary mob fall path.
func TestFallDamageNonImmuneMobTakesFall(t *testing.T) {
	loop, floorY := fallImmuneLoop(t)
	c := spawnCow(loop, 8.5, float64(floorY+1), 8.5)
	before := c.health
	dealt := loop.causeFallDamageEntity(c, 20.0, 1.0)
	if !dealt {
		t.Fatal("cow (not FALL_DAMAGE_IMMUNE) took NO fall damage from a 20-block descent")
	}
	if c.health >= before {
		t.Fatalf("cow health did not drop after fall damage: before=%v after=%v", before, c.health)
	}
}
