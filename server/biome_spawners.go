package server

// biome_spawners.go - the biome MobSpawnSettings parse + the WeightedList weighted mob pick
// (gap-reaudit #9): the 1:1 port of the vanilla natural-spawn mob TYPE selection that replaces the
// earlier uniform stub (async.go pickNaturalSpawnMob / pickNaturalCreatureMob).
//
// PORTED (the STANDING MANDATE - idiomatic Go, never a GPL paste) from the unobfuscated 26.2 jar via
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.biome.MobSpawnSettings
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.biome.MobSpawnSettings.SpawnerData
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.util.random.WeightedList
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.util.random.WeightedRandom
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.NaturalSpawner
// (this session). The vanilla shapes this file translates:
//
//   MobSpawnSettings: a Map[MobCategory]WeightedList[SpawnerData] spawners (getMobs(category)
//     returns spawners.getOrDefault(category, EMPTY_MOB_LIST)). SpawnerData is a record
//     {EntityType type; int minCount; int maxCount} - the WEIGHT lives on the WeightedList
//     Weighted[SpawnerData] wrapper, NOT on SpawnerData (the biome JSON carries type/minCount/maxCount
//     PLUS the wrapper weight in one flat object).
//
//   WeightedList.getRandom(random): if the selector is null (totalWeight == 0) return Optional.empty()
//     WITHOUT drawing; else i = random.nextInt(totalWeight); return selector.get(i). The selector.get(i)
//     walk (WeightedList.Compact.get, the WeightedRandom.getWeightedItem algorithm) is the CUMULATIVE
//     walk: for each Weighted w: i -= w.weight(); if (i lt 0) return w.value(). So the observable pick
//     is: draw i in [0,totalWeight), walk the list subtracting each weight, return the first entry that
//     drives i negative. (The Flat/Compact split is a perf-only representation choice; both take the
//     same i and return the same element - the cumulative walk is the observable spec.)
//
//   NaturalSpawner.getRandomSpawnMobAt(level, ..., category, random, pos):
//     biome = level.getBiome(pos); ... ; return mobsAt(...).getRandom(random)   // ONE nextInt(total)
//   NaturalSpawner.spawnCategoryForPosition, when spawnerData == null (the first successful pack member):
//     spawnerData = getRandomSpawnMobAt(...); if empty -> break the pack loop (goto next group);
//     packSize (var 18) = spawnerData.minCount() + random.nextInt(1 + maxCount - minCount)  // RE-SET
//
// V1 SUBSET (documented, the SAME discipline the earlier uniform stub carried): the biome list holds
// many mob types the server cannot spawn yet (horse, donkey, slime, zombie_villager, ...). The pick is
// FILTERED to the types the mob registry can actually spawn AND that are in the explicit natural pool
// (naturalCreatureMobNames / naturalMonsterMobNames, async.go) - so a not-yet-ported type neither
// spawns nor is drawn. The total weight is computed over the FILTERED list, so the weighted walk is
// proportional-among-the-implemented-types (vanilla weighting, minus the un-ported entries). This is
// the faithful weighted pick over the ported subset; adding a mob to the pool + registry widens the
// list toward full vanilla weighting with no algorithm change. The nether-fortress + WATER_AMBIENT
// special branches of getRandomSpawnMobAt/mobsAt (isInNetherFortressBounds -> FORTRESS_ENEMIES, the
// REDUCED_WATER_AMBIENT_SPAWNS 0.98 roll) are NOT reached by the overworld CREATURE/MONSTER passes v1
// runs and are cited-deferred with the water/nether spawn categories.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/imhinotori/sulfur/level/biome"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// biomeSpawnerData ports MobSpawnSettings.SpawnerData PLUS the weight off its WeightedList
// Weighted[SpawnerData] wrapper (the biome JSON flattens the two into one object:
// {type, weight, minCount, maxCount}). typeName is the bare vanilla entity id (pig, the minecraft:
// prefix stripped) so it maps to a data/entity record + a declared mob via the registry.
// Cite MobSpawnSettings.SpawnerData (type/minCount/maxCount) + Weighted.weight().
type biomeSpawnerData struct {
	typeName string
	weight   int
	minCount int
	maxCount int
}

// biomeSpawnersJSON is the on-disk shape of one biome entry spawners section: per serialized MobCategory
// name (the JSON keys creature/monster/... = MobCategory.getSerializedName) a list of flat spawner
// objects. Only spawners is decoded; the rest of the biome entry is the RegistryData path concern.
type biomeSpawnersJSON struct {
	Spawners map[string][]struct {
		Type     string `json:"type"`
		Weight   int    `json:"weight"`
		MinCount int    `json:"minCount"`
		MaxCount int    `json:"maxCount"`
	} `json:"spawners"`
}

// serializedName ports net.minecraft.world.entity.MobCategory.getSerializedName - the JSON key the
// biome spawners map uses per category (the same string MobSpawnSettings SimpleMapCodec keys on). Read
// directly from the enum constructor serializedName arg (mob_category.go cites the ctor).
func (c mobCategory) serializedName() string {
	switch c {
	case categoryMonster:
		return "monster"
	case categoryCreature:
		return "creature"
	case categoryAmbient:
		return "ambient"
	case categoryAxolotls:
		return "axolotls"
	case categoryUndergroundWaterCreature:
		return "underground_water_creature"
	case categoryWaterCreature:
		return "water_creature"
	case categoryWaterAmbient:
		return "water_ambient"
	case categoryMisc:
		return "misc"
	default:
		return ""
	}
}

// biomeSpawnTable is the parsed, immutable per-biome MobSpawnSettings: biome id (minecraft:plains) ->
// category -> the WeightedList[SpawnerData] in JSON LIST ORDER (the cumulative-walk order MUST match the
// JSON order, the vanilla datagen order the WeightedList is built from). Loaded ONCE at first use
// (sync.Once) from the embedded 26.2 biome registry - the same authoritative jar-derived data the
// RegistryData send path uses. Read-only after load, so a tick-goroutine read is race-free.
var (
	biomeSpawnTable   map[string]map[mobCategory][]biomeSpawnerData
	biomeSpawnOnce    sync.Once
	biomeSpawnLoadErr error
)

// loadBiomeSpawners parses every embedded biome JSON spawners section into biomeSpawnTable. Each
// category list is kept in JSON order (the WeightedList build order). A biome/category with no list is
// simply absent (getMobs returns EMPTY_MOB_LIST -> getRandom returns empty -> the spawn breaks).
// Idempotent via sync.Once; the first caller triggers the parse, later callers see the cached table.
func loadBiomeSpawners() (map[string]map[mobCategory][]biomeSpawnerData, error) {
	biomeSpawnOnce.Do(func() {
		files, err := registrydata.BiomeFiles()
		if err != nil {
			biomeSpawnLoadErr = fmt.Errorf("biome spawners: list: %w", err)
			return
		}
		table := make(map[string]map[mobCategory][]biomeSpawnerData, len(files))
		catByName := map[string]mobCategory{}
		for c := categoryMonster; c <= categoryMisc; c++ {
			catByName[c.serializedName()] = c
		}
		for _, name := range files {
			data, err := registrydata.ReadBiome(name)
			if err != nil {
				biomeSpawnLoadErr = fmt.Errorf("biome spawners: read %s: %w", name, err)
				return
			}
			var doc biomeSpawnersJSON
			if err := json.Unmarshal(data, &doc); err != nil {
				biomeSpawnLoadErr = fmt.Errorf("biome spawners: decode %s: %w", name, err)
				return
			}
			id := "minecraft:" + strings.TrimSuffix(name, ".json")
			perCat := make(map[mobCategory][]biomeSpawnerData, len(doc.Spawners))
			for catName, list := range doc.Spawners {
				cat, ok := catByName[catName]
				if !ok || len(list) == 0 {
					continue
				}
				entries := make([]biomeSpawnerData, 0, len(list))
				for _, e := range list {
					entries = append(entries, biomeSpawnerData{
						typeName: strings.TrimPrefix(e.Type, "minecraft:"),
						weight:   e.Weight,
						minCount: e.MinCount,
						maxCount: e.MaxCount,
					})
				}
				perCat[cat] = entries
			}
			table[id] = perCat
		}
		biomeSpawnTable = table
	})
	return biomeSpawnTable, biomeSpawnLoadErr
}

// mobNameForType maps a bare vanilla entity id (pig) to the DECLARED mob name (vanilla_pig) the registry
// can spawn, or ok=false when no loaded declaration renders that base type. It resolves the name via the
// registry captured base types (decl.baseType.Name), so the biome JSON type is tied to a real spawnable
// mob exactly as vanilla SpawnerData.type() is an EntityType the level can create. A type with no
// declaration (horse, slime, ...) returns ok=false and is filtered out of the weighted pick (the V1
// subset - only ported types spawn). Read-only over the registry (written at boot).
func (t *TickLoop) mobNameForType(typeName string) (string, bool) {
	if t.mobRegistry == nil {
		return "", false
	}
	for name, decl := range t.mobRegistry.byName {
		if decl != nil && decl.baseType.Name == typeName {
			return name, true
		}
	}
	return "", false
}

// biomeIDAt looks up the biome id string (minecraft:plains) at a world block position via the loaded
// chunk biome storage (world.ChunkManager.BiomeAt -> ServerLevel.getBiome analogue). ok=false when the
// column is not loaded (the caller then treats the biome list as empty - no pick, break the pack loop),
// mirroring the fact that vanilla only spawns in loaded chunks.
func (t *TickLoop) biomeIDAt(x, y, z int) (string, bool) {
	if t.world() == nil {
		return "", false
	}
	bt, ok := t.world().BiomeAt(pk.Position{X: x, Y: y, Z: z}, dimMinY)
	if !ok {
		return "", false
	}
	return biome.Type(bt).String(), true
}

// pickBiomeSpawnMob ports NaturalSpawner.getRandomSpawnMobAt for the overworld CREATURE/MONSTER passes:
// look up the biome at (x,y,z), take its category WeightedList[SpawnerData] (MobSpawnSettings.getMobs),
// FILTER to the types the registry can spawn AND that are in the explicit natural pool for the category,
// then run WeightedList.getRandom weighted walk over the filtered list on the region levelRandom.
//
// The RNG contract (the crucial spawning-determinism invariant): when the filtered list is NON-EMPTY it
// draws EXACTLY ONE lr.NextIntN(totalWeight) (WeightedList.getRandom single nextInt(totalWeight)) and
// walks the cumulative weights. When the filtered list is EMPTY it draws NOTHING and returns ok=false -
// mirroring getRandom early "if selector == null return Optional.empty()" (totalWeight == 0, no draw),
// which the caller turns into the vanilla "if spawnerData == null break" of the pack loop. So the draw
// count matches vanilla: one nextInt per successful pick, zero on an empty list.
//
// It MUST be called inside a withRegion scope (cur() resolves the owning region seeded levelRandom); off
// a region it falls back / panics under strictRegion exactly as the rest of the spawn path does.
func (t *TickLoop) pickBiomeSpawnMob(x, y, z int, cat mobCategory) (data biomeSpawnerData, mobName string, ok bool) {
	table, err := loadBiomeSpawners()
	if err != nil || table == nil {
		return biomeSpawnerData{}, "", false
	}
	id, okB := t.biomeIDAt(x, y, z)
	if !okB {
		return biomeSpawnerData{}, "", false
	}
	list := table[id][cat] // getMobs(category); a missing biome/category -> nil == EMPTY_MOB_LIST

	// Restrict to the explicit natural pool for the category (the SAME intentional list the uniform stub
	// used). Build a small membership set for the O(1) test.
	pool := naturalPoolFor(cat)

	// Filter to (in pool) AND (registry-resolvable), preserving JSON order (the walk order). Sum the
	// filtered weights - WeightedList.totalWeight over the ported subset (WeightedRandom.getTotalWeight).
	type filtered struct {
		data biomeSpawnerData
		name string
	}
	kept := make([]filtered, 0, len(list))
	total := 0
	for _, sd := range list {
		if sd.weight <= 0 {
			continue // a non-positive weight contributes nothing (defensive; vanilla weights are >=1)
		}
		name, resolvable := t.mobNameForType(sd.typeName)
		if !resolvable || !pool[name] {
			continue
		}
		kept = append(kept, filtered{data: sd, name: name})
		total += sd.weight
	}
	if total <= 0 || len(kept) == 0 {
		// EMPTY selector: getRandom returns Optional.empty() WITHOUT drawing. No nextInt here.
		return biomeSpawnerData{}, "", false
	}

	// WeightedList.getRandom: i = random.nextInt(totalWeight); walk the cumulative weights.
	lr := t.cur().levelRandom
	i := int(lr.NextIntN(int32(total)))
	for _, f := range kept {
		i -= f.data.weight
		if i < 0 {
			return f.data, f.name, true
		}
	}
	// Unreachable when total is the exact sum of the walked weights (mirrors Compact.get guaranteed hit);
	// return the last as a defensive fallback rather than panic.
	last := kept[len(kept)-1]
	return last.data, last.name, true
}

// naturalPoolFor returns the explicit natural-spawn pool for a category as a membership set. It reuses
// the SAME slices the uniform stub picks from (naturalCreatureMobNames / naturalMonsterMobNames,
// async.go) so the weighted pick and the (removed) uniform pick draw from the identical intentional set
// - only the DISTRIBUTION changes (weighted, not uniform). A category with no v1 pool (ambient, water,
// misc) returns an empty set -> no pick -> no spawn, matching that v1 only runs CREATURE + MONSTER.
func naturalPoolFor(cat mobCategory) map[string]bool {
	var names []string
	switch cat {
	case categoryMonster:
		names = naturalMonsterMobNames
	case categoryCreature:
		names = naturalCreatureMobNames
	default:
		return map[string]bool{}
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// packSizeFromSpawnerData ports the packSize RE-SET in NaturalSpawner.spawnCategoryForPosition when a
// SpawnerData is first picked (var 18 = spawnerData.minCount() + random.nextInt(1 + maxCount - minCount)).
// It draws ONE nextInt(1 + maxCount - minCount) on the region levelRandom - matching the vanilla draw
// exactly at the point (getOrCreateNextSpawnData) where the spawner re-sets the pack size from the
// picked data. Cite NaturalSpawner.spawnCategoryForPosition.
func (t *TickLoop) packSizeFromSpawnerData(sd biomeSpawnerData) int {
	span := int32(1 + sd.maxCount - sd.minCount)
	if span <= 0 {
		return sd.minCount // maxCount <= minCount (never in vanilla data): no draw, just minCount
	}
	return sd.minCount + int(t.cur().levelRandom.NextIntN(span))
}
