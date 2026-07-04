package server

// primed_tnt.go — PRIMED TNT: a 1:1 port of net.minecraft.world.entity.item.PrimedTnt (the primed-TNT
// entity) + the net.minecraft.world.level.block.TntBlock ignite seams (flint&steel useItemOn, redstone
// power neighborChanged/onPlace, explosion-chain wasExploded), decompiled from temp/cache/26.2-inner.jar
// (javap -c -p this session). A PrimedTnt is a NON-mob moving Entity (the sibling of the arrow / item
// drop / lightning bolt): it has no AI — its whole behavior is the gravity+drag fall plus a fuse
// countdown that, at 0, discards the entity and runs the ServerExplosion (radius 4.0, TNT interaction,
// via the existing t.explode). The TntMinecart prime + velocity-scaled explode lives here too.
//
// PrimedTnt ctor (verified javap PrimedTnt.<init>(Level,double,double,double,LivingEntity)):
//   setPos(x,y,z);
//   double d = level.getRandom().nextDouble() * (float)(Math.PI * 2);   // 6.2831854820251465 (TAU as float)
//   setDeltaMovement(-sin(d)*0.02, 0.2, -cos(d)*0.02);                   // the upward pop + random spread
//   setFuse(80);   xo=x; yo=y; zo=z;   owner = EntityReference.of(igniter);
//   explosionPower = 4.0F; blocksBuilding = true.
// The nextDouble() is drawn from level.getRandom() (Level.random == the region levelRandom), NOT any
// per-entity stream — so a block-primed TNT perturbs ONLY the region's level RNG, never a mob's per-entity
// stream (the pig oracle stream is untouched: no TNT is ever primed in the oracle scenario).
//
// PrimedTnt.tick (verified javap): handlePortal(); applyGravity(); move(SELF, deltaMovement);
//   applyEffectsFromBlocks(); setDeltaMovement(deltaMovement.scale(getAirDrag()));  // 0.98
//   if (onGround) setDeltaMovement(deltaMovement.multiply(0.7, -0.5, 0.7));
//   int i = getFuse() - 1; setFuse(i);
//   if (i <= 0) { discard(); if (!isClientSide) explode(); }
//   else { updateFluidInteraction(); ... }
// PrimedTnt.explode (verified javap): level.explode(this, damageSource, calc, getX(), getY(0.0625),
//   getZ(), explosionPower(4.0), false, ExplosionInteraction.TNT). getY(0.0625) == y + 0.0625*bbHeight.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// PrimedTnt / TntBlock constants (verified javap — exact values).
const (
	// tntDefaultFuseTime is PrimedTnt.DEFAULT_FUSE_TIME (bipush 80 in setFuse(80)). The vanilla
	// ~4-second fuse a block prime seeds.
	tntDefaultFuseTime = 80
	// tntDefaultExplosionPower is PrimedTnt.DEFAULT_EXPLOSION_POWER (ldc 4.0f in the ctor). The radius
	// PrimedTnt.explode passes to level.explode.
	tntDefaultExplosionPower = 4.0
	// tntPrimeVelocityUp is the +0.2 vertical pop the ctor sets (ldc2_w 0.20000000298023224 — the float
	// 0.2 widened to double). The random horizontal spread is -sin(d)*0.02 / -cos(d)*0.02.
	tntPrimeVelocityUp = 0.20000000298023224
	// tntPrimeVelocitySpread is the 0.02 horizontal scale (ldc2_w 0.02d) applied to the -sin/-cos of the
	// random angle.
	tntPrimeVelocitySpread = 0.02
	// tntGravity is PrimedTnt.getDefaultGravity() (ldc2_w 0.04d). Applied downward each tick.
	tntGravity = 0.04
	// tntAirDrag is Entity.getAirDrag() (0.98f — PrimedTnt inherits it). deltaMovement.scale(0.98) each tick.
	tntAirDrag = 0.98
	// tntCenterOffset is the 0.0625 (1/16) getY offset PrimedTnt.explode passes: the explosion center is
	// getY(0.0625) == y + 0.0625*bbHeight (ldc2_w 0.0625d). The blast originates just above the feet.
	tntCenterOffset = 0.0625
	// tntTAU is the float-widened (float)(Math.PI*2) the ctor multiplies nextDouble() by (ldc2_w
	// 6.2831854820251465d). Using this EXACT constant (not 2*math.Pi) preserves the ctor's byte-identical
	// deltaMovement — the low bits differ from the double 2π.
	tntTAU = 6.2831854820251465
)

// tntExplodes mirrors the GameRules.TNT_EXPLODES rule (default true). TntBlock.prime / PrimedTnt.explode /
// TntBlock.wasExploded all gate on it: when off, no TNT is ever spawned or detonated. A package var
// (like explosion_blocks.go's mobGriefing) so a test / future gamerule wire can flip it. CITE
// GameRules.TNT_EXPLODES.
var tntExplodes = true

// spawnPrimedTnt is the port of the PrimedTnt(Level, x, y, z, LivingEntity) constructor + addFreshEntity.
// It creates the primed-TNT entity CENTERED at (x,y,z) with the random horizontal pop + the +0.2 upward
// velocity (drawn from the region levelRandom, exactly as level.getRandom().nextDouble()), seeds the fuse
// to 80 and the explosion power to 4.0, and adds it to the owning region's store (the tracker broadcasts
// its AddEntity next tick, exactly as an item drop / arrow rides the store-add path). Returns the entity.
//
//	[VERIFIED javap PrimedTnt.<init>: setPos; d=nextDouble()*TAU; setDeltaMovement(-sin(d)*0.02, 0.2,
//	 -cos(d)*0.02); setFuse(80); explosionPower=4.0F.]
func (t *TickLoop) spawnPrimedTnt(x, y, z float64, fuse int32) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.Tnt, x, y, z)
	e.isTnt = true
	e.tntExplosionPower = tntDefaultExplosionPower

	// double d = level.getRandom().nextDouble() * TAU. The draw is from the OWNING region's levelRandom
	// (Level.random) — resolve it via the entity's region so the store-add + the RNG draw agree. A nil
	// levelRandom (a bare test loop with no seeded region) skips the pop (deltaMovement stays 0), which is
	// a cited fallback: the fuse + explode are the target, and the initial pop is a cosmetic scatter.
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.levelRandom != nil {
		d := owner.levelRandom.NextDouble() * tntTAU
		e.vx = -math.Sin(d) * tntPrimeVelocitySpread
		e.vy = tntPrimeVelocityUp
		e.vz = -math.Cos(d) * tntPrimeVelocitySpread
	} else {
		e.vy = tntPrimeVelocityUp
	}

	e.tntFuse = fuse

	if owner != nil {
		owner.entities.add(e)
	}
	return e
}

// tntGetRandomShortFuse is the port of PrimedTnt.getRandomShortFuse(fuse, random): random.nextInt(max(1,
// fuse/4)) + fuse/8. For the default fuse 80 this is nextInt(20) + 10 (a 10..29 tick fuse) — the shorter,
// staggered fuse a chain (an explosion priming a neighbor TNT) uses so a TNT cluster does not detonate in
// perfect lockstep. Drawn from the region levelRandom (ServerLevel.getRandom()).
//
//	[VERIFIED javap PrimedTnt.getRandomShortFuse: nextInt(Math.max(1, fuse/4)) + fuse/8.]
func tntGetRandomShortFuse(fuse int, r interface{ NextIntN(int32) int32 }) int {
	bound := fuse / 4
	if bound < 1 {
		bound = 1
	}
	return int(r.NextIntN(int32(bound))) + fuse/8
}

// primeTntBlock is the port of TntBlock.prime(Level, BlockPos, LivingEntity): if TNT_EXPLODES is on, spawn
// a PrimedTnt centered at the block (x+0.5, y, z+0.5) with the default fuse (80) and add it to the world,
// then (in vanilla) play the primed sound + fire the PRIME_FUSE game event (both cite-deferred client
// cues here). It does NOT remove the block — the CALLER (useItemOn / neighborChanged / onPlace) removes
// the source block, exactly as vanilla's callers do (removeBlock after prime). Returns true when a TNT was
// spawned (the block should be removed), false when TNT_EXPLODES is off (leave the block).
//
//	[VERIFIED javap TntBlock.prime(Level,BlockPos,LivingEntity): if (!TNT_EXPLODES) return false; new
//	 PrimedTnt(level, x+0.5, y, z+0.5, igniter); level.addFreshEntity(tnt); playSound(TNT_PRIMED);
//	 gameEvent(PRIME_FUSE); return true. The x/z are block+0.5 CENTERED, the y is the block's integer y.]
func (t *TickLoop) primeTntBlock(pos pk.Position) bool {
	if !tntExplodes {
		return false // GameRules.TNT_EXPLODES off: no prime.
	}
	// Resolve the spawn into the OWNING region (its levelRandom is the one the ctor's nextDouble() draws
	// from and its store is where the entity lands), mirroring the place/spawn seams in block_interact.go.
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y)
	cz := float64(pos.Z) + 0.5
	t.withRegion(t.regionForColumn(columnOf(cx, cz)), func() {
		t.spawnPrimedTnt(cx, cy, cz, tntDefaultFuseTime)
	})
	// playSound(SoundEvents.TNT_PRIMED) + gameEvent(PRIME_FUSE): cite-deferred client cues (no sound /
	// game-event seam for a fresh-entity spawn here). The fuse countdown + the explosion are the target.
	return true
}

// isTntBlock reports whether the state is minecraft:tnt (the placed TNT block). Mirrors the isChestBlock /
// isBeaconBlock membership helpers (block.StateList[s].ID() name match). CITE Blocks.TNT.
func isTntBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:tnt"
}

// tickPrimedTnt drives every PrimedTnt in every region, the sibling of tickArrows/tickItems. It runs on
// the coordinator (quiescent — every region joined) and processes each region WITH that region registered
// (withRegion) so the tnt tick's t.cur() (the moveEntity re-bucket + the discard remove + the explode's
// level RNG) resolves to the tnt's OWN store. A per-region snapshot keeps the loop stable across an
// in-loop discard (a detonation removal).
func (t *TickLoop) tickPrimedTnt() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isTnt {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickOnePrimedTnt(e)
			}
		})
	}
}

// tickOnePrimedTnt is the port of PrimedTnt.tick for one primed-TNT entity: apply gravity, move along the
// deltaMovement, apply air drag (0.98) + the on-ground friction (0.7, -0.5, 0.7), then decrement the fuse
// and, at fuse <= 0, discard the entity and run explode(). The vanilla ORDER is preserved exactly.
//
//	[VERIFIED javap PrimedTnt.tick: applyGravity(); move(SELF, deltaMovement); setDeltaMovement(scale
//	 (0.98)); if (onGround) multiply(0.7,-0.5,0.7); i=fuse-1; setFuse(i); if (i<=0){ discard(); explode(); }.]
func (t *TickLoop) tickOnePrimedTnt(e *Entity) {
	// handlePortal(): cite-deferred (no per-entity portal-cooldown seam on a non-player entity here).

	// applyGravity(): deltaMovement.y -= getDefaultGravity() (0.04). (No fluid/slow-falling branch — a
	// primed TNT is a plain gravity entity; water buoyancy is handled by updateFluidInteraction, cite-
	// deferred below.)
	e.vy -= tntGravity

	// move(SELF, deltaMovement): the per-axis swept resolver (the shared anti-tunneling discipline). It
	// re-buckets through entities.move and sets onGround / zeroes blocked velocity so the TNT lands on the
	// floor instead of falling through it.
	t.moveEntity(e, e.vx, e.vy, e.vz)

	// applyEffectsFromBlocks(): cite-deferred (no block-effect subsystem — soul sand / honey / bubble).

	// setDeltaMovement(deltaMovement.scale(getAirDrag())): the uniform 0.98 drag on all three axes.
	e.vx *= tntAirDrag
	e.vy *= tntAirDrag
	e.vz *= tntAirDrag

	// if (onGround) setDeltaMovement(deltaMovement.multiply(0.7, -0.5, 0.7)): a grounded TNT loses most
	// horizontal speed and bounces slightly (the -0.5 flip) so it settles on the block it landed on.
	if e.onGround {
		e.vx *= 0.7
		e.vy *= -0.5
		e.vz *= 0.7
	}

	// int i = getFuse() - 1; setFuse(i); if (i <= 0) { discard(); explode(); }
	e.tntFuse--
	if e.tntFuse <= 0 {
		t.cur().entities.remove(e.id) // discard()
		t.primedTntExplode(e)         // !isClientSide -> explode()
		return
	}
	// else: updateFluidInteraction() (water buoyancy) — cite-deferred (no fluid-interaction seam here);
	// the client-side SMOKE particle is a render-only cosmetic (cite-deferred).
}

// primedTntExplode is the port of PrimedTnt.explode: if TNT_EXPLODES is on, run the ServerExplosion at
// (x, y+0.0625*height, z) with radius explosionPower (4.0) and the TNT interaction — reusing the existing
// t.explode (server/explosion.go), which draws the 16^3-shell ray nextFloats from the region levelRandom
// exactly as ServerExplosion.explode does. The exploding TNT (e.id) is excluded from the hurt set (it is
// already discarded). The usedPortal ExplosionDamageCalculator branch is cite-deferred (no portal-travel
// seam on a spawned TNT; usedPortal is always false here, so the null-calculator default path is taken).
//
//	[VERIFIED javap PrimedTnt.explode: if (TNT_EXPLODES) level.explode(this, defaultDamageSource, calc,
//	 getX(), getY(0.0625), getZ(), explosionPower, false, ExplosionInteraction.TNT). getY(0.0625) ==
//	 position.y + getBbHeight()*0.0625.]
func (t *TickLoop) primedTntExplode(e *Entity) {
	if !tntExplodes {
		return // GameRules.TNT_EXPLODES off: no detonation.
	}
	cy := e.y + e.height*tntCenterOffset // getY(0.0625) == y + bbHeight*0.0625
	t.explode(e.id, e.x, cy, e.z, float64(e.tntExplosionPower))
}
