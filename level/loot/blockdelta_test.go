package loot

// blockdelta_test.go — RED-then-GREEN tests for the block-table delta (20-02 Task 1):
// the `alternatives` entry (first-passing child wins) + the four block-only
// functions/conditions (match_tool, survives_explosion, apply_bonus,
// explosion_decay). These are the elements the blocks/*.json tables need that the
// chest groups never use. The faithful no-tool / no-explosion defaults (decompiled
// this session) are the v1 block-break behavior: a hand break with NO tool/explosion
// context drops the non-silk-touch alternative.
//
// Sources (javap -c, 26.2-inner.jar):
//   - AlternativesEntry / CompositeEntryBase.expand (OR over children; first whose canRun passes)
//   - MatchTool.test (TOOL param null -> false)
//   - ExplosionCondition.test (EXPLOSION_RADIUS null -> true)
//   - ApplyExplosionDecay.run (EXPLOSION_RADIUS null -> stack unchanged)
//   - ApplyBonusCount.run (TOOL param null -> stack unchanged)

import "testing"

// rollBlock loads a blocks/<name> table and rolls it at a fixed seed with the
// default (no-tool, no-explosion) context — the v1 hand-break path. It returns the
// flat list of rolled item ids.
func rollBlock(t *testing.T, name string, seed int64) []int32 {
	t.Helper()
	tbl, err := LoadTable("minecraft:blocks/" + name)
	if err != nil {
		t.Fatalf("LoadTable(blocks/%s): %v", name, err)
	}
	stacks := Roll(tbl, seed, NewLootContext(seed, 0))
	out := make([]int32, 0, len(stacks))
	for _, s := range stacks {
		out = append(out, int32(s.ItemID))
	}
	return out
}

// itemID resolves a "minecraft:<name>" to its numeric id via the package's parse-time
// reverse index (the same map the item entries use).
func itemID(t *testing.T, name string) int32 {
	t.Helper()
	id, ok := itemNameToID[name]
	if !ok {
		t.Fatalf("unknown item %q", name)
	}
	return id
}

// TestBlockDropViaLootAlternatives: the canonical block-drop tables roll their
// non-silk-touch alternative when broken WITHOUT a tool (TOOL param absent ->
// match_tool false -> the silk_touch child fails -> the second child wins).
// stone->cobblestone, grass_block->dirt, oak_log->oak_log (single pool, no
// alternatives). diamond_ore->diamond (apply_bonus + explosion_decay both no-op).
func TestBlockDropViaLootAlternatives(t *testing.T) {
	cases := []struct {
		block string
		want  string
	}{
		{"stone", "minecraft:cobblestone"},
		{"grass_block", "minecraft:dirt"},
		{"oak_log", "minecraft:oak_log"},
		{"diamond_ore", "minecraft:diamond"},
	}
	const seed = int64(42)
	for _, c := range cases {
		t.Run(c.block, func(t *testing.T) {
			got := rollBlock(t, c.block, seed)
			want := itemID(t, c.want)
			if len(got) != 1 {
				t.Fatalf("blocks/%s rolled %d stacks, want exactly 1 (%v)", c.block, len(got), got)
			}
			if got[0] != want {
				t.Fatalf("blocks/%s -> item %d, want %s (%d)", c.block, got[0], c.want, want)
			}
		})
	}
}

// TestBlockDropDiamondCountNoTool: without a TOOL (and thus no fortune enchantment)
// and without an explosion, diamond_ore drops exactly ONE diamond — apply_bonus's
// fortune multiplier and explosion_decay's loss both no-op when their context params
// are absent (the v1 hand-break default).
func TestBlockDropDiamondCountNoTool(t *testing.T) {
	tbl, err := LoadTable("minecraft:blocks/diamond_ore")
	if err != nil {
		t.Fatalf("LoadTable: %v", err)
	}
	stacks := Roll(tbl, 99, NewLootContext(99, 0))
	if len(stacks) != 1 {
		t.Fatalf("diamond_ore rolled %d stacks, want 1", len(stacks))
	}
	if stacks[0].Count != 1 {
		t.Fatalf("diamond_ore drop count = %d, want 1 (no fortune/explosion without tool)", stacks[0].Count)
	}
}

// TestMatchToolNoToolFails: MatchTool.test returns false when the LootContext carries
// no TOOL (the pure block-break default) — the decompiled `TOOL == null -> false`.
func TestMatchToolNoToolFails(t *testing.T) {
	c := &matchTool{}
	ctx := NewLootContext(1, 0) // no tool
	if c.Test(ctx) {
		t.Fatal("match_tool with no TOOL param must be false (decompiled MatchTool.test)")
	}
}

// TestSurvivesExplosionNoExplosionPasses: ExplosionCondition.test returns true when
// EXPLOSION_RADIUS is absent (the decompiled `radius == null -> true`).
func TestSurvivesExplosionNoExplosionPasses(t *testing.T) {
	c := &explosionCondition{}
	ctx := NewLootContext(1, 0)
	if !c.Test(ctx) {
		t.Fatal("survives_explosion with no EXPLOSION_RADIUS must be true (decompiled ExplosionCondition.test)")
	}
}

// TestAlternativesFirstPassing: an alternatives entry whose first child fails its
// condition falls through to the second (first-passing child wins). Built directly
// (not from JSON) to isolate the expand semantics.
func TestAlternativesFirstPassing(t *testing.T) {
	first := &Entry{Type: "minecraft:item", Name: "minecraft:stone", itemID: 1, Weight: 1,
		Conditions: []LootCondition{&matchTool{}}} // fails (no tool)
	second := &Entry{Type: "minecraft:item", Name: "minecraft:cobblestone", itemID: 2, Weight: 1,
		Conditions: []LootCondition{&explosionCondition{}}} // passes (no explosion)
	alt := &Entry{Type: "minecraft:alternatives", Children: []*Entry{first, second}}

	ctx := NewLootContext(7, 0)
	var emitted []*Entry
	expand(alt, ctx, func(e *Entry) { emitted = append(emitted, e) })
	if len(emitted) != 1 {
		t.Fatalf("alternatives emitted %d entries, want 1 (first-passing child)", len(emitted))
	}
	if emitted[0].itemID != 2 {
		t.Fatalf("alternatives emitted itemID %d, want 2 (cobblestone, the second child)", emitted[0].itemID)
	}
}
