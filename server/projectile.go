package server

// projectile.go — PROJECTILE-01 (Task #8): the AbstractArrow flight subsystem, a 1:1 port of
// net.minecraft.world.entity.projectile.arrow.AbstractArrow.tick / onHitEntity (temp/cache/26.2-inner.jar,
// CFR/javap this session — see .planning/PROJECTILE-JARNOTES.md). An arrow is a NON-mob moving Entity
// (isArrow) with no AI: its whole behavior is this physics + hit tick, the sibling of tickItems/tickOrbs.
//
// The vanilla per-tick order for an airborne arrow (AbstractArrow.tick, physicsEnabled branch):
//   1. if the arrow's block cell is solid AND its position is inside that block's collision box:
//        deltaMovement = ZERO; inGround = true.  (the "stuck in a block" latch)
//   2. if inGround: tickDespawn (life++ / discard at 1200) and RETURN — a grounded arrow does not fly.
//   3. else: move along deltaMovement, testing entities on the swept segment (stepMoveAndHit →
//      onHitEntity for the first entity the segment passes through), then:
//        deltaMovement *= getAirDrag() (0.99)   [waterInertia 0.6 in water — v1 no fluid drag branch here]
//        applyGravity: deltaMovement.y -= getDefaultGravity() (0.05)
//   The ORDER is: move → drag → gravity (verified CFR AbstractArrow.tick).
//
// onHitEntity (verified CFR): pow = deltaMovement.length(); damage = ceil(clamp(pow*baseDamage,0,MAXINT));
// hurt the victim with damageSources().arrow(this, owner); the arrow is then discarded (v1: no pierce).
// baseDamage was set at spawn by setBaseDamageFromMob(power) = power*2.0 + triangle(difficulty*0.11,0.57425).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// AbstractArrow physics constants (verified CFR — exact float values).
const (
	arrowAirDrag      = 0.99 // AbstractArrow.getAirDrag()
	arrowGravity      = 0.05 // AbstractArrow.getDefaultGravity() (Arrow does not override)
	arrowDespawnTicks = 1200 // AbstractArrow.tickDespawn: discard at life>=1200
)

// arrowTriangle is RandomSource.triangle(mode, deviation) == mode + deviation*(nextDouble()-nextDouble()).
// The 2-nextDouble draw ORDER matters for lockstep (the shooter's seeded stream). Cite RandomSource.triangle.
func arrowTriangle(r *entityRandom, mode, deviation float64) float64 {
	return mode + deviation*(r.nextDouble()-r.nextDouble())
}

// spawnArrow creates an AbstractArrow at (x,y,z) with the given deltaMovement + owner + baseDamage and
// adds it to the owner region's store (the tracker broadcasts its AddEntity next tick, exactly as an item
// drop / XP orb rides the store-add path). yaw/pitch are seeded from the movement vector (Projectile.shoot:
// yRot=atan2(x,z), xRot=atan2(y, horizontalDistance)). The spawnData wire field carries ownerId+1
// (ClientboundAddEntity object data == owner link for the client crit visual). Returns the arrow entity.
func (t *TickLoop) spawnArrow(shooterID int32, x, y, z, vx, vy, vz, baseDamage float64) *Entity {
	a := NewEntity(t.idAlloc.AllocID(), entity.Arrow, x, y, z)
	a.isArrow = true
	a.arrowShooterID = shooterID
	a.arrowBaseDamage = baseDamage
	a.vx, a.vy, a.vz = vx, vy, vz
	a.spawnData = shooterID + 1 // ClientboundAddEntity data: ownerId+1 (0 == none)

	// Projectile.shoot seeds the render angles from the launch vector.
	horiz := math.Sqrt(vx*vx + vz*vz)
	a.yaw = float32(math.Atan2(vx, vz) * 180.0 / math.Pi)
	a.pitch = float32(math.Atan2(vy, horiz) * 180.0 / math.Pi)
	a.headYaw = a.yaw

	owner := t.regionForEntity(a)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(a)
	return a
}

// tickArrows drives every arrow in every region, the sibling of tickOrbs/tickItems. It runs on the
// coordinator (quiescent) and processes each region WITH that region registered (withRegion) so the
// arrow tick's t.cur() (the moveEntity re-bucket + the despawn remove) resolves to the arrow's OWN store.
// A per-region snapshot keeps the loop stable across an in-loop discard (a hit/despawn removal).
func (t *TickLoop) tickArrows() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isArrow {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickArrow(e)
			}
		})
	}
}

// tickArrow is the port of AbstractArrow.tick (physicsEnabled branch) for one arrow. It latches into a
// solid block, despawns a grounded arrow, else flies (segment entity-hit → drag → gravity) and despawns
// at 1200 ticks.
func (t *TickLoop) tickArrow(e *Entity) {
	// (1) Block latch: if the arrow's cell is solid, stop and stick. (Vanilla tests the collision-shape
	// AABB contains the position; v1 uses the block-solidity gate isSolidAt — the same observable "an
	// arrow that reaches a solid block stops there".)
	cell := pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(e.y)), Z: int(math.Floor(e.z))}
	if !e.arrowInGround && t.isSolidAt(cell) {
		e.vx, e.vy, e.vz = 0, 0, 0
		e.arrowInGround = true
	}

	// (2) Grounded arrow: tickDespawn (life++ / discard at 1200) and return — no flight.
	if e.arrowInGround {
		e.arrowLife++
		if e.arrowLife >= arrowDespawnTicks {
			t.cur().entities.remove(e.id)
		}
		return
	}

	// (3) Airborne: clip the flight segment against solid blocks FIRST (AbstractArrow.tick calls
	// clipIncludingBorder before stepMoveAndHit), then test entities only up to that clipped endpoint —
	// an entity BEHIND a wall the arrow hit first is not hit (stepMoveAndHit uses blockHitResult's
	// location as the segment end).
	ox, oy, oz := e.x, e.y, e.z
	nx, ny, nz := ox+e.vx, oy+e.vy, oz+e.vz
	hx, hy, hz, blockHit := t.arrowClipSegment(ox, oy, oz, nx, ny, nz)
	endX, endY, endZ := nx, ny, nz
	if blockHit {
		endX, endY, endZ = hx, hy, hz
	}

	if victim := t.arrowFindHitPlayer(e, ox, oy, oz, endX, endY, endZ); victim != nil {
		t.arrowOnHitPlayer(e, victim)
		return // v1: no pierce — the arrow is consumed by the hit
	}

	if blockHit {
		t.cur().entities.move(e, endX, endY, endZ)
		e.vx, e.vy, e.vz = 0, 0, 0
		e.arrowInGround = true
	} else {
		t.cur().entities.move(e, nx, ny, nz)
	}

	// Update the render angles from the (pre-drag) movement — AbstractArrow lerps them; v1 sets directly.
	horiz := math.Sqrt(e.vx*e.vx + e.vz*e.vz)
	if e.vx != 0 || e.vz != 0 {
		e.yaw = float32(math.Atan2(e.vx, e.vz) * 180.0 / math.Pi)
		e.headYaw = e.yaw
	}
	e.pitch = float32(math.Atan2(e.vy, horiz) * 180.0 / math.Pi)

	// getAirDrag()==0.99 applied to all three axes (applyInertia == deltaMovement.scale(0.99)).
	e.vx *= arrowAirDrag
	e.vy *= arrowAirDrag
	e.vz *= arrowAirDrag

	// applyGravity: deltaMovement.y -= getDefaultGravity() (0.05). (Airborne only — checked above.)
	if !e.arrowInGround {
		e.vy -= arrowGravity
	}

	// tickDespawn counts even while flying (AbstractArrow.life increments every tick via super.tick /
	// the ground branch; the 1200 cap is a hard lifetime). A never-landing arrow still despawns.
	e.arrowLife++
	if e.arrowLife >= arrowDespawnTicks {
		t.cur().entities.remove(e.id)
	}
}

// arrowFindHitPlayer is the ProjectileUtil.getEntityHitResult port scoped to players: the FIRST player
// whose collision AABB the arrow's flight segment (origin→next) passes through, excluding the shooter.
// It returns the nearest such player (sorted by distance from the segment origin, matching stepMoveAndHit's
// entitiesHit.sort). v1 tests players only (mobs are not arrow victims yet — a cited scope note).
func (t *TickLoop) arrowFindHitPlayer(e *Entity, ox, oy, oz, nx, ny, nz float64) *tickPlayer {
	var best *tickPlayer
	bestT := math.Inf(1)
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if p.entityID == e.arrowShooterID {
			continue // the arrow never hits its own shooter (checkLeftOwner guard)
		}
		// Build the player's collision AABB (0.6×1.8, base at feet), inflated by the arrow's half-size
		// (0.3) — ProjectileUtil inflates the target box by the projectile's bounding box before the clip.
		half := entity.Arrow.Width / 2.0
		minX := p.x - playerWidth/2 - half
		maxX := p.x + playerWidth/2 + half
		minY := p.y - half
		maxY := p.y + playerHeight + half
		minZ := p.z - playerWidth/2 - half
		maxZ := p.z + playerWidth/2 + half
		if hit, tHit := segmentAABB(ox, oy, oz, nx, ny, nz, minX, minY, minZ, maxX, maxY, maxZ); hit {
			if tHit < bestT {
				bestT = tHit
				best = p
			}
		}
	}
	return best
}

// arrowOnHitPlayer is the AbstractArrow.onHitEntity port for a player victim: damage =
// ceil(clamp(deltaMovement.length() * baseDamage, 0, MAXINT)), hurt with the arrow damage source
// (attributed to the shooter), then discard the arrow (v1: no pierce). Cite AbstractArrow.onHitEntity.
func (t *TickLoop) arrowOnHitPlayer(e *Entity, victim *tickPlayer) {
	pow := math.Sqrt(e.vx*e.vx + e.vy*e.vy + e.vz*e.vz) // getDeltaMovement().length()
	raw := pow * e.arrowBaseDamage
	if raw < 0 {
		raw = 0
	}
	dmg := int(math.Ceil(raw)) // Mth.ceil(clamp(...)); clamp upper bound is MAXINT (unreachable here)
	src := damageSourceArrow(e.arrowShooterID)
	t.applyDamage(victim, src, float32(dmg))
	t.cur().entities.remove(e.id)
}

// segmentAABB reports whether the segment (o→n) intersects the axis-aligned box and, if so, the entry
// parameter t in [0,1] along the segment (the slab method). Used to pick the FIRST entity the arrow's
// flight passes through this tick.
func segmentAABB(ox, oy, oz, nx, ny, nz, minX, minY, minZ, maxX, maxY, maxZ float64) (bool, float64) {
	dx, dy, dz := nx-ox, ny-oy, nz-oz
	tmin, tmax := 0.0, 1.0
	for i := 0; i < 3; i++ {
		var o, d, lo, hi float64
		switch i {
		case 0:
			o, d, lo, hi = ox, dx, minX, maxX
		case 1:
			o, d, lo, hi = oy, dy, minY, maxY
		default:
			o, d, lo, hi = oz, dz, minZ, maxZ
		}
		if math.Abs(d) < 1e-12 {
			// Segment parallel to this slab: miss unless the origin is already within it.
			if o < lo || o > hi {
				return false, 0
			}
			continue
		}
		t1 := (lo - o) / d
		t2 := (hi - o) / d
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return false, 0
		}
	}
	return true, tmin
}

// arrowClipSegment samples the flight segment (o→n) in ≤0.25-block steps and returns the first sampled
// point whose block cell is solid (hit=true), so the arrow sticks at the point of contact instead of
// tunneling through a thin block. Returns hit=false (and the endpoint) when the whole segment is clear.
// This is the v1 stand-in for level.clipIncludingBorder(ClipContext.Block.COLLIDER) — same observable
// "an arrow that reaches a solid block stops there".
func (t *TickLoop) arrowClipSegment(ox, oy, oz, nx, ny, nz float64) (float64, float64, float64, bool) {
	dx, dy, dz := nx-ox, ny-oy, nz-oz
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length < 1e-9 {
		return nx, ny, nz, false
	}
	steps := int(math.Ceil(length / 0.25))
	if steps < 1 {
		steps = 1
	}
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		sx := ox + dx*f
		sy := oy + dy*f
		sz := oz + dz*f
		cell := pk.Position{X: int(math.Floor(sx)), Y: int(math.Floor(sy)), Z: int(math.Floor(sz))}
		if t.isSolidAt(cell) {
			return sx, sy, sz, true
		}
	}
	return nx, ny, nz, false
}
