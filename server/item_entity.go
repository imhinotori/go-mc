package server

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_entity.go — Plan 17-14 (ITEM-PICKUP): the dropped-item lifecycle that makes a
// ground item actually fall/settle, age out, and — the reported bug — get PICKED UP.
//
// Three ported vanilla surfaces, all decompiled from temp/cache/26.2-inner.jar this session
// and cited at each call site:
//
//	FIX C  net.minecraft.world.entity.item.ItemEntity.tick()       → tickItems / tickItem
//	FIX D  ItemEntity.playerTouch(Player) + LivingEntity.take(...)  → pickup scan + takeItem
//	       net.minecraft.world.entity.player.Inventory.add(ItemStack) → inventoryAdd
//
// SINGLE-OWNER (TICK-05): every function here runs on the tick goroutine over the tick-owned
// entityStore / tickPlayer state. The item tick mutates velocity/age/pickupDelay and moves the
// item via t.moveEntity (the bucket-consistent path); the pickup scan mutates the player
// inventory and removes the item from the store. No goroutine, no xsync/ants — pure owner work.

const (
	// itemDefaultPickupDelay is ItemEntity.setDefaultPickUpDelay()'s value: a freshly dropped
	// item is NOT pickable for 10 ticks (0.5 s). The item tick counts it down to 0.
	//   [VERIFIED javap ItemEntity.setDefaultPickUpDelay: bipush 10; putfield pickupDelay.]
	itemDefaultPickupDelay = 10

	// itemInfinitePickupDelay is ItemEntity.INFINITE_PICKUP_DELAY (32767): a sentinel the tick
	// never decrements and playerTouch treats as "never pickable". Ported for completeness so a
	// future setNeverPickUp() drop behaves vanilla-correctly.
	//   [VERIFIED javap: private static final int INFINITE_PICKUP_DELAY = 32767.]
	itemInfinitePickupDelay = 32767

	// itemLifetime is ItemEntity.LIFETIME (6000 ticks == 5 minutes): the item DESPAWNS once its
	// age reaches this. The item tick discards at age >= 6000.
	//   [VERIFIED javap: private static final int LIFETIME = 6000.]
	itemLifetime = 6000

	// itemInfiniteLifetime is ItemEntity.INFINITE_LIFETIME (-32768): the age sentinel the tick
	// never increments (an unlimited-lifetime item never despawns). Ported for completeness.
	//   [VERIFIED javap: private static final int INFINITE_LIFETIME = -32768.]
	itemInfiniteLifetime = -32768
)

const (
	// itemGravity is ItemEntity.getDefaultGravity() == 0.04 — the per-tick downward acceleration
	// a dropped item gets (HALF a living entity's 0.08). The generic tickPhysics uses 0.08, so
	// items are stepped HERE with their own 0.04 so the tossed item falls at the vanilla rate.
	//   [VERIFIED javap ItemEntity.getDefaultGravity: ldc2_w 0.04d; dreturn.]
	itemGravity = 0.04

	// itemPickupInflateXZ / itemPickupInflateY are Player.aiStep's item-collection AABB inflation:
	// getBoundingBox().inflate(1.0, 0.5, 1.0) — the player's box grown by 1 block on X/Z and 0.5
	// on Y, and any item entity intersecting it is touched (picked up if pickable). Decompiled
	// verbatim from Player.aiStep (dconst_1 / 0.5d / dconst_1 → AABB.inflate(DDD)).
	//   [VERIFIED javap Player.aiStep: getBoundingBox().inflate(1.0, 0.5, 1.0) → getEntities → touch.]
	itemPickupInflateXZ = 1.0
	itemPickupInflateY  = 0.5
)

const (
	// itemAirDrag is Entity.getAirDrag() == 0.98f — the per-tick velocity multiplier ItemEntity.tick
	// applies to ALL THREE axes after the move (vertical always, horizontal on top of the ground
	// friction). This is the ITEM's own drag, NOT the generic physics.go airDrag tunable (also 0.98
	// but [ASSUMED]); ported here as the cited vanilla constant so the item trajectory is 1:1.
	//   [VERIFIED javap Entity.getAirDrag: ldc_w float 0.98f; freturn.]
	itemAirDrag = 0.98

	// itemGroundFriction is the DEFAULT Block.getFriction() (BlockBehaviour.Properties.friction ==
	// 0.6f) that ItemEntity.tick reads off the block below when onGround, then multiplies by 0.98:
	// `f1 = level.getBlockState(below).getBlock().getFriction() * 0.98`. Sulfur has no per-block
	// friction table wired for arbitrary blocks (same state as boatBlockFriction), so it reads the
	// DEFAULT 0.6 — the value stone/dirt/grass (every block a dropped item rests on) actually carry.
	// Structured to become a per-block getFriction() read later (CLAUDE.md: cite the default, never
	// bake it away).
	//   [VERIFIED javap ItemEntity.tick: onGround branch multiplies f1 by getBlockState(below)
	//    .getBlock().getFriction(); CFR Block.getFriction: return this.friction; Properties default 0.6f.]
	itemGroundFriction = 0.6

	// itemBounce is the vertical velocity multiplier ItemEntity.tick applies when it lands with a
	// downward velocity: `if onGround && deltaMovement.y < 0 { setDeltaMovement(delta.multiply(1, -0.5, 1)) }`.
	// The item bounces at half its impact speed. (In Sulfur the swept resolver zeroes vy on a floor
	// hit BEFORE this runs, so the guard delta.y < 0 is normally already false — the bounce is ported
	// 1:1 for the pre-resolve-velocity case the vanilla ordering exposes; see tickItem.)
	//   [VERIFIED javap ItemEntity.tick: onGround && Vec3.y < 0 -> multiply(1.0, -0.5, 1.0).]
	itemBounce = -0.5

	// itemFluidHeightThreshold is the (double)(float)0.1 the fluid branch compares getFluidHeight
	// against: `isInWater() && getFluidHeight(WATER) > 0.1` (else lava, else applyGravity).
	//   [VERIFIED javap ItemEntity.tick: ldc2_w double 0.10000000149011612d (== (double)0.1f).]
	itemFluidHeightThreshold = 0.10000000149011612

	// itemWaterFluidMovement is ItemEntity.setUnderwaterMovement()'s scale == 0.99 (the horizontal
	// buoyancy drag), and itemLavaFluidMovement is setUnderLavaMovement()'s == 0.95. Both feed
	// setFluidMovement(d): delta.x*d, (delta.y<0.06 ? delta.y+0.0005 : delta.y), delta.z*d.
	//   [VERIFIED javap ItemEntity.setUnderwaterMovement: ldc2_w 0.9900000095367432d; setFluidMovement.
	//    setUnderLavaMovement: ldc2_w 0.949999988079071d; setFluidMovement.]
	itemWaterFluidMovement = 0.9900000095367432
	itemLavaFluidMovement  = 0.949999988079071

	// itemFluidBobCutoff / itemFluidBobAdd are setFluidMovement's vertical bob: while the current
	// vertical velocity is below 0.06 the item gets a tiny +0.0005 upward nudge (so a submerged item
	// slowly rises to the surface), else the vertical velocity is left as-is.
	//   [VERIFIED javap ItemEntity.setFluidMovement: getfield y; ldc2_w 0.05999999865889549d; dcmpg
	//    iflt -> ldc float 5.0E-4f else fconst_0; f2d; dadd -> new y.]
	itemFluidBobCutoff = 0.05999999865889549
	itemFluidBobAdd    = 5.0e-4

	// itemMergeInflateXZ / itemMergeInflateY are mergeWithNeighbours' scan-box inflation:
	// getBoundingBox().inflate(0.5, 0.0, 0.5) — half a block on X/Z, nothing on Y.
	//   [VERIFIED javap ItemEntity.mergeWithNeighbours: ldc2_w 0.5d; dconst_0; ldc2_w 0.5d;
	//    AABB.inflate(DDD); getEntitiesOfClass(ItemEntity.class, box, predicate).]
	itemMergeInflateXZ = 0.5
	itemMergeInflateY  = 0.0

	// itemMergeRestingInterval / itemMergeMovingInterval are the tickCount % interval merge cadence:
	// 40 ticks when the item stayed in the same block cell this tick, 2 ticks when it moved cells.
	//   [VERIFIED javap ItemEntity.tick: `int interval = movedThisTick ? 2 : 40; if tickCount % interval == 0 ...`.]
	itemMergeRestingInterval = 40
	itemMergeMovingInterval  = 2

	// itemRestMoveThrottle is the (tickCount + getId()) % 4 gate: a grounded item whose horizontal
	// speed² is <= 1e-5 only runs its move() every 4th tick (an idle-item CPU optimization). The
	// horizontalDistanceSqr threshold is 9.999999747378752E-6 == (double)(float)1e-5.
	//   [VERIFIED javap ItemEntity.tick: onGround && horizontalDistanceSqr() <= 9.999999747378752E-6
	//    && (tickCount + getId()) % 4 != 0 -> skip the move block.]
	itemRestMoveThrottle       = 4
	itemRestHorizontalSpeedSqr = 9.999999747378752e-6
)

// tickItems is the Plan 17-14 per-tick item pass: it steps every dropped Item entity in the
// tick-owned store (gravity + age + despawn) and then scans each player for nearby pickable
// items. It is wired into tickEntities (alongside tickFallDamage / tickBreath) so no new tick
// phase is added (TestTickPhaseOrder stays green). Runs on the tick goroutine.
//
// Ordering: items are TICKED first (so pickupDelay decrements and the toss settles), THEN the
// pickup scan runs — mirroring vanilla, where ItemEntity.tick() (which decrements pickupDelay)
// runs in the entity tick BEFORE Player.aiStep collects items in the same server tick.
func (t *TickLoop) tickItems() {
	// Phase-27 STEP-3 (N=2): item entities live across BOTH regions (an item dropped near the seam),
	// and tickItems runs on the COORDINATOR (quiescent — every region joined). Process each region's
	// items WITH that region registered as the current region (withRegion), so tickItem's t.only()
	// (the moveEntity re-bucket + the despawn remove) resolves to the item's OWN store rather than
	// globalRegion. The snapshot per region keeps the loop stable across an in-loop discard.
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isItem {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickItem(e)
			}
		})
	}

	// Pickup scan AFTER the item step (vanilla: ItemEntity.tick precedes Player.aiStep's touch).
	for _, p := range t.players {
		if p == nil || p.dead {
			continue // a dead player (death screen) collects nothing
		}
		t.scanItemPickup(p)
	}
}

// tickItem ports the load-bearing body of net.minecraft.world.entity.item.ItemEntity.tick() 1:1
// (FIX C, extended by divergence audit B-A7 with buoyancy/lava-pop/bounce/merge). It reproduces
// the vanilla call chain and float ops EXACTLY. Tick-owned.
//
// Vanilla ItemEntity.tick() (javap, the observable-state path -- client-only branches elided):
//
//	if getItem().isEmpty() { discard(); return }
//	super.tick()                                             // tickCount++
//	if pickupDelay > 0 && pickupDelay != 32767 { pickupDelay-- }
//	xo = getX(); yo = getY(); zo = getZ()
//	Vec3 vec3 = getDeltaMovement()
//	if isInWater() && getFluidHeight(WATER) > 0.1 { setUnderwaterMovement() }        // 0.99
//	else if isInLava() && getFluidHeight(LAVA) > 0.1 { setUnderLavaMovement() }      // 0.95
//	else { applyGravity() }                                                          // 0.04
//	if !(onGround() && horizontalDistanceSqr() <= 1e-5 && (tickCount+getId())%4 != 0) {
//	    move(SELF, getDeltaMovement()); applyEffectsFromBlocks()
//	    float f = getAirDrag(); float f1 = f
//	    if onGround() { f1 = getBlockState(below).getBlock().getFriction() * 0.98 }
//	    setDeltaMovement(delta.multiply(f1, f, f1))
//	    if onGround() && delta.y < 0 { setDeltaMovement(delta.multiply(1, -0.5, 1)) }  // bounce
//	} else { applyEffectsFromBlocksForLastMovements() }
//	boolean moved = floor(xo)!=floor(x) || floor(yo)!=floor(y) || floor(zo)!=floor(z)
//	int interval = moved ? 2 : 40
//	if tickCount % interval == 0 && !clientSide && isMergable() { mergeWithNeighbours() }
//	if age != -32768 { age++ }
//	if !clientSide && age >= 6000 { discard() }
func (t *TickLoop) tickItem(e *Entity) {
	// ItemEntity.tick first branch: an empty stack discards the entity (it carries nothing).
	if e.itemStack.Count <= 0 {
		t.cur().entities.remove(e.id)
		return
	}

	// super.tick(): Entity.tick increments the free-running tickCount (used below for the rest
	// throttle and merge cadence). This is the only super.tick side-effect this item path reads.
	e.itemTickCount++

	// pickupDelay countdown (skipping the INFINITE sentinel). A fresh drop is pickable after
	// itemDefaultPickupDelay (10) ticks of this decrement.
	if e.pickupDelay > 0 && e.pickupDelay != itemInfinitePickupDelay {
		e.pickupDelay--
	}

	// xo/yo/zo: the previous-tick position, captured BEFORE the move so the moved-cell test below
	// (which chooses the 2- vs 40-tick merge interval) compares the same floors vanilla does.
	xo, yo, zo := e.x, e.y, e.z

	// Fluid branch (mutually exclusive, in vanilla order): water buoyancy, else lava pop, else
	// the item's own 0.04 gravity. getFluidHeight must exceed 0.1 (submerged past the surface skin)
	// before the buoyancy replaces gravity, matching the vanilla > 0.1 gates.
	switch {
	case t.mobInWater(e) && t.mobFluidHeight(e, fluidWater) > itemFluidHeightThreshold:
		t.setItemFluidMovement(e, itemWaterFluidMovement) // setUnderwaterMovement (0.99 + bob)
	case t.mobInLava(e) && t.mobFluidHeight(e, fluidLava) > itemFluidHeightThreshold:
		t.setItemFluidMovement(e, itemLavaFluidMovement) // setUnderLavaMovement (0.95 + bob)
	default:
		e.vy -= itemGravity // applyGravity(): getDefaultGravity()==0.04 (isNoGravity() is false here)
	}

	// The rest-throttle: a grounded item that is essentially still horizontally only runs its move
	// (and the drag/bounce that follows) every 4th tick -- an idle-item optimization keyed on the
	// PREVIOUS move's onGround/velocity. When the throttle skips, the drag/friction/bounce is NOT
	// applied this tick (vanilla runs only applyEffectsFromBlocksForLastMovements, a no-op here),
	// so the item's velocity is preserved untouched until the next non-throttled tick.
	horizSpeedSqr := e.vx*e.vx + e.vz*e.vz
	throttled := e.onGround && horizSpeedSqr <= itemRestHorizontalSpeedSqr &&
		(e.itemTickCount+int(e.id))%itemRestMoveThrottle != 0

	if !throttled {
		// move(SELF, delta): integrate via the per-axis swept resolver (the anti-tunneling
		// discipline shared with tickPhysics/tickOrb). It re-buckets through entities.move, sets
		// onGround, and zeroes any blocked velocity component so the item lands on the floor.
		t.moveEntity(e, e.vx, e.vy, e.vz)

		// Air drag + ground friction: f = getAirDrag() (0.98) on the vertical; the horizontal
		// scale f1 = f, but when onGround f1 = blockFrictionBelow * 0.98. delta.multiply(f1, f, f1).
		f := itemAirDrag
		f1 := f
		if e.onGround {
			f1 = itemGroundFriction * itemAirDrag
		}
		e.vx *= f1
		e.vy *= f
		e.vz *= f1

		// Bounce: on the ground with a still-downward velocity, halve and invert vy (multiply by
		// -0.5). The swept resolver above zeroes vy on a floor landing BEFORE this, so vy<0 is
		// normally already false and the bounce is a no-op -- ported 1:1 for the frames where the
		// item is grounded from a prior tick yet retains downward velocity (vanilla's exact case).
		if e.onGround && e.vy < 0 {
			e.vy *= itemBounce
		}
	}

	// mergeWithNeighbours cadence: 2 ticks if the item crossed a block cell this tick, else 40.
	// Only the SERVER merges (Sulfur has no client world -> always run), and only a mergable item
	// (alive, pickable, not INFINITE_LIFETIME, age<6000, count<maxStackSize).
	moved := floorI(xo) != floorI(e.x) || floorI(yo) != floorI(e.y) || floorI(zo) != floorI(e.z)
	interval := itemMergeRestingInterval
	if moved {
		interval = itemMergeMovingInterval
	}
	if e.itemTickCount%interval == 0 && t.isItemMergable(e) {
		t.mergeItemWithNeighbours(e)
		// mergeWithNeighbours may discard THIS item (when it merged into a larger neighbour and its
		// stack emptied). A discarded item is gone from the store -- do not age/despawn it.
		if _, ok := t.cur().entities.get(e.id); !ok {
			return
		}
	}

	// age++ (skipping the INFINITE_LIFETIME sentinel), then DESPAWN at LIFETIME. Removing the
	// item from the store makes the tracker emit RemoveEntities to every tracking player next tick.
	if e.age != itemInfiniteLifetime {
		e.age++
	}
	if e.age >= itemLifetime {
		t.cur().entities.remove(e.id)
	}
}

// setItemFluidMovement ports ItemEntity.setFluidMovement(double) (setUnderwaterMovement passes 0.99,
// setUnderLavaMovement passes 0.95): scale horizontal velocity by d, and add a tiny +0.0005 upward
// bob while the vertical velocity is below 0.06 (so a submerged item slowly rises to the surface),
// leaving the vertical velocity untouched otherwise. Tick-owned.
//
// Vanilla (javap ItemEntity.setFluidMovement):
//
//	Vec3 v = getDeltaMovement();
//	setDeltaMovement(v.x * d, v.y + (v.y < 0.06 ? 5.0E-4f : 0.0f), v.z * d);
func (t *TickLoop) setItemFluidMovement(e *Entity, d float64) {
	e.vx *= d
	if e.vy < itemFluidBobCutoff {
		e.vy += itemFluidBobAdd
	}
	e.vz *= d
}

// isItemMergable ports ItemEntity.isMergable(): the item can still combine with a neighbour -- it
// is alive (in the store), NOT INFINITE_PICKUP_DELAY, NOT INFINITE_LIFETIME, has not yet aged out
// (age < 6000), and its stack is below its max stack size (a full stack absorbs no more). Tick-owned.
//
// Vanilla (javap ItemEntity.isMergable):
//
//	ItemStack s = getItem();
//	return isAlive() && pickupDelay != 32767 && age != -32768 && age < 6000
//	    && s.getCount() < s.getMaxStackSize();
func (t *TickLoop) isItemMergable(e *Entity) bool {
	if _, ok := t.cur().entities.get(e.id); !ok {
		return false // !isAlive(): already removed from the store
	}
	if e.pickupDelay == itemInfinitePickupDelay || e.age == itemInfiniteLifetime || e.age >= itemLifetime {
		return false
	}
	return int(e.itemStack.Count) < maxStackSize(e.itemStack)
}

// mergeItemWithNeighbours ports ItemEntity.mergeWithNeighbours(): scan the item entities in the
// (0.5, 0.0, 0.5)-inflated box around this item and, for each OTHER mergable item, tryToMerge; stop
// the scan the instant THIS item is discarded (its stack emptied into a larger neighbour). The broad
// phase is the store near() (the same query scanItemPickup uses); the (0.5,0,0.5) inflation is the
// narrow-phase AABB test on that candidate set. Tick-owned.
//
// Vanilla (javap ItemEntity.mergeWithNeighbours):
//
//	if (!isMergable()) return;
//	for (ItemEntity other : level.getEntitiesOfClass(ItemEntity.class,
//	        getBoundingBox().inflate(0.5, 0.0, 0.5), e -> e != this && e.isMergable())) {
//	    if (other.isMergable()) { tryToMerge(other); if (isRemoved()) break; }
//	}
func (t *TickLoop) mergeItemWithNeighbours(e *Entity) {
	if !t.isItemMergable(e) {
		return // re-checked at entry exactly as vanilla (mergeWithNeighbours guards isMergable first)
	}

	// The (0.5, 0.0, 0.5)-inflated scan box (getBoundingBox() is feet-anchored, half-width e.width/2).
	hw := e.width/2 + itemMergeInflateXZ
	loX, hiX := e.x-hw, e.x+hw
	loY, hiY := e.y-itemMergeInflateY, e.y+e.height+itemMergeInflateY
	loZ, hiZ := e.z-hw, e.z+hw

	// Broad phase: items within a chunk column of this one (trackRange comfortably covers the
	// half-block reach). Snapshot the candidate slice before mutating (tryToMerge may discard an
	// entry) so the iteration is stable across an in-loop remove.
	candidates := t.entitiesNearAcrossRegions(e.x, e.z, trackRange)
	for _, other := range candidates {
		if other == e || !other.isItem {
			continue // vanilla predicate: e != this (getEntitiesOfClass excludes self via the lambda)
		}
		if !t.isItemMergable(other) {
			continue // predicate e.isMergable() + the loop-body re-check both require mergable
		}
		// Narrow phase: the other item box must intersect the inflated scan box on all three axes.
		ohw := other.width / 2
		if hiX <= other.x-ohw || other.x+ohw <= loX ||
			hiY <= other.y || other.y+other.height <= loY ||
			hiZ <= other.z-ohw || other.z+ohw <= loZ {
			continue
		}
		t.tryToMergeItems(e, other)
		// isRemoved(): THIS item merged its whole stack into the (larger) other and was discarded --
		// stop the scan (there is nothing left to merge from).
		if _, ok := t.cur().entities.get(e.id); !ok {
			break
		}
	}
}

// tryToMergeItems ports ItemEntity.tryToMerge(ItemEntity other): if the two stacks are mergable
// (same target owner and areMergable -- same item+components and combined count <= max), pour the
// SMALLER stack into the LARGER one. The item with the smaller count is drained into the one with
// the larger count (so the larger stack grows and the smaller is emptied/discarded). Tick-owned.
//
// Vanilla (javap ItemEntity.tryToMerge):
//
//	ItemStack s1 = getItem(), s2 = other.getItem();
//	if (Objects.equals(target, other.target) && areMergable(s1, s2)) {
//	    if (s2.getCount() < s1.getCount()) merge(this, s1, other, s2);
//	    else                               merge(other, s2, this, s1);
//	}
func (t *TickLoop) tryToMergeItems(e, other *Entity) {
	s1 := e.itemStack
	s2 := other.itemStack
	// target equality: Sulfur has no ItemEntity.target (thrower-restricted pickup) wired -- both are
	// the vanilla default null, so Objects.equals(null, null) is a cited constant-true. areMergable
	// carries the real gate (same item+components, combined count <= max).
	if !areItemsMergable(s1, s2) {
		return
	}
	if int(s2.Count) < int(s1.Count) {
		t.mergeItems(e, other) // other (smaller) pours INTO e (larger): e grows, other shrinks/discards
	} else {
		t.mergeItems(other, e) // e (smaller-or-equal) pours INTO other
	}
}

// areItemsMergable ports the static ItemEntity.areMergable(destination, source): the two stacks
// combine iff their counts sum within the max stack size AND they are the same item with the same
// components. Tick-owned read.
//
// Vanilla (javap ItemEntity.areMergable(ItemStack, ItemStack)):
//
//	if (source.getCount() + destination.getCount() > source.getMaxStackSize()) return false;
//	return ItemStack.isSameItemSameComponents(destination, source);
func areItemsMergable(destination, source component.SlotData) bool {
	if int(source.Count)+int(destination.Count) > maxStackSize(source) {
		return false
	}
	return stackSameItemSameComponents(destination, source)
}

// mergeItems ports the static ItemEntity.merge(destEntity, destStack, srcEntity, srcStack): move as
// much of the source stack into the destination stack as fits (up to 64 / the type max), keep the
// LARGER pickupDelay (max) and the YOUNGER age (min) on the destination, write the grown stack back
// onto the destination entity, and -- if the source stack emptied -- discard the source. Tick-owned.
//
// Vanilla (javap ItemEntity.merge(ItemEntity,ItemStack,ItemEntity,ItemStack) ->
// merge(ItemEntity, ItemStack, ItemStack) -> static merge(ItemStack dst, ItemStack src, int max)):
//
//	int placed = min(min(dst.getMaxStackSize(), 64) - dst.getCount(), src.getCount());
//	ItemStack grown = dst.copyWithCount(dst.getCount() + placed);
//	src.shrink(placed);
//	destEntity.setItem(grown);
//	destEntity.pickupDelay = max(destEntity.pickupDelay, srcEntity.pickupDelay);
//	destEntity.age         = min(destEntity.age,         srcEntity.age);
//	if (src.isEmpty()) sourceEntity.discard();
func (t *TickLoop) mergeItems(dest, src *Entity) {
	dstStack := dest.itemStack
	srcStack := src.itemStack

	// static merge(dst, src, 64): placed = min(min(maxStackSize, 64) - dst.count, src.count).
	limit := maxStackSize(dstStack)
	if limit > 64 {
		limit = 64 // the merge(..., 64) cap (a merge never grows the destination past a 64-stack)
	}
	placed := limit - int(dstStack.Count)
	if int(srcStack.Count) < placed {
		placed = int(srcStack.Count)
	}
	if placed < 0 {
		placed = 0
	}

	grown := stackCopyWithCount(dstStack, int(dstStack.Count)+placed) // dst.copyWithCount(count+placed)
	src.itemStack.Count -= pk.VarInt(placed)                          // src.shrink(placed)
	dest.itemStack = grown                                            // destEntity.setItem(grown)

	// pickupDelay = max(dst, src); age = min(dst, src) -- keep the more-restrictive delay and the
	// younger age on the surviving (destination) item.
	if src.pickupDelay > dest.pickupDelay {
		dest.pickupDelay = src.pickupDelay
	}
	if src.age < dest.age {
		dest.age = src.age
	}

	// if src.isEmpty() sourceEntity.discard(): the drained source is removed from the store.
	if src.itemStack.Count <= 0 {
		t.cur().entities.remove(src.id)
	}
}

// itemFireImmune ports ItemEntity.fireImmune(): the item entity is fire-immune iff its stack cannot
// be hurt by fire (a fire-resistant item -- netherite gear, the nether star) OR the base Entity is
// fire-immune. Sulfur does not yet tick fire/lava DAMAGE to item entities (ItemEntity has no
// hurtServer on the tick path and no health machine wired), so this override currently has no
// observable effect on tickItem; it is ported for structural fidelity and to become load-bearing
// once item fire damage lands. The fire-resistant read is a CITED stub (the item registry carries no
// DamageResistant/fire_resistant component yet) defaulting to the base Entity.fireImmune (false),
// structured to become a real component read later (CLAUDE.md: cite the default, never bake it away).
//
// Vanilla (javap ItemEntity.fireImmune):
//
//	return getItem().canBeHurtBy(damageSources().inFire()) ? super.fireImmune() : true;
func (t *TickLoop) itemFireImmune(e *Entity) bool {
	// canBeHurtBy(inFire): a normal stack CAN be hurt by fire -> defer to super.fireImmune(). A
	// fire-resistant stack (netherite/nether star) canBeHurtBy == false -> the item is fire-immune.
	if itemStackFireResistant(e.itemStack) {
		return true
	}
	return false // super.fireImmune(): the item entity has no fire-immune base in v1
}

// itemStackFireResistant is the CITED stub for ItemStack fire-resistance (the vanilla
// minecraft:damage_resistant { types: #minecraft:is_fire } component netherite gear and the nether
// star carry). The item registry does not yet extract that component, so this returns false (no
// item is fire-resistant yet); structured to become a real RawComponents/registry read later.
//   [ItemEntity.fireImmune reads getItem().canBeHurtBy(inFire); a fire-resistant stack returns false
//    there. Cite net.minecraft.world.item.ItemStack.canBeHurtBy / DataComponents.DAMAGE_RESISTANT.]
func itemStackFireResistant(_ component.SlotData) bool { return false }

// scanItemPickup ports Player.aiStep's item-collection loop (the touch path) + ItemEntity
// .playerTouch (FIX D): build the player's pickup AABB (the collision box inflated by
// (1.0, 0.5, 1.0)), and for every Item entity whose box intersects it, attempt the vanilla
// playerTouch — pick the stack up if pickupDelay == 0 and it fits the inventory, send the
// take-item animation, and remove the now-collected item. Tick-owned.
//
// Broad phase: the store's near() returns the items in the columns around the player, so the
// scan never walks all entities; the AABB intersection is the narrow phase on that candidate
// set. trackRange columns comfortably covers the (1-block-inflated) pickup reach.
func (t *TickLoop) scanItemPickup(p *tickPlayer) {
	// Player pickup AABB: the player collision box (playerWidth × playerHeight, feet at p.y)
	// inflated by (1.0, 0.5, 1.0) — Player.aiStep's getBoundingBox().inflate(1.0, 0.5, 1.0).
	hw := playerWidth/2 + itemPickupInflateXZ
	pLoX, pHiX := p.x-hw, p.x+hw
	pLoY, pHiY := p.y-itemPickupInflateY, p.y+playerHeight+itemPickupInflateY
	pLoZ, pHiZ := p.z-hw, p.z+hw

	for _, e := range t.entitiesNearAcrossRegions(p.x, p.z, trackRange) {
		if !e.isItem {
			continue // only dropped items are collectible here
		}

		// Item entity AABB (feet-anchored, width × height centered on x/z). Narrow-phase
		// intersection test on ALL THREE axes (half-open is fine for a pickup proximity check).
		ihw := e.width / 2
		if pHiX <= e.x-ihw || e.x+ihw <= pLoX ||
			pHiY <= e.y || e.y+e.height <= pLoY ||
			pHiZ <= e.z-ihw || e.z+ihw <= pLoZ {
			continue // boxes do not overlap on some axis: not in pickup range
		}

		t.playerTouchItem(p, e)
	}
}

// playerTouchItem ports ItemEntity.playerTouch(Player) (FIX D): if the item is pickable
// (pickupDelay == 0) and the stack fits the player's inventory (Inventory.add succeeds), the
// stack is taken — the take-item animation packet is broadcast (takeItem → LivingEntity.take →
// ClientboundTakeItemEntity) and, when the whole stack was absorbed, the item entity is
// discarded. A partial pickup (inventory filled some but not all) leaves the item with its
// remaining count, exactly as vanilla's leftover ItemStack does. Tick-owned.
//
// Vanilla (javap ItemEntity.playerTouch, server side):
//
//	if pickupDelay == 0 && (target == null || target == player.getUUID()) {
//	    int count = stack.getCount();
//	    if (player.getInventory().add(stack)) {
//	        player.take(this, count);          // sends ClientboundTakeItemEntity to trackers
//	        if (stack.isEmpty()) { discard(); stack.setCount(count); }
//	        ... awardStat / onItemPickup ...
//	    }
//	}
func (t *TickLoop) playerTouchItem(p *tickPlayer, e *Entity) {
	if e.pickupDelay != 0 {
		return // not yet pickable (delay still running, or the INFINITE sentinel)
	}

	count := int(e.itemStack.Count)
	if count <= 0 {
		return // empty stack: nothing to pick up (discarded by tickItem next tick anyway)
	}

	inv := ensureInventory(p)
	// Snapshot BEFORE the add so we can diff the slots that actually changed and broadcast each
	// one (vanilla AbstractContainerMenu.broadcastChanges → synchronizeSlotToRemote per changed
	// slot). This is the authoritative slot update the client needs to render the pickup (BUG-3).
	before := inv.snapshot()

	// Inventory.add mutates the stack's Count in place to the LEFTOVER that did not fit. add
	// returns true iff it absorbed at least one item (vanilla's `count < startCount`).
	if !t.inventoryAdd(p, inv, &e.itemStack) {
		return // inventory full / nothing fit: the item stays on the ground (vanilla behavior)
	}

	// player.take(this, count): broadcast the ClientboundTakeItemEntity animation (the item
	// flies into the player), then send the authoritative ClientboundContainerSetSlot for every
	// slot the pickup changed so the client always reflects the picked-up stack (BUG-3).
	t.takeItem(p, e, count)
	t.broadcastInventoryChanges(p, inv, before)

	// PROGRESS (advancements.go/stats.go): a pickup is an inventory change. Feed the
	// minecraft:inventory_changed advancement trigger with the picked-up item id (reaches
	// story/root crafting_table + the "obtain item" advancement class) and bump the
	// ITEM_PICKED_UP stat by the count taken. itemID indexes registryid.Item; an out-of-range
	// id is skipped by the trigger/stat helpers (never a panic). CITE: PickedUpItemTrigger +
	// Stats.ITEM_PICKED_UP + CriteriaTriggers.INVENTORY_CHANGED.
	if int(e.itemStack.ItemID) >= 0 && int(e.itemStack.ItemID) < len(registryid.Item) {
		picked := registryid.Item[e.itemStack.ItemID]
		t.triggerInventoryChanged(p, picked)
		if p.stats != nil {
			p.stats.increment(statKey{typeID: StatTypePickedUp, valueID: int32(e.itemStack.ItemID)}, int32(count))
		}
	}

	// If the whole stack was absorbed (leftover empty), discard the item entity now — the
	// tracker emits RemoveEntities next tick (it leaves near()). A partial pickup leaves the
	// item with its remaining count on the ground.
	if e.itemStack.Count <= 0 {
		// Phase-27 STEP-3 (N=2): remove from the item's OWNING region (it may live in either region;
		// scanItemPickup ran cross-region). owningRegion(nil) → skip (already gone — a safe no-op).
		if owner := t.owningRegion(e.id); owner != nil {
			owner.entities.remove(e.id)
		}
	}
}

// takeItem ports LivingEntity.take(Entity, int) (FIX D): broadcast ClientboundTakeItemEntity —
// the "item flies into the collector" animation — to every player tracking the item (here: the
// collecting player and any other nearby player). The packet carries (itemEntityId, collectorId,
// count). Tick-owned.
//
// Vanilla bytecode (javap LivingEntity.take): on the server, getChunkSource().sendToTrackingPlayers(
// item, new ClientboundTakeItemEntityPacket(item.getId(), this.getId(), count)).
func (t *TickLoop) takeItem(collector *tickPlayer, e *Entity, count int) {
	pkt := encodeTakeItemEntity(e.id, collector.entityID, count)
	// sendToTrackingPlayers: every player that currently TRACKS this item (p.tracked[e.id]) gets
	// the animation. The collector itself tracks the item (it was in its near() set to be picked
	// up), so its own client plays the pickup animation too. A player who never saw the item
	// (not tracking) does not need the packet.
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		if p.tracked != nil && p.tracked[e.id] {
			p.client.Send(pkt)
		}
	}
}

// inventoryAdd ports net.minecraft.world.entity.player.Inventory.add(ItemStack) (== add(-1, stack))
// for the v1 server-owned slot array (FIX D). It merges the dropped stack into existing matching
// stacks up to their max stack size, then drops any remainder into the first free slot, mutating
// stack.Count in place to the LEFTOVER and returning true iff it absorbed at least one item
// (vanilla's `stack.getCount() < startCount`). v1 items are never "damaged", so only the
// stackable branch of add(int, ItemStack) is ported (the damaged-tool single-slot branch is a
// later refinement and never reached by a block drop). Tick-owned.
//
// Vanilla add(int=-1, stack) stackable branch (javap Inventory.add):
//
//	startCount = stack.getCount();
//	do { stack.setCount(addResource(stack)); } while (!stack.isEmpty() && stack.getCount() < startCount);
//	return stack.getCount() < startCount;   // (ignoring the creative-infinite-materials path)
func (t *TickLoop) inventoryAdd(p *tickPlayer, inv *Inventory, stack *component.SlotData) bool {
	if stack.Count <= 0 {
		return false // empty stack: nothing to add (Inventory.add(int) isEmpty guard)
	}

	// Vanilla add(int, ItemStack) stackable loop (the do-while): each iteration captures the
	// current count as startCount, deposits one slot's worth via addResource (which mutates
	// stack.Count to the leftover), and repeats WHILE the stack is non-empty AND made progress
	// (count strictly dropped). The OUTERMOST startCount is what `return count < startCount`
	// compares against to report whether ANY item was absorbed.
	outerStart := int(stack.Count)
	for {
		startCount := int(stack.Count)
		stack.Count = pk.VarInt(t.addResource(inv, stack))
		if stack.Count <= 0 || int(stack.Count) >= startCount {
			break // emptied, or this pass placed nothing (no space): stop
		}
	}
	// add(int, ItemStack) returns stack.getCount() < startCount — i.e. the count dropped, so at
	// least one item was absorbed. (The creative hasInfiniteMaterials short-circuit is not a v1
	// path: survival player, finite materials.)
	return int(stack.Count) < outerStart
}

// VANILLA STORAGE-SLOT MAPPING (BUG-2). In 26.2 Inventory.items is the 36-entry
// getNonEquipmentItems() list — items[0..8] = hotbar, items[9..35] = main storage. getFreeSlot()
// and getSlotWithRemainingSpace() iterate items[0..35] ONLY; pickups never land in armor or the
// crafting grid. getItem(40) is the offhand (via EQUIPMENT_SLOT_MAPPING). [VERIFIED javap
// net.minecraft.world.entity.player.Inventory: getFreeSlot iterates items; getSlotWithRemainingSpace
// checks getItem(selected), then getItem(40), then iterates items; getItem(i<size) returns items[i].]
//
// Sulfur's inv.slots is the 46-entry WINDOW array (0=craft-result, 1-4=craft-grid, 5-8=armor,
// 9-35=main, 36-44=hotbar, 45=offhand) — the InventoryMenu slot layout. So vanilla's items-index
// space maps onto window slots as: items[0..8] (hotbar) -> window 36..44; items[9..35] (main) ->
// window 9..35; offhand (getItem(40)) -> window 45; selected (items index heldSlot) -> window
// 36+heldSlot. storageWindowSlots lists the 36 window slots that back items[0..35] IN ITEMS-INDEX
// ORDER (hotbar first, then main), so getFreeSlot / getSlotWithRemainingSpace iterate them in the
// exact vanilla order. The previous code iterated ALL 46 window slots, so pickups leaked into the
// crafting grid (slots 0-4) and armor (5-8) — the reported bug.
const (
	windowSlotOffhand = 45 // window slot for getItem(40) (offhand)
	windowHotbarFirst = 36 // window slot for items[0] (hotbar slot 0)
	windowMainFirst   = 9  // window slot for items[9] (main storage slot 0)
	windowMainCount   = 27 // items[9..35] -> window 9..35 (main storage)
	windowHotbarCount = 9  // items[0..8]  -> window 36..44 (hotbar)
)

// storageWindowSlots returns the 36 window-slot indices backing vanilla items[0..35], in
// items-index order (hotbar items[0..8] -> window 36..44, then main items[9..35] -> window 9..35).
// This is the precise iteration order of Inventory.getFreeSlot / getSlotWithRemainingSpace.
func storageWindowSlots() []int16 {
	out := make([]int16, 0, windowHotbarCount+windowMainCount)
	for i := 0; i < windowHotbarCount; i++ { // items[0..8] (hotbar)
		out = append(out, int16(windowHotbarFirst+i))
	}
	for i := 0; i < windowMainCount; i++ { // items[9..35] (main)
		out = append(out, int16(windowMainFirst+i))
	}
	return out
}

// addResource ports Inventory.addResource(ItemStack) → addResource(int, ItemStack): find a slot
// with remaining space for the stack's item (else the first free slot), deposit as much as the
// slot's max-stack-size headroom allows, and return the COUNT STILL UNPLACED. The deposited
// slot's count grows by the placed amount; a free slot is initialized to the item with the
// placed amount. Returns the full count when there is no slot at all (inventory full). Tick-owned.
//
// Vanilla (javap Inventory.addResource(int, ItemStack)):
//
//	slot = getSlotWithRemainingSpace(stack); if (slot == -1) slot = getFreeSlot();
//	if (slot == -1) return stack.getCount();
//	existing = getItem(slot); if existing.isEmpty() existing = stack.copyWithCount(0), setItem(...);
//	headroom = getMaxStackSize(existing) - existing.getCount();
//	placed = min(stack.getCount(), headroom);
//	if (placed == 0) return stack.getCount();
//	existing.grow(placed); return stack.getCount() - placed;
func (t *TickLoop) addResource(inv *Inventory, stack *component.SlotData) int {
	count := int(stack.Count)

	slot := slotWithRemainingSpace(inv, *stack)
	if slot == -1 {
		slot = freeSlot(inv)
	}
	if slot == -1 {
		return count // inventory full: nothing placed, the whole stack is unplaced
	}

	existing := inv.slots[slot]
	if existing.Count <= 0 {
		// Initialize a free slot to the item with count 0 (copyWithCount(0) + setItem), so the
		// grow below brings it to `placed`. ItemID + components mirror the dropped stack.
		existing = component.SlotData{ItemID: stack.ItemID, RawComponents: stack.RawComponents}
	}

	headroom := maxStackSize(existing) - int(existing.Count)
	placed := min(count, headroom)
	if placed == 0 {
		return count // the found slot is full at the type's max: nothing placed here
	}

	existing.Count = pk.VarInt(int(existing.Count) + placed) // ItemStack.grow(placed)
	inv.slots[slot] = existing
	return count - placed
}

// hasRemainingSpaceForItem ports Inventory.hasRemainingSpaceForItem(existing, toAdd): the existing
// slot can take more of toAdd iff it is non-empty, the same stackable item, and below its max
// stack size. v1 stacks by item id alone (component-aware stacking — e.g. enchanted tools never
// merging — is a later refinement; a block drop is a plain stackable). Tick-owned read.
//
// Vanilla (javap Inventory.hasRemainingSpaceForItem): !existing.isEmpty() &&
// ItemStack.isSameItemSameComponents(existing, toAdd) && existing.isStackable() &&
// existing.getCount() < getMaxStackSize(existing).
func hasRemainingSpaceForItem(existing, toAdd component.SlotData) bool {
	return existing.Count > 0 && existing.ItemID == toAdd.ItemID && int(existing.Count) < maxStackSize(existing)
}

// slotWithRemainingSpace ports Inventory.getSlotWithRemainingSpace(ItemStack) EXACTLY (BUG-2): it
// checks the SELECTED hotbar slot first, then the OFFHAND, then iterates items[0..35] (the 36 main
// + hotbar storage slots) IN ITEMS-INDEX ORDER. It NEVER returns a crafting (window 0-4) or armor
// (window 5-8) slot. Returns the window-slot index of the first slot with room for the item, or -1.
// Tick-owned read.
//
// Vanilla (javap Inventory.getSlotWithRemainingSpace):
//
//	if (hasRemainingSpaceForItem(getItem(selected), stack)) return selected;        // window 36+heldSlot
//	if (hasRemainingSpaceForItem(getItem(40),       stack)) return 40;              // window 45 (offhand)
//	for (i = 0; i < items.size(); i++)                                              // items[0..35]
//	    if (hasRemainingSpaceForItem(items.get(i), stack)) return i;
//	return -1;
func slotWithRemainingSpace(inv *Inventory, stack component.SlotData) int {
	// 1) selected hotbar slot (vanilla items index heldSlot -> window 36+heldSlot).
	selectedWindow := windowHotbarFirst + int(inv.heldSlot)
	if inv.heldSlot >= 0 && inv.heldSlot < windowHotbarCount &&
		hasRemainingSpaceForItem(inv.get(int16(selectedWindow)), stack) {
		return selectedWindow
	}
	// 2) offhand (vanilla getItem(40) -> Sulfur window slot 45).
	if hasRemainingSpaceForItem(inv.get(windowSlotOffhand), stack) {
		return windowSlotOffhand
	}
	// 3) items[0..35] in items-index order (hotbar 36..44, then main 9..35).
	for _, w := range storageWindowSlots() {
		if hasRemainingSpaceForItem(inv.get(w), stack) {
			return int(w)
		}
	}
	return -1
}

// freeSlot ports Inventory.getFreeSlot() EXACTLY (BUG-2): the window-slot index of the first EMPTY
// items[0..35] storage slot (in items-index order: hotbar 36..44, then main 9..35), or -1 when the
// 36 storage slots are full. It NEVER returns a crafting (0-4), armor (5-8), or offhand (45) slot —
// a pickup overflow can only land in main/hotbar storage, matching vanilla. Tick-owned read.
//
// Vanilla (javap Inventory.getFreeSlot): for (i=0; i<items.size(); i++) if (items.get(i).isEmpty())
// return i; return -1.
func freeSlot(inv *Inventory) int {
	for _, w := range storageWindowSlots() {
		if inv.get(w).Count <= 0 {
			return int(w)
		}
	}
	return -1
}

// maxStackSize ports Inventory.getMaxStackSize(ItemStack) for v1: the item registry's StackSize
// (data/item, generated from the jar — Stone/Cobblestone/etc. == 64). An unknown item id falls
// back to the vanilla default of 64 (Item.DEFAULT_MAX_STACK_SIZE). Tick-owned read.
func maxStackSize(stack component.SlotData) int {
	if it, ok := item.ByID[item.ID(stack.ItemID)]; ok && it.StackSize > 0 {
		return int(it.StackSize)
	}
	return 64
}
