package server

// goat_behavior_test.go -- behavior pins for the Goat RAM / horn-drop / fall-reduction / milking / age
// ports (RamTarget / Goat.dropHorn / Goat.calculateFallDamage / Goat.mobInteract / Goat.ageBoundaryReached,
// 1:1 javap this session). Complements goat_test.go (the spawn-attribute + screaming-roll pins).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestGoatRamDamageAndKnockback: goatRam hurts the victim by ATTACK_DAMAGE (2.0 adult) and knocks it back
// AWAY from the goat (a victim at +x recoils +x). Mirrors TestRavagerRoarAoE's two-entity template.
func TestGoatRamDamageAndKnockback(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	g.onGround = true

	victim := NewEntity(999101, entity.Cow, 9.5, float64(floorY+1), 8.5)
	victim.health = 10
	victim.width = 0.9
	victim.height = 1.4
	victim.onGround = true
	loop.regionForEntity(g).entities.add(victim)
	startHealth := victim.health

	loop.withRegion(loop.regionForEntity(g), func() {
		loop.goatRam(g, victim)
	})

	if dealt := startHealth - victim.health; math.Abs(float64(dealt)-goatAdultAttackDamage) > 1e-6 {
		t.Fatalf("ram dealt %v damage, want %v (ATTACK_DAMAGE)", dealt, goatAdultAttackDamage)
	}
	if victim.vx <= 0 {
		t.Fatalf("ram did NOT knock the cow back (vx %v <= 0) -- knockback missed", victim.vx)
	}
	if g.goatRamCooldownTicks <= 0 {
		t.Fatalf("finishRam did not reseed RAM_COOLDOWN_TICKS (got %d)", g.goatRamCooldownTicks)
	}
}

// TestGoatBabyRamKnockbackForce: a BABY goat rams with the 1.0 force, an ADULT with 2.5.
func TestGoatBabyRamKnockbackForce(t *testing.T) {
	if goatBabyRamKnockbackForce >= goatAdultRamKnockbackForce {
		t.Fatalf("baby ram force %v should be < adult %v", goatBabyRamKnockbackForce, goatAdultRamKnockbackForce)
	}
	loop, floorY := goatLoop(t)
	baby := loop.spawnGoat(8.5, float64(floorY+1), 8.5, true)
	if got := goatRamKnockbackForce(baby); got != goatBabyRamKnockbackForce {
		t.Fatalf("baby goatRamKnockbackForce = %v, want %v", got, goatBabyRamKnockbackForce)
	}
	adult := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	if got := goatRamKnockbackForce(adult); got != goatAdultRamKnockbackForce {
		t.Fatalf("adult goatRamKnockbackForce = %v, want %v", got, goatAdultRamKnockbackForce)
	}
}

func countGoatHornItems(r *region) int {
	n := 0
	for _, e := range r.entities.byID {
		if e.isItem && e.itemStack.ItemID == pk.VarInt(item.GoatHorn.ID) {
			n++
		}
	}
	return n
}

// TestGoatDropHorn: an ADULT goat with both horns snaps a horn -> a goat_horn ItemEntity spawns and a
// horn flag flips false. A BABY never drops.
func TestGoatDropHorn(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	g.goatHasLeftHorn = true
	g.goatHasRightHorn = true
	region := loop.regionForEntity(g)
	before := countGoatHornItems(region)

	var dropped bool
	loop.withRegion(region, func() {
		dropped = loop.goatDropHorn(g)
	})
	if !dropped {
		t.Fatal("goatDropHorn returned false for an adult with two horns")
	}
	if after := countGoatHornItems(region); after != before+1 {
		t.Fatalf("goat_horn item count = %d, want %d (one drop)", after, before+1)
	}
	if g.goatHasLeftHorn && g.goatHasRightHorn {
		t.Fatal("goatDropHorn did not clear either horn flag")
	}
	baby := loop.spawnGoat(8.5, float64(floorY+1), 8.5, true)
	baby.goatHasLeftHorn = true
	baby.goatHasRightHorn = true
	if loop.goatDropHorn(baby) {
		t.Fatal("a baby goat dropped a horn (Goat.dropHorn: isBaby -> false)")
	}
}

// TestGoatHasRammedHornBreakingBlock: a stone floor block is in #snaps_goat_horn.
func TestGoatHasRammedHornBreakingBlock(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	g.y = float64(floorY)
	g.vx, g.vz = 1.0, 0.0
	loop.withRegion(loop.regionForEntity(g), func() {
		if !loop.goatHasRammedHornBreakingBlock(g) {
			t.Fatal("stone floor should be a #snaps_goat_horn ram surface")
		}
	})
}

// TestGoatFallDamageReduction: causeFallDamageEntity subtracts 10 for a goat vs a non-goat.
func TestGoatFallDamageReduction(t *testing.T) {
	loop, floorY := goatLoop(t)
	const d = 20.0

	cow := NewEntity(999201, entity.Cow, 8.5, float64(floorY+1), 8.5)
	cow.health = 100
	cow.width = 0.9
	cow.height = 1.4
	loop.regionForEntity(cow).entities.add(cow)
	cowStart := cow.health
	loop.withRegion(loop.regionForEntity(cow), func() { loop.causeFallDamageEntity(cow, d, 1.0) })
	cowDealt := cowStart - cow.health

	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	g.health = 100
	gStart := g.health
	loop.withRegion(loop.regionForEntity(g), func() { loop.causeFallDamageEntity(g, d, 1.0) })
	gDealt := gStart - g.health

	if math.Abs(float64(cowDealt-gDealt)-goatFallDamageReduction) > 1e-6 {
		t.Fatalf("goat fall %v vs cow %v; diff %v, want %d", gDealt, cowDealt, cowDealt-gDealt, goatFallDamageReduction)
	}
}

// TestGoatMilking: empty bucket on an ADULT goat -> milk_bucket; a baby is not milkable.
func TestGoatMilking(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)

	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 9.5, 424242)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(item.Bucket.ID)})

	if !loop.tryMilkGoat(p, g) {
		t.Fatal("tryMilkGoat returned false for an empty bucket on an adult goat")
	}
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if int32(held.ItemID) != int32(item.MilkBucket.ID) {
		t.Fatalf("hand item after milking = %d, want milk_bucket %d", held.ItemID, item.MilkBucket.ID)
	}

	baby := loop.spawnGoat(8.5, float64(floorY+1), 8.5, true)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(item.Bucket.ID)})
	if loop.tryMilkGoat(p, baby) {
		t.Fatal("tryMilkGoat succeeded on a baby goat (Goat.mobInteract !isBaby gate)")
	}
}

// TestGoatBabyAttackDamage: baby ATTACK_DAMAGE 1.0, adult 2.0 (Goat.ageBoundaryReached).
func TestGoatBabyAttackDamage(t *testing.T) {
	loop, floorY := goatLoop(t)
	baby := loop.spawnGoat(8.5, float64(floorY+1), 8.5, true)
	if got := baby.getAttributeValue(attribute.AttackDamage); math.Abs(got-goatBabyAttackDamage) > 1e-9 {
		t.Fatalf("baby goat ATTACK_DAMAGE = %v, want %v", got, goatBabyAttackDamage)
	}
	adult := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	if got := adult.getAttributeValue(attribute.AttackDamage); math.Abs(got-goatAdultAttackDamage) > 1e-9 {
		t.Fatalf("adult goat ATTACK_DAMAGE = %v, want %v", got, goatAdultAttackDamage)
	}
}
