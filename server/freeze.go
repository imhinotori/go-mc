package server

// freeze.go -- the POWDER-SNOW / FREEZE damage subsystem. PORTED 1:1 from the unobfuscated 26.2 jar
// (javap -c -p / CFR temp/cache/26.2-inner.jar, this session):
//
//   - net.minecraft.world.entity.Entity.getTicksRequiredToFreeze()  -> sipush 140; ireturn.
//   - net.minecraft.world.entity.Entity.getTicksFrozen / setTicksFrozen (DATA_TICKS_FROZEN, INT, index 7).
//   - net.minecraft.world.entity.Entity.getPercentFrozen: min(getTicksFrozen(), required) / required (float).
//   - net.minecraft.world.entity.Entity.isFullyFrozen: getTicksFrozen() >= getTicksRequiredToFreeze().
//   - net.minecraft.world.entity.Entity.canFreeze: !this.is(EntityTypeTags.FREEZE_IMMUNE_ENTITY_TYPES).
//   - net.minecraft.world.entity.LivingEntity.canFreeze: for each ARMOR slot, if getItemBySlot(slot).is(
//     ItemTags.FREEZE_IMMUNE_WEARABLES) return false; else super.canFreeze() (isSpectator gate first).
//   - net.minecraft.world.entity.LivingEntity.aiStep freeze block (bytecode 666-779, ServerLevel-only):
//         if (this.isInPowderSnow && this.canFreeze()) { /* keep frost */ }
//         else this.setTicksFrozen(Math.max(0, this.getTicksFrozen() - 2));
//         this.removeFrost(); this.tryAddFrost();
//         if (this.tickCount % 40 == 0 && this.isFullyFrozen() && this.canFreeze())
//             this.hurtServer(serverLevel, this.damageSources().freeze(), 1.0F);
//   - net.minecraft.world.entity.InsideBlockEffectType.FREEZE lambda (PowderSnowBlock.entityInside applies it):
//         entity.setIsInPowderSnow(true);
//         if (entity.canFreeze())
//             entity.setTicksFrozen(Math.min(entity.getTicksRequiredToFreeze(), entity.getTicksFrozen() + 1));
//   - net.minecraft.world.level.block.PowderSnowBlock.entityInside: makeStuckInBlock(0.9,1.5,0.9) then the
//     FREEZE inside-block effect; canEntityWalkOnPowderSnow: POWDER_SNOW_WALKABLE_MOBS tag OR (LivingEntity
//     && getItemBySlot(getFeetSlot()).is(Items.LEATHER_BOOTS)) -- the leather-boots surface-walk exemption.
//
// CONSTANTS (jar-exact): getTicksRequiredToFreeze() == 140; the aiStep cadence is tickCount % 40 == 0;
// the FREEZE damage amount is 1.0F (fconst_1 at aiStep offset 767); the per-tick decay is -2 clamped
// at 0; the per-tick accumulation is +1 clamped at 140; the FREEZE_HURTS_EXTRA_TYPES multiply is *5.0f
// (wired in combat_mob.go's applyDamageEntity, matching the LivingEntity.hurtServer pipeline order).
//
// RNG DISCIPLINE (pig oracle): every path here is pure world/int math + tag reads -- NO RandomSource
// draw. The accumulation (setIsInPowderSnow+ +1) only advances when the entity is INSIDE a powder-snow
// block; the oracle pig is never in powder snow, so its ticksFrozen stays 0, isFullyFrozen is false, no
// FREEZE damage is dealt, and its pinned per-mob RNG stream is byte-identically unperturbed.
//
// DEFERRALS (cite-recorded, NEVER silently dropped):
//   1. removeFrost/tryAddFrost apply/remove the SPEED_MODIFIER_POWDER_SNOW transient MOVEMENT_SPEED
//      modifier (-0.05 * getPercentFrozen). No generic per-entity transient AttributeModifier map
//      exists yet (only the witch's specialized speed handling), so the SPEED SLOW is a cited stub --
//      the ticksFrozen accumulation/decay and the FREEZE damage (the observable that kills) are ported
//      EXACTLY; only the frost movement-slow visual is deferred. Cite LivingEntity.removeFrost/tryAddFrost.
//   2. isInPowderSnow / setIsInPowderSnow: no checkInsideBlocks inside-block-effect dispatch runs yet,
//      so tickEntityFreeze reads the OBSERVABLE PROXY entityInsidePowderSnow (whether the entity's feet
//      block IS powder snow -- the block whose PowderSnowBlock.entityInside would setIsInPowderSnow +
//      apply the FREEZE effect). It becomes a real e.isInPowderSnow field read when the inside-block
//      subsystem lands. This is the SAME cited seam ai_goals_powder_snow.go's mobInPowderSnow uses.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/tag"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// ticksRequiredToFreeze ports Entity.getTicksRequiredToFreeze() == 140 (sipush 140; ireturn). The
// full-freeze threshold: at ticksFrozen >= 140 the entity isFullyFrozen and takes FREEZE damage.
//
//	[VERIFIED javap Entity.getTicksRequiredToFreeze: sipush 140; ireturn.]
const ticksRequiredToFreeze int32 = 140

// freezeDamageInterval is the aiStep FREEZE cadence: tickCount % 40 == 0 (bipush 40; irem; ifne). A
// fully-frozen can-freeze entity takes freeze damage once every 40 ticks (2 seconds).
//
//	[VERIFIED javap LivingEntity.aiStep: getfield tickCount; bipush 40; irem; ifne (skip damage).]
const freezeDamageInterval int32 = 40

// freezeDamageAmount is the FREEZE hurtServer amount: 1.0F (fconst_1 at aiStep offset 767, passed to
// hurtServer). A member of freeze_hurts_extra_types multiplies this by 5 in the hurt pipeline (that
// multiply lives in combat_mob.go, matching LivingEntity.hurtServer's freeze-extra branch order).
//
//	[VERIFIED javap LivingEntity.aiStep: ...damageSources().freeze(); fconst_1; invokevirtual hurtServer.]
const freezeDamageAmount float32 = 1.0

// getTicksFrozen ports Entity.getTicksFrozen(): the DATA_TICKS_FROZEN value (int). Reads the tick-owned
// e.ticksFrozen field (the Go home for the INT SynchedEntityData accessor). A nil entity reads 0.
//
//	[VERIFIED javap Entity.getTicksFrozen: entityData.get(DATA_TICKS_FROZEN); intValue.]
func getTicksFrozen(e *Entity) int32 {
	if e == nil {
		return 0
	}
	return e.ticksFrozen
}

// setTicksFrozen ports Entity.setTicksFrozen(int): stores the DATA_TICKS_FROZEN value. Vanilla writes
// the SynchedEntityData accessor (which, on a real change, queues a SetEntityData broadcast to trackers
// so the client draws the frost vignette). We store the tick-owned field and, on a change, broadcast
// the index-7 INT data value (t supplies the tracker fan-out; a nil t / nil e is a store-only no-op).
//
//	[VERIFIED javap Entity.setTicksFrozen: entityData.set(DATA_TICKS_FROZEN, Integer.valueOf(i)).]
func (t *TickLoop) setTicksFrozen(e *Entity, n int32) {
	if e == nil {
		return
	}
	if e.ticksFrozen == n {
		return // SynchedEntityData.set only marks dirty (and broadcasts) on an actual change.
	}
	e.ticksFrozen = n
	if t != nil {
		t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, ticksFrozenDataEntry(n)))
	}
}

// getPercentFrozen ports Entity.getPercentFrozen(): (float) Math.min(getTicksFrozen(), required) /
// (float) required, where required = getTicksRequiredToFreeze(). The client's frost-vignette intensity.
//
//	[VERIFIED javap Entity.getPercentFrozen: required=getTicksRequiredToFreeze; i2f( min(getTicksFrozen,
//	 required) ) / i2f(required).]
func getPercentFrozen(e *Entity) float32 {
	required := ticksRequiredToFreeze
	frozen := getTicksFrozen(e)
	if frozen > required {
		frozen = required // Math.min(getTicksFrozen(), required)
	}
	return float32(frozen) / float32(required)
}

// isFullyFrozen ports Entity.isFullyFrozen(): getTicksFrozen() >= getTicksRequiredToFreeze().
//
//	[VERIFIED javap Entity.isFullyFrozen: getTicksFrozen; getTicksRequiredToFreeze; if_icmplt -> false;
//	 else true.]
func isFullyFrozen(e *Entity) bool {
	return getTicksFrozen(e) >= ticksRequiredToFreeze
}

// isFreezeImmuneType ports Entity.canFreeze()'s only gate, EntityTypeTags.FREEZE_IMMUNE_ENTITY_TYPES:
// Entity.canFreeze returns !this.is(FREEZE_IMMUNE_ENTITY_TYPES). The exact jar tag contents
// (freeze_immune_entity_types.json, this session): stray, polar_bear, snow_golem, wither. Modeled as an
// explicit type set (no generic entity-type-tag table exists yet -- the same cited pattern
// ai_goals_powder_snow.go's isPowderSnowWalkableMob uses); it becomes a real tag lookup when tags land.
//
//	[VERIFIED javap Entity.canFreeze: is(EntityTypeTags.FREEZE_IMMUNE_ENTITY_TYPES) -> return !that.]
func isFreezeImmuneType(typ entity.ID) bool {
	switch typ {
	case entity.Stray.ID, entity.PolarBear.ID, entity.SnowGolem.ID, entity.Wither.ID:
		return true
	default:
		return false
	}
}

// isFreezeHurtsExtraType ports EntityTypeTags.FREEZE_HURTS_EXTRA_TYPES membership: the exact jar tag
// contents (freeze_hurts_extra_types.json, this session): strider, blaze, magma_cube. A member takes
// amount*5 FREEZE damage in the hurtServer pipeline (LivingEntity.hurtServer freeze-extra branch; wired
// in combat_mob.go). Modeled as an explicit type set (same cited pattern as isFreezeImmuneType).
//
//	[VERIFIED javap LivingEntity.hurtServer: source.is(IS_FREEZING) && this.is(FREEZE_HURTS_EXTRA_TYPES)
//	 -> amount *= 5.0f.]
func isFreezeHurtsExtraType(typ entity.ID) bool {
	switch typ {
	case entity.Strider.ID, entity.Blaze.ID, entity.MagmaCube.ID:
		return true
	default:
		return false
	}
}

// entityCanFreeze ports the LivingEntity.canFreeze() override, which itself calls Entity.canFreeze():
//
//	LivingEntity.canFreeze():
//	    if (this.isSpectator()) return false;                       // v1: mobs are never spectators
//	    for (EquipmentSlot slot : EquipmentSlotGroup.ARMOR)         // HEAD/CHEST/LEGS/FEET (+BODY animal)
//	        if (getItemBySlot(slot).is(ItemTags.FREEZE_IMMUNE_WEARABLES)) return false;  // leather armor
//	    return super.canFreeze();                                   // == Entity.canFreeze (type-tag gate)
//	Entity.canFreeze():
//	    return !this.is(EntityTypeTags.FREEZE_IMMUNE_ENTITY_TYPES);
//
// v1 mobs carry no armor equipment (no mob-equipment subsystem -- cited across the AI files, e.g.
// ai_goals_skeleton_sun.go), so the FREEZE_IMMUNE_WEARABLES armor scan is a cited constant-false for a
// mob: getItemBySlot(ARMOR) is EMPTY, and EMPTY.is(tag) is false. entityCanFreeze therefore reduces to
// Entity.canFreeze (the type-tag gate) for a mob -- structurally faithful, and it becomes a real armor
// scan when mob equipment lands. isWearingFreezeImmuneArmor is the seam that reads real armor (a player
// path exists via playerItemBySlot); for a plain mob it returns false, so canFreeze == !immuneType.
// mob equipment lands. wearsFreezeImmuneArmor is the seam that scans real armor stacks; for a plain
// v1 mob the armor slots are empty (mobEquippedArmor returns none), so canFreeze == !immuneType.
func (t *TickLoop) entityCanFreeze(e *Entity) bool {
	if e == nil {
		return false
	}
	// LivingEntity.canFreeze: the FREEZE_IMMUNE_WEARABLES armor scan (leather armor makes the wearer
	// freeze-immune). v1 mobs carry no armor equipment, so mobEquippedArmor returns an empty set and the
	// scan is a cited constant-false; it becomes a real read when mob equipment lands.
	if wearsFreezeImmuneArmor(mobEquippedArmor(e)...) {
		return false
	}
	// super.canFreeze() == Entity.canFreeze() == !is(FREEZE_IMMUNE_ENTITY_TYPES).
	return !isFreezeImmuneType(e.typ)
}

// mobEquippedArmor returns the entity's four humanoid armor stacks (HEAD/CHEST/LEGS/FEET) for the
// LivingEntity.canFreeze armor scan. v1 mobs have no armor-equipment subsystem (cited across the AI
// files, e.g. ai_goals_skeleton_sun.go getItemBySlot(HEAD)==EMPTY), so this returns an empty slice --
// a faithful constant. It becomes a real getItemBySlot(ARMOR) read when mob equipment lands.
func mobEquippedArmor(_ *Entity) []component.SlotData { return nil }

// wearsFreezeImmuneArmor ports the LivingEntity.canFreeze() armor loop: for each supplied ARMOR stack,
// if getItemBySlot(slot).is(ItemTags.FREEZE_IMMUNE_WEARABLES) it returns true (the wearer is exempt).
// FREEZE_IMMUNE_WEARABLES = {leather_boots, leather_leggings, leather_chestplate, leather_helmet,
// leather_horse_armor} (freeze_immune_wearables.json; ItemTags["freeze_immune_wearables"] =
// {982,983,984,985,1290}). This is also the LEATHER-BOOTS surface/exemption the task pins: a leather
// boots stack in the feet slot returns true. An EMPTY stack (Count <= 0) is skipped (ItemStack.EMPTY.is
// == false).
//
//	[VERIFIED javap LivingEntity.canFreeze: EquipmentSlotGroup.ARMOR loop; getItemBySlot;
//	 ItemStack.is(ItemTags.FREEZE_IMMUNE_WEARABLES) -> iconst_0 ireturn.]
func wearsFreezeImmuneArmor(armor ...component.SlotData) bool {
	for _, it := range armor {
		if it.Count > 0 && tag.ItemTags["freeze_immune_wearables"][int32(it.ItemID)] {
			return true
		}
	}
	return false
}

// entityInsidePowderSnow is the cited proxy for Entity.isInPowderSnow (DEFERRAL 2): no inside-block
// effect dispatch models the field yet, so it reads whether the entity's feet block IS powder snow --
// the block whose PowderSnowBlock.entityInside would setIsInPowderSnow(true) + apply the FREEZE effect.
// Reads the codegen'd block.PowderSnow state id (data present). Becomes a real e.isInPowderSnow read
// when checkInsideBlocks lands. Same seam ai_goals_powder_snow.go's mobInPowderSnow uses.
//
//	[VERIFIED javap PowderSnowBlock.entityInside: makeStuckInBlock + InsideBlockEffectType.FREEZE, whose
//	 lambda does setIsInPowderSnow(true) then the +1 frost accumulation.]
func (t *TickLoop) entityInsidePowderSnow(e *Entity) bool {
	if e == nil || t.world() == nil {
		return false
	}
	feet := pk.Position{
		X: int(math.Floor(e.x)),
		Y: int(math.Floor(e.y)),
		Z: int(math.Floor(e.z)),
	}
	s, ok := t.world().GetBlock(feet, dimMinY)
	if !ok {
		return false // unloaded column == air == not powder snow (the faithful default; no false frost)
	}
	return s == block.ToStateID[block.PowderSnow{}]
}

// accumulateFrost ports the InsideBlockEffectType.FREEZE lambda (PowderSnowBlock.entityInside applies it
// while the entity is inside powder snow): setIsInPowderSnow(true) then, if canFreeze(), ADD 1 to
// ticksFrozen clamped at getTicksRequiredToFreeze() (Math.min(required, ticksFrozen + 1)). The
// setIsInPowderSnow(true) side is folded into entityInsidePowderSnow's proxy read (DEFERRAL 2). This is
// the ACCUMULATION side gated on the real powder-snow block read -- no fake frost is ever added.
//
//	[VERIFIED javap InsideBlockEffectType.lambda$static$0: setIsInPowderSnow(true); canFreeze ifeq ret;
//	 setTicksFrozen(Math.min(getTicksRequiredToFreeze(), getTicksFrozen()+1)).]
func (t *TickLoop) accumulateFrost(e *Entity) {
	if !t.entityCanFreeze(e) {
		return
	}
	n := getTicksFrozen(e) + 1
	if n > ticksRequiredToFreeze {
		n = ticksRequiredToFreeze // Math.min(getTicksRequiredToFreeze(), getTicksFrozen()+1)
	}
	t.setTicksFrozen(e, n)
}

// tickEntityFreeze ports the LivingEntity.aiStep FREEZE block (bytecode 666-779, the ServerLevel-gated
// slice) EXACTLY, in bytecode branch order:
//
//	if (this.isInPowderSnow && this.canFreeze()) { /* keep frost -- skip the reset */ }
//	else this.setTicksFrozen(Math.max(0, this.getTicksFrozen() - 2));   // decay by 2, clamped at 0
//	this.removeFrost();                                                 // frost speed-slow (DEFERRAL 1)
//	this.tryAddFrost();                                                 // frost speed-slow (DEFERRAL 1)
//	if (this.tickCount % 40 == 0 && this.isFullyFrozen() && this.canFreeze())
//	    this.hurtServer(serverLevel, this.damageSources().freeze(), 1.0F);
//
// The ACCUMULATION (the +1 while inside powder snow) is vanilla done in checkInsideBlocks BEFORE aiStep;
// with no inside-block dispatch yet (DEFERRAL 2) we fold it in here at the top, gated on the SAME real
// powder-snow block read the decay branch consults -- so a tick inside snow nets +1 (accumulate) then
// the keep-frost branch (no -2), and a tick outside nets -2 (decay), byte-matching vanilla's net.
//
// isInPowderSnow is the entityInsidePowderSnow proxy (DEFERRAL 2). tickCount is the mob's per-mob
// Entity.tickCount counter (e.ai.aiTickCount -- the same counter allay.go reads for tickCount % 10; it
// is advanced once per serverAiStep, so a call to tickEntityFreeze AFTER serverAiStep reads the same
// already-incremented value vanilla's aiStep sees). NO RNG anywhere.
//
//	[VERIFIED javap LivingEntity.aiStep offsets 697-771: isInPowderSnow && canFreeze keep-frost gate;
//	 else setTicksFrozen(Math.max(0, getTicksFrozen()-2)); removeFrost; tryAddFrost; tickCount%40==0
//	 && isFullyFrozen && canFreeze -> hurtServer(freeze(), 1.0F).]
func (t *TickLoop) tickEntityFreeze(e *Entity) {
	if e == nil || e.dead || e.health <= 0 {
		return // a corpse/dead entity does not run aiStep; and only living entities freeze (v1 gate).
	}
	inPowderSnow := t.entityInsidePowderSnow(e)

	// ACCUMULATION (DEFERRAL 2 fold-in of PowderSnowBlock.entityInside -> InsideBlockEffectType.FREEZE):
	// while inside powder snow, setIsInPowderSnow(true) + (canFreeze) +1 clamped at 140. Gated on the
	// real block read -- the oracle pig (never in snow) skips this entirely, so ticksFrozen stays 0.
	if inPowderSnow {
		t.accumulateFrost(e)
	}

	// The aiStep keep-vs-decay branch (bytecode 697-722): keep frost iff isInPowderSnow && canFreeze;
	// else decay by 2 clamped at 0.
	if inPowderSnow && t.entityCanFreeze(e) {
		// keep-frost branch: do not decay (the +1 above already applied this tick).
	} else {
		n := getTicksFrozen(e) - 2
		if n < 0 {
			n = 0 // Math.max(0, getTicksFrozen()-2)
		}
		t.setTicksFrozen(e, n)
	}

	// removeFrost(); tryAddFrost() (bytecode 725-732): the SPEED_MODIFIER_POWDER_SNOW MOVEMENT_SPEED
	// slow (-0.05 * getPercentFrozen). DEFERRAL 1 -- no generic transient AttributeModifier map yet, so
	// the speed slow is a cited stub. The frost damage below is the observable that is ported EXACTLY.
	//	[VERIFIED javap LivingEntity.removeFrost/tryAddFrost: MOVEMENT_SPEED.removeModifier /
	//	 addTransientModifier(SPEED_MODIFIER_POWDER_SNOW, -0.05f*getPercentFrozen, ADD_VALUE).]

	// The FREEZE damage gate (bytecode 733-771): tickCount % 40 == 0 && isFullyFrozen && canFreeze ->
	// hurtServer(freeze(), 1.0F). tickCount == the per-mob aiTickCount.
	tickCount := int32(0)
	if e.ai != nil {
		tickCount = int32(e.ai.aiTickCount)
	}
	if tickCount%freezeDamageInterval == 0 && isFullyFrozen(e) && t.entityCanFreeze(e) {
		t.applyDamageEntity(e, damageSourceOf(damageTypeFreeze), freezeDamageAmount)
	}
}

// ticksFrozenDataEntry builds the single SynchedEntityData$DataValue entry carrying Entity.DATA_TICKS_FROZEN
// (index 7, INT serializer -> VAR_INT codec), so a tracker's client updates the frost vignette. Framed on
// the wire as Byte(7) + VarInt(intSerializerID=1) + VarInt(ticksFrozen) (entityDataEntry.WriteTo).
//
//	[VERIFIED: Entity.defineId order puts DATA_TICKS_FROZEN at index 7 (0=SHARED_FLAGS ... 7=TICKS_FROZEN);
//	 EntityDataSerializers.INT == id 1 == ByteBufCodecs.VAR_INT (entity_encode.go dataAirSupplyIndex note).]
func ticksFrozenDataEntry(ticksFrozen int32) entityDataEntry {
	return entityDataEntry{
		index:        dataTicksFrozenIndex,
		serializerID: intSerializerID,
		value:        pk.VarInt(ticksFrozen),
	}
}

// dataTicksFrozenIndex is the SynchedEntityData accessor index for Entity.DATA_TICKS_FROZEN. Entity
// .defineId assigns indices sequentially in static-init order: 0=DATA_SHARED_FLAGS_ID, 1=DATA_AIR_SUPPLY_ID,
// 2=DATA_CUSTOM_NAME, 3=DATA_CUSTOM_NAME_VISIBLE, 4=DATA_SILENT, 5=DATA_NO_GRAVITY, 6=DATA_POSE,
// 7=DATA_TICKS_FROZEN -- the eighth accessor (entity_encode.go's dataAirSupplyIndex note documents the
// same 0..7 span, "7 = DATA_TICKS_FROZEN").
//
//	[VERIFIED: javap -c -p net.minecraft.world.entity.Entity static{} defineId order, index 7.]
const dataTicksFrozenIndex uint8 = 7
