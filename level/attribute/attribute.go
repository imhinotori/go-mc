// Package attribute is the LITERAL 1:1 port of the vanilla Minecraft Java 26.2
// (protocol 776) entity-attribute system, ported method-for-method from the
// unobfuscated server jar (temp/cache/26.2-inner.jar) via `javap -c -p` this
// session. No GPL source is pasted — every algorithm is re-expressed in idiomatic
// Go — but the structure, fold ordering, float/double ops, NaN/clamp guards, and
// the exact numeric constants are IDENTICAL to the bytecode.
//
// It models the four collaborating vanilla types:
//
//   - Attribute / RangedAttribute  (net.minecraft.world.entity.ai.attributes.Attribute,
//     RangedAttribute): a registered attribute's default value + the RangedAttribute
//     [min,max] envelope and sanitizeValue clamp.
//   - AttributeModifier + Operation (AttributeModifier, AttributeModifier$Operation):
//     a single additive/multiplicative modifier and the three fold operations.
//   - AttributeInstance (AttributeInstance): a per-entity attribute holder with its
//     own baseValue + the modifier set, and calculateValue — the EXACT fold
//     (ADD_VALUE -> freeze d1 -> ADD_MULTIPLIED_BASE -> ADD_MULTIPLIED_TOTAL ->
//     sanitizeValue).
//   - AttributeMap + AttributeSupplier (AttributeMap, AttributeSupplier): the
//     per-entity instance set backed by a default supplier (DefaultAttributes).
//
// SINGLE-OWNER (TICK-05): an AttributeMap is per-entity tick-owned game state,
// mutated ONLY on the tick goroutine. Nothing here is goroutine-safe by itself; the
// tick is the sole owner, exactly as vanilla's per-entity AttributeMap is touched
// only on the server thread.
package attribute

import "math"

// Operation is the port of net.minecraft.world.entity.ai.attributes.AttributeModifier$Operation:
// the three fold operations calculateValue applies, in this order. The integer ids match the
// enum's `id` field (ADD_VALUE=0, ADD_MULTIPLIED_BASE=1, ADD_MULTIPLIED_TOTAL=2), which is also
// the wire id (AttributeModifier$Operation.BY_ID / STREAM_CODEC) — kept exact so the NBT/packet
// codec lands on the same ids later.
type Operation int

const (
	// AddValue is ADD_VALUE (id 0): folded first, summed straight into the running base
	// (`d1 += modifier.amount()`).
	AddValue Operation = 0
	// AddMultipliedBase is ADD_MULTIPLIED_BASE (id 1): each modifier adds `d1 * amount` to the
	// total, where d1 is the base FROZEN after the ADD_VALUE pass (`d3 += d1 * amount`).
	AddMultipliedBase Operation = 1
	// AddMultipliedTotal is ADD_MULTIPLIED_TOTAL (id 2): each modifier scales the running total
	// by `(1.0 + amount)` (`d3 *= 1.0 + amount`).
	AddMultipliedTotal Operation = 2
)

// AttributeModifier is the port of the AttributeModifier record
// (net.minecraft.world.entity.ai.attributes.AttributeModifier): `record AttributeModifier(
// Identifier id, double amount, Operation operation)`. The id is the modifier's stable identity
// (a namespaced resource id in vanilla, e.g. "minecraft:random_spawn_bonus"); within an
// AttributeInstance the id is the map key, so a modifier with the same id is "already applied".
// amount is a double (the fold is double-precision) and operation selects the fold pass.
type AttributeModifier struct {
	// ID is the modifier's stable identity (vanilla Identifier). Used as the per-instance map
	// key (addModifier rejects a duplicate id; finalizeSpawn gates on hasModifier(id)).
	ID string
	// Amount is the modifier value (double). Its meaning depends on Operation.
	Amount float64
	// Operation selects which fold pass applies this modifier.
	Operation Operation
}

// Attribute is the port of net.minecraft.world.entity.ai.attributes.Attribute: a registered
// attribute carrying its descriptionId (name) and defaultValue. The base Attribute.sanitizeValue
// is the identity (returns the value unchanged); RangedAttribute overrides it with the
// NaN->min + clamp envelope (see RangedAttribute). Sentiment/syncable are render/wire concerns not
// needed by the gameplay fold, so they are intentionally omitted (the value math is identical).
type Attribute struct {
	// name is the attribute's descriptionId stem (the registry name, e.g. "max_health").
	name string
	// defaultValue is Attribute.defaultValue — the base an AttributeInstance starts at
	// (AttributeInstance ctor reads `attribute.getDefaultValue()` into baseValue).
	defaultValue float64
	// ranged carries the RangedAttribute [min,max] envelope when this attribute is a
	// RangedAttribute (the only Attribute subclass used by the entities here). A nil ranged means
	// the base Attribute (identity sanitizeValue). All vanilla attributes here are RangedAttribute,
	// so ranged is always set in practice — the nil case is the faithful base-Attribute fallback.
	ranged *rangedBounds
}

// rangedBounds is the RangedAttribute [min,max] envelope (RangedAttribute.minValue/maxValue).
type rangedBounds struct {
	min float64
	max float64
}

// NewAttribute is the port of `new Attribute(name, default)` — a plain (non-ranged) attribute
// whose sanitizeValue is the identity. None of the entities here use a bare Attribute (all are
// RangedAttribute), but the constructor exists for fidelity with the base class.
func NewAttribute(name string, defaultValue float64) *Attribute {
	return &Attribute{name: name, defaultValue: defaultValue}
}

// NewRangedAttribute is the port of `new RangedAttribute(name, default, min, max)`
// (net.minecraft.world.entity.ai.attributes.RangedAttribute.<init>). It mirrors the vanilla
// ctor's three argument-order invariants (min<=max, default>=min, default<=max) by panicking on a
// violation — these are programmer-error registration bugs (vanilla throws IllegalArgumentException
// in the same three checks), never runtime input, so a panic is the faithful Go analogue and keeps
// the registration table honest. The returned Attribute carries the ranged envelope so sanitizeValue
// applies the NaN->min + Mth.clamp behavior.
func NewRangedAttribute(name string, defaultValue, min, max float64) *Attribute {
	// RangedAttribute.<init>: `if (min > max) throw IllegalArgumentException("Minimum value cannot
	// be bigger than maximum value!");`
	if min > max {
		panic("attribute: minimum value cannot be bigger than maximum value")
	}
	// `if (default < min) throw IllegalArgumentException("Default value cannot be lower than minimum value!");`
	if defaultValue < min {
		panic("attribute: default value cannot be lower than minimum value")
	}
	// `if (default > max) throw IllegalArgumentException("Default value cannot be bigger than maximum value!");`
	if defaultValue > max {
		panic("attribute: default value cannot be bigger than maximum value")
	}
	return &Attribute{
		name:         name,
		defaultValue: defaultValue,
		ranged:       &rangedBounds{min: min, max: max},
	}
}

// Name returns the attribute's registry name (descriptionId stem). Used as the AttributeMap key
// and for the NBT "attributes" id.
func (a *Attribute) Name() string { return a.name }

// DefaultValue is the port of Attribute.getDefaultValue(): the base an AttributeInstance starts
// at (AttributeInstance ctor: `this.baseValue = attribute.getDefaultValue();`).
func (a *Attribute) DefaultValue() float64 { return a.defaultValue }

// sanitizeValue is the port of Attribute.sanitizeValue / RangedAttribute.sanitizeValue.
//
// Base Attribute.sanitizeValue (no ranged envelope): `return value;` — identity.
//
// RangedAttribute.sanitizeValue:
//
//	if (Double.isNaN(value)) return minValue;       // NaN collapses to the floor
//	return Mth.clamp(value, minValue, maxValue);    // otherwise clamp into [min,max]
//
// The NaN guard is FIRST (a NaN never reaches Mth.clamp), exactly as the bytecode orders it.
func (a *Attribute) sanitizeValue(value float64) float64 {
	if a.ranged == nil {
		// Base Attribute.sanitizeValue: identity.
		return value
	}
	// RangedAttribute.sanitizeValue: NaN -> minValue.
	if math.IsNaN(value) {
		return a.ranged.min
	}
	return mthClamp(value, a.ranged.min, a.ranged.max)
}

// mthClamp is the port of net.minecraft.util.Mth.clamp(double value, double min, double max):
//
//	if (value < min) return min;          // (bytecode: dcmpg ifge -> dload min dreturn)
//	return Math.min(value, max);          // else Math.min(value, max)
//
// NOTE the asymmetry, ported verbatim: the LOWER bound is a strict `<` test, while the UPPER bound
// goes through Math.min (which, for a non-NaN value, returns max when value > max and value
// otherwise). Re-expressing this as a naive `if value > max` would diverge on a -0.0/+0.0 edge that
// Math.min resolves; we mirror Math.min to stay bit-identical to vanilla. (NaN never reaches here —
// sanitizeValue's isNaN guard runs first.)
func mthClamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	return math.Min(value, max)
}
