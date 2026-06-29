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
)

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
