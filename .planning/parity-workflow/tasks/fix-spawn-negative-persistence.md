# Task: fix-spawn-negative-persistence

Read `CLAUDE.md` and `.planning/audits/PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md` completely.

## Objective

Persist block-centered spawn coordinates without truncating negative values toward zero.

## Allowed edit

Only modify `server/saveddata.go`. Do not edit tests or any other file.

## Required method

1. Trace the runtime `.5` center representation through `snapshotLevelData` and the reload path.
2. Verify vanilla BlockPos flooring semantics from the JAR/utilities.
3. Convert X/Y/Z with floor semantics so `-3.5` persists as block coordinate `-4`, while positive centers remain unchanged.
4. Do not address the separate `[0,0,0]` sentinel or spawn-search radius in this task.
5. Run `gofmt -w server/saveddata.go`, relevant saved-data tests, and `git diff --check`.

No commit.

