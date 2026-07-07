package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// unbreakingStack builds a damageable non-armor stack (item id 895 — a diamond pickaxe-ish tool id not in
// the enchantable/armor tag) carrying an Unbreaking-L enchantment entry.
func unbreakingStack(level int) component.SlotData {
	s := damageableStack(895, 1, 1561, 0)
	// The unbreaking wire id — resolve its registry index so the entry matches enchantWireName.
	id := enchantWireIndex(enchantUnbreaking)
	p := component.DecodePatch(s)
	p.Set(compEnchantments, &component.Enchantments{
		Enchantments: []component.EnchantmentEntry{{ID: pk.VarInt(id), Level: pk.VarInt(level)}},
	})
	return p.ApplyTo(s)
}

// enchantWireIndex resolves an enchantment resource id to its wire index (the inverse of enchantWireName).
func enchantWireIndex(id string) int {
	enchOrderOnce.Do(loadEnchantOrder)
	return enchIndex[id]
}

// TestUnbreakingReducesDurabilityLoss: over many single-point hits, an Unbreaking-III tool loses far fewer
// durability points than the raw count (the remove_binomial removes ~L/(L+1) = 3/4 of them for a tool).
func TestUnbreakingReducesDurabilityLoss(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(4242)
	s := unbreakingStack(3) // Unbreaking III: tool chance = 3/4

	total := 0
	loop.withRegion(loop.regions[globalRegion], func() {
		for i := 0; i < 2000; i++ {
			total += loop.enchantDurabilityChange(s, 1)
		}
	})
	// Expected ~2000*(1 - 3/4) = ~500 points actually applied. Assert a generous band (never near 2000).
	if total <= 0 || total >= 1200 {
		t.Fatalf("Unbreaking III applied %d/2000 durability points; expected ~500 (band 1..1199)", total)
	}
}

// TestNoUnbreakingUnchanged: a stack without Unbreaking passes the amount through unchanged.
func TestNoUnbreakingUnchanged(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(1)
	s := damageableStack(895, 1, 1561, 0) // no enchantments
	loop.withRegion(loop.regions[globalRegion], func() {
		if got := loop.enchantDurabilityChange(s, 1); got != 1 {
			t.Fatalf("no-Unbreaking durability change = %d, want 1 (unchanged)", got)
		}
	})
}
