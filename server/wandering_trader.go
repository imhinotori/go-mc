package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/registryid"
)

// wandering_trader.go -- net.minecraft.world.entity.npc.wanderingtrader.WanderingTrader, the roaming
// merchant. A LITERAL port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p / CFR
// this session). The WanderingTrader is an AbstractVillager, so it REUSES the villager MERCHANT machinery
// (merchantOffer / MerchantOffers / the MerchantMenu open/click engine in villager_trades.go +
// merchant_menu.go + merchant_click.go) verbatim -- the wire/take/uses path is shared, only the trade
// TABLE and the WT-specific ctor/aiStep/mobInteract differ.
//
// LANDED (this file):
//   - the WT attributes: createMobAttributes() -> MAX_HEALTH 20.0, MOVEMENT_SPEED registration default 0.7
//     (VERIFIED CFR DefaultAttributes: WANDERING_TRADER -> Mob.createMobAttributes().build(), NO override --
//     the wanderingTraderSupplier in level/attribute/defaults.go carries this).
//   - the trade OFFER LIST (wanderingTraderOffers): a faithful concrete slice of the vanilla
//     wandering_trader trade table (data/minecraft/villager_trade/wandering_trader/*.json this session).
//   - the merchant MENU open on right-click (wanderingTraderMobInteract -> openMerchantMenu, shared path).
//   - the despawnDelay countdown (wanderingTraderAiStep -> maybeDespawn: --despawnDelay while !isTrading,
//     discard at 0). A /dbg-spawned trader has despawnDelay==0 (ctor default) so it never auto-despawns.
//   - the 2-trader-llama caravan spawn (spawnWanderingTrader spawns 2 TraderLlamas nearby, reusing
//     horse.go spawnLlama(trader=true)).
//
// DEFERRED (cited):
//   - the WanderingTraderSpawner periodic natural spawn (per-world spawn timer + findSpawnPositionNear +
//     hasEnoughSpace). No per-world CustomSpawner feed in v1 -> /dbg wandering_trader. CITE
//     WanderingTraderSpawner.tick/spawn.
//   - the night-invisibility DRINK + day-reappear (the two UseItemGoal(0) with predicates
//     lambda$registerGoals$0 = level.isDarkOutside() && !isInvisible() -> drink INVISIBILITY potion,
//     lambda$registerGoals$1 = level.isBrightOutside() && isInvisible() -> drink MILK to clear it). Needs
//     the mob-effect (INVISIBILITY) + isInvisible subsystem (mob_effect.go, another agent) -- DEFERRED
//     behind a cited stub, NOT wired here. CITE WanderingTrader.registerGoals (the UseItemGoals).
//   - the caravan-FOLLOW link (TraderLlama.setLeashedTo + LlamaFollowCaravanGoal 2.1): v1 has no leash
//     subsystem (const-false elsewhere) and the follow goal is the DEFERRED Llama goal layer, so the 2
//     llamas spawn near the trader but are not leashed/following. CITE WanderingTraderSpawner
//     .tryToSpawnLlamaFor (setLeashedTo) + Llama.registerGoals (LlamaFollowCaravanGoal).
//   - the autonomous goal layer (TradeWithPlayerGoal, the AvoidEntityGoal set for Zombie/Evoker/
//     Vindicator/Vex/Pillager/Illusioner/Zoglin, WanderToPositionGoal 2.0/0.35, MoveTowardsRestrictionGoal
//     0.35): the visible AI is the bounded passive stand-in (Float + WaterAvoidingRandomStroll 0.35 +
//     LookAtPlayer + RandomLookAround), mirroring the horse/llama passive-AI reduction. CITE
//     WanderingTrader.registerGoals.

// wanderingTraderDefaultDespawnDelay is WanderingTrader.DEFAULT_DESPAWN_DELAY, the value the spawner writes
// via setDespawnDelay (VERIFIED CFR WanderingTraderSpawner.spawn: ldc 48000 -> setDespawnDelay). 48000
// ticks == 40 minutes. The WT ctor sets despawnDelay to 0 (a bare-spawned trader does not count down).
const wanderingTraderDefaultDespawnDelay = 48000

// wanderingTraderStrollSpeed is WaterAvoidingRandomStrollGoal(this, 0.35) -- the WT roam pace (VERIFIED
// CFR WanderingTrader.registerGoals: ldc2_w 0.35d). The literal is the float-widened double, preserved.
const wanderingTraderStrollSpeed = 0.3499999940395355

// wanderingTraderLookDist is LookAtPlayerGoal(this, Player.class, 8.0f) -- the WT look range (VERIFIED CFR
// WanderingTrader.registerGoals: the Mob-variant LookAtPlayerGoal at ldc 8.0f).
const wanderingTraderLookDist = 8.0

// newWanderingTraderBuy builds a "sell ITEM x costCount -> get EMERALD x emeraldCount" offer -- the
// wandering_trader BUYING trades (wants=item, gives=emerald, where gives.count may be > 1). The existing
// villager helper newEmeraldForItemsN fixes the emerald result at 1, so the WT (which buys for 2-3
// emeralds) needs this variant. rewardExp is FALSE for the WT buying set (no xp field in the datapack ->
// default 0 -> rewardExp false). CITE VillagerTrade { wants:item, gives:emerald xN }.
func newWanderingTraderBuy(costItem item.Item, costCount, emeraldCount, maxUses int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: costItem, count: costCount},
		result:          itemStackResult{item: item.Emerald, count: emeraldCount},
		maxUses:         maxUses,
		rewardExp:       false,
		priceMultiplier: discount,
		xp:              0,
	}
}

// wanderingTraderOffers ports the WanderingTrader.updateTrades trade set -- a faithful concrete slice of
// the vanilla wandering_trader trade table. Vanilla updateTrades (VERIFIED CFR) calls addOffersFromTradeSet
// three times: WANDERING_TRADER_BUYING (amount 2.0), WANDERING_TRADER_UNCOMMON (amount 2.0),
// WANDERING_TRADER_COMMON (amount 5.0) -- a RANDOM pick of that many from each tag. Like the villager
// path, v1 supplies a DETERMINISTIC concrete set (a faithful data reduction of the randomized pick, not a
// menu behavior change); the merchant wire/take/uses logic is identical regardless of which offers the
// list holds. CITE WanderingTrader.updateTrades / AbstractVillager.addOffersFromTradeSet as the future
// randomized-selection adjuster.
//
//	[VERIFIED base datapack data/minecraft/villager_trade/wandering_trader/*.json this session
//	 (max_uses / reputation_discount == priceMultiplier per entry):
//	  BUYING (wants item -> gives emerald):
//	    water_bottle_emerald:        WATER potion x1        -> emerald x1, mu 2, disc 0.05 (potion component
//	                                 DEFERRED; uses Potion item as the WATER bottle base id)
//	    water_bucket_emerald:        water_bucket x1        -> emerald x2, mu 2, disc 0.05
//	    milk_bucket_emerald:         milk_bucket x1         -> emerald x2, mu 2, disc 0.05
//	    fermented_spider_eye_emerald:fermented_spider_eye x1-> emerald x3, mu 2, disc 0.05
//	    baked_potato_emerald:        baked_potato x4        -> emerald x1, mu 2, disc 0.05
//	    hay_block_emerald:           hay_block x1           -> emerald x1, mu 2, disc 0.05
//	  COMMON (wants emerald -> gives item):
//	    emerald_packed_ice:  emerald x1 -> packed_ice x1, mu 6; emerald_sea_pickle: emerald x2 -> x1, mu 5;
//	    emerald_glowstone:   emerald x2 -> glowstone x1,  mu 5; emerald_slime_ball: emerald x4 -> x1, mu 5
//	  UNCOMMON (wants emerald -> gives item):
//	    emerald_gunpowder: emerald x1 -> gunpowder x4, mu 2; emerald_blue_ice: emerald x6 -> x1, mu 6;
//	    emerald_podzol:    emerald x3 -> podzol x3,     mu 6   (all disc 0.05)]
func wanderingTraderOffers() merchantOffers {
	return merchantOffers{
		newWanderingTraderBuy(item.Potion, 1, 1, 2, 0.05),
		newWanderingTraderBuy(item.WaterBucket, 1, 2, 2, 0.05),
		newWanderingTraderBuy(item.MilkBucket, 1, 2, 2, 0.05),
		newWanderingTraderBuy(item.FermentedSpiderEye, 1, 3, 2, 0.05),
		newWanderingTraderBuy(item.BakedPotato, 4, 1, 2, 0.05),
		newWanderingTraderBuy(item.HayBlock, 1, 1, 2, 0.05),
		newItemsForEmeraldN(1, item.PackedIce, 1, 6, 0, 0.05),
		newItemsForEmeraldN(2, item.SeaPickle, 1, 5, 0, 0.05),
		newItemsForEmeraldN(2, item.Glowstone, 1, 5, 0, 0.05),
		newItemsForEmeraldN(4, item.SlimeBall, 1, 5, 0, 0.05),
		newItemsForEmeraldN(1, item.Gunpowder, 4, 2, 0, 0.05),
		newItemsForEmeraldN(6, item.BlueIce, 1, 6, 0, 0.05),
		newItemsForEmeraldN(3, item.Podzol, 3, 6, 0, 0.05),
	}
}

// newWanderingTraderAI builds the WT bounded passive AI (WanderingTrader.registerGoals subset). The
// vanilla registerGoals set is Float(0) + 2x UseItemGoal(0, invisibility drink -- DEFERRED) +
// TradeWithPlayerGoal(1) + a long AvoidEntityGoal(1) list + WanderToPositionGoal(2.0/0.35) +
// MoveTowardsRestrictionGoal(0.35) + WaterAvoidingRandomStrollGoal(8, 0.35) + LookAtPlayerGoal(9, Player,
// 8.0) + LookAtPlayerGoal(10, Mob, 8.0). This supplies the "visibly alive" passive stand-in (Float /
// WaterAvoidingRandomStroll 0.35 / LookAtPlayer 8.0 / RandomLookAround), mirroring newLlamaAI reduction;
// the trade/avoid/drink/wander-to-position goals are the DEFERRED autonomous layer. Cite
// WanderingTrader.registerGoals.
func newWanderingTraderAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * wanderingTraderStrollSpeed
	m.navigation.canFloat = true
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(8, newWaterAvoidingRandomStrollGoal(wanderingTraderStrollSpeed))
	m.goals.addGoal(9, newLookAtPlayerGoal(wanderingTraderLookDist))
	m.goals.addGoal(10, newRandomLookAroundGoal())
	return m
}

// spawnWanderingTrader creates a WanderingTrader at (x,y,z) with the jar attributes (MAX_HEALTH 20,
// MOVEMENT_SPEED 0.7 via the wandering_trader supplier) and the passive goal AI, adds it to the owner
// region store, and spawns its 2-trader-llama caravan nearby. despawnDelay starts at 0 (the WT ctor
// default). initSpawnHealth seeds health from MAX_HEALTH (20.0). The offers build lazily on the first
// getOffers() (villagerGetOffers WT branch -> wanderingTraderOffers). Cite WanderingTrader(EntityType,
// Level) + WanderingTraderSpawner.spawn (the caravan llamas).
func (t *TickLoop) spawnWanderingTrader(x, y, z float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.WanderingTrader, x, y, z)
	e.isWanderingTrader = true
	e.despawnDelay = 0
	initSpawnHealth(e)
	e.ai = newWanderingTraderAI()
	reseedMobAI(e.ai, e.id)
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	// WanderingTraderSpawner.spawn: for (i = 0; i < 2; ++i) tryToSpawnLlamaFor(...) -- spawn 2 TraderLlamas
	// as the caravan (VERIFIED CFR spawn loop iconst_2 if_icmpge). The leash link + LlamaFollowCaravanGoal
	// follow are DEFERRED (no leash subsystem; the follow goal is the deferred Llama layer). Reuse
	// horse.go spawnLlama(trader=true).
	t.spawnLlama(x+1, y, z, false, true)
	t.spawnLlama(x-1, y, z, false, true)
	return e
}

// wanderingTraderSetDespawnDelay ports WanderingTrader.setDespawnDelay(int): despawnDelay = i. The public
// setter the spawner calls (setDespawnDelay(48000)). CITE WanderingTrader.setDespawnDelay.
func wanderingTraderSetDespawnDelay(e *Entity, i int) { e.despawnDelay = i }

// wanderingTraderAiStep ports WanderingTrader.aiStep server tail: super.aiStep(), then (!isClientSide)
// maybeDespawn(). maybeDespawn (VERIFIED CFR): if (despawnDelay > 0 && !isTrading() && --despawnDelay == 0)
// discard(). The pre-decrement + zero-test is exact (a delay of 1 discards this tick). A trader with
// despawnDelay <= 0 (a bare /dbg spawn) or one currently trading never counts down. Per-type-gated on
// isWanderingTrader, AFTER serverAiStep. RNG-free. Cite WanderingTrader.aiStep + maybeDespawn.
//
//	[VERIFIED CFR WanderingTrader.maybeDespawn: getfield despawnDelay; ifle 32; isTrading ifne 32;
//	 despawnDelay-1 dup_x1 putfield; ifne 32; discard; return.]
func (t *TickLoop) wanderingTraderAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	if e.despawnDelay > 0 && !villagerIsTrading(e) {
		e.despawnDelay-- // --despawnDelay
		if e.despawnDelay == 0 {
			// discard(): the idiom the creeper/endermite/checkDespawn self-removals use -- mark dead + drop
			// from the owner region store, so the tickAI loop e.dead re-check skips the rest of the frame.
			e.dead = true
			t.regionForEntity(e).entities.remove(e.id)
		}
	}
}

// wanderingTraderMobInteract ports net.minecraft.world.entity.npc.wanderingtrader.WanderingTrader
// .mobInteract for the trading path (VERIFIED CFR this session). The leading `!itemStack.is(
// VILLAGER_SPAWN_EGG)` gate is a cited const-true (no WT spawn-egg dup action in the v1 subset), so the
// body reduces to:
//
//	if (isAlive && !isTrading && !isBaby) {                     // NOTE: no isSleeping gate (WT never sleeps)
//	    if (hand == MAIN_HAND) player.awardStat(TALKED_TO_VILLAGER);   // v1 no-op stat
//	    if (!clientSide) {
//	        if (getOffers().isEmpty()) return CONSUME;          // no offers -> consume, no menu
//	        setTradingPlayer(player); openTradingScreen(...);   // open the merchant menu
//	    }
//	    return SUCCESS;
//	}
//	return super.mobInteract(...);                              // busy -> fall through (v1: no-op)
//
// UNLIKE Villager.mobInteract, the WT does NOT call setUnhappy (no unhappy counter) and does NOT call
// updateSpecialPrices (the WT has no gossip economy -- openWanderingTraderMenu skips it). Returns true when
// the interact is CONSUMED (a menu open, a no-offers consume, or a busy WT) so handleInteract does not fall
// to the feed path. Cite WanderingTrader.mobInteract.
func (t *TickLoop) wanderingTraderMobInteract(p *tickPlayer, trader *Entity) bool {
	// isAlive() && !isTrading() (no isSleeping gate for the WT). A trader already trading is busy: a no-op
	// consume (vanilla falls to super.mobInteract, which for the WT does no trading action).
	if villagerIsTrading(trader) {
		return true
	}
	// !isBaby(): the WT has no baby form in practice, but the gate is faithful -- a baby WT falls to super
	// (no unhappy for the WT). isBaby is the real breedAge<0 read.
	if trader.isBaby() {
		return true
	}
	// server: getOffers().isEmpty() -> CONSUME (no menu). MAIN_HAND awardStat is a v1 no-op.
	offers := villagerGetOffers(trader)
	if offers.isEmpty() {
		return true
	}
	// setTradingPlayer(player) + openTradingScreen(...): open the merchant menu (no updateSpecialPrices).
	t.openWanderingTraderMenu(p, trader)
	return true
}

// openWanderingTraderMenu ports WanderingTrader.mobInteract's open tail: setTradingPlayer(player) then
// openTradingScreen(player, getDisplayName(), 1). It mirrors openMerchantMenu (the shared MerchantMenu
// wire path) with two WT-faithful differences: (1) it does NOT call updateSpecialPrices (the WT has no
// gossip/reputation economy), and (2) it sends showProgress=FALSE (WanderingTrader.showProgressBar()
// returns iconst_0, VERIFIED CFR -- unlike AbstractVillager's true). canRestock is true (AbstractVillager
// .canRestock default). The offers are the WT LIVE getOffers() (shared across re-opens so uses depletion
// persists). villagerLevel/villagerXp are 0 for a WT (it carries no profession level; the progress bar is
// hidden anyway). Cite WanderingTrader.mobInteract (setTradingPlayer + openTradingScreen) +
// WanderingTrader.showProgressBar.
func (t *TickLoop) openWanderingTraderMenu(p *tickPlayer, trader *Entity) bool {
	if p == nil || p.client == nil || trader == nil {
		return false
	}
	offers := villagerGetOffers(trader)
	if offers.isEmpty() {
		return false
	}
	// setTradingPlayer(player) -- NO updateSpecialPrices (WT has no gossip economy).
	villagerSetTradingPlayer(trader, p.entityID)
	if p.openContainer != nil {
		p.openContainer = nil
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{
		windowID:           win,
		kind:               containerKindMerchant,
		merchantVillagerID: trader.id,
		mselectionHint:     0,
		mactiveOffer:       -1,
	}
	menuID := menuTypeID(registryid.Menu, "minecraft:merchant")
	p.client.Send(openScreen(int32(win), menuID, merchantTitle))
	// showProgress=FALSE (WanderingTrader.showProgressBar returns false); canRestock=true. level/xp 0.
	p.client.Send(clientboundMerchantOffers(int32(win), offers, trader.villagerLevel, trader.villagerXp, false, true))
	t.sendMerchantContent(p)
	return true
}
