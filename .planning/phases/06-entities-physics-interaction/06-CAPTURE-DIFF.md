# ENT-01 Capture-Diff: Sulfur vs. Vanilla 26.2 (Entity Wire Encoders)

**Phase 6, Plan 06-02 — the jar-derived record; byte-level seal deferred to Plan 06-07.**

This document records the jar-derived (decompiled, `javap -p -c`) wire layouts of the
proto-776 clientbound ENTITY packets Sulfur's tracker emits — `ClientboundAddEntity`,
`ClientboundSetEntityData`, `ClientboundSetEntityMotion`, the three
`ClientboundMoveEntity*` deltas, `ClientboundTeleportEntity`, `ClientboundRotateHead`,
and `ClientboundRemoveEntities` — plus the two MEDIUM-confidence sub-codecs they depend
on: the **`Vec3.LP_STREAM_CODEC`** low-precision movement quantizer (new in 26.x — NOT
the historical `×8000 short`) and the **`SynchedEntityData`** indexed-entry framing with
its `0xFF` (255) EOF terminator.

Status: **JAR-DERIVED (this plan). The byte-level seal — a capture-diff against a real
vanilla 26.2 server + a real-client visual (the entity spawns, moves, and despawns in an
unmodified 26.2 client) — is Plan 06-07.** The fork's `pk.*` codecs are *symmetric*, so a
self-round-trip in `entity_encode_test.go` passes even on a wire a real client could
reject; the LP quantizer and the metadata serializer codecs are exactly the surfaces that
demand the real-server diff. Do NOT claim these byte-proven from the self-round-trip alone.

---

## 1. Decompile Method (reproducible)

- Jar: `temp/cache/26.2-inner.jar` (the unobfuscated 26.2 inner server jar).
- Tool: Zulu 25.0.3 `javap -p -c -constants -classpath temp/cache/26.2-inner.jar <FQCN>`.
- Classes read this session:
  - `net.minecraft.network.protocol.game.ClientboundAddEntityPacket` (`write` method)
  - `net.minecraft.world.phys.Vec3` (`LP_STREAM_CODEC` static init → delegates to `LpVec3`)
  - `net.minecraft.network.LpVec3` (`write` / `read` / `pack` / `unpack` / `sanitize`)
  - `net.minecraft.network.protocol.game.ClientboundSetEntityDataPacket` (`pack` + `EOF_MARKER`)
  - `net.minecraft.network.syncher.SynchedEntityData$DataValue` (`write`)
  - `net.minecraft.network.protocol.game.ClientboundMoveEntityPacket$Pos/$PosRot/$Rot`
  - `net.minecraft.network.protocol.game.ClientboundTeleportEntityPacket`
  - `net.minecraft.world.entity.PositionMoveRotation`
  - `net.minecraft.network.protocol.game.ClientboundRotateHeadPacket`
  - `net.minecraft.network.protocol.game.ClientboundRemoveEntitiesPacket` (`writeIntIdList`)
  - `net.minecraft.network.protocol.game.ClientboundSetEntityMotionPacket`
  - `net.minecraft.network.protocol.game.VecDeltaCodec` (the move-delta `×4096` scale)

---

## 2. `ClientboundAddEntity` wire order (jar-confirmed)

From `ClientboundAddEntityPacket.write(RegistryFriendlyByteBuf)`, in exact emit order:

| # | Field      | Codec                         | Sulfur encoder |
|---|------------|-------------------------------|----------------|
| 1 | id         | `writeVarInt`                 | `pk.VarInt`    |
| 2 | uuid       | `writeUUID` (16 raw bytes)    | `pk.UUID`      |
| 3 | type       | `ByteBufCodecs.registry(ENTITY_TYPE)` → `writeVarInt(typeId)` | `pk.VarInt` |
| 4 | x          | `writeDouble`                 | `pk.Double`    |
| 5 | y          | `writeDouble`                 | `pk.Double`    |
| 6 | z          | `writeDouble`                 | `pk.Double`    |
| 7 | movement   | **`Vec3.LP_STREAM_CODEC`**    | `lpVec3` (§3)  |
| 8 | xRot       | `writeByte` (byte angle)      | `pk.Angle`     |
| 9 | yRot       | `writeByte` (byte angle)      | `pk.Angle`     |
| 10| yHeadRot   | `writeByte` (byte angle)      | `pk.Angle`     |
| 11| data       | `writeVarInt`                 | `pk.VarInt`    |

**CRITICAL:** the entity **type id is field 3** (right after the UUID, BEFORE x/y/z) — the
registry codec is encoded immediately after `writeUUID`. The movement is the LP quantizer,
**NOT three Doubles** and **NOT the legacy `×8000` short triple**. Rotation order is
`xRot, yRot, yHeadRot` (pitch, yaw, head-yaw) as raw byte angles.

---

## 3. `Vec3.LP_STREAM_CODEC` → `net.minecraft.network.LpVec3` (the 26.x quantizer)

`Vec3.LP_STREAM_CODEC = StreamCodec.of(LpVec3::write, LpVec3::read)`. This is a **new
variable-width quantized format**, not the historical `clamp(v,±3.9)*8000` short. Constants
(from `LpVec3`):

```
DATA_BITS          = 15        DATA_BITS_MASK = 32767
MAX_QUANTIZED_VALUE= 32766.0
SCALE_BITS         = 2         SCALE_BITS_MASK = 3        CONTINUATION_FLAG = 4 (bit 2)
X_OFFSET = 3   Y_OFFSET = 18   Z_OFFSET = 33
ABS_MAX_VALUE = 1.7179869183e10     ABS_MIN_VALUE = 3.051944088384301e-5
```

### `write(buf, vec)` algorithm (jar-exact)

```
x = sanitize(vec.x); y = sanitize(vec.y); z = sanitize(vec.z)   // NaN→0, clamp ±ABS_MAX
m = absMax(absMax(x, y), z)                                       // largest |component|
if m < ABS_MIN_VALUE (3.051944088384301e-5):
    writeByte(0x00); return                                      // ZERO-vector fast path
scale = ceilLong(m)                                              // smallest long ≥ m
cont  = (scale & 3) != scale                                     // needs > 2 bits of scale?
hdr   = cont ? ((scale & 3) | 4) : scale                         // 3-bit header (2 scale + cont)
px = pack(x/scale) << 3                                          // 15 bits at offset 3
py = pack(y/scale) << 18                                         // 15 bits at offset 18
pz = pack(z/scale) << 33                                         // 15 bits at offset 33
combined = hdr | px | py | pz                                    // a 48-bit-ish long
writeByte(combined)          // bits 0..7
writeByte(combined >> 8)     // bits 8..15
writeInt(combined >> 16)     // bits 16..47  (big-endian 4 bytes)
if cont: VarInt.write(scale >> 2)                               // high scale bits
```

where `pack(d) = round((d*0.5 + 0.5) * 32766.0)` maps `[-1,1] → [0,32766]`, and
`unpack(q) = min(q & 32767, 32766) * 2.0 / 32766.0 - 1.0` is the inverse (∈ `[-1,1]`).

### v1 consequence (the headline simplification)

A **freshly-spawned, stationary entity has velocity (0,0,0)** → `m = 0 < ABS_MIN_VALUE` →
`LpVec3.write` emits **exactly one byte `0x00`**. So for the v1 tracker (spawn + position
via TeleportEntity, no velocity broadcast) every `AddEntity` movement field is a single
`0x00`. Sulfur's `lpVec3` helper implements the FULL algorithm (correct for non-zero
velocity too — `SetEntityMotion`), but the only bytes the v1 path exercises are the
zero-vector `0x00`. **Byte-correctness of the non-zero quantization is sealed by 06-07.**

---

## 4. `ClientboundSetEntityData` + `SynchedEntityData` framing (jar-confirmed)

From `ClientboundSetEntityDataPacket`:

```
STREAM_CODEC body:  VarInt id, then pack(packedItems, buf)
EOF_MARKER = 255    // public static final int
pack(list, buf):    for each DataValue v: v.write(buf);  buf.writeByte(255)
```

So the wire is: `VarInt id`, then zero-or-more entry bodies, then a **mandatory single
`0xFF` (255) terminator byte** — ALWAYS present, even for an empty list.

`SynchedEntityData$DataValue.write(buf)` per entry (jar-exact order):

```
writeByte(id)                     // the metadata index (1 byte)
writeVarInt(serializerId)         // EntityDataSerializers.getSerializedId(serializer)
serializer.codec().encode(buf, value)   // the serializer-specific value
```

### v1 metadata decision

For v1 Sulfur emits the **minimum**: an `AddEntity` followed by a `SetEntityData` whose
list is EMPTY → body is just `VarInt id` + `0xFF`. No per-entry serializer codec surface to
get wrong (threat T-6-04 minimized). Whether the v1 test entity (`SulfurCube`) *renders*
from the empty-metadata path, or needs a specific shared-flags / pose entry, is the one
open question the **06-07 real-client visual** answers. If a non-default entry is required,
06-07 will add the single index-0 shared-flags `Byte` entry (serializer id for `BYTE`) and
re-seal. The framing here (`Byte index, VarInt serializerId, value, … , 0xFF`) is final.

---

## 5. Move / Teleport / Rotate / Remove / Motion (jar-confirmed)

| Packet                  | Wire order (jar `write`)                                                                 |
|-------------------------|------------------------------------------------------------------------------------------|
| `MoveEntityPos`         | `VarInt id, Short xa, Short ya, Short za, Boolean onGround`                               |
| `MoveEntityPosRot`      | `VarInt id, Short xa, Short ya, Short za, Byte yRot, Byte xRot, Boolean onGround`         |
| `MoveEntityRot`         | `VarInt id, Byte yRot, Byte xRot, Boolean onGround`                                       |
| `TeleportEntity`        | `VarInt id, PositionMoveRotation change, Int relativeFlags, Boolean onGround`             |
| `RotateHead`            | `VarInt id, Byte yHeadRot`                                                                |
| `RemoveEntities`        | `writeIntIdList` = `VarInt count, N × VarInt id`                                          |
| `SetEntityMotion`       | `VarInt id, Vec3.LP_STREAM_CODEC movement` (the §3 quantizer)                             |

`PositionMoveRotation.STREAM_CODEC` (used inside `TeleportEntity`) =
`Vec3.STREAM_CODEC position` (THREE Doubles) + `Vec3.STREAM_CODEC deltaMovement` (THREE
Doubles) + `Float yRot` + `Float xRot`. NOTE: the **regular** `Vec3.STREAM_CODEC` is three
plain `writeDouble`s (confirmed from `Vec3$1.encode`), distinct from the LP codec.

`relativeFlags` is `Relative.SET_STREAM_CODEC` = `ByteBufCodecs.INT` (big-endian Int);
`0` = every component absolute.

**Move delta scale (`VecDeltaCodec`):** `encode(d) = Math.round(d * 4096.0)`; the `Pos`/
`PosRot` short deltas are `(encode(curr) - encode(prev))` cast to `short` (`l2i; i2s`).
`TRUNCATION_STEPS = 4096.0`. **v1 favors `TeleportEntity` (absolute Doubles) for moved
entities** to avoid the short-delta overflow edge (deltas > ±8 blocks/tick overflow a
short); the delta encoders exist and are tested but the tracker uses the teleport path for
correctness. Switching to deltas is a later bandwidth optimization.

---

## 6. Byte-Angle helper

`degToByteAngle(deg) = round(deg * 256 / 360)` truncated to `int8` (`pk.Angle`), mirroring
the vanilla `Mth.packDegrees`/byte-angle convention used by the rotation fields above and
the existing Play-state precedent.

---

## 7. Deferred to Plan 06-07 (the byte-level seal)

1. **LP quantizer byte-correctness for non-zero velocity** — capture a real vanilla
   `SetEntityMotion` / a moving entity's `AddEntity` movement bytes and diff against
   Sulfur's `lpVec3`. The zero-vector `0x00` path is trivially correct; the quantized
   path is the MEDIUM surface.
2. **`SetEntityData` v1 metadata** — confirm the empty-list `0xFF`-only body renders the
   `SulfurCube` in a real 26.2 client, or determine the minimal required entry.
3. **`AddEntity` type-id registry index** — confirm `SulfurCube`'s `data/entity.ID` matches
   the vanilla `ENTITY_TYPE` registry index on the wire (it is codegen-derived, so HIGH
   confidence, but the visual seals it).

Until 06-07 signs off, these three surfaces are jar-shape-correct but NOT byte-proven.

---

# ENT-04 Capture-Diff: Component-Slot ItemStack + HashedStack ContainerClick (Plan 06-05)

**Phase 6, Plan 06-05 — the jar-derived record for the inventory wire; byte-level seal deferred to Plan 06-07.**

This section records the jar-derived (`javap -p -c -classpath temp/cache/26.2-inner.jar`)
wire layouts for the component-based slot inventory (ENT-04): the **clientbound
component-slot `ItemStack`** (`ContainerSetContent`/`ContainerSetSlot`), and — the
load-bearing mis-framing risk (1.21.5+) — the **serverbound `HashedStack`** form used by
`ServerboundContainerClick`. The `HashedStack` is a CRC digest, NOT a full `ItemStack`;
decoding it as a full stack mis-frames the entire packet. The server is AUTHORITATIVE and
DISCARDS the hashes — it only needs to consume the bytes correctly and re-send authoritative
content.

## 1. Clientbound component-slot `ItemStack` (`SlotData`) — sealed shape, REUSED

The wire `ItemStack` (the same `level/component.SlotData`):

```
VarInt count            (count <= 0 => empty stack, STOP — nothing else follows)
VarInt itemId           (registry id; holderRegistry(Registries.ITEM) => plain VarInt)
VarInt addedCount
VarInt removedCount
addedCount   × ( VarInt componentTypeId + <component value> )
removedCount × ( VarInt componentTypeId )
```

`SlotData.WriteTo` was EXTENDED from the old `count+id+0+0` minimal form to be provably
inverse to `ReadFrom`: `ReadFrom` now tees the added+removed component byte span verbatim
into `RawComponents`, and `WriteTo` re-emits `count, id, addedCount, removedCount,
RawComponents`. A component-free stack (`RawComponents` empty, counts 0) still writes
exactly `count+id+0+0`. Proven by `TestSlotEncode` (empty / component-free / one-added all
round-trip byte-exact). Not the wiki — derived from the vanilla `ItemStack` StreamCodec.

## 2. Serverbound `HashedStack` — JAR-DERIVED (the mis-framing risk)

`net.minecraft.network.HashedStack.STREAM_CODEC = ByteBufCodecs.optional(ActualItem.STREAM_CODEC)`.
On the wire `optional(X)` is a **Boolean present-flag**, then if present the `X` body:

```
HashedStack:
  Boolean present
  if present:
    VarInt   itemId         (ActualItem.item: holderRegistry(Registries.ITEM) => VarInt)
    VarInt   count          (ActualItem.count: ByteBufCodecs.VAR_INT)
    HashedPatchMap components
```

`HashedStack$ActualItem.STREAM_CODEC` is a 3-field `StreamCodec.composite`:
`holderRegistry(Registries.ITEM)` (VarInt) + `ByteBufCodecs.VAR_INT` (count) +
`HashedPatchMap.STREAM_CODEC`.

`net.minecraft.network.HashedPatchMap.STREAM_CODEC` is a 2-field composite:

```
HashedPatchMap:
  addedComponents:   ByteBufCodecs.map(  key=registry(DATA_COMPONENT_TYPE) [VarInt],
                                         value=ByteBufCodecs.INT [FIXED 4-byte BE int = the CRC hash],
                                         maxSize=256 )
                     => VarInt count, then count × ( VarInt componentTypeId + Int32 hash )
  removedComponents: ByteBufCodecs.collection( element=registry(DATA_COMPONENT_TYPE) [VarInt],
                                               maxSize=256 )
                     => VarInt count, then count × ( VarInt componentTypeId )
```

**THE LOAD-BEARING FACT:** the added-component *value* is `ByteBufCodecs.INT` — a **fixed
4-byte big-endian int** (the component's CRC32 hash), NOT a component value. This is exactly
what distinguishes `HashedStack` from a full `ItemStack` (whose added value is the real
component payload). Decoding the added value as a component body (the old/full-stack shape)
mis-frames every subsequent byte. The minimum correct decode: for each added component,
consume `VarInt typeId + 4 raw bytes` and DISCARD them.

## 3. `ServerboundContainerClickPacket` — JAR-DERIVED 7-field composite

`STREAM_CODEC` is a `StreamCodec.composite` of 7 fields, in this exact order:

```
ServerboundContainerClick:
  1. containerId   ByteBufCodecs.CONTAINER_ID   (alias of VAR_INT => VarInt)
  2. stateId       ByteBufCodecs.VAR_INT        (VarInt)
  3. slotNum       ByteBufCodecs.SHORT          (Short, big-endian int16)
  4. buttonNum     ByteBufCodecs.BYTE           (Byte)
  5. containerInput ContainerInput.STREAM_CODEC  (idMapper => VarInt, enum 0..6)
  6. changedSlots  SLOTS_STREAM_CODEC           (map<Short slot -> HashedStack>, max 128)
                   => VarInt count, then count × ( Short slot + HashedStack )
  7. carriedItem   HashedStack.STREAM_CODEC     (HashedStack as in §2)
```

CONFIRMED CORRECTIONS vs. the research/wiki guess:
- field 5 is **`ContainerInput`** (a VarInt-encoded enum, jar name for the click-type),
  not a bare "clickType VarInt" — same VarInt on the wire, but jar-confirmed semantics.
- the changedSlots map key is a **Short** (`SLOTS_STREAM_CODEC` maps `ByteBufCodecs.SHORT`
  into the int key), NOT a VarInt slot — this is the easy-to-get-wrong framing.
- the map/collection length prefixes are **VarInt** counts (standard `ByteBufCodecs.map`/
  `.collection`).

Sulfur's `decodeHashedStack` / `handleContainerClick` consume exactly this layout and
discard the hashes; the server re-sends authoritative `ContainerSetContent` over `SlotData`.

## 4. Status / deferred to 06-07

JAR-DERIVED (this plan). The byte-level seal — a capture-diff of `ContainerSetContent`
against a real vanilla 26.2 inventory + a real `ServerboundContainerClick` round-trip from
a vanilla client — is deferred to Plan 06-07. The MEDIUM-confidence surfaces are:

1. The component-slot added-component *value* codecs (the 111 schemas) in `ContainerSetContent`
   — Sulfur captures/re-emits raw bytes (byte-exact by construction), but the per-schema
   value layout is only seal-proven when a real component-carrying stack is diffed.
2. The `HashedStack` `ByteBufCodecs.INT` fixed-4-byte hash width — jar-confirmed here;
   06-07 confirms a real client's click decodes without leftover/short bytes.

Until 06-07 signs off, the inventory wire is jar-shape-correct (no mis-framing) but the
component-value bytes are NOT yet byte-proven against a live client.
