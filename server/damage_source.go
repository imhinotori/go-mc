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
	// damageTypeEnderPearl is minecraft:ender_pearl — the 5.0 self-hit the ender pearl deals on teleport.
	damageTypeEnderPearl = damageTypeID(tag.DamageTypeIDs["minecraft:ender_pearl"])
	// damageTypeDrown is minecraft:drown — the source DROWN carries (hurtServer(DROWN, 2.0F) in breath).
	damageTypeDrown = damageTypeID(tag.DamageTypeIDs["minecraft:drown"])
	// damageTypeDryOut is minecraft:dry_out -- the source a Dolphin/Axolotl takes when its moistness
	// runs out on land (Dolphin.tick: hurt(dryOut(), 1.0F)). Resolved by name from the generated table.
	damageTypeDryOut = damageTypeID(tag.DamageTypeIDs["minecraft:dry_out"])
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
	// damageTypeTrident is minecraft:trident -- the source ThrownTrident.onHitEntity deals
	// (DamageSources.trident, the projectile as direct entity, the shooter as causing). A member of
	// is_projectile (an indirect, projectile-attributed source). Cite ThrownTrident.onHitEntity.
	damageTypeTrident = damageTypeID(tag.DamageTypeIDs["minecraft:trident"])
	// damageTypeExplosion is minecraft:explosion — the source a MOB explosion (a creeper) deals via
	// ServerExplosion (Explosion.getDefaultDamageSource: type EXPLOSION, causingEntity = the source mob).
	damageTypeExplosion = damageTypeID(tag.DamageTypeIDs["minecraft:explosion"])
	// damageTypeMagic is minecraft:magic — the source instant-damage/poison effects deal
	// (damageSources().magic()). A bypasses_armor member (magic ignores the armor curve).
	damageTypeMagic = damageTypeID(tag.DamageTypeIDs["minecraft:magic"])
	// damageTypeIndirectMagic is minecraft:indirect_magic — the source a thrown-potion splash deals with a
	// thrower (damageSources().indirectMagic(potion, owner)). Also a bypasses_armor member.
	damageTypeIndirectMagic = damageTypeID(tag.DamageTypeIDs["minecraft:indirect_magic"])
	// damageTypeWither is minecraft:wither -- the source WitherMobEffect.applyEffectTick deals
	// (damageSources().wither(), 1.0 damage). A magic-like damage type but distinct from wither_skull.
	damageTypeWither = damageTypeID(tag.DamageTypeIDs["minecraft:wither"])
	// damageTypeLightning is minecraft:lightning_bolt — the source Entity.thunderHit deals when a
	// LightningBolt strikes an entity in range (damageSources().lightningBolt(), 5.0 damage). NOT a
	// bypasses_armor member (the victim folds the armor curve). Cite Entity.thunderHit + DamageSources
	// .lightningBolt (DamageTypes.LIGHTNING_BOLT).
	damageTypeLightning = damageTypeID(tag.DamageTypeIDs["minecraft:lightning_bolt"])
	// damageTypeFireball is minecraft:fireball — the source a Fireball (small/large ghast fireball) deals
	// via DamageSources.fireball(Fireball, Entity): type FIREBALL, causingEntity = the fireball's owner
	// (the ghast/blaze). An is_fire + is_projectile member. Cite SmallFireball/LargeFireball.onHitEntity.
	damageTypeFireball = damageTypeID(tag.DamageTypeIDs["minecraft:fireball"])
	// damageTypeWitherSkull is minecraft:wither_skull — the source WitherSkull.onHitEntity deals via
	// DamageSources.witherSkull(WitherSkull, Entity): type WITHER_SKULL, causingEntity = the owner (the
	// wither). An is_projectile member. Cite WitherSkull.onHitEntity (hurtServer(witherSkull, 8.0)).
	damageTypeWitherSkull = damageTypeID(tag.DamageTypeIDs["minecraft:wither_skull"])
	// damageTypeWindCharge is minecraft:wind_charge — the source AbstractWindCharge.onHitEntity deals via
	// DamageSources.windCharge(Entity, LivingEntity): type WIND_CHARGE, causingEntity = the owner (breeze /
	// the throwing player). The 1.0 direct hit; the gust knockback is a separate wind-burst explosion. Cite
	// AbstractWindCharge.onHitEntity (hurtServer(windCharge, 1.0)).
	damageTypeWindCharge = damageTypeID(tag.DamageTypeIDs["minecraft:wind_charge"])
	// damageTypeOutOfWorld is minecraft:out_of_world — the source DamageSources.fellOutOfWorld() deals
	// when an entity falls below minY-64 (Entity.checkBelowWorld -> LivingEntity.onBelowWorld:
	// hurt(fellOutOfWorld(), 4.0F)). A bypasses_invulnerability member, so it kills even a creative
	// player. Cite Entity.checkBelowWorld / LivingEntity.onBelowWorld / DamageSources.fellOutOfWorld.
	damageTypeOutOfWorld = damageTypeID(tag.DamageTypeIDs["minecraft:out_of_world"])
	// damageTypeLava is minecraft:lava — the source Entity.lavaHurt deals (hurtServer(lava(), 4.0F))
	// while an entity is in lava. An is_fire member, so a FIRE_RESISTANCE holder is immune via the
	// applyDamage/applyDamageEntity fire guard. Cite Entity.lavaHurt / DamageSources.lava.
	damageTypeLava = damageTypeID(tag.DamageTypeIDs["minecraft:lava"])
	// damageTypeThorns is minecraft:thorns — the source the Thorns enchant's DamageEntity effect
	// deals to the attacker (thorns.json post_attack: damage_entity damage_type minecraft:thorns,
	// causingEntity = the enchanted item's owner). A bypasses_shield + NO_KNOCKBACK-less member per
	// the damage_type JSON. Cite effects.DamageEntity.apply (new DamageSource(damageType, owner)).
	damageTypeThorns = damageTypeID(tag.DamageTypeIDs["minecraft:thorns"])
	// damageTypeCramming is minecraft:cramming — the source LivingEntity.pushEntities deals (6.0) when
	// too many entities overlap: `hurtServer(damageSources().cramming(), 6.0F)` once
	// list.size() > maxEntityCramming-1 non-passenger neighbours crowd the mob. No causing entity
	// (DamageSources.cramming() — causingEntity null). Cite LivingEntity.pushEntities /
	// DamageSources.cramming (DamageTypes.CRAMMING).
	damageTypeCramming = damageTypeID(tag.DamageTypeIDs["minecraft:cramming"])
	// damageTypeOutsideBorder is minecraft:outside_border — the source DamageSources.outOfBorder()
	// deals when a player's bounding box leaves the world border past the safe zone: the
	// LivingEntity.baseTick border branch hurts the player with max(1, floor(-distance * damagePerBlock)).
	// NOT a bypasses_invulnerability member, so a creative/spectator player (abilities.invulnerable) is
	// immune (matching vanilla — the border only bites survival). Cite LivingEntity.baseTick /
	// DamageSources.outOfBorder (DamageTypes.OUTSIDE_BORDER).
	damageTypeOutsideBorder = damageTypeID(tag.DamageTypeIDs["minecraft:outside_border"])
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
	// hitBone is the MODEL-M5 (G.1 / H.2.3) per-bone hit resolution: the name of the model bone a
	// native per-bone raycast resolved this hit to ("" == an entity-level hit, the vanilla observable).
	// It is NOT a vanilla DamageSource field -- it is Sulfur plugin-layer surface (the model system is
	// NEW, free of the 1:1 mandate) threaded here so applyDamageEntity's "damaged" trigger can carry
	// the bone into the skill-condition context (the hit_bone condition -> headshot skills). Every
	// vanilla/non-model damage path leaves it "" (the zero value) -> the pig oracle is byte-identical.
	hitBone string
}

// is is the port of DamageSource.is(TagKey<DamageType>) == type.is(tag): the source's damage-type id
// is a member of the named tag set. Reads the genuine generated table (data/tag.DamageTypeTags, Plan
// 01) — never a const false (PITFALLS Pitfall 3, the keystone deliverable). A nil/absent tag set
// yields false (the zero value of the inner map read), exactly as Holder.is over a tag with no members.
func (s damageSource) is(tagName string) bool {
	return tag.DamageTypeTags[tagName][int32(s.typeTag)]
}

// indirectDamageTypes are the damage types this server constructs with a DIRECT entity (the
// projectile/potion) DISTINCT from the CAUSING entity (the shooter/thrower). DamageSource.isDirect()
// == (causingEntity == directEntity) [VERIFIED javap DamageSource.isDirect]; the thin damageSource
// value carries only the causing entity, so directness is derived from HOW each source is built:
// every melee/environmental constructor sets directEntity == causingEntity in vanilla (isDirect
// true — for an environmental source both are null, and null == null), while the projectile
// constructors (DamageSources.arrow/fireball/witherSkull/windCharge/indirectMagic) pass the
// projectile as the direct entity and the owner as causing (isDirect false).
var indirectDamageTypes = map[damageTypeID]bool{
	damageTypeArrow:         true,
	damageTypeTrident:       true,
	damageTypeFireball:      true,
	damageTypeWitherSkull:   true,
	damageTypeWindCharge:    true,
	damageTypeIndirectMagic: true,
}

// isDirect ports DamageSource.isDirect(): causingEntity == directEntity — derived per the
// indirectDamageTypes table above (the Fire Aspect / Bane post-attack `is_direct` predicate read).
func (s damageSource) isDirect() bool {
	return !indirectDamageTypes[s.typeTag]
}

// damageSourceByTypeName builds a DamageSource for a datapack-named damage type (the enchant
// DamageEntity effect's `damage_type` field, e.g. "minecraft:thorns") with the given causing
// entity. An unknown name maps to id 0 only if absent from the generated table — the embedded
// 26.2 table carries every vanilla type, so this is a faithful direct construction.
func damageSourceByTypeName(name string, attackerID int32) damageSource {
	return damageSource{typeTag: damageTypeID(tag.DamageTypeIDs[name]), attacker: attackerID}
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

// damageSourceTrident builds the DamageSource for a ThrownTrident hit: type trident with the SHOOTER's
// entity id as the causing entity. The port of DamageSources.trident(ThrownTrident, Entity) -- type
// TRIDENT, causingEntity = the owner (the throwing player/mob). attackerID 0 means an ownerless trident
// (it attributes to the trident itself in vanilla; here 0 = anonymous). Cite ThrownTrident.onHitEntity:
// damageSources().trident(this, owner == null ? this : owner).
func damageSourceTrident(attackerID int32) damageSource {
	return damageSource{typeTag: damageTypeTrident, attacker: attackerID}
}

// damageSourceEnderPearl builds the DamageSource for the ender-pearl teleport self-hit: type ender_pearl,
// no attacker (the pearl damages its own thrower). CITE: ThrownEnderpearl.onHit hurtServer(enderPearl(), 5).
func damageSourceEnderPearl() damageSource {
	return damageSource{typeTag: damageTypeEnderPearl, attacker: 0}
}

// damageSourceFireball builds the DamageSource for a fireball hit: type fireball with the SHOOTER's
// entity id as the causing entity. The port of DamageSources.fireball(Fireball, Entity) — type FIREBALL,
// causingEntity = the owner (the ghast/blaze). ownerID 0 means an ownerless fireball. Cite
// SmallFireball/LargeFireball.onHitEntity: damageSources().fireball(this, owner).
func damageSourceFireball(ownerID int32) damageSource {
	return damageSource{typeTag: damageTypeFireball, attacker: ownerID}
}

// damageSourceWitherSkull builds the DamageSource for a wither-skull hit: type wither_skull with the
// SHOOTER's entity id. The port of DamageSources.witherSkull(WitherSkull, Entity) — type WITHER_SKULL,
// causingEntity = the owner (the wither). Cite WitherSkull.onHitEntity: damageSources().witherSkull(this, living).
func damageSourceWitherSkull(ownerID int32) damageSource {
	return damageSource{typeTag: damageTypeWitherSkull, attacker: ownerID}
}

// damageSourceWindCharge builds the DamageSource for a wind-charge direct hit: type wind_charge with the
// SHOOTER's entity id (breeze / throwing player). The port of DamageSources.windCharge(Entity, LivingEntity)
// — the 1.0 hit dealt in AbstractWindCharge.onHitEntity (the gust knockback is separate). ownerID 0 = anonymous.
func damageSourceWindCharge(ownerID int32) damageSource {
	return damageSource{typeTag: damageTypeWindCharge, attacker: ownerID}
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

// damageSourceWither builds the DamageSource WitherMobEffect.applyEffectTick uses:
// DamageSources.wither(), type minecraft:wither, no causing entity.
func damageSourceWither() damageSource {
	return damageSource{typeTag: damageTypeWither, attacker: 0}
}

// damageSourceLightning builds the DamageSource a LightningBolt deals via Entity.thunderHit: type
// lightning_bolt, no causing entity. The port of DamageSources.lightningBolt() — the source is a bare
// environmental damage type (causingEntity null; the bolt itself is not recorded as the attacker in
// vanilla's DamageSources.lightningBolt()). Cite Entity.thunderHit: hurtServer(damageSources()
// .lightningBolt(), 5.0F).
func damageSourceLightning() damageSource {
	return damageSource{typeTag: damageTypeLightning, attacker: 0}
}

// damageSourceCramming builds the DamageSource entity cramming deals: type cramming, no attacker
// (DamageSources.cramming() — causingEntity null). The 6.0 hit LivingEntity.pushEntities applies
// when list.size() > maxEntityCramming-1 non-passenger neighbours crowd the mob. Cite
// LivingEntity.pushEntities: hurtServer(damageSources().cramming(), 6.0F).
func damageSourceCramming() damageSource {
	return damageSource{typeTag: damageTypeCramming, attacker: 0}
}
