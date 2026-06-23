---
phase: 02-net-protocol-state-machine
plan: 04
subsystem: api
tags: [minecraft-protocol-776, configuration-state, registry-data, update-tags, go-embed, net-pipe]

# Dependency graph
requires:
  - phase: 02-net-protocol-state-machine (02-01)
    provides: net.Pipe harness, state-aware Disconnect helper (NET-07), one-writer concurrency seam
  - phase: 02-net-protocol-state-machine (02-02)
    provides: handshake assert-776, Status, offline Login + compression, cmd/ender assembly
  - phase: 02-net-protocol-state-machine (02-03)
    provides: embedded real 26.2 registry NBT + WriteRegistryData/WriteTags send helpers
provides:
  - "Full ordered 26.2 Configuration sequence (Known Packs → drain → Feature Flags → Registry Data ×29 → Update Tags → Finish → read Acknowledge) reaching Play (NET-04)"
  - "Real vanilla 26.2 Update Tags: 15 registries with exact vanilla tag counts, binding the registry-referenced tags (NET-04)"
  - "ConfigFailErr → readable ClientboundConfigDisconnect on every config failure path (NET-07)"
  - "NET04-CAPTURE-DIFF.md: vanilla-26.2 capture-diff + real-client sign-off (the authoritative proof for a protocol no spec covers)"
affects: [phase-03-tick-world-state, play-state-entry, registry-tag-consumers]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "//go:embed of the 26.2 datagen tag tree (server/registrydata/tags/) — runtime reads only the embedded FS, never temp/"
    - "Tag entry-index resolution: built-in registries index from data/registryid; datapack registries index from RegistryData send order (sorted embedded filenames)"
    - "Tag-of-tags (#minecraft:other_tag) expanded to the union of leaf entry indices, de-duplicated, with cycle guarding"

key-files:
  created:
    - "server/registrydata/tags.go — Update Tags resolver + wire encoder"
    - "server/registrydata/tags_test.go — Update Tags validation (counts, bind, wire round-trip)"
    - "server/registrydata/tags/ — embedded 26.2 datagen tag tree (697 files, 15 registries)"
    - ".planning/phases/02-net-protocol-state-machine/NET04-CAPTURE-DIFF.md — capture-diff + sign-off"
  modified:
    - "server/configuration.go — AcceptConfig rewritten to the full ordered sequence"
    - "server/configuration_test.go — TestConfigSequence / TestConfigNoSilentKickOnError over net.Pipe"
    - "server/registrydata/send.go — WriteTags now emits the real 15-registry set"

key-decisions:
  - "Empty Update Tags (the prior v1 stub) was a CONFIRMED HARD BLOCKER, not optional polish: a real 26.2 client crashes at Registry Loading with 'Unbound tags in registry' / 'Failed to parse value' because registry entries reference tags that empty tags never bind. Fix = send the real vanilla 15-registry tag set."
  - "proto-776 ClientboundLoginFinishedPacket needs a trailing sessionId UUID (GAME_PROFILE + UUIDUtil.STREAM_CODEC). The fork omitted it; the real client failed to decode login_finished. net.Pipe missed it (the bot scans only UUID+name)."
  - "Tag entry indices resolve from two authoritative sources kept in lockstep with the send path: data/registryid order (built-ins) and Load()'s sorted-filename RegistryData send order (datapack registries)."

patterns-established:
  - "Pattern: real-client capture-diff as the NET-04 correctness gate — self-consistent net.Pipe tests cannot prove vanilla-correctness for a protocol no spec covers; only a byte-comparison + a real client reaching Play can."
  - "Pattern: tag-of-tags flattening to numeric indices on the wire (the Update Tags wire format has no tag reference)."

requirements-completed: [NET-04, NET-07]

# Metrics
duration: 178min
completed: 2026-06-23
---

# Phase 2 Plan 04: Configuration Sequence + Real Update Tags Summary

**Rewrote AcceptConfig into the full ordered 26.2 Configuration sequence (Known Packs → 29 Registry Data → real 15-registry Update Tags → Finish → read Acknowledge) and proved, against a real vanilla 26.2 client, that the server reaches Play — fixing two real-client-only bugs (empty Update Tags and a missing login_finished sessionId UUID) that self-consistent tests could not catch.**

## Performance

- **Duration:** ~178 min (13:32 → 16:31 across the AcceptConfig rewrite, 29-registry payload, and Update Tags fix)
- **Started:** 2026-06-23T13:32:54Z
- **Completed:** 2026-06-23T16:31:14Z
- **Tasks:** 3 (Task 1 AcceptConfig rewrite, Task 2 integration test, Task 3 capture-diff + human sign-off)
- **Files modified:** 4 source/test + NET04-CAPTURE-DIFF.md + 697 embedded tag JSON

## Accomplishments

- **NET-04 reached Play against a real client.** A real vanilla 26.2 (PrismLauncher) client completed the full chain Handshake → Status → Login → Configuration → Play, receiving the readable Phase-2 kick "Server not yet playable — gameplay arrives in Phase 3" with NO "Unbound tags" crash, NO "Failed to parse value", NO "Loading terrain…" hang, NO login_finished decode error.
- **AcceptConfig rewritten** from the 3-pitfall stub into the full ordered sequence: Select Known Packs first → drain client replies by packet ID (KeepAlive/Ping answered inline; Client Information and unknown/CoC IDs no-op; loop exits on the Known Packs echo only) → Update Enabled Features → Registry Data ×29 (embedded real 26.2 NBT) → Update Tags → Finish → **block reading the Acknowledge** before returning into Play.
- **Real Update Tags** for the 15 registries vanilla sends, with vanilla's exact tag counts (block=265, item=224, worldgen/biome=68, entity_type=48, damage_type=34, enchantment=22, banner_pattern=11, fluid=6, game_event=5, timeline=4, instrument=3, point_of_interest_type=3, dialog=2, painting_variant=1, potion=1), binding the previously-unbound enchantment exclusive_set/*, timeline in_*, dialog, and sulfur_cube_archetype item tags.
- **NET-07** honored: every config failure path returns ConfigFailErr → readable ClientboundConfigDisconnect, never a bare close.
- **Capture-diff signed off:** NET04-CAPTURE-DIFF.md records the 29/29 registry exact match, the 15/15 tag-set exact match, NBT shape, and packet-ID alignment, plus the real-client result.

## Task Commits

1. **Task 1: Rewrite AcceptConfig (full ordered Configuration sequence)** — `70e7d6da` (feat)
2. **Task 2: TestConfigSequence integration over net.Pipe vs fork bot client** — `5ecdd523` (test)
3. **Task 3 (prep): expand embedded registry set to full vanilla 26.2 (29 registries)** — `fdbfca0d` (feat)
4. **Task 3 (prep): NET04 capture-diff vs vanilla 26.2 (awaiting sign-off)** — `d6788311` (docs)
5. **Real-client bug 1: login_finished trailing sessionId UUID** — `e283886c` (fix, found during the real-client test)
6. **Real-client bug 2: real vanilla 26.2 Update Tags (15 registries)** — `dfb4af04` (feat, found during the real-client test)

**Plan metadata:** docs commit closing Phase 2 (this SUMMARY + STATE/ROADMAP/REQUIREMENTS + capture-diff APPROVED).

## Files Created/Modified

- `server/configuration.go` — AcceptConfig rewritten to the full ordered 26.2 sequence; reads the Acknowledge before Play; sources registry data from server/registrydata, not registry.Registries.
- `server/configuration_test.go` — TestConfigSequence (full leg over net.Pipe with the fork's bot client, -race clean), TestConfigNoSilentKickOnError (mid-sequence failure → ConfigFailErr with readable text).
- `server/registrydata/send.go` — WriteTags now emits the real 15-registry tag set (was present-but-empty).
- `server/registrydata/tags.go` — tag resolver: loads the embedded tag tree, resolves entry indices (built-in via data/registryid, datapack via send order), expands tag-of-tags, and encodes the ClientboundUpdateTags wire body.
- `server/registrydata/tags_test.go` — TestUpdateTagsMatchesVanillaCounts (every member resolves; counts == vanilla), TestUpdateTagsBindsReferencedTags (the previously-unbound tags now bind), TestWriteTagsWireRoundTrip (wire bytes round-trip, no trailing).
- `server/registrydata/tags/` — embedded 26.2 datagen tag JSON (697 files across the 15 registries).
- `.planning/phases/02-net-protocol-state-machine/NET04-CAPTURE-DIFF.md` — the audit artifact: registry set/order/NBT/tags/packet-ID diff vs vanilla 26.2 + real-client sign-off.

## Decisions Made

- **Update Tags must be the real non-empty vanilla set.** Open Question 2 ("is present-but-empty enough?") was DISPROVEN by a real client crash. Sending the real 15-registry tag set is the only fix that binds the registry-referenced tags.
- **login_finished carries a trailing sessionId UUID in proto 776** (GAME_PROFILE + UUIDUtil.STREAM_CODEC). Appended it so the real client decodes the packet.
- **Tag indices resolve in lockstep with the send path** — datapack-registry tag indices use the same sorted-filename order Load() uses for RegistryData, so a tag member's index equals the slot the entry occupies in the packet Ender actually sends.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] proto-776 login_finished missing trailing sessionId UUID**
- **Found during:** Task 3 (real-client capture/verification)
- **Issue:** The fork's ClientboundLoginFinishedPacket omitted the sessionId UUID that proto 776 appends after GAME_PROFILE; a real client failed to decode login_finished. The net.Pipe bot missed it because it scans only UUID+name.
- **Fix:** Appended the sessionId UUID (GAME_PROFILE + UUIDUtil.STREAM_CODEC) to the Login Finished packet.
- **Files modified:** server/login.go (proto-776 login_finished packet path)
- **Verification:** Real vanilla 26.2 client decodes login_finished and proceeds to Configuration.
- **Committed in:** `e283886c`

**2. [Rule 1 - Bug] Empty Update Tags left registry-referenced tags unbound**
- **Found during:** Task 3 (real-client capture/verification)
- **Issue:** The present-but-empty Update Tags stub bound nothing, so a real 26.2 client aborted Registry Loading with "Unbound tags in registry" (enchantment exclusive_set/*, timeline in_*, dialog) and "Failed to parse value … from server" (dimension_type, enchantment, all 14 sulfur_cube_archetype entries reference tags).
- **Fix:** Rewrote WriteTags to send the real vanilla 26.2 tag set for the 15 registries, with correct per-tag entry indices and tag-of-tags expansion. Embedded the 26.2 datagen tag JSON.
- **Files modified:** server/registrydata/tags.go, server/registrydata/send.go, server/registrydata/tags/ (embedded)
- **Verification:** TestUpdateTagsMatchesVanillaCounts/BindsReferencedTags/WireRoundTrip pass natively and under -race (golang:1.26 Docker); a real vanilla 26.2 client passes Registry/Tag Loading and reaches Play.
- **Committed in:** `dfb4af04`

---

**Total deviations:** 2 auto-fixed (both Rule 1 — real-client-only protocol bugs).
**Impact on plan:** Both fixes were essential for NET-04 correctness and could only be surfaced by the real-client capture-diff (Task 3), which is exactly why Task 3 was a blocking human-verify checkpoint. No scope creep.

## Issues Encountered

- **Self-consistent tests cannot catch real-client-only protocol bugs.** The net.Pipe bot client tolerated both the missing login_finished sessionId UUID and empty Update Tags because its decoder is laxer than a real client's strict registry/tag validation. Resolved by the Task-3 capture-diff against a real vanilla 26.2 client, which is the authoritative gate for protocol 776.
- **-race needs cgo; the host has no C compiler.** All -race gates ran in the golang:1.26 Docker image (consistent with the Phase-2 pattern). Native go build/vet/test ran on the host.

## User Setup Required

None — no external service configuration required. (A real vanilla/PrismLauncher 26.2 client + the cached 26.2 server jar were used for the capture-diff, already present.)

## Next Phase Readiness

- **Phase 2 is complete.** NET-01..07 are all done (NET-01/02/03 from 02-02, NET-05/06 from 02-01, NET-04/07 from 02-04). A real vanilla 26.2 client reaches Play and receives the readable Phase-2 kick.
- **Phase 3 (Tick/World) entry point:** the Play-state handoff is the stub kick today; Phase 3 owns the tick loop, KeepAlive timer (the Acknowledge read is bounded by the conn read deadline — threat T-2-11, deferred to TICK-04), and the actual world state.
- **Deferred (tracked in STATE.md):** the project rename Ender → Sulfur (module `imhinotori/go-mc` → `imhinotori/sulfur`, `cmd/ender` → `cmd/sulfur`, user-facing strings) is a dedicated commit at Phase 2 close, intentionally NOT mixed with feature work. The binary/module still say "ender"/"go-mc" for now.

## Self-Check: PASSED

- SUMMARY.md present.
- All referenced commits exist: `70e7d6da`, `5ecdd523`, `fdbfca0d`, `d6788311`, `e283886c`, `dfb4af04`.
- `go build ./...` clean; `go test ./server/...` (incl. registrydata + config sequence) green.
- Real-client sign-off recorded in NET04-CAPTURE-DIFF.md (APPROVED).

---
*Phase: 02-net-protocol-state-machine*
*Completed: 2026-06-23*
