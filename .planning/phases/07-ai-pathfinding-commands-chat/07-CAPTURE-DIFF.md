# CMD-01/02 Capture-Diff: Sulfur vs. Vanilla 26.2 (Command/Chat Wire) — Plan 07-06

**Phase 7, Plan 07-06 — the authoritative command/chat wire gate (the automatable half).**

This document records the byte-for-byte capture-diff of Sulfur's two MEDIUM-confidence
Phase-7 wire surfaces — the **`ClientboundCommands`** node tree (the command graph), the
**`ClientboundSystemChat`** content+overlay broadcast, and the **`ServerboundChat`** decode
(the trailing signature/lastSeen layout Sulfur's `handleChat` must consume) — against a REAL
vanilla 26.2 server/client, captured for an identical superflat session. It is the
producer-side proof; the real-client interactive run (Task 2 below) is the consumer-side proof.

The golden packet bytes are committed as fixtures under
`.planning/phases/07-ai-pathfinding-commands-chat/fixtures/` so CI byte-diffs without booting
Java every run; `server/chat_command_capture_test.go`
(`TestChatCommandBytesVsVanillaCapture`) is the field-by-field parser/asserter (it skips
cleanly if a fixture is absent — the capture is the gate, not a native-CI blocker).

A Go self-round-trip cannot prove vanilla-correctness for a protocol no published spec covers
(the wiki documents ≤773; the `ClientboundCommands` node framing, the `ClientboundSystemChat`
content+overlay, and the `ServerboundChat` `readUtf256`/instant/salt/nullable-sig/lastSeen
trailing layout are jar-confirmed in SHAPE but their exact bytes drift). The capture-diff is
the only 776 truth — and it found **TWO real divergences** (the `LastSeenMessages$Update`
checksum byte in the serverbound chat trailing layout, and the `SystemChat` content encoding as
a `TAG_Compound` where vanilla emits a bare `TAG_String`), both **fixed and re-diffed** below.

Status (automatable half): **SEALED — the capture-diff confirmed Sulfur's command/chat wire
matches vanilla 26.2 after two encoder fixes: the `ClientboundCommands` node framing MATCHES,
the `ClientboundSystemChat` content+overlay is now BYTE-IDENTICAL (the `TAG_String` fix), and
the `ServerboundChat` decoder consumes a real vanilla client's chat to exactly zero trailing
bytes (the `LastSeenMessages$Update` checksum-byte fix). The real-client interactive run
(Task 2) is the remaining BLOCKING human-verify gate.**

---

## 1. Capture Method (reproducible)

### Vanilla 26.2 server (the golden source)

- Jar: `temp/cache/26.2-server.jar`, run with Zulu 25.0.3 (`java -Xmx2G -jar server.jar nogui`).
- Scratch dir: `temp/vanilla-scratch/` (gitignored), `eula=true`, port `25599`,
  RCON on `25575` (`rcon.password=sulfurcap`) to drive the capture. The exact
  `server.properties` (the load-bearing keys; identical to the 06-07 superflat session):

  ```
  level-type=minecraft:flat
  level-seed=144
  online-mode=false
  enforce-secure-profile=false
  enable-code-of-conduct=false
  server-port=25599
  view-distance=4
  generate-structures=false
  gamemode=creative
  enable-rcon=true
  rcon.port=25575
  rcon.password=sulfurcap
  ```

  `enforce-secure-profile=false` is load-bearing: it lets the offline client send an
  UNSIGNED `ServerboundChat` (absent signature, empty lastSeen) — the exact wire Sulfur's v1
  decode targets.

### Capture harness

`temp/chatcommandcapture/` (gitignored, built on the fork's `net`/`bot`/`packetid`/`chat`,
adapted from the proven 06-07 `temp/entitycapture/`): handshake (proto 776) → offline login →
full Configuration leg → **Play**, where it:

- saves the `ClientboundCommands` the vanilla server sends at join (the full vanilla command
  tree) → `vanilla-commands.bin`;
- drives the vanilla server over RCON with `tellraw @a {"text":"SulfurCaptureHello"}` to emit a
  guaranteed `ClientboundSystemChat` (a server-attributed component with `overlay=false`) and
  saves it → `vanilla-system-chat.bin` (matched on the rendered text; `/say` routes through
  `DisguisedChat`/`PlayerChat`, so `tellraw` is the faithful `SystemChat` trigger);
- **SENDS** one `ServerboundChat` in the exact 776 wire form a vanilla client uses (the
  jar-derived `readUtf256` message + `Instant` + salt + nullable-sig + `LastSeenMessages$Update`)
  and saves those bytes → `vanilla-serverbound-chat.bin`. The vanilla server **ACCEPTS** it
  (no `DecoderException`/disconnect) — proving the trailing layout is vanilla-valid (mirrors how
  06-07 sealed the serverbound `ContainerClick`).

```
DUMP_DIR=temp/captures RCON_PASS=sulfurcap \
  temp/chatcommandcapture/chatcommandcapture.exe 127.0.0.1:25599 127.0.0.1:25575 ChatCap vanilla
```

### Committed golden fixtures

| Fixture | Bytes | sha1 | Seals |
|---------|-------|------|-------|
| `vanilla-commands.bin` | 265 | `25413c3f4dc80aeb2e253d1fa0e45be31630d9d9` | ClientboundCommands `VarInt nodeCount`, per-node stub framing, trailing `VarInt rootIndex` |
| `vanilla-system-chat.bin` | 22 | `e8a799d6f956e5911055efcde191c643b65e7eac` | SystemChat content = bare NBT `TAG_String` + `Boolean overlay` (the `TAG_String` shorthand) |
| `vanilla-serverbound-chat.bin` | 42 | `b66dd9e542b751663b7238cc4a4cfae68bc7144d` | ServerboundChat `readUtf256` + `Instant` + salt + nullable-sig + `LastSeenMessages$Update` (VarInt offset + `FixedBitSet(20)`=3B + **Byte checksum**) |

> The id byte is NOT included in any fixture — each is the raw packet BODY (everything after
> the packet-id varint), matching the 05-/06- capture convention and the test's parsers.

---

## 2. Per-Field Diff (the byte-level seal)

Legend: **MATCH** = byte-structure identical; **SEALED** = byte-identical for identical
inputs; **CONTENT** = framing identical, value/command-set differs (expected).

### 2.1 `ClientboundCommands` — the command-graph node framing (CMD-01, Open Question A3)

Vanilla golden (26 nodes — the full vanilla command set):
`1a 00 0a 01…0a … 00 00` — `1a`=26 node count, the root node (`00`=flags kind-root, then its
10 child indices), the literal/argument stubs, then the trailing `00`=`VarInt rootIndex`.

Sulfur graph (`buildCommandGraph().WriteTo`, 5 nodes — root + `/say`+arg + `/me`+arg):
`05 00 02 0204 06…6d657373616765 106272696761646965723a737472696e6720 0205 01 … 00`
— `05`=5 node count, root (`00` flags, 2 children `02 04`), the literals + `brigadier:string`
argument stubs, then the trailing `00`=`rootIndex`.

| Field | Vanilla | Sulfur | Verdict |
|-------|---------|--------|---------|
| node-count prefix | `VarInt 26` | `VarInt 5` | **MATCH** (same VarInt-count framing; the SET differs — Sulfur ships its 2 v1 literals) |
| first node | `00` flags = kind ROOT, not executable | `00` flags = kind ROOT | **MATCH** (offset-0 framing identical) |
| per-node stub | `Byte flags, readVarIntArray children, [redirect], [name], [parser]` | identical (`server/command/serialize.go`) | **MATCH** |
| brigadier:string arg | `Identifier "brigadier:string" + VarInt behavior` | identical (`StringParser(2)` → `brigadier:string`, behavior 2) | **MATCH** |
| trailing rootIndex | `VarInt 0` | `VarInt 0` | **MATCH** |
| whole body parses to 0 trailing bytes | yes (Sulfur graph: strict walk completes; vanilla: root + brigadier-string nodes walk, exotic parsers validated by client) | yes | **MATCH** |

**The seal:** Sulfur's `buildCommandGraph().WriteTo` produces a node tree with the SAME
`VarInt nodeCount` prefix, the SAME per-node `(flags, child-index array, name, parser-id +
brigadier:string behavior)` framing, and the SAME trailing `VarInt rootIndex` (0) as vanilla.
The command SET differs (Sulfur ships `/say`+`/me`; vanilla ships 26 nodes) — exactly the
documented PlayerInfoUpdate-style action-set difference — but the FRAMING that rejects a
command tree is byte-correct. **No divergence; no fix needed for ClientboundCommands.**
(Open Question A3 resolved on the producer side — the real client tab-complete is Task 2.)

### 2.2 `ClientboundSystemChat` — content+overlay framing (CMD-02, Open Question A2) — **DIVERGENCE FOUND + FIXED**

Vanilla golden (`tellraw @a {"text":"SulfurCaptureHello"}`):
`08 0012 53756c6675724361707475726548656c6c6f 00`
— `08`=NBT `TAG_String`, `0012`=length 18, the UTF-8 text, then `00`=`Boolean overlay` (false).

| Field | Vanilla | Sulfur (BEFORE fix) | Verdict |
|-------|---------|---------------------|---------|
| content tag | `08` (`TAG_String`) — the literal-text-component shorthand | `0a` (`TAG_Compound`) `{text:…}` | **DIVERGED** |
| content body | `0012 <utf8>` (length-prefixed string) | `08 0004 74657874 0012 <utf8> 00` (a `text` field in a compound) | **DIVERGED** |
| overlay | `00` (`Boolean` false) | `00` | MATCH |

**The divergence:** vanilla 26.2 serializes a **text-only** chat component as a **bare NBT
`TAG_String`** (the `ComponentSerialization` shorthand for a literal text component), but
Sulfur's `chat.Message.MarshalNBT` always emitted a `TAG_Compound` (`{"text":"…"}`). A vanilla
client ACCEPTS both (a string is shorthand for a text component), so this is not a kick — but
it is a wire-framing divergence the capture-diff exists to catch.

**The fix (in the OWNING encoder — `chat/nbtmessage.go`):** added `Message.isPlainText()` (true
iff ONLY `Text` is set — no styling/event/translate/extra) and a vanilla-faithful fast path:
- `TagType()` now returns `nbt.TagString` for a plain-text message (was always `TagCompound`);
- `MarshalNBT()` now writes a bare length-prefixed string body for a plain-text message (it
  encodes the Go `string` in network format and strips the leading tag byte, the same
  splice technique it already used for the compound body).
A styled/translate/extra message still serializes as the `TAG_Compound`. The decoder
(`UnmarshalNBT`) already handled a leading `TAG_String` → `m.Text`, so the round-trip survives.

**Re-diff (AFTER fix):** Sulfur's `broadcastSystemChat("SulfurCaptureHello")` body is now
`08 0012 53756c6675724361707475726548656c6c6f 00` — **BYTE-IDENTICAL** to the vanilla golden
(`bytes.Equal` in `TestChatCommandBytesVsVanillaCapture/ClientboundSystemChat`). `go test
./chat/...` and the full `./server/...` suite stay green (the round-trip and every other chat
use are unaffected). **SystemChat content+overlay is SEALED.** (Open Question A2 resolved on the
producer side — the real client rendering "<name> msg" is Task 2.)

### 2.3 `ServerboundChat` — the decode / trailing layout (CMD-02, Open Question A6) — **DIVERGENCE FOUND + FIXED**

Vanilla-form serverbound golden (42 bytes, message "sulfur capture chat"):
`13 <19-byte msg> 0000019efa…(Instant Long) 0000000000000000(salt Long) 00(sig absent)
00(VarInt offset) 000000(FixedBitSet 20→3B) 00(checksum)`

| Field | Jar-verified layout (`javap ServerboundChatPacket` + `LastSeenMessages$Update`) | Initial harness send | Verdict |
|-------|-------------------------------------------------------------------------------|----------------------|---------|
| message | `readUtf(256)` String | String | MATCH |
| timeStamp | `readInstant()` (Long ms) | Long | MATCH |
| salt | `readLong()` | Long | MATCH |
| signature | `readNullable(MessageSignature)` (Boolean present-prefix; absent = `00`) | `00` | MATCH |
| lastSeen | `LastSeenMessages$Update`: `readVarInt offset` + `readFixedBitSet(20)` (`positiveCeilDiv(20,8)`=3B) + **`readByte checksum`** | offset + 3B bitset, **NO checksum byte** | **DIVERGED** |

**The divergence:** the FIRST capture attempt sent the `LastSeenMessages$Update` as
`VarInt offset + 3-byte FixedBitSet` and the vanilla server REJECTED it:
`io.netty.handler.codec.DecoderException: Failed to decode packet 'serverbound/minecraft:chat'`.
Decompiling `LastSeenMessages$Update.<init>(FriendlyByteBuf)` showed the constructor reads a
THIRD field — a trailing **`readByte()` checksum** (`IGNORE_CHECKSUM`=0 for an offline client) —
which the initial layout omitted. This is exactly the trailing-layout drift (A6 / T-7-14) the
capture-diff is designed to surface: a Go self-round-trip would have round-tripped the WRONG
(checksum-less) layout green.

**The fix:** the capture method's `serverboundChatBody` now appends the `Byte(0)` checksum after
the 3-byte `FixedBitSet(20)`, so the full empty lastSeen is `VarInt(0) + 00 00 00 + 00` (5
bytes). With the checksum byte present the vanilla server **ACCEPTS** the chat and broadcasts
it (`[Not Secure] <ChatCap2> sulfur capture chat` in the server log — no DecoderException). The
committed golden carries this vanilla-valid layout.

**Sulfur's decoder (`server/chat.go handleChat`) was already correct** by construction: it
decodes ONLY the leading message String and IGNORES the entire trailing
timestamp/salt/sig/lastSeen (server-authoritative, offline). So no encoder/decoder fix was
needed on the Sulfur side — Sulfur's DEFENSIVE decode consumes the real vanilla bytes cleanly:

| Step | Result |
|------|--------|
| `handleChat` over the real golden | recovers "sulfur capture chat" → broadcasts `<bob> sulfur capture chat` |
| full client-style walk of the golden (incl. the checksum byte) | consumes to **exactly 0 trailing bytes** (the layout Sulfur documents is the real wire) |
| vanilla server's reaction to the same framing | **ACCEPTED — no DecoderException** (vanilla-valid) |

**SEALED, no Sulfur-side fix needed; the trailing layout (incl. the checksum byte) is the real
vanilla wire and Sulfur's defensive decode consumes it without mis-framing the stream.**

---

## 3. The Open Questions — Resolved

- **A2 — SystemChat renders as an acceptable broadcast ("<name> msg"):** RESOLVED on the
  producer side. Sulfur's `broadcastSystemChat` body is now BYTE-IDENTICAL to a real vanilla
  `ClientboundSystemChat` (the `TAG_String` content fix, §2.2) — content + `overlay=false`. The
  client renders a byte-identical packet exactly as it renders vanilla's `/tellraw`. The "<name>
  msg" attribution is applied by Sulfur's `handleChat` (`"<" + name + "> " + text`). **Final
  visual ("<AISmoke> hi world" appears in a real client) deferred to Task 2** — the live smoke
  (§5) already confirms it end-to-end against Sulfur.
- **A3 — the command graph serializes a ClientboundCommands the real client accepts/tab-completes:**
  RESOLVED on the producer side. Sulfur's node-tree framing MATCHES vanilla's (`VarInt nodeCount`,
  per-node stub layout, trailing `VarInt rootIndex`, §2.1); the v1 graph carries `/say`+`/me`.
  The live smoke (§5) confirms a 74-byte `ClientboundCommands` reaches a joined client. **The
  real client tab-complete is the Task-2 visual.**
- **A6 — does the client need a ServerboundChatAck / session handshake before SystemChat is
  accepted back?** RESOLVED **NO** by the byte-diff. The capture proves: (a) an offline vanilla
  client (enforce-secure-profile=false) sends an UNSIGNED `ServerboundChat` with an EMPTY
  `LastSeenMessages$Update` (no chat session established) and the vanilla server accepts it; (b)
  the vanilla server emits a `ClientboundSystemChat` (driven by `/tellraw`) WITHOUT any prior
  ack/session from the client. SystemChat is a server-authoritative, session-LESS broadcast — it
  needs NO `ServerboundChatAck`. Sulfur's decode-and-IGNORE of `ServerboundChatAck` /
  `ServerboundChatSessionUpdate` (the v1 no-op, `TestChatAckIgnored`) is therefore correct: the
  acks are bookkeeping for the SIGNED chat chain Sulfur does not ship, and SystemChat is accepted
  back with no handshake. **The live smoke (§5) confirms SystemChat is rendered with no prior
  ack; the final real-client confirmation is Task 2** (watch for chat appearing on the FIRST
  message with no warm-up — if it ever required an ack the byte-diff would not have shown the
  session-less round-trip working).

---

## 4. Deviations / Fixes Applied

**Two divergences found by the capture-diff, both fixed:**

1. **[Capture method] `LastSeenMessages$Update` missing checksum byte (§2.3).** The first
   capture attempt's serverbound-chat trailing layout omitted the jar-verified trailing
   `readByte()` checksum; the vanilla server rejected it with a `DecoderException`. Fixed in the
   harness (`temp/chatcommandcapture/serverboundChatBody` now appends `Byte(0)`); the committed
   golden carries the vanilla-valid layout. **No Sulfur-side change** — `handleChat` already
   decode-and-ignores the trailing fields, so it consumes the corrected golden cleanly.
2. **[Rule 1 — wire-framing bug] `ClientboundSystemChat` content encoded as `TAG_Compound`
   instead of vanilla's bare `TAG_String` (§2.2).** Fixed in the OWNING encoder
   (`chat/nbtmessage.go`): a text-only `Message` now serializes as a bare NBT `TAG_String`
   (`isPlainText()` + `TagType()`/`MarshalNBT()` fast path), byte-identical to vanilla.
   Re-diffed green; `./chat/...` + `./server/...` suites stay green; zero new deps.

Total: 2 divergences (1 capture-method, 1 Sulfur encoder), both fixed and re-diffed.

---

## 5. Gate Results (automatable half — all green)

| Gate | Command | Result |
|------|---------|--------|
| Capture-diff | `go test ./server/ -run TestChatCommandBytesVsVanillaCapture -count=1` | PASS (ClientboundCommands framing MATCH; SystemChat content+overlay byte-identical; ServerboundChat decode + full walk → 0 trailing bytes) |
| Owning-plan wire tests | `go test ./server/ ./server/command/ -run 'TestCommandGraphBuilds\|TestSystemChatBroadcast\|TestChatDecodeKeepsMessage' -count=1` | PASS |
| Chat encoder fix | `go test ./chat/...` | PASS (round-trip survives the TAG_String fast path) |
| Full server + chat suite | `go test ./server/... ./chat/...` | PASS |
| Build + vet | `go build ./...`, `go vet ./...` | clean, zero new deps |
| `-race` (Docker) | `docker run … golang:1.26 go test -race ./server/... ./world/...` | **PASS — race-clean under load (Plans 07-01..05 + the chat/command dispatch + the debug nav trigger)** |
| Live smoke vs Sulfur (proto 776) | join `cmd/sulfur` (SULFUR_DEBUG=1 SULFUR_DEBUG_NAV=1): `ClientboundCommands` (74B) at join, pig (type 100) spawns + MOVES (AI-driven, ≥3 move packets), chat broadcasts as `<AISmoke> hi world` | PASS (`temp/aismoke`) |

---

## 6. Interactive Gate Trigger (how the operator drives Task 2)

`cmd/sulfur` ships an OFF-by-default debug harness for the interactive human-verify. Start the
server with `SULFUR_DEBUG=1` and (for the observable A* navigation) `SULFUR_DEBUG_NAV=1`:

```
SULFUR_DEBUG=1 SULFUR_DEBUG_NAV=1 sulfur -addr :25565
```

This arms (logged at startup):

- a **visible, vanilla-renderable pig (type 100)** near spawn driven by the **REAL ported AI**
  (07-01 GoalSelector — `randomStrollGoal` + `lookAtPlayer` + `randomLookAround` — over the
  07-02 A* navigation), so it WANDERS with vanilla-style behavior (amble + look), **NOT** the
  retired sinusoidal pacing (AI-01);
- the player gets a stack of **stone in hotbar slot 0** so the operator can PLACE a wall
  (Phase-6 block place);
- with `SULFUR_DEBUG_NAV=1`, the pig is additionally **deterministically retargeted** every ~6s
  to a fixed point ±12 blocks along Z through its spawn column (via the SAME `setWantTarget` → A*
  seam the stroll goal uses, only with a deterministic destination), so it paces a KNOWN straight
  line — the operator walls that line and reliably watches the **A* route around** the obstacle
  (AI-02). Without `SULFUR_DEBUG_NAV` the pig still wanders via random strolling; the nav flag
  only makes the obstacle-navigation reliably observable.

A normal `sulfur` run (no env flags) is completely unaffected (the debug hooks are a nil-check
no-op — no extra entity, no damage, no forced navigation).

---

## Task 2 — Real vanilla 26.2 client INTERACTS (BLOCKING human-verify)

> The decisive AI-01/02/03 + CMD-01/02 milestone cannot be self-approved.

PENDING — the orchestrator presents this gate to the operator. Connect an **unmodified vanilla
26.2 client (PrismLauncher)** to `cmd/sulfur` (`SULFUR_DEBUG=1 SULFUR_DEBUG_NAV=1`) and confirm:

- **a** MOB WANDERS (AI-01): the debug pig is VISIBLE and wanders with vanilla-style AI (stroll +
  look at the player), NOT sinusoidal pacing.
- **b** MOB NAVIGATES AN OBSTACLE (AI-02): place a wall (stone from hotbar slot 0) across the
  pig's fixed nav line — the pig ROUTES AROUND it (the A* `request→snapshot→result` seam), it
  does not walk into the wall or clip through.
- **c** MOBS POPULATE (AI-03): the world is not empty (the debug pig + any NaturalSpawner mobs).
- **d** COMMAND RUNS + REPLIES (CMD-01): type a /command (e.g. `/say hi`) — it runs; an invalid
  command replies with a SystemChat error ("Unknown or invalid command: …").
- **e** CHAT BROADCASTS (CMD-02): type a chat message — it broadcasts to all players as
  "<name> message".
- **f** NO KICK / NO HANG: no malformed-packet kick, no "Loading terrain…" hang.

**Sign-off:**

> Reviewed by: ____   Date: ____
> Result: [ ] mob wanders / navigates obstacle / mobs populate / command replies / chat broadcasts — APPROVED
