# SUB-ITEMNBT — ItemStack Disk (NBT) Codec — 1:1 jar spec

Decompiled from the **unobfuscated 26.2 server jar** (`temp/cache/26.2-inner.jar`, protocol 776)
via `javap -c -p`. Every claim below cites the class/method and bytecode it came from.

Scope: the **disk** (NBT / `ValueOutput`+`Codec`) serialization of `ItemStack` `{id,count,components}`,
plus `ItemStackWithSlot` and `ContainerHelper.saveAllItems/loadAllItems` — for persisting chest
contents and player inventory. This is the format Sulfur does **not** yet have (it has only the WIRE
format, in `level/component/types.go`).

> **TL;DR for the impatient.** Disk ItemStack is an NBT compound
> `{ id: "minecraft:stone", count: 3, components: { "minecraft:custom_name": <tag>, "!minecraft:enchantments": {} } }`.
> The `components` map is **string-keyed by the component's registry name** (with a `"!"` prefix for
> removals) and each value is the component's **`codec()`** (NBT) form — which is a **different
> serializer** from the wire **`streamCodec()`** form Sulfur currently captures as raw bytes. The wire
> bytes Sulfur stores **cannot** be dropped into disk NBT. See [WIRE-VS-DISK GAP](#wire-vs-disk-gap).

---

## 1. `ItemStack.MAP_CODEC` / `CODEC` / `OPTIONAL_CODEC`

### Fields (`javap -p net.minecraft.world.item.ItemStack`)

```
public static final MapCodec<ItemStack> MAP_CODEC;
public static final Codec<ItemStack>    CODEC;
public static final Codec<ItemStack>    OPTIONAL_CODEC;
private int count;
private final Holder<Item> item;
private final PatchedDataComponentMap components;
```

### `static {}` wiring (ItemStack `static {}`)

```
 85: ldc_w  "ItemStack"
 88: invokedynamic #26 apply ()Function          // -> lambda$static$1 (the RecordCodecBuilder body)
 93: invokestatic  MapCodec.recursive:(String,Function)MapCodec
 96: putstatic     MAP_CODEC
 99: getstatic     MAP_CODEC
107: invokedynamic #27 get (MapCodec)Supplier
112: invokestatic  Codec.lazyInitialized:(Supplier)Codec
115: putstatic     CODEC                          // CODEC = lazyInitialized(MAP_CODEC.codec())
118: getstatic     CODEC
121: invokestatic  ExtraCodecs.optionalEmptyMap:(Codec)Codec
124: invokedynamic #28 apply ()Function          // lambda$static$3  (Optional -> ItemStack)
129: invokedynamic #29 apply ()Function          // lambda$static$4  (ItemStack -> Optional)
134: invokeinterface Codec.xmap:(Function,Function)Codec
139: putstatic     OPTIONAL_CODEC
```

- **`MAP_CODEC`** = `MapCodec.recursive("ItemStack", lambda$static$1)` — the record builder (below).
- **`CODEC`** = `Codec.lazyInitialized(MAP_CODEC.codec())` — same three fields, as a standalone compound.
- **`OPTIONAL_CODEC`** = `ExtraCodecs.optionalEmptyMap(CODEC).xmap(opt→stack, stack→opt)`:
  `lambda$static$3` maps `Optional.empty → ItemStack.EMPTY`; `lambda$static$4` maps
  `EMPTY → Optional.empty`, present → `Optional.of(stack)`. **i.e. an empty/absent compound == EMPTY
  stack.** Use OPTIONAL_CODEC where a slot may legitimately be empty and you want `{}`/absent to mean
  "no item"; use CODEC/MAP_CODEC where the stack is always present.

### The record builder (`ItemStack.lambda$static$1(RecordCodecBuilder$Instance)`)

```
  0: aload_0
  1: getstatic  Item.CODEC_WITH_BOUND_COMPONENTS:Codec
  4: ldc_w      "id"
  7: invokeinterface Codec.fieldOf:(String)MapCodec
 12: invokedynamic #22 apply ()Function           // getter ItemStack::typeHolder
 17: invokevirtual MapCodec.forGetter
                                                    // --- field 1: "id" (REQUIRED) ---
 20: iconst_1                                       // 1
 21: bipush 99                                      // 99
 23: invokestatic  ExtraCodecs.intRange:(II)Codec   // intRange(1,99)
 26: ldc_w  "count"
 29: iconst_1 ; Integer.valueOf(1)
 33: invokestatic  ExtraCodecs.optionalAlwaysPresentFieldOf:(Codec,String,Object)MapCodec
 36: invokedynamic #23 apply ()Function            // getter ItemStack::getCount
 41: invokevirtual MapCodec.forGetter
                                                    // --- field 2: "count" (default 1, range 1..99) ---
 44: getstatic  DataComponentPatch.CODEC:Codec
 47: ldc_w  "components"
 50: getstatic  DataComponentPatch.EMPTY:DataComponentPatch
 53: invokeinterface Codec.optionalFieldOf:(String,Object)MapCodec
 58: invokedynamic #24 apply ()Function            // getter ItemStack::getComponentsPatch
 63: invokevirtual MapCodec.forGetter
                                                    // --- field 3: "components" (default EMPTY) ---
 66: invokevirtual Instance.group:(...)Products$P3
 70: invokedynamic #25 apply ()Function3           // ctor (Holder,int,DataComponentPatch)->ItemStack
 75: invokevirtual P3.apply
 78: areturn
```

### Disk NBT layout — EXACT field names, order, defaults

| Order | NBT key       | Codec                                                        | Default            | Always written?                                            |
| ----- | ------------- | ----------------------------------------------------------- | ------------------ | --------------------------------------------------------- |
| 1     | `"id"`        | `Item.CODEC_WITH_BOUND_COMPONENTS` → `Codec<Holder<Item>>`  | none (REQUIRED)    | yes                                                       |
| 2     | `"count"`     | `optionalAlwaysPresentFieldOf(intRange(1,99), "count", 1)`  | `1`                | **yes** (always present — see §1a)                        |
| 3     | `"components"`| `DataComponentPatch.CODEC.optionalFieldOf("components", EMPTY)` | `EMPTY` (no patch) | **no** — omitted when the patch is empty (§3)             |

- **`"id"`** serializes as a registry identifier **string**, e.g. `"minecraft:diamond_sword"` (it is a
  `Holder<Item>` codec; the holder-by-name form writes the namespaced id). `CODEC_WITH_BOUND_COMPONENTS`
  differs from plain `Item.CODEC` only in that it binds the item's prototype components for validation;
  the **on-disk shape is the same identifier string**.
- **`"count"`** is an NBT **int**, clamped 1..99 on decode (`intRange`). Counts ≤0 never occur on disk —
  empty stacks are simply omitted from the container list (see §5).
- **`"components"`** is an NBT **compound** (the DataComponentPatch, §3). Omitted entirely if empty.

### §1a — `optionalAlwaysPresentFieldOf` semantics (why "count" is always written)

`ExtraCodecs.optionalAlwaysPresentFieldOf(codec, name, default)` (bytecode):

```
0: aload_1 ; aload_0 ; iload_3
3: invokestatic Codec.optionalField:(String,Codec,boolean=false)MapCodec   // lenient=false
6: aload_2
7: invokedynamic apply (default)Function   // decode: Optional -> orElse(default)
12: invokedynamic apply ()Function         // encode: value -> Optional.of(value)
17: invokevirtual MapCodec.xmap
```

The encode side wraps the value in `Optional.of(...)` **unconditionally**, so `optionalField` always
emits the key. On decode, a **missing** key yields `default`. **Net: `count` is always written, even
when it equals 1; a missing `count` on read defaults to 1.** (Contrast `optionalFieldOf(name,default)`
used for `components`, which omits the key when the value equals the default.)

### Pseudo-Go

```go
// Disk NBT compound for one ItemStack (non-empty).
type ItemStackDisk struct {
    ID         string       `nbt:"id"`                   // "minecraft:stone"  (required)
    Count      int32        `nbt:"count"`                // 1..99, always present, default 1
    Components *ComponentsNBT `nbt:"components,omitempty"` // omitted when empty patch
}
// EMPTY stack <-> absent/empty compound is handled by OPTIONAL_CODEC, not by this struct.
```

---

## 2. `ItemStack.save(...)` / serialization to a Tag

There is **no hand-written `ItemStack.save(...)`** in 26.2 — serialization goes entirely through
`CODEC`/`MAP_CODEC` (the codec *is* the save path). The relevant accessor methods the codec getters
call are confirmed present:

```
public Holder<Item> typeHolder();                  // getter for "id"
public int getCount();                              // getter for "count"
public DataComponentPatch getComponentsPatch();     // getter for "components"
```

`getComponentsPatch()` (confirmed in `-p` listing) returns the **patch** (diff vs the item's prototype
components), **not** the full `DataComponentMap`. So on disk you only store **non-default** components
(plus explicit removals), exactly like the wire patch — but encoded differently (§3, §GAP).

To "save" an ItemStack to NBT you encode `MAP_CODEC` (flattening into a parent compound, e.g. inside
`ItemStackWithSlot`) or `CODEC` (standalone compound). To save an *empty-or-present* slot, use
`OPTIONAL_CODEC`.

---

## 3. `DataComponentPatch.CODEC` — the disk component-map container

This is the heart of the disk format and the **key difference from the wire**.

### Fields (`net.minecraft.core.component.DataComponentPatch`)

```
public static final DataComponentPatch EMPTY;
public static final Codec<DataComponentPatch> CODEC;
public static final StreamCodec<RegistryFriendlyByteBuf, DataComponentPatch> STREAM_CODEC;          // wire
public static final StreamCodec<RegistryFriendlyByteBuf, DataComponentPatch> DELIMITED_STREAM_CODEC; // wire
private static final String REMOVED_PREFIX;   // == "!"  (see PatchKey decode/encode)
final Reference2ObjectMap<DataComponentType<?>, Optional<?>> map;   // type -> Optional(value); empty Optional == removed
```

### `CODEC` wiring (`DataComponentPatch.static {}`)

```
13: getstatic     DataComponentPatch$PatchKey.CODEC:Codec
16: invokedynamic #1 apply ()Function           // PatchKey::valueCodec
21: invokestatic  Codec.dispatchedMap:(Codec,Function)Codec   // dispatchedMap(keyCodec, key->valueCodec)
24: invokedynamic #2 apply ()Function           // lambda$static$0 : Map -> DataComponentPatch
29: invokedynamic #3 apply ()Function           // lambda$static$1 : DataComponentPatch -> Map
34: invokeinterface Codec.xmap
39: putstatic     CODEC
```

So **`CODEC = Codec.dispatchedMap(PatchKey.CODEC, PatchKey::valueCodec).xmap(map→patch, patch→map)`**.

`dispatchedMap` = an NBT **compound** whose **keys** are produced by `PatchKey.CODEC` (a String) and
whose **value** for each key is decoded/encoded by `PatchKey.valueCodec()` for that key.

### `PatchKey` — the string key with the `"!"` removal prefix

`PatchKey` is a record `{ DataComponentType<?> type, boolean removed }`. Its `CODEC` is
`Codec.STRING.flatXmap(decode, encode)`:

**Decode** (`PatchKey.lambda$static$0(String)`):
```
 0: aload_0 ; ldc "!" ; invokevirtual String.startsWith         // removed = key.startsWith("!")
 7: ifeq 21
11: aload_0 ; ldc "!" ; String.length ; String.substring        // strip leading "!"
21: Identifier.tryParse(key)
26: BuiltInRegistries.DATA_COMPONENT_TYPE.getValue(id)           // lookup component type
39: ifnonnull 53 -> error "No component with type ..." if missing
53: DataComponentType.isTransient() ? error : new PatchKey(type, removed)
```

**Encode** (`PatchKey.lambda$static$3(PatchKey)`):
```
 5: BuiltInRegistries.DATA_COMPONENT_TYPE.getKey(type)           // id = registry key (Identifier)
15: ifnonnull 29 -> error if not registered
29: removed ? ("!" + id.toString())  :  id.toString()           // prefix "!" for removals
52: success
```

→ **Key string = `"<namespace>:<path>"` for a set component, `"!<namespace>:<path>"` for a removal.**
Transient components are rejected (cannot be persisted).

### `PatchKey.valueCodec()` — per-key value codec

```
0: getfield removed
4: ifeq 16
7: Codec.EMPTY.codec()       // removed -> value is the EMPTY map codec (writes/reads an empty compound {})
16: type.codecOrThrow()      // set -> the component's DATA codec  (type.codec(), NOT streamCodec())
```

- For a **set** component, the value is encoded by **`DataComponentType.codec()`** — the *data/NBT* codec
  (the one used by datapacks / disk). This is the per-component delegation: each component supplies its
  own NBT shape. (e.g. `minecraft:damage` → an int tag; `minecraft:custom_name` → a text-component tag;
  `minecraft:enchantments` → a compound; etc. — 100+ components, each delegating to its own `codec()`.)
- For a **removed** component, the value is `Codec.EMPTY.codec()` which serializes as an **empty compound
  `{}`** (the marker carries no data; the `"!"` prefix in the key is what signals removal).

### `map`/`Map` xmap (`lambda$static$1`)

Round-trips the internal `Reference2ObjectMap<type,Optional<value>>` to a `Map<PatchKey,Object>`:
present `Optional` → `PatchKey(type, removed=false)` with the value; empty `Optional` →
`PatchKey(type, removed=true)` with `Unit.INSTANCE`. **Transient types are skipped** (`isTransient()` →
`continue`). This confirms removals are stored as the empty-Optional entries.

### Disk NBT layout of `components`

```
components: {
    "minecraft:custom_name":  <component.codec() output>,   // a set component
    "minecraft:damage":       42,                            // another set component
    "!minecraft:enchantments": {}                            // a removal marker (empty compound)
    ...
}
```

### Pseudo-Go

```go
// ComponentsNBT is an NBT compound. Keys are component registry ids; a "!"-prefixed key is a removal.
// Each set value is the component-type-specific DATA codec output (per-component delegation).
type ComponentsNBT map[string]nbt.RawMessage
// Encode rule: for each component in the patch:
//   if set:     out["minecraft:<name>"]  = component.DataCodec.encode(value)
//   if removed: out["!minecraft:<name>"] = {}   // empty compound
// Transient components are never written.
// Decode rule: for each key:
//   removed := strings.HasPrefix(key, "!")
//   id      := strings.TrimPrefix(key, "!")
//   type    := registry.DataComponentType(id)   // error if unknown / transient
//   if removed { patch.remove(type) } else { patch.set(type, type.DataCodec.decode(value)) }
```

> **Per-component values are out of scope to enumerate (100+ types).** What matters structurally is the
> container above: **string keys, `"!"` removal prefix, per-type delegation to `DataComponentType.codec()`.**

---

## 4. `net.minecraft.world.ItemStackWithSlot` — `{Slot:int, <flattened ItemStack>}`

### Class

```
public final class ItemStackWithSlot extends Record {
    private final int slot;
    private final ItemStack stack;
    public static final Codec<ItemStackWithSlot> CODEC;
    public ItemStackWithSlot(int, ItemStack);
    public boolean isValidInContainer(int);
    public int slot();
    public ItemStack stack();
}
```

### `CODEC` (`ItemStackWithSlot.lambda$static$0(RecordCodecBuilder$Instance)`)

```
 1: getstatic  ExtraCodecs.UNSIGNED_BYTE:Codec        // <Integer>, 0..255 as NBT byte
 4: ldc        "Slot"                                  // <-- CAPITAL "Slot"
 6: iconst_0 ; Integer.valueOf(0)
10: invokestatic ExtraCodecs.optionalAlwaysPresentFieldOf:(Codec,String,Object)MapCodec
13: invokedynamic #1 apply ()Function                  // getter ItemStackWithSlot::slot
18: invokevirtual MapCodec.forGetter
21: getstatic  ItemStack.MAP_CODEC:MapCodec            // <-- the ItemStack record, FLATTENED
24: invokedynamic #2 apply ()Function                  // getter ItemStackWithSlot::stack
29: invokevirtual MapCodec.forGetter
32: invokevirtual Instance.group:(...)Products$P2
36: invokedynamic #3 apply ()BiFunction                // ctor (int,ItemStack)->ItemStackWithSlot
41: invokevirtual P2.apply
```

### Confirmed facts

- The field is **`"Slot"` with a capital S** (`ldc "Slot"`). Not `"slot"`.
- `"Slot"` codec is **`ExtraCodecs.UNSIGNED_BYTE`** → NBT **byte** (0..255), default `0`, written via
  `optionalAlwaysPresentFieldOf` so it is **always present** (and a missing `Slot` decodes to 0).
- The stack uses **`ItemStack.MAP_CODEC`** (a `MapCodec`, via `forGetter`) — so `id`/`count`/`components`
  are **flattened into the SAME compound as `Slot`**. There is no nested `"stack"`/`"item"` wrapper.

### Disk NBT layout (one list element)

```
{ Slot: 3b, id: "minecraft:diamond_sword", count: 1, components: { ... } }
```

### Pseudo-Go

```go
type ItemStackWithSlotDisk struct {
    Slot       int8         `nbt:"Slot"`                 // UNSIGNED_BYTE, default 0, always present
    // ---- ItemStack.MAP_CODEC flattened into the SAME compound ----
    ID         string       `nbt:"id"`
    Count      int32        `nbt:"count"`
    Components *ComponentsNBT `nbt:"components,omitempty"`
}
```

### isValidInContainer — see §6.

---

## 5. `ContainerHelper.saveAllItems` / `loadAllItems` — the `"Items"` list

```
public static final String TAG_ITEMS;   // == "Items"
public static void saveAllItems(ValueOutput, NonNullList<ItemStack>);
public static void saveAllItems(ValueOutput, NonNullList<ItemStack>, boolean);
public static void loadAllItems(ValueInput, NonNullList<ItemStack>);
```

### `saveAllItems(out, list)` (2-arg)

```
0: aload_0 ; aload_1 ; iconst_1
3: invokestatic saveAllItems(out, list, true)   // delegates with flag = true
```
→ **the 2-arg form passes `true`** for the boolean.

### `saveAllItems(out, list, boolean keepEmptyTag)` (3-arg)

```
 0: out.list("Items", ItemStackWithSlot.CODEC)  -> TypedOutputList typedList     // astore_3
12: i = 0
15: for (i=0; i < list.size(); i++) {
24:   stack = list.get(i)
35:   if (!stack.isEmpty()) {                                                     // SKIP EMPTY
43:     typedList.add(new ItemStackWithSlot(i, stack))                            // slot = list index i
       }
60:   i++
   }
66: if (typedList.isEmpty() && !flag)        // flag==false AND list empty
79:   out.discard("Items")                   // remove the key entirely
87: return
```

- Writes an NBT **list** under key **`"Items"`**, each element an `ItemStackWithSlot` (§4).
- **Empty stacks are skipped** (`stack.isEmpty()` → not added). The element's `Slot` = the **index `i`**
  in the `NonNullList` (so it is a sparse list keyed by slot).
- **The `boolean` flag = "keep empty `Items` tag".** If the resulting list is empty **and** `flag==false`,
  the `"Items"` key is **discarded** (removed). If `flag==true`, an empty `Items` list is left in place.
  The 2-arg convenience form passes `true` (keep the empty tag).

### `loadAllItems(in, list)`

```
 0: in.listOrEmpty("Items", ItemStackWithSlot.CODEC) -> TypedInputList           // empty if absent/invalid
11: iterator
17: while (it.hasNext()) {
26:   ItemStackWithSlot e = it.next()
36:   if (e.isValidInContainer(list.size()))      // bounds check, §6
47:     list.set(e.slot(), e.stack())             // place stack at its slot index
     }
63: return
```

- Reads `"Items"` as a list (or empty if missing/wrong type — `listOrEmpty`).
- For each element, **bounds-checks** `isValidInContainer(list.size())`; if valid, `list.set(slot, stack)`.
  Out-of-range slots are **silently skipped** (no throw). Slots not present in the list keep their prior
  value (normally `ItemStack.EMPTY` from `NonNullList.withSize(..., EMPTY)`).

### Pseudo-Go

```go
func SaveAllItems(out *NbtCompound, list []ItemStack, keepEmptyTag bool) {
    var items []ItemStackWithSlotDisk
    for i, st := range list {
        if st.IsEmpty() { continue }            // skip empty
        items = append(items, encodeWithSlot(i, st))
    }
    if len(items) == 0 && !keepEmptyTag {
        out.Discard("Items")
        return
    }
    out.PutList("Items", items)
}
// SaveAllItems2(out, list) => SaveAllItems(out, list, true)

func LoadAllItems(in *NbtCompound, list []ItemStack) {
    for _, e := range in.ListOrEmpty("Items") {        // empty if key absent/not a list
        if isValidInContainer(e.Slot, len(list)) {
            list[e.Slot] = e.Stack
        }
    }
}
```

---

## 6. `ItemStackWithSlot.isValidInContainer(int size)`

```
0: getfield slot
4: iflt 19                  // slot < 0  -> false
7: getfield slot ; iload_1
12: if_icmpge 19            // slot >= size -> false
15: iconst_1 ; goto 20      // else true
19: iconst_0
20: ireturn
```

→ **`return slot >= 0 && slot < size;`** Plain half-open bounds check. (Note: even though `Slot` is read
as UNSIGNED_BYTE so the value is already 0..255, the `slot >= 0` guard is kept verbatim.)

```go
func isValidInContainer(slot int, size int) bool { return slot >= 0 && slot < size }
```

---

## WIRE-VS-DISK GAP

This is the crux of the task. **Sulfur today stores components as raw WIRE bytes** (`SlotData.RawComponents`
in `level/component/types.go`). Those bytes **cannot be written into disk NBT.** Here is exactly why.

### The two formats are structurally different containers AND use different per-component serializers

| Aspect                | WIRE (`DataComponentPatch.STREAM_CODEC`, `DataComponentPatch$3`) | DISK (`DataComponentPatch.CODEC`)                                  |
| --------------------- | ---------------------------------------------------------------- | ----------------------------------------------------------------- |
| Container shape       | `VarInt addedCount, VarInt removedCount`, then two flat lists    | NBT **compound** (a `dispatchedMap`)                              |
| Component **key**     | `DataComponentType.STREAM_CODEC` → **registry id as VarInt**     | `PatchKey.CODEC` → **registry name string** `"namespace:path"`    |
| Removal marker        | listed in the `removedCount` section (just the id VarInt)        | key **prefixed with `"!"`**, value `{}`                           |
| Per-component **value** serializer | **`DataComponentType.streamCodec()`** (the *network* codec) | **`DataComponentType.codec()`** (the *data/NBT* codec)            |
| Empty patch encoding  | `VarInt 0, VarInt 0`                                             | key `components` **omitted** entirely                            |

The decisive evidence:

- **`DataComponentType` exposes two independent codecs** (`-p net.minecraft.core.component.DataComponentType`):
  ```
  public abstract Codec<T> codec();                                            // DATA / NBT  -> used by DISK
  public abstract StreamCodec<? super RegistryFriendlyByteBuf, T> streamCodec(); // NETWORK    -> used by WIRE
  ```
  Wire decode (`DataComponentPatch$3.decode`) calls `codecGetter.apply(type)` → that `CodecGetter`
  resolves to **`type.streamCodec()`**. Disk (`PatchKey.valueCodec`) calls **`type.codecOrThrow()`** →
  `type.codec()`. **These are different serializers** and for most components produce **different byte
  layouts** (e.g. a text Component is a length-prefixed NBT blob on the wire vs an SNBT/Component tag on
  disk; identifiers are VarInt registry ids on the wire vs strings on disk; etc.).
- The **keys** are completely different encodings: VarInt registry index (wire) vs namespaced string (disk).
- Sulfur's `SlotData.RawComponents` is, by its own doc, "the verbatim wire bytes of the added+removed
  component lists" — i.e. it is **registry-id-indexed, streamCodec-encoded data.** Writing those bytes
  under a disk `components` compound would produce a corrupt, unreadable region file.

### Confirmation that Sulfur's WIRE model is correct (so the bridge is the only missing piece)

`DataComponentPatch$3.decode` (wire) bytecode matches `SlotData.ReadFrom` field-for-field:
```
readVarInt addedCount ; readVarInt removedCount ;
addedCount   × ( DataComponentType.STREAM_CODEC.decode  [id VarInt] ;  codecGetter(type).decode [value] )
removedCount × ( DataComponentType.STREAM_CODEC.decode  [id VarInt] )
```
…and `encode` writes `VarInt 0, VarInt 0` for an empty patch — identical to `SlotData`'s
`count<=0`/empty handling. So Sulfur's wire capture is faithful; the gap is purely **wire→disk
transcoding**, which requires actually *interpreting* each component value.

### Recommendation — what can be done NOW (1:1 faithful) vs what needs the component parser

**Can persist faithfully TODAY (no component parser needed):**

- `id` (item registry id → namespaced string) — Sulfur has the item registry, so `ItemID → "minecraft:..."`.
- `count` (int 1..99, default 1).
- The whole `ItemStackWithSlot` envelope: `Slot` byte, flatten id/count, skip-empty, the `"Items"` list,
  `isValidInContainer` bounds, the `saveAllItems`/`loadAllItems` control flow and the `keepEmptyTag` flag.
- **Round-trip of component-free stacks is fully correct** (the common case: plain blocks, tools without
  enchants/names, food, etc. — for these `RawComponents` is empty and `components` is simply omitted).

**Requires a wire→disk component transcoder (a per-`DataComponentType` codec layer) — CANNOT be faked:**

- Any stack carrying components (enchanted, renamed/`custom_name`, damaged tools, written books, potions,
  shulker/bundle contents, banners, profiles, etc.). Sulfur holds these only as opaque `streamCodec` bytes.
  To write disk NBT you must (a) decode each value with `type.streamCodec()` into a real value, then
  (b) re-encode it with `type.codec()` into NBT, and (c) emit the `"namespace:path"` (or `"!..."`) key.
  There is no shortcut: the key encoding and the value encoding both differ.

**Recommended phased plan:**

1. **Phase A (now, fully 1:1):** Implement `ItemStackWithSlot` + `ContainerHelper.saveAllItems/loadAllItems`
   + the `id`/`count` portion of `ItemStack.MAP_CODEC`, with `components` **omitted on write** and
   **ignored on read**. This persists inventories/chests correctly for component-free items, faithful to
   vanilla structure. **Explicitly degrade**: a stack that *has* components is written as `{Slot,id,count}`
   only — it survives as the right item & count but **loses enchantments/custom names/damage/etc.** This
   is a *lossy* but structurally-vanilla save. Mark it clearly (a TODO/metric) so the gap is visible —
   do **not** silently bake away the components.
   - Caveat: dropping `components` means a damaged/enchanted tool reloads as a pristine base item. Acceptable
     only as an interim; flag every stack where `len(RawComponents) > 0` so the loss is measurable.
2. **Phase B (unblocks full fidelity):** Build the per-`DataComponentType` codec pair (the `codec()` /
   `streamCodec()` duo) — at minimum a `streamCodec → value → codec` transcoder. Sulfur already has many
   per-component **wire** structs in `level/component/*_gen.go`; those give the `streamCodec` decode. What's
   missing is the **`codec()` (NBT) encode** for each. Once that exists, `components` round-trips on disk
   and enchanted/named items persist 1:1.

**Bottom line:** Today Sulfur can persist `{Slot, id, count}` 1:1 with vanilla structure (correct for plain
items, degrading component-bearing items to their base form). Full component persistence is **blocked on a
wire→disk component transcoder** (per-type `streamCodec`↔`codec`), because the disk container is string-keyed
NBT using each component's *data* codec, which the raw wire bytes are **not**.

---

## Source map (every claim → bytecode)

| Claim                                            | Class / method                                                        |
| ------------------------------------------------ | --------------------------------------------------------------------- |
| ItemStack fields `id`/`count`/`components`, order, defaults | `ItemStack.lambda$static$1`                                  |
| CODEC / OPTIONAL_CODEC / MAP_CODEC wiring        | `ItemStack.static {}`                                                  |
| `count` always-written semantics                 | `ExtraCodecs.optionalAlwaysPresentFieldOf(...,boolean)` → `Codec.optionalField` |
| `components` omitted when empty                  | `optionalFieldOf("components", EMPTY)` in `lambda$static$1`            |
| save path is the codec; getters                  | `ItemStack` `-p` (`typeHolder/getCount/getComponentsPatch`)           |
| Disk component map = dispatchedMap, `"!"` prefix | `DataComponentPatch.static {}`, `DataComponentPatch$PatchKey` (CODEC, valueCodec, lambda$static$0/$3) |
| disk value uses `type.codec()`; removed uses `Codec.EMPTY` | `DataComponentPatch$PatchKey.valueCodec`                    |
| wire value uses `type.streamCodec()`; counts layout | `DataComponentPatch$3.decode/encode`                              |
| two distinct codecs on a component type          | `DataComponentType` `-p` (`codec()` vs `streamCodec()`)               |
| `"Slot"` capital, UNSIGNED_BYTE, flattened stack | `ItemStackWithSlot.lambda$static$0`                                   |
| `isValidInContainer` = `slot>=0 && slot<size`    | `ItemStackWithSlot.isValidInContainer`                                |
| `"Items"`, skip-empty, slot=index, keepEmptyTag flag, discard | `ContainerHelper.saveAllItems(...,boolean)` + 2-arg delegate |
| load: listOrEmpty, bounds-check, set             | `ContainerHelper.loadAllItems`                                        |
| `id` is a `Holder<Item>` identifier string       | `Item.CODEC_WITH_BOUND_COMPONENTS` (field type `Codec<Holder<Item>>`) |
