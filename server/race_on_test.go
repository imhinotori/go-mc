//go:build race

package server

// raceEnabled is true when the binary is built with the race detector (`go test -race`). Timing-
// sensitive tests (the wall-clock perf gate) consult it to skip under -race, where the race
// instrumentation's per-access overhead inflates absolute ns/(mob·tick) and would trip an absolute
// timing cap on instrumentation cost rather than a real regression. The -race correctness coverage
// comes from the equality oracle + nav/float tests, which carry no timing assertion.
const raceEnabled = true
