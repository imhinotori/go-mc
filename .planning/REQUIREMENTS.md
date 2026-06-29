# Requirements — Sulfur v5 (Mob Behaviors & Living-Entity Subsystems)

**Milestone:** v5 — Mob Behaviors & Living-Entity Subsystems
**Defined:** 2026-06-29
**Source:** `.planning/research/SUMMARY.md` (HIGH confidence, jar-verified) + the Phase-24 `deferred-goals.md` cited subsystems.

> **Mandate carry-in:** every behavior below is a LITERAL 1:1 PORT of the unobfuscated 26.2 jar
> (`temp/cache/26.2-inner.jar`, javap-verify BEFORE writing, cite the class/method). The ONLY
> permitted deviation is optimization that provably preserves identical observable behavior. CGO=0
> static binary + `-race` (Docker) + the bit-fragile pig oracle (`TestPluginPigEqualsGoNativePig`)
> green are standing constraints on every requirement. New mobs are jar-faithful Starlark plugins
> (the v4 `vanilla_pig` dogfood pattern).

---

## v5 Requirements

### Living-Entity Subsystems (the 4 deferred prerequisites)

- [ ] **MOB-SUB-01**: A mob can take and deal damage — a parallel `*Entity` damage pipeline (`applyDamageEntity`/`actuallyHurtEntity`) reading the mob's real `*attribute.Map`, with i-frame/invulnerable-time gating, the `on_damage` Emit at the post-mitigation site, and player↔mob / mob↔mob / mob↔player attack flows wired. **(S2 — the keystone.)**
- [ ] **MOB-SUB-02**: A mob records its last damage source — a per-`*Entity` `lastDamageSource` field (owner-thread-safe under Folia), readable by goals via a `was_hurt`/`last_damage_type` handle attr (the real ported source, NOT a faked hurt flag).
- [ ] **MOB-SUB-03**: Damage-type tag membership is data-driven — a jar-extracted damage-type tag table (`PANIC_CAUSES`, `BYPASSES_ARMOR`, `IS_FIRE`, …) so `source.is(tag)` reads are genuine, not `const false` (the armor-bypass + panic-causing branches).
- [ ] **MOB-SUB-04**: A mob can jump on command — a `JumpControl` impulse seam on `mobAI` consumed in `serverAiStep`'s JUMP slot in jar order (the real `jumpFromGround` impulse), claimable by a goal's JUMP flag.
- [ ] **MOB-SUB-05**: A mob can detect fluid — `mobIsInWater`/`mobFluidHeight`/`isInLava` predicates (built on the existing `fluidAt` read) so FloatGoal and swim behavior work for `*Entity`, not just `*tickPlayer`.
- [ ] **MOB-SUB-06**: A goal can read the nearest player's held item — `world.player_main_hand` (+ a nearest-player-with-id scan) and `world.item_in_tag` / host-side `itemTagContains` returning frozen tag-bools across the Starlark boundary. **(S4.)**
- [ ] **MOB-SUB-07**: Item-food tag membership is data-driven — a jar-extracted item-tag table (`PIG_FOOD`, `COW_FOOD`, `WOLF_FOOD`, `Items.CARROT_ON_A_STICK`, …) added to the `tools/` codegen pipeline (`GenTags.java` → `tags.json` → `data/tag/`).
- [ ] **MOB-SUB-08**: Animals age — a signed-int `age`/`forcedAge` machine (-24000 baby start ticking up; +6000 breed cooldown) with `tickMobAging` (RNG-free), `isBaby`, and the baby half-scale hitbox. **(S3.)**
- [ ] **MOB-SUB-09**: Animals breed — `inLove`/`loveCause` set by feeding (via MOB-SUB-06), same-region partner search, `canMate`, and baby spawn reusing `spawnDeclaredMob` (correct region routing + per-id RNG reseed) with `finalizeSpawnChildFromBreeding` (XP orb `1+nextInt(7)` + cooldown). Cross-region breeding ships the same-region cut (full-fidelity barrier-resolved match documented as deferred).
- [ ] **MOB-SUB-10**: Hostiles have a target selector — a second `targetSelector goalSelector` instance on `mobAI` run in jar order, `buildAIFromDecl` routing TARGET-flag goals into it, and an `attackTargetID` thin-id field. The shared `MeleeAttackGoal` / `NearestAttackableTargetGoal` / `HurtByTargetGoal` primitives, built once for reuse.
- [ ] **MOB-SUB-11**: Hostile spawning is gated — a per-category cap (MONSTER 70 vs CREATURE 10, keeping the `/289` divisor) with MONSTER tallied in `countByCategory`, `checkDespawn` ported (cap bounds spawns, not population), and a day/night spawn gate (a real minimal light read OR a documented gametime-darkness proxy — never a silent daylight flood). **(S5.)**

### Pig Parity (the dogfood gate)

- [ ] **MOB-GATE-01**: The pig's 5 deferred goals are wired 1:1 onto the existing `vanilla_pig` plugin via MOB-SUB-01..09 — FloatGoal@0, PanicGoal@1, BreedGoal@3, TemptGoal@4 ×2 (PIG_FOOD + CARROT_ON_A_STICK), FollowParentGoal@5 — each added to the Go-native oracle AND the plugin pig IN LOCKSTEP. (Goal-wiring lands incrementally across Phases 30–33; formally closed at Phase 33.)
- [ ] **MOB-GATE-02**: The full 8-goal pig oracle stays green — `TestPluginPigEqualsGoNativePig` passes byte-identical over 500 ticks with all goals live, AND two fed pigs breed + the baby follows its parent (the observable dogfood). This gate MUST pass before any new mob type is added.

### Passive Mobs

- [ ] **MOB-PASS-01**: Cow — a jar-faithful Starlark plugin (`Cow.registerGoals`), mapped to CREATURE, with the milking interact (`Cow.mobInteract`).
- [ ] **MOB-PASS-02**: Sheep — a jar-faithful plugin (`Sheep.registerGoals`) adding EatBlockGoal + shear/wool (and wool regrow as a differentiator).
- [ ] **MOB-PASS-03**: Chicken — a jar-faithful plugin (`Chicken.registerGoals`) adding the `aiStep` egg-lay + slow-fall.

### Hostile Mobs

- [ ] **MOB-HOST-01**: Zombie — a jar-faithful plugin (`Zombie.registerGoals`) with `ZombieAttackGoal` + target selectors, mapped to MONSTER, day/night burn behavior.
- [ ] **MOB-HOST-02**: Skeleton — a jar-faithful plugin (`Skeleton.registerGoals`), melee-only subset acceptable for v1 (bow / `RangedBowAttackGoal` + `reassessWeaponGoal` a documented follow-up), day/night burn.
- [ ] **MOB-HOST-03**: Spider — a jar-faithful plugin (`Spider.registerGoals`) with its target selector (wall-climb + light-gated aggression as differentiators).

### Neutral Mob

- [ ] **MOB-NEUT-01**: Wolf (wild subset) — a `"wolf"` base type + a NEW 1:1 `Wolf.createAttributes()` supplier in `level/attribute/defaults.go`; the wild goal set (Float/Panic/Avoid/Leap/Melee/target).
- [ ] **MOB-NEUT-02**: Wolf (tame second pass) — `TamableAnimal` owner/sit state + the OwnerHurt target chain + anger-on-hit (reads MOB-SUB-02's `lastDamageSource`).

---

## Future Requirements (deferred past v5)

- Mob variants (Husk/Drowned/Stray/Bogged/CaveSpider/MushroomCow) — same goal skeletons, different data.
- `MoveThroughVillageGoal` (needs village pathing); Brain/behavior-tree mobs (none of the six v5 mobs use Brain).
- Full light-propagation cave spawning (`isDarkEnoughToSpawn` with a real block+sky light engine) if v5 ships the gametime-darkness proxy.
- Barrier-resolved full-fidelity cross-region breeding (v5 ships the same-region cut).
- Skeleton bow / ranged attack (`RangedBowAttackGoal` + `reassessWeaponGoal`).
- Furnace/cooking blocks (Phase-25 `deferred-blocks.md`); SUB-ITEMNBT Phase B (carryover from prior milestones).

## Out of Scope

- **New Go dependencies / any cgo** — v5 is a pure 1:1 jar-port onto existing seams; the concurrency stack (`xsync`/`ants`/`conc`/`x/sync`) is the complete substrate; CGO=0 is preserved. A 3rd-party nav/spatial/tag library would itself violate the 1:1 mandate.
- **Interface-generalizing the player damage pipeline** — there are two attribute systems; the mob path is a parallel `*Entity` sibling reusing only the pure CombatRules/clamp helpers, never the v3-sealed player flow.
- **Async optimization of the new mob logic before it is correct** — optimization-last mandate; port synchronous vanilla first, layer `ants`/`xsync` only as proven-neutral optimizations.
- **Bedrock / cross-platform; village-AI Brain mobs; the full TamableAnimal breeding chain for wolves** — out for v5.

## Traceability

Every v5 requirement maps to exactly one phase. 21/21 mapped — no orphans, no duplicates.

| Requirement | Phase | Status |
|-------------|-------|--------|
| MOB-SUB-01 | Phase 29 — Damage Keystone (S2) | Pending |
| MOB-SUB-02 | Phase 29 — Damage Keystone (S2) | Pending |
| MOB-SUB-03 | Phase 29 — Damage Keystone (S2) | Pending |
| MOB-SUB-04 | Phase 30 — JumpControl + Fluid (S1) | Pending |
| MOB-SUB-05 | Phase 30 — JumpControl + Fluid (S1) | Pending |
| MOB-SUB-06 | Phase 32 — Held-Item + Item Tags (S4) | Pending |
| MOB-SUB-07 | Phase 32 — Held-Item + Item Tags (S4) | Pending |
| MOB-SUB-08 | Phase 33 — Aging + Breeding (S3) / Pig Parity GATE | Pending |
| MOB-SUB-09 | Phase 33 — Aging + Breeding (S3) / Pig Parity GATE | Pending |
| MOB-SUB-10 | Phase 35 — Hostiles + Spawn Rules | Pending |
| MOB-SUB-11 | Phase 35 — Hostiles + Spawn Rules | Pending |
| MOB-GATE-01 | Phase 33 — Aging + Breeding (S3) / Pig Parity GATE | Pending |
| MOB-GATE-02 | Phase 33 — Aging + Breeding (S3) / Pig Parity GATE | Pending |
| MOB-PASS-01 | Phase 34 — New Passive Mobs | Pending |
| MOB-PASS-02 | Phase 34 — New Passive Mobs | Pending |
| MOB-PASS-03 | Phase 34 — New Passive Mobs | Pending |
| MOB-HOST-01 | Phase 35 — Hostiles + Spawn Rules | Pending |
| MOB-HOST-02 | Phase 35 — Hostiles + Spawn Rules | Pending |
| MOB-HOST-03 | Phase 35 — Hostiles + Spawn Rules | Pending |
| MOB-NEUT-01 | Phase 36 — Wolf (Neutral) | Pending |
| MOB-NEUT-02 | Phase 36 — Wolf (Neutral) | Pending |

**Coverage:** 21/21 v5 requirements mapped ✓ — no orphans, no duplicates.

> **Note on MOB-GATE-01 (incremental wiring):** the pig's 5 deferred goals are wired across Phases 30–33 in lockstep with each enabling subsystem — FloatGoal@0 (Phase 30), PanicGoal@1 (Phase 31), TemptGoal@4 ×2 (Phase 32), BreedGoal@3 + FollowParentGoal@5 (Phase 33). The requirement is FORMALLY OWNED/CLOSED at Phase 33, where the full 8-goal oracle (MOB-GATE-02) must pass before any new mob (Phases 34–36).
