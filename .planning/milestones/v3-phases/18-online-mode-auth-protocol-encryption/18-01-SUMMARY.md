---
phase: 18-online-mode-auth-protocol-encryption
plan: 01
subsystem: auth
tags: [rsa, pkcs1v15, aes, cfb8, sha1, yggdrasil, online-mode, protocol-776, encryption]

# Dependency graph
requires:
  - phase: 02-net-login-config
    provides: "MojangLoginHandler + AcceptLogin online/offline branch, ClientboundLoginFinished sessionId, the net.Conn SetCipher seam"
  - phase: 05-first-playable
    provides: "the in-memory net.Pipe test harness (pipe_test.go newPipe) reused for the online-handshake integration test"
provides:
  - "776-faithful EncryptionRequest: the 4-field ClientboundHelloPacket wire (serverId, publicKey, challenge, shouldAuthenticate=true) a real 26.2 client decodes"
  - "4-byte challenge (Ints.toByteArray(nextInt())) replacing the 16-byte token"
  - "URL-encoded hasJoined query via net/url + an injectable sessionServerURL test seam"
  - "--online-mode operator flag (+ SULFUR_ONLINE_MODE env) threaded into MojangLoginHandler.OnlineMode"
  - "Notchian authDigest vectors (incl. the negative jeb_ path) + an offline online-handshake integration test (RSA/PKCS1v15 -> CFB8 -> digest), sessionserver stubbed"
affects: [18-02 skins-add-player, online-mode-visual-gate]

# Tech tracking
tech-stack:
  added: []  # pure stdlib (crypto/aes, crypto/rsa, crypto/x509, crypto/sha1, net/url, net/http/httptest); CGO_ENABLED=0 holds, zero new deps
  patterns:
    - "Injectable base-URL package var (sessionServerURL) as the offline test seam — production never reassigns it, so the live endpoint stays byte-identical to vanilla"
    - "Strict ordered field-decode (not a self-consistent Go round-trip) as the regression guard for a missing trailing wire field (RESEARCH Pitfall 3)"

key-files:
  created:
    - "server/auth/wire_test.go - TestEncryptionRequestWire: strict 4-field decode guard for the shouldAuthenticate boolean"
    - "server/auth/handshake_test.go - TestOnlineHandshake: end-to-end online login over net.Pipe with the sessionserver stubbed"
  modified:
    - "server/auth/auth.go - the three 1:1 crypto-wire deltas + the sessionServerURL seam"
    - "cmd/sulfur/main.go - the --online-mode flag threaded through newServer into the handler"
    - "server/auth/auth_test.go - TestAuthDigest Notchian vectors"

key-decisions:
  - "EncryptionRequest is now the jar-exact 4-field ClientboundHelloPacket.write wire: String(serverId), ByteArray(publicKey), ByteArray(challenge), Boolean(shouldAuthenticate=true). The boolean is always true because the server only sends Hello in online-mode (handleHello passes iconst_1)."
  - "newServer flag-threading used option (a): added an onlineMode bool param to newServer(gameplay, onlineMode) — explicit, no type assertion on srv.LoginHandler."
  - "authentication() gained a sessionServerURL package var (test seam, ~3 lines) so the integration test stubs hasJoined via httptest; this crosses NO faithful crypto logic (DecryptPKCS1v15/CFB8/authDigest/ordering untouched)."
  - "RSA stays PKCS#1 v1.5 + 1024-bit; AES-128/CFB8, authDigest, the two-array EncryptionResponse, and the cipher-then-auth ordering are all FAITHFUL and were NOT touched (RESEARCH verdict table)."

patterns-established:
  - "Injectable-base-URL test seam: a package var defaulting to the FAITHFUL production endpoint, repointed only by tests, keeps CI offline without changing live behavior."
  - "Wire-fidelity regression guard: decode each field explicitly in the jar write order so a missing/extra trailing field fails the test (vs a symmetric round-trip that would hide it)."

requirements-completed: [ONLINE-01, ONLINE-02]

# Metrics
duration: 18min
completed: 2026-06-26
---

# Phase 18 Plan 01: Online-mode auth + protocol encryption Summary

**Closed the three 1:1 deltas in the inherited login-crypto path — the missing 776 EncryptionRequest `shouldAuthenticate` boolean (the only hard blocker against a real 26.2 client), the 16->4-byte challenge, and the un-encoded hasJoined query — then wired the `--online-mode` operator flag and locked the digest + encrypted handshake with offline tests.**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-06-26T22:13:33Z (approx)
- **Completed:** 2026-06-26
- **Tasks:** 3
- **Files modified:** 5 (2 created, 3 modified)

## Accomplishments
- EncryptionRequest now writes the jar-exact 4-field wire with `shouldAuthenticate=true` — a real 26.2 client can decode it (previously desynced on the missing trailing boolean).
- The challenge is the strict 1:1 4-byte value; the hasJoined query is correctly percent-encoded via `net/url`.
- `--online-mode` (default false, + `SULFUR_ONLINE_MODE` env) toggles authenticated/encrypted logins; offline (default) is byte-identical to before.
- The Notchian authDigest vectors (incl. the negative `jeb_` twos-complement path) and the full encrypted handshake (4-field EncryptionRequest -> RSA/PKCS1v15 decrypt -> CFB8 -> digest) are test-locked with the sessionserver stubbed (CI stays offline).

## Task Commits

Each task was committed atomically:

1. **Task 1: Fix the three 1:1 crypto-wire deltas** - `b163863e` (fix) — auth.go deltas + TestEncryptionRequestWire (RED-confirmed before the fix, GREEN after)
2. **Task 2: Wire the online-mode operator flag** - `afe923e1` (feat) — flag + env, threaded via newServer(gameplay, onlineMode)
3. **Task 3: Lock the digest + online handshake with tests** - `c11014a8` (test) — TestAuthDigest + TestOnlineHandshake

**Plan metadata:** (this commit) docs: complete plan

## Files Created/Modified
- `server/auth/auth.go` - DELTA #1 `pk.Boolean(true)` 4th EncryptionRequest field; DELTA #3 `verifyTokenLen` 16->4; DELTA #4 `net/url`-built hasJoined query + injectable `sessionServerURL`; all with javap citations. The faithful surfaces (encryptionResponse, authDigest, twosComplement, the CFB8/SetCipher ordering) were left untouched.
- `cmd/sulfur/main.go` - `--online-mode` flag (default false) + `SULFUR_ONLINE_MODE` env, threaded through `newServer(gameplay, onlineMode)` into the `MojangLoginHandler` literal, replacing the hardcoded `OnlineMode: false`; listen log now reports `online-mode=%v`.
- `server/auth/auth_test.go` - `TestAuthDigest` table over the three canonical vectors, asserting the `jeb_` negative path.
- `server/auth/wire_test.go` (new) - `TestEncryptionRequestWire` strict-decodes the 4 fields over a pipe (the DELTA #1 regression guard).
- `server/auth/handshake_test.go` (new) - `TestOnlineHandshake` drives `Encrypt` over `net.Pipe`: strict-decodes the 4-field request, RSA/PKCS1v15-encrypts a known shared secret + echoes the challenge, sends the EncryptionResponse, flips the client to CFB8, and asserts the hasJoined `serverId` equals `authDigest("", secret, pubkey)`. The sessionserver GET is stubbed via `httptest` + the `sessionServerURL` seam.

## Fixed EncryptionRequest wire (the exact shape)

Per `net.minecraft.network.protocol.login.ClientboundHelloPacket.write` (javap-verified):

1. `writeUtf(serverId)`      -> `pk.String("")`
2. `writeByteArray(publicKey)` -> `pk.ByteArray(publicKey)` (X.509 SubjectPublicKeyInfo DER)
3. `writeByteArray(challenge)` -> `pk.ByteArray(verifyToken)` (4 bytes = `Ints.toByteArray(nextInt())`)
4. `writeBoolean(shouldAuthenticate)` -> `pk.Boolean(true)`  ← the previously-missing 4th field

`shouldAuthenticate` is unconditionally `true` because the server only ever sends the Hello in online-mode (`ServerLoginPacketListenerImpl.handleHello` constructs the packet with `iconst_1`).

## Decisions Made
- **newServer threading = option (a)** (param, not post-construction set): `newServer(gameplay server.GamePlay, onlineMode bool)`. Explicit and avoids a `srv.LoginHandler.(*server.MojangLoginHandler)` type assertion.
- **authentication test-seam:** a `sessionServerURL` package var (defaulting to the FAITHFUL prod endpoint) was added so the integration test can repoint it to an httptest server. ~3 lines; it crosses no faithful crypto logic — `DecryptPKCS1v15`, CFB8, `authDigest`, and the cipher-then-auth ordering are byte-identical.
- **Kept the faithful surfaces:** PKCS#1 v1.5 (not OAEP), 1024-bit RSA (not 2048), AES-128/CFB8 with IV=sharedSecret, the two-array EncryptionResponse, and the SetCipher-before-auth ordering were all left exactly as inherited (RESEARCH verdict table marks them FAITHFUL; Pitfalls 1/2/4).

## Deviations from Plan

None - plan executed exactly as written. The three deltas, the flag (option a), the test-seam (option a), and both test files were implemented per the task specs; all acceptance criteria met.

## Issues Encountered
- RED confirmation for Task 1: `TestEncryptionRequestWire` failed with `scanning packet field[3] error: EOF` before the boolean was added — exactly the desync a real client would hit — then passed after the delta. This validated the strict-decode guard catches the missing-boolean class (RESEARCH Pitfall 3).
- The initial `TestOnlineHandshake` digest assertion was tautological (both sides computed the same `authDigest` call); strengthened it to assert the `serverId` the server actually sent to the stubbed hasJoined equals the independently-computed digest, proving the exchanged secret keyed the digest.

## Verification

- `CGO_ENABLED=0 go build ./...` exits 0 (no new dependency; pure stdlib crypto).
- `go vet ./server/auth/ ./cmd/sulfur/ ./server/` clean.
- `go test ./server/auth/ ./server/` passes — `TestAuthDigest`, `TestOnlineHandshake`, `TestEncryptionRequestWire`, and the existing `TestResp`/`TestOfflineLogin` all green.
- `grep import "C"` across `git ls-files '*.go'` stays 0 (CGO_ENABLED=0 invariant).
- Offline default path unchanged: with the flag unset, `AcceptLogin` still takes the `offline.NameToUUID` branch (no `auth.Encrypt`).

## Live-client visual verification (out-of-band, not automatable in CI)

CI stubs the sessionserver, so the authoritative confirmation that a real premium 26.2 client completes an encrypted+authenticated login is a manual out-of-band pass: run `sulfur --online-mode` and log in with a real Mojang/Microsoft account. This mirrors the `autonomous:false` visual-gate pattern used in prior phases. The automated tests lock the wire shape, the digest, and the crypto round-trip; the live login is the operator gate.

## Next Phase Readiness
- The crypto/auth wire is closed for ONLINE-02 and the ONLINE-01 flag. A real client can complete an encrypted, authenticated login when `--online-mode` is set; offline stays the byte-identical default.
- **Skins propagation (ONLINE-01's ADD_PLAYER skin visibility) is owned by the parallel plan 18-02** on disjoint files (`server/play_join.go` playerInfoEntriesEncoder, `server/player_visibility.go`, `server/gameplay_tick.go` AcceptPlayer carry). The fetched `Resp.Properties` (textures + signature) already return from `auth.Encrypt`; 18-02 carries them onto the player and writes the per-property loop in ADD_PLAYER. This plan does NOT touch those files.

## Self-Check: PASSED

- Created files verified on disk: `server/auth/wire_test.go`, `server/auth/handshake_test.go`, `18-01-SUMMARY.md`.
- Task commits verified in git: `b163863e` (fix), `afe923e1` (feat), `c11014a8` (test).

---
*Phase: 18-online-mode-auth-protocol-encryption*
*Completed: 2026-06-26*
