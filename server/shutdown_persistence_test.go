package server

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/world"
)

func TestShutdownDrainsFullPlayerSaveChannelAndSavesOnlinePlayers(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	t.Cleanup(loop.Close)
	loop.SetSaveSink()

	// Saturate the bounded channel, then prove the next snapshot is retained in owner-side overflow.
	for i := 0; i < leaveSnapshotBuffer; i++ {
		loop.enqueuePlayerSnapshot(playerLeaveSnapshot{
			uuid: uuid.New(),
			data: save.PlayerData{DataVersion: playerDataVersion, Health: float32(i + 1)},
		}, false)
	}
	overflowID := uuid.New()
	loop.enqueuePlayerSnapshot(playerLeaveSnapshot{
		uuid: overflowID,
		data: save.PlayerData{DataVersion: playerDataVersion, Health: 17},
	}, false)
	loop.enqueuePlayerSnapshot(playerLeaveSnapshot{
		uuid: overflowID,
		data: save.PlayerData{DataVersion: playerDataVersion, Health: 18},
	}, false)
	if got := len(loop.pendingPlayerSnapshots); got != 1 {
		t.Fatalf("pending overflow = %d, want 1 (full channel must not discard)", got)
	}

	onlineID := uuid.New()
	loop.players = append(loop.players, &tickPlayer{uuid: onlineID, health: 13, food: maxFood, saturation: defaultSaturation})

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	saveDone := make(chan struct{})
	tickDone := make(chan struct{})
	go func() { loop.RunSaveLoop(ctx, dir); close(saveDone) }()
	go func() { loop.Run(ctx, nil); close(tickDone) }()
	<-loop.Started()
	cancel()
	waitDone(t, tickDone, "tick shutdown")
	waitDone(t, saveDone, "player save drain")

	for id, wantHealth := range map[uuid.UUID]float32{overflowID: 18, onlineID: 13} {
		f, err := os.Open(filepath.Join(dir, "playerdata", id.String()+".dat"))
		if err != nil {
			t.Fatalf("open final player save %s: %v", id, err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			t.Fatalf("gzip final player save %s: %v", id, err)
		}
		data, err := save.ReadPlayerData(gz)
		gz.Close()
		f.Close()
		if err != nil {
			t.Fatalf("decode final player save %s: %v", id, err)
		}
		if data.Health != wantHealth {
			t.Fatalf("player %s health = %v, want %v", id, data.Health, wantHealth)
		}
	}
}

func TestShutdownFlushesEntitiesWithChunkSaverDisabled(t *testing.T) {
	loop, _ := newBlockLoop()
	t.Cleanup(loop.Close)
	dir := t.TempDir()
	loop.SetPersistDir(dir)
	loop.SetChunkSaver(world.NewChunkSaver("")) // default: entity storage on, .mca saves off
	pos := level.ChunkPos{0, 0}
	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, 64, 8.5)
	initSpawnHealth(pig)
	old, ok := entityToDisk(pig)
	if !ok {
		t.Fatal("entityToDisk rejected seed pig")
	}
	if err := saveEntities(dir, pos, []save.Entities{old}); err != nil {
		t.Fatalf("seed stale entity cell: %v", err)
	}
	// Keep the live store empty: the final pass must actively overwrite the stale disk cell.

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx, nil); close(done) }()
	<-loop.Started()
	cancel()
	waitDone(t, done, "entity-only final snapshot")

	recs, hit, err := loadEntities(dir, pos)
	if err != nil || !hit || len(recs) != 0 {
		t.Fatalf("final entity snapshot with chunks disabled: len=%d hit=%v err=%v", len(recs), hit, err)
	}
}

func TestPlayerSaveConsumerCancelWithoutTickProducerReturns(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	t.Cleanup(loop.Close)
	loop.SetSaveSink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.RunSaveLoop(ctx, t.TempDir()); close(done) }()
	cancel()
	waitDone(t, done, "standalone player save consumer")
}

func TestShutdownBeforePeriodicPassSavesLoadedChunk(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	t.Cleanup(loop.Close)
	dir := t.TempDir()
	regionDir := filepath.Join(dir, "region")
	gen := world.NewSuperflat(24, -64, -1)
	mgr := world.NewChunkManager()
	worker := world.NewWorker(gen, regionDir, 1)
	loop.SetWorld(mgr, worker)
	saver := world.NewChunkSaver(regionDir)
	loop.SetChunkSaver(saver)

	pos := level.ChunkPos{3, -2}
	ch := gen.Generate(pos)
	mgr.Insert(pos, ch)
	edit := pk.Position{X: int(pos[0])<<4 + 5, Y: 70, Z: int(pos[1])<<4 + 7}
	stone := block.ToStateID[block.Stone{}]
	if !mgr.SetBlock(edit, stone, -64) {
		t.Fatal("test edit did not change the loaded chunk")
	}
	if loop.chunkSaveTickCounter != 0 {
		t.Fatal("test must shut down before the 100-tick periodic save pass")
	}

	ctx, cancel := context.WithCancel(context.Background())
	saveDone := make(chan struct{})
	tickDone := make(chan struct{})
	go func() { saver.RunChunkSaveLoop(ctx, t.Logf); close(saveDone) }()
	go func() { loop.Run(ctx, nil); close(tickDone) }()
	<-loop.Started()
	cancel()
	waitDone(t, tickDone, "tick final chunk snapshot")
	waitDone(t, saveDone, "chunk IO drain")

	reloadWorker := world.NewWorker(gen, regionDir, 1)
	reloadCtx, reloadCancel := context.WithCancel(context.Background())
	defer reloadCancel()
	go reloadWorker.Run(reloadCtx)
	reloadWorker.Request(pos)
	var reloaded *level.Chunk
	select {
	case res := <-reloadWorker.Results():
		if res.Err != nil || res.Pos != pos {
			t.Fatalf("reload final chunk: pos=%v err=%v", res.Pos, res.Err)
		}
		reloaded = res.Chunk
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reloading final chunk")
	}
	if reloaded == nil {
		t.Fatal("reload final chunk returned nil")
	}
	sec := (edit.Y - (-64)) >> 4
	local := ((edit.Y-(-64))&15)<<8 | (edit.Z&15)<<4 | (edit.X & 15)
	if got := reloaded.Sections[sec].GetBlock(local); got != stone {
		t.Fatalf("reloaded shutdown edit = %d, want %d", got, stone)
	}
}

func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
