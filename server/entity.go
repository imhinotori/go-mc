package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
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
	// sentinel is honored (never aged, never despawns). The XP-orb tick reuses this field with the
	// SAME semantics (ExperienceOrb.age, discard at 6000) — both vanilla entities count an `age`.
	age int

	// --- XP-ORB PICKUP (WR-06): the experience-orb lifecycle state -------------------------
	//
	// These mirror net.minecraft.world.entity.ExperienceOrb's private fields (decompiled from
	// temp/cache/26.2-inner.jar this session). Tick-owned plain values, set ONLY for an XP orb
	// (typ==entity.ExperienceOrb.ID); for every other entity they stay zero and are never read
	// (the orb tick/pickup scan gate on isOrb). They preserve the snapshot-friendly contract.

	// isOrb marks this entity as an ExperienceOrb (typ==entity.ExperienceOrb.ID). The orb tick
	// (gravity + age + despawn + followNearbyPlayer) and the player pickup scan run ONLY for entities
	// with this set, exactly as isItem gates the dropped-item path. Set at spawn by awardExperienceOrbs.
	isOrb bool

	// xpValue is ExperienceOrb.value — the experience this orb carries (the 1-3 reward chunk for a pig).
	// playerTouch awards getValue() == this to the collecting player, then the orb is discarded. Set at
	// spawn (the chunk from ExperienceOrb.awardWithDirection's split). Zero for a non-orb entity.
	xpValue int

	// followingPlayerID is ExperienceOrb.followingPlayer modeled as the followed player's entity id
	// (NEVER a live *tickPlayer — the Folia/snapshot rule). followNearbyPlayer sets it to the nearest
	// player within 8 blocks and homes the orb toward them; 0 == not currently following. Tick-owned.
	followingPlayerID int32

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

	// attributes is the live per-entity AttributeMap (level/attribute), the Go analogue of
	// LivingEntity.attributes (the per-entity AttributeMap backed by its EntityType's
	// DefaultAttributes supplier). nil for an entity type with NO registered supplier (a dropped
	// Item, an unported mob) — every attribute read helper (getAttributeValue) falls back to the
	// vanilla registration default for a nil map, so a nil-attributes entity behaves as a
	// modifier-free default exactly as vanilla's AttributeSupplier default would. Seeded at spawn by
	// NewEntity (attribute.NewMapForEntity(typeName)). Tick-owned (TICK-05): mutated ONLY on the tick
	// goroutine (finalizeSpawn's permanent modifier, future item/effect modifiers). Like ai, it is a
	// pointer to mutable tick-owned state and is NOT part of the snapshot-friendly value set the async
	// tracker copies — the tracker only ever reads pos/angle/dims — so it does not break the snapshot
	// contract.
	attributes *attribute.Map

	// leftHanded is Mob.isLeftHanded() — the 5% left-handed roll Mob.finalizeSpawn performs
	// (nextFloat() < 0.05F). It is set at spawn by drainStructureSpawns via attribute.FinalizeSpawn.
	// CITED: there is no left-handed SynchedEntityData (entity metadata) wire-out yet, so this flag is
	// recorded but not yet broadcast to the client (it controls which hand the mob attacks/holds with
	// — a visual the metadata subsystem surfaces). Tick-owned (TICK-05).
	leftHanded bool

	// --- MOB-SUB-01/02: the *Entity hurt-pipeline state (Plan 29-02) ----------------------
	//
	// These are the *Entity siblings of the tickPlayer combat fields (health/lastHurt/
	// invulnerableTime/hurtTime/hurtDuration in server/combat.go), mirroring the private
	// net.minecraft.world.entity.LivingEntity fields (decompiled from temp/cache/26.2-inner.jar this
	// session). They are PLAIN VALUE types — set for a goal-bearing mob (health initialized to its
	// MaxHealth at spawn) and read/written ONLY on the tick goroutine (TICK-05). They carry the SAME
	// snapshot-friendly exemption ai/attributes carry (l.119-135): damageSource is a small value struct
	// (an int enum + an int32, NO pointers), so the contract above holds — they travel with the
	// *Entity at the barrier with no special handling.

	// health is LivingEntity.health (setHealth subtracts; clamp at 0). Initialized to the mob's
	// MaxHealth at the spawn site (a mob born with health 0 would be instantly dead).
	health float32

	// lastHurt is LivingEntity.lastHurt: the previous hit's amount, the i-frame excess gate in
	// hurtServer (`if (amount <= lastHurt) return false` within the 10.0F window).
	lastHurt float32

	// invulnerableTime is LivingEntity.invulnerableTime: the i-frame window (armed to 20 on a fresh
	// hit, decremented each tick in baseTick — the `> 10.0F` upper half is the anti-spam half).
	invulnerableTime int32

	// hurtTime is LivingEntity.hurtTime: the red-flash timer (decremented unconditionally each tick in
	// baseTick). Set to hurtDuration on a fresh hit.
	hurtTime int32

	// hurtDuration is LivingEntity.hurtDuration: set to 10 on a fresh hit (`hurtDuration = 10;
	// hurtTime = hurtDuration`).
	hurtDuration int32

	// lastDamageSource is LivingEntity.lastDamageSource — the genuine ported source set in the flag2
	// (fresh-hit) block of hurtServer (bytecode 449-451). MOB-SUB-02: PanicGoal (P31) reads its tag,
	// wolf anger (P36) reads its attacker. A plain value (no pointer) — the Folia rule.
	lastDamageSource damageSource

	// dead is net.minecraft.world.entity.LivingEntity.dead (the death guard set TRUE the first time
	// die() runs). dieEntity (Plan 29-04) checks `if (isRemoved() || dead) return` at the top and sets
	// dead=true before the loot/XP/removal, so a second lethal hit (or a re-entrant die) is a no-op —
	// the loot table is never double-rolled and the store-remove is idempotent (T-29-08, the guarded
	// double-death). A plain bool (snapshot-friendly). Sulfur has no separate isRemoved() flag for a
	// mob (removal IS the store delete), so `dead` covers both the vanilla `dead` and the post-removal
	// re-entry guard.
	dead bool

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
	lastSentYHeadRot     int8 // Mth.packDegrees(yHeadRot) at last RotateHead send (its OWN threshold)
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
		// Attach the per-entity AttributeMap from this type's DefaultAttributes supplier (the Go
		// analogue of LivingEntity's `this.attributes = new AttributeMap(DefaultAttributes.getSupplier(
		// type))`). NewMapForEntity returns nil for a type with no registered supplier (a dropped Item,
		// an unported mob) — a nil map is the faithful "no DefaultAttributes" state and every read
		// helper degrades to the registration default. Keyed by the type's registry name (t.Name).
		attributes: attribute.NewMapForEntity(t.Name),
	}
}

// getAttributeValue is the port of LivingEntity.getAttributeValue(Holder<Attribute>) for a live
// entity: the folded value of the entity's AttributeMap for the given attribute. For an entity with
// NO attribute map (nil — a non-living entity / unported type) it falls back to the attribute's
// registration DEFAULT (attr.DefaultValue()), which is exactly what a modifier-free AttributeSupplier
// default would yield — so a read is always total and faithful. The caller applies any d2f narrowing
// at the vanilla cast site. Tick-owned (TICK-05).
func (e *Entity) getAttributeValue(attr *attribute.Attribute) float64 {
	if e.attributes == nil {
		return attr.DefaultValue()
	}
	return e.attributes.GetValue(attr.Name())
}

// initSpawnHealth sets a freshly-spawned mob's health to its folded MaxHealth — the port of
// net.minecraft.world.entity.LivingEntity.<init>'s `setHealth(getMaxHealth())`, where
// getMaxHealth() == (float) getAttributeValue(MAX_HEALTH) (the d2f narrowing matches the vanilla
// cast site). This MUST run on EVERY mob spawn path (declared, structure, test fixtures): a mob
// born with health 0 is treated as already-dead — applyDamageEntity's `e.health <= 0` guard and
// applyMobAttackDamage's `mob.health <= 0` short-circuit make it permanently invulnerable.
//
// It is the SINGLE source of truth for spawn-side health init: call it AFTER the attribute map is
// seeded (seedAttributes / FinalizeSpawn) so it reads the FINAL folded MaxHealth (e.g. the vanilla
// pig's declared 10.0, not the bare living default). Tick-owned (TICK-05): a pure store-state write
// on the owner at spawn, no RNG draw.
func initSpawnHealth(e *Entity) {
	e.health = float32(e.getAttributeValue(attribute.MaxHealth))
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
