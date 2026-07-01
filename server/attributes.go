package server

import "github.com/imhinotori/sulfur/level/attribute"

// attributes.go — the minimal, FAITHFUL per-player attribute holder the 1:1 melee-combat port
// reads from (instead of hardcoding numbers). It is a literal port of the relevant slice of the
// vanilla attribute system: each attribute has a registered BASE value (from
// net.minecraft.world.entity.ai.attributes.Attributes.<clinit>, the RangedAttribute default
// argument) which Player.createAttributes / LivingEntity.createLivingAttributes may OVERRIDE for
// the player default supplier. getAttributeValue(attr) returns base (no modifiers yet — items and
// effects compose via modifiers in a later plan), exactly mirroring vanilla's
// AttributeInstance.getValue() when no modifiers are attached.
//
// ============================================================================================
// LITERAL 1:1 PORT OF VANILLA JAVA 26.2 (protocol 776) — verified method-for-method against
// temp/cache/26.2-inner.jar via `javap -c -p` this session. No GPL source is pasted; the
// algorithm (per-attribute base, getAttributeValue == base when modifier-free) is re-expressed
// in Go with IDENTICAL numeric constants.
//
// ATTRIBUTE REGISTRATION DEFAULTS (Attributes.<clinit>, `new RangedAttribute(name, DEFAULT, min, max)`):
//
//	armor                  -> RangedAttribute("armor",                  0.0,  0.0,   30.0)   base 0.0
//	armor_toughness        -> RangedAttribute("armor_toughness",        0.0,  0.0,   20.0)   base 0.0
//	attack_damage          -> RangedAttribute("attack_damage",          2.0,  0.0, 2048.0)   base 2.0
//	attack_knockback       -> RangedAttribute("attack_knockback",       0.0,  0.0,    5.0)   base 0.0
//	attack_speed           -> RangedAttribute("attack_speed",           4.0,  0.0, 1024.0)   base 4.0
//	entity_interaction_range -> RangedAttribute("entity_interaction_range", 3.0, 0.0, 64.0)  base 3.0
//	knockback_resistance   -> RangedAttribute("knockback_resistance",   0.0, -2.0,    1.0)   base 0.0
//	max_absorption         -> RangedAttribute("max_absorption",         0.0,  0.0, 2048.0)   base 0.0
//	max_health             -> RangedAttribute("max_health",            20.0,  1.0, 1024.0)   base 20.0
//	movement_speed         -> RangedAttribute("movement_speed",         (varies)        )    (Player overrides 0.1)
//	sweeping_damage_ratio  -> RangedAttribute("sweeping_damage_ratio",  0.0,  0.0,    1.0)   base 0.0
//	step_height            -> RangedAttribute("step_height",            0.6,  0.0,   10.0)   base 0.6
//	safe_fall_distance     -> RangedAttribute("safe_fall_distance",     3.0, -1024.0, 1024.0) base 3.0
//
// PLAYER DEFAULT SUPPLIER (Player.createAttributes() extends LivingEntity.createLivingAttributes()):
//   - .add(ATTACK_DAMAGE,   1.0d)  -> OVERRIDES the 2.0 registration default to 1.0 for a player
//   - .add(MOVEMENT_SPEED,  0.10000000149011612d) -> 0.1 (float-widened double, vanilla literal)
//   - .add(ATTACK_SPEED)          -> no override, uses the 4.0 registration default
//   - .add(SWEEPING_DAMAGE_RATIO) -> no override, uses the 0.0 registration default
//   - createLivingAttributes adds MAX_HEALTH(20), ARMOR(0), ARMOR_TOUGHNESS(0), KNOCKBACK_RESISTANCE(0),
//     MAX_ABSORPTION(0), STEP_HEIGHT(0.6), SAFE_FALL_DISTANCE(3.0), ENTITY_INTERACTION_RANGE(3.0), etc.
//     with no overrides -> registration defaults.
//
// So the PLAYER base for each attribute the melee port reads is the value below. These are the
// AttributeSupplier.getValue() returns for a modifier-free player — the literal numbers vanilla's
// getAttributeValue(holder) yields before any item/effect modifier exists.
// ============================================================================================

// attributeKey identifies a single attribute in the per-player holder. It is a Go stand-in for
// the vanilla Holder<Attribute> key; only the attributes the melee-combat port reads are modeled
// (the holder is intentionally minimal, not the full vanilla registry).
type attributeKey int

const (
	attrAttackDamage attributeKey = iota
	attrAttackSpeed
	attrAttackKnockback
	attrArmor
	attrArmorToughness
	attrKnockbackResistance
	attrSweepingDamageRatio
	attrMaxHealth
	attrMovementSpeed
	attrMaxAbsorption
	attrEntityInteractionRange
	attrStepHeight
	attrSafeFallDistance
)

// playerAttributeBase is the per-player BASE value for each attribute — the value
// AttributeSupplier.getValue() returns for a modifier-free player, i.e. what
// getAttributeValue(holder) yields before any item/effect modifier is attached. Every constant is
// the jar-verified value from the Player default supplier (createAttributes extending
// createLivingAttributes), as documented in the file header. All are doubles to mirror vanilla's
// double-precision attribute math (getAttributeValue returns a double; callers d2f as vanilla does).
var playerAttributeBase = map[attributeKey]float64{
	attrAttackDamage:           1.0,                 // Player.createAttributes .add(ATTACK_DAMAGE, 1.0) overrides the 2.0 default
	attrAttackSpeed:            4.0,                 // registration default (Player .add(ATTACK_SPEED) no override)
	attrAttackKnockback:        0.0,                 // registration default
	attrArmor:                  0.0,                 // registration default (no armor items yet)
	attrArmorToughness:         0.0,                 // registration default (no armor items yet)
	attrKnockbackResistance:    0.0,                 // registration default
	attrSweepingDamageRatio:    0.0,                 // registration default (Player .add(SWEEPING_DAMAGE_RATIO) no override)
	attrMaxHealth:              20.0,                // createLivingAttributes registration default
	attrMovementSpeed:          0.10000000149011612, // Player .add(MOVEMENT_SPEED, 0.1) — the vanilla float-widened double literal
	attrMaxAbsorption:          0.0,                 // registration default
	attrEntityInteractionRange: 3.0,                 // registration default
	attrStepHeight:             0.6,                 // createLivingAttributes STEP_HEIGHT registration default (Player no override)
	attrSafeFallDistance:       3.0,                 // createLivingAttributes SAFE_FALL_DISTANCE registration default (Player no override)
}

// attributeHolder is a per-player map of attribute -> base value. It is the Go stand-in for
// vanilla's AttributeMap (the per-entity AttributeInstance set). v1 stores only the BASE value;
// item/effect MODIFIERS are not yet modeled, so getValue() == base. The struct is deliberately a
// thin wrapper around a map so that when modifiers arrive each entry becomes a base + a modifier
// list and getValue() folds them in — with no change to the call sites in combat.go.
type attributeHolder struct {
	// base is attribute -> base value. Seeded lazily from playerAttributeBase on first access so
	// no registration code path is touched (the holder self-initializes on the tick goroutine).
	base map[attributeKey]float64

	// modifiers is attribute -> (modifierID -> modifier). The effect subsystem (mob_effect.go) attaches
	// a modifier here when a modifier-bearing effect starts (slowness → MOVEMENT_SPEED, weakness →
	// ATTACK_DAMAGE) and removes it on expiry. getAttributeValue folds them via calculateValue (the
	// vanilla AttributeInstance.calculateValue order: ADD_VALUE, then ADD_MULTIPLIED_BASE, then
	// ADD_MULTIPLIED_TOTAL). Nil until the first modifier attaches (a modifier-free player stays base-only).
	modifiers map[attributeKey]map[string]attribute.AttributeModifier
}

// addModifier attaches (or replaces by id) an effect attribute modifier to the given attribute.
func (h *attributeHolder) addModifier(attr attributeKey, m attribute.AttributeModifier) {
	if h.modifiers == nil {
		h.modifiers = make(map[attributeKey]map[string]attribute.AttributeModifier)
	}
	bucket := h.modifiers[attr]
	if bucket == nil {
		bucket = make(map[string]attribute.AttributeModifier)
		h.modifiers[attr] = bucket
	}
	bucket[m.ID] = m
}

// removeModifier detaches the modifier with this id from the attribute (a no-op if absent).
func (h *attributeHolder) removeModifier(attr attributeKey, id string) {
	if h.modifiers == nil {
		return
	}
	if bucket := h.modifiers[attr]; bucket != nil {
		delete(bucket, id)
	}
}

// newAttributeHolder builds a holder seeded with the vanilla player default base values
// (playerAttributeBase). This is the Go analogue of constructing an AttributeMap from
// Player.createAttributes()'s AttributeSupplier — every attribute starts at its player default.
func newAttributeHolder() *attributeHolder {
	base := make(map[attributeKey]float64, len(playerAttributeBase))
	for k, v := range playerAttributeBase {
		base[k] = v
	}
	return &attributeHolder{base: base}
}

// getAttributeValue mirrors LivingEntity.getAttributeValue(Holder<Attribute>) for the
// modifier-free case: it returns the attribute's current value, which (with no modifiers
// attached) is exactly the base value. Vanilla's AttributeInstance.getValue() computes
// base + sum(modifiers); v1 has no modifiers, so the sum is empty and getValue() == base. An
// unknown key (not in the player default supplier) returns 0.0, the same as vanilla's
// AttributeSupplier default for an unregistered attribute.
//
// The return is a float64 (double), exactly as vanilla's getAttributeValue returns a double; the
// melee-combat call sites apply the d2f narrowing cast at the point vanilla does (e.g.
// `(float) getAttributeValue(ATTACK_DAMAGE)` in Player.attack), so the cast location is faithful.
func (h *attributeHolder) getAttributeValue(attr attributeKey) float64 {
	if h == nil || h.base == nil {
		// Defensive: an uninitialized holder behaves as the player default supplier (the value
		// vanilla's modifier-free AttributeInstance would yield). This keeps getAttributeValue
		// total even if a tickPlayer is constructed without an explicit holder.
		if v, ok := playerAttributeBase[attr]; ok {
			return v
		}
		return 0.0
	}
	base := h.base[attr]
	// Fold any effect modifiers via the vanilla AttributeInstance.calculateValue order: ADD_VALUE first
	// (summed into base), then ADD_MULTIPLIED_BASE (of the pre-multiply base), then ADD_MULTIPLIED_TOTAL
	// (compounded on the running result). A modifier-free attribute returns base unchanged.
	bucket := h.modifiers[attr]
	if len(bucket) == 0 {
		return base
	}
	for _, m := range bucket {
		if m.Operation == attribute.AddValue {
			base += m.Amount
		}
	}
	result := base
	for _, m := range bucket {
		if m.Operation == attribute.AddMultipliedBase {
			result += base * m.Amount
		}
	}
	for _, m := range bucket {
		if m.Operation == attribute.AddMultipliedTotal {
			result *= 1.0 + m.Amount
		}
	}
	return result
}

// playerAttributes returns the tickPlayer's attribute holder, lazily seeding it with the vanilla
// player default base values on first access. Lazy seeding (rather than seeding at registration)
// keeps gameplay_tick.go / drainRegistrations untouched — the holder self-initializes the first
// time the combat path reads an attribute, on the tick goroutine (TICK-05), so it stays -race
// clean by the single-owner discipline. This is the single accessor the melee-combat port uses to
// read every attribute (getAttributeValue), so when item/effect modifiers arrive they attach here
// and every combat formula reads the composed value with no call-site change.
func (p *tickPlayer) playerAttributes() *attributeHolder {
	if p.attributes == nil {
		p.attributes = newAttributeHolder()
	}
	return p.attributes
}

// getAttributeValue is the tickPlayer-level convenience that mirrors
// LivingEntity.getAttributeValue(holder): it reads through the lazily-seeded per-player holder.
// Every melee-combat call site goes through this so the read is uniform and the d2f cast (where
// vanilla narrows the double result to a float) is applied by the caller exactly where the
// bytecode does.
func (p *tickPlayer) getAttributeValue(attr attributeKey) float64 {
	return p.playerAttributes().getAttributeValue(attr)
}
