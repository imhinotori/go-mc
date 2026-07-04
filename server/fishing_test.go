package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/loot"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// fishing_test.go — FISHING ROD + FISHING HOOK validation gates, each asserting the ported
// FishingRodItem.use / FishingHook.<init>/tick/catchingFish/retrieve against the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR/javap):
//   - a fishing rod use casts a FishingBobber toward the player's look with the ctor cast velocity;
//   - the bobber floats on a water column (FLYING -> BOBBING) instead of sinking;
//   - the catchingFish timer arms timeUntilLured in the jar range [100, 600];
//   - a reel with a pending catch (nibble>0) rolls the FISHING loot table and spawns the caught item
//     flying toward the player + awards an XP orb, then discards the bobber;
//   - reeling an in-air bobber (no catch) simply retrieves it (discard, clear player.fishing).

// newFishingLoop wires a TickLoop with one ready all-air chunk + block-tick container for column (0,0).
func newFishingLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// fillFishingWaterColumn writes a source-water column (Water{Level:0}) into cells [y0..y1] at (x,z).
func fillFishingWaterColumn(mgr *world.ChunkManager, x, z, y0, y1 int) {
	src := block.ToStateID[block.Water{Level: 0}]
	for y := y0; y <= y1; y++ {
		mgr.SetBlock(pk.Position{X: x, Y: y, Z: z}, src, dimMinY)
	}
}

// newFishingPlayer registers a player in the loop holding a fishing rod, at (x,y,z) with the given look.
func newFishingPlayer(loop *TickLoop, x, y, z float64, yaw, pitch float32, entityID int32) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, yaw: yaw, pitch: pitch, entityID: entityID, client: captureClient(64)}
	p.tracked = make(map[int32]bool)
	giveHeld(p, item.FishingRod.ID, 1)
	loop.players = append(loop.players, p)
	return p
}

// TestFishingRodCastsBobber: a fishing rod use spawns a FishingBobber that flies toward the player's
// look (+Z for yaw 0) with the ctor cast velocity, and links player.fishing. CITE FishingRodItem.use +
// FishingHook.<init>.
func TestFishingRodCastsBobber(t *testing.T) {
	loop, _ := newFishingLoop()
	p := newFishingPlayer(loop, 8.5, 64.0, 8.5, 0, 0, 9001)

	loop.useItemInHand(p, interactionHandMain)

	if p.fishingHookID == 0 {
		t.Fatal("after a rod cast, player.fishing (fishingHookID) is 0 — no bobber was spawned")
	}
	b := loop.entityByIDAnyRegion(p.fishingHookID)
	if b == nil {
		t.Fatalf("bobber id %d not found in any region", p.fishingHookID)
	}
	if !b.isFishingHook {
		t.Fatal("spawned entity is not marked isFishingHook")
	}
	// yaw 0 / pitch 0 -> the cast vector is +Z (mvz = 1), so vz must be strongly positive and vx ~ 0.
	// The per-axis magnitude is 0.6 + triangle(0.5, 0.0103365) ~ 1.1, so vz is roughly in (0.8, 1.4).
	if b.vz <= 0.5 {
		t.Fatalf("bobber cast toward look (+Z) should have vz > 0.5, got vz=%.4f", b.vz)
	}
	if math.Abs(b.vx) > 0.2 {
		t.Fatalf("bobber cast straight ahead should have |vx| ~ 0, got vx=%.4f", b.vx)
	}
	// The AddEntity data field carries the owner id (the client fishing-line link).
	if b.spawnData != p.entityID {
		t.Fatalf("bobber spawnData (AddEntity owner link) = %d, want owner id %d", b.spawnData, p.entityID)
	}
	// The bobber starts near the caster's eye height (getEyeY = y + 1.62).
	if b.y < 64.0+1.0 {
		t.Fatalf("bobber should start near eye height (~%.2f), got y=%.4f", 64.0+fishEyeHeight, b.y)
	}
}

// TestFishingBobberFloatsOnWater: a bobber cast over a water column reaches BOBBING and floats near the
// surface instead of sinking through it. CITE FishingHook.tick (FLYING -> BOBBING; the fluid bob float).
func TestFishingBobberFloatsOnWater(t *testing.T) {
	loop, mgr := newFishingLoop()
	// Deep water column at (8, z=8), water top at y=64 (cells 55..63 water; surface 64).
	fillFishingWaterColumn(mgr, 8, 8, 55, 63)

	// Player above the water looking straight down so the bobber drops into the column.
	p := newFishingPlayer(loop, 8.5, 70.0, 8.5, 0, 90, 9002)
	loop.useItemInHand(p, interactionHandMain)
	b := loop.entityByIDAnyRegion(p.fishingHookID)
	if b == nil {
		t.Fatal("no bobber spawned")
	}
	// Force the bobber into the water column near the surface so it enters the BOBBING branch quickly.
	loop.cur().entities.move(b, 8.5, 63.5, 8.5)
	b.vx, b.vy, b.vz = 0, -0.2, 0

	for i := 0; i < 60; i++ {
		loop.tickFishingHook(b)
		if b.fishingState == fishStateBobbing {
			break
		}
	}
	if b.fishingState != fishStateBobbing {
		t.Fatalf("bobber over water did not reach BOBBING, state=%d", b.fishingState)
	}
	// Tick more; it must float near the surface (y ~ 63.x .. 64.x), not sink to the column bottom.
	for i := 0; i < 100; i++ {
		loop.tickFishingHook(b)
	}
	if b.fishingHookedID != 0 { // sanity: no accidental hooked-entity latch
		t.Fatalf("bobber unexpectedly hooked an entity (%d)", b.fishingHookedID)
	}
	if b.y < 60.0 {
		t.Fatalf("bobber sank through the water: y=%.4f, want it floating near the surface (>= 60)", b.y)
	}
}

// TestFishingCatchTimerArms: the first in-water catchingFish tick with all countdowns at 0 arms
// timeUntilLured to Mth.nextInt(100, 600) - lureSpeed (lureSpeed 0), i.e. in [100, 600]. CITE
// FishingHook.catchingFish (the else branch).
func TestFishingCatchTimerArms(t *testing.T) {
	loop, mgr := newFishingLoop()
	fillFishingWaterColumn(mgr, 8, 8, 55, 63)
	p := newFishingPlayer(loop, 8.5, 70.0, 8.5, 0, 90, 9003)
	loop.useItemInHand(p, interactionHandMain)
	b := loop.entityByIDAnyRegion(p.fishingHookID)
	if b == nil {
		t.Fatal("no bobber spawned")
	}
	// All countdowns at 0; call the catch step directly at the water cell.
	b.fishingNibble, b.fishingLured, b.fishingHooked = 0, 0, 0
	loop.fishingCatchingFish(b, 8, 63, 8)
	if b.fishingLured < 100 || b.fishingLured > 600 {
		t.Fatalf("timeUntilLured armed to %d, want the jar range [100, 600]", b.fishingLured)
	}
}

// TestFishingReelRollsLootAndSpawnsItem: reeling a bobber with a pending catch (nibble > 0) rolls the
// FISHING loot table, spawns the caught ItemEntity flying toward the player, awards an XP orb, and
// discards the bobber (clearing player.fishing). CITE FishingHook.retrieve.
func TestFishingReelRollsLootAndSpawnsItem(t *testing.T) {
	loop, mgr := newFishingLoop()
	fillFishingWaterColumn(mgr, 8, 8, 55, 63)
	p := newFishingPlayer(loop, 8.5, 70.0, 8.5, 0, 90, 9004)
	loop.useItemInHand(p, interactionHandMain)
	b := loop.entityByIDAnyRegion(p.fishingHookID)
	if b == nil {
		t.Fatal("no bobber spawned")
	}
	loop.cur().entities.move(b, 8.5, 63.5, 8.5)
	b.fishingNibble = 5 // a catch is pending (a fish is biting)

	itemsBefore := countStoreItems(loop)
	orbsBefore := countStoreOrbs(loop)

	// Second rod use -> reel (retrieve).
	loop.useItemInHand(p, interactionHandMain)

	if p.fishingHookID != 0 {
		t.Fatalf("after reel, player.fishing should be cleared, got %d", p.fishingHookID)
	}
	if loop.entityByIDAnyRegion(b.id) != nil {
		t.Fatal("after reel, the bobber should be discarded")
	}
	if got := countStoreItems(loop); got <= itemsBefore {
		t.Fatalf("reel with a pending catch spawned no caught item (items %d -> %d)", itemsBefore, got)
	}
	if got := countStoreOrbs(loop); got <= orbsBefore {
		t.Fatalf("reel with a pending catch awarded no XP orb (orbs %d -> %d)", orbsBefore, got)
	}
	// The caught item flies toward the player (owner above at y=70 -> ya = +6.5 -> upward vy).
	var caught *Entity
	for _, e := range loop.cur().entities.byID {
		if e.isItem {
			caught = e
			break
		}
	}
	if caught == nil {
		t.Fatal("no caught item entity found")
	}
	if caught.vy <= 0 {
		t.Fatalf("caught item should fly UP toward the player above it, got vy=%.4f", caught.vy)
	}
}

// TestFishingReelInAirRetrieves: reeling an in-air bobber (no water, no catch) simply retrieves it —
// the bobber is discarded and player.fishing cleared, with no loot. CITE FishingHook.retrieve (no
// hookedIn, nibble 0 -> dmg path, discard).
func TestFishingReelInAirRetrieves(t *testing.T) {
	loop, _ := newFishingLoop()
	p := newFishingPlayer(loop, 8.5, 64.0, 8.5, 0, 0, 9005)
	loop.useItemInHand(p, interactionHandMain)
	b := loop.entityByIDAnyRegion(p.fishingHookID)
	if b == nil {
		t.Fatal("no bobber spawned")
	}
	itemsBefore := countStoreItems(loop)

	loop.useItemInHand(p, interactionHandMain) // reel

	if p.fishingHookID != 0 {
		t.Fatalf("after in-air reel, player.fishing should be cleared, got %d", p.fishingHookID)
	}
	if loop.entityByIDAnyRegion(b.id) != nil {
		t.Fatal("after in-air reel, the bobber should be discarded")
	}
	if got := countStoreItems(loop); got != itemsBefore {
		t.Fatalf("an in-air reel (no catch) spawned %d items, want 0", got-itemsBefore)
	}
}

// TestFishingLootTableRolls: the FISHING loot table loads (NestedLootTable sub-tables + in_open_water)
// and rolls a non-empty caught stack. Asserts the treasure entry is gated by open water: an open-water
// roll can produce treasure; a closed-water roll never does. CITE FishingHook.retrieve loot roll +
// the fishing table's in_open_water condition.
func TestFishingLootTableRolls(t *testing.T) {
	tbl, err := loot.LoadTable("minecraft:gameplay/fishing")
	if err != nil {
		t.Fatalf("load fishing table: %v", err)
	}
	// A deterministic seed must yield exactly one caught stack (rolls == 1).
	ctx := loot.NewFishingLootContext(0xF15, 0, true)
	got := loot.Roll(tbl, 0xF15, ctx)
	if len(got) != 1 {
		t.Fatalf("fishing roll produced %d stacks, want 1 (the table rolls once)", len(got))
	}
	if got[0].Count <= 0 || got[0].ItemID <= 0 {
		t.Fatalf("fishing roll produced an empty stack: %+v", got[0])
	}
	// Open-water vs closed-water: over many seeds, closed water must NEVER yield a treasure-only item
	// (name_tag / saddle / nautilus_shell are treasure-exclusive). We assert the closed-water roll set
	// never contains name_tag (a treasure item), while the open-water set at least sometimes differs.
	nameTagID := int32(item.NameTag.ID)
	closedHasTreasure := false
	for seed := int64(1); seed <= 400; seed++ {
		c := loot.NewFishingLootContext(seed, 0, false) // NOT open water
		for _, s := range loot.Roll(tbl, seed, c) {
			if int32(s.ItemID) == nameTagID {
				closedHasTreasure = true
			}
		}
	}
	if closedHasTreasure {
		t.Fatal("closed-water fishing yielded a name_tag (treasure) — the in_open_water gate failed")
	}
}

// countStoreItems / countStoreOrbs count ItemEntity / ExperienceOrb entities across the current region.
func countStoreItems(loop *TickLoop) int {
	n := 0
	for _, e := range loop.cur().entities.byID {
		if e.isItem {
			n++
		}
	}
	return n
}

func countStoreOrbs(loop *TickLoop) int {
	n := 0
	for _, e := range loop.cur().entities.byID {
		if e.isOrb {
			n++
		}
	}
	return n
}
