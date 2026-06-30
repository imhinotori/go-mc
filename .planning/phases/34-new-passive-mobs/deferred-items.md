# Phase 34 Deferred / Out-of-Scope Items

## 34-03 (chicken) executor — out-of-scope discovery

- **server/cow_test.go is the parallel 34-01 (cow) executor's TDD RED commit — currently a failing /
    non-compiling test by design, landed between this plan's two commits (cow commit `d429836d`,
    "test(34-01): add failing cow tests").**
  - Found during 34-03 final verification (`go test ./...`). The file references symbols the cow
    GREEN phase has not landed yet: `loop.tryMilkCow`, `loop.tick`, `component`, `bytesReader`,
    `cow.attributes.value`. So it fails to COMPILE, which blocks `go test ./server/` for the whole
    package until 34-01's GREEN commit lands those symbols. (Initially mis-read as an untracked WIP;
    it is in fact a tracked, intentional RED commit from 34-01.)
  - NOT touched by 34-03 (chicken). It belongs to 34-01; fixing it here would race with that plan.
  - 34-03's chicken tests + the full pig oracle were verified GREEN by temporarily PARKING cow_test.go
    (moving it aside UNMODIFIED, running the suite — all chicken + pig tests pass, full `go test
    ./server/` ok in 8.6s — then restoring it byte-for-byte). cow_test.go is left exactly as the cow
    executor committed it.
  - **34-04 (gate) blocker:** the phase-wide `go test ./...` / the `-race` Docker run will stay RED
    until 34-01's cow GREEN commit compiles. 34-04 should run the phase gate only after cow + sheep
    have landed their Go symbols.
  - **RESOLVED (34-01 GREEN, commit `ef8651ea`):** the cow GREEN commit landed `tryMilkCow` +
    `createFilledResult` (attack_dispatch.go) + the `categoryOf(Cow) -> CREATURE` case (mob_category.go).
    cow_test.go now COMPILES and all 6 cow tests pass; `CGO_ENABLED=0 go test ./server/ -count=1` is
    GREEN (7.77s) and `TestPluginPigEqualsGoNativePig` is byte-identical. The RED test referenced
    `loop.tryMilkCow`, `loop.advance`, `component`, `bytes.NewReader`, `cow.attributes.GetValue` (the
    final test uses the real APIs, not the `bytesReader`/`attributes.value` placeholders the chicken
    executor read in the interim RED snapshot). The phase-wide suite is no longer blocked by the cow gap.
