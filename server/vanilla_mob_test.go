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
//
// goalCount is the number of GOAL-SELECTOR goals (every non-TARGET-flag goal — what lands in
// e.ai.goals.goals at spawn); targetCount is the number of TARGET-flag goals (what routes into the
// independent e.ai.targetSelector.goals, the Mob.registerGoals goalSelector/targetSelector split). The
// passive mobs declare NO target goal (targetCount 0), so their registry total == goalCount.
//
// Plan 36-03 adds the wolf (the 8th boot-loaded declaration): its IN-SCOPE Wolf.registerGoals set is 15
// goals — goalSelector float/panic/sit/leap_at_target/melee_attack/follow_owner/breed/stroll/look/around
// (10) + targetSelector owner_hurt_by/owner_hurt/hurt_by_target/angry_player_target/skeleton_target (5).
// The 5 cited+omitted deferrals (WolfAvoidEntityGoal@3, BegGoal@9, NonTameRandomTarget@5/@6,
// ResetUniversalAnger@8) are NOT counted — they ship with their not-yet-built targets/subsystems.
var allFourMobs = []struct {
	name        string
	baseID      entity.ID
	goalCount   int
	targetCount int
}{
	{vanillaPigMobName, entity.Pig.ID, 9, 0},
	{vanillaCowMobName, entity.Cow.ID, 8, 0},
	{vanillaSheepMobName, entity.Sheep.ID, 9, 0},
	{vanillaChickenMobName, entity.Chicken.ID, 8, 0},
	{vanillaWolfMobName, entity.Wolf.ID, 10, 5},
	// MOB-VARIANT (Task #9): Husk == Zombie goals (4 goalSelector + 2 targetSelector, base Husk wire id);
	// Mooshroom == Cow goals (8 goalSelector + 0 targetSelector, base Mooshroom wire id).
	{vanillaHuskMobName, entity.Husk.ID, 4, 2},
	{vanillaMooshroomMobName, entity.Mooshroom.ID, 8, 0},
	// MOB-HOST-05 (Task #9 + infest + powder-snow): Silverfish — float@1 + climb_on_powder_snow@1 +
	// wake_friends@3 + melee@4 + merge_stone@5 (5 goalSelector) + hurt_by@1 + nearest@2 (2 targetSelector).
	// wake-friends + merge-stone infest goals wired (#16); climb_on_powder_snow wired (#17).
	{vanillaSilverfishMobName, entity.Silverfish.ID, 5, 2},
	// MOB-HOST-06 (Task #9): Creeper — float@1 + swell@2 + avoid_entity(cat)@3 + melee@4 + stroll@5 +
	// look@6 + around@6 (7 goalSelector) + nearest@1 + hurt_by@2 (2 targetSelector); the AvoidEntity Cat
	// leg is wired (kind="avoid_entity" avoid_type="cat", ai_goals_avoid.go), the Ocelot leg cite-deferred.
	// Creeper is NOT in POWDER_SNOW_WALKABLE_MOBS per the jar — no climb goal (#17 verified).
	{vanillaCreeperMobName, entity.Creeper.ID, 7, 2},
	// MOB-HOST-07 (Task #9 + heal branch): Witch — float@1 + witch_ranged@2 + stroll@2 + look@3 +
	// around@3 (5 goalSelector) + hurt_by@1 + healable_raider@2 + nearest@3 (3 targetSelector). The self-
	// drink potion buff is a per-type aiStep hook (witchAiStep, NOT a goal); the healable-raider TARGET goal
	// is a STRUCTURE (inert: hasActiveRaid stub-false, no raid subsystem). The raid EVENT is cite-deferred.
	{vanillaWitchMobName, entity.Witch.ID, 5, 3},
	// MOB-PASS-05 (Task #9 + hop + powder-snow): Rabbit — float@1 + climb_on_powder_snow@1 + panic@1 +
	// breed@2 + tempt@3 + stroll@6 + look@11 (7 goalSelector, 0 targetSelector); climb wired (#17), the
	// hop is now Go-native (#13, rabbitAiStep); avoid/raid-garden cite-deferred.
	{vanillaRabbitMobName, entity.Rabbit.ID, 7, 0},
	// MOB-HOST-08 (Task #9 + gaze + block-carry): Enderman — float@0 + freeze@1 + melee@2 + stroll@7 + look@8 +
	// around@8 + leave_block@10 + take_block@11 (8 goalSelector) + look_for_player@1 + hurt_by@2 (2 targetSelector);
	// the gaze subsystem (freeze + look_for_player) is Go-native (#14, ai_goals_enderman_gaze.go); block-carry
	// (leave_block/take_block) is now BUILT (ai_goals_enderman_carry.go, DATA_CARRY_STATE index 16); endermite
	// cite-deferred; teleport Go-native.
	{vanillaEndermanMobName, entity.Enderman.ID, 8, 2},
	// MOB-NEUT-03 (Task #9): Cat — float@1 + panic@1 + sit@2 + cat_relax_on_owner@3 + tempt@4 +
	// cat_lie_on_bed@5 + follow_owner@6 + cat_sit_on_block@7 + breed@10 + stroll@11 + look@12
	// (11 goalSelector, 0 targetSelector); prey goals (@8 leap / @9 ocelot / targetSelector) cite-deferred;
	// fish-tamed.
	{vanillaCatMobName, entity.Cat.ID, 11, 0}, // +3: cat_relax_on_owner@3, cat_lie_on_bed@5, cat_sit_on_block@7
	// MOB-PASS-06 (Task #9) + fox CHARACTER LAYER: Fox — float@0 + faceplant@1 + panic@2 + breed@3 +
	// stalk@5 + pounce@6 + seek_shelter@6 + melee@7 + sleep@7 + leap@10 + stroll@11 + search_items@11 + look@12 +
	// climb_on_powder_snow@0 + perch_search@13 (15 goalSelector) + defend_trusted@3 + land_target@4 (2 targetSelector). The
	// eat-berries/village-stroll/avoid/fish-target goals remain cite-deferred (subsystems absent).
	{vanillaFoxMobName, entity.Fox.ID, 15, 2},
	// MOB-CUBE (SulfurCube): the NEW 26.2 cube mob — the AbstractCubeMob.registerGoals CORE jump-move goals
	// (CubeMobFloatGoal@1 + CubeMobRandomDirectionGoal@4 + CubeMobKeepOnJumpingGoal@5 = 3 goalSelector, 0
	// targetSelector — addTargetingGoals is EMPTY). The SulfurCube.addBehaviourGoals TemptGoal@2 +
	// SearchForItemsGoal@3 are cite-deferred (tempt/item-pickup subsystems).
	{vanillaSulfurCubeMobName, entity.SulfurCube.ID, 3, 0},
	// happy_ghast (Task): HappyGhast — HappyGhastFloatGoal@3 + TemptGoal.ForNonPathfinders@4 (2 declared
	// observable goals, 0 targetSelector). RandomFloatAroundGoal@5 FLIGHT is HOST-NATIVE (happyGhastAiStep),
	// NOT a declared nav goal; the Brain + ride/harness + dried-ghast spawn are cite-deferred (.star header).
	{vanillaHappyGhastMobName, entity.HappyGhast.ID, 2, 0},
	// MOB-PREY (Task #9): Endermite - float@1 + climb_on_powder_snow@1 + melee@2 + stroll@3 + look@7 +
	// around@8 (6 goalSelector) + hurt_by@1 + nearest@2 (2 targetSelector); the Go-native despawn timer
	// (endermiteAiStep) is a tick hook, not a declared goal.
	{vanillaEndermiteMobName, entity.Endermite.ID, 6, 2},
	// MOB-PREY (Task #9): Turtle - panic@0 + breed@1 + layEgg@1 + tempt(turtle_food)@2 + gotoWater@3 +
	// goHome@4 + travel@7 + look@8 + stroll@9 (9 goalSelector, 0 targetSelector). The water-nav trio
	// (GoToWater/GoHome/Travel) + egg-lay are now BUILT (kind-goals -> ai_goals_turtle.go); the water-
	// traversal node evaluator is the one cited reduction. The breed hasEgg-override + prey-target legs defer.
	{vanillaTurtleMobName, entity.Turtle.ID, 9, 0},
	// MOB-PREY (Task #9): Ocelot - tempt@0 + float@1 + tempt@3 (the same instance re-added) + breed@9 +
	// stroll@10 + look@11 (6 goalSelector, 0 targetSelector); leap/attack/prey-target + trust are cite-deferred.
	{vanillaOcelotMobName, entity.Ocelot.ID, 6, 0},
}

// TestAllFourMobsBootLoad: loadVanillaMobRegistry returns ONE registry holding the passive + hostile +
// wolf declarations, each with the right base type + jar goal count. This is the T-34-10 guarantee (no
// silent missing mob) exercised directly. As of Plan 35-06 the same ONE registry ALSO boot-loads the 3
// Phase-35 hostiles (zombie/skeleton/spider) additively; Plan 36-03 adds the wolf — so the total
// declaration count is now 8 (the 4 passives + the wolf asserted here via allFourMobs + the 3 hostiles
// asserted by TestHostilesBootLoad). The exact total guards against an accidental extra/missing mob in
// vanillaMobNames.
func TestAllFourMobsBootLoad(t *testing.T) {
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	const wantTotal = 22 // + the 3 prey mobs (endermite/turtle/ocelot) added to the 19 (husk/mooshroom/silverfish/creeper/witch/rabbit/enderman/cat/fox/sulfur_cube/happy_ghast)
	if got := len(r.byName); got != wantTotal {
		t.Fatalf("registry holds %d declarations, want %d (22: passives+hostiles+variants+witch+rabbit+enderman+cat+fox+sulfur_cube+happy_ghast+endermite+turtle+ocelot)", got, wantTotal)
	}
	for _, m := range allFourMobs {
		decl, ok := r.byName[m.name]
		if !ok {
			t.Fatalf("registry missing %q declaration after boot-load (T-34-10: must fail loudly, never silently)", m.name)
		}
		if decl.baseType.ID != m.baseID {
			t.Fatalf("%s base type = %d, want %d", m.name, decl.baseType.ID, m.baseID)
		}
		// The registry decl.goals is the COMBINED declared set (goalSelector + targetSelector — the
		// split happens at spawn, plugin_mob_ai.go's flagTarget routing). So assert against the total.
		if wantTotalGoals := m.goalCount + m.targetCount; len(decl.goals) != wantTotalGoals {
			t.Fatalf("%s captured %d goals, want %d (the jar registerGoals set: %d goalSelector + %d targetSelector)", m.name, len(decl.goals), wantTotalGoals, m.goalCount, m.targetCount)
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
		// The TARGET-flag goals route into the independent targetSelector at spawn (plugin_mob_ai.go's
		// flagTarget split); every other goal lands in goals.goals. Assert BOTH lists (the wolf is the
		// first vanilla CREATURE with a targetSelector — owner-defense + anger + skeleton hunting).
		if got := len(e.ai.goals.goals); got != m.goalCount {
			t.Fatalf("spawnVanillaMob(%q) built %d goalSelector goals, want %d", m.name, got, m.goalCount)
		}
		if got := len(e.ai.targetSelector.goals); got != m.targetCount {
			t.Fatalf("spawnVanillaMob(%q) built %d targetSelector goals, want %d", m.name, got, m.targetCount)
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
		vanillaRabbitMobName:  true, // MOB-PASS-05 (Task #9): the rabbit joined the overworld CREATURE pool
	}
	seen := map[string]bool{}

	// The test goroutine has no region registered, so cur() falls back to region 0; reseed THAT
	// region's levelRandom so the draw is deterministic and we sweep the full pick space.
	for seed := int64(0); seed < 64; seed++ {
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(seed)
		for draw := 0; draw < 64; draw++ {
			name := loop.pickNaturalCreatureMob()
			if !valid[name] {
				t.Fatalf("pickNaturalCreatureMob returned %q, not one of the overworld CREATURE mobs", name)
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
