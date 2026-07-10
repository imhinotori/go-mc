package server

// area_effect_cloud.go -- AREA EFFECT CLOUD: a 1:1 port of net.minecraft.world.entity.AreaEffectCloud
// (serverTick), decompiled from temp/cache/26.2-inner.jar (javap -c -p this session). An AEC is a
// stationary NON-mob Entity (isAreaEffectCloud) whose whole server behavior is: shrink the radius over
// time (radiusPerTick / radiusOnUse), and every 5 ticks apply its potion effects to every LivingEntity
// inside the radius, tracking a per-victim reapplication-delay cooldown. It is the lingering-potion /
// DragonFireball residue that makes lingering potions actually do something.
//
// The vanilla per-tick order (AreaEffectCloud.serverTick, verified javap):
//   1. if duration != -1 AND tickCount - waitTime >= duration -> discard() and return  (lifetime cap).
//   2. waiting = isWaiting(); nextWaiting = tickCount < waitTime; if changed -> setWaiting(nextWaiting).
//   3. if nextWaiting -> return                                                        (still in wait phase).
//   4. if radiusPerTick != 0: radius += radiusPerTick; if radius < 0.5 -> discard(); else setRadius(radius).
//   5. if tickCount % 5 == 0: expire per-victim cooldowns (removeIf tickCount >= value); if no effects clear
//        victims; else for each LivingEntity in the box not already a victim + affected: record cooldown =
//        tickCount + reapplicationDelay, apply each effect (instantaneous scale 0.5, else full copy), then
//        apply radiusOnUse (discard if <0.5) and durationOnUse (discard if <=0).
//
// SINGLE-OWNER (TICK-05): the AEC tick runs on the tick goroutine over tick-owned state, like tickPotions.

import (
	"sort"

	"github.com/imhinotori/sulfur/data/entity"
)

// AreaEffectCloud constants (verified javap AreaEffectCloud -- the exact defaults / literals).
const (
	aecMaxRadius        = 32.0 // MAX_RADIUS; setRadius clamps to [0, 32] (Mth.clamp(x, 0, 32.0f)).
	aecMinRadius        = 0.5  // discard floor: radius < 0.5f -> discard (radiusPerTick + radiusOnUse tests).
	aecApplyPeriod      = 5    // effect-application interval (tickCount % 5 == 0).
	aecInstantScale     = 0.5  // 0.5d scale for an instantaneous effect (applyInstantaneousEffect).
	aecInfiniteDuration = -1   // duration == -1 sentinel (infinite-lifetime cloud).
)

// spawnAreaEffectCloud creates an AreaEffectCloud at (x,y,z) carrying the given effects and shape, owned by
// ownerID, and adds it to the owner region store (the tracker broadcasts AddEntity next tick, exactly as a
// splash potion / arrow rides the store-add path). Cite the AreaEffectCloud ctor defaults + the caller
// setRadius/setDuration/setWaitTime/setRadiusOnUse/setRadiusPerTick overrides (ThrownLingeringPotion).
func (t *TickLoop) spawnAreaEffectCloud(ownerID int32, x, y, z float64, effects []splashEffect,
	radius float32, duration, waitTime, reapplyDelay, durationOnUse int, radiusOnUse, radiusPerTick float32) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.AreaEffectCloud, x, y, z)
	e.isAreaEffectCloud = true
	e.aecOwnerID = ownerID
	e.aecEffects = effects
	e.aecRadius = clampF32(radius, 0, aecMaxRadius) // setRadius clamps to [0, 32]
	e.aecDuration = duration
	e.aecWaitTime = waitTime
	e.aecReapplyDelay = reapplyDelay
	e.aecDurationOnUse = durationOnUse
	e.aecRadiusOnUse = radiusOnUse
	e.aecRadiusPerTick = radiusPerTick
	e.aecTickCount = 0
	e.aecWaiting = false // DATA_WAITING defaults false; first serverTick recomputes tickCount(0) < waitTime
	e.aecVictims = make(map[int32]int)

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// tickAreaEffectClouds drives every AEC in every region -- the sibling of tickPotions/tickArrows. Runs on
// the coordinator with each region registered so the AEC tick t.cur() (the discard remove) resolves to the
// AEC OWN store. A per-region snapshot keeps the loop stable across an in-loop discard. ADDITIVE + AEC-gated.
func (t *TickLoop) tickAreaEffectClouds() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isAreaEffectCloud {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickAreaEffectCloud(e)
			}
		})
	}
}

// tickAreaEffectCloud is the AreaEffectCloud.serverTick port for one cloud. Entity.tick (super.tick)
// increments tickCount at the top of the tick; v1 folds that into aecTickCount here BEFORE serverTick runs,
// matching the vanilla order (tick() -> super.tick() bumps tickCount -> serverTick reads it).
func (t *TickLoop) tickAreaEffectCloud(e *Entity) {
	e.aecTickCount++ // Entity.tick() (super.tick) bump

	// (1) Lifetime cap.
	if e.aecDuration != aecInfiniteDuration && e.aecTickCount-e.aecWaitTime >= e.aecDuration {
		t.cur().entities.remove(e.id)
		return
	}

	// (2) waiting flag.
	nextWaiting := e.aecTickCount < e.aecWaitTime
	if e.aecWaiting != nextWaiting {
		e.aecWaiting = nextWaiting
	}
	// (3) still waiting.
	if nextWaiting {
		return
	}

	// (4) radiusPerTick shrink.
	radius := e.aecRadius
	if e.aecRadiusPerTick != 0 {
		radius += e.aecRadiusPerTick
		if radius < aecMinRadius {
			t.cur().entities.remove(e.id)
			return
		}
		e.aecRadius = clampF32(radius, 0, aecMaxRadius)
		radius = e.aecRadius
	}

	// (5) every-5-tick application pass.
	if e.aecTickCount%aecApplyPeriod != 0 {
		return
	}

	// Expire the per-victim reapplication cooldowns (removeIf tickCount >= value).
	for id, expireAt := range e.aecVictims {
		if e.aecTickCount >= expireAt {
			delete(e.aecVictims, id)
		}
	}

	// No effects -> clear victims and stop.
	if len(e.aecEffects) == 0 {
		if len(e.aecVictims) != 0 {
			e.aecVictims = make(map[int32]int)
		}
		return
	}

	// getEntitiesOfClass(LivingEntity, box): players then mobs, in a deterministic id order (the v1 stand-in
	// for the list order), each filtered to distSqr(x,z) <= radius^2 by aecInBox.
	radiusSqr := float64(radius) * float64(radius)

	players := make([]*tickPlayer, 0, len(t.players))
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		players = append(players, p)
	}
	sort.Slice(players, func(i, j int) bool { return players[i].entityID < players[j].entityID })
	for _, p := range players {
		t.aecApplyToPlayer(e, p, radius, radiusSqr)
		if !t.aecStillAlive(e) {
			return
		}
	}

	mobs := make([]*Entity, 0, t.cur().entities.len())
	for _, m := range t.cur().entities.near(e.x, e.z, 1) {
		if m == nil || m == e || m.dead || !m.isAlive() || !isLivingMob(m) {
			continue
		}
		mobs = append(mobs, m)
	}
	sort.Slice(mobs, func(i, j int) bool { return mobs[i].id < mobs[j].id })
	for _, m := range mobs {
		t.aecApplyToMob(e, m, radius, radiusSqr)
		if !t.aecStillAlive(e) {
			return
		}
	}
}

// aecStillAlive reports whether the AEC is still in the store (a radiusOnUse shrink can discard it mid-pass).
func (t *TickLoop) aecStillAlive(e *Entity) bool {
	for _, x := range t.cur().entities.all() {
		if x == e {
			return true
		}
	}
	return false
}

// aecInBox reports whether a LivingEntity at (ex,ey,ez) is inside the AEC vertical slab AND within radius on
// the horizontal plane. Vanilla getEntitiesOfClass uses the box (2*radius wide, 0.5 tall); the inner filter
// is distanceToSqr in the X/Z plane <= radius^2 (verified javap: only x/z deltas are squared).
func aecInBox(e *Entity, ex, ey, ez float64, radiusSqr float64) bool {
	loY := e.y
	hiY := e.y + float64(e.height)
	entLoY := ey
	entHiY := ey + 1.8 // a standing LivingEntity spans ~its height; the slab overlap catches one in the cloud
	if entHiY <= loY || hiY <= entLoY {
		return false
	}
	dx := ex - e.x
	dz := ez - e.z
	return dx*dx+dz*dz <= radiusSqr
}

// aecApplyToPlayer applies the cloud effects to a PLAYER victim (skip if a current victim / out of range),
// records the reapplication cooldown, then runs radiusOnUse/durationOnUse.
func (t *TickLoop) aecApplyToPlayer(e *Entity, p *tickPlayer, radius float32, radiusSqr float64) {
	if _, seen := e.aecVictims[p.entityID]; seen {
		return
	}
	if !aecInBox(e, p.x, p.y, p.z, radiusSqr) {
		return
	}
	e.aecVictims[p.entityID] = e.aecTickCount + e.aecReapplyDelay
	for _, ef := range e.aecEffects {
		if isInstantEffect(ef.id) {
			t.addPlayerEffect(p, e.aecOwnerID, ef.id, ef.duration, ef.amplifier, aecInstantScale)
			continue
		}
		t.addPlayerEffect(p, e.aecOwnerID, ef.id, ef.duration, ef.amplifier, 1.0)
	}
	t.aecOnUse(e, radius)
}

// aecApplyToMob applies the cloud effects to a MOB victim through the mob effect path.
func (t *TickLoop) aecApplyToMob(e *Entity, m *Entity, radius float32, radiusSqr float64) {
	if _, seen := e.aecVictims[m.id]; seen {
		return
	}
	if !aecInBox(e, m.x, m.y, m.z, radiusSqr) {
		return
	}
	e.aecVictims[m.id] = e.aecTickCount + e.aecReapplyDelay
	for _, ef := range e.aecEffects {
		if isInstantEntityEffect(ef.id) {
			t.addEntityEffectWithSource(m, e.aecOwnerID, ef.id, ef.duration, ef.amplifier, aecInstantScale)
			continue
		}
		t.addEntityEffectWithSource(m, e.aecOwnerID, ef.id, ef.duration, ef.amplifier, 1.0)
	}
	t.aecOnUse(e, radius)
}

// aecOnUse ports the radiusOnUse + durationOnUse block that runs after a victim is affected.
func (t *TickLoop) aecOnUse(e *Entity, radius float32) {
	if e.aecRadiusOnUse != 0 {
		radius += e.aecRadiusOnUse
		if radius < aecMinRadius {
			t.cur().entities.remove(e.id)
			return
		}
		e.aecRadius = clampF32(radius, 0, aecMaxRadius)
	}
	if e.aecDurationOnUse != 0 && e.aecDuration != aecInfiniteDuration {
		e.aecDuration += e.aecDurationOnUse
		if e.aecDuration <= 0 {
			t.cur().entities.remove(e.id)
			return
		}
	}
}

// lingering AEC shape (ThrownLingeringPotion.onHitAsPotion, verified javap -- the exact setters).
const (
	lingerAECRadius        = 3.0  // setRadius(3.0f)
	lingerAECRadiusOnUse   = -0.5 // setRadiusOnUse(-0.5f)
	lingerAECDuration      = 600  // setDuration(600)
	lingerAECWaitTime      = 10   // setWaitTime(10)
	lingerAECReapplyDelay  = 20   // AreaEffectCloud default reapplicationDelay (ctor bipush 20)
	lingerAECDurationOnUse = 0    // AreaEffectCloud default durationOnUse (ctor iconst_0)
)

// spawnLingeringCloud is the ThrownLingeringPotion.onHitAsPotion port: spawn an AreaEffectCloud at the
// (already-moved-to-impact) potion position carrying the potion effects, with the lingering shape. The
// radiusPerTick is setRadiusPerTick(-getRadius() / getDuration()) == -3.0/600 == -0.005f (the documented
// per-tick shrink). The owner carries through (setOwner if the thrower is a LivingEntity). Cite
// ThrownLingeringPotion.onHitAsPotion (offsets 0-113).
func (t *TickLoop) spawnLingeringCloud(e *Entity) {
	radiusPerTick := float32(-float32(lingerAECRadius) / float32(lingerAECDuration)) // -getRadius()/getDuration()
	t.spawnAreaEffectCloud(
		e.arrowShooterID, // the potion owner (arrowShooterID reuses the projectile owner field)
		e.x, e.y, e.z,
		e.potionEffects,
		lingerAECRadius,
		lingerAECDuration,
		lingerAECWaitTime,
		lingerAECReapplyDelay,
		lingerAECDurationOnUse,
		lingerAECRadiusOnUse,
		radiusPerTick,
	)
	t.cur().entities.remove(e.id) // discard() the thrown lingering potion
}
