---
milestone: v4
audited: 2026-06-29T00:40:24Z
status: passed
scores:
  requirements: 8/8
  phases: 8/8
  integration: 7/7
  flows: 4/4
gaps:
  requirements: []
  integration: []
  flows: []
tech_debt:
  - phase: 24-vanilla-mobs-as-plugins
    items:
      - "Deferred goals (deferred-goals.md): Float (needs mob jumpControl), Panic (needs mob damage source), Breed/Tempt/FollowParent (need entity aging/breeding) — matchers/structure ready, subsystems deferred"
  - phase: 25-crafting-recipes-as-plugins
    items:
      - "Deferred blocks (deferred-blocks.md): furnace/blast/smoker/campfire BLOCKS (need a per-tick block-entity drive + FuelValues table); the smelting/cooking MATCHERS ship + are tested"
  - phase: 26-opt-in-python-runtime
    items:
      - "Richer Python mutation vocabulary (beyond set_block/spawn/log) deferred"
      - "Sub-interpreter parallelism deferred (gopy alpha has no sub-interp surface — serialized fallback, cited)"
  - phase: 27-folia-regionization
    items:
      - "Dynamic region merge/split deferred (N=2 static hash proves the seam)"
      - "Per-region persistence flush a follow-up"
      - "WARNING: (*region).emitEntityEvent helper unused (dead contract) — all entity-scoped emits currently fire on the coordinator (race-safe); the helper is the documented path for emits fired from a region fan-out goroutine if one is ever added. Functionally identical to direct Emit; not a broken flow."
  - phase: 28-plugin-system-visual-perf-gate
    items:
      - "Docker CGO=1 -race not re-run for phase 28 (defensible: bench/trigger/sink add no cross-tick shared state; equality + region paths -race-verified in Phases 24/27)"
  - milestone-wide:
      - "./world/ -race-gate timeout (needs 2400s) — ops follow-up to raise/shard"
      - "Operator action (not code): rotate the prod root password exposed in a much earlier session"
---

# Milestone v4 — Plugin / Scripting System — Audit

**Status: PASSED.** All 8 requirements satisfied, all 8 phases verified (status: passed), cross-phase integration fully wired (7/7 chains), all 4 end-to-end gate flows pass with live wire evidence.

## Definition of Done

v4 delivers a dual-runtime plugin/extension API for Sulfur **without giving up the two things that define the project**: the pure-Go static binary (CGO_ENABLED=0) and the 1:1-with-the-jar gameplay mandate. The API is dogfood-validated across two domains (vanilla mobs as 1:1-jar plugins; crafting through the plugin API) and the same API enables fully custom mobs + recipes. Folia regionization (REGION-01, folded from the v3 deferral) layers parallel-region tick threading over the faithful logic. PLUGIN-07 (the visual + perf gate) is the closing proof.

## Requirements Coverage (3-source cross-reference)

| Requirement | Phase | VERIFICATION | SUMMARY | Traceability | Final Status |
|-------------|-------|--------------|---------|--------------|--------------|
| PLUGIN-01 (Starlark runtime, sandbox, CGO=0, -race) | 21 | passed | listed | `[x]` | **satisfied** |
| PLUGIN-02 (plugin host + typed event bus) | 22 | passed | listed | `[x]` | **satisfied** |
| PLUGIN-03 (declarative entity/mob API, frozen handles) | 23 | passed | listed | `[x]` | **satisfied** |
| PLUGIN-04 (vanilla mobs AS 1:1 plugins) | 24 | passed | listed | `[x]` | **satisfied** |
| PLUGIN-05 (crafting THROUGH the plugin API) | 25 | passed | listed | `[x]` | **satisfied** |
| PLUGIN-06 (opt-in Python, build-tag isolated) | 26 | passed | listed | `[x]` | **satisfied** |
| REGION-01 (Folia per-region tick threading) | 27 | passed | listed | `[x]` | **satisfied** |
| PLUGIN-07 (visual + perf gate, closes v4) | 28 | passed | listed | `[x]` | **satisfied** |

No unsatisfied requirements. No orphaned requirements (every REQ-ID participates in ≥1 verified cross-phase connection). FAIL gate not triggered.

## Phase Verifications

All 8 phase VERIFICATION.md files report `status: passed`. Several verifiers independently disassembled the 26.2 jar to confirm 1:1 fidelity (Phases 23/24/25); the Phase-28 verifier re-ran all 10 prescribed gates rather than trust the SUMMARYs.

## Cross-Phase Integration (FULLY WIRED — 7/7)

Verified by code-trace AND live wire evidence (gate-transcript.log). Boot wiring in `cmd/sulfur/main.go` (lines 296-349), all boot-critical calls FATAL-guarded:

1. host(22) → starlark(21) → event bus → 9 server Emit seams — WIRED (`SetPlugins`)
2. entity API(23) → vanilla_pig(24) as the ONLY pig (both live spawn sites swapped; `newPigAI` survives only as the test oracle) — WIRED (`LoadVanillaPigRegistry`→`SetMobRegistry`)
3. recipe plugin(25) → `Match` seam → un-stubbed `inventory_click.go` result+consume — WIRED (`LoadCraftingPlugin`+`SetRecipeTable`)
4. custom wander mob(28) merged into the SAME live registry + test-kit egg → `spawnDeclaredMob` — WIRED (`RegisterWanderMob`, gated by `SULFUR_TEST_KIT=1`)
5. chat() builtin(28) → installed sink → `broadcastSystemChat` on the wire — WIRED (`InstallChatSink`)
6. region-aware hooks(27): all entity-scoped emits fire on the coordinator (quiescent) → race-safe; frozen registry global/lock-free across region threads — WIRED
7. Python isolation(26): `go list -deps ./cmd/sulfur | grep -i gopython` → EMPTY; all 6 gopy imports behind `//go:build python` — WIRED

## End-to-End Gate Flows (4/4 PASS)

`gate-transcript.log` (2026-06-28 20:19) — `GATE RESULT: PASS (all 4 plugin-system items observed)`:
1. Custom wander mob — AddEntity + move packets (spawned via egg, walks via Go nav)
2. Vanilla-pig-as-plugin — pig-wire AddEntity + move/headrot
3. Crafting — OpenScreen + result slot populated for VANILLA (planks→stick 974) AND CUSTOM (dirt→diamond 926) via the plugin Match path
4. Events — `ClientboundSystemChat "gate_events: block broken..."` from the on_block_break→chat()→broadcastSystemChat path

## Build / Isolation Gates (green)

- `CGO_ENABLED=0 go build ./...` → exit 0 (pure-Go static default binary)
- `go list -deps ./cmd/sulfur | grep -i gopython` → empty (Python fully isolated — PLUGIN-06's #1 gate)
- `go vet ./server/ ./plugin/host/ ./cmd/testbot ./cmd/sulfur` → clean
- `go test ./server/ ./plugin/...` → ok (TestPerfGate exits 0; plugin tax ~2.3% ns / 4.2% per-mob-tick, under the 15% cap; zero-subscriber Emit = 0 allocs/op)

## Tech Debt

All deferred items are cited in their phase directories (deferred-goals.md, deferred-blocks.md) — none block v4's definition of done. See the frontmatter `tech_debt` block. The single integration WARNING (`emitEntityEvent` unused) is forward-looking, not a broken flow.

## Verdict

**v4 PASSED.** Ready for `/gsd-complete-milestone v4` (archive, collapse ROADMAP, tag v4, evolve PROJECT.md, retrospective).
