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
- **Raids** — Witch heal/regeneration branch (Raider target) + the raid subsystem (PatrollingMonster,
  NearestHealableRaiderTargetGoal). Also RestrictSun/FleeSun for the skeleton.
- **Rabbit hop** — RabbitJumpControl/RabbitMoveControl (the visible hop-vs-walk movement style) +
  RabbitPanicGoal.setSpeedModifier.
- **Prey mobs** — Endermite (enderman target), Turtle (fox/cat/skeleton target + eggs), Ocelot
  (creeper-avoid + OcelotAttackGoal), the Fox landTarget (chicken/rabbit).
- **Cat comfort** — CatRelaxOnOwnerGoal, CatLieOnBedGoal, CatSitOnBlockGoal, morning-gift, collar-dye.
- **Powder snow** — ClimbOnTopOfPowderSnowGoal (rabbit/silverfish/fox/creeper @1).
- **Silverfish infest** — SilverfishWakeUpFriendsGoal, SilverfishMergeWithStoneGoal (stone-infest blocks).
- **AvoidEntityGoal** — the generic flee goal (creeper←cat/ocelot, skeleton←wolf, rabbit/fox flees).

### Attribute/movement gaps
- **STEP_HEIGHT / SAFE_FALL_DISTANCE** attributes (enderman step 1.0, fox safe-fall 5.0) — currently not in
  the v1 attribute set; add them + wire the physics reads.

### Equipment / items (broad)
- Mob equipment (skeleton bow item, zombie/skeleton armor), the held-item subsystem the enderman
  block-carry + weapon goals need.

### Client-visual metadata (cite-deferred, behavior-neutral but wanted for fidelity)
- Creeper swell metadata (DATA_SWELL_DIR/POWERED/IGNITED), cat/wolf TamableAnimal DATA_FLAGS sit-pose +
  owner-UUID wire-out, zombie setAggressive raise-arm, arrow crit/owner spawnData.

(This list is the LIVING queue — as each mob header's DEFERRED notes close, cross them off. New deferrals
found during Phase A append here.)
