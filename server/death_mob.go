package server

// death_mob.go — Phase 29 Plan 04 (MOB-SUB-01 keystone close): the mob death flow, the literal
// 1:1 port of net.minecraft.world.entity.LivingEntity.die(DamageSource) and its loot/XP tail
// (dropAllDeathLoot -> dropFromLootTable + dropExperience), verified method-for-method against
// temp/cache/26.2-inner.jar via `javap -c -p` this session. No GPL source is pasted — the
// algorithm is re-expressed in Go — but the call chain, the ordering, the gates, and the numeric
// ops (the xpReward draw, the 1-3 set_count, the player-kill gate) are IDENTICAL to the bytecode.
//
// THE FOLIA RULE (Pitfall 2): the death path runs at the BARRIER or on the owner region, NOT
// already inside the victim's owning region context (unlike block_drop.go's spawnBlockDrop, which
// runs on a tick path already in the owning region and uses cur()). So every store mutation here —
// the Item-drop spawn, the XP-orb spawn, the mob removal — routes through t.regionForEntity(e),
// NOT t.cur() (which would silently fall to region 0). The death-status broadcast rides the
// existing tracker fan-out (broadcastToTrackers), and the REMOVAL itself auto-broadcasts
// RemoveEntities (the tracker's near() no longer returns the gone entity — A2).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/loot"
)

// deathAnimationTicks is the LivingEntity.tickDeath threshold: the entity is removed (and the
// status-60 death poof broadcast) once deathTime reaches 20 ticks (~1s at 20 TPS) — the length of
// the client-side fall-over animation that the status-3 broadcast in die() starts.
//
//	[VERIFIED javap LivingEntity.tickDeath: `if (this.deathTime >= 20 ...)` (bipush 20).]
const deathAnimationTicks int32 = 20

// dieEntity is the port of net.minecraft.world.entity.LivingEntity.die(DamageSource) — the death
// flow driven from applyDamageEntity when a mob's health reaches 0. CRITICAL (WR live-debug): die()
// does NOT remove the entity. It marks it dead, rolls the loot/XP, broadcasts the death-animation
// status (3), and leaves the corpse in the world; LivingEntity.tickDeath() (below) then runs every
// tick and ONLY at deathTime >= 20 (~1s of fall-over animation) broadcasts the death-poof status
// (60) and removes the entity. The old immediate-removal here made a dying mob vanish instantly —
// no fall-over, no death sound, no poof — which this fix corrects.
//
// Faithful bytecode trace (javap die this session):
//
//	if (isRemoved() || this.dead) return;            // the death guard (T-29-08, no double-death)
//	this.dead = true;                                // (set in setHealth<=0 / die; modeled here)
//	Entity attacker = source.getEntity();            // the causing entity (unused for v1 kill score)
//	getKillCredit(); awardKillScore(...);            // scoreboard — v1 stub
//	stopSleeping(); stopUsingItem();                 // v1 stubs (a mob has no sleep/use state wired)
//	if (!level.isClientSide && hasCustomName()) LOGGER.info(...);  // v1: no custom-name death log
//	handleKillingBlow(); getCombatTracker().recheckStatus();       // v1 stubs
//	if (level instanceof ServerLevel) {
//	    if (attacker == null || attacker.killedEntity(level, this, source)) {
//	        gameEvent(ENTITY_DIE);                   // v1 stub
//	        dropAllDeathLoot(level, source);         // -> loot + XP (dropMobLoot + dropMobExperience)
//	        createWitherRose(killCredit);            // v1 stub (only a Wither kill makes a rose)
//	    }
//	    level.broadcastEntityEvent(this, (byte) 3);  // the death-status animation (status 3)
//	}
//	setPose(Pose.DYING);                             // v1: pose is not wired to a mob metadata yet
//	// NOTE: there is NO this.remove() in die() — the removal is tickDeath()'s job at deathTime>=20.
//
// Sulfur ordering: guard -> dead=true -> dropAllDeathLoot (loot + XP, AT death-time so the drops
// spawn at the mob's still-current position: vanilla rolls loot in die() while the entity is still
// in the world) -> death-status broadcast (status 3) -> seed deathTime=0 for the tickDeath countdown.
// The corpse STAYS in the store so trackers keep sending it and the client plays the ~1s fall-over.
//
//	[CITED: LivingEntity.die contains NO remove() call (javap die: dropAllDeathLoot, broadcastEntityEvent
//	 (3), setPose(DYING) — and returns). The removal lives in LivingEntity.tickDeath (deathTime>=20).]
func (t *TickLoop) dieEntity(e *Entity, src damageSource) {
	// The death guard (bytecode 0-14): `if (isRemoved() || dead) return`. Sulfur has no separate
	// isRemoved() flag for a mob (removal IS the store delete), so `dead` covers both — a second
	// lethal hit or a re-entrant die is a no-op (the loot is never double-rolled, deathTime is not
	// re-seeded). T-29-08 (the guarded double-death).
	if e.dead {
		return
	}
	e.dead = true

	// Pin the authoritative health at 0 (the isDeadOrDying state) and record the lethal source — the
	// loot/XP gate + the (P31/P36) consumers read it.
	if e.health > 0 {
		e.health = 0
	}
	e.lastDamageSource = src

	// STATISTICS (stats.go): if a PLAYER dealt the lethal blow, credit their statistics — the
	// ENTITY_KILLED[mobType] tally + the CUSTOM minecraft:mob_kills counter (the stats screen
	// "Mobs Killed" + per-mob rows). src.attacker is the causing entity id (0 == no entity source,
	// e.g. fall/lava). e.typ indexes registryid.EntityType directly (entity.Pig.ID == the registry
	// index). Nil-guarded so a mob killed by the environment or a stats-less player is a no-op.
	//	[VERIFIED javap Player.awardKillScore / LivingEntity.dropAllDeathLoot flow: on a player kill
	//	 the killer awards Stats.ENTITY_KILLED.get(type) + Stats.MOB_KILLS. The minecraft:player_killed_entity
	//	 ADVANCEMENT trigger (entity_type predicate) is DEFERRED here — the predicate-condition feed is
	//	 not parsed (only inventory_changed item ids are), so kill_a_mob et al. are not granted; the
	//	 kill STAT (the load-bearing stats-screen observable) is wired.]
	if src.attacker != 0 {
		if killer := t.playerByEntityID(src.attacker); killer != nil && killer.stats != nil {
			if int(e.typ) >= 0 && int(e.typ) < len(registryid.EntityType) {
				killer.stats.increment(statKey{typeID: StatTypeKilled, valueID: int32(e.typ)}, 1)
			}
			killer.stats.incrementCustom("minecraft:mob_kills", 1)
		}
	}

	// dropAllDeathLoot(level, source) — the loot table + the XP orb. Vanilla rolls loot in die() while
	// the entity is still in the world, so the drops spawn at the mob's still-current position. The
	// killedEntity gate (`attacker == null || attacker.killedEntity(...)`) is a v1 stub that always
	// allows the loot (no kill-cancel hook), so dropAllDeathLoot runs unconditionally on a ServerLevel.
	t.dropAllDeathLoot(e, src)

	// level.broadcastEntityEvent(this, 3) — the death-status animation (status 3) that STARTS the
	// client-side fall-over. Broadcast to every player tracking the mob, exactly as
	// ServerChunkCache.broadcastAndSend fans broadcastEntityEvent out. The corpse remains in the
	// store, so the players keep it in their tracked set and watch the ~1s death animation play.
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventDeath))

	// setPose(Pose.DYING): a mob's pose is not wired to entity metadata in v1 (no DATA_POSE sync for a
	// mob yet) — cited no-op, the death animation already plays from the status-3 broadcast above.

	// Seed the death-animation countdown. die() leaves the mob in the world; tickDeath() increments
	// deathTime each tick and removes the entity at >= 20 (broadcasting the status-60 poof first). The
	// removal is DELIBERATELY NOT done here (vanilla die() has no remove()) — see tickDeath below.
	e.deathTime = 0

	// SKILLS-01 (mob_skills.go): the declared-skill "death" trigger — the MythicMobs ~onDeath analogue.
	// Fires ONCE per death (the e.dead guard above makes dieEntity single-entry), after the loot roll +
	// the status-3 broadcast, while the corpse is still in the store (targeters resolve at the death
	// position). Gated on e.skills != nil — every vanilla mob pays one nil-check and nothing else.
	if e.skills != nil {
		t.fireMobSkillTrigger(e, triggerDeath, skillTriggerCtx{})
	}
}

// tickDeath is the port of net.minecraft.world.entity.LivingEntity.tickDeath() — the per-tick death
// countdown that drives the actual removal. LivingEntity.baseTick calls it every tick while the mob
// isDeadOrDying() (and level.shouldTickDeath(this)); after ~1s (deathTime >= 20) it broadcasts the
// death-poof status (60) and removes the entity (Entity.RemovalReason.KILLED), which auto-broadcasts
// RemoveEntities to every tracker.
//
// Faithful bytecode trace (javap tickDeath this session):
//
//	protected void tickDeath() {
//	    ++this.deathTime;
//	    if (this.deathTime >= 20 && !this.level().isClientSide() && !this.isRemoved()) {
//	        this.level().broadcastEntityEvent(this, (byte) 60);     // status 60 = the death poof
//	        this.remove(Entity.RemovalReason.KILLED);               // -> RemoveEntities to trackers
//	    }
//	}
//	[VERIFIED javap LivingEntity.tickDeath: dup getfield deathTime; iconst_1; iadd; putfield deathTime;
//	 getfield deathTime; bipush 20; if_icmplt return; isClientSide ifne return; isRemoved ifne return;
//	 bipush 60; Level.broadcastEntityEvent(this,60); getstatic RemovalReason.KILLED; remove(...).]
//
// !isClientSide is a constant true on the server (Sulfur has no client world). !isRemoved is covered
// by the call-site gate in tickAI: tickDeath runs only for an entity STILL in the store (e.dead==true
// and not yet removed), so a re-entry after removal cannot happen — the entity is gone from the byID
// map the loop ranges. Runs on the owner goroutine over tick-owned state (TICK-05). Pure integer math
// (the ++deathTime / >=20 compare) — no RNG draw, so it cannot perturb the per-mob RNG stream the pig
// oracle pins (PITFALLS Pitfall 5); the oracle pig is never killed, so e.dead stays false and tickDeath
// never runs on it.
func (t *TickLoop) tickDeath(e *Entity) {
	// ++this.deathTime;
	e.deathTime++

	// if (deathTime >= 20 && !isClientSide && !isRemoved) { broadcastEntityEvent(60); remove(KILLED); }
	// 20 ticks == ~1s: the fall-over animation has played; now poof + despawn.
	if e.deathTime >= deathAnimationTicks {
		// broadcastEntityEvent(this, 60) — the death-poof particles. Broadcast to the still-tracking
		// players BEFORE the remove so they still have the mob in their tracked set this tick (the poof
		// renders at its last-known position; the removal that follows despawns it).
		t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventDeathPoof))

		// remove(Entity.RemovalReason.KILLED): the actual store removal, routed through the OWNING
		// region (regionForEntity(e), NEVER cur() — the region-0 trap, Pitfall 2). The remove
		// auto-broadcasts RemoveEntities: the tracker's near() no longer returns the gone id, so it is
		// batched into the next tick's RemoveEntities and dropped from every viewer's tracked set (A2).
		// MOB-CUBE (SulfurCube): AbstractCubeMob.remove() splits a size>1 cube into 2 smaller cubes JUST
		// BEFORE the store removal (vanilla's remove() runs the split then super.remove()). Per-type-gated on
		// typ == entity.SulfurCube.ID; a no-op for every other mob (and for a size-1 cube). Cite
		// AbstractCubeMob.remove.
		if e.typ == entity.SulfurCube.ID {
			t.sulfurCubeSplitOnRemove(e)
		}
		// MAGMA CUBE (Task): AbstractCubeMob.remove() splits a size>1 magma cube into 2..4 (2+nextInt(3))
		// smaller cubes JUST BEFORE the store removal, exactly like the SulfurCube split. Per-type-gated on
		// typ == entity.MagmaCube.ID; a no-op for a size-1 cube + every other mob. Cite AbstractCubeMob.remove.
		if e.typ == entity.MagmaCube.ID {
			t.magmaCubeSplitOnRemove(e)
		}
		// SLIME (Task): AbstractCubeMob.remove split -- a dead size>1 slime spawns 2..4 half-size slimes.
		// Per-type-gated on typ == entity.Slime.ID; a no-op for a size-1 slime + every other mob. Cite
		// AbstractCubeMob.remove.
		if e.typ == entity.Slime.ID {
			t.slimeSplitOnRemove(e)
		}
		// Entity.setRemoved ejects passengers before the store removal: `getPassengers().forEach(Entity::
		// stopRiding)` — so a player riding this mob (a happy ghast) is dismounted (its SetPassengers list
		// shrinks + it stops following a despawned vehicle) instead of being orphaned. A mob with no
		// passengers (every non-vehicle, the oracle pig) is a cheap no-op. Cite Entity.setRemoved.
		if len(e.passengers) > 0 {
			t.ejectPassengers(e)
		}
		// WITHER BOSS (Task): tear down the boss bar on the wither's removal (ServerBossEvent.removeAllPlayers
		// runs on the boss entity's removal). Gated on e.wither != nil; a no-op for every other mob. Cite
		// WitherBoss (ServerBossEvent lifecycle tied to the entity).
		if e.wither != nil {
			t.witherBossBarRemoveAll(e)
		}
		t.regionForEntity(e).entities.remove(e.id)
	}
}

// dropAllDeathLoot is the port of LivingEntity.dropAllDeathLoot(ServerLevel, DamageSource):
//
//	boolean flag = this.lastHurtByPlayerMemoryTime > 0;   // "recently hurt by a player"
//	if (shouldDropLoot(level)) {                          // !isBaby && MOB_DROPS gamerule
//	    dropFromLootTable(level, source, flag);
//	    dropCustomDeathLoot(level, source, flag);         // v1 stub (no per-mob custom loot)
//	}
//	dropEquipment(level);                                 // per-slot 0.085f roll + damage + spawn
//	dropExperience(level, source.getEntity());
//
// flag (the looting/smelt-affecting "killed by a player" bit) is modeled by the player-attack proxy
// (see killedByPlayer): there is no lastHurtByPlayerMemoryTime field on *Entity yet, so a direct
// player-attack death is the v1 proxy for "recently hurt by a player". shouldDropLoot is the
// !isBaby + MOB_DROPS gate — v1 has no baby state on a plain mob and the gamerule defaults true, so
// it is a constant true (cited). dropEquipment is the 1:1 port of Mob.dropEquipment: for each
// non-empty equipment slot, roll nextFloat() < dropChance[slot] on the LEVEL rng; on success,
// damage the item and spawnAtLocation (the per-slot 0.085f default is the cited vanilla
// DropChances.DEFAULT_EQUIPMENT_DROP_CHANCE; see dropMobEquipment, entity_equipment.go). Runs
// INSIDE the death window — BEFORE the corpse is removed at deathTime>=20 — so a player watching
// the death sees the drops appear. dropExperience runs unconditionally (its own player-kill gate
// is inside dropMobExperience).
func (t *TickLoop) dropAllDeathLoot(e *Entity, src damageSource) {
	// shouldDropLoot(level): !isBaby() && gameRules.MOB_DROPS. v1 has no baby state on a plain mob and
	// MOB_DROPS defaults to true, so this is a cited constant-true gate — structured so a future baby
	// flag / gamerule slots in here unchanged.
	const shouldDropLoot = true
	if shouldDropLoot {
		t.dropMobLoot(e, src)
		// dropCustomDeathLoot: per-mob hand-coded extra drops. WITHER BOSS (Task): WitherBoss
		// .dropCustomDeathLoot spawns a NETHER_STAR (setExtendedLifetime). Gated on e.wither != nil so every
		// other mob takes the unchanged v1 no-op path. Cite WitherBoss.dropCustomDeathLoot.
		if e.wither != nil {
			t.witherDropNetherStar(e)
		}
	}
	// dropEquipment(level): the per-slot 0.085f roll + damageItem + spawnAtLocation for every
	// non-empty equipment slot (Mob.dropEquipment / the surviving half of the 26.2
	// Mob.dropPreservedEquipment). Runs BEFORE dropExperience exactly as the jar orders it
	// (LivingEntity.dropAllDeathLoot). The dead mob is still in the store this tick — die() does
	// NOT remove (see tickDeath) — so the drop spawns at the mob's current position. See
	// entity_equipment.go dropMobEquipment + slotDropChance + damageEquipmentItem. CITE:
	// net.minecraft.world.entity.LivingEntity.dropEquipment (vanilla 26.2 stubbed in
	// LivingEntity; the actual implementation lives in Mob.dropEquipment / Mob.dropPreservedEquipment).
	t.dropMobEquipment(e)

	// dropExperience(level, source.getEntity()): the XP orb (its own player-kill + not-baby gate).
	t.dropMobExperience(e, src)
}

// dropMobLoot is the port of LivingEntity.dropFromLootTable(ServerLevel, DamageSource, boolean) ->
// dropFromLootTable(..., ResourceKey): it resolves the mob's loot-table id (Mob.getLootTable, which
// falls back to the LivingEntity default key "minecraft:entities/<registry-name>"), and if present
// rolls it into Item entities spawned at the mob's position in its OWNER region.
//
//	Optional<ResourceKey> opt = getLootTable();
//	if (opt.isEmpty()) return;                    // no table -> no drop (item/orb entities, etc.)
//	LootTable tbl = ...get(opt);
//	tbl.getRandomItems(params, seed, this::spawnAtLocation);  // per stack: popResource
//
// The loot CONTEXT carries the killed_by_player bit + the entity flags (is_on_fire) the pig table's
// gated functions read — the BOUNDED A4 extension (see level/loot.NewEntityLootContext): for v1 the
// pig is never on fire and the attacker has no looting/smelts_loot enchant, so furnace_smelt and
// enchanted_count_increase are faithful no-ops and the UNCONDITIONAL set_count[1,3] pool rolls — the
// pig drops 1-3 RAW porkchop. seed is a fresh server-generated math/rand/v2 draw (event-time, OUTSIDE
// the 500-tick oracle window; never client-supplied — T-29-07), the same discipline block_drop.go
// uses.
func (t *TickLoop) dropMobLoot(e *Entity, src damageSource) {
	rec, ok := entity.ByID[e.typ]
	if !ok {
		return // unknown type -> no loot table
	}
	// Mob.getLootTable -> LivingEntity.getLootTable default key: "minecraft:entities/<registry-name>".
	name := "minecraft:entities/" + rec.Name
	tbl, err := loot.LoadTable(name)
	if err != nil {
		return // no embedded loot table for this type (item/orb/unported) -> no drop (never panic).
	}

	// The event-time loot seed: a fresh math/rand/v2 draw (server-generated, never client-supplied —
	// the same T-20-05 discipline chests/block-drops use). Outside the oracle window (the oracle pig
	// is never killed in its 500-tick run).
	seed := rand.Int64()

	// Build the ENTITY loot context (the A4 bounded extension): it carries killed_by_player (the
	// player-attack proxy) + the v1 entity-flag defaults the gated functions read.
	// CUBE-MOB (slime/magma_cube): the loot table gates its per-size pools on
	// type_specific/cube_mob.size (the slimeball pool is size 1). Thread the dying cube's size so the
	// size-1 slimeball pool fires only for a tiny slime (the roll runs while getSize() is still the
	// dying cube's size, exactly as vanilla rolls loot in remove()/die). 0 for a non-cube mob.
	cubeSize := 0
	if e.isSlime || e.isMagmaCube {
		cubeSize = int(e.cubeSize)
	}
	ctx := loot.NewEntityLootContext(seed, 0, loot.EntityLootParams{
		KilledByPlayer: killedByPlayer(src),
		CubeMobSize:    cubeSize,
		// VictimOnFire / AttackerLootingLevel / AttackerSmeltsLoot default to the v1 vanilla state
		// (false / 0 / false): no fire/effect/enchant subsystem is wired, so the gated furnace_smelt
		// and enchanted_count_increase are faithful no-ops. Structured to become real reads when those
		// subsystems land — never baked away.
	})

	for _, stack := range loot.Roll(tbl, seed, ctx) {
		if stack.Count <= 0 {
			continue // a looting/decay'd-to-zero stack (never in a v1 pig roll) — skip.
		}
		// Spawn the Item at the mob's vertical center (popResource spawns at the entity position; the
		// half-height centering mirrors block_drop.go's NewItemEntity drop). OWNER-region routing.
		ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, stack)
		t.regionForEntity(e).entities.add(ie)
	}
}

// dropMobExperience is the port of LivingEntity.dropExperience(ServerLevel, Entity) — the XP orb on
// a player kill:
//
//	if (wasExperienceConsumed()) return;                          // v1: false (never consumed)
//	if (isAlwaysExperienceDropper()                               // v1: false for a passive mob
//	    || (lastHurtByPlayerMemoryTime > 0 && shouldDropExperience() && gameRules.MOB_DROPS)) {
//	    int reward = getExperienceReward(level, attacker);        // = getBaseExperienceReward (xpReward)
//	    ExperienceOrb.award(level, position(), reward);
//	}
//
// The lastHurtByPlayerMemoryTime>0 gate is modeled by the player-attack proxy (killedByPlayer): there
// is no lastHurtByPlayerMemoryTime field on *Entity yet, so a direct player-attack death is the v1
// proxy for "recently hurt by a player" — documented, structured to become a real
// lastHurtByPlayerMemoryTime read later (never baked away). shouldDropExperience() (!isBaby) +
// MOB_DROPS are cited constant-true (no baby state, gamerule defaults true). The reward is the
// jar-verified per-type value (for a pig / any Animal: getBaseExperienceReward == 1 + random.nextInt(3)
// = 1-3, javap-cited in entityBaseExperienceReward). The orb spawns at the mob position in the OWNER
// region.
func (t *TickLoop) dropMobExperience(e *Entity, src damageSource) {
	// wasExperienceConsumed(): v1 false. isAlwaysExperienceDropper(): v1 false (no always-dropper mob).
	const wasExperienceConsumed = false
	const isAlwaysExperienceDropper = false
	if wasExperienceConsumed {
		return
	}

	// SCULK CATALYST (sculk_catalyst_be.go): the vanilla ENTITY_DIE game event fires from
	// LivingEntity.die regardless of the killer, and a SculkCatalystBlockEntity.CatalystListener within
	// listener range (8, BY_DISTANCE) CONSUMES the mob XP (skipDropExperience) after feeding it into
	// the catalyst SculkSpreader as spread charge. So before the orb path, offer the death to the
	// nearest in-range catalyst; if it handles it, no ExperienceOrb spawns (matching vanilla). The
	// reward draw + the addCursors are the CatalystListener.handleGameEvent body. CITE:
	// SculkCatalystBlockEntity.CatalystListener.handleGameEvent.
	if t.sculkCatalystOnEntityDie(e) {
		return
	}

	// The player-kill gate: lastHurtByPlayerMemoryTime>0 (the player-attack proxy) && shouldDropExperience
	// (!isBaby, cited true) && MOB_DROPS (gamerule, cited true).
	const shouldDropExperience = true
	const mobDropsGamerule = true
	if !isAlwaysExperienceDropper && !(killedByPlayer(src) && shouldDropExperience && mobDropsGamerule) {
		return // not killed by a player (or baby/gamerule off) — no XP, exactly as vanilla.
	}

	// getExperienceReward(level, attacker) = getBaseExperienceReward (EnchantmentHelper.processMobExperience
	// is a v1 no-op: no mob-XP enchant). The per-type base value is the jar-verified xpReward.
	reward := t.entityBaseExperienceReward(e)
	if reward <= 0 {
		return // ExperienceOrb.awardWithDirection returns immediately for value <= 0 (bytecode 0-1).
	}

	// ExperienceOrb.award(level, position, reward): split the reward into orb-sized chunks and spawn
	// each. Routed through the OWNER region (NOT cur()).
	t.awardExperienceOrbs(e, reward)
}

// killedByPlayer is the v1 proxy for net.minecraft.world.entity.LivingEntity.lastHurtByPlayerMemoryTime
// > 0 (the "recently hurt by a player" bit dropAllDeathLoot/dropExperience gate on). There is no
// lastHurtByPlayerMemoryTime field on *Entity yet, so a DIRECT player-attack death source is the v1
// proxy: the source is a player_attack with a real attacker id. This matches vanilla's behavior for a
// direct melee kill (the common case); the full lastHurtByPlayerMemoryTime window (a player hit N
// ticks before an environmental kill still credits the player) is DEFERRED — documented, structured so
// a real lastHurtByPlayerMemoryTime read replaces this with no caller change (CLAUDE.md: never bake
// the value away).
//
//	[CITED: LivingEntity.dropAllDeathLoot `flag = lastHurtByPlayerMemoryTime > 0`; dropExperience gate.]
func killedByPlayer(src damageSource) bool {
	return src.attacker != 0 && src.is("is_player_attack")
}

// entityBaseExperienceReward is the port of Mob.getBaseExperienceReward(ServerLevel) for the per-type
// base XP, with the equipment-bonus loop a v1 no-op (no mob equipment), so it returns the mob's
// xpReward directly. For an Animal (the pig), Animal OVERRIDES getBaseExperienceReward:
//
//	@Override protected int getBaseExperienceReward(ServerLevel level) { return 1 + this.random.nextInt(3); }
//	[VERIFIED javap net.minecraft.world.entity.animal.Animal.getBaseExperienceReward:
//	 iconst_1; aload_0 getfield random; iconst_3; nextInt(3); iadd; ireturn  => 1 + random.nextInt(3).]
//
// So a pig (and every Animal) drops 1 + nextInt(3) ∈ {1,2,3}. The draw is from the mob's RNG at the
// DEATH event (outside the 500-tick oracle window; the oracle pig is never killed). v1 has no other
// mob type with death, so the Animal value is the only live case; a non-Animal mob (none yet) would
// read its xpReward field (Mob.getBaseExperienceReward) — cited so a hostile's value slots in later.
//
// CITED STUB STRUCTURE: the xpReward field is not modeled on *Entity (the jar never assigns it for
// the pig — the Animal override supplies the value), so this is the faithful per-type read, not a
// baked constant: it computes 1 + nextInt(3) exactly as the jar, and a future per-type xpReward (for
// hostiles) extends the switch with no caller change.
func (t *TickLoop) entityBaseExperienceReward(e *Entity) int {
	// All v1 death-capable mobs are Animals (the pig and the egg-spawned passives), so the Animal
	// override applies: `1 + this.random.nextInt(3)`. The draw MUST come from the mob's OWN
	// RandomSource (the `this.random` field), NOT the process-global pool — `this.random.nextInt(3)`
	// is a draw on the per-entity stream (CLAUDE.md: mirror the RNG source/draw-order EXACTLY).
	// mobRandom(e) is the Mob.getRandom() analogue (e.ai.rng), nil-safe for a hand-built/AI-less mob.
	// The draw is at the DEATH event — outside the 500-tick oracle window (the oracle pig is never
	// killed), so it does not perturb TestPluginPigEqualsGoNativePig's pinned in-window stream.
	//
	//	[VERIFIED javap net.minecraft.world.entity.animal.Animal.getBaseExperienceReward:
	//	 iconst_1; aload_0 getfield random; iconst_3; invokeinterface RandomSource.nextInt:(I)I; iadd;
	//	 ireturn  => 1 + this.random.nextInt(3).]
	//
	// SLIME (Task): Slime does NOT override getBaseExperienceReward, so it uses Mob.getBaseExperienceReward
	// == this.xpReward (the equipment-bonus loop a v1 no-op). Slime.setSize sets xpReward = getSize(), so a
	// size-N slime drops N XP (NO nextInt draw -- do NOT perturb the mob RNG for a slime). Cite
	// Mob.getBaseExperienceReward + Slime.setSize (xpReward = getSize()).
	if e.isSlime {
		return int(e.slimeXpReward)
	}
	return 1 + mobRandom(e).nextInt(3)
}

// awardExperienceOrbs is the port of net.minecraft.world.entity.ExperienceOrb.award ->
// awardWithDirection(ServerLevel, Vec3, Vec3.ZERO, int): split a reward into orb-sized chunks (the
// getExperienceValue cap table) and spawn one ExperienceOrb per chunk at the mob position.
//
//	while (value > 0) {
//	    int chunk = getExperienceValue(value);   // 2477/1237/617/307/149/73/37/17/7/3/1 caps
//	    value -= chunk;
//	    if (!tryMergeToExisting(level, pos, chunk)) level.addFreshEntity(new ExperienceOrb(...chunk));
//	}
//	[VERIFIED javap ExperienceOrb.awardWithDirection: the while(value>0) split loop; getExperienceValue
//	 the descending-cap table.]
//
// v1 has no XP-orb merge (tryMergeToExisting is a stub returning false: no existing-orb merge subsystem),
// so each chunk spawns a fresh ExperienceOrb. The orb is a plain Entity spawned into the OWNER region
// (NOT cur()), where the tracker broadcasts its AddEntity — the same store-add path an Item drop rides.
// The orb's carried XP value (the SynchedEntityData DATA_VALUE) is a LATER metadata concern (no
// ExperienceOrb metadata wire-out yet); the orb spawns + renders, the value is cited to slot into the
// metadata when the orb's data accessor lands.
func (t *TickLoop) awardExperienceOrbs(e *Entity, value int) {
	owner := t.regionForEntity(e)
	for value > 0 {
		chunk := getExperienceValue(value)
		value -= chunk
		// tryMergeToExisting: v1 stub (false) — no orb-merge subsystem, so always spawn a fresh orb.
		orb := NewEntity(t.idAlloc.AllocID(), entity.ExperienceOrb, e.x, e.y, e.z)
		// WR-06: mark the orb so the orb tick (followNearbyPlayer + pickup) drives it, and record the
		// carried value (ExperienceOrb.value == this chunk) so playerTouchOrb awards getValue() == chunk
		// to the collector. Without these the orb spawns + renders but never follows/collects (the bug).
		orb.isOrb = true
		orb.xpValue = chunk
		orb.orbCount = 1 // ExperienceOrb.count default (1); scanForMerges combines equal-value orbs into it
		owner.entities.add(orb)
	}
}

// getExperienceValue is the port of ExperienceOrb.getExperienceValue(int): the descending cap table
// that splits a reward into the largest orb size <= value (so 1-3 XP is a single orb of that size).
//
//	[VERIFIED javap ExperienceOrb.getExperienceValue: if (value >= 2477) return 2477; >=1237 ->1237;
//	 >=617->617; >=307->307; >=149->149; >=73->73; >=37->37; >=17->17; >=7->7; >=3->3; else 1.]
func getExperienceValue(value int) int {
	switch {
	case value >= 2477:
		return 2477
	case value >= 1237:
		return 1237
	case value >= 617:
		return 617
	case value >= 307:
		return 307
	case value >= 149:
		return 149
	case value >= 73:
		return 73
	case value >= 37:
		return 37
	case value >= 17:
		return 17
	case value >= 7:
		return 7
	case value >= 3:
		return 3
	default:
		return 1
	}
}
