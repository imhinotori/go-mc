package server

// death_player.go — the PLAYER death-loot + XP tail: the literal 1:1 port of the death-drop path
// net.minecraft.world.entity.player.Player.die(DamageSource) drives through its super
// (net.minecraft.world.entity.LivingEntity.die -> dropAllDeathLoot -> dropEquipment + dropExperience),
// plus the Player.die XP-field reset. Verified method-for-method against temp/cache/26.2-inner.jar via
// javap -c -p this session. No GPL source is pasted; the algorithm is re-expressed in Go, but the
// call chain, the gates, and the numeric ops (the min(level*7,100) cap, the KEEP_INVENTORY gates, the
// dropAll iteration, the XP reset) are IDENTICAL to the bytecode.
//
// Faithful bytecode trace (javap this session):
//
//	Player.die(source): super.die (Avatar.die -> LivingEntity.die); when ServerLevel && !isSpectator:
//	    dropAllDeathLoot(level, source) -> dropEquipment(level) + dropExperience(level, attacker).
//	Player.dropEquipment(level): super.dropEquipment; if (!KEEP_INVENTORY) {
//	    destroyVanishingCursedItems(); inventory.dropAll(); }   // dropAll -> player.drop(stack,true,false)
//	LivingEntity.dropExperience: a player is isAlwaysExperienceDropper()==TRUE, so it ALWAYS awards
//	    ExperienceOrb.award(level, position(), getExperienceReward(level, attacker)); amount gated to 0
//	    by getBaseExperienceReward under KEEP_INVENTORY / spectator.
//	Player.getBaseExperienceReward(level): if (KEEP_INVENTORY || isSpectator) return 0;
//	    return Math.min(experienceLevel * 7, 100);   // the death-XP CAP: 7 per level, max 100.
//	    [VERIFIED javap: getfield experienceLevel; bipush 7; imul; bipush 100; Math.min(II)I; ireturn;
//	     gated to iconst_0 on KEEP_INVENTORY||spectator.  Player.isAlwaysExperienceDropper: iconst_1.]
//	Player.die tail (XP reset): experienceLevel = experienceProgress = totalExperience = 0. Sulfur
//	    reuses the tickPlayer across respawn, so the reset lives here, gated on the SAME KEEP_INVENTORY
//	    condition (a not-keepInventory respawn = 0 XP).
//
// region note: die(p) runs on the tick goroutine already in the owning region context (the same path
// playerDrop/spawnBlockDrop use t.cur() from), so the item + orb spawns route through t.cur().

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
)

// dropAllDeathLootPlayer is the port of the PLAYER branch of LivingEntity.die -> dropAllDeathLoot:
// dropEquipment (the inventory drop, KEEP_INVENTORY-gated) then dropExperience (the XP orbs +
// getBaseExperienceReward cap), followed by the Player.die XP-field reset. Called ONCE per death from
// die(p) when the player is on a ServerLevel and not a spectator (both cited constant-true in v1).
// Tick-owned.
func (t *TickLoop) dropAllDeathLootPlayer(p *tickPlayer) {
	// dropEquipment(level): Player override; super.dropEquipment (no inventory drop) then, when
	// !KEEP_INVENTORY, destroyVanishingCursedItems + inventory.dropAll.
	t.dropPlayerEquipment(p)

	// dropExperience(level, attacker): a player is isAlwaysExperienceDropper()==true, so it ALWAYS
	// awards; the amount is min(level*7,100) gated to 0 by getBaseExperienceReward on KEEP_INVENTORY.
	t.dropPlayerExperience(p)
}

// dropPlayerEquipment is the port of net.minecraft.world.entity.player.Player.dropEquipment(ServerLevel):
// super.dropEquipment (Avatar.dropEquipment, a v1 no-op) then, when !KEEP_INVENTORY,
// destroyVanishingCursedItems (a cited v1 no-op: no PREVENT_EQUIPMENT_DROP enchant subsystem, so the
// vanilla loop removes nothing) then inventory.dropAll. The KEEP_INVENTORY gate reads the loop's real
// gamerules store (gameRule(ruleKeepInventory), default FALSE = drop).
//
//	[VERIFIED javap Player.dropEquipment: super.dropEquipment; getGameRules().get(KEEP_INVENTORY); ifne
//	 return; destroyVanishingCursedItems(); inventory.dropAll().]
//	[VERIFIED javap Inventory.dropAll: for each compartment, for each stack, if !isEmpty
//	 player.drop(stack, true, false); compartment.set(i, EMPTY).]
//
// In Sulfur's 46-slot menu-window inventory the item-bearing Inventory slots are the armor (5-8),
// main+hotbar (9-44), and offhand (45); the crafting result/grid slots (0-4) belong to the
// InventoryMenu, not to Inventory, so dropAll never touches them. Tick-owned.
func (t *TickLoop) dropPlayerEquipment(p *tickPlayer) {
	// !level.getGameRules().get(KEEP_INVENTORY): default false -> drop.
	if t.gameRule(ruleKeepInventory) {
		return // keepInventory on: keep everything (no drop, no clear).
	}

	inv := ensureInventory(p)
	for slot := int16(deathDropFirstSlot); slot <= int16(deathDropLastSlot); slot++ {
		stack := inv.get(slot)
		if stackEmpty(stack) {
			continue
		}
		// player.drop(stack, true, false): spawn the ItemEntity near the player with the vanilla toss
		// (playerDrop -> NewItemEntity, the same primitive the Q-throw / container-close drops use).
		t.playerDrop(p, stack, true)
		// compartment.set(i, ItemStack.EMPTY): clear the slot.
		inv.set(slot, component.SlotData{Count: 0})
	}

	// Broadcast the now-empty inventory so the client's HUD reflects the cleared state (the client never
	// clears its own inventory on death; it waits for the authoritative ContainerSetContent). sendContent
	// bumps the state id and pushes ClientboundContainerSetContent for window 0.
	t.sendContent(p)
}

// deathDropFirstSlot / deathDropLastSlot bound the item-bearing Inventory slots in Sulfur's 46-slot
// player menu window: armor (5-8), main+hotbar (9-44), offhand (45). Slots 0-4 (craft result + 2x2 grid)
// are InventoryMenu slots, NOT part of Inventory, so dropAll skips them. See inventory.go's slot layout.
const (
	deathDropFirstSlot = 5  // first armor slot
	deathDropLastSlot  = 45 // offhand slot (offHandMenuSlot)
)

// dropPlayerExperience is the port of the PLAYER path of LivingEntity.dropExperience composed with
// Player.getBaseExperienceReward and the Player.die XP-field reset:
//
//	// a player is isAlwaysExperienceDropper()==true, so it ALWAYS awards:
//	ExperienceOrb.award(level, position(), getExperienceReward(level, attacker));
//	// getExperienceReward == getBaseExperienceReward (processMobExperience is a no-op for a player kill):
//	//   if (KEEP_INVENTORY || isSpectator) return 0; else return min(experienceLevel*7, 100);
//	// then the die() XP reset: experienceLevel = experienceProgress = totalExperience = 0.
//
// The reward is capped at 100 (7 per level): the vanilla death-XP cap. isSpectator is a cited
// constant-false (v1 has no spectator death path). The XP reset leaves the respawned player with 0 XP
// exactly as vanilla (a fresh ServerPlayer respawns with 0 XP when not keepInventory). Tick-owned.
func (t *TickLoop) dropPlayerExperience(p *tickPlayer) {
	// Player.getBaseExperienceReward: KEEP_INVENTORY (|| isSpectator, cited false) -> 0.
	reward := 0
	if !t.gameRule(ruleKeepInventory) {
		// min(experienceLevel * 7, 100): the death-XP cap (7 per level, max 100).
		reward = int(p.experienceLevel) * 7
		if reward > 100 {
			reward = 100
		}
	}

	// ExperienceOrb.award(level, position(), reward): split into orb-sized chunks and spawn each at the
	// player position. award() returns immediately for value <= 0, so a keepInventory / 0-level player
	// spawns no orbs.
	if reward > 0 {
		t.awardPlayerExperienceOrbs(p, reward)
	}

	// Player.die XP-field reset (experienceProgress = experienceLevel = totalExperience = 0). Gated on
	// the SAME !KEEP_INVENTORY condition: with keepInventory ON, vanilla keeps the XP. Push the
	// authoritative ClientboundSetExperience so the client XP bar zeroes out.
	if !t.gameRule(ruleKeepInventory) {
		p.experienceProgress = 0
		p.experienceLevel = 0
		p.totalExperience = 0
		t.sendExperience(p)
	}
}

// awardPlayerExperienceOrbs is the player-position sibling of awardExperienceOrbs (death_mob.go): the
// port of ExperienceOrb.award -> awardWithDirection's while(value>0) split loop over the descending
// getExperienceValue cap table, spawning one ExperienceOrb per chunk at the player position. v1 has no
// orb-merge (tryMergeToExisting stub == false), so each chunk spawns a fresh orb into t.cur(): the same
// region-add path playerDrop and block drops use during the player tick. Tick-owned.
//
//	[VERIFIED javap ExperienceOrb.awardWithDirection: while(value>0){ chunk=getExperienceValue(value);
//	 value -= chunk; if(!tryMergeToExisting(...)) addFreshEntity(new ExperienceOrb(...chunk)); }.]
func (t *TickLoop) awardPlayerExperienceOrbs(p *tickPlayer, value int) {
	owner := t.cur()
	for value > 0 {
		chunk := getExperienceValue(value)
		value -= chunk
		// tryMergeToExisting: v1 stub (false): no orb-merge subsystem, so always spawn a fresh orb.
		orb := NewEntity(t.idAlloc.AllocID(), entity.ExperienceOrb, p.x, p.y, p.z)
		// Mark the orb so the orb tick (followNearbyPlayer + pickup) drives it and record the carried
		// value (the same WR-06 wiring death_mob.go's awardExperienceOrbs uses).
		orb.isOrb = true
		orb.xpValue = chunk
		owner.entities.add(orb)
	}
}
