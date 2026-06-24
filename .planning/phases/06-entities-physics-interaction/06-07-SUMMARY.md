# 06-07 SUMMARY — Capture-diff seal + ENT-01..05 interactive milestone (SIGNED OFF)

**Plan:** 06-07 (Phase 6, Wave 7) — `autonomous: false`
**Status:** COMPLETE — both tasks done; human-verify gate PASSED.

## Task 1 — Capture-diff vs real vanilla 26.2 (automatable)

Captured 5 golden fixtures from the real vanilla 26.2 server (`temp/cache/26.2-server.jar`, superflat seed 144, offline, driven over RCON via a bot harness) and committed them under `fixtures/`:
- `vanilla-add-entity.bin`, `vanilla-set-entity-data.bin`, `vanilla-container-set-content.bin`, `vanilla-container-set-slot.bin`, `vanilla-container-click.bin`.

`server/entity_capture_test.go` (`TestEntityBytesVsVanillaCapture` + `TestSlotBytesVsVanillaCapture`) byte-diffs Sulfur's encoders against the goldens, skipping cleanly if a fixture is absent.

**Result: all 4 MEDIUM-confidence surfaces matched byte-for-byte — ZERO divergences, no encoder fix needed.** First capture-diff in the project to find no bug — because the earlier waves already jar-derived the unusual shapes (06-04 UseItemOn's two trailing booleans; 06-05 HashedPatchMap's fixed Int32 CRC value).
- AddEntity Vec3.LP short scaling: `[0.5,0.4,-0.3]` → `f9ff59996662`, byte-identical. SEALED.
- SetEntityData indexed entries + 0xFF terminator: byte-identical. SEALED.
- ContainerSetContent slot count+id+added+removed framing: byte-identical. SEALED.
- ContainerClick HashedStack decode: consumes a real vanilla click to exactly zero trailing bytes; the vanilla server accepted Sulfur's click form without disconnect. SEALED.

Open Questions resolved (recorded in 06-CAPTURE-DIFF.md): LP scaling sealed; entity render keys off the AddEntity type id, not metadata (custom SulfurCube type 130 has no vanilla renderer → debug uses a vanilla pig type 100); HashedStack decode depth confirmed; physics faithfulness = visible behaviors only (A1).

## Task 2 — Real vanilla 26.2 client interactive gate (human-verify, BLOCKING)

The user connected an unmodified vanilla 26.2 client (PrismLauncher) to `cmd/sulfur` (SULFUR_DEBUG=1) and signed off ("ok funciona" / "approved") after the fixes below. The ENT-01..05 interactive milestone is MET: an entity is visible and moves, blocks place/break and persist (no ghost), the health bar drops on damage, and death→respawn completes into a streamed world — no kick/hang.

### Three real-client bugs the interactive check surfaced (none caught by any self-test) — fixed
1. **Respawn stuck on "Loading terrain" then dead with respawn menu** (94ec8a14): `performRespawn` sent `ClientboundRespawn` (which rebuilds an EMPTY ClientLevel) but not the bootstrap framing the fresh level needs — the `GameEvent(LEVEL_CHUNKS_LOAD_START)` the client waits on before rendering chunks, and the authoritative `SetHealth(full)` that clears the death overlay. `performRespawn` now mirrors the join bootstrap: Respawn → GameEvent → SetHealth → re-teleport → abilities/held-slot → streamer reset.
2. **Blocks broke instantly like creative** (94ec8a14): `handlePlayerAction` broke on `START_DESTROY_BLOCK` (action 0), which a survival client sends the instant the dig button is pressed (before mining). Break now triggers ONLY on `STOP_DESTROY_BLOCK` (action 2, dig FINISH).
3. **No placeable block in hand** (94ec8a14): the v1 inventory is empty and a survival client emits no `UseItemOn` (place) without a held item. The SULFUR_DEBUG trigger now gives each player a stack of stone in hotbar slot 0 via an authoritative ContainerSetContent.
4. **Debug pig "walking backwards very fast"** (83b084d1, cosmetic): the debug pacing oscillated too fast and faced perpendicular to its motion. Slowed + yaw aligned to travel direction. This is a throwaway SULFUR_DEBUG trigger — real mob AI is Phase 7.

All fixes: `go build` 0, `go vet` 0, tests green, Docker `-race` clean over `./server/...`.

## Requirements

ENT-01..06 all complete. Phase 6 DONE — the world is interactive: entities spawn/track/move, gravity + per-axis AABB collision, authoritative place/break with reconciliation, component-slot inventory with HashedStack decode, server-owned health/damage/death/respawn, Anvil persistence — all byte-diffed against vanilla 26.2 and confirmed by a real client.

## Carry-forward to Phase 7
- USER MANDATE: mob logic must be ported DIRECTLY FROM JAVA (decompiled vanilla jar + Paper/Leaf) for behavioral parity — source-porting (non-1:1 translation) is now permitted for gameplay logic. The debug pig is NOT the mob model; it gets replaced by the real ported AI (Goal/GoalSelector, Brain/Behavior, PathNavigation A*).
