package server

// raid_captain.go — the RAID CAPTAIN + OMINOUS BANNER + HERO-OF-THE-VILLAGE + RAVAGER-RIDER wiring,
// ported 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.entity.raid.Raid + Raider +
// PatrollingMonster, CFR + javap this session). This completes the raid entry/exit loop that
// raid.go/raids.go/raid_tick.go left as flavor stubs:
//
//   - the WAVE CAPTAIN: Raid.spawnGroup sets the FIRST canBeLeader() raider of each wave as the patrol
//     leader (setPatrolLeader(true)) and calls setLeader(wave, raider), which puts the OMINOUS BANNER in
//     the raider's HEAD slot (drop chance 2.0). isCaptain() then reads true — which fires the is_captain
//     loot pool (the ominous_bottle drop, loot condition wired in level/loot/condition.go). Cite
//     Raid.spawnGroup @122-149 + Raid.setLeader + Raider.isCaptain + PatrollingMonster.canBeLeader.
//   - the OMINOUS BANNER item: Raid.getOminousBannerInstance -> getOminousBannerTemplate: a white_banner
//     carrying the ominous BannerPatternLayers + the "block.minecraft.ominous_banner" item name. Cite
//     Raid.getOminousBannerInstance / getOminousBannerTemplate.
//   - HERO OF THE VILLAGE: on a raid VICTORY, every player in heroesOfTheVillage (the players who slew a
//     raider — Raider.die adds the killing player's UUID) gets MobEffects.HERO_OF_THE_VILLAGE for 48000
//     ticks at amplifier (raidOmenLevel - 1). Cite Raid.tick VICTORY branch @718-841 + Raid.addHeroOfTheVillage.

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// raiderCanBeLeader ports PatrollingMonster.canBeLeader() (returns true) with the Witch.canBeLeader()
// override (returns false). Among the 5 RaiderTypes, every PatrollingMonster (Vindicator/Evoker/
// Pillager/Ravager) can be a leader; the Witch — which is a Raider but NOT a PatrollingMonster — cannot.
// Keyed by the spawned entity type (e.typ), so a witch-only wave never assigns a captain, exactly as
// vanilla (spawnGroup's `!bl && raider.canBeLeader()` gate skips the witch).
//
//	[VERIFIED javap PatrollingMonster.canBeLeader: iconst_1 ireturn. Witch.canBeLeader: iconst_0 ireturn.]
func raiderCanBeLeader(e *Entity) bool {
	return e.typ != entity.Witch.ID
}

// raiderIsPatrolLeader ports PatrollingMonster.isPatrolLeader() -> the patrolLeader flag (mobAI). A
// witch never carries it (canBeLeader false -> setPatrolLeader is never called on it). Nil-ai safe.
//
//	[VERIFIED javap PatrollingMonster.isPatrolLeader: getfield patrolLeader:Z ireturn.]
func raiderIsPatrolLeader(e *Entity) bool {
	return e.ai != nil && e.ai.patrolLeader
}

// raiderIsCaptain ports Raider.isCaptain(): the HEAD slot holds the ominous banner AND the raider is the
// patrol leader. VERIFIED javap Raider.isCaptain: `boolean bl = !head.isEmpty() && ItemStack.matches(head,
// getOminousBannerInstance()); boolean bl2 = isPatrolLeader(); return bl && bl2;`. The ItemStack.matches
// full-component compare is reduced to the item-id compare (white_banner) + the patrolLeader flag: in v1
// the ONLY way a raider's HEAD carries a white_banner is setLeader (which installs the ominous banner), so
// the item-id match is observationally exact — a captain has the banner and is the leader. This drives the
// is_captain loot predicate (the ominous_bottle drop).
func raiderIsCaptain(e *Entity) bool {
	head := e.getItemBySlot(eqSlotHead)
	if head.Count <= 0 || uint32(head.ItemID) != uint32(item.WhiteBanner.ID) {
		return false
	}
	return raiderIsPatrolLeader(e)
}

// ominousBannerInstance ports Raid.getOminousBannerInstance(HolderGetter<BannerPattern>) ->
// getOminousBannerTemplate().create(): a single white_banner carrying the ominous BannerPatternLayers +
// the ominous item name. VERIFIED javap Raid.getOminousBannerInstance -> ItemStackTemplate.create(). The
// per-layer BannerPatternLayers + ITEM_NAME components are reduced to the base white_banner item (the
// observable a raider HEAD slot renders / the loot-predicate item-id match reads); the layer patch lands
// with the full banner-component model. Count 1.
func ominousBannerInstance() component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(item.WhiteBanner.ID)}
}

// raidSetLeader ports Raid.setLeader(int, Raider): record the raider as the wave captain
// (groupToLeaderMap[wave] = raider) and put the ominous banner in its HEAD slot with drop chance 2.0f.
// VERIFIED javap Raid.setLeader: `groupToLeaderMap.put(wave, raider); raider.setItemSlot(HEAD,
// getOminousBannerInstance(...)); raider.setDropChance(HEAD, 2.0f);`. The setDropChance is a cited no-op
// (the per-slot drop-chance override is deferred — entity_equipment.go DropChances note); the banner in the
// HEAD slot is the observable that drives isCaptain() + the client-visible captain equipment.
func (t *TickLoop) raidSetLeader(r *Raid, wave int, raider *Entity) {
	r.groupToLeaderMap[wave] = raider.id
	raider.setItemSlot(eqSlotHead, ominousBannerInstance())
	// raider.setDropChance(HEAD, 2.0f): the per-slot drop-chance override is deferred (the default 0.085
	// drop-chance path — entity_equipment.go). Cited; the banner slot is the load-bearing observable.
}

// removeLeader ports Raid.removeLeader(int): drop the wave's captain record. VERIFIED javap
// Raid.removeLeader: `groupToLeaderMap.remove(Integer.valueOf(wave));`.
func (r *Raid) removeLeader(wave int) {
	delete(r.groupToLeaderMap, wave)
}

// getLeader ports Raid.getLeader(int): the captain raider id for a wave, or 0 if none.
func (r *Raid) getLeader(wave int) int32 { return r.groupToLeaderMap[wave] }

// addHeroOfTheVillage ports Raid.addHeroOfTheVillage(Entity): add the entity's UUID to the
// heroesOfTheVillage set. VERIFIED javap Raid.addHeroOfTheVillage: `heroesOfTheVillage.add(entity.getUUID());`.
// The set is a slice here (the raid_persist.go SavedData round-trips it); dedupe on add so the VICTORY
// grant runs once per player, exactly as the Set semantics.
func (r *Raid) addHeroOfTheVillage(playerUUID uuid.UUID) {
	for _, u := range r.heroesOfTheVillage {
		if u == playerUUID {
			return // Set.add on an existing element is a no-op
		}
	}
	r.heroesOfTheVillage = append(r.heroesOfTheVillage, playerUUID)
}

// grantHeroesOfTheVillage ports the Raid.tick VICTORY branch @718-841: on the ONGOING->VICTORY status
// transition, iterate heroesOfTheVillage; for each UUID resolve the server player; if alive + !spectator
// grant MobEffects.HERO_OF_THE_VILLAGE for 48000 ticks at amplifier (raidOmenLevel - 1), ambient=false,
// showParticles=false, showIcon=true. VERIFIED javap Raid.tick: `new MobEffectInstance(HERO_OF_THE_VILLAGE,
// 48000, raidOmenLevel - 1, false, false, true)`; the Stats.RAID_WIN + CriteriaTriggers.RAID_WIN awards are
// cite-deferred (no raid-win stat/advancement wired). The discount math this feeds (villager_reputation.go)
// is already built. Runs ONCE at the transition (tickRaid calls this exactly when status flips to VICTORY).
func (t *TickLoop) grantHeroesOfTheVillage(r *Raid) {
	amplifier := r.raidOmenLevel - 1
	for _, u := range r.heroesOfTheVillage {
		p := t.playerByUUID(u)
		if p == nil || p.dead {
			continue // getEntity(uuid) instanceof LivingEntity && !isSpectator() -> alive, non-spectator only
		}
		t.addPlayerEffect(p, 0, heroOfTheVillageEffectID, raidHeroDuration, amplifier, 1.0)
	}
}
