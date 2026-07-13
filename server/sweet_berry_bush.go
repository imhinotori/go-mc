package server

import (
	"math/rand/v2"
	"strconv"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/loot"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// sweet_berry_bush.go -- SweetBerryBushBlock.useWithoutItem 1:1 (temp/cache/26.2-inner.jar,
// javap-verified this session). A right-click (use item on block) on a sweet berry bush harvests
// the berries: below AGE 2 it PASSes (the VegetationBlock super, a no-op), at AGE 2 or 3 it drops
// the harvest loot, plays the pick sound, resets AGE to 1, and posts BLOCK_CHANGE. This routes
// through the block-use dispatch seam (useBlockInteraction in chest_open.go), so it runs at
// ServerPlayerGameMode.useItemOn STEP 1 (the block's own interaction) BEFORE any item placement.
//
// THE 1:1 PORT (javap SweetBerryBushBlock.useWithoutItem):
//
//	protected InteractionResult useWithoutItem(state, level, pos, player, hit) {
//	    int age = state.getValue(AGE);
//	    if (age <= 1) return super.useWithoutItem(state, level, pos, player, hit);  // PASS (VegetationBlock)
//	    if (level instanceof ServerLevel sl) {
//	        Block.dropFromBlockInteractLootTable(sl, BuiltInLootTables.HARVEST_SWEET_BERRY_BUSH,
//	            state, level.getBlockEntity(pos), null, player,
//	            (l, stack) -> Block.popResource(l, pos, stack));                    // popResource per drop
//	        sl.playSound(null, pos, SoundEvents.SWEET_BERRY_BUSH_PICK_BERRIES, SoundSource.BLOCKS,
//	            1.0F, 0.8F + sl.getRandom().nextFloat() * 0.4F);
//	        BlockState reset = state.setValue(AGE, 1);
//	        sl.setBlock(pos, reset, 2);                                             // UPDATE_CLIENTS
//	        sl.gameEvent(GameEvent.BLOCK_CHANGE, pos, GameEvent.Context.of(player, reset));
//	    }
//	    return InteractionResult.SUCCESS;                                          // CONSUMES -> no place
//	}
//
// RNG (task-confirmed, pig oracle UNAFFECTED): the harvest draws are level.getRandom() draws (the
// loot roll's set_count uniform + the sound pitch nextFloat + the popResource jitter nextDoubles), NOT
// a per-entity/pig stream. Sulfur reuses the per-region levelRandom (Level.getRandom analogue, the
// same non-gameplay source the bone-meal/composter/anvil/... paths already draw from) for the roll +
// jitter + pitch; the ClientboundSound wire seed is the dedicated sound-seed generator (rand.Int64 --
// NEVER reseeded onto the levelRandom, so it cannot perturb the stream the pig oracle pins). The AGE
// gate is `age <= 1` (harvest at age 2 and 3).

// sweetBerryHarvestTable is BuiltInLootTables.HARVEST_SWEET_BERRY_BUSH.
const sweetBerryHarvestTable = "minecraft:harvest/sweet_berry_bush"

// sweetBerryPickSoundID is SoundEvents.SWEET_BERRY_BUSH_PICK_BERRIES -- index into the SoundEvent
// registry (data/registryid/soundevent.go "minecraft:block.sweet_berry_bush.pick_berries" == 1617).
const sweetBerryPickSoundID = 1617

// harvestSweetBerryBush ports SweetBerryBushBlock.useWithoutItem. It is called from the block-use
// dispatch (useBlockInteraction) when the clicked block is a sweet_berry_bush. Returns true when the
// interaction CONSUMED the action (SUCCESS -> no placement), which is EVERY sweet-berry-bush click
// (vanilla returns SUCCESS for age <= 1 too, via the VegetationBlock super whose base
// BlockBehaviour.useWithoutItem returns PASS -- but a right-click on ANY bush must not fall through to
// place the held block on top; see below). Runs on the tick goroutine over the shared world.
func (t *TickLoop) harvestSweetBerryBush(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	age := block.SweetBerryAge(state)
	if age < 0 {
		return false // not a sweet berry bush (defensive) -> PASS, placement continues.
	}
	// `if (age <= 1) return super.useWithoutItem(...)`. The VegetationBlock/BlockBehaviour super
	// returns InteractionResult.PASS (a no-op) -- the action was NOT consumed, so placement continues
	// exactly as vanilla's useItemOn continuation (a PASS block hook falls through to the item's useOn).
	if age <= 1 {
		return false
	}
	w := t.world()
	if w == nil {
		return false
	}
	// dropFromBlockInteractLootTable(HARVEST_SWEET_BERRY_BUSH, state, be=null-ish, tool=null,
	// interactingEntity=player, popResource per drop). The BLOCK_STATE param carries the clicked
	// bush's AGE so the loot table's pool-1 {age:"3"} condition resolves (age 3 -> +1 guaranteed berry;
	// age 2 -> only the uniform(1,2) pool). The roll draws from the region's levelRandom
	// (Level.getRandom analogue) -- the SAME non-gameplay stream the rest of the region's draws come
	// from, so the pig per-entity RNG stream is untouched (the harvest handler NEVER seeds its own RNG;
	// the loot engine reads the rng through the context's Random() getter, and the Roll `seed`
	// argument is IGNORED when ctx is non-nil).
	rng := t.cur().levelRandom
	ctx := loot.NewBlockInteractLootContextWithSource(rng, "minecraft:sweet_berry_bush", map[string]string{"age": strconv.Itoa(age)})
	tbl, err := loot.LoadTable(sweetBerryHarvestTable)
	if err == nil {
		drops := loot.Roll(tbl, 0, ctx)
		for _, drop := range drops {
			if drop.Count <= 0 {
				continue
			}
			// Block.popResource: block center + per-axis Mth.nextDouble(-0.25,0.25) jitter, Y offset down
			// by ITEM.getHeight()/2 (0.125). Reuses the block-break popResource geometry (block_drop.go);
			// the jitter draws come from the same levelRandom (Level.getRandom analogue) as the roll.
			x := float64(pos.X) + 0.5 + rng.NextDouble()*(2*itemSpawnJitter) - itemSpawnJitter
			y := float64(pos.Y) + 0.5 + rng.NextDouble()*(2*itemSpawnJitter) - itemSpawnJitter - itemEntityHalfHeight
			z := float64(pos.Z) + 0.5 + rng.NextDouble()*(2*itemSpawnJitter) - itemSpawnJitter
			if t.cur() != nil && t.idAlloc != nil {
				ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, drop)
				t.cur().entities.add(ie)
			}
		}
	}

	// sl.playSound(null, pos, SWEET_BERRY_BUSH_PICK_BERRIES, BLOCKS, 1.0F, 0.8F + random.nextFloat()*0.4F).
	// The pitch draws level.getRandom().nextFloat(); mirror it with the same levelRandom the rest of
	// this handler uses. The ClientboundSound wire seed is the dedicated sound-seed generator
	// (rand.Int64() -- NOT the levelRandom), matching Level.soundSeedGenerator; a single draw off
	// math/rand/v2 cannot perturb any gameplay stream (the per-tick sound seed is intentionally NOT
	// reseeded onto the levelRandom, so it never perturbs the pig oracle).
	pitch := 0.8 + rng.NextFloat()*0.4
	t.playSound(sweetBerryPickSoundID, soundSourceBlocks,
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 1.0, pitch, rand.Int64())

	// state.setValue(AGE, 1); sl.setBlock(pos, reset, 2) (UPDATE_CLIENTS) -> SetBlock + broadcast.
	if reset, ok := block.SweetBerryWithAge(state, 1); ok {
		if w.SetBlock(pos, reset, dimMinY) {
			t.broadcastBlockUpdate(pos, reset)
		}
		// sl.gameEvent(GameEvent.BLOCK_CHANGE, pos, GameEvent.Context.of(player, reset)): the harvest is a
		// player-sourced in-place state change. Post onto the tick-owned game-event bus (a Warden vibration
		// listener gains anger). CITE SweetBerryBushBlock.useWithoutItem (gameEvent BLOCK_CHANGE).
		t.gameEventAt(geBlockChange, pos, gameEventContext{sourceEntityID: p.entityID, affectedState: int(reset)})
	}
	// InteractionResult.SUCCESS -> consumes the action; no block is placed on the bush.
	return true
}
