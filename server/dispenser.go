package server

// dispenser.go — the DISPENSER + DROPPER dispense DRIVE (redstone tier-4): the 1:1 port of
// net.minecraft.world.level.block.DispenserBlock (+ DropperBlock override) + the DEFAULT
// DispenseItemBehavior (net.minecraft.core.dispenser.DefaultDispenseItemBehavior) over the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). This is the SERVER
// half — the redstone rising-edge trigger + the scheduled TRIGGERED tick that ejects an item out
// the FACING. The 9-slot container BE lives in dispenser_be.go; the block-state shape (FACING/
// TRIGGERED) in level/block/dispenser.go; the 3x3 menu in dispenser_menu.go.
//
// CITE (methods, jar-verified this session):
//   DispenserBlock.neighborChanged:
//       boolean shouldTrigger = hasNeighborSignal(pos) || hasNeighborSignal(pos.above());
//       boolean isTriggered   = state.getValue(TRIGGERED);
//       if (shouldTrigger && !isTriggered) { scheduleTick(pos, this, 4); setBlock(pos, TRIGGERED=true, 2); }
//       else if (!shouldTrigger && isTriggered) { setBlock(pos, TRIGGERED=false, 2); }
//   DispenserBlock.tick -> dispenseFrom(level, state, pos).
//   DispenserBlock.dispenseFrom: getBlockEntity; source=new BlockSource(...); slot=getRandomSlot(getRandom());
//       if (slot<0) { levelEvent(1001, pos, 0); gameEvent(BLOCK_ACTIVATE); return; }
//       stack=getItem(slot); behavior=getDispenseMethod(level, stack);
//       if (behavior != NOOP) setItem(slot, behavior.dispense(source, stack));
//   DispenserBlock.TRIGGER_DURATION = 4.
//   DispenserBlock.getDispensePosition(source) = center + 0.7*FACING (scale 0.7, offset ZERO).
//   DefaultDispenseItemBehavior.execute: split(1) one item, spawnItem(level, item, 6, FACING, pos), return remainder.
//   DefaultDispenseItemBehavior.spawnItem: spawnY -= (axis==Y ? 0.125 : 0.15625);
//       new ItemEntity(level, x, y, z, item);
//       double d = random.nextDouble()*0.1 + 0.2;
//       setDeltaMovement(triangle(stepX*d, 0.0172275*6), triangle(0.2, 0.0172275*6), triangle(stepZ*d, 0.0172275*6));
//   DropperBlock.dispenseFrom override: getContainerAt(pos.relative(FACING)); if null -> default shoot; else Hopper.addItem.
//   RandomSource.triangle(a, b) = a + b*(nextDouble() - nextDouble()).
//
// SCOPE / DEFERRALS (each jar-cited):
//   - The special DispenseItemBehavior registry is PARTIALLY ported in dispense_behaviors.go: the
//     ARROW/SNOWBALL/EGG ProjectileDispenseBehavior and the FlintAndSteelDispenseItemBehavior (fire +
//     TNT-prime branches) now fire via getDispenseMethod; the remaining registry entries (fire_charge/
//     potion/firework projectiles, armor/shears/dye entity behaviors, spawn-egg/boat/minecart/bucket/
//     bonemeal placement) remain cited deferrals in dispense_behaviors.go. CITE: DispenserBlock
//     .DISPENSER_REGISTRY (an IdentityHashMap populated by DispenseItemBehavior.bootStrap).
//   - The dropper's "eject into a container in front" (HopperBlockEntity.getContainerAt + addItem): Hopper
//     is NOT ported, so getContainerAt returns null (no container) and the dropper falls to the loose-item
//     DEFAULT shoot (dispenseFrom's `into == null` branch) — the dropper still WORKS, just always shoots
//     loose. CITE: DropperBlock.dispenseFrom (the addItem branch is the Hopper follow-up).
//   - levelEvent(1000 dispense sound / 1001 fail sound / 2000 dispense particle) + gameEvent are client
//     cosmetics with no gameplay effect — DEFERRED as faithful no-ops (the same seam brewing/piston/torch
//     use). CITE: DispenserBlock.dispenseFrom levelEvent(1001); DefaultDispenseItemBehavior playDefaultSound/
//     playDefaultAnimation.
//   - hasAnalogOutputSignal / getAnalogOutputSignal (a comparator reading a dispenser's fill level) is
//     DEFERRED (no comparator-off-container read yet). CITE: DispenserBlock.getAnalogOutputSignal.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// dispenserTriggerDuration is DispenserBlock.TRIGGER_DURATION (4) — the scheduled-tick delay between the
// rising redstone edge and the actual dispense. CITE: DispenserBlock.TRIGGER_DURATION.
const dispenserTriggerDuration = 4

// dispenserTickType / dropperTickType are the block ids the dispense TRIGGERED tick (DispenserBlock.tick)
// is scheduled/dispatched under. A dispenser and a dropper share the same tick machinery but schedule
// under their own block id. CITE: DispenserBlock (minecraft:dispenser) / DropperBlock (minecraft:dropper).
const (
	dispenserTickType blockTickType = "minecraft:dispenser"
	dropperTickType   blockTickType = "minecraft:dropper"
)

// dispenseAccuracy is DefaultDispenseItemBehavior.DEFAULT_ACCURACY (6) — the spread accuracy the velocity
// triangle draws scale by (0.0172275 * accuracy). CITE: DefaultDispenseItemBehavior.DEFAULT_ACCURACY.
const dispenseAccuracy = 6

// dispenseSpreadUnit is the 0.0172275 constant DefaultDispenseItemBehavior.spawnItem multiplies by the
// accuracy for the triangle spread half-range. CITE: DefaultDispenseItemBehavior.spawnItem.
const dispenseSpreadUnit = 0.0172275

// dispensePositionScale is the 0.7 scale DispenserBlock.getDispensePosition applies to the FACING step to
// place the dispense point 0.7 blocks out the front face from the block center. CITE:
// DispenserBlock.getDispensePosition (scale 0.7, offset Vec3.ZERO).
const dispensePositionScale = 0.7

// dispenserNeighborChanged ports DispenserBlock.neighborChanged (shared by DropperBlock): the redstone
// rising-edge trigger + the TRIGGERED latch. On a rising power edge (now powered, not yet TRIGGERED) it
// schedules the dispense 4 ticks out AND sets TRIGGERED=true (the latch that prevents re-triggering while
// held powered). On a falling edge (no longer powered, still TRIGGERED) it clears TRIGGERED. The power
// read is hasNeighborSignal(pos) OR hasNeighborSignal(pos.above()) — the dispenser is powered by any of
// its 6 neighbors OR any neighbor of the block directly above it (the same above-cell read pistons use).
// Dispatched from drainRedstoneUpdates (redstone.go) when a neighbor of a dispenser-family block changes.
//
// 1:1 net.minecraft.world.level.block.DispenserBlock.neighborChanged
func (t *TickLoop) dispenserNeighborChanged(pos pk.Position, state block.StateID) {
	// boolean shouldTrigger = level.hasNeighborSignal(pos) || level.hasNeighborSignal(pos.above());
	shouldTrigger := t.hasNeighborSignal(pos) || t.hasNeighborSignal(relative(pos, block.Up))
	// boolean isTriggered = state.getValue(TRIGGERED);
	isTriggered := block.DispenserTriggered(state)

	typ := dispenserTickType
	if block.IsDropper(state) {
		typ = dropperTickType
	}

	if shouldTrigger && !isTriggered {
		// level.scheduleTick(pos, this, 4); level.setBlock(pos, state.setValue(TRIGGERED, true), 2);
		t.scheduleBlockTick(pos, typ, dispenserTriggerDuration)
		if ns, ok := block.DispenserWithTriggered(state, true); ok && t.world().SetBlock(pos, ns, dimMinY) {
			// setBlock flag 2 == UPDATE_CLIENTS (no neighbor update): broadcast the state to clients.
			t.broadcastBlockUpdate(pos, ns)
		}
	} else if !shouldTrigger && isTriggered {
		// level.setBlock(pos, state.setValue(TRIGGERED, false), 2);
		if ns, ok := block.DispenserWithTriggered(state, false); ok && t.world().SetBlock(pos, ns, dimMinY) {
			t.broadcastBlockUpdate(pos, ns)
		}
	}
}

// dispenserTick ports DispenserBlock.tick(state, level, pos, random) -> dispenseFrom(level, state, pos):
// the scheduled TRIGGERED tick fires the dispense. Dispatched from tickBlock (block_ticks.go) after the
// stale-tick guard confirms the block is still a dispenser-family block.
//
// 1:1 net.minecraft.world.level.block.DispenserBlock.tick
func (t *TickLoop) dispenserTick(state block.StateID, pos pk.Position) {
	t.dispenseFrom(pos, state)
}

// dispenseFrom ports DispenserBlock.dispenseFrom(level, state, pos) with the DropperBlock override:
//
//	DispenserBlockEntity be = level.getBlockEntity(pos, DISPENSER/DROPPER).orElse(null);
//	if (be == null) { warn; return; }
//	BlockSource source = new BlockSource(level, pos, state, be);
//	int slot = be.getRandomSlot(level.getRandom());
//	if (slot < 0) { levelEvent(1001, pos, 0); [gameEvent]; return; }   // fail: nothing to dispense
//	ItemStack stack = be.getItem(slot);
//	<dispenser>: behavior = getDispenseMethod(level, stack); if (behavior != NOOP) be.setItem(slot, behavior.dispense(source, stack));
//	<dropper>:   if (stack.isEmpty()) return; into = getContainerAt(pos.relative(FACING));
//	             if (into == null) remaining = DEFAULT.dispense(source, stack);
//	             else remaining = Hopper.addItem(...); be.setItem(slot, remaining);
//
// The BE resolve is resolveDispenser (the tick-owned t.dispensers store — a placed dispenser synthesizes
// an empty BE, so the warn-on-null path never fires for a real dispenser cell). The RNG is the level
// random (level.getRandom()) — the same stream growth/thunder draw, NEVER a per-entity mob stream, so the
// pig oracle's pinned per-mob streams are unperturbed. Tick-owned.
//
// 1:1 DispenserBlock.dispenseFrom / DropperBlock.dispenseFrom
func (t *TickLoop) dispenseFrom(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	d := t.resolveDispenser(pos, state)
	if d == nil {
		return // getBlockEntity == null: warn + return (never for a real dispenser cell)
	}

	rng := t.dispenserRandom()
	if rng == nil {
		return // no level random (a test without a region): cannot draw the slot — no-op
	}

	// int slot = be.getRandomSlot(level.getRandom());
	slot := d.getRandomSlot(rng)
	if slot < 0 {
		// DispenserBlock.dispenseFrom empty branch: level.levelEvent(1001, pos, 0); level.gameEvent(
		// GameEvent.BLOCK_ACTIVATE, pos) -- the empty-fire click vibration (frequency 10). World source.
		t.dispenserLevelEvent(pos, 1001) // fail sound — cited client no-op seam
		t.gameEventAt(geBlockActivate, pos, gameEventContext{})
		return
	}

	stack := d.items[slot]

	facing, _ := block.DispenserFacing(state)

	if d.isDropper {
		// DropperBlock.dispenseFrom: if (itemStack.isEmpty()) return.
		if stackEmpty(stack) {
			return
		}
		// Container into = HopperBlockEntity.getContainerAt(level, pos.relative(direction));
		// getContainerAt now resolves ANY container block-entity in front (chest/furnace/dispenser/brewing/
		// hopper) via the shared seam (container.go). into == null -> the loose-item DEFAULT shoot; else the
		// Hopper.addItem eject into the container. CITE: DropperBlock.dispenseFrom.
		if into := t.dropperContainerInFront(pos, facing); into == nil {
			remaining := t.dispenseDefaultBehavior(pos, state, facing, stack)
			d.items[slot] = remaining
		} else {
			remaining := t.dropperEjectInto(d, into, stack, pos, facing)
			d.items[slot] = remaining
		}
		t.markDispenserDirty(pos)
		t.broadcastDispenserChange(pos, d)
		return
	}

	// DispenserBlock: behavior = getDispenseMethod(level, stack); if (behavior != NOOP) be.setItem(slot,
	// behavior.dispense(source, stack)). getDispenseMethod looks the item up in DISPENSER_REGISTRY: a HIT
	// runs the registered special behavior (dispense_behaviors.go), a MISS falls to the DEFAULT eject.
	// DEFAULT is never NOOP, so it always fires. CITE: DispenserBlock.getDispenseMethod / DISPENSER_REGISTRY.
	if remaining, handled := t.dispenseSpecialBehavior(pos, state, facing, stack); handled {
		d.items[slot] = remaining
		t.markDispenserDirty(pos)
		t.broadcastDispenserChange(pos, d)
		return
	}
	remaining := t.dispenseDefaultBehavior(pos, state, facing, stack)
	d.items[slot] = remaining
	t.markDispenserDirty(pos)
	t.broadcastDispenserChange(pos, d)
}

// dispenseDefaultBehavior ports DefaultDispenseItemBehavior.dispense -> execute + spawnItem: split ONE
// item off the dispensed stack, shoot it out the FACING as an ItemEntity with the vanilla spread
// velocity, and return the REMAINDER (the stack after split(1)) for setItem back into the slot.
//
//	execute: direction=FACING; position=getDispensePosition(source); itemStack=dispensed.split(1);
//	         spawnItem(level, itemStack, 6, direction, position); return dispensed;
//	spawnItem: spawnX/Y/Z = position; spawnY -= (axis==Y ? 0.125 : 0.15625);
//	           new ItemEntity(level, x, y, z, itemStack);          // 3 entity-random toss draws (overwritten)
//	           double d = random.nextDouble()*0.1 + 0.2;           // LEVEL random
//	           setDeltaMovement(triangle(stepX*d, 0.0172275*6), triangle(0.2, 0.0172275*6), triangle(stepZ*d, 0.0172275*6));
//
// 1:1 net.minecraft.core.dispenser.DefaultDispenseItemBehavior.execute + spawnItem
func (t *TickLoop) dispenseDefaultBehavior(pos pk.Position, state block.StateID, facing block.Direction, dispensed component.SlotData) component.SlotData {
	if stackEmpty(dispensed) {
		return dispensed // split(1) on empty is empty; nothing to shoot
	}
	// ItemStack itemStack = dispensed.split(1) — one item off, `dispensed` is the remainder.
	work := dispensed
	shot := stackSplit(&work, 1) // shot.Count == 1, work == remainder

	// Position position = getDispensePosition(source) = center + 0.7*direction (offset ZERO).
	sx, sy, sz := dirVec(facing)
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	posX := cx + dispensePositionScale*float64(sx)
	posY := cy + dispensePositionScale*float64(sy)
	posZ := cz + dispensePositionScale*float64(sz)

	// spawnItem: spawnY -= (direction.getAxis() == Y ? 0.125 : 0.15625).
	spawnX := posX
	spawnY := posY
	spawnZ := posZ
	if facing == block.Up || facing == block.Down {
		spawnY -= 0.125
	} else {
		spawnY -= 0.15625
	}

	// new ItemEntity(level, x, y, z, itemStack): the ctor draws 3 ENTITY-random toss values (this.random),
	// then setDeltaMovement OVERWRITES them with the spread below. NewItemEntity draws its toss from
	// math/rand/v2 (the per-entity toss, immediately discarded here), so only the observable spread —
	// computed from the LEVEL random — survives. pickupDelay defaults to 10 (setDefaultPickUpDelay is NOT
	// called by spawnItem; ItemEntity.<init> leaves the DEFAULT pickup delay, which is 10 — same value).
	ie := NewItemEntity(t.idAlloc.AllocID(), spawnX, spawnY, spawnZ, shot)

	// double d = random.nextDouble() * 0.1 + 0.2;   (LEVEL random)
	rng := t.dispenserRandom()
	pow := rng.NextDouble()*0.1 + 0.2
	spread := dispenseSpreadUnit * float64(dispenseAccuracy)
	// setDeltaMovement(triangle(stepX*d, spread), triangle(0.2, spread), triangle(stepZ*d, spread)).
	ie.vx = randTriangle(rng, float64(sx)*pow, spread)
	ie.vy = randTriangle(rng, 0.2, spread)
	ie.vz = randTriangle(rng, float64(sz)*pow, spread)

	t.cur().entities.add(ie) // level.addFreshEntity(itemEntity) -> the tracker broadcasts AddEntity next tick

	// levelEvent(1000) dispense sound + levelEvent(2000) dispense particle — client cosmetics, DEFERRED.
	t.dispenserLevelEvent(pos, 1000)

	return work // return dispensed (the remainder after split(1))
}

// randTriangle ports net.minecraft.util.RandomSource.triangle(double center, double halfRange):
// center + halfRange * (nextDouble() - nextDouble()). TWO nextDouble draws (in that order) — the exact
// draw order + count vanilla uses so the dispensed-item velocity is bit-faithful.
//
// 1:1 RandomSource.triangle (VERIFIED javap: dload center; dload half; nextDouble; nextDouble; dsub; dmul; dadd).
func randTriangle(rng interface{ NextDouble() float64 }, center, halfRange float64) float64 {
	a := rng.NextDouble()
	b := rng.NextDouble()
	return center + halfRange*(a-b)
}

// dropperContainerInFront ports HopperBlockEntity.getContainerAt(level, pos.relative(FACING)) for the
// dropper's eject target — the FILLED Hopper seam: resolve ANY container block-entity (chest / furnace /
// dispenser / brewing / hopper) at the cell in FACING to the shared containerView. Returns nil when the
// front cell holds no container (then the dropper falls to the loose-item DEFAULT shoot). CITE:
// DropperBlock.dispenseFrom (Container into = HopperBlockEntity.getContainerAt(level, pos.relative(dir))).
func (t *TickLoop) dropperContainerInFront(pos pk.Position, facing block.Direction) containerView {
	return t.getContainerAt(relative(pos, facing))
}

// dropperEjectInto ports the DropperBlock.dispenseFrom `into != null` branch: push ONE item into the
// container in front via HopperBlockEntity.addItem, then compute the remaining source stack.
//
//	remaining = HopperBlockEntity.addItem(be, into, itemStack.copyWithCount(1), direction.getOpposite());
//	if (remaining.isEmpty()) { remaining = itemStack.copy(); remaining.shrink(1); }
//	else                     { remaining = itemStack.copy(); }
//
// VERIFIED CFR DropperBlock.dispenseFrom: the addItem direction is FACING.getOpposite() (the item enters
// the front container from the dropper's side, i.e. the face pointing back at the dropper). `be` is the
// dropper's own dispenserBE wrapped as the `from` container. CITE: DropperBlock.dispenseFrom.
func (t *TickLoop) dropperEjectInto(from *dispenserBE, into containerView, stack component.SlotData, pos pk.Position, facing block.Direction) component.SlotData {
	// remaining = HopperBlockEntity.addItem(be, into, itemStack.copyWithCount(1), direction.getOpposite());
	fromView := &dispenserContainer{t: t, pos: pos, d: from}
	one := stackCopyWithCount(stack, 1)
	remaining := t.hopperAddItem(fromView, into, one, dirOpposite(facing))
	out := stack // itemStack.copy()
	if stackEmpty(remaining) {
		// remaining = itemStack.copy(); remaining.shrink(1);
		out.Count = toVar(int(out.Count) - 1)
		if out.Count <= 0 {
			out = component.SlotData{Count: 0}
		}
	}
	return out
}

// dispenserRandom returns the LEVEL random (level.getRandom()) the dispense drive draws from — the same
// per-region levelRandom growth/thunder/XP use, NEVER a per-entity mob stream, so the pig oracle's pinned
// per-mob streams are unperturbed. Nil when no region random is available (a test without a region).
func (t *TickLoop) dispenserRandom() *levelgen.LegacyRandomSource {
	if r := t.cur(); r != nil && r.levelRandom != nil {
		return r.levelRandom
	}
	return nil
}

// dispenserLevelEvent is the cited faithful no-op seam for DispenserBlock/DefaultDispenseItemBehavior's
// levelEvent calls (1000 dispense sound, 1001 fail sound, 2000 dispense particle) — client cosmetics with
// no gameplay effect, the same seam brewing_stand_be.go / piston.go / redstone.go defer. When a sound/
// particle subsystem lands, this becomes the real broadcast.
func (t *TickLoop) dispenserLevelEvent(_ pk.Position, _ int) {}
