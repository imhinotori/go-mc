package server

// villager_data.go — the VillagerData model (net.minecraft.world.entity.npc.villager.VillagerData) plus
// the profession <-> job-site-POI mapping. A LITERAL port of the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p / CFR this session).
//
// VillagerData is a record{ Holder<VillagerType> type, Holder<VillagerProfession> profession, int level }.
// Here the two Holders are reduced to their registry-path strings (the villagerType/villagerProfession
// fields on the Entity) — the wire/render only needs the path, and the profession path is what the
// AssignProfessionFromJobSite behavior sets from the claimed job-site POI. The numeric level is clamped to
// [MIN_VILLAGER_LEVEL, MAX_VILLAGER_LEVEL].
//
//	[VERIFIED javap VillagerData: fields type/profession/level; static init MIN_VILLAGER_LEVEL=1 (iconst),
//	 MAX_VILLAGER_LEVEL=5 (iconst_5), NEXT_LEVEL_XP_THRESHOLDS = {0,10,70,150,250}. ctor clamps
//	 level = Math.max(MIN_VILLAGER_LEVEL, level) (never below 1).]
const (
	minVillagerLevel = 1 // VillagerData.MIN_VILLAGER_LEVEL
	maxVillagerLevel = 5 // VillagerData.MAX_VILLAGER_LEVEL

	// villagerProfessionNone is VillagerProfession.NONE — the default (a freshly-spawned villager has no
	// profession until it claims a job site). VillagerData.DEFAULT_TYPE is "plains".
	villagerProfessionNone = "none"
	villagerTypeDefault    = "plains" // VillagerData.DEFAULT_TYPE (ResourceKey path)
)

// nextLevelXpThresholds is VillagerData.NEXT_LEVEL_XP_THRESHOLDS = {0,10,70,150,250} (the merchant XP a
// villager needs to reach the next level). Landed as the DATA anchor for the (deferred) trade-XP economy;
// getMinXpPerLevel/getMaxXpPerLevel index into it. VERIFIED VillagerData static init bytecode.
var nextLevelXpThresholds = [...]int{0, 10, 70, 150, 250}

// villagerCanLevelUp ports VillagerData.canLevelUp(level): level >= MIN_VILLAGER_LEVEL(1) &&
// level < MAX_VILLAGER_LEVEL(5). A level-5 villager cannot level further.
//
//	[VERIFIED CFR VillagerData.canLevelUp: iload_0; iconst_1; if_icmplt false; iload_0; iconst_5;
//	 if_icmpge false; true.]
func villagerCanLevelUp(level int) bool {
	return level >= minVillagerLevel && level < maxVillagerLevel
}

// villagerGetMinXpPerLevel ports VillagerData.getMinXpPerLevel(level): canLevelUp(level) ?
// NEXT_LEVEL_XP_THRESHOLDS[level - 1] : 0.
//
//	[VERIFIED CFR VillagerData.getMinXpPerLevel: canLevelUp ? NEXT_LEVEL_XP_THRESHOLDS[level-1] : 0.]
func villagerGetMinXpPerLevel(level int) int {
	if villagerCanLevelUp(level) {
		return nextLevelXpThresholds[level-1]
	}
	return 0
}

// villagerGetMaxXpPerLevel ports VillagerData.getMaxXpPerLevel(level): canLevelUp(level) ?
// NEXT_LEVEL_XP_THRESHOLDS[level] : 0. This is the XP a villager must accumulate at its current level to
// be eligible to level up (shouldIncreaseLevel compares villagerXp against it).
//
//	[VERIFIED CFR VillagerData.getMaxXpPerLevel: canLevelUp ? NEXT_LEVEL_XP_THRESHOLDS[level] : 0.]
func villagerGetMaxXpPerLevel(level int) int {
	if villagerCanLevelUp(level) {
		return nextLevelXpThresholds[level]
	}
	return 0
}

// clampVillagerLevel ports VillagerData's level clamp (Math.max(MIN, level), then the max-5 cap the
// codec/withLevel enforce).
func clampVillagerLevel(level int) int {
	if level < minVillagerLevel {
		return minVillagerLevel
	}
	if level > maxVillagerLevel {
		return maxVillagerLevel
	}
	return level
}

// professionForJobSitePoi ports AssignProfessionFromJobSite: a villager that has claimed a job-site POI is
// assigned the profession whose acquirableJobSite == that POI type. Since every job-site POI's key path
// (e.g. "minecraft:farmer") equals its profession's registry path ("farmer") by construction (VillagerProfession
// .register(KEY, PoiTypes.<SAME_KEY>, ...)), the mapping is the POI key path. Returns "none" for a nil/non-
// job-site POI (a villager holding no job site stays professionless).
//
//	[VERIFIED VillagerProfession.bootstrap: register(FARMER, PoiTypes.FARMER, ...), register(ARMORER,
//	 PoiTypes.ARMORER, ...) etc — profession key path == job-site POI key path, 1:1.]
func professionForJobSitePoi(pt *poiType) string {
	if pt == nil || !acquirableJobSitePois[pt] {
		return villagerProfessionNone
	}
	// pt.key is "minecraft:<name>"; the profession path is the same <name>.
	return stripNamespace(pt.key)
}

// stripNamespace drops the "minecraft:" (or any "ns:") prefix from a registry key, returning the bare path.
func stripNamespace(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[i+1:]
		}
	}
	return key
}
