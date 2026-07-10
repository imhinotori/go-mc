package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// consume_effects_test.go covers the CONSUMABLE onConsume effect-application chain (consume_effects.go):
// ApplyStatusEffects (golden_apple regen+absorption, spider_eye poison), RemoveStatusEffects
// (honey_bottle poison), ClearAllStatusEffects (milk_bucket), TeleportRandomly (chorus_fruit), and the
// OminousBottleAmplifier ConsumableListener (ominous_bottle bad_omen). Reuses the item_use_test harness
// (newBlockLoop / blockPlayer / giveHeld / startEat). The RNG-touching cases pin a deterministic
// region levelRandom so the probability gate and teleport rolls are reproducible.

// eatFully starts a mainhand use and ticks it to completion (durationTicks tickUseItem calls). The
// player must already hold the item and satisfy canConsume.
func eatFully(loop *TickLoop, p *tickPlayer, durationTicks int) {
	startEat(loop, p)
	for i := 0; i < durationTicks; i++ {
		loop.tickUseItem()
	}
}

// TestGoldenAppleAppliesRegenAndAbsorption: a completed golden_apple eat applies the two
// apply_effects MobEffectInstances -- regeneration (amp 1, 100t) and absorption (amp 0, 2400t) --
// and absorption onEffectStarted fills 4*(amp+1) = 4 absorption hearts. Cite ApplyStatusEffects
// ConsumeEffect + golden_apple on_consume_effects.
func TestGoldenAppleAppliesRegenAndAbsorption(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 10
	giveHeld(p, item.GoldenApple.ID, 1)

	eatFully(loop, p, 32)

	if isUsingItem(p) {
		t.Fatalf("golden_apple still using after 32 ticks")
	}
	regen, ok := p.activeEffects[effectRegeneration]
	if !ok {
		t.Fatalf("golden_apple did not apply regeneration")
	}
	if regen.amplifier != 1 || regen.duration != 100 {
		t.Fatalf("regeneration amp=%d dur=%d, want amp=1 dur=100", regen.amplifier, regen.duration)
	}
	abs, ok := p.activeEffects[effectAbsorption]
	if !ok {
		t.Fatalf("golden_apple did not apply absorption")
	}
	if abs.amplifier != 0 || abs.duration != 2400 {
		t.Fatalf("absorption amp=%d dur=%d, want amp=0 dur=2400", abs.amplifier, abs.duration)
	}
	// AbsorptionMobEffect.onEffectStarted: setAbsorptionAmount(max(cur, 4*(amp+1))) = 4.0.
	if got := p.getAbsorptionAmount(); got != 4.0 {
		t.Fatalf("absorption amount = %v, want 4.0 (4*(amp+1))", got)
	}
}

// TestSpiderEyeAppliesPoison: a completed spider_eye eat applies poison (amp 0, 100t) at
// probability 1.0. Cite spider_eye on_consume_effects.
func TestSpiderEyeAppliesPoison(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 10
	giveHeld(p, item.SpiderEye.ID, 1)

	eatFully(loop, p, 32)

	poison, ok := p.activeEffects[effectPoison]
	if !ok {
		t.Fatalf("spider_eye did not apply poison")
	}
	if poison.amplifier != 0 || poison.duration != 100 {
		t.Fatalf("poison amp=%d dur=%d, want amp=0 dur=100", poison.amplifier, poison.duration)
	}
}

// TestMilkBucketClearsAllEffects: drinking a milk_bucket (no FOOD component, so usable at full hunger)
// runs ClearAllStatusEffectsConsumeEffect -> removeAllEffects, clearing every active effect. Cite
// milk_bucket clear_all_effects.
func TestMilkBucketClearsAllEffects(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 20 // full hunger: milk has no food, so canConsume is still true
	// Seed two active effects to be cleared.
	loop.addPlayerEffect(p, 0, effectPoison, 200, 0, 1.0)
	loop.addPlayerEffect(p, 0, effectRegeneration, 200, 0, 1.0)
	if len(p.activeEffects) != 2 {
		t.Fatalf("pre-drink effects = %d, want 2", len(p.activeEffects))
	}
	giveHeld(p, item.MilkBucket.ID, 1)

	eatFully(loop, p, 32) // milk consume_seconds default 1.6 -> 32 ticks

	if len(p.activeEffects) != 0 {
		t.Fatalf("milk_bucket left %d effects, want 0 (removeAllEffects)", len(p.activeEffects))
	}
}

// TestHoneyBottleRemovesPoison: drinking a honey_bottle (consume_seconds 2.0 -> 40 ticks) removes
// poison via RemoveStatusEffectsConsumeEffect but leaves other effects intact. Cite honey_bottle
// remove_effects (minecraft:poison).
func TestHoneyBottleRemovesPoison(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 10
	loop.addPlayerEffect(p, 0, effectPoison, 200, 0, 1.0)
	loop.addPlayerEffect(p, 0, effectRegeneration, 200, 0, 1.0)
	giveHeld(p, item.HoneyBottle.ID, 1)

	eatFully(loop, p, 40) // honey consume_seconds 2.0 -> 40 ticks

	if _, ok := p.activeEffects[effectPoison]; ok {
		t.Fatalf("honey_bottle did not remove poison")
	}
	if _, ok := p.activeEffects[effectRegeneration]; !ok {
		t.Fatalf("honey_bottle wrongly removed regeneration (should only remove poison)")
	}
}

// TestOminousBottleGrantsBadOmen: drinking an ominous_bottle (no FOOD component) runs the
// OminousBottleAmplifier ConsumableListener, granting BAD_OMEN (duration 120000, amplifier ==
// component value 0). Cite OminousBottleAmplifier.onConsume.
func TestOminousBottleGrantsBadOmen(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 20 // full: ominous_bottle has no food -> canConsume true
	giveHeld(p, item.OminousBottle.ID, 1)

	eatFully(loop, p, 32)

	bo, ok := p.activeEffects[effectBadOmen]
	if !ok {
		t.Fatalf("ominous_bottle did not grant bad_omen")
	}
	if bo.amplifier != 0 || bo.duration != 120000 {
		t.Fatalf("bad_omen amp=%d dur=%d, want amp=0 dur=120000", bo.amplifier, bo.duration)
	}
}

// TestChorusFruitTeleports: eating a chorus_fruit runs TeleportRandomlyConsumeEffect, moving the
// player to a ground-standable landing within the 16.0 diameter box. With a pinned levelRandom and a
// solid floor across the search area, the teleport succeeds and the player position changes. Cite
// TeleportRandomlyConsumeEffect + LivingEntity.randomTeleport.
func TestChorusFruitTeleports(t *testing.T) {
	loop, mgr := newBlockLoop()
	// Pin the region RNG so the teleport offset rolls are deterministic.
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(1)
	// Lay a solid floor at y=64 across the whole reachable search area (player at y=65 stands on it).
	const floorY = 64
	for x := -10; x <= 10; x++ {
		for z := -10; z <= 10; z++ {
			mgr.SetBlock(pk.Position{X: x, Y: floorY, Z: z}, block.ToStateID[block.Stone{}], dimMinY)
		}
	}
	p := blockPlayer(loop, 0.5, float64(floorY+1), 0.5)
	p.gameMode = gameModeSurvival
	p.food = 10
	startX, startZ := p.x, p.z
	giveHeld(p, item.ChorusFruit.ID, 1)

	eatFully(loop, p, 32)

	if p.x == startX && p.z == startZ {
		t.Fatalf("chorus_fruit did not teleport the player (still at %.2f,%.2f)", startX, startZ)
	}
	// randomTeleport decrements the (continuous) sampled height by 1.0 per step until the cell below
	// blocksMotion, then teleportTo(x, height, z). The landing height is thus the sampled y snapped by
	// whole steps to just above the floor -- above the floor top (floorY+1) and within one step of it.
	if p.y < float64(floorY+1) || p.y >= float64(floorY+2) {
		t.Fatalf("chorus landing y=%v, want in [%d, %d) (just above the floor)", p.y, floorY+1, floorY+2)
	}
}
