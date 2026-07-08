package server

// brain_villager_sensors.go -- the sensors the villager BRAIN_PROVIDER needs for the PANIC leg of the
// schedule, ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this
// session): HurtBySensor (writes HURT_BY / HURT_BY_ENTITY so VillagerPanicTrigger fires on a hit) and
// VillagerHostilesSensor (writes NEAREST_HOSTILE so the villager panics when a monster is near).
// Cite: HurtBySensor.doTick, VillagerHostilesSensor / NearestVisibleLivingEntitySensor.doTick.

// villagerHurtByWindow is LivingEntity.getLastDamageSource freshness: the source (and thus HURT_BY) is
// live only while gameTime - lastDamageStamp <= 40 (VERIFIED javap LivingEntity.getLastDamageSource: if
// gameTime - lastDamageStamp > 40L the source is cleared to null). The server has no lastDamageStamp
// field yet (it lives in the off-limits combat store), so this reads lastHurtByMobTimestamp -- set at the
// SAME store-point beside lastHurtByMob (combat_mob.go: lastHurtByMobTimestamp = gameTime) -- as the hit
// gameTime. Same 40-tick window, same observable "hurt-by memory lives ~40 ticks after the last hit".
const villagerHurtByWindow = 40

// sensorVillagerHurtByTick ports HurtBySensor.doTick: if getLastDamageSource() != null set HURT_BY (and
// HURT_BY_ENTITY when the attacker is a living entity), else erase HURT_BY. Here getLastDamageSource != null
// is modeled as "a fresh hit within the 40-tick window" (lastHurtByMob set && gameTime-stamp <= 40).
//
//	[VERIFIED javap HurtBySensor.doTick: DamageSource s = body.getLastDamageSource(); if (s != null) {
//	 brain.setMemory(HURT_BY, s); if (s.getEntity() instanceof LivingEntity le) brain.setMemory(
//	 HURT_BY_ENTITY, le); } else { brain.eraseMemory(HURT_BY); brain.getMemory(HURT_BY_ENTITY).ifPresent(
//	 le -> { if (!le.isAlive()) brain.eraseMemory(HURT_BY_ENTITY); }); }. HURT_BY is stored as a bool
//	 (present == was recently hurt) since the damage-source object is not modeled on the villager brain.]
func sensorVillagerHurtByTick(t *TickLoop, e *Entity, gameTime int64) {
	fresh := e.lastHurtByMob != 0 && gameTime-int64(e.lastHurtByMobTimestamp) <= villagerHurtByWindow
	if fresh {
		e.brain.setMemory(memHurtBy, true)
		// s.getEntity() instanceof LivingEntity -> HURT_BY_ENTITY = the attacker id (THIN id, Folia rule).
		if villagerAttackerIsLiving(t, e.lastHurtByMob) {
			e.brain.setMemory(memHurtByEntity, e.lastHurtByMob)
		}
	} else {
		e.brain.eraseMemory(memHurtBy)
		// getMemory(HURT_BY_ENTITY).ifPresent: drop it when the attacker is gone/dead.
		if id, ok := e.brain.getMemoryEntityID(memHurtByEntity); ok {
			if _, _, _, _, alive := t.resolveTargetPos(id); !alive {
				e.brain.eraseMemory(memHurtByEntity)
			}
		}
	}
}

// villagerAttackerIsLiving reports whether the attacker id resolves to a live entity/player (the
// instanceof LivingEntity gate). A player or a store entity qualifies.
func villagerAttackerIsLiving(t *TickLoop, id int32) bool {
	if id == 0 {
		return false
	}
	_, _, _, _, ok := t.resolveTargetPos(id)
	return ok
}

// villagerHostileRange is VillagerHostilesSensor.ACCEPTABLE_DISTANCE_FROM_HOSTILES default (VERIFIED
// javap VillagerHostilesSensor: the per-type map defaults to 8.0f). The per-hostile-type overrides
// (creeper/zombie/... distinct radii) are cite-reduced to the 8.0 default -- the same "a monster within
// range makes the villager panic" observable.
const villagerHostileRange = 8.0

// sensorVillagerHostilesTick ports VillagerHostilesSensor.doTick (NearestVisibleLivingEntitySensor): scan
// nearby living entities, keep the closest that is a hostile (Monster category) within the acceptable
// distance, and write it to NEAREST_HOSTILE (else erase). VERIFIED VillagerHostilesSensor.isClose +
// NearestVisibleLivingEntitySensor.doTick (setMemory(NEAREST_HOSTILE, nearest) / eraseMemory). The
// Monster-category proxy stands in for the explicit ACCEPTABLE_DISTANCE_FROM_HOSTILES type set (same
// hostile membership for the vanilla raid/monster mobs).
func sensorVillagerHostilesTick(t *TickLoop, e *Entity, _ int64) {
	best := villagerHostileRange * villagerHostileRange
	var bestID int32
	found := false
	region := t.cur()
	if region == nil || region.entities == nil {
		e.brain.eraseMemory(memNearestHostile)
		return
	}
	for _, other := range region.entities.near(e.x, e.z, 1) {
		if other == e || !other.isAlive() {
			continue
		}
		if categoryOf(other.typ) != categoryMonster { // the Enemy/Monster proxy for a hostile
			continue
		}
		dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best {
			best = d2
			bestID = other.id
			found = true
		}
	}
	if found {
		e.brain.setMemory(memNearestHostile, bestID)
	} else {
		e.brain.eraseMemory(memNearestHostile)
	}
}
