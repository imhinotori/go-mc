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

// --- MODEL-M2: the static rig attach ---------------------------------------------------------------
//
// attachModelRig materializes a declared model's STATIC bone rig onto a freshly-spawned base mob and
// mounts it, the MODEL-M2 hot-path analogue of newSkillRunner. For each bone it spawns an
// item_display (M1 spawnItemDisplay) carrying the bone item + its FIXED display context, bakes the
// bone's pivot into DATA_TRANSLATION, mounts the bone as a PASSENGER of the base (the existing
// passenger seam — the client positions passengers at the vehicle, so the rig rides the base for
// free), sets the base INVISIBLE (SHARED_FLAGS bit 0x20 — the base is the collision/AI carrier, the
// rig is the visible body), and attaches the modelInstance. Called from spawnDeclaredMob AFTER the
// store add and BEFORE the spawn trigger (H.1.2), so a spawn-trigger skill already sees the rig.
//
// NO animation (M3): the bones are static — their pivot is a fixed DATA_TRANSLATION offset; the base
// moves and the passengers move with it. The bone display world x/y/z mirror the base (copied here;
// re-mirrored each tick by tickModelRig so tracker distance + the G.1 bone AABBs stay correct).
//
//	[VERIFIED javap Entity.setInvisible -> setSharedFlag(5, ...) => bit 1<<5 == 0x20; ClientboundSet
//	 PassengersPacket vehicle + passenger ids; Display$ItemDisplay DATA_TRANSLATION (index 11).]
func (t *TickLoop) attachModelRig(e *Entity, decl *modelDecl) {
	inst := &modelInstance{decl: decl, bones: make([]boneRuntime, 0, len(decl.bones))}
	for i := range decl.bones {
		b := &decl.bones[i]
		// Spawn the bone item_display at the base's position (world coord copy — the passenger mount +
		// DATA_TRANSLATION pivot give the on-screen offset; the server x/y/z mirror the base).
		bone := t.spawnItemDisplay(e.x, e.y, e.z, b.item, b.displayContext)
		// Bake the bone's pivot into DATA_TRANSLATION (index 11) and re-encode the spawn metadata BEFORE
		// the tracker's first AddEntity/SetEntityData (spawnItemDisplay added it this tick; the tracker
		// broadcasts next tick), so the bone renders at its rig offset from the first frame.
		bone.setDisplayTranslation(b.pivotX, b.pivotY, b.pivotZ)
		bone.metadata = encodeItemDisplayMetadata(bone)
		// Mount the bone on the base via the passenger seam (a non-player passenger — id-only list).
		t.vehicleAddPassenger(e, bone.id, false)
		bone.vehicle = e.id
		inst.bones = append(inst.bones, boneRuntime{id: bone.id, decl: b})
	}
	// Set the base INVISIBLE: splice the SHARED_FLAGS byte (bit 0x20) onto the base's spawn metadata so
	// the tracker's first AddEntity/SetEntityData hides the carrier (the SAME metadata-splice seam the
	// sheep-wool / wolf-flags carries use — the base has not been broadcast yet). The rig is the visible
	// body; the base is the invisible collision/AI carrier.
	var buf bytes.Buffer
	_, _ = sharedFlagsDataEntry(entityInvisibleFlag).WriteTo(&buf)
	e.metadata = append(e.metadata, buf.Bytes()...)
	e.model = inst
	// Broadcast the passenger list so any already-tracking observer mounts the rig (at spawn the base is
	// not yet broadcast, so trackers are typically empty — but this keeps the mount correct for any
	// mid-spawn tracker, mirroring where vanilla addPassenger triggers the SetPassengers packet).
	t.broadcastSetPassengers(e)
}

// entityInvisibleFlag is Entity.SHARED_FLAGS bit 5 (1<<5 == 0x20) — setInvisible(true). The base mob of
// a native model is invisible (the bone rig is the visible body). VERIFIED javap Entity.setInvisible ->
// setSharedFlag(5, b), and setSharedFlag sets bit (1 << i).
const entityInvisibleFlag int8 = 0x20

// tickModelRig mirrors every bone display's world x/y/z onto the base's position, the MODEL-M2
// per-tick coordinate copy (H.1.5). The bones ride the base client-side (passengers), so NO wire
// traffic is needed for their render position; the SERVER-side copy keeps the tracker's add/remove
// distance (a bone that drifted from the base could enter/leave a tracker independently) and the G.1
// per-bone AABBs anchored to the base. Called from the per-mob tick slot gated `e.model != nil`, so a
// mob with no model pays exactly one nil-check (the pig oracle is untouched). No animation (M3): the
// bones only follow the base; their transform offset is the static DATA_TRANSLATION pivot.
func (t *TickLoop) tickModelRig(e *Entity) {
	m := e.model
	if m == nil {
		return
	}
	for i := range m.bones {
		bone := t.entityByIDAnyRegion(m.bones[i].id)
		if bone == nil {
			continue // a bone despawned (defensive; M2 never removes bones mid-life)
		}
		bone.x, bone.y, bone.z = e.x, e.y, e.z
	}
}

// --- MODEL-M3: the keyframe animator ---------------------------------------------------------------
//
// modelAnimPushInterval is N in the spec ("N-tick pushes"): every N ticks the animator re-encodes and
// pushes each due bone's transform with DATA_TRANSFORMATION_INTERPOLATION_DURATION == N and
// START_DELTA_TICKS == 0, so the CLIENT interpolates between the server keyframes (smooth motion,
// bandwidth bounded to one burst every N ticks instead of every tick). N=2 per the M1 spec Section E
// amendment (b) / H.2.
const modelAnimPushInterval = 2

// tickModelAnimator is the MODEL-M3 per-mob animator tick (H.0 tick ordering: runs BEFORE tickMobSkills
// so an animation_frame-triggered skill lands the SAME tick the frame crosses). It (1) mirrors the bone
// world coords onto the base (the M2 tickModelRig job, folded in so the model path is one call), then
// (2) if an animator exists: applies any pending clip (H.0 reentrancy — pending is swapped in HERE, not
// synchronously by play_animation), advances the clock, interpolates + pushes due bone transforms every
// N ticks, fires animation_frame triggers on frame crossings and animation_end at clip end, and handles
// once/loop/hold end semantics. Gated at the call site by e.model != nil (the pig oracle pays one
// nil-check). A mob with a rig but no active clip still runs the coordinate mirror.
func (t *TickLoop) tickModelAnimator(e *Entity) {
	m := e.model
	if m == nil {
		return
	}
	// (1) M2 coordinate mirror: keep every bone's server x/y/z on the base (tracker distance + G.1 AABBs).
	t.tickModelRig(e)

	a := m.animator
	if a == nil {
		return
	}
	// (2a) H.0 reentrancy: apply a queued clip at the START of the tick (never synchronously in the
	// play_animation mechanic). A newly-applied clip starts at t=0 and applies its frame-0 pose this tick.
	if a.pending != nil {
		a.clip = a.pending
		a.mode = a.pendingMode
		a.t = 0
		a.pending = nil
		a.endFired = false
	}
	if a.clip == nil {
		return
	}
	clip := a.clip
	length := int(clip.length)

	// (2b) Frame crossing for animation_frame triggers: a declared frame N fires when the clock REACHES
	// N (t == N), evaluated on the current tick before any end handling wraps/stops the clock. Fire for
	// the current t (which starts at 0 on the apply tick and increments each subsequent tick).
	if a.t <= length {
		t.fireMobSkillTriggerAnim(e, clip.name, a.t)
	}

	// (2c) Push interpolated transforms every N ticks (and on the very first frame t==0). Each channel
	// writes its bone's Display transform + interp pair, then one pushItemDisplayData per touched bone.
	if a.t%modelAnimPushInterval == 0 {
		t.pushModelFrame(e, m, clip, float32(a.t))
	}

	// (2d) End handling: at t >= length the clip has played its full span.
	if a.t >= length {
		switch a.mode {
		case animModeLoop:
			t.fireMobSkillTriggerAnim(e, clip.name, animEndFrame)
			a.t = 0 // wrap; next tick re-applies frame 0
			return
		case animModeOnce:
			if !a.endFired {
				t.fireMobSkillTriggerAnim(e, clip.name, animEndFrame)
				a.endFired = true
			}
			a.clip = nil // stop (idle) — the last pushed pose stays on the client
			return
		case animModeHold:
			if !a.endFired {
				t.fireMobSkillTriggerAnim(e, clip.name, animEndFrame)
				a.endFired = true
			}
			// clamp: keep clip set, do NOT advance t past length (the client holds the last frame).
			return
		}
	}
	a.t++
}

// pushModelFrame interpolates every animated bone's transform at clip tick `at` and pushes the due
// bones' Display metadata with interp_duration=N so the client lerps to the new pose over N ticks.
// Linear interpolation between the surrounding keyframes per channel (position->translation vec3,
// scale->scale vec3, rotation->left_rotation quaternion built from the euler xyz via eulerXYZToQuat).
func (t *TickLoop) pushModelFrame(e *Entity, m *modelInstance, clip *animClip, at float32) {
	for ci := range clip.channels {
		ch := &clip.channels[ci]
		bone := t.modelBoneEntity(m, ch.bone)
		if bone == nil {
			continue
		}
		x, y, z := sampleChannel(ch, at)
		switch ch.kind {
		case channelPosition:
			bone.setDisplayTranslation(x, y, z)
		case channelScale:
			bone.setDisplayScale(x, y, z)
		case channelRotation:
			bone.setDisplayLeftRotation(eulerXYZToQuat(x, y, z))
		}
		// Client-interp seam: lerp over N ticks starting now (H.2 / M1 Section E amendment (b)).
		bone.setDisplayInterpolation(modelAnimPushInterval, 0)
		t.pushItemDisplayData(bone)
	}
}

// sampleChannel linearly interpolates a channel's 3-component value at clip tick `at` between the two
// surrounding keyframes. Before the first / after the last keyframe it clamps to the endpoint value
// (hold). Keyframes are load-sorted ascending by time (collectAnimClips guarantees it).
func sampleChannel(ch *animChannel, at float32) (float32, float32, float32) {
	kf := ch.keyframes
	if len(kf) == 0 {
		return 0, 0, 0
	}
	if at <= kf[0].time {
		return kf[0].x, kf[0].y, kf[0].z
	}
	last := kf[len(kf)-1]
	if at >= last.time {
		return last.x, last.y, last.z
	}
	for i := 1; i < len(kf); i++ {
		if at <= kf[i].time {
			a, b := kf[i-1], kf[i]
			span := b.time - a.time
			if span <= 0 {
				return b.x, b.y, b.z
			}
			f := (at - a.time) / span
			return a.x + (b.x-a.x)*f, a.y + (b.y-a.y)*f, a.z + (b.z-a.z)*f
		}
	}
	return last.x, last.y, last.z
}

// modelBoneEntity resolves a bone name to its live item_display entity via the instance's boneRuntime
// ids (the animator's bone lookup). Nil if the bone despawned (defensive).
func (t *TickLoop) modelBoneEntity(m *modelInstance, name string) *Entity {
	for i := range m.bones {
		if m.bones[i].decl != nil && m.bones[i].decl.name == name {
			return t.entityByIDAnyRegion(m.bones[i].id)
		}
	}
	return nil
}
