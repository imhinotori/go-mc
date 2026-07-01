package server

// ravager_test.go — RAIDER (Task): the per-mob behavior test for the Ravager, the 1:1 vanilla Ravager
// re-expressed as the vanilla_ravager Starlark plugin (float@0 + Go-native melee@4 + hurt_by@2 + nearest@3 +
// patrol@4) PLUS the Go-native ravagerAiStep roar/stun/attack state machine. Asserts (a) the ravager HUNTS
// and MELEES the player (ATTACK_DAMAGE 12.0, and doHurtTarget sets attackTick=10), and (b) the roar() AoE
// damages a nearby non-illager mob 6.0 + knocks it back (driven directly, since the stun TRIGGER is deferred).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// TestRavagerMeleeAndAttackTick: a spawned ravager next to a player ACQUIRES it, deals REAL ATTACK_DAMAGE
// (12.0), and its doHurtTarget sets attackTick=10 (ravagerDidHurt).
func TestRavagerMeleeAndAttackTick(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_ravager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Ravager.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Ravager) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_ravager"]
	rv := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	rv.onGround = true
	if rv.typ != entity.Ravager.ID {
		t.Fatalf("ravager typ = %d, want %d", rv.typ, entity.Ravager.ID)
	}

	p := combatTestPlayer(loop, 9.2, float64(floorY+1), 8.5, 7402)
	start := p.health

	acquired := false
	sawAttackTick := false
	for i := 0; i < 300; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if rv.ai.getTarget() == p.entityID {
			acquired = true
		}
		if rv.ravagerAttackTick > 0 {
			sawAttackTick = true
		}
		if p.health < start {
			break
		}
	}
	if !acquired {
		t.Fatal("the ravager never ACQUIRED the player")
	}
	if p.health >= start {
		t.Fatalf("the player took NO melee damage (health %v >= %v)", p.health, start)
	}
	if dealt := start - p.health; dealt != float32(rv.getAttributeValue(attribute.AttackDamage)) {
		t.Fatalf("player lost %v health, want %v (Ravager ATTACK_DAMAGE 12.0)", dealt, rv.getAttributeValue(attribute.AttackDamage))
	}
	if !sawAttackTick {
		t.Fatal("doHurtTarget never set attackTick (ravagerDidHurt not wired)")
	}
}

// TestRavagerRoarAoE: drive ravagerRoar directly (the stun TRIGGER that arms the roar is cite-deferred, so
// exercise the roar effect head-on). A nearby non-illager mob (a cow) in the roar box takes 6.0 damage AND
// is knocked back (strongKnockback). Cite Ravager.roar.
func TestRavagerRoarAoE(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_ravager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_ravager"]
	rv := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	rv.onGround = true

	// A cow victim 2 blocks away (inside the roar box = ravager AABB inflated by 4.0), health 10.
	victim := NewEntity(999001, entity.Cow, 10.5, float64(floorY+1), 8.5)
	victim.health = 10
	victim.width = 0.9
	victim.height = 1.4
	loop.only().entities.add(victim)
	startHealth := victim.health

	// Drive the roar directly on the owning region: set roarTick to fire the roar this aiStep (roar() runs
	// when roarTick decrements to 10), then step ravagerAiStep once.
	loop.withRegion(loop.only(), func() {
		rv.ravagerRoarTick = 11
		loop.ravagerAiStep(rv)
	})

	if victim.health >= startHealth {
		t.Fatalf("roar dealt NO damage (victim health %v >= %v) — the roar AoE missed the cow", victim.health, startHealth)
	}
	if dealt := startHealth - victim.health; dealt != float32(ravagerRoarDamage) {
		t.Fatalf("roar dealt %v damage, want %v (Ravager.roar hurtServer 6.0)", dealt, ravagerRoarDamage)
	}
	// strongKnockback pushes the cow AWAY (+x, since it is at +x of the ravager). vx must be > 0.
	if victim.vx <= 0 {
		t.Fatalf("roar did NOT knock the cow back (vx %v <= 0) — strongKnockback missed", victim.vx)
	}
}
