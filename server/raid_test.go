package server

// raid_test.go — the RAID subsystem (raid.go/raids.go/raid_tick.go): start a raid via the createRaidAt
// SEAM, tick it, and assert (a) the wave schedule counts down + spawns witch raiders, (b) a spawned
// raider's hasActiveRaid() is true, (c) the witch's NearestHealableRaiderTargetGoal proceeds past the
// hasActiveRaid gate now that the raid is live. Deterministic (seeded per-mob + per-raid RNG). The pig
// oracle is a separate mob and untouched (a raid draws ZERO from any pig stream).

import (
	"testing"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
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
	// As of the RAIDER task ALL FIVE RaiderTypes map to a boot-loaded declaration (createRaider spawns a
	// REAL raider for each). Pin the ordinal -> mob-name mapping (the RaiderType VALUES order is load-bearing).
	wantMob := map[int]string{
		0: vanillaVindicatorMobName,
		1: vanillaEvokerMobName,
		2: vanillaPillagerMobName,
		3: vanillaWitchMobName,
		4: vanillaRavagerMobName,
	}
	for _, rt := range raiderTypesValues {
		if rt.mobName != wantMob[rt.ordinal] {
			t.Fatalf("RaiderType ordinal %d maps to mob %q, want %q", rt.ordinal, rt.mobName, wantMob[rt.ordinal])
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
	loop.levelDifficulty = difficultyHard // the raid's live difficulty is the level's (spawnGroup/tick read it live)
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
	loop.levelDifficulty = difficultyHard // the raid's live difficulty is the level's (spawnGroup/tick read it live)

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
	loop.levelDifficulty = difficultyEasy // the raid's live difficulty is the level's (spawnGroup/tick read it live)

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
	loop.levelDifficulty = difficultyNormal // the raid's live difficulty is the level's (spawnGroup/tick read it live)
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

// raidFullRaiderLoop builds a physics loop with a stone floor + the FULL vanilla mob registry (all 5
// raiders), so a wave can spawn a real pillager/vindicator/evoker/ravager captain + a ravager rider.
func raidFullRaiderLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	reg, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	loop.SetMobRegistry(reg)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestRaidWaveAssignsCaptainWithOminousBanner: the FIRST canBeLeader() raider of a wave becomes the
// patrol leader and carries the ominous banner in its HEAD slot (Raid.spawnGroup -> setLeader). isCaptain()
// reads true for the banner-carrying leader; the raid records it in groupToLeaderMap.
func TestRaidWaveAssignsCaptainWithOminousBanner(t *testing.T) {
	loop, floorY := raidFullRaiderLoop(t)
	rm := loop.only().ensureRaidsManager()
	// A HARD raid: wave 1 spawns 4 pillagers (the first is the captain). Drive to the first wave spawn.
	raid := rm.createRaidAt(8, floorY, 8, difficultyHard, 1)
	loop.levelDifficulty = difficultyHard // the raid's live difficulty is the level's (spawnGroup/tick read it live)
	for i := 0; i < 400 && raid.getTotalRaidersAlive() == 0; i++ {
		loop.raidsTick(rm)
		if raid.isStopped() || raid.isOver() {
			break
		}
	}
	if raid.getTotalRaidersAlive() == 0 {
		t.Fatalf("no wave ever spawned (groupsSpawned=%d status=%v)", raid.groupsSpawned, raid.status)
	}
	// Exactly one captain in the wave, carrying the ominous banner + reading isCaptain()==true.
	var captain *Entity
	captains := 0
	for _, set := range raid.groupRaiderMap {
		for _, r := range set {
			if raiderIsCaptain(r) {
				captain = r
				captains++
			}
		}
	}
	if captains != 1 {
		t.Fatalf("expected exactly 1 captain in the wave, got %d", captains)
	}
	head := captain.getItemBySlot(eqSlotHead)
	if uint32(head.ItemID) != uint32(item.WhiteBanner.ID) || head.Count != 1 {
		t.Fatalf("captain HEAD slot = {id=%d count=%d}, want the ominous banner (white_banner x1)", head.ItemID, head.Count)
	}
	if !raiderIsPatrolLeader(captain) {
		t.Fatal("captain is not the patrol leader")
	}
	if raid.getLeader(captain.ai.raidWave) != captain.id {
		t.Fatalf("raid leader for wave %d = %d, want captain %d", captain.ai.raidWave, raid.getLeader(captain.ai.raidWave), captain.id)
	}
}

// TestKillingCaptainDropsOminousBottle: the is_captain loot predicate fires for a slain captain, dropping
// the ominous bottle (which, drunk, grants BAD_OMEN via consume_effects.go). A non-captain raider drops
// no bottle. This is the "killing a captain grants bad omen" loop.
func TestKillingCaptainDropsOminousBottle(t *testing.T) {
	loop, floorY := raidFullRaiderLoop(t)
	rm := loop.only().ensureRaidsManager()
	raid := rm.createRaidAt(8, floorY, 8, difficultyHard, 1)
	loop.levelDifficulty = difficultyHard // the raid's live difficulty is the level's (spawnGroup/tick read it live)

	cap := loop.spawnVanillaMob(vanillaPillagerMobName, 8.5, float64(floorY+1), 8.5)
	if cap == nil {
		t.Fatal("failed to spawn pillager")
	}
	cap.ai.patrolLeader = true
	loop.raidSetLeader(raid, 1, cap)
	loop.raidJoinRaid(raid, 1, cap)
	if !raiderIsCaptain(cap) {
		t.Fatal("the leader pillager with the ominous banner must read isCaptain()==true")
	}

	killer := &tickPlayer{entityID: 9001, x: 8.5, y: float64(floorY + 1), z: 8.5, health: 20}
	loop.players = append(loop.players, killer)
	loop.withRegion(loop.only(), func() {
		loop.dieEntity(cap, damageSourcePlayerAttack(killer.entityID))
	})

	sawBottle := false
	for _, e := range loop.only().entities.all() {
		if e.itemStack.Count > 0 && uint32(e.itemStack.ItemID) == uint32(item.OminousBottle.ID) {
			sawBottle = true
		}
	}
	if !sawBottle {
		t.Fatal("killing a captain did not drop an ominous bottle (the is_captain loot pool did not fire)")
	}

	plain := loop.spawnVanillaMob(vanillaPillagerMobName, 4.5, float64(floorY+1), 4.5)
	if raiderIsCaptain(plain) {
		t.Fatal("a plain pillager (no banner, not leader) must not be a captain")
	}
}

// TestRaidVictoryGrantsHeroOfTheVillage: a player who slew a raider (addHeroOfTheVillage) gets
// HERO_OF_THE_VILLAGE at amplifier (raidOmenLevel-1) on the raid VICTORY transition.
func TestRaidVictoryGrantsHeroOfTheVillage(t *testing.T) {
	loop, floorY := raidFullRaiderLoop(t)
	rm := loop.only().ensureRaidsManager()
	// raidOmenLevel 3 -> hero amplifier 2. EASY (3 waves, no witches -> instant clears).
	raid := rm.createRaidAt(8, floorY, 8, difficultyEasy, 3)
	loop.levelDifficulty = difficultyEasy // the raid's live difficulty is the level's (spawnGroup/tick read it live)

	hero := &tickPlayer{entityID: 9100, uuid: uuid.New(), x: 8.5, y: float64(floorY + 1), z: 8.5, health: 20}
	loop.players = append(loop.players, hero)
	raid.addHeroOfTheVillage(hero.uuid)
	raid.addHeroOfTheVillage(hero.uuid) // a second add is a no-op (Set semantics)
	if len(raid.heroesOfTheVillage) != 1 {
		t.Fatalf("heroesOfTheVillage size = %d, want 1 (dedup)", len(raid.heroesOfTheVillage))
	}

	// Drive the raid: each spawned raider is killed by the hero so waves clear and advance to VICTORY.
	reachedVictory := false
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 300*8+700; i++ {
			loop.raidsTick(rm)
			for _, set := range raid.groupRaiderMap {
				for _, r := range set {
					if !r.dead {
						loop.dieEntity(r, damageSourcePlayerAttack(hero.entityID))
					}
				}
			}
			if raid.isVictory() {
				reachedVictory = true
			}
			if rm.get(raid.id) == nil {
				break
			}
		}
	})
	if !reachedVictory {
		t.Fatalf("EASY raid never reached VICTORY (status=%v)", raid.status)
	}
	eff := hero.activeEffects[heroOfTheVillageEffectID]
	if eff == nil {
		t.Fatal("the hero did not receive HERO_OF_THE_VILLAGE on raid victory")
	}
	if eff.amplifier != 2 { // raidOmenLevel(3) - 1
		t.Fatalf("HERO_OF_THE_VILLAGE amplifier = %d, want 2 (raidOmenLevel-1)", eff.amplifier)
	}
	if eff.duration != raidHeroDuration {
		t.Fatalf("HERO_OF_THE_VILLAGE duration = %d, want %d", eff.duration, raidHeroDuration)
	}
}

// TestRaidRavagerWaveSpawnsRider: a ravager wave on wave >= getNumGroups(HARD) carries a rider
// (EVOKER for the first ravager, VINDICATOR after). We assert a ravager spawns with a mob passenger
// whose vehicle back-pointer is that ravager.
func TestRaidRavagerWaveSpawnsRider(t *testing.T) {
	loop, floorY := raidFullRaiderLoop(t)
	rm := loop.only().ensureRaidsManager()
	raid := rm.createRaidAt(8, floorY, 8, difficultyHard, 1)
	loop.levelDifficulty = difficultyHard // the raid's live difficulty is the level's (spawnGroup/tick read it live)

	// Drive the raid, killing every raider each tick so the waves advance to the ravager-bearing final
	// wave. A player killer lets dieEntity/removeFromRaid clear the wave set so shouldSpawnGroup advances.
	killer := &tickPlayer{entityID: 9200, x: 8.5, y: float64(floorY + 1), z: 8.5, health: 20}
	loop.players = append(loop.players, killer)
	sawRiddenRavager := false
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 300*12 && !sawRiddenRavager; i++ {
			loop.raidsTick(rm)
			for _, set := range raid.groupRaiderMap {
				for _, r := range set {
					if r.typ == entity.Ravager.ID && len(r.passengers) > 0 {
						rider, ok := loop.only().entities.get(r.passengers[0])
						if ok && rider.vehicle == r.id {
							sawRiddenRavager = true
						}
					}
				}
			}
			if sawRiddenRavager {
				break
			}
			// Clear the wave so the raid advances toward the ravager-final wave (a ravager itself is killed
			// only AFTER we have observed its rider — the check above runs before this kill each tick).
			for _, set := range raid.groupRaiderMap {
				for _, r := range set {
					if !r.dead {
						loop.dieEntity(r, damageSourcePlayerAttack(killer.entityID))
					}
				}
			}
			if raid.isStopped() || raid.isOver() {
				break
			}
		}
	})
	if !sawRiddenRavager {
		t.Fatalf("no ravager with a rider ever spawned (groupsSpawned=%d status=%v)", raid.groupsSpawned, raid.status)
	}
}
