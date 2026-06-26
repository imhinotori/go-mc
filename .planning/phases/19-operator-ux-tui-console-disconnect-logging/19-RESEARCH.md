# Phase 19: Operator UX — TUI console + disconnect logging - Research

**Researched:** 2026-06-26
**Domain:** Terminal UI (charmbracelet bubbletea v2), structured logging (log/slog), TTY detection, disconnect-reason taxonomy
**Confidence:** HIGH

## Summary

Phase 19 gives the operator a real terminal console: a command-input line + a live scrolling
log viewport in one screen (TUI-01), and logs *why* every player drops (TUI-02). This is
**operator tooling, not gameplay** — the CLAUDE.md 1:1-vanilla-jar mandate does NOT bind here
(there is no vanilla equivalent). The binding constraints are: **`CGO_ENABLED=0` stays clean**
(pure-Go static binary) and **`go test -race` stays green** (the log bridge crosses goroutines).

The standard, current stack is the **charm v2 line under the new `charm.land/...` module path**:
`charm.land/bubbletea/v2` (the Model/Update/View runtime), `charm.land/bubbles/v2`
(`textinput` + `viewport` components), `charm.land/lipgloss/v2` (styling), plus
`golang.org/x/term` for pure-Go TTY detection. **[VERIFIED: all four build clean under
`CGO_ENABLED=0` with ZERO cgo files anywhere in the resolved dependency tree — `go list -deps`
over the import set returned no `HAS_CGO` matches this session.]** Structured logging is
**stdlib `log/slog`** (pure-Go, no new dep) — the project currently uses bare stdlib `log`
everywhere and has no structured logger yet.

The architecture is a **TTY fork in `main()`**: detect `term.IsTerminal(os.Stdout.Fd())`;
if TTY → build a `tea.Program`, install an `slog.Handler` whose `Handle` converts each record
into a custom `tea.Msg` and calls **`program.Send(msg)`** (goroutine-safe), and the TUI's
`textinput` Enter routes a typed line into the EXISTING command graph via a new
**console-sourced dispatch** (the seam needs a synthetic/console executor because the existing
`runCommand` requires a `*tickPlayer` issuer). If NOT a TTY (Docker/headless) → **never call
`tea.NewProgram`**; the same `slog.Handler` writes plain structured lines to stderr (today's
behavior preserved). The bridge MUST be **non-blocking / drop-on-full** so a slow or absent TUI
never stalls the tick or network goroutines.

**Primary recommendation:** Adopt the `charm.land/*/v2` stack; introduce `log/slog` as the
structured backbone behind a single custom `slog.Handler` that fans out to (a) `program.Send`
when a TTY, (b) plain stderr otherwise; gate `tea.NewProgram` behind `term.IsTerminal`; route
console commands through a new `runConsoleCommand` seam parallel to `runCommand`; and expand the
already-present `disconnectReason` plumbing (added in 17-09) into the full 5-category taxonomy.

## User Constraints

> No CONTEXT.md exists for this phase (`/gsd-research-phase` standalone, or pre-discuss). The
> constraints below are extracted verbatim from the phase brief + REQUIREMENTS.md + CLAUDE.md.

### Locked Decisions (from the phase brief + REQUIREMENTS.md)
- TUI-01: charmbracelet **bubbletea** + **bubbles** console — `textinput` command zone + scrolling
  log `viewport` in one screen; typed commands dispatch through Sulfur's EXISTING command system;
  **degrades gracefully when stdout is NOT a TTY** (headless/Docker → plain structured logging,
  no TUI).
- TUI-02: disconnect-reason logging — every player drop logs WHY (kick / timeout / protocol error /
  clean quit / login failure) with player identity + reason, surfaced in **BOTH** the TUI log
  stream and the structured logs.
- `CGO_ENABLED=0` MUST stay clean (pure-Go static binary). **NO cgo, including transitive.**
- The log bridge (server log events → bubbletea viewport) MUST be race-clean (`go test -race`)
  and MUST NOT block the tick/network goroutines if the TUI is slow or absent.
- Introduce `log/slog` (stdlib) as the structured backbone with a custom `slog.Handler`.

### Claude's Discretion
- Exact bridge buffering (bounded channel vs direct `program.Send` from `Handle`) + the drop policy
  (ring buffer / non-blocking send) — recommended below, but the planner may tune capacity.
- TUI layout details (input above vs below the viewport; lipgloss border/title styling).
- Whether the console executor is a synthetic `tickPlayer` or a dedicated console dispatch path
  (recommended: dedicated path — see Architecture Patterns).

### Deferred Ideas (OUT OF SCOPE)
- Anything in REQUIREMENTS.md "Out of Scope" (Folia per-region threading, plugin API, Bedrock).
- The signed `ClientboundPlayerChat` path (chat.go documents it as deferred).
- A full operator permission/ops-list model (the `playerHasPermission` hook stays all-true; console
  is implicitly operator).

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| TUI-01 | bubbletea+bubbles console: `textinput` + log `viewport`, dispatch through existing command system, degrade on non-TTY | Standard Stack (charm v2, CGO=0 verified); Architecture Patterns (Model composition, TTY fork, console dispatch seam); Code Examples |
| TUI-02 | Disconnect-reason logging: kick/timeout/protocol/clean-quit/login-failure + identity, in TUI + structured logs | Disconnect Taxonomy (every drop site file:line + reason category); the existing `Client.disconnectReason` plumbing (17-09) is the foundation |

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Terminal rendering / input loop | Operator console (NEW, own goroutine) | — | bubbletea owns the render loop; runs isolated from the tick |
| Log record → display | `slog.Handler` bridge (NEW) | Operator console | Handler is the single fan-out point; non-blocking into the TUI |
| TTY detection / degrade | `main()` startup | — | One decision at boot picks TUI vs plain-stderr |
| Command parse + execute | Existing command graph (`server/commands.go`, tick goroutine) | Operator console (input source) | Console is a NEW command *source*; execution stays on the tick (state-mutating) |
| Disconnect reason capture | Network/login/keepalive seams (`server/`) | `slog` log stream | Each drop site already exists; we tag + log the reason at each |
| Process shutdown | `main()` (ctx cancel) | Operator console (`tea.Quit` → cancel) | Ctrl-C / tea.Quit must stop the server too |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `charm.land/bubbletea/v2` | **v2.0.7** | TUI runtime: Model/Update/View, `Program.Send`, `tea.Quit`, `WindowSizeMsg` | The canonical Go TUI framework; v2 is GA. `Program.Send` is the goroutine-safe external-event injector the log bridge needs. **[VERIFIED: pkg.go.dev / go list -m -versions]** |
| `charm.land/bubbles/v2` | **v2.1.0** | `textinput` (command zone) + `viewport` (scrolling logs) | The blessed component set for bubbletea; do not hand-roll input/scroll. **[VERIFIED: resolved via `go mod tidy`]** |
| `charm.land/lipgloss/v2` | **v2.0.4** | Styling/layout (borders, join input+viewport, title bar) | Standard styling layer; `lipgloss.JoinVertical` composes the two panes. **[VERIFIED: go list -m -versions]** |
| `golang.org/x/term` | **v0.44.0** | `term.IsTerminal(int(fd)) bool` — pure-Go TTY detection | Canonical pure-Go isatty (no cgo). Already adjacent to existing `golang.org/x/sync` dep. **[VERIFIED]** |
| `log/slog` | stdlib (Go 1.25) | Structured logging backbone + custom `slog.Handler` | Stdlib, pure-Go, zero new dep. Replaces the bare `log` calls. **[VERIFIED: stdlib since Go 1.21]** |

> **MODULE PATH ALERT [VERIFIED this session]:** the charm v2 line moved to the
> **`charm.land/...`** vanity path. `github.com/charmbracelet/bubbletea` is the **v1** line
> (latest v1.3.10). Do NOT mix: v2 imports are `charm.land/bubbletea/v2`,
> `charm.land/bubbles/v2/textinput`, `charm.land/bubbles/v2/viewport`, `charm.land/lipgloss/v2`.
> Context7 docs for `/charmbracelet/bubbletea` + `/charmbracelet/bubbles` return the **v2 API**
> (the `UPGRADE_GUIDE_V2.md` examples). Training-data snippets using `tea.WithAltScreen()` as a
> `NewProgram` option or `View() string` are **v1 and WRONG for this stack** (see State of the Art).

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| charm v2 (`charm.land/.../v2`) | charm v1 (`github.com/charmbracelet/bubbletea` v1.3.10) | v1 is older API (`View() string`, imperative `WithAltScreen()`). v2 is GA and Context7's current docs. Recommend v2; v1 only if a v2 bug blocks. |
| `golang.org/x/term.IsTerminal` | `github.com/mattn/go-isatty` | go-isatty is fine but `x/term` is pure-Go, stdlib-adjacent, and bubbletea's own dep tree already uses it. Prefer x/term. |
| `log/slog` custom handler | `github.com/charmbracelet/log` | charm/log is nice but adds a dep + its own style; slog is stdlib, pure-Go, and the planner can format. Use slog. |

**Installation:**
```bash
go get charm.land/bubbletea/v2@v2.0.7
go get charm.land/bubbles/v2@v2.1.0
go get charm.land/lipgloss/v2@v2.0.4
go get golang.org/x/term@v0.44.0
# log/slog is stdlib — no get
```

**Version verification (run this session):**
- `charm.land/bubbletea/v2` → v2.0.7 (latest; v2.0.x GA line) [VERIFIED: go list -m -versions]
- `charm.land/bubbles/v2` → v2.1.0 [VERIFIED: resolved by go mod tidy]
- `charm.land/lipgloss/v2` → v2.0.4 [VERIFIED: go list -m -versions]
- `golang.org/x/term` → v0.44.0 [VERIFIED: go list -m -versions]
- **CGO=0 build of all four together → BUILD OK; cgo dep scan → empty [VERIFIED]**

## Architecture Patterns

### System Architecture Diagram

```
                        ┌─────────────────────────────────────────────┐
   server goroutines    │  tick loop  │  keepalive  │  net accept/read │
   (tick/net/keepalive) └──────┬───────────┬──────────────┬───────────┘
                               │  slog.Info/Warn (records) │
                               ▼           ▼               ▼
                        ┌──────────────────────────────────────┐
                        │   fanoutHandler (custom slog.Handler) │
                        │   ── single fan-out point ──          │
                        └───────────────┬──────────────────────┘
                            TTY?  ──────┴────── not TTY?
                              │                    │
                  program.Send(logLineMsg)   plain text → os.Stderr
                  (NON-BLOCKING, drop-on-full)   (today's behavior)
                              │
                              ▼
                 ┌────────────────────────────┐
                 │  tea.Program (own goroutine)│
                 │  Model{ viewport, textinput}│
                 │  Update: logLineMsg→append  │
                 │          KeyEnter→dispatch  │
                 │          WindowSizeMsg→resize│
                 └─────────────┬───────────────┘
                   Enter line  │  (console command)
                               ▼
                 runConsoleCommand(line)  ──► cmdGraph.Execute(consoleCtx, line)
                 (NEW seam; sender = console, no tickPlayer)   [server/commands.go]
                               │
                               ▼
                  command handler mutates state ON THE TICK
                  (route the execute onto the tick goroutine — see Pitfalls)
```

Entry points: server goroutines emit `slog` records; the operator types into `textinput`.
Processing: the custom handler is the ONLY fan-out; the TTY fork picks `program.Send` vs stderr;
console Enter feeds the existing command graph. Boundary: the tick goroutine remains the sole
mutator of game state — the console dispatch must hop onto it (see Common Pitfalls).

### Recommended Project Structure
```
cmd/sulfur/
├── main.go            # TTY fork: build slog handler; if TTY → start tea.Program; else plain stderr
server/
├── tui/               # NEW package (operator tooling, isolated from gameplay)
│   ├── model.go       # bubbletea Model: viewport + textinput + resize + logLineMsg
│   ├── handler.go     # custom slog.Handler → program.Send (TTY) / stderr (non-TTY)
│   └── console.go     # console command source adapter (Enter line → server dispatch)
├── commands.go        # EXISTING graph; add runConsoleCommand seam here
├── client.go          # EXISTING disconnectReason plumbing (17-09) — extend taxonomy
├── server.go          # EXISTING login/config drop sites — tag reasons
├── keepalive.go       # EXISTING timeout kick — already tags "timeout"
└── gameplay_tick.go   # EXISTING join/leave log — convert to slog + full taxonomy
```
> **Why a new `server/tui` package:** keeps the charm imports + render loop OUT of the gameplay
> packages so the 1:1-mandated code is untouched and the TUI is independently testable.

### Pattern 1: bubbletea v2 Model — textinput + viewport in one screen
**What:** A single `Model` holds a `viewport.Model` (log scroll) + a `textinput.Model` (command
line). `Update` handles three message classes: `logLineMsg` (external, appended to the viewport),
`tea.KeyPressMsg` Enter (read+reset the input, dispatch the line), and `tea.WindowSizeMsg`
(resize both panes). `View()` returns a `tea.View` joining the two with lipgloss.
**When to use:** the TUI-01 console.
**Key v2 API facts [CITED: charm.land UPGRADE_GUIDE_V2.md via Context7]:**
- `View()` returns **`tea.View`** (not `string`): `v := tea.NewView(content); v.AltScreen = true`.
  Terminal flags (AltScreen, MouseMode) are **declarative fields on `tea.View`**, NOT `NewProgram`
  options.
- Key handling uses **`tea.KeyPressMsg`** (not v1 `tea.KeyMsg`); `msg.String()` gives `"enter"`,
  `"ctrl+c"`, etc.
- `viewport` constructor is **functional-option / setter** style: `viewport.New(viewport.WithWidth(w),
  viewport.WithHeight(h))` or `viewport.New()` then `vp.SetWidth(w)/vp.SetHeight(h)`. Dimensions are
  **methods** now (`vp.Width()`, `vp.SetWidth()`), not fields.
- `textinput` width is `ti.SetWidth(n)` (method), value via `ti.Value()` / `ti.SetValue("")`.

### Pattern 2: The log bridge — custom slog.Handler → Program.Send (NON-BLOCKING)
**What:** A `slog.Handler` whose `Handle(ctx, record)` formats the record and, in TUI mode,
sends it to the program; in non-TTY mode, writes plain text to stderr. **`Program.Send` is
safe to call from any goroutine** — bubbletea's internal code itself does `go p.Send(...)` from
worker goroutines [CITED: bubbletea exec.go via Context7], and the framework is built on an
internal message queue. The drop policy: the handler must NEVER block a server goroutine, so it
either (a) calls `program.Send` directly (Send enqueues internally; if you want a hard guarantee
against any blocking, front it with a buffered channel + a single forwarder goroutine that does
`select { case <-bufferedSend: default: /* drop */ }`), or (b) bounds itself with a non-blocking
channel mirroring the existing `Client.Send` drop-and-continue discipline.
**Recommended:** a bounded `chan logLineMsg` (cap ~1024, matching the project's `inboundCap`
convention) with a non-blocking send (`select { case ch <- msg: default: drop++ }`) drained by
ONE forwarder goroutine calling `program.Send`. This guarantees the tick/net goroutines never
block on a slow TUI and is trivially `-race` clean (one writer pattern per channel, like the
existing `ChannelQueue`).
**Anti-pattern:** calling `program.Send` directly from `Handle` with no bound *if* Send could
ever block under a stalled render — the buffered-channel-with-drop removes that risk entirely.

### Pattern 3: TTY detection + graceful degrade (the Docker path)
**What:** In `main()`, BEFORE building any program:
```go
isTTY := term.IsTerminal(int(os.Stdout.Fd()))  // pure-Go [CITED: golang.org/x/term]
```
If `isTTY` → build the `tea.Program` + TUI handler. If NOT → **do not call `tea.NewProgram` at
all**; install the same handler in plain-stderr mode. **Do not rely on bubbletea to self-degrade**
— [CITED: pkg.go.dev/charm.land/bubbletea/v2] `Program.Run()` does NOT auto-error on a non-TTY;
it just produces no usable output. The guard is OURS. This preserves today's exact
Docker/headless behavior (plain log lines to stderr).
**When to use:** the TUI-01 degrade requirement; the canonical CI/Docker run.

### Pattern 4: Console → existing-command-system dispatch seam
**What:** The TUI's Enter must route into the SAME graph the chat path uses. The existing
entrypoint is `cmdGraph.Execute(ctx, cmd)` via `executeCommand` (server/commands.go:180) and
`(*TickLoop).runCommand` (server/commands.go:217). **Seam gap:** `runCommand` takes a
`*tickPlayer` issuer and replies via `sendSystemChat` (player-bound). A console source has **no
player**. The handlers already tolerate a missing issuer: `executorFrom(ctx)` returns
`ok=false` when no executor is installed (commands.go:84) and the `/tp` handler no-ops in that
case (commands.go:146 "no issuer (console/test path)"). So the console path is:
add `runConsoleCommand(line string)` that builds a context with the permission resolver
(grant-all for console — console is operator) and **no executor**, then calls `executeCommand`,
sending the reply to the LOG STREAM (slog) instead of a player. **This must run on the tick
goroutine** because handlers mutate authoritative state (commands.go:207 TICK-05). Route the
console line as a tick message (a small `chan string` console-command channel drained in the
tick loop, mirroring the `register`/`unregister` message pattern) — do NOT call `Execute` from
the TUI goroutine directly.
**When to use:** TUI-01 "typed commands dispatch through the EXISTING command system."

### Pattern 5: Startup + shutdown model (Listen vs Program)
**What:** `main()` currently ends `log.Fatal(srv.Listen(*addr))` (cmd/sulfur/main.go:266) —
a blocking call on the main goroutine. To add the TUI without changing `Listen` semantics:
run **`srv.Listen` on a goroutine** and **`program.Run()` on main** (the TUI owns the terminal;
`tea.Quit` / Ctrl-C returns from `Run`, which then cancels the shared `ctx` that already drives
every server goroutine — `ctx, cancel := context.WithCancel` exists at main.go:129). In non-TTY
mode, keep `Listen` blocking on main (today's behavior). `tea.NewProgram(model,
tea.WithContext(ctx))` ties program lifetime to the same ctx. On `Run` return → `cancel()` →
all `go tick.Run(ctx,...)`, `keep.Run(ctx)`, `worker.Run(ctx)` unwind.
**When to use:** the TUI-01 startup wiring.

### Anti-Patterns to Avoid
- **Calling `cmdGraph.Execute` from the TUI goroutine** — it mutates tick-owned state. Route via a
  tick message channel (Pitfall 1).
- **Relying on bubbletea to degrade on non-TTY** — it won't; guard with `term.IsTerminal` (Pitfall 3).
- **Mixing v1 (`github.com/charmbracelet/...`) and v2 (`charm.land/.../v2`) imports** — they are
  different module paths and incompatible types (Pitfall 5).
- **An unbounded log→TUI channel** — a slow render would grow memory; bound it + drop (Pitfall 1).
- **Losing the plain-stderr path** — if `slog` only fans to the TUI, Docker logs go dark (Pitfall 4).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Terminal rendering / cursor / scroll region | A custom ANSI renderer | `charm.land/bubbletea/v2` + `bubbles/v2/viewport` | Render loop, resize, alt-screen, framerate are solved + race-tested |
| Command-line input editing | A readline/line-editor | `bubbles/v2/textinput` | Cursor, history, width, validation handled |
| TTY / isatty detection | A syscall/ioctl `TIOCGWINSZ` probe | `golang.org/x/term.IsTerminal` | Pure-Go, cross-platform (Windows/Linux), no cgo |
| Structured logging | A custom leveled logger | stdlib `log/slog` | Levels, attrs, handler interface, pure-Go, zero dep |
| Command parsing/dispatch | A new parser | The EXISTING `server/command` Brigadier graph | Already on disk + tested; console is just a new *source* |
| Cross-goroutine UI event injection | Shared mutex + manual redraw | `tea.Program.Send` | Goroutine-safe message queue; the framework's intended hook |

**Key insight:** every piece of this phase has a blessed, pure-Go, race-tested solution. The
*only* net-new code is glue: the `slog.Handler`, the Model composition, the TTY fork, and the
console-command tick message.

## Disconnect Taxonomy (TUI-02)

Foundation already in place [VERIFIED: code read this session]: `Client.disconnectReason`
(atomic.Pointer[string], client.go:64), `SetDisconnectReason` (CAS, first-writer-wins,
client.go:73), `DisconnectReason()` (defaults `"quit"`, client.go:79), `RemoteAddr()`
(client.go:88). The leave log already reads it (gameplay_tick.go:423). Phase 19 EXPANDS this
from the 2-state minimum (`timeout` / `quit`) to the full 5-category taxonomy and tags each drop
site, then surfaces every line through `slog` (→ TUI + stderr).

| # | Reason category | Drop site (file:line) | How it fires | Identity available | Reason detail to log |
|---|-----------------|------------------------|--------------|--------------------|----------------------|
| 1 | **login failure** | `server/server.go:107-119` (`AcceptLogin` err → `LoginFailErr`) | login handler returns error (auth fail, EOF mid-login — Phase 18 path) | remote addr only (no name/uuid yet) | `login_failure` + the `LoginFailErr.reason` / wrapped err |
| 2 | **protocol mismatch** (a login-stage kick) | `server/server.go:98-105` (`protocol != ProtocolVersion`) | client speaks wrong protocol; explicit `Disconnect(StateLogin,…)` | remote addr + protocol number | `protocol_mismatch` + got vs want |
| 3 | **config failure** | `server/server.go:121-133` (`AcceptConfig` err → `ConfigFailErr`) | config sequence error; explicit Config `Disconnect` | remote addr (name known by login but not threaded to this log) | `config_failure` + `ConfigFailErr.reason` / err |
| 4 | **keep-alive timeout** | `server/keepalive.go:140-147` (`kickPlayer`) → `keepAliveClient.SendDisconnect` (gameplay_tick.go:158) | 30s no keep-alive echo; `SetDisconnectReason("timeout")` already set here | name + uuid (in Play) | `timeout` (already tagged; surface via slog) |
| 5 | **explicit kick** (server-full + future ops kick) | `server/playerlist.go:38` (`SendDisconnect(server_full)`) + any future `Disconnect(StatePlay,…)` | server full at join, or operator/console kick | name/uuid if past login, else addr | `kicked` + the kick reason message |
| 6 | **protocol/decode error** (in Play) | `server/client.go:137-153` (`readLoop` → `c.conn.ReadPacket` err → `c.Close()`) | malformed frame or read error tears the client down; reason currently UNSET → defaults `"quit"` | name + uuid (Play) | NEW: set `protocol_error` before Close on a decode error vs a clean EOF |
| 7 | **write failure / backpressure drop** | `server/client.go:119-131` (`writeLoop` write err → `Close`) + `Send` queue-full → `Close` (client.go:169) | socket write error, or outbound queue full (slow client) | name + uuid (Play) | NEW: set `write_error` / `backpressure` before Close |
| 8 | **clean quit** (client closed / EOF) | `server/gameplay_tick.go:414-423` (`<-c.quit` unblocks; `DisconnectReason()` default) | client closes socket; read EOF; nothing tagged → `"quit"` | name + uuid (Play) | `quit` (the default; keep) |

**Taxonomy wiring tasks the planner will enumerate:**
1. Distinguish **#6 protocol_error** from **#8 clean_quit** in `readLoop`: a clean EOF
   (`errors.Is(err, io.EOF)` / `net.ErrClosed` after our own Close) → leave default `quit`;
   any other decode/read error → `SetDisconnectReason("protocol_error")` before `Close()`.
2. Tag **#7** in `writeLoop`/`Send` (`write_error`, `backpressure`).
3. Thread **name/uuid into the login/config drop logs (#1-3)** where known, else `RemoteAddr()`.
4. Convert the join/leave/connection `log.Printf` lines (gameplay_tick.go:408,423; server.go:84,
   102,117,131) to `slog` with structured attrs (`name`, `uuid`, `addr`, `reason`, `protocol`)
   so they appear in BOTH the TUI stream and stderr.
5. Map the reason token → a one-line human string for the TUI (kick / timeout / protocol error /
   clean quit / login failure), matching the TUI-02 wording.

> **Robustness note (carried from STATE.md Deferred Items):** `KeepAlive.removePlayer`
> (keepalive.go:88) derefs `listIndex[c]` and the deferred-item warns it could panic on a
> double-leave (a player the keep-alive already kicked on timeout, then `ClientLeft` again).
> Phase 19 touches the timeout/disconnect path — harden this double-leave here (the deferred item
> explicitly says "harden in the phase that adds real timeout-driven disconnects").

## Common Pitfalls

### Pitfall 1: Blocking the tick/network on a slow or absent TUI
**What goes wrong:** the `slog.Handler` calls into the TUI synchronously; if the render stalls or
the program is mid-resize, the emitting goroutine (tick/net/keepalive) blocks → tick stutter.
**Why it happens:** treating `program.Send` as a fast path and emitting from hot goroutines.
**How to avoid:** front the bridge with a bounded channel + non-blocking send + drop-on-full
(mirror the existing `ChannelQueue` / `Client.Send` drop discipline), drained by ONE forwarder
goroutine. The tick NEVER waits on the TUI.
**Warning signs:** MSPT spikes correlated with log bursts; `-race` clean but TPS dips under
heavy logging.

### Pitfall 2: Racing on the log bridge
**What goes wrong:** multiple server goroutines write the same buffer / call into Model fields.
**Why it happens:** sharing mutable state between the emitter and the bubbletea Update.
**How to avoid:** the ONLY cross-goroutine handoff is `program.Send(msg)` (or the bounded
channel). Model state is touched ONLY inside `Update` (single-threaded by bubbletea's design).
Never let the handler reach into the Model. Run the full `server/...` + new `tui` package under
Docker `go test -race` (the project's standard gate).

### Pitfall 3: `tea.NewProgram` under no-TTY (Docker/CI)
**What goes wrong:** building a program in a headless container produces garbage/no output or
swallows logs; CI logs go dark.
**Why it happens:** assuming bubbletea self-degrades. [CITED] `Run()` does NOT auto-error on a
non-TTY.
**How to avoid:** guard with `term.IsTerminal(int(os.Stdout.Fd()))` BEFORE `tea.NewProgram`; in
non-TTY mode never construct a program — plain stderr only.
**Warning signs:** empty/blank Docker logs; the server "runs" but produces no console output.

### Pitfall 4: Losing the plain-log path in Docker (double-degrade)
**What goes wrong:** the `slog.Handler` only knows how to `program.Send`, so non-TTY mode logs
nothing.
**How to avoid:** the handler has TWO modes baked in from the start (TTY→Send, non-TTY→stderr);
the non-TTY mode is the default and preserves today's behavior. Test both modes.

### Pitfall 5: Mixing charm v1 and v2 module paths
**What goes wrong:** `github.com/charmbracelet/bubbletea` (v1) and `charm.land/bubbletea/v2`
coexist in the graph; types don't match; `View() string` vs `View() tea.View` compile errors.
**How to avoid:** use ONLY `charm.land/.../v2` imports. Ignore any training-data snippet using
`tea.WithAltScreen()` as a program option or `View() string` — those are v1.
**Warning signs:** "cannot use model (type X) as tea.Model"; `WithAltScreen undefined`.

### Pitfall 6: Double-logging the leave / connect lines
**What goes wrong:** converting `log.Printf` → `slog` but leaving the old `log.Printf` in place,
or both the `Server.Logger` and the new slog handler fire.
**How to avoid:** replace `log.Default()`/`Server.Logger` with the slog-backed logger as the
single sink. The existing `srv.Logger.Printf` (server.go) and `log.Printf` (gameplay_tick.go,
main.go) calls must be migrated, not duplicated. `Server.Logger` is a `*log.Logger` field
(server.go:48) — bridge it (set its output to an `slog`-backed writer, or replace the call
sites). Pick ONE.

### Pitfall 7: Console command runs off the tick → data race on game state
**What goes wrong:** the TUI goroutine calls `cmdGraph.Execute` directly; a `/tp`-style handler
mutates tick-owned state from the wrong goroutine → race.
**How to avoid:** the console line crosses to the tick via a message channel (like
`register`/`unregister`); `Execute` runs on the tick goroutine (commands.go:207 TICK-05).
**Warning signs:** `-race` flags writes to `tickPlayer`/`TickLoop` fields from a `tui`-package
goroutine.

## Code Examples

> Skeletons against the CURRENT v2 API [CITED: charm.land UPGRADE_GUIDE_V2.md + pkg.go.dev via
> Context7/WebFetch this session]. Not 2022-era v1.

### Example 1: bubbletea v2 Model — textinput + viewport + resize + external log msg
```go
// server/tui/model.go
package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// logLineMsg is the external message the slog bridge Sends into the program.
type logLineMsg string

// commandFn routes a typed console line to the server (a tick-message send — see console.go).
type commandFn func(line string)

type Model struct {
	vp      viewport.Model
	input   textinput.Model
	lines   []string
	dispatch commandFn
	ready   bool
}

func New(dispatch commandFn) Model {
	ti := textinput.New()
	ti.Placeholder = "type a command…"
	ti.Focus()
	return Model{input: ti, dispatch: dispatch}
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		inputH := 1
		if !m.ready {
			m.vp = viewport.New(
				viewport.WithWidth(msg.Width),
				viewport.WithHeight(msg.Height-inputH),
			)
			m.ready = true
		} else {
			m.vp.SetWidth(msg.Width)
			m.vp.SetHeight(msg.Height - inputH)
		}
		m.input.SetWidth(msg.Width)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			line := m.input.Value()
			m.input.SetValue("")
			if line != "" && m.dispatch != nil {
				m.dispatch(line) // → tick message; reply comes back as a logLineMsg
			}
		}
	case logLineMsg:
		m.lines = append(m.lines, string(msg))
		m.vp.SetContent(lipgloss.JoinVertical(lipgloss.Left, m.lines...))
		m.vp.GotoBottom()
	}
	var c tea.Cmd
	m.input, c = m.input.Update(msg); cmds = append(cmds, c)
	m.vp, c = m.vp.Update(msg); cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	body := lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.input.View())
	v := tea.NewView(body)
	v.AltScreen = true // declarative in v2 (NOT a NewProgram option)
	return v
}
```

### Example 2: the slog.Handler bridge — Send (TTY) / stderr (non-TTY), non-blocking
```go
// server/tui/handler.go
package tui

import (
	"context"
	"log/slog"
	tea "charm.land/bubbletea/v2"
)

// fanoutHandler formats records and (a) Sends into the program when prog != nil,
// else (b) delegates to a plain text handler (stderr). Non-blocking into the TUI.
type fanoutHandler struct {
	slog.Handler            // embedded text handler → stderr (the always-on path)
	prog *tea.Program       // nil in non-TTY mode
	ch   chan logLineMsg    // bounded; drained by one forwarder goroutine
}

func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	// 1) always keep the structured/stderr path (Pitfall 4).
	_ = h.Handler.Handle(ctx, r)
	// 2) if a TUI is up, push a formatted line NON-BLOCKING (Pitfall 1).
	if h.prog != nil {
		line := formatLine(r) // level + time + msg + attrs, one string
		select {
		case h.ch <- logLineMsg(line):
		default: // viewport behind → drop (never block the emitting goroutine)
		}
	}
	return nil
}

// forwarder: the ONLY goroutine that calls prog.Send (one-writer discipline).
func (h *fanoutHandler) forwarder() {
	for msg := range h.ch {
		h.prog.Send(msg) // goroutine-safe [CITED: bubbletea internal `go p.Send`]
	}
}
```

### Example 3: the TTY fork in main()
```go
// cmd/sulfur/main.go (replacing the tail log.Fatal(srv.Listen(...)))
import "golang.org/x/term"

isTTY := term.IsTerminal(int(os.Stdout.Fd())) // pure-Go [CITED: golang.org/x/term]

if isTTY {
	model := tui.New(func(line string) { gp.EnqueueConsoleCommand(line) }) // tick message
	prog := tea.NewProgram(model, tea.WithContext(ctx))
	handler := tui.NewHandler(prog)            // TTY mode: Send + stderr
	slog.SetDefault(slog.New(handler))
	go func() { _ = srv.Listen(*addr) }()      // Listen off the main goroutine
	if _, err := prog.Run(); err != nil {      // TUI owns main; returns on tea.Quit/Ctrl-C
		slog.Error("tui exited", "err", err)
	}
	cancel()                                   // tea.Quit → stop every server goroutine
} else {
	handler := tui.NewHandler(nil)             // headless: plain stderr (today's behavior)
	slog.SetDefault(slog.New(handler))
	log.Fatal(srv.Listen(*addr))               // blocking on main, unchanged
}
```

### Example 4: console command dispatch (the seam — runs on the tick)
```go
// server/commands.go (new seam, parallel to runCommand)

// EnqueueConsoleCommand is called from the TUI goroutine; it hands the line to the tick
// via a bounded message channel (mirrors register/unregister). NEVER executes here.
func (g *gameTick) EnqueueConsoleCommand(line string) {
	select {
	case g.consoleCmd <- line: // drained in the tick loop
	default:                   // tick busy → drop (console is best-effort)
	}
}

// runConsoleCommand executes on the TICK goroutine (drained from consoleCmd). No tickPlayer
// issuer: install the permission resolver (console = operator, grant-all) and NO executor,
// so executorFrom(ctx) returns ok=false and issuer-acting commands no-op (commands.go:84,146).
// The reply goes to the LOG STREAM (slog) instead of a player's SystemChat.
func (t *TickLoop) runConsoleCommand(line string) {
	if len(line) == 0 || len(line) > maxCommandLen {
		return
	}
	ctx := withPermissionResolver(context.Background(), func(string) bool { return true })
	if err := executeCommand(ctx, line); err != nil {
		slog.Warn("console command failed", "cmd", line, "err", err)
	} else {
		slog.Info("console command", "cmd", line)
	}
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `github.com/charmbracelet/bubbletea` (v1) | `charm.land/bubbletea/v2` | v2 GA (module moved to `charm.land`) | Different import path + API |
| `func View() string` | `func View() tea.View` (`tea.NewView`) | v2 | Compile-breaking signature change |
| `tea.NewProgram(m, tea.WithAltScreen())` | `v.AltScreen = true` in `View()` (declarative) | v2 | Terminal flags moved off NewProgram |
| `tea.KeyMsg` | `tea.KeyPressMsg` (+ `tea.KeyReleaseMsg`) | v2 | Key handling type changed |
| `viewport.New(w, h)`, `vp.Width` field | `viewport.New(viewport.WithWidth(w),…)`, `vp.SetWidth()/vp.Width()` | v2 | Constructor + field→method |
| stdlib `log` (`log.Printf`/`log.Fatal`) | `log/slog` structured handler | this phase | Structured attrs, fan-out handler |

**Deprecated/outdated for THIS stack:**
- Any v1 bubbletea snippet (the bulk of training data). Use the v2 `charm.land` API above.
- `github.com/mattn/go-isatty` is not wrong but unnecessary — `golang.org/x/term` is pure-Go.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `Program.Send` is fully goroutine-safe to call from server goroutines | Pattern 2 / Code Ex 2 | LOW — pkg.go.dev WebFetch did not state it verbatim, but bubbletea's own exec.go does `go p.Send(...)` [CITED] and the framework is queue-based; the recommended bounded-channel + single-forwarder design makes us robust either way. |
| A2 | `executorFrom` returning ok=false cleanly no-ops issuer-acting commands for a console source | Pattern 4 / Code Ex 4 | LOW — verified by reading commands.go:84,146 ("console/test path"); the existing code anticipates this. |
| A3 | Routing the console command as a tick message is the right concurrency model | Pattern 4 / Pitfall 7 | LOW — matches the project's established register/unregister message discipline (TICK-05). |

## Open Questions

1. **Input above or below the viewport?**
   - Known: vanilla server consoles + most TUI consoles put the input at the BOTTOM.
   - Unclear: cosmetic only.
   - Recommendation: input at the bottom (`JoinVertical(viewport, input)`); discretion.
2. **Bridge: direct `program.Send` from `Handle` vs bounded-channel+forwarder?**
   - Known: both work; Send is safe.
   - Unclear: whether Send can ever block under a stalled render.
   - Recommendation: bounded-channel + single forwarder + drop-on-full (zero blocking risk;
     `-race` trivial). Planner may simplify to direct Send if profiling shows it's free.
3. **Migrate `Server.Logger` (*log.Logger) or replace call sites?**
   - Known: `Server.Logger` is a `*log.Logger` field used in server.go.
   - Recommendation: point its output at an `slog`-backed `io.Writer` (`slog.NewLogLogger` or a
     small adapter) so the existing `srv.Logger.Printf` lines flow through the one slog sink —
     avoids touching every call site while keeping a single fan-out (Pitfall 6).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | build | ✓ | 1.25.0 (go.mod) | — |
| `charm.land/bubbletea/v2` | TUI-01 | ✓ (go get) | v2.0.7 | — |
| `charm.land/bubbles/v2` | TUI-01 | ✓ | v2.1.0 | — |
| `charm.land/lipgloss/v2` | TUI-01 | ✓ | v2.0.4 | — |
| `golang.org/x/term` | TTY detect | ✓ | v0.44.0 | stdlib syscall (worse) |
| `log/slog` | TUI-02 / logging | ✓ (stdlib) | Go 1.25 | — |
| Docker `golang:1.26` (race gate) | CGO=0 `-race` | ✓ (project standard) | — | — (host has no C compiler; race runs in Docker per STATE.md) |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** none (all resolve + build CGO=0 this session).

## Validation Architecture

> `.planning/config.json` not read for an explicit `nyquist_validation:false`; included by default.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./server/... ./server/tui/...` |
| Full suite command | Docker `golang:1.26` `go test -race ./server/... ./server/tui/...` (host CGO=0, no C compiler — race runs in Docker per STATE.md) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| TUI-01 | Model.Update appends a logLineMsg to the viewport | unit | `go test ./server/tui -run TestModelAppendsLog` | ❌ Wave 0 |
| TUI-01 | Enter routes the input line to the dispatch fn | unit | `go test ./server/tui -run TestEnterDispatches` | ❌ Wave 0 |
| TUI-01 | non-TTY handler writes plain stderr, no program | unit | `go test ./server/tui -run TestHeadlessStderr` | ❌ Wave 0 |
| TUI-01 | bridge drops on full, never blocks | unit + race | `go test -race ./server/tui -run TestBridgeNonBlocking` | ❌ Wave 0 |
| TUI-01 | console command executes via the existing graph | unit | `go test ./server -run TestRunConsoleCommand` | ❌ Wave 0 |
| TUI-02 | each drop category sets the right reason token | unit | `go test ./server -run TestDisconnectReason` (extend existing client_test.go) | partial (client_test.go exists) |
| TUI-02 | protocol_error vs clean_quit distinguished in readLoop | unit | `go test ./server -run TestReadLoopReason` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./server/tui/...` (+ the touched server test)
- **Per wave merge:** `go test ./server/... ./server/tui/...`
- **Phase gate:** Docker `golang:1.26` `go test -race ./server/... ./server/tui/...` green
  (the project's standard concurrency gate), plus a real-operator visual pass (autonomous:false)
  confirming the TUI renders + a disconnect line appears.

### Wave 0 Gaps
- [ ] `server/tui/model_test.go` — Model append + Enter dispatch + resize (TUI-01)
- [ ] `server/tui/handler_test.go` — TTY vs non-TTY fan-out + non-blocking drop (TUI-01)
- [ ] `server/tui/console_test.go` (or extend `server/commands_test.go`) — `runConsoleCommand`
      executes via the existing graph, no issuer (TUI-01)
- [ ] Extend `server/client_test.go` — full reason taxonomy (protocol_error / write_error /
      login_failure / clean quit) (TUI-02)
- [ ] Framework install: `go get` the 4 charm/x-term deps (none present in go.mod yet)

## Security Domain

> `security_enforcement` not explicitly disabled; included. This is operator tooling, so the
> surface is small but non-trivial (a console executes commands; logs carry remote input).

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | console is local-stdin only; no network auth on the TUI |
| V3 Session Management | no | — |
| V4 Access Control | yes | console = implicit operator; the `permissionResolver` already gates command nodes (commands.go) — console grants all by design (it IS the operator) |
| V5 Input Validation | yes | console line bounded by `maxCommandLen` (256, commands.go:33); log lines carry remote addr/name — must not be interpreted as control sequences in the TUI |
| V6 Cryptography | no | — |

### Known Threat Patterns for this stack
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Log/terminal injection (a hostile player name/addr containing ANSI escapes corrupts the TUI) | Tampering | Sanitize/escape untrusted strings (player name, remote addr) before they reach the viewport; lipgloss/bubbletea render text, but strip control bytes from attacker-controlled fields |
| Unbounded console input | DoS | `maxCommandLen` cap already enforced before Execute (commands.go:222) — reuse it in `runConsoleCommand` |
| Log flood blocks the tick | DoS | bounded bridge channel + drop-on-full (Pitfall 1) — the emitting goroutine never blocks |
| Console runs privileged command off-tick | Tampering (race) | route via tick message; Execute on the tick goroutine (Pitfall 7) |

## Sources

### Primary (HIGH confidence)
- Context7 `/charmbracelet/bubbletea` — UPGRADE_GUIDE_V2.md (v2 Program/View/KeyPressMsg/WindowSizeMsg), exec.go (`go p.Send`), screen.go (WindowSizeMsg)
- Context7 `/charmbracelet/bubbles` — UPGRADE_GUIDE_V2.md (import paths `charm.land/bubbles/v2`, viewport/textinput constructor + setter API)
- `go list -m -versions` (this session) — bubbletea v2.0.7, bubbles v2.1.0, lipgloss v2.0.4, x/term v0.44.0, bubbletea v1 latest v1.3.10
- **CGO=0 build probe (this session)** — all four v2 deps build under `CGO_ENABLED=0`; `go list -deps` cgo scan returned EMPTY (no cgo in the tree)
- Codebase read (this session) — server.go, client.go, disconnect.go, keepalive.go, gameplay_tick.go, commands.go, chat.go, cmd/sulfur/main.go, go.mod, REQUIREMENTS.md, STATE.md (every file:line in the taxonomy verified)

### Secondary (MEDIUM confidence)
- WebFetch pkg.go.dev/charm.land/bubbletea/v2 — Program.Send / Run / Wait / Kill / WithContext / WithoutSignalHandler signatures + the non-TTY-does-not-auto-error behavior (doc did not state Send's thread-safety verbatim — corroborated by the exec.go primary source)

### Tertiary (LOW confidence)
- none

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions + CGO=0 + cgo-free dep tree all VERIFIED by build this session
- Architecture (bridge/TTY/console seam): HIGH — v2 API CITED via Context7; seams read in-code
- Disconnect taxonomy: HIGH — every drop site read at file:line; foundation already in code (17-09)
- Program.Send thread-safety: MEDIUM-HIGH — primary-source corroborated; bounded-channel design de-risks it

**Research date:** 2026-06-26
**Valid until:** 2026-07-26 (charm v2 is GA but the `charm.land` line iterates — re-check versions if >30 days)
