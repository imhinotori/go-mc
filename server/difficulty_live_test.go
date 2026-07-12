package server

// difficulty_live_test.go — pins the P0-02 audit fix: the RUNTIME difficulty is the LIVE
// ServerLevel.getDifficulty() read (t.levelDifficulty, settable via /difficulty), NOT the frozen
// serverDifficulty NORMAL const nor a create-time snapshot. These tests exercise the read at the
// vanilla read points that matter: the hunger tick (FoodData.tick), the zombie reinforcement gate
// (Zombie.hurtServer), the spawn-equipment partial-chance roll (Mob.populateDefaultEquipmentSlots),
// the raid bonus-spawn count (Raid.spawnGroup -> getPotentialBonusSpawns, the VERIFIER GAP), and the
// /difficulty command mutation flowing into a hot path.
//
// The pig oracle stays UNTOUCHED: a pig is an Animal, never reaching any of these hostile/difficulty
// paths; and at the NORMAL default (serverDifficulty == levelDifficulty init) every read computes the
// identical value the const did, so the byte-identical spawn stream is preserved.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// TestHungerTickReadsLiveDifficulty: FoodData.tick's exhaustion-drain food-loss branch fires when
// difficulty != PEACEFUL and skips it on PEACEFUL. With saturation 0 and exhaustion > 4.0 the food
// level drops by 1 on NORMAL/HARD; on PEACEFUL it stays. This proves foodDataTick reads the LIVE
// t.levelDifficulty (the diff local), not a frozen const. Cite FoodData.tick (the diff != PEACEFUL
// food-drain branch).
func TestHungerTickReadsLiveDifficulty(t *testing.T) {
	cases := []struct {
		diff     difficulty
		wantDrop bool
	}{
		{difficultyPeaceful, false}, // PEACEFUL skips the food drain
		{difficultyEasy, true},
		{difficultyNormal, true},
		{difficultyHard, true},
	}
	for _, c := range cases {
		loop, _ := newPhysicsLoop()
		loop.levelDifficulty = c.diff
		p := &tickPlayer{
			food:       10,
			saturation: 0.0, // saturationLevel == 0 so the drain hits foodLevel
			exhaustion: 5.0, // > 4.0f -> the exhaustion-drain block runs
			health:     20.0,
		}
		loop.foodDataTick(p)
		dropped := p.food < 10
		if dropped != c.wantDrop {
			t.Fatalf("difficulty %d: food drained=%v (food=%d), want drained=%v", c.diff, dropped, p.food, c.wantDrop)
		}
	}
}

// TestZombieReinforcementReadsLiveDifficulty: the zombieHurtReinforcements HARD gate reads the LIVE
// t.levelDifficulty. On NORMAL it short-circuits (no spawn); on HARD (set live, mid-run) it arms the
// reinforcement and a second zombie spawns. This is the load-bearing proof that the gate is LIVE, not
// the frozen NORMAL const (which would make the HARD branch permanently dead). Cite Zombie.hurtServer
// (level.getDifficulty() == HARD gate).
func TestZombieReinforcementReadsLiveDifficulty(t *testing.T) {
	drive := func(diff difficulty) (before, after int) {
		loop, floorY, _ := zombieLoop(t)
		loop.levelDifficulty = diff
		parent := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
		parent.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name()).SetBaseValue(1.0)
		parent.ai.attackTargetID = 4242 // a target so only the difficulty gate can stop it
		before = countZombies(loop)
		loop.withRegion(loop.only(), func() {
			loop.zombieHurtReinforcements(parent, damageSource{attacker: 4242})
		})
		after = countZombies(loop)
		return
	}

	// NORMAL: the HARD gate short-circuits -> no reinforcement.
	if b, a := drive(difficultyNormal); a != b {
		t.Fatalf("NORMAL: reinforcement spawned (count %d -> %d); the LIVE HARD gate must short-circuit", b, a)
	}
	// HARD (set live): the gate opens -> exactly one reinforcement.
	if b, a := drive(difficultyHard); a != b+1 {
		t.Fatalf("HARD: reinforcement count %d -> %d, want +1 (the LIVE HARD gate must arm the spawn)", b, a)
	}
}

// TestRaidBonusSpawnCountReadsLiveDifficulty is the VERIFIER GAP fix: Raid.spawnGroup @17 calls
// ServerLevel.getCurrentDifficultyAt(pos) LIVE at spawn time (reduced to t.levelDifficulty here, since
// inhabited-time/moon are untracked) and passes it to getPotentialBonusSpawns, which branches on
// getDifficulty()==EASY/NORMAL. So a /difficulty change mid-raid changes the NEXT wave's bonus count —
// the count must follow the LIVE level, NOT the create-time r.difficulty snapshot.
//
// Setup: both raids are CREATED with the EASY snapshot (createRaidAt EASY). Wave 3 (groupNumber 3) has a
// WITCH default of 0 and a bonus of +1 witch when !isEasy (getPotentialBonusSpawns WITCH branch: wav>2
// && wav!=4). If spawnGroup used the frozen r.difficulty=EASY snapshot, BOTH raids would spawn 0 witches.
// Because it reads LIVE, the raid whose level was flipped to NORMAL spawns 1 witch and the one left EASY
// spawns 0 — proving the live read. Cite Raid.spawnGroup @17 + getPotentialBonusSpawns @0-35.
func TestRaidBonusSpawnCountReadsLiveDifficulty(t *testing.T) {
	witchWave := func(liveDiff difficulty) int {
		loop, floorY := raidFullRaiderLoop(t)
		rm := loop.only().ensureRaidsManager()
		// Create with the EASY snapshot; then set the LIVE level difficulty to liveDiff (the /difficulty
		// mid-raid change the verifier gap is about).
		raid := rm.createRaidAt(8, floorY, 8, difficultyEasy, 1)
		loop.levelDifficulty = liveDiff
		raid.groupsSpawned = 2 // next spawnGroup is groupNumber 3 (wav > 2, wav != 4 -> witch bonus branch)
		loop.withRegion(loop.only(), func() {
			loop.raidSpawnGroup(raid)
		})
		witches := 0
		for _, set := range raid.groupRaiderMap {
			for _, r := range set {
				if r.typ == entity.Witch.ID && !r.dead {
					witches++
				}
			}
		}
		return witches
	}

	// LIVE EASY: the witch bonus branch requires !isEasy, so wave 3 has 0 witches (default 0 + bonus 0).
	if got := witchWave(difficultyEasy); got != 0 {
		t.Fatalf("live EASY wave 3 witch count = %d, want 0 (the !isEasy bonus branch must not fire)", got)
	}
	// LIVE NORMAL (created EASY, flipped mid-raid): the +1 witch bonus fires -> 1 witch. The frozen
	// snapshot (EASY) would have given 0, so a passing NORMAL count proves the LIVE read.
	if got := witchWave(difficultyNormal); got != 1 {
		t.Fatalf("live NORMAL wave 3 witch count = %d, want 1 (spawnGroup must read the LIVE difficulty, "+
			"not the create-time EASY snapshot)", got)
	}
}

// TestGetPotentialBonusSpawnsBranchesOnLiveDifficulty pins the pure getPotentialBonusSpawns difficulty
// branch (the function Raid.spawnGroup feeds the live DifficultyInstance.getDifficulty() into). For a
// VINDICATOR (ordinal 0) the pre-draw bonus is NORMAL -> 1, HARD -> 2, and the returned count is
// random.nextInt(bonus+1): NORMAL yields 0..1, HARD yields 0..2. So a returned 2 is REACHABLE only on
// HARD and IMPOSSIBLE on NORMAL — a crisp, seed-robust proof that the difficulty argument (now the LIVE
// read) selects the branch. We sweep the raid RNG seed and assert: NORMAL never exceeds 1, HARD does
// reach 2. Cite getPotentialBonusSpawns VINDICATOR/PILLAGER branch (isNormal -> 1; else -> 2) +
// the `random.nextInt(bonusSpawns + 1)` tail (VERIFIED javap @155-165).
func TestGetPotentialBonusSpawnsBranchesOnLiveDifficulty(t *testing.T) {
	var vindicator raiderType
	found := false
	for _, rt := range raiderTypesValues {
		if rt.ordinal == 0 { // VINDICATOR
			vindicator = rt
			found = true
		}
	}
	if !found {
		t.Fatal("VINDICATOR RaiderType (ordinal 0) missing from raiderTypesValues")
	}
	hardSaw2 := false
	for seed := uint64(0); seed < 512; seed++ {
		rn := newRaid(1, 0, 64, 0, difficultyNormal, seed)
		if got := rn.getPotentialBonusSpawns(vindicator, 1, difficultyNormal, false); got > 1 {
			t.Fatalf("seed %d: VINDICATOR bonus at NORMAL = %d, want <= 1 (NORMAL pre-draw bonus is 1 -> nextInt(2))", seed, got)
		}
		rh := newRaid(1, 0, 64, 0, difficultyHard, seed)
		got := rh.getPotentialBonusSpawns(vindicator, 1, difficultyHard, false)
		if got > 2 {
			t.Fatalf("seed %d: VINDICATOR bonus at HARD = %d, want <= 2 (HARD pre-draw bonus is 2 -> nextInt(3))", seed, got)
		}
		if got == 2 {
			hardSaw2 = true
		}
	}
	if !hardSaw2 {
		t.Fatal("HARD VINDICATOR bonus never reached 2 across 512 seeds — the HARD branch (pre-draw bonus 2) is not selected by the difficulty argument")
	}
}

// TestEquipPartialChanceReadsLiveDifficulty: Mob.populateDefaultEquipmentSlots picks partialChance =
// (difficulty == HARD ? 0.1f : 0.25f), the per-slot break gate. Feeding NORMAL vs HARD (the LIVE diff
// now threaded through) with the SAME seed + a mult that opens the armor gate must, for at least one
// seed, diverge the equipped set (the 0.1 vs 0.25 break point differs). This proves the diff argument
// is wired to the partialChance, not baked to a constant. Cite Mob.populateDefaultEquipmentSlots.
func TestEquipPartialChanceReadsLiveDifficulty(t *testing.T) {
	const mult = float32(1.0) // getSpecialMultiplier saturated so the 0.15*mult armor gate is live
	diverged := false
	for seed := uint64(0); seed < 4096 && !diverged; seed++ {
		norm := NewEntity(1, entity.Zombie, 0, 0, 0)
		hard := NewEntity(2, entity.Zombie, 0, 0, 0)
		populateDefaultEquipmentSlots(norm, newEntityRandom(seed), mult, difficultyNormal)
		populateDefaultEquipmentSlots(hard, newEntityRandom(seed), mult, difficultyHard)
		for slot := 0; slot < equipmentSlotCount; slot++ {
			ns, hs := norm.getItemBySlot(slot), hard.getItemBySlot(slot)
			if ns.Count != hs.Count || ns.ItemID != hs.ItemID {
				diverged = true
				break
			}
		}
	}
	if !diverged {
		t.Fatal("NORMAL and HARD spawn-equip never diverged across 4096 seeds — partialChance is not " +
			"reading the live difficulty argument")
	}
}

// TestDifficultyCommandMutationObservedByHotPath: a /difficulty change (routed through the tick-owned
// setDifficulty mutation point the command shares) is OBSERVED by a gameplay hot path, not merely
// stored. We set HARD via setDifficulty, then drive the zombie reinforcement gate; it must now arm
// (the HARD-only branch), whereas the default NORMAL loop did not. This closes the "stored but not
// read" gap end-to-end. Cite MinecraftServer.setDifficulty + Zombie.hurtServer.
func TestDifficultyCommandMutationObservedByHotPath(t *testing.T) {
	loop, floorY, _ := zombieLoop(t)
	// The loop starts at the vanilla default NORMAL. Flip to HARD through the command mutation point.
	loop.setDifficulty(difficultyHard)
	if loop.levelDifficulty != difficultyHard {
		t.Fatalf("setDifficulty did not store HARD: got %d", loop.levelDifficulty)
	}
	parent := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	parent.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name()).SetBaseValue(1.0)
	parent.ai.attackTargetID = 4242
	before := countZombies(loop)
	loop.withRegion(loop.only(), func() {
		loop.zombieHurtReinforcements(parent, damageSource{attacker: 4242})
	})
	after := countZombies(loop)
	if after != before+1 {
		t.Fatalf("after /difficulty hard the reinforcement gate did not arm (count %d -> %d); the mutation "+
			"was stored but not read by the hot path", before, after)
	}
}
