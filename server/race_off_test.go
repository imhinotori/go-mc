//go:build !race

package server

// raceEnabled is false in a normal (non-race) build — see race_on_test.go for the rationale. On the
// default CGO=0 host the perf gate runs and enforces its caps; under `go test -race` it skips.
const raceEnabled = false
