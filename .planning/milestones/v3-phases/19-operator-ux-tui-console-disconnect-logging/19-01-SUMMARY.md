---
phase: 19-operator-ux-tui-console-disconnect-logging
plan: 01
subsystem: operator-tooling
tags: [tui, bubbletea, slog, logging, charm-v2]
requires: []
provides:
  - "tui.New(dispatch commandFn) Model — bubbletea v2 console Model (viewport + textinput)"
  - "tui.NewHandler(prog *tea.Program) slog.Handler — stderr always + non-blocking TUI Send"
  - "tui.IsTerminal(fd uintptr) bool / tui.StdoutIsTerminal() — pure-Go TTY guard for the main() fork"
  - "logLineMsg contract: the external tea.Msg the bridge Sends into the program"
  - "bridge channel capacity = 1024 (bridgeCap)"
affects:
  - "go.mod / go.sum — 4 new pinned deps (charm.land v2 stack + x/term)"
tech-stack:
  added:
    - "charm.land/bubbletea/v2 v2.0.7"
    - "charm.land/bubbles/v2 v2.1.0"
    - "charm.land/lipgloss/v2 v2.0.4"
    - "golang.org/x/term v0.44.0"
    - "log/slog (stdlib — no new module)"
  patterns:
    - "bounded chan + single forwarder goroutine + non-blocking drop-on-full (log→TUI bridge)"
    - "embedded slog.Handler (stderr always-on) wrapped by a fan-out handler"
    - "control-byte sanitizer on every untrusted attr value (terminal-injection mitigation)"
key-files:
  created:
    - "server/tui/model.go — bubbletea v2 Model (viewport + textinput + resize + logLineMsg + Enter dispatch)"
    - "server/tui/handler.go — fanoutHandler slog.Handler (stderr always + non-blocking prog.Send)"
    - "server/tui/format.go — formatLine(slog.Record) + sanitize() control-byte stripper"
    - "server/tui/tty.go — IsTerminal/StdoutIsTerminal pure-Go TTY guard (anchors x/term)"
    - "server/tui/model_test.go — append / Enter-dispatch / empty-no-dispatch / resize"
    - "server/tui/handler_test.go — headless-stderr / non-blocking-drop (race) / sanitize"
  modified:
    - "go.mod / go.sum"
decisions:
  - "x/term anchored via a small IsTerminal seam in tty.go because go mod tidy strips deps with no importing source — Plan 02's main() consumes it directly"
  - "bridge channel cap = 1024 (mirrors the project inboundCap convention)"
  - "WithAttrs/WithGroup share the bridge channel so logger derivation keeps the fan-out"
metrics:
  duration: ~18min
  completed: 2026-06-26
---

# Phase 19 Plan 01: Operator Console TUI Backbone Summary

bubbletea v2 operator console (log viewport + command input) plus a race-safe,
non-blocking slog.Handler bridge that always writes structured lines to stderr and,
in TTY mode, fans formatted lines into the TUI via a bounded channel drained by one
forwarder goroutine — the rendering + logging foundation for TUI-01.

## What Was Built

Three atomic tasks, each committed separately:

1. **Pinned charm.land v2 dep stack + x/term** (`build(19)`, e1c50e03) — the four
   research-pinned deps (`charm.land/bubbletea/v2 v2.0.7`, `bubbles/v2 v2.1.0`,
   `lipgloss/v2 v2.0.4`, `golang.org/x/term v0.44.0`). `CGO_ENABLED=0 go build ./...`
   is clean and the cgo scan over the new import set is empty (no `.CgoFiles`) — the
   pure-Go static-binary gate holds. The TTY guard (`tty.go`) anchors x/term.

2. **bubbletea v2 console Model** (`feat(19)`, 5294eabd) — `server/tui/model.go`. One
   `Model` holds a `viewport.Model` (logs) + a `textinput.Model` (command line).
   `Update` handles `tea.WindowSizeMsg` (resize/first-time-create), `tea.KeyPressMsg`
   (Enter → read+clear input → dispatch; ctrl+c → quit), and `logLineMsg` (append →
   `SetContent` + `GotoBottom`). `View()` returns a `tea.View` with the declarative
   `AltScreen` field (v2 API, not a NewProgram option).

3. **slog.Handler log bridge** (`feat(19)`, 24406553) — `server/tui/handler.go` +
   `format.go`. `fanoutHandler` embeds a text handler (stderr, ALWAYS on) and, when a
   program is attached, pushes a `formatLine`'d record into a bounded `chan logLineMsg`
   (cap 1024) via a non-blocking `select`-default (drop-on-full + atomic drop counter).
   ONE forwarder goroutine drains the channel into `prog.Send` (single-writer). The
   emitting tick/net goroutine NEVER blocks on a slow/absent render. `sanitize()` strips
   ASCII control bytes from every attr value + message before a line reaches stderr or
   the viewport.

## Key Interfaces (for Plans 02 / 03)

- `func tui.New(dispatch commandFn) Model` — `commandFn = func(line string)`. The
  dispatch fn is called from the bubbletea goroutine on Enter; it MUST be non-blocking
  and hand the line to the tick via a message channel (Plan 02's `EnqueueConsoleCommand`).
- `func tui.NewHandler(prog *tea.Program) slog.Handler` — `prog == nil` → headless
  (stderr-only, today's behavior); `prog != nil` → stderr + non-blocking TUI Send.
- `func tui.IsTerminal(fd uintptr) bool` / `func tui.StdoutIsTerminal() bool` — the
  pure-Go TTY fork guard for Plan 02's `main()`.
- `logLineMsg` (unexported) is the external `tea.Msg` the bridge Sends; the Model
  appends it to the viewport. Both the handler and Model live in `package tui`, so the
  type stays unexported.
- **Bridge channel capacity = 1024** (`bridgeCap`).

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `go vet ./...` — clean.
- `go test ./server/tui/` — green (model: append/dispatch/empty/resize; handler:
  headless-stderr/non-blocking-drop/sanitize).
- **Docker `golang:1.26` `go test -race ./server/tui/` — green** (the non-blocking
  bridge test crosses 20 goroutines × 10k Handle calls; race-clean).
- v1-import guard: no `github.com/charmbracelet/` import anywhere in `server/tui/`.
- All 4 deps pinned at the research versions (`go list -m` confirmed).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] x/term + the charm deps evicted by `go mod tidy`**
- **Found during:** Task 1.
- **Issue:** The plan's Task 1 ran `go get` then `go mod tidy`, expecting the deps to
  remain in go.mod. Go's module pruning REMOVES deps with no importing source file, so
  after tidy go.mod was empty of all four deps and the cgo scan gave a false PASS
  (it scanned nothing).
- **Fix:** Created `server/tui/tty.go` (a pure-Go `IsTerminal`/`StdoutIsTerminal`
  seam — a real, Plan-02-consumed import) to anchor `golang.org/x/term`, and let
  Task 2/3's model+handler imports anchor the three charm deps. Re-ran `go get` +
  `go mod tidy`; all four now persist in go.mod at the pinned versions and the cgo
  scan resolves a real import set (genuinely empty `.CgoFiles`). `tty.go` was committed
  with Task 1 (it is dep-acquisition infrastructure + the Plan-02 TTY guard).
- **Files modified:** `server/tui/tty.go` (new), `go.mod`, `go.sum`.
- **Commit:** e1c50e03.

**2. [Rule 1 - Test bug] sanitize test asserted the wrong post-strip remainder**
- **Found during:** Task 3.
- **Issue:** `TestFormatLineSanitizes` asserted that `"evil\x1b[2Jname"` collapses to
  `"evilname"`. Only the ESC control byte (`\x1b`) is a control byte; the `[2J` payload
  is printable and correctly survives as inert literal text. The implementation was
  correct (stripping the ESC neutralizes the terminal command); the test expectation
  was wrong.
- **Fix:** Relaxed the assertion to require `"evil"` + `"name="` present and NO raw ESC
  byte — the actual security contract. Implementation unchanged.
- **Files modified:** `server/tui/handler_test.go`.
- **Commit:** 24406553.

### Threat Model (from PLAN.md) — all mitigated

- **T-19-01 (Tampering, terminal/ANSI injection):** `sanitize()` strips `\x00-\x1f` +
  `\x7f` from every attr value + msg before it reaches the viewport. Tested by
  `TestFormatLineSanitizes`.
- **T-19-02 (DoS, log-flood blocks the tick):** bounded chan (1024) + non-blocking
  send + drop-on-full + single forwarder. Tested by `TestBridgeNonBlocking` (10k
  concurrent emits never block, drops fire).
- **T-19-03 (DoS, unbounded memory):** the bounded+drop channel caps memory; the
  always-on stderr path keeps the full record even when a TUI line is dropped (asserted
  in `TestBridgeNonBlocking`: all 10k records hit stderr).

## Notes for Plan 02 / 03

- Plan 02 wires `main()`: `if tui.StdoutIsTerminal()` → build `tea.NewProgram(tui.New(
  dispatch), tea.WithContext(ctx))`, `slog.SetDefault(slog.New(tui.NewHandler(prog)))`,
  run `srv.Listen` on a goroutine + `prog.Run()` on main; else `tui.NewHandler(nil)` +
  blocking `srv.Listen` (today's behavior). The dispatch fn enqueues a tick message.
- `formatLine` lives in `package tui`; the disconnect taxonomy (Plan 03) just emits slog
  records with structured attrs (`name`, `uuid`, `addr`, `reason`) — they flow through
  this handler to BOTH stderr and the TUI automatically, sanitized.

## Self-Check: PASSED

All created files exist on disk; all 3 task commits (e1c50e03, 5294eabd, 24406553)
present in git history.
