# Task: fix-worker-neighbor-backpressure

Read `CLAUDE.md` and `.planning/audits/PARITY-AUDIT-2026-07-12-CLAUDE-WAVE.md` completely.

## Objective

Fix the P0 terrain-worker bug where an auto-requested neighbor can be marked requested before a nonblocking send is accepted, leaving a wanted 3x3 permanently incomplete under pool backpressure.

## Allowed edit

Only modify `world/worker.go`. Do not edit tests or any other file.

## Required method

1. Trace `requestNeighbors`, `requestInternal`, requested dedupe and `pool.Submit` end to end.
2. Preserve bounded concurrency and dedupe, but guarantee a neighbor is never permanently marked requested unless its request is accepted or otherwise durably queued.
3. Preserve cancellation and avoid blocking the owner indefinitely.
4. Run `gofmt -w world/worker.go`, `go test ./world -run 'TestWorkerPool|TestWorker' -count=1 -timeout=120s`, and `git diff --check`.

No commit. Do not broaden worldgen behavior.

