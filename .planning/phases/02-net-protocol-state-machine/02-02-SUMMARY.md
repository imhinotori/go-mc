---
phase: 02-net-protocol-state-machine
plan: 02
subsystem: net-protocol-gate
tags: [handshake, status, login, compression, proto-776, assembly]
requires:
  - "Phase 1: module github.com/imhinotori/go-mc, proto-776 data, constants 776/26.2"
  - "Plan 02-01: NET-05 Client seam, NET-07 Disconnect helper, net.Pipe harness (pipe_test.go)"
provides:
  - "NET-01: proto-776 assertion on the login path with a readable Login Disconnect on mismatch"
  - "NET-02: concrete proto-776 Status handler advertising 26.2 / 776 / MOTD / player count"
  - "NET-03: offline Login with Set Compression negotiated at threshold and LoginAcknowledged read (verified)"
  - "cmd/ender runnable server assembly composing the full Phase-2 gate"
affects:
  - "server/server.go AcceptConn (login path now gated)"
  - "cmd/ender/main.go (new entrypoint; consumes Configurations from 02-03/02-04 and GamePlay seam from Phase 3)"
tech-stack:
  added: []
  patterns:
    - "Proto gate placed in AcceptConn case 2 before AcceptLogin; status (case 1) intentionally ungated"
    - "ListPingHandler composed from PingInfo (version/MOTD) + PlayerList (player counts)"
    - "Integration tests drive the gate over the Wave-1 net.Pipe harness (newPipe/runAcceptConn)"
key-files:
  created:
    - cmd/ender/main.go
    - server/handshake_test.go
    - server/ping_test.go
    - server/login_test.go
  modified:
    - server/server.go
decisions:
  - "Reused the Wave-1 Disconnect(conn, StateLogin, reason) helper for the NET-01 rejection rather than hand-marshalling the Login Disconnect packet — keeps the state->packet-id mapping in one place"
  - "cmd/ender ListPingHandler embeds *PingInfo + *PlayerList because PingInfo alone does not satisfy MaxPlayer/OnlinePlayer/PlayerSamples (those live on PlayerList)"
  - "cmd/ender Configurations uses registry.NewNetworkCodec() (empty) — real registry data is wired by Plan 02-03/02-04; the empty codec keeps the assembly buildable for the Phase-2 gate"
  - "stubGamePlay sends a readable Play Disconnect and returns — no tick loop, no game state (Phase 3 replaces it)"
metrics:
  duration: "~5 min"
  completed: 2026-06-23
  tasks: 3
  files: 5
---

# Phase 2 Plan 02: Handshake / Status / Login Gate Summary

Assembled the front of the protocol state machine over the fork's existing primitives: a proto-776 assertion on the login path (the one genuinely new behavior), a concrete proto-776 Status handler, verified offline login with compression negotiation, and a runnable `cmd/ender` server that composes the whole gate.

## What Was Built

### NET-01 — Proto-776 assertion (server/server.go)
`AcceptConn` `case 2` (login) now compares the handshake protocol to `ProtocolVersion` (776) **before** calling `AcceptLogin`. On mismatch it sends a readable Login Disconnect via the Wave-1 helper and returns:

```go
case 2: // login
    if protocol != ProtocolVersion {
        _ = Disconnect(conn, StateLogin, chat.Text(
            "Unsupported protocol: server is "+ProtocolName+" ("+strconv.Itoa(ProtocolVersion)+")"))
        // logs and returns -> deferred conn.Close() closes the socket
        return
    }
    name, id, ... := s.AcceptLogin(conn, protocol)
```

The disconnect reason mentions both `ProtocolName` ("26.2") and `ProtocolVersion` (776), so a mismatched client gets a human-readable explanation — never a silent drop (T-2-01 mitigated). `case 1` (status) is deliberately left ungated so a version-mismatched client still sees the server-list version label.

### NET-02 — Status payload shape
The status response (produced by the existing `Server.listResp`) is JSON of the form:

```json
{
  "version":     { "name": "26.2", "protocol": 776 },
  "players":     { "max": 20, "online": 0, "sample": [] },
  "description": { "text": "<MOTD>" },
  "favicon":     "data:image/png;base64,..."   // omitted when empty
}
```

`version.name` comes from `PingInfo.Name()` (`ProtocolName`), `version.protocol` from `PingInfo.Protocol(_)` which returns the fixed `ProtocolVersion` (no literal 776), and the player counts from `PlayerList`. The concrete handler is constructed in `cmd/ender/main.go` by embedding `*PingInfo` + `*PlayerList`.

### NET-03 — Offline login + compression
Verified the existing `MojangLoginHandler{OnlineMode:false, Threshold:256}` flow end-to-end: `LoginStart` → offline UUID via `offline.NameToUUID(name)` → **Set Compression at threshold 256 sent before LoginSuccess** → `LoginSuccess` (ClientboundLoginLoginFinished) carrying the offline UUID + username → server reads `LoginAcknowledged` and proceeds to config. No login/codec re-implementation.

### cmd/ender/main.go — assembly
Composes `server.Server` with the proto-776 ping handler, `MojangLoginHandler{OnlineMode:false, Threshold:256}`, `Configurations{Registries: registry.NewNetworkCodec()}`, and `stubGamePlay`. Builds to a binary; `Listen(":25565")` runs the gate. The `-addr` flag overrides the bind address.

## Tasks Completed

| Task | Name | Commit | Files |
| ---- | ---- | ------ | ----- |
| 1 | NET-01 proto-776 assertion + readable Disconnect | c56befc7 | server/server.go, server/handshake_test.go |
| 2 | NET-02 proto-776 Status handler + cmd/ender assembly | 78d4c8d2 | cmd/ender/main.go, server/ping_test.go |
| 3 | NET-03 offline login + compression test | 0dc2fae1 | server/login_test.go |

## Gate Results

| Gate | Result |
| ---- | ------ |
| `go test ./server/ -run 'TestHandshakeProtocol\|TestStatusPing\|TestOfflineLogin' -count=1` | PASS |
| `go test ./server/... ./net/...` | PASS (all packages) |
| `go build ./...` | clean (cmd/ender binary builds) |
| `go vet ./...` | clean |
| `go test -race` (3 NET tests, golang:1.26 Docker) | PASS, no data races |

The native host has no C compiler (CGO_ENABLED=0), so the `-race` gate was run in the `golang:1.26` container with the repo mounted, exactly as Wave 1 did. The pipe-harness tests spawn a server goroutine per connection; `-race` confirms the handshake/login/status handoff has no races.

## How NET-01 Rejects Mismatches

1. `handshake()` decodes `(protocol, intention)` from the handshake packet.
2. `intention == 2` (login) enters the gated branch.
3. If `protocol != 776`, `Disconnect(conn, StateLogin, reason)` writes a `ClientboundLoginLoginDisconnect` whose NBT-encoded `chat.Message` reason reads `Unsupported protocol: server is 26.2 (776)`.
4. `AcceptConn` returns; the deferred `conn.Close()` closes the socket. `AcceptLogin` is never reached.
5. `intention == 1` (status) bypasses the check entirely — `acceptListPing` answers with `ProtocolVersion` regardless of the client's protocol, so a mismatched client still gets a clean version label.

`TestHandshakeProtocol` proves all three cases: mismatch→readable disconnect (and the goroutine returns, not hangs), match→reaches `AcceptLogin`, wrong-protocol status→reaches the status responder.

## Status Payload Shape

See NET-02 above. Asserted by `TestStatusPing`: drives a status-intent handshake + Status Request over the pipe, reads the `ClientboundStatusStatusResponse`, unmarshals the JSON, and checks `version.name == "26.2"`, `version.protocol == 776`, `players.max`/`players.online` match the configured `PlayerList`, and the description (MOTD) is non-empty.

## Deviations from Plan

None affecting behavior. One implementation detail worth recording:

- **[Plan-guided] ListPingHandler composition.** The plan suggested `NewPingInfo(...)` could serve as the status handler directly, but `PingInfo` only implements `Name/Protocol/Description/FavIcon` — `MaxPlayer/OnlinePlayer/PlayerSamples` live on `PlayerList`. The concrete handler therefore embeds both `*PingInfo` and `*PlayerList`, which is the fork's standard composition. No change to the plan's intent; just the correct wiring.

## Known Stubs

| Stub | File | Reason |
| ---- | ---- | ------ |
| `Configurations{Registries: registry.NewNetworkCodec()}` (empty codec) | cmd/ender/main.go | Phase-2 placeholder. Real 26.2 registry data is provided by Plan 02-03 (`server/registrydata`) and wired into the config handler by Plan 02-04. The empty codec keeps the `cmd/ender` assembly buildable now; the gate (NET-01/02/03) does not depend on registry contents. |
| `stubGamePlay.AcceptPlayer` sends a Play Disconnect and returns | cmd/ender/main.go | Intentional Phase-2 GamePlay seam. Phase 3 replaces it with the NET-05 Client read/write loops and the tick loop. The stub starts no goroutines and accesses no game state — it only proves the gate reaches the Play handoff. |

Both stubs are explicitly sanctioned by the plan (`<key_facts>`: "depend on the existing Configurations type ... Plan 02-04 rewrites its AcceptConfig body" and "The GamePlay is a Phase-2 stub").

## Self-Check: PASSED

- Files exist: server/server.go, server/handshake_test.go, server/ping_test.go, server/login_test.go, cmd/ender/main.go — all FOUND.
- Commits exist: c56befc7, 78d4c8d2, 0dc2fae1 — all FOUND in git log.
