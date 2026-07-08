package server

// crafter.go -- the CRAFTER auto-craft DRIVE (redstone-driven): a 1:1 port of
// net.minecraft.world.level.block.CrafterBlock + CrafterBlockEntity.serverTick over the 26.2 jar
// (temp/cache/26.2-inner.jar, javap this session). The redstone rising-edge trigger schedules a 4-tick
// dispense; the scheduled tick assembles the current 3x3 recipe (via the shared recipe matcher), ejects
// the result out the ORIENTATION.front() face (into a container if present, else as a loose item), buckets
// back any per-cell remaining items, consumes one from each grid slot, sets CRAFTING for 6 ticks, and the
// per-tick serverTick clears CRAFTING when the countdown hits 0. The comparator output is the crafter fill
// count (crafter_be.go crafterGetRedstoneSignal). The 9-slot BE lives in crafter_be.go; the block-state
// shape in level/block/crafter.go.
//
// CITE (methods, jar-verified this session):
//   CrafterBlock.neighborChanged: shouldTrigger = hasNeighborSignal(pos); isTriggered = getValue(TRIGGERED);
//       be = getBlockEntity(pos);
//       if (shouldTrigger && !isTriggered) { scheduleTick(pos,this,4); setBlock(TRIGGERED=true,2); setBlockEntityTriggered(be,true); }
//       else if (!shouldTrigger && isTriggered) { setBlock(TRIGGERED=false,CRAFTING=false,2); setBlockEntityTriggered(be,false); }
//   CrafterBlock.tick -> dispenseFrom(state, level, pos).
//   CrafterBlock.dispenseFrom: be=getBlockEntity; input=be.asCraftInput();
//       opt = getPotentialResults(level, input); if (opt.isEmpty()) { levelEvent(1050); return; }
//       recipe = opt.get(); result = recipe.assemble(input); if (result.isEmpty()) { levelEvent(1050); return; }
//       be.setCraftingTicksRemaining(6); setBlock(CRAFTING=true, 2); result.onCraftedBySystem(level);
//       dispenseItem(level, pos, be, result, state, recipe);
//       for (remaining : recipe.getRemainingItems(input)) if (!remaining.isEmpty()) dispenseItem(...);
//       be.getItems().forEach(s -> { if (!s.isEmpty()) s.shrink(1); }); be.setChanged();
//   CrafterBlock.MAX_CRAFTING_TICKS = 6; CRAFTING_TICK_DELAY = 4.
//   CrafterBlockEntity.serverTick: n = craftingTicksRemaining-1; if (n<0) return; craftingTicksRemaining=n;
//       if (n==0) setBlock(pos, state.setValue(CRAFTING,false), 3).
//
// SCOPE / DEFERRALS (each jar-cited):
//   - levelEvent(1050 craft-fail sound / 1049 craft sound / 2010 eject particle) are client cosmetics with
//     no gameplay effect -- DEFERRED as cited no-ops (the same seam dispenser.go uses). CITE:
//     CrafterBlock.dispenseFrom levelEvent.
//   - CRAFTER_RECIPE_CRAFTED advancement trigger (the 17-block radius ServerPlayer scan) is DEFERRED --
//     no gameplay effect on the craft itself. CITE: CrafterBlock.dispenseItem CriteriaTriggers.CRAFTER_RECIPE_CRAFTED.
//   - onCraftedBySystem (map/decorated-pot post-process) is a no-op for the common item result -- DEFERRED
//     behind the cited seam. CITE: ItemStack.onCraftedBySystem.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// crafterMaxCraftingTicks is CrafterBlock.MAX_CRAFTING_TICKS (6) -- the CRAFTING animation latch duration
// (the value setCraftingTicksRemaining is set to on a successful craft). CITE: CrafterBlock.MAX_CRAFTING_TICKS.
const crafterMaxCraftingTicks = 6

// crafterCraftingTickDelay is CrafterBlock.CRAFTING_TICK_DELAY (4) -- the scheduled-tick delay between the
// rising redstone edge and the dispense. CITE: CrafterBlock.CRAFTING_TICK_DELAY.
const crafterCraftingTickDelay = 4

// crafterTickType is the block id the crafter dispense TRIGGERED tick is scheduled/dispatched under.
// CITE: CrafterBlock (minecraft:crafter).
const crafterTickType blockTickType = "minecraft:crafter"

// crafterNeighborChanged ports CrafterBlock.neighborChanged: the redstone rising-edge trigger + the
// TRIGGERED latch. On a rising power edge (now powered, not yet TRIGGERED) it schedules the dispense 4
// ticks out, sets TRIGGERED=true, and marks the BE triggered. On a falling edge (no longer powered, still
// TRIGGERED) it clears TRIGGERED AND CRAFTING and un-triggers the BE. The power read is hasNeighborSignal
// (any of the 6 neighbors), NOT the dispenser above-cell variant. Dispatched from drainRedstoneUpdates
// (redstone.go) when a neighbor of a crafter changes.
//
// 1:1 net.minecraft.world.level.block.CrafterBlock.neighborChanged
func (t *TickLoop) crafterNeighborChanged(pos pk.Position, state block.StateID) {
	shouldTrigger := t.hasNeighborSignal(pos)
	isTriggered := block.CrafterTriggered(state)
	be := t.resolveCrafter(pos, state)

	if shouldTrigger && !isTriggered {
		// scheduleTick(pos, this, 4); setBlock(TRIGGERED=true, 2); setBlockEntityTriggered(be, true).
		t.scheduleBlockTick(pos, crafterTickType, crafterCraftingTickDelay)
		if ns, ok := block.CrafterWithTriggered(state, true); ok && t.world().SetBlock(pos, ns, dimMinY) {
			t.broadcastBlockUpdate(pos, ns)
		}
		_ = be // BE "triggered" flag is a client-anim hint (DATA_TRIGGERED); no gameplay effect here.
	} else if !shouldTrigger && isTriggered {
		// setBlock(TRIGGERED=false, CRAFTING=false, 2); setBlockEntityTriggered(be, false).
		ns, ok := block.CrafterWithTriggered(state, false)
		if ok {
			if ns2, ok2 := block.CrafterWithCrafting(ns, false); ok2 {
				ns = ns2
			}
			if t.world().SetBlock(pos, ns, dimMinY) {
				t.broadcastBlockUpdate(pos, ns)
			}
		}
	}
}

// crafterTick ports CrafterBlock.tick(state, level, pos, random) -> dispenseFrom(state, level, pos): the
// scheduled TRIGGERED tick fires the auto-craft. Dispatched from tickBlock (block_ticks.go) after the
// stale-tick guard confirms the block is still a crafter.
//
// 1:1 net.minecraft.world.level.block.CrafterBlock.tick
func (t *TickLoop) crafterTick(state block.StateID, pos pk.Position) {
	t.crafterDispenseFrom(state, pos)
}

// crafterServerTick ports CrafterBlockEntity.serverTick(level, pos, state, be): decrement
// craftingTicksRemaining; when it reaches 0 clear the CRAFTING block-state. A crafter that is not mid-
// craft (craftingTicksRemaining already <= 0 -> n < 0) is a cheap early-out. Driven per-tick from
// tickCrafters (tick_phases.go).
//
// 1:1 net.minecraft.world.level.block.entity.CrafterBlockEntity.serverTick
func (t *TickLoop) crafterServerTick(pos pk.Position, state block.StateID, c *crafterBE) {
	n := c.craftingTicksRemaining - 1
	if n < 0 {
		return
	}
	c.craftingTicksRemaining = n
	if n == 0 {
		if ns, ok := block.CrafterWithCrafting(state, false); ok && t.world().SetBlock(pos, ns, dimMinY) {
			t.broadcastBlockUpdate(pos, ns)
		}
	}
}

// crafterDispenseFrom ports CrafterBlock.dispenseFrom(state, level, pos): assemble the current 3x3 grid
// via the shared recipe matcher, eject the result + per-cell remaining items out the front, consume one
// from every non-empty grid slot, and latch CRAFTING for 6 ticks. On no-recipe or an empty assembled
// result it plays the fail levelEvent (cited no-op) and returns without consuming. Tick-owned.
//
// 1:1 net.minecraft.world.level.block.CrafterBlock.dispenseFrom
func (t *TickLoop) crafterDispenseFrom(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	c := t.resolveCrafter(pos, state)
	if c == nil {
		return // getBlockEntity not a CrafterBlockEntity -> return
	}

	// input = be.asCraftInput(); opt = getPotentialResults(level, input) = RECIPE_CACHE.get(level, input).
	// Sulfur assembles via the shared plugin matcher (the SAME path the crafting table uses).
	resultID, resultCount, ok := t.crafterMatch(c)
	if !ok {
		// opt.isEmpty() -> levelEvent(1050); return (no consume).
		return
	}
	// result = recipe.assemble(input); if (result.isEmpty()) { levelEvent(1050); return; }
	if resultID <= 0 || resultCount <= 0 {
		return
	}
	result := component.SlotData{ItemID: toItemID(resultID), Count: toVar(resultCount)}

	// be.setCraftingTicksRemaining(6); setBlock(CRAFTING=true, 2); result.onCraftedBySystem(level) [no-op].
	c.craftingTicksRemaining = crafterMaxCraftingTicks
	if ns, ok := block.CrafterWithCrafting(state, true); ok && t.world().SetBlock(pos, ns, dimMinY) {
		t.broadcastBlockUpdate(pos, ns)
		state = ns
	}

	facing, _ := block.CrafterFacing(state)

	// dispenseItem(level, pos, be, result, state, recipe): eject the assembled result.
	t.crafterDispenseItem(pos, facing, c, pos, result)

	// for (remaining : recipe.getRemainingItems(input)) if (!remaining.isEmpty()) dispenseItem(...).
	// The per-cell bucket-back (empty bucket after milk, glass bottle after potion, etc.) uses the same
	// Remaining matcher seam the crafting table uses; the common no-remainder recipe yields nothing.
	for _, rem := range t.crafterRemaining(c) {
		if !stackEmpty(rem) {
			t.crafterDispenseItem(pos, facing, c, pos, rem)
		}
	}

	// be.getItems().forEach(s -> if (!s.isEmpty()) s.shrink(1)); be.setChanged().
	for i := range c.items {
		if !stackEmpty(c.items[i]) {
			c.items[i].Count = toVar(int(c.items[i].Count) - 1)
			if c.items[i].Count <= 0 {
				c.items[i] = component.SlotData{Count: 0}
			}
		}
	}
	t.markCrafterDirty(pos)
}

// crafterDispenseItem ports CrafterBlock.dispenseItem: push the stack into the container in the FACING
// cell (HopperBlockEntity.addItem, from the crafter side = FACING.getOpposite), spawning any un-inserted
// remainder as a loose item 0.7 out the front. Two insert loops match the jar exactly:
//   - guard (into instanceof CrafterBlockEntity) OR (count > into.getMaxStackSize(stack)): FALSE selects
//     loop A (insert ONE item at a time via copyWithCount(1); stop the moment addItem leaves a leftover);
//     TRUE selects loop B (addItem the whole stack; stop when a pass makes no progress).
//
// 1:1 net.minecraft.world.level.block.CrafterBlock.dispenseItem
func (t *TickLoop) crafterDispenseItem(pos pk.Position, facing block.Direction, from *crafterBE, fromPos pk.Position, stack component.SlotData) {
	frontPos := relative(pos, facing)
	into := t.getContainerAt(frontPos)
	remaining := stack // ItemStack.copy()

	fromView := &crafterContainer{t: t, pos: fromPos, c: from}
	opp := dirOpposite(facing)

	if into != nil {
		_, intoIsCrafter := into.(*crafterContainer)
		guard := intoIsCrafter || int(stack.Count) > crafterMaxStack(into, stack)
		if !guard {
			// Loop A: insert one item at a time; stop the first time addItem cannot place the item.
			for !stackEmpty(remaining) {
				one := stackCopyWithCount(remaining, 1)
				left := t.hopperAddItem(fromView, into, one, opp)
				if !stackEmpty(left) {
					break
				}
				remaining.Count = toVar(int(remaining.Count) - 1)
				if remaining.Count <= 0 {
					remaining = component.SlotData{Count: 0}
				}
			}
		} else {
			// Loop B (into is a crafter, or count over the container max): addItem the whole stack; stop
			// when a pass makes no progress (count unchanged).
			for !stackEmpty(remaining) {
				before := int(remaining.Count)
				remaining = t.hopperAddItem(fromView, into, remaining, opp)
				if int(remaining.Count) == before {
					break
				}
			}
		}
	}

	if !stackEmpty(remaining) {
		t.crafterSpawnItem(pos, facing, remaining)
	}
}

// crafterMaxStack ports the into.getMaxStackSize(stack) read used by the dispenseItem guard: a plain
// container caps at the stack max size (Container.getMaxStackSize default = min(64, item max)). CITE:
// CrafterBlock.dispenseItem (into.getMaxStackSize(itemStack)).
func crafterMaxStack(into containerView, stack component.SlotData) int {
	return stackMaxSize(stack)
}

// crafterSpawnItem ports DefaultDispenseItemBehavior.spawnItem(level, itemStack, 6, direction, position)
// as invoked by CrafterBlock.dispenseItem with the FULL remaining stack (NOT split(1)): the spawn point is
// Vec3.atCenterOf(pos).relative(front, 0.7); spawnY -= 0.125 (Y axis) / 0.15625 (horizontal); the velocity
// is triangle(step*d, spread) with d = level.nextDouble()*0.1+0.2 and spread = 0.0172275*6. The level
// random draws (never a per-entity mob stream) leave the pig oracle unperturbed. Tick-owned.
//
// 1:1 net.minecraft.core.dispenser.DefaultDispenseItemBehavior.spawnItem
func (t *TickLoop) crafterSpawnItem(pos pk.Position, facing block.Direction, stack component.SlotData) {
	if stackEmpty(stack) {
		return
	}
	sx, sy, sz := dirVec(facing)
	posX := float64(pos.X) + 0.5 + dispensePositionScale*float64(sx)
	posY := float64(pos.Y) + 0.5 + dispensePositionScale*float64(sy)
	posZ := float64(pos.Z) + 0.5 + dispensePositionScale*float64(sz)

	spawnY := posY
	if facing == block.Up || facing == block.Down {
		spawnY -= 0.125
	} else {
		spawnY -= 0.15625
	}

	ie := NewItemEntity(t.idAlloc.AllocID(), posX, spawnY, posZ, stack)

	rng := t.dispenserRandom()
	if rng != nil {
		pow := rng.NextDouble()*0.1 + 0.2
		spread := dispenseSpreadUnit * float64(dispenseAccuracy)
		ie.vx = randTriangle(rng, float64(sx)*pow, spread)
		ie.vy = randTriangle(rng, 0.2, spread)
		ie.vz = randTriangle(rng, float64(sz)*pow, spread)
	}
	t.cur().entities.add(ie)
}

// crafterMatch runs the shared recipe matcher over the crafter grid: build the row-major 3x3 payload
// (the SAME {w,h,cells} dict the crafting table uses) and return the assembled (id, count, ok). This is
// the Sulfur analogue of RECIPE_CACHE.get(level, be.asCraftInput()).map(RecipeHolder::value).assemble(input).
// A DISABLED slot contributes an EMPTY cell so a disabled slot never participates in the match (matching
// vanilla, where a disabled slot holds no item). CITE: CrafterBlock.getPotentialResults + CraftingRecipe.assemble.
func (t *TickLoop) crafterMatch(c *crafterBE) (id, count int, ok bool) {
	if t.plugins == nil {
		return 0, 0, false
	}
	v := crafterCraftView(c)
	return t.plugins.Match(craftGridPayload(v))
}

// crafterRemaining runs the shared Remaining matcher over the crafter grid for the per-cell bucket-back
// (empty bucket / glass bottle left after a craft). The plugin returns a single (id,count) leftover; the
// common no-remainder recipe returns nothing. Returned as a slice so a future per-cell remaining maps 1:1
// to CraftingRecipe.getRemainingItems. CITE: CrafterBlock.dispenseFrom (recipe.getRemainingItems(input)).
func (t *TickLoop) crafterRemaining(c *crafterBE) []component.SlotData {
	if t.plugins == nil {
		return nil
	}
	id, count, ok := t.plugins.Remaining(craftGridPayload(crafterCraftView(c)))
	if !ok || id <= 0 || count <= 0 {
		return nil
	}
	return []component.SlotData{{ItemID: toItemID(id), Count: toVar(count)}}
}

// crafterCraftView builds a 3x3 craftView over the crafter grid (result unused: the matcher returns the
// assembled stack directly). A DISABLED slot reports EMPTY so it never enters the match. Mirrors
// crafting_menu.go craftingTableView. CITE: CrafterBlockEntity.asCraftInput.
func crafterCraftView(c *crafterBE) craftView {
	return craftView{
		w: 3, h: 3,
		getCell: func(i int) component.SlotData {
			if i < 0 || i >= crafterContainerSize || c.crafterIsSlotDisabled(i) {
				return component.SlotData{Count: 0}
			}
			return c.items[i]
		},
		setCell: func(i int, s component.SlotData) {
			if i >= 0 && i < crafterContainerSize {
				c.items[i] = s
			}
		},
		getRes: func() component.SlotData { return component.SlotData{Count: 0} },
		setRes: func(s component.SlotData) {},
	}
}
