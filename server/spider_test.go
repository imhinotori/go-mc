package server

// spider_test.go — MOB-HOST-03 (Phase 35-05): the per-hostile behavior + the leap/daylight RNG tests
// for the THIRD hostile, the 1:1 vanilla Spider re-expressed as the vanilla_spider Starlark plugin
// (Spider.registerGoals: FloatGoal@1 + LeapAtTargetGoal@3 + SpiderAttackGoal@4 + stroll@5 + look@6 +
// around@6; targetSelector HurtByTargetGoal@1 + SpiderTargetGoal<Player>@2). The spider is the
// differentiated hostile: the leap impulse (the ONE goal that IMPULSES, not paths) + the daylight
// aggression gate (a spider in bright light drops its target 1-in-100/tick).
//
// Two layers, mirroring 35-01's split (the Go-native goal primitives are unit-tested directly in
// ai_goals_target_test.go; the .star is a separate boot-load gate):
//
//   - The Go-native leapAtTargetGoal + spiderAttackGoal (ai_goals_attack.go) — the CANONICAL ports,
//     exercised here with focused lockstep RNG tests (the leap nextInt(reducedTickDelay(5)=>raw 5)
//     gate, the SpiderAttackGoal daylight-flee nextInt(100), the leap setDeltaMovement impulse).
//   - The vanilla_spider .star pair — boot-loads the declared goal set (6 goalSelector + 2
//     targetSelector) with the TARGET routing.
//
// The pig oracle (TestPluginPigEqualsGoNativePig) STAYS UNTOUCHED — the spider is a separate mob, so
// its leap/daylight RNG never touches the pinned pig stream.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// --- spiderTestMob: a live spider with a deterministic per-entity rng (reseeded by id) ----------

// spiderTestMob builds a live spider mob with a goal-bearing AI + a deterministic per-entity rng
// (reseeded by id exactly as the spawn path does), so a reference entityRandom seeded identically
// reproduces its draws. Mirrors targetTestMob (ai_goals_target_test.go) for the Spider entity type.
func spiderTestMob(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Spider, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 16.0
	e.onGround = true
	return e
}

// --- TestLeapAtTargetGateRNG -------------------------------------------------------------------

// TestLeapAtTargetGateRNG pins the LeapAtTargetGoal.canUse RNG gate: with a target acquired, the mob
// on-ground, and the target in the [4.0, 16.0] distanceToSqr band, canUse draws EXACTLY ONE
// nextInt(leapReducedInterval) — the FULL raw 5 (NOT the jar's reducedTickDelay(5)=3, the same
// full-rate-tick identity rule the NearestAttackableTargetGoal gate follows). The gate "passes"
// (returns true) iff that draw == 0.
//
//	[VERIFIED javap LeapAtTargetGoal.canUse: ... getRandom().nextInt(reducedTickDelay(5)); ifeq -> true.]
func TestLeapAtTargetGateRNG(t *testing.T) {
	if leapReducedInterval != 5 {
		t.Fatalf("leapReducedInterval = %d, want 5 (the FULL DEFAULT, NOT reducedTickDelay(5)=3)", leapReducedInterval)
	}

	loop := NewTickLoop(newFakeClock())
	e := spiderTestMob(8001, 8.5, 64, 8.5)
	ref := referenceRng(e.id)

	// A target ~2 blocks away (distanceToSqr 4.0 <= d <= 16.0 — the leap band).
	p := addTestPlayer(loop, 9400, 8.5+2.0, 64, 8.5)
	e.ai.setTarget(p.entityID)

	// The reference draws the SAME nextInt(5) the gate draws (the gate value).
	gateRoll := ref.nextInt(leapReducedInterval)
	wantPassed := gateRoll == 0

	g := newLeapAtTargetGoal(0.4)
	got := g.canUse(loop, e)
	if got != wantPassed {
		t.Fatalf("canUse = %v, want %v (the leap gate passes iff nextInt(5)==0; gateRoll was %d)", got, wantPassed, gateRoll)
	}

	// LOCKSTEP: canUse consumed exactly one nextInt(5), so the mob's rng and the reference must now
	// produce the IDENTICAL next draw. A nextFloat gate or a wrong bound (3) or a double draw would desync.
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("draw desync after canUse: mob rng next=%d, reference next=%d — canUse did NOT draw exactly one nextInt(5)", a, b)
	}
}

// TestLeapGateRejectsOutOfBand: canUse returns false BEFORE the RNG gate when the target is outside
// the [4.0, 16.0] distanceToSqr band or the mob is off-ground (the jar checks the band + onGround
// BEFORE the nextInt draw). The off-band/airborne reject must draw ZERO RNG.
func TestLeapGateRejectsOutOfBand(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// (1) Too close: distanceToSqr < 4.0 -> false, NO draw.
	e := spiderTestMob(8010, 8.5, 64, 8.5)
	ref := referenceRng(e.id)
	pNear := addTestPlayer(loop, 9410, 8.5+1.0, 64, 8.5) // d² = 1.0 < 4.0
	e.ai.setTarget(pNear.entityID)
	g := newLeapAtTargetGoal(0.4)
	if g.canUse(loop, e) {
		t.Fatal("canUse must be false when distanceToSqr < 4.0 (too close to leap)")
	}
	if a, b := e.ai.rng.nextInt(7), ref.nextInt(7); a != b {
		t.Fatal("the too-close reject must draw ZERO RNG (the band check precedes the gate)")
	}

	// (2) Too far: distanceToSqr > 16.0 -> false, NO draw.
	loop2 := NewTickLoop(newFakeClock())
	e2 := spiderTestMob(8011, 8.5, 64, 8.5)
	ref2 := referenceRng(e2.id)
	pFar := addTestPlayer(loop2, 9411, 8.5+5.0, 64, 8.5) // d² = 25.0 > 16.0
	e2.ai.setTarget(pFar.entityID)
	g2 := newLeapAtTargetGoal(0.4)
	if g2.canUse(loop2, e2) {
		t.Fatal("canUse must be false when distanceToSqr > 16.0 (too far to leap)")
	}
	if a, b := e2.ai.rng.nextInt(7), ref2.nextInt(7); a != b {
		t.Fatal("the too-far reject must draw ZERO RNG (the band check precedes the gate)")
	}

	// (3) Off-ground: onGround false -> false, NO draw (the onGround check precedes the gate).
	loop3 := NewTickLoop(newFakeClock())
	e3 := spiderTestMob(8012, 8.5, 64, 8.5)
	e3.onGround = false
	ref3 := referenceRng(e3.id)
	pBand := addTestPlayer(loop3, 9412, 8.5+2.0, 64, 8.5)
	e3.ai.setTarget(pBand.entityID)
	g3 := newLeapAtTargetGoal(0.4)
	if g3.canUse(loop3, e3) {
		t.Fatal("canUse must be false when the mob is off-ground (airborne — cannot leap)")
	}
	if a, b := e3.ai.rng.nextInt(7), ref3.nextInt(7); a != b {
		t.Fatal("the airborne reject must draw ZERO RNG (the onGround check precedes the gate)")
	}
}

// --- TestLeapSetsImpulse -----------------------------------------------------------------------

// TestLeapSetsImpulse: leapAtTargetGoal.start() sets a velocity IMPULSE toward the target (the
// normalized horizontal (dx,dz)*0.4 + 0.2*existing delta, plus the vertical yd) via setDeltaMovement
// — NOT a nav path. The leap is the ONE goal exception that impulses rather than setting a want.
//
//	[VERIFIED javap LeapAtTargetGoal.start: delta = getDeltaMovement(); v = new Vec3(tx-x, 0, tz-z);
//	 if (v.lengthSqr() > 1e-7) v = v.normalize().scale(0.4).add(delta.scale(0.2));
//	 setDeltaMovement(v.x, yd, v.z).]
func TestLeapSetsImpulse(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := spiderTestMob(8020, 8.5, 64, 8.5)
	// A target straight along +X, 2 blocks away (d² = 4.0, in the leap band).
	p := addTestPlayer(loop, 9420, 8.5+2.0, 64, 8.5)
	e.ai.setTarget(p.entityID)

	const yd = 0.4
	g := newLeapAtTargetGoal(yd)
	g.target = p.entityID // canUse captures this; here we unit-test start()'s impulse in isolation.

	// Pre-leap delta is zero (a fresh spider). The horizontal direction normalizes to (1,0,0); scaled
	// by 0.4 + 0.2*0 = (0.4, _, 0.0). The vertical component is yd = 0.4.
	g.start(loop, e)

	if e.vy != yd {
		t.Fatalf("leap vy = %v, want %v (the yd vertical leap component, setDeltaMovement y = yd)", e.vy, yd)
	}
	// Horizontal toward +X: vx > 0, vz == 0 (the target is straight +X). The normalized*0.4 gives vx≈0.4.
	if e.vx <= 0 {
		t.Fatalf("leap vx = %v, want > 0 (the normalized horizontal impulse toward +X target)", e.vx)
	}
	if e.vz != 0 {
		t.Fatalf("leap vz = %v, want 0 (the target is straight +X, no Z component)", e.vz)
	}
	// The horizontal impulse magnitude is exactly 0.4 (normalize().scale(0.4), delta was zero).
	const wantVX = 0.4
	if d := e.vx - wantVX; d > 1e-9 || d < -1e-9 {
		t.Fatalf("leap vx = %v, want %v (normalize().scale(0.4) with zero prior delta)", e.vx, wantVX)
	}

	// The leap set NO nav want (it impulses, it does not path). hasTarget must be clear.
	if e.ai.hasTarget {
		t.Fatal("the leap must NOT set a nav want — it impulses via setDeltaMovement, never paths")
	}
}

// --- TestSpiderAttackDaylightGate --------------------------------------------------------------

// TestSpiderAttackDaylightGate: spiderAttackGoal (the SpiderAttackGoal port) gates on the daylight
// proxy. canContinueToUse, when it is BRIGHT (daytime — the inverse of isDarkEnoughToSpawn), draws
// nextInt(100) and on a 0 roll drops the target (setTarget(null)) and returns false; at NIGHT it never
// draws and never drops. The melee hit is therefore suppressed in daylight (the target is dropped).
//
//	[VERIFIED javap Spider$SpiderAttackGoal.canContinueToUse: br = getLightLevelDependentMagicValue();
//	 if (br >= 0.5f && getRandom().nextInt(100) == 0) { setTarget(null); return false; }
//	 return super.canContinueToUse().]
func TestSpiderAttackDaylightGate(t *testing.T) {
	// (1) NIGHT (isDarkEnoughToSpawn true): no daylight draw, target retained, canContinueToUse honors
	// the base melee continuation (a present target -> true).
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 18000 // mid-night (13000 <= 18000 < 23000) -> isDarkEnoughToSpawn true -> dark
	e := spiderTestMob(8030, 8.5, 64, 8.5)
	ref := referenceRng(e.id)
	p := addTestPlayer(loop, 9430, 8.5+1.0, 64, 8.5)
	e.ai.setTarget(p.entityID)

	g := newSpiderAttackGoal(1.0)
	if !g.canContinueToUse(loop, e) {
		t.Fatal("at night the spider must keep its target (no daylight drop) -> canContinueToUse true")
	}
	if e.ai.getTarget() != p.entityID {
		t.Fatal("at night the target must be RETAINED (no setTarget(null))")
	}
	// ZERO RNG at night (the daylight branch never runs).
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("the spider drew RNG at night: mob next=%d, ref next=%d — the daylight-flee draw must be gated on BRIGHT", a, b)
	}

	// (2) DAY (isDarkEnoughToSpawn false): the daylight branch draws nextInt(100). Drive gametime to a
	// daytime tick where the FIRST nextInt(100) is 0 so the drop fires; assert the target is dropped and
	// canContinueToUse returns false.
	loop2 := NewTickLoop(newFakeClock())
	loop2.gametime = 1000 // daytime (< 13000) -> isDarkEnoughToSpawn false -> BRIGHT
	// Find an id whose first nextInt(100) == 0 so the 1/100 drop fires deterministically.
	var dayMob *Entity
	for id := int32(8040); id < 8040+5000; id++ {
		probe := referenceRng(id)
		if probe.nextInt(100) == 0 {
			dayMob = spiderTestMob(id, 8.5, 64, 8.5)
			break
		}
	}
	if dayMob == nil {
		t.Fatal("could not find a seed whose first nextInt(100)==0 (the daylight-drop probe)")
	}
	p2 := addTestPlayer(loop2, 9440, 8.5+1.0, 64, 8.5)
	dayMob.ai.setTarget(p2.entityID)

	gDay := newSpiderAttackGoal(1.0)
	if gDay.canContinueToUse(loop2, dayMob) {
		t.Fatal("in daylight with a nextInt(100)==0 roll, the spider must DROP its target -> canContinueToUse false")
	}
	if dayMob.ai.getTarget() != 0 {
		t.Fatal("the daylight 1/100 flee must clear the target (setTarget(null))")
	}
}

// --- the vanilla_spider .star pair boot-load gate ----------------------------------------------

// loadVanillaSpiderRegistry materializes the repo-root plugins/vanilla_spider plugin into a temp dir
// and loads it through the host with the server-built declare_mob/goal builtins injected, returning the
// registry holding the captured "vanilla_spider" declaration. Mirrors loadVanillaCowRegistry. Tests run
// with cwd=server/, so the repo-root copy sits at ../plugins/vanilla_spider.
func loadVanillaSpiderRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_spider")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir spider plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "vanilla_spider", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_spider/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp spider %s: %v", name, err)
		}
	}

	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_spider): %v", err)
	}
	if _, ok := r.byName["vanilla_spider"]; !ok {
		t.Fatal("vanilla_spider declaration not captured after load")
	}
	return r
}

// spiderLoop builds a physics loop with a one-chunk stone floor and the vanilla_spider registry
// installed (replacing the default pig-only registry so spawnDeclaredMob can build a spider).
func spiderLoop(t *testing.T) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSpiderRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

// spawnSpider spawns the declared vanilla_spider via the shared spawnDeclaredMob path.
func spawnSpider(loop *TickLoop, x, y, z float64) *Entity {
	decl := loop.mobRegistry.byName["vanilla_spider"]
	return loop.spawnDeclaredMob(decl, x, y, z)
}

// --- TestSpiderBootLoads -----------------------------------------------------------------------

// TestSpiderBootLoads: the vanilla_spider plugin boot-loads, spawnSpider builds a live spider rendering
// as entity.Spider.ID with a non-nil AI holding the 6 goalSelector goals at the jar registerGoals
// indices (FloatGoal@1 + LeapAtTarget@3 + SpiderAttack@4 + stroll@5 + look@6 + around@6) and the 2
// targetSelector goals (HurtByTarget@1 + SpiderTarget@2) routed to the targetSelector. Spider
// attributes max_health 16.0 + movement_speed 0.3.
func TestSpiderBootLoads(t *testing.T) {
	loop, floorY, _ := spiderLoop(t)

	if _, ok := loop.mobRegistry.byName["vanilla_spider"]; !ok {
		t.Fatal("registry has no vanilla_spider declaration after boot-load")
	}

	spider := spawnSpider(loop, 8.5, float64(floorY+1), 8.5)
	if spider.typ != entity.Spider.ID {
		t.Fatalf("spider typ = %d, want entity.Spider.ID %d (custom = behavior)", spider.typ, entity.Spider.ID)
	}
	if spider.ai == nil {
		t.Fatal("spider has no AI")
	}

	// 6 goalSelector goals (the TARGET goals route to the targetSelector, NOT goals).
	if got := len(spider.ai.goals.goals); got != 6 {
		t.Fatalf("spider has %d goalSelector goals, want 6 (Float@1 Leap@3 SpiderAttack@4 stroll@5 look@6 around@6)", got)
	}
	// 2 targetSelector goals (HurtByTarget@1 + SpiderTarget@2).
	if got := len(spider.ai.targetSelector.goals); got != 2 {
		t.Fatalf("spider has %d targetSelector goals, want 2 (HurtByTarget@1 + SpiderTarget@2)", got)
	}

	// The goalSelector priorities are the jar Spider.registerGoals indices (1,3,4,5,6,6).
	seen := map[int]int{}
	for _, wg := range spider.ai.goals.goals {
		seen[wg.priority]++
	}
	if seen[1] != 1 || seen[3] != 1 || seen[4] != 1 || seen[5] != 1 || seen[6] != 2 {
		t.Fatalf("spider goalSelector priorities = %v, want {1:1, 3:1, 4:1, 5:1, 6:2}", seen)
	}

	// Attributes seeded from the declaration (Spider.createAttributes: max_health 16.0, movement_speed 0.3).
	if mh := spider.attributes.GetValue(attribute.MaxHealth.Name()); mh != 16.0 {
		t.Fatalf("spider max_health = %v, want 16.0 (Spider.createAttributes MAX_HEALTH)", mh)
	}
	if ms := spider.attributes.GetValue(attribute.MovementSpeed.Name()); ms != 0.3 {
		t.Fatalf("spider movement_speed = %v, want 0.3 (Spider.createAttributes MOVEMENT_SPEED)", ms)
	}

	if _, ok := loop.only().entities.get(spider.id); !ok {
		t.Fatal("spawnSpider did not add the spider to the tick-owned store")
	}
}

// --- TestSpiderBehavior ------------------------------------------------------------------------

// TestSpiderBehavior: a spawned spider is a MONSTER (categoryOf -> categoryMonster; the jar
// EntityType.SPIDER is MobCategory.MONSTER) and its AI ticks without error over many ticks (the shared
// goals + the leap/daylight goals drive it). A smoke drive that the spider is a live, ticking mob.
func TestSpiderBehavior(t *testing.T) {
	loop, floorY, clock := spiderLoop(t)

	if got := categoryOf(entity.Spider.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Spider) = %v, want categoryMonster (vanilla EntityType.SPIDER is MobCategory.MONSTER)", got)
	}

	spider := spawnSpider(loop, 8.5, float64(floorY+1), 8.5)
	spider.onGround = true
	if spider.ai == nil {
		t.Fatal("spider has no AI to drive")
	}

	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
	}
	if _, ok := loop.only().entities.get(spider.id); !ok {
		t.Fatal("the spider vanished from the store after the drive")
	}
	if spider.typ != entity.Spider.ID {
		t.Fatalf("spider typ drifted to %d, want entity.Spider.ID %d", spider.typ, entity.Spider.ID)
	}
}
