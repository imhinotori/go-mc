package server

import "testing"

func TestActiveEffectHiddenDowngradesAfterOverride(t *testing.T) {
	cur := newActiveEffect(effectSlowness, 200, 0)
	override := newActiveEffect(effectSlowness, 40, 1)
	if !updateActiveEffect(cur, override) {
		t.Fatal("stronger shorter effect did not update the current MobEffectInstance")
	}
	if cur.amplifier != 1 || cur.duration != 40 {
		t.Fatalf("override active = amp %d duration %d, want amp 1 duration 40", cur.amplifier, cur.duration)
	}
	if cur.hidden == nil || cur.hidden.amplifier != 0 || cur.hidden.duration != 200 {
		t.Fatalf("hidden effect = %+v, want original amp 0 duration 200", cur.hidden)
	}
	for i := 0; i < 40; i++ {
		tickDownActiveEffect(cur)
	}
	if !downgradeActiveEffect(cur) {
		t.Fatal("expired override did not downgrade to hidden effect")
	}
	if cur.amplifier != 0 || cur.duration != 160 {
		t.Fatalf("downgraded effect = amp %d duration %d, want amp 0 duration 160", cur.amplifier, cur.duration)
	}
}

func TestPlayerInstantHealthHealsAndDoesNotStore(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.health = 5

	loop.addPlayerEffect(p, 0, effectInstantHealth, 1, 0, 1.0)
	if p.health != 9 {
		t.Fatalf("instant_health player health = %v, want 9", p.health)
	}
	if playerHasEffect(p, effectInstantHealth) {
		t.Fatal("instant_health should not be stored as a duration effect")
	}
}

func TestMobWitherEffectTicks(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 20

	loop.addEntityEffect(w, effectWither, 40, 0)
	loop.tickMobEffects(w)
	if w.health != 19 {
		t.Fatalf("mob health after first Wither tick = %v, want 19", w.health)
	}
}
