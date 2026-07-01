package server

// wolf_test.go — THE PHASE-36 GATE (Plan 36-04, SC#1/SC#2/SC#3 of MOB-NEUT-01/02): the holistic wolf
// behavior + boot-load + focused-RNG test suite for the LAST v5 mob, the 1:1 vanilla Wolf
// (net.minecraft.world.entity.animal.wolf.Wolf) re-expressed as the vanilla_wolf Starlark plugin (the
// 15-goal IN-SCOPE registerGoals set: 10 goalSelector + 5 targetSelector). It asserts the phase goal
// HOLISTICALLY from the REAL //go:embed boot-load:
//
//   - TestWolfBootLoads     — the embedded wolf registry loads; the wolf spawns as entity.Wolf.ID,
//                             CREATURE, MAX_HEALTH 8.0 / ATTACK_DAMAGE 4.0, with 10 goalSelector + 5
//                             targetSelector goals.
//   - TestWolfTame          — a BONE on an untamed non-angry wolf draws ONE nextInt(3); on 0 → tame +
//                             HP 8→40 + owner + orderedToSit; on !=0 → smoke, bone still consumed.
//   - TestWolfSit           — a tamed orderedToSit wolf parks (SitWhenOrderedToGoal claims MOVE/JUMP,
//                             inSittingPose, navigation stopped).
//   - TestWolfFollowOwner   — a tamed wolf beyond 10² paths toward its owner; within 2² it stops;
//                             orderedToSit suppresses follow (the sit goal claims MOVE at higher prio).
//   - TestWolfWildNoAggro   — a WILD UN-HIT wolf with a player in range acquires NO player target (the
//                             isAngryAt gate holds — wolves are NOT aggressive on sight). THE must-have.
//   - TestWolfAngerOnHit    — a wild wolf HIT by a player sets angerEndTime = gameTime + 400 + nextInt(381)
//                             + angerTarget = attacker; the angry_player_target goal THEN acquires the
//                             player; advancing past angerEndTime makes isAngry false (the expiry).
//   - TestWolfSkeletonTarget— a wild wolf with a skeleton in FOLLOW_RANGE acquires it (skeleton_target,
//                             no anger gate needed).
//   - TestWolfTameRNG / TestWolfAngerRNG — focused lockstep draw-order tests pinning nextInt(3) (tame)
//                             and 400 + nextInt(381) (anger) against a seeded mobRandom stream.
//
// THE PIG ORACLE (TestPluginPigEqualsGoNativePig) IS A SEPARATE, UNTOUCHED MOB — these build wolves
// only, so the pinned pig stream is unperturbed. The Phase-35 hostile/target tests
// (TestNearestAttackableTargetGateUsesTen et al.) stay green: the B1/B2 parameterization is additive
// (the bare nearest_attackable_target is byte-identical; only the wolf's angerGate/skeleton branch
// differs).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// --- wolf registry test harness -----------------------------------------------------------------

// loadVanillaWolfRegistry materializes the repo-root plugins/vanilla_wolf plugin into a temp dir and
// loads it through the host with the server-built declare_mob/goal builtins injected, returning the
// registry holding the captured "vanilla_wolf" declaration. Mirrors loadVanillaCowRegistry /
// loadVanillaSkeletonRegistry exactly: the plugin is read from the canonical repo-root copy (the
// byte-identical sibling of the server/assets embed). Tests run with cwd=server/, so the repo-root copy
// sits at ../plugins/vanilla_wolf.
func loadVanillaWolfRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_wolf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir wolf plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_wolf", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_wolf/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp wolf %s: %v", name, err)
		}
	}

	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_wolf): %v", err)
	}
	if _, ok := r.byName["vanilla_wolf"]; !ok {
		t.Fatal("vanilla_wolf declaration not captured after load")
	}
	return r
}

// wolfLoop builds a physics loop with a one-chunk stone floor and the vanilla_wolf registry installed
// (replacing the default pig-only registry so spawnDeclaredMob can build a wolf). Returns the loop, the
// floor Y, and the fake clock (for the behavior/expiry drive). Mirrors cowLoop / skeletonLoop.
func wolfLoop(t *testing.T) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaWolfRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

// spawnWolf spawns the declared vanilla_wolf via the shared spawnDeclaredMob path (real Wolf attrs +
// the 10 goalSelector + 5 targetSelector goals + a per-entity RNG stream reseeded by id). It is added to
// the tick-owned store, so the target scans (nearestEntityOfTypeAt) can see it and other store entities.
func spawnWolf(loop *TickLoop, x, y, z float64) *Entity {
	decl := loop.mobRegistry.byName["vanilla_wolf"]
	return loop.spawnDeclaredMob(decl, x, y, z)
}

// targetGoalOfType finds the wolf's targetSelector goal of the concrete *nearestAttackableTargetGoal
// shape matching the wanted targetClass (PLAYER for angry_player_target, SKELETON for skeleton_target),
// so a test can drive THE REAL boot-loaded goal (with its angerGate wired) rather than a hand-built one.
// Returns nil if absent (a Fatal-worthy miss in the caller).
func targetGoalOfType(e *Entity, class nearestTargetClass) *nearestAttackableTargetGoal {
	if e.ai == nil {
		return nil
	}
	for _, wg := range e.ai.targetSelector.goals {
		if g, ok := wg.g.(*nearestAttackableTargetGoal); ok && g.targetClass == class {
			return g
		}
	}
	return nil
}

// --- TestWolfBootLoads --------------------------------------------------------------------------

// TestWolfBootLoads: the vanilla_wolf plugin boot-loads from the real //go:embed registry
// (loadVanillaMobRegistry) AND the temp-dir harness; spawnWolf builds a live wolf rendering as
// entity.Wolf.ID with a non-nil AI holding the 10 goalSelector + 5 targetSelector goals at the jar
// registerGoals indices, the CREATURE category, and Wolf.createAttributes (MAX_HEALTH 8.0, ATTACK_DAMAGE
// 4.0, MOVEMENT_SPEED 0.30000001192092896). This is the holistic boot-load gate the prior plans deferred.
func TestWolfBootLoads(t *testing.T) {
	// (1) The REAL //go:embed boot-load: the shipped binary's ONE registry holds the wolf.
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry (the real //go:embed boot-load): %v", err)
	}
	if _, ok := r.byName[vanillaWolfMobName]; !ok {
		t.Fatalf("boot-loaded registry has no %q declaration — the embed directive + vanillaMobNames did not wire the wolf in", vanillaWolfMobName)
	}

	// (2) Spawn the wolf through the temp-dir harness registry and assert the holistic shape.
	loop, floorY, _ := wolfLoop(t)

	if _, ok := loop.mobRegistry.byName["vanilla_wolf"]; !ok {
		t.Fatal("registry has no vanilla_wolf declaration after boot-load")
	}

	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	if wolf.typ != entity.Wolf.ID {
		t.Fatalf("wolf typ = %d, want entity.Wolf.ID %d (custom = behavior, not a new wire type)", wolf.typ, entity.Wolf.ID)
	}
	if wolf.ai == nil {
		t.Fatal("wolf has no AI")
	}

	// The Mob.registerGoals flagTarget split: 10 goalSelector + 5 targetSelector (the 5 cited+omitted
	// deferrals — WolfAvoidEntityGoal@3, BegGoal@9, NonTameRandomTarget@5/@6, ResetUniversalAnger@8 —
	// are NOT counted; they ship with their not-yet-built targets/subsystems).
	if got := len(wolf.ai.goals.goals); got != 10 {
		t.Fatalf("wolf has %d goalSelector goals, want 10 (float@1 panic@1 sit@2 leap@4 melee@5 follow_owner@6 breed@7 stroll@8 look@10 around@10)", got)
	}
	if got := len(wolf.ai.targetSelector.goals); got != 5 {
		t.Fatalf("wolf has %d targetSelector goals, want 5 (owner_hurt_by@1 owner_hurt@2 hurt_by_target@3 angry_player_target@4 skeleton_target@7)", got)
	}

	// The wolf is the FIRST vanilla CREATURE with a populated targetSelector. Its target goals are the
	// PARAMETERIZED kinds (angry_player_target gated on isAngryAt + skeleton_target), NEVER the bare
	// un-gated nearest_attackable_target the hostiles use (that one would aggro players on sight).
	if targetGoalOfType(wolf, targetClassPlayer) == nil {
		t.Fatal("wolf has no PLAYER-class target goal (the @4 angry_player_target — the anger-gated NearestAttackableTargetGoal<Player>)")
	}
	if g := targetGoalOfType(wolf, targetClassPlayer); g.angerGate == nil {
		t.Fatal("the wolf @4 player target goal has a NIL angerGate — it would aggro players on sight (must be isAngryAt-gated)")
	}
	if targetGoalOfType(wolf, targetClassSkeleton) == nil {
		t.Fatal("wolf has no SKELETON-class target goal (the @7 skeleton_target — wolves hunt skeletons; NOT deferred)")
	}

	// Wolf.createAttributes: MAX_HEALTH 8.0 (untamed base), ATTACK_DAMAGE 4.0, CREATURE category.
	if mh := wolf.attributes.GetValue(attribute.MaxHealth.Name()); mh != 8.0 {
		t.Fatalf("wolf max_health = %v, want 8.0 (Wolf.createAttributes untamed base)", mh)
	}
	if ad := wolf.attributes.GetValue(attribute.AttackDamage.Name()); ad != 4.0 {
		t.Fatalf("wolf attack_damage = %v, want 4.0 (Wolf.createAttributes)", ad)
	}
	if cat := categoryOf(entity.Wolf.ID); cat != categoryCreature {
		t.Fatalf("categoryOf(Wolf) = %v, want categoryCreature (vanilla EntityType.WOLF is MobCategory.CREATURE)", cat)
	}

	if _, ok := loop.only().entities.get(wolf.id); !ok {
		t.Fatal("spawnWolf did not add the wolf to the tick-owned store")
	}
}

// --- TestWolfTame -------------------------------------------------------------------------------

// TestWolfTame: feeding a BONE to an untamed non-angry wolf draws EXACTLY one nextInt(3). On a forced-0
// stream the wolf becomes tame (isTame), MAX_HEALTH bumps 8→40 + full heal, owner is set, orderedToSit
// becomes true, and the bone is consumed. On a forced-!=0 stream it stays untamed (smoke), but the bone
// is STILL consumed. Ports Wolf.mobInteract (UNTAMED BONE branch) + Wolf.tryToTame end-to-end through the
// boot-loaded spawn (the wolf carries its real per-entity rng — the seed is overridden for determinism).
func TestWolfTame(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)

	// (A) tame SUCCESS: force the wolf's rng to roll nextInt(3) == 0.
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	wolf.ai.rng = newEntityRandom(wolfTameSuccessSeed)
	if r := newEntityRandom(wolfTameSuccessSeed).nextInt(3); r != 0 {
		t.Fatalf("precondition: wolfTameSuccessSeed nextInt(3) = %d, want 0", r)
	}
	p := newTestPlayerHolding(loop, 42001, int32(item.Bone.ID))

	if !loop.tryWolfInteract(p, wolf) {
		t.Fatal("tryWolfInteract on a BONE-fed untamed wolf must return true (interact consumed)")
	}
	if !wolf.tame {
		t.Fatal("wolf.tame = false after a tame-success BONE feed, want true (isTame)")
	}
	if wolf.health != 40.0 {
		t.Fatalf("tamed wolf health = %v, want 40.0 (applyTamingSideEffects full heal)", wolf.health)
	}
	if mh := wolf.getAttributeValue(attribute.MaxHealth); mh != 40.0 {
		t.Fatalf("tamed wolf MAX_HEALTH = %v, want 40.0 (setBaseValue(40.0) 8→40 bump)", mh)
	}
	if wolf.ownerUUID != p.entityID {
		t.Fatalf("tamed wolf ownerUUID = %d, want %d (setOwner)", wolf.ownerUUID, p.entityID)
	}
	if !wolf.orderedToSit {
		t.Fatal("tamed wolf orderedToSit = false, want true (tryToTame setOrderedToSit(true))")
	}
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("held slot after tame = %+v, want empty (stack.consume(1) ate the bone)", held)
	}

	// (B) tame FAILURE: force nextInt(3) != 0 → smoke, untamed, but bone STILL consumed.
	wolf2 := spawnWolf(loop, 9.5, float64(floorY+1), 9.5)
	wolf2.ai.rng = newEntityRandom(wolfTameFailSeed)
	if r := newEntityRandom(wolfTameFailSeed).nextInt(3); r == 0 {
		t.Fatal("precondition: wolfTameFailSeed nextInt(3) = 0, want != 0")
	}
	p2 := newTestPlayerHolding(loop, 42002, int32(item.Bone.ID))

	if !loop.tryWolfInteract(p2, wolf2) {
		t.Fatal("tryWolfInteract on a BONE feed must return true even on a tame fail (the bone is eaten)")
	}
	if wolf2.tame {
		t.Fatal("wolf2.tame = true after a tame-FAIL feed, want false (smoke branch)")
	}
	if wolf2.health != 8.0 {
		t.Fatalf("tame-fail wolf health = %v, want 8.0 (no applyTamingSideEffects)", wolf2.health)
	}
	if wolf2.ownerUUID != 0 {
		t.Fatalf("tame-fail wolf ownerUUID = %d, want 0 (no setOwner)", wolf2.ownerUUID)
	}
	if held := ensureInventory(p2).get(heldWindowSlot(ensureInventory(p2).heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("held slot after tame-fail = %+v, want empty (the bone is consumed before tryToTame)", held)
	}
}

// --- TestWolfSit --------------------------------------------------------------------------------

// TestWolfSit: a tamed orderedToSit wolf's SitWhenOrderedToGoal parks it — canUse is true (the goal
// claims MOVE/JUMP), start() sets inSittingPose true + stops the navigation. Ported SitWhenOrderedToGoal
// (the @2 goalSelector goal). The wolf is on the ground, not in water, ownerless-resolution returns true.
func TestWolfSit(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	wolf.onGround = true
	wolf.tame = true
	wolf.orderedToSit = true

	g := newSitWhenOrderedToGoal()
	if !g.canUse(loop, wolf) {
		t.Fatal("SitWhenOrderedToGoal.canUse on a tamed orderedToSit grounded wolf must be true (it parks the wolf)")
	}
	// The goal claims MOVE + JUMP (so a sitting wolf neither wanders nor jumps).
	if f := g.flags(); f&flagMove == 0 || f&flagJump == 0 {
		t.Fatalf("sit goal flags = %b, want MOVE+JUMP claimed (a sitting wolf is parked)", f)
	}

	g.start(loop, wolf)
	if !wolf.inSittingPose {
		t.Fatal("after start() the wolf is not inSittingPose — setInSittingPose(true) did not fire")
	}
	if !g.canContinueToUse(loop, wolf) {
		t.Fatal("canContinueToUse on a still-ordered wolf must be true (isOrderedToSit)")
	}

	// Un-order the sit: the goal stops claiming the wolf, and stop() clears the pose.
	wolf.orderedToSit = false
	if g.canContinueToUse(loop, wolf) {
		t.Fatal("canContinueToUse on a no-longer-ordered wolf must be false (the sit-toggle released it)")
	}
	g.stop(loop, wolf)
	if wolf.inSittingPose {
		t.Fatal("after stop() the wolf is still inSittingPose — setInSittingPose(false) did not fire")
	}
}

// --- TestWolfFollowOwner ------------------------------------------------------------------------

// TestWolfFollowOwner: a tamed wolf beyond startDistance² (10² = 100) from its owner STARTS following
// (canUse true) and tick() issues a nav want toward the owner; a wolf within stopDistance² (2² = 4) does
// NOT (canUse false: too close). Ports FollowOwnerGoal (the @6 goalSelector goal). The owner is a present
// player resolved via ownerUUID.
func TestWolfFollowOwner(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	wolf.onGround = true
	wolf.tame = true
	wolf.ownerUUID = 51000

	g := newFollowOwnerGoal(1.0)

	// (1) Owner FAR (>10 blocks away): canUse true, and tick sets a nav want toward the owner.
	owner := newTestPlayerHolding(loop, 51000, -1) // empty hand; positioned next
	owner.x, owner.y, owner.z = wolf.x+20, wolf.y, wolf.z // 20 blocks east → distSqr 400 > 100
	if !g.canUse(loop, wolf) {
		t.Fatal("FollowOwnerGoal.canUse with the owner 20 blocks away must be true (distSqr 400 > startDistance² 100)")
	}
	g.start(loop, wolf)
	// Drive tick once: the recalc countdown starts at 0 → on the first tick it issues the moveTo want.
	g.tick(loop, wolf)
	if wolf.ai == nil || !wolf.ai.hasTarget {
		t.Fatal("FollowOwnerGoal.tick did not issue a nav want toward the far owner (navigation.moveTo set hasTarget)")
	}

	// (2) Owner CLOSE (within 2 blocks): canUse false (too close to bother following).
	owner.x, owner.y, owner.z = wolf.x+1, wolf.y, wolf.z // 1 block away → distSqr 1 < stopDistance² 4
	if g.canUse(loop, wolf) {
		t.Fatal("FollowOwnerGoal.canUse with the owner 1 block away must be false (distSqr 1 < startDistance² 100)")
	}
	if g.canContinueToUse(loop, wolf) {
		t.Fatal("FollowOwnerGoal.canContinueToUse with the owner inside stopDistance² must be false (arrived)")
	}
}

// --- TestWolfWildNoAggro (THE not-aggressive-on-sight must-have) --------------------------------

// TestWolfWildNoAggro: a WILD UN-HIT wolf with a player WELL INSIDE FOLLOW_RANGE acquires NO player
// target — the @4 angry_player_target goal's isAngryAt gate holds (angerEndTime == 0 → isAngry false →
// the candidate is dropped). The RNG gate is FORCED (forceTrigger), so the ONLY thing that can block the
// target is the anger gate — proving a wild wolf is NOT aggressive on sight (the core "neutral" property).
func TestWolfWildNoAggro(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)

	// Precondition: the wolf is wild (untamed, un-hit). angerEndTime == 0 → isAngry false.
	if wolf.angerEndTime != 0 {
		t.Fatalf("precondition: a fresh wild wolf must have angerEndTime 0, got %d", wolf.angerEndTime)
	}

	// A player ~1 block away — well inside the wolf's FOLLOW_RANGE.
	follow := wolf.getAttributeValue(attribute.FollowRange)
	if follow <= 0 {
		t.Fatalf("wolf FOLLOW_RANGE = %v, want > 0", follow)
	}
	p := addTestPlayer(loop, 60000, wolf.x, wolf.y, wolf.z+1)

	// Drive the REAL boot-loaded angry_player_target goal (with its isAngryAt angerGate wired).
	g := targetGoalOfType(wolf, targetClassPlayer)
	if g == nil {
		t.Fatal("wolf has no PLAYER-class target goal to drive")
	}
	if g.angerGate == nil {
		t.Fatal("the wolf player target goal must carry the isAngryAt angerGate (else it aggros on sight)")
	}
	g.forceTrigger = true // bypass the RNG GATE so ONLY the anger gate can block — isolate the must-have

	if g.canUse(loop, wolf) {
		t.Fatalf("a WILD UN-HIT wolf ACQUIRED the in-range player (target %d) — wolves must NOT aggro on sight (the isAngryAt gate failed to hold)", g.target)
	}
	if g.target != 0 {
		t.Fatalf("the wild wolf's target = %d, want 0 (the anger gate dropped the candidate)", g.target)
	}
	if wolf.ai.getTarget() != 0 {
		t.Fatalf("the wild wolf committed a target %d, want 0 (no aggro on sight)", wolf.ai.getTarget())
	}
	// Sanity: the player IS in range — so range is NOT what blocked it; only the anger gate did. Prove it
	// by confirming the SAME scan with the anger gate removed WOULD have acquired the player.
	g2 := newNearestAttackableTargetGoal() // the bare (un-gated) goal — the hostile shape
	g2.forceTrigger = true
	if !g2.canUse(loop, wolf) || g2.target != p.entityID {
		t.Fatalf("the in-range player is NOT acquirable even un-gated (target %d) — the no-aggro proof is vacuous; the player must be in range", g2.target)
	}
}

// --- TestWolfAngerOnHit (THE anger-on-hit must-have) --------------------------------------------

// TestWolfAngerOnHit: a wild wolf HIT by a player sets angerEndTime = gameTime + 400 + nextInt(381) (one
// draw; the offset in [400,780]) + angerTarget = the attacker; isAngryAt(attacker) becomes true; the @4
// angry_player_target goal THEN acquires the player. Advancing the clock past angerEndTime makes isAngry
// false again (the gametime-endpoint expiry — NO decrement, NO ResetUniversalAnger). Drives the hit
// through the combat_mob.go store-point (applyDamageEntity, the same path the HurtByTargetGoal reads).
func TestWolfAngerOnHit(t *testing.T) {
	loop, floorY, clock := wolfLoop(t)
	loop.gametime = 1000 // a non-zero gametime so the endpoint math is unambiguous
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	// Force the wolf's rng so the anger offset is deterministic: capture the SAME nextInt(381) a
	// reference draws, then assert angerEndTime == gameTime + 400 + that draw.
	wolf.ai.rng = newEntityRandom(wolfAngerSeed)
	wantOffset := 400 + newEntityRandom(wolfAngerSeed).nextInt(381)
	if wantOffset < 400 || wantOffset > 780 {
		t.Fatalf("precondition: anger offset %d out of [400,780] — UniformInt(400,780) = 400 + nextInt(381)", wantOffset)
	}

	// A player ~1 block away (in FOLLOW_RANGE), the attacker.
	attacker := addTestPlayer(loop, 61000, wolf.x, wolf.y, wolf.z+1)

	// Precondition: a wild wolf does NOT aggro the player before the hit (the no-aggro gate holds).
	pre := targetGoalOfType(wolf, targetClassPlayer)
	pre.forceTrigger = true
	if pre.canUse(loop, wolf) {
		t.Fatal("precondition: the wild wolf aggroed the player BEFORE the hit — the anger gate should hold")
	}

	// THE HIT: drive a player melee through the combat store-point (the same path applyDamageEntity takes
	// for a real attack). This sets lastHurtByMob AND (wolf-gated, player-attacker-gated) the anger timer.
	loop.applyDamageEntity(wolf, damageSourcePlayerAttack(attacker.entityID), 1.0)

	// The gametime-endpoint anger: angerEndTime == gameTime + 400 + nextInt(381); angerTarget == attacker.
	wantEnd := int64(1000) + int64(wantOffset)
	if wolf.angerEndTime != wantEnd {
		t.Fatalf("angerEndTime = %d, want %d (gameTime 1000 + 400 + nextInt(381) = 1000 + %d)", wolf.angerEndTime, wantEnd, wantOffset)
	}
	if wolf.angerTarget != attacker.entityID {
		t.Fatalf("angerTarget = %d, want %d (the attacking player)", wolf.angerTarget, attacker.entityID)
	}
	if !isAngryAt(loop, wolf, attacker.entityID) {
		t.Fatal("isAngryAt(attacker) is false after the hit — the wolf is not angry at its attacker")
	}

	// THE ANGER-ON-HIT ACQUIRE: the angry_player_target goal now acquires the player (the gate opened).
	g := targetGoalOfType(wolf, targetClassPlayer)
	g.forceTrigger = true
	if !g.canUse(loop, wolf) {
		t.Fatal("after the hit the angry_player_target goal did NOT acquire the attacker — anger-on-hit failed")
	}
	if g.target != attacker.entityID {
		t.Fatalf("post-hit acquired target = %d, want the attacker %d", g.target, attacker.entityID)
	}
	g.start(loop, wolf)
	if wolf.ai.getTarget() != attacker.entityID {
		t.Fatalf("start() committed target %d, want the attacker %d", wolf.ai.getTarget(), attacker.entityID)
	}

	// THE EXPIRY (gametime-endpoint, no decrement): advance the clock PAST angerEndTime → isAngry false.
	// loop.advance ticks gametime forward; drive it past wantEnd.
	for loop.gametime <= wantEnd {
		clock.add(tickStep)
		loop.advance(clock.Now())
	}
	if isAngryAt(loop, wolf, attacker.entityID) {
		t.Fatalf("isAngryAt still true at gametime %d > angerEndTime %d — the anger did not expire (the gametime-endpoint failed)", loop.gametime, wantEnd)
	}
}

// --- TestWolfSkeletonTarget ---------------------------------------------------------------------

// TestWolfSkeletonTarget: a wild wolf with a skeleton (entity.Skeleton.ID) spawned in FOLLOW_RANGE
// acquires it via the @7 skeleton_target goal (the nearestEntityOfTypeAt scan) — NO anger gate needed
// (wolves attack skeletons on sight). Drives the REAL boot-loaded skeleton_target goal (targetClass
// SKELETON, angerGate nil).
func TestWolfSkeletonTarget(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)

	// A skeleton ~1 block away — well inside FOLLOW_RANGE. A bare *Entity in the store (the scan reads it).
	skel := NewEntity(70000, entity.Skeleton, wolf.x, wolf.y, wolf.z+1)
	skel.health = 20.0
	loop.only().entities.add(skel)

	g := targetGoalOfType(wolf, targetClassSkeleton)
	if g == nil {
		t.Fatal("wolf has no SKELETON-class target goal to drive (the @7 skeleton_target)")
	}
	if g.angerGate != nil {
		t.Fatal("the skeleton_target goal must have a NIL angerGate (wolves attack skeletons on sight, no anger needed)")
	}
	g.forceTrigger = true // bypass the RNG gate so the scan runs deterministically

	if !g.canUse(loop, wolf) {
		t.Fatal("the wolf did NOT acquire the in-range skeleton (skeleton_target scan failed)")
	}
	if g.target != skel.id {
		t.Fatalf("acquired target = %d, want the skeleton id %d", g.target, skel.id)
	}
	g.start(loop, wolf)
	if wolf.ai.getTarget() != skel.id {
		t.Fatalf("start() committed target %d, want the skeleton %d", wolf.ai.getTarget(), skel.id)
	}

	// A skeleton OUTSIDE follow range is NOT acquired (the bound is faithful).
	loop2, floorY2, _ := wolfLoop(t)
	wolf2 := spawnWolf(loop2, 8.5, float64(floorY2+1), 8.5)
	follow := wolf2.getAttributeValue(attribute.FollowRange)
	far := NewEntity(70001, entity.Skeleton, wolf2.x, wolf2.y, wolf2.z+follow+10)
	far.health = 20.0
	loop2.only().entities.add(far)
	g2 := targetGoalOfType(wolf2, targetClassSkeleton)
	g2.forceTrigger = true
	if g2.canUse(loop2, wolf2) {
		t.Fatalf("the wolf acquired a skeleton beyond FOLLOW_RANGE (%v) — the distance bound is broken", follow)
	}
}

// --- TestWolfTameRNG (focused tame draw-order) --------------------------------------------------

// TestWolfTameRNG: tryToTameWolf draws EXACTLY ONE nextInt(3) (the tame roll). Proven by lockstep with a
// reference rng seeded identically: the reference draws ONE nextInt(3); after the feed, the wolf's rng
// and the reference must agree on the NEXT draw iff tryToTame consumed exactly one nextInt(3). A second
// draw (or a different bound) would desync the follow-up. This is the focused-RNG must-have for tame.
func TestWolfTameRNG(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)
	const seed = wolfTameFailSeed // a fail seed keeps the wolf untamed (no side effects to perturb the stream)
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	wolf.ai.rng = newEntityRandom(seed)
	p := newTestPlayerHolding(loop, 62000, int32(item.Bone.ID))

	ref := newEntityRandom(seed)
	_ = ref.nextInt(3) // the ONE tame roll tryToTame draws

	loop.tryWolfInteract(p, wolf)

	const probeBound = 1_000_000
	if got, want := mobRandom(wolf).nextInt(probeBound), ref.nextInt(probeBound); got != want {
		t.Fatalf("post-feed rng desync: wolf nextInt(%d)=%d, reference=%d — tryToTame did NOT draw exactly one nextInt(3)", probeBound, got, want)
	}
}

// --- TestWolfAngerRNG (focused anger draw-order) ------------------------------------------------

// TestWolfAngerRNG: the anger trigger draws EXACTLY ONE nextInt(381), and angerEndTime == gameTime + 400
// + that draw (UniformInt(400,780).sample = 400 + nextInt(381)). Proven both by the explicit endpoint
// value AND by lockstep: a reference seeded identically draws ONE nextInt(381); after the hit, the wolf's
// rng and the reference agree on the NEXT draw iff the trigger consumed exactly one nextInt(381). This is
// the focused-RNG must-have for anger (the lockstep discipline even without a full oracle).
func TestWolfAngerRNG(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)
	loop.gametime = 500
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	wolf.ai.rng = newEntityRandom(wolfAngerSeed)
	attacker := addTestPlayer(loop, 63000, wolf.x, wolf.y, wolf.z+1)

	// The reference draws the SAME one nextInt(381) the anger trigger draws FIRST on the wolf's stream.
	ref := newEntityRandom(wolfAngerSeed)
	wantDraw := ref.nextInt(381)
	wantEnd := int64(500) + int64(400+wantDraw)

	loop.applyDamageEntity(wolf, damageSourcePlayerAttack(attacker.entityID), 1.0)

	// (1) THE BOUND + THE SINGLE ANGER DRAW: angerEndTime == gameTime + 400 + nextInt(381). Because `ref`
	// is a FRESH stream and its FIRST draw is the nextInt(381) above, this equality proves the anger
	// trigger drew exactly nextInt(381) as the FIRST draw on the wolf's stream with the correct [400,780]
	// bound (UniformInt(400,780).sample = 400 + nextInt(381)). A wrong bound or a pre-anger draw would
	// shift the endpoint off wantEnd.
	if wolf.angerEndTime != wantEnd {
		t.Fatalf("angerEndTime = %d, want %d (500 + 400 + nextInt(381)=%d) — the anger draw bound/order is wrong", wolf.angerEndTime, wantEnd, wantDraw)
	}
	// (2) THE LOCKSTEP continuation through the FULL hit: after the anger draw, LivingEntity.hurtServer's
	// survival branch plays the HURT SOUND, whose getVoicePitch draws (nextFloat() - nextFloat()) from the
	// SAME mob stream (combat_mob.go playMobHurtSound). So the wolf's post-hit draw order is:
	// nextInt(381) [anger] → nextFloat(), nextFloat() [hurt-sound pitch]. The reference replays that EXACT
	// order; the next draw must then agree iff the anger trigger consumed exactly one nextInt(381) (no
	// extra/missing anger draw) AND the bound was 381 — a wrong bound desyncs the whole tail.
	_ = ref.nextFloat() // hurt-sound getVoicePitch nextFloat() #1
	_ = ref.nextFloat() // hurt-sound getVoicePitch nextFloat() #2
	const probeBound = 1_000_000
	if got, want := mobRandom(wolf).nextInt(probeBound), ref.nextInt(probeBound); got != want {
		t.Fatalf("post-hit rng desync: wolf nextInt(%d)=%d, reference=%d — the anger trigger + hurt-sound did NOT draw the expected stream (one nextInt(381) then two nextFloat())", probeBound, got, want)
	}
}

// wolfAngerSeed is an arbitrary fixed seed for the anger-draw tests; its nextInt(381) is whatever the
// stream yields (the tests read it from a reference rng, so no magic value is hard-coded). wolfTameSuccessSeed
// / wolfTameFailSeed are defined in wolf_taming_test.go (the 36-02 sibling) and reused here.
const wolfAngerSeed uint64 = 0xA17E
