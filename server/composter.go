package server

// composter.go -- the COMPOSTER utility block, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.ComposterBlock). Full vanilla lifecycle:
//
//   - LEVEL 0..8 IntegerProperty; useItemOn feeds a COMPOSTABLE item (per-item float chance) which,
//     on a successful RNG roll (or the level==0 && chance>0 free-fill short-circuit), bumps LEVEL by 1;
//   - at LEVEL 7 a scheduled tick (delay 20) cycles LEVEL 7 -> 8 (READY);
//   - useWithoutItem on a LEVEL-8 composter extractProduce: spawns a bone_meal ItemEntity and empties
//     the composter back to LEVEL 0;
//   - getAnalogOutputSignal == LEVEL (0..8) for the comparator.
//
// VANILLA CALL CHAIN (ComposterBlock, javap -c -p this session):
//
//	useItemOn: int lvl = getValue(LEVEL); if (lvl < 8 && COMPOSTABLES.containsKey(item)) {
//	    if (lvl < 7 && !isClientSide) { ns = addItem(...); levelEvent(1500, pos, ns!=state?1:0);
//	        awardStat(...); stack.consume(1, player); } return SUCCESS; } return super.useItemOn(...);
//	useWithoutItem: if (getValue(LEVEL) == 8) { extractProduce(...); return SUCCESS; } return PASS;
//	addItem: float chance = COMPOSTABLES.getFloat(item);
//	    if ((lvl != 0 || chance <= 0) && random.nextDouble() >= (double)chance) return state;
//	    int nl = lvl+1; ns = setValue(LEVEL, nl); setBlock(pos, ns, 3); gameEvent(BLOCK_CHANGE);
//	    if (nl == 7) scheduleTick(pos, this, 20); return ns;
//	extractProduce: if (!isClientSide) { Vec3 v = atLowerCornerWithOffset(pos, .5, 1.01, .5)
//	    .offsetRandomXZ(random, 0.7); ItemEntity ie = new ItemEntity(level, v.x, v.y, v.z,
//	    new ItemStack(BONE_MEAL)); ie.setDefaultPickUpDelay(); addFreshEntity(ie); }
//	    empty(...); playSound(COMPOSTER_EMPTY); return ns;
//	empty: ns = setValue(LEVEL, 0); setBlock(pos, ns, 3); gameEvent(BLOCK_CHANGE); return ns;
//	tick: if (getValue(LEVEL) == 7) { setBlock(pos, cycle(LEVEL), 3); playSound(COMPOSTER_READY); }
//	hasAnalogOutputSignal = true; getAnalogOutputSignal = getValue(LEVEL);
//
// RNG: addItem draws EXACTLY ONE nextDouble -- and ONLY when NOT the (lvl==0 && chance>0) free-fill
// short-circuit. extractProduce draws two nextFloat (offsetRandomXZ) on the SERVER before empty(). Both
// use the region levelRandom (Level.random), never a per-entity stream; composting is a PLAYER
// right-click (never serverAiStep), so the pig oracle is untouched.

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	composterMinLevel = 0
	composterMaxLevel = 8
	composterReady    = 8
)

// composterReadyDelay is the addItem scheduleTick delay (bipush 20) firing the 7 -> 8 cycle. CITE:
// ComposterBlock.addItem (scheduleTick(pos, this, 20)).
const composterReadyDelay = 20

// composterTickType is the block id composter READY ticks schedule/dispatch under.
const composterTickType blockTickType = "minecraft:composter"

// composterLevelEventFill is levelEvent 1500 (fill-attempt particle; data 1 on a bump, 0 on a rejected
// roll). Client-visual; no ClientboundLevelEvent wire yet, so this is a cited no-op (the level bump is
// broadcast via the block update). CITE: ComposterBlock.useItemOn (levelEvent(1500, pos, added?1:0)).
const composterLevelEventFill = 1500

// composterCompostables is ComposterBlock.COMPOSTABLES: the per-item compost float chance keyed by the
// item's resource-location name (the stable identity of the Object2FloatMap<ItemLike> key). Transcribed
// 1:1 from ComposterBlock.bootStrap in the jar INSERTION ORDER (chances 0.3 / 0.5 / 0.65 / 0.85 / 1.0).
// A missing key yields the default -1.0 (containsKey false). dry_short_grass / dry_tall_grass are in the
// jar map but absent from data/item (block-only in this build), so those two entries are inert but kept
// for a faithful transcription. CITE: ComposterBlock.bootStrap.
var composterCompostables = map[string]float32{
	"minecraft:jungle_leaves":           0.3,
	"minecraft:oak_leaves":              0.3,
	"minecraft:spruce_leaves":           0.3,
	"minecraft:dark_oak_leaves":         0.3,
	"minecraft:pale_oak_leaves":         0.3,
	"minecraft:acacia_leaves":           0.3,
	"minecraft:cherry_leaves":           0.3,
	"minecraft:birch_leaves":            0.3,
	"minecraft:azalea_leaves":           0.3,
	"minecraft:mangrove_leaves":         0.3,
	"minecraft:oak_sapling":             0.3,
	"minecraft:spruce_sapling":          0.3,
	"minecraft:birch_sapling":           0.3,
	"minecraft:jungle_sapling":          0.3,
	"minecraft:acacia_sapling":          0.3,
	"minecraft:cherry_sapling":          0.3,
	"minecraft:dark_oak_sapling":        0.3,
	"minecraft:pale_oak_sapling":        0.3,
	"minecraft:mangrove_propagule":      0.3,
	"minecraft:beetroot_seeds":          0.3,
	"minecraft:dried_kelp":              0.3,
	"minecraft:short_grass":             0.3,
	"minecraft:kelp":                    0.3,
	"minecraft:melon_seeds":             0.3,
	"minecraft:pumpkin_seeds":           0.3,
	"minecraft:seagrass":                0.3,
	"minecraft:sweet_berries":           0.3,
	"minecraft:glow_berries":            0.3,
	"minecraft:wheat_seeds":             0.3,
	"minecraft:moss_carpet":             0.3,
	"minecraft:pale_moss_carpet":        0.3,
	"minecraft:pale_hanging_moss":       0.3,
	"minecraft:pink_petals":             0.3,
	"minecraft:wildflowers":             0.3,
	"minecraft:leaf_litter":             0.3,
	"minecraft:small_dripleaf":          0.3,
	"minecraft:hanging_roots":           0.3,
	"minecraft:mangrove_roots":          0.3,
	"minecraft:torchflower_seeds":       0.3,
	"minecraft:pitcher_pod":             0.3,
	"minecraft:firefly_bush":            0.3,
	"minecraft:bush":                    0.3,
	"minecraft:cactus_flower":           0.3,
	"minecraft:dry_short_grass":         0.3,
	"minecraft:dry_tall_grass":          0.3,
	"minecraft:dried_kelp_block":        0.5,
	"minecraft:tall_grass":              0.5,
	"minecraft:flowering_azalea_leaves": 0.5,
	"minecraft:cactus":                  0.5,
	"minecraft:sugar_cane":              0.5,
	"minecraft:vine":                    0.5,
	"minecraft:nether_sprouts":          0.5,
	"minecraft:weeping_vines":           0.5,
	"minecraft:twisting_vines":          0.5,
	"minecraft:melon_slice":             0.5,
	"minecraft:glow_lichen":             0.5,
	"minecraft:sea_pickle":              0.65,
	"minecraft:lily_pad":                0.65,
	"minecraft:pumpkin":                 0.65,
	"minecraft:carved_pumpkin":          0.65,
	"minecraft:melon":                   0.65,
	"minecraft:apple":                   0.65,
	"minecraft:beetroot":                0.65,
	"minecraft:carrot":                  0.65,
	"minecraft:cocoa_beans":             0.65,
	"minecraft:potato":                  0.65,
	"minecraft:wheat":                   0.65,
	"minecraft:brown_mushroom":          0.65,
	"minecraft:red_mushroom":            0.65,
	"minecraft:mushroom_stem":           0.65,
	"minecraft:crimson_fungus":          0.65,
	"minecraft:warped_fungus":           0.65,
	"minecraft:nether_wart":             0.65,
	"minecraft:crimson_roots":           0.65,
	"minecraft:warped_roots":            0.65,
	"minecraft:shroomlight":             0.65,
	"minecraft:dandelion":               0.65,
	"minecraft:poppy":                   0.65,
	"minecraft:blue_orchid":             0.65,
	"minecraft:allium":                  0.65,
	"minecraft:azure_bluet":             0.65,
	"minecraft:red_tulip":               0.65,
	"minecraft:orange_tulip":            0.65,
	"minecraft:white_tulip":             0.65,
	"minecraft:pink_tulip":              0.65,
	"minecraft:oxeye_daisy":             0.65,
	"minecraft:cornflower":              0.65,
	"minecraft:lily_of_the_valley":      0.65,
	"minecraft:wither_rose":             0.65,
	"minecraft:open_eyeblossom":         0.65,
	"minecraft:closed_eyeblossom":       0.65,
	"minecraft:fern":                    0.65,
	"minecraft:sunflower":               0.65,
	"minecraft:lilac":                   0.65,
	"minecraft:rose_bush":               0.65,
	"minecraft:peony":                   0.65,
	"minecraft:large_fern":              0.65,
	"minecraft:spore_blossom":           0.65,
	"minecraft:azalea":                  0.65,
	"minecraft:moss_block":              0.65,
	"minecraft:pale_moss_block":         0.65,
	"minecraft:big_dripleaf":            0.65,
	"minecraft:hay_block":               0.85,
	"minecraft:brown_mushroom_block":    0.85,
	"minecraft:red_mushroom_block":      0.85,
	"minecraft:nether_wart_block":       0.85,
	"minecraft:warped_wart_block":       0.85,
	"minecraft:flowering_azalea":        0.85,
	"minecraft:bread":                   0.85,
	"minecraft:baked_potato":            0.85,
	"minecraft:cookie":                  0.85,
	"minecraft:torchflower":             0.85,
	"minecraft:pitcher_plant":           0.85,
	"minecraft:cake":                    1.0,
	"minecraft:pumpkin_pie":             1.0,
}

// composterChance returns the COMPOSTABLES chance for an item name and whether the item is a key
// (containsKey). CITE: Object2FloatMap.containsKey / getFloat.
func composterChance(itemName string) (float32, bool) {
	c, ok := composterCompostables[itemName]
	return c, ok
}

// composterLevel reads LEVEL (0..8) of a composter state, or (0,false) for a non-composter. Source:
// block.StateList (block.Composter{Level Integer}).
func composterLevel(s block.StateID) (int, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return 0, false
	}
	if c, ok := block.StateList[s].(block.Composter); ok {
		return int(c.Level), true
	}
	return 0, false
}

// composterStateAt returns the composter state id for a LEVEL (0..8). Source:
// block.ToStateID[block.Composter{Level Integer}].
func composterStateAt(level int) (block.StateID, bool) {
	s, ok := block.ToStateID[block.Composter{Level: block.Integer(level)}]
	return s, ok
}

// isComposterBlock reports whether a state is a composter (any LEVEL).
func isComposterBlock(s block.StateID) bool {
	_, ok := composterLevel(s)
	return ok
}

// useComposter is the useBlockInteraction dispatch for a composter (block_interact.go seam). It merges
// ComposterBlock.useItemOn (feed a COMPOSTABLE) and useWithoutItem (extract bone meal from a READY
// composter), in vanilla order: try the item/fill path first, and only when it PASSes (not a compostable,
// or already LEVEL 8) fall through to the extract path. Returns true on SUCCESS (no block placed), false
// on PASS (placement continues). CITE: ComposterBlock.useItemOn / useWithoutItem.
func (t *TickLoop) useComposter(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	lvl, ok := composterLevel(state)
	if !ok {
		return false
	}

	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))

	// useItemOn: lvl < 8 && COMPOSTABLES.containsKey(item).
	if lvl < composterMaxLevel && !slotIsEmpty(held) {
		if itName, isItem := composterItemName(held); isItem {
			if _, isKey := composterChance(itName); isKey {
				// lvl < 7 && !isClientSide -> addItem + levelEvent + consume. The server is never
				// client-side; the lvl==7 case still returns SUCCESS but does NOTHING (a full-but-not-READY
				// composter just eats the click).
				if lvl < composterReady-1 {
					newState := t.composterAddItem(p, state, pos, itName, lvl)
					_ = composterLevelEventFill // levelEvent(1500,...): client-visual, cited no-op.
					_ = newState
					if p.gameMode != gameModeCreative {
						t.shrinkHeldItem(p, inv) // stack.consume(1, player): survival shrinks; creative keeps.
					}
				}
				return true // SUCCESS regardless of the lvl==7 no-op.
			}
		}
	}

	// useWithoutItem: LEVEL == 8 -> extractProduce; else PASS.
	if lvl == composterMaxLevel {
		t.composterExtractProduce(p, pos)
		return true
	}
	return false // PASS: not a compostable and not READY -> placement continues.
}

// composterAddItem ports ComposterBlock.addItem 1:1: read the item chance; if NOT the (lvl==0 &&
// chance>0) free-fill short-circuit, roll ONE nextDouble and reject when >= chance (state unchanged).
// Otherwise bump LEVEL, setBlock (flag 3), and -- when the new level reaches 7 -- schedule the 20-tick
// READY cycle. Returns the resulting state (== input on a rejected roll). CITE: ComposterBlock.addItem.
func (t *TickLoop) composterAddItem(_ *tickPlayer, state block.StateID, pos pk.Position, itemName string, lvl int) block.StateID {
	chance, _ := composterChance(itemName)

	// if ((lvl != 0 || chance <= 0) && random.nextDouble() >= (double)chance) return state;
	// The && short-circuits: lvl==0 AND chance>0 skips the nextDouble entirely (guaranteed fill to 1).
	if lvl != 0 || chance <= 0 {
		r := t.cur()
		if r == nil || r.levelRandom == nil {
			return state
		}
		if r.levelRandom.NextDouble() >= float64(chance) {
			return state // rejected roll: no level change, one nextDouble drawn.
		}
	}

	newLvl := lvl + 1
	ns, ok := composterStateAt(newLvl)
	if !ok {
		return state
	}
	if t.world() != nil && t.world().SetBlock(pos, ns, dimMinY) {
		t.broadcastBlockUpdate(pos, ns)
		t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	}
	if newLvl == composterReady-1 {
		t.scheduleBlockTick(pos, composterTickType, composterReadyDelay) // schedule 7 -> 8 READY.
	}
	return ns
}

// composterExtractProduce ports ComposterBlock.extractProduce 1:1: spawn a bone_meal ItemEntity at
// (x+0.5, y+1.01, z+0.5) offset by offsetRandomXZ(random, 0.7) with the default pickup delay, then empty
// to LEVEL 0 (setBlock flag 3) and play COMPOSTER_EMPTY (cited client no-op). RNG: offsetRandomXZ draws
// two nextFloat from the region levelRandom, X then Z, BEFORE empty(). CITE: ComposterBlock.extractProduce.
func (t *TickLoop) composterExtractProduce(_ *tickPlayer, pos pk.Position) {
	x := float64(pos.X) + 0.5
	y := float64(pos.Y) + 1.01
	z := float64(pos.Z) + 0.5
	x, z = t.composterOffsetRandomXZ(x, z, 0.7)

	t.composterSpawnBoneMeal(x, y, z)

	t.composterEmpty(pos) // empty(...): setValue(LEVEL,0) + setBlock flag 3 + gameEvent.
	// playSound(COMPOSTER_EMPTY): client SFX, cited no-op (no ClientboundLevelEvent wire).
}

// composterOffsetRandomXZ ports Vec3.offsetRandomXZ(random, factor) for X/Z 1:1: add
// (nextFloat() - 0.5) * factor to X then to Z, in that draw order. CITE: Vec3.offsetRandomXZ.
func (t *TickLoop) composterOffsetRandomXZ(x, z float64, factor float32) (float64, float64) {
	r := t.cur()
	if r == nil || r.levelRandom == nil {
		return x, z
	}
	dx := (float64(r.levelRandom.NextFloat()) - 0.5) * float64(factor)
	dz := (float64(r.levelRandom.NextFloat()) - 0.5) * float64(factor)
	return x + dx, z + dz
}

// composterTick ports ComposterBlock.tick: the scheduled 7 -> 8 READY cycle. Dispatched from tickBlock
// (block_ticks.go) under composterTickType with the stale guard applied. Only a LEVEL-7 composter cycles
// (7 -> 8, flag 3); any other level is a no-op. COMPOSTER_READY sound is a cited client no-op. CITE:
// ComposterBlock.tick.
func (t *TickLoop) composterTick(state block.StateID, pos pk.Position) {
	lvl, ok := composterLevel(state)
	if !ok || lvl != composterReady-1 {
		return // only LEVEL 7 cycles to READY.
	}
	ns, ok := composterStateAt(composterReady)
	if !ok {
		return
	}
	if t.world() != nil && t.world().SetBlock(pos, ns, dimMinY) {
		t.broadcastBlockUpdate(pos, ns)
		t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	}
}

// composterAnalogOutputSignal ports ComposterBlock.getAnalogOutputSignal (hasAnalogOutputSignal true):
// getValue(LEVEL) -- 0..8. Returns (signal, true) for a composter; (0, false) otherwise so the comparator
// falls to its container/super path. CITE: ComposterBlock.getAnalogOutputSignal.
func (t *TickLoop) composterAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return 0, false
	}
	lvl, isComposter := composterLevel(s)
	if !isComposter {
		return 0, false
	}
	return lvl, true
}

// composterItemName resolves a held slot's item to "minecraft:<name>" for the COMPOSTABLES lookup, or
// ("", false) for an empty/unknown item. Mirrors the item.ByID resolution in block_place.go.
func composterItemName(s component.SlotData) (string, bool) {
	if slotIsEmpty(s) {
		return "", false
	}
	it, ok := item.ByID[item.ID(s.ItemID)]
	if !ok {
		return "", false
	}
	return "minecraft:" + it.Name, true
}

// -------------------------------------------------------------------------------------------------
// Hopper interaction -- ComposterBlock is a WorldlyContainerHolder. getContainer(state, level, pos)
// returns, by LEVEL: 8 -> OutputContainer (1-slot SimpleContainer pre-filled with a bone_meal stack,
// DOWN face takes it; setChanged -> empty), < 7 -> InputContainer (1-slot empty SimpleContainer, UP face
// places a COMPOSTABLE; setChanged -> addItem), 7 -> EmptyContainer (0 slots). A hopper ABOVE inserts
// compostables (UP face, InputContainer); a hopper BELOW pulls the bone_meal (DOWN face, OutputContainer).
// CITE: ComposterBlock.getContainer / InputContainer / OutputContainer / EmptyContainer.
// -------------------------------------------------------------------------------------------------

// composterInputSlot is the InputContainer/OutputContainer single slot index 0 (both are 1-slot
// SimpleContainers). CITE: ComposterBlock InputContainer.<init> (super(1)), OutputContainer.<init>.
const composterInputSlot = 0

// composterGetContainer ports ComposterBlock.getContainer(state, level, pos): resolve the composter's
// WorldlyContainer for the hopper. LEVEL comes from the state getContainerAt already read at pos. Returns
// nil for a non-composter. CITE: ComposterBlock.getContainer.
func (t *TickLoop) composterGetContainer(pos pk.Position, state block.StateID) containerView {
	lvl, ok := composterLevel(state)
	if !ok {
		return nil
	}
	switch {
	case lvl == composterMaxLevel: // 8 -> OutputContainer(new ItemStack(BONE_MEAL)).
		return &composterOutputContainer{
			t:     t,
			pos:   pos,
			state: state,
			item:  component.SlotData{ItemID: toItemID(boneMealItemID), Count: 1},
		}
	case lvl < composterReady-1: // < 7 -> InputContainer (empty 1-slot).
		return &composterInputContainer{t: t, pos: pos, state: state}
	default: // == 7 -> EmptyContainer (0 slots).
		return &composterEmptyContainer{}
	}
}

// composterInputContainer ports ComposterBlock InputContainer: a 1-slot WorldlyContainer a hopper ABOVE
// fills. getMaxStackSize()==1; UP face exposes slot 0; canPlaceItemThroughFace requires !changed && UP &&
// COMPOSTABLES.containsKey(item); canTakeItemThroughFace is always false. setChanged (fired by the hopper
// after setItem(0, stack)): if slot 0 non-empty -> changed=true, addItem(state, pos, stack),
// levelEvent(1500, added?1:0), removeItemNoUpdate(0). CITE: ComposterBlock InputContainer.
type composterInputContainer struct {
	t       *TickLoop
	pos     pk.Position
	state   block.StateID
	item    component.SlotData // the single SimpleContainer slot (starts empty).
	changed bool
}

func (c *composterInputContainer) getContainerSize() int { return 1 }
func (c *composterInputContainer) getItem(slot int) component.SlotData {
	if slot != composterInputSlot {
		return component.SlotData{Count: 0}
	}
	return c.item
}
func (c *composterInputContainer) setItem(slot int, stack component.SlotData) {
	if slot != composterInputSlot {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.item = stack
}
func (c *composterInputContainer) isEmpty() bool { return stackEmpty(c.item) }

// setChanged ports ComposterBlock InputContainer.setChanged: on a non-empty slot 0, mark changed, run
// ComposterBlock.addItem (RNG roll + LEVEL bump + scheduleTick at 7), fire levelEvent 1500, then empty
// slot 0. addItem uses the CAPTURED state (LEVEL at getContainer time). CITE: InputContainer.setChanged.
func (c *composterInputContainer) setChanged() {
	stack := c.item
	if stackEmpty(stack) {
		return
	}
	c.changed = true
	itName, ok := composterItemName(stack)
	if ok {
		lvl, _ := composterLevel(c.state)
		_ = c.t.composterAddItem(nil, c.state, c.pos, itName, lvl) // addItem(null, state, level, pos, stack).
		_ = composterLevelEventFill                                // levelEvent(1500, pos, added?1:0): cited client no-op.
	}
	c.item = component.SlotData{Count: 0} // removeItemNoUpdate(0).
}
func (c *composterInputContainer) getSlotsForFace(direction block.Direction) []int {
	if direction == block.Up { // UP -> {0}; else {}.
		return []int{composterInputSlot}
	}
	return []int{}
}
func (c *composterInputContainer) canPlaceItem(int, component.SlotData) bool { return true }

// canPlaceItemThroughFace ports InputContainer.canPlaceItemThroughFace: !changed && dir==UP &&
// COMPOSTABLES.containsKey(item). CITE: InputContainer.canPlaceItemThroughFace.
func (c *composterInputContainer) canPlaceItemThroughFace(_ int, stack component.SlotData, direction block.Direction) bool {
	if c.changed || direction != block.Up {
		return false
	}
	itName, ok := composterItemName(stack)
	if !ok {
		return false
	}
	_, isKey := composterChance(itName)
	return isKey
}
func (c *composterInputContainer) canTakeItem(int, component.SlotData) bool { return true }
func (c *composterInputContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return false // InputContainer.canTakeItemThroughFace -> false.
}
func (c *composterInputContainer) isWorldly() bool     { return true }
func (c *composterInputContainer) asHopper() *hopperBE { return nil }

// composterOutputContainer ports ComposterBlock OutputContainer: a 1-slot WorldlyContainer pre-filled with
// a bone_meal stack a hopper BELOW pulls. getMaxStackSize()==1; DOWN face exposes slot 0;
// canPlaceItemThroughFace is always false; canTakeItemThroughFace requires !changed && DOWN &&
// stack.is(BONE_MEAL). setChanged (fired by the hopper after it removes the bone_meal): empty(state, pos)
// -> LEVEL 0, then changed=true. CITE: ComposterBlock OutputContainer.
type composterOutputContainer struct {
	t       *TickLoop
	pos     pk.Position
	state   block.StateID
	item    component.SlotData // slot 0, pre-filled with bone_meal.
	changed bool
}

func (c *composterOutputContainer) getContainerSize() int { return 1 }
func (c *composterOutputContainer) getItem(slot int) component.SlotData {
	if slot != composterInputSlot {
		return component.SlotData{Count: 0}
	}
	return c.item
}
func (c *composterOutputContainer) setItem(slot int, stack component.SlotData) {
	if slot != composterInputSlot {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.item = stack
}
func (c *composterOutputContainer) isEmpty() bool { return stackEmpty(c.item) }

// setChanged ports ComposterBlock OutputContainer.setChanged: empty(null, state, level, pos) -> LEVEL 0
// (setBlock flag 3 + gameEvent), then changed=true. Vanilla runs empty() UNCONDITIONALLY (no isEmpty
// guard, unlike InputContainer). CITE: OutputContainer.setChanged.
func (c *composterOutputContainer) setChanged() {
	c.t.composterEmpty(c.pos)
	c.changed = true
}
func (c *composterOutputContainer) getSlotsForFace(direction block.Direction) []int {
	if direction == block.Down { // DOWN -> {0}; else {}.
		return []int{composterInputSlot}
	}
	return []int{}
}
func (c *composterOutputContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (c *composterOutputContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return false // OutputContainer.canPlaceItemThroughFace -> false.
}
func (c *composterOutputContainer) canTakeItem(int, component.SlotData) bool { return true }

// canTakeItemThroughFace ports OutputContainer.canTakeItemThroughFace: !changed && dir==DOWN &&
// stack.is(BONE_MEAL). CITE: OutputContainer.canTakeItemThroughFace.
func (c *composterOutputContainer) canTakeItemThroughFace(_ int, stack component.SlotData, direction block.Direction) bool {
	if c.changed || direction != block.Down {
		return false
	}
	return int32(stack.ItemID) == int32(toItemID(boneMealItemID))
}
func (c *composterOutputContainer) isWorldly() bool     { return true }
func (c *composterOutputContainer) asHopper() *hopperBE { return nil }

// composterEmptyContainer ports ComposterBlock EmptyContainer: a 0-slot WorldlyContainer (LEVEL 7 -- full
// but not yet READY). No face exposes a slot; nothing may be placed or taken. CITE: ComposterBlock EmptyContainer.
type composterEmptyContainer struct{}

func (c *composterEmptyContainer) getContainerSize() int { return 0 }
func (c *composterEmptyContainer) getItem(int) component.SlotData {
	return component.SlotData{Count: 0}
}
func (c *composterEmptyContainer) setItem(int, component.SlotData)           {}
func (c *composterEmptyContainer) isEmpty() bool                             { return true }
func (c *composterEmptyContainer) setChanged()                               {}
func (c *composterEmptyContainer) getSlotsForFace(block.Direction) []int     { return []int{} }
func (c *composterEmptyContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (c *composterEmptyContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return false
}
func (c *composterEmptyContainer) canTakeItem(int, component.SlotData) bool { return true }
func (c *composterEmptyContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return false
}
func (c *composterEmptyContainer) isWorldly() bool     { return true }
func (c *composterEmptyContainer) asHopper() *hopperBE { return nil }

// composterEmpty ports ComposterBlock.empty(entity, state, level, pos): setValue(LEVEL, 0), setBlock
// (flag 3) + gameEvent(BLOCK_CHANGE). Shared by extractProduce (hand) and OutputContainer.setChanged
// (hopper). CITE: ComposterBlock.empty.
func (t *TickLoop) composterEmpty(pos pk.Position) {
	ns, ok := composterStateAt(composterMinLevel)
	if !ok {
		return
	}
	if t.world() != nil && t.world().SetBlock(pos, ns, dimMinY) {
		t.broadcastBlockUpdate(pos, ns)
		t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	}
}

// composterSpawnBoneMeal spawns a bone_meal ItemEntity at (x,y,z) with the default pickup delay, the
// extractProduce drop. Mirrors the block-drop / dispenser spawn: NewItemEntity (which sets the vanilla
// default pickup delay 10 and the ItemEntity ctor's per-entity-random toss -- on the entity's OWN fresh
// RandomSource, NOT level.random, so it does NOT perturb the composter's level.random stream) then a
// store insert (the tracker broadcasts AddEntity + SetEntityData). CITE: ComposterBlock.extractProduce
// (new ItemEntity(level, x, y, z, new ItemStack(BONE_MEAL)); setDefaultPickUpDelay; addFreshEntity).
func (t *TickLoop) composterSpawnBoneMeal(x, y, z float64) {
	if t.cur() == nil || t.idAlloc == nil {
		return
	}
	stack := component.SlotData{ItemID: toItemID(boneMealItemID), Count: 1}
	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, stack)
	t.cur().entities.add(ie)
}
