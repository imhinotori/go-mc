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

	// jumping is net.minecraft.world.entity.LivingEntity.jumping — the per-tick "this mob WANTS to
	// jump" flag the JumpControl writes (jumpControl.tick → setJumping(jump)) and the aiStep jump
	// branch reads (`if (jumping && isAffectedByFluids())`). A plain bool (snapshot-friendly,
	// tick-owned, RNG-free), set every tick by the mob's jumpControl. MOB-SUB-04 (Plan 30-02).
	//	[VERIFIED javap LivingEntity.setJumping(boolean): `this.jumping = b;` — a bare field write.]
	jumping bool

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

	// --- PROJECTILE / ARROW (net.minecraft.world.entity.projectile.arrow.AbstractArrow) ----------
	//
	// Tick-owned plain values, set ONLY for an Arrow (typ==entity.Arrow.ID); zero + never read for any
	// other entity (the arrow tick gates on isArrow). Snapshot-friendly (plain values, no pointers).

	// isArrow marks this entity as an AbstractArrow. The arrow tick (drag 0.99 + gravity 0.05 + swept
	// entity/block hit + 1200-tick despawn) runs ONLY for entities with this set. Set at spawn by spawnArrow.
	isArrow bool

	// arrowBaseDamage is AbstractArrow.baseDamage — setBaseDamageFromMob(power) == power*2.0 +
	// triangle(difficulty*0.11, 0.57425). onHitEntity deals ceil(clamp(deltaMovement.length()*baseDamage)).
	arrowBaseDamage float64

	// arrowShooterID is AbstractArrow.getOwner() modeled as the shooter's entity id (NEVER a live pointer
	// — the Folia/snapshot rule). Attributed on the arrow's damage source; the arrow never hits its owner.
	arrowShooterID int32

	// arrowLife is AbstractArrow.life — ticks since spawn; tickDespawn discards the arrow at life>=1200.
	arrowLife int

	// arrowInGround is AbstractArrow.inGround — true once the arrow stuck in a block (deltaMovement zeroed).
	// A grounded arrow skips flight physics and counts toward despawn (tickDespawn).
	arrowInGround bool

	// spawnData is the ClientboundAddEntity "data" field (object-specific). For an arrow vanilla sets it to
	// ownerId+1 (the client owner link for crit visuals); 0 for a plain mob. Set at spawn.
	spawnData int32

	// --- THROWN SPLASH POTION (net.minecraft.world.entity.projectile.ThrownSplashPotion) --------------
	//
	// A thrown potion is a NON-mob projectile (isPotion) that arcs (gravity 0.05, drag 0.99) and SPLASHES
	// on the first block/entity hit — applying its effects to LivingEntities in the inflated AABB. Zero for
	// every non-potion entity (the potion tick gates on isPotion).

	// isPotion marks this entity as a ThrownSplashPotion. The potion tick (arc + splash-on-hit) runs ONLY
	// for entities with this set. Set at spawn by spawnSplashPotion.
	isPotion bool

	// potionEffects is the splash payload — the effects the potion applies on impact (the PotionContents
	// getAllEffects). Plain values (no pointers); scaled by proximity at splash time.
	potionEffects []splashEffect

	// --- CREEPER SWELL (net.minecraft.world.entity.monster.Creeper) --------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Creeper. swellDir is the SwellGoal output
	// (Creeper.setSwellDir: +1 arming, -1 disarming); swell is the fuse counter Creeper.tick advances by
	// swellDir each tick and, at maxSwell, triggers explodeCreeper. oldSwell mirrors Creeper.oldSwell (the
	// client-render prev value). powered/ignited are the charged/primed flags. maxSwell default 30 (the
	// vanilla fuse). Zero for every non-creeper entity (the creeper tick gates on typ == entity.Creeper.ID).
	swellDir int32
	swell    int32
	oldSwell int32
	maxSwell int32
	powered  bool
	ignited  bool

	// --- CUBE MOB (net.minecraft.world.entity.monster.cubemob.AbstractCubeMob / SulfurCube) ---------
	//
	// Tick-owned plain values, set/read ONLY for a sulfur cube (typ == entity.SulfurCube.ID). cubeSize is
	// AbstractCubeMob.ID_SIZE (the SynchedEntityData Integer size, 1..127 clamped; SulfurCube uses 1 or 2)
	// - it drives the size-scaled MAX_HEALTH (SulfurCube.setcubeMobHealth: 4*size), MOVEMENT_SPEED
	// (AbstractCubeMob.setSize: 0.2+0.1*size) and dims (getDefaultDimensions: baseDims.scale(size)). The
	// move-control fields mirror AbstractCubeMob.CubeMobMoveControl: cubeMoveYRot (the CubeMobMoveControl
	// .yRot heading it rotlerps toward), cubeJumpDelay (the JumpControl countdown), cubeAggressive (the
	// isAggressive flag that thirds the jump delay when chasing/tempted), cubeWantMove (the setWantedMovement
	// speedModifier; a NEGATIVE value is the Operation.WAIT default - no MOVE_TO this tick). cubeWasOnGround
	// mirrors AbstractCubeMob.wasOnGround (the squish/land edge). Zero for every non-cube entity (the cube
	// tick gates on typ == entity.SulfurCube.ID).
	cubeSize        int32
	cubeMoveYRot    float32
	cubeJumpDelay   int32
	cubeAggressive  bool
	cubeWantMove    float64 // negative sentinel: no MOVE_TO wanted this tick (Operation.WAIT)
	cubeWasOnGround bool
	// --- HAPPY GHAST MOVE CONTROL (net.minecraft.world.entity.monster.Ghast$GhastMoveControl +
	// Ghast$RandomFloatAroundGoal) --------------------------------------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a HappyGhast (happyGhastAiStep gates on typ ==
	// entity.HappyGhast.ID). ghastWantedX/Y/Z + ghastHasWanted mirror MoveControl.wantedX/Y/Z +
	// hasWanted() (the fly-to target RandomFloatAroundGoal.start commits). ghastFloatDuration mirrors
	// GhastMoveControl.floatDuration (the accel cadence: += nextInt(5)+2 between deltaMovement kicks).
	// Zero for every non-ghast entity.
	ghastWantedX, ghastWantedY, ghastWantedZ float64
	ghastHasWanted                           bool
	ghastFloatDuration                       int32
	// brain is the ported net.minecraft.world.entity.ai.Brain (brain.go). It is NON-NIL only for a mob
	// that runs the behavior subsystem — currently the BABY HappyGhast (HappyGhast.customServerAiStep
	// ticks the brain ONLY when isBaby()); every other entity leaves it nil (a nil brain is never ticked,
	// so the classic-goal mobs are wholly unaffected). Attached at spawn by attachHappyGhastBrain.
	// Tick-owned (TICK-05).
	brain *brain
	// --- FOX CHARACTER STATE (net.minecraft.world.entity.animal.fox.Fox) ---------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Fox. foxFlags is the DATA_FLAGS_ID byte the fox
	// goals read/write via the flag helpers below — the bit layout is jar-verified: SITTING=1,
	// CROUCHING=4, INTERESTED=8, POUNCING=16, SLEEPING=32, FACEPLANTED=64, DEFENDING=128 (Fox
	// isSitting/isCrouching/isInterested/isPouncing/isSleeping/isFaceplanted/isDefending accessors).
	// crouchAmount / crouchAmountO mirror Fox.crouchAmount(O) — the per-tick crouch animation counter
	// Fox.tick advances by 0.2 toward 5.0 while crouching (isFullyCrouched == crouchAmount == 5.0).
	// interestedAngle / interestedAngleO mirror Fox.interestedAngle(O). ticksSinceEaten mirrors
	// Fox.ticksSinceEaten (Fox.aiStep's mouth-food counter). foxDefendSince is DefendTrustedTargetGoal
	// .timestamp. Zero for every non-fox entity (the fox tick gates on typ == entity.Fox.ID). The
	// DATA_FLAGS wire metadata is a cite-deferred client visual (like the creeper's DATA_SWELL_DIR) —
	// the SERVER-SIDE gameplay (goal gating, pounce, sleep immobility) lands here.
	//	[VERIFIED javap Fox: DATA_FLAGS_ID byte; FLAG_SITTING=1, FLAG_CROUCHING=4, FLAG_INTERESTED=8,
	//	 FLAG_POUNCING=16, FLAG_SLEEPING=32, FLAG_FACEPLANTED=64, FLAG_DEFENDING=128; isFullyCrouched
	//	 == crouchAmount==5.0f; Fox.tick crouch/interested lerp; Fox.aiStep ++ticksSinceEaten.]
	foxFlags         byte
	crouchAmount     float32
	crouchAmountO    float32
	interestedAngle  float32
	interestedAngleO float32
	ticksSinceEaten  int32
	foxDefendSince   int32
	// foxTrusted0 / foxTrusted1 are the two DATA_TRUSTED_ID_0/1 EntityReference slots (Fox's trust list),
	// reduced to the trusted entities' THIN entity ids for v1 (the Folia rule == angerTarget). A tamed
	// fox trusts whoever bred it. DefendTrustedTargetGoal scans these. 0 == an empty slot; 0 for a wild
	// fox and every non-fox.
	//	[VERIFIED javap Fox: DATA_TRUSTED_ID_0 / DATA_TRUSTED_ID_1 OptionalLivingEntityReference; getTrustedEntities.]
	foxTrusted0 int32
	foxTrusted1 int32

	// --- Witch self-drink state (MOB-HOST-07 heal branch, ai_goals_witch.go witchAiStep) ---
	// Tick-owned plain values, set/read ONLY for a Witch (Witch.aiStep). witchDrinking mirrors
	// Witch.DATA_USING_ITEM (isDrinkingPotion); witchUsingTime mirrors Witch.usingTime (the drink
	// countdown, seeded to the potion useDuration=32 and decremented each tick). false/0 for every
	// non-witch. When the countdown hits 0 the witch APPLIES the drunk potion's effects to ITSELF via
	// the entity-side mobEffects map below. During the drink a -0.25 ADD_VALUE MOVEMENT_SPEED transient
	// modifier ("drinking") is attached and removed on finish.
	//	[VERIFIED CFR Witch.aiStep + Witch.usingTime / DATA_USING_ITEM / SPEED_MODIFIER_DRINKING.]
	witchDrinking  bool
	witchUsingTime int32
	// witchDrinkPending is the effect id the in-progress drink will apply when its countdown finishes
	// (the reduced stand-in for reading the mainhand potion's PotionContents on finish); witchDrinkPendingDur
	// is that effect's base duration. Cleared on finish. false/""/0 for every non-drinking witch.
	witchDrinkPending    string
	witchDrinkPendingDur int

	// mobEffects is the entity-side MobEffectInstance map (LivingEntity.activeEffects), the mob analogue
	// of tickPlayer.activeEffects (mob_effect.go). Populated when a witch drinks a potion on itself (the
	// self-buff branch) and ticked down by tickMobEffects. nil until the first self-effect applies; only
	// the witch writes it in v1. Effect ids -> the active instance (duration counts DOWN). Tick-owned.
	mobEffects map[string]*activeEffect

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

	// remainingFireTicks is Entity.remainingFireTicks: the burn countdown. igniteForSeconds(n) sets it
	// to floor(n*20) (only if larger); baseTick decrements it, deals 1 fire damage every 20 ticks, and
	// broadcasts the on-fire shared-flag (DATA_SHARED_FLAGS bit 0x01). 0 = not on fire. Tick-owned.
	//	[VERIFIED javap Entity.remainingFireTicks / igniteForTicks / baseTick fire block.]
	remainingFireTicks int32

	// lastDamageSource is LivingEntity.lastDamageSource — the genuine ported source set in the flag2
	// (fresh-hit) block of hurtServer (bytecode 449-451). MOB-SUB-02: PanicGoal (P31) reads its tag,
	// wolf anger (P36) reads its attacker. A plain value (no pointer) — the Folia rule.
	lastDamageSource damageSource

	// hasLastDamage is the faithful LivingEntity.getLastDamageSource() != null signal — set TRUE the
	// first time hurtServer records a real lastDamageSource (the flag2 block, combat_mob.go). It
	// PERSISTS (it is NOT the 10-tick hurtTime window): vanilla's lastDamageSource is a longer-lived
	// source reference, not the hurt-flash timer, so PanicGoal.shouldPanic (which gates on
	// getLastDamageSource() != null, NOT on hurtTime) keeps firing while the source persists. A
	// SEPARATE not-null signal is required because lastDamageSource is a plain VALUE struct (never nil)
	// whose zero value has typeTag==0 — and id 0 (minecraft:in_fire) is a REAL panic_causes member
	// (data/tag/tags.go:152), so the id itself cannot represent "unset" (Decision B). A plain bool
	// (snapshot-friendly, the Folia rule). PanicGoal (P31) reads it via shouldPanic.
	hasLastDamage bool

	// lastHurtByMob is net.minecraft.world.entity.LivingEntity.lastHurtByMob — the entity id of the
	// attacker that last hit this mob (0 == null/no attacker). Set in LivingEntity.hurtServer alongside
	// the Phase-31 lastDamageSource (combat_mob.go flag2 store-point) when an entity attacker hits the
	// mob. HurtByTargetGoal.canUse reads it (timestamp != its own && lastHurtByMob != null -> retaliate).
	// Phase 31 tracked the damage SOURCE (the tag); this is the attacker ENTITY ref. A THIN id (never a
	// live *Entity — the Folia rule, == damageSource.attacker). Tick-owned (TICK-05).
	lastHurtByMob int32

	// lastHurtByMobTimestamp is net.minecraft.world.entity.LivingEntity.lastHurtByMobTimestamp — the
	// gameTime tick stamp of the last attacker hit, set beside lastHurtByMob. HurtByTargetGoal compares
	// it against its OWN stored timestamp (canUse: timestamp == this.timestamp -> false) so it
	// retaliates only ONCE per fresh hit. A plain int32 (snapshot-friendly), tick-owned (TICK-05).
	lastHurtByMobTimestamp int32

	// dead is net.minecraft.world.entity.LivingEntity.dead (the death guard set TRUE the first time
	// die() runs). dieEntity (Plan 29-04) checks `if (isRemoved() || dead) return` at the top and sets
	// dead=true before the loot/XP/removal, so a second lethal hit (or a re-entrant die) is a no-op —
	// the loot table is never double-rolled and the store-remove is idempotent (T-29-08, the guarded
	// double-death). A plain bool (snapshot-friendly). Sulfur has no separate isRemoved() flag for a
	// mob (removal IS the store delete), so `dead` covers both the vanilla `dead` and the post-removal
	// re-entry guard.
	dead bool

	// deathTime is net.minecraft.world.entity.LivingEntity.deathTime — the death-animation countdown
	// ticked by tickDeath() once the mob is dead. die() leaves the mob in the world (it does NOT
	// remove it); LivingEntity.baseTick then calls tickDeath() every tick while isDeadOrDying(), which
	// increments deathTime and, at deathTime >= 20 (~1s, the fall-over animation length), broadcasts
	// the death-poof status (60) and removes the entity (Entity.RemovalReason.KILLED). Reset to 0 in
	// dieEntity. A plain int32 (snapshot-friendly), tick-owned (TICK-05).
	//	[VERIFIED javap LivingEntity.tickDeath: ++deathTime; if (deathTime >= 20 && !isClientSide &&
	//	 !isRemoved) { broadcastEntityEvent(this, 60); remove(KILLED); }.]
	deathTime int32

	// fallDistance is net.minecraft.world.entity.Entity.fallDistance — the running descent distance
	// the entity has accumulated since it was last on the ground (or in water). Entity.checkFallDamage
	// adds the per-tick drop (`fallDistance -= (float) deltaY` while !isInWater && deltaY < 0) and, on
	// landing (onGround), feeds it to LivingEntity.causeFallDamage -> calculateFallDamage to compute
	// the fall damage, then resets it. It is the *Entity sibling of tickPlayer.fallDistance (the player
	// fall path, fall_damage.go / tick.go). LIVE-DEBUG B: without this the mob fall-damage path never
	// ran (only tickPlayer accumulated fallDistance), so a mob that fell from height took no damage. A
	// plain float64 (snapshot-friendly), tick-owned (TICK-05): mutated only on the tick goroutine in
	// tickPhysics (the mob fall path, mob_fall_damage.go).
	//	[VERIFIED javap Entity.checkFallDamage: getfield fallDistance; dload deltaY; d2f; f2d; dsub;
	//	 putfield fallDistance (the descent accumulation), then on onGround the >0 -> causeFallDamage ->
	//	 resetFallDistance chain.]
	fallDistance float64

	// --- MOB-SUB-08 (Plan 33-01): AgeableMob aging state -------------------------------------
	//
	// breedAge is net.minecraft.world.entity.AgeableMob.age (the server-side signed-int age machine,
	// NOT the ItemEntity/XP-orb `age int` at :119 — that is ticks-since-spawn for a dropped item).
	// Semantics (AgeableMob.getAge/setAge/aiStep, javap'd from temp/cache/26.2-inner.jar):
	//   breedAge <  0  -> BABY: ticks UP toward 0 (canAgeUp() == isBaby()), grows into an adult at 0.
	//   breedAge >  0  -> ADULT on breeding cooldown: ticks DOWN toward 0.
	//   breedAge == 0  -> ADULT, breeding-ready: a pure no-op every tick (no draw, no transition).
	// A freshly-spawned mob is breedAge 0 (the zero value) — an adult — so the oracle pig (an un-fed
	// lone adult) is breedAge 0 from spawn and never ages; the per-tick aging is a pure-int no-op on it
	// (the byte-identical gate). A spawned BABY sets breedAge = BABY_START_AGE (-24000) (Plan C's breed).
	// Tick-owned plain int (TICK-05), zero for every non-animal entity, snapshot-friendly.
	//	[VERIFIED javap AgeableMob.getAge (server branch): `return this.age;`; setAge writes age + flips
	//	 DATA_BABY_ID on the 0-crossing; aiStep: `if (canAgeUp()) setAge(age+1); else if (age>0) setAge(age-1)`;
	//	 BABY_START_AGE static = sipush -24000; isBaby() == getAge() < 0.]
	breedAge int

	// adultWidth, adultHeight capture the entity's UN-SCALED (adult) AABB footprint at spawn, copied
	// from the data/entity table in NewEntity. refreshDimensions reads them as the source dims so the
	// baby half-scale toggle (width = adultWidth * babyDimensionScale) and the grow-up restore
	// (width = adultWidth) are both exact, with no entity-type re-lookup. This is the Go analogue of
	// Pig.getDefaultDimensions reading EntityType.PIG.getDimensions() (the un-scaled table dims) vs
	// BABY_DIMENSIONS (the scaled ones). Plain values, snapshot-friendly, never mutated after spawn.
	adultWidth, adultHeight float64

	// --- MOB-SUB-09 (Plan 33-02): Animal in-love state ---------------------------------------
	//
	// inLove is net.minecraft.world.entity.animal.Animal.inLove — the love-mode countdown.
	// setInLove sets it to DEFAULT_IN_LOVE_TIME (600 ticks == 30s); it ticks DOWN one per server
	// tick while > 0 (the Animal.aiStep tail), and is FORCED to 0 every tick for a non-adult
	// (getAge() != 0 -> inLove = 0), so only a breeding-ready ADULT (breedAge == 0) can stay in
	// love. isInLove() == inLove > 0; canFallInLove() == inLove <= 0. A freshly-spawned mob is
	// inLove 0 (the zero value, can-fall-in-love, not in love) — so the oracle pig (an un-fed lone
	// adult) is inLove 0 from spawn and the whole love subsystem (the decrement + the heart trigger)
	// is dormant on it (the byte-identical gate). setInLove fires ONLY from the FEED path (a real
	// ServerboundInteract) — the oracle never feeds. The loveCause/ServerPlayer record (vanilla's
	// setInLove(player) second effect, used for the breed XP-orb attribution) is OUT OF SCOPE for
	// this plan — a Plan-C concern — so setInLove here is reduced to the inLove countdown (the value
	// is NOT baked away: a loveCause field slots in later without changing the countdown). Tick-owned
	// plain int (TICK-05), zero for every non-animal entity, snapshot-friendly.
	//	[VERIFIED javap Animal: `private int inLove`; DEFAULT_IN_LOVE_TIME == sipush 600; setInLove ->
	//	 inLove = 600 (+ loveCause record + broadcastEntityEvent(this,18)); isInLove() == inLove > 0;
	//	 canFallInLove() == inLove <= 0; aiStep tail: if getAge()!=0 inLove=0; if inLove>0 {--inLove; ...}.]
	inLove int

	// --- MOB-PASS-02 (Phase 34): Sheep wool / shear state ------------------------------------
	//
	// sheared is the host-side mirror of net.minecraft.world.entity.animal.sheep.Sheep's DATA_WOOL
	// sheared bit (isSheared() == (DATA_WOOL & 0x10) != 0). Sheep.shear sets it true (the wool is
	// gone until it regrows); Sheep.ate (the EatBlockGoal eat) sets it false (the wool regrows). Sulfur
	// keeps the bool here and DERIVES the wire DATA_WOOL byte from it on broadcast (setSheared in
	// sheep_eat.go), since v1 ships WHITE sheep only (the low color nibble is always 0). readyForShearing()
	// == !sheared && !isBaby() gates the shear action. Tick-owned plain bool (TICK-05), FALSE (not sheared)
	// for the zero value, never read for a non-sheep entity (only the shear/eat seams touch it), so it is a
	// harmless no-op for the pig and every other mob — the pig oracle's stream gains ZERO draws from it.
	//	[VERIFIED javap Sheep: isSheared() == (DATA_WOOL & 0x10) != 0; setSheared(b) -> DATA_WOOL =
	//	 b ? (cur|0x10) : (cur&0xEF); DEFAULT_SHEARED == false; getColor default WHITE (DATA_WOOL low nibble 0).]
	sheared bool

	// --- MOB-PASS-03 (Phase 34): Chicken egg-lay timer ----------------------------------------
	//
	// eggTime is net.minecraft.world.entity.animal.chicken.Chicken.eggTime — the per-tick countdown to
	// the next egg lay. Chicken.<init> seeds it to nextInt(6000) + 6000 (5..10 minutes), the aiStep
	// egg-lay decrements it every server tick, and on hitting <= 0 it drops an egg, plays the egg sound,
	// and resets to nextInt(6000) + 6000. Set at spawn ONLY for a chicken (plugin_mob_decl.go's spawn
	// init); zero for every other entity, where the chickenAiStep hook is never invoked (the tick wiring
	// gates on typ == entity.Chicken.ID), so it is inert for the pig — the pig oracle stream is unperturbed.
	//	[VERIFIED javap Chicken: `int eggTime`; <init> eggTime = random.nextInt(6000) + 6000; aiStep
	//	 `if (--eggTime <= 0) { ...drop... ; eggTime = random.nextInt(6000) + 6000; }`.]
	eggTime int

	// life is net.minecraft.world.entity.monster.Endermite.life - the endermite's despawn counter. A
	// NON-persistent endermite increments it each server tick (Endermite.aiStep) and discard()s when it
	// reaches MAX_LIFE (2400 ticks, ~2 min). Set at 0 (DEFAULT_LIFE) on spawn; advanced ONLY by
	// endermiteAiStep (tick wiring gates on typ == entity.Endermite.ID), so it is inert for every other
	// entity and the pig oracle stream is unperturbed.
	//	[VERIFIED javap Endermite: `int life`; MAX_LIFE 2400; aiStep `if (!isPersistenceRequired()) ++life;
	//	 if (life >= 2400) discard();`.]
	life int

	// --- MOB-NEUT-01/02 (Phase 36-01): Wolf TamableAnimal / NeutralMob state ------------------
	//
	// The wolf is the FIRST tameable/neutral mob; these fields mirror the TamableAnimal (tame/owner/
	// sit) + NeutralMob (anger) state. EVERY field is wolf-gated at its reader/writer (typ ==
	// entity.Wolf.ID / a "wolf" base_type), so the ZERO value is the exact non-wolf default and the
	// pig oracle's stream gains ZERO draws from any of them — the same discipline the sheared/eggTime
	// fields above follow. All are tick-owned plain values (TICK-05), snapshot-friendly (no live
	// pointers — the owner/anger refs are THIN entity ids, the Folia rule == lastHurtByMob).
	//
	// tame is the TamableAnimal DATA_FLAGS bit 0x4 (isTame() == (DATA_FLAGS & 4) != 0). setTame flips
	// it; the tamed-HP bump (8->40 applyTamingSideEffects) is the runtime side-effect Plan B owns.
	// FALSE (untamed) for the pig and the zero value.
	//	[VERIFIED javap TamableAnimal: isTame() == (DATA_FLAGS & 4)!=0; setTame -> DATA_FLAGS |= 4.]
	tame bool

	// orderedToSit is the plain TamableAnimal.orderedToSit field (isOrderedToSit/setOrderedToSit — a
	// real bool field, NOT a DATA_FLAGS bit). SitWhenOrderedToGoal.canUse gates on it; the sit-toggle
	// interact (Plan B) flips it. FALSE for the pig and the zero value.
	//	[VERIFIED javap TamableAnimal: orderedToSit is a private boolean field; isOrderedToSit returns it.]
	orderedToSit bool

	// inSittingPose is the TamableAnimal DATA_FLAGS bit 0x1 (isInSittingPose() == (DATA_FLAGS & 1)!=0).
	// SitWhenOrderedToGoal.start/stop sets it (setInSittingPose), and it drives the DATA_FLAGS wire
	// broadcast (the client renders the sit pose). FALSE for the pig and the zero value.
	//	[VERIFIED javap TamableAnimal: isInSittingPose() == (DATA_FLAGS & 1)!=0; setInSittingPose(b) ->
	//	 DATA_FLAGS = b ? (cur|1) : (cur&0xFE).]
	inSittingPose bool

	// ownerUUID is the TamableAnimal DATA_OWNERUUID_ID owner ref (Optional<EntityReference<LivingEntity>>),
	// reduced to the owner's THIN entity id for v1 (the goals only need to resolve the owner on the
	// loop; the WIRE broadcast of the owner ref is deferred — server-side ref drives the goals). 0 ==
	// no owner (the pig and the zero value). setOwner (Plan B) writes it; getOwner resolves it via
	// playerByEntityID (v1 owners are players).
	//	[VERIFIED javap TamableAnimal: DATA_OWNERUUID_ID = OPTIONAL_LIVING_ENTITY_REFERENCE accessor
	//	 (index 19); setOwner sets it from the LivingEntity.]
	ownerUUID int32

	// angerEndTime is the NeutralMob persistent-anger GAMETIME ENDPOINT (the DATA_ANGER_END_TIME /
	// getPersistentAngerEndTime() value, held as a plain server-side int64 — the wire accessor index 22
	// is deferred). The anger model is a gametime-endpoint, NOT a counter: isAngry() == angerEndTime > 0
	// && (angerEndTime - gameTime) > 0, so the anger EXPIRES automatically when gameTime passes
	// angerEndTime — there is NO per-tick decrement and NO ResetUniversalAngerTargetGoal (both
	// dissolved). On a player hit (combat_mob.go store-point) it is set to gameTime + 400 + nextInt(381)
	// (UniformInt(400,780).sample). 0 (no live anger) for the pig and the zero value.
	//	[VERIFIED javap NeutralMob.isAngry(): endTime = getPersistentAngerEndTime(); endTime > 0 &&
	//	 (endTime - level.getGameTime()) > 0. Wolf.PERSISTENT_ANGER_TIME = TimeUtil.rangeOfSeconds(20,39)
	//	 = UniformInt.of(400,780); sample = 400 + nextInt(381).]
	angerEndTime int64

	// angerTarget is the NeutralMob persistentAngerTarget (the EntityReference of whoever provoked the
	// wolf), reduced to that attacker's THIN entity id for v1. isAngryAt gates the angry_player_target
	// goal on `angerEndTime live && angerTarget == candidate`. Set beside angerEndTime at the combat
	// store-point. 0 (no target) for the pig and the zero value.
	//	[VERIFIED javap NeutralMob.isAngryAt: canAttack(target) && (... || getPersistentAngerTarget()
	//	 matches target). getPersistentAngerTarget() == the persistentAngerTarget EntityReference.]
	angerTarget int32

	// lastHurtMob / lastHurtMobTimestamp are the OWNER-SIDE attack bookkeeping OwnerHurtTargetGoal
	// reads: net.minecraft.world.entity.LivingEntity.lastHurtMob (whoever THIS entity last ATTACKED)
	// and its gameTime stamp — the attack-side mirror of lastHurtByMob/lastHurtByMobTimestamp (the
	// hurt-by side, above). OwnerHurtTargetGoal.canUse reads owner.getLastHurtMob()/Timestamp() to
	// retaliate against whatever the owner is fighting. Set on the attacker side wherever an entity
	// deals damage; 0 for the pig and the zero value (the pig owns no wolf, and these reads are
	// wolf-goal-gated). THIN id (the Folia rule == lastHurtByMob). Tick-owned (TICK-05).
	//	[VERIFIED javap LivingEntity: lastHurtMob (the entity this last hit) + lastHurtMobTimestamp;
	//	 OwnerHurtTargetGoal.canUse: owner.getLastHurtMob(); owner.getLastHurtMobTimestamp().]
	lastHurtMob          int32
	lastHurtMobTimestamp int32

	// --- MOB-PREY (Task #9): Turtle home-pos / egg-lay state -----------------------------------
	//
	// The turtle is the FIRST home-bound / block-placing mob; these fields mirror
	// net.minecraft.world.entity.animal.turtle.Turtle's private BlockPos homePos / travelPos +
	// the boolean goingHome + the synched HAS_EGG / LAYING_EGG data + the int layEggCounter. EVERY
	// field is turtle-gated at its reader/writer (typ == entity.Turtle.ID / the turtle native goals),
	// so the ZERO value is the exact non-turtle default and the pig oracle's stream gains ZERO draws
	// from any of them — the SAME discipline the wolf / eggTime / life fields above follow. All are
	// tick-owned plain values (TICK-05); homePos/travelPos are packed block coords (no live pointers).
	//
	// homePos is Turtle.homePos — the scented sand home a turtle returns to lay eggs. Turtle.finalizeSpawn
	// sets it to blockPosition() (setHomePos), so a spawned turtle's home is its spawn column. TurtleGoHomeGoal
	// / TurtleLayEggGoal read it (closerToCenterThan checks). homePosSet distinguishes "home == (0,0,0)"
	// (a real world pos) from "never set" so a home-goal never fires on an unspawned zero pos.
	//	[VERIFIED javap Turtle: private BlockPos homePos; setHomePos/getHomePos; finalizeSpawn setHomePos(blockPosition()).]
	homePosX, homePosY, homePosZ int
	homePosSet                   bool

	// travelPos is Turtle.travelPos — the far deep-water wander goal TurtleTravelGoal picks (nextInt(1025)-512
	// on X/Z). travelPosSet mirrors the Java field being null before the goal's start() sets it (canUse/tick
	// null-guards). Only TurtleTravelGoal / TurtlePathNavigation.isStableDestination read it.
	//	[VERIFIED javap Turtle: private BlockPos travelPos; TurtleTravelGoal.start sets it, stop nulls it.]
	travelPosX, travelPosY, travelPosZ int
	travelPosSet                       bool

	// goingHome is Turtle.goingHome — set true by TurtleGoHomeGoal.start / false by its stop. It GATES
	// TurtleGoToWaterGoal.canUse (a homing turtle does not detour to water) and TurtleTravelGoal.canUse
	// (a homing turtle does not deep-wander). FALSE for the pig and the zero value.
	//	[VERIFIED javap Turtle: private boolean goingHome; TurtleGoHomeGoal.start goingHome=true, stop=false.]
	goingHome bool

	// hasEgg is Turtle.hasEgg() (the HAS_EGG synched data, DEFAULT_HAS_EGG=false). TurtleBreedGoal.breed
	// sets it true (the turtle lays instead of spawning a live baby); TurtleLayEggGoal.tick clears it after
	// placing the egg block. It GATES TurtleGoHomeGoal (a pregnant turtle always homes) and TurtleLayEggGoal.
	// FALSE for the pig and the zero value. (Breed still spawns a baby in v1 — the hasEgg breed override is
	// cited-deferred in the .star; the field + lay/home consumers are built so a future breed override slots in.)
	//	[VERIFIED javap Turtle: HAS_EGG synched Boolean; hasEgg()/setHasEgg(boolean); DEFAULT_HAS_EGG=false.]
	hasEgg bool

	// layingEgg is Turtle.isLayingEgg() (the LAYING_EGG synched data). TurtleLayEggGoal.tick sets it true
	// while the turtle is digging (layEggCounter counting up), cleared when the egg is placed. It slows the
	// turtle's movement in vanilla (travelInWater 0.3 factor — a cited-deferred physics tweak). FALSE default.
	//	[VERIFIED javap Turtle: LAYING_EGG synched Boolean; isLayingEgg()/setLayingEgg(boolean).]
	layingEgg bool

	// layEggCounter is Turtle.layEggCounter — the dig-timer TurtleLayEggGoal.tick counts up once the turtle
	// has reached the sand target and set layingEgg; at > adjustedTickDelay(200) the egg block is placed. 0 default.
	//	[VERIFIED javap Turtle: private int layEggCounter; TurtleLayEggGoal.tick ++layEggCounter / place at >200.]
	layEggCounter int

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

	// --- MOB EQUIPMENT (net.minecraft.world.entity.EntityEquipment / LivingEntity.equipment) ------
	//
	// equipment is the mob's held-item/armor slots — the Go analogue of LivingEntity's
	//   protected final EntityEquipment equipment;   // an EnumMap<EquipmentSlot, ItemStack>
	// represented as a fixed [equipmentSlotCount]SlotData array indexed by EquipmentSlot ordinal
	// (MAINHAND=0..SADDLE=7 — the SAME ordinal ClientboundSetEquipmentPacket writes). A zero-value
	// SlotData (Count==0) IS ItemStack.EMPTY, so an un-populated slot reads back empty exactly as
	// EntityEquipment.get's getOrDefault(slot, EMPTY) does — a mob with no equipment (the oracle pig)
	// carries an all-zero array: no allocation beyond the inline array, no RNG draw, no wire bytes.
	// getItemBySlot/setItemSlot (entity_equipment.go) are the accessors; populateSkeletonEquipment
	// seeds the skeleton's MAINHAND bow at spawn. Tick-owned (TICK-05); a VALUE array (snapshot-
	// friendly — snapshotEntity copies it by value so the async tracker can emit the spawn-time
	// SetEquipment without aliasing the live store).
	//	[VERIFIED javap LivingEntity: `protected final EntityEquipment equipment;`; EntityEquipment:
	//	 `private final EnumMap<EquipmentSlot,ItemStack> items;` get/set over it.]
	equipment [equipmentSlotCount]component.SlotData
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
		// Capture the un-scaled (adult) dims so refreshDimensions can derive the baby half-scale box
		// and restore the adult box exactly on grow-up (Pig.getDefaultDimensions source dims). MOB-SUB-08.
		adultWidth:  t.Width,
		adultHeight: t.Height,
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

// setJumping is net.minecraft.world.entity.LivingEntity.setJumping(boolean) — the bare field write
// the mob's JumpControl performs each tick (jumpControl.tick → mob.setJumping(jump)). MOB-SUB-04.
//	[VERIFIED javap LivingEntity.setJumping(boolean): aload_0; iload_1; putfield jumping:Z; return —
//	 i.e. `this.jumping = b;`. No side effects beyond the field write.]
func (e *Entity) setJumping(b bool) { e.jumping = b }

// isAffectedByFluids is net.minecraft.world.entity.LivingEntity.isAffectedByFluids — the gate the
// aiStep jump branch checks (`if (jumping && isAffectedByFluids())`). For a base LivingEntity it
// returns TRUE unconditionally (only a few overrides — e.g. ArmorStand — return false). A v1 Pig is
// a plain LivingEntity, so this is a cited const-true, structured to become a per-type override read
// (a `noFluidAffect` flag) once a mob type needs the ArmorStand-style false. MOB-SUB-04.
//	[VERIFIED javap LivingEntity.isAffectedByFluids: iconst_1; ireturn — `return true;`.]
func (e *Entity) isAffectedByFluids() bool { return true }

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

// babyDimensionScale is the BABY hitbox scale factor — net.minecraft.world.entity.animal.Pig's
// BABY_DIMENSIONS = EntityType.PIG.getDimensions().scale(0.5f) (the adult box halved on both axes;
// for the pig: adult 0.9x0.9 -> baby 0.45x0.45 == EntityDimensions.scalable(0.45, 0.45)). It is the
// SCALE, derived once, NOT a hardcoded 0.45, so a non-pig AgeableMob (Phase 34 cow/sheep/chicken)
// reuses the factor against its OWN adult dims. MOB-SUB-08 + ROADMAP SC#1 (the baby half-scale hitbox).
//	[VERIFIED javap (33-JARNOTES): Pig.BABY_DIMENSIONS = EntityType.PIG.getDimensions().scale(0.5f)
//	 .withEyeHeight(0.40625f) == scalable(0.45, 0.45); adult pig dims 0.9x0.9 from the data table.]
const babyDimensionScale = 0.5

// isBaby is net.minecraft.world.entity.AgeableMob.isBaby() — true exactly while the age machine is
// negative (a growing baby). The follow/breed goal distSqr checks and the half-scale hitbox both
// read it. MOB-SUB-08.
//	[VERIFIED javap AgeableMob.isBaby: `return getAge() < 0;` (server getAge() == this.age == breedAge).]
func (e *Entity) isBaby() bool { return e.breedAge < 0 }

// refreshDimensions is the port of net.minecraft.world.entity.animal.Pig.getDefaultDimensions(Pose):
// `return this.isBaby() ? BABY_DIMENSIONS : super.getDefaultDimensions(pose);`. It sets the entity's
// live AABB footprint (width/height — what AABB() reads) to either the BABY box (adult dims x
// babyDimensionScale, the load-bearing half-scale hitbox the BreedGoal distSqr<9 / FollowParentGoal
// 9..256 checks consume) or the restored ADULT box (the captured spawn-table dims). It is called
// whenever breedAge crosses 0 (tickMobAging's onGrewUp) and will be called by Plan C's breed()
// child-spawn (which sets child.breedAge = BABY_START_AGE then refreshes to the small box).
//
// Eye height (baby 0.40625): our Entity has NO eye-height field today — cite-deferred (Pig.BABY_DIMENSIONS
// .withEyeHeight(0.40625f)); it is NOT load-bearing for the goal distSqr checks (those read the AABB).
// If an eye-height field lands later, scale it by 0.40625/adult at the same seam (33-deviations.md).
//	[VERIFIED javap Pig.getDefaultDimensions: isBaby() ? BABY_DIMENSIONS : super; BABY_DIMENSIONS is the
//	 adult dims scaled 0.5 — so baby width/height = adultWidth/adultHeight * babyDimensionScale.]
func (e *Entity) refreshDimensions() {
	if e.isBaby() {
		e.width = e.adultWidth * babyDimensionScale
		e.height = e.adultHeight * babyDimensionScale
		return
	}
	e.width = e.adultWidth
	e.height = e.adultHeight
}

// defaultInLoveTime is net.minecraft.world.entity.animal.Animal.DEFAULT_IN_LOVE_TIME — the love-mode
// countdown setInLove arms (600 ticks == 30 seconds at 20 TPS).
//	[VERIFIED javap Animal.setInLove: `this.inLove = 600;` (sipush 600); DEFAULT_IN_LOVE_TIME == 600.]
const defaultInLoveTime = 600

// isInLove is net.minecraft.world.entity.animal.Animal.isInLove() == `this.inLove > 0`. True while
// the love-mode countdown is running (BreedGoal.canUse gates on it: a pig only seeks a partner while
// in love). Pure read, draws no RNG.
//	[VERIFIED javap Animal.isInLove: `return this.inLove > 0;` (getfield inLove; ifle; iconst_1/0).]
func (e *Entity) isInLove() bool { return e.inLove > 0 }

// canFallInLove is net.minecraft.world.entity.animal.Animal.canFallInLove() == `this.inLove <= 0`.
// The FEED-path adult branch (mobInteract) gates on it so a pig already in love is not re-armed (and
// does not consume a second food item). Pure read, draws no RNG.
//	[VERIFIED javap Animal.canFallInLove: `return this.inLove <= 0;` (getfield inLove; ifgt; iconst_1/0).]
func (e *Entity) canFallInLove() bool { return e.inLove <= 0 }

// setInLove is net.minecraft.world.entity.animal.Animal.setInLove(player) reduced to its inLove
// countdown effect: `this.inLove = 600`. Vanilla ALSO records the love-causing ServerPlayer (for the
// breed XP-orb attribution) and calls level.broadcastEntityEvent(this, (byte)18) to fire the client
// heart-particle burst; the loveCause record is a Plan-C concern (cited OUT OF SCOPE above, NOT baked
// away), and the EntityEvent-18 heart broadcast is performed by the FEED-path caller (broadcastHearts)
// right after this, mirroring setInLove's own broadcast. Tick-owned; the value is never mutated off
// the tick goroutine.
//	[VERIFIED javap Animal.setInLove: sipush 600; putfield inLove; (record loveCause); level();
//	 bipush 18; Level.broadcastEntityEvent(this, 18).]
func (e *Entity) setInLove() { e.inLove = defaultInLoveTime }

// canMate is net.minecraft.world.entity.animal.Animal.canMate(Animal other): the partner gate the
// BreedGoal's getFreePartner scan applies — `other != this && other.getClass() == this.getClass()
// && this.isInLove() && other.isInLove()`. Our same-species check is `other.typ == e.typ` (both the
// vanilla_pig wire type, entity.Pig.ID): two distinct same-type animals that are BOTH in love may
// breed. Pure read, draws no RNG.
//	[VERIFIED javap Animal.canMate: `other != this` (if_acmpeq -> 0); `other.getClass()==getClass()`
//	 (if_acmpne -> 0); `isInLove()` (ifeq -> 0); `other.isInLove()` (ifeq -> 0); else 1.]
func (e *Entity) canMate(other *Entity) bool {
	return other != e && other.typ == e.typ && e.isInLove() && other.isInLove()
}

// isAlive is net.minecraft.world.entity.LivingEntity.isAlive(): `!isRemoved() && health > 0`. The
// BreedGoal.canContinueToUse (partner alive) and FollowParentGoal.canContinueToUse (parent alive)
// gates read it so a goal drops a partner/parent that died or was removed mid-courting/follow. Sulfur
// folds isRemoved() into the `dead` flag (removal IS the store delete; see the field doc), so this is
// `!e.dead && e.health > 0`. Pure read, no RNG.
//	[VERIFIED javap LivingEntity.isAlive: `return !isRemoved() && getHealth() > 0.0F;`.]
func (e *Entity) isAlive() bool { return !e.dead && e.health > 0 }

// ageUp is net.minecraft.world.entity.AgeableMob.ageUp(int amount) == ageUp(amount, false): it
// advances the age toward adulthood by `amount * 20` ticks, clamped at 0 so the result never goes
// positive (a fed baby lands exactly on 0 = adult, never into the breeding cooldown). The exact
// bytecode (ageUp(int,boolean)): `int i = getAge(); i += amount * 20; if (i > 0) i = 0; setAge(i)`.
// The *20 factor is LOAD-BEARING — getSpeedUpSecondsWhenFeeding returns a number of SECONDS, and
// ageUp converts seconds to ticks. The `forced` branch (forcedAge / forcedAgeTimer) is skipped: the
// FEED path calls ageUp(amount, true) in vanilla, but forcedAge/forcedAgeTimer are v1 const-0 stubs
// (no age-lock subsystem), so only the age advance + clamp is observable. If the add crosses 0 (the
// baby grows up), the caller (the feed path) performs the same 0-crossing side effect tickMobAging's
// onGrewUp does (refresh dims + broadcast DATA_BABY_ID=false). Pure int, draws no RNG.
//	[VERIFIED javap AgeableMob.ageUp(int,boolean): iload age; iload amount; bipush 20; imul; iadd ->
//	 i; ifle skip-clamp; iconst_0 -> i (clamp >0 to 0); setAge(i); the forced branch touches
//	 forcedAge/forcedAgeTimer only (both v1 const-0).]
func (e *Entity) ageUp(amount int) {
	newAge := e.breedAge + amount*20
	if newAge > 0 {
		newAge = 0 // clamp: a fed baby lands exactly on adulthood (0), never overshoots positive
	}
	e.breedAge = newAge
}

// getSpeedUpSecondsWhenFeeding is net.minecraft.world.entity.AgeableMob.getSpeedUpSecondsWhenFeeding(
// int ageDelta): `return (int)((float)(ageDelta / 20) * 0.1f);`. The FEED baby branch passes -breedAge
// (a positive number, since a baby's breedAge is negative) to get the SECONDS of grow-up speedup,
// which ageUp then multiplies by 20 back to ticks. The op ORDER is load-bearing and ported verbatim:
// the integer division `ageDelta / 20` is computed FIRST (truncating int division), THEN cast to
// float, THEN * 0.1f, THEN truncated back to int. (For a fresh -24000 baby: -(-24000)=24000; 24000/20
// =1200; 1200f*0.1f=120.0f; (int)120.0f=120 seconds == 2400 ticks of speedup.) Pure int/float, no RNG.
//	[VERIFIED javap AgeableMob.getSpeedUpSecondsWhenFeeding: iload; bipush 20; idiv; i2f; ldc 0.1f;
//	 fmul; f2i; ireturn — int-divide first, float-multiply, truncate.]
func getSpeedUpSecondsWhenFeeding(ageDelta int) int {
	return int(float32(ageDelta/20) * 0.1)
}

// canAgeUp is net.minecraft.world.entity.AgeableMob.canAgeUp() == `isBaby() && !isAgeLocked()`.
// isAgeLocked() reads the AGE_LOCKED SynchedEntityData boolean (DATA_AGE_ID's sibling), which is a v1
// const-false stub (no age-lock subsystem is wired — the tryFeedAnimal BABY branch already documents
// this), so canAgeUp() reduces to isBaby() == breedAge < 0. Sheep.ate calls it to decide whether a baby
// sheep that just ate grass grows up by 60 (ageUp(60)). Pure int read, draws no RNG.
//	[VERIFIED javap AgeableMob.canAgeUp: `return isBaby() && !isAgeLocked();`; isAgeLocked reads
//	 AGE_LOCKED (v1 const-false stub here, same as the tryFeedAnimal canAgeUp comment).]
func (e *Entity) canAgeUp() bool { return e.isBaby() }
