package server

// lightning_conversion.go — the per-mob thunderHit CONVERSIONS, ported 1:1 from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, read via CFR / javap -c -p this session). It layers on top of the LIGHTNING
// BOLT subsystem (lightning.go) already merged: the bolt's damage loop (boltDamageEntitiesInBox) calls
// boltThunderHitMob per struck LivingEntity, which is Entity.thunderHit (fire + 5.0 damage). Several mob
// species OVERRIDE thunderHit — that override runs INSTEAD OF the base for a struck mob of that species,
// exactly as Java virtual dispatch resolves `mob.thunderHit(level, bolt)` to the most-derived override.
//
// THE FOUR 26.2 OVERRIDES (the full set — verified by grepping thunderHit across every net.minecraft.world
// .entity class in the jar this session; only these four, plus the base Entity.thunderHit, exist):
//
//  1. net.minecraft.world.entity.animal.pig.Pig.thunderHit (CFR, this session):
//         if (level.getDifficulty() != Difficulty.PEACEFUL) {
//             ZombifiedPiglin zp = convertTo(ZOMBIFIED_PIGLIN, ConversionParams.single(this,false,true), zp -> {
//                 zp.populateDefaultEquipmentSlots(getRandom(), level.getCurrentDifficultyAt(blockPosition()));
//                 zp.setPersistenceRequired(); });
//             if (zp == null) super.thunderHit(level, bolt);   // only fire/damage if the convert failed
//         } else super.thunderHit(level, bolt);
//
//  2. net.minecraft.world.entity.monster.Creeper.thunderHit (javap, this session):
//         super.thunderHit(level, bolt);                       // Monster -> ... -> Entity.thunderHit (fire+5.0)
//         entityData.set(DATA_IS_POWERED, true);               // == setPowered(true): the charged creeper
//
//  3. net.minecraft.world.entity.npc.villager.Villager.thunderHit (CFR, this session):
//         if (level.getDifficulty() != Difficulty.PEACEFUL) {
//             LOGGER.info(...);
//             Witch w = convertTo(WITCH, ConversionParams.single(this,false,false), w -> {
//                 w.finalizeSpawn(level, getCurrentDifficultyAt(w.blockPosition()), CONVERSION, null);
//                 w.setPersistenceRequired(); releaseAllPois(); });
//             if (w == null) super.thunderHit(level, bolt);
//         } else super.thunderHit(level, bolt);
//
//  4. net.minecraft.world.entity.animal.cow.MushroomCow.thunderHit (CFR, this session):
//         UUID id = bolt.getUUID();
//         if (!id.equals(this.lastLightningBoltUUID)) {
//             setVariant(getVariant() == RED ? BROWN : RED);   // toggle the mushroom color
//             this.lastLightningBoltUUID = id;                 // once per bolt, not once per damage tick
//             playSound(MOOSHROOM_CONVERT, 2.0f, 1.0f);
//         }
//         // NOTE: MushroomCow does NOT call super.thunderHit — a struck mooshroom takes NO lightning
//         // fire/damage, only the color toggle.
//
// KEY VANILLA INVARIANT (verified in all three convert-overrides): on a SUCCESSFUL convertTo the base
// Entity.thunderHit (the fire +1 / igniteForSeconds / 5.0 hurt) is NOT applied — super.thunderHit runs ONLY
// when convertTo returns null (isRemoved / entityType.create failed). So a converting mob is REPLACED, not
// hurt. The creeper is the exception: it calls super.thunderHit FIRST (fire+damage), THEN setPowered.
//
// convertTo (net.minecraft.world.entity.Mob.convertTo, CFR this session): create the target EntityType,
// ConversionType.SINGLE.convert copies POSITION (copyPosition), deltaMovement, fallDistance, hurtTime,
// yBodyRot, onGround, effects, absorption, baby/age, custom-name + flags; addFreshEntity(newMob); then
// discard() the old mob (SINGLE.discardAfterConversion == true). HEALTH is NOT copied — the new mob spawns
// at its own default MaxHealth (finalizeConversion runs the afterConversion hook). The v1 spawnDeclaredMob /
// the bare NewEntity type-swap reproduce this: a FRESH entity at the old (x,y,z) with default max health,
// and the old mob removed from the store (== discard()).
//
// SCOPE / CITED DEFERRALS (each a not-yet-built subsystem, structured to become real later):
//   - Pig -> ZombifiedPiglin: the ZombifiedPiglin TYPE exists (data/entity id 155) but has NO bundled mob
//     PLUGIN declaration (no assets/vanilla_zombified_piglin), so it has no declared AI/attribute set. The
//     TYPE-SWAP is REAL (a fresh entity.ZombifiedPiglin at the pig's pos + the pig discarded, faithful to
//     convertTo's create+copyPosition+discard); the zombified-piglin's AI GOALS + the afterConversion
//     equipment (populateDefaultEquipmentSlots: the golden sword) + setPersistenceRequired are DEFERRED
//     (no piglin declaration / no equipment subsystem on the type-swap). CITE Pig.thunderHit. When a
//     vanilla_zombified_piglin plugin lands, swap the bare NewEntity for spawnVanillaMob so it gets its AI.
//   - Villager -> Witch: the Witch is a BUNDLED mob (vanilla_witch) — this conversion is FULLY REAL: the
//     witch spawns with its declared AI via spawnVanillaMob. The afterConversion finalizeSpawn (RandomWalk
//     nav init) + setPersistenceRequired + releaseAllPois (the villager POI release) are cite-deferred (the
//     POI-release/persistence subsystems); the witch is a live, AI-driven, correctly-positioned mob. CITE
//     Villager.thunderHit. The LOGGER.info is a server log line (not gameplay) — omitted.
//   - Creeper -> powered: FULLY REAL. e.powered drives the doubled explosion radius (ai_goals_creeper.go's
//     explodeCreeper: multiplier = powered?2:1). The DATA_IS_POWERED client-metadata broadcast remains the
//     EXISTING cited deferral (ai_goals_creeper.go already cite-defers the charged-creeper client visual) —
//     the server-authoritative powered STATE is the observable that matters (the double blast). CITE
//     Creeper.thunderHit -> setPowered.
//   - Mooshroom RED<->BROWN toggle: FULLY REAL server-side (mooshroomVariant + the lastLightningBoltUUID
//     per-bolt guard). The MOOSHROOM_CONVERT sound + the DATA_TYPE client-metadata broadcast are cite-
//     deferred client cues (the mooshroom variant subsystem is otherwise host-deferred, per
//     vanilla_mooshroom/main.star). CITE MushroomCow.thunderHit.

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// mooshroomVariantRed / mooshroomVariantBrown are MushroomCow.Variant.id (RED==0 == Variant.DEFAULT,
// BROWN==1) — the DATA_TYPE entity-data values. VERIFIED CFR: RED("red",0,...), BROWN("brown",1,...),
// DEFAULT = RED.
const (
	mooshroomVariantRed   int32 = 0
	mooshroomVariantBrown int32 = 1
)

// boltThunderHitConvert is the per-species thunderHit DISPATCH the bolt's damage loop consults BEFORE the
// base Entity.thunderHit (boltThunderHitMob). It ports the mob-species virtual override of
// `mob.thunderHit(level, bolt)`: for a species with a CONVERTING override (Pig/Villager) it performs the
// convertTo and returns true (the base fire/damage is SKIPPED — vanilla only calls super.thunderHit when
// convertTo returns null); for the Creeper it returns false so the caller applies the base fire+damage,
// then the caller sets powered (matching Creeper.thunderHit's super-then-setPowered order); for the
// Mooshroom it performs the variant toggle and returns true (a struck mooshroom takes NO fire/damage). A
// species with NO thunderHit override returns false — the base Entity.thunderHit (fire + 5.0 damage) runs.
//
// Return value: true == "handled, DO NOT apply the base fire/damage"; false == "apply the base
// Entity.thunderHit". The `bolt` is the striking LightningBolt entity (its uuid is the mooshroom per-bolt
// guard). CITE: Pig/Villager/MushroomCow.thunderHit (convert/toggle == skip super); Creeper.thunderHit
// (super first, then setPowered — handled by the caller).
func (t *TickLoop) boltThunderHitConvert(bolt *Entity, m *Entity) bool {
	switch m.typ {
	case entity.Pig.ID:
		// Pig.thunderHit: difficulty != PEACEFUL -> convertTo(ZOMBIFIED_PIGLIN). serverDifficulty is the
		// cited NORMAL stub (!= PEACEFUL), so the convert always fires. On a successful type-swap the base
		// fire/damage is skipped (super.thunderHit only on a null convert).
		if serverDifficulty == difficultyPeaceful {
			return false // convert suppressed -> caller applies base Entity.thunderHit
		}
		return t.thunderHitConvertPig(m)
	case entity.Villager.ID:
		// Villager.thunderHit: difficulty != PEACEFUL -> convertTo(WITCH).
		if serverDifficulty == difficultyPeaceful {
			return false
		}
		return t.thunderHitConvertVillager(m)
	case entity.Mooshroom.ID:
		// MushroomCow.thunderHit: per-bolt-guarded RED<->BROWN toggle; NO super (no fire/damage).
		t.thunderHitMooshroom(bolt, m)
		return true
	default:
		// No thunderHit override (incl. Creeper, whose override calls super FIRST): apply the base
		// Entity.thunderHit. The Creeper's setPowered is applied by the caller AFTER the base hit.
		return false
	}
}

// thunderHitConvertPig ports Pig.thunderHit's convert branch: convertTo(ZOMBIFIED_PIGLIN,
// ConversionParams.single(this,false,true), ...). The ZombifiedPiglin TYPE exists (id 155) but has no
// bundled AI plugin, so the type-swap spawns a bare NewEntity(entity.ZombifiedPiglin) at the pig's position
// (== convertTo's create + ConversionType.SINGLE copyPosition) with default max health, then discards the
// pig (== convertTo's discard on SINGLE). The afterConversion (populateDefaultEquipmentSlots golden sword +
// setPersistenceRequired) and the piglin's AI are DEFERRED — the OBSERVABLE conversion (a pig struck by
// lightning becomes a zombified piglin at the same spot) is faithful. Returns true (convert succeeded ->
// skip the base fire/damage). CITE Pig.thunderHit + Mob.convertTo + ConversionType.SINGLE.
func (t *TickLoop) thunderHitConvertPig(m *Entity) bool {
	// convertTo's isRemoved() guard: a pig already scheduled for removal does not convert (super.thunderHit
	// would run, but a dead mob is a no-op). Mirror it — a removed mob returns false so the base path's own
	// isAlive guards handle it.
	if m.dead {
		return false
	}
	newMob := t.thunderHitTypeSwap(m, entity.ZombifiedPiglin)
	if newMob == nil {
		return false // convertTo == null -> super.thunderHit (base fire/damage)
	}
	return true
}

// thunderHitConvertVillager ports Villager.thunderHit's convert branch: convertTo(WITCH,
// ConversionParams.single(this,false,false), ...). The Witch is a BUNDLED mob (vanilla_witch), so the swap
// spawns a fully AI-driven witch via spawnVanillaMob at the villager's position (== convertTo's create +
// copyPosition) and discards the villager (== convertTo's discard). The afterConversion (finalizeSpawn +
// setPersistenceRequired + releaseAllPois) is cite-deferred (POI/persistence subsystems). Returns true
// (convert succeeded -> skip the base fire/damage). CITE Villager.thunderHit + Mob.convertTo.
func (t *TickLoop) thunderHitConvertVillager(m *Entity) bool {
	if m.dead {
		return false
	}
	// The Witch is a declared mob: spawn it with its real AI at the villager's exact position (copyPosition),
	// then discard the villager. A nil registry / missing declaration is a loud failure in spawnVanillaMob
	// (the boot-load guarantees vanilla_witch is present), so a reached-here spawn always succeeds.
	witch := t.spawnVanillaMob(vanillaWitchMobName, m.x, m.y, m.z)
	if witch == nil {
		return false
	}
	// ConversionType.SINGLE.convert copies a handful of carry fields; the load-bearing one here is the
	// position (already set at spawn). Copy the small faithful subset the v1 entity models (deltaMovement,
	// yBodyRot, onGround) so the witch inherits the villager's momentum/facing exactly as the jar does.
	t.thunderHitCopyCommon(m, witch)
	// discard() the old villager (SINGLE.shouldDiscardAfterConversion == true): remove it from its store so
	// the tracker broadcasts RemoveEntities next tick.
	t.thunderHitDiscard(m)
	return true
}

// thunderHitMooshroom ports MushroomCow.thunderHit: the per-bolt-guarded RED<->BROWN variant toggle. It
// draws NO random and applies NO fire/damage (MushroomCow does not call super.thunderHit). The guard
// (lastLightningBoltUUID != bolt.uuid) makes the toggle fire ONCE per striking bolt — the bolt hits the
// same mooshroom every tick it is alive (life 2..0), so without the guard the color would flip 3x and net
// back. CITE MushroomCow.thunderHit.
func (t *TickLoop) thunderHitMooshroom(bolt *Entity, m *Entity) {
	// if (!bolt.getUUID().equals(this.lastLightningBoltUUID)) { toggle; lastLightningBoltUUID = id; sound; }
	if m.hasLastLightningBolt && m.lastLightningBoltUUID == bolt.uuid {
		return // already toggled for THIS bolt — no-op the rest of the bolt's life
	}
	// setVariant(getVariant() == RED ? BROWN : RED).
	if m.mooshroomVariant == mooshroomVariantRed {
		m.mooshroomVariant = mooshroomVariantBrown
	} else {
		m.mooshroomVariant = mooshroomVariantRed
	}
	m.lastLightningBoltUUID = bolt.uuid
	m.hasLastLightningBolt = true
	// playSound(MOOSHROOM_CONVERT, 2.0f, 1.0f): cite-deferred client cue (no per-mob sound emit wired for
	// this event). The DATA_TYPE client-metadata broadcast is likewise cite-deferred (the mooshroom variant
	// subsystem is host-deferred per vanilla_mooshroom/main.star). CITE MushroomCow.thunderHit sound/metadata.
}

// thunderHitTypeSwap performs convertTo's create + copyPosition + discard for a target with NO bundled AI
// plugin (the ZombifiedPiglin path): spawn a bare NewEntity(targetType) at the old mob's position with its
// type's default attributes/max-health, insert it into the old mob's owning store (== addFreshEntity), and
// discard the old mob. Returns the new entity (never nil in v1 — NewEntity always succeeds; the null path
// exists only to mirror convertTo's contract). CITE Mob.convertTo + ConversionType.SINGLE (copyPosition +
// discard).
func (t *TickLoop) thunderHitTypeSwap(from *Entity, target entity.Entity) *Entity {
	// Mob newMob = entityType.create(...); to.copyPosition(from). NewEntity attaches the target type's
	// DefaultAttributes map (attribute.NewMapForEntity by the type's registry name), so the new mob folds
	// its own MaxHealth.
	newMob := NewEntity(t.idAlloc.AllocID(), target, from.x, from.y, from.z)
	// LivingEntity.<init>: setHealth(getMaxHealth()) — the fresh mob starts at its default max health (health
	// is NOT copied from the old mob; convertCommon copies effects/absorption but never setHealth). Shared
	// spawn-health helper so the type-swap stays in lockstep with the declared spawners.
	initSpawnHealth(newMob)
	// Carry the small faithful subset ConversionType.SINGLE.convert copies (position already set).
	t.thunderHitCopyCommon(from, newMob)
	// addFreshEntity(newMob): insert into the OLD mob's owning region store (the tracker broadcasts AddEntity
	// next tick). regionForEntity resolves the old mob's owner; fall back to the current region.
	owner := t.regionForEntity(from)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return nil // no store to add to (a bare test loop): the convert cannot complete
	}
	owner.entities.add(newMob)
	// discard() the old mob (SINGLE.shouldDiscardAfterConversion): remove from its store.
	t.thunderHitDiscard(from)
	return newMob
}

// thunderHitCopyCommon carries the faithful subset of ConversionType.SINGLE.convert the v1 entity models:
// deltaMovement, yBodyRot, onGround, fallDistance. Position is copied at spawn (copyPosition). The
// equipment/effects/absorption/age/leash carries are cite-deferred (their subsystems are unbuilt or the
// v1 entity does not model them). CITE ConversionType.SINGLE.convert.
func (t *TickLoop) thunderHitCopyCommon(from, to *Entity) {
	// to.setDeltaMovement(from.getDeltaMovement()).
	to.vx, to.vy, to.vz = from.vx, from.vy, from.vz
	// to.yBodyRot = from.yBodyRot; to.setOnGround(from.onGround()); to.fallDistance = from.fallDistance.
	// v1 models the body-yaw as `yaw` (there is no separate yBodyRot field), so the body-rotation carry is
	// yaw/headYaw; the look pitch is carried too.
	to.yaw = from.yaw
	to.pitch = from.pitch
	to.headYaw = from.headYaw
	to.onGround = from.onGround
	to.fallDistance = from.fallDistance
}

// thunderHitDiscard is convertTo's `this.discard()` for the converted-away mob: mark it dead and remove it
// from its owning store so the tracker broadcasts RemoveEntities. Mirrors the death/silverfish discard path.
// CITE Entity.discard / Mob.convertTo (discardAfterConversion).
func (t *TickLoop) thunderHitDiscard(m *Entity) {
	m.dead = true
	owner := t.regionForEntity(m)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.entities != nil {
		owner.entities.remove(m.id)
	}
}
