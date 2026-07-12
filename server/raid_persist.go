package server

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"

	"github.com/imhinotori/sulfur/nbt"

	"github.com/google/uuid"
)

// raid_persist.go — SavedData persistence for the Raids MANAGER (raids.go), ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.entity.raid.Raids + its SavedDataStorage envelope).
// A vanilla 26.2 server reading world/data/raids.dat expects the EXACT tag names/types produced
// here, so this is a compatibility surface (the .dat is portable between Ender and vanilla).
//
// 1:1 ANCHORS (VERIFIED CFR/javap this session):
//   - net.minecraft.world.level.saveddata.SavedData: the isDirty()/setDirty(bool) contract. The
//     manager carries a `dirty` flag; setDirty() marks it; the periodic save clears it after a
//     successful write. (raidsManager.dirty in raids.go is the SavedData.dirty field.)
//   - net.minecraft.world.level.storage.SavedDataStorage.encodeUnchecked / getDataFile / tryWrite:
//     the on-disk file is <dataFolder>/<id>.dat == world/data/raids.dat, written via
//     NbtIo.writeCompressed (GZIP'd NBT). The root compound is { "data": <codec output>,
//     "DataVersion": <int> } (NbtUtils.addCurrentDataVersion). Load reads tag.get("data") back
//     through the codec. RAID_FILE_ID == Identifier "raids" (Raids.RAID_FILE_ID).
//   - net.minecraft.world.entity.raid.Raids.CODEC (RecordCodecBuilder): fields
//       "raids"   -> RaidWithId.CODEC.listOf().optionalFieldOf(..., List.of())
//       "next_id" -> Codec.INT
//       "tick"    -> Codec.INT
//     RaidWithId.CODEC = group( Codec.INT.fieldOf("id"), Raid.MAP_CODEC ) — so each list entry is a
//     compound carrying "id" PLUS the flat Raid fields (Raid.MAP_CODEC is inlined, not nested).
//   - net.minecraft.world.entity.raid.Raid.MAP_CODEC (RecordCodecBuilder.mapCodec): fields
//       "started"(BOOL) "active"(BOOL) "ticks_active"(LONG) "raid_omen_level"(INT)
//       "groups_spawned"(INT) "cooldown_ticks"(INT) "post_raid_ticks"(INT) "total_health"(FLOAT)
//       "group_count"(INT) "status"(RaidStatus.CODEC, a StringRepresentable enum: ongoing/victory/
//       loss/stopped) "center"(BlockPos.CODEC == Codec.INT_STREAM -> a TAG_Int_Array [x,y,z])
//       "heroes_of_the_village"(UUIDUtil.CODEC_SET == Codec.list(UUID) -> a TAG_List of
//       TAG_Int_Array, each UUID = 4 ints [msb-hi,msb-lo,lsb-hi,lsb-lo]).
//
// The Go side uses the fork's reflection-based nbt struct mapping (the same path player .dat uses).
// The struct field `nbt:` tags reproduce the vanilla key names byte-for-byte; [3]int32 encodes as
// TAG_Int_Array (BlockPos), [][4]int32 as a TAG_List of TAG_Int_Array (the UUID set).

// raidsDataVersion is SharedConstants.getCurrentVersion().dataVersion().version() for 26.2
// (VERIFIED net.minecraft.DetectedVersion: new DataVersion(4903, "main")). Written into the
// "DataVersion" wrapper tag exactly as NbtUtils.addCurrentDataVersion does.
const raidsDataVersion int32 = 4903

// savedDataDir is the per-dimension SavedData folder (SavedDataStorage.dataFolder == world/data).
// raids.dat + the future map_* / scoreboard SavedData live here.
const savedDataDir = "data"

// raidsFileName is Raids.RAID_FILE_ID (Identifier "raids") with the ".dat" suffix
// SavedDataStorage.getDataFile appends.
const raidsFileName = "raids.dat"

// raidWithIdDisk is one entry of the Raids "raids" list: RaidWithId.CODEC == { "id", <Raid.MAP_CODEC
// inlined> }. The Raid fields are FLAT in the same compound (MAP_CODEC, not a nested field), so this
// struct carries `id` alongside every Raid.MAP_CODEC field.
type raidWithIdDisk struct {
	ID int32 `nbt:"id"`

	// --- Raid.MAP_CODEC fields (inlined flat, exact key names/types) ---
	Started       bool     `nbt:"started"`         // Codec.BOOL
	Active        bool     `nbt:"active"`          // Codec.BOOL
	TicksActive   int64    `nbt:"ticks_active"`    // Codec.LONG
	RaidOmenLevel int32    `nbt:"raid_omen_level"` // Codec.INT
	GroupsSpawned int32    `nbt:"groups_spawned"`  // Codec.INT
	CooldownTicks int32    `nbt:"cooldown_ticks"`  // Codec.INT
	PostRaidTicks int32    `nbt:"post_raid_ticks"` // Codec.INT
	TotalHealth   float32  `nbt:"total_health"`    // Codec.FLOAT
	GroupCount    int32    `nbt:"group_count"`     // Codec.INT (== Raid.numGroups)
	Status        string   `nbt:"status"`          // RaidStatus.CODEC (StringRepresentable)
	Center        [3]int32 `nbt:"center"`          // BlockPos.CODEC (INT_STREAM -> TAG_Int_Array)

	// heroes_of_the_village: UUIDUtil.CODEC_SET -> TAG_List of TAG_Int_Array (each UUID = 4 ints).
	Heroes [][4]int32 `nbt:"heroes_of_the_village"`
}

// raidsDataDisk is the Raids.CODEC output (the inner "data" compound): the raids list + next_id + tick.
type raidsDataDisk struct {
	Raids  []raidWithIdDisk `nbt:"raids"`
	NextID int32            `nbt:"next_id"`
	Tick   int32            `nbt:"tick"`
}

// raidsRootDisk is the SavedDataStorage envelope (SavedDataStorage.encodeUnchecked): { "data",
// "DataVersion" }. NbtIo.writeCompressed gzips this compound to world/data/raids.dat.
type raidsRootDisk struct {
	Data        raidsDataDisk `nbt:"data"`
	DataVersion int32         `nbt:"DataVersion"`
}

// encodeRaidsData ports Raids.CODEC.encodeStart over a raidsManager: it walks the raid map (ordering
// is not observable — vanilla emits the list from an Int2ObjectMap entry stream into a HashSet on
// load), emitting one raidWithIdDisk per raid with the flat Raid.MAP_CODEC fields. Pure READ over the
// coordinator-owned manager; called on the owner goroutine, producing an IMMUTABLE value the off-tick
// IO writes.
func encodeRaidsData(rm *raidsManager) raidsDataDisk {
	list := make([]raidWithIdDisk, 0, len(rm.raidMap))
	for id, raid := range rm.raidMap {
		list = append(list, encodeRaid(int32(id), raid))
	}
	return raidsDataDisk{
		Raids:  list,
		NextID: int32(rm.nextId),
		Tick:   int32(rm.tick),
	}
}

// encodeRaid ports Raid.MAP_CODEC.encodeStart(raid) into the flat disk record (with its id). Every
// field is copied by value; the heroesOfTheVillage set becomes the [][4]int32 UUID-int-array list.
func encodeRaid(id int32, r *Raid) raidWithIdDisk {
	heroes := make([][4]int32, 0, len(r.heroesOfTheVillage))
	for _, u := range r.heroesOfTheVillage {
		heroes = append(heroes, uuidToIntArray(u))
	}
	return raidWithIdDisk{
		ID:            id,
		Started:       r.started,
		Active:        r.active,
		TicksActive:   r.ticksActive,
		RaidOmenLevel: int32(r.raidOmenLevel),
		GroupsSpawned: int32(r.groupsSpawned),
		CooldownTicks: int32(r.raidCooldownTicks),
		PostRaidTicks: int32(r.postRaidTicks),
		TotalHealth:   r.totalHealth,
		GroupCount:    int32(r.numGroups),
		Status:        r.status.name(),
		Center:        [3]int32{int32(r.centerX), int32(r.centerY), int32(r.centerZ)},
		Heroes:        heroes,
	}
}

// decodeRaidsData ports Raids::new (the CODEC apply): rebuild the raid map from the list, restoring
// nextId + tick. Each raidWithIdDisk becomes a *Raid via decodeRaid. Returns a fresh raidsManager
// (clean — a freshly-loaded SavedData is not dirty until mutated).
func decodeRaidsData(d raidsDataDisk) *raidsManager {
	rm := &raidsManager{raidMap: make(map[int]*Raid, len(d.Raids)), nextId: int(d.NextID), tick: int(d.Tick)}
	// vanilla's field initializer leaves nextId==1 when absent (private Raids(...) only overwrites it
	// when the codec supplies next_id). The codec always supplies it, but guard so a zero/negative
	// next_id from a legacy/corrupt file never breaks getUniqueId's ++.
	if rm.nextId < 1 {
		rm.nextId = 1
	}
	for _, rw := range d.Raids {
		rm.raidMap[int(rw.ID)] = decodeRaid(rw)
	}
	return rm
}

// decodeRaid ports Raid(RecordCodecBuilder apply): reconstruct a *Raid from the flat disk record. The
// difficulty/numGroups relation: vanilla stores group_count (== numGroups) directly and re-derives the
// bonus-spawn difficulty from the LIVE level at spawn time (raid_tick.go reads t.levelDifficulty per wave),
// so numGroups is restored verbatim from group_count and the difficulty passed to newRaid here is only a
// throwaway numGroups seed (immediately overwritten below) — decode is not on the tick loop, so the
// vanilla default is used. The boss-bar MODEL + RNG are non-persisted runtime state
// (vanilla's ServerBossEvent + Raid.random are rebuilt fresh on load — neither is in Raid.MAP_CODEC),
// so they are re-initialized exactly as newRaid does (deterministic per-id seed, RED/NOTCHED_10 bar).
func decodeRaid(rw raidWithIdDisk) *Raid {
	id := int(rw.ID)
	// Rebuild the runtime scaffolding (bar model + rng + group map) via newRaid, then overwrite the
	// persisted fields. newRaid seeds the rng from the id (the same salt the create seams use), so a
	// reloaded raid resumes on the identical RNG stream a freshly-created one of that id would.
	// difficulty here only seeds a numGroups that line below overwrites from group_count; decodeRaid has no
	// TickLoop, so the vanilla default (serverDifficulty == NORMAL) is used. The live spawn difficulty is
	// read per wave in raid_tick.go (t.levelDifficulty), never from this value.
	r := newRaid(id, int(rw.Center[0]), int(rw.Center[1]), int(rw.Center[2]), serverDifficulty, uint64(id)^raidCreateSeedSalt)
	r.numGroups = int(rw.GroupCount) // group_count IS numGroups (Raid.numGroups); restore it verbatim
	r.started = rw.Started
	r.active = rw.Active
	r.ticksActive = rw.TicksActive
	r.raidOmenLevel = int(rw.RaidOmenLevel)
	r.groupsSpawned = int(rw.GroupsSpawned)
	r.raidCooldownTicks = int(rw.CooldownTicks)
	r.postRaidTicks = int(rw.PostRaidTicks)
	r.totalHealth = rw.TotalHealth
	r.status = raidStatusFromName(rw.Status)
	r.heroesOfTheVillage = make([]uuid.UUID, 0, len(rw.Heroes))
	for _, ia := range rw.Heroes {
		r.heroesOfTheVillage = append(r.heroesOfTheVillage, uuidFromIntArray(ia))
	}
	return r
}

// raidStatusFromName ports Raid.RaidStatus.CODEC (StringRepresentable.fromEnum): the serialized name
// back to the enum. An unknown name defaults to ONGOING (the ctor default) — a corrupt status never
// crashes the load.
func raidStatusFromName(name string) raidStatus {
	switch name {
	case "victory":
		return raidStatusVictory
	case "loss":
		return raidStatusLoss
	case "stopped":
		return raidStatusStopped
	default: // "ongoing" or anything unexpected
		return raidStatusOngoing
	}
}

// uuidToIntArray ports net.minecraft.core.UUIDUtil.uuidToIntArray: split the two 64-bit halves into
// four 32-bit ints [msb>>32, msb, lsb>>32, lsb] (the order the codec streams). The result is the
// TAG_Int_Array the UUID codec writes.
func uuidToIntArray(u uuid.UUID) [4]int32 {
	msb := uint64(u[0])<<56 | uint64(u[1])<<48 | uint64(u[2])<<40 | uint64(u[3])<<32 |
		uint64(u[4])<<24 | uint64(u[5])<<16 | uint64(u[6])<<8 | uint64(u[7])
	lsb := uint64(u[8])<<56 | uint64(u[9])<<48 | uint64(u[10])<<40 | uint64(u[11])<<32 |
		uint64(u[12])<<24 | uint64(u[13])<<16 | uint64(u[14])<<8 | uint64(u[15])
	return [4]int32{
		int32(uint32(msb >> 32)),
		int32(uint32(msb)),
		int32(uint32(lsb >> 32)),
		int32(uint32(lsb)),
	}
}

// uuidFromIntArray ports UUIDUtil.uuidFromIntArray: reassemble the two 64-bit halves from the four
// ints and build the UUID. The inverse of uuidToIntArray.
func uuidFromIntArray(a [4]int32) uuid.UUID {
	msb := uint64(uint32(a[0]))<<32 | uint64(uint32(a[1]))
	lsb := uint64(uint32(a[2]))<<32 | uint64(uint32(a[3]))
	var u uuid.UUID
	for i := 0; i < 8; i++ {
		u[i] = byte(msb >> (56 - 8*i))
		u[8+i] = byte(lsb >> (56 - 8*i))
	}
	return u
}

// saveRaids writes the Raids SavedData to world/data/raids.dat (NbtIo.writeCompressed == gzip NBT of
// the { data, DataVersion } envelope). Runs OFF the tick over the immutable raidsDataDisk snapshot
// (encodeRaidsData is a pure owner-side read; only the value crosses the seam). The data dir is
// created on demand. The gzip trailer is flushed before the bytes are written so the .dat is a
// complete, re-readable stream (the SAME discipline savePlayer uses).
func saveRaids(dir string, data raidsDataDisk) error {
	dataDir := filepath.Join(dir, savedDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	root := raidsRootDisk{Data: data, DataVersion: raidsDataVersion}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := nbt.NewEncoder(gz).Encode(root, ""); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	path := filepath.Join(dataDir, raidsFileName)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// loadRaids reads world/data/raids.dat back into a *raidsManager. On a MISSING file it returns
// (nil, false, nil) — a first boot has no raids.dat and simply starts with no raids (the lazy
// ensureRaidsManager path). A real read/parse error is surfaced (corrupt .dat). A clean read returns
// (rm, true, nil). Mirrors SavedDataStorage.get: read tag, parse tag.get("data") through the codec.
func loadRaids(dir string) (*raidsManager, bool, error) {
	path := filepath.Join(dir, savedDataDir, raidsFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no raids.dat yet: no raids (first boot)
		}
		return nil, false, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, false, err // not a gzip stream: corrupt
	}
	defer gz.Close()

	var root raidsRootDisk
	if _, err := nbt.NewDecoder(gz).Decode(&root); err != nil {
		return nil, false, err // malformed NBT: corrupt
	}
	return decodeRaidsData(root.Data), true, nil
}
