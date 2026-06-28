# Phase 27 — Deferred Items

Out-of-scope discoveries logged during execution (per the executor scope boundary). NOT fixed in
the plan that found them.

## 27-01

- **`world` package -race suite exceeds `-timeout 900s` under the race detector.** Found running
  Task 3's Docker -race gate over `./server/ ./world/...`. The `world` suite takes ~250s
  un-instrumented; under `-race` (5-10x slowdown) it overflows the 900s window and the run reports
  a timeout panic (an all-goroutines dump rooted in `TestSafeSpawnDeterministic` →
  `world/structure` stronghold ring generation — a TIMEOUT dump, NOT a `WARNING: DATA RACE`).
  - **Out of scope for 27-01:** Plan 27-01 modified ONLY `package server`; `world` imports nothing
    from `server` and zero `world/**` files were touched, so the extraction cannot have changed
    `world`'s race characteristics. Confirmed pre-existing.
  - **Evidence it is a timeout, not a race:** re-running `./world/` with `-timeout 2400s` passes
    clean (see 27-01-SUMMARY "Task 3"). The race detector is clean; the 900s ceiling is simply too
    low for the instrumented worldgen suite.
  - **Suggested follow-up (a future ops/CI plan, not gameplay):** either raise the `world` race-gate
    timeout to ~1800-2400s, shard the `world` race run, or mark the heaviest worldgen tests
    `testing.Short()`-skippable under the race gate. The `./server/` race gate (the load-bearing
    gate for the regionization phase) stays at 900s and passes in ~19s.
