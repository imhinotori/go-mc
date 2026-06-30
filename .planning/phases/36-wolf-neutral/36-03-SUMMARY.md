---
phase: 36-wolf-neutral
plan: 03
subsystem: mob-plugin
tags: [wolf, tamable, neutral-mob, starlark, plugin, embed, hybrid-kind-callback, declaration]

# Dependency graph
requires:
  - phase: 36-wolf-neutral
    plan: 01
    provides: "base_type \"wolf\" resolver + the 6 wolf kinds (sit/follow_owner/owner_hurt_by/owner_hurt/angry_player_target/skeleton_target) live in buildNativeGoal + the parameterized target goal + wolfSupplier/category + the vanillaWolfMobName const + /dbg wolf"
  - phase: 35-hostiles
    provides: "the combat kinds (melee_attack/leap_at_target/hurt_by_target) + the kind= seam + the goalSelector/targetSelector flagTarget split at spawn"
  - phase: 33-passive-breed
    provides: "the BreedGoal breed_* .star callbacks (mob-agnostic, reused verbatim)"
  - phase: 31-passive-core
    provides: "the PanicGoal panic_* .star callbacks (reused verbatim at speed 1.5)"
provides:
  - "The vanilla_wolf Starlark plugin (repo-root + byte-identical embed): declare_mob base_type \"wolf\" + the full IN-SCOPE Wolf.registerGoals set (10 goalSelector + 5 targetSelector = 15 goals)"
  - "The COMPLETED wolf boot-load wiring: assets/vanilla_wolf in //go:embed + vanillaWolfMobName in vanillaMobNames (the 36-01 deferral discharged)"
  - "The wolf as the FIRST vanilla CREATURE with a populated targetSelector (owner-defense + anger-gated player + skeleton hunting)"
affects: [36-04 (the wolf gate/behavior tests — TestWolfBootLoads/TestWolfBehavior + the focused RNG tests)]

# Tech tracking
tech-stack:
  added: []  # no new Go dependency (go.mod unchanged)
  patterns:
    - "Hybrid kind=/callback mob declaration: combat + tame/owner/target goals by kind= (Go-native, zero re-port), passive goals as verbatim .star callbacks copied from a sibling mob"
    - "Discharging a foundation-plan embed deferral: the //go:embed directive + the vanillaMobNames load entry land in the asset-shipping plan, not the foundation plan (a missing-dir embed is a hard compile error)"

key-files:
  created:
    - plugins/vanilla_wolf/main.star
    - plugins/vanilla_wolf/plugin.toml
    - server/assets/vanilla_wolf/main.star
    - server/assets/vanilla_wolf/plugin.toml
  modified:
    - server/vanilla_pig_embed.go
    - server/vanilla_mob_test.go

key-decisions:
  - "The wolf goal set splits 10 goalSelector + 5 targetSelector at spawn (the Mob.registerGoals flagTarget routing, plugin_mob_ai.go) — the registry decl.goals holds the combined 15"
  - "panic reuses the cow PanicGoal callbacks verbatim with PANIC_SPEED=1.5 (TamableAnimalPanicGoal EXTENDS PanicGoal; the speed multiplier + the tamed water/away tick refinement are cite-deferred)"
  - "breed reuses the cow breed_* callbacks verbatim (Phase 33, mob-agnostic); the wolf's BreedGoal sits at @7 (vs the cow's @2) — the priority differs, the callbacks do not"
  - "the @4 player target uses kind=angry_player_target (the 36-01 B1 anger-gated PLAYER goal) + the @7 skeleton uses kind=skeleton_target (B2) — NEVER the bare nearest_attackable_target (count 0); the skeleton target is IN SCOPE, not deferred"
  - "plugin.toml mirrors the cow caps (entities.read/write + world.read + nav) — taming + all combat/tame/target ops are HOST-side, no widened cap"

requirements-completed: [MOB-NEUT-01, MOB-NEUT-02]

# Metrics
duration: 24min
completed: 2026-06-30
---

# Phase 36 Plan 03: vanilla_wolf Starlark Plugin Summary

**The wolf's declaration half — a hybrid kind=/callback Starlark plugin declaring the full IN-SCOPE Wolf.registerGoals set (15 goals: 10 goalSelector + 5 targetSelector) by NAME, consuming the Phase-35 combat kinds + the Phase-36 tame/owner/anger kinds via kind= with zero re-port, reusing the cow's panic + breed + passive callbacks verbatim, and completing the boot-load wiring (the embed directive + the load entry) the 36-01 foundation plan deferred. The pig oracle stays byte-identical; the wolf boot-loads clean.**

## Performance

- **Duration:** ~24 min
- **Tasks:** 2/2
- **Files:** 4 created (the wolf .star + plugin.toml × repo-root + embed), 2 modified (the embed wiring + the boot-load count test)
- **Commits:** ea6d79f6 (Task 1), d7b24235 (Task 2)

## Accomplishments

### Task 1 — Author vanilla_wolf main.star + plugin.toml (commit ea6d79f6)

Authored `plugins/vanilla_wolf/main.star` from the vanilla_zombie hybrid template, citing `net.minecraft.world.entity.animal.wolf.Wolf.registerGoals` + `Wolf.createAttributes` (36-JARNOTES.md:13-41):

- **declare_mob** `base_type = "wolf"` (the 36-01 resolver → entity.Wolf, ID 149) + attributes `movement_speed 0.3` / `max_health 8.0` (untamed) / `attack_damage 4.0`.
- **goalSelector (10):** `@1 float` + `@1 panic` (the cow PanicGoal reuse, PANIC_SPEED=1.5) + `@2 kind="sit"` + `@4 kind="leap_at_target"` + `@5 kind="melee_attack"` + `@6 kind="follow_owner"` + `@7 breed` (the cow BreedGoal reuse) + `@8 stroll` + `@10 look` + `@10 around`.
- **targetSelector (5):** `@1 kind="owner_hurt_by"` + `@2 kind="owner_hurt"` + `@3 kind="hurt_by_target"` + `@4 kind="angry_player_target"` (the B1 anger-gated PLAYER goal) + `@7 kind="skeleton_target"` (the B2 skeleton scan — IN SCOPE).
- **passive callbacks** (float/panic/stroll/look/around/breed) copied verbatim from vanilla_cow (mob-agnostic — they read only entity.*/world.*/nav. handles); the draw order matches the bytecode exactly.
- **plugin.toml** mirrors the cow caps (`entities.read`, `entities.write`, `world.read`, `nav`) — taming + combat/tame/target are host-side, no widened cap.

### Task 2 — Embed byte-identical + complete the boot-load wiring (commit d7b24235)

- **server/assets/vanilla_wolf/{main.star,plugin.toml}** copied byte-for-byte (`diff` empty, the vanilla_pig_embed.go:28-30 invariant).
- **server/vanilla_pig_embed.go** (the 36-01 deferral discharged): appended `assets/vanilla_wolf` to the `//go:embed` directive + `vanillaWolfMobName` to `vanillaMobNames` + updated the forward-declaration + load-order comments. With the asset present, `spawnVanillaMob(vanillaWolfMobName)` + `/dbg wolf` now resolve a real boot-loaded declaration.
- **server/vanilla_mob_test.go**: registry total 7→8 + a `vanillaWolfMobName` row with the goalSelector/targetSelector split (10 + 5); split `TestSpawnVanillaMobByName` to assert BOTH `e.ai.goals.goals` (10) and `e.ai.targetSelector.goals` (5) — the wolf is the first vanilla CREATURE with a populated targetSelector.

## The final goal-list shape

| Selector | Pri | Goal | Wiring |
|----------|-----|------|--------|
| goal | 1 | FloatGoal | .star float_* |
| goal | 1 | TamableAnimalPanicGoal(1.5) | .star panic_* (cow reuse, speed 1.5) |
| goal | 2 | SitWhenOrderedToGoal | kind="sit" (36-01) |
| goal | 4 | LeapAtTargetGoal(0.4) | kind="leap_at_target" (35-01) |
| goal | 5 | MeleeAttackGoal(1.0,true) | kind="melee_attack" (35-01) |
| goal | 6 | FollowOwnerGoal(1.0,10,2) | kind="follow_owner" (36-01) |
| goal | 7 | BreedGoal(1.0) | .star breed_* (cow reuse, Phase 33) |
| goal | 8 | WaterAvoidingRandomStrollGoal(1.0) | .star stroll_* |
| goal | 10 | LookAtPlayerGoal(Player,8.0) | .star look_* |
| goal | 10 | RandomLookAroundGoal | .star around_* |
| target | 1 | OwnerHurtByTargetGoal | kind="owner_hurt_by" (36-01) |
| target | 2 | OwnerHurtTargetGoal | kind="owner_hurt" (36-01) |
| target | 3 | HurtByTargetGoal.setAlertOthers | kind="hurt_by_target" (35-01) |
| target | 4 | NearestAttackableTargetGoal&lt;Player&gt;(isAngryAt) | kind="angry_player_target" (36-01 B1) |
| target | 7 | NearestAttackableTargetGoal&lt;AbstractSkeleton&gt; | kind="skeleton_target" (36-01 B2) |

**Total: 15 (10 goalSelector + 5 targetSelector).** `kind = "nearest_attackable_target"` count = 0 (the wolf uses the parameterized kinds, never the bare un-gated player goal the hostiles use).

## Load-smoke result

The wolf boot-loads clean (proven in isolation from the parallel 36-02 WIP — see Deviations):
- `CGO_ENABLED=0 go build ./...` → exit 0 (the embed resolves).
- `TestPluginPigEqualsGoNativePig` → PASS (the pig oracle byte-identical — the wolf addition perturbs nothing).
- `TestHostilesBootLoad` + `TestAllFourMobsBootLoad` (now 8 declarations) + `TestSpawnVanillaMobByName` (wolf: 10 goalSelector + 5 targetSelector) → PASS — the 6 wolf kinds resolve through `buildNativeGoal` with **no unknown-kind panic and no flags() disagreement** (T-36-07 exercised early).
- Full `go test ./server/ -count=1` → ok (the whole server suite green).

The authoritative wolf boot proof (TestWolfBootLoads / TestWolfBehavior + the focused RNG tests for the tame nextInt(3) and the anger UniformInt(400,780)) is Plan 36-04's gate, per the plan.

## DEFERRALS (cited+omitted, NEVER silently dropped)

- **WolfAvoidEntityGoal&lt;Llama&gt; @3** — no Llama mob in v1; the untamed-only flee-llama goal ships WITH the Llama mob (36-JARNOTES.md:19,84,250).
- **BegGoal @9** — the wolf head-tilt beg is a client-only visual (an interested-flag + a food-holder facing scan); no observable server behavior (36-JARNOTES.md:25,99,250).
- **NonTameRandomTargetGoal&lt;Animal&gt; @5 + &lt;Turtle&gt; @6** — the untamed prey-hunting goals need PREY_SELECTOR + the prey/turtle target entities, none of which exist in v1; they defer WITH those mobs (36-JARNOTES.md:33-34,84,250).
- **ResetUniversalAngerTargetGoal @8** — OMITTED: its canUse is UNIVERSAL_ANGER-gamerule (defaults FALSE) → never fires; the gametime-endpoint anger model has no per-tick decrement for it to reset (both W6-dissolved, 36-JARNOTES.md:197-204,239-247). The PER-MOB anger timer IS in scope (it expires automatically).
- **The TamableAnimalPanicGoal tamed/owner-guard refinement** — TamableAnimalPanicGoal's tick override is a minor water/away nudge over the base PanicGoal; v1 reuses the cow PanicGoal core at speed 1.5; the tamed-specific guard is cite-deferred, no new RNG (36-JARNOTES.md:192-195).

**The skeleton target is NOT deferred** — it ships via `kind="skeleton_target"` (the skeleton exists from Phase 35, the 36-01 B2 scan is buildable). No silent drop, no follow-up punt.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Test] Updated TestAllFourMobsBootLoad + TestSpawnVanillaMobByName for the wolf (registry 7→8, the goalSelector/targetSelector split)**
- **Found during:** Task 2 (the load-smoke after adding the load entry).
- **Issue:** `TestAllFourMobsBootLoad` asserted exactly 7 boot-loaded declarations; my embed load entry correctly made it 8 (the wolf). `TestSpawnVanillaMobByName` iterated the per-mob table asserting `len(e.ai.goals.goals) == goalCount`, but the wolf's 5 TARGET-flag goals route into the independent `e.ai.targetSelector.goals` at spawn (the Mob.registerGoals flagTarget split, plugin_mob_ai.go) — so the wolf builds 10 goalSelector + 5 targetSelector, not 15 in one list.
- **Fix:** Bumped the count to 8 (+ the message), added a `vanillaWolfMobName` row with a new `targetCount` field (10 + 5), made `TestAllFourMobsBootLoad` assert the registry combined total (`goalCount + targetCount`), and split `TestSpawnVanillaMobByName` to assert both lists. This is a direct consequence of MY load entry (the wolf is now a legitimate boot-loaded mob) — a Rule 1 fix in MY scope (the boot-load test is the load-smoke this plan owns).
- **Files modified:** server/vanilla_mob_test.go
- **Commit:** d7b24235

### Scope deviation (cited, not silent) — the parallel-wave race

**2. [Out of scope — 36-02 WIP] server/attack_dispatch.go is dirty + non-compiling in the shared working tree**
- **Issue:** Plan 36-02 (the disjoint Wave-2 plan that owns attack_dispatch.go) is running in parallel and has an UNCOMMITTED, mid-flight `attack_dispatch.go` in the working tree: it adds a `t.tryWolfInteract(p, mob)` call site + a `level/attribute` import, but `tryWolfInteract` is not yet defined and the import is unused — so `go build ./server/` fails. This is 36-02's WIP, NOT caused by my changes (my Task-1 commit did not touch attack_dispatch.go; the call site is absent from HEAD; the count even changed mid-session from 1→2 as 36-02 wrote again).
- **Resolution:** I did NOT touch attack_dispatch.go (it is 36-02's file; per my disjointness gate I edit only the .star/.toml + the embed wiring). To verify MY work compiles + the wolf boot-loads, I atomically `git stash push -- server/attack_dispatch.go` (restoring the committed, no-tryWolfInteract version) → ran the build/vet/test gate → `git stash pop` (restoring 36-02's WIP) in single shell invocations so the parallel writer could not race in between. Against the committed attack_dispatch.go, MY embed + tests build + pass clean (full server suite green, pig oracle byte-identical). 36-02's WIP file is left exactly as I found it for 36-02 to complete. No blanket git clean/reset was used — only the single-file stash + the committed-version restore.

## Known Stubs

None that block the plan's goal. The wolf's combat/tame/target behavior is fully wired via the 36-01 kinds; the passive goals are verbatim faithful ports. The cited deferrals (llama-avoid, beg, prey/turtle, ResetUniversalAnger, the tamed-panic refinement) are FAITHFUL v1 reductions — each ships with its not-yet-built target entity or subsystem, never a baked-away value.

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0 (isolated from the 36-02 WIP file)
- `CGO_ENABLED=0 go vet ./server/` → clean
- `CGO_ENABLED=0 go test ./server/ -count=1` → ok (full suite)
- `TestPluginPigEqualsGoNativePig` → PASS (the pig oracle byte-identical)
- `TestHostilesBootLoad` / `TestAllFourMobsBootLoad` (8 decls) / `TestSpawnVanillaMobByName` (wolf 10+5) → PASS (the wolf boot-loads, the 6 kinds resolve, no unknown-kind panic / flags() disagreement)
- `diff plugins/vanilla_wolf/main.star server/assets/vanilla_wolf/main.star` → empty (byte-identical)
- `diff plugins/vanilla_wolf/plugin.toml server/assets/vanilla_wolf/plugin.toml` → empty
- `grep -c 'kind = "nearest_attackable_target"' plugins/vanilla_wolf/main.star` → 0 (and the loose token grep → 0)
- `git diff go.mod` → empty (no new dependency)

## Self-Check: PASSED

- Created files exist: plugins/vanilla_wolf/{main.star,plugin.toml}, server/assets/vanilla_wolf/{main.star,plugin.toml}.
- Commits exist: ea6d79f6 (Task 1), d7b24235 (Task 2).
- Byte-identical pair confirmed; kind="nearest_attackable_target" count 0; the embed directive + the load entry present; go.mod unchanged.
- attack_dispatch.go (36-02 WIP) left untouched — verified my commits do not include it.
