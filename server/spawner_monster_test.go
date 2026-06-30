package server

// spawner_monster_test.go — Phase 35-02 (MOB-SUB-11, SC#3 + SC#4): the MONSTER spawn-gating
// tests. The 3 hostile categoryOf cases, the monsterCap (70 / spawnableChunkCount / 289), the
// FORCED gametime-darkness isDarkEnoughToSpawn proxy, the night-gated + cap-gated natural-spawn
// MONSTER pass, and the explicit pickNaturalMonsterMob picker. These are PURE-ADDITIVE over the
// CREATURE machinery (spawner_test.go) — the pig/cow/sheep/chicken CREATURE accounting and the
// pig oracle are untouched.
//
// The harness REUSES newSpawnLoop / runSpawnCycle / totalEntities / findEntityOfType from
// spawner_test.go (same package). The MONSTER-pass tests gate the day/night proxy directly via
// loop.gametime so a test controls "is it night" deterministically.

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

// runSpawnMonsterCycle drives ONE full natural-spawn MONSTER pass end-to-end for a test, in
// ISOLATION from the CREATURE pass (so a MONSTER-cap / dark-gate assertion is not masked by the
// CREATURE-first ordering of naturalSpawn's single-in-flight gate). It applies the SAME dark gate +
// submit machinery the production naturalSpawn uses for its MONSTER pass: gate on isDarkEnoughToSpawn,
// then submitSpawnScanFor(categoryMonster, ...). If a scan submitted (night + under cap + columns +
// pool free) it deterministically receives the worker result and applies it on the test goroutine
// (standing in for applyAsyncResults). When the dark gate is shut OR MONSTER is at cap, nothing
// submits — the helper returns immediately (no monster, as expected). Production parity: naturalSpawn
// runs this exact MONSTER pass after the CREATURE pass when the gate is free.
func runSpawnMonsterCycle(t *testing.T, loop *TickLoop) {
	t.Helper()
	if loop.cur().spawnScanPending {
		return // a scan already in flight (the single-in-flight gate) — mirror naturalSpawn's early out
	}
	if !loop.isDarkEnoughToSpawn() {
		return // daytime: the FORCED dark gate blocks the MONSTER pass (no submit, no hostile)
	}
	cols := loop.spawnableColumns()
	if len(cols) == 0 {
		return
	}
	if !loop.submitSpawnScanFor(categoryMonster, cols, len(cols), loop.spawnRefY()) {
		return // at cap or pool overload: no scan submitted this cycle
	}
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop) // the owner re-checks the MONSTER cap + mobNear and adds at most one (async.go)
	case <-time.After(2 * time.Second):
		t.Fatal("monster spawn scan result never arrived on asyncIn2 (the off-tick worker did not rejoin)")
	}
}

// TestCategoryOfMonster: the 3 hostiles (zombie/skeleton/spider) map to MobCategory.MONSTER, like
// the Phase-34 CREATURE fix mapped cow/sheep/chicken — so countByCategory tallies them under the
// MONSTER cap. The pig still maps to CREATURE (the additive arms leave the CREATURE path intact).
func TestCategoryOfMonster(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   entity.ID
	}{
		{"zombie", entity.Zombie.ID},
		{"skeleton", entity.Skeleton.ID},
		{"spider", entity.Spider.ID},
	} {
		if got := categoryOf(tc.id); got != categoryMonster {
			t.Fatalf("categoryOf(%s) = %v, want categoryMonster (data/entity %s.Type == \"monster\")", tc.name, got, tc.name)
		}
	}
	// The pig is UNTOUCHED — still CREATURE, never dragged into the MONSTER cap.
	if got := categoryOf(entity.Pig.ID); got != categoryCreature {
		t.Fatalf("the pig must stay CREATURE (additive MONSTER cases must not perturb it), got %v", got)
	}
}

// TestMonsterCap: the MONSTER cap is maxInstancesPerChunk(70) * spawnableChunkCount / MAGIC_NUMBER(289)
// — the SAME /289 divisor as creatureCap, only the per-category value (70 vs 10) differs. At
// spawnableChunkCount==289 the cap is exactly 70 (the MONSTER max-per-chunk).
func TestMonsterCap(t *testing.T) {
	if got := monsterCap(289); got != 70 {
		t.Fatalf("monsterCap(289) = %d, want 70 (70*289/289)", got)
	}
	// The divisor is kept: a single column yields 70*1/289 = 0 (no spawn under a tiny area), exactly
	// as creatureCap(1)==0. This is the load-bearing anti-flood divisor (Pitfall 3).
	if got := monsterCap(1); got != 0 {
		t.Fatalf("monsterCap(1) = %d, want 0 (the /289 divisor is kept)", got)
	}
	// A radius-8 fully-loaded area (~17x17 = 289 columns) yields ~70 — matching vanilla's MONSTER cap.
	if got := monsterCap(578); got != 140 {
		t.Fatalf("monsterCap(578) = %d, want 140 (70*578/289)", got)
	}
}

// TestIsDarkEnoughToSpawn: the FORCED gametime-darkness proxy (35-CONTEXT SC#4). It returns true in
// the night window of t.gametime % 24000 (vanilla dusk≈13000 .. dawn≈23000) and false in daytime.
// This is the cited stub standing in for Monster.isDarkEnoughToSpawn's SKY/BLOCK light read until the
// lighting engine lands — NEVER a silent daylight flood.
func TestIsDarkEnoughToSpawn(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	for _, tc := range []struct {
		gametime int64
		darkWant bool
		desc     string
	}{
		{0, false, "midday tick 0 (day)"},
		{6000, false, "noon (day)"},
		{12999, false, "just before dusk (day)"},
		{13000, true, "dusk start (night begins)"},
		{18000, true, "midnight (night)"},
		{22999, true, "just before dawn (night)"},
		{23000, false, "dawn (day resumes)"},
		{24000 + 18000, true, "next day's midnight (the % 24000 wrap)"},
		{24000 + 6000, false, "next day's noon (the % 24000 wrap)"},
	} {
		loop.gametime = tc.gametime
		if got := loop.isDarkEnoughToSpawn(); got != tc.darkWant {
			t.Fatalf("isDarkEnoughToSpawn() at gametime=%d (%s) = %v, want %v", tc.gametime, tc.desc, got, tc.darkWant)
		}
	}
}

// --- Task 2: the natural-spawn MONSTER pass (dark + cap gate) + pickNaturalMonsterMob ----------

// findAnyNaturalMonster scans EVERY region for the first naturally-spawnable MONSTER mob (zombie,
// skeleton, or spider) — the MONSTER analogue of findAnyNaturalCreature. A monster spawn can be
// routed into either region (the column-owning region), so it scans across regions.
func findAnyNaturalMonster(loop *TickLoop) *Entity {
	return findEntityOfType(loop, entity.Zombie, entity.Skeleton, entity.Spider)
}

// TestMonsterPassDaytimeNoSpawn: with isDarkEnoughToSpawn() FALSE (daytime), the natural-spawn
// MONSTER pass adds NO hostile — the dark gate blocks the monster submit even when columns are
// loaded and the MONSTER count is well under cap. (The CREATURE pass is unaffected — it has no dark
// gate — so this test asserts specifically that NO monster appears, not that nothing spawns.)
func TestMonsterPassDaytimeNoSpawn(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)
	loop.gametime = 6000 // noon: isDarkEnoughToSpawn() == false

	// Drive several full cycles; no monster may ever appear while it is daytime.
	for i := 0; i < 8; i++ {
		runSpawnMonsterCycle(t, loop)
	}
	if m := findAnyNaturalMonster(loop); m != nil {
		t.Fatalf("the MONSTER pass must NOT spawn a hostile in daytime (dark gate): found %v", m.typ)
	}
}

// TestMonsterCapBlocks: with the live MONSTER count already AT the cap, the monster pass (and the
// apply-time cross-region re-check) adds no hostile even at night — the cap bounds the global MONSTER
// count exactly like the CREATURE cap.
func TestMonsterCapBlocks(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	loop.gametime = 18000 // midnight: dark gate OPEN

	cols := loop.spawnableColumns()
	if len(cols) == 0 {
		t.Fatal("expected at least one spawnable column near the player")
	}
	cap := monsterCap(len(cols))
	// Fill the store with exactly `cap` MONSTER mobs (zombies) so MONSTER is AT the cap.
	for i := 0; i < cap; i++ {
		loop.only().entities.add(NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 8.5, float64(floorY+1), 8.5))
	}
	before := totalEntities(loop)
	for i := 0; i < 4; i++ {
		runSpawnMonsterCycle(t, loop)
	}
	// At cap, no NEW monster may be added (count may still grow via the CREATURE pass, but the count
	// of MONSTER-category entities must not exceed the cap).
	monsters := 0
	for _, r := range loop.regions {
		if r == nil || r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if categoryOf(e.typ) == categoryMonster {
				monsters++
			}
		}
	}
	if monsters > cap {
		t.Fatalf("the MONSTER cap must bound spawns: %d monsters > cap %d (before=%d)", monsters, cap, before)
	}
}

// TestPickNaturalMonsterMob: the picker returns one of the explicit naturalMonsterMobNames
// {vanilla_zombie, vanilla_skeleton, vanilla_spider}, drawn from the per-region seeded levelRandom.
func TestPickNaturalMonsterMob(t *testing.T) {
	if len(naturalMonsterMobNames) != 3 {
		t.Fatalf("naturalMonsterMobNames must hold the 3 hostiles, got %v", naturalMonsterMobNames)
	}
	want := map[string]bool{
		vanillaZombieMobName:   true,
		vanillaSkeletonMobName: true,
		vanillaSpiderMobName:   true,
	}
	for _, n := range naturalMonsterMobNames {
		if !want[n] {
			t.Fatalf("naturalMonsterMobNames has an unexpected entry %q (must be a vanilla hostile)", n)
		}
	}

	loop, _, _ := newSpawnLoop(t)
	// The picker reads cur().levelRandom — call it inside the owning region (globalRegion for N=1).
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 20; i++ {
			got := loop.pickNaturalMonsterMob()
			if !want[got] {
				t.Fatalf("pickNaturalMonsterMob returned %q, not a member of naturalMonsterMobNames", got)
			}
		}
	})
}
