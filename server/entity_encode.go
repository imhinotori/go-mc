package server

import (
	"bytes"
	"io"
	"math"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// entity_encode.go holds the JAR-DERIVED proto-776 clientbound ENTITY encoders the
// synchronous entityTracker (tracker.go) emits: AddEntity, SetEntityData, the three
// MoveEntity* deltas, TeleportEntity, RotateHead, SetEntityMotion, and RemoveEntities.
//
// ALL wire layouts are decompiled from the unobfuscated 26.2 inner jar
// (temp/cache/26.2-inner.jar, net.minecraft.network.protocol.game.* + the LpVec3 /
// SynchedEntityData sub-codecs), NOT guessed from the (<=773) community wiki. The two
// MEDIUM-confidence surfaces — the Vec3.LP_STREAM_CODEC movement quantizer and the
// SynchedEntityData 0xFF-terminated indexed-entry framing — are recorded byte-by-byte in
// .planning/phases/06-entities-physics-interaction/06-CAPTURE-DIFF.md and BYTE-SEALED
// against a real vanilla 26.2 server + a real-client visual by Plan 06-07. The fork's
// pk.* codecs are symmetric, so the self-round-trip tests here are regression-only — they
// do NOT prove byte-identity with vanilla; that is 06-07's job.

// --- Vec3.LP_STREAM_CODEC quantizer (jar: net.minecraft.network.LpVec3) ---------------
//
// 26.x replaced the legacy "movement as 3 shorts (velocity*8000 clamped)" with a
// variable-width quantized codec. lpVec3 implements LpVec3.write EXACTLY (06-CAPTURE-DIFF
// §3): sanitize → absMax → ceilLong scale → 15-bit pack per axis → header + 6 bytes
// (+ optional VarInt scale-extension). For the common ZERO velocity it emits a single
// 0x00 byte, which is the only path the v1 tracker exercises (spawn is stationary;
// position is broadcast via TeleportEntity, not velocity). The full algorithm is
// implemented so SetEntityMotion (and any non-zero AddEntity movement) is correct too.

const (
	// lpAbsMinValue is LpVec3.ABS_MIN_VALUE: below this the vector is treated as zero and
	// the codec emits a single 0x00 byte (the stationary-entity fast path).
	lpAbsMinValue = 3.051944088384301e-5
	// lpAbsMaxValue is LpVec3.ABS_MAX_VALUE: sanitize clamps each component to ±this.
	lpAbsMaxValue = 1.7179869183e10
	// lpMaxQuantized is LpVec3.MAX_QUANTIZED_VALUE: pack maps [-1,1] onto [0, 32766].
	lpMaxQuantized = 32766.0
	// lpContinuationFlag is bit 2 of the header byte: set when the scale needs more than 2
	// bits, in which case the high scale bits trail as a VarInt.
	lpContinuationFlag = 4
)

// lpSanitize mirrors LpVec3.sanitize: NaN becomes 0, everything else is clamped to the
// representable ±ABS_MAX range.
func lpSanitize(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < -lpAbsMaxValue {
		return -lpAbsMaxValue
	}
	if v > lpAbsMaxValue {
		return lpAbsMaxValue
	}
	return v
}

// lpPack mirrors LpVec3.pack: round((d*0.5 + 0.5) * 32766) — maps a normalized component
// in [-1,1] to the 15-bit quantized integer [0, 32766].
func lpPack(d float64) int64 {
	return int64(math.Round((d*0.5 + 0.5) * lpMaxQuantized))
}

// lpVec3 is the FieldEncoder for a Vec3 under LP_STREAM_CODEC, byte-identical to
// LpVec3.write (06-CAPTURE-DIFF §3). A zero/near-zero vector writes a single 0x00 byte;
// otherwise it writes header+2 bytes + a big-endian Int (the low 48 bits of the packed
// long, minus the 16 already written), then optionally a VarInt of the high scale bits.
type lpVec3 struct{ x, y, z float64 }

func (m lpVec3) WriteTo(w io.Writer) (int64, error) {
	x := lpSanitize(m.x)
	y := lpSanitize(m.y)
	z := lpSanitize(m.z)

	// absMax(absMax(x,y),z): the largest absolute component decides the scale.
	mx := math.Max(math.Abs(x), math.Abs(y))
	mx = math.Max(mx, math.Abs(z))

	if mx < lpAbsMinValue {
		// Stationary/zero vector: a single 0x00 byte (the v1 spawn path).
		return pk.UnsignedByte(0).WriteTo(w)
	}

	scale := int64(math.Ceil(mx)) // LpVec3 ceilLong: smallest long >= mx
	cont := (scale & 3) != scale  // does the scale need more than 2 bits?

	var hdr int64
	if cont {
		hdr = (scale & 3) | lpContinuationFlag
	} else {
		hdr = scale
	}

	fscale := float64(scale)
	px := lpPack(x/fscale) << 3
	py := lpPack(y/fscale) << 18
	pz := lpPack(z/fscale) << 33
	combined := hdr | px | py | pz

	var n int64
	// byte(combined), byte(combined>>8), writeInt(combined>>16) — the jar emits two raw
	// bytes then a big-endian 4-byte int covering bits 16..47.
	if c, err := pk.UnsignedByte(combined & 0xFF).WriteTo(w); err != nil {
		return n + c, err
	} else {
		n += c
	}
	if c, err := pk.UnsignedByte((combined >> 8) & 0xFF).WriteTo(w); err != nil {
		return n + c, err
	} else {
		n += c
	}
	if c, err := pk.Int(int32(combined >> 16)).WriteTo(w); err != nil {
		return n + c, err
	} else {
		n += c
	}
	if cont {
		// VarInt of the high scale bits (scale >> 2).
		if c, err := pk.VarInt(int32(scale >> 2)).WriteTo(w); err != nil {
			return n + c, err
		} else {
			n += c
		}
	}
	return n, nil
}

// degToByteAngle converts a rotation in degrees to the 1/256-turn byte angle the entity
// rotation fields use (xRot/yRot/yHeadRot), matching the vanilla Mth byte-angle
// convention: round(deg * 256 / 360) truncated into an int8/pk.Angle (06-CAPTURE-DIFF §6).
func degToByteAngle(deg float32) pk.Angle {
	return pk.Angle(int8(math.Round(float64(deg) * 256.0 / 360.0)))
}

// packDegrees is net.minecraft.util.Mth.packDegrees(float) = (byte)Mth.floor(deg * 256 / 360).
// The delta-move tracking (ServerEntity.sendChanges) uses THIS (floor, not round) both for the
// rotation-change threshold compare against lastSent*Rot AND for the byte written on the wire,
// so the server's idea of "the angle the client has" matches what vanilla would send. (The
// AddEntity/Teleport encoders keep degToByteAngle for backward compatibility; the move-delta
// path is the one that must be floor-exact to track correctly tick-over-tick.)
//   [VERIFIED javap: Mth.packDegrees -> fmul 256; fdiv 360; floor; i2b.]
func packDegrees(deg float32) int8 {
	return int8(mthFloorF(float64(deg) * 256.0 / 360.0))
}

// mthFloorF is Mth.floor for a float argument (Math.floor then cast). A small local to avoid a
// dependency on the worldgen floor helper; matches `(int)Math.floor(d)`.
func mthFloorF(d float64) int { return int(math.Floor(d)) }

// --- Task 1 encoders ------------------------------------------------------------------

// encodeAddEntity builds ClientboundAddEntity for a newly-visible entity. JAR-DERIVED
// field order (06-CAPTURE-DIFF §2): VarInt id, UUID, VarInt typeId (registry codec,
// BEFORE x/y/z), Double x/y/z, LP movement, Byte xRot/yRot/yHeadRot, VarInt data. The
// movement uses the LP quantizer (a single 0x00 for a stationary spawn), NOT three
// Doubles and NOT the legacy short triple.
func encodeAddEntity(e *Entity) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundAddEntity),
		pk.VarInt(e.id),           // 1: entity id
		pk.UUID(e.uuid),           // 2: object UUID
		pk.VarInt(int32(e.typ)),   // 3: entity type id (registry index)
		pk.Double(e.x),            // 4: x
		pk.Double(e.y),            // 5: y
		pk.Double(e.z),            // 6: z
		lpVec3{e.vx, e.vy, e.vz},  // 7: movement (LP_STREAM_CODEC)
		degToByteAngle(e.pitch),   // 8: xRot
		degToByteAngle(e.yaw),     // 9: yRot
		degToByteAngle(e.headYaw), // 10: yHeadRot
		pk.VarInt(0),              // 11: data (object-specific; 0 for a plain mob)
	)
}

// entityDataEntry is one SynchedEntityData$DataValue on the wire (06-CAPTURE-DIFF §4):
// Byte index, VarInt serializerId, then the serializer-specific value bytes. v1 emits no
// entries (the empty-list path), but the type exists so 06-07 can add the single
// shared-flags entry if the real-client visual requires it.
type entityDataEntry struct {
	index        uint8
	serializerID int32
	value        pk.FieldEncoder
}

func (e entityDataEntry) WriteTo(w io.Writer) (int64, error) {
	var n int64
	if c, err := pk.UnsignedByte(e.index).WriteTo(w); err != nil {
		return n + c, err
	} else {
		n += c
	}
	if c, err := pk.VarInt(e.serializerID).WriteTo(w); err != nil {
		return n + c, err
	} else {
		n += c
	}
	if e.value != nil {
		if c, err := e.value.WriteTo(w); err != nil {
			return n + c, err
		} else {
			n += c
		}
	}
	return n, nil
}

// --- GAMEPLAY-06: the Item entity ITEM data-value -------------------------------------
//
// A dropped Item entity renders NOTHING unless its SynchedEntityData carries the ITEM
// stack value (06-RESEARCH Pitfall 5). Unlike a mob (which renders with an empty metadata
// body), the client reads ItemEntity.DATA_ITEM to know WHICH item model to draw — an empty
// body spawns an invisible entity. The two wire numbers below are JAR-DERIVED (javap'd this
// session from temp/cache/26.2-inner.jar), NOT guessed.

// dataItemIndex is the SynchedEntityData accessor index for ItemEntity.DATA_ITEM.
// SynchedEntityData.defineId assigns indices sequentially per class hierarchy starting at
// the superclass count. ItemEntity extends net.minecraft.world.entity.Entity DIRECTLY, and
// Entity defines 8 base accessors (indices 0..7: BYTE flags, INT air, OPTIONAL_COMPONENT
// custom-name, BOOLEAN name-visible, BOOLEAN silent, BOOLEAN no-gravity, POSE, INT ticks-
// frozen). So ItemEntity.DATA_ITEM — the FIRST (and only) accessor ItemEntity defines — is
// index 8.
//   [VERIFIED: javap -c -p net.minecraft.world.entity.item.ItemEntity →
//     static{}: getstatic EntityDataSerializers.ITEM_STACK; SynchedEntityData.defineId(...)
//     → putstatic DATA_ITEM; defineSynchedData defines ONLY DATA_ITEM.
//    javap -c -p net.minecraft.world.entity.Entity → 8 SynchedEntityData.defineId calls.]
const dataItemIndex uint8 = 8

// itemStackSerializerID is the registry id of EntityDataSerializers.ITEM_STACK — the VarInt
// serializerId the DataValue carries. The id is the registration order in the
// EntityDataSerializers static initializer: 0=BYTE, 1=INT, 2=LONG, 3=FLOAT, 4=STRING,
// 5=COMPONENT, 6=OPTIONAL_COMPONENT, 7=ITEM_STACK. The ITEM_STACK serializer's codec is
// ItemStack.OPTIONAL_STREAM_CODEC — the SAME stream codec ContainerSetContent's carried item
// uses, so component.SlotData's WriteTo is the correct value encoder (no new item codec).
//   [VERIFIED: javap -c -p net.minecraft.network.syncher.EntityDataSerializers → static{}
//     registerSerializer order; EntityDataSerializers$1.codec() = ItemStack.OPTIONAL_STREAM_CODEC.]
const itemStackSerializerID int32 = 7

// itemDataEntry builds the single SynchedEntityData$DataValue entry that carries a dropped
// item's stack, so the Item entity renders its model instead of spawning invisible. The
// value reuses the component.SlotData ItemStack codec (the same framing ContainerSetContent
// emits) — NOT a hand-rolled item stream. The returned entry frames on the wire as
// Byte(dataItemIndex) + VarInt(itemStackSerializerID) + the ItemStack body (entityDataEntry.WriteTo).
func itemDataEntry(stack component.SlotData) entityDataEntry {
	return entityDataEntry{
		index:        dataItemIndex,
		serializerID: itemStackSerializerID,
		value:        &stack, // *SlotData implements pk.FieldEncoder (ItemStack.OPTIONAL_STREAM_CODEC)
	}
}

// --- GAMEPLAY-17 (Plan 17-18): the DATA_AIR_SUPPLY_ID data-value -----------------------
//
// The OXYGEN BUBBLE BAR is driven ENTIRELY by the player's synched DATA_AIR_SUPPLY_ID field —
// the server is authoritative for air and PUSHES it to the client via SetEntityData (the client
// does NOT locally simulate air in multiplayer). Plan 17-13 decremented airSupply server-side but
// never sent it, so the bar never moved. This entry is the missing wire piece. Both numbers below
// are JAR-DERIVED (javap'd from temp/cache/26.2-inner.jar this session), NOT guessed.

// dataAirSupplyIndex is the SynchedEntityData accessor index for Entity.DATA_AIR_SUPPLY_ID.
// Entity.defineId assigns indices sequentially in static-init order: index 0 = DATA_SHARED_FLAGS_ID
// (BYTE), index 1 = DATA_AIR_SUPPLY_ID (INT), 2 = DATA_CUSTOM_NAME, ... 7 = DATA_TICKS_FROZEN. So
// air is index 1 — the SECOND accessor Entity defines and the first INT one.
//   [VERIFIED: javap -c -p net.minecraft.world.entity.Entity → static{} defineId order:
//     getstatic EntityDataSerializers.BYTE; defineId → DATA_SHARED_FLAGS_ID  (index 0)
//     getstatic EntityDataSerializers.INT;  defineId → DATA_AIR_SUPPLY_ID    (index 1)]
const dataAirSupplyIndex uint8 = 1

// intSerializerID is the registry id of EntityDataSerializers.INT — the VarInt serializerId the
// DataValue carries. The id is the registerSerializer() call order in the EntityDataSerializers
// static initializer: 0=BYTE, 1=INT, 2=LONG, 3=FLOAT, 4=STRING, 5=COMPONENT, 6=OPTIONAL_COMPONENT,
// 7=ITEM_STACK (the same order itemStackSerializerID==7 above is derived from). The INT serializer's
// value codec is EntityDataSerializer.forValueType(ByteBufCodecs.VAR_INT), so the air value is
// written as a VarInt (NOT a fixed big-endian Int).
//   [VERIFIED: javap -c -p net.minecraft.network.syncher.EntityDataSerializers → static{}:
//     getstatic BYTE; registerSerializer (id 0), getstatic INT; registerSerializer (id 1), ...
//     and BYTE/INT = forValueType(ByteBufCodecs.BYTE / .VAR_INT).]
const intSerializerID int32 = 1

// airDataEntry builds the single SynchedEntityData$DataValue entry that carries a player's
// DATA_AIR_SUPPLY_ID, the field the client's bubble bar reads. The value is the int air supply
// written through the INT serializer's VAR_INT codec (pk.VarInt). It frames on the wire as
// Byte(dataAirSupplyIndex=1) + VarInt(intSerializerID=1) + VarInt(air) (entityDataEntry.WriteTo).
func airDataEntry(air int32) entityDataEntry {
	return entityDataEntry{
		index:        dataAirSupplyIndex,
		serializerID: intSerializerID,
		value:        pk.VarInt(air), // EntityDataSerializers.INT codec == ByteBufCodecs.VAR_INT
	}
}

// --- GAMEPLAY-07: the DATA_LIVING_ENTITY_FLAGS data-value (eat/use pose) -----------------
//
// The 3rd-person EATING/USING pose (the arm-raise-to-mouth animation observers see) is driven by
// the player's synched DATA_LIVING_ENTITY_FLAGS byte: bit 0x01 = IS_USING_ITEM, bit 0x02 = the
// active hand is the OFF_HAND, bit 0x04 = spin attack. startUsingItem sets bit 0x01 (+0x02 for
// the off hand); stopUsingItem clears them. The server is authoritative and PUSHES the flag via
// SetEntityData; without it observers never see the eat pose (the eater predicts it locally).
//
// dataLivingEntityFlagsIndex is the SynchedEntityData accessor index for LivingEntity
// .DATA_LIVING_ENTITY_FLAGS. Entity defines indices 0..7 (0=SHARED_FLAGS, 1=AIR_SUPPLY, ...,
// 7=TICKS_FROZEN); LivingEntity's FIRST defined field is DATA_LIVING_ENTITY_FLAGS, so it is
// index 8 (a BYTE).
//   [VERIFIED javap: net.minecraft.world.entity.LivingEntity static{} -> the first defineId is
//    EntityDataSerializers.BYTE -> DATA_LIVING_ENTITY_FLAGS, after Entity's 8 fields (0..7).]
const dataLivingEntityFlagsIndex uint8 = 8

// byteSerializerID is the registry id of EntityDataSerializers.BYTE — 0 (the first
// registerSerializer call). Its value codec is ByteBufCodecs.BYTE (a single signed byte).
//   [VERIFIED javap: EntityDataSerializers static{} -> BYTE registered first (id 0).]
const byteSerializerID int32 = 0

// livingEntityFlag bit masks (LivingEntity.setLivingEntityFlag arg = the MASK, not a bit index):
//   USING_ITEM = 0x01, OFFHAND active hand = 0x02, SPIN_ATTACK = 0x04 (v1 sets only 0x01/0x02).
//   [VERIFIED javap: startUsingItem -> setLivingEntityFlag(1,true), setLivingEntityFlag(2,
//    hand==OFF_HAND); the flag is OR'd/AND-NOT'd into the byte.]
const (
	livingFlagUsingItem  = 0x01
	livingFlagOffHandUse = 0x02
)

// livingEntityFlagsEntry builds the SynchedEntityData$DataValue entry for
// DATA_LIVING_ENTITY_FLAGS: Byte(index=8) + VarInt(byteSerializerID=0) + Byte(flags). The BYTE
// serializer's value is a single signed byte (pk.Byte), mirroring airDataEntry's INT pattern.
func livingEntityFlagsEntry(flags int8) entityDataEntry {
	return entityDataEntry{
		index:        dataLivingEntityFlagsIndex,
		serializerID: byteSerializerID,
		value:        pk.Byte(flags), // EntityDataSerializers.BYTE codec == ByteBufCodecs.BYTE
	}
}

// dataPlayerModeCustomisationIndex is the SynchedEntityData accessor index for
// Avatar.DATA_PLAYER_MODE_CUSTOMISATION (the displayed-skin-parts bitmask). defineId assigns indices
// sequentially down the class hierarchy: Entity 0..7 (8), LivingEntity 8..14 (7), Avatar 15
// (DATA_PLAYER_MAIN_HAND) then 16 (DATA_PLAYER_MODE_CUSTOMISATION). So the index is 16, BYTE
// serializer. The client reads this to decide which skin LAYERS (hat/jacket/sleeves/pants) to render
// on the avatar; without it OTHER players see the base model only (no second/overlay layer).
//   [VERIFIED javap: net.minecraft.world.entity.player.Avatar DATA_PLAYER_MODE_CUSTOMISATION =
//    EntityDataAccessor<Byte>; index 16 after Entity(8)+LivingEntity(7)+Avatar.MAIN_HAND(15).]
const dataPlayerModeCustomisationIndex uint8 = 16

// skinCustomisationEntry builds the SynchedEntityData$DataValue entry for
// DATA_PLAYER_MODE_CUSTOMISATION: Byte(index=16) + VarInt(byteSerializerID=0) + Byte(parts). parts is
// the client's displayed-skin-parts bitmask (bit1 jacket, bit2/3 sleeves, bit4/5 pants legs, bit6
// hat). 1:1 port of ServerPlayer.updateOptions -> entityData.set(DATA_PLAYER_MODE_CUSTOMISATION,
// (byte) info.modelCustomisation()).
func skinCustomisationEntry(parts uint8) entityDataEntry {
	return entityDataEntry{
		index:        dataPlayerModeCustomisationIndex,
		serializerID: byteSerializerID,
		value:        pk.Byte(parts),
	}
}

// playerSkinMetadata returns the pre-built SynchedEntityData entry bytes for a player's displayed
// skin parts (the DATA_PLAYER_MODE_CUSTOMISATION value), to be carried on Entity.metadata so every
// AddEntity-time SetEntityData a tracker sends includes it. Returns nil when parts is 0 (the client
// has not reported its preference yet) so the player renders the vanilla default until it does.
func playerSkinMetadata(parts uint8) []byte {
	if parts == 0 {
		return nil
	}
	var buf bytes.Buffer
	_, _ = skinCustomisationEntry(parts).WriteTo(&buf)
	return buf.Bytes()
}

// --- MOB-SUB-08 (Plan 33-01): the DATA_BABY_ID data-value (the baby render flag) ----------
//
// A baby AgeableMob (breedAge < 0) renders SMALL client-side, driven ENTIRELY by its synched
// DATA_BABY_ID boolean — the server is authoritative and PUSHES it (the client does not derive
// "baby" locally). This is a SEPARATE deliverable from the half-scale HITBOX (the server-side AABB
// shrink, entity.go refreshDimensions): the hitbox drives the goal distSqr checks; DATA_BABY_ID is
// only the client render. Both numbers below are JAR-DERIVED (javap'd from temp/cache/26.2-inner.jar
// this session), NOT guessed.

// dataBabyIndex is the SynchedEntityData accessor index for AgeableMob.DATA_BABY_ID. defineId assigns
// indices sequentially down the class hierarchy: Entity 0..7 (8), LivingEntity 8..14 (7), Mob 15
// (DATA_MOB_FLAGS_ID), AgeableMob 16 = DATA_BABY_ID (then 17 = AGE_LOCKED). So the index is 16, BOOLEAN
// serializer. The client renders a small pig when this is true.
//   [VERIFIED javap: net.minecraft.world.entity.AgeableMob static{} -> defineSynchedData defines
//     DATA_BABY_ID FIRST (BOOLEAN) then AGE_LOCKED (BOOLEAN); the hierarchy count Entity(8)+
//     LivingEntity(7)+Mob.DATA_MOB_FLAGS_ID(15) puts DATA_BABY_ID at accessor index 16.]
const dataBabyIndex uint8 = 16

// boolSerializerID is the registry id of EntityDataSerializers.BOOLEAN — the VarInt serializerId the
// DataValue carries. The id is the registerSerializer() call ORDER in the EntityDataSerializers static
// initializer: 0=BYTE, 1=INT, 2=LONG, 3=FLOAT, 4=STRING, 5=COMPONENT, 6=OPTIONAL_COMPONENT,
// 7=ITEM_STACK, 8=BOOLEAN (the same registration order itemStackSerializerID==7 / intSerializerID==1
// are derived from). The BOOLEAN serializer's value codec is forValueType(ByteBufCodecs.BOOL) — a
// single byte 0/1 (pk.Boolean).
//   [VERIFIED javap: net.minecraft.network.syncher.EntityDataSerializers static{} registerSerializer
//     sequence — getstatic BYTE;register (id0) INT(1) LONG(2) FLOAT(3) STRING(4) COMPONENT(5)
//     OPTIONAL_COMPONENT(6) ITEM_STACK(7) BOOLEAN(8); BOOLEAN = forValueType(ByteBufCodecs.BOOL).]
const boolSerializerID int32 = 8

// babyDataEntry builds the single SynchedEntityData$DataValue entry that carries an AgeableMob's
// DATA_BABY_ID — the field the client reads to render the pig small. It frames on the wire as
// Byte(dataBabyIndex=16) + VarInt(boolSerializerID=8) + Boolean(isBaby) (entityDataEntry.WriteTo),
// mirroring airDataEntry's INT pattern with the BOOL codec. Carried at spawn (the small-render baby)
// and broadcast on the -1->0 grow-up (DATA_BABY_ID=false, so the client re-renders full size).
//   [VERIFIED javap AgeableMob.DATA_BABY_ID = EntityDataAccessor<Boolean>; the BOOLEAN codec is
//    ByteBufCodecs.BOOL == one byte 0/1, which pk.Boolean writes.]
func babyDataEntry(isBaby bool) entityDataEntry {
	return entityDataEntry{
		index:        dataBabyIndex,
		serializerID: boolSerializerID,
		value:        pk.Boolean(isBaby), // EntityDataSerializers.BOOLEAN codec == ByteBufCodecs.BOOL
	}
}

// entityDataEOF is the SynchedEntityData EOF_MARKER (255 / 0xFF) — the MANDATORY single
// terminator byte that closes the packed-items list. It is ALWAYS written, even for an
// empty list; omitting it desyncs the client's entity stream and the entity is dropped.
const entityDataEOF = 0xFF

// rawBytes is a FieldEncoder that writes its bytes verbatim (no length prefix) — used to
// splice a pre-built body (the SetEntityData packed-items list + 0xFF terminator) into a
// pk.Marshal field list. The standard pk.ByteArray would prepend an unwanted VarInt length.
type rawBytes []byte

func (b rawBytes) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b)
	return int64(n), err
}

// encodeSetEntityData builds ClientboundSetEntityData (06-CAPTURE-DIFF §4): VarInt id, then
// the packed-items body, then the mandatory 0xFF terminator. The body is, in order: any
// PRE-BUILT entry bytes carried on the entity's metadata slot (Entity.metadata — a snapshot-
// friendly []byte holding already-encoded DataValue entries, the slot 06-01 reserved for the
// tracker), then any explicit entries passed here. For v1 both are empty, so the body is just
// VarInt id + 0xFF — the minimum framing that keeps the stream aligned with no serializer-
// codec surface to get wrong (threat T-6-04). A future plan (or a spawn helper) fills
// Entity.metadata with the entity's default SynchedEntityData entries and they flow through
// here unchanged; 06-07 confirms which entries the client requires.
func encodeSetEntityData(e *Entity, entries ...entityDataEntry) pk.Packet {
	var body bytes.Buffer
	// Pre-built metadata entry bytes (if the entity carries any) are spliced verbatim — they
	// are already in DataValue.write framing (Byte index, VarInt serializerId, value).
	if len(e.metadata) > 0 {
		body.Write(e.metadata)
	}
	for _, entry := range entries {
		// Errors writing to a bytes.Buffer are impossible; ignore for the in-memory build.
		_, _ = entry.WriteTo(&body)
	}
	_, _ = pk.UnsignedByte(entityDataEOF).WriteTo(&body) // ALWAYS-present terminator
	return pk.Marshal(
		int32(packetid.ClientboundSetEntityData),
		pk.VarInt(e.id),
		rawBytes(body.Bytes()),
	)
}

// encodeSetEntityDataByID builds ClientboundSetEntityData for an entity referenced by id alone
// (a player has no *Entity instance): VarInt id, the entries' DataValue bytes, then the
// mandatory 0xFF terminator. Same framing as encodeSetEntityData, minus the e.metadata splice
// (a player carries no pre-built metadata slot). Used to push a single live flag change (e.g.
// DATA_LIVING_ENTITY_FLAGS on start/stop using an item) to observers.
func encodeSetEntityDataByID(entityID int32, entries ...entityDataEntry) pk.Packet {
	var body bytes.Buffer
	for _, entry := range entries {
		_, _ = entry.WriteTo(&body)
	}
	_, _ = pk.UnsignedByte(entityDataEOF).WriteTo(&body)
	return pk.Marshal(
		int32(packetid.ClientboundSetEntityData),
		pk.VarInt(entityID),
		rawBytes(body.Bytes()),
	)
}

// encodeSetEntityMotion builds ClientboundSetEntityMotion (06-CAPTURE-DIFF §5): VarInt id
// + the LP-quantized velocity. Same quantizer as AddEntity's movement.
func encodeSetEntityMotion(e *Entity) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetEntityMotion),
		pk.VarInt(e.id),
		lpVec3{e.vx, e.vy, e.vz},
	)
}

// lpDeltaScale is VecDeltaCodec.TRUNCATION_STEPS: the move-delta position quantization
// factor. encode(d) = round(d * 4096); a Pos/PosRot short delta is
// (encode(curr) - encode(prev)) cast to int16 (06-CAPTURE-DIFF §5).
const lpDeltaScale = 4096.0

// encodeDelta is VecDeltaCodec.encode: round(d * 4096) as a long.
func encodeDelta(d float64) int64 { return int64(math.Round(d * lpDeltaScale)) }

// moveDeltaShort computes the int16 short delta between a previous and current coordinate,
// matching the jar's (encode(curr) - encode(prev)) -> l2i -> i2s. Deltas whose magnitude
// exceeds ±8 blocks/tick overflow a short — the tracker uses TeleportEntity for moved
// entities in v1 to avoid that edge (the delta encoders exist for the later optimization).
func moveDeltaShort(prev, curr float64) pk.Short {
	return pk.Short(int16(encodeDelta(curr) - encodeDelta(prev)))
}

// encodeMoveEntityPos builds ClientboundMoveEntityPos (06-CAPTURE-DIFF §5): VarInt id,
// Short xa/ya/za, Boolean onGround. The shorts are 4096-scaled position deltas.
func encodeMoveEntityPos(id int32, xa, ya, za pk.Short, onGround bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundMoveEntityPos),
		pk.VarInt(id),
		xa, ya, za,
		pk.Boolean(onGround),
	)
}

// encodeMoveEntityPosRot builds ClientboundMoveEntityPosRot (06-CAPTURE-DIFF §5): VarInt
// id, Short xa/ya/za, Byte yRot, Byte xRot (yaw BEFORE pitch), Boolean onGround.
func encodeMoveEntityPosRot(id int32, xa, ya, za pk.Short, yaw, pitch float32, onGround bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundMoveEntityPosRot),
		pk.VarInt(id),
		xa, ya, za,
		degToByteAngle(yaw),   // yRot first
		degToByteAngle(pitch), // xRot second
		pk.Boolean(onGround),
	)
}

// encodeMoveEntityRot builds ClientboundMoveEntityRot (06-CAPTURE-DIFF §5): VarInt id,
// Byte yRot, Byte xRot, Boolean onGround.
func encodeMoveEntityRot(id int32, yaw, pitch float32, onGround bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundMoveEntityRot),
		pk.VarInt(id),
		degToByteAngle(yaw),
		degToByteAngle(pitch),
		pk.Boolean(onGround),
	)
}

// encodeMoveEntityPosRotB / encodeMoveEntityRotB are the byte-angle delta encoders the
// ServerEntity.sendChanges path uses: they take the ALREADY-PACKED yRot/xRot bytes (packDegrees,
// floor) rather than re-packing a float with degToByteAngle (round). sendChanges packs the angle
// once into b2/b3 (lastSentYRot/XRot), tests the change threshold on those bytes, and writes the
// SAME bytes onto the wire — so the move encoders must consume the bytes verbatim. Wire layout is
// identical to encodeMoveEntityPosRot / encodeMoveEntityRot (yRot before xRot).
func encodeMoveEntityPosRotB(id int32, xa, ya, za pk.Short, yRot, xRot int8, onGround bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundMoveEntityPosRot),
		pk.VarInt(id),
		xa, ya, za,
		pk.Angle(yRot), // yRot first (already packDegrees-floored)
		pk.Angle(xRot), // xRot second
		pk.Boolean(onGround),
	)
}

func encodeMoveEntityRotB(id int32, yRot, xRot int8, onGround bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundMoveEntityRot),
		pk.VarInt(id),
		pk.Angle(yRot),
		pk.Angle(xRot),
		pk.Boolean(onGround),
	)
}

// encodeEntityPositionSync builds ClientboundEntityPositionSync — the per-tick ABSOLUTE position
// packet ServerEntity.sendChanges emits when a delta would overflow / 400 ticks elapse / onGround
// flipped (NOT ClientboundTeleportEntity, which is for explicit teleports). Wire (jar:
// ClientboundEntityPositionSyncPacket.STREAM_CODEC): VarInt id, PositionMoveRotation (pos 3×Double,
// deltaMovement 3×Double, Float yRot, Float xRot), Boolean onGround.
//   [VERIFIED javap: ClientboundEntityPositionSyncPacket.of -> id, PositionMoveRotation(
//    trackingPosition, deltaMovement, yRot, xRot), onGround; PositionMoveRotation.STREAM_CODEC =
//    Vec3 position, Vec3 deltaMovement, Float yRot, Float xRot.]
func encodeEntityPositionSync(e *Entity) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundEntityPositionSync),
		pk.VarInt(e.id),
		pk.Double(e.x), pk.Double(e.y), pk.Double(e.z),
		pk.Double(e.vx), pk.Double(e.vy), pk.Double(e.vz),
		pk.Float(e.yaw), pk.Float(e.pitch),
		pk.Boolean(e.onGround),
	)
}

// encodeTeleportEntity builds ClientboundTeleportEntity (06-CAPTURE-DIFF §5): VarInt id,
// PositionMoveRotation (Double x/y/z + Double dx/dy/dz + Float yRot + Float xRot), Int
// relativeFlags (0 = all absolute), Boolean onGround. This is the v1 "moved entity" path:
// absolute Doubles avoid the short-delta overflow edge.
func encodeTeleportEntity(e *Entity) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundTeleportEntity),
		pk.VarInt(e.id),
		// PositionMoveRotation: Vec3.STREAM_CODEC position (3 Double), Vec3.STREAM_CODEC
		// deltaMovement (3 Double), Float yRot, Float xRot.
		pk.Double(e.x), pk.Double(e.y), pk.Double(e.z),
		pk.Double(e.vx), pk.Double(e.vy), pk.Double(e.vz),
		pk.Float(e.yaw), pk.Float(e.pitch),
		pk.Int(0), // relativeFlags: 0 == every component absolute
		pk.Boolean(e.onGround),
	)
}

// encodeRotateHead builds ClientboundRotateHead (06-CAPTURE-DIFF §5): VarInt id, Byte
// yHeadRot. Sent alongside a move so the head tracks the body for living entities.
func encodeRotateHead(id int32, headYaw float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundRotateHead),
		pk.VarInt(id),
		degToByteAngle(headYaw),
	)
}

// animateActionMainHandSwing / animateActionOffHandSwing are the ClientboundAnimatePacket
// `action` byte values for an arm swing. JAR-DERIVED (LivingEntity.swing): the action is
// `hand == MAIN_HAND ? 0 : 3` (the bytecode's iconst_0 / iconst_3 branch). Values 1/2/5 are
// hurt/wake/crit-other animations not driven by swing.
//
//	[VERIFIED javap: LivingEntity.swing(hand,boolean) -> new ClientboundAnimatePacket(this,
//	 MAIN_HAND ? 0 : 3); ClientboundAnimatePacket.write -> writeVarInt(id), writeByte(action).]
const (
	animateActionMainHandSwing = 0
	animateActionOffHandSwing  = 3
)

// encodeAnimate builds ClientboundAnimate (jar: ClientboundAnimatePacket.write): VarInt id +
// UByte action. Broadcast to players TRACKING the entity (LivingEntity.swing ->
// ServerChunkCache.sendToTrackingPlayers) so observers see the arm swing. The swinging player
// is NOT included (swing(hand) passes updateSelf=false).
func encodeAnimate(entityID int32, action int) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundAnimate),
		pk.VarInt(entityID),
		pk.UnsignedByte(action),
	)
}

// equipmentSlotMainHand is EquipmentSlot.MAINHAND.ordinal() — 0 (the enum's first constant:
// MAINHAND, OFFHAND, FEET, LEGS, CHEST, HEAD, BODY, SADDLE). The SetEquipment slot byte is the
// ordinal, with bit 0x80 (continuation) set on every entry EXCEPT the last.
//   [VERIFIED javap: ClientboundSetEquipmentPacket.write -> for each pair: writeByte(
//    isLast ? ordinal : ordinal | 0x80), ItemStack.OPTIONAL_STREAM_CODEC.encode(stack).]
const equipmentSlotMainHand = 0

// encodeSetEquipment builds ClientboundSetEquipment for a SINGLE equipment slot (jar:
// ClientboundSetEquipmentPacket.write): VarInt entityId, then a list of (Byte slotFlag,
// ItemStack) pairs. The slot byte = slot.ordinal() with 0x80 set on all-but-last; a one-entry
// list sets no continuation bit. The ItemStack is the OPTIONAL_STREAM_CODEC (component.SlotData
// .WriteTo — the SAME codec ContainerSetContent's carried item uses). v1 syncs only MAINHAND
// (no armor inventory yet — the other 7 slots stay empty/unsynced, the cited default).
func encodeSetEquipment(entityID int32, slot int, item component.SlotData) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetEquipment),
		pk.VarInt(entityID),
		pk.Byte(slot), // single entry => no 0x80 continuation bit
		&item,         // ItemStack.OPTIONAL_STREAM_CODEC
	)
}

// encodeTakeItemEntity builds ClientboundTakeItemEntity (Plan 17-14 / ITEM-PICKUP): the
// "item flies into the collector" pickup animation. JAR-DERIVED wire layout (javap'd this
// session from temp/cache/26.2-inner.jar, ClientboundTakeItemEntityPacket.write): three
// VarInts in order — itemId (the picked-up item entity), playerId (the collector), amount
// (the count taken). LivingEntity.take broadcasts this to every player tracking the item.
//
//	[VERIFIED javap: write → writeVarInt(itemId), writeVarInt(playerId), writeVarInt(amount).]
func encodeTakeItemEntity(itemID, collectorID int32, amount int) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundTakeItemEntity),
		pk.VarInt(itemID),        // itemId: the item entity being collected
		pk.VarInt(collectorID),   // playerId: the collecting player's entity id
		pk.VarInt(int32(amount)), // amount: the stack count taken
	)
}

// entityEventDeath is the EntityEvent (the legacy "entity status") byte broadcast by
// net.minecraft.world.level.Level.broadcastEntityEvent(this, 3) inside LivingEntity.die: status
// 3 == the death animation (the client plays the death tilt/fade). The full status table lives in
// ClientboundEntityEventPacket; v1 needs only the death status the die() port broadcasts.
//   [VERIFIED javap: LivingEntity.die -> Level.broadcastEntityEvent(this, (byte) 3) (the iconst_3
//    at die bytecode 162) -> ClientboundEntityEventPacket(entity, 3).]
const entityEventDeath byte = 3

// entityEventDeathPoof is the EntityEvent byte broadcast by
// net.minecraft.world.entity.LivingEntity.tickDeath at deathTime >= 20: status 60 == the death
// "poof" — the client spawns the despawn smoke/explosion particles as the dying entity is removed.
// Unlike status 3 (which die() sends to START the fall-over animation), status 60 is the FINAL
// despawn cue, sent by tickDeath the same tick it calls remove(KILLED).
//   [VERIFIED javap LivingEntity.tickDeath: bipush 60; Level.broadcastEntityEvent(this, 60); then
//    remove(Entity$RemovalReason.KILLED) — the deathTime>=20 branch.]
const entityEventDeathPoof byte = 60

// encodeEntityEvent builds ClientboundEntityEvent (jar: ClientboundEntityEventPacket.write):
// writeInt(entityId) — a PLAIN 4-byte Int, NOT a VarInt — then writeByte(eventId). Broadcast to
// every player tracking the entity (broadcastEntityEvent -> ServerChunkCache.broadcastAndSend).
//   [VERIFIED javap: ClientboundEntityEventPacket.write -> writeInt(entityId); writeByte(eventId).]
func encodeEntityEvent(entityID int32, eventID byte) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundEntityEvent),
		pk.Int(entityID),     // writeInt — a fixed 4-byte int, NOT a VarInt
		pk.Byte(int8(eventID)),
	)
}

// writeOptionalEntityIDPlusOne is the port of FriendlyByteBuf.writeOptionalEntityId / the
// ClientboundDamageEventPacket id encoding: an entity id is written as a VarInt of (id + 1), so 0
// means "none/absent" and a real id N is written as N+1. An absent id (Sulfur models it as <= 0
// from damageSource.attacker, where 0 == none) writes the VarInt 0.
//   [VERIFIED javap ClientboundDamageEventPacket: sourceCauseId/sourceDirectId written via
//    buf.writeVarInt(id + 1) with 0 reserved for the empty optional (writeOptionalEntityId).]
func writeOptionalEntityIDPlusOne(id int32) pk.VarInt {
	if id <= 0 {
		return pk.VarInt(0) // no causing/direct entity (an environmental / anonymous source)
	}
	return pk.VarInt(id + 1)
}

// encodeDamageEvent builds ClientboundDamageEvent — THE RED-FLASH packet. It is the wire side of
// net.minecraft.world.level.Level.broadcastDamageEvent(Entity, DamageSource), which
// LivingEntity.hurtServer fires on the tookFullDamage branch so the client plays the hurt animation
// (the red flash) and the directional knockback tilt. v1's hurt pipeline ported the damage MATH but
// never broadcast this packet, so neither mobs nor players flashed red on a hit — this encoder + the
// broadcast calls in combat_mob.go / combat.go close that gap, 1:1 with the jar.
//
// JAR-DERIVED wire layout (javap ClientboundDamageEventPacket.write, proto 776, this session) — the
// fields IN ORDER:
//   - entityId      : VarInt  (the hurt entity)
//   - sourceTypeId  : VarInt  (Holder<DamageType> written as its registry network id — the
//                     damage_type holder id the client got at config; == damageSource.typeTag, because
//                     BOTH the config registrydata damage_type send order AND data/tag.DamageTypeNames
//                     are sorted alphabetically, so the sorted index IS the holder id — no remap)
//   - sourceCauseId : VarInt  via writeOptionalEntityId (id+1; 0 == none)
//   - sourceDirectId: VarInt  via writeOptionalEntityId (id+1; 0 == none)
//   - hasSourcePos  : Boolean (Optional<Vec3> present flag) — empty for a normal entity-caused hit, so
//                     a single false; the 3 source-position doubles are written ONLY when present
//                     (they are not, in v1: no positional damage source is wired).
//
//	[VERIFIED javap net.minecraft.world.entity.LivingEntity.hurtServer: tookFullDamage branch ->
//	 level.broadcastDamageEvent(this, source); ServerLevel.broadcastDamageEvent ->
//	 getChunkSource().sendToTrackingPlayersAndSelf(entity, new ClientboundDamageEventPacket(entity,
//	 source)); ClientboundDamageEventPacket.write -> writeVarInt(entityId); writeVarInt(sourceTypeId);
//	 writeVarInt(causeId+1 / 0); writeVarInt(directId+1 / 0); writeBoolean(sourcePos.isPresent()).]
//
// For a direct melee hit sourceCauseId == sourceDirectId == the attacker's entity id; both are
// absent (0) for an environmental hit (fall/drown/starve/suffocation), and sourcePosition is always
// empty in v1.
func encodeDamageEvent(entityID, sourceTypeID, sourceCauseID, sourceDirectID int32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundDamageEvent),
		pk.VarInt(entityID),                            // entityId
		pk.VarInt(sourceTypeID),                        // sourceTypeId: the damage_type holder id
		writeOptionalEntityIDPlusOne(sourceCauseID),    // sourceCauseId (id+1; 0 == none)
		writeOptionalEntityIDPlusOne(sourceDirectID),   // sourceDirectId (id+1; 0 == none)
		pk.Boolean(false),                              // sourcePosition: empty Optional<Vec3> (no pos)
	)
}

// --- WR-05: the entity-attached SOUND packet (ClientboundSoundEntity) ------------------
//
// The HURT SOUND (the reported "no sound on hit" gap) rides ClientboundSoundEntityPacket — the
// entity-attached sound variant LivingEntity.makeSound -> playSound emits, which is closer than the
// positional ClientboundSoundPacket because it follows the entity. The codebase had NO sound packet at
// all; this is the first one. All wire numbers are JAR-DERIVED (javap'd from temp/cache/26.2-inner.jar
// this session), NOT guessed.

// soundSourceNeutral is SoundSource.NEUTRAL.ordinal() == 6. SoundSource's enum order is MASTER(0),
// MUSIC(1), RECORDS(2), WEATHER(3), BLOCKS(4), HOSTILE(5), NEUTRAL(6), PLAYERS(7), AMBIENT(8), VOICE(9),
// UI(10). A passive mob (Animal/Pig) overrides getSoundSource() to NEUTRAL, so its hurt sound plays on
// the NEUTRAL category. FriendlyByteBuf.writeEnum writes the ordinal as a VarInt.
//   [VERIFIED javap: net.minecraft.sounds.SoundSource enum order (MASTER..UI); Animal.getSoundSource ->
//    getstatic SoundSource.NEUTRAL; FriendlyByteBuf.writeEnum -> writeVarInt(ordinal).]
const soundSourceNeutral = 6

// encodeSoundEntity builds ClientboundSoundEntity (jar: ClientboundSoundEntityPacket.write) — an
// entity-attached sound. JAR-DERIVED wire layout (the constructor/decode field order matches write):
//   - sound  : Holder<SoundEvent> via SoundEvent.STREAM_CODEC == ByteBufCodecs.holder(SOUND_EVENT, ...):
//              a registry Reference holder writes VarInt(registryId + 1); a Direct holder writes
//              VarInt(0) then the inline SoundEvent. PIG_HURT is a registry sound, so we write
//              VarInt(soundID + 1) only (the inline-direct path is never taken for a registered sound).
//   - source : SoundSource enum via writeEnum -> VarInt(ordinal)
//   - id     : VarInt (the entity the sound is attached to)
//   - volume : Float
//   - pitch  : Float
//   - seed   : Long (the client's per-sound RNG seed for variant selection)
//
//	[VERIFIED javap net.minecraft.network.protocol.game.ClientboundSoundEntityPacket: ctor/decode order
//	 SoundEvent.STREAM_CODEC(Holder) ; readEnum(SoundSource) ; readVarInt(id) ; readFloat(volume) ;
//	 readFloat(pitch) ; readLong(seed). write writes them in the SAME order. SoundEvent.STREAM_CODEC =
//	 ByteBufCodecs.holder(Registries.SOUND_EVENT, DIRECT_STREAM_CODEC); ByteBufCodecs$30.encode writes
//	 VarInt(getIdOrThrow + 1) for a Reference holder, VarInt(0)+direct for a Direct holder.]
func encodeSoundEntity(soundID int32, source int, entityID int32, volume, pitch float32, seed int64) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSoundEntity),
		pk.VarInt(soundID+1), // Holder<SoundEvent>: registry Reference -> id + 1 (0 reserved for inline)
		pk.VarInt(int32(source)), // SoundSource ordinal (writeEnum)
		pk.VarInt(entityID),      // the entity the sound follows
		pk.Float(volume),         // getSoundVolume() == 1.0 for a pig
		pk.Float(pitch),          // getVoicePitch()
		pk.Long(seed),            // per-sound RNG seed
	)
}

// --- MOB-SUB-09: the HEART particle wire-out (encodeLevelParticles) --------------------
//
// THE GAP THIS CLOSES: the codebase had a ClientboundLevelParticles packet id (data/packetid) but NO
// encoder. This is the minimal, javap-confirmed ClientboundLevelParticlesPacket wire-out — the general
// "the server spawns particles" packet (ServerLevel.sendParticles builds exactly this). It is built
// for the in-love/breed HEART burst, but it is generic (any particle-type id + position + offset +
// speed + count).
//
// JAR-CONFIRMED WIRE LAYOUT (javap net.minecraft.network.protocol.game.ClientboundLevelParticlesPacket
// write/decode this session — the read ctor and write() agree field-for-field):
//	1. overrideLimiter : Boolean   (writeBoolean)
//	2. alwaysShow      : Boolean   (writeBoolean)   <- the 26.2 addition (was absent in <=1.21.4)
//	3. x, y, z         : Double    (writeDouble × 3)
//	4. xDist, yDist, zDist : Float (writeFloat × 3) — the spread (per-particle random offset radius)
//	5. maxSpeed        : Float     (writeFloat)
//	6. count           : Int       (writeInt — a FIXED 4-byte int, NOT a VarInt)
//	7. particle        : ParticleTypes.STREAM_CODEC == ByteBufCodecs.registry(PARTICLE_TYPE).dispatch:
//	                     VarInt(particleTypeId) THEN the per-type options stream. ParticleTypes.HEART is
//	                     a SimpleParticleType whose options codec is StreamCodec.unit -> writes NOTHING,
//	                     so for HEART the particle field is just VarInt(particleTypeId), nothing trailing.
//
// THE COUNT FIELD IS writeInt (a plain 4-byte big-endian int), NOT a VarInt — load-bearing (pk.Int).
// THE PARTICLE-TYPE id is the trailing field (after count), a VarInt registry index — load-bearing
// (it is NOT leading; the 26.2 layout puts the particle last).
//	[VERIFIED javap ClientboundLevelParticlesPacket.write: writeBoolean(overrideLimiter);
//	 writeBoolean(alwaysShow); writeDouble(x/y/z); writeFloat(xDist/yDist/zDist); writeFloat(maxSpeed);
//	 writeInt(count); ParticleTypes.STREAM_CODEC.encode(buf, particle). ParticleTypes.STREAM_CODEC ==
//	 ByteBufCodecs.registry(Registries.PARTICLE_TYPE).dispatch(...) -> VarInt(typeId) + options;
//	 SimpleParticleType (HEART) streamCodec == StreamCodec.unit -> no per-particle bytes.]
func encodeLevelParticles(particleID int32, overrideLimiter, alwaysShow bool, x, y, z float64, xDist, yDist, zDist, maxSpeed float32, count int32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundLevelParticles),
		pk.Boolean(overrideLimiter), // overrideLimiter (long-distance / ignore the client particle cap)
		pk.Boolean(alwaysShow),      // alwaysShow (26.2 addition)
		pk.Double(x),                // x
		pk.Double(y),                // y
		pk.Double(z),                // z
		pk.Float(xDist),             // xDist — per-particle spread radius on X
		pk.Float(yDist),             // yDist
		pk.Float(zDist),             // zDist
		pk.Float(maxSpeed),          // maxSpeed
		pk.Int(count),               // count — writeInt: a FIXED 4-byte int, NOT a VarInt
		pk.VarInt(particleID),       // particle: ParticleTypes registry index (HEART has no options bytes)
	)
}

// entityEventInLoveHearts is the EntityEvent ("entity status") byte Animal.setInLove +
// finalizeSpawnChildFromBreeding broadcast via Level.broadcastEntityEvent(this, (byte)18): status 18 ==
// the in-love / breeding HEART burst. The CLIENT's Animal.handleEntityEvent(18) spawns 7 HEART
// particles around the mob locally; the SERVER never sends the aiStep hearts on the wire (its
// Level.addParticle is a no-op) — the heart trigger is THIS event, not a ClientboundLevelParticles
// packet. This is the faithful in-love heart path on a dedicated server.
//	[VERIFIED javap Animal.setInLove: level(); bipush 18; Level.broadcastEntityEvent(this, 18).
//	 Animal.handleEntityEvent: `if (event == 18) { for i<7: addParticle(HEART, getRandomX(1),
//	 getRandomY()+0.5, getRandomZ(1), gauss*0.02 ×3) }`. finalizeSpawnChildFromBreeding also
//	 broadcastEntityEvent(this, 18).]
const entityEventInLoveHearts byte = 18

// encodeRemoveEntities builds ClientboundRemoveEntities (06-CAPTURE-DIFF §5):
// writeIntIdList == VarInt count followed by N VarInt ids. The tracker batches ALL of a
// player's newly-out-of-range ids into ONE such packet per tick.
func encodeRemoveEntities(ids []int32) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, len(ids)+1)
	fields = append(fields, pk.VarInt(int32(len(ids))))
	for _, id := range ids {
		fields = append(fields, pk.VarInt(id))
	}
	return pk.Marshal(int32(packetid.ClientboundRemoveEntities), fields...)
}
