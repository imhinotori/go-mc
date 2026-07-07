package server

// commands_ops_test.go -- behavior tests for the operator commands (/give /tp /time /effect
// /setblock /summon) ported in commands_ops.go. Each drives the REAL command graph via
// loop.runCommand (which installs the executor + all-operator resolver on the tick goroutine)
// and asserts the resulting tick-owned state + the vanilla feedback ClientboundSystemChat.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// opsPlayer registers a command player at a known position on the loop, named "Steve".
func opsPlayer(loop *TickLoop, x, y, z float64) *tickPlayer {
	p := commandPlayer(loop)
	p.name = "Steve"
	p.x, p.y, p.z = x, y, z
	p.prevX, p.prevY, p.prevZ = x, y, z
	return p
}

// invCount sums the count of the given item id across the player's inventory slots.
func invCount(p *tickPlayer, id int) int {
	if p.inventory == nil {
		return 0
	}
	n := 0
	for _, s := range p.inventory.slots {
		if int(s.ItemID) == id && s.Count > 0 {
			n += int(s.Count)
		}
	}
	return n
}

// TestGiveAddsItem: /give <self> stone 5 puts 5 stone in the inventory + the vanilla feedback.
func TestGiveAddsItem(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "give Steve stone 5")
	if got := invCount(p, int(item.Stone.ID)); got != 5 {
		t.Fatalf("give stone 5: inventory has %d stone, want 5", got)
	}
	if !containsSystemChat(drainPackets(p.client), "Gave 5 Stone to Steve") {
		t.Fatal("give: missing 'Gave 5 Stone to Steve' feedback")
	}
}

// TestGiveOverflowDrops: with every storage/hotbar slot full of a different item, /give stone 1
// cannot fit and drops the stack as an ItemEntity (GiveCommand's leftover -> player.drop path).
func TestGiveOverflowDrops(t *testing.T) {
	loop, _ := newPhysicsLoop()
	p := opsPlayer(loop, 8.5, 64, 8.5)
	inv := ensureInventory(p)
	// Fill the 36 storage window slots (main 9..35, hotbar 36..44) with full dirt stacks so a
	// stone stack has nowhere to go.
	for _, s := range storageWindowSlots() {
		inv.slots[s] = component.SlotData{ItemID: pk.VarInt(item.Dirt.ID), Count: 64}
	}
	beforeItems := 0
	loop.withRegion(loop.only(), func() {
		for _, e := range loop.only().entities.all() {
			if e.isItem {
				beforeItems++
			}
		}
		loop.runCommand(p, "give Steve stone 1")
	})
	afterItems := 0
	for _, e := range loop.only().entities.all() {
		if e.isItem {
			afterItems++
		}
	}
	if afterItems != beforeItems+1 {
		t.Fatalf("give overflow: item entities %d -> %d, want +1 dropped stack", beforeItems, afterItems)
	}
	if got := invCount(p, int(item.Stone.ID)); got != 0 {
		t.Fatalf("give overflow: inventory absorbed %d stone, want 0 (all dropped)", got)
	}
}

// TestTimeSetNight: /time set night snaps the day-phase (gametime % 24000) to 13000.
func TestTimeSetNight(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "time set night")
	if got := loop.gametime % dayLengthTicksTime; got != timeMarkerNight {
		t.Fatalf("time set night: daytime %d, want %d", got, timeMarkerNight)
	}
	if !containsSystemChat(drainPackets(p.client), "Set the time to 13000") {
		t.Fatal("time set night: missing 'Set the time to 13000' feedback")
	}
}

// TestTimeQueryGametime reports the raw gametime with the vanilla string.
func TestTimeQueryGametime(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.gametime = 4242
	loop.runCommand(p, "time query gametime")
	if !containsSystemChat(drainPackets(p.client), "The game time is 4242 tick(s)") {
		t.Fatal("time query gametime: missing 'The game time is 4242 tick(s)' feedback")
	}
}

// TestEffectGiveAppliesDuration: /effect give <self> poison 10 2 applies poison amp 2 for 10*20 ticks.
func TestEffectGiveAppliesDuration(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	p.health = 20
	loop.runCommand(p, "effect give Steve poison 10 2")
	eff := p.activeEffects[effectPoison]
	if eff == nil {
		t.Fatal("effect give poison: poison not present on the player")
	}
	if eff.duration != 200 { // 10s * 20 ticks
		t.Fatalf("effect give poison 10: duration %d ticks, want 200", eff.duration)
	}
	if eff.amplifier != 2 {
		t.Fatalf("effect give poison amp 2: amplifier %d, want 2", eff.amplifier)
	}
	if !containsSystemChat(drainPackets(p.client), "Applied effect minecraft:poison to Steve") {
		t.Fatal("effect give: missing 'Applied effect minecraft:poison to Steve' feedback")
	}
}

// TestEffectGiveDefaultDuration: no seconds arg -> the 600-tick (30s) default for a non-instant effect.
func TestEffectGiveDefaultDuration(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	p.health = 20
	loop.runCommand(p, "effect give Steve poison")
	eff := p.activeEffects[effectPoison]
	if eff == nil || eff.duration != effectDefaultDurationTicks {
		t.Fatalf("effect give poison (default): got %+v, want duration %d", eff, effectDefaultDurationTicks)
	}
}

// TestSetblockPlaces: /setblock <coords> stone places stone at the world position + feedback.
func TestSetblockPlaces(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	p := opsPlayer(loop, 8.5, 64, 8.5)
	loop.withRegion(loop.only(), func() {
		loop.runCommand(p, "setblock 8 64 8 stone")
	})
	stone := block.DefaultStateID["minecraft:stone"]
	got, _ := loop.world().GetBlock(pk.Position{X: 8, Y: 64, Z: 8}, dimMinY)
	if got != stone {
		t.Fatalf("setblock stone: state %d at (8,64,8), want stone %d", got, stone)
	}
	if !containsSystemChat(drainPackets(p.client), "Changed the block at 8, 64, 8") {
		t.Fatal("setblock: missing 'Changed the block at 8, 64, 8' feedback")
	}
}

// TestSetblockKeepGuard: /setblock ... keep only places on air; over a solid block it fails.
func TestSetblockKeepGuard(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	p := opsPlayer(loop, 8.5, 64, 8.5)
	stone := block.DefaultStateID["minecraft:stone"]
	dirt := block.DefaultStateID["minecraft:dirt"]
	loop.withRegion(loop.only(), func() {
		loop.world().SetBlock(pk.Position{X: 8, Y: 64, Z: 8}, stone, dimMinY)
		loop.runCommand(p, "setblock 8 64 8 dirt keep")
	})
	got, _ := loop.world().GetBlock(pk.Position{X: 8, Y: 64, Z: 8}, dimMinY)
	if got != stone {
		t.Fatalf("setblock keep over solid: state %d, want unchanged stone %d (keep must not overwrite)", got, stone)
	}
	_ = dirt
}

// TestTeleportToCoords: /tp <x> <y> <z> moves the issuer + the vanilla feedback.
func TestTeleportToCoords(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 0, 64, 0)
	loop.runCommand(p, "tp 100 70 -50")
	if p.x != 100 || p.y != 70 || p.z != -50 {
		t.Fatalf("tp 100 70 -50: player at (%.1f,%.1f,%.1f), want (100,70,-50)", p.x, p.y, p.z)
	}
	if !containsSystemChat(drainPackets(p.client), "Teleported Steve to 100, 70, -50") {
		t.Fatal("tp: missing 'Teleported Steve to 100, 70, -50' feedback")
	}
}

// TestTeleportRelative: /tp ~ ~10 ~ adds to the current position (WorldCoordinate relative).
func TestTeleportRelative(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := opsPlayer(loop, 5, 64, 5)
	loop.runCommand(p, "tp ~ ~10 ~")
	if p.x != 5 || p.y != 74 || p.z != 5 {
		t.Fatalf("tp ~ ~10 ~: player at (%.1f,%.1f,%.1f), want (5,74,5)", p.x, p.y, p.z)
	}
}

// TestSummonSpawnsEntity: /summon minecraft:pig spawns a pig entity + the vanilla feedback.
func TestSummonSpawnsEntity(t *testing.T) {
	loop, _ := newPhysicsLoop() // installs the vanilla_pig registry
	p := opsPlayer(loop, 8.5, 64, 8.5)
	before := 0
	loop.withRegion(loop.only(), func() {
		before = loop.only().entities.len()
		loop.runCommand(p, "summon minecraft:pig")
	})
	if after := loop.only().entities.len(); after != before+1 {
		t.Fatalf("summon pig: entity count %d -> %d, want +1", before, after)
	}
	if !containsSystemChat(drainPackets(p.client), "Summoned new minecraft:pig") {
		t.Fatal("summon: missing 'Summoned new minecraft:pig' feedback")
	}
}
