package server

// fire.go — the entity FIRE subsystem + the mob daylight-burn (isSunBurnTick). PORTED 1:1 from the
// unobfuscated 26.2 jar (Entity.igniteForTicks/baseTick fire block; Mob.isSunBurnTick), read via CFR
// this session.
//
// Fire model: remainingFireTicks (entity.go) counts down each tick; every 20 ticks a burning entity
// takes 1 on_fire damage; the on-fire shared-flag (DATA_SHARED_FLAGS bit 0x01) is broadcast to
// trackers so the client renders flames; water extinguishes it. Daylight burn: a zombie/skeleton in
// open sky during the day ignites for 8s (isSunBurnTick), the classic "mobs burn at dawn" behavior.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fireSharedFlagBit is Entity.FLAG_ONFIRE — DATA_SHARED_FLAGS (index 0) bit 0x01: the client renders
// flames on the entity while set.
//
//	[VERIFIED javap Entity.setSharedFlagOnFire -> setSharedFlag(0, value); FLAG_ONFIRE == 0 (1<<0).]
const fireSharedFlagBit = 0x01

// igniteForSeconds ports Entity.igniteForSeconds(float): set remainingFireTicks to floor(seconds*20),
// but only if that is LARGER than the current value (igniteForTicks never shortens an existing burn).
//
//	[VERIFIED javap Entity.igniteForSeconds(n): igniteForTicks(Mth.floor(n*20)); igniteForTicks(t):
//	 if (remainingFireTicks < t) setRemainingFireTicks(t).]
func (t *TickLoop) igniteForSeconds(e *Entity, seconds float32) {
	ticks := int32(math.Floor(float64(seconds) * 20.0))
	if e.remainingFireTicks < ticks {
		e.remainingFireTicks = ticks
		t.broadcastEntityFireFlag(e) // it just became on-fire → tell trackers to render flames
	}
}

// tickEntityFire ports the fire block of Entity.baseTick for a server-side entity: while burning, deal
// 1 on_fire damage every 20 ticks (unless in lava — lava has its own damage), decrement the counter,
// and keep the on-fire shared-flag in sync. Water/rain extinguishes (remainingFireTicks -> 0). Called
// once per entity per tick from the entity tick phase.
//
//	[VERIFIED javap Entity.baseTick: if (remainingFireTicks>0) { if fireImmune clearFire; else { if
//	 (remainingFireTicks%20==0 && !isInLava) hurtServer(onFire(),1.0); setRemainingFireTicks(-1); } }
//	 ... setSharedFlagOnFire(remainingFireTicks>0). Water extinguish via updateFluidInteraction ->
//	 clearFire when in water.]
func (t *TickLoop) tickEntityFire(e *Entity) {
	if e.remainingFireTicks <= 0 {
		return
	}
	// Extinguish in water (updateInWaterStateAndDoFluidPushing -> clearFire). v1: check the block at the
	// entity's feet is water. Rain is not modeled (no weather); powder snow is not modeled.
	if t.entityInWater(e) {
		e.remainingFireTicks = 0
		t.broadcastEntityFireFlag(e)
		return
	}
	// fireImmune(): a fire-immune mob (MagmaCube/Strider -- nether types) never takes on-fire damage
	// (Entity.baseTick: `if (fireImmune) clearFire`). Clear the burn + the client flag and return.
	if entityFireImmune(e) {
		e.remainingFireTicks = 0
		t.broadcastEntityFireFlag(e)
		return
	}
	if e.remainingFireTicks%20 == 0 {
		t.applyDamageEntity(e, damageSourceOf(damageTypeOnFire), 1.0)
	}
	e.remainingFireTicks--
	if e.remainingFireTicks == 0 {
		t.broadcastEntityFireFlag(e) // stopped burning → clear the client flame flag
	}
}

// tickEntityLava ports the lava-in-block effects (26.2 LavaFluid.entityInside): while a living mob is in
// lava, apply lavaIgnite (igniteForSeconds 15) then lavaHurt (hurt(lava, 4.0)), and halve fallDistance
// (Entity.baseTick: `if (isInLava()) fallDistance *= 0.5`). In 26.2 lava damage moved into the deferred
// InsideBlockEffect system, but both effects fire unconditionally whenever isInLava() holds, so a
// per-tick check is observably identical. Gated on a living mob (e.ai != nil, the v1 LivingEntity
// population) that is alive — items/orbs/projectiles are a no-op (item burn-in-lava is a separate
// ItemEntity mechanic, not modeled). A dry/non-living entity (the oracle pig on land) draws no RNG.
//
//	[VERIFIED javap Entity.lavaIgnite: if(fireImmune)return; igniteForSeconds(15.0f). Entity.lavaHurt:
//	 if(fireImmune)return; hurtServer(damageSources().lava(), 4.0f). Entity.baseTick: isInLava ->
//	 fallDistance *= 0.5. LavaFluid.entityInside: apply(LAVA_IGNITE) then runAfter(LAVA_IGNITE, lavaHurt).]
func (t *TickLoop) tickEntityLava(e *Entity) {
	if e.ai == nil || e.dead {
		return // only living mobs take lava damage in v1; a corpse/non-living entity is skipped
	}
	if !t.mobInLava(e) {
		return
	}
	// fireImmune(): Entity.lavaIgnite/lavaHurt both `if(fireImmune)return` -- a fire-immune mob
	// (MagmaCube/Strider) is NOT ignited and takes NO lava damage. The fallDistance halving below still
	// runs (Entity.baseTick: isInLava -> fallDistance *= 0.5 is unconditional, independent of fireImmune).
	if entityFireImmune(e) {
		e.fallDistance *= 0.5
		return
	}
	// fireImmune() is the v1 constant-false for every OTHER mob (none wired), so lavaIgnite/lavaHurt's
	// `if(fireImmune)return` guards are no-ops. igniteForSeconds(15) FIRST (LAVA_IGNITE applied
	// immediately), then the 4.0 lava hurt (runAfter callback). FIRE_RESISTANCE (an effect, not
	// fireImmune) still lets the mob ignite but negates the is_fire lava damage via applyDamageEntity.
	t.igniteForSeconds(e, 15.0)
	t.applyDamageEntity(e, damageSourceOf(damageTypeLava), 4.0)
	// Entity.baseTick: fallDistance *= 0.5 while in lava (unconditional, independent of fireImmune).
	e.fallDistance *= 0.5
}

// broadcastEntityFireFlag pushes the on-fire shared-flag (DATA_SHARED_FLAGS bit 0x01) to every player
// tracking e, so the client shows/hides the flames. Mirrors the wool/wolf-flag byte broadcast (BYTE
// serializer at index 0). Other shared-flag bits (sneaking/sprinting/…) are not modeled in v1, so the
// byte carries only the fire bit.
func (t *TickLoop) broadcastEntityFireFlag(e *Entity) {
	var flags int8
	if e.remainingFireTicks > 0 {
		flags |= fireSharedFlagBit
	}
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, sharedFlagsDataEntry(flags)))
}

// sunBurnTick ports Mob.isSunBurnTick for the sun-sensitive mobs (zombie/skeleton): during the day,
// in open sky, not in water, a burning roll ignites the mob for 8 seconds. Called from serverAiStep
// for the sun-sensitive types.
//
// Vanilla: br = getLightLevelDependentMagicValue(); burn if br>0.5 && nextFloat()*30 < (br-0.4)*2 &&
// !isInWaterOrRain && level.canSeeSky(eyePos). v1 has no light engine: during the DAY with open sky,
// br == 1.0 (full sky light), so the roll is nextFloat()*30 < 1.2 (a ~4% per-tick chance). canSeeSky
// is the superflat shortcut (open sky above the floor). This is the CITED-STUB faithful path — the
// br/canSeeSky reads become real when the light engine lands; the RNG draw + ignite are exact.
//
//	[VERIFIED javap Mob.isSunBurnTick: br>0.5 && random.nextFloat()*30 < (br-0.4)*2 && !isInWaterOrRain
//	 && canSeeSky(roundedEyePos); Zombie.aiStep: if (isSunBurnTick) igniteForSeconds(8) (armor-gated in
//	 vanilla — v1 zombies have no armor slot, so the bare-ignite path is faithful).]
func (t *TickLoop) sunBurnTick(e *Entity) {
	if !t.isDay() {
		return
	}
	if t.entityInWater(e) {
		return
	}
	if !t.canSeeSky(e) {
		return
	}
	// br == 1.0 (day, open sky) → the roll is nextFloat()*30 < (1.0-0.4)*2 == 1.2.
	const br = 1.0
	if mobRandom(e).nextFloat()*30.0 < float32((br-0.4)*2.0) {
		t.igniteForSeconds(e, 8.0)
	}
}

// isSunSensitive reports whether an entity type burns in daylight (Zombie/Skeleton family). Husk +
// drowned are NOT (unported anyway); wither-skeleton/stray likewise unported. v1: zombie + skeleton.
//
//	[VERIFIED javap Zombie.isSunSensitive == true (base zombie); AbstractSkeleton.aiStep sun-burn.]
func isSunSensitive(e *Entity) bool {
	return e.typ == entity.Zombie.ID || e.typ == entity.Skeleton.ID
}

// entityFireImmune ports Entity.fireImmune() (EntityType.fireImmune()) for the wired fire-immune types.
// A MagmaCube (MagmaCube.isOnFire() const-false + the magma_cube EntityType is registered fireImmune) and
// a Strider (Strider.isOnFire() const-false + the strider EntityType is registered fireImmune) take NO
// fire OR lava damage and are never ignited. Every OTHER v1 mob is const-false (zombie/skeleton/passives
// all burn). Gated in tickEntityFire + tickEntityLava. Cite EntityType.fireImmune + {MagmaCube,Strider}.isOnFire.
func entityFireImmune(e *Entity) bool {
	return e.typ == entity.MagmaCube.ID || e.typ == entity.Strider.ID
}

// isDay reports whether it is daytime (the sun-burn window). The inverse of the night window the
// hostile spawner uses (nightStartTicks..nightEndTicks); outside that window the sun is up. Uses the
// same gametime day-phase proxy (no separate dayTime clock in v1).
//
//	[VERIFIED javap net.minecraft.world.level.Level.isDay == !isNight; the monster spawn gate uses the
//	 same [13000,23000) night window.]
func (t *TickLoop) isDay() bool {
	dayTime := t.gametime % 24000
	return !(dayTime >= nightStartTicks && dayTime < nightEndTicks)
}

// entityInWater reports whether the block at the entity's feet is water — the fire-extinguish +
// sun-burn water guard. v1 shortcut over the world block state (no full fluid-tag AABB sweep). A nil
// world (test loop) is treated as not-in-water.
func (t *TickLoop) entityInWater(e *Entity) bool {
	if t.world() == nil {
		return false
	}
	return t.fluidAt(pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(e.y)), Z: int(math.Floor(e.z))}).isWater
}

// canSeeSky is the superflat sky-exposure shortcut: an entity at or above the world surface has open
// sky above it (no light engine / heightmap raycast in v1). CITED STUB — becomes level.canSeeSky when
// the light engine + real heightmaps land. For the superflat stub the floor top is the spawn surface,
// so any entity standing on it sees sky.
func (t *TickLoop) canSeeSky(e *Entity) bool {
	return int(math.Floor(e.y)) >= t.spawnSurfaceY
}

// canSeeSkyAt is canSeeSky for a raw block position (the GroundPathNavigation.trimPath per-node check
// level.canSeeSky(new BlockPos(node.x, node.y, node.z))). Same superflat sky-exposure shortcut as
// canSeeSky: a cell at or above the spawn surface has open sky. CITED STUB (becomes the real light-
// engine canSeeSky later), sibling of canSeeSky above.
func (t *TickLoop) canSeeSkyAt(y int) bool {
	return y >= t.spawnSurfaceY
}
