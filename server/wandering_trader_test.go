package server

// wandering_trader_test.go -- deterministic pins for the WANDERING TRADER
// (net.minecraft.world.entity.npc.wanderingtrader.WanderingTrader, 1:1 javap/CFR this session). Pins the
// spawn defaults (MAX_HEALTH 20, MOVEMENT_SPEED 0.7 -- createMobAttributes with NO override), the 2-trader-
// llama caravan, the trade offer list, the merchant menu open on interact, and the despawnDelay countdown.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
)

func TestWanderingTraderSpawnDefaults(t *testing.T) {
	loop, _ := newBlockLoop()
	e := loop.spawnWanderingTrader(8.5, 64.0, 8.5)
	if e.typ != entity.WanderingTrader.ID {
		t.Fatalf("WT typ = %d, want entity.WanderingTrader.ID %d", e.typ, entity.WanderingTrader.ID)
	}
	if !e.isWanderingTrader {
		t.Fatal("WT not marked isWanderingTrader")
	}
	hp := e.getAttributeValue(attribute.MaxHealth)
	if math.Abs(hp-20.0) > 1e-9 {
		t.Fatalf("WT MAX_HEALTH = %v, want 20.0", hp)
	}
	if math.Abs(float64(e.health)-20.0) > 1e-6 {
		t.Fatalf("WT health = %v, want 20.0 (initSpawnHealth)", e.health)
	}
	sp := e.getAttributeValue(attribute.MovementSpeed)
	if math.Abs(sp-0.7) > 1e-6 {
		t.Fatalf("WT MOVEMENT_SPEED = %v, want 0.7 (createMobAttributes default, NO override)", sp)
	}
	if e.despawnDelay != 0 {
		t.Fatalf("WT despawnDelay = %d, want 0 (ctor DEFAULT_DESPAWN_DELAY)", e.despawnDelay)
	}
	if e.ai == nil || e.ai.rng == nil {
		t.Fatal("WT has no minimal AI / rng")
	}
}

func TestWanderingTraderCaravanLlamas(t *testing.T) {
	loop, _ := newBlockLoop()
	loop.spawnWanderingTrader(8.5, 64.0, 8.5)
	traderLlamas := 0
	for _, ent := range loop.only().entities.byID {
		if ent.typ == entity.TraderLlama.ID && ent.isTraderLlama {
			traderLlamas++
		}
	}
	if traderLlamas != 2 {
		t.Fatalf("caravan trader llamas = %d, want 2 (spawn 2-llama loop)", traderLlamas)
	}
}

func TestWanderingTraderOffersPopulated(t *testing.T) {
	loop, _ := newBlockLoop()
	e := loop.spawnWanderingTrader(8.5, 64.0, 8.5)
	offers := villagerGetOffers(e)
	if offers.isEmpty() {
		t.Fatal("WT offers empty after getOffers()")
	}
	if len(offers) != 13 {
		t.Fatalf("WT offer count = %d, want 13 (buying+common+uncommon slice)", len(offers))
	}
	var found bool
	for _, o := range offers {
		if int(o.baseCostA.item.ID) == int(item.FermentedSpiderEye.ID) {
			found = true
			if o.result.item.ID != item.Emerald.ID || o.result.count != 3 {
				t.Fatalf("fermented_spider_eye result = item %d x%d, want emerald x3", o.result.item.ID, o.result.count)
			}
			if o.maxUses != 2 {
				t.Fatalf("fermented_spider_eye maxUses = %d, want 2", o.maxUses)
			}
		}
	}
	if !found {
		t.Fatal("no fermented_spider_eye buying offer")
	}
	found = false
	for _, o := range offers {
		if o.result.item.ID == item.PackedIce.ID {
			found = true
			if int(o.baseCostA.item.ID) != int(item.Emerald.ID) || o.baseCostA.count != 1 {
				t.Fatalf("packed_ice cost = item %d x%d, want emerald x1", o.baseCostA.item.ID, o.baseCostA.count)
			}
			if o.maxUses != 6 {
				t.Fatalf("packed_ice maxUses = %d, want 6", o.maxUses)
			}
		}
	}
	if !found {
		t.Fatal("no packed_ice offer")
	}
	offers2 := villagerGetOffers(e)
	if len(offers2) != len(offers) {
		t.Fatalf("second getOffers() len = %d, want %d (cached list)", len(offers2), len(offers))
	}
}

func TestWanderingTraderMenuOpens(t *testing.T) {
	loop, _ := newBlockLoop()
	e := loop.spawnWanderingTrader(8.5, 64.0, 8.5)
	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1
	loop.handleInteract(p, interactPacket(e.id))
	if p.openContainer == nil {
		t.Fatal("openContainer nil after WT interact")
	}
	if p.openContainer.kind != containerKindMerchant {
		t.Fatalf("openContainer kind = %d, want merchant (%d)", p.openContainer.kind, containerKindMerchant)
	}
	if p.openContainer.windowID < 1 || p.openContainer.windowID > 100 {
		t.Fatalf("windowId %d out of 1..100", p.openContainer.windowID)
	}
	if e.villagerTradingPlayer != p.entityID {
		t.Fatalf("WT tradingPlayer = %d, want %d", e.villagerTradingPlayer, p.entityID)
	}
	if p.openContainer.merchantVillagerID != e.id {
		t.Fatalf("merchantVillagerID = %d, want %d", p.openContainer.merchantVillagerID, e.id)
	}
}

func TestWanderingTraderDespawnTimer(t *testing.T) {
	loop, _ := newBlockLoop()
	e := loop.spawnWanderingTrader(8.5, 64.0, 8.5)
	loop.wanderingTraderAiStep(e)
	if e.despawnDelay != 0 || e.dead {
		t.Fatalf("bare WT (delay 0): delay=%d dead=%v, want 0/false", e.despawnDelay, e.dead)
	}
	wanderingTraderSetDespawnDelay(e, 3)
	loop.wanderingTraderAiStep(e)
	if e.despawnDelay != 2 || e.dead {
		t.Fatalf("step 1: delay=%d dead=%v, want 2/false", e.despawnDelay, e.dead)
	}
	loop.wanderingTraderAiStep(e)
	if e.despawnDelay != 1 || e.dead {
		t.Fatalf("step 2: delay=%d dead=%v, want 1/false", e.despawnDelay, e.dead)
	}
	loop.wanderingTraderAiStep(e)
	if e.despawnDelay != 0 || !e.dead {
		t.Fatalf("step 3: delay=%d dead=%v, want 0/true (discard)", e.despawnDelay, e.dead)
	}
	if _, ok := loop.only().entities.byID[e.id]; ok {
		t.Fatal("discarded WT still in store")
	}
}

func TestWanderingTraderDespawnPausedWhileTrading(t *testing.T) {
	loop, _ := newBlockLoop()
	e := loop.spawnWanderingTrader(8.5, 64.0, 8.5)
	wanderingTraderSetDespawnDelay(e, 5)
	villagerSetTradingPlayer(e, 42)
	loop.wanderingTraderAiStep(e)
	if e.despawnDelay != 5 || e.dead {
		t.Fatalf("trading WT: delay=%d dead=%v, want 5/false (paused while trading)", e.despawnDelay, e.dead)
	}
}
