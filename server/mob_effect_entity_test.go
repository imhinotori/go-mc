package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func mobEffectEntityLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

func mobEffectTestEntity(loop *TickLoop, typ entity.Entity, x, y, z float64) *Entity {
	e := NewEntity(loop.idAlloc.AllocID(), typ, x, y, z)
	initSpawnHealth(e)
	loop.cur().entities.add(e)
	return e
}

func TestWitherEffectTicksPlayer(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.health = maxHealth

	loop.addPlayerEffect(p, 0, effectWither, 40, 0, 1.0)
	loop.tickPlayerEffects(p)

	if got := p.health; got != maxHealth-1.0 {
		t.Fatalf("WITHER tick health = %v, want %v", got, maxHealth-1.0)
	}
	if e := p.activeEffects[effectWither]; e == nil || e.duration != 39 {
		t.Fatalf("WITHER duration after tick = %#v, want duration 39", e)
	}
}

func TestEntityInstantHealHarmInvertsForUndead(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	z := mobEffectTestEntity(loop, entity.Zombie, 8.5, float64(floorY+1), 8.5)

	z.health = 20.0
	loop.addEntityEffect(z, effectInstantHealth, 1, 0)
	if got := z.health; got != 14.0 {
		t.Fatalf("instant_health on undead health = %v, want 14.0", got)
	}

	z.health = 10.0
	loop.addEntityEffect(z, effectInstantDamage, 1, 0)
	if got := z.health; got != 14.0 {
		t.Fatalf("instant_damage on undead health = %v, want 14.0", got)
	}
}

func TestUndeadRejectPoisonAndRegeneration(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	z := mobEffectTestEntity(loop, entity.Zombie, 8.5, float64(floorY+1), 8.5)

	loop.addEntityEffect(z, effectPoison, 100, 0)
	if entityHasEffect(z, effectPoison) {
		t.Fatal("undead accepted POISON; canBeAffected must reject ignores_poison_and_regen")
	}
	loop.addEntityEffect(z, effectRegeneration, 100, 0)
	if entityHasEffect(z, effectRegeneration) {
		t.Fatal("undead accepted REGENERATION; canBeAffected must reject ignores_poison_and_regen")
	}
}

func TestMobAbsorptionModifierLifecycle(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	pig := mobEffectTestEntity(loop, entity.Pig, 8.5, float64(floorY+1), 8.5)

	loop.addEntityEffect(pig, effectAbsorption, 1, 0)
	if got := pig.getAbsorptionAmount(); got != 4.0 {
		t.Fatalf("mob absorption fill = %v, want 4.0", got)
	}
	if got := pig.getAttributeValue(attribute.MaxAbsorption); got != 4.0 {
		t.Fatalf("mob MAX_ABSORPTION with effect = %v, want 4.0", got)
	}

	loop.tickMobEffects(pig)
	if entityHasEffect(pig, effectAbsorption) {
		t.Fatal("mob ABSORPTION should expire after duration 1")
	}
	if got := pig.getAbsorptionAmount(); got != 0.0 {
		t.Fatalf("mob absorption after expiry = %v, want 0", got)
	}
	if got := pig.getAttributeValue(attribute.MaxAbsorption); got != 0.0 {
		t.Fatalf("mob MAX_ABSORPTION after expiry = %v, want 0", got)
	}
}

func TestSplashPotionAppliesToMob(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	pig := mobEffectTestEntity(loop, entity.Pig, 8.5, float64(floorY+1), 8.5)
	potion := loop.spawnSplashPotion(0, 8.5, float64(floorY+1), 8.5, 0, 0, 0, []splashEffect{
		{id: effectPoison, duration: 100, amplifier: 0},
	})

	loop.splashPotion(potion)

	if !entityHasEffect(pig, effectPoison) {
		t.Fatal("splash potion did not apply POISON to nearby pig")
	}
	if _, ok := loop.cur().entities.get(potion.id); ok {
		t.Fatal("splash potion entity was not discarded")
	}
}
