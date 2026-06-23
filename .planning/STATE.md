---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Completed 02-04-PLAN.md (Phase 2 complete; real vanilla 26.2 client reaches Play)
last_updated: "2026-06-23T22:00:33.678Z"
last_activity: 2026-06-23
progress:
  total_phases: 9
  completed_phases: 2
  total_plans: 10
  completed_plans: 9
  percent: 90
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-23)

**Core value:** A Go server that an unmodified vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — architected from day one for Leaf-style async optimizations.
**Current focus:** Phase 3 (Authoritative Tick Loop) underway — Wave 1 (tick spine core) complete. Next: 03-02 (subtick input buffer) and 03-03 (GamePlay wiring).

## Current Position

Phase: 3 of 9 (Authoritative Tick Loop) — IN PROGRESS
Plan: 2 of 3 complete (03-01 done; 03-02, 03-03 pending)
Status: Ready to execute
Last activity: 2026-06-23

Wave-1 proof point: a single-owner TickLoop with a fixed-timestep accumulator over an
injectable clock anchors game-time to exactly 1200 ticks/60s (TICK-02), runs the fixed
ordered phase pipeline with no-op applyAsyncResults/tracker seams (TICK-01/05), publishes
an atomic MSPT/TPS/gametime snapshot (TICK-06), and is Docker -race -count=10 clean.

Progress: [█████████░] 90%

## Performance Metrics

**Velocity:**

- Total plans completed: 0
- Average duration: — min
- Total execution time: 0.0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**

- Last 5 plans: —
- Trend: —

*Updated after each plan completion*
| Phase 01 P01 | 8min | 3 tasks | 343 files |
| Phase 01 P02 | 12m | 3 tasks | 265 files |
| Phase 01 P03 | 10min | 3 tasks | 4 files |
| Phase 02 P01 | 70min | 3 tasks | 6 files |
| Phase 02 P02 | 5m | 3 tasks | 5 files |
| Phase 02 P03 | 55 min | 2 tasks | 131 files |
| Phase 02 P04 | 178min | 3 tasks | 701 files |
| Phase 03 P01 | 18 | 2 tasks | 4 files |
| Phase 03 P02 | 18min | 2 tasks | 5 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Roadmap]: Dependency-forced 9-phase spine — protocol-state sequence forces early order; codegen (Phase 1) gates everything.
- [Roadmap]: "Vanilla logic first, Leaf optimizations last" — Phases 5-7 build synchronous reference implementations; Phase 8 swaps executors behind pre-built seams.
- [Roadmap]: Ownership-based synchronization established in Phase 3 (near-zero cost single-threaded) is the insurance policy that keeps Phases 8-9 additive, not a rewrite.
- [Roadmap]: Game-time anchored to 50ms (TICK-02) + CS2-style subtick layer (TICK-03) — deliberate, not vanilla 20-TPS naive scaling.
- [Phase 1]: Fork go-mc merged into repo root (allow-unrelated-histories) on branch ender-776; module path rewritten Tnze/go-mc -> imhinotori/go-mc tree-wide so the runtime builds Go-only (GEN-01).
- [Phase 1]: Pinned base 539b4a3a + specific PR-head SHAs (pr294 23fbe76, pr295 f9c5c05, pr296 d6dece0) recorded for T-1-02 reproducibility; extractor retargeted to eclipse-temurin:25-jdk (tag form, digest deferred to Plan 02).
- [Phase ?]: 26.2 removed net.minecraft.world.item.EitherHolder; variant/damage components now encode as plain Holder<X> (VarInt), fixed in extractor by simple-name guard
- [Phase ?]: 26.2 split reports/items.json into per-item component files; ExtractAll.java synthesizes items.json to preserve gen_item.go contract
- [Phase ?]: Added Mojang-manifest sha1 gate to download.go (T-1-01); jar verified before extraction, abort on mismatch
- [Phase 01]: Enumerated 774->776 via set-deltas (comm on identifier sets) to isolate true additions from iota/ID reindex noise
- [Phase 01]: sulfur_cube_archetype registry + sulfur_cube_hot damage type are datapack/dynamic registries outside the codegen surface (absent by design, not dropped)
- [Phase ?]: [Phase 2]: NET-05 backpressure = bounded ChannelQueue + disconnect-on-full for Phase 2; revisit when the tick owns flush timing (Phase 3)
- [Phase ?]: [Phase 2]: chan Intent (Intent{*Client, pk.Packet}) is the stable network->tick seam; stubTickConsumer is the Phase 3 attach point with no API change
- [Phase ?]: [Phase 2]: Fixed fork bug — chat.Message.MarshalNBT double-encoded the compound tag header, corrupting every chat.Message Field on the wire (incl. disconnect reasons)
- [Phase ?]: [Phase 2]: -race needs cgo; host has no C compiler (CGO_ENABLED=0) so the server/net race gate runs in golang:1.26 Docker — NET-05 seam proven race-clean
- [Phase ?]: NET-01 reuses the Wave-1 Disconnect helper for the proto-776 login rejection
- [Phase ?]: cmd/ender ListPingHandler embeds PingInfo+PlayerList; PingInfo alone lacks player-count methods
- [Phase 02]: Registry send path uses type-faithful JSON->nbt/dynbt conversion (not json->map[string]any), with a floatFields override for vanilla Codec.FLOAT fields — encoding/json maps all numbers to float64->TagDouble; dynbt lets us assign TagInt/TagByte/TagFloat explicitly so the 26.2 client does not silently reject the registry
- [Phase ?]: [Phase 2]: Empty Update Tags was a confirmed HARD BLOCKER (not optional) — a real 26.2 client crashes at Registry Loading with 'Unbound tags'/'Failed to parse value' because registry entries reference tags; fix = send the real vanilla 15-registry tag set (dfb4af04)
- [Phase ?]: [Phase 2]: proto-776 ClientboundLoginFinishedPacket needs a trailing sessionId UUID (GAME_PROFILE + UUIDUtil.STREAM_CODEC); the fork omitted it and net.Pipe missed it (bot scans only UUID+name) — only a real client surfaced it (e283886c)
- [Phase ?]: [Phase 2]: NET-04 correctness gate is a real-client capture-diff, not self-consistent tests — the net.Pipe bot's lax decoder tolerated both the login_finished and empty-tags bugs a real client rejects
- [Phase ?]: [Phase 3]: TickLoop single-owner spine over injectable Clock; gametime++ inside for acc>=step anchors TICK-02 (1200/60s); 250ms spiral clamp; applyAsyncResults/tracker no-op seams for Phase 8; MSPT via atomic.Pointer[TickStats]
- [Phase ?]: [Phase 3]: TICK-03 subtick seam = bounded (cap 256, drop-oldest) per-player buffer, server-stamped At=clock.Now() (never client time), chronological drain (stable sort) through STUB applyInput; physics deferred to Phase 6. TICK-04 proven: KeepAlive on own goroutine fires no timeout under a stalled tick. Zero new deps; -race clean.

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- Bleeding-edge risk: proto 776 post-dates go-mc, the wiki, and training data. All exact 776 byte layouts must be jar-derived/capture-diffed, not asserted from memory. Affects Phases 1, 2, 4, 5.
- Phases flagged for deeper per-phase research (`/gsd-research-phase`): 1 (codegen retarget), 2 (Configuration registry set), 4 (chunk section layout), 5 (teleport/spawn layout), 9 (vanilla-parity worldgen).

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| Rename | Project rename Ender → **Sulfur**: module `imhinotori/go-mc` → `imhinotori/sulfur` (367 .go + both go.mod), `cmd/ender` → `cmd/sulfur`, user-facing strings. MC entity names + frozen 774 baseline left as-is. | ✅ Done (630f90e3) | Phase 2 (NET-04) |

## Session Continuity

Last session: 2026-06-23T22:00:24.878Z
Stopped at: Completed 02-04-PLAN.md (Phase 2 complete; real vanilla 26.2 client reaches Play)
Resume file: None
Next: plan Phase 3 (Tick & World State). Deferred at Phase 2 close: Ender → Sulfur rename (see Deferred Items).
