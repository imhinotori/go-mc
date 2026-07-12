package server

// throwable.go — THROWABLE ITEM PROJECTILES: a 1:1 port of the ThrowableProjectile flight + onHit for
// snowball, egg, and ender_pearl (net.minecraft.world.entity.projectile.throwableitemprojectile.*),
// decompiled from temp/cache/26.2-inner.jar (CFR/javap this session). A throwable is a NON-mob moving
// Entity (isThrowable) with no AI: its whole behavior is the physics + hit tick, the sibling of
// tickArrows / tickPotions.
//
// Vanilla per-tick order (ThrowableProjectile.tick, verified javap):
//   1. applyGravity()   : deltaMovement.y -= getDefaultGravity() (0.03 for a throwable)
//   2. applyInertia()   : deltaMovement *= getAirDrag() (0.99 in air, 0.8 in water)
//   3. getHitResultOnMoveVector : clip the move segment vs blocks+entities; setPos to hit-or-next
//   4. onHit(hitResult) : per-kind effect, then discard
// NOTE the vanilla order is gravity+drag BEFORE the move (unlike the arrow's move→drag→gravity). Preserved.
//
// onHit per kind (verified CFR):
//   - Snowball.onHitEntity: entity.hurt(thrown, entity instanceof Blaze ? 3 : 0); then discard.
//   - ThrownEgg.onHitEntity: entity.hurt(thrown, 0); (1/8 spawn chicken — CITE-DEFERRED, no chicken spawn
//     from a projectile yet); then discard.
//   - ThrownEnderpearl.onHit: teleport the OWNER to oldPosition() (the pre-move position), resetFallDistance,
//     then hurtServer(enderPearl, 5.0). (endermite 5% roll + portal cooldown are CITE-DEFERRED.)

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
)

// throwable kinds.
const (
	throwSnowball = iota
	throwEgg
	throwEnderPearl
	throwExperienceBottle
)

// ThrowableProjectile physics constants (verified javap — exact float values).
const (
	throwGravity      = 0.03 // ThrowableProjectile.getDefaultGravity()
	throwAirDrag      = 0.99 // ThrowableProjectile.getAirDrag()
	throwWaterDrag    = 0.8  // in-water inertia (isInWater branch)
	throwDespawnTicks = 1200 // a throwable that never lands still despawns (lifetime backstop)
	// throwLaunchPower is Projectile.shootFromRotation velocity for a hand-thrown item: the vanilla
	// Player use of a snowball/egg/ender_pearl calls shoot(x,y,z, 1.5f, 1.0f) — velocity magnitude 1.5,
	// inaccuracy 1.0. v1 uses 0 inaccuracy (no per-throw spread) so the throw is deterministic.
	throwLaunchPower = 1.5
	// xp bottle: ExperienceBottleItem.use spawns at angle -20.0 with velocity 0.7 (ldc -20.0f, ldc 0.7f),
	// unlike the 0/1.5 the other throwables use. Cite ExperienceBottleItem.use spawnProjectileFromRotation.
	xpBottleThrowAngle float32 = -20.0
	xpBottleThrowPower         = 0.7
	// enderPearlFallDamage is ThrownEnderpearl.onHit: hurtServer(enderPearl(), 5.0) after teleport.
	enderPearlFallDamage = 5.0
	// xpBottleGravity is ThrownExperienceBottle.getDefaultGravity() == 0.07d -- the xp bottle overrides the
	// ThrowableProjectile default 0.03 with a HEAVIER 0.07, so it arcs down faster than a snowball.
	//   [VERIFIED javap ThrownExperienceBottle.getDefaultGravity: ldc2_w 0.07d; dreturn.]
	xpBottleGravity = 0.07
)

// throwLaunchParams returns the (angleOffset, velocity) each throwable item's *.use passes to
// spawnProjectileFromRotation. snowball/egg/ender_pearl: angle 0, velocity 1.5 (SnowballItem/EggItem/
// EnderpearlItem.use: fconst_0, ldc 1.5f). xp bottle: angle -20, velocity 0.7 (ExperienceBottleItem.use:
// ldc -20.0f, ldc 0.7f). Cite the per-item .use spawnProjectileFromRotation args.
func throwLaunchParams(kind int) (angle float32, velocity float64) {
	if kind == throwExperienceBottle {
		return xpBottleThrowAngle, xpBottleThrowPower
	}
	return 0, throwLaunchPower
}

// itemToThrowableKind maps a held item id to its throwable kind, ok=false for a non-throwable item.
// CITE: the Item -> projectile binding (SnowballItem/EggItem/EnderpearlItem.use each spawn their kind).
func itemToThrowableKind(itemID int32) (int, bool) {
	switch item.ID(itemID) {
	case item.Snowball.ID:
		return throwSnowball, true
	case item.Egg.ID:
		return throwEgg, true
	case item.EnderPearl.ID:
		return throwEnderPearl, true
	case item.ExperienceBottle.ID:
		return throwExperienceBottle, true
	}
	return 0, false
}

// throwableEntityType returns the wire entity type for a throwable kind.
func throwableEntityType(kind int) entity.Entity {
	switch kind {
	case throwEgg:
		return entity.Egg
	case throwEnderPearl:
		return entity.EnderPearl
	case throwExperienceBottle:
		return entity.ExperienceBottle
	default:
		return entity.Snowball
	}
}

// spawnThrowable creates a throwable projectile at (x,y,z) launched with (vx,vy,vz), owned by ownerID,
// and adds it to the owner region's store (the tracker broadcasts AddEntity next tick, exactly as an
// arrow / potion). yaw/pitch are seeded from the launch vector (Projectile.shoot). Returns the entity.
func (t *TickLoop) spawnThrowable(ownerID int32, kind int, x, y, z, vx, vy, vz float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), throwableEntityType(kind), x, y, z)
	e.isThrowable = true
	e.throwableKind = kind
	e.throwOwnerID = ownerID
	e.throwOldX, e.throwOldY, e.throwOldZ = x, y, z
	e.vx, e.vy, e.vz = vx, vy, vz
	e.spawnData = ownerID + 1 // ClientboundAddEntity data: ownerId+1 (0 == none)

	horiz := math.Sqrt(vx*vx + vz*vz)
	e.yaw = float32(math.Atan2(vx, vz) * 180.0 / math.Pi)
	e.pitch = float32(math.Atan2(vy, horiz) * 180.0 / math.Pi)
	e.headYaw = e.yaw

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// throwEyeHeight is the player standing eye height (getEyeY basis) the throw launches from, matching the
// fishing-rod cast origin. CITE: Player standingEyeHeight ~1.62.
const throwEyeHeight = 1.62

// tryThrowItem is the SnowballItem/EggItem/EnderpearlItem.use port: a right-click-air with a throwable
// item spawns its ThrowableProjectile from the player eye toward the look direction (shootFromRotation
// velocity 1.5, inaccuracy 1.0; the xp bottle uses velocity 0.7/angle -20) and consumes 1 (creative-exempt).
// Returns true if the item was a throwable (so
// useItemInHand stops), false to fall through to the food path. Tick-owned; the spawn rides the store-add
// path (tracker broadcasts AddEntity). CITE: Projectile.shootFromRotation + ItemStack.consume(1).
func (t *TickLoop) tryThrowItem(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	kind, ok := itemToThrowableKind(int32(held.ItemID))
	if !ok {
		return false
	}
	// spawnProjectileFromRotation(factory, level, stack, player, angle, velocity, 1.0f) ->
	// shootFromRotation: the launch vector from the look angles + the 3 per-axis inaccuracy triangle draws
	// (inaccuracy 1.0) on the projectile's OWN throwRNG + the owner known-movement inherit. The xp bottle
	// alone uses angle -20 / velocity 0.7 (ExperienceBottleItem.use); snowball/egg/ender_pearl use angle 0 /
	// velocity 1.5. Spawn at rest at the eye, seed the stream, then apply the shot vector -- a pig throws
	// nothing so its stream is never perturbed. Cite SnowballItem/EggItem/EnderpearlItem/ExperienceBottleItem
	// .use (spawnProjectileFromRotation) + Projectile.shootFromRotation.
	angle, velocity := throwLaunchParams(kind)
	e := t.spawnThrowable(p.entityID, kind, p.x, p.y+throwEyeHeight, p.z, 0, 0, 0)
	if e.throwRNG == nil {
		e.throwRNG = newEntityRandom(uint64(e.id))
	}
	mx, my, mz := t.playerKnownMovement(p)
	vx, vy, vz := shootVectorFromRotation(e.throwRNG, p.yaw, p.pitch, angle, velocity, 1.0, mx, my, mz, p.onGround)
	e.vx, e.vy, e.vz = vx, vy, vz
	e.throwOldX, e.throwOldY, e.throwOldZ = e.x, e.y, e.z
	horiz := math.Sqrt(vx*vx + vz*vz)
	e.yaw = float32(math.Atan2(vx, vz) * 180.0 / math.Pi)
	e.pitch = float32(math.Atan2(vy, horiz) * 180.0 / math.Pi)
	e.headYaw = e.yaw

	// ItemStack.consume(1): shrink by 1 unless creative.
	if p.gameMode != gameModeCreative {
		held.Count--
		if held.Count <= 0 {
			held = component.SlotData{Count: 0}
		}
		inv.set(heldMenuSlot(p, hand), held)
		t.syncHeldAfterThrow(p, hand)
	}
	return true
}

// syncHeldAfterThrow pushes the post-consume held slot to the client (a SetSlot), so the thrown item's
// count decrements on the HUD immediately. Mirrors the eat path's syncAfterEat slot resync.
func (t *TickLoop) syncHeldAfterThrow(p *tickPlayer, hand int32) {
	if p.client == nil {
		return
	}
	slot := heldMenuSlot(p, hand)
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetSlot(playerContainerID, inv.stateID, slot, inv.get(slot)))
}

// tickThrowables drives every throwable in every region — the sibling of tickArrows/tickPotions. Runs on
// the coordinator (quiescent) with each region registered so the tick's t.cur() (move re-bucket / discard)
// resolves to the projectile's OWN store. A per-region snapshot keeps the loop stable across an in-loop
// discard (a hit/despawn removal).
func (t *TickLoop) tickThrowables() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isThrowable {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickThrowable(e)
			}
		})
	}
}

// tickThrowable is the port of ThrowableProjectile.tick for one throwable: gravity, drag, then the swept
// block+entity hit test; on a hit it runs the per-kind onHit and discards; else it moves and ages.
func (t *TickLoop) tickThrowable(e *Entity) {
	// (1) applyGravity: deltaMovement.y -= getDefaultGravity(). BEFORE the move (throwable order). The xp
	// bottle overrides the default 0.03 with 0.07 (ThrownExperienceBottle.getDefaultGravity).
	gravity := throwGravity
	if e.throwableKind == throwExperienceBottle {
		gravity = xpBottleGravity
	}
	e.vy -= gravity

	// (2) applyInertia: deltaMovement *= getAirDrag() (0.99), or 0.8 in water.
	drag := throwAirDrag
	if t.isWaterAt(int(math.Floor(e.x)), int(math.Floor(e.y)), int(math.Floor(e.z))) {
		drag = throwWaterDrag
	}
	e.vx *= drag
	e.vy *= drag
	e.vz *= drag

	// (3) getHitResultOnMoveVector: clip the move segment vs blocks FIRST, then test entities up to that
	// clipped endpoint (an entity behind a wall the throwable hits first is not hit).
	ox, oy, oz := e.x, e.y, e.z
	nx, ny, nz := ox+e.vx, oy+e.vy, oz+e.vz
	hx, hy, hz, blockHit := t.arrowClipSegment(ox, oy, oz, nx, ny, nz)
	endX, endY, endZ := nx, ny, nz
	if blockHit {
		endX, endY, endZ = hx, hy, hz
	}

	// Record oldPosition() BEFORE moving — the ender_pearl teleports the owner to the pre-move position.
	e.throwOldX, e.throwOldY, e.throwOldZ = ox, oy, oz

	// SNOW-GOLEM snowball MOB-victim scan (additive, snowballHitsMobs-gated): a snow golem's snowball
	// resolves against the FIRST Enemy mob its flight segment crosses (Snowball.onHitEntity applies to any
	// LivingEntity, not just players). Gated on snowballHitsMobs so a player-thrown snowball/egg/ender-pearl
	// keeps the byte-identical player-only path below. Checked BEFORE the player scan; the closer of the two
	// still wins because a mob hit returns immediately and the player scan is a separate broad-phase (a snow
	// golem never targets a player, so in practice only one branch ever fires). Cite Snowball.onHitEntity.
	if e.snowballHitsMobs {
		if victim := t.snowballFindHitMobVictim(e, ox, oy, oz, endX, endY, endZ); victim != nil {
			t.snowballOnHitMob(e, victim)
			t.cur().entities.remove(e.id) // discard on hit
			return
		}
	}
	// Entity hit (ProjectileUtil.getEntityHitResult): the SINGLE nearest EntityHitResult over BOTH players
	// and mobs. A snowball/egg deals 0 (blaze 3) + the thrown knockback, an ender pearl teleports the owner,
	// an xp bottle breaks -- Snowball/ThrownEgg/ThrownEnderpearl.onHitEntity apply to ANY LivingEntity, not
	// just players. Scan both, dispatch to whichever is nearer along the segment (smaller tHit). The
	// snowballHitsMobs snow-golem fast path above already returned for a golem snowball.
	half := throwableEntityType(e.throwableKind).Width / 2.0
	pv, pt := t.projectileFindHitPlayerT(e.throwOwnerID, nil, ox, oy, oz, endX, endY, endZ)
	mv, mt := t.projectileFindHitMobT(e.throwOwnerID, nil, half, ox, oy, oz, endX, endY, endZ)
	if pv != nil && (mv == nil || pt <= mt) {
		t.throwableOnHitEntity(e, pv)
		t.cur().entities.remove(e.id) // discard on hit
		return
	} else if mv != nil {
		t.throwableOnHitMob(e, mv)
		t.cur().entities.remove(e.id) // discard on hit
		return
	}
	if blockHit {
		// Move to the impact point so a client that predicted the flight sees it land there, then onHit.
		t.cur().entities.move(e, endX, endY, endZ)
		t.throwableOnHitBlock(e)
		t.cur().entities.remove(e.id) // discard on hit
		return
	}

	// (4) No hit: move to the next position + age. updateRotation from the movement.
	t.cur().entities.move(e, nx, ny, nz)
	horiz := math.Sqrt(e.vx*e.vx + e.vz*e.vz)
	if e.vx != 0 || e.vz != 0 {
		e.yaw = float32(math.Atan2(e.vx, e.vz) * 180.0 / math.Pi)
		e.headYaw = e.yaw
	}
	e.pitch = float32(math.Atan2(e.vy, horiz) * 180.0 / math.Pi)

	e.throwLife++
	if e.throwLife >= throwDespawnTicks {
		t.cur().entities.remove(e.id)
	}
}

// throwableOnHitEntity ports the per-kind onHitEntity for a player victim. Snowball: 3 dmg to a blaze
// (none exist yet) else 0 — i.e. a snowball/egg deals 0 to a player, matching vanilla (the knockback-only
// hit). Ender pearl: no entity damage (it teleports on ANY hit — handled in onHit, so the entity branch
// just triggers the discard + teleport via throwableOnHitBlock-equivalent). v1 tests players only.
func (t *TickLoop) throwableOnHitEntity(e *Entity, victim *tickPlayer) {
	switch e.throwableKind {
	case throwSnowball, throwEgg:
		// Snowball/ThrownEgg.onHitEntity: entity.hurt(damageSources().thrown(this, getOwner()), i) where
		// i == (victim instanceof Blaze ? 3 : 0). A player is never a Blaze, so i == 0 -- but the hurt call
		// STILL happens: a 0-damage thrown hit flashes the victim + applies the 0.4 dealDefaultKnockback
		// (NO_KNOCKBACK does not include `thrown`), so a snowball/egg visibly knocks a player back. The
		// direct entity is the snowball, so getSourcePosition() is the snowball position -- the player is
		// pushed away from the impact point. Mirrors the (correct) mob branch. Cite Snowball/ThrownEgg
		// .onHitEntity + ThrowableItemProjectile.onHitEntity (super) knockback.
		src := damageSourceThrown(e.throwOwnerID)
		src.sourceX, src.sourceZ, src.hasSourcePos = e.x, e.z, true
		t.applyDamage(victim, src, 0)
	case throwEnderPearl:
		// ThrownEnderpearl.onHitEntity: entity.hurt(thrown, 0) -- a 0-damage thrown hit that flashes the
		// victim + applies the 0.4 knockback (away from the pearl's impact point) -- THEN the pearl teleports
		// its owner (onHit, regardless of block/entity). Cite ThrownEnderpearl.onHitEntity + onHit.
		src := damageSourceThrown(e.throwOwnerID)
		src.sourceX, src.sourceZ, src.hasSourcePos = e.x, e.z, true
		t.applyDamage(victim, src, 0)
		t.enderPearlTeleport(e)
	case throwExperienceBottle:
		// The xp bottle breaks on ANY hit (block or entity), splitting into XP orbs at the impact point.
		t.experienceBottleBreak(e)
	}
}

// throwableOnHitMob ports the per-kind onHitEntity for a MOB (LivingEntity) victim -- the *Entity sibling of
// throwableOnHitEntity. Snowball: 3 to a blaze, 0 otherwise, plus the thrown 0.4 knockback (delegated to
// snowballOnHitMob, which sets the directEntity source-position so the mob is pushed away from the snowball).
// Egg: hurt(thrown, 0) -- 0 damage + the thrown knockback (a 0-damage thrown hit still recoils the mob, since
// dealDefaultKnockback is gated on !NO_KNOCKBACK, not the amount). Ender pearl: teleport the OWNER (0 entity
// damage). XP bottle: break into orbs. All applied to any LivingEntity, matching Snowball/ThrownEgg/
// ThrownEnderpearl.onHitEntity. Cite the per-kind onHitEntity.
func (t *TickLoop) throwableOnHitMob(e *Entity, victim *Entity) {
	switch e.throwableKind {
	case throwSnowball:
		// Snowball.onHitEntity: 3 to a blaze else 0 + the thrown knockback. snowballOnHitMob is the exact port.
		t.snowballOnHitMob(e, victim)
	case throwEgg:
		// ThrownEgg.onHitEntity: hurt(thrown, 0) -- 0 damage, but the thrown source still applies the 0.4
		// dealDefaultKnockback on a fresh hit (NO_KNOCKBACK does not include `thrown`). The source carries the
		// egg's position so the mob is pushed radially away from the egg. (The 1/8 chicken spawn is CITE-DEFERRED,
		// as in throwableOnHitEntity.)
		src := damageSourceThrown(e.throwOwnerID)
		src.sourceX, src.sourceZ, src.hasSourcePos = e.x, e.z, true
		t.applyDamageEntity(victim, src, 0)
	case throwEnderPearl:
		// ThrownEnderpearl.onHitEntity: hurt(thrown, 0) then the pearl teleports its OWNER regardless of what it
		// hit. The 0-damage hurt recoils the mob (thrown knockback); the teleport is the pearl's whole point.
		src := damageSourceThrown(e.throwOwnerID)
		src.sourceX, src.sourceZ, src.hasSourcePos = e.x, e.z, true
		t.applyDamageEntity(victim, src, 0)
		t.enderPearlTeleport(e)
	case throwExperienceBottle:
		// The xp bottle breaks on ANY hit (block or entity), splitting into XP orbs at the impact point.
		t.experienceBottleBreak(e)
	}
}

// throwableOnHitBlock ports the per-kind onHit for a block impact. Snowball/egg: just discard (the
// caller removes it). Ender pearl: teleport the owner to the pre-move position.
func (t *TickLoop) throwableOnHitBlock(e *Entity) {
	switch e.throwableKind {
	case throwEnderPearl:
		t.enderPearlTeleport(e)
	case throwExperienceBottle:
		t.experienceBottleBreak(e)
	}
}

// enderPearlTeleport ports ThrownEnderpearl.onHit's teleport: move the OWNER to the pearl's oldPosition()
// (the pre-move position), reset fall distance, then deal 5.0 enderPearl fall damage. The 5% endermite
// spawn + the portal-cooldown re-arm are CITE-DEFERRED (no per-projectile endermite spawn / portal state
// on a throwable yet). CITE: ThrownEnderpearl.onHit.
func (t *TickLoop) enderPearlTeleport(e *Entity) {
	owner := t.playerByEntityID(e.throwOwnerID)
	if owner == nil {
		return // owner logged off / not a player: no teleport (vanilla discards)
	}
	// teleport to oldPosition(): the pearl's position BEFORE this tick's move (vanilla uses the pre-move
	// position so the player lands where the pearl was, not inside the block it hit).
	t.teleportPlayer(owner, e.throwOldX, e.throwOldY, e.throwOldZ)
	owner.fallDistance = 0 // resetFallDistance()
	// hurtServer(enderPearl(), 5.0): the 5-damage self-hit on teleport. Routed through the shared damage
	// path so i-frames / death are handled exactly like any other player damage.
	t.applyDamage(owner, damageSourceEnderPearl(), enderPearlFallDamage)
}

// --- THROWN EXPERIENCE BOTTLE (ThrownExperienceBottle) --------------------------------------------------

// experienceBottleBreak ports ThrownExperienceBottle.onHit: on the server, emit the break level-event
// (2002, blockPos, -13083194 -- the potion-color splash particles), roll the XP payload
//
//	i = 3 + random.nextInt(5) + random.nextInt(5)
//
// and award it as ExperienceOrbs at the impact point (ExperienceOrb.awardWithDirection), then discard. The
// RNG draw ORDER is nextInt(5) then nextInt(5) (verified javap offsets 35-58) on the bottle's OWN per-entity
// stream, so the pig oracle stream is never perturbed. CITE ThrownExperienceBottle.onHit.
func (t *TickLoop) experienceBottleBreak(e *Entity) {
	if e.throwRNG == nil {
		e.throwRNG = newEntityRandom(uint64(e.id))
	}
	// levelEvent(2002, blockPosition(), -13083194): the splash-particle client event -- a cited no-op seam
	// (the ClientboundLevelEvent broadcast plumbing is not wired for throwables yet; the observable gameplay
	// is the XP award below). Kept as a named call so the wire-out slots in when the level-event path lands.
	t.xpBottleLevelEvent(e)

	// i = 3 + nextInt(5) + nextInt(5) -- the two draws in order.
	value := 3 + e.throwRNG.nextInt(5) + e.throwRNG.nextInt(5)
	// ExperienceOrb.awardWithDirection(level, hitLocation, direction, value): split value into orbs at the
	// impact point (getExperienceValue table) and spawn each. v1 spawns at the bottle position (the hit
	// location the projectile was moved to before onHit) -- the direction seeds the orb's initial motion
	// (a metadata/motion nicety), so the observable "XP appears here and can be collected" is preserved.
	t.awardExperienceOrbsAt(e.x, e.y, e.z, value)
}

// xpBottleLevelEvent is the cited faithful no-op seam for ThrownExperienceBottle.onHit's levelEvent(2002,
// blockPos, -13083194) -- the splash-particle client feedback, matching the brewLevelEvent/dispenserLevelEvent
// no-op seams (no ClientboundLevelEvent broadcast wired for throwables yet).
func (t *TickLoop) xpBottleLevelEvent(_ *Entity) {}
