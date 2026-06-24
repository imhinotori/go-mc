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

---

# ENT-01/04 BYTE-LEVEL SEAL — Sulfur vs. Vanilla 26.2 (Plan 06-07)

**Phase 6, Plan 06-07 — the authoritative entity/slot wire gate.**

This section records the byte-for-byte capture-diff of Sulfur's MEDIUM-confidence Phase-6
encoders against a REAL vanilla 26.2 server, captured for an identical superflat session.
It is the producer-side proof; the real-client interactive run (§Task 2 below) is the
consumer-side proof. The golden packet bytes are committed as fixtures under
`.planning/phases/06-entities-physics-interaction/fixtures/` so CI byte-diffs without
booting Java every run; `server/entity_capture_test.go`
(`TestEntityBytesVsVanillaCapture` + `TestSlotBytesVsVanillaCapture`) is the field-by-field
parser/asserter (it skips cleanly if a fixture is absent — the capture is the gate, not a
native-CI blocker).

Status (automatable half): **SEALED — the capture-diff confirmed Sulfur's four uncertain
entity/slot wire surfaces byte-for-byte against the vanilla golden with NO DIVERGENCE: the
`Vec3.LP` short-scaling movement, the `SetEntityData` `0xFF` framing, the
`ContainerSetContent` slot framing, and the `HashedStack` `ContainerClick` decode. The
real-client interactive run (Task 2) is the remaining BLOCKING human-verify gate.**

---

## 8. Capture Method (reproducible)

### Vanilla 26.2 server (the golden source)

- Jar: `temp/cache/26.2-server.jar`, run with Zulu 25.0.3 (`java -Xmx2G -jar server.jar nogui`).
- Scratch dir: `temp/vanilla-scratch/` (gitignored), `eula=true`, port `25599`,
  RCON enabled on `25575` (`rcon.password=sulfurcap`) to drive the capture.
- `server.properties` (the load-bearing keys):

  ```
  level-type=minecraft:flat
  level-seed=144
  online-mode=false
  enforce-secure-profile=false
  enable-code-of-conduct=false
  server-port=25599
  view-distance=4
  generate-structures=false
  gamemode=creative
  enable-rcon=true
  rcon.port=25575
  rcon.password=sulfurcap
  ```

  Vanilla's superflat default spawn lands the player at block `(0, -60, 0)`.

### Capture harness

`temp/entitycapture/` (gitignored, built on the fork's `net`/`bot`/`packetid`/`level/component`,
adapted from the proven Phase-5 `temp/playcapture/`): handshake (proto 776) → offline login →
full Configuration leg → **Play**, where it confirms the spawn teleport, paces chunk batches,
then DRIVES the vanilla server over RCON to:

- `execute at @p run summon pig ~ ~ ~ {Motion:[0.5,0.4,-0.3],NoAI:1b,PersistenceRequired:1b}`
  — a MOVING pig near the player so its `ClientboundAddEntity` carries a NON-ZERO `Vec3.LP`
  movement (the decisive LP short-scaling seal) + its `ClientboundSetEntityData`;
- `give @p minecraft:stone 1` — a known item into hotbar slot 36, captured as
  `ClientboundContainerSetSlot` (the non-empty slot framing).

It then SENDS one `ServerboundContainerClick` in the jar-derived 1.21.5+ `HashedStack` form
(the exact wire a vanilla client sends) — the vanilla server ACCEPTS it (no disconnect),
proving the framing is vanilla-valid — and commits those serverbound bytes as the click golden.

```
DUMP_DIR=temp/captures RCON_PASS=sulfurcap \
  temp/entitycapture/entitycapture.exe 127.0.0.1:25599 127.0.0.1:25575 EntCap vanilla
```

### Committed golden fixtures

| Fixture | Bytes | sha1 | Seals |
|---------|-------|------|-------|
| `vanilla-add-entity.bin` | 52 | `88c87788287a3118d748bdb60604865ccf039599` | AddEntity field order + `Vec3.LP` short scaling + byte angles |
| `vanilla-set-entity-data.bin` | 11 | `c8c079791581096abef8651b3716d99f5b7fd631` | SynchedEntityData indexed entries + `0xFF` terminator |
| `vanilla-container-set-content.bin` | 50 | `8f1588840344c2f18938bf024ba81e3363b47fbd` | list count-prefix + per-slot empty framing + carried framing |
| `vanilla-container-set-slot.bin` | 8 | `740685332e2c5ddbc7fd8ad95601004f587f7d22` | non-empty slot `count,itemId,addedCount,removedCount` |
| `vanilla-container-click.bin` | 15 | `e5b9973e597716bec22c86bba881971b4f6fda36` | serverbound `HashedStack` `ContainerClick` decode |

---

## 9. Per-Field Diff (the byte-level seal)

Legend: **MATCH** = byte-structure identical; **SEALED** = byte-identical for identical
inputs; **CONTENT** = framing identical, value differs (expected — different entity/inventory).

### 9.1 `ClientboundAddEntity` — the `Vec3.LP` short-scaling seal (ENT-01, Open Question 1)

Vanilla golden (a pig, type 100, summoned with `Motion:[0.5,0.4,-0.3]`):

```
02   08104c9a…275d   64   …x… …y… …z…   f9ff59996662   00 00 00   00
└id┘ └─uuid 16B────┘ └100┘ └3×Double pos┘ └─LP movement─┘ └3 angles┘ └data┘
```

| Field | Vanilla | Sulfur (same inputs) | Verdict |
|-------|---------|----------------------|---------|
| field order | id, uuid, **typeId(field 3)**, x,y,z, LP, xRot,yRot,yHeadRot, data | identical | **MATCH** |
| **`Vec3.LP` movement** for `[0.5,0.4,-0.3]` | `f9 ff 59 99 66 62` | `f9 ff 59 99 66 62` | **SEALED — byte-identical** |
| byte angles | 3 × `0x00` | 3 × `0x00` | MATCH |
| whole body | 52 bytes, 0 trailing | byte-identical to golden | **SEALED** |

**The decisive seal:** Sulfur's `lpVec3` quantizer (the full `LpVec3.write` algorithm —
sanitize → absMax → `ceilLong` scale → 15-bit pack per axis → header+2 bytes + big-endian
Int) produces `f9ff59996662` for `Motion [0.5, 0.4, -0.3]`, **byte-identical** to vanilla's.
Feeding `encodeAddEntity` the same id/uuid/type/pos/velocity yields a body **byte-identical**
to the 52-byte vanilla golden (`bytes.Equal`). The MEDIUM-confidence LP short-scaling (the
`×8000`-replacement quantizer) is **SEALED, no divergence**. (Open Question 1 resolved.)

### 9.2 `ClientboundSetEntityData` — the `0xFF` framing seal (ENT-01, Open Question 2)

Vanilla golden (the pig's metadata): `3b  09 03 41200000  0f 00 01  ff`

```
3b      id (VarInt)
09 03   index=9, serializerId=3 (Float)  value=41200000 → health 10.0
0f 00   index=15, serializerId=0 (Byte)  value=01       → pig data-flags
ff      EOF_MARKER terminator
```

| Field | Vanilla pig | Sulfur v1 entity | Verdict |
|-------|-------------|------------------|---------|
| leading id | VarInt | VarInt | MATCH |
| entry framing | `Byte index, VarInt serializerId, value` | identical (splice path) | **MATCH** |
| **`0xFF` terminator** | always-present trailing `0xFF` | always-present trailing `0xFF` | **SEALED** |
| metadata SET | health(9) + data-flags(15) | EMPTY (just `0xFF`) | CONTENT (type-specific) |

**The decisive seal:** the SynchedEntityData framing is `VarInt id`, zero-or-more
`Byte index / VarInt serializerId / value` entries, then the **mandatory single `0xFF`
terminator** — confirmed by the vanilla bytes. Sulfur's empty-list body is exactly
`VarInt id + 0xFF` (the same terminator vanilla closes its non-empty list with), and when
Sulfur's `Entity.metadata` carries the pig's exact entry bytes, `encodeSetEntityData`
re-emits a body **byte-identical** to the golden (`bytes.Equal`). **The terminator framing is
SEALED, no divergence.**

**Open Question 2 (does a v1 entity need non-default metadata to render?) — resolved on the
producer side, with the visual deferred to Task 2:** the capture proves a vanilla mob DOES
carry type-specific metadata (a pig sends `health=10` + a data-flags byte), but that metadata
is *type data*, not a *render gate* — a client renders an entity from its **type id** in
`AddEntity`, and the `SetEntityData` entries only set per-instance attributes (health bar of
the entity, baby/adult, etc.). For Sulfur's v1 path the **empty `0xFF`-only metadata is
structurally correct**; the open render risk is therefore NOT the metadata but the **entity
type id** Sulfur spawns. The Sulfur `SulfurCube` is a CUSTOM type (id 130) a vanilla client
has **no renderer for**, so the 06-07 debug spawn uses a **vanilla-renderable pig (type 100)**
for the interactive check — that is what Task 2 must visually confirm appears and moves. If a
future Sulfur entity needs a specific shared-flags entry to look right (e.g. not invisible),
the splice path (proven byte-identical above) ships it with no framing change.

### 9.3 `ClientboundContainerSetContent` + `ContainerSetSlot` — the slot framing seal (ENT-04)

Vanilla `ContainerSetContent` golden (player inventory, container 0): `00 01 2e  <46×empty> <carried>`
— containerId 0, stateId 1, list count `0x2e`=46, every slot the single-byte empty form
(`count<=0`), then the 1-byte empty carried item. 50 bytes, 0 trailing.

Vanilla `ContainerSetSlot` golden (the `/give`'d stone in slot 36):
`00 02 0024  01 01 00 00` — container 0, state 2, slot 36, then `SlotData` `count=1, itemId=1
(stone), addedCount=0, removedCount=0`.

| Field | Vanilla | Sulfur (same items) | Verdict |
|-------|---------|---------------------|---------|
| containerId / stateId | VarInt / VarInt | identical | MATCH |
| **list count-prefix** | VarInt `46` | VarInt `46` | **SEALED** |
| per-slot EMPTY | single `0x00` | single `0x00` | **SEALED** |
| **carried** item framing | trailing `SlotData` | identical | **SEALED** |
| non-empty slot `count,itemId,added,removed` | `01 01 00 00` | `01 01 00 00` | **SEALED** |

**The decisive seal:** feeding `containerSetContent` the slots+carried parsed from the vanilla
golden yields a body **byte-identical** to the 50-byte golden (`bytes.Equal`); feeding
`containerSetSlot` the parsed stone yields a body **byte-identical** to the `ContainerSetSlot`
golden. The component-slot `SlotData` codec (count → empty stop, else `count,itemId,added,
removed` + verbatim component span) matches vanilla for both the empty and the non-empty
(stone) stack. **SEALED, no divergence.**

### 9.4 `ServerboundContainerClick` — the `HashedStack` decode seal (ENT-04, Open Question 3)

Vanilla-form serverbound golden (a left-click pickup of slot 36):
`00 02 0024 00 00  01 0024 00  01 0101 0000` —
containerId 0, stateId 2, slotNum 36, button 0, containerInput 0 (PICKUP), changedSlots map
{count 1, slot 36 → empty `HashedStack`}, carried `HashedStack`{present, itemId 1, count 1,
0 added, 0 removed}.

| Step | Result |
|------|--------|
| walk the 7-field composite with Sulfur's `decodeHashedStack` for both `HashedStack` bodies | consumes to **exactly 0 trailing bytes** |
| full dispatch (`applyInput`) of the real click | no panic / no mis-frame; server discards hashes, re-sends authoritative content |
| vanilla server's reaction to the same framing | **ACCEPTED — no disconnect** (the framing is vanilla-valid) |

**The decisive seal:** Sulfur's `decodeHashedStack` (Boolean present → VarInt itemId, VarInt
count, `HashedPatchMap`: VarInt addedCount × `(VarInt typeId + fixed Int32 hash)`, VarInt
removedCount × VarInt typeId) consumes the real vanilla serverbound click to **exactly zero
trailing bytes** — the `ByteBufCodecs.INT` fixed-4-byte hash width and the Short map-key are
correct, no mis-framing. **Open Question 3 (HashedStack decode depth) resolved:** the decoder
consumes exactly the jar-derived framing with no leftover/short bytes, and the vanilla server
accepts Sulfur's identically-framed click. **SEALED, no divergence.**

---

## 10. The Open Questions — Resolved

1. **`Vec3.LP` short scaling** — **RESOLVED/SEALED.** Sulfur's `lpVec3` is byte-identical to
   vanilla's `LpVec3.write` for `[0.5,0.4,-0.3]` → `f9ff59996662` (§9.1). The
   `×8000`-replacement quantizer is correct.
2. **Does a v1 entity need non-default metadata to render?** — **RESOLVED on the producer
   side; visual deferred to Task 2.** Metadata is type *data*, not a render *gate* — render
   keys off the AddEntity **type id**. The empty `0xFF` path is structurally correct; the real
   render risk is the type id, so the interactive check uses a vanilla-renderable **pig (type
   100)**, NOT the custom `SulfurCube` (type 130) a vanilla client cannot draw (§9.2). Task 2
   confirms the pig is visible and moves.
3. **`HashedStack` decode depth** — **RESOLVED/SEALED.** The real vanilla click decodes to
   exactly zero trailing bytes and the vanilla server accepts Sulfur's identically-framed
   click (§9.4).
4. **Physics faithfulness scope** — **RESOLVED: visible behaviors only (06-RESEARCH A1).** v1
   requires the VISIBLE behaviors (an entity lands on the floor / `onGround`; a player cannot
   clip through stone) — confirmed structurally by `physics_test.go` and observable in Task 2 —
   NOT exact vanilla constant-for-constant parity (gravity/drag/friction are tunable,
   wire-irrelevant constants). Exact-constant parity is explicitly NOT a v1 requirement.

---

## 11. Deviations from Plan

**None — the four uncertain encoders already matched vanilla byte-for-byte; no encoder fix
was needed.** Sulfur's `entity_encode.go` (AddEntity / SetEntityData / lpVec3),
`slot_encode.go` (containerSetContent / containerSetSlot / decodeHashedStack), and
`level/component/types.go` (SlotData) all produced/consumed wire byte-identical to the vanilla
26.2 golden. The only new code is the test (`server/entity_capture_test.go`), the committed
fixtures, and the OFF-by-default debug triggers (`server/debug.go` + the `tickEntities` hook +
the `SULFUR_DEBUG` wiring in `cmd/sulfur/main.go`) the interactive gate needs.

---

## 12. Gate Results (automatable half — all green)

| Gate | Command | Result |
|------|---------|--------|
| Capture-diff | `go test ./server/ -run 'TestEntityBytesVsVanillaCapture\|TestSlotBytesVsVanillaCapture' -count=1` | PASS (all subtests; Sulfur framing == vanilla golden, no divergence) |
| Owning-plan wire tests | `go test ./server/ -run 'TestAddEntityWire\|TestSetEntityDataWire\|TestContainerSetContent\|TestContainerClickDecode' -count=1` | PASS |
| Full server suite | `go test ./server/...` | PASS |
| Build + vet | `go build ./...`, `go vet ./...` | clean |
| `-race` (Docker) | `docker run … golang:1.26 go test -race ./server/... ./world/... ./level/... ./save/...` | **PASS — race-clean under load (Plans 06-01..06 + the debug triggers)** |
| Debug triggers live | join `cmd/sulfur` (SULFUR_DEBUG=1): pig (type 100) spawns + MOVES (TeleportEntity), health bar steps 20→0, `PlayerCombatKill` (death screen) fires | PASS (verified via `temp/verifydebug`) |

Zero new dependencies.

---

## 13. Interactive Gate Trigger (how the operator drives Task 2)

`cmd/sulfur` ships an OFF-by-default debug harness for the interactive human-verify. Start
the server with `SULFUR_DEBUG=1`:

```
SULFUR_DEBUG=1 sulfur -addr :25565
```

This arms (logged at startup as `SULFUR_DEBUG=1: debug entity-spawn + periodic damage
triggers ARMED`):

- a **visible, vanilla-renderable pig (type 100)** that spawns near spawn and **PACES** back
  and forth (the `entityTracker`'s Add / TeleportEntity / Remove — ENT-01/02), and
- **periodic damage** (2 HP every ~2s) so the on-screen **health bar drops**, the **death
  screen** appears at 0 HP, and clicking **Respawn** runs `performRespawn` (re-teleport + full
  world re-stream — ENT-05/06).

Without `SULFUR_DEBUG=1` a normal `sulfur` run is completely unaffected (the debug hooks are a
nil-check no-op, no extra entity, no damage).

---

## Task 2 — Real vanilla 26.2 client INTERACTS (BLOCKING human-verify)

> The decisive ENT-01..05 milestone cannot be self-approved.

PENDING — the orchestrator presents this gate to the operator. Connect an **unmodified vanilla
26.2 client (PrismLauncher)** to `cmd/sulfur` (`SULFUR_DEBUG=1`) and confirm:

- **a** ENTITY VISIBLE: the debug pig appears and PACES (the tracker's Add/Move/Remove).
- **b** PLACE/BREAK: place a block and SEE it persist; break one and SEE it vanish — NO
  ghost / no snap-back on a valid edit (Plan 06-04's reconciliation).
- **c** HEALTH BAR: the on-screen health bar DROPS as the periodic damage lands (Plan 06-06's
  SetHealth).
- **d** DEATH/RESPAWN: at 0 HP the death screen appears; clicking Respawn returns the player
  to a streamed, walkable world (Plan 06-06's Respawn).
- **e** NO KICK / NO HANG: no malformed-packet kick, no "Loading terrain…" hang.

**Sign-off:**

> Reviewed by: ____   Date: ____
> Result: [ ] entity visible+moves / place-break / health bar / death-respawn — APPROVED
