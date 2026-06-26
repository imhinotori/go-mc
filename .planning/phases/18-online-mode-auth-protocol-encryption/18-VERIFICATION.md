---
phase: 18-online-mode-auth-protocol-encryption
verified: 2026-06-26T00:00:00Z
status: passed
score: 4/4 must-haves verified
overrides_applied: 0
operator_gate: "PARTIAL-PASS 2026-06-26 — operator ran `sulfur --online-mode` on localhost:25565; an OFFLINE (non-premium) client was correctly REJECTED with client-side 'Invalid session' (server log: EncryptionRequest sent → `login error: EOF` as the offline client aborted at the auth step). This confirms the encryption handshake + the online-mode auth gate run and enforce premium-only login exactly as vanilla. The remaining sub-item (a successful PREMIUM login + cross-player real-skin render) still needs two real Mojang accounts and is the only out-of-band item — code + all wire/crypto/property bytes are locked by green automated tests."
human_verification:
  - test: "Run `sulfur --online-mode` and complete a login with a REAL premium Mojang/Microsoft 26.2 client; have a SECOND premium client join."
    expected: "Both clients complete the encrypted+authenticated handshake (no decode desync, no kick), get their real Mojang UUIDs, and each renders the OTHER player's real authenticated skin (not Steve/Alex) in the world and tab list."
    why_human: "CI stubs sessionserver.mojang.com via httptest, so a live premium-account login + cross-player skin render against the real Yggdrasil endpoint and a real client renderer is an out-of-band visual/network gate that cannot be exercised programmatically. All wire shapes, the digest, the crypto round-trip, and the property bytes ARE locked by green automated tests."
    operator_partial: "2026-06-26 — offline-client REJECTION verified live (Invalid session). Successful premium login + skin render deferred (needs real Mojang accounts) — accepted as out-of-band debt, like the Phase 17 visual gate."
---

# Phase 18: Online-mode — auth + protocol encryption Verification Report

**Phase Goal:** Sulfur runs in online-mode — authenticated, encrypted logins with real Mojang/Microsoft UUIDs, skins, and ownership verification — behind an `online-mode` config flag (offline remains the default for local dev).
**Verified:** 2026-06-26
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (= ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | EncryptionRequest/EncryptionResponse ported: RSA PKCS#1 v1.5 (NOT OAEP) exchange of shared secret + verify token, + trailing `shouldAuthenticate` boolean on the 776 EncryptionRequest | ✓ VERIFIED | `auth.go:100-106` writes 4 fields `pk.String("")`, `pk.ByteArray(publicKey)`, `pk.ByteArray(verifyToken)`, `pk.Boolean(true)`. `auth.go:128,136` use `rsa.DecryptPKCS1v15` (not OAEP). `verifyTokenLen=4` (`auth.go:43`). 1024-bit key `login.go:97`. javap `ClientboundHelloPacket.write` = writeUtf/writeByteArray/writeByteArray/writeBoolean (exact order match); `readUtf(20)`. javap `Crypt.setupCipher` = `Cipher.getInstance(key.getAlgorithm())` → bare `"RSA"` = `RSA/ECB/PKCS1Padding` (PKCS#1 v1.5, NOT OAEP); `Crypt.generateKeyPair` = `sipush 1024`. Tests `TestEncryptionRequestWire`, `TestOnlineHandshake` PASS. |
| 2 | AES-128/CFB8 stream encryption wraps the connection after handshake; CFB8 hand-rolled over stdlib AES, no new dep, CGO=0 | ✓ VERIFIED | `auth.go:71-79` `aes.NewCipher(SharedSecret)` then `conn.SetCipher(CFB8.NewCFB8Encrypt/Decrypt(block, SharedSecret))` BEFORE `authentication` (faithful ordering, lines 76-81). `net/CFB8/cfb8.go` imports only `crypto/cipher`,`crypto/subtle`,`unsafe` — hand-rolled, no new dep. javap `Crypt.getCipher` = `"AES/CFB8/NoPadding"`. `CGO_ENABLED=0 go build ./...` exit 0; zero `import "C"`; go.mod/go.sum unchanged. `./net/CFB8/` tests PASS. |
| 3 | Yggdrasil hasJoined → real UUID + skin properties; authenticated skin reaches SELF and OTHER players via tab-list ADD_PLAYER | ✓ VERIFIED | `auth.go:152-174` `authentication()` uses `net/url`-encoded query to `sessionserver.../hasJoined`; `Resp{Name,ID,Properties}`. `login.go:148-150` online branch sets name/id/properties from resp. `server.go:107,135` threads properties AcceptLogin→AcceptPlayer. `gameplay_tick.go:272,329` stores `properties` on tickPlayer + bootstrapParams (was DROPPED). `play_join.go:397-404` writes `VarInt(len(properties))` + per-property loop (was hardcoded `VarInt(0)`). `play_join.go:593` self-add passes `params.properties`. `player_visibility.go:92` broadcast passes `joiner.properties`; `:114` existing-players pass `other.properties`. javap `ByteBufCodecs$32.encode` = size() count + per-Property name/value/writeNullable(signature) — matches `user.Property.WriteTo` (String,String,Option). Tests `TestAddPlayerSkinProperties` PASS. |
| 4 | `online-mode` config flag toggles auth/encryption; offline (default) is unchanged/byte-identical | ✓ VERIFIED | `main.go:118` `flag.Bool("online-mode", false, ...)` default false; `:124` `\|\| SULFUR_ONLINE_MODE==1`; `:262` `newServer(gp, online)`; `:103` `OnlineMode: onlineMode`. `login.go:136` gates `auth.Encrypt` vs `:153` `offline.NameToUUID`; offline leaves `properties` nil. `play_join.go:397` nil properties → count 0. Test `TestAddPlayerNoPropertiesOffline` PASS (count 0, no signature leak). |

**Score:** 4/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `server/auth/auth.go` | 4-field EncryptionRequest, PKCS1v15, 4-byte challenge, url-encoded hasJoined | ✓ VERIFIED | `pk.Boolean(true)` present; `verifyTokenLen=4`; `net/url` query; DecryptPKCS1v15 |
| `cmd/sulfur/main.go` | online-mode flag → MojangLoginHandler.OnlineMode | ✓ VERIFIED | flag default false, threaded via newServer, replaces hardcoded false |
| `server/login.go` | OnlineMode branch gates Encrypt, 1024-bit key | ✓ VERIFIED | `:97` GenerateKey 1024; `:136-150` online branch carries properties |
| `net/CFB8/cfb8.go` | hand-rolled CFB8, stdlib only | ✓ VERIFIED | crypto/cipher+subtle+unsafe only |
| `server/play_join.go` | property count+loop (no VarInt(0)) | ✓ VERIFIED | `:397-404`; self-add `:593` |
| `server/player_visibility.go` | both broadcasts pass properties | ✓ VERIFIED | `:92` joiner, `:114` other |
| `server/gameplay_tick.go` | AcceptPlayer stores properties | ✓ VERIFIED | `:272`, `:329` (was dropped) |
| `server/tick.go` | tickPlayer.properties field | ✓ VERIFIED | `:325` |
| test files | digest vectors, wire guard, handshake, skin round-trip, offline count=0 | ✓ VERIFIED | TestAuthDigest/Notch/jeb_/simon, TestEncryptionRequestWire, TestOnlineHandshake, TestAddPlayerSkinProperties, TestAddPlayerNoPropertiesOffline all PASS |

### Key Link Verification

| From | To | Via | Status |
|------|-----|-----|--------|
| main.go online-mode flag | MojangLoginHandler.OnlineMode | newServer(gp, online) → `OnlineMode: onlineMode` | ✓ WIRED |
| auth.go encryptionRequest | ClientboundHelloPacket trailing boolean | `pk.Boolean(true)` after verify token | ✓ WIRED |
| AcceptLogin properties | AcceptPlayer → tickPlayer | server.go:107→135, gameplay_tick.go:329 | ✓ WIRED |
| playerInfoEntriesEncoder | per-property loop | `VarInt(len)` + `prop.WriteTo` | ✓ WIRED |
| broadcastPlayerInfoAdd / sendExistingPlayersTo | writePlayerInfoUpdateAdd(...properties) | joiner.properties / other.properties | ✓ WIRED |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build (CGO off) | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Vet | `go vet ./server/ ./server/auth/ ./cmd/sulfur/` | clean | ✓ PASS |
| Tests | `go test -count=1 ./server/ ./server/auth/ ./net/CFB8/` | ok all 3 | ✓ PASS |
| Named phase tests | digest/wire/handshake/skin/offline | all PASS | ✓ PASS |
| No new dep | `git diff go.mod go.sum` | no change | ✓ PASS |
| No CGO | grep `import "C"` | 0 matches | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
|-------------|-------------|--------|----------|
| ONLINE-01 | 18-01 (flag), 18-02 (skins) | ✓ SATISFIED | hasJoined→UUID+skin; properties propagate to ADD_PLAYER self+others; online-mode flag |
| ONLINE-02 | 18-01 | ✓ SATISFIED | EncryptionRequest 4-field PKCS1v15 + shouldAuthenticate boolean; AES-128/CFB8; no new dep |

### Anti-Patterns Found

None in modified surfaces. Two "placeholder" string matches in `play_join.go:513,554` are doc comments about teleport-ID constants — unrelated to this phase, not stubs.

### 1:1 Mandate Spot-Check (javap vs temp/cache/26.2-inner.jar)

- `ClientboundHelloPacket.write` → writeUtf(serverId)/writeByteArray(publicKey)/writeByteArray(challenge)/writeBoolean(shouldAuthenticate). Matches auth.go:100-106 EXACTLY. `readUtf(20)` confirms serverId max-len.
- `Crypt.setupCipher` = `Cipher.getInstance("RSA")` = `RSA/ECB/PKCS1Padding` (PKCS#1 v1.5, NOT OAEP) → matches DecryptPKCS1v15. `Crypt.generateKeyPair` = 1024-bit → matches login.go:97. `Crypt.getCipher` = `AES/CFB8/NoPadding` → matches hand-rolled CFB8.
- `ByteBufCodecs$32.encode` (GAME_PROFILE_PROPERTIES) = PropertyMap.size() count + per-Property name()/value()/writeNullable(signature) → matches play_join.go loop + user.Property.WriteTo (String,String,Option). No deviation.

### Human Verification Required

1. **Live premium-client online-mode login + cross-player skin render** — Run `sulfur --online-mode`, log in with a real premium 26.2 client, join a second premium client.
   - Expected: both complete the encrypted+authenticated handshake, receive real Mojang UUIDs, and each renders the other's real skin (not Steve/Alex) in-world and in the tab list.
   - Why human: CI stubs sessionserver via httptest; a live Yggdrasil round-trip + real client renderer is an out-of-band visual/network gate. All wire shapes, digest, crypto round-trip, and property bytes are already locked by green automated tests.

### Gaps Summary

No code gaps. All four ROADMAP success criteria are delivered and faithful to the 26.2 jar (javap-verified on ClientboundHelloPacket, Crypt, and ByteBufCodecs$32). All gates green (build CGO=0, vet, tests), no new dependency, no CGO. The sole open item is the inherently out-of-band premium-client visual confirmation — status `human_needed`, not `gaps_found`.

---

_Verified: 2026-06-26_
_Verifier: Claude (gsd-verifier)_
