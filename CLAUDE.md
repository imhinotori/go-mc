<!-- GSD:project-start source:PROJECT.md -->
## Project

**Ender — Minecraft Java Server in Go**

A from-scratch Minecraft Java Edition server (version 26.2, protocol 776) written in Go, inspired architecturally by Leaf (Winds-Studio's high-performance Paper fork). It uses `Tnze/go-mc` as the protocol/data foundation and builds the full server stack — networking, world, chunks, entities, AI, physics, game loop — on top. Target audience: server operators and Go developers who want a high-performance, vanilla-faithful Minecraft server without the JVM.

**Core Value:** A Go server that a vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — with an architecture designed from day one for the concurrency-based optimizations Leaf pioneered (async pathfinding, async entity tracking, async mob spawning).

### Constraints

- **Tech stack**: Go 1.26.1 server; Java 25 only for offline jar data extraction (not a runtime dependency of the server).
- **Compatibility**: Must speak protocol 776 to an unmodified vanilla 26.2 client.
- **Dependencies**: Built on `Tnze/go-mc` (likely a fork to apply the #294-296 codegen approach and retarget 776).
- **Performance**: Architecture must be concurrency-ready from the start so Leaf-style async optimizations can be layered without rewrites.
- **Scope**: Server core only — no plugin/extension API.
<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->
## Technology Stack

## TL;DR Decisions
## Recommended Stack
### Core Technologies
| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| **Go** | 1.26.1 | Server language + codegen host language | Confirmed toolchain. Generics (1.18+), `min`/`max`/`clear` builtins, improved runtime, and mature `sync` primitives are all available. go-mc only requires 1.22; 1.26.1 is a strict superset. |
| **Fork of `Tnze/go-mc`** | fork of `master` @ ~Dec 2024 + #294–296 cherry-picks | Protocol codec, NBT, region/chunk/save, server connection framework, chat, RCON | Provides the ~30% foundation for free. But upstream master = MC 1.21; the codegen pipeline you need is in unmerged drafts. You vendor-fork and retarget to 776 yourself. **Do not `go get @master`** — pin your fork. |
| **go-mc `tools/` submodule** | from #294 (separate Go module) | Codegen pipeline: downloads jar, runs Java extractors, emits Go source | This is the *only* sanctioned way to produce authoritative packet/registry/block/item/entity/component data for 776. Hand-transcription is a non-starter (264 packets, 29671 block states, 104 components). |
| **Java (Zulu/Temurin)** | **25** (build-time only) | Runs the 7 Java extractors that reflect over the unobfuscated 26.2 server jar | Confirmed local toolchain (Zulu 25). The 26.2 jar ships unobfuscated, so extractors read real field/type names via reflection. **Never** a server runtime dependency. |
| **Docker** | 29 | Hermetic extractor container + reproducible builds | Pins the JDK + jar environment so codegen is reproducible across machines/CI. #294 uses a `temurin:21-jdk` container; retarget to a `25-jdk` image for 26.2. |
### Supporting Libraries (concurrency — the Leaf-parity layer)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| **`github.com/puzpuzpuz/xsync/v4`** | v4.x (latest) | CLHT-based concurrent `Map[K,V]`, `MPMCQueue` (Vyukov bounded MPMC), `Counter`, `SPSCQueue` | The Go substitute for Leaf's FastUtil collections + lock-free queues. Use for the entity table, chunk-holder map, async work queues feeding the pathfinding/tracker/spawner pools. Reads are obstruction-free; writes use lock-sharding — far better than `sync.Map` under write contention. **This is the single most important supporting dep.** |
| **`github.com/panjf2000/ants/v2`** | v2.12.0+ | Bounded, reusable goroutine pool with recycling | The execution substrate for Leaf's async optimizations: async pathfinding, async entity tracker, async mob spawning. Caps goroutine count (prevents the "spawn a goroutine per mob" blowup) and recycles workers. Use one tuned pool per async subsystem. |
| **`github.com/sourcegraph/conc`** | latest (v0.3.x) | Structured concurrency: scoped `WaitGroup`, `pool.NewWithResults[T]`, panic propagation | Tick-loop orchestration where you fan out per-region/per-chunk work *within a tick boundary* and must join before the next tick. Gives owner-scoped goroutines with proper panic handling — safer than raw `errgroup` for the game loop. |
| **`golang.org/x/sync`** | latest | `errgroup`, `singleflight`, `semaphore` | `singleflight` for dedup'ing concurrent chunk-generation requests (two players entering the same ungenerated chunk → one generation). `semaphore` for weighted IO throttling on region flush. Standard, stable, zero-risk. |
### Supporting Libraries (world / data — mostly already in the fork)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| **go-mc `nbt`** (in fork) | — | NBT read/write + SNBT, reflection-based struct mapping | Already provided. Use directly; do NOT pull a second NBT lib. Covers entity/player/level.dat persistence and the (legacy) NBT paths. |
| **go-mc `save` + `level` + region** (in fork) | — | Anvil `.mca` region IO, chunk (de)serialization, BitStorage, palettes | Already provided. This is the anvil/region IO answer. Extend it in-fork for the **linear region format** (a later Leaf-parity requirement), rather than adding a new dep. |
| **Custom Mojang-noise port** | n/a (you write it) | Vanilla-parity world generation noise (improved Perlin / OctaveSimplexNoise, NormalNoise, DensityFunctions) | For the *vanilla-parity* stretch goal. Minecraft's terrain is NOT OpenSimplex — it's a specific improved-Perlin/Simplex octave stack + density-function graph. Port from the unobfuscated `net.minecraft.world.level.levelgen` classes (now readable). No library substitutes for this. |
| **`github.com/ojrac/opensimplex-go`** *(MVP stub only)* | v1.0.2 (⚠ archived Jan 2025) | Deterministic noise for the non-vanilla MVP generator | ONLY for the "deterministic generator" MVP requirement, never for parity. It is archived/unmaintained and implements original OpenSimplex (not OpenSimplex2). Acceptable for a throwaway stub; vendor the file if you use it. Prefer porting OpenSimplex2 (`KdotJPG/OpenSimplex2`) if you want a maintained noise primitive. |
| **`github.com/zeebo/xxh3`** | latest | Fast non-crypto hashing for chunk keys / spatial hashing | When you need a fast 64-bit hash for `(x,z)`→chunk or `(x,y,z)`→section keys feeding the xsync maps. Optional; a hand-rolled `int64` morton/zigzag key is often enough and allocation-free. |
### Spatial data structures
| Approach | Recommendation | Rationale |
|----------|---------------|-----------|
| Chunk/section indexing | **Hand-rolled `int64`-keyed `xsync.Map`** (pack x,z or x,y,z into one int64) | Minecraft's world is grid-aligned; you do not need a quadtree/octree for chunk lookup — a packed-int key into a concurrent map is O(1) and allocation-free. This is what vanilla/Leaf effectively do. |
| Entity broad-phase (tracking, AABB queries) | Start with **per-section entity lists** (grid bucketing); add a tree only if profiling demands | Vanilla itself buckets entities per section/chunk. A generic quadtree (`s0rg/quadtree`, generic + zero-alloc, updated Apr 2025) is a fallback **only** if grid bucketing proves insufficient — most likely it won't. **Do not** reach for a 3rd-party octree first. |
| What to avoid | `paulmach/orb/quadtree` for live entity tracking | Documented as not safe for concurrent insertion (only concurrent reads). Wrong fit for a multi-goroutine tick model. |
### Development / Codegen Tools
| Tool | Purpose | Notes |
|------|---------|-------|
| go-mc `tools/` (separate `go.mod`) | Orchestrates the 3-phase pipeline: (1) Go host downloads jar+lang, (2) Java extractors run in container, (3) Go generators emit `.go` from extracted JSON | Keep it a **second module** (own `go.mod`) so codegen deps never pollute the server module. Run: `cd tools && go run . --version 26.2`. |
| `eclipse-temurin:25-jdk` (or Zulu 25) container | Hosts the 7 Java extractors that reflect over the 26.2 server jar | #294 used `temurin:21-jdk`; bump to 25 for 26.2. Pin the digest for reproducibility. |
| Hand-crafted JSON config (in `tools/`) | Supplies wire-format schemas / naming / protocol metadata reflection can't capture (e.g., packet field order, varint-prefix quirks) | This is where the PROJECT.md "known protocol shifts" (component slots, prefix-less BitStorage, restructured Teleport, PlayerChat globalIndex) get encoded. Budget real effort here — codegen gives you IDs/types, this gives you the *wire layout*. |
| `go generate` directives | Wire the generators into the build for repeatability | Optional but recommended so 776→26.3 retargets are one command. |
| `golangci-lint`, `go test -race` | Quality gates | `-race` is **non-negotiable** given the concurrency-first architecture; run it in CI on the tick/async subsystems. |
## Fork vs. Vendor — Decision: FORK
| Option | Verdict | Reasoning |
|--------|---------|-----------|
| Depend on `Tnze/go-mc@master` | ❌ Reject | Master = MC 1.21, last meaningful update Dec 2024. It cannot speak 776 and lacks the codegen pipeline. A `replace` won't help — you need to modify generated + framework code. |
| `go mod vendor` upstream as-is | ❌ Reject | Vendoring freezes a tree you must *modify* (retarget codegen to 776, apply #295 framework adaptations, encode 776 wire shifts). Vendoring is for unmodified deps. |
| **Hard fork** (own repo, cherry-pick #294/#295/#296, retarget to 26.2) | ✅ **Adopt** | You must (a) pull the unmerged draft pipeline, (b) re-run it against the 26.2 jar, (c) hand-edit framework code for the protocol-776 wire shifts. That is fork-level ownership. Pin your fork via module path or `replace`. |
## Alternatives Considered
| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| `puzpuzpuz/xsync/v4` (concurrent maps + MPMCQueue) | `sync.Map` / hand-rolled lock-free | `sync.Map` only if a map is read-mostly and low-churn (rare here). Hand-rolled lock-free only after profiling proves xsync is the bottleneck — almost never your first move. |
| `panjf2000/ants/v2` | `sourcegraph/conc/pool`, `alitto/pond`, raw `errgroup` | `conc/pool` when work is *scoped to a tick* (join before next tick). `ants` when you want a *long-lived, bounded, recycling* pool for steady async streams (pathfinding). Use both, for different jobs. `pond` is a fine alt to ants but ants has more battle-testing. |
| Hand-rolled int64-keyed xsync.Map for chunks | `s0rg/quadtree` (generic, zero-alloc, 2025) | Only if entity broad-phase grid-bucketing measurably underperforms. Not for chunk lookup. |
| Custom Mojang-noise port (parity) | `ojrac/opensimplex-go`, `KEINOS/go-noise`, `aldernero/go-noise` | Off-the-shelf noise is fine for the *non-parity* deterministic MVP generator. None of them reproduce vanilla terrain — that's why parity needs a port. |
| go-mc fork's `save`/region | Writing anvil IO from scratch | Never write anvil from scratch — the fork already has it. Extend it in-place for linear region format later. |
## What NOT to Use
| Avoid | Why | Use Instead |
|-------|-----|-------------|
| `go get github.com/Tnze/go-mc@master` as a live dep | Pinned at MC 1.21; no 776; codegen is in unmerged drafts. Upstream churn could also break you. | Your hard fork, pinned. |
| `ojrac/opensimplex-go` for vanilla-parity terrain | Archived Jan 2025; original OpenSimplex only; Minecraft uses improved-Perlin/OctaveSimplex, so it cannot match vanilla anyway. | Port Mojang's noise from the unobfuscated `levelgen` classes. Use the lib (or OpenSimplex2) only for a throwaway stub. |
| Java in the server runtime / `go.mod` / runtime Docker image | Java 25 is *only* for offline jar extraction. Leaking it into runtime defeats the entire "no JVM" value prop. | Keep Java strictly in the `tools/` codegen container. Server image = pure Go static binary. |
| A second NBT library | go-mc fork already has a reflection-based NBT/SNBT impl. | go-mc `nbt`. |
| `paulmach/orb/quadtree` for live entity tracking | Not safe for concurrent insertion (reads only). Wrong for the multi-goroutine tick model. | Per-section grid bucketing in concurrent maps. |
| Generic octree libraries as a first choice | Premature; vanilla buckets entities per section. Adds a dep + cache-unfriendly pointer-chasing. | Grid bucketing; profile before adding any tree. |
| Building Leaf optimizations before vanilla logic | Per PROJECT.md key decision — you cannot async-optimize logic that doesn't exist. | Correctness-first: vanilla tick/world/entity logic, *then* layer xsync/ants async on top. |
## Stack Patterns by Variant
- Concurrency stack can stay minimal: stdlib `sync` + `golang.org/x/sync` is enough for handshake→login→config→play.
- World gen = deterministic stub (flat or single-noise). Pull `ojrac/opensimplex-go` only if you want non-flat terrain; vendor the file.
- Do NOT introduce ants/xsync yet — no async subsystems exist to optimize.
- Introduce `xsync/v4` for the entity table, chunk-holder map, and async work queues.
- Introduce `ants/v2` pools — one per async subsystem (pathfinding, entity tracker, mob spawner).
- Use `conc` for tick-bounded fan-out/join; `singleflight` for chunk-gen dedup.
- Add the linear region format by extending the fork's region package.
- Turn on `go test -race` gates for every async subsystem.
- Re-run `cd tools && go run . --version 26.3` in the fork; bump extractor JDK if Mojang bumps Java.
- Re-encode any new wire shifts in the hand-crafted `tools/` JSON config.
- This is the payoff of the codegen approach: a retarget is hours, not weeks.
## Version Compatibility
| Package | Compatible With | Notes |
|---------|-----------------|-------|
| Go 1.26.1 | go-mc (needs ≥1.22), xsync/v4, ants/v2 (≥1.19), conc, x/sync | All deps require far older Go; 1.26.1 is safe across the board. |
| go-mc fork | Java/JDK 25 extractor container | Coupling is *build-time only* via the `tools/` module; runtime server has zero Java coupling. |
| go-mc `tools/` module | Separate `go.mod` | Keep codegen deps isolated from the server module to avoid bloating the runtime dependency graph. |
| xsync/v4 | — | v4 dropped non-generic types; `MapOf` is now an alias for `Map`. If you copy v3 example code, adjust names. Generics-only API — fine on Go 1.26. |
| ants/v2 | — | Pinned at v2.12.0 (Mar 2024) as latest; stable and battle-tested. v1 is legacy — use v2. |
| protocol 776 / MC 26.2 | NOT in upstream go-mc, NOT on minecraft.wiki main protocol page (documented up to 1.21.10/773; wiki notes "currently 775 in Minecraft 26.1") | 776 is bleeding-edge. You are ahead of both upstream and the community wiki — codegen from the jar is your authoritative source, not the wiki. Confirms the fork decision. |
## Confidence Levels
| Area | Confidence | Basis |
|------|------------|-------|
| Fork (not vendor) decision | **HIGH** | Verified upstream master = MC 1.21 (Dec 2024); #294/#295/#296 confirmed as *draft* PRs targeting 1.21.11/774. You're building 776. Fork is unavoidable. |
| Codegen pipeline mechanics | **HIGH** | Verified against #294 and #296 directly: 3-phase host/container/host flow, `cd tools && go run . --version`, temurin JDK container, the exact extraction counts match PROJECT.md. |
| Concurrency stack (xsync/ants/conc) | **HIGH** | xsync CLHT map + Vyukov MPMCQueue verified; ants v2.12.0 + Go-version floor verified; conc structured-concurrency model verified. Direct analogue to Leaf's FastUtil+lock-free-queue approach. |
| NBT / anvil / region IO | **HIGH** | Confirmed present in go-mc (`nbt`, `save`, `level`, region) per pkg.go.dev and PROJECT.md. Reuse, don't replace. |
| World-gen noise | **MEDIUM** | HIGH that off-the-shelf OpenSimplex ≠ vanilla and that `ojrac/opensimplex-go` is archived. MEDIUM on the parity port effort since it depends on which `levelgen` density-function subset you target — that's a phase-specific research item. |
| Spatial structures | **MEDIUM** | Grid bucketing is the clearly correct first choice (matches vanilla); MEDIUM only because the exact entity broad-phase may need profiling-driven revision. |
| Protocol 776 specifics | **MEDIUM** (intentionally deferred) | 776/26.2 not yet on the community wiki; authoritative source is the jar via codegen. Wire-shift details live in the `tools/` JSON config and are a Phase-specific deep-dive, not a stack decision. |
## Sources
- `Tnze/go-mc` repo, master README, branches, pkg.go.dev — verified master = MC 1.21, Go ≥1.22, sub-packages (net/nbt/save/level/server/chat/yggdrasil) — HIGH
- go-mc PR #294 (pipeline infrastructure, 1.21.11/774, `cd tools && go run .`, temurin:21-jdk container, 3-phase flow) — HIGH
- go-mc PR #296 (generated data: 264 packets, 29671 block states, 1505 items, 157 entities, 104 components, 95 registries; draft status; relation to #294/#295) — HIGH
- `puzpuzpuz/xsync` repo + pkg.go.dev (CLHT Map, Vyukov MPMCQueue, v3→v4 migration) — HIGH
- `panjf2000/ants` releases (v2.12.0, Go ≥1.19) — HIGH
- `sourcegraph/conc` + `conc/pool` docs (structured concurrency, NewWithResults) — HIGH
- `ojrac/opensimplex-go` repo (v1.0.2, archived Jan 2025, original OpenSimplex not OpenSimplex2) — HIGH
- minecraft.wiki Java Edition protocol page (documented to 1.21.10/773; notes "currently 775 in Minecraft 26.1" — confirms 776/26.2 is ahead of community docs) — MEDIUM
- `s0rg/quadtree` (generic zero-alloc, Apr 2025), `paulmach/orb/quadtree` (concurrent-insert-unsafe) — MEDIUM
- `.planning/PROJECT.md` Context section (established codegen facts, protocol shifts, Leaf architecture) — project ground truth
<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->
## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->
## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->
## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->
## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:
- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->



<!-- GSD:profile-start -->
## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
