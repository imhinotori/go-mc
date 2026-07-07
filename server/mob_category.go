package server

import "github.com/imhinotori/sulfur/data/entity"

// mob_category.go — AI-03: the ported net.minecraft.world.entity.MobCategory.
//
// PORTED (the STANDING MANDATE) from the unobfuscated 26.2 jar via
//   `javap -p -c -classpath temp/cache/26.2-inner.jar net.minecraft.world.entity.MobCategory`
// (this session). MobCategory is an enum; its constructor is
//   MobCategory(String name, String serializedName, String debugAbbrev,
//               int max, boolean isFriendly, boolean isPersistent, int despawnDistance)
// and getMaxInstancesPerChunk() returns the `max` field. The per-enum `max` values are read
// directly from the static-initializer bytecode (the `int` constructor arg), e.g. for CREATURE
//   28: ldc "CREATURE"; 30: iconst_1; 31: ldc "creature"; 33: ldc "C";
//   35: bipush 10;      <-- max = 10 (getMaxInstancesPerChunk)
//   37: iconst_1 (isFriendly=true); 38: iconst_1 (isPersistent=true); 39: sipush 128 (despawnDist)
// The full table the bytecode yields:
//   MONSTER                     max=70   (bipush 70)
//   CREATURE                    max=10   (bipush 10)   <- the v1-relevant cap (the Pig's category)
//   AMBIENT                     max=15   (bipush 15)
//   AXOLOTLS                    max=5    (iconst_5)
//   UNDERGROUND_WATER_CREATURE  max=5    (iconst_5)
//   WATER_CREATURE              max=5    (iconst_5)
//   WATER_AMBIENT               max=20   (bipush 20)
//   MISC                        max=-1   (iconst_m1)
//
// SINGLE-OWNER (TICK-05): mobCategory is a pure value type; the live per-category COUNT that
// gates spawning (spawner.go countByCategory) ranges the tick-owned entityStore on the tick
// goroutine. No shared state, no goroutine, no xsync.

// mobCategory is the ported MobCategory enum. The ordinal order mirrors the jar's enum
// declaration order (MONSTER=0 … MISC=7) so a future serialized-name/ordinal mapping lines up.
type mobCategory int

const (
	categoryMonster                  mobCategory = iota // MONSTER
	categoryCreature                                    // CREATURE  (the Pig's category — v1)
	categoryAmbient                                     // AMBIENT
	categoryAxolotls                                    // AXOLOTLS
	categoryUndergroundWaterCreature                    // UNDERGROUND_WATER_CREATURE
	categoryWaterCreature                               // WATER_CREATURE
	categoryWaterAmbient                                // WATER_AMBIENT
	categoryMisc                                        // MISC
)

// maxInstancesPerChunk ports MobCategory.getMaxInstancesPerChunk() — the per-chunk cap the
// natural spawner multiplies by the spawnable-chunk count to derive the global per-category
// cap (NaturalSpawner.getFilteredSpawningCategories / SpawnState). The values are the `max`
// constructor argument read from the static-initializer bytecode (cited above). v1 only spawns
// CREATURE, but the whole table is ported faithfully so adding MONSTER/AMBIENT later is a
// one-line categoryOf change, not a re-port. MISC's -1 is "uncapped" in vanilla (it is filtered
// out of SPAWNING_CATEGORIES); v1 never spawns it.
func (c mobCategory) maxInstancesPerChunk() int {
	switch c {
	case categoryMonster:
		return 70
	case categoryCreature:
		return 10 // the load-bearing v1 cap (Pitfall 3 anti-flood) — javap-confirmed
	case categoryAmbient:
		return 15
	case categoryAxolotls:
		return 5
	case categoryUndergroundWaterCreature:
		return 5
	case categoryWaterCreature:
		return 5
	case categoryWaterAmbient:
		return 20
	case categoryMisc:
		return -1
	default:
		return -1
	}
}

// despawnDistance ports MobCategory.getDespawnDistance() — the hard cull radius in blocks past which
// checkDespawn removes the mob outright (`d > despawn*despawn`). The value is the last constructor int
// arg read from the static-initializer bytecode: 128 for every category except WATER_AMBIENT (64).
// Cite net.minecraft.world.entity.MobCategory.getDespawnDistance.
func (c mobCategory) despawnDistance() int {
	if c == categoryWaterAmbient {
		return 64
	}
	return 128
}

// noDespawnDistance ports MobCategory.getNoDespawnDistance() — inside this radius a mob NEVER despawns
// and its idle counter resets. The getter ignores the per-category field and returns the literal 32
// for ALL categories (javap: `bipush 32; ireturn`). Cite MobCategory.getNoDespawnDistance.
func (c mobCategory) noDespawnDistance() int { return 32 }

// maxSpawnClusterSize ports Mob.getMaxSpawnClusterSize() — the cap on how many mobs the natural
// spawner's OUTER pack-group loop places at ONE candidate position before it returns
// (spawnCategoryForPosition: `if (spawnedInGroup >= mob.getMaxSpawnClusterSize()) return;`). The base
// Mob impl returns the literal 4 (javap `iconst_4; ireturn`); the per-species overrides
// (e.g. animals, fish schools) tune it. v1 has no per-species Mob override yet, so this is the cited
// base constant 4, structured as a helper so a per-type override slots in later without a call-site
// change. Cite net.minecraft.world.entity.Mob.getMaxSpawnClusterSize.
const maxSpawnClusterSize = 4

// isMaxGroupSizeReached ports Mob.isMaxGroupSizeReached(int) — the INNER pack-loop break gate
// (spawnCategoryForPosition: `if (mob.isMaxGroupSizeReached(spawnedInPack)) break;`). The base Mob
// impl returns false unconditionally (javap `iconst_0; ireturn`); Animal/PathfinderMob overrides
// bound the pack size by the biome spawn-group max. v1 uses the base false (the pack size is bounded
// only by the drawn packSize + maxSpawnClusterSize), structured as a helper so a per-type override
// slots in later. Cite net.minecraft.world.entity.Mob.isMaxGroupSizeReached.
func isMaxGroupSizeReached(spawnedInPack int) bool { return false }

// categoryOf maps a live entity's wire type to its MobCategory. In vanilla this is the
// EntityType.category field set at registration (e.g. EntityType.PIG is built with
// MobCategory.CREATURE). v1 only needs the Pig -> CREATURE mapping for the cap accounting;
// every other type defaults to MISC (uncapped, never spawned by v1) so a non-creature entity
// in the store neither consumes nor starves the CREATURE budget. As more spawnable mobs are
// added, extend this switch with their javap-read EntityType.category.
func categoryOf(t entity.ID) mobCategory {
	switch t {
	case entity.Pig.ID:
		return categoryCreature
	case entity.Cow.ID:
		// MOB-PASS-01 (Phase 34): the Cow is MobCategory.CREATURE (vanilla EntityType.COW is built with
		// MobCategory.CREATURE; data/entity Cow.Type == "creature", the jar-derived codegen source). Like
		// the pig, the cow consumes the CREATURE spawn budget. Additive — the pig + default arms are
		// untouched, so the pig oracle's category accounting is unperturbed.
		return categoryCreature
	case entity.Sheep.ID:
		// MOB-PASS-02 (Phase 34): Sheep is MobCategory.CREATURE (vanilla EntityType.SHEEP; data/entity
		// Sheep.Type == "creature"). Consumes the CREATURE spawn budget, so countByCategory tallies it and
		// the natural-spawn cap bounds it — the vanilla anti-flood cap holds for all 4 passives.
		return categoryCreature
	case entity.Chicken.ID:
		// MOB-PASS-03 (Phase 34): Chicken is MobCategory.CREATURE (vanilla EntityType.CHICKEN; data/entity
		// Chicken.Type == "creature"). Same CREATURE-budget accounting as pig/cow/sheep.
		return categoryCreature
	case entity.Zombie.ID:
		// MOB-HOST-01 (Phase 35): Zombie is MobCategory.MONSTER (vanilla EntityType.ZOMBIE is built with
		// MobCategory.MONSTER; data/entity Zombie.Type == "monster", the jar-derived codegen source —
		// confirmed entity.go:1386). Consumes the MONSTER spawn budget (cap 70 via maxInstancesPerChunk),
		// so countByCategory tallies it under categoryMonster with ZERO counting-code change. Additive —
		// the pig/cow/sheep/chicken CREATURE arms + the default arm are untouched (the pig oracle's
		// category accounting is unperturbed).
		return categoryMonster
	case entity.Skeleton.ID:
		// MOB-HOST-02 (Phase 35): Skeleton is MobCategory.MONSTER (vanilla EntityType.SKELETON; data/entity
		// Skeleton.Type == "monster", entity.go:1062). Same MONSTER-budget accounting as the zombie — the
		// monsterCap bounds it once categoryOf returns categoryMonster here.
		return categoryMonster
	case entity.Spider.ID:
		// MOB-HOST-03 (Phase 35): Spider is MobCategory.MONSTER (vanilla EntityType.SPIDER; data/entity
		// Spider.Type == "monster", entity.go:1143). Same MONSTER-budget accounting; countByCategory +
		// countByCategoryAcrossRegions tally all 3 hostiles under the one MONSTER cap.
		return categoryMonster
	case entity.Wolf.ID:
		// MOB-NEUT-01 (Phase 36): the Wolf is MobCategory.CREATURE (vanilla EntityType.WOLF is built with
		// MobCategory.CREATURE; data/entity Wolf.Type == "creature", entity.go:1368 — ID 149). Like the
		// other passives it consumes the CREATURE spawn budget. Additive — the pig + the 35 MONSTER arms +
		// the default arm are untouched (the pig oracle's category accounting is unperturbed).
		return categoryCreature
	case entity.Husk.ID:
		// MOB-VARIANT (Task #9): Husk is MobCategory.MONSTER (vanilla EntityType.HUSK; Husk extends Zombie
		// which is MONSTER; data/entity Husk.Type == "monster"). Same MONSTER-budget accounting as the zombie.
		return categoryMonster
	case entity.Mooshroom.ID:
		// MOB-VARIANT (Task #9): Mooshroom is MobCategory.CREATURE (vanilla EntityType.MOOSHROOM;
		// MushroomCow extends AbstractCow which is CREATURE; data/entity Mooshroom.Type == "creature").
		// Same CREATURE-budget accounting as the cow.
		return categoryCreature
	case entity.Silverfish.ID:
		// MOB-HOST-05 (Task #9): Silverfish is MobCategory.MONSTER (vanilla EntityType.SILVERFISH;
		// data/entity Silverfish.Type == "monster"). Same MONSTER-budget accounting as the zombie.
		return categoryMonster
	case entity.Creeper.ID:
		// MOB-HOST-06 (Task #9): Creeper is MobCategory.MONSTER (vanilla EntityType.CREEPER; data/entity
		// Creeper.Type == "monster"). Same MONSTER-budget accounting as the zombie.
		return categoryMonster
	case entity.Witch.ID:
		// MOB-HOST-07 (Task #9): Witch is MobCategory.MONSTER (vanilla EntityType.WITCH; data/entity
		// Witch.Type == "monster"). Same MONSTER-budget accounting as the zombie.
		return categoryMonster
	case entity.Rabbit.ID:
		// MOB-PASS-05 (Task #9): Rabbit is MobCategory.CREATURE (vanilla EntityType.RABBIT; data/entity
		// Rabbit.Type == "creature"). Same CREATURE-budget accounting as the cow — an overworld passive.
		return categoryCreature
	case entity.Enderman.ID:
		// MOB-HOST-08 (Task #9): Enderman is MobCategory.MONSTER (vanilla EntityType.ENDERMAN; data/entity
		// Enderman.Type == "monster"). Same MONSTER-budget accounting as the zombie — an overworld hostile.
		return categoryMonster
	case entity.Cat.ID:
		// MOB-NEUT-03 (Task #9): Cat is MobCategory.CREATURE (vanilla EntityType.CAT; data/entity
		// Cat.Type == "creature"). Same CREATURE-budget accounting as the wolf/cow.
		return categoryCreature
	case entity.Fox.ID:
		// MOB-PASS-06 (Task #9): Fox is MobCategory.CREATURE (vanilla EntityType.FOX; data/entity
		// Fox.Type == "creature"). Same CREATURE-budget accounting as the cow.
		return categoryCreature
	case entity.SulfurCube.ID:
		// MOB-CUBE (SulfurCube): SulfurCube is MobCategory.MONSTER (jar EntityTypes.SULFUR_CUBE =
		// EntityType.Builder.of(SulfurCube::new, MobCategory.MONSTER); data/entity SulfurCube.Type == "monster").
		// Consumes the MONSTER spawn budget like the zombie. (It spawns naturally ONLY in the sulfur_caves
		// biome — biome-gated, so it is kept OUT of the uniform natural MONSTER pool; see async.go.)
		return categoryMonster
	case entity.HappyGhast.ID:
		// happy_ghast (Task): HappyGhast is MobCategory.CREATURE (vanilla EntityType.HAPPY_GHAST extends
		// Animal; data/entity HappyGhast.Type == "creature", id 58). Consumes the CREATURE spawn budget
		// like the other passives — though it never joins the NATURAL pool (dried-ghast rehydration only,
		// cited), so the category matters only for the shared count accounting.
		return categoryCreature
	case entity.Endermite.ID:
		// MOB-PREY (Task #9): Endermite is MobCategory.MONSTER (vanilla EntityType.ENDERMITE; data/entity
		// Endermite.Type == "monster", id 42). Same MONSTER-budget accounting as the zombie. (Endermite is NOT
		// naturally spawned - only from an EnderMan teleport, special-gated - so it stays OUT of the uniform
		// natural MONSTER pool; see async.go, like husk/silverfish.)
		return categoryMonster
	case entity.Turtle.ID:
		// MOB-PREY (Task #9): Turtle is MobCategory.CREATURE (vanilla EntityType.TURTLE; data/entity
		// Turtle.Type == "creature", id 138). Same CREATURE-budget accounting as the cow. (Turtle spawns only
		// on BEACH biomes - biome-gated - so it stays OUT of the uniform natural CREATURE pool.)
		return categoryCreature
	case entity.Ocelot.ID:
		// MOB-PREY (Task #9): Ocelot is MobCategory.CREATURE (vanilla EntityType.OCELOT; data/entity
		// Ocelot.Type == "creature", id 91). Same CREATURE-budget accounting as the cat. (Ocelot spawns only
		// in JUNGLE biomes - biome-gated - so it stays OUT of the uniform natural CREATURE pool.)
		return categoryCreature
	case entity.Pillager.ID:
		// RAIDER (Task): Pillager is MobCategory.MONSTER (vanilla EntityTypes.PILLAGER = Builder.of(...,
		// MobCategory.MONSTER); data/entity Pillager.Type == "monster", id 103). Same MONSTER-budget accounting
		// as the zombie. (Pillager spawns naturally ONLY via the PatrolSpawner special path - NOT the uniform
		// natural pool - so it stays OUT of naturalMonsterMobNames; async.go.)
		return categoryMonster
	case entity.Vindicator.ID:
		// RAIDER (Task): Vindicator is MobCategory.MONSTER (vanilla EntityTypes.VINDICATOR; data/entity
		// Vindicator.Type == "monster", id 141). Same MONSTER-budget accounting. (Vindicator spawns ONLY in
		// raids + woodland mansions - never a uniform natural spawn - so it stays OUT of the natural pool.)
		return categoryMonster
	case entity.Evoker.ID:
		// RAIDER (Task): Evoker is MobCategory.MONSTER (vanilla EntityTypes.EVOKER; data/entity Evoker.Type
		// == "monster", id 46). Same MONSTER-budget accounting. (Evoker spawns ONLY in raids + woodland
		// mansions - never a uniform natural spawn - so it stays OUT of the natural pool.)
		return categoryMonster
	case entity.Ravager.ID:
		// RAIDER (Task): Ravager is MobCategory.MONSTER (vanilla EntityTypes.RAVAGER = Builder.of(...,
		// MobCategory.MONSTER).sized(1.95, 2.2); data/entity Ravager.Type == "monster", id 109). Same MONSTER-
		// budget accounting. (Ravager spawns ONLY in raids - never a uniform natural spawn - so it stays OUT.)
		return categoryMonster
	case entity.IronGolem.ID:
		// IRON GOLEM (Task): IronGolem is MobCategory.MISC (vanilla EntityTypes.IRON_GOLEM = Builder.of(...,
		// MobCategory.MISC); data/entity IronGolem.Type == "misc", id 70). MISC's cap is -1 (uncapped) and MISC
		// mobs are NOT placed by the uniform natural spawner, so the golem stays OUT of the natural pool — it is
		// village-summoned or manually placed (/dbg). Explicit (rather than the misc default) for recipe parity.
		return categoryMisc
	case entity.Villager.ID:
		// VILLAGER (Task): Villager is MobCategory.MISC (vanilla EntityType.VILLAGER = Builder.of(...,
		// MobCategory.MISC); data/entity Villager.Type == "misc", id 140). MISC's cap is -1 (uncapped) and MISC
		// mobs are NOT placed by the uniform natural spawner, so the villager stays OUT of the natural pool — a
		// real villager spawns from a village structure or breeding (both cite-deferred: no village worldgen).
		// Explicit (rather than the misc default) for recipe parity with the iron golem.
		return categoryMisc
	default:
		return categoryMisc
	}
}
