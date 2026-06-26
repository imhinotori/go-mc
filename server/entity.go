package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/server/internal/bvh"

	"github.com/google/uuid"
)

type (
	Pos struct{ X, Y, Z float64 }
	Rot struct{ Yaw, Pitch float32 }
)

// --- ENT-01: the live entity instance ------------------------------------------------
//
// Entity is the live, spawned entity INSTANCE — a thing with a position — that lives in
// the tick-owned entityStore (entity_store.go). It is distinct from data/entity.Entity,
// which is the generated 776 TYPE-TABLE record (the per-type AABB dims / wire id / name).
// The instance REFERENCES the table: NewEntity copies the table's ID + Width/Height at
// spawn so the AABB/physics hot path never re-looks-up the table.
//
// SINGLE-OWNER (TICK-05): every field below is tick-owned game state — read and mutated
// ONLY by the tick goroutine (the store that holds these lives on TickLoop). The one
// off-tick value in this subsystem is the atomic EntityIDAllocator counter
// (entity_store.go), which crosses no Entity state.
//
// SNAPSHOT-FRIENDLY (ROADMAP Phase 6 note / 06-RESEARCH Pattern 1 "Critical"): the hot
// fields (pos/vel/angles/onGround/dims) are PLAIN VALUE types (float64/float32/bool), not
// pointers or maps, so the Phase-8 async entity tracker can copy a cheap, immutable
// snapshot of an Entity by value without chasing pointers or sharing mutable state. Keep
// it that way — do NOT replace a hot field with a pointer/slice/map. (metadata is the one
// reference field; it is a plain []byte so a snapshot copy is still a shallow byte-slice
// share the tracker can treat as immutable.)

// Entity is a live, spawned entity instance owned by the tick goroutine. Players are
// entities too (a player's instance would draw its id from the same allocator); this plan
// provides the model + store, and Plans 06-02/06-03 populate metadata, run the tracker,
// and move entities.
type Entity struct {
	// id is the server-issued, never-reused entity id from EntityIDAllocator. Both players
	// and entities draw from the same monotonic space so no id ever collides (the
	// replacement for the old hard-coded joinEntityID=1; 06-RESEARCH Pitfall 7).
	id int32

	// typ is the WIRE entity-type id (data/entity.ID) — what the AddEntity packet carries
	// for this instance's type. Copied from the table record at spawn.
	typ entity.ID

	// uuid is the entity's network UUID (the AddEntity packet's objectUUID / a player's
	// profile id). A plain value (16 bytes), snapshot-friendly.
	uuid uuid.UUID

	// x, y, z are the entity's world position (block space, Double on the wire). Tick-owned;
	// movement (Plan 06-03) updates these via entityStore.move so the bucket stays correct.
	x, y, z float64

	// vx, vy, vz are the entity's velocity (blocks/tick). Tick-owned; physics (Plan 06-03)
	// integrates these into the position. Zero for a freshly spawned entity.
	vx, vy, vz float64

	// yaw, pitch are the body look angles in degrees; headYaw is the separate head rotation
	// living entities carry (Float on the wire). Tick-owned plain values.
	yaw, pitch, headYaw float32

	// onGround mirrors the player movement flag for entities — whether the entity rests on
	// a solid block (physics, Plan 06-03). Tick-owned.
	onGround bool

	// width, height are the entity's AABB footprint, COPIED from the data/entity table at
	// spawn (NewEntity) so the AABB helper and physics never re-look-up the table. Plain
	// values, snapshot-friendly.
	width, height float64

	// metadata is the entity's wire metadata slot — a copy-friendly byte buffer the tracker
	// (Plan 06-02) fills with the SynchedEntityData entries for the AddEntity / SetEntityData
	// packets. A nil/empty slot is a freshly spawned entity with default metadata. Kept as a
	// plain []byte (not a map) so an Entity snapshot stays cheap.
	metadata []byte

	// --- ITEM-PICKUP (Plan 17-14): dropped-item lifecycle state ----------------------
	//
	// These mirror net.minecraft.world.entity.item.ItemEntity's private int fields (decompiled
	// from temp/cache/26.2-inner.jar this session). They are tick-owned plain ints, set ONLY
	// for an Item entity (typ==entity.Item.ID); for every other entity they stay at the zero
	// value and are never read (the item tick/pickup scan gate on isItem). Kept as plain values
	// so the snapshot-friendly contract above is preserved.

	// itemStack is the dropped stack this Item entity carries — the SAME component.SlotData the
	// inventory holds, so pickup hands it straight to Inventory.add with no item-codec surface.
	// Zero value (Count==0) for a non-item entity. The ITEM SynchedEntityData metadata (the
	// render data-value) is built FROM this at spawn (encodeItemMetadata).
	itemStack component.SlotData

	// isItem marks this entity as a dropped Item (typ==entity.Item.ID). The item tick (gravity
	// 0.04 + age + despawn) and the player pickup scan run ONLY for entities with this set, so a
	// mob/player is never mistaken for a pickup. Set at spawn by spawnBlockDrop / NewItemEntity.
	isItem bool

	// pickupDelay is ItemEntity.pickupDelay: the ticks a fresh drop is NOT pickable
	// (setDefaultPickUpDelay() == 10). The item tick decrements it toward 0 each tick; playerTouch
	// refuses pickup while it is > 0. Vanilla's INFINITE sentinel (32767) is honored (never
	// decremented, never pickable) for completeness.
	pickupDelay int

	// age is ItemEntity.age: ticks since spawn. The item tick increments it and DISCARDS the
	// entity at LIFETIME (6000 == 5 minutes) — the vanilla despawn. The -32768 INFINITE_LIFETIME
	// sentinel is honored (never aged, never despawns).
	age int

	// ai is the per-mob AI handle (AI-01, Plan 07-01): the mob's goalSelector + the
	// navigation/look targets a goal writes (server/ai_mob.go). nil for a non-mob entity (a
	// dropped item, a player's instance) and for a mob with no AI registered. Hung off the
	// Entity (not a side map) so a snapshot/move carries it with the instance; it is tick-owned
	// game state mutated ONLY by the tick goroutine (TICK-05) via serverAiStep. Plan 07-03
	// wires the tickAI() call site that drives it; this plan builds the machinery + the hook.
	// NOTE: unlike the plain-value hot fields above, ai is a pointer to mutable tick-owned
	// state — it is NOT part of the snapshot-friendly value set the async tracker copies (the
	// tracker only ever reads the pos/angle/dims), so it does not break the snapshot contract.
	ai *mobAI

	// --- GAMEPLAY-07: delta-move tracking state (ServerEntity.sendChanges) ----------------
	//
	// These mirror net.minecraft.server.level.ServerEntity's per-entity send state so the
	// tracker can emit DELTA move packets (MoveEntityPos/PosRot/Rot, ~6 bytes) for small steps
	// instead of an absolute teleport every tick (~32 bytes). One ServerEntity exists per
	// tracked entity (NOT per observer), so this state is correctly per-Entity here. Tick-owned;
	// the tracker reads+writes them on the owner goroutine. moveInit guards the first send (the
	// VecDeltaCodec base + the lastSent* must be seeded from the spawn position before any delta
	// is computed, exactly as ServerEntity's ctor sets positionCodec.setBase to the spawn pos).
	moveInit             bool
	lastSentX, lastSentY float64 // VecDeltaCodec base (the last absolute pos the client knows)
	lastSentZ            float64
	lastSentYRot         int8 // Mth.packDegrees(yaw) at last send (the byte-angle)
	lastSentXRot         int8 // Mth.packDegrees(pitch) at last send
	teleportDelay        int  // ServerEntity.teleportDelay: ticks since the last absolute sync (reset on sync)
	wasOnGround          bool // ServerEntity.wasOnGround: onGround at the last send
	// sendTickCount is ServerEntity.tickCount: a FREE-RUNNING per-entity counter (++ every
	// sendChanges, NOT reset on a teleport). It drives the `tickCount % 60 == 0` idle re-anchor —
	// distinct from teleportDelay (which resets on each absolute sync). Conflating them shifts the
	// re-anchor cadence after a teleport, so they are kept separate (bytecode reads #139 tickCount
	// for the %60, #257 teleportDelay for the 400 cap).
	sendTickCount int
}

// NewEntity constructs a live entity instance from a data/entity TABLE record at the given
// position. It copies the table's wire ID and AABB Width/Height into the instance so the
// hot path (AABB/physics) never touches the table again. id comes from the tick-owned
// EntityIDAllocator (or, for a test entity, any unique value). Velocity/angles default to
// zero (a stationary, north-facing spawn); a fresh uuid is assigned so the instance has a
// network identity. Plan 06-02 fills metadata; Plan 06-03 moves it.
func NewEntity(id int32, t entity.Entity, x, y, z float64) *Entity {
	return &Entity{
		id:     id,
		typ:    t.ID,
		uuid:   uuid.New(),
		x:      x,
		y:      y,
		z:      z,
		width:  t.Width,
		height: t.Height,
	}
}

// AABB returns the entity's axis-aligned bounding box in world space, sourced from the
// data/entity Width/Height copied at spawn (06-RESEARCH Code Example): the box is CENTERED
// horizontally on x/z (half-width = width/2 on both X and Z), with its BASE at y (the
// entity's feet) and its TOP at y+height. The vanilla entity AABB is feet-anchored, not
// center-anchored vertically, so y is the lower bound and y+height the upper. Returns the
// generic bvh.AABB primitive (server/internal/bvh) so later collision/overlap work reuses
// Touch/WithIn without a second box type.
func (e *Entity) AABB() bvh.AABB[float64, bvh.Vec3[float64]] {
	hw := e.width / 2
	return bvh.AABB[float64, bvh.Vec3[float64]]{
		Lower: bvh.Vec3[float64]{e.x - hw, e.y, e.z - hw},
		Upper: bvh.Vec3[float64]{e.x + hw, e.y + e.height, e.z + hw},
	}
}
