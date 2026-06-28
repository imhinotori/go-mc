# Project Retrospective

*A living document updated after each milestone. Lessons feed forward into future planning.*

## Milestone: v3 — Online-mode + Operator UX + Structure polish

**Shipped:** 2026-06-27
**Phases:** 4 (17–20) | **Plans:** 33 SUMMARYs (17×22, 18×2, 19×3, 20×6) | **Requirements:** 15/15

### What Was Built
- **Gameplay completion (Phase 17):** the six unwired client-facing seams — player-entity-in-tracker broadcast (the keystone), persisted-position apply, inventory join-sync, Attack/Interact damage + fall damage, the large net-new FlowingFluid water simulation + player fluid physics, and block-break Item-entity drops. The v1 "fully playable" claim had isolated-function tests but never an end-to-end real-client wiring; Phase 17 closed every seam.
- **Online-mode (Phase 18):** RSA/PKCS#1v1.5 EncryptionRequest/Response (incl. the 776 trailing `shouldAuthenticate` boolean), hand-rolled AES-128/CFB8 over stdlib AES (no new dep, CGO=0 preserved), Yggdrasil `hasJoined`, authenticated skin properties propagated to PlayerInfoUpdate(ADD_PLAYER), behind an `online-mode` flag (offline byte-identical).
- **Operator UX (Phase 19):** a bubbletea+bubbles TUI console (textinput + live log viewport) that degrades to plain logging off-TTY, console commands routed through the existing graph on the tick owner, full disconnect-reason taxonomy in both the TUI stream and structured logs.
- **Structure polish (Phase 20):** a shared `level/loot` evaluator (one engine for block drops AND chest loot, the v1 hardcoded map deleted), structure inhabitants (witch/cat/villager spawn + silverfish spawner) crossing worldgen→tick via `ChunkResult.Spawns`, the Beardifier terrain-adaptation (village raise / stronghold bury, others byte-identical), structure-start NBT persistence (survive reload without recompute), and the full chest-OPEN UI runtime path.
- **Live 12-bug fidelity sweep (post-phase, this session):** chest placement (DefaultStateID), sugar-cane cascade (block-tick container), natural water flow (carver post-process marks), hats/skins (CONFIG modelCustomisation + sendSelfSkin), persist respawn crash (Anvil palette rebuild), chest drag + double-click (QUICK_CRAFT + PICKUP_ALL), swim-in-reloaded-chunks (FluidCount), static reloaded water (PostProcessing persist), oxygen-while-submerged + oxygen-while-swimming (fluid surface height + swim-pose eye height). Each reproduced offline/live → jar-verified → 1:1 ported → regression-tested → operator-confirmed in-game.

### What Worked
- **Instrument-first, never reason-offline.** Every one of the 12 live bugs was reproduced with an offline probe test or a live `SULFUR_ULTRA_DEBUG` capture BEFORE editing. The two times an offline assumption was made (hat-visible-with-1-player, water-deferral) it was WRONG — the operator caught it. The discipline paid for itself.
- **Jar bytecode as the single source of truth.** `javap -c -p` before writing kept ports literal; it caught real deltas (the 776 `shouldAuthenticate` boolean, the 4-byte challenge, FlowingFluid.getHeight, swim eye height 0.4).
- **One seam, many producers.** The Phase 17 entity/tracker keystone meant Phase 18 skins and Phase 20 structure spawns rode the SAME path for free — the integration audit found 4/4 seams wired with one shared encoder, one shared loot evaluator, one entities.add.
- **The "no built-but-unwired code" rule** (from the SC4 gap lesson) — recording subsystem-blocked work in a backlog instead of shipping speculative dead code — kept the tree honest.

### What Was Inefficient
- **v1 closed on isolated tests, not E2E.** The entire Phase 17 existed because "core logic + unit test" was mistaken for "done." The real client surfaced six defects on day one. Lesson already absorbed (visual gates now autonomous:false), but it cost a whole phase.
- **Phase 20 shipped a documented split (chest seam built, no runtime caller)** that then needed a follow-up this session to wire. Honest, but the split could have been one phase if the open-UI was scoped in from the start.
- **gopls staleness** flooded false "undefined" diagnostics all session — every check had to gate on `go build`/vet/test instead. Minor friction tax, but constant.

### Patterns Established
- **`run-debug.sh` — debug mode always on.** Single command builds CGO=0, kills the prior instance, starts online+test-kit+persist+ULTRA_DEBUG firehose. Operator-requested standing config.
- **`udebug("cat", ...)` one-shot probes** added at the seam, captured live, removed before commit (keep the fix, drop the trace).
- **Codegen-emitted defaults** (`block.DefaultStateID` from the jar's `Default` flag) instead of Go zero-value structs — the chest-facing bug root cause; the general fix covers every block with non-zero default props.
- **Anvil vs NETWORK palette distinction** made explicit in `level/palette.go` — direct/global (raw ids) on the wire vs always-indexed on disk; the reload-crash root cause.

### Key Lessons
1. **Reproduce before you edit — always.** Offline reasoning was wrong twice; live instrumentation was right every time. This is now a standing order.
2. **"Unit-tested" ≠ "wired."** Test the seam end-to-end with the real consumer (real client), or it isn't done. Visual gates must be operator-confirmed, not assumed.
3. **Verify the port against bytecode every time**, even for "obvious" logic — the 776 protocol shifts and float-exact constants (eye height 0.4, getHeight amount/9) don't survive paraphrase.
4. **Recompute as the coherence authority, persistence as the optimization** (SC4): persist computed state but always fall back to recompute on a missing/garbled tag — never panic, never trust the cache blindly.

### Cost Observations
- Model mix: predominantly sonnet for execution; opus for the cross-phase integration audit.
- Sessions: multiple, spanning the phase builds + the live-bug sweep + this close.
- Notable: parallel worktree builds (ATTRIB+FACESTURDY, then ITEMNBT+PERSIST, then BLOCKTICK) merged to `ender-776` with zero conflicts (disjoint files) — the v3.1 subsystem prerequisites landed cheaply.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Phases | Key Change |
|-----------|--------|------------|
| v1.0 MVP | 1–9 | Codegen-from-jar foundation; closed on isolated-function tests (the gap that created v3 Phase 17). |
| v2.0 Worldgen + Structures | 10–16 | Per-seed determinism + visual gates introduced; structures shipped with documented deferrals. |
| v3 Online + Operator UX + Structure polish | 17–20 | E2E real-client wiring became the bar; instrument-first live debugging became the standard; "no built-but-unwired code" rule formalized. |

### Cumulative Quality

| Milestone | Suite | -race | Zero-Dep Discipline |
|-----------|-------|-------|---------------------|
| v3 | level/world/server green | Docker -race clean on async seams | CFB8 hand-rolled (no crypto dep); CGO=0 preserved throughout |

### Top Lessons (Verified Across Milestones)
1. **Codegen from the jar beats hand-transcription** — verified v1→v3 (packets, blocks, loot, attributes, support shapes all generated/ported, not typed).
2. **"Tested" must mean "wired E2E with the real consumer"** — v1's isolated-test close directly caused v3 Phase 17; the lesson is now load-bearing.
3. **Reproduce-before-edit + jar-bytecode-before-write** — the two disciplines that made the 12-bug sweep correct on the first try every time.
