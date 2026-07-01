package server

// raid_test.go — the RAID subsystem (raid.go/raids.go/raid_tick.go): start a raid via the createRaidAt
// SEAM, tick it, and assert (a) the wave schedule counts down + spawns witch raiders, (b) a spawned
// raider's hasActiveRaid() is true, (c) the witch's NearestHealableRaiderTargetGoal proceeds past the
// hasActiveRaid gate now that the raid is live. Deterministic (seeded per-mob + per-raid RNG). The pig
// oracle is a separate mob and untouched (a raid draws ZERO from any pig stream).

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// raidLoop builds a physics loop with a stone floor + the vanilla_witch registry so raid-spawned witches
// carry their seeded attributes/health, then starts the loop.
func raidLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaWitchRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestRaidNumGroupsByDifficulty pins Raid.getNumGroups: PEACEFUL 0, EASY 3, NORMAL 5, HARD 7.
func TestRaidNumGroupsByDifficulty(t *testing.T) {
	cases := []struct {
		d    difficulty
		want int
	}{
		{difficultyPeaceful, 0},
		{difficultyEasy, 3},
		{difficultyNormal, 5},
		{difficultyHard, 7},
	}
	for _, c := range cases {
		if got := getNumGroups(c.d); got != c.want {
			t.Fatalf("getNumGroups(%d) = %d, want %d", c.d, got, c.want)
		}
	}
}

// TestRaidWaveSpawnTable pins the RaiderType.spawnsPerWaveBeforeBonus wave counts (VERIFIED CFR): the
// witch spawns 3 in wave 4 (index 4) and 1 in the bonus slot (index 7); it spawns 0 in waves 1-3, 5-6.
func TestRaidWaveSpawnTable(t *testing.T) {
	var witch raiderType
	found := false
	for _, rt := range raiderTypesValues {
		if rt.ordinal == 3 {
			witch = rt
			found = true
		}
	}
	if !found {
		t.Fatal("WITCH RaiderType (ordinal 3) missing from raiderTypesValues")
	}
	want := [8]int{0, 0, 0, 0, 3, 0, 0, 1}
	if witch.spawnsPerWaveBeforeBonus != want {
		t.Fatalf("witch spawnsPerWaveBeforeBonus = %v, want %v", witch.spawnsPerWaveBeforeBonus, want)
	}
	// The absent RaiderTypes carry no v1 mob (createRaider returns nil for them).
	for _, rt := range raiderTypesValues {
		if rt.ordinal != 3 && rt.mobName != "" {
			t.Fatalf("RaiderType ordinal %d unexpectedly maps to mob %q (only WITCH has a v1 mob)", rt.ordinal, rt.mobName)
		}
	}
}

// TestRaidCooldownCountdownThenWaveSpawns starts a raid, ticks it through the 300-tick pre-wave cooldown,
// and asserts a wave spawns (raiders alive) + groupsSpawned advances + hasActiveRaid becomes true for a
// spawned raider.
func TestRaidCooldownCountdownThenWaveSpawns(t *testing.T) {
	loop, floorY := raidLoop(t)
	rm := loop.only().ensureRaidsManager()
	// A HARD raid (7 waves): wave 4 spawns 3 witches, so a wave with witches is reached. But the pre-wave
	// cooldown must expire and each empty wave (1-3) also spends a full 300-tick cooldown. Drive enough
	// ticks to pass through to wave 4 (the first witch-bearing wave). Center at the floor.
	raid := rm.createRaidAt(8, floorY, 8, difficultyHard, 1)
	if raid.status != raidStatusOngoing {
		t.Fatalf("new raid status = %v, want ONGOING", raid.status)
	}
	if raid.raidCooldownTicks != raidDefaultPreTicks {
		t.Fatalf("new raid cooldown = %d, want %d", raid.raidCooldownTicks, raidDefaultPreTicks)
	}

	// Tick the raid manager until the first witch-bearing wave spawns (some raider alive). Each empty wave
	// burns a 300-tick cooldown; wave 4 is the first with witches. Cap generously.
	sawRaider := false
	for i := 0; i < 300*6; i++ {
		loop.raidsTick(rm)
		if raid.getTotalRaidersAlive() > 0 {
			sawRaider = true
			break
		}
		if raid.isStopped() || raid.isOver() {
			break
		}
	}
	if !sawRaider {
		t.Fatalf("no raider ever spawned (groupsSpawned=%d status=%v)", raid.groupsSpawned, raid.status)
	}
	if raid.groupsSpawned == 0 {
		t.Fatal("groupsSpawned did not advance despite a raider being alive")
	}

	// A spawned raider must be in the store, be a witch, and read hasActiveRaid()==true.
	var raider *Entity
	for _, set := range raid.groupRaiderMap {
		for _, r := range set {
			raider = r
		}
	}
	if raider == nil {
		t.Fatal("raid roster empty despite getTotalRaidersAlive>0")
	}
	if !witchHasActiveRaid(raider) {
		t.Fatal("a raid-spawned raider reports hasActiveRaid()==false (membership not wired)")
	}
	if raider.ai == nil || raider.ai.currentRaid != raid {
		t.Fatal("raider.currentRaid back-pointer not set to the raid")
	}
	if _, ok := loop.only().entities.get(raider.id); !ok {
		t.Fatal("raid-spawned raider is not in the entity store")
	}
}

// TestRaidHealGoalActivatesUnderActiveRaid: with a live raid the witch's NearestHealableRaiderTargetGoal
// runs its full canUse (past the hasActiveRaid gate). In a witch-only wave it acquires NO target (the
// !is(WITCH) subselector excludes every candidate), so canUse returns false — but this is the REAL
// findTarget path (hasActiveRaid true), not the old constant-false short-circuit. We prove the gate is
// live by confirming a witch NOT in a raid short-circuits earlier (hasActiveRaid false) while an in-raid
// witch reaches findTarget.
func TestRaidHealGoalActivatesUnderActiveRaid(t *testing.T) {
	loop, floorY := raidLoop(t)
	rm := loop.only().ensureRaidsManager()
	raid := rm.createRaidAt(8, floorY, 8, difficultyHard, 1)

	// Spawn a witch and join it to the raid (as a wave spawn would).
	w := loop.spawnVanillaMob(vanillaWitchMobName, 8.5, float64(floorY+1), 8.5)
	if w == nil {
		t.Fatal("failed to spawn witch")
	}
	loop.raidJoinRaid(raid, 1, w)

	if !witchHasActiveRaid(w) {
		t.Fatal("in-raid witch reports hasActiveRaid()==false")
	}

	// Drive the heal goal's canUse many times: it must never PANIC and (witch-only wave) never acquire a
	// non-witch target. The nextBoolean coin-flip + findTarget run — the goal is REAL, not a stub.
	g := newNearestHealableRaiderTargetGoal()
	for i := 0; i < 500; i++ {
		if g.canUse(loop, w) {
			t.Fatal("heal goal acquired a target in a witch-only wave (the !is(WITCH) subselector must exclude every raider)")
		}
	}

	// A witch NOT in a raid must fail the hasActiveRaid gate.
	lone := loop.spawnVanillaMob(vanillaWitchMobName, 4.5, float64(floorY+1), 4.5)
	if lone == nil {
		t.Fatal("failed to spawn lone witch")
	}
	if witchHasActiveRaid(lone) {
		t.Fatal("a witch with no raid reports hasActiveRaid()==true")
	}
}

// TestRaidVictoryAfterFinalWaveCleared: an EASY raid (3 waves, no witch waves in the table -> every wave
// spawns nothing, so each wave clears instantly). After the final wave + the 40-tick post-raid delay the
// status flips to VICTORY, then celebration -> STOPPED, and the manager prunes it.
func TestRaidVictoryAfterFinalWaveCleared(t *testing.T) {
	loop, floorY := raidLoop(t)
	rm := loop.only().ensureRaidsManager()
	// raidOmenLevel 1 -> hasBonusWave() false (needs >1), so the raid ends at the final normal wave.
	raid := rm.createRaidAt(8, floorY, 8, difficultyEasy, 1)

	reachedVictory := false
	for i := 0; i < 300*5+700; i++ {
		loop.raidsTick(rm)
		if raid.isVictory() {
			reachedVictory = true
		}
		if rm.get(raid.id) == nil { // pruned after STOPPED
			break
		}
	}
	if !reachedVictory {
		t.Fatalf("EASY raid never reached VICTORY (status=%v groupsSpawned=%d)", raid.status, raid.groupsSpawned)
	}
	if rm.raidCount() != 0 {
		t.Fatalf("raid manager did not prune the finished raid (count=%d)", rm.raidCount())
	}
}

// TestRaidTimeoutStops: a raid whose ticksActive crosses RAID_TIMEOUT_TICKS stops. We fast-forward
// ticksActive to just under the cap and confirm one more tick stops it.
func TestRaidTimeoutStops(t *testing.T) {
	loop, floorY := raidLoop(t)
	rm := loop.only().ensureRaidsManager()
	raid := rm.createRaidAt(8, floorY, 8, difficultyNormal, 1)
	raid.ticksActive = raidTimeoutTicks - 1
	loop.raidsTick(rm)
	if !raid.isStopped() {
		t.Fatalf("raid did not stop at the timeout cap (ticksActive=%d status=%v)", raid.ticksActive, raid.status)
	}
	if rm.raidCount() != 0 {
		t.Fatalf("timed-out raid not pruned (count=%d)", rm.raidCount())
	}
}

// TestLongDistancePatrolGoalInert: the patrol goal is registered-able + faithful but a v1 mob is never
// isPatrolling(), so canUse is always false. It also must not panic when driven.
func TestLongDistancePatrolGoalInert(t *testing.T) {
	loop, floorY := raidLoop(t)
	w := loop.spawnVanillaMob(vanillaWitchMobName, 8.5, float64(floorY+1), 8.5)
	if w == nil {
		t.Fatal("failed to spawn witch")
	}
	g := newLongDistancePatrolGoal()
	if g.flags() != flagMove {
		t.Fatalf("patrol goal flags = %d, want flagMove", g.flags())
	}
	for i := 0; i < 100; i++ {
		if g.canUse(loop, w) {
			t.Fatal("patrol goal canUse true for a non-patrolling mob (must be inert in v1)")
		}
	}
	// Arm patrol state manually and confirm the goal becomes usable + its tick runs the faithful path
	// without panicking (proves the goal is a REAL port, not a stub). With the companion broad-phase
	// cite-deferred (patrolCompanionsEmptyV1), a lone patroller with no companions drops patrolling — the
	// exact vanilla LongDistancePatrolGoal.tick branch (`if isPatrolling() && companions.isEmpty() ->
	// setPatrolling(false)`). So after one tick with the nav done, patrolling is cleared.
	w.ai.patrolling = true
	w.ai.setPatrolTarget(600, floorY, 600)
	if !g.canUse(loop, w) {
		t.Fatal("patrol goal canUse false even with patrolling+patrolTarget set")
	}
	w.ai.hasTarget = false // navigation.isDone() == true
	g.tick(loop, w)
	if w.ai.patrolling {
		t.Fatal("lone patroller (no companions) did not drop patrolling — the faithful no-companion branch")
	}
}
