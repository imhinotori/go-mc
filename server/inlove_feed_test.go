package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// inlove_feed_test.go — MOB-SUB-09 (Plan 33-02): the Animal in-love + FEED interaction regression set.
// These exercise the jar-faithful Animal.mobInteract FEED path (Pig.isFood = pig_food tag) and the
// Animal.aiStep in-love decrement, all ported verbatim from temp/cache/26.2-inner.jar:
//
//   - TestFeedAdultSetsLove   — feeding pig_food to an ADULT (breedAge 0, canFallInLove) sets inLove
//     = 600 (DEFAULT_IN_LOVE_TIME) and shrinks the held stack by 1.
//   - TestFeedBabyAgesUp       — feeding pig_food to a BABY ages it up by ageUp(getSpeedUpSecondsWhen
//     Feeding(-age)) toward 0 and shrinks the held stack by 1.
//   - TestFeedNonFoodNoOp      — feeding a NON-pig_food item leaves inLove 0 and consumes nothing.
//   - TestFeedAlreadyInLoveNoOp — feeding an adult ALREADY in love is a no-op (canFallInLove false):
//     no second consume, inLove unchanged (the adult branch's canFallInLove gate).
//   - TestInLoveDecrement      — tickMobAging decrements inLove on an adult and forces it to 0 the
//     instant the mob is not an adult (breedAge != 0); the %10 heart cadence does not panic.
//   - TestHeartParticleEncoder — encodeLevelParticles emits the javap-confirmed ClientboundLevelParticles
//     wire layout for a HEART burst (the previously-missing encoder).
//   - TestFeedThroughHandleInteract — the FULL dispatch path: a ServerboundInteract naming an adult pig
//     held with pig_food sets it in love (end-to-end through handleInteract's decode + resolve).

// feedTestPig builds a one-region floor loop with an adult pig at (8.5, 64, 8.5) in chunk (0,0). The
// pig starts breedAge 0 (an adult, can-fall-in-love) and inLove 0 — the same un-fed lone-adult start
// the oracle uses. Returns the loop and the pig.
func feedTestPig(t *testing.T) (*TickLoop, *Entity) {
	t.Helper()
	loop, pig := temptTestLoop(t) // a live adult pig at (8.5, 64, 8.5) in the sole region
	return loop, pig
}

// feedPlayerHolding registers a player at the pig's column holding itemID (count `count`) in the main
// hand, so owningRegion(pig)/regionForColumn(player) both resolve to the same (only) region. entityID
// 1 so a future cross-check has a real id; confirmedTeleport is irrelevant to the feed path.
func feedPlayerHolding(loop *TickLoop, x, y, z float64, itemID int32, count int) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, entityID: 1}
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(itemID)})
	loop.players = append(loop.players, p)
	return p
}

// TestFeedAdultSetsLove: feeding pig_food (carrot id 1257) to an adult pig (breedAge 0) sets inLove to
// 600 and shrinks the held stack from 3 to 2 (usePlayerItem -> consume 1).
func TestFeedAdultSetsLove(t *testing.T) {
	loop, pig := feedTestPig(t)
	p := feedPlayerHolding(loop, pig.x, pig.y, pig.z, 1257, 3) // 1257 = carrot, a pig_food member

	if pig.breedAge != 0 {
		t.Fatalf("precondition: pig breedAge = %d, want 0 (adult)", pig.breedAge)
	}
	if pig.inLove != 0 {
		t.Fatalf("precondition: pig inLove = %d, want 0 (not yet in love)", pig.inLove)
	}

	loop.tryFeedAnimal(p, pig)

	if pig.inLove != defaultInLoveTime {
		t.Fatalf("after feeding pig_food to an adult, inLove = %d, want %d (setInLove -> 600)", pig.inLove, defaultInLoveTime)
	}
	if !pig.isInLove() {
		t.Fatal("after feeding, isInLove() is false — the adult should be in love")
	}
	if got := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; got != 2 {
		t.Fatalf("held stack count = %d after feeding, want 2 (consume 1 of 3)", got)
	}
}

// TestFeedBabyAgesUp: feeding pig_food to a BABY (breedAge -100) ages it up by ageUp(getSpeedUpSeconds
// WhenFeeding(100)). getSpeedUpSecondsWhenFeeding(100) = (int)((float)(100/20)*0.1f) = (int)(5f*0.1f) =
// (int)0.5f = 0 seconds -> ageUp(0) = breedAge + 0*20 = -100 unchanged for THIS small age; so use a
// larger baby age where the speedup is non-zero. For breedAge -24000 (a fresh baby): -(-24000)=24000;
// 24000/20=1200; 1200f*0.1f=120.0f -> 120 seconds; ageUp(120) = -24000 + 120*20 = -24000 + 2400 =
// -21600. The held stack shrinks by 1.
func TestFeedBabyAgesUp(t *testing.T) {
	loop, pig := feedTestPig(t)
	pig.breedAge = -24000 // a fresh baby (BABY_START_AGE)
	pig.refreshDimensions()
	p := feedPlayerHolding(loop, pig.x, pig.y, pig.z, 1257, 2)

	loop.tryFeedAnimal(p, pig)

	const wantAge = -24000 + 120*20 // ageUp(getSpeedUpSecondsWhenFeeding(24000)=120) = -24000 + 2400
	if pig.breedAge != wantAge {
		t.Fatalf("after feeding a baby, breedAge = %d, want %d (ageUp speedup toward 0)", pig.breedAge, wantAge)
	}
	if !pig.isBaby() {
		t.Fatal("a -24000 baby fed once should still be a baby (it only grew to -21600)")
	}
	if pig.inLove != 0 {
		t.Fatal("feeding a BABY must NOT set inLove (a baby cannot fall in love)")
	}
	if got := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; got != 1 {
		t.Fatalf("held stack count = %d after feeding a baby, want 1 (consume 1 of 2)", got)
	}
}

// TestFeedTinyBabySpeedupTruncatesToZero: a baby a hair from adulthood (-10) fed once stays a baby —
// getSpeedUpSecondsWhenFeeding(10) = (int)((float)(10/20)*0.1f) = (int)(0f*0.1f) = 0 SECONDS, so
// ageUp(0) = -10 + 0*20 = -10 (unchanged). This is the FAITHFUL vanilla truncation: a single feed's
// grow-up speedup truncates to 0 for a baby near adulthood (the per-feed speedup is always far smaller
// than the distance to 0 — a single feed can never cross). The item is STILL consumed (usePlayerItem
// runs before ageUp), exactly as vanilla.
func TestFeedTinyBabySpeedupTruncatesToZero(t *testing.T) {
	loop, pig := feedTestPig(t)
	pig.breedAge = -10 // a baby a hair from adulthood
	pig.refreshDimensions()
	babyWidth := pig.width
	p := feedPlayerHolding(loop, pig.x, pig.y, pig.z, 1257, 2)

	loop.tryFeedAnimal(p, pig)

	if pig.breedAge != -10 {
		t.Fatalf("a -10 baby fed once: breedAge = %d, want -10 (getSpeedUpSecondsWhenFeeding(10) truncates to 0 seconds)", pig.breedAge)
	}
	if !pig.isBaby() {
		t.Fatal("a -10 baby fed once should remain a baby (0-second speedup, the vanilla truncation)")
	}
	if pig.width != babyWidth {
		t.Fatalf("the still-baby pig's AABB changed (width %v -> %v) — it should stay the half-scale baby box", babyWidth, pig.width)
	}
	if got := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; got != 1 {
		t.Fatalf("held stack count = %d after feeding a baby, want 1 (the item is consumed even at 0 speedup)", got)
	}
}

// TestAgeUpClampsAtZero: ageUp never overshoots past 0 — a baby fed an amount whose *20 ticks would
// land positive clamps EXACTLY to 0 (adult), and the onGrewUp side effect path restores the adult AABB.
// This unit-tests the ageUp clamp + the feed-path 0-crossing handler directly (the per-feed speedup can
// never cross in one real feed, but a large manual ageUp exercises the clamp + grow-up faithfully).
func TestAgeUpClampsAtZero(t *testing.T) {
	loop, pig := feedTestPig(t)
	pig.breedAge = -10
	pig.refreshDimensions()
	adultWidth := pig.adultWidth

	// ageUp(1) = -10 + 1*20 = +10 -> clamped to 0 (a baby never overshoots into the breeding cooldown).
	wasBaby := pig.isBaby()
	pig.ageUp(1)
	if pig.breedAge != 0 {
		t.Fatalf("ageUp(1) on a -10 baby: breedAge = %d, want 0 (clamp, never positive)", pig.breedAge)
	}
	// Drive the same 0-crossing side effect the feed path performs.
	if wasBaby && !pig.isBaby() {
		loop.onGrewUp(pig)
	}
	if pig.width != adultWidth {
		t.Fatalf("after growing up, width = %v, want the adult footprint %v (onGrewUp refreshDimensions)", pig.width, adultWidth)
	}
}

// TestFeedNonFoodNoOp: feeding a NON-pig_food item (dirt id 1) to an adult leaves inLove 0 and consumes
// nothing (isFood gate).
func TestFeedNonFoodNoOp(t *testing.T) {
	loop, pig := feedTestPig(t)
	p := feedPlayerHolding(loop, pig.x, pig.y, pig.z, 1, 3) // id 1 = dirt, not pig_food

	loop.tryFeedAnimal(p, pig)

	if pig.inLove != 0 {
		t.Fatalf("feeding dirt set inLove = %d, want 0 (isFood must gate)", pig.inLove)
	}
	if got := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; got != 3 {
		t.Fatalf("held stack count = %d after feeding a non-food item, want 3 (no consume)", got)
	}
}

// TestFeedAlreadyInLoveNoOp: feeding an adult ALREADY in love does nothing (canFallInLove is false) —
// no second consume, inLove unchanged. The adult branch gates on canFallInLove (inLove <= 0).
func TestFeedAlreadyInLoveNoOp(t *testing.T) {
	loop, pig := feedTestPig(t)
	pig.setInLove() // already in love (inLove 600)
	p := feedPlayerHolding(loop, pig.x, pig.y, pig.z, 1257, 3)

	loop.tryFeedAnimal(p, pig)

	if pig.inLove != defaultInLoveTime {
		t.Fatalf("feeding an already-in-love adult changed inLove to %d, want %d (canFallInLove gate)", pig.inLove, defaultInLoveTime)
	}
	if got := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; got != 3 {
		t.Fatalf("held stack count = %d after feeding an already-in-love adult, want 3 (no consume)", got)
	}
}

// TestInLoveDecrement: tickMobAging decrements inLove on an adult by 1 per tick, forces it to 0 the
// instant the mob is not an adult (breedAge != 0), and the %10 heart-cadence boundary does not panic.
func TestInLoveDecrement(t *testing.T) {
	loop, pig := feedTestPig(t)

	t.Run("adult decrements by 1", func(t *testing.T) {
		pig.breedAge = 0
		pig.inLove = 20
		loop.tickMobAging(pig)
		if pig.inLove != 19 {
			t.Fatalf("inLove after one tick = %d, want 19 (decrement by 1)", pig.inLove)
		}
	})

	t.Run("heart cadence boundary does not panic", func(t *testing.T) {
		pig.breedAge = 0
		pig.inLove = 11 // ticks to 10 -> 10 %% 10 == 0 -> the heart-cadence draw fires
		loop.tickMobAging(pig)
		if pig.inLove != 10 {
			t.Fatalf("inLove after the %%10 boundary tick = %d, want 10", pig.inLove)
		}
	})

	t.Run("non-adult forces inLove to 0", func(t *testing.T) {
		pig.breedAge = 6000 // a breeding-cooldown adult (not age 0) -> getAge() != 0 -> inLove = 0
		pig.inLove = 500
		loop.tickMobAging(pig)
		if pig.inLove != 0 {
			t.Fatalf("inLove on a cooldown mob = %d, want 0 (the getAge()!=0 -> inLove=0 guard)", pig.inLove)
		}
	})

	t.Run("baby forces inLove to 0", func(t *testing.T) {
		pig.breedAge = -100
		pig.refreshDimensions()
		pig.inLove = 500
		loop.tickMobAging(pig)
		if pig.inLove != 0 {
			t.Fatalf("inLove on a baby = %d, want 0 (a baby cannot be in love)", pig.inLove)
		}
	})
}

// TestHeartParticleEncoder: encodeLevelParticles emits the javap-confirmed ClientboundLevelParticles
// wire layout for a single HEART. Asserts the field ORDER and the load-bearing facts: count is a fixed
// 4-byte Int (NOT a VarInt) and the particle-type id is the TRAILING VarInt (HEART has no options bytes).
func TestHeartParticleEncoder(t *testing.T) {
	heartID := menuTypeID(registryid.ParticleType, "minecraft:heart") // == the particle registry index (52)
	if heartID < 0 {
		t.Fatal("minecraft:heart not found in registryid.ParticleType")
	}

	pkt := encodeLevelParticles(heartID, true, false, 1.5, 64.25, -3.5, 0.0, 0.0, 0.0, 0.0, 1)
	if pkt.ID != int32(packetid.ClientboundLevelParticles) {
		t.Fatalf("packet id = %d, want ClientboundLevelParticles (%d)", pkt.ID, int32(packetid.ClientboundLevelParticles))
	}

	// Rebuild the expected body field-by-field in the javap order and byte-compare.
	var want bytes.Buffer
	for _, f := range []pk.FieldEncoder{
		pk.Boolean(true),  // overrideLimiter
		pk.Boolean(false), // alwaysShow
		pk.Double(1.5),    // x
		pk.Double(64.25),  // y
		pk.Double(-3.5),   // z
		pk.Float(0.0),     // xDist
		pk.Float(0.0),     // yDist
		pk.Float(0.0),     // zDist
		pk.Float(0.0),     // maxSpeed
		pk.Int(1),         // count — writeInt, a FIXED 4-byte int
		pk.VarInt(heartID), // particle-type id — TRAILING VarInt (HEART = SimpleParticleType, no options)
	} {
		if _, err := f.WriteTo(&want); err != nil {
			t.Fatalf("building expected body: %v", err)
		}
	}
	if !bytes.Equal(pkt.Data, want.Bytes()) {
		t.Fatalf("encodeLevelParticles body mismatch:\n got %x\nwant %x", pkt.Data, want.Bytes())
	}
}

// TestFeedThroughHandleInteract: the FULL dispatch path — a ServerboundInteract naming the adult pig,
// from a player holding pig_food in the same region, sets the pig in love (exercises handleInteract's
// VarInt decode + owningRegion/regionForColumn resolve + the same-region gate + tryFeedAnimal).
func TestFeedThroughHandleInteract(t *testing.T) {
	loop, pig := feedTestPig(t)
	p := feedPlayerHolding(loop, pig.x, pig.y, pig.z, 1257, 5)

	// ServerboundInteract wire body: VarInt entityId ; InteractionHand (VarInt 0=main) ; Vec3 (3 doubles)
	// ; Boolean usingSecondaryAction. handleInteract only reads the leading entityId; the rest is decoded
	// defensively. We send a full faithful frame so the decode consumes a real packet.
	pkt := pk.Marshal(int32(packetid.ServerboundInteract),
		pk.VarInt(pig.id),  // entityId
		pk.VarInt(0),       // InteractionHand = MAIN_HAND
		pk.Double(pig.x), pk.Double(pig.y), pk.Double(pig.z), // Vec3 location
		pk.Boolean(false), // usingSecondaryAction
	)
	loop.handleInteract(p, pkt)

	if pig.inLove != defaultInLoveTime {
		t.Fatalf("after a ServerboundInteract feed, pig inLove = %d, want %d", pig.inLove, defaultInLoveTime)
	}
	if got := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; got != 4 {
		t.Fatalf("held stack count = %d after the interact-feed, want 4 (consume 1 of 5)", got)
	}
}
