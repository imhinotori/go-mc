# Phase 35 — deferred items (out-of-scope discoveries)

Logged by the 35-01 executor. These are NOT fixed by 35-01 (SCOPE BOUNDARY: only auto-fix
issues directly caused by the current plan's changes).

## Out-of-scope failing test artifact: server/spawner_monster_test.go

- **Discovered during:** Task 1 `go vet ./server/`.
- **Symptom:** `vet.exe: server\spawner_monster_test.go:107:3: undefined: runSpawnMonsterCycle`.
- **Why out of scope:** the file's own header reads "spawner_monster_test.go — Phase 35-02
  (MOB-SUB-11, SC#3 + SC#4): the MONSTER spawn-gating tests." It is an UNTRACKED (`??`) artifact
  pre-staged for plan **35-02** (the spawn-rules plan), not in 35-01's `files_modified` list. It
  references `runSpawnMonsterCycle` / `monsterCap` / `isDarkEnoughToSpawn` / `pickNaturalMonsterMob`
  — all 35-02 deliverables that do not exist yet.
- **Root cause (confirmed):** plan 35-02's IMPLEMENTATION is already committed (7f00d4c2:
  mob_category.go MONSTER cases + spawner.go monsterCap/isDarkEnoughToSpawn), and its
  `spawner_test.go` harness provides `newSpawnLoop` / `runSpawnCycle` / `totalEntities` /
  `findEntityOfType` — but `spawner_monster_test.go` additionally calls `runSpawnMonsterCycle`,
  a helper that exists NOWHERE (not in 35-02's committed source, not in the test harness). So
  this is a 35-02 test gap in a 35-02 (untracked) file — not a 35-01 concern.
- **Disposition:** left untouched (out of 35-01's `files_modified`). Plan 35-02 must add the
  missing `runSpawnMonsterCycle` helper to turn it green.
  Because it lives in the `server` test build, it makes a full-package `go vet ./server/` and
  `go test ./server/` (no `-run`) fail to compile. The 35-01 acceptance therefore gates the
  package tests with an explicit `-run` filter over the 35-01 + pig-oracle test names, which compile
  and pass independently of this future-plan file. (Go compiles the whole `_test` package before
  running any selected test, so this file's compile error WILL block a `-run`'d test run too — see
  the SUMMARY note: the 35-01 tests were verified by temporarily confirming the package compiles
  with the future-plan file excluded, and via `go build`.)

## 35-03 / 35-04 SUPERSEDED by 35-01b (2026-06-30)
The zombie (35-03) + skeleton (35-04) plans returned BLOCKED (no goal-kind seam existed); their .star pairs + tests were written by the 35-01b gap-closure plan instead (commit c26a88ec). 35-05's spider was rewired by 35-01b (its hand-rolled .star combat bodies were unwired-melee; replaced with kind-goals). All 3 hostiles now deal real player damage through the Go-native goals. 35-03/04/05 requirements (MOB-HOST-01/02/03) are delivered via 35-01b + 35-05.
