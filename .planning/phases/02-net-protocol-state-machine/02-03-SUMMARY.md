---
phase: 02-net-protocol-state-machine
plan: 03
subsystem: api
tags: [registry-data, nbt, network-protocol, configuration-state, go-embed, dynbt, protocol-776, net-04]

# Dependency graph
requires:
  - phase: 01-protocol-data-foundation
    provides: "proto-776 packetid symbols (ClientboundConfigRegistryData/UpdateTags), nbt + nbt/dynbt encoder, registry/network.go RegistryData frame, registry/codec.go vanilla field types"
  - phase: 02-01-network-plumbing
    provides: "net.Pipe test harness pattern; *net.Conn WritePacket seam"
provides:
  - "server/registrydata package: embedded real 26.2 registry NBT (dimension_type, worldgen/biome, damage_type, chat_type) converted to network-format NBT via a type-faithful path"
  - "WriteRegistryData(PacketWriter): one ClientboundConfigRegistryData per registry with the correct 776 frame (id + count + per-entry key/hasData/network-NBT)"
  - "WriteTags(PacketWriter): present empty-but-valid ClientboundConfigUpdateTags so the client does not kick on a tag reference"
  - "Load() []Registry: embedded-FS loader exposing each registry's ordered, type-faithful dynbt entries"
affects: [02-04-configuration-sequence, world, chunk, play-state]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Type-faithful JSON->NBT conversion via nbt/dynbt (explicit per-value tag types) instead of a json->map[string]any->nbt bridge"
    - "Embedded committed registry source (//go:embed) decoupling runtime from the gitignored build-time datagen tree"
    - "Vanilla type-schema override (floatFields) for the float-vs-double case Gson serialization cannot disambiguate"

key-files:
  created:
    - server/registrydata/embed.go
    - server/registrydata/send.go
    - server/registrydata/registrydata_test.go
    - server/registrydata/registries/ (128 committed 26.2 registry JSON entries)
  modified: []

key-decisions:
  - "Used nbt/dynbt as the type-faithful intermediate (plan option c, schema-carried) rather than per-registry typed structs (option a): the 26.2 dimension_type attributes map and biome features/spawners are deeply heterogeneous free-form trees that typed structs would not capture cleanly"
  - "Drove int-vs-decimal typing from the JSON number token's lexical form (json.Number + UseNumber), proven type-faithful because Minecraft's Gson datagen serializer always emits floats/doubles with a decimal point and integers without (verified across all 128 entries)"
  - "Added an explicit floatFields override (exhaustion/temperature/downfall/creature_spawn_probability/probability -> TagFloat) for the only case the lexical heuristic cannot resolve: Gson serializes float and double identically"
  - "Send order: chat_type, damage_type, dimension_type, worldgen/biome (deterministic, reproducible from the embedded tree); registries.json holds only code-driven known-pack registries, not these datapack registries"
  - "WriteTags emits VarInt(0) — a present but empty Update Tags; the minimal real set (if any) is recovered by the 02-04 capture-diff"

patterns-established:
  - "Registry send path uses embedded real jar-derived NBT, never the stale registry/codec.go decode struct"
  - "Wave-2 tag-type guard test asserts concrete NBT tag types (TagInt/TagByte, never TagDouble) to catch typing regressions before the human capture-diff"

requirements-completed: [NET-04]

# Metrics
duration: ~55 min
completed: 2026-06-23
---

# Phase 2 Plan 03: Registry Data Payload (NET-04) Summary

**Embedded the authoritative 26.2 registry NBT (dimension_type/biome/damage_type/chat_type) and built WriteRegistryData/WriteTags via a type-faithful JSON->dynbt conversion that serializes integer fields as TagInt/TagByte and vanilla float fields as TagFloat — never TagDouble — defusing the stale-schema and float64->TagDouble silent-reject traps.**

## Performance

- **Duration:** ~55 min
- **Started:** 2026-06-23T16:31Z (approx)
- **Completed:** 2026-06-23T17:26Z
- **Tasks:** 2 (Task 2 TDD: RED via failing test compile, GREEN via send.go + float override)
- **Files created:** 3 Go files + 128 committed registry JSON entries

## Accomplishments
- Extracted the real 26.2 registry content out of the gitignored datagen tree into the committed runtime source and `//go:embed`-ed it — runtime has zero dependency on the build-time artifact.
- Type-faithful JSON->network-NBT conversion using `nbt/dynbt`: integer fields -> TagInt, booleans -> TagByte, vanilla `Codec.FLOAT` fields -> TagFloat, others decimal -> TagDouble. No `json -> map[string]any -> nbt` bridge anywhere.
- The embedded dimension_type retains the real nested 26.2 schema (`attributes`, `default_clock`, `has_ender_dragon_fight`, `timelines`), NOT the stale flat `registry/codec.go` Dimension struct.
- `WriteRegistryData` emits one `ClientboundConfigRegistryData` per registry with the correct 776 frame; `WriteTags` emits a present empty `ClientboundConfigUpdateTags`.
- Round-trips through the fork's own `bot/` client decoder (`registry.NewNetworkCodec`) yielding `minecraft:overworld` and `minecraft:plains` with no decode error.

## Embedded registries (entry counts — for the 02-04 capture-diff)

| Registry | Entries | Notes |
|----------|---------|-------|
| `minecraft:chat_type` | 7 | full set |
| `minecraft:damage_type` | 51 | full set |
| `minecraft:dimension_type` | 4 | overworld, overworld_caves, the_end, the_nether (nested 26.2 schema) |
| `minecraft:worldgen/biome` | 66 | full set, includes `plains` |
| **Total embedded JSON entries** | **128** | |

Send order (reproducible without the datagen tree): `chat_type`, `damage_type`, `dimension_type`, `worldgen/biome`. Entries within each registry are sorted alphabetically by key.

## Type-faithful conversion approach (chosen: c — carry the vanilla type schema)

Chose **option (c)**, implemented as a generic JSON->`nbt/dynbt` converter where the concrete NBT tag type is decided per value:

- JSON bool -> `TagByte`; JSON string -> `TagString`; JSON object -> `TagCompound`; JSON array -> `TagList`.
- JSON number decoded with `json.Decoder.UseNumber()` so the original token survives. A token with `.`/`e`/`E` is floating-point; an integral token is `TagInt` (`TagLong` only on int32 overflow, which never occurs here).
- **Why this is type-faithful:** Minecraft's Gson datagen serializer is itself type-faithful — float/double fields are always emitted with a decimal point (`coordinate_scale: 1.0`), integer fields never (`min_y: -64`). Verified across all 128 entries: no float/double field is serialized without a decimal, so a bare-integer token always denotes a vanilla `TagInt`.
- **floatFields override:** the one ambiguity Gson cannot express textually is float vs double (`0.1` could be either). An explicit set — `exhaustion`, `temperature`, `downfall`, `creature_spawn_probability`, `probability` — maps the vanilla `Codec.FLOAT` fields to `TagFloat`; everything decimal else defaults to `TagDouble`. The fork's typed `DamageType.Exhaustion float32` decoder confirms `exhaustion` is `TagFloat`.

Rejected option (a) typed-structs: the 26.2 dimension_type `attributes` map and biome `features`/`spawners`/`spawn_costs` are deeply heterogeneous free-form trees that typed structs would not faithfully capture; dynbt represents them exactly.

## TestRegistryNBTTagTypes result (the Wave-2 guard)

Marshals dimension_type `overworld` to network NBT, re-decodes as a concrete `dynbt` tag tree (NOT `nbt.RawMessage`), and asserts:

| Field | Asserted tag | Result |
|-------|-------------|--------|
| `min_y` | TagInt | PASS (not TagDouble) |
| `monster_spawn_block_light_limit` | TagInt | PASS (not TagDouble) |
| `height` | TagInt | PASS |
| `has_skylight` | TagByte | PASS (not TagDouble) |
| `has_ceiling` | TagByte | PASS |
| `ambient_light` | TagDouble | PASS (heuristic does not over-correct) |
| `coordinate_scale` | TagDouble | PASS |
| `attributes` | TagCompound | PASS (nested 26.2 schema survives) |
| `default_clock` | TagString | PASS |

## Task Commits

1. **Task 1: Extract + commit + embed 26.2 registry source** — `a9645646` (feat)
2. **Task 2: WriteRegistryData/WriteTags + round-trip + tag-type tests (TDD)** — `87a6e906` (feat; includes the floatFields GREEN fix)

## Files Created/Modified
- `server/registrydata/embed.go` — `//go:embed registries`, `Load()`, type-faithful `jsonToNBT`/`valueToNBT`/`numberToNBT`, `floatFields` vanilla type override.
- `server/registrydata/send.go` — `PacketWriter` seam, `WriteRegistryData`, `WriteTags`, `entriesEncoder` (mirrors `registry.Registry.WriteTo` frame shape, sources NBT from the embedded content).
- `server/registrydata/registrydata_test.go` — `TestRegistryDataRoundTrip`, `TestRegistryNBTTagTypes`, `TestRegistryDataFrame`, `TestUpdateTagsPresent`.
- `server/registrydata/registries/**` — 128 committed real 26.2 registry JSON entries (incl. `worldgen/biome/plains.json`, nested `dimension_type/overworld.json`).

## Decisions Made
See `key-decisions` frontmatter. The load-bearing ones: dynbt over typed structs for the heterogeneous nested schema; lexical int/decimal split proven against Gson's type-faithful output; explicit float override for the one float-vs-double ambiguity.

## Deviations from Plan

### Adjustments

**1. [Rule 3 - Blocking] floatFields schema override added for vanilla TagFloat fields**
- **Found during:** Task 2 (round-trip test, GREEN phase)
- **Issue:** The initial lexical converter mapped every decimal number to `TagDouble`. `damage_type.exhaustion` (0.1) is a vanilla `TagFloat` (the fork's `DamageType.Exhaustion` is `float32`), so the round-trip decode failed: "cannot parse TagDouble as float32". The same applies to biome `temperature`/`downfall`/`creature_spawn_probability`/`probability` for the real client.
- **Fix:** Added an explicit `floatFields` set (sourced from vanilla codecs + `registry/codec.go` field types) routing those fields to `dynbt.NewFloat` -> `TagFloat`. This is exactly the plan's "carry the vanilla type schema" guidance for fields the lexical heuristic cannot disambiguate.
- **Files modified:** server/registrydata/embed.go
- **Verification:** `TestRegistryDataRoundTrip` now passes (damage_type decodes via the fork float32 decoder); `TestRegistryNBTTagTypes` confirms integer/byte fields remain TagInt/TagByte.
- **Committed in:** 87a6e906 (Task 2 commit)

**2. [Rule 3 - Blocking] Test assertion fields adjusted to the real 26.2 top-level schema**
- **Found during:** Task 2 (writing TestRegistryNBTTagTypes)
- **Issue:** The plan suggested `piglin_safe`/`has_raids`/`respawn_anchor_works` as the byte field to assert, but in the real 26.2 dimension_type those moved into the nested `attributes` map and are no longer top-level. The plan's `min_y`/`monster_spawn_block_light_limit` integer suggestions are still top-level.
- **Fix:** Asserted the authoritative top-level 26.2 byte fields `has_skylight`/`has_ceiling` (Minecraft booleans -> TagByte) alongside the integer fields. The guard's intent (a known byte field is TagByte, never TagDouble) is fully satisfied.
- **Files modified:** server/registrydata/registrydata_test.go
- **Verification:** TestRegistryNBTTagTypes passes asserting has_skylight/has_ceiling = TagByte.
- **Committed in:** 87a6e906 (Task 2 commit)

**3. [Rule 3 - Blocking] Registry id-order not sourced from registries.json**
- **Found during:** Task 1
- **Issue:** The plan said to source the send id-order from `registries.json`, but that file contains only the code-driven "known packs" registries (block, item, etc.) — none of the four datapack registries (dimension_type, biome, damage_type, chat_type) appear in it.
- **Fix:** Used a deterministic, reproducible send order encoded in `registryDirs` (chat_type, damage_type, dimension_type, worldgen/biome), with entries sorted alphabetically. The exact vanilla set/order is confirmed by the 02-04 capture-diff, as the plan already designates.
- **Files modified:** server/registrydata/embed.go
- **Verification:** TestRegistryDataFrame asserts each packet's registry id matches the send order.
- **Committed in:** a9645646 (Task 1 commit)

---

**Total deviations:** 3 (all Rule 3 - blocking/missing-info, auto-resolved). No architectural changes.
**Impact on plan:** All three were necessary for correctness; none expand scope. The floatFields override is the substantive one — it is the second half of the same typing trap the plan flagged (the plan emphasized integer->TagInt; the float->TagFloat direction surfaced via the round-trip and is handled with the same type-schema mechanism).

## Issues Encountered
- The float-vs-double ambiguity (Deviation 1) was the only real issue; resolved with the floatFields override. The new 26.2 attribute system introduces undocumented visual-modifier fields (e.g. `water_fog_end_distance.argument`) that default to TagDouble; their exact wire type is deferred to the 02-04 capture-diff per threat T-2-06 (final byte-correctness).

## Threat Mitigations
- **T-2-06 (stale/malformed registry, float64->TagDouble):** mitigated — real 26.2 jar-derived NBT embedded; type-faithful dynbt path; Wave-2 tag-type guard asserts TagInt/TagByte (never TagDouble); round-trip asserts overworld+plains decode.
- **T-2-09 (runtime reads gitignored build artifact):** mitigated — registry files committed under `server/registrydata/registries/` and `//go:embed`-ed; `Load()` reads only the embedded FS; verify confirms no `temp/` reference in the package Go source.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- NET-04 payload is complete and round-trip-verified, ready for **02-04** to wire `WriteRegistryData`/`WriteTags` into the Configuration sequence.
- Independent package with no overlap into 02-02's files (server.go/handshake.go/cmd) — confirmed: 02-02 committed in parallel without conflict.
- Open item for 02-04: capture-diff against a real vanilla 26.2 server confirms exact registry set/order, the minimal real Update Tags body, and byte-correctness of the new 26.2 attribute-system fields.

## Gate Results
- `go build ./...` — OK
- `go vet ./...` — OK
- `go test ./server/registrydata/...` — OK (TestRegistryDataRoundTrip, TestRegistryNBTTagTypes, TestRegistryDataFrame, TestUpdateTagsPresent all pass)
- `go test -race ./server/registrydata/...` (Docker golang:1.26, CGO_ENABLED=1) — OK
- `! grep -rq 'temp/' server/registrydata/*.go` — OK (no runtime build-artifact dependency)

## Self-Check: PASSED

- Files verified on disk: embed.go, send.go, registrydata_test.go, registries/worldgen/biome/plains.json, registries/dimension_type/overworld.json — all FOUND.
- Commits verified in git: a9645646 (Task 1), 87a6e906 (Task 2) — all FOUND.
- All plan `<verification>` gates re-run green (build/vet/test/-race/no-temp).

---
*Phase: 02-net-protocol-state-machine*
*Completed: 2026-06-23*
