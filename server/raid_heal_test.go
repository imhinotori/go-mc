package server

// raid_heal_test.go — the Witch self-drink potion buff branch (Witch.aiStep, ai_goals_witch.go
// witchAiStep) + the entity-side mob-effect slice (mob_effect.go addEntityEffect/tickMobEffects) + the
// NearestHealableRaiderTargetGoal STRUCTURE (inert without a raid). Deterministic per-mob RNG (seeded like
// the fox/skeleton behavior tests). Pig oracle UNTOUCHED (a separate mob).

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// witchDrinkLoop builds a physics loop with a stone floor + the vanilla_witch registry, so a spawned witch
// carries its seeded attributes (max_health 26, movement_speed 0.25).
func witchDrinkLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaWitchRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

func spawnWitch(loop *TickLoop, x, y, z float64) *Entity {
	decl := loop.mobRegistry.byName["vanilla_witch"]
	return loop.spawnDeclaredMob(decl, x, y, z)
}

// TestWitchInstantHealthSelfHeal: addEntityEffect(instant_health) heals the witch 4 (amp0, self scale 1.0),
// clamped to max health. It is INSTANT (never enters the duration map).
func TestWitchInstantHealthSelfHeal(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 10.0

	loop.addEntityEffect(w, effectInstantHealth, 1, 0)
	if w.health != 14.0 {
		t.Fatalf("instant_health healed to %v, want 14 (10 + (int)(1.0*(4<<0)+0.5))", w.health)
	}
	if entityHasEffect(w, effectInstantHealth) {
		t.Fatal("instant_health should be an instant effect, not stored in the duration map")
	}

	// Heal clamps to max health (26.0 for a witch).
	w.health = 25.0
	loop.addEntityEffect(w, effectInstantHealth, 1, 0)
	maxH := float32(w.getAttributeValue(attribute.MaxHealth))
	if w.health != maxH {
		t.Fatalf("instant_health over max healed to %v, want clamp %v", w.health, maxH)
	}
}

// TestWitchRegenerationTicks: a regeneration effect on the witch heals 1.0 every 50 ticks (amp0) while
// health < max, and counts down + expires.
func TestWitchRegenerationTicks(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 10.0

	// Apply regeneration directly (duration 120, amp0 -> interval 50).
	loop.addEntityEffect(w, effectRegeneration, 120, 0)
	if !entityHasEffect(w, effectRegeneration) {
		t.Fatal("regeneration not stored in the entity effect map")
	}
	before := w.health
	for i := 0; i < 120; i++ {
		loop.tickMobEffects(w)
	}
	if w.health <= before {
		t.Fatalf("regeneration did not heal (health %v -> %v)", before, w.health)
	}
	if entityHasEffect(w, effectRegeneration) {
		t.Fatal("regeneration should have expired after its duration counted down")
	}
}

// TestWitchDrinkFinishAppliesEffect: driving witchAiStep while the drink countdown finishes applies the
// pending potion effect and removes the drinking speed modifier.
func TestWitchDrinkFinishAppliesEffect(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 10.0

	// Arm a drink of HEALING manually (as the ladder would), then attach the drinking modifier.
	w.witchDrinking = true
	w.witchUsingTime = witchPotionUseDuration
	w.witchDrinkPending = effectInstantHealth
	w.witchDrinkPendingDur = 1
	loop.witchAddDrinkingModifier(w)
	inst := w.attributes.GetInstance(attribute.MovementSpeed.Name())
	if inst == nil {
		t.Fatal("witch has no movement_speed attribute instance")
	}

	// Drive witchAiStep until the countdown finishes.
	for i := 0; i < witchPotionUseDuration+2; i++ {
		loop.witchAiStep(w)
	}
	if w.witchDrinking {
		t.Fatal("drink never finished (witchDrinking still set after the countdown)")
	}
	if w.health <= 10.0 {
		t.Fatalf("drink finish did not apply the HEALING self-heal (health %v)", w.health)
	}
	// The drinking speed modifier must be gone on finish.
	inst2 := w.attributes.GetInstance(attribute.MovementSpeed.Name())
	base := inst2.Value()
	// Re-attaching + removing should leave the base value (no lingering -0.25 modifier).
	loop.witchAddDrinkingModifier(w)
	loop.witchRemoveDrinkingModifier(w)
	if inst2.Value() != base {
		t.Fatalf("drinking modifier left a residue: %v vs base %v", inst2.Value(), base)
	}
}

// TestWitchAiStepEventuallyDrinks: a hurt witch (health<max), driven many ticks, eventually rolls a self-
// drink (the HEALING rung, rand<0.05 — deterministic per-mob RNG guarantees it fires within the window).
func TestWitchAiStepEventuallyDrinks(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 10.0 // < max 26 -> the HEALING rung is eligible

	drank := false
	for i := 0; i < 2000; i++ {
		loop.witchAiStep(w)
		if w.witchDrinking {
			drank = true
			break
		}
	}
	if !drank {
		t.Fatal("witchAiStep never started a self-drink for a hurt witch over 2000 ticks")
	}
}

// TestHealableRaiderGoalInert: the NearestHealableRaiderTargetGoal STRUCTURE never acquires a target in v1
// (hasActiveRaid stub-false), but it holds the TARGET flag and canUse runs without panicking (RNG-faithful).
func TestHealableRaiderGoalInert(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)

	g := newNearestHealableRaiderTargetGoal()
	if g.flags() != flagTarget {
		t.Fatalf("healable-raider goal flags = %d, want flagTarget", g.flags())
	}
	for i := 0; i < 500; i++ {
		if g.canUse(loop, w) {
			t.Fatal("healable-raider goal acquired a target in v1 (it must be inert without an active raid)")
		}
	}
	// The witch's targetSelector holds the goal at @2 (registered + inert).
	seen := map[int]int{}
	for _, wg := range w.ai.targetSelector.goals {
		seen[wg.priority]++
	}
	if seen[2] != 1 {
		t.Fatalf("witch missing nearest_healable_raider_target@2 (targetSelector priority 2 count = %d)", seen[2])
	}
}
