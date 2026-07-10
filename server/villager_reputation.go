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

// villagerRestockGameDay ports the OVERWORLD_DAY timeline day index shouldRestock reads for the daily
// numberOfRestocksToday reset. Vanilla queries level.registryAccess().get(Timelines.OVERWORLD_DAY).map(t ->
// t.dayCount(level)).orElse(0). The overworld day is gameTime / DAY_LENGTH(24000) — the elapsed-day count
// (integer division floors, matching the timeline's per-day bucketing). This reuses the same gameTime-as-
// day proxy the villager schedule / spider daytime checks already use. CITE Villager.shouldRestock (timeline
// OVERWORLD_DAY dayCount) + the 24000-tick Minecraft day.
func villagerRestockGameDay(gameTime int64) int64 { return gameTime / 24000 }

// villagerNeedsToRestock ports Villager.needsToRestock(): true if ANY offer needs a restock (uses > 0).
//
//	[VERIFIED CFR Villager.needsToRestock: for offer in getOffers(): if offer.needsRestock() return true.]
func villagerNeedsToRestock(e *Entity) bool {
	for _, offer := range villagerGetOffers(e) {
		if offer.needsRestock() {
			return true
		}
	}
	return false
}

// villagerAllowedToRestock ports Villager.allowedToRestock():
//
//	return this.numberOfRestocksToday == 0
//	    || (this.numberOfRestocksToday < 2 && level.getGameTime() > this.lastRestockGameTime + 2400L);
//
// The first restock of a day is always allowed; a second is allowed only >2400 ticks after the first; a
// third+ is never allowed (the 2x/day cap). CITE Villager.allowedToRestock (RESTOCK_LIMIT_2, 2400L gap).
//
//	[VERIFIED CFR Villager.allowedToRestock: numberOfRestocksToday==0 OR (numberOfRestocksToday<2 &&
//	 gameTime > lastRestockGameTime + 2400).]
func villagerAllowedToRestock(e *Entity, gameTime int64) bool {
	if e.numberOfRestocksToday == 0 {
		return true
	}
	return e.numberOfRestocksToday < 2 && gameTime > e.lastRestockGameTime+2400
}

// villagerShouldRestock ports Villager.shouldRestock(ServerLevel):
//
//	long nextRestock = this.lastRestockGameTime + 12000L;
//	long gameTime = level.getGameTime();
//	boolean flag = gameTime > nextRestock;
//	long day = <OVERWORLD_DAY timeline dayCount, else 0>;
//	flag = flag || (this.lastRestockCheckDay != 0 && day > this.lastRestockCheckDay);
//	this.lastRestockCheckDay = day;
//	if (flag) { this.lastRestockGameTime = gameTime; this.resetNumberOfRestocks(); }
//	return this.allowedToRestock() && this.needsToRestock();
//
// The 12000-tick window (half a day) OR a crossed day-boundary triggers the daily reset (numberOfRestocksToday
// -> 0 via resetNumberOfRestocks). Mutates lastRestockCheckDay/lastRestockGameTime/numberOfRestocksToday as
// side effects, exactly as the jar. CITE Villager.shouldRestock.
//
//	[VERIFIED CFR Villager.shouldRestock this session (12000L window; day-boundary OR; reset side effects).]
func villagerShouldRestock(e *Entity, gameTime int64) bool {
	nextRestock := e.lastRestockGameTime + 12000
	flag := gameTime > nextRestock
	day := villagerRestockGameDay(gameTime)
	flag = flag || (e.lastRestockCheckDay != 0 && day > e.lastRestockCheckDay)
	e.lastRestockCheckDay = day
	if flag {
		e.lastRestockGameTime = gameTime
		e.numberOfRestocksToday = 0 // resetNumberOfRestocks(): this.numberOfRestocksToday = 0
	}
	return villagerAllowedToRestock(e, gameTime) && villagerNeedsToRestock(e)
}

// villagerRestock ports Villager.restock() in full:
//
//	this.updateDemand();
//	for offer: offer.resetUses();
//	this.resendOffersToTradingPlayer();     // v1: the caller resends the merchant content
//	this.lastRestockGameTime = getGameTime();
//	++this.numberOfRestocksToday;
//
// It realizes the price/stock effects (updateDemand + resetUses) AND the scheduling bookkeeping
// (lastRestockGameTime stamp + numberOfRestocksToday increment) that gate the 2x/day cap. gameTime is the
// caller's level.getGameTime(). resetUses zeroes each offer's uses (MerchantOffer.resetUses: this.uses = 0).
//
// The CALL SITE (WorkAtPoi.start: if at job-site POI, useWorkstation() + if shouldRestock() restock()) is
// still a cited deferral -- the WORK activity landed the walk-to-job-site + UpdateActivityFromSchedule but
// not WorkAtPoi/WorkAtComposter (brain_villager.go cites them deferred). Once WorkAtPoi is wired it calls
// villagerShouldRestock + villagerRestock with no change here. CITE Villager.restock + WorkAtPoi.start.
func villagerRestock(e *Entity, gameTime int64) {
	villagerUpdateDemand(e)
	for _, offer := range villagerGetOffers(e) {
		offer.uses = 0 // MerchantOffer.resetUses(): this.uses = 0
	}
	e.lastRestockGameTime = gameTime
	e.numberOfRestocksToday++
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

// villagerBreedFoodThreshold is the Villager.canBreed food gate (bipush 12): a villager can breed only
// when foodLevel + countFoodPointsInInventory() >= 12. Also the digestFood amount eatAndDigestFood spends.
//
//	[VERIFIED CFR Villager.canBreed: (foodLevel + countFoodPointsInInventory()) >= 12; eatAndDigestFood ->
//	 digestFood(12).]
const villagerBreedFoodThreshold = 12

// villagerCountFoodPointsInInventory ports Villager.countFoodPointsInInventory(): the sum of FOOD_POINTS
// over the villager's SimpleContainer inventory. The villager inventory subsystem is not built (there is
// no getInventory() seam yet), so this is a CITED STUB == 0 — the exact value for a villager that carries
// no food (the ctor state). It is structured as a call so it becomes a real inventory scan when the
// villager container lands, with no change at the canBreed call site. CITE Villager.countFoodPointsInInventory.
func villagerCountFoodPointsInInventory(_ *Entity) int { return 0 }

// villagerCanBreed ports Villager.canBreed():
//
//	return (foodLevel + countFoodPointsInInventory()) >= 12 && !isSleeping() && getAge() == 0;
//
// getAge() == 0 (an ADULT off breeding cooldown, breedAge == 0) AND not sleeping AND enough food. isSleeping
// is the mob sleep-pose seam (villagers do not enter the SLEEPING pose here yet — a bed-sleep is cite-
// deferred REST work), so it reads the never-sleeping default (false), matching a villager that is awake.
//
//	[VERIFIED CFR Villager.canBreed: foodLevel+countFoodPointsInInventory()>=12 && !isSleeping()
//	 && getAge()==0.]
func villagerCanBreed(e *Entity) bool {
	if e.villagerFoodLevel+villagerCountFoodPointsInInventory(e) < villagerBreedFoodThreshold {
		return false
	}
	if villagerIsSleeping(e) {
		return false
	}
	return e.breedAge == 0 // getAge() == 0
}

// villagerIsSleeping ports Villager.isSleeping() for the canBreed gate. The villager sleep-pose (REST
// SleepInBed) is cite-deferred (the mob sleep-pose seam is not built), so a villager is never in the
// SLEEPING pose here — this returns the awake default (false). CITE LivingEntity.isSleeping (villager
// bed-sleep deferred with the REST SleepInBed behavior).
func villagerIsSleeping(_ *Entity) bool { return false }

// villagerEatAndDigestFood ports Villager.eatAndDigestFood(): eatUntilFull() then digestFood(12). eatUntilFull
// tops foodLevel up from the villager inventory (the SimpleContainer scan) — deferred with the villager
// inventory, so it is a no-op stub here (an un-fed villager has nothing to eat). digestFood(12) subtracts 12
// from foodLevel. VillagerMakeLove.tick calls this on both parents at birth, spending the breeding food.
//
//	[VERIFIED CFR Villager.eatAndDigestFood: eatUntilFull(); digestFood(12). digestFood(int n): foodLevel -= n.
//	 eatUntilFull reads getInventory() FOOD_POINTS (inventory subsystem deferred -> no-op).]
func villagerEatAndDigestFood(e *Entity) {
	// eatUntilFull(): scan the villager inventory for food and top foodLevel up. DEFERRED (no villager
	// SimpleContainer seam) -> no-op, exactly as a villager with an empty inventory.
	e.villagerFoodLevel -= villagerBreedFoodThreshold // digestFood(12): foodLevel -= 12
}

// villagerGossipDecayWindow is the Villager.maybeDecayGossip window (ldc2_w 24000L): the gossip container
// decays once per this many ticks (one Minecraft day).
//
//	[VERIFIED CFR Villager.maybeDecayGossip: gameTime < lastGossipDecayTime + 24000L -> return.]
const villagerGossipDecayWindow int64 = 24000

// villagerMaybeDecayGossip ports Villager.maybeDecayGossip():
//
//	long gameTime = level().getGameTime();
//	if (this.lastGossipDecayTime == 0L) { this.lastGossipDecayTime = gameTime; return; }
//	if (gameTime < this.lastGossipDecayTime + 24000L) return;
//	this.gossips.decay();
//	this.lastGossipDecayTime = gameTime;
//
// The first call seeds lastGossipDecayTime (no decay); thereafter the whole container decays every 24000
// ticks (GossipContainer.decay -> each EntityGossips.decay: each type -= decayPerDay, drop < 2). Called from
// Villager.tick() every tick; villager-gated at the call site (villagerBrainTick). e.villagerGossips is nil
// until first use (villagerEnsureGossips) — a nil container has nothing to decay, so the seed/window logic
// still runs to stamp lastGossipDecayTime, matching the jar's decay() on an empty container (a no-op).
//
//	[VERIFIED CFR Villager.maybeDecayGossip this session (seed-on-zero; 24000L window; gossips.decay();
//	 stamp lastGossipDecayTime).]
func villagerMaybeDecayGossip(e *Entity, gameTime int64) {
	if e.lastGossipDecayTime == 0 {
		e.lastGossipDecayTime = gameTime
		return
	}
	if gameTime < e.lastGossipDecayTime+villagerGossipDecayWindow {
		return
	}
	if e.villagerGossips != nil {
		e.villagerGossips.decay()
	}
	e.lastGossipDecayTime = gameTime
}
