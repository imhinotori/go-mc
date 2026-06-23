---
phase: 01-foundation-fork-codegen
plan: 01
subsystem: infra
tags: [go-mc, fork, codegen, protocol-776, two-module, jdk25, baseline-774]

# Dependency graph
requires:
  - phase: none (first execution plan)
    provides: pinned upstream go-mc master + draft PRs #294/#295/#296
provides:
  - Forked go-mc on branch ender-776, branched off pinned master 539b4a3a, with #294-296 merged in order
  - Runtime module github.com/imhinotori/go-mc that builds with Go alone (GEN-01 structural guarantee)
  - Separate tools/ codegen module (tools/go.mod) with replace => .. isolating Java/codegen deps
  - 774 generated trees captured as baseline-774/ snapshot for the GEN-04 776 diff
  - Extractor container retargeted to eclipse-temurin:25-jdk (26.2 codegen prerequisite)
affects: [01-02 (26.2 codegen run), 01-03 (776 generated-data diff/GEN-04), all downstream server work]

# Tech tracking
tech-stack:
  added:
    - "Fork of Tnze/go-mc @539b4a3a + PRs #294/#295/#296 (protocol-774 base, codegen pipeline)"
    - "tools/ codegen module (separate go.mod, Java-shelling extractor)"
  patterns:
    - "Two-module separation: runtime go.mod (Go-only) vs tools/go.mod (codegen, replace => ..)"
    - "Pinned-commit + specific-PR-head merge (no moving @master) for supply-chain reproducibility"
    - "Generated-tree baseline snapshot under .planning/ for regeneration diffs"

key-files:
  created:
    - ".planning/phases/01-foundation-fork-codegen/baseline-774/ (380 files: 774 generated trees)"
    - "go.mod, tools/go.mod (introduced by the fork base + #294 merge)"
    - "tools/extract.go (introduced by #294, retargeted to JDK 25)"
  modified:
    - "go.mod (module path Tnze/go-mc -> imhinotori/go-mc)"
    - "tools/go.mod (module + require + replace rewritten to imhinotori path)"
    - "339 *.go files + templates (internal import path rewrite)"
    - "tools/extract.go (jdkImage 21-jdk -> 25-jdk)"

key-decisions:
  - "Brought the fork INTO the repo root (merge --allow-unrelated-histories) so runtime module lives alongside .planning/, rather than a sibling clone"
  - "Rewrote module path across all 342 code/mod/tmpl files (not just go.mod) — required for the runtime build to resolve internal imports under the fork path"
  - "Used eclipse-temurin:25-jdk tag form (not digest-pinned): Docker daemon was unavailable at build time; digest pin deferred to Plan 02's codegen run"
  - "Made only the mandatory jdkImage edit in tools/extract.go per plan; flagged --source 21 launcher flag as a Plan-02 validation item rather than editing it here"

patterns-established:
  - "Two-module isolation guarantees GEN-01 (Go-only runtime, zero Java in runtime go.mod)"
  - "PR-head SHAs recorded in SUMMARY for T-1-02 supply-chain reproducibility"

requirements-completed: [GEN-01, GEN-02]

# Metrics
duration: 8min
completed: 2026-06-23
---

# Phase 1 Plan 01: Fork Foundation Summary

**Forked Tnze/go-mc pinned at 539b4a3a onto branch ender-776 with PRs #294/#295/#296 merged in order, module path rewritten to github.com/imhinotori/go-mc, the runtime module building Go-only, the 774 generated trees snapshotted as the GEN-04 baseline, and the extractor container retargeted to JDK 25.**

## Performance

- **Duration:** 8 min
- **Started:** 2026-06-23T14:14:00Z
- **Completed:** 2026-06-23T14:22:01Z
- **Tasks:** 3
- **Files modified:** 343 (342 path rewrite + 1 jdkImage) + 380 baseline files created

## Accomplishments
- Established the forked go-mc as the project foundation on branch `ender-776`, branched off the exact pinned master commit `539b4a3a7f030332eb58b8a946116ae7907630d2`, with the three draft codegen PRs merged in dependency order (#294 tools pipeline → #295 framework adaptations/proto-774 → #296 generated 774 data) — all three merged CLEAN with zero conflicts (confirming the correct base commit, Pitfall 5 avoided).
- Rewrote the runtime module path from `github.com/Tnze/go-mc` to `github.com/imhinotori/go-mc` across go.mod, tools/go.mod, all 339 internal-import .go files, and the codegen templates; the runtime module builds with `go build ./...` using Go alone (no Java, no Docker, no codegen) — the GEN-01 structural checkpoint.
- Captured the 774 generated trees (`data/{packetid,registryid,item,entity,soundid,lang}` + `level/{block,component,biome}`, 380 files) into `baseline-774/` as the before-side of the GEN-04 776 diff.
- Applied the single mandatory retarget edit: `tools/extract.go` `jdkImage` bumped from `eclipse-temurin:21-jdk` to `eclipse-temurin:25-jdk` (the 26.2 jar is JDK-25 / class-version-69 bytecode); the tools module compiles.

## Task Commits

Each task was committed atomically (in addition to the four merge commits that brought in the fork base + PRs):

- **Merge: go-mc master base @539b4a3a (pinned)** - `58550402`
- **Merge: PR #294 tools codegen pipeline** - `5bb9a154`
- **Merge: PR #295 framework adaptations (proto 774)** - `022a01ef`
- **Merge: PR #296 generated 774 data baseline** - `d4dfecf4`
1. **Task 1: Clone fork, branch, merge #294-296 + module-path rewrite** - `24c3679a` (refactor)
2. **Task 2: Capture 774 baseline (Go-only build verified)** - `29e12c57` (chore)
3. **Task 3: Retarget extractor to eclipse-temurin:25-jdk** - `47af0f8e` (chore)

### Resolved PR head SHAs (T-1-02 reproducibility)

| Branch | Head SHA |
|--------|----------|
| pr294 (tools codegen pipeline) | `23fbe76efa3ba882b3d549d41033ecf66d1547fe` |
| pr295 (framework adaptations, proto 774) | `f9c5c05c4519b2c8fd76b16cedca51abe8c1d0c0` |
| pr296 (generated 774 data baseline) | `d6dece0e43ff372f2b716286cd21acd4ea728fa6` |
| pinned base (upstream master) | `539b4a3a7f030332eb58b8a946116ae7907630d2` |

## Files Created/Modified
- `go.mod` - Runtime module, path rewritten to `github.com/imhinotori/go-mc`, no JVM/codegen dependency
- `tools/go.mod` - Separate codegen module: `module github.com/imhinotori/go-mc/tools`, `require github.com/imhinotori/go-mc`, `replace github.com/imhinotori/go-mc => ../`
- `tools/extract.go` - `const jdkImage = "docker.io/library/eclipse-temurin:25-jdk"`
- 339 `*.go` files + codegen `*.tmpl` templates - internal import path rewritten to the fork path
- `.planning/phases/01-foundation-fork-codegen/baseline-774/` - 380-file snapshot of the 774 generated trees

### Baseline-774 captured trees (774 "before" counts for GEN-04)

| Tree | Files | Lines |
|------|-------|-------|
| data/packetid | 4 | 644 |
| data/registryid | 96 | 7313 |
| data/item | 1 | 10557 |
| data/entity | 1 | 1594 |
| data/soundid | 1 | 1855 |
| level/block | 10 | 8819 |
| level/component | 119 | 3619 |
| level/biome | 1 | 119 |
| data/lang | 147 | 1075835 |
| **Total** | **380** | **~1,110,355** |

## Decisions Made
- **Fork merged into repo root** (not a sibling clone): used `git merge --no-ff --allow-unrelated-histories` from branch `ender-776` so the runtime module lives at D:\ender alongside the already-committed `.planning/` + `CLAUDE.md`. The pinned base and all three PRs merged with no conflicts.
- **Module-path rewrite scope** extended beyond `go.mod`: the gate `! grep -q '^module github.com/Tnze/go-mc$' go.mod` only checks the module line, but 339 internal imports reference the old path. Rewriting only the module line would have broken `go build ./...` (Task 2 gate). Rewrote the path across all 342 `.go`/`.mod`/`.tmpl` files for a consistent, building tree (deviation Rule 3 — blocking issue).
- **JDK image used tag form, not digest:** Docker daemon was unavailable at build time (still starting), so `eclipse-temurin:25-jdk` could not be resolved to a `@sha256` digest. The plan explicitly permits the tag form with a SUMMARY note. Digest pinning is deferred to Plan 02's codegen run when Docker is up.
- **`unobfuscated_versions.json` left untouched** — `isNativelyUnobfuscated(major>=26)` routes 26.x to the standard manifest; adding a 26.2 entry would be the documented anti-pattern. Verified no `26.2` entry present.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Rewrote internal import paths tree-wide, not just the go.mod module line**
- **Found during:** Task 1 (module-path rewrite)
- **Issue:** The plan/gate phrasing focuses on the `go.mod` module line, but 339 fork `.go` files import `github.com/Tnze/go-mc/...`. Rewriting only the module line leaves internal imports unresolvable, which would have failed the Task 2 `go build ./...` gate.
- **Fix:** Applied the `Tnze/go-mc -> imhinotori/go-mc` rewrite across all 342 `.go`/`.mod`/`.tmpl` files in one mechanical pass; preserved `tools/go.mod`'s `replace => ../`.
- **Files modified:** go.mod, tools/go.mod, 339 *.go, codegen templates
- **Verification:** `go build ./...` exits 0 (runtime); `cd tools && go build ./...` exits 0; `! grep -q '^module github.com/Tnze/go-mc$' go.mod` clean; zero remaining `Tnze/go-mc` references in code/mod/tmpl.
- **Committed in:** `24c3679a` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking).
**Impact on plan:** The rewrite scope expansion was necessary for the runtime build to succeed; it is the natural consequence of changing the module path and introduces no scope creep. All plan gates pass as written.

## Deferred Issues
- **`tools/extract.go:122` passes `--source 21` to the single-file `java` launcher for `ExtractAll.java`.** This sets the extractor's own source language level, independent of the 26.2 jar's class-version (which JDK 25 loads at runtime regardless). The plan mandated exactly one edit (the `jdkImage` constant), so this was intentionally NOT changed here. Plan 02 should validate during the real codegen run whether the extractor uses any JDK 22-25 language features; if so, bump this flag to `25`. Out of scope for this Wave-1 git/build plan.
- **JDK image digest not pinned** (Docker unavailable). Pin `eclipse-temurin:25-jdk@sha256:<digest>` in Plan 02 once Docker is available, for full supply-chain reproducibility.

## Issues Encountered
- The `fork`/`upstream` remotes were pre-configured with SSH URLs (`git@github.com:`); `git remote set-url` to HTTPS appeared not to persist in `remote -v` output (likely a local `insteadOf` rewrite), but all `git fetch upstream` operations (pinned base + the three PR branches) succeeded via the working SSH key. No impact on the result.
- Git emitted LF→CRLF normalization warnings when staging on Windows. Harmless — content is unchanged beyond the path rewrite; git normalizes line endings on checkout.

## Threat Model Adherence
- **T-1-02 (Tampering — forked dependency):** mitigated. Pinned the exact base commit and merged specific PR-branch heads; the resolved SHAs are recorded above. No moving `@master` tracked.
- **T-1-03 (Tampering — tools/ leaking into runtime):** mitigated. `tools/go.mod` is a separate module with `replace github.com/imhinotori/go-mc => ../`; runtime `go.mod` contains no Java/codegen dependency (`! grep -i java go.mod` clean); `go build ./...` succeeds Go-only.
- **T-1-01 (Spoofing — 26.2 jar integrity):** accepted here; jar is not fetched in this plan (owned by Plan 02).

## Known Stubs
None — all work was mechanical (path rewrite, file snapshot, one constant edit). No placeholder logic or empty data sources introduced.

## User Setup Required
None for this plan. (The GitHub fork was already created by the user per `user_setup`.) Docker must be running before Plan 02's codegen run.

## Next Phase Readiness
- Fork foundation, two-module isolation, 774 baseline, and JDK-25 retarget are all in place — Plan 02 (26.2 codegen run) is unblocked once Docker is available.
- Carry-forward for Plan 02: pin the `eclipse-temurin:25-jdk` digest, verify `--source` flag adequacy, and (per T-1-01) verify the pinned 26.2 jar sha1 `823e2250…` during the codegen fetch.

## Self-Check: PASSED

All claimed files exist (go.mod, tools/go.mod, tools/extract.go, SUMMARY, baseline-774/data/packetid, baseline-774/data/registryid) and all 7 claimed commits (3 task commits + 4 merge commits) are present in git history.

---
*Phase: 01-foundation-fork-codegen*
*Completed: 2026-06-23*
