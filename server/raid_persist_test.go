package server

// raid_persist_test.go — round-trips the Raids SavedData (raid_persist.go): build a raidsManager with a
// couple of raids in varied states, save to <tmp>/data/raids.dat, load it back, and assert every
// persisted field (the Raids.CODEC + Raid.MAP_CODEC shape: next_id, tick, and per-raid started/active/
// ticks_active/raid_omen_level/groups_spawned/cooldown_ticks/post_raid_ticks/total_health/group_count/
// status/center/heroes_of_the_village) survives byte-for-byte. Persistence draws ZERO from any pig
// stream, so the pig oracle is untouched.

import (
	"testing"

	"github.com/google/uuid"
)

func TestRaidsSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	rm := newRaidsManager()
	rm.tick = 1234
	// Raid A: an ONGOING started+active raid mid-schedule with two hero UUIDs.
	a := rm.createRaidAt(100, 64, -200, difficultyHard, 3)
	a.started = true
	a.active = true
	a.ticksActive = 5000
	a.groupsSpawned = 4
	a.raidCooldownTicks = 150
	a.postRaidTicks = 0
	a.totalHealth = 123.5
	a.status = raidStatusOngoing
	h1 := uuid.MustParse("12345678-1234-5678-1234-567812345678")
	h2 := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	a.heroesOfTheVillage = []uuid.UUID{h1, h2}

	// Raid B: a VICTORY raid with no heroes, different difficulty (group_count differs).
	b := rm.createRaidAt(-40, 70, 300, difficultyNormal, 1)
	b.started = true
	b.active = false
	b.ticksActive = 48000
	b.groupsSpawned = 5
	b.raidCooldownTicks = 0
	b.postRaidTicks = 40
	b.totalHealth = 0
	b.status = raidStatusVictory

	wantNextID := rm.nextId
	wantTick := rm.tick

	// Save the immutable encode, then reload from disk.
	if err := saveRaids(dir, encodeRaidsData(rm)); err != nil {
		t.Fatalf("saveRaids: %v", err)
	}
	got, ok, err := loadRaids(dir)
	if err != nil {
		t.Fatalf("loadRaids: %v", err)
	}
	if !ok {
		t.Fatal("loadRaids: miss on a file we just wrote")
	}

	if got.nextId != wantNextID {
		t.Errorf("nextId = %d, want %d", got.nextId, wantNextID)
	}
	if got.tick != wantTick {
		t.Errorf("tick = %d, want %d", got.tick, wantTick)
	}
	if len(got.raidMap) != 2 {
		t.Fatalf("raid count = %d, want 2", len(got.raidMap))
	}
	if got.isDirty() {
		t.Error("a freshly-loaded raidsManager must be clean (SavedData.dirty=false), got dirty")
	}

	assertRaidEqual(t, "A", a, got.raidMap[a.id])
	assertRaidEqual(t, "B", b, got.raidMap[b.id])

	// Heroes: exact set membership on raid A.
	ga := got.raidMap[a.id]
	if len(ga.heroesOfTheVillage) != 2 {
		t.Fatalf("A heroes = %d, want 2", len(ga.heroesOfTheVillage))
	}
	set := map[uuid.UUID]bool{}
	for _, u := range ga.heroesOfTheVillage {
		set[u] = true
	}
	if !set[h1] || !set[h2] {
		t.Errorf("A heroes = %v, want {%v,%v}", ga.heroesOfTheVillage, h1, h2)
	}
}

// assertRaidEqual checks every persisted Raid.MAP_CODEC field round-trips (excluding non-persisted
// runtime scaffolding: rng/bossEvent/groupRaiderMap, which are rebuilt fresh on load).
func assertRaidEqual(t *testing.T, tag string, want, got *Raid) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: raid missing after load", tag)
	}
	if got.id != want.id {
		t.Errorf("%s id = %d, want %d", tag, got.id, want.id)
	}
	if got.started != want.started {
		t.Errorf("%s started = %v, want %v", tag, got.started, want.started)
	}
	if got.active != want.active {
		t.Errorf("%s active = %v, want %v", tag, got.active, want.active)
	}
	if got.ticksActive != want.ticksActive {
		t.Errorf("%s ticksActive = %d, want %d", tag, got.ticksActive, want.ticksActive)
	}
	if got.raidOmenLevel != want.raidOmenLevel {
		t.Errorf("%s raidOmenLevel = %d, want %d", tag, got.raidOmenLevel, want.raidOmenLevel)
	}
	if got.groupsSpawned != want.groupsSpawned {
		t.Errorf("%s groupsSpawned = %d, want %d", tag, got.groupsSpawned, want.groupsSpawned)
	}
	if got.raidCooldownTicks != want.raidCooldownTicks {
		t.Errorf("%s raidCooldownTicks = %d, want %d", tag, got.raidCooldownTicks, want.raidCooldownTicks)
	}
	if got.postRaidTicks != want.postRaidTicks {
		t.Errorf("%s postRaidTicks = %d, want %d", tag, got.postRaidTicks, want.postRaidTicks)
	}
	if got.totalHealth != want.totalHealth {
		t.Errorf("%s totalHealth = %v, want %v", tag, got.totalHealth, want.totalHealth)
	}
	if got.numGroups != want.numGroups {
		t.Errorf("%s numGroups (group_count) = %d, want %d", tag, got.numGroups, want.numGroups)
	}
	if got.status != want.status {
		t.Errorf("%s status = %v, want %v", tag, got.status, want.status)
	}
	if got.centerX != want.centerX || got.centerY != want.centerY || got.centerZ != want.centerZ {
		t.Errorf("%s center = (%d,%d,%d), want (%d,%d,%d)", tag,
			got.centerX, got.centerY, got.centerZ, want.centerX, want.centerY, want.centerZ)
	}
}

// TestLoadRaidsMissingFile confirms a first boot (no raids.dat) is a clean (nil,false,nil) miss, not an
// error — the lazy ensureRaidsManager path takes over.
func TestLoadRaidsMissingFile(t *testing.T) {
	dir := t.TempDir()
	rm, ok, err := loadRaids(dir)
	if err != nil {
		t.Fatalf("loadRaids on empty dir: unexpected error %v", err)
	}
	if ok || rm != nil {
		t.Fatalf("loadRaids on empty dir = (%v,%v), want (nil,false)", rm, ok)
	}
}

// TestUUIDIntArrayRoundTrip pins the UUIDUtil.uuidToIntArray/uuidFromIntArray port (the heroes codec).
func TestUUIDIntArrayRoundTrip(t *testing.T) {
	for _, s := range []string{
		"12345678-1234-5678-1234-567812345678",
		"00000000-0000-0000-0000-000000000000",
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
		"deadbeef-cafe-babe-f00d-0123456789ab",
	} {
		u := uuid.MustParse(s)
		if got := uuidFromIntArray(uuidToIntArray(u)); got != u {
			t.Errorf("uuid round-trip %s -> %s", u, got)
		}
	}
}
