package server

// explosion.go — MOB-HOST-06 (Task #9): the ServerExplosion port (the creeper's blast), a 1:1 port of
// net.minecraft.world.level.ServerExplosion.hurtEntities + getSeenPercent + the
// ExplosionDamageCalculator entity-damage/knockback formulas (temp/cache/26.2-inner.jar, javap this
// session). The ENTITY effect (the iconic "a creeper kills you") is here; the BLOCK-DESTRUCTION half
// (calculateExplodedPositions + interactWithBlocks — the resistance-attenuated ray collection, the
// Util.shuffle, and the per-block destroy+drop, gated by the MOB_GRIEFING gamerule) lives in
// explosion_blocks.go, driven by the codegen'd level/block.ExplosionResistance table.
//
// Verified constants (bytecode): entity in-range if sqrt(distSqr)/(radius*2) <= 1.0; the exposure fraction
// is getSeenPercent (a 3D grid of collision ray casts, MISS==clear); damage = ((p²+p)/2 * 7.0 * (radius*2)
// + 1.0) with p=(1-dist)*exposure; knockback impulse = dir * (1-dist)*exposure*kbMult(1.0)*(1-kbResist);
// the radius<1e-5 guard skips entity damage. The 16³-shell ray grid rolls radius*(0.7 + nextFloat()*0.6)
// per ray from level.random.

import "math"

// Explosion constants (bytecode-verified).
const (
	explosionRayPowerBase   = 0.7    // radius * (0.7 + nextFloat()*0.6)
	explosionRayPowerRange  = 0.6    //   the nextFloat() scale
	explosionRadiusEpsilon  = 1.0e-5 // radius < 1e-5f skips entity damage
	explosionDamageConstant = 7.0    // the (p²+p)/2 * 7.0 * doubleRadius + 1.0 formula constant
	explosionKnockbackMult  = 1.0    // ExplosionDamageCalculator.getKnockbackMultiplier == 1.0f
)

// explode is the port of ServerExplosion.explode for the creeper path (interaction MOB). It mirrors
// the vanilla method-and-RNG order EXACTLY: (1) collect the destroyed-block set via
// calculateExplodedPositions (this draws the 16^3-shell ray nextFloats from level.random — the FIRST
// RNG use), (2) hurtEntities (falloff+exposure damage + knockback; no RNG), (3) if the explosion
// interacts with blocks (MOB_GRIEFING gate) interactWithBlocks(toBlow) (Util.shuffle nextInts + the
// per-block drop rolls). srcID is the exploding entity id (excluded from the hurt set — a creeper does
// not damage itself; it is already discarded). The gameEvent(EXPLODE) + createFire are cite-deferred
// (no gameEvent/fire seam; a creeper explosion sets fire=false so createFire never runs anyway).
//
//	[VERIFIED javap ServerExplosion.explode: calculateExplodedPositions(); hurtEntities();
//	 if (interactsWithBlocks()) interactWithBlocks(list); if (fire) createFire(list).]
func (t *TickLoop) explode(srcID int32, x, y, z, radius float64) {
	toBlow := t.calculateExplodedPositions(x, y, z, radius)
	t.hurtEntitiesFromExplosion(srcID, x, y, z, radius)
	// interactsWithBlocks(): blockInteraction != KEEP. For a creeper (ExplosionInteraction.MOB) the
	// interaction is KEEP exactly when MOB_GRIEFING is off (ServerLevel.explode); so gate on mobGriefing
	// — when off, NO block is removed (the vanilla KEEP path). The ray nextFloats above are still drawn
	// (calculateExplodedPositions runs unconditionally in vanilla), so level.random stays in lockstep
	// regardless of the gamerule. Cite ServerExplosion.explode + interactsWithBlocks + ServerLevel.explode.
	if t.gameRule(ruleMobGriefing) {
		t.interactWithBlocks(toBlow, radius)
	}
}

// hurtEntitiesFromExplosion is the port of ServerExplosion.hurtEntities: for every player + mob within
// radius*2 of the blast, apply the falloff+exposure damage and the knockback impulse. The exploding
// entity (srcID) is excluded. Cite ServerExplosion.hurtEntities + ExplosionDamageCalculator.
func (t *TickLoop) hurtEntitiesFromExplosion(srcID int32, x, y, z, radius float64) {
	if radius < explosionRadiusEpsilon {
		return
	}
	doubleRadius := radius * 2.0

	// Players.
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		// distanceToSqr(center) uses the entity's position (feet). in-range if sqrt/doubleRadius <= 1.
		dx, dy, dz := p.x-x, p.y-y, p.z-z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / doubleRadius
		if dist > 1.0 {
			continue
		}
		// getEyePosition for the knockback direction origin (players use eye pos in vanilla hurtEntities).
		exOx, exOy, exOz := p.x, p.y+playerHeight*0.85, p.z
		exposure := t.explosionSeenPercent(x, y, z, exOx-playerWidth/2, exOy-playerHeight*0.85, exOz-playerWidth/2, playerWidth, playerHeight)
		impact := (1.0 - dist) * float64(exposure)
		dmg := (impact*impact+impact)/2.0*explosionDamageConstant*doubleRadius + 1.0
		if dmg > 0 {
			t.applyDamage(p, damageSourceOf(damageTypeExplosion), float32(dmg))
		}
		// Knockback: dir(eye - center).normalize() * (1-dist)*exposure*kbMult*(1-kbResist). No kbResist
		// attribute on players in v1 (0). Apply to the player's store entity + send one SetEntityMotion.
		t.applyExplosionKnockback(p, x, y, z, exOx, exOy, exOz, dist, float64(exposure))
	}

	// Mobs (store entities). A creeper explosion damages nearby mobs too (chain reactions, farm kills).
	for _, e := range t.cur().entities.byID {
		if e == nil || e.id == srcID || e.dead || e.ai == nil {
			continue
		}
		dx, dy, dz := e.x-x, e.y-y, e.z-z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / doubleRadius
		if dist > 1.0 {
			continue
		}
		exposure := t.explosionSeenPercent(x, y, z, e.x-e.width/2, e.y, e.z-e.width/2, e.width, e.height)
		impact := (1.0 - dist) * float64(exposure)
		dmg := (impact*impact+impact)/2.0*explosionDamageConstant*doubleRadius + 1.0
		if dmg > 0 {
			t.applyDamageEntity(e, damageSourceOf(damageTypeExplosion), float32(dmg))
		}
	}
}

// applyExplosionKnockback pushes a player away from the blast center by the vanilla impulse and sends the
// single SetEntityMotion. dir = (eye - center).normalize(); power = (1-dist)*exposure*1.0*(1-0). Cite
// ServerExplosion.hurtEntities (entity.push(direction.scale(knockbackPower))).
func (t *TickLoop) applyExplosionKnockback(p *tickPlayer, cx, cy, cz, eyeX, eyeY, eyeZ, dist, exposure float64) {
	dx, dy, dz := eyeX-cx, eyeY-cy, eyeZ-cz
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if l < 1e-9 {
		return
	}
	dx, dy, dz = dx/l, dy/l, dz/l
	power := (1.0 - dist) * exposure * explosionKnockbackMult // *(1 - kbResist=0)
	if p.playerEntity == nil {
		return
	}
	p.playerEntity.vx += dx * power
	p.playerEntity.vy += dy * power
	p.playerEntity.vz += dz * power
	if p.client != nil {
		p.client.Send(encodeSetEntityMotion(p.playerEntity))
	}
}

// explosionSeenPercent is the port of ServerExplosion.getSeenPercent: sample a grid of points across the
// target's AABB and return the fraction whose straight line to the blast center is UNOBSTRUCTED (a MISS
// clip). No RNG (pure ray casts). The grid density derives from the box size exactly as vanilla. Cite
// ServerExplosion.getSeenPercent.
func (t *TickLoop) explosionSeenPercent(cx, cy, cz, minX, minY, minZ, w, h float64) float32 {
	maxX, maxY, maxZ := minX+w, minY+h, minZ+w
	xs := 1.0 / ((maxX-minX)*2.0 + 1.0)
	ys := 1.0 / ((maxY-minY)*2.0 + 1.0)
	zs := 1.0 / ((maxZ-minZ)*2.0 + 1.0)
	if xs < 0 || ys < 0 || zs < 0 {
		return 0
	}
	xOffset := (1.0 - math.Floor(1.0/xs)*xs) / 2.0
	zOffset := (1.0 - math.Floor(1.0/zs)*zs) / 2.0
	hits, count := 0, 0
	for xx := 0.0; xx <= 1.0; xx += xs {
		for yy := 0.0; yy <= 1.0; yy += ys {
			for zz := 0.0; zz <= 1.0; zz += zs {
				px := lerp(xx, minX, maxX) + xOffset
				py := lerp(yy, minY, maxY)
				pz := lerp(zz, minZ, maxZ) + zOffset
				if t.explosionRayClear(px, py, pz, cx, cy, cz) {
					hits++
				}
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	return float32(hits) / float32(count)
}

// explosionRayClear reports whether the straight segment from (fx,fy,fz) to the blast center (cx,cy,cz) is
// unobstructed by solid blocks (a MISS clip). Reuses the ≤0.25-block segment sampler (arrowClipSegment)
// — no solid cell on the path == clear (seen). Cite level.clip(...).getType() == MISS.
func (t *TickLoop) explosionRayClear(fx, fy, fz, cx, cy, cz float64) bool {
	_, _, _, blocked := t.arrowClipSegment(fx, fy, fz, cx, cy, cz)
	return !blocked
}

// windBurstKnockbackMult is WindCharge's SimpleExplosionDamageCalculator knockbackMultiplier (1.22f). The
// wind burst does NO explosion damage (damagesEntities=false) and destroys NO terrain (TRIGGER interaction):
// its whole effect is a knockback gust scaled by this multiplier. Cite WindCharge static EXPLOSION_DAMAGE_CALCULATOR.
const windBurstKnockbackMult = 1.22

// windBurstRadius is the WindCharge.explode radius (1.2f). Cite WindCharge.explode.
const windBurstRadius = 1.2

// explodeWindBurst is the port of WindCharge.explode's Level.explode(WIND_BURST): a radius-1.2 explosion
// whose damage calculator has damagesEntities=false (NO explosion damage) and whose interaction is TRIGGER
// (NO terrain destruction) — so the entire observable effect is a knockback GUST. It reuses the exposure +
// direction math of the standard explosion but applies ONLY the knockback impulse (scaled by kbMult 1.22),
// never damage and never a block edit. The srcID (the wind charge) is excluded. Players get a SetEntityMotion;
// mobs get the impulse on their store velocity (the tracker syncs it). The flying-player kbMult=0 special
// case is CITE-DEFERRED (no player fly-ability state yet — survival players, the common case, all take 1.22).
// Cite WindCharge.explode + ServerExplosion.hurtEntities (knockback branch) + SimpleExplosionDamageCalculator.
func (t *TickLoop) explodeWindBurst(srcID int32, x, y, z float64) {
	if windBurstRadius < explosionRadiusEpsilon {
		return
	}
	doubleRadius := windBurstRadius * 2.0

	// Players.
	for _, p := range t.players {
		if p == nil || p.dead || p.playerEntity == nil {
			continue
		}
		dx, dy, dz := p.x-x, p.y-y, p.z-z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / doubleRadius
		if dist > 1.0 {
			continue
		}
		exOx, exOy, exOz := p.x, p.y+playerHeight*0.85, p.z
		exposure := t.explosionSeenPercent(x, y, z, exOx-playerWidth/2, exOy-playerHeight*0.85, exOz-playerWidth/2, playerWidth, playerHeight)
		// dir(eye - center).normalize() * (1-dist)*exposure*kbMult(1.22). No damage.
		ddx, ddy, ddz := exOx-x, exOy-y, exOz-z
		l := math.Sqrt(ddx*ddx + ddy*ddy + ddz*ddz)
		if l < 1e-9 {
			continue
		}
		power := (1.0 - dist) * float64(exposure) * windBurstKnockbackMult
		p.playerEntity.vx += ddx / l * power
		p.playerEntity.vy += ddy / l * power
		p.playerEntity.vz += ddz / l * power
		if p.client != nil {
			p.client.Send(encodeSetEntityMotion(p.playerEntity))
		}
	}

	// Mobs (store entities): the gust launches nearby mobs too. Impulse on the store velocity; the tracker
	// broadcasts the motion. srcID (the wind charge) is excluded.
	for _, e := range t.cur().entities.byID {
		if e == nil || e.id == srcID || e.dead {
			continue
		}
		dx, dy, dz := e.x-x, e.y-y, e.z-z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / doubleRadius
		if dist > 1.0 {
			continue
		}
		exposure := t.explosionSeenPercent(x, y, z, e.x-e.width/2, e.y, e.z-e.width/2, e.width, e.height)
		cx, cy, cz := e.x, e.y+float64(e.height)*0.5, e.z
		ddx, ddy, ddz := cx-x, cy-y, cz-z
		l := math.Sqrt(ddx*ddx + ddy*ddy + ddz*ddz)
		if l < 1e-9 {
			continue
		}
		power := (1.0 - dist) * float64(exposure) * windBurstKnockbackMult
		e.vx += ddx / l * power
		e.vy += ddy / l * power
		e.vz += ddz / l * power
	}
}

// lerp is Mth.lerp(t, a, b) == a + t*(b-a).
func lerp(tt, a, b float64) float64 { return a + tt*(b-a) }
