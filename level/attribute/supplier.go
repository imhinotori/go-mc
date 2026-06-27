package attribute

// supplier.go — the port of net.minecraft.world.entity.ai.attributes.AttributeSupplier (+ its
// Builder) and AttributeMap: the per-entity instance set backed by an entity-type default supplier.
//
// Verified against AttributeSupplier / AttributeSupplier$Builder / AttributeMap bytecode (javap -c
// -p, temp/cache/26.2-inner.jar) this session.

// Supplier is the port of net.minecraft.world.entity.ai.attributes.AttributeSupplier: the IMMUTABLE
// per-entity-type default attribute set (the value DefaultAttributes registers for each EntityType).
// It holds a template AttributeInstance per attribute (base value already set), and an AttributeMap
// reads through it for any attribute the entity has not locally diverged. Built via Builder so the
// base-value overrides (createAttributes' `.add(holder, value)`) are applied exactly once.
type Supplier struct {
	// instances maps an attribute name -> the template instance for that attribute (its base value
	// already set by the builder). The Map's value fold is the supplier default when the
	// AttributeMap has no local instance.
	instances map[string]*AttributeInstance
}

// Builder is the port of AttributeSupplier$Builder: accumulates attribute instances (with optional
// base-value overrides) and freezes them into a Supplier. Mirrors the fluent `.add(holder)` /
// `.add(holder, value)` chain the per-entity createAttributes methods use.
type Builder struct {
	instances map[string]*AttributeInstance
}

// NewBuilder is the port of AttributeSupplier.builder(): a fresh empty Builder.
func NewBuilder() *Builder {
	return &Builder{instances: make(map[string]*AttributeInstance)}
}

// Add is the port of AttributeSupplier$Builder.add(Holder<Attribute>): registers the attribute with
// its REGISTRATION DEFAULT as the base (the AttributeInstance ctor seeds baseValue from
// attribute.getDefaultValue(); the no-value `add` does not call setBaseValue, so the default
// stands). Returns the Builder for chaining.
func (b *Builder) Add(a *Attribute) *Builder {
	b.instances[a.Name()] = newAttributeInstance(a)
	return b
}

// AddValue is the port of AttributeSupplier$Builder.add(Holder<Attribute>, double): registers the
// attribute and OVERRIDES its base with value (create(holder) then instance.setBaseValue(value)).
// Named AddValue (not Add) because Go has no overloading; the two `add` overloads become Add /
// AddValue. Returns the Builder for chaining.
func (b *Builder) AddValue(a *Attribute, value float64) *Builder {
	inst := newAttributeInstance(a)
	inst.SetBaseValue(value)
	b.instances[a.Name()] = inst
	return b
}

// Build is the port of AttributeSupplier$Builder.build(): freezes the accumulated instances into an
// immutable Supplier (vanilla's buildKeepingLast — a later add of the same attribute wins, which the
// Go map assignment already mirrors).
func (b *Builder) Build() *Supplier {
	return &Supplier{instances: b.instances}
}

// HasAttribute is the port of AttributeSupplier.hasAttribute(Holder): whether the supplier registers
// this attribute name.
func (s *Supplier) HasAttribute(name string) bool {
	_, ok := s.instances[name]
	return ok
}

// supplierInstance returns the supplier's template instance for an attribute name (vanilla's
// getAttributeInstance, which throws when absent). Returns (nil, false) when the supplier does not
// register the attribute — the AttributeMap caller decides what an absent attribute means (vanilla
// AttributeSupplier.getValue throws; AttributeMap.getValue returns the supplier value only when the
// supplier HAS it). Kept total (no panic) so an out-of-set read is a defined 0 rather than a crash,
// matching AttributeMap.getValue's behavior for an entity that simply lacks the attribute.
func (s *Supplier) supplierInstance(name string) (*AttributeInstance, bool) {
	inst, ok := s.instances[name]
	return inst, ok
}

// Map is the port of net.minecraft.world.entity.ai.attributes.AttributeMap: the per-ENTITY attribute
// set. It lazily materializes a per-entity AttributeInstance (copied from the supplier template) the
// first time an attribute is mutated (getInstance), and reads through the supplier default otherwise
// (getValue/getBaseValue fall back to the supplier when no local instance exists). This is the exact
// vanilla two-tier model: the supplier is the shared immutable default, the map is the entity's local
// divergence.
//
// SINGLE-OWNER (TICK-05): a Map is per-entity tick-owned state; not goroutine-safe by itself.
type Map struct {
	// supplier is the entity-type default (DefaultAttributes.getSupplier(type)) every read falls
	// back to.
	supplier *Supplier
	// attributes is the entity's LOCAL instances (vanilla's `attributes` map), materialized lazily
	// on first mutation (GetInstance). An attribute absent here reads from the supplier.
	attributes map[string]*AttributeInstance
}

// NewMap is the port of `new AttributeMap(AttributeSupplier)`: a map backed by the given supplier,
// with no local instances yet (every read resolves to the supplier default until an attribute is
// locally mutated).
func NewMap(supplier *Supplier) *Map {
	return &Map{
		supplier:   supplier,
		attributes: make(map[string]*AttributeInstance),
	}
}

// GetInstance is the port of AttributeMap.getInstance(Holder): the entity's LOCAL AttributeInstance
// for an attribute, materialized on first call by COPYING the supplier template (vanilla's
// computeIfAbsent(holder, this::createInstance) -> AttributeSupplier.createInstance, which clones the
// template's base + modifiers via replaceFrom). Returns nil when the supplier does not register the
// attribute (vanilla createInstance returns null in that case) — the caller must not add modifiers to
// an attribute the entity does not have.
func (m *Map) GetInstance(name string) *AttributeInstance {
	if inst, ok := m.attributes[name]; ok {
		return inst
	}
	template, ok := m.supplier.supplierInstance(name)
	if !ok {
		// AttributeSupplier.createInstance returns null for an unregistered attribute; the Map's
		// computeIfAbsent would then store null. We return nil and do NOT cache, so a later
		// supplier-backed read still resolves cleanly.
		return nil
	}
	// createInstance: new AttributeInstance(holder, onDirty) then replaceFrom(template) — clone the
	// template's base value (and its modifiers, if any; supplier templates carry none beyond the
	// base, but we copy faithfully).
	clone := newAttributeInstance(template.attribute)
	clone.baseValue = template.baseValue
	for _, mod := range template.modifierByID {
		clone.putModifier(mod)
	}
	m.attributes[name] = clone
	return clone
}

// HasAttribute is the port of AttributeMap.hasAttribute(Holder): whether the entity has the
// attribute locally OR via the supplier.
func (m *Map) HasAttribute(name string) bool {
	if _, ok := m.attributes[name]; ok {
		return true
	}
	return m.supplier.HasAttribute(name)
}

// GetValue is the port of AttributeMap.getValue(Holder):
//
//	AttributeInstance inst = attributes.get(holder);
//	return inst != null ? inst.getValue() : supplier.getValue(holder);
//
// i.e. the folded value of the local instance if the entity has diverged, else the supplier default
// (which is the template instance's folded value). For an attribute the entity does not have at all,
// the supplier read resolves to its registered default; a name neither local nor in the supplier
// returns 0.0 (the faithful "no such attribute" zero — vanilla would throw in
// AttributeSupplier.getValue, but the AttributeMap path only reaches it for an attribute the supplier
// HAS, so 0.0 is the safe total fallback).
func (m *Map) GetValue(name string) float64 {
	if inst, ok := m.attributes[name]; ok {
		return inst.Value()
	}
	if template, ok := m.supplier.supplierInstance(name); ok {
		return template.Value()
	}
	return 0.0
}

// GetBaseValue is the port of AttributeMap.getBaseValue(Holder): the BASE (pre-modifier) value of
// the local instance if present, else the supplier default base.
func (m *Map) GetBaseValue(name string) float64 {
	if inst, ok := m.attributes[name]; ok {
		return inst.BaseValue()
	}
	if template, ok := m.supplier.supplierInstance(name); ok {
		return template.BaseValue()
	}
	return 0.0
}
