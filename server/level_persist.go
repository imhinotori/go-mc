package server

// level_persist.go -- SUB-PERSIST for level.dat (the world-global save that save.Level /
// WriteLevel/ReadLevel define but that had ZERO callers). It writes world/level.dat on the periodic
// save flush + shutdown, and reads it back at boot, so the world seed, time, weather, spawn,
// gamerules and worldborder survive a restart. This is the vanilla LevelStorageSource.saveDataTag
// path: PrimaryLevelData.setTagData builds the Data compound and NbtIo.writeCompressed gzips it to
// level.dat; the boot load reads it back.
//
// CITED JAR (26.2-inner.jar):
//   - LevelStorageSource.LevelStorageAccess.saveDataTag: put("Data", worldData.createTag(...)) then
//     NbtIo.writeCompressed(root, level.dat).
//   - PrimaryLevelData.setTagData / createTag: the Data compound fields (Time, DayTime,
//     clearWeatherTime, rainTime/raining, thunderTime/thundering, SpawnX/Y/Z + SpawnAngle, GameRules,
//     WorldGenSettings seed, Border center/size/safeZone/damagePerBlock/warning*, Version, DataVersion).
//   - The world-global timers/flags mirror WeatherData; the border fields mirror WorldBorder.Settings.
//
// CONCURRENCY (TICK-05): encodeLevelData is a PURE owner-side read of the loop world-global state into
// an IMMUTABLE save.Level; the gzip+write runs synchronously in the save pass (level.dat is one tiny
// file, like raids.dat, so it needs no off-tick channel -- a cheap owner-side flush gated by cadence).
//
// encodeLevelData folds the single gametime counter into BOTH Time and DayTime (Sulfur derives the
// day-time as gametime % dayLength), so restoring Time on load recovers the sky phase exactly. The
// rainLevel/thunderLevel ramp fields are ServerLevel-transient (recomputed from the flags each tick)
// and are NOT persisted -- vanilla level.dat also stores only the timers + flags. The abilities/XP/
// spawn player state lives in the per-player .dat (player_persist_ext.go), not here.

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"

	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
)

const levelDataVersion int32 = 4903
const levelStorageVersion int32 = 19133
const levelDatName = "level.dat"
const persistLevelName = "sulfur"

func (t *TickLoop) encodeLevelData(spawnX, spawnY, spawnZ int32, spawnAngle float32) save.Level {
	var lvl save.Level
	d := &lvl.Data
	d.DataVersion = levelDataVersion
	d.StorageVersion = levelStorageVersion
	d.LevelName = persistLevelName
	d.Initialized = true
	d.Version.ID = levelDataVersion
	d.Version.Name = ProtocolName
	d.Time = t.gametime
	d.DayTime = t.gametime
	d.ClearWeatherTime = t.weather.clearWeatherTime
	d.RainTime = t.weather.rainTime
	d.Raining = t.weather.raining
	d.ThunderTime = t.weather.thunderTime
	d.Thundering = t.weather.thundering
	d.SpawnX, d.SpawnY, d.SpawnZ = spawnX, spawnY, spawnZ
	d.SpawnAngle = spawnAngle
	d.RandomSeed = t.worldSeed
	d.WorldGenSettings.Seed = t.worldSeed
	d.GameType = gameModeSurvival
	if t.gamerules == nil {
		t.gamerules = newGameRules()
	}
	d.GameRules = t.gamerules.toStringMap()
	b := t.worldBorder
	d.BorderCenterX = b.centerX
	d.BorderCenterZ = b.centerZ
	d.BorderSize = b.size
	d.BorderSafeZone = b.safeZone
	d.BorderDamagePerBlock = b.damagePerBlock
	d.BorderWarningBlocks = float64(b.warningBlocks)
	d.BorderWarningTime = float64(b.warningTime)
	d.BorderSizeLerpTarget = b.size
	d.BorderSizeLerpTime = 0
	return lvl
}

func (t *TickLoop) applyLevelData(lvl save.Level) {
	d := lvl.Data
	t.gametime = d.Time
	t.weather.clearWeatherTime = d.ClearWeatherTime
	t.weather.rainTime = d.RainTime
	t.weather.raining = d.Raining
	t.weather.thunderTime = d.ThunderTime
	t.weather.thundering = d.Thundering
	if d.Raining {
		t.weather.rainLevel = 1
		t.weather.oRainLevel = 1
	}
	if d.Thundering {
		t.weather.thunderLevel = 1
		t.weather.oThunderLevel = 1
	}
	if d.WorldGenSettings.Seed != 0 {
		t.worldSeed = d.WorldGenSettings.Seed
	} else if d.RandomSeed != 0 {
		t.worldSeed = d.RandomSeed
	}
	if len(d.GameRules) > 0 {
		if t.gamerules == nil {
			t.gamerules = newGameRules()
		}
		t.gamerules.applyStringMap(d.GameRules)
	}
	if d.BorderSize > 0 {
		b := &t.worldBorder
		b.centerX = d.BorderCenterX
		b.centerZ = d.BorderCenterZ
		b.size = d.BorderSize
		if d.BorderSafeZone != 0 {
			b.safeZone = d.BorderSafeZone
		}
		if d.BorderDamagePerBlock != 0 {
			b.damagePerBlock = d.BorderDamagePerBlock
		}
		b.warningBlocks = int(d.BorderWarningBlocks)
		b.warningTime = int(d.BorderWarningTime)
	}
}

func saveLevelDat(worldDir string, lvl save.Level) error {
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := save.WriteLevel(gz, lvl); err != nil {
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

func loadLevelDat(worldDir string) (save.Level, bool) {
	path := filepath.Join(worldDir, levelDatName)
	f, err := os.Open(path)
	if err != nil {
		return save.Level{}, false
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return save.Level{}, false
	}
	defer gz.Close()
	var lvl save.Level
	if _, err := nbt.NewDecoder(gz).Decode(&lvl); err != nil {
		return save.Level{}, false
	}
	return lvl, true
}
