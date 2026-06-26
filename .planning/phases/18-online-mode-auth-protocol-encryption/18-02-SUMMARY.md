---
phase: 18-online-mode-auth-protocol-encryption
plan: 02
subsystem: server (Play-state tab-list / online-mode skins)
tags: [online-mode, skins, ONLINE-01, player-info-update, game-profile-properties, wire]
requires:
  - "server/auth AcceptLogin already returns the hasJoined properties []user.Property (18-01 crypto/flag)"
  - "user.Property.WriteTo (yggdrasil/user) == Property.STREAM_CODEC (pre-existing, jar-faithful)"
provides:
  - "Authenticated GameProfile properties (the textures skin) reach the ADD_PLAYER tab-list wire for SELF + every OTHER player"
  - "playerInfoEntriesEncoder writes a real count-prefixed GAME_PROFILE_PROPERTIES list (was hardcoded VarInt(0))"
  - "tickPlayer.properties + bootstrapParams.properties carry the authenticated skin (no longer dropped in AcceptPlayer)"
affects:
  - "server/play_join.go (writePlayerInfoUpdateAdd + playerInfoEntriesEncoder + bootstrapParams + self-add)"
  - "server/player_visibility.go (broadcastPlayerInfoAdd / sendExistingPlayersTo)"
  - "server/gameplay_tick.go (AcceptPlayer tickPlayer literal + bootstrapParams construction)"
  - "server/tick.go (tickPlayer struct)"
tech-stack:
  added: []
  patterns:
    - "Reuse the jar-faithful user.Property.WriteTo (== ByteBufCodecs$32 / Property.STREAM_CODEC) in the property loop — never hand-write property bytes"
    - "Thread server-authoritative profile data (properties) onto the tickPlayer at registration as a value, like name/uuid/entityID (crosses no tick-owned state)"
key-files:
  created: []
  modified:
    - "server/play_join.go"
    - "server/player_visibility.go"
    - "server/gameplay_tick.go"
    - "server/tick.go"
    - "server/play_join_test.go"
    - "server/play_join_capture_test.go"
decisions:
  - "Reused user.Property.WriteTo verbatim — it already matches Property.STREAM_CODEC (String name, String value, Optional<signature>), confirmed against ByteBufCodecs$32.encode bytecode; no new per-property codec written"
  - "Self-add passes params.properties (NOT an empty slice) so the joiner sees its OWN skin — required by the objective, not optional"
metrics:
  duration: 4min
  tasks: 2
  files: 6
  completed: "2026-06-26T22:28:40Z"
---

# Phase 18 Plan 02: ADD_PLAYER skin properties (ONLINE-01 skins half) Summary

Threaded the authenticated `textures` GameProfile property (fetched by online-mode `hasJoined`) all the way from `AcceptPlayer` to the `PlayerInfoUpdate(ADD_PLAYER)` tab-list wire, so SELF and every OTHER player render the real authenticated skin instead of Steve/Alex. Closed the two MISSING surfaces the research's skins addendum found: `AcceptPlayer` DROPPED the accepted `properties`, and `playerInfoEntriesEncoder.WriteTo` hardcoded `pk.VarInt(0)` for the property count.

## What was done

**Task 1 — wire the property propagation (commit e1a616c4):**
- `server/tick.go`: added `properties []user.Property` to the `tickPlayer` struct (next to `name`/`uuid`/`entityID`), with a doc comment; added the `yggdrasil/user` import.
- `server/gameplay_tick.go`: the `tickPlayer` literal now sets `properties: properties` (the already-accepted-but-dropped `AcceptPlayer` param); the `bootstrapParams` at the `sendPlayBootstrap` call site now sets `properties: properties`; replaced the stale "accepted but not yet consumed" doc comment. `AcceptPlayer`'s signature is unchanged.
- `server/play_join.go`:
  - `writePlayerInfoUpdateAdd` gained a `properties []user.Property` parameter.
  - `playerInfoEntriesEncoder` gained a `properties` field, set from the new param.
  - `WriteTo` replaced `write(pk.VarInt(0))` (the GAME_PROFILE_PROPERTIES count) with `write(pk.VarInt(int32(len(e.properties))))` + a loop calling `write(prop)` per property (`user.Property` implements `pk.FieldEncoder` via `WriteTo`).
  - `bootstrapParams` gained a `properties` field; the self-add now passes `params.properties` (NOT an empty slice).
  - jar-citation comment added (`ClientboundPlayerInfoUpdatePacket$Action.ADD_PLAYER` + `ByteBufCodecs$32` / `Property.STREAM_CODEC`).
- `server/player_visibility.go`: `broadcastPlayerInfoAdd` passes `joiner.properties`; `sendExistingPlayersTo` passes `other.properties`.
- Migrated the two existing 3-arg test callers (`play_join_test.go:356`, `play_join_capture_test.go:197`) to the 4-arg signature (pass `nil` = offline, count 0).

**Task 2 — strict round-trip test (commit c17e1e14):**
- `TestAddPlayerSkinProperties` (online): builds an ADD_PLAYER with a single signed `textures` property, STRICT-decodes the bytes in exact jar wire order, asserting count=1, name="textures", value match, `hasSignature=true` + the signature String, then gameMode + listed, and zero trailing bytes. Decodes the property fields EXPLICITLY (not via `user.Property.ReadFrom`) so it pins the exact bytes, not a self-consistent round-trip.
- `TestAddPlayerNoPropertiesOffline` (offline): nil properties -> count 0, no signature bytes leak, trailing gameMode/listed still decode, zero trailing bytes — locks the byte-identical offline default.

## Callers of writePlayerInfoUpdateAdd updated

| Caller | File:Line | Properties passed |
|--------|-----------|-------------------|
| self-add (bootstrap) | server/play_join.go (sendPlayBootstrap) | `params.properties` (joiner's own skin) |
| broadcastPlayerInfoAdd | server/player_visibility.go | `joiner.properties` (joiner skin -> others) |
| sendExistingPlayersTo | server/player_visibility.go | `other.properties` (existing skins -> joiner) |
| TestPlayerInfoUpdateWire | server/play_join_test.go | `nil` (offline test) |
| TestPlayBytesVsVanillaCapture | server/play_join_capture_test.go | `nil` (offline test) |

## Jar verification (1:1 mandate)

Verified against `temp/cache/26.2-inner.jar` via `javap -c -p`:
- `ClientboundPlayerInfoUpdatePacket$Action.ADD_PLAYER` writes `GameProfile.name()` (String), then `ByteBufCodecs.GAME_PROFILE_PROPERTIES` over `GameProfile.properties()`.
- `ByteBufCodecs$32.encode` (the GAME_PROFILE_PROPERTIES codec) writes `PropertyMap.size()` as the VarInt count, then per `Property`: `Utf8String.write(name)`, `Utf8String.write(value)`, `FriendlyByteBuf.writeNullable(signature)` = a present-Boolean + the signature String when non-null.
- **Confirmed `user.Property.WriteTo` already matches this** — it writes `pk.String(Name)`, `pk.String(Value)`, `pk.Option[pk.String]{Has: Signature != ""}`, which is exactly `Property.STREAM_CODEC` / `writeNullable`. So NO new per-property codec was written; the loop reuses the existing jar-faithful codec.

## Verification gates (all green)

- `CGO_ENABLED=0 go build ./...` — exit 0 (no new dependency; no `import "C"`).
- `go vet ./server/` — clean.
- `go test ./server/` — green (the 2 new tests + all existing play_join / player_visibility / suite tests; known-flaky `TestTickAIDrivesMobs` passed).
- Docker `-race` (`golang:1.26`, host CGO=0) over `./server/` — clean (7.7s).

## Offline regression

Offline-mode -> `properties` is nil -> `playerInfoEntriesEncoder` writes count 0 -> Steve/Alex, byte-identical to before. Locked by `TestAddPlayerNoPropertiesOffline` and the unchanged `TestPlayerInfoUpdateWire`.

## Live verification note (out-of-band)

The automated strict round-trip seals the exact bytes the encoder emits. The final out-of-band confirmation is a real premium client seeing another premium player's real skin in online-mode (`--online-mode`), and a capture-diff of the ADD_PLAYER frame against a vanilla 26.2 client seals the exact optional-signature bytes (per RESEARCH §ADDENDUM pitfall). The vanilla capture-diff fixtures for PlayerInfoUpdate are not present in the repo (the `TestPlayBytesVsVanillaCapture` subtests SKIP when absent — pre-existing, see 05-CAPTURE-DIFF.md to re-capture); they are the authority for the exact-byte seal when captured.

## Relationship to 18-01

The crypto/auth-wire half (the EncryptionRequest `shouldAuthenticate` boolean, the `--online-mode` flag, the authDigest tests) is owned by plan 18-01 on DISJOINT files (`auth.go`, `cmd/sulfur/main.go`, `login.go`). 18-02 needs none of it to compile or test — both ran in Wave 1. 18-02 consumes only the already-present `properties []user.Property` that `AcceptLogin`/`AcceptPlayer` thread in (offline: nil).

## Deviations from Plan

None - plan executed exactly as written. (No auto-fixes needed; all six target files changed as specified, both test callers migrated, both gates green on first run.)

## Self-Check: PASSED

- server/play_join.go — FOUND
- server/player_visibility.go — FOUND
- server/gameplay_tick.go — FOUND
- server/tick.go — FOUND
- server/play_join_test.go — FOUND
- server/play_join_capture_test.go — FOUND
- commit e1a616c4 — FOUND
- commit c17e1e14 — FOUND
