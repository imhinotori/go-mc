---
phase: 19-operator-ux-tui-console-disconnect-logging
verified: 2026-06-26T00:00:00Z
status: passed
score: 3/3 must-haves verified
overrides_applied: 0
---

# Phase 19: Operator UX — TUI console + disconnect logging Verification Report

**Phase Goal:** The operator runs + watches Sulfur from a proper terminal console — command input + live scrolling logs in one screen — with every player drop logged with its reason.
**Verified:** 2026-06-26
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (mapped to the 3 ROADMAP Success Criteria)

| # | Truth (Success Criterion) | Status | Evidence |
|---|---------------------------|--------|----------|
| 1 | bubbletea+bubbles console renders textinput + live log viewport; typed commands dispatch through the EXISTING command system (TUI-01) | ✓ VERIFIED | `server/tui/model.go:35-50` Model holds `viewport.Model`+`textinput.Model`; `model.go:79-84` Enter → `dispatch(line)` + clear; `model.go:87-92` logLineMsg → viewport SetContent+GotoBottom. `cmd/sulfur/main.go:286` dispatch = `tick.EnqueueConsoleCommand`. `server/commands.go:336-355` runConsoleCommand → `executeCommand(ctx, line)` (the EXISTING graph). Drained on tick at `server/tick.go:1004-1009`. Tests: TestEnterDispatches, TestModelAppendsLog, TestRunConsoleCommand all green. Operator-validated this session (TUI rendered, commands routed, join/leave streamed). |
| 2 | TUI degrades gracefully when stdout is NOT a TTY (headless/Docker → plain logging, server runs identically) (TUI-01) | ✓ VERIFIED | `cmd/sulfur/main.go:279` `if tui.StdoutIsTerminal()` fork. TTY branch (279-295): TUI + `tui.NewHandler(prog)` + goroutine `srv.Listen` + ctx cancel. Non-TTY branch (300-301): `slog.SetDefault(slog.New(tui.NewHandler(nil)))` then UNCHANGED `log.Fatal(srv.Listen(*addr))` — byte-for-behavior identical. `server/tui/tty.go:13` pure-Go `term.IsTerminal` (no cgo). `handler.go:41-49` NewHandler(nil) → stderr-only, no forwarder goroutine. Test: TestHeadlessStderr green. |
| 3 | Every player disconnect logs WHY with identity + reason, in BOTH TUI log stream + structured logs (TUI-02) | ✓ VERIFIED | Full taxonomy tagged at seams: `client.go:133` write_error, `:156` protocol_error, `:190` backpressure; `gameplay_tick.go:159` timeout; `playerlist.go:53` kicked; `server.go:108` protocol_mismatch, `:124` login_failure, `:139` config_failure; default `quit`. Leave line `gameplay_tick.go:425-427` slog.Info "player left" with name/uuid/addr/reason/detail; join `:407`. `disconnect_reason.go:20` reasonHuman maps to the 5 TUI-02 categories. slog default handler (main.go:288/300) fans to TUI viewport + stderr both paths. Double-leave hardened `keepalive.go:96-99` ok-guard. Tests: TestReadLoopReason_*, TestWriteLoop_WriteError, TestSendFull_Backpressure, TestRemovePlayerDoubleLeave, TestReasonHumanString, TestDisconnectReason all green. |

**Score:** 3/3 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `server/tui/model.go` | bubbletea v2 Model (viewport+textinput+resize+dispatch) | ✓ VERIFIED | 111 lines; Update handles WindowSize/KeyPress/logLineMsg; charm.land v2 imports |
| `server/tui/handler.go` | fanout slog.Handler: stderr always + non-blocking drop-on-full | ✓ VERIFIED | `Handle` always calls embedded text handler; bridge `select{}/default` drops + counts (`:73-80`); single forwarder goroutine |
| `server/tui/format.go` | formatLine + control-byte sanitizer | ✓ VERIFIED | sanitize strips \x00-\x1f/\x7f (ANSI-injection guard); formatLine renders LEVEL time msg attrs |
| `server/tui/tty.go` | pure-Go TTY probe | ✓ VERIFIED | golang.org/x/term, StdoutIsTerminal |
| `server/commands.go` | runConsoleCommand (no issuer) + EnqueueConsoleCommand (non-blocking) | ✓ VERIFIED | executeCommand via withConsole+grant-all resolver, no withExecutor; Enqueue drop-on-full |
| `server/tick.go` | consoleCmd chan + tick drain | ✓ VERIFIED | field `:204`, made `:719`, drained `:1004-1009` in drainRegistrations |
| `cmd/sulfur/main.go` | TTY fork; ctx-cancel on tea.Quit | ✓ VERIFIED | `:279` fork; both branches install slog handler; non-TTY keeps log.Fatal(srv.Listen) |
| `server/client.go` | protocol_error/write_error/backpressure classification | ✓ VERIFIED | readLoop EOF/ErrClosed→quit vs decode→protocol_error; writeLoop/Send tag |
| `server/keepalive.go` | double-leave-hardened removePlayer | ✓ VERIFIED | `:96` ok-guard, early return on missing key |
| `server/gameplay_tick.go` | slog join/leave with structured attrs | ✓ VERIFIED | timeout tag + join/leave slog lines |
| `server/disconnect_reason.go` | reason→human map | ✓ VERIFIED | reasonHuman, 5 TUI-02 categories |

### Key Link Verification

| From | To | Via | Status |
|------|-----|-----|--------|
| handler.go | tea.Program.Send | single forwarder draining bounded chan logLineMsg | ✓ WIRED (`handler.go:86-94`) |
| model.go | viewport.Model | logLineMsg → SetContent + GotoBottom | ✓ WIRED (`model.go:87-92`) |
| main.go | tui.New / tui.NewHandler | term.IsTerminal fork | ✓ WIRED (`main.go:279,286-288,300`) |
| EnqueueConsoleCommand | runConsoleCommand | consoleCmd channel drained on tick | ✓ WIRED (`commands.go:315`→`tick.go:1004`→`runConsoleCommand`) |
| client.go readLoop | SetDisconnectReason("protocol_error") | EOF/ErrClosed classification before Close | ✓ WIRED (`client.go:150-156`) |
| gameplay_tick.go leave | slog log stream (TUI + stderr) | slog.Info reason=DisconnectReason() | ✓ WIRED (`gameplay_tick.go:424-427`) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full build, cgo-free | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Static analysis | `go vet ./server/ ./server/tui/ ./cmd/sulfur` | exit 0, clean | ✓ PASS |
| Unit tests | `go test -count=1 ./server/ ./server/tui/` | both ok | ✓ PASS |
| No cgo in dep tree | `go list -deps -f CgoFiles ./cmd/sulfur` | empty | ✓ PASS |
| No `import "C"` | grep server/ cmd/ | none | ✓ PASS |
| charm v2 paths (not v1 direct) | go.mod | charm.land/* direct; charmbracelet/* indirect only | ✓ PASS |
| Console dispatch routing | TestRunConsoleCommand + bounds | green | ✓ PASS |
| Non-blocking enqueue | TestEnqueueConsoleCommandNonBlocking | green | ✓ PASS |
| Disconnect taxonomy | TestReadLoopReason_*/WriteLoop/Backpressure/DisconnectReason/ReasonHuman | green | ✓ PASS |
| Double-leave no panic | TestRemovePlayerDoubleLeave | green | ✓ PASS |
| Headless stderr + bridge drop | TestHeadlessStderr / TestBridgeNonBlocking | green | ✓ PASS |

### Requirements Coverage

| Requirement | Description | Status | Evidence |
|-------------|-------------|--------|----------|
| TUI-01 | bubbletea console (textinput+viewport), dispatch via existing graph, graceful non-TTY degrade | ✓ SATISFIED | Truths 1+2; main.go TTY fork; runConsoleCommand→executeCommand |
| TUI-02 | every disconnect logs why with identity+reason in TUI + structured logs | ✓ SATISFIED | Truth 3; full taxonomy at all drop seams; slog join/leave with attrs |

### Anti-Patterns Found

None blocking. The `\ ok-guard` typo in `keepalive.go:95` is inside a `//` comment line (cosmetic, no compile/behavior impact — build is clean). Drop-on-full and stderr-only fallbacks are intentional design (non-blocking bridge, headless degrade), not stubs.

### Tick Discipline (TICK-05)

Console command crosses to the tick via the bounded `consoleCmd` channel and executes ONLY on the owner goroutine in `drainRegistrations` (`tick.go:1004-1009`); EnqueueConsoleCommand never mutates tick-owned state off-thread (drop-on-full from the TUI goroutine). `go test ./server/` (which includes the race-sensitive tick tests) is green.

### Human Verification Required

None outstanding. The TUI-01 operator checkpoint (interactive TUI renders, typed commands route through the existing graph, join/leave stream, headless stays plain) was OPERATOR-VALIDATED in the 19-02 session (recorded in 19-VALIDATION.md). Code + all gates confirm the same surfaces.

### Gaps Summary

No gaps. All 3 success criteria are delivered in substantive, wired, data-flowing code with dedicated passing tests. Build is cgo-free under CGO_ENABLED=0, vet is clean, tests are green, the non-TTY path preserves the exact `log.Fatal(srv.Listen)` behavior, and console commands honor tick discipline. Phase goal achieved.

---

_Verified: 2026-06-26_
_Verifier: Claude (gsd-verifier)_
