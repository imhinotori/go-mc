# FINAL MILESTONE — Full 1:1 Jar Parity → Clean-Room Delink

> **This is the FINAL milestone of the project.** It is NOT pinned to a version number (may or may not be
> "v6"). It ALWAYS sits LAST in the roadmap: whenever a new milestone is inserted, this one moves to the
> end. Nothing ships after it — it IS the finish line.

## The two phases

### Phase A — FULL 1:1 PARITY (follow Mojang exactly)
Every gameplay/protocol behavior a vanilla 26.2 client can observe is a LITERAL method-for-method port of
the unobfuscated jar (`temp/cache/26.2-inner.jar`), verified against bytecode BEFORE writing, citing the
class/method. This is the CURRENT discipline (the CLAUDE.md mandate) carried to completeness: every mob
goal, every subsystem, every cited-deferred stub becomes a real jar-faithful implementation. The ONLY
permitted deviation is OPTIMIZATION (a concurrency/perf change that provably preserves identical observable
gameplay — the Leaf-style async layered over the faithful logic).

**Exit criterion for Phase A:** ZERO cite-deferred stubs remain in the gameplay path. A vanilla client
cannot distinguish Ender from the reference server in any observable behavior (mob AI, physics, combat,
redstone, worldgen, effects, …). Every "DEFERRED (cite-recorded)" note in the codebase is closed.

### Phase B — CLEAN-ROOM DELINK (identical results, no EULA/GPL exposure)
Once Phase A is complete and the observable behavior is pinned by tests, REWRITE the ported logic so the
server no longer derives from Mojang's code — SAME observable results, but a clean-room re-derivation from
the BEHAVIORAL SPEC (the tests + the cited numeric constants), not from the jar bytecode. Goal: a server
that is legally independent (does not violate the Minecraft EULA / is not a derivative work of Mojang's
copyrighted code) while producing byte-identical gameplay.

- The Phase-A test suite (the pig oracle, the per-mob behavior tests, the physics/effect/explosion units)
  becomes the PARITY CONTRACT: Phase B must keep every test green.
- Re-derive each subsystem from the OBSERVABLE spec (inputs → outputs + the RNG draw order the tests pin),
  NOT by transcribing bytecode. The jar becomes a black-box reference to validate against, not a source.
- Stop shipping `temp/cache/26.2-inner.jar`-derived data where it is copyrightable; keep only what is
  factual/uncopyrightable (protocol wire ids, registry indices) or regenerate from clean sources.
- **This phase does not start until Phase A is DONE** (you cannot clean-room a behavior you have not yet
  fully pinned with tests).

## How this milestone is tracked
- It is the LAST entry in `.planning/ROADMAP.md`. When `/gsd-new-milestone` adds a milestone, MOVE this one
  back to the end (it is always the terminal milestone).
- Its phases = the remaining cite-deferred subsystems (Phase A) + the delink rewrite (Phase B).

## Phase-A work queue — the cite-deferred subsystems (from the mob headers + JARNOTES)
Each is a dedicated jar-faithful phase (javap-verify BEFORE writing, cite class/method):

### Mob-behavior subsystems
- **Gaze** — EndermanFreezeWhenLookedAt + EndermanLookForPlayerGoal (look-at-enderman aggro); needs a
  player-look-direction read. Unblocks the real enderman aggro (currently substituted with nearest-target).
- **Fox character layer** — FaceplantGoal, StalkPreyGoal, FoxPounceGoal, SeekShelterGoal, SleepGoal,
  FoxEatBerriesGoal, FoxSearchForItemsGoal, PerchAndSearchGoal, FoxStrollThroughVillageGoal, the trust
  system (DefendTrustedTargetGoal + AvoidEntityGoal player/wolf/bear).
- **Creeper block-destruction** — ServerExplosion.interactWithBlocks: the per-block ExplosionResistance
  table + the mobGriefing gamerule (ExplosionInteraction.MOB → KEEP when off). Explosion entity-damage is
  already done; this adds the block removal + drops.
- **Raids** — the raid EVENT subsystem (wave spawning, village detection, bad-omen, raid bar) +
  PatrollingMonster patrol (LongDistancePatrolGoal@4 + the patrol-leader spawn) + Witch.performRangedAttack's
  Raider heal-THROW branch (HEALING/REGENERATION at a hurt fellow raider). LANDED (skeleton-sun batch):
  RestrictSunGoal + FleeSunGoal for the skeleton (ai_goals_skeleton_sun.go — the avoid-sun pathfinding MALUS
  itself is still node-evaluator-deferred, see below); the Witch.aiStep self-drink potion buff (witchAiStep,
  WATER_BREATHING/FIRE_RESISTANCE/HEALING/SWIFTNESS on itself + the entity-side mobEffects slice); the
  NearestHealableRaiderTargetGoal STRUCTURE (registered + RNG-faithful, INERT until hasActiveRaid becomes a
  real read). REMAINING here: the raid EVENT + patrol + the heal-THROW (all need the raid subsystem).
- **Rabbit hop** — RabbitJumpControl/RabbitMoveControl (the visible hop-vs-walk movement style) +
  RabbitPanicGoal.setSpeedModifier.
- **Prey mobs** — Endermite (enderman target), Turtle (fox/cat/skeleton target + eggs), Ocelot
  (creeper-avoid + OcelotAttackGoal), the Fox landTarget (chicken/rabbit).
- **Cat comfort** — CatRelaxOnOwnerGoal, CatLieOnBedGoal, CatSitOnBlockGoal, morning-gift, collar-dye.
- **Powder snow** — ClimbOnTopOfPowderSnowGoal (rabbit/silverfish/fox/creeper @1).
- **Silverfish infest** — SilverfishWakeUpFriendsGoal, SilverfishMergeWithStoneGoal (stone-infest blocks).
- **AvoidEntityGoal** — the generic flee goal (creeper←cat/ocelot, skeleton←wolf, rabbit/fox flees).

### Happy Ghast subsystems (from vanilla_happy_ghast, Task)
- **Brain subsystem** — the entity Brain framework (memory modules, sensors, activities, behaviors). The
  HappyGhast is the first brain-based mob: HappyGhastAi drives the BABY (customServerAiStep ticks the brain
  only while isBaby). Sensors NEAREST_LIVING_ENTITIES/HURT_BY/FOOD_TEMPTATIONS/NEAREST_ADULT_ANY_TYPE/
  NEAREST_PLAYERS + the activity/behavior tree. v1 SUBSTITUTES the baby brain with the same classic
  goalSelector the adult runs (both baby+adult registerGoals: HappyGhastFloatGoal@3 + TemptGoal@4 +
  Ghast.RandomFloatAroundGoal@5). This subsystem, once built, unblocks every future brain mob (villager,
  piglin, axolotl, allay, warden, etc.), not just the happy ghast.
- **Ride / harness (happy ghast)** — doPlayerRide/startRiding, MAX_PASSANGERS 4 (canAddPassenger),
  getRiddenInput/getRiddenRotation/tickRidden (the mounted flight controls), isFlyingVehicle,
  getDismountLocationForPassenger; canUseSlot(BODY)=adult-only harness equip + canDispenserEquipIntoSlot,
  the goggles-up/down harness states (lang subtitles happy_ghast.harness_goggles_up/down + equip/unequip).
  Needs a riding/vehicle subsystem (none in v1).
- **Dried-ghast rehydration spawn** — the happy ghast is NOT a natural mob-spawn; it hatches from a dried
  ghast block rehydrated in water. Needs a dried-ghast block + the rehydration timer (v1 spawns via /dbg or
  the spawn egg only; the mob stays OUT of the natural pool).
- **Happy ghast ambient upkeep** — continuousHeal (heal 1 every 20t in clouds/rain else 600t; needs an
  isInClouds + precipitationAt read), checkRestriction/home-radius (32/64-block leash to a home), serverStill
  Timeout (setRequiresPrecisePosition + scanPlayerAboveGhast), leash-holder (IS_LEASH_HOLDER). The
  GhastMoveControl.canReach careful-mode AABB traversal + the HAPPY_GHAST_AVOIDS block tag (v1 uses a
  reduced destination-air reach check).
- **FLYING_SPEED / CAMERA_DISTANCE attributes** — ADDED this task (level/attribute/attributes.go). The
  happy ghast flight reads FLYING_SPEED; when riding lands, wire the mounted camera to CAMERA_DISTANCE.

### Attribute/movement gaps
- **STEP_HEIGHT / SAFE_FALL_DISTANCE** attributes (enderman step 1.0, fox safe-fall 5.0) — currently not in
  the v1 attribute set; add them + wire the physics reads.

### Equipment / items (broad)
- **LANDED (mob equipment subsystem — the held-item/armor slots):** EntityEquipment storage on Entity
  (the [8]SlotData ordinal-indexed EnumMap analogue), getItemBySlot/setItemSlot/getMainHandItem/
  getOffhandItem/isHoldingItem (LivingEntity accessors), the skeleton's spawn-time MAINHAND bow
  (AbstractSkeleton.populateDefaultEquipmentSlots), the spawn-time ClientboundSetEquipment wire (a client
  SEES the bow), and RangedBowAttackGoal.isHoldingBow now backed by the real held item. (server/
  entity_equipment.go, server/entity.go equipment field, spawnDeclaredMob skeleton gate, tracker wire.)
- STILL DEFERRED (equipment):
  - Mob.populateDefaultEquipmentSlots for the OTHER mobs (zombie/skeleton ARMOR, the full 6-slot
    population + the difficulty-scaled armor roll) and populateDefaultEquipmentEnchantments (the
    armor-enchant RNG draw). Only the skeleton's unconditional RNG-free bow lands so far.
  - getDropChances / DropChances + the on-death equipment drop roll (dropEquipment) — the storage lands,
    the death-drop of a held/worn item does not.
  - The LIVE mob-side equipment SWAP broadcast (detectEquipmentUpdates per-tick compare for a mob) — the
    spawn-time SetEquipment lands; a runtime equip CHANGE reuses encodeSetEquipment when a swap path exists.
  - The enderman block-carry HELD-ITEM (carriedBlockState → getItemBySlot) — the enderman uses the same
    equipment surface once its carry state feeds a slot.

### Client-visual metadata (cite-deferred, behavior-neutral but wanted for fidelity)
- Creeper swell metadata (DATA_SWELL_DIR/POWERED/IGNITED), cat/wolf TamableAnimal DATA_FLAGS sit-pose +
  owner-UUID wire-out, zombie setAggressive raise-arm, arrow crit/owner spawnData.

(This list is the LIVING queue — as each mob header's DEFERRED notes close, cross them off. New deferrals
found during Phase A append here.)

### Appended (skeleton-sun batch — new deferrals surfaced while landing the sun/heal goals)
- **Avoid-sun pathfinding malus** — RestrictSunGoal.setAvoidSun flips groundNavigation.avoidSun faithfully,
  but the WalkNodeEvaluator sun-exposed MALUS (the actual pathfinding bias that routes a day-time skeleton
  through shade) is node-evaluator-deferred (no path-malus subsystem in v1). Sibling of the canFloat
  float-pathing deferral. Also FleeSunGoal/RestrictSunGoal's getItemBySlot(HEAD).isEmpty() is a cited
  constant-true (no mob equipment slots) — closes with the mob-equipment subsystem.
- **Witch drink client feedback** — SoundEvents.WITCH_DRINK / WITCH_THROW + the broadcastEntityEvent(15)
  idle-particle (the RNG draw fires; the client broadcast is deferred). Closes with a sound/particle bus.
- **Witch self-buff subsystem reads** — SPEED (SWIFTNESS) MOVEMENT_SPEED buff, WATER_BREATHING (air), and
  FIRE_RESISTANCE (fire immunity) effects are attached + ticked + hasEffect-visible on the witch, but their
  server-side observable action is movement/breath/fire-subsystem-deferred (sibling of the player-side
  slowness no-op). The effect PRESENCE (the ladder's !hasEffect gate) is faithful now.

## RAID event subsystem (raid.go / raids.go / raid_tick.go / ai_goals_patrol.go)

The Raid EVENT core landed 1:1 from `net.minecraft.world.entity.raid.Raid` / `.Raids` /
`PatrollingMonster$LongDistancePatrolGoal` (VERIFIED CFR): the wave schedule (numGroups PEACEFUL 0 /
EASY 3 / NORMAL 5 / HARD 7), the RaiderType.spawnsPerWaveBeforeBonus table (ordinals VINDICATOR 0 /
EVOKER 1 / PILLAGER 2 / WITCH 3 / RAVAGER 4), getPotentialBonusSpawns (RNG-draw-faithful), the 300-tick
pre-wave cooldown countdown, shouldSpawnGroup / spawnGroup / joinRaid / updateRaiders / removeFromRaid,
the ONGOING->VICTORY (final wave + 40 postRaidTicks) / STOPPED (48000 timeout) transitions, and the
ServerBossEvent MODEL (progress/name/visible). `Raider.hasActiveRaid()` is now a REAL query
(mobAI.currentRaid back-pointer), so the witch's NearestHealableRaiderTargetGoal proceeds past the
hasActiveRaid gate + runs its real findTarget. A raid is startable via the `/dbg raid` + `createRaidAt`
SEAM and the raid loop ticks on the coordinator at the quiescent barrier (raidsTickAllRegions).

Deferrals (cited, NOT silently dropped):
- **Bad-omen -> raid AUTO-trigger (`Raids.createOrExtendRaid`)** — reads
  `level.getPoiManager().getInRange(VILLAGE)`; there is NO village/POI subsystem (grep PoiManager: zero
  hits) and NO BAD_OMEN/RAID_OMEN mob effects. Replaced by the `createRaidAt` test/dbg SEAM so the raid
  loop is fully exercised + hasActiveRaid is real+testable. Closes with the POI/village + omen-effect subsystems.
- **Village center guards in Raid.tick** — `isVillage(center)` / `moveRaidCenterToNearbyVillageSection` /
  the "drifted out of a village -> LOSS" branch are POI-deferred; the raid holds its fixed center.
- **Absent RaiderType mobs (Pillager/Vindicator/Evoker/Ravager)** — only WITCH exists among the 22 merged
  mobs. spawnGroup computes their counts faithfully but `raidCreateRaider` returns nil for them (the jar's
  `EntityType.create(...) == null` loop-guard behavior), so no mob spawns. The ravager-rider spawns
  (Pillager/Evoker/Vindicator riding a Ravager) + the leader ominous banner (setLeader) are deferred with
  them. In a witch-only wave the witch's heal subselector `!target.is(WITCH)` correctly excludes every
  raider (findTarget acquires nothing) — 1:1 with vanilla for that roster.
- **findRandomSpawnPos heightmap/village probing** — reduced to the raid center (spawnGroup places raiders
  at center+0.5). The observable (a wave of witches spawns near center) is faithful.
- **ServerBossEvent CLIENT WIRE** — there is NO ClientboundBossEventPacket in net/packet (grep: zero
  hits). The bar MODEL (progress/name/visible/players) is computed 1:1; its packet emission closes when a
  BossEvent wire lands (pure add-on).
- **Raids SavedData persistence + RAIDS gamerule** — the per-raid tick + STOPPED prune are faithful; the
  Codec/SavedData disk persistence + the RAIDS gamerule short-circuit are reduced (no gamerule/SavedData
  subsystem).
- **Hero-of-the-village grant + raid horn sound** — the VICTORY path's HERO_OF_THE_VILLAGE effect grant +
  RAID_HORN ClientboundSoundPacket are deferred (no player-hero tracking / sound bus).
- **PatrollingMonster.LongDistancePatrolGoal (`kind="long_distance_patrol"`)** — a COMPLETE, testable port
  (canUse gate + the yRot(90)*0.4 long-distance step + moveRandomly + findPatrolTarget + the nav-fail
  200-tick cooldown), registered via buildNativeGoal. STRUCTURALLY REAL but INERT: no PatrollingMonster
  mob exists among the 22 (all RaiderTypes but WITCH are absent), so no mob declares it and a v1 mob is
  never isPatrolling(). The leader companion-drag (findPatrolCompanions) is broad-phase-deferred; a lone
  patroller drops patrolling (the faithful no-companion branch). Becomes live when a PatrollingMonster mob lands.

## POI (Point-of-Interest) subsystem + bad-omen->raid auto-trigger (poi.go)

Landed 1:1 from the 26.2 jar (javap/CFR): the PoiManager storage (per-region, per-section
`records[SectionPos.sectionRelativePos]`), the PoiRecord ticket/occupancy system (freeTickets starts at
maxTickets; acquire/release; hasSpace=free>0; isOccupied=free!=max), the Occupancy enum (HAS_SPACE/
IS_OCCUPIED/ANY), the block-state->PoiType map (`PoiTypes.forState`), and the queries
(getInSquare/getInRange/getCountInRange/findClosest). Village machinery: isVillageCenter (a section with an
IS_OCCUPIED #village POI), sectionsToVillage (Chebyshev distance to the nearest such section, cap
MAX_VILLAGE_DISTANCE=6), isVillage (`ServerLevel.isVillage` = sectionsToVillage<=1). POI types landed: HOME
(beds, maxTickets 1, validRange 1) and MEETING (bell, 32, 6) — VERIFIED `PoiTypes.bootstrap`. The
block-change hook `updatePoiOnBlockStateChange(pos,old,new)` (`ServerLevel.updatePOIOnBlockStateChange`) is
wired into the block PLACE (block_interact.go) and BREAK (block_break.go) seams. The bad-omen->raid
AUTO-trigger is now REAL, replacing the /dbg-only createRaidAt seam: BadOmenMobEffect (in a village +
non-peaceful + no raid at max omen -> convert to RAID_OMEN, record raidOmenPosition) -> RaidOmenMobEffect
(on its final tick -> createOrExtendRaid) -> `Raids.createOrExtendRaid` (getInRange(#village, pos, 64,
IS_OCCUPIED) -> average POI positions for the center, else raidPosition; getOrCreateRaid; absorbRaidOmen).
The /dbg raid createRaidAt seam is KEPT (a manual, POI-independent trigger).

### Cite-deferred (POI)
- **Villager JOB-SITE POI types** (`ARMORER..WEAPONSMITH`, the `#acquirable_job_site` members —
  composter/lectern/barrel/... block sets) — omitted from `poiTypeForState` / `poiTypeVillage`. No
  villagers exist to claim job sites; the two VILLAGE-tag members the raid center query needs (HOME/MEETING)
  are landed. Slots in at `poiTypeForState` + `poiTypeVillage` (`#acquirable_job_site` branch) when villager
  POIs land. VERIFIED `registrydata/tags/point_of_interest_type/village.json` =
  {#acquirable_job_site, home, meeting}.
- **The rest of `PoiTypes`** (BEEHIVE/BEE_NEST/NETHER_PORTAL/LODESTONE/LIGHTNING_ROD/TEST_INSTANCE and the
  cauldron LEATHERWORKER) — not village-relevant; omitted from the type table.
- **Villager bed/bell CLAIMING** (the acquireTicket path that makes a HOME/MEETING POI IS_OCCUPIED) — no
  villagers yet, so in a live world NO bed/bell ever becomes occupied, so `isVillage` is always false and
  the bad-omen chain never spontaneously fires in-world (a bad-omen player over an UNCLAIMED bed-village
  does nothing — faithful to vanilla, where a village requires occupied POIs). The occupancy math + the
  full trigger chain are exercised in poi_test.go by simulating a villager claim (`acquireTicket`). The
  chain becomes spontaneously live when villagers land and claim beds/bells.
- **PoiManager.DistanceTracker (SectionTracker) incrementality** — replaced by a lazily-recomputed
  `villageDist` cache cleared on any village-POI add/remove. This is an OPTIMIZATION/reduction: the
  observable result (min Chebyshev section distance to an occupied #village section, cap 6) is identical.
- **SectionStorage disk persistence (poi/*.mca)** — POI is rebuilt from runtime block edits; no POI region
  file is written/read yet (no villager memory to persist). The PoiRecord `setDirty` runnable is reduced.
- **createOrExtendRaid guards**: the RAIDS gamerule + `EnvironmentAttributes.CAN_START_RAID` + isSpectator()
  checks are const-true/false analogues (no gamerule/dimension-attribute/spectator subsystem); a live
  non-peaceful world always passes. `absorbRaidOmen`'s `awardStat(RAID_TRIGGER)` + `CriteriaTriggers.RAID_OMEN`
  are deferred (no stats/advancement subsystem); the omen-level math is faithful.
