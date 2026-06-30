# Sulfur — Session Handoff (2026-06-29, mid-v5: Phases 29+30 done + in-game debug sweep → next: Phase 31 PanicGoal)

> Minecraft Java 26.2 (proto 776) server in Go. Branch `ender-776`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`. NOT pushed (push to `origin/development` only on user instruction). HEAD `85e1be00`.

## ⭐ THE MANDATE
ALL gameplay/protocol logic is a LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`. Verify bytecode BEFORE writing: `JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`. Readable decompile: `java -jar /tmp/cfr.jar --extraclasspath temp/cache/26.2-inner.jar <FQCN> --methodname <m>` (slow, scope with --methodname). Cite class/method. Only OPTIMIZATION (provably identical behavior) permitted. NO Claude attribution in commits (Claude-Session trailer OK).

## 🛠 STANDING WORKFLOW (proved out all session)
- **gopls diagnostics are STALE + FALSE.** Every edit throws a flood of "undefined/missing method" diagnostics — ALL FALSE. Gate ONLY on the real `CGO_ENABLED=0 go build ./...` + `go vet` + `go test`. Run them yourself after every change. This bit us ~10 times this session; always re-verify with the real compiler.
- **-race needs CGO=1 (Docker); ship build is CGO=0.** `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/ ./data/tag/ ./level/loot/`. NOTE: `TestRegionPanicIsolated` prints a scary recovered-panic stack inside a full -race run that LOOKS like a failure but the test passes in isolation (known false alarm).
- **Live debug:** `./run-debug.sh` (online-mode + SULFUR_TEST_KIT + persist + ULTRA_DEBUG firehose → sulfur.log, seed 777, bg). Firehose categories: packet move water tick fluid edit combat bandwidth skin. `udebug("<cat>", ...)` is the firehose log helper (server/ultradebug.go).
- **NEW `/dbg` operator command** (server/commands_dbg.go): `/dbg pig` (spawn a vanilla pig at you), `/dbg water` (5x5x5 water box around you), `/dbg pig-in-water` (both). Built this session so I can reproduce mob-physics bugs in-game myself. **BUT** in-game requires a real client — the FASTER path proved to be a headless Go test driving `spawnVanillaPig` + `tickPhysics` (caught the wandermob-vs-pig confusion in one test). Prefer a headless repro test over asking the user to re-test.
- **The pig oracle is BIT-FRAGILE.** `TestPluginPigEqualsGoNativePig` drives the Go-native `newPigAI` vs the plugin pig, byte-identical over 500 ticks. The oracle world is DRY (fillFloor only, no water) → FloatGoal.canUse is false → zero new draws → adding FloatGoal stayed byte-identical. RULE: add any new RNG-drawing goal to BOTH `newPigAI` AND `vanilla_pig/main.star` IN LOCKSTEP, in the SAME plan, draw confined to the goal's tick() callback. There are TWO main.star copies: the repo-root `plugins/vanilla_pig/main.star` AND the `//go:embed`'d `server/assets/vanilla_pig/main.star` (the boot-load source of truth) — edit BOTH.

## ✅ DONE THIS SESSION — v5 Phases 29 + 30 shipped (+ a big in-game debug sweep)
**v5 milestone** (Mob Behaviors & Living-Entity Subsystems, Phases 29–36) started this session via /gsd-new-milestone (research + requirements + roadmap, all committed). Then /gsd-autonomous drove Phase 29 + 30:

### Phase 29 — Damage Keystone (S2) — COMPLETE (MOB-SUB-01/02/03)
The parallel `*Entity` mob damage pipeline (`combat_mob.go`: applyDamageEntity/actuallyHurtEntity, the SIBLING of the *tickPlayer combat.go path — dual attribute systems forced a parallel path, not an interface). `damageSource{typeTag, attacker}` + genuine `is(tag)` over a jar-extracted tag table (`data/tag/`, new `GenTags.java` codegen extracting BOTH damage-type AND item tags). Cross-region `damageIntent` barrier-queue (mirror transferIntent, route via owner region NEVER cur()). Death+loot+XP (`death_mob.go`: dieEntity, the v3 loot evaluator generalized to entity tables). Then a LIVE-DEBUG sweep (all 1:1, oracle green): red flash (`ClientboundDamageEvent` — first damage-event packet), knockback, hurt sound + death sound (`ClientboundSoundEntity` — first sound packet in the codebase), XP-orb pickup (`xp_orb.go`: orb tick/followNearbyPlayer/playerTouch), full death animation (delayed removal via `tickDeath`: status-3 at death → 20 ticks fall-over → status-60 poof + remove). Code review caught + fixed a real BLOCKER (structure-spawned mobs born at 0 hp → permanently invulnerable; `initSpawnHealth` centralized).

### Phase 30 — JumpControl + Fluid (S1) — COMPLETE (MOB-SUB-04/05)
mob fluid predicates (`mobInWater`/`mobFluidHeight`/`mobInLava`, mirror playerInWater AABB scan) + lava decode (extended `fluidState.isLava`/`decodeFluid`, audited every `.isWater` consumer). `jumpControl` + `noJumpDelay` + the aiStep jump branch (`jumpInLiquid` vy += 0.03999999910593033 exact double, `jumpFromGround` max(0.42,vy)). FloatGoal@0 lockstep on both pigs. Code review caught a real BLOCKER (CR-01: `floatGoal` didn't override `canContinueToUse` → inherited baseGoal→true → kept running forever out of water, desyncing the oracle; fixed to delegate to canUse, vanilla Goal default). Then a LIVE-DEBUG sweep:
- **Mob water physics** (`fluid_travel.go`: `travelInWaterVertical` — vertical drag 0.8 + reduced gravity baseGravity/16=0.005) so a pig stops sinking at the dry 0.08 rate and FloatGoal keeps it afloat.
- **Mob fall damage** (`mob_fall_damage.go`: wired into the Phase-29 applyDamageEntity keystone). CRITICAL FIX: accumulate the ACTUAL post-move displacement `e.y - yBefore` (vanilla Entity.move → checkFallDamage), NOT the pre-move velocity intent — a standing mob's gravity intent (-0.08) is clipped by the floor so actual deltaY≈0; accumulating the intent piled up phantom fallDistance → constant damage on flat ground.
- **The SULFUR_TEST_KIT spawn egg now spawns vanilla_pig** (was the wandermob — a v4 custom mob with ONE MOVE goal and NO FloatGoal, so it sank in water and MASKED the working vanilla-pig float; cost a long debug detour until a headless test revealed the egg spawned the wrong mob).
- **The wandermob is no longer boot-loaded** (cmd/sulfur/main.go) — the dogfood is REAL vanilla mobs as plugins (vanilla_pig now, cow/sheep/etc. Phase 34), not a toy. The wandermob embed (`server/wandermob_embed.go`) + its tests (`plugin_mob_test.go`, `region_*_test.go`, `cmd/testbot/gate.go`) STILL EXIST — a dedicated cleanup pass should remove them (deferred; touching them needs care to not break the API-gate tests).

## ▶ RESUME HERE — Phase 31 (PanicGoal), via /gsd-autonomous
v5 is mid-flight: Phases 29+30 complete (4+3 plans, all verified, oracle green, -race clean), Phases 31–36 remain. `/gsd-autonomous` will pick up Phase 31 next (it re-reads ROADMAP, finds the first incomplete phase). The autonomous loop is: smart-discuss (grey areas via AskUserQuestion) → plan-phase (pattern-mapper + planner + plan-checker) → execute-phase (sequential executors on main tree — the plans are sequential single-plan waves) → code-review + verifier in parallel → route → next phase.

**Phase 31 = PanicGoal (S2 consumer)** — the one the USER specifically wants: passive mobs FLEE when hit. It reads the `lastDamageSource` the Phase-29 keystone built (`source.is(PANIC_CAUSES)` tag). PanicGoal@1 on the pig, lockstep Go+plugin. Per `deferred-goals.md`: PanicGoal canUse = `getLastDamageSource() != null && lastDamageSource.is(panicCausingDamageTypes)` then look for water / a random flee position; tick navigates away at speedModifier 1.25. javap `net.minecraft.world.entity.ai.goal.PanicGoal`. The keystone provides everything it needs (lastDamageSource, the damage-type tags). NOTE: PanicGoal draws RNG (flee-pos search) → the oracle-lockstep rule applies (add to both pigs, same plan, dry-oracle stays green since canUse needs a damage source the oracle pig never receives).

Then 32 (Held-Item+Tags / TemptGoal), 33 (Aging+Breeding = pig-parity DOGFOOD GATE), 34 (cow/sheep/chicken), 35 (hostiles+spawn), 36 (wolf). Full scope in `.planning/REQUIREMENTS.md` + `.planning/research/SUMMARY.md`.

## STATE OF THE TREE
- HEAD `85e1be00`, branch `ender-776`. NOT pushed. Tree clean of tracked changes.
- `CGO_ENABLED=0 go build ./...` exit 0; `go vet` clean; full server/tag/loot suite green; pig oracle GREEN; no gopy leak; Docker -race clean (server+tag+loot).
- The live server runs (run-debug.sh). The egg spawns vanilla_pig.
- Untracked noise (ignored): `.claude/`, `*.log`, `web/`, `world/region/`, `sulfur.exe`.

## CARRYOVER / DEFERRED (cited, non-blocking)
- **The wandermob full removal** — embed + tests + testbot gate (see above). A dedicated cleanup.
- **`TestBehaviorRegressionMobSpawns` flake** (async_stress_test.go) — intermittent under `-count`/full -race ("async spawner added no mob over N cycles"). ROOT: the spawner picks candidate positions via GLOBAL unseeded `math/rand/v2` (spawner.go:360); under `-count` the global RNG carries across reruns. NOT a Phase-29/30 regression. FIX (own task): inject a seeded `*rand.Rand` into the spawner. Documented in STATE.md.
- The 3 Phase-30 code-review WARNINGs (lava in flow primitives, un-cached per-tick fluid re-scans) — cited-deferral hazards for the deferred lava-flow work, not regressions.
- Vanilla water buoyancy/`travel` HORIZONTAL swim-speed (the 0.02 moveRelative) — the wet test is a differential (FloatGoal pig sinks slower), the full horizontal swim is a cited follow-on.
- Prod root password rotation (operator action, from an earlier session).

## MILESTONES SHIPPED (reference)
v1.0 MVP (1–9) · v2.0 Worldgen+Structures (10–16) · v3 Online+OperatorUX (17–20) · v4 Plugin/Scripting System (21–28). v5 Mob Behaviors IN FLIGHT (29–30 done, 31–36 remain).
