# Deferred Items — Phase 10 Worldgen Foundation

## From 10-01 (LCG / WorldgenRandom) execution

- **`go vet ./world/levelgen/...` (recursive) fails in the `surface` subpackage**:
  `world/levelgen/surface/heightmap_test.go:49: undefined: BuildWorldgenHeightmaps`.
  This is UNCOMMITTED in-progress work from plan **10-02** (the parallel-wave live-WG-heightmap
  plan) sitting in the working tree (`surface/system.go` modified, `surface/heightmap_test.go`
  untracked). It is NOT caused by 10-01 (which only touches `world/levelgen/random.go` +
  `random_test.go`). The `world/levelgen` package itself builds, vets, and tests clean.
  Resolution belongs to 10-02 when `BuildWorldgenHeightmaps` lands. No action for 10-01.

- **`-race` gate not runnable locally**: no GCC/cgo toolchain on this Windows host
  (`go test -race` requires cgo). Per 10-RESEARCH the `-race` gate runs in the project's
  Docker image. The LCG is pure stateless arithmetic with no shared mutable state, so it is
  race-clean by construction; the non-race suite is green. Run the Docker `-race` gate in CI.
