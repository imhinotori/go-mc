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
