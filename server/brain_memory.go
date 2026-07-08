package server

// brain_memory.go — the memory layer of the ported net.minecraft.world.entity.ai.Brain subsystem
// (temp/cache/26.2-inner.jar, javap -c -p / CFR this task). This ports, 1:1:
//
//   - net.minecraft.world.entity.ai.memory.MemoryStatus  (the enum VALUE_PRESENT/VALUE_ABSENT/REGISTERED)
//   - net.minecraft.world.entity.ai.memory.MemorySlot<T> (the value + timeToLive expiry cell; the
//     NEVER_EXPIRE == Long.MAX_VALUE sentinel; tick()/set/clear/hasValue/canExpire/hasExpired)
//   - net.minecraft.world.entity.ai.memory.MemoryModuleType<T> ids (the registry, data/registryid)
//
// DESIGN: vanilla keys memories on a generic MemoryModuleType<T> and stores a MemorySlot<T>. Go has no
// per-key value type in a single map, so a memoryKey is an int index (the registry id order,
// data/registryid/memorymoduletype.go) and the stored value is `any`. The typed accessors (getMemory*
// helpers on Brain) round-trip through `any` at the call site — the behaviors know their own value type.
// This preserves the EXACT slot semantics (present/absent/registered, per-tick ttl countdown) while
// staying idiomatic Go. Cite: MemorySlot, MemoryStatus, MemoryModuleType.

// memoryStatus ports net.minecraft.world.entity.ai.memory.MemoryStatus (the entry-condition qualifier
// a Behavior's entryCondition / an Activity's requirement checks a slot against).
//
//	[VERIFIED CFR MemoryStatus: enum { VALUE_PRESENT, VALUE_ABSENT, REGISTERED }.]
type memoryStatus uint8

const (
	memValuePresent memoryStatus = iota // VALUE_PRESENT — the slot exists AND holds a value
	memValueAbsent                      // VALUE_ABSENT  — the slot exists AND is empty
	memRegistered                       // REGISTERED    — the slot exists (value or not)
)

// memoryKey is a memory module type, keyed by its registry index (data/registryid.MemoryModuleType
// order). It stands in for net.minecraft.world.entity.ai.memory.MemoryModuleType<T>; the value type T
// is tracked by the caller (the behavior that reads/writes it), not the key.
type memoryKey int

// The memory module type keys the HappyGhast brain (and the framework's tick pipeline) reference. The
// numeric values are the registry indices in data/registryid/memorymoduletype.go (generated from
// 26.2/registries.json) — the 1:1 registry order. Only the memories actually used by ported behaviors
// are named here; the full set lives in the registry and any index is a valid memoryKey.
//
//	[VERIFIED data/registryid/memorymoduletype.go — indices match minecraft:<id> order in registries.json.]
const (
	memMobs                     memoryKey = 6  // minecraft:mobs
	memVisibleMobs              memoryKey = 7  // minecraft:visible_mobs
	memNearestPlayers           memoryKey = 9  // minecraft:nearest_players
	memNearestVisiblePlayer     memoryKey = 10 // minecraft:nearest_visible_player
	memWalkTarget               memoryKey = 13 // minecraft:walk_target
	memLookTarget               memoryKey = 14 // minecraft:look_target
	memInteractionTarget        memoryKey = 17 // minecraft:interaction_target
	memBreedTarget              memoryKey = 18 // minecraft:breed_target
	memPath                     memoryKey = 20 // minecraft:path
	memHurtBy                   memoryKey = 23 // minecraft:hurt_by
	memCantReachWalkTargetSince memoryKey = 29 // minecraft:cant_reach_walk_target_since
	memNearestVisibleAdult      memoryKey = 35 // minecraft:nearest_visible_adult
	memTemptingPlayer           memoryKey = 45 // minecraft:tempting_player
	memTemptationCooldownTicks  memoryKey = 46 // minecraft:temptation_cooldown_ticks
	memIsTempted                memoryKey = 48 // minecraft:is_tempted
	memIsInWater                memoryKey = 54 // minecraft:is_in_water
	memIsPanicking              memoryKey = 56 // minecraft:is_panicking

	// Villager POI + panic memories (registry indices from data/registryid/memorymoduletype.go; distinct
	// from the keys above). The brain uses these as unique slot keys — the AcquirePoi/AssignProfession/
	// updateActivity behaviors read/write them.
	//
	//	[VERIFIED data/registryid/memorymoduletype.go: home=1, job_site=2, potential_job_site=3,
	//	 meeting_point=4, hurt_by_entity=24, nearest_hostile=26.]
	memHome             memoryKey = 1  // minecraft:home
	memJobSite          memoryKey = 2  // minecraft:job_site
	memPotentialJobSite memoryKey = 3  // minecraft:potential_job_site
	memMeetingPoint     memoryKey = 4  // minecraft:meeting_point
	memHurtByEntity     memoryKey = 24 // minecraft:hurt_by_entity
	memNearestHostile   memoryKey = 26 // minecraft:nearest_hostile
)

// memNeverExpire is MemorySlot.NEVER_EXPIRE (Long.MAX_VALUE) — a slot with this ttl is permanent
// (canExpire() == false), so tick() never counts it down.
//
//	[VERIFIED CFR MemorySlot: private static final long NEVER_EXPIRE = Long.MAX_VALUE.]
const memNeverExpire int64 = 1<<63 - 1

// memorySlot ports net.minecraft.world.entity.ai.memory.MemorySlot<T>: a value cell with an expiry
// countdown. A newly registered slot is empty (value nil) and permanent (ttl == NEVER_EXPIRE).
type memorySlot struct {
	value any   // nil == empty (the @Nullable T value field)
	ttl   int64 // timeToLive; NEVER_EXPIRE == permanent
}

// newMemorySlot ports MemorySlot.create(): new MemorySlot<>(null, Long.MAX_VALUE).
//
//	[VERIFIED CFR MemorySlot.create: new MemorySlot(null, Long.MAX_VALUE).]
func newMemorySlot() *memorySlot { return &memorySlot{value: nil, ttl: memNeverExpire} }

// hasValue ports MemorySlot.hasValue(): value != null.
func (s *memorySlot) hasValue() bool { return s.value != nil }

// canExpire ports MemorySlot.canExpire(): timeToLive != NEVER_EXPIRE.
func (s *memorySlot) canExpire() bool { return s.ttl != memNeverExpire }

// hasExpired ports MemorySlot.hasExpired(): timeToLive <= 0.
func (s *memorySlot) hasExpired() bool { return s.ttl <= 0 }

// set ports MemorySlot.set(T, long): value = v; timeToLive = ttl.
func (s *memorySlot) set(v any, ttl int64) { s.value = v; s.ttl = ttl }

// clear ports MemorySlot.clear(): value = null; timeToLive = NEVER_EXPIRE.
func (s *memorySlot) clear() { s.value = nil; s.ttl = memNeverExpire }

// tick ports MemorySlot.tick(): while the slot holds an expiring value, either drop it (expired) or
// count the ttl down by one. This is the forgetOutdatedMemories per-tick machinery.
//
//	[VERIFIED CFR MemorySlot.tick: if (hasValue() && canExpire()) { if (hasExpired()) clear(); else --timeToLive; }.]
func (s *memorySlot) tick() {
	if s.hasValue() && s.canExpire() {
		if s.hasExpired() {
			s.clear()
		} else {
			s.ttl--
		}
	}
}
