package server

// ai_goals_panic.go — MOB-GATE-01 (Phase 31-01): the Go-native PanicGoal@1, the SECOND of the pig's
// five deferred goals and the FIRST real consumer of the Phase-29 lastDamageSource keystone. A pig that
// takes panic-causing damage (a panic_causes member, e.g. player_attack) flees to a random nearby
// position (or, if on fire, toward water). It is a THIN consumer: shouldPanic reads the keystone, and
// findRandomPosition reuses the Phase-30.1 candidate machinery (generateRandomDirection) +
// setWantCandidates -> snapStrollWant path at the DefaultRandomPos.getPos(mob, 5, 4) radius. NO new
// subsystem. Wired @1 (MOVE flag, speed 1.25) on newPigAI in LOCKSTEP with the plugin declaration
// (plugins/vanilla_pig/main.star) — never split across plans (the oracle contract).
//
// PORTED 1:1 from net.minecraft.world.entity.ai.goal.PanicGoal in the unobfuscated 26.2 jar
// (javap -c -p temp/cache/26.2-inner.jar, this session):
//
//	public PanicGoal(PathfinderMob mob, double speedModifier) { this(mob, speedModifier, DamageTypeTags.PANIC_CAUSES); }
//	public boolean canUse() {
//	    if (!shouldPanic()) return false;                                    // RETURN BEFORE ANY RNG
//	    if (mob.isOnFire()) {
//	        BlockPos bp = lookForWater(mob.level(), mob, 5);                  // RNG-free spiral scan
//	        if (bp != null) { posX=bp.getX(); posY=bp.getY(); posZ=bp.getZ(); return true; }
//	    }
//	    return findRandomPosition();
//	}
//	protected boolean shouldPanic() {
//	    return mob.getLastDamageSource() != null && mob.getLastDamageSource().is(panicCausingDamageTypes.apply(mob));
//	}
//	protected boolean findRandomPosition() {
//	    Vec3 pos = DefaultRandomPos.getPos(mob, 5, 4);                        // h=5, v=4 — NO up-snap
//	    if (pos == null) return false;
//	    posX=pos.x; posY=pos.y; posZ=pos.z; return true;
//	}
//	public void start() { mob.getNavigation().moveTo(posX, posY, posZ, speedModifier); isRunning = true; }
//	public void stop() { isRunning = false; }
//	public boolean canContinueToUse() { return !mob.getNavigation().isDone(); }   // == our hasTarget
//
// RNG DISCIPLINE (the oracle care item — trivially safe here): canUse draws RNG ONLY when shouldPanic
// is TRUE. The first line short-circuits (return false) before any draw when shouldPanic is false, EXACTLY
// like FloatGoal.canUse's NO-RNG fluid predicate (ai_goals_float.go). The DRY pig oracle
// (TestPluginPigEqualsGoNativePig) drives an unattacked pig -> getLastDamageSource()==null -> shouldPanic
// false -> canUse returns on line 1 -> ZERO draws -> the oracle stays byte-identical with PanicGoal@1
// added to BOTH the Go pig and both .star copies in lockstep. When a panic DOES fire, findRandomPosition
// draws the UNCONDITIONAL 30 nextInt (10 candidates x 3, x/y/z order) of DefaultRandomPos.getPos(5,4) —
// identical on both halves.

// panicHorizontalRadius / panicVerticalRadius are DefaultRandomPos.getPos(mob, 5, 4)'s args (cf.
// strollHorizontalRadius=10 / strollVerticalRadius=7 for the stroll's getPos(mob, 10, 7)).
//
//	[VERIFIED javap PanicGoal.findRandomPosition: aload mob; iconst_5; iconst_4;
//	 invokestatic DefaultRandomPos.getPos(PathfinderMob, int, int).]
const (
	panicHorizontalRadius = 5
	panicVerticalRadius   = 4
)

// panicSpeedModifier is the Pig's PanicGoal speed (Pig.registerGoals @1 PanicGoal(mob, 1.25)).
//
//	[VERIFIED javap Pig.registerGoals: iconst_1 (priority); new PanicGoal; dup; aload_0;
//	 ldc2_w double 1.25d; invokespecial PanicGoal.<init>(PathfinderMob, double); GoalSelector.addGoal.]
const panicSpeedModifier = 1.25

// panicGoal ports net.minecraft.world.entity.ai.goal.PanicGoal (flag {MOVE}). It claims the MOVE flag at
// priority 1 (after FloatGoal@0, before stroll@6) and, on a panic-causing hit, hands flee candidates to
// the runtime snap. It carries wantCandidates between canUse (which draws + emits them) and start (which
// hands them to setWantCandidates) — exactly like randomStrollGoal carries its candidates across the
// Phase-30.1 architecture split. The jar's posX/posY/posZ scalars become the candidate slice here (the
// runtime snap picks the first valid, mirroring DefaultRandomPos's first-non-null winner).
type panicGoal struct {
	baseGoal
	speedModifier  float64
	wantCandidates [][3]float64
}

// newPanicGoal builds the PanicGoal with the MOVE flag (PanicGoal ctor: setFlags(EnumSet.of(MOVE))) and
// the Pig's 1.25 speed modifier. The ctor's panicCausingDamageTypes = DamageTypeTags.PANIC_CAUSES is the
// "panic_causes" tag read by shouldPanic (data/tag.DamageTypeTags, Phase 29) — never duplicated here.
//
//	[VERIFIED javap PanicGoal.<init>(PathfinderMob, double): delegates to the 3-arg ctor with
//	 DamageTypeTags.PANIC_CAUSES; the 3-arg ctor calls setFlags(EnumSet.of(Goal$Flag.MOVE)).]
func newPanicGoal(speed float64) *panicGoal {
	return &panicGoal{baseGoal: newBaseGoal(flagMove), speedModifier: speed}
}

// shouldPanic ports PanicGoal.shouldPanic: getLastDamageSource() != null && getLastDamageSource()
// .is(panicCausingDamageTypes). e.hasLastDamage is the faithful not-null signal (set in hurtServer's
// flag2 block — combat_mob.go; Decision B: typeTag==0 is minecraft:in_fire, a REAL panic_causes member,
// so the id cannot be the unset sentinel). e.lastDamageSource.is("panic_causes") is the GENUINE data/tag
// membership read (damage_source.go:78), NEVER const false.
//
//	[VERIFIED javap PanicGoal.shouldPanic: getLastDamageSource ifnull -> iconst_0; else
//	 getLastDamageSource(); panicCausingDamageTypes.apply(mob); DamageSource.is(TagKey).]
func (g *panicGoal) shouldPanic(e *Entity) bool {
	return e.hasLastDamage && e.lastDamageSource.is("panic_causes")
}

// isOnFire ports Entity.isOnFire(). No remainingFireTicks / fire-tick state exists on Entity yet (a pig
// is rarely on fire), so this is a CITED stub returning false: the on-fire water-flee branch in canUse
// then never runs (faithful for a non-burning pig, drawing ZERO RNG). UPGRADE PATH: read
// remainingFireTicks > 0 once Entity fire-tick state lands.
//
//	[VERIFIED javap PanicGoal.canUse: getfield mob; invokevirtual PathfinderMob.isOnFire; ifeq -> skip
//	 the lookForWater branch.]
func (g *panicGoal) isOnFire(_ *Entity) bool { return false }

// lookForWater ports PanicGoal.lookForWater(level, mob, 5): an RNG-FREE spiral block scan —
// BlockPos.findClosestMatch(mob.blockPosition(), 5, 1, p -> getFluidState(p).is(WATER)).orElse(null),
// gated on the mob's own block having an empty collision shape. No fluid-aware spiral-scan helper exists
// and this is the RARE branch for a non-burning pig, so it is a CITED stub returning ok=false (the
// on-fire water-flee never runs — faithful for a non-burning pig, drawing ZERO RNG). UPGRADE PATH: a
// real findClosestMatch over getFluidState(WATER) when fluid nav lands.
//
//	[VERIFIED javap PanicGoal.lookForWater: mob.blockPosition; getBlockState getCollisionShape isEmpty
//	 ifeq -> return null; else BlockPos.findClosestMatch(mp, 5, 1, isWater).orElse(null). NO RNG.]
func (g *panicGoal) lookForWater(_ *TickLoop, _ *Entity) (bp [3]float64, ok bool) {
	return [3]float64{}, false
}

// canUse ports PanicGoal.canUse — branch order EXACTLY: (a) shouldPanic gate FIRST, returning before any
// RNG (the oracle-safety short-circuit, mirroring FloatGoal.canUse's no-RNG predicate); (b) the on-fire
// lookForWater branch (currently a cited false-stub — never taken for a non-burning pig, RNG-free); (c)
// findRandomPosition (the 30-draw DefaultRandomPos(5,4) flee selection).
//
//	[VERIFIED javap PanicGoal.canUse: shouldPanic ifne; iconst_0 ireturn; isOnFire ifeq 69;
//	 lookForWater ifnull 69 -> set posX/Y/Z, return true; else findRandomPosition.]
func (g *panicGoal) canUse(t *TickLoop, e *Entity) bool {
	// (a) shouldPanic gate — RETURN BEFORE ANY RNG. A dry/unhurt pig stops here with zero draws.
	if !g.shouldPanic(e) {
		return false
	}
	// (b) the on-fire water-flee branch (rare; lookForWater is a cited RNG-free false-stub today, so this
	// never commits — faithful for a non-burning pig). When it lands it sets the want from the water block.
	if g.isOnFire(e) {
		if bp, ok := g.lookForWater(t, e); ok {
			g.wantCandidates = [][3]float64{bp, bp, bp, bp, bp, bp, bp, bp, bp, bp}
			return true
		}
	}
	// (c) findRandomPosition — the DefaultRandomPos.getPos(5,4) flee selection (30 nextInt).
	return g.findRandomPosition(t, e)
}

// findRandomPosition ports PanicGoal.findRandomPosition = DefaultRandomPos.getPos(mob, 5, 4): the SAME
// unconditional best-of-10 loop as the stroll's getPosition, but at radius (5,4) and in DefaultRandomPos
// mode (no up-snap intent). It reuses generateRandomDirection VERBATIM (do NOT duplicate; x/y/z draw
// order, 30 nextInt total) and emits 10 raw candidates; the runtime snap (snapStrollWant) commits the
// first valid via setWantTarget, mirroring DefaultRandomPos returning null -> false when none survive.
// Per Decision A the flee candidates flow through the SAME setWantCandidates -> snapStrollWant path as
// stroll (no separate snapFleeWant).
//
//	[VERIFIED javap DefaultRandomPos.getPos(mob, 5, 4) -> RandomPos.generateRandomPos(mob, supplier);
//	 supplier = generateRandomDirection(random, 5, 4) (3 nextInt: x=nextInt(11)-5, y=nextInt(9)-4,
//	 z=nextInt(11)-5) -> generateRandomPosTowardDirection (DefaultRandomPos mode: NO moveUpOutOfSolid,
//	 NO isWater); generateRandomPos: for i<10, NO break; first non-null wins.]
func (g *panicGoal) findRandomPosition(_ *TickLoop, e *Entity) bool {
	r := mobRandom(e)
	cands := make([][3]float64, 0, 10)
	for i := 0; i < 10; i++ { // RandomPos.generateRandomPos: for i<10, NO break (always 10 supplier calls)
		// generateRandomDirection draws x, y, z in ORDER (the bit-fragile lockstep contract) at radius
		// (5,4) — DRAW order per candidate: nextInt(11)-5, nextInt(9)-4, nextInt(11)-5.
		xt, yt, zt := generateRandomDirection(r, panicHorizontalRadius, panicVerticalRadius)
		// DefaultRandomPos.generateRandomPosTowardDirection: BlockPos.containing(xt+x, yt+y, zt+z); a v1
		// pig has no home, so the hasHome()&&xzDist>1.0 bias branch never runs (ZERO extra draws).
		cands = append(cands, [3]float64{e.x + float64(xt), e.y + float64(yt), e.z + float64(zt)})
	}
	g.wantCandidates = cands
	return true // the 10 raw candidates exist; the runtime snap commits the first valid (or leaves no want).
}

// start ports PanicGoal.start = navigation.moveTo(posX, posY, posZ, speedModifier): hand the flee
// candidates to the runtime via setWantCandidates with landMode=false (DefaultRandomPos — NO up-snap
// intent; the runtime snap is consumed-as-Land today, exactly like stroll's rare DefaultRandomPos branch
// — see stroll_snap.go). Guard the 10-candidate contract like randomStrollGoal.start.
//
// SPEED MODIFIER (cited-deferred): the panic speedModifier 1.25 differs from stroll's 1.0, but the v1
// nav uses the single pigWalkSpeed const and the want carries POSITION only — so the 1.25 multiplier is
// NOT yet wired into navigation.speed (the SAME posture as stroll's speedModifier, which is also carried
// but not routed to the per-goal nav speed). The faithful value is stored on the goal (speedModifier)
// for the upgrade where navigation.moveTo takes a per-request speed. Documented faithful-scope.
//
//	[VERIFIED javap PanicGoal.start: getNavigation().moveTo(posX, posY, posZ, speedModifier); isRunning=true.]
func (g *panicGoal) start(_ *TickLoop, e *Entity) {
	if e.ai == nil || len(g.wantCandidates) != 10 {
		return
	}
	var arr [10][3]float64
	copy(arr[:], g.wantCandidates)
	e.ai.setWantCandidates(arr, false) // landMode=false (DefaultRandomPos)
}

// canContinueToUse ports PanicGoal.canContinueToUse = !navigation.isDone(). With the Phase-30.1 nav, "not
// done" == the mob still has a pending wanted target (hasTarget). Mirror randomStrollGoal.canContinueToUse.
//
//	[VERIFIED javap PanicGoal.canContinueToUse: getNavigation().isDone(); ifne iconst_0; iconst_1; ireturn
//	 (== !isDone()).]
func (g *panicGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.hasTarget
}

// stop ports PanicGoal.stop = isRunning=false. The Go runtime equivalent of the jar's moveTo/isDone
// lifecycle is clearWantTarget() on the running->stopped edge (mirror randomStrollGoal.stop).
//
//	[VERIFIED javap PanicGoal.stop: aload_0; iconst_0; putfield isRunning.]
func (g *panicGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}
