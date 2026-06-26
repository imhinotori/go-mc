---
phase: 19-operator-ux-tui-console-disconnect-logging
plan: 02
subsystem: operator-tooling
tags: [tui, console, command-dispatch, slog, bubbletea, tty-fork, tick-message]
requires:
  - "tui.New(dispatch commandFn) Model — bubbletea v2 console Model (Plan 19-01)"
  - "tui.NewHandler(prog *tea.Program) slog.Handler — stderr always + non-blocking TUI Send (Plan 19-01)"
  - "tui.StdoutIsTerminal() bool — pure-Go TTY guard (Plan 19-01)"
provides:
  - "(*TickLoop).EnqueueConsoleCommand(line string) — TUI→tick non-blocking drop-on-full console seam"
  - "(*TickLoop).runConsoleCommand(line string) — tick-goroutine executor through the EXISTING command graph, grant-all resolver, NO issuer, replies to slog"
  - "TickLoop.consoleCmd chan string — bounded console-line channel (cap = registerBuffer = 64), drained in drainRegistrations"
  - "main() TTY fork: TTY → bubbletea console + slog TUI handler + Listen-on-goroutine + ctx-cancel shutdown; non-TTY → unchanged log.Fatal(srv.Listen) plain-stderr path"
affects:
  - "cmd/sulfur/main.go — the boot tail now forks on tui.StdoutIsTerminal()"
  - "server/tick.go — TickLoop gains the consoleCmd field + drain case"
tech-stack:
  added:
    - "log/slog used directly in server/commands.go (console reply path) + cmd/sulfur/main.go (handler install)"
    - "charm.land/bubbletea/v2 + server/tui consumed by cmd/sulfur (first runtime consumer of the 19-01 backbone)"
  patterns:
    - "console line crosses to the tick via a bounded message channel (mirrors register/unregister — TICK-05)"
    - "non-blocking drop-on-full enqueue (the TUI goroutine never blocks the tick, Pitfall 7 / T-19-04)"
    - "grant-all permission resolver + NO executor → executorFrom ok=false → issuer-acting commands no-op (console = operator)"
    - "boot-time TTY fork: TUI program on main + Listen on a goroutine vs blocking Listen on main"
key-files:
  created: []
  modified:
    - "server/commands.go — runConsoleCommand + EnqueueConsoleCommand (+ log/slog import)"
    - "server/tick.go — consoleCmd field, NewTickLoop init, drainRegistrations console case"
    - "server/commands_test.go — TestRunConsoleCommand / TestRunConsoleCommandBounds / TestEnqueueConsoleCommandNonBlocking (+ time import)"
    - "cmd/sulfur/main.go — TTY fork tail (tui + tea + slog + golang.org/x/term via tui.StdoutIsTerminal)"
decisions:
  - "consoleCmd capacity = registerBuffer (64) — reused the existing tick join/leave channel bound; console lines are rarer than the 5ms wake so 64 is ample, and drop-on-full bounds a flood"
  - "EnqueueConsoleCommand lives on *TickLoop (not gameTick): the plan's pseudocode wrote gp.EnqueueConsoleCommand but the tick owns the command path + the consoleCmd channel; main's closure calls tick.EnqueueConsoleCommand (tick is in scope at main.go:133). This keeps the seam where runCommand/runConsoleCommand live."
  - "main() uses tui.StdoutIsTerminal() (the 19-01 seam that anchors golang.org/x/term) rather than a duplicate term.IsTerminal import — single TTY-guard source, no new direct x/term import in main"
  - "the console reply goes to slog (Info on success, Warn on failure) — NOT a player SystemChat — because a console source has no issuing *tickPlayer; the slog handler fans it to BOTH the TUI viewport and stderr"
metrics:
  duration: ~14min
  completed: 2026-06-26
status: 2-of-3-tasks-complete — Task 3 is a pending autonomous:false operator human-verify checkpoint
---

# Phase 19 Plan 02: Console Dispatch + main() TTY Fork Summary

Wires the operator console END-TO-END behind the 19-01 backbone: a `runConsoleCommand`
dispatch seam that routes a typed line through the EXISTING command graph with NO player
issuer (grant-all resolver) and replies to the slog log stream; a bounded `consoleCmd`
tick-message channel so the TUI goroutine never executes command handlers off-thread
(TICK-05); and the `main()` TTY fork that runs the bubbletea program when stdout is a
terminal and falls back to today's exact plain-stderr `log.Fatal(srv.Listen)` otherwise.

## Status: 2 of 3 tasks complete — operator checkpoint PENDING

Tasks 1 + 2 are implemented, tested, and committed. **Task 3 is an `autonomous:false`
operator human-verify checkpoint** (run Sulfur in a real terminal, type a command, connect
a vanilla client, Ctrl-C, then a headless `| cat` check). It cannot be automated and is
returned to the orchestrator for the operator to perform. The requirement **TUI-01 is NOT
marked complete** in STATE/ROADMAP/REQUIREMENTS until the checkpoint passes.

## What Was Built

1. **Console command dispatch seam** (`feat(19)`, `ced76463`) — `server/commands.go` +
   `server/tick.go`:
   - `TickLoop.consoleCmd chan string` (cap `registerBuffer` = 64), initialized in
     `NewTickLoop`, drained by a new non-parking `case line := <-t.consoleCmd:` in
     `drainRegistrations` (BEFORE `default`), so a console line runs `runConsoleCommand`
     ON THE OWNER goroutine exactly like register/unregister (TICK-05 / Pitfall 7).
   - `(*TickLoop).runConsoleCommand(line)` — length-bounds (`maxCommandLen`, reused) BEFORE
     any parse work (T-19-05), installs a grant-all `permissionResolver` (the console IS the
     operator) and **NO executor** (so `executorFrom(ctx)` returns ok=false → `/tp` and other
     issuer-acting commands no-op), calls the existing `executeCommand` seam, and replies to
     the slog stream (`Info` ok / `Warn` on error) — never a player SystemChat, never a panic.
   - `(*TickLoop).EnqueueConsoleCommand(line)` — non-blocking drop-on-full send onto
     `consoleCmd` (the method the TUI's Enter closure calls from the TUI goroutine; T-19-04).

2. **main() TTY fork** (`feat(19)`, `9db922b6`) — `cmd/sulfur/main.go`:
   - `if tui.StdoutIsTerminal()` (pure-Go x/term guard, CGO=0 clean): build
     `tui.New(func(line string){ tick.EnqueueConsoleCommand(line) })`,
     `tea.NewProgram(model, tea.WithContext(ctx))`,
     `slog.SetDefault(slog.New(tui.NewHandler(prog)))`, run `srv.Listen` on a goroutine,
     `prog.Run()` on main, and `cancel()` on `tea.Quit`/Ctrl-C to unwind every server
     goroutine via the shared ctx.
   - else (headless/Docker/piped): `slog.SetDefault(slog.New(tui.NewHandler(nil)))` +
     the EXACT `log.Fatal(srv.Listen(*addr))` blocking path this binary has today
     (byte-for-behavior unchanged, Pitfall 4).
   - The `srv.Logger.Printf("Sulfur listening …")` startup line stays BEFORE the fork via the
     embedded `*log.Logger` (it goes to stderr; in TUI mode it is emitted before the slog
     handler is installed, so it is NOT in the viewport — expected, per the checkpoint wording).

## Key Interfaces (for later op-command work)

- `func (t *TickLoop) EnqueueConsoleCommand(line string)` — call from any goroutine; the
  send is non-blocking drop-on-full onto `consoleCmd`. The line executes on the next tick
  drain via `runConsoleCommand`.
- `consoleCmd` capacity = `registerBuffer` = **64**.
- `runConsoleCommand` is the parallel to `runCommand` for a no-issuer console source; a future
  ops-list / per-node console policy plugs into its grant-all resolver (the structural hook),
  not into the dispatch (T-19-06 deferred: full ops model).

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0 (whole repo).
- `go vet ./cmd/sulfur ./server/` — clean.
- `go test ./server/ -run 'TestRunConsoleCommand|TestEnqueueConsoleCommand' -v` — green
  (grant-all + no-issuer routing; empty/over-long bounds no-op; drop-on-full + tick-drain
  delivery to runConsoleCommand).
- `go test ./server/ ./server/tui/` — green (no regression).
- `gameplay_tick.go` NOT touched (reserved for Plan 19-03) — confirmed via `git diff`.
- NOTE: gated ONLY on `CGO_ENABLED=0 go build` + `go vet` + `go test` per the plan — NEVER on
  gopls (stale gopls falsely reports the `charm.land/...` imports unresolved in this repo even
  though the CGO=0 build is exit 0).

## Pending Checkpoint (Task 3 — autonomous:false operator human-verify)

The operator must, in a REAL terminal:
1. `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur`
2. `./sulfur.exe -seed 777` → expect an alt-screen TUI (log viewport + "type a command…"
   input). The "Sulfur listening …" line is emitted before the TUI installs, so it is NOT in
   the viewport — expected.
3. Type `say hi` + Enter → expect the input clears and a `console command cmd=say hi` line in
   the viewport (routed through the existing graph on the tick).
4. Connect a vanilla 26.2 client to localhost:25565 → expect a join line in the viewport (the
   slog stream is live; the full per-reason leave taxonomy is Plan 19-03).
5. Ctrl-C → the TUI exits cleanly and the process stops (ctx cancel unwinds the server).
6. `./sulfur.exe -seed 777 | cat` (non-TTY) → expect NO TUI, just plain structured stderr log
   lines (today's behavior). Ctrl-C stops it.

Resume signal: "approved" → mark TUI-01 complete (STATE/ROADMAP/REQUIREMENTS) and advance the
plan; else describe what broke.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] EnqueueConsoleCommand placed on *TickLoop, not gameTick**
- **Found during:** Task 1.
- **Issue:** The plan's research Example 4 wrote `(g *gameTick) EnqueueConsoleCommand` /
  `g.consoleCmd`, but the command path (`runCommand`, `executeCommand`, the cmdGraph) and the
  drain loop (`drainRegistrations`) all live on `*TickLoop` — `gameTick` has no consoleCmd
  channel and no drain loop. Placing the seam on `gameTick` would have required a second
  cross-type channel hop.
- **Fix:** Put `consoleCmd`, `EnqueueConsoleCommand`, and `runConsoleCommand` on `*TickLoop`
  (the file_scope constraint explicitly directs this). `main()`'s closure calls
  `tick.EnqueueConsoleCommand` (`tick` is in scope at main.go:133). This is the documented
  intent of the plan's `<critical_constraints>`, not a behavior change.
- **Files modified:** `server/commands.go`, `server/tick.go`, `cmd/sulfur/main.go`.
- **Commits:** `ced76463`, `9db922b6`.

**2. [Rule 3 - Blocking] main() uses tui.StdoutIsTerminal() instead of a direct term.IsTerminal import**
- **Found during:** Task 2.
- **Issue:** The plan's Example 3 imported `golang.org/x/term` directly and called
  `term.IsTerminal(int(os.Stdout.Fd()))`. Plan 19-01 already exposes `tui.StdoutIsTerminal()`
  (the seam that anchors x/term so `go mod tidy` doesn't evict it), and the 19-01 SUMMARY's
  "Notes for Plan 02" explicitly directs Plan 02 to use it.
- **Fix:** Called `tui.StdoutIsTerminal()` — a single TTY-guard source, no duplicate x/term
  import in main. Identical behavior (pure-Go isatty on stdout).
- **Files modified:** `cmd/sulfur/main.go`.
- **Commit:** `9db922b6`.

### Threat Model (from PLAN.md) — dispositions honored

- **T-19-04 (Tampering/race, console mutating tick state off-thread):** MITIGATED —
  `EnqueueConsoleCommand` hands the line to the tick via `consoleCmd`; `runConsoleCommand`
  runs on the owner goroutine inside `drainRegistrations`. Tested by
  `TestEnqueueConsoleCommandNonBlocking` (drain delivers to runConsoleCommand) +
  `TestRunConsoleCommand`.
- **T-19-05 (DoS, unbounded console input):** MITIGATED — `runConsoleCommand` reuses
  `maxCommandLen` (256) BEFORE Execute; `EnqueueConsoleCommand` is bounded (cap 64) +
  drop-on-full. Tested by `TestRunConsoleCommandBounds` + `TestEnqueueConsoleCommandNonBlocking`.
- **T-19-06 (EoP, console grants all nodes):** ACCEPTED by design — the console IS the
  operator (local stdin); the grant-all `permissionResolver` is the structural hook a future
  ops-list model plugs into (deferred).

## Self-Check: PASSED

- `server/commands.go`, `server/tick.go`, `server/commands_test.go`, `cmd/sulfur/main.go` all
  modified on disk.
- Commits `ced76463` (Task 1) + `9db922b6` (Task 2) present in git history.
- Task 3 (operator checkpoint) intentionally NOT executed — returned to the orchestrator.
