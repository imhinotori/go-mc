package loot

import (
	"testing"
)

// TestSetCount asserts set_count (non-add) sets the stack count to count.GetInt(ctx)
// drawn via its NumberProvider (here a constant, then a uniform). Add=false is the
// chest form: setCount(count.getInt(ctx)).
func TestSetCount(t *testing.T) {
	ctx := NewLootContext(7, 0)
	// constant count -> exact.
	s := &SetItemCountFunction{Count: ConstantValue(5)}
	stack := &ItemStack{ItemID: 1, Count: 1}
	s.Run(stack, ctx)
	if stack.Count != 5 {
		t.Errorf("set_count constant 5: got count %d, want 5", stack.Count)
	}
	// non-add overwrites (does not accumulate): a second run with constant 3 -> 3.
	s2 := &SetItemCountFunction{Count: ConstantValue(3), Add: false}
	s2.Run(stack, ctx)
	if stack.Count != 3 {
		t.Errorf("set_count non-add: got count %d, want 3 (overwrite, not 5+3)", stack.Count)
	}
	// add form accumulates: base(3) + 4 = 7.
	s3 := &SetItemCountFunction{Count: ConstantValue(4), Add: true}
	s3.Run(stack, ctx)
	if stack.Count != 7 {
		t.Errorf("set_count add: got count %d, want 7 (3+4)", stack.Count)
	}
	// uniform count stays in range.
	u := &SetItemCountFunction{Count: &Uniform{Min: ConstantValue(2), Max: ConstantValue(6)}}
	st2 := &ItemStack{ItemID: 1, Count: 1}
	u.Run(st2, ctx)
	if st2.Count < 2 || st2.Count > 6 {
		t.Errorf("set_count uniform[2,6]: got %d, out of [2,6]", st2.Count)
	}
}

// TestEnchantRandomly asserts enchant_randomly picks one enchantment from the
// resolved #on_random_loot option set via Util.getRandomSafe (one nextInt(size)
// draw) and records a level in [1, maxLevel]. The book item is upgraded to
// enchanted_book.
func TestEnchantRandomly(t *testing.T) {
	js := `{"pools":[{"rolls":1.0,"entries":[
		{"type":"minecraft:item","name":"minecraft:book","weight":1,
		 "functions":[{"function":"minecraft:enchant_randomly","options":"#minecraft:on_random_loot"}]}
	]}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	bookID := itemNameToID["minecraft:book"]
	encBookID := itemNameToID["minecraft:enchanted_book"]
	// Roll several times from one warmed source; every roll must record exactly one
	// enchantment with a valid level, and upgrade book -> enchanted_book.
	ctx := NewLootContext(0xE0CA, 0)
	for i := 0; i < 200; i++ {
		stacks := RollStacks(tbl, ctx)
		if len(stacks) != 1 {
			t.Fatalf("iter %d: got %d stacks, want 1", i, len(stacks))
		}
		s := stacks[0]
		if s.ItemID == bookID {
			t.Errorf("iter %d: book not upgraded to enchanted_book", i)
		}
		if encBookID != 0 && s.ItemID != encBookID {
			t.Errorf("iter %d: got item %d, want enchanted_book %d", i, s.ItemID, encBookID)
		}
		if len(s.Enchantments) != 1 {
			t.Fatalf("iter %d: got %d enchantments, want 1: %v", i, len(s.Enchantments), s.Enchantments)
		}
		for ench, lvl := range s.Enchantments {
			maxLvl := enchantMaxLevel[ench]
			if maxLvl < 1 {
				maxLvl = 1
			}
			if lvl < 1 || lvl > maxLvl {
				t.Errorf("iter %d: enchant %s level %d out of [1,%d]", i, ench, lvl, maxLvl)
			}
		}
	}
}

// TestEnchantRandomlyResolvesTag asserts the #on_random_loot tag resolves to a
// non-empty, deterministic (sorted) candidate set including its known members
// (mending is an explicit member; the non_treasure nested tag contributes sharpness).
func TestEnchantRandomlyResolvesTag(t *testing.T) {
	opts := resolveEnchantTag("#minecraft:on_random_loot", map[string]bool{})
	if len(opts) == 0 {
		t.Fatal("on_random_loot resolved to empty")
	}
	has := func(id string) bool {
		for _, o := range opts {
			if o == id {
				return true
			}
		}
		return false
	}
	if !has("minecraft:mending") {
		t.Errorf("on_random_loot missing mending; got %v", opts)
	}
	if !has("minecraft:sharpness") {
		t.Errorf("on_random_loot missing sharpness (via #non_treasure); got %v", opts)
	}
	// Deterministic: a second resolution yields the same order.
	opts2 := resolveEnchantTag("#minecraft:on_random_loot", map[string]bool{})
	if len(opts) != len(opts2) {
		t.Fatalf("non-deterministic resolution: %d vs %d", len(opts), len(opts2))
	}
	for i := range opts {
		if opts[i] != opts2[i] {
			t.Errorf("order differs at %d: %q vs %q", i, opts[i], opts2[i])
		}
	}
}

// TestEnchantWithLevels asserts enchant_with_levels (jungle_temple book at
// levels:30) draws the level budget via its NumberProvider and produces an
// enchanted_book, recording the cited stub budget (the EnchantmentHelper selection
// is deferred — see enchant.go). The draw must happen (faithful RNG consumption).
func TestEnchantWithLevels(t *testing.T) {
	js := `{"pools":[{"rolls":1.0,"entries":[
		{"type":"minecraft:item","name":"minecraft:book","weight":1,
		 "functions":[{"function":"minecraft:enchant_with_levels","levels":30}]}
	]}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	encBookID := itemNameToID["minecraft:enchanted_book"]
	ctx := NewLootContext(0x30, 0)
	stacks := RollStacks(tbl, ctx)
	if len(stacks) != 1 {
		t.Fatalf("got %d stacks, want 1", len(stacks))
	}
	s := stacks[0]
	if encBookID != 0 && s.ItemID != encBookID {
		t.Errorf("got item %d, want enchanted_book %d", s.ItemID, encBookID)
	}
	if got := s.Enchantments["__enchant_with_levels_budget__"]; got != 30 {
		t.Errorf("recorded budget %d, want 30 (levels constant)", got)
	}
}

// TestJungleTempleParses asserts the jungle_temple table (the only enchant_with_levels
// user) parses + rolls without error (the enchant function is wired into the engine).
func TestJungleTempleRolls(t *testing.T) {
	tbl, err := LoadTable("minecraft:chests/jungle_temple")
	if err != nil {
		t.Fatalf("LoadTable jungle_temple: %v", err)
	}
	ctx := NewLootContext(424242, 0)
	stacks := RollStacks(tbl, ctx)
	if len(stacks) == 0 {
		t.Error("jungle_temple rolled nothing")
	}
}
