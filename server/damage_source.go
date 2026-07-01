package server

// damage_source.go — the real ported DamageSource value type (MOB-SUB-02). A literal port of
// net.minecraft.world.damagesource.DamageSource as a plain value (a damage-type id + an optional
// attacker entity id), so a mob can record its genuine lastDamageSource — NOT a faked hurt flag
// (deferred-goals.md "do NOT fake a hurt flag"). PanicGoal (Phase 31, a tag read) and wolf
// anger-on-hit (Phase 36, an attacker read) consume this with no later refactor.
//
// JAR AUTHORITY (javap -c -p net.minecraft.world.damagesource.DamageSource, temp/cache/26.2-inner.jar
// this session):
//
//	public boolean is(TagKey<DamageType> tag) { return this.type.is(tag); }   // bytecode: getfield type;
//	                                                                          //   Holder.is(tag)
//	public Entity getEntity() { return this.causingEntity; }                  // bytecode: getfield causingEntity
//
// DamageSource.is(tag) is type.is(tag) — the damage-type Holder's membership in the tag set, which is
// exactly the generated data/tag.DamageTypeTags membership map (Plan 01). getEntity() returns
// causingEntity, modeled here as a THIN entity id (never a live *Entity pointer — the Folia rule: a
// value travels across the barrier with the *Entity with no special handling, and a goal never holds a
// live source).

import (
	"github.com/imhinotori/sulfur/data/tag"
)

// damageTypeID is the internal, generated damage-type id (data/tag.DamageTypeIDs). It is the index of
// the element in the sorted minecraft:damage_type registry element set — server-side DamageSource
// state ONLY, never a wire registry id (minecraft:damage_type is a dynamic/datapack registry with no
// static protocol id; Plan 01-SUMMARY). Consumers + tests resolve by NAME via DamageTypeIDs, never a
// hardcoded literal.
type damageTypeID int32

// damageType* are the named consts Phase 29 uses, resolved from the generated data/tag.DamageTypeIDs
// table (so the ids stay in lockstep with the regenerated jar data — never invented). At minimum the
// plan needs player_attack (the melee source), mob_attack (mob->X), and fall (a bypasses_armor source
// the bypass branch + PanicGoal read).
var (
	// damageTypePlayerAttack is minecraft:player_attack — a member of is_player_attack / panic_causes,
	// NOT a member of bypasses_armor (so a player melee hit folds the armor curve).
	damageTypePlayerAttack = damageTypeID(tag.DamageTypeIDs["minecraft:player_attack"])
	// damageTypeMobAttack is minecraft:mob_attack — the generic mob melee source (panic_causes member).
	damageTypeMobAttack = damageTypeID(tag.DamageTypeIDs["minecraft:mob_attack"])
	// damageTypeFall is minecraft:fall — a bypasses_armor + is_fall member (the bypass branch reads it).
	damageTypeFall = damageTypeID(tag.DamageTypeIDs["minecraft:fall"])
	// damageTypeDrown is minecraft:drown — the source DROWN carries (hurtServer(DROWN, 2.0F) in breath).
	damageTypeDrown = damageTypeID(tag.DamageTypeIDs["minecraft:drown"])
	// damageTypeStarve is minecraft:starve — the source the hunger-starvation tick carries.
	damageTypeStarve = damageTypeID(tag.DamageTypeIDs["minecraft:starve"])
	// damageTypeInWall is minecraft:in_wall — the source suffocation (block-in-eye) carries.
	damageTypeInWall = damageTypeID(tag.DamageTypeIDs["minecraft:in_wall"])
	// damageTypeGeneric is minecraft:generic — the catch-all source for an attributed-less hit (the
	// debug /damage command, which carries no real DamageType source in v1).
	damageTypeGeneric = damageTypeID(tag.DamageTypeIDs["minecraft:generic"])
	// damageTypeOnFire is minecraft:on_fire — the source Entity.baseTick deals every 20 fire ticks
	// (damageSources().onFire(), 1.0 damage) while remainingFireTicks > 0.
	damageTypeOnFire = damageTypeID(tag.DamageTypeIDs["minecraft:on_fire"])
	// damageTypeArrow is minecraft:arrow — the source AbstractArrow.onHitEntity deals
	// (damageSources().arrow(this, owner)). NOT a bypasses_armor member (the victim folds the armor curve).
	damageTypeArrow = damageTypeID(tag.DamageTypeIDs["minecraft:arrow"])
	// damageTypeExplosion is minecraft:explosion — the source a MOB explosion (a creeper) deals via
	// ServerExplosion (Explosion.getDefaultDamageSource: type EXPLOSION, causingEntity = the source mob).
	damageTypeExplosion = damageTypeID(tag.DamageTypeIDs["minecraft:explosion"])
	// damageTypeMagic is minecraft:magic — the source instant-damage/poison effects deal
	// (damageSources().magic()). A bypasses_armor member (magic ignores the armor curve).
	damageTypeMagic = damageTypeID(tag.DamageTypeIDs["minecraft:magic"])
	// damageTypeIndirectMagic is minecraft:indirect_magic — the source a thrown-potion splash deals with a
	// thrower (damageSources().indirectMagic(potion, owner)). Also a bypasses_armor member.
	damageTypeIndirectMagic = damageTypeID(tag.DamageTypeIDs["minecraft:indirect_magic"])
)

// damageSourceOf builds a DamageSource for an environmental/anonymous source: the given damage-type
// id with no causing entity (attacker 0 == none, exactly DamageSources.<env>() whose causingEntity is
// null). The port of e.g. DamageSources.drown()/starve()/inWall()/fall()/generic().
func damageSourceOf(typeID damageTypeID) damageSource {
	return damageSource{typeTag: typeID, attacker: 0}
}

// damageSource is the ported DamageSource value: a damage-type id + the causing entity's id. It is a
// PLAIN VALUE (an int enum + an int32, NO pointers), so it satisfies the entity.go snapshot-friendly
// contract — it travels with the *Entity at the barrier with no special handling (the Folia rule).
type damageSource struct {
	// typeTag is the damage-type id (DamageSource.type Holder). is(tag) reads its membership.
	typeTag damageTypeID
	// attacker is the entity id of the causing entity (DamageSource.getEntity == causingEntity);
	// 0 = none (an environmental/anonymous source). NEVER a live *Entity pointer (the Folia rule).
	attacker int32
}

// is is the port of DamageSource.is(TagKey<DamageType>) == type.is(tag): the source's damage-type id
// is a member of the named tag set. Reads the genuine generated table (data/tag.DamageTypeTags, Plan
// 01) — never a const false (PITFALLS Pitfall 3, the keystone deliverable). A nil/absent tag set
// yields false (the zero value of the inner map read), exactly as Holder.is over a tag with no members.
func (s damageSource) is(tagName string) bool {
	return tag.DamageTypeTags[tagName][int32(s.typeTag)]
}

// damageSourcePlayerAttack builds the DamageSource for a player melee hit: type player_attack with the
// given attacker entity id. The port of DamageSources.playerAttack(Player) (type PLAYER_ATTACK,
// causingEntity = the attacking player). attackerID 0 means an anonymous attacker (e.g. a test).
func damageSourcePlayerAttack(attackerID int32) damageSource {
	return damageSource{typeTag: damageTypePlayerAttack, attacker: attackerID}
}

// damageSourceMobAttack builds the DamageSource for a MOB melee hit: type mob_attack with the given
// attacker (the mob) entity id. The port of DamageSources.mobAttack(LivingEntity) — the source a
// no-weapon Mob.doHurtTarget builds (getWeaponItem().getDamageSource(this) for an empty weapon resolves
// to the generic mob attack source, type MOB_ATTACK, causingEntity = the attacking mob). attackerID is
// the host-set mob id (never plugin-forgeable, T-35-02). mob_attack is a panic_causes member (so a hit
// mob's PanicGoal still reads it) and NOT a bypasses_armor member (so the victim folds the armor curve).
func damageSourceMobAttack(attackerID int32) damageSource {
	return damageSource{typeTag: damageTypeMobAttack, attacker: attackerID}
}

// damageSourceArrow builds the DamageSource for an arrow hit: type arrow with the SHOOTER's entity id as
// the causing entity. The port of DamageSources.arrow(AbstractArrow, Entity) — type ARROW, causingEntity
// = the owner (the shooting mob/player). attackerID 0 means an ownerless arrow (it attributes to itself
// in vanilla; here 0 = anonymous). Cite AbstractArrow.onHitEntity: damageSources().arrow(this, owner).
func damageSourceArrow(attackerID int32) damageSource {
	return damageSource{typeTag: damageTypeArrow, attacker: attackerID}
}

// damageSourceMagic builds the DamageSource for a direct magic effect (instant_damage / poison self-tick):
// type magic, no attacker (DamageSources.magic() — causingEntity null). A bypasses_armor source.
func damageSourceMagic() damageSource {
	return damageSource{typeTag: damageTypeMagic, attacker: 0}
}

// damageSourceIndirectMagic builds the DamageSource for a thrown-potion splash harm attributed to the
// thrower (owner): type indirect_magic, causingEntity = owner. The port of DamageSources.indirectMagic.
func damageSourceIndirectMagic(ownerID int32) damageSource {
	return damageSource{typeTag: damageTypeIndirectMagic, attacker: ownerID}
}
