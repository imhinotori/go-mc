package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// model_display.go -- DISPLAY ENTITY metadata foundation (MODEL-M1), a 1:1 port of
// net.minecraft.world.entity.Display (+ Display$ItemDisplay), verified against the 26.2 jar
// (temp/cache/26.2-inner.jar) via javap -c -p. No GPL paste; the exact SynchedEntityData indices,
// serializer ids and defaults are jar-derived, cited inline.
//
// M1 scope: spawn a Display$ItemDisplay carrying a STATIC transformed item -- the render transform
// (DATA_TRANSLATION/SCALE/LEFT_ROTATION/RIGHT_ROTATION), the transform-interp pair (DURATION/
// START_DELTA_TICKS) and the item (DATA_ITEM_STACK/DATA_ITEM_DISPLAY). Animation rigs come in M2+.
// REUSE: rides the SAME store-add/tracker path spawnItemFrame uses; transform+item flow through
// e.metadata. Mirrors spawnItemFrame/encodeFrameMetadata/pushFrameData 1:1.

// Display SynchedEntityData indices. VERIFIED javap Display static-init defineId order: 8=INTERP_
// START_DELTA_TICKS, 9=INTERP_DURATION, 10=POS_ROT_INTERP_DURATION, 11=TRANSLATION (VECTOR3),
// 12=SCALE (VECTOR3), 13=LEFT_ROTATION (QUATERNION), 14=RIGHT_ROTATION (QUATERNION); Display$Item
// Display: 23=DATA_ITEM_STACK (ITEM_STACK), 24=DATA_ITEM_DISPLAY (BYTE).
const (
	// index 8, INT=1, default 0.
	dataDisplayInterpStartDeltaIndex uint8 = 8
	// index 9, INT=1, default 0.
	dataDisplayInterpDurationIndex uint8 = 9
	// index 11, VECTOR3=39, default (0,0,0).
	dataDisplayTranslationIndex uint8 = 11
	// index 12, VECTOR3=39, default (1,1,1).
	dataDisplayScaleIndex uint8 = 12
	// index 13, QUATERNION=40, default identity (0,0,0,1).
	dataDisplayLeftRotationIndex uint8 = 13
	// index 14, QUATERNION=40, default identity (0,0,0,1).
	dataDisplayRightRotationIndex uint8 = 14
	// index 23, ITEM_STACK=7, default EMPTY.
	dataItemDisplayStackIndex uint8 = 23
	// index 24, BYTE=0, default 0 (ItemDisplayContext.NONE).
	dataItemDisplayContextIndex uint8 = 24
)

// spawnItemDisplay constructs a Display$ItemDisplay at (x,y,z) carrying item+context, seeds the
// Display transform defaults (scale (1,1,1), rotations identity (0,0,0,1)), encodes M1 metadata and
// adds it to the owning region store (the tracker broadcasts AddEntity+SetEntityData next tick, as
// spawnItemFrame does). Returns the entity.
//
//	[VERIFIED javap Display.defineSynchedData: DATA_SCALE default new Vector3f(1,1,1); LEFT/RIGHT
//	 ROTATION default new Quaternionf()==(0,0,0,1); DATA_TRANSLATION default new Vector3f()==(0,0,0);
//	 interp INT fields default 0. Display$ItemDisplay: ITEM_STACK default EMPTY, ITEM_DISPLAY 0.]
func (t *TickLoop) spawnItemDisplay(x, y, z float64, item component.SlotData, displayContext int8) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.ItemDisplay, x, y, z)
	e.isItemDisplay = true
	e.displayItem = item
	e.displayContext = displayContext
	// Display defineSynchedData defaults (seeded here to keep NewEntity generic). Translation + interp
	// default to the zero value already.
	e.dispScaleX, e.dispScaleY, e.dispScaleZ = 1, 1, 1
	e.dispLeftRot = [4]float32{0, 0, 0, 1}
	e.dispRightRot = [4]float32{0, 0, 0, 1}
	e.metadata = encodeItemDisplayMetadata(e)

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// encodeItemDisplayMetadata builds the verbatim SynchedEntityData DataValue bytes for a
// Display$ItemDisplay carrying a static transformed item, WITHOUT the 0xFF terminator
// (encodeSetEntityData appends that). Emits the transform + interp + item entries explicitly,
// mirroring encodeFrameMetadata. Result goes into e.metadata.
func encodeItemDisplayMetadata(e *Entity) []byte {
	var buf bytes.Buffer

	// DATA_TRANSFORMATION_INTERPOLATION_START_DELTA_TICKS (index 8, INT=1): VAR_INT.
	_, _ = entityDataEntry{
		index:        dataDisplayInterpStartDeltaIndex,
		serializerID: intSerializerID,
		value:        pk.VarInt(e.dispInterpStartDelta),
	}.WriteTo(&buf)
	// DATA_TRANSFORMATION_INTERPOLATION_DURATION (index 9, INT=1): VAR_INT.
	_, _ = entityDataEntry{
		index:        dataDisplayInterpDurationIndex,
		serializerID: intSerializerID,
		value:        pk.VarInt(e.dispInterpDuration),
	}.WriteTo(&buf)
	// DATA_TRANSLATION (index 11, VECTOR3=39): 3 big-endian float32 x,y,z.
	_, _ = entityDataEntry{
		index:        dataDisplayTranslationIndex,
		serializerID: vector3SerializerID,
		value:        vec3f{e.dispTransX, e.dispTransY, e.dispTransZ},
	}.WriteTo(&buf)
	// DATA_SCALE (index 12, VECTOR3=39): 3 big-endian float32 x,y,z.
	_, _ = entityDataEntry{
		index:        dataDisplayScaleIndex,
		serializerID: vector3SerializerID,
		value:        vec3f{e.dispScaleX, e.dispScaleY, e.dispScaleZ},
	}.WriteTo(&buf)
	// DATA_LEFT_ROTATION (index 13, QUATERNION=40): 4 big-endian float32 x,y,z,w.
	_, _ = entityDataEntry{
		index:        dataDisplayLeftRotationIndex,
		serializerID: quaternionSerializerID,
		value:        quatf{e.dispLeftRot[0], e.dispLeftRot[1], e.dispLeftRot[2], e.dispLeftRot[3]},
	}.WriteTo(&buf)
	// DATA_RIGHT_ROTATION (index 14, QUATERNION=40): 4 big-endian float32 x,y,z,w.
	_, _ = entityDataEntry{
		index:        dataDisplayRightRotationIndex,
		serializerID: quaternionSerializerID,
		value:        quatf{e.dispRightRot[0], e.dispRightRot[1], e.dispRightRot[2], e.dispRightRot[3]},
	}.WriteTo(&buf)
	// DATA_ITEM_STACK (index 23, ITEM_STACK=7): the shown stack (ItemStack.OPTIONAL_STREAM_CODEC),
	// reusing component.SlotData WriteTo as the frame/dropped-item DATA_ITEM does.
	di := e.displayItem
	_, _ = entityDataEntry{
		index:        dataItemDisplayStackIndex,
		serializerID: itemStackSerializerID,
		value:        &di,
	}.WriteTo(&buf)
	// DATA_ITEM_DISPLAY (index 24, BYTE=0): the ItemDisplayContext ordinal, a single signed byte.
	_, _ = entityDataEntry{
		index:        dataItemDisplayContextIndex,
		serializerID: byteSerializerID,
		value:        pk.Byte(e.displayContext),
	}.WriteTo(&buf)

	return buf.Bytes()
}

// pushItemDisplayData re-encodes the metadata and pushes a live ClientboundSetEntityData to every
// tracking observer (vanilla SynchedEntityData.set -> dirty -> ServerEntity broadcast). Mirrors
// pushFrameData 1:1. M3 (live transform updates) calls this; provided now.
func (t *TickLoop) pushItemDisplayData(e *Entity) {
	e.metadata = encodeItemDisplayMetadata(e)
	pkt := encodeSetEntityData(e)
	t.broadcastToTrackers(e.id, pkt)
}

// Setters store on Entity; the caller pushes (mirroring the frame setters no-auto-push contract).

// setDisplayTranslation is Display.setTransformation translation -> DATA_TRANSLATION.set.
func (e *Entity) setDisplayTranslation(x, y, z float32) {
	e.dispTransX, e.dispTransY, e.dispTransZ = x, y, z
}

// setDisplayScale is Display.setTransformation scale -> DATA_SCALE.set.
func (e *Entity) setDisplayScale(x, y, z float32) {
	e.dispScaleX, e.dispScaleY, e.dispScaleZ = x, y, z
}

// setDisplayLeftRotation is Display.setTransformation leftRotation -> DATA_LEFT_ROTATION.set ([x,y,z,w]).
func (e *Entity) setDisplayLeftRotation(quat [4]float32) { e.dispLeftRot = quat }

// setDisplayRightRotation is Display.setTransformation rightRotation -> DATA_RIGHT_ROTATION.set ([x,y,z,w]).
func (e *Entity) setDisplayRightRotation(quat [4]float32) { e.dispRightRot = quat }

// setDisplayInterpolation sets DATA_TRANSFORMATION_INTERPOLATION_DURATION (durationTicks) and
// DATA_TRANSFORMATION_INTERPOLATION_START_DELTA_TICKS (startDelta).
func (e *Entity) setDisplayInterpolation(durationTicks, startDelta int32) {
	e.dispInterpDuration = durationTicks
	e.dispInterpStartDelta = startDelta
}
