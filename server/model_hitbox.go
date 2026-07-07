package server

// model_hitbox.go -- MODEL-M5 (G.1 + H.2.3): native per-bone hitboxes. Each rig bone carries a real
// server-side AABB (boneRuntime.aabbMin/Max), refreshed every tick from the bone's world position, so a
// melee (or, when mobs become projectile victims, projectile) hit against a modeled mob can resolve
// WHICH bone it struck and thread that bone name into the "damaged" trigger -- a server-authoritative
// headshot a Bukkit plugin structurally cannot do (their "model" is a client-side display-entity
// illusion; the server owns no per-part AABB). This is the native-advantage payoff of owning the tick.
//
// VANILLA PRECEDENT (bytecode-verified, temp/cache/26.2-inner.jar via javap -c -p this session): the
// ender dragon is vanilla's only multi-AABB entity and the exact template.
//   - net.minecraft.world.entity.boss.enderdragon.EnderDragonPart extends Entity, carries its own
//     `private final EntityDimensions size` (a per-part AABB) and returns isPickable()==true, so each
//     part participates in the world's entity hit scans as an independent box.
//   - EnderDragon.hurt(ServerLevel, EnderDragonPart, DamageSource, float) applies a PER-PART damage
//     transform before the hurt pipeline: `if (part != this.head) damage = damage/4.0f + Math.min(
//     damage, 1.0f)` (javap offsets 37-57). Per-bone damage scaling (our optional boneDecl.damageMult)
//     is therefore a vanilla pattern, not an invention.
//
// REENTRANCY / PIG-ORACLE ORACLE: every entry point here is gated on e.model != nil. A vanilla mob has
// a nil model -> zero bones -> the bone raycast returns "" -> the hit routes through the UNCHANGED
// entity-level path (handleMobAttack / applyDamageEntity byte-identical). No new RNG, no new metadata
// on a plain pig spawn. TICK-05: updateBoneAABBs runs inside tickModelRig on the owning region goroutine.

import "math"

// boneHitboxDefault is the fallback per-bone hitbox edge (width == height, in blocks) used when a bone
// declares no explicit hitbox=(w,h). A small cube so an unspecified bone is still resolvable by a hit
// but does not overlap its neighbours by default. Chosen (not a jar value -- the plugin model layer is
// NEW Sulfur surface, free of the 1:1 mandate; the vanilla precedent is only the multi-AABB PATTERN).
const boneHitboxDefault = 0.25

// updateBoneAABBs recomputes every bone's server-side AABB from the base mob's CURRENT world position +
// the bone's pivot offset, centered on that point, sized by the bone's hitbox (or boneHitboxDefault).
// Called from tickModelRig each tick (the same per-tick coordinate copy that mirrors the bone displays
// onto the base), so the per-bone boxes track the mob for free. The box is fully centered on the bone
// point (min = center - half, max = center + half) so the resolve is symmetric about the pivot.
//
// The pivot is the bone's DATA_TRANSLATION offset from the base (plugin_model_decl.go boneDecl.pivot);
// in the M2 static rig it is the bone's fixed position relative to the base, so base + pivot is the
// bone's world center. (An M3 animation offsets the DISPLAY translation for the client, but the server
// AABB stays anchored at the declared pivot -- the animator's client-side lerp does not move the
// authoritative hitbox; a future phase can drive the AABB off the sampled channel if per-frame hitbox
// motion is wanted. Documented so it slots in without a rewrite.)
func updateBoneAABBs(e *Entity) {
	m := e.model
	if m == nil {
		return
	}
	for i := range m.bones {
		br := &m.bones[i]
		if br.decl == nil {
			continue
		}
		w := br.decl.hitboxW
		h := br.decl.hitboxH
		if w <= 0 {
			w = boneHitboxDefault
		}
		if h <= 0 {
			h = boneHitboxDefault
		}
		cx := e.x + float64(br.decl.pivotX)
		cy := e.y + float64(br.decl.pivotY)
		cz := e.z + float64(br.decl.pivotZ)
		hw := float64(w) / 2.0
		hh := float64(h) / 2.0
		br.aabbMinX, br.aabbMinY, br.aabbMinZ = cx-hw, cy-hh, cz-hw
		br.aabbMaxX, br.aabbMaxY, br.aabbMaxZ = cx+hw, cy+hh, cz+hw
	}
}

// resolveHitBone tests the segment (ox,oy,oz)->(nx,ny,nz) against every bone's server AABB and returns
// the name of the CLOSEST intersected bone (nearest entry parameter along the segment) plus its
// per-bone damage multiplier, mirroring ProjectileUtil.getEntityHitResult's nearest-entry-point rule
// (segmentAABB returns the slab-method entry t; the smallest t wins) extended from one entity box to a
// mob's sub-AABBs. Returns ("", 1.0) when the mob has no model or the segment misses every bone -- the
// caller then routes an UNCHANGED entity-level hit (boneName "" fails the hit_bone condition closed).
//
// GATED on e.model != nil so a modelless mob costs one nil-check and takes the identical no-bone path
// (the pig oracle). Read-only reuse of segmentAABB (projectile.go) -- the same slab intersection the
// arrow flight uses, no new geometry primitive.
func (t *TickLoop) resolveHitBone(e *Entity, ox, oy, oz, nx, ny, nz float64) (string, float32) {
	m := e.model
	if m == nil {
		return "", 1.0
	}
	bestT := math.Inf(1)
	bestName := ""
	var bestMult float32 = 1.0
	for i := range m.bones {
		br := &m.bones[i]
		if br.decl == nil {
			continue
		}
		hit, tHit := segmentAABB(ox, oy, oz, nx, ny, nz,
			br.aabbMinX, br.aabbMinY, br.aabbMinZ, br.aabbMaxX, br.aabbMaxY, br.aabbMaxZ)
		if !hit {
			continue
		}
		if tHit < bestT {
			bestT = tHit
			bestName = br.decl.name
			bestMult = boneDamageMult(br.decl)
		}
	}
	return bestName, bestMult
}

// boneDamageMult reads a bone's damage multiplier, mapping the "unset" 0 to 1.0 (no change). A declared
// damage_mult (> 0) scales the hit that resolves to this bone -- the generic form of the EnderDragon.hurt
// per-part damage transform (the head takes full damage, other parts damage/4 + min(damage,1)).
func boneDamageMult(d *boneDecl) float32 {
	if d == nil || d.damageMult <= 0 {
		return 1.0
	}
	return d.damageMult
}

// resolveMeleeHitBone resolves which bone a player's melee swing struck on a modeled mob. Vanilla melee
// is client-picked (ServerboundAttack names the target id) and server-validated by reach overlap; the
// bone id is a strictly ADDITIONAL resolution layered on top -- the hit/no-hit observable is unchanged
// (handleMobAttack already reach-gated). It casts the attacker's eye-line (eye position along the
// yaw/pitch look vector, out to attackReach) through the mob's bone AABBs and returns the closest bone.
//
// The look vector is the standard Minecraft (yaw, pitch)->direction: with pitch p and yaw y (degrees),
// dir = (-cos(p)*sin(y), -sin(p), cos(p)*cos(y)) -- the exact form Entity.calculateViewVector /
// getLookAngle produces (verified elsewhere in the sensing/aim ports). The origin is the eye
// (p.y + playerStandingEyeHeight). Returns ("", 1.0) for a modelless mob or a swing that grazes the
// entity box but no bone (an entity-level hit).
func (t *TickLoop) resolveMeleeHitBone(p *tickPlayer, mob *Entity) (string, float32) {
	if mob.model == nil {
		return "", 1.0
	}
	ox := p.x
	oy := p.y + playerStandingEyeHeight
	oz := p.z
	yaw := float64(p.yaw) * math.Pi / 180.0
	pitch := float64(p.pitch) * math.Pi / 180.0
	cp := math.Cos(pitch)
	dx := -cp * math.Sin(yaw)
	dy := -math.Sin(pitch)
	dz := cp * math.Cos(yaw)
	nx := ox + dx*attackReach
	ny := oy + dy*attackReach
	nz := oz + dz*attackReach
	return t.resolveHitBone(mob, ox, oy, oz, nx, ny, nz)
}
