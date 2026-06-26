# Phase 18: Online-mode — auth + protocol encryption - Research

**Researched:** 2026-06-26
**Domain:** Minecraft Java 26.2 (protocol 776) login encryption + Yggdrasil session auth, in Go
**Confidence:** HIGH (every protocol/crypto claim verified against `temp/cache/26.2-inner.jar` bytecode via `javap -c -p`)

## Summary

Phase 18 is a **VERIFY-then-WIRE** phase, not a from-scratch build. ~90% of the machinery already exists in the inherited go-mc fork (`server/login.go`, `server/auth/auth.go`, `net/CFB8/cfb8.go`). I decompiled the four authoritative jar classes (`net.minecraft.util.Crypt`, `ClientboundHelloPacket`, `ServerboundKeyPacket`, `ServerLoginPacketListenerImpl`) plus authlib `YggdrasilMinecraftSessionService`, and audited every existing surface against the bytecode.

**Two real 1:1 framing/crypto resolutions came out of the bytecode:**
1. **RSA padding = PKCS#1 v1.5 (NOT OAEP).** `Crypt.cipherData` calls `Cipher.getInstance(Key.getAlgorithm())` → `getInstance("RSA")`, whose JCE default transformation is `RSA/ECB/PKCS1Padding`. The ROADMAP/REQUIREMENTS text saying "RSA-OAEP" is **WRONG**; the existing code's `rsa.DecryptPKCS1v15` is **FAITHFUL**. `[VERIFIED: javap net.minecraft.util.Crypt.cipherData/setupCipher]`
2. **`ClientboundHelloPacket` (EncryptionRequest) has a 4th wire field — a trailing `boolean shouldAuthenticate`** added in the 1.20.5 era. The existing `encryptionRequest()` sends only `serverId, publicKey, challenge` — it is **MISSING the boolean** and would desync a real 26.2 client. This is the single hard blocker. `[VERIFIED: javap ClientboundHelloPacket.write]`

**Primary recommendation:** Fix the two deviations above (add the `shouldAuthenticate=true` boolean to the EncryptionRequest write; keep PKCS1v15 and fix the ROADMAP text), wire `online-mode` from a flag into `MojangLoginHandler.OnlineMode`, add the missing test vectors (authDigest Notchian + a CFB8 round-trip already exists), and URL-encode the `hasJoined` query. Everything else (CFB8, authDigest, key size, cipher-then-auth ordering, two-array EncryptionResponse, hasJoined base URL, LoginFinished sessionId) is already FAITHFUL — do not touch it. NO new dependencies; pure stdlib; CGO_ENABLED=0 holds.

## Verdict: existing code 1:1 status

| Surface | Status | Jar citation / evidence |
|---------|--------|-------------------------|
| **EncryptionRequest wire** (`ClientboundHelloPacket`) | **DEVIATION (fix)** | `ClientboundHelloPacket.write`: order = `writeUtf(serverId)`, `writeByteArray(publicKey)`, `writeByteArray(challenge)`, **`writeBoolean(shouldAuthenticate)`**. Existing `auth.go:encryptionRequest` sends only the first 3 — **missing the trailing boolean**. Fix: append `pk.Boolean(true)`. `[VERIFIED: javap ClientboundHelloPacket]` |
| **EncryptionResponse wire** (`ServerboundKeyPacket`) | **FAITHFUL** | `ServerboundKeyPacket.<init>(FriendlyByteBuf)`: `readByteArray()`→`keybytes` (enc shared secret), `readByteArray()`→`encryptedChallenge`. No signed-nonce/message-sig variant. Existing reads `keyBytes` then `encryptedVerifyToken` — exact match. `[VERIFIED: javap ServerboundKeyPacket]` |
| **RSA padding** | **FAITHFUL** | `Crypt.cipherData` → `setupCipher(int, Key.getAlgorithm()="RSA", Key)` → `Cipher.getInstance("RSA")` = JCE default `RSA/ECB/PKCS1Padding` = PKCS#1 v1.5. Existing uses `rsa.DecryptPKCS1v15`. **OAEP is NOT used anywhere.** `[VERIFIED: javap net.minecraft.util.Crypt.cipherData/setupCipher/decryptUsingKey]` |
| **RSA key size** | **FAITHFUL** | `Crypt.generateKeyPair`: `KeyPairGenerator.getInstance("RSA"); init(1024)` (`sipush 1024`). Existing `getPrivateKey` uses `rsa.GenerateKey(rand.Reader, 1024)`. **Do NOT "upgrade" to 2048** — the wire/encoding assumes 1024-bit modulus. `[VERIFIED: javap net.minecraft.util.Crypt.generateKeyPair]` |
| **AES mode / CFB8** | **FAITHFUL** | `Crypt.getCipher`: `Cipher.getInstance("AES/CFB8/NoPadding")` with `IvParameterSpec(key.getEncoded())` — AES-128, CFB8, IV = the 16-byte shared secret itself. Existing `CFB8.NewCFB8Encrypt/Decrypt(block, SharedSecret)` uses sharedSecret as both key and IV. `[VERIFIED: javap net.minecraft.util.Crypt.getCipher]` |
| **authDigest (SHA-1 server hash)** | **FAITHFUL** | `Crypt.digestData(String serverId, PublicKey, SecretKey)` builds `byte[][]{ serverId.getBytes(ISO_8859_1), secretKey.getEncoded(), publicKey.getEncoded() }`, SHA-1 over them in that order; `handleKey` wraps result in `new BigInteger(bytes).toString(16)` (signed → the negative-hash hex). Existing `authDigest("", SharedSecret, publicKey)` writes serverId, sharedSecret, publicKey in the same order and does the twos-complement negative path. `[VERIFIED: javap Crypt.digestData + ServerLoginPacketListenerImpl.handleKey]` |
| **hasJoined URL** | **FAITHFUL (minor fix)** | authlib `YggdrasilMinecraftSessionService.hasJoinedServer`: base `https://sessionserver.mojang.com` + `/session/minecraft/hasJoined`, query params `username`, `serverId`, and `ip` **only when InetAddress != null** (i.e. preventProxyConnections). Existing GETs the right base + `username` + `serverId`. Fix: **URL-encode `username`** (defense; valid names are safe but the hash can be `-`-prefixed and is hex-only so safe — encode the username to be correct). `ip` param is OPTIONAL — omit unless preventProxyConnections is a future config. `[VERIFIED: javap authlib YggdrasilMinecraftSessionService.hasJoinedServer + YggdrasilEnvironment PROD host]` |
| **LoginFinished sessionId** | **FAITHFUL** | proto-776 `ClientboundLoginFinishedPacket` = GameProfile(UUID,name,properties) + trailing sessionId UUID. Existing `login.go` already writes `pk.UUID(id), pk.String(name), pk.Array(properties), pk.UUID(getSessionID())`. (Not re-verified this phase — was settled in a prior phase; flagged in code comment.) `[CITED: server/login.go:176-186 prior-phase port]` |
| **cipher-then-auth ordering** | **FAITHFUL** | `handleKey`: validate challenge → derive SecretKey → compute digest → `setEncryptionKey(cipher,cipher)` (AES ON) → **then** spawn thread calling `hasJoinedServer`. So encryption is enabled BEFORE auth. Existing `Encrypt()` calls `conn.SetCipher(...)` then `authentication(...)`. Exact match. `[VERIFIED: javap ServerLoginPacketListenerImpl.handleKey]` |
| **verify-token (challenge) length** | **DEVIATION (cosmetic)** | Vanilla challenge = `Ints.toByteArray(RandomSource.create().nextInt())` = **4 bytes**. Existing uses `verifyTokenLen = 16`. The client echoes whatever length the server sent, so 16 bytes round-trips fine and does NOT break a real client — but a strict 1:1 port is 4 bytes from one `nextInt()`. Recommend changing to 4 for faithfulness; LOW functional risk either way. `[VERIFIED: javap ServerLoginPacketListenerImpl <init> challenge field]` |

## User Constraints (from CLAUDE.md — no CONTEXT.md present)

This phase has no `*-CONTEXT.md`. The governing constraints are the CLAUDE.md mandate:
- **1:1 vanilla port is ABSOLUTE.** Every crypto/protocol op mirrors the 26.2 jar exactly. The ONLY permitted deviation is optimization that preserves identical observable behavior.
- **No new runtime deps; CGO_ENABLED=0 must hold.** Crypto is stdlib-only.
- **GSD workflow enforced** before edits.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| ONLINE-01 | Yggdrasil `hasJoined` session verification → real UUID/skins, behind `online-mode` flag | `auth.go:authentication` already GETs the correct base URL; `Resp` already parses id/name/properties (tested). Remaining: URL-encode username, wire the flag. Verified against authlib `hasJoinedServer`. |
| ONLINE-02 | EncryptionRequest/Response RSA (PKCS1v15, **not OAEP**) exchange of 16-byte shared secret + verify token, then AES-128/CFB8 on all subsequent packets | All present and FAITHFUL **except** the missing `shouldAuthenticate` boolean on the EncryptionRequest wire (hard fix) and the 4-vs-16-byte challenge (cosmetic). CFB8 hand-rolled + NIST-tested. |

## Standard Stack

**Pure Go standard library only. NO new dependencies. CGO_ENABLED=0 preserved.** `[VERIFIED: grep — no `import "C"` in repo; go.mod has no crypto deps]`

| Package | Purpose | Maps to vanilla |
|---------|---------|-----------------|
| `crypto/rsa` (`GenerateKey`, `DecryptPKCS1v15`) | 1024-bit keypair; decrypt shared secret + challenge | `Crypt.generateKeyPair` (RSA 1024), `Crypt.decryptUsingKey`/`decryptByteToSecretKey` (RSA/ECB/PKCS1Padding) |
| `crypto/x509` (`MarshalPKIXPublicKey`) | DER-encode the public key for the EncryptionRequest | `PublicKey.getEncoded()` returns X.509 SubjectPublicKeyInfo (PKIX) |
| `crypto/aes` (`NewCipher`) | AES-128 block for CFB8 | `Cipher.getInstance("AES/CFB8/NoPadding")` |
| `net/CFB8` (in-repo, hand-rolled) | CFB8 stream over the AES block | the `CFB8` half of the JCE transformation (Go stdlib dropped `cipher.CFB`) |
| `crypto/sha1` | the server-id digest | `Crypt.digestData` SHA-1 |
| `crypto/rand` | verify token + key gen entropy | `RandomSource`/SecureRandom |
| `net/http` + `encoding/json` | `hasJoined` GET + response parse | authlib `MinecraftClient.get` + `HasJoinedMinecraftServerResponse` |
| `math/big` (optional) | could replace the hand-rolled twos-complement to mirror `new BigInteger(bytes).toString(16)` exactly | `handleKey` uses `BigInteger.toString(16)` |

**Note on the digest:** vanilla literally does `new BigInteger(sha1Bytes).toString(16)`. The existing code reimplements that as manual twos-complement + hex-trim. Both produce identical output for the canonical vectors (see Test Vectors). The hand-rolled version is FAITHFUL in *output*; if you want structural fidelity you may switch to `new(big.Int).SetBytes`/signed-`SetBytes` semantics, but it is not required — keep what passes the vectors.

## Architecture Patterns

### Online-mode flag threading
```
flag/env (cmd/sulfur/main.go)
   └─> MojangLoginHandler.OnlineMode (bool)   [currently hardcoded false at main.go:100]
          └─> AcceptLogin: if OnlineMode { auth.Encrypt(conn, name, key) } else { offline.NameToUUID }
```
- main.go currently uses Go `flag` (no config-file struct yet). Add `flag.Bool("online-mode", false, ...)` (and/or env `SULFUR_ONLINE_MODE`), default **false** (offline = local-dev default per ROADMAP). Pass into the `MojangLoginHandler` literal at main.go:99-102.
- `EnforceSecureProfile` field already exists on the handler but is unused; 26.2 login does NOT require a profile public key in the login Hello (the 1.19 signed-profile path is gone — confirmed: `ServerboundHelloPacket.name()` is the only field read in `handleHello`). Leave `EnforceSecureProfile` wired to false; do not add a profile-key path.

### Login state machine (already correct order)
`ServerboundLoginHello(name,uuid)` → [if online] EncryptionRequest → EncryptionResponse → **enable AES** → hasJoined → SetCompression → ClientboundLoginFinished(profile+sessionId) → ServerboundLoginAcknowledged. The cipher is enabled mid-stream (after EncryptionResponse, before LoginFinished); all bytes from SetCompression onward are encrypted. Existing flow matches `handleKey` + `verifyLoginAndFinishConnectionSetup` ordering. `[VERIFIED: javap ServerLoginPacketListenerImpl]`

### `shouldAuthenticate` semantics
The server sends `shouldAuthenticate = true` when `MinecraftServer.usesAuthentication()` (i.e. online-mode). The client uses it to decide whether to contact `sessionserver` before replying. Since Sulfur only sends the EncryptionRequest in online-mode, **always send `true`** in `encryptionRequest()`. `[VERIFIED: javap ServerLoginPacketListenerImpl handleHello — iconst_1 passed to ClientboundHelloPacket.<init>(String,[B,[B,Z)]`

## Don't Hand-Roll

| Problem | Don't build | Use instead | Why |
|---------|-------------|-------------|-----|
| CFB8 stream | A new CFB8 | `net/CFB8` (already exists, NIST-vector tested) | Go stdlib dropped `cipher.CFB`; the repo's hand-rolled CFB8 is already correct and covered by `cfb8_test.go`. **Do not rewrite.** |
| RSA / AES / SHA-1 | Custom crypto | `crypto/rsa`, `crypto/aes`, `crypto/sha1` | Stdlib; FIPS-correct; matches JCE defaults. |
| Twos-complement hex digest | (it already works) | keep existing `authDigest`, or `math/big` BigInteger semantics | Both reproduce the Notchian vectors; no third path needed. |
| hasJoined HTTP/JSON | A client lib (authlib) | `net/http` + `encoding/json` into `Resp` | The response shape (`id`, `name`, `properties[]`) is already modeled + tested. |

**Key insight:** the entire crypto surface is stdlib + one tiny in-repo CFB8 that is already validated. The phase adds ZERO dependencies. The risk is not "building crypto" — it's **wire-framing fidelity** (the missing boolean) and **flag wiring**.

## Implementation Tasks (the actual remaining work)

1. **[HARD FIX — blocks real client] Add `shouldAuthenticate` to EncryptionRequest.** In `server/auth/auth.go:encryptionRequest`, append a `pk.Boolean(true)` after `verifyToken`. Confirm a `pk.Boolean` codec exists (it does in `net/packet`); wire order must be `String("") , ByteArray(publicKey), ByteArray(challenge), Boolean(true)`. `[VERIFIED: ClientboundHelloPacket.write]`
2. **[DOC FIX] Correct the "RSA-OAEP" wording** in REQUIREMENTS.md (ONLINE-02) and ROADMAP.md (Phase 18 success criteria #1) to "RSA / PKCS#1 v1.5". Keep `rsa.DecryptPKCS1v15` in code. `[VERIFIED: Crypt.cipherData]`
3. **[WIRE] online-mode flag.** Add `flag.Bool("online-mode", false, ...)` (+ optional `SULFUR_ONLINE_MODE` env) in `cmd/sulfur/main.go`; pass into `MojangLoginHandler{OnlineMode: *onlineMode, ...}` (replace the hardcoded `false` at main.go:100).
4. **[FIDELITY] Challenge length 16 → 4 bytes.** Change `verifyTokenLen` to 4 (or generate via one `int32`'s big-endian bytes to mirror `Ints.toByteArray(nextInt())`). Cosmetic for clients but required for strict 1:1. `[VERIFIED: ServerLoginPacketListenerImpl challenge field]`
5. **[CORRECTNESS] URL-encode the hasJoined query.** In `auth.go:authentication`, use `url.Values`/`url.QueryEscape` for `username` (and `serverId`). The serverId hash is hex/`-` (safe) but encode for correctness. Do NOT add the `ip` param unless a future preventProxyConnections config exists. `[VERIFIED: authlib buildQuery]`
6. **[TEST] authDigest known-vectors.** Add a unit test asserting the three Notchian vectors (see Test Vectors below) — currently absent. This locks the digest + negative-hash path.
7. **[TEST] online-login integration.** Extend `server/login_test.go` (currently offline-only) with an in-memory pipe test: drive the server side through EncryptionRequest → feed a crafted EncryptionResponse (client-side RSA-encrypt a known shared secret + echo the challenge with the server's pubkey) → assert the connection flips to CFB8 and the digest matches. Stub/skip the live `sessionserver` GET (inject the HTTP client or gate behind a build tag) so CI stays offline.
8. **[TEST] (already covered) CFB8 round-trip** — `net/CFB8/cfb8_test.go` exists with NIST F.3.7 vectors. No new CFB8 test needed; optionally add a Minecraft-style key=IV=sharedSecret round-trip assertion.

## Common Pitfalls

### Pitfall 1: "Upgrading" the RSA key to 2048
**What goes wrong:** larger modulus changes the encoded pubkey length + the encrypted block size; clients expect the vanilla 1024-bit handshake.
**Avoid:** keep `rsa.GenerateKey(rand.Reader, 1024)`. `[VERIFIED: Crypt.generateKeyPair sipush 1024]`

### Pitfall 2: Switching to OAEP because the ROADMAP says so
**What goes wrong:** OAEP-decrypting a PKCS1v15-encrypted blob fails; login dies with a decrypt error. The ROADMAP text is wrong, the jar is right.
**Avoid:** PKCS#1 v1.5 only.

### Pitfall 3: Forgetting the `shouldAuthenticate` boolean
**What goes wrong:** the real client `readBoolean()`s a byte that isn't there → reads into the next field / desync / disconnect. This is invisible to a naive Go-to-Go test that uses the same (buggy) writer/reader.
**Avoid:** add the boolean AND test against the exact `ClientboundHelloPacket.write` field order, not against a self-consistent Go round-trip.

### Pitfall 4: Encryption-enable timing
**What goes wrong:** if you enable CFB8 too early (before reading EncryptionResponse) or too late (after LoginFinished already went out plaintext-then-encrypted mid-packet), the stream corrupts.
**Avoid:** mirror `handleKey`: enable cipher immediately after a VALID EncryptionResponse, before any further writes. Existing order is correct.

### Pitfall 5: Negative-hash digest
**What goes wrong:** `serverId` hashes whose SHA-1 high bit is set must be rendered as a *negative* hex (twos-complement, leading `-`), not unsigned hex. Mojang rejects the wrong rendering.
**Avoid:** keep the existing negative-path; lock it with the `jeb_` vector (which IS negative).

## Test Vectors

Canonical Notchian `authDigest` (sha1 of `serverId=""` + that name's bytes-as-shared-input per the wiki vectors). These three lock the negative-hash + trim behavior:

| Input | Expected digest |
|-------|-----------------|
| `Notch` | `4ed1f46bbe04bc756bcb17c0c7ce3e4632f06a48` |
| `jeb_`  | `-7c9d5b0044c130109a5d7b5fb5c317c02b4e28c1` (negative path) |
| `simon` | `88e16a1019277b15d58faf0541e11910eb756f6` |

**Coverage status:** NOT currently tested. `server/auth/auth_test.go` only covers `Resp` JSON parsing (`jeb_` profile). Add a `TestAuthDigest` using these vectors. `[VERIFIED: grep — no authDigest test exists]`

CFB8 round-trip: **already covered** by `net/CFB8/cfb8_test.go` (NIST SP 800-38A F.3.7 vectors).

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary | Rationale |
|------------|--------------|-----------|-----------|
| RSA keypair gen / shared-secret decrypt | Server login handler | — | Server owns its keypair; matches `MinecraftServer.getKeyPair` |
| EncryptionRequest/Response framing | Net/packet codec | login handler | Wire layout is a codec concern; values supplied by login |
| CFB8 stream | Net/conn (cipher.Stream) | net/CFB8 | Encryption is a transport concern below packet logic |
| hasJoined session verify | server/auth (HTTP) | — | Out-of-band HTTPS to Mojang, off the wire |
| online-mode toggle | cmd/sulfur (config) | login handler | Operator config, threaded into the handler |

## Environment Availability

| Dependency | Required by | Available | Notes |
|------------|-------------|-----------|-------|
| Go 1.25+ stdlib crypto | all of ONLINE-02 | ✓ | go.mod `go 1.25.0` |
| `sessionserver.mojang.com` HTTPS | ONLINE-01 live test only | network-dependent | Unit/integration tests MUST stub this (inject HTTP client / build tag) so CI is offline. Live verification needs a real premium client (matches the `autonomous:false` visual-gate pattern). |
| `temp/cache/26.2-inner.jar` + `javap` | research/verification | ✓ | unobfuscated; used for this audit |

**No new external dependency. CGO_ENABLED=0 holds.**

## Assumptions Log

| # | Claim | Section | Risk if wrong |
|---|-------|---------|---------------|
| A1 | LoginFinished sessionId framing is correct for 776 (settled in a prior phase, not re-decompiled this round) | Verdict table | LOW — already working in offline mode; the trailing UUID is the only 776 addition and is present |
| A2 | A 16-byte challenge round-trips on a real client (only the 4-byte form is strictly faithful) | Verdict / Tasks | LOW — client echoes the server's bytes regardless of length; recommend 4 bytes anyway for fidelity |
| A3 | `pk.Boolean` codec exists and encodes a single 0/1 byte | Tasks #1 | LOW — standard in go-mc packet types; verify the exact type name when implementing |

## Sources

### Primary (HIGH — jar bytecode, this session)
- `javap -c -p net.minecraft.util.Crypt` — generateKeyPair (1024), getCipher (`AES/CFB8/NoPadding`), cipherData/setupCipher (`getInstance(Key.getAlgorithm())` = RSA/PKCS1v15), digestData (SHA-1, ISO_8859_1 serverId + secret + pubkey order)
- `javap -c -p net.minecraft.network.protocol.login.ClientboundHelloPacket` — write order: serverId, publicKey, challenge, **shouldAuthenticate(boolean)**
- `javap -c -p net.minecraft.network.protocol.login.ServerboundKeyPacket` — read order: keybytes, encryptedChallenge (two arrays, no signed-nonce)
- `javap -c -p net.minecraft.server.network.ServerLoginPacketListenerImpl` — handleKey (validate→derive→digest→setEncryptionKey→thread hasJoined), handleHello (sends shouldAuthenticate=true), challenge = `Ints.toByteArray(nextInt())` (4 bytes)
- `javap -c -p` authlib-9.0.75 `YggdrasilMinecraftSessionService.hasJoinedServer` + `YggdrasilEnvironment` — base `https://sessionserver.mojang.com`, path `/session/minecraft/hasJoined`, params username/serverId/optional ip

### Secondary (HIGH — repo)
- `server/login.go`, `server/auth/auth.go`, `net/CFB8/cfb8.go`, `net/conn.go`, `data/packetid/packetid.go` (ClientboundLoginHello=1, ServerboundLoginKey=1 — match vanilla login ordering), `net/packet/types.go` (String/ByteArray = VarInt-prefixed), `net/CFB8/cfb8_test.go`, `server/auth/auth_test.go`, `cmd/sulfur/main.go:99-102`

## Metadata

**Confidence breakdown:**
- RSA padding / key size / AES mode / digest order: **HIGH** — direct bytecode
- EncryptionRequest 4-field wire (shouldAuthenticate): **HIGH** — direct bytecode
- hasJoined URL + params: **HIGH** — authlib bytecode
- Challenge length (4 vs 16): **HIGH** on vanilla=4; MEDIUM on "16 is harmless" (very likely, standard client behavior)
- LoginFinished sessionId: **MEDIUM** — relied on prior-phase port, not re-decompiled

**Research date:** 2026-06-26
**Valid until:** stable until a 26.x protocol bump (re-verify against the jar on any version retarget)

---

## ADDENDUM (post-research, user-directed): Skin/texture propagation — the "online-mode also means skins" requirement

The user explicitly scoped: *online-mode no solo significa todo lo de la fase, pero también skins de los jugadores* — the authenticated **texture/skin properties must reach OTHER players**, not just be fetched. Auditing the codebase surfaced a real MISSING surface beyond the crypto wire:

### Skin data flow audit (jar surface = `ClientboundPlayerInfoUpdatePacket` ADD_PLAYER → `GameProfile.properties`)

| Stage | Status | Evidence |
|-------|--------|----------|
| **Fetch** skin from hasJoined | **FAITHFUL** | `auth.Encrypt` returns `Resp.Properties` (the `textures` property w/ value+signature) from the sessionserver JSON; `AcceptLogin` returns `properties []user.Property`. The texture IS fetched in online-mode. |
| **Carry** properties into the player | **MISSING** | `gameTick.AcceptPlayer` (`server/gameplay_tick.go:180`) accepts `properties []user.Property` but **DROPS it** — never stored on the tickPlayer/entity. |
| **Broadcast** properties in tab-list | **MISSING (hard)** | `playerInfoEntriesEncoder.WriteTo` (`server/play_join.go:367-396`) hardcodes `pk.VarInt(0)` for `GAME_PROFILE_PROPERTIES` count — sends ZERO properties. `writePlayerInfoUpdateAdd(id, name, gameMode)` has no properties param. So even with a fetched skin, every other client renders the default Steve/Alex. This is the skin-visibility hole. |

### Vanilla wire (jar-verified, the per-property shape to port)
`ClientboundPlayerInfoUpdatePacket` ADD_PLAYER writes `GameProfile`:
- `String name`
- `writeCollection(GAME_PROFILE_PROPERTIES)` — VarInt count, then per property:
  - `String name`   (e.g. `"textures"`)
  - `String value`  (base64 JSON blob)
  - `Boolean hasSignature` + (if true) `String signature`
  (= `ByteBufCodecs`/`Property.STREAM_CODEC` — name, value, Optional<signature>.)

The existing `user.Property` type already carries `Name`, `Value`, `Signature` (the yggdrasil/user package). The encoder just needs to write them.

### Remaining work (skins) — fold into the plan
7. **[WIRE]** `writePlayerInfoUpdateAdd` + `playerInfoEntriesEncoder` gain a `properties []user.Property` param; `WriteTo` replaces `pk.VarInt(0)` with a real count-prefixed property loop (name, value, optional signature) — jar-faithful per-property shape above. The self-add (`writePlayerInfoUpdateAdd` for the joining player's own list) and the cross-player broadcasts (`broadcastPlayerInfoAdd` / `sendExistingPlayersTo` in `server/player_visibility.go`) all route through this encoder, so ONE encoder fix covers self + others.
8. **[CARRY]** Store the authenticated `properties` on the tickPlayer/player entity in `AcceptPlayer` so `player_visibility.go` can read them when building each ADD_PLAYER. Offline-mode → empty properties (current behavior, Steve/Alex — unchanged, correct).
9. **[TEST]** ADD_PLAYER with a non-empty texture property round-trips (count=1, name="textures", value, signature present) → strict decode in a test (mirror the testbot strict validators); offline-mode still emits count=0.

### Pitfall (skins)
- The **signature** is REQUIRED for the client to accept/display a signed texture in online-mode (unsigned textures are dropped by a vanilla client when `enforce-secure-profile`/signed-chat context applies). Online-mode hasJoined returns the signature; send `hasSignature=true` + the signature String. Offline-mode has no signature → `hasSignature=false`. Port `Property.STREAM_CODEC`'s optional-signature exactly.
- Property ORDER and the Optional encoding must match `ByteBufCodecs.optional`/`Property.STREAM_CODEC` — verify the exact bytes against a capture-diff with a real client during the visual gate.
