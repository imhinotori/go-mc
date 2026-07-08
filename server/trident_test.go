package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// trident_test.go covers the trident port (trident.go): the throw (spawn a ThrownTrident at 2.5x look
// velocity, consuming 1 in survival), onHitEntity (flat 8 damage), the Loyalty return-to-owner homing +
// re-pickup, the Riptide self-launch, and the below-threshold no-op release.

// mkTrident builds a trident stack carrying max_damage + damage + the given enchantments (mirrors
// mkEnchantableSword) so the durability + enchant seams read real values.
func mkTrident(enchants map[string]int) component.SlotData {
	base := component.SlotData{ItemID: pk.VarInt(item.Trident.ID), Count: 1}
	e := editStack(base)
	e.setInt(enchTypeMaxDamage, 250) // trident durability
	e.setInt(enchTypeDamage, 0)
	if len(enchants) > 0 {
		e.setEnchantments(enchants)
	} else {
		e.setEnchantments(map[string]int{})
	}
	return e.materialize()
}

func firstTrident(loop *TickLoop) *Entity {
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isTrident {
				return e
			}
		}
	}
	return nil
}

func tridentStart(loop *TickLoop, p *tickPlayer) bool {
	inv := ensureInventory(p)
	return loop.tryStartTridentUse(p, inv, inv.get(hotbarMenuSlotBase), interactionHandMain)
}

func tridentInvCount(p *tickPlayer) int {
	inv := ensureInventory(p)
	total := 0
	// Slots 9..playerInventorySize span the main inventory AND the hotbar (36..44) -- one scan, no double count.
	for i := int16(9); i < playerInventorySize; i++ {
		s := inv.get(i)
		if !slotIsEmpty(s) && item.ID(s.ItemID) == item.Trident.ID {
			total += int(s.Count)
		}
	}
	return total
}

// TestTridentThrowSpawnsAt2p5xLook: a full-hold release of a plain trident spawns a ThrownTrident with
// velocity magnitude 2.5 along the look (+Z), sets isArrow+isTrident, and consumes exactly 1 trident.
func TestTridentThrowSpawnsAt2p5xLook(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase, mkTrident(nil))
	inv.heldSlot = 0

	if !tridentStart(loop, p) {
		t.Fatalf("tryStartTridentUse returned false for a trident")
	}
	if !isUsingItem(p) {
		t.Fatalf("player is not using the trident after tryStartTridentUse")
	}
	p.useItemRemaining = tridentUseDuration - 20 // held past the 10-tick throw threshold

	loop.releaseUsingItem(p)

	a := firstTrident(loop)
	if a == nil {
		t.Fatalf("no thrown trident spawned on release")
	}
	if !a.isArrow || !a.isTrident {
		t.Fatalf("thrown trident flags: isArrow=%v isTrident=%v, want both true", a.isArrow, a.isTrident)
	}
	mag := math.Sqrt(a.vx*a.vx + a.vy*a.vy + a.vz*a.vz)
	if math.Abs(mag-2.5) > 1e-6 {
		t.Fatalf("thrown trident velocity magnitude = %.6f, want ~2.5", mag)
	}
	if a.vz <= 0 || math.Abs(a.vx) > 1e-6 || math.Abs(a.vy) > 1e-6 {
		t.Fatalf("trident not thrown straight along +Z: v=(%.4f,%.4f,%.4f)", a.vx, a.vy, a.vz)
	}
	if got := inv.get(hotbarMenuSlotBase); !slotIsEmpty(got) {
		t.Fatalf("survival throw did not consume the trident (held slot count=%d)", got.Count)
	}
	if isUsingItem(p) {
		t.Fatalf("use state not cleared after trident release")
	}
}

// TestTridentBelowThresholdNoThrow: a release under the 10-tick THROW_THRESHOLD_TIME throws nothing and
// keeps the trident in hand.
func TestTridentBelowThresholdNoThrow(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase, mkTrident(nil))
	inv.heldSlot = 0

	tridentStart(loop, p)
	p.useItemRemaining = tridentUseDuration - 5 // charge 5 < 10 -> below threshold

	loop.releaseUsingItem(p)

	if a := firstTrident(loop); a != nil {
		t.Fatalf("below-threshold release spawned a trident")
	}
	if got := inv.get(hotbarMenuSlotBase); slotIsEmpty(got) {
		t.Fatalf("below-threshold release consumed the trident")
	}
	if isUsingItem(p) {
		t.Fatalf("use state not cleared after below-threshold release")
	}
}

// TestTridentCreativeNoConsume: a creative throw spawns a trident but consumes none, and marks it CREATIVE_ONLY.
func TestTridentCreativeNoConsume(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeCreative)
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase, mkTrident(nil))
	inv.heldSlot = 0

	tridentStart(loop, p)
	p.useItemRemaining = tridentUseDuration - 20
	loop.releaseUsingItem(p)

	a := firstTrident(loop)
	if a == nil {
		t.Fatalf("creative trident release spawned no trident")
	}
	if !a.tridentCreativeOnly {
		t.Fatalf("creative-thrown trident is not CREATIVE_ONLY pickup")
	}
	if got := inv.get(hotbarMenuSlotBase); slotIsEmpty(got) {
		t.Fatalf("creative throw consumed the trident")
	}
}

// TestTridentOnHitDealsEight: a ThrownTrident whose flight segment passes through a survival player deals
// exactly 8 damage.
func TestTridentOnHitDealsEight(t *testing.T) {
	loop := bowLoop() // bowLoop wires each region's world (ChunkManager) so isSolidAt does not panic
	shooter := combatPlayer(loop, 1)
	shooter.x, shooter.y, shooter.z = 0, 64, 0
	victim := combatPlayer(loop, 2)
	victim.x, victim.y, victim.z = 0, 64, 5

	// Spawn a thrown trident just short of the victim, moving +Z so its flight segment sweeps the victim.
	e := loop.spawnThrownTrident(shooter.entityID, 0, 64+playerStandingEyeHeight, 4.0, 0, 0, 1.5, mkTrident(nil), false)
	if e == nil {
		t.Fatalf("spawnThrownTrident returned nil")
	}
	loop.tickArrows()

	want := float32(maxHealth) - 8.0
	if victim.health != want {
		t.Fatalf("trident hit victim health = %v, want %v (8 damage)", victim.health, want)
	}
	if !e.tridentDealtDamage {
		t.Fatalf("trident dealtDamage flag not set after the hit")
	}
}

// TestTridentLoyaltyReturnsToOwner: a Loyalty trident that has dealt damage homes toward its owner (its
// velocity acquires a component pointing back), and reaching the owner gives the trident back.
func TestTridentLoyaltyReturnsToOwner(t *testing.T) {
	loop := bowLoop()
	owner := bowPlayer(loop, gameModeSurvival)
	owner.entityID = 7
	owner.x, owner.y, owner.z = 0, 64, 0

	// A Loyalty-3 trident far from the owner, already dealtDamage, moving AWAY (+Z). The return homing must
	// pull it back toward the owner (a -Z velocity component after the pre-tick lerp).
	e := loop.spawnThrownTrident(owner.entityID, 0, 64, 20, 0, 0, 0.5, mkTrident(map[string]int{enchLoyalty: 3}), false)
	if e == nil {
		t.Fatalf("spawnThrownTrident returned nil")
	}
	if e.tridentLoyalty != 3 {
		t.Fatalf("trident loyalty level = %d, want 3", e.tridentLoyalty)
	}
	e.tridentDealtDamage = true // it has hit something -> Loyalty return begins

	loop.tickTridentPre(e)
	if !e.tridentReturning {
		t.Fatalf("Loyalty trident did not enter the returning (noPhysics) state")
	}
	// The return lerp is deltaMovement.scale(0.95).add(toOwner.normalize()*0.05*loyalty). The homing term
	// points at the owner (-Z here), so the post-lerp vz must be BELOW the pure-inertia value (0.5*0.95),
	// proving the pull toward the owner was applied. (Homing is gradual -- vanilla builds it over ticks.)
	pureInertia := 0.5 * tridentReturnKeepInert
	if e.vz >= pureInertia {
		t.Fatalf("returning trident vz = %.4f, want < %.4f (homing pull toward the owner at z=0)", e.vz, pureInertia)
	}

	// Place the trident within pickup range of the owner and tick again: it should discard + give back.
	loop.cur().entities.move(e, owner.x, owner.y+playerStandingEyeHeight, owner.z)
	beforeCount := tridentInvCount(owner)
	handled := loop.tickTridentPre(e)
	if !handled {
		t.Fatalf("Loyalty trident within pickup range was not discarded on arrival")
	}
	if got := tridentInvCount(owner); got != beforeCount+1 {
		t.Fatalf("owner trident count after return = %d, want %d (item given back)", got, beforeCount+1)
	}
}

// TestTridentRiptideLaunchesPlayer: releasing a Riptide trident in rain launches the player (adds a
// look-direction impulse to the player velocity) and throws no projectile.
func TestTridentRiptideLaunchesPlayer(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	p.playerEntity = &Entity{id: p.entityID}
	p.pitch = -90 // look straight up -> vy = -sin(pitch) = +1 (a clean +Y launch)
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase, mkTrident(map[string]int{enchRiptide: 3}))
	inv.heldSlot = 0

	// Force rain so isInWaterOrRain is true (the riptide gate).
	loop.weather.raining = true
	loop.weather.rainLevel = 1.0

	if !tridentStart(loop, p) {
		t.Fatalf("tryStartTridentUse returned false for a riptide trident")
	}
	if !isUsingItem(p) {
		t.Fatalf("riptide trident draw did not begin in rain")
	}
	p.useItemRemaining = tridentUseDuration - 20
	loop.releaseUsingItem(p)

	if a := firstTrident(loop); a != nil {
		t.Fatalf("riptide release spawned a thrown trident (should launch the player instead)")
	}
	// Riptide 3: f = 1.5 + 0.75*2 = 3.0, straight up -> vy ~ 3.0.
	if p.playerEntity.vy < 2.5 {
		t.Fatalf("riptide launch vy = %.4f, want ~3.0 (Riptide 3 straight up)", p.playerEntity.vy)
	}
}
