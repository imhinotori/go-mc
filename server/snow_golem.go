// snow_golem.go -- the SnowGolem (net.minecraft.world.entity.animal.golem.SnowGolem), a 1:1 port from
// the unobfuscated 26.2 jar. The Snow Golem is a player-built (two snow blocks + a carved pumpkin)
// AbstractGolem that leaves a TRAIL OF SNOW on the ground as it walks, throws SNOWBALLS at nearest
// hostile mobs (a RangedAttackMob), MELTS (takes 1 on_fire damage/tick) in a warm biome or when wet, and
// can be SHEARED to remove its pumpkin. This port lands the attributes + spawn + the SIGNATURE snow-trail
// place (the aiStep block-leaving loop) + the shear-pumpkin state + the melt hook + the SNOWBALL RANGED
// ATTACK (RangedAttackGoal @1 + NearestAttackableTargetGoal<Enemy> @1 + performRangedAttack spawning a
// Snowball throwable that hits its Enemy MOB target: 3 to a blaze, 0 else, + the hit knockback). The
// melt-in-warm-biome env-attribute read is a cited const-false hook until the EnvironmentAttributes
// SNOW_GOLEM_MELTS subsystem lands. Code-spawned (spawnSnowGolem) with a *mobAI
// carrying the golem goals; its per-tick extra is snowGolemAiStep from tickAI (typ == entity.SnowGolem.ID).
//
// VANILLA (verified javap SnowGolem this session):
//   createAttributes: Mob.createMobAttributes + MAX_HEALTH 4.0 + MOVEMENT_SPEED 0.20000000298023224
//     (AbstractGolem has NO createAttributes override). NOT Animal/Monster -> no TEMPT_RANGE / base
//     ATTACK_DAMAGE. See snowGolemSupplier.
//   registerGoals: @1 RangedAttackGoal(1.25, 20, 10.0f); @2 WaterAvoidingRandomStrollGoal(1.0, 1.0E-5f);
//     @3 LookAtPlayerGoal(Player, 6.0f); @4 RandomLookAroundGoal. target @1 NearestAttackableTargetGoal(
//     Mob, 10, true, false, Enemy predicate).
//   aiStep(): AbstractGolem.aiStep; if ServerLevel: if(SNOW_GOLEM_MELTS at pos) hurtServer(onFire(),1.0f);
//     if(!MOB_GRIEFING) return; snow=Blocks.SNOW.defaultBlockState(); for i in 0..3: x=floor(getX()+(i%2*
//     2-1)*0.25); y=floor(getY()); z=floor(getZ()+(i/2%2*2-1)*0.25); pos=BlockPos(x,y,z); if(getBlockState
//     (pos).isAir() && snow.canSurvive(level,pos)){ setBlockAndUpdate(pos,snow); gameEvent(BLOCK_PLACE); }.
//   performRangedAttack(target, f): spawn a Snowball aimed at target. (DEFERRED -- projectile subsystem.)
//   shear/readyForShearing/hasPumpkin/setPumpkin: DATA_PUMPKIN_ID (default true); shear clears it + drops
//     a carved pumpkin; readyForShearing == isAlive() && hasPumpkin().
//
// v1 STUBS (cited): the melt-in-warm-biome branch is a cited const-false hook (snowGolemMelts) until the
// SNOW_GOLEM_MELTS read lands -- when it does, the onFire self-damage fires at this exact seam. The
// pumpkin-drop loot is DEFERRED. The snowball hit KNOCKBACK direction rides the shared
// dealDefaultKnockbackEntity (0.4 magnitude exact; the projectile-source DIRECTION is the nudge stub until
// that shared helper gains a projectile source-position branch -- see snowballOnHitMob). The Enemy target
// selector uses the categoryMonster proxy for `instanceof Enemy` (a Blaze, categorized MISC today, is not
// auto-acquired though the blaze-3 hit path is fully wired -- see the target-class PROXY NOTE). The
// observable attributes, the snow-trail place, the pumpkin/shear, the fire cadence + shoot math, and the
// blaze-3 / else-0 damage are EXACT.

package server

import (
	"math"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/tag"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// SnowGolem constants (VERIFIED javap SnowGolem this session).
const (
	snowGolemMaxHealth     = 4.0                 // createAttributes MAX_HEALTH 4.0
	snowGolemMovementSpeed = 0.20000000298023224 // createAttributes MOVEMENT_SPEED (float-widened)
	snowGolemRangedSpeed   = 1.25                // @1 RangedAttackGoal speedModifier 1.25
	snowGolemRangedInt     = 20                  // @1 RangedAttackGoal attackInterval 20
	snowGolemRangedRadius  = 10.0                // @1 RangedAttackGoal attackRadius 10.0f
	snowGolemStrollSpeed   = 1.0                 // @2 WaterAvoidingRandomStrollGoal speed 1.0
	snowGolemStrollProb    = 1.0000001e-5        // @2 WaterAvoidingRandomStrollGoal probability 1.0E-5f
	snowGolemLookDistance  = 6.0                 // @3 LookAtPlayerGoal distance 6.0f
	snowGolemTrailOffset   = 0.25                // aiStep snow-trail offset scale (0.25f)

	snowGolemRangedRadiusSqr = snowGolemRangedRadius * snowGolemRangedRadius // 10.0f^2 == 100
	// snowballLaunchVelocity / snowballInaccuracy are the SnowGolem.performRangedAttack shoot(...) args:
	// snowball.shoot(dx, dy + Mth.sqrt(hd*hd)*0.2, dz, 1.6f, 12.0f). VERIFIED javap lambda$performRangedAttack$0
	// offsets 15-21: ldc 1.6f; ldc 12.0f; Snowball.shoot(DDDFF).
	snowballLaunchVelocity = 1.6
	snowballInaccuracy     = 12.0
	// snowGolemAimEyeDrop is the vertical aim anchor: target.getEyeY() - 1.100000023841858. VERIFIED javap
	// SnowGolem.performRangedAttack offsets 11-18: LivingEntity.getEyeY(); ldc2_w 1.100000023841858; dsub.
	snowGolemAimEyeDrop = 1.100000023841858
	// snowballBlazeDamage is Snowball.onHitEntity: (entity instanceof Blaze ? 3 : 0). VERIFIED javap
	// Snowball.onHitEntity offsets 10-22: instanceof Blaze ? iconst_3 : iconst_0.
	snowballBlazeDamage = 3.0
	snowballDefaultDamage = 0.0
	// snowGolemShootSoundID is SoundEvents.SNOW_GOLEM_SHOOT ("entity.snow_golem.shoot", soundid 1573). The
	// shoot sound pitch = 1.0f / (getRandom().nextFloat()*0.4f + 0.8f). VERIFIED javap SnowGolem
	// .performRangedAttack tail offsets 114-139.
	snowGolemShootSoundID   = 1573
	snowGolemShootVolume    = 1.0
	snowGolemShootPitchScale = 0.4
	snowGolemShootPitchBase  = 0.8
)

// damageTypeThrown is minecraft:thrown -- the source Snowball.onHitEntity deals via
// damageSources().thrown(this, getOwner()) (DamageTypes.THROWN, id 45 in the 26.2 tag table). It carries
// the golem as the causing entity (getOwner()). CITE: DamageSources.thrown + Snowball.onHitEntity.
var damageTypeThrown = damageTypeID(tag.DamageTypeIDs["minecraft:thrown"])

// damageSourceThrown builds the DamageSource for a Snowball hit: type thrown with the SHOOTER's (the snow
// golem's) entity id as the causing entity. The port of DamageSources.thrown(Entity direct, Entity
// causing) -- causingEntity = getOwner() (the golem). ownerID 0 == an ownerless snowball. CITE:
// Snowball.onHitEntity: entity.hurt(damageSources().thrown(this, getOwner()), i).
func damageSourceThrown(ownerID int32) damageSource {
	return damageSource{typeTag: damageTypeThrown, attacker: ownerID}
}

// snowGolemMelts is the cited const-false hook for SnowGolem.aiStep melt branch: vanilla reads
// environmentAttributes.SNOW_GOLEM_MELTS at the golem position (a warm-biome / wet predicate) and, when
// true, deals 1 on_fire self-damage per tick. The EnvironmentAttributes subsystem is not yet ported, so
// this is a faithful const-false stub (a snow golem in a cold biome never melts) -- structured so the melt
// self-damage fires at the exact aiStep seam once the env-attribute read lands, never baked away. Cite
// SnowGolem.aiStep (environmentAttributes.SNOW_GOLEM_MELTS).
func (t *TickLoop) snowGolemMelts(e *Entity) bool { return false }

// newSnowGolemAI builds the SnowGolem goal AI 1:1 with SnowGolem.registerGoals. The golem is a
// RangedAttackMob: goalSelector @1 RangedAttackGoal(this, 1.25, 20, 10.0f), @2 WaterAvoidingRandomStroll
// (1.0), @3 LookAtPlayer(6.0), @4 RandomLookAround; targetSelector @1 NearestAttackableTargetGoal<Mob>
// (this, Mob.class, 10, true, false, target instanceof Enemy). The ranged snowball attack fires via
// performSnowGolemRangedAttack on the 20-tick cadence. Cite SnowGolem.registerGoals.
func newSnowGolemAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * snowGolemMovementSpeed // seed with MOVEMENT_SPEED (0.2)
	m.navigation.canFloat = true                                 // golem navigation floats (Swim-style)
	// goalSelector @1 RangedAttackGoal(this, 1.25, 20, 10.0f): the snowball throw. Placed at @1 exactly as
	// the jar (it out-prioritizes the stroll so a golem with an Enemy target stops wandering and fires).
	m.goals.addGoal(1, newSnowGolemRangedAttackGoal())
	m.goals.addGoal(2, newWaterAvoidingRandomStrollGoal(snowGolemStrollSpeed))
	m.goals.addGoal(3, newLookAtPlayerGoal(snowGolemLookDistance))
	m.goals.addGoal(4, newRandomLookAroundGoal())
	// targetSelector @1 NearestAttackableTargetGoal<Mob>(this, Mob.class, 10, true, false, Enemy): the
	// golem acquires the nearest Enemy (Monster-category) mob within FOLLOW_RANGE, creepers INCLUDED.
	m.targetSelector.addGoal(1, newSnowGolemEnemyTargetGoal())
	return m
}

// spawnSnowGolem creates a SnowGolem at (x,y,z) with the jar attributes and the golem AI, then adds it to
// the owner region store. hasPumpkin defaults TRUE (DATA_PUMPKIN_ID). initSpawnHealth seeds health from
// MAX_HEALTH (4.0). Cite SnowGolem.createAttributes + SnowGolem(EntityType, Level) (setPumpkin(true)).
func (t *TickLoop) spawnSnowGolem(x, y, z float64) *Entity {
	g := NewEntity(t.idAlloc.AllocID(), entity.SnowGolem, x, y, z)
	g.isSnowGolem = true
	g.snowGolemPumpkin = true // DATA_PUMPKIN_ID default true (a freshly built golem wears its pumpkin)
	initSpawnHealth(g)        // setHealth(getMaxHealth()) -> 4.0
	g.ai = newSnowGolemAI()
	reseedMobAI(g.ai, g.id)
	owner := t.regionForEntity(g)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(g)
	return g
}

// snowGolemShear ports SnowGolem.shear / readyForShearing: readyForShearing == isAlive() && hasPumpkin();
// shear clears the pumpkin (setPumpkin(false)) and (DEFERRED) drops a carved pumpkin + plays the shear
// sound. Returns true if the shear was applied. Cite SnowGolem.shear + readyForShearing + hasPumpkin.
func (t *TickLoop) snowGolemShear(e *Entity) bool {
	if e.dead || e.health <= 0 || !e.snowGolemPumpkin {
		return false // readyForShearing: isAlive() && hasPumpkin()
	}
	e.snowGolemPumpkin = false // setPumpkin(false) (the carved-pumpkin drop is DEFERRED loot)
	return true
}

// snowGolemAiStep is the SnowGolem per-tick extra (SnowGolem.aiStep). It ports the melt hook (const-false
// today, snowGolemMelts) and the SIGNATURE snow-trail place: gated on MOB_GRIEFING, for i in 0..3 compute
// the four ground offsets, and where the block is AIR with a solid block below (the canSurvive analogue),
// place a snow layer + broadcast the update. Per-type-gated (typ == entity.SnowGolem.ID) AFTER serverAi
// Step. ADDITIVE + golem-gated (zero cost / zero RNG for every non-golem -- the pig oracle stream is
// untouched). Cite SnowGolem.aiStep.
func (t *TickLoop) snowGolemAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// Melt branch: if(SNOW_GOLEM_MELTS at pos) hurtServer(onFire(), 1.0f). const-false hook today.
	if t.snowGolemMelts(e) {
		t.applyDamageEntity(e, damageSourceOf(damageTypeOnFire), 1.0)
		if e.dead || e.health <= 0 {
			return
		}
	}
	// if(!MOB_GRIEFING) return -- a no-griefing golem leaves no trail.
	if !t.gameRule(ruleMobGriefing) {
		return
	}
	if t.world() == nil {
		return
	}
	snow, ok := block.DefaultStateID["minecraft:snow"] // Blocks.SNOW.defaultBlockState()
	if !ok {
		return
	}
	// for i in 0..3: the four snow-trail offsets around the golem feet (vanilla (i%2*2-1)*0.25 and
	// (i/2%2*2-1)*0.25 X/Z offsets, floored). Place a snow layer where AIR with a solid support below.
	for i := 0; i < 4; i++ {
		xf := e.x + float64((i%2)*2-1)*snowGolemTrailOffset
		zf := e.z + float64((i/2%2)*2-1)*snowGolemTrailOffset
		bx := mthFloor(xf)
		by := mthFloor(e.y)
		bz := mthFloor(zf)
		// isAir() (blockStateAt == 0 is air, per the enderman carry port) && canSurvive (a solid support
		// below -- SnowLayerBlock.canSurvive requires a sturdy face below; the bounded solid-below check).
		if t.blockStateAt(bx, by, bz) != 0 {
			continue
		}
		if !t.blockSolidAt(bx, by-1, bz) {
			continue
		}
		pos := pk.Position{X: bx, Y: by, Z: bz}
		prePlace, _ := t.world().GetBlock(pos, dimMinY)
		if t.world().SetBlock(pos, snow, dimMinY) {
			t.broadcastBlockUpdate(pos, snow)
			t.relightOnEdit(nil, pos, prePlace, snow) // LevelChunk.setBlockState light hook
			// gameEvent(GameEvent.BLOCK_PLACE, ...): CITE-DEFERRED no-op.
		}
	}
}

// --- SNOW GOLEM RANGED ATTACK (RangedAttackGoal + performRangedAttack + Snowball onHitEntity) ----------

// snowGolemRangedAttackGoal ports net.minecraft.world.entity.ai.goal.RangedAttackGoal (flags {MOVE,
// LOOK}, requiresUpdateEveryTick) as the SnowGolem builds it: RangedAttackGoal(this, 1.25, 20, 10.0f) --
// the 4-arg ctor, so attackIntervalMin == attackIntervalMax == 20 (the fire cadence is a FIXED 20 ticks).
// The golem's target is an Enemy MOB (not a player), so tick resolves getTarget() as an entity victim in
// the region store and runs the mob-victim shape. Cite SnowGolem.registerGoals @1 + RangedAttackGoal.
type snowGolemRangedAttackGoal struct {
	baseGoal
	attackTime int // RangedAttackGoal.attackTime (start -1)
	seeTime    int // RangedAttackGoal.seeTime
}

// newSnowGolemRangedAttackGoal builds the golem's RangedAttackGoal with the vanilla -1 attackTime start.
func newSnowGolemRangedAttackGoal() *snowGolemRangedAttackGoal {
	return &snowGolemRangedAttackGoal{baseGoal: newBaseGoal(flagMove | flagLook), attackTime: -1}
}

func (g *snowGolemRangedAttackGoal) requiresUpdateEveryTick() bool { return true }

// snowGolemTargetVictim resolves the golem's current target id to a live MOB victim in the owning region
// store (getTarget() is a LivingEntity; the golem's target is always an Enemy mob). nil if unresolved.
func (t *TickLoop) snowGolemTargetVictim(e *Entity) *Entity {
	id := mobTarget(e)
	if id == 0 {
		return nil
	}
	if victim, ok := t.cur().entities.get(id); ok && !victim.dead && victim.isAlive() {
		return victim
	}
	return nil
}

// canUse is RangedAttackGoal.canUse: target != null && target.isAlive(). The golem's target is a mob.
func (g *snowGolemRangedAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	return t.snowGolemTargetVictim(e) != nil
}

// canContinueToUse is RangedAttackGoal.canContinueToUse: canUse() || (target alive && !navigation.isDone()).
func (g *snowGolemRangedAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	return g.canUse(t, e) || e.ai.navigation.active()
}

// start: RangedAttackGoal has no start() override (Goal.start is empty).
func (g *snowGolemRangedAttackGoal) start(t *TickLoop, e *Entity) {}

// stop: RangedAttackGoal.stop: target=null; seeTime=0; attackTime=-1. Clear the nav want too (the nav
// stop the tick would otherwise leave a stale path once the goal drops). Cite RangedAttackGoal.stop.
func (g *snowGolemRangedAttackGoal) stop(t *TickLoop, e *Entity) {
	g.seeTime = 0
	g.attackTime = -1
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// tick is the port of RangedAttackGoal.tick for the golem's MOB victim. It manages seeTime, the
// approach/stop nav decision, the head look, and the fire-on-interval (performSnowGolemRangedAttack at
// attackTime==0 with line-of-sight, then re-arm attackTime). Cite RangedAttackGoal.tick.
func (g *snowGolemRangedAttackGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	victim := t.snowGolemTargetVictim(e)
	if victim == nil {
		return
	}

	// double d = mob.distanceToSqr(target.getX(), target.getY(), target.getZ()).
	dx0 := victim.x - e.x
	dy0 := victim.y - e.y
	dz0 := victim.z - e.z
	targetDistSqr := dx0*dx0 + dy0*dy0 + dz0*dz0

	// boolean flag = getSensing().hasLineOfSight(target). seeTime = flag ? seeTime+1 : 0.
	hasLineOfSight := t.sensingHasLineOfSightEntity(e, victim)
	if hasLineOfSight {
		g.seeTime++
	} else {
		g.seeTime = 0
	}

	// if (!(d > attackRadiusSqr) && seeTime >= 5) navigation.stop(); else navigation.moveTo(target, speed).
	if targetDistSqr > snowGolemRangedRadiusSqr || g.seeTime < 5 {
		getSpeed := e.getAttributeValue(attribute.MovementSpeed) * snowGolemRangedSpeed
		e.ai.setWantTargetSpeed(victim.x, victim.y, victim.z, getSpeed) // navigation.moveTo(target, speedModifier)
	} else {
		e.ai.clearWantTarget() // navigation.stop()
	}

	// getLookControl().setLookAt(target, 30, 30): head-only turn toward the victim.
	yRotD := yawTowardDeg(victim.x-e.x, victim.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, meleeLookMaxYawStep)

	// if (--attackTime == 0) { if (!flag) return; float f = sqrt(d)/attackRadius; performRangedAttack(
	// target, clamp(f, 0.1, 1.0)); attackTime = floor(f*(max-min)+min); } else if (attackTime < 0)
	// attackTime = floor(lerp(sqrt(d)/attackRadius, min, max)). min==max==20, so both re-arms give 20.
	g.attackTime--
	if g.attackTime == 0 {
		if !hasLineOfSight {
			return
		}
		f := math.Sqrt(targetDistSqr) / snowGolemRangedRadius
		power := f
		if power < 0.1 {
			power = 0.1
		} else if power > 1.0 {
			power = 1.0
		}
		t.performSnowGolemRangedAttack(e, victim, power)
		g.attackTime = snowGolemRangedInt // floor(f*(20-20)+20) == 20
	} else if g.attackTime < 0 {
		g.attackTime = snowGolemRangedInt // floor(lerp(., 20, 20)) == 20
	}
}

// performSnowGolemRangedAttack is the port of SnowGolem.performRangedAttack(LivingEntity, float): spawn a
// Snowball at the golem aimed at the target, then play the shoot sound. The aim + shoot velocity are the
// EXACT jar math:
//
//	double dx = target.getX() - getX();
//	double dy = target.getEyeY() - 1.100000023841858 - snowball.getY();   // snowball.getY() == golem eye Y
//	double dz = target.getZ() - getZ();
//	double hd = Math.sqrt(dx*dx + dz*dz) * 0.20000000298023224;           // the horizontal-dist lob term
//	snowball.shoot(dx, dy + Mth.sqrt(hd*hd)*0.2, dz, 1.6f, 12.0f);
//
// NOTE: in the jar `dy` is captured relative to the snowball's spawn Y (getEyeY of the golem, where the
// throwable-item ctor places it), and the lambda re-reads snowball.getY(); v1 spawns the snowball at the
// golem eye Y and computes dy against that same origin, so the arithmetic is identical. Mth.sqrt(hd*hd)
// == |hd| (hd >= 0), i.e. the lob adds hd*0.2 to dy exactly as vanilla. The `power` arg is UNUSED by
// SnowGolem.performRangedAttack (verified javap -- it takes (LivingEntity, float) but never reads the
// float; the fixed 1.6 velocity is used). Cite SnowGolem.performRangedAttack + lambda$performRangedAttack$0.
func (t *TickLoop) performSnowGolemRangedAttack(e *Entity, victim *Entity, power float64) {
	_ = power // SnowGolem.performRangedAttack ignores the RangedAttackMob power arg (fixed 1.6 velocity).

	// The Snowball ctor (ThrowableItemProjectile(Level, LivingEntity)) spawns it at the golem's eye Y:
	// getEyeY() == y + (float)(height*0.85f). This is the snowball.getY() the lambda subtracts.
	launchX := e.x
	launchY := e.y + float64(float32(e.height)*snowGolemEyeHeightFactor)
	launchZ := e.z

	dx := victim.x - e.x
	// target.getEyeY() == victim.y + (float)(victim.height*0.85f); minus 1.1; minus snowball.getY().
	targetEyeY := victim.y + float64(float32(victim.height)*snowGolemEyeHeightFactor)
	dy := targetEyeY - snowGolemAimEyeDrop - launchY
	dz := victim.z - e.z

	// hd = sqrt(dx*dx + dz*dz) * 0.2; the lob adds Mth.sqrt(hd*hd)*0.2 == |hd|*0.2 to dy.
	hd := math.Sqrt(dx*dx+dz*dz) * snowGolemMovementSpeed // the 0.20000000298023224 literal == MOVEMENT_SPEED
	lobY := dy + math.Sqrt(hd*hd)*0.2

	// Projectile.shoot(x, y, z, velocity 1.6f, inaccuracy 12.0f): normalize(dir) + triangle spread *
	// (0.0172275 * inaccuracy) per axis on the snowball's OWN rng, scaled by velocity. The snowball is a
	// fresh entity with its own stream, so the golem's per-entity rng is untouched (only the shoot-sound
	// pitch below draws on the golem stream). Cite Projectile.shoot + getMovementToShoot.
	sb := t.spawnThrowable(e.id, throwSnowball, launchX, launchY, launchZ, 0, 0, 0)
	sb.snowballHitsMobs = true // opt this golem snowball into the additive mob-victim scan (tickThrowable)
	if sb.throwRNG == nil {
		sb.throwRNG = newEntityRandom(uint64(sb.id))
	}
	r := sb.throwRNG
	vx, vy, vz := normalizeVec3(dx, lobY, dz)
	spread := 0.0172275 * snowballInaccuracy
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	vx *= snowballLaunchVelocity
	vy *= snowballLaunchVelocity
	vz *= snowballLaunchVelocity
	sb.vx, sb.vy, sb.vz = vx, vy, vz
	// Re-seed the render angles from the actual launch vector (spawnThrowable seeded them from the 0,0,0
	// passed above; recompute now that the real velocity is set).
	horiz := math.Sqrt(vx*vx + vz*vz)
	sb.yaw = float32(math.Atan2(vx, vz) * 180.0 / math.Pi)
	sb.pitch = float32(math.Atan2(vy, horiz) * 180.0 / math.Pi)
	sb.headYaw = sb.yaw

	// playSound(SNOW_GOLEM_SHOOT, 1.0f, 1.0f / (getRandom().nextFloat()*0.4f + 0.8f)). getSoundSource()
	// == NEUTRAL (SnowGolem is not a Monster). The nextFloat() is ONE draw on the GOLEM's per-entity rng
	// (mobRandom), AFTER the snowball spawn -- it is golem-gated (a non-golem never reaches here, the pig
	// oracle is untouched). The soundSeedGenerator.nextLong() analogue seed is a dedicated non-gameplay
	// draw so it never perturbs the golem stream. Cite SnowGolem.performRangedAttack tail.
	shootPitch := float32(1.0) / (mobRandom(e).nextFloat()*snowGolemShootPitchScale + snowGolemShootPitchBase)
	t.broadcastToTrackers(e.id, encodeSoundEntity(snowGolemShootSoundID, soundSourceNeutral, e.id, snowGolemShootVolume, shootPitch, rand.Int64()))
}

// snowGolemEyeHeightFactor is EntityDimensions.defaultEyeHeight (height * 0.85f) -- the getEyeY() factor
// the snowball ctor + the aim read. Cite EntityDimensions.defaultEyeHeight.
const snowGolemEyeHeightFactor = 0.85

// snowballFindHitMobVictim is the ProjectileUtil.getEntityHitResult port for a snow-golem snowball scoped
// to MOB victims: the FIRST live mob (isLivingMob) whose collision AABB (inflated by the snowball's
// half-size) the flight segment (origin->next) crosses, excluding the shooting golem (throwOwnerID). The
// mob-victim analog of projectileFindHitPlayer -- the geometry is identical, only the candidate set is the
// region entity store instead of t.players. Cite ProjectileUtil.getEntityHitResult / getHitResultOnMoveVector.
func (t *TickLoop) snowballFindHitMobVictim(e *Entity, ox, oy, oz, nx, ny, nz float64) *Entity {
	var best *Entity
	bestT := math.Inf(1)
	half := entity.Snowball.Width / 2.0
	for _, other := range t.cur().entities.all() {
		if other == nil || other == e || other.dead || !other.isAlive() {
			continue
		}
		if other.id == e.throwOwnerID {
			continue // never hits its own shooter (checkLeftOwner guard)
		}
		if !isLivingMob(other) {
			continue // Snowball.onHitEntity applies to a LivingEntity victim only
		}
		vhw := other.width / 2
		minX := other.x - vhw - half
		maxX := other.x + vhw + half
		minY := other.y - half
		maxY := other.y + other.height + half
		minZ := other.z - vhw - half
		maxZ := other.z + vhw + half
		if hit, tHit := segmentAABB(ox, oy, oz, nx, ny, nz, minX, minY, minZ, maxX, maxY, maxZ); hit {
			if tHit < bestT {
				bestT = tHit
				best = other
			}
		}
	}
	return best
}

// snowballOnHitMob is the port of Snowball.onHitEntity for a MOB victim:
//
//	int i = entity instanceof Blaze ? 3 : 0;
//	entity.hurt(damageSources().thrown(this, getOwner()), (float)i);
//
// A snowball deals 3 to a blaze and 0 to every other mob. The hit's KNOCKBACK rides the shared
// applyDamageEntity -> broadcastMobDamageEvent -> dealDefaultKnockbackEntity recoil (0.4 power), which
// runs on a fresh (tookFullDamage) hit -- including a 0-damage snowball, since dealDefaultKnockback is
// gated on !NO_KNOCKBACK (the `thrown` type is NOT a NO_KNOCKBACK member), NOT on the damage amount.
//
// DIRECTION deferral (cited, not silently dropped): vanilla LivingEntity.dealDefaultKnockback resolves
// getSourcePosition() from the directEntity (the SNOWBALL) and pushes the victim away from the snowball's
// impact position. The shared dealDefaultKnockbackEntity currently resolves a source position only for a
// PLAYER attacker (combat_mob.go, which this file does not own); a golem/projectile source leaves
// xd==zd==0, so the shared path applies knockback in a small RANDOM direction (the xd^2+zd^2<1e-5 nudge
// guard) rather than radially away from the snowball. The MAGNITUDE (0.4) is exact; only the DIRECTION is
// the nudge stub until dealDefaultKnockbackEntity gains the projectile/source-position branch (at which
// point this snowball hit becomes byte-exact with zero change here -- the source already carries the
// golem/thrown attribution). Cite Snowball.onHitEntity + LivingEntity.dealDefaultKnockback.
func (t *TickLoop) snowballOnHitMob(e *Entity, victim *Entity) {
	damage := snowballDefaultDamage
	if victim.typ == entity.Blaze.ID {
		damage = snowballBlazeDamage
	}
	src := damageSourceThrown(e.throwOwnerID)
	t.applyDamageEntity(victim, src, float32(damage))
}
