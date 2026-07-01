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
	default:
		return categoryMisc
	}
}
