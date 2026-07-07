package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// xp_orb_test.go covers WR-06 (XP-ORB PICKUP): the orb homes toward a nearby player
// (followNearbyPlayer), is collected on touch (playerTouch -> giveExperiencePoints + discard), the
// XP bar updates and a ClientboundSetExperience is sent, and a player on takeXpDelay cooldown does not
// collect. Mirrors item_entity_test.go.

// newOrb spawns an ExperienceOrb carrying value at (x,y,z), added to the loop's single region store,
// marked isOrb with its xpValue — exactly as awardExperienceOrbs spawns it on a mob death.
func newOrb(loop *TickLoop, x, y, z float64, value int) *Entity {
	orb := NewEntity(loop.idAlloc.AllocID(), entity.ExperienceOrb, x, y, z)
	orb.isOrb = true
	orb.xpValue = value
	orb.orbCount = 1 // ExperienceOrb.count default (1), as the real spawn sites set
	loop.only().entities.add(orb)
	return orb
}

// TestOrbHomesTowardNearbyPlayer (WR-06): an orb spawned a few blocks from a player gains velocity
// TOWARD the player after one tick (followNearbyPlayer's homing impulse). The orb is EAST of the player,
// so after the tick its vx must be negative (moving WEST, toward the player).
func TestOrbHomesTowardNearbyPlayer(t *testing.T) {
	loop, _ := newDropLoop()

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000

	// Orb 3 blocks EAST of the player (same z), well within the 8-block follow range.
	orb := newOrb(loop, 11.5, 64.0, 8.5, 3)

	vxBefore := orb.vx
	loop.tickOrb(orb)

	if orb.followingPlayerID != p.entityID {
		t.Fatalf("orb followingPlayerID = %d, want %d (the nearby player within 8 blocks)", orb.followingPlayerID, p.entityID)
	}
	// The orb is EAST of the player, so the homing impulse points WEST (-x): vx must decrease.
	if orb.vx >= vxBefore {
		t.Fatalf("orb vx = %v (was %v), want it to DECREASE (home WEST toward the player EAST of the orb)", orb.vx, vxBefore)
	}
}

// TestOrbNoFollowOutOfRange: an orb farther than 8 blocks from every player gains no follow target and
// no horizontal homing impulse (only gravity acts). Pins the 8-block follow range gate.
func TestOrbNoFollowOutOfRange(t *testing.T) {
	loop, _ := newDropLoop()

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000

	// Orb 20 blocks away — far outside the 8-block follow range.
	orb := newOrb(loop, 28.5, 64.0, 8.5, 3)

	loop.tickOrb(orb)

	if orb.followingPlayerID != 0 {
		t.Fatalf("orb followingPlayerID = %d, want 0 (no player within 8 blocks)", orb.followingPlayerID)
	}
	if orb.vx != 0 {
		t.Fatalf("orb vx = %v, want 0 (no horizontal homing impulse out of follow range)", orb.vx)
	}
}

// TestOrbCollectedGivesXP (WR-06, the core fix): a player touching an orb collects it — the XP bar
// updates (totalExperience grows by the orb value), the orb is discarded from the store, a
// ClientboundSetExperience and a ClientboundTakeItemEntity (the orb suck) are sent, and takeXpDelay is
// armed to 2.
func TestOrbCollectedGivesXP(t *testing.T) {
	loop, mgr := newDropLoop()
	// Floor so the orb does not free-fall out of pickup range during the scan.
	mgr.SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000

	// Orb right at the player's feet (inside the inflated pickup box) carrying 3 XP.
	orb := newOrb(loop, 8.5, 64.0, 8.5, 3)
	p.tracked = map[int32]bool{orb.id: true} // so takeItem's sendToTrackingPlayers reaches the collector

	if p.totalExperience != 0 {
		t.Fatalf("precondition: player totalExperience = %d, want 0", p.totalExperience)
	}

	loop.scanOrbPickup(p)

	if _, ok := loop.only().entities.get(orb.id); ok {
		t.Fatalf("orb NOT collected by a player on top of it — the reported XP-orb pickup bug")
	}
	if p.totalExperience != 3 {
		t.Fatalf("player totalExperience = %d, want 3 (the orb value awarded via giveExperiencePoints)", p.totalExperience)
	}
	if p.takeXpDelay != orbTakeXpDelay {
		t.Fatalf("player takeXpDelay = %d, want %d (armed on a successful pickup)", p.takeXpDelay, orbTakeXpDelay)
	}
	// experienceProgress = 3 / getXpNeededForNextLevel(0) = 3/7 (level 0 cost is 7 + 0*2 = 7).
	if p.experienceProgress <= 0 {
		t.Fatalf("player experienceProgress = %v, want > 0 (3/7 of level 0)", p.experienceProgress)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundSetExperience); n < 1 {
		t.Fatalf("ClientboundSetExperience emitted %d times, want >= 1 (the XP-bar update)", n)
	}
	if n := countID(got, packetid.ClientboundTakeItemEntity); n < 1 {
		t.Fatalf("ClientboundTakeItemEntity emitted %d times, want >= 1 (the orb-suck animation)", n)
	}
}

// TestOrbNotCollectedDuringTakeXpDelay: a player whose takeXpDelay > 0 does NOT collect an orb it is
// standing on (the per-player XP cooldown). Pins the takeXpDelay==0 gate.
func TestOrbNotCollectedDuringTakeXpDelay(t *testing.T) {
	loop, _ := newDropLoop()

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000
	p.takeXpDelay = 2 // cooldown running -> not collectible this tick

	orb := newOrb(loop, 8.5, 64.0, 8.5, 3)

	loop.scanOrbPickup(p)

	if _, ok := loop.only().entities.get(orb.id); !ok {
		t.Fatalf("orb collected while takeXpDelay=%d, want it left (cooldown running)", p.takeXpDelay)
	}
	if p.totalExperience != 0 {
		t.Fatalf("player gained XP during takeXpDelay cooldown (total=%d), want 0", p.totalExperience)
	}
}

// TestOrbAgesAndDespawns: the orb tick increments age each tick and DISCARDS the orb at LIFETIME (6000).
func TestOrbAgesAndDespawns(t *testing.T) {
	loop, _ := newDropLoop()
	orb := newOrb(loop, 8.5, 64.0, 8.5, 3)

	orb.age = orbLifetime - 1
	loop.tickOrb(orb) // age -> 6000 >= LIFETIME -> discard

	if _, ok := loop.only().entities.get(orb.id); ok {
		t.Fatalf("orb still in store at age %d, want despawned at LIFETIME %d", orb.age, orbLifetime)
	}
}

// TestGiveExperienceLevelsUp: giveExperiencePoints rolls the level up when progress crosses 1.0. A
// level-0 player given 7 XP (== getXpNeededForNextLevel(0)) reaches exactly level 1 with progress 0.
func TestGiveExperienceLevelsUp(t *testing.T) {
	loop, _ := newDropLoop()
	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000

	loop.giveExperiencePoints(p, 7) // level 0 costs 7 -> exactly one level

	if p.experienceLevel != 1 {
		t.Fatalf("experienceLevel = %d, want 1 (7 XP == the level-0 cost)", p.experienceLevel)
	}
	if p.experienceProgress != 0 {
		t.Fatalf("experienceProgress = %v, want 0 (the level rolled cleanly)", p.experienceProgress)
	}
	if p.totalExperience != 7 {
		t.Fatalf("totalExperience = %d, want 7", p.totalExperience)
	}
}

// TestXpNeededForNextLevel pins the jar curve at the two breakpoints (15 and 30) and below.
func TestXpNeededForNextLevel(t *testing.T) {
	cases := []struct {
		level int32
		want  int32
	}{
		{0, 7},    // 7 + 0*2
		{14, 35},  // 7 + 14*2 (still the <15 branch)
		{15, 37},  // 37 + 0*5
		{29, 107}, // 37 + 14*5
		{30, 112}, // 112 + 0*9
		{40, 202}, // 112 + 10*9
	}
	for _, c := range cases {
		if got := getXpNeededForNextLevel(c.level); got != c.want {
			t.Fatalf("getXpNeededForNextLevel(%d) = %d, want %d", c.level, got, c.want)
		}
	}
}

// TestOrbMergeEqualValue (B-A8): two orbs of the SAME value within merge range combine — the primary
// absorbs the other one count and the absorbed orb is removed from the store. Drives tickOrb at an
// age where age%20==1 so scanForMerges runs (ExperienceOrb.scanForMerges every 20 ticks).
func TestOrbMergeEqualValue(t *testing.T) {
	loop, _ := newDropLoop()

	primary := newOrb(loop, 8.5, 64.0, 8.5, 3)
	other := newOrb(loop, 8.6, 64.0, 8.6, 3) // same value, well inside the 0.5-inflated box
	primary.age = 1                           // age%20==1 -> scanForMerges fires this tick

	loop.tickOrb(primary)

	if _, ok := loop.only().entities.get(other.id); ok {
		t.Fatalf("second equal-value orb still present, want merged+removed")
	}
	if primary.orbCount != 2 {
		t.Fatalf("primary orbCount = %d, want 2 (1 + the absorbed orb count)", primary.orbCount)
	}
}

// TestOrbNoMergeDifferentValue (B-A8): orbs of DIFFERENT value never merge (canMerge gates on equal
// value), so both survive and the primary count stays 1.
func TestOrbNoMergeDifferentValue(t *testing.T) {
	loop, _ := newDropLoop()

	primary := newOrb(loop, 8.5, 64.0, 8.5, 3)
	other := newOrb(loop, 8.6, 64.0, 8.6, 7) // DIFFERENT value -> not mergeable
	primary.age = 1

	loop.tickOrb(primary)

	if _, ok := loop.only().entities.get(other.id); !ok {
		t.Fatalf("different-value orb was merged away, want it left intact")
	}
	if primary.orbCount != 1 {
		t.Fatalf("primary orbCount = %d, want 1 (no merge across differing values)", primary.orbCount)
	}
}

// TestOrbGravityInAir (B-A8): an orb in open air (no water) loses vertical velocity by the 0.03 gravity
// step before the move. With the floor far below, gravity is applied and vy goes negative.
func TestOrbGravityInAir(t *testing.T) {
	loop, _ := newDropLoop()
	orb := newOrb(loop, 8.5, 200.0, 8.5, 3) // high up, nothing under it in the drop loop
	orb.age = 2                              // avoid the merge-scan tick

	loop.tickOrb(orb)

	if orb.vy >= 0 {
		t.Fatalf("orb vy = %v after a gravity tick in air, want negative (0.03 gravity minus drag)", orb.vy)
	}
}

// TestOrbWaterBuoyancy (B-A8): an orb whose eye is in water gets setUnderwaterMovement instead of
// gravity — a submerged orb starting at rest drifts UPWARD (vy becomes positive from the +0.0005 rise),
// the opposite of the in-air gravity case. A water column is placed around the orb so orbEyeInWater is true.
func TestOrbWaterBuoyancy(t *testing.T) {
	loop, mgr := newDropLoop()
	water := block.ToStateID[block.Water{Level: 0}]
	for y := 63; y <= 66; y++ {
		mgr.SetBlock(pk.Position{X: 8, Y: y, Z: 8}, water, dimMinY)
	}

	orb := newOrb(loop, 8.5, 64.0, 8.5, 3)
	orb.age = 2 // avoid the merge-scan tick

	if !loop.orbEyeInWater(orb) {
		t.Fatalf("precondition: orbEyeInWater = false, want true (orb eye inside the water column)")
	}
	loop.tickOrb(orb)

	if orb.vy <= 0 {
		t.Fatalf("orb vy = %v underwater, want positive (setUnderwaterMovement buoyant rise, not gravity)", orb.vy)
	}
}

// TestOrbLavaPop (B-A8): an orb sitting in a lava cell gets the random horizontal + fixed 0.2 upward
// pop each tick (getFluidState(blockPosition).is(LAVA)). The vy after the tick is positive (the 0.2 pop
// minus drag), and the orb draws from its own RNG for the horizontal kick.
func TestOrbLavaPop(t *testing.T) {
	loop, mgr := newDropLoop()
	lava := block.ToStateID[block.Lava{Level: 0}]
	mgr.SetBlock(pk.Position{X: 8, Y: 64, Z: 8}, lava, dimMinY)

	orb := newOrb(loop, 8.5, 64.5, 8.5, 3)
	orb.age = 2 // avoid the merge-scan tick

	loop.tickOrb(orb)

	if orb.vy <= 0 {
		t.Fatalf("orb vy = %v in lava, want positive (the 0.2 lava pop upward impulse)", orb.vy)
	}
}

// TestOrbMendingOnPickup (B-A8): a player collecting an orb repairs a damaged mending item FIRST, then
// the leftover XP lands on the bar. A mending diamond sword at damage 6 collecting a value-5 orb is
// fully repaired (toRepair=10, repaired=6) and the player gains 2 XP (5 - 6*5/10). Drives the full
// playerTouchOrb pickup path (not just repairPlayerItems in isolation).
func TestOrbMendingOnPickup(t *testing.T) {
	loop, _ := newDropLoop()

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000
	holdEnchanted(p, damageableStack(idDiamondSword, 1, 100, 6, enchTestEntry(t, "minecraft:mending", 1)))

	orb := newOrb(loop, 8.5, 64.0, 8.5, 5)
	p.tracked = map[int32]bool{orb.id: true}

	loop.playerTouchOrb(p, orb)

	inv := ensureInventory(p)
	if got := stackDamageValue(inv.get(heldWindowSlot(inv.heldSlot))); got != 0 {
		t.Fatalf("mending item damage after pickup = %d, want 0 (repaired first)", got)
	}
	if p.totalExperience != 2 {
		t.Fatalf("player totalExperience = %d, want 2 (5 - 6*5/10 int-division remainder)", p.totalExperience)
	}
	if _, ok := loop.only().entities.get(orb.id); ok {
		t.Fatalf("orb still present after pickup, want discarded (count 1 -> 0)")
	}
}
