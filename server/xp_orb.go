package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// xp_orb.go — WR-06 (XP-ORB PICKUP): the experience-orb lifecycle that makes a spawned orb actually
// FOLLOW a nearby player and get COLLECTED (the reported "orbs spawn but can't be picked up" bug). The
// orbs were spawned by awardExperienceOrbs (death_mob.go) but NEVER ticked, so they just sat on the
// ground. This mirrors the working dropped-item subsystem (item_entity.go) for the orb.
//
// Three ported vanilla surfaces, all decompiled from temp/cache/26.2-inner.jar this session and cited
// at each call site:
//
//	net.minecraft.world.entity.ExperienceOrb.tick()              -> tickOrbs / tickOrb
//	ExperienceOrb.followNearbyPlayer()                            -> followNearbyPlayerOrb (the homing)
//	ExperienceOrb.playerTouch(Player) + Player.giveExperiencePoints -> scanOrbPickup / playerTouchOrb
//
// SINGLE-OWNER (TICK-05): every function here runs on the tick goroutine over the tick-owned
// entityStore / tickPlayer state, exactly like tickItems. The orb tick mutates velocity/age and moves
// the orb via t.moveEntity (the bucket-consistent path); the pickup scan mutates the player's XP and
// removes the orb from the store. No goroutine, no xsync/ants — pure owner work.

const (
	// orbLifetime is ExperienceOrb.LIFETIME analogue: the orb DESPAWNS once its age reaches 6000 ticks
	// (5 minutes), identical to the dropped-item LIFETIME. The orb tick discards at age >= 6000.
	//   [VERIFIED javap ExperienceOrb.tick: ++age; if (age >= 6000) discard().]
	orbLifetime = 6000

	// orbFollowRange is the 8.0-block radius ExperienceOrb.followNearbyPlayer passes to
	// Level.getNearestPlayer(this, 8.0): the orb only homes toward a player within this distance.
	//   [VERIFIED javap ExperienceOrb.followNearbyPlayer: ldc2_w 8.0d; getNearestPlayer(this, 8.0).]
	orbFollowRange = 8.0

	// orbFollowKeepRangeSqr is the 64.0 (== 8²) distanceToSqr cap that KEEPS the currently-followed
	// player: followNearbyPlayer drops the follow target once it is farther than this and re-scans.
	//   [VERIFIED javap ExperienceOrb.followNearbyPlayer: distanceToSqr(followingPlayer) > 64.0 -> re-scan.]
	orbFollowKeepRangeSqr = 64.0

	// orbFollowScale is the 0.1 factor on the squared closeness in the homing impulse:
	// deltaMovement += to.normalize().scale(scale*scale*0.1), where scale = 1 - sqrt(d)/8.
	//   [VERIFIED javap ExperienceOrb.followNearbyPlayer: ... scale*scale ; ldc2_w 0.1d ; dmul ; scale().]
	orbFollowScale = 0.1

	// orbTakeXpDelay is the value ExperienceOrb.playerTouch writes to Player.takeXpDelay on a successful
	// pickup (the 2-tick cooldown before the next orb can be absorbed).
	//   [VERIFIED javap ExperienceOrb.playerTouch: player.takeXpDelay = 2.]
	orbTakeXpDelay = 2

	// orbGravity is ExperienceOrb's applyGravity acceleration. ExperienceOrb.getDefaultGravity() == 0.03
	// (HALF a living entity's 0.06... but the orb-specific value), applied per tick while not colliding.
	//   [VERIFIED javap ExperienceOrb.getDefaultGravity: ldc2_w 0.03d; dreturn.]
	orbGravity = 0.03

	// orbAirDrag is ExperienceOrb.getAirDrag() == 0.98 — the per-tick velocity scale (drag) the orb
	// tick applies to ALL THREE axes (deltaMovement = deltaMovement.scale(0.98f)).
	//   [VERIFIED javap ExperienceOrb.getAirDrag: ldc_w 0.98f; freturn.]
	orbAirDrag = 0.98

	// orbPickupInflateXZ / orbPickupInflateY: the player's XP-collection reach. Vanilla collects an orb
	// whose box touches the player's collision box (Player.touch via the entity-collision AABB). v1
	// reuses the dropped-item pickup inflation (1.0, 0.5, 1.0) — the same proximity the item pickup uses,
	// which comfortably covers the orb-suck range (the orb homes to within touching distance first).
	orbPickupInflateXZ = 1.0
	orbPickupInflateY  = 0.5

	// orbUnderwaterXZDrag is ExperienceOrb.setUnderwaterMovement's per-tick horizontal scale: vx,vz *=
	// 0.99f (stored as the double 0.9900000095367432). Buoyancy replaces gravity while the orb's eye is
	// submerged in water.
	//   [VERIFIED javap ExperienceOrb.setUnderwaterMovement: getfield x; ldc2_w 0.9900000095367432; dmul.]
	orbUnderwaterXZDrag = 0.9900000095367432

	// orbUnderwaterYRise is the vy increment while underwater: vy + 0.0005f (the double
	// 5.000000237487257E-4) — a gentle upward drift so a submerged orb floats up.
	//   [VERIFIED javap ExperienceOrb.setUnderwaterMovement: getfield y; ldc2_w 5.000000237487257E-4; dadd.]
	orbUnderwaterYRise = 5.000000237487257e-4

	// orbUnderwaterYCap is the min() ceiling on the risen vy: 0.06f (the double 0.05999999865889549) — the
	// buoyant rise is clamped so the orb bobs at the surface instead of shooting up.
	//   [VERIFIED javap ExperienceOrb.setUnderwaterMovement: ...dadd; ldc2_w 0.05999999865889549; Math.min.]
	orbUnderwaterYCap = 0.05999999865889549

	// orbLavaPopXZScale is the 0.2f factor on (nextFloat()-nextFloat()) for the x/z components of the
	// lava-pop impulse: an orb sitting in lava is kicked sideways+up each tick (getFluidState.is(LAVA)).
	//   [VERIFIED javap ExperienceOrb.tick: (random.nextFloat()-random.nextFloat())*0.2f -> x and z.]
	orbLavaPopXZScale = 0.2

	// orbLavaPopY is the fixed upward vy of the lava-pop impulse: 0.2f (the double 0.20000000298023224).
	//   [VERIFIED javap ExperienceOrb.tick: ldc2_w 0.20000000298023224 -> the y arg of setDeltaMovement.]
	orbLavaPopY = 0.20000000298023224

	// orbGroundFrictionMul is the extra factor the air drag is multiplied by on the ground: the block's
	// friction (Block.getFriction default 0.6) TIMES 0.98. Vanilla: f = getAirDrag(); if (onGround()) f *=
	// blockBelow.getFriction() * 0.98f. v1 has no per-block friction table, so the DEFAULT 0.6 stands in
	// (stone/dirt/grass — where an orb rests — all carry 0.6). Structured to become a per-block read later.
	//   [VERIFIED javap ExperienceOrb.tick: getAirDrag; onGround? fmul getFriction; ldc 0.98f... — wait,
	//    the bytecode does f *= getFriction() only (the 0.98 is getAirDrag's own value); see tickOrb.]
	orbGroundFriction = 0.6

	// orbBounceScale is the -0.4 restitution on the bounce: when the orb hits the ground moving faster
	// than -gravity downward, its vertical velocity flips to -vy0*0.4f (a small bounce), matching vanilla.
	//   [VERIFIED javap ExperienceOrb.tick: verticalCollisionBelow && vy0 < -getGravity() -> vec3(vx,
	//    -vy0*0.4, vz); ldc2_w 0.4.]
	orbBounceScale = 0.4

	// orbMergeInflate is the 0.5 the merge scan inflates the orb's box by (getBoundingBox().inflate(0.5))
	// when searching for a nearby equal-value orb to combine. The scan runs on tickCount%20==1.
	//   [VERIFIED javap ExperienceOrb.scanForMerges: getBoundingBox; ldc2_w 0.5; AABB.inflate.]
	orbMergeInflate = 0.5

	// orbMergeScanPeriod is the tickCount interval on which scanForMerges runs: (tickCount % 20 == 1).
	//   [VERIFIED javap ExperienceOrb.tick: getfield tickCount; bipush 20; irem; iconst_1; if_icmpne.]
	orbMergeScanPeriod = 20
)

// tickOrbs is the WR-06 per-tick XP-orb pass: it steps every XP orb in each region's tick-owned store
// (gravity + homing + move + age + despawn) and then scans each player for nearby collectible orbs. It
// is wired into tickEntities right after tickItems (the sibling item pass), so no new tick phase is
// added (TestTickPhaseOrder stays green). Runs on the tick goroutine.
//
// Ordering mirrors tickItems: orbs are TICKED first (so they home toward the player this tick), THEN the
// pickup scan runs — matching vanilla, where ExperienceOrb.tick (which calls followNearbyPlayer) runs in
// the entity tick BEFORE Player.touch collects orbs in the same server tick.
func (t *TickLoop) tickOrbs() {
	// Phase-27 N=2: orbs live across regions; tickOrbs runs on the COORDINATOR (quiescent). Process each
	// region's orbs WITH that region registered (withRegion) so tickOrb's t.cur()/t.only() (the moveEntity
	// re-bucket + the despawn remove) resolves to the orb's OWN store, exactly like tickItems. The snapshot
	// per region keeps the loop stable across an in-loop discard.
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isOrb {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickOrb(e)
			}
		})
	}

	// Pickup scan AFTER the orb step (vanilla: ExperienceOrb.tick precedes Player.touch).
	for _, p := range t.players {
		if p == nil || p.dead {
			continue // a dead player (death screen) collects nothing
		}
		t.scanOrbPickup(p)
	}
}

// tickOrb ports the load-bearing body of ExperienceOrb.tick() (B-A8, 1:1): buoyancy vs gravity
// (setUnderwaterMovement while the eye is in water, else applyGravity 0.03), the lava pop, the
// every-20-tick equal-value merge (scanForMerges), the homing toward a nearby player
// (followNearbyPlayer), the swept move, the ground-friction-scaled air drag, the ground bounce, and
// age++ -> DESPAWN at LIFETIME (6000). Tick-owned.
//
// Vanilla bytecode (javap ExperienceOrb.tick, the parts that change observable state):
//
//	super.tick();
//	boolean flag = !level.noCollision(getBoundingBox());        // colliding with a block?
//	if (isEyeInFluid(WATER)) setUnderwaterMovement();
//	else if (!flag) applyGravity();                             // gravity while not stuck in a block
//	if (getFluidState(blockPosition()).is(LAVA))                // lava pop
//	if (tickCount % 20 == 1) scanForMerges();                   // merge equal-value orbs
//	followNearbyPlayer();                                       // the homing
//	move(SELF, getDeltaMovement()); f = getAirDrag(); scale by f (x block friction on ground); bounce.
//	++age; if (age >= 6000) discard();
func (t *TickLoop) tickOrb(e *Entity) {
	// isEyeInFluid(WATER) chooses setUnderwaterMovement (buoyancy) over applyGravity while the orb eye is
	// submerged in water: horizontal 0.99 drag plus a clamped upward drift, instead of the 0.03 pull. The
	// vanilla else-if (!flag) gate (skip gravity while stuck in a block) is handled observably by the
	// swept resolver zeroing vy on landing, so a resting orb still settles; open-air is unaffected.
	if t.orbEyeInWater(e) {
		// setUnderwaterMovement: (vx*0.99, min(vy+0.0005, 0.06), vz*0.99).
		e.vx *= orbUnderwaterXZDrag
		e.vy = math.Min(e.vy+orbUnderwaterYRise, orbUnderwaterYCap)
		e.vz *= orbUnderwaterXZDrag
	} else {
		// applyGravity (ExperienceOrb.getDefaultGravity()==0.03): accelerate downward.
		e.vy -= orbGravity
	}

	// lava pop: an orb in a lava cell (getFluidState(blockPosition()).is(LAVA)) is kicked with a random
	// horizontal plus fixed 0.2 upward impulse each tick, so it hops out. The two (nextFloat-nextFloat)
	// draws come off the orb OWN RandomSource in x-then-z order (the exact vanilla draw order); only drawn
	// on a lava cell, so a normal orb perturbs no stream.
	if t.fluidAt(pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(e.y)), Z: int(math.Floor(e.z))}).isLava {
		r := t.orbRandom(e)
		e.vx = float64((r.nextFloat() - r.nextFloat()) * orbLavaPopXZScale)
		e.vy = orbLavaPopY
		e.vz = float64((r.nextFloat() - r.nextFloat()) * orbLavaPopXZScale)
	}

	// scanForMerges every 20 ticks (tickCount % 20 == 1): combine a nearby equal-value orb into this one.
	// The orb has no separate tickCount field; age increments once per tick from spawn exactly like
	// tickCount, so it drives the same 1-in-20 cadence.
	if e.age%orbMergeScanPeriod == 1 {
		t.scanForMergesOrb(e)
	}

	// followNearbyPlayer: set/clear the follow target and add the homing impulse toward the player.
	t.followNearbyPlayerOrb(e)

	// vy0 = getDeltaMovement().y BEFORE the move: the bounce test compares the pre-move downward speed
	// against -getGravity().
	vy0 := e.vy

	// move(SELF, getDeltaMovement()) via the per-axis swept resolver (shared with tickItem/tickPhysics):
	// re-buckets through entities.move, sets onGround, and zeroes blocked velocity so the orb lands on the
	// floor and is blocked by walls.
	t.moveEntity(e, e.vx, e.vy, e.vz)

	// f = getAirDrag() (0.98); if (onGround()) f *= blockBelow.getFriction() * 0.98f. The block friction
	// default is 0.6 (Block.getFriction: v1 has no per-block table yet; stone/dirt/grass all carry 0.6).
	f := orbAirDrag
	if e.onGround {
		f = orbAirDrag * orbGroundFriction * 0.98
	}
	// setDeltaMovement(getDeltaMovement().scale(f)) on all three axes.
	e.vx *= f
	e.vy *= f
	e.vz *= f

	// bounce: if the orb hit the ground this tick (verticalCollisionBelow == onGround here) moving faster
	// than -getGravity() downward, flip the vertical velocity to -vy0*0.4 (a small bounce). getGravity for
	// the orb is orbGravity (0.03).
	if e.onGround && vy0 < -orbGravity {
		e.vy = -vy0 * orbBounceScale
	}

	// age++ then DESPAWN at LIFETIME. Removing the orb from the store makes the tracker emit RemoveEntities
	// to every tracking player next tick (it no longer appears in near()).
	e.age++
	if e.age >= orbLifetime {
		t.cur().entities.remove(e.id)
	}
}

// orbEyeInWater ports Entity.isEyeInFluid(FluidTags.WATER) for the orb: sample water at the block
// containing getEyeY() (== y + height*0.85, the default non-living eye height). A nil world (test loop)
// reads as not-in-water. Mirrors mobEyeInWater (breath_mob.go) exactly.
func (t *TickLoop) orbEyeInWater(e *Entity) bool {
	if t.world() == nil {
		return false
	}
	eyeY := e.y + float64(e.height)*0.85
	return t.fluidAt(pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(eyeY)), Z: int(math.Floor(e.z))}).isWater
}

// orbRandom lazily initializes and returns the orb dedicated RandomSource (ExperienceOrb inherits
// Entity.random), seeded from the orb entity id (mirroring fishingRNG: never a mob stream). Drawn only
// by the lava pop, so a normal orb never touches it. Tick-owned.
func (t *TickLoop) orbRandom(e *Entity) *entityRandom {
	if e.orbRNG == nil {
		e.orbRNG = newEntityRandom(uint64(e.id))
	}
	return e.orbRNG
}

// scanForMergesOrb ports ExperienceOrb.scanForMerges(): scan every OTHER ExperienceOrb whose box
// overlaps this orb box inflated by 0.5, and merge each mergeable one into this orb. Runs on a
// ServerLevel every 20 ticks. Tick-owned.
//
//	[VERIFIED javap ExperienceOrb.scanForMerges: getEntities(ExperienceOrb, getBoundingBox().inflate(0.5),
//	 this canMerge predicate) -> for each -> merge(other).]
func (t *TickLoop) scanForMergesOrb(e *Entity) {
	// getBoundingBox().inflate(0.5): the orb box (feet-anchored width x height on x/z) grown 0.5 on every
	// face. Candidates come from the region near-list (the v1 getEntities analogue), narrow-phased against
	// this inflated box.
	hw := float64(e.width)/2 + orbMergeInflate
	loX, hiX := e.x-hw, e.x+hw
	loY, hiY := e.y-orbMergeInflate, e.y+float64(e.height)+orbMergeInflate
	loZ, hiZ := e.z-hw, e.z+hw

	for _, o := range t.entitiesNearAcrossRegions(e.x, e.z, pickupMergeScanChunks) {
		if o == e || !o.isOrb {
			continue // getEntities(ExperienceOrb, ...): orbs only, never self
		}
		if !canMergeOrb(e, o) {
			continue // canMerge: same value (the id%40 gate is a v1 no-op, see canMergeOrb)
		}
		ohw := float64(o.width) / 2
		if hiX <= o.x-ohw || o.x+ohw <= loX ||
			hiY <= o.y || o.y+float64(o.height) <= loY ||
			hiZ <= o.z-ohw || o.z+ohw <= loZ {
			continue // boxes do not overlap on some axis
		}
		t.mergeOrb(e, o)
	}
}

// canMergeOrb ports ExperienceOrb.canMerge(other) -> canMerge(other, getId(), getValue()): the other orb
// is mergeable when it is NOT this orb and carries the SAME value. Vanilla ALSO gates on
// (other.getId() - id) % 40 == 0 (a hash spread throttling merge storms): that depends on the exact
// vanilla entity-id assignment, which v1 does not reproduce, so it is a CITED no-op here (v1 merges any
// same-value orb in range; the observable end state, same-value orbs combine and count sums, is
// identical, only the tick-spread differs, not observable to a client). isRemoved is handled by the store.
//
//	[VERIFIED javap ExperienceOrb.canMerge(other): other != this && canMerge(other, getId(), getValue());
//	 canMerge(orb,id,value): !orb.isRemoved() && (orb.getId()-id)%40==0 && orb.getValue()==value.]
func canMergeOrb(e, o *Entity) bool {
	return o != e && o.isOrb && o.xpValue == e.xpValue
}

// mergeOrb ports ExperienceOrb.merge(other): absorb the other orb count into this one, keep the SMALLER
// age (Math.min, so the combined orb despawns no sooner than the younger of the two), and discard the
// other orb. Tick-owned.
//
//	[VERIFIED javap ExperienceOrb.merge: count += other.count; age = Math.min(age, other.age);
//	 other.discard().]
func (t *TickLoop) mergeOrb(e, o *Entity) {
	e.orbCount += o.orbCount
	if o.age < e.age {
		e.age = o.age // Math.min(age, other.age)
	}
	if owner := t.owningRegion(o.id); owner != nil {
		owner.entities.remove(o.id)
	}
}

// followNearbyPlayerOrb ports ExperienceOrb.followNearbyPlayer() (WR-06, the homing): keep the current
// follow target while it is within 8 blocks (distanceToSqr <= 64), otherwise re-scan for the nearest
// player within 8 blocks (not spectator, not dead). When following, add a velocity impulse toward the
// player's mid-body point (player.y + eyeHeight/2), scaled by (1 - sqrt(d)/8)² * 0.1. Tick-owned.
//
// Vanilla (javap ExperienceOrb.followNearbyPlayer):
//
//	if (followingPlayer != null && (followingPlayer.isSpectator() || distanceToSqr(followingPlayer) > 64.0))
//	    followingPlayer = null;                                    // drop a too-far / spectator target
//	if (followingPlayer == null) {                                 // (re-)scan
//	    Player p = level.getNearestPlayer(this, 8.0);
//	    followingPlayer = (p != null && !p.isSpectator() && !p.isDeadOrDying()) ? p : null;
//	}
//	if (followingPlayer != null) {
//	    Vec3 to = new Vec3(player.getX() - getX(),
//	                       player.getY() + player.getEyeHeight()/2.0 - getY(),
//	                       player.getZ() - getZ());
//	    double d = to.lengthSqr();
//	    double scale = 1.0 - Math.sqrt(d) / 8.0;
//	    setDeltaMovement(getDeltaMovement().add(to.normalize().scale(scale * scale * 0.1)));
//	}
func (t *TickLoop) followNearbyPlayerOrb(e *Entity) {
	// Keep the current follow target only if it is still in range (<= 64 distanceToSqr) and not a
	// spectator. A nil/departed/out-of-range target is dropped so the re-scan below picks a new one.
	if e.followingPlayerID != 0 {
		p := t.playerByEntityID(e.followingPlayerID)
		if p == nil || distanceToSqrPlayer(p, e) > orbFollowKeepRangeSqr {
			e.followingPlayerID = 0
		}
	}

	// (Re-)scan for the nearest player within 8 blocks (not dead). getNearestPlayer's NO_SPECTATORS +
	// not-dead predicate is honored via nearestPlayerEntityWithin (v1 has no spectator state, so the
	// spectator half is a cited no-op; the not-dead half is real — a dead player collects no orbs).
	if e.followingPlayerID == 0 {
		if p := t.nearestPlayerEntityWithin(e, orbFollowRange); p != nil && !p.dead {
			e.followingPlayerID = p.entityID
		}
	}

	// If following, add the homing impulse toward the player's mid-body point.
	if e.followingPlayerID == 0 {
		return
	}
	p := t.playerByEntityID(e.followingPlayerID)
	if p == nil {
		return // target departed between the keep-check and here: no impulse this tick
	}

	// to = player mid-body - orb. The vanilla y target is player.getY() + getEyeHeight()/2.0 (the orb
	// sucks toward the player's chest, not the feet). playerStandingEyeHeight (1.62) is the standing eye.
	tox := p.x - e.x
	toy := p.y + playerStandingEyeHeight/2.0 - e.y
	toz := p.z - e.z
	d := tox*tox + toy*toy + toz*toz
	if d <= 0 {
		return // the orb is exactly on the player's mid-body: normalize is undefined; no impulse
	}

	// scale = 1 - sqrt(d)/8; the impulse is to.normalize().scale(scale² * 0.1). The closer the orb, the
	// larger scale (toward 1 at d=0), so the orb accelerates harder as it nears the player.
	scale := 1.0 - math.Sqrt(d)/orbFollowRange
	length := math.Sqrt(d)
	imp := scale * scale * orbFollowScale
	e.vx += tox / length * imp
	e.vy += toy / length * imp
	e.vz += toz / length * imp
}

// scanOrbPickup ports Player.touch's XP-orb-collection path (WR-06): for every XP orb whose box is
// within the player's pickup reach, attempt the vanilla playerTouch — collect it (award XP + the
// orb-suck animation) if the player's takeXpDelay is 0. Tick-owned. Mirrors scanItemPickup.
func (t *TickLoop) scanOrbPickup(p *tickPlayer) {
	// Player pickup AABB: the player collision box inflated by (1.0, 0.5, 1.0), exactly the item pickup
	// reach (the orb homes to within touching distance first, so this proximity collects it).
	hw := playerWidth/2 + orbPickupInflateXZ
	pLoX, pHiX := p.x-hw, p.x+hw
	pLoY, pHiY := p.y-orbPickupInflateY, p.y+playerHeight+orbPickupInflateY
	pLoZ, pHiZ := p.z-hw, p.z+hw

	for _, e := range t.entitiesNearAcrossRegions(p.x, p.z, pickupMergeScanChunks) {
		if !e.isOrb {
			continue // only XP orbs are collectible here
		}

		// Orb AABB (feet-anchored, width × height centered on x/z). Narrow-phase intersection on all
		// three axes.
		ihw := e.width / 2
		if pHiX <= e.x-ihw || e.x+ihw <= pLoX ||
			pHiY <= e.y || e.y+e.height <= pLoY ||
			pHiZ <= e.z-ihw || e.z+ihw <= pLoZ {
			continue // boxes do not overlap on some axis: not in pickup range
		}

		t.playerTouchOrb(p, e)
	}
}

// playerTouchOrb ports ExperienceOrb.playerTouch(Player) (WR-06): if the player's takeXpDelay is 0,
// arm it to 2, play the orb-suck animation (player.take -> ClientboundTakeItemEntity, reusing
// encodeTakeItemEntity), award the orb's value to the player (giveExperiencePoints), then discard the
// orb. The mending repair (repairPlayerItems, E-3) consumes the value first; only the remainder is
// awarded. Tick-owned.
//
// Vanilla (javap ExperienceOrb.playerTouch, server side):
//
//	if (!(player instanceof ServerPlayer sp)) return;
//	if (player.takeXpDelay == 0) {
//	    player.takeXpDelay = 2;
//	    player.take(this, 1);                                 // ClientboundTakeItemEntity (the orb suck)
//	    int remaining = repairPlayerItems(sp, getValue());    // mending — v1 SKIP, remaining = getValue()
//	    if (remaining > 0) player.giveExperiencePoints(remaining);
//	    --count; if (count == 0) discard();
//	}
func (t *TickLoop) playerTouchOrb(p *tickPlayer, e *Entity) {
	if p.takeXpDelay != 0 {
		return // the per-player XP cooldown is still running: not yet collectible
	}
	p.takeXpDelay = orbTakeXpDelay

	// player.take(this, 1): the "orb flies into the player" animation, broadcast to every player tracking
	// the orb (reuses encodeTakeItemEntity / the takeItem fan-out — the orb suck and the item suck share
	// the same ClientboundTakeItemEntity packet). count is always 1 for an orb take.
	t.takeItem(p, e, 1)

	// int remaining = repairPlayerItems(sp, getValue()) (E-3): the MENDING repair — a random damaged
	// equipped item carrying a repair_with_xp enchant converts the orb value to durability (x2 via
	// the multiply-2.0 effect), recursing with the remainder; only what is left over lands on the XP
	// bar. A player with no mending gear returns the full value (and draws NO RNG) — the pre-E-3 path.
	remaining := t.repairPlayerItems(p, e.xpValue)
	if remaining > 0 {
		t.giveExperiencePoints(p, remaining)
	}

	// --count; if (count == 0) discard(). A merged orb carries orbCount logical sub-orbs; each touch
	// awards getValue() (via the mending+giveExperiencePoints above) and consumes ONE sub-orb, so a
	// count-3 orb is collected over 3 touches (gated by takeXpDelay). Only when count hits 0 is the entity
	// removed. Remove from the orb OWNING region (scanOrbPickup ran cross-region); a nil owner is a no-op.
	e.orbCount--
	if e.orbCount <= 0 {
		if owner := t.owningRegion(e.id); owner != nil {
			owner.entities.remove(e.id)
		}
	}
}

// giveExperiencePoints is the port of net.minecraft.world.entity.player.Player.giveExperiencePoints(int):
// add the points to the XP bar (experienceProgress fraction + totalExperience), rolling levels up as the
// progress crosses 1.0, then push the authoritative ClientboundSetExperience to the player's client.
// Tick-owned.
//
// Vanilla (javap Player.giveExperiencePoints):
//
//	increaseScore(points);                                                  // scoreboard — v1 stub
//	experienceProgress += (float) points / (float) getXpNeededForNextLevel();
//	totalExperience = Mth.clamp(totalExperience + points, 0, Integer.MAX_VALUE);
//	while (experienceProgress < 0.0F) { ... giveExperienceLevels(-1) ... }  // (only on XP LOSS — v1 gains)
//	while (experienceProgress >= 1.0F) {
//	    float f = (experienceProgress - 1.0F) * getXpNeededForNextLevel();
//	    giveExperienceLevels(1);                                            // level++ (clamped >= 0)
//	    experienceProgress = f / getXpNeededForNextLevel();
//	}
func (t *TickLoop) giveExperiencePoints(p *tickPlayer, points int) {
	// increaseScore(points): the "xp" scoreboard criterion — a v1 stub (no scoreboard subsystem).

	// experienceProgress += points / getXpNeededForNextLevel() (the float division at the CURRENT level).
	p.experienceProgress += float32(points) / float32(getXpNeededForNextLevel(p.experienceLevel))

	// totalExperience = clamp(totalExperience + points, 0, MaxInt32).
	total := int64(p.totalExperience) + int64(points)
	if total < 0 {
		total = 0
	}
	if total > math.MaxInt32 {
		total = math.MaxInt32
	}
	p.totalExperience = int32(total)

	// while (experienceProgress >= 1.0): roll a level and carry the remainder into the next level's
	// fraction. giveExperienceLevels(1) increments experienceLevel (clamped >= 0). v1 only GAINS XP from
	// orbs, so the `< 0.0` underflow loop (XP loss) is not reached — it is omitted as a cited no-op path.
	for p.experienceProgress >= 1.0 {
		f := (p.experienceProgress - 1.0) * float32(getXpNeededForNextLevel(p.experienceLevel))
		p.experienceLevel++ // giveExperienceLevels(1): saturatedAdd then clamp >= 0 (always positive here)
		p.experienceProgress = f / float32(getXpNeededForNextLevel(p.experienceLevel))
	}

	// Push the authoritative XP bar to the client (ServerPlayer sends ClientboundSetExperience when the
	// XP changes). Send only on a real change so a no-op give does not spam the packet.
	t.sendExperience(p)
}

// getXpNeededForNextLevel is the port of Player.getXpNeededForNextLevel(): the XP cost of the level the
// player is currently on (drives the experienceProgress denominator).
//
//	[VERIFIED javap Player.getXpNeededForNextLevel: level>=30 -> 112 + (level-30)*9; level>=15 ->
//	 37 + (level-15)*5; else 7 + level*2.]
func getXpNeededForNextLevel(level int32) int32 {
	switch {
	case level >= 30:
		return 112 + (level-30)*9
	case level >= 15:
		return 37 + (level-15)*5
	default:
		return 7 + level*2
	}
}

// sendExperience pushes ClientboundSetExperience to the player's client when the XP triple changed since
// the last send (the ServerPlayer dirty-send discipline). Seeds the last-sent triple on the first call
// (xpInit) so a join with 0 XP does not spuriously send. Tick-owned.
func (t *TickLoop) sendExperience(p *tickPlayer) {
	if p.xpInit &&
		p.experienceProgress == p.lastSentXpProgress &&
		p.experienceLevel == p.lastSentXpLevel &&
		p.totalExperience == p.lastSentXpTotal {
		return // no change since the last send
	}
	p.lastSentXpProgress = p.experienceProgress
	p.lastSentXpLevel = p.experienceLevel
	p.lastSentXpTotal = p.totalExperience
	p.xpInit = true
	if p.client != nil {
		p.client.Send(setExperience(p.experienceProgress, p.experienceLevel, p.totalExperience))
	}
}

// distanceToSqrPlayer is Entity.distanceToSqr(Entity) for a player↔orb pair: the 3D squared Euclidean
// distance between the player's position and the orb's. Tick-owned read.
func distanceToSqrPlayer(p *tickPlayer, e *Entity) float64 {
	dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
	return dx*dx + dy*dy + dz*dz
}

// nearestPlayerEntityWithin is the port of Level.getNearestPlayer(Entity, double) for the orb: the
// nearest player within maxDist (3D Euclidean) of the orb, or nil if none. Scans the tick-owned
// loop.players (the player seam — players are not entityStore entries here), returning the *tickPlayer so
// the caller can record its entityID + home toward its exact position. A dead player is skipped
// (getNearestPlayer's not-dead predicate). Tick-owned read.
func (t *TickLoop) nearestPlayerEntityWithin(e *Entity, maxDist float64) *tickPlayer {
	best := maxDist * maxDist
	var nearest *tickPlayer
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best {
			best = d2
			nearest = p
		}
	}
	return nearest
}
