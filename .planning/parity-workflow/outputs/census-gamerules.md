# Census: gamerules (wave 00)

Read-only parity audit of every `GameRule` declared by the vanilla 26.2 JAR
(`temp/cache/26.2-inner.jar`, class `net.minecraft.world.level.gamerules.GameRules`)
versus `server/gamerules.go` and every production consumer in `server/`.

Test files (`server/*_test.go`) are excluded from "production consumer".

## Totals

| Source | Count |
| --- | ---: |
| JAR `public static final GameRule<T>` fields (`javap -p`) | **59** |
| Go rules registered in `newGameRules` (`server/gamerules.go:107-172`) | **59** (47 bool + 12 int) |
| Rules with a live-store consumer (`t.gameRule` / `t.gameRuleInt`) | **14** |
| Rules consumed via a hard-coded constant/var citing the vanilla default | **7** |
| Rules registered but unused in production (`absent`) | **38** |
| Net divergences (constant path co-existing with live path) | **1** (`MOB_GRIEFING` — silverfish) |
| Total boolean / int rules | **47 / 12** |

`MOB_GRIEFING` is counted under **live-store** for primary classification (8 call
sites read `t.gameRule(ruleMobGriefing)`), but it ALSO has a divergent
`silverfishMobGriefing` constant path. See "Critical consumer mismatches" below.

## Commands executed

```bash
# 1. JAR enumeration
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.gamerules.GameRules

# 2. Enumerate registrations in server/gamerules.go (booleans, ints, helpers)
Select-String -Path server/gamerules.go -Pattern '^\s+rule[A-Z]\w+\s+='
Select-String -Path server/gamerules.go -Pattern '^\s+rule[A-Z]\w+:'
Select-String -Path server/gamerules.go -Pattern 'newGameRules|gameRule|gameRuleInt|setBool|setInt'

# 3. Find every production consumer of every rule constant (excluding tests + gamerules.go)
rg -n '\b(rule[A-Z]\w+)\b' server --glob '!server/*_test.go' --glob '!server/gamerules.go'
# then narrow to live-store reads specifically
rg -n 'gameRule\(|gameRuleInt\(' server --glob '!server/*_test.go' --glob '!server/gamerules.go'

# 4. Find every gamerule-citing constant/var (failing live-store cases)
rg -n '\b(tntExplodes|vineSpreadVines|waterSourceConversion|lavaSourceConversion|silverfishMobGriefing|mobExplosionDropDecay|fallingBlockEntityDrops|fillMaxBlocks)\b' server --glob '!server/*_test.go'
```

## Complete table

Columns: **JAR field** is the `public static final GameRule<T>` field declared in
`net.minecraft.world.level.gamerules.GameRules`; **GO id** is the registered
constant in `server/gamerules.go:29-91`; **type** is `bool` or `int`; **default**
is the value pushed to the bool/int map in `newGameRules`
(`server/gamerules.go:110-156` booleans, `159-170` ints); **class** is one of:

| Class | Meaning |
| --- | --- |
| `live-store` | Read off `t.gameRule` / `t.gameRuleInt` against the loop's `gamerules` store at runtime. |
| `constant` | Hard-coded `var`/`const` (equals the vanilla default), cited against the JAR rule; `/gamerule` set does NOT flip it. |
| `absent`  | Registered as a default in `newGameRules`; no production code path reads it (test-only refs do not count). |
| `unknown` | Evidence is ambiguous; not used in this audit. |

| # | JAR field | GO id | Type | Default | Class | Evidence (production, excludes `_test.go` and `gamerules.go`) |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `ADVANCE_TIME` | `ruleAdvanceTime` | bool | true | live-store | `server/sleep.go:399`; `server/time.go:83` |
| 2 | `ADVANCE_WEATHER` | `ruleAdvanceWeather` | bool | true | live-store | `server/sleep.go:404`; `server/weather.go:129` (helper `advanceWeatherCycleGameRule`), `:156` (call site) |
| 3 | `ALLOW_ENTERING_NETHER_USING_PORTALS` | `ruleAllowNetherPortals` | bool | true | absent | — |
| 4 | `BLOCK_DROPS` | `ruleBlockDrops` | bool | true | absent | — |
| 5 | `BLOCK_EXPLOSION_DROP_DECAY` | `ruleBlockExplosionDecay` | bool | true | absent | cite-only: `server/bed_explode.go:23`, `server/bed_explode.go:131`; no implementation. |
| 6 | `COMMAND_BLOCKS_WORK` | `ruleCommandBlocksWork` | bool | true | absent | — |
| 7 | `COMMAND_BLOCK_OUTPUT` | `ruleCommandBlockOutput` | bool | true | absent | — |
| 8 | `DROWNING_DAMAGE` | `ruleDrowningDamage` | bool | true | absent | — |
| 9 | `ELYTRA_MOVEMENT_CHECK` | `ruleElytraMovementCheck` | bool | true | absent | — |
| 10 | `ENDER_PEARLS_VANISH_ON_DEATH` | `ruleEnderPearlsVanish` | bool | true | absent | — |
| 11 | `ENTITY_DROPS` | `ruleEntityDrops` | bool | true | constant | `var fallingBlockEntityDrops = true` at `server/falling_block.go:52`; cited uses `server/falling_block.go:256`, `:292`, `:300` |
| 12 | `FALL_DAMAGE` | `ruleFallDamage` | bool | true | absent | — |
| 13 | `FIRE_DAMAGE` | `ruleFireDamage` | bool | true | absent | — |
| 14 | `FIRE_SPREAD_RADIUS_AROUND_PLAYER` | `ruleFireSpreadRadius` | int | 128 | live-store | `server/lightning.go:131` (helper `doFireTickGameRule`); `:731` (call site at lightning strike). |
| 15 | `FORGIVE_DEAD_PLAYERS` | `ruleForgiveDeadPlayers` | bool | true | absent | — |
| 16 | `FREEZE_DAMAGE` | `ruleFreezeDamage` | bool | true | absent | — |
| 17 | `GLOBAL_SOUND_EVENTS` | `ruleGlobalSoundEvents` | bool | true | absent | — |
| 18 | `IMMEDIATE_RESPAWN` | `ruleImmediateRespawn` | bool | false | absent | — |
| 19 | `KEEP_INVENTORY` | `ruleKeepInventory` | bool | false | live-store | `server/death_player.go:69`, `:130`, `:148` |
| 20 | `LAVA_SOURCE_CONVERSION` | `ruleLavaSourceConversion` | bool | false | constant | `const lavaSourceConversion = false` at `server/fluid.go:84`; cited use `server/fluid.go:221` |
| 21 | `LIMITED_CRAFTING` | `ruleLimitedCrafting` | bool | false | absent | — |
| 22 | `LOCATOR_BAR` | `ruleLocatorBar` | bool | true | absent | — |
| 23 | `LOG_ADMIN_COMMANDS` | `ruleLogAdminCommands` | bool | true | absent | — |
| 24 | `MAX_BLOCK_MODIFICATIONS` | `ruleMaxBlockMods` | int | 32768 | constant | `const fillMaxBlocks = 32768` at `server/commands_batch.go:478`; cited uses `server/commands_batch.go:537`, `:538` |
| 25 | `MAX_COMMAND_FORKS` | `ruleMaxCommandForks` | int | 65536 | absent | — |
| 26 | `MAX_COMMAND_SEQUENCE_LENGTH` | `ruleMaxCmdSequenceLen` | int | 65536 | absent | — |
| 27 | `MAX_ENTITY_CRAMMING` | `ruleMaxEntityCramming` | int | 24 | live-store | `server/entity_collision.go:207` |
| 28 | `MAX_MINECART_SPEED` | `ruleMaxMinecartSpeed` | int | 8 | absent | — |
| 29 | `MAX_SNOW_ACCUMULATION_HEIGHT` | `ruleMaxSnowAccumulation` | int | 1 | absent | — |
| 30 | `MOB_DROPS` | `ruleMobDrops` | bool | true | live-store | `server/death_mob.go:410`; `server/ender_dragon.go:448` |
| 31 | `MOB_EXPLOSION_DROP_DECAY` | `ruleMobExplosionDecay` | bool | true | constant | `var mobExplosionDropDecay = true` at `server/explosion_blocks.go:35`; cited uses `:162`, `:197` |
| 32 | `MOB_GRIEFING` | `ruleMobGriefing` | bool | true | live-store | `server/explosion.go:46`; `server/ai_goals_enderman_carry.go:119`, `:182`; `server/ai_goals_evoker.go:367`; `server/ai_goals_ravager.go:122`, `:195`; `server/hurting_projectile.go:368`, `:427`; `server/snow_golem.go:176`; `server/wither.go:221` |
| 32' | `MOB_GRIEFING` (silverfish path) | — | bool | true | **constant (divergent)** | `const silverfishMobGriefing = true` at `server/silverfish_infest.go:45`; cited uses `server/ai_goals_silverfish.go:105`, `:313`. **Does not consult the gamerule store** — see Critical mismatches. |
| 33 | `NATURAL_HEALTH_REGENERATION` | `ruleNaturalRegen` | bool | true | live-store | `server/food.go:244` |
| 34 | `PLAYER_MOVEMENT_CHECK` | `rulePlayerMovementCheck` | bool | true | absent | — |
| 35 | `PLAYERS_NETHER_PORTAL_CREATIVE_DELAY` | `ruleNetherPortalCrDelay` | int | 0 | absent | — |
| 36 | `PLAYERS_NETHER_PORTAL_DEFAULT_DELAY` | `ruleNetherPortalDefDlay` | int | 80 | absent | — |
| 37 | `PLAYERS_SLEEPING_PERCENTAGE` | `ruleSleepPercent` | int | 100 | live-store | `server/sleep.go:395` |
| 38 | `PROJECTILES_CAN_BREAK_BLOCKS` | `ruleProjectilesBreak` | bool | true | absent | — |
| 39 | `PVP` | `rulePvP` | bool | true | absent | — |
| 40 | `RAIDS` | `ruleRaids` | bool | true | live-store | `server/raids.go:126` |
| 41 | `RANDOM_TICK_SPEED` | `ruleRandomTickSpeed` | int | 3 | live-store | `server/random_tick.go:93` |
| 42 | `REDUCED_DEBUG_INFO` | `ruleReducedDebugInfo` | bool | false | absent | — |
| 43 | `RESPAWN_RADIUS` | `ruleRespawnRadius` | int | 10 | absent | — |
| 44 | `SEND_COMMAND_FEEDBACK` | `ruleSendCommandFeedback` | bool | true | absent | — |
| 45 | `SHOW_ADVANCEMENT_MESSAGES` | `ruleShowAdvancementMsgs` | bool | true | absent | — |
| 46 | `SHOW_DEATH_MESSAGES` | `ruleShowDeathMessages` | bool | true | absent | — |
| 47 | `SPAWNER_BLOCKS_WORK` | `ruleSpawnerBlocksWork` | bool | true | absent | — |
| 48 | `SPAWN_MOBS` | `ruleSpawnMobs` | bool | true | live-store | `server/gamerules.go:218` (helper `isSpawningMonsters`); `server/lightning.go:135` (helper `spawnMobsGameRule`); `server/spawner.go:446` (gate). |
| 49 | `SPAWN_MONSTERS` | `ruleSpawnMonsters` | bool | true | live-store | `server/gamerules.go:218` (helper `isSpawningMonsters`); call site `server/spawner.go:499`. |
| 50 | `SPAWN_PATROLS` | `ruleSpawnPatrols` | bool | true | absent | — |
| 51 | `SPAWN_PHANTOMS` | `ruleSpawnPhantoms` | bool | true | absent | — |
| 52 | `SPAWN_WANDERING_TRADERS` | `ruleSpawnTraders` | bool | true | absent | — |
| 53 | `SPAWN_WARDENS` | `ruleSpawnWardens` | bool | true | live-store | `server/sculk_shrieker_be.go:309` |
| 54 | `SPECTATORS_GENERATE_CHUNKS` | `ruleSpectatorsGenChunks` | bool | true | absent | — |
| 55 | `SPREAD_VINES` | `ruleSpreadVines` | bool | true | constant | `const vineSpreadVines = true` at `server/vine.go:45`; cited use `server/vine.go:56` |
| 56 | `TNT_EXPLODES` | `ruleTNTExplodes` | bool | true | constant | `var tntExplodes = true` at `server/primed_tnt.go:69`; uses `server/primed_tnt.go:134`, `:239`; `server/dispense_behaviors.go:244`; `server/minecart.go:334`, `:373` |
| 57 | `TNT_EXPLOSION_DROP_DECAY` | `ruleTNTExplosionDecay` | bool | false | absent | cite-only in `server/gamerules.go:73` and `:105`; no implementation. |
| 58 | `UNIVERSAL_ANGER` | `ruleUniversalAnger` | bool | false | absent | cite-only at `server/ai_goals_target.go:866`; no implementation. |
| 59 | `WATER_SOURCE_CONVERSION` | `ruleWaterSourceConversion` | bool | true | constant | `const waterSourceConversion = true` at `server/fluid.go:78`; cited use `server/fluid.go:223` |

### Summary by class

| Class | Bool | Int | Total |
| --- | ---: | ---: | ---: |
| live-store | 10 | 4 | **14** |
| constant | 6 | 1 | **7** |
| absent | 31 | 7 | **38** |
| **Total** | **47** | **12** | **59** |

## Setter surface (informational, not part of the per-rule classification)

The `/gamerule` setter is wired through `commands_vanilla.go` and the
`gameRules.setBool / setInt` methods in `server/gamerules.go:181-194`. The
`setBool` / `setInt` paths only flip registered IDs (`unknown id -> no-op`),
so every registered rule IS settable at runtime, regardless of whether
production code reads it. See `server/gamerules_test.go:39-43` for the
"invented-id guard" lockstep test (`TestSetBoolIgnoresUnknownId` analogue).

The **`tntExplodes`** constant and the other `var` constants
(`fallingBlockEntityDrops`, `mobExplosionDropDecay`, `fillMaxBlocks`,
`silverfishMobGriefing`, `waterSourceConversion`, `lavaSourceConversion`,
`vineSpreadVines`) are NOT consulted by `setBool`/`setInt`. `/gamerule
<these rules> <value>` will return success and store the value but the
behavioral sites will not flip until those reads are converted. This is the
biggest practical fallout of the `constant`-classified rules.

## Critical consumer mismatches

These are the cases where the consumer-side code disagrees with what JAR
`GameRules.<clinit>` would mandate (`/gamerule <id> <value>` must observably
change gameplay). For each `constant` row above, the `/gamerule` command
appears to succeed but the cited constant is fixed at the vanilla default.
In a 1:1 port, ALL of the following MUST be flipped to `live-store` reads
before claiming parity. Listed in priority order (player-visible first):

1. **`MOB_GRIEFING` — silverfish path**
   `server/silverfish_infest.go:45` (`const silverfishMobGriefing = true`)
   plus the consumers at `server/ai_goals_silverfish.go:105` and
   `:313`. Every other producer of griefing behavior reads the live store
   (8 sites enumerated in the table above), so `/gamerule mob_griefing false`
   leaves silverfish in vanilla and any "everything off" test in the Go
   server will fail because silverfish continues to inflict/merge damage.
   Fix: either replace the constant with `t.gameRule(ruleMobGriefing)` at
   `:105` / `:313`, or have the silverfish callers consume the same helper
   that `explosion.go:46` uses.

2. **`ENTITY_DROPS` — entity-as-item form**
   `server/falling_block.go:52` (`var fallingBlockEntityDrops = true`),
   used at `:256`, `:292`, `:300`. Falling-block drop gating ignores the
   live store.

3. **`TNT_EXPLODES`** — `server/primed_tnt.go:69`
   (`var tntExplodes = true`), used at `server/primed_tnt.go:134`,
   `server/primed_tnt.go:239`, `server/dispense_behaviors.go:244`,
   `server/minecart.go:334`, `server/minecart.go:373`. Dispenser-tnt,
   primed-tnt prime/detonate, and tnt-in-minecart detonation paths all
   bypass the live store.

4. **`MOB_EXPLOSION_DROP_DECAY`** — `server/explosion_blocks.go:35`
   (`var mobExplosionDropDecay = true`), used at `:162`, `:197`. Selecting
   `DESTROY_WITH_DECAY` vs `DESTROY` from the live store is required for
   the EXPLOSION_RADIUS loot-context branch to be toggleable.

5. **`SPREAD_VINES`** — `server/vine.go:45`
   (`const vineSpreadVines = true`), used at `server/vine.go:56`. Random-tick
   vine spread is hardcoded.

6. **`WATER_SOURCE_CONVERSION` / `LAVA_SOURCE_CONVERSION`** —
   `server/fluid.go:78` (`const waterSourceConversion = true`),
   `server/fluid.go:84` (`const lavaSourceConversion = false`), used at
   `server/fluid.go:221`, `server/fluid.go:223`. Note LAVA defaults to
   **false** (per registration comment at `server/gamerules.go:47`); if
   the JAR's `registerBoolean("lava_source_conversion", ..., false)`
   iconst_0 ever drifts in a 26.x bump, this constant will silently
   diverge from the JAR.

7. **`MAX_BLOCK_MODIFICATIONS`** — `server/commands_batch.go:478`
   (`const fillMaxBlocks = 32768`), used at `:537`, `:538`. The
   `/fill ... 32769` block-volume rejection threshold is fixed; an op
   cannot raise or lower the cap at runtime.

The 38 `absent` rules are not currently observable to a player running the
Go server because the gameplay paths they would gate are themselves not
port-ed (redstone, command-blocks, weather timing paths, etc.). They are
NOT independently safe: as soon as a related subsystem lands, every
`absent` row above must be promoted to either `live-store` or `constant`,
never left `absent`. They are listed here so the next parity wave can
sequence their introductions without rediscovering them.

## Ledger row proposals (NOT applied — write target is `PARITY-LEDGER.csv`,
which is out of scope for this census)

Current ledger has a single aggregated row
`CORE-GAMERULES,game-rules,net.minecraft.world.level.gamerules.GameRules,
<registry>,exact,server/gamerules.go,...,59/59 registered; wave-00 rechecks
every consumer`. The wave-00 result does NOT warrant promoting the
aggregated `exact` status because the seven `constant` consumers enumerate
d above are NOT 1:1 with the JAR's live-store behavior (the registry is
exact, but the consumer wiring is not).

Proposed replacements / addenda (do NOT apply during this census):

1. **Demote** `CORE-GAMERULES` `exact` → `partial` and update the
   `notes` column to enumerate the 7 `constant` rows and the
   `MOB_GRIEFING` silverfish split. Suggested `notes` payload:

   ```text
   14 live-store, 7 constant (needs wire-up), 38 absent (subsystem-
   deferred). Critical: MOB_GRIEFING silverfish-path constant diverges
   from live path; see .planning/parity-workflow/outputs/census-
   gamerules.md.
   ```

2. **Add per-rule rows** so each consumer wave can claim precisely what
   it touched. Suggested shape (one row per rule id, all cite the same
   `jar_method = "<registry>"`):

   ```text
   CORE-GAMERULES-ADVANCE_TIME,game-rules,net.minecraft.world.level.gamerules.GameRules,ADVANCE_TIME,live-store,server/sleep.go:399+server/time.go:83,javap -p GameRules.ADVANCE_TIME field,<test>,no,<owner>,<commit>,registered + consumed; default matches jar
   CORE-GAMERULES-KEEP_INVENTORY,game-rules,net.minecraft.world.level.gamerules.GameRules,KEEP_INVENTORY,live-store,server/death_player.go:69+130+148,javap -p GameRules.KEEP_INVENTORY field,<test>,no,<owner>,<commit>,registered + consumed; default matches jar
   CORE-GAMERULES-MOB_GRIEFING,game-rules,net.minecraft.world.level.gamerules.GameRules,MOB_GRIEFING,partial,server/explosion.go:46+8 sibling sites ; sibling constant server/silverfish_infest.go:45,javap -p GameRules.MOB_GRIEFING field,<test>,no,<owner>,<commit>,live + constant divergent; silverfish must wire to live store
   CORE-GAMERULES-MOB_DROPS,... (live-store)
   CORE-GAMERULES-NATURAL_HEALTH_REGENERATION,... (live-store)
   CORE-GAMERULES-RAIDS,... (live-store)
   CORE-GAMERULES-SPAWN_MOBS,... (live-store)
   CORE-GAMERULES-SPAWN_MONSTERS,... (live-store via isSpawningMonsters)
   CORE-GAMERULES-SPAWN_WARDENS,... (live-store)
   CORE-GAMERULES-FIRE_SPREAD_RADIUS_AROUND_PLAYER,... (live-store)
   CORE-GAMERULES-MAX_ENTITY_CRAMMING,... (live-store)
   CORE-GAMERULES-PLAYERS_SLEEPING_PERCENTAGE,... (live-store)
   CORE-GAMERULES-RANDOM_TICK_SPEED,... (live-store)
   CORE-GAMERULES-ENTITY_DROPS,... (constant; server/falling_block.go:52 — must flip to live-store)
   CORE-GAMERULES-LAVA_SOURCE_CONVERSION,... (constant; server/fluid.go:84)
   CORE-GAMERULES-MOB_EXPLOSION_DROP_DECAY,... (constant; server/explosion_blocks.go:35)
   CORE-GAMERULES-SPREAD_VINES,... (constant; server/vine.go:45)
   CORE-GAMERULES-TNT_EXPLODES,... (constant; server/primed_tnt.go:69)
   CORE-GAMERULES-WATER_SOURCE_CONVERSION,... (constant; server/fluid.go:78)
   CORE-GAMERULES-MAX_BLOCK_MODIFICATIONS,... (constant; server/commands_batch.go:478)
   CORE-GAMERULES-BLOCK_EXPLOSION_DROP_DECAY,... (absent; cite-only server/bed_explode.go:23,131)
   CORE-GAMERULES-TNT_EXPLOSION_DROP_DECAY,... (absent)
   CORE-GAMERULES-UNIVERSAL_ANGER,... (absent; cite-only server/ai_goals_target.go:866)
   ... (one row per remaining absent rule, marked absent with the parent subsystem that must wire them)
   ```

   The exact set of per-rule rows is an integration-time decision; the
   table above IS the per-rule inventory, but the write target is out of
   scope for this census.

3. **No other `PARITY-LEDGER.csv` rows** are suggested by this census
   alone. The 38 `absent` rules are deliberately not promoted to their
   own rows because no implementation change occurs in this wave.

## Files referenced (read-only)

- `server/gamerules.go` (registry, `newGameRules`, `gameRule` / `gameRuleInt`
  helpers, `setBool` / `setInt` setters, `isSpawningMonsters` helper,
  `applyStringMap` / `toStringMap` persistence shape)
- `server/commands_vanilla.go:46-122` (`/gamerule` command + suggestion hook at
  `commands_suggest.go:64-68`)
- Production consumers (14 live-store + 7 constant + the 2
  divergent silverfish call sites): see "Evidence" column in the table.

## Out-of-scope acknowledgement

This task is read-only. The seven `constant`-class consumers are NOT
modified, the `PARITY-LEDGER.csv` row is NOT edited, no Go file is
edited, no commit is created. The report is the only artifact written.
