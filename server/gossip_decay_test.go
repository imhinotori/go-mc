package server

// gossip_decay_test.go -- coverage for GAP 3 (GOSSIP NEVER DECAYS): the villager tick now calls
// villagerMaybeDecayGossip (brain_villager.go villagerBrainTick -> villager_reputation.go), porting
// Villager.tick() -> maybeDecayGossip(). A villager's gossip container decays every 24000 ticks (one day)
// toward 0 -- so a player's reputation (and the trade-price discount it drives) fades instead of being
// permanent. All numbers are jar-verified against Villager.maybeDecayGossip (24000L window) + GossipContainer
// .decay + GossipType decayPerDay. The pig oracle (TestPluginPigEqualsGoNativePig) is UNTOUCHED -- the decay
// is villager-gated and draws no RNG.

import (
	"testing"

	"github.com/google/uuid"
)

// TestVillagerMaybeDecayGossipWindow pins the maybeDecayGossip cadence: the FIRST call seeds
// lastGossipDecayTime (no decay); a call before the 24000-tick window is a no-op; a call at/after the window
// decays the container by the per-type decayPerDay.
func TestVillagerMaybeDecayGossipWindow(t *testing.T) {
	e := &Entity{}
	player := uuid.New()

	// Seed some gossip: MINOR_POSITIVE 20 (decayPerDay 1), TRADING 20 (decayPerDay 2), MAJOR_POSITIVE 20
	// (decayPerDay 0). getReputation weights: 20*1 + 20*1 + 20*5 = 140 before any decay.
	g := villagerEnsureGossips(e)
	g.add(player, gossipMinorPositive, 20)
	g.add(player, gossipTrading, 20)
	g.add(player, gossipMajorPositive, 20)
	repBefore := villagerGetPlayerReputation(e, player)
	if repBefore != 140 {
		t.Fatalf("seeded reputation = %d, want 140 (20*1 + 20*1 + 20*5)", repBefore)
	}

	// First call seeds lastGossipDecayTime = gameTime; NO decay yet.
	villagerMaybeDecayGossip(e, 1000)
	if e.lastGossipDecayTime != 1000 {
		t.Fatalf("first maybeDecayGossip must seed lastGossipDecayTime=1000, got %d", e.lastGossipDecayTime)
	}
	if got := villagerGetPlayerReputation(e, player); got != repBefore {
		t.Fatalf("first call must NOT decay: reputation = %d, want %d", got, repBefore)
	}

	// A call before the 24000 window from the seed -> still a no-op.
	villagerMaybeDecayGossip(e, 1000+23999)
	if got := villagerGetPlayerReputation(e, player); got != repBefore {
		t.Fatalf("call within the window must NOT decay: reputation = %d, want %d", got, repBefore)
	}

	// A call at exactly seed + 24000 -> DECAY fires. Per-type decayPerDay: MINOR_POSITIVE 20-1=19,
	// TRADING 20-2=18, MAJOR_POSITIVE 20-0=20 -> weighted 19*1 + 18*1 + 20*5 = 137.
	villagerMaybeDecayGossip(e, 1000+24000)
	if e.lastGossipDecayTime != 1000+24000 {
		t.Fatalf("decay must re-stamp lastGossipDecayTime=%d, got %d", 1000+24000, e.lastGossipDecayTime)
	}
	got := villagerGetPlayerReputation(e, player)
	if got != 137 {
		t.Fatalf("after one decay reputation = %d, want 137 (19*1 + 18*1 + 20*5)", got)
	}
	if got >= repBefore {
		t.Fatalf("reputation must DECREASE after decay: %d -> %d", repBefore, got)
	}
}

// TestVillagerGossipDecayViaBrainTick drives the decay through the REAL villager tick path (villagerBrainTick)
// to prove the wiring: after the seed tick and a tick past the 24000 window, the gossip value has decreased.
func TestVillagerGossipDecayViaBrainTick(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	e := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 64.0, 8.5)
	if e == nil {
		t.Fatal("spawnVanillaMob(vanilla_villager) returned nil")
	}
	player := uuid.New()
	g := villagerEnsureGossips(e)
	g.add(player, gossipTrading, 20) // TRADING decayPerDay 2 -> weighted 20 before decay
	repBefore := villagerGetPlayerReputation(e, player)
	if repBefore != 20 {
		t.Fatalf("seeded TRADING reputation = %d, want 20", repBefore)
	}

	// First brain tick seeds lastGossipDecayTime (no decay).
	loop.gametime = 500
	loop.villagerBrainTick(e)
	if e.lastGossipDecayTime != 500 {
		t.Fatalf("first villagerBrainTick must seed lastGossipDecayTime=500, got %d", e.lastGossipDecayTime)
	}
	if got := villagerGetPlayerReputation(e, player); got != repBefore {
		t.Fatalf("first tick must not decay: reputation=%d want %d", got, repBefore)
	}

	// Tick past the 24000-tick window -> the brain tick decays the gossip.
	loop.gametime = 500 + 24000
	loop.villagerBrainTick(e)
	got := villagerGetPlayerReputation(e, player)
	if got != 18 { // 20 - 2 (TRADING decayPerDay)
		t.Fatalf("after the window a brain tick must decay TRADING 20->18, got %d", got)
	}
	if got >= repBefore {
		t.Fatalf("reputation must decrease over the decay cadence: %d -> %d", repBefore, got)
	}
}

// TestGossipDecayDropsBelowThreshold pins that GossipContainer.decay drops an entry that falls below the
// DISCARD_THRESHOLD (2): a TRADING gossip of 3 (decayPerDay 2) becomes 1 (< 2) and is removed, emptying the
// container (the target entry is dropped when it becomes empty).
func TestGossipDecayDropsBelowThreshold(t *testing.T) {
	e := &Entity{}
	player := uuid.New()
	g := villagerEnsureGossips(e)
	g.add(player, gossipTrading, 3) // value 3

	villagerMaybeDecayGossip(e, 100)       // seed
	villagerMaybeDecayGossip(e, 100+24000) // decay: 3 - 2 = 1 < 2 -> removed
	if got := villagerGetPlayerReputation(e, player); got != 0 {
		t.Fatalf("a gossip decayed below the discard threshold must be removed (reputation 0), got %d", got)
	}
}
