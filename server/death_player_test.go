package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// death_player_test.go covers the PLAYER death-loot + XP drop (death_player.go): the 1:1 port of
// Player.die -> dropAllDeathLoot (dropEquipment drops+clears the inventory when !KEEP_INVENTORY;
// dropExperience awards the capped XP orbs and resets the player XP). All three observable outcomes
// are asserted: items drop as ItemEntity + inventory cleared, XP orbs spawn + level resets to 0, and
// keepInventory=true suppresses both.

// seedInvSlot puts a stack of `count` of the given item into an arbitrary player-inventory WINDOW slot
// (used to seed armor/main/offhand for the death-drop test).
func seedInvSlot(p *tickPlayer, slot int16, itemID item.ID, count int) {
	inv := ensureInventory(p)
	inv.set(slot, component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(itemID)})
}

// lethalDamage kills a player by applying maxHealth generic damage (health -> 0 drives die()).
func lethalDamage(loop *TickLoop, p *tickPlayer) {
	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), maxHealth)
}

// TestPlayerDeathDropsInventory: a player holding items in the main, armor and offhand slots dies with
// keepInventory off (the default). Every non-empty stack becomes an ItemEntity in the store and the
// inventory slots are cleared. Ports Player.dropEquipment -> Inventory.dropAll.
func TestPlayerDeathDropsInventory(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	// Seed three item-bearing slots: a hotbar/main slot (9), an armor slot (5), the offhand (45).
	seedInvSlot(p, 9, item.Stone.ID, 5)
	seedInvSlot(p, 5, item.Stone.ID, 1)
	seedInvSlot(p, 45, item.Stone.ID, 3)

	if got := countItemEntities(loop); got != 0 {
		t.Fatalf("pre-death item entities = %d, want 0", got)
	}

	lethalDamage(loop, p)

	if !p.dead {
		t.Fatalf("player not marked dead after lethal damage")
	}
	// Three non-empty stacks -> three dropped ItemEntity.
	if got := countItemEntities(loop); got != 3 {
		t.Fatalf("dropped item entities = %d, want 3", got)
	}
	// Every seeded slot is now empty (Inventory.dropAll clears each after the drop).
	inv := ensureInventory(p)
	for _, slot := range []int16{9, 5, 45} {
		if !stackEmpty(inv.get(slot)) {
			t.Fatalf("slot %d not cleared after death: %+v", slot, inv.get(slot))
		}
	}
}

// TestPlayerDeathDropsExperience: a player with XP levels dies with keepInventory off. XP orbs spawn
// (min(level*7,100) worth) and the player's XP fields reset to 0. Ports Player.getBaseExperienceReward
// (the 7/level, 100-cap) -> ExperienceOrb.award, then the Player.die XP reset.
func TestPlayerDeathDropsExperience(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	// 5 levels -> min(5*7,100) = 35 XP. 35 splits (getExperienceValue) into 17 + 17 + 1 = 3 orbs.
	p.experienceLevel = 5
	p.experienceProgress = 0.5
	p.totalExperience = 42

	if got := countOrbs(loop); got != 0 {
		t.Fatalf("pre-death orbs = %d, want 0", got)
	}

	lethalDamage(loop, p)

	if got := countOrbs(loop); got != 3 {
		t.Fatalf("XP orbs after death = %d, want 3 (17+17+1 for 35 XP)", got)
	}
	// The carried orb values sum to the dropped reward (35).
	sum := 0
	loop.forEachRegion(func(r *region) {
		for _, e := range r.entities.all() {
			if e != nil && e.isOrb {
				sum += e.xpValue
			}
		}
	})
	if sum != 35 {
		t.Fatalf("orb xp value sum = %d, want 35", sum)
	}
	// The player's XP resets to 0 (a not-keepInventory respawn = 0 XP).
	if p.experienceLevel != 0 || p.experienceProgress != 0 || p.totalExperience != 0 {
		t.Fatalf("XP not reset: level=%d progress=%v total=%d, want 0/0/0",
			p.experienceLevel, p.experienceProgress, p.totalExperience)
	}
}

// TestPlayerDeathXPCap: a high-level player's death XP is capped at 100 (min(level*7,100)). 20 levels
// -> 140 uncapped, capped to 100. Confirms the Player.getBaseExperienceReward min(...,100).
func TestPlayerDeathXPCap(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.experienceLevel = 20 // 20*7 = 140, capped to 100

	lethalDamage(loop, p)

	sum := 0
	loop.forEachRegion(func(r *region) {
		for _, e := range r.entities.all() {
			if e != nil && e.isOrb {
				sum += e.xpValue
			}
		}
	})
	if sum != 100 {
		t.Fatalf("capped death XP = %d, want 100", sum)
	}
}

// TestPlayerDeathKeepInventory: with the keepInventory gamerule ON, a player who dies drops NOTHING
// (no item entities, no XP orbs), keeps its inventory, and keeps its XP. Ports the KEEP_INVENTORY gate
// on both dropEquipment and getBaseExperienceReward / the XP reset.
func TestPlayerDeathKeepInventory(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gamerules = newGameRules()
	loop.gamerules.setBool(ruleKeepInventory, true)

	p := combatPlayer(loop, 1)
	seedInvSlot(p, 9, item.Stone.ID, 5)
	p.experienceLevel = 5
	p.experienceProgress = 0.5
	p.totalExperience = 42

	lethalDamage(loop, p)

	if got := countItemEntities(loop); got != 0 {
		t.Fatalf("keepInventory: dropped item entities = %d, want 0", got)
	}
	if got := countOrbs(loop); got != 0 {
		t.Fatalf("keepInventory: XP orbs = %d, want 0", got)
	}
	// Inventory intact.
	if stackEmpty(ensureInventory(p).get(9)) {
		t.Fatalf("keepInventory: slot 9 was cleared, want intact")
	}
	// XP intact.
	if p.experienceLevel != 5 || p.experienceProgress != 0.5 || p.totalExperience != 42 {
		t.Fatalf("keepInventory: XP changed: level=%d progress=%v total=%d, want 5/0.5/42",
			p.experienceLevel, p.experienceProgress, p.totalExperience)
	}
}
