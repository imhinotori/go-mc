package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/save"
)

func TestLoadPersistedSpawnReturnsInitializedZeroSpawn(t *testing.T) {
	worldDir := t.TempDir()
	want := save.RespawnData262{
		Dimension: "minecraft:overworld",
		Pos:       [3]int32{},
		Yaw:       90,
		Pitch:     -15,
	}
	writeLevelDat262(t, worldDir, true, want)

	got, ok := loadPersistedSpawn(worldDir)
	if !ok {
		t.Fatal("loadPersistedSpawn rejected initialized zero spawn")
	}
	if got != want {
		t.Fatalf("loadPersistedSpawn = %+v, want %+v", got, want)
	}
}

func TestLoadPersistedSpawnRejectsUninitializedZeroSpawn(t *testing.T) {
	worldDir := t.TempDir()
	writeLevelDat262(t, worldDir, false, save.RespawnData262{
		Dimension: "minecraft:overworld",
		Pos:       [3]int32{},
	})

	if got, ok := loadPersistedSpawn(worldDir); ok {
		t.Fatalf("loadPersistedSpawn = (%+v, true), want rejected", got)
	}
}

func TestLoadPersistedSpawnRejectsMissingAndCorruptData(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		if got, ok := loadPersistedSpawn(t.TempDir()); ok {
			t.Fatalf("loadPersistedSpawn = (%+v, true), want rejected", got)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		worldDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(worldDir, "level.dat"), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("write corrupt level.dat: %v", err)
		}
		if got, ok := loadPersistedSpawn(worldDir); ok {
			t.Fatalf("loadPersistedSpawn = (%+v, true), want rejected", got)
		}
	})
}

func writeLevelDat262(t *testing.T, worldDir string, initialized bool, spawn save.RespawnData262) {
	t.Helper()

	level := save.Level262{Data: save.LevelData262{
		ServerBrands:   []string{"Ender"},
		Version:        save.Version262{Name: "26.2", ID: 4903, Series: "main"},
		DataVersion:    4903,
		Spawn:          spawn,
		LevelName:      "sulfur-test",
		StorageVersion: 19133,
		Initialized:    initialized,
		Difficulty:     save.DifficultySettings262{Difficulty: "normal"},
	}}

	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	if err := save.WriteLevel262(gz, level); err != nil {
		t.Fatalf("save.WriteLevel262: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close level.dat gzip writer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "level.dat"), data.Bytes(), 0o600); err != nil {
		t.Fatalf("write level.dat: %v", err)
	}
}
