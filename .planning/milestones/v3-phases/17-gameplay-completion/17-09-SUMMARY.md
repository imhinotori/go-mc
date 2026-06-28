---
phase: 17
plan: "17-09"
subsystem: server
tags: [logging, observability, disconnect-reason, keepalive, TUI-02]
requires:
  - server/client.go (Client teardown + quit signal)
  - server/keepalive.go (timeout kick funnel)
  - server/gameplay_tick.go (AcceptPlayer lifecycle)
provides:
  - structured stderr connect/disconnect logging (player joined / player left / connection from)
  - Client disconnect-reason capture seam (SetDisconnectReason / DisconnectReason)
affects:
  - Phase 17 visual gate (player join/leave now visible)
  - Phase 19 TUI-02 (full disconnect-reason taxonomy builds on this seam)
tech-stack:
  added: []
  patterns:
    - "atomic.Pointer[string] + CompareAndSwap for first-writer-wins disconnect reason (lock-free, race-clean by construction)"
    - "log.Printf to shared stderr for lifecycle events (no logger threaded through the tick struct)"
key-files:
  created: []
  modified:
    - server/client.go
    - server/keepalive.go (no edit needed — funnels through keepAliveClient.SendDisconnect)
    - server/gameplay_tick.go
    - server/server.go
decisions:
  - "Reason capture funnels through keepAliveClient.SendDisconnect, not keepalive.go kickPlayer, keeping keepalive.go untouched"
  - "Minimal viable reason taxonomy: timeout vs quit; full kick/protocol taxonomy deferred to TUI-02"
metrics:
  duration: ~15m
  completed: 2026-06-25
---

# Phase 17 Plan 09: Connect/Disconnect Logging Summary

Structured one-line-per-event stderr logging for the player connection lifecycle (connect, join, leave) with a minimal disconnect-reason taxonomy (timeout vs quit), bringing the TUI-02 disconnect-reason seam forward so the Phase 17 visual gate has join/leave visibility. Previously the server logged NOTHING about a player connecting, joining, or leaving.

## What Was Built

Three greppable, one-line-per-event log lines plus a reusable disconnect-reason seam on `Client`:

1. **`connection from <addr>: protocol=<p> intention=<i>`** — `server/server.go AcceptConn`, emitted after a successful handshake (before login), so list pings (intention=1) and login attempts (intention=2) are both visible. Uses the server's existing `*log.Logger` (`s.Logger`). Raw TCP probes that fail the handshake are intentionally NOT logged (they never reach this point).

2. **`player joined: name=<name> uuid=<uuid> entityID=<id> addr=<addr>`** — `server/gameplay_tick.go AcceptPlayer`, emitted immediately after `g.loop.register <- player` (player accepted, bootstrapped, handed to the tick). `name`/`uuid` from the login profile args, `entityID` from the allocated id, `addr` from the connection.

3. **`player left: name=<name> uuid=<uuid> reason=<reason>`** — `server/gameplay_tick.go AcceptPlayer`, emitted after `<-c.quit` unblocks (connection gone). `reason` is read from the new `Client.DisconnectReason()`.

`AcceptPlayer` holds no logger field, and threading one would touch the tick struct that sibling agents were concurrently editing, so the join/leave lines use `log.Printf` — which writes to the same stderr as `log.Default()` (the server's logger), keeping output unified and the change non-invasive.

## Disconnect-Reason Capture (the TUI-02 seam)

New on `*Client` (`server/client.go`):

- `disconnectReason atomic.Pointer[string]` — set BEFORE `Close`, by whatever code decides the teardown.
- `SetDisconnectReason(reason string)` — `CompareAndSwap(nil, &reason)`, first-writer-wins, idempotent, lock-free. Safe from any goroutine.
- `DisconnectReason() string` — returns the recorded reason, or `"quit"` if none was set.
- `RemoteAddr() string` — non-panicking remote-address accessor for logging (`"unknown"` fallback).

The keep-alive timeout kick funnels through `keepAliveClient.SendDisconnect` (`server/gameplay_tick.go`), which now calls `SetDisconnectReason("timeout")` before enqueuing the disconnect packet. That is the single funnel `keepalive.go kickPlayer` already uses, so **`keepalive.go` needed no edit** — the reason is threaded entirely through the existing adapter.

### Reason taxonomy: done now vs deferred to TUI-02

| Disconnect cause | Reason logged | Status |
|------------------|---------------|--------|
| Client closed socket / read EOF / write EOF | `quit` | DONE (default — no reason set, `Close` cascades) |
| Keep-alive timeout kick | `timeout` | DONE (set in `keepAliveClient.SendDisconnect`) |
| Queue-full drop-and-disconnect | `quit` | Partial — currently reported as `quit`; a distinct `"backpressure"` reason would set it in `Client.Send`'s drop path (deferred) |
| Protocol / login / config error (pre-join) | (separate `server.go` lines, format unchanged) | Already logged; never reaches the join/leave path |
| Explicit admin/command kick, ban, whitelist, etc. | n/a | DEFERRED to TUI-02 (Phase 19) — no such kick paths exist yet |

**Minimal viable distinction achieved:** `timeout` (keep-alive kick) vs `quit` (everything else). Full reason capture — distinct tokens for backpressure drops, command kicks, bans, and the protocol/login/config taxonomy unified into the same `player left` line — is **TUI-02 in Phase 19**. The seam (`SetDisconnectReason`) is in place: future kick paths only need to call it before `Close` and the leave line attributes them automatically.

## Concurrency Correctness

The reason field is touched by two goroutines: the keep-alive goroutine (`SetDisconnectReason("timeout")` inside `SendDisconnect`) and the accept goroutine (`DisconnectReason()` after `<-c.quit`). It is an `atomic.Pointer[string]` with `CompareAndSwap`/`Load` — race-clean by construction, mirroring the existing `Client.closed atomic.Bool` discipline. The `SetDisconnectReason` → `Close` (closes `c.quit`) → accept goroutine reads `c.quit` → `DisconnectReason()` ordering forms a happens-before edge, so the timeout reason is always visible to the leave line.

## Deviations from Plan

None — plan executed as written. Constraints honored: only `server/client.go`, `server/gameplay_tick.go`, `server/server.go` were edited (`server/keepalive.go` was in scope but needed no change because the reason threads through the existing `SendDisconnect` funnel). `fall_damage.go`, `fluid*.go`, `tick.go` tick-pipeline structs, and `world/` were NOT touched (sibling agents editing concurrently).

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0.
- `CGO_ENABLED=0 go test ./server/...` → all pass (`ok github.com/imhinotori/sulfur/server`).
- `-race` could not run in this environment (no C compiler / `-race` requires CGO), but the new field uses lock-free atomics exclusively, consistent with the existing teardown-path discipline.

## Self-Check: PASSED
- server/client.go modified (SetDisconnectReason / DisconnectReason / RemoteAddr / disconnectReason field) — FOUND
- server/gameplay_tick.go modified (join + leave log lines, timeout reason in SendDisconnect, log import) — FOUND
- server/server.go modified (connection-from log line) — FOUND
- Build exit 0, server tests pass — CONFIRMED
