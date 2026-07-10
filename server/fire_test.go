package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// TestIgniteAndFireCountdown: igniteForSeconds sets the countdown (floor(s*20)); tickEntityFire
// decrements it and deals 1 damage every 20 ticks; it extinguishes at 0.
func TestIgniteAndFireCountdown(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Zombie, 8.5, 100, 8.5)
	e.health = 20

	loop.igniteForSeconds(e, 8.0)
	if e.remainingFireTicks != 160 {
		t.Fatalf("igniteForSeconds(8) -> remainingFireTicks %d, want 160", e.remainingFireTicks)
	}

	// A shorter ignite must NOT shorten an active longer burn (igniteForTicks only extends).
	loop.igniteForSeconds(e, 1.0)
	if e.remainingFireTicks != 160 {
		t.Fatalf("a shorter ignite shortened the burn to %d, want 160 (igniteForTicks never shortens)", e.remainingFireTicks)
	}

	startHP := e.health
	// Tick once: remainingFireTicks%20==0 (160) → 1 fire damage, then decrement to 159.
	loop.tickEntityFire(e)
	if e.remainingFireTicks != 159 {
		t.Fatalf("after one tick remainingFireTicks %d, want 159", e.remainingFireTicks)
	}
	if e.health >= startHP {
		t.Fatalf("no fire damage on the %%20==0 tick (health %v -> %v)", startHP, e.health)
	}

	// Run out the burn; it must reach 0 and stop.
	for i := 0; i < 200 && e.remainingFireTicks > 0; i++ {
		loop.tickEntityFire(e)
	}
	if e.remainingFireTicks != 0 {
		t.Fatalf("fire never extinguished: remainingFireTicks %d", e.remainingFireTicks)
	}
	// A non-burning entity is a no-op (no panic, no damage).
	loop.tickEntityFire(e)
}

// TestFireResistanceNegatesFireDamage: a mob holding FIRE_RESISTANCE takes NO on_fire tick damage
// (LivingEntity.hurtServer bytecode 20-41: source.is(IS_FIRE) && hasEffect(FIRE_RESISTANCE) -> return
// false). Without the effect the same on-fire tick lands 1 damage.
func TestFireResistanceNegatesFireDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Baseline: a burning mob with NO effect takes 1 fire damage on the %20==0 tick.
	bare := NewEntity(1, entity.Zombie, 8.5, 100, 8.5)
	bare.health = 20
	loop.igniteForSeconds(bare, 8.0) // remainingFireTicks 160 -> %20==0 on the first tick
	loop.tickEntityFire(bare)
	if bare.health >= 20 {
		t.Fatalf("bare burning mob should take fire damage (health %v, want < 20)", bare.health)
	}

	// With FIRE_RESISTANCE the on_fire hit is fully negated.
	fr := NewEntity(2, entity.Zombie, 8.5, 100, 8.5)
	fr.health = 20
	loop.addEntityEffect(fr, effectFireResistance, 600, 0)
	loop.igniteForSeconds(fr, 8.0)
	loop.tickEntityFire(fr)
	if fr.health != 20 {
		t.Fatalf("FIRE_RESISTANCE mob took fire damage: health %v, want 20 (source.is(IS_FIRE) && hasEffect(FIRE_RESISTANCE) -> return false)", fr.health)
	}
}

// TestSunSensitiveGate: the burn_in_daylight entity_type tag members are sun-sensitive (skeleton, stray,
// wither_skeleton, bogged, zombie, zombie_horse, zombie_villager, drowned, zombie_nautilus, phantom); a
// pig and a HUSK are NOT (Husk is deliberately excluded from the tag). Cite the vanilla
// data/minecraft/tags/entity_type/burn_in_daylight.json.
func TestSunSensitiveGate(t *testing.T) {
	for _, typ := range []entity.Entity{
		entity.Skeleton, entity.Stray, entity.WitherSkeleton, entity.Bogged, entity.Zombie,
		entity.ZombieHorse, entity.ZombieVillager, entity.Drowned, entity.ZombieNautilus, entity.Phantom,
	} {
		e := NewEntity(1, typ, 0, 0, 0)
		if !e.isSunSensitive() {
			t.Fatalf("%s is in burn_in_daylight and must be sun-sensitive", typ.Name)
		}
	}
	// Pig (passive, pig-oracle guard) and Husk (excluded from the tag) must NOT be sun-sensitive.
	for _, typ := range []entity.Entity{entity.Pig, entity.Husk} {
		e := NewEntity(2, typ, 0, 0, 0)
		if e.isSunSensitive() {
			t.Fatalf("%s must NOT be sun-sensitive", typ.Name)
		}
	}
}

// TestIsDayWindow: daytime is outside the [13000,23000) night window.
func TestIsDayWindow(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 6000 // noon
	if !loop.isDay() {
		t.Error("gametime 6000 (noon) should be day")
	}
	loop.gametime = 18000 // midnight
	if loop.isDay() {
		t.Error("gametime 18000 (midnight) should be night")
	}
}

func TestSkeletonBurnsInDaylight(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 6000
	s := NewEntity(10, entity.Skeleton, 8.5, 64, 8.5)
	s.health = 20

	loop.tickMobSunBurn(s)

	if s.remainingFireTicks != 160 {
		t.Fatalf("skeleton daylight burn ticks = %d, want 160", s.remainingFireTicks)
	}
}

func TestSkeletonDoesNotBurnAtNight(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 18000
	s := NewEntity(11, entity.Skeleton, 8.5, 64, 8.5)
	s.health = 20

	loop.tickMobSunBurn(s)

	if s.remainingFireTicks != 0 {
		t.Fatalf("night skeleton fire ticks = %d, want 0", s.remainingFireTicks)
	}
}

func TestSkeletonDoesNotBurnInWater(t *testing.T) {
	loop, mgr := newSunBurnLightLoop()
	loop.gametime = 6000
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	s := NewEntity(12, entity.Skeleton, 8.5, 64, 8.5)
	s.health = 20

	loop.tickMobSunBurn(s)

	if s.remainingFireTicks != 0 {
		t.Fatalf("water skeleton fire ticks = %d, want 0", s.remainingFireTicks)
	}
}

func TestZombieWithHelmetDoesNotBurn(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 6000
	z := NewEntity(13, entity.Zombie, 8.5, 64, 8.5)
	z.health = 20
	z.setItemSlot(eqSlotHead, itemStackOf(item.IronHelmet))

	loop.tickMobSunBurn(z)

	if z.remainingFireTicks != 0 {
		t.Fatalf("helmeted zombie fire ticks = %d, want 0", z.remainingFireTicks)
	}
}

func TestZombieWithoutHelmetBurns(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 6000
	z := NewEntity(14, entity.Zombie, 8.5, 64, 8.5)
	z.health = 20

	loop.tickMobSunBurn(z)

	if z.remainingFireTicks != 160 {
		t.Fatalf("bare zombie fire ticks = %d, want 160", z.remainingFireTicks)
	}
}

func TestPigNotSunSensitive(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 6000
	p := NewEntity(15, entity.Pig, 8.5, 64, 8.5)
	p.health = 10

	loop.tickMobSunBurn(p)

	if p.remainingFireTicks != 0 {
		t.Fatalf("pig fire ticks = %d, want 0", p.remainingFireTicks)
	}
}

func newSunBurnLightLoop() (*TickLoop, *world.ChunkManager) {
	loop, mgr := newFluidLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	for i := range ch.Sections {
		ch.Sections[i].SkyLight = bytes.Repeat([]byte{0xff}, 2048)
	}
	loop.spawnSurfaceY = 64
	return loop, mgr
}
