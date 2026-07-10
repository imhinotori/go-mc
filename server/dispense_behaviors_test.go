package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dispense_behaviors_test.go -- validation gates for the DispenserBlock.DISPENSER_REGISTRY special
// behaviors (dispense_behaviors.go). Each asserts the ported behavior against the unobfuscated 26.2 jar:
//   - a dispenser holding an ARROW shoots an AbstractArrow (not a loose item) out the FACING;
//   - SNOWBALL / EGG shoot their throwable projectile out the FACING;
//   - FLINT_AND_STEEL ignites a fire in the cell in front and wears the tool by 1;
//   - FLINT_AND_STEEL primes a TNT block in front (and clears the block);
//   - a non-registered item (cobblestone) still falls through to the DEFAULT loose eject.

// dispenseProjectileFacing seeds a dispenser at pos facing `facing` with one `it` in slot 0, fires it, and
// returns the (projectile-flag predicate) count of new entities. Uses newDispenserLoop (dispenser_test.go).
func dispenseProjectileFacing(t *testing.T, facing block.Direction, it item.ID) (*TickLoop, pk.Position, *dispenserBE) {
	t.Helper()
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 5, Y: 64, Z: 5}
	disp := block.ToStateID[block.Dispenser{Facing: facing, Triggered: false}]
	mgr.SetBlock(pos, disp, dimMinY)
	d := loop.resolveDispenser(pos, disp)
	d.items[0] = component.SlotData{Count: 3, ItemID: pk.VarInt(it)}
	return loop, pos, d
}

// TestDispenseArrowShootsProjectile locks the ARROW ProjectileDispenseBehavior: a dispenser holding arrows,
// fired, spawns an AbstractArrow (isArrow) rather than a loose ItemEntity, shot out the +X (EAST) face, and
// the source slot shrinks by 1. CITE: ProjectileDispenseBehavior.execute + ArrowItem.asProjectile.
func TestDispenseArrowShootsProjectile(t *testing.T) {
	loop, pos, d := dispenseProjectileFacing(t, block.East, item.Arrow.ID)
	disp, _ := loop.only().world.GetBlock(pos, dimMinY)

	before := loop.only().entities.len()
	loop.dispenseFrom(pos, disp)

	var arrow *Entity
	for _, e := range loop.only().entities.all() {
		if e.isArrow {
			arrow = e
			break
		}
	}
	if arrow == nil {
		t.Fatal("dispensed arrow did not spawn an AbstractArrow (isArrow) entity")
	}
	// No loose ItemEntity should appear (the projectile is NOT the default eject).
	for _, e := range loop.only().entities.all() {
		if e.isItem {
			t.Fatal("dispensed arrow wrongly spawned a loose ItemEntity (default eject fired)")
		}
	}
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("dispensed arrow: %d new entities, want 1", got-before)
	}
	// Shot out the +X (EAST) face: vx dominant and positive.
	if arrow.vx <= 0 || arrow.vx < absF(arrow.vy) || arrow.vx < absF(arrow.vz) {
		t.Fatalf("dispensed arrow velocity (%v,%v,%v) not dominantly +X (EAST)", arrow.vx, arrow.vy, arrow.vz)
	}
	// Slot shrank by 1 (stack.shrink(1)).
	if d.items[0].Count != 2 {
		t.Fatalf("arrow dispenser slot count = %d, want 2 (shrink(1) of 3)", d.items[0].Count)
	}
}

// TestDispenseSnowballShootsThrowable locks the SNOWBALL ProjectileDispenseBehavior: a dispenser holding
// snowballs spawns a throwable (isThrowable, kind snowball), not a loose item. CITE:
// ProjectileDispenseBehavior.execute + SnowballItem.asProjectile.
func TestDispenseSnowballShootsThrowable(t *testing.T) {
	loop, pos, d := dispenseProjectileFacing(t, block.West, item.Snowball.ID)
	disp, _ := loop.only().world.GetBlock(pos, dimMinY)

	loop.dispenseFrom(pos, disp)

	var thr *Entity
	for _, e := range loop.only().entities.all() {
		if e.isThrowable && e.throwableKind == throwSnowball {
			thr = e
			break
		}
	}
	if thr == nil {
		t.Fatal("dispensed snowball did not spawn a throwable snowball projectile")
	}
	for _, e := range loop.only().entities.all() {
		if e.isItem {
			t.Fatal("dispensed snowball wrongly spawned a loose ItemEntity")
		}
	}
	// Shot out the -X (WEST) face: vx dominant and negative.
	if thr.vx >= 0 || absF(thr.vx) < absF(thr.vy) || absF(thr.vx) < absF(thr.vz) {
		t.Fatalf("dispensed snowball velocity (%v,%v,%v) not dominantly -X (WEST)", thr.vx, thr.vy, thr.vz)
	}
	if d.items[0].Count != 2 {
		t.Fatalf("snowball dispenser slot count = %d, want 2", d.items[0].Count)
	}
}

// TestDispenseFlintAndSteelIgnitesFire locks the FLINT_AND_STEEL FlintAndSteelDispenseItemBehavior fire
// branch: a dispenser facing UP over a sturdy floor, holding flint_and_steel, lights a FIRE in the cell in
// front (above) and wears the tool by 1. CITE: FlintAndSteelDispenseItemBehavior.execute (fire branch).
func TestDispenseFlintAndSteelIgnitesFire(t *testing.T) {
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 5, Y: 64, Z: 5}
	// Dispenser facing UP; the ignite target is pos.above() == (5,65,5).
	disp := block.ToStateID[block.Dispenser{Facing: block.Up, Triggered: false}]
	mgr.SetBlock(pos, disp, dimMinY)
	// A sturdy floor UNDER the target so BaseFireBlock.getState().canSurvive() holds (the target's below is
	// the dispenser at (5,64,5), which is a full sturdy block).
	target := pk.Position{X: 5, Y: 65, Z: 5}

	d := loop.resolveDispenser(pos, disp)
	// A real flint_and_steel carries max_damage 64 + damage 0 from its item default components; the bare
	// test SlotData has none, so seed them (a damageable stack needs both present -- enchant_helper.go
	// stackIsDamageableItem). This is the faithful fresh-tool state, not a behavior tweak.
	fas := component.SlotData{Count: 1, ItemID: pk.VarInt(item.FlintAndSteel.ID)}
	fas = setStackMaxDamage(fas, 64)
	fas = setStackDamageValue(fas, 0)
	d.items[0] = fas

	loop.dispenseFrom(pos, disp)

	lit, _ := mgr.GetBlock(target, dimMinY)
	if !block.IsFire(lit) {
		t.Fatalf("flint_and_steel dispenser did not light a fire at %v (got state %d)", target, lit)
	}
	// The tool wore by 1 (damage 0 -> 1) OR was consumed; the slot must not still be a pristine count-1
	// undamaged flint_and_steel. Assert the damage advanced.
	got := d.items[0]
	if stackEmpty(got) {
		t.Fatal("flint_and_steel unexpectedly consumed on a single successful ignite (should just wear by 1)")
	}
	if stackDamageValue(got) != 1 {
		t.Fatalf("flint_and_steel damage = %d after one ignite, want 1 (hurtAndBreak(1))", stackDamageValue(got))
	}
}

// TestDispenseFlintAndSteelPrimesTnt locks the FLINT_AND_STEEL TntBlock branch: a dispenser facing a TNT
// block ignites it -> the TNT block is cleared to AIR and a PrimedTnt entity spawns. CITE:
// FlintAndSteelDispenseItemBehavior.execute (TntBlock.prime branch).
func TestDispenseFlintAndSteelPrimesTnt(t *testing.T) {
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 5, Y: 64, Z: 5}
	disp := block.ToStateID[block.Dispenser{Facing: block.East, Triggered: false}]
	mgr.SetBlock(pos, disp, dimMinY)
	target := pk.Position{X: 6, Y: 64, Z: 5}
	mgr.SetBlock(target, block.DefaultStateID["minecraft:tnt"], dimMinY)

	d := loop.resolveDispenser(pos, disp)
	d.items[0] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.FlintAndSteel.ID)}

	before := loop.only().entities.len()
	loop.dispenseFrom(pos, disp)

	cleared, _ := mgr.GetBlock(target, dimMinY)
	if !block.IsAir(cleared) {
		t.Fatalf("flint_and_steel did not clear the primed TNT block to air (state %d)", cleared)
	}
	var primed *Entity
	for _, e := range loop.only().entities.all() {
		if e.isTnt {
			primed = e
			break
		}
	}
	if primed == nil {
		t.Fatal("flint_and_steel on a TNT block did not spawn a PrimedTnt entity")
	}
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("TNT prime spawned %d new entities, want 1 PrimedTnt", got-before)
	}
}

// TestDispenseNonRegisteredFallsThrough locks getDispenseMethod's DEFAULT fallback: an item with NO
// registered behavior (cobblestone) still ejects as a loose ItemEntity via the DEFAULT behavior. CITE:
// DispenserBlock.getDispenseMethod (registry miss -> DEFAULT).
func TestDispenseNonRegisteredFallsThrough(t *testing.T) {
	loop, pos, d := dispenseProjectileFacing(t, block.East, item.Cobblestone.ID)
	disp, _ := loop.only().world.GetBlock(pos, dimMinY)

	before := loop.only().entities.len()
	loop.dispenseFrom(pos, disp)

	var loose *Entity
	for _, e := range loop.only().entities.all() {
		if e.isItem {
			loose = e
			break
		}
	}
	if loose == nil {
		t.Fatal("a non-registered item (cobblestone) did not fall through to the DEFAULT loose eject")
	}
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("cobblestone dispense: %d new entities, want 1 loose item", got-before)
	}
	if d.items[0].Count != 2 {
		t.Fatalf("cobblestone dispenser slot count = %d, want 2 (split(1) of 3)", d.items[0].Count)
	}
}
