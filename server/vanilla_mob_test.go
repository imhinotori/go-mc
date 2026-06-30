package server

// vanilla_mob_test.go — Plan 34-04 (THE GATE): the generalized boot-load + spawn coverage. It proves
// the ONE registry holds all 4 vanilla mobs after the generalized boot-load, that spawnVanillaMob(name)
// builds each as the right wire entity, and that the natural-spawn pick reaches all 4 CREATURE mobs via
// the seeded per-region source (NOT the unseeded global rand, T-34-11). The pig oracle
// (TestPluginPigEqualsGoNativePig, plugin_pig_test.go) is the SEPARATE byte-identical gate and is
// untouched — this file is the lighter multi-mob coverage the Phase-34 dogfood adds on top.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// allFourMobs pairs each declared mob name with its expected wire base type + jar goal count
// (registerGoals): pig 9 (FloatGoal@0 + PanicGoal@1 + BreedGoal@3 + 2×TemptGoal@4 + FollowParent@5 +
// @6/@7/@8), cow 8 (@0..7), sheep 9 (@0..8 with EatBlockGoal@5), chicken 8 (@0..7). The counts are the
// SAME ones the per-mob tests (plugin_pig_test/cow_test/sheep_test/chicken_test) assert independently;
// re-asserted here from the ONE generalized registry to prove the loop loaded EACH faithfully.
var allFourMobs = []struct {
	name      string
	baseID    entity.ID
	goalCount int
}{
	{vanillaPigMobName, entity.Pig.ID, 9},
	{vanillaCowMobName, entity.Cow.ID, 8},
	{vanillaSheepMobName, entity.Sheep.ID, 9},
	{vanillaChickenMobName, entity.Chicken.ID, 8},
}

// TestAllFourMobsBootLoad: loadVanillaMobRegistry returns ONE registry holding the 4 passive
// declarations, each with the right base type + jar goal count. This is the T-34-10 guarantee (no
// silent missing mob) exercised directly. As of Plan 35-06 the same ONE registry ALSO boot-loads the 3
// Phase-35 hostiles (zombie/skeleton/spider) additively — so the total declaration count is now 7 (the 4
// passives asserted here + the 3 hostiles asserted by TestHostilesBootLoad). The exact total guards
// against an accidental extra/missing mob in vanillaMobNames.
func TestAllFourMobsBootLoad(t *testing.T) {
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	const wantTotal = 7 // 4 passives (this test) + 3 Phase-35 hostiles (TestHostilesBootLoad)
	if got := len(r.byName); got != wantTotal {
		t.Fatalf("registry holds %d declarations, want %d (pig/cow/sheep/chicken + zombie/skeleton/spider)", got, wantTotal)
	}
	for _, m := range allFourMobs {
		decl, ok := r.byName[m.name]
		if !ok {
			t.Fatalf("registry missing %q declaration after boot-load (T-34-10: must fail loudly, never silently)", m.name)
		}
		if decl.baseType.ID != m.baseID {
			t.Fatalf("%s base type = %d, want %d", m.name, decl.baseType.ID, m.baseID)
		}
		if len(decl.goals) != m.goalCount {
			t.Fatalf("%s captured %d goals, want %d (the jar registerGoals set)", m.name, len(decl.goals), m.goalCount)
		}
	}
}

// TestSpawnVanillaMobByName: spawnVanillaMob(name) builds each mob as its right wire entity with a
// non-nil AI carrying the declared goals — the generic spawn lever the /dbg + natural-spawn paths use.
func TestSpawnVanillaMobByName(t *testing.T) {
	loop, _ := newPhysicsLoop() // installs the 4-mob registry (loadVanillaMobRegistry via the harness)
	loop.start(loop.clock.(*fakeClock).Now())

	for _, m := range allFourMobs {
		e := loop.spawnVanillaMob(m.name, 8.5, 64, 8.5)
		if e == nil {
			t.Fatalf("spawnVanillaMob(%q) returned nil", m.name)
		}
		if e.typ != m.baseID {
			t.Fatalf("spawnVanillaMob(%q) typ = %d, want %d (custom = behavior, renders as the base wire id)", m.name, e.typ, m.baseID)
		}
		if e.ai == nil {
			t.Fatalf("spawnVanillaMob(%q) has no AI", m.name)
		}
		if got := len(e.ai.goals.goals); got != m.goalCount {
			t.Fatalf("spawnVanillaMob(%q) built %d goals, want %d", m.name, got, m.goalCount)
		}
	}
}

// TestPigOracleStillRoutesThroughDecl: spawnVanillaPig stays a thin wrapper over spawnVanillaMob and
// resolves the SAME pig declaration the generalized registry holds — a guard that the Plan 34-04
// generalization did not detour the pig path. The bit-exact proof is TestPluginPigEqualsGoNativePig
// (untouched); this only pins that the wrapper still hits the pig decl + spawnVanillaPigWithID is intact.
func TestPigOracleStillRoutesThroughDecl(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	pig := loop.spawnVanillaPig(8.5, 64, 8.5)
	if pig == nil || pig.typ != entity.Pig.ID {
		t.Fatalf("spawnVanillaPig built %v, want a pig (entity.Pig.ID %d)", pig, entity.Pig.ID)
	}
	// spawnVanillaPigWithID (the oracle helper) must still pin the supplied id (untouched by 34-04).
	withID := loop.spawnVanillaPigWithID(424242, 9.5, 64, 9.5)
	if withID == nil || withID.id != 424242 {
		t.Fatalf("spawnVanillaPigWithID(424242) = %v, want id 424242 pinned (oracle helper unchanged)", withID)
	}
	if withID.typ != entity.Pig.ID {
		t.Fatalf("spawnVanillaPigWithID typ = %d, want entity.Pig.ID %d", withID.typ, entity.Pig.ID)
	}
}

// TestNaturalSpawnPicksAmongFour: pickNaturalCreatureMob over many seeded draws returns EACH of the 4
// CREATURE mobs (none is unreachable) and ONLY the 4 valid names. The draw reads the region's seeded
// levelRandom (NOT the unseeded global rand.IntN, T-34-11) — the test reseeds region 0's levelRandom
// across seeds and over thousands of draws to cover the uniform pick deterministically.
func TestNaturalSpawnPicksAmongFour(t *testing.T) {
	loop, _ := newPhysicsLoop()

	valid := map[string]bool{
		vanillaPigMobName:     true,
		vanillaCowMobName:     true,
		vanillaSheepMobName:   true,
		vanillaChickenMobName: true,
	}
	seen := map[string]bool{}

	// The test goroutine has no region registered, so cur() falls back to region 0; reseed THAT
	// region's levelRandom so the draw is deterministic and we sweep the full pick space.
	for seed := int64(0); seed < 64; seed++ {
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(seed)
		for draw := 0; draw < 64; draw++ {
			name := loop.pickNaturalCreatureMob()
			if !valid[name] {
				t.Fatalf("pickNaturalCreatureMob returned %q, not one of the 4 CREATURE mobs", name)
			}
			seen[name] = true
		}
	}

	for name := range valid {
		if !seen[name] {
			t.Fatalf("pickNaturalCreatureMob never returned %q over %d draws — that mob is unreachable from natural spawn", name, 64*64)
		}
	}
}
