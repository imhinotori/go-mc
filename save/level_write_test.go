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
