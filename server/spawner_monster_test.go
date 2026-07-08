package server

// spawner_monster_test.go -- Phase 35-02 (MOB-SUB-11, SC#3 + SC#4): the MONSTER spawn-gating tests.
// The 3 hostile categoryOf cases, the monsterCap (70 / spawnableChunkCount / 289), the REAL
// per-position light darkness gate (Monster.isDarkEnoughToSpawn over the ported light engine), the
// cap-gated natural-spawn MONSTER pass, and the explicit pickNaturalMonsterMob picker. PURE-ADDITIVE
// over the CREATURE machinery (spawner_test.go) -- the pig/cow/sheep/chicken accounting and the pig
// oracle are untouched. The darkness gate is now the real light read applied per-candidate inside
// spawnPackAt, so a test controls "is it dark here" by setting the chunk SKY light.

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// lightAllSpawnColumns sets the SKY light of every loaded column near the player to value so the
// per-position Monster.isDarkEnoughToSpawn gate reads a controlled brightness at candidate positions.
// value 15 == a fully daylit surface (the gate REJECTS every candidate: getMaxLocalRawBrightness == 15
// > any nextInt(8) sample); value 0 == a dark cave cell (the gate PASSES: 0 <= nextInt(8)).
func lightAllSpawnColumns(loop *TickLoop, value byte) {
	w := loop.only().world
	for _, col := range loop.spawnableColumns() {
		if ch, ok := w.Get(col); ok {
			setSkyLight(ch, value)
		}
	}
}

// runSpawnMonsterCycle drives ONE full natural-spawn MONSTER pass end-to-end for a test, in ISOLATION
// from the CREATURE pass. It mirrors the production MONSTER pass: it ALWAYS submits (the darkness gate
// is no longer a once-per-cycle pre-submit check -- it is the REAL per-candidate light read inside
// spawnPackAt). If a scan submitted it deterministically receives the worker result and applies it on
// the test goroutine (standing in for applyAsyncResults); applyTo -> spawnPackAt runs the per-position
// isDarkEnoughToSpawn gate, so a daylit column yields NO monster and a dark column yields one.
func runSpawnMonsterCycle(t *testing.T, loop *TickLoop) {
	t.Helper()
	if loop.cur().spawnScanPending {
		return
	}
	cols := loop.spawnableColumns()
	if len(cols) == 0 {
		return
	}
	if !loop.submitSpawnScanFor(categoryMonster, cols, len(cols), loop.spawnRefY()) {
		return
	}
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop)
	case <-time.After(2 * time.Second):
		t.Fatal("monster spawn scan result never arrived on asyncIn2 (the off-tick worker did not rejoin)")
	}
}

// TestCategoryOfMonster: the 3 hostiles map to MobCategory.MONSTER; the pig stays CREATURE.
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
			t.Fatalf("categoryOf(%s) = %v, want categoryMonster", tc.name, got)
		}
	}
	if got := categoryOf(entity.Pig.ID); got != categoryCreature {
		t.Fatalf("the pig must stay CREATURE (additive MONSTER cases must not perturb it), got %v", got)
	}
}

// TestMonsterCap: maxInstancesPerChunk(70) * spawnableChunkCount / MAGIC_NUMBER(289).
func TestMonsterCap(t *testing.T) {
	if got := monsterCap(289); got != 70 {
		t.Fatalf("monsterCap(289) = %d, want 70 (70*289/289)", got)
	}
	if got := monsterCap(1); got != 0 {
		t.Fatalf("monsterCap(1) = %d, want 0 (the /289 divisor is kept)", got)
	}
	if got := monsterCap(578); got != 140 {
		t.Fatalf("monsterCap(578) = %d, want 140 (70*578/289)", got)
	}
}

// TestIsNightByGametime: the day/night GAMETIME proxy (Spider daylight-flee / skeleton sun-burn /
// bed-rule / fox-sleep gates -- NOT the spawn darkness gate). True in the night window of
// t.gametime % 24000 (dusk 13000 .. dawn 23000).
func TestIsNightByGametime(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	for _, tc := range []struct {
		gametime  int64
		nightWant bool
		desc      string
	}{
		{0, false, "midday tick 0 (day)"},
		{6000, false, "noon (day)"},
		{12999, false, "just before dusk (day)"},
		{13000, true, "dusk start (night begins)"},
		{18000, true, "midnight (night)"},
		{22999, true, "just before dawn (night)"},
		{23000, false, "dawn (day resumes)"},
		{24000 + 18000, true, "next day midnight (wrap)"},
		{24000 + 6000, false, "next day noon (wrap)"},
	} {
		loop.gametime = tc.gametime
		if got := loop.isNightByGametime(); got != tc.nightWant {
			t.Fatalf("isNightByGametime() at gametime=%d (%s) = %v, want %v", tc.gametime, tc.desc, got, tc.nightWant)
		}
	}
}

// TestIsDarkEnoughToSpawnLightGate: the REAL per-position Monster.isDarkEnoughToSpawn light read. At a
// FULL-DAYLIGHT surface cell (SKY 15) the gate returns false for EVERY RNG outcome. At a DARK cave cell
// (SKY 0, BLOCK 0) the gate returns true (0 <= nextInt(8)).
func TestIsDarkEnoughToSpawnLightGate(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	pos := pk.Position{X: 8, Y: floorY + 1, Z: 8}

	lightAllSpawnColumns(loop, 15)
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 100; i++ {
			if loop.isDarkEnoughToSpawn(pos) {
				t.Fatalf("isDarkEnoughToSpawn at a SKY-15 surface must ALWAYS be false, got true on try %d", i)
			}
		}
	})

	lightAllSpawnColumns(loop, 0)
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 100; i++ {
			if !loop.isDarkEnoughToSpawn(pos) {
				t.Fatalf("isDarkEnoughToSpawn at a SKY-0/BLOCK-0 dark cell must ALWAYS be true, got false on try %d", i)
			}
		}
	})
}

// findAnyNaturalMonster scans EVERY region for the first naturally-spawnable MONSTER mob.
func findAnyNaturalMonster(loop *TickLoop) *Entity {
	return findEntityOfType(loop, entity.Zombie, entity.Skeleton, entity.Spider)
}

// TestMonsterPassDaytimeNoSpawn: with the spawn columns fully DAYLIT (SKY 15), the natural-spawn
// MONSTER pass adds NO hostile -- the per-position dark gate rejects every daylit candidate. This is
// the reported-bug regression test: hostiles must NOT spawn on a daylit surface.
func TestMonsterPassDaytimeNoSpawn(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)
	lightAllSpawnColumns(loop, 15)
	for i := 0; i < 8; i++ {
		runSpawnMonsterCycle(t, loop)
	}
	if m := findAnyNaturalMonster(loop); m != nil {
		t.Fatalf("the MONSTER pass must NOT spawn a hostile on a daylit surface (light gate): found %v", m.typ)
	}
}

// TestMonsterPassDarkSpawns: with the spawn columns fully DARK (SKY 0), the natural-spawn MONSTER pass
// DOES place a hostile -- the per-position dark gate passes and the cap/columns permit it.
func TestMonsterPassDarkSpawns(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)
	lightAllSpawnColumns(loop, 0)
	for i := 0; i < 40; i++ {
		runSpawnMonsterCycle(t, loop)
	}
	if m := findAnyNaturalMonster(loop); m == nil {
		t.Fatal("the MONSTER pass MUST spawn a hostile in the dark (SKY light 0): found none")
	}
}

// TestMonsterCapBlocks: with the live MONSTER count already AT the cap, the monster pass adds no
// hostile even in the dark -- the cap bounds the global MONSTER count.
func TestMonsterCapBlocks(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	lightAllSpawnColumns(loop, 0)
	cols := loop.spawnableColumns()
	if len(cols) == 0 {
		t.Fatal("expected at least one spawnable column near the player")
	}
	cap := monsterCap(len(cols))
	for i := 0; i < cap; i++ {
		loop.only().entities.add(NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 8.5, float64(floorY+1), 8.5))
	}
	before := totalEntities(loop)
	for i := 0; i < 4; i++ {
		runSpawnMonsterCycle(t, loop)
	}
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

// TestPickNaturalMonsterMob: the picker returns one of the explicit naturalMonsterMobNames.
func TestPickNaturalMonsterMob(t *testing.T) {
	if len(naturalMonsterMobNames) != 5 {
		t.Fatalf("naturalMonsterMobNames must hold the 5 overworld hostiles, got %v", naturalMonsterMobNames)
	}
	want := map[string]bool{
		vanillaZombieMobName:   true,
		vanillaSkeletonMobName: true,
		vanillaSpiderMobName:   true,
		vanillaCreeperMobName:  true,
		vanillaEndermanMobName: true,
	}
	for _, n := range naturalMonsterMobNames {
		if !want[n] {
			t.Fatalf("naturalMonsterMobNames has an unexpected entry %q (must be a vanilla hostile)", n)
		}
	}
	loop, _, _ := newSpawnLoop(t)
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 20; i++ {
			got := loop.pickNaturalMonsterMob()
			if !want[got] {
				t.Fatalf("pickNaturalMonsterMob returned %q, not a member of naturalMonsterMobNames", got)
			}
		}
	})
}
