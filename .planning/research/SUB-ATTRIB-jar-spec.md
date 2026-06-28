# SUB-ATTRIB — Attribute System 1:1 Bytecode Spec (Minecraft 26.2, protocol 776)

Source: unobfuscated `temp/cache/26.2-inner.jar`, read via
`"/c/Program Files/Zulu/zulu-25/bin/javap" -c -p -classpath D:/ender/temp/cache/26.2-inner.jar <FQCN>`.

This is a **literal port spec**. Every numeric constant below is copied verbatim from the bytecode (`ldc2_w` doubles shown exactly, no rounding). Mirror the call chain, the float/double casts (`i2f`, `d2f`, `f2d`, `d2i`), the RNG draw order, and the guard checks. The ONLY permitted deviation is concurrency/perf optimization that provably preserves identical observable behavior.

> **26.2 package reorg note** — several classes moved vs older MCP names:
> - `net.minecraft.world.entity.animal.feline.Cat`
> - `net.minecraft.world.entity.monster.zombie.Zombie`
> - `net.minecraft.world.entity.npc.villager.Villager`, `.AbstractVillager`
> - `Identifier` is the 26.2 name for the old `ResourceLocation`.
> - `EntityTypes` / `EntityType` both appear; the registry field class is `net.minecraft.world.entity.EntityType`.

---

## 1. AttributeInstance.calculateValue() — the modifier fold

FQCN: `net.minecraft.world.entity.ai.attributes.AttributeInstance`
Signature: `private double calculateValue()`

### Fields (constructor)
```
modifiersByOperation : EnumMap<Operation, Map<Identifier, AttributeModifier>>   // Maps.newEnumMap
modifierById         : Object2ObjectArrayMap<Identifier, AttributeModifier>
permanentModifiers   : Object2ObjectArrayMap<Identifier, AttributeModifier>
baseValue            : double = attribute.value().getDefaultValue()   // set in ctor
dirty                : boolean = true
cachedValue          : double
onDirty              : Consumer<AttributeInstance>
```

### Bytecode (verbatim, key opcodes)
```
 0: getBaseValue()                         -> d1 (dstore_1)        // d
 5..49:  for m in getModifiersOrEmpty(ADD_VALUE):
         38: dload_1; 41: m.amount(); 44: dadd; 45: dstore_1       // d1 += m.amount()
 49: dload_1; 50: dstore_3                                          // d2 = d1
 51..100: for m in getModifiersOrEmpty(ADD_MULTIPLIED_BASE):
         87: dload_3; 88: dload_1; 91: m.amount(); 94: dmul; 95: dadd; 96: dstore_3
                                                                   // d2 += d1 * m.amount()
100..149: for m in getModifiersOrEmpty(ADD_MULTIPLIED_TOTAL):
        136: dload_3; 137: dconst_1; 140: m.amount(); 143: dadd; 144: dmul; 145: dstore_3
                                                                   // d2 = d2 * (1.0 + m.amount())
149..165: return attribute.value().sanitizeValue(d2)
```

### Exact formula
```
d1 = baseValue
for m in ADD_VALUE:            d1 += m.amount
d2 = d1
for m in ADD_MULTIPLIED_BASE:  d2 += d1 * m.amount        // note: d1 is the FROZEN post-ADD_VALUE base
for m in ADD_MULTIPLIED_TOTAL: d2 *= (1.0 + m.amount)
return sanitizeValue(d2)
```
Order is fixed by enum iteration via `getModifiersOrEmpty(op)` calls in the literal sequence ADD_VALUE → ADD_MULTIPLIED_BASE → ADD_MULTIPLIED_TOTAL. All math is `double`. `d1` (the ADD_MULTIPLIED_BASE multiplier) is captured ONCE after ADD_VALUE and reused for every ADD_MULTIPLIED_BASE modifier — it does NOT update as d2 grows.

### getModifiersOrEmpty
```
return modifiersByOperation.getOrDefault(op, Map.of()).values();
```
Iteration order = insertion order of the inner per-operation map. For 1:1 parity within a single operation bucket, modifiers are summed/multiplied commutatively except ADD_MULTIPLIED_TOTAL which is `*=` repeatedly — multiplication is commutative in exact double terms regardless of order, but ADD_MULTIPLIED_BASE is `+=` so also order-independent. Order only matters across operations, which is fixed. **Use an insertion-ordered map per bucket to be safe.**

### getValue (cache wrapper)
```
public double getValue() {
    if (dirty) { cachedValue = calculateValue(); dirty = false; }
    return cachedValue;
}
```
`setDirty()` sets `dirty=true` and calls `onDirty.accept(this)`. `setBaseValue(d)` is a no-op if `d == baseValue` (dcmpl/ifne), else sets and marks dirty.

### Pseudo-Go
```go
func (ai *AttributeInstance) calculateValue() float64 {
    d1 := ai.getBaseValue()
    for _, m := range ai.modifiersOrEmpty(OpAddValue) {
        d1 += m.Amount
    }
    d2 := d1
    for _, m := range ai.modifiersOrEmpty(OpAddMultipliedBase) {
        d2 += d1 * m.Amount
    }
    for _, m := range ai.modifiersOrEmpty(OpAddMultipliedTotal) {
        d2 = d2 * (1.0 + m.Amount)
    }
    return ai.attribute.Value().SanitizeValue(d2) // virtual dispatch: RangedAttribute clamps
}
```

### Other AttributeInstance methods (port faithfully)
- `addModifier(m)` (private): `modifierById.putIfAbsent(m.id(), m)`; if the returned old value is non-null → throw `IllegalArgumentException("Modifier is already applied on this attribute!")`. Then `getModifiers(m.operation()).put(m.id(), m)` and `setDirty()`.
- `addTransientModifier(m)` → calls `addModifier(m)` (throws on dup).
- `addOrUpdateTransientModifier(m)`: `modifierById.put(...)` returns old; if `old == m` (reference equal, `if_acmpne`) return without dirtying; else put into operation map + `setDirty()`. (Note: does NOT update permanentModifiers.)
- `addPermanentModifier(m)`: `addModifier(m)` then `permanentModifiers.put(m.id(), m)`.
- `addOrReplacePermanentModifier(m)`: `removeModifier(m.id())` then `addModifier(m)` then `permanentModifiers.put(...)`.
- `removeModifier(Identifier id)` returns bool: remove from modifierById; if null → return false; else remove from its operation map + permanentModifiers, `setDirty()`, return true.
- `replaceFrom(other)`: copy baseValue; clear+putAll modifierById, permanentModifiers; clear modifiersByOperation, then rebuild via `other.modifiersByOperation.forEach((op,map) -> this.getModifiers(op).putAll(map))`; `setDirty()`. (Used by AttributeSupplier.createInstance.)

---

## 2. AttributeModifier and Operation enum

FQCN: `net.minecraft.world.entity.ai.attributes.AttributeModifier` (a Java `record`)
```
record AttributeModifier(Identifier id, double amount, Operation operation)
```
- `is(Identifier id)` → `id.equals(this.id)`.
- Codecs: `MAP_CODEC` / `CODEC` are RecordCodecBuilder over fields `"id"` (Identifier.CODEC), `"amount"` (Codec.DOUBLE), `"operation"` (Operation.CODEC). `STREAM_CODEC` = composite(Identifier.STREAM_CODEC, ByteBufCodecs.DOUBLE, Operation.STREAM_CODEC). This is the on-disk/on-wire format of a single modifier.

### Operation enum
FQCN: `net.minecraft.world.entity.ai.attributes.AttributeModifier$Operation`
Implements `StringRepresentable`. Three constants, in declaration order with their `id` ints and serialized names:

| Java constant         | ordinal | `id()` | serialized name (`getSerializedName`) | role in calculateValue |
|-----------------------|---------|--------|----------------------------------------|------------------------|
| `ADD_VALUE`           | 0       | 0      | `add_value`                            | `d1 += amount` (flat add to base) |
| `ADD_MULTIPLIED_BASE` | 1       | 1      | `add_multiplied_base`                  | `d2 += d1 * amount` (d1 = frozen post-ADD_VALUE base) |
| `ADD_MULTIPLIED_TOTAL`| 2       | 2      | `add_multiplied_total`                 | `d2 *= (1.0 + amount)` |

- Constructor stores `(String name, int id)` where `name` is the lowercase serialized form and `id` is the numeric id.
- `BY_ID = ByIdMap.continuous(Operation::id, values(), OutOfBoundsStrategy.ZERO)` → out-of-range network ids clamp to `ADD_VALUE` (id 0).
- `STREAM_CODEC = ByteBufCodecs.idMapper(BY_ID, Operation::id)` → wire form is the **int id** (a VarInt-encoded enum index, value 0/1/2), NOT a string.
- `CODEC = StringRepresentable.fromEnum(...)` → NBT/JSON form is the serialized **string** name.

### Pseudo-Go
```go
type Operation uint8
const (
    OpAddValue          Operation = 0 // "add_value"
    OpAddMultipliedBase Operation = 1 // "add_multiplied_base"
    OpAddMultipliedTotal Operation = 2 // "add_multiplied_total"
)
// network read: id<0 || id>2 -> OpAddValue
```

---

## 3. AttributeMap

FQCN: `net.minecraft.world.entity.ai.attributes.AttributeMap`
```
attributes        : Object2ObjectOpenHashMap<Holder<Attribute>, AttributeInstance>
attributesToSync  : ObjectOpenHashSet<AttributeInstance>
attributesToUpdate: ObjectOpenHashSet<AttributeInstance>
supplier          : AttributeSupplier
ctor(AttributeSupplier supplier)
```

### getInstance(Holder<Attribute>)
```
return attributes.computeIfAbsent(holder, h -> supplier.createInstance(this::onAttributeModified, h));
```
- `createInstance` (on supplier) returns null if the supplier has no default for that attribute → `computeIfAbsent` would store null. **In practice only called for attributes the supplier defines.** If null, `getInstance` returns null (callers null-check).
- `onAttributeModified(ai)` (the `onDirty` consumer): `attributesToUpdate.add(ai)`; if `ai.getAttribute().value().isClientSyncable()` → `attributesToSync.add(ai)`.

### getValue(Holder) — the hot read path
```
AttributeInstance ai = attributes.get(holder);    // raw get, NO computeIfAbsent
return ai != null ? ai.getValue() : supplier.getValue(holder);
```
So an attribute that has never had a modifier/baseValue change is NOT instantiated; its value comes straight from the supplier's frozen template instance. `getBaseValue(holder)` and `getModifierValue(holder,id)` follow the same `ai != null ? ai.x() : supplier.x()` pattern.

### hasAttribute(holder)
```
return attributes.get(holder) != null || supplier.hasAttribute(holder);
```

### Other methods
- `addTransientAttributeModifiers(Multimap)`: for each (holder, modifier) → `getInstance(holder)?.addOrUpdateTransientModifier(modifier)`.
- `removeAttributeModifiers(Multimap)`: for each holder, for each modifier → `attributes.get(holder)?.removeModifier(modifier.id())`.
- `assignAllValues(other)`: for each AttributeInstance in other.attributes → `getInstance(holder).replaceFrom(otherInstance)`.
- `assignBaseValues(other)`: copy base values only.
- `assignPermanentModifiers(other)`: `getInstance(holder).addPermanentModifiers(otherInstance.getPermanentModifiers())`.
- `resetBaseValue(holder)`: if `!supplier.hasAttribute(holder)` return false; else `attributes.get(holder)?.setBaseValue(supplier.getBaseValue(holder))`, return true.
- `pack()` / `apply(List)` → §8.

### Construction per entity type
`AttributeMap` is built in `LivingEntity` constructor (verified at LivingEntity bytecode offset ~135):
```
this.attributes = new AttributeMap( DefaultAttributes.getSupplier(entityType) );
```
`DefaultAttributes.getSupplier(EntityType)` → `SUPPLIERS.get(type)` (a `Map<EntityType<? extends LivingEntity>, AttributeSupplier>`). See §5.

### Pseudo-Go
```go
func (m *AttributeMap) GetValue(h Holder) float64 {
    if ai := m.attributes[h]; ai != nil { return ai.GetValue() }
    return m.supplier.GetValue(h)
}
func (m *AttributeMap) GetInstance(h Holder) *AttributeInstance {
    if ai, ok := m.attributes[h]; ok { return ai }
    ai := m.supplier.CreateInstance(m.onAttributeModified, h) // may be nil
    if ai != nil { m.attributes[h] = ai }
    return ai
}
```

---

## 4. Attribute vs RangedAttribute

### Attribute (base)
FQCN: `net.minecraft.world.entity.ai.attributes.Attribute`
```
defaultValue : double (final)
syncable     : boolean
descriptionId: String
sentiment    : Attribute$Sentiment = POSITIVE   // ctor default
ctor(String descriptionId, double defaultValue)
```
- `getDefaultValue()` → `defaultValue`.
- `sanitizeValue(double v)` → **returns v unchanged** (no clamp). This is the base; subclasses override.
- `setSyncable(bool)` / `setSentiment(...)` are fluent setters returning `this`.

### RangedAttribute
FQCN: `net.minecraft.world.entity.ai.attributes.RangedAttribute extends Attribute`
```
minValue : double (final)
maxValue : double (final)
ctor(String descriptionId, double defaultValue, double minValue, double maxValue)
   // ctor validation (throws IllegalArgumentException):
   //   if (minValue > maxValue) "Minimum value cannot be bigger than maximum value!"
   //   if (defaultValue < minValue) "Default value cannot be lower than minimum value!"
   //   if (defaultValue > maxValue) "Default value cannot be bigger than maximum value!"
```

#### sanitizeValue (verbatim)
```
public double sanitizeValue(double v) {
    if (Double.isNaN(v)) return this.minValue;   // NaN -> min
    return Mth.clamp(v, this.minValue, this.maxValue);
}
```
`Mth.clamp(d, min, max)` = `d < min ? min : (d > max ? max : d)`. Port `Mth.clamp` exactly; note NaN short-circuits to `minValue` BEFORE the clamp.

### Pseudo-Go
```go
type Attribute struct { defaultValue float64; syncable bool; descriptionId string; sentiment Sentiment }
func (a *Attribute) SanitizeValue(v float64) float64 { return v }  // base: identity

type RangedAttribute struct { Attribute; minValue, maxValue float64 }
func (r *RangedAttribute) SanitizeValue(v float64) float64 {
    if math.IsNaN(v) { return r.minValue }
    return mthClamp(v, r.minValue, r.maxValue)
}
```

---

## 5. DefaultAttributes & per-entity suppliers

FQCN: `net.minecraft.world.entity.ai.attributes.DefaultAttributes`
- `SUPPLIERS : Map<EntityType<? extends LivingEntity>, AttributeSupplier>` — a static ImmutableMap built in `<clinit>` (one `.put(EntityType, SomeClass.createAttributes().build())` per living type). The per-type **values** are the `createAttributes()` builders below; the registry wiring itself is data we did not fully extract, but each entity's `createAttributes()` is fully decoded.
- `getSupplier(type)` → `SUPPLIERS.get(type)`.
- `hasSupplier(type)` → `containsKey`.
- `validate()` logs any LivingEntity type missing a supplier.

### AttributeSupplier
FQCN: `net.minecraft.world.entity.ai.attributes.AttributeSupplier`
```
instances : Map<Holder<Attribute>, AttributeInstance>   // immutable, from Builder
```
- `getValue(holder)` / `getBaseValue(holder)` / `getModifierValue(holder,id)` look up via `getAttributeInstance(holder)` which throws `IllegalArgumentException` if the attribute is not present ("Can't find attribute %s").
- `createInstance(Consumer onDirty, Holder)`: `inst = instances.get(holder)`; if null return null; else `new AttributeInstance(holder, onDirty)` then `.replaceFrom(inst)` (copies baseValue + modifiers from the template) and return it.
- `hasAttribute(holder)` → `instances.containsKey`.

### AttributeSupplier.Builder
FQCN: `net.minecraft.world.entity.ai.attributes.AttributeSupplier$Builder`
```
builder : ImmutableMap.Builder<Holder<Attribute>, AttributeInstance>
instanceFrozen : boolean
private create(holder): new AttributeInstance(holder, onDirty=guard) ; builder.put(holder, ai); return ai
   // onDirty guard throws UnsupportedOperationException if instanceFrozen (template must not mutate post-build)
add(holder)        -> create(holder)            // baseValue stays = attribute default
add(holder, double)-> create(holder).setBaseValue(double)
build()            -> instanceFrozen = true; new AttributeSupplier(builder.buildKeepingLast())
```
`buildKeepingLast()` → if the same holder is added twice, the LAST value wins. This matters: subclasses call `super.createXAttributes()` then `.add(SAME_ATTR, newVal)` to override (e.g. Witch overrides MAX_HEALTH from default 20 to 26).

### Attribute default/min/max table (from `Attributes.<clinit>`, verbatim doubles)
Registered via `RangedAttribute(descId, default, min, max)`; identifiers shown by their registry string.

| attribute (id)              | default | min       | max     | syncable | sentiment |
|-----------------------------|---------|-----------|---------|----------|-----------|
| `max_health`                | 20.0    | 1.0       | 1024.0  | yes      | POSITIVE  |
| `movement_speed`            | 0.7     | 0.0       | 1024.0  | yes      | POSITIVE  |
| `attack_damage`             | 2.0     | 0.0       | 2048.0  | no       | POSITIVE  |
| `attack_speed`              | 4.0     | 0.0       | 1024.0  | yes      | POSITIVE  |
| `attack_knockback`          | 0.0     | 0.0       | 5.0     | no       | POSITIVE  |
| `armor`                     | 0.0     | 0.0       | 30.0    | yes      | POSITIVE  |
| `armor_toughness`           | 0.0     | 0.0       | 20.0    | yes      | POSITIVE  |
| `knockback_resistance`      | 0.0     | -2.0      | 1.0     | no       | POSITIVE  |
| `follow_range`              | 32.0    | 0.0       | 2048.0  | no       | POSITIVE  |
| `max_absorption`            | 0.0     | 0.0       | 2048.0  | yes      | POSITIVE  |
| `safe_fall_distance`        | 3.0     | -1024.0   | 1024.0  | yes      | POSITIVE  |
| `fall_damage_multiplier`    | 1.0     | 0.0       | 100.0   | yes      | NEGATIVE  |
| `spawn_reinforcements`      | 0.0     | 0.0       | 1.0     | no       | POSITIVE  |
| `air_drag_modifier`         | 1.0     | 0.0       | 2048.0  | yes      | POSITIVE  |
| `flying_speed`              | 0.4     | 0.0       | 1024.0  | yes      | POSITIVE  |
| `luck`                      | 0.0     | -1024.0   | 1024.0  | yes      | POSITIVE  |
| `mining_efficiency`         | 0.0     | 0.0       | 1024.0  | yes      | POSITIVE  |
| `movement_efficiency`       | 0.0     | 0.0       | 1.0     | yes      | POSITIVE  |
| `scale`                     | 1.0     | 0.0625    | 16.0    | yes      | NEUTRAL   |
| `sneaking_speed`            | 0.3     | 0.0       | 1.0     | yes      | POSITIVE  |
| `friction_modifier`         | 1.0     | 0.0       | (…)     | (…)      | (…)       |
| `temptation_range` (`tempt_range`) | 10.0 | (…)   | (…)     | (…)      | (…)       |

> Values not in scope (gravity, step_height, jump_strength, oxygen_bonus, burning_time, etc.) exist in the same `<clinit>` and follow the same `RangedAttribute(default,min,max)` pattern; extract them the same way when needed. Items marked `(…)` were truncated in this pass — re-dump `Attributes` `<clinit>` if required. **The 6 attributes the SUB-ATTRIB mobs actually use (max_health, movement_speed, attack_damage, armor, armor_toughness, follow_range, knockback_resistance, attack_knockback, spawn_reinforcements, safe_fall_distance, fall_damage_multiplier) are all exact above.**

### Builder chains (createAttributes), verbatim base values

**LivingEntity.createLivingAttributes()** — the root. Adds (default base unless noted):
`MAX_HEALTH, KNOCKBACK_RESISTANCE, MOVEMENT_SPEED, ARMOR, ARMOR_TOUGHNESS, MAX_ABSORPTION, STEP_HEIGHT, SCALE, GRAVITY, SAFE_FALL_DISTANCE, FALL_DAMAGE_MULTIPLIER, JUMP_STRENGTH, ENTITY_INTERACTION_RANGE, OXYGEN_BONUS, BURNING_TIME, EXPLOSION_KNOCKBACK_RESISTANCE, WATER_MOVEMENT_EFFICIENCY, MOVEMENT_EFFICIENCY, ATTACK_KNOCKBACK, CAMERA_DISTANCE, WAYPOINT_TRANSMIT_RANGE, BOUNCINESS, AIR_DRAG_MODIFIER, FRICTION_MODIFIER, NAME_TAG_DISTANCE, BELOW_NAME_DISTANCE` — all via no-arg `add(holder)` (base = attribute default). None get an explicit base value here.

**Mob.createMobAttributes()** = `createLivingAttributes()` then `.add(FOLLOW_RANGE, 16.0)`.
> Note: FOLLOW_RANGE attribute default is 32.0, but Mob overrides base to **16.0**.

**Animal.createAnimalAttributes()** = `createMobAttributes()` then `.add(TEMPT_RANGE, 10.0)`.

**Monster.createMonsterAttributes()** = `createMobAttributes()` then `.add(ATTACK_DAMAGE)` (no-arg → base = default 2.0).

**Player.createAttributes()** = `createLivingAttributes()` then:
```
.add(ATTACK_DAMAGE, 1.0)
.add(MOVEMENT_SPEED, 0.10000000149011612)   // exact ldc2_w
.add(ATTACK_SPEED)                            // no-arg, base = default 4.0
.add(LUCK)
.add(BLOCK_INTERACTION_RANGE)
.add(BLOCK_BREAK_SPEED)
.add(SUBMERGED_MINING_SPEED)
.add(SNEAKING_SPEED)
.add(MINING_EFFICIENCY)
.add(SWEEPING_DAMAGE_RATIO)
.add(WAYPOINT_TRANSMIT_RANGE, 6.0E7)
.add(WAYPOINT_RECEIVE_RANGE, 6.0E7)
```
(Player does NOT add FOLLOW_RANGE — it extends LivingEntity/Avatar directly, not Mob.)

**Witch.createAttributes()** = `Monster.createMonsterAttributes()` then:
```
.add(MAX_HEALTH, 26.0)
.add(MOVEMENT_SPEED, 0.25)
```
Result: MAX_HEALTH base=26.0 (overrides 20.0), MOVEMENT_SPEED base=0.25 (overrides default), FOLLOW_RANGE base=16.0 (from Mob), ATTACK_DAMAGE base=2.0 (from Monster), plus all LivingEntity defaults.

**Cat.createAttributes()** = `Animal.createAnimalAttributes()` then:
```
.add(MAX_HEALTH, 10.0)
.add(MOVEMENT_SPEED, 0.30000001192092896)   // exact ldc2_w
.add(ATTACK_DAMAGE, 3.0)
```
(Cat extends TamableAnimal → Animal → AgeableMob → ... → Mob, so it has FOLLOW_RANGE=16.0 and TEMPT_RANGE=10.0.)

**Villager.createAttributes()** = `Mob.createMobAttributes()` then:
```
.add(MOVEMENT_SPEED, 0.5)
```
(Villager: MAX_HEALTH=20.0 default, FOLLOW_RANGE=16.0, MOVEMENT_SPEED=0.5. No ATTACK_DAMAGE added — villagers have no attack damage attribute.)

**Zombie.createAttributes()** = `Monster.createMonsterAttributes()` then:
```
.add(FOLLOW_RANGE, 35.0)
.add(MOVEMENT_SPEED, 0.23000000417232513)   // exact ldc2_w
.add(ATTACK_DAMAGE, 3.0)
.add(ARMOR, 2.0)
.add(SPAWN_REINFORCEMENTS_CHANCE)            // no-arg, base = default 0.0
```
Result: MAX_HEALTH=20.0, FOLLOW_RANGE=35.0 (overrides Mob's 16.0), MOVEMENT_SPEED=0.23000000417232513, ATTACK_DAMAGE=3.0 (overrides Monster's 2.0), ARMOR=2.0, KNOCKBACK_RESISTANCE=0.0, SPAWN_REINFORCEMENTS=0.0.

**Silverfish.createAttributes()** = `Monster.createMonsterAttributes()` then:
```
.add(MAX_HEALTH, 8.0)
.add(MOVEMENT_SPEED, 0.25)
.add(ATTACK_DAMAGE, 1.0)
```
Result: MAX_HEALTH=8.0, MOVEMENT_SPEED=0.25, ATTACK_DAMAGE=1.0 (overrides Monster's 2.0), FOLLOW_RANGE=16.0.

> Holder field references (`Attributes.X`) resolve to the registered `Holder<Attribute>` from `Attributes.<clinit>`. In Go, key these suppliers by the same registry identifier strings.

---

## 6. Mob.finalizeSpawn — RNG draw order (CRITICAL)

FQCN: `net.minecraft.world.entity.Mob`
Signature: `public SpawnGroupData finalizeSpawn(ServerLevelAccessor level, DifficultyInstance difficulty, EntitySpawnReason reason, SpawnGroupData data)`

### Bytecode (verbatim)
```
 0: RandomSource random = level.getRandom();                       // local 5
 8: AttributeInstance ai = Objects.requireNonNull(this.getAttribute(Attributes.FOLLOW_RANGE));  // local 6
23: if (!ai.hasModifier(RANDOM_SPAWN_BONUS_ID)) {
       ai.addPermanentModifier(new AttributeModifier(
           RANDOM_SPAWN_BONUS_ID,
           random.triangle(0.0, 0.11485000000000001),               // DRAW #1
           Operation.ADD_MULTIPLIED_BASE));
    }
63: this.setLeftHanded( random.nextFloat() < 0.05f );               // DRAW #2  (float 0.05f)
86: return data;
```

### Exact draw order (base Mob.finalizeSpawn)
1. `random.triangle(0.0, 0.11485000000000001)` — ONLY drawn if FOLLOW_RANGE has no existing `RANDOM_SPAWN_BONUS_ID` modifier. Applied as a permanent `ADD_MULTIPLIED_BASE` modifier on FOLLOW_RANGE.
2. `random.nextFloat()` — compared `< 0.05f` to set left-handed.

`RandomSource.triangle(d, d2)` is vanilla `d + d2 * (nextDouble() - nextDouble())` → **internally draws nextDouble() twice** (in that order: first minuend, then subtrahend). Port `triangle` exactly: it consumes two `nextDouble()` calls.

> So for a NATURAL spawn with no pre-existing modifier, base Mob draws: `nextDouble(), nextDouble()` (inside triangle), then `nextFloat()`. Subclasses prepend/append their own draws by calling `super.finalizeSpawn(...)` at a specific point — order is determined by where in the override the super call sits.

### Witch (no own override)
`Witch` → `Raider.finalizeSpawn` → `PatrollingMonster.finalizeSpawn` → ... → `Mob.finalizeSpawn`.
**Raider.finalizeSpawn** (verbatim):
```
 0: this.setCanJoinRaid( !(this.is(EntityTypes.WITCH) && reason == EntitySpawnReason.NATURAL) );
       // i.e. canJoinRaid = false ONLY when it's a Witch AND natural spawn; true otherwise
26: return PatrollingMonster.finalizeSpawn(level, difficulty, reason, data);   // no RNG draw here
```
`setCanJoinRaid` consumes NO RNG. PatrollingMonster.finalizeSpawn (not separately dumped here) chains to Mob.finalizeSpawn — verify it adds no draws before Mob if exact RNG-stream parity for witches is required. For the Witch the observable attribute effect is: FOLLOW_RANGE random spawn bonus (Mob draw #1) + left-handed (draw #2). Witch holds no items / no variant in finalizeSpawn.

### Cat
FQCN: `net.minecraft.world.entity.animal.feline.Cat`
```
 0: SpawnGroupData data = TamableAnimal.finalizeSpawn(level, difficulty, reason, data);
       // -> chains up to Mob.finalizeSpawn: draws triangle(2x nextDouble) + nextFloat
11: SpawnContext ctx = SpawnContext.create(level, this.blockPosition());
19: VariantUtils.selectVariantToSpawn(ctx, Registries.CAT_VARIANT)        // VARIANT SELECTION
       .ifPresent(this::setVariant);
34: this.setSoundVariant( CatSoundVariants.pickRandomSoundVariant(this.registryAccess(),
                                                                  level.getRandom()) );  // RNG
51: return data;
```
Draw order for Cat:
1. (via super) Mob's `triangle` → `nextDouble(), nextDouble()`, then `nextFloat()` (left-handed).
2. `VariantUtils.selectVariantToSpawn(ctx, CAT_VARIANT)` — variant selection. Its RNG usage depends on the variant registry/tag data (priority + weighted random among matching `cat_variant` entries by SpawnContext biome) — **NOT extracted here; depends on registry/tag data.** Document as a dependency: needs the `minecraft:cat_variant` registry entries + their spawn conditions to reproduce draw count/order.
3. `CatSoundVariants.pickRandomSoundVariant(registryAccess, random)` — picks a sound variant; RNG draw count depends on `cat_variant`/sound registry — **dependency, not extracted.**

### Villager
FQCN: `net.minecraft.world.entity.npc.villager.Villager`
```
 0: if (reason == EntitySpawnReason.BREEDING) {
       this.setVillagerData( this.getVillagerData()
            .withProfession(level.registryAccess(), VillagerProfession.NONE) );   // no RNG
    }
27: this.finalizeVillagerType(level, this.blockPosition());            // sets villager TYPE (biome-based)
36: if (reason == EntitySpawnReason.STRUCTURE) { this.assignProfessionWhenSpawned = true; }   // no RNG
48: return AbstractVillager.finalizeSpawn(level, difficulty, reason, data);  // chains to Mob
57: areturn
```
- `finalizeVillagerType(level, pos)` selects the villager **type** (plains/desert/etc.) from the biome at `pos` — biome-deterministic, **no RNG draw** (lookup by biome→type registry). Depends on `minecraft:villager_type` registry/biome tags — type mapping data dependency, but no random.
- Profession is NOT assigned here (only forced to NONE on breeding, or deferred via `assignProfessionWhenSpawned` flag for structure spawns). Profession assignment happens later in the brain tick (`customServerAiStep`), not in finalizeSpawn.
- The actual RNG draws for Villager come from `AbstractVillager.finalizeSpawn` → ... → `Mob.finalizeSpawn` (triangle + nextFloat). AbstractVillager.finalizeSpawn was not separately dumped — verify it adds no own draws before Mob.

### Pseudo-Go (base Mob)
```go
func (m *Mob) FinalizeSpawn(level ServerLevelAccessor, diff DifficultyInstance, reason SpawnReason, data SpawnGroupData) SpawnGroupData {
    rand := level.GetRandom()
    ai := mustNotNil(m.GetAttribute(FOLLOW_RANGE))
    if !ai.HasModifier(RANDOM_SPAWN_BONUS_ID) {
        amount := rand.Triangle(0.0, 0.11485000000000001) // consumes 2x nextDouble
        ai.AddPermanentModifier(AttributeModifier{RANDOM_SPAWN_BONUS_ID, amount, OpAddMultipliedBase})
    }
    m.SetLeftHanded(rand.NextFloat() < 0.05)
    return data
}
```

`RANDOM_SPAWN_BONUS_ID` is a fixed `Identifier` constant (registered static field `Mob.RANDOM_SPAWN_BONUS_ID`, conventionally `minecraft:random_spawn_bonus`). Confirm the exact string from `Mob.<clinit>` when wiring.

---

## 7. LivingEntity — combat / armor / fall consumers of attributes

### getAttributeValue (the read used everywhere)
```
public double getAttributeValue(Holder attr) { return this.getAttributes().getValue(attr); }
```

### getArmorValue
```
public int getArmorValue() { return Mth.floor( getAttributeValue(Attributes.ARMOR) ); }
```
`Mth.floor(double)` = `(int) Math.floor(d)` with the standard Mth fast-floor. Port exactly.

### getDamageAfterArmorAbsorb(DamageSource src, float damage)
```
protected float getDamageAfterArmorAbsorb(DamageSource src, float damage) {
    if (!src.is(DamageTypeTags.BYPASSES_ARMOR)) {
        this.hurtArmor(src, damage);
        damage = CombatRules.getDamageAfterAbsorb(
            this,
            damage,
            src,
            (float) this.getArmorValue(),                              // i2f
            (float) this.getAttributeValue(Attributes.ARMOR_TOUGHNESS) // d2f
        );
    }
    return damage;
}
```
Note casts: armor is `int`→`float` (`i2f`); toughness is `double`→`float` (`d2f`).

### CombatRules.getDamageAfterAbsorb (THE armor formula)
FQCN: `net.minecraft.world.damagesource.CombatRules`
Signature: `static float getDamageAfterAbsorb(LivingEntity entity, float damage, DamageSource src, float armor, float toughness)`
```
 0: float f  = 2.0f + toughness / 4.0f;                              // local 5
 9: float f2 = Mth.clamp( armor - damage / f,  armor * 0.2f,  20.0f ); // local 6
26: float f3 = f2 / 25.0f;                                            // local 7
33: ItemStack weapon = src.getWeaponItem();
39: float f4;
    if (weapon != null && entity.level() instanceof ServerLevel sl) {
        f4 = Mth.clamp(
               EnchantmentHelper.modifyArmorEffectiveness(sl, weapon, entity, src, f3),
               0.0f, 1.0f );                                          // local 8
    } else {
        f4 = f3;
    }
90: float f5 = 1.0f - f4;                                            // local 10
96: return damage * f5;
```
Exact constants: `2.0f`, `4.0f`, `0.2f`, `20.0f`, `25.0f`, clamp `0.0f..1.0f`. The intermediate `f2` clamp lower bound is `armor * 0.2f` (NOT a constant) and upper `20.0f`. `modifyArmorEffectiveness` is the enchant hook (armor-piercing); if no weapon or client-side, `f4 = f3 = f2/25`. Without enchantments this reduces to the classic:
```
reduction = clamp(armor - damage/(2 + toughness/4), armor*0.2, 20) / 25
result    = damage * (1 - reduction)
```

### getDamageAfterMagicAbsorb (enchant protection)
```
static float getDamageAfterMagicAbsorb(float damage, float protectionDiff) {
    float f = Mth.clamp(protectionDiff, 0.0f, 20.0f);
    return damage * (1.0f - f / 25.0f);
}
```

### Knockback (knockback_resistance consumer)
`LivingEntity.knockback(double strength, double dx, double dz, DamageSource, float, boolean)`:
```
 0: strength = strength * (1.0 - getAttributeValue(Attributes.KNOCKBACK_RESISTANCE));  // double
12: if (strength <= 0.0) return;   // dcmpg; ifgt -> else return
...   // builds Vec3(dx, 0, dz).normalize().scale(strength); deltaMovement adjust
   // degenerate-direction guard: while (dx*dx + dz*dz < 9.999999747378752E-6) {
   //    dx = (random.nextDouble() - random.nextDouble()) * 0.01;   // 2 draws
   //    dz = (random.nextDouble() - random.nextDouble()) * 0.01;   // 2 draws
   // }
   // new dm.x = oldDm.x/2.0 - knockVec.x ;  (and z), y = min(0.4, oldDm.y/2 + strength) [verify y branch]
```
`strength` reduced by `(1 - knockback_resistance)`. The epsilon `9.999999747378752E-6` (= float 1.0E-5 widened) gates a degenerate-direction RNG fixup that draws `nextDouble()` x4 in the order shown. Port the loop and constants exactly.

### Fall damage (safe_fall_distance + fall_damage_multiplier consumers)
`LivingEntity.causeFallDamage(double fallDistance, float multiplier, DamageSource)`:
```
if (isIgnoringFallDamageFromCurrentImpulse()) {
    d = Math.min(fallDistance, currentImpulseImpactPos.y - getY());
    if (d <= 0.0) resetCurrentImpulseContext(); else tryResetCurrentImpulseContext();
} else { d = fallDistance; }
boolean baseHurt = super.causeFallDamage(d, multiplier, src);    // Entity.causeFallDamage
int dmg = calculateFallDamage(d, multiplier);
if (dmg > 0) {
    resetCurrentImpulseContext();
    playSound(getFallDamageSound(dmg), 1.0f, 1.0f);
    playBlockFallSound();
    hurt(src, (float) dmg);
    return true;
}
return baseHurt;   // (continues past offset 116 for the dmg<=0 path)
```

`LivingEntity.calculateFallDamage(double fallDistance, float multiplier)`:
```
protected int calculateFallDamage(double fallDistance, float multiplier) {
    if (this.is(EntityTypeTags.FALL_DAMAGE_IMMUNE)) return 0;
    double power = calculateFallPower(fallDistance);                 // = fallDistance + 1.0E-6 - safe_fall_distance
    return Mth.floor( power * (double) multiplier
                            * getAttributeValue(Attributes.FALL_DAMAGE_MULTIPLIER) );
}
private double calculateFallPower(double fallDistance) {
    return fallDistance + 1.0E-6 - getAttributeValue(Attributes.SAFE_FALL_DISTANCE);
}
```
Exact: epsilon `1.0E-6` (added BEFORE subtracting safe_fall_distance), `multiplier` is `float`→`double` (`f2d`), final `Mth.floor` to int. So:
```
damage = floor( (fallDistance + 1e-6 - SAFE_FALL_DISTANCE) * multiplier * FALL_DAMAGE_MULTIPLIER )
```
Both `safe_fall_distance` (default 3.0) and `fall_damage_multiplier` (default 1.0) ARE real attributes in 26.2 (§5 table). FALL_DAMAGE_IMMUNE is an entity-type tag (data dependency).

### Pseudo-Go (armor formula)
```go
func getDamageAfterAbsorb(e *LivingEntity, damage float32, src DamageSource, armor, toughness float32) float32 {
    f := 2.0 + toughness/4.0
    f2 := mthClampF(armor-damage/f, armor*0.2, 20.0)
    f3 := f2 / 25.0
    var f4 float32
    if w := src.WeaponItem(); w != nil && e.IsServerLevel() {
        f4 = mthClampF(modifyArmorEffectiveness(e.serverLevel, w, e, src, f3), 0.0, 1.0)
    } else {
        f4 = f3
    }
    return damage * (1.0 - f4)
}
```

---

## 8. NBT / disk format — pack() / apply()

### AttributeInstance.Packed (record)
FQCN: `net.minecraft.world.entity.ai.attributes.AttributeInstance$Packed`
```
record Packed(Holder<Attribute> attribute, double baseValue, List<AttributeModifier> modifiers)
```
Codec `Packed.CODEC` (RecordCodecBuilder.create):
```
"id"        : BuiltInRegistries.ATTRIBUTE.holderByNameCodec()      // the attribute holder, by registry name
"base"      : Codec.DOUBLE, ExtraCodecs.optionalAlwaysPresentFieldOf default 0.0   // ALWAYS written, default 0.0
"modifiers" : AttributeModifier.CODEC.listOf(), optionalFieldOf default []          // omitted when empty
```
`Packed.LIST_CODEC = Packed.CODEC.listOf()`.

So one packed attribute on disk = a compound:
```
{ id: "minecraft:max_health", base: 26.0, modifiers: [ {id:"...", amount: X, operation:"add_multiplied_base"}, ... ] }
```
- `base` is **always present** (optionalAlwaysPresentFieldOf → defaults to 0.0 on read if missing, always serialized on write).
- `modifiers` omitted entirely when the permanent-modifier list is empty (optionalFieldOf default `[]`).

### AttributeInstance.pack()
```
new Packed(this.attribute, this.baseValue, List.copyOf(this.permanentModifiers.values()))
```
**Only PERMANENT modifiers are saved** (transient modifiers — equipment, effects, the FOLLOW_RANGE random_spawn_bonus is permanent so it IS saved — are excluded unless permanent).

### AttributeInstance.apply(Packed p)
```
this.baseValue = p.baseValue;
for (AttributeModifier m : p.modifiers) {
    this.modifierById.put(m.id(), m);
    this.getModifiers(m.operation()).put(m.id(), m);
    this.permanentModifiers.put(m.id(), m);
}
// (then setDirty — see tail of method)
```
Loaded modifiers go into all three maps (treated as permanent).

### AttributeMap.pack() / apply(List)
```
List<Packed> pack() {
    ArrayList result = new ArrayList(attributes.size());
    for (AttributeInstance ai : attributes.values()) result.add(ai.pack());
    return result;
}
void apply(List<Packed> list) {
    for (Packed p : list) {
        AttributeInstance ai = this.getInstance(p.attribute());   // instantiates from supplier template
        if (ai != null) ai.apply(p);
    }
}
```
Only currently-instantiated attributes (those that diverged from the supplier template) are packed. `apply` materializes each saved attribute via `getInstance` (which copies the supplier default first via createInstance→replaceFrom) then overwrites base+modifiers.

### Entity-level wiring (LivingEntity)
- **Save** (`addAdditionalSaveData`):
  ```
  output.store("attributes", AttributeInstance.Packed.LIST_CODEC, this.getAttributes().pack());
  ```
  NBT key = `"attributes"`, value = a list of the Packed compounds above.
- **Load** (`readAdditionalSaveData`):
  ```
  input.read("attributes", AttributeInstance.Packed.LIST_CODEC).ifPresent(this.getAttributes()::apply);
  ```

> This is the disk format SUB-PERSIST will need. The `"attributes"` tag is a TAG_List of compounds; each compound is `{id:string, base:double, modifiers:list}`. Modifier compound = `{id:string, amount:double, operation:string}`.

---

## Dependencies / things NOT fully extracted (flagged per mandate)

1. **`Mob.RANDOM_SPAWN_BONUS_ID`** — RESOLVED: `Mob.<clinit>` does `Identifier.withDefaultNamespace("random_spawn_bonus")` → **`minecraft:random_spawn_bonus`**.
2. **`RandomSource.triangle`** — RESOLVED (verbatim bytecode): `double triangle(d, d2) { return d + d2 * (nextDouble() - nextDouble()); }` — draws `nextDouble()` twice, **minuend first then subtrahend**. (There is also a `float triangle(f, f2)` overload using nextFloat twice, unused here.) Still confirm the concrete `nextDouble`/`nextFloat` algorithm of the level's `getRandom()` source (LegacyRandomSource vs ThreadSafeLegacyRandomSource) for full bit-exact stream parity.
3. **Cat variant + sound variant selection** (`VariantUtils.selectVariantToSpawn`, `CatSoundVariants.pickRandomSoundVariant`) — RNG draw count/order depends on the `minecraft:cat_variant` registry entries, their `SpawnConditions`, and weighting. Needs registry/tag data to reproduce exactly.
4. **Villager type mapping** (`finalizeVillagerType`) — biome→`villager_type` registry lookup, deterministic (no RNG) but needs the type registry + biome tag data.
5. **PatrollingMonster.finalizeSpawn / AbstractVillager.finalizeSpawn** intermediate overrides — verify they add no RNG draws before the `super` → `Mob.finalizeSpawn` call (to keep the Witch/Villager RNG stream exact). Dump them if exact stream parity is required.
6. **`EnchantmentHelper.modifyArmorEffectiveness`** — armor-piercing enchant hook; returns `f3` unchanged when no relevant enchantments. Stub as identity (`return f3`) behind a cited constant until the enchantment subsystem exists.
7. **`DamageTypeTags.BYPASSES_ARMOR`, `EntityTypeTags.FALL_DAMAGE_IMMUNE`** — tag data dependencies (extract from data tags).
8. **A few `Attributes` defaults** (gravity, step_height, jump_strength, oxygen_bonus, friction_modifier full range, temptation_range range) were truncated; re-dump `Attributes.<clinit>` when those attributes are needed. All SUB-ATTRIB-mob-relevant attributes are exact in §5.

## Identical-behavior checklist for the implementer
- [ ] `calculateValue`: ADD_VALUE→ADD_MULTIPLIED_BASE (d1 frozen)→ADD_MULTIPLIED_TOTAL `*=(1+a)`; all `float64`; final `SanitizeValue`.
- [ ] `RangedAttribute.SanitizeValue`: NaN→min, else clamp(min,max). Base `Attribute.SanitizeValue` = identity.
- [ ] Operation wire = int id (0/1/2, OOB→0); NBT = lowercase string.
- [ ] Supplier base overrides via `buildKeepingLast` (last add wins); Mob FOLLOW_RANGE base=16.0; per-mob bases exact (§5).
- [ ] `finalizeSpawn`: triangle(0.0, 0.11485000000000001) [2x nextDouble, only if no existing modifier] then nextFloat()<0.05f.
- [ ] Armor: `2.0f + toughness/4.0f`; clamp(armor - dmg/f, armor*0.2f, 20.0f)/25.0f; `dmg*(1-f4)`; casts i2f/d2f.
- [ ] Fall: `floor((fallDist + 1e-6 - safe_fall_distance) * multiplier * fall_damage_multiplier)`; f2d on multiplier.
- [ ] Knockback: `strength *= (1 - knockback_resistance)`; `<=0` early return; degenerate-dir epsilon `9.999999747378752E-6`, 4x nextDouble fixup.
- [ ] Save/load: only permanent modifiers packed; NBT key `"attributes"`; compound `{id, base(always), modifiers(omit-if-empty)}`.
