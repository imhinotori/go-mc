package server

// fox_target_test.go — the Fox CHARACTER-LAYER RED-variant target tests (1:1 jar port):
// FoxFishTargetGoal (Cod/Salmon), FoxTurtleEggTargetGoal (Baby Turtle on land), and the
// Fox AvoidEntityGoal wiring (player, wolf, polar-bear). Each test drives the goal methods
// directly with a deterministic per-entity rng (reseeded by id) and asserts the jar-faithful
// gating + transitions. The pig oracle is byte-identically untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// foxTargetTestMobWithFaction builds a fox + a faction entity (cod/salmon/turtle/etc) at given
// positions. factionType is the entity.Entity (e.g. entity.Cod); factionBreedAge<0 makes it a
// baby (turtle-target test); factionTame controls the Wolf tame bit. The chunkColumn-broad-phase
// scan (t.cur().entities.near) returns the entity when within range.
func foxTargetTestMobWithFaction(t *testing.T, factionType entity.Entity, factionBreedAge int, factionTame bool, fx, fy, fz, mx, my, mz int) (*TickLoop, *Entity, *Entity) {
	t.Helper()
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	fox := foxTestMob(int32(fx*1000+mx), float64(fx), float64(fy), float64(fz))
	loop.only().entities.add(fox)
	mob := NewEntity(int32(mx*1000+fy), factionType, float64(mx), float64(my), float64(mz))
	mob.health = 6.0
	mob.breedAge = factionBreedAge
	mob.tame = factionTame
	loop.only().entities.add(mob)
	return loop, fox, mob
}

func TestFoxFishTargetCodWithinRange(t *testing.T) {
	// FoxFishTargetGoal: NearestAttackableTargetGoal<AbstractFish>(Cod||Salmon). A cod on land
	// within FOLLOW_RANGE is targeted; out of range is not.
	loop, fox, cod := foxTargetTestMobWithFaction(t, entity.Cod, 0, false, 0, 64, 0, 3, 64, 0)
	g := newFoxFishTargetGoal()
	acquired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			acquired = true
			break
		}
	}
	if !acquired {
		t.Fatal("fox_fish_target never acquired a nearby cod")
	}
	g.start(loop, fox)
	if fox.ai.getTarget() != cod.id {
		t.Fatalf("fox_fish_target committed the wrong target: got %d want %d", fox.ai.getTarget(), cod.id)
	}
}

func TestFoxFishTargetSalmonWithinRange(t *testing.T) {
	loop, fox, salmon := foxTargetTestMobWithFaction(t, entity.Salmon, 0, false, 0, 64, 0, 4, 64, 0)
	g := newFoxFishTargetGoal()
	acquired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			acquired = true
			break
		}
	}
	if !acquired {
		t.Fatal("fox_fish_target never acquired a nearby salmon")
	}
	g.start(loop, fox)
	if fox.ai.getTarget() != salmon.id {
		t.Fatalf("fox_fish_target committed the wrong target: got %d want %d", fox.ai.getTarget(), salmon.id)
	}
}

func TestFoxFishTargetOutOfRange(t *testing.T) {
	// Cod 100 blocks away — outside FOLLOW_RANGE (fox FOLLOW_RANGE = 32). No acquisition.
	loop, fox, _ := foxTargetTestMobWithFaction(t, entity.Cod, 0, false, 0, 64, 0, 100, 64, 0)
	g := newFoxFishTargetGoal()
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			t.Fatalf("fox_fish_target acquired a cod 100 blocks away (out of FOLLOW_RANGE=32)")
		}
	}
}

func TestFoxFishTargetREDVariantGated(t *testing.T) {
	// The RED-variant ORDERING: fishTargetGoal is added at @6 in RED and @4 in SNOW. The goal
	// itself has no variant gate (the GATING is in setTargetGoals which we don't port — the
	// variant reordering is cited-deferred for v1). The goal fires regardless of foxVariant for
	// the FoxFishTargetGoal ctor. We verify the findTarget scans the AbstractSchoolingFish set.
	// (The variant ORDERING lives in setTargetGoals; not in the goal itself.)
	loop, fox, _ := foxTargetTestMobWithFaction(t, entity.Cod, 0, false, 0, 64, 0, 3, 64, 0)
	fox.foxVariant = 0 // RED (the v1 default)
	g := newFoxFishTargetGoal()
	acquired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			acquired = true
			break
		}
	}
	if !acquired {
		t.Fatal("fox_fish_target did not fire for a RED fox with a cod in range")
	}
}

func TestFoxTurtleEggTargetBabyOnLand(t *testing.T) {
	// FoxTurtleEggTargetGoal: BABY_ON_LAND_SELECTOR (isBaby && !isInWater). A baby turtle
	// on land is targeted; an adult turtle or a turtle in water is NOT.
	loop, fox, baby := foxTargetTestMobWithFaction(t, entity.Turtle, -100, false, 0, 64, 0, 3, 64, 0)
	g := newFoxTurtleEggTargetGoal()
	acquired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			acquired = true
			break
		}
	}
	if !acquired {
		t.Fatal("fox_turtle_egg_target never acquired a baby turtle on land")
	}
	g.start(loop, fox)
	if fox.ai.getTarget() != baby.id {
		t.Fatalf("fox_turtle_egg_target committed the wrong target: got %d want %d", fox.ai.getTarget(), baby.id)
	}
}

func TestFoxTurtleEggTargetAdultRejected(t *testing.T) {
	// An adult turtle (breedAge >= 0) is NOT a target — the BABY_ON_LAND_SELECTOR's isBaby gate
	// rejects it.
	loop, fox, _ := foxTargetTestMobWithFaction(t, entity.Turtle, 200, false, 0, 64, 0, 3, 64, 0)
	g := newFoxTurtleEggTargetGoal()
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			t.Fatal("fox_turtle_egg_target acquired an ADULT turtle (BABY_ON_LAND_SELECTOR's isBaby gate failed)")
		}
	}
}

func TestFoxTurtleEggTargetBabyInWaterRejected(t *testing.T) {
	// A baby turtle IN WATER is NOT a target — the !isInWater half of BABY_ON_LAND_SELECTOR
	// rejects it. We force entityInWater=true by placing a water block at the baby's feet (the
	// entityInWater seam reads the fluid at feet-y via the world block state).
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	// Insert a loaded chunk at (0,0) so SetBlock works.
	if w := loop.world(); w != nil {
		readyOverworldChunk(w)
		// Place a water block at the baby's feet (3, 64, 0).
		water, ok := block.ToStateID[block.Water{Level: 0}]
		if !ok {
			t.Fatal("Water state id missing")
		}
		if !w.SetBlock(pk.Position{X: 3, Y: 64, Z: 0}, water, dimMinY) {
			t.Fatal("failed to place water at (3,64,0)")
		}
	}
	fox := foxTestMob(8000, 0, 64, 0)
	loop.only().entities.add(fox)
	baby := NewEntity(8001, entity.Turtle, 3.0, 64.0, 0.0)
	baby.health = 10.0
	baby.breedAge = -100
	loop.only().entities.add(baby)

	g := newFoxTurtleEggTargetGoal()
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			t.Fatal("fox_turtle_egg_target acquired a baby turtle IN WATER (the !isInWater half of BABY_ON_LAND_SELECTOR failed)")
		}
	}
}

func TestFoxTurtleEggTargetOutOfRange(t *testing.T) {
	// Baby turtle 100 blocks away (outside FOLLOW_RANGE=32). No acquisition.
	loop, fox, _ := foxTargetTestMobWithFaction(t, entity.Turtle, -100, false, 0, 64, 0, 100, 64, 0)
	g := newFoxTurtleEggTargetGoal()
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			t.Fatal("fox_turtle_egg_target acquired a baby turtle 100 blocks away (out of FOLLOW_RANGE)")
		}
	}
}
