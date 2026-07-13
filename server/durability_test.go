package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
)

// damageableStack (defined in grindstone_test.go) builds a SlotData carrying MAX_DAMAGE + DAMAGE
// components (a vanilla-created tool), so stackIsDamageableItem is true — reused here.

// TestStackHurtAndBreakWears: a damageable item takes damage and does NOT break until it reaches max.
func TestStackHurtAndBreakWears(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := damageableStack(100, 1, 10, 0) // maxDamage 10, damage 0
	next, broke := loop.stackHurtAndBreak(s, 1, false)
	if broke {
		t.Fatalf("a tool at damage 1/10 must not break")
	}
	if got := stackDamageValue(next); got != 1 {
		t.Fatalf("damage value after 1 hit = %d, want 1", got)
	}
}

// TestStackHurtAndBreakBreaks: at max damage the item breaks (shrinks by 1 -> empty for the last one).
func TestStackHurtAndBreakBreaks(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := damageableStack(100, 1, 10, 9) // one hit from max
	next, broke := loop.stackHurtAndBreak(s, 1, false)
	if !broke {
		t.Fatalf("a tool reaching max damage must break")
	}
	if !stackEmpty(next) {
		t.Fatalf("the last item breaking must leave an empty stack, got count=%d", next.Count)
	}
}

// TestStackHurtAndBreakCreative: a creative player's gear never wears.
func TestStackHurtAndBreakCreative(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := damageableStack(100, 1, 10, 9)
	next, broke := loop.stackHurtAndBreak(s, 1, true) // creative == true
	if broke {
		t.Fatalf("creative gear must not break")
	}
	if got := stackDamageValue(next); got != 9 {
		t.Fatalf("creative gear must not wear: damage = %d, want 9", got)
	}
}

// TestStackHurtAndBreakUndamageable: an item with no MAX_DAMAGE/DAMAGE components never wears.
func TestStackHurtAndBreakUndamageable(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := component.SlotData{ItemID: 1, Count: 1} // a plain block, no durability components
	next, broke := loop.stackHurtAndBreak(s, 1, false)
	if broke {
		t.Fatalf("an undamageable item must not break")
	}
	if stackDamageValue(next) != 0 {
		t.Fatalf("an undamageable item must not accrue damage")
	}
}

// TestStackIsBroken: isBroken is true exactly when a damageable item's damage reaches its max.
func TestStackIsBroken(t *testing.T) {
	if stackIsBroken(damageableStack(100, 1, 10, 9)) {
		t.Fatalf("damage 9/10 is not broken")
	}
	if !stackIsBroken(damageableStack(100, 1, 10, 10)) {
		t.Fatalf("damage 10/10 must be broken")
	}
	if stackIsBroken(component.SlotData{ItemID: 1, Count: 1}) {
		t.Fatalf("an undamageable item is never broken")
	}
}

// TestStackToolDamagePerBlock reads the tool component's damage_per_block, absent on a non-tool.
func TestStackToolDamagePerBlock(t *testing.T) {
	s := damageableStack(100, 1, 100, 0)
	p := component.DecodePatch(s)
	p.Set(compTool, &component.Tool{DamagePerBlock: 2})
	s = p.ApplyTo(s)
	if dpb, ok := stackToolDamagePerBlock(s); !ok || dpb != 2 {
		t.Fatalf("tool damage_per_block = (%d,%v), want (2,true)", dpb, ok)
	}
	if _, ok := stackToolDamagePerBlock(component.SlotData{ItemID: 1, Count: 1}); ok {
		t.Fatalf("a non-tool item must have no tool damage_per_block")
	}
}

func TestStackToolDamagePerBlockUsesItemDefault(t *testing.T) {
	pick := plainTool(int32(item.DiamondPickaxe.ID))
	if dpb, ok := stackToolDamagePerBlock(pick); !ok || dpb != 1 {
		t.Fatalf("default pickaxe damage_per_block = (%d,%v), want (1,true)", dpb, ok)
	}
	sword := plainTool(int32(item.DiamondSword.ID))
	if dpb, ok := stackToolDamagePerBlock(sword); !ok || dpb != 2 {
		t.Fatalf("default sword damage_per_block = (%d,%v), want (2,true)", dpb, ok)
	}
}
