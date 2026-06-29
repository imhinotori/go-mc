# Phase 24: Vanilla mobs AS plugins (1:1 dogfood) — Context

**Gathered:** 2026-06-28
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 24-RESEARCH.md (HIGH-confidence, jar-disassembled)
**Requirement:** PLUGIN-04

<domain>
## Phase Boundary

**Delivers:** the FIRST 1:1 dogfood — the vanilla mob AI is re-expressed AS Starlark plugins (via the Phase-23 declare_mob/goal/handle API), staying a LITERAL method-for-method port of the 26.2 jar. This VALIDATES the plugin API expresses real vanilla AI (v4-PLAN: "if vanilla AI doesn't fit the API, the API is wrong — caught here, before Python"). The research PROVED the Phase-23 API does NOT yet fully express vanilla AI — so Phase 24 FAITHFULLY EXTENDS the handle API to cover the gaps, ports the pig's goals 1:1, fixes the per-entity RNG, and SWAPS the Go-native pig for the plugin pig (existing tests become the behavior-identical proof).

**THE 1:1 MANDATE FULLY APPLIES** (unlike Phase 23's custom mob). Every re-expressed goal + every new ported goal + the RNG + the handle extensions are LITERAL jar copies, cited (class + method), verified against `temp/cache/26.2-inner.jar` bytecode BEFORE writing.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Extend the Phase-23 handle API — all 3 gaps (the dogfood point)
The research mapped each pig goal's needs against the Phase-23 handle surface and found 3 concrete gaps. EXTEND the handle API faithfully to cover them (this IS the "API is wrong → fix it" deliverable):
1. **`entity.set_look(yaw, pitch)`** — LookAtPlayer/RandomLookAround SET `e.headYaw`/`e.yaw`; no look-mutate seam exists. Add it as a tick-owned mutator (faithful to how the Go goals set look). Capability: entities.write.
2. **`world.nearest_player(x,y,z, range, [predicate])`** — players are NOT in the entityStore; `entities_near` returns mobs only. LookAt/Tempt/FollowParent need the nearest player. Add a player-query seam (faithful to vanilla's `getNearestPlayer`/`getNearestEntity` + the targeting conditions). Capability: world.read (or a new entities.read scope for players).
3. **Per-entity RNG** — the Go goals use package `math/rand/v2` (global); vanilla draws from a per-entity `RandomSource`. Add `entity.rand_int(n)`/`rand_float()` backed by a PER-ENTITY SEEDED source that reproduces the BYTECODE DRAW ORDER (see below).
- Surface a likely 4th gap if the new goals need it: per-mob state across the frozen Starlark boundary (breeding/age/panic flags) — the planner resolves (research Open-Q §6). Likely a small tick-owned per-entity state bag the handle reads/writes through a seam.

### Per-entity seeded RNG — bytecode draw order (1:1 critical; ALSO retires a flake)
- Replace the global `math/rand/v2` in the AI with a PER-ENTITY seeded RandomSource that reproduces vanilla's EXACT draw order (the research confirmed from bytecode: e.g. RandomStroll = `getRandom().nextInt(reducedTickDelay(interval))` THEN `getPosition()`). The draw order determines behavior — it must match the jar method-for-method.
- This ALSO retires the `TestTickAIDrivesMobs` flake (STATE.md notes it "needs a seeded source per the 1:1 mandate") AND helps the `TestServerAiStepWalksToGoalTarget` flake (logged in STATE) — own both: the seeded source makes AI deterministic so the timing flakes resolve.

### SWAP — the plugin pig is the ONLY pig
- Replace `newPigAI()` at its call sites (server/async.go:312, server/debug.go:167) with the plugin-driven build (the pig's `e.ai` built from the Starlark `vanilla_pig` declaration, NOT the Go goals). The plugin pig becomes the only pig. The EXISTING pig-AI tests become the behavior-identical contract (they must pass against the plugin path UNCHANGED). Requires boot-time plugin-load wiring (a Phase-23-deferred follow-up — wire the host to load the bundled vanilla plugins at server start).
- Add `TestPluginPigEqualsGoNativePig` (or assert the existing TestServerAiStep* suite passes against the plugin pig) as the explicit behavior-identical proof.

### Mob scope — pig's EXISTING 3 goals + the NEW vanilla goals (operator-directed: "pig + portar goals nuevos")
- **Existing Go goals to re-express 1:1** (server/ai_goals_passive.go): RandomStroll (@6 MOVE), LookAtPlayer (@7 LOOK), RandomLookAround (@8 MOVE|LOOK) — each a literal port of WaterAvoidingRandomStrollGoal/LookAtPlayerGoal/RandomLookAroundGoal.
- **NEW vanilla pig goals to BUILD + port 1:1** (the operator wants the fuller pig AI): the rest of `Pig.registerGoals()` — FloatGoal (@0), PanicGoal (@1), BreedGoal (@2/3 area), TemptGoal, FollowParentGoal. EACH is a literal jar port. BUT each needs a prerequisite the research flagged as "unbuilt":
  - **FloatGoal** needs in-water detection — the v3 fluid/breath work has water detection (`eyeInWater`/fluid surface); reuse/extend it for the mob `isInWater`. Likely buildable now.
  - **PanicGoal** needs a damage→AI hook (panic on hurt) — the Phase-22 on_damage event + a per-mob "was hurt" flag the goal reads. Wire it faithfully.
  - **BreedGoal/TemptGoal/FollowParentGoal** need entity AGING + breeding state + held-item detection (the player holding wheat for Tempt; baby/adult age for Breed/FollowParent). These are NEW subsystems.
- **The "no built-but-unwired" rule still governs:** for each new goal, the planner decides — if its prerequisite subsystem is small + faithfully buildable in this phase (Float's water-check, Panic's hurt-flag), BUILD it 1:1 and port the goal. If a prerequisite is a LARGE unbuilt subsystem (full entity aging/breeding for Breed), the planner either (a) builds the minimal faithful slice the goal needs, cited, or (b) DEFERS that specific goal with a cited reason in the backlog (do NOT ship a goal whose subsystem is faked). The OUTCOME the operator wants: as much of the real vanilla pig AI as can be ported 1:1 this phase, with any deferral explicitly justified — not a silent skip.
- Each ported goal: javap the jar class, match priority + flags + canUse/canContinueToUse + the RNG draw order + the numeric constants EXACTLY. Cite the class.

### Where the plugin lives + boot-load
- A bundled `vanilla_pig` plugin (a `.star` + manifest) shipped with Sulfur (e.g. an embedded/default plugins dir). The host loads the bundled vanilla plugins at boot (the wiring the SWAP needs). Capabilities declared (entities.write for set_look/velocity, world.read for nearest_player, nav). Scope the exact bundling (//go:embed the .star vs a default plugins/ dir) at planning.
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 24 row + gate (rewrite Go mob AI AS plugins, 1:1, behavior-identical, existing tests green).
- `.planning/REQUIREMENTS.md` — PLUGIN-04 full.
- `.planning/phases/24-vanilla-mobs-as-plugins/24-RESEARCH.md` — HIGH-confidence: only the pig has Go AI; the 3 (likely 4) handle gaps; the jar anchors (Pig.registerGoals + each goal class disassembled); the RNG draw-order trap; SWAP-not-parallel; the boot-load requirement; the flakes.
- `.planning/phases/23-entity-mob-behavior-api/23-RESEARCH.md` + the 23 SUMMARYs + `server/plugin_entity.go`/`plugin_capability.go`/`plugin_mob_decl.go`/`plugin_mob_ai.go` — the Phase-23 API being dogfooded + extended.
- The port source: `server/async.go` (newPigAI + the 2 call sites), `server/ai_goals_passive.go` (the 3 existing goals), `server/ai_goal.go` (Goal interface), `server/ai_mob.go` (serverAiStep), `server/debug.go:167` (the other newPigAI site). The v3 fluid/breath water-detection for FloatGoal.
- `CLAUDE.md` — **the 1:1 mandate (FULLY APPLIES here — javap before writing, cite the class)**, CGO=0, -race Docker, push development, TICK-05.
- The jar: `temp/cache/26.2-inner.jar` via `javap -c -p` (`/c/Program Files/Zulu/zulu-25/bin/javap`): `net.minecraft.world.entity.animal.Pig` (registerGoals + createAttributes), `net.minecraft.world.entity.ai.goal.{FloatGoal,PanicGoal,BreedGoal,TemptGoal,FollowParentGoal,WaterAvoidingRandomStrollGoal,LookAtPlayerGoal,RandomLookAroundGoal}`, the RandomSource draw order.
</canonical_refs>

<specifics>
## Specific Ideas

- The behavior-identical proof: the existing `TestServerAiStep*` / pig-AI suite passes against the plugin pig UNCHANGED after the swap. If a test's expectation depends on the OLD global-rand behavior, it must be updated to the SEEDED-source behavior (the seeded source is the 1:1-correct one — the test asserts the jar-faithful sequence, with the seed fixed for determinism).
- The seeded RNG is the linchpin: it fixes the flakes AND makes the 1:1 port verifiable (a fixed seed → a deterministic, bytecode-matching sequence the test pins).
- For each NEW goal, the planner produces: the javap of the jar class + the prerequisite-subsystem decision (build-minimal-1:1 vs defer-with-reason). Float + Panic are the most likely buildable now; Breed/Tempt/FollowParent may defer the aging/breeding subsystem (cited).
- Capability: the bundled vanilla plugin declares its real capabilities; enforcement (Phase 23) applies to it like any plugin.
- -race: the swapped plugin pig ticking is -race clean (Docker CGO=1); the seeded source is per-entity tick-owned (no shared global rand race — another flake source removed).

## Open items the planner resolves
- The exact final handle-extension surface (the 3 + possible 4th per-mob-state seam).
- Per-new-goal: build-the-minimal-subsystem-1:1 vs defer-with-cited-reason (Float/Panic likely build; Breed/Tempt/FollowParent likely defer the aging subsystem — but the planner verifies what's actually needed).
- The boot-load wiring (embed the .star vs a default plugins dir).
- Which existing tests become the behavior-identical contract + which need the seeded-source expectation update.
</specifics>

<deferred>
## Deferred Ideas

- Mobs OTHER than the pig (no Go AI exists for them) — future, when their subsystems land.
- Any new pig goal whose prerequisite subsystem is too large to build faithfully this phase → deferred WITH a cited reason (not silently skipped).
- Python runtime → Phase 26; Folia → Phase 27.
</deferred>

---

*Phase: 24-vanilla-mobs-as-plugins*
*Context gathered: 2026-06-28 via operator decisions + v4-PLAN.md + 24-RESEARCH.md*
