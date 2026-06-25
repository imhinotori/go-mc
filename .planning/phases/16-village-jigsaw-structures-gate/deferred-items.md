# Phase 16 — Deferred Items

Items discovered during execution that are OUT OF SCOPE for the current plan and logged
for a future plan rather than fixed inline (per the GSD scope-boundary rule).

## 16-01

### Untracked 26.2 biome category tags (extractor surfaced, pre-existing gap)

Re-running `cd tools && go run . --version 26.2` (needed to extract the village .nbt /
template_pool / processor_list data for 16-01) also regenerated the FULL worldgen tree.
That surfaced **19 NEW worldgen/biome category tags** that exist in the 26.2 jar but were
never committed in Phase 14/15 (Phase 15 committed 49; the jar now ships 68):

```
world/levelgen/data/tags/worldgen/biome/allows_surface_slime_spawns.json
world/levelgen/data/tags/worldgen/biome/allows_tropical_fish_spawns_at_any_height.json
world/levelgen/data/tags/worldgen/biome/mineshaft_blocking.json
world/levelgen/data/tags/worldgen/biome/more_frequent_drowned_spawns.json
world/levelgen/data/tags/worldgen/biome/polar_bears_spawn_on_alternate_blocks.json
world/levelgen/data/tags/worldgen/biome/produces_corals_from_bonemeal.json
world/levelgen/data/tags/worldgen/biome/reduce_water_ambient_spawns.json
world/levelgen/data/tags/worldgen/biome/required_ocean_monument_surrounding.json
world/levelgen/data/tags/worldgen/biome/spawns_cold_variant_farm_animals.json
world/levelgen/data/tags/worldgen/biome/spawns_cold_variant_frogs.json
world/levelgen/data/tags/worldgen/biome/spawns_coral_variant_zombie_nautilus.json
world/levelgen/data/tags/worldgen/biome/spawns_gold_rabbits.json
world/levelgen/data/tags/worldgen/biome/spawns_snow_foxes.json
world/levelgen/data/tags/worldgen/biome/spawns_warm_variant_farm_animals.json
world/levelgen/data/tags/worldgen/biome/spawns_warm_variant_frogs.json
world/levelgen/data/tags/worldgen/biome/spawns_white_rabbits.json
world/levelgen/data/tags/worldgen/biome/water_on_map_outlines.json
world/levelgen/data/tags/worldgen/biome/without_wandering_trader_spawns.json
world/levelgen/data/tags/worldgen/biome/without_zombie_sieges.json
```

**Why deferred, not fixed here:** these are spawn/mob/ocean-monument biome category tags
owned by the structure-biome-tag subsystem (Phase 14/15 / future mob-spawn work), NOT by
STRUCT-05 (the .nbt StructureTemplate system). 16-01 added ONLY the `structure/village`,
`template_pool/village`, `processor_list`, and `empty.json` extractor entries. Absorbing
unrelated biome tags into a STRUCT-05 commit would violate the atomic-commit boundary.

**Impact:** none on 16-01. The `//go:embed tags` directive includes whatever is present on
disk; 16-01 references none of these tags. `go build`, `CGO_ENABLED=0 go build`, `go vet`,
and all tests pass with or without them. No build breakage.

**Action for a follow-up plan:** commit these 19 tags (they are legitimate 26.2 extractor
output) under the biome-tag subsystem, ideally alongside a count-assertion bump in the
relevant tags test, so a fresh checkout's embed matches the extractor output.
