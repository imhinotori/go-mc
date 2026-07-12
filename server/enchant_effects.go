package server

// enchant_effects.go — the ENCHANTMENT-EFFECT RUNTIME (divergence-audit E-3): the 1:1 port of the
// 26.2 data-driven enchantment effect system that makes Sharpness/Smite/Bane add melee damage,
// Protection reduce damage, Knockback push harder, Fire Aspect ignite, Thorns reflect, and Mending
// repair from XP orbs. The enchantment JSONs (server/registrydata/registries/enchantment/*.json —
// the SAME embedded datapack content the server sends the client) are parsed ONCE into the
// LevelBasedValue / EnchantmentValueEffect / EnchantmentEntityEffect / LootItemCondition model this
// file interprets, exactly mirroring vanilla's codec-decoded Enchantment.effects() component map.
//
// JAR AUTHORITY (all javap-verified against temp/cache/26.2-inner.jar this session, cited per
// function):
//
//   net.minecraft.world.item.enchantment.LevelBasedValue$Constant/Linear/Clamped/Lookup/Fraction
//   net.minecraft.world.item.enchantment.effects.AddValue/MultiplyValue/AllOf$ValueEffects
//   net.minecraft.world.item.enchantment.effects.Ignite/DamageEntity/ChangeItemDamage/AllOf$EntityEffects
//   net.minecraft.world.item.enchantment.Enchantment.modifyDamage/modifyDamageProtection/
//     modifyKnockback/modifyDurabilityToRepairFromXp/doPostAttack/matchingSlot/applyEffects
//   net.minecraft.world.item.enchantment.EnchantmentHelper.runIterationOnItem (both overloads),
//     runIterationOnEquipment, modifyDamage, getDamageProtection, modifyKnockback,
//     doPostAttackEffects[WithItemSource[OnBreak]], modifyDurabilityToRepairFromXp, getRandomItemWith
//   net.minecraft.advancements.predicates.DamageSourcePredicate.matches + DamageSource.isDirect
//   net.minecraft.world.level.storage.loot.predicates.LootItemRandomChanceCondition.test
//   net.minecraft.util.Mth.randomBetween
//
// TICK-05: the parsed effect table is built lazily once (sync.Once) then read-only; every
// application runs on the tick goroutine over tick-owned state. RNG DISCIPLINE (the pig oracle):
// NO code path here draws ANY random number unless an enchanted item is actually present — an
// un-enchanted stack iterates zero entries (runIterationOnItem over the EMPTY component), so the
// oracle streams are untouched.

import (
	"encoding/json"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// ============================================================================================
// LevelBasedValue — net.minecraft.world.item.enchantment.LevelBasedValue
// ============================================================================================

// enchLevelValue is LevelBasedValue: a per-level float curve.
type enchLevelValue interface {
	calculate(level int) float32
}

// lbvConstant is LevelBasedValue$Constant: `return value;` (a bare JSON number).
type lbvConstant float32

func (v lbvConstant) calculate(int) float32 { return float32(v) }

// lbvLinear is LevelBasedValue$Linear: `return base + perLevelAboveFirst * (level - 1);`
// (the i2f widening on (level-1) matches the bytecode's fmul operand order).
type lbvLinear struct{ base, perLevelAboveFirst float32 }

func (v lbvLinear) calculate(level int) float32 {
	return v.base + v.perLevelAboveFirst*float32(level-1)
}

// lbvClamped is LevelBasedValue$Clamped: `return Mth.clamp(value.calculate(level), min, max);`
type lbvClamped struct {
	value    enchLevelValue
	min, max float32
}

func (v lbvClamped) calculate(level int) float32 {
	return mthClampF(v.value.calculate(level), v.min, v.max)
}

// lbvLookup is LevelBasedValue$Lookup:
// `return level <= values.size() ? values.get(level - 1) : fallback.calculate(level);`
type lbvLookup struct {
	values   []float32
	fallback enchLevelValue
}

func (v lbvLookup) calculate(level int) float32 {
	if level <= len(v.values) {
		return v.values[level-1]
	}
	return v.fallback.calculate(level)
}

// lbvFraction is LevelBasedValue$Fraction:
// `float d = denominator.calculate(level); return d == 0.0F ? 0.0F : numerator.calculate(level) / d;`
type lbvFraction struct{ numerator, denominator enchLevelValue }

func (v lbvFraction) calculate(level int) float32 {
	d := v.denominator.calculate(level)
	if d == 0.0 {
		return 0.0
	}
	return v.numerator.calculate(level) / d
}

// parseLevelValue decodes a LevelBasedValue JSON form: a bare number is a Constant
// (LevelBasedValue.CODEC's either(FLOAT, dispatch)); an object dispatches on "type". An unknown
// type yields nil (the caller skips the enclosing effect — only reachable for effect components
// this runtime does not consume; every component it DOES consume uses the five types above).
func parseLevelValue(raw json.RawMessage) enchLevelValue {
	var f float32
	if err := json.Unmarshal(raw, &f); err == nil {
		return lbvConstant(f)
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	switch head.Type {
	case "minecraft:constant":
		var body struct {
			Value float32 `json:"value"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		return lbvConstant(body.Value)
	case "minecraft:linear":
		var body struct {
			Base               float32 `json:"base"`
			PerLevelAboveFirst float32 `json:"per_level_above_first"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		return lbvLinear{base: body.Base, perLevelAboveFirst: body.PerLevelAboveFirst}
	case "minecraft:clamped":
		var body struct {
			Value json.RawMessage `json:"value"`
			Min   float32         `json:"min"`
			Max   float32         `json:"max"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		inner := parseLevelValue(body.Value)
		if inner == nil {
			return nil
		}
		return lbvClamped{value: inner, min: body.Min, max: body.Max}
	case "minecraft:lookup":
		var body struct {
			Values   []float32       `json:"values"`
			Fallback json.RawMessage `json:"fallback"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		fb := parseLevelValue(body.Fallback)
		if fb == nil {
			return nil
		}
		return lbvLookup{values: body.Values, fallback: fb}
	case "minecraft:fraction":
		var body struct {
			Numerator   json.RawMessage `json:"numerator"`
			Denominator json.RawMessage `json:"denominator"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		n, d := parseLevelValue(body.Numerator), parseLevelValue(body.Denominator)
		if n == nil || d == nil {
			return nil
		}
		return lbvFraction{numerator: n, denominator: d}
	}
	return nil // levels_squared/... : not used by any consumed effect component (cited)
}

// ============================================================================================
// EnchantmentValueEffect — net.minecraft.world.item.enchantment.effects.*
// ============================================================================================

// enchRNG is the RandomSource slice the value/entity effects draw from. nextFloat backs the
// FloatAction value/entity effects (thorns/damage_entity, apply_mob_effect's Mth.randomBetween);
// nextInt backs HolderSet.getRandomElement (Util.getRandomSafe -> list.get(random.nextInt(size)))
// in apply_mob_effect. Adapters wrap the player/mob/level sources below.
type enchRNG interface {
	nextFloat() float32
	nextInt(n int) int
}

// enchValueEffect is EnchantmentValueEffect.process(int level, RandomSource, float value).
type enchValueEffect interface {
	process(level int, rng enchRNG, value float32) float32
}

// veAdd is AddValue: `return value + this.value.calculate(level);`
type veAdd struct{ value enchLevelValue }

func (e veAdd) process(level int, _ enchRNG, value float32) float32 {
	return value + e.value.calculate(level)
}

// veMultiply is MultiplyValue: `return value * factor.calculate(level);`
type veMultiply struct{ factor enchLevelValue }

func (e veMultiply) process(level int, _ enchRNG, value float32) float32 {
	return value * e.factor.calculate(level)
}

// veAllOf is AllOf$ValueEffects: fold value through each inner effect in list order.
type veAllOf struct{ effects []enchValueEffect }

func (e veAllOf) process(level int, rng enchRNG, value float32) float32 {
	for _, inner := range e.effects {
		value = inner.process(level, rng, value)
	}
	return value
}

// veSet is SetValue: `return this.value.calculate(level);` (the input is replaced).
type veSet struct{ value enchLevelValue }

func (e veSet) process(level int, _ enchRNG, _ float32) float32 { return e.value.calculate(level) }

// parseValueEffect decodes an EnchantmentValueEffect by its "type". An unknown type (e.g.
// remove_binomial — Unbreaking's item_damage, which durability.go already ports on its own path)
// yields nil and the enclosing conditional effect is skipped.
func parseValueEffect(raw json.RawMessage) enchValueEffect {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	switch head.Type {
	case "minecraft:add":
		var body struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		if v := parseLevelValue(body.Value); v != nil {
			return veAdd{value: v}
		}
	case "minecraft:multiply":
		var body struct {
			Factor json.RawMessage `json:"factor"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		if v := parseLevelValue(body.Factor); v != nil {
			return veMultiply{factor: v}
		}
	case "minecraft:set":
		var body struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		if v := parseLevelValue(body.Value); v != nil {
			return veSet{value: v}
		}
	case "minecraft:all_of":
		var body struct {
			Effects []json.RawMessage `json:"effects"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		out := veAllOf{}
		for _, r := range body.Effects {
			inner := parseValueEffect(r)
			if inner == nil {
				return nil
			}
			out.effects = append(out.effects, inner)
		}
		return out
	}
	return nil
}

// ============================================================================================
// LootItemConditions — the "requirements" evaluator over the enchantment damage/item context
// ============================================================================================

// enchDamageCtx is the slice of vanilla's LootContext (Enchantment.damageContext) the shipped
// enchantment conditions read: THIS_ENTITY's type (the victim entity passed to damageContext),
// DAMAGE_SOURCE, the ENCHANTMENT_LEVEL param, and the level RandomSource (LootContext.getRandom()
// == ServerLevel.random — the region levelRandom).
type enchDamageCtx struct {
	level      int          // LootContextParams.ENCHANTMENT_LEVEL
	thisType   string       // LootContextParams.THIS_ENTITY's entity-type resource id
	directType string       // LootContextParams.DIRECT_ATTACKER's entity-type id (DamageSource.getDirectEntity)
	src        damageSource // LootContextParams.DAMAGE_SOURCE
	t          *TickLoop    // the loop, for the LAZY level-RNG resolve below

	levelRNG         enchRNG // cached LootContext.getRandom() (resolved on first draw)
	levelRNGResolved bool
}

// getLevelRNG lazily resolves LootContext.getRandom() (ServerLevel.random — the region levelRandom
// via the tolerant only() resolver, which never panics off the fan-out). LAZY on purpose: it is
// touched ONLY when a random_chance condition actually draws (an enchanted thorns hit), so the
// common un-enchanted path resolves no region and draws nothing (the pig-oracle discipline).
func (c *enchDamageCtx) getLevelRNG() enchRNG {
	if !c.levelRNGResolved {
		c.levelRNGResolved = true
		if c.t != nil {
			c.levelRNG = c.t.enchLevelRNG()
		}
	}
	return c.levelRNG
}

// enchCondition is LootItemCondition.test(LootContext) over the ctx slice above.
type enchCondition interface {
	matches(c *enchDamageCtx) bool
}

// condAllOf is AllOfCondition: every term matches. condAnyOf is AnyOfCondition: any term matches.
// condInverted is InvertedLootItemCondition: !term.
type condAllOf struct{ terms []enchCondition }

func (c condAllOf) matches(ctx *enchDamageCtx) bool {
	for _, t := range c.terms {
		if !t.matches(ctx) {
			return false
		}
	}
	return true
}

type condAnyOf struct{ terms []enchCondition }

func (c condAnyOf) matches(ctx *enchDamageCtx) bool {
	for _, t := range c.terms {
		if t.matches(ctx) {
			return true
		}
	}
	return false
}

type condInverted struct{ term enchCondition }

func (c condInverted) matches(ctx *enchDamageCtx) bool { return !c.term.matches(ctx) }

// condDamageSourceProps is DamageSourceCondition -> DamageSourcePredicate.matches: every TagPredicate
// (source.type.is(tag) == expected) must hold, and when isDirect is present it must equal
// DamageSource.isDirect() (== causingEntity == directEntity). The directEntity/sourceEntity
// EntityPredicates do not occur in the shipped enchantment data (cited).
//
//	[VERIFIED javap DamageSourcePredicate.matches: per-tag TagPredicate.matches(typeHolder) else
//	 false; isDirect present -> booleanValue != source.isDirect() -> false; else true.
//	 DamageSource.isDirect: causingEntity == directEntity.]
type condTagExpect struct {
	id       string // "minecraft:bypasses_invulnerability" etc. (a damage_type tag)
	expected bool
}

type condDamageSourceProps struct {
	tags     []condTagExpect
	isDirect *bool
}

func (c condDamageSourceProps) matches(ctx *enchDamageCtx) bool {
	for _, tp := range c.tags {
		// TagPredicate.matches(holder): holder.is(tag) == expected. src.is takes the bare tag name.
		if ctx.src.is(bareTagName(tp.id)) != tp.expected {
			return false
		}
	}
	if c.isDirect != nil && *c.isDirect != ctx.src.isDirect() {
		return false
	}
	return true
}

// condEntityType is LootItemEntityPropertyCondition (entity: "this") with an EntityPredicate whose
// only used field is entityType (EntityTypePredicate): the THIS_ENTITY's type must be the named id
// or a member of the "#tag" (tags/entity_type). Any OTHER EntityPredicate field in the JSON makes
// the predicate unsupported (parseCondition yields condUnsupported) — none of the consumed effect
// components carry one (cited: smite/bane use entity_type only).
type condEntityType struct {
	ref    string // "#minecraft:sensitive_to_smite" or a bare "minecraft:<type>"
	target string // the LootContext.EntityTarget: "this" (victim) or "direct_attacker"
}

// condEntityTypeOf resolves which entity's type this predicate reads: THIS_ENTITY (the victim) for the
// "this" target (smite/bane), or the DIRECT_ATTACKER (DamageSource.getDirectEntity — the arrow itself)
// for the "direct_attacker" target (Power/Punch's `entity_type: #minecraft:arrows` gate). Cite
// LootContext.EntityTarget.THIS/DIRECT_ATTACKER + Enchantment.damageContext param population.
func (c condEntityType) matches(ctx *enchDamageCtx) bool {
	typeName := ctx.thisType
	if c.target == "direct_attacker" {
		typeName = ctx.directType
	}
	if typeName == "" {
		return false // the target entity is absent from the context -> the predicate cannot match
	}
	if len(c.ref) > 0 && c.ref[0] == '#' {
		ok, err := registrydata.EntityTypeInTag(typeName, c.ref)
		return err == nil && ok
	}
	return typeName == c.ref
}

// condRandomChance is LootItemRandomChanceCondition.test:
// `return context.getRandom().nextFloat() < chance.getFloat(context);` — the chance NumberProvider
// is EnchantmentLevelProvider (amount.calculate(ENCHANTMENT_LEVEL)) in the shipped data (thorns:
// linear 0.15/level), or a plain float. Drawn from the LEVEL random (LootContext.getRandom()).
// A nil levelRNG (a bare test loop with no region) yields false — the effect is skipped, never a
// panic; combat tests seed the region levelRandom.
//
//	[VERIFIED javap LootItemRandomChanceCondition.test: chance.getFloat(ctx); ctx.getRandom()
//	 .nextFloat(); fcmpg ifge -> false.]
type condRandomChance struct {
	amount enchLevelValue // EnchantmentLevelProvider.amount (or a Constant for a plain float)
}

func (c condRandomChance) matches(ctx *enchDamageCtx) bool {
	rng := ctx.getLevelRNG()
	if rng == nil {
		return false
	}
	return rng.nextFloat() < c.amount.calculate(ctx.level)
}

// condUnsupported marks a condition kind this runtime does not evaluate (weather_check,
// location_check, match_tool, enchantment_active_check, movement/flag entity predicates — none
// gate the six consumed effect kinds). It conservatively never matches, so an unconsumed exotic
// effect stays dormant rather than half-applying.
type condUnsupported struct{}

func (condUnsupported) matches(*enchDamageCtx) bool { return false }

// parseCondition decodes a LootItemCondition by its "condition" id.
func parseCondition(raw json.RawMessage) enchCondition {
	var head struct {
		Condition string `json:"condition"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return condUnsupported{}
	}
	switch head.Condition {
	case "minecraft:all_of", "minecraft:any_of":
		var body struct {
			Terms []json.RawMessage `json:"terms"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return condUnsupported{}
		}
		var terms []enchCondition
		for _, t := range body.Terms {
			terms = append(terms, parseCondition(t))
		}
		if head.Condition == "minecraft:all_of" {
			return condAllOf{terms: terms}
		}
		return condAnyOf{terms: terms}
	case "minecraft:inverted":
		var body struct {
			Term json.RawMessage `json:"term"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return condUnsupported{}
		}
		return condInverted{term: parseCondition(body.Term)}
	case "minecraft:damage_source_properties":
		var body struct {
			Predicate struct {
				Tags []struct {
					ID       string `json:"id"`
					Expected bool   `json:"expected"`
				} `json:"tags"`
				IsDirect *bool `json:"is_direct"`
			} `json:"predicate"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return condUnsupported{}
		}
		out := condDamageSourceProps{isDirect: body.Predicate.IsDirect}
		for _, tp := range body.Predicate.Tags {
			out.tags = append(out.tags, condTagExpect{id: tp.ID, expected: tp.Expected})
		}
		return out
	case "minecraft:entity_properties":
		var body struct {
			Entity    string                     `json:"entity"`
			Predicate map[string]json.RawMessage `json:"predicate"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return condUnsupported{}
		}
		// A pure entity_type predicate on THIS_ENTITY (smite/bane) or DIRECT_ATTACKER (Power/Punch's
		// `entity_type: #minecraft:arrows` gate — the arrow is source.getDirectEntity) is evaluated.
		// Any other target or a compound predicate -> unsupported (never matches).
		if body.Entity != "this" && body.Entity != "direct_attacker" {
			return condUnsupported{}
		}
		if len(body.Predicate) != 1 {
			return condUnsupported{}
		}
		typeRaw, ok := body.Predicate["minecraft:entity_type"]
		if !ok {
			return condUnsupported{}
		}
		var ref string
		if json.Unmarshal(typeRaw, &ref) != nil {
			return condUnsupported{}
		}
		return condEntityType{ref: ref, target: body.Entity}
	case "minecraft:random_chance":
		var body struct {
			Chance json.RawMessage `json:"chance"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return condUnsupported{}
		}
		// A plain float, or the EnchantmentLevelProvider {type: enchantment_level, amount: LBV}.
		var f float32
		if json.Unmarshal(body.Chance, &f) == nil {
			return condRandomChance{amount: lbvConstant(f)}
		}
		var prov struct {
			Type   string          `json:"type"`
			Amount json.RawMessage `json:"amount"`
		}
		if json.Unmarshal(body.Chance, &prov) == nil && prov.Type == "minecraft:enchantment_level" {
			if v := parseLevelValue(prov.Amount); v != nil {
				return condRandomChance{amount: v}
			}
		}
		return condUnsupported{}
	}
	return condUnsupported{}
}

// bareTagName strips the "#"/"minecraft:" prefixes off a tag reference — damageSource.is and the
// tag tables key by the bare name ("bypasses_invulnerability").
func bareTagName(id string) string {
	if len(id) > 0 && id[0] == '#' {
		id = id[1:]
	}
	const ns = "minecraft:"
	if len(id) > len(ns) && id[:len(ns)] == ns {
		id = id[len(ns):]
	}
	return id
}

// ============================================================================================
// EnchantmentEntityEffect — the post_attack payloads (Ignite / DamageEntity / ChangeItemDamage)
// ============================================================================================

// enchEntityRef names one live entity for the post-attack application: exactly one of player/mob
// is non-nil (the Sulfur split of vanilla's single Entity hierarchy).
type enchEntityRef struct {
	player *tickPlayer
	mob    *Entity
}

func (r enchEntityRef) valid() bool { return r.player != nil || r.mob != nil }

// entityID is the wire entity id (DamageSource.causingEntity attribution).
func (r enchEntityRef) entityID() int32 {
	if r.player != nil {
		return r.player.entityID
	}
	if r.mob != nil {
		return r.mob.id
	}
	return 0
}

// typeName is the entity-type resource id — THIS_ENTITY's EntityTypePredicate read.
func (r enchEntityRef) typeName() string {
	if r.player != nil {
		return "minecraft:player"
	}
	if r.mob != nil {
		if t := int(r.mob.typ); t >= 0 && t < len(registryid.EntityType) {
			return registryid.EntityType[t]
		}
	}
	return ""
}

// getRandom is Entity.getRandom(): the mob's per-entity source, or the player's own RandomSource
// (Player.random — playerEnchantRandom, lazily built exactly as the enchant-menu path does).
func (r enchEntityRef) getRandom() enchRNG {
	if r.player != nil {
		ensurePlayerEnchantState(r.player)
		return legacyRNGAdapter{r.player.playerEnchantRandom}
	}
	if r.mob != nil {
		return mobRandom(r.mob)
	}
	return nil
}

// legacyRNGAdapter adapts *legacyRandom (java.util.Random port) to enchRNG.
type legacyRNGAdapter struct{ r *legacyRandom }

func (a legacyRNGAdapter) nextFloat() float32 { return a.r.nextFloat() }
func (a legacyRNGAdapter) nextInt(n int) int  { return int(a.r.nextIntN(int32(n))) }

// enchLevelRNG is LootContext.getRandom() / ServerLevel.getRandom(): the resolved region's
// levelRandom via the TOLERANT only() resolver — never a panic off the fan-out (a dispatch-time
// attack resolves region 0, matching the other tolerant level-RNG sites). nil when the region has
// no seeded levelRandom (a bare test loop — see condRandomChance).
func (t *TickLoop) enchLevelRNG() enchRNG {
	if r := t.only(); r != nil && r.levelRandom != nil {
		return levelRNGAdapter{r.levelRandom}
	}
	return nil
}

type levelRNGAdapter struct {
	r interface {
		NextFloat() float32
		NextIntN(int32) int32
	}
}

func (a levelRNGAdapter) nextFloat() float32 { return a.r.NextFloat() }
func (a levelRNGAdapter) nextInt(n int) int  { return int(a.r.NextIntN(int32(n))) }

// enchItemInUse is net.minecraft.world.item.enchantment.EnchantedItemInUse: the enchanted stack,
// its equipment slot, its owner, and a write-back for a stack mutation (ChangeItemDamage). A nil
// write means the stack has no mutable home here (never the case for the wired paths).
type enchItemInUse struct {
	stack component.SlotData
	slot  int
	owner enchEntityRef
	write func(component.SlotData)
}

// enchEntityEffect is EnchantmentEntityEffect.apply(ServerLevel, int level, EnchantedItemInUse,
// Entity affected, Vec3 origin) — origin is always affected.position() at the wired call sites, so
// it is not carried.
type enchEntityEffect interface {
	apply(t *TickLoop, level int, inUse *enchItemInUse, affected enchEntityRef)
}

// eeIgnite is Ignite: `entity.igniteForSeconds(duration.calculate(level));` — Fire Aspect's
// payload. A MOB victim burns via the ported Entity fire model (fire.go); a PLAYER victim is a
// CITED DEFERRAL: v1 players carry no remainingFireTicks field (the same deferral lightning.go
// records for a player bolt hit) — the fire lands when the player fire model does.
type eeIgnite struct{ duration enchLevelValue }

func (e eeIgnite) apply(t *TickLoop, level int, _ *enchItemInUse, affected enchEntityRef) {
	if affected.mob != nil {
		t.igniteForSeconds(affected.mob, e.duration.calculate(level))
	}
	// affected.player: cited player-fire deferral (no player remainingFireTicks in v1).
}

// eeDamageEntity is DamageEntity (Thorns' reflect):
//
//	float f = Mth.randomBetween(entity.getRandom(), minDamage.calculate(level), maxDamage.calculate(level));
//	entity.hurtServer(level, new DamageSource(damageType, item.owner()), f);
//
// randomBetween = min + nextFloat()*(max-min) [VERIFIED javap Mth.randomBetween]. The source
// carries the ENCHANTED item's owner as the causing entity (the thorns wearer).
type eeDamageEntity struct {
	damageType string // "minecraft:thorns"
	minDamage  enchLevelValue
	maxDamage  enchLevelValue
}

func (e eeDamageEntity) apply(t *TickLoop, level int, inUse *enchItemInUse, affected enchEntityRef) {
	rng := affected.getRandom()
	if rng == nil {
		return
	}
	lo := e.minDamage.calculate(level)
	hi := e.maxDamage.calculate(level)
	f := lo + rng.nextFloat()*(hi-lo) // Mth.randomBetween(entity.getRandom(), lo, hi)
	src := damageSourceByTypeName(e.damageType, inUse.owner.entityID())
	if affected.player != nil {
		t.applyDamage(affected.player, src, f)
	} else if affected.mob != nil {
		t.applyDamageEntity(affected.mob, src, f)
	}
}

// eeChangeItemDamage is ChangeItemDamage (Thorns' armor wear):
//
//	ItemStack it = item.itemStack();
//	if (it.has(MAX_DAMAGE) && it.has(DAMAGE)) {
//	    ServerPlayer owner = item.owner() instanceof ServerPlayer sp ? sp : null;
//	    it.hurtAndBreak((int) amount.calculate(level), level, owner, item.onBreak());
//	}
//
// hurtAndBreak routes through the ported stackHurtAndBreak (durability.go: processDurabilityChange
// -> the Unbreaking roll -> applyDamage -> break shrink); creative is false here (the wearer took
// a real hit — vanilla's hurtAndBreak creative gate reads the owner, and a creative owner never
// reaches this path because a creative victim absorbs the triggering hit in applyDamage).
type eeChangeItemDamage struct{ amount enchLevelValue }

func (e eeChangeItemDamage) apply(t *TickLoop, level int, inUse *enchItemInUse, _ enchEntityRef) {
	s := inUse.stack
	if !stackHasComponent(s, compMaxDamage) || !stackHasComponent(s, compDamage) {
		return
	}
	amount := int(e.amount.calculate(level)) // the f2i truncation
	next, _ := t.stackHurtAndBreak(s, amount, false)
	if inUse.write != nil {
		inUse.write(next)
	}
}

// eeApplyMobEffect is ApplyMobEffect (Bane-of-Arthropods' post-attack SLOWNESS):
//
//	if (entity instanceof LivingEntity le) {
//	    RandomSource rng = le.getRandom();
//	    Optional<Holder<MobEffect>> pick = toApply.getRandomElement(rng);   // draws nextInt(size)
//	    if (pick.isPresent()) {
//	        int dur = Math.round(Mth.randomBetween(rng, minDuration.calc(lvl), maxDuration.calc(lvl)) * 20.0F);
//	        int amp = Math.max(0, Math.round(Mth.randomBetween(rng, minAmplifier.calc(lvl), maxAmplifier.calc(lvl))));
//	        le.addEffect(new MobEffectInstance(pick.get(), dur, amp));
//	    }
//	}
//
// RNG DRAW ORDER (jar-exact, off entity.getRandom()): (1) getRandomElement -> Util.getRandomSafe ->
// list.get(rng.nextInt(size)) draws one nextInt EVEN for a single-element to_apply (nextInt(1) still
// advances the source); (2) Mth.randomBetween for the duration draws one nextFloat; (3) Mth.randomBetween
// for the amplifier draws one nextFloat. Mth.randomBetween(rng, min, max) == rng.nextFloat()*(max-min)+min
// [VERIFIED javap]. Math.round(float) == Mth.floor(f + 0.5F) — the java.lang.Math.round port (mathRoundF).
type eeApplyMobEffect struct {
	toApply      []string // the HolderSet contents (effect resource ids), in list order
	minDuration  enchLevelValue
	maxDuration  enchLevelValue
	minAmplifier enchLevelValue
	maxAmplifier enchLevelValue
}

func (e eeApplyMobEffect) apply(t *TickLoop, level int, _ *enchItemInUse, affected enchEntityRef) {
	rng := affected.getRandom()
	if rng == nil {
		return
	}
	// toApply.getRandomElement(rng): Util.getRandomSafe returns empty for an empty list (no draw);
	// otherwise list.get(nextInt(size)) — the nextInt draw happens for any non-empty set.
	if len(e.toApply) == 0 {
		return
	}
	idx := rng.nextInt(len(e.toApply))
	if idx < 0 || idx >= len(e.toApply) {
		return
	}
	effectID := e.toApply[idx]
	// dur = Math.round(Mth.randomBetween(rng, minDur, maxDur) * 20.0F).
	minD, maxD := e.minDuration.calculate(level), e.maxDuration.calculate(level)
	dur := int(mathRoundF(mthRandomBetween(rng, minD, maxD) * 20.0))
	// amp = Math.max(0, Math.round(Mth.randomBetween(rng, minAmp, maxAmp))).
	minA, maxA := e.minAmplifier.calculate(level), e.maxAmplifier.calculate(level)
	amp := int(mathRoundF(mthRandomBetween(rng, minA, maxA)))
	if amp < 0 {
		amp = 0
	}
	// le.addEffect(new MobEffectInstance(pick, dur, amp)): a player victim routes through the player
	// effect map (scale 1.0, no owner — MobEffectInstance carries no source entity), a mob victim
	// through the entity effect map. Both are the ported LivingEntity.addEffect merge.
	if affected.player != nil {
		t.addPlayerEffect(affected.player, 0, effectID, dur, amp, 1.0)
	} else if affected.mob != nil {
		t.addEntityEffect(affected.mob, effectID, dur, amp)
	}
}

// mthRandomBetween ports net.minecraft.util.Mth.randomBetween(RandomSource, float, float):
// `return rng.nextFloat() * (max - min) + min;` — the nextFloat draw happens FIRST (jar op order),
// then the (max-min) scale and +min. [VERIFIED javap Mth.randomBetween.]
func mthRandomBetween(rng enchRNG, min, max float32) float32 {
	return rng.nextFloat()*(max-min) + min
}

// eeAllOfEntity is AllOf$EntityEffects: apply each inner effect in list order with the same args.
type eeAllOfEntity struct{ effects []enchEntityEffect }

func (e eeAllOfEntity) apply(t *TickLoop, level int, inUse *enchItemInUse, affected enchEntityRef) {
	for _, inner := range e.effects {
		inner.apply(t, level, inUse, affected)
	}
}

// parseEntityEffect decodes an EnchantmentEntityEffect by "type". Unsupported kinds (apply_mob_effect,
// strike_lightning, explode/wind burst, play_sound, spawn_particles, replace_disk/frost walker, ...)
// yield nil — the enclosing effect entry is skipped, a cited deferral per kind (none are among the
// E-3 target set).
func parseEntityEffect(raw json.RawMessage) enchEntityEffect {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	switch head.Type {
	case "minecraft:ignite":
		var body struct {
			Duration json.RawMessage `json:"duration"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		if v := parseLevelValue(body.Duration); v != nil {
			return eeIgnite{duration: v}
		}
	case "minecraft:damage_entity":
		var body struct {
			DamageType string          `json:"damage_type"`
			MinDamage  json.RawMessage `json:"min_damage"`
			MaxDamage  json.RawMessage `json:"max_damage"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		lo, hi := parseLevelValue(body.MinDamage), parseLevelValue(body.MaxDamage)
		if lo != nil && hi != nil {
			return eeDamageEntity{damageType: body.DamageType, minDamage: lo, maxDamage: hi}
		}
	case "minecraft:change_item_damage":
		var body struct {
			Amount json.RawMessage `json:"amount"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		if v := parseLevelValue(body.Amount); v != nil {
			return eeChangeItemDamage{amount: v}
		}
	case "minecraft:apply_mob_effect":
		var body struct {
			ToApply      json.RawMessage `json:"to_apply"`
			MinDuration  json.RawMessage `json:"min_duration"`
			MaxDuration  json.RawMessage `json:"max_duration"`
			MinAmplifier json.RawMessage `json:"min_amplifier"`
			MaxAmplifier json.RawMessage `json:"max_amplifier"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		apply := parseHolderSetIDs(body.ToApply)
		if len(apply) == 0 {
			return nil // an empty/#tag HolderSet -> nothing to apply (cited: no shipped enchant uses a #tag here)
		}
		minD := parseLevelValue(body.MinDuration)
		maxD := parseLevelValue(body.MaxDuration)
		minA := parseLevelValue(body.MinAmplifier)
		maxA := parseLevelValue(body.MaxAmplifier)
		if minD == nil || maxD == nil || minA == nil || maxA == nil {
			return nil
		}
		return eeApplyMobEffect{toApply: apply, minDuration: minD, maxDuration: maxD, minAmplifier: minA, maxAmplifier: maxA}
	case "minecraft:all_of":
		var body struct {
			Effects []json.RawMessage `json:"effects"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return nil
		}
		out := eeAllOfEntity{}
		for _, r := range body.Effects {
			inner := parseEntityEffect(r)
			if inner == nil {
				return nil
			}
			out.effects = append(out.effects, inner)
		}
		return out
	}
	return nil
}

// parseHolderSetIDs decodes a HolderSet<T> JSON form used by apply_mob_effect's `to_apply`: a bare
// id string (a single-element homogeneous list), or an array of id strings. A "#tag" reference is a
// cited deferral (no shipped enchantment's apply_mob_effect uses one — bane_of_arthropods names
// minecraft:slowness directly) and yields an empty list so the effect stays dormant rather than
// half-applying. The list ORDER is preserved (getRandomElement indexes into it).
func parseHolderSetIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if len(single) > 0 && single[0] == '#' {
			return nil // #tag HolderSet: cited deferral
		}
		return []string{single}
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		var out []string
		for _, id := range list {
			if len(id) > 0 && id[0] == '#' {
				continue // skip nested tag entries (cited deferral)
			}
			out = append(out, id)
		}
		return out
	}
	return nil
}

// ============================================================================================
// EnchantmentAttributeEffect — the minecraft:attributes location-based effects (equip modifiers)
// ============================================================================================

// enchAttrEffect is net.minecraft.world.item.enchantment.effects.EnchantmentAttributeEffect: a
// LEVEL-scaled AttributeModifier the enchant grants while the item is equipped and matchingSlot(slot)
// accepts the slot (Swift Sneak's sneaking_speed, Efficiency's mining_efficiency, ...). It is applied
// via EnchantmentHelper.forEachModifier (the ItemStack.forEachModifier enchant tail), keyed alongside
// the item's own ATTRIBUTE_MODIFIERS so equip adds and unequip removes cleanly by modifier id.
//
//	[VERIFIED javap EnchantmentAttributeEffect(id, attribute, amount, operation);
//	 getModifier(int level, StringRepresentable slot) = new AttributeModifier(idForSlot(slot),
//	   (double) amount.calculate(level), operation);
//	 idForSlot(slot) = id.withSuffix("/" + slot.getSerializedName()).]
type enchAttrEffect struct {
	id        string         // the base modifier id ("minecraft:enchantment.swift_sneak")
	attribute string         // the target attribute resource id ("minecraft:sneaking_speed")
	amount    enchLevelValue // LevelBasedValue
	operation attribute.Operation
}

// getModifier is EnchantmentAttributeEffect.getModifier(int level, StringRepresentable slot): the
// per-slot AttributeModifier. The id is idForSlot(slot) = base id + "/" + slot.getSerializedName();
// the amount is amount.calculate(level) widened f2d, exactly the bytecode.
func (e enchAttrEffect) getModifier(level, slot int) attribute.AttributeModifier {
	return attribute.AttributeModifier{
		ID:        e.id + "/" + equipmentSlotSerializedName(slot),
		Amount:    float64(e.amount.calculate(level)), // f2d after the float LevelBasedValue.calculate
		Operation: e.operation,
	}
}

// equipmentSlotSerializedName is EquipmentSlot.getSerializedName() (the enum ctor's lowercase name),
// indexed by the eqSlot* ordinal — the suffix EnchantmentAttributeEffect.idForSlot appends.
//
//	[VERIFIED javap EquipmentSlot.<clinit>: mainhand/offhand/feet/legs/chest/head/body/saddle.]
func equipmentSlotSerializedName(slot int) string {
	switch slot {
	case eqSlotMainHand:
		return "mainhand"
	case eqSlotOffHand:
		return "offhand"
	case eqSlotFeet:
		return "feet"
	case eqSlotLegs:
		return "legs"
	case eqSlotChest:
		return "chest"
	case eqSlotHead:
		return "head"
	case eqSlotBody:
		return "body"
	case eqSlotSaddle:
		return "saddle"
	}
	return ""
}

// enchForEachAttributeModifier is EnchantmentHelper.forEachModifier(ItemStack, EquipmentSlot,
// BiConsumer) — the enchant tail of ItemStack.forEachModifier. For each enchantment on the stack it
// visits the ATTRIBUTES effect list and, gated on Enchantment.matchingSlot(slot) (lambda$forEachModifier$3),
// emits consumer.accept(effect.attribute(), effect.getModifier(level, slot)). The attribute is passed as
// its resource id string; the caller maps it to an AttributeInstance (the vanilla getInstance null-guard)
// and applies remove-by-id-then-add exactly as the item-modifier path does.
//
//	[VERIFIED javap EnchantmentHelper.forEachModifier(EquipmentSlot overload) -> runIterationOnItem ->
//	 lambda$forEachModifier$2: getEffects(ATTRIBUTES).forEach -> lambda$forEachModifier$3:
//	 matchingSlot(slot) ? consumer.accept(effect.attribute(), effect.getModifier(level, slot)).]
func enchForEachAttributeModifier(s component.SlotData, slot int, fn func(attributeID string, m attribute.AttributeModifier)) {
	forEachItemEnchant(s, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.attributes) == 0 || !enchMatchingSlot(set, slot) {
			return
		}
		for _, eff := range set.attributes {
			fn(eff.attribute, eff.getModifier(level, slot))
		}
	})
}

// parseAttributeEffects decodes the minecraft:attributes effect list (a plain list of
// EnchantmentAttributeEffect records — NOT ConditionalEffect-wrapped). An unknown operation or an
// unparseable amount skips that entry (the record would fail its codec).
func parseAttributeEffects(raw json.RawMessage) []enchAttrEffect {
	if len(raw) == 0 {
		return nil
	}
	var entries []struct {
		ID        string          `json:"id"`
		Attribute string          `json:"attribute"`
		Amount    json.RawMessage `json:"amount"`
		Operation string          `json:"operation"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var out []enchAttrEffect
	for _, e := range entries {
		amt := parseLevelValue(e.Amount)
		if amt == nil {
			continue
		}
		op, ok := attributeOperationByName(e.Operation)
		if !ok {
			continue
		}
		out = append(out, enchAttrEffect{id: e.ID, attribute: e.Attribute, amount: amt, operation: op})
	}
	return out
}

// attributeOperationByName maps the AttributeModifier$Operation serialized name to the ported enum.
//
//	[VERIFIED javap AttributeModifier$Operation.<clinit>: add_value(0), add_multiplied_base(1),
//	 add_multiplied_total(2).]
func attributeOperationByName(name string) (attribute.Operation, bool) {
	switch name {
	case "add_value":
		return attribute.AddValue, true
	case "add_multiplied_base":
		return attribute.AddMultipliedBase, true
	case "add_multiplied_total":
		return attribute.AddMultipliedTotal, true
	}
	return 0, false
}

// ============================================================================================
// The parsed per-enchantment effect table (Enchantment.effects() + definition.slots())
// ============================================================================================

// enchCondValue is ConditionalEffect<EnchantmentValueEffect>: an effect + optional requirements.
type enchCondValue struct {
	effect enchValueEffect
	req    enchCondition // nil = no requirements (always matches)
}

// enchPostAttack is TargetedConditionalEffect<EnchantmentEntityEffect> (the POST_ATTACK entries):
// enchanted names whose item carries the enchant (attacker/victim), affected names who the effect
// hits (attacker/victim/damaging_entity).
type enchPostAttack struct {
	enchanted string
	affected  string
	effect    enchEntityEffect
	req       enchCondition
}

// enchEffectSet is one enchantment's parsed runtime data.
type enchEffectSet struct {
	slots              []string // definition.slots() group keys (Enchantment.matchingSlot)
	damage             []enchCondValue
	damageProtection   []enchCondValue
	knockback          []enchCondValue
	armorEffectiveness []enchCondValue // consumed by enchModifyArmorEffectiveness (breach — E-3)
	repairWithXP       []enchCondValue
	postAttack         []enchPostAttack
	attributes         []enchAttrEffect // minecraft:attributes location-based modifiers (equip/unequip)
	projectileSpawned  []enchCondEntity // minecraft:projectile_spawned (Flame -> ignite the arrow at shoot)
	fishingLuck        []enchCondValue  // minecraft:fishing_luck_bonus (Luck of the Sea)
	fishingTime        []enchCondValue  // minecraft:fishing_time_reduction (Lure)
	crossbowCharge     enchValueEffect  // minecraft:crossbow_charge_time (Quick Charge) — a bare value effect

	// projectileCount is minecraft:projectile_count (Multishot: add linear 2.0 + 2.0/level). Folded over
	// the MutableFloat(1) ammo count in EnchantmentHelper.processProjectileCount -> ProjectileWeaponItem.draw
	// (a bow/crossbow drawing 1 stack becomes N copies), then Math.max(0, (int) value).
	projectileCount []enchCondValue
	// projectileSpread is minecraft:projectile_spread (Multishot: add linear 10.0 + 10.0/level degrees).
	// Folded over 0.0 in EnchantmentHelper.processProjectileSpread -> the ProjectileWeaponItem.shoot angle
	// fan-out (center + the -/+ half-spread arrows), then Math.max(0.0F, value).
	projectileSpread []enchCondValue
	// projectilePiercing is minecraft:projectile_piercing (Piercing: add linear 1.0 + 1.0/level). Folded
	// over 0.0 in EnchantmentHelper.getPiercingCount (the crossbow weapon read at arrow spawn), then
	// Math.max(0, (int) value) -> AbstractArrow.setPierceLevel.
	projectilePiercing []enchCondValue

	// preventEquipmentDrop is TRUE when the enchantment carries the minecraft:prevent_equipment_drop marker
	// component (Vanishing Curse). EnchantmentHelper.has(stack, PREVENT_EQUIPMENT_DROP) is TRUE for a stack
	// bearing any such enchantment; Player.destroyVanishingCursedItems deletes those stacks on death instead
	// of dropping them. The component is a bare marker ("minecraft:prevent_equipment_drop": {}), so presence
	// is all that matters. Cite EnchantmentEffectComponents.PREVENT_EQUIPMENT_DROP.
	preventEquipmentDrop bool
	// preventArmorChange is TRUE when the enchantment carries the minecraft:prevent_armor_change marker
	// component (Binding Curse). ArmorSlot.mayPickup returns false when EnchantmentHelper.has(stack,
	// PREVENT_ARMOR_CHANGE) and the player is not creative -> the armor cannot be removed once equipped.
	// A bare marker ("minecraft:prevent_armor_change": {}). Cite EnchantmentEffectComponents.PREVENT_ARMOR_CHANGE.
	preventArmorChange bool

	// smashDamagePerFallenBlock is minecraft:smash_damage_per_fallen_block (Density: add linear 0.5 +
	// 0.5/level). CITED BLOCKED: it is folded in MaceItem.getAttackDamageBonus over the mace smash-attack
	// fall distance, but the mace smash-attack subsystem (MaceItem.getAttackDamageBonus / postAttack) is not
	// yet ported (v1 getAttackDamageBonus == 0), so this parses but never fires. Cite MaceItem.getAttackDamageBonus.
	smashDamagePerFallenBlock []enchCondValue
}

// enchCondEntity is ConditionalEffect<EnchantmentEntityEffect>: an entity effect + optional requirements.
// The PROJECTILE_SPAWNED list (Flame) uses it; the effect targets the spawned projectile (the arrow).
type enchCondEntity struct {
	effect enchEntityEffect
	req    enchCondition // nil = no requirements (Flame carries none)
}

var (
	enchEffectTableOnce sync.Once
	enchEffectTableVal  []*enchEffectSet
)

// enchantEffectTable lazily parses every enchantment's effect JSON into the runtime model, indexed
// by WIRE id (the sorted-registry order — the id EnchantmentEntry carries). A load error yields an
// empty table (no enchantment effects — degraded, never a panic), matching enchantCosts' policy.
func enchantEffectTable() []*enchEffectSet {
	enchEffectTableOnce.Do(func() {
		defs, err := registrydata.EnchantmentEffectDefs()
		if err != nil {
			return
		}
		table := make([]*enchEffectSet, len(defs))
		for i, def := range defs {
			set := &enchEffectSet{slots: def.Slots}
			set.damage = parseCondValueList(def.Effects["minecraft:damage"])
			set.damageProtection = parseCondValueList(def.Effects["minecraft:damage_protection"])
			set.knockback = parseCondValueList(def.Effects["minecraft:knockback"])
			set.armorEffectiveness = parseCondValueList(def.Effects["minecraft:armor_effectiveness"])
			set.repairWithXP = parseCondValueList(def.Effects["minecraft:repair_with_xp"])
			set.postAttack = parsePostAttackList(def.Effects["minecraft:post_attack"])
			set.attributes = parseAttributeEffects(def.Effects["minecraft:attributes"])
			set.projectileSpawned = parseCondEntityList(def.Effects["minecraft:projectile_spawned"])
			set.fishingLuck = parseCondValueList(def.Effects["minecraft:fishing_luck_bonus"])
			set.fishingTime = parseCondValueList(def.Effects["minecraft:fishing_time_reduction"])
			set.crossbowCharge = parseValueEffect(def.Effects["minecraft:crossbow_charge_time"])
			set.projectileCount = parseCondValueList(def.Effects["minecraft:projectile_count"])
			set.projectileSpread = parseCondValueList(def.Effects["minecraft:projectile_spread"])
			set.projectilePiercing = parseCondValueList(def.Effects["minecraft:projectile_piercing"])
			set.smashDamagePerFallenBlock = parseCondValueList(def.Effects["minecraft:smash_damage_per_fallen_block"])
			// The prevent_* markers are bare presence flags (a "{}" component, not a ConditionalEffect
			// list): EnchantmentHelper.has checks only that the component is present in the enchant's effects.
			_, set.preventEquipmentDrop = def.Effects["minecraft:prevent_equipment_drop"]
			_, set.preventArmorChange = def.Effects["minecraft:prevent_armor_change"]
			table[i] = set
		}
		enchEffectTableVal = table
	})
	return enchEffectTableVal
}

// parseCondValueList decodes a ConditionalEffect<EnchantmentValueEffect> list.
func parseCondValueList(raw json.RawMessage) []enchCondValue {
	if len(raw) == 0 {
		return nil
	}
	var entries []struct {
		Effect       json.RawMessage `json:"effect"`
		Requirements json.RawMessage `json:"requirements"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var out []enchCondValue
	for _, e := range entries {
		eff := parseValueEffect(e.Effect)
		if eff == nil {
			continue // unsupported effect kind: skipped (cited in parseValueEffect)
		}
		var req enchCondition
		if len(e.Requirements) > 0 {
			req = parseCondition(e.Requirements)
		}
		out = append(out, enchCondValue{effect: eff, req: req})
	}
	return out
}

// parsePostAttackList decodes the TargetedConditionalEffect<EnchantmentEntityEffect> list.
func parsePostAttackList(raw json.RawMessage) []enchPostAttack {
	if len(raw) == 0 {
		return nil
	}
	var entries []struct {
		Enchanted    string          `json:"enchanted"`
		Affected     string          `json:"affected"`
		Effect       json.RawMessage `json:"effect"`
		Requirements json.RawMessage `json:"requirements"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var out []enchPostAttack
	for _, e := range entries {
		eff := parseEntityEffect(e.Effect)
		if eff == nil {
			continue // unsupported entity-effect kind: cited deferral (parseEntityEffect)
		}
		var req enchCondition
		if len(e.Requirements) > 0 {
			req = parseCondition(e.Requirements)
		}
		out = append(out, enchPostAttack{enchanted: e.Enchanted, affected: e.Affected, effect: eff, req: req})
	}
	return out
}

// parseCondEntityList decodes a ConditionalEffect<EnchantmentEntityEffect> list (the PROJECTILE_SPAWNED
// form: each entry is {effect, requirements?}). An unsupported entity-effect kind is skipped (cited).
func parseCondEntityList(raw json.RawMessage) []enchCondEntity {
	if len(raw) == 0 {
		return nil
	}
	var entries []struct {
		Effect       json.RawMessage `json:"effect"`
		Requirements json.RawMessage `json:"requirements"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var out []enchCondEntity
	for _, e := range entries {
		eff := parseEntityEffect(e.Effect)
		if eff == nil {
			continue
		}
		var req enchCondition
		if len(e.Requirements) > 0 {
			req = parseCondition(e.Requirements)
		}
		out = append(out, enchCondEntity{effect: eff, req: req})
	}
	return out
}

// enchOnProjectileSpawned is EnchantmentHelper.onProjectileSpawned(ServerLevel, ItemStack weapon,
// Projectile, Consumer<Item>): for each enchantment on the firing weapon, run its PROJECTILE_SPAWNED
// effects against the just-spawned projectile (Flame's Ignite -> projectile.igniteForSeconds(100)). The
// projectile is the affected entity; the loot context carries no damage source (entityContext), so any
// damage_source_properties condition is absent — Flame carries no requirements. Cite EnchantmentHelper
// .onProjectileSpawned -> Enchantment.onProjectileSpawned -> applyEffects(getEffects(PROJECTILE_SPAWNED)).
func (t *TickLoop) enchOnProjectileSpawned(weapon component.SlotData, projectile *Entity) {
	if stackEmpty(weapon) || projectile == nil {
		return
	}
	inUse := &enchItemInUse{stack: weapon, slot: eqSlotMainHand}
	affected := enchEntityRef{mob: projectile}
	ctx := &enchDamageCtx{thisType: affected.typeName(), t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.projectileSpawned) == 0 {
			return
		}
		ctx.level = level
		for _, e := range set.projectileSpawned {
			if e.req != nil && !e.req.matches(ctx) {
				continue
			}
			e.effect.apply(t, level, inUse, affected)
		}
	})
}

// enchEffectsFor resolves an enchantment wire id to its parsed effect set (nil for an unknown id).
func enchEffectsFor(wireID int) *enchEffectSet {
	table := enchantEffectTable()
	if wireID < 0 || wireID >= len(table) {
		return nil
	}
	return table[wireID]
}

// enchMatchingSlot is Enchantment.matchingSlot(EquipmentSlot):
// `definition.slots().stream().anyMatch(g -> g.test(slot));` — reuses the ported
// EquipmentSlotGroup.test (equipment_attributes.go).
func enchMatchingSlot(set *enchEffectSet, slot int) bool {
	for _, g := range set.slots {
		if equipmentSlotGroupTest(g, slot) {
			return true
		}
	}
	return false
}

// ============================================================================================
// EnchantmentHelper iteration + the modify*/getDamageProtection/doPostAttackEffects ports
// ============================================================================================

// forEachItemEnchant is runIterationOnItem(ItemStack, EnchantmentVisitor): visit each
// (enchantment, level) of the stack's ENCHANTMENTS component (getOrDefault(ENCHANTMENTS, EMPTY) —
// an absent component visits nothing). NOTE: NO matchingSlot filter on this overload (only the
// in-slot overload gates on it) — jar-verified.
func forEachItemEnchant(s component.SlotData, fn func(wireID, level int)) {
	if stackEmpty(s) {
		return
	}
	comp := stackComponent(s, compEnchantments)
	if comp == nil {
		return
	}
	e, ok := comp.(*component.Enchantments)
	if !ok {
		return
	}
	for _, en := range e.Enchantments {
		fn(int(en.ID), int(en.Level))
	}
}

// forEachItemEnchantInSlot is runIterationOnItem(ItemStack, EquipmentSlot, LivingEntity,
// EnchantmentInSlotVisitor): like forEachItemEnchant but each enchantment is gated on
// Enchantment.matchingSlot(slot) — jar-verified (offset 103 of the 4-arg overload).
func forEachItemEnchantInSlot(s component.SlotData, slot int, fn func(wireID, level int)) {
	forEachItemEnchant(s, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || !enchMatchingSlot(set, slot) {
			return
		}
		fn(wireID, level)
	})
}

// applyEnchCondValues is Enchantment.applyEffects(List<ConditionalEffect<T>>, LootContext,
// MutableFloat, FloatAction): fold `value` through each entry whose requirements match, using the
// given RandomSource for the effect's process (the entity random for the damage/knockback/
// protection FloatActions; the level random for the item-filtered mending count — jar-verified
// per lambda).
func applyEnchCondValues(entries []enchCondValue, ctx *enchDamageCtx, rng enchRNG, value float32) float32 {
	for _, e := range entries {
		if e.req != nil && !e.req.matches(ctx) {
			continue
		}
		value = e.effect.process(ctx.level, rng, value)
	}
	return value
}

// enchModifyDamage is EnchantmentHelper.modifyDamage(ServerLevel, ItemStack weapon, Entity victim,
// DamageSource, float damage): fold the weapon's DAMAGE effects (Sharpness/Smite/Bane/Impaling)
// over the damage value. victim supplies THIS_ENTITY (the smite/bane entity-type condition) and
// the FloatAction RandomSource (entity.getRandom() — no shipped DAMAGE effect draws, kept for the
// 1:1 shape).
//
//	[VERIFIED javap EnchantmentHelper.modifyDamage -> runIterationOnItem(weapon, visitor) ->
//	 Enchantment.modifyDamage -> modifyDamageFilteredValue(DAMAGE, ..., damageContext) ->
//	 applyEffects(getEffects(DAMAGE), ctx, mutable, (eff,v) -> eff.process(lvl, victim.getRandom(), v)).]
func (t *TickLoop) enchModifyDamage(weapon component.SlotData, victim enchEntityRef, src damageSource, damage float32) float32 {
	return t.enchModifyDamageDirect(weapon, victim, src, t.enchDirectAttackerType(src), damage)
}

// enchModifyDamageDirect is enchModifyDamage with an explicit DIRECT_ATTACKER entity type — the
// AbstractArrow.onHitEntity path passes the ARROW's type here (source.getDirectEntity() == the arrow),
// which is what Power's `direct_attacker is #minecraft:arrows` requirement reads. The melee path resolves
// it from the source (direct == causing entity) via enchDirectAttackerType.
func (t *TickLoop) enchModifyDamageDirect(weapon component.SlotData, victim enchEntityRef, src damageSource, directType string, damage float32) float32 {
	ctx := &enchDamageCtx{thisType: victim.typeName(), directType: directType, src: src, t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.damage) == 0 {
			return
		}
		ctx.level = level
		damage = applyEnchCondValues(set.damage, ctx, victim.getRandom(), damage)
	})
	return damage
}

// enchFishingLuckBonus is EnchantmentHelper.getFishingLuckBonus(ServerLevel, ItemStack rod, Entity
// owner): fold the rod's FISHING_LUCK_BONUS effects (Luck of the Sea: add linear 1.0 + 1.0/level) over
// 0.0 via runIterationOnItem (no slot/requirement gate), then Math.max(0, (int)floatValue) — the
// MutableFloat.intValue f2i truncation. Cite EnchantmentHelper.getFishingLuckBonus bytecode.
func (t *TickLoop) enchFishingLuckBonus(rod component.SlotData) int {
	f := float32(0.0)
	ctx := &enchDamageCtx{t: t}
	forEachItemEnchant(rod, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.fishingLuck) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.fishingLuck, ctx, nil, f)
	})
	v := int(f) // MutableFloat.intValue() f2i truncation
	if v < 0 {
		return 0
	}
	return v
}

// enchFishingTimeReduction is EnchantmentHelper.getFishingTimeReduction(ServerLevel, ItemStack rod,
// Entity owner): fold the rod's FISHING_TIME_REDUCTION effects (Lure: add linear 5.0 + 5.0/level) over
// 0.0 via runIterationOnItem, then Math.max(0.0F, floatValue). The FishingRodItem.use caller multiplies
// by 20.0F and f2i-truncates to the lureSpeed tick reduction. Cite getFishingTimeReduction bytecode.
func (t *TickLoop) enchFishingTimeReduction(rod component.SlotData) float32 {
	f := float32(0.0)
	ctx := &enchDamageCtx{t: t}
	forEachItemEnchant(rod, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.fishingTime) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.fishingTime, ctx, nil, f)
	})
	if f < 0.0 {
		return 0.0
	}
	return f
}

// enchModifyCrossbowChargingTime is EnchantmentHelper.modifyCrossbowChargingTime(ItemStack crossbow,
// LivingEntity owner, float base): fold the crossbow's CROSSBOW_CHARGE_TIME effect (Quick Charge: add
// -0.25 - 0.25/level) over base (1.25s at the caller), via runIterationOnItem (no slot/requirement gate),
// then Math.max(0.0F, floatValue). Enchantment.modifyCrossbowChargeTime applies the single value effect
// directly. Cite EnchantmentHelper.modifyCrossbowChargingTime bytecode.
func (t *TickLoop) enchModifyCrossbowChargingTime(crossbow component.SlotData, base float32) float32 {
	f := base
	forEachItemEnchant(crossbow, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || set.crossbowCharge == nil {
			return
		}
		// Enchantment.modifyCrossbowChargeTime: mutable = effect.process(level, owner.getRandom(), mutable).
		// Quick Charge's add draws no RNG (nil source is observably identical, kept RNG-lazy).
		f = set.crossbowCharge.process(level, nil, f)
	})
	if f < 0.0 {
		return 0.0
	}
	return f
}

// enchCrossbowChargeDuration is CrossbowItem.getChargeDuration(stack, entity): Mth.floor(
// modifyCrossbowChargingTime(stack, entity, 1.25F) * 20.0F). No Quick Charge -> floor(1.25*20) = 25.
func (t *TickLoop) enchCrossbowChargeDuration(crossbow component.SlotData) int32 {
	return int32(math.Floor(float64(t.enchModifyCrossbowChargingTime(crossbow, 1.25) * 20.0)))
}

// enchProcessProjectileCount is EnchantmentHelper.processProjectileCount(ServerLevel, ItemStack weapon,
// Entity owner, int base): fold the weapon's PROJECTILE_COUNT effects (Multishot: add linear 2.0 +
// 2.0/level) over float(base) via runIterationOnItem (no slot/requirement gate that Multishot uses), then
// Math.max(0, (int) value). ProjectileWeaponItem.draw calls it with base 1 to decide how many ammo copies
// to draw (unenchanted -> 1; Multishot I -> 3). The FloatAction RandomSource is owner.getRandom() —
// Multishot's add draws nothing (nil is observably identical, kept RNG-lazy).
//
//	[VERIFIED javap EnchantmentHelper.processProjectileCount: MutableFloat(base); runIterationOnItem ->
//	 Enchantment.modifyProjectileCount -> applyEffects(getEffects(PROJECTILE_COUNT), ...); Math.max(0,
//	 mutable.intValue()).]
func (t *TickLoop) enchProcessProjectileCount(weapon component.SlotData, base int) int {
	f := float32(base)
	ctx := &enchDamageCtx{t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.projectileCount) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.projectileCount, ctx, nil, f)
	})
	v := int(f) // MutableFloat.intValue() f2i truncation
	if v < 0 {
		return 0
	}
	return v
}

// enchProcessProjectileSpread is EnchantmentHelper.processProjectileSpread(ServerLevel, ItemStack weapon,
// Entity owner, float base): fold the weapon's PROJECTILE_SPREAD effects (Multishot: add linear 10.0 +
// 10.0/level degrees) over base via runIterationOnItem, then Math.max(0.0F, value). ProjectileWeaponItem
// .shoot calls it with base 0.0 to get the total fan-out spread (Multishot I -> 10 degrees). The FloatAction
// RandomSource is owner.getRandom() — Multishot's add draws nothing (nil kept RNG-lazy).
//
//	[VERIFIED javap EnchantmentHelper.processProjectileSpread: MutableFloat(base); runIterationOnItem ->
//	 Enchantment.modifyProjectileSpread -> applyEffects(getEffects(PROJECTILE_SPREAD), ...); Math.max(0.0F,
//	 mutable.floatValue()).]
func (t *TickLoop) enchProcessProjectileSpread(weapon component.SlotData, base float32) float32 {
	f := base
	ctx := &enchDamageCtx{t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.projectileSpread) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.projectileSpread, ctx, nil, f)
	})
	if f < 0.0 {
		return 0.0
	}
	return f
}

// enchGetPiercingCount is EnchantmentHelper.getPiercingCount(ServerLevel, ItemStack weapon, ItemStack
// pickupStack): fold the WEAPON's PROJECTILE_PIERCING effects (Piercing: add linear 1.0 + 1.0/level) over
// 0.0 via runIterationOnItem (over the weapon — the crossbow — NOT the ammo), then Math.max(0, (int) value).
// The AbstractArrow ctor reads this once at spawn and calls setPierceLevel((byte) count) when > 0. The
// FloatAction RandomSource is unused by Piercing's add (nil kept RNG-lazy).
//
//	[VERIFIED javap EnchantmentHelper.getPiercingCount: MutableFloat(0.0F); runIterationOnItem(weapon, ...);
//	 Math.max(0, mutable.intValue()). AbstractArrow.<init>: getPiercingCount(level, firedFromWeapon,
//	 pickupItemStack); if > 0 setPierceLevel((byte) count).]
func (t *TickLoop) enchGetPiercingCount(weapon component.SlotData) int {
	f := float32(0.0)
	ctx := &enchDamageCtx{t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.projectilePiercing) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.projectilePiercing, ctx, nil, f)
	})
	v := int(f) // MutableFloat.intValue() f2i truncation
	if v < 0 {
		return 0
	}
	return v
}

// enchHasPreventEquipmentDrop is EnchantmentHelper.has(stack, PREVENT_EQUIPMENT_DROP): TRUE when any
// enchantment on the stack carries the minecraft:prevent_equipment_drop marker (Vanishing Curse). Drives
// Player.destroyVanishingCursedItems (the item is deleted on death instead of dropping). No RNG.
//
//	[VERIFIED javap EnchantmentHelper.has: runIterationOnItem sets a MutableBoolean true iff an enchant's
//	 effects() has(component). Player.destroyVanishingCursedItems: per inventory slot, if has(stack,
//	 PREVENT_EQUIPMENT_DROP) -> removeItemNoUpdate(i).]
func enchHasPreventEquipmentDrop(s component.SlotData) bool {
	has := false
	forEachItemEnchant(s, func(wireID, _ int) {
		if set := enchEffectsFor(wireID); set != nil && set.preventEquipmentDrop {
			has = true
		}
	})
	return has
}

// enchHasPreventArmorChange is EnchantmentHelper.has(stack, PREVENT_ARMOR_CHANGE): TRUE when any
// enchantment on the stack carries the minecraft:prevent_armor_change marker (Binding Curse). Drives
// ArmorSlot.mayPickup (a non-creative player cannot remove the equipped armor). No RNG.
//
//	[VERIFIED javap ArmorSlot.mayPickup: !isEmpty && !player.isCreative && EnchantmentHelper.has(stack,
//	 PREVENT_ARMOR_CHANGE) -> return false.]
func enchHasPreventArmorChange(s component.SlotData) bool {
	has := false
	forEachItemEnchant(s, func(wireID, _ int) {
		if set := enchEffectsFor(wireID); set != nil && set.preventArmorChange {
			has = true
		}
	})
	return has
}

// enchDirectAttackerType resolves DamageSource.getDirectEntity()'s entity-type id for the MELEE path,
// where the direct entity IS the causing entity (isDirect true). For an indirect source (a projectile)
// the thin damageSource value does not carry the projectile, so this returns "" and the caller that has
// the projectile (the arrow hit path) passes the type explicitly via enchModifyDamageDirect. No shipped
// melee DAMAGE/KNOCKBACK effect reads direct_attacker, so "" is observably identical there.
func (t *TickLoop) enchDirectAttackerType(src damageSource) string {
	if !src.isDirect() || src.attacker == 0 {
		return ""
	}
	return t.enchResolveEntity(src.attacker).typeName()
}

// enchModifyKnockback is EnchantmentHelper.modifyKnockback(ServerLevel, ItemStack weapon, Entity
// victim, DamageSource, float f): fold the weapon's KNOCKBACK effects (the Knockback enchant: add
// linear 1.0 + 1.0/level) over the raw knockback strength.
//
//	[VERIFIED javap EnchantmentHelper.modifyKnockback -> Enchantment.modifyKnockback ->
//	 modifyDamageFilteredValue(KNOCKBACK, ...).]
func (t *TickLoop) enchModifyKnockback(weapon component.SlotData, victim enchEntityRef, src damageSource, f float32) float32 {
	return t.enchModifyKnockbackDirect(weapon, victim, src, t.enchDirectAttackerType(src), f)
}

// enchModifyKnockbackDirect is enchModifyKnockback with an explicit DIRECT_ATTACKER type — the arrow
// doKnockback path passes the ARROW's type so Punch's `direct_attacker is #minecraft:arrows` gate fires.
func (t *TickLoop) enchModifyKnockbackDirect(weapon component.SlotData, victim enchEntityRef, src damageSource, directType string, f float32) float32 {
	ctx := &enchDamageCtx{thisType: victim.typeName(), directType: directType, src: src, t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.knockback) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.knockback, ctx, victim.getRandom(), f)
	})
	return f
}

// enchDamageProtection is EnchantmentHelper.getDamageProtection(ServerLevel, LivingEntity victim,
// DamageSource): sum the DAMAGE_PROTECTION effects (the EPF) across the victim's EQUIPMENT — a
// runIterationOnEquipment walk (every EquipmentSlot in VALUES order, each item's enchants gated on
// Enchantment.matchingSlot(slot)) folding a MutableFloat(0). The Protection family's
// damage_source_properties conditions (bypasses_invulnerability=false; is_fall/is_fire/... for the
// specialized ones) read the source. The victim is THIS_ENTITY.
//
//	[VERIFIED javap EnchantmentHelper.getDamageProtection -> runIterationOnEquipment(entity,
//	 (ench,lvl,inUse) -> ench.modifyDamageProtection(level, lvl, inUse.itemStack(), entity, src, f));
//	 Enchantment.modifyDamageProtection -> applyEffects(getEffects(DAMAGE_PROTECTION),
//	 damageContext, f, (eff,v) -> eff.process(lvl, entity.getRandom(), v)).]
func (t *TickLoop) enchDamageProtection(victim enchEntityRef, equip func(slot int) component.SlotData, src damageSource) float32 {
	ctx := &enchDamageCtx{thisType: victim.typeName(), src: src, t: t}
	protection := float32(0.0)
	for slot := 0; slot < equipmentSlotCount; slot++ { // EquipmentSlot.VALUES order
		s := equip(slot)
		if stackEmpty(s) {
			continue
		}
		forEachItemEnchantInSlot(s, slot, func(wireID, level int) {
			set := enchEffectsFor(wireID)
			if set == nil || len(set.damageProtection) == 0 {
				return
			}
			ctx.level = level
			protection = applyEnchCondValues(set.damageProtection, ctx, victim.getRandom(), protection)
		})
	}
	return protection
}

// enchModifyArmorEffectiveness is EnchantmentHelper.modifyArmorEffectiveness(ServerLevel, ItemStack
// weapon, Entity victim, DamageSource, float f): fold the WEAPON's ARMOR_EFFECTIVENESS effects
// (Breach: add linear -0.15 -0.15/level) over the raw armor-ratio f (armorClamp/25.0). victim
// supplies THIS_ENTITY and the FloatAction RandomSource (victim.getRandom() — no shipped
// armor_effectiveness effect draws, kept for the 1:1 shape). The CombatRules caller then clamps the
// result into [0,1]; a lower ratio means more damage penetrates.
//
//	[VERIFIED javap EnchantmentHelper.modifyArmorEffectiveness -> runIterationOnItem(weapon, visitor)
//	 -> Enchantment.modifyArmorEffectivness -> modifyDamageFilteredValue(ARMOR_EFFECTIVENESS, ...,
//	 damageContext) -> applyEffects(getEffects(ARMOR_EFFECTIVENESS), ctx, mutable,
//	 (eff,v) -> eff.process(lvl, victim.getRandom(), v)). CombatRules.getDamageAfterAbsorb wraps it in
//	 Mth.clamp(..., 0.0F, 1.0F).]
func (t *TickLoop) enchModifyArmorEffectiveness(weapon component.SlotData, victim enchEntityRef, src damageSource, f float32) float32 {
	ctx := &enchDamageCtx{thisType: victim.typeName(), src: src, t: t}
	forEachItemEnchant(weapon, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.armorEffectiveness) == 0 {
			return
		}
		ctx.level = level
		f = applyEnchCondValues(set.armorEffectiveness, ctx, victim.getRandom(), f)
	})
	return f
}

// enchArmorEffectivenessFn builds the CombatRules armor-effectiveness closure for a hit: it resolves
// the attacker's weapon (source.getWeaponItem() == the causing entity's mainhand) and, ONLY when that
// weapon carries an armor_effectiveness enchant (Breach), returns a closure folding
// enchModifyArmorEffectiveness. When no such enchant is present it returns nil — the no-enchant hit
// then takes the vanilla else-branch (f4 = f3) byte-identically, drawing no RNG and touching no
// enchant state (the pig-oracle discipline). victim supplies THIS_ENTITY for the (unused-by-Breach)
// damage-source conditions and the FloatAction random.
func (t *TickLoop) enchArmorEffectivenessFn(victim enchEntityRef, src damageSource) func(float32) float32 {
	if src.attacker == 0 {
		return nil // an environmental source has no weapon (source.getWeaponItem() == null)
	}
	attacker := t.enchResolveEntity(src.attacker)
	if !attacker.valid() {
		return nil
	}
	weapon, _ := t.enchWeaponInUse(attacker)
	if stackEmpty(weapon) {
		return nil // source.getWeaponItem() == null -> the `ifnull` else branch
	}
	// Zero-perturbation gate: only fold when the weapon actually carries an armor_effectiveness effect.
	// A weapon with unrelated enchants (or none) leaves the vanilla result unchanged (f3 in [0,0.8] is
	// already inside the [0,1] clamp), so returning nil is observably identical and avoids all work.
	has := false
	forEachItemEnchant(weapon, func(wireID, _ int) {
		if set := enchEffectsFor(wireID); set != nil && len(set.armorEffectiveness) > 0 {
			has = true
		}
	})
	if !has {
		return nil
	}
	return func(f float32) float32 {
		return t.enchModifyArmorEffectiveness(weapon, victim, src, f)
	}
}

// playerEquipRead adapts the player's 6 populated equipment slots to the 8-ordinal equipment walk
// (BODY/SADDLE read EMPTY on a player, exactly as the vanilla player's EnumMap has no entry).
func playerEquipRead(p *tickPlayer) func(slot int) component.SlotData {
	return func(slot int) component.SlotData { return playerItemBySlot(p, slot) }
}

// mobEquipRead adapts a mob's equipment array to the walk.
func mobEquipRead(e *Entity) func(slot int) component.SlotData {
	return func(slot int) component.SlotData { return e.getItemBySlot(slot) }
}

// doPostAttackEffects is EnchantmentHelper.doPostAttackEffects(ServerLevel, Entity victim,
// DamageSource): resolve the attacker (source.getEntity()) and its weapon, then delegate to the
// WithItemSource form. Wired at Mob.doHurtTarget (mob->player melee) and reused by the player
// attack paths (which pass the weapon explicitly, as Player.attack's itemAttackInteraction does).
//
//	[VERIFIED javap EnchantmentHelper.doPostAttackEffects: attacker = source.getEntity();
//	 attacker instanceof LivingEntity -> doPostAttackEffectsWithItemSource(level, victim, source,
//	 attacker.getWeaponItem()); else null weapon.]
func (t *TickLoop) doPostAttackEffects(victim enchEntityRef, src damageSource) {
	attacker := t.enchResolveEntity(src.attacker)
	var weapon component.SlotData
	var weaponInUse *enchItemInUse
	if attacker.valid() {
		weapon, weaponInUse = t.enchWeaponInUse(attacker)
	}
	t.doPostAttackEffectsWithItemSource(victim, src, weapon, weaponInUse, attacker)
}

// doPostAttackEffectsWithItemSource is EnchantmentHelper.doPostAttackEffectsWithItemSource(
// ServerLevel, Entity victim, DamageSource, ItemStack weapon) (-> ...OnBreak with a null onBreak):
//
//  1. victim instanceof LivingEntity: runIterationOnEquipment(victim, (ench,lvl,inUse) ->
//     ench.doPostAttack(level, lvl, inUse, VICTIM, victim, source))       — Thorns on the victim's gear
//  2. weapon != null && source.getEntity() instanceof LivingEntity attacker: runIterationOnItem(
//     weapon, MAINHAND, attacker, (ench,lvl,inUse) -> ench.doPostAttack(level, lvl, inUse,
//     ATTACKER, victim, source))                                          — Fire Aspect on the weapon
//
// Enchantment.doPostAttack visits each POST_ATTACK entry whose enchanted() matches the target,
// then (static doPostAttack) applies the entry when its condition matches the damage context,
// resolving the affected entity: ATTACKER -> source.getEntity(), DIRECT_ATTACKER ->
// source.getDirectEntity() (== the attacker for a direct melee hit), VICTIM -> the victim.
//
//	[VERIFIED javap doPostAttackEffectsWithItemSourceOnBreak + Enchantment.doPostAttack (both) —
//	 the enchanted()==target gate, the TargetedConditionalEffect.matches(damageContext) gate, the
//	 affected switch, effect.apply(level, lvl, inUse, affected, affected.position()).]
func (t *TickLoop) doPostAttackEffectsWithItemSource(victim enchEntityRef, src damageSource, weapon component.SlotData, weaponInUse *enchItemInUse, attacker enchEntityRef) {
	ctx := &enchDamageCtx{thisType: victim.typeName(), src: src, t: t}

	// (1) The VICTIM's equipment (Thorns): every slot, matchingSlot-gated, target VICTIM.
	if victim.valid() {
		t.enchForEachEquipmentInUse(victim, func(slot int, s component.SlotData, inUse *enchItemInUse) {
			forEachItemEnchantInSlot(s, slot, func(wireID, level int) {
				t.enchDoPostAttack(wireID, level, inUse, "victim", victim, attacker, src, ctx)
			})
		})
	}

	// (2) The ATTACKER's weapon (Fire Aspect): runIterationOnItem(weapon, MAINHAND, attacker, ...),
	// target ATTACKER. Requires a living attacker (source.getEntity() instanceof LivingEntity).
	if !stackEmpty(weapon) && attacker.valid() {
		forEachItemEnchantInSlot(weapon, eqSlotMainHand, func(wireID, level int) {
			t.enchDoPostAttack(wireID, level, weaponInUse, "attacker", victim, attacker, src, ctx)
		})
	}
}

// enchDoPostAttack is Enchantment.doPostAttack(ServerLevel, int level, EnchantedItemInUse,
// EnchantmentTarget target, Entity victim, DamageSource) + the static per-entry applier.
func (t *TickLoop) enchDoPostAttack(wireID, level int, inUse *enchItemInUse, target string, victim, attacker enchEntityRef, src damageSource, ctx *enchDamageCtx) {
	set := enchEffectsFor(wireID)
	if set == nil || len(set.postAttack) == 0 {
		return
	}
	ctx.level = level
	for _, entry := range set.postAttack {
		if entry.enchanted != target {
			continue // enchanted() != target: this entry belongs to the other side
		}
		if entry.req != nil && !entry.req.matches(ctx) {
			continue // TargetedConditionalEffect.matches(damageContext) failed
		}
		// The affected switch: ATTACKER -> source.getEntity(); DIRECT_ATTACKER ->
		// source.getDirectEntity() (the same entity for every wired direct-melee source);
		// VICTIM -> the victim parameter.
		var affected enchEntityRef
		switch entry.affected {
		case "attacker", "damaging_entity":
			affected = attacker
		case "victim":
			affected = victim
		}
		if !affected.valid() {
			continue // vanilla's `if (entity != null)` guard
		}
		entry.effect.apply(t, level, inUse, affected)
	}
}

// enchResolveEntity resolves a damage-source attacker id to a live entity ref (player first, then
// the current region's entity store — the same resolution dealDefaultKnockbackPlayer uses).
func (t *TickLoop) enchResolveEntity(id int32) enchEntityRef {
	if id == 0 {
		return enchEntityRef{}
	}
	if p := t.playerByEntityID(id); p != nil {
		return enchEntityRef{player: p}
	}
	if r := t.cur(); r != nil {
		if mob, ok := r.entities.get(id); ok {
			return enchEntityRef{mob: mob}
		}
	}
	return enchEntityRef{}
}

// enchWeaponInUse reads an entity's weapon (getWeaponItem() == the mainhand stack) plus the
// EnchantedItemInUse write-back for it (a weapon-stack mutation writes the player's held window
// slot / the mob's MAINHAND equipment slot).
func (t *TickLoop) enchWeaponInUse(owner enchEntityRef) (component.SlotData, *enchItemInUse) {
	if owner.player != nil {
		p := owner.player
		s := playerItemBySlot(p, eqSlotMainHand)
		inUse := &enchItemInUse{stack: s, slot: eqSlotMainHand, owner: owner, write: func(next component.SlotData) {
			inv := ensureInventory(p)
			before := inv.snapshot()
			inv.set(heldWindowSlot(inv.heldSlot), next)
			t.broadcastInventoryChanges(p, inv, before)
		}}
		return s, inUse
	}
	if owner.mob != nil {
		e := owner.mob
		s := e.getMainHandItem()
		inUse := &enchItemInUse{stack: s, slot: eqSlotMainHand, owner: owner, write: func(next component.SlotData) {
			e.setItemSlot(eqSlotMainHand, next)
		}}
		return s, inUse
	}
	return component.SlotData{}, nil
}

// enchForEachEquipmentInUse walks an entity's populated equipment slots in EquipmentSlot.VALUES
// order (runIterationOnEquipment), yielding each non-empty stack with its EnchantedItemInUse
// write-back (the ChangeItemDamage armor-wear path writes through it).
func (t *TickLoop) enchForEachEquipmentInUse(owner enchEntityRef, fn func(slot int, s component.SlotData, inUse *enchItemInUse)) {
	for slot := 0; slot < equipmentSlotCount; slot++ {
		if owner.player != nil {
			p := owner.player
			s := playerItemBySlot(p, slot)
			if stackEmpty(s) {
				continue
			}
			winSlot, ok := playerEquipWindowSlot(p, slot)
			slotCopy := slot
			inUse := &enchItemInUse{stack: s, slot: slotCopy, owner: owner, write: func(next component.SlotData) {
				if !ok {
					return
				}
				inv := ensureInventory(p)
				before := inv.snapshot()
				inv.set(winSlot, next)
				t.broadcastInventoryChanges(p, inv, before)
			}}
			fn(slot, s, inUse)
			continue
		}
		if owner.mob != nil {
			e := owner.mob
			s := e.getItemBySlot(slot)
			if stackEmpty(s) {
				continue
			}
			slotCopy := slot
			inUse := &enchItemInUse{stack: s, slot: slotCopy, owner: owner, write: func(next component.SlotData) {
				e.setItemSlot(slotCopy, next)
			}}
			fn(slot, s, inUse)
		}
	}
}

// playerEquipWindowSlot maps an EquipmentSlot ordinal to the player's 46-slot inventory window
// slot (the write-back home of playerItemBySlot's read): MAINHAND = 36+heldSlot, OFFHAND = 45,
// HEAD/CHEST/LEGS/FEET = 5/6/7/8. BODY/SADDLE have no player slot (ok=false).
func playerEquipWindowSlot(p *tickPlayer, slot int) (int16, bool) {
	inv := p.inventory
	if inv == nil {
		return 0, false
	}
	switch slot {
	case eqSlotMainHand:
		return heldWindowSlot(inv.heldSlot), true
	case eqSlotOffHand:
		return offhandWindowSlot, true
	case eqSlotHead:
		return 5, true
	case eqSlotChest:
		return 6, true
	case eqSlotLegs:
		return 7, true
	case eqSlotFeet:
		return 8, true
	}
	return 0, false
}

// ============================================================================================
// Mending — ExperienceOrb.repairPlayerItems + EnchantmentHelper.modifyDurabilityToRepairFromXp
// ============================================================================================

// repairPlayerItems is the port of net.minecraft.world.entity.ExperienceOrb.repairPlayerItems(
// ServerPlayer, int value): pick a RANDOM equipped item that is damaged and carries a
// REPAIR_WITH_XP enchant (Mending), convert the orb value to durability (x2 via the multiply
// effect), and recurse with the remainder. Returns the XP left over for giveExperiencePoints.
//
//	Optional<EnchantedItemInUse> pick = EnchantmentHelper.getRandomItemWith(REPAIR_WITH_XP, player,
//	                                                                        ItemStack::isDamaged);
//	if (pick.isPresent()) {
//	    ItemStack it = pick.get().itemStack();
//	    int toRepair = EnchantmentHelper.modifyDurabilityToRepairFromXp(level, it, value);
//	    int repaired = Math.min(toRepair, it.getDamageValue());
//	    it.setDamageValue(it.getDamageValue() - repaired);
//	    if (repaired > 0) {
//	        int remainder = value - repaired * value / toRepair;
//	        if (remainder > 0) return repairPlayerItems(player, remainder);
//	    }
//	    return 0;
//	}
//	return value;
//
//	[VERIFIED javap ExperienceOrb.repairPlayerItems — the full trace above, including the
//	 int-division remainder and the tail recursion.]
func (t *TickLoop) repairPlayerItems(p *tickPlayer, value int) int {
	stack, inUse, ok := t.enchGetRandomItemWithRepair(p)
	if !ok {
		return value
	}
	toRepair := t.enchModifyDurabilityToRepairFromXp(stack, value)
	damage := stackDamageValue(stack)
	repaired := toRepair
	if damage < repaired {
		repaired = damage // Math.min(toRepair, getDamageValue())
	}
	next := setStackDamageValue(stack, damage-repaired)
	if inUse.write != nil {
		inUse.write(next)
	}
	if repaired > 0 {
		remainder := value - repaired*value/toRepair // int division, exactly the bytecode
		if remainder > 0 {
			return t.repairPlayerItems(p, remainder)
		}
	}
	return 0
}

// enchGetRandomItemWithRepair is EnchantmentHelper.getRandomItemWith(REPAIR_WITH_XP, entity,
// ItemStack::isDamaged) for the player: walk EquipmentSlot.VALUES, keep each slot whose stack is
// damaged and carries an enchantment that (a) has the REPAIR_WITH_XP component and (b) matches the
// slot, then pick uniformly with the ENTITY's random (Util.getRandomSafe(list, getRandom()) ==
// list.get(random.nextInt(size))). One candidate entry per matching enchantment, as vanilla's
// inner loop adds one per entry.
//
//	[VERIFIED javap EnchantmentHelper.getRandomItemWith: per VALUES slot -> predicate.test(stack)
//	 gate -> per ENCHANTMENTS entry -> effects().has(component) && matchingSlot(slot) -> add
//	 EnchantedItemInUse; Util.getRandomSafe(list, entity.getRandom()).]
func (t *TickLoop) enchGetRandomItemWithRepair(p *tickPlayer) (component.SlotData, *enchItemInUse, bool) {
	owner := enchEntityRef{player: p}
	var candidates []*enchItemInUse
	t.enchForEachEquipmentInUse(owner, func(slot int, s component.SlotData, inUse *enchItemInUse) {
		// ItemStack.isDamaged(): isDamageableItem() && getDamageValue() > 0.
		if !stackIsDamageableItem(s) || stackDamageValue(s) <= 0 {
			return
		}
		forEachItemEnchant(s, func(wireID, _ int) {
			set := enchEffectsFor(wireID)
			if set == nil || len(set.repairWithXP) == 0 || !enchMatchingSlot(set, slot) {
				return
			}
			candidates = append(candidates, inUse)
		})
	})
	if len(candidates) == 0 {
		return component.SlotData{}, nil, false
	}
	// Util.getRandomSafe(list, entity.getRandom()): a uniform nextInt(size) pick off the PLAYER's
	// own RandomSource (Player.random — playerEnchantRandom). Drawn ONLY when a mending candidate
	// exists, so no oracle stream is perturbed.
	ensurePlayerEnchantState(p)
	pick := candidates[int(p.playerEnchantRandom.nextIntN(int32(len(candidates))))]
	return pick.stack, pick, true
}

// enchModifyDurabilityToRepairFromXp is EnchantmentHelper.modifyDurabilityToRepairFromXp(
// ServerLevel, ItemStack, int value): fold the stack's REPAIR_WITH_XP effects (Mending: multiply
// factor 2.0) over float(value), then `Math.max(0, (int) f)`. The FloatAction random is the LEVEL
// random (lambda$modifyItemFilteredCount$0 draws level.getRandom() — no shipped effect draws).
// Mending's effect list carries no requirements; the itemContext conditions are vacuous here.
func (t *TickLoop) enchModifyDurabilityToRepairFromXp(s component.SlotData, value int) int {
	f := float32(value)
	ctx := &enchDamageCtx{t: t}
	forEachItemEnchant(s, func(wireID, level int) {
		set := enchEffectsFor(wireID)
		if set == nil || len(set.repairWithXP) == 0 {
			return
		}
		ctx.level = level
		// The FloatAction RandomSource here is level.getRandom() (lambda$modifyItemFilteredCount$0);
		// mending's multiply draws nothing, so nil is observably identical (kept RNG-lazy).
		f = applyEnchCondValues(set.repairWithXP, ctx, nil, f)
	})
	if out := int(f); out > 0 {
		return out
	}
	return 0 // Math.max(0, mutable.intValue())
}
