---
phase: 29-damage-keystone-s2
plan: 04
subsystem: combat-death-loot
tags: [combat, death, loot, xp, mob, jar-port, living-entity, mob-sub-01, keystone, folia]
requires:
  - phase: 29-damage-keystone-s2 (Plan 02)
    provides: "applyDamageEntity (LivingEntity.hurtServer port) with the dieEntity lethal tail seam; the *Entity hurt fields (health/lastHurt/invulnerableTime/hurtTime/lastDamageSource); damageSource value type + is(tag); damageSourcePlayerAttack"
  - phase: 29-damage-keystone-s2 (Plan 03)
    provides: "the real dieEntity caller (handleMobAttack -> applyDamageEntity drives mob health to 0); the cross-region barrier-queue; regionForEntity/owningRegion routing"
  - phase: 20-structure-polish (STRUCT-POLISH-01)
    provides: "the level/loot evaluator (LoadTable/Roll/NewLootContext); the loot model + parse + roll + condition + function dispatch; all 94 entity tables embedded"
  - phase: 06-entities (GAMEPLAY-06)
    provides: "NewItemEntity + entityStore.add/remove + the tracker AddEntity/RemoveEntities lifecycle (store-add auto-broadcasts AddEntity, store-remove auto-broadcasts RemoveEntities — A2)"
provides:
  - "server/death_mob.go: dieEntity (LivingEntity.die — death guard + dropAllDeathLoot + status-3 broadcast + owner-region store REMOVAL), dropAllDeathLoot, dropMobLoot (entity loot roll -> Item entities), dropMobExperience (XP orb on a player kill), killedByPlayer, entityBaseExperienceReward, awardExperienceOrbs, getExperienceValue"
  - "server/entity_encode.go: encodeEntityEvent + entityEventDeath (the status-3 death animation packet)"
  - "server/entity.go: the Entity.dead guard field (no double-death, T-29-08)"
  - "level/loot/context.go: EntityLootParams + NewEntityLootContext + the entity-context fields (KilledByPlayer/VictimOnFire/AttackerLootingLevel/AttackerSmeltsLoot)"
  - "level/loot/condition.go: any_of / all_of / killed_by_player / entity_properties handlers"
  - "level/loot/function.go: furnace_smelt + enchanted_count_increase handlers"
affects:
  - "Phase 31 PanicGoal (a mob that panics on hit can now actually DIE)"
  - "Phase 33 pig-parity gate (death + drops + XP are part of the full pig fidelity)"
  - "Phase 34/35/36 new mobs (every new mob's death rides dieEntity; a hostile's per-type xpReward extends entityBaseExperienceReward; the entity-loot context fields become real reads when fire/effect/enchant subsystems land)"
tech-stack:
  added: []
  patterns:
    - "death flow at the OWNER region (regionForEntity, NOT cur()): the loot/XP spawn + the mob removal all route through the victim's owning region (Pitfall 2) — the death path is NOT already in the owning region context"
    - "store-remove == despawn broadcast (A2): removing the dead mob from the store makes the tracker's near() drop it, auto-emitting RemoveEntities; no explicit despawn send needed"
    - "the A4 bounded entity-loot extension: the entity-table functions/conditions read cited-stub context fields at the vanilla v1 default (no fire/looting/smelts_loot), so the gated transforms are faithful no-ops and the unconditional pool rolls the correct raw drop — a real port, not a documented cut"
key-files:
  created:
    - "server/death_mob.go"
    - "level/loot/entity_loot_test.go"
    - "server/death_mob_test.go"
  modified:
    - "server/combat_mob.go (replaced the Plan-02 dieEntity seam — now in death_mob.go)"
    - "server/entity.go (the dead guard field)"
    - "server/entity_encode.go (encodeEntityEvent + entityEventDeath status 3)"
    - "level/loot/context.go (EntityLootParams + NewEntityLootContext + entity-context fields)"
    - "level/loot/condition.go (any_of/all_of/killed_by_player/entity_properties)"
    - "level/loot/function.go (furnace_smelt/enchanted_count_increase)"
key-decisions:
  - "A4 RESOLVED = bounded extension, NOT a cut: the entity TABLE type parses (ParseTable ignores the unused top-level type); only the pig pool's entity-context functions/conditions (any_of, entity_properties, furnace_smelt, enchanted_count_increase) needed handlers. They were ADDED reading cited-stub context fields at the v1 vanilla default — so the pig drops the CORRECT 1-3 raw porkchop, not an approximation."
  - "Pig xpReward = Animal.getBaseExperienceReward override == 1 + random.nextInt(3) (1-3), NOT a Mob xpReward field: javap-confirmed the jar NEVER assigns Pig/Animal/Mob.xpReward; Animal OVERRIDES getBaseExperienceReward to return 1 + random.nextInt(3). entityBaseExperienceReward ports that exact override (the only v1 death-capable mobs are Animals)."
  - "The death-status broadcast (status 3) ships BEFORE the store remove (players still track the mob this tick); the actual despawn rides the tracker's next-tick RemoveEntities from the store removal (A2 confirmed: the tracker diffs near() against p.tracked, so a removed entity auto-despawns)."
  - "killedByPlayer (the lastHurtByPlayerMemoryTime>0 gate) is the player-attack-source proxy: a direct player_attack death credits the kill. The full memory-time window (a player hit N ticks before an environmental kill) is the documented deferral, structured to become a real lastHurtByPlayerMemoryTime read."
requirements-completed: [MOB-SUB-01]

duration: ~28min
completed: 2026-06-29
---

# Phase 29 Plan 04: Mob Death + Loot + XP (Keystone Close) Summary

**A Go-native mob taken to 0 HP now DIES the vanilla way: it is removed from its owning region
store (the HARD deliverable, auto-broadcasting RemoveEntities), a death-status animation (status 3)
fires to every tracker, its loot table rolls into Item entities, and a player kill awards an XP orb
at the jar value (Animal.getBaseExperienceReward == 1 + random.nextInt(3)) — every method a 1:1
port of LivingEntity.die / dropAllDeathLoot / dropFromLootTable / dropExperience verified against
temp/cache/26.2-inner.jar, the loot/XP routed through the OWNER region (NOT cur()), Docker -race +
strictRegion clean, the pig oracle still GREEN. MOB-SUB-01 closed.**

## What Shipped

- **dieEntity (the HARD deliverable)** — the port of `LivingEntity.die(DamageSource)`: the death
  guard (`if (isRemoved() || dead) return; dead = true` — Sulfur's `dead` field covers both, T-29-08
  no double-death), then `dropAllDeathLoot` (loot + XP, BEFORE removal so drops spawn at the mob's
  still-current position), then `broadcastEntityEvent(this, 3)` (the death animation), then the
  REMOVAL from the OWNING region store via `regionForEntity(e).entities.remove` — NOT `cur()`
  (Pitfall 2: the death path is not already in the owning region context). The removal
  auto-broadcasts `RemoveEntities` (A2 confirmed: the tracker's `near()` no longer returns the gone
  id, so it is batched into the next-tick despawn).
- **dropMobLoot** — resolves the loot table name from the entity registry
  (`entity.ByID[e.typ].Name` -> `minecraft:entities/<name>`, the `Mob.getLootTable` ->
  `LivingEntity.getLootTable` default key), rolls it through the shared `level/loot` evaluator with a
  server-generated `rand.Int64()` seed (T-29-07, never client-supplied), and spawns one
  `NewItemEntity` per stack into the OWNER region — the same loot-roll -> Item-spawn pattern
  `block_drop.go` uses, re-routed for the death (barrier/owner) context.
- **dropMobExperience** — the port of `LivingEntity.dropExperience`: gated by the player kill
  (`killedByPlayer`, the v1 proxy for `lastHurtByPlayerMemoryTime > 0`) + `shouldDropExperience`
  (!isBaby) + MOB_DROPS (both cited constant-true). The reward is the jar pig value
  (`entityBaseExperienceReward` == `Animal.getBaseExperienceReward` == `1 + random.nextInt(3)` =
  1-3), split into orbs via the exact `ExperienceOrb.getExperienceValue` cap table, each orb spawned
  into the OWNER region.
- **The A4 bounded entity-loot extension** — `level/loot` now handles the entity-table
  functions/conditions: `any_of`/`all_of` (OR/AND composites), `killed_by_player`
  (`hasParameter(LAST_DAMAGE_PLAYER)`), `entity_properties` (is_on_fire on "this", smelts_loot on
  "direct_attacker"), `furnace_smelt` (a gated no-op), `enchanted_count_increase` (the looting
  bonus). They read the new `LootContext` entity-context fields, which carry cited-stub v1 defaults
  (no fire/looting/smelts_loot), so the pig's gated transforms are faithful no-ops and the
  unconditional `set_count[1,3]` pool rolls 1-3 RAW porkchop — the correct vanilla v1 drop.
- **encodeEntityEvent + the dead guard** — `ClientboundEntityEvent` (writeInt id + writeByte event)
  with `entityEventDeath = 3`, and the `Entity.dead` field guarding double-death.

## jar Verification (javap -c -p temp/cache/26.2-inner.jar, this session)

- `LivingEntity.die`: guard `isRemoved() || dead -> return`; `dropAllDeathLoot(level, source)` (162
  `iconst_3`); `Level.broadcastEntityEvent(this, 3)`; `setPose(DYING)`.
- `LivingEntity.dropAllDeathLoot`: `flag = lastHurtByPlayerMemoryTime > 0`; `if (shouldDropLoot)
  dropFromLootTable(level, src, flag) + dropCustomDeathLoot`; `dropEquipment`; `dropExperience(level,
  src.getEntity())`.
- `LivingEntity.dropFromLootTable(level, src, bool)`: `getLootTable().isEmpty() -> return`, else roll
  the ResourceKey table.
- `LivingEntity.dropExperience`: gate `!wasExperienceConsumed && (isAlwaysExperienceDropper ||
  (lastHurtByPlayerMemoryTime > 0 && shouldDropExperience() && MOB_DROPS))` -> `getExperienceReward
  -> ExperienceOrb.award(level, position, reward)`.
- `LivingEntity.getExperienceReward` = `EnchantmentHelper.processMobExperience(level, attacker, this,
  getBaseExperienceReward(level))` (the enchant step a v1 no-op).
- `Mob.getBaseExperienceReward`: `if (xpReward > 0) { sum xpReward + per-droppable-equipment-slot (1
  + nextInt(3)); } else return xpReward`. **`Animal` OVERRIDES it**: `getBaseExperienceReward` =
  `iconst_1; random.nextInt(3); iadd; ireturn` == `1 + random.nextInt(3)` (1-3). The jar NEVER
  assigns Pig/Animal/Mob.xpReward (confirmed via javap + CFR), so the Animal override IS the pig's
  death-XP source.
- `ExperienceOrb.award` -> `awardWithDirection`: the `while (value > 0) { chunk =
  getExperienceValue(value); value -= chunk; tryMergeToExisting || addFreshEntity(new
  ExperienceOrb(...chunk)) }` loop; `getExperienceValue` the descending cap table
  (2477/1237/617/307/149/73/37/17/7/3/1).
- `ClientboundEntityEventPacket.write`: `writeInt(entityId)` (a plain 4-byte Int, NOT a VarInt) +
  `writeByte(eventId)`.
- `SmeltItemFunction.run` (smelting-recipe replace) + `EnchantedCountIncreaseFunction.run`
  (`getEnchantmentLevel; if (level == 0) return; grow(round(level * count.getFloat))`) +
  `AnyOfCondition` (CompositeLootItemCondition OR) + `LootItemKilledByPlayerCondition.test`
  (`hasParameter(LAST_DAMAGE_PLAYER)`).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Death-test fixture id collided with the idAlloc-issued loot/orb ids**
- **Found during:** Task 0->2 (running TestMobDeath_Loot after dieEntity landed)
- **Issue:** The scaffold built the test mob with a LITERAL id (1), but `dieEntity` allocates the
  loot Item + XP orb ids from `t.idAlloc` (which starts at 0 -> first AllocID() == 1). The first
  loot Item therefore got id 1, OVERWROTE the mob in `entityStore.byID`, and the subsequent
  `entities.remove(mob.id == 1)` removed the ITEM instead of the mob — so the loot test saw 0 items.
- **Fix:** `lethalPigInRegion0` now draws the mob id from `loop.idAlloc.AllocID()`, exactly as
  production spawns every entity from the one shared id space — no collision with the death-flow ids.
  This is a TEST-FIXTURE fix; production was always correct (all entities draw from idAlloc).
- **Files modified:** server/death_mob_test.go
- **Verification:** all four TestMobDeath_* tests green; the removal/loot/XP no longer alias ids.
- **Committed in:** fed81f59 (with the death flow)

**Total deviations:** 1 auto-fixed (1 test-fixture bug). The implementation matched the plan: Task 0
de-risked A4 FIRST, Task 1 shipped death removal + broadcast, Task 2 the loot roll + the bounded
loot-context extension (A4 applied), Task 3 the XP orb + the full phase gate.

## A4 Outcome (the de-risk, recorded)

The headless `loot.Roll("minecraft:entities/pig")` (Task 0, run FIRST) RED'd on
`condition "minecraft:any_of" not ported`. So A4 = **the entity TABLE type parses (ParseTable
ignores the unused top-level "type"); only the pig pool's entity-context functions/conditions need
handlers** — a BOUNDED extension, not a large net-new subsystem. The handlers were added reading
cited-stub context fields at the vanilla v1 default, producing the CORRECT 1-3 raw porkchop. **The
loot deliverable shipped in full — no documented cut.**

## Verification

- `CGO_ENABLED=0 go build ./...` clean; `CGO_ENABLED=0 go vet ./...` clean.
- `CGO_ENABLED=0 go test ./level/loot/` green (TestEntityLootRoll + the golden seed-reproduction
  suite — the entity-context extension did NOT perturb the chest/block golden rolls).
- `CGO_ENABLED=0 go test ./server/ ./level/loot/ ./data/tag/` green (29.6s server) — death removal +
  loot + XP + the player-kill gate + tags.
- `TestPluginPigEqualsGoNativePig` **GREEN** — death is event-driven (a discrete lethal hit, never in
  the oracle pig's 500-tick window), so no AI RNG draw order shifted.
- **Docker -race over ./server/ ./level/loot/ CLEAN** (439s server, 2s loot) — no DATA RACE; the
  strictRegion-armed cross-region damage tests pass; the owner-routed death spawns/removal are
  race-clean by construction.
- `go list -deps ./... | grep -i gopy` EMPTY (no cgo / no new deps; CGO_ENABLED=0 preserved).
- Every ported death/loot/XP method cites its jar method; every stub equals the vanilla default and
  is structured to become a real read.

## Threat Model Coverage

- **T-29-07 (Elevation/Tampering — death-loot seed):** mitigated — the loot + XP draws are
  server-generated `rand.Int64()`/`rand.IntN(3)`, never client-supplied (same discipline as chest
  LootTableSeed, T-20-05).
- **T-29-02 (Tampering — death removal + drop spawn region routing):** mitigated —
  dieEntity/dropMobLoot/dropMobExperience all route through `regionForEntity(e)` (the owner), never
  `cur()`; Docker -race + strictRegion prove no cross-goroutine store mutation.
- **T-29-08 (DoS — double-death):** mitigated (guarded) — the `dead` guard makes a second die() a
  no-op (no double loot roll, idempotent removal); a re-entrant death is a defensive no-op.

## Known Stubs

All are cited at the vanilla default and structured to become real reads (CLAUDE.md mandate):

- **lastHurtByPlayerMemoryTime window** — `killedByPlayer` uses the direct player-attack-source proxy
  (credits a direct melee kill, the common case). The full memory-time window (a player hit N ticks
  before an environmental kill still credits the player) is deferred; a real `lastHurtByPlayerMemoryTime`
  read replaces the proxy with no caller change.
- **XP-orb carried value** — the spawned `experience_orb` renders + tracks, but its SynchedEntityData
  DATA_VALUE (the per-orb XP amount) is not wired (no ExperienceOrb metadata accessor yet); cited to
  slot in when the orb data accessor lands. The reward AMOUNT is correct (1-3), split into the right
  orb count via the exact cap table.
- **furnace_smelt** — a cited no-op: in v1 the wrapping any_of gate is false (pig not on fire, no
  smelts_loot enchant) so it is unreachable; the loot-time smelting-recipe lookup slots in with the
  gate already correct.
- **entity_properties / enchanted_count_increase context fields** — VictimOnFire/AttackerLootingLevel/
  AttackerSmeltsLoot default to the v1 vanilla state (false/0/false); when a fire/effect/enchant
  subsystem lands the death caller fills them with NO handler change.
- **setPose(DYING) / kill score / wither rose / dropCustomDeathLoot / dropEquipment** — v1 stubs at
  the vanilla no-op state (no mob pose metadata / scoreboard / wither / per-mob custom loot / mob
  equipment inventory). The death animation already plays from the status-3 broadcast.

None of these block the deliverable: death REMOVAL + the status-3 broadcast + the 1-3 porkchop drop +
the 1-3 XP orb on a player kill all ship and are tested.

## Next Phase Readiness

- The damage keystone is COMPLETE end-to-end: a player can hit a Go-native mob, drive its health to
  0, and watch it die, drop its loot, and award XP — same- or cross-region, race-clean.
- Phase 31 PanicGoal now has a mob that can actually die after panicking.
- Phase 33 pig-parity has death + drops + XP as part of the full fidelity.
- A new mob's death rides dieEntity unchanged; a hostile's per-type xpReward extends
  entityBaseExperienceReward; the entity-loot context fields become real reads when the
  fire/effect/enchant subsystems land.

## Self-Check: PASSED

- server/death_mob.go (func (t *TickLoop) dieEntity) — FOUND
- server/death_mob_test.go (TestMobDeath_Removal/_Loot/_XP) — FOUND
- level/loot/entity_loot_test.go (TestEntityLootRoll) — FOUND
- level/loot/context.go (NewEntityLootContext) — FOUND
- server/entity_encode.go (encodeEntityEvent) — FOUND
- commit bacf60fa (Task 0 de-risk + scaffold) — FOUND
- commit 1847d405 (loot-context extension) — FOUND
- commit fed81f59 (death flow) — FOUND

---
*Phase: 29-damage-keystone-s2*
*Completed: 2026-06-29*
