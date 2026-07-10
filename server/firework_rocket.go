package server

// firework_rocket.go -- FIREWORK ROCKET: a 1:1 port of net.minecraft.world.entity.projectile
// .FireworkRocketEntity (tick / explode / dealExplosionDamage), decompiled from temp/cache/26.2-inner.jar
// (javap -c -p this session). A firework is a NON-mob Projectile (isFirework). It has two modes:
//
//   ATTACHED (the elytra boost): spawned by FireworkRocketItem.use when a player isFallFlying. Each tick,
//     while the rider is still fall-flying, it ACCELERATES the rider along its look direction:
//       look = rider.getLookAngle();
//       delta = rider.getDeltaMovement();
//       rider.setDeltaMovement(delta + (look*0.1 + (look*1.5 - delta)*0.5));
//     and re-anchors the firework to the rider (setPos + setDeltaMovement = rider delta).
//
//   FREE (a fired/dispensed rocket): if !isShotAtAngle, self-accelerate upward:
//       delta = delta*(horizontalCollision ? 1.0 : 1.15) + (0, 0.04, 0); then move(SELF, delta).
//
// Both modes then: life++; at the first tick play the launch sound; and once life > lifetime it detonates
// on the server -> explode(): broadcastEntityEvent(17) + dealExplosionDamage + discard().
//
// dealExplosionDamage (verified javap): if there are explosion stars, baseDamage = 5.0 + stars*2. It hurts
// the attached rider for 5.0 + stars*2, then every OTHER LivingEntity within distanceToSqr <= 25 (5 blocks)
// that has line of sight, for baseDamage * sqrt((5 - distanceTo)/5) -- the falloff by distance.
//
// SINGLE-OWNER (TICK-05): the firework tick runs on the tick goroutine over tick-owned state. The elytra
// boost mutates the rider's velocity (pushed to the rider client via ClientboundSetEntityMotion, the same
// seam the golem fling / ravager throw use); detonation removes the firework from the store.

import (
	"bytes"
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// FireworkRocketEntity constants (verified javap -- the exact literals).
const (
	fireworkUpwardAccel    = 0.04 // free-flight upward self-accel (0, 0.04, 0)
	fireworkHCollScale     = 1.0  // horizontalCollision inertia (kills forward speed on a wall)
	fireworkFreeScale      = 1.15 // free-flight forward inertia (no wall)
	fireworkBoostLookScale = 0.1  // elytra boost: look * 0.1
	fireworkBoostAimScale  = 1.5  // elytra boost: look * 1.5 (target speed)
	fireworkBoostLerp      = 0.5  // elytra boost: (look*1.5 - delta) * 0.5
	fireworkBaseDamage     = 5.0  // dealExplosionDamage base (5.0f) when there are stars
	fireworkStarDamage     = 2    // per-star damage bonus (stars * 2)
	fireworkDamageRadius   = 5.0  // the 5-block damage radius (distanceToSqr <= 25.0)
	fireworkDamageRadiusSq = 25.0 // 5^2
	fireworkEventExplode   = 17   // broadcastEntityEvent(this, (byte)17) on detonation
)

// spawnAttachedFirework is the FireworkRocketEntity(Level, ItemStack, LivingEntity) ctor + the
// FireworkRocketItem.use elytra-boost path: spawn a firework ATTACHED to a fall-flying rider (attachID),
// carrying starCount explosion stars and flightDuration. The lifetime is 10*(1+flightDuration) + nextInt(6)
// + nextInt(7); the initial velocity is triangle(0,0.002297), 0.05, triangle(0,0.002297) on the firework's
// own stream. Cite the base ctor (Level,double,double,double,ItemStack) offsets 78-141.
func (t *TickLoop) spawnAttachedFirework(ownerID, attachID int32, x, y, z float64, flightDuration, starCount int) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.FireworkRocket, x, y, z)
	e.isFirework = true
	e.fireworkOwnerID = ownerID
	e.fireworkAttachID = attachID
	e.fireworkStarCount = starCount
	e.fireworkShotAngle = false
	e.fireworkLife = 0
	e.fireworkRNG = newEntityRandom(uint64(e.id))
	// setDeltaMovement(triangle(0, 0.002297), 0.05, triangle(0, 0.002297)) -- the tiny launch jitter.
	e.vx = arrowTriangle(e.fireworkRNG, 0, 0.002297)
	e.vy = 0.05
	e.vz = arrowTriangle(e.fireworkRNG, 0, 0.002297)
	// lifetime = 10*(1+flightDuration) + nextInt(6) + nextInt(7).
	e.fireworkLifetime = 10*(1+flightDuration) + e.fireworkRNG.nextInt(6) + e.fireworkRNG.nextInt(7)
	e.spawnData = ownerID + 1

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// spawnFreeFirework is the base FireworkRocketEntity(Level, double, double, double, ItemStack) spawn for a
// free-flying (dispensed / crossbow) rocket -- same lifetime + init-velocity draws, but with no attached
// rider. shotAtAngle marks a crossbow-multishot angled shot (no upward self-accel). Cite the base ctor.
func (t *TickLoop) spawnFreeFirework(ownerID int32, x, y, z float64, flightDuration, starCount int, shotAtAngle bool) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.FireworkRocket, x, y, z)
	e.isFirework = true
	e.fireworkOwnerID = ownerID
	e.fireworkAttachID = 0
	e.fireworkStarCount = starCount
	e.fireworkShotAngle = shotAtAngle
	e.fireworkLife = 0
	e.fireworkRNG = newEntityRandom(uint64(e.id))
	e.vx = arrowTriangle(e.fireworkRNG, 0, 0.002297)
	e.vy = 0.05
	e.vz = arrowTriangle(e.fireworkRNG, 0, 0.002297)
	e.fireworkLifetime = 10*(1+flightDuration) + e.fireworkRNG.nextInt(6) + e.fireworkRNG.nextInt(7)
	e.spawnData = ownerID + 1

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// tickFireworkRockets drives every firework in every region -- the sibling of tickThrowables/tickPotions.
// Runs on the coordinator with each region registered so the firework tick t.cur() (the detonation remove)
// resolves to the firework OWN store. A per-region snapshot keeps the loop stable across an in-loop discard.
// ADDITIVE + firework-gated (zero cost when none is in flight, so the pig oracle stream is unperturbed).
func (t *TickLoop) tickFireworkRockets() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isFirework {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickFirework(e)
			}
		})
	}
}

// tickFirework is the FireworkRocketEntity.tick port for one firework: the attached elytra-boost branch or
// the free-flight self-accel branch, then life++ and the detonation at life > lifetime.
func (t *TickLoop) tickFirework(e *Entity) {
	if e.fireworkAttachID != 0 {
		// ATTACHED: while the rider is still fall-flying, accelerate it along its look; re-anchor the firework.
		rider := t.playerByEntityID(e.fireworkAttachID)
		if rider != nil && rider.fallFlying && rider.playerEntity != nil {
			lx, ly, lz := playerViewVector(rider.yaw, rider.pitch) // rider.getLookAngle()
			re := rider.playerEntity
			// delta + (look*0.1 + (look*1.5 - delta)*0.5), component-wise (verified javap: the add(x,y,z)).
			re.vx += lx*fireworkBoostLookScale + (lx*fireworkBoostAimScale-re.vx)*fireworkBoostLerp
			re.vy += ly*fireworkBoostLookScale + (ly*fireworkBoostAimScale-re.vy)*fireworkBoostLerp
			re.vz += lz*fireworkBoostLookScale + (lz*fireworkBoostAimScale-re.vz)*fireworkBoostLerp
			if rider.client != nil {
				rider.client.Send(encodeSetEntityMotion(re)) // ServerPlayer is the motion authority
			}
			// setPos(rider + handAngle) + setDeltaMovement(rider delta): re-anchor to the rider. v1 uses the
			// rider position directly (handHoldingItemAngle is a small hand-offset -- a client-render nicety;
			// the observable boost is the rider velocity push above). The firework rides with the rider.
			t.cur().entities.move(e, re.x, re.y, re.z)
			e.vx, e.vy, e.vz = re.vx, re.vy, re.vz
		}
		// getHitResultOnMoveVector: the attached firework does not move independently (no block/entity hit
		// resolution needed -- it detonates on lifetime), so no clip here (v1 scope: the boost + detonation).
	} else {
		// FREE: if not shot at an angle, self-accelerate upward, then move.
		if !e.fireworkShotAngle {
			scale := fireworkFreeScale
			if e.horizontalCollision {
				scale = fireworkHCollScale
			}
			e.vx *= scale
			e.vz *= scale
			e.vy = e.vy*scale + fireworkUpwardAccel
		}
		// move(SELF, delta): advance the firework by its velocity (v1 has no per-firework block collision in
		// scope -- the free rocket flies straight up until its lifetime, matching the common launch case).
		t.cur().entities.move(e, e.x+e.vx, e.y+e.vy, e.z+e.vz)
	}

	// updateRotation() -- render angles; skipped (a client-render nicety, no gameplay effect).

	// life++ (post-increment: the detonation test uses the pre-increment life > lifetime, but vanilla bumps
	// life AFTER the launch-sound-at-life==0 check and BEFORE the life > lifetime test; v1 mirrors that order).
	e.fireworkLife++

	// Detonation: once life > lifetime, explode on the server.
	if e.fireworkLife > e.fireworkLifetime {
		t.explodeFirework(e)
	}
}

// explodeFirework is the FireworkRocketEntity.explode port: broadcast the detonation entity-event (17) to
// trackers, deal the explosion damage, then discard. Cite FireworkRocketEntity.explode.
func (t *TickLoop) explodeFirework(e *Entity) {
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, fireworkEventExplode)) // broadcastEntityEvent(this, 17)
	t.dealFireworkExplosionDamage(e)
	t.cur().entities.remove(e.id) // discard()
}

// dealFireworkExplosionDamage is the FireworkRocketEntity.dealExplosionDamage port: with explosion stars,
// baseDamage = 5.0 + stars*2. Hurt the attached rider (if any) for the full baseDamage, then every OTHER
// LivingEntity within 5 blocks (distanceToSqr <= 25) for baseDamage * sqrt((5 - distanceTo)/5) -- the
// linear-ish falloff by distance. A firework with NO stars deals 0 (baseDamage stays 0, the whole block is
// skipped). LOS (the ClipContext ray from the firework to two body points) is CITE-DEFERRED (v1 hurts every
// living entity in range -- the same simplification the lightning/AEC ports take for the visibility ray).
// Cite FireworkRocketEntity.dealExplosionDamage.
func (t *TickLoop) dealFireworkExplosionDamage(e *Entity) {
	if e.fireworkStarCount <= 0 {
		return // getExplosions().isEmpty() -> baseDamage 0.0 -> the whole damage block is skipped
	}
	baseDamage := float32(fireworkBaseDamage) + float32(e.fireworkStarCount*fireworkStarDamage) // 5 + stars*2

	src := damageSourceFireworks(e.fireworkOwnerID)

	// The attached rider takes the FULL baseDamage (no falloff) -- it is at the firework's own position.
	if e.fireworkAttachID != 0 {
		if rider := t.playerByEntityID(e.fireworkAttachID); rider != nil && !rider.dead {
			t.applyDamage(rider, src, baseDamage)
		}
	}

	// Every OTHER LivingEntity within 5 blocks, falloff by distance. Players first, then mobs (deterministic).
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if p.entityID == e.fireworkAttachID {
			continue // the rider is handled above (it == attachedToEntity -> skipped in the loop)
		}
		dx := p.x - e.x
		dy := (p.y + playerHeight/2) - (e.y + float64(e.height)/2)
		dz := p.z - e.z
		distSqr := dx*dx + dy*dy + dz*dz
		if distSqr > fireworkDamageRadiusSq {
			continue // distanceToSqr > 25.0 -> out of range
		}
		// LOS gate: cast up to 2 COLLIDER rays from the firework to the victim's feet (getY(0)) and mid
		// (getY(0.5)); damage only if at least one reaches unobstructed (a MISS). Cite dealExplosionDamage
		// (the j in [0,2) ClipContext loop -> flag; if(flag) hurt).
		if !t.fireworkHasLineOfSight(e, p.x, p.y, p.z, playerHeight) {
			continue
		}
		dist := math.Sqrt(distSqr)
		dmg := baseDamage * float32(math.Sqrt((fireworkDamageRadius-dist)/fireworkDamageRadius))
		t.applyDamage(p, src, dmg)
	}
	for _, m := range t.cur().entities.near(e.x, e.z, 1) {
		if m == nil || m == e || m.dead || !m.isAlive() || !isLivingMob(m) {
			continue
		}
		if m.id == e.fireworkAttachID {
			continue
		}
		dx := m.x - e.x
		dy := (m.y + float64(m.height)/2) - (e.y + float64(e.height)/2)
		dz := m.z - e.z
		distSqr := dx*dx + dy*dy + dz*dz
		if distSqr > fireworkDamageRadiusSq {
			continue
		}
		if !t.fireworkHasLineOfSight(e, m.x, m.y, m.z, float64(m.height)) {
			continue
		}
		dist := math.Sqrt(distSqr)
		dmg := baseDamage * float32(math.Sqrt((fireworkDamageRadius-dist)/fireworkDamageRadius))
		t.applyDamageEntity(m, src, dmg)
	}
}

// fireworkHasLineOfSight ports the FireworkRocketEntity.dealExplosionDamage per-victim visibility loop:
// cast COLLIDER rays from the firework's position to the victim's getY(0.5*j) for j in {0,1} (feet + mid);
// return true if ANY ray reaches the victim without hitting a collider (level.clip(...).getType() == MISS).
// A wall between the firework and the target blocks BOTH rays -> no damage. Cite
// FireworkRocketEntity.dealExplosionDamage (ClipContext COLLIDER/NONE, the j-loop flag).
func (t *TickLoop) fireworkHasLineOfSight(e *Entity, vx, vy, vz, victimHeight float64) bool {
	if t.world() == nil {
		return true // no world to clip against -> permissive (the pre-LoS behaviour)
	}
	for j := 0; j < 2; j++ {
		ty := vy + 0.5*float64(j)*victimHeight // getY(0.5*j): feet at j=0, mid at j=1
		if !t.clipBlocksCollider(e.x, e.y, e.z, vx, ty, vz) {
			return true // this ray is a MISS (clear) -> the firework can see the victim
		}
	}
	return false // both rays blocked -> occluded, no damage
}

// fireworksComponentID is the wire type id of minecraft:fireworks (data/registryid/datacomponenttype.go
// index 69). It prefixes the component in a SlotData added-component list.
const fireworksComponentID = 69

// readFireworksComponent decodes a firework_rocket stack's FIREWORKS data component, returning the
// flightDuration and the number of explosion stars. A stack with no FIREWORKS component yields (1, 0) --
// vanilla treats a bare firework as flightDuration 1, no stars (the ctor default `int i = 1`, and
// getExplosions() on a null component is List.of()). Only the ADDED-component list is scanned (a crafted /
// given firework carries the component as an added override), matching readBottlePotion. Cite the
// FireworkRocketEntity base ctor (Fireworks.flightDuration) + getExplosions (Fireworks.explosions).
func readFireworksComponent(s component.SlotData) (flightDuration, starCount int) {
	flightDuration, starCount = 1, 0 // ctor default: int i = 1; no stars
	if stackEmpty(s) || len(s.RawComponents) == 0 || int(s.AddedCount) <= 0 {
		return
	}
	r := bytes.NewReader(s.RawComponents)
	for i := int32(0); i < int32(s.AddedCount); i++ {
		var compType pk.VarInt
		if _, err := compType.ReadFrom(r); err != nil {
			return
		}
		comp := component.NewComponent(int32(compType))
		if comp == nil {
			return // unknown component: cannot advance the reader safely
		}
		if _, err := comp.ReadFrom(r); err != nil {
			return
		}
		if int32(compType) == fireworksComponentID {
			fw, ok := comp.(*component.Fireworks)
			if !ok {
				return
			}
			// i += fireworks.flightDuration() (the ctor adds the component duration to the base 1).
			flightDuration = 1 + int(fw.FlightDuration)
			starCount = len(fw.Explosions)
			return
		}
	}
	return
}

// tryUseFirework is the FireworkRocketItem.use port: a right-click with a firework_rocket while the player
// isFallFlying spawns a FireworkRocketEntity ATTACHED to the player (the elytra boost) and consumes 1.
// A NON-fall-flying use is PASS (returns false -> falls through to the food/other path; vanilla returns
// PASS so the block-place useOn path can run -- v1 has no firework block-place, so a grounded firework is a
// no-op). Firework-gated (a cheap id compare, no RNG draw -- the pig oracle is unperturbed). Cite
// FireworkRocketItem.use. Returns true if it handled the use (a boost fired).
func (t *TickLoop) tryUseFirework(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	if item.ID(held.ItemID) != item.FireworkRocket.ID {
		return false
	}
	if !p.fallFlying {
		return false // Player.isFallFlying() false -> PASS (v1: fall through / no-op)
	}
	flightDuration, starCount := readFireworksComponent(held)
	// FireworkRocketEntity(Level, ItemStack, LivingEntity): attached to the rider at its position.
	t.spawnAttachedFirework(p.entityID, p.entityID, p.x, p.y, p.z, flightDuration, starCount)

	// itemStack.consume(1, player): shrink by 1 unless creative.
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
