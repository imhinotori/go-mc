# Deferred Items — Phase 17 Gameplay Completion

## Out-of-scope discoveries (logged, not fixed)

### TestTickAIDrivesMobs is flaky (pre-existing, unrelated to 17-14)

- **Found during:** Plan 17-14 (BLOCK-DROP + ITEM-PICKUP) full-suite test run.
- **File:** `server/spawner_test.go` (mob AI / spawner — explicitly out of scope for 17-14,
  which is forbidden from touching combat/fall_damage/fluid and was scoped to drops/pickup).
- **Symptom:** `TestTickAIDrivesMobs` asserts a wandering Pig's x advances by > 2.0 over 400
  ticks. The mob's navigation/stroll goals draw from the package-level `math/rand/v2`, so the
  walk is RNG-dependent and occasionally fails to clear the +2.0 threshold.
- **Proof it is pre-existing:** `git stash`-ing all 17-14 changes and running the test 8× on
  the clean `ender-776` tree fails 8/8. The failure predates and is independent of 17-14.
- **Why not fixed here:** Scope boundary — 17-14 only auto-fixes issues its own changes cause.
  This is an AI-subsystem test-determinism bug (the test should seed a deterministic RNG or
  assert progress over a longer horizon), owned by the AI/spawner plan, not the drop/pickup work.
- **Suggested fix (for the owning plan):** Inject a seeded `rand.Source` into `mobAI` navigation
  so the test is deterministic, or relax the assertion to "x strictly increased" over more ticks.
- **Re-confirmed during 17-15** (water-disconnect + pickup-slot fixes): the same test still flakes
  non-deterministically (fails ~1 in 3 full-suite runs, passes 5/5 in isolation and passes on the
  stashed tree by luck) — caused by the async pathfinding pool not rejoining within 400 ticks under
  CPU contention. Independent of the 17-15 fluid/inventory changes (those files are unrelated to the
  AI/async path); all 17-15 tests pass 5/5 in isolation. Still owned by the AI/spawner plan.

### Mobs walk ON water — mob AI not 1:1 with the jar (gate finding, 17-15/16 era)

- **Found during:** Phase 17 visual gate (real client).
- **Symptom:** mobs (the Pig) walk across the water surface instead of swimming/sinking.
- **Root cause (suspected):** the mob navigation/pathfinding does not consult fluid state —
  vanilla `GroundPathNavigation`/`WalkNodeEvaluator` treats water as a penalized/blocked node
  (`PathType.WATER`/`getPathTypeOfMob`), and a mob in water uses `travelInFluid` (the SAME 0.8
  slowdown + buoyancy that, for SERVER-controlled entities, IS applied server-side — unlike the
  client-authoritative player, see 17-15). Sulfur's mob movement/nav currently ignores fluids,
  so mobs path straight over water as if it were solid.
- **Why not fixed here:** owned by the AI/mob subsystem (the parallel mob-AI port the user is
  running in another session). The player-side fluid physics (17-13) is wired correctly; the
  MOB-side `travelInFluid` + water-aware pathfinding is the AI plan's responsibility.
- **1:1 mandate note:** under the new absolute mandate (gameplay = literal jar copy), the mob
  nav must port `WalkNodeEvaluator.getPathType` (water nodes) + `Mob.travel`→`travelInFluid`
  server-side. Decompile `net.minecraft.world.level.pathfinder.WalkNodeEvaluator` +
  `net.minecraft.world.entity.Mob.travel`.

### Block survival — a broken support does NOT destroy the unsupported block above (gate finding, 17-05)

- **STATUS: CLOSED for VEGETATION (the visible 80%) — torches/rails/redstone/doors still DEFERRED.**
  Resolved by the BLOCK-SURVIVAL vegetation pass (this session): `block.IsVegetation` /
  `block.IsDoublePlant` predicates (level/block/utilfuncs.go) + `updateVegetationOnEdit` /
  `destroyUnsupportedVegetationAbove` (server/block_survival.go), wired into `reconcileEdit`
  (server/block_interact.go) ALONGSIDE the fluid neighbor notification. Breaking a block under a
  flower/sapling/short_grass/fern/bush/2-tall-plant now destroys + drops the unsupported plant
  (cascading 2-tall plants and stacked columns via the bounded 512-deep recursion), reusing the
  GAMEPLAY-06 `spawnBlockDrop` path + the `broadcastBlockUpdate(air)` to trackers. 1:1 cited against
  the jar: `VegetationBlock.updateShape`/`canSurvive` (== `belowState.is(SUPPORTS_VEGETATION)`),
  `DoublePlantBlock.canSurvive` (half-dependent), `Block.updateOrDestroy` (newState.isAir() ->
  `destroyBlock(pos, dropBlock=(flags&32)==0, null, recursionLeft=512)`). Tests:
  server/block_survival_test.go (break-under-flower drops, 2-tall cascade, non-vegetation no-op,
  still-supported survives, full-dig-path integration) + level/block/vegetation_test.go (predicate
  coverage). Gates: `CGO_ENABLED=0 go build/vet/test` green, Docker `-race` green.
- **STILL DEFERRED (a follow-up survival pass):** the NON-vegetation survival classes — torches/
  walls/ground torches (`DiodeBlock`/`BaseTorchBlock` ATTACHED-face survival), rails
  (`BaseRailBlock`), redstone (`RedStoneWireBlock`/`DiodeBlock`), doors/beds (two-cell), ladders,
  vines, signs, banners, pressure plates, and the DryVegetationBlock/FlowerBedBlock/LeafLitter/
  MangrovePropagule/Seagrass plants that use a DIFFERENT ground predicate than SUPPORTS_VEGETATION.
  Each is a distinct `canSurvive`/`updateShape` class to port; the generic `updateNeighborsAt` recursion
  seam (`destroyUnsupportedVegetationAbove`) is the template to extend (it currently checks only the
  cell ABOVE — the non-vegetation classes also need side/below neighbor checks for wall/floor mounts).
- **Found during:** Phase 17 visual gate (real client). User: "rompe un bloque con flores arriba, las flores no se rompen".
- **Symptom:** breaking a block that supports a plant/flower/torch/etc. leaves the unsupported block
  floating instead of breaking + dropping it. Vanilla destroys it the instant its support is removed.
- **Root cause:** Sulfur's edit path (`reconcileEdit` in `server/block_interact.go`) only runs the
  FLUID neighbor notification (`scheduleFluidNeighborsOnEdit`) — it does NOT run the generic
  `Level.updateNeighborsAt` → `BlockState.updateShape`/`neighborChanged` → `Block.updateOrDestroy`
  chain. So a block that loses its required support is never told to re-check `canSurvive`.
- **1:1 chain (jar-verified, ready to port):**
  - `net.minecraft.world.level.Level.setBlock` → `updateNeighborsAt(pos)` notifies the 6 neighbors.
  - A neighbor runs `BlockState.updateShape(state, ..., direction, neighborPos, neighborState, ...)`.
    For vegetation: `VegetationBlock.updateShape` → `if (!state.canSurvive(level, pos)) return AIR.defaultBlockState()`.
  - `VegetationBlock.canSurvive` = `mayPlaceOn(belowState, level, belowPos)` =
    `belowState.is(BlockTags.SUPPORTS_VEGETATION)` (the tag = `#substrate_overworld` + `farmland`).
  - `Block.updateOrDestroy(oldState, newState=AIR, level, pos, flags)` → since newState.isAir() and
    !isClientSide → `level.destroyBlock(pos, dropBlock=(flags & 32)==0, null, recursionLeft)` →
    drops the block (the same drop path GAMEPLAY-06 already has via `spawnBlockDrop`).
- **Why not fixed in Phase 17:** it is a net-new SUBSYSTEM, not a quick wire-up — it needs (a) a
  runtime block-tag lookup (the tag JSONs are currently send-to-client-only, not queried in Go),
  (b) a block→survival-class mapping extracted from the jar (which of the 881 blocks are
  `VegetationBlock` / need a support + their support predicate — flowers, saplings, grass, torches,
  rails, redstone, doors, etc.), and (c) the `updateOrDestroy` + recursive `updateNeighborsAt` port.
  Scoped OUT of Phase 17 (gameplay-completion of the SIX wired seams); the visual gate otherwise
  PASSES. User chose to defer + proceed to Phase 18.
- **Suggested handling (own plan/phase):** a "block survival" mini-phase — extract the needs-support
  block set + support tags via the codegen pipeline, port `updateOrDestroy` + `VegetationBlock.canSurvive`
  (+ the other common survival classes), and wire it into `reconcileEdit` alongside the existing fluid
  neighbor notification. The drop path already exists (GAMEPLAY-06). Start with vegetation (the visible
  80%), then torches/rails/redstone.

### New worldgen biome-tag JSONs emitted by the extractor re-run (17-21)

- **Found during:** Plan 17-21 (block-break dig-time), when re-running the codegen pipeline
  (`cd tools && go run . --version 26.2 --extract`) to extract block hardness from the jar.
- **Symptom:** the worldgen extractor (`tools/extract_worldgen.go` / `genWorldgen`) emitted ~20
  NEW untracked files under `world/levelgen/data/tags/worldgen/biome/` (e.g.
  `allows_surface_slime_spawns.json`, `spawns_cold_variant_frogs.json`, etc.) — biome tag data
  that was apparently not present in the previously-committed tree.
- **Why not committed here:** out of scope — 17-21 is the block-break dig-time + block-hardness
  extraction. These worldgen biome tags are unrelated worldgen data; committing them would mix an
  unrelated generated-data delta into the block-break commit. All OTHER generated files (blocks.go,
  item.go, entity.go, registry files, …) regenerated BYTE-IDENTICALLY (no diff), confirming the
  pipeline is reproducible and these tags are genuinely new output, not a spurious churn.
- **Suggested handling (for a worldgen/codegen plan):** review the new biome tags, confirm they
  are correct jar output, and commit them as a dedicated worldgen-data refresh (or add them to
  `.gitignore` if biome tags are intentionally not tracked).
