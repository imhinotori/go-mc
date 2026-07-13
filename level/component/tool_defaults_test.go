package component

import "testing"

func TestDefaultToolCodecScalarDefaults(t *testing.T) {
	ordinary := []string{"minecraft:diamond_pickaxe", "minecraft:shears"}
	for _, id := range ordinary {
		tool, ok := DefaultTool[id]
		if !ok || tool.DefaultMiningSpeed != 1 || tool.DamagePerBlock != 1 || !tool.CanDestroyBlocksInCreative {
			t.Fatalf("DefaultTool[%q] = (%v, %v), want mining=1 damage=1 creative=true", id, tool, ok)
		}
	}
	for _, id := range []string{"minecraft:diamond_sword", "minecraft:mace", "minecraft:trident"} {
		tool, ok := DefaultTool[id]
		if !ok || tool.DefaultMiningSpeed != 1 || tool.DamagePerBlock != 2 || tool.CanDestroyBlocksInCreative {
			t.Fatalf("DefaultTool[%q] = (%v, %v), want mining=1 damage=2 creative=false", id, tool, ok)
		}
	}
}

func TestDefaultToolCodecDefaultSplit(t *testing.T) {
	var ordinary, restricted int
	for id, tool := range DefaultTool {
		switch {
		case tool.DamagePerBlock == 1 && tool.CanDestroyBlocksInCreative:
			ordinary++
		case tool.DamagePerBlock == 2 && !tool.CanDestroyBlocksInCreative:
			restricted++
		default:
			t.Fatalf("DefaultTool[%q] has unexpected damage/creative pair %d/%v", id, tool.DamagePerBlock, tool.CanDestroyBlocksInCreative)
		}
	}
	if ordinary != 29 || restricted != 9 {
		t.Fatalf("tool default split = %d ordinary / %d restricted, want 29 / 9", ordinary, restricted)
	}
}
