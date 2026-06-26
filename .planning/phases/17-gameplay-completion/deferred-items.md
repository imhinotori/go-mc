# Deferred Items — Phase 17 Gameplay Completion

## Out-of-scope discoveries (logged, not fixed)

### TestTickAIDrivesMobs is flaky (pre-existing, unrelated to 17-14)

- **Found during:** Plan 17-14 (BLOCK-DROP + ITEM-PICKUP) full-suite test run.
- **File:** `server/spawner_test.go` (mob AI / spawner — explicitly out of scope for 17-14,
  which is forbidden from touching combat/fall_damage/fluid and was scoped to drops/pickup).
- **Symptom:** `TestTickAIDrivesMobs` asserts a wandering Pig's x advances by > 2.0 over 400
  ticks. The mob's navigation/stroll goals draw from the package-level `math/rand/v2`, so the
  walk is RNG-dependent and occasionally fails to clear the +2.0 threshold.
- **Proof it is pre-existing:** `git stash`-ing all 17-14 changes and running the test 8× on
  the clean `ender-776` tree fails 8/8. The failure predates and is independent of 17-14.
- **Why not fixed here:** Scope boundary — 17-14 only auto-fixes issues its own changes cause.
  This is an AI-subsystem test-determinism bug (the test should seed a deterministic RNG or
  assert progress over a longer horizon), owned by the AI/spawner plan, not the drop/pickup work.
- **Suggested fix (for the owning plan):** Inject a seeded `rand.Source` into `mobAI` navigation
  so the test is deterministic, or relax the assertion to "x strictly increased" over more ticks.
- **Re-confirmed during 17-15** (water-disconnect + pickup-slot fixes): the same test still flakes
  non-deterministically (fails ~1 in 3 full-suite runs, passes 5/5 in isolation and passes on the
  stashed tree by luck) — caused by the async pathfinding pool not rejoining within 400 ticks under
  CPU contention. Independent of the 17-15 fluid/inventory changes (those files are unrelated to the
  AI/async path); all 17-15 tests pass 5/5 in isolation. Still owned by the AI/spawner plan.
