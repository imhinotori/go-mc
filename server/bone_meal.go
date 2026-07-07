package server

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bone_meal.go -- BONE MEAL on CROPS, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.item.BoneMealItem + net.minecraft.world.level.block.CropBlock). A player
// right-clicking a growable crop (wheat/carrots/potatoes/beetroots) with a bone_meal item ages it by
// the vanilla random amount (clamped to MAX_AGE), consumes 1 bone meal (creative keeps it), and
// spawns the happy_villager particle burst. This is a useOn-block action, so it hooks the
// handleUseItemOn (block) path BEFORE block placement -- bone meal is not a block item.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//   BoneMealItem.useOn: pos=getClickedPos; if growCrop(stack,level,pos) -> (server) levelEvent(1505,pos,15)
//     -> return SUCCESS_SERVER; else isFaceSturdy -> growWaterPlant (follow-up) else PASS.
//   BoneMealItem.growCrop(stack,level,pos): state=getBlockState(pos); if block is BonemealableBlock bmb
//     and bmb.isValidBonemealTarget(level,pos,state): if ServerLevel: if bmb.isBonemealSuccess(...)
//     bmb.performBonemeal(sl,getRandom(),pos,state); stack.shrink(1); return true; else return false.
//   CropBlock.isValidBonemealTarget: return !isMaxAge(state).
//   CropBlock.isBonemealSuccess: return true (ALWAYS for crops -- no RNG gate).
//   CropBlock.performBonemeal -> growCrops(level,pos,state):
//     age = Math.min(getMaxAge(), getAge(state) + getBonemealAgeIncrease(level));
//     level.setBlock(pos, getStateForAge(age), 2);  // flag 2 == UPDATE_CLIENTS.
//   CropBlock.getBonemealAgeIncrease(level): return Mth.nextInt(getRandom(), 2, 5).
//   Mth.nextInt(r,lo,hi): lo>=hi ? lo : r.nextInt(hi-lo+1)+lo;  // (2,5) -> r.nextInt(4)+2 -> 2..5.
//
// RNG: getBonemealAgeIncrease draws level.getRandom() (this.random == the owning region levelRandom)
// EXACTLY ONCE -- one nextInt(4) yielding 2..5. The 2 and 5 are FIXED bytecode constants (iconst_2 /
// iconst_5), independent of the crop family, so beetroot (MAX_AGE 3) uses the same 2..5 span (clamped
// by Math.min to 3). This draw is GATED behind having bone_meal in hand and clicking a sub-max-age crop
// -- a state the pig-oracle player never reaches -- so the levelRandom stream the pig oracle pins is
// unperturbed. CITE: Mth.nextInt(RandomSource,int,int).
//
// FOLLOW-UPS (cited, not ported here): growWaterPlant (seagrass/coral spread over water on a sturdy
// face), saplings (SaplingBlock/TreeGrower.performBonemeal -- tree grow), grass-block spread
// (GrassBlock.performBonemeal via BonemealableBlock). This deliverable is the CropBlock family; those
// land as additional target arms once their BonemealableBlock ports exist.

// boneMealItemID is the bone_meal item registry id (data/item/item.go: 1111). CITE: item.BoneMeal.
const boneMealItemID = 1111

// boneMealAgeIncreaseLo / boneMealAgeIncreaseHi are the FIXED (lo,hi) span of
// CropBlock.getBonemealAgeIncrease == Mth.nextInt(random, 2, 5) -- literal bytecode constants
// (iconst_2 / iconst_5), independent of crop family. CITE: CropBlock.getBonemealAgeIncrease.
const (
	boneMealAgeIncreaseLo = 2
	boneMealAgeIncreaseHi = 5
)

// happyVillagerParticle is the particle vanilla levelEvent(1505,pos,15) resolves to on the client
// (LevelRenderer.levelEvent case 1505 -> HAPPY_VILLAGER). Sulfur has no levelEvent bus for a block
// edit, so the observable effect is reproduced via ServerLevel.sendParticles (spawnParticle,
// particles.go) -- a jar-faithful re-expression of the same happy_villager burst at the crop.
// CITE: LevelRenderer.levelEvent (1505) -> HAPPY_VILLAGER.
const happyVillagerParticle = "minecraft:happy_villager"

// tryBoneMealCrop is the crop half of BoneMealItem.useOn -> growCrop, hooked in handleUseItemOn BEFORE
// block placement. Returns true when the held item is bone_meal AND the CLICKED block is a growable
// crop below MAX_AGE (isValidBonemealTarget) -- the use consumed the action (for crops isBonemealSuccess
// is always true, so a consumed use always advances the crop). Returns false when the held item is not
// bone_meal OR the clicked block is not a sub-max-age crop, so placement continues (a no-op for the
// non-block bone_meal item, matching vanilla PASS).
//
// Runs on the tick goroutine. The age-increase draw + SetBlock run in the region OWNING the crop column
// (withRegion + t.cur().levelRandom), matching the crop-growth port this.random. The bone-meal consume
// (creative-gated) runs after, on the region-independent inventory. CITE: BoneMealItem.useOn/growCrop.
func (t *TickLoop) tryBoneMealCrop(p *tickPlayer, inv *Inventory, held component.SlotData, pos pk.Position) bool {
	if slotIsEmpty(held) || int32(held.ItemID) != int32(boneMealItemID) {
		return false // not bone meal -> not this path; placement continues
	}
	pmgr := t.dimWorld(p)
	pMinY := dimMinYFor(p.dimension)
	if pmgr == nil {
		return false
	}
	// growCrop: state = getBlockState(pos); block is BonemealableBlock. An unreadable/unloaded cell
	// reads as not-a-crop (getBlockState on an unloaded chunk returns air) -> false.
	state, ok := pmgr.GetBlock(pos, pMinY)
	if !ok || !block.IsCrop(state) {
		return false
	}
	// CropBlock.isValidBonemealTarget: !isMaxAge(state). A max-age crop is NOT a valid target -- growCrop
	// returns false and NOTHING is consumed (the use falls through to placement, a no-op for bone meal).
	age := block.CropAge(state)
	maxAge := block.CropMaxAge(state)
	if age < 0 || maxAge < 0 || age >= maxAge {
		return false
	}
	// Reach gate (server-authoritative): bone meal is an on-block interaction like every other useOn.
	if !t.withinReach(p, pos) {
		return false
	}

	// The use is CONSUMED (isValidBonemealTarget held). Run the RNG draw + mutation in the region OWNING
	// the crop column so t.cur().levelRandom resolves to that region this.random (not blindly region 0).
	t.withRegion(t.regionForColumn(columnOf(float64(pos.X)+0.5, float64(pos.Z)+0.5)), func() {
		// CropBlock.getBonemealAgeIncrease: Mth.nextInt(random, 2, 5) == random.nextInt(4) + 2 (2..5).
		inc := boneMealAgeIncreaseLo
		if r := t.cur(); r != nil && r.levelRandom != nil {
			// Mth.nextInt(r,lo,hi): lo>=hi ? lo : r.nextInt(hi-lo+1)+lo. Here hi(5)>lo(2), so one nextInt(4)
			// draw. A bare test loop with no seeded levelRandom falls back to the low bound (2), so the use
			// still consumes + advances faithfully in the degenerate case.
			inc = int(r.levelRandom.NextIntN(int32(boneMealAgeIncreaseHi-boneMealAgeIncreaseLo+1))) + boneMealAgeIncreaseLo
		}
		// growCrops: age = Math.min(getMaxAge(), getAge(state)+inc); setBlock(getStateForAge(age), 2).
		newAge := age + inc
		if newAge > maxAge {
			newAge = maxAge // Math.min(getMaxAge(), ...)
		}
		grown, ok := block.CropStateForAge(state, newAge)
		if !ok {
			return
		}
		// setBlock(pos, getStateForAge(age), 2): flag 2 == UPDATE_CLIENTS, mirrored as SetBlock +
		// broadcastBlockUpdate -- the same flag-2 shape the crop random-tick port uses.
		if pmgr.SetBlock(pos, grown, pMinY) {
			t.broadcastBlockUpdate(pos, grown)
		}
	})

	// BoneMealItem.useOn success tail (server side): level.levelEvent(1505, pos, 15) -- the happy_villager
	// burst. Re-expressed via ServerLevel.sendParticles: 15 happy_villager particles spread over the crop
	// 1x1 cell, centered, matching the client case-1505 rendering. Count 15 is the levelEvent data byte.
	t.spawnParticle(happyVillagerParticle, false, false,
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		0.3, 0.3, 0.3, 0, 15)

	// growCrop tail: stack.shrink(1) -- UNCONDITIONAL in growCrop, but the outer
	// ServerPlayerGameMode.useItemOn wraps the creative path in a count save/restore, so creative keeps
	// the item. Sulfur collapses that to shrink-in-survival / keep-in-creative (same gate the block
	// placement consume uses). CITE: ServerPlayerGameMode.useItemOn (hasInfiniteMaterials count guard).
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, inv)
	}
	return true
}
