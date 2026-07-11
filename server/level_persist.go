package server

// level_persist.go -- SUB-PERSIST for level.dat (the world-global save). It writes world/level.dat on
// the periodic save flush + shutdown, and reads it back at boot. This is the vanilla
// LevelStorageSource.saveDataTag path: PrimaryLevelData.setTagData builds the Data compound and
// NbtIo.writeCompressed gzips it to level.dat; the boot load reads it back.
//
// 26.2 SCHEMA (VERIFIED via javap PrimaryLevelData.setTagData -- see save/level.go LevelData262 for
// the full key list): the level.dat root in 26.2 carries ServerBrands, WasModded, removed_features,
// Version, DataVersion, GameType, spawn (RespawnData), Time, LastPlayed, LevelName, version(19133),
// allowCommands, initialized, difficulty_settings, singleplayer_uuid, and the inlined
// WorldDataConfiguration (DataPacks/enabled_features). It does NOT carry DayTime, SpawnX/Y/Z,
// GameRules, WorldGenSettings, Border*, or the weather timers/flags -- those moved out of the level.dat
// root in 26.2 (they live in their own WorldData structures). encodeLevelData therefore writes ONLY the
// setTagData key set; weather/seed/gamerules/border are NOT round-tripped through level.dat.
//
// CITED JAR (26.2-inner.jar):
//   - LevelStorageSource.LevelStorageAccess.saveDataTag: put("Data", worldData.createTag(...)) then
//     NbtIo.writeCompressed(root, level.dat).
//   - PrimaryLevelData.setTagData / createTag / writeVersionTag: the Data compound key set.
//
// CONCURRENCY (TICK-05): encodeLevelData is a PURE owner-side read of the loop world-global state into
// an IMMUTABLE save.Level262; the gzip+write runs synchronously in the save pass (level.dat is one tiny
// file, like raids.dat, so it needs no off-tick channel -- a cheap owner-side flush gated by cadence).
//
// Time restores the gametime counter (Sulfur derives day-time as gametime % dayLength, so the sky phase
// recovers exactly). The abilities/XP/spawn player state lives in the per-player .dat
// (player_persist_ext.go), not here.

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"time"

	"github.com/imhinotori/sulfur/save"
)

const levelDataVersion int32 = 4903
const levelStorageVersion int32 = 19133
const levelDatName = "level.dat"
const persistLevelName = "sulfur"

// levelDataSeries is WorldVersion.dataVersion().series() -- the "main" series string writeVersionTag
// stamps into Version.Series for a release build.
const levelDataSeries = "main"

// persistServerBrand is the single known server brand written into ServerBrands (the vanilla server
// records its brand in knownServerBrands; Ender records its own).
const persistServerBrand = "Ender"

// difficultyFromName is the inverse of difficultyName (a corrupt/unknown name -> NORMAL, the
// WorldData default).
func difficultyFromName(name string) difficulty {
	switch name {
	case "peaceful":
		return difficultyPeaceful
	case "easy":
		return difficultyEasy
	case "hard":
		return difficultyHard
	default:
		return difficultyNormal
	}
}

// encodeLevelData builds the 26.2 PrimaryLevelData.setTagData Data compound from the loop's world-
// global state (TICK-05: a pure owner-side read into an IMMUTABLE save.Level262). It is the literal
// setTagData key set -- weather/time/gamerules/border/seed are NOT written here (they moved out of
// the level.dat root in 26.2; see save/level.go for the full moved-key list). Only Time (gameTime),
// the spawn RespawnData, GameType, difficulty_settings, Version, DataVersion and the metadata keys
// are level.dat root keys in 26.2.
func (t *TickLoop) encodeLevelData(spawnX, spawnY, spawnZ int32, spawnAngle float32) save.Level262 {
	var lvl save.Level262
	d := &lvl.Data
	d.ServerBrands = []string{persistServerBrand}
	d.WasModded = false
	d.Version = save.Version262{
		Name:     ProtocolName,
		ID:       levelDataVersion,
		Snapshot: false, // 26.2 is a stable release (WorldVersion.stable() == true -> Snapshot false)
		Series:   levelDataSeries,
	}
	d.DataVersion = levelDataVersion
	d.GameType = gameModeSurvival
	// spawn: LevelData$RespawnData. The overworld world-spawn (dimension/pos/yaw). pitch defaults 0.
	d.Spawn = save.RespawnData262{
		Dimension: overworldDimensionName,
		Pos:       [3]int32{spawnX, spawnY, spawnZ},
		Yaw:       spawnAngle,
		Pitch:     0,
	}
	d.Time = t.gametime
	d.LastPlayed = nowEpochMillis()
	d.LevelName = persistLevelName
	d.StorageVersion = levelStorageVersion
	d.AllowCommands = true
	d.Initialized = true
	d.Difficulty = save.DifficultySettings262{
		Difficulty: difficultyName(t.levelDifficulty),
		Hardcore:   false,
		Locked:     t.difficultyLocked,
	}
	// singleplayer_uuid: Ender is a dedicated server (no singleplayer owner), so setTagData's arg is
	// null and NO singleplayer_uuid key is written (SingleplayerUUID stays nil).
	d.SingleplayerUUID = nil
	return lvl
}

// nowEpochMillis is Util.getEpochMillis() (System wall clock), written into LastPlayed by setTagData.
func nowEpochMillis() int64 {
	return time.Now().UnixMilli()
}

// applyLevelData restores the loop world-global state from a loaded 26.2 level.dat. Only the keys
// setTagData actually writes are read back (Time -> gametime; difficulty_settings -> levelDifficulty
// + difficultyLocked). Weather/gamerules/border/seed are NOT in the 26.2 level.dat root, so they are
// not restored here -- they round-trip through their own structures (a faithful setTagData is the
// scope of this file).
func (t *TickLoop) applyLevelData(lvl save.Level262) {
	d := lvl.Data
	t.gametime = d.Time
	t.levelDifficulty = difficultyFromName(d.Difficulty.Difficulty)
	t.difficultyLocked = d.Difficulty.Locked
}

func saveLevelDat(worldDir string, lvl save.Level262) error {
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := save.WriteLevel262(gz, lvl); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	path := filepath.Join(worldDir, levelDatName)
	tmp := path + "_new"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadLevelDat(worldDir string) (save.Level262, bool) {
	path := filepath.Join(worldDir, levelDatName)
	f, err := os.Open(path)
	if err != nil {
		return save.Level262{}, false
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return save.Level262{}, false
	}
	defer gz.Close()
	lvl, err := save.ReadLevel262(gz)
	if err != nil {
		return save.Level262{}, false
	}
	return lvl, true
}
