package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shield_blocking_test.go exercises the SHIELD / minecraft:blocks_attacks blocking path (Fable E-2):
// a player actively blocking with a shield past the block delay takes reduced/zero damage from a front
// melee hit; a hit from behind is not blocked; a #bypasses_shield source ignores the shield; the shield
// takes durability; a not-yet-past-block_delay hold does not block.

// equipShieldBlocking sets the player up as actively blocking with a shield in the mainhand: the shield
// is in the held inventory slot, useItem == the shield, and useItemRemaining is set so that
// (getUseDuration 72000 - useItemRemaining) >= blockDelayTicks (5). heldTicks controls the elapsed hold.
func equipShieldBlocking(p *tickPlayer, heldTicks int32) {
	// The shield stack carries its items.json default durability components (max_damage 336, damage 0):
	// v1 reads durability off the stack patch, matching the vanilla default-component merge.
	shield := component.SlotData{Count: 1, ItemID: pk.VarInt(shieldItemID)}
	shield = setStackMaxDamage(shield, 336)
	shield = setStackDamageValue(shield, 0)
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase+inv.heldSlot, shield)
	p.useItem = shield
	p.useItemHand = interactionHandMain
	p.useItemRemaining = blocksAttacksItemUseDuration - heldTicks
}

// addMeleeAttacker places a mob attacker in the current region at (x, z) and returns a mob_attack
// damageSource naming it, so applyItemBlocking can resolve getSourcePosition() for the angle check.
func addMeleeAttacker(loop *TickLoop, id int32, x, z float64) damageSource {
	e := &Entity{id: id, typ: entity.Zombie.ID, x: x, y: 64, z: z}
	loop.only().entities.add(e)
	return damageSourceMobAttack(id)
}

func TestShieldBlocksFrontMelee(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.x, p.z = 0, 0
	p.headYaw = 0 // looking +Z
	equipShieldBlocking(p, 20)
	// Attacker in front (+Z): dot(look, toSource) == 1, angle 0 < 90deg cone -> full block.
	src := addMeleeAttacker(loop, 2, 0, 5)
	loop.applyDamage(p, src, 6)
	if p.health != maxHealth {
		t.Fatalf("front-blocked hit: health = %v, want %v (full block)", p.health, maxHealth)
	}
	// The shield took durability: itemDamage.apply(6) = floor(1 + 1*6) = 7 (6 >= threshold 3).
	inv := ensureInventory(p)
	shield := inv.get(hotbarMenuSlotBase + inv.heldSlot)
	if stackDamageValue(shield) != 7 {
		t.Fatalf("shield damage value = %d, want 7 (itemDamage.apply(6))", stackDamageValue(shield))
	}
}

func TestShieldDoesNotBlockFromBehind(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.x, p.z = 0, 0
	p.headYaw = 0 // looking +Z
	equipShieldBlocking(p, 20)
	// Attacker behind (-Z): dot(look, toSource) == -1, angle = PI > 90deg cone -> NOT blocked.
	src := addMeleeAttacker(loop, 2, 0, -5)
	loop.applyDamage(p, src, 6)
	if p.health != maxHealth-6 {
		t.Fatalf("behind hit: health = %v, want %v (no block)", p.health, maxHealth-6)
	}
}

func TestShieldBypassedBySource(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.x, p.z = 0, 0
	p.headYaw = 0
	equipShieldBlocking(p, 20)
	// A #minecraft:bypasses_shield source (generic) from the front still lands fully.
	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 6)
	if p.health != maxHealth-6 {
		t.Fatalf("bypasses_shield hit: health = %v, want %v (shield ignored)", p.health, maxHealth-6)
	}
}

func TestShieldNotPastBlockDelayDoesNotBlock(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.x, p.z = 0, 0
	p.headYaw = 0
	// Held only 3 ticks: (72000 - useItemRemaining) == 3 < blockDelayTicks 5 -> not yet blocking.
	equipShieldBlocking(p, 3)
	if isBlocking(p) {
		t.Fatalf("isBlocking true before block delay elapsed")
	}
	src := addMeleeAttacker(loop, 2, 0, 5)
	loop.applyDamage(p, src, 6)
	if p.health != maxHealth-6 {
		t.Fatalf("pre-delay hit: health = %v, want %v (no block)", p.health, maxHealth-6)
	}
}

func TestShieldNotUsingDoesNotBlock(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.x, p.z = 0, 0
	p.headYaw = 0
	// Shield in hand but NOT being used (no right-click) -> isUsingItem false -> no block.
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase+inv.heldSlot, component.SlotData{Count: 1, ItemID: pk.VarInt(shieldItemID)})
	if isBlocking(p) {
		t.Fatalf("isBlocking true without using the item")
	}
	src := addMeleeAttacker(loop, 2, 0, 5)
	loop.applyDamage(p, src, 6)
	if p.health != maxHealth-6 {
		t.Fatalf("not-using hit: health = %v, want %v (no block)", p.health, maxHealth-6)
	}
}
