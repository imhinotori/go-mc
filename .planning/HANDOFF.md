# Sulfur — Session Handoff (2026-06-24, late)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. Branch `ender-776`, pushes to `main` (`git push origin ender-776:main`). Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir on disk is `D:\ender`. Last pushed: `3aebc557`.

## Where we are

**8/9 phases COMPLETE (v1 server) + Phase 9 (vanilla worldgen) at 99% — terrain generates, but 2 structural port divergences vs 1:1 vanilla remain (audited, see below).**

| Phase | Status |
|-------|--------|
| 1 Foundation/Codegen | ✅ |
| 2 Net & Protocol | ✅ |
| 3 Tick Loop | ✅ |
| 4 World & Chunks | ✅ |
| 5 Player Session / FIRST PLAYABLE | ✅ |
| 6 Entities/Physics/Interaction | ✅ |
| 7 AI/Pathfinding/Commands/Chat | ✅ (ported mob logic from decompiled jar per user mandate) |
| 8 Leaf Async Optimizations | ✅ (async path/tracker/spawn behind seams; -race clean) |
| **9 Stretch — Vanilla Worldgen (PARITY-01 only)** | 🔄 **9/9 plans + perf fix + both fidelity fixes DONE (aa619b81); awaiting visual real-client gate** |

ONLINE-01/02 + REGION-01 are deferred v2 (user chose PARITY-01 only for Phase 9).

## RESUME HERE — Phase 9 worldgen fidelity DONE (2 fixes applied), VISUAL GATE pending

Both audited fidelity fixes are **applied + committed + pushed** (`aa619b81`). The worldgen MATH was already bit-exact; these closed the 2 structural port divergences. What remains is the **VISUAL real-client gate** (the user reconnects, walks the world, confirms sharper cliffs + noodle caves present + bare-stone cave rims). No more code work is queued unless the visual check surfaces something.

**FIX #1 DONE — per-marker interpolation (terrain SHAPE):** `world/levelgen/density/mapall.go` (NEW — ports `DensityFunction.mapAll(Visitor)`: bottom-up tree rewrite, identity-memoized for the shared DAG) + `world/levelgen/noisechunk/interpolator.go` (the interpolator is now `*interpolatedFn`, a `density.Function` that `MapAll` substitutes for each `Marker(Interpolated)`; `Compute` returns the trilerped value inside the cell loop via a shared `fillState.filling` flag, samples its inner filler directly outside it — porting `NoiseInterpolator.compute`'s `ctx==this$0` discriminant) + `world/levelgen/noisechunk/noisechunk.go` (`wrapFinalDensity` collects all **5** overworld interpolators = main density `mul` + 4 cave branches; `fill` drives every interpolator on the cell grid then evaluates the rewritten `final_density` PER BLOCK). `TestNoiseChunkCornerExact` still passes (corner-exact preserved); `TestDensityFieldHasCaves` passes. Per-block compute raised gen cost ~70ms→~116ms/chunk — vanilla's real cost, absorbed off-tick by the Phase-8 async-gen seams.

**FIX #2 DONE — surface before carvers (APPEARANCE):** `world/noisegen.go` Generate swapped to `FillChunk → BuildSurface → ApplyCarvers` (was fill→carve→surface), mirroring vanilla ChunkStatus `NOISE → SURFACE → CARVERS`. Carved cave/ravine openings now expose bare stone instead of grass/dirt/sand rims.

**FIX #3 (skipped — cosmetic, measure-zero):** RTree tiebreak order; only matters on exact-equal-fitness biome boundaries. Not worth it.

DEFERRED (NOT bugs — do not "fix"): trees/vegetation/ores-as-features/structures (separate feature/decoration + structure subsystems, deliberately out of PARITY-01 scope). `temperatureCondition` stubbed false (surface/rules.go:314). These are why the world lacks trees — scope, not a port bug. They are the natural next milestone if the user wants deeper gameplay (the user already flagged: items/crafting, more mobs, block mechanics — explicitly a FUTURE milestone).

Verification done: `go build ./...`, `go test ./...` (all green), `go test -race` in Docker golang:1.26 on `./world/...` + `./world/levelgen/noisechunk/` (clean), binary builds. The ONLY thing left for Phase 9 is the user's visual confirmation on a real client.

## What just happened this session (Phase 9 + the disconnect fix)

- Phase 9 planned (9 plans, 7 waves) + plan-checked (caught 1 blocker: the density node-set was assumed-not-jar-walked, missing `find_top_surface`+`invert`, inventing `weird_scaled_sampler` — fixed). All 9 executed:
  - 09-01 extract+embed 111 worldgen JSON (2.8MB) from the jar. 09-02 Xoroshiro seeding BIT-EXACT + noise primitives (caught `getOctaveNoise` reversed-index from bytecode). 09-03 the 29 density node types + data-driven graph parser (caves are in the graph = negative density). 09-04 NoiseChunk cell-sample + trilerp (THE per-marker interpolation shortcut = FIX #1). 09-05 Aquifer + OreVeinifier + doFill. 09-06 carvers (ravines + tunnels). 09-07 surface rules + multi-noise biomes. 09-08 NoiseGenerator assembly. 09-09 wire into cmd/sulfur + spawn-Y from real surface.
- DISCONNECT BUG (real client kept disconnecting): root cause was **985ms/chunk** — `biome.ParameterList.findValue` did a linear O(7594) scan per quart-cell. FIXED by porting vanilla's Climate RTree (O(log n), behavior-identical: `TestRTreeMatchesLinearScan` = 50000 points, same biome exactly) + a per-chunk biome cache → **70ms/chunk (14×)**. Commits `77a60d31`/`3aebc557`. The disconnect should be gone (user to re-confirm) but the 1:1 divergence (#1/#2 above) is separate.
- Also fixed CI/CD this session (was the go-mc fork's stale workflows): rewrote `.github/workflows/{go.yml,codeql-analysis.yml}` for Sulfur — current action versions (checkout@v7, setup-go@v6, upload-artifact@v7, codeql@v4), `go-version-file: go.mod` (reads the real `go 1.25.0`), main branch, -race + CGO=0 jobs, tools/ module job. + `.golangci.yml` (lint Sulfur core, exclude fork-inherited + test Close noise). The Go CI build/test/-race/vet/tools jobs pass; lint passes with the config. (Note: `gh` CLI defaults to the upstream `Tnze/go-mc` remote — use `gh ... --repo imhinotori/sulfur`.)

## How to run / test the worldgen
- `go run ./cmd/sulfur` → noise terrain, seed 0x5EEDC0DE (1592639710), listens :25565 proto 776. `-seed N` overrides. `SULFUR_SUPERFLAT=1` falls back to the flat stub. `SULFUR_DEBUG=1`/`SULFUR_DEBUG_NAV=1`/`SULFUR_DEBUG_DAMAGE=1` are the debug triggers (pig + obstacle nav + damage).
- Build the binary to test (don't `go run` in background — port-conflict if an old one lingers; `taskkill //F //IM sulfur.exe` first).
- Bench: `BenchmarkGenerateOneChunk` in `world/noisegen_bench_test.go` (~70ms/chunk now).

## CRITICAL GOTCHAS (unchanged from prior sessions — still true)
1. **STALE LSP** — gopls floods FALSE diagnostics after every wave (`could not import go-mc`, `undefined: <new symbol>`, `*server.gameTick does not implement server.GamePlay`, `packetid.ServerboundAttack undefined`, `entity.SulfurCube undefined`, import-cycle-in-test for the moved levelgen sub-packages). ALL FALSE — gopls hasn't re-indexed. Trust `go build ./...` (exits 0) + `go test`. NEVER revert imports or re-declare symbols.
2. **`-race` needs Docker** (host CGO_ENABLED=0). `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./world/...` (or ./server/...). Plain go build/vet/test native.
3. **Import-cycle discipline for levelgen**: a `package levelgen` file importing `density`/`router`/`biome`/`synth` CYCLES. Every levelgen sub-component lives in its OWN package (`density/`, `router/`, `noisechunk/`, `biome/`, `surface/`, `carver/`, `synth/`, `data/`). FIX #1's tree-rewrite must respect this.
4. **Port-from-jar mandate** (user, standing): worldgen + mob LOGIC ported DIRECTLY from the decompiled jar (javap), translated faithfully (idiomatic Go, no GPL paste, cite bytecode). The math is bit-exact BECAUSE of this discipline — keep it for FIX #1.
5. **Capture-diff / real-client is the only 776 wire truth** — but worldgen adds NO wire surface (fills the Phase-4-sealed chunk format), so its gate is the VISUAL real-client check + determinism, not a capture-diff.
6. **`CGO_ENABLED=0` must stay clean** (pure-Go static binary, no JVM — the value prop). The one external dep added in Phase 8/9 is `klauspost/compress` (pure-Go zstd, OPT-05 linear region). No cgo.

## Key paths
- Worldgen: `world/levelgen/` (the 8 sub-packages) + `world/noisegen.go` (assembly) + `world/levelgen/data/` (embedded JSON). Plans: `.planning/phases/09-stretch-online-worldgen-regions/09-0{1..9}-*.md`.
- The Generator seam: `world/generator.go` (`Generator interface { Generate(level.ChunkPos) *level.Chunk }`). NoiseGenerator + Superflat both implement it. Swapped in `cmd/sulfur/main.go`.
- Vanilla jar (capture-diff/javap, gitignored): `temp/cache/26.2-inner.jar` (unobfuscated) + `26.2-server.jar`. Java 25 + Docker available.
- Audit detail: the full fidelity-audit report is in this session's transcript (the 2 fixes above are its top findings).

## v1 status (separate from the worldgen polish)
The v1 server (Phases 1-8) is COMPLETE + playable: a real vanilla 26.2 client logs in, walks a ticking world (now noise terrain), places/breaks blocks, has inventory, takes damage/dies/respawns, persists, sees mobs that spawn/wander/navigate with ported vanilla AI, runs commands, chats — all -race clean with Leaf-style async optimizations. Phase 9 is the stretch polish (worldgen 1:1); the 2 fixes above are the remaining gap to true parity.
