package server

// pillager_test.go — RAIDER (Task): the per-mob behavior test for the Pillager, the 1:1 vanilla Pillager
// re-expressed as the vanilla_pillager Starlark plugin (float@0 + Go-native pillager_crossbow_attack@3 +
// hurt_by@1 + nearest@2 + patrol@4). The CORE assertion is the phase goal "the pillager HUNTS the player
// and FIRES its crossbow": a spawned pillager + a player drives ticks until the pillager ACQUIRES the player
// AND a crossbow ARROW is spawned into the store (the RangedCrossbowAttackGoal charged + released). Cite
// Pillager.registerGoals @3 RangedCrossbowAttackGoal + Pillager.performRangedAttack.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// TestPillagerFiresCrossbow: a spawned pillager acquires a player in crossbow range and FIRES an arrow.
func TestPillagerFiresCrossbow(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_pillager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Pillager.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Pillager) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_pillager"]
	pl := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	pl.onGround = true
	if pl.typ != entity.Pillager.ID {
		t.Fatalf("pillager typ = %d, want %d", pl.typ, entity.Pillager.ID)
	}

	// Player ~5 blocks away (inside crossbow attackRadius 8 -> radiusSqr 64) so the goal stops moving + fires.
	p := combatTestPlayer(loop, 13.5, float64(floorY+1), 8.5, 7403)

	acquired := false
	firedArrow := false
	for i := 0; i < 400 && !firedArrow; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if pl.ai.getTarget() == p.entityID {
			acquired = true
		}
		for _, e := range loop.only().entities.byID {
			if e.isArrow && e.arrowShooterID == pl.id {
				firedArrow = true
				break
			}
		}
	}
	if !acquired {
		t.Fatal("the pillager never ACQUIRED the player")
	}
	if !firedArrow {
		t.Fatal("the pillager never FIRED a crossbow arrow (RangedCrossbowAttackGoal did not release)")
	}
}
