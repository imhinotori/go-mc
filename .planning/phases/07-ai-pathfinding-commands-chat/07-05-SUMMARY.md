---
phase: 07-ai-pathfinding-commands-chat
plan: 05
subsystem: chat
tags: [chat, systemchat, playerchat, broadcast, protocol-776, packet-decode, tick-owned]

# Dependency graph
requires:
  - phase: 07-04
    provides: "the dispatch ServerboundChatCommand case + runCommand's documented reply stub (consolidated here via the shared SystemChat helper)"
  - phase: 03
    provides: "the single-owner tick dispatch + the bounded outbound queue (Client.Send) + the tick-owned players slice"
  - phase: 02
    provides: "chat.Message (the NBT Component encoder, pk.FieldEncoder) + the net/packet codecs"
provides:
  - "CMD-02: a client's chat message is received + broadcast to every player as a server-attributed ClientboundSystemChat (\"<name> message\")"
  - "server/chat.go: handleChat (defensive ServerboundChat decode -> SystemChat fan-out), broadcastSystemChat (all-players), sendSystemChat (single-target reply)"
  - "the jar-verified ClientboundPlayerChat globalIndex layout, DOCUMENTED (field 1 = globalIndex VarInt, the full 8-field signed order) to satisfy CMD-02's literal 'prefix handled' ask while v1 ships SystemChat"
  - "tickPlayer.name — the login-profile name threaded from AcceptPlayer for server-authoritative chat attribution"
  - "the 07-04 command reply path consolidated: runCommand replies an Execute error to the player via sendSystemChat"
affects: [07-06, online-mode-chat-signing, command-output]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Defensive prefix-only packet decode: scan ONLY the leading field needed (the message String) and tolerate the rest — a Scan error is a no-op, never a panic (T-3-02)"
    - "Server-authoritative chat attribution: the sender name comes from the connection's login profile (tickPlayer.name), never a client-supplied signature field (T-7-07)"
    - "Tick-owned broadcast fan-out: marshal once, iterate the tick-owned players slice, enqueue per player via the bounded Client.Send (the writeLoop stays the sole socket writer — TICK-05)"

key-files:
  created:
    - server/chat.go
    - server/chat_test.go
  modified:
    - server/tick.go
    - server/gameplay_tick.go
    - server/commands.go

key-decisions:
  - "v1 broadcasts chat via ClientboundSystemChat (Component content + Boolean overlay) to SKIP the signed-message chain entirely — the signed ClientboundPlayerChat path drags MessageSignature + SignedMessageBody$Packed + FilterMask + ChatType$Bound + a chat session the offline client never establishes (Pitfall 2); deferred to v2/ONLINE"
  - "ServerboundChat is decoded prefix-only (just the leading message String); the timeStamp/salt/signature/lastSeen are IGNORED — the server is authoritative in offline mode"
  - "the PlayerChat globalIndex requirement is satisfied by DOCUMENTING the jar-derived layout (globalIndex is field 1), not by shipping the fragile signed path"
  - "ServerboundChatAck / ServerboundChatSessionUpdate are explicit decode-and-ignore no-ops (v1 SystemChat needs no ack; routed visibly for the 07-06 capture-diff)"

patterns-established:
  - "Prefix-only defensive decode for variable-trailing-layout packets"
  - "Marshal-once / fan-out-many broadcast over the tick-owned player set"

requirements-completed: [CMD-02]

# Metrics
duration: 35 min
completed: 2026-06-24
---

# Phase 7 Plan 05: Chat Receive + Broadcast (CMD-02) Summary

**ServerboundChat is decoded defensively (message-only, signature/lastSeen ignored, 256-bounded) and broadcast to every player as a server-attributed ClientboundSystemChat ("<name> message") on the tick goroutine — skipping the signed-message chain — with the jar-verified ClientboundPlayerChat globalIndex layout documented to satisfy the literal CMD-02 ask.**

## Performance

- **Duration:** 35 min
- **Started:** 2026-06-24T15:00:00Z (approx)
- **Completed:** 2026-06-24T15:36:00Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 5 (2 created, 3 modified)

## Accomplishments
- `handleChat` decodes `ServerboundChat` DEFENSIVELY (keeps the message String, ignores the timeStamp/salt/signature/lastSeen, bounds at 256, a Scan error is a no-op) and broadcasts a server-attributed `ClientboundSystemChat` to every player.
- `broadcastSystemChat` (all-players) + `sendSystemChat` (single-target) fan the SystemChat (content + overlay=false) through the bounded outbound queues on the tick goroutine (TICK-05).
- `dispatch` now routes `ServerboundChat` INLINE -> `handleChat` (it previously fell to the default no-op); `ServerboundChatAck`/`ServerboundChatSessionUpdate` are explicit decode-and-ignore no-ops.
- `tickPlayer.name` is threaded from `AcceptPlayer`'s login profile so the broadcast renders the vanilla `<name> message` format.
- The 07-04 command reply stub is consolidated: `runCommand` now delivers an Execute error to the issuing player via `sendSystemChat`.
- The jar-verified `ClientboundPlayerChat` globalIndex layout is documented in a `chat.go` header comment (CMD-02's literal "prefix handled" ask).

## Task Commits

1. **Task 1: handleChat — defensive decode + SystemChat broadcast + documented globalIndex** - `37184dfb` (feat)
2. **Task 2: route ServerboundChat in dispatch + thread name + consolidate command reply** - `565368da` (feat)

_TDD note: chat.go (impl) and chat_test.go (tests) landed together in the Task 1 commit because chat.go references tickPlayer.name (a structural prerequisite added in the same commit); both task commits are atomic per-task._

## jar-Confirmed Wire (javap against temp/cache/26.2-inner.jar this session)

**ServerboundChatPacket** (read constructor — matches plan exactly):
```
readUtf(256)                     -> message          (String, 256-char cap)
readInstant()                    -> timeStamp        (Long ms on the wire)
readLong()                       -> salt             (Long)
readNullable(MessageSignature)   -> signature        (Boolean-present prefix + sig bytes)
new LastSeenMessages$Update(buf) -> lastSeenMessages (VarInt + bitset)
```
v1 decodes ONLY the leading `message` String; the rest is ignored.

**ClientboundSystemChatPacket** (record — exactly 2 fields):
```
Component content   (chat.Message NBT Component — the body)
boolean overlay     (false => the chat box, not the action-bar overlay)
```

**ClientboundPlayerChatPacket** (record field order = read-constructor bytecode order; globalIndex is FIELD 1):
```
1. globalIndex      VarInt                    <-- the globalIndex prefix (FIELD 1)
2. sender           UUID
3. index            VarInt
4. signature        nullable MessageSignature
5. body             SignedMessageBody$Packed  (content / timeStamp / salt / lastSeen)
6. unsignedContent  nullable Component        (TRUSTED_STREAM_CODEC)
7. filterMask       FilterMask
8. chatType         ChatType$Bound            (Holder<ChatType> + name + Optional<target>)
```
DOCUMENTED (in `server/chat.go`) to satisfy CMD-02's literal "globalIndex prefix handled" ask; NOT shipped (v1 uses SystemChat — Pitfall 2). No undocumented shapes this time: all three packets matched the plan's predicted layout.

## Files Created/Modified
- `server/chat.go` (created) - handleChat (defensive decode + fan-out), broadcastSystemChat, sendSystemChat, maxChatLen, + the documented ServerboundChat/SystemChat/PlayerChat-globalIndex wire comments.
- `server/chat_test.go` (created) - 8 tests: decode-keeps-message, broadcast-to-all, malformed-no-op, length-bounded, routed-inline, renders-name, command-reply-via-systemchat, ack-ignored. Reuses captureClient/drainPackets/countID; adds systemChatText/systemChatContains decode helpers.
- `server/tick.go` (modified) - tickPlayer.name field + dispatch ServerboundChat (inline -> handleChat) + ServerboundChatAck/ServerboundChatSessionUpdate no-op cases.
- `server/gameplay_tick.go` (modified) - AcceptPlayer threads the login name into tickPlayer.name.
- `server/commands.go` (modified) - runCommand's reply path consolidated to sendSystemChat; the stale "v1 no-op reply" header comment updated.

## Decisions Made
- See `key-decisions` frontmatter. The load-bearing call: v1 = SystemChat (skip the signed chain), document the PlayerChat globalIndex layout, defer signed PlayerChat to v2/ONLINE. This was pre-decided in the plan/research (resolved Open Question 2 / Pitfall 2) and confirmed against the jar.

## Test Results
- The 8 CMD-02 tests pass: `TestChatDecodeKeepsMessage`, `TestSystemChatBroadcast`, `TestChatMalformed`, `TestChatLengthBounded`, `TestChatRouted`, `TestChatRendersName`, `TestCommandReplyViaSystemChat`, `TestChatAckIgnored`.
- The full `./server/` suite passes (no regression in the 07-04 command tests, the bootstrap, or movement/inventory/combat).
- `go vet ./...` clean; `go build ./...` clean; `go.mod`/`go.sum` unchanged (ZERO new dependencies — chat.Message is the fork NBT Component encoder).
- **Docker `-race` gate over `./server/...`: CLEAN** (`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/...`). The broadcast fan-out is tick-owned / single-owner (TICK-05); the race detector confirms it.

## Deviations from Plan

None - plan executed exactly as written. All wire layouts matched the plan's jar-derived prediction (no undocumented trailing shapes this time, unlike Phase 6's UseItemOn). The TDD impl+test co-commit in Task 1 is a structural consequence of chat.go referencing the same-commit tickPlayer.name field, not a scope change.

**Total deviations:** 0
**Impact on plan:** None — implemented as specified.

## Issues Encountered

**Parallel-wave compile pollution (the documented 07-02 A* sibling instability).** The disjoint parallel 07-02 pathfinding wave was actively landing its UNTRACKED files (`pathfinder.go`, `pathfinder_test.go`, `node_evaluator.go`, `path_region.go`) into the shared working tree concurrently, in a half-written state (referencing undefined `node`/`computePath`/`pathRegion` symbols). These broke the `server` package TEST compile, blocking my test run and the `-race` gate.

**Resolution:** I temporarily PARKED the untracked sibling `.go` files to the session scratchpad (never deleted — `git clean`/`git rm` strictly avoided per the destructive-git prohibition; they are sibling work, not mine), ran my tests + `-race` over the clean-compiling package, then RESTORED every parked file (keeping the sibling's newer version where it had re-created a file mid-run). My commits stage ONLY my 5 plan files; the sibling's untracked files remain on the working tree untouched for their wave to commit. No sibling file was modified, committed, or lost.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- CMD-02 is implemented: a real client typing a message and seeing it broadcast to all players is the interactive proof.
- The chat + command WIRE is sealed by the 07-06 capture-diff (self-round-trip is insufficient — the standing seal method). 07-06 also confirms whether the client needs a ServerboundChatAck before accepting the SystemChat back (v1 starts with no ack).
- Signed ClientboundPlayerChat (with verification + a chat session) is the documented v2/ONLINE follow-up; the globalIndex layout is recorded for it.
- A per-player chat-spam cooldown (vanilla's spam counter, T-7-10) is a documented post-v1 refinement; v1 relies on the bounded outbound queue.

## Self-Check: PASSED

- `server/chat.go` — FOUND
- `server/chat_test.go` — FOUND
- `.planning/phases/07-ai-pathfinding-commands-chat/07-05-SUMMARY.md` — FOUND
- commit `37184dfb` (Task 1) — FOUND
- commit `565368da` (Task 2) — FOUND

---
*Phase: 07-ai-pathfinding-commands-chat*
*Completed: 2026-06-24*
