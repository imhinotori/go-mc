---
phase: 36-wolf-neutral
plan: 04
subsystem: mob-test-gate
tags: [wolf, tamable, neutral-mob, anger, sit, follow-owner, skeleton-target, boot-load, gate, race, v5-milestone, go]

# Dependency graph
requires:
  - phase: 36-wolf-neutral
    plan: 01
    provides: "the wolf TamableAnimal/NeutralMob state + the parameterized target goal (angerGate/skeleton class) + isAngryAt + the gametime-endpoint anger trigger + the sit/follow_owner/owner-hurt goal classes"
  - phase: 36-wolf-neutral
    plan: 02
    provides: "tryWolfInteract/tryToTameWolf (BONE nextInt(3) tame → HP 8→40 + owner + sit) + the owner sit-toggle"
  - phase: 36-wolf-neutral
    plan: 03
    provides: "the vanilla_wolf //go:embed plugin (15-goal registerGoals: 10 goalSelector + 5 targetSelector) + the completed boot-load wiring (the embed directive + vanillaMobNames entry)"
  - phase: 35-hostiles
    provides: "the cow/skeleton _test.go harness shape (loadVanilla*Registry temp-dir + *Loop + spawn*) + the hostiles_embed_test.go byte-identity gate + combatTestPlayer + the target-goal forceTrigger seam"
provides:
  - "TestWolfBootLoads — the real //go:embed wolf boot-load + the holistic 10 goalSelector + 5 targetSelector shape + CREATURE + MAX_HEALTH 8.0 / ATTACK_DAMAGE 4.0 + the anger-gated player + skeleton goals"
  - "TestWolfTame/Sit/FollowOwner — the tame (nextInt(3) → HP 8→40 + owner + sit), the SitWhenOrderedToGoal park, the FollowOwnerGoal moveTo-want band, end-to-end on the boot-loaded wolf"
  - "TestWolfWildNoAggro — the WILD-UN-HIT-NO-AGGRO must-have (isAngryAt gate holds; the forceTrigger isolates the gate; the un-gated scan proves the player IS in range)"
  - "TestWolfAngerOnHit — the ANGER-ON-HIT must-have (angerEndTime = gameTime + 400 + nextInt(381) + angerTarget at the combat store-point; the angry_player_target goal THEN acquires; the gametime-endpoint expiry)"
  - "TestWolfSkeletonTarget — the skeleton_target acquire (in-range yes / far no, no anger gate)"
  - "TestWolfTameRNG/TestWolfAngerRNG — the focused lockstep draw-order proofs (nextInt(3) tame; 400+nextInt(381) anger then the hurt-sound nextFloat() tail)"
  - "the wolf added to embedByteIdenticalMobs (T-36-09 anti-drift) + TestHostilesBootLoad's real-registry wolf assertion"
affects: []  # the LAST v5 plan — no downstream consumer

# Tech tracking
tech-stack:
  added: []  # no new Go dependency (go.mod unchanged)
  patterns:
    - "Drive-the-REAL-boot-loaded-goal: targetGoalOfType(e, class) finds the wolf's actual targetSelector goal (with its angerGate wired) so a gate test exercises the shipped goal, not a hand-built one"
    - "Isolate-the-gate proof: forceTrigger bypasses the RNG roll so ONLY the anger gate can block the target; a paired un-gated scan proves the candidate IS in range (the no-aggro proof is non-vacuous)"
    - "Lockstep-through-the-full-path: the anger RNG proof replays the EXACT post-hit stream (nextInt(381) anger → nextFloat()×2 hurt-sound) so the follow-up draw agreement pins the bound + the single anger draw despite the survival hurt-sound also drawing"

key-files:
  created:
    - server/wolf_test.go
  modified:
    - server/hostiles_embed_test.go
    - cmd/sulfur/main.go

key-decisions:
  - "The anger focused-RNG test (TestWolfAngerRNG) proves the single nextInt(381) anger draw via BOTH the explicit endpoint (angerEndTime == gameTime + 400 + ref's first nextInt(381)) AND a full-stream lockstep that replays the post-anger hurt-sound nextFloat()×2 draws — because applyDamageEntity's survival branch plays the hurt sound (combat_mob.go playMobHurtSound getVoicePitch) which ALSO draws from the wolf's mob stream AFTER the anger trigger; a naive one-draw probe would falsely desync"
  - "The wolf byte-identity gate lives in a NEW embedByteIdenticalMobs list (not hostileEmbedSpecs) because the wolf is a CREATURE, not in the MONSTER boot-load loop; TestHostileEmbedByteIdentical iterates the full 4-mob list (3 hostiles + wolf), TestHostilesBootLoad asserts the wolf boot-loads separately"
  - "The cmd/sulfur/main.go boot log undercounted the mobs (7, omitting the wolf) — fixed to '8 mobs ... + wolf' (a Rule 1 in-scope fix: the boot smoke must log all 8 loaded)"

patterns-established:
  - "A holistic phase GATE that ASSERTS the per-mob must-haves end-to-end from the REAL //go:embed boot-load (not the temp-dir harness alone) — the wolf is proven shipped, tamed, sitting, following, neutral (no-aggro), provoked (anger-on-hit), and skeleton-hunting in ONE suite"

requirements-completed: [MOB-NEUT-01, MOB-NEUT-02]

# Metrics
duration: 8min
completed: 2026-06-30
---

# Phase 36 Plan 04: The Wolf Gate Summary

**THE PHASE-36 ACCEPTANCE GATE (the LAST v5 plan) — a holistic wolf test suite that asserts the neutral/tameable wolf END-TO-END from the real //go:embed boot-load: it boots as the 8th mob (10 goalSelector + 5 targetSelector, CREATURE, MH 8.0 / ATK 4.0), tames on a BONE (nextInt(3) → HP 8→40 + owner + sit), sits, follows its owner, does NOT aggro a player on sight (the isAngryAt gate holds), DOES retaliate when hit (angerEndTime = gameTime + 400 + nextInt(381) → the angry_player_target goal acquires the attacker → the anger expires at the gametime endpoint), and hunts a skeleton — with the tame + anger draws pinned by focused lockstep RNG tests. The pig oracle stays byte-identical, the Phase-35 hostiles stay green, all 8 mob embed pairs are byte-identical, and the Docker -race + strictRegion gate is clean.**

## Performance

- **Duration:** ~8 min
- **Tasks:** 2/2 automated (Task 3 is the live human-verify checkpoint — deferred for the bot, see below)
- **Files:** 1 created (server/wolf_test.go), 2 modified (hostiles_embed_test.go, cmd/sulfur/main.go)
- **Commits:** 7bf5948a (Task 1 — the wolf gate suite), c30bf946 (the boot-log wolf fix)

## Accomplishments

### Task 1 — The wolf gate suite + the embed assertions (commit 7bf5948a)

**`server/wolf_test.go`** (13 tests, all green), mirroring the cow/skeleton harness:
- **harness**: `loadVanillaWolfRegistry` (temp-dir from `../plugins/vanilla_wolf`), `wolfLoop`, `spawnWolf` (the shared `spawnDeclaredMob` path), `targetGoalOfType(e, class)` (finds the REAL boot-loaded `nearestAttackableTargetGoal` of the wanted class so a test drives the shipped goal with its `angerGate` wired).
- **TestWolfBootLoads**: loads the REAL `//go:embed` registry (`loadVanillaMobRegistry`) AND the temp-dir harness; asserts `entity.Wolf.ID`, 10 goalSelector + 5 targetSelector, the PLAYER-class goal carries a non-nil `angerGate` (else it'd aggro on sight), the SKELETON-class goal exists with a nil gate, MAX_HEALTH 8.0, ATTACK_DAMAGE 4.0, CREATURE, and the wolf in the tick store.
- **TestWolfTame**: a BONE feed on a forced-0 stream → `tame` + HP 40 + MAX_HEALTH 40 + owner + `orderedToSit` + bone consumed; a forced-!=0 stream → smoke, untamed, HP 8, no owner, bone STILL consumed.
- **TestWolfSit**: `SitWhenOrderedToGoal.canUse` true on a tamed orderedToSit grounded wolf; the goal claims MOVE+JUMP; `start()` sets `inSittingPose`; un-ordering releases it; `stop()` clears the pose.
- **TestWolfFollowOwner**: owner 20 blocks away → `canUse` true + `tick()` issues a nav want (`hasTarget`); owner 1 block away → `canUse` false (`distSqr 1 < startDistance² 100`) + `canContinueToUse` false.
- **TestWolfWildNoAggro** (the no-aggro-on-sight must-have): a WILD UN-HIT wolf (`angerEndTime == 0`) with a player ~1 block away acquires NO target — `forceTrigger` bypasses the RNG gate so ONLY the `isAngryAt` gate can block; a paired bare (un-gated) scan proves the player IS in range, so the no-aggro proof is non-vacuous.
- **TestWolfAngerOnHit** (the anger-on-hit must-have): drives a player melee through `applyDamageEntity` (the combat store-point); asserts `angerEndTime == gameTime + 400 + nextInt(381)`, `angerTarget == attacker`, `isAngryAt(attacker)` true; the `angry_player_target` goal THEN acquires the attacker + commits it via `start()`; advancing the clock past `angerEndTime` makes `isAngryAt` false (the gametime-endpoint expiry — no decrement).
- **TestWolfSkeletonTarget**: a skeleton in FOLLOW_RANGE is acquired via `skeleton_target` (nil anger gate); a skeleton beyond FOLLOW_RANGE is not.
- **TestWolfTameRNG / TestWolfAngerRNG**: focused lockstep draw-order proofs — `nextInt(3)` (tame, exactly one draw) and `400 + nextInt(381)` (anger) with the full post-hit stream (`nextInt(381)` then the hurt-sound `nextFloat()×2`) replayed so the follow-up draw agreement pins the bound + the single anger draw.

**`server/hostiles_embed_test.go`**: added `embedByteIdenticalMobs` (the 3 hostiles + the wolf) driving `TestHostileEmbedByteIdentical` (T-36-09 embed-vs-root anti-drift), and a wolf boot-load assertion in `TestHostilesBootLoad` (the wolf is a CREATURE, asserted separately from the MONSTER loop).

### Task 2 — Docker -race + strictRegion gate (commit c30bf946 for the boot-log fix)

- The standing **Docker -race gate** ran VERBATIM (the Phase-34/35 pattern from 36-JARNOTES):
  ```
  MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/
  ```
  **Result: `ok  github.com/imhinotori/sulfur/server  18.341s`** — the FINAL line is `ok`, no race, no recovered-panic stack surfaced. The wolf's new state (the `angerEndTime` gametime-endpoint, the `angerTarget`/owner bookkeeping, the tame/sit DATA_FLAGS broadcast) is tick-owned (TICK-05); only immutable snapshots cross the tick→broadcast seam. The documented carryover flake (`TestBehaviorRegressionMobSpawns`) did not surface this run.
- **The boot-log fix** (`cmd/sulfur/main.go`): the vanilla-mobs boot line still read 7 mobs (omitting the wolf) after 36-03 wired the wolf into the embed registry. Fixed to `pig/cow/sheep/chicken + zombie/skeleton/spider + wolf ... (8 mobs ...)` so the headless boot smoke confirms all 8.

### Task 3 — Live bot verify (DEFERRED to the orchestrator's bot — see below)

The plan's Task 3 is a `checkpoint:human-verify` and `autonomous: false`. The user is AFK, so I did the headless verification I can and recorded a deferred VERIFY checkpoint for the orchestrator's bot.

## Verification (ALL automated gates green)

- `CGO_ENABLED=0 go build ./...` → exit 0
- `CGO_ENABLED=0 go vet ./server/` → clean
- `CGO_ENABLED=0 go test ./server/ -count=1` → **ok** (full suite, 19.9s)
- `CGO_ENABLED=0 go test ./server/ -run 'TestWolf'` → all 13 wolf tests PASS (8 taming from 36-02 + the 5+ holistic/RNG from this plan)
- **`TestPluginPigEqualsGoNativePig` → PASS (the pig oracle BYTE-IDENTICAL)** — the wolf is `entity.Wolf.ID`-gated; zero new pig draws
- `TestNearestAttackableTargetGateUsesTen` → PASS (the Phase-35 target test UNCHANGED — the B1/B2 parameterization is additive)
- `TestHostileEmbedByteIdentical / TestHostilesBootLoad / TestAllFourMobsBootLoad (8 decls) / TestSpawnVanillaMobByName (wolf 10+5)` → PASS
- **Docker -race gate** (verbatim, above) → **final line `ok`, -race clean over the wolf/mob packages**
- **All 8 mob embed pairs byte-identical** (`diff` empty): pig / cow / sheep / chicken / zombie / skeleton / spider / wolf (both main.star + plugin.toml)
- **Headless boot smoke**: the built `sulfur` binary boots, logs `vanilla mobs: ... + wolf plugins boot-loaded (8 mobs ...)` + `Sulfur listening on :25599 (protocol 776, 26.2)`, then killed cleanly (`taskkill //F //PID`). The bare-filesystem `plugins/` hot-reload watcher's `skipping "vanilla_wolf" (undefined: declare_mob)` line is the PRE-EXISTING, identical-for-all-8-mobs dual-path behavior (the embed registry injects `declare_mob`; the bare watcher correctly skips them) — NOT a wolf regression.
- `git diff go.mod go.sum` → empty (no new dependency; CGO_ENABLED=0 preserved)

## Deferred live-bot VERIFY checkpoint (for the orchestrator to bot-verify)

Task 3 (`checkpoint:human-verify`) needs a live client/bot. The AUTOMATED gates fully prove the wolf's logic (the 13 unit/integration tests cover tame/sit/follow/no-aggro/anger-on-hit/skeleton end-to-end, and the boot smoke confirms the live server loads + serves the wolf). The remaining LIVE confirmation, for the orchestrator's bot, is the in-world end-to-end:
1. `/dbg wolf` near a player → a wolf spawns (eid in chat).
2. Stand next to the FRESH wild wolf empty-handed → it does NOT attack (wild-no-aggro on sight).
3. Right-click the untamed wolf with BONEs → smoke (status 6) until eventually hearts (status 7) + the wolf becomes yours + sits (the 1/3 chance).
4. Right-click the tamed wolf empty-handed → sit-toggle; walk >10 blocks → it follows; within ~2 blocks it stops.
5. `/dbg wolf`, then HIT it once → it turns and attacks back (anger-on-hit), calms after ~20-39s.
6. Spawn a skeleton near a fresh wild wolf → the wolf attacks the skeleton.

This is a VERIFY-only headless-impossible step (it needs the rendering/interaction client), recorded — NOT silently dropped.

## Consolidated Phase-36 DEFER list (recorded across all 4 plans — never silently dropped)

- **WolfAvoidEntityGoal<Llama> @3** — no Llama mob in v1; ships with the Llama (36-01/03).
- **BegGoal @9** — client-only head-tilt visual; no observable server behavior (36-03).
- **NonTameRandomTargetGoal<Animal>@5 + <Turtle>@6** — untamed prey-hunting; needs PREY_SELECTOR + prey/turtle mobs (36-03).
- **ResetUniversalAngerTargetGoal @8** — OMITTED: UNIVERSAL_ANGER gamerule defaults FALSE (never fires) + the gametime-endpoint anger model has no decrement to reset (W6-dissolved, 36-01).
- **The TamableAnimalPanicGoal tamed/owner-guard refinement** — reuses the cow PanicGoal core at speed 1.5; the tamed water/away tick refinement is cite-deferred (36-03).
- **The tamed feed-heal / collar-dye / body-armor equip+repair** — no health-feed/dye/equipment system in v1 (36-02).
- **The FollowOwner teleport (tryToTeleportToOwner)** — only the moveTo path-follow ships; `shouldTryTeleportToOwner` is a cited constant-false (36-01).
- **The owner-UUID WIRE broadcast (DATA_OWNERUUID index 19)** — the server-side ownerUUID drives the goals; the client owner-glow/collar render defers (36-01/02).
- **owner.getLastHurtByMob (OwnerHurtByTargetGoal + the SitWhenOrderedToGoal 144.0 guard)** — players carry no inbound lastHurtByMob in v1 → cited constant-false; the owner ATTACK side (getLastHurtMob, OwnerHurtTargetGoal) IS live (36-01).
- **The TAME_ANIMAL advancement trigger** — no advancement system in v1 (36-02).
- **The FollowOwner WATER pathfinding-malus save/restore** — no per-PathType malus subsystem in v1 (36-01).
- **canAttack/wantsToAttack TargetingConditions (LoS/team/invisibility)** — the same cited no-ops the Phase-35 goals carry (the existence check is the active gate).

## v5 milestone completion readiness

**This is the LAST v5 plan (the LAST mob of the LAST v5 phase).** With this gate green, Phase 36 (the wolf — the most complex single mob: taming + owner + sitting + neutral anger + skeleton hunting) is complete, and v5 ("Mob Behaviors & Living-Entity Subsystems") has all 9 phases done. The vanilla mob roster (pig/cow/sheep/chicken/zombie/skeleton/spider/wolf — 8 mobs) ships as jar-faithful `//go:embed` Starlark plugins, all byte-identical to their repo-root copies, all -race clean, with the pig oracle byte-identical throughout. **v5 is ready for the milestone-completion verification** (the only open item is the live-bot VERIFY checkpoint above + the standing carryover spawner-flake fix tracked in STATE.md, neither a wolf regression).

## Self-Check: PASSED

- Created files exist: server/wolf_test.go (+ hostiles_embed_test.go, cmd/sulfur/main.go modified).
- Commits exist: 7bf5948a (Task 1 — the wolf gate suite), c30bf946 (the boot-log fix).
- All automated verification gates green (build/vet/full-suite, 13 wolf tests, pig oracle byte-identical, Phase-35 hostiles green, Docker -race ok, 8 embed pairs byte-identical, headless boot smoke, go.mod empty).
