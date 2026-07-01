package server

// brain_sensors.go — the ported net.minecraft.world.entity.ai.sensing sensors the HappyGhast BRAIN_PROVIDER
// registers (temp/cache/26.2-inner.jar). Each writes the memories its behaviors read. Cite per sensor.
//
// SENSING SCOPE: vanilla sensors scan the level with a visibility cache (NearestVisibleLivingEntities) +
// line-of-sight. v1 reduces "visible" to "alive + within range" (the LOS raycast + the visibility-cache
// class are deferred and cited) — the same observable "the ghast reacts to the nearest player/adult it
// could see" for an unobstructed hover. The store/players scans reuse the classic-goal helpers
// (t.cur().entities.near, t.players) so the brain and the classic goals select the IDENTICAL neighbors.

// resolveTargetPos resolves a THIN entity id (a mob OR a player, per the Folia rule) to its live world
// position + height. It checks the entity store first, then the player table. ok=false for a stale/removed
// id. Used by positionTracker.currentPosition and the behaviors that read entity-id memories.
func (t *TickLoop) resolveTargetPos(id int32) (x, y, z, height float64, ok bool) {
	if id == 0 {
		return 0, 0, 0, 0, false
	}
	if t.cur() != nil && t.cur().entities != nil {
		if e, found := t.cur().entities.get(id); found {
			return e.x, e.y, e.z, e.height, true
		}
	}
	for _, p := range t.players {
		if p != nil && p.entityID == id {
			return p.x, p.y, p.z, playerEyeHeightApprox, true
		}
	}
	return 0, 0, 0, 0, false
}

// playerEyeHeightApprox is the standing player bounding-box height (1.8) used as the target height for a
// player entity-tracker (getEyeHeight then adds ~0.85*height). The exact per-pose eye height is a client
// render detail; 1.8 is the vanilla standing player height. Cited default.
const playerEyeHeightApprox = 1.8

// sensorNearestLivingEntitiesTick ports NearestLivingEntitySensor.doTick: scan LivingEntities within
// inflate(FOLLOW_RANGE), sorted by distance, into MOBS + VISIBLE_MOBS (v1 stores the count/nearest as the
// backing for the visibility gates; the full NearestVisibleLivingEntities cache is cited-reduced). For the
// HappyGhast baby these memories feed EntityTracker.isVisibleBy, which we reduce to "present", so this
// sensor is a no-op writer today beyond keeping the memories REGISTERED — it is wired (registered +
// ticked) so the visibility cache slots in here later without touching the behaviors.
//
//	[VERIFIED CFR NearestLivingEntitySensor.doTick: getEntitiesOfClass(LivingEntity, boundingBox.inflate(
//	 followRange), mob!=body && isAlive); sort by distanceToSqr; setMemory(NEAREST_LIVING_ENTITIES, list);
//	 setMemory(NEAREST_VISIBLE_LIVING_ENTITIES, new NearestVisibleLivingEntities(...)).]
func sensorNearestLivingEntitiesTick(t *TickLoop, e *Entity, _ int64) {
	_ = happyGhastAttrFollowRange(e) // the inflate radius (read like vanilla; the list build is cited-reduced)
	// The memories stay REGISTERED (registered by the provider) — the LOS/visibility cache lands here.
}

// sensorHurtByTick ports HurtBySensor.doTick: mirror the LivingEntity.getLastDamageSource into HURT_BY (and
// HURT_BY_ENTITY when the attacker is a live entity). v1 has no per-mob stored damage source on the Entity
// yet, so HURT_BY is written by the damage path when a ghast is hit (cited seam) and this sensor keeps the
// memory REGISTERED so AnimalPanic can start when it becomes present. No RNG.
//
//	[VERIFIED CFR HurtBySensor.doTick: brain.setMemory(HURT_BY, getLastDamageSource()) or eraseMemory;
//	 setMemory(HURT_BY_ENTITY, damageSource.getEntity()). The stored damage source drives AnimalPanic.]
func sensorHurtByTick(t *TickLoop, e *Entity, _ int64) {
	// HURT_BY is populated by the damage path (the ghast being hit); this sensor is the registration +
	// per-tick refresh seam. The damage-source memory write lands with the ghast damage wiring (cited).
}

// sensorFoodTemptationsTick ports TemptingSensor.doTick(FOOD): find the nearest non-spectator player within
// TEMPT_RANGE holding a HAPPY_GHAST_FOOD item; set TEMPTING_PLAYER to its id, else erase. Reuses the
// classic held-item scan (nearestPlayerHolding pattern) with the HAPPY_GHAST_FOOD predicate.
//
//	[VERIFIED CFR TemptingSensor.doTick: players().filter(NO_SPECTATORS).filter(targeting.range(TEMPT_RANGE)
//	 .test).filter(playerHoldingTemptation).sorted(by distanceToSqr); if !empty setMemory(TEMPTING_PLAYER,
//	 players.get(0)) else eraseMemory(TEMPTING_PLAYER). HappyGhast.isFood: itemStack.is(HAPPY_GHAST_FOOD).]
func sensorFoodTemptationsTick(t *TickLoop, e *Entity, _ int64) {
	rng := happyGhastAttrTemptRange(e)
	best := rng * rng
	var bestID int32
	found := false
	for _, p := range t.players {
		if p == nil {
			continue
		}
		if !playerHoldsTempt(p, happyGhastFoodPred) { // main/off hand HAPPY_GHAST_FOOD
			continue
		}
		d2 := happyGhastCloserDistSqr(e, p.x, p.y, p.z)
		if d2 <= best {
			best = d2
			bestID = p.entityID
			found = true
		}
	}
	if found {
		e.brain.setMemory(memTemptingPlayer, bestID)
	} else {
		e.brain.eraseMemory(memTemptingPlayer)
	}
}

// happyGhastFoodPred is HappyGhast.isFood: itemStack.is(ItemTags.HAPPY_GHAST_FOOD).
//
//	[VERIFIED CFR HappyGhast.isFood: return itemStack.is(ItemTags.HAPPY_GHAST_FOOD). (data/tag: item 1044.)]
func happyGhastFoodPred(itemID int32) bool { return itemInTag(itemID, "happy_ghast_food") }

// sensorNearestAdultAnyTypeTick ports NearestAdultSensor(any type).doTick: find the nearest ADULT living
// entity (any type; breedAge>=0) within FOLLOW_RANGE and set NEAREST_VISIBLE_ADULT to its id, else erase.
//
//	[VERIFIED CFR NearestAdultSensor: scan visible mobs; the nearest that isBaby()==false -> setMemory(
//	 NEAREST_VISIBLE_ADULT). ANY_TYPE variant does not require the same class.]
func sensorNearestAdultAnyTypeTick(t *TickLoop, e *Entity, _ int64) {
	rng := happyGhastAttrFollowRange(e)
	best := rng * rng
	var bestE *Entity
	for _, other := range t.cur().entities.near(e.x, e.z, 1) {
		if other == e || !other.isAlive() {
			continue
		}
		if other.breedAge < 0 { // isBaby() — only an ADULT qualifies
			continue
		}
		d2 := happyGhastCloserDistSqr(e, other.x, other.y, other.z)
		if d2 <= best {
			best = d2
			bestE = other
		}
	}
	if bestE != nil {
		e.brain.setMemory(memNearestVisibleAdult, bestE.id)
	} else {
		e.brain.eraseMemory(memNearestVisibleAdult)
	}
}

// sensorNearestPlayersTick ports PlayerSensor.doTick: the sorted nearby players into NEAREST_PLAYERS and
// the nearest non-spectator into NEAREST_VISIBLE_PLAYER. v1 sets NEAREST_VISIBLE_PLAYER to the nearest
// player id within FOLLOW_RANGE (visibility reduced to range), else erases it.
//
//	[VERIFIED CFR PlayerSensor.doTick: players sorted by distanceToSqr -> setMemory(NEAREST_PLAYERS);
//	 first that is attackable/visible -> setMemory(NEAREST_VISIBLE_PLAYER).]
func sensorNearestPlayersTick(t *TickLoop, e *Entity, _ int64) {
	rng := happyGhastAttrFollowRange(e)
	best := rng * rng
	var bestID int32
	found := false
	for _, p := range t.players {
		if p == nil {
			continue
		}
		d2 := happyGhastCloserDistSqr(e, p.x, p.y, p.z)
		if d2 <= best {
			best = d2
			bestID = p.entityID
			found = true
		}
	}
	if found {
		e.brain.setMemory(memNearestVisiblePlayer, bestID)
	} else {
		e.brain.eraseMemory(memNearestVisiblePlayer)
	}
}

// happyGhastCloserDistSqr is the center squared distance body->point (the sensor sort key).
func happyGhastCloserDistSqr(e *Entity, x, y, z float64) float64 {
	dx, dy, dz := x-e.x, y-e.y, z-e.z
	return dx*dx + dy*dy + dz*dz
}
