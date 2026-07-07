package server

// despawn.go is the 1:1 port of net.minecraft.world.entity.Mob.checkDespawn — the per-tick cull that
// removes a hostile/idle mob once no player has been near it for long enough. Vanilla runs it from
// ServerLevel.tick's per-entity consumer BEFORE the entity's own tick(); Sulfur runs it at the TOP of
// the tickAI per-mob loop (before serverAiStep), matching that frame order. Single owner: the tick
// goroutine over tick-owned state (TICK-05).
//
// Cited bytecode (net.minecraft.world.entity.Mob.checkDespawn):
//
//	if (level().getDifficulty() == PEACEFUL && !getType().isAllowedInPeaceful()) { discard(); return; }
//	if (isPersistenceRequired() || requiresCustomPersistence()) { noActionTime = 0; return; }
//	Player player = level().getNearestPlayer(this, -1.0);   // -1 radius => nearest non-spectator, unbounded
//	if (player == null) return;
//	double d = player.distanceToSqr(this);
//	int despawn = getType().getCategory().getDespawnDistance();
//	if (d > despawn*despawn && removeWhenFarAway(d)) discard();          // instant cull
//	int noDespawn = getType().getCategory().getNoDespawnDistance();      // == 32
//	if (noActionTime > 600 && random.nextInt(800) == 0 && d > noDespawn*noDespawn && removeWhenFarAway(d))
//	    discard();                                                       // random soft cull
//	else if (d < noDespawn*noDespawn) noActionTime = 0;                  // near a player: reset idle

// checkDespawn ports Mob.checkDespawn 1:1. Runs before serverAiStep for every live AI mob. On a cull it
// sets e.dead = true and removes the entity from its owner region (the discard() idiom used by the
// creeper/endermite/silverfish self-removals), so the tickAI loop's e.dead re-check skips the rest of
// this mob's frame. Cite Mob.checkDespawn; MobCategory.getDespawnDistance/getNoDespawnDistance;
// EntityGetter.getNearestPlayer(Entity,-1.0); Entity.distanceToSqr(Entity); Mob.removeWhenFarAway.
func (t *TickLoop) checkDespawn(e *Entity) {
	m := e.ai
	if m == nil {
		return
	}

	// `if (getDifficulty() == PEACEFUL && !getType().isAllowedInPeaceful()) { discard(); return; }`.
	// serverDifficulty is the cited NORMAL stub (food.go) — never PEACEFUL — so this branch is dead in
	// v1, but it is kept structurally so a future difficulty read culls PEACEFUL-disallowed mobs with no
	// call-site change. isAllowedInPeaceful defaults false for hostiles; unreachable here regardless.
	if serverDifficulty == difficultyPeaceful {
		e.dead = true
		t.regionForEntity(e).entities.remove(e.id) // discard()
		return
	}

	// `if (isPersistenceRequired() || requiresCustomPersistence()) { noActionTime = 0; return; }`.
	// isPersistenceRequired() -> m.persistenceRequired (false in v1: no name-tag/bucket source yet).
	// requiresCustomPersistence() -> isPassenger() || isLeashed(); v1 has no leash subsystem (const
	// false), so it reduces to the isPassenger() proxy (e.vehicle != 0).
	if m.persistenceRequired || e.vehicle != 0 {
		m.noActionTime = 0
		return
	}

	// `Player player = getNearestPlayer(this, -1.0);` — radius -1 skips the distance filter, so this is
	// the globally nearest non-spectator player. nil => nothing to measure against, return.
	player := t.nearestPlayerNoSpectator(e)
	if player == nil {
		return
	}

	// `double d = player.distanceToSqr(this);`
	d := distanceToSqrPlayer(player, e)
	cat := categoryOf(e.typ)

	// Instant cull past despawnDistance²: `if (d > despawn*despawn && removeWhenFarAway(d)) discard();`.
	despawn := cat.despawnDistance()
	if d > float64(despawn*despawn) && removeWhenFarAway(e, d) {
		e.dead = true
		t.regionForEntity(e).entities.remove(e.id) // discard()
		return
	}

	// Random soft cull vs noDespawnDistance² (== 32² == 1024): the random.nextInt(800) draw is GATED
	// behind noActionTime > 600, so a mob with a player nearby (which resets noActionTime every tick via
	// the else-branch) never reaches the draw — the pig oracle's harness keeps a player close, so its
	// per-mob RNG stream is byte-identically unperturbed. Cite Mob.checkDespawn.
	noDespawn := cat.noDespawnDistance()
	noDespawnSq := float64(noDespawn * noDespawn)
	if m.noActionTime > 600 && m.rng.nextInt(800) == 0 && d > noDespawnSq && removeWhenFarAway(e, d) {
		e.dead = true
		t.regionForEntity(e).entities.remove(e.id) // discard()
	} else if d < noDespawnSq {
		m.noActionTime = 0
	}
}

// removeWhenFarAway ports Mob.removeWhenFarAway(double). The base Mob impl returns true unconditionally
// (javap: `iconst_1; ireturn`); the Animal/TamableAnimal/named overrides return false. v1 mobs use the
// base, so this is a cited constant true, structured as a helper so a per-type override slots in later.
// Cite net.minecraft.world.entity.Mob.removeWhenFarAway.
func removeWhenFarAway(e *Entity, d float64) bool { return true }

// nearestPlayerNoSpectator ports EntityGetter.getNearestPlayer(Entity, -1.0): the nearest non-spectator,
// non-dead player with NO distance bound (the -1 radius skips the range filter). Scans the tick-owned
// loop.players seam, returning the closest by squared distance, or nil if none. The NO_SPECTATORS
// predicate maps to skipping gameModeSpectator; a dead player is skipped like getNearestPlayer's
// not-dead guard. Tick-owned read. Cite EntityGetter.getNearestPlayer + EntitySelector.NO_SPECTATORS.
func (t *TickLoop) nearestPlayerNoSpectator(e *Entity) *tickPlayer {
	best := -1.0
	var nearest *tickPlayer
	for _, p := range t.players {
		if p == nil || p.dead || p.gameMode == gameModeSpectator {
			continue
		}
		d2 := distanceToSqrPlayer(p, e)
		if nearest == nil || d2 < best {
			best = d2
			nearest = p
		}
	}
	return nearest
}
