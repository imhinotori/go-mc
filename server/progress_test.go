package server

// progress_test.go - tests for the ADVANCEMENTS + STATISTICS port (stats.go /
// advancements.go). Covers: stat increment + the ClientboundAwardStats wire decode;
// an advancement criterion grant -> ClientboundUpdateAdvancements delta + progress;
// the login sync (the whole tree + reset flag); and the stats save/load roundtrip.

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestStatIncrementAndAwardStatsWire increments a couple of stats and asserts the
// ClientboundAwardStats packet decodes as VarInt count + per-entry (statType,
// value, count) triple, with the values matching.
func TestStatIncrementAndAwardStatsWire(t *testing.T) {
	c := newStatsCounter()
	c.incrementCustom("minecraft:mob_kills", 3)
	c.incrementCustom("minecraft:mob_kills", 2) // now 5
	c.increment(statKey{typeID: StatTypeKilled, valueID: int32(indexOf(registryid.EntityType, "minecraft:zombie"))}, 4)

	if got := c.getValue(statKey{typeID: StatTypeCustom, valueID: indexOf(registryid.CustomStat, "minecraft:mob_kills")}); got != 5 {
		t.Fatalf("mob_kills = %d, want 5", got)
	}

	p := encodeAwardStats(c)
	if p.ID != int32(packetid.ClientboundAwardStats) {
		t.Fatalf("AwardStats id = %d, want %d", p.ID, packetid.ClientboundAwardStats)
	}
	var count pk.VarInt
	// Decode the whole packet: count + count*(3 VarInts).
	fields := []pk.FieldDecoder{&count}
	// We must know count first; scan just the count, then re-scan everything.
	if err := p.Scan(&count); err != nil {
		t.Fatalf("AwardStats count did not decode: %v", err)
	}
	if int(count) != len(c.stats) {
		t.Fatalf("AwardStats count = %d, want %d", count, len(c.stats))
	}
	fields = fields[:1]
	got := make(map[statKey]int32)
	entries := make([]pk.VarInt, int(count)*3)
	for i := range entries {
		fields = append(fields, &entries[i])
	}
	if err := p.Scan(fields...); err != nil {
		t.Fatalf("AwardStats body did not decode: %v", err)
	}
	for i := 0; i < int(count); i++ {
		typeID := int32(entries[i*3])
		valueID := int32(entries[i*3+1])
		val := int32(entries[i*3+2])
		got[statKey{typeID: StatType(typeID), valueID: valueID}] = val
	}
	for k, v := range c.stats {
		if got[k] != v {
			t.Fatalf("wire stat %+v = %d, want %d", k, got[k], v)
		}
	}
}

// TestStatsSaveLoadRoundtrip writes a counter to world/stats/<uuid>.json and reads
// it back, asserting every entry survives the id<->name mapping.
func TestStatsSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()
	c := newStatsCounter()
	c.incrementCustom("minecraft:play_time", 12000)
	c.incrementCustom("minecraft:deaths", 2)
	c.increment(statKey{typeID: StatTypeMined, valueID: int32(indexOf(registryid.Block, "minecraft:stone"))}, 42)
	c.increment(statKey{typeID: StatTypePickedUp, valueID: int32(indexOf(registryid.Item, "minecraft:diamond"))}, 7)

	if err := saveStats(dir, id, c); err != nil {
		t.Fatalf("saveStats: %v", err)
	}
	if _, err := os.Stat(dir + "/stats/" + id.String() + ".json"); err != nil {
		t.Fatalf("stats file not written: %v", err)
	}
	back := loadStats(dir, id)
	if len(back.stats) != len(c.stats) {
		t.Fatalf("loaded %d stats, want %d", len(back.stats), len(c.stats))
	}
	for k, v := range c.stats {
		if back.stats[k] != v {
			t.Fatalf("loaded stat %+v = %d, want %d", k, back.stats[k], v)
		}
	}
	// A missing file yields an empty (non-nil) counter, never a crash.
	empty := loadStats(dir, uuid.New())
	if empty == nil || len(empty.stats) != 0 {
		t.Fatalf("missing stats file must load an empty counter")
	}
}

// TestAdvancementLoginSyncAndGrant asserts the login sync sends one
// ClientboundUpdateAdvancements with reset=true carrying the whole tree, and that
// a criterion grant sends a reset=false delta whose progress map contains the
// granted criterion.
func TestAdvancementLoginSyncAndGrant(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	if loop.advancements == nil || len(loop.advancements.order) == 0 {
		t.Fatal("advancement tree did not load")
	}
	// story/root must be present (a known root advancement).
	root, ok := loop.advancements.byID["minecraft:story/root"]
	if !ok {
		t.Fatal("minecraft:story/root missing from loaded tree")
	}
	if !root.IsRoot() {
		t.Fatal("story/root should be a root (no parent)")
	}

	p := combatPlayer(loop, 21)
	p.advancements = newPlayerAdvancements()

	// Login sync: one UpdateAdvancements, reset=true, added-count == tree size.
	loop.sendAdvancementsLogin(p)
	ps := drainPackets(p.client)
	if n := countID(ps, packetid.ClientboundUpdateAdvancements); n != 1 {
		t.Fatalf("login sync sent %d UpdateAdvancements, want 1", n)
	}
	var reset pk.Boolean
	var added pk.VarInt
	if err := ps[len(ps)-1].Scan(&reset, &added); err != nil {
		t.Fatalf("UpdateAdvancements did not decode reset+count: %v", err)
	}
	if !bool(reset) {
		t.Fatal("login sync UpdateAdvancements reset should be true")
	}
	if int(added) != len(loop.advancements.order) {
		t.Fatalf("login sync added-count = %d, want %d", added, len(loop.advancements.order))
	}

	// Grant story/root's crafting_table criterion -> a reset=false delta with progress.
	// Use a FRESH player: drainPackets above closed the first client's outbound queue.
	p2 := combatPlayer(loop, 23)
	p2.advancements = newPlayerAdvancements()
	loop.grantAdvancementCriterion(p2, "minecraft:story/root", "crafting_table")
	if !p2.advancements.isDone(root) {
		t.Fatal("story/root should be done after granting crafting_table (its only requirement)")
	}
	ps = drainPackets(p2.client)
	if n := countID(ps, packetid.ClientboundUpdateAdvancements); n != 1 {
		t.Fatalf("grant sent %d UpdateAdvancements, want 1 (delta)", n)
	}
	// Decode the delta: reset=false, added-count=1.
	var dReset pk.Boolean
	var dAdded pk.VarInt
	if err := ps[len(ps)-1].Scan(&dReset, &dAdded); err != nil {
		t.Fatalf("grant delta did not decode: %v", err)
	}
	if bool(dReset) {
		t.Fatal("grant delta reset should be false")
	}
	if int(dAdded) != 1 {
		t.Fatalf("grant delta added-count = %d, want 1", dAdded)
	}
	// A repeat grant on a THIRD fresh player already-granted is a no-op (no delta).
	p3 := combatPlayer(loop, 24)
	p3.advancements = newPlayerAdvancements()
	p3.advancements.grantCriterion("minecraft:story/root", "crafting_table") // pre-grant
	loop.grantAdvancementCriterion(p3, "minecraft:story/root", "crafting_table")
	ps2 := drainPackets(p3.client)
	if n := countID(ps2, packetid.ClientboundUpdateAdvancements); n != 0 {
		t.Fatalf("repeat grant sent %d UpdateAdvancements, want 0", n)
	}
}

// TestInventoryChangedTriggerGrantsRoot asserts the inventory_changed trigger feed
// grants story/root when the player picks up a crafting_table (the wired trigger).
func TestInventoryChangedTriggerGrantsRoot(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := combatPlayer(loop, 22)
	p.advancements = newPlayerAdvancements()

	loop.triggerInventoryChanged(p, "minecraft:crafting_table")
	if _, ok := p.advancements.progress["minecraft:story/root"]["crafting_table"]; !ok {
		t.Fatal("inventory_changed(crafting_table) should grant story/root crafting_table")
	}
	// A picked-up item that no advancement gates on grants nothing (no panic).
	loop.triggerInventoryChanged(p, "minecraft:bedrock")
}
