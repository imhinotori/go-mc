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
