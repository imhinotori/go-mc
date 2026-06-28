---
phase: 19-operator-ux-tui-console-disconnect-logging
plan: 03
subsystem: operator-tooling
tags: [disconnect-taxonomy, slog, tui-02, keepalive-hardening, client-teardown, structured-logging]
requires:
  - "Client.SetDisconnectReason / DisconnectReason / RemoteAddr — the 17-09 atomic first-writer-wins foundation"
  - "tui.NewHandler — the default slog handler installed in TTY mode (Plan 19-01) that fans records to the TUI viewport + stderr"
provides:
  - "full disconnect-reason taxonomy tagged at every drop seam: login_failure / protocol_mismatch / config_failure / timeout / kicked / protocol_error / write_error / backpressure / quit"
  - "readLoop classification: protocol_error on a non-EOF/non-ErrClosed read error vs the default clean quit"
  - "writeLoop write_error + Send-full backpressure tagged before Close"
  - "reasonHuman(token string) string — maps the taxonomy tokens to the five TUI-02 human strings (timeout / protocol error / login failure / kick / clean quit)"
  - "slog join/leave lines with structured attrs (name, uuid, addr, reason, detail) — surface in BOTH the TUI viewport and stderr"
  - "KeepAlive.removePlayer double-leave hardened (ok-guard on listIndex[c]) — the STATE.md deferred-item closed"
affects:
  - "server/client.go — readLoop/writeLoop/Send now tag the teardown cause"
  - "server/server.go — the four login/config/protocol drop logs are slog with reason attrs (Logger field retained for main.go's startup line)"
  - "server/gameplay_tick.go — join/leave log.Printf → slog (log import dropped)"
  - "server/keepalive.go — removePlayer no longer panics on a double-leave"
tech-stack:
  added:
    - "log/slog used directly in server/server.go, server/playerlist.go, server/gameplay_tick.go (drop-seam logging) — no new external dep"
  patterns:
    - "tag-before-Close, first-writer-wins (SetDisconnectReason CAS) at every client-teardown seam"
    - "errors.Is(err, io.EOF) / stdnet.ErrClosed classification to separate a clean quit from a protocol fault"
    - "pre-*Client drop sites (server.go login/config stage) carry the reason as a slog attr, not via SetDisconnectReason (no *Client yet)"
    - "best-effort token via an optional disconnectReasonSetter assertion + an always-on slog.Warn so the kick is logged regardless"
    - "ok-guard on a map read returning early (mirrors tickPlayer) to make a double-leave a no-op"
    - "single reasonHuman mapping point so the leave line and any future reason-display share one vocabulary"
key-files:
  created:
    - "server/disconnect_reason.go — reasonHuman taxonomy→human-string map"
    - "server/keepalive_test.go — double-leave does-not-panic + reasonHuman table tests"
  modified:
    - "server/client.go — readLoop protocol_error classification, writeLoop write_error, Send backpressure (+ errors/io/stdnet imports)"
    - "server/client_test.go — clean-EOF / ErrClosed / decode-error / send-full / write-error reason tests (+ errReader/errWriter/newReaderConn harness)"
    - "server/server.go — 4 s.Logger.Printf → slog with reason attrs (+ log/slog import; *log.Logger field kept)"
    - "server/playerlist.go — server-full kick tags kicked (best-effort) + slog.Warn (+ log/slog import + disconnectReasonSetter)"
    - "server/gameplay_tick.go — join/leave log.Printf → slog with reason + reasonHuman detail (log import → log/slog)"
    - "server/keepalive.go — removePlayer ok-guard double-leave hardening"
decisions:
  - "stdlib net aliased stdnet in client.go because the wrapped fork net is already imported as the identifier `net`; readLoop uses stdnet.ErrClosed + io.EOF for the clean-vs-fault split"
  - "the server.go login/config/protocol drop sites carry the reason token as a slog ATTR (not via SetDisconnectReason) because at that stage the teardown is a raw conn, not yet a *Client with the atomic disconnectReason"
  - "Pitfall 6 honored: the 4 s.Logger.Printf lines were REPLACED with slog (no double-log); the *log.Logger field stays non-nil because main.go (19-02) still emits the startup line through it"
  - "playerlist.go server-full kick: the PlayerListClient interface only exposes SendDisconnect, so SetDisconnectReason(kicked) is best-effort via an optional disconnectReasonSetter assertion; the always-on slog.Warn is the actual TUI-02 deliverable"
  - "reasonHuman lives in a dedicated disconnect_reason.go (not gameplay_tick.go) as the single taxonomy→human mapping point; unknown tokens pass through so a newly-added reason is never silently lost"
  - "double-leave test calls the package-private pushPlayer/removePlayer directly (deterministic) rather than driving Run's channels — a clean unit test of the ok-guard"
metrics:
  duration: ~22min
  completed: 2026-06-26
  tasks: 3
  files: 8
status: complete
---

# Phase 19 Plan 03: Disconnect Taxonomy (TUI-02) Summary

Expands the 17-09 atomic first-writer-wins `disconnectReason` plumbing into the FULL
disconnect taxonomy and surfaces every player drop through `slog`, so the operator sees
WHY a player left — in both the TUI viewport (TTY) and plain stderr (headless). Tags each
drop seam at the file:line the research's Disconnect Taxonomy enumerated, classifies a
decode/read fault (`protocol_error`) from a clean EOF quit in `readLoop`, converts the
join/leave/connection `log.Printf` lines to structured `slog`, and lands the deferred
`KeepAlive.removePlayer` double-leave hardening (STATE.md deferred-item, carried from
Phase 3).

## The taxonomy token set (phase verification gate)

Every drop seam now sets/logs exactly one token:

| Token | Drop seam (file) | How set |
|-------|------------------|---------|
| `login_failure` | server.go (AcceptLogin err) | slog attr (pre-*Client raw conn) |
| `protocol_mismatch` | server.go (protocol != 776) | slog attr |
| `config_failure` | server.go (AcceptConfig err) | slog attr |
| `timeout` | gameplay_tick.go keepAliveClient.SendDisconnect | SetDisconnectReason (17-09, kept) |
| `kicked` | playerlist.go server-full | best-effort SetDisconnectReason + slog.Warn |
| `protocol_error` | client.go readLoop (non-EOF/non-ErrClosed read err) | SetDisconnectReason before Close |
| `write_error` | client.go writeLoop (WritePacket err) | SetDisconnectReason before Close |
| `backpressure` | client.go Send (queue full) | SetDisconnectReason before Close |
| `quit` | default (clean EOF / our own Close) | DisconnectReason() fallback |

## The reasonHuman map (TUI-02 leave-line wording)

`reasonHuman(token) string` (server/disconnect_reason.go) — the single mapping point:

| Tokens | Human string |
|--------|-------------|
| `""`, `quit` | clean quit |
| `timeout` | timeout |
| `protocol_error`, `protocol_mismatch` | protocol error |
| `login_failure`, `config_failure` | login failure |
| `kicked`, `backpressure`, `write_error` | kick |
| (unknown) | the token verbatim (never lost) |

The leave line emits `slog.Info("player left", name, uuid, addr, reason, detail)` where
`detail = reasonHuman(reason)`.

## Tasks

1. **Classify protocol_error vs clean-quit in readLoop; tag write_error/backpressure**
   (commit `18e6db4a`). `readLoop` tags `protocol_error` only on a non-EOF/non-ErrClosed
   read error (clean EOF / `stdnet.ErrClosed` leaves the default `quit`); `writeLoop` tags
   `write_error`; `Send`-full tags `backpressure` — all before `Close`, first-writer-wins
   preserved. Five client_test.go behaviors (clean EOF, ErrClosed, decode error, send-full,
   write error).

2. **Tag login/config/protocol/kick drop sites + convert connection/login logs to slog**
   (commit `0afd0713`). The four `s.Logger.Printf` lines in server.go converted to `slog`
   with structured attrs (addr + reason token + got/want/err). playerlist.go server-full
   kick tags `kicked` (best-effort via the optional `disconnectReasonSetter`) + an always-on
   `slog.Warn`. Pitfall 6 honored — no double-log; the `*log.Logger` field kept for main.go.

3. **slog the join/leave lines (full taxonomy) + harden KeepAlive.removePlayer double-leave**
   (commit `ed106fe3`). join/leave `log.Printf` → `slog` with name/uuid/addr/reason +
   `reasonHuman` detail; the `log` import dropped from gameplay_tick.go. `removePlayer`
   hardened with an ok-guard on `listIndex[c]` (mirrors the tickPlayer guard, non-panicking)
   — a timeout-kick-then-ClientLeft double-leave is now a no-op. Double-leave + reasonHuman
   table tests.

## Deviations from Plan

None — plan executed exactly as written. All three tasks landed with their planned commits,
behaviors, and verification commands. No Rule 1-4 deviations were needed.

## Threat mitigations landed

- **T-19-07** (DoS: removePlayer double-leave panic) — closed by the ok-guard early return.
- **T-19-08** (terminal injection via a hostile name/addr in a leave line) — the slog records
  flow through Plan 01's `formatLine` sanitize at the single fan-out point; this plan adds no
  per-call-site sanitization (correctly — it is centralized).
- **T-19-09** (an unattributed drop) — the full taxonomy tags every seam; the leave line always
  logs a reason (defaulting to `quit`/clean quit), so no disconnect is unattributed.

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0
- `go vet ./server/...` → clean
- `go test ./server/` → ok (2.2s)
- Docker `golang:1.26` `go test -race -timeout 1800s ./server/` → ok (9.4s)
- grep gate: every taxonomy token (login_failure / protocol_mismatch / config_failure /
  timeout / protocol_error / write_error / backpressure / kicked / quit) appears at its
  drop site.
- File scope: touched ONLY server/client.go, server/server.go, server/keepalive.go,
  server/playerlist.go, server/gameplay_tick.go, server/client_test.go, server/keepalive_test.go,
  server/disconnect_reason.go (new). Did NOT touch 19-02's commands.go / tick.go / main.go.

## Self-Check: PASSED
