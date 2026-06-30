# Sulfur — Session Handoff (2026-06-30, v5 mid-flight: Phases 30.1/31/32/33 DONE + the verification bot; next: Phase 34)

> Minecraft Java 26.2 (proto 776) server in Go. Branch `ender-776`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`. NOT pushed (push to `origin/development` only on user instruction). HEAD `846eca9d`.

## ⭐ THE MANDATE
ALL gameplay/protocol logic is a LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`. Verify bytecode BEFORE writing: `JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`. Readable: `java -jar /tmp/cfr.jar --extraclasspath temp/cache/26.2-inner.jar <FQCN> --methodname <m>`. Cite class/method. Only OPTIMIZATION permitted. NO Claude attribution in commits (Claude-Session trailer OK; NEVER Co-Authored-By).

## 🛠 STANDING WORKFLOW
- **gopls diagnostics are STALE/FALSE.** Gate ONLY on real `CGO_ENABLED=0 go build ./...` + `go vet` + `go test`. Bit ~15× this session.
- **-race needs CGO=1 (Docker):** `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/`. `TestRegionPanicIsolated` prints a scary recovered-panic stack in full runs (KNOWN false alarm; passes in isolation — judge final ok/FAIL).
- **The pig oracle is BIT-FRAGILE + IS THE GATE.** `TestPluginPigEqualsGoNativePig` drives Go-native `newPigAI` vs the plugin pig byte-identical. RULE: any new RNG-drawing goal goes on BOTH `newPigAI` AND both byte-identical `vanilla_pig/main.star` copies (repo-root `plugins/vanilla_pig/main.star` + `//go:embed`'d `server/assets/vanilla_pig/main.star` — EDIT BOTH, keep `diff` empty). Oracle pig is a lone un-fed adult so Float/Panic/Tempt/Breed/Follow stay dormant (canUse-gated) → zero draws → byte-identical.
- **THE VERIFICATION BOT (built this session — USE IT):** `cmd/botmcp` (12-tool MCP stdio server, hand-rolled JSON-RPC, zero deps) + `internal/botclient` (reusable proto-776 client). Live tests gated behind the `botlive` build tag (need a running server). HOW TO LIVE-VERIFY:
  1. **KILL ALL zombie servers first** (Windows holds the port): `tasklist | grep -i sulfur`, then `taskkill //F //PID <pid>` each. `pkill -f sulfur` does NOT work on Windows. Verify the new server actually BOUND (grep "listening" in its log, NOT "bind: ... permitted").
  2. Build a uniquely-named binary + a FRESH port each run: `CGO_ENABLED=0 go build -o /tmp/sulfurN.exe ./cmd/sulfur/; SULFUR_SUPERFLAT=1 SULFUR_TEST_KIT=1 /tmp/sulfurN.exe -addr 127.0.0.1:<freshport> 2>log >/dev/null &`
  3. Run with `-count=1 -v` (non-verbose suppresses t.Logf on PASS; the loop greps miss otherwise): `SULFUR_BOT_ADDR=127.0.0.1:<port> CGO_ENABLED=0 go test -tags botlive -count=1 ./internal/botclient/ -run <Test> -v`.
  - Existing live tests: `TestLiveSpawnAndObservePig` (stroll), `TestLivePanicGoalFleesOnHit` (walk onto pig first — attack reach-gates ~3.5), `TestLiveTemptGoalFollowsCarrot` (SelectSlot(2)=carrot), `TestLiveBreedingSpawnsBaby` (Interact=feed → baby spawns).
  - The test kit has a **Carrot in hotbar slot 38 (index 2)** = pig_food (tempt + breed feed). `/dbg pig` spawns a pig (no op needed — v1 all-players-operator). Client.Interact = right-click feed.
- **Pre-decompile NEXT phase's bytecode into a `<phase>-JARNOTES.md` while the current phase's agents run** — kept every phase jar-grounded with zero idle time. 33-JARNOTES + 34-JARNOTES already done.

## ✅ DONE THIS SESSION — v5 Phases 30.1, 31, 32, 33 + the bot
The user said: "continúa autonomous, voy a estar AFK, vas a las fases completas, yo verifico al volver" + "agrégate un bot que te permita controlarlo por API/MCP para verificar tú". So: build the bot, then drive 31→36 autonomously, verifying each with the bot (no in-game pause).

- **Phase 30.1 — Faithful RandomStrollGoal target selection (BUGFIX).** The "pig walks then jams against a block forever" wedge: `getPosition` returned a raw underground Y. Re-ported `LandRandomPos.getPos` (best-of-10, x/y/z draw order, moveUpOutOfSolid snap, isStableDestination). Split: goal draws direction-only / shared Go runtime snaps (RNG-free). Code-review caught CR-01 (`wantLandMode` inverted vs jar) + off-by-one maxBuildHeightY. Live: pigs wander. **Bot built + first live-verified here.**
- **Phase 31 — PanicGoal@1 (speed 1.25).** shouldPanic = hasLastDamage && is(panic_causes); findRandomPosition = DefaultRandomPos.getPos(5,4) reusing 30.1. Added `has_last_damage` + `damage_in_tag` plugin handle. Live: a hit pig flees 4.2 blocks. Oracle green, -race clean.
- **Phase 32 — Held-Item + Item Tags (S4) / TemptGoal@4 ×2.** carrot_on_a_stick + pig_food, speed 1.2, canScare=false, TEMPT_RANGE 10, stopDistance 2.5. Added nearestPlayerHolding + itemInTag + offhandWindowSlot 45 + 2 host handles. Live: pig tempted to 0.00 by held carrot. (The live flakiness was the STALE-SERVER port conflict, NOT a bug.) Oracle green, -race clean.
- **Phase 33 — Aging + Breeding (S3) — THE DOGFOOD GATE (5 plans/5 waves).** breedAge int (NOT the existing item `age`), tickMobAging (PURE INT, OUTSIDE serverAiStep), baby half-scale hitbox (0.45 = adult×0.5) + DATA_BABY_ID (index 16, BOOL) + cross-0 broadcast; inLove + FEED path (adult→love, baby→ageUp) + hearts (via EntityEvent-18, addParticle is a server no-op) + the HEART encoder built; BreedGoal@3(1.0) + FollowParentGoal@5(1.1, EMPTY flags) + breed() (variant nextBoolean FIRST, XP 1+nextInt(7) SECOND); the **9-goal pig {0,1,3,4,4,5,6,7,8} byte-identical on BOTH halves** — THE DOGFOOD PROVES S1–S4 are 1:1. Plan-check caught the baby-hitbox blocker (MOB-SUB-08 named it, plans only did the render flag). Code-review caught CR-01 (bred baby's DATA_BABY_ID not spliced onto child.metadata → late-trackers render it full-size) — FIXED + regression test. **Live: feeding 2 pigs a carrot spawns a baby (confirmed 3×).** Oracle byte-identical, -race clean. MOB-GATE-01/02 CLOSED.

Each phase ran the full GSD loop: context (with pre-decompiled JARNOTES) → pattern-map → plan → plan-check → execute (per-wave for 33) → code-review → code-fix → verify → **bot live-verify**. Every phase: oracle byte-identical + Docker -race clean + both .star byte-identical.

## ▶ RESUME HERE — Phase 34 (cow / sheep / chicken)
**Context is WRITTEN + committed** (`.planning/phases/34-new-passive-mobs/34-CONTEXT.md` + `34-JARNOTES.md`, all bytecode jar-verified). NOT yet pattern-mapped or planned. RESUME by: pattern-map → plan (multi-plan, likely per-mob) → plan-check → execute → review → verify → bot-live.

**Phase 34 KEY FACTS (all in 34-CONTEXT.md + 34-JARNOTES.md):**
- All 3 mobs reuse the EXACT 8-goal set built in 30.1–33 (only params + food tags differ) — CHEAP (the dogfood payoff). Tags cow_food/sheep_food/chicken_food all present in data/tag.
- Attributes: cow MH10/spd0.2, sheep MH8/spd0.23, chicken MH4/spd0.25. Wire types Cow/Sheep/Chicken present in data/entity. categoryOf → CREATURE.
- PER-MOB EXTRAS (the real work): cow milking (bucket→milk_bucket, no RNG); sheep EatBlockGoal@5 (RNG gate nextInt(adjustedTickDelay(baby?50:1000)), eats grass — EDIBLE_FOR_SHEEP is a BLOCK tag NOT extracted → extend extractor OR cite-defer to grass_block-below) + shear/wool; chicken aiStep egg-lay (eggTime nextInt(6000)+6000, +2 nextFloat sound) + slow-fall (deltaMovement.y *= 0.6 falling).
- REGISTRATION: the mobRegistry `byName` map is multi-mob ready. Generalize `LoadVanillaPigRegistry`→load all 4 embeds; `spawnVanillaPig`→`spawnVanillaMob(name)`; natural spawn (async.go:336) + `/dbg <mob>` + the bot spawn levers. Each new mob = a `vanilla_<mob>/main.star` (declare_mob + the shared goal callbacks) in BOTH repo-root + server/assets, byte-identical.
- The PIG ORACLE stays byte-identical (the pig is UNTOUCHED — cow/sheep/chicken are separate mobs). The new RNG goals (sheep EatBlock, chicken egg) need their own lockstep IF that mob is dogfooded Go-vs-plugin; else a per-mob behavior test. Planner decides — lean to per-mob behavior test + focused RNG tests.

Then Phase 35 (hostiles zombie/skeleton/spider + targetSelector + spawn rules) + Phase 36 (wolf — neutral, the most complex single mob). Both still need their JARNOTES pre-decompiled.

## STATE OF THE TREE
- HEAD `846eca9d`, branch `ender-776`. NOT pushed. Tree clean of tracked changes.
- `CGO_ENABLED=0 go build ./...` exit 0; vet clean; full server/tag/loot suite green; the 9-goal pig oracle byte-identical; Docker -race clean; both .star byte-identical.
- Phases 30.1/31/32/33 complete in ROADMAP. 34 context-ready (no plans yet). 35/36 not started.
- Untracked noise (ignored): `.claude/`, `*.log`, `web/`, `world/region/`, `*.exe`. Test servers: kill any stray `sulfur*.exe` with taskkill (see workflow).

## CARRYOVER / DEFERRED (cited, non-blocking)
- 33-deviations.md: same-region breeding cut (pre-accepted), baby eye-height 0.40625 (AABB ships), PigVariant assign (draw preserved), Age/InLove NBT persist, hearts-via-EntityEvent-18, pig eat-sound no-op, WR-01/02/03 (partner-cache rescan, breed orb count — masked by the single-candidate oracle, RNG-lockstep-safe).
- `TestPerfGate` intermittently overshoots its wall-clock cap under full-suite parallel contention (passes in isolation, skipped under -race) — a pre-existing wall-clock-vs-alloc-count flake, not a regression; the real fix (assert alloc count) is noted in perf_gate_test.go.
- The wandermob full removal (embed + tests + testbot gate) — still deferred (only boot-load removed earlier).
- `TestBehaviorRegressionMobSpawns` global-RNG spawn flake — inject a seeded *rand.Rand (own task).
- Prod root password rotation (operator action, earlier session).

## MILESTONES SHIPPED (reference)
v1.0 MVP (1–9) · v2 Worldgen+Structures (10–16) · v3 Online+OperatorUX (17–20) · v4 Plugin/Scripting (21–28). v5 Mob Behaviors IN FLIGHT: 29, 30, 30.1, 31, 32, 33 DONE (the pig is the full 9-goal dogfood, byte-identical); 34 context-ready; 35, 36 remain.
