# Pitfalls Research

**Domain:** Custom Minecraft Java Edition server (26.2 / protocol 776) implemented in Go on Tnze/go-mc, no plugins
**Researched:** 2026-06-23
**Confidence:** HIGH for protocol/registry/chunk mechanics (Minecraft Wiki protocol docs, verified against version-specific notes); HIGH for go-mc state (GitHub PR/repo inspection); HIGH for concurrency model (Folia/Luminol docs); MEDIUM for exact 776-only byte layouts (26.2 post-dates training cutoff — must be re-derived from the jar/wiki, not asserted)

> **Meta-pitfall that frames everything below:** 26.2 (protocol 776, released 2026-06-16) post-dates most LLM training data and a large share of community tooling. Any byte-level layout you "remember" is a *hypothesis* until verified against the 26.2 wiki or extracted from the jar. Treat every "the packet looks like X" statement in this file as "X was true at version N — confirm it is still true at 776." The codegen pipeline (PR #294-296) exists precisely so you stop hand-transcribing layouts that drift every minor version.

## Critical Pitfalls

### Pitfall 1: Registry/codec desync during configuration state → silent disconnect

**What goes wrong:**
Client connects, passes login, then drops at "Loading terrain..." or "Joining world..." with no useful error, or disconnects the instant it receives a packet referencing a registry entry. Most "client connects but can't play" reports trace here.

**Why it happens:**
Since 1.20.2 the client validates all synchronized registries received during the **configuration** state at the moment it gets `Finish Configuration`. Registry entries are referenced *by numeric index*, where the index is the entry's position in the array you sent. If you (a) omit a required registry (e.g. `minecraft:dimension_type`, `minecraft:worldgen/biome`, `minecraft:damage_type`, `minecraft:wolf_variant`, `minecraft:painting_variant`, and whatever 26.2 added), (b) send entries in the wrong order so IDs don't line up, (c) encode the network NBT wrong (1.20.2+ network NBT has **no root compound name** — bytes start `0x0A` then straight into contents), or (d) mishandle the `Clientbound Known Packs` negotiation (if you don't send known packs you must inline *all* NBT; if you claim the client knows `minecraft:core` you must omit NBT it already has) — the client rejects silently.

**How to avoid:**
- Code-generate the full registry set from the 26.2 jar (this is exactly what PR #294-296 produces: 95 registries). Do not hand-curate.
- Send registries in the **same order** the vanilla server sends them; preserve index assignment.
- Implement the simplest path first: **skip Known Packs**, inline all registry NBT. Only optimize to known-packs negotiation after a vanilla client logs in.
- Build a "minimal viable login" harness that connects a real 26.2 client and logs the last packet sent before disconnect.

**Warning signs:**
Disconnect with empty/generic reason; works for one minor version then breaks after a snapshot bump; client log mentions "missing registry entry" or "Fatally missing registry entries"; falls over specifically at `Finish Configuration`.

**Phase to address:**
Login/Configuration phase (the phase that lands the handshake→login→configuration→play transition). This is the gate for "first playable connection."

---

### Pitfall 2: Treating 26.2/776 as if it were a version in training data or in go-mc

**What goes wrong:**
You implement against remembered 1.20/1.21 layouts (or against go-mc master, which tracks ~1.21.x), the bytes are subtly wrong for 776, and the client desyncs or disconnects in ways that look like a logic bug but are actually a protocol-version bug.

**Why it happens:**
26.2 (proto 776) was released 2026-06-16 with the new `YY.D.H` (year.drop.hotfix) scheme and is newer than: (a) the model's training cutoff, (b) go-mc's supported version, and (c) the draft codegen PRs (which target **1.21.11 / proto 774**, not 776). Mojang restructures packets nearly every minor version (e.g. chunk data length-prefix removed at 1.21.5; `teleport_entity` renamed and split at 1.21.2; fluid count short added to chunk sections at 26.1). Protocol number 776 alone tells you nothing about which structs changed.

**How to avoid:**
- Treat the **26.2 server jar as the single source of truth.** Run the codegen pipeline with `--version 26.2` (retargeting the #294-296 approach from 1.21.11). The jar ships unobfuscated, so extractors read real field names.
- Diff the generated 776 packet/registry set against 774 to enumerate exactly what moved; don't assume parity.
- Hard-code/verify the protocol number 776 in the handshake; reject mismatched clients with a clear message rather than letting them proceed and fail mysteriously.
- When the wiki only documents an older proto (e.g. 773), use it as a *starting hypothesis* and confirm field-by-field against the jar.

**Warning signs:**
Implementing from memory or from a blog post without checking the jar; "it matches the wiki" where the wiki page is for 773/774; codegen run still pointed at 1.21.11; any struct copied from go-mc master without re-verifying against 776.

**Phase to address:**
Foundation / data-extraction phase (the very first phase — fork go-mc, stand up codegen, generate 776 data). Everything downstream depends on this being correct.

---

### Pitfall 3: Chunk data array encoding — bits-per-entry, long packing, and the missing length prefix

**What goes wrong:**
Chunks load but the player falls through the floor, sees a void, sees garbled/striped terrain, or the client disconnects parsing the chunk packet. The classic "client connects but sees void / falls through world" failure.

**Why it happens:**
Paletted containers are encoding-fragile in several ways at once:
- **bits-per-entry → palette format selection.** 0 bpe = single-valued (one VarInt, no data array); blocks 4-8 bpe = indirect (VarInt palette + indices); 15+ = direct (global IDs inline). Biomes use 1-3 indirect / 7+ direct. Pick the wrong format for the bpe and the client misreads everything after it.
- **Long packing.** Entries are packed LSB-first, `floor(64/bpe)` entries per long, and an entry **must not span two longs** — leftover high bits are padding. Get the entries-per-long or the padding wrong and the whole section shifts.
- **No length prefix (1.21.5+).** The VarInt long-array length prefix was **removed**. The reader/writer must *compute* long count = `ceil(4096 / entries_per_long)` for blocks (64 for biomes). If your encoder still writes a prefix, or your decoder still reads one, every subsequent field is misaligned.
- **26.1+ fluid count.** Chunk sections gained a leading `short` fluid count; omitting it shifts the section.

**How to avoid:**
- Implement paletted-container read AND write as a single tested module with round-trip property tests (encode→decode→equal) before sending anything to a real client.
- Use go-mc's `level`/`chunks` structures as a reference but verify the 776 section layout (fluid count, no length prefix) against the jar/wiki — go-mc may lag.
- Test against a real client incrementally: send a single all-stone section first (single-valued palette is the simplest), confirm the player stands on it, then add complexity.
- Validate with a known-good capture: connect a vanilla client to a vanilla 26.2 server and capture the chunk packet bytes; diff your output against it.

**Warning signs:**
Player spawns then falls; terrain appears in stripes/offset; works for single-block sections but breaks with mixed blocks (indicates packing bug); breaks only on tall worlds (height/bpe bug); decoder reads a plausible-but-wrong long count.

**Phase to address:**
World/Chunk phase. Sub-gate: "vanilla client stands on a flat all-stone chunk" must pass before any generation work.

---

### Pitfall 4: Heightmaps and lighting — the "stands on chunk but rubber-bands / dark / falls later" class

**What goes wrong:**
Player loads in but rubber-bands, suffocates, the world is pitch black, or the client falls through after moving to a neighbor chunk that wasn't sent.

**Why it happens:**
- **Heightmaps** are now packed long arrays (same paletted/long-packing rules as block data, `ceil(log2(height+1))` bpe, 256 entries), **not** NBT compounds. Wrong `MOTION_BLOCKING` heightmap → client mispredicts collision/spawn → rubber-band or suffocation.
- **Lighting** must be sent (sky + block light bitsets and arrays) or the client renders darkness; some versions tolerate missing light, others don't.
- **Neighbor chunks.** A client won't fully render/collide a chunk whose neighbors are absent. Move toward an unsent chunk and you fall through. The view-distance ring + `Set Center Chunk` must keep up with movement.

**How to avoid:**
- Generate heightmaps with the same long-packing module as block data (one correct implementation, reused).
- Send full light data from day one even if you compute trivial (full-bright) light initially; defer correct propagation but never skip the packet.
- Send a full `chunk view radius` ring around spawn before sending the player-position/spawn packet, and update the ring on movement keyed off `Set Center Chunk`.

**Warning signs:**
World is dark; player suffocates on spawn; rubber-banding on first step; falls through specifically when walking to chunk edges (neighbor-not-sent signature).

**Phase to address:**
World/Chunk phase (heightmaps + light alongside chunk send) and Player-session phase (view-distance ring management on movement).

---

### Pitfall 5: Player position / teleport packet — proto 769+ restructure and velocity units

**What goes wrong:**
The spawn/teleport packet is misencoded; player spawns at wrong coords, gets launched, or the client ignores the teleport and never confirms, leaving the connection half-alive.

**Why it happens:**
The position-sync packet was restructured around proto 769. Per the 26.2-era layout the project already documents: **Teleport ID first**, then X/Y/Z, then velocity **DX/DY/DZ**, then yaw/pitch, then a **32-bit Int flags** bitfield (no longer a single byte). The lower 8 bits of flags are *relative* axis flags (set bit = relative). Velocity is in units of **1/8000 block per tick** — using blocks/sec or blocks/tick directly launches the player. The client must echo a `Confirm Teleportation` with the matching Teleport ID; if your ID handling is off, position never confirms and the player is stuck.

**How to avoid:**
- Encode flags as Int32, not byte. Verify the exact field order against the 776 jar (the 769 layout is the hypothesis; confirm it survived to 776).
- Track outstanding Teleport IDs and only accept movement that matches a confirmed teleport.
- Set velocity to 0 for a plain spawn; only populate DX/DY/DZ when you actually mean to impart motion, in 1/8000-block units.

**Warning signs:**
Player launched on spawn; position never settles; server logs movement packets the client sends before confirming the teleport; flags read as garbage because a byte was read where an int was expected (everything after shifts).

**Phase to address:**
Player-session phase (spawn + movement handling).

---

### Pitfall 6: Premature async — optimizing logic that doesn't exist yet

**What goes wrong:**
Engineering effort goes into async pathfinding / async entity tracking / regionized ticking before there is correct single-threaded vanilla logic to optimize. Result: a complex concurrent skeleton with no working game, or races baked into the foundation.

**Why it happens:**
Leaf/Folia are the architectural inspiration, and it's tempting to build the "fast" version first. But Leaf's optimizations are *concurrency layers over existing, correct, single-threaded vanilla logic* (async pathfinding wraps a working pathfinder; DAB throttles working brains). You cannot async-optimize a pathfinder you haven't written.

**How to avoid:**
- Hard rule: **vanilla correctness first, single-threaded tick loop, then layer concurrency.** This is already a Key Decision in PROJECT.md — enforce it as a phase-ordering constraint, not a guideline.
- Design data structures to be *partitionable later* (per-region ownership, message-passing seams) without actually parallelizing on day one. "Concurrency-ready" ≠ "concurrent now."
- Defer Folia-style region threading to a dedicated late phase with its own success criteria.

**Warning signs:**
Goroutines/channels appearing in the entity or world code before there's a passing single-threaded tick; "we'll make pathfinding async" tickets before a pathfinder exists; benchmarks before correctness tests.

**Phase to address:**
Architecture/Tick-loop phase establishes the single-threaded authoritative loop; a *separate, later* Concurrency-optimization phase adds async layers.

---

### Pitfall 7: Data races on entity/world state and connection write ordering

**What goes wrong:**
Intermittent corruption: entities teleport randomly, blocks revert, players see ghost state, or panics under `-race`. On the connection: packets arrive out of order and the client desyncs or disconnects.

**Why it happens:**
Go makes spawning goroutines trivial, so it's easy to (a) read/write shared entity/world maps from the tick goroutine and a network/IO goroutine simultaneously, and (b) write to a single player's TCP connection from multiple goroutines, breaking Minecraft's **strict per-connection packet ordering** requirement (TCP gives byte ordering, but interleaved concurrent writes corrupt frame boundaries / ordering). Folia's hard lesson: *only the thread ticking a region may touch that region's data*; cross-region access = data race or corruption.

**How to avoid:**
- One **writer goroutine per connection** fed by a channel; never write to a conn from multiple goroutines. Frame/compress in that single writer.
- World/entity mutation happens **only inside the tick loop.** Network goroutines parse inbound packets and enqueue intents (move, place, break) onto a channel the tick consumes; they never mutate world state directly.
- Run the full test suite and a soak test under `go test -race` / `-race` builds from the start, not as an afterthought.
- Adopt Folia's ownership invariant early: a unit of world (chunk/region) has a single owner; cross-owner access goes through messages, never shared mutable pointers.

**Warning signs:**
`-race` reports (treat any as a release blocker); nondeterministic test failures; clients desync only under load/multiplayer; corrupted packet frames; entities that "jump" inexplicably.

**Phase to address:**
Architecture/Tick-loop phase (establish the connection-writer + intent-queue pattern and the ownership invariant before entities/world grow).

---

### Pitfall 8: Keepalive timing and the half-open connection

**What goes wrong:**
Clients drop with "Timed out" even though the server is running, or dead connections linger and leak resources.

**Why it happens:**
The server must send a Keep Alive every 1-15 s during play; the client disconnects if none arrives within ~20 s. The client echoes the keepalive ID and the server must verify it; a mismatched/never-returned ID should disconnect that client. Two common mistakes: (a) tying keepalive emission to the game tick and stalling it when the tick stalls (so a lag spike times everyone out), and (b) never reaping connections whose keepalive went unanswered (half-open sockets accumulate).

**How to avoid:**
- Drive keepalive from an independent timer, not the world tick, so a slow tick doesn't time players out.
- Track outstanding keepalive IDs per connection with a deadline; disconnect on miss.
- Send keepalive only in play state (configuration has its own keepalive); don't send in the wrong state.

**Warning signs:**
Mass "Timed out" disconnects correlated with TPS drops; rising open-connection count with no active players; keepalive IDs not validated on return.

**Phase to address:**
Player-session phase (keepalive loop), revisited in Concurrency phase (ensure tick stalls can't block keepalive).

---

### Pitfall 9: Component-based slot / item format (1.20.5+)

**What goes wrong:**
Inventory, equipment, or item-entity packets are misencoded; client shows wrong/empty items or disconnects parsing a slot.

**Why it happens:**
Since 1.20.5, item slots are **not NBT** — they're a structured component set: item count, item ID, a count of components-to-add, a count of components-to-remove, then each component encoded by its own type-specific format (104 data components in the 1.21.11 set; verify the 776 count). Treating slots as legacy `id+count+NBT` produces malformed packets the moment any item carries components.

**How to avoid:**
- Code-generate the data-component registry and per-component codecs from the jar (PR #294-296 emits 104 components for 1.21.11; regenerate for 776).
- Start with the empty-slot and bare-item (zero components) cases to get framing right, then add components.
- Round-trip test each component type.

**Warning signs:**
Items render empty or wrong; disconnect when a stack has enchantments/custom name/durability (i.e. any component); code that writes NBT into a slot.

**Phase to address:**
Inventory/Item phase (after first playable connection; needed before block placement/breaking with real items).

---

### Pitfall 10: go-mc — master vs tag, unmerged codegen PRs, Java toolchain for codegen

**What goes wrong:**
You depend on a go-mc tag that lags Minecraft by a version, or expect the codegen PRs to be mergeable, and the foundation doesn't actually target 776.

**Why it happens:**
- go-mc's tagging convention is offset: "if 1.19.4 is the latest MC, the newest go-mc **tag** is 1.19.3" — the current version lives on **master**, with no backward-compat promise. Pinning a tag silently gives you an older protocol.
- The codegen pipeline you're relying on (**PR #294, #295, #296** by mj41) is **draft and unmerged**, and targets **1.21.11 / proto 774**, not 776. You will be working from a fork and retargeting it yourself.
- The codegen extracts data **from the server jar**, which requires the **Java toolchain** (Java 25 Zulu / javac 25 are already in the confirmed toolchain) at *build/codegen time* — not at server runtime. Forgetting this breaks reproducible builds on machines without Java.

**How to avoid:**
- Fork go-mc, pin to a specific master commit (not a tag), and apply the #294-296 changes on top; document the exact commit.
- Plan for retargeting the codegen from 1.21.11 → 26.2 as explicit work, not a config flag flip — expect schema drift.
- Keep Java strictly a **codegen/offline dependency**; the produced Go data files are committed/checked so the runtime server builds with Go alone.
- Pin Java/Go versions in the build to match the confirmed toolchain (Go 1.26.1, Java 25).

**Warning signs:**
`go get tnze/go-mc@latest` resolving to a 1.21.x tag; assuming PRs are merged; CI that needs Java to build the *server* (it should only need Java to *regenerate* data); codegen still on `--version 1.21.11`.

**Phase to address:**
Foundation / data-extraction phase (fork, pin, retarget codegen, separate Java-time from Go-runtime).

---

### Pitfall 11: VarInt/VarLong and packet framing/compression edge cases

**What goes wrong:**
"VarInt too big" errors, "Badly compressed packet," frame desync where one bad length cascades into garbage for the rest of the connection.

**Why it happens:**
- VarInt ≤ 5 bytes, VarLong ≤ 10 bytes; a decoder that doesn't cap byte count will loop/overflow on malformed input. The packet **length prefix** must be ≤ 3 bytes even though VarInt allows 5.
- Compression: once `Set Compression` with a non-negative threshold is sent, *every* subsequent packet uses the compressed framing (uncompressed-length VarInt prefix; packets below threshold sent with data-length 0 = uncompressed). Mixing framed/unframed, or compressing packets below threshold, breaks the stream. Max packet 2^21−1 bytes.
- TCP has no message boundaries — partial reads must be buffered until a full frame is available; a naive "one read = one packet" assumption desyncs immediately.

**How to avoid:**
- Use go-mc's `net` codec for VarInt/VarLong and framing rather than rolling your own; it already enforces the byte caps. Verify its compression threshold handling.
- Centralize all framing/compression in the single per-connection writer (ties into Pitfall 7).
- Fuzz the inbound decoder with truncated/oversized/maximal VarInts.

**Warning signs:**
"VarInt too big"; "Badly compressed packet"; connection works for small packets but breaks on the first large chunk packet (threshold/framing bug); decoder assuming a read returns exactly one packet.

**Phase to address:**
Foundation / networking phase (codec + framing), before any gameplay packets.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Full-bright lighting (skip light propagation) | Chunks visible immediately | No real lighting; mobs/visuals wrong; rework later | MVP — must send the light *packet*, just with trivial values |
| Skip Known Packs, inline all registry NBT | Simpler configuration handshake | Larger login payload | Always fine for a no-plugin server until bandwidth matters |
| Single-threaded tick for everything | Correctness, no races | No multithread scaling | Acceptable and *required* until vanilla logic is correct (Pitfall 6) |
| Flat/deterministic world gen first | Unblocks chunk-send testing | Not vanilla-parity terrain | MVP — vanilla-parity is a stretch goal |
| Offline-mode only (no auth/encryption) | No auth in critical path | No security; anyone can join | Acceptable per PROJECT scope until online-mode phase |
| Hand-writing a few packets instead of codegen | Fast for one packet | Drifts every minor version; transcription bugs | Never for the bulk; only for a throwaway spike |
| Pinning a go-mc git tag | Reproducible | Silently an older protocol than 776 | Never — pin a master commit instead |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| 26.2 server jar (data source) | Extracting from an obfuscated/old jar or guessing field names | Use the official 26.2 jar (ships unobfuscated/public mappings); extract real names via Java codegen |
| go-mc upstream | Depending on a tag or assuming PRs #294-296 are merged | Fork, pin a master commit, apply draft PRs, retarget to 776 |
| Vanilla 26.2 client (compat target) | Testing only your own decoder round-trips | Capture real vanilla server↔client traffic and diff your bytes against it |
| Java toolchain | Letting Java leak into the server runtime/build | Java only at codegen time; commit generated Go; server builds with Go alone |
| Minecraft Wiki protocol pages | Reading a page for proto 773/774 and assuming 776 parity | Use wiki as hypothesis; confirm every struct against the 776 jar |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Re-encoding chunk packets per player | TPS drops as players cluster | Encode a chunk once, cache the byte buffer, multicast | Multiple players sharing chunks |
| Keepalive driven by world tick | Lag spike → mass "Timed out" | Independent keepalive timer (Pitfall 8) | First time a tick stalls under load |
| Global lock on world/entity maps | Tick serializes; no scaling | Region/chunk ownership + message passing (Folia model) | Adding the concurrency layer |
| Sending entire view-distance ring every move | Bandwidth + CPU spike on movement | Send only newly-entered chunks; track loaded set per player | Larger view distances / many players |
| Sync teleport "same region" check via center chunk only | Cross-region race at region edges | Check full ownership, not just center chunk (Luminol's fix) | Region threading phase, entities near borders |
| Naive per-entity goroutine | Scheduler thrash, races | Tick entities within their region's single thread | Hundreds+ of entities |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Trusting client-sent position/inventory | Movement/dupe/teleport exploits | Server is authoritative; validate against last confirmed teleport and reachable moves |
| No cap on inbound packet size before allocating | Memory-exhaustion DoS via huge length prefix | Enforce the 2^21−1 max and 3-byte length cap *before* allocating (Pitfall 11) |
| No decoder bounds on VarInt/array lengths | DoS / panic via malformed packets | Cap VarInt bytes; bound array/string lengths from length prefixes |
| Unauthenticated offline mode left on in "production" | Anyone can join as any username | Documented as offline-first; gate online-mode/auth as a required later phase |
| Unbounded connection accept (no keepalive reaping) | Half-open socket exhaustion | Reap on keepalive miss (Pitfall 8) |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Silent disconnect with empty reason | Operator can't diagnose | Always send a Disconnect with a concrete reason; log last packet sent |
| Long "Loading terrain..." then drop | Looks like a hang | Surface configuration/registry failures as explicit disconnect text |
| Protocol-mismatch client gets cryptic failure | Confused users | Detect proto ≠ 776 at handshake and send a clear version-mismatch message |
| Rubber-banding on spawn | Feels broken | Correct heightmap + teleport-confirm handshake before allowing movement (Pitfalls 4, 5) |

## "Looks Done But Isn't" Checklist

- [ ] **Login flow:** Reaches play state but verify the *full* chain handshake→login→`Login Acknowledged`→configuration→registries→`Finish Configuration`→`Login (play)`→spawn — a missing `Login Acknowledged` or `Finish Configuration` ack stalls silently.
- [ ] **Chunk send:** Player sees terrain but verify they can *stand on it and walk to a neighbor chunk without falling* (neighbor-not-sent + heightmap bugs hide here).
- [ ] **Registries:** "Configuration completes" but verify against a *real vanilla 26.2 client*, not your own decoder — only the real client validates indices.
- [ ] **Paletted container:** Round-trips in tests but verify a *mixed-block* section renders correctly on a real client (single-valued sections hide packing bugs).
- [ ] **Teleport/spawn:** Player appears but verify the client sent `Confirm Teleportation` with the matching ID and position settled.
- [ ] **Keepalive:** Connection stays up but verify it survives a deliberate tick stall and that unanswered keepalives get reaped.
- [ ] **Codegen:** Data generates but verify it targets `--version 26.2` (proto 776), not 1.21.11/774.
- [ ] **Concurrency:** Tests pass but verify they pass under `-race` and a multiplayer soak test.
- [ ] **Item slots:** Empty inventory works but verify an item *with components* (enchant/name) round-trips.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Registry desync (Pitfall 1) | LOW-MED | Regenerate registries from jar; diff order/IDs against vanilla capture; flip to inline-NBT (skip known packs) |
| Wrong-version layouts (Pitfall 2) | HIGH | Re-run codegen at 26.2; diff 774→776; audit every hand-touched struct — cost scales with how much was hand-written |
| Chunk packing bug (Pitfall 3) | MED | Isolate the paletted-container module; add round-trip + real-capture diff tests; fix once, reused everywhere |
| Falls through world (Pitfalls 3/4) | LOW-MED | Add heightmap correctness + light packet + neighbor-ring send; reproduce with all-stone flat chunk |
| Premature async (Pitfall 6) | HIGH | Rip out concurrency layer, restore single-threaded correctness, re-layer later — avoid by phase ordering |
| Data race (Pitfall 7) | MED-HIGH | Enforce connection-writer + intent-queue + ownership invariant; `-race` to find all sites |
| Connection-writer race (Pitfall 7) | LOW | Funnel all writes through one goroutine per conn |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| 2. Wrong-version / 776 drift | Foundation / data-extraction | Codegen runs at `--version 26.2`; handshake asserts proto 776; 774→776 diff reviewed |
| 10. go-mc tag/PR/Java | Foundation / data-extraction | Pinned master commit + applied PRs; server builds with Go only; Java isolated to codegen |
| 11. VarInt/framing/compression | Foundation / networking | Fuzz tests on decoder; large chunk packet transmits under compression |
| 1. Registry/configuration desync | Login / Configuration | Real vanilla 26.2 client reaches play; last-packet-before-disconnect logging in place |
| 3. Chunk paletted encoding | World / Chunk | Vanilla client stands on all-stone chunk; mixed-block section renders; real-capture diff |
| 4. Heightmap / light / neighbors | World / Chunk (+ Player-session) | No suffocation/rubber-band on spawn; walk across chunk borders without falling; light present |
| 5. Teleport packet 769+ | Player-session | Client confirms teleport ID; no launch on spawn; flags read as Int32 |
| 8. Keepalive timing | Player-session (+ Concurrency) | Survives induced tick stall; half-open conns reaped |
| 9. Component slot format | Inventory / Item | Item with components round-trips on real client |
| 6. Premature async | Architecture/Tick-loop ordering (defer to Concurrency phase) | No goroutines in entity/world logic before single-threaded tick passes |
| 7. Data races / write ordering | Architecture/Tick-loop | `-race` clean; single connection-writer; world mutated only in tick |

## Sources

- [Java Edition protocol/FAQ – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/FAQ) — configuration state, keepalive timing (1-15s send / ~20s timeout), Login Acknowledged, network NBT no-root-name, minimal spawn sequence, component slots
- [Java Edition protocol/Registries – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/Registries) — registry order = ID assignment, silent disconnect on missing/unknown entry, Known Packs negotiation
- [Java Edition protocol/Chunk format – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/Chunk_format) — paletted containers, bpe→format selection, long-packing/padding, length-prefix removed at 1.21.5, heightmap long arrays, fluid count short at 26.1+
- [Java Edition protocol/Packets – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/Packets) — player position flags as Int bitfield (lower 8 bits relative), velocity 1/8000 block/tick, teleport_entity rename/split at 1.21.2
- [Java Edition 26.2 – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_26.2) & [Protocol version – Minecraft Wiki](https://minecraft.wiki/w/Protocol_version) — 26.2 released 2026-06-16, protocol 776, YY.D.H scheme
- [Tnze/go-mc GitHub repo + Pulls](https://github.com/Tnze/go-mc/pulls) — PRs #294/295/296 (mj41) draft & unmerged, target 1.21.11/proto 774; master-vs-tag versioning offset
- [PaperMC/Folia](https://github.com/PaperMC/Folia) & [Folia Threading Fixes – LuminolMC/Luminol DeepWiki](https://deepwiki.com/LuminolMC/Luminol/7.2-folia-threading-fixes) — region ownership invariant, cross-region data races, same-region teleport check via center chunk insufficient
- [The uncompressed packet size validation – PaperMC/Velocity #1556](https://github.com/PaperMC/Velocity/issues/1556) & [VarInt too big – BungeeCord #2258](https://github.com/SpigotMC/BungeeCord/issues/2258) — compression threshold framing, VarInt byte caps, length-prefix 3-byte limit, 2^21−1 max packet

---
*Pitfalls research for: custom Minecraft Java 26.2 (proto 776) server in Go on go-mc*
*Researched: 2026-06-23*
</content>
</invoke>
