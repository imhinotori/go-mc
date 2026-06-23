# Phase 1: Foundation — Fork & Codegen - Research

**Researched:** 2026-06-23
**Domain:** Retargeting the Tnze/go-mc #294–296 draft codegen pipeline from MC 1.21.11 / proto 774 → MC 26.2 / proto 776, producing committed Go source data and a Go-only runtime build.
**Confidence:** HIGH — every load-bearing claim was verified by directly inspecting the PR #294/#295/#296 source on GitHub and by querying Mojang's live `version_manifest_v2.json` and `26.2.json`.

## Summary

The stack is decided (Stack research, HIGH confidence): hard-fork `Tnze/go-mc`, pin a master commit, apply the three draft PRs (#294 tools pipeline, #295 framework adaptations, #296 generated data), retarget the extractor to MC 26.2, and commit the generated Go source so the runtime builds with Go alone. This phase research answered the real question — *what do we not know about retargeting 774 → 776* — by reading the actual PR internals rather than reasoning from the summaries. The headline finding flips the prior risk assessment: **the #294 pipeline was deliberately built with 26.x support already in it.** `tools/main.go` contains `isNativelyUnobfuscated(version)` returning true for `major >= 26`, `tools/download.go` resolves the jar from the standard Mojang `version_manifest_v2.json` for 26.x (only pre-26.x needs the hardcoded `unobfuscated_versions.json` entry), and the docs state this explicitly: *"from the Mojang version manifest for 26.x+"*. The 26.x branch in the code is currently untested by the authors (they only ran 1.21.11), but it exists by design. [VERIFIED: PR #294 source]

Two facts are decisive and verified against Mojang's live API. First, **26.2 IS the current `latest.release` in `version_manifest_v2.json`** and its server jar is fetchable (sha1 `823e2250d24b3ddac457a60c92a6a941943fcd6a`, 60.9 MB). Second, **Mojang's own `javaVersion` for 26.2 is `majorVersion: 25`** (component `java-runtime-epsilon`), versus `21` for 1.21.11. The 26.2 jar is *built for and bundles* JDK 25 bytecode, so running the extractors under the PR's `eclipse-temurin:21-jdk` container would fail with `UnsupportedClassVersionError`. Bumping the container to a JDK-25 image is therefore not a nice-to-have — it is mandatory and exactly what the Stack research already prescribed. This is also good news: 26.1+ ships unobfuscated by default, so the standard manifest jar carries real `net.minecraft.*` class names and the reflection-based extractors (`GenComponentSchema` et al.) can read them directly with no mapping file. [VERIFIED: Mojang version_manifest_v2.json + 26.2.json; CITED: minecraft.net obfuscation-removal article]

The actual 774→776 work is mechanical, not investigative: regenerate with `--version 26.2`, then diff the generated Go against the committed 774 output. The 776 content delta is small and known (Sulfur Cube feature set: new blocks/items, one new entity with 12 archetypes, a `minecraft:sulfur_cube_content` data component, a `minecraft:sulfur_cube_archetype` registry, five new entity attributes, a new game event, a new damage type, and restructured entity predicates) — but the jar is ground truth and the diff *enumerates* it; we never hand-transcribe. The one genuine residual risk is extractor-compile fragility: `GenComponentSchema.java` imports ~15 specific `net.minecraft.*` classes by exact path (e.g. `net.minecraft.resources.Identifier`, `net.minecraft.network.codec.StreamCodec`, `net.minecraft.world.item.EitherHolder`). If 26.2 renamed or moved any of these vs 1.21.11, the extractor won't compile until the import is fixed. That is the only "unknown unknown" left, and it surfaces as a clean compile error, not a silent data corruption. [VERIFIED: PR #294 GenComponentSchema.java source; CITED: minecraft.wiki Java_Edition_26.2]

**Primary recommendation:** Fork go-mc at master commit `539b4a3a7f030332eb58b8a946116ae7907630d2`, apply #294→#295→#296 in order (all three report `MERGEABLE/CLEAN` against current master today), change exactly one line — `jdkImage` in `tools/extract.go` from `eclipse-temurin:21-jdk` to a pinned `eclipse-temurin:25-jdk` digest — then run `cd tools && go run . --version 26.2 --extract`. No `unobfuscated_versions.json` entry is needed for 26.2. Diff the generated output against the 774 baseline to satisfy GEN-04, fix any extractor import that 26.2 renamed, and commit the generated Go.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Fetch 26.2 server jar | Codegen host (Go, `tools/download.go`) | Mojang CDN | Phase-1 host needs internet; resolves jar URL from `version_manifest_v2.json` for 26.x. |
| Unobfuscated class extraction / reflection | Build-time JDK-25 container (`tools/java/*`) | — | Reflects over `net.minecraft.*` in the jar; JDK + jar are build-time-only, never runtime. |
| Data-generator reports (`--all`) | Build-time JDK-25 container | MC `net.minecraft.data.Main` | Produces `packets.json`/`registries.json`/`blocks.json`/`items.json` — the canonical source data. |
| Go source generation | Codegen host (Go, `tools/gen_*.go`) | — | Reads extracted JSON, emits `.go`/`.nbt` into the runtime module's standard paths. |
| Runtime protocol/codec/NBT/chunk | Runtime module (Go, forked go-mc) | — | Pure Go; consumes the committed generated data; zero JVM dependency (GEN-01). |
| Module isolation | `tools/go.mod` (separate module) | runtime `go.mod` | `replace` directive keeps codegen deps out of the runtime dependency graph (Java never leaks). |

## Standard Stack

> Already decided in `.planning/research/STACK.md`. Restated concisely with Phase-1-specific pins verified this session.

### Core
| Component | Version / Pin | Purpose | Why Standard |
|-----------|---------------|---------|--------------|
| Forked `Tnze/go-mc` | fork of master commit **`539b4a3a7f030332eb58b8a946116ae7907630d2`** (2024-12-24, "Add 1.21.1 chat support") + #294/#295/#296 | Protocol codec, NBT, chunk/region/save, connection framework, the `tools/` codegen module | The PRs branch off this master; pinning the commit (not a tag) avoids the go-mc tag-lag trap. [VERIFIED: GitHub API repos/Tnze/go-mc/commits/master] |
| go-mc `tools/` submodule | from #294 (separate `go.mod`, `replace` → parent) | 3-phase codegen: download jar → container-extract → emit Go | The only sanctioned way to produce 776 data. Already has 26.x support wired in. [VERIFIED: PR #294] |
| JDK (Temurin/Zulu) | **25** (build-time only) | Runs `--all` data generator + reflection extractors over the 26.2 jar | Mojang declares 26.2 needs `majorVersion: 25`; the jar is JDK-25 bytecode. [VERIFIED: 26.2.json `javaVersion`] |
| Docker / Podman | any current (pipeline auto-detects, podman preferred) | Hermetic extractor container | `tools/extract.go` `detectRuntime()` checks podman then docker. [VERIFIED: PR #294 extract.go] |
| Container image | **`eclipse-temurin:25-jdk`** (pin a digest) — replaces `21-jdk` | Hosts the 6 reflection extractors + `--all` generator | One-line change to `jdkImage` const in `tools/extract.go`. Mandatory for 26.2. [VERIFIED: extract.go line `const jdkImage`] |
| Go | 1.26.1 (runtime + codegen host) | Both modules; far exceeds go-mc's `go 1.22` floor | [VERIFIED: master go.mod `go 1.22`] |

### Generated data (#296 output — what GEN-03 commits)
| Package | 1.21.11 count (baseline) | 26.2 expected delta | Generator |
|---------|--------------------------|---------------------|-----------|
| `data/packetid` | 264 packets (proto 774) | proto 776 IDs; 775 already shifted serverbound 50→69, clientbound 135→141 | `packetid` |
| `data/registryid` (95 files) | 95 registries | +`sulfur_cube_archetype`; verify others | `registryid` |
| `level/block` blocks + states | 1,166 types / 29,671 states | +Cinnabar/Sulfur block families | `blocks` |
| `data/item` | 1,505 items | +sulfur items, Music Disc "Bounce" | `item` |
| `data/entity` | 157 entities | +Sulfur Cube | `entity` |
| `level/component` | 104 components (100 `*_gen.go`) | +`sulfur_cube_content` | `component` |
| `data/soundid` | 1,838 sounds | verify | `soundid` |
| `level/biome` | 65 biomes | verify | `biome` |
| `data/lang` | 147 languages | verify | `lang` |

*(Counts are the 1.21.11 baseline from #296; 26.2 numbers come out of the regen and the diff enumerates the change — do not assert them ahead of the jar.)* [VERIFIED: PR #296 body; CITED: minecraft.wiki Java_Edition_26.2 for delta drivers]

**Invocation:**
```bash
cd tools && go run . --version 26.2 --extract
```

## Architecture Patterns

### The 3-Phase Codegen Flow (verified from PR #294 source)

```
                    cd tools && go run . --version 26.2 --extract
                                      │
        ┌─────────────────────────────┼─────────────────────────────┐
        ▼                             ▼                             ▼
  PHASE 1 (Go host)            PHASE 2 (JDK-25 container)     PHASE 3 (Go host)
  download.go                  tools/java/ExtractAll.java     tools/gen_*.go (×10)
  ───────────                  ─────────────────────────      ──────────────────
  resolveServerJarURL():       1. extractInnerJar()           reads temp/jsons/26.2/*.json
   major>=26 → Mojang             (META-INF/versions/*.jar     ┌──────────────────────────┐
   version_manifest_v2.json       from bundler format)         │ packetid  → data/packetid│
   → 26.2.json                 2. runDataGenerator():          │ soundid   → data/soundid │
   → server.jar (60.9 MB)         java -DbundlerMainClass=     │ item      → data/item    │
  download server.jar            net.minecraft.data.Main       │ blocks    → level/block  │
  download ~147 lang files       -jar server.jar --all         │ entity    → data/entity  │
        │                       → generated/reports/*.json     │ component → level/comp.  │
        │                      3. copyReports():               │ blockent. → level/block  │
        │                         packets/registries/blocks/   │ registryid→ data/regid×95│
        │  (internet)             items/commands/datapack.json │ biome     → level/biome  │
        │                      4. runCustomExtractors():        │ lang      → data/lang×147│
        ▼                         javac+java GenEntities,       └──────────────────────────┘
   temp/cache/26.2-server.jar     GenComponents, GenBlockEnt.,         │
   temp/jsons/26.2/lang/          GenBlockProperties, GenBiomes,       ▼
                                  GenComponentSchema           generated .go committed to runtime module
                              → temp/jsons/26.2/*.json         (GEN-03) → builds with Go alone (GEN-01)
                                  (no internet)
```

Data entering: a version string (`26.2`) and the Mojang CDN. Decision point: `isNativelyUnobfuscated()` (major≥26) chooses manifest vs hardcoded URL. The container is the *only* place Java/JDK runs; its output is JSON, which the Go host turns into committed Go. Java never crosses into the runtime module. [VERIFIED: PR #294 main.go/download.go/extract.go/ExtractAll.java]

### Pattern 1: Fork + cherry-pick layout (module separation)
**What:** Two Go modules in one repo. Runtime `go.mod` (`module github.com/<you>/go-mc`) holds zero codegen deps. `tools/go.mod` is a separate module with a `replace` directive pointing at the parent, so generator deps (and the Java toolchain it shells out to) never enter the runtime dependency graph.
**When to use:** Always — this is how GEN-01 ("builds with Go alone, no JVM at runtime") is structurally guaranteed rather than merely intended.
**Source:** [VERIFIED: PR #294 — `tools/go.mod` with replace; docs/dev/tools.md "Separate tools/go.mod to isolate generator dependencies"]

### Pattern 2: Apply the three PRs in dependency order
**What:** #294 (infra: `tools/` module, removes old generators) → #295 (hand-written framework adaptations: ProtocolVersion 774, BitStorage no length-prefix, teleport 769+ layout, component slot format, per-registry RegistryData) → #296 (generated data output). #296 depends on #294's pipeline and #295's framework.
**When to use:** This is the only correct order. Cherry-pick or merge each branch's head commit.
**Pins (verified this session):**
- #294 head: `23fbe76efa3ba882b3d549d41033ecf66d1547fe` (branch `mj-121-v2a`)
- #295: branch `mj-121-v2b`
- #296: branch `mj-121-v2c`
- All three report **`MERGEABLE` / `mergeStateStatus: CLEAN`** against current master (master is frozen at Dec 2024, so no drift). [VERIFIED: `gh pr view --json mergeable,mergeStateStatus`]

### Pattern 3: "Regenerate and diff", never "guess the layout"
**What:** GEN-04 is satisfied mechanically. Generate the full 774 baseline first (`--version 1.21.11`, needs the hardcoded `unobfuscated_versions.json` entry already present in #294), commit it, then `--version 26.2`, and `git diff` the generated trees. The diff IS the 774→776 enumeration. Every byte traces to the jar.
**When to use:** For all of GEN-04. The wiki documents only ≤775 and is a hypothesis source at best.

### Anti-Patterns to Avoid
- **Hand-editing generated `.go`/`.nbt` files:** they are machine output; #296 says "do not edit manually." Fix the generator or the hand-crafted schema, regenerate.
- **Adding a `26.2` entry to `unobfuscated_versions.json`:** unnecessary and wrong — `isNativelyUnobfuscated()` routes 26.x to the manifest. An entry there is only for pre-26.x.
- **Leaving the container at `21-jdk`:** guarantees `UnsupportedClassVersionError` on the 26.2 jar.
- **Pulling go-mc as a live `@master` dependency:** master = MC 1.21.1, no 776, codegen unmerged. Fork and pin.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| 776 packet IDs / state tables | Hand-transcribed constant tables | `--all` `packets.json` → `gen_packetid` | 264+ packets; 775 already reshuffled IDs; transcription drifts every minor version. [VERIFIED] |
| 95+ registries + network indices | Curated registry list | `registries.json` → `gen_registryid` (95 files) | Order = network index; one wrong index = silent client disconnect at Finish Configuration. |
| 29,671+ block states | Manual block/state enumeration | `blocks.json`+`GenBlockProperties` → `gen_blocks` | Combinatorial; impossible by hand without error. |
| 104+ data-component wire schemas | Hand-written codecs | `GenComponentSchema.java` reflection + hand-crafted overrides → `gen_component` | Reflection probes `StreamCodec` for VarInt-vs-Int per field; 48 needed manual overrides even with reflection. |
| Jar acquisition + bundler unpack | Custom downloader/unzipper | `download.go` + `ExtractAll.extractInnerJar()` | Already handles `version_manifest_v2.json`, `META-INF/versions/*.jar`, and `META-INF/libraries/*.jar` classpath assembly. |
| 26.x unobfuscation | Mapping-file remap step | nothing — 26.1+ ships unobfuscated | Standard manifest jar already has real `net.minecraft.*` names. [CITED: minecraft.net] |

**Key insight:** The entire value of #294 is that it converts "transcribe thousands of version-specific layouts by hand" into "run one command and diff." The codegen output is ground truth; the hand-crafted `tools/hand-crafted/*.json` (component schema overrides, packet phase ordering, naming overrides) is the *only* place human judgment encodes wire details reflection can't see — budget review effort there, nowhere else.

## Common Pitfalls

### Pitfall 1: Container still pinned to JDK 21 → `UnsupportedClassVersionError`
**What goes wrong:** `cd tools && go run . --version 26.2` runs the `--all` generator and extractors under `eclipse-temurin:21-jdk`; the JDK-25 bytecode in the 26.2 jar fails to load.
**Why it happens:** `tools/extract.go` hardcodes `const jdkImage = "docker.io/library/eclipse-temurin:21-jdk"`.
**How to avoid:** Change it to a pinned `eclipse-temurin:25-jdk` digest. This is the single mandatory code change to retarget. Mojang declares 26.2 needs `majorVersion: 25`. [VERIFIED: extract.go + 26.2.json]
**Warning signs:** `class file version 69.0` errors; the `--all` generator or `javac` step exiting non-zero immediately.

### Pitfall 2: Extractor imports renamed/moved in 26.2 → compile failure
**What goes wrong:** `GenComponentSchema.java` (and the other 5 extractors) `javac`-compile against the 26.2 jar's classes by exact path. If 26.2 renamed/moved any imported class, compilation fails and extraction is skipped (the orchestrator logs a WARNING and continues with missing JSON).
**Why it happens:** The extractors hardcode ~15 imports like `net.minecraft.resources.Identifier`, `net.minecraft.core.component.DataComponents`, `net.minecraft.network.codec.StreamCodec`, `net.minecraft.world.item.EitherHolder`, `net.minecraft.server.Bootstrap`. The `ResourceLocation → Identifier` rename at 1.21.11 is precedent that Mojang does move these. [VERIFIED: GenComponentSchema.java imports]
**How to avoid:** Run the extract; if `javac` fails, read the error, find the renamed class in the (unobfuscated, readable) 26.2 jar, fix the import, rerun. This is a clean compile error, not silent corruption — surfaces loudly.
**Warning signs:** `ExtractAll` logs `WARNING: extractor compilation failed`; downstream Go generators error with "file not found" for `entities.json`/`components.json`/`component_schema.json`.

### Pitfall 3: Wrong protocol assertion (774 leaking into 776 build)
**What goes wrong:** #295 hardcodes `ProtocolVersion = 774` (bot) and the version string `"1.21.11"`. If you apply #295 and don't bump these, the handshake asserts the wrong protocol.
**Why it happens:** #295 is hand-written framework code, not generated; `--version 26.2` does NOT touch it. The protocol number and version string are manual edits.
**How to avoid:** After applying #295, grep for `774` and `"1.21.11"` in `bot/`, `server/`, and set them to `776` / `"26.2"`. (Phase 2 owns the handshake assertion, but the constant lives in the framework code applied here.) [VERIFIED: PR #295 body — "Bot ProtocolVersion: 767 → 774", version string "1.21.11"]
**Warning signs:** Client rejected with version-mismatch; codegen ran at 26.2 but `ProtocolVersion` still reads 774.

### Pitfall 4: 26.x manifest path untested by PR authors
**What goes wrong:** The `major>=26` branch in `download.go`/`main.go` exists but the authors only ran 1.21.11; an edge case (e.g. the manifest `server` download shape, asset-index format change at index id 32) could surface.
**Why it happens:** `isNativelyUnobfuscated` and the manifest-resolution code are written but unexercised for real on 26.x.
**How to avoid:** Run `--version 26.2 --dry-run` first to confirm URL resolution, then the full extract. The manifest flow was verified fetchable this session (26.2 is `latest.release`, server jar resolves), so the risk is low. [VERIFIED: live manifest + 26.2.json]
**Warning signs:** "version 26.2 not found in Mojang manifest" (won't happen — confirmed present); asset-index parse error on lang download.

### Pitfall 5: Applying PRs to a drifted/wrong base → merge conflicts
**What goes wrong:** Cherry-picking #294-296 onto the wrong commit produces conflicts the drafts didn't anticipate.
**Why it happens:** Drafts are conflict-free only against the master they branch from.
**How to avoid:** Fork and branch off exactly `539b4a3a7f030332eb58b8a946116ae7907630d2`. All three PRs verified `MERGEABLE/CLEAN` against that master today; master is frozen since Dec 2024 so no drift. Apply #294→#295→#296 in order. [VERIFIED: gh pr mergeable status]
**Warning signs:** Git reports conflicts in `level/`, `bot/`, `server/` — means you based off the wrong commit.

### Pitfall 6: Java leaking into the runtime module
**What goes wrong:** A codegen dependency or the `tools/` module gets pulled into the runtime `go.mod`, making the server require Java/Docker to build.
**Why it happens:** Forgetting the two-module separation; importing `tools/` from runtime code.
**How to avoid:** Keep `tools/go.mod` separate with its `replace` directive (as #294 ships it). Verify the runtime builds with `go build ./...` on a machine with no Java/Docker after committing generated data. [VERIFIED: PR #294 two-module design]
**Warning signs:** `go build` of the server pulls codegen packages; CI needs Java to build the *server* (it should only need Java to *regenerate*).

## Code Examples

### Resolve + verify the 26.2 jar (already automated in download.go; manual confirm)
```bash
# Mojang manifest → 26.2 detail → server jar (VERIFIED live this session)
curl -s https://launchermeta.mojang.com/mc/game/version_manifest_v2.json \
  | jq '.latest, (.versions[] | select(.id=="26.2"))'
# → latest.release == "26.2"
# → 26.2 url: https://piston-meta.mojang.com/v1/packages/4c3cd3500ce8b9ea104c358a784634fedb2a610f/26.2.json

curl -s https://piston-meta.mojang.com/v1/packages/4c3cd3500ce8b9ea104c358a784634fedb2a610f/26.2.json \
  | jq '{javaVersion, server: .downloads.server}'
# → javaVersion.majorVersion == 25   (⇒ container must be JDK 25)
# → server.sha1 == "823e2250d24b3ddac457a60c92a6a941943fcd6a"  (60.9 MB)
```

### Fork + apply the three PRs (pinned base, dependency order)
```bash
# 1. Fork Tnze/go-mc to github.com/<you>/go-mc, then:
git clone https://github.com/<you>/go-mc && cd go-mc
git remote add upstream https://github.com/Tnze/go-mc
git fetch upstream

# 2. Branch off the pinned master commit the PRs target
git checkout -b ender-776 539b4a3a7f030332eb58b8a946116ae7907630d2

# 3. Fetch and merge the three draft PR branches IN ORDER (all MERGEABLE/CLEAN)
git fetch upstream pull/294/head:pr294 && git merge --no-ff pr294   # tools pipeline
git fetch upstream pull/295/head:pr295 && git merge --no-ff pr295   # framework adaptations
git fetch upstream pull/296/head:pr296 && git merge --no-ff pr296   # generated 774 data (baseline)
```

### The one mandatory retarget edit (tools/extract.go)
```go
// BEFORE (PR #294):
const jdkImage = "docker.io/library/eclipse-temurin:21-jdk"
// AFTER (26.2 requires JDK 25 — pin a digest for reproducibility):
const jdkImage = "docker.io/library/eclipse-temurin:25-jdk@sha256:<pin-digest>"
```

### Generate 776 data + enumerate the 774→776 diff (GEN-02, GEN-03, GEN-04)
```bash
# Baseline 774 already committed by #296. Now regenerate at 26.2:
cd tools
go run . --version 26.2 --dry-run     # confirm manifest URL resolution first
go run . --version 26.2 --extract     # 3-phase: download → JDK25 container extract → gen Go

# Enumerate what 776 changed (GEN-04) — the diff IS the enumeration:
cd .. && git add -A && git diff --staged --stat   # per-file change magnitude
git diff --staged data/packetid/ data/registryid/ level/component/  # the load-bearing deltas

# Prove GEN-01 (Go-only runtime) on a Java/Docker-free path:
go build ./...   # runtime module must build with Go alone
```

### How `--version 26.2` routes (no unobfuscated entry needed — verified in source)
```go
// tools/main.go — pre-26.x needs a hardcoded jar entry; 26.x does not:
if !isNativelyUnobfuscated(version) {        // major >= 26 → returns true → SKIP this
    checkUnobfuscatedEntry(goMCRoot, version)
}
// tools/download.go resolveServerJarURL(): unobfuscated_versions.json miss for "26.2"
//   → falls through to fetchVersionDetail() → version_manifest_v2.json → server jar URL.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Obfuscated server jar + mapping file remap | Jar ships unobfuscated; extract `net.minecraft.*` directly | MC 26.1 (Dec 2025) | No deobfuscation/mapping step; reflection extractors work as-is. [CITED: minecraft.net] |
| `unobfuscated_versions.json` hardcoded jar URLs | Standard `version_manifest_v2.json` for 26.x+ | Built into #294 via `isNativelyUnobfuscated()` | `--version 26.2` needs no manual entry. [VERIFIED] |
| Scattered per-data-type generators | Unified `tools/` module, 10 generators + 6 extractors, one command | #294 (2026-02) | `cd tools && go run . --version X` retargets in one command. [VERIFIED] |
| JDK 21 build target | JDK 25 (`java-runtime-epsilon`) | MC 26.2 | Container must be `25-jdk`. [VERIFIED: 26.2.json] |
| `ResourceLocation` | `Identifier`; `BlockEntity` → `BlockEntityType` | 1.21.11 | Precedent for class renames; check 26.2 for more. [VERIFIED: PR commit msg] |

**Deprecated/outdated:**
- minecraft.wiki protocol page documents only ≤775; it is a *hypothesis* source for 776, never authoritative. The jar is ground truth.
- go-mc master (MC 1.21.1) and its tags lag MC by a version; never use as a live dependency.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | 26.2's extractor imports compile unchanged vs 1.21.11 (no further `net.minecraft.*` renames beyond what's known) | Pitfall 2 | Extractor `javac` fails; fix is mechanical (rename import) and surfaces as a loud error, not silent corruption. Cannot fully verify without running against the jar. |
| A2 | The `major>=26` manifest-resolution branch in download.go works end-to-end for 26.2 (URL resolution verified; full asset-index/lang download for assetIndex id 32 not executed) | Pitfall 4 | Lang download or jar fetch edge case; low risk — manifest + jar URL confirmed fetchable live. |
| A3 | The `--all` data generator in the 26.2 jar still emits `packets.json` in the `{phase}.{direction}.{name}.protocol_id` shape `gen_packetid` expects | Standard Stack | If the report schema changed, `gen_packetid` needs adjustment. `packets.json` has existed since 1.18 and is stable; CITED, not run against 26.2. |
| A4 | 26.2 server jar uses the same bundler format (`META-INF/versions/*.jar`, `META-INF/libraries/*.jar`) `extractInnerJar`/`buildExtractorClasspath` expect | Architecture | Inner-jar/classpath extraction fails; bundler format has been stable since 1.18; the code also has a "not bundled, use directly" fallback. |

*These four are the residual unknowns that only running the pipeline against the real 26.2 jar can close. All surface as loud failures (compile/parse errors), none as silent data corruption — consistent with "the jar is ground truth, diff enumerates the change."*

## Open Questions

1. **Did 26.2 rename any class the extractors import?**
   - What we know: 26.1+ is unobfuscated; the extractors import ~15 exact `net.minecraft.*` paths; 1.21.11 renamed `ResourceLocation→Identifier`.
   - What's unclear: whether 26.2 moved any of those 15 vs 1.21.11. The 26.2 changelog (Sulfur Cube + entity-predicate restructure + 5 new attributes) suggests data additions more than core-class renames, but predicate restructuring could touch component classes.
   - Recommendation: run the extract early in the phase; treat any `javac` WARNING as the first task. Budget it as real work, not config.

2. **Exact 776 counts (packets, registries, states, components).**
   - What we know: the 774 baseline counts; the qualitative 776 delta (Sulfur Cube feature set).
   - What's unclear: precise numbers — by design. The regen produces them.
   - Recommendation: do not assert counts in the plan; the GEN-04 diff reports them.

3. **`go run . --version 26.2` vs `--extract` first-run caching.**
   - What we know: `main.go` only extracts if `temp/jsons/26.2/` is absent or `--extract` given.
   - Recommendation: use `--extract` on the first 26.2 run to force the container even if a stale cache exists.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| JDK | `--all` generator + extractors (build-time only) | ✓ (per STACK.md: confirmed Zulu/Temurin 25) | 25 | none — 26.2 mandates JDK 25 |
| Docker or Podman | hermetic extractor container | ⚠ verify on build host | any current | run extractors on host JDK 25 via `MC_*_DIR` env vars (ExtractAll supports non-container paths) |
| Go | both modules | ✓ | 1.26.1 | none |
| `git` + `gh` | fork, fetch PR branches | ✓ | — | manual patch download |
| Internet (Phase 1 only) | jar + lang download | ✓ at codegen time | — | pre-seed `temp/cache/26.2-server.jar` manually |

**Missing dependencies with no fallback:** JDK 25 is mandatory for the jar (lower JDK = `UnsupportedClassVersionError`).
**Missing dependencies with fallback:** Docker/Podman — `ExtractAll.java` honors `MC_CACHE_DIR`/`MC_JSONS_DIR`/`MC_JAVA_DIR` env vars and can run on the host JDK 25 directly if no container runtime is present (the `runExtract` host path assumes a runtime, but `ExtractAll` itself is container-agnostic; running it host-side is the documented escape hatch). [VERIFIED: ExtractAll.java env-var handling]

## Validation Architecture

> `.planning/config.json` not present in repo; treating `nyquist_validation` as enabled (default).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` (`go test`) |
| Config file | none (idiomatic Go) |
| Quick run command | `go build ./... && go vet ./...` (generated code must compile + vet clean) |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GEN-01 | Runtime builds with Go alone, no JVM | smoke | `go build ./...` (on a Java/Docker-free path) | ✅ (go-mc has tests) |
| GEN-02 | `cd tools && go run . --version 26.2` produces 26.2 JSON | integration | `cd tools && go run . --version 26.2 --extract` (requires JDK 25 + Docker) | ❌ Wave 0 — manual/CI gated on container |
| GEN-03 | Generated 776 data is valid committed Go | unit | `go build ./data/... ./level/...` after regen | ✅ |
| GEN-04 | 774→776 diff enumerated | manual+diff | `git diff --staged --stat` between 774 and 26.2 trees | ❌ Wave 0 — diff artifact, human-reviewed |

### Sampling Rate
- **Per task commit:** `go build ./... && go vet ./...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full `go test ./...` green + the 774→776 diff reviewed + a clean `go build` on a JVM-free machine.

### Wave 0 Gaps
- [ ] CI job (or documented manual step) to run `cd tools && go run . --version 26.2 --extract` — requires JDK 25 + Docker/Podman; not pure-Go so cannot run in a plain `go test`.
- [ ] A committed 774 baseline (generate via `--version 1.21.11`) to diff 776 against for GEN-04.
- [ ] A "JVM-free build" smoke check (e.g. `go build ./...` in a Go-only container) proving GEN-01.

*(Generated `.go` files are self-validating: every go-mc generator validates Go syntax before writing, and `go build`/`go test` over the generated packages is the regression net.)* [VERIFIED: PR #294 "All generators validate Go syntax before write"]

## Project Constraints

No `./CLAUDE.md` in the project working directory (`D:\ender`). Global user rules apply (commit authorship, Context7 doc lookups) but impose no Phase-1 technical constraints beyond those already in REQUIREMENTS/STATE/research.

Locked constraints carried from `.planning` (treat as authority):
- **GEN-01:** runtime builds with Go alone, no JVM. → two-module separation; Java is build-time-only.
- **Out of Scope — "JVM at runtime":** Java is build-time-only; never in runtime `go.mod` or runtime image.
- **Pitfall 10/2 (research):** pin a master *commit* not a tag; retarget codegen to 26.2 as real work.

## Sources

### Primary (HIGH confidence)
- **Tnze/go-mc PR #294** (`mj41`, `mj-121-v2a`, head `23fbe76e…`) — full source of `tools/main.go`, `download.go`, `extract.go`, `ExtractAll.java`, `GenComponentSchema.java`, `gen_packetid.go`, `docs/dev/tools.md`, `unobfuscated_versions.json`. Verified `isNativelyUnobfuscated(major>=26)`, manifest routing, `jdkImage = 21-jdk`, 3-phase flow, two-module isolation.
- **Tnze/go-mc PR #295** (`mj-121-v2b`) — framework adaptations: ProtocolVersion 774, version string "1.21.11", BitStorage length-prefix removal, teleport 769+ layout, component slot format, per-registry RegistryData.
- **Tnze/go-mc PR #296** (`mj-121-v2c`) — generated-data counts (264 packets, 95 registries, 29,671 states, 104 components, etc.), 20 new / 6 removed registry types, "do not edit manually."
- **Mojang `version_manifest_v2.json`** (live) — `latest.release == "26.2"`; 26.2 detail URL.
- **Mojang `26.2.json`** (live) — `javaVersion.majorVersion == 25`; server jar sha1 `823e2250…`, 60.9 MB; assetIndex id 32. Compared to `1.21.11.json` (`majorVersion 21`).
- **go-mc master** (`gh api commits/master`) — pin commit `539b4a3a…` (2024-12-24); `go.mod` `go 1.22`. PR mergeable status `MERGEABLE/CLEAN` ×3.

### Secondary (MEDIUM confidence)
- **minecraft.wiki Java_Edition_26.2** — 776 content delta (Sulfur Cube, Cinnabar/Sulfur blocks, `sulfur_cube_content` component, `sulfur_cube_archetype` registry ×12, 5 new entity attributes, `minecraft:bounce` game event, `sulfur_cube_hot` damage type, restructured entity predicates, data pack 107.1).
- **minecraft.net obfuscation-removal article / Java Code Geeks 2026** — 26.1+ ships unobfuscated; no mapping file needed for 26.2.
- **PrismarineJS minecraft-data #3888 / mineflayer** — 775 (26.1.2) added ~15 serverbound packets, shifted IDs (serverbound 50→69, clientbound 135→141).
- **OpenJDK JEP 330/458, Oracle JDK 25 release notes** — single-file source launcher `--source` semantics; class-file backward compatibility (JDK 25 reads JDK-21-target class files; not vice versa).

### Tertiary (LOW confidence — needs validation by running the pipeline)
- Exact 776 generated counts and any 26.2-specific extractor import renames — confirmable only by executing `--version 26.2` against the real jar (Assumptions A1–A4).

## Metadata

**Confidence breakdown:**
- Standard stack (fork, PRs, JDK 25, pins): **HIGH** — read PR source directly; verified pins, mergeability, and the JDK-25 requirement against Mojang's live API.
- Architecture (3-phase flow, module separation, 26.x routing): **HIGH** — traced through `main.go`/`download.go`/`extract.go`/`ExtractAll.java`.
- Pitfalls: **HIGH** on the mechanism (JDK pin, import fragility, protocol constant, module leak); **MEDIUM** on whether 26.2 specifically trips Pitfall 2 (only the jar can confirm).
- 774→776 content delta: **MEDIUM** — wiki-cited, jar-authoritative; the diff enumerates it.

**Research date:** 2026-06-23
**Valid until:** ~2026-07-07 (7 days — bleeding-edge: 26.2 is current `latest.release`; a 26.3 drop or upstream go-mc movement would shift pins. The PR mergeable status and master pin are the most drift-prone facts.)
