---
phase: 29-damage-keystone-s2
plan: 01
subsystem: codegen-tags
tags: [codegen, damage, tags, jar-port, data-driven, mob-sub-03]
requires:
  - "tools/ codegen pipeline (gen_item_food.go / ExtractAll.java / GenEntities.java patterns)"
  - "data/registryid/item.go (item resource-id -> protocol-id resolution via registries.json)"
  - "temp/cache/26.2-inner.jar (the frozen jar; data/minecraft/tags/{damage_type,item}/ JSON)"
provides:
  - "data/tag package: DamageTypeTags + ItemTags membership tables (map[string]map[int32]bool)"
  - "data/tag: DamageTypeIDs (name->id) + DamageTypeNames (id->name) for the dynamic damage_type registry"
  - "tools/java/GenTags.java extractor + tools/gen_tags.go generator (reusable shared tag tooling)"
affects:
  - "Phase 29 Plan 02/03 (damageSource.is(\"bypasses_armor\")/.is(\"panic_causes\") read DamageTypeTags)"
  - "Phase 32 (TemptGoal) consumes ItemTags (pig_food/cow_food/wolf_food) — front-loaded here"
tech-stack:
  added: []
  patterns:
    - "ZipFile jar-read extractor (mirrors ExtractAll.java) with recursive nested-tag flatten"
    - "JSON-read -> sorted-rows -> generatedHeader -> format.Source -> writeFile generator (mirrors gen_item_food.go)"
    - "dynamic-registry id assignment via sorted-element index (damage_type has no static protocol id)"
key-files:
  created:
    - "tools/java/GenTags.java"
    - "tools/gen_tags.go"
    - "data/tag/tags.go (generated)"
    - "data/tag/tags_test.go"
  modified:
    - "tools/main.go (registered {\"tags\", genTags})"
    - "tools/java/ExtractAll.java (added GenTags to runCustomExtractors list)"
decisions:
  - "damage_type ids assigned by sorted-element index (minecraft:damage_type is a dynamic/datapack registry absent from registries.json; the id is internal server-side DamageSource state, never a wire registry id, so a deterministic generated ordering is faithful + reproducible)"
  - "membership maps key by int32 id (the plan contract); DamageTypeIDs/DamageTypeNames expose the name<->id mapping so consumers + tests never hardcode magic numbers"
  - "recursive #-ref flatten done at EXTRACTION time in GenTags.java (TagLoader.build semantics), so the emitted tags.json + generated Go are already flat"
metrics:
  duration: ~35min
  completed: 2026-06-29
---

# Phase 29 Plan 01: Codegen Tag Tooling (Damage-Type + Item Tags) Summary

Built the shared codegen tag tooling that emits genuine, jar-extracted damage-type AND item tag
membership tables into a new `data/tag/` package — making `source.is("bypasses_armor")` /
`source.is("panic_causes")` real reads against the frozen 26.2 jar data (the MOB-SUB-03 keystone
deliverable), NOT `const false` stubs (PITFALLS Pitfall 3). Item-food tags are generated-but-unwired
here; their consumer is Phase 32 (intentional shared-tooling front-load per CONTEXT Grey Area 1).

## What Shipped

- **`tools/java/GenTags.java`** — a ZipFile jar-read extractor (mirrors `ExtractAll.java`) that reads
  every `data/minecraft/tags/damage_type/*.json` (34 tags) and `data/minecraft/tags/item/*.json`
  (224 tags) from the inner jar, **recursively flattens `#`-prefixed nested tag references**
  (e.g. `#minecraft:is_player_attack` inside `panic_causes.json`) exactly as
  `net.minecraft.tags.TagLoader.build` does (cycle-guarded), and also emits the 51-element
  `minecraft:damage_type` registry set. Output is a flat `tags.json` (hand-written PrintWriter +
  `jsonStr`, GSON for parse). Locates the inner jar via the classpath; registered in
  `ExtractAll.runCustomExtractors`.
- **`tools/gen_tags.go`** — the generator (mirrors `gen_item_food.go`): reads `tags.json` +
  `registries.json`, resolves item member ids via the `minecraft:item` registry protocol_id, assigns
  damage-type ids by sorted-element index, and emits `data/tag/tags.go` with sorted, deterministic
  rows. Registered as `{"tags", genTags}` in `tools/main.go`.
- **`data/tag/tags.go`** (generated) — `DamageTypeTags` (34 tags), `ItemTags` (224 tags), both
  `map[string]map[int32]bool`, plus `DamageTypeIDs` (name->id) and `DamageTypeNames` (id->name) for
  the 51 damage-type elements.
- **`data/tag/tags_test.go`** — `TestTagMembership` (bypasses_armor positive fall/magic/drown +
  negative player_attack), `TestNestedTagFlatten` (panic_causes superset of is_player_attack —
  the nested-flatten proof), `TestItemTagsGenerated` (pig_food contains carrot/potato/beetroot).

## Verification

- `CGO_ENABLED=0 go build ./...` clean; `CGO_ENABLED=0 go vet ./data/tag/ ./server/` clean.
- `CGO_ENABLED=0 go test ./data/tag/` green (all 3 tests).
- Deterministic: re-running `genTags` produces a byte-identical `data/tag/tags.go` (sha1 match).
- `go list -deps ./... | grep -i gopy` empty (no cgo/dep leak; no new Go deps).
- Pig oracle `TestPluginPigEqualsGoNativePig` still green (no server code touched this plan).
- Nested-flatten validated directly against the jar JSON: `panic_causes` (29 members) contains
  `player_attack`+`spear` (from `#is_player_attack`) and `cactus` (from
  `#panic_environmental_causes`), with zero `#` literals remaining; `wolf_food` flattened `#meat`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Codegen pipeline cannot run full-list against the cached jsons**
- **Found during:** Task 2 (running `cd tools && go run . --version 26.2`).
- **Issue:** The cached `temp/jsons/26.2/` is missing `block_support.json` (its custom extractor
  output was never cached), so the full generator list aborts at the `blocksupport` step before
  reaching `tags`. This is a pre-existing cache gap, NOT caused by this task (the repo's
  `block_support.go` came from a prior full Docker run).
- **Fix:** Ran the `genTags` generator in isolation (temporary single-entry generators slice, then
  restored `main.go` to the full 15-generator list) to produce `data/tag/tags.go`. Also ran
  `GenTags.java` standalone (Zulu 25 + the jar's GSON lib) to produce `tags.json`, since cached
  extraction skips the Java extractors. `main.go` is committed with the correct full list +
  `{"tags", genTags}`.
- **Files modified:** none beyond the planned set (the swap was reverted).
- **Commit:** 928b9287

### Design choice (within plan discretion)

**damage_type id source:** the plan assumed ids resolve "via the same registry-id data
gen_registryid uses." But `minecraft:damage_type` is a DYNAMIC (datapack) registry and is **absent
from `registries.json`** (verified: `data/registryid/` has no damage_type file). Its protocol ids
are assigned at server load, not in the static report, and `datapack.json` reports only
`elements: true` (a presence flag, no ordering). Resolution: `GenTags.java` emits the full
damage_type element set; `gen_tags.go` assigns each a deterministic id = its sorted-element index,
and exports `DamageTypeIDs`/`DamageTypeNames`. The damage-type id is internal server-side
`DamageSource` state (never a wire registry id), so a stable generated ordering is faithful and
reproducible. Consumers (P02/P03) and the tests resolve by NAME, never by a hardcoded id.

## Known Stubs

None. Both tag families carry real, jar-flattened membership and are tested headless. ItemTags is
generated-but-unwired by design (consumer is Phase 32) — documented in the generator header and
CONTEXT Grey Area 1; this is a shared-tooling front-load, not a built-but-unwired violation.

## Self-Check: PASSED

- data/tag/tags.go — FOUND
- data/tag/tags_test.go — FOUND
- tools/gen_tags.go — FOUND
- tools/java/GenTags.java — FOUND
- tools/main.go contains {"tags", genTags} — FOUND
- commits 53d67c17, cc39d679, 928b9287 — FOUND
