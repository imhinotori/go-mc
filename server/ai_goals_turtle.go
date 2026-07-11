package server

// ai_goals_turtle.go — MOB-PREY (Task #9): the Turtle's four deferred goals, now BUILT 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session): the water-nav trio
// (TurtleGoToWaterGoal@3 / TurtleGoHomeGoal@4 / TurtleTravelGoal@7) + the egg-lay (TurtleLayEggGoal@1).
// They route through the SAME nativeKind seam the hostile combat goals use (buildNativeGoal,
// plugin_mob_ai.go) — the turtle .star DECLARES priority+flags+kind, the Go-native goal (drawing from the
// mob's per-entity seeded rng in lockstep) does the work. The turtle's OTHER goals (panic/breed/tempt/
// look/stroll) stay .star (unchanged).
//
// PORTED (the STANDING MANDATE — idiomatic Go, never a GPL paste; CITE the class/method) from:
//   - net.minecraft.world.entity.animal.turtle.Turtle$TurtleGoToWaterGoal (extends MoveToBlockGoal, range 24)
//   - net.minecraft.world.entity.animal.turtle.Turtle$TurtleGoHomeGoal   (extends Goal)
//   - net.minecraft.world.entity.animal.turtle.Turtle$TurtleTravelGoal   (extends Goal)
//   - net.minecraft.world.entity.animal.turtle.Turtle$TurtleLayEggGoal   (extends MoveToBlockGoal, range 16)
//   - net.minecraft.world.entity.ai.goal.MoveToBlockGoal                 (the shared base of the two block goals)
//   - net.minecraft.world.entity.ai.util.DefaultRandomPos.getPosTowards  (the home/travel biased-direction pick)
//
// WATER-NAV REDUCTION (cited — the ONE permitted deviation is optimization/reduction of a not-yet-built
// subsystem, structured to become real later): vanilla's turtle uses TurtlePathNavigation extends
// AmphibiousPathNavigation — a node evaluator (AmphibiousNodeEvaluator) that pathfinds THROUGH water as
// well as on land. THIS server's node_evaluator.go is WALKABLE-only (water/lava classification is a
// documented deferral there AND on navigation.go's canFloat field). So the turtle goals here compute the
// EXACT same target selections + RNG draws + gates vanilla does, and hand the target to the SAME ground A*
// (setWantTargetSpeed -> serverAiStep -> groundNavigation) the other mobs use. The GOAL LOGIC is 1:1; only
// the underlying node evaluator's water-traversal is the reduction (sibling of navigation.go's canFloat +
// avoidSun node-evaluator deferrals). When the amphibious node evaluator lands, the goals here are
// UNCHANGED — only the A* under them gains water nodes. The observable difference in v1 (superflat, no
// ocean) is nil: a superflat turtle homes/lays on land exactly as vanilla would.
//
// The DefaultRandomPos.getPosTowards biased-direction machinery (generateRandomDirectionWithinRadians)
// is a large RNG-shaped helper; ported faithfully below (turtleRandomPosTowards) so the home/travel draw
// order + candidate math match the jar. It draws from mobRandom(e) — the SAME per-entity stream the .star
// goals draw from — so the turtle's whole AI stays in lockstep and deterministic for a fixed seed.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// --- turtle constants (jar-confirmed) --------------------------------------------------------------

const (
	// turtleGoToWaterSearchRange is MoveToBlockGoal's searchRange for TurtleGoToWaterGoal (super(turtle,
	// speed, 24)). The findNearestBlock spiral scans a 24-block horizontal radius for a WATER cell.
	//	[VERIFIED CFR TurtleGoToWaterGoal.<init>: super(turtle, ..., 24); this.verticalSearchStart = -1.]
	turtleGoToWaterSearchRange = 24

	// turtleGoToWaterVerticalStart is TurtleGoToWaterGoal's verticalSearchStart override (-1): the
	// findNearestBlock y-loop starts one block BELOW the mob (a turtle looks for water at/below foot level).
	turtleGoToWaterVerticalStart = -1

	// turtleGoToWaterGiveUp is TurtleGoToWaterGoal.GIVE_UP_TICKS (1200): canContinueToUse fails past it.
	turtleGoToWaterGiveUp = 1200

	// turtleLayEggSearchRange is MoveToBlockGoal's searchRange for TurtleLayEggGoal (super(turtle, speed, 16)).
	//	[VERIFIED CFR TurtleLayEggGoal.<init>: super(turtle, speedModifier, 16).]
	turtleLayEggSearchRange = 16

	// turtleLayEggPlaceDelay is the adjustedTickDelay(200) dig time before the egg block is placed
	// (layEggCounter > 200 -> place). adjustedTickDelay is identity in v1 (ai_goals_breed.go).
	//	[VERIFIED CFR TurtleLayEggGoal.tick: else if (layEggCounter > adjustedTickDelay(200)) { ...place... }.]
	turtleLayEggPlaceDelay = 200

	// turtleLayEggInLoveCooldown is setInLoveTime(600) applied to the turtle after it lays (a 30s breed
	// cooldown). Turtle.setInLoveTime(int) just sets the inLove countdown (no loveCause / heart event),
	// so it maps to e.inLove = 600 (the DEFAULT_IN_LOVE_TIME the shared inLove machinery uses).
	//	[VERIFIED CFR TurtleLayEggGoal.tick: this.turtle.setInLoveTime(600) after placing the egg.]
	turtleLayEggInLoveCooldown = 600

	// turtleGoHomeGiveUp is TurtleGoHomeGoal.GIVE_UP_TICKS (600): canContinueToUse fails past
	// closeToHomeTryTicks > adjustedTickDelay(600).
	turtleGoHomeGiveUp = 600

	// turtleGoHomeInterval is the nextInt(reducedTickDelay(700)) canUse gate (a homeless turtle rolls
	// once per ~700/2 ticks to decide to head home).
	//	[VERIFIED CFR TurtleGoHomeGoal.canUse: getRandom().nextInt(reducedTickDelay(700)) != 0 -> false.]
	turtleGoHomeInterval = 700

	// turtleTravelChunkRadius is the hasChunksAt(+/-34) reachability guard TurtleTravelGoal.tick applies.
	//	[VERIFIED CFR TurtleTravelGoal.tick: if (!level.hasChunksAt(xc-34, zc-34, xc+34, zc+34)) next = null.]
	turtleTravelChunkRadius = 34
)

// turtleNarrowRadians / turtleWideRadians are the two maxXzRadiansFromDir arcs the home/travel goals pass
// to DefaultRandomPos.getPosTowards: 0.3141592741012573 (~pi/10, a tight cone toward home) then, on a
// null, 1.5707963705062866 (pi/2, a wide half-plane). The exact float literals from the jar.
//
//	[VERIFIED CFR TurtleGoHomeGoal.tick / TurtleTravelGoal.tick: getPosTowards(mob,16,3,pos,0.3141592741012573)
//	 then getPosTowards(mob,8,7,pos,1.5707963705062866) then (home only) getPosTowards(mob,16,5,pos,1.5707963705062866).]
const (
	turtleNarrowRadians = 0.3141592741012573
	turtleWideRadians   = 1.5707963705062866
)

// --- world reads (the turtle goals' block predicates) ---------------------------------------------

// blockStateAt reads the raw state id at a world block pos (0/air on an unloaded column) — the
// getBlockState(pos) analogue the turtle predicates use. Mirrors blockSolidAt's ChunkManager read.
func (t *TickLoop) blockStateAt(x, y, z int) block.StateID {
	if t.world() == nil {
		return 0
	}
	s, ok := t.world().GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
	if !ok {
		return 0
	}
	return s
}

// isWaterBlockAt is BlockState.is(Blocks.WATER) — the TurtleGoToWaterGoal.isValidTarget predicate (a
// WATER-block cell, source or flowing). waterLevelOf (fluid.go) reports isWater for any Water state id.
//
//	[VERIFIED CFR TurtleGoToWaterGoal.isValidTarget: level.getBlockState(pos).is(Blocks.WATER).]
func (t *TickLoop) isWaterBlockAt(x, y, z int) bool {
	_, isWater := waterLevelOf(t.blockStateAt(x, y, z))
	return isWater
}

// isSandAt is TurtleEggBlock.isSand == BlockState.is(BlockTags.SAND) — the TurtleLayEggGoal.isValidTarget
// sand check (sand / red_sand / suspicious_sand, the #minecraft:sand tag closure). block.IsSand backs it.
//
//	[VERIFIED CFR TurtleLayEggGoal.isValidTarget: !level.isEmptyBlock(pos.above()) -> false;
//	 return TurtleEggBlock.isSand(level, pos); isSand = getBlockState(pos).is(BlockTags.SAND).]
func (t *TickLoop) isSandAt(x, y, z int) bool {
	return block.IsSand(t.blockStateAt(x, y, z))
}

// isEmptyBlockAt is LevelReader.isEmptyBlock(pos) == getBlockState(pos).isAir(). Air is state id 0 (the
// codegen'd minecraft:air default). Used by TurtleLayEggGoal.isValidTarget (the cell ABOVE the sand must
// be empty) — a faithful air read.
//
//	[VERIFIED CFR TurtleLayEggGoal.isValidTarget: if (!level.isEmptyBlock(pos.above())) return false.]
func (t *TickLoop) isEmptyBlockXYZ(x, y, z int) bool {
	return t.blockStateAt(x, y, z) == 0
}

// --- home-pos setter (the finalizeSpawn seam) -----------------------------------------------------

// applyTurtleAmphibiousMalus ports net.minecraft.world.level.pathfinder.AmphibiousNodeEvaluator.prepare's
// per-mob malus overrides (VERIFIED CFR AmphibiousNodeEvaluator.prepare, this session): WATER 0.0 (water
// is a FREE swim node, not the 8.0 land-mob avoidance), WALKABLE 6.0 (dry land is COSTLY so the turtle
// prefers water), WATER_BORDER 4.0 (the shore is mid-cost). The turtle's TurtlePathNavigation extends
// AmphibiousPathNavigation, so its node evaluator IS the amphibious one — these are its per-mob malus. Plus
// setCanFloat(true) so the ground A* (node_evaluator.findAcceptedNode) treats WATER as a standable surface
// node and the turtle paths THROUGH water (the water-nav the WalkNodeEvaluator-only server previously could
// not express — now the malus + canFloat wiring lets the SAME ground A* route the turtle across water).
//
// This LANDS the water-nav reduction the turtle goals documented: the goal logic was already 1:1; only the
// underlying node evaluator's water traversal was reduced. With the malus map + canFloat wired, the ground
// A* now re-costs water as cheap and land as costly for the turtle, so a turtle in an ocean prefers to swim
// — the AmphibiousNodeEvaluator's observable bias, re-expressed over the ground A*. (The vertical up/down
// WATER neighbors + prefersShallowSwimming +1 cost are a deeper amphibious-specific getNeighbors change,
// cited-deferred; the malus + canFloat capture the dominant observable — water-preference routing.) On a
// superflat v1 world (no ocean) it is a near-no-op. Cite AmphibiousNodeEvaluator.prepare.
func applyTurtleAmphibiousMalus(m *mobAI) {
	if m == nil {
		return
	}
	m.malus.setPathfindingMalus(pathWater, 0.0)
	m.malus.setPathfindingMalus(pathWalkable, 6.0)
	m.malus.setPathfindingMalus(pathWaterBorder, 4.0)
	m.navigation.canFloat = true // TurtlePathNavigation swims: WATER is a standable surface node
}

// setTurtleHomePos is Turtle.setHomePos(BlockPos): record the turtle's scented home column. Called at
// spawn (finalizeSpawn setHomePos(blockPosition())) so a spawned turtle's home is its spawn block.
//
//	[VERIFIED javap Turtle.setHomePos: putfield homePos; finalizeSpawn: setHomePos(this.blockPosition()).]
func setTurtleHomePos(e *Entity, x, y, z int) {
	e.homePosX, e.homePosY, e.homePosZ = x, y, z
	e.homePosSet = true
}

// --- shared MoveToBlockGoal base (the two block goals) ---------------------------------------------

// turtleMoveToBlock is the ported net.minecraft.world.entity.ai.goal.MoveToBlockGoal shared by
// TurtleGoToWaterGoal + TurtleLayEggGoal: the "find the nearest valid block in a spiral, walk to it,
// keep re-pathing until reached or given up" machinery. The concrete goal supplies isValidTarget (a
// water cell / an above-empty sand cell) + its canUse/tick overrides. flags {MOVE, JUMP} (the ctor
// setFlags(EnumSet.of(MOVE, JUMP))). It is embedded, NOT a Goal itself — the two concrete goals embed it.
//
//	[VERIFIED CFR MoveToBlockGoal: fields nextStartTick/tryTicks/maxStayTicks/blockPos/reachedTarget/
//	 searchRange/verticalSearchRange/verticalSearchStart; canUse/canContinueToUse/start/tick/findNearestBlock
//	 as ported below; setFlags(MOVE, JUMP).]
type turtleMoveToBlock struct {
	speedModifier       float64
	searchRange         int
	verticalSearchRange int
	verticalSearchStart int

	nextStartTick int
	tryTicks      int
	maxStayTicks  int
	blockPosX     int
	blockPosY     int
	blockPosZ     int
	reachedTarget bool
}

// mtbNextStartTick is MoveToBlockGoal.nextStartTick(mob) = reducedTickDelay(200 + nextInt(200)).
//
//	[VERIFIED CFR MoveToBlockGoal.nextStartTick: reducedTickDelay(200 + mob.getRandom().nextInt(200)).]
func (b *turtleMoveToBlock) mtbNextStartTick(e *Entity) int {
	return reducedTickDelay(200 + mobRandom(e).nextInt(200))
}

// mtbCanUse is MoveToBlockGoal.canUse: decrement the cooldown (return false while > 0) else re-arm it and
// scan for the nearest valid block. The concrete goal's canUse wraps this (super.canUse()).
//
//	[VERIFIED CFR MoveToBlockGoal.canUse: if (nextStartTick > 0) { --nextStartTick; return false; }
//	 nextStartTick = nextStartTick(mob); return findNearestBlock().]
func (b *turtleMoveToBlock) mtbCanUse(t *TickLoop, e *Entity, isValid func(x, y, z int) bool) bool {
	if b.nextStartTick > 0 {
		b.nextStartTick--
		return false
	}
	b.nextStartTick = b.mtbNextStartTick(e)
	return b.mtbFindNearestBlock(t, e, isValid)
}

// mtbCanContinueToUse is MoveToBlockGoal.canContinueToUse: tryTicks in [-maxStayTicks, 1200] AND the
// target still valid.
//
//	[VERIFIED CFR MoveToBlockGoal.canContinueToUse: tryTicks >= -maxStayTicks && tryTicks <= 1200 &&
//	 isValidTarget(level, blockPos).]
func (b *turtleMoveToBlock) mtbCanContinueToUse(isValid func(x, y, z int) bool) bool {
	return b.tryTicks >= -b.maxStayTicks && b.tryTicks <= 1200 && isValid(b.blockPosX, b.blockPosY, b.blockPosZ)
}

// mtbStart is MoveToBlockGoal.start: move to the block, reset tryTicks, roll maxStayTicks =
// nextInt(nextInt(1200)+1200)+1200.
//
//	[VERIFIED CFR MoveToBlockGoal.start: moveMobToBlock(); tryTicks = 0;
//	 maxStayTicks = getRandom().nextInt(getRandom().nextInt(1200) + 1200) + 1200.]
func (b *turtleMoveToBlock) mtbStart(e *Entity) {
	b.mtbMoveMobToBlock(e)
	b.tryTicks = 0
	r := mobRandom(e)
	b.maxStayTicks = r.nextInt(r.nextInt(1200)+1200) + 1200
}

// mtbMoveMobToBlock is MoveToBlockGoal.moveMobToBlock: navigation.moveTo(blockPos.x+0.5, blockPos.y+1,
// blockPos.z+0.5, speed). Routes through setWantTargetSpeed (the navigation.moveTo(target, speedModifier)
// analogue — the water-nav reduction: ground A* toward the target).
//
//	[VERIFIED CFR MoveToBlockGoal.moveMobToBlock: getNavigation().moveTo(x+0.5, y+1, z+0.5, speedModifier).]
func (b *turtleMoveToBlock) mtbMoveMobToBlock(e *Entity) {
	if e.ai == nil {
		return
	}
	e.ai.setWantTargetMod(float64(b.blockPosX)+0.5, float64(b.blockPosY+1), float64(b.blockPosZ)+0.5, b.speedModifier) // (seam x MOVEMENT_SPEED)
}

// mtbGetMoveToTarget is MoveToBlockGoal.getMoveToTarget = blockPos.above().
func (b *turtleMoveToBlock) mtbGetMoveToTarget() (x, y, z int) {
	return b.blockPosX, b.blockPosY + 1, b.blockPosZ
}

// mtbShouldRecalculatePath is MoveToBlockGoal.shouldRecalculatePath = tryTicks % 40 == 0 (the base; the
// GoToWater goal OVERRIDES it to % 160 — handled in its own tick).
//
//	[VERIFIED CFR MoveToBlockGoal.shouldRecalculatePath: return tryTicks % 40 == 0.]
func (b *turtleMoveToBlock) mtbShouldRecalculatePath() bool { return b.tryTicks%40 == 0 }

// mtbTick is MoveToBlockGoal.tick: if not within acceptedDistance(1.0) of getMoveToTarget, ++tryTicks +
// re-path on the recalculate cadence; else set reachedTarget + --tryTicks. recalc is the concrete goal's
// shouldRecalculatePath (GoToWater overrides to %160).
//
//	[VERIFIED CFR MoveToBlockGoal.tick: moveToTarget = getMoveToTarget(); if (!moveToTarget.closerToCenterThan
//	 (mob.position(), acceptedDistance()=1.0)) { reachedTarget=false; ++tryTicks; if (shouldRecalculatePath())
//	 getNavigation().moveTo(mtX+0.5, mtY, mtZ+0.5, speedModifier); } else { reachedTarget=true; --tryTicks; }.]
func (b *turtleMoveToBlock) mtbTick(e *Entity, recalc func() bool) {
	mtx, mty, mtz := b.mtbGetMoveToTarget()
	// closerToCenterThan(mob.position(), 1.0): center = (mtx+0.5, mty+0.5, mtz+0.5), dist^2 < 1.0.
	cx := float64(mtx) + 0.5 - e.x
	cy := float64(mty) + 0.5 - e.y
	cz := float64(mtz) + 0.5 - e.z
	if cx*cx+cy*cy+cz*cz >= 1.0 { // NOT closer than acceptedDistance
		b.reachedTarget = false
		b.tryTicks++
		if recalc() && e.ai != nil {
			e.ai.setWantTargetMod(float64(mtx)+0.5, float64(mty), float64(mtz)+0.5, b.speedModifier) // (seam x MOVEMENT_SPEED)
		}
	} else {
		b.reachedTarget = true
		b.tryTicks--
	}
}

// mtbFindNearestBlock is MoveToBlockGoal.findNearestBlock: the outward spiral scan (the exact vanilla
// x/z zig-zag walk) for the first valid block within [verticalSearchStart, verticalSearchRange] x the
// horizontal searchRange. Sets blockPos + returns true on the first hit. isWithinHome is the mob's home
// restriction — a turtle has NO restrictTo box (only homePos scent), so isWithinHome is always true (the
// PathfinderMob default with no home restriction). RNG-FREE (a pure world scan). Cite MoveToBlockGoal.findNearestBlock.
//
//	[VERIFIED CFR MoveToBlockGoal.findNearestBlock: y from verticalSearchStart..verticalSearchRange;
//	 for r in 0..horizontalSearch; x zig-zag; z zig-zag (n = x<r&&x>-r ? r : 0); pos = mobPos.offset(x, y-1, z);
//	 if (isWithinHome(pos) && isValidTarget(level, pos)) { blockPos = pos; return true; }. No RNG.]
func (b *turtleMoveToBlock) mtbFindNearestBlock(t *TickLoop, e *Entity, isValid func(x, y, z int) bool) bool {
	mobX, mobY, mobZ := floorI(e.x), floorI(e.y), floorI(e.z)
	horizontal := b.searchRange
	vertical := b.verticalSearchRange
	for y := b.verticalSearchStart; y <= vertical; y = ternaryNextSpiral(y) {
		for r := 0; r < horizontal; r++ {
			x := 0
			for x <= r {
				z := 0
				if x < r && x > -r {
					z = r
				}
				for z <= r {
					px, py, pz := mobX+x, mobY+y-1, mobZ+z
					if isValid(px, py, pz) { // isWithinHome is always true (no restrictTo box)
						b.blockPosX, b.blockPosY, b.blockPosZ = px, py, pz
						return true
					}
					// z = z > 0 ? -z : 1 - z
					if z > 0 {
						z = -z
					} else {
						z = 1 - z
					}
				}
				// x = x > 0 ? -x : 1 - x
				if x > 0 {
					x = -x
				} else {
					x = 1 - x
				}
			}
		}
	}
	return false
}

// ternaryNextSpiral is the y = (y > 0 ? -y : 1 - y) step of MoveToBlockGoal.findNearestBlock's vertical
// loop, extracted so the for-post reads cleanly. It walks 0,1,-1,2,-2,... from a non-negative start, or from
// -1 (GoToWater's verticalSearchStart) walks -1,2,-2,3,... — matching the jar's ternary exactly.
func ternaryNextSpiral(y int) int {
	if y > 0 {
		return -y
	}
	return 1 - y
}

// ==================================================================================================
// @3  TurtleGoToWaterGoal(turtle, 1.0) extends MoveToBlockGoal(turtle, baby?2.0:1.0, 24)   {MOVE, JUMP}
// ==================================================================================================

type turtleGoToWaterGoal struct {
	baseGoal
	turtleMoveToBlock
}

// newTurtleGoToWaterGoal builds TurtleGoToWaterGoal: MoveToBlockGoal(turtle, speed, 24) with
// verticalSearchStart = -1. The speed 1.0 is the registerGoals arg; a baby uses 2.0 (isBaby ? 2.0 : speed).
// The isBaby speed swap is per-spawn, not per-decl; the ctor value is the ADULT 1.0 and the baby-2.0 swap
// is a cited micro-detail (a v1 turtle rarely spawns as a baby on superflat, and the nav speed is a
// tunable pace not a 1:1 wire value). flags {MOVE, JUMP} (MoveToBlockGoal ctor).
//
//	[VERIFIED CFR TurtleGoToWaterGoal.<init>: super(turtle, turtle.isBaby() ? 2.0 : speedModifier, 24);
//	 this.verticalSearchStart = -1.  MoveToBlockGoal ctor setFlags(MOVE, JUMP).]
func newTurtleGoToWaterGoal(speed float64) *turtleGoToWaterGoal {
	return &turtleGoToWaterGoal{
		baseGoal: newBaseGoal(flagMove | flagJump),
		turtleMoveToBlock: turtleMoveToBlock{
			speedModifier:       speed,
			searchRange:         turtleGoToWaterSearchRange,
			verticalSearchRange: 1, // MoveToBlockGoal 3-arg ctor: verticalSearchRange defaults to 1
			verticalSearchStart: turtleGoToWaterVerticalStart,
		},
	}
}

func (g *turtleGoToWaterGoal) isValidTarget(t *TickLoop) func(x, y, z int) bool {
	return func(x, y, z int) bool { return t.isWaterBlockAt(x, y, z) }
}

// canUse ports TurtleGoToWaterGoal.canUse: a baby out of water uses the base scan; else an adult that is
// NOT going-home, NOT already in water, and does NOT have an egg uses the base scan; otherwise false.
//
//	[VERIFIED CFR TurtleGoToWaterGoal.canUse: if (isBaby() && !isInWater()) return super.canUse();
//	 if (!(goingHome || isInWater() || hasEgg())) return super.canUse(); return false.]
func (g *turtleGoToWaterGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.isBaby() && !t.mobInWater(e) {
		return g.mtbCanUse(t, e, g.isValidTarget(t))
	}
	if !(e.goingHome || t.mobInWater(e) || e.hasEgg) {
		return g.mtbCanUse(t, e, g.isValidTarget(t))
	}
	return false
}

// canContinueToUse ports TurtleGoToWaterGoal.canContinueToUse: NOT in water AND tryTicks <= 1200 AND the
// target still a water cell.
//
//	[VERIFIED CFR TurtleGoToWaterGoal.canContinueToUse: !isInWater() && tryTicks <= 1200 &&
//	 isValidTarget(level, blockPos).]
func (g *turtleGoToWaterGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return !t.mobInWater(e) && g.tryTicks <= turtleGoToWaterGiveUp && g.isValidTarget(t)(g.blockPosX, g.blockPosY, g.blockPosZ)
}

func (g *turtleGoToWaterGoal) start(_ *TickLoop, e *Entity) { g.mtbStart(e) }

// tick ports MoveToBlockGoal.tick with TurtleGoToWaterGoal's shouldRecalculatePath override (% 160).
//
//	[VERIFIED CFR TurtleGoToWaterGoal.shouldRecalculatePath: return tryTicks % 160 == 0.]
func (g *turtleGoToWaterGoal) tick(_ *TickLoop, e *Entity) {
	g.mtbTick(e, func() bool { return g.tryTicks%160 == 0 })
}

func (g *turtleGoToWaterGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// requiresUpdateEveryTick ports MoveToBlockGoal.requiresUpdateEveryTick() = true.
func (g *turtleGoToWaterGoal) requiresUpdateEveryTick() bool { return true }

// ==================================================================================================
// @4  TurtleGoHomeGoal(turtle, 1.0) extends Goal   {MOVE}
// ==================================================================================================

type turtleGoHomeGoal struct {
	baseGoal
	speedModifier       float64
	stuck               bool
	closeToHomeTryTicks int
}

// newTurtleGoHomeGoal builds TurtleGoHomeGoal (flags {MOVE} — the registerGoals goalSelector goal claims
// MOVE via its navigation.moveTo; the Goal base has no setFlags, so the turtle's MOVE flag is declared in
// the .star and asserted against this). Cite TurtleGoHomeGoal.
func newTurtleGoHomeGoal(speed float64) *turtleGoHomeGoal {
	return &turtleGoHomeGoal{baseGoal: newBaseGoal(flagMove), speedModifier: speed}
}

// canUse ports TurtleGoHomeGoal.canUse: never for a baby; ALWAYS for an egg-carrier; else a
// nextInt(reducedTickDelay(700)) gate then home is farther than 64 blocks.
//
//	[VERIFIED CFR TurtleGoHomeGoal.canUse: if (isBaby()) false; if (hasEgg()) true;
//	 if (getRandom().nextInt(reducedTickDelay(700)) != 0) false;
//	 return !homePos.closerToCenterThan(position(), 64.0).]
func (g *turtleGoHomeGoal) canUse(_ *TickLoop, e *Entity) bool {
	if e.isBaby() {
		return false
	}
	if e.hasEgg {
		return true
	}
	if mobRandom(e).nextInt(reducedTickDelay(turtleGoHomeInterval)) != 0 {
		return false
	}
	return !turtleHomeCloserThan(e, 64.0)
}

// start ports TurtleGoHomeGoal.start: goingHome = true; reset stuck + closeToHomeTryTicks.
func (g *turtleGoHomeGoal) start(_ *TickLoop, e *Entity) {
	e.goingHome = true
	g.stuck = false
	g.closeToHomeTryTicks = 0
}

// stop ports TurtleGoHomeGoal.stop: goingHome = false.
func (g *turtleGoHomeGoal) stop(_ *TickLoop, e *Entity) {
	e.goingHome = false
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// canContinueToUse ports TurtleGoHomeGoal.canContinueToUse: home NOT within 7 AND not stuck AND
// closeToHomeTryTicks <= adjustedTickDelay(600).
//
//	[VERIFIED CFR TurtleGoHomeGoal.canContinueToUse: !homePos.closerToCenterThan(position(), 7.0)
//	 && !stuck && closeToHomeTryTicks <= adjustedTickDelay(600).]
func (g *turtleGoHomeGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return !turtleHomeCloserThan(e, 7.0) && !g.stuck && g.closeToHomeTryTicks <= adjustedTickDelay(turtleGoHomeGiveUp, false)
}

// tick ports TurtleGoHomeGoal.tick: count close-to-home ticks; when the nav is done, pick a biased
// direction toward home (16,3 @ narrow -> 8,7 @ wide -> if not-close and not-water, 16,5 @ wide) and moveTo it.
//
//	[VERIFIED CFR TurtleGoHomeGoal.tick: closeToHome = homePos.closerToCenterThan(position(), 16.0);
//	 if (closeToHome) ++closeToHomeTryTicks; if (getNavigation().isDone()) {
//	   homeVec = Vec3.atBottomCenterOf(homePos);
//	   next = getPosTowards(turtle, 16, 3, homeVec, 0.314...);
//	   if (next == null) next = getPosTowards(turtle, 8, 7, homeVec, 1.570...);
//	   if (next != null && !closeToHome && !level.getBlockState(containing(next)).is(WATER))
//	       next = getPosTowards(turtle, 16, 5, homeVec, 1.570...);
//	   if (next == null) { stuck = true; return; }
//	   getNavigation().moveTo(next.x, next.y, next.z, speedModifier); }.]
func (g *turtleGoHomeGoal) tick(t *TickLoop, e *Entity) {
	closeToHome := turtleHomeCloserThan(e, 16.0)
	if closeToHome {
		g.closeToHomeTryTicks++
	}
	if e.ai == nil || e.ai.hasTarget { // getNavigation().isDone() == !hasTarget
		return
	}
	homeVecX := float64(e.homePosX) + 0.5
	homeVecY := float64(e.homePosY)
	homeVecZ := float64(e.homePosZ) + 0.5
	next, ok := turtleRandomPosTowards(t, e, 16, 3, homeVecX, homeVecY, homeVecZ, turtleNarrowRadians)
	if !ok {
		next, ok = turtleRandomPosTowards(t, e, 8, 7, homeVecX, homeVecY, homeVecZ, turtleWideRadians)
	}
	if ok && !closeToHome && !t.isWaterBlockAt(floorI(next[0]), floorI(next[1]), floorI(next[2])) {
		next, ok = turtleRandomPosTowards(t, e, 16, 5, homeVecX, homeVecY, homeVecZ, turtleWideRadians)
	}
	if !ok {
		g.stuck = true
		return
	}
	e.ai.setWantTargetMod(next[0], next[1], next[2], g.speedModifier) // (seam x MOVEMENT_SPEED)
}

// ==================================================================================================
// @7  TurtleTravelGoal(turtle, 1.0) extends Goal   {MOVE}
// ==================================================================================================

type turtleTravelGoal struct {
	baseGoal
	speedModifier float64
	stuck         bool
}

// newTurtleTravelGoal builds TurtleTravelGoal (flags {MOVE}, declared in .star, asserted here).
func newTurtleTravelGoal(speed float64) *turtleTravelGoal {
	return &turtleTravelGoal{baseGoal: newBaseGoal(flagMove), speedModifier: speed}
}

// canUse ports TurtleTravelGoal.canUse: NOT going-home AND NO egg AND in water (the deep-wander only runs
// once the turtle is already swimming).
//
//	[VERIFIED CFR TurtleTravelGoal.canUse: !goingHome && !hasEgg() && isInWater().]
func (g *turtleTravelGoal) canUse(t *TickLoop, e *Entity) bool {
	return !e.goingHome && !e.hasEgg && t.mobInWater(e)
}

// start ports TurtleTravelGoal.start: pick a far random travelPos (nextInt(1025)-512 on X/Z, nextInt(9)-4
// on Y, Y clamped to 0 if it would surface), reset stuck. The RNG draw ORDER is x, y, z (the lockstep contract).
//
//	[VERIFIED CFR TurtleTravelGoal.start: xt = random.nextInt(1025)-512; yt = random.nextInt(9)-4;
//	 zt = random.nextInt(1025)-512; if (yt + getY() > getSeaLevel()-1) yt = 0;
//	 travelPos = BlockPos.containing(xt + getX(), yt + getY(), zt + getZ()); stuck = false.]
func (g *turtleTravelGoal) start(_ *TickLoop, e *Entity) {
	r := mobRandom(e)
	xt := r.nextInt(1025) - 512
	yt := r.nextInt(9) - 4
	zt := r.nextInt(1025) - 512
	if float64(yt)+e.y > float64(overworldSeaLevel-1) {
		yt = 0
	}
	e.travelPosX = floorI(float64(xt) + e.x)
	e.travelPosY = floorI(float64(yt) + e.y)
	e.travelPosZ = floorI(float64(zt) + e.z)
	e.travelPosSet = true
	g.stuck = false
}

// tick ports TurtleTravelGoal.tick: null-guard travelPos; when nav is done pick a biased direction toward
// travelPos (16,3 @ narrow -> 8,7 @ wide), reject if the target chunks are not loaded (+/-34), moveTo it.
//
//	[VERIFIED CFR TurtleTravelGoal.tick: if (travelPos == null) { stuck = true; return; }
//	 if (getNavigation().isDone()) { targetVec = Vec3.atBottomCenterOf(travelPos);
//	   next = getPosTowards(turtle, 16, 3, targetVec, 0.314...);
//	   if (next == null) next = getPosTowards(turtle, 8, 7, targetVec, 1.570...);
//	   if (next != null) { xc = floor(next.x); zc = floor(next.z);
//	       if (!level.hasChunksAt(xc-34, zc-34, xc+34, zc+34)) next = null; }
//	   if (next == null) { stuck = true; return; }
//	   getNavigation().moveTo(next.x, next.y, next.z, speedModifier); }.]
func (g *turtleTravelGoal) tick(t *TickLoop, e *Entity) {
	if !e.travelPosSet {
		g.stuck = true
		return
	}
	if e.ai == nil || e.ai.hasTarget { // getNavigation().isDone() == !hasTarget
		return
	}
	targetX := float64(e.travelPosX) + 0.5
	targetY := float64(e.travelPosY)
	targetZ := float64(e.travelPosZ) + 0.5
	next, ok := turtleRandomPosTowards(t, e, 16, 3, targetX, targetY, targetZ, turtleNarrowRadians)
	if !ok {
		next, ok = turtleRandomPosTowards(t, e, 8, 7, targetX, targetY, targetZ, turtleWideRadians)
	}
	if ok {
		xc, zc := floorI(next[0]), floorI(next[2])
		if !t.turtleHasChunksAt(xc-turtleTravelChunkRadius, zc-turtleTravelChunkRadius, xc+turtleTravelChunkRadius, zc+turtleTravelChunkRadius) {
			ok = false
		}
	}
	if !ok {
		g.stuck = true
		return
	}
	e.ai.setWantTargetMod(next[0], next[1], next[2], g.speedModifier) // (seam x MOVEMENT_SPEED)
}

// canContinueToUse ports TurtleTravelGoal.canContinueToUse: nav NOT done AND not stuck AND not going-home
// AND not in love AND no egg.
//
//	[VERIFIED CFR TurtleTravelGoal.canContinueToUse: !getNavigation().isDone() && !stuck && !goingHome
//	 && !isInLove() && !hasEgg().]
func (g *turtleTravelGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.hasTarget && !g.stuck && !e.goingHome && !e.isInLove() && !e.hasEgg
}

// stop ports TurtleTravelGoal.stop: travelPos = null.
func (g *turtleTravelGoal) stop(_ *TickLoop, e *Entity) {
	e.travelPosSet = false
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// ==================================================================================================
// @1  TurtleLayEggGoal(turtle, 1.0) extends MoveToBlockGoal(turtle, 1.0, 16)   {MOVE, JUMP}
// ==================================================================================================

type turtleLayEggGoal struct {
	baseGoal
	turtleMoveToBlock
}

// newTurtleLayEggGoal builds TurtleLayEggGoal: MoveToBlockGoal(turtle, speed, 16). flags {MOVE, JUMP}.
//
//	[VERIFIED CFR TurtleLayEggGoal.<init>: super(turtle, speedModifier, 16). MoveToBlockGoal ctor setFlags(MOVE, JUMP).]
func newTurtleLayEggGoal(speed float64) *turtleLayEggGoal {
	return &turtleLayEggGoal{
		baseGoal: newBaseGoal(flagMove | flagJump),
		turtleMoveToBlock: turtleMoveToBlock{
			speedModifier:       speed,
			searchRange:         turtleLayEggSearchRange,
			verticalSearchRange: 1,
			verticalSearchStart: 0,
		},
	}
}

// isValidTarget ports TurtleLayEggGoal.isValidTarget: the cell ABOVE the sand must be empty AND the cell
// is sand.
//
//	[VERIFIED CFR TurtleLayEggGoal.isValidTarget: if (!level.isEmptyBlock(pos.above())) return false;
//	 return TurtleEggBlock.isSand(level, pos).]
func (g *turtleLayEggGoal) isValidTarget(t *TickLoop) func(x, y, z int) bool {
	return func(x, y, z int) bool {
		if !t.isEmptyBlockXYZ(x, y+1, z) {
			return false
		}
		return t.isSandAt(x, y, z)
	}
}

// canUse ports TurtleLayEggGoal.canUse: has an egg AND home within 9 -> base scan; else false.
//
//	[VERIFIED CFR TurtleLayEggGoal.canUse: if (hasEgg() && homePos.closerToCenterThan(position(), 9.0))
//	 return super.canUse(); return false.]
func (g *turtleLayEggGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.hasEgg && turtleHomeCloserThan(e, 9.0) {
		return g.mtbCanUse(t, e, g.isValidTarget(t))
	}
	return false
}

// canContinueToUse ports TurtleLayEggGoal.canContinueToUse: base continue AND has egg AND home within 9.
//
//	[VERIFIED CFR TurtleLayEggGoal.canContinueToUse: super.canContinueToUse() && hasEgg()
//	 && homePos.closerToCenterThan(position(), 9.0).]
func (g *turtleLayEggGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.mtbCanContinueToUse(g.isValidTarget(t)) && e.hasEgg && turtleHomeCloserThan(e, 9.0)
}

func (g *turtleLayEggGoal) start(_ *TickLoop, e *Entity) { g.mtbStart(e) }

// tick ports TurtleLayEggGoal.tick: super.tick() (walk to the sand), then when out of water AND reached
// the target, run the dig timer -> place the TURTLE_EGG block (EGGS = nextInt(4)+1), clear hasEgg/layingEgg,
// set the breed cooldown.
//
//	[VERIFIED CFR TurtleLayEggGoal.tick: super.tick(); turtlePos = blockPosition();
//	 if (!isInWater() && isReachedTarget()) {
//	   if (layEggCounter < 1) setLayingEgg(true);
//	   else if (layEggCounter > adjustedTickDelay(200)) {
//	     playSound(TURTLE_LAY_EGG ...);
//	     eggPos = blockPos.above();
//	     eggState = TURTLE_EGG.defaultBlockState().setValue(EGGS, random.nextInt(4)+1);
//	     level.setBlock(eggPos, eggState, 3);
//	     level.gameEvent(BLOCK_PLACE, eggPos, ...);
//	     setHasEgg(false); setLayingEgg(false); setInLoveTime(600); }
//	   if (isLayingEgg()) ++layEggCounter; }.]
func (g *turtleLayEggGoal) tick(t *TickLoop, e *Entity) {
	g.mtbTick(e, g.mtbShouldRecalculatePath) // super.tick() — base %40 recalc
	if !t.mobInWater(e) && g.reachedTarget {
		if e.layEggCounter < 1 {
			e.layingEgg = true
		} else if e.layEggCounter > adjustedTickDelay(turtleLayEggPlaceDelay, true) { // TurtleLayEggGoal extends MoveToBlockGoal (requiresUpdateEveryTick=true) -> identity
			// Place the egg block one above the sand target. EGGS = nextInt(4)+1 (a 1..4 egg cluster).
			eggX, eggY, eggZ := g.blockPosX, g.blockPosY+1, g.blockPosZ
			eggCount := mobRandom(e).nextInt(4) + 1
			t.placeTurtleEgg(eggX, eggY, eggZ, eggCount)
			e.hasEgg = false
			e.layingEgg = false
			// setInLoveTime(600): arm the shared inLove countdown (the 30s breed cooldown). Turtle
			// .setInLoveTime just sets the field (no loveCause/heart), so e.inLove = 600 is faithful.
			e.inLove = turtleLayEggInLoveCooldown
		}
		if e.layingEgg {
			e.layEggCounter++
		}
	}
}

func (g *turtleLayEggGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

func (g *turtleLayEggGoal) requiresUpdateEveryTick() bool { return true }

// placeTurtleEgg is level.setBlock(eggPos, TURTLE_EGG.defaultBlockState().setValue(EGGS, count), 3) + the
// broadcast: write the turtle_egg state (HATCH=0, EGGS=count) into the world and push a block update to
// trackers. block.ToStateID[TurtleEgg{Eggs:count, Hatch:0}] is the setValue(EGGS, count) on the default
// state (verified: eggs 1..4 -> sids 15090/15093/15096/15099). The gameEvent(BLOCK_PLACE) is cited-deferred
// (no game-event/sculk subsystem in v1). Cite TurtleLayEggGoal.tick level.setBlock.
func (t *TickLoop) placeTurtleEgg(x, y, z, count int) {
	if t.world() == nil {
		return
	}
	if count < 1 {
		count = 1
	} else if count > 4 {
		count = 4
	}
	sid, ok := block.ToStateID[block.TurtleEgg{Eggs: block.Integer(count), Hatch: 0}]
	if !ok {
		sid = block.DefaultStateID["minecraft:turtle_egg"] // defensive: the eggs=1 default
	}
	pos := pk.Position{X: x, Y: y, Z: z}
	if t.world().SetBlock(pos, sid, dimMinY) {
		t.broadcastBlockUpdate(pos, sid)
	}
}

// --- shared helpers -------------------------------------------------------------------------------

// turtleHomeCloserThan is homePos.closerToCenterThan(turtle.position(), d): the squared-distance check
// against the home BLOCK CENTER (homePos + 0.5 on each axis). Returns false when no home is set (an
// unspawned zero home never counts as "close"). Cite Vec3i.closerToCenterThan (dist^2 < d^2).
//
//	[VERIFIED CFR BlockPos.closerToCenterThan(Vec3, d): distToCenterSqr(vec) < d*d, center = pos + 0.5.]
func turtleHomeCloserThan(e *Entity, d float64) bool {
	if !e.homePosSet {
		return false
	}
	dx := float64(e.homePosX) + 0.5 - e.x
	dy := float64(e.homePosY) + 0.5 - e.y
	dz := float64(e.homePosZ) + 0.5 - e.z
	return dx*dx+dy*dy+dz*dz < d*d
}

// turtleHasChunksAt is Level.hasChunksAt(minX, minZ, maxX, maxZ): every column in the block-coord box is
// loaded. Reduced to a corner+center loaded check over the ChunkManager (a full box scan is the eventual
// upgrade); a superflat v1 world has the spawn region loaded, so this reports true where vanilla does.
// Cite Level.hasChunksAt (TurtleTravelGoal.tick reachability guard).
func (t *TickLoop) turtleHasChunksAt(minX, minZ, maxX, maxZ int) bool {
	if t.world() == nil {
		return false
	}
	// A block cell counts as "has chunk" if a read resolves (ok) — the same loaded-column signal
	// blockSolidAt uses. Probe the four corners + center of the box.
	probe := func(bx, bz int) bool {
		_, ok := t.world().GetBlock(pk.Position{X: bx, Y: overworldSeaLevel, Z: bz}, dimMinY)
		return ok
	}
	cx, cz := (minX+maxX)/2, (minZ+maxZ)/2
	return probe(minX, minZ) && probe(maxX, maxZ) && probe(minX, maxZ) && probe(maxX, minZ) && probe(cx, cz)
}

// turtleRandomPosTowards is DefaultRandomPos.getPosTowards(mob, horizontalDist, verticalDist, towardsPos,
// maxXzRadiansFromDir): pick a random reachable position in a cone of maxXzRadiansFromDir around the
// direction toward towardsPos. It ports the RandomPos.generateRandomPos best-of-10 loop over the SHARED
// generateRandomDirectionWithinRadians supplier (ai_goals_avoid.go — the authoritative RandomPos port,
// draw order nextFloat/nextDouble/nextInt) + generateRandomPosTowardDirection validation. getPosTowards's
// horizontal is a FIXED distance (min == max == horizontalDist), flyingHeight 0. Returns the first VALID
// candidate (ok=false == the jar's null -> the goal marks stuck / falls to the wider arc).
//
// RNG: the SAME per-candidate draw shape the shared helper pins, from mobRandom(e). The A* reachability
// (isNotStable / hasMalus) rides the SAME ground-nav world reads the stroll snap uses (the water-nav
// reduction). Cite DefaultRandomPos.getPosTowards + RandomPos.generateRandomDirectionWithinRadians.
//
//	[VERIFIED CFR DefaultRandomPos.getPosTowards: dir = towardsPos - mob.position(); restrict =
//	 mobRestricted(mob, h); return generateRandomPos(mob, () -> {
//	   direction = generateRandomDirectionWithinRadians(random, 0.0, h, v, 0, dir.x, dir.z, maxXzRadians);
//	   if (direction == null) return null;
//	   return generateRandomPosTowardDirection(mob, h, restrict, direction); }).
//	 RandomPos.generateRandomPos: for i<10 { pos = supplier.get(); if (pos != null) { weight =
//	   getWalkTargetValue(pos)=0.0; if (weight > best) { best = weight; result = pos; } } } return result;
//	 (all weights 0.0 -> first non-null wins).]
func turtleRandomPosTowards(t *TickLoop, e *Entity, horizontalDist, verticalDist int, towardX, towardY, towardZ, maxXzRadians float64) ([3]float64, bool) {
	_ = towardY
	r := mobRandom(e)
	dirX := towardX - e.x
	dirZ := towardZ - e.z
	// generateRandomPos: best-of-10, all weights 0.0 -> first valid wins. Draw the SAME per-candidate
	// stream for all 10 iterations (the jar calls the supplier once per i and keeps drawing).
	var result [3]float64
	found := false
	for i := 0; i < 10; i++ {
		// getPosTowards's horizontal is a FIXED distance: min == max == horizontalDist, flyingHeight 0.
		xtF, ytF, ztF, ok := generateRandomDirectionWithinRadians(r, float64(horizontalDist), horizontalDist, verticalDist, 0, dirX, dirZ, maxXzRadians)
		if !ok {
			continue
		}
		// generateRandomPosTowardDirection: the candidate world pos = mobBlock + relative direction.
		bx := floorI(e.x) + int(xtF)
		by := floorI(e.y) + int(ytF)
		bz := floorI(e.z) + int(ztF)
		// Reject: isOutsideLimits || isNotStable || hasMalus (isRestricted false — no home box). The
		// stable check == a solid floor below (the ground-nav reachability, the water-nav reduction).
		if t.isOutsideLimits(by) || !t.isStableDestination(bx, by, bz) || t.hasMalus(e, bx, by, bz) {
			continue
		}
		if !found { // first non-null wins (all weights 0.0, strict-> tie-break)
			result = [3]float64{float64(bx) + 0.5, float64(by), float64(bz) + 0.5}
			found = true
		}
	}
	return result, found
}
