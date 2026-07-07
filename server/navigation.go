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

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
)

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
	// flag is ported 1:1 here AND now DRIVES the float-pathing: it is threaded into the A* request
	// (pathRequest.canFloat) so findAcceptedNode (node_evaluator.go) treats a WATER node as a standable
	// surface — a canFloat mob paths ACROSS water instead of routing around it. (The float-pathing was a
	// cited node-evaluator deferral; the NodeEvaluator path-malus work closed it.) Plain bool, tick-owned.
	//	[VERIFIED javap FloatGoal.<init>: getNavigation().setCanFloat(true); PathNavigation.setCanFloat(boolean).]
	canFloat bool

	// avoidSun is net.minecraft.world.entity.ai.navigation.GroundPathNavigation.avoidSun, flipped by the
	// skeleton's RestrictSunGoal (setAvoidSun(true) on start, false on stop — ai_goals_skeleton_sun.go).
	// It drives GroundPathNavigation.trimPath's avoid-sun tail (trimPathAvoidSun, this file): after a fresh
	// path is adopted (async.go pathReady.applyTo) the path is TRUNCATED at the first sky-exposed node, so a
	// day-time skeleton routes only as far as the shade extends. (Vanilla's avoid-sun is a post-A* path TRIM,
	// not a costMalus — VERIFIED CFR GroundPathNavigation.trimPath.) The flag now has its real observable
	// effect (previously a cited deferral). Plain bool, tick-owned.
	//	[VERIFIED CFR RestrictSunGoal: start setAvoidSun(true); stop setAvoidSun(false).]
	avoidSun bool

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
	return maxVisitedBudgetFor(navFollowRange)
}

// maxVisitedBudgetFor computes the A* visited-node budget for a given follow range — vanilla's
// createPathFinder(floor(FOLLOW_RANGE * 16.0)) times the searchDepthMultiplier (maxVisitedNodesMultiplier
// 0.5). A zombie's 35 follow range yields a larger budget than a pig's 16, so the longer pursuit path
// has the search depth to actually be found (Pitfall 6 / T-7-04 DoS cap still applies — it just scales
// with the mob's real follow range as vanilla does, instead of a fixed 16).
func maxVisitedBudgetFor(followRange int) int {
	return int(float64(followRange) * 16.0 * navMaxVisitedMultiplier)
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
	// followRange is the mob's FOLLOW_RANGE attribute, NOT a hardcoded constant: vanilla's PathNavigation
	// sizes both the search region (createPath: max(FOLLOW_RANGE, requiredPathLength)) and the visited-node
	// budget (createPathFinder: floor(FOLLOW_RANGE * 16) * multiplier) from this attribute. A Pig uses the
	// generic 16; a ZOMBIE uses 35 — so capping every mob at 16 left a zombie able to ACQUIRE a player up
	// to 35 blocks (the targetSelector reads the same attribute) but only PATHFIND within 16, freezing it
	// out of range past 16 blocks (the "se queda parado / no me sigue de lejos" stall the bot caught).
	//	[VERIFIED CFR PathNavigation: createPathFinder(Mth.floor(getAttributeBaseValue(FOLLOW_RANGE)*16.0));
	//	 getMaxPathLength = max((float)getAttributeValue(FOLLOW_RANGE), requiredPathLength).]
	followRangeF := e.getAttributeValue(attribute.FollowRange)
	followRangeI := int(followRangeF)
	if followRangeI < 1 {
		followRangeI = navFollowRange // defensive floor (an unset attribute would zero the search box)
		followRangeF = float64(navFollowRange)
	}

	region := snapshotRegion(t.world(), e, tx, ty, tz, followRangeI, navReachRange) // COPY (on the tick)
	req := pathRequest{
		startX: floorI(e.x), startY: floorI(e.y), startZ: floorI(e.z),
		targetX: tx, targetY: ty, targetZ: tz,
		region:      region,
		mobW:        e.width,
		mobH:        e.height,
		followRange: followRangeF,
		reachRange:  navReachRange,
		maxVisited:  maxVisitedBudgetFor(followRangeI),
		// The mob per-mob pathfinding-malus map SNAPSHOT (an immutable copy — Mob.getPathfindingMalus,
		// node_evaluator.go). newEvalNode stamps node.costMalus from it, so a fire-averse Animal (FIRE -1)
		// or a water-avoider re-costs the A* off-tick. A copy so the off-tick worker never aliases the
		// live mob's map (the Phase-8 purity contract). canFloat routes WATER as a standable node.
		malus:    mobMalusOf(e),
		canFloat: n.canFloat,
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
// rejoined). It is the navigation.isDone() INVERSE on the REAL path state.
//
// NOTE: the plugin nav handle's has_path() does NOT call this — has_path() reads mobAI.hasTarget
// directly (plugin_entity.go), which is the !navigation.isDone() proxy the stroll goal's
// canContinueToUse depends on. mobAI.hasTarget is set on path_to/setWantTarget and CLEARED ON ARRIVAL
// by markArrived (which clears BOTH navigation.hasTarget and mobAI.hasTarget), so it correctly goes
// false when the mob reaches its target — ending the goal and letting it re-roll. (The earlier wander
// freeze — has_path() staying true forever — was fixed by markArrived clearing hasTarget on arrival,
// NOT by an alternative active()-based path; that alternative was removed as dead code.) active()
// itself remains the direct real-path-state observer used by diagnostics/tests.
func (n *groundNavigation) active() bool {
	if n.pending {
		return true // a compute is in flight — a path is coming; don't re-request yet
	}
	return n.path != nil && !n.path.done()
}

// setAvoidSun ports GroundPathNavigation.setAvoidSun(boolean): flip the avoid-sun path-trim flag. The
// skeleton RestrictSunGoal drives it (start true / stop false); trimPathAvoidSun (this file) consumes it
// on path adoption to truncate the path at the first sky-exposed node.
func (n *groundNavigation) setAvoidSun(b bool) { n.avoidSun = b }

// mobMalusOf returns an IMMUTABLE COPY of the mob's per-mob pathfinding-malus map (mobAI.malus,
// node_evaluator.go mobMalus) to thread into the A* pathRequest. A nil-ai mob (a hand-built test
// entity) yields the zero-value mobMalus (pure PathType defaults). The copy is the Phase-8 purity
// discipline: the off-tick worker reads a frozen snapshot, never the live mob's map. Cite Mob
// .getPathfindingMalus (the per-mob override read the A* performs per node).
func mobMalusOf(e *Entity) mobMalus {
	if e == nil || e.ai == nil {
		return mobMalus{}
	}
	return e.ai.malus.copy()
}

// trimPathAvoidSun ports net.minecraft.world.entity.ai.navigation.GroundPathNavigation.trimPath's
// avoid-sun tail (VERIFIED CFR): when avoidSun is set, if the mob is CURRENTLY sky-exposed the path is
// left whole (nowhere shaded to route to — it already burns); otherwise the path is TRUNCATED at the
// FIRST node that sees sky, so a day-time skeleton (RestrictSunGoal set avoidSun) walks only as far as
// the shade extends. It runs ON THE OWNER after a fresh path is adopted (async.go pathReady.applyTo),
// where the live *TickLoop canSeeSky read is available — the faithful post-A* trim, NOT a costMalus.
// A mob with avoidSun=false (every mob but a day-time restricted skeleton) is a zero-cost no-op.
//
//	[VERIFIED CFR GroundPathNavigation.trimPath: super.trimPath(); if (avoidSun) { if (canSeeSky(mobPos))
//	 return; for i in 0..nodeCount: if (canSeeSky(node)) { truncateNodes(i); return; } }.]
func (n *groundNavigation) trimPathAvoidSun(t *TickLoop, e *Entity) {
	if !n.avoidSun || n.path == nil {
		return
	}
	// canSeeSky(BlockPos.containing(mob.getX(), mob.getY()+0.5, mob.getZ())): the mob is already in the
	// open — nothing to trim toward (it burns regardless), so leave the path whole.
	if t.canSeeSky(e) {
		return
	}
	// Truncate at the first sky-exposed node (the mob stops at the edge of the shade).
	for i := 0; i < len(n.path.nodes); i++ {
		nd := n.path.nodes[i]
		if t.canSeeSkyAt(nd.y) {
			n.path.truncateNodes(i)
			return
		}
	}
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
// markArrived records that the navigation reached (or got within reachRange of) its target. It
// clears the navigation's own hasTarget AND mobAI.hasTarget so they stay consistent — the stroll
// goal's canContinueToUse (RandomStrollGoal.canContinueToUse == !navigation.isDone()) reads
// mobAI.hasTarget as the "still navigating" proxy, so clearing it ends the goal and lets it re-roll
// a fresh stroll target next interval. Both the Go-native and plugin pig run this in serverAiStep's
// navigation.tick, so the bit-fragile oracle observes the SAME hasTarget transition on both sides.
func (n *groundNavigation) markArrived(e *Entity) {
	n.hasTarget = false
	if e.ai != nil {
		e.ai.hasTarget = false
	}
}

func (n *groundNavigation) tick(t *TickLoop, e *Entity) {
	if n.cooldown > 0 {
		n.cooldown--
	}
	if n.path == nil {
		return
	}
	if n.path.done() {
		// A fresh path is in flight (pending): the current n.path is the PREVIOUS, already-done path
		// that requestPath has NOT yet replaced (the async result rejoins 1+ ticks later via
		// applyAsyncResults). The mob has NOT arrived at the new target — it is waiting for the new
		// path. Do NOT markArrived here, or it would clobber the freshly-committed mobAI.hasTarget and
		// the mob would drop the new target before ever pathing to it. Just idle until the path lands.
		if n.pending {
			return
		}
		// No pending compute and the path is done at entry — either we arrived last tick, or the A*
		// returned a trivially-done path because the mob was already within reachRange of the target
		// (the navReachRange-vs-target-column case). EITHER WAY the navigation has arrived: mark done so
		// the stroll goal's canContinueToUse (== !navigation.isDone(), read via mobAI.hasTarget) ends
		// and the goal re-rolls a fresh target. Without this, a reachable target the A* completes one
		// block short (within reachRange) leaves mobAI.hasTarget set forever → the goal never re-rolls
		// → the mob wedges. (This is the latent arrival bug the old unreachable-underground targets
		// masked; Phase 30.1's reachable targets expose it.) markArrived clears BOTH the navigation's
		// own hasTarget and mobAI.hasTarget so the two stay consistent.
		n.markArrived(e)
		return
	}

	// followThePath (PathNavigation.followThePath, ported): advance the current node when the mob is close
	// enough on BOTH horizontal axes (per-axis maxDistanceToWaypoint, NOT euclidean) OR it can cut the
	// corner toward the next node. This per-axis + corner-cut advance is what keeps a grid A* path from
	// staircase-wobbling the heading — a euclidean radius advance kept the mob aimed at the immediate node
	// as it zigzagged, which read as "se pega vueltas de 30° a los lados". Vanilla advances at most ONE
	// node per tick here.
	//	[VERIFIED CFR PathNavigation.followThePath: maxDistanceToWaypoint = bbWidth>0.75 ? bbWidth/2 :
	//	 0.75 - bbWidth/2; advance if (|x-nodeX|<max && |z-nodeZ|<max && |y-nodeY|<1.0) ||
	//	 (canCutCorner(nextType) && shouldTargetNextNodeInDirection(mobPos)). getMaxVerticalDistanceToWaypoint
	//	 == 1.0. canCutCorner is true for a plain WALKABLE node (no FIRE/DAMAGING/DOOR types in v1).]
	{
		cur := n.path.nextNode()
		maxWp := 0.75 - e.width/2.0
		if e.width > 0.75 {
			maxWp = e.width / 2.0
		}
		xDist := math.Abs(e.x - (float64(cur.x) + 0.5))
		yDist := math.Abs(e.y - float64(cur.y))
		zDist := math.Abs(e.z - (float64(cur.z) + 0.5))
		closeEnough := xDist < maxWp && zDist < maxWp && yDist < 1.0
		if closeEnough || n.shouldTargetNextNodeInDirection(e) {
			n.path.advance()
		}
	}
	if n.path.done() {
		n.markArrived(e) // arrived: clear so the stroll goal's canContinueToUse ends it + it re-rolls
		return
	}

	next := n.path.nextNode()
	dx := float64(next.x) + 0.5 - e.x
	dz := float64(next.z) + 0.5 - e.z

	// Turn the BODY yaw toward the current path node (MoveControl.MOVE_TO: yRotD = atan2(zd,xd); setYRot
	// (rotlerp(getYRot(), yRotD, 90))). rotlerp caps the turn at 90°/tick along the shortest wrapped arc
	// so the mob curves toward the heading instead of snapping. The node advance above is the faithful
	// followThePath (per-axis maxDistanceToWaypoint + corner-cut), which is what keeps the heading from
	// staircase-wobbling. Skip rotation when essentially ON the node (vanilla MOVE_TO returns on dd tiny).
	if dx*dx+dz*dz >= 2.5e-7 {
		yRotD := yawTowardDeg(dx, dz)
		e.yaw = rotlerpDeg(e.yaw, yRotD, moveControlMaxYawStep)
		e.headYaw = e.yaw
	}

	// MOVEMENT = the ported LivingEntity.travel ground-physics (momentum + friction), NOT a fixed
	// kinematic step. Vanilla accelerates the mob ALONG its yaw each tick and lets block+air friction
	// build to a terminal velocity; a fixed step toward the waypoint (independent of the yaw and with no
	// momentum) is what made the mob feel "raro" (lunge on start, slide, moonwalk). The faithful chain:
	//
	//   frictionSpeed = getSpeed × (0.216 / blockFriction³)          [getFrictionInfluencedSpeed, ground]
	//   deltaMovement += getInputVector(forward=(0,0,1), frictionSpeed, yaw)   [moveRelative]
	//   move(deltaMovement)                                          [our moveEntity — swept collision]
	//   deltaMovement.xz ×= blockFriction × airDrag(0.91)            [travelInAir tail]
	//
	// getSpeed = speedModifier × MOVEMENT_SPEED (MoveControl.setSpeed), carried in n.speed. blockFriction
	// for a normal block is 0.6 → frictionSpeed = getSpeed × (0.216/0.216) = getSpeed, and the terminal
	// horizontal speed is getSpeed × 0.546/(1−0.546) ≈ getSpeed × 1.203 (a zombie ≈ 0.277 b/tick).
	//	[VERIFIED CFR LivingEntity.travelInAir/handleRelativeFrictionAndCalculateMovement/getFrictionInfluencedSpeed;
	//	 Entity.moveRelative/getInputVector: delta = forward.scale(speed) rotated by yaw (x·cos−z·sin, z·cos+x·sin).]
	const blockFriction = 0.6
	const airDrag = 0.91
	const friction = blockFriction * airDrag // 0.546
	frictionSpeed := n.speed * (0.21600002 / (blockFriction * blockFriction * blockFriction))

	// getInputVector(forward=(0,0,1) scaled by frictionSpeed, rotated by yaw). With input.z=1: the rotated
	// delta is (−sin·s, +cos·s) — i.e. frictionSpeed along the FACING. (input.x=0, so the x·cos/z·sin form
	// reduces to this.) Accumulate onto the horizontal velocity (deltaMovement += delta).
	yawRad := float64(e.yaw) * math.Pi / 180.0
	sinY := math.Sin(yawRad)
	cosY := math.Cos(yawRad)
	e.vx += -sinY * frictionSpeed
	e.vz += cosY * frictionSpeed

	// Jump when the next node is one block UP: a small upward Δ lifts the mob onto the ledge (gravity
	// in tickPhysics settles it). Kept as a direct Δy on the move — the vertical momentum model stays
	// with the existing physics. ORTHOGONAL to the collision engine's auto step-up (collision.go):
	// step-up covers obstacles up to maxUpStep (0.6 — slabs/stairs/snow layers) exactly as vanilla
	// Entity.collide does, while a FULL 1-block ledge is what vanilla clears by JUMPING (JumpControl);
	// this Δy is that jump's emulation, so it stays until the real jump impulse replaces it.
	var stepY float64
	if next.y > floorI(e.y) {
		stepY = float64(next.y) - e.y
	}

	// move(deltaMovement): step the mob by its accumulated horizontal velocity through the EXISTING
	// moveEntity (per-axis swept collision + onGround + re-bucket). Horizontal only here; gravity is
	// tickPhysics (Pattern 3).
	t.moveEntity(e, e.vx, stepY, e.vz)

	// travelInAir tail: apply friction so the velocity decays toward the terminal speed instead of
	// growing unbounded. (Vertical friction is handled by the physics/gravity pass.)
	e.vx *= friction
	e.vz *= friction
}

// shouldTargetNextNodeInDirection ports PathNavigation.shouldTargetNextNodeInDirection — the corner-cut
// half of followThePath. It lets the mob advance to the next node EARLY (before physically reaching the
// current one) when it is already heading past it, so a grid-staircase path is walked as a smooth
// diagonal instead of a step-by-step zigzag (the source of the side-to-side wobble). The canMoveDirectly
// LoS/collision fast-path is a cited v1 stub (no raycast subsystem); the dot-product corner test — the
// heading-reversal check that does the actual corner-cutting — is ported faithfully.
//
//	[VERIFIED CFR PathNavigation.shouldTargetNextNodeInDirection: if (nextIndex+1 >= nodeCount) false;
//	 currentNode = atBottomCenterOf(nextNodePos); if (!mobPos.closerThan(currentNode, 2.0)) false;
//	 if (canMoveDirectly(mobPos, getNextEntityPos)) true;  // v1: cited stub, skipped
//	 nextNode = atBottomCenterOf(nodePos(nextIndex+1)); mobToCurrent = currentNode - mobPos;
//	 mobToNext = nextNode - mobPos; if (mobToNextSqr < mobToCurrentSqr || mobToCurrentSqr < 0.5)
//	     return mobToNext.normalize().dot(mobToCurrent.normalize()) < 0.0;  else false.]
func (n *groundNavigation) shouldTargetNextNodeInDirection(e *Entity) bool {
	idx := n.path.idx
	if idx+1 >= len(n.path.nodes) {
		return false
	}
	cur := n.path.nodes[idx]
	// currentNode = Vec3.atBottomCenterOf(nodePos) = (x+0.5, y, z+0.5).
	curX, curY, curZ := float64(cur.x)+0.5, float64(cur.y), float64(cur.z)+0.5
	mcx, mcy, mcz := curX-e.x, curY-e.y, curZ-e.z
	// mobPos.closerThan(currentNode, 2.0): only cut the corner when within 2 blocks of the current node.
	if mcx*mcx+mcy*mcy+mcz*mcz >= 2.0*2.0 {
		return false
	}
	nxt := n.path.nodes[idx+1]
	nxX, nxY, nxZ := float64(nxt.x)+0.5, float64(nxt.y), float64(nxt.z)+0.5
	mnx, mny, mnz := nxX-e.x, nxY-e.y, nxZ-e.z
	mobToCurrentSqr := mcx*mcx + mcy*mcy + mcz*mcz
	mobToNextSqr := mnx*mnx + mny*mny + mnz*mnz
	if mobToNextSqr < mobToCurrentSqr || mobToCurrentSqr < 0.5 {
		// dot(mobToNext.normalize(), mobToCurrent.normalize()) < 0 — the next node is on the OPPOSITE side
		// of the mob from the current node, i.e. the mob has effectively passed/rounded the corner.
		cLen := math.Sqrt(mobToCurrentSqr)
		nLen := math.Sqrt(mobToNextSqr)
		if cLen < 1e-9 || nLen < 1e-9 {
			return true // degenerate (on the node) — advance
		}
		dot := (mcx*mnx + mcy*mny + mcz*mnz) / (cLen * nLen)
		return dot < 0.0
	}
	return false
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
