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
	// playerEntityTypeID is entity.Player.ID (156) — the store-entity typ of a player's server-side
	// Entity. hurtEntities' general (byID) loop skips it because a player's store entity lives in byID
	// too, and the players loop above already handled it (vanilla's single getEntities list processes a
	// player exactly once; the Go split must not push it twice). Cite data/entity Player ID 156.
	playerEntityTypeID = 156
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
	// hurtEntities applies damage + knockback and returns the per-player knockback map the
	// ClientboundExplode Optional carries. blockCount is the destroyed-block count vanilla's
	// ServerExplosion.explode() returns and ServerLevel.explode forwards as the packet field. When
	// the interaction is KEEP (mobGriefing off, below) NO block is destroyed, but vanilla still
	// returns len(toBlow) from explode() (calculateExplodedPositions ran) — blockCount is that count.
	hitPlayers := t.hurtEntitiesFromExplosion(srcID, x, y, z, radius, damageSourceOf(damageTypeExplosion))
	// interactsWithBlocks(): blockInteraction != KEEP. For a creeper (ExplosionInteraction.MOB) the
	// interaction is KEEP exactly when MOB_GRIEFING is off (ServerLevel.explode); so gate on mobGriefing
	// — when off, NO block is removed (the vanilla KEEP path). The ray nextFloats above are still drawn
	// (calculateExplodedPositions runs unconditionally in vanilla), so level.random stays in lockstep
	// regardless of the gamerule. Cite ServerExplosion.explode + interactsWithBlocks + ServerLevel.explode.
	if t.gameRule(ruleMobGriefing) {
		t.interactWithBlocks(toBlow, radius)
	}
	// A1: ServerLevel.explode tail — send ClientboundExplode to every player within 64 blocks
	// (distanceToSqr(center) < 4096.0), each carrying its own knockback Optional (hitPlayers[p], the
	// SAME vector applied to it; absent for a player not in the hurt set). blockCount == len(toBlow).
	t.sendExplodePackets(x, y, z, float32(radius), int32(len(toBlow)), hitPlayers)
}

// explosionKnockback is one player's stored knockback vector (the Vec3 the server pushed it by),
// keyed by the player's store-entity id — the ClientboundExplode Optional<Vec3> the client applies.
type explosionKnockback struct{ x, y, z float64 }

// sendExplodePackets is the ServerLevel.explode player loop: for every player whose distanceToSqr to
// the blast center is < 4096.0 (64 blocks), send one ClientboundExplode carrying that player's
// knockback Optional (present only if it is in hitPlayers). Cite ServerLevel.explode tail.
func (t *TickLoop) sendExplodePackets(cx, cy, cz float64, radius float32, blockCount int32, hitPlayers map[int32]explosionKnockback) {
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		// distanceToSqr(center) uses the player's position (feet), exactly as ServerPlayer.distanceToSqr.
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		if dx*dx+dy*dy+dz*dz >= explosionSendRadiusSqr {
			continue
		}
		kb, present := hitPlayers[p.entityID]
		p.client.Send(encodeExplode(cx, cy, cz, radius, blockCount, kb.x, kb.y, kb.z, present))
	}
}

// hurtEntitiesFromExplosion is the port of ServerExplosion.hurtEntities. Vanilla iterates
// level.getEntities(source, box) — EVERY non-ignored entity in the blast AABB — and for each one within
// radius*2: computes the exposure, applies the falloff damage to entities that accept it, and PUSHES
// EVERY entity by dir*(1-dist)*exposure*kbMult*(1-kbResist) (Entity.push == deltaMovement += impulse).
// The Go store splits players (t.players) from other entities (byID); the player's store entity ALSO
// lives in byID, so the general loop skips typ==Player to avoid double-processing (players are handled
// in the first loop, exactly as vanilla handles them once). The push is UNIVERSAL — most importantly it
// pushes primed TNT so a blast chains it (A3), and it launches items/boats/minecarts/arrows too (A2/A3).
// Returns the per-player knockback vectors (keyed by store-entity id) for the ClientboundExplode Optional.
//
// Per-entity knockback ORIGIN (bytecode offsets 224-245): a PrimedTnt uses position() (feet); EVERY
// other entity (players + mobs + items + ...) uses getEyePosition() == position + (0, eyeHeight, 0).
// The exploding entity (srcID) is excluded (vanilla's getEntities(source,...) drops the source).
// Cite ServerExplosion.hurtEntities + Entity.push + ExplosionDamageCalculator.
func (t *TickLoop) hurtEntitiesFromExplosion(srcID int32, x, y, z, radius float64, src damageSource) map[int32]explosionKnockback {
	hitPlayers := make(map[int32]explosionKnockback)
	if radius < explosionRadiusEpsilon {
		return hitPlayers
	}
	doubleRadius := radius * 2.0

	// Players. shouldDamageEntity is true (a player accepts explosion damage) and kbMult==1.0, so the
	// exposure branch always runs; the knockback vector is recorded into hitPlayers for the packet.
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
			t.applyDamage(p, src, float32(dmg))
		}
		// Knockback: dir(eye - center).normalize() * (1-dist)*exposure*kbMult*(1-kbResist). No kbResist
		// attribute on players in v1 (0). Apply to the player's store entity + send one SetEntityMotion.
		kbx, kby, kbz := t.applyExplosionKnockback(p, x, y, z, exOx, exOy, exOz, dist, float64(exposure))
		// hitPlayers: a non-spectator, non-(creative && flying) player -> its knockback Vec3 (the SAME
		// vector we pushed by), which the ClientboundExplode Optional carries. v1 has no flying-ability
		// state (survival, the common case, always records); the creative&&flying exclusion is
		// cite-deferred. Cite ServerExplosion.hurtEntities (hitPlayers.put(player, wrappedVec22)).
		if p.gameMode != gameModeSpectator {
			hitPlayers[p.entityID] = explosionKnockback{x: kbx, y: kby, z: kbz}
		}
	}

	// Every OTHER entity (mobs, primed TNT, items, boats, minecarts, arrows, ...). Vanilla's
	// getEntities(source, box) returns these uniformly; the Go split handles players above, so skip
	// typ==Player here (a player's store entity lives in byID too — skipping it prevents a double push).
	for _, e := range t.cur().entities.all() {
		if e == nil || e.id == srcID || e.dead || e.typ == playerEntityTypeID {
			continue
		}
		dx, dy, dz := e.x-x, e.y-y, e.z-z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / doubleRadius
		if dist > 1.0 {
			continue
		}
		exposure := t.explosionSeenPercent(x, y, z, e.x-e.width/2, e.y, e.z-e.width/2, e.width, e.height)
		// DAMAGE is gated to entities that accept it (a living mob). A primed TNT / item / boat / arrow
		// takes NO explosion damage in this v1 path — only the push. (Vanilla gates damage on
		// shouldDamageEntity; a living mob is the accepting case the store models.)
		if e.ai != nil {
			impact := (1.0 - dist) * float64(exposure)
			dmg := (impact*impact+impact)/2.0*explosionDamageConstant*doubleRadius + 1.0
			if dmg > 0 {
				t.applyDamageEntity(e, src, float32(dmg))
			}
		}
		// PUSH is UNIVERSAL (A2+A3). Origin: a PrimedTnt uses feet position(); everything else uses
		// getEyePosition() == position + (0, eyeHeight≈0.85*height, 0). dir = (origin-center).normalize();
		// power = (1-dist)*exposure*kbMult(1.0)*(1-kbResist=0). Entity.push adds the impulse to velocity;
		// the entity integrates it in its own tick (a chained TNT is nudged, a mob is launched).
		var origY float64
		if e.isTnt {
			origY = e.y // PrimedTnt: position() (feet)
		} else {
			origY = e.y + e.height*0.85 // getEyePosition(): position + eyeHeight (≈0.85*height)
		}
		ddx, ddy, ddz := e.x-x, origY-y, e.z-z
		nx, ny, nz := normalizeVec3(ddx, ddy, ddz) // Vec3.normalize() -> zero for a zero vector
		power := (1.0 - dist) * float64(exposure) * explosionKnockbackMult
		e.vx += nx * power
		e.vy += ny * power
		e.vz += nz * power
	}
	return hitPlayers
}

// applyExplosionKnockback pushes a player away from the blast center by the vanilla impulse and sends the
// single SetEntityMotion. dir = (eye - center).normalize(); power = (1-dist)*exposure*1.0*(1-0). Returns
// the applied (dx,dy,dz) impulse so the caller can record it for the player's ClientboundExplode Optional
// (the vanilla wrapped-vec22 that the client re-applies). Cite ServerExplosion.hurtEntities
// (entity.push(direction.scale(knockbackPower)) + hitPlayers.put(player, vec)).
func (t *TickLoop) applyExplosionKnockback(p *tickPlayer, cx, cy, cz, eyeX, eyeY, eyeZ, dist, exposure float64) (float64, float64, float64) {
	dx, dy, dz := eyeX-cx, eyeY-cy, eyeZ-cz
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if l < 1e-9 {
		return 0, 0, 0
	}
	dx, dy, dz = dx/l, dy/l, dz/l
	power := (1.0 - dist) * exposure * explosionKnockbackMult // *(1 - kbResist=0)
	ix, iy, iz := dx*power, dy*power, dz*power
	if p.playerEntity == nil {
		return ix, iy, iz
	}
	p.playerEntity.vx += ix
	p.playerEntity.vy += iy
	p.playerEntity.vz += iz
	if p.client != nil {
		p.client.Send(encodeSetEntityMotion(p.playerEntity))
	}
	return ix, iy, iz
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
	for _, e := range t.cur().entities.all() {
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
