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
	default:
		return categoryMisc
	}
}
