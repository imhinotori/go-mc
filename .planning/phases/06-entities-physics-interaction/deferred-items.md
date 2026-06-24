# Phase 6 Deferred Items

Out-of-scope discoveries logged during execution (not fixed — they predate or fall outside
the current plan's changes).

## From Plan 06-02 (entity tracker)

- **Pre-existing golangci-lint issues in `server/server.go` and `server/configuration_test.go`**
  (logged 06-02): `errcheck` on `conn.Close` (server.go:71), and `staticcheck` QF1001
  (De Morgan's law, configuration_test.go:51) + three QF1008 (embedded `Logger` selector,
  server.go:91/106/120). These are pre-existing (untouched by 06-02), so out of scope per the
  deviation scope boundary. Fix in a dedicated cleanup pass or when those files are next
  modified for a feature.
