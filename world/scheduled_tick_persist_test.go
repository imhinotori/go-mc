package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save"
)

// TestScheduledTickAndHeaderRoundTrip proves the SUB-BLOCKTICK persistence closure (audit #36 item
// 1): a chunk's pending block_ticks / fluid_ticks and the SerializableChunkData header fields
// (DataVersion / InhabitedTime / isLightOn / namespaced Status) survive SerializeChunkData ->
// save.Chunk.Load -> ChunkFromSave. A repeater mid-delay (block tick) or water mid-spread (fluid
// tick) that was lost on the old minimal shape now reloads.
func TestScheduledTickAndHeaderRoundTrip(t *testing.T) {
	gen := NewSuperflat(blockTestSecs, blockTestMinY, -1)
	pos := level.ChunkPos{1, -2}
	ch := gen.Generate(pos)
	ch.Status = level.StatusFull
	ch.InhabitedTime = 12345
	ch.IsLightOn = true

	blockTicks := []save.SavedTickNBT{
		{ID: "minecraft:repeater", X: 1<<4 + 3, Y: 70, Z: -2<<4 + 5, Delay: 2, Priority: 0},
		{ID: "minecraft:sugar_cane", X: 1<<4 + 7, Y: 71, Z: -2<<4 + 9, Delay: 16, Priority: 1},
	}
	fluidTicks := []save.SavedTickNBT{
		{ID: "minecraft:water", X: 1<<4 + 2, Y: 69, Z: -2<<4 + 2, Delay: 5, Priority: 0},
	}

	data, err := SerializeChunkData(nil, pos, ch, blockTestMinY, blockTicks, fluidTicks)
	if err != nil {
		t.Fatalf("SerializeChunkData: %v", err)
	}

	var sc save.Chunk
	if err := sc.Load(data); err != nil {
		t.Fatalf("save.Chunk.Load: %v", err)
	}

	// Header fields.
	if sc.DataVersion != 4903 {
		t.Errorf("DataVersion = %d, want 4903", sc.DataVersion)
	}
	if sc.InhabitedTime != 12345 {
		t.Errorf("InhabitedTime = %d, want 12345", sc.InhabitedTime)
	}
	if sc.IsLightOn != 1 {
		t.Errorf("isLightOn = %d, want 1", sc.IsLightOn)
	}
	if sc.Status != "minecraft:full" {
		t.Errorf("Status = %q, want %q (namespaced)", sc.Status, "minecraft:full")
	}

	// block_ticks / fluid_ticks decode back to the same lists.
	gotBlock, err := save.DecodeChunkTicks(sc.BlockTicks)
	if err != nil {
		t.Fatalf("DecodeChunkTicks(block): %v", err)
	}
	if len(gotBlock) != len(blockTicks) {
		t.Fatalf("block_ticks len = %d, want %d", len(gotBlock), len(blockTicks))
	}
	for i, want := range blockTicks {
		if gotBlock[i] != want {
			t.Errorf("block_ticks[%d] = %+v, want %+v", i, gotBlock[i], want)
		}
	}
	gotFluid, err := save.DecodeChunkTicks(sc.FluidTicks)
	if err != nil {
		t.Fatalf("DecodeChunkTicks(fluid): %v", err)
	}
	if len(gotFluid) != 1 || gotFluid[0] != fluidTicks[0] {
		t.Fatalf("fluid_ticks = %+v, want %+v", gotFluid, fluidTicks)
	}

	// ChunkFromSave surfaces the ticks as transient load-state + parses the namespaced status back.
	rel, err := level.ChunkFromSave(&sc)
	if err != nil {
		t.Fatalf("ChunkFromSave: %v", err)
	}
	if rel.Status != level.StatusFull {
		t.Errorf("reloaded Status = %q, want %q (namespace stripped)", rel.Status, level.StatusFull)
	}
	if rel.InhabitedTime != 12345 || !rel.IsLightOn {
		t.Errorf("reloaded header: InhabitedTime=%d IsLightOn=%v", rel.InhabitedTime, rel.IsLightOn)
	}
	if len(rel.SavedBlockTicks) != len(blockTicks) {
		t.Errorf("reloaded SavedBlockTicks len = %d, want %d", len(rel.SavedBlockTicks), len(blockTicks))
	}
	if len(rel.SavedFluidTicks) != 1 {
		t.Errorf("reloaded SavedFluidTicks len = %d, want 1", len(rel.SavedFluidTicks))
	}
}

// TestTickFreeChunkOmitsTickTags proves a chunk with NO pending ticks omits the block_ticks /
// fluid_ticks tags entirely (vanilla writes no list for a tick-free chunk), so the on-disk shape
// is unchanged for the common case (the pig's generated chunk).
func TestTickFreeChunkOmitsTickTags(t *testing.T) {
	gen := NewSuperflat(blockTestSecs, blockTestMinY, -1)
	pos := level.ChunkPos{0, 0}
	ch := gen.Generate(pos)

	data, err := SerializeChunkData(nil, pos, ch, blockTestMinY, nil, nil)
	if err != nil {
		t.Fatalf("SerializeChunkData: %v", err)
	}
	var sc save.Chunk
	if err := sc.Load(data); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b, _ := save.DecodeChunkTicks(sc.BlockTicks); len(b) != 0 {
		t.Errorf("tick-free chunk emitted block_ticks: %+v", b)
	}
	if f, _ := save.DecodeChunkTicks(sc.FluidTicks); len(f) != 0 {
		t.Errorf("tick-free chunk emitted fluid_ticks: %+v", f)
	}
}
