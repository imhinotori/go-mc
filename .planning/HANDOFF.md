# Sulfur — Session Handoff (2026-06-25)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. **Branch `ender-776`.** NEW push target since the CI/CD work: **`development`** (= GHCR `:latest`), NOT `main`. `git push origin ender-776:development`. Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## RESUME HERE — execute Phase 16 (the LAST v2 phase), then close milestone v2

Milestone **v2 (worldgen features + structures)** is **6/7 phases done**. Only **Phase 16 (Village Jigsaw + Structures Visual Gate)** remains. Its 3 plans are **written + plan-checked + committed + pushed** (commit `fc58840e`) — NOT executed. Execute them, then the visual gate closes v2.

**Why this was paused, not executed:** the prior session hit 72% context. Phase 16 has the 3 HEAVIEST executors of v2 (483 village `.nbt` files + the jigsaw BFS Placer + the visual gate) — running them at high context risked splitting the work. The compact is the clean handoff point: plans committed, nothing half-done.

**Execute order (sequential, each depends on the prior):**
- **16-01** — the `.nbt` StructureTemplate system: binary `.nbt` embed (incl. the `empty.json` terminator pool — the plan-check caught it was outside the `village/` prefix) + runtime gzip+nbt parse (REUSE the existing `nbt` package, no 2nd lib) + `placeInWorld` rotation/mirror (reuse `transformState(st, mirror, rotation)` at `world/structure/piece.go:176` — mirror 2nd, rotation 3rd) + the processors. **62** village template_pools (NOT 74 — that was stale pre-26.2), 483 `.nbt`, 40 processor_lists.
- **16-02** — the bounded-BFS `JigsawPlacement.Placer` (SequencedPriorityIterator work queue, NOT stack recursion; bounded SIMULTANEOUSLY by maxDepth 6 + max_distance 80 + VoxelShape collision; depth-0 → `minecraft:empty` fallback terminator) + the village `random_spread` StartGenerator (salt 10387312 / spacing 34 / sep 8 / 5 biome variants as one `villages.json` structure_set, each gated by `has_structure/village_*`, biome-gated NO accept-by-default, registered into `CompositeStartGenerator`).
- **16-03** — full structures acceptance + **THE autonomous:false VISUAL GATE** (the v2-closing gate): a real client finds villages (5 variants) + temples + mineshafts + strongholds, deterministic per seed. This is a BLOCKING human-verify — PAUSE for the user's real-client check. The executor prints the salt-10387312-derived village chunk for the fixed seed so the user can find one.

**How to run each plan:** spawn a `gsd-executor` agent per plan (the prior session delegated every executor to keep the orchestrator lean). After each: verify `go build ./...` exit 0 + `go vet ./...` + `go test ./world/... ./world/structure/`, then the Docker `-race` gate **with `-timeout 1800s`** (the structures `./world` suite hits ~834s with structures live — a timeout dump, NOT a race; 15-03 found this). Commit atomically, push to `development`, then the next plan. At 16-03's visual gate, STOP and present the checklist to the user.

## Milestone v2 progress (Phases 10-16; v1 = Phases 1-9, shipped + tagged v1.0)

| Phase | Status |
|-------|--------|
| 10 Worldgen Foundation (LCG + cross-chunk seam + live heightmap) | ✅ |
| 11 Feature Pipeline & Decoration Orchestration | ✅ |
| 12 Core Feature Types (ore/patch/selectors/...) | ✅ |
| 13 Trees + Dungeon + Features VISUAL GATE | ✅ (gate APPROVED — "árboles y vines" on a real client) |
| 14 Structure Pipeline & Temples (desert pyramid/jungle/igloo/swamp hut) | ✅ |
| 15 Mineshaft & Stronghold | ✅ |
| **16 Village Jigsaw & Structures VISUAL GATE** | 🔄 **planned + plan-checked + pushed; NOT executed (RESUME HERE)** |

After 16's visual gate approves → run `/gsd-complete-milestone` for v2 (archive roadmap/requirements, tag, like v1.0 was).

## CI/CD + PRODUCTION DEPLOY (done this session — live)

- **GHCR image** `ghcr.io/imhinotori/sulfur` (PUBLIC). `Dockerfile` = pure-Go CGO=0 static → distroless, EXPOSE 25565. `.github/workflows/docker-publish.yml`: **development → `:latest`**, **main → `:stable`**, + immutable `:sha-<commit>`; a prune step keeps the 3 newest versions.
- **Branch model (NEW):** `development` = latest (THIS is where work goes now — the user said "post-CI trabajás sobre development, no main"), `main` = stable (currently at `34915b1a` = v2-features+Phase-14; promote to it when something is stable).
- **Production server:** `151.242.242.206` / `demo.trysulfur.net` (Debian 13, root SSH). Docker 29 + **Watchtower** (the maintained `ghcr.io/nicholas-fedor/watchtower` fork — the `containrrr` one crash-looped on Docker 29) auto-updates `:latest` every 5min (pull+recreate+cleanup). Sulfur runs on **:25565**, volume `sulfur-world:/app` persists the world, `--restart unless-stopped`. Idempotent redeploy script at `/root/deploy-sulfur.sh`. Every push to `development` → CI builds `:latest` → Watchtower deploys to prod in ≤5min.
- ⚠️ **USER MUST ROTATE THE ROOT PASSWORD** — it was sent in plaintext in the chat (exposed). Recommend SSH-key auth + disable password login. The deploy used it only in ephemeral SSH askpass (never persisted to disk/repo). NEVER commit it.

## Performance fixes (done this session — the user reported "muuy lenta")

Per-chunk gen was ~296ms (real streaming); fixed to ~88ms (~3.3×). Two fixes, both committed+pushed:
1. `world/feature_ore.go` — cache the decoded `OreConfiguration` per `*ConfiguredFeature` (`sync.Map`). `decodeOreConfig`→`resolveOreTagSet` scanned all ~30K block states PER ore placement. ~20%.
2. `world/decoration.go` `retainedBiomes` — READ the biomes from each chunk's per-section 4×4×4 palette container (FillBiomes wrote them) instead of RE-SAMPLING the multi-noise climate (6 density fns + RTree) over the whole 96-level vertical column × 16 cols × 9 neighborhood chunks (~13800 climate evals/chunk = ~79% of Decorate). Decorate 216ms→52ms.
- The "Connection reset / packet handling error" the user hit was a STALE binary running in bg — a rebuild fixed it; NOT a real wire bug (heightmaps + StateIDs verified in-range).
- `server/navigation.go` — fixed a latent pathfinding bug: `requestPath` armed the recompute cooldown on a DROPPED async submit, so under sustained CPU contention (heavy worldgen) every submit dropped + the cooldown throttled the retry forever → mob sat motionless. Now arms cooldown ONLY on an accepted submit. (Surfaced as flaky `TestTickAIDrivesMobs` under concurrent world load.)

## CRITICAL GOTCHAS (unchanged + new)

1. **STALE LSP/gopls** floods FALSE diagnostics after every wave (`undefined: X`, `redeclared`, `does not implement`, `tools/ undefined logf`, syntax errors in test files). ALL FALSE — trust `go build ./...` (exit 0) + `go vet` + `go test`. NEVER revert based on gopls. `tools/` is a SEPARATE Go module (gopls confuses it).
2. **`-race` needs Docker** (host CGO_ENABLED=0): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./world/...`. The `-timeout 1800s` is REQUIRED now (structures push ./world to ~834s).
3. **Push target is `development` now**, not `main` (CI: development→latest deploys to prod).
4. **Port-from-jar mandate** (user, standing): worldgen + structure LOGIC ported DIRECTLY from the decompiled jar (`temp/cache/26.2-inner.jar`, javap -c / CFR), idiomatic Go, no GPL paste, cite the class. The plan-checker DECOMPILES the jar to verify — it caught a real Phase-14 bug this session: the `FrequencyReductionMethod` enum had `legacy_type_1`↔`legacy_type_3` SWAPPED + `legacyProbabilityReducerWithDouble` mis-seeded `(z,salt)` instead of `(chunkX,chunkZ)` — fixed in 15-01.
5. **CGO_ENABLED=0 must stay clean** (pure-Go static binary, no JVM — the value prop + the Docker image). No new deps in v2 (reuse nbt/xsync/ants/singleflight/klauspost-compress).
6. **Determinism is the contract**: every structure/feature is pure over (seed, pos); the 5×5-region-two-request-orders byte-identity test (`TestDecorationReorderIdentical`) + `TestEmitOnce` must STAY green after every plan. The worldgen adds NO new wire surface (chunk format sealed in v1 Phase 4) — gates are VISUAL, not capture-diff.
7. **The cross-chunk seam** (Phase 10): a chunk decorates/places-structures into its 3×3 neighborhood; the worker holds-at-carved + the single scheduler goroutine owns staging (no locks, -race clean). Structures use the ±8 compute-on-demand REFERENCES (a structure owned ≤8 chunks away is found) + placeInChunk clips each piece to the target chunk's writable box (idempotent once-per-overlapping-chunk).

## Key paths
- Structures: `world/structure/` (the pipeline + StructurePiece machinery + temples/mineshaft/stronghold; Phase 16 adds the templatesystem + jigsaw Placer + village here). Pieces write through `world/neighborhood.go` (the WorldGenView).
- Features: `world/levelgen/feature/` + `world/levelgen/placement/` + `world/feature_*.go` (the bodies in package `world`, registered via `registerFeatureBody`). `world/decoration.go` = applyBiomeDecoration. `world/noisegen.go` = the Generator (GenerateTerrain + Decorate split).
- Embedded data: `world/levelgen/data/` (//go:embed) + `tools/extract_worldgen.go` (the extractor — extend `worldgenZipPrefixes`/`worldgenSingleFiles` for new data). Jar: `temp/cache/26.2-inner.jar` (gitignored, unobfuscated).
- Plans: `.planning/phases/16-village-jigsaw-structures-gate/16-0{1,2,3}-PLAN.md`. Research: `.planning/research/v2-structures.md` (Tier 4 = village jigsaw).
- Infra: `Dockerfile`, `.dockerignore`, `.github/workflows/docker-publish.yml`.

## Deferred (NOT bugs — v3, documented)
Loot tables (chests = block + loot tag, no contents), structure entities (villagers/witch/cat/silverfish — spawner is block-only), `afterPlace` terrain-beard, structure NBT persistence (starts recompute on demand — pure over seed+pos). ONLINE-01/02 auth+encryption, REGION-01 Folia. **TUI (bubbletea + bubbles) + disconnect-reason logs** — the user requested these mid-session; deferred to AFTER v2 closes (their explicit choice: "terminar v2 primero").
