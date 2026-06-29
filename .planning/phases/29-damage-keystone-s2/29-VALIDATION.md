---
phase: 29
slug: damage-keystone-s2
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-29
---

# Phase 29 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (Go 1.26.1) |
| **Config file** | none — standard `*_test.go` |
| **Quick run command** | `CGO_ENABLED=0 go test ./server/ ./data/tag/ ./level/...` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...` |
| **-race (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...` |
| **Estimated runtime** | ~30s CGO=0 suite; ~470s Docker -race |

---

## Sampling Rate

- **After every task commit:** Run the quick command (scoped to touched packages).
- **After every plan wave:** Run the full CGO=0 suite.
- **Before `/gsd-verify-work`:** Full CGO=0 suite green + Docker -race green + pig oracle `TestPluginPigEqualsGoNativePig` green.
- **Max feedback latency:** ~30s (CGO=0); -race deferred to wave boundaries.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 29-01-* | 01 | 1 | MOB-SUB-03 | — | tag membership matches jar (no `const false`) | unit | `go test ./data/tag/` | ❌ W0 | ⬜ pending |
| 29-02-* | 02 | 2 | MOB-SUB-01, MOB-SUB-02 | T-29-01 | mob health drops, i-frame gated, `lastDamageSource` set | unit | `go test ./server/ -run Damage` | ❌ W0 | ⬜ pending |
| 29-03-* | 03 | 3 | MOB-SUB-01, MOB-SUB-02 | T-29-02 | player→mob + cross-region damage routes to owner region | unit + -race | `go test ./server/ -run 'Attack|Region'` | ❌ W0 | ⬜ pending |
| 29-04-* | 04 | 4 | MOB-SUB-01 | — | mob death removes entity, drops loot + XP | unit | `go test ./server/ -run 'Death|Loot'` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `data/tag/tags_test.go` — assert PANIC_CAUSES/BYPASSES_ARMOR membership matches jar javap output (golden subset).
- [ ] `server/combat_mob_test.go` — mob hurt pipeline (armor/absorption fold, i-frame gate, lastDamageSource).
- [ ] `server/damage_region_test.go` — cross-region damageIntent routing (N=2, -race).
- [ ] `server/mob_death_test.go` — death removal + loot roll + XP orb.
- [ ] Oracle backstop: existing `TestPluginPigEqualsGoNativePig` MUST stay green (no AI RNG touched).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| In-game: hit a pig, it loses health, flashes red, dies + drops | MOB-SUB-01 | Visual/client-observed | Live server, SULFUR_TEST_KIT, attack a spawned pig; observe health/death/drops/XP |

---

## Standing Gates (every wave)

- Pig oracle `TestPluginPigEqualsGoNativePig` GREEN — damage is event-driven, must not touch serverAiStep/navigation RNG.
- CGO_ENABLED=0 build + vet clean; `go list -deps ./... | grep -i gopy` EMPTY (no cgo leak).
- Docker `-race` + `strictRegion` clean on the cross-region damage path.
- Every ported op javap-verified against `temp/cache/26.2-inner.jar` BEFORE writing; class/method cited in the commit/comment.
