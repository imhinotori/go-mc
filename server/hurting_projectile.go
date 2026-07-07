package server

// hurting_projectile.go — HURTING PROJECTILES: a 1:1 port of the AbstractHurtingProjectile flight +
// per-kind onHit for small_fireball, large_fireball, and wither_skull
// (net.minecraft.world.entity.projectile.hurtingprojectile.*), decompiled from temp/cache/26.2-inner.jar
// (javap this session). A hurting projectile is a NON-mob moving Entity (isHurting) with no AI: its whole
// behavior is the physics + hit tick, the sibling of tickArrows / tickThrowables / tickPotions.
//
// The DEFINING difference from an arrow/throwable: NO gravity. It flies STRAIGHT with a self-acceleration
// term. Vanilla AbstractHurtingProjectile.tick per-tick order (verified javap):
//   1. applyInertia() : deltaMovement = (deltaMovement + deltaMovement.normalize()*accelerationPower) * inertia
//        inertia = getInertia() (0.95 air) or getLiquidInertia() (0.8 in water). accelerationPower default 0.1.
//   2. getHitResultOnMoveVector : clip the move segment vs blocks (COLLIDER) + entities (canHitEntity)
//   3. rotateTowardsMovement(0.2) ; setPos(hit-or-next)
//   4. if shouldBurn(): igniteForSeconds(1)   (fireballs burn; a wither skull does not)
//   5. if hit != MISS && isAlive(): hitTargetOrDeflectSelf(hit) -> onHit(hit)
// NOTE the order is inertia+accelerate BEFORE the move, and there is NO per-tick gravity term. Preserved.
//
// onHit per kind (verified javap):
//   - SmallFireball.onHitEntity : igniteForSeconds(5); hurt(fireball(this,owner), 5.0); if the hit fails,
//       restore the victim's prior fire ticks. onHitBlock: if owner is not a Mob OR mobGriefing, place fire
//       in the empty block adjacent to the hit face. Then super.onHit discards.
//   - LargeFireball.onHitEntity : hurt(fireball(this,owner), 6.0). onHit: explode(explosionPower=1,
//       fire=mobGriefing, MOB) then discard.
//   - WitherSkull.onHitEntity : owner LivingEntity -> hurt(witherSkull(this,owner), 8.0) (if it fails, heal
//       owner 5.0); owner null -> hurt(magic(), 5.0). On a successful hit to a LivingEntity, on NORMAL add
//       wither 20*10 ticks amp1, on HARD 20*40 ticks amp1 (EASY/PEACEFUL: no effect). onHit: explode(1,
//       fire=false, MOB) then discard. getInertia() is 0.73 when dangerous, else 0.95. shouldBurn == false.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// hurting-projectile kinds.
const (
	hurtSmallFireball = iota
	hurtLargeFireball
	hurtWitherSkull
)

// AbstractHurtingProjectile physics constants (verified javap — exact float values).
const (
	hurtInitialAccelPow  = 0.1  // AbstractHurtingProjectile ctor: accelerationPower = 0.1
	hurtInertiaAir       = 0.95 // getInertia()
	hurtInertiaWater     = 0.8  // getLiquidInertia()
	hurtInertiaDangerous = 0.73 // WitherSkull.getInertia() when isDangerous()
	hurtDespawnTicks     = 1200 // a hurting projectile that never lands still despawns (lifetime backstop)
	// smallFireballDamage / largeFireballDamage / witherSkullDamage are the onHitEntity hurt amounts.
	smallFireballDamage = 5.0 // SmallFireball.onHitEntity hurt 5.0F
	largeFireballDamage = 6.0 // LargeFireball.onHitEntity hurt 6.0F
	witherSkullDamage   = 8.0 // WitherSkull.onHitEntity hurt 8.0F (owner LivingEntity)
	witherSkullMagic    = 5.0 // WitherSkull.onHitEntity hurt 5.0F magic (owner null)
	smallFireballIgnite = 5.0 // SmallFireball.onHitEntity igniteForSeconds(5)
	// largeFireballExplosionPower / witherSkullExplosionPower are the explosion radii on hit.
	largeFireballExplosionPower = 1 // LargeFireball default explosionPower (byte 1)
	witherSkullExplosionPower   = 1 // WitherSkull.onHit explode radius 1.0
)

// hurtingEntityType returns the wire entity type for a hurting-projectile kind.
func hurtingEntityType(kind int) entity.Entity {
	switch kind {
	case hurtLargeFireball:
		return entity.Fireball // LargeFireball == the "fireball" entity type (ghast)
	case hurtWitherSkull:
		return entity.WitherSkull
	default:
		return entity.SmallFireball
	}
}

// spawnHurtingProjectile creates a hurting projectile at (x,y,z) and assigns its directional movement from
// the aim vector (dir), scaled by accelerationPower — the port of AbstractHurtingProjectile's
// assignDirectionalMovement(movement, accelerationPower) = setDeltaMovement(movement.normalize()*power).
// The projectile then flies straight, re-accelerating along its own heading each tick. Owned by ownerID;
// added to the owner region's store (the tracker broadcasts AddEntity next tick, exactly as an arrow).
// Cite AbstractHurtingProjectile(EntityType, LivingEntity, Vec3, Level) + assignDirectionalMovement.
func (t *TickLoop) spawnHurtingProjectile(ownerID int32, kind int, x, y, z, dirX, dirY, dirZ float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), hurtingEntityType(kind), x, y, z)
	e.isHurting = true
	e.hurtingKind = kind
	e.hurtOwnerID = ownerID
	e.hurtAccelPow = hurtInitialAccelPow
	e.hurtExplosion = largeFireballExplosionPower
	e.spawnData = ownerID + 1 // ClientboundAddEntity data: ownerId+1 (0 == none)

	// assignDirectionalMovement(movement, accelerationPower): normalize the aim vector, scale by power.
	mag := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if mag > 0 {
		e.vx = dirX / mag * e.hurtAccelPow
		e.vy = dirY / mag * e.hurtAccelPow
		e.vz = dirZ / mag * e.hurtAccelPow
	}

	// setRot from the launch vector (the ctor snaps yRot/xRot to the direction).
	horiz := math.Sqrt(e.vx*e.vx + e.vz*e.vz)
	e.yaw = float32(math.Atan2(e.vx, e.vz) * 180.0 / math.Pi)
	e.pitch = float32(math.Atan2(e.vy, horiz) * 180.0 / math.Pi)
	e.headYaw = e.yaw

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// tickHurtingProjectiles drives every hurting projectile in every region — the sibling of tickArrows /
// tickThrowables / tickPotions. Runs on the coordinator (quiescent) with each region registered so the
// tick's t.cur() (move re-bucket / discard) resolves to the projectile's OWN store. A per-region snapshot
// keeps the loop stable across an in-loop discard (a hit/despawn removal).
func (t *TickLoop) tickHurtingProjectiles() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isHurting {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickHurtingProjectile(e)
			}
		})
	}
}

// tickHurtingProjectile is the port of AbstractHurtingProjectile.tick for one projectile: applyInertia
// (re-accelerate along heading, scale by inertia — NO gravity), then the swept block+entity hit test; on a
// hit it runs the per-kind onHit and discards; else it moves and ages. The owner-gone/unchunked discard
// (vanilla: owner removed OR no chunk at blockPosition) is preserved.
func (t *TickLoop) tickHurtingProjectile(e *Entity) {
	// (1) applyInertia: deltaMovement = (deltaMovement + deltaMovement.normalize()*accelerationPower) * inertia.
	inertia := hurtInertiaAir
	if t.isWaterAt(int(math.Floor(e.x)), int(math.Floor(e.y)), int(math.Floor(e.z))) {
		inertia = hurtInertiaWater
	} else if e.hurtingKind == hurtWitherSkull && e.hurtDangerous {
		inertia = hurtInertiaDangerous // WitherSkull.getInertia() overrides the AIR inertia when dangerous
	}
	speed := math.Sqrt(e.vx*e.vx + e.vy*e.vy + e.vz*e.vz)
	if speed > 0 {
		e.vx += e.vx / speed * e.hurtAccelPow
		e.vy += e.vy / speed * e.hurtAccelPow
		e.vz += e.vz / speed * e.hurtAccelPow
	}
	e.vx *= inertia
	e.vy *= inertia
	e.vz *= inertia

	// (2) getHitResultOnMoveVector: clip the move segment vs blocks FIRST (COLLIDER), then test entities up
	// to that clipped endpoint (an entity behind a wall the projectile hits first is not hit).
	ox, oy, oz := e.x, e.y, e.z
	nx, ny, nz := ox+e.vx, oy+e.vy, oz+e.vz
	hx, hy, hz, blockHit := t.arrowClipSegment(ox, oy, oz, nx, ny, nz)
	endX, endY, endZ := nx, ny, nz
	if blockHit {
		endX, endY, endZ = hx, hy, hz
	}

	// Entity hit (players only in v1 — the arrow-scope note; a ghast/blaze fireball targets a player).
	if victim := t.projectileFindHitPlayer(e.hurtOwnerID, ox, oy, oz, endX, endY, endZ); victim != nil {
		t.hurtingOnHitEntity(e, victim)
		t.hurtingOnHit(e, victim.x, victim.y, victim.z) // per-kind onHit (explosion) + discard
		return
	}
	if blockHit {
		// Update render angles toward movement (rotateTowardsMovement 0.2 — v1 sets directly), move to impact.
		t.hurtingUpdateRotation(e)
		t.cur().entities.move(e, endX, endY, endZ)
		t.hurtingOnHitBlock(e, endX, endY, endZ, hx, hy, hz)
		t.hurtingOnHit(e, endX, endY, endZ)
		return
	}

	// (3) No hit: move to the next position + age. rotateTowardsMovement.
	t.hurtingUpdateRotation(e)
	t.cur().entities.move(e, nx, ny, nz)

	e.hurtLife++
	if e.hurtLife >= hurtDespawnTicks {
		t.cur().entities.remove(e.id)
	}
}

// hurtingUpdateRotation sets the render angles from the movement (ProjectileUtil.rotateTowardsMovement;
// v1 snaps directly rather than lerping by 0.2 — the observable heading is identical after settle).
func (t *TickLoop) hurtingUpdateRotation(e *Entity) {
	horiz := math.Sqrt(e.vx*e.vx + e.vz*e.vz)
	if e.vx != 0 || e.vz != 0 {
		e.yaw = float32(math.Atan2(e.vx, e.vz) * 180.0 / math.Pi)
		e.headYaw = e.yaw
	}
	e.pitch = float32(math.Atan2(e.vy, horiz) * 180.0 / math.Pi)
}

// hurtingOnHitEntity ports the per-kind onHitEntity for a player victim: the fireball/wither-skull damage
// (attributed to the owner). Igniting the victim (SmallFireball igniteForSeconds(5)) is CITE-DEFERRED for a
// player victim (no player fire-tick state yet, like the arrow's mob-victim note); the damage — the visible
// gameplay — applies through the shared player damage path.
func (t *TickLoop) hurtingOnHitEntity(e *Entity, victim *tickPlayer) {
	switch e.hurtingKind {
	case hurtSmallFireball:
		// SmallFireball.onHitEntity: igniteForSeconds(5) [player fire deferred]; hurt(fireball, 5.0).
		t.applyDamage(victim, damageSourceFireball(e.hurtOwnerID), smallFireballDamage)
	case hurtLargeFireball:
		// LargeFireball.onHitEntity: hurt(fireball, 6.0). (The explosion is dealt separately in onHit.)
		t.applyDamage(victim, damageSourceFireball(e.hurtOwnerID), largeFireballDamage)
	case hurtWitherSkull:
		// WitherSkull.onHitEntity: owner LivingEntity -> hurt(witherSkull, 8.0), else hurt(magic, 5.0). The
		// owner-heal-on-failed-hit branch needs the owner as a LivingEntity; a player owner uses the magic
		// fallback path only when there is NO living owner. Here the owner is the shooting mob (blaze/wither),
		// so witherSkull with the owner id is the correct source; the 5.0 magic branch is the ownerless case.
		if e.hurtOwnerID != 0 {
			t.applyDamage(victim, damageSourceWitherSkull(e.hurtOwnerID), witherSkullDamage)
		} else {
			t.applyDamage(victim, damageSourceMagic(), witherSkullMagic)
		}
		// wither effect on a LivingEntity victim: NORMAL 20*10 ticks amp1, HARD 20*40 ticks amp1. A player
		// victim carries player effects (addPlayerEffect); no wither on EASY/PEACEFUL (dur factor 0).
		if dur := witherSkullEffectTicks(); dur > 0 {
			t.addPlayerEffect(victim, e.hurtOwnerID, effectWither, dur, 1, 1.0)
		}
	}
}

// witherSkullEffectTicks ports WitherSkull.onHitEntity's difficulty-scaled wither duration: NORMAL -> 10,
// HARD -> 40, else 0 (EASY/PEACEFUL: no effect). The applied duration is 20*factor ticks, amplifier 1.
func witherSkullEffectTicks() int {
	switch serverDifficulty {
	case difficultyNormal:
		return 20 * 10
	case difficultyHard:
		return 20 * 40
	default:
		return 0
	}
}

// hurtingOnHitBlock ports SmallFireball.onHitBlock: if the owner is not a Mob (a player-shot fireball) OR
// mobGriefing is on, and the block adjacent to the hit face is empty, place fire there. Large fireball /
// wither skull do not override onHitBlock (their block impact just runs onHit's explosion). Cite
// SmallFireball.onHitBlock. hx/hy/hz is the exact hit point; endX/Y/Z is the clipped move endpoint.
func (t *TickLoop) hurtingOnHitBlock(e *Entity, endX, endY, endZ, hx, hy, hz float64) {
	if e.hurtingKind != hurtSmallFireball {
		return
	}
	// The owner-not-a-Mob gate: a player owner always places fire; a mob owner only under MOB_GRIEFING. v1
	// tracks the owner as a thin id — a player id resolves via playerByEntityID; anything else is a mob.
	ownerIsMob := e.hurtOwnerID == 0 || t.playerByEntityID(e.hurtOwnerID) == nil
	if ownerIsMob && !mobGriefing {
		return
	}
	// Place fire in the block adjacent to the hit face (BlockPos.relative(direction)). v1 approximates the
	// hit face by the dominant axis of (hit - endpoint) and steps one block along it into the empty cell.
	fx, fy, fz := hurtingFireCell(endX, endY, endZ, hx, hy, hz)
	cell := pk.Position{X: fx, Y: fy, Z: fz}
	// isEmptyBlock(pos): only fill AIR. Place the plain fire block (SoulFireBlock / the neighbor-connectivity
	// fire-face bitmask is a cited render/spread simplification, exactly as boltPlaceFire in lightning.go).
	if !t.isEmptyBlockAt(cell) {
		return
	}
	fire := block.DefaultStateID["minecraft:fire"]
	if t.world().SetBlock(cell, fire, dimMinY) {
		t.broadcastBlockUpdate(cell, fire)
	}
}

// hurtingFireCell picks the cell adjacent to the hit block along the face the projectile struck — the block
// the fireball's flight was heading INTO. It steps one block back along the dominant approach axis from the
// hit point, matching BlockHitResult.getBlockPos().relative(getDirection()) (the air side of the face).
func hurtingFireCell(endX, endY, endZ, hx, hy, hz float64) (int, int, int) {
	bx, by, bz := int(math.Floor(hx)), int(math.Floor(hy)), int(math.Floor(hz))
	// Approach direction = movement into the block; the fire goes on the side the projectile came from.
	dx, dy, dz := hx-endX, hy-endY, hz-endZ
	adx, ady, adz := math.Abs(dx), math.Abs(dy), math.Abs(dz)
	switch {
	case adx >= ady && adx >= adz:
		if dx > 0 {
			bx--
		} else {
			bx++
		}
	case ady >= adx && ady >= adz:
		if dy > 0 {
			by--
		} else {
			by++
		}
	default:
		if dz > 0 {
			bz--
		} else {
			bz++
		}
	}
	return bx, by, bz
}

// hurtingOnHit ports the per-kind AbstractHurtingProjectile.onHit tail: the explosion (large fireball /
// wither skull) then discard. The small fireball has no explosion — it just discards. x/y/z is the impact
// position (the explosion center). Cite LargeFireball.onHit / WitherSkull.onHit (Level.explode + discard).
func (t *TickLoop) hurtingOnHit(e *Entity, x, y, z float64) {
	switch e.hurtingKind {
	case hurtLargeFireball:
		// explode(this, x, y, z, explosionPower, mobGriefing, MOB). v1 explode is the MOB path (fire=false,
		// block interaction gated on mobGriefing internally); the fire=mobGriefing arg is the createFire
		// seam, cite-deferred (explosion.go notes createFire is deferred). Radius = explosionPower.
		t.explode(e.id, x, y, z, float64(e.hurtExplosion))
	case hurtWitherSkull:
		// explode(this, x, y, z, 1.0, false, MOB). Radius 1, no fire.
		t.explode(e.id, x, y, z, float64(witherSkullExplosionPower))
	}
	t.cur().entities.remove(e.id) // discard()
}

// shouldBurnKind ports AbstractHurtingProjectile.shouldBurn (default true, fireballs) vs WitherSkull
// (false). A burning projectile calls igniteForSeconds(1) each tick — self-ignition is a render/no-op on a
// projectile with no fire-tick effect of its own, so v1 skips the self-ignite call and keeps the predicate
// for documentation parity; it does not change any observable projectile behavior. Retained for the port map.
func shouldBurnKind(kind int) bool { return kind != hurtWitherSkull }
