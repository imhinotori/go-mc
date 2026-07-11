package server

// entity_sun_avoid.go - the Entity-level thin wrappers for the sun-avoidance pathfinding flag that
// net.minecraft.world.entity.ai.navigation.GroundPathNavigation.avoidSun owns (VERIFIED CFR).
//
// The flag itself is the JAR property: it lives on the navigation object (GroundPathNavigation has
// `private boolean avoidSun;`), and it is consumed by GroundPathNavigation.trimPath's tail (see
// navigation.go trimPathAvoidSun: super.trimPath() then if (avoidSun) iterate path nodes and
// truncate at the first sky-exposed one). The byte-for-byte vanilla pathway is:
//
//	RestrictSunGoal.start  -> GroundPathNavigation.setAvoidSun(true)
//	GroundPathNavigation.trimPath -> if (avoidSun) { if canSeeSky(mobPos) return; for nodes
//	                                   truncateNodes(first canSeeSky) }
//
// A day-time, HEAD-bare skeleton -> RestrictSunGoal.canUse fires -> start -> setAvoidSun(true) ->
// path computed -> on adoption (async.go pathReady.applyTo) trimPathAvoidSun truncates it at the
// first sky-exposed node -> the skeleton walks only as far as the shade extends. stop() flips the
// bit back. There is NO per-node sun-exposed malus in WalkNodeEvaluator.getPathType (verified this
// session via javap -c -p net.minecraft.world.level.pathfinder.WalkNodeEvaluator.getPathType - no
// isSunSensitive/getLightLevel/SkyLight reference exists in the path-type classifier), so adding a
// path-type SUN_EXPOSED variant with a per-node malus would CONTRADICT the jar and is intentionally
// NOT done. The post-A* trim IS the jar-faithful mechanism.
//
// These Entity-level accessors are a clean-API thin wrapper (so callers can write e.setAvoidSun(b)
// instead of reaching through e.ai.navigation.setAvoidSun(b)). They delegate to the navigation
// object so the canonical state lives where the jar puts it.

// setAvoidSun forwards to the mob's GroundPathNavigation avoidSun field - RestrictSunGoal.start
// -> setAvoidSun(true), .stop -> setAvoidSun(false). Vanilla-faithful: the flag lives on the
// navigation object, not the entity (jar: GroundPathNavigation.avoidSun).
//
//	[VERIFIED CFR RestrictSunGoal.start: getNavigation() instanceof GroundPathNavigation -> true ->
//	 ((GroundPathNavigation) nav).setAvoidSun(true); .stop mirrors with false.]
func (e *Entity) setAvoidSun(b bool) {
	if e == nil || e.ai == nil {
		return
	}
	e.ai.navigation.setAvoidSun(b)
}

// isAvoidSun reports the current GroundPathNavigation avoidSun flag - true while the mob is a
// day-time restricted skeleton (RestrictSunGoal active and no HEAD helmet). Reads through the
// navigation so the canonical state is observed (no parallel Entity-side field to drift).
//
//	[VERIFIED CFR GroundPathNavigation.avoidSun - the field lives on the nav, mirror getter.]
func (e *Entity) isAvoidSun() bool {
	if e == nil || e.ai == nil {
		return false
	}
	return e.ai.navigation.avoidSun
}
