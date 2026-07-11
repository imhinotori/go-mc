package server

// shulker.go -- the Shulker (net.minecraft.world.entity.monster.Shulker), a 1:1 port from the
// unobfuscated 26.2 jar (javap -c -p this task). The Shulker is the End box-turret hostile: it
// CLINGS to a surface (attachFace), sits CLOSED (peek 0, +20 armor), OPENS to fire a homing
// ShulkerBullet that inflicts LEVITATION, and TELEPORTS when its spot is destroyed/exposed. Same
// additive + per-type-gated pattern as phantom/warden: ALL shulker state lives behind the single
// e.shulker pointer (nil for every other entity) so the pig oracle stays byte-identical.
//
// VANILLA (verified javap Shulker + inner classes + ShulkerBullet this task):
//   createAttributes: Mob.createMobAttributes() + MAX_HEALTH 30.0 (ARMOR base 0.0). The +20 armor
//     while CLOSED is COVERED_ARMOR_MODIFIER ("covered", 20.0, ADD_VALUE), added when peek==0 /
//     removed otherwise by setRawPeekAmount. isClosed() == getRawPeekAmount()==0.
//   ShulkerAttackGoal.tick: attackTime--; at <=0 arm attackTime = 20 + nextInt(10)*20/2 + spawn a
//     ShulkerBullet(level, this, target, attachFace.axis).
//   teleportSomewhere(): 5 tries, each axis Mth.randomBetweenInclusive(rng,-8,8); require empty +
//     above getMinY() + an attachable surface; teleportTo + setAttachFace on success.
//   ShulkerBullet.onHitEntity: hurtOrSimulate(mobProjectile(owner),4.0f); on hit a LivingEntity add
//     MobEffectInstance(LEVITATION, 200) (amplifier 0).
//
// LANDED (bytecode-exact): attributes (30 HP + the +20 covered-armor toggle), the peek open/close
// state machine, the ranged-attack cadence -> homing bullet, the bullet 4.0 damage + 200-tick
// LEVITATION via the EXISTING addPlayerEffect, the 5-attempt teleport-on-expose, attachFace/color.
// ALL RNG is on the shulker OWN mobRandom stream.
//
// DEFERRED (cited): natural spawn is via the End City "Sentry" data marker (no roaming spawner,
// matching vanilla). The peek-animation lerp, open/close sounds + CONTAINER game events, and the
// bullet client particles are cite-deferred visuals; the peek folds to a closed/open toggle.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
)

// Shulker constants (VERIFIED javap Shulker + Shulker$ShulkerAttackGoal + ShulkerBullet this task).
const (
	shulkerMaxHealth        = 30.0
	shulkerCoveredArmor     = 20.0
	shulkerCoveredArmorID   = "covered"
	shulkerPeekClosed       = 0
	shulkerPeekOpen         = 30
	shulkerNoColor          = 16
	shulkerFireDistSqr      = 400.0 // ShulkerAttackGoal.tick fires when distanceToSqr(target) < 400.0 (Euclidean < 20)
	shulkerAttackBaseTicks  = 20
	shulkerAttackJitter     = 10
	shulkerTeleportTries    = 5
	shulkerTeleportSpan     = 8
	shulkerScanCadence      = 10
	shulkerBulletDamage     = 4.0
	shulkerBulletLevitTicks = 200
	shulkerBulletLevitAmp   = 0
	shulkerBulletSpeed      = 0.35
	shulkerBulletMaxLife    = 300
	shulkerBulletHitDistSqr = 1.0
)

type shulkerState struct {
	peek         int
	attachFace   block.Direction
	color        byte
	attackTime   int32
	nextScanTick int32
	// prevHealth is the health the shulker held at the END of the previous shulkerAiStep, so the tick seam
	// can detect a health DROP (a hit landed since last tick) and run the post-hurt reaction
	// (shulkerHurtServerReaction) at the shulker's OWN tick entrypoint. This is the WIRING seam for the
	// otherwise-orphaned Shulker.hurtServer POST-hurt tail: the real hurt path (applyDamageEntity,
	// combat_mob.go) is not owned here and carries no shulker dispatch, so the teleport-on-low-hp reaction is
	// driven from the shulker-owned tick by observing the applied health change. Sentinel -1 == "not yet
	// initialized" (first tick seeds it, no reaction). Cite Shulker.hurtServer (the post-hurt teleport tail).
	prevHealth float32
}

type shulkerBulletState struct {
	ownerID  int32
	targetID int32
	life     int32
}

// spawnShulker creates a Shulker at (x,y,z) with 30 HP, CLOSED (+20 covered armor), attachFace DOWN.
// Cite Shulker(EntityType, Level) ctor + setRawPeekAmount(0).
func (t *TickLoop) spawnShulker(x, y, z float64) *Entity {
	s := NewEntity(t.idAlloc.AllocID(), entity.Shulker, x, y, z)
	s.shulker = &shulkerState{
		peek:       shulkerPeekClosed,
		attachFace: block.Down,
		color:      shulkerNoColor,
		prevHealth: -1, // seeded on the first shulkerAiStep (no reaction on the seeding tick)
	}
	_ = shulkerMaxHealth
	initSpawnHealth(s)
	s.ai = &mobAI{}
	reseedMobAI(s.ai, s.id)
	t.shulkerSetRawPeek(s, shulkerPeekClosed)
	owner := t.regionForEntity(s)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(s)
	return s
}

// shulkerSetRawPeek ports Shulker.setRawPeekAmount(int): REMOVE covered-armor; if peek==0 (closed)
// ADD COVERED_ARMOR_MODIFIER (+20 ARMOR ADD_VALUE). Sounds/game-event are cite-deferred visuals.
func (t *TickLoop) shulkerSetRawPeek(e *Entity, v int) {
	s := e.shulker
	s.peek = v
	if e.attributes == nil {
		return
	}
	inst := e.attributes.GetInstance(attribute.Armor.Name())
	if inst == nil {
		return
	}
	inst.RemoveModifier(shulkerCoveredArmorID)
	if v == shulkerPeekClosed {
		inst.AddPermanentModifier(attribute.AttributeModifier{
			ID:        shulkerCoveredArmorID,
			Amount:    shulkerCoveredArmor,
			Operation: attribute.AddValue,
		})
	}
}

// shulkerIsClosed ports Shulker.isClosed(): getRawPeekAmount() == 0.
func shulkerIsClosed(e *Entity) bool { return e.shulker != nil && e.shulker.peek == shulkerPeekClosed }

// shulkerTarget reads the shulker current attack-target player, or nil (mirrors phantomTarget).
func (t *TickLoop) shulkerTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// shulkerAiStep ports the Shulker tick + goals (gated on e.shulker != nil, AFTER serverAiStep).
// Cite Shulker.customServerAiStep + Shulker$ShulkerAttackGoal.
func (t *TickLoop) shulkerAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	s := e.shulker
	if s == nil {
		return
	}
	// POST-HURT REACTION WIRING (Shulker.hurtServer tail): the real mob hurt path (applyDamageEntity,
	// combat_mob.go) is NOT owned here and carries no shulker dispatch, so the previously-orphaned
	// shulkerHurtServerReaction (teleport-on-low-hp) is driven from the shulker's OWN tick by observing a
	// health DROP since the last tick. When the shulker took damage this tick window (health fell below the
	// seeded prevHealth), run the post-hurt reaction (nextInt(4)==0 teleport if health < maxHealth*0.5). The
	// reaction's RNG is on the shulker's own stream and is teleport-gated, so a shulker that took no damage
	// draws ZERO. prevHealth==-1 is the un-seeded first tick (seed only, no reaction). Cite Shulker.hurtServer.
	//
	// The CLOSED arrow-immunity PRE-hurt gate (shulkerArrowImmune — a closed shulker rejects an AbstractArrow
	// BEFORE damage applies) is WIRED at the top of applyDamageEntity (combat_mob.go), the sibling of the
	// wither/guardian PRE-hurt gates. See shulkerArrowImmune's doc.
	if s.prevHealth < 0 {
		s.prevHealth = e.health // seed on the first tick (no reaction)
	} else if e.health < s.prevHealth {
		// A hit landed since last tick (health dropped): run the Shulker.hurtServer post-hurt tail. src is the
		// generic post-hit reaction source (the teleport branch does not read the source type; the
		// shulker-bullet self-hit branch is a cited no-op in shulkerHurtServerReaction). Cite Shulker.hurtServer.
		t.shulkerHurtServerReaction(e, damageSourceOf(damageTypeGeneric))
	}
	s.prevHealth = e.health // track for the next tick's drop detection (nothing below changes the shulker's health)
	// ShulkerAttackGoal.canUse: getTarget() != null && getTarget().isAlive() && difficulty != PEACEFUL.
	// There is NO range gate on canUse -- the goal RUNS (and opens the shell) whenever a live target exists;
	// it only FIRES a bullet when distanceToSqr(target) < 400.0 (ShulkerAttackGoal.tick). PEACEFUL disarms.
	//	[VERIFIED javap Shulker$ShulkerAttackGoal.canUse: getTarget() alive && level.getDifficulty() != PEACEFUL
	//	 (no distance check); tick: distanceToSqr(target) < 400.0d (dcmpg; iflt) gates the bullet.]
	if serverDifficulty == difficultyPeaceful {
		if !shulkerIsClosed(e) {
			t.shulkerSetRawPeek(e, shulkerPeekClosed) // stop(): setRawPeekAmount(0)
		}
		return
	}
	t.shulkerAcquireTarget(e)
	target := t.shulkerTarget(e)
	if target != nil {
		// canUse true (live target, not peaceful): open the shell (start(): setRawPeekAmount(100)) and run tick.
		if shulkerIsClosed(e) {
			t.shulkerSetRawPeek(e, shulkerPeekOpen)
			s.attackTime = shulkerAttackBaseTicks // start(): attackTime = 20 (the first-shot warm-up delay)
		}
		t.shulkerAttackGoal(e, target) // tick(): fire only when distanceToSqr < 400.0 + attackTime <= 0
	} else if !shulkerIsClosed(e) {
		t.shulkerSetRawPeek(e, shulkerPeekClosed) // stop(): setRawPeekAmount(0)
	}
}

// shulkerFireInRange ports ShulkerAttackGoal.tick's fire gate: distanceToSqr(target) < 400.0 (Euclidean
// distance < 20). Cite Shulker$ShulkerAttackGoal.tick (ldc2_w 400.0d; dcmpg; iflt).
func (t *TickLoop) shulkerFireInRange(e *Entity, p *tickPlayer) bool {
	return distanceToSqrPlayer(p, e) < shulkerFireDistSqr
}

// shulkerAcquireTarget scans for the nearest live player in range every scanCadence ticks. RNG-free.
// Cite Shulker.registerGoals targetSelector (NearestAttackableTargetGoal Player).
func (t *TickLoop) shulkerAcquireTarget(e *Entity) {
	if e.ai == nil {
		return
	}
	s := e.shulker
	// Acquisition uses the ShulkerNearestAttackGoal's FOLLOW_RANGE (createMobAttributes default 16.0), NOT
	// the (removed) per-axis 15-block gate. The ATTACK goal has no range gate on canUse; the fire range
	// (distanceToSqr < 400) is applied in shulkerAttackGoal. Cite Shulker$ShulkerNearestAttackGoal
	// (NearestAttackableTargetGoal, FOLLOW_RANGE-bounded) + Mob.createMobAttributes FOLLOW_RANGE 16.
	followRange := e.getAttributeValue(attribute.FollowRange) // 16.0
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p != nil && !p.dead && distanceToSqrPlayer(p, e) <= rangeSqr {
			return
		}
		e.ai.attackTargetID = 0
	}
	if s.nextScanTick > 0 {
		s.nextScanTick--
		return
	}
	s.nextScanTick = int32(reducedTickDelay(shulkerScanCadence))
	var best *tickPlayer
	bestSq := rangeSqr
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq <= bestSq {
			bestSq = dsq
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID
	}
}

// shulkerAttackGoal ports Shulker$ShulkerAttackGoal.tick: attackTime--; at <=0 arm attackTime =
// 20 + nextInt(10)*20/2 (bipush 20; nextInt(10); bipush 20; imul; iconst_2; idiv; iadd) + spawn a
// homing ShulkerBullet. Cite Shulker$ShulkerAttackGoal.tick.
func (t *TickLoop) shulkerAttackGoal(e *Entity, target *tickPlayer) {
	s := e.shulker
	// tick(): attackTime-- (unconditional decrement, matching `attackTime = attackTime - 1`); the LookControl
	// setLookAt(180,180) is a cite-deferred cosmetic. The bullet fires only when distanceToSqr(target) < 400.0
	// AND attackTime <= 0, then re-arms attackTime = 20 + nextInt(10)*20/2. Cite Shulker$ShulkerAttackGoal.tick.
	s.attackTime--
	if !t.shulkerFireInRange(e, target) { // distanceToSqr(target) >= 400.0 (Euclidean >= 20)
		// tick else branch (bytecode 189-194): setTarget(null) -- the shulker DROPS a target that has fled
		// beyond 20 blocks (the shell then re-closes next tick via the no-target branch). Cite
		// Shulker$ShulkerAttackGoal.tick offsets 64-194.
		e.ai.attackTargetID = 0
		return
	}
	if s.attackTime > 0 { // ifgt: only fire when the cooldown has elapsed
		return
	}
	s.attackTime = int32(shulkerAttackBaseTicks + int(mobRandom(e).nextInt(shulkerAttackJitter))*shulkerAttackBaseTicks/2)
	t.spawnShulkerBullet(e, target)
	// playSound(SHULKER_SHOOT, 2.0F, (nextFloat() - nextFloat()) * 0.2F + 1.0F): the sound is a cited
	// client-cue deferral, but its TWO nextFloat() pitch draws are on the shulker's own RNG stream and
	// are observable via draw order -- consume them so the stream stays in lockstep. Cite
	// Shulker$ShulkerAttackGoal.tick offsets 157-172 (nextFloat - nextFloat).
	mobRandom(e).nextFloat()
	mobRandom(e).nextFloat()
}

// spawnShulkerBullet ports new ShulkerBullet(level, this, target, attachFace.getAxis()) + addFreshEntity.
// Cite Shulker$ShulkerAttackGoal.tick + ShulkerBullet ctor.
func (t *TickLoop) spawnShulkerBullet(e *Entity, target *tickPlayer) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.ShulkerBullet, e.x, e.y+0.5, e.z)
	b.shulkerBullet = &shulkerBulletState{
		ownerID:  e.id,
		targetID: target.entityID,
	}
	b.ai = &mobAI{}
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// shulkerBulletIsFlyer reports whether an entity is a ShulkerBullet (tickPhysics NO-gravity gate).
// Cite ShulkerBullet ctor (isNoGravity() == true).
func shulkerBulletIsFlyer(e *Entity) bool { return e.shulkerBullet != nil }

// shulkerBulletTick ports the ShulkerBullet flight: home toward the target; on contact apply 4.0
// mob-projectile damage + a 200-tick LEVITATION via the EXISTING addPlayerEffect. Despawns on hit,
// on a lost target, or after the max-life guard. The selectNextMoveDirection axis walk is a
// cite-deferred refinement; the ON-HIT effect is bytecode-exact. Cite ShulkerBullet.tick + onHitEntity.
func (t *TickLoop) shulkerBulletTick(e *Entity) {
	if e.dead {
		return
	}
	b := e.shulkerBullet
	if b == nil {
		return
	}
	b.life++
	if b.life > shulkerBulletMaxLife {
		t.regionForEntity(e).entities.remove(e.id)
		return
	}
	target := t.playerByEntityID(b.targetID)
	if target == nil || target.dead {
		t.regionForEntity(e).entities.remove(e.id)
		return
	}
	dx := target.x - e.x
	dy := (target.y + 0.5*playerHeight) - e.y
	dz := target.z - e.z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist > 1e-4 {
		e.vx = dx / dist * shulkerBulletSpeed
		e.vy = dy / dist * shulkerBulletSpeed
		e.vz = dz / dist * shulkerBulletSpeed
	}
	nx, ny, nz := e.x+e.vx, e.y+e.vy, e.z+e.vz
	t.regionForEntity(e).entities.move(e, nx, ny, nz)
	ddx := target.x - e.x
	ddy := (target.y + 0.5*playerHeight) - e.y
	ddz := target.z - e.z
	if ddx*ddx+ddy*ddy+ddz*ddz <= shulkerBulletHitDistSqr {
		t.shulkerBulletHit(e, target)
	}
}

// shulkerBulletHit ports ShulkerBullet.onHitEntity: hurtOrSimulate(mobProjectile(owner),4.0f); on a
// LANDED hit add LEVITATION 200 via the EXISTING addPlayerEffect (mob_effect.go CALLED, not edited).
// Cite ShulkerBullet.onHitEntity.
func (t *TickLoop) shulkerBulletHit(e *Entity, target *tickPlayer) {
	b := e.shulkerBullet
	src := damageSourceMobAttack(b.ownerID)
	t.applyDamage(target, src, shulkerBulletDamage)
	t.addPlayerEffect(target, b.ownerID, effectLevitation, shulkerBulletLevitTicks, shulkerBulletLevitAmp, 1.0)
	t.regionForEntity(e).entities.remove(e.id)
}

// shulkerTeleportSomewhere ports Shulker.teleportSomewhere(): 5 tries, each axis
// Mth.randomBetweenInclusive(rng,-8,8), require empty + above minY + an attachable neighbor.
// Cite Shulker.teleportSomewhere.
func (t *TickLoop) shulkerTeleportSomewhere(e *Entity) bool {
	s := e.shulker
	if s == nil {
		return false
	}
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	r := mobRandom(e)
	for i := 0; i < shulkerTeleportTries; i++ {
		nx := bx + shulkerRandomBetweenInclusive(r, -shulkerTeleportSpan, shulkerTeleportSpan)
		ny := by + shulkerRandomBetweenInclusive(r, -shulkerTeleportSpan, shulkerTeleportSpan)
		nz := bz + shulkerRandomBetweenInclusive(r, -shulkerTeleportSpan, shulkerTeleportSpan)
		if ny <= dimEndMinY {
			continue
		}
		if t.isSolidAt(blockPosOf(float64(nx), float64(ny), float64(nz))) {
			continue
		}
		face, ok := t.shulkerFindAttachableSurface(nx, ny, nz)
		if !ok {
			continue
		}
		s.attachFace = face
		t.regionForEntity(e).entities.move(e, float64(nx)+0.5, float64(ny), float64(nz)+0.5)
		return true
	}
	return false
}

// shulkerArrowImmune ports the CLOSED arrow-immunity gate at the TOP of Shulker.hurtServer: a closed shulker
// (isClosed() == getRawPeekAmount()==0) hit by an AbstractArrow (source.getDirectEntity() instanceof
// AbstractArrow) returns false -- the +20 covered armor is impenetrable to arrows while boxed up. Returns
// true when the hit must be REJECTED (closed + arrow directEntity). A wired combat path calls this before the
// shared pipeline (the sibling of the wither/guardian pre-hurt gates). Cite Shulker.hurtServer offsets 0-22.
func shulkerArrowImmune(e *Entity, src damageSource) bool {
	if !shulkerIsClosed(e) {
		return false
	}
	return src.typeTag == damageTypeArrow // getDirectEntity() instanceof AbstractArrow
}

// shulkerHurtServerReaction ports the POST-hurt tail of Shulker.hurtServer (run AFTER super.hurtServer
// succeeded): a shulker whose health has dropped BELOW 50% of its max (getHealth() < getMaxHealth()*0.5)
// rolls nextInt(4)==0 and, on a hit, teleportSomewhere() (it bolts to a new attachable spot). Otherwise, if
// the source is_projectile AND its directEntity is a SHULKER_BULLET, hitByShulkerBullet() (a self-heal-cancel
// cited-deferred visual). The RNG draw is on the shulker's OWN stream. This wires the previously-orphaned
// shulkerTeleportSomewhere. A wired combat path calls this after the hit lands (the sibling of
// endermanHurtTeleport). Cite Shulker.hurtServer offsets 30-108.
func (t *TickLoop) shulkerHurtServerReaction(e *Entity, src damageSource) {
	if e.shulker == nil || e.dead || e.health <= 0 {
		return
	}
	// getHealth() < getMaxHealth()*0.5 (dcmpg; iflt): the float-widened half-max compare (f2d on both sides).
	maxH := e.getAttributeValue(attribute.MaxHealth)
	if float64(e.health) < maxH*0.5 {
		if mobRandom(e).nextInt(4) == 0 { // nextInt(4)==0 (ifne skips): a 1-in-4 bolt
			t.shulkerTeleportSomewhere(e)
		}
		return // the low-HP branch is exclusive of the shulker-bullet branch (goto 108)
	}
	// else if (source.is(IS_PROJECTILE) && directEntity.is(SHULKER_BULLET)) hitByShulkerBullet(): the bullet
	// self-hit reaction. hitByShulkerBullet is a cited-deferred client visual (the peek/color flash); no
	// gameplay state changes, so this is a documented no-op seam. Cite Shulker.hurtServer offsets 72-104.
	_ = src
}

// shulkerFindAttachableSurface ports Shulker.findAttachableSurface: FIRST solid neighbor in
// Direction.values() order (DOWN,UP,NORTH,SOUTH,WEST,EAST). Cite Shulker.findAttachableSurface.
func (t *TickLoop) shulkerFindAttachableSurface(x, y, z int) (block.Direction, bool) {
	type nb struct {
		dir        block.Direction
		dx, dy, dz int
	}
	for _, n := range []nb{
		{block.Down, 0, -1, 0},
		{block.Up, 0, 1, 0},
		{block.North, 0, 0, -1},
		{block.South, 0, 0, 1},
		{block.West, -1, 0, 0},
		{block.East, 1, 0, 0},
	} {
		if t.isSolidAt(blockPosOf(float64(x+n.dx), float64(y+n.dy), float64(z+n.dz))) {
			return n.dir, true
		}
	}
	return block.Down, false
}

// shulkerRandomBetweenInclusive ports Mth.randomBetweenInclusive(rng, min, max) = min + nextInt(max-min+1).
func shulkerRandomBetweenInclusive(r *entityRandom, lo, hi int) int {
	return lo + int(r.nextInt(hi-lo+1))
}
