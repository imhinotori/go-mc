# Roadmap: Sulfur — Minecraft Java 26.2 (protocol 776) server

## Milestones

- ✅ **v1.0 MVP** — Phases 1–9 (shipped 2026-06-24) — full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied noise world with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md)
- ✅ **v2.0 Worldgen Features + Structures** — Phases 10–16 (shipped 2026-06-25) — the overworld now looks + reads like vanilla: full per-biome vegetation (trees/grass/flowers/ores) + the emblematic structures (temples, mineshafts, strongholds, villages) in vanilla positions, deterministic per seed, ported 1:1 from the jar. → [archive](milestones/v2-ROADMAP.md) · [requirements](milestones/v2-REQUIREMENTS.md)
- ✅ **v3 Online-mode + Operator UX + Structure polish** — Phases 17–20 (shipped 2026-06-27) — the world became *actually* playable on a real client (the six unwired gameplay seams), then authenticated/encrypted online-mode logins, a bubbletea TUI console + disconnect logging, the structures finished (loot, inhabitants, terrain-beard, NBT persistence), and a live 12-bug fidelity sweep (chest/water/cane/hats/persist/swim/oxygen) all operator-confirmed in-game. REGION-01 Folia regionization deferred to v4. → [archive](milestones/v3-ROADMAP.md) · [requirements](milestones/v3-REQUIREMENTS.md) · [audit](milestones/v3-MILESTONE-AUDIT.md)
- ✅ **v4 Plugin / Scripting System** — Phases 21–28 (shipped 2026-06-29) — a dual-runtime extension API: **Starlark** (`go.starlark.net`, pure-Go, CGO=0 preserved, sandboxed) as the hot-path core + an **opt-in Python** runtime (`qur/gopy` @ `python3.14`, behind a `python` build tag so the default binary stays pure-Go static). Plugins DECLARE behavior loaded once; Go runs the hot path calling declared hooks. Dogfood-validated: vanilla pig rewritten AS a 1:1-jar plugin (behavior-identical to the Go oracle, the only pig) + crafting built THROUGH the plugin API (recipe-provider + 3×3 menu). Folia regionization (REGION-01) folded in (2 parallel regions, -race clean). Closed by a bot-driven visual gate + an automated perf gate (plugin tax ~1.7–4.2%/mob·tick). 8/8 reqs, 7/7 integration chains, 4/4 gate flows. → [archive](milestones/v4-ROADMAP.md) · [requirements](milestones/v4-REQUIREMENTS.md) · [audit](milestones/v4-MILESTONE-AUDIT.md)
- 🚧 **v5 Mob Behaviors & Living-Entity Subsystems** — Phases 29–36 (started 2026-06-29) — "finish porting the jar" into the mob layer. Builds the four deferred living-entity subsystems 1:1 from the unobfuscated 26.2 jar — (S2) the mob damage/hurt pipeline + `lastDamageSource` + damage-type tags (the keystone, widest fan-out), (S1) mob JumpControl + fluid detection, (S4) held-item read + item tags, (S3) animal aging + breeding — finishes the pig's 5 deferred goals (FloatGoal/PanicGoal/Tempt×2/BreedGoal/FollowParentGoal) onto the existing `vanilla_pig` plugin (the **8-goal pig-oracle dogfood gate**), then adds passive (cow/sheep/chicken), hostile (zombie/skeleton/spider + spawn rules), and neutral (wolf) mobs as jar-faithful Starlark plugins. No new Go deps, no cgo (one new codegen tag-extractor adds jar DATA, not a lib); CGO=0 + Docker `-race` + the bit-fragile pig oracle GREEN are standing constraints. 21 MOB-* requirements.

## Phases

<details>
<summary>✅ v1.0 MVP (Phases 1–9) — SHIPPED 2026-06-24</summary>

See [milestones/v1.0-ROADMAP.md](milestones/v1.0-ROADMAP.md) for full phase details.

</details>

<details>
<summary>✅ v2.0 Worldgen Features + Structures (Phases 10–16) — SHIPPED 2026-06-25</summary>

- [x] **Phase 10: Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap** (3/3 plans) — completed 2026-06-25
- [x] **Phase 11: Feature Pipeline & Decoration Orchestration** (3/3 plans) — completed 2026-06-25
- [x] **Phase 12: Core Feature Types** (3/3 plans) — completed 2026-06-25
- [x] **Phase 13: Trees, Dungeon & Features Visual Gate** (4/4 plans) — completed 2026-06-25 (visual gate approved: trees + vines on a real client)
- [x] **Phase 14: Structure Pipeline & Temples** (3/3 plans) — completed 2026-06-25
- [x] **Phase 15: Mineshaft & Stronghold** (3/3 plans) — completed 2026-06-25
- [x] **Phase 16: Village Jigsaw & Structures Visual Gate** (3/3 plans) — completed 2026-06-25 (visual gate approved 2026-06-25, seed 25: villages + temples + mineshafts + strongholds reproduce per seed)

Full phase details: [milestones/v2-ROADMAP.md](milestones/v2-ROADMAP.md).

</details>

<details>
<summary>✅ v3 Online-mode + Operator UX + Structure polish (Phases 17–20) — SHIPPED 2026-06-27</summary>

- [x] **Phase 17: Gameplay Completion — the six unwired seams** (GAMEPLAY-01..07) — wired the existing-but-disconnected gameplay: player-entity-in-tracker broadcast (GAMEPLAY-01, the keystone), persisted-position apply (02), inventory join-sync (03), Attack/Interact damage dispatch + fall damage (04), fluid simulation + player fluid physics (05), block-break item drops (06), closed by a real-client multiplayer VISUAL GATE (07, operator-confirmed live). (completed 2026-06-27)
- [x] **Phase 18: Online-mode — auth + protocol encryption** (ONLINE-01/02) — EncryptionRequest/Response RSA key exchange + AES-128/CFB8 stream encryption (hand-rolled CFB8 over stdlib AES, no new dep) + Yggdrasil `hasJoined` session-server verification, behind an `online-mode` config flag. (completed 2026-06-26)
- [x] **Phase 19: Operator UX — TUI console + disconnect logging** (TUI-01/02) — a bubbletea+bubbles terminal console (command-input + live log viewport) that degrades to plain logging when stdout is not a TTY, plus disconnect-reason logging (kick/timeout/protocol/quit/login-fail).
 Both complete + operator-validated; verified 3/3. (completed 2026-06-27)
- [x] **Phase 20: Structure polish — loot, inhabitants, beard, persistence** (STRUCT-POLISH-01..04) — the documented v2 deferrals: loot tables (chests + block drops, shared evaluator with GAMEPLAY-06), structure entities (villagers/witch/cat/silverfish), `afterPlace` terrain-beard, and structure-start NBT persistence.
 (completed 2026-06-27)

Full phase details: [milestones/v3-ROADMAP.md](milestones/v3-ROADMAP.md).

</details>

<details>
<summary>✅ v4 Plugin / Scripting System (Phases 21–28) — SHIPPED 2026-06-29</summary>

- [x] **Phase 21: Starlark runtime foundation** (PLUGIN-01) — completed 2026-06-28
- [x] **Phase 22: Plugin host + event bus** (PLUGIN-02) — completed 2026-06-28
- [x] **Phase 23: Entity/mob behavior API** (PLUGIN-03) — completed 2026-06-28
- [x] **Phase 24: Vanilla mobs AS plugins (1:1 dogfood)** (PLUGIN-04) — completed 2026-06-28
- [x] **Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood)** (PLUGIN-05) — completed 2026-06-28
- [x] **Phase 26: Opt-in Python runtime** (PLUGIN-06) — completed 2026-06-28
- [x] **Phase 27: Folia regionization** (REGION-01) — completed 2026-06-28
- [x] **Phase 28: Plugin system visual + perf gate** (PLUGIN-07) — completed 2026-06-29

Full phase details: [milestones/v4-ROADMAP.md](milestones/v4-ROADMAP.md).

</details>

### 🚧 v5 Mob Behaviors & Living-Entity Subsystems (Phases 29–36) — IN PROGRESS

- [x] **Phase 29: Damage Keystone (S2)** — the parallel `*Entity` mob damage/hurt pipeline + `lastDamageSource` + jar-extracted damage-type tags; the widest fan-out, built first.
 (completed 2026-06-29)
- [x] **Phase 30: JumpControl + Fluid (S1)** — mob `JumpControl` impulse + `*Entity` fluid predicates; FloatGoal@0 on the pig (oracle + plugin in lockstep).
 (completed 2026-06-29)
- [x] **Phase 30.1: Faithful RandomStrollGoal Target Selection (BUGFIX)** — re-port `LandRandomPos.getPos` (best-of-10 weighted + `generateRandomDirection` x/y/z draw order + `movePosUpOutOfSolid` ground-snap + `isStableDestination`/water/malus rejection) so the pig's stroll target is always reachable; fixes the "walk a little then jam against a block forever" wedge. RNG-sensitive → lockstep `newPigAI` + both `vanilla_pig/main.star`.
 (completed 2026-06-30)
- [x] **Phase 31: PanicGoal (S2 consumer)** — PanicGoal@1 on the pig reading the real `lastDamageSource` + the panic-causing tag set.
 (completed 2026-06-30)
- [x] **Phase 32: Held-Item + Item Tags (S4)** — nearest-player held-item read + jar-extracted item-food tags; TemptGoal@4 ×2 on the pig.
 (completed 2026-06-30)
- [x] **Phase 33: Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE** — animal aging + breeding; BreedGoal@3 + FollowParentGoal@5 on the pig closes the full 8-goal oracle. HARD GATE before any new mob.
 (completed 2026-06-30)
- [x] **Phase 34: New Passive Mobs** — cow/sheep/chicken as jar-faithful Starlark plugins. (completed 2026-06-30)
- [ ] **Phase 35: Hostiles + Spawn Rules** — target-selector machinery + per-category cap + day/night gate; zombie/skeleton/spider.
- [ ] **Phase 36: Wolf (Neutral)** — `"wolf"` base type + supplier; wild goal set then the tame/owner second pass.

## Phase Details

### Phase 29: Damage Keystone (S2)
**Goal**: A mob can take and deal damage through a faithful, parallel `*Entity` hurt pipeline, remembering what hurt it — the keystone every panic, target-selector, melee, and wolf-anger behavior reads.
**Depends on**: Phase 28 (v4 complete: Folia regions, plugin entity API, attribute system). Requires the NEW damage-type tag extractor (schedule the `tools/` codegen tag-extractor work at/before this phase).
**Requirements**: MOB-SUB-01, MOB-SUB-02, MOB-SUB-03
**Success Criteria** (what must be TRUE):
  1. A player can hit a Go-native mob and the mob loses health, with i-frame / invulnerable-time gating identical to the jar (`actuallyHurtEntity` reads the mob's real `*attribute.Map`).
  2. A mob records its real `lastDamageSource` (the ported source, not a faked hurt flag), readable by a goal via a `was_hurt` / `last_damage_type` handle attr.
  3. `source.is(tag)` reads are genuine against a jar-extracted damage-type tag table (`PANIC_CAUSES` / `BYPASSES_ARMOR` / `IS_FIRE` …), NOT `const false`; the `on_damage` Emit fires at the post-mitigation site.
  4. Cross-region hits route through the OWNER's region as a barrier-queued `damageIntent` (never the actor's `cur()`); Docker `-race` + `strictRegion` clean.
  5. Standing: every op `float32` and every clamp/branch verbatim vs the jar (javap before writing); the `combat.go` CombatRules/clamp helpers reused not duplicated; CGO=0 + no new Go deps; the pig oracle `TestPluginPigEqualsGoNativePig` stays GREEN (no AI RNG touched).
**Plans**: 4 plans (4 sequential waves)
- [x] 29-01-PLAN.md — Tag codegen: GenTags.java (recursive nested-tag flatten) + gen_tags.go → data/tag/ (damage-type AND item tags) [MOB-SUB-03]
- [x] 29-02-PLAN.md — Entity hurt pipeline: *Entity fields + damage_source.go + combat_mob.go (applyDamageEntity/actuallyHurtEntity + baseTick i-frame decrement) [MOB-SUB-01, MOB-SUB-02]
- [x] 29-03-PLAN.md — Attack routing + cross-region: handleAttack dual-resolve + damageIntent barrier-queue + was_hurt/last_damage_type handle attrs (-race + strictRegion) [MOB-SUB-01, MOB-SUB-02]
- [x] 29-04-PLAN.md — Death + loot + XP: dieEntity (death removal) + dropMobLoot + dropMobExperience (entity-loot de-risk first) [MOB-SUB-01]
**Research flag**: yes — the cross-region `damageIntent` barrier-queue is the FIRST true cross-region write; the exact barrier/inbox plumbing (`transferIntent`/`asyncIn2` discipline) must be pinned before coding. Also confirm the registry extraction surfaces damage-type *entries* with their fields (exponent/scaling/message-id) — extend slightly if only IDs are present.

### Phase 30: JumpControl + Fluid (S1)
**Goal**: A mob can jump on command and detect fluid, so FloatGoal and any swimming/leaping mob work for `*Entity`, not just the player.
**Depends on**: Phase 29 (sequenced after the keystone to keep pig-plugin churn serial; S1 is otherwise independent of S2).
**Requirements**: MOB-SUB-04, MOB-SUB-05
**Success Criteria** (what must be TRUE):
  1. A goal can claim a JUMP flag and the mob performs the real `jumpFromGround` impulse, consumed in `serverAiStep`'s JUMP slot in jar order (after `navigation.tick`).
  2. `mobIsInWater` / `mobFluidHeight` / `isInLava` predicates (built on the existing `fluidAt` read) return jar-correct values for an `*Entity` — a mob in water no longer sinks/suffocates visibly.
  3. FloatGoal@0 is wired onto the pig in the Go-native oracle AND the plugin pig IN LOCKSTEP (the same plan), its `nextFloat()<0.8` draw confined to the running-goal callback — never the shared `serverAiStep`/`navigation.tick`.
  4. Standing: jar-verified (javap before writing); CGO=0 + no new Go deps; the pig oracle stays GREEN; Docker `-race` + `strictRegion` clean.
**Plans**: 3 plans (3 sequential waves)
- [x] 30-01-PLAN.md — Mob fluid predicates + lava decode: extend fluidState/decodeFluid for lava (lavaLevelOf + .isWater consumer audit) + mobInWater/mobFluidHeight/mobInLava/getFluidJumpThreshold [MOB-SUB-05]
- [x] 30-02-PLAN.md — JumpControl + aiStep jump branch: jumpControl/noJumpDelay + jumping/setJumping + the RNG-free JUMP slot after navigation.tick + jumpInLiquid(+0.04)/jumpFromGround(0.42) + entity.in_water/fluid_height/in_lava + nav.jump() handle ops [MOB-SUB-04]
- [x] 30-03-PLAN.md — FloatGoal@0 lockstep (Go-native newPigAI + vanilla_pig/main.star) + the wet-behavior test; the DRY pig oracle stays GREEN (canUse false -> zero draws) [MOB-SUB-04]

### Phase 30.1: Faithful RandomStrollGoal Target Selection (BUGFIX)
**Goal**: A wandering pig picks a REACHABLE stroll target every time, so it ambles freely instead of walking a few blocks and then jamming against a block forever. The current `randomStrollGoal.getPosition` returns a raw `e.y+dy` offset (dy ∈ [-7..+7]) with NO ground-snap or walkability check — an underground/unreachable target produces a partial/no A* path, and because `canContinueToUse` keys purely off `hasTarget` (cleared only on `path.done()`), the mob wedges permanently with `pending=true`.
**Depends on**: Phase 30 (navigation + jump in place). Reuses the existing `groundNavigation` + `computePath` seam; no new subsystem.
**Requirements**: BUGFIX (contributes to MOB-GATE-01 pig parity — RandomStrollGoal must be 1:1 before the dogfood gate in Phase 33).
**Root cause** (jar-verified): vanilla `RandomStrollGoal.getPosition` → `WaterAvoidingRandomStrollGoal`/`LandRandomPos.getPos(mob, 10, 7)` → `RandomPos.generateRandomPos(supplier, mob::getWalkTargetValue)`:
  - Tries **10** candidates, keeps the highest `getWalkTargetValue` weight (`Vec3.atBottomCenterOf(bestPos)`).
  - Each candidate: `generateRandomDirection(random, 10, 7)` draws **xt, yt, zt** IN THAT ORDER (our port draws dx, dz, dy — wrong RNG order) → `generateRandomPosTowardDirection` (home-restriction bias + `isOutsideLimits`/`isRestricted`/`isNotStable` reject) → `movePosUpOutOfSolid` (`RandomPos.moveUpOutOfSolid` scans Y up out of solid to the walkable surface; reject on `isWater`/`hasMalus`).
**Success Criteria** (what must be TRUE):
  1. A pig on flat ground strolls continuously (no permanent jam): a headless full-tick repro driving `serverAiStep`+`advance` shows the pig visits many distinct positions over N ticks and is never motionless for more than ~`interval`×2 ticks (the re-roll cadence), NOT 500+ ticks frozen.
  2. The stroll target is always a reachable walkable column: `getPosition` ports `LandRandomPos.getPos` — best-of-10 weighted by `getWalkTargetValue`, `generateRandomDirection` x/y/z draw order, `movePosUpOutOfSolid` ground-snap, `isStableDestination`/water/malus rejection — verbatim vs the jar (javap/CFR before writing).
  3. RNG lockstep: the new draw sequence is added to BOTH `newPigAI` (Go-native oracle) AND both `vanilla_pig/main.star` copies (repo-root + `server/assets/`) IN THE SAME PLAN; the pig oracle `TestPluginPigEqualsGoNativePig` stays GREEN (DRY world: the stroll gate still rolls, but the new candidate draws are identical on both sides → byte-identical).
  4. Standing: every op faithful (best-of-10 loop, draw order, moveUpOutOfSolid scan); CGO=0 + no new Go deps; Docker `-race` + `strictRegion` clean.
**Plans**: 1 plan (pattern-map → port `getWalkTargetValue` + `RandomPos`/`LandRandomPos` helpers → rewrite `getPosition` → lockstep both pigs → repro regression test).
- [x] 30.1-01-PLAN.md — Re-port `LandRandomPos.getPos`: goal draws 10 candidates (x/y/z, 30 nextInt) + emits them; RNG-free runtime snap/validate in `serverAiStep` (moveUpOutOfSolid + isStableDestination/water/malus, first-valid); lockstep both `.star` copies; green `TestPigStrollsWithoutWedging` + the pig oracle.
**Research flag**: no — the jar methods are decompiled and cited above; the only care item is the exact `getWalkTargetValue` (BlockPathTypes malus) port surface, scoped during pattern-map.

### Phase 31: PanicGoal (S2 consumer)
**Goal**: A hurt pig flees — PanicGoal reads the real damage source delivered by the keystone.
**Depends on**: Phase 29 (`lastDamageSource` + the panic-causing tag set).
**Requirements**: (contributes to MOB-GATE-01; no standalone MOB-SUB — PanicGoal is one of the pig's 5 deferred goals, formally closed by MOB-GATE-01/02 in Phase 33)
**Success Criteria** (what must be TRUE):
  1. PanicGoal@1 is wired onto the pig (oracle + plugin in lockstep) reading `entity.was_hurt` / `last_damage_type` against the panic-causing tag set — a genuine `is(panicCausing)` read, not a faked flag.
  2. A pig that takes panic-causing damage searches for water / picks a flee position in the exact jar draw order, then flees.
  3. Standing: jar-verified (javap before writing); CGO=0 + no new Go deps; the pig oracle stays GREEN (the water-search-vs-flee-pos draw order added in lockstep on both halves); Docker `-race` + `strictRegion` clean.
**Plans**: 1 plan (1 wave, sequential)
- [x] 31-01-PLAN.md — PanicGoal@1 (speed 1.25, MOVE) thin-consumer: has_last_damage signal + damage_in_tag handle, Go-native panicGoal (reuses generateRandomDirection 5/4 + snapStrollWant), lockstep both vanilla_pig/main.star, flee + non-panic + 5-goal boot-load tests [MOB-GATE-01]

### Phase 32: Held-Item + Item Tags (S4)
**Goal**: A goal can read the nearest player's held item and test item-tag membership — the read that powers tempting and (next phase) love-on-feed.
**Depends on**: Phase 29 (kept serial for oracle churn; S4 is otherwise independent of S1–S3). MUST precede Phase 33 (feeding sets `in_love`). Requires the NEW item-food tag extractor.
**Requirements**: MOB-SUB-06, MOB-SUB-07
**Success Criteria** (what must be TRUE):
  1. A goal can read the nearest player's main-hand item via `world.player_main_hand` (+ a nearest-player-with-id scan).
  2. `world.item_in_tag` / host-side `itemTagContains` return frozen tag-bools across the Starlark boundary against a jar-extracted item-tag table (`PIG_FOOD`, `CARROT_ON_A_STICK`, …) added to the `tools/` codegen pipeline (`GenTags.java` → `tags.json` → `data/tag/`).
  3. TemptGoal@4 ×2 (PIG_FOOD + CARROT_ON_A_STICK) is wired onto the pig in the oracle AND the plugin pig IN LOCKSTEP — a tempted pig follows the held food.
  4. Standing: jar-verified (javap before writing); CGO=0 + no new Go deps (the tag-extractor adds jar DATA, not a lib); the pig oracle stays GREEN; Docker `-race` + `strictRegion` clean.
**Plans**: 1 plan (1 wave, sequential)
- [x] 32-01-PLAN.md — S4 read primitives (itemInTag + offhand slot 45 + nearestPlayerHolding + two host nearest-tempt-player handles) + temptGoal port + two TemptGoal@4 (carrot 887 / pig_food tag) in newPigAI + lockstep both vanilla_pig/main.star + follow/ignore/boot-load-7 tests incl. -race [MOB-SUB-06, MOB-SUB-07, MOB-GATE-01]

### Phase 33: Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE
**Goal**: Animals age and breed, and the pig's last two deferred goals land — closing full pig parity and PROVING S1–S4 are 1:1 before any new mob reuses them. This is the HARD GATE.
**Depends on**: Phase 32 (feeding sets `in_love` via the held-item read); reuses `spawnDeclaredMob`.
**Requirements**: MOB-SUB-08, MOB-SUB-09, MOB-GATE-01, MOB-GATE-02
**Success Criteria** (what must be TRUE):
  1. Animals age via a signed-int `age`/`forcedAge` machine (-24000 baby start ticking up; +6000 breed cooldown) with RNG-free `tickMobAging`, `isBaby`, and the baby half-scale hitbox — never an `isBaby bool`.
  2. Two fed pigs in the same region breed: `inLove`/`loveCause` set by feeding, same-region partner search, `canMate`, baby spawn via `spawnDeclaredMob` (correct region routing + per-id RNG reseed) + `finalizeSpawnChildFromBreeding` (XP orb `1+nextInt(7)` + cooldown). Cross-region breeding ships the documented same-region cut.
  3. BreedGoal@3 + FollowParentGoal@5 are wired onto the pig (oracle + plugin in lockstep), completing all 5 deferred goals; the baby follows its parent (the observable dogfood).
  4. **THE GATE**: the full 8-goal pig oracle `TestPluginPigEqualsGoNativePig` passes byte-identical over 500 ticks with all goals live, AND two fed pigs breed + the baby follows its parent. This MUST pass before any new mob type is added (Phases 34–36).
  5. Standing: jar-verified (javap before writing); CGO=0 + no new Go deps; Docker `-race` + `strictRegion` clean.
**Plans**: 5 plans (5 sequential waves)
  - [x] 33-01-PLAN.md - Aging + AgeableMob: breedAge int + tickMobAging (pure-int, OUTSIDE serverAiStep) + baby HALF-SCALE hitbox + DATA_BABY_ID metadata + cross-0 broadcast
  - [x] 33-02-PLAN.md - Animal in-love + FEED: inLove int + setInLove/canFallInLove/isInLove + handleInteract feed path + encodeLevelParticles (HEART)
  - [x] 33-03-PLAN.md - C1 Go-native goals: BreedGoal@3 + FollowParentGoal@5 + breed()/getFreePartner/canMate + adjustedTickDelay + faithful isPanicking + Go-native goal-count test
  - [x] 33-04-PLAN.md - C2 lockstep: 5 host handles + both .star copies (9 goals, byte-identical) + 3 plugin-side goal-count tests + breed/follow scenario tests
  - [x] 33-05-PLAN.md - THE GATE: full 9v9 oracle byte-identical + end-to-end scenario + Docker -race + deviation doc (same-region/eye-height) + live dogfood verify
**Research flag**: yes — javap-verify `AgeableMob.aging`/`setAge`/`ageUp`, `Animal.canMate`/`spawnChildFromBreeding`/`finalizeSpawnChildFromBreeding` (XP/cooldown draw order) before porting; the same-region-cut vs barrier-resolved full-fidelity decision must be FORCED and documented as an accepted deviation, not left silent.

### Phase 34: New Passive Mobs
**Goal**: Cow, sheep, and chicken exist as jar-faithful Starlark plugins reusing the now-proven subsystem set.
**Depends on**: Phase 33 (the pig-parity dogfood gate MUST be green first — new mobs must NOT precede the dogfood).
**Requirements**: MOB-PASS-01, MOB-PASS-02, MOB-PASS-03
**Success Criteria** (what must be TRUE):
  1. Cow (`Cow.registerGoals`), sheep (`Sheep.registerGoals`), and chicken (`Chicken.registerGoals`) each spawn, tick, walk, float, panic, tempt, and breed via the shared subsystems; `categoryOf` maps all three to CREATURE.
  2. Per-mob extras port 1:1: cow milking interact (`Cow.mobInteract`), sheep EatBlockGoal + shear/wool (with wool regrow), chicken `aiStep` egg-lay + slow-fall.
  3. Each per-mob hitbox/node sizing is correct (no FloatGoal/nav mis-sizing from wrong dimensions).
  4. Standing: jar-verified (javap `<Mob>.registerGoals` before writing); CGO=0 + no new Go deps; the pig oracle stays GREEN; Docker `-race` + `strictRegion` clean.
**Plans**: 5 plans (3 waves)
- [x] 34-00-PLAN.md — Shared infra (wave 1): parameterized nearest_player_holding_food handle + sheep eat seam (EatBlockGoal.tick/Sheep.ate, DATA_WOOL regrow) + chicken aiStep hook (slow-fall + egg-lay, eggTime) + tick wiring [MOB-PASS-01/02/03]
- [x] 34-01-PLAN.md — Cow (wave 2): vanilla_cow .star pair (8-goal subset) + milking interact (AbstractCow.mobInteract bucket->milk_bucket) + cow tests [MOB-PASS-01]
- [x] 34-02-PLAN.md — Sheep (wave 2): vanilla_sheep .star pair (9 goals + EatBlockGoal@5 RNG-gated via the 34-00 seam, grass_block-below cite-defer) + EatBlockGoal RNG test [MOB-PASS-02]
- [x] 34-03-PLAN.md — Chicken (wave 2): vanilla_chicken .star pair (8 goals) + boot/behavior + slow-fall-applies-live test (egg-lay/slow-fall built in 34-00) [MOB-PASS-03]
- [x] 34-04-PLAN.md — The gate (wave 3): generalize boot-load (all 4 embeds) + spawnVanillaMob(name) + /dbg cow|sheep|chicken + natural-spawn pick + main.go boot + Docker -race + live bot; the pig oracle stays byte-identical [MOB-PASS-01/02/03]

### Phase 35: Hostiles + Spawn Rules
**Goal**: A survival night exists — zombie/skeleton/spider hunt and attack the player, gated by a faithful per-category cap and a day/night spawn rule.
**Depends on**: Phase 29 (mob→player damage) + Phase 33 (gate green). Adds the new `targetSelector` machinery.
**Requirements**: MOB-SUB-10, MOB-SUB-11, MOB-HOST-01, MOB-HOST-02, MOB-HOST-03
**Success Criteria** (what must be TRUE):
  1. `mobAI` gains a second `targetSelector goalSelector` instance run in jar order, `buildAIFromDecl` routes TARGET-flag goals into it, and an `attackTargetID` thin-id field carries the target; the shared `MeleeAttackGoal` / `NearestAttackableTargetGoal` / `HurtByTargetGoal` are built once for reuse.
  2. Zombie (`Zombie.registerGoals`, `ZombieAttackGoal`), skeleton (`Skeleton.registerGoals`, melee-only subset OK for v1), and spider (`Spider.registerGoals`) each acquire a player target, path to it, and deal melee damage through the keystone; all three map to MONSTER.
  3. Spawning is gated: a per-category cap (MONSTER 70 vs CREATURE 10, `/289` divisor kept), MONSTER tallied in `countByCategory`, `checkDespawn` ported (cap bounds spawns, not population).
  4. **The day/night light-gate DECISION is FORCED**: ship EITHER a minimal real light read OR a documented gametime-darkness proxy — never a silent daylight flood; the chosen approach and its deferral (full light-propagation cave spawning) are recorded.
  5. Standing: jar-verified (javap `registerGoals`/`isDarkEnoughToSpawn`/`checkMonsterSpawnRules`/`checkDespawn` before writing); CGO=0 + no new Go deps; target/attack goal draws added in lockstep where they touch any shared pig path; the pig oracle stays GREEN; Docker `-race` + `strictRegion` clean.
**Plans**: 6 plans (3 waves)
- [ ] 35-01-PLAN.md — targetSelector machinery: 2nd goalSelector instance + jar-order tick + buildAIFromDecl TARGET routing + attackTargetID + lastHurtByMob bookkeeping + shared NearestAttackableTarget/HurtByTarget/MeleeAttack goals [MOB-SUB-10]
- [x] 35-02-PLAN.md — Spawn gating: 3 MONSTER categoryOf cases + monsterCap (70/289) + isDarkEnoughToSpawn gametime proxy (FORCED) + the natural-spawn MONSTER pass + checkDespawn [MOB-SUB-11]
- [ ] 35-03-PLAN.md — Zombie plugin: Zombie.registerGoals (ZombieAttackGoal + target selectors) + per-hostile behavior/RNG test [MOB-HOST-01]
- [ ] 35-04-PLAN.md — Skeleton plugin: Skeleton.registerGoals melee-only subset (bow deferred) + test [MOB-HOST-02]
- [ ] 35-05-PLAN.md — Spider plugin: Spider.registerGoals (FloatGoal + LeapAtTargetGoal nextFloat + daylight-gated SpiderAttackGoal) + test [MOB-HOST-03]
- [ ] 35-06-PLAN.md — The gate: boot-load 3 embeds + /dbg levers + embed-vs-root byte-identity + pig-oracle byte-identical + Docker -race + live bot verify [MOB-SUB-10, MOB-SUB-11, MOB-HOST-01, MOB-HOST-02, MOB-HOST-03]
**Research flag**: yes — the light-engine vs documented darkness-proxy decision must be FORCED (not left silent); javap `isDarkEnoughToSpawn`/`checkMonsterSpawnRules`/`checkDespawn`; verify hostile types map to MONSTER in `countByCategory` and port `checkDespawn` (cap gates spawns, not population).

### Phase 36: Wolf (Neutral)
**Goal**: The flagship neutral mob — a wolf that lives wild, defends itself, and (second pass) can be tamed, sit, and anger on hit.
**Depends on**: Phase 29 (`lastDamageSource` for anger-on-hit) + Phase 35 (target machinery); the most complex single mob, built last.
**Requirements**: MOB-NEUT-01, MOB-NEUT-02
**Success Criteria** (what must be TRUE):
  1. A `"wolf"` base type exists with a NEW 1:1 `Wolf.createAttributes()` supplier in `level/attribute/defaults.go`; the wild goal set (Float/Panic/Avoid/Leap/Melee/target) runs jar-faithfully.
  2. The tame second pass lands: `TamableAnimal` owner/sit state + the OwnerHurt target chain + anger-on-hit reading MOB-SUB-02's `lastDamageSource`.
  3. Standing: jar-verified (javap `Wolf.registerGoals` + `Wolf.createAttributes` + `TamableAnimal` before writing); CGO=0 + no new Go deps; wolf goal draws kept in lockstep discipline (even without a wolf oracle); the pig oracle stays GREEN; Docker `-race` + `strictRegion` clean.
**Plans**: TBD
**Research flag**: yes — the `TamableAnimal` owner/sit/anger state model + the OwnerHurt target chain; verify/add the wolf attribute supplier FIRST (the one missing supplier).

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1–9 (v1) | v1.0 | — | Complete | 2026-06-24 |
| 10–16 (v2.0 worldgen + structures) | v2.0 | 22/22 | Complete | 2026-06-25 |
| 17–20 (v3 online-mode + operator-UX + structure-polish) | v3 | 33/33 | Complete | 2026-06-27 |
| 21–28 (v4 plugin / scripting system) | v4 | 19/19 | Complete | 2026-06-29 |
| 29. Damage Keystone (S2) | v5 | 4/4 | Complete   | 2026-06-29 |
| 30. JumpControl + Fluid (S1) | v5 | 3/3 | Complete   | 2026-06-29 |
| 31. PanicGoal (S2 consumer) | v5 | 1/1 | Complete   | 2026-06-30 |
| 32. Held-Item + Item Tags (S4) | v5 | 1/1 | Complete   | 2026-06-30 |
| 33. Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE | v5 | 5/5 | Complete   | 2026-06-30 |
| 34. New Passive Mobs | v5 | 5/5 | Complete   | 2026-06-30 |
| 35. Hostiles + Spawn Rules | v5 | 1/6 | In Progress|  |
| 36. Wolf (Neutral) | v5 | 0/? | Not started | - |

## Deferred / Backlog (unwired or subsystem-blocked)

Items discovered during execution that are **not yet wired** or are **blocked on an unbuilt subsystem**. Each names the missing subsystem and the phase that should build it. Per the "no built-but-unwired code" rule (the SC4 gap lesson), we DO NOT ship speculative unwired code — we record it here and build it when its subsystem lands. Nothing here blocks the v3 close (all are post-v3 / vanilla-completeness work).

### v5-deferred (documented deviations / future requirements)

Per the SUMMARY.md "Future Requirements" + the forced phase decisions — these are accepted, cited deferrals, never silent:

- **Barrier-resolved full-fidelity cross-region breeding** — v5 ships the same-region cut (partners must share a region); the barrier-resolved match is documented as deferred (Phase 33 decision).
- **Full light-propagation cave spawning** (`isDarkEnoughToSpawn` with a real block+sky light engine) — if Phase 35 ships the gametime-darkness proxy, the real light engine is the deferred follow-up.
- **Skeleton bow / ranged attack** (`RangedBowAttackGoal` + `reassessWeaponGoal`) — Phase 35 ships the melee-only subset; ranged is a documented follow-up.
- **Mob variants** (Husk/Drowned/Stray/Bogged/CaveSpider/MushroomCow) — same goal skeletons, different data; deferred past v5.
- **`MoveThroughVillageGoal`** (needs village pathing) + **Brain/behavior-tree mobs** (none of the six v5 mobs use Brain) — out for v5.
- **Furnace/cooking blocks** (Phase-25 `deferred-blocks.md`) + **SUB-ITEMNBT Phase B** (component transcoder) — carryover from prior milestones, untouched by v5.

### Missing subsystems → BUILT as the **v3.1 "Persistence & Vanilla Completeness"** milestone (2026-06-27)

All 5 prerequisite subsystems are now PORTED 1:1 from the 26.2 jar, built in isolated worktrees with
maximum parallelization (ATTRIB+FACESTURDY simultaneous, then ITEMNBT+PERSIST, then BLOCKTICK),
merged to `ender-776` with zero conflicts (disjoint files). Each build/vet/test green + -race green.

| Subsystem | Status | Commit | What landed |
|-----------|--------|--------|-------------|
| **SUB-ATTRIB** — attribute system | ✅ DONE | 301aaec3 | `level/attribute/`: AttributeInstance.calculateValue fold (ADD_VALUE→MULT_BASE→MULT_TOTAL), DefaultAttributes per-entity (bit-exact: Witch 26.0, Cat 0.30000001192092896, Zombie 0.23000000417232513…), Mob.finalizeSpawn RNG draw order (triangle + left-handed), combat/armor/fall/knockback scaling. Stubs cited: variant/profession (registry-bound), NBT-persist of attrs. |
| **SUB-FACESTURDY** — block support shapes | ✅ DONE | d25b1db7 | Codegen `GenBlockSupport.java`+`gen_block_support.go` → `level/block/support.go` (32366 states, precomputed [18] table). Replaced 2 proxies: `faceSturdyUp` (worldgen) + `isSuffocating` (now per-block faithful — glass/leaves/slabs no longer suffocate). Attachment-block `canSurvive` (torches/rails) deferred — the `IsFaceSturdy` primitive is now available for it. |
| **SUB-ITEMNBT** — ItemStack disk codec | ✅ DONE (Phase A) | 6406dcfe | `save/item_nbt.go`: `{Slot,id,count}` + ContainerHelper.saveAllItems/loadAllItems 1:1. **Phase B TODO** (cited): wire→disk component transcoder (per-DataComponentType streamCodec→codec) for enchanted/named items; today component-bearing stacks persist `{id,count}` with a measured `DroppedComponents` counter (never silent). |
| **SUB-PERSIST** — chunk-save loop | ✅ DONE | 8d2a5ac2 | `world/chunk_save_loop.go` RunChunkSaveLoop: dirty tracking + periodic/on-unload flush + region write (semaphore IO throttle) + `save.Level` level.dat writer. **Concurrency**: only immutable byte snapshots cross the tick→save seam (race-free by construction, TICK-05). **Opt-in** `SULFUR_PERSIST_CHUNKS=1` (default unchanged). Unblocked the HANDOFF chest-item flush (trySaveLootTable XOR saveAllItems 1:1) + player-inventory persist. |
| **SUB-BLOCKTICK** — scheduled block ticks | ✅ DONE | (v3.1 merge) | `level/ticks/` LevelTicks: ScheduledTick deterministic ordering (triggerTick→priority→subTickOrder), save/load `block_ticks` NBT, cap 65536. First consumer wired: sugar cane (scheduleTick→drain→canSurvive→destroy+drop, growth). Existing fluid loop COEXISTS (not migrated — documented). TODO cited: tickCheck tightened to shouldTickBlocksAt; world random-tick driver (separate subsystem). |

**Remaining follow-ups (smaller, not new subsystems):** SUB-ITEMNBT Phase B (component transcoder), attachment-block survival (torches/rails — uses the now-available `IsFaceSturdy`), cactus/crop block-tick consumers, attribute NBT-persistence, variant/profession real reads (registry-bound).

### Wired-but-partial / small follow-ups (no new subsystem needed)

| Item | State | Note |
|------|-------|------|
| Chest BE persistence | **DONE (cb4597d6)** | `block_entities` now serialize (loot table+seed round-trip; unopened chest re-rolls deterministically = vanilla-correct). Item-level flush waits on SUB-PERSIST + SUB-ITEMNBT. |
| nbt list-of-Marshaler encoding | **FIXED (cb4597d6)** | Encoder list loop now routes through `marshal` — also un-corrupts `entities`/`Lights`/`ScheduledEvents` (`[]RawMessage`) for when those write paths land. |
| Chest open UI follow-ups | Partial | Random-slot shuffle (cosmetic), multi-viewer sync — small, no subsystem. |
| Vegetation survival | DONE | Non-vegetation classes blocked on SUB-FACESTURDY / SUB-BLOCKTICK above. |
| DATA_SHARED_FLAGS sprint/sneak pose; mob-water-nav; async-fluid | Deferred | AI/perf-track items, not persistence. |
