package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
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

	// stuckSpeedMultiplier* is net.minecraft.world.entity.Entity.stuckSpeedMultiplier (a Vec3): the
	// per-axis movement scale a cobweb / sweet-berry-bush / powder-snow entityInside sets via
	// makeStuckInBlock(state, Vec3). Entity.move consumes it at the START of the NEXT move (if
	// lengthSqr > 1e-7 and moverType != PISTON: movement *= stuckSpeedMultiplier, then reset to ZERO
	// and setDeltaMovement(ZERO)); makeStuckInBlock also resetFallDistance(). stuck is the "armed"
	// flag (Vec3.ZERO in vanilla means "not stuck" -- lengthSqr 0 <= 1e-7 skips the multiply); we keep
	// an explicit bool so the zero-value Entity is unambiguously NOT stuck and moveEntity is a strict
	// no-op until checkInsideBlocks arms it. Tick-owned plain values (snapshot-friendly).
	//	[VERIFIED javap Entity.move: stuckSpeedMultiplier.lengthSqr() > 1e-7 && moverType != PISTON ->
	//	 movement = movement.multiply(stuckSpeedMultiplier); stuckSpeedMultiplier = Vec3.ZERO;
	//	 setDeltaMovement(Vec3.ZERO). Entity.makeStuckInBlock: resetFallDistance(); stuckSpeedMultiplier=v.]
	stuckSpeedMultiplierX, stuckSpeedMultiplierY, stuckSpeedMultiplierZ float64
	stuck                                                               bool

	// yaw, pitch are the body look angles in degrees; headYaw is the separate head rotation
	// living entities carry (Float on the wire). Tick-owned plain values.
	yaw, pitch, headYaw float32

	// onGround mirrors the player movement flag for entities — whether the entity rests on
	// a solid block (physics, Plan 06-03). Tick-owned.
	onGround bool

	// horizontalCollision / verticalCollision mirror Entity.horizontalCollision /
	// Entity.verticalCollision: written by moveEntity's "rest" section on every move —
	// horizontal with the Mth.equal 1e-5f tolerance, vertical with the exact != compare.
	// CITE: javap Entity.move (26.2). Tick-owned plain values.
	horizontalCollision bool
	verticalCollision   bool

	// jumping is net.minecraft.world.entity.LivingEntity.jumping — the per-tick "this mob WANTS to
	// jump" flag the JumpControl writes (jumpControl.tick → setJumping(jump)) and the aiStep jump
	// branch reads (`if (jumping && isAffectedByFluids())`). A plain bool (snapshot-friendly,
	// tick-owned, RNG-free), set every tick by the mob's jumpControl. MOB-SUB-04 (Plan 30-02).
	//	[VERIFIED javap LivingEntity.setJumping(boolean): `this.jumping = b;` — a bare field write.]
	jumping bool

	// traveledThisTick marks that this mob already ran its per-tick travel (moveRelative ->
	// move -> gravity -> drag via travelInAir) inside navigation.followThePath during serverAiStep
	// (tickAI phase). tickPhysics reads + clears it so a NAVIGATING mob is not moved a SECOND time
	// that same tick (audit B-A2: navigating mobs got move()+friction applied twice). An IDLE mob
	// (no active path) never sets it, so tickPhysics runs its travelInAir(zero-input) gravity pass
	// as usual. Tick-owned, RNG-free, reset every tick -- not persisted.
	traveledThisTick bool

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

	// itemTickCount is net.minecraft.world.entity.Entity.tickCount for a dropped Item — the
	// free-running per-entity tick counter Entity.tick() increments (`tickCount++`) every server
	// tick. ItemEntity.tick reads it for two vanilla cadences DISTINCT from `age` (which merge
	// averages to the younger value): the resting-item move throttle `(tickCount + getId()) % 4 == 0`
	// and the merge interval `tickCount % (moved ? 2 : 40) == 0`. Incremented in tickItem (the
	// item's super.tick()). Zero/unused for non-item entities.
	//	[VERIFIED javap Entity.tick: `this.tickCount++`; ItemEntity.tick reads #238 tickCount for
	//	 both the (tickCount+id)%4 rest-throttle and the tickCount%interval merge gate.]
	itemTickCount int

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

	// orbCount is ExperienceOrb.count — the number of logical orbs merged into this single entity
	// (default 1). scanForMerges combines a nearby equal-value orb by adding its count into this one
	// and discarding the other; playerTouch decrements count per absorbed sub-orb and discards the
	// entity once it reaches 0. Zero for a non-orb entity (never read unless isOrb). Tick-owned.
	//
	//	[VERIFIED javap ExperienceOrb: `private int count;` initialized to 1 in the ctor; merge()
	//	 does count += other.count; playerTouch does --count; if (count == 0) discard().]
	orbCount int

	// orbRNG is the orb's dedicated RandomSource (ExperienceOrb inherits Entity.random) — a per-orb
	// entityRandom seeded from the orb's entity id (NEVER a mob stream, mirroring fishingRNG). The orb
	// tick draws from it for the lava-pop impulse (setDeltaMovement random on a lava cell). Nil for a
	// non-orb entity; lazily initialized on first use so an orb built before this field is still safe.
	orbRNG *entityRandom

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

	// arrowCrit is AbstractArrow.isCritArrow (setCritArrow): set true by a full-draw player bow shot
	// (BowItem.releaseUsing power==1.0f -> setCritArrow(true)). A crit arrow deals extra damage
	// (onHitEntity: damage += random.nextInt(damage/2 + 2)) and shows the crit particle trail. Cite
	// AbstractArrow.setCritArrow / onHitEntity.
	arrowCrit bool

	// arrowRNG is the arrow's per-entity RandomSource (AbstractArrow.random) -- a dedicated stream seeded
	// from the arrow id at spawn (NEVER a mob stream, mirroring orbRNG/fishingRNG), so the crit-damage
	// draw (onHitEntity: random.nextInt(damage/2 + 2)) never perturbs any mob's per-entity stream and the
	// pig oracle stays byte-identical. Nil until the first crit hit needs it (lazy init).
	arrowRNG *entityRandom

	// arrowEffects are the tipped-arrow MobEffectInstances an Arrow carries (Arrow.getPotionContents
	// / addEffect). A Stray fires SLOWNESS 600 arrows; a Bogged fires POISON 100 arrows (getArrow
	// overrides). On a landed hit arrowOnHitPlayer/Entity applies each to the victim (Arrow
	// .doPostHurtEffects). Nil for a plain arrow. Cite Arrow.getArrow + Arrow.doPostHurtEffects.
	arrowEffects []splashEffect

	// arrowWeapon is AbstractArrow.firedFromWeapon: the ItemStack the arrow was launched from (the bow /
	// crossbow), stored so AbstractArrow.onHitEntity/doKnockback can read its enchantments at hit time.
	// getWeaponItem() returns it; a nil (empty) stack means no firing weapon (a dispenser/summoned arrow).
	// Power (minecraft:damage) and Punch (minecraft:knockback) are effects on THIS stack, gated on the
	// DIRECT_ATTACKER being an arrow — so they apply only when the arrow carries its bow here. Cite
	// AbstractArrow.firedFromWeapon / getWeaponItem / setSoundEvent path in ProjectileWeaponItem.createProjectile.
	arrowWeapon component.SlotData

	// spawnData is the ClientboundAddEntity "data" field (object-specific). For an arrow vanilla sets it to
	// ownerId+1 (the client owner link for crit visuals); 0 for a plain mob. Set at spawn.
	spawnData int32

	// --- THROWN TRIDENT (net.minecraft.world.entity.projectile.arrow.ThrownTrident) --------------------
	//
	// A ThrownTrident is an AbstractArrow subclass (isArrow is ALSO set, so it flies via the shared arrow
	// physics), distinguished by isTrident so the trident-specific tick (Loyalty return + dealtDamage
	// latch) layers on TOP of the arrow flight. It carries the thrown ItemStack's id (the pickup item) and
	// its enchant levels. Zero for every non-trident entity (the trident branch gates on isTrident).
	// Cite ThrownTrident.

	// isTrident marks this arrow as a ThrownTrident. When set, tickArrow runs the trident pre-tick
	// (inGroundTime>4 -> dealtDamage; Loyalty return-to-owner) before the shared AbstractArrow physics.
	isTrident bool

	// tridentLoyalty is ThrownTrident.ID_LOYALTY -- the byte returned by getLoyaltyFromItem ==
	// clamp(getTridentReturnToOwnerAcceleration, 0, 127). In vanilla data this equals the Loyalty enchant
	// level (linear base 1.0, per_level_above_first 1.0). 0 == no Loyalty (the trident stays where it lands).
	tridentLoyalty int

	// tridentDealtDamage is ThrownTrident.dealtDamage -- true once the trident has hit an entity OR sat in
	// the ground for >4 ticks (inGroundTime>4). Loyalty return only begins once dealtDamage (or noPhysics).
	tridentDealtDamage bool

	// tridentInGroundTime is AbstractArrow.inGroundTime -- ticks the trident has been stuck in a block.
	// ThrownTrident.tick sets dealtDamage once inGroundTime>4 (so a landed trident with Loyalty returns).
	tridentInGroundTime int

	// tridentReturning is ThrownTrident's noPhysics-return state (setNoPhysics(true) once homing to the
	// owner). A returning trident ignores block collision and lerps toward the owner's eye each tick.
	tridentReturning bool

	// tridentImpaling / tridentChanneling are the thrown trident's enchant levels, read at spawn from the
	// ItemStack's minecraft:enchantments (the same real seam bow.go uses via stackEnchantments). 0 == the
	// enchant is absent (the vanilla "no enchant" default). onHitEntity reads them for the Impaling damage
	// bonus (vs #sensitive_to_impaling) and the Channeling lightning strike (when thundering && canSeeSky).
	tridentImpaling   int
	tridentChanneling int

	// tridentItemID is the item id of the thrown ItemStack (getPickupItem) -- TRIDENT unless a plugin
	// thrown a variant. A Loyalty trident that reaches its owner gives this item back to the player.
	tridentItemID int32

	// tridentCreativeOnly marks the thrown trident's Pickup as CREATIVE_ONLY (a creative owner's throw):
	// only the creative owner may pick it up, and a lost-owner trident does NOT drop an item (the owner
	// has infinite tridents). ALLOWED (the survival default) is the false case. Cite AbstractArrow.Pickup.
	tridentCreativeOnly bool

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

	// potionLingering marks a thrown potion as a ThrownLingeringPotion (vs a ThrownSplashPotion). On hit a
	// lingering potion does NOT splash — it spawns an AreaEffectCloud carrying its effects (radius 3.0,
	// duration 600, waitTime 10, radiusOnUse -0.5, radiusPerTick -radius/duration). Zero (false) for a
	// splash potion. Set at spawn by spawnLingeringPotion. Cite ThrownLingeringPotion.onHitAsPotion.
	potionLingering bool

	// --- AREA EFFECT CLOUD (net.minecraft.world.entity.AreaEffectCloud) ---------------------------------
	//
	// A stationary NON-mob Entity (isAreaEffectCloud) that shrinks its radius over time and periodically
	// applies its potion effects to every LivingEntity inside its radius (tickCount%5==0), tracking a
	// per-victim reapplication-delay cooldown. Spawned by a lingering potion / DragonFireball. Tick-owned
	// plain values (the AEC tick gates on isAreaEffectCloud). Cite AreaEffectCloud.serverTick.
	isAreaEffectCloud bool
	aecOwnerID        int32          // getOwner() as a THIN id (never a live pointer — the Folia rule)
	aecEffects        []splashEffect // PotionContents effects (potionContents.forEachEffect scaled)
	aecRadius         float32        // DATA_RADIUS (Mth.clamp(x, 0, 32) on set)
	aecDuration       int            // duration (-1 == INFINITE); discard at tickCount-waitTime >= duration
	aecWaitTime       int            // waitTime (default 20; lingering sets 10)
	aecReapplyDelay   int            // reapplicationDelay (default 20)
	aecDurationOnUse  int            // durationOnUse (0 default; lingering leaves 0)
	aecRadiusOnUse    float32        // radiusOnUse (lingering sets -0.5)
	aecRadiusPerTick  float32        // radiusPerTick (lingering: -radius/duration == -0.005)
	aecWaiting        bool           // DATA_WAITING (tickCount < waitTime)
	aecTickCount      int            // the entity's tickCount (AEC gates on it — separate from throwLife)
	aecVictims        map[int32]int  // victim entity id -> tickCount at which the reapplication delay expires

	// --- FIREWORK ROCKET (net.minecraft.world.entity.projectile.FireworkRocketEntity) -------------------
	//
	// A NON-mob projectile (isFirework). Either free-flying (self-accelerating upward) or ATTACHED to a
	// fall-flying player (the elytra boost — accelerate the rider along its look direction). At life>=lifetime
	// it detonates: broadcastEntityEvent(17) + dealExplosionDamage (5 + 2*starCount, falloff by distance).
	// Tick-owned plain values (the firework tick gates on isFirework). Cite FireworkRocketEntity.tick.
	isFirework        bool
	fireworkOwnerID   int32         // getOwner() as a THIN id
	fireworkAttachID  int32         // attachedToEntity id (a fall-flying player) — 0 == free-flying
	fireworkLife      int           // life (ticks since spawn)
	fireworkLifetime  int           // 10*(1+flightDuration) + nextInt(6) + nextInt(7)
	fireworkStarCount int           // number of firework_explosion stars (the explosion-damage bonus)
	fireworkShotAngle bool          // isShotAtAngle (crossbow multishot) — free flight has no upward self-accel
	fireworkRNG       *entityRandom // FireworkRocketEntity.random — the dedicated per-firework stream (lifetime + init velocity draws)

	// --- THROWABLE ITEM PROJECTILE (net.minecraft.world.entity.projectile.throwableitemprojectile.*) ----
	//
	// A snowball / egg / ender_pearl: a NON-mob ThrowableProjectile that arcs (getDefaultGravity 0.03,
	// getAirDrag 0.99, water 0.8) and resolves on the FIRST block/entity hit. Order per ThrowableProjectile
	// .tick: applyGravity -> applyInertia(drag) -> getHitResult(move) -> onHit(discard + per-kind effect).
	// Zero for every non-throwable entity (the throwable tick gates on isThrowable). Cite ThrowableProjectile
	// / Snowball / ThrownEnderpearl.
	isThrowable   bool
	throwableKind int   // throwSnowball / throwEgg / throwEnderPearl
	throwOwnerID  int32 // getOwner() as a THIN id (never a live pointer — the Folia rule)
	throwOldX     float64
	throwOldY     float64
	throwOldZ     float64       // oldPosition() — the ender_pearl teleports the owner to the PRE-move position
	throwLife     int32         // ticks alive; a throwable that never lands discards at a hard cap
	throwRNG      *entityRandom // ThrowableProjectile.random — the per-throwable stream (xp bottle orb split draws)
	// snowballHitsMobs marks a snowball whose flight also resolves against MOB victims (the snow_golem
	// ranged attack). Vanilla Snowball.onHitEntity ALWAYS applies to any LivingEntity (Blaze 3 / else 0 +
	// the thrown knockback); v1's shared throwable tick tested PLAYERS only. Rather than widen the shared
	// entity-hit scan for every throwable (which would perturb the egg/ender-pearl tests), this per-entity
	// flag opts a SNOW-GOLEM-fired snowball into the additive mob-victim scan (snowballFindHitMobVictim) so
	// the golem's snowball actually reaches its Enemy target. Zero for a player-thrown snowball / egg /
	// ender pearl -> their behavior is byte-identical. Cite Snowball.onHitEntity.
	snowballHitsMobs bool

	// isLlamaSpit marks a LlamaSpit projectile (net.minecraft.world.entity.projectile.LlamaSpit): a NON-mob
	// projectile spawned by Llama.spit that FLIES (getAirDrag 0.99, getDefaultGravity 0.06) and, on the first
	// LivingEntity it crosses, deals 1.0 spit damage (LlamaSpit.onHitEntity: hurtServer(spit(this, owner),
	// 1.0F)) then discards; a block hit also discards (LlamaSpit.onHitBlock). Tick-owned; set and read ONLY
	// for a llama spit (the flight tick gates on isLlamaSpit, so a world with no spit draws zero extra work
	// and the pig oracle stream is unperturbed). The spit reuses arrowShooterID as getOwner() (a THIN id, the
	// Folia rule) and vx/vy/vz as its deltaMovement. Cite LlamaSpit.tick / LlamaSpit.onHitEntity.
	isLlamaSpit bool

	// --- HURTING PROJECTILE (net.minecraft.world.entity.projectile.hurtingprojectile.*) ----------------
	//
	// A hurting projectile (small/large fireball, wither skull) is a NON-mob projectile (isHurting) with
	// NO gravity — it flies STRAIGHT with a self-acceleration term: each tick applyInertia does
	// deltaMovement = (deltaMovement + deltaMovement.normalize()*accelerationPower) * inertia. It ignites
	// on contact and runs a per-kind onHit (fire damage / explosion / wither). Tick-owned plain values, set
	// and read ONLY for a hurting projectile (the hurting tick gates on isHurting). Cite AbstractHurtingProjectile.
	isHurting     bool
	hurtingKind   int     // hurtSmallFireball / hurtLargeFireball / hurtWitherSkull
	hurtOwnerID   int32   // getOwner() as a THIN id (never a live pointer — the Folia rule)
	hurtAccelPow  float64 // AbstractHurtingProjectile.accelerationPower (default 0.1)
	hurtExplosion int     // LargeFireball.explosionPower (default 1); unused by other kinds
	hurtDangerous bool    // WitherSkull.isDangerous() — a wither-boss "dangerous" skull (inertia 0.73)
	hurtLife      int32   // ticks alive; a hurting projectile that never lands discards at a hard cap

	// --- FISHING HOOK / BOBBER (net.minecraft.world.entity.projectile.FishingHook) --------------------
	//
	// The bobber is a NON-mob projectile (isFishingHook) cast from a fishing rod. Its whole behavior is
	// the FishingHook.tick state machine (FLYING -> BOBBING -> catchingFish countdown -> a bite) plus the
	// retrieve roll. Tick-owned plain values, set/read ONLY for a bobber (the fishing tick gates on
	// isFishingHook). Its RNG is a dedicated per-bobber entityRandom seeded from the bobber id (NEVER a
	// mob/pig stream — the pig oracle is untouched). Cite FishingHook fields.

	// isFishingHook marks this entity as a FishingHook. The fishing tick runs ONLY for entities with this
	// set. Set at spawn by spawnFishingHook.
	isFishingHook bool

	// fishingOwnerID is FishingHook.getPlayerOwner() modeled as the caster's entity id (THIN id, the Folia
	// rule — never a live *tickPlayer). The retrieve pull/loot is attributed to this player; the bobber
	// discards if the owner drops the rod / strays > 32 blocks (shouldStopFishing).
	fishingOwnerID int32

	// fishingRNG is the bobber's dedicated RandomSource (FishingHook.random analogue) — a per-bobber
	// entityRandom seeded from the bobber id at spawn. All fishing draws (the cast spread, the wait/lure/
	// hook countdowns, the loot seed) come from HERE, never a mob stream, so the pig oracle is unperturbed.
	fishingRNG *entityRandom

	// FishingHook state-machine fields (FishingHook.currentState + the countdowns). currentState:
	// 0=FLYING, 1=HOOKED_IN_ENTITY, 2=BOBBING. life is the on-ground despawn counter (>=1200 -> discard).
	// nibble/timeUntilLured/timeUntilHooked are the catchingFish countdowns; biting is the DATA_BITING
	// bite flag; openWater is isOpenWaterFishing() (gates treasure); outOfWaterTime tracks surface float.
	// fishingHookedID is the hooked entity id (0 == none). Cite FishingHook.
	fishingState       int32
	fishingLife        int32
	fishingNibble      int32
	fishingLured       int32 // timeUntilLured
	fishingHooked      int32 // timeUntilHooked
	fishingBiting      bool
	fishingOpenWater   bool
	fishingOutOfWater  int32
	fishingHookedID    int32
	fishingLure        int32   // lureSpeed (Lure enchant; v1 cited-stub 0)
	fishingLuck        int32   // Luck of the Sea (v1 cited-stub 0)
	fishingWanderAngle float32 // FishingHook.fishAngle (the wander/tease particle heading)

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

	// --- MUSHROOM COW VARIANT (net.minecraft.world.entity.animal.cow.MushroomCow) --------------------
	//
	// Tick-owned plain values, set/read ONLY for a Mooshroom (typ == entity.Mooshroom.ID). mooshroomVariant
	// mirrors the DATA_TYPE entity-data accessor (MushroomCow.Variant.id: RED==0 == Variant.DEFAULT, BROWN==1);
	// a fresh mooshroom starts RED (0). lastLightningBoltUUID mirrors MushroomCow.lastLightningBoltUUID: the
	// per-bolt guard in thunderHit so the RED<->BROWN toggle fires ONCE per struck bolt (not once per damage
	// tick the bolt is alive). hasLastLightningBolt distinguishes the vanilla null default (no bolt seen yet)
	// from a real all-zero UUID. Zero for every non-mooshroom entity. Cite MushroomCow.thunderHit + Variant.
	mooshroomVariant      int32     // MushroomCow DATA_TYPE (0=RED default, 1=BROWN)
	lastLightningBoltUUID uuid.UUID // MushroomCow.lastLightningBoltUUID (the per-bolt toggle guard)
	hasLastLightningBolt  bool      // the lastLightningBoltUUID field is set (vanilla non-null)

	// --- LAND-MOB SPAWN VARIANTS (Rabbit/Cat/Fox) + Ocelot trust ------------------------------------
	//
	// Tick-owned plain values set at finalizeSpawn (plugin_mob_decl.go) and read ONLY for the owning
	// mob type; zero for every other entity (the pig oracle keeps them 0 and draws no RNG on their
	// account). Each mirrors the vanilla DATA accessor / trust flag:
	//   rabbitVariant  -- Rabbit DATA_TYPE (Rabbit.Variant.id: BROWN 0, WHITE 1, BLACK 2, WHITE_SPLOTCHED 3,
	//                    GOLD 4, SALT 5, EVIL 99). Cite Rabbit.getRandomRabbitVariant + Variant static init.
	//   catVariant     -- Cat DATA_VARIANT (a cat_variant registry index; v1 stores the biome-select result;
	//                    default 0). Cite Cat.finalizeSpawn (VariantUtils.selectVariantToSpawn).
	//   foxVariant     -- Fox DATA_TYPE_ID (Fox.Variant.byBiome: RED 0 default, SNOW 1). Cite Fox.finalizeSpawn.
	//   ocelotTrusting -- Ocelot.isTrusting()/setTrusting() (DATA_TRUSTING). A trusting ocelot no longer
	//                    tempt-flees and can breed; set by the feed-trust interact. Cite Ocelot.mobInteract.
	rabbitVariant  int32
	catVariant     int32
	foxVariant     int32
	ocelotTrusting bool

	// --- ARMADILLO DANGER MEMORY (Brain MemoryModuleType.DANGER_DETECTED_RECENTLY) -------------------
	//
	// armadilloDangerExpiry is the v1 stand-in for Brain.getTimeUntilExpiry(DANGER_DETECTED_RECENTLY):
	// set to 80 (setMemoryWithExpiry(..., 80L)) each tick a threat is scared-by, and decremented toward 0
	// otherwise. The ArmadilloBallUp state machine reads it (SCARED->UNROLLING when expiry < 30 == the
	// UNROLLING animation duration; UNROLLING->SCARED when expiry > 30). Zero for every non-armadillo.
	// Cite Armadillo.onSyncedDataUpdated (setMemoryWithExpiry 80L) + ArmadilloAi.ArmadilloBallUp.tick.
	armadilloDangerExpiry int

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
	// ghastServerStillTimeout mirrors HappyGhast.serverStillTimeout: a countdown (max 10) armed to 10
	// whenever a non-riding player stands on top of the ghast (scanPlayerAboveGhast). While it is > 0 the
	// ghast is "on still timeout" (isOnStillTimeout): it holds position (requiresPrecisePosition) and the
	// harness ride is NOT steerable. ghastStaysStill mirrors the STAYS_STILL synched entity data
	// (syncStayStillFlag: STAYS_STILL := serverStillTimeout > 0) -- the client-visible frozen flag. Both
	// are HappyGhast-only; zero for every other entity. Cite HappyGhast.tick / setServerStillTimeout /
	// syncStayStillFlag / isOnStillTimeout / scanPlayerAboveGhast.
	ghastServerStillTimeout int32
	ghastStaysStill         bool
	// ghastTickCount is the HappyGhast-relevant slice of Entity.tickCount (the free-running per-entity
	// counter Entity.tick increments each server tick). It gates the still-timeout decrement's on-load
	// grace: HappyGhast.tick only decrements serverStillTimeout while tickCount > STILL_TIMEOUT_ON_LOAD_
	// GRACE_PERIOD (60), so a ghast loaded WITH a still_timeout holds it for the first 60 ticks. Ghast-only.
	// Cite HappyGhast.tick (tickCount > 60 grace) + Entity.tick (tickCount++).
	ghastTickCount int32
	// ghastRequiresPrecisePosition mirrors HappyGhast.aiStep's setRequiresPrecisePosition(isOnStillTimeout())
	// — Entity.requiresPrecisePosition, which forces the tracker to send an exact position sync instead of
	// the quantized delta while the ghast is frozen. Tracked as a tick-owned bool; the ClientboundEntity
	// PositionSyncPacket broadcast side effect is client-visual (cite-deferred like DATA_IS_CHARGING). Ghast-
	// only. Cite HappyGhast.aiStep -> setRequiresPrecisePosition(isOnStillTimeout()).
	ghastRequiresPrecisePosition bool
	// --- HOSTILE GHAST (net.minecraft.world.entity.monster.Ghast) ----------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a hostile Ghast (ghastAiStep gates on typ ==
	// entity.Ghast.ID). The hostile Ghast REUSES the ghastWantedX/Y/Z + ghastHasWanted + ghastFloat
	// Duration fields above for its GhastMoveControl + RandomFloatAroundGoal (an entity is either a happy
	// ghast OR a hostile ghast, never both, so the shared fields never collide). isGhast marks the entity;
	// ghastChargeTime mirrors GhastShootFireballGoal.chargeTime (0..20 charge, then -40 cooldown);
	// ghastCharging mirrors DATA_IS_CHARGING (set to chargeTime > 10). ghastExplosionPower mirrors
	// Ghast.explosionPower (default 1) -- the LargeFireball's blast radius. Zero for every non-ghast entity.
	isGhast             bool
	ghastChargeTime     int32
	ghastCharging       bool
	ghastExplosionPower int
	// --- BLAZE (net.minecraft.world.entity.monster.Blaze) ------------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Blaze (blazeAiStep gates on typ == entity.Blaze.ID).
	// isBlaze marks the entity. blazeAttackStep mirrors Blaze$BlazeAttackGoal.attackStep (the 0..>4
	// fireball-burst counter: step 1 charges, steps 2..4 each shoot one SmallFireball, then reset);
	// blazeAttackTime mirrors BlazeAttackGoal.attackTime (the per-step cadence countdown: 60 at charge,
	// 6 between the burst shots, 100 cooldown, 20 the melee swing); blazeLastSeen mirrors
	// BlazeAttackGoal.lastSeen (ticks since the target was last in line-of-sight, gates the pursue move).
	// blazeCharged mirrors DATA_FLAGS_ID bit 1 (setCharged -> the on-fire visual). Zero for non-blazes.
	isBlaze         bool
	blazeAttackStep int32
	blazeAttackTime int32
	blazeLastSeen   int32
	blazeCharged    bool
	// blazeAllowedHeightOffset mirrors Blaze.allowedHeightOffset (the vertical band the blaze hovers
	// above its target; the ctor seeds 0.5f, then customServerAiStep refreshes it every 100 ticks to
	// random.triangle(0.5, 6.891)). blazeNextHeightOffsetChangeTick mirrors Blaze.nextHeightOffsetChangeTick
	// (the countdown gating that refresh). Zero for non-blazes. Cite Blaze.customServerAiStep.
	blazeAllowedHeightOffset        float64
	blazeNextHeightOffsetChangeTick int32
	// --- ENDER DRAGON (net.minecraft.world.entity.boss.enderdragon.EnderDragon) --------------------
	//
	// Tick-owned plain values, set/read ONLY for an EnderDragon (enderDragonAiStep + dragonHurtPart gate
	// on typ == entity.EnderDragon.ID via e.dragon != nil). All the dragon state is grouped behind ONE
	// pointer (e.dragon) so a plain mob (the pig oracle) pays exactly one nil pointer and touches NONE of
	// these fields -- additive-minimal, zero new RNG, byte-identical for every non-dragon. Cite EnderDragon.
	dragon *dragonState
	// --- END CRYSTAL (net.minecraft.world.entity.boss.enderdragon.EndCrystal) -----------------------
	//
	// Tick-owned plain values, set/read ONLY for an EndCrystal (tickEndCrystal + endCrystalHurt gate on
	// isEndCrystal). isEndCrystal marks the entity; endCrystalTime mirrors EndCrystal.time (the free-
	// running ++ counter EndCrystal.tick increments, drives the beam/bob client visual). Zero for every
	// non-crystal entity. Cite EndCrystal.tick.
	isEndCrystal   bool
	endCrystalTime int32
	// --- WITHER BOSS (net.minecraft.world.entity.boss.wither.WitherBoss) ----------------------------
	//
	// Tick-owned plain values, set/read ONLY for a WitherBoss (witherAiStep + witherDropNetherStar gate
	// on typ == entity.Wither.ID via e.wither != nil). All the wither state is grouped behind ONE
	// pointer (e.wither) so a plain mob (the pig oracle) pays exactly one nil pointer and touches NONE
	// of these fields -- additive-minimal, zero new RNG, byte-identical for every non-wither. Cite WitherBoss.
	wither *witherState
	// --- PHANTOM (net.minecraft.world.entity.monster.Phantom) --------------------------------------
	//
	// Tick-owned state, set/read ONLY for a Phantom (phantomAiStep + phantomIsFlyer gate on e.phantom !=
	// nil). All the phantom state (the CIRCLE/SWOOP attack phase, the anchor point + altitude, the orbit
	// angle/distance/height/clockwise, the move-target point, the move-control speed, the sweep/scan
	// timers) is grouped behind ONE pointer (e.phantom) so a plain mob (the pig oracle) pays exactly one
	// nil pointer and touches NONE of these fields -- additive-minimal, zero new RNG, byte-identical for
	// every non-phantom. Cite Phantom + its three goals.
	phantom *phantomState
	// --- WARDEN (net.minecraft.world.entity.monster.warden.Warden, entity id 143) -------------------
	//
	// Tick-owned state, set/read ONLY for a Warden (wardenAiStep gates on e.warden != nil). All the
	// warden state (the per-suspect AngerManagement map, the emerge/dig lifecycle timers, the SonicBoom
	// charge/cooldown, the dig-away no-anger counter) is grouped behind ONE pointer (e.warden) so a plain
	// mob (the pig oracle) pays exactly one nil pointer and touches NONE of these fields -- additive-
	// minimal, zero new RNG, byte-identical for every non-warden. Cite Warden + AngerManagement + SonicBoom.
	warden *wardenState
	// --- SHULKER (net.minecraft.world.entity.monster.Shulker, entity id 112) -------------------------
	//
	// Tick-owned state, set/read ONLY for a Shulker (shulkerAiStep gates on e.shulker != nil). All the
	// shulker state (the peek amount 0..100 + the covered-armor toggle, the attach face, the color, the
	// ranged attack timer, the teleport-on-expose) is grouped behind ONE pointer (e.shulker) so a plain
	// mob (the pig oracle) pays exactly one nil pointer and touches NONE of these fields -- additive-
	// minimal, zero new RNG, byte-identical for every non-shulker. Cite Shulker + Shulker$ShulkerAttackGoal.
	shulker *shulkerState
	// shulkerBullet is the live ShulkerBullet homing state (owner + target + life), set/read ONLY for a
	// ShulkerBullet (shulkerBulletTick gates on e.shulkerBullet != nil). Additive-minimal; nil for every
	// other entity. Cite ShulkerBullet.
	shulkerBullet *shulkerBulletState
	// --- GUARDIAN / ELDER_GUARDIAN (net.minecraft.world.entity.monster.{Guardian,ElderGuardian}) -----
	//
	// Tick-owned Guardian/ElderGuardian beam state, set/read ONLY for a guardian (guardianAiStep +
	// guardianHurtThorns gate on e.guardian != nil). All the guardian state (the elder subtype flag + the
	// GuardianAttackGoal.attackTime beam-charge counter) is grouped behind ONE pointer (e.guardian) so a
	// plain mob (the pig oracle) pays exactly one nil pointer and touches NONE of these fields -- additive-
	// minimal, zero new RNG, byte-identical for every non-guardian. Cite Guardian + Guardian.GuardianAttackGoal.
	guardian *guardianState
	// --- MAGMA CUBE (net.minecraft.world.entity.monster.cubemob.MagmaCube) --------------------------
	//
	// Tick-owned plain values, set/read ONLY for a MagmaCube (magmaCubeAiStep gates on typ ==
	// entity.MagmaCube.ID). isMagmaCube marks the entity. MagmaCube is an AbstractCubeMob, so it REUSES
	// the cubeSize/cubeMoveYRot/cubeJumpDelay/cubeAggressive/cubeWantMove/cubeWasOnGround move-control
	// fields above (an entity is either a SulfurCube OR a MagmaCube, never both, so the shared cube fields
	// never collide). magmaCubeAttackTime mirrors AbstractCubeMob$CubeMobAttackGoal.growTiredTimer only in
	// so far as the attack goal drives the move control toward the target; the per-size attributes (MAX_HEALTH
	// size*size, MOVEMENT_SPEED 0.2+0.1*size, ATTACK_DAMAGE size, ARMOR size*3) live in the AttributeMap.
	// Zero for every non-magma-cube entity. Cite MagmaCube + AbstractCubeMob.
	isMagmaCube bool
	// --- SLIME (net.minecraft.world.entity.monster.cubemob.Slime) ----------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Slime (slimeAiStep gates on typ == entity.Slime.ID).
	// isSlime marks the entity. Slime is an AbstractCubeMob, so it REUSES the shared cube move-control
	// fields above (cubeSize/cubeMoveYRot/cubeJumpDelay/cubeAggressive/cubeWantMove/cubeWasOnGround) --
	// an entity is exactly one of SulfurCube / MagmaCube / Slime, never two, so the shared cube fields
	// never collide. slimeXpReward mirrors the Slime.setSize `xpReward = size` field (Mob.getBaseExperience
	// Reward returns xpReward; Slime does NOT override it, unlike Animal's 1+nextInt(3)). The per-size
	// attributes (MAX_HEALTH size*size, MOVEMENT_SPEED 0.2+0.1*size, ATTACK_DAMAGE size -- NO ARMOR)
	// live in the AttributeMap. Zero for every non-slime entity. Cite Slime + AbstractCubeMob.
	isSlime       bool
	slimeXpReward int32
	// --- STRIDER (net.minecraft.world.entity.monster.Strider) --------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Strider (striderAiStep gates on typ == entity.Strider.ID).
	// isStrider marks the entity. striderSuffocating mirrors DATA_SUFFOCATING (the cold-shiver state: true
	// when OFF a warm block / out of lava, which applies the SUFFOCATING_MODIFIER -0.34 ADD_MULTIPLIED_BASE
	// transient MOVEMENT_SPEED modifier via setStriderSuffocating). Zero for every non-strider entity. Cite
	// Strider.setSuffocating / isSuffocating / DATA_SUFFOCATING.
	isStrider          bool
	striderSuffocating bool
	// striderFinalized guards the one-shot Strider.finalizeSpawn variant roll (jockey/baby/saddle) so it
	// draws exactly once at natural spawn. striderSaddled mirrors the SADDLE equipment slot set on a
	// zombified-piglin jockey strider (a guaranteed-drop saddle) -- the rider-mount itself is DEFERRED
	// (no passenger subsystem) but the saddle set + the rng draws happen 1:1. Cite Strider.finalizeSpawn.
	striderFinalized bool
	striderSaddled   bool
	// striderBoosting / striderBoostTime / striderBoostTimeTotal port ItemBasedSteering (the DATA_BOOST_TIME
	// steering timer): boost(rng) sets boosting=true, boostTime=0, boostTimeTotal=nextInt(841)+140; tickBoost()
	// increments boostTime and clears boosting past total; boostFactor() = boosting ? 1.0+1.15f*sin(boostTime/
	// total*PI) : 1.0. DATA_BOOST_TIME (the synced total) is striderBoostTimeTotal here. Cite ItemBasedSteering.
	striderBoosting       bool
	striderBoostTime      int
	striderBoostTimeTotal int
	// --- WITHER SKELETON / HOGLIN STATE (nether roster) ---------------------------------------------
	//
	// Tick-owned plain values. isWitherSkeleton / isHoglin / isZoglin mark the entity (set/read ONLY by
	// their per-type aiStep, gated on typ). meleeCooldown is the shared MeleeAttackGoal swing countdown
	// (resetAttackCooldown == 20): decremented each tick, reset to 20 on a swing -- used by BOTH the wither
	// skeleton and the hoglin (an entity is exactly one type, so no collision). hoglinAttackAnimTicks
	// mirrors Hoglin.attackAnimationRemainingTicks (the 10-tick attack animation, client cue).
	// hoglinTimeInOverworld mirrors Hoglin.timeInOverworld (the zoglin-conversion timer: converts at > 300
	// while in a PIGLINS_ZOMBIFY dimension i.e. NOT the nether). Zero for every other entity. Cite
	// WitherSkeleton / Hoglin (attackAnimationRemainingTicks / timeInOverworld / CONVERSION_TIME 300).
	isWitherSkeleton      bool
	isHoglin              bool
	isZoglin              bool
	meleeCooldown         int
	hoglinAttackAnimTicks int
	hoglinTimeInOverworld int
	// zoglinAttackTargetExpiry mirrors the Zoglin brain's ATTACK_TARGET memory expiry (gametime tick at
	// which the latched target lapses). Zoglin.setAttackTarget -> brain.setMemoryWithExpiry(ATTACK_TARGET,
	// le, 200L): a retaliation-latched target survives for 200 ticks even if it leaves the nearest-scan
	// radius. 0 == no latch (the plain nearest-scan owns the target). Zoglin-only; zero for every other
	// entity. Cite Zoglin.setAttackTarget (Brain.setMemoryWithExpiry ATTACK_TARGET, 200L).
	zoglinAttackTargetExpiry int64
	// hoglinDimension records the dimension the hoglin lives in (dimOverworld default / dimNether), the
	// bounded stand-in for reading environmentAttributes.PIGLINS_ZOMBIFY at the entity's position: the
	// conversion runs everywhere EXCEPT dimNether. Set at spawn (spawnHoglin) from the spawn context; the
	// entity store is single-dimension in v1, so this is how a hoglin knows it is (not) in the nether.
	hoglinDimension int
	// hoglinPacifiedTicks / hoglinBreedTarget / hoglinAvoidTicks / hoglinAvoidTargetID are the bounded
	// stand-ins for the HoglinAi brain memories the target/repellent/piglin-avoid logic reads: PACIFIED
	// (an expiry countdown seeded 200t by BecomePassiveIfMemoryPresent when NEAREST_REPELLENT is present),
	// BREED_TARGET (whether the hoglin is currently breeding), and AVOID_TARGET (the retreat target id +
	// its expiry, seeded RETREAT_DURATION == rangeOfSeconds(5,20) sampled). While pacified OR breeding the
	// hoglin acquires NO attack target (HoglinAi.findNearestValidAttackTarget); while avoiding it flees the
	// avoid target at 1.3 speed. Zero for every other entity. Cite HoglinAi (PACIFIED / BREED_TARGET /
	// AVOID_TARGET, REPELLENT_PACIFY_TIME 200, RETREAT_DURATION).
	hoglinPacifiedTicks int
	hoglinBreedTarget   bool
	hoglinAvoidTicks    int
	hoglinAvoidTargetID int32
	// --- ZOMBIE/SKELETON VARIANTS (Drowned/Stray/Bogged/ZombieVillager) ------------------------------
	//
	// Tick-owned plain values, set/read ONLY for their own type. isDrowned/isStray/isBogged mark the
	// entity so the per-type aiStep + arrow-tag hooks fire. boggedSheared mirrors Bogged DATA_SHEARED
	// (a sheared Bogged shows no mushrooms + drops nothing more from shearing). Zero for every other
	// entity. Cite Drowned / Stray / Bogged.
	isDrowned bool
	// drownedTridentTime mirrors RangedAttackGoal.attackTime for the DrownedTridentAttackGoal cadence:
	// the inter-throw cooldown, decremented EVERY tick and re-armed to floor(dist*(max-min)+min) (==40 for
	// the drowned, min==max==40) on a throw. Starts -1 (the RangedAttackGoal ctor sentinel). Cite
	// net.minecraft.world.entity.ai.goal.RangedAttackGoal.attackTime + DrownedTridentAttackGoal.
	drownedTridentTime int
	// drownedTridentSeeTime mirrors RangedAttackGoal.seeTime: the line-of-sight run length (++ while the
	// target is visible, RESET to 0 the moment it is not -- the base RangedAttackGoal resets, it does NOT
	// decrement like RangedBowAttackGoal). Gates the chase-vs-stop move (seeTime<5 -> keep closing). Cite
	// net.minecraft.world.entity.ai.goal.RangedAttackGoal.seeTime.
	drownedTridentSeeTime int
	isStray               bool
	isBogged              bool
	boggedSheared         bool
	// ZombieVillager conversion state (net.minecraft.world.entity.monster.zombie.ZombieVillager):
	// isZombieVillager marks the entity; zvConverting mirrors DATA_CONVERTING_ID (isConverting());
	// zvConversionTime mirrors villagerConversionTime (the per-tick countdown started by the cure);
	// zvConversionStarter mirrors conversionStarter (the curer, entity id; 0 == none/natural). At
	// startConverting: zvConversionTime = random.nextInt(2401)+3600. tick(): zvConversionTime -=
	// getConversionProgress() (base 1); at <=0 -> finishConversion to a Villager. Zero for every other
	// entity. Cite ZombieVillager.startConverting + tick + finishConversion.
	isZombieVillager    bool
	zvConverting        bool
	zvConversionTime    int
	zvConversionStarter int32
	// ZOMBIE water-conversion state (net.minecraft.world.entity.monster.zombie.Zombie.tick):
	// zombieInWaterTime mirrors Zombie.inWaterTime (++ each tick the zombie's eye is in WATER while
	// convertsInWater(); RESET to -1 the moment the eye leaves water; at >= 600 -> startUnderWaterConversion(300)).
	// zombieConversionTime mirrors Zombie.conversionTime (the drowning countdown once started; -- each tick,
	// and at < 0 -> doUnderWaterConversion). zombieUnderWaterConverting mirrors DATA_DROWNED_CONVERSION_ID
	// (isUnderWaterConverting()), set true by startUnderWaterConversion. A base Zombie converts to a Drowned;
	// a Husk (convertsInWater() true, doUnderWaterConversion overridden) converts to a Zombie. Zero for every
	// non-zombie-family entity. Cite Zombie.tick + startUnderWaterConversion + doUnderWaterConversion + Husk.
	zombieInWaterTime          int
	zombieConversionTime       int
	zombieUnderWaterConverting bool
	// --- BEE / GOAT / FROG (passive animals, Task) --------------------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for their own type (each *AiStep gates on typ). isBee/isGoat/
	// isFrog mark the entity. beeHasStung mirrors Bee.hasStung (DATA_FLAGS bit set on a sting -> the bee then
	// dies gradually). beeTimeSinceSting mirrors Bee.timeSinceSting (the post-sting death countdown: once
	// hasStung, ++ every tick, and on (timeSinceSting % 5 == 0 && nextInt(clamp(1200-timeSinceSting,1,1200))
	// == 0) the bee takes generic getHealth() self-damage -> dies with rising probability). goatScreaming
	// mirrors Goat.isScreamingGoat (DATA_IS_SCREAMING_GOAT, rolled nextDouble() < 0.02 at finalizeSpawn --
	// the louder, ram-prone variant). frogVariant mirrors Frog's DATA_VARIANT_ID (0 temperate / 1 warm /
	// 2 cold, biome-derived at spawn). Zero for every other entity. Cite Bee.hasStung/timeSinceSting +
	// Bee.customServerAiStep sting-death, Goat.isScreamingGoat + finalizeSpawn, Frog FrogVariant.
	isBee              bool
	isGoat             bool
	isFrog             bool
	beeHasStung        bool
	beeTimeSinceSting  int
	beeUnderWaterTicks int // Bee.underWaterTicks: ++ while isInWater, reset out; >20 -> drown 1.0F (unconditional)
	// --- BEE HIVE / POLLINATION (BEEHIVE-01) ---------------------------------------------------------
	//
	// DISTINCT-named from the anger commit's beeHasStung/beeTimeSinceSting (owned there -- do NOT reuse).
	// These mirror the Bee hive/pollination state read by BeehiveBlockEntity + the hive/pollination goals
	// (bee_hive.go): beeHasNectar mirrors Bee.hasNectar (DATA_FLAGS FLAG_HAS_NECTAR bit; dropOffNectar
	// clears it, a completed pollinate sets it); beeSavedFlowerPos mirrors Bee.savedFlowerPos (the last
	// flower it pollinated, restored on hive release); beeHivePos mirrors Bee.hivePos (the hive it belongs
	// to, set by BeehiveBlockEntity.Occupant.createEntity via setHivePos). nil pos == "none". Zero for
	// every non-bee entity. CITE: Bee.hasNectar/setHasNectar/dropOffNectar, Bee.savedFlowerPos,
	// Bee.hivePos/setHivePos, BeehiveBlockEntity.Occupant.createEntity.
	beeHasNectar      bool
	beeSavedFlowerPos *pk.Position
	beeHivePos        *pk.Position
	// Bee hive/pollination timers (Bee instance fields, VERIFIED javap this session). All bee-gated
	// (read/written only by the bee goals + beeAiStep), distinct from the anger commit fields.
	// stayOutOfHiveCountdown (Bee.stayOutOfHiveCountdown, set to 400 by emptyAllLivingFromHive; decrements
	// each customServerAiStep), ticksWithoutNectarSinceExitingHive (Bee.ticksWithoutNectarSinceExitingHive;
	// ++ each customServerAiStep, reset on setHasNectar(true)), remainingCooldownBeforeLocatingNewFlower /
	// NewHive (the two locate cooldowns, decremented each customServerAiStep). beePollinating mirrors
	// BeePollinateGoal.pollinating (wantsToEnterHive reads it via isPollinating). CITE Bee.customServerAiStep.
	beeStayOutOfHiveCountdown          int
	beeTicksWithoutNectarSinceExiting  int
	beeRemainingCooldownLocatingFlower int
	beeRemainingCooldownLocatingHive   int
	beePollinating                     bool
	// beeHiveBlacklist mirrors BeeGoToHiveGoal.blacklistedTargets (max 3, FIFO): hive positions the bee
	// failed to path to, shared between the locate + go-to-hive goals (in vanilla it lives on the goToHiveGoal
	// instance the locate goal reaches via bee.goToHiveGoal; here it is bee-owned so both goals see it). CITE
	// BeeGoToHiveGoal.{blacklistedTargets,blacklistTarget,isTargetBlacklisted,clearBlacklist}.
	beeHiveBlacklist []pk.Position
	goatScreaming    bool
	// goatHasLeftHorn / goatHasRightHorn mirror Goat's DATA_HAS_LEFT_HORN / DATA_HAS_RIGHT_HORN (both
	// default true; finalizeSpawn's UNIHORN roll can clear one; RamTarget.dropHorn clears one on a ram into
	// a #snaps_goat_horn block). goatRamCooldownTicks mirrors the RAM_COOLDOWN_TICKS memory (sampled from
	// GoatAi.TIME_BETWEEN_RAMS(_SCREAMER) at spawn + on finishRam; counted down by goatAiStep). Zero for
	// every non-goat entity. Cite Goat.hasLeftHorn/hasRightHorn/dropHorn + RamTarget.finishRam.
	goatHasLeftHorn      bool
	goatHasRightHorn     bool
	goatRamCooldownTicks int32
	// goatBrain groups the TRANSIENT Goat GoatAi brain phase (ram prepare/charge, long-jump prepare/mid-jump
	// + the LONG_JUMP_COOLDOWN_TICKS memory) behind ONE pointer so a non-goat pays exactly one nil pointer
	// and touches NONE of these fields (additive-minimal, byte-identical for the pig oracle). Nil for every
	// non-goat. RAM_COOLDOWN_TICKS stays the flat goatRamCooldownTicks above. Cite GoatAi (LongJumpToRandomPos
	// / PrepareRamNearestTarget / RamTarget).
	goatBrain   *goatBrainState
	frogVariant int
	// --- CAMEL / SNIFFER / ALLAY / AXOLOTL (passive animals, Task) ----------------------------------
	//
	// Tick-owned plain values, set/read ONLY for their own type (each *AiStep gates on typ). isCamel/
	// isSniffer/isAllay/isAxolotl mark the entity. axolotlVariant mirrors Axolotl's DATA_VARIANT (0 lucy /
	// 1 wild / 2 gold / 3 cyan / 4 blue; the blue is the rare breeding mutation, DEFERRED). Zero for every
	// other entity. Camel/Sniffer/Axolotl are BRAIN Animals (their sit/dash, dig-for-seeds, and play-dead/
	// variant behaviors are DEFERRED brain hooks); Allay is a BRAIN PathfinderMob flyer (its item-fetch/
	// follow-note/duplicate is a DEFERRED brain hook). Cite Camel/Sniffer/Allay/Axolotl.customServerAiStep
	// + the respective *Ai brain (the brain deferral note in each mob file).
	isCamel        bool
	isSniffer      bool
	isAllay        bool
	isAxolotl      bool
	axolotlVariant int
	// camelDashCooldown mirrors Camel.dashCooldown (the int field): set to 55 (DASH_COOLDOWN_TICKS) by
	// executeRidersJump, decremented each tick() while > 0, and gating onPlayerJump (a dash only fires when
	// dashCooldown <= 0). camelDashing mirrors Camel.DASH (the BOOLEAN EntityDataAccessor, default false):
	// set true on the dash burst, cleared in tick() once the camel lands / is no longer a passenger past the
	// DASH_MINIMUM_DURATION window. camelLastPoseChangeTick mirrors Camel.LAST_POSE_CHANGE_TICK (the LONG
	// EntityDataAccessor, default 0): the signed game-time stamp of the last pose change -- NEGATIVE while
	// sitting (isCamelSitting == stamp < 0), and getPoseTime = gameTime - abs(stamp) is the ticks-since-pose.
	// Zero for every non-camel entity. Cite Camel.dashCooldown/DASH/LAST_POSE_CHANGE_TICK.
	camelDashCooldown       int32
	camelDashing            bool
	camelLastPoseChangeTick int64
	// --- SNIFFER dig state machine (net.minecraft.world.entity.animal.sniffer.Sniffer + SnifferAi) ------
	//
	// Tick-owned plain values, set/read ONLY for the Sniffer (snifferAiStep gates on typ == entity.Sniffer.ID).
	// snifferState mirrors Sniffer.DATA_STATE (the Sniffer$State enum id: 0 IDLING / 1 FEELING_HAPPY /
	// 2 SCENTING / 3 SNIFFING / 4 SEARCHING / 5 DIGGING / 6 RISING). snifferStateTimer counts down the
	// current behavior's duration (the brain schedules each behavior for a UniformInt(min,max) tick budget;
	// re-expressed as a per-mob timer since the brain is DEFERRED). snifferDropSeedAtTick mirrors
	// DATA_DROP_SEED_AT_TICK (onDiggingStart sets it to tickCount+120; dropSeed fires when tickCount ==
	// snifferDropSeedAtTick). snifferCooldown mirrors the SnifferAi dig cooldown (SNIFFING_COOLDOWN_TICKS
	// = 9600 ticks between dig cycles). snifferExplored is the SNIFFER_EXPLORED_POSITIONS memory (a
	// capped-at-20, evict-oldest list of packed BlockPos longs so a sniffer never re-digs an explored
	// spot). Zero for every other entity. Cite Sniffer (DATA_STATE / DATA_DROP_SEED_AT_TICK /
	// storeExploredPosition) + SnifferAi (SNIFFING_COOLDOWN_TICKS + the behavior durations).
	snifferState          int
	snifferStateTimer     int
	snifferDropSeedAtTick int
	snifferCooldown       int
	snifferExplored       []int64
	// --- PARROT / BAT (flying passive + ambient, Task) ----------------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for their own type (each *AiStep gates on typ). isParrot/
	// isBat mark the entity. parrotVariant mirrors Parrot's DATA_VARIANT_ID (0 RED_BLUE / 1 BLUE /
	// 2 GREEN / 3 YELLOW_BLUE / 4 GRAY -- the 5 plumage variants; Parrot.Variant.byId). batResting
	// mirrors Bat's DATA_ID_FLAGS bit 0x1 (isResting() == (flags & 1) != 0; setResting flips it) -- a
	// resting bat hangs from a ceiling with zero velocity; a flying bat picks a random target and drifts
	// toward it (Bat.customServerAiStep). batTargetX/Y/Z + batHasTarget mirror Bat.targetPosition (the
	// BlockPos the flying bat drifts toward; null == !batHasTarget). Zero for every other entity.
	// Cite Parrot DATA_VARIANT_ID + Parrot.Variant, Bat.isResting/setResting + Bat.customServerAiStep.
	isParrot                           bool
	isBat                              bool
	parrotVariant                      int32
	parrotPerched                      bool
	batResting                         bool
	batTargetX, batTargetY, batTargetZ int
	batHasTarget                       bool
	// --- HORSE FAMILY (net.minecraft.world.entity.animal.equine.{AbstractHorse,Horse,Donkey,Mule,
	//     AbstractChestedHorse,Llama,TraderLlama}) ------------------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a horse-family member (each read gates on typ or the
	// isHorseFamily flag). isHorse/isDonkey/isMule/isLlama mark the concrete kind (isTraderLlama refines a
	// llama). isHorseFamily is the union flag (any AbstractHorse) so the shared paths (jump launch, taming)
	// gate once. horseTamed mirrors AbstractHorse DATA_ID_FLAGS bit-2 (isTamed/setTamed). horseTemper mirrors
	// AbstractHorse.temper (0..getMaxTemper(); modifyTemper clamps). horseJumpStrength mirrors the
	// JUMP_STRENGTH attribute base value (Sulfur has no JUMP_STRENGTH registered attribute -- the CITED
	// non-gameplay omission; the launch reads this field, which randomizeAttributes/createBase*Attributes
	// seed). horsePlayerJumpPendingScale mirrors AbstractHorse.playerJumpPendingScale (the rider-charge
	// launch multiplier, 0..1). horseHasChest mirrors AbstractChestedHorse DATA_ID_CHEST (donkey/mule/llama
	// only; a chest opens the inventory columns). horseInvColumns mirrors getInventoryColumns() (0 horse; 5
	// chested-with-chest; llama = strength when chested). llamaStrength mirrors Llama DATA_STRENGTH_ID (1..5).
	// Zero for every non-horse entity. The mount packet path + the container GUI are the DEFERRED behavior
	// layer (horse.go). Cite AbstractHorse (temper/isTamed/playerJumpPendingScale) + AbstractChestedHorse
	// (hasChest/getInventoryColumns) + Llama (getStrength).
	isHorse                     bool
	isDonkey                    bool
	isMule                      bool
	isLlama                     bool
	isTraderLlama               bool
	isHorseFamily               bool
	horseTamed                  bool
	horseTemper                 int
	horseJumpStrength           float64
	horsePlayerJumpPendingScale float32
	horseHasChest               bool
	horseInvColumns             int
	llamaStrength               int
	// --- WATER MOBS (Squid/GlowSquid/Cod/Salmon/Pufferfish/TropicalFish/Dolphin/Tadpole) -----------
	//
	// Tick-owned plain values, set/read ONLY for their own type (each *AiStep gates on typ). isWaterMob
	// marks any of the 8 aquatic mobs (it drives the WaterAnimal-style out-of-water air drain in the
	// breath path; a fish/squid/dolphin DROWNS ON LAND, the inversion of a land mob). isSquid/isPufferfish/
	// isDolphin/isTadpole mark the signature-behavior mobs. squidTentacleMovement/squidTentacleSpeed mirror
	// Squid.tentacleMovement/tentacleSpeed (the aiStep tentacle-rotation accumulator + its RNG-set speed).
	// pufferPuffState mirrors Pufferfish PUFF_STATE (0 small / 1 mid / 2 full); pufferInflateCounter/
	// pufferDeflateTimer mirror Pufferfish.inflateCounter/deflateTimer (the tick() puff/deflate timers).
	// dolphinMoistness mirrors Dolphin MOISTNESS_LEVEL (2400 in water; drains -1/tick on land -> dryOut
	// damage at <= 0). tadpoleAge mirrors Tadpole.age (the ++ per tick that grows it into a Frog at
	// ticksToBeFrog 24000). tropicalVariant mirrors TropicalFish DATA_ID_TYPE_VARIANT (the packed
	// pattern+2-color int; DEFAULT_VARIANT = KOB/WHITE/WHITE packs to 0). Zero for every other entity.
	// Cite Squid.aiStep, Pufferfish.tick/PufferfishPuffGoal, Dolphin.tick, Tadpole.aiStep/setAge,
	// TropicalFish.packVariant, WaterAnimal.handleAirSupply.
	isWaterMob            bool
	isSquid               bool
	isPufferfish          bool
	isDolphin             bool
	isTadpole             bool
	squidTentacleMovement float32
	squidTentacleSpeed    float32
	pufferPuffState       int
	pufferInflateCounter  int
	pufferDeflateTimer    int
	dolphinMoistness      int
	tadpoleAge            int
	tropicalVariant       int
	// salmonVariant mirrors Salmon DATA_TYPE (0 SMALL / 1 MEDIUM / 2 LARGE, weighted 30/50/15 at
	// finalizeSpawn); salmonScale mirrors the Salmon$Variant.boundingBoxScale (SMALL 0.5 / MEDIUM 1.0 /
	// LARGE 1.5) folded into the hitbox via getSalmonScale. Zero for every non-salmon. Cite
	// Salmon.finalizeSpawn + Salmon$Variant + Salmon.getSalmonScale.
	salmonVariant int
	salmonScale   float32
	// schoolLeaderID / schoolSize mirror AbstractSchoolingFish.leader / AbstractSchoolingFish.schoolSize
	// (Cod/Salmon/TropicalFish -- Pufferfish is NOT a schooling fish and never sets these). schoolLeaderID
	// is 0 == "no leader" (a leaderless/leader fish); a non-zero id is this follower's leader entity id
	// (isFollower gates on the leader still being alive). schoolSize starts at 1 (the AbstractSchoolingFish
	// ctor seeds schoolSize=1) and counts self+followers on a leader (hasFollowers == schoolSize>1,
	// canBeFollowed == hasFollowers && schoolSize<getMaxSchoolSize). Zero/1 for every non-schooling entity.
	// Cite AbstractSchoolingFish (leader/schoolSize fields + ctor schoolSize=1) + FollowFlockLeaderGoal.
	schoolLeaderID int32
	schoolSize     int
	// --- NAUTILUS FAMILY (Task, NEW 26.2) ----------------------------------------------------------
	// AbstractNautilus is a brain-driven TamableAnimal aquatic mount (Nautilus + the zombified
	// ZombieNautilus). isNautilus gates the (bounded) swim tick; isZombieNautilus marks the undead
	// variant (breath/effect classification). horseTamed is reused for the TamableAnimal tamed flag.
	// The brain/rideable/inventory layers are DEFERRED. Cite AbstractNautilus.
	isNautilus       bool
	isZombieNautilus bool
	// --- MANNEQUIN (Task, NEW 26.2) ---------------------------------------------------------------
	// Mannequin extends Avatar (a player-shaped, no-AI display entity). isMannequin gates its (no-op)
	// tick; mannequinImmovable mirrors DATA_IMMOVABLE (setImmovable) -- a placed mannequin does not get
	// pushed. The ResolvableProfile / description data + skin layers are DEFERRED. Cite Mannequin.
	isMannequin        bool
	mannequinImmovable bool
	// --- PANDA / SNOW_GOLEM (Task) ------------------------------------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for their own type (each *AiStep / spawn gates on typ).
	// isPanda / isSnowGolem mark the entity. pandaMainGene / pandaHiddenGene mirror Panda's
	// DATA_MAIN_GENE_ID / DATA_HIDDEN_GENE_ID (the two Panda.Gene ids 0..6; the OBSERVABLE variant is
	// getVariantFromGenes(main, hidden) -- a recessive main only shows if main==hidden, else NORMAL).
	// The genes are rolled at spawn (finalizeSpawn: getRandom x2) or from parents at breed
	// (setGeneFromParents), then setAttributes() diverges THIS instance (WEAK -> MAX_HEALTH 10, LAZY ->
	// MOVEMENT_SPEED 0.07) via setBaseValue. snowGolemPumpkin mirrors SnowGolem DATA_PUMPKIN_ID (has-
	// pumpkin flag, default true; shearing clears it). Zero/false for every other entity. The roll/sneeze/
	// sit/lie panda cosmetics + the snowball ranged attack are the DEFERRED behavior layer (panda.go /
	// snow_golem.go). Cite Panda.Gene (main/hidden/getVariantFromGenes) + Panda.setAttributes, SnowGolem
	// DATA_PUMPKIN_ID.
	isPanda          bool
	isSnowGolem      bool
	pandaMainGene    int
	pandaHiddenGene  int
	snowGolemPumpkin bool
	// --- PIGLIN (net.minecraft.world.entity.monster.piglin.Piglin) -------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Piglin (piglinBrainTick gates on typ == entity.Piglin.ID).
	// isPiglin marks the entity. piglinTimeInOverworld mirrors AbstractPiglin.timeInOverworld (the off-nether
	// zombification counter: ++ while isConverting(), reset to 0 in the nether; at > CONVERSION_TIME(300) the
	// piglin convertTo's ZOMBIFIED_PIGLIN). piglinImmuneToZombification mirrors DATA_IMMUNE_TO_ZOMBIFICATION
	// (a piglin set immune never converts; default false). piglinAttackTime mirrors MeleeAttackGoal's
	// ticksUntilNextAttack (the RNG-free swing cooldown, reset to adjustedTickDelay(20) after a hit). Zero for
	// every non-piglin entity. The DATA_IS_CHARGING_CROSSBOW / DATA_IS_DANCING client-metadata flags are
	// cite-deferred (the crossbow ranged attack + the celebrate dance are in the deferred activity graph).
	// Cite Piglin / AbstractPiglin (timeInOverworld / isImmuneToZombification) + MeleeAttackGoal.
	isPiglin                    bool
	piglinTimeInOverworld       int
	piglinImmuneToZombification bool
	piglinInNether              bool // the nether-dimension guard for isConverting (default false == off-nether == converting)
	piglinAttackTime            int
	// piglinAdmiringDisabled mirrors the ADMIRING_DISABLED memory (a boolean-with-expiry): while > 0 the piglin
	// cannot start admiring / bartering. wasHurtBy sets it to 400 on a player hit; the barter's mobInteract is
	// gated on it (canAdmire: !isAdmiringDisabled). Ticks down each brain tick. Cite PiglinAi.wasHurtBy (400l) +
	// canAdmire. piglinAvoidTicks mirrors the AVOID_TARGET memory (a baby AVOID for 100t on hit): while > 0 the
	// piglin flees the avoid target instead of hunting. piglinAvoidTargetID is that avoid target's entity id.
	// piglinOffhandItem mirrors the offhand-held admire item (holdInOffhand); dropped on hit (stopHoldingOffHandItem).
	// piglinAdmireTicks mirrors the ADMIRE_DURATION (119) admire-hold on a picked-up gold item. piglinIsCrossbow
	// marks a crossbow-armed piglin (createSpawnWeapon nextFloat<0.5); piglinCrossbowCharge mirrors the charge.
	// Zero for every non-piglin. Cite PiglinAi + Piglin.createSpawnWeapon + CrossbowAttack.
	piglinAdmiringDisabled int
	piglinAvoidTicks       int
	piglinAvoidTargetID    int32
	piglinAdmireTicks      int
	piglinIsCrossbow       bool
	piglinCrossbowCharge   int // the CrossbowAttack CHARGING getTicksUsingItem() analogue (0..chargeDuration 25)
	// piglinCrossbowState mirrors CrossbowAttack.CrossbowState (0 UNCHARGED / 1 CHARGING / 2 CHARGED / 3
	// READY_TO_ATTACK); piglinCrossbowAttackDelay is the CHARGED countdown (20 + nextInt(20)) before firing.
	// Cite CrossbowAttack.crossbowAttack state machine.
	piglinCrossbowState       int
	piglinCrossbowAttackDelay int
	// piglinAngeredAt mirrors the ANGRY_AT memory (an entity-id, 600t via setAngerTarget): the retaliation
	// target set by wasHurtBy. Unlike the sensor's NEAREST_TARGETABLE_PLAYER_NOT_WEARING_GOLD memory, ANGRY_AT
	// ignores gold armor -- an angered piglin fights a gold-armored player. The FIGHT StartHunting behavior
	// reads ANGRY_AT, so a live ANGRY_AT overrides the gold-neutrality drop. Cite PiglinAi.setAngerTarget +
	// StartHuntingBehavior (ANGRY_AT). piglinAngerEnd is the game-tick the ANGRY_AT expires (600t).
	piglinAngeredAt int32
	piglinAngerEnd  int64
	// piglinOffhandItem mirrors the offhand ItemStack a piglin holds while admiring a picked-up gold item
	// (holdInOffhand). Dropped on hit (stopHoldingOffHandItem, false) or bartered (stopHoldingOffHandItem, true).
	// Empty for every non-holding piglin. Cite PiglinAi.holdInOffhand / stopHoldingOffHandItem.
	piglinOffhandItem component.SlotData
	// --- ZOMBIFIED PIGLIN (net.minecraft.world.entity.monster.zombie.ZombifiedPiglin) ----------------
	//
	// Tick-owned plain values, set/read ONLY for a ZombifiedPiglin (zombifiedPiglinAiStep gates on typ ==
	// entity.ZombifiedPiglin.ID). isZombifiedPiglin marks the entity. The persistent-anger state reuses the
	// shared NeutralMob fields (angerEndTime / angerTarget, below) the wolf/iron_golem already carry -- a
	// zombified piglin is a NeutralMob too, with the IDENTICAL PERSISTENT_ANGER_TIME UniformInt(400,780).
	// zombifiedPiglinAlertCooldown mirrors ZombifiedPiglin.ticksUntilNextAlert (the maybeAlertOthers
	// throttle, reset to ALERT_INTERVAL.sample = 80 + nextInt(41) on a set target / after an alert pass);
	// the anger-pack spread (alertOthers) fires when it hits 0. Zero for every non-zombified-piglin entity.
	// Cite ZombifiedPiglin (ticksUntilNextAlert / ALERT_INTERVAL) + NeutralMob (PERSISTENT_ANGER_TIME).
	isZombifiedPiglin            bool
	zombifiedPiglinAlertCooldown int
	// zombifiedPiglinFirstAngerSound mirrors ZombifiedPiglin.playFirstAngerSoundIn: on a FRESH target,
	// setTarget seeds it from FIRST_ANGER_SOUND_DELAY.sample = 0 + nextInt(21) (rangeOfSeconds(0,1) ->
	// UniformInt(0,20)). It drives the delayed first-anger sound (client cue, deferred) but the RNG DRAW
	// is observable via draw order -- it precedes the ALERT_INTERVAL draw in setTarget. Cite
	// ZombifiedPiglin.setTarget offsets 11-24 + FIRST_ANGER_SOUND_DELAY.
	zombifiedPiglinFirstAngerSound int
	// --- ILLUSIONER (net.minecraft.world.entity.monster.illager.Illusioner) --------------------------
	//
	// Tick-owned plain values, set/read ONLY for an Illusioner (illusionerAiStep gates on typ ==
	// entity.Illusioner.ID). illusionerBlindnessCooldown mirrors the IllusionerBlindnessSpellGoal cast
	// cadence (getCastingInterval 180): decremented per tick while a target is in range, and on <=0 the
	// spell casts (BLINDNESS 400 on the target) and resets. illusionerCastTicks mirrors the spell WARMUP
	// (SpellcasterUseSpellGoal spellWarmup 20): while > 0 the illusioner is casting -> setInvisible(true).
	// Zero for every non-illusioner entity. Cite Illusioner$IllusionerBlindnessSpellGoal (getCastingInterval
	// 180, spellWarmup 20, performSpellCasting BLINDNESS 400) + Illusioner.aiStep (invisible while casting).
	illusionerBlindnessCooldown int
	illusionerCastTicks         int
	// brain is the ported net.minecraft.world.entity.ai.Brain (brain.go). It is NON-NIL only for a mob
	// that runs the behavior subsystem — currently the BABY HappyGhast (HappyGhast.customServerAiStep
	// ticks the brain ONLY when isBaby()); every other entity leaves it nil (a nil brain is never ticked,
	// so the classic-goal mobs are wholly unaffected). Attached at spawn by attachHappyGhastBrain (happy
	// ghast baby) or attachVillagerBrain (villager). Tick-owned (TICK-05).
	brain *brain
	// --- VILLAGER STATE (net.minecraft.world.entity.npc.villager.Villager + VillagerData) ------------
	//
	// Tick-owned plain values, set/read ONLY for a Villager (typ == entity.Villager.ID). villagerProfession
	// + villagerLevel + villagerType mirror VillagerData(type, profession, level) (VillagerData is a record;
	// Villager holds it in the DATA_VILLAGER_DATA entity-data accessor). villagerLevel is clamped MIN=1..
	// MAX=5 (VillagerData ctor Math.max(1,level); MAX_VILLAGER_LEVEL=5). villagerJobSite* + villagerHasJobSite
	// mirror the JOB_SITE memory module (a GlobalPos): the claimed job-site POI position. Zero for every
	// non-villager entity. Cite VillagerData + Villager.getVillagerData/setVillagerData.
	villagerType       string // VillagerData.type() path (biome variant; "plains" default) — cosmetic
	villagerProfession string // VillagerData.profession() path ("none" until AcquirePoi assigns one)
	villagerLevel      int    // VillagerData.level() (1..5)
	villagerJobSiteX   int
	villagerJobSiteY   int
	villagerJobSiteZ   int
	villagerHasJobSite bool // the JOB_SITE memory is present (a job site is claimed)
	// --- VILLAGER TRADING STATE (net.minecraft.world.entity.npc.villager.AbstractVillager + Villager) ---
	//
	// Tick-owned, set/read ONLY for a Villager (typ == entity.Villager.ID). offers mirrors
	// AbstractVillager.offers (the lazily-built MerchantOffers — nil until getOffers() first builds it via
	// updateTrades). offersBuilt is the "offers != null" gate (a Go nil slice is a valid EMPTY offers, so a
	// separate bool distinguishes "not yet built" from "built empty"). villagerTradingPlayer mirrors
	// AbstractVillager.tradingPlayer, reduced to the trading player's THIN entity id (0 == none, the
	// isTrading() == tradingPlayer != null gate). villagerXp mirrors Villager.villagerXp (accumulates
	// offer.getXp() on each trade; the level-up read is deferred). Zero for every non-villager.
	//	[VERIFIED CFR AbstractVillager.getOffers (offers==null -> new MerchantOffers + updateTrades);
	//	 setTradingPlayer/isTrading (tradingPlayer field); Villager.rewardTradeXp (villagerXp += offer.getXp()).]
	offers                merchantOffers
	offersBuilt           bool
	villagerTradingPlayer int32
	villagerXp            int
	// updateMerchantTimer + increaseProfessionLevelOnUpdate mirror Villager.updateMerchantTimer /
	// increaseProfessionLevelOnUpdate: rewardTradeXp arms them (timer=40, flag=true) when a trade pushes
	// villagerXp past the level threshold; customServerAiStep counts the timer down while !isTrading() and,
	// on reaching 0, runs increaseMerchantCareer (level+1, add next-level trades) + a REGENERATION 200t buff.
	//	[VERIFIED CFR Villager fields updateMerchantTimer:I / increaseProfessionLevelOnUpdate:Z +
	//	 customServerAiStep countdown block + rewardTradeXp arm block.]
	updateMerchantTimer             int
	increaseProfessionLevelOnUpdate bool
	// lastRestockGameTime + numberOfRestocksToday + lastRestockCheckDay mirror Villager's restock-scheduling
	// fields: the twice-a-day workstation restock cadence (Villager.shouldRestock/allowedToRestock/restock).
	// lastRestockGameTime is the gameTime of the last restock; numberOfRestocksToday counts restocks in the
	// current day (reset at the day boundary, cap 2); lastRestockCheckDay is the last day index shouldRestock
	// saw (0 == never checked). All zero for a non-villager.
	//	[VERIFIED CFR Villager fields lastRestockGameTime:J / numberOfRestocksToday:I / lastRestockCheckDay:J
	//	 + shouldRestock/allowedToRestock/restock/resetNumberOfRestocks.]
	lastRestockGameTime   int64
	numberOfRestocksToday int
	lastRestockCheckDay   int64
	// villagerGossips mirrors Villager.gossips (net.minecraft.world.entity.ai.gossip.GossipContainer): the
	// per-UUID reputation store that drives the trade-price economy (getPlayerReputation ->
	// updateSpecialPrices) and receives reputation events (onReputationEventFrom: TRADE/VILLAGER_HURT/
	// VILLAGER_KILLED/ZOMBIE_VILLAGER_CURED). Lazily created (villagerEnsureGossips) so non-villager
	// entities carry no map. lastTradedPlayerUUID mirrors Villager.lastTradedPlayer (set in rewardTradeXp,
	// consumed by the customServerAiStep TRADE-event fire) — the zero UUID means "none".
	//	[VERIFIED CFR Villager.gossips (GossipContainer field) / getPlayerReputation / onReputationEventFrom;
	//	 Villager.rewardTradeXp (lastTradedPlayer = getTradingPlayer()) + customServerAiStep (fires TRADE).]
	villagerGossips      *gossipContainer
	lastTradedPlayerUUID uuid.UUID
	// villagerFoodLevel mirrors Villager.foodLevel (the private `foodLevel:I` field, ctor default 0). It
	// gates breeding: Villager.canBreed() == (foodLevel + countFoodPointsInInventory()) >= 12 && !isSleeping
	// && getAge()==0. eatAndDigestFood() (VillagerMakeLove.tick, on birth) runs eatUntilFull() then
	// digestFood(12) == foodLevel -= 12. countFoodPointsInInventory reads the villager SimpleContainer, which
	// is not built yet, so it is a cited stub == 0 (villagerCountFoodPointsInInventory) — an un-fed villager's
	// canBreed reduces to foodLevel >= 12, exactly matching a vanilla villager that has never picked up food.
	// Zero for every non-villager (the field is villager-gated at every read/write).
	//	[VERIFIED CFR Villager: foodLevel:I (ctor 0); canBreed (foodLevel+countFoodPointsInInventory()>=12
	//	 && !isSleeping() && getAge()==0); eatAndDigestFood (eatUntilFull(); digestFood(12)); digestFood(int
	//	 n): foodLevel -= n.]
	villagerFoodLevel int
	// lastGossipDecayTime mirrors Villager.lastGossipDecayTime (the `lastGossipDecayTime:J` field, ctor 0):
	// the gameTime of the last GossipContainer.decay(). Villager.tick() -> maybeDecayGossip() decays the
	// gossips once the 24000-tick (one-day) window elapses. Zero for every non-villager.
	//	[VERIFIED CFR Villager: lastGossipDecayTime:J (ctor 0); maybeDecayGossip (if ==0 seed=gameTime,return;
	//	 if gameTime < lastGossipDecayTime+24000 return; gossips.decay(); lastGossipDecayTime=gameTime).]
	lastGossipDecayTime int64
	// --- WANDERING TRADER STATE (net.minecraft.world.entity.npc.wanderingtrader.WanderingTrader) -----
	//
	// Tick-owned, set/read ONLY for a WanderingTrader (typ == entity.WanderingTrader.ID). isWanderingTrader
	// gates the WT-specific offer build (villagerGetOffers branch), the WT mobInteract, and the aiStep
	// maybeDespawn -- the WT is an AbstractVillager, so it REUSES the shared offers/offersBuilt/
	// villagerTradingPlayer/villagerXp fields above (getOffers/setTradingPlayer/rewardTradeXp live on
	// AbstractVillager). despawnDelay mirrors WanderingTrader.despawnDelay: a countdown the spawner seeds
	// (setDespawnDelay(48000)); maybeDespawn decrements it each server tick while !isTrading() and discards
	// the trader at 0. The ctor sets it to DEFAULT_DESPAWN_DELAY==0 (a trader spawned outside the spawner
	// never auto-despawns until a delay is set). wanderTarget (WanderToPositionGoal destination) is the
	// DEFERRED autonomous-goal seam (no field yet -- the goal is cite-deferred). Zero for every non-WT.
	//	[VERIFIED CFR WanderingTrader: ctor despawnDelay=0; maybeDespawn (despawnDelay>0 && !isTrading() &&
	//	 --despawnDelay==0 -> discard()); WanderingTraderSpawner.spawn setDespawnDelay(48000).]
	isWanderingTrader bool
	despawnDelay      int
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

	// skills is the per-mob declared-skill runner (SKILLS-01, mob_skills.go): the per-mob timers +
	// reentrancy guard over the SHARED immutable []skillDecl the declaration captured. nil for every
	// mob whose declaration carries no skills (every vanilla mob — the pig oracle pays one nil-check
	// in the tick loop and nothing else). Attached in spawnDeclaredMob; tick-owned (TICK-05); like ai
	// it is a pointer to mutable tick-owned state outside the tracker's snapshot value-set.
	skills *skillRunner

	// model is the per-mob native-model instance (MODEL-M2, plugin_model_decl.go): the sibling of
	// skills. It holds the rig's live bone item_display entity ids (the base's passengers) + the per-bone
	// server AABBs (G.1) + the reserved M3 animator slot. nil for every mob whose declaration carries no
	// model (every vanilla mob — the pig oracle pays one nil-check in the tick loop and nothing else; a
	// nil model = zero new code path, zero RNG, byte-identical wire). Attached in spawnDeclaredMob
	// BEFORE the spawn trigger; tick-owned (TICK-05), a pointer to mutable tick-owned state outside the
	// tracker's snapshot value-set, exactly like skills / ai.
	model *modelInstance

	// --- RAVAGER state (net.minecraft.world.entity.monster.Ravager) --------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Ravager (ravagerAiStep gates on typ ==
	// entity.Ravager.ID). ravagerAttackTick mirrors Ravager.attackTick (ATTACK_DURATION 10, set by
	// doHurtTarget, counted down in aiStep); ravagerStunnedTick mirrors Ravager.stunnedTick (STUN_DURATION
	// 40, set on a blocked hit, drives isImmobile + the roar re-arm at 0); ravagerRoarTick mirrors
	// Ravager.roarTick (set to 20 when the stun ends, fires roar() at 10). All three gate isImmobile()
	// (Ravager.isImmobile). Zero for every non-ravager entity.
	//	[VERIFIED CFR Ravager: ATTACK_DURATION=10, STUN_DURATION=40; aiStep countdowns; roarTick=20 on
	//	 stun-end -> roar() at roarTick==10; attackTick=10 in doHurtTarget.]
	ravagerAttackTick  int32
	ravagerStunnedTick int32
	ravagerRoarTick    int32

	// --- IRON GOLEM state (net.minecraft.world.entity.animal.golem.IronGolem) ----------------------
	//
	// Tick-owned plain values, set/read ONLY for an IronGolem (ironGolemAiStep + the doHurtTarget override
	// gate on typ == entity.IronGolem.ID). ironGolemAttackAnimationTick mirrors IronGolem.attackAnimationTick
	// (set to 10 in doHurtTarget + on handleEntityEvent(4), counted down in aiStep — the swing animation);
	// ironGolemOfferFlowerTick mirrors IronGolem.offerFlowerTick (OFFER_TICKS 400, counted down in aiStep —
	// the poppy-offer animation, cite-deferred goal); ironGolemPlayerCreated mirrors the DATA_FLAGS_ID bit
	// 0x01 (isPlayerCreated — a player-built golem never hunts players; no construction path in v1 so it stays
	// false for village/dbg golems). Zero/false for every non-golem entity.
	//	[VERIFIED CFR IronGolem: attackAnimationTick=10 in doHurtTarget/handleEntityEvent(4); offerFlowerTick
	//	 OFFER_TICKS=400; isPlayerCreated (DATA_FLAGS_ID & 1); aiStep decrements both ticks.]
	ironGolemAttackAnimationTick int32
	ironGolemOfferFlowerTick     int32
	ironGolemPlayerCreated       bool

	// --- EVOKER / SpellcasterIllager state (net.minecraft.world.entity.monster.illager.Evoker) ------
	//
	// Tick-owned plain values, set/read ONLY for an Evoker (the evoker spell goals + evokerAiStep gate on
	// typ == entity.Evoker.ID). spellCastingTickCount mirrors SpellcasterIllager.spellCastingTickCount
	// (set by a UseSpellGoal.start to the spell's castingTime, decremented in customServerAiStep; drives
	// the prio-1 EvokerCastingSpellGoal.canUse gate). currentSpell mirrors DATA_SPELL_CASTING_ID (0=NONE,
	// 1=SUMMON_VEX, 2=FANGS, 3=WOLOLO); isCastingSpell == currentSpell>0. evokerWololoTarget is the BLUE
	// sheep the WOLOLO goal picked (a thin entity id; 0 == none). Zero for every non-evoker.
	//	[VERIFIED CFR SpellcasterIllager: spellCastingTickCount, DATA_SPELL_CASTING_ID byte; customServer
	//	 AiStep decrements spellCastingTickCount; IllagerSpell ids NONE=0/SUMMON_VEX=1/FANGS=2/WOLOLO=3.]
	spellCastingTickCount int32
	currentSpell          int32
	evokerWololoTarget    int32

	// --- VEX state (net.minecraft.world.entity.monster.Vex) ----------------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a Vex (the code-spawned Vex tick gates on isVex). A Vex
	// is a flying Monster the evoker's SUMMON_VEX spell spawns (setOwner/setBoundOrigin/setLimitedLife).
	// It flies (VexMoveControl), charges its target (VexChargeAttackGoal), wanders (VexRandomMoveGoal),
	// copies the evoker's target (VexCopyOwnerTargetGoal), and STARVES to death when its limited life
	// runs out (Vex.tick: hurt(starve, 1.0) every 20 ticks once hasLimitedLife && --limitedLifeTicks<=0).
	//	[VERIFIED CFR Vex + Vex$VexMoveControl/VexChargeAttackGoal/VexRandomMoveGoal/VexCopyOwnerTargetGoal.]

	// isVex marks this entity as a Vex. The Vex tick (flight + charge + random-move + copy-owner-target +
	// limited-life starve) runs ONLY for entities with this set. Set at spawn by spawnVex.
	isVex bool

	// vexOwnerID is Vex.owner modeled as the summoning evoker's entity id (NEVER a live pointer -- the
	// Folia/snapshot rule). setOwner(evoker) records it; VexCopyOwnerTargetGoal reads the owner's target.
	vexOwnerID int32

	// vexBoundOrigin{X,Y,Z} + vexHasBoundOrigin mirror Vex.boundOrigin (a BlockPos, nullable). The
	// SUMMON_VEX spell sets it to the spawn cell; VexRandomMoveGoal wanders within +/-(7,5,7) of it (or the
	// vex's current block position when unset). Integer block coords (the vanilla BlockPos).
	vexBoundOriginX, vexBoundOriginY, vexBoundOriginZ int
	vexHasBoundOrigin                                 bool

	// vexHasLimitedLife + vexLimitedLifeTicks mirror Vex.hasLimitedLife / Vex.limitedLifeTicks. setLimited
	// Life(20*(30+nextInt(90))) arms the countdown; Vex.tick decrements it and, at <=0, resets it to 20 and
	// deals 1.0 starve damage (so a limited-life vex slowly starves out). Zero/false for a non-summoned vex.
	vexHasLimitedLife   bool
	vexLimitedLifeTicks int32

	// vexCharging mirrors Vex FLAG_IS_CHARGING (DATA_FLAGS_ID bit 1) -- set while VexChargeAttackGoal is
	// mid-charge (drives the client charge visual; server-side it gates canContinueToUse). A plain bool.
	vexCharging bool

	// vexWantX/Y/Z + vexHasWant mirror the VexMoveControl target (MoveControl.wantedX/Y/Z + Operation.
	// MOVE_TO). The charge/random-move goals set them via setWantedPosition; the move-control tick flies
	// the vex toward them and clears vexHasWant on arrival (deltaLength < boundingBox.getSize()). vexWant
	// Speed is the MoveControl.speedModifier the goal requested (1.0 charge, 0.25 random-move).
	vexWantX, vexWantY, vexWantZ float64
	vexHasWant                   bool
	vexWantSpeed                 float64

	// --- EVOKER FANGS state (net.minecraft.world.entity.projectile.EvokerFangs) --------------------
	//
	// Tick-owned plain values, set/read ONLY for an EvokerFangs (the code-spawned fangs tick gates on
	// isFangs). EvokerFangs is a NON-mob projectile the evoker's FANGS spell spawns at a sturdy floor. It
	// counts down warmupDelayTicks; at warmupDelayTicks==-8 it deals 6.0 magic damage to every LivingEntity
	// in its inflated (0.2,0,0.2) box (dealDamageTo), broadcasts the spike event ONCE, then counts down
	// lifeTicks (default 22) and discards at <0.
	//	[VERIFIED CFR EvokerFangs.tick: --warmupDelayTicks<0 { at -8 dealDamageTo(box.inflate(0.2,0,0.2));
	//	 broadcast spike once; --lifeTicks<0 discard }. ATTACK_TRIGGER_TICKS=14, LIFE_OFFSET=2, lifeTicks=22.]

	// isFangs marks this entity as an EvokerFangs. The fangs tick (warmup countdown -> attack -> despawn)
	// runs ONLY for entities with this set. Set at spawn by spawnEvokerFangs.
	isFangs bool

	// fangsOwnerID is EvokerFangs.owner modeled as the casting evoker's entity id (NEVER a live pointer --
	// the Folia/snapshot rule). setOwner records it; dealDamageTo skips the owner and attributes the hit.
	fangsOwnerID int32

	// fangsWarmupDelayTicks mirrors EvokerFangs.warmupDelayTicks (the ctor arg -- 0 for the arc's first ring,
	// the per-fang stagger for the ARC/LINE). The tick decrements it below zero; the attack fires at -8.
	fangsWarmupDelayTicks int32

	// fangsLifeTicks mirrors EvokerFangs.lifeTicks (default 22). Once warmup drops below 0 the tick counts
	// it down each tick; the fangs discards when it goes below 0 (the ~22-tick lifetime).
	fangsLifeTicks int32

	// fangsSentSpikeEvent mirrors EvokerFangs.sentSpikeEvent -- the broadcastEntityEvent(4) latch so the
	// bite animation event is emitted exactly once (the first tick warmup drops below 0).
	fangsSentSpikeEvent bool

	// --- LIGHTNING BOLT (net.minecraft.world.entity.LightningBolt) ---------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a LightningBolt (isBolt). The bolt is a code-spawned
	// visual+damage entity (the sibling of EvokerFangs above) the thunder strike (lightning.go
	// ServerLevel.tickThunder) spawns. Its whole behavior is the life/flashes lifecycle: at life==2 it
	// spawns ground fire, then damages entities in a 3.0 radius each tick it is alive, then re-flashes
	// flashes-1 more times before discarding. Zero for every non-bolt entity (the bolt tick gates on
	// isBolt).
	//	[VERIFIED CFR LightningBolt: START_LIFE=2, life=2, flashes=nextInt(3)+1, visualOnly; tick()
	//	 life==2 -> spawnFire(4)+powerLightningRod+clearCopper+gameEvent; --life; life<0 && flashes==0 ->
	//	 discard; life<0 && life<-nextInt(10) -> --flashes,life=1,seed=nextLong(),spawnFire(0); life>=0 &&
	//	 !visualOnly -> thunderHit every LivingEntity in AABB(±3,-3..+6+3,±3).]

	// isBolt marks this entity as a LightningBolt. The bolt tick (life/flashes lifecycle -> fire +
	// damage -> discard) runs ONLY for entities with this set. Set at spawn by spawnLightningBolt.
	isBolt bool

	// boltLife mirrors LightningBolt.life (START_LIFE=2). The tick spawns the flash at life==2, then
	// counts it down each tick; at life<0 it discards (flashes==0) or re-flashes (life<-nextInt(10)).
	boltLife int32

	// boltFlashes mirrors LightningBolt.flashes (ctor: nextInt(3)+1 — 1..3 additional strike flashes).
	// Each re-flash decrements it and resets life to 1; the bolt discards once flashes hits 0 and life
	// drops below 0.
	boltFlashes int32

	// boltVisualOnly mirrors LightningBolt.visualOnly. A visual-only bolt (the skeleton-trap spawn, or a
	// summoned cosmetic bolt) renders + plays sound but deals NO entity damage and starts NO fire.
	boltVisualOnly bool

	// boltBlocksSetOnFire mirrors LightningBolt.blocksSetOnFire — a diagnostic counter incremented per
	// fire block placed by spawnFire (advancement-side in vanilla; kept for fidelity, unread in v1).
	boltBlocksSetOnFire int32

	// boltHitEntities mirrors LightningBolt.hitEntities — the set of entity ids already struck this
	// bolt's lifetime, so the same entity is not double-hit across the multi-tick strike window. Modeled
	// as a set of THIN entity ids (never live *Entity pointers — the Folia/snapshot rule). Lazily created
	// on first hit; nil for a non-bolt entity.
	boltHitEntities map[int32]bool

	// --- PASSENGER / VEHICLE (net.minecraft.world.entity.Entity ride subsystem) --------------------
	//
	// The ride-subsystem state, mirroring net.minecraft.world.entity.Entity's `passengers`
	// (ImmutableList<Entity>) and `vehicle` (Entity) fields, plus `boardingCooldown`. Per the
	// Folia/snapshot rule (like arrowShooterID / vexOwnerID above), the live *Entity references are
	// reduced to THIN entity ids: passengers is the ordered list of passenger ids (index 0 == the
	// first/controlling passenger — Entity.getFirstPassenger), vehicle is the ridden entity's id
	// (0 == not riding). ejectPassengers / startRiding / stopRiding mutate them exactly in the vanilla
	// order (see passenger.go). Tick-owned plain values (TICK-05); the empty-list / zero-vehicle
	// default is the un-ridden state EVERY non-ridden entity (the oracle pig) carries — a nil slice
	// and zero int mutate/read nothing, so the pig oracle's RNG stream is byte-identically unperturbed.
	//
	//	[VERIFIED CFR Entity: passengers (ImmutableList<Entity>), vehicle (Entity, @Nullable),
	//	 boardingCooldown (int); addPassenger prepends a Player ahead of a non-Player first passenger;
	//	 removePassenger sets passenger.boardingCooldown = 60.]
	passengers []int32
	// vehicle is Entity.vehicle reduced to the ridden entity's id (0 == not a passenger).
	vehicle int32
	// boardingCooldown is Entity.boardingCooldown — set to 60 by removePassenger; canRide gates on
	// boardingCooldown <= 0 (a just-dismounted entity cannot immediately re-mount). Decremented each
	// tick in the ride tick. 0 for a never-ridden entity.
	boardingCooldown int32

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

	// attrDirty is the set of ATTRIBUTE registry names whose value changed since the last sync flush —
	// the Go analogue of AttributeMap.attributesToSync for a mob. applyEntityEffectModifiers /
	// removeEntityEffectModifiers mark the touched attribute; the per-tick flush (effect_sync.go
	// flushEntityAttributes) drains it into ONE ClientboundUpdateAttributesPacket broadcast to the mob's
	// trackers and clears it, exactly as ServerEntity.sendChanges drains getAttributesToSync once per
	// tick. Nil until the first modifier change (a modifier-free mob, incl. the pig oracle, marks nothing
	// dirty and emits ZERO attribute packets). Tick-owned (TICK-05).
	attrDirty map[string]bool

	// trackSpawnAttrs is a SNAPSHOT-ONLY field: snapshotEntity fills it (on the tick owner, reading the
	// live AttributeMap) with the mob's currently-modified attributes so the off-tick tracker worker can
	// emit a ClientboundUpdateAttributesPacket on spawn (ServerEntity.sendPairingData's syncable-attribute
	// send) WITHOUT touching the live *attribute.Map off-tick (Pitfall 3). It is nil on a live Entity and
	// nil in the snapshot for a mob with no modified attributes (the pig oracle spawns with none -> zero
	// extra packets).
	trackSpawnAttrs []attrSnapshot

	// leftHanded is Mob.isLeftHanded() — the 5% left-handed roll Mob.finalizeSpawn performs
	// (nextFloat() < 0.05F). It is set at spawn by drainStructureSpawns via attribute.FinalizeSpawn.
	// CITED: there is no left-handed SynchedEntityData (entity metadata) wire-out yet, so this flag is
	// recorded but not yet broadcast to the client (it controls which hand the mob attacks/holds with
	// — a visual the metadata subsystem surfaces). Tick-owned (TICK-05).
	leftHanded bool

	// --- MOB-AGGRESSIVE (Mob.setAggressive — the raise-arm/follow-through client visual) ----------
	//
	// aggressive mirrors Mob.isAggressive()/setAggressive(boolean): toggles DATA_MOB_FLAGS_ID bit
	// 0x04 (the raise-arm bit the ZombieAttackGoal + MeleeAttackGoal.start drive). Tick-owned.
	//	[VERIFIED javap Mob.setAggressive: read-modify-write bit 0x04 of DATA_MOB_FLAGS_ID.]
	//
	// NOTE for the audit-flagged MOB-HOST-08 carry task: this `aggressive bool` lives on the
	// SHARED Mob DATA_MOB_FLAGS_ID byte alongside the carry MAINHAND SYNTHETIC surface; the two
	// are independent metadata channels (carry uses a separate Optional<BlockState> accessor).
	// A non-enderman (the pig oracle) carries neither.
	aggressive bool

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

	// absorptionAmount is LivingEntity.absorptionAmount: temporary absorption hearts folded before health.
	// It stays zero unless an ABSORPTION mob effect raises MAX_ABSORPTION and fills it.
	absorptionAmount float32

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
	// remainingFireTicks is Entity.remainingFireTicks: the burn countdown. igniteForSeconds(n) sets it
	// to floor(n*20) (only if larger); baseTick decrements it, deals 1 fire damage every 20 ticks, and
	// broadcasts the on-fire shared-flag (DATA_SHARED_FLAGS bit 0x01). 0 = not on fire. Tick-owned.
	//	[VERIFIED javap Entity.remainingFireTicks / igniteForTicks / baseTick fire block.]
	remainingFireTicks int32

	// ticksFrozen is Entity.DATA_TICKS_FROZEN (an INT SynchedEntityData accessor, index 7). It is the
	// powder-snow frost accumulation counter read/written by Entity.getTicksFrozen/setTicksFrozen: it
	// climbs by 1 each tick a can-freeze entity is inside powder snow (InsideBlockEffectType.FREEZE
	// lambda: setTicksFrozen(min(getTicksRequiredToFreeze(), getTicksFrozen()+1))) and decays by 2 each
	// tick it is not (LivingEntity.aiStep: setTicksFrozen(max(0, getTicksFrozen()-2))). At >=140
	// (getTicksRequiredToFreeze) the entity isFullyFrozen and takes FREEZE damage every 40 ticks. The
	// client reads DATA_TICKS_FROZEN to draw the freeze/frost vignette (getPercentFrozen). 0 = not
	// frosted. Tick-owned (mutated only on the owning region goroutine).
	//	[VERIFIED javap Entity.getTicksFrozen/setTicksFrozen (DATA_TICKS_FROZEN, index 7, INT) +
	//	 Entity.getTicksRequiredToFreeze (sipush 140) + LivingEntity.aiStep freeze block.]
	ticksFrozen int32

	// fireImmune mirrors EntityType.fireImmune() / Entity.fireImmune(): fire-immune entity types clear
	// ordinary fire ticks and ignore lava/fire ignition. Default false for ordinary mobs (Skeleton,
	// Zombie, Pig, Chicken); NewEntity seeds true for the registered nether fire-immune types.
	//	[VERIFIED javap Entity.fireImmune delegates to EntityType.fireImmune().]
	fireImmune bool

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

	// airSupply is net.minecraft.world.entity.Entity's DATA_AIR_SUPPLY_ID (getAirSupply()/setAirSupply()) —
	// the mob-side twin of tickPlayer.airSupply (breath.go). The Entity ctor seeds DATA_AIR_SUPPLY_ID to
	// getMaxAirSupply() == 300 (Entity.getMaxAirSupply: `sipush 300; ireturn`), so NewEntity initializes it
	// to maxAirSupply — a mob is born with a full bubble bar. LivingEntity.baseTick drains it while the eyes
	// are submerged (decreaseAirSupply == air-1 with OXYGEN_BONUS 0) and refills it out of water
	// (increaseAirSupply == min(air+4, 300)); at air <= -20 (shouldTakeDrowningDamage) the mob resets it to
	// 0 and takes 2.0 DROWN damage. A plain int32 (snapshot-friendly), tick-owned (TICK-05): mutated only on
	// the tick goroutine in tickMobBreath (breath_mob.go). Non-living entities (items/orbs/arrows) keep it at
	// the 300 seed and never read it — the breath tick self-gates on mobRunsBaseTickEnv.
	airSupply int32

	// lastAirSent is the last airSupply value pushed to observers via SetEntityData — the mob-side twin of
	// tickPlayer.lastAirSent. syncMobAirSupply only broadcasts a CHANGED value (vanilla SynchedEntityData
	// dirty-only semantics), so a mob at a steady bubble level (full out of water, the common case) sends
	// nothing. Initialized to maxAirSupply alongside airSupply so a fresh full-air mob is already "sent"
	// (no spurious first-tick packet). Tick-owned (TICK-05).
	lastAirSent int32

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

	// sheepColor is the host-side mirror of net.minecraft.world.entity.animal.sheep.Sheep's DATA_WOOL
	// low nibble (getColor() == DyeColor.byId(DATA_WOOL & 0xF); setColor(c) -> DATA_WOOL = (cur & 0xF0) |
	// (c.getId() & 0xF)). It is the sheep's wool DyeColor id (WHITE 0 .. BLACK 15). Set at spawn by
	// finalizeSpawn (getRandomSheepColor, drawn off the LEVEL RandomSource -- NOT the mob stream), flipped
	// by the dye interact (DyeItem.interactLivingEntity -> setColor) and the evoker WOLOLO (setColor(RED)).
	// The wire DATA_WOOL byte is DERIVED from this nibble + the sheared bit (woolByte). Sheep-gated at every
	// reader/writer (typ == entity.Sheep.ID), so the ZERO value (WHITE) is the exact non-sheep default and
	// the pig oracle's stream gains ZERO draws from it. Tick-owned plain byte (TICK-05), snapshot-friendly.
	//	[VERIFIED javap Sheep: getColor() == DyeColor.byId(entityData.get(DATA_WOOL_ID) & 0xF);
	//	 setColor(c) -> DATA_WOOL = (byte)((cur & 0xF0) | (c.getId() & 0xF)); DEFAULT_COLOR == WHITE (id 0).]
	sheepColor byte

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

	// catLying is net.minecraft.world.entity.animal.feline.Cat's IS_LYING synched data (BOOLEAN,
	// accessor index 21): the cat is lying flat (CatRelaxOnOwnerGoal.tick setLying / CatLieOnBedGoal
	// setLying). It drives the lying-pose client render (catLyingDataEntry) and the spaceIsOccupied
	// scan (another cat lying||relaxStateOne blocks the spot). FALSE for a non-cat and the zero value.
	//	[VERIFIED javap Cat: IS_LYING = defineId(Cat.class, BOOLEAN); setLying(b) = entityData.set(IS_LYING,b);
	//	 isLying() = get(IS_LYING). Index derivation: Entity(0..7) LivingEntity(8..14) Mob(15) AgeableMob
	//	 (16,17) Animal(none) TamableAnimal(18,19) Cat DATA_VARIANT_ID(20) IS_LYING(21).]
	catLying bool

	// catRelaxStateOne is Cat's RELAX_STATE_ONE synched data (BOOLEAN, accessor index 22): the cat is in
	// the head-up "relax state one" pre-lying pose (CatRelaxOnOwnerGoal.tick setRelaxStateOne). Drives
	// the relax-pose client render (catRelaxDataEntry) and the spaceIsOccupied scan. FALSE otherwise.
	//	[VERIFIED javap Cat: RELAX_STATE_ONE = defineId(Cat.class, BOOLEAN); setRelaxStateOne(b) =
	//	 entityData.set(RELAX_STATE_ONE,b); isRelaxStateOne() = get(RELAX_STATE_ONE). Index 22 (after
	//	 IS_LYING at 21).]
	catRelaxStateOne bool

	// catOnBedTicks is CatRelaxOnOwnerGoal.onBedTicks -- the per-run counter (reset to 0 on start/stop)
	// that, once it exceeds adjustedTickDelay(16), flips the cat from relaxStateOne to lying while it
	// sits on the sleeping owner. Server-side goal state (not synched). 0 for a non-cat / between runs.
	//	[VERIFIED javap Cat$CatRelaxOnOwnerGoal: private int onBedTicks; tick ++onBedTicks; >
	//	 adjustedTickDelay(16) -> setLying(true); stop onBedTicks = 0.]
	catOnBedTicks int

	// catCollarColor is net.minecraft.world.entity.animal.feline.Cat's DATA_COLLAR_COLOR synched data
	// (INT, accessor index 23), reduced to the DyeColor id (WHITE 0 .. BLACK 15). Cat.mobInteract's
	// collar-dye branch sets it (setCollarColor(color) = entityData.set(DATA_COLLAR_COLOR, color.getId())),
	// and getCollarColor() = DyeColor.byId(get(DATA_COLLAR_COLOR)). Its define(...) default is
	// DEFAULT_COLLAR_COLOR.getId() == DyeColor.RED (14) per Cat.defineSynchedData, so a fresh cat starts
	// with a RED collar (NOT 0/WHITE). Set to catDefaultCollarColor at spawn (newTestCat / the mob factory).
	//	[VERIFIED javap Cat: DATA_COLLAR_COLOR = defineId(Cat.class, INT); defineSynchedData define(
	//	 DATA_COLLAR_COLOR, DEFAULT_COLLAR_COLOR.getId()); static{} DEFAULT_COLLAR_COLOR = DyeColor.RED.
	//	 Index derivation: ...TamableAnimal(18,19) Cat DATA_VARIANT_ID(20) IS_LYING(21) RELAX_STATE_ONE(22)
	//	 DATA_COLLAR_COLOR(23).]
	catCollarColor int

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

	// needsSync is net.minecraft.world.entity.Entity.needsSync -- the force-a-move-send-THIS-tick
	// (regardless of updateInterval) flag. Entity.absMoveTo / the deltaMovement push/knockback paths
	// set it true; ServerEntity.sendChanges clears it at the END of every call (needsSync = false).
	// It is the escape hatch that makes a DELIBERATE teleport/knockback immediate even for a coarse-
	// updateInterval type (item/arrow) -- ordinary per-tick AI motion does NOT set it, so AI motion
	// is gated by tickCount %% updateInterval. v1 has no absMoveTo/knockback wired to set it yet
	// (structured so those paths flip it to true later); it defaults false, exactly the vanilla value
	// for an entity that has not been teleported this tick.
	//	[VERIFIED javap Entity: public boolean needsSync; set true in absMoveTo / the push+
	//	 setDeltaMovement path; ServerEntity.sendChanges clears it (needsSync = false) at the tail
	//	 just before tickCount++.]
	needsSync bool

	// leashHolderID is the entity id of this entity's leash HOLDER (a fence leash-knot or another
	// entity), the v1 stand-in for Leashable.getLeashHolder().getId(). 0 == NOT leashed
	// (Leashable.isLeashed() == false). The tracker's sendPairingData branch emits a
	// ClientboundSetEntityLink(this, holder) when this is non-zero at tracking-start so a leashed
	// mob shows its lead client-side; attachLeash/dropLeash (below) set it and broadcast the
	// attach/detach packet. A plain mob (the pig) keeps it 0 -- no leash packet, byte-identical.
	// Tick-owned plain value (snapshot-friendly); a THIN id, never a live *Entity (the Folia rule).
	//	[VERIFIED javap Leashable.isLeashed()/getLeashHolder(); ServerEntity.sendPairingData leash
	//	 branch; ClientboundSetEntityLinkPacket(entity, holder).]
	leashHolderID int32

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

	// equipmentDropChances is the per-slot drop probability used by the on-death dropEquipment
	// path (slotDropChance reads it; the cited v1 default 0.085f substitutes when the field is
	// exactly zero). Mirrors the LivingEntity.dropChances EnumMap<EquipmentSlot,Float>
	// (DropChances.DEFAULT_EQUIPMENT_DROP_CHANCE = 0.085f is the constructor seed). The zero
	// value (no override) keeps the cited vanilla default; a real override (dropChances.setDropChance
	// in a future seam) writes a non-zero value to the corresponding index.
	//	[VERIFIED javap LivingEntity.dropChances field — EnumMap<EquipmentSlot, Float>
	//	 constructed with the DEFAULT 0.085f seed for every slot.]
	equipmentDropChances [equipmentSlotCount]float32

	// equipmentLastBroadcast is the per-slot snapshot used by detectMobEquipmentUpdates to diff
	// against the LIVE e.equipment on each tick and broadcast only the changed slots as
	// ClientboundSetEquipment. The first-call init (equipmentBroadcastInit == false) records the
	// current state WITHOUT broadcasting (the spawn-time equipmentSpawnPackets tracker path is the
	// authoritative initial wire — a seed-then-broadcast would double the equipment packets).
	// Mirror of LivingEntity.lastEquipmentItems (the EntityEquipment.copy() snapshot held on
	// LivingEntity, written by handleEquipmentChanges, read by collectEquipmentChanges).
	equipmentLastBroadcast [equipmentSlotCount]component.SlotData

	// equipmentBroadcastInit gates the first-call seed of equipmentLastBroadcast: while false, a
	// diff between live + last-broadcast SEEDS last-broadcast WITHOUT broadcasting (the equipInit
	// mirror). Flips to true after the first non-empty-slot observation (a fresh-spawn mob with
	// all-empty slots stays at false and stays silent — the byte-identical default the oracle
	// pig relies on, since its slots are all EMPTY and the init seed would otherwise write EMPTY
	// over EMPTY which is observably a no-op).
	equipmentBroadcastInit bool

	// canPickUpLoot is net.minecraft.world.entity.Mob.canPickUpLoot — the boolean field the Mob.aiStep
	// looting scan gates on. Default FALSE (Mob's ctor: `this.canPickUpLoot = false`), so a passive Animal
	// (the oracle pig) NEVER runs the pickup scan and draws ZERO new RNG. A mob that overrides it to true
	// in its ctor (Fox: `setCanPickUpLoot(true)`) — or a hostile with CanPickUpLoot in its save data —
	// enters the scan. Set at spawn (plugin_mob_decl.go) for the mobs that default it true; read by
	// mobPickupItems (item_entity_mob.go). Tick-owned (TICK-05).
	//	[VERIFIED javap Mob: `private boolean canPickUpLoot;` = false in <init>; canPickUpLoot() getter,
	//	 setCanPickUpLoot(boolean) setter; Fox.<init> calls setCanPickUpLoot(true).]
	canPickUpLoot bool

	// --- MOB-HOST-08 (Enderman block-carry): the DATA_CARRY_STATE synched state -----------------
	//
	// carriedBlockState mirrors net.minecraft.world.entity.monster.EnderMan's DATA_CARRY_STATE
	// (EntityDataAccessor<Optional<BlockState>>, EntityDataSerializers.OPTIONAL_BLOCK_STATE). It holds the
	// block state the enderman is carrying (the DEFAULT state of the block it picked up), or "none".
	// EnderMan.setCarriedBlock(state) stores it; getCarriedBlock() reads it back (null == not carrying).
	// EndermanTakeBlockGoal fills it (setCarriedBlock(block.defaultBlockState())); EndermanLeaveBlockGoal
	// clears it (setCarriedBlock(null)). carriedBlockSet is the Optional present-bit: it distinguishes
	// "carrying air (state id 0)" — never a Take result (HOLDABLE blocks are never air) — from the null/
	// not-carrying default, exactly as Java's Optional<BlockState> distinguishes empty from present.
	// Enderman-gated at every reader/writer (typ == entity.Enderman.ID / the enderman native goals), so the
	// ZERO value is the exact non-enderman default and the pig oracle stream gains ZERO draws — the same
	// discipline the turtle fields above follow. Tick-owned (TICK-05); a plain state id (no live pointer).
	//	[VERIFIED javap EnderMan: private static final EntityDataAccessor<Optional<BlockState>>
	//	 DATA_CARRY_STATE; setCarriedBlock(BlockState)/getCarriedBlock():BlockState over SynchedEntityData.]
	carriedBlockState block.StateID
	carriedBlockSet   bool

	// --- MOB-HOST-08 visible-state (Task, audit-flagged 1/1 expansion): the MAINHAND-SYNTHETIC ----
	// broadcast snapshot for the Enderman block-carry. endermanCarryLastBroadcast mirrors the
	// carried state AT THE LAST detectEndermanCarryUpdates broadcast (entity_equipment.go).
	// Parallel to equipmentLastBroadcast[eqSlotMainHand] but kept SEPARATE because the carried
	// MAINHAND packet is a SYNTHETIC add — it does NOT write e.equipment[MAINHAND], so a fresh
	// carry is observable on the wire without disturbing the equipment layer's own snapshot.
	// The first call (endermanCarryBroadcastInit == false) records the live state WITHOUT
	// broadcasting (the spawn-time equipmentSpawnPackets tracker path is the authoritative
	// initial wire — a seed-then-broadcast would double the MAINHAND packet for an enderman
	// spawned mid-carry).
	// Enderman-gated at every reader/writer (typ == entity.Enderman.ID + the carry helpers in
	// enderman_carry_visible.go). A non-enderman (the oracle pig) NEVER reaches the
	// detectEndermanCarryUpdates call site (tick_phases.go's endermanAiStep branch), so the
	// pig's RNG stream gains ZERO new draws and TestPluginPigEqualsGoNativePig stays
	// byte-identical.
	//	[VERIFIED javap EnderMan.setCarriedBlock / getCarriedBlock: vanilla does NOT broadcast a
	//	 MAINHAND equipment packet when the carried state changes — the MAINHAND surface here
	//	 is a Sulfur-side visible-state addition.]
	endermanCarryLastBroadcast    block.StateID
	endermanCarryLastBroadcastSet bool
	endermanCarryBroadcastInit    bool

	// --- MINECART (net.minecraft.world.entity.vehicle.minecart.AbstractMinecart + OldMinecartBehavior) ---
	//
	// Tick-owned plain values, set/read ONLY for a minecart (isMinecart). A minecart is a NON-mob rideable
	// entity whose whole behavior is the rail-follow physics (moveAlongTrack) — the sibling of isArrow /
	// isItem. Vanilla splits minecart movement into a MinecartBehavior class selected by the
	// MINECART_IMPROVEMENTS feature flag; that flag is OFF by default, so the DEFAULT vanilla behavior is
	// OldMinecartBehavior — the one ported here (minecart.go). Zero for every non-minecart entity (the
	// minecart tick gates on isMinecart), so the pig oracle's stream is byte-identically unperturbed.
	//	[VERIFIED CFR AbstractMinecart.<init>: `behavior = useExperimentalMovement(level) ? new
	//	 NewMinecartBehavior(this) : new OldMinecartBehavior(this)`; useExperimentalMovement ==
	//	 enabledFeatures().contains(FeatureFlags.MINECART_IMPROVEMENTS) (experimental, off by default).]

	// isMinecart marks this entity as an AbstractMinecart. The minecart tick (rail-follow physics + the
	// off-rail fall) runs ONLY for entities with this set. Set at spawn by spawnMinecart.
	isMinecart bool

	// minecartFlipped is AbstractMinecart.flipped — the 180deg rotation latch OldMinecartBehavior.tick
	// toggles when the yaw wraps past +/-170deg (so a minecart reversing direction flips its render
	// orientation instead of spinning). DEFAULT false. Tick-owned plain bool.
	//	[VERIFIED CFR AbstractMinecart.flipped (private boolean, DEFAULT_FLIPPED_ROTATION=false);
	//	 isFlipped/setFlipped; OldMinecartBehavior.tick flips it on a wrapDegrees(yaw-yRotO) >= 170 turn.]
	minecartFlipped bool

	// minecartXo/Yo/Zo mirror Entity.xo/yo/zo — the PREVIOUS-tick position OldMinecartBehavior.tick reads
	// to derive the yaw (atan2(zo-z, xo-x)) and the "did the cart cross a block boundary" checks. Updated
	// to the current position at the START of each minecart tick (Entity.baseTick's xo=x/yo=y/zo=z),
	// BEFORE moveAlongTrack moves the cart, so the diff reflects this tick's motion.
	//	[VERIFIED CFR Entity: xo/yo/zo (previous position), set in baseTick before movement.]
	minecartXo, minecartYo, minecartZo float64

	// minecartYRotO mirrors Entity.yRotO — the previous-tick yaw OldMinecartBehavior.tick compares the new
	// yaw against (wrapDegrees(yRot - yRotO)) to decide the flip. Set to yaw at the tick start.
	//	[VERIFIED CFR Entity.yRotO; OldMinecartBehavior.tick: `Mth.wrapDegrees(getYRot() - minecart.yRotO)`.]
	minecartYRotO float32

	// minecartItems is the container backing a CHEST/HOPPER minecart — AbstractMinecartContainer.itemStacks
	// (a NonNullList sized by getContainerSize: MinecartChest==27, MinecartHopper==5). Sized at spawn to the
	// type's container size (minecartContainerSize). nil for a plain minecart. A block hopper pulls from / a
	// player opens this via the entity-container seam (container.go). Also reused as the 27-slot backing of a
	// CHEST BOAT / CHEST RAFT (AbstractChestBoat.itemStacks, getContainerSize()==27) — the boat container
	// adapter (boat_container.go) shares this same slice, so the identical 27-slot chest click engine and the
	// getEntityContainer seam serve both vehicle families with no duplicate item store.
	//	[VERIFIED CFR AbstractMinecartContainer: `NonNullList<ItemStack> itemStacks`; getContainerSize
	//	 abstract (MinecartChest.getContainerSize()==27, MinecartHopper.getContainerSize()==5).
	//	 AbstractChestBoat.itemStacks (NonNullList), getContainerSize()==27.]
	minecartItems []component.SlotData

	// --- PRIMED TNT (net.minecraft.world.entity.item.PrimedTnt) -----------------------------------
	//
	// Tick-owned plain values, set/read ONLY for a PrimedTnt (isTnt). A PrimedTnt is a NON-mob moving
	// Entity (the sibling of the arrow / item drop): its whole behavior is the gravity+drag fall plus
	// a fuse countdown that, at 0, discards the entity and runs the ServerExplosion (radius 4.0, TNT
	// interaction). Zero for every non-tnt entity (the primed-tnt tick gates on isTnt), so the pig
	// oracle stream is unperturbed. Cite PrimedTnt.tick / PrimedTnt.explode.
	//
	// isTnt marks this entity as a PrimedTnt. The primed-tnt tick (gravity 0.04 + drag 0.98 + fuse--
	// -> discard + explode at 0) runs ONLY for entities with this set. Set at spawn by spawnPrimedTnt.
	isTnt bool

	// tntFuse is PrimedTnt.fuse (DEFAULT_FUSE_TIME=80): the ticks until detonation. PrimedTnt.tick
	// decrements it by one each tick and, when it reaches <=0, discards the entity and calls explode().
	// The block-prime path seeds it to 80; the explosion-chain (wasExploded) path seeds it to the
	// random-short fuse (getRandomShortFuse). CITE PrimedTnt.fuse / setFuse / DEFAULT_FUSE_TIME.
	tntFuse int32

	// tntExplosionPower is PrimedTnt.explosionPower (DEFAULT_EXPLOSION_POWER=4.0): the explosion radius
	// PrimedTnt.explode passes to level.explode. A plain float; every block-primed TNT carries 4.0.
	// CITE PrimedTnt.explosionPower / explode (level.explode(..., explosionPower, false, TNT)).
	tntExplosionPower float32

	// --- FALLING BLOCK (net.minecraft.world.entity.item.FallingBlockEntity) -----------------------
	//
	// Tick-owned plain values, set/read ONLY for a FallingBlockEntity (isFalling). A FallingBlockEntity
	// is a NON-mob moving Entity (the sibling of the PrimedTnt / item drop): its whole behavior is the
	// gravity (0.04) + air drag (0.98) fall plus, on landing, either setBlock(blockState) at the target
	// cell or (when the cell won't accept it) a drop as the block's item. Zero for every non-falling
	// entity (the falling tick gates on isFalling), so the pig oracle stream is unperturbed. CITE
	// FallingBlockEntity.tick / getDefaultGravity (0.04) / getAirDrag (0.98).
	//
	// isFalling marks this entity as a FallingBlockEntity. The falling tick (gravity 0.04 + drag 0.98 +
	// land-or-break at rest) runs ONLY for entities with this set. Set at spawn by spawnFallingBlock.
	// The generic MOB-ONLY physics pass (tick_phases.go non-mob skip list) skips this entity
	// (`|| e.isFalling`) so it is not double-integrated. CITE FallingBlockEntity.tick.
	isFalling bool

	// fallingBlockState is FallingBlockEntity.blockState: the block state the entity carries and, on a
	// clean landing, writes back into the world via setBlock. Seeded at spawn from the source block's
	// state. On break it maps to the block's item form for the drop. CITE FallingBlockEntity.blockState.
	fallingBlockState block.StateID

	// fallingTime is FallingBlockEntity.time: the age (ticks) since spawn, incremented once per tick.
	// FallingBlockEntity.tick uses it for the "fell too long" (>100 outside world bounds, or >600)
	// despawn guard. CITE FallingBlockEntity.time.
	fallingTime int32

	// fallingDropItem is FallingBlockEntity.dropItem (DEFAULT true): whether the entity drops its block
	// item when it breaks (can't land) or falls too long. Every block-triggered fall carries true.
	// CITE FallingBlockEntity.dropItem (default true).
	fallingDropItem bool

	// --- TNT MINECART (net.minecraft.world.entity.vehicle.minecart.MinecartTNT) -------------------
	//
	// Tick-owned plain values, set/read ONLY for a TntMinecart (isMinecart && typ==entity.TntMinecart.ID).
	// mcTntFuse mirrors MinecartTNT.fuse (DEFAULT -1 == not primed; primeFuse sets it to 80). MinecartTNT
	// .tick counts it down while >0 and, at ==0, explodes with a velocity-scaled power (explosionPowerBase
	// 4.0 + explosionSpeedFactor 1.0 * nextDouble() * 1.5 * min(sqrt(horizDistSqr),5.0)). -1/false for a
	// non-TNT minecart. CITE MinecartTNT.fuse / primeFuse / tick / explode.
	mcTntFuse int32
	// mcTntPrimed distinguishes the vanilla fuse<0 "never primed" default (-1) from a primed fuse that
	// happened to reach a low value — MinecartTNT.isPrimed() == fuse > -1. A plain bool: false until
	// primeFuse first fires. CITE MinecartTNT.isPrimed (fuse > NO_FUSE(-1)).
	mcTntPrimed bool

	// --- BOAT (net.minecraft.world.entity.vehicle.boat.AbstractBoat + Boat/Raft/ChestBoat/ChestRaft) -----
	//
	// Tick-owned plain values, set/read ONLY for a boat (isBoat). A boat is a NON-mob rideable VehicleEntity
	// whose whole behavior is the surface-float physics (floatBoat) — the sibling of isMinecart. Vanilla
	// runs floatBoat + move(SELF) on the SERVER only while the boat is server-authoritative (no controlling
	// passenger); a ridden boat is client-authoritative (the controlling player's client drives it via
	// ServerboundMoveVehicle, wired in passenger.go). Zero for every non-boat entity (the boat tick gates on
	// isBoat), so the pig oracle's stream is byte-identically unperturbed.
	//	[VERIFIED CFR AbstractBoat.tick: `if (isLocalInstanceAuthoritative()) { ...; floatBoat(); ...;
	//	 move(SELF, getDeltaMovement()); } else setDeltaMovement(Vec3.ZERO);` — server-authoritative iff
	//	 !isClientAuthoritative() (no controlling passenger).]

	// isBoat marks this entity as an AbstractBoat. The boat tick (floatBoat surface physics + the off-water
	// gravity fall) runs ONLY for entities with this set. Set at spawn by spawnBoat.
	isBoat bool

	// boatIsRaft marks a Raft/ChestRaft (bamboo). It changes ONLY the rideHeight seat factor
	// (Raft.rideHeight == height*0.8888889 vs Boat.rideHeight == height/3.0). DEFAULT false (a boat).
	//	[VERIFIED CFR Raft.rideHeight: `return dimensions.height() * 0.8888889f;`.]
	boatIsRaft bool

	// boatStatus / boatOldStatus mirror AbstractBoat.status / oldStatus — the surface state (IN_AIR /
	// ON_LAND / IN_WATER / UNDER_WATER / UNDER_FLOWING_WATER) computed each tick by getStatus, read by
	// floatBoat to pick the buoyancy delta + friction. oldStatus is the PREVIOUS tick's status (the
	// air->water transition that snaps the boat to the surface). DEFAULT boatStatusInAir.
	//	[VERIFIED CFR AbstractBoat.status/oldStatus (Status enum); tick: oldStatus=status; status=getStatus().]
	boatStatus    boatStatus
	boatOldStatus boatStatus

	// boatWaterLevel mirrors AbstractBoat.waterLevel — the water-surface Y (set by checkInWater/getStatus,
	// read by floatBoat for the IN_WATER buoyancy `(waterLevel - y)/bbHeight`). DEFAULT 0.
	//	[VERIFIED CFR AbstractBoat.waterLevel (double).]
	boatWaterLevel float64

	// boatLastYd mirrors AbstractBoat.lastYd — the previous tick's vertical delta, read by getWaterLevelAbove
	// to bound the upward scan. DEFAULT 0.
	//	[VERIFIED CFR AbstractBoat.lastYd (double).]
	boatLastYd float64

	// boatLandFriction mirrors AbstractBoat.landFriction — the averaged block friction under the boat on
	// land (getGroundFriction), used as the ON_LAND invFriction. DEFAULT 0.
	//	[VERIFIED CFR AbstractBoat.landFriction (float).]
	boatLandFriction float32

	// boatDeltaRotation mirrors AbstractBoat.deltaRotation — the per-tick yaw spin the paddle input adds,
	// decayed by invFriction each floatBoat. The paddle-INPUT that grows it is a cited deferral (the client
	// drives a ridden boat's rotation directly via MoveVehicle); the decay is ported for fidelity. DEFAULT 0.
	//	[VERIFIED CFR AbstractBoat.deltaRotation (float); floatBoat: `this.deltaRotation *= invFriction;`.]
	boatDeltaRotation float32

	// boatOutOfControlTicks mirrors AbstractBoat.outOfControlTicks — the submerged-time counter; at >= 60
	// the boat ejects its passengers (a capsized boat throws its rider). DEFAULT 0.
	//	[VERIFIED CFR AbstractBoat.outOfControlTicks (float); tick ejects at >= 60.]
	boatOutOfControlTicks float32

	// --- DISPLAY ENTITIES: ITEM FRAME + ARMOR STAND (net.minecraft.world.entity.decoration) --------
	//
	// Tick-owned plain values, set/read ONLY for a display entity (an ItemFrame/GlowItemFrame, isFrame;
	// or an ArmorStand, isArmorStand). For every OTHER entity they stay at the zero value and are never
	// read (each reader gates on the marker flag / typ), preserving the snapshot-friendly contract and
	// the byte-identical oracle-pig default (a pig draws ZERO of these).

	// isFrame marks this entity as an ItemFrame or GlowItemFrame (a HangingEntity). The frame interact
	// (place-item / rotate) and break (drop-item) paths gate on this. Set at spawn by spawnItemFrame.
	isFrame bool

	// frameItem is net.minecraft.world.entity.decoration.ItemFrame's DATA_ITEM (the framed ItemStack,
	// stored always with Count==1 per setItem's copyWithCount(1)). The zero value (Count==0) is
	// ItemStack.EMPTY — an empty frame. getItem()/setItem() read/write it (display_entity.go).
	//	[VERIFIED javap ItemFrame: DATA_ITEM (ITEM_STACK); setItem copyWithCount(1); getItem.]
	frameItem component.SlotData

	// frameRotation is ItemFrame's DATA_ROTATION (an int 0..7). interact rotates via
	// setRotation(getRotation()+1) which stores `r % 8`, cycling 0→1→…→7→0. Default 0.
	//	[VERIFIED javap ItemFrame.setRotation(int,bool): DATA_ROTATION.set(r % 8); NUM_ROTATIONS=8.]
	frameRotation int32

	// frameDirection is HangingEntity's DATA_DIRECTION reduced to the 3D-data value of the wall face the
	// frame attaches TO (the clicked block face; Direction.get3DDataValue: DOWN=0,UP=1,NORTH=2,SOUTH=3,
	// WEST=4,EAST=5). ClientboundAddEntity carries it as the object `data` field (spawnData). The frame
	// hangs on pos.relative(direction.opposite). Default SOUTH (3).
	//	[VERIFIED javap HangingEntity: DATA_DIRECTION (DIRECTION), DEFAULT_DIRECTION=SOUTH; ItemFrame
	//	 recreateFromPacket: setDirection(Direction.from3DDataValue(packet.getData())).]
	frameDirection int32

	// frameGlow marks a GlowItemFrame (vs a plain ItemFrame): the ONLY behavioral difference is
	// getFrameItemStack() (glow_item_frame vs item_frame) — all geometry/interact/drop logic is shared.
	//	[VERIFIED javap GlowItemFrame: only sound + getFrameItemStack overrides; extends ItemFrame.]
	frameGlow bool

	// frameBlockX/Y/Z is BlockAttachedEntity.pos — the block cell the frame occupies (the clicked block's
	// adjacent cell). blockPosition()/pos; the break spawns the drop offset from it. Set at spawn.
	frameBlockX, frameBlockY, frameBlockZ int

	// --- ARMOR STAND (net.minecraft.world.entity.decoration.ArmorStand) -----------------------------

	// isArmorStand marks this entity as an ArmorStand. The armor-stand interact (equip/take a slot) and
	// break (drop the armor_stand item + every equipped item) paths gate on this. The 6 held/armor slots
	// live in the shared e.equipment array (LivingEntity.equipment). Set at spawn by spawnArmorStand.
	isArmorStand bool

	// armorStandFlags is ArmorStand's DATA_CLIENT_FLAGS byte: SMALL=0x01, SHOW_ARMS=0x04, NO_BASEPLATE=
	// 0x08, MARKER=0x10 (bit 0x02 unused). isSmall/showArms/isMarker read it; showBasePlate is INVERTED
	// (bit 0x08 clear == baseplate shown). Default 0. Carried in metadata for the client render.
	//	[VERIFIED javap ArmorStand: CLIENT_FLAG_SMALL=1, SHOW_ARMS=4, NO_BASEPLATE=8, MARKER=16.]
	armorStandFlags byte

	// armorStandLastHit is net.minecraft.world.entity.decoration.ArmorStand.lastHit — the gameTime tick of
	// the last (non-breaking) player punch. A SECOND punch within 5 ticks (`time - lastHit <= 5L`) breaks
	// the stand; the first punch just records the time + wobbles (broadcastEntityEvent 32). Default 0 for a
	// fresh stand (a first punch's `time - 0` is huge, so it never breaks on the first hit). Tick-owned.
	//	[VERIFIED javap ArmorStand.hurtServer: `if (time - this.lastHit <= 5L || shouldKill) brokenByPlayer;
	//	 else broadcastEntityEvent(32); lastHit = time`.]
	armorStandLastHit int64

	// --- DISPLAY ENTITIES: ITEM DISPLAY (net.minecraft.world.entity.Display + Display$ItemDisplay) ----
	//
	// MODEL-M1 foundation. Tick-owned plain values, set/read ONLY for an ItemDisplay (isItemDisplay). For
	// every OTHER entity they stay at the zero value and are never read (readers gate on the marker/typ),
	// preserving the snapshot-friendly contract and the byte-identical oracle-pig default (a pig draws ZERO
	// of these). Seeded to the Display defaults (scale (1,1,1), rotations identity (0,0,0,1)) in
	// spawnItemDisplay (NewEntity stays generic).

	// isItemDisplay marks this entity as a Display$ItemDisplay. The metadata encoder + transform setters
	// gate on this. Set at spawn by spawnItemDisplay.
	isItemDisplay bool

	// displayItem is Display$ItemDisplay's DATA_ITEM_STACK (the shown ItemStack, ITEM_STACK serializer).
	// The zero value (Count==0) is ItemStack.EMPTY.
	//	[VERIFIED javap Display$ItemDisplay: DATA_ITEM_STACK (ITEM_STACK) index 23, default EMPTY.]
	displayItem component.SlotData

	// displayContext is Display$ItemDisplay's DATA_ITEM_DISPLAY (the ItemDisplayContext ordinal, BYTE
	// serializer). Default 0 (ItemDisplayContext.NONE).
	//	[VERIFIED javap Display$ItemDisplay: DATA_ITEM_DISPLAY (BYTE) index 24, default 0 (NONE).]
	displayContext int8

	// dispTransX/Y/Z is Display's DATA_TRANSLATION (VECTOR3, index 11). Default (0,0,0) — the Display
	// defineSynchedData `new Vector3f()`.
	dispTransX, dispTransY, dispTransZ float32

	// dispScaleX/Y/Z is Display's DATA_SCALE (VECTOR3, index 12). Default (1,1,1) — the Display
	// defineSynchedData `new Vector3f(1,1,1)`. Seeded in spawnItemDisplay.
	dispScaleX, dispScaleY, dispScaleZ float32

	// dispLeftRot is Display's DATA_LEFT_ROTATION (QUATERNION, index 13) as [x,y,z,w]. Default identity
	// (0,0,0,1) — the Display defineSynchedData `new Quaternionf()`. Seeded in spawnItemDisplay.
	dispLeftRot [4]float32

	// dispRightRot is Display's DATA_RIGHT_ROTATION (QUATERNION, index 14) as [x,y,z,w]. Default identity
	// (0,0,0,1). Seeded in spawnItemDisplay.
	dispRightRot [4]float32

	// dispInterpDuration is Display's DATA_TRANSFORMATION_INTERPOLATION_DURATION_ID (INT, index 9). Default 0.
	dispInterpDuration int32

	// dispInterpStartDelta is Display's DATA_TRANSFORMATION_INTERPOLATION_START_DELTA_TICKS_ID (INT,
	// index 8). Default 0.
	dispInterpStartDelta int32
	// --- ARMADILLO / BREEZE / CREAKING / COPPER_GOLEM (26.x roster, Task) ---------------------------
	//
	// Tick-owned plain values, set/read ONLY for their own type (each *AiStep gates on typ). isArmadillo/
	// isBreeze/isCreaking/isCopperGolem mark the entity.
	//
	// ARMADILLO (net.minecraft.world.entity.animal.armadillo.Armadillo): armadilloState mirrors ARMADILLO_
	// STATE (0 IDLE / 1 ROLLING / 2 SCARED / 3 UNROLLING); armadilloInStateTicks mirrors inStateTicks (the
	// per-state tick counter, incremented every tick in tick()); armadilloScuteTime mirrors scuteTime (the
	// customServerAiStep countdown: at <=0 the armadillo sheds a scute + resets to pickNextScuteDropTime =
	// nextInt(6000)+6000). Cite Armadillo ARMADILLO_STATE + inStateTicks + scuteTime + customServerAiStep.
	isArmadillo           bool
	armadilloState        int
	armadilloInStateTicks int64
	armadilloScuteTime    int
	// BREEZE (net.minecraft.world.entity.monster.breeze.Breeze): a Brain mob whose signature is the jump-
	// around movement + the WindCharge ranged shoot (BreezeAi Shoot behavior). breezeShootCooldown mirrors
	// the Shoot behavior cadence (SHOOT_COOLDOWN_TICKS 10 between shoots; the WindCharge projectile entity
	// is the DEFERRED behavior layer). Cite Breeze + BreezeAi Shoot.
	isBreeze            bool
	breezeShootCooldown int
	// CREAKING (net.minecraft.world.entity.monster.creaking.Creaking): the pale-garden mob that FREEZES when
	// a player looks at it (checkCanMove -> false) and moves only when NOT observed. creakingCanMove mirrors
	// CAN_MOVE (the observed-freeze flag, default true); creakingActive mirrors IS_ACTIVE (activated when a
	// player is observed within 12 blocks); creakingHomeX/Y/Z + creakingHeartBound mirror HOME_POS (the
	// Creaking Heart it is tied to; the heart block-entity is the DEFERRED behavior layer). creakingAttack
	// AnimTicks mirrors attackAnimationRemainingTicks (the 15-tick attack window). Cite Creaking CAN_MOVE +
	// IS_ACTIVE + HOME_POS + checkCanMove + aiStep.
	isCreaking              bool
	creakingCanMove         bool
	creakingActive          bool
	creakingHeartBound      bool
	creakingHomeX           int
	creakingHomeY           int
	creakingHomeZ           int
	creakingAttackAnimTicks int
	// COPPER_GOLEM (net.minecraft.world.entity.animal.golem.CopperGolem): the button-pressing golem that
	// OXIDIZES over time (updateWeathering). copperGolemWeather mirrors DATA_WEATHER_STATE (0 UNAFFECTED /
	// 1 EXPOSED / 2 WEATHERED / 3 OXIDIZED); copperGolemNextWeatherTick mirrors nextWeatheringTick (-1 =
	// uninitialized, -2 = disabled, else the gameTime the next oxidation stage fires); copperGolemIsStatue
	// mirrors the turnToStatue terminal state (an OXIDIZED golem eventually freezes into a statue; the
	// statue BLOCK conversion is the DEFERRED behavior layer). Cite CopperGolem DATA_WEATHER_STATE +
	// nextWeatheringTick + updateWeathering + turnToStatue.
	isCopperGolem              bool
	copperGolemWeather         int
	copperGolemNextWeatherTick int64
	copperGolemIsStatue        bool
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
		// Seed the air bubble to Entity.getMaxAirSupply() == 300 exactly as the vanilla Entity ctor does
		// (`define(DATA_AIR_SUPPLY_ID, getMaxAirSupply())`). lastAirSent mirrors it so a full-air mob is
		// already "sent" (no spurious first-tick SetEntityData). maxAirSupply is the breath.go const (300).
		airSupply:   maxAirSupply,
		lastAirSent: maxAirSupply,
		// Attach the per-entity AttributeMap from this type's DefaultAttributes supplier (the Go
		// analogue of LivingEntity's `this.attributes = new AttributeMap(DefaultAttributes.getSupplier(
		// type))`). NewMapForEntity returns nil for a type with no registered supplier (a dropped Item,
		// an unported mob) — a nil map is the faithful "no DefaultAttributes" state and every read
		// helper degrades to the registration default. Keyed by the type's registry name (t.Name).
		attributes: attribute.NewMapForEntity(t.Name),
		fireImmune: entityTypeFireImmune(t.ID),
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
//
//	[VERIFIED javap LivingEntity.setJumping(boolean): aload_0; iload_1; putfield jumping:Z; return —
//	 i.e. `this.jumping = b;`. No side effects beyond the field write.]
func (e *Entity) setJumping(b bool) { e.jumping = b }

// isAffectedByFluids is net.minecraft.world.entity.LivingEntity.isAffectedByFluids — the gate the
// aiStep jump branch checks (`if (jumping && isAffectedByFluids())`). For a base LivingEntity it
// returns TRUE unconditionally (only a few overrides — e.g. ArmorStand — return false). A v1 Pig is
// a plain LivingEntity, so this is a cited const-true, structured to become a per-type override read
// (a `noFluidAffect` flag) once a mob type needs the ArmorStand-style false. MOB-SUB-04.
//
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
//
//	[VERIFIED javap (33-JARNOTES): Pig.BABY_DIMENSIONS = EntityType.PIG.getDimensions().scale(0.5f)
//	 .withEyeHeight(0.40625f) == scalable(0.45, 0.45); adult pig dims 0.9x0.9 from the data table.]
const babyDimensionScale = 0.5

// isBaby is net.minecraft.world.entity.AgeableMob.isBaby() — true exactly while the age machine is
// negative (a growing baby). The follow/breed goal distSqr checks and the half-scale hitbox both
// read it. MOB-SUB-08.
//
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
//
//	[VERIFIED javap Pig.getDefaultDimensions: isBaby() ? BABY_DIMENSIONS : super; BABY_DIMENSIONS is the
//	 adult dims scaled 0.5 — so baby width/height = adultWidth/adultHeight * babyDimensionScale.]
func (e *Entity) refreshDimensions() {
	// MODEL-M6 (spec G.4): a declared model whose ACTIVE nav-state carries a real bounding-box override
	// resizes the mob's ACTUAL collision/hitbox AABB -- the native-advantage payoff (a Bukkit plugin can
	// only swap the cosmetic Pose byte; the server box never moves for it). This is the Sulfur analogue of
	// vanilla getDimensions(getPose()): the active model state is the "pose", modelPoseDimensions is the
	// per-pose EntityDimensions table. Gated on a NON-NIL override -- a modelless mob (e.model==nil) and a
	// model without state_dims both fall through to the vanilla baby/adult logic below, byte-identical to
	// the pre-M6 path (the pig oracle draws nothing new; it never has e.model). The override wins over
	// baby-scaling exactly as a vanilla per-Pose dims entry wins over the type default.
	//
	//	[VERIFIED javap Entity.refreshDimensions: dimensions = getDimensions(getPose()); the pose selects
	//	 the EntityDimensions, then makeBoundingBox rebuilds the AABB from (width,height). Sulfur's AABB()
	//	 derives from width/height, so writing them here IS the makeBoundingBox recompute.]
	if w, h, ok := modelPoseDimensions(e); ok {
		e.width = float64(w)
		e.height = float64(h)
		return
	}
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
//
//	[VERIFIED javap Animal.setInLove: `this.inLove = 600;` (sipush 600); DEFAULT_IN_LOVE_TIME == 600.]
const defaultInLoveTime = 600

// isInLove is net.minecraft.world.entity.animal.Animal.isInLove() == `this.inLove > 0`. True while
// the love-mode countdown is running (BreedGoal.canUse gates on it: a pig only seeks a partner while
// in love). Pure read, draws no RNG.
//
//	[VERIFIED javap Animal.isInLove: `return this.inLove > 0;` (getfield inLove; ifle; iconst_1/0).]
func (e *Entity) isInLove() bool { return e.inLove > 0 }

// canFallInLove is net.minecraft.world.entity.animal.Animal.canFallInLove() == `this.inLove <= 0`.
// The FEED-path adult branch (mobInteract) gates on it so a pig already in love is not re-armed (and
// does not consume a second food item). Pure read, draws no RNG.
//
//	[VERIFIED javap Animal.canFallInLove: `return this.inLove <= 0;` (getfield inLove; ifgt; iconst_1/0).]
func (e *Entity) canFallInLove() bool { return e.inLove <= 0 }

// setInLove is net.minecraft.world.entity.animal.Animal.setInLove(player) reduced to its inLove
// countdown effect: `this.inLove = 600`. Vanilla ALSO records the love-causing ServerPlayer (for the
// breed XP-orb attribution) and calls level.broadcastEntityEvent(this, (byte)18) to fire the client
// heart-particle burst; the loveCause record is a Plan-C concern (cited OUT OF SCOPE above, NOT baked
// away), and the EntityEvent-18 heart broadcast is performed by the FEED-path caller (broadcastHearts)
// right after this, mirroring setInLove's own broadcast. Tick-owned; the value is never mutated off
// the tick goroutine.
//
//	[VERIFIED javap Animal.setInLove: sipush 600; putfield inLove; (record loveCause); level();
//	 bipush 18; Level.broadcastEntityEvent(this, 18).]
func (e *Entity) setInLove() { e.inLove = defaultInLoveTime }

// isTrusting is net.minecraft.world.entity.animal.feline.Ocelot.isTrusting() ==
// getEntityData().get(DATA_TRUSTING). The trust flag the OcelotTemptGoal.canScare override reads
// (OcelotTemptGoal.canScare = super.canScare() && !this.ocelot.isTrusting()). False for a fresh
// ocelot; true once the feed-trust interact's 1-in-3 roll succeeds. Zero for every non-ocelot.
//
//	[VERIFIED javap Ocelot.isTrusting: getfield entityData; getstatic DATA_TRUSTING;
//	 invokevirtual SynchedEntityData.get.(EntityDataAccessor).]
func (e *Entity) isTrusting() bool { return e.ocelotTrusting }

// setTrusting is net.minecraft.world.entity.animal.feline.Ocelot.setTrusting(b) reduced to its
// ocelotTrusting field write (entityData.set(DATA_TRUSTING, b)). The matching broadcast (the
// SetEntityData push SynchedEntityData.set fans to trackers) is performed by the FEED-path caller
// (TickLoop.tryToTrustOcelot: setTrusting + broadcastEntityEvent heart/smoke + the DATA_TRUSTING
// broadcast via setOcelotTrustingData), exactly mirroring the setInLove/broadcastHearts split
// (entity.go:2091 + combat_mob.go:912). Tick-owned.
//
//	[VERIFIED javap Ocelot.setTrusting: getfield entityData; getstatic DATA_TRUSTING;
//	 invokevirtual SynchedEntityData.set.(EntityDataAccessor, Object); invokevirtual reassessTrustingGoals.]
func (e *Entity) setTrusting(b bool) { e.ocelotTrusting = b }

// canMate is net.minecraft.world.entity.animal.Animal.canMate(Animal other): the partner gate the
// BreedGoal's getFreePartner scan applies — `other != this && other.getClass() == this.getClass()
// && this.isInLove() && other.isInLove()`. Our same-species check is `other.typ == e.typ` (both the
// vanilla_pig wire type, entity.Pig.ID): two distinct same-type animals that are BOTH in love may
// breed. Pure read, draws no RNG.
//
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
//
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
//
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
//
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
//
//	[VERIFIED javap AgeableMob.canAgeUp: `return isBaby() && !isAgeLocked();`; isAgeLocked reads
//	 AGE_LOCKED (v1 const-false stub here, same as the tryFeedAnimal canAgeUp comment).]
func (e *Entity) canAgeUp() bool { return e.isBaby() }
