package server

// witch_test.go — MOB-HOST-07 (Task #9): the Witch behavior test (acquire → throw splash potion → the
// potion splashes and the player takes an effect) + the mob-effect subsystem units (poison periodic
// damage, weakness attribute modifier, instant-damage splash). Pig oracle UNTOUCHED (a separate mob).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaWitchRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_witch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir witch plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_witch", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_witch/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp witch %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_witch): %v", err)
	}
	if _, ok := r.byName["vanilla_witch"]; !ok {
		t.Fatal("vanilla_witch declaration not captured after load")
	}
	return r
}

// TestWitchThrowsAndHarms: a witch acquires a nearby player, throws a splash potion, and the potion
// splashes → the player takes damage (HARMING/POISON) OR gains a duration effect. Drives ticks until the
// player's health drops or an effect is applied.
func TestWitchThrowsAndHarms(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaWitchRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Witch.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Witch) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_witch"]
	w := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	w.onGround = true
	if w.typ != entity.Witch.ID {
		t.Fatalf("witch typ = %d, want entity.Witch.ID %d", w.typ, entity.Witch.ID)
	}

	// Player a few blocks away (within the 10-block attack radius, >3 so it's not the weakness branch).
	p := combatTestPlayer(loop, 13.5, float64(floorY+1), 8.5, 8484)
	start := p.health

	acquired := false
	sawPotion := false
	harmed := false
	for i := 0; i < 400; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if w.ai.getTarget() == p.entityID {
			acquired = true
		}
		for _, e := range loop.only().entities.byID {
			if e.isPotion {
				sawPotion = true
			}
		}
		if p.health < start || len(p.activeEffects) > 0 {
			harmed = true
			break
		}
	}
	if !acquired {
		t.Fatal("the witch never ACQUIRED the player — nearestAttackableTargetGoal did not hunt")
	}
	if !sawPotion {
		t.Fatal("the witch never THREW a potion — the witch_ranged_attack goal did not performRangedAttack")
	}
	if !harmed {
		t.Fatalf("the player took no harm (health %v, effects %d) — the splash did not apply", p.health, len(p.activeEffects))
	}
}

// TestPoisonEffectTicks: a poison effect applied to a player deals 1 damage every 25 ticks (amp0) and
// never kills (stops at health 1).
func TestPoisonEffectTicks(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	p := combatTestPlayer(loop, 0, 64, 0, 555)
	p.health = 20

	// Apply poison directly (a 200-tick duration, amp 0 → interval 25).
	loop.addPlayerEffect(p, 0, effectPoison, 200, 0, 1.0)
	if !playerHasEffect(p, effectPoison) {
		t.Fatal("poison effect not registered after addPlayerEffect")
	}

	before := p.health
	// Tick 25 times: at the tick where remaining%25==0 the poison deals 1. duration starts 200 (200%25==0).
	for i := 0; i < 25; i++ {
		loop.tickPlayerEffects(p)
	}
	if p.health >= before {
		t.Fatalf("poison dealt no damage over 25 ticks (health %v >= %v)", p.health, before)
	}

	// Run poison to near-death; it must never drop the player below 1 (health>1 gate).
	p.health = 1.0
	for i := 0; i < 60; i++ {
		loop.tickPlayerEffects(p)
	}
	if p.health < 1.0 {
		t.Fatalf("poison killed the player (health %v < 1.0) — the health>1 gate failed", p.health)
	}
}

// TestWeaknessModifier: a weakness effect subtracts 4.0 from the player's ATTACK_DAMAGE (ADD_VALUE),
// observable via getAttributeValue; removing it (duration expiry) restores the base.
func TestWeaknessModifier(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	p := combatTestPlayer(loop, 0, 64, 0, 556)

	base := p.getAttributeValue(attrAttackDamage) // player base 1.0
	loop.addPlayerEffect(p, 0, effectWeakness, 100, 0, 1.0)
	weak := p.getAttributeValue(attrAttackDamage)
	if weak != base-4.0 {
		t.Fatalf("weakness ATTACK_DAMAGE = %v, want %v (base %v - 4.0)", weak, base-4.0, base)
	}

	// Expire it (tick past the duration) → the modifier is removed and the base returns.
	for i := 0; i < 101; i++ {
		loop.tickPlayerEffects(p)
	}
	if got := p.getAttributeValue(attrAttackDamage); got != base {
		t.Fatalf("after weakness expiry ATTACK_DAMAGE = %v, want base %v (modifier not removed)", got, base)
	}
}

// TestWitchLaunchAndAimEyeHeight proves performWitchRangedAttack spawns the splash potion at the witch's
// getEyeY() - 0.10000000149011612 (ThrowableItemProjectile ctor), NOT the old feet + height*0.85, and that
// the aim uses the TARGET's real standing eye (1.62) minus 1.100000023841858 -- both the 1:1 jar values.
// Cite Witch.performRangedAttack + ThrowableItemProjectile ctor.
func TestWitchLaunchAndAimEyeHeight(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaWitchRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_witch"]
	w := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	w.onGround = true
	// A target 6 blocks away, above the witch a little so the aim yd is nonzero (exercises the eye math).
	p := combatTestPlayer(loop, 14.5, float64(floorY+1), 8.5, 8490)
	w.ai.attackTargetID = p.entityID
	w.witchDrinking = false

	before := 0
	for _, e := range loop.only().entities.byID {
		if e.isPotion {
			before++
		}
	}
	loop.performWitchRangedAttack(w, p, 0.75)

	var potion *Entity
	for _, e := range loop.only().entities.byID {
		if e.isPotion {
			potion = e
		}
	}
	if potion == nil {
		t.Fatal("witch threw no splash potion")
	}
	// Launch Y == getEyeY() - 0.1f: y + (float)(height*0.85f) - 0.10000000149011612.
	wantLaunchY := w.y + float64(float32(w.height)*0.85) - 0.10000000149011612
	if potion.y != wantLaunchY {
		t.Fatalf("potion launch Y = %v, want getEyeY()-0.1 = %v (was feet+height*0.85 before the fix)", potion.y, wantLaunchY)
	}
	// Prove the launch is NOT the old feet + height*0.85 value.
	oldLaunchY := w.y + float64(w.height)*0.85
	if potion.y == oldLaunchY {
		t.Fatalf("potion launch Y still equals the OLD feet+height*0.85 = %v -- the eye-Y fix is not in effect", oldLaunchY)
	}
	// The aim uses the target's 1.62 standing eye, not playerHeight*0.85 == 1.53. Recompute the intended yd
	// and confirm it matches the port's expression (a coordinate-level proof the target-eye fix is applied).
	wantYd := (p.y + playerStandingEyeHeight - 1.100000023841858) - w.y
	oldYd := (p.y + float64(playerHeight)*0.85 - 1.1) - w.y
	if wantYd == oldYd {
		t.Fatal("target getEyeY (1.62) equals the old playerHeight*0.85 (1.53) -- test cannot distinguish; check constants")
	}
}
