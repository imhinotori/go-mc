# Milestones — Sulfur

| Version | Shipped | Summary | Phases | Plans | Reqs |
|---------|---------|---------|--------|-------|------|
| **v1.0** | 2026-06-24 | Playable vanilla-faithful 26.2 server: login → biome-varied noise world (caves/ravines/aquifers/ore-veins) → entities/mob-AI/inventory/combat/respawn/persistence → commands/chat → Leaf-style async, `-race` clean. | 9 | 46 | 45/45 v1 + PARITY-01 |

## v1.0 — Key accomplishments

1. **Bleeding-edge proto-776 from the jar** — forked go-mc, retargeted the #294-296 codegen pipeline to the unobfuscated 26.2 jar; every wire layout jar-derived + capture-diffed against a real vanilla server (no wiki, no memory).
2. **Ownership-isolated tick spine** — single-owner 20-TPS ordered pipeline with 50ms game-time anchor + CS2-style subtick layer; the `applyAsyncResults` / `tracker.Tick()` / pure `computePath` seams built as no-ops from day one so the async swaps stay additive.
3. **First playable → fully interactive** — a real client walks a following, streamed world; places/breaks blocks, component-slot inventory, gravity/AABB physics, health/death/respawn, Anvil persistence.
4. **Vanilla AI ported from the jar** — GoalSelector brain + request→snapshot→result A* navigation + NaturalSpawner, translated faithfully from the decompiled sources per the port-from-jar mandate.
5. **Leaf async, additive not a rewrite** — async pathfinding/tracker/spawn swapped behind the pre-built seams; snapshot discipline kept collections plain; linear region format (pure-Go zstd); `-race` clean -count=10 under combined load.
6. **Full vanilla-parity worldgen (PARITY-01)** — Xoroshiro seeding + the data-driven density-function graph + NoiseChunk per-marker interpolation + Aquifer + OreVeinifier + WorldCarvers + SurfaceRules + multi-noise Climate biomes, all ported bit-exact from the jar; 2 post-gate fidelity fixes (per-marker interpolation, surface-before-carve) brought terrain to true 1:1. Visual gate approved.

Archives: [roadmap](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md) · [audit](v1.0-MILESTONE-AUDIT.md)
