package server

// explosion_blocks.go — the BLOCK-DESTRUCTION half of the ServerExplosion port (a 1:1 port of
// net.minecraft.world.level.ServerExplosion.calculateExplodedPositions + interactWithBlocks,
// temp/cache/26.2-inner.jar, javap -c -p this session). The ENTITY half (hurtEntities +
// getSeenPercent + knockback) lives in explosion.go; this file adds the toBlow collection (the
// 16^3-shell resistance-attenuated rays), the Util.shuffle, and the per-block destroy+drop.
//
// GATE — MOB_GRIEFING (ServerLevel.explode ExplosionInteraction.MOB case + ServerExplosion
// .interactsWithBlocks): a creeper is ExplosionInteraction.MOB. When MOB_GRIEFING is TRUE the
// destroy type is getDestroyType(MOB_EXPLOSION_DROP_DECAY) (DESTROY_WITH_DECAY, since that
// gamerule defaults true); when MOB_GRIEFING is FALSE the type is KEEP and interactsWithBlocks()
// returns false — NO block is removed. Both gamerules default TRUE in vanilla 26.2 (javap
// GameRules.<clinit>: registerBoolean(..., true)); v1 has no gamerule engine yet, so they are
// their cited defaults, structured to become real ServerLevel.getGameRules().get(...) reads later.
//   [VERIFIED javap ServerLevel.explode (MOB -> MOB_GRIEFING?getDestroyType(MOB_EXPLOSION_DROP_DECAY):KEEP);
//    ServerExplosion.interactsWithBlocks (blockInteraction != KEEP); GameRules MOB_GRIEFING/
//    MOB_EXPLOSION_DROP_DECAY default true.]

import (
	"math"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/loot"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// mobExplosionDropDecay is the GameRules.MOB_EXPLOSION_DROP_DECAY gamerule value (vanilla default
// TRUE — javap GameRules.<clinit>). It selects the MOB destroy type: TRUE -> DESTROY_WITH_DECAY
// (each drop rolls the 1/radius explosion-decay survival), FALSE -> DESTROY (all drops survive).
// Cited default; becomes a real getGameRules().get(MOB_EXPLOSION_DROP_DECAY) read later.
//
//	[VERIFIED javap ServerLevel.getDestroyType + GameRules.<clinit>: MOB_EXPLOSION_DROP_DECAY == true.]
var mobExplosionDropDecay = true

// Explosion ray-collection constants (bytecode-verified against ServerExplosion
// .calculateExplodedPositions).
const (
	explosionStepBase       = 0.30000001192092896 // the d15 += d6 * 0.3(double) per-step advance
	explosionResistanceAdd  = 0.3                 // (resistance + 0.3f) * 0.3f attenuation
	explosionResistanceMul  = 0.3                 //   the * 0.3f factor
	explosionStepDrain      = 0.22500001          // f14 -= 0.22500001f each step (the per-cell falloff)
	explosionWorldBoundHorz = 30000000            // isInWorldBoundsHorizontal: |x|,|z| < 30_000_000
)

// calculateExplodedPositions is the 1:1 port of ServerExplosion.calculateExplodedPositions: it
// walks the 16^3 shell of unit rays out of the blast center, attenuating each ray strength by
// every block explosion resistance it passes through, and collects the BlockPos set whose
// resistance the ray still overcomes (the "toBlow" set). It draws EXACTLY one level.random
// nextFloat() per shell ray (the radius*(0.7+nextFloat()*0.6) ray strength), in xx->yy->zz order,
// so the RNG stream stays in lockstep. This REPLACES the old calculateExplodedRayRolls stub
// (which only advanced the RNG) — the draw order is identical, and now the blocks are collected.
//
//	[VERIFIED javap ServerExplosion.calculateExplodedPositions — the exact float casts, the 0.3f
//	 step, the (r+0.3f)*0.3f attenuation, the 0.22500001f drain, and the 0.7f+0.6f ray band.]
func (t *TickLoop) calculateExplodedPositions(cx, cy, cz, radius float64) []pk.Position {
	r := t.cur().levelRandom
	w := t.world()
	// A HashSet in vanilla; a map here (dedup — a ray can revisit a cell). Order does not matter:
	// interactWithBlocks shuffles it before use, so the collection order is irrelevant.
	toBlow := make(map[pk.Position]struct{})

	for xx := 0; xx < 16; xx++ {
		for yy := 0; yy < 16; yy++ {
			for zz := 0; zz < 16; zz++ {
				// Shell only: skip the interior (vanilla continues when every axis is neither 0 nor 15).
				if xx != 0 && xx != 15 && yy != 0 && yy != 15 && zz != 0 && zz != 15 {
					continue
				}
				// Unit direction: (idx/15f * 2f - 1f) per axis, then normalized. The float32 math
				// is preserved (idx/15f is a float32 divide) before widening to float64.
				d6 := float64(float32(xx)/15.0*2.0 - 1.0)
				d8 := float64(float32(yy)/15.0*2.0 - 1.0)
				d10 := float64(float32(zz)/15.0*2.0 - 1.0)
				mag := math.Sqrt(d6*d6 + d8*d8 + d10*d10)
				d6 /= mag
				d8 /= mag
				d10 /= mag

				// Ray strength: radius * (0.7f + nextFloat()*0.6f). ONE nextFloat per shell ray
				// (the lockstep draw). The float32 arithmetic matches the bytecode f2 arithmetic.
				f14 := float32(radius) * (float32(explosionRayPowerBase) + r.NextFloat()*float32(explosionRayPowerRange))

				d15, d17, d19 := cx, cy, cz
				for f14 > 0.0 {
					px := floorI(d15)
					py := floorI(d17)
					pz := floorI(d19)
					pos := pk.Position{X: px, Y: py, Z: pz}
					if !explosionInWorldBounds(px, py, pz) {
						break
					}
					// getBlockExplosionResistance: present iff (block not air) OR (fluid not empty).
					// air with no fluid -> Optional.empty -> no attenuation. Everything else attenuates.
					if st, ok := w.GetBlock(pos, dimMinY); ok {
						if !block.IsAir(st) {
							res := block.StateExplosionResistance(st)
							f14 -= (res + float32(explosionResistanceAdd)) * float32(explosionResistanceMul)
						}
					}
					// shouldBlockExplode is unconditionally true (ExplosionDamageCalculator).
					if f14 > 0.0 {
						toBlow[pos] = struct{}{}
					}
					d15 += d6 * explosionStepBase
					d17 += d8 * explosionStepBase
					d19 += d10 * explosionStepBase
					f14 -= float32(explosionStepDrain)
				}
			}
		}
	}

	// Deterministic slice for the shuffle (map iteration is random; Util.shuffle re-randomizes
	// with level.random anyway, and vanilla builds an ObjectArrayList from the HashSet whose order
	// is itself unspecified — so any stable-then-shuffled order matches observable behavior).
	out := make([]pk.Position, 0, len(toBlow))
	for p := range toBlow {
		out = append(out, p)
	}
	return out
}

// interactWithBlocks is the 1:1 port of ServerExplosion.interactWithBlocks: shuffle the toBlow
// list with level.random (Util.shuffle), then for each pos run the block onExplosionHit (drop its
// loot with the explosion-decay drop-chance, then set it to air). Vanilla batches drops through a
// StackCollector then popResource; v1 spawns an Item entity per rolled stack directly (the
// server/block_drop path), which is the same observable drop with the merge being a cosmetic
// stack-count grouping we do not need for correctness.
//
//	[VERIFIED javap ServerExplosion.interactWithBlocks + BlockBehaviour.onExplosionHit
//	 (dropFromExplosion? getDrops(EXPLOSION_RADIUS for DESTROY_WITH_DECAY) then setBlock(AIR,3)).]
func (t *TickLoop) interactWithBlocks(toBlow []pk.Position, radius float64) {
	w := t.world()
	if w == nil {
		return
	}
	explosionShuffle(toBlow, t)

	air := block.DefaultStateID["minecraft:air"]
	for _, pos := range toBlow {
		st, ok := w.GetBlock(pos, dimMinY)
		if !ok || block.IsAir(st) {
			continue // BlockBehaviour.onExplosionHit early-returns on air.
		}
		// Drop the block loot before removing it (dropFromExplosion is true for the base Block).
		// DESTROY_WITH_DECAY threads EXPLOSION_RADIUS so each drop rolls the 1/radius survival;
		// DESTROY leaves it unset (all drops survive). Selected by mobExplosionDropDecay.
		t.spawnExplosionDrop(pos, st, radius)
		// setBlock(pos, AIR, 3): remove the block + broadcast the update to trackers.
		w.SetBlock(pos, air, dimMinY)
		t.broadcastBlockUpdate(pos, air)

		// Block.wasExploded (the onExplosionHit tail, AFTER the drop + setBlock(AIR)): for a TNT block, the
		// chain-prime — spawn a PrimedTnt at the (now-air) cell with a random SHORT fuse (getRandomShortFuse:
		// nextInt(20)+10 for the default fuse) so a TNT cluster detonates in a staggered cascade rather than
		// in perfect lockstep. Every other block's wasExploded is a no-op here. CITE TntBlock.wasExploded:
		// `if (TNT_EXPLODES) { PrimedTnt tnt = new PrimedTnt(...); tnt.setFuse(getRandomShortFuse(getFuse(),
		// random)); addFreshEntity(tnt); }` — the random fuse is drawn from ServerLevel.getRandom() (the
		// region levelRandom).
		if isTntBlock(st) && tntExplodes {
			shortFuse := tntDefaultFuseTime
			if r := t.cur(); r != nil && r.levelRandom != nil {
				shortFuse = tntGetRandomShortFuse(tntDefaultFuseTime, r.levelRandom)
			}
			cx := float64(pos.X) + 0.5
			cy := float64(pos.Y)
			cz := float64(pos.Z) + 0.5
			t.spawnPrimedTnt(cx, cy, cz, int32(shortFuse))
		}
	}
}

// spawnExplosionDrop is the explosion-flavored block drop: it rolls the block loot table with the
// EXPLOSION_RADIUS context (when mobExplosionDropDecay selects DESTROY_WITH_DECAY) so the per-drop
// 1/radius explosion-decay survival roll applies (ApplyExplosionDecay / survives_explosion), then
// spawns an Item entity per surviving stack at the block center — the same store-add + toss path
// server/block_drop.spawnBlockDrop uses for a hand break. No creative gate (an explosion is not a
// player break). Tick-owned (TICK-05).
//
//	[VERIFIED javap BlockBehaviour.onExplosionHit: DESTROY_WITH_DECAY adds LootContextParams
//	 .EXPLOSION_RADIUS(radius); getDrops runs ApplyExplosionDecay/survives_explosion at that radius.]
func (t *TickLoop) spawnExplosionDrop(pos pk.Position, brokenState block.StateID, radius float64) {
	name, ok := blockTableName(brokenState)
	if !ok {
		return // air/unknown -> no drop
	}
	tbl, err := loot.LoadTable("minecraft:blocks/" + name)
	if err != nil {
		return // no embedded table -> no drop (same miss policy as blockDropsFor)
	}
	lootSeed := rand.Int64()
	ctx := loot.NewLootContext(lootSeed, 0)
	// DESTROY_WITH_DECAY (mobExplosionDropDecay true): thread the EXPLOSION_RADIUS so the decay
	// functions fire. DESTROY (false): leave HasExplosion false -> all drops survive.
	if mobExplosionDropDecay {
		ctx.HasExplosion = true
		ctx.ExplosionRadius = float32(radius)
	}
	drops := loot.Roll(tbl, lootSeed, ctx)
	for _, drop := range drops {
		if drop.Count <= 0 {
			continue // decayed-to-zero stack: no item.
		}
		x := float64(pos.X) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
		y := float64(pos.Y) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter) - itemEntityHalfHeight
		z := float64(pos.Z) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
		ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, drop)
		t.cur().entities.add(ie)
	}
}

// explosionShuffle is the 1:1 port of net.minecraft.util.Util.shuffle(List, RandomSource): the
// Fisher-Yates that walks i from size down to 2, drawing j = random.nextInt(i) and swapping
// list[i-1] with list[j]. Uses the region levelRandom (Level.random). A nil random (a test
// without a seeded region) leaves the list unshuffled.
//
//	[VERIFIED javap Util.shuffle: for (i=size; i>1; i--) { j=random.nextInt(i); swap(i-1, j); }]
func explosionShuffle(list []pk.Position, t *TickLoop) {
	r := t.cur().levelRandom
	if r == nil {
		return
	}
	for i := len(list); i > 1; i-- {
		j := int(r.NextIntN(int32(i)))
		list[i-1], list[j] = list[j], list[i-1]
	}
}

// explosionInWorldBounds ports Level.isInWorldBounds(BlockPos): the y is inside build height
// [minY, maxY] (inclusive; overworld -64..319) AND |x|,|z| < 30_000_000 (isInWorldBoundsHorizontal).
//
//	[VERIFIED javap Level.isInWorldBounds -> isInsideBuildHeight(y in [getMinY,getMaxY]) &&
//	 isInWorldBoundsHorizontal(x,z in [-30_000_000, 30_000_000)).]
func explosionInWorldBounds(x, y, z int) bool {
	if y < dimMinY || y > maxBuildHeightY {
		return false
	}
	if x < -explosionWorldBoundHorz || z < -explosionWorldBoundHorz {
		return false
	}
	if x >= explosionWorldBoundHorz || z >= explosionWorldBoundHorz {
		return false
	}
	return true
}

// createExplosionFire is the 1:1 port of ServerExplosion.createFire(List<BlockPos>): after the block
// destruction, walk the (shuffled) toBlow list and, for each pos, roll level.random.nextInt(3) -- when
// it is 0 AND the cell is now air AND the block below is a full solid-render cube, place a fire block
// (BaseFireBlock.getState). ONE nextInt(3) is drawn per pos in list order (so it stays in lockstep with
// the vanilla RNG). Only reached when the explosion's fire flag is true (a ghast fireball with
// mobGriefing on; TNT/creeper pass fire=false so this never runs). A nil levelRandom (a bare test loop)
// draws nothing and places no fire.
//
//	[VERIFIED javap ServerExplosion.createFire: for (pos : list) if (random.nextInt(3)==0 &&
//	 getBlockState(pos).isAir() && getBlockState(pos.below()).isSolidRender())
//	 setBlockAndUpdate(pos, BaseFireBlock.getState(level, pos)).]
func (t *TickLoop) createExplosionFire(toBlow []pk.Position) {
	w := t.world()
	if w == nil {
		return
	}
	r := t.cur().levelRandom
	if r == nil {
		return // no seeded region: draw nothing (fire is a cosmetic-but-gameplay side effect; RNG untouched).
	}
	for _, pos := range toBlow {
		// nextInt(3) is drawn UNCONDITIONALLY per pos (the vanilla short-circuit tests it FIRST), so the
		// RNG advances once per list entry regardless of the air/below checks.
		if r.NextIntN(3) != 0 {
			continue
		}
		st, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsAir(st) {
			continue // getBlockState(pos).isAir(): only an air cell can catch fire.
		}
		below := pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		if !t.isSolidAt(below) {
			continue // getBlockState(pos.below()).isSolidRender(): fire needs a full solid floor.
		}
		// setBlockAndUpdate(pos, BaseFireBlock.getState(level, pos)): the default fire state for this
		// location (FireBlock.getStateForPlacement -- age 0; the soul-fire base variant is cite-deferred).
		sid, ok := t.fireStateForPlacement(pos)
		if !ok {
			continue
		}
		w.SetBlock(pos, sid, dimMinY)
		t.broadcastBlockUpdate(pos, sid)
	}
}
