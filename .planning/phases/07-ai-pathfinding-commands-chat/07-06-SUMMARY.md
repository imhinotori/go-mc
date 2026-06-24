# 07-06 SUMMARY — Capture-diff seal + AI/CMD interactive milestone (SIGNED OFF)

**Plan:** 07-06 (Phase 7, Wave 4) — `autonomous: false`
**Status:** COMPLETE — both tasks done; human-verify gate PASSED.

## Task 1 — Capture-diff vs real vanilla 26.2 (automatable)

Captured 3 golden fixtures from the real vanilla 26.2 server (RCON bot harness, superflat seed 144, offline) and committed them under `fixtures/`: `vanilla-commands.bin`, `vanilla-system-chat.bin`, `vanilla-serverbound-chat.bin`. `server/chat_command_capture_test.go` (`TestChatCommandBytesVsVanillaCapture`) byte-diffs Sulfur's encoders/decoder, skipping cleanly if a fixture is absent.

**Result: the diff caught 2 wire divergences (fixed), and the real client caught a 3rd the diff missed.**
- ClientboundCommands node framing: MATCH.
- **ServerboundChat trailing**: the LastSeenMessages$Update `Byte checksum` was missing → vanilla server rejected with DecoderException. Fixture corrected (Sulfur's handleChat decode-and-ignores, so no handler change).
- **ClientboundSystemChat content**: Sulfur emitted `TAG_Compound{text}`; vanilla emits a bare `TAG_String` for plain text. Fixed in `chat/nbtmessage.go` (isPlainText fast path) → byte-identical.

Open Questions resolved (07-CAPTURE-DIFF.md): A2 SystemChat renders as `<name> msg`; A3 command graph accepted/tab-completes; A6 NO ServerboundChatAck needed before SystemChat (offline client sends unsigned chat with empty lastSeen, server accepts).

## Task 2 — Real vanilla 26.2 client interactive gate (human-verify, BLOCKING)

The user connected an unmodified vanilla 26.2 client and signed off ("approved" / "Si, de hecho salta un bloque") after the fixes below. The AI-01/02/03 + CMD-01/02 milestone is MET: mobs spawn distributed across the loaded area and wander with vanilla-style AI (and jump up a block — the navigation step-up working), commands run and reply, chat broadcasts to all players — no kick/hang. Player movement works.

### Real-client bugs the interactive run surfaced (none caught by self-tests) — fixed
1. **Commands kick "Failed to decode packet clientbound/minecraft:commands"** (4e75ec2c): the fork serialized an ArgumentNode's parser as `pk.Identifier("brigadier:string")` (pre-1.19 wire). Since proto 759+ it's a VarInt registry index — jar-confirmed brigadier:string = 5 (bool=0..long=4,string=5). The capture-diff missed it because its walker was written against the same buggy wire (self-consistent). Fixed the encoder AND the test walker.
2. **Player "can't move"** (69309abe): the SULFUR_DEBUG periodic damage killed the player every ~20s → death screen → client stops sending movement. Was the same symptom, not a movement bug. Made damage opt-in (SULFUR_DEBUG_DAMAGE), default off.
3. **Debug pig stood still / fell** (759a31ca): the debug pig spawned the instant a player joined, before its column (generated off-tick) was loaded → all-air navigation snapshot → no path + gravity dropped it. Gated the spawn on the column being loaded.
4. **Only one pig visible** (119853dc): `naturalSpawn` always placed at the first eligible column's center → every mob stacked on one block. Ported vanilla's random-column + random-in-chunk placement + a packing guard (skip if a mob is within ~6 blocks). Mobs now distribute across the loaded area.

All fixes: `go build` 0, `go vet` 0, tests green, Docker `-race` clean.

## The Java port (per the user mandate)

All AI/spawn LOGIC was ported directly from the decompiled unobfuscated 26.2 jar via `javap -c` (idiomatic Go, no GPL paste, bytecode cited): GoalSelector flag-lock arbitration + the Pig goal set (07-01); PathFinder A* (fudge 1.5f, distanceTo g-cost, maxVisited=128, WalkNodeEvaluator malus table) with computePath as a PURE function of an immutable snapshot — the Phase-8 async hinge (07-02); NaturalSpawner CREATURE cap=10 + spawn distances + ON_GROUND placement (07-03). Wire (chat/command) sealed via capture-diff against the real server.

## Requirements

AI-01..03 + CMD-01..02 all complete. Phase 7 DONE — mobs behave and navigate, the world populates by vanilla-style spawning, the server dispatches commands and broadcasts chat. Everything synchronous on the tick (TICK-05) behind async-ready seams; zero new deps.

## Carry-forward to Phase 8 (Leaf async)
The A* `computePath(req)` is a pure function of an immutable snapshot, ready for OPT-01 to hand to an ants pool behind the existing `applyAsyncResults` seam with zero logic change. The tracker (OPT-02) and the spawner candidate-scan (OPT-03) are structured for the same off-tick swap. NOW introduce xsync/ants/conc.
