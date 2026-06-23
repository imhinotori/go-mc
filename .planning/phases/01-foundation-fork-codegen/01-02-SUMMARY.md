---
phase: 01-foundation-fork-codegen
plan: 02
subsystem: codegen
tags: [codegen, protocol-776, mc-26.2, jdk25, jar-sha1, extractor-retarget, GEN-02, GEN-03]

# Dependency graph
requires:
  - phase: 01-01
    provides: forked go-mc @539b4a3a (#294-296), two-module layout, JDK-25 extractor container, baseline-774/ snapshot
provides:
  - Committed, compiling proto-776 Go source generated from the sha1-verified official 26.2 server jar
  - 26.2-retargeted extractors (EitherHolder removal + ItemStackTemplate mapping) and items.json synthesis for the 26.2 per-item report layout
  - Jar-integrity sha1 gate in download.go (T-1-01) that aborts extraction on mismatch
affects: [01-03 (GEN-04 774->776 diff; this is the "after" side), all downstream server work consuming packet/registry/block/item/entity/component/sound data]

# Tech tracking
tech-stack:
  added:
    - "stringer (golang.org/x/tools/cmd/stringer) — required to regenerate data/packetid/*_string.go after packet-ID regen"
  patterns:
    - "Jar integrity gate: verify Mojang-manifest sha1 before executing any extractor over the jar"
    - "Extractor type-mapping by class simple-name (resilient to package moves) rather than compile-time class imports for volatile MC types"
    - "Report-shape adaptation in ExtractAll.java (synthesize legacy consolidated JSON from new per-item report tree) instead of rewriting Go generators"

key-files:
  created:
    - "level/component/sulfurcubecontent_gen.go + 11 other new 26.2 component files (catsoundvariant, chickensoundvariant, cowsoundvariant, pigsoundvariant, additionaltradecost, dye, ...)"
  modified:
    - "tools/download.go (added sha1 verification of server jar before extraction — T-1-01)"
    - "tools/java/GenComponentSchema.java (drop removed EitherHolder import; map ItemStackTemplate -> SlotData)"
    - "tools/java/ExtractAll.java (synthesize items.json from reports/minecraft/components/item/*.json)"
    - "262 generated files across data/{packetid,registryid,item,entity,soundid,lang}, level/{block,component,biome}"
    - "data/packetid/{clientbound,serverbound}packetid_string.go (regenerated via stringer)"

key-decisions:
  - "Fixed the EitherHolder removal in the EXTRACTOR by simple-name guard + verified via javap that 26.2 variant/damage components now use plain Holder<X> (VarInt) — behavior-correct, not just compile-fixing"
  - "Mapped 26.2 ItemStackTemplate to SlotData after confirming via javap its wire shape is identical to ItemStack (Holder<Item> + VarInt count + DataComponentPatch)"
  - "Synthesized items.json in ExtractAll.java rather than rewriting gen_item.go — preserves the existing generator contract and copies per-item JSON bodies verbatim (no re-encode/precision loss)"
  - "Added the sha1 gate to download.go itself (manifest-supplied checksum) so every future run is gated, not just this one"

patterns-established:
  - "Codegen for natively-unobfuscated MC (major>=26) flows through the standard Mojang manifest with no unobfuscated_versions.json entry"
  - "javap-in-container is the authoritative way to resolve a 26.2 class rename/removal during an extractor fix"

requirements-completed: [GEN-02, GEN-03]

# Metrics
metrics:
  duration: ~12m
  tasks-completed: 3
  files-committed: 265
  completed-date: 2026-06-23
---

# Phase 1 Plan 2: 26.2 Codegen Run Summary

Ran the retargeted go-mc codegen pipeline against the official, sha1-verified Minecraft 26.2 server jar inside the JDK-25 extractor container, fixed the two genuine 26.2 breaks (an extractor class removal and a restructured items report), and committed authoritative, compiling proto-776 Go source for packets, registries, blocks/states, items, entities, components, and sounds — no hand transcription.

## What Was Built

- **Jar integrity gate (T-1-01):** `download.go` now resolves the server jar's expected sha1 from the Mojang manifest and verifies it (download path and cached path) before any extractor runs; mismatch aborts and removes the bad file. The 26.2 jar verified to `823e2250d24b3ddac457a60c92a6a941943fcd6a`.
- **26.2 proto-776 data** generated into the runtime module's standard paths and committed.

## Task-by-Task

**Task 1 — Dry-run + full extract with sha1 verification.**
- `--dry-run` resolved 26.2 from `version_manifest_v2.json` (latest.release) and printed the server jar URL. 141 lang files downloaded.
- Jar sha1 verified: `823e2250d24b3ddac457a60c92a6a941943fcd6a` (exact match; recorded).
- `--extract` ran the JDK-25 container: `--all` data generator + 6 reflection extractors, then 10 Go generators. Container exited 0.
- The `--source 21` launcher flag (deferred Wave-1 item) was fine: `ExtractAll.java` uses only JDK-21-compatible features. No change needed.

**Task 2 — Conditional extractor fixes (REQUIRED for 26.2).** Two distinct 26.2 changes surfaced, both LOUD as expected:
1. **`EitherHolder` removed.** `GenComponentSchema.java` failed javac at 4 sites: `cannot find symbol: class EitherHolder` (was `net.minecraft.world.item.EitherHolder`). `unzip -l` confirmed the class is gone entirely (no `*EitherHolder` successor). `javap` on `ChickenVariant` / `DataComponents` confirmed the 26.2 wire form: `DAMAGE_TYPE`, `CHICKEN_VARIANT`, `ZOMBIE_NAUTILUS_VARIANT` are now `DataComponentType<Holder<X>>` — plain `Holder<X>` (VarInt), not `Either<Holder,ResourceKey>`. **Fix:** dropped the dead import; converted the 3 compile-breaking `EitherHolder.class.isAssignableFrom(...)` guards to simple-name checks. The variant components now correctly classify as `Holder<X> -> pk.VarInt`.
   - Rename/removal recorded: `net.minecraft.world.item.EitherHolder` -> **removed in 26.2** (components migrated to plain `Holder<X>`).
2. **`items.json` no longer emitted by `--all`.** 26.2 split the consolidated `reports/items.json` into per-item files `reports/minecraft/components/item/<id>.json` (1537 files), which broke `gen_item.go` ("items.json file not found"). **Fix:** `ExtractAll.java` now synthesizes `items.json` (map of `minecraft:<id>` -> `{components:{...}}`) from the per-item tree when the consolidated report is absent, copying each body verbatim. Synthesized `items.json` = 979 KB.

Re-ran `--extract`: clean. All 6 extractors compiled; `entities.json`, `components.json`, `component_schema.json`, `block_properties.json`, and synthesized `items.json` present. No `extractor compilation failed`.

**Task 3 — Build + commit generated 776 source.** Initial build surfaced two more real gaps (both fixed, not hand-edited):
- **`ItemStackTemplate` undefined** in `level/component/sulfurcubecontent_gen.go`. `sulfur_cube_content` (component id 78) is **genuine new 26.2 vanilla content** (verified present across the sha1-locked jar's registries/sounds/items — sulfur blocks, sulfur cube mob, etc.). `javap` showed `SulfurCubeContent` is a single-field record wrapping `ItemStackTemplate absorbedBlockItemStack`, and `ItemStackTemplate` has the identical wire shape to `ItemStack` (`Holder<Item>` + VarInt count + `DataComponentPatch`). **Fix in the extractor:** map `ItemStackTemplate -> SlotData`. Regenerated `type SulfurCubeContent struct{ SlotData }`.
- **Stale stringer tables.** `data/packetid/*_string.go` are `go:generate stringer` output, not pipeline output; the regenerated packet IDs invalidated their compile-time index assertions. **Fix:** installed `stringer` and re-ran `go generate ./data/packetid/`.

Final gates: `go build ./data/... ./level/...`, `go vet ./data/... ./level/...`, and `go build ./...` all exit 0 with Go alone. Working tree clean apart from planning docs.

## 776 Generated-Tree Counts (the "after" side for the GEN-04 diff)

| Tree | Files | Go lines | Notable counts |
|------|-------|----------|----------------|
| data/packetid | 4 | 656 | 776 packet IDs (+ regenerated stringer tables) |
| data/registryid | 96 | 7566 | 95 registries, 6979 entries |
| data/item | 1 | 10781 | 1537 items |
| data/entity | 1 | 1604 | 158 entities |
| data/soundid | 1 | 1985 | 1968 sounds |
| level/block | 10 | 8542 | 1196 blocks, 32366 block states (block_states.nbt 125011 B), 30 property enums |
| level/component | 126 | 3721 | 111 components, 106 type files |
| level/biome | 1 | 120 | 66 biomes |
| data/lang | 152 | 1154257 | 141 language tables |

## Deviations from Plan

All within deviation rules (no architectural escalation needed).

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `items.json` removed from 26.2 `--all` reports**
- **Found during:** Task 1 (first extract; `gen_item.go` errored "items.json not found").
- **Issue:** 26.2 replaced consolidated `reports/items.json` with per-item `reports/minecraft/components/item/<id>.json` (1537 files).
- **Fix:** `ExtractAll.java` synthesizes the expected `items.json` from the per-item tree (verbatim bodies). Preserves the `gen_item.go` contract.
- **Files modified:** tools/java/ExtractAll.java
- **Commit:** 33e3ce39

**2. [Rule 1 - Bug] `EitherHolder` extractor import broken by 26.2 class removal**
- **Found during:** Task 2 (javac: 4x `cannot find symbol: class EitherHolder`).
- **Issue:** `net.minecraft.world.item.EitherHolder` removed in 26.2; variant/damage components migrated to plain `Holder<X>` (confirmed via javap).
- **Fix:** removed dead import; simple-name guards. Components now classify as `Holder -> pk.VarInt`.
- **Files modified:** tools/java/GenComponentSchema.java
- **Commit:** 33e3ce39

**3. [Rule 1 - Bug] `ItemStackTemplate` had no Go type mapping (new 26.2 component)**
- **Found during:** Task 3 (`undefined: ItemStackTemplate`).
- **Issue:** new 26.2 vanilla component `sulfur_cube_content` wraps `ItemStackTemplate`; extractor emitted the bare simple name with no Go equivalent.
- **Fix:** map `ItemStackTemplate -> SlotData` in the extractor (verified identical wire shape to ItemStack).
- **Files modified:** tools/java/GenComponentSchema.java; regenerated level/component/sulfurcubecontent_gen.go
- **Commit:** 33e3ce39 (extractor) / dfed3358 (generated)

**4. [Rule 2 - Missing critical functionality] No jar sha1 gate (T-1-01)**
- **Found during:** Task 1 read-first (download.go discarded the manifest sha1).
- **Issue:** the threat register requires verifying the jar before executing it; the pipeline did not.
- **Fix:** thread the manifest/unobfuscated sha1 through `resolveServerJarURL` and verify in `downloadServerJar` (download and cache paths), aborting on mismatch.
- **Files modified:** tools/download.go
- **Commit:** 33e3ce39

**5. [Rule 3 - Blocking] `stringer` not installed**
- **Found during:** Task 3 (`go:generate stringer` for packetid).
- **Issue:** regenerated packet IDs invalidated committed `*_string.go` index assertions; stringer absent.
- **Fix:** `go install golang.org/x/tools/cmd/stringer@latest`; re-ran `go generate ./data/packetid/`.
- **Files modified:** data/packetid/clientboundpacketid_string.go, serverboundpacketid_string.go (regenerated)
- **Commit:** dfed3358

## Authentication Gates

None.

## Known Stubs

None. All generated files are wired and compile; the previously-empty `embedType` for `sulfur_cube_content` is now a real `SlotData` embed.

## Self-Check: PASSED

- tools/download.go, tools/java/GenComponentSchema.java, tools/java/ExtractAll.java — present, committed (33e3ce39).
- level/component/sulfurcubecontent_gen.go — present, committed (dfed3358), compiles.
- data/packetid, data/registryid, level/block non-empty; `go build ./...` and `go vet ./data/... ./level/...` exit 0.
- Commits 33e3ce39 and dfed3358 exist in git log.
