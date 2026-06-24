---
phase: 07-ai-pathfinding-commands-chat
plan: 04
subsystem: commands
tags: [command-dispatch, brigadier, command-graph, ClientboundCommands, ServerboundChatCommand, tick, permission-gate]

# Dependency graph
requires:
  - phase: 03-tick-loop-networking
    provides: "the tick goroutine + dispatch switch (single-owner TICK-05) and the bounded outbound Client.Send queue"
  - phase: 04-world-chunks-streaming
    provides: "the AcceptPlayer Play bootstrap (Login -> ... -> SetDefaultSpawnPosition) the ClientboundCommands send appends to"
  - phase: 06-entities-physics-interaction
    provides: "the inline-resolve dispatch pattern (ServerboundClientCommand) the command case mirrors, and the captureClient/drainPackets/countID test helpers"
provides:
  - "server-side command dispatch (CMD-01): the fork command.Graph built once + sent per-connection at join as ClientboundCommands (the client tab-completes /say, /me)"
  - "ServerboundChatCommand decoded + routed to Graph.Execute on the tick goroutine (defensive Scan, length bound, permission gate)"
  - "a context-threaded permission hook (permissionGated) every future privileged command gates on by construction"
  - "a commandClientAdapter bridging server.*Client to the fork command.Client interface"
affects: [07-05-chat, 07-06-capture-diff]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Reuse the fork command.Graph (NewGraph + Literal/Argument builders + Execute/WriteTo/ClientJoin) — do NOT hand-roll a Brigadier parser"
    - "On-tick command resolve: dispatch routes ServerboundChatCommand INLINE on the tick goroutine (TICK-05), not via the subtick buffer"
    - "Context-threaded permission resolver: handlers are wrapped by permissionGated, the per-command resolver is installed on ctx before Execute (fail-closed if absent)"
    - "Swappable executor seam (executeCommand var) so a test captures the exact Execute string without mutating the read-only shared graph"

key-files:
  created:
    - server/commands.go
    - server/commands_test.go
  modified:
    - server/tick.go
    - server/gameplay_tick.go

key-decisions:
  - "Register each v1 literal with a GREEDY-string argument child (/say <message>, /me <action>) so the whole line parses — a bare literal rejects 'say hi' as extra text"
  - "ServerboundChatCommandSigned is a documented v1 no-op (offline mode sends the unsigned variant; the server never trusts client signatures)"
  - "The v1 reply path is a documented no-op (the command runs); the SystemChat reply helper lands in 07-05 and runCommand will reuse it"
  - "maxCommandLen=256 bounds the command string before Execute (the realistic vanilla command-input cap, well under the 32767 readUtf wire bound)"

patterns-established:
  - "Permission hook present from day one even when v1 grants every node — making a command operator-only is a resolver change, not a handler change"
  - "Every serverbound decode Scan-errors to a silent no-op, never panics (T-3-02)"

requirements-completed: [CMD-01]

# Metrics
duration: 25min
completed: 2026-06-24
---

# Phase 7 Plan 4: Command Dispatch (CMD-01) Summary

**Server-side command dispatch on the reused fork command.Graph: /say and /me built once + sent per-connection at join as ClientboundCommands, and ServerboundChatCommand decoded + routed to Graph.Execute on the tick goroutine with a defensive decode, a length bound, and a permission gate.**

## Performance

- **Duration:** ~25 min (completion of an interrupted run)
- **Completed:** 2026-06-24
- **Tasks:** 2 (both completed; the interrupted run had left commands.go/commands_test.go untracked and tick.go/gameplay_tick.go un-wired)
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments
- Completed the interrupted CMD-01 run: defined the missing `commandPlayer` test helper, wired the `ServerboundChatCommand` dispatch route in `tick.go`, and added the join-time `ClientboundCommands` send in `gameplay_tick.go`.
- Added the four Task-2 `TestChatCommand*` tests (routed / malformed / length-bounded / parse-error-no-panic) that the interrupted run never wrote.
- Fixed the v1 graph so `/say hi` actually parses end-to-end (greedy-string argument child) — the objective's interactive proof now dispatches.
- Full CMD-01 test set (8 tests) green; `go build ./...` + `go vet ./...` clean; zero new deps; Docker `-race` clean across all server packages.

## ServerboundChatCommand wire (javap-confirmed)

`javap -c net.minecraft.network.protocol.game.ServerboundChatCommandPacket` (from `temp/cache/26.2-inner.jar`) confirms a `java.lang.Record` with a SINGLE field `command:Ljava/lang/String;` and a `StreamCodec<FriendlyByteBuf, ServerboundChatCommandPacket>` — NO signing, salt, or timestamp. The serverbound decode is therefore exactly one `pk.String` (`var s pk.String; packet.Scan(&s)`), matching `runChatCommand`. `ServerboundChatCommand=7`, `ServerboundChatCommandSigned=8` in `data/packetid`.

## Task Commits

1. **Task 1+2 implementation** — `2dba5cf2` (feat) — commands.go (graph build + adapter + join send + the runChatCommand/runCommand route), tick.go (the dispatch case), gameplay_tick.go (the ClientJoin send)
2. **Task 1+2 tests** — `4cf32e6d` (test) — commands_test.go (8 CMD-01 tests + the commandPlayer helper)

_The interrupted run committed nothing for 07-04; both commits above are fresh._

## Files Created/Modified
- `server/commands.go` (created) — `buildCommandGraph` (NewGraph + `/say <message>` & `/me <action>` greedy-string literals, each permission-gated), `cmdGraph`, `commandClientAdapter`, `sendCommandGraph`, `runCommand`, `runChatCommand`, the `executeCommand` test seam, `maxCommandLen`, the context-threaded permission resolver.
- `server/commands_test.go` (created) — the 8 CMD-01 tests + the `commandPlayer` registration helper + the `withExecuteCommand` seam helper.
- `server/tick.go` (modified) — added `case packetid.ServerboundChatCommand` (inline on-tick resolve via `runChatCommand`, nil-player guarded) and a documented `ServerboundChatCommandSigned` no-op case to the dispatch switch.
- `server/gameplay_tick.go` (modified) — `cmdGraph.ClientJoin(commandClientAdapter{c})` in the AcceptPlayer bootstrap tail (after `sendPlayBootstrap`, before `register`).

## Decisions Made
- Greedy-string argument per literal so `/say hi` parses (see Deviations — this fixed a real bug in the interrupted run's graph/test).
- `ServerboundChatCommandSigned` routed explicitly as a v1 no-op (visible choice for the 07-06 capture-diff) rather than falling to `default`.
- A swappable `executeCommand` package-level var as the test seam, so `TestChatCommandRouted` captures the exact Execute string and `TestChatCommandParseErrorNoPanic` drives the real graph — neither mutates the read-only shared `cmdGraph`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] The v1 command literals could not parse a trailing message (`/say hi` -> "command contains extra text: hi")**
- **Found during:** Task 1 (graph build) — surfaced by the interrupted run's own `TestCommandGraphBuilds`, which asserts `g.Execute(ctx, "say hi")` succeeds and was FAILING.
- **Issue:** The interrupted run registered `/say` and `/me` as bare literals with NO child node. The fork dispatcher descends to the literal, finds no child to absorb "hi", and returns an error — so the objective's stated `/say hi` proof failed, and any real client command with arguments would have been a silent parse-error no-op.
- **Fix:** Registered each literal with a greedy-string (`StringParser(2)`) argument child (`/say <message>`, `/me <action>`) and moved the permission-gated handler onto the argument node; the literal itself is `Unhandle()`d. The whole line now parses and dispatches.
- **Files modified:** server/commands.go
- **Verification:** `TestCommandGraphBuilds` (which Executes both `say hi` and `me waves`) now passes; full CMD-01 set green; Docker -race clean.
- **Committed in:** `2dba5cf2` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 bug).
**Impact on plan:** The fix is required for CMD-01's interactive proof (`/say hi` must run) and was demanded by the plan's own existing test. No scope creep — still two harmless literals on the reused fork graph, zero new deps.

## Issues Encountered
- The interrupted run left `go vet ./server/...` failing on a single real missing symbol (`undefined: commandPlayer`). Distinguished from the documented stale-LSP phantom diagnostics by trusting the real compiler (`go build`/`go vet`): build was already clean, vet failed only on the genuinely-missing helper. Defining `commandPlayer` cleared it.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- CMD-01 complete: commands dispatch server-side and the graph reaches the client at join. Ready for 07-05 (chat), which adds the `ServerboundChat` dispatch case in Wave 2 (sequential to avoid a same-wave dispatch-switch conflict) and lands the SystemChat reply helper that `runCommand` will reuse for command output.
- The v1 command-output reply is a documented no-op until 07-05; the command still runs. 07-06's capture-diff should confirm the unmodified client sends the unsigned `ServerboundChatCommand` (the signed variant remains a no-op).

## Self-Check: PASSED

- `server/commands.go` — FOUND
- `server/commands_test.go` — FOUND
- `server/tick.go` (modified, contains ServerboundChatCommand case) — FOUND
- `server/gameplay_tick.go` (modified, contains ClientJoin send) — FOUND
- Commit `2dba5cf2` — FOUND
- Commit `4cf32e6d` — FOUND

---
*Phase: 07-ai-pathfinding-commands-chat*
*Completed: 2026-06-24*
