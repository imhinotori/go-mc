package server

import (
	"testing"

	"github.com/google/uuid"
	"github.com/imhinotori/sulfur/save"
)

// food_test.go (Plan 17-19) gates the 1:1 FoodData hunger/exhaustion/regen/starvation port
// (food.go). Constants under test are the jar-verified vanilla values: exhaustion drains 4.0 at a
// time (threshold 4.0) into 1.0 saturation (or 1 food) loss; fast saturated regen every 10 ticks;
// starvation every 80 ticks (NORMAL stops at 1.0 HP); addExhaustion caps at 40.0. All deterministic
// (no RNG, no world needed — the movement ladder is exercised via direct foodDataTick calls so the
// nil-world eyeInWater/playerInWater both return false faithfully).

// foodPlayer makes a survival tickPlayer at full health/food with a capturing client (the
// starvation path's applyDamage -> actuallyHurt SetHealth send needs a sink). The dirty-send
// trackers seed to the spawn values, matching registration.
func foodPlayer() *tickPlayer {
	return &tickPlayer{
		health:             maxHealth,
		food:               maxFood,
		saturation:         defaultSaturation,
		lastFoodSent:       maxFood,
		lastSaturationSent: defaultSaturation,
		lastHealthSent:     maxHealth,
		client:             captureClient(64),
	}
}

// TestAddExhaustionCapsAt40 asserts addExhaustion accumulates and clamps at 40.0
// (FoodData.addExhaustion: Math.min(exhaustionLevel + v, 40.0f)).
func TestAddExhaustionCapsAt40(t *testing.T) {
	p := foodPlayer()
	p.addExhaustion(10)
	if p.exhaustion != 10 {
		t.Fatalf("exhaustion after +10 = %v, want 10", p.exhaustion)
	}
	p.addExhaustion(25)
	if p.exhaustion != 35 {
		t.Fatalf("exhaustion after +25 = %v, want 35", p.exhaustion)
	}
	// 35 + 10 = 45 -> clamped to 40.
	p.addExhaustion(10)
	if p.exhaustion != maxExhaustion {
		t.Fatalf("exhaustion cap = %v, want %v (min(sum, 40))", p.exhaustion, maxExhaustion)
	}
}

// TestExhaustionDrainsSaturationThenFood asserts the FoodData.tick drain block: exhaustion > 4.0
// subtracts 4.0 and converts to 1.0 saturation loss while saturation > 0, then 1 food loss once
// saturation is empty (NORMAL difficulty, not PEACEFUL).
func TestExhaustionDrainsSaturationThenFood(t *testing.T) {
	loop := &TickLoop{}
	p := foodPlayer()
	p.food = maxFood
	p.saturation = 2.0
	p.exhaustion = 5.0 // > 4.0 threshold

	// First drain: exhaustion 5 -> 1; saturation 2 -> 1 (saturation > 0 branch).
	loop.foodDataTick(p)
	if p.exhaustion != 1.0 {
		t.Fatalf("exhaustion after drain = %v, want 1.0 (5 - 4)", p.exhaustion)
	}
	if p.saturation != 1.0 {
		t.Fatalf("saturation after drain = %v, want 1.0 (2 - 1)", p.saturation)
	}
	if p.food != maxFood {
		t.Fatalf("food after saturated drain = %d, want %d (food untouched while sat > 0)", p.food, maxFood)
	}

	// Drain saturation to exactly 0 over the next drains, then food drops.
	p.exhaustion = 5.0
	loop.foodDataTick(p) // sat 1 -> 0
	if p.saturation != 0.0 {
		t.Fatalf("saturation = %v, want 0.0", p.saturation)
	}
	if p.food != maxFood {
		t.Fatalf("food = %d, want %d (still untouched: this drain emptied saturation)", p.food, maxFood)
	}

	// Now saturation is 0: the next drain hits food (NORMAL != PEACEFUL).
	p.exhaustion = 5.0
	loop.foodDataTick(p)
	if p.food != maxFood-1 {
		t.Fatalf("food after empty-saturation drain = %d, want %d (Math.max(food-1, 0))", p.food, maxFood-1)
	}
}

// TestSaturatedRegenEvery10Ticks asserts the FAST saturated-regen branch: saturation > 0, player
// hurt, food >= 20 -> heal saturation/6 every 10 ticks (capped at 6), exhaust by the heal amount.
func TestSaturatedRegenEvery10Ticks(t *testing.T) {
	loop := &TickLoop{}
	p := foodPlayer()
	p.food = maxFood        // 20, satisfies food >= 20
	p.saturation = 5.0      // > 0
	p.health = maxHealth - 3 // hurt (below max)
	p.exhaustion = 0.0       // no drain interference

	// 9 ticks: timer climbs 1..9, no heal yet (< 10).
	for i := 0; i < 9; i++ {
		loop.foodDataTick(p)
	}
	if p.foodTickTimer != 9 {
		t.Fatalf("foodTickTimer after 9 ticks = %d, want 9 (no heal before 10)", p.foodTickTimer)
	}
	if p.health != maxHealth-3 {
		t.Fatalf("health before 10th tick = %v, want %v (no heal yet)", p.health, maxHealth-3)
	}

	// 10th tick: heal min(saturation,6)/6 = 5/6, addExhaustion(5), reset timer.
	loop.foodDataTick(p)
	if p.foodTickTimer != 0 {
		t.Fatalf("foodTickTimer after heal = %d, want 0 (reset on fire)", p.foodTickTimer)
	}
	wantHealth := float32(maxHealth - 3 + 5.0/6.0)
	if !floatNear(float64(p.health), float64(wantHealth), 1e-5) {
		t.Fatalf("health after fast regen = %v, want %v (heal min(sat,6)/6)", p.health, wantHealth)
	}
}

// TestStarvationEvery80Ticks asserts the STARVATION branch: food <= 0, every 80 ticks deals 1.0
// starve damage when (NORMAL && health > 1.0). Damage routes through applyDamage (the hurtServer
// port), so the i-frame window is respected — a fresh player takes the hit.
func TestStarvationEvery80Ticks(t *testing.T) {
	loop := &TickLoop{}
	p := foodPlayer()
	p.food = 0          // food <= 0 -> starvation branch
	p.saturation = 0.0  // no regen
	p.health = maxHealth // well above the 1.0 NORMAL floor
	p.exhaustion = 0.0

	// 79 ticks: timer climbs, no damage yet.
	for i := 0; i < 79; i++ {
		loop.foodDataTick(p)
	}
	if p.foodTickTimer != 79 {
		t.Fatalf("foodTickTimer after 79 ticks = %d, want 79 (no starve before 80)", p.foodTickTimer)
	}
	if p.health != maxHealth {
		t.Fatalf("health before 80th tick = %v, want %v (no starve yet)", p.health, float32(maxHealth))
	}

	// 80th tick: 1.0 starve damage, timer reset.
	loop.foodDataTick(p)
	if p.foodTickTimer != 0 {
		t.Fatalf("foodTickTimer after starve = %d, want 0 (reset on fire)", p.foodTickTimer)
	}
	if !floatNear(float64(p.health), float64(maxHealth-starveDamage), 1e-6) {
		t.Fatalf("health after starvation = %v, want %v (1.0 starve damage)", p.health, maxHealth-starveDamage)
	}
}

// TestStarvationStopsAt1HPonNormal asserts the NORMAL-difficulty starvation floor: at health == 1.0
// (NOT > 1.0) and food 0, the 80-tick fire does NOT damage (the gate getHealth() > 1.0f && NORMAL is
// false, and health is not > 10.0, and NORMAL != HARD).
func TestStarvationStopsAt1HPonNormal(t *testing.T) {
	loop := &TickLoop{}
	p := foodPlayer()
	p.food = 0
	p.saturation = 0.0
	p.health = 1.0 // exactly the floor: getHealth() > 1.0f is FALSE
	p.exhaustion = 0.0

	for i := 0; i < 80; i++ {
		loop.foodDataTick(p)
	}
	if p.health != 1.0 {
		t.Fatalf("health after 80 ticks at 1.0 HP = %v, want 1.0 (NORMAL starvation stops at 1 HP)", p.health)
	}
}

// TestFoodAddClampsSaturationToFood asserts FoodData.add clamps food to [0,20] and saturation to
// [0, foodLevel] — saturation can never exceed the current food.
func TestFoodAddClampsSaturationToFood(t *testing.T) {
	p := foodPlayer()
	p.food = 2
	p.saturation = 0
	// add(0, 10): food stays 2, saturation = clamp(0+10, 0, 2) = 2.
	p.foodAdd(0, 10)
	if p.food != 2 {
		t.Fatalf("food = %d, want 2 (add 0 food)", p.food)
	}
	if p.saturation != 2 {
		t.Fatalf("saturation = %v, want 2 (clamped to foodLevel)", p.saturation)
	}

	// add(100, 0): food = clamp(2+100, 0, 20) = 20.
	p.foodAdd(100, 0)
	if p.food != maxFood {
		t.Fatalf("food = %d, want %d (clamped to 20)", p.food, maxFood)
	}
}

// TestSaturationByModifier asserts FoodConstants.saturationByModifier: nutrition * modifier * 2.0f,
// the value foodEat feeds into add.
func TestSaturationByModifier(t *testing.T) {
	if got := saturationByModifier(4, 0.3); !floatNear(float64(got), float64(4*0.3*2.0), 1e-6) {
		t.Fatalf("saturationByModifier(4, 0.3) = %v, want %v", got, float32(4*0.3*2.0))
	}
}

// TestFoodFieldsPersistRoundTrip asserts the new FoodData fields (exhaustionLevel/tickTimer)
// round-trip through the .dat alongside food/saturation.
func TestFoodFieldsPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()

	want := save.PlayerData{
		Dimension:           overworldDimensionName,
		Health:              17.5,
		FoodLevel:           15,
		FoodSaturationLevel: 3.5,
		FoodExhaustionLevel: 2.75,
		FoodTickTimer:       42,
	}
	if err := savePlayer(dir, id, want); err != nil {
		t.Fatalf("savePlayer: %v", err)
	}
	got, ok := loadPlayer(dir, id)
	if !ok {
		t.Fatal("loadPlayer reported miss for a just-saved player")
	}
	if got.FoodExhaustionLevel != want.FoodExhaustionLevel {
		t.Fatalf("FoodExhaustionLevel = %v, want %v", got.FoodExhaustionLevel, want.FoodExhaustionLevel)
	}
	if got.FoodTickTimer != want.FoodTickTimer {
		t.Fatalf("FoodTickTimer = %v, want %v", got.FoodTickTimer, want.FoodTickTimer)
	}
}

// TestSnapshotPlayerPersistsFoodFields asserts snapshotPlayer copies the live exhaustion/tickTimer
// into the save.PlayerData (the off-tick IO source).
func TestSnapshotPlayerPersistsFoodFields(t *testing.T) {
	p := foodPlayer()
	p.exhaustion = 3.25
	p.foodTickTimer = 17
	snap := snapshotPlayer(p)
	if snap.FoodExhaustionLevel != 3.25 {
		t.Fatalf("snapshot FoodExhaustionLevel = %v, want 3.25", snap.FoodExhaustionLevel)
	}
	if snap.FoodTickTimer != 17 {
		t.Fatalf("snapshot FoodTickTimer = %v, want 17", snap.FoodTickTimer)
	}
}
