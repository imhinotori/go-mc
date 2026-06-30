package server

// chicken_aistep.go — Phase 34 (MOB-PASS-03): the server extras of
// net.minecraft.world.entity.animal.chicken.Chicken.aiStep — the slow-fall (y *= 0.6 while falling) and
// the egg-lay (drop an egg + the egg sound + the eggTime reset). A literal 1:1 port of the unobfuscated
// 26.2 jar bytecode (verbatim in 34-JARNOTES.md:177-196), re-expressed in Go (no GPL paste) with the
// class/method cited. Wired into the per-mob tick (tick_phases.go) gated on typ == entity.Chicken.ID, so
// it NEVER touches the pig (the pig oracle stream is unperturbed — chickenAiStep is never invoked for it).
//
// SCOPE DEFERRAL (cited, locked by 34-JARNOTES.md:223-239): the CHICKEN_LAY loot table is a
// minecraft:gift table whose 3 alternatives are EACH gated on a chicken/variant COMPONENT predicate the
// loot evaluator does NOT port. Vanilla resolves a default-biome chicken to the TEMPERATE variant ->
// minecraft:egg. So dropChickenEgg drops a plain minecraft:egg DIRECTLY (the TEMPERATE default), bypassing
// the variant-gated alternatives. brown_egg/blue_egg by cold/warm biome variant is a future deferral
// (34-CONTEXT Deferred Ideas). The egg-lay RNG (2 nextFloat + nextInt(6000)) is UNCHANGED either way —
// only the item resolution is deferred. The flap visuals (oFlap/flapSpeed/flapping) are pure client-render
// state with no server/net effect and are intentionally omitted.

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chickenIsChickenJockey is the v1 const-false stub for net.minecraft.world.entity.animal.chicken.Chicken
// .isChickenJockey() — true only for a baby chicken ridden by a spawned chicken-jockey (a raid/zombie
// jockey subsystem). No jockey subsystem is wired in v1, so a naturally-spawned / dbg chicken is never a
// jockey; the field defaults false. Named here (not a bare parenthetical) so the !isChickenJockey egg-lay
// gate is explicit and cited. When a jockey subsystem lands, this becomes a real per-entity read.
//
//	[VERIFIED javap Chicken.isChickenJockey: reads the isChickenJockey boolean field; default false on a
//	 naturally spawned chicken (set true only by the jockey spawn path, not wired in v1).]
const chickenIsChickenJockey = false

// chickenAiStep ports the SERVER extras of Chicken.aiStep (slow-fall + egg-lay). VERBATIM
// (34-JARNOTES.md:182-191):
//
//	Vec3 m = getDeltaMovement();
//	if (!onGround && m.y < 0) setDeltaMovement(m.multiply(1.0, 0.6, 1.0));     // SLOW FALL — NO RNG
//	if (level instanceof ServerLevel sl) {
//	    if (isAlive && !isBaby && !isChickenJockey && --eggTime <= 0) {
//	        if (dropFromGiftLootTable(sl, CHICKEN_LAY, this::spawnAtLocation))  // drop 1 egg (v1: direct egg)
//	            playSound(CHICKEN_EGG, 1.0, (nextFloat()-nextFloat())*0.2 + 1.0);  // 2 nextFloat — ONLY if dropped
//	        eggTime = nextInt(6000) + 6000;                                    // nextInt(6000) — ALWAYS when <=0
//	    }
//	}
//
// RNG order when laying: [loot — v1 direct drop, no mob-stream draw] -> 2 nextFloat (sound, ONLY if
// dropped) -> nextInt(6000) (reset). When eggTime > 0: ZERO draws (just the decrement). The slow-fall is
// pure physics (no RNG). isAlive is implicitly true (the tick wiring snapshots only live, non-dead mobs).
// Cite net.minecraft.world.entity.animal.chicken.Chicken.aiStep.
func (t *TickLoop) chickenAiStep(e *Entity) {
	// SLOW FALL: if (!onGround && deltaMovement.y < 0) deltaMovement.multiply(1.0, 0.6, 1.0). Only the y
	// component is scaled (x/z multiply by 1.0). NO RNG.
	if !e.onGround && e.vy < 0 {
		e.vy = e.vy * 0.6 // deltaMovement.multiply(1.0, 0.6, 1.0): only the y component is scaled
	}

	// EGG-LAY gate: !isBaby && !isChickenJockey (isAlive is guaranteed by the live snapshot). A baby (or a
	// jockey, const-false in v1) never lays — ZERO draws on this path.
	if e.isBaby() || chickenIsChickenJockey {
		return
	}

	// --eggTime <= 0: decrement EVERY server tick; only act (drop + reset) when it crosses 0.
	e.eggTime--
	if e.eggTime > 0 {
		return // eggTime still counting down: ZERO RNG draws this tick.
	}

	// dropFromGiftLootTable(CHICKEN_LAY): v1 drops a plain egg directly (the TEMPERATE-variant default —
	// the variant-gated gift table is cite-deferred above). Returns whether an egg was dropped.
	if t.dropChickenEgg(e) {
		// playSound(CHICKEN_EGG, 1.0, (nextFloat()-nextFloat())*0.2 + 1.0): the 2-nextFloat pitch jitter,
		// MOB stream, drawn ONLY when an egg actually dropped (jar order: the 2 nextFloat precede the reset).
		pitch := (mobRandom(e).nextFloat()-mobRandom(e).nextFloat())*0.2 + 1.0
		// soundid 352 == entity.chicken.egg; Chicken (Animal) getSoundSource == NEUTRAL.
		t.broadcastToTrackers(e.id, encodeSoundEntity(352, soundSourceNeutral, e.id, 1.0, pitch, 0))
	}

	// eggTime = nextInt(6000) + 6000: the reset, drawn AFTER the sound (jar order). ALWAYS drawn when
	// eggTime hit <= 0, whether or not an egg dropped.
	e.eggTime = mobRandom(e).nextInt(6000) + 6000
}

// dropChickenEgg drops a single plain minecraft:egg at the chicken's vertical center — the v1 faithful
// port of Chicken.aiStep's dropFromGiftLootTable(CHICKEN_LAY) for a default-biome (TEMPERATE) chicken
// (34-JARNOTES.md:223-239: the variant-gated gift table's TEMPERATE child IS minecraft:egg). Built like a
// block/death drop (NewItemEntity at the mob center, owner-region routing). item.Egg.ID == 1060. Returns
// true (an egg is always produced — the gift table's TEMPERATE branch yields exactly 1 egg). The
// per-item toss velocity is NewItemEntity's own ItemEntity.<init> draw (math/rand/v2, OFF the mob stream),
// not the chicken's egg-lay RNG. Cite Chicken.aiStep dropFromGiftLootTable(CHICKEN_LAY).
func (t *TickLoop) dropChickenEgg(e *Entity) bool {
	stack := component.SlotData{Count: 1, ItemID: pk.VarInt(item.Egg.ID)} // minecraft:egg, count 1
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, stack)
	t.regionForEntity(e).entities.add(ie)
	return true
}
