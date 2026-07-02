package server

// villager_reputation.go — the VILLAGER REPUTATION -> TRADE-PRICE economy, ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this session). It wires the gossip container
// (gossip.go) into the merchant offers:
//
//   Villager.getPlayerReputation(player)     -> villagerGetPlayerReputation
//   Villager.updateSpecialPrices(player)     -> villagerUpdateSpecialPrices  (reputation + hero discounts)
//   Villager.resetSpecialPrices()            -> villagerResetSpecialPrices   (trade-stop reset)
//   Villager.updateDemand()  [restock]       -> villagerUpdateDemand
//   Villager.onReputationEventFrom(type,src) -> villagerOnReputationEventFrom (TRADE/HURT/KILLED/CURED)
//
// The reputation is a WEIGHTED SUM (gossipContainer.getReputation with an all-types predicate); a positive
// reputation DISCOUNTS every offer (specialPriceDiff -= floor(reputation * priceMultiplier)); the
// HERO_OF_THE_VILLAGE effect adds a second discount. Tick-owned (TICK-05): every entry point runs on the
// tick goroutine off the merchant/interact path.

import (
	"github.com/google/uuid"
)

// heroOfTheVillageEffectID is the effect key read by updateSpecialPrices' hero-of-the-village discount
// (MobEffects.HERO_OF_THE_VILLAGE). The raid-WIN grant of this effect is DEFERRED (raid_tick.go cites the
// missing player-hero tracking), so a player normally has NO such effect and the hero loop is a faithful
// no-op — but the read is LIVE (villagerHeroOfVillageAmplifier reads p.activeEffects), so the moment the
// raid-win grant is wired the discount activates with zero change here. CITE Villager.updateSpecialPrices
// (player.hasEffect(HERO_OF_THE_VILLAGE)) + MobEffects.HERO_OF_THE_VILLAGE.
const heroOfTheVillageEffectID = "minecraft:hero_of_the_village"

// villagerEnsureGossips lazily creates the villager's gossip container (Villager.gossips is a final field
// in the jar; v1 allocates on first use so non-villager entities carry no map).
func villagerEnsureGossips(e *Entity) *gossipContainer {
	if e.villagerGossips == nil {
		e.villagerGossips = newGossipContainer()
	}
	return e.villagerGossips
}

// villagerGetPlayerReputation ports Villager.getPlayerReputation(player):
//
//	return this.gossips.getReputation(player.getUUID(), t -> true);
//
// The predicate `t -> true` sums EVERY gossip type (major/minor +/-, trading) weighted. CITE
// Villager.getPlayerReputation.
func villagerGetPlayerReputation(e *Entity, playerUUID uuid.UUID) int {
	if e.villagerGossips == nil {
		return 0
	}
	return e.villagerGossips.getReputation(playerUUID, func(*gossipType) bool { return true })
}

// villagerUpdateSpecialPrices ports net.minecraft.world.entity.npc.villager.Villager.updateSpecialPrices
// (player). Called from startTrading BEFORE setTradingPlayer (so the freshly-opened menu shows the
// discounted prices). Two independent discount loops (jar body):
//
//	int reputation = getPlayerReputation(player);
//	if (reputation != 0)
//	    for offer: offer.addToSpecialPriceDiff(-Mth.floor((float)reputation * offer.getPriceMultiplier()));
//	if (player.hasEffect(HERO_OF_THE_VILLAGE)) {
//	    amplifier = getEffect(HERO_OF_THE_VILLAGE).getAmplifier();
//	    for offer:
//	        double modifier = 0.3 + 0.0625 * (double)amplifier;
//	        int costReduction = (int)Math.floor(modifier * (double)offer.getBaseCostA().getCount());
//	        offer.addToSpecialPriceDiff(-Math.max(costReduction, 1));
//	}
//
// heroAmplifier < 0 signals "no HERO_OF_THE_VILLAGE effect" (the hasEffect gate is false), so the hero loop
// is skipped. CITE Villager.updateSpecialPrices.
//
//	[VERIFIED CFR Villager.updateSpecialPrices this session: reputation != 0 loop with
//	 -Mth.floor((float)reputation * priceMultiplier); hero loop 0.3 + 0.0625*amp, floor(modifier*baseCount),
//	 -max(costReduction, 1).]
func villagerUpdateSpecialPrices(e *Entity, playerUUID uuid.UUID, heroAmplifier int) {
	offers := villagerGetOffers(e)

	reputation := villagerGetPlayerReputation(e, playerUUID)
	if reputation != 0 {
		for _, offer := range offers {
			// -Mth.floor((float)reputation * offer.getPriceMultiplier()): the product is float32 (Java (float)
			// cast on reputation, then float*float), Mth.floor(float) == (int)Math.floor. Compute in float32
			// to mirror the single-precision arithmetic exactly.
			prod := float32(reputation) * offer.getPriceMultiplier()
			offer.addToSpecialPriceDiff(-mthFloorF32(prod))
		}
	}

	if heroAmplifier >= 0 { // player.hasEffect(HERO_OF_THE_VILLAGE)
		for _, offer := range offers {
			modifier := 0.3 + 0.0625*float64(heroAmplifier)
			costReduction := mthFloorF(modifier * float64(offer.baseCostA.count))
			if costReduction < 1 { // -Math.max(costReduction, 1)
				costReduction = 1
			}
			offer.addToSpecialPriceDiff(-costReduction)
		}
	}
}

// villagerResetSpecialPrices ports Villager.resetSpecialPrices(): every offer's specialPriceDiff is zeroed.
// Called when trading STOPS (setTradingPlayer(null) -> stopTrading -> resetSpecialPrices), so the next open
// recomputes the discount from scratch. The jar's isClientSide() guard is server-only here (always false).
// CITE Villager.resetSpecialPrices + Villager.stopTrading.
//
//	[VERIFIED CFR Villager.resetSpecialPrices: for offer: offer.resetSpecialPriceDiff().]
func villagerResetSpecialPrices(e *Entity) {
	for _, offer := range villagerGetOffers(e) {
		offer.resetSpecialPriceDiff()
	}
}

// villagerUpdateDemand ports Villager.updateDemand() (the private restock helper): call updateDemand() on
// every offer. Run from restock() (once per in-game restock), which then resets uses and resends offers.
// CITE Villager.updateDemand / Villager.restock.
//
//	[VERIFIED CFR Villager.updateDemand: for offer in getOffers(): offer.updateDemand().]
func villagerUpdateDemand(e *Entity) {
	for _, offer := range villagerGetOffers(e) {
		offer.updateDemand()
	}
}

// villagerRestock ports the demand/uses portion of Villager.restock():
//
//	this.updateDemand();
//	for offer: offer.resetUses();
//	this.resendOffersToTradingPlayer();     // v1: the caller resends the merchant content
//	this.lastRestockGameTime = getGameTime(); ++numberOfRestocksToday;   // v1: restock-scheduling deferred
//
// v1 realizes the LOAD-BEARING price/stock effects (updateDemand + resetUses); the restock SCHEDULING
// (lastRestockGameTime / numberOfRestocksToday / the twice-a-day work-package trigger) is DEFERRED with the
// villager work-activity package (brain_villager.go cites the WORK package as deferred). resetUses zeroes
// each offer's uses (MerchantOffer.resetUses: this.uses = 0). CITE Villager.restock + MerchantOffer.resetUses.
func villagerRestock(e *Entity) {
	villagerUpdateDemand(e)
	for _, offer := range villagerGetOffers(e) {
		offer.uses = 0 // MerchantOffer.resetUses(): this.uses = 0
	}
}

// villagerOnReputationEventFrom ports Villager.onReputationEventFrom(ReputationEventType, source):
//
//	ZOMBIE_VILLAGER_CURED: gossips.add(uuid, MAJOR_POSITIVE, 20); gossips.add(uuid, MINOR_POSITIVE, 25)
//	TRADE                : gossips.add(uuid, TRADING, 2)
//	VILLAGER_HURT        : gossips.add(uuid, MINOR_NEGATIVE, 25)
//	VILLAGER_KILLED      : gossips.add(uuid, MAJOR_NEGATIVE, 25)
//	(GOLEM_KILLED        : no gossip mutation in Villager — the jar switch has no GOLEM_KILLED branch)
//
// The `source` is the entity that caused the event (the trading/attacking player); its UUID keys the
// gossip. CITE Villager.onReputationEventFrom.
//
//	[VERIFIED CFR Villager.onReputationEventFrom this session (exact add amounts above).]
func villagerOnReputationEventFrom(e *Entity, eventType reputationEventType, sourceUUID uuid.UUID) {
	g := villagerEnsureGossips(e)
	switch eventType {
	case reputationZombieVillagerCured:
		g.add(sourceUUID, gossipMajorPositive, 20)
		g.add(sourceUUID, gossipMinorPositive, 25)
	case reputationTrade:
		g.add(sourceUUID, gossipTrading, 2)
	case reputationVillagerHurt:
		g.add(sourceUUID, gossipMinorNegative, 25)
	case reputationVillagerKilled:
		g.add(sourceUUID, gossipMajorNegative, 25)
	}
	// GOLEM_KILLED / any other type: Villager.onReputationEventFrom has no branch -> no gossip mutation.
}

// reputationEventType ports net.minecraft.world.entity.ai.village.ReputationEventType — the marker
// interface's five registered singletons. Modeled as an int enum (the events carry no data; only identity
// matters for the onReputationEventFrom switch). GOLEM_KILLED exists in the jar but Villager's switch has no
// branch for it (only IronGolem-adjacent handlers react), so it is a valid value with no gossip effect.
//
//	[VERIFIED CFR ReputationEventType: ZOMBIE_VILLAGER_CURED, GOLEM_KILLED, VILLAGER_HURT, VILLAGER_KILLED,
//	 TRADE (register(name)).]
type reputationEventType int

const (
	reputationZombieVillagerCured reputationEventType = iota
	reputationGolemKilled
	reputationVillagerHurt
	reputationVillagerKilled
	reputationTrade
)

// villagerHeroOfVillageAmplifier reads a player's HERO_OF_THE_VILLAGE amplifier for updateSpecialPrices'
// hero discount. Returns -1 when the player has no such effect (the hasEffect gate is false). This is the
// LIVE seam over the effect subsystem (mob_effect.go's p.activeEffects) — currently returns -1 for every
// player because the raid-WIN grant is deferred, exactly matching vanilla when a player has never won a
// raid. CITE Villager.updateSpecialPrices (player.hasEffect/getEffect(HERO_OF_THE_VILLAGE)).
func villagerHeroOfVillageAmplifier(p *tickPlayer) int {
	if p == nil || p.activeEffects == nil {
		return -1
	}
	if eff, ok := p.activeEffects[heroOfTheVillageEffectID]; ok && eff != nil {
		return eff.amplifier
	}
	return -1
}
