---
phase: 19
phase-slug: operator-ux-tui-console-disconnect-logging
created: 2026-06-26
nyquist: enabled
---

# Phase 19 — Validation Strategy (Nyquist Dimension 8)

> Operator tooling (TUI console + disconnect logging). The 1:1 vanilla-jar mandate does NOT
> bind this phase; the binding gates are CGO_ENABLED=0 (pure-Go static), race-cleanliness,
> and the non-TTY/headless behavior staying byte-for-behavior identical to today.

## Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` |
| Config | none (standard `go test`) |
| Quick run | `go test ./server/... ./server/tui/...` |
| Full / race | Docker `golang:1.26`: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./server/... ./server/tui/...` (host CGO=0, no C compiler — race runs in Docker per STATE.md) |
| Lint gate | `CGO_ENABLED=0 go build ./...` + `go vet ./...` (NEVER gopls — stale, emits false diagnostics in this repo) |

## Phase Requirements → Test Map
| Req | Behavior | Type | Automated command | Plan |
|-----|----------|------|-------------------|------|
| TUI-01 | Model.Update appends a logLineMsg to the viewport | unit | `go test ./server/tui -run TestModel` | 19-01 |
| TUI-01 | Enter routes the input line to the dispatch fn | unit | `go test ./server/tui -run TestEnter` | 19-01 |
| TUI-01 | non-TTY handler writes plain stderr, no program | unit | `go test ./server/tui -run TestHeadless` | 19-01 |
| TUI-01 | bridge drops on full, never blocks (race) | unit+race | `go test -race ./server/tui -run TestBridgeNonBlocking` | 19-01 |
| TUI-01 | console command executes via the existing graph, no issuer | unit | `go test ./server -run TestRunConsoleCommand` | 19-02 |
| TUI-01 | TTY-vs-non-TTY fork preserves headless plain logging | unit/build | `CGO_ENABLED=0 go build ./cmd/sulfur` + headless `\| cat` operator check | 19-02 |
| TUI-02 | each drop category sets the right reason token (5 categories) | unit | `go test ./server -run TestReadLoopReason` | 19-03 |
| TUI-02 | slog join/leave carries identity + reason attrs | unit | `go test ./server -run TestReasonHuman` | 19-03 |
| TUI-02 | KeepAlive.removePlayer double-leave hardened (no nil deref) | unit | `go test ./server -run TestRemovePlayerDoubleLeave` | 19-03 |

## Sampling Rate
- **Per task commit:** `go test ./server/tui/...` + the touched server test (fast, < a few s).
- **Per wave merge:** `go test -count=1 ./server/... ./server/tui/...`.
- **Phase gate:** Docker `golang:1.26` `go test -race ./server/... ./server/tui/...` green (the project's
  standard concurrency gate), plus a real-operator visual pass (autonomous:false, 19-02 checkpoint)
  confirming the TUI renders, a typed command routes, a join/leave line appears, and the headless
  `| cat` path stays plain.

## Wave 0 Gaps (tests created in-task via tdd, not pre-staged)
- [ ] `server/tui/model_test.go` — Model append + Enter dispatch + resize (TUI-01, 19-01)
- [ ] `server/tui/handler_test.go` — TTY vs non-TTY fan-out + non-blocking drop under load (TUI-01, 19-01)
- [ ] `server/commands_test.go` (extend) — `runConsoleCommand` executes via existing graph, no issuer (TUI-01, 19-02)
- [ ] `server/client_test.go` + `server/keepalive_test.go` (extend) — full reason taxonomy + double-leave (TUI-02, 19-03)
- [ ] Framework install: `go get` the 4 pinned charm v2 / x-term deps (none in go.mod yet) — CGO=0 dep-scan gate confirms no cgo crept in.

## Out of scope for validation
- A real premium-client / multi-client scenario (that was Phase 17/18). Here a single local client
  + the operator terminal is the full surface.
