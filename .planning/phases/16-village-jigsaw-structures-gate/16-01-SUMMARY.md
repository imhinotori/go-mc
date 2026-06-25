---
phase: 16-village-jigsaw-structures-gate
plan: 01
subsystem: worldgen-structures
tags: [structures, villages, structure-template, nbt-template, template-pool, processor-list, rule-processor, jigsaw, rotation, mirror, place-in-world, legacy-single-pool-element, cross-chunk, protocol-776, STRUCT-05]

# Dependency graph
requires:
  - phase: 14-03
    provides: "the igloo OFFLINE-extraction seam (igloo_data.go + igloo.go resolveIglooState): the proven .nbt schema (size / palette of {Name,Properties} / blocks of {pos,paletteIndex}) + the block.State{Name,Properties}.Block() -> block.ToStateID palette->StateID resolution this plan GENERALIZES to a runtime parser"
  - phase: 14-02
    provides: "piece.go transformState (mirror-then-rotate the block-state facing; stairs SHAPE flip; vine/redstone/tripwire compass permutation), Rotation/Mirror enums, WorldGenView, BoundingBox (IsInside/Intersects) — all REUSED, not re-implemented"
  - phase: 9 (PARITY-01)
    provides: "tools/extract_worldgen.go (pure-unzip copyZipEntry) + world/levelgen/data/embed.go (the //go:embed + typed-accessor pattern) this plan EXTENDS with three new trees + the empty.json single-file"
provides:
  - "the runtime StructureTemplate: ParseTemplate (gzip+nbt via the existing nbt pkg) -> size/palette/blocks/entities; the palette resolved to []StateID once at load (FAIL LOUD on unknown block, T-16-02)"
  - "PlaceInWorld: the template-PIVOT transform (transformPos / calculateRelativePosition, CFR StructureTemplate.transform — DISTINCT from piece.go getWorldX/Y/Z) + the 14-02 transformState for the state, clipped to the writable box, with the processor chain applied; jigsaw blocks -> air (LegacySinglePoolElement), structure_void/data markers skipped"
  - "BoundingBoxAt (CFR getBoundingBox — the rotated/mirrored extent) + Jigsaws (CFR getJigsaws — the connector pool/target/joint/front-face) — both consumed by the 16-02 jigsaw Placer"
  - "the minecraft:rule processor + RuleTest impls (always_true/block_match/blockstate_match/random_block_match/tag_match) + the processor_list JSON loader; the per-block seed via ported Mth.getSeed"
  - "the embedded village DATA: 483 village .nbt StructureTemplates (binary, verbatim) + 62 village template_pool JSONs + the top-level empty.json terminator + 40 processor_list JSONs, with typed accessors StructureTemplateNBT/TemplatePoolJSON/ProcessorListJSON + ID lists"
affects:
  - "16-02 (the jigsaw Placer + village StartGenerator): consumes ParseTemplate/PlaceInWorld/BoundingBoxAt/Jigsaws + LoadProcessorList + TemplatePoolJSON('empty')"

# Tech tracking
tech-stack:
  added: []  # zero new deps — reused nbt + level/block + world/levelgen/data + piece.go
  patterns:
    - "the igloo OFFLINE shortcut generalized to a RUNTIME parser: igloo_data.go baked the palette/blocks into Go literals + igloo.go re-marshalled a Go prop-map to a bare compound (strip the 3-byte nbt.Marshal header). The runtime .nbt path is SIMPLER — the nbt decoder already captures the palette's Properties compound as a bare RawMessage, so the palette entry IS a block.State directly (rawTemplate.Palette []block.State); no header-strip needed."
    - "the template-PIVOT transform (transformPos) is a NEW transform DISTINCT from piece.go's scattered-piece getWorldX/Y/Z. The .nbt placer rotates a LOCAL pos about a (px,pz) pivot (mirror first, then rotate) via the exact jar formulas; the $SwitchMap$Rotation synthetic remap (decoded from StructureTemplate$1.<clinit>: CCW90->1, CW90->2, CW180->3) was needed to map the bytecode switch cases to the real enum constants — trusting the case numbers as ordinals would have swapped CW90/CCW90."
    - "all 16 village-referenced processor_lists are minecraft:rule lists (verified across farm_*/mossify_*/street_*/zombie_*), so only the RuleProcessor is ported. The per-block RandomSource is a fresh LegacyRandomSource(Mth.getSeed(pos)) — NOT the place-time worldgen rng — so the random_block_match draw is positionally deterministic + order-independent (jar-faithful)."

key-files:
  created:
    - world/structure/template.go
    - world/structure/template_test.go
    - world/structure/template_processor.go
    - world/structure/template_processor_test.go
    - world/levelgen/data/embed_template_test.go
  modified:
    - tools/extract_worldgen.go
    - world/levelgen/data/embed.go

key-decisions:
  - "PALETTE = []block.State DIRECTLY (the runtime simplification): the .nbt palette stores Properties as the bare compound payload with string-valued props — exactly what block.State.Properties wants. The nbt decoder captures it as a RawMessage via RawMessage.UnmarshalNBT (TeeReader), so rawTemplate.Palette decodes straight into []block.State and resolveTemplateState just calls .Block() -> ToStateID. An unknown block name returns an error the caller surfaces (T-16-02 FAIL LOUD — never a silent air-fill)."
  - "THE TEMPLATE-PIVOT TRANSFORM ported from bytecode, NOT guessed: transformPos uses the exact StructureTemplate.transform formulas (CCW90: px-pz+z,y,px+pz-x | CW90: px+pz-z,y,pz-px+x | CW180: 2px-x,y,2pz-z), with the $SwitchMap$Rotation synthetic remap decoded from StructureTemplate$1 static-init to map the switch CASE numbers to the real enum constants. calculateRelativePosition = transform about settings.getRotationPivot() (default BlockPos.ZERO, verified via javap). The standalone PlaceInWorld takes (pivotX,pivotZ) so 16-02 can supply a pivot."
  - "JIGSAW BLOCK -> AIR (LegacySinglePoolElement): a jigsaw palette block places as stateAir at place time; its pool/target/joint/front-face are surfaced separately via Jigsaws (parsing the jigsaw block-entity nbt {name,pool,target,final_state,joint} + the orientation prop's front face, rotated). structure_void + structure_block DATA markers are skipped (handleDataMarker). Pinned by TestTemplateJigsawToAir (both house jigsaws resolve to air; pools = streets + villagers)."
  - "ONLY minecraft:rule PROCESSORS (the village set): the 5 RuleTest predicates villages use are ported (always_true / block_match / blockstate_match / random_block_match / tag_match); an unsupported processor_type or predicate_type FAILS LOUD. #minecraft:doors is the only block tag villages reference (zombie_* -> air); block tags aren't a runtime subsystem yet, so doorBlockNames is built from the registry (every *_door id) with a fail-loud guard on an unlisted tag."
  - "THE empty.json TERMINATOR as a worldgenSingleFiles entry (the plan-check catch): the minecraft:empty fallback-chain terminator lives at data/minecraft/worldgen/template_pool/empty.json — OUTSIDE village/, so the village-only prefix never copies it. 42 of the 62 village pools reference it; without it 16-02's data.TemplatePoolJSON('empty') would not-found and the jigsaw would never terminate. TemplatePoolJSON('empty') resolves it (asserted in the embed test)."
  - "ZERO NEW DEPS + .nbt RIDES THE EXISTING structure EMBED: //go:embed structure already recurses, so the binary village .nbt land under structure/village/ with NO new directive beyond `template_pool processor_list`. go.mod/go.sum UNCHANGED. CGO_ENABLED=0 clean. The extractor reused copyZipEntry verbatim for the binary .nbt (no new code path — D-RESEARCH 'binary = verbatim')."

patterns-established:
  - "RUNTIME .nbt template parsing for any future data-driven structure (bastion/mansion/trail-ruins are v3): ParseTemplate + PlaceInWorld + BoundingBoxAt + Jigsaws is the reusable templatesystem; a new structure family adds an extractor prefix (scoped, like village/) + calls LoadTemplate."
  - "decode-the-$SwitchMap discipline: when porting a Mojang switch over an enum from bytecode, the tableswitch case numbers are the COMPILER's synthetic $SwitchMap remap, NOT the enum ordinals — decode the SwitchMap class's <clinit> to map cases to constants before transcribing the branches."

# Metrics
metrics:
  duration: ~50m
  tasks-completed: 2
  files-created: 5
  files-modified: 2
  commits: 2
  completed: 2026-06-25
---

# Phase 16 Plan 01: .nbt StructureTemplate System Summary

The data-driven geometry layer villages need (STRUCT-05 part 1): a runtime `.nbt`
`StructureTemplate` parser + `PlaceInWorld` placer with place-time rotation/mirror + the
village block-replace processors, plus the embedded 483 village `.nbt` + 62 village
`template_pool` + the `empty.json` terminator + 40 `processor_list`. Generalizes the 14-03
igloo offline-extraction shortcut to a runtime parser; isolated from the 16-02 jigsaw
Placer (this delivers "place a template at a position+rotation"; 16-02 decides which
templates go where).

## What landed

| File | Lines | Role |
|------|-------|------|
| `world/structure/template.go` | 389 | `StructureTemplate` parse (gzip+nbt) + `PlaceInWorld` (pivot transform + processors + clip) + `BoundingBoxAt` + `Jigsaws` + palette->StateID resolution |
| `world/structure/template_processor.go` | 326 | the `minecraft:rule` processor + 5 `RuleTest` impls + `processor_list` JSON loader + ported `Mth.getSeed` |
| `world/structure/template_test.go` | 173 | parse pin (7x7x7/24/343), NONE+CW90 fingerprints, rotated bbox, jigsaw->air, clip, fail-loud |
| `world/structure/template_processor_test.go` | 158 | mossify/zombie-tag processor transforms, chain through PlaceInWorld, unsupported-type fail-loud, Mth.getSeed |
| `world/levelgen/data/embed_template_test.go` | 146 | embed gzip-magic + pool/processor parse + empty terminator + counts 483/62/40 |
| `tools/extract_worldgen.go` | +28 | 3 new prefixes (`structure/village`, `template_pool/village`, `processor_list`) + `empty.json` single-file |
| `world/levelgen/data/embed.go` | +66 | `//go:embed template_pool processor_list` + 5 accessors + `listNested` |

Plus the extracted village DATA committed alongside (Task 1): **483** village `.nbt` +
**62** village `template_pool` JSONs + the top-level **empty.json** + **40**
`processor_list` JSONs.

## Embed counts confirmed

| Tree | Count | Confirmed by |
|------|-------|--------------|
| village `.nbt` StructureTemplates | **483** | `countNBT(structure/village)` == 483; jar zip-count == 483 |
| village `template_pool` JSONs | **62** | `TemplatePoolIDs()` == 62 (common 6 + desert 12 + plains 11 + savanna 12 + snowy 11 + taiga 10) |
| top-level `empty.json` terminator | **present** | `TemplatePoolJSON("empty")` resolves |
| `processor_list` JSONs | **40** | `ProcessorListIDs()` == 40 |

A known `.nbt` begins with the gzip magic `0x1f 0x8b`; a known pool + processor parse as
JSON; `plains_small_house_1.nbt` round-trips (size 7x7x7, palette 24, 343 blocks, NONE
fingerprint pinned, CW90 fingerprint + bbox rotated, 2 jigsaws -> air).

## Verification gates (all 7 PASS)

| # | Gate | Result |
|---|------|--------|
| 1 | `go build ./...` | PASS (exit 0) |
| 2 | `CGO_ENABLED=0 go build ./...` | PASS (exit 0) |
| 3 | `go vet ./world/...` | PASS (clean) |
| 4 | `go test ./world/levelgen/data/ -run TestEmbed -count=1` | PASS |
| 5 | `go test ./world/structure/ -run 'TestTemplate\|TestProcessor' -count=1` | PASS |
| 6 | `cd tools && go build ./...` | PASS (exit 0) |
| 7 | Docker `-race` (`golang:1.26`, `-timeout 1800s`, structure + data) | PASS (11.1s / 1.3s) |

`go.mod`/`go.sum` **UNCHANGED** (zero new deps). No encoder/packet/chunk-wire/golden file
touched. Full `world/structure` package suite passes (no regression).

## Deviations from Plan

### Auto-fixed / adapted (none required user input)

**1. [Adaptation] Palette decodes directly into `[]block.State` (simpler than igloo.go).**
- The plan anticipated copying igloo.go's strip-root-header dance (re-marshal a Go
  prop-map, strip the 3-byte `nbt.Marshal` header). At runtime that is unnecessary: the
  nbt decoder captures the palette's `Properties` compound as a bare `RawMessage`, which is
  exactly what `block.State.Properties` consumes. So `rawTemplate.Palette` is `[]block.State`
  directly and `resolveTemplateState` just calls `.Block()`. (The strip-header pattern IS
  still used in `resolveJSONState` for processor `output_state`, which DOES come from a Go map.)
- Files: `world/structure/template.go`, `world/structure/template_processor.go`.

**2. [Rule 3 - Blocking] Decoded the `$SwitchMap$Rotation` synthetic remap.**
- The `StructureTemplate.transform` bytecode switch cases are the compiler's synthetic
  `$SwitchMap` remap, not enum ordinals. Trusting the case numbers would have swapped
  CW90/CCW90. Decoded `StructureTemplate$1.<clinit>` (CCW90->1, CW90->2, CW180->3) to map
  cases to constants before transcribing — the formulas now match the canonical Mojang
  pivot-rotation. Verified by the CW90 fingerprint + rotated bbox pins.

**3. [Reuse] Shared test helpers `mapView`/`fingerprint` reused.**
- The plan's tests originally declared their own `captureView`/`fingerprint`; these
  collided with the existing `piece_test.go`/`desert_pyramid_test.go` helpers. Reused the
  shared `mapView` + `fingerprint(*mapView)(int,uint64)` so the template placer is
  exercised through the SAME `WorldGenView` the pieces use; pinned the fingerprints the
  shared hash produces.

### Out of scope (logged, not fixed)

Re-running the full extractor (needed to produce the village data) surfaced **19 new 26.2
`worldgen/biome` category tags** never committed in Phase 14/15 (spawn/mob/ocean-monument
tags — a pre-existing extraction gap, NOT STRUCT-05). Logged to
`.planning/phases/16-village-jigsaw-structures-gate/deferred-items.md`. No build impact
(all 7 gates pass); 16-01 references none of them. A follow-up biome-tag plan should commit
them.

## Threat surface

- **T-16-01 (gzip bomb on .nbt)** — accepted per plan: templates are pinned build-time data,
  not network input.
- **T-16-02 (unknown palette block -> silent air)** — MITIGATED: `resolveTemplateState`
  returns an error on an unknown block name; `ParseTemplate` surfaces it (template fails to
  load). Pinned by `TestTemplateUnknownBlockFailsLoud` + the processor `resolveJSONState`
  fail-loud + `TestProcessorUnsupportedFailsLoud`. No new threat surface beyond the register.

## Known Stubs

- **Entities stored, not placed** (`StructureTemplate.entities`): village `.nbt` entity
  lists are parsed + retained but NOT spawned — entities are a v3 deferral matching the
  14/15 precedent. `plains_small_house_1` has 0 entities; the field exists for 16-02/v3.
- **Jigsaw block-entity `final_state` retained, not applied**: `Jigsaws` surfaces the
  connector's `final_state` (the block to leave once connected); LegacySinglePoolElement
  leaves air, which 16-02 may override per the joined element. Surfaced for 16-02.

Both are intentional and documented; neither blocks STRUCT-05 part 1's goal (geometry
placement). 16-02 consumes the jigsaw connectors; v3 owns entities.

## Self-Check: PASSED
