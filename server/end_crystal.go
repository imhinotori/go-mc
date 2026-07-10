package server

import "github.com/imhinotori/sulfur/data/entity"

// endCrystalExplosionPower is the radius EndCrystal.hurtServer passes to level.explode (ldc 6.0f).
//
//	[VERIFIED javap EndCrystal.hurtServer: level.explode(this, x, y, z, 6.0f, ExplosionInteraction.BLOCK).]
const endCrystalExplosionPower = 6.0

// end_crystal.go -- the End Crystal (net.minecraft.world.entity.boss.enderdragon.EndCrystal), a 1:1 port
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this task). The EndCrystal is the
// healing beacon of the Ender Dragon fight: it sits atop an obsidian pillar, and while the dragon's
// checkCrystals adopts it as nearestCrystal it heals the dragon +1 every 10 ticks. ANY damage to a crystal
// removes it and explodes (power 6.0f) + calls EnderDragon.onCrystalDestroyed on the fight's dragon (which
// makes the dragon take a 10.0 head hit if this was its nearestCrystal).
//
// VANILLA (verified javap EndCrystal this task):
//   EndCrystal.tick(): this.time++; ... (the beam/bob is client visual; the observable server state is the
//     ++time counter + the pickable box). isPickable() == true (it participates in hit scans).
//   EndCrystal.hurtServer(ServerLevel, DamageSource, float): if (isInvulnerableTo || src.getEntity()
//     instanceof EnderDragon) return false; if (!isRemoved()) { remove(KILLED); if (!src.is(IS_EXPLOSION))
//     { level.explode(this, x, y, z, 6.0f, ExplosionInteraction.BLOCK); } onDestroyedBy(src); } return true.
//   EndCrystal.onDestroyedBy(src): if (dragonFight != null) dragonFight.onCrystalDestroyed(this, src).
//
// The OBSERVABLE gameplay for the test: any damage to a crystal REMOVES it + calls onCrystalDestroyed on
// the fight's dragon (-> the dragon head takes 10.0 if it was nearest). The explosion (power 6.0, BLOCK
// interaction) is the AoE/block-break side effect -- deferred to the shared explosion path if a live
// explosion is wanted; the crystal removal + onCrystalDestroyed dispatch is the boss-loop-relevant half.

// spawnEndCrystal creates an EndCrystal at (x,y,z) and adds it to the owner region's store (the tracker
// broadcasts AddEntity next tick). Marked isEndCrystal + pickable; time starts at 0. NO AI (it is a plain
// Entity, not a Mob -- no goalSelector, no attribute map beyond the non-living nil). Cite EndCrystal ctor.
func (t *TickLoop) spawnEndCrystal(x, y, z float64) *Entity {
	c := NewEntity(t.idAlloc.AllocID(), entity.EndCrystal, x, y, z)
	c.isEndCrystal = true
	c.endCrystalTime = 0
	owner := t.regionForEntity(c)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(c)
	return c
}

// tickEndCrystal is the port of EndCrystal.tick() -- the per-tick counter advance, driven per-type from
// tickAI (gated on e.isEndCrystal). ++time is the only observable server state (the beam/bob rendering is
// client-side). No RNG, no movement (the crystal is stationary). Cite EndCrystal.tick.
func (t *TickLoop) tickEndCrystal(e *Entity) {
	if !e.isEndCrystal || e.dead {
		return
	}
	e.endCrystalTime++ // this.time++
}

// endCrystalHurt is the port of EndCrystal.hurtServer(ServerLevel, DamageSource, float): ANY damage from a
// non-EnderDragon source removes the crystal and (unless the source is itself an explosion) explodes at
// power 6.0, then calls onDestroyedBy -> EnderDragonFight.onCrystalDestroyed. Here it removes the crystal
// from the store, then dispatches dragonOnCrystalDestroyed to EVERY live dragon in the region (v1 has no
// EnderDragonFight object holding a single dragon ref -- the dragon's own nearestCrystal check inside
// dragonOnCrystalDestroyed gates whether it actually takes the 10.0 head hit, which is the faithful
// observable). Returns whether the hit landed (false if the crystal was already removed or the source is
// the dragon itself).
//
//	[VERIFIED javap EndCrystal.hurtServer:
//	 if (isInvulnerableTo(src) || src.getEntity() instanceof EnderDragon) return false;
//	 if (!isRemoved()) { remove(KILLED);
//	   if (!src.is(IS_EXPLOSION)) level.explode(this, x, y, z, 6.0f, BLOCK);
//	   onDestroyedBy(src); }
//	 return true;]
//
// The `src.getEntity() instanceof EnderDragon` guard (a crystal is immune to the dragon's own damage) is
// modeled by checking whether the source attacker is a live EnderDragon. The explosion (power 6.0, BLOCK)
// is the AoE/block-break side effect, now wired to the shared explosion path (t.explode power 6.0); the
// crystal removal + onCrystalDestroyed dispatch is the boss-loop half the test exercises.
func (t *TickLoop) endCrystalHurt(e *Entity, src damageSource) bool {
	if !e.isEndCrystal || e.dead {
		return false // !isRemoved() guard: an already-removed crystal takes no hit.
	}
	// src.getEntity() instanceof EnderDragon -> the crystal is immune to the dragon's own damage.
	if src.attacker != 0 {
		owner := t.regionForEntity(e)
		if a, ok := owner.entities.get(src.attacker); ok && a != nil && a.dragon != nil {
			return false
		}
	}
	// remove(KILLED): the crystal is destroyed. Mark dead + remove from the store (the tracker batches the
	// RemoveEntities next tick). Capture the position BEFORE removal for the explosion center.
	crystalID := e.id
	cx, cy, cz := e.x, e.y, e.z
	e.dead = true
	t.regionForEntity(e).entities.remove(crystalID)

	// if (!src.is(IS_EXPLOSION)) level.explode(this, x, y, z, 6.0f, BLOCK): a crystal destroyed by anything
	// OTHER than an explosion detonates at power 6.0 (the classic crystal blast that chains a pillar of
	// crystals and cracks the surrounding obsidian). A crystal killed by another explosion does NOT re-explode
	// (the IS_EXPLOSION guard prevents an infinite blast chain). The already-removed crystal (crystalID) is
	// excluded from the blast's own hurt set. CITE EndCrystal.hurtServer (level.explode power 6.0, BLOCK).
	if !src.is("is_explosion") {
		t.explode(crystalID, cx, cy, cz, endCrystalExplosionPower)
	}

	// onDestroyedBy -> EnderDragonFight.onCrystalDestroyed(this, src): dispatch to every live dragon in the
	// region. Each dragon's dragonOnCrystalDestroyed gates on crystal == nearestCrystal, so only the dragon
	// that was healing off THIS crystal takes the 10.0 head hit (the faithful single-dragon-fight outcome).
	owner := t.regionForEntity(e)
	for _, other := range owner.entities.all() {
		if other != nil && other.dragon != nil && !other.dead {
			t.dragonOnCrystalDestroyed(other, crystalID, src)
		}
	}
	return true
}
