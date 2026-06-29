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

	// canFloat is net.minecraft.world.entity.ai.navigation.PathNavigation.canFloat, set true by the
	// FloatGoal ctor (mob.getNavigation().setCanFloat(true)). It tells the node-evaluator that the mob
	// may PATH over/through water (WalkNodeEvaluator.setCanFloat → BlockPathTypes.WATER walkable). The
	// flag is ported 1:1 here; the float PATHING behavior (pathing across water surfaces) is a
	// node-evaluator concern DEFERRED + cited (CONTEXT deferred: "port the flag set; the nav float
	// behavior is a node-evaluator concern, cite if not trivially included") — FloatGoal's observable
	// (the swim-jump impulse) does not depend on float-pathing, so setting the flag fully satisfies the
	// FloatGoal ctor without the pathing port. Plain bool, tick-owned.
	//	[VERIFIED javap FloatGoal.<init>: getNavigation().setCanFloat(true); PathNavigation.setCanFloat(boolean).]
	canFloat bool

	// pending is set when an async path compute is in flight (OPT-01, 08-02): requestPath
	// SUBMITS computePath to the off-tick pathPool and sets pending=true, then pathReady.applyTo
	// clears it on the owner when the late path lands (or it is left false on a dropped/overloaded
	// submit). While pending, navigation.tick keeps following the mob's CURRENT path (or idles) —
	// it never blocks on the result — and shouldRecomputePath gates a NEW submit on !pending so a
	// mob never races two outstanding path computes (one in-flight request per mob).
	pending bool
}

// maxVisitedBudget computes the A* visited-node budget — ported from createPathFinder's
// `(int)(getMaxPathLength() * 16.0)` times the searchDepthMultiplier (maxVisitedNodesMultiplier).
// followRange*16*0.5 = followRange*8 effective expansions — the DoS cap (Pitfall 6 / T-7-04).
func maxVisitedBudget() int {
	return int(float64(navFollowRange) * 16.0 * navMaxVisitedMultiplier)
}

// requestPath ports GroundPathNavigation.createPath + moveTo and, for OPT-01 (08-02), SWAPS the
// EXECUTOR from inline to off-tick: it still builds the IMMUTABLE snapshot region ON the tick
// (snapshotRegion reads the tick-owned ChunkManager — owner-only), still builds the same immutable
// pathRequest ON the tick, then SUBMITS the PURE computePath(req) to the Wave-0 pathPool (an ants
// pool) and RETURNS IMMEDIATELY — the mob keeps its current action while the A* runs off-tick
// (paths tolerated 1+ ticks late). computePath is UNCHANGED (its purity is the contract that makes
// this a swap not a rewrite); only WHERE it runs moved.
//
// The submit closure captures ONLY immutable values — req (the snapshot copy), the mob's id, and
// the target ints — NEVER the live *Entity or *TickLoop game state (08-RESEARCH Pitfall 3). It
// computes off-tick and rejoins by sending a pathReady on asyncIn2; pathReady.applyTo (async.go)
// re-validates the mob still exists + the target is unchanged on the OWNER before adopting the late
// path. On a successful submit pending=true (so shouldRecomputePath gates a second submit). On a
// DROPPED submit (the non-blocking pool is saturated — submitOrDrop returns false) pending is left
// false and n.path is UNTOUCHED: the mob keeps its last path and shouldRecomputePath re-requests on
// a later tick (08-RESEARCH Pitfall 4, world.Worker.Request's drop-on-full discipline). The target
// tracking + recompute cooldown (the A* DoS guards) are reset exactly as before — they SURVIVE the
// swap. Runs on the tick goroutine (TICK-05).
func (n *groundNavigation) requestPath(t *TickLoop, e *Entity, tx, ty, tz int) {
	region := snapshotRegion(t.world(), e, tx, ty, tz, navFollowRange, navReachRange) // COPY (on the tick)
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

	// Capture ONLY immutable values for the off-tick worker (NEVER e or t.world() — Pitfall 3): the
	// mob id (re-resolved on apply) and the goal target (re-checked on apply), both plain values.
	mobID := e.id
	tgt := [3]int{tx, ty, tz}
	accepted := submitOrDrop(t.pathPool, func() {
		// Off-tick: the PURE A* over the immutable snapshot. The result rejoins on asyncIn2; the
		// OWNER (pathReady.applyTo) performs the only state mutation. No tick-owned state is read
		// or written here.
		p := computePath(req)
		t.asyncIn2 <- pathReady{mobID: mobID, target: tgt, path: p}
	})

	// The target tracking is updated regardless (so shouldRecomputePath sees this as the same
	// target next tick).
	n.lastTX, n.lastTY, n.lastTZ = tx, ty, tz
	n.hasTarget = true

	// The recompute cooldown anchors ONLY on an ACCEPTED submit. A DROPPED submit (the
	// non-blocking pool was saturated) must NOT arm the cooldown: if it did, a mob with no
	// usable path whose every submit drops under sustained pool contention (e.g. heavy
	// concurrent worldgen) would be throttled forever and never acquire a path (it would
	// sit motionless). Leaving the cooldown at 0 lets the very next tick retry until a submit
	// lands. An accepted submit still rate-limits the retry so a stuck/unreachable target does
	// not recompute every tick (Pitfall 6 / T-7-04).
	if accepted {
		n.cooldown = navRecomputeCooldown
	} else {
		n.cooldown = 0
	}

	// pending only reflects an ACCEPTED submit: a dropped submit must leave pending=false so the mob
	// keeps its last path and a later tick re-requests (Pitfall 4). Do NOT touch n.path here — the
	// path (if any) arrives via applyAsyncResults, 1+ ticks late.
	n.pending = accepted
}

// shouldRecomputePath ports PathNavigation.shouldRecomputePath + the recompute throttle: a fresh
// path is wanted iff there is no active path / it is done, OR the target changed; and recompute
// is rate-limited by the cooldown unless the target changed (so an unreachable-target flood
// cannot blow the budget — Pitfall 6 / T-7-04). Returns whether requestPath should run this tick.
//
// OPT-01 single-in-flight gate (08-02): while a SAME-target submit is pending (the async compute
// has not yet rejoined), no new submit fires — a mob never races two outstanding path computes,
// so a saturated/slow pool cannot accumulate duplicate work for one mob. A target CHANGE still
// supersedes a pending compute (the old result will be dropped by applyTo's retarget check), so a
// retarget is never blocked by an in-flight request for the previous goal.
// active reports whether the mob is currently navigating toward a target — there is an active
// (not-done) path, OR an async compute is in flight (a path was just requested and has not yet
// rejoined). It is the navigation.isDone() INVERSE used by the plugin nav handle's has_path():
// a declared MOVE goal asks "do I already have a path?" and re-requests only when this is false.
// CRITICAL: this reads the REAL path state, NOT mobAI.hasTarget — mobAI.hasTarget is the "a target
// is wanted" intent flag that is set on path_to and only cleared by an explicit nav.stop(), so it
// stays true forever after the first path_to. Keying has_path() off mobAI.hasTarget made a declared
// wander mob walk to its first target and then freeze (has_path() never went false, so the goal
// never re-requested). When the mob ARRIVES (path.done()) or the A* FAILS (no path, pending
// cleared), active() returns false so the goal re-requests the next target — the mob keeps moving.
func (n *groundNavigation) active() bool {
	if n.pending {
		return true // a compute is in flight — a path is coming; don't re-request yet
	}
	return n.path != nil && !n.path.done()
}

func (n *groundNavigation) shouldRecomputePath(tx, ty, tz int) bool {
	targetChanged := !n.hasTarget || tx != n.lastTX || ty != n.lastTY || tz != n.lastTZ
	if targetChanged {
		return true // a new destination always justifies a recompute (it supersedes any pending one)
	}
	if n.pending {
		return false // a same-target compute is already in flight — one request per mob (OPT-01)
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
//
// OPT-01 (08-02) late-path tolerance: tick NEVER blocks on a pending async compute. While n.pending
// is true and no path has landed yet, the path==nil/done early return simply keeps the mob doing
// its last action (following its previous path if any, else idle) — the freshly-computed path is
// adopted by pathReady.applyTo on a later tick (paths tolerated 1+ ticks late) and tick starts
// following it the next tick it runs. No change to the follow logic is needed: pending is purely a
// submit-side gate, and the existing path==nil no-op IS the tolerance.
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
