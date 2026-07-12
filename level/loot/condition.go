package loot

// condition.go — the loot condition dispatch. The 5 chest groups use exactly one
// per-entry condition: abandoned_mineshaft's lone `location_check` (biome predicate
// on one music-disc entry). The dispatch is SHAPED so 20-02 adds the block-table
// condition delta (match_tool, survives_explosion, ...) DATA-ONLY.
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.predicates.LootItemCondition (Predicate<LootContext>)
//   - net.minecraft.world.level.storage.loot.predicates.LocationCheck (biome/structure predicate)
//   - net.minecraft.world.level.storage.loot.entries.LootPoolEntryContainer.canRun (compositeCondition.test)

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imhinotori/sulfur/data/registryid"
)

// allConditions mirrors the compositeCondition (Util.allOf): every condition must
// pass. An empty list passes (the all-of identity) — matching canRun for an entry
// with no conditions.
//
// Source: javap LootPoolEntryContainer.canRun (compositeCondition.test) / Util.allOf.
func allConditions(conds []LootCondition, ctx *LootContext) bool {
	for _, c := range conds {
		if !c.Test(ctx) {
			return false
		}
	}
	return true
}

// locationCheck mirrors net.minecraft.world.level.storage.loot.predicates.LocationCheck
// for the one chest use: a biome-membership predicate. The decompiled LocationCheck
// resolves the loot context's ORIGIN position and tests a LocationPredicate (here a
// biome set). The pure chest evaluator does not carry world geometry, so this reads
// the context's Biome field.
//
// CITED STUB (CLAUDE.md "stub behind a cited constant equal to the vanilla default,
// structured to become a real read later"): when the context Biome is unset (the
// pure golden path, and any pre-20-02 caller), the predicate returns TRUE — the
// permissive default that keeps the entry eligible. 20-02 populates ctx.Biome from
// the real chest-open LootParams, at which point this becomes the faithful
// biome-membership test against `biomes`. The golden table (simple_dungeon) has no
// conditions, so this stub never affects the seed-reproduction proof.
type locationCheck struct {
	biomes []string // the predicate's biome allow-set
}

// Test mirrors LocationCheck.test: biome membership at the loot origin (here the
// context Biome). Empty context Biome -> permissive TRUE (cited stub above).
func (l *locationCheck) Test(ctx *LootContext) bool {
	if ctx.Biome == "" {
		return true
	}
	for _, b := range l.biomes {
		if b == ctx.Biome {
			return true
		}
	}
	return false
}

// parseCondition decodes one condition object, dispatching on its "condition" type
// key (validated against registryid.LootConditionType). location_check is parsed
// faithfully; the block-table conditions (match_tool/survives_explosion/...) are
// shaped for 20-02 and error loudly until then. An unrecognized type errors loudly.
func parseCondition(rc rawCondition) (LootCondition, error) {
	typeStr, err := rawString(rc, "condition")
	if err != nil {
		return nil, err
	}
	if typeStr == "" {
		return nil, fmt.Errorf("condition missing \"condition\" type")
	}
	if !validType(typeStr, registryid.LootConditionType) {
		return nil, fmt.Errorf("unrecognized loot condition type %q", typeStr)
	}
	switch normalizeType(typeStr) {
	case "location_check":
		var pred struct {
			Predicate struct {
				// `biomes` is either a single tag/id string or a list of ids.
				Biomes json.RawMessage `json:"biomes"`
			} `json:"predicate"`
		}
		if raw, ok := rc["predicate"]; ok {
			// re-wrap: rc holds the WHOLE condition object; decode predicate.biomes.
			full, _ := json.Marshal(map[string]json.RawMessage{"predicate": raw})
			if err := json.Unmarshal(full, &pred); err != nil {
				return nil, fmt.Errorf("location_check predicate: %w", err)
			}
		}
		return &locationCheck{biomes: parseBiomeList(pred.Predicate.Biomes)}, nil
	case "match_tool":
		// MatchTool.test -> (TOOL != null) && (predicate.isEmpty() || predicate.get().test(TOOL)).
		// The block tables use two ItemPredicate forms: (a) an `items` HolderSet gate
		// (predicate.items = "minecraft:shears" — the leaf/vine shear form) and (b) a silk_touch
		// enchantment gate (predicate.predicates["minecraft:enchantments"] = [{silk_touch, min 1}]).
		// Parse BOTH so matchTool.Test ports ItemPredicate.test faithfully (items membership AND
		// the silk_touch sub-predicate); a form with neither is an empty predicate -> true under a
		// real tool. 20-02 Task 1 + this fix (javap MatchTool.test + ItemPredicate.test).
		return &matchTool{
			requireItems:      matchToolItemIDs(rc),
			requiresSilkTouch: matchToolWantsSilkTouch(rc),
		}, nil
	case "survives_explosion":
		// ExplosionCondition: no fields. EXPLOSION_RADIUS absent -> true (the v1
		// break default). 20-02 Task 1 (javap ExplosionCondition.test).
		return &explosionCondition{}, nil
	case "any_of":
		// AnyOfCondition (Phase 29-04, the entity-loot OR): test = OR over the child terms
		// (CompositeLootItemCondition with Predicate.or). The pig table wraps furnace_smelt in an
		// any_of(is_on_fire, attacker-smelts_loot). javap AnyOfCondition extends
		// CompositeLootItemCondition (the `terms` list, .or composition).
		var rcs []rawCondition
		if raw, ok := rc["terms"]; ok {
			if err := json.Unmarshal(raw, &rcs); err != nil {
				return nil, fmt.Errorf("any_of terms: %w", err)
			}
		}
		terms, err := parseConditions(rcs)
		if err != nil {
			return nil, err
		}
		return &anyOfCondition{terms: terms}, nil
	case "all_of":
		// AllOfCondition (the AND composite, the sibling of any_of): test = AND over the terms
		// (CompositeLootItemCondition with Predicate.and). Not used by the pig table but ported
		// alongside any_of for the other entity tables. javap AllOfCondition.and composition.
		var rcs []rawCondition
		if raw, ok := rc["terms"]; ok {
			if err := json.Unmarshal(raw, &rcs); err != nil {
				return nil, fmt.Errorf("all_of terms: %w", err)
			}
		}
		terms, err := parseConditions(rcs)
		if err != nil {
			return nil, err
		}
		return &allOfCondition{terms: terms}, nil
	case "killed_by_player":
		// LootItemKilledByPlayerCondition.test == hasParameter(LAST_DAMAGE_PLAYER) — the mob was
		// killed by a player. Reads ctx.KilledByPlayer (the death-source player-attack proxy).
		// javap LootItemKilledByPlayerCondition.test: hasParameter(LAST_DAMAGE_PLAYER); ireturn.
		return &killedByPlayerCondition{}, nil
	case "entity_properties":
		// LootItemEntityPropertyCondition.test: resolve the target entity (this / direct_attacker /
		// attacker) and test its EntityPredicate. The pig table uses two forms: (a) entity="this"
		// predicate.flags.is_on_fire (the victim is burning -> furnace_smelt), and (b)
		// entity="direct_attacker" predicate.equipment.mainhand enchants #smelts_loot. Parse which
		// form this is so the condition reads the matching cited-stub context field (VictimOnFire /
		// AttackerSmeltsLoot). javap LootItemEntityPropertyCondition.test (EntityPredicate.matches).
		return parseEntityProperties(rc)
	case "inverted":
		// InvertedLootItemCondition.test: NOT the inner term. The slime table wraps a
		// damage_source_properties(frog) in inverted -> "NOT killed by a frog" gates the slime_ball pool.
		// javap InvertedLootItemCondition.test: return !this.term.test(ctx).
		var inner rawCondition
		if raw, ok := rc["term"]; ok {
			if err := json.Unmarshal(raw, &inner); err != nil {
				return nil, fmt.Errorf("inverted term: %w", err)
			}
		}
		term, err := parseCondition(inner)
		if err != nil {
			return nil, err
		}
		return &invertedCondition{term: term}, nil
	case "damage_source_properties":
		// DamageSourceCondition.test: the kill DamageSource matches the predicate. The slime table's only
		// use is source_entity type == frog (the "killed by a frog" term, wrapped in inverted). v1 has no
		// frog kills, so this is a cited constant-FALSE (KilledByFrog default false) -> inverted -> true,
		// the correct v1 slime_ball drop. Structured so a real frog-source read slots in. Cite
		// DamageSourceCondition.test + DamageSourcePredicate (source_entity entity_type).
		return &damageSourceCondition{kind: damageSourceUnknown}, nil
	case "table_bonus":
		// BonusLevelTableCondition.test: read the TOOL's level for `enchantment` (0 when no tool / not
		// enchanted -> getOptionalParameter(TOOL) may be null, EnchantmentHelper.getItemEnchantmentLevel
		// returns 0), pick chances[min(level, chances.size-1)], and return random.nextFloat() < that. The
		// `chances` array is the values list (index 0 = no bonus, the decay/no-tool case). Used by the
		// leaves tables (sapling/stick chance scaled by fortune). javap BonusLevelTableCondition.test.
		var tb struct {
			Enchantment string    `json:"enchantment"`
			Chances     []float64 `json:"chances"`
		}
		full, _ := json.Marshal(rc)
		if err := json.Unmarshal(full, &tb); err != nil {
			return nil, fmt.Errorf("table_bonus: %w", err)
		}
		if len(tb.Chances) == 0 {
			return nil, fmt.Errorf("table_bonus: empty chances list")
		}
		return &tableBonusCondition{enchantment: tb.Enchantment, values: tb.Chances}, nil
	default:
		return nil, fmt.Errorf("loot condition %q not ported (location_check/match_tool/survives_explosion/any_of/all_of/killed_by_player/entity_properties/inverted/damage_source_properties/table_bonus in scope)", typeStr)
	}
}

// invertedCondition is the port of net.minecraft.world.level.storage.loot.predicates
// .InvertedLootItemCondition: test == !term.test(ctx). Used by the slime table (NOT killed by a frog).
type invertedCondition struct{ term LootCondition }

func (c *invertedCondition) Test(ctx *LootContext) bool { return !c.term.Test(ctx) }

// damageSourceKind is which death-source fact a damage_source_properties condition asserts. The only
// entity-table use (slime) is source_entity==frog, which v1 never satisfies -> damageSourceUnknown.
type damageSourceKind int

const (
	// damageSourceUnknown: a source form v1 does not model (source_entity==frog) -> Test FALSE (the
	// conservative default; wrapped in inverted it yields the correct "not a frog kill" true). Cited so
	// a real DamageSourcePredicate match slots in.
	damageSourceUnknown damageSourceKind = iota
)

// damageSourceCondition is the port of DamageSourceCondition for the entity-table form. Test reads the
// matching cited-stub context fact; an unmodeled form (source_entity==frog) tests FALSE.
type damageSourceCondition struct{ kind damageSourceKind }

func (c *damageSourceCondition) Test(ctx *LootContext) bool {
	switch c.kind {
	default:
		return false // unmodeled source form (e.g. killed-by-frog) -> conservative false.
	}
}

// tableBonusCondition ports net.minecraft.world.level.storage.loot.predicates.BonusLevelTableCondition:
// read the TOOL's level for `enchantment` (0 when no tool / not enchanted), index the `values` list at
// min(level, len-1), and return random.nextFloat() < that chance. The world-driven decay path (no tool)
// takes index 0 -- the base drop chance (e.g. a leaf's 5% sapling). Cite BonusLevelTableCondition.test.
type tableBonusCondition struct {
	enchantment string
	values      []float64
}

func (c *tableBonusCondition) Test(ctx *LootContext) bool {
	// EnchantmentHelper.getItemEnchantmentLevel(this.enchantment, tool): 0 when the tool is absent or
	// unenchanted. Mirrors applyBonusCount's read (map first, then the fortune fast-path field).
	level := 0
	if lvl, ok := ctx.ToolEnchantments[c.enchantment]; ok {
		level = lvl
	} else if lvl, ok := ctx.ToolEnchantments[normalizeType(c.enchantment)]; ok {
		level = lvl
	} else if c.enchantment == "minecraft:fortune" {
		level = ctx.ToolFortuneLevel
	}
	idx := level
	if idx > len(c.values)-1 {
		idx = len(c.values) - 1 // Math.min(level, values.size() - 1)
	}
	if idx < 0 {
		idx = 0
	}
	return float64(ctx.Random().NextFloat()) < c.values[idx]
}

// anyOfCondition is the port of net.minecraft.world.level.storage.loot.predicates.AnyOfCondition
// (CompositeLootItemCondition with Predicate.or): test passes if ANY child term passes. An empty
// terms list is FALSE (the OR identity — vanilla's CompositeLootItemCondition.or over no terms is
// the constant-false predicate). Short-circuits on the first passing term.
type anyOfCondition struct {
	terms []LootCondition
}

func (a *anyOfCondition) Test(ctx *LootContext) bool {
	for _, t := range a.terms {
		if t.Test(ctx) {
			return true
		}
	}
	return false
}

// allOfCondition is the port of AllOfCondition (CompositeLootItemCondition with Predicate.and):
// test passes if EVERY child term passes (an empty list is TRUE — the AND identity). Reuses the
// allConditions fold semantics.
type allOfCondition struct {
	terms []LootCondition
}

func (a *allOfCondition) Test(ctx *LootContext) bool {
	return allConditions(a.terms, ctx)
}

// killedByPlayerCondition is the port of LootItemKilledByPlayerCondition: the mob was killed by a
// player (hasParameter(LAST_DAMAGE_PLAYER)). Reads the entity context's KilledByPlayer bit.
type killedByPlayerCondition struct{}

func (k *killedByPlayerCondition) Test(ctx *LootContext) bool {
	return ctx.KilledByPlayer
}

// entityPropertyKind is which entity-context fact an entity_properties condition asserts (the two
// forms the entity tables use). Each maps to a cited-stub context field with the v1 vanilla default.
type entityPropertyKind int

const (
	// entityPropOnFire: entity="this" predicate.flags.is_on_fire — the VICTIM is burning (reads
	// VictimOnFire, v1 default false).
	entityPropOnFire entityPropertyKind = iota
	// entityPropAttackerSmeltsLoot: entity="direct_attacker" predicate.equipment.mainhand enchants
	// #smelts_loot — the ATTACKER's weapon smelts loot (reads AttackerSmeltsLoot, v1 default false).
	entityPropAttackerSmeltsLoot
	// entityPropInOpenWater: entity="this" predicate.type_specific/fishing_hook.in_open_water==true —
	// THIS_ENTITY (the FishingHook) is fishing in OPEN water (reads InOpenWater, the retrieve-time
	// isOpenWaterFishing()). Gates the fishing table's TREASURE sub-table.
	entityPropInOpenWater
	// entityPropCubeMobSize: entity="this" predicate.type_specific/cube_mob.size==N — THIS_ENTITY
	// (a slime/magma_cube) has getSize()==N (reads CubeMobSize). Gates the per-size pools of the
	// slime/magma_cube tables (slimeball pool: size 1). The wanted size is carried in the condition.
	// Source: javap CubeMobPredicate + entities/slime.json type_specific/cube_mob.size.
	entityPropCubeMobSize
	// entityPropRaiderIsCaptain: entity="this" predicate.type_specific/raider.is_captain==true —
	// THIS_ENTITY (a raider) is a raid CAPTAIN (Raider.isCaptain — ominous banner in HEAD + patrol
	// leader). Reads RaiderIsCaptain. Gates the pillager/vindicator/… ominous_bottle pool. Source:
	// javap RaiderPredicate (isCaptain match) + entities/pillager.json type_specific/raider.is_captain.
	entityPropRaiderIsCaptain
	// entityPropUnknown: a form this v1 port does not model (a future entity table) — TEST FALSE
	// (the conservative default: a drop gated on an unmodeled predicate does not fire, never a
	// wrong/extra drop). Cited so the real EntityPredicate match slots in later.
	entityPropUnknown
)

// entityPropertyCondition is the port of LootItemEntityPropertyCondition for the two entity-table
// forms (is_on_fire on "this", smelts_loot on "direct_attacker"). Test reads the matching cited-stub
// context field. An unrecognized form tests FALSE (the conservative default — never a wrong drop).
type entityPropertyCondition struct {
	kind entityPropertyKind
	// wantSize is the target getSize() for an entityPropCubeMobSize form (the type_specific/cube_mob
	// size term). Zero for every other kind.
	wantSize int
}

func (e *entityPropertyCondition) Test(ctx *LootContext) bool {
	switch e.kind {
	case entityPropOnFire:
		return ctx.VictimOnFire
	case entityPropAttackerSmeltsLoot:
		return ctx.AttackerSmeltsLoot
	case entityPropInOpenWater:
		return ctx.InOpenWater
	case entityPropCubeMobSize:
		return ctx.CubeMobSize == e.wantSize
	case entityPropRaiderIsCaptain:
		return ctx.RaiderIsCaptain
	default:
		return false // unmodeled predicate form -> conservative false (no wrong drop).
	}
}

// parseEntityProperties decodes an entity_properties condition into the entity-table form it asserts.
// It inspects entity (this / direct_attacker) + predicate (flags.is_on_fire | equipment.mainhand
// enchants) to pick the cited-stub field the Test reads. A form this v1 port does not model parses to
// entityPropUnknown (Test -> false), erroring on nothing (the table is trusted/vendored).
func parseEntityProperties(rc rawCondition) (LootCondition, error) {
	ent, err := rawString(rc, "entity")
	if err != nil {
		return nil, err
	}
	predRaw := rc["predicate"]
	switch ent {
	case "this":
		// predicate.minecraft:flags.is_on_fire == true -> the victim-on-fire form.
		if entityPredicateWantsOnFire(predRaw) {
			return &entityPropertyCondition{kind: entityPropOnFire}, nil
		}
		// predicate.minecraft:type_specific/fishing_hook.in_open_water == true -> the fishing form.
		if entityPredicateWantsOpenWater(predRaw) {
			return &entityPropertyCondition{kind: entityPropInOpenWater}, nil
		}
		// predicate.minecraft:type_specific/cube_mob.size == N -> the cube-mob (slime/magma_cube) form.
		if sz, ok := entityPredicateCubeMobSize(predRaw); ok {
			return &entityPropertyCondition{kind: entityPropCubeMobSize, wantSize: sz}, nil
		}
		// predicate.minecraft:type_specific/raider.is_captain == true -> the raid-captain form.
		if entityPredicateWantsRaiderCaptain(predRaw) {
			return &entityPropertyCondition{kind: entityPropRaiderIsCaptain}, nil
		}
		return &entityPropertyCondition{kind: entityPropUnknown}, nil
	case "direct_attacker", "attacker":
		// predicate.minecraft:equipment.mainhand enchants #smelts_loot -> the smelts-loot form.
		return &entityPropertyCondition{kind: entityPropAttackerSmeltsLoot}, nil
	default:
		return &entityPropertyCondition{kind: entityPropUnknown}, nil
	}
}

// entityPredicateWantsOnFire reports whether an entity_properties predicate gates on the entity being
// on fire (predicate.minecraft:flags.is_on_fire == true) — the pig table's "this is burning" term.
func entityPredicateWantsOnFire(predRaw json.RawMessage) bool {
	if len(predRaw) == 0 {
		return false
	}
	var pred struct {
		Flags struct {
			IsOnFire *bool `json:"is_on_fire"`
		} `json:"minecraft:flags"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return false
	}
	return pred.Flags.IsOnFire != nil && *pred.Flags.IsOnFire
}

// entityPredicateWantsOpenWater reports whether an entity_properties predicate gates on the FishingHook
// being in open water (predicate.minecraft:type_specific/fishing_hook.in_open_water == true) — the
// fishing table's treasure entry. Source: the gameplay/fishing.json treasure entry condition +
// javap FishingHookPredicate (inOpenWater Optional<Boolean> matches).
func entityPredicateWantsOpenWater(predRaw json.RawMessage) bool {
	if len(predRaw) == 0 {
		return false
	}
	// The predicate key is the single string "minecraft:type_specific/fishing_hook" (a slash in the
	// key, NOT a nested object) mapping to { "in_open_water": <bool> }.
	var pred struct {
		FishingHook struct {
			InOpenWater *bool `json:"in_open_water"`
		} `json:"minecraft:type_specific/fishing_hook"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return false
	}
	w := pred.FishingHook.InOpenWater
	return w != nil && *w
}

// entityPredicateCubeMobSize reports whether an entity_properties predicate gates on a cube-mob's
// getSize() (predicate.minecraft:type_specific/cube_mob.size == N) and returns the wanted size. The
// slime/magma_cube tables use the EXACT-int form ("size": 1) -- an integer literal (JSON number),
// which CubeMobPredicate reads as a MinMaxBounds.Ints exact bound. A predicate without a cube_mob
// size term returns (0, false). Source: entities/slime.json type_specific/cube_mob.size +
// javap CubeMobPredicate (size MinMaxBounds.Ints).
func entityPredicateCubeMobSize(predRaw json.RawMessage) (int, bool) {
	if len(predRaw) == 0 {
		return 0, false
	}
	// The predicate key is the single string "minecraft:type_specific/cube_mob" (a slash in the key)
	// mapping to { "size": <int> }. The slime table uses the exact-int form.
	var pred struct {
		CubeMob struct {
			Size *int `json:"size"`
		} `json:"minecraft:type_specific/cube_mob"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return 0, false
	}
	if pred.CubeMob.Size == nil {
		return 0, false
	}
	return *pred.CubeMob.Size, true
}

// entityPredicateWantsRaiderCaptain reports whether an entity_properties predicate gates on THIS_ENTITY
// being a raid captain (predicate.minecraft:type_specific/raider.is_captain == true) — the illager
// tables' ominous_bottle pool. The predicate key is the single string "minecraft:type_specific/raider"
// (a slash in the key) mapping to { "is_captain": <bool> }. A predicate without the raider is_captain
// term returns false. Source: entities/pillager.json type_specific/raider.is_captain + javap RaiderPredicate.
func entityPredicateWantsRaiderCaptain(predRaw json.RawMessage) bool {
	if len(predRaw) == 0 {
		return false
	}
	var pred struct {
		Raider struct {
			IsCaptain *bool `json:"is_captain"`
		} `json:"minecraft:type_specific/raider"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return false
	}
	return pred.Raider.IsCaptain != nil && *pred.Raider.IsCaptain
}

// matchToolItemIDs decodes an ItemPredicate's `items` field (predicate.items) into the set of
// item resource ids the tool must be one of. ItemPredicate.items is an Optional<HolderSet<Item>>;
// MatchTool -> ItemPredicate.test tests tool.is(items) when it is present. The leaf/vine tables
// use the literal single-id form ("items": "minecraft:shears"); the codec also accepts a list of
// ids and a "#tag" reference. A predicate WITHOUT an items field returns nil (items Optional empty
// -> that sub-predicate is skipped, per ItemPredicate.test's isPresent guard). Tag references
// (a leading '#') are kept verbatim; the leaf tables never use one, so an unexpanded tag simply
// never matches a concrete tool id (conservative — never a wrong leaf-block drop).
//
// Source: javap ItemPredicate.test (items.isPresent() && !tool.is(items.get()) -> false).
func matchToolItemIDs(rc rawCondition) []string {
	predRaw, ok := rc["predicate"]
	if !ok {
		return nil
	}
	var pred struct {
		// `items` is either a single id/tag string or a JSON array of ids.
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return nil
	}
	if len(pred.Items) == 0 {
		return nil
	}
	ids := parseBiomeList(pred.Items) // reuse the single-or-list string decoder
	if len(ids) == 0 {
		return nil
	}
	// Normalize each concrete id to the "minecraft:"-prefixed form the context carries; keep a
	// "#tag" reference verbatim (it will never equal a concrete ToolItemID -> no match).
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if len(id) > 0 && id[0] == '#' {
			out = append(out, id)
			continue
		}
		if !strings.Contains(id, ":") {
			id = "minecraft:" + id
		}
		out = append(out, id)
	}
	return out
}

// matchToolWantsSilkTouch reports whether a match_tool condition's predicate gates on a
// silk_touch enchantment (level>=1) — the only match_tool form the block tables use. It
// inspects predicate.predicates["minecraft:enchantments"][*].enchantments for
// "minecraft:silk_touch". A predicate without an enchantment gate returns false (the
// matchTool then defaults to the predicate.isEmpty()->true path under a real tool).
//
// Source: blocks/*.json match_tool predicate shape (verified against the datagen JSON).
func matchToolWantsSilkTouch(rc rawCondition) bool {
	predRaw, ok := rc["predicate"]
	if !ok {
		return false
	}
	var pred struct {
		Predicates struct {
			Enchantments []struct {
				Enchantments json.RawMessage `json:"enchantments"`
			} `json:"minecraft:enchantments"`
		} `json:"predicates"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return false
	}
	for _, e := range pred.Predicates.Enchantments {
		// `enchantments` is either a single id string or a list of ids.
		for _, id := range parseBiomeList(e.Enchantments) { // reuse the single-or-list decoder
			if id == "minecraft:silk_touch" || id == "silk_touch" {
				return true
			}
		}
	}
	return false
}

// parseBiomeList accepts either a single biome/tag string or a JSON array of biome
// ids, returning the flat list. (A "#tag" reference is kept verbatim; the pure
// evaluator's permissive stub never needs it expanded.)
func parseBiomeList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	return nil
}
