package server

// zombie_finalize_test.go -- pins the ZombieGroupData baby + CHICKEN_JOCKEY slice of Zombie.finalizeSpawn
// (VERIFIED javap Zombie.finalizeSpawn @58-296 this session). The draw ORDER is load-bearing:
//
//	getSpawnAsBabyOdds(random): nextFloat() < 0.05F          // ALWAYS one draw
//	if baby: setBaby(true)
//	    if ((double) nextFloat() < 0.05D) ride-existing-chicken   // DRAW only when baby
//	    else if ((double) nextFloat() < 0.05D) spawn-new-chicken  // DRAW only when the first gate FAILS
//
// The tests drive zombieFinalizeSpawnBabyAndJockey on a seeded stream and assert (1) the baby-odds roll
// fires (a below-0.05 draw makes a baby, an above-0.05 draw does not) and (2) the 0.05 chicken-jockey
// gates fire IN ORDER for a baby -- verified by mirroring the exact draw sequence on an independent oracle
// stream and checking the observable outcome (baby flag + a spawned chicken) matches. The pig oracle is
// untouched (a zombie is a separate mob; the hook is zombie-family-gated).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// zombieAndChickenLoop builds a physics loop whose registry holds BOTH the vanilla_zombie and the
// vanilla_chicken declarations, so the chicken-jockey spawn path (spawnDeclaredMob of a Chicken) has a
// chicken decl to build from. Mirrors zombieLoop but loads two plugins into one registry.
func zombieAndChickenLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"vanilla_zombie", "vanilla_chicken"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		for _, f := range []string{"plugin.toml", "main.star"} {
			data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", name, f))
			if err != nil {
				t.Fatalf("read %s/%s: %v", name, f, err)
			}
			if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", name, f, err)
			}
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(zombie+chicken): %v", err)
	}
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(r)
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY
}

// countChickens counts live chicken-type entities across all regions (a spawned jockey chicken may land
// in either region -- regionOf hashes (x^z)&1).
func countChickens(loop *TickLoop) int {
	n := 0
	for _, r := range loop.regions {
		for _, e := range r.entities.all() {
			if e.typ == entity.Chicken.ID && !e.dead {
				n++
			}
		}
	}
	return n
}

// findSeedWhere returns the first seed in [0,limit) for which pickBabyThenJockey reports (baby, ride, new)
// matching the requested outcome, using the SAME draw sequence zombieFinalizeSpawnBabyAndJockey performs.
// This is the independent oracle: nextFloat() (baby), then (if baby) nextFloat() (ride gate), then (if
// ride failed) nextFloat() (new gate) -- the exact order the ported code draws.
func pickBabyThenJockey(seed uint64) (baby, ride, spawnNew bool) {
	r := newEntityRandom(seed)
	baby = r.nextFloat() < zombieSpawnAsBabyChance
	if !baby {
		return
	}
	if float64(r.nextFloat()) < zombieChickenJockeyChance {
		ride = true
		return
	}
	if float64(r.nextFloat()) < zombieChickenJockeyChance {
		spawnNew = true
	}
	return
}

// TestZombieFinalizeBabyOddsRoll: the getSpawnAsBabyOdds nextFloat() < 0.05F roll fires -- a seed whose
// first draw is < 0.05 makes a baby (breedAge < 0); a seed whose first draw is >= 0.05 leaves an adult.
func TestZombieFinalizeBabyOddsRoll(t *testing.T) {
	loop, floorY, _ := zombieLoop(t)

	// Find a seed the oracle says produces a baby, and one that does not.
	var babySeed, adultSeed uint64
	haveBaby, haveAdult := false, false
	for s := uint64(0); s < 100000 && !(haveBaby && haveAdult); s++ {
		baby, _, _ := pickBabyThenJockey(s)
		if baby && !haveBaby {
			babySeed, haveBaby = s, true
		}
		if !baby && !haveAdult {
			adultSeed, haveAdult = s, true
		}
	}
	if !haveBaby || !haveAdult {
		t.Fatal("could not find both a baby-producing and an adult-producing seed (baby-odds roll broken)")
	}

	// Baby seed -> breedAge < 0.
	z := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	z.breedAge = 0
	z.ai.rng = newEntityRandom(babySeed)
	loop.withRegion(loop.only(), func() {
		loop.zombieFinalizeSpawnBabyAndJockey(z, mobRandom(z))
	})
	if !z.isBaby() {
		t.Fatalf("baby-odds seed %d did not make a baby (breedAge %d, want < 0)", babySeed, z.breedAge)
	}

	// Adult seed -> breedAge unchanged (0).
	z2 := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	z2.breedAge = 0
	z2.ai.rng = newEntityRandom(adultSeed)
	loop.withRegion(loop.only(), func() {
		loop.zombieFinalizeSpawnBabyAndJockey(z2, mobRandom(z2))
	})
	if z2.isBaby() {
		t.Fatalf("adult seed %d wrongly made a baby (breedAge %d)", adultSeed, z2.breedAge)
	}
}

// TestZombieFinalizeChickenJockeyDrawOrder: for a BABY whose SECOND draw (the ride-existing gate) FAILS
// and whose THIRD draw (the spawn-new gate) SUCCEEDS, zombieFinalizeSpawnBabyAndJockey spawns a NEW chicken
// -- proving the two 0.05 jockey gates fire in order (ride-first, then spawn-new) on the exact stream. A
// baby whose ride gate SUCCEEDS spawns NO chicken (the ride branch takes no new spawn in v1).
func TestZombieFinalizeChickenJockeyDrawOrder(t *testing.T) {
	loop, floorY := zombieAndChickenLoop(t)

	// Find a seed: baby, ride gate FAILS, spawn-new gate SUCCEEDS -> a NEW chicken is spawned.
	var newSeed uint64
	found := false
	for s := uint64(0); s < 5000000 && !found; s++ {
		baby, ride, spawnNew := pickBabyThenJockey(s)
		if baby && !ride && spawnNew {
			newSeed, found = s, true
		}
	}
	if !found {
		t.Fatal("could not find a baby+ride-fail+spawn-new seed (jockey draw order untestable)")
	}

	chickBefore := countChickens(loop)
	z := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	z.breedAge = 0
	z.ai.rng = newEntityRandom(newSeed)
	loop.withRegion(loop.only(), func() {
		loop.zombieFinalizeSpawnBabyAndJockey(z, mobRandom(z))
	})
	if !z.isBaby() {
		t.Fatalf("spawn-new seed %d did not make a baby", newSeed)
	}
	if got := countChickens(loop); got <= chickBefore {
		t.Fatalf("no chicken jockey spawned (chicken count %d -> %d): the third-draw spawn-new gate did not fire in order", chickBefore, got)
	}

	// A baby whose RIDE gate succeeds spawns NO chicken (ride branch is a cited mount-deferral, no new spawn).
	var rideSeed uint64
	foundRide := false
	for s := uint64(0); s < 5000000 && !foundRide; s++ {
		baby, ride, _ := pickBabyThenJockey(s)
		if baby && ride {
			rideSeed, foundRide = s, true
		}
	}
	if !foundRide {
		t.Fatal("could not find a baby+ride-success seed")
	}
	chickBefore2 := countChickens(loop)
	z2 := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	z2.breedAge = 0
	z2.ai.rng = newEntityRandom(rideSeed)
	loop.withRegion(loop.only(), func() {
		loop.zombieFinalizeSpawnBabyAndJockey(z2, mobRandom(z2))
	})
	if got := countChickens(loop); got != chickBefore2 {
		t.Fatalf("ride-existing branch spawned a NEW chicken (count %d -> %d): the ride gate must take the ride branch (no new spawn), not the spawn-new branch", chickBefore2, got)
	}
}

// TestDrownedNautilusOffhandRollOrder: pins the Drowned.finalizeSpawn nautilus-shell offhand roll
// (VERIFIED javap Drowned.finalizeSpawn @11-64). The roll draws nextFloat() ONLY when OFFHAND is empty
// (Java && short-circuit), and on nextFloat() < 0.03 the OFFHAND becomes NAUTILUS_SHELL. This is the
// CORRECTION of the old audit's mislocation: the roll lives in Drowned.finalizeSpawn (after super), NOT in
// Drowned.populateDefaultEquipmentSlots (which is only the trident/fishing-rod roll -- initDrownedTridentEquip).
func TestDrownedNautilusOffhandRollOrder(t *testing.T) {
	// A seed whose first nextFloat() < 0.03 -> the empty offhand gets a nautilus shell.
	var hitSeed uint64
	found := false
	for s := uint64(0); s < 200000 && !found; s++ {
		if newEntityRandom(s).nextFloat() < drownedNautilusShellChance {
			hitSeed, found = s, true
		}
	}
	if !found {
		t.Fatal("could not find a seed with first nextFloat() < 0.03 (nautilus roll untestable)")
	}

	// Empty offhand + a below-0.03 first draw -> OFFHAND = NAUTILUS_SHELL.
	e := NewEntity(1, entity.Drowned, 0, 0, 0)
	drownedNautilusOffhandRoll(e, newEntityRandom(hitSeed))
	off := e.getItemBySlot(eqSlotOffHand)
	if off.Count == 0 || int32(off.ItemID) != int32(item.NautilusShell.ID) {
		t.Fatalf("nautilus roll on a <0.03 seed did not put a NAUTILUS_SHELL in OFFHAND (got count=%d id=%d)", off.Count, off.ItemID)
	}

	// A drowned whose OFFHAND is already occupied takes NO draw and keeps its item (the && short-circuit).
	// Prove the draw is short-circuited: seed the SAME below-0.03 stream, but pre-fill the offhand -- the
	// nautilus placement must NOT happen (the offhand keeps its original item).
	e2 := NewEntity(2, entity.Drowned, 0, 0, 0)
	e2.setItemSlot(eqSlotOffHand, itemStackOf(item.Bow))
	before := e2.getItemBySlot(eqSlotOffHand)
	drownedNautilusOffhandRoll(e2, newEntityRandom(hitSeed))
	after := e2.getItemBySlot(eqSlotOffHand)
	if int32(after.ItemID) != int32(before.ItemID) {
		t.Fatalf("a non-empty offhand was overwritten by the nautilus roll (id %d -> %d): the isEmpty() short-circuit failed", before.ItemID, after.ItemID)
	}
}
