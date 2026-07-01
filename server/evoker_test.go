package server

// evoker_test.go — RAIDER (Task): the per-mob behavior test for the Evoker, the 1:1 vanilla Evoker
// re-expressed as the vanilla_evoker Starlark plugin (float@0 + Go-native spell goals @1/@4/@5/@6 +
// hurt_by@1 + nearest@2 + patrol@4). The CORE assertion is the phase goal "the evoker HUNTS the player and
// CASTS a spell": a spawned evoker + a player drives ticks until the evoker ACQUIRES the player AND begins
// casting (currentSpell != NONE + spellCastingTickCount > 0). The Vex/EvokerFangs spawns are cite-deferred;
// the RNG-faithful cast STATE MACHINE is the assertion. Cite Evoker.registerGoals + SpellcasterUseSpellGoal.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// TestEvokerCastsSpell: a spawned evoker acquires a player and enters a spell cast (spellCastingTickCount>0,
// currentSpell set). Since the evoker FLEES the player (AvoidEntity, cite-deferred) it stays at range; the
// summon/fangs spells fire on the tickCount cooldown.
func TestEvokerCastsSpell(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_evoker"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Evoker.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Evoker) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_evoker"]
	ev := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	ev.onGround = true
	if ev.typ != entity.Evoker.ID {
		t.Fatalf("evoker typ = %d, want %d", ev.typ, entity.Evoker.ID)
	}

	p := combatTestPlayer(loop, 11.5, float64(floorY+1), 8.5, 7404)

	acquired := false
	castSpell := false
	var spellSeen int32
	for i := 0; i < 400 && !castSpell; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if ev.ai.getTarget() == p.entityID {
			acquired = true
		}
		if ev.spellCastingTickCount > 0 && ev.currentSpell != illagerSpellNone {
			castSpell = true
			spellSeen = ev.currentSpell
		}
	}
	if !acquired {
		t.Fatal("the evoker never ACQUIRED the player")
	}
	if !castSpell {
		t.Fatal("the evoker never CAST a spell (spellCastingTickCount stayed 0 / currentSpell NONE)")
	}
	if spellSeen != illagerSpellSummon && spellSeen != illagerSpellFangs {
		t.Fatalf("evoker cast spell id %d, want SUMMON_VEX(1) or FANGS(2) against a player", spellSeen)
	}
}
