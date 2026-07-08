// snow_golem.go -- the SnowGolem (net.minecraft.world.entity.animal.golem.SnowGolem), a 1:1 port from
// the unobfuscated 26.2 jar. The Snow Golem is a player-built (two snow blocks + a carved pumpkin)
// AbstractGolem that leaves a TRAIL OF SNOW on the ground as it walks, throws SNOWBALLS at nearest
// hostile mobs (a RangedAttackMob), MELTS (takes 1 on_fire damage/tick) in a warm biome or when wet, and
// can be SHEARED to remove its pumpkin. This port lands the attributes + spawn + the SIGNATURE snow-trail
// place (the aiStep block-leaving loop) + the shear-pumpkin state + the melt hook. The snowball ranged
// attack (spawning a Snowball projectile) is the DEFERRED behavior layer (it needs the throwable-
// projectile subsystem); the melt-in-warm-biome env-attribute read is a cited const-false hook until the
// EnvironmentAttributes SNOW_GOLEM_MELTS subsystem lands. Code-spawned (spawnSnowGolem) with a *mobAI
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
// v1 STUBS (cited): the snowball RangedAttack is DEFERRED (no throwable-projectile subsystem); the melt-
// in-warm-biome branch is a cited const-false hook (snowGolemMelts) until the SNOW_GOLEM_MELTS read lands
// -- when it does, the onFire self-damage fires at this exact seam. The pumpkin-drop loot + shoot sound
// are DEFERRED. The observable attributes, the SIGNATURE snow-trail place, the pumpkin state, and the
// shear are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
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
)

// snowGolemMelts is the cited const-false hook for SnowGolem.aiStep melt branch: vanilla reads
// environmentAttributes.SNOW_GOLEM_MELTS at the golem position (a warm-biome / wet predicate) and, when
// true, deals 1 on_fire self-damage per tick. The EnvironmentAttributes subsystem is not yet ported, so
// this is a faithful const-false stub (a snow golem in a cold biome never melts) -- structured so the melt
// self-damage fires at the exact aiStep seam once the env-attribute read lands, never baked away. Cite
// SnowGolem.aiStep (environmentAttributes.SNOW_GOLEM_MELTS).
func (t *TickLoop) snowGolemMelts(e *Entity) bool { return false }

// newSnowGolemAI builds the SnowGolem goal AI. Vanilla registerGoals is a RangedAttackMob golem: the
// snowball RangedAttackGoal@1 is DEFERRED (projectile subsystem), so this lands the movement/look subset
// (Stroll(1.0)@2, LookAtPlayer(6.0)@3, RandomLook@4), mirroring the passive shape (per-mob rng, navigation
// seed, canFloat). Cite SnowGolem.registerGoals (the ranged deferral note in the header).
func newSnowGolemAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * snowGolemMovementSpeed // seed with MOVEMENT_SPEED (0.2)
	m.navigation.canFloat = true                                 // golem navigation floats (Swim-style)
	m.goals.addGoal(2, newWaterAvoidingRandomStrollGoal(snowGolemStrollSpeed))
	m.goals.addGoal(3, newLookAtPlayerGoal(snowGolemLookDistance))
	m.goals.addGoal(4, newRandomLookAroundGoal())
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
