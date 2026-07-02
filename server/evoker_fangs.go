package server

// evoker_fangs.go -- the EvokerFangs entity (net.minecraft.world.entity.projectile.EvokerFangs), a 1:1
// port from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). EvokerFangs is
// a NON-mob projectile the Evoker's FANGS spell (Evoker$EvokerAttackSpellGoal.createSpellEntity) spawns at
// a sturdy floor. It is a code-spawned entity (the sibling of the arrow / item / xp-orb ticks in
// projectile.go / item_entity.go / xp_orb.go), NOT a plugin-declared mob: its whole behavior is this
// warmup -> attack -> despawn tick.
//
// VANILLA (verified CFR EvokerFangs):
//
//	ctor: warmupDelayTicks = <arg>; lifeTicks = 22; setOwner(evoker); setYRot(rot*57.295776); setPos(x,y,z).
//	static: ATTACK_DURATION=20, LIFE_OFFSET=2, ATTACK_TRIGGER_TICKS=14, DEFAULT_WARMUP_DELAY=0.
//	tick() (server branch): if (--warmupDelayTicks < 0) {
//	    if (warmupDelayTicks == -8) { for each LivingEntity in getBoundingBox().inflate(0.2,0,0.2): dealDamageTo }
//	    if (!sentSpikeEvent) { broadcastEntityEvent(this, 4); sentSpikeEvent = true; }
//	    if (--lifeTicks < 0) discard();
//	}
//	dealDamageTo(target): owner = getOwner(); if (!target.isAlive() || target.isInvulnerable() || target==owner) return;
//	    if (owner == null) target.hurt(magic(), 6.0);
//	    else { if (owner.isAlliedTo(target)) return; src = indirectMagic(this, owner); target.hurtServer(src, 6.0); }
//
// The client-only particle burst (lifeTicks==14 -> 12 CRIT particles) and the getAnimationProgress lerp are
// pure client render (isClientSide branch) -- NOT ported (server-authoritative). The spike EVENT (byte 4)
// IS emitted (broadcastToTrackers) so the client plays the bite animation + attack sound. NO RandomSource
// draws on the server path (the particle draws are client-side), so the fangs tick never perturbs any
// entity's lockstep stream.

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// EvokerFangs constants (VERIFIED CFR -- the compile-time-inlined static finals).
const (
	fangsDefaultLifeTicks = 22  // EvokerFangs.lifeTicks initial value (ctor: lifeTicks = 22)
	fangsAttackWarmup     = -8  // the warmupDelayTicks value at which dealDamageTo fires (tick: == -8)
	fangsDamage           = 6.0 // dealDamageTo: hurt(..., 6.0F)
	fangsInflateXZ        = 0.2 // getBoundingBox().inflate(0.2, 0.0, 0.2) -- the attack box XZ inflation
	fangsSpikeEvent       = 4   // broadcastEntityEvent(this, (byte)4) -- the client bite-animation event
	// fangsDegPerRad is EvokerFangs' setYRot(rotationRadians * 57.295776f) factor (radians -> degrees).
	fangsDegPerRad = 57.295776
)

// spawnEvokerFangs creates an EvokerFangs at (x,y,z) with the given warmupDelayTicks + owner + yaw (radians)
// and adds it to the owner region's store (the tracker broadcasts its AddEntity next tick, exactly as the
// arrow / item drop rides the store-add path). Returns the fangs entity. Cite EvokerFangs.<init>(Level, x,
// y, z, rotationRadians, warmupDelayTicks, owner).
func (t *TickLoop) spawnEvokerFangs(ownerID int32, x, y, z float64, yawRadians float64, warmupDelayTicks int) *Entity {
	f := NewEntity(t.idAlloc.AllocID(), entity.EvokerFangs, x, y, z)
	f.isFangs = true
	f.fangsOwnerID = ownerID
	f.fangsWarmupDelayTicks = int32(warmupDelayTicks)
	f.fangsLifeTicks = fangsDefaultLifeTicks
	// setYRot(rotationRadians * 57.295776f): the fangs face the cast direction (the client renders the bite
	// aligned to yaw). d2f narrowing at the vanilla cast site (setYRot takes a float).
	f.yaw = float32(yawRadians * fangsDegPerRad)
	f.headYaw = f.yaw

	owner := t.regionForEntity(f)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(f)
	return f
}

// tickFangs drives every EvokerFangs in every region, the sibling of tickArrows/tickPotions. It runs on the
// coordinator (quiescent) and processes each region WITH that region registered (withRegion) so the fangs
// tick's t.cur() (the despawn remove) resolves to the fangs' OWN store. A per-region snapshot keeps the loop
// stable across an in-loop discard (the despawn removal).
func (t *TickLoop) tickFangs() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isFangs {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickFang(e)
			}
		})
	}
}

// tickFang ports EvokerFangs.tick's server branch for one fangs: decrement warmupDelayTicks; once it drops
// below 0, fire the attack at exactly -8 (dealDamageTo every LivingEntity in the inflated box), broadcast
// the spike event ONCE, then count lifeTicks down and discard at <0. NO RandomSource draws (the particle
// burst is client-only). Cite EvokerFangs.tick.
func (t *TickLoop) tickFang(e *Entity) {
	// --warmupDelayTicks; the whole attack/despawn body runs only once it goes below 0 (the warmup).
	e.fangsWarmupDelayTicks--
	if e.fangsWarmupDelayTicks >= 0 {
		return // still warming up (the ground-crack telegraph before the bite)
	}

	// At exactly -8 (ATTACK_TRIGGER_TICKS 14 relative to the 22-tick life -- the bite frame), deal 6.0 magic
	// to every LivingEntity in the box inflated (0.2, 0.0, 0.2).
	if e.fangsWarmupDelayTicks == fangsAttackWarmup {
		t.fangsDealDamageInBox(e)
	}

	// broadcastEntityEvent(this, 4) ONCE (the first tick warmup < 0): the client plays the bite animation +
	// the EVOKER_FANGS_ATTACK sound (its handleEntityEvent(4)).
	if !e.fangsSentSpikeEvent {
		t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, fangsSpikeEvent))
		e.fangsSentSpikeEvent = true
	}

	// --lifeTicks; discard at < 0 (the ~22-tick lifetime). The tracker emits RemoveEntities next tick.
	e.fangsLifeTicks--
	if e.fangsLifeTicks < 0 {
		t.cur().entities.remove(e.id)
	}
}

// fangsDealDamageInBox is EvokerFangs.tick's getEntitiesOfClass(LivingEntity.class, box.inflate(0.2,0,0.2))
// loop: hurt every LivingEntity intersecting the fangs' inflated box via dealDamageTo. v1 LivingEntities are
// the players (t.players) AND the mob-store entities -- both are hit (vanilla hits all LivingEntities in the
// box, not just players). The fangs' own AABB is feet-anchored (width 0.5, height 0.8); it is inflated by
// 0.2 on X/Z and 0.0 on Y, exactly the vanilla inflate. Cite EvokerFangs.tick + dealDamageTo.
func (t *TickLoop) fangsDealDamageInBox(e *Entity) {
	// The fangs' attack AABB: its feet-anchored box (width x height) inflated (0.2, 0.0, 0.2).
	hw := e.width/2 + fangsInflateXZ
	loX, hiX := e.x-hw, e.x+hw
	loY, hiY := e.y, e.y+e.height // Y inflation is 0.0 -- the box keeps its exact vertical extent
	loZ, hiZ := e.z-hw, e.z+hw

	// Player LivingEntities in the box.
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if boxIntersectsPlayer(p, loX, loY, loZ, hiX, hiY, hiZ) {
			t.fangsDealDamageToPlayer(e, p)
		}
	}

	// Mob LivingEntities in the box (the store entities that are alive mobs -- skip the fangs itself, items,
	// orbs, arrows, potions, and other non-living projectiles). near() is the broad phase; the AABB test is
	// the narrow phase. The fangs lives in this region's store, so scan the columns around it.
	for _, m := range t.cur().entities.near(e.x, e.z, 1) {
		if m == nil || m == e || !isLivingMob(m) {
			continue
		}
		mhw := m.width / 2
		if hiX <= m.x-mhw || m.x+mhw <= loX ||
			hiY <= m.y || m.y+m.height <= loY ||
			hiZ <= m.z-mhw || m.z+mhw <= loZ {
			continue // boxes do not overlap
		}
		t.fangsDealDamageToMob(e, m)
	}
}

// fangsDealDamageToPlayer ports EvokerFangs.dealDamageTo for a PLAYER victim. The owner-guard (target ==
// owner, allied) uses the fangs' ownerID: the evoker never bites itself, and a player is not the evoker's
// ally in v1 (no team subsystem -> isAlliedTo is false for a player). With a live owner the source is
// indirect_magic attributed to the owner (bypasses_armor); with no owner it is plain magic. Cite
// EvokerFangs.dealDamageTo.
func (t *TickLoop) fangsDealDamageToPlayer(e *Entity, p *tickPlayer) {
	// !target.isAlive() -> skip (a dead player is already filtered by the caller's p.dead guard).
	// target.isInvulnerable(): a creative/spectator player is invulnerable -> the player hurt path's own
	// invulnerability guard handles it (applyDamage), so we route through it uniformly.
	// target == owner: a player is never the evoker owner (owner is a mob id), so this never trips.
	var src damageSource
	if e.fangsOwnerID == 0 {
		src = damageSourceMagic() // owner == null -> damageSources().magic()
	} else {
		src = damageSourceIndirectMagic(e.fangsOwnerID) // indirectMagic(this, owner) -- attributed to owner
	}
	t.applyDamage(p, src, fangsDamage) // the PLAYER hurt path (victim is a player)
}

// fangsDealDamageToMob ports EvokerFangs.dealDamageTo for a MOB victim (a store LivingEntity). The
// owner-guard skips the owner itself (a fangs never bites its caster) and an already-dead/invulnerable mob.
// v1 has no team subsystem, so isAlliedTo is false for a mob too -- the fangs damages any live mob in range
// (matching vanilla for non-teamed mobs). The source is indirect_magic (owner) or plain magic (no owner),
// applied via applyDamageEntity (the mob hurt path). Cite EvokerFangs.dealDamageTo.
func (t *TickLoop) fangsDealDamageToMob(e *Entity, m *Entity) {
	if !m.isAlive() || m.dead {
		return // !target.isAlive() guard
	}
	if m.id == e.fangsOwnerID {
		return // target == owner -> the fangs never bites its caster
	}
	var src damageSource
	if e.fangsOwnerID == 0 {
		src = damageSourceMagic()
	} else {
		src = damageSourceIndirectMagic(e.fangsOwnerID)
	}
	t.applyDamageEntity(m, src, fangsDamage) // the MOB hurt path (victim is a store mob)
}

// boxIntersectsPlayer reports whether the given AABB (feet-anchored world box) intersects the player's
// collision box (playerWidth x playerHeight, feet at p.y). Half-open on each axis (a shared-face touch does
// not count) -- the same narrow-phase convention scanItemPickup uses.
func boxIntersectsPlayer(p *tickPlayer, loX, loY, loZ, hiX, hiY, hiZ float64) bool {
	phw := playerWidth / 2
	pLoX, pHiX := p.x-phw, p.x+phw
	pLoY, pHiY := p.y, p.y+playerHeight
	pLoZ, pHiZ := p.z-phw, p.z+phw
	return !(hiX <= pLoX || pHiX <= loX ||
		hiY <= pLoY || pHiY <= loY ||
		hiZ <= pLoZ || pHiZ <= loZ)
}

// isLivingMob reports whether a store entity is a LIVING mob (a hurtable LivingEntity) -- excluding the
// non-living projectiles/props (items, xp orbs, arrows, potions, fangs). This is the v1 stand-in for
// instanceof LivingEntity -- items/orbs/arrows/potions/fangs are Entity, not LivingEntity, so they are
// never fangs victims. A vex (isVex) IS a living mob. Cite EvokerFangs.tick's getEntitiesOfClass(
// LivingEntity.class, ...).
func isLivingMob(e *Entity) bool {
	if e == nil {
		return false
	}
	if e.isItem || e.isOrb || e.isArrow || e.isPotion || e.isFangs {
		return false // non-living Entity subclasses -- not a LivingEntity
	}
	return true
}
