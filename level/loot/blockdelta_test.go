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

// --- Leaf/shears items-predicate tests (the by-hand leaf-block-drop bug fix) --------------------
//
// The leaf tables gate their leaf-BLOCK drop on an alternatives child whose condition is
// any_of(match_tool{items:"minecraft:shears"}, match_tool{silk_touch}). Vanilla: a bare hand drops
// only saplings/sticks/apple (probabilistic) — the leaf block drops ONLY with shears OR silk_touch.
// The bug was that match_tool{items:shears} was not modeled, so a bare hand (HasTool true) fell
// through to `return true` and dropped the leaf block. These tests lock the fixed behavior.
//
// Source: javap MatchTool.test -> ItemPredicate.test (items HolderSet membership: tool.is(items)).

// rollBlockWithCtx rolls a blocks/<name> table at a fixed seed with a caller-supplied context (a
// tool/silk-touch context) and returns the flat list of rolled item ids.
func rollBlockWithCtx(t *testing.T, name string, seed int64, ctx *LootContext) []int32 {
	t.Helper()
	tbl, err := LoadTable("minecraft:blocks/" + name)
	if err != nil {
		t.Fatalf("LoadTable(blocks/%s): %v", name, err)
	}
	stacks := Roll(tbl, seed, ctx)
	out := make([]int32, 0, len(stacks))
	for _, s := range stacks {
		out = append(out, int32(s.ItemID))
	}
	return out
}

// containsItem reports whether the rolled id list includes item `name`.
func containsItem(t *testing.T, got []int32, name string) bool {
	t.Helper()
	want := itemID(t, name)
	for _, id := range got {
		if id == want {
			return true
		}
	}
	return false
}

// shearsToolContext builds a block-break LootContext for a held shears (HasTool true, ToolItemID
// "minecraft:shears", no enchantments) — the state blockBreakLootContext produces for a shears break.
func shearsToolContext(seed int64) *LootContext {
	c := NewLootContext(seed, 0)
	c.HasTool = true
	c.ToolItemID = "minecraft:shears"
	return c
}

// silkTouchToolContext builds a block-break context for a silk_touch tool (e.g. a diamond_pickaxe
// with silk_touch): HasTool true, a non-shears ToolItemID, ToolSilkTouch true.
func silkTouchToolContext(seed int64) *LootContext {
	c := NewLootContext(seed, 0)
	c.HasTool = true
	c.ToolItemID = "minecraft:diamond_pickaxe"
	c.ToolSilkTouch = true
	return c
}

// bareHandContext builds the block-break context for a bare hand: HasTool true (vanilla always sets
// TOOL even for the fist), ToolItemID "" (no items match), no enchantments — the exact state
// blockBreakLootContext produces for an empty main hand.
func bareHandContext(seed int64) *LootContext {
	c := NewLootContext(seed, 0)
	c.HasTool = true
	return c
}

// TestOakLeavesBareHandDropsNoLeafBlock: breaking oak_leaves BY HAND must NOT drop the oak_leaves
// block — the shears/silk_touch alternative fails (ToolItemID "" is not a member of {shears} and
// ToolSilkTouch is false), so only the sapling/stick/apple pools are eligible. This is the bug: it
// previously dropped the leaf block. We scan a spread of seeds so a probabilistic sapling/stick roll
// never masks a leaf-block leak.
func TestOakLeavesBareHandDropsNoLeafBlock(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		got := rollBlockWithCtx(t, "oak_leaves", seed, bareHandContext(seed))
		if containsItem(t, got, "minecraft:oak_leaves") {
			t.Fatalf("seed %d: bare-hand oak_leaves break dropped the LEAF BLOCK (%v); vanilla drops only sapling/stick/apple", seed, got)
		}
	}
}

// TestOakLeavesShearsDropsLeafBlock: breaking oak_leaves with SHEARS drops the oak_leaves block (the
// items:"minecraft:shears" alternative wins). Across seeds the leaf block must ALWAYS be present.
func TestOakLeavesShearsDropsLeafBlock(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		got := rollBlockWithCtx(t, "oak_leaves", seed, shearsToolContext(seed))
		if !containsItem(t, got, "minecraft:oak_leaves") {
			t.Fatalf("seed %d: shears oak_leaves break did NOT drop the leaf block (%v)", seed, got)
		}
	}
}

// TestOakLeavesSilkTouchDropsLeafBlock: breaking oak_leaves with a silk_touch tool drops the
// oak_leaves block (the silk_touch alternative wins even though the tool is not shears).
func TestOakLeavesSilkTouchDropsLeafBlock(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		got := rollBlockWithCtx(t, "oak_leaves", seed, silkTouchToolContext(seed))
		if !containsItem(t, got, "minecraft:oak_leaves") {
			t.Fatalf("seed %d: silk_touch oak_leaves break did NOT drop the leaf block (%v)", seed, got)
		}
	}
}

// TestMatchToolItemsMembership: the ItemPredicate items sub-predicate — a match_tool{items:shears}
// passes ONLY when ToolItemID is exactly minecraft:shears, and fails for a bare hand or a wrong tool.
func TestMatchToolItemsMembership(t *testing.T) {
	m := &matchTool{requireItems: []string{"minecraft:shears"}}

	// bare hand: HasTool true, ToolItemID "" -> not a member -> false.
	if m.Test(bareHandContext(1)) {
		t.Fatal("match_tool{items:shears} must be FALSE for a bare hand (ToolItemID empty)")
	}
	// wrong tool: a diamond_pickaxe -> not a member -> false.
	wrong := NewLootContext(1, 0)
	wrong.HasTool = true
	wrong.ToolItemID = "minecraft:diamond_pickaxe"
	if m.Test(wrong) {
		t.Fatal("match_tool{items:shears} must be FALSE for a non-shears tool")
	}
	// shears -> member -> true.
	if !m.Test(shearsToolContext(1)) {
		t.Fatal("match_tool{items:shears} must be TRUE for a shears tool")
	}
	// no TOOL at all (HasTool false) -> MatchTool.test TOOL==null -> false.
	noTool := NewLootContext(1, 0)
	if m.Test(noTool) {
		t.Fatal("match_tool{items:shears} must be FALSE when no TOOL param is present")
	}
}
