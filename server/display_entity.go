package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// display_entity.go — the DISPLAY ENTITIES (ITEM FRAME + GLOW ITEM FRAME + ARMOR STAND), a 1:1 port
// of net.minecraft.world.entity.decoration.{ItemFrame, GlowItemFrame, ArmorStand} (+ HangingEntity /
// BlockAttachedEntity + net.minecraft.world.item.ArmorStandItem), verified against the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar) via `javap -c -p` / CFR this session. No GPL paste; the exact
// constants (the 8-rotation frame steps + %8 wrap, the armor-stand slot-click y thresholds, the
// DATA_CLIENT_FLAGS bits, the drop lists) are the jar's, cited inline.
//
// REUSE: these are NON-mob entities that ride the SAME store-add/tracker path the boat/minecart/item
// drop use (owner.entities.add -> the tracker broadcasts ClientboundAddEntity + SetEntityData +
// SetEquipment next tick). The armor stand's held/armor slots live in the shared e.equipment array
// (LivingEntity.equipment); the equipmentSpawnPackets tracker path renders them for free. The frame's
// held item + rotation + direction and the armor-stand flags flow through e.metadata (the tracker
// splices it verbatim into SetEntityData). The interact seam is handleInteract (place/take/rotate);
// the attack/break seam is handleMobAttack (drop the frame/stand + its contents).
//
// SCOPED DEFERRALS (each jar-cited):
//   - MAP-IN-FRAME: the filled_map render + MapItemSavedData.isTrackedCountOverLimit(256) FAIL gate
//     (ItemFrame.interact) — no map subsystem. The frame holds ANY item + rotates faithfully; the map
//     limit check is a cited no-op (a non-map item never trips it). CITE ItemFrame.interact map branch.
//   - ARMOR-STAND POSE EDITING: the 6 DATA_*_POSE Rotations fields exist client-side (defineSynchedData
//     defaults) but v1 wires no arm/leg pose EDIT tool — the equip/take + break drops are the target.
//     A default-pose armor stand renders correctly (the client uses its own DEFAULT_*_POSE when no pose
//     entry is sent). CITE ArmorStand.DATA_*_POSE.
//   - The disabledSlots take/put lock bits (ArmorStand.disabledSlots) are DEFAULT 0 (no NBT tag), so
//     isDisabled/the swapItem lock branches are const-false — ported structurally, inert on a placed
//     stand. CITE ArmorStand.isDisabled / swapItem disabled branches.
//   - PAINTINGS (the OTHER HangingEntity) are OUT OF SCOPE — frames are 1×1, so the multi-block
//     HangingEntity survival is the simple single-face check ItemFrame.survives uses.

// --- ITEM FRAME constants (net.minecraft.world.entity.decoration.ItemFrame) ----------------------

// frameNumRotations is ItemFrame.NUM_ROTATIONS — the 8 discrete 45° rotation steps DATA_ROTATION
// cycles through (setRotation stores r % 8). CITE ItemFrame: `public static final int NUM_ROTATIONS = 8;`.
const frameNumRotations = 8

// frameDefaultDropChance is ItemFrame.DEFAULT_DROP_CHANCE (dropChance field init) == 1.0f: the framed
// contents always drop on break (random.nextFloat() < 1.0f is always true). CITE ItemFrame ctor
// `this.dropChance = 1.0f`.
const frameDefaultDropChance = float32(1.0)

// frameFixed is ItemFrame.DEFAULT_FIXED == false: a v1-placed frame is never a fixed (map-art) frame,
// so the `if (fixed)` PASS/return guards in interact/hurt/dropItem are const-false. CITE ItemFrame
// DEFAULT_FIXED.
const frameFixed = false

// frameSpawnStackOffset is HangingEntity.spawnAtLocation's directional offset (getStepX/Z * 0.15f):
// the drop pops slightly off the wall face along the frame's facing. CITE HangingEntity.spawnAtLocation
// (getX() + stepX*0.15, ..., getZ() + stepZ*0.15).
const frameSpawnStackOffset = 0.15

// --- ARMOR STAND DATA_CLIENT_FLAGS bits (net.minecraft.world.entity.decoration.ArmorStand) ---------
//
// CITE ArmorStand: CLIENT_FLAG_SMALL=1, CLIENT_FLAG_SHOW_ARMS=4, CLIENT_FLAG_NO_BASEPLATE=8,
// CLIENT_FLAG_MARKER=16 (bit 2 / value 2 is unused).
const (
	armorStandFlagSmall       = 0x01
	armorStandFlagShowArms    = 0x04
	armorStandFlagNoBasePlate = 0x08
	armorStandFlagMarker      = 0x10
)

// --- ItemFrame SynchedEntityData indices (accessor chain: Entity 0..7, HangingEntity DATA_DIRECTION=8,
// ItemFrame DATA_ITEM=9, DATA_ROTATION=10). VERIFIED javap: BlockAttachedEntity defines 0 accessors;
// HangingEntity defines DATA_DIRECTION first (index 8); ItemFrame defines DATA_ITEM then DATA_ROTATION. ---

const (
	// dataFrameDirectionIndex is HangingEntity.DATA_DIRECTION's accessor index (8 — the first after
	// Entity's 8 base fields). Serializer DIRECTION (id 12 — the registerSerializer order:
	// BYTE0,INT1,LONG2,FLOAT3,STRING4,COMPONENT5,OPTIONAL_COMPONENT6,ITEM_STACK7,BOOLEAN8,ROTATIONS9,
	// BLOCK_POS10,OPTIONAL_BLOCK_POS11,DIRECTION12). VERIFIED javap EntityDataSerializers static init.
	dataFrameDirectionIndex uint8 = 8
	// dataFrameItemIndex is ItemFrame.DATA_ITEM (index 9), serializer ITEM_STACK (id 7).
	dataFrameItemIndex uint8 = 9
	// dataFrameRotationIndex is ItemFrame.DATA_ROTATION (index 10), serializer INT (id 1).
	dataFrameRotationIndex uint8 = 10

	// directionSerializerID is EntityDataSerializers.DIRECTION's registry id (12). VERIFIED javap.
	directionSerializerID int32 = 12
)

// --- ArmorStand SynchedEntityData index (accessor chain: Entity 0..7, LivingEntity 8..14 (7 fields),
// ArmorStand DATA_CLIENT_FLAGS=15). VERIFIED javap: ArmorStand extends LivingEntity; DATA_CLIENT_FLAGS
// is the first ArmorStand accessor. The 6 DATA_*_POSE Rotations follow (16..21) — deferred (default pose). ---
const dataArmorStandClientFlagsIndex uint8 = 15

// =================================================================================================
// SPAWN
// =================================================================================================

// spawnItemFrame constructs an ItemFrame (or GlowItemFrame if glow) at the block cell (bx,by,bz),
// attached to the wall face `direction3D` (the CLICKED block face's 3D-data value: DOWN=0,UP=1,
// NORTH=2,SOUTH=3,WEST=4,EAST=5), and adds it to the owning region's store (the tracker broadcasts its
// AddEntity next tick, exactly as spawnBoat rides the store-add path). The frame's world position is the
// center of the AABB the wall-attach geometry produces; for the 1×1-cell v1 model that is the block cell
// center shifted 0.46875 toward the clicked face (ItemFrame.createBoundingBox: Vec3.atCenterOf(pos)
// .relative(direction, -0.46875)). Returns the spawned entity.
//
//	[VERIFIED javap ItemFrame.<init>(type, level, pos, direction) -> setDirection(direction); createBoundingBox:
//	 shiftToWall = 0.5 - DEPTH/2 = 0.46875; position = atCenterOf(pos).relative(direction, -0.46875).]
func (t *TickLoop) spawnItemFrame(bx, by, bz int, direction3D int, glow bool) *Entity {
	rec := entity.ItemFrame
	if glow {
		rec = entity.GlowItemFrame
	}
	// createBoundingBox: the frame sits shifted -0.46875 into the wall along the facing direction, from
	// the block-cell center. direction3D is the face the frame LOOKS OUT from (the clicked face normal).
	dx, dy, dz := direction3DStep(direction3D)
	x := float64(bx) + 0.5 - float64(dx)*frameWallShift
	y := float64(by) + 0.5 - float64(dy)*frameWallShift
	z := float64(bz) + 0.5 - float64(dz)*frameWallShift

	e := NewEntity(t.idAlloc.AllocID(), rec, x, y, z)
	e.isFrame = true
	e.frameGlow = glow
	e.frameDirection = int32(direction3D)
	e.frameBlockX, e.frameBlockY, e.frameBlockZ = bx, by, bz
	e.frameRotation = 0
	// setDirection: yaw/pitch from the facing (a horizontal facing sets yaw = get2DDataValue*90; a
	// vertical facing sets pitch = ±90). The client re-derives the render from spawnData (get3DDataValue);
	// these angles are the server-side book-keeping ItemFrame.setDirection writes. VERIFIED javap.
	e.yaw, e.pitch = frameFacingAngles(direction3D)
	e.headYaw = e.yaw
	// ClientboundAddEntity's object `data` field carries the direction 3D-data value; recreateFromPacket
	// on the client does setDirection(Direction.from3DDataValue(data)). VERIFIED javap ItemFrame.getAddEntityData.
	e.spawnData = int32(direction3D)
	e.metadata = encodeFrameMetadata(e)

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// frameWallShift is ItemFrame.createBoundingBox's `0.46875` (== 0.5 - DEPTH(0.0625)/2): the amount the
// frame face is shifted from the block-cell center toward the wall it hangs on. CITE ItemFrame.createBoundingBox.
const frameWallShift = 0.46875

// spawnArmorStand constructs an ArmorStand at (x,y,z) with the given yaw (the 45°-snapped yaw
// ArmorStandItem.useOn computes) and adds it to the owning region's store. A fresh stand carries the
// default DATA_CLIENT_FLAGS (0 — full-size, no arms, baseplate shown, not a marker) and empty equipment.
// initSpawnHealth seeds its LivingEntity health (createLivingAttributes MAX_HEALTH default), so the
// double-hit break window's isDeadOrDying guards read a live stand. Returns the spawned entity.
//
//	[VERIFIED javap ArmorStand extends LivingEntity; createAttributes = createLivingAttributes + STEP_HEIGHT 0;
//	 defineSynchedData DATA_CLIENT_FLAGS=(byte)0; ArmorStandItem.useOn: snapTo(x,y,z, yRot, 0).]
func (t *TickLoop) spawnArmorStand(x, y, z float64, yaw float32) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.ArmorStand, x, y, z)
	e.isArmorStand = true
	e.yaw = yaw
	e.headYaw = yaw
	e.armorStandFlags = 0
	initSpawnHealth(e) // LivingEntity.<init>: setHealth(getMaxHealth())
	e.metadata = encodeArmorStandMetadata(e)

	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// =================================================================================================
// METADATA ENCODING (the SynchedEntityData DataValue bytes the tracker splices into SetEntityData)
// =================================================================================================

// encodeFrameMetadata builds the verbatim SynchedEntityData DataValue bytes for an ItemFrame: the
// DATA_DIRECTION (so the client attaches it to the right wall), DATA_ITEM (the framed stack — an empty
// stack still writes, matching defineSynchedData) and DATA_ROTATION entries, WITHOUT the 0xFF terminator
// (encodeSetEntityData appends that). Result goes into e.metadata so the tracker's SetEntityData splices
// it unchanged. Re-encoded whenever the frame's item/rotation changes (setFrameItem / rotateFrame).
func encodeFrameMetadata(e *Entity) []byte {
	var buf bytes.Buffer
	// DATA_DIRECTION (index 8, serializer DIRECTION=12): the DIRECTION codec writes the 3D-data value
	// as a VarInt. VERIFIED javap EntityDataSerializers.DIRECTION -> ByteBufCodecs of Direction (VAR_INT
	// of get3DDataValue).
	_, _ = entityDataEntry{
		index:        dataFrameDirectionIndex,
		serializerID: directionSerializerID,
		value:        pk.VarInt(e.frameDirection),
	}.WriteTo(&buf)
	// DATA_ITEM (index 9, serializer ITEM_STACK=7): the framed stack (ItemStack.OPTIONAL_STREAM_CODEC),
	// reusing component.SlotData's WriteTo exactly as the dropped-item DATA_ITEM does.
	fi := e.frameItem
	_, _ = entityDataEntry{
		index:        dataFrameItemIndex,
		serializerID: itemStackSerializerID,
		value:        &fi,
	}.WriteTo(&buf)
	// DATA_ROTATION (index 10, serializer INT=1): the 0..7 rotation, VAR_INT.
	_, _ = entityDataEntry{
		index:        dataFrameRotationIndex,
		serializerID: intSerializerID,
		value:        pk.VarInt(e.frameRotation),
	}.WriteTo(&buf)
	return buf.Bytes()
}

// encodeArmorStandMetadata builds the SynchedEntityData DataValue bytes for an ArmorStand: the single
// DATA_CLIENT_FLAGS byte entry (index 15, serializer BYTE=0), WITHOUT the 0xFF terminator. The 6 pose
// Rotations are DEFERRED (the client renders its own DEFAULT_*_POSE when no pose entry is sent). Result
// goes into e.metadata for the tracker's SetEntityData.
func encodeArmorStandMetadata(e *Entity) []byte {
	var buf bytes.Buffer
	_, _ = entityDataEntry{
		index:        dataArmorStandClientFlagsIndex,
		serializerID: byteSerializerID,
		value:        pk.Byte(int8(e.armorStandFlags)),
	}.WriteTo(&buf)
	return buf.Bytes()
}

// pushFrameData re-encodes the frame's metadata and pushes a live ClientboundSetEntityData to every
// tracking observer so a place-item / rotate is reflected immediately (vanilla's SynchedEntityData.set
// -> dirty -> the ServerEntity broadcast). Mirrors the live SetEntityData push the equipment-swap /
// eat-pose paths use (encodeSetEntityData over the refreshed metadata).
func (t *TickLoop) pushFrameData(e *Entity) {
	e.metadata = encodeFrameMetadata(e)
	pkt := encodeSetEntityData(e)
	t.broadcastToTrackers(e.id, pkt)
}

// =================================================================================================
// ITEM FRAME — interact (place item / rotate) + break (drop)
// =================================================================================================

// getFrameItem is ItemFrame.getItem() -> DATA_ITEM (the framed stack, EMPTY if none).
func (e *Entity) getFrameItem() component.SlotData { return e.frameItem }

// setFrameItem is ItemFrame.setItem(stack) -> setItem(stack, true): store the stack ALWAYS with Count 1
// (copyWithCount(1)), refresh the metadata. The updateNeighbourForOutputSignal (comparator) side effect
// is a cited no-op (no comparator reads a frame in v1). CITE ItemFrame.setItem: copyWithCount(1) ->
// DATA_ITEM.set.
func (e *Entity) setFrameItem(stack component.SlotData) {
	if stack.Count > 0 {
		stack.Count = 1 // copyWithCount(1): a frame always holds exactly one
	}
	e.frameItem = stack
}

// getFrameRotation is ItemFrame.getRotation() -> DATA_ROTATION.
func (e *Entity) getFrameRotation() int32 { return e.frameRotation }

// setFrameRotation is ItemFrame.setRotation(int) -> setRotation(int, true): store `r % 8` (the 8-step
// wrap). CITE ItemFrame.setRotation(int,bool): DATA_ROTATION.set(rotation % 8).
func (e *Entity) setFrameRotation(r int32) {
	e.frameRotation = ((r % frameNumRotations) + frameNumRotations) % frameNumRotations
}

// frameGetAnalogOutput is ItemFrame.getAnalogOutput(): the comparator read of a framed item -- 0 when the
// frame is empty, else (getRotation() % 8) + 1 (a value 1..8). CITE ItemFrame.getAnalogOutput:
// `getItem().isEmpty() ? 0 : getRotation() % 8 + 1`. (frameRotation is already stored %8 by
// setFrameRotation, so the extra %8 is a faithful no-op guard.)
func (e *Entity) frameGetAnalogOutput() int {
	if slotIsEmpty(e.getFrameItem()) {
		return 0
	}
	return int(e.getFrameRotation()%frameNumRotations) + 1
}

// frameFrameItemStack is ItemFrame.getFrameItemStack() -> new ItemStack(Items.ITEM_FRAME) (or
// GLOW_ITEM_FRAME for a glow frame): the frame's OWN drop item. CITE ItemFrame / GlowItemFrame.getFrameItemStack.
func (e *Entity) frameFrameItemStack() component.SlotData {
	if e.frameGlow {
		return itemStackOf(item.GlowItemFrame)
	}
	return itemStackOf(item.ItemFrame)
}

// tryItemFrameInteract is the 1:1 port of net.minecraft.world.entity.decoration.ItemFrame.interact(
// Player, InteractionHand, Vec3) — the SERVER branch (isClientSide() is always false here). Returns true
// when the interact belongs to the frame (SUCCESS/FAIL/PASS all consume the click for a frame — a frame is
// not fed). The held item is read from the player's selected hand (main hand, tick-owned).
//
//	if (fixed) return PASS;                                             // v1: fixed const-false
//	if (!frameHasItem) {                                                // EMPTY frame
//	    if (hasHeldItem && !isRemoved()) {
//	        // (map limit FAIL branch: deferred — a non-map item never trips it)
//	        setItem(held); gameEvent(BLOCK_CHANGE); held.consume(1, player); return SUCCESS;
//	    }
//	    return PASS;
//	}
//	// frame HAS an item -> rotate
//	playSound(getRotateItemSound()); setRotation(getRotation()+1); gameEvent(BLOCK_CHANGE); return SUCCESS;
//
//	[VERIFIED javap ItemFrame.interact — full bytecode traced this session.]
func (t *TickLoop) tryItemFrameInteract(p *tickPlayer, frame *Entity) bool {
	if frameFixed {
		return true // fixed -> PASS (still consumes the click for a frame)
	}
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	frameHasItem := !slotIsEmpty(frame.getFrameItem())
	hasHeldItem := !slotIsEmpty(held)

	if !frameHasItem {
		if hasHeldItem {
			// MAP LIMIT (deferred): MapItem.getSavedData(held) != null && isTrackedCountOverLimit(256)
			// -> FAIL. No map subsystem, and a non-map item's saved data is null, so the branch is a
			// cited no-op (never FAILs). CITE ItemFrame.interact map branch.
			frame.setFrameItem(held)     // setItem(held): store copyWithCount(1)
			t.pushFrameData(frame)        // gameEvent(BLOCK_CHANGE) + the synched-data broadcast
			t.frameUpdateComparators(frame) // setItem(stack,true): updateNeighbourForOutputSignal(pos, AIR)
			if p.gameMode != gameModeCreative {
				t.shrinkHeldItem(p, inv) // itemStack.consume(1, player): shrink UNLESS creative
			}
			return true // SUCCESS
		}
		return true // empty frame + empty hand -> PASS (still the frame's click)
	}
	// frame already has an item -> rotate (+1, %8).
	frame.setFrameRotation(frame.getFrameRotation() + 1)
	t.pushFrameData(frame)
	t.frameUpdateComparators(frame) // setRotation(r,true): updateNeighbourForOutputSignal(pos, AIR)
	return true // SUCCESS
}

// frameUpdateComparators is the ItemFrame setItem/setRotation `updateNeighbours` side effect:
// level.updateNeighbourForOutputSignal(this.pos, Blocks.AIR) at the frame's ATTACHED block cell (this.pos,
// the wall cell it hangs in), so a comparator reading the frame's analog output re-evaluates on a live
// place / rotate / empty. CITE ItemFrame.setItem(stack,true) / setRotation(int,true).
func (t *TickLoop) frameUpdateComparators(frame *Entity) {
	t.updateNeighbourForOutputSignal(pk.Position{X: frame.frameBlockX, Y: frame.frameBlockY, Z: frame.frameBlockZ})
}

// breakItemFrame is the port of the ItemFrame hurt/break drop chain for a player attack
// (handleMobAttack routes here). It reproduces the exact vanilla split:
//
//   - shouldDamageDropItem(source) == !IS_EXPLOSION && !getItem().isEmpty(): a non-explosion attack on a
//     frame that HOLDS an item pops JUST the framed contents (dropItem withFrame=false) and the frame
//     survives (emptied). Returns WITHOUT removing the entity.
//   - otherwise (the frame is EMPTY, or the fall-through): BlockAttachedEntity.hurtServer removes the
//     frame (kill) and calls dropItem withFrame=true — the frame item + any remaining contents.
//
// A player attack is never IS_EXPLOSION, so `shouldDamageDropItem` reduces to `frameHasItem`.
//
//	[VERIFIED javap ItemFrame.hurtServer: fixed guard; isInvulnerableToBase; shouldDamageDropItem ->
//	 dropItem(level, attacker, false) + gameEvent + sound + return true; else super.hurtServer ->
//	 BlockAttachedEntity.hurtServer -> kill + markHurt + dropItem(level, attacker) -> dropItem(...,true).]
func (t *TickLoop) breakItemFrame(frame *Entity, attacker *tickPlayer) {
	creative := attacker != nil && attacker.gameMode == gameModeCreative
	frameHasItem := !slotIsEmpty(frame.getFrameItem())

	if frameHasItem {
		// shouldDamageDropItem true -> dropItem(withFrame=false): drop only the framed contents, the
		// frame stays (emptied). Does NOT remove the entity.
		t.frameDropItem(frame, creative, false)
		t.pushFrameData(frame) // gameEvent(BLOCK_CHANGE) + the now-empty DATA_ITEM to the client
		t.frameUpdateComparators(frame) // dropItem -> setItem(EMPTY,true): comparator falls to 0
		return
	}
	// EMPTY frame: BlockAttachedEntity.hurtServer -> kill + dropItem(withFrame=true) -> drop the frame item.
	t.frameDropItem(frame, creative, true)
	t.removeFrameEntity(frame)
}

// frameDropItem is the private ItemFrame.dropItem(ServerLevel, Entity causedBy, boolean withFrame) core:
//
//	if (fixed) return;                                                   // v1: const-false
//	ItemStack it = getItem(); setItem(EMPTY);                            // clear the frame
//	if (!ENTITY_DROPS) { ...; return; }                                 // v1: gamerule true -> skip
//	if (causedBy instanceof Player && hasInfiniteMaterials()) { ...; return; }  // creative: NO drops
//	if (withFrame) spawnAtLocation(getFrameItemStack());                // drop the frame item
//	if (!it.isEmpty()) { removeFramedMap(it); if (random.nextFloat() < dropChance) spawnAtLocation(it); }
//
// dropChance default 1.0f, so the framed-item pop is unconditional (the nextFloat() draw is on the shared
// world RNG the ItemEntity toss already draws from — order-preserved: frame item first, then contents).
//
//	[VERIFIED javap ItemFrame.dropItem(ServerLevel, Entity, boolean) — full bytecode traced this session.]
func (t *TickLoop) frameDropItem(frame *Entity, creative, withFrame bool) {
	// getItem() then setItem(EMPTY): capture the contents, clear the frame.
	contents := frame.getFrameItem()
	frame.setFrameItem(component.SlotData{Count: 0})

	if creative {
		return // hasInfiniteMaterials(): removeFramedMap (no-op) + return, NO drops.
	}
	if withFrame {
		// spawnAtLocation(getFrameItemStack()): drop the item_frame / glow_item_frame item.
		t.frameSpawnAtLocation(frame, frame.frameFrameItemStack())
	}
	if !slotIsEmpty(contents) {
		// removeFramedMap(copy): a cited no-op (no map data). Then random.nextFloat() < dropChance(1.0)
		// -> always drop the framed contents.
		t.frameSpawnAtLocation(frame, contents)
	}
}

// frameSpawnAtLocation is HangingEntity.spawnAtLocation(level, stack): a dropped ItemEntity offset off the
// wall face along the frame's facing (getX + stepX*0.15, getY, getZ + stepZ*0.15). Reuses NewItemEntity
// (the popResource toss + pickup delay + ITEM metadata) so the drop behaves exactly like every other
// dropped item. CITE HangingEntity.spawnAtLocation directional offset.
func (t *TickLoop) frameSpawnAtLocation(frame *Entity, stack component.SlotData) {
	if slotIsEmpty(stack) {
		return
	}
	dx, _, dz := direction3DStep(int(frame.frameDirection))
	x := frame.x + float64(dx)*frameSpawnStackOffset
	y := frame.y
	z := frame.z + float64(dz)*frameSpawnStackOffset
	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, stack)
	t.cur().entities.add(ie)
}

// removeFrameEntity removes a broken frame from its owning region's store (the kill/discard). The tracker
// sends ClientboundRemoveEntities to every observer next tick (near() no longer returns it), the same
// discard seam the creeper/boat/fangs removals ride. Runs in the region context the break registered.
func (t *TickLoop) removeFrameEntity(frame *Entity) {
	t.cur().entities.remove(frame.id)
}

// =================================================================================================
// ARMOR STAND — interact (equip / take a slot) + break (drop the stand + its equipment)
// =================================================================================================

// isArmorStandSmall / showArms / isMarker read the DATA_CLIENT_FLAGS bits (CITE ArmorStand.isSmall/
// showArms/isMarker). showBasePlate is inverted (bit clear == shown) — unused server-side, so not modeled.
func (e *Entity) isArmorStandSmall() bool { return e.armorStandFlags&armorStandFlagSmall != 0 }
func (e *Entity) armorStandShowArms() bool {
	return e.armorStandFlags&armorStandFlagShowArms != 0
}
func (e *Entity) isArmorStandMarker() bool { return e.armorStandFlags&armorStandFlagMarker != 0 }

// tryArmorStandInteract is the 1:1 port of net.minecraft.world.entity.decoration.ArmorStand.interact(
// Player, InteractionHand, Vec3 location) — the SERVER branch. clickY is the location.y of the interact
// (ServerboundInteract's Vec3). Returns true when the interact belongs to the stand (a marker or a
// name-tag falls to super.interact -> a no-op consume; every other outcome is the stand's).
//
//	if (isMarker() || held.is(NAME_TAG)) return super.interact(...);    // marker/name-tag -> base no-op
//	if (isSpectator()) return SUCCESS;                                  // v1: const-false
//	EquipmentSlot itemInHandSlot = getEquipmentSlotForItem(held);
//	if (held.isEmpty()) {                                               // EMPTY hand -> TAKE
//	    EquipmentSlot clicked = getClickedSlot(location);
//	    EquipmentSlot target = isDisabled(clicked) ? itemInHandSlot : clicked;
//	    if (hasItemInSlot(target) && swapItem(player, target, held, hand)) return SUCCESS_SERVER;
//	} else {                                                            // holding -> EQUIP
//	    if (isDisabled(itemInHandSlot)) return FAIL;
//	    if (itemInHandSlot.getType()==HAND && !showArms()) return FAIL;
//	    if (swapItem(player, itemInHandSlot, held, hand)) return SUCCESS_SERVER;
//	}
//	return super.interact(...);
//
//	[VERIFIED javap ArmorStand.interact + getClickedSlot + swapItem — full bytecode traced this session.]
func (t *TickLoop) tryArmorStandInteract(p *tickPlayer, stand *Entity, clickY float64) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))

	if stand.isArmorStandMarker() {
		return true // marker -> super.interact (a marker is non-interactable) — still consumes the click
	}
	// (held.is(NAME_TAG) -> super.interact: a name tag renames — deferred no-op consume. A NAME_TAG in
	// hand on a stand simply consumes with no equip; modeled as the base no-op below only when the
	// item is a name tag. v1 has no rename subsystem, so we treat NAME_TAG as a base no-op consume.)
	if !slotIsEmpty(held) && int32(held.ItemID) == int32(item.NameTag.ID) {
		return true // NAME_TAG -> super.interact base no-op (rename deferred)
	}

	// isSpectator(): const-false in v1 (no spectator mode wired). isClientSide() is always false (server).
	itemInHandSlot := armorStandEquipmentSlotForItem(stand, held)

	if slotIsEmpty(held) {
		// EMPTY hand -> TAKE the clicked slot.
		clicked := stand.getClickedSlot(clickY)
		target := clicked
		if armorStandIsDisabled(stand, clicked) {
			target = itemInHandSlot
		}
		if !slotIsEmpty(stand.getItemBySlot(target)) && t.armorStandSwapItem(p, stand, target, held, inv) {
			return true // SUCCESS_SERVER
		}
		return true // hasItemInSlot false / swap refused -> super.interact base no-op (still the stand's click)
	}

	// holding an item -> EQUIP.
	if armorStandIsDisabled(stand, itemInHandSlot) {
		return true // FAIL (still consumes the click)
	}
	if armorStandSlotIsHand(itemInHandSlot) && !stand.armorStandShowArms() {
		return true // a hand slot with arms hidden -> FAIL
	}
	if t.armorStandSwapItem(p, stand, itemInHandSlot, held, inv) {
		return true // SUCCESS_SERVER
	}
	return true // super.interact base no-op
}

// getClickedSlot is the 1:1 port of ArmorStand.getClickedSlot(Vec3) — the y-threshold slot picker. clickY
// is location.y; vanilla divides by getScale()*getAgeScale() (both 1.0 for a non-small, adult stand — a
// small stand IS an age-scaled model, but v1 places only full-size stands, so the divisor is 1.0; the
// `small` branch thresholds are still ported for fidelity). Returns the EquipmentSlot ordinal (eqSlot*).
//
//	EquipmentSlot slot = MAINHAND; boolean small = isSmall();
//	double y = location.y / (getScale() * getAgeScale());
//	if (y >= 0.1  && y < 0.1 + (small?0.8:0.45) && hasItemInSlot(FEET))  return FEET;
//	if (y >= 0.9+(small?0.3:0.0) && y < 0.9+(small?0.3:0.0)+(small?1.0:0.7) && hasItemInSlot(CHEST)) return CHEST;
//	if (y >= 0.4  && y < 0.4 + (small?1.0:0.8) && hasItemInSlot(LEGS))   return LEGS;
//	if (y >= 1.6  && hasItemInSlot(HEAD))                                return HEAD;
//	if (hasItemInSlot(MAINHAND))  return MAINHAND;
//	if (!hasItemInSlot(OFFHAND))  return MAINHAND;
//	return OFFHAND;
//
//	[VERIFIED javap ArmorStand.getClickedSlot — full bytecode traced this session.]
func (e *Entity) getClickedSlot(clickY float64) int {
	small := e.isArmorStandSmall()
	// getScale()*getAgeScale() == 1.0 for a full-size adult stand (v1 places only these). VERIFIED javap.
	y := clickY // / (scale * ageScale) == /1.0

	if y >= 0.1 {
		d := 0.45
		if small {
			d = 0.8
		}
		if y < 0.1+d && !slotIsEmpty(e.getItemBySlot(eqSlotFeet)) {
			return eqSlotFeet
		}
	}
	base := 0.0
	if small {
		base = 0.3
	}
	if y >= 0.9+base {
		d2 := 0.7
		if small {
			d2 = 1.0
		}
		if y < 0.9+base+d2 && !slotIsEmpty(e.getItemBySlot(eqSlotChest)) {
			return eqSlotChest
		}
	}
	if y >= 0.4 {
		d3 := 0.8
		if small {
			d3 = 1.0
		}
		if y < 0.4+d3 && !slotIsEmpty(e.getItemBySlot(eqSlotLegs)) {
			return eqSlotLegs
		}
	}
	if y >= 1.6 && !slotIsEmpty(e.getItemBySlot(eqSlotHead)) {
		return eqSlotHead
	}
	if !slotIsEmpty(e.getItemBySlot(eqSlotMainHand)) {
		return eqSlotMainHand
	}
	if slotIsEmpty(e.getItemBySlot(eqSlotOffHand)) {
		return eqSlotMainHand
	}
	return eqSlotOffHand
}

// armorStandSwapItem is the 1:1 port of ArmorStand.swapItem(Player, EquipmentSlot, ItemStack, Hand). The
// take/put disabled-lock branches (disabledSlots offset 8/16) are const-false (disabledSlots default 0),
// ported structurally. Returns whether the swap happened.
//
//	ItemStack cur = getItemBySlot(slot);
//	if (!cur.isEmpty() && (disabledSlots & (1<<slot.getFilterBit(8))) != 0) return false;   // take-locked
//	if ( cur.isEmpty() && (disabledSlots & (1<<slot.getFilterBit(16)))!= 0) return false;   // put-locked
//	if (hasInfiniteMaterials() && cur.isEmpty() && !held.isEmpty()) { setItemSlot(slot, held.copyWithCount(1)); return true; }
//	if (!held.isEmpty() && held.getCount() > 1) { if (!cur.isEmpty()) return false; setItemSlot(slot, held.split(1)); return true; }
//	setItemSlot(slot, held); player.setItemInHand(hand, cur); return true;
//
//	[VERIFIED javap ArmorStand.swapItem — full bytecode traced this session.]
func (t *TickLoop) armorStandSwapItem(p *tickPlayer, stand *Entity, slot int, held component.SlotData, inv *Inventory) bool {
	cur := stand.getItemBySlot(slot)
	// disabledSlots == 0 in v1 -> the take/put lock branches never fire (const-false). CITE swapItem.

	creative := p.gameMode == gameModeCreative
	if creative && slotIsEmpty(cur) && !slotIsEmpty(held) {
		// CREATIVE: place a copy (count 1), do NOT take the player's item.
		placed := held
		placed.Count = 1
		stand.setItemSlot(slot, placed)
		t.pushArmorStandEquipment(stand, slot)
		return true
	}
	if !slotIsEmpty(held) && held.Count > 1 {
		if !slotIsEmpty(cur) {
			return false // won't swap a stack>1 into an occupied slot
		}
		// split(1): place one, decrement the held stack by one (the rest stays in hand).
		one := held
		one.Count = 1
		stand.setItemSlot(slot, one)
		t.pushArmorStandEquipment(stand, slot)
		t.shrinkHeldItem(p, inv) // held.split(1) removed one from the hand stack
		return true
	}
	// stack of 1 (or empty hand): straight swap — the slot's old item goes to the hand, the held (count-1
	// or empty) goes to the slot. This is BOTH the equip (held count 1 -> slot) and the take (empty hand,
	// cur -> hand) case. player.setItemInHand(hand, cur) writes the slot's old item into the selected hand
	// and re-sends the authoritative content (the milk-bucket set-hand seam, attack_dispatch.go).
	stand.setItemSlot(slot, held)
	inv.set(heldWindowSlot(inv.heldSlot), cur) // player.setItemInHand(hand, cur)
	t.sendContent(p)
	t.pushArmorStandEquipment(stand, slot)
	return true
}

// pushArmorStandEquipment broadcasts the changed slot's ClientboundSetEquipment to every observer so a
// live equip/take is reflected immediately (the ServerEntity equipment-diff broadcast). Reuses
// encodeSetEquipment (the same single-slot framing equipmentSpawnPackets emits at spawn).
func (t *TickLoop) pushArmorStandEquipment(stand *Entity, slot int) {
	pkt := encodeSetEquipment(stand.id, slot, stand.getItemBySlot(slot))
	t.broadcastToTrackers(stand.id, pkt)
}

// armorStandEquipmentSlotForItem is LivingEntity.getEquipmentSlotForItem(stack): the slot the held item is
// worn in (a helmet -> HEAD), via the item's EQUIPPABLE data component's slot(), gated by canUseSlot.
// Sulfur's item table carries no EQUIPPABLE component, so we classify by the vanilla armor item's name
// suffix (the observable Equippable.slot() for every vanilla armor piece): *_helmet/turtle_helmet/carved_
// pumpkin -> HEAD, *_chestplate/elytra -> CHEST, *_leggings -> LEGS, *_boots -> FEET; everything else ->
// MAINHAND. canUseSlot excludes BODY/SADDLE/disabled — none of those apply to a HAND/armor classification
// here. CITE LivingEntity.getEquipmentSlotForItem -> Equippable.slot(); the armor Equippable slots.
func armorStandEquipmentSlotForItem(stand *Entity, stack component.SlotData) int {
	if slotIsEmpty(stack) {
		return eqSlotMainHand
	}
	it := item.ByID[item.ID(stack.ItemID)]
	if it == nil {
		return eqSlotMainHand
	}
	slot := armorSlotForItemName(it.Name)
	// canUseSlot(slot): BODY/SADDLE excluded (armorSlotForItemName never returns them) and !isDisabled
	// (disabledSlots 0 in v1). So the classified slot is always usable.
	return slot
}

// armorSlotForItemName maps a vanilla armor item's registry name to its Equippable slot ordinal — the
// observable Equippable.slot() for each armor family. A non-armor item -> MAINHAND. CITE the vanilla armor
// Equippable components (LEATHER/COPPER/GOLDEN/CHAINMAIL/IRON/DIAMOND/NETHERITE_{HELMET,CHESTPLATE,
// LEGGINGS,BOOTS} + TURTLE_HELMET (HEAD) + ELYTRA (CHEST) + CARVED_PUMPKIN (HEAD)).
func armorSlotForItemName(name string) int {
	switch {
	case hasSuffix(name, "_helmet") || name == "turtle_helmet" || name == "carved_pumpkin":
		return eqSlotHead
	case hasSuffix(name, "_chestplate") || name == "elytra":
		return eqSlotChest
	case hasSuffix(name, "_leggings"):
		return eqSlotLegs
	case hasSuffix(name, "_boots"):
		return eqSlotFeet
	default:
		return eqSlotMainHand
	}
}

// hasSuffix is a tiny stdlib-free suffix check (strings.HasSuffix would pull the import for one use; kept
// local and allocation-free).
func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

// armorStandIsDisabled is ArmorStand.isDisabled(slot): (disabledSlots & (1<<slot.getFilterBit(0))) != 0
// || (slot.getType()==HAND && !showArms()). disabledSlots is 0 in v1, so it reduces to the hand-slot
// arms-hidden guard. CITE ArmorStand.isDisabled.
func armorStandIsDisabled(stand *Entity, slot int) bool {
	// disabledSlots == 0: the first term is const-false. The hand-slot guard: a MAINHAND/OFFHAND slot on a
	// stand with arms hidden is disabled.
	return armorStandSlotIsHand(slot) && !stand.armorStandShowArms()
}

// armorStandSlotIsHand reports whether a slot ordinal is a HAND slot (MAINHAND/OFFHAND) — EquipmentSlot
// .Type.HAND. CITE EquipmentSlot: MAINHAND/OFFHAND are Type.HAND; FEET/LEGS/CHEST/HEAD are HUMANOID_ARMOR.
func armorStandSlotIsHand(slot int) bool {
	return slot == eqSlotMainHand || slot == eqSlotOffHand
}

// armorStandWobbleEvent is ArmorStand.hurtServer's `broadcastEntityEvent(this, (byte) 32)` on the FIRST
// punch — the client hit-flash/wobble. CITE ArmorStand.hurtServer.
const armorStandWobbleEvent byte = 32

// hitArmorStand is the 1:1 port of the PLAYER-attack arm of net.minecraft.world.entity.decoration
// .ArmorStand.hurtServer (the CAN_BREAK_ARMOR_STAND path a player_attack takes — player_attack is in the
// CAN_BREAK_ARMOR_STAND tag, not ALWAYS_KILLS, so allowIncrementalBreaking is true, shouldKill false):
//
//	if (isRemoved()) return false;
//	// (MOB_GRIEFING / BYPASSES / invuln / invisible / marker / IS_EXPLOSION / fire tag branches: a plain
//	//  player_attack matches none — invisible/marker are v1 const-false for a placed stand)
//	boolean shouldKill = source.is(ALWAYS_KILLS_ARMOR_STANDS);       // player_attack: false
//	// allowIncrementalBreaking (CAN_BREAK_ARMOR_STAND): true for player_attack -> not an early return
//	if (entity instanceof Player p && !p.getAbilities().mayBuild) return false;   // v1: mayBuild true
//	if (source.isCreativePlayer()) { sound/particles; kill; return true; }        // creative instant break
//	long time = getGameTime();
//	if (time - lastHit <= 5L || shouldKill) { brokenByPlayer; particles; kill; }  // 2nd hit within 5t -> break
//	else { broadcastEntityEvent(32); gameEvent(ENTITY_DAMAGE); lastHit = time; }  // 1st hit -> wobble
//
//	[VERIFIED javap ArmorStand.hurtServer — the CAN_BREAK/lastHit window traced this session.]
func (t *TickLoop) hitArmorStand(stand *Entity, attacker *tickPlayer) {
	// invisible/marker are const-false for a v1-placed stand (isMarker gate below is the real read); a
	// marker stand is not attackable (attackable() false) so it never reaches here, but guard anyway.
	if stand.isArmorStandMarker() {
		return
	}
	// Creative punch: instant break with NO item drops (sound + particles + kill). breakArmorStand's own
	// creative branch performs the no-drop kill.
	if attacker != nil && attacker.gameMode == gameModeCreative {
		t.breakArmorStand(stand, attacker)
		return
	}
	time := t.gametime
	if time-stand.armorStandLastHit <= 5 {
		// SECOND hit within 5 ticks -> brokenByPlayer (drop the stand + equipment) + kill.
		t.breakArmorStand(stand, attacker)
		return
	}
	// FIRST hit -> wobble only: broadcast the hit event (32), record the time.
	t.broadcastToTrackers(stand.id, encodeEntityEvent(stand.id, armorStandWobbleEvent))
	stand.armorStandLastHit = time
}

// breakArmorStand is the port of the survival-player break of an ArmorStand (handleMobAttack routes here
// after the double-hit window). It reproduces brokenByPlayer -> brokenByAnything:
//
//	brokenByPlayer: popResource(new ItemStack(ARMOR_STAND)) at blockPosition(); then brokenByAnything.
//	brokenByAnything: dropAllDeathLoot (the ARMOR_STAND loot table -> the armor_stand item — v1 models the
//	  loot table AS the single armor_stand drop, folded into the brokenByPlayer pop so the stand drops
//	  EXACTLY one armor_stand item + its equipment, matching the observable vanilla drop); then for each
//	  EquipmentSlot in VALUES order: itemStack = equipment.set(slot, EMPTY); if empty/prevent-drop skip;
//	  else popResource(itemStack) at blockPosition().above().
//
// Creative: no drops (the hurtServer creative branch: sound + particles + kill only) — the caller gates that.
//
//	[VERIFIED javap ArmorStand.brokenByPlayer (popResource ARMOR_STAND) + brokenByAnything (dropAllDeathLoot
//	 + the EquipmentSlot.VALUES loop popResource above()).]
func (t *TickLoop) breakArmorStand(stand *Entity, attacker *tickPlayer) {
	if attacker != nil && attacker.gameMode == gameModeCreative {
		// hurtServer creative branch: kill, no drops.
		t.removeArmorStandEntity(stand)
		return
	}
	// brokenByPlayer: popResource(new ItemStack(ARMOR_STAND)) at blockPosition(). The dropAllDeathLoot
	// armor-stand loot table yields exactly the armor_stand item; v1 folds it into this single pop so the
	// observable drop is one armor_stand item (no double-drop) + the equipment below.
	bx, by, bz := standBlockPos(stand)
	t.popResourceAt(bx, by, bz, itemStackOf(item.ArmorStand))

	// brokenByAnything equipment loop: EquipmentSlot.VALUES order (MAINHAND, OFFHAND, FEET, LEGS, CHEST,
	// HEAD, BODY, SADDLE); pop each non-empty at blockPosition().above().
	for slot := 0; slot < equipmentSlotCount; slot++ {
		it := stand.getItemBySlot(slot)
		stand.setItemSlot(slot, component.SlotData{Count: 0}) // equipment.set(slot, EMPTY)
		if slotIsEmpty(it) {
			continue // empties skipped (+ the PREVENT_EQUIPMENT_DROP enchant skip — no enchants in v1)
		}
		t.popResourceAt(bx, by+1, bz, it) // blockPosition().above()
	}
	t.removeArmorStandEntity(stand)
}

// removeArmorStandEntity removes a broken armor stand from its owning region's store. The tracker sends
// ClientboundRemoveEntities next tick (near() no longer returns it). Runs in the region context the break
// registered.
func (t *TickLoop) removeArmorStandEntity(stand *Entity) {
	t.cur().entities.remove(stand.id)
}

// standBlockPos is ArmorStand.blockPosition() — the floor block cell of the stand's feet position
// (floor(x), floor(y), floor(z)). Used as the popResource origin.
func standBlockPos(stand *Entity) (int, int, int) {
	return floorInt(stand.x), floorInt(stand.y), floorInt(stand.z)
}

// popResourceAt is Block.popResource(level, pos, stack) for an arbitrary stack at a block cell: the drop
// spawns at the cell center + per-axis ±0.25 jitter, with the Y offset down by the item half-height, and
// the popResource random toss velocity — exactly the geometry spawnBlockDrop uses (NewItemEntity gives the
// toss + pickup delay + metadata). CITE Block.popResource.
func (t *TickLoop) popResourceAt(bx, by, bz int, stack component.SlotData) {
	if slotIsEmpty(stack) {
		return
	}
	x := float64(bx) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
	y := float64(by) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter) - itemEntityHalfHeight
	z := float64(bz) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, stack)
	t.cur().entities.add(ie)
}

// =================================================================================================
// PLACEMENT (ArmorStandItem.useOn + ItemFrame placement) + shared Direction helpers
// =================================================================================================

// direction3DStep maps a Direction 3D-data value to its unit step (getStepX/Y/Z): DOWN=0(0,-1,0),
// UP=1(0,1,0), NORTH=2(0,0,-1), SOUTH=3(0,0,1), WEST=4(-1,0,0), EAST=5(1,0,0). CITE Direction.get3DDataValue
// / getStep{X,Y,Z}. (directionNormal in block_interact.go is the same table; this is the display-entity-
// local copy keyed by 3D-data value for the frame geometry.)
func direction3DStep(d int) (int, int, int) {
	switch d {
	case 0:
		return 0, -1, 0
	case 1:
		return 0, 1, 0
	case 2:
		return 0, 0, -1
	case 3:
		return 0, 0, 1
	case 4:
		return -1, 0, 0
	case 5:
		return 1, 0, 0
	default:
		return 0, 0, 1 // SOUTH default (DEFAULT_DIRECTION)
	}
}

// frameFacingAngles is the yaw/pitch ItemFrame.setDirection writes for a facing (server book-keeping; the
// client re-derives render from spawnData). A HORIZONTAL facing: pitch 0, yaw = get2DDataValue*90
// (SOUTH=0,WEST=90,NORTH=180,EAST=270). A VERTICAL facing: yaw 0, pitch = -90*step (UP -> -90, DOWN -> +90).
// CITE ItemFrame.setDirection.
func frameFacingAngles(direction3D int) (yaw, pitch float32) {
	switch direction3D {
	case 1: // UP
		return 0, -90
	case 0: // DOWN
		return 0, 90
	case 2: // NORTH
		return 180, 0
	case 3: // SOUTH
		return 0, 0
	case 4: // WEST
		return 90, 0
	case 5: // EAST
		return 270, 0
	default:
		return 0, 0
	}
}

// tryPlaceItemFrame is the port of the item_frame / glow_item_frame placement (HangingEntityItem.useOn):
// a frame item used on a block FACE spawns the frame on the wall behind that face, if it survives (the
// wall block is solid). Returns true when the held item is a frame item AND the placement consumed the
// action (so handleUseItemOn does not fall through to block placement). The frame ATTACHES to the clicked
// block face — the frame lives in the ADJACENT cell (pos + face normal) and looks OUT along the face; the
// support block is the clicked block itself (pos.relative(direction.opposite) from the frame's cell).
//
//	[VERIFIED javap HangingEntityItem.useOn: clickedPos = pos.relative(face); new ItemFrame(level,
//	 clickedPos, face); if (frame.survives()) { addFreshEntity; shrink } else FAIL. ItemFrame.survives:
//	 the support block pos.relative(getDirection().opposite) must be solid.]
func (t *TickLoop) tryPlaceItemFrame(p *tickPlayer, inv *Inventory, pos pk.Position, direction3D int) bool {
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) {
		return false
	}
	glow := false
	switch int32(held.ItemID) {
	case int32(item.ItemFrame.ID):
		glow = false
	case int32(item.GlowItemFrame.ID):
		glow = true
	default:
		return false // not a frame item
	}

	// clickedPos = pos.relative(face): the frame's cell is the block ADJACENT to the clicked face.
	dx, dy, dz := direction3DStep(direction3D)
	bx, by, bz := pos.X+dx, pos.Y+dy, pos.Z+dz

	// survives(): the support block (behind the frame's facing == the CLICKED block) must be solid. The
	// clicked block IS pos (frame's cell relative(direction.opposite) == the block we clicked). Check it.
	if !t.frameSupportSolid(pos) {
		return false // !survives() -> FAIL (no placement, the click is consumed as a failed use)
	}

	t.spawnItemFrame(bx, by, bz, direction3D, glow)

	// shrink the held frame item (survival). Creative keeps it.
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, inv)
	}
	return true
}

// frameSupportSolid reports whether the block at pos is a solid support the frame can hang on
// (ItemFrame.survives: state.isSolid()). v1 uses the block-collision/solid classification the placement
// obstruction path already relies on (a full solid cube is a valid support; air/water/replaceable is not).
// CITE ItemFrame.survives support check.
func (t *TickLoop) frameSupportSolid(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	// isSolid: a non-replaceable, non-air block. isReplaceableState covers air/water/lava/replaceable;
	// its complement is a solid support (the v1 subset of BlockState.isSolid the survives check needs).
	return !isReplaceableState(s)
}

// tryPlaceArmorStand is the 1:1 port of net.minecraft.world.item.ArmorStandItem.useOn(UseOnContext): an
// armor_stand item used on a block (any face except DOWN) spawns an ArmorStand at the adjacent cell's
// bottom-center, with the 45°-snapped yaw. Returns true when the held item is an armor_stand item AND the
// use consumed the action.
//
//	if (clickedFace == DOWN) return FAIL;
//	BlockPos pos = new BlockPlaceContext(ctx).getClickedPos();          // the adjacent cell (or replaced clicked)
//	Vec3 spawn = Vec3.atBottomCenterOf(pos);                            // x+0.5, y, z+0.5
//	AABB box = ARMOR_STAND.getDimensions().makeBoundingBox(spawn);
//	if (!level.noCollision(box) || !level.getEntities(box).isEmpty()) return FAIL;   // obstruction
//	ArmorStand e = ARMOR_STAND.create(...);
//	float yRot = floor((wrapDegrees(player.yaw - 180) + 22.5) / 45.0) * 45.0;        // 45° snap
//	e.snapTo(x, y, z, yRot, 0); addFreshEntity(e); shrink(1);
//
//	[VERIFIED javap ArmorStandItem.useOn — full bytecode traced this session.]
func (t *TickLoop) tryPlaceArmorStand(p *tickPlayer, inv *Inventory, pos pk.Position, direction3D int) bool {
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.ArmorStand.ID) {
		return false // not an armor_stand item
	}
	if direction3D == 0 {
		return false // clickedFace == DOWN -> FAIL (still a use of the item; not placed)
	}

	// BlockPlaceContext.getClickedPos(): the adjacent cell (pos + face normal) for a non-replaceable
	// clicked block — the common case (clicking a solid top face). v1 uses the adjacent cell directly.
	dx, dy, dz := direction3DStep(direction3D)
	bx, by, bz := pos.X+dx, pos.Y+dy, pos.Z+dz

	// spawn = Vec3.atBottomCenterOf(pos): the cell center X/Z, cell bottom Y.
	x := float64(bx) + 0.5
	y := float64(by)
	z := float64(bz) + 0.5

	// Obstruction: fail if a block collides OR an entity intersects the armor-stand-sized box at spawn.
	if t.armorStandPlacementObstructed(x, y, z) {
		return false // FAIL (obstructed) — the item is used but nothing spawns
	}

	// yRot = floor((wrapDegrees(player.yaw - 180) + 22.5) / 45.0) * 45.0 — the 45° snap over the player yaw.
	yRot := armorStandSnapYaw(p.yaw)
	t.spawnArmorStand(x, y, z, yRot)

	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, inv) // itemStack.shrink(1)
	}
	return true
}

// armorStandSnapYaw is ArmorStandItem.useOn's yaw snap: floor((wrapDegrees(yaw - 180) + 22.5) / 45) * 45.
// wrapDegreesF is Mth.wrapDegrees (folds into (-180, 180]). Mth.floor of the float quotient (the vanilla
// (float)Mth.floor(...) narrowing). CITE ArmorStandItem.useOn.
func armorStandSnapYaw(yaw float32) float32 {
	w := wrapDegreesF(yaw - 180.0)
	return float32(floorInt(float64((w+22.5)/45.0))) * 45.0
}

// armorStandPlacementObstructed is ArmorStandItem.useOn's !noCollision || !getEntities().isEmpty() guard
// for the armor-stand-sized box (width 0.5, height 1.975 centered on x/z, base at y). v1 checks the entity
// half (a stand may not spawn inside a player/mob) reusing the placement entity scan; the block-collision
// half is a cited reduction (a stand placed on a solid top face has an air cell above it — the common case).
func (t *TickLoop) armorStandPlacementObstructed(x, y, z float64) bool {
	w := entity.ArmorStand.Width
	h := entity.ArmorStand.Height
	hw := w / 2
	sx0, sy0, sz0 := x-hw, y, z-hw
	sx1, sy1, sz1 := x+hw, y+h, z+hw
	for _, e := range t.entitiesNearAcrossRegions(x, z, 1) {
		if e == nil || e.isItem {
			continue // dropped items do not block (blocksBuilding false)
		}
		ehw := e.width / 2
		ex0, ey0, ez0 := e.x-ehw, e.y, e.z-ehw
		ex1, ey1, ez1 := e.x+ehw, e.y+e.height, e.z+ehw
		if sx0 < ex1 && ex0 < sx1 && sy0 < ey1 && ey0 < sy1 && sz0 < ez1 && ez0 < sz1 {
			return true // an entity intersects the stand box -> obstructed
		}
	}
	return false
}
