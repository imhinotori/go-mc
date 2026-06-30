---
phase: 35-hostiles
plan: 06
subsystem: mob-integration
tags: [hostiles, boot-load, embed, dbg, integration-gate, pig-oracle, race, mob-as-plugin, zombie, skeleton, spider]

# Dependency graph
requires:
  - phase: 35-01
    provides: "the targetSelector machinery + meleeAttackGoal + nearestAttackable/hurtByTarget target goals + the TARGET-flag buildAIFromDecl routing + the mob-attack damage path"
  - phase: 35-01b
    provides: "the goal-kind seam + the 3 hostile .star pairs (vanilla_zombie/skeleton/spider, byte-identical) dealing REAL melee through the Go-native goals + the zombie/skeleton/spider_test.go behavior tests"
  - phase: 35-02
    provides: "the categoryOf MONSTER cases + the monsterCap + isDarkEnoughToSpawn day/night gametime proxy + the natural-spawn MONSTER pass + the 3 hostile mob-name consts (vanillaZombie/Skeleton/SpiderMobName)"
  - phase: 34-mob-as-plugin
    provides: "the generalized loadVanillaMobRegistry boot-load + vanillaMobNames load order + the //go:embed vanillaMobFS + spawnVanillaMob(name) + the /dbg passive-mob template + the byte-identical embed-pair invariant"
provides:
  - "the 3 hostiles boot-load into the ONE tick-owned registry (the //go:embed vanillaMobFS directive + vanillaMobNames both extended to all 7 mobs)"
  - "the /dbg zombie | skeleton | spider operator levers (cloned from the /dbg cow shape, operator-gated by command.tp)"
  - "TestHostileEmbedByteIdentical — the embed-vs-root byte-identity gate (T-35-12) for all 3 hostiles"
  - "TestHostilesBootLoad — the LOUD real-//go:embed boot-load assertion (all 3 hostiles register with their goal/target sets + MONSTER category through loadVanillaMobRegistry)"
  - "the shipped binary boot-loads all 7 mobs; the boot log reports them"
affects: [36-wolf, hostile-natural-spawn-live-verify, lighting-engine]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the hostile boot-load is the EXACT additive extension Plan 34-04 made for cow/sheep/chicken: grow the //go:embed directive + vanillaMobNames; the loader + the LOUD re-assertion are category-agnostic (a MONSTER declares goal+target sets exactly as a CREATURE declares goals — ZERO loader change)"
    - "the embed-vs-root byte-identity gate is a table-driven test reading plugins/vanilla_<hostile>/ and server/assets/vanilla_<hostile>/ and asserting bytes.Equal per file (T-35-12: the embedded manifest governs the swap's caps, so the operator-visible copy must == the shipped copy)"
    - "the live AFK-checkpoint stand-in is a botlive-tagged test (internal/botclient/live_hostiles_test.go) that /dbg-spawns a zombie at the bot and asserts it CLOSES distance (hunt) + drops the bot's health (melee through the Phase-29 keystone)"

key-files:
  created:
    - server/hostiles_embed_test.go
  modified:
    - server/vanilla_pig_embed.go
    - server/commands_dbg.go
    - server/vanilla_mob_test.go
    - cmd/sulfur/main.go

key-decisions:
  - "The 3 hostile mob-name consts (vanillaZombie/Skeleton/SpiderMobName) already existed in vanilla_pig_embed.go (added by 35-02 as the canonical name home); this plan ONLY grew the //go:embed directive + appended them to vanillaMobNames — no new const, no duplicate."
  - "TestAllFourMobsBootLoad's exact-count assertion (was len==4) was updated to len==7 (4 passives + 3 hostiles, additive). The passive type/goal-count checks are unchanged; the total guards against an accidental extra/missing entry in vanillaMobNames. This is a Rule-1 fix: the additive boot-load made the old exact-count stale, not a behavior change."
  - "The cmd/sulfur boot log was updated to report all 7 boot-loaded mobs (was 'pig/cow/sheep/chicken'). The general-plugin-host 'skipping ... undefined: declare_mob' scan lines for all 7 are EXPECTED (the repo-root plugins/ dir is scanned by the general host WITHOUT the declare_mob builtin; the mobs load via the SEPARATE LoadVanillaMobRegistry embed path that DOES inject it) — identical to the pre-existing 4-passive behavior."

# Metrics
duration: "~40m"
completed: 2026-06-30
tasks: 2
files-created: 1
files-modified: 4
---

# Phase 35 Plan 06: THE GATE — Boot-Load the 3 Hostiles + /dbg + -race + Live Summary

The phase-35 integration + acceptance gate. 35-01..05 built the targetSelector machinery, the spawn gating, and the 3 hostile plugins in isolation (temp-dir harnesses). This plan WIRES them into the shipped binary's boot-load + the operator /dbg path, runs the full -race integration, asserts the embed-vs-root byte-identity, confirms the pig oracle stays byte-identical with the hostiles bundled, and live-verifies (headlessly, via the botlive harness — the user is AFK) that a hostile spawns at the player, hunts, and deals real melee damage.

## What shipped

- **Boot-load (vanilla_pig_embed.go):** the `//go:embed vanillaMobFS` directive grew to include `assets/vanilla_zombie assets/vanilla_skeleton assets/vanilla_spider`; `vanillaMobNames` grew from 4 to 7 so all 7 mobs load into the ONE tick-owned registry. The loader (`loadVanillaMobRegistry`/`loadOneVanillaMob`) and the LOUD re-assertion are category-agnostic and needed ZERO change.
- **/dbg levers (commands_dbg.go):** added `case "zombie" | "skeleton" | "spider"` (each cloned byte-for-byte from the `/dbg cow` shape, routing through the name-parameterized `spawnVanillaMob`); the usage string lists the 3.
- **The embed-vs-root byte-identity gate (hostiles_embed_test.go, NEW):** `TestHostileEmbedByteIdentical` asserts `bytes.Equal` for `plugin.toml` + `main.star` across the repo-root and embed copies of all 3 hostiles (T-35-12). `TestHostilesBootLoad` calls the REAL `loadVanillaMobRegistry()` (the //go:embed path, not a temp-dir harness), asserts all 3 hostiles + the 4 passives register, then spawns each hostile through that boot-loaded registry asserting its wire type + goalSelector/targetSelector goal counts (zombie 4+2, skeleton 4+2, spider 6+2) + the MONSTER category.
- **Boot-log + stale-count fixes:** `cmd/sulfur/main.go` boot log now reports all 7 mobs; `TestAllFourMobsBootLoad` asserts the registry now holds 7 declarations (additive).

## Verification (all gates GREEN)

- `CGO_ENABLED=0 go build ./...` — exit 0. `go vet ./server/` — clean.
- `go test ./server/ -count=1` — full suite **ok** (17.5s). `TestPluginPigEqualsGoNativePig` **byte-identical** with the 3 hostiles bundled. The 3 hostile behavior tests + `TestHostileEmbedByteIdentical` + `TestHostilesBootLoad` pass.
- **Docker -race (verbatim recipe):** `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/` — final line **ok** (15.6s). No data race. No carryover flake hit this run.
- **All 7 embed pairs byte-identical** (pig/cow/sheep/chicken/zombie/skeleton/spider — `diff` empty for both files each).
- `git diff --stat go.mod go.sum` — **empty** (no new dependency; CGO_ENABLED=0 preserved).
- **Boot smoke:** the server boots on a fresh port and logs `vanilla mobs: bundled 1:1 pig/cow/sheep/chicken + zombie/skeleton/spider plugins boot-loaded` then `Sulfur listening on ... (protocol 776, 26.2)`.
- **Live (botlive, headless — AFK stand-in for the human checkpoint):** `TestLiveZombieHuntsAndAttacks` against a live server: zombie spawned at the bot (dist 0.79) → **HUNTED** (closed to 0.40 blocks: target acquired + pathed) → **ATTACKED** (bot health 19.3 → 0.0: REAL melee damage through the Phase-29 keystone). Both PASS assertions fired.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated the stale exact-count assertion in TestAllFourMobsBootLoad**
- **Found during:** Task 2 (the full server suite run)
- **Issue:** `TestAllFourMobsBootLoad` asserted `len(r.byName) == 4` exactly. Adding the 3 hostiles to `vanillaMobNames` (the plan's core action) correctly makes the registry hold 7, so the exact-count check failed ("registry holds 7 declarations, want 4").
- **Fix:** changed the assertion to `wantTotal = 7` (4 passives asserted here + 3 hostiles asserted by `TestHostilesBootLoad`). The per-passive type/goal-count checks (the `allFourMobs` table) are unchanged. The intent (no silent missing/extra mob) is preserved and strengthened.
- **Files modified:** server/vanilla_mob_test.go
- **Commit:** 3449089e

**2. [Rule 1 - Bug] Updated the stale cmd/sulfur boot-log line**
- **Found during:** Task 2 (the boot smoke)
- **Issue:** the boot log said "pig/cow/sheep/chicken plugins boot-loaded" — misrepresenting reality after the additive hostile boot-load (all 7 now load).
- **Fix:** the log now reads "pig/cow/sheep/chicken + zombie/skeleton/spider plugins boot-loaded".
- **Files modified:** cmd/sulfur/main.go
- **Commit:** 3449089e

## Deferrals (carried per plan + this session)

- **The natural-night-spawn live observation + the spider daylight-gate + the MONSTER-cap-holds live checks** were NOT run as a live-bot test (no such harness exists; only `live_hostiles_test.go`'s /dbg-spawn hunt+attack test). They ARE covered HEADLESSLY by the 35-02 spawn-gating tests + the spider daylight tests (all green in the full suite). See the deferred VERIFY checkpoint below for the orchestrator to bot-verify.
- **The FORCED gametime-darkness proxy** (35-02 `isDarkEnoughToSpawn`): full light-propagation cave/block-light spawning is the follow-up (lighting engine).
- **The skeleton ranged-bow goal** is deferred (the wave-1 subset gives the skeleton a melee goal; the bow/RangedAttackGoal is a later cite-deferred goal).
- **The per-hostile cite-deferred goals** (the registerGoals subsets noted in 35-03..05) remain as documented in those plans.

## Threat Flags

None. The /dbg hostile levers reuse the existing `command.tp` operator gate (T-35-13 mitigated, identical to the /dbg cow gate); the embed-vs-root byte-identity gate mitigates T-35-12; the MONSTER cap (35-02) bounds the natural-spawn DoS (T-35-14, accept).

## Self-Check: PASSED

- server/hostiles_embed_test.go — FOUND
- .planning/phases/35-hostiles/35-06-SUMMARY.md — FOUND
- commit 0bee1a76 (Task 1) — FOUND
- commit 3449089e (Task 2) — FOUND
