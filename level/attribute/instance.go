package attribute

// instance.go — the port of net.minecraft.world.entity.ai.attributes.AttributeInstance: a single
// attribute's per-entity holder (its own baseValue + the modifier set) and calculateValue, the
// EXACT fold the whole subsystem hinges on.
//
// Verified method-for-method against AttributeInstance bytecode (javap -c -p,
// temp/cache/26.2-inner.jar) this session — see calculateValue for the bytecode trace.

// AttributeInstance is the port of net.minecraft.world.entity.ai.attributes.AttributeInstance: one
// attribute attached to one entity, carrying the entity's baseValue for that attribute plus the
// modifiers grouped by operation. getValue() folds them via calculateValue (vanilla caches behind a
// dirty flag; the cache is a pure performance detail — getValue() always equals calculateValue() —
// so we recompute on read, which is observably identical and -race trivial under single-owner tick
// ownership).
type AttributeInstance struct {
	// attribute is the registered Attribute this instance is an instance OF — its defaultValue
	// seeds baseValue and its sanitizeValue is applied at the tail of calculateValue.
	attribute *Attribute

	// baseValue is AttributeInstance.baseValue: the entity's base for this attribute. Seeded from
	// attribute.getDefaultValue() at construction (the AttributeInstance ctor), then overridden by
	// the supplier (AttributeSupplier$Builder.add(holder, d) -> setBaseValue(d)) or by a later
	// setBaseValue.
	baseValue float64

	// modifiersByOperation groups the active modifiers by their Operation (vanilla's
	// modifiersByOperation EnumMap). calculateValue iterates each operation's bucket in the fixed
	// order ADD_VALUE -> ADD_MULTIPLIED_BASE -> ADD_MULTIPLIED_TOTAL. Within an operation the
	// iteration order over a Java EnumMap->Object2ObjectArrayMap is insertion order; the fold for
	// ADD_VALUE/ADD_MULTIPLIED_BASE is order-INVARIANT (pure additive sums), and
	// ADD_MULTIPLIED_TOTAL is a product of (1+amount) factors which is ALSO commutative, so the Go
	// map's unspecified iteration order yields the identical double result. (Vanilla's double
	// addition is not associative in general, but the multi-modifier-same-operation case is rare;
	// for the entities here at most one modifier per operation is ever attached, so the order
	// question never even arises.)
	modifiersByOperation map[Operation]map[string]AttributeModifier

	// modifierByID is vanilla's modifierById: every active modifier keyed by its id, for the
	// hasModifier(id) / getModifier(id) / removeModifier(id) lookups and the duplicate-id guard.
	modifierByID map[string]AttributeModifier

	// permanent records the ids of permanent modifiers (AddPermanentModifier), mirroring vanilla's
	// permanentModifiers map. It is NOT consulted by the value fold (calculateValue folds every
	// modifier regardless); it exists so a future pack()/entity-NBT save serializes exactly the
	// permanent set (e.g. the finalizeSpawn random-spawn bonus), as vanilla's save does. CITED TODO:
	// entity attribute persistence (the NBT "attributes" tag) is not yet wired into entity save/load
	// in Sulfur, so this is recorded but not yet serialized — see pack() in supplier.go.
	permanent []string
}

// newAttributeInstance is the port of the AttributeInstance ctor: `this.baseValue =
// attribute.getDefaultValue();` plus the empty modifier maps. The onDirty consumer is a
// cache-invalidation callback in vanilla; we recompute on read, so it is intentionally omitted.
func newAttributeInstance(attribute *Attribute) *AttributeInstance {
	return &AttributeInstance{
		attribute:            attribute,
		baseValue:            attribute.DefaultValue(),
		modifiersByOperation: make(map[Operation]map[string]AttributeModifier),
		modifierByID:         make(map[string]AttributeModifier),
	}
}

// Attribute returns the registered Attribute this instance is an instance of.
func (i *AttributeInstance) Attribute() *Attribute { return i.attribute }

// BaseValue is the port of AttributeInstance.getBaseValue(): the entity's base for this attribute,
// BEFORE any modifier fold.
func (i *AttributeInstance) BaseValue() float64 { return i.baseValue }

// SetBaseValue is the port of AttributeInstance.setBaseValue(double): sets the base (vanilla
// short-circuits when unchanged and flips the dirty flag; with no cache the assignment is enough).
func (i *AttributeInstance) SetBaseValue(v float64) { i.baseValue = v }

// HasModifier is the port of AttributeInstance.hasModifier(Identifier): whether a modifier with
// this id is attached. finalizeSpawn gates the random-spawn bonus on `!hasModifier(RANDOM_SPAWN_BONUS_ID)`.
func (i *AttributeInstance) HasModifier(id string) bool {
	_, ok := i.modifierByID[id]
	return ok
}

// GetModifier is the port of AttributeInstance.getModifier(Identifier): the attached modifier with
// this id, or (zero, false) if none.
func (i *AttributeInstance) GetModifier(id string) (AttributeModifier, bool) {
	m, ok := i.modifierByID[id]
	return m, ok
}

// addModifier is the port of AttributeInstance.addModifier(AttributeModifier) (the private helper
// behind addTransientModifier): rejects a duplicate id (vanilla throws
// IllegalArgumentException("Modifier is already applied on this attribute!")), then files the
// modifier into both modifierById and the per-operation bucket. The duplicate is a programmer error
// (the same id added twice), so a panic is the faithful analogue. addOrUpdateTransientModifier (the
// non-throwing put) and addPermanentModifier diverge from this only in whether they replace; here a
// single add path suffices for finalizeSpawn (which gates on hasModifier first, so it never
// double-adds).
func (i *AttributeInstance) addModifier(m AttributeModifier) {
	if _, exists := i.modifierByID[m.ID]; exists {
		panic("attribute: modifier is already applied on this attribute")
	}
	i.putModifier(m)
}

// AddPermanentModifier is the port of AttributeInstance.addPermanentModifier(AttributeModifier).
// Vanilla also records it in the permanentModifiers map (so it survives a save/reset); the value
// fold is identical to a transient modifier, so for the gameplay calculation the permanent vs
// transient distinction only matters for persistence (a cited concern: when entity NBT save/load is
// wired, the permanent set is what serializes). finalizeSpawn adds the random-spawn bonus as a
// PERMANENT modifier — recorded here so a future pack()/save serializes it.
func (i *AttributeInstance) AddPermanentModifier(m AttributeModifier) {
	if _, exists := i.modifierByID[m.ID]; exists {
		panic("attribute: modifier is already applied on this attribute")
	}
	i.putModifier(m)
	i.permanent = append(i.permanent, m.ID)
}

// AddTransientModifier is the port of AttributeInstance.addTransientModifier(AttributeModifier):
// a modifier that is NOT persisted (item/effect modifiers in vanilla). Delegates to addModifier.
func (i *AttributeInstance) AddTransientModifier(m AttributeModifier) { i.addModifier(m) }

// RemoveModifier is the port of AttributeInstance.removeModifier(Identifier): detach the modifier with
// this id from both modifierById and its per-operation bucket (a no-op if absent). Used when a mob
// effect that applied an attribute modifier (slowness → MOVEMENT_SPEED, weakness → ATTACK_DAMAGE)
// expires or is removed (MobEffect.removeAttributeModifiers). Cite AttributeInstance.removeModifier.
func (i *AttributeInstance) RemoveModifier(id string) {
	m, ok := i.modifierByID[id]
	if !ok {
		return
	}
	delete(i.modifierByID, id)
	if bucket := i.modifiersByOperation[m.Operation]; bucket != nil {
		delete(bucket, id)
	}
	for idx, pid := range i.permanent {
		if pid == id {
			i.permanent = append(i.permanent[:idx], i.permanent[idx+1:]...)
			break
		}
	}
}

// putModifier files a modifier into modifierById + the per-operation bucket (the shared tail of
// addModifier/addOrUpdateTransientModifier/addPermanentModifier).
func (i *AttributeInstance) putModifier(m AttributeModifier) {
	i.modifierByID[m.ID] = m
	bucket := i.modifiersByOperation[m.Operation]
	if bucket == nil {
		bucket = make(map[string]AttributeModifier)
		i.modifiersByOperation[m.Operation] = bucket
	}
	bucket[m.ID] = m
}

// getModifiersOrEmpty is the port of AttributeInstance.getModifiersOrEmpty(Operation): the
// collection of modifiers for an operation (an empty collection if none). calculateValue iterates
// it per operation.
func (i *AttributeInstance) getModifiersOrEmpty(op Operation) map[string]AttributeModifier {
	return i.modifiersByOperation[op]
}

// Value is the port of AttributeInstance.getValue(): the current attribute value after the modifier
// fold. Vanilla caches behind a dirty flag (getValue recomputes via calculateValue only when
// dirty); the cached path always returns the same number calculateValue would, so we compute it
// directly — observably identical, and trivially correct under single-owner tick ownership.
func (i *AttributeInstance) Value() float64 { return i.calculateValue() }

// calculateValue is the port of AttributeInstance.calculateValue() — the HEART of the subsystem.
//
// Faithful bytecode trace (AttributeInstance.calculateValue, verified this session):
//
//	double d1 = getBaseValue();                                  // 0..4
//	for (AttributeModifier m : getModifiersOrEmpty(ADD_VALUE))   // 5..48
//	    d1 += m.amount();                                        //   dadd
//	double d3 = d1;                                              // 49..50  (FREEZE d1)
//	for (AttributeModifier m : getModifiersOrEmpty(ADD_MULTIPLIED_BASE))   // 51..99
//	    d3 += d1 * m.amount();                                   //   dmul, dadd  (uses FROZEN d1)
//	for (AttributeModifier m : getModifiersOrEmpty(ADD_MULTIPLIED_TOTAL))  // 100..148
//	    d3 *= 1.0 + m.amount();                                  //   dconst_1 dadd, dmul
//	return attribute.value().sanitizeValue(d3);                 // 149..165
//
// The load-bearing subtlety (and the one most ports get wrong): the ADD_MULTIPLIED_BASE pass
// multiplies by `d1` — the base FROZEN immediately after the ADD_VALUE pass (local var d3 starts as
// a COPY of d1; the multiplier is d1, NOT the running d3). So two ADD_MULTIPLIED_BASE modifiers each
// see the SAME d1, not a compounding total. Only ADD_MULTIPLIED_TOTAL compounds (it scales the
// running d3). We mirror this exactly: `d1` is captured once, `d3` accumulates.
func (i *AttributeInstance) calculateValue() float64 {
	// double d1 = getBaseValue();
	d1 := i.baseValue

	// ADD_VALUE pass: d1 += m.amount() for each.
	for _, m := range i.getModifiersOrEmpty(AddValue) {
		d1 += m.Amount
	}

	// double d3 = d1; — freeze the post-ADD_VALUE base into d1 (it is no longer mutated) and start
	// the total d3 as a copy.
	d3 := d1

	// ADD_MULTIPLIED_BASE pass: d3 += d1 * m.amount() — d1 is the FROZEN base, NOT d3.
	for _, m := range i.getModifiersOrEmpty(AddMultipliedBase) {
		d3 += d1 * m.Amount
	}

	// ADD_MULTIPLIED_TOTAL pass: d3 *= 1.0 + m.amount() — compounds the running total.
	for _, m := range i.getModifiersOrEmpty(AddMultipliedTotal) {
		d3 *= 1.0 + m.Amount
	}

	// return attribute.sanitizeValue(d3);
	return i.attribute.sanitizeValue(d3)
}
