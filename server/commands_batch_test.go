package server

// commands_batch_test.go -- behavior tests for the second operator-command wave (/clear /xp /weather
// /difficulty /enchant /fill) ported in commands_batch.go. Each drives the REAL command graph via
// loop.runCommand and asserts the resulting tick-owned state + the vanilla feedback ClientboundSystemChat.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestClearRemovesItems: /clear removes every matching item + reports the count.
func TestClearRemovesItems(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	inv := ensureInventory(p)
	inv.slots[36] = component.SlotData{ItemID: pk.VarInt(item.Stone.ID), Count: 10}
	inv.slots[37] = component.SlotData{ItemID: pk.VarInt(item.Dirt.ID), Count: 5}
	loop.runCommand(p, "clear Steve stone")
	if got := invCount(p, int(item.Stone.ID)); got != 0 {
		t.Fatalf("clear stone: %d stone remain, want 0", got)
	}
	if got := invCount(p, int(item.Dirt.ID)); got != 5 {
		t.Fatalf("clear stone: dirt count %d changed, want 5 (only stone cleared)", got)
	}
	if !containsSystemChat(drainPackets(p.client), "Removed 10 item(s) from player Steve") {
		t.Fatal("clear: missing 'Removed 10 item(s) from player Steve' feedback")
	}
}

// TestClearAllItems: /clear with no item removes everything.
func TestClearAllItems(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	inv := ensureInventory(p)
	inv.slots[36] = component.SlotData{ItemID: pk.VarInt(item.Stone.ID), Count: 3}
	inv.slots[37] = component.SlotData{ItemID: pk.VarInt(item.Dirt.ID), Count: 4}
	loop.runCommand(p, "clear")
	if invCount(p, int(item.Stone.ID)) != 0 || invCount(p, int(item.Dirt.ID)) != 0 {
		t.Fatal("clear (all): items remain, want empty inventory")
	}
}

// TestXpAddLevels: /xp add <self> 5 levels raises the level from 0 to 5 + feedback.
func TestXpAddLevels(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "xp add Steve 5 levels")
	if p.experienceLevel != 5 {
		t.Fatalf("xp add 5 levels: level %d, want 5", p.experienceLevel)
	}
	if !containsSystemChat(drainPackets(p.client), "Gave 5 experience levels to Steve") {
		t.Fatal("xp add levels: missing 'Gave 5 experience levels to Steve' feedback")
	}
}

// TestXpSetLevels: /xp set <self> 10 levels sets the level to exactly 10.
func TestXpSetLevels(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	p.experienceLevel = 3
	loop.runCommand(p, "xp set Steve 10 levels")
	if p.experienceLevel != 10 {
		t.Fatalf("xp set 10 levels: level %d, want 10", p.experienceLevel)
	}
}

// TestWeatherCmdRain: /weather rain 500 sets raining + rainTime 500 + clearWeatherTime 0.
func TestWeatherCmdRain(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "weather rain 500")
	if !loop.weather.raining {
		t.Fatal("weather rain: raining flag not set")
	}
	if loop.weather.rainTime != 500 {
		t.Fatalf("weather rain 500: rainTime %d, want 500", loop.weather.rainTime)
	}
	if loop.weather.clearWeatherTime != 0 {
		t.Fatalf("weather rain: clearWeatherTime %d, want 0", loop.weather.clearWeatherTime)
	}
	if !containsSystemChat(drainPackets(p.client), "Set the weather to rain") {
		t.Fatal("weather rain: missing 'Set the weather to rain' feedback")
	}
}

// TestWeatherCmdThunder: /weather thunder 300 sets raining + thundering + the shared weatherTime.
func TestWeatherCmdThunder(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "weather thunder 300")
	if !loop.weather.raining || !loop.weather.thundering {
		t.Fatalf("weather thunder: raining=%v thundering=%v, want both true", loop.weather.raining, loop.weather.thundering)
	}
	if loop.weather.rainTime != 300 || loop.weather.thunderTime != 300 {
		t.Fatalf("weather thunder 300: rainTime=%d thunderTime=%d, want both 300", loop.weather.rainTime, loop.weather.thunderTime)
	}
}

// TestDifficultyHard: /difficulty hard sets the level difficulty + feedback.
func TestDifficultyHard(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "difficulty hard")
	if loop.levelDifficulty != difficultyHard {
		t.Fatalf("difficulty hard: got %d, want %d", loop.levelDifficulty, difficultyHard)
	}
	if !containsSystemChat(drainPackets(p.client), "Set the difficulty to hard") {
		t.Fatal("difficulty hard: missing 'Set the difficulty to hard' feedback")
	}
}

// TestDifficultyAlreadySame: /difficulty normal when already NORMAL is rejected (the guard).
func TestDifficultyAlreadySame(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "difficulty normal")
	if !containsSystemChat(drainPackets(p.client), "already set to normal") {
		t.Fatal("difficulty normal (already same): missing the 'already set to normal' guard message")
	}
}

// TestEnchantCmdAppliesToHeld: /enchant applies sharpness to the held diamond sword.
func TestEnchantCmdAppliesToHeld(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{ItemID: pk.VarInt(idDiamondSword), Count: 1})
	loop.runCommand(p, "enchant Steve sharpness 3")
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if lvl := stackEnchantments(held)["minecraft:sharpness"]; lvl != 3 {
		t.Fatalf("enchant sharpness 3: held sword has sharpness %d, want 3", lvl)
	}
	if !containsSystemChat(drainPackets(p.client), "Applied enchantment sharpness to Steve item") {
		t.Fatal("enchant: missing 'Applied enchantment sharpness to Steve item' feedback")
	}
}

// TestEnchantCmdLevelTooHigh: /enchant sharpness 99 exceeds the max level and is rejected.
func TestEnchantCmdLevelTooHigh(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{ItemID: pk.VarInt(idDiamondSword), Count: 1})
	loop.runCommand(p, "enchant Steve sharpness 99")
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if _, ok := stackEnchantments(held)["minecraft:sharpness"]; ok {
		t.Fatal("enchant sharpness 99: the enchant was applied despite exceeding the max level")
	}
}

// TestFillFillsRegion: /fill fills a 3x1x3 region with stone (9 blocks).
func TestFillFillsRegion(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	p := opsPlayer(loop, 8.5, 64, 8.5)
	loop.withRegion(loop.only(), func() {
		loop.runCommand(p, "fill 0 64 0 2 64 2 stone")
	})
	stone := block.DefaultStateID["minecraft:stone"]
	got, _ := loop.world().GetBlock(pk.Position{X: 1, Y: 64, Z: 1}, dimMinY)
	if got != stone {
		t.Fatalf("fill stone: center block %d, want stone %d", got, stone)
	}
	if !containsSystemChat(drainPackets(p.client), "Successfully filled 9 block(s)") {
		t.Fatal("fill: missing 'Successfully filled 9 block(s)' feedback")
	}
}

// TestFillTooBig: a region exceeding fillMaxBlocks (32768) is rejected with the area-too-large message.
func TestFillTooBig(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	p := opsPlayer(loop, 8.5, 64, 8.5)
	loop.withRegion(loop.only(), func() {
		loop.runCommand(p, "fill 0 0 0 39 39 39 stone")
	})
	if !containsSystemChat(drainPackets(p.client), "Too many blocks in the specified area") {
		t.Fatal("fill too-big: missing the 'Too many blocks in the specified area' rejection")
	}
}
