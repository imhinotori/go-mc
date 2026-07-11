package server

import "github.com/imhinotori/sulfur/data/item"

// ai_goals_creeper.go — MOB-HOST-06 (Task #9): the Creeper's SwellGoal + the fuse tick, PORTED 1:1 from
// the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session):
//
//   - net.minecraft.world.entity.ai.goal.SwellGoal: flags {MOVE}, requiresUpdateEveryTick. It reads the
//     creeper's target + distance and DRIVES setSwellDir(±1) — it does NOT itself explode. canUse arms
//     when already swelling OR a live target is within 3 blocks (dist²<9); tick disarms (-1) with no
//     target / target dead / dist²>49 (7 blocks) / no line-of-sight, else arms (+1).
//   - Creeper.tick advances swell by swellDir each tick and, at swell>=maxSwell (30), explodeCreeper()
//     (creeperAiStep below — the per-type hook, the sibling of chickenAiStep).
//
// v1 STUBS (cited): the primed-fuse SOUND + the swell client METADATA
// (DATA_SWELL_DIR/DATA_IS_POWERED/DATA_IS_IGNITED) are cite-deferred client visuals (the creeper still
// fuses + explodes with REAL damage/blocks — the gameplay — exactly as the zombie's raise-arm bit was
// deferred). The explosion math lives in explosion.go (level.explode port).

// SwellGoal / Creeper.tick constants (verified CFR).
const (
	creeperSwellArmDistSqr    = 9.0  // SwellGoal.canUse: distanceToSqr(target) < 9.0 (arm within 3 blocks)
	creeperSwellDisarmDistSqr = 49.0 // SwellGoal.tick: distanceToSqr(target) > 49.0 (disarm past 7 blocks)
	creeperMaxSwellDefault    = 30   // Creeper.maxSwell default (the 30-tick fuse)
	creeperExplosionRadius    = 3    // Creeper.explosionRadius default
)

// setCreeperSwellDir ports Creeper.setSwellDir(int): assign the swell mirror AND push the
// DATA_SWELL_DIR DataValue to every tracking player (the same SynchedEntityData broadcast the
// vanilla set(SWELL_DIR) performs). Idempotent on no change (skip when e.swellDir == value, the
// vanilla SynchedEntityData's dirty-only push); mirrors setWolfInSittingPose's idempotency. Each
// call site (SwellGoal.tick + creeperAiStep's ignited branch + explosion-driven disarm) routes
// through this helper so the swell-dir transition 0 -> 1 (arming) and 1 -> -1 (deflating) reaches
// the client.
//
//	[VERIFIED javap Creeper.setSwellDir: get(SynchedEntityData); set(DATA_SWELL_DIR, Integer.valueOf
//	 (int)).]
func (t *TickLoop) setCreeperSwellDir(e *Entity, value int32) {
	if e.swellDir == value {
		return // no change → no broadcast (matches SynchedEntityData's dirty-only push)
	}
	e.swellDir = value
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, creeperSwellDataEntry(int8(value))))
}

// swellGoal is the ported SwellGoal (net.minecraft.world.entity.ai.goal.SwellGoal). It holds the target
// snapshot the tick disarm checks (SwellGoal.target).
type swellGoal struct {
	baseGoal
	targetID int32 // SwellGoal.target (the LivingEntity captured at start)
}

func newSwellGoal() *swellGoal {
	return &swellGoal{baseGoal: newBaseGoal(flagMove)}
}

func (g *swellGoal) requiresUpdateEveryTick() bool { return true }

// canUse: creeper.getSwellDir() > 0 || (target != null && !target.isDeadOrDying() && dist²<9.0).
func (g *swellGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.swellDir > 0 {
		return true
	}
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	target := t.playerByEntityID(id)
	if target == nil || target.dead {
		return false
	}
	return distanceToSqrPlayer(target, e) < creeperSwellArmDistSqr
}

// start: navigation.stop(); target = getTarget(). Park the creeper (it swells in place). Vanilla
// does NOT call setSwellDir in start — the first tick() after engage does the +1 / -1 flip, so the
// broadcast also lands on the FIRST tick (not on start). SwellGoal.start ports this as-is.
func (g *swellGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
	g.targetID = mobTarget(e)
}

// stop: target = null. Vanilla has SwellGoal.stop clear the captured target field. The swellDir
// stays at its last value through stop (vanilla: the next setSwellDir call resets it).
func (g *swellGoal) stop(t *TickLoop, e *Entity) {
	g.targetID = 0
}

// tick: disarm (setSwellDir(-1)) with no/dead target, dist²>49, or no LoS; else arm (setSwellDir(1)).
// Each setSwellDir flips the swell mirror AND broadcasts the DATA_SWELL_DIR data-value to every
// tracker (the SynchedEntityData dirty push) — so the 0→1 (arming) + 1→-1 (deflating) transitions
// reach the client in lockstep with the server-side decision. NO RNG.
//	[VERIFIED CFR SwellGoal.tick: target==null || isDeadOrDying → creeper.setSwellDir(-1);
//	 distanceToSqr>49 → setSwellDir(-1); !hasLineOfSight → setSwellDir(-1); else setSwellDir(1).]
func (g *swellGoal) tick(t *TickLoop, e *Entity) {
	target := t.playerByEntityID(g.targetID)
	if target == nil || target.dead {
		t.setCreeperSwellDir(e, -1)
		return
	}
	if distanceToSqrPlayer(target, e) > creeperSwellDisarmDistSqr {
		t.setCreeperSwellDir(e, -1)
		return
	}
	// !getSensing().hasLineOfSight(target): the creeper disarms if it cannot see the target (a wall
	// between eye and target aborts the swell). Now a REAL per-tick-cached raycast (sensing.go), 1:1 with
	// the jar SwellGoal.tick offsets 53-78. Route through setCreeperSwellDir so the DATA_SWELL_DIR
	// metadata broadcasts to the client (idempotent — no re-broadcast when the value is unchanged).
	if !t.sensingHasLineOfSight(e, target) {
		t.setCreeperSwellDir(e, -1)
		return
	}
	t.setCreeperSwellDir(e, 1)
}

// creeperAiStep is the port of Creeper.tick's fuse advance (the per-type hook, the sibling of
// chickenAiStep). It advances swell by swellDir each tick and, at swell>=maxSwell, explodes. Called from
// tickAI for a live creeper (typ == entity.Creeper.ID), AFTER serverAiStep so the SwellGoal has set
// swellDir this tick.
//
//	[VERIFIED CFR Creeper.tick: oldSwell=swell; if(isIgnited()) setSwellDir(1); swellDir=getSwellDir();
//	 if(swellDir>0 && swell==0){ playSound(CREEPER_PRIMED); } swell += swellDir; if(swell<0) swell=0;
//	 if(swell>=maxSwell){ swell=maxSwell; explodeCreeper(); }.]
func (t *TickLoop) creeperAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.maxSwell == 0 {
		e.maxSwell = creeperMaxSwellDefault // lazy default (spawn does not set it)
	}
	e.oldSwell = e.swell
	if e.ignited {
		// isIgnited() branch: vanilla's creeper.setSwellDir(1) — re-arm the fuse on a fresh flint-and-
		// steel or external ignition. setCreeperSwellDir's idempotency skips the broadcast when the
		// dir was already +1 (the ignited creeper that's been swelling since the SwellGoal armed it).
		t.setCreeperSwellDir(e, 1)
	}
	swellDir := e.swellDir
	// Creeper.tick: if (swellDir > 0 && swell == 0) { playSound(PRIMED_FUSE); gameEvent(PRIME_FUSE); }
	// -- the fuse-start vibration (frequency 10). The PRIMED_FUSE sound is a cite-deferred client cue; the
	// gameEvent is the load-bearing vibration a warden/sculk hears. Source is the creeper itself.
	if swellDir > 0 && e.swell == 0 {
		t.gameEvent(gePrimeFuse, e.x, e.y, e.z, gameEventContext{sourceEntityID: e.id})
	}
	e.swell += swellDir
	if e.swell < 0 {
		e.swell = 0
	}
	if e.swell >= e.maxSwell {
		e.swell = e.maxSwell
		t.explodeCreeper(e)
	}
}

// explodeCreeper is the port of Creeper.explodeCreeper: mark dead, run the explosion at the creeper's
// position with radius explosionRadius × (powered ? 2 : 1), then discard the creeper.
//
//	[VERIFIED CFR Creeper.explodeCreeper: multiplier = isPowered()?2:1; dead=true;
//	 level.explode(this, x, y, z, explosionRadius*multiplier, ExplosionInteraction.MOB); discard().]
func (t *TickLoop) explodeCreeper(e *Entity) {
	multiplier := 1.0
	if e.powered {
		multiplier = 2.0
	}
	e.dead = true
	// Vanilla: post-death SwellGoal.tick's "target dead" disarm branch fires next tick, but the
	// armed-fuse visual is moot the moment dead=true. setCreeperSwellDir's idempotency skips the
	// broadcast when swellDir is already -1 (SwellGoal.tick already disarmed) — no extra wire.
	t.setCreeperSwellDir(e, -1)
	t.explode(e.id, e.x, e.y, e.z, float64(creeperExplosionRadius)*multiplier)
	t.cur().entities.remove(e.id)
}

// Creeper.mobInteract igniter sound ids (registryid/soundevent.go): FLINTANDSTEEL_USE + FIRECHARGE_USE.
const (
	creeperFlintAndSteelUseSoundID int32 = 642 // "minecraft:item.flintandsteel.use"
	creeperFireChargeUseSoundID    int32 = 626 // "minecraft:item.firecharge.use"
)

// creeperIgnite ports Creeper.ignite(): set DATA_IS_IGNITED = true (e.ignited). The next creeperAiStep reads
// it and forces swellDir = 1, so the creeper fuses to detonation regardless of its target/LoS. Cite
// Creeper.ignite.
func (e *Entity) creeperIgnite() { e.ignited = true }

// tryCreeperIgnite ports Creeper.mobInteract's CREEPER_IGNITERS branch: if the held item is in the
// CREEPER_IGNITERS tag (flint_and_steel / fire_charge), play the use sound (FIRECHARGE_USE for a fire_charge,
// else FLINTANDSTEEL_USE) at pitch nextFloat()*0.4f+0.8f drawn on the creeper's OWN random stream, ignite()
// the creeper, and consume the tool (shrink 1 for a non-damageable item like fire_charge; hurtAndBreak 1 for
// a damageable one like flint_and_steel), then return true (SUCCESS). A non-igniter held item returns false
// (super.mobInteract -> no-op). The held item is read SERVER-side (the TemptGoal precedent). The pitch draw
// is on the creeper's per-entity stream ONLY, so every non-creeper mob (the pig oracle) is unperturbed. Cite
// Creeper.mobInteract + Creeper.ignite + ItemStack.isDamageableItem/shrink/hurtAndBreak.
func (t *TickLoop) tryCreeperIgnite(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) || !itemInTag(int32(held.ItemID), "creeper_igniters") {
		return false // not an igniter -> super.mobInteract (no-op)
	}
	// SoundEvent soundEvent = itemStack.is(Items.FIRE_CHARGE) ? FIRECHARGE_USE : FLINTANDSTEEL_USE.
	soundID := creeperFlintAndSteelUseSoundID
	if int32(held.ItemID) == int32(item.FireCharge.ID) {
		soundID = creeperFireChargeUseSoundID
	}
	// playSound(soundEvent, 1.0F, random.nextFloat()*0.4F + 0.8F): the pitch draw is on the CREEPER's own
	// stream (mobRandom(mob)) so the draw ORDER is faithful and no other mob's stream is touched. The sound
	// packet is a client cue emitted via the entity-attached seam (broadcastToTrackers), so a client hears
	// the ignite; the GAMEPLAY is the ignite() + tool consume below.
	pitch := mobRandom(mob).nextFloat()*0.4 + 0.8
	t.broadcastToTrackers(mob.id, encodeSoundEntity(soundID, soundSourceHostile, mob.id, 1.0, pitch, 0))
	// !level.isClientSide branch: ignite() then damage the tool.
	mob.creeperIgnite()
	// if (!itemStack.isDamageableItem()) itemStack.shrink(1); else itemStack.hurtAndBreak(1, player, hand).
	// hurtHeldItem no-ops on an undamageable item (fire_charge), so route it explicitly: shrink for a
	// non-damageable igniter, hurtAndBreak for a damageable one (flint_and_steel). Cite Creeper.mobInteract
	// offsets 95-119.
	if !stackIsDamageableItem(held) {
		t.shrinkHeldItem(p, inv) // stack.shrink(1)
	} else {
		t.hurtHeldItem(p, inv, 1) // stack.hurtAndBreak(1, player, hand)
	}
	return true // InteractionResult.SUCCESS
}
