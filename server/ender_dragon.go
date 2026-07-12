package server

// ender_dragon.go -- the Ender Dragon boss (net.minecraft.world.entity.boss.enderdragon.EnderDragon), a
// 1:1 port from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this task). The dragon
// is vanilla's ONLY multi-AABB entity: it carries 8 EnderDragonPart sub-boxes (head/neck/body/tail1..3/
// wing1..2), each an independent pickable box, and its hurt(ServerLevel, EnderDragonPart, DamageSource,
// float) applies a PER-PART damage transform (the head takes full damage; every other part takes
// damage/4 + min(damage,1)) BEFORE the shared hurt pipeline. This is the exact template model_hitbox.go
// documents (the per-bone damageMult generalizes this rule) -- but the dragon has NO plugin model, so it
// owns its OWN []dragonPart array on the Entity (recomputed each aiStep from the dragon's world pos),
// never routed through the plugin bone system.
//
// PHASES LANDED: HOLDING (the default circling-flight phase, phase 0). PHASES DEFERRED (cited): the full
// EnderDragonPhaseManager state machine (STRAFE/LANDING/TAKEOFF/SITTING/CHARGING/DYING transitions). v1
// pins the dragon in HOLDING (currentPhase.onHurt is identity in HOLDING, isSitting() false), so the hurt
// transform + crystal heal + death XP/portal/egg -- the OBSERVABLE boss loop -- are all exact. The
// obsidian-pillar EndCrystal towers (SpikeFeature) are deferred; spawnEndDragonFight spawns a testable
// ring of crystals so the heal is exercisable.

import (
	"math"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// dragonPhase enumerates the EnderDragonPhase kinds we model. v1 pins the dragon in HOLDING; DYING is set
// by the death drive so dragonHurtPart's `getPhase() == DYING` guard reads it. Cite EnderDragonPhase.
type dragonPhase int32

const (
	dragonPhaseHolding dragonPhase = 0 // EnderDragonPhase.HOLDING (the default circling flight)
	dragonPhaseDying   dragonPhase = 9 // EnderDragonPhase.DYING (set by tickDragonDeath)
)

// dragonPart is one of the EnderDragon's 8 EnderDragonPart sub-boxes. name is the vanilla part name; w/h
// are the EntityDimensions; offX/Y/Z is the part's offset from the dragon position; min/max is the world
// AABB recomputed each aiStep, centered on the part point (EnderDragonPart.makeBoundingBox). Cite
// EnderDragon ctor subEntities + EnderDragonPart.
type dragonPart struct {
	name             string
	w, h             float32
	offX, offY, offZ float64
	minX, minY, minZ float64
	maxX, maxY, maxZ float64
}

// dragonState holds all EnderDragon-specific tick state behind the single e.dragon pointer (a non-dragon
// entity touches none of it). parts is the 8-part array; phase is the current EnderDragonPhase (HOLDING in
// v1); dragonDeathTime mirrors EnderDragon.dragonDeathTime (0..200); growlTime mirrors EnderDragon.growlTime
// (ctor 100); nearestCrystalID is EnderDragon.nearestCrystal (0 == none); sittingDamageReceived mirrors the
// sitting-phase TAKEOFF accumulator (a cited no-op in HOLDING); bossBarID is the ServerBossEvent id;
// bossProgress is the last broadcast progress; origin is the fight origin the HOLDING flight circles. Cite
// EnderDragon fields.
type dragonState struct {
	parts                     []dragonPart
	phase                     dragonPhase
	dragonDeathTime           int32
	growlTime                 int32
	nearestCrystalID          int32
	sittingDamageReceived     float32
	bossBarID                 uuid.UUID
	bossProgress              float32
	originX, originY, originZ float64

	// FLIGHT AI (the phase state machine + pillar-node pathfinder, ender_dragon_flight.go /
	// ender_dragon_phases.go). path is the lazily-built 24-node flight graph; flight is the 64-sample
	// DragonFlightHistory the part animation reads back; phaseState folds every phase instance's mutable
	// fields; yRotA mirrors EnderDragon.yRotA (the aiStep yaw accumulator); inWall mirrors EnderDragon.inWall;
	// takeoffFirstTick mirrors DragonTakeoffPhase.firstTick. Cite EnderDragon + the phases.*.
	path             *dragonPathState
	flight           *dragonFlightHistory
	phaseState       dragonPhaseState
	yRotA            float32
	inWall           bool
	takeoffFirstTick bool

	// flameCloudID is DragonSittingFlamingPhase.flame -- the id of the breath AreaEffectCloud spawned at
	// flameTick 10, held so DragonSittingFlamingPhase.end() can discard() it (setPhase out of SITTING_FLAMING).
	// 0 == no live flame cloud. Cite DragonSittingFlamingPhase.flame.
	flameCloudID int32
}

// dragonPartSpec is the ctor's per-part (name, width, height) table, in the EXACT subEntities[] order.
// Cite EnderDragon ctor: head(1,1) neck(3,3) body(5,3) tail1(2,2) tail2(2,2) tail3(2,2) wing1(4,2) wing2(4,2).
var dragonPartSpec = []struct {
	name string
	w, h float32
}{
	{"head", 1, 1},
	{"neck", 3, 3},
	{"body", 5, 3},
	{"tail1", 2, 2},
	{"tail2", 2, 2},
	{"tail3", 2, 2},
	{"wing1", 4, 2},
	{"wing2", 4, 2},
}

// Ender Dragon constants (VERIFIED javap EnderDragon ctor + tickDeath this task).
const (
	dragonMaxHealth            = 200.0 // createAttributes MAX_HEALTH 200.0
	dragonGrowlTime            = 100   // ctor growlTime = 100
	dragonCrystalHealAmount    = 1.0   // checkCrystals setHealth(health + 1.0f)
	dragonCrystalHealPeriod    = 10    // checkCrystals tickCount % 10 == 0
	dragonCrystalScanRadius    = 32.0  // checkCrystals getBoundingBox().inflate(32.0)
	dragonCrystalHurtDamage    = 10.0  // onCrystalDestroyed hurt(head, explosion, 10.0f)
	dragonDeathMaxTicks        = 200   // tickDeath removes at dragonDeathTime == 200
	dragonDeathXpStartTick     = 150   // tickDeath awards XP at dragonDeathTime > 150
	dragonDeathXpPeriod        = 5     // ... && dragonDeathTime % 5 == 0
	dragonDeathXpTotal         = 500   // xp = 500 (12000 first-ever kill -- cited default 500 in v1)
	dragonDeathXpFraction      = 0.08  // ExperienceOrb.award(Mth.floor(xp * 0.08f)) per award
	dragonDeathFinalXpFraction = 0.2   // one-shot ExperienceOrb.award(Mth.floor(xp * 0.2f)) at dragonDeathTime == 200
	dragonHurtMinDamage        = 0.01  // hurt: if (dmg < 0.01f) return false
	dragonHoldingRadius        = 30.0  // HOLDING circling radius around fightOrigin (v1 flight)
	dragonHoldingSpeed         = 0.02  // HOLDING angular step per tick (radians)
)

// spawnEnderDragon creates the EnderDragon boss at (x,y,z) with 200 HP (attribute map + initSpawnHealth)
// and the 8-part sub-box array, pinned in HOLDING. The code-spawned analogue of EnderDragonFight.spawnDragon
// (new EnderDragon + addFreshEntity). initSpawnHealth reads the folded MAX_HEALTH (200.0). Cite EnderDragon
// ctor + EnderDragonFight.spawnDragon.
func (t *TickLoop) spawnEnderDragon(x, y, z float64) *Entity {
	d := NewEntity(t.idAlloc.AllocID(), entity.EnderDragon, x, y, z)
	parts := make([]dragonPart, len(dragonPartSpec))
	for i, s := range dragonPartSpec {
		parts[i] = dragonPart{name: s.name, w: s.w, h: s.h}
	}
	d.dragon = &dragonState{
		parts:            parts,
		phase:            dragonPhaseHovering, // ctor: phaseManager starts HOVERING (setPhase(HOLDING) below)
		dragonDeathTime:  0,                   // ctor: dragonDeathTime = 0
		growlTime:        dragonGrowlTime,     // ctor: growlTime = 100
		nearestCrystalID: 0,
		bossBarID:        uuid.New(),
		bossProgress:     1.0,
		originX:          x,
		originY:          y,
		originZ:          z,
		path:             &dragonPathState{},
		flight:           newDragonFlightHistory(),
	}
	initSpawnHealth(d) // setHealth(getMaxHealth()) -> 200.0
	// Seed the dragon per-entity RNG (mobRandom reads e.ai.rng) so the checkCrystals nextInt(10) rescan
	// cadence draws on the dragon's OWN stream, seeded deterministically off its id (like spawnGhast).
	d.ai = &mobAI{}
	reseedMobAI(d.ai, d.id)
	// EnderDragon.removeWhenFarAway returns false (a boss never despawns); model it as persistenceRequired
	// so checkDespawn returns early (the faithful "boss is always persistent" outcome). Cite EnderDragon
	// (no removeWhenFarAway cull) + Mob.isPersistenceRequired.
	d.ai.persistenceRequired = true
	t.dragonRecomputeParts(d)
	// createNewDragon: getPhaseManager().setPhase(HOLDING_PATTERN) (begin() clears the HOLDING path/target).
	t.dragonSetPhase(d, dragonPhaseHolding)
	owner := t.regionForEntity(d)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(d)
	// Boss bar (vanilla PINK / PROGRESS / createWorldFog=true / playBossMusic=true): add every tracked player.
	t.dragonBossBarAddAll(d)
	return d
}

// dragonIsFlyer reports whether an entity is the EnderDragon (the tickPhysics flyer gate reads it so the
// dragon takes the no-gravity path -- EnderDragon.noPhysics == true). Cite EnderDragon ctor noPhysics = true.
func dragonIsFlyer(e *Entity) bool {
	return e.dragon != nil
}

// dragonRecomputeParts refreshes every part's world AABB from the dragon's CURRENT position + the part's
// static offset, centered on the part point (min = center - half, max = center + half) exactly as
// EnderDragonPart.makeBoundingBox centers a box on its position. v1 uses a STATIC offset layout along the
// +X body axis (vanilla animates these off the flight sim -- cited deferral); the head-full/others-quartered
// hit transform only needs the parts to exist + be resolvable. Cite EnderDragon ctor subEntities.
func (t *TickLoop) dragonRecomputeParts(e *Entity) {
	d := e.dragon
	if d == nil {
		return
	}
	layout := map[string][3]float64{
		"head":  {6, 0, 0},
		"neck":  {4, 0, 0},
		"body":  {0, 0, 0},
		"tail1": {-4, 0, 0},
		"tail2": {-6, 0, 0},
		"tail3": {-8, 0, 0},
		"wing1": {0, 1, 4},
		"wing2": {0, 1, -4},
	}
	for i := range d.parts {
		p := &d.parts[i]
		off := layout[p.name]
		p.offX, p.offY, p.offZ = off[0], off[1], off[2]
		cx := e.x + p.offX
		cy := e.y + p.offY
		cz := e.z + p.offZ
		hw := float64(p.w) / 2.0
		hh := float64(p.h) / 2.0
		p.minX, p.minY, p.minZ = cx-hw, cy-hh, cz-hw
		p.maxX, p.maxY, p.maxZ = cx+hw, cy+hh, cz+hw
	}
}

// enderDragonAiStep is the per-tick EnderDragon tick, driven per-type from tickAI (gated on typ ==
// entity.EnderDragon.ID). Reduced to the OBSERVABLE HOLDING loop: recompute the part boxes, decrement
// growlTime, run the HOLDING circling flight, run checkCrystals (the +1 heal every 10 ticks while a live
// crystal is near and health<max, plus the nextInt(10)==0 rescan), and push the boss-bar progress. A DYING
// dragon does NOT run this (tickDragonDeath owns it). All RNG draws are on the dragon's OWN stream. Cite
// EnderDragon.aiStep + checkCrystals.
func (t *TickLoop) enderDragonAiStep(e *Entity) {
	d := e.dragon
	if d == nil || e.dead || e.health <= 0 {
		return
	}
	t.dragonRecomputeParts(e)

	// SERVER aiStep (EnderDragon.aiStep ServerLevel branch): the growl draw is CLIENT-only (inside
	// if(level.isClientSide())), so the server does NOT decrement growlTime or draw the growl-pitch
	// nextFloat -- the only server RNG here is checkCrystals' nextInt(10) + the phase draws. checkCrystals
	// runs FIRST (aiStep line: this.checkCrystals()), then the flight/phase integrator. Cite EnderDragon.aiStep.
	t.dragonCheckCrystals(e)
	t.dragonFlightStep(e)

	// Boss bar progress = health / maxHealth (ServerBossEvent.setProgress); sends only on a change.
	t.dragonBossBarUpdateProgress(e)
}

// dragonCheckCrystals is the port of EnderDragon.checkCrystals(): while a nearestCrystal is tracked and
// still alive, heal +1 every 10 ticks (while health < maxHealth); if it was removed, clear it. Then, on
// random.nextInt(10)==0, rescan for the nearest EndCrystal within getBoundingBox().inflate(32.0) and adopt
// it.
//
//	[VERIFIED javap EnderDragon.checkCrystals:
//	 if (nearestCrystal != null) { if (nearestCrystal.isRemoved()) nearestCrystal = null;
//	   else if (tickCount % 10 == 0 && getHealth() < getMaxHealth()) setHealth(getHealth() + 1.0f); }
//	 if (random.nextInt(10) == 0) { list = level.getEntitiesOfClass(EndCrystal, bb.inflate(32.0));
//	   ... pick nearest -> nearestCrystal; }]
//
// tickCount is the dragon's per-entity counter; v1 uses t.gametime (the dragon lives for the fight, so
// gametime%10 is an equivalent cadence). The random draw is on the dragon's OWN mobRandom stream.
func (t *TickLoop) dragonCheckCrystals(e *Entity) {
	d := e.dragon
	if d.nearestCrystalID != 0 {
		owner := t.regionForEntity(e)
		cr, ok := owner.entities.get(d.nearestCrystalID)
		if !ok || cr == nil || cr.dead {
			d.nearestCrystalID = 0 // nearestCrystal.isRemoved() -> null
		} else if t.gametime%dragonCrystalHealPeriod == 0 && e.health < float32(dragonMaxHealth) {
			e.health += dragonCrystalHealAmount // setHealth(getHealth() + 1.0f)
			if e.health > float32(dragonMaxHealth) {
				e.health = float32(dragonMaxHealth)
			}
		}
	}
	if mobRandom(e).nextInt(10) == 0 { // random.nextInt(10) == 0: rescan
		// checkCrystals: list = getEntitiesOfClass(EndCrystal, getBoundingBox().inflate(32.0)); then
		// d3 = Double.MAX_VALUE; for each crystal, distSq = crystal.distanceToSqr(this); if (distSq < d3)
		// { d3 = distSq; nearest = crystal; }. The 32.0 ONLY bounds the candidate AABB (a pre-filter) --
		// the SELECTION is strict `<` with an initial +Inf, NOT a `<= 32*32` distance bound. Cite
		// EnderDragon.checkCrystals (bytecode 82-85 inflate(32.0); 94 Double.MAX_VALUE; 138-140 dcmpg
		// ifge -> keep only on strict <).
		var best *Entity
		bestSq := math.Inf(1)              // d3 = Double.MAX_VALUE
		inflate := dragonCrystalScanRadius // 32.0: the getBoundingBox().inflate(32.0) half-extent
		owner := t.regionForEntity(e)
		for _, other := range owner.entities.all() {
			if other == nil || !other.isEndCrystal || other.dead {
				continue
			}
			// Pre-filter: candidate must lie within the inflated-32 AABB around the dragon's box (the only
			// role the 32.0 plays -- it does NOT bound the nearest-selection distance).
			if math.Abs(other.x-e.x) > inflate || math.Abs(other.y-e.y) > inflate || math.Abs(other.z-e.z) > inflate {
				continue
			}
			ddx := other.x - e.x
			ddy := other.y - e.y
			ddz := other.z - e.z
			dsq := ddx*ddx + ddy*ddy + ddz*ddz
			if dsq < bestSq { // strict < (dcmpg; ifge)
				bestSq = dsq
				best = other
			}
		}
		if best != nil {
			d.nearestCrystalID = best.id
		}
	}
}

// dragonResolveHitPart tests a segment (ox,oy,oz)->(nx,ny,nz) against every dragon sub-part box and returns
// the name of the CLOSEST intersected part (nearest entry parameter), mirroring ProjectileUtil
// .getEntityHitResult's nearest-entry rule extended to the 8 boxes -- the same nearest-t slab method
// model_hitbox.go's resolveHitBone uses (both reuse segmentAABB from projectile.go). Returns "" on a miss
// (the caller treats "" as a non-head body hit). Cite EnderDragonPart pickable boxes + getEntityHitResult.
func (t *TickLoop) dragonResolveHitPart(e *Entity, ox, oy, oz, nx, ny, nz float64) string {
	d := e.dragon
	if d == nil {
		return ""
	}
	bestT := math.Inf(1)
	bestName := ""
	for i := range d.parts {
		p := &d.parts[i]
		hit, tHit := segmentAABB(ox, oy, oz, nx, ny, nz, p.minX, p.minY, p.minZ, p.maxX, p.maxY, p.maxZ)
		if !hit {
			continue
		}
		if tHit < bestT {
			bestT = tHit
			bestName = p.name
		}
	}
	return bestName
}

// dragonHurtPart is the port of EnderDragon.hurt(ServerLevel, EnderDragonPart, DamageSource, float) -- the
// PER-PART damage entry the boss fight routes through. partName selects the sub-box ("head" or one of the
// others; "" is treated as a non-head body hit). The transform: the head takes FULL damage; every other
// part takes damage/4 + min(damage,1). A DYING dragon rejects all damage; a sub-0.01 result is dropped. On
// a surviving hit from a Player (or an ALWAYS_HURTS_ENDER_DRAGONS source) the reduced amount routes through
// the shared reallyHurt-equivalent (applyDamageEntity: mutates e.health + drives dieEntity if lethal).
//
//	[VERIFIED javap EnderDragon.hurt:
//	 if (getPhase() == DYING) return false;
//	 dmg = currentPhase.onHurt(src, dmg);                    // HOLDING onHurt == identity
//	 if (part != this.head) dmg = dmg / 4.0f + Math.min(dmg, 1.0f);
//	 if (dmg < 0.01f) return false;
//	 if (src.getEntity() instanceof Player || src.is(ALWAYS_HURTS_ENDER_DRAGONS)) {
//	    float h = getHealth(); reallyHurt(level, src, dmg);
//	    if (currentPhase.isSitting() && ...sittingDamageReceived...);
//	 }
//	 return true;]
//
// onHurt is IDENTITY in HOLDING (cited v1 phase); the reduction is applied AFTER onHurt as vanilla orders.
// isSitting() is FALSE in HOLDING, so the sitting-damage TAKEOFF accumulator is a cited no-op (structured
// to slot a real phase manager in unchanged). The Player gate is modeled by src.attacker resolving to a
// live player, OR the source being an ALWAYS_HURTS_ENDER_DRAGONS member (the crystal-explosion self-hit).
func (t *TickLoop) dragonHurtPart(e *Entity, partName string, src damageSource, dmg float32) bool {
	d := e.dragon
	if d == nil {
		return false
	}
	if d.phase == dragonPhaseDying { // if (getPhase() == DYING) return false;
		return false
	}
	// dmg = currentPhase.onHurt(src, dmg): AbstractDragonPhaseInstance.onHurt is identity; the SITTING
	// phases (AbstractDragonSittingPhase.onHurt) ZERO arrow/wind_charge damage (and ignite the projectile
	// -- ignite is a cited seam since the projectile entity ref is not threaded here). Applied BEFORE the
	// part /4 reduction, exactly as vanilla orders. Cite EnderDragon.hurt + AbstractDragonSittingPhase.onHurt.
	dmg = dragonPhaseOnHurt(d, src, dmg)

	if partName != "head" { // if (part != this.head) dmg = dmg/4.0f + Math.min(dmg, 1.0f);
		dmg = dmg/4.0 + minF32(dmg, 1.0)
	}

	if dmg < dragonHurtMinDamage { // if (dmg < 0.01f) return false;
		return false
	}

	isPlayer := src.attacker != 0 && t.playerByEntityID(src.attacker) != nil
	if isPlayer || src.is("always_hurts_ender_dragons") {
		hBefore := e.health
		// reallyHurt(level, src, dmg): the shared LivingEntity hurt tail (applyDamageEntity mutates
		// e.health, clamps 0, and drives dieEntity on a lethal hit).
		t.applyDamageEntity(e, src, dmg)
		// Sitting-phase TAKEOFF accumulator (EnderDragon.hurt: if (currentPhase.isSitting()) {
		// sittingDamageReceived += h - getHealth(); if (sittingDamageReceived > 0.25f*getMaxHealth())
		// { sittingDamageReceived = 0; setPhase(TAKEOFF); } }). Now that the phase machine + sitting
		// phases exist, this is FAITHFUL (not a stub): a sitting dragon that takes >25% max HP bursts
		// back into flight. Cite EnderDragon.hurt.
		if dragonPhaseIsSitting(d) {
			d.sittingDamageReceived += hBefore - e.health
			if d.sittingDamageReceived > 0.25*float32(dragonMaxHealth) {
				d.sittingDamageReceived = 0
				t.dragonSetPhase(e, dragonPhaseTakeoff)
			}
		} else {
			_ = hBefore
		}
	}
	return true
}

// dragonOnCrystalDestroyed is the port of EnderDragon.onCrystalDestroyed(ServerLevel, EndCrystal, BlockPos,
// DamageSource): when a crystal that was THE dragon's nearestCrystal is destroyed, the dragon takes a
// 10.0-damage explosion hit to the HEAD. The player is resolved from the destroying source (v1 uses the
// source attacker, or 0). currentPhase.onCrystalDestroyed is a cited no-op in HOLDING.
//
//	[VERIFIED javap EnderDragon.onCrystalDestroyed:
//	 Player player = (src.getEntity() instanceof Player) ? src.getEntity()
//	     : level.getNearestPlayer(CRYSTAL_DESTROY_TARGETING, this);
//	 if (crystal == this.nearestCrystal) hurt(level, this.head, damageSources().explosion(this, player), 10.0f);
//	 currentPhase.onCrystalDestroyed(crystal, pos, src, player);]
//
// The explosion source is an ALWAYS_HURTS_ENDER_DRAGONS member so dragonHurtPart's Player-or-tag gate passes
// even with no player resolvable. The 10.0 lands on the HEAD -> the full-damage branch (no /4 reduction).
func (t *TickLoop) dragonOnCrystalDestroyed(e *Entity, crystalID int32, src damageSource) {
	d := e.dragon
	if d == nil {
		return
	}
	if crystalID == d.nearestCrystalID {
		explosion := damageSourceOf(damageTypeExplosion)
		explosion.attacker = src.attacker // the destroying player (or 0)
		t.dragonHurtPart(e, "head", explosion, dragonCrystalHurtDamage)
	}
	// currentPhase.onCrystalDestroyed: cited no-op in HOLDING.
}

// tickDragonDeath is the port of EnderDragon.tickDeath() -- the death countdown that drives the exit-portal
// spawn, the XP shower, and the dragon egg. It runs from the death path (tick_phases.go, gated on e.dragon)
// INSTEAD of the generic tickDeath (a dragon has no 20-tick poof; it rises for 200 ticks then spawns the
// end-fight rewards). Sets DYING (so further hits reject), rises 0.1/tick, showers XP (floor(xp*0.08) every
// 5 ticks after tick 150), and at tick 200 spawns the exit portal + dragon egg and removes the dragon.
//
//	[VERIFIED javap EnderDragon.tickDeath:
//	 ++dragonDeathTime;
//	 if (dragonDeathTime >= 180 && <= 200) addParticle EXPLOSION_EMITTER;         // client visual
//	 int xp = 500; if (dragonFight != null && !hasPreviouslyKilledDragon()) xp = 12000;
//	 if (level instanceof ServerLevel) {
//	   if (dragonDeathTime > 150 && dragonDeathTime % 5 == 0 && MOB_DROPS)
//	      ExperienceOrb.award(level, position(), Mth.floor(xp * 0.08f));
//	   if (dragonDeathTime == 1 && !isSilent()) globalLevelEvent(1028, blockPosition(), 0); }  // sound
//	 move(SELF, new Vec3(0.0, 0.1, 0.0));                                          // rises while dying
//	 if (dragonDeathTime == 200) { setDragonKilled(...); remove(KILLED); }]
//
// xp = 500 (the 12000 first-ever-kill bonus needs an EnderDragonFight.hasPreviouslyKilledDragon read; cited
// default 500). The EXPLOSION_EMITTER particle + levelEvent 1028 death sound are client visuals (deferred).
// MOB_DROPS defaults true (cited const). Runs on the owner region.
func (t *TickLoop) tickDragonDeath(e *Entity) {
	d := e.dragon
	if d == nil {
		return
	}
	d.phase = dragonPhaseDying // setPhase(DYING): dragonHurtPart rejects further hits.

	d.dragonDeathTime++ // ++dragonDeathTime;

	xp := dragonDeathXpTotal // int xp = 500 (v1 default; 12000 first-kill bonus cite-deferred).

	// if (dragonDeathTime > 150 && % 5 == 0 && MOB_DROPS) ExperienceOrb.award(pos, Mth.floor(xp*0.08f)).
	// MOB_DROPS is now a live GameRules read (default true). CITE: EnderDragon.tickDeath (MOB_DROPS gate).
	mobDrops := t.gameRule(ruleMobDrops)
	if d.dragonDeathTime > dragonDeathXpStartTick && d.dragonDeathTime%dragonDeathXpPeriod == 0 && mobDrops {
		reward := int(math.Floor(float64(xp) * dragonDeathXpFraction)) // floor(500 * 0.08) == 40
		t.awardExperienceOrbs(e, reward)
	}

	// move(SELF, new Vec3(0, 0.1, 0)): the dragon rises while dying.
	t.regionForEntity(e).entities.move(e, e.x, e.y+0.1, e.z)

	// if (dragonDeathTime == 200) { <one-shot XP>; setDragonKilled -> spawn exit portal + dragon egg;
	// remove(KILLED); gameEvent(ENTITY_DIE). }
	if d.dragonDeathTime >= dragonDeathMaxTicks {
		// E-2 FIX (bytecode 331-394): at dragonDeathTime >= 200, a ONE-SHOT ExperienceOrb.award(level,
		// position(), Mth.floor(xp * 0.2f)) fires (gated on MOB_DROPS) BEFORE setDragonKilled. This is
		// SEPARATE from and ADDITIONAL to the per-5-tick floor(xp*0.08) shower above -- the death frame
		// awards the remaining 0.2 chunk (default xp 500 -> +100). Cite EnderDragon.tickDeath (bytecode
		// 380-394: fload xp; i2f; ldc 0.2f; fmul; Mth.floor; ExperienceOrb.award).
		if mobDrops {
			reward := int(math.Floor(float64(xp) * dragonDeathFinalXpFraction)) // floor(500 * 0.2) == 100
			t.awardExperienceOrbs(e, reward)
		}
		t.dragonSpawnExitPortalAndEgg(e)
		t.dragonBossBarRemoveAll(e)
		t.regionForEntity(e).entities.remove(e.id)
	}
}

// dragonSpawnExitPortalAndEgg is the reward half of EnderDragonFight.setDragonKilled -> spawnExitPortal: an
// END_PORTAL block at the fight origin (the way home) with a DRAGON_EGG perched on top (the trophy). The
// full EndPodiumFeature (bedrock podium + portal-frame ring + beacon) is the cited deferral -- the
// OBSERVABLE reward (a portal block + the egg) is placed. Written through the dragon's dimension world
// (t.endWorld when present, else the region world). Cite EnderDragonFight.spawnExitPortal + EndPodiumFeature.
func (t *TickLoop) dragonSpawnExitPortalAndEgg(e *Entity) {
	d := e.dragon
	mgr := t.endWorld
	minY := dimEndMinY
	if mgr == nil {
		mgr = t.regionForEntity(e).world
		minY = dimMinY
	}
	if mgr == nil {
		return
	}
	bx := int(math.Floor(d.originX))
	by := int(math.Floor(d.originY))
	bz := int(math.Floor(d.originZ))
	portal := block.ToStateID[block.EndPortal{}]
	egg := block.ToStateID[block.DragonEgg{}]
	mgr.SetBlock(pk.Position{X: bx, Y: by, Z: bz}, portal, minY)  // END_PORTAL at the origin (exit)
	mgr.SetBlock(pk.Position{X: bx, Y: by + 1, Z: bz}, egg, minY) // DRAGON_EGG one above (trophy)

	// END GATEWAY (Task): EnderDragonFight.setDragonKilled -> spawnNewGateway() places ONE gateway on the
	// 96-block ring per dragon kill (idx popped from the shuffled ContiguousSet[0,20)). v1 spawns the next
	// ring slot in index order (a cited simplification -- the RING math is identical, only WHICH slot
	// differs). This is the delimited death-sequence hook: after the exit portal + egg, place the gateway
	// so the player can leave for the outer End islands. Cite EnderDragonFight.spawnNewGateway.
	if t.endGatewaysSpawned < gatewayCount {
		t.spawnNewGateway(t.endGatewaysSpawned)
		t.endGatewaysSpawned++
	}
}

// --- BOSS BAR (vanilla dragon bar: PINK / PROGRESS / createWorldFog / playBossMusic) ------------------
//
// The dragon boss bar is a ServerBossEvent that is NOT raid-coupled (bossbar.go's broadcast helpers are
// Raid-bound), so these send ClientboundBossEvent per-player directly over t.players via pl.client.Send --
// the same seam bossBroadcast's loop uses. name is the translate key entity.minecraft.ender_dragon.
// progress = health / maxHealth. Cite EnderDragonFight.dragonEvent (ServerBossEvent(component, PINK,
// PROGRESS); setCreateWorldFog(true); setPlayBossMusic(true)).

// dragonBossName is the dragon bar's translatable Component (entity.minecraft.ender_dragon).
func dragonBossName() chat.Message {
	return chat.Message{Translate: "entity.minecraft.ender_dragon"}
}

// dragonBossBarAddAll sends the ADD packet (the full bar state) to every currently-tracked player (mirrors
// ServerBossEvent.addPlayer over the player set). PINK / PROGRESS, darken=false, music=true, fog=true.
func (t *TickLoop) dragonBossBarAddAll(e *Entity) {
	d := e.dragon
	if d == nil {
		return
	}
	progress := e.health / float32(dragonMaxHealth)
	d.bossProgress = progress
	pkt := encodeBossEventAdd(d.bossBarID, dragonBossName(), progress,
		bossBarColorPink, bossBarOverlayProgress, false, true, true)
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(pkt)
		}
	}
}

// dragonBossBarUpdateProgress pushes health/maxHealth as the bar progress, sending UPDATE_PROGRESS only on
// a change (ServerBossEvent.setProgress change guard).
func (t *TickLoop) dragonBossBarUpdateProgress(e *Entity) {
	d := e.dragon
	if d == nil {
		return
	}
	progress := e.health / float32(dragonMaxHealth)
	if progress == d.bossProgress {
		return
	}
	d.bossProgress = progress
	pkt := encodeBossEventUpdateProgress(d.bossBarID, progress)
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(pkt)
		}
	}
}

// dragonBossBarRemoveAll sends the REMOVE packet to every tracked player (ServerBossEvent.removeAllPlayers).
// Called when the dragon dies (tickDragonDeath at tick 200).
func (t *TickLoop) dragonBossBarRemoveAll(e *Entity) {
	d := e.dragon
	if d == nil {
		return
	}
	pkt := encodeBossEventRemove(d.bossBarID)
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(pkt)
		}
	}
}

// spawnEndDragonFight is the lazy fight initializer: on the first player entry into the End it spawns the
// EnderDragon at the fight origin ((0,~128,0)) and a testable RING of EndCrystals around it (so the +1 heal
// is exercisable). The reduced analogue of EnderDragonFight.spawnDragon + SpikeFeature (the 10 obsidian
// pillars each topped with an EndCrystal). The full SpikeFeature pillar generation is the CITED DEFERRAL --
// v1 spawns a bare ring of crystals at the pillar radius so the crystal-heal + crystal-destroy -> head-hit
// loop is fully testable. Idempotent via t.endDragonFightInit. Cite EnderDragonFight.spawnDragon + SpikeFeature.
func (t *TickLoop) spawnEndDragonFight() *Entity {
	if t.endDragonFightInit {
		return nil
	}
	t.endDragonFightInit = true
	const originX, originY, originZ = 0.0, 128.0, 0.0
	dragon := t.spawnEnderDragon(originX, originY, originZ)
	const pillarRadius = 43.0
	const crystalY = 76.0
	const pillarCount = 10
	for i := 0; i < pillarCount; i++ {
		a := (2.0 * math.Pi * float64(i)) / float64(pillarCount)
		cx := originX + pillarRadius*math.Cos(a)
		cz := originZ + pillarRadius*math.Sin(a)
		t.spawnEndCrystal(cx, crystalY, cz)
	}
	return dragon
}
