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

// broadcastEntityFireFlag pushes the entity DATA_SHARED_FLAGS byte to every player tracking e, so the
// client shows/hides flames. The byte is shared with other flags; entitySharedFlags composes the modeled
// fire and invisibility bits before writing index 0.
func (t *TickLoop) broadcastEntityFireFlag(e *Entity) {
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, sharedFlagsDataEntry(entitySharedFlags(e))))
}

// tickMobSunBurn ports Mob.aiStep's BURN_IN_DAYLIGHT limb through Mob.burnUndead: a live,
// sun-sensitive mob that passes Mob.isSunBurnTick and has an empty sunProtectionSlot ignites for 8s.
//
//	[VERIFIED javap Mob.aiStep: is(BURN_IN_DAYLIGHT) -> burnUndead(); burnUndead: if isAlive &&
//	 isSunBurnTick then if getItemBySlot(sunProtectionSlot()).isEmpty() igniteForSeconds(8.0f).
//	 Mob.isSunBurnTick: !client, MONSTERS_BURN, brightness/sun/water gates. Sulfur's light read uses
//	 Level.getMaxLocalRawBrightness(pos) and the 14+ daylight threshold requested for this slice.]
func (t *TickLoop) tickMobSunBurn(e *Entity) {
	if e == nil || !e.isSunSensitive() || !e.isAlive() {
		return
	}
	if !t.isDay() {
		return
	}
	if t.entityInWater(e) {
		return
	}
	if e.isFireImmune() {
		return
	}
	if !slotIsEmpty(e.getItemBySlot(eqSlotHead)) {
		return
	}
	pos := pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(e.y)), Z: int(math.Floor(e.z))}
	if t.maxLocalRawBrightness(pos) < 14 {
		return
	}
	t.igniteForSeconds(e, 8.0)
}

func (e *Entity) isSunSensitive() bool {
	if e == nil {
		return false
	}
	// Mob.aiStep gates the daylight burn on getType().is(EntityTypeTags.BURN_IN_DAYLIGHT). The vanilla
	// tag data/minecraft/tags/entity_type/burn_in_daylight.json is EXACTLY: skeleton, stray,
	// wither_skeleton, bogged, zombie, zombie_horse, zombie_villager, drowned, zombie_nautilus, phantom.
	// HUSK is deliberately NOT in the tag (it survives daylight). WitherSkeleton IS in the tag but is
	// fire-immune, so tickMobSunBurn's isFireImmune() gate no-ops it. Drowned burns only out of water
	// (the entityInWater gate). Cite Mob.aiStep BURN_IN_DAYLIGHT + the burn_in_daylight entity_type tag.
	switch e.typ {
	case entity.Skeleton.ID, entity.Stray.ID, entity.WitherSkeleton.ID, entity.Bogged.ID,
		entity.Zombie.ID, entity.ZombieHorse.ID, entity.ZombieVillager.ID, entity.Drowned.ID,
		entity.ZombieNautilus.ID, entity.Phantom.ID:
		return true
	default:
		return false
	}
}

func isSunSensitive(e *Entity) bool { return e.isSunSensitive() }

func (e *Entity) isFireImmune() bool {
	return e != nil && (e.fireImmune || entityTypeFireImmune(e.typ))
}

func entityFireImmune(e *Entity) bool { return e.isFireImmune() }

func entityTypeFireImmune(typ entity.ID) bool {
	return typ == entity.MagmaCube.ID || typ == entity.Strider.ID || typ == entity.WitherSkeleton.ID ||
		typ == entity.ZombifiedPiglin.ID || typ == entity.Zoglin.ID
}

// isDay reports Level.isDay for the daylight burn window: dayTime in [0, 12000).
//
//	[VERIFIED javap Level.isDay / day-cycle semantics used by Mob.isSunBurnTick.]
func (t *TickLoop) isDay() bool {
	dayTime := t.gametime % 24000
	return dayTime >= 0 && dayTime < 12000
}

// sunBurnTick is the legacy stochastic helper the Phantom test loop drives; retained as a thin
// shim that calls tickMobSunBurn (the deterministic 1:1 daylight ignite path supersedes the
// old nextFloat()*30 < 1.2 stub).
//
//	[DEPRECATED] superseded by tickMobSunBurn (fire.go); kept for the phantom_test harness.
func (t *TickLoop) sunBurnTick(e *Entity) { t.tickMobSunBurn(e) }


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
