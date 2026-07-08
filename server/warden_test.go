package server

// warden_test.go -- deterministic pins for the Warden (net.minecraft.world.entity.monster.warden.Warden
// + AngerManagement + SonicBoom, 1:1 javap this task). Verifies: (a) the spawn attributes (MAX_HEALTH 500
// / MOVEMENT_SPEED 0.3 / KNOCKBACK_RESISTANCE 1.0 / ATTACK_KNOCKBACK 1.5 / ATTACK_DAMAGE 30 / FOLLOW_RANGE
// 24) + the emerge lock; (b) the anger thresholds -> AngerLevel byAnger (0/40/80) + increaseAngerAt +35;
// (c) the melee doHurtTarget deals ATTACK_DAMAGE 30 + arms the 40-tick sonic lock; (d) the SonicBoom range
// gate (15 XZ / 20 Y) + the 10.0 damage; (e) the dig-away despawn after the no-anger idle window.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// wardenLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick the warden.
func wardenLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestWardenSpawnDefaults: spawnWarden builds a Warden rendering as entity.Warden.ID with the jar
// attributes (MAX_HEALTH 500 etc), full health 500, and the 134-tick EMERGE lock armed.
func TestWardenSpawnDefaults(t *testing.T) {
	loop, floorY := wardenLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, true)
	if w.typ != entity.Warden.ID {
		t.Fatalf("warden typ = %d, want entity.Warden.ID %d", w.typ, entity.Warden.ID)
	}
	if w.warden == nil {
		t.Fatal("warden has no wardenState (e.warden nil)")
	}
	if got := w.getAttributeValue(attribute.MaxHealth); math.Abs(got-500.0) > 1e-9 {
		t.Fatalf("warden MAX_HEALTH = %v, want 500.0", got)
	}
	if got := w.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.30000001192092896) > 1e-15 {
		t.Fatalf("warden MOVEMENT_SPEED = %v, want 0.30000001192092896", got)
	}
	if got := w.getAttributeValue(attribute.KnockbackResistance); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("warden KNOCKBACK_RESISTANCE = %v, want 1.0", got)
	}
	if got := w.getAttributeValue(attribute.AttackKnockback); math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("warden ATTACK_KNOCKBACK = %v, want 1.5", got)
	}
	if got := w.getAttributeValue(attribute.AttackDamage); math.Abs(got-30.0) > 1e-9 {
		t.Fatalf("warden ATTACK_DAMAGE = %v, want 30.0", got)
	}
	if got := w.getAttributeValue(attribute.FollowRange); math.Abs(got-24.0) > 1e-9 {
		t.Fatalf("warden FOLLOW_RANGE = %v, want 24.0", got)
	}
	if math.Abs(float64(w.health)-500.0) > 1e-4 {
		t.Fatalf("warden health = %v, want 500.0", w.health)
	}
	if w.warden.emergeTicks != wardenEmergeDuration {
		t.Fatalf("warden emergeTicks = %d, want %d (EMERGE_DURATION)", w.warden.emergeTicks, wardenEmergeDuration)
	}
}

// TestWardenAngerLevelThresholds: AngerLevel.byAnger -- CALM (0) below 40, AGITATED (1) at [40,80), ANGRY
// (2) at >= 80. Also increaseAngerAt adds DEFAULT_ANGER 35 per call (so 3 calls -> 105 -> ANGRY).
func TestWardenAngerLevelThresholds(t *testing.T) {
	cases := []struct {
		anger int
		want  int
	}{
		{0, 0}, {34, 0}, {39, 0}, {40, 1}, {79, 1}, {80, 2}, {105, 2}, {150, 2},
	}
	for _, c := range cases {
		if got := wardenAngerLevel(c.anger); got != c.want {
			t.Errorf("wardenAngerLevel(%d) = %d, want %d", c.anger, got, c.want)
		}
	}

	loop, floorY := wardenLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	const pid int32 = 5001
	// Three +35 increments -> 105 anger -> ANGRY (>= 80).
	loop.wardenIncreaseAngerAt(w, pid, wardenDefaultAnger)
	loop.wardenIncreaseAngerAt(w, pid, wardenDefaultAnger)
	loop.wardenIncreaseAngerAt(w, pid, wardenDefaultAnger)
	if got := w.warden.angerBySuspect[pid]; got != 105 {
		t.Fatalf("anger after 3x +35 = %d, want 105", got)
	}
	if lvl := wardenAngerLevel(w.warden.angerBySuspect[pid]); lvl != 2 {
		t.Fatalf("anger level at 105 = %d, want 2 (ANGRY)", lvl)
	}
}

// TestWardenMeleeDealsThirty: with an ANGRY suspect in melee reach, wardenDoHurtTarget deals ATTACK_DAMAGE
// 30 through the player hurt path and arms the 40-tick sonic melee lock (SonicBoom.setCooldown(this, 40)).
func TestWardenMeleeDealsThirty(t *testing.T) {
	loop, floorY := wardenLoop(t)
	py := float64(floorY + 1)
	p := combatTestPlayer(loop, 8.5, py, 8.5, 6001)
	p.health = 500.0 // large HP so the 30 lands without dying (isolate the damage number)
	start := p.health
	w := loop.spawnWarden(8.5, py, 8.5, false) // co-located -> in melee reach
	w.ai.attackTargetID = p.entityID

	loop.wardenDoHurtTarget(w, p)
	dealt := start - p.health
	if math.Abs(float64(dealt)-30.0) > 1e-4 {
		t.Fatalf("warden melee dealt %v, want 30.0 (ATTACK_DAMAGE)", dealt)
	}
	if w.warden.sonicCooldown != wardenMeleeToSonicLock {
		t.Fatalf("sonicCooldown after melee = %d, want %d (TIME_TO_USE_MELEE_UNTIL_SONIC_BOOM)", w.warden.sonicCooldown, wardenMeleeToSonicLock)
	}
}

// TestWardenSonicBoomReachAndDamage: the SonicBoom range gate is closerThan(15 XZ, 20 Y); a target inside
// fires + deals 10.0, a target outside (16 blocks XZ) does not. Drive wardenFireSonicBoom directly (the
// windup is orthogonal) for the damage; wardenCloserThan for the gate.
func TestWardenSonicBoomReachAndDamage(t *testing.T) {
	loop, floorY := wardenLoop(t)
	py := float64(floorY + 1)
	w := loop.spawnWarden(8.5, py, 8.5, false)

	// Gate: inside 15 XZ / 20 Y -> true; outside 15 XZ -> false.
	near := &tickPlayer{x: 8.5 + 10.0, y: py, z: 8.5, entityID: 7101}
	far := &tickPlayer{x: 8.5 + 16.0, y: py, z: 8.5, entityID: 7102}
	if !wardenCloserThan(w, near, wardenSonicDistanceXZ, wardenSonicDistanceY) {
		t.Fatalf("target 10 blocks away should be within sonic range (15 XZ)")
	}
	if wardenCloserThan(w, far, wardenSonicDistanceXZ, wardenSonicDistanceY) {
		t.Fatalf("target 16 blocks away should be OUTSIDE sonic range (15 XZ)")
	}
	// Damage: fire at an in-range target -> 10.0 lands.
	p := combatTestPlayer(loop, 8.5+10.0, py, 8.5, 7103)
	p.health = 500.0
	start := p.health
	loop.wardenFireSonicBoom(w, p)
	dealt := start - p.health
	if math.Abs(float64(dealt)-10.0) > 1e-4 {
		t.Fatalf("sonic boom dealt %v, want 10.0", dealt)
	}
}

// TestWardenDigsAwayAfterNoAnger: a warden with no anger (past the emerge lock) counts up noAngerTicks and,
// at wardenNoAngerDespawnTicks, starts digging; the dig countdown then discards it. Drive the timers directly.
func TestWardenDigsAwayAfterNoAnger(t *testing.T) {
	loop, floorY := wardenLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false) // no emerge lock
	// Push the no-anger counter to the threshold, then one aiStep starts the dig.
	w.warden.noAngerTicks = wardenNoAngerDespawnTicks - 1
	loop.wardenAiStep(w)
	if !w.warden.digging {
		t.Fatalf("warden did not start digging after the no-anger idle window")
	}
	// Run the dig to completion -> the warden is discarded (dead + removed).
	for i := 0; i < wardenDiggingDuration+2 && !w.dead; i++ {
		loop.wardenAiStep(w)
	}
	if !w.dead {
		t.Fatalf("warden did not despawn after the dig-away completed")
	}
}

// TestWardenDarknessPulse: wardenApplyDarknessAround (Warden.applyDarknessAround -> MobEffectUtil
// .addEffectToPlayersAround) gives DARKNESS 260/0 to a SURVIVAL player within 20 blocks (3D) of the
// warden's feet, and NOT to a player 21 blocks away. The re-application gate leaves a still-long pulse
// untouched (>199 ticks remaining) but refreshes one about to end (<=199). Verifies id/duration/amplifier
// and the stored render flags (ambient/visible/showIcon all false). Cite Warden.applyDarknessAround.
func TestWardenDarknessPulse(t *testing.T) {
	loop, floorY := wardenLoop(t)
	py := float64(floorY + 1)
	w := loop.spawnWarden(8.5, py, 8.5, false)

	near := combatTestPlayer(loop, 8.5+10.0, py, 8.5, 7201) // within 20
	far := combatTestPlayer(loop, 8.5+21.0, py, 8.5, 7202)  // outside 20

	loop.wardenApplyDarknessAround(w)

	ne := near.activeEffects[effectDarkness]
	if ne == nil {
		t.Fatalf("player within 20 did not receive DARKNESS")
	}
	if ne.duration != wardenDarknessDuration || ne.amplifier != wardenDarknessAmplifier {
		t.Fatalf("DARKNESS = dur %d amp %d, want dur %d amp %d", ne.duration, ne.amplifier, wardenDarknessDuration, wardenDarknessAmplifier)
	}
	if ne.ambient || ne.visible || ne.showIcon {
		t.Fatalf("DARKNESS render flags = ambient %v visible %v showIcon %v, want all false", ne.ambient, ne.visible, ne.showIcon)
	}
	if far.activeEffects[effectDarkness] != nil {
		t.Fatalf("player 21 blocks away should NOT receive DARKNESS")
	}

	// Re-application gate: a still-long pulse (>199 ticks) is NOT reset by a second call.
	near.activeEffects[effectDarkness].duration = 250 // >199 remaining, amp >= new amp -> gated
	loop.wardenApplyDarknessAround(w)
	if got := near.activeEffects[effectDarkness].duration; got != 250 {
		t.Fatalf("still-long DARKNESS was reset to %d, want left at 250 (reapply gate)", got)
	}
	// A pulse about to end (<=199) IS refreshed back to 260.
	near.activeEffects[effectDarkness].duration = 100 // <=199 -> endsWithin(199) true -> re-apply
	loop.wardenApplyDarknessAround(w)
	if got := near.activeEffects[effectDarkness].duration; got != wardenDarknessDuration {
		t.Fatalf("about-to-end DARKNESS refreshed to %d, want %d", got, wardenDarknessDuration)
	}

	// A CREATIVE player within range is skipped (lambda$0 isSurvival gate).
	creative := combatTestPlayer(loop, 8.5+5.0, py, 8.5, 7203)
	creative.gameMode = gameModeCreative
	loop.wardenApplyDarknessAround(w)
	if creative.activeEffects[effectDarkness] != nil {
		t.Fatalf("creative player should NOT receive DARKNESS (isSurvival gate)")
	}
}

// TestWardenSonicLockOnTargetAcquire pins the WardEN-03 fix: Warden.setAttackTarget -> SonicBoom
// .setCooldown(this, 200). When the warden ACQUIRES a target (wardenSelectTarget transitions to a new
// suspect) sonicCooldown is armed to 200 (TIME_TO_USE_MELEE_UNTIL_SONIC_BOOM) so it must melee before
// it may boom.
func TestWardenSonicLockOnTargetAcquire(t *testing.T) {
	loop, floorY := wardenLoop(t)
	py := float64(floorY + 1)
	w := loop.spawnWarden(8.5, py, 8.5, false)

	// A live player suspect with enough anger to be selected as the top suspect.
	p := combatTestPlayer(loop, 8.5, py, 8.5, 8801)
	loop.wardenIncreaseAngerAt(w, p.entityID, wardenDefaultAnger)

	if w.warden.sonicCooldown != 0 {
		t.Fatalf("sonicCooldown before acquisition = %d, want 0", w.warden.sonicCooldown)
	}
	got := loop.wardenSelectTarget(w)
	if got == nil || got.entityID != p.entityID {
		t.Fatalf("wardenSelectTarget did not pick the anger suspect")
	}
	if w.warden.sonicCooldown != wardenSonicOnAcquire {
		t.Fatalf("sonicCooldown after acquisition = %d, want %d (SonicBoom.setCooldown(this, 200))", w.warden.sonicCooldown, wardenSonicOnAcquire)
	}
	// Re-selecting the SAME target does NOT re-arm the 200 lock (only a NEW target acquisition does).
	w.warden.sonicCooldown = 5
	loop.wardenSelectTarget(w)
	if w.warden.sonicCooldown != 5 {
		t.Fatalf("sonicCooldown re-armed on same-target reselect = %d, want 5 (unchanged)", w.warden.sonicCooldown)
	}
}

// TestWardenMeleeCooldown pins the WardEN-07 fix: WardenAi initFightActivity MeleeAttack.create(18) ->
// MELEE_ATTACK_COOLDOWN 18. wardenDoHurtTarget does not set the melee cooldown itself (the melee gate
// does); the constant is 18 and the countdown decrements one per tick in wardenAiStep.
func TestWardenMeleeCooldown(t *testing.T) {
	if wardenMeleeCooldown != 18 {
		t.Fatalf("wardenMeleeCooldown = %d, want 18 (MeleeAttack.create(18))", wardenMeleeCooldown)
	}
	loop, floorY := wardenLoop(t)
	py := float64(floorY + 1)
	w := loop.spawnWarden(8.5, py, 8.5, false)
	// Arm the melee cooldown and confirm it decrements exactly one per aiStep tick (not to melee twice
	// within 18 ticks). Give the warden no target so the aiStep just runs the countdown.
	w.warden.meleeCooldown = wardenMeleeCooldown
	loop.withRegion(loop.regionForEntity(w), func() {
		loop.wardenAiStep(w)
	})
	if w.warden.meleeCooldown != wardenMeleeCooldown-1 {
		t.Fatalf("meleeCooldown after one aiStep = %d, want %d (decrement by 1)", w.warden.meleeCooldown, wardenMeleeCooldown-1)
	}
}
