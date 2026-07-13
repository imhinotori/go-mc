package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// plainTool builds a plain (no NBT patch) held stack for an item id, so heldEffectiveTool resolves
// its DEFAULT minecraft:tool component from component.DefaultTool.
func plainTool(itemID int32) component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)}
}

// TestToolMiningSpeedShovelDirt verifies the base ItemStack.getDestroySpeed(state) is now the held
// tool's Tool.getMiningSpeed(state): a diamond_shovel digs dirt at 8.0 (mineable/shovel rule), a bare
// hand at 1.0, and a diamond_shovel on stone (not a shovel block) falls back to defaultMiningSpeed 1.0.
func TestToolMiningSpeedShovelDirt(t *testing.T) {
	loop, _ := newBlockLoop()
	dirt := block.ToStateID[block.Dirt{}]
	stone := block.ToStateID[block.Stone{}]

	// Bare hand: 1.0.
	hand := blockPlayer(loop, 1.5, 65.0, 1.5)
	if got := loop.heldToolMiningSpeed(hand, dirt); got != 1.0 {
		t.Fatalf("bare-hand mining speed on dirt = %v, want 1.0", got)
	}

	// Diamond shovel on dirt: mineable/shovel rule speed 8.0.
	shovel := toolPlayer(loop, plainTool(int32(item.DiamondShovel.ID)))
	if got := loop.heldToolMiningSpeed(shovel, dirt); got != 8.0 {
		t.Fatalf("diamond_shovel mining speed on dirt = %v, want 8.0", got)
	}
	// Diamond shovel on stone (not a shovel block): defaultMiningSpeed 1.0.
	if got := loop.heldToolMiningSpeed(shovel, stone); got != 1.0 {
		t.Fatalf("diamond_shovel mining speed on stone = %v, want 1.0 (default)", got)
	}
}

// TestToolDestroyProgressFasterWithTool verifies the composed getDestroyProgress: a diamond_shovel
// digs dirt 8x faster than a bare hand (both correct-for-drops -> 30 divisor). dirt hardness 0.5.
//   bare hand: 1.0/0.5/30 = 0.06666667 ; shovel: 8.0/0.5/30 = 0.53333336.
func TestToolDestroyProgressFasterWithTool(t *testing.T) {
	loop, _ := newBlockLoop()
	dirt := block.ToStateID[block.Dirt{}]

	hand := blockPlayer(loop, 1.5, 65.0, 1.5)
	hand.gameMode = gameModeSurvival
	shovel := toolPlayer(loop, plainTool(int32(item.DiamondShovel.ID)))
	shovel.gameMode = gameModeSurvival

	handProg := loop.getDestroyProgress(hand, dirt)
	shovelProg := loop.getDestroyProgress(shovel, dirt)
	if !approxEq(handProg, 1.0/0.5/30.0, 1e-7) {
		t.Fatalf("hand dirt progress = %v, want %v", handProg, float32(1.0/0.5/30.0))
	}
	if !approxEq(shovelProg, 8.0/0.5/30.0, 1e-7) {
		t.Fatalf("shovel dirt progress = %v, want %v", shovelProg, float32(8.0/0.5/30.0))
	}
	if shovelProg <= handProg {
		t.Fatalf("shovel progress %v not faster than hand %v", shovelProg, handProg)
	}
}

// TestToolCorrectForDropsTierGate verifies hasCorrectToolForDrops via the Tool rules' correct_for_drops
// and the incorrect_for_X_tool tier gate, and its effect on getDestroyProgress's 30-vs-100 divisor.
//   - stone (requiresTool): bare hand INCORRECT (100); wooden_pickaxe CORRECT (mineable/pickaxe true,
//     not in incorrect_for_wooden_tool) -> 30 divisor.
//   - iron_ore (requiresTool, in incorrect_for_wooden_tool): wooden_pickaxe INCORRECT -> 100 divisor;
//     diamond_pickaxe CORRECT -> 30.
func TestToolCorrectForDropsTierGate(t *testing.T) {
	loop, _ := newBlockLoop()
	stone := block.ToStateID[block.Stone{}]
	ironOre := block.ToStateID[block.IronOre{}]

	hand := blockPlayer(loop, 1.5, 65.0, 1.5)
	woodPick := toolPlayer(loop, plainTool(int32(item.WoodenPickaxe.ID)))
	diamondPick := toolPlayer(loop, plainTool(int32(item.DiamondPickaxe.ID)))

	// stone requires a correct tool.
	if loop.hasCorrectToolForDrops(hand, stone, true) {
		t.Fatalf("bare hand should be INCORRECT for stone")
	}
	if !loop.hasCorrectToolForDrops(woodPick, stone, true) {
		t.Fatalf("wooden_pickaxe should be CORRECT for stone")
	}

	// iron_ore is in incorrect_for_wooden_tool.
	if loop.hasCorrectToolForDrops(woodPick, ironOre, true) {
		t.Fatalf("wooden_pickaxe should be INCORRECT for iron_ore (incorrect_for_wooden_tool)")
	}
	if !loop.hasCorrectToolForDrops(diamondPick, ironOre, true) {
		t.Fatalf("diamond_pickaxe should be CORRECT for iron_ore")
	}
}
