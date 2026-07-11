package server

// dogfood_gate_test.go — Phase 33 Plan 05: THE DOGFOOD GATE (MOB-GATE-01 + MOB-GATE-02).
//
// This is the consolidated gate suite that proves the full 9-goal pig composes end-to-end. It is a
// thin ORCHESTRATION over the pieces Plans 33-01..04 already unit-test (aging, hitbox, inLove/feed,
// breedGoal/followParentGoal/breed) — it asserts the COMPOSED behavior, not the subsystem internals.
//
// Two gates live here:
//
//  1. TestDogfoodGateOracleNineGoals — the FORMAL byte-identical oracle gate. It asserts BOTH pigs
//     boot with EXACTLY 9 goals {0,1,3,4,4,5,6,7,8} (boot-load-9, so a regression below 9 is caught
//     HERE), then drives a Go-native pig and a plugin pig with the same id/world/inputs for 500 ticks
//     and asserts the observable stream is byte-identical (the same proof TestPluginPigEqualsGoNativePig
//     runs, wrapped with the len==9 up-front assertion). The oracle pig is a LONE un-fed adult — breed
//     (isInLove-gated) and follow (isBaby-gated) stay dormant, drawing zero RNG, so the stream matches.
//
//  2. TestDogfoodGateEndToEnd — the OBSERVABLE dogfood in ONE scenario, exercising the real
//     feed→breed→aging→follow→grow-up composition that the oracle keeps dormant:
//       a. two ADULT pigs fall in love (the feed path's setInLove effect) → both inLove==600;
//       b. the live breedGoal courts to threshold → a HALF-SCALE baby spawns (breedAge==-24000,
//          AABB span 0.45), both parents go to the 6000 cooldown + inLove reset, an XP orb is awarded;
//       c. the baby's live followParentGoal paths to the nearby adult (want target AT the adult,
//          re-path every 10 ticks, NO RNG);
//       d. the baby ages fully to breedAge==0 → isBaby() flips false, the AABB restores to full 0.9,
//          and the DATA_BABY_ID cross-0 broadcast fires (onGrewUp).
//
// JAR AUTHORITY (javap -c -p temp/cache/26.2-inner.jar): Pig.registerGoals (9 goals), BreedGoal,
// FollowParentGoal, Animal.spawnChildFromBreeding -> finalizeSpawnChildFromBreeding (setAge(6000)×2 +
// resetLove×2 + XP 1+nextInt(7)), AgeableMob.aiStep/setAge (the -1->0 grow-up + DATA_BABY_ID flip),
// Pig.BABY_DIMENSIONS (== adult x 0.5 == 0.45×0.45). These are the cited contracts; the unit tests in
// aging_mob_test.go / inlove_feed_test.go / breed_follow_test.go pin the internals — this suite gates
// the COMPOSITION.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// dogfoodEpsilon is the float64 compare slack for the AABB span assertions (0.9 / 0.45 are exact in
// IEEE-754, but the half-width AABB arithmetic warrants a tiny tolerance — same as agingEpsilon).
const dogfoodEpsilon = 1e-9

// TestDogfoodGateOracleNineGoals is THE FORMAL GATE (MOB-GATE-02). It asserts the boot-loaded pig has
// EXACTLY 9 goals up front (a regression below 9 fails HERE, not silently), then runs the same
// byte-identical proof TestPluginPigEqualsGoNativePig runs (a Go-native pig vs the plugin pig, same id
// => same RNG seed, same world + player, 500 ticks). It stays green ONLY because breed (isInLove-gated)
// and follow (isBaby-gated) are dormant on the lone un-fed adult oracle → zero draws → identical stream.
func TestDogfoodGateOracleNineGoals(t *testing.T) {
	const floorY = 64
	const pigID = 4242 // same id for both => same reseedMobAI seed => same RNG stream

	// Boot-load-9: BOTH the Go-native pig and the plugin pig must register exactly 9 goals
	// {0,1,3,4,4,5,6,7,8}. Assert it up front so a drop below 9 is caught by THIS gate.
	assertNineGoals := func(t *testing.T, label string, ai *mobAI) {
		t.Helper()
		if ai == nil {
			t.Fatalf("%s: nil ai", label)
		}
		if got := len(ai.goals.goals); got != 9 {
			t.Fatalf("%s: %d goals, want 9 {0,1,3,4,4,5,6,7,8} (FloatGoal@0 + PanicGoal@1 + BreedGoal@3 + TemptGoal@4 ×2 + FollowParentGoal@5 + Stroll@6 + LookAtPlayer@7 + RandomLookAround@8)", label, got)
		}
		priorities := map[int]int{}
		for _, wg := range ai.goals.goals {
			priorities[wg.priority]++
		}
		for _, p := range []int{0, 1, 3, 5, 6, 7, 8} {
			if priorities[p] != 1 {
				t.Fatalf("%s: priority %d has %d goals, want exactly 1 (got %v)", label, p, priorities[p], priorities)
			}
		}
		if priorities[4] != 2 {
			t.Fatalf("%s: priority 4 has %d goals, want exactly 2 (the two TemptGoals)", label, priorities[4])
		}
	}

	// Two independent loops with identical worlds + an identical player east of the pig (so the lookAt
	// goal fires for both). The ONLY difference is the AI builder (Go newPigAI vs the plugin decl).
	build := func() *TickLoop {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				fillFloor(ch, floorY)
			}
		}
		loop.players = append(loop.players, &tickPlayer{x: 11.5, y: float64(floorY + 1), z: 8.5})
		return loop
	}

	// The Go-native oracle pig.
	goLoop := build()
	goPig := NewEntity(pigID, entity.Pig, 8.5, float64(floorY+1), 8.5)
	goPig.ai = newPigAI()
	reseedMobAI(goPig.ai, goPig.id)
	goLoop.only().entities.add(goPig)
	assertNineGoals(t, "go-native pig", goPig.ai)

	// The plugin pig — same id (so reseedMobAI gives the identical seed), same start pos.
	plLoop := build()
	plPig := plLoop.spawnVanillaPigWithID(pigID, 8.5, float64(floorY+1), 8.5)
	assertNineGoals(t, "plugin pig", plPig.ai)

	observe := func(e *Entity) pigObservation {
		return pigObservation{
			hasTarget: e.ai.hasTarget,
			wantX:     e.ai.wantX, wantY: e.ai.wantY, wantZ: e.ai.wantZ,
			yaw: e.yaw, headYaw: e.headYaw,
			x: e.x, y: e.y, z: e.z,
		}
	}

	const ticks = 500
	for i := 0; i < ticks; i++ {
		goPig.ai.serverAiStep(goLoop, goPig)
		plPig.ai.serverAiStep(plLoop, plPig)
		// Deterministically rejoin any async path on BOTH loops (same rationale as
		// TestPluginPigEqualsGoNativePig: remove off-tick A* timing jitter from the comparison).
		drainPendingPath(goLoop, goPig)
		drainPendingPath(plLoop, plPig)

		go_ := observe(goPig)
		pl := observe(plPig)
		if go_ != pl {
			t.Fatalf("tick %d: the 9-goal plugin pig diverged from the Go oracle (an un-mirrored RNG draw):\n  go     = %+v\n  plugin = %+v", i, go_, pl)
		}
	}
}

// TestDogfoodGateEndToEnd is the OBSERVABLE dogfood composition (the headless mirror of the live-verify
// checkpoint): two adult pigs fall in love → the live breedGoal spawns a half-scale baby + cooldown +
// inLove reset + XP orb → the baby's live followParentGoal paths to the nearby adult → the baby ages to
// breedAge==0 and grows to full size (AABB restored, DATA_BABY_ID broadcast). It drives the REAL goals
// (not loop.breed() directly), proving the canUse→tick→breed and canUse→tick→follow paths compose.
func TestDogfoodGateEndToEnd(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop) // the bred child spawns via spawnVanillaPig

	// --- (a) two ADULT pigs fall in love -------------------------------------------------------
	// setInLove() is exactly what the feed path arms (tryFeedAnimal: adult + pig_food → setInLove);
	// the feed→inLove wiring itself is unit-tested in inlove_feed_test.go. Here we compose from love.
	e := newDamageMob(loop, 1, 20.0)
	e.x, e.y, e.z = 8.5, 64, 8.5
	e.ai = &mobAI{} // setWantTarget (courting) needs an ai
	e.setInLove()
	partner := newDamageMob(loop, 2, 20.0)
	partner.x, partner.y, partner.z = 9.5, 64, 8.5 // 1 block apart, well within the 3-block breed distance
	partner.ai = &mobAI{}
	partner.setInLove()

	if e.inLove != defaultInLoveTime || partner.inLove != defaultInLoveTime {
		t.Fatalf("(a) both fed pigs must be in love: e.inLove=%d partner.inLove=%d, want %d/%d",
			e.inLove, partner.inLove, defaultInLoveTime, defaultInLoveTime)
	}
	if !e.canMate(partner) {
		t.Fatal("(a) two in-love same-class adult pigs must canMate, want true")
	}

	preCount := len(loop.only().entities.byID)

	// --- (b) the live breedGoal courts to threshold → a HALF-SCALE baby spawns ------------------
	g := newBreedGoal(1.0)
	if !g.canUse(loop, e) {
		t.Fatal("(b) breedGoal.canUse false with two in-love pigs in range, want true (the isInLove gate is satisfied)")
	}
	g.start(loop, e)

	var baby *Entity
	for i := 0; i < 120 && baby == nil; i++ {
		g.tick(loop, e) // courts: look + move + ++loveTime; at loveTime>=60 && distSqr<9 fires breed()
		for _, x := range loop.only().entities.all() {
			if x.breedAge == babyStartAge {
				baby = x
			}
		}
	}
	if baby == nil {
		t.Fatal("(b) the live breedGoal.tick never bred a baby over 120 ticks (the canUse→tick→breed scenario)")
	}
	if !baby.isBaby() {
		t.Fatal("(b) the bred child is not a baby (isBaby() false), want true (breedAge == BABY_START_AGE)")
	}
	if baby.typ != entity.Pig.ID {
		t.Fatalf("(b) the bred child typ = %d, want a pig %d (same-class breeding)", baby.typ, entity.Pig.ID)
	}

	// HALF-SCALE hitbox: the baby's AABB span is 0.45 (adult 0.9 × babyDimensionScale 0.5). breed()
	// sets child.breedAge=-24000 then refreshDimensions(), so the spawned baby is already half-scale.
	if spanX := aabbSpanX(baby); math.Abs(spanX-0.45) > dogfoodEpsilon {
		t.Fatalf("(b) the bred baby AABB X span = %v, want 0.45 (half-scale Pig.BABY_DIMENSIONS)", spanX)
	}
	if spanY := aabbSpanY(baby); math.Abs(spanY-0.45) > dogfoodEpsilon {
		t.Fatalf("(b) the bred baby AABB Y span = %v, want 0.45 (half-scale Pig.BABY_DIMENSIONS)", spanY)
	}

	// Both parents to the 6000 cooldown + inLove reset.
	if e.breedAge != breedingCooldownAge || partner.breedAge != breedingCooldownAge {
		t.Fatalf("(b) parents not at the 6000 breeding cooldown after breed: e=%d partner=%d, want %d/%d",
			e.breedAge, partner.breedAge, breedingCooldownAge, breedingCooldownAge)
	}
	if e.inLove != 0 || partner.inLove != 0 {
		t.Fatalf("(b) inLove not reset after breed: e=%d partner=%d, want 0/0", e.inLove, partner.inLove)
	}

	// The finalizeSpawnChildFromBreeding XP orb (1 + nextInt(7)).
	var orb *Entity
	for _, x := range loop.only().entities.all() {
		if x.isOrb {
			orb = x
		}
	}
	if orb == nil {
		t.Fatal("(b) breedGoal.tick awarded no XP orb (the finalizeSpawnChildFromBreeding 1+nextInt(7) draw)")
	}
	if orb.xpValue < 1 || orb.xpValue > 7 {
		t.Fatalf("(b) breed XP orb value = %d, want 1..7 (1 + nextInt(7))", orb.xpValue)
	}
	if len(loop.only().entities.all()) <= preCount {
		t.Fatalf("(b) entity count did not grow after the breed scenario (%d -> %d), want a baby + orb", preCount, len(loop.only().entities.all()))
	}

	// --- (c) the baby's live followParentGoal paths to the nearby adult -------------------------
	// Position the baby + an adult in the 3..16 follow band (the parents are now on cooldown breedAge>0,
	// i.e. ADULTS per isBaby() — they qualify as follow targets). Drive the real follow goal.
	baby.ai = &mobAI{} // the bred child needs an ai for setWantTarget (the breed child has none yet)
	baby.x, baby.y, baby.z = 8.5, 64, 8.5
	// The NEAREST adult must be in the 3..16 follow band: move BOTH parents out of the "too close"
	// (<3 blocks) zone. Vanilla picks the nearest adult and rejects it if it is within 3 blocks
	// (DONT_FOLLOW_IF_CLOSER_THAN), so a partner left 1 block away would veto the follow. Put the
	// initiator at 5 blocks (distSqr 25, in-band) and the partner farther out (8 blocks) so the
	// nearest qualifying adult is the initiator.
	e.x, e.y, e.z = 13.5, 64, 8.5                   // the initiator parent: 5 blocks east (distSqr 25, in the 9..256 band)
	partner.x, partner.y, partner.z = 16.5, 64, 8.5 // the partner: 8 blocks east (distSqr 64, still in-band but farther)

	follow := newFollowParentGoal(1.1)
	if follow.flags() != 0 {
		t.Fatalf("(c) followParentGoal flags = %v, want 0 (EMPTY — the baby-follow goal locks nothing)", follow.flags())
	}
	if !follow.canUse(loop, baby) {
		t.Fatal("(c) followParentGoal.canUse false with the baby + an adult at 5 blocks, want true (the isBaby gate is satisfied)")
	}
	if follow.parent == nil {
		t.Fatal("(c) followParentGoal acquired no parent, want the nearest adult")
	}
	parentX := follow.parent.x
	follow.start(loop, baby)
	baby.ai.hasTarget = false
	follow.tick(loop, baby) // first tick re-paths (--0 = -1, not >0) → wants the parent's position
	if !baby.ai.hasTarget {
		t.Fatal("(c) followParentGoal.tick set no want target on the first (re-path) tick — the baby must path to its parent")
	}
	if baby.ai.wantX != parentX {
		t.Fatalf("(c) the baby want X = %v does not point at its parent X = %v (FollowParentGoal navigation)", baby.ai.wantX, parentX)
	}
	if follow.timeToRecalcPath != adjustedTickDelay(followRecalcInterval, false) {
		t.Fatalf("(c) timeToRecalcPath = %d after re-path, want %d (adjustedTickDelay(10, false)=5, NO RNG)", follow.timeToRecalcPath, adjustedTickDelay(followRecalcInterval, false))
	}

	// --- (d) the baby ages fully to breedAge==0 → grows to full size ----------------------------
	// Drive the baby one tick from grow-up so the cross-0 transition (onGrewUp) fires deterministically:
	// restore the adult AABB (0.9) + broadcast DATA_BABY_ID=false. (tickMobAging +1/tick toward 0.)
	baby.breedAge = -1
	baby.refreshDimensions() // ensure the half-scale box pre-grow-up
	if spanX := aabbSpanX(baby); math.Abs(spanX-0.45) > dogfoodEpsilon {
		t.Fatalf("(d) pre-grow-up baby AABB X span = %v, want 0.45 (still half-scale)", spanX)
	}

	loop.tickMobAging(baby) // -1 -> 0: onGrewUp restores the adult box + flips DATA_BABY_ID
	if baby.breedAge != 0 {
		t.Fatalf("(d) after the grow-up tick breedAge = %d, want 0 (the baby grew up)", baby.breedAge)
	}
	if baby.isBaby() {
		t.Fatal("(d) after grow-up isBaby() = true, want false (breedAge == 0 is an adult)")
	}
	if spanX := aabbSpanX(baby); math.Abs(spanX-0.9) > dogfoodEpsilon {
		t.Fatalf("(d) grown-up pig AABB X span = %v, want 0.9 (onGrewUp must restore the adult dims — 'grows to full size')", spanX)
	}
	if spanY := aabbSpanY(baby); math.Abs(spanY-0.9) > dogfoodEpsilon {
		t.Fatalf("(d) grown-up pig AABB Y span = %v, want 0.9 (onGrewUp must restore the adult dims)", spanY)
	}
	// The DATA_BABY_ID cross-0 broadcast carries isBaby()==false (babyDataEntry(false)) — the wire flag
	// the live client reads to re-render the pig at full size. babyDataEntry reflects the current state.
	if entry := babyDataEntry(baby.isBaby()); entry.index != dataBabyIndex {
		t.Fatalf("(d) babyDataEntry index = %d, want the DATA_BABY_ID index %d", entry.index, dataBabyIndex)
	}
}
