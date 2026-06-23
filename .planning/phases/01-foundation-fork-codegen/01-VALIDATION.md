---
phase: 1
slug: foundation-fork-codegen
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-23
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

This phase is a build/codegen foundation (fork go-mc, retarget the codegen pipeline to proto 776, generate + commit Go data). Validation is dominated by **build success** and **generated-output sanity**, not unit tests of business logic.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (runtime module) + `go build ./...` as the primary gate |
| **Config file** | none — `go.mod` defines the module; `tools/go.mod` is a separate module |
| **Quick run command** | `go build ./...` |
| **Full suite command** | `go vet ./... && go build ./... && go test ./...` |
| **Estimated runtime** | ~30–90 seconds (codegen run excluded — it is a one-time generation step, minutes) |

---

## Sampling Rate

- **After every task commit:** Run `go build ./...`
- **After the codegen task:** Run `go build ./...` over the generated trees + a count assertion (packets/registries/blocks present)
- **After every plan wave:** Run `go vet ./... && go build ./... && go test ./...`
- **Before `/gsd-verify-work`:** Full suite green AND a vanilla-26.2-jar codegen run completes without manual transcription
- **Max feedback latency:** 90 seconds (excluding the one-time codegen extraction)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 1-01-xx | 01 | 1 | GEN-01 | — | Server module builds with Go alone; no `java`/JVM import in runtime `go.mod` | build | `go build ./... && ! grep -ri 'jvm\|java' go.mod` | ❌ W0 | ⬜ pending |
| 1-01-xx | 01 | 2 | GEN-02 | T-1-01 (jar integrity) | Jar fetched by pinned sha1 from official manifest; checksum verified before extraction | integration | `cd tools && go run . --version 26.2` exits 0 | ❌ W0 | ⬜ pending |
| 1-01-xx | 01 | 3 | GEN-03 | — | Generated 776 data exists as committed Go source | build | `go build ./...` over generated pkgs; non-empty packet/registry/block files | ❌ W0 | ⬜ pending |
| 1-01-xx | 01 | 3 | GEN-04 | — | 774→776 diff enumerated; every change traces to jar output | manual+diff | `git diff` of generated trees vs 774 baseline reviewed | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `go.mod` (runtime module) + `tools/go.mod` (codegen module) exist and are separate
- [ ] A pinned go-mc master commit recorded; #294-296 cherry-pick state captured
- [ ] A smoke `go build ./...` passes on the bare fork before codegen
- [ ] A 774 baseline of generated trees captured (for the GEN-04 diff) before regenerating at 776

*If a 774 baseline cannot be generated locally (no 1.21.11 jar), capture the #296 PR's committed generated files as the baseline instead — note this in the plan.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| 774→776 diff is correct and jar-traceable | GEN-04 | Semantic review — "is this byte-layout change real?" can't be auto-asserted | Run codegen at 26.2, `git diff` generated trees vs 774 baseline, confirm each delta (new registries/components, packet-ID shifts, block-state count, 26.1+ fluid-count short) corresponds to jar output, not edits |
| Extractor compiles against the 26.2 jar | GEN-02 | Depends on whether 26.2 renamed `net.minecraft.*` classes the extractor imports; only the real jar confirms | Run the container extractor; a `javac` error names the renamed class — fix the import, rerun |

---

## Validation Sign-Off

- [ ] All tasks have an `<automated>` build/diff verify or a Wave 0 dependency
- [ ] Sampling continuity: no 3 consecutive tasks without an automated build check
- [ ] Wave 0 captures the 774 baseline before 776 regeneration
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s (codegen extraction excluded)
- [ ] `nyquist_compliant: true` set in frontmatter once plans satisfy the map

**Approval:** pending
