---
phase: 20
phase-slug: structure-polish-loot-inhabitants-beard-persistence
created: 2026-06-26
nyquist: enabled
---

# Phase 20 — Validation Strategy (Nyquist Dimension 8)

> Structure polish — loot evaluator, structure inhabitants, terrain beard, StructureStart
> persistence. The 1:1 vanilla-jar mandate BINDS this phase: every loot/spawn/beard algorithm
> is a literal port of `temp/cache/26.2-inner.jar`. The binding automated gates are
> CGO_ENABLED=0 (pure-Go static), race-cleanliness on the off-tick->tick seams, byte-identity
> for non-adapting terrain, and BYTECODE-derived loot goldens (a real vanilla client is not
> runnable here, so per-seed parity against a live client is the autonomous:false human gate).

## Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` |
| Config | none (standard `go test`) |
| Quick run | `go test ./level/loot/... ./world/structure/...` |
| Full / race | Docker `golang:1.26`: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./server/... ./world/... ./level/... ./save/...` (host CGO=0, no C compiler — race runs in Docker per STATE.md) |
| Lint gate | `CGO_ENABLED=0 go build ./...` + `go vet ./...` (NEVER gopls — stale, emits false diagnostics in this repo) |

## Phase Requirements → Test Map
| Req | Behavior | Type | Automated command | Plan |
|-----|----------|------|-------------------|------|
| STRUCT-POLISH-01 | A known table+fixed seed rolls the EXACT item list a HAND-TRACE OF THE JAR BYTECODE predicts (golden, NOT a Go self-snapshot; cites jar method+offsets) | unit (golden) | `go test ./level/loot/ -run TestLootSeedReproduces` | 20-01 |
| STRUCT-POLISH-01 | `set_count`/`uniform`/weighted-select draw order matches vanilla (rolls.getInt -> nextInt(total) -> per-entry fns in JSON order) | unit | `go test ./level/loot/ -run TestPoolDrawOrder` | 20-01 |
| STRUCT-POLISH-01 | Block drop uses the shared evaluator (stone->cobblestone via loot.Roll over minecraft:blocks/stone, not the v1 map) | unit | `go test ./server/ -run TestBlockDropViaLoot` | 20-02 |
| STRUCT-POLISH-01 | A generated chest rolls LAZILY on first open via loot.Roll(table, LootTableSeed); re-open does not re-roll | unit | `go test ./server/ -run TestChestLazyRoll` | 20-02 |
| STRUCT-POLISH-02 | Swamp hut records witch+cat spawn requests (one-shot); stronghold places a silverfish SPAWNER block (not a live entity) | unit | `go test ./world/structure/ -run 'TestSwampHutSpawns\|TestStrongholdSpawner'` | 20-04 |
| STRUCT-POLISH-02 | Spawn requests reach the tick store via ChunkResult.Spawns; no off-tick store mutation (race); reload guard holds | unit + race | `go test -race ./server/ -run TestStructureSpawnSeam` | 20-04 |
| STRUCT-POLISH-03 | getBuryContribution/getBeardContribution match the jar at sample offsets; only village+stronghold gather | unit | `go test ./world/structure/ -run 'TestBeardContribution\|TestBeardScope'` | 20-05 |
| STRUCT-POLISH-03 | Village beard raises terrain; stronghold buries; non-adapting (NONE) structures are BYTE-IDENTICAL to Phase 16 | unit (regression) | `go test ./world/... -run 'TestVillageBeardRaises\|TestStrongholdBuries\|TestNonAdaptingUnchanged'` | 20-05 |
| STRUCT-POLISH-04 | StructureStart round-trips NBT (createTag<->loadStaticStart); a loaded start EQUALS ComputeStarts (recompute-coherence) | unit | `go test ./world/structure/ -run TestStartNBTRoundTrip` | 20-03 |
| STRUCT-POLISH-04 | Per-piece Save()/Load() NBT round-trips every Sulfur piece type (incl. the spawn-guard slots) | unit | `go test ./world/structure/ -run TestPieceNBTRoundTrip` | 20-03 |
| STRUCT-POLISH-04 | Cache writes starts to region structures.Starts + reloads from NBT; a missing/garbled tag falls back to recompute (no panic) | unit | `go test ./world/structure/ -run TestStructurePersist` | 20-03 |

### Mandatory test list (Nyquist gate — these MUST exist + pass)
- `TestLootSeedReproduces` (golden, bytecode-hand-traced) — 20-01
- `TestBlockDropViaLoot` — 20-02
- `TestChestLazyRoll` — 20-02
- `TestStructureSpawnSeam` (`-race`) — 20-04
- `TestStartNBTRoundTrip` — 20-03
- `TestNonAdaptingUnchanged` (byte-identity regression guard) — 20-05

## Sampling Rate
- **Per task commit:** `go test ./level/loot/... ./world/structure/...` (fast; + the touched server test where applicable).
- **Per wave merge:** Docker `-race` over the touched trees (`./level/... ./world/... ./server/... ./save/...`).
- **Phase gate:** full Docker `-race` suite green + the worldgen capture-diff / determinism goldens
  addressed. This phase adds chest BlockEntities + spawned entities (touching the chunk-wire
  BlockEntity/entity list) AND changes village+stronghold terrain bytes — so the capture-diff/
  determinism goldens MAY need a deliberate re-seal (see the re-seal note below). Plus the
  autonomous:false real-client visual gate at phase close (open a structure chest with vanilla
  loot at a known seed, see villagers/witch/cat, structures fit terrain) — the per-seed
  live-client parity check the bytecode golden cannot stand in for.

### Capture-diff / determinism re-seal note (W4)
- 20-02 adds chest BlockEntities to chunks → re-run the capture-diff goldens; re-seal if the
  BlockEntity-list framing changed (document in 20-02 SUMMARY), else confirm green.
- 20-04 adds spawned entities + the silverfish spawner BE → same: re-run, re-seal-or-confirm.
- 20-05 CHANGES village+stronghold terrain bytes by design. If the Phase-13/16 determinism/
  capture-diff goldens cover a village or stronghold chunk at the pinned seed, the byte delta is
  EXPECTED — perform a DELIBERATE re-seal (capture the new bytes, document the village/stronghold
  delta as expected in the 20-05 SUMMARY), NOT a silent byte-identity pass. The
  `TestNonAdaptingUnchanged` guard proves NONE-adaptation chunks stayed byte-identical; the
  adapting (village/stronghold) goldens are the ones that legitimately re-seal.

## Wave 0 Gaps (tests created in-task via tdd, not pre-staged)
- [ ] `level/loot/loot_test.go` — the seed-reproduces golden, derived by HAND-TRACING THE JAR
      BYTECODE (the LCG draws + pool selection written out from the decompiled LootTable/LootPool,
      cited by jar method+offset), NOT by snapshotting the Go impl. A real vanilla client is not
      runnable here, so the bytecode trace is the honest automated proof of per-seed reproduction;
      the live-client capture is the autonomous:false human gate. (STRUCT-POLISH-01, 20-01)
- [ ] `level/loot/testdata/golden_loot.json` — the bytecode-derived expected item list + the
      `bytecode_trace` citation (STRUCT-POLISH-01, 20-01)
- [ ] `server/chest_loot_test.go` — lazy roll on first open + no re-roll on re-open (STRUCT-POLISH-01, 20-02)
- [ ] `world/structure/start_nbt_test.go` — createTag<->loadStaticStart round-trip + recompute
      coherence assertion (STRUCT-POLISH-04, 20-03)
- [ ] `world/structure/piece_nbt` tests (in 20-03's test files) — per-piece Save/Load incl. the
      spawn-guard slots 20-04 fills (STRUCT-POLISH-04, 20-03)
- [ ] `world/structure/spawn` / `server/structure_spawn_test.go` — per-piece spawn-request
      assertions + the off-tick->tick `-race` seam (STRUCT-POLISH-02, 20-04)
- [ ] `world/structure/beard_test.go` — getBury/getBeard contribution fixtures (village/stronghold
      density delta) + the NONE byte-identity regression guard (STRUCT-POLISH-03, 20-05)

## Out of scope for validation
- A real running vanilla 26.2 server/client for automated per-seed loot parity — not runnable in
  this harness. Covered by the bytecode hand-trace (automated) + the phase-close autonomous:false
  visual gate (human).
- Trade-rebalance datapack loot variants, non-target structure loot (ancient city/bastion/etc.) —
  out of scope per RESEARCH.md Deferred Ideas.
