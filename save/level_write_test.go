package save

import (
	"bytes"
	"compress/gzip"
	"testing"
)

// TestLevelRoundTrip proves WriteLevel→(gzip)→ReadLevel round-trips the level.dat fields SUB-PERSIST
// writes (time, spawn, seed, gamerules, level name), through the SAME { "Data": ... } root the
// vanilla LevelStorageSource.saveDataTag emits. ReadLevel uses DisallowUnknownFields, so this also
// proves WriteLevel emits ONLY fields the reader knows (no stray keys).
func TestLevelRoundTrip(t *testing.T) {
	var want Level
	want.Data.LevelName = "sulfur-test"
	want.Data.Time = 123456
	want.Data.DayTime = 6000
	want.Data.RandomSeed = 0x5EEDC0DE
	want.Data.SpawnX, want.Data.SpawnY, want.Data.SpawnZ = 8, 72, 8
	want.Data.SpawnAngle = 90
	want.Data.GameType = 0 // survival
	want.Data.Initialized = true
	want.Data.GameRules = map[string]string{
		"doDaylightCycle": "true",
		"keepInventory":   "false",
	}

	// WriteLevel writes RAW NBT; the caller gzips (NbtIo.writeCompressed). Round-trip through gzip
	// to exercise the full on-disk framing.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := WriteLevel(gz, want); err != nil {
		t.Fatalf("WriteLevel: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	got, err := ReadLevel(gr)
	if err != nil {
		t.Fatalf("ReadLevel: %v", err)
	}

	if got.Data.LevelName != want.Data.LevelName {
		t.Errorf("LevelName = %q, want %q", got.Data.LevelName, want.Data.LevelName)
	}
	if got.Data.Time != want.Data.Time || got.Data.DayTime != want.Data.DayTime {
		t.Errorf("time = (%d,%d), want (%d,%d)", got.Data.Time, got.Data.DayTime, want.Data.Time, want.Data.DayTime)
	}
	if got.Data.RandomSeed != want.Data.RandomSeed {
		t.Errorf("RandomSeed = %d, want %d", got.Data.RandomSeed, want.Data.RandomSeed)
	}
	if got.Data.SpawnX != 8 || got.Data.SpawnY != 72 || got.Data.SpawnZ != 8 {
		t.Errorf("spawn = (%d,%d,%d), want (8,72,8)", got.Data.SpawnX, got.Data.SpawnY, got.Data.SpawnZ)
	}
	if got.Data.GameRules["doDaylightCycle"] != "true" || got.Data.GameRules["keepInventory"] != "false" {
		t.Errorf("gamerules = %v", got.Data.GameRules)
	}
	if !got.Data.Initialized {
		t.Error("Initialized = false, want true")
	}
}

// TestLevel262RoundTrip proves WriteLevel262->(gzip)->ReadLevel262 round-trips the 26.2
// PrimaryLevelData.setTagData key set (Version, spawn RespawnData, GameType, Time, difficulty_settings,
// singleplayer_uuid absence). This is the schema Ender's world/level.dat actually uses.
func TestLevel262RoundTrip(t *testing.T) {
	var want Level262
	d := &want.Data
	d.ServerBrands = []string{"Ender"}
	d.WasModded = false
	d.Version = Version262{Name: "26.2", ID: 4903, Snapshot: false, Series: "main"}
	d.DataVersion = 4903
	d.GameType = 0
	d.Spawn = RespawnData262{Dimension: "minecraft:overworld", Pos: [3]int32{8, 72, 8}, Yaw: 45, Pitch: 0}
	d.Time = 987654
	d.LastPlayed = 1700000000000
	d.LevelName = "sulfur"
	d.StorageVersion = 19133
	d.AllowCommands = true
	d.Initialized = true
	d.Difficulty = DifficultySettings262{Difficulty: "hard", Hardcore: false, Locked: true}
	d.SingleplayerUUID = nil // dedicated server -> no key

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := WriteLevel262(gz, want); err != nil {
		t.Fatalf("WriteLevel262: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	got, err := ReadLevel262(gr)
	if err != nil {
		t.Fatalf("ReadLevel262: %v", err)
	}

	if got.Data.Version != want.Data.Version {
		t.Errorf("Version = %+v, want %+v", got.Data.Version, want.Data.Version)
	}
	if got.Data.Spawn != want.Data.Spawn {
		t.Errorf("spawn = %+v, want %+v", got.Data.Spawn, want.Data.Spawn)
	}
	if got.Data.Difficulty != want.Data.Difficulty {
		t.Errorf("difficulty_settings = %+v, want %+v", got.Data.Difficulty, want.Data.Difficulty)
	}
	if got.Data.Time != 987654 || got.Data.GameType != 0 || got.Data.StorageVersion != 19133 {
		t.Errorf("Time/GameType/version = (%d,%d,%d)", got.Data.Time, got.Data.GameType, got.Data.StorageVersion)
	}
	if got.Data.SingleplayerUUID != nil {
		t.Errorf("singleplayer_uuid = %v, want absent", got.Data.SingleplayerUUID)
	}
	if !got.Data.Initialized || !got.Data.AllowCommands {
		t.Errorf("initialized/allowCommands not restored")
	}
}
