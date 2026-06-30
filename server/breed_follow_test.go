package server

// breed_follow_test.go — MOB-SUB-09 (Plan 33-03, C1): the GO-NATIVE BreedGoal + FollowParentGoal
// coverage. These tests pin the jar-exact behavior of the two new goals + their helpers:
//   - adjustedTickDelay (identity at 20 TPS, NOT reducedTickDelay's ceil(n/2))
//   - canMate (Animal.canMate: distinct same-class both-in-love)
//   - getFreePartner (nearest same-class in-love non-panicking within range 8.0)
//   - isPanicking (the FAITHFUL running-PanicGoal read, NOT a hurtTime proxy)
//   - breedGoal canUse/canContinue/tick + breed() (child baby half-scale + parent cooldown + inLove
//     reset + the two breed-path RNG draws: variant nextBoolean() then XP nextInt(7))
//   - followParentGoal canUse/canContinue/tick (baby trails nearest adult, re-path every 10, NO RNG)
//
// JAR AUTHORITY (javap -c -p temp/cache/26.2-inner.jar, this session): BreedGoal, FollowParentGoal,
// Animal.canMate, Animal.spawnChildFromBreeding -> Pig.getBreedOffspring (variant nextBoolean) ->
// finalizeSpawnChildFromBreeding (setAge(6000)×2, resetLove×2, XP 1+nextInt(7)), Goal.adjustedTickDelay.

import "testing"

// TestAdjustedTickDelay: the identity helper — adjustedTickDelay(n) == n at 20 TPS (NOT
// reducedTickDelay's ceil(n/2)). The breed/follow thresholds depend on this being 60/10, not 30/5.
func TestAdjustedTickDelay(t *testing.T) {
	if got := adjustedTickDelay(60); got != 60 {
		t.Fatalf("adjustedTickDelay(60) = %d, want 60 (identity @20TPS; reducedTickDelay would give 30)", got)
	}
	if got := adjustedTickDelay(10); got != 10 {
		t.Fatalf("adjustedTickDelay(10) = %d, want 10 (identity @20TPS; reducedTickDelay would give 5)", got)
	}
	// It must NOT be reducedTickDelay (the WRONG helper): reducedTickDelay(60)=30 != adjustedTickDelay(60)=60.
	if adjustedTickDelay(60) == reducedTickDelay(60) {
		t.Fatalf("adjustedTickDelay(60) must differ from reducedTickDelay(60)=%d (it must be 60, not the halved 30)", reducedTickDelay(60))
	}
}

// TestCanMate: Animal.canMate — distinct, same-class, both-in-love is the only mate-able pair.
func TestCanMate(t *testing.T) {
	loop, _ := newBlockLoop()
	a := newDamageMob(loop, 1, 20.0) // entity.Pig
	b := newDamageMob(loop, 2, 20.0) // entity.Pig

	// Neither in love → cannot mate.
	if a.canMate(b) {
		t.Fatal("canMate true with neither in love, want false")
	}
	// Only a in love → cannot mate.
	a.setInLove()
	if a.canMate(b) {
		t.Fatal("canMate true with only a in love, want false")
	}
	// Both in love → can mate.
	b.setInLove()
	if !a.canMate(b) {
		t.Fatal("canMate false with both same-class pigs in love, want true")
	}
	// A pig cannot mate with itself (other != this).
	if a.canMate(a) {
		t.Fatal("canMate true with self, want false (other != this)")
	}
}

// TestIsPanicking: the FAITHFUL read — isPanicking is true iff the mob's PanicGoal wrappedGoal is
// running, false otherwise. NOT a hurtTime proxy.
func TestIsPanicking(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)

	// No ai → not panicking (nil-guard).
	if loop.isPanicking(e) {
		t.Fatal("isPanicking true for a mob with no ai, want false")
	}

	// Wire an ai with a panicGoal that is NOT running.
	e.ai = &mobAI{}
	e.ai.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	if loop.isPanicking(e) {
		t.Fatal("isPanicking true while the PanicGoal is not running, want false")
	}

	// Mark the PanicGoal running → isPanicking true.
	for _, wg := range e.ai.goals.goals {
		if _, ok := wg.g.(*panicGoal); ok {
			wg.running = true
		}
	}
	if !loop.isPanicking(e) {
		t.Fatal("isPanicking false while the PanicGoal IS running, want true (the faithful read)")
	}

	// hurtTime must NOT drive isPanicking (the proxy is wrong): clear running, set hurtTime, expect false.
	for _, wg := range e.ai.goals.goals {
		if _, ok := wg.g.(*panicGoal); ok {
			wg.running = false
		}
	}
	e.hurtTime = 10
	if loop.isPanicking(e) {
		t.Fatal("isPanicking true from hurtTime>0 with the PanicGoal NOT running — must read the running goal, not hurtTime")
	}
}

// TestGetFreePartner: getFreePartner returns the NEAREST same-class in-love non-panicking partner
// within range 8.0, and nil when none qualifies.
func TestGetFreePartner(t *testing.T) {
	loop, _ := newBlockLoop()
	// All entities share column {0,0} (x≈8.5) so the in-region near() scan finds them.
	e := newDamageMob(loop, 1, 20.0)
	e.x, e.y, e.z = 8.5, 64, 8.5
	e.setInLove()
	g := newBreedGoal(1.0)

	// No partners in love → nil.
	if p := g.getFreePartner(loop, e); p != nil {
		t.Fatalf("getFreePartner = %v with no in-love partner, want nil", p)
	}

	// A near in-love same-class pig at 2 blocks → that is the partner.
	near := newDamageMob(loop, 2, 20.0)
	near.x, near.y, near.z = 10.5, 64, 8.5 // 2 blocks east
	near.setInLove()
	// A farther in-love same-class pig at 4 blocks → must lose to the nearer one.
	far := newDamageMob(loop, 3, 20.0)
	far.x, far.y, far.z = 12.5, 64, 8.5 // 4 blocks east
	far.setInLove()

	if p := g.getFreePartner(loop, e); p != near {
		t.Fatalf("getFreePartner = %v, want the NEAREST in-love partner (id 2)", p)
	}

	// A partner out of range (>8 blocks) is not selected when it is the only candidate.
	near.inLove = 0 // near no longer in love
	far.inLove = 0  // far no longer in love
	out := newDamageMob(loop, 4, 20.0)
	out.x, out.y, out.z = 20.5, 64, 8.5 // 12 blocks east — out of the 8.0 range
	out.setInLove()
	if p := g.getFreePartner(loop, e); p != nil {
		t.Fatalf("getFreePartner = %v with the only candidate out of range, want nil", p)
	}
}

// TestBreedGoalCanUseGate: canUse returns false WITHOUT scanning when the mob is not in love (the
// oracle-safety gate — the un-fed pig draws nothing) and true with a partner when in love.
func TestBreedGoalCanUseGate(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.x, e.y, e.z = 8.5, 64, 8.5
	g := newBreedGoal(1.0)

	// Not in love → canUse false (the line-1 isInLove gate).
	if g.canUse(loop, e) {
		t.Fatal("breedGoal.canUse true while not in love, want false (the oracle-safety gate)")
	}

	// In love with a near in-love partner → canUse true, partner set.
	e.setInLove()
	partner := newDamageMob(loop, 2, 20.0)
	partner.x, partner.y, partner.z = 10.5, 64, 8.5
	partner.setInLove()
	if !g.canUse(loop, e) {
		t.Fatal("breedGoal.canUse false with an in-love near partner, want true")
	}
	if g.partner != partner {
		t.Fatalf("breedGoal.partner = %v, want the near partner (id 2)", g.partner)
	}
}

// TestBreed: breed() spawns a baby (breedAge=BABY_START_AGE, half-scale), sets BOTH parents to the
// 6000 cooldown, resets BOTH inLove, and draws the two breed-path RNG values (variant nextBoolean()
// then XP nextInt(7)) — exercised via the spawned XP orb in the owning region.
func TestBreed(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop) // spawnVanillaPig needs the declaration

	e := newDamageMob(loop, 1, 20.0)
	e.x, e.y, e.z = 8.5, 64, 8.5
	e.setInLove()
	partner := newDamageMob(loop, 2, 20.0)
	partner.x, partner.y, partner.z = 9.5, 64, 8.5
	partner.setInLove()

	preCount := len(loop.only().entities.byID)

	loop.breed(e, partner)

	// Both parents go to the 6000-tick breeding cooldown and out of love.
	if e.breedAge != breedingCooldownAge {
		t.Fatalf("initiator breedAge = %d after breed, want %d (cooldown)", e.breedAge, breedingCooldownAge)
	}
	if partner.breedAge != breedingCooldownAge {
		t.Fatalf("partner breedAge = %d after breed, want %d (cooldown)", partner.breedAge, breedingCooldownAge)
	}
	if e.inLove != 0 || partner.inLove != 0 {
		t.Fatalf("inLove not reset after breed: e=%d partner=%d, want 0/0", e.inLove, partner.inLove)
	}

	// A baby + at least one XP orb were added to the owning region's store.
	post := loop.only().entities.all()
	var baby, orb *Entity
	for _, x := range post {
		if x.breedAge == babyStartAge {
			baby = x
		}
		if x.isOrb {
			orb = x
		}
	}
	if baby == nil {
		t.Fatal("breed() spawned no baby (an entity with breedAge == BABY_START_AGE)")
	}
	if !baby.isBaby() {
		t.Fatal("the bred child is not a baby (isBaby() false)")
	}
	if orb == nil {
		t.Fatal("breed() awarded no XP orb (the finalizeSpawnChildFromBreeding 1+nextInt(7) draw)")
	}
	if orb.xpValue < 1 || orb.xpValue > 7 {
		t.Fatalf("breed XP orb value = %d, want 1..7 (1 + nextInt(7))", orb.xpValue)
	}
	if len(post) <= preCount {
		t.Fatalf("entity count did not grow after breed (%d -> %d), want a baby + orb added", preCount, len(post))
	}
}

// TestFollowParent: a baby (breedAge<0) selects the nearest ADULT same-class within ~8 blocks
// (rejecting one already within 3 blocks), keeps following while 3..16 blocks, stops when grown up,
// and re-paths on the right cadence — all with NO RNG.
func TestFollowParent(t *testing.T) {
	loop, _ := newBlockLoop()

	baby := newDamageMob(loop, 1, 20.0)
	baby.x, baby.y, baby.z = 8.5, 64, 8.5
	baby.breedAge = -100 // a baby
	baby.ai = &mobAI{}   // setWantTarget needs an ai

	g := newFollowParentGoal(1.1)

	// EMPTY flags — the goal never locks MOVE/LOOK.
	if g.flags() != 0 {
		t.Fatalf("followParentGoal flags = %v, want 0 (EMPTY — the ctor never sets flags)", g.flags())
	}

	// No adult nearby → canUse false.
	if g.canUse(loop, baby) {
		t.Fatal("followParentGoal.canUse true with no adult nearby, want false")
	}

	// An adult at 5 blocks (distSqr 25, in the 9..256 follow band) → canUse true, parent set.
	adult := newDamageMob(loop, 2, 20.0)
	adult.x, adult.y, adult.z = 13.5, 64, 8.5 // 5 blocks east
	adult.breedAge = 0                        // an adult
	if !g.canUse(loop, baby) {
		t.Fatal("followParentGoal.canUse false with an adult at 5 blocks, want true")
	}
	if g.parent != adult {
		t.Fatalf("followParentGoal.parent = %v, want the adult (id 2)", g.parent)
	}

	// An adult that is the only candidate but within 3 blocks (distSqr<9) is NOT followed.
	g2 := newFollowParentGoal(1.1)
	adult.x = 10.5 // 2 blocks east of the baby → distSqr 4 < 9
	if g2.canUse(loop, baby) {
		t.Fatal("followParentGoal.canUse true with the adult within 3 blocks, want false (DONT_FOLLOW_IF_CLOSER_THAN)")
	}
	adult.x = 13.5 // restore to 5 blocks

	// A grown-up baby (breedAge>=0) stops following (canUse + canContinue both false).
	grown := newDamageMob(loop, 3, 20.0)
	grown.x, grown.y, grown.z = 8.5, 64, 8.5
	grown.breedAge = 0
	g3 := newFollowParentGoal(1.1)
	if g3.canUse(loop, grown) {
		t.Fatal("followParentGoal.canUse true for a grown adult, want false (only a baby follows)")
	}

	// tick: the re-path cadence. start() sets timeToRecalcPath=0; the first tick re-paths (sets a want)
	// and resets the timer to adjustedTickDelay(10)=10; intermediate ticks do NOT re-path.
	g.start(loop, baby)
	baby.ai.hasTarget = false
	g.tick(loop, baby) // first tick: --0 = -1, not >0 → re-path
	if !baby.ai.hasTarget {
		t.Fatal("followParentGoal.tick did not set a want target on the first (re-path) tick")
	}
	if g.timeToRecalcPath != adjustedTickDelay(followRecalcInterval) {
		t.Fatalf("timeToRecalcPath = %d after re-path, want %d (adjustedTickDelay(10))", g.timeToRecalcPath, followRecalcInterval)
	}
}

// TestFollowParentContinueBand: canContinueToUse follows only while 3..16 blocks (9.0 <= distSqr <=
// 256.0) and stops outside it or on grow-up.
func TestFollowParentContinueBand(t *testing.T) {
	loop, _ := newBlockLoop()
	baby := newDamageMob(loop, 1, 20.0)
	baby.x, baby.y, baby.z = 8.5, 64, 8.5
	baby.breedAge = -100
	adult := newDamageMob(loop, 2, 20.0)
	adult.breedAge = 0
	g := newFollowParentGoal(1.1)
	g.parent = adult

	// 5 blocks (distSqr 25, in-band) → continue.
	adult.x, adult.y, adult.z = 13.5, 64, 8.5
	if !g.canContinueToUse(loop, baby) {
		t.Fatal("canContinueToUse false at 5 blocks (in the 3..16 band), want true")
	}
	// 2 blocks (distSqr 4 < 9) → stop (back within DONT_FOLLOW).
	adult.x = 10.5
	if g.canContinueToUse(loop, baby) {
		t.Fatal("canContinueToUse true at 2 blocks (<3), want false")
	}
	// 20 blocks (distSqr 400 > 256) → stop (lost the parent).
	adult.x = 28.5
	if g.canContinueToUse(loop, baby) {
		t.Fatal("canContinueToUse true at 20 blocks (>16), want false")
	}
	// Grown up → stop regardless of distance.
	adult.x = 13.5
	baby.breedAge = 0
	if g.canContinueToUse(loop, baby) {
		t.Fatal("canContinueToUse true after grow-up, want false")
	}
}
