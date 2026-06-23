---
phase: 02-net-protocol-state-machine
plan: 01
subsystem: api
tags: [networking, concurrency, channels, goroutines, backpressure, nbt, chat, race-detector, net.Pipe]

# Dependency graph
requires:
  - phase: 01-foundation-fork-codegen
    provides: forked go-mc (module github.com/imhinotori/go-mc), proto-776 packet ids, chat/, net codec, net/queue
provides:
  - "Per-connection Client with exactly one outbound writer goroutine draining a bounded PacketQueue (single-writer invariant, NET-05)"
  - "Read goroutine that produces Intent{*Client, pk.Packet} onto an inbound channel and touches no game state (network->tick seam)"
  - "Stable tick-seam channel type `chan Intent` with a stub consumer (stubTickConsumer) that Phase 3's real tick loop attaches to without an API change"
  - "Bounded outbound backpressure with drop-and-disconnect on full (NET-05 / T-2-04)"
  - "Extended NET-06 assertions over the existing codec: 2^21 MaxDataLength cap (uncompressed + compressed-bomb paths) and compression-threshold boundary"
  - "State-aware Disconnect helper carrying a readable chat.Message reason for Login/Config/Play (NET-07)"
  - "Shared in-memory net.Pipe test harness (newPipe + runAcceptConn) for all NET-01..04 integration tests"
  - "Fix: chat.Message NBT Field encoding (was double-encoding the compound tag header, corrupting every chat.Message on the wire)"
affects: [02-02, 02-03, 02-04, 03-tick-loop, phase-8-async]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Single-writer-per-connection: one goroutine owns conn.WritePacket; everything else enqueues onto a bounded ChannelQueue"
    - "Network->tick seam: read goroutine forwards only *Client + pk.Packet across `chan Intent`; no game-state pointer is reachable from a network goroutine"
    - "Bounded backpressure = drop-and-disconnect (ChannelQueue non-blocking Push; false -> Close)"
    - "Stub-now/attach-later: define the stable channel type with a stub consumer in Phase 2 so Phase 3 swaps the body, not the contract"
    - "Race gating via Docker: -race needs cgo; run server/net race suite in golang:1.26 container since no native C compiler is present"

key-files:
  created:
    - server/client.go (extended)
    - server/client_test.go
    - server/disconnect.go
    - server/pipe_test.go
    - net/packet/packet_test.go (extended)
  modified:
    - chat/nbtmessage.go

key-decisions:
  - "Outbound backpressure = bounded ChannelQueue + disconnect-on-full for Phase 2 (revisit when the tick owns flush timing in Phase 3)"
  - "readLoop's inbound send is guarded by a quit channel select so Close never leaves a producer to panic on a closed inbound; the consumer that owns inbound closes it only after producers stop"
  - "-race is run in a golang:1.26 Docker container (cgo enabled) because the Windows host has no C compiler and CGO_ENABLED=0; the NET-05 seam is proven race-clean there (count=10)"
  - "chat.Message.MarshalNBT body-only encoding fixed by network-format-encode-then-strip-leading-tag-byte, self-contained in chat/ (no nbt core change)"

patterns-established:
  - "Single writer goroutine per connection (the cheap single-threaded form of TICK-05 ownership)"
  - "chan Intent is the stable network->tick boundary contract"
  - "Drop-and-disconnect bounded backpressure"
  - "net.Pipe paired-conn harness for in-process protocol integration tests"

requirements-completed: [NET-05, NET-06, NET-07]

# Metrics
duration: 70min
completed: 2026-06-23
---

# Phase 2 Plan 01: Network plumbing & NET-05 concurrency seam Summary

**Per-connection Client with a single outbound writer goroutine, an Intent-producing read goroutine feeding a stub-consumed `chan Intent` tick seam, bounded drop-and-disconnect backpressure (NET-05, -race clean), 2^21 cap + threshold assertions over the existing codec (NET-06), a state-aware readable Disconnect helper (NET-07), and a fix for double-encoded chat.Message NBT.**

## Performance

- **Duration:** ~70 min
- **Started:** 2026-06-23T16:00:00Z (approx)
- **Completed:** 2026-06-23T17:11:26Z
- **Tasks:** 3
- **Files modified:** 6 (5 created/extended + 1 fork bugfix)

## Accomplishments
- NET-05 seam: `Client` owns exactly one `writeLoop` draining a bounded `NewChannelQueue`; `readLoop` produces `Intent{*Client, pk.Packet}` onto an inbound channel and holds no game-state pointer. Stable `chan Intent` contract + `stubTickConsumer` documented as the Phase 3 attach point. Proven `-race` clean (count=10) in Docker.
- Bounded backpressure: `Send` uses non-blocking `Push`; on a full queue it disconnects (drop-and-disconnect, T-2-04) rather than blocking or growing memory.
- NET-06: extended `net/packet/packet_test.go` to assert the existing 2^21 `MaxDataLength` cap rejects oversized uncompressed lengths AND oversized declared compressed `DataLength` (compression-bomb guard) without panic/large alloc, plus a compression-threshold boundary round-trip. `packet.go` untouched.
- NET-07: `server/disconnect.go` `Disconnect(conn, state, reason)` maps Login/Config/Play to the correct generated 776 Disconnect packet id and writes a readable `chat.Message`; round-trip verified per state.
- Shared `net.Pipe` harness (`server/pipe_test.go`) for the rest of Phase 2.
- Fixed a real fork bug: `chat.Message.MarshalNBT` double-encoded the compound tag header, silently corrupting every chat.Message Field on the wire (including the existing AcceptConn disconnect reasons).

## Task Commits

1. **Task 1: NET-06 cap/threshold tests + net.Pipe harness** - `ff3def6f` (test)
2. **Task 2: NET-05 Client seam (single writer, intent inbound, bounded backpressure, stub consumer)** - `5226032a` (feat)
3. **Deviation (Rule 1): chat.Message NBT double-encode fix** - `ce6e2149` (fix)
4. **Task 3: NET-07 state-aware Disconnect helper** - `852865af` (feat)

## Files Created/Modified
- `server/client.go` - Extended with `Intent`, `Client`, `NewClient`, `writeLoop` (single writer), `readLoop` (intent producer, quit-guarded send), `Send` (bounded drop-and-disconnect), `Start`, idempotent `Close` (sync.Once), `stubTickConsumer`. Kept the existing `PacketQueue` alias and legacy `Packet757/758`/`WritePacketError`. No game-state field.
- `server/client_test.go` - `TestSingleWriter`, `TestInboundSeam`, `TestBackpressureDisconnect`, `TestStubConsumerSeam`, `TestDisconnectReason`.
- `server/disconnect.go` - `ConnState` (StateLogin/StateConfig/StatePlay) + `Disconnect(conn, state, reason)`.
- `server/pipe_test.go` - `newPipe(t)` paired `*netmc.Conn` over `net.Pipe()` (threshold -1) + `runAcceptConn` runner.
- `net/packet/packet_test.go` - `TestMaxDataLength` (uncompressed + compressed-bomb reject) and `TestThreshold` (boundary round-trip).
- `chat/nbtmessage.go` - `MarshalNBT` now writes only the compound body (network-format encode then strip the single leading tag byte).

## Decisions Made
- Bounded ChannelQueue + disconnect-on-full chosen for Phase 2 backpressure; flush timing revisited in Phase 3 when the tick owns it.
- `readLoop` send is guarded by a `quit` channel select; ownership rule: the consumer that owns `inbound` closes it only after all producers (readLoops) have exited.
- `-race` executed in a `golang:1.26` Docker container (cgo) because the host lacks a C compiler and runs `CGO_ENABLED=0`.
- chat NBT fix kept self-contained in `chat/` (encode-in-network-format-then-strip-tag-byte) to avoid touching the shared `nbt` core.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] chat.Message.MarshalNBT double-encoded the compound tag header**
- **Found during:** Task 3 (NET-07 Disconnect helper)
- **Issue:** `chat.Message.MarshalNBT` emitted a full standalone NBT document (`0A 0A 00 00 ...`) instead of just the tag body that the `nbt.Marshaler` contract requires (the outer encoder already writes the tag byte via `Message.TagType`). The doubled header made every chat.Message Field undecodable by the network-format reader (NBT EOF) — silently corrupting Disconnect reasons sent via `pk.Marshal(id, reason)` in the existing `AcceptConn` and any chat component on the wire. NET-07's `Scan(&chat.Message{})` exposed it.
- **Fix:** Encode the chosen struct (`rawMsgStruct`/`translateMsg`) in network format to a buffer (`[tag][body]`, no name) and write only `body[1:]`, dropping the duplicate leading tag byte. Result: `0A 08 00 04 'text' 00 02 'hi' 00` round-trips to "hi".
- **Files modified:** chat/nbtmessage.go
- **Verification:** Byte-level dump confirms correct network-format encoding; round-trip recovers the text; `./chat/...` and full `./...` native suites stay green; NET-07 `TestDisconnectReason` passes for all three states.
- **Committed in:** ce6e2149 (standalone fix commit)

**2. [Rule 3 - Blocking] -race requires cgo; ran the race gate in Docker**
- **Found during:** Task 2 (NET-05 seam, mandated `-race`)
- **Issue:** `go test -race` failed with "race requires cgo"; the Windows host has no gcc/clang/mingw on PATH and `CGO_ENABLED=0`. The plan mandates `-race` on the seam.
- **Fix:** Ran the race suite inside the sanctioned `golang:1.26` Docker container (`CGO_ENABLED=1`), mounting the repo and the host module cache. The `-race` `quit`-select hazard (send-on-closed-channel) surfaced under `-count=5` and was fixed in `readLoop`/`Close` + a deterministic test teardown; re-verified clean at `-count=10` and across the full `./server/... ./net/...` suite.
- **Files modified:** server/client.go (quit channel + guarded send), server/client_test.go (deterministic producer-join teardown)
- **Verification:** `golang:1.26` container: `go test ./server/... ./net/... -race -count=1` all `ok`; NET-05 tests `-race -count=10` clean.
- **Committed in:** 5226032a (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 bug, 1 blocking)
**Impact on plan:** Both essential for correctness and for satisfying the mandated `-race` gate. The chat fix corrects a pre-existing wire-corruption bug in the fork that NET-07 depends on. No scope creep — `packet.go` and the existing AcceptConn mapping were left unchanged as the plan required.

## Issues Encountered
- The `-race` detector caught a genuine `send on closed channel` in the inbound seam teardown (Task 2) only at `-count>=5`. Root-caused to readLoop being able to send on `inbound` after the consumer/owner closed it. Fixed by (a) guarding readLoop's send with a `quit`-channel select in `Close`, and (b) making the test close `inbound` strictly after the lone producer (readLoop) has exited — the correct ownership handoff. This is exactly why the plan mandated `-race` on the seam now rather than in Phase 3.

## Tick-Seam Contract (for Waves 02-02..04 and Phase 3)

The stable seam is the channel element type:

```go
// server/client.go
type Intent struct {
    Client *Client   // the connection the packet arrived on
    Packet pk.Packet // raw, undecoded beyond ID; dispatched by the tick consumer, never by readLoop
}

// The seam channel type. Phase 2 uses stubTickConsumer; Phase 3 replaces the body.
func (c *Client) Start(inbound chan<- Intent)        // launches one writeLoop + one readLoop
func (c *Client) Send(p pk.Packet)                   // bounded; drop-and-disconnect on full
func (c *Client) Close()                             // idempotent; closes quit, outbound queue, conn
func stubTickConsumer(inbound <-chan Intent)         // drain+discard; Phase 3 attach point
```

Consume by reading `Intent` values off the `chan Intent`; dispatch happens in the consumer (tick loop), never in `readLoop`. The consumer owns `inbound`'s lifecycle and must not close it while any `Client` producing onto it is live.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Wave 02-02..04 can use the shared `net.Pipe` harness (`newPipe`, `runAcceptConn`) and the `Disconnect` helper for state-aware kicks.
- Phase 3 attaches the real tick loop to `chan Intent` by replacing `stubTickConsumer`'s body — no API/type change.
- Concern: native `-race` is unavailable on this Windows host (no C compiler, CGO_ENABLED=0). Future race gates must run in Docker (`golang:1.26`); consider installing mingw-w64 or documenting the Docker race-gate as the standard path.

## Self-Check: PASSED

All created/modified files exist on disk and all task commits are present in history:
- Files: server/client.go, server/client_test.go, server/disconnect.go, server/pipe_test.go, net/packet/packet_test.go, chat/nbtmessage.go, 02-01-SUMMARY.md — all FOUND.
- Commits: ff3def6f, 5226032a, ce6e2149, 852865af — all FOUND.
- Gates: `go build ./...` OK; `go vet ./...` clean; NET-06 + NET-07 native pass; `go test ./server/... ./net/... -race` clean in golang:1.26 (NET-05 seam race-clean at -count=10).

---
*Phase: 02-net-protocol-state-machine*
*Completed: 2026-06-23*
