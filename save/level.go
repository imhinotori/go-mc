package save

import (
	"io"

	"github.com/imhinotori/sulfur/nbt"
)

type Level struct {
	Data LevelData
}

type LevelData struct {
	AllowCommands                byte `nbt:"allowCommands"`
	BorderCenterX, BorderCenterZ float64
	BorderDamagePerBlock         float64
	BorderSafeZone               float64
	BorderSize                   float64
	BorderSizeLerpTarget         float64
	BorderSizeLerpTime           int64
	BorderWarningBlocks          float64
	BorderWarningTime            float64
	ClearWeatherTime             int32 `nbt:"clearWeatherTime"`
	CustomBossEvents             map[string]CustomBossEvent
	DataPacks                    struct {
		Enabled, Disabled []string
	}
	DataVersion      int32
	DayTime          int64
	Difficulty       byte
	DifficultyLocked bool
	DimensionData    struct {
		TheEnd struct {
			DragonFight struct {
				Gateways         []int32
				DragonKilled     byte
				PreviouslyKilled byte
			}
		} `nbt:"1"`
	}
	DragonFight struct {
		Gateways           []int32
		DragonKilled       bool
		NeedsStateScanning bool
		PreviouslyKilled   bool
	}
	GameRules              map[string]string
	WorldGenSettings       WorldGenSettings
	GameType               int32
	HardCore               bool `nbt:"hardcore"`
	Initialized            bool `nbt:"initialized"`
	LastPlayed             int64
	LevelName              string
	MapFeatures            bool
	Player                 map[string]any
	Raining                bool  `nbt:"raining"`
	RainTime               int32 `nbt:"rainTime"`
	RandomSeed             int64
	ScheduledEvents        []nbt.RawMessage
	ServerBrands           []string
	SizeOnDisk             int64
	SpawnAngle             float32
	SpawnX, SpawnY, SpawnZ int32
	Thundering             bool  `nbt:"thundering"`
	ThunderTime            int32 `nbt:"thunderTime"`
	Time                   int64
	Version                struct {
		ID       int32 `nbt:"Id"`
		Name     string
		Series   string
		Snapshot byte
	}
	StorageVersion             int32 `nbt:"version"`
	WanderingTraderId          []int32
	WanderingTraderSpawnChance int32
	WanderingTraderSpawnDelay  int32
	WasModded                  bool
}

type CustomBossEvent struct {
	Players        [][]int32
	Color          string
	CreateWorldFog bool
	DarkenScreen   bool
	Max            int32
	Value          int32
	Name           string
	Overlay        string
	PlayBossMusic  bool
	Visible        bool
}

func ReadLevel(r io.Reader) (data Level, err error) {
	decoder := nbt.NewDecoder(r)
	decoder.DisallowUnknownFields()
	_, err = decoder.Decode(&data)
	return
}

// ============================================================================================
// 26.2 level.dat schema (PrimaryLevelData.setTagData) -- the ONLY schema Ender writes.
//
// The legacy Level / LevelData above is retained for reading a pre-26 vanilla level.dat (the
// testdata reader), but Ender's own world/level.dat is written by the setTagData key set below.
// VERIFIED method-for-method against temp/cache/26.2-inner.jar via `javap -c -p`:
//
//	net.minecraft.world.level.storage.PrimaryLevelData.setTagData(CompoundTag, UUID) writes EXACTLY:
//	  ServerBrands            ListTag<String>   (stringCollectionToTag(knownServerBrands))
//	  WasModded               boolean           (putBoolean)
//	  removed_features        ListTag<String>   (ONLY when removedFeatureFlags is non-empty)
//	  Version                 compound          (writeVersionTag: Name/Id/Snapshot/Series)
//	  DataVersion             int               (NbtUtils.addCurrentDataVersion)
//	  GameType                int               (settings.gameType().getId())
//	  spawn                   compound          (LevelData$RespawnData.CODEC: GlobalPos inlined + yaw + pitch)
//	  Time                    long              (gameTime)
//	  LastPlayed              long              (Util.getEpochMillis)
//	  LevelName               String            (settings.levelName())
//	  version                 int               (19133 -- the storage/anvil version)
//	  allowCommands           boolean           (settings.allowCommands())
//	  initialized             boolean
//	  difficulty_settings     compound          (LevelSettings$DifficultySettings.CODEC: difficulty/hardcore/locked)
//	  singleplayer_uuid       int[4]            (ONLY when the uuid is non-null; storeNullable)
//	  <inlined at root>       DataPacks/enabled_features  (WorldDataConfiguration.MAP_CODEC.store -- no wrapper key)
//
// KEYS THAT MOVED / DO NOT EXIST AT ROOT IN 26.2 (they were in the pre-26 legacy Data compound):
//	DayTime, SpawnX/SpawnY/SpawnZ, SpawnAngle           -> spawn now lives in the RespawnData `spawn` compound
//	GameRules                                           -> WorldData gamerules (not level.dat root)
//	WorldGenSettings/RandomSeed                         -> world_gen_settings (separate; not setTagData)
//	Border* (center/size/safeZone/damage/warning*)      -> WorldBorder.Settings (not level.dat root)
//	raining/rainTime/thundering/thunderTime/clearWeatherTime -> WeatherData (level structures, not root)
//	Difficulty(byte)/DifficultyLocked/hardcore(root)    -> difficulty_settings compound
// ============================================================================================

// Level262 is the 26.2 level.dat root: a single "Data" compound (LevelStorageSource.saveDataTag
// does put("Data", worldData.createTag())). CreateTag builds a fresh CompoundTag and calls
// setTagData on it, so Data == the LevelData262 key set above.
type Level262 struct {
	Data LevelData262
}

// LevelData262 is the exact PrimaryLevelData.setTagData key set (26.2). Field order in the struct is
// irrelevant to NBT (a compound is unordered); the `nbt:` tags fix the on-disk key names 1:1.
//
// SingleplayerUUID is a pointer so `storeNullable` fidelity is preserved: a nil pointer writes NO
// singleplayer_uuid key (as setTagData skips it when the uuid arg is null), and a present value
// writes the int[4] UUIDUtil.CODEC form. removed_features is `omitempty` so an empty set writes no
// key (setTagData guards `if (!removedFeatureFlags.isEmpty())`).
type LevelData262 struct {
	ServerBrands     []string              `nbt:"ServerBrands"`
	WasModded        bool                  `nbt:"WasModded"`
	RemovedFeatures  []string              `nbt:"removed_features,omitempty"`
	Version          Version262            `nbt:"Version"`
	DataVersion      int32                 `nbt:"DataVersion"`
	GameType         int32                 `nbt:"GameType"`
	Spawn            RespawnData262        `nbt:"spawn"`
	Time             int64                 `nbt:"Time"`
	LastPlayed       int64                 `nbt:"LastPlayed"`
	LevelName        string                `nbt:"LevelName"`
	StorageVersion   int32                 `nbt:"version"`
	AllowCommands    bool                  `nbt:"allowCommands"`
	Initialized      bool                  `nbt:"initialized"`
	Difficulty       DifficultySettings262 `nbt:"difficulty_settings"`
	SingleplayerUUID *[4]int32             `nbt:"singleplayer_uuid,omitempty"`

	// DataPacks / EnabledFeatures are WorldDataConfiguration.MAP_CODEC inlined at the Data root (the
	// store(MapCodec, value) overload writes the map's keys directly, no wrapper). MAP_CODEC uses
	// lenientOptionalFieldOf with empty defaults, so an absent value reads clean.
	DataPacks       *DataPacks262 `nbt:"DataPacks,omitempty"`
	EnabledFeatures []string      `nbt:"enabled_features,omitempty"`
}

// Version262 is PrimaryLevelData.writeVersionTag's compound: Name (String), Id (int), Snapshot
// (boolean), Series (String). Keys VERIFIED via javap.
type Version262 struct {
	Name     string `nbt:"Name"`
	ID       int32  `nbt:"Id"`
	Snapshot bool   `nbt:"Snapshot"`
	Series   string `nbt:"Series"`
}

// RespawnData262 is LevelData$RespawnData.MAP_CODEC: GlobalPos.MAP_CODEC (dimension + pos) inlined,
// then yaw (float) and pitch (float). GlobalPos.MAP_CODEC keys: dimension (ResourceKey string), pos
// (BlockPos.CODEC == int[3] [x,y,z]). VERIFIED via javap GlobalPos / RespawnData static initializers.
type RespawnData262 struct {
	Dimension string   `nbt:"dimension"`
	Pos       [3]int32 `nbt:"pos"`
	Yaw       float32  `nbt:"yaw"`
	Pitch     float32  `nbt:"pitch"`
}

// DifficultySettings262 is LevelSettings$DifficultySettings.CODEC: difficulty (Difficulty enum
// serialized name), hardcore (boolean), locked (boolean). VERIFIED via javap.
type DifficultySettings262 struct {
	Difficulty string `nbt:"difficulty"`
	Hardcore   bool   `nbt:"hardcore"`
	Locked     bool   `nbt:"locked"`
}

// DataPacks262 is WorldDataConfiguration's DataPacks sub-compound (Enabled/Disabled lists), the same
// shape the legacy DataPacks carried.
type DataPacks262 struct {
	Enabled  []string `nbt:"Enabled"`
	Disabled []string `nbt:"Disabled"`
}

// WriteLevel262 writes the 26.2 level.dat NBT to w as the vanilla root compound { "Data": <LevelData262> }
// (LevelStorageSource.saveDataTag: put("Data", createTag())). The caller wraps w in gzip
// (NbtIo.writeCompressed). RAW NBT here so the gzip framing lives in one place.
func WriteLevel262(w io.Writer, data Level262) error {
	return nbt.NewEncoder(w).Encode(data, "")
}

// ReadLevel262 reads the 26.2 level.dat back. It does NOT DisallowUnknownFields (a real vanilla
// level.dat carries world_gen_settings and other sibling keys under Data that Ender does not model;
// tolerating them keeps a vanilla-written world loadable).
func ReadLevel262(r io.Reader) (data Level262, err error) {
	_, err = nbt.NewDecoder(r).Decode(&data)
	return
}

// WriteLevel writes the level.dat NBT to w as the vanilla root compound { "Data": <LevelData> }
// (SUB-PERSIST). It is the inverse of ReadLevel: the Level struct's single "Data" field gives the
// `Data` wrapper LevelStorageSource.saveDataTag emits (CompoundTag.put("Data", worldData.createTag)),
// and the encoder writes the unnamed root compound the same NbtIo.writeCompressed reads back.
//
// The CALLER wraps w in gzip (NbtIo.writeCompressed is gzip) — WriteLevel writes RAW NBT so it is
// reusable for the (rare) uncompressed level.dat and so the gzip framing lives in one place
// (SaveLevel). CITE: LevelStorageSource$LevelStorageAccess.saveDataTag — put("Data", tag) then
// saveLevelData -> NbtIo.writeCompressed.
func WriteLevel(w io.Writer, data Level) error {
	return nbt.NewEncoder(w).Encode(data, "")
}
