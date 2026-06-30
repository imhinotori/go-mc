package server

// stroll_snap.go — Phase 30.1: the RNG-FREE shared-runtime snap/validate for the stroll goal's 10
// raw candidates. PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, no GPL paste) from the
// unobfuscated 26.2 jar (javap/CFR over temp/cache/26.2-inner.jar, CONTEXT <jar_bytecode>):
//
//   - net.minecraft.world.entity.ai.util.RandomPos.generateRandomPos — the best-of-10 loop. Every
//     candidate weight == PathfinderMob.getWalkTargetValue == 0.0 (a v1 pig has no WALK_TARGET
//     memory), and the loop keeps the candidate with weight STRICTLY `> bestWeight`, so the FIRST
//     non-null (first valid) candidate wins all ties → the snap returns the first surviving candidate.
//   - net.minecraft.world.entity.ai.util.LandRandomPos.getPos(mob, 10, 7) supplier path:
//       pos = generateRandomPosTowardDirection(...)  // null if isOutsideLimits || isRestricted || isNotStable
//       return movePosUpOutOfSolid(mob, pos)         // null if isWater || hasMalus
//   - net.minecraft.world.entity.ai.util.GoalUtils predicates (the rejects).
//   - net.minecraft.world.entity.ai.util.RandomPos.moveUpOutOfSolid — snap Y up out of solid.
//   - net.minecraft.world.entity.ai.navigation.PathNavigation.isStableDestination — the reachability
//     gate (solid floor under the target).
//   - net.minecraft.world.phys.Vec3.atBottomCenterOf(pos) == (bx+0.5, by, bz+0.5) — the committed target.
//
// ARCHITECTURE SPLIT (user-approved, CONTEXT <decisions>): vanilla draws + validates inside one loop
// in the goal. Here the GOAL (Go getPosition AND both .star stroll_can_use) draws ONLY the direction
// (30 nextInt, x/y/z order — the single per-mob lockstep RNG source) and emits the 10 raw candidates;
// this snap applies the validity + ground-snap. It is RNG-FREE (it draws ZERO randoms — only
// world-solidity reads + integer arithmetic), so it is identical for the Go-native and plugin pig and
// the bit-fragile pig oracle (TestPluginPigEqualsGoNativePig) stays byte-identical. The OBSERVABLE
// behavior is 1:1 with the jar: same 30-draw stream, same candidate set, same first-valid selection,
// same atBottomCenterOf snapped target. Tick-owned (TICK-05): all reads run on the tick goroutine.

// isSolidBlock is GoalUtils.isSolid(mob, pos) == mob.level().getBlockState(pos).isSolid() — the
// solidityTester moveUpOutOfSolid loops on. It reuses t.blockSolidAt, the SAME blocksMotion()/isSolid()
// solidity snapshotRegion copies for the pathfinder, so the snap and the A* never disagree on solidity.
//
//	[VERIFIED CFR GoalUtils.isSolid: `return mob.level().getBlockState(pos).isSolid();`.]
func (t *TickLoop) isSolidBlock(bx, by, bz int) bool { return t.blockSolidAt(bx, by, bz) }

// isStableDestination is PathNavigation.isStableDestination(pos) == the reachability gate: the block
// directly BELOW pos renders solid (a solid floor to stand on). Re-expressed as blockSolidAt(x, y-1, z)
// — the floor-below walkable check the pathfinder's pathWalkable classification uses (node_evaluator.go).
//
//	[VERIFIED CFR PathNavigation.isStableDestination: `return level.getBlockState(pos.below()).isSolidRender();`.]
func (t *TickLoop) isStableDestination(bx, by, bz int) bool { return t.blockSolidAt(bx, by-1, bz) }

// moveUpOutOfSolid is RandomPos.moveUpOutOfSolid(pos, maxY, isSolid): if the candidate cell is solid,
// move Y up while up.getY() <= maxY && the cell is still solid; return the first clear y. maxY is pinned
// to maxBuildHeightY == ServerLevel.getMaxY() (block_break.go:78 — the overworld getMinY()+getHeight()
// == -64+384 == 320); the jar passes mob.level().getMaxY() here. If the cell is already clear, return by.
//
//	[VERIFIED javap RandomPos.moveUpOutOfSolid: if (solidityTester.test(pos)) { up = pos.mutable().move(UP);
//	 while (up.getY() <= maxY && solidityTester.test(up)) up.move(UP); return up.immutable(); } return pos;.]
func (t *TickLoop) moveUpOutOfSolid(bx, by, bz int) int {
	if !t.isSolidBlock(bx, by, bz) {
		return by
	}
	y := by + 1
	for y <= maxBuildHeightY && t.isSolidBlock(bx, y, bz) {
		y++
	}
	return y
}

// isOutsideLimits is GoalUtils.isOutsideLimits(pos, mob) == mob.level().isOutsideBuildHeight(pos.getY())
// — reject a candidate whose Y is below the build floor (dimMinY) or above the ceiling (maxBuildHeightY
// == ServerLevel.getMaxY(), the SAME bound moveUpOutOfSolid's maxY uses). A flat-world pig at Y≈64 with
// ±7 vertical offset never trips this, but it is a CITED guard at the vanilla default, never baked away.
//
//	[VERIFIED CFR GoalUtils.isOutsideLimits: `return mob.level().isOutsideBuildHeight(pos.getY());`
//	 — Level.isOutsideBuildHeight(y) = y < getMinBuildHeight() || y >= getMaxBuildHeight().]
func (t *TickLoop) isOutsideLimits(by int) bool { return by < dimMinY || by > maxBuildHeightY }

// isRestricted is GoalUtils.isRestricted(restrict, mob, pos) == restrict && !mob.isWithinHome(pos). A
// v1 passive pig has restrict=false (no home/leash), so this is ALWAYS false — a CITED no-op at the
// vanilla default so a future home-bound mob (the WALK_TARGET / restrictTo path) slots in its leash.
//
//	[VERIFIED CFR GoalUtils.isRestricted: `return restrict && !mob.isWithinHome(pos);` — pig restrict=false.]
func (t *TickLoop) isRestricted(_ *Entity, _, _, _ int) bool { return false }

// isWaterAt is GoalUtils.isWater(mob, pos) == mob.level().getFluidState(pos).is(FluidTags.WATER) —
// reject a candidate whose snapped cell is water. A flat-world pig never strolls onto water, and the
// fluid-tag read for an arbitrary world pos is not yet wired into the tick-owned solidity read, so this
// is a CITED stub at the vanilla default (false). UPGRADE PATH: read the real getFluidState(pos) water
// tag here when the mob nav becomes water-aware (the deferred WalkNodeEvaluator water classification).
//
//	[VERIFIED CFR GoalUtils.isWater: `return mob.level().getFluidState(pos).is(FluidTags.WATER);`.]
func (t *TickLoop) isWaterAt(_, _, _ int) bool { return false }

// hasMalus is GoalUtils.hasMalus(mob, pos) == mob.getPathfindingMalus(WalkNodeEvaluator.getPathTypeStatic
// (mob, pos)) != 0.0f — reject a candidate whose path type carries a non-zero pathfinding malus. A v1
// walkable node's malus is 0.0 (PathType WALKABLE/OPEN malus == 0.0 in node_evaluator.go's faithful
// table; the WATER/LAVA/FENCE/DOOR malus classes are deferred there), so this is ALWAYS false. CITED
// stub at the vanilla default; UPGRADE PATH: a real getPathfindingMalus(getPathTypeStatic(mob,pos)) read
// when the wider BlockPathTypes malus set lands. This is the SAME getWalkTargetValue==0.0 consequence
// that makes the first valid candidate win.
//
//	[VERIFIED CFR GoalUtils.hasMalus: `return mob.getPathfindingMalus(WalkNodeEvaluator.getPathTypeStatic
//	 (mob, pos)) != 0.0f;` — pig walkable malus 0.0.]
func (t *TickLoop) hasMalus(_ *Entity, _, _, _ int) bool { return false }

// snapStrollWant is the RNG-FREE first-valid scan over the 10 raw candidates (RandomPos.generateRandomPos
// with all weights 0.0 + the strict-`>` tie-break → the first non-null candidate wins). It walks the
// candidates IN ORDER and returns the FIRST that survives validity + the ground-snap, as
// Vec3.atBottomCenterOf(snappedPos) == (bx+0.5, snappedY, bz+0.5). If NO candidate survives, it returns
// ok=false (no want this roll — re-roll next interval). Draws ZERO randoms — the lockstep stream is
// untouched.
//
// PER-CANDIDATE VALIDATION, JAR ORDER (shared by both WaterAvoidingRandomStrollGoal.getPosition
// branches): isOutsideLimits || isRestricted || isNotStable (==!isStableDestination), THEN
// moveUpOutOfSolid (maxY = maxBuildHeightY == ServerLevel.getMaxY()), THEN isWater || hasMalus.
//
// USER-APPROVED OPTIMIZATION (CONTEXT <decisions> ARCHITECTURE DECISION + plan must_haves): the
// committed target is ALWAYS the moveUpOutOfSolid-snapped WALKABLE COLUMN (solid floor at want_y-1) —
// never a raw e.y+dy underground point. The bare DefaultRandomPos.getPos path (the ≈99.9% probability
// branch) does NOT up-snap in vanilla — it commits an underground target and lets PathNavigation
// resolve it to the nearest reachable surface node at path time. THIS server's groundNavigation marks
// "arrived" by REACHING THE TARGET BLOCK, so an underground target would leave hasTarget true forever
// (the mob walks to the surface XZ but never "arrives" → never re-rolls → the WEDGE). Eagerly
// up-snapping every committed target to the same surface column the vanilla A* would resolve it to is
// the PERMITTED optimization: it preserves the observable gameplay (the pig walks to that surface
// column either way) while keeping our arrival detection well-defined. The probability nextFloat()
// draw is still taken in the goal (lockstep) and m.wantLandMode is recorded for a future upgrade where
// the navigation resolves underground targets natively (then the DefaultRandomPos no-up-snap path can
// be restored verbatim); for now both branches snap. CITED so it becomes a real branch later.
func (m *mobAI) snapStrollWant(t *TickLoop, e *Entity) (x, y, z float64, ok bool) {
	for _, c := range m.wantCands {
		// BlockPos.containing(cand) — floor each axis (negative-correct).
		bx, by, bz := floorI(c[0]), floorI(c[1]), floorI(c[2])
		// generateRandomPosTowardDirection rejects: isOutsideLimits || isRestricted, then isNotStable.
		if t.isOutsideLimits(by) || t.isRestricted(e, bx, by, bz) {
			continue
		}
		if !t.isStableDestination(bx, by, bz) { // isNotStable == !isStableDestination
			continue
		}
		// movePosUpOutOfSolid: snap Y up out of solid (maxY pinned to maxBuildHeightY == getMaxY()) —
		// the always-snap optimization (see the doc comment). For a stable candidate already standing on
		// a solid floor with a clear cell above, this is a no-op (returns by); for an underground stable
		// candidate it climbs to the first clear cell == the walkable surface above the floor.
		by = t.moveUpOutOfSolid(bx, by, bz)
		// movePosUpOutOfSolid post-rejects: isWater || hasMalus (both vanilla-default false here).
		if t.isWaterAt(bx, by, bz) || t.hasMalus(e, bx, by, bz) {
			continue
		}
		// Vec3.atBottomCenterOf(bestPos) == (bx+0.5, by, bz+0.5) — the committed walkable column.
		return float64(bx) + 0.5, float64(by), float64(bz) + 0.5, true
	}
	return 0, 0, 0, false
}
