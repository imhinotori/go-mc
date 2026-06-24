# Sulfur — Session Handoff (2026-06-24)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. Branch `ender-776`, remote `git@github.com:imhinotori/sulfur` (pushed to `main`). Module `github.com/imhinotori/sulfur`. Dir on disk is still `D:\ender`.

## Where we are

**5 of 9 phases COMPLETE + FIRST PLAYABLE achieved.** A real unmodified vanilla 26.2 client (PrismLauncher) connects, logs in, and WALKS AROUND a solid, visible, ticking superflat world — chunk ring follows, tab list works, no kick.

| Phase | Status |
|-------|--------|
| 1 Foundation/Fork & Codegen | ✅ complete |
| 2 Net & Protocol State Machine | ✅ complete (NET-01..07) |
| 3 Authoritative Tick Loop | ✅ complete (TICK-01..06) |
| 4 World & Chunk System | ✅ complete (WORLD-01..05) |
| 5 Player Session / FIRST PLAYABLE | ✅ complete (PLAY-01..06) |
| **6 Entities, Physics & Interaction** | 🔄 **IN PROGRESS — 2/7 plans done (ENT-01 ✅)** |
| 7 AI, Pathfinding, Commands & Chat | ⬜ not started |
| 8 Leaf Async Optimizations | ⬜ not started |
| 9 Stretch (online/parity/regions) | ⬜ not started (v2) |

Last pushed commit: `a7740616` (Phase 6 Wave 2 / 06-02 complete).

## RESUME HERE — Phase 6, Wave 3

Phase 6 is planned as **7 sequential waves (one plan per wave, zero same-wave file overlap)**. Plans are committed and plan-checker-PASSED (0 blockers; the 2 routing-premise warnings in 06-04/06-05 are ALREADY FIXED in the plan files). Execute the remaining waves in order, each via a fresh `gsd-executor` agent:

- **06-01** ✅ done — entity store + monotonic `EntityIDAllocator` + per-section grid bucketing.
- **06-02** ✅ done — synchronous `entityTracker` behind `tracker.Tick()` + jar-derived AddEntity/SetEntityData/move/Remove encoders.
- **06-03** ⬅ **NEXT** — ENT-02 AABB physics (per-axis swept-AABB via `server/internal/bvh`, gravity, lands/blocked/no-tunnel; authoritative player anti-clip-through). Fills `tickPhysics()`; touches `server/subtick.go` (applyInput).
- **06-04** — ENT-03 block place/break. **MUST add `ServerboundPlayerAction` to the `server/tick.go` dispatch switch (tick.go:480-489) — it is NOT routed today (hits default no-op at tick.go:532).** New `world.ChunkManager.SetBlock(pos)` API + BlockChangedAck/BlockUpdate reconciliation. files_modified includes `server/subtick.go`.
- **06-05** — ENT-04 component-slot inventory (HIGHEST wire risk). **MUST add `ServerboundContainerClick`/`SetCreativeModeSlot`/`ContainerClose`/`SetCarriedItem` to the dispatch switch — NOT routed today.** Extend `level/component.SlotData.WriteTo`; HashedStack (1.21.5+ `HashedPatchMap`) decode — jar-derive in its Wave-0 task.
- **06-06** — ENT-05 health/damage/respawn (reuses Phase-5 sealed `commonPlayerSpawnInfoEncoder`; `ClientboundRespawn` = spawn-info + trailing Byte) + ENT-06 Anvil persistence (player `.dat` + entities region via `save/region`, off-tick over a value snapshot).
- **06-07** — **`autonomous: false`** — capture-diff the 3 MEDIUM surfaces (AddEntity LP-movement scaling, SetEntityData metadata, ContainerSetContent + ContainerClick HashedStack) against a real vanilla 26.2 server, THEN a BLOCKING human-verify real-client INTERACTIVE check (entity visible, place/break a block & SEE it, take damage & SEE the health bar, death→respawn). Mirrors Phase 4 04-04 / Phase 5 05-03.

### How to resume (the proven loop)
For each remaining wave: spawn a `gsd-executor` agent with the plan file + the critical-runtime-facts block (module path, stale-LSP caveat, the Wave-N seams it consumes, single-owner-tick discipline, `-race` in Docker `golang:1.26`). After it returns: verify the REAL `go build ./...` + `go test ./server/... ./world/...` (NOT the LSP — see below), then move to the next wave. After 06-06, run 06-07; it auto-runs the capture-diff then PAUSES for the user to do the real-client interactive test (start `go run ./cmd/sulfur` on `localhost:25565`, user connects, confirms, types "approved"). Then finalize Phase 6 (mark ENT-01..06 complete, close phase, push) and continue to Phase 7.

## CRITICAL GOTCHAS (learned this session — do not relearn)

1. **STALE LSP / gopls — IGNORE IT.** After every wave, gopls floods bogus diagnostics: `could not import github.com/imhinotori/go-mc/...`, `undefined: <symbol the executor just added>`, `undefined: packetid.ServerboundAttack`, `*server.gameTick does not implement server.GamePlay`. ALL FALSE — gopls hasn't re-indexed. The real compiler is the source of truth: `go build ./...` exits 0 and tests pass every time. NEVER "fix" these — do not revert imports to `go-mc`, do not re-declare symbols. Verify with `go build`/`go test`, not the diagnostics.
2. **`-race` needs Docker.** Host has no C compiler (`CGO_ENABLED=0`). Run race gates in `golang:1.26` Docker; under Git Bash use `MSYS_NO_PATHCONV=1` + an explicit `//d/ender://src` bind mount. Plain `go build`/`vet`/`test` run natively fine.
3. **Capture-diff / real-client is the ONLY correctness truth for 776 wire surfaces.** The wiki documents ≤773; 776 post-dates it. Self-round-trip tests PASS on broken wire (read/write are symmetric) — they caught NOTHING in Phases 2/4/5; the capture-diff against the real vanilla 26.2 server (`temp/cache/26.2-server.jar`, sha1 `823e2250…`, Java 25) + a real client caught EVERY real bug. Jar-derive wire layouts via `javap` on the inner `server-26.2.jar`. This is why 06-07 exists.
4. **Single-owner tick (TICK-05).** All entity/physics/block/inventory/combat state is tick-owned, mutated only on the tick goroutine. The tracker is SYNCHRONOUS behind `tracker.Tick()`. NO `xsync`/`ants`/`conc` — those are Phase 8 (the seams stay no-op: `applyAsyncResults`, the swappable tracker). Async-optimize nothing yet.
5. **EXTEND, don't rebuild.** ~80% of the data layer is generated/in-fork: `data/entity` (158 entities + AABB), `level/block` (32366 states, `ToStateID`), `level/component.SlotData` (extend, don't replace), `save`/`save/region` (Anvil), `server/internal/bvh` (AABB), `nbt`. New work = server LOGIC + a few encoders.
6. **Real bugs only a real client/capture-diff found this session (the pattern repeats):** `login_finished` missing trailing sessionId UUID; empty Update Tags → "Unbound tags" crash (needed the real 15-registry vanilla set); paletted-container header-vs-data bit-width mismatch (stripes/void); fluid-count short missing; `SetChunkCacheCenter` before Join Game → NPE; movement packets end in a PACKED FLAGS BYTE not a Boolean (1.21.3+); `ForgetLevelChunk` is a packed Long not VarInt z/x; a `net/queue.ChannelQueue` close-vs-send race. Expect 06-07 to surface 1-3 more in entity metadata / HashedStack.

## Project conventions / decisions

- **Project renamed Ender → Sulfur** (commit `630f90e3`): module is `imhinotori/go-mc` → `imhinotori/sulfur`, binary is `cmd/sulfur`, but MC entity names (Enderman/EnderDragon) and the frozen `.planning/.../baseline-774/` snapshot are intentionally untouched. The on-disk directory is still `D:\ender` (not renamed; harmless).
- **No plugin system** (scope boundary). Offline-mode only for v1 (online auth/encryption = Phase 9 v2). No JVM at runtime (Java is build-time codegen only).
- **GSD config**: mode YOLO, all quality gates on (research + plan-check + verifier + nyquist). Each phase: research → plan → plan-check → execute waves → (capture-diff + human-verify where wire-correctness needs it) → finalize. Commits are `docs(NN)`/`feat(NN-MM)`/`fix(NN-MM)`/`test(NN-MM)`. NEVER add Co-Authored-By / self-attribution (user is sole author — absolute).
- **Caveman mode (full)** active — terse Spanish responses to the user; code/commits/PRs written normally. User is Spanish-speaking (matiascanovasg@gmail.com / GitHub imhinotori).
- **Docker** must be running for codegen (Phase 1, done) and `-race` gates. It was started this session; re-check `docker version` on resume.

## Key paths
- Plans/state: `.planning/` (ROADMAP.md, STATE.md, REQUIREMENTS.md, PROJECT.md; per-phase under `.planning/phases/NN-*/`).
- Phase 6 plans: `.planning/phases/06-entities-physics-interaction/06-0{1..7}-PLAN.md` (+ 06-VALIDATION.md, 06-RESEARCH.md, 06-CAPTURE-DIFF.md started by 06-02).
- Runnable server: `go run ./cmd/sulfur` → listens `:25565`, proto 776.
- Vanilla jar for capture-diff: `temp/cache/26.2-server.jar` (gitignored; `temp/` never read at runtime).
