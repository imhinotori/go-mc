# Phase 9: Validation Architecture (PARITY-01 — FULL parity)

> Generated from `09-RESEARCH.md` `## Validation Architecture`. `workflow.nyquist_validation` is enabled `[VERIFIED: config.json]`. This is the nyquist pre-flight test map for the Phase-9 plans (09-01…09-08).
>
> **Scope note:** RESEARCH's test map was written for the T0 recommendation; the phase is now **FULL PARITY** (see the supersede banners in RESEARCH + the ROADMAP + the 09-0N plans). The per-tier commands below are unchanged (they target the same packages), but the FULL-parity plans add the cave/aquifer/ore-vein/carver/biome tests in 09-03…09-08; the determinism + chunk-encode + visual gates are the same.

## Test Framework

| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no external runner) |
| Config file | none — `go test` convention |
| Quick run command | `go test ./world/... ./world/levelgen/...` |
| Full suite command (race) | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./world/...` `[VERIFIED: prior-phase Docker -race pattern]` |

## Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? | Plan |
|--------|----------|-----------|-------------------|-------------|------|
| PARITY-01 (Tier A) | `Xoroshiro` reproduces known output vectors for a fixed seed | unit (golden vectors) | `go test ./world/levelgen/ -run TestXoroshiro` | ❌ Wave 0 → 09-02 | 09-02 |
| PARITY-01 (Tier B) | `NormalNoise.getValue` matches known noise samples at fixed coords | unit (golden) | `go test ./world/levelgen/synth/ -run TestNormalNoise` | ❌ Wave 0 → 09-02 | 09-02 |
| PARITY-01 (Tier C nodes) | The FULL jar-walked node set (29 types incl. find_top_surface + invert) + parser build the embedded graph (incl. cave functions) | unit | `go test ./world/levelgen/density/ -run 'TestUnaryAndArithmeticNodes\|TestYGradientAndRangeChoice\|TestSplineCubic\|TestParseResolvesStringRef\|TestParseCaveFunctions\|TestInterpolatedMarkerPreserved\|TestUnsupportedNodeErrors'` | ❌ Wave 0 → 09-03 | 09-03 |
| PARITY-01 (Tier D router) | The ENTIRE noise_router (all 15 named functions, incl. preliminary_surface_level) parses + binds + evaluates deterministically | unit | `go test ./world/levelgen/ -run 'TestParseNoiseSettings\|TestRandomStateSeedsNoises\|TestFinalDensityEvaluable\|TestParseFullGraphEndToEnd\|TestParsePreliminarySurfaceLevel'` | ❌ Wave 0 → 09-03 | 09-03 |
| PARITY-01 (Tier E cells) | `NoiseChunk` cell-samples + trilerps `final_density` (incl. caves) into a per-block density field | unit | `go test ./world/levelgen/ -run 'TestNoiseChunk\|TestInterpolator'` | ❌ Wave 0 → 09-04 | 09-04 |
| PARITY-01 (aquifer/ore) | Aquifer fluid table + OreVeinifier consume barrier/fluid_level_*/lava/vein_* | unit | `go test ./world/levelgen/ -run 'TestAquifer\|TestOreVein'` | ❌ Wave 0 → 09-05 | 09-05 |
| PARITY-01 (carvers) | CaveWorldCarver (tunnels) + CanyonWorldCarver (ravines), replaceables-gated, aquifer-aware, cross-chunk, seeded | unit | `go test ./world/levelgen/carver/ -run 'TestParseCarverConfigs\|TestCanReplaceGate\|TestApplyCarversSeeded\|TestCarverAquiferAware\|TestCaveCarves\|TestCanyonCarvesRavine\|TestCarveDeterministic\|TestCarveContinuousAcrossChunks'` | ❌ Wave 0 → 09-06 | 09-06 |
| PARITY-01 (surface/biome) | SurfaceRules subset + multi-noise Climate biome source (uses preliminary_surface_level via above_preliminary_surface) | unit | `go test ./world/levelgen/ -run 'TestSurface\|TestClimate\|TestBiomeSource'` | ❌ Wave 0 → 09-07 | 09-07 |
| PARITY-01 (determinism) | `Generate(pos)` is PURE — same seed+pos → identical chunk bytes | unit (reuse the Superflat purity test shape) | `go test ./world/ -run TestNoiseGenDeterministic` | ❌ Wave 0 → 09-08 | 09-08 |
| PARITY-01 (surface chunk) | Generated chunk has solid ground, water at y=63, valid heightmaps, valid biome containers | unit + round-trip vs `level.Chunk` encode | `go test ./world/ -run TestNoiseGenChunk` | ❌ Wave 0 → 09-08 | 09-08 |
| PARITY-01 (T2 optional) | A fixed-seed column heightmap matches a real vanilla 26.2 server | capture-diff (manual/golden) | `go test ./world/ -run TestSeedHeightmapVsVanilla` | ❌ optional | — |
| PARITY-01 (milestone) | A real vanilla 26.2 client renders recognizable terrain | **human-verify (BLOCKING, autonomous:false)** | manual real-client visual check | n/a | 09-08 |

## Sampling Rate

- **Per task commit:** `go build ./... && go test ./world/... ./world/levelgen/...`
- **Per wave merge:** the Docker `-race` `./world/...` suite (`-count=1`) — the noise stack is pure/deterministic so `-race` is cheap insurance.
- **Phase gate:** the BLOCKING real-client visual check (like 04-04 / 05-03) — *"does it look like Minecraft terrain (hills/plains/oceans + explorable caves + ravines)?"* — plus the determinism + chunk-encode tests green.

## Determinism / -race / Visual Gate

- **Determinism (REQUIREMENT — WORLD-04 / Pitfall 7):** ALL randomness flows from the world seed through the ported `Xoroshiro` positional factory; no `math/rand`, no time seed, no global mutable state, no map-iteration-order dependence in the eval path. `TestNoiseGenDeterministic` asserts same seed+pos → identical chunk bytes; `TestParseFullGraphEndToEnd` (09-03) asserts the entire noise_router binds deterministically.
- **`-race`:** run the Docker `golang:1.26 go test -race ./world/...` suite per wave merge — the noise stack is pure/deterministic so `-race` is cheap insurance over the off-tick worker boundary.
- **Visual gate (phase gate, BLOCKING, `autonomous:false`):** connect an unmodified vanilla 26.2 client, swap the generator, and confirm the world renders as recognizable Minecraft terrain (hills, plains, valleys, oceans/lakes with water at sea level, explorable caves + ravines, walkable solid ground, no void, no stripes). Same human-verify gate that sealed Phases 4 and 5. Directly proves the FULL-parity success criterion.

## Wave 0 Gaps

- [ ] `world/levelgen/random_test.go` — `Xoroshiro` golden vectors (Tier A — test FIRST) — 09-02
- [ ] `world/levelgen/synth/*_test.go` — noise primitive golden samples (Tier B) — 09-02
- [ ] `world/levelgen/density/density_test.go` — the FULL jar-walked node set + parser (incl. find_top_surface/invert + cave functions) — 09-03
- [ ] `world/levelgen/router_test.go` — RandomState seeding + `TestParseFullGraphEndToEnd` (all 15 router functions) + `TestParsePreliminarySurfaceLevel` — 09-03
- [ ] `world/levelgen/*_test.go` — NoiseChunk cell sampler + interpolator (09-04); Aquifer + OreVeinifier (09-05); SurfaceRules + Climate biome source (09-07)
- [ ] `world/levelgen/carver/carver_test.go` — the carver pass (cave/canyon, replaceables, aquifer-aware, cross-chunk, seeded) — 09-06
- [ ] `world/noisegen_test.go` — determinism (purity) + chunk-encode round-trip + sea-level water + heightmaps — 09-08
- [ ] The offline extract step in `tools/` + the `//go:embed` of the worldgen JSON (production code, but the test data seam lives here) — 09-01
- [ ] (Optional, T2) a captured vanilla-26.2 seed-heightmap fixture for the byte-diff
- [ ] Framework install: none (stdlib testing); no new runtime deps expected
