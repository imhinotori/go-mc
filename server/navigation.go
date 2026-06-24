package server

// navigation.go — AI-02: the ported GroundPathNavigation — requestPath (the seam call:
// snapshot -> computePath -> Path) + tick (advance the path toward the next node, feed a desired
// Δ to the EXISTING moveEntity). PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL
// paste) from the unobfuscated 26.2 jar (javap -c, this session):
//
//   net.minecraft.world.entity.ai.navigation.GroundPathNavigation / PathNavigation
//     - createPath(BlockPos, accuracy) -> createPath(Set, accuracy): builds a PathNavigationRegion
//       `new PathNavigationRegion(level, blockPos.offset(-size…), blockPos.offset(+size…))` with
//       `size = (int)(followRange + accuracy)`, then `pathFinder.findPath(region, mob, targets,
//       followRange, accuracy, maxVisitedNodesMultiplier)`. (javap createPath bytecode.)
//     - createPathFinder(maxVisited): `new PathFinder(new WalkNodeEvaluator(), maxVisited)` where
//       maxVisited = `(int)(getMaxPathLength() * 16.0)`, getMaxPathLength = `max(FOLLOW_RANGE
//       attr, requiredPathLength)`, and searchDepthMultiplier = maxVisitedNodesMultiplier
//       (DEFAULT 0.5f). (javap createPathFinder + getMaxPathLength + ctor.)
//     - tick() (javap): ++tick; if isDone return; followThePath() — advance the path node index
//       when the mob's (floor(x),floor(z)) reaches the next node's column; then feed
//       path.getNextEntityPos(mob) to moveControl.setWantedPosition(x, groundY, z, speed).
//     - shouldRecomputePath(BlockPos) / recompute throttle (timeLastRecompute, MAX_TIME_RECOMPUTE)
//       so a mob targeting an unreachable spot every tick cannot flood the A* (Security Domain /
//       Pitfall 6 / T-7-04).
//
// PATTERN 3 (07-RESEARCH, load-bearing): vanilla's moveControl turns the desired position into a
// per-tick velocity that aiStep->travel->move integrates with collision. v1 COLLAPSES that into a
// direct desired-Δ toward the next waypoint fed straight to the EXISTING moveEntity (per-axis
// swept collision + landing + re-bucket). The moved mob is AUTO-BROADCAST by the unchanged
// entityTracker (TeleportEntity/RotateHead) — NO new entity encoder. Physics (tickPhysics) runs
// AFTER tickAI so gravity settles the post-move y — do NOT reorder the pipeline.
//
// THE SEAM (the Phase-8 hinge): requestPath builds the immutable snapshot ON the tick, then calls
// the PURE computePath over ONLY the copy. In Phase 7 this is INLINE on the tick goroutine
// (TICK-05); in Phase 8 (OPT-01) the same pathRequest is handed to an ants pool and the Path
// rejoins via applyAsyncResults — ZERO logic change. NO ants/conc/xsync here — the seam is
// async-READY but executed inline.

import "math"

// --- navigation tuning (ported constants) ----------------------------------------------

const (
	// navFollowRange is the A* follow range — vanilla's FOLLOW_RANGE attribute (generic default
	// 16; a Pig uses the default). It bounds the snapshot box and the A* expansion radius.
	navFollowRange = 16

	// navReachRange is the accuracy (manhattan distance at which a target counts reached). The
	// stroll/createPath default accuracy is 1.
	navReachRange = 1

	// navMaxVisitedMultiplier is GroundPathNavigation.maxVisitedNodesMultiplier (jar DEFAULT
	// 0.5f) — the searchDepthMultiplier folded into the budget.
	navMaxVisitedMultiplier = 0.5

	// navWaypointReach is the horizontal distance (blocks) at which the mob is considered to have
	// reached the current waypoint and advances to the next. Mirrors vanilla's followThePath
	// column-match (floor(x),floor(z) == node) with a small slack so a mob centered near the node
	// advances smoothly.
	navWaypointReach = 0.5

	// navRecomputeCooldown throttles recompute (vanilla MAX_TIME_RECOMPUTE ~20 ticks): a fresh
	// path is requested at most once per this many ticks unless the target changed (Pitfall 6 /
	// T-7-04 — an unreachable-target flood cannot blow the budget).
	navRecomputeCooldown = 20
)

// groundNavigation is the per-mob path follower (ported GroundPathNavigation). It holds the
// active Path, the move speed (blocks/tick from the goal), the last requested target, and the
// recompute throttle. Tick-owned (TICK-05): requestPath + tick run on the tick goroutine.
type groundNavigation struct {
	path  *Path
	speed float64 // blocks/tick the mob walks (the goal's speedModifier scaled)

	// lastTX/Y/Z + hasTarget record the target the active path was computed for, so
	// shouldRecomputePath only rebuilds when the target actually changed (the throttle).
	lastTX, lastTY, lastTZ int
	hasTarget              bool

	// cooldown counts ticks since the last recompute (the MAX_TIME_RECOMPUTE throttle).
	cooldown int
}

// maxVisitedBudget computes the A* visited-node budget — ported from createPathFinder's
// `(int)(getMaxPathLength() * 16.0)` times the searchDepthMultiplier (maxVisitedNodesMultiplier).
// followRange*16*0.5 = followRange*8 effective expansions — the DoS cap (Pitfall 6 / T-7-04).
func maxVisitedBudget() int {
	return int(float64(navFollowRange) * 16.0 * navMaxVisitedMultiplier)
}

// requestPath ports GroundPathNavigation.createPath + moveTo: build the IMMUTABLE snapshot region
// ON the tick (snapshotRegion reads the tick-owned ChunkManager), then call the PURE computePath
// over ONLY the copy (the Phase-8 hinge). The resulting Path becomes the active path the mob
// walks. Runs on the tick goroutine, INLINE (TICK-05). Resets the recompute cooldown.
func (n *groundNavigation) requestPath(t *TickLoop, e *Entity, tx, ty, tz int) {
	region := snapshotRegion(t.world, e, tx, ty, tz, navFollowRange, navReachRange) // COPY (on the tick)
	req := pathRequest{
		startX: floorI(e.x), startY: floorI(e.y), startZ: floorI(e.z),
		targetX: tx, targetY: ty, targetZ: tz,
		region:      region,
		mobW:        e.width,
		mobH:        e.height,
		followRange: navFollowRange,
		reachRange:  navReachRange,
		maxVisited:  maxVisitedBudget(),
	}
	n.path = computePath(req) // PURE: reads ONLY req (no live world) — Phase 8 swaps the executor
	n.lastTX, n.lastTY, n.lastTZ = tx, ty, tz
	n.hasTarget = true
	n.cooldown = navRecomputeCooldown
}

// shouldRecomputePath ports PathNavigation.shouldRecomputePath + the recompute throttle: a fresh
// path is wanted iff there is no active path / it is done, OR the target changed; and recompute
// is rate-limited by the cooldown unless the target changed (so an unreachable-target flood
// cannot blow the budget — Pitfall 6 / T-7-04). Returns whether requestPath should run this tick.
func (n *groundNavigation) shouldRecomputePath(tx, ty, tz int) bool {
	targetChanged := !n.hasTarget || tx != n.lastTX || ty != n.lastTY || tz != n.lastTZ
	if targetChanged {
		return true // a new destination always justifies a recompute
	}
	if n.path == nil || n.path.done() {
		// Same target but no usable path: throttle the retry so a stuck/unreachable target does
		// not recompute every tick.
		return n.cooldown <= 0
	}
	return false // an active path toward the same target — keep following it
}

// tick ports GroundPathNavigation.tick (Pattern 3): if there is no active path or it is done,
// do nothing. Otherwise advance the node index when the mob reaches the current waypoint, then
// step the mob toward the next node by a desired Δ fed to the EXISTING moveEntity (which resolves
// collision/landing/re-bucket). Sets e.yaw toward the heading and jumps when the next node is one
// block up. The moved mob is auto-broadcast by the unchanged tracker (NO new encoder). Runs on
// the tick goroutine, INLINE.
func (n *groundNavigation) tick(t *TickLoop, e *Entity) {
	if n.cooldown > 0 {
		n.cooldown--
	}
	if n.path == nil || n.path.done() {
		return
	}

	// Advance past any waypoints the mob has already reached (followThePath's column-match).
	for !n.path.done() {
		next := n.path.nextNode()
		dx := float64(next.x) + 0.5 - e.x
		dz := float64(next.z) + 0.5 - e.z
		if dx*dx+dz*dz > navWaypointReach*navWaypointReach {
			break
		}
		n.path.advance()
	}
	if n.path.done() {
		n.hasTarget = false // arrived: clear so the stroll goal's canContinueToUse ends it
		return
	}

	next := n.path.nextNode()
	dx := float64(next.x) + 0.5 - e.x
	dz := float64(next.z) + 0.5 - e.z

	// Face the heading (the v1 LookControl analogue — yawToward sets the body yaw so the tracker
	// broadcasts a turned mob).
	e.yaw = yawTowardDeg(dx, dz)
	e.headYaw = e.yaw

	// Clamp the per-axis step to the move speed (a unit-vector × speed toward the waypoint).
	stepX, stepZ := clampStep(dx, dz, n.speed)

	// Jump when the next node is one block UP (a step the swept collision will not auto-climb):
	// give a small upward Δ so moveEntity lifts the mob onto the ledge. Gravity (tickPhysics,
	// AFTER tickAI) settles it back down on the higher floor.
	var stepY float64
	if next.y > floorI(e.y) {
		stepY = float64(next.y) - e.y // lift toward the ledge top
	}

	// Motion goes through the EXISTING moveEntity: per-axis swept collision + onGround + the
	// re-bucket (entities.move), so the mob never clips a wall and the tracker's near() stays
	// correct (Pitfall 5). Gravity is applied by tickPhysics afterward — pass 0 vertical here
	// unless jumping (Pattern 3: "gravity in tickPhysics").
	t.moveEntity(e, stepX, stepY, stepZ)
}

// clampStep returns a horizontal step (dx,dz) clamped to magnitude `speed` toward the waypoint.
// If the remaining distance is already under one step, it returns the exact remainder so the mob
// lands ON the waypoint rather than overshooting and oscillating.
func clampStep(dx, dz, speed float64) (sx, sz float64) {
	dist := math.Hypot(dx, dz)
	if dist <= 1e-9 {
		return 0, 0
	}
	if dist <= speed {
		return dx, dz // close enough: step exactly onto the waypoint
	}
	scale := speed / dist
	return dx * scale, dz * scale
}
