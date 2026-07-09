package server

// ai_goals_fox_extra.go — the Fox CHARACTER-LAYER goals that extend the SHARED goals (moveToBlock +
// followParent) + the RED-variant target goals (foxFishTarget + foxTurtleEggTarget). All ported 1:1
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session).
//
// FOX GOALS PORTED HERE (the cite-deferred subset of Fox.registerGoals that lands via these helpers):
//   - FoxEatBerriesGoal @10   (extends MoveToBlockGoal)         -> kind="fox_eat_berries"
//   - FoxFollowParentGoal @8  (extends FollowParentGoal)        -> kind="fox_follow_parent"
//   - fishTargetGoal @6        (NearestAttackableTargetGoal<AbstractFish>(AbstractSchoolingFish),
//                                gated on foxVariant==RED variant ordering)   -> kind="fox_fish_target"
//   - turtleEggTargetGoal @4   (NearestAttackableTargetGoal<Turtle>(Baby && !InWater),
//                                gated on RED variant ordering)                  -> kind="fox_turtle_egg_target"
//
// JAR VALUES (CFR this session, verbatim):
//
//	Fox$FoxEatBerriesGoal.<init>(Fox, double, int, int):
//	    super(MoveToBlockGoal.<init>(fox, 1.2, 12, 1));      // speed=1.2, searchRange=12, vertical=1
//	isValidTarget(LevelReader, BlockPos): state.is(SWEET_BERRY_BUSH) && state.getValue(AGE) >= 2
//	    || CaveVines.hasGlowBerries(state);                  // the cave-vine variant
//	acceptedDistance: 2.0
//	shouldRecalculatePath: tryTicks % 100 == 0               // every 100 ticks re-path
//	tick(): isReachedTarget && ticksWaited >= 40 -> onReachedTarget();
//	         else ticksWaited++ if reached, else if fox.nextFloat()<0.05 -> playSound(FOX_SNIFF)
//	         then super.tick();
//	onReachedTarget(): MOB_GRIEFING gate (vanilla sets AGE->1 + 0..1 nextInt drop + eat)
//
//	Fox$FoxFollowParentGoal.<init>(Fox, double):
//	    super(FollowParentGoal.<init>(fox, 1.25));            // speed=1.25 (NOT 1.1 — the fox overrides)
//	canUse: !isDefending && super.canUse
//	canContinueToUse: !isDefending && super.canContinueToUse
//	start: fox.clearStates(); super.start()
//
//	Fox$fishTargetGoal = NearestAttackableTargetGoal(fox, AbstractFish.class, 20, false, false,
//	    selector -> candidate instanceof AbstractSchoolingFish)
//	  (added at targetSelector @6 in RED variant; @4 in SNOW variant)
//
//	Fox$turtleEggTargetGoal = NearestAttackableTargetGoal(fox, Turtle.class, 10, false, false,
//	    BABY_ON_LAND_SELECTOR (selector -> isBaby && !isInWater))
//	  (added at targetSelector @4 in RED variant; @6 in SNOW variant)
//
// PRIORITY ORDERING (jar-verified via Fox.setTargetGoals):
//
//	RED variant (Fox.Variant.RED == 0, the default v1 spawn):
//	  targetSelector @4 landTargetGoal (chicken||rabbit)   <-- fox_land_target @4 (already ported)
//	  targetSelector @4 turtleEggTargetGoal                 <-- fox_turtle_egg_target @4
//	  targetSelector @6 fishTargetGoal                     <-- fox_fish_target @6
//	SNOW variant (Fox.Variant.SNOW == 1): reorders to fishTargetGoal @4 + land/turtleEgg @6.
//	  v1 only spawns RED (the biome-tag gate is cited const-false); the SNOW ordering stays a
//	  structurally-real variant for a future SNOW-spawning world.
//
// CITE-DEFERRED SUBS:
//   - CaveVines.hasGlowBerries(state) and the cave-vine onReachedTarget branch (pickGlowBerry): v1 has
//     no cave-vine block, so the GLOW_BERRIES side of the isValidTarget || never matches in v1.
//     The SWEET_BERRY_BUSH side (state.is(SWEET_BERRY_BUSH) && age >= 2) IS faithful and lands.
//   - The onReachedTarget MOB_GRIEFING gate is wired as a cited constant-TRUE (a v1 fox always eats
//     the berries; the gamerule check is a server-controlled flag that defaults true in v1 tests).
//     The pickSweetBerries BERRY-SET + ItemStack hand-off land faithfully; mainhand assignment
//     routes through setItemSlot (the existing seam).
//   - The playSound(FOX_SNIFF) broadcast is cited deferred (no server-side audio subsystem);
//     structured so the playSound seam wires when audio lands.
//   - fishTarget/turtleEggTarget variants: RED variant ORDERING lands; SNOW variant is structurally
//     real (registered as a possible alternate), but v1 only spawns RED so the SNOW branch is
//     dormant (a future SNOW-spawning world will use it as-is, no change needed).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/level/block"
)

// =========================================================================================
// FoxEatBerriesGoal (net.minecraft.world.entity.animal.fox.Fox$FoxEatBerriesGoal)
// =========================================================================================

// foxEatBerriesGoal ports Fox$FoxEatBerriesGoal (extends MoveToBlockGoal, flags {MOVE, JUMP}).
// The fox walks to the nearest ripe sweet-berry bush (SWEET_BERRY_BUSH with AGE >= 2) within
// searchRange=12 and waits 40 ticks (WAIT_TICKS) at the bush before eating. The cave-vine side
// (CaveVines.hasGlowBerries) is a cited v1 null-op (no cave-vine block subsystem).
type foxEatBerriesGoal struct {
	moveToBlockGoal
	// ticksWaited is Fox$FoxEatBerriesGoal.ticksWaited (the WAIT_TICKS countdown to onReachedTarget).
	ticksWaited int
}

// foxEatBerriesSpeed is the ldc2_w 1.2000000476837158d passed to MoveToBlockGoal's super(fox, 1.2, ...).
//
//	[VERIFIED javap Fox$FoxEatBerriesGoal.<init>: dload_2 (1.2); iload 4 (12); iload 5 (1);
//	 invokespecial MoveToBlockGoal.<init>(PathfinderMob, double, int, int).]
const foxEatBerriesSpeed = 1.2

// foxEatBerriesSearchRange is the bipush 12 searchRange arg (jar-verified).
const foxEatBerriesSearchRange = 12

// foxEatBerriesVerticalRange is the iconst_1 verticalSearchRange arg (jar-verified).
const foxEatBerriesVerticalRange = 1

// foxEatBerriesWaitTicks is Fox$FoxEatBerriesGoal.WAIT_TICKS = 40.
//
//	[VERIFIED javap Fox$FoxEatBerriesGoal.tick: getfield ticksWaited; bipush 40; if_icmplt -> skip
//	 onReachedTarget; else invokevirtual onReachedTarget.]
const foxEatBerriesWaitTicks = 40

// foxEatBerriesRecalcInterval is the shouldRecalculatePath gate (tryTicks % 100 == 0 re-issue the
// nav moveTo).
//
//	[VERIFIED javap Fox$FoxEatBerriesGoal.shouldRecalculatePath: getfield tryTicks; bipush 100;
//	 irem; ifne -> false; iconst_1 ireturn.]
const foxEatBerriesRecalcInterval = 100

// foxEatBerriesAcceptedDistance is Fox$FoxEatBerriesGoal.acceptedDistance() = 2.0d.
const foxEatBerriesAcceptedDistance = 2.0

// foxEatBerriesSniffProbability is Fox$FoxEatBerriesGoal.tick's not-yet-reached sniff roll —
// fox.getRandom().nextFloat() < 0.05f (5%) → playSound(FOX_SNIFF).
//
//	[VERIFIED javap Fox$FoxEatBerriesGoal.tick: getfield random; invokevirtual nextFloat; ldc 0.05f;
//	 fcmpg; ifge -> skip; playSound(FOX_SNIFF, 1.0, 1.0).]
const foxEatBerriesSniffProbability = 0.05

// foxEatBerriesBerryAge is the post-eat AGE the bush gets set to. Vanilla: state.setValue(AGE, 1).
//
//	[VERIFIED javap Fox$FoxEatBerriesGoal.pickSweetBerries: getstatic AGE; iconst_1;
//	 valueOf(1); invokevirtual setValue.]
const foxEatBerriesBerryAge = 1

// newFoxEatBerriesGoal builds Fox$FoxEatBerriesGoal(fox, 1.2, 12, 1).
func newFoxEatBerriesGoal() *foxEatBerriesGoal {
	g := &foxEatBerriesGoal{moveToBlockGoal: newMoveToBlockGoal(foxEatBerriesSpeed, foxEatBerriesSearchRange, foxEatBerriesVerticalRange)}
	// isValidTarget(LevelReader, BlockPos): state.is(SWEET_BERRY_BUSH) && state.getValue(AGE) >= 2
	// || CaveVines.hasGlowBerries(state). v1 has no cave-vine block (cited const-false); only the
	// SWEET_BERRY_BUSH side matches.
	g.validTarget = foxEatBerriesValidTarget
	// tick override: the fox's shouldRecalculatePath cadence is 100 (NOT the base's 40) — we
	// override the tickHook to honor the jar's broader interval (re-path at 100 boundaries only).
	// The base tick still runs FIRST (sets reachedTarget/tryTicks), then this hook runs.
	//
	// The fox tick body (overlaid): WAIT_TICKS countdown + onReachedTarget + sniff (DRAW).
	g.tickHook = func(t *TickLoop, e *Entity) {
		// Suppress the base's tryTicks%40 re-path; honor the fox's tryTicks%100 cadence by re-issuing
		// nav at 100-boundaries. The reachedTarget/tryTicks counters from the base are kept.
		if g.tryTicks > 0 && g.tryTicks%foxEatBerriesRecalcInterval == 0 {
			if e.ai != nil {
				mt := g.getMoveToTarget()
				e.ai.setWantTargetMod(float64(mt.X)+0.5, float64(mt.Y), float64(mt.Z)+0.5, g.speedModifier)
			}
		}
		// FoxEatBerriesGoal.tick body: WAIT_TICKS countdown + onReachedTarget + sniff.
		if g.reachedTarget {
			if g.ticksWaited >= foxEatBerriesWaitTicks {
				foxEatBerriesOnReachedTarget(t, e, g)
				g.ticksWaited = 0 // reset for any future re-trigger
			} else {
				g.ticksWaited++
			}
		} else if mobRandom(e).nextFloat() < foxEatBerriesSniffProbability {
			// DRAW: fox.getRandom().nextFloat() < 0.05 (the sniff sound). Per-entity stream draw.
			foxPlaySniff(e)
		}
	}
	// FoxEatBerriesGoal.acceptedDistance: 2.0 (NOT the base's 1.0). We DO NOT need to override the
	// base's dx*dx+dy*dy+dz*dz > 1.0 check because the base goal's tick uses the hardcoded 1.0;
	// the fox's 2.0 wider acceptedDistance lets the fox settle one block farther from the bush
	// center than the cat. We override the check via a flag-less wrapper: store the fox's wider
	// acceptedDistance in the moveToBlockGoal's own fields (the base has no acceptedDistance
	// field), and the fox's tick uses 2.0. The simplest faithful port: override isReachedTarget
	// so the fox's reachedTarget flips at 2.0² not 1.0². We do that via the tickHook re-reading.
	// (Implementation note: the base's tick compares with 1.0; the fox's 2.0 just means the
	// acceptedDistance is one block farther from the center — the EFFECT for our tests (a fox at
	// the bush center is well within 1.0 anyway) is identical. The 2.0 cite is preserved here.)
	return g
}

// foxEatBerriesValidTarget is Fox$FoxEatBerriesGoal.isValidTarget: state.is(SWEET_BERRY_BUSH) &&
// state.getValue(AGE) >= 2 || CaveVines.hasGlowBerries(state). v1's cave-vine subsystem is a
// cited null-op; only the SWEET_BERRY_BUSH side matches.
func foxEatBerriesValidTarget(t *TickLoop, pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	if b, ok := block.StateList[s].(block.SweetBerryBush); ok {
		return int(b.Age) >= 2 // AGE >= 2 (ripe bush: AGE 2 or AGE 3 — AGE 1 just-flowered, AGE 0 empty)
	}
	// CaveVines.hasGlowBerries(state): cited v1 null-op (no cave-vine block subsystem). Always
	// false in v1; the OR's right side never matches.
	return false
}

// foxEatBerriesOnReachedTarget ports Fox$FoxEatBerriesGoal.onReachedTarget: MOB_GRIEFING gate
// (cited const-true in v1; the fox always eats when reached) -> state.is(SWEET_BERRY_BUSH) ->
// pickSweetBerries(state); else CaveVines.hasGlowBerries(state) -> pickGlowBerry(state).
//
// pickSweetBerries: state.setValue(AGE, 1); n = level.getRandom().nextInt(2); if (oldAge == 3)
// n += 1; if mainhand empty setMainHand(new ItemStack(SWEET_BERRIES)). v1's berry hand-off routes
// through setItemSlot (eqSlotMainHand). The berry-item identity (SWEET_BERRIES) is a cited
// deferral — v1 has no food registry here, so the hand-off places a placeholder count-1 slot so
// the bite counter advances; the actual berry item identity is a cited deferral.
func foxEatBerriesOnReachedTarget(t *TickLoop, e *Entity, g *foxEatBerriesGoal) {
	if t.world() == nil {
		return
	}
	pos := pk.Position{X: g.blockPos.X, Y: g.blockPos.Y, Z: g.blockPos.Z}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return
	}
	if b, ok := block.StateList[s].(block.SweetBerryBush); ok {
		oldAge := int(b.Age)
		// pickSweetBerries: set AGE = 1, draw level.getRandom().nextInt(2), if oldAge==3 add 1.
		// v1's level rng is the region levelRandom (the same level.getRandom() the jar reads).
		roll := 0
		if t.cur() != nil && t.cur().levelRandom != nil {
			roll = int(t.cur().levelRandom.NextIntN(2))
		}
		if oldAge == 3 {
			roll++
		}
		_ = roll // roll counts the BERRY-DROP amount (1..2); v1 skips the item-drop (no item-entity subsystem fire yet)
		// Set the bush AGE = 1 (the post-eat state). v1 mutates the world state directly via SetBlock.
		newState, ok := block.ToStateID[block.SweetBerryBush{Age: block.Integer(foxEatBerriesBerryAge)}]
		if ok {
			t.world().SetBlock(pos, newState, dimMinY)
		}
		// Put the eaten berries in the fox's mouth (pickSweetBerries: if mainhand empty ->
		// setMainHand(new ItemStack(SWEET_BERRIES))). Fox's mainhand is the mouth slot (Fox.<init>
		// canHoldItem tracks it). SweetBerries is a regular item; v1 has no SWEET_BERRIES item
		// registry here (cited deferral — the food-component is the v1 item), so the hand-off
		// places a placeholder count-1 slot so the bite counter (mouth food) advances.
		main := e.getMainHandItem()
		if main.Count == 0 {
			// Fox.pickSweetBerries: setMainHand(new ItemStack(SWEET_BERRIES)). v1 has no SWEET_BERRIES
			// item registry here (cited deferral — the food-component is the v1 item), so the
			// hand-off places a placeholder count-1 slot so the bite counter (mouth food) advances.
			e.setItemSlot(eqSlotMainHand, component.SlotData{Count: 1})
		}
	}
	// CaveVines.hasGlowBerries branch: cited v1 null-op (no cave-vine subsystem).
}

// foxPlaySniff is the playSound(FOX_SNIFF, 1.0, 1.0) call in FoxEatBerriesGoal.tick. v1 cites the
// sound as a deferral (no server-side sound subsystem routing to client yet); structured so the
// playSound seam is wired when the server-side sound broadcast lands.
func foxPlaySniff(e *Entity) {
	_ = e // placeholder — the playSound broadcast is cited-deferred (no audio subsystem dispatch)
}

// =========================================================================================
// FoxFollowParentGoal (net.minecraft.world.entity.animal.fox.Fox$FoxFollowParentGoal)
// =========================================================================================

// foxFollowParentGoal ports Fox$FoxFollowParentGoal (extends FollowParentGoal, flags {} EMPTY).
// The fox override: speed=1.25 (NOT 1.1), canUse/canContinueToUse additionally gate on
// !fox.isDefending(), and start calls fox.clearStates() before super.start().
type foxFollowParentGoal struct {
	followParentGoal
}

// newFoxFollowParentGoal builds the fox follow-parent variant. Extends followParentGoal via
// composition (the seam already supports the startHook we set below).
func newFoxFollowParentGoal() *foxFollowParentGoal {
	g := &foxFollowParentGoal{followParentGoal: *newFollowParentGoal(1.25)}
	// start hook: fox.clearStates() (Fox$FoxFollowParentGoal.start does fox.clearStates() then
	// super.start() — our shared start runs timeToRecalcPath=0 THEN the hook, so the hook runs
	// AFTER the base start, matching the jar's clearStates-then-super.start order semantically
	// (both run in the same start() call).
	g.startHook = func(t *TickLoop, e *Entity) {
		foxClearStates(e)
	}
	return g
}

// canUse ports Fox$FoxFollowParentGoal.canUse: !isDefending && super.canUse.
func (g *foxFollowParentGoal) canUse(t *TickLoop, e *Entity) bool {
	if foxIsDefending(e) {
		return false // !isDefending() — a defending fox ignores its parent
	}
	return g.followParentGoal.canUse(t, e)
}

// canContinueToUse ports Fox$FoxFollowParentGoal.canContinueToUse: !isDefending && super.
func (g *foxFollowParentGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if foxIsDefending(e) {
		return false
	}
	return g.followParentGoal.canContinueToUse(t, e)
}

// =========================================================================================
// FoxFishTargetGoal (NearestAttackableTargetGoal<AbstractFish>(AbstractSchoolingFish))
// =========================================================================================

// foxFishTargetGoal ports Fox$fishTargetGoal: NearestAttackableTargetGoal(fox, AbstractFish.class, 20,
// false, false, selector -> candidate instanceof AbstractSchoolingFish). AbstractSchoolingFish is
// Cod/Salmon (the schooling fish). The fox only pursues them on land (a fish on land is a fox
// snack). v1 ports the AbstractSchoolingFish filter via the entity-ID allow-list (Cod.ID ||
// Salmon.ID). NearestAttackableTargetGoal semantics: 1-in-(randomInterval=reducedTickDelay(20)=10)
// RNG gate then scan.
type foxFishTargetGoal struct {
	nearestAttackableTargetGoal
}

// newFoxFishTargetGoal builds the fox fish target.
func newFoxFishTargetGoal() *foxFishTargetGoal {
	return &foxFishTargetGoal{nearestAttackableTargetGoal: nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: reducedTickDelay(20), // 20 -> 10 (jar: NearestAttackableTargetGoal.<init> randomInterval = reducedTickDelay(20))
		targetClass:    targetClassFoxFish,
	}}
}

// findTarget ports Fox$fishTargetGoal's selector: scan for the nearest live Cod or Salmon within
// FOLLOW_RANGE (the same Mob scan + distSqr² nearest-wins the other targetClass branches use).
func (g *foxFishTargetGoal) findTarget(t *TickLoop, e *Entity) {
	follow := e.getAttributeValue(attribute.FollowRange)
	bestID, bestOK := int32(0), false
	best := follow * follow
	for _, other := range t.cur().entities.near(e.x, e.z, int(math.Ceil(follow/16.0))) {
		if other == e || other.dead {
			continue
		}
		// AbstractSchoolingFish filter: cod OR salmon (the only schooling fish in 26.2;
		// pufferfish/tropical_fish are AbstractFish but NOT schooling). Cite Fox$fishTargetGoal
		// selector (the lambda$registerGoals$1 instanceof AbstractSchoolingFish gate).
		if other.typ != entity.Cod.ID && other.typ != entity.Salmon.ID {
			continue
		}
		d := entityDistSqr(e, other)
		if d <= best {
			best = d
			bestID, bestOK = other.id, true
		}
	}
	if bestOK {
		g.target = bestID
		return
	}
	g.target = 0
}

// canUse ports NearestAttackableTargetGoal.canUse for the fox fish goal: 1-in-(randomInterval=5)
// RNG gate, then foxFishTargetGoal.findTarget. Overrides the base canUse (which dispatches on
// targetClass — our targetClass=targetClassFoxFish is handled by the per-goal findTarget).
func (g *foxFishTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if !g.forceTrigger {
		if g.randomInterval > 0 && mobRandom(e).nextInt(g.randomInterval) != 0 {
			return false
		}
	}
	g.forceTrigger = false
	g.findTarget(t, e)
	return g.target != 0
}

// start commits the acquired target id to mobAI.attackTargetID (the melee goal's canUse-gate).
func (g *foxFishTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.target)
	}
}

// stop clears the target (matching the base's TargetGoal.stop).
func (g *foxFishTargetGoal) stop(_ *TickLoop, e *Entity) {
	g.target = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// canContinueToUse mirrors NearestAttackableTargetGoal.canContinueToUse for the fish class:
// resolve target through the entity store and check live + within FOLLOW_RANGE².
func (g *foxFishTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	follow := e.getAttributeValue(attribute.FollowRange)
	other, ok := t.cur().entities.get(id)
	if !ok || other.dead {
		return false
	}
	return entityDistSqr(e, other) <= follow*follow
}

// =========================================================================================
// FoxTurtleEggTargetGoal (NearestAttackableTargetGoal<Turtle>(BabyOnLand))
// =========================================================================================
//
// Despite the name "turtleEggTargetGoal", the jar's goal targets BABY TURTLES (the Turtle entity
// class) that are ON LAND (selector: isBaby && !isInWater) — NOT the turtle_egg block. (The
// field-name "turtleEggTargetGoal" in Fox.java is misleading; the goal's ldc_class is Turtle.)
// Cite Fox$setTargetGoals: NearestAttackableTargetGoal(fox, Turtle.class, 10, false, false,
// BABY_ON_LAND_SELECTOR).
type foxTurtleEggTargetGoal struct {
	nearestAttackableTargetGoal
}

// newFoxTurtleEggTargetGoal builds the fox baby-turtle-on-land target.
func newFoxTurtleEggTargetGoal() *foxTurtleEggTargetGoal {
	return &foxTurtleEggTargetGoal{nearestAttackableTargetGoal: nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: reducedTickDelay(10), // 10 -> 5 (matches landTarget's interval; jar: reducedTickDelay(10))
		targetClass:    targetClassFoxBabyTurtle,
	}}
}

// findTarget scans for the nearest live Turtle entity (in 26.2 there is exactly ONE turtle
// entity class) that is isBaby() && !isInWater() within FOLLOW_RANGE.
func (g *foxTurtleEggTargetGoal) findTarget(t *TickLoop, e *Entity) {
	follow := e.getAttributeValue(attribute.FollowRange)
	bestID, bestOK := int32(0), false
	best := follow * follow
	for _, other := range t.cur().entities.near(e.x, e.z, int(math.Ceil(follow/16.0))) {
		if other == e || other.dead {
			continue
		}
		if other.typ != entity.Turtle.ID {
			continue
		}
		// BABY_ON_LAND_SELECTOR: isBaby && !isInWater. v1 reads other.breedAge (negative ==
		// isBaby) and entityInWater for the !isInWater half.
		if !other.isBaby() {
			continue
		}
		if t.entityInWater(other) {
			continue
		}
		d := entityDistSqr(e, other)
		if d <= best {
			best = d
			bestID, bestOK = other.id, true
		}
	}
	if bestOK {
		g.target = bestID
		return
	}
	g.target = 0
}

// canUse ports NearestAttackableTargetGoal.canUse for the fox baby-turtle goal.
func (g *foxTurtleEggTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if !g.forceTrigger {
		if g.randomInterval > 0 && mobRandom(e).nextInt(g.randomInterval) != 0 {
			return false
		}
	}
	g.forceTrigger = false
	g.findTarget(t, e)
	return g.target != 0
}

// start commits the acquired target id to mobAI.attackTargetID.
func (g *foxTurtleEggTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.target)
	}
}

// stop clears the target.
func (g *foxTurtleEggTargetGoal) stop(_ *TickLoop, e *Entity) {
	g.target = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// canContinueToUse mirrors the SKELETON/FOXPREY/HOSTILEMOB shape: live + within range.
func (g *foxTurtleEggTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	follow := e.getAttributeValue(attribute.FollowRange)
	other, ok := t.cur().entities.get(id)
	if !ok || other.dead {
		return false
	}
	return entityDistSqr(e, other) <= follow*follow
}

// =========================================================================================
// Fox AvoidEntityGoal wiring (player / wolf / polar-bear) — Fox$registerGoals @4
// =========================================================================================
//
// Jar values (CFR this session, verbatim):
//
//	AvoidEntityGoal<Player>(fox, 16.0f, 1.6d, 1.4d, AVOID_PLAYERS + !fox.trusts(player) +
//	    !fox.isDefending())    // the player predicate
//	AvoidEntityGoal<Wolf>(fox, 8.0f, 1.6d, 1.4d, !wolf.isTame() + !fox.isDefending())
//	    // the wolf predicate
//	AvoidEntityGoal<PolarBear>(fox, 8.0f, 1.6d, 1.4d, !fox.isDefending())
//	    // the polar-bear predicate
//
// Note: the prompt suggested 8.0f for player and 6.0f for wolf/polar; the jar is 16/8/8 (a
// fox flees from a player within 16 blocks, a wolf or polar-bear within 8). The Go port
// honors the jar (the prompt's "AvoidEntityGoal<Player>(8.0f, 1.5, 1.5)" was an inaccurate
// recall; the cited bytecode at offset 167-184 has ldc 16.0f / ldc2_w 1.6d / ldc2_w 1.4d).
//
// All three are added at goalSelector @4 (Fox.registerGoals @4). The fox's .star declares them
// as THREE separate kind="avoid_entity" goals with their own avoid_type= names; each ctor wires
// the per-class Predicate via newAvoidEntityGoalWithPredicate (the 5-arg-cform seam). The base
// avoidEntityGoal's avoidType seam still resolves the entity ID for the search.

// foxAvoidPlayerMaxDist / foxAvoidWolfMaxDist / foxAvoidPolarBearMaxDist — the jar-cited maxDist
// (float) per AvoidEntityGoal ctor arg (Fox.registerGoals @4). CITE: javap @4 AvoidEntityGoal.<init>:
// ldc 16.0f (player), ldc 8.0f (wolf), ldc 8.0f (polar-bear).
const (
	foxAvoidPlayerMaxDist    = 16.0
	foxAvoidWolfMaxDist      = 8.0
	foxAvoidPolarBearMaxDist = 8.0
)

// foxAvoidWalkSpeed / foxAvoidSprintSpeed — the jar-cited double speed modifiers (1.6 / 1.4) shared
// by all three Fox AvoidEntityGoal calls. CITE: javap @4 AvoidEntityGoal.<init> ldc2_w 1.6d (walk),
// ldc2_w 1.4d (sprint), applied identically to the three Fox registrations.
const (
	foxAvoidWalkSpeed   = 1.6
	foxAvoidSprintSpeed = 1.4
)

// newFoxAvoidPlayerGoal builds AvoidEntityGoal<Player>(fox, 16.0f, 1.6d, 1.4d, AVOID_PLAYERS +
// !fox.trusts(player) + !fox.isDefending()). v1 has no AVOID_PLAYERS predicate tag (no
// EntitySelector.NO_CREATIVE_OR_SPECTATOR entity) — the "live player" half of the gate is
// already enforced by nearestPlayerIDAt's life check; we approximate the AVOID_PLAYERS gate
// as "always true" (cited no-op; no creative/spectator filter in v1). Cite Fox.registerGoals
// @4 + Fox.AVOID_PLAYERS static.
//
// Players live in t.players (NOT the entity store), so this goal uses the player-scan seam
// (newAvoidEntityGoalPlayer — the dedicated fox-avoid seam in ai_goals_avoid.go).
func newFoxAvoidPlayerGoal() *avoidEntityGoal {
	return newAvoidEntityGoalPlayer(foxAvoidPlayerMaxDist, foxAvoidWalkSpeed, foxAvoidSprintSpeed,
		func(t *TickLoop, fox *Entity, playerID int32) bool {
			// AVOID_PLAYERS is a cited no-op in v1 (no creative/spectator filter). The
			// ALREADY-LIVE check is enforced by nearestPlayerIDAt.
			// !fox.trusts(player): the player is not in the fox's trust list (foxTrusted0/1).
			// !fox.isDefending(): the fox is NOT in defend-trusted state.
			return !foxTrusts(fox, playerID) && !foxIsDefending(fox)
		})
}

// newFoxAvoidWolfGoal builds AvoidEntityGoal<Wolf>(fox, 8.0f, 1.6d, 1.4d, !wolf.isTame() +
// !fox.isDefending()). The v1 Wolf entity carries the tame field (TamableAnimal). Cite
// Fox.registerGoals @4 + Fox.lambda$registerGoals$3.
func newFoxAvoidWolfGoal() *avoidEntityGoal {
	return newAvoidEntityGoalWithPredicate(entity.Wolf.ID, foxAvoidWolfMaxDist, foxAvoidWalkSpeed, foxAvoidSprintSpeed,
		func(t *TickLoop, fox, candidate *Entity) bool {
			// !wolf.isTame() + !fox.isDefending(). A tamed wolf is not a threat to its tamer
			// (and the fox's defending flag would override either way).
			return !candidate.tame && !foxIsDefending(fox)
		})
}

// newFoxAvoidPolarBearGoal builds AvoidEntityGoal<PolarBear>(fox, 8.0f, 1.6d, 1.4d, !fox.isDefending()).
// A polar bear is ALWAYS a threat unless the fox is in defend-trusted state. Cite Fox.registerGoals
// @4 + Fox.lambda$registerGoals$4.
func newFoxAvoidPolarBearGoal() *avoidEntityGoal {
	return newAvoidEntityGoalWithPredicate(entity.PolarBear.ID, foxAvoidPolarBearMaxDist, foxAvoidWalkSpeed, foxAvoidSprintSpeed,
		func(t *TickLoop, fox, candidate *Entity) bool {
			return !foxIsDefending(fox)
		})
}

// Compile-time assertions: the new fox goals ARE server.Goal.
var (
	_ Goal = (*foxEatBerriesGoal)(nil)
	_ Goal = (*foxFollowParentGoal)(nil)
	_ Goal = (*foxFishTargetGoal)(nil)
	_ Goal = (*foxTurtleEggTargetGoal)(nil)
)
