---
phase: 27
slug: folia-regionization
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-28
---

# Phase 27 — Validation Strategy

> The most invasive v4 change. The load-bearing checks: behavior-neutral at each step + -race clean + the existing suite passes unchanged.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Quick run command** | `CGO_ENABLED=0 go test ./server/ ./plugin/...` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./server/ ./plugin/... && CGO_ENABLED=0 go test ./server/ ./plugin/... ./world/...` |
| **-race command (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...` |
| **Estimated runtime** | ~30–90s (server + world); -race is the load-bearing gate here |

---

## Sampling Rate

- **After every task commit:** the quick suite + (for the region-tick steps) the Docker -race
- **After every STEP (extract / barrier / N=2):** the FULL existing suite UNCHANGED + Docker -race — regionization must be behavior-neutral at each step
- **Before `/gsd-verify-work`:** full suite green + Docker -race green + the parallel-regions + region-aware-hook gates
- **Max feedback latency:** ~90s quick / ~3-5 min race (the race gate is the one that matters most)

---

## Per-Task Verification Map

> Provisional. The REGION-01 load-bearing samples — the 3 incremental steps + the gates.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 27-01-01 | 01 | 1 | REGION-01 | — | STEP 1: the `region` struct extracted at N=1 (entityStore/ChunkManager/scheduled ticks/levelRandom move to region; the single region == today's TickLoop); BEHAVIOR-NEUTRAL — the FULL existing server suite passes UNCHANGED | unit | `CGO_ENABLED=0 go test ./server/ ./world/...` (the existing suite, unchanged) | ❌ W0 | ⬜ pending |
| 27-01-02 | 01 | 1 | REGION-01 | T-27-01 | the per-region/global split: per-region (store/chunks/ticks/rand) vs GLOBAL (idAlloc/player-network/frozen registry/gametime/on_tick/console) is explicit; step-1 -race clean | race | Docker `-race ./server/` (the extract is race-clean) | ❌ W0 | ⬜ pending |
| 27-02-01 | 02 | 2 | REGION-01 | — | STEP 2: the coordinator fan-out/barrier at N=1 (conc scoped WaitGroup + panic propagation); still behavior-neutral; gametime advanced ONCE per tick by the coordinator (the shared 50ms anchor) | unit | `CGO_ENABLED=0 go test ./server/ -run TestCoordinatorBarrier` | ❌ W0 | ⬜ pending |
| 27-02-02 | 02 | 2 | REGION-01 | T-27-02 | conc added (pure-Go, CGO=0 confirmed); a region panic propagates structured (one region's panic does NOT hang the tick — recover per region); the global-region thread ticks the server-wide state | unit + build | `CGO_ENABLED=0 go build ./...` + `CGO_ENABLED=0 go test ./server/ -run TestRegionPanicIsolated` | ❌ W0 | ⬜ pending |
| 27-03-01 | 03 | 3 | REGION-01 | — | STEP 3: N=2 — a static chunk→region hash; 2 regions tick CONCURRENTLY (assert both region goroutines are in their tick phase simultaneously) | unit | `CGO_ENABLED=0 go test ./server/ -run TestTwoRegionsTickInParallel` | ❌ W0 | ⬜ pending |
| 27-03-02 | 03 | 3 | REGION-01 | T-27-03 | cross-region entity TRANSFER at the barrier: an entity crossing the boundary moves A→B at the sync point, keeps ticking on B's thread, no double-tick / no drop; the async rejoin (pathReady/applyTo) routes to the OWNING region | unit | `CGO_ENABLED=0 go test ./server/ -run TestCrossRegionTransfer` | ❌ W0 | ⬜ pending |
| 27-03-03 | 03 | 3 | REGION-01 | — | THE GATE — region-aware plugin seam: an entity in region R → its declared-mob goal callback runs on R's goroutine (NOT the global, NOT region 0); the global on_tick runs on the global thread; the frozen registry is shared safely | unit | `CGO_ENABLED=0 go test ./server/ -run TestPluginHookRunsOnOwningRegion` | ❌ W0 | ⬜ pending |
| 27-03-04 | 03 | 3 | REGION-01 | T-27-01 | RACE: 2 regions ticking in parallel + cross-region transfer + the tracker (cross-region read at the barrier) + region-aware Emit — ALL -race clean | race | Docker `-race ./server/ ./plugin/...` `-run TestRegionizedTickRace` | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `server/region.go` — the extracted region struct + the per-region/global boundary
- [ ] `server/region_coordinator.go` — the conc fan-out/barrier + the gametime-advance-once
- [ ] `server/region_transfer.go` — the chunk→region hash + the cross-region transfer
- [ ] the region-aware Emit routing (extend plugin dispatch / the handle resolution)
- [ ] `server/region_test.go` + `server/region_race_test.go` — the 3-step + the gates + the -race
- [ ] `go get github.com/sourcegraph/conc` (confirm pure-Go, CGO=0) + go mod tidy
- [ ] the existing server + world suites RE-RUN UNCHANGED after each step (the behavior-neutral safety net)

*The #1 invariant: at EACH step (extract / barrier / N=2) the existing suite passes UNCHANGED + -race is clean. Regionization is a concurrency change over the faithful logic — zero behavior drift.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | — |

*All Phase-27 behaviors are automatable (parallel-region ticking + transfer + region-aware hooks are testable headless under -race). The real-client + perf-scaling gate is Phase 28.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Each step (extract/barrier/N=2) is behavior-neutral (existing suite unchanged)
- [ ] The -race gate is green at each region-tick step (the load-bearing check)
- [ ] conc is pure-Go (CGO=0 preserved)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
