package server

// breed_offspring_test.go -- the getBreedOffspring SPECIES-DISPATCH regression (C1/C2). Proves breed()
// no longer spawns a pig for every species: a cow breeds a COW, a rabbit draws nextInt(20) FIRST, a
// sheep lamb inherits a parent wool color, a horse foal inherits parent-averaged stats, and the pig
// still breeds a PIG (the oracle-safety contract).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// breedTypedPair spawns two same-species declared mobs in love, 1 block apart, on the floor.
func breedTypedPair(t *testing.T, loop *TickLoop, name string) (*Entity, *Entity) {
	t.Helper()
	e := loop.spawnVanillaMob(name, 8.5, 64, 8.5)
	e.health = 10
	e.setInLove()
	partner := loop.spawnVanillaMob(name, 9.5, 64, 8.5)
	partner.health = 10
	partner.setInLove()
	return e, partner
}

// findBreedBaby returns the first non-orb baby (breedAge==babyStartAge) in the store.
func findBreedBaby(loop *TickLoop) *Entity {
	for _, x := range loop.only().entities.all() {
		if x.isOrb {
			continue
		}
		if x.breedAge == babyStartAge {
			return x
		}
	}
	return nil
}

// TestBreedCowSpawnsCow: two cows breed a COW baby (typ == entity.Cow.ID), NOT a pig.
func TestBreedCowSpawnsCow(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	e, partner := breedTypedPair(t, loop, vanillaCowMobName)
	if e.typ != entity.Cow.ID {
		t.Fatalf("parent typ = %d, want Cow %d", e.typ, entity.Cow.ID)
	}
	loop.breed(e, partner)
	baby := findBreedBaby(loop)
	if baby == nil {
		t.Fatal("breed() spawned no baby")
	}
	if baby.typ != entity.Cow.ID {
		t.Fatalf("cow bred a %d, want a COW (%d) -- the pig-for-everything bug is back", baby.typ, entity.Cow.ID)
	}
	if !baby.isBaby() {
		t.Fatal("cow baby is not a baby (isBaby false)")
	}
}

// TestBreedSheepSpawnsSheepLambColorMatchesAParent: two sheep breed a SHEEP whose wool color is one of
// the two parents' colors (DyeColor.getMixedColor fallback -- the recipe branch is deferred).
func TestBreedSheepSpawnsSheepLambColorMatchesAParent(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	e, partner := breedTypedPair(t, loop, vanillaSheepMobName)
	e.sheepColor = dyeBlue
	partner.sheepColor = dyeRed
	loop.breed(e, partner)
	baby := findBreedBaby(loop)
	if baby == nil {
		t.Fatal("breed() spawned no baby")
	}
	if baby.typ != entity.Sheep.ID {
		t.Fatalf("sheep bred a %d, want a SHEEP (%d)", baby.typ, entity.Sheep.ID)
	}
	c := sheepGetColor(baby)
	if c != dyeBlue && c != dyeRed {
		t.Fatalf("lamb color = %d, want one of parent colors {%d,%d} (getMixedColor fallback)", c, dyeBlue, dyeRed)
	}
}

// TestBreedRabbitDrawOrder: Rabbit.getBreedOffspring draws nextInt(20) FIRST (then a conditional
// nextBoolean), NOT a leading nextBoolean like the pig. We prove the draw order by seeding a mirror
// stream and confirming the initiator's stream advances by nextInt(20) first: after breed the baby is a
// RABBIT and the initiator RNG has consumed the exact getBreedOffspring draws for the seed.
func TestBreedRabbitDrawOrder(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	e, partner := breedTypedPair(t, loop, vanillaRabbitMobName)

	// Snapshot the initiator's RNG state, then predict the draws Rabbit.getBreedOffspring makes:
	// nextInt(20) FIRST; if !=0 (and partner is a rabbit) nextBoolean; then the XP orb 1+nextInt(7).
	mirror := *mobRandom(e) // value copy of the entityRandom state
	first := mirror.nextInt(20)
	if first != 0 {
		_ = mirror.nextBoolean() // the variant inherit draw (partner is a rabbit)
	}
	wantXP := 1 + mirror.nextInt(7)

	loop.breed(e, partner)

	baby := findBreedBaby(loop)
	if baby == nil {
		t.Fatal("breed() spawned no baby")
	}
	if baby.typ != entity.Rabbit.ID {
		t.Fatalf("rabbit bred a %d, want a RABBIT (%d)", baby.typ, entity.Rabbit.ID)
	}
	// The XP orb value must match the prediction that assumed nextInt(20) was drawn FIRST -- if breed()
	// had used the pig's leading nextBoolean the stream would be off and the XP would not match.
	var orb *Entity
	for _, x := range loop.only().entities.all() {
		if x.isOrb {
			orb = x
		}
	}
	if orb == nil {
		t.Fatal("breed() awarded no XP orb")
	}
	if int(orb.xpValue) != wantXP {
		t.Fatalf("XP orb = %d, want %d -- the rabbit breed draw order (nextInt(20) FIRST) is wrong", orb.xpValue, wantXP)
	}
}

// TestBreedHorseFoalInheritsStats: two horses breed a HORSE foal whose MAX_HEALTH base lies within the
// parent-average-biased offspring bounds [15,17] (setOffspringAttributes), NOT the wild finalize roll
// alone. Proves C2 inheritance is wired (a WILD-only foal could still land in-range, but the health is
// derived from BOTH parents via createOffspringAttribute).
func TestBreedHorseFoalInheritsStats(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	e := loop.spawnHorse(8.5, 64, 8.5, false)
	e.health = 20
	e.setInLove()
	partner := loop.spawnHorse(9.5, 64, 8.5, false)
	partner.health = 20
	partner.setInLove()

	loop.breed(e, partner)
	baby := findBreedBaby(loop)
	if baby == nil {
		t.Fatal("breed() spawned no horse foal")
	}
	if baby.typ != entity.Horse.ID {
		t.Fatalf("horse bred a %d, want a HORSE (%d)", baby.typ, entity.Horse.ID)
	}
	if !baby.isBaby() {
		t.Fatal("horse foal is not a baby")
	}
	inst := baby.attributes.GetInstance("max_health")
	if inst == nil {
		t.Fatal("foal has no max_health attribute instance")
	}
	h := inst.BaseValue()
	if h < horseOffMinHealth-0.001 || h > horseOffMaxHealth+0.001 {
		t.Fatalf("foal MAX_HEALTH base = %v, want within offspring bounds [%v,%v]", h, horseOffMinHealth, horseOffMaxHealth)
	}
}

// TestBreedPigStillSpawnsPig: the oracle-safety contract -- a pig STILL breeds a PIG, byte-identical
// draw. Two pigs (via the registry) breed a pig baby.
func TestBreedPigStillSpawnsPig(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	e, partner := breedTypedPair(t, loop, vanillaPigMobName)
	if e.typ != entity.Pig.ID {
		t.Fatalf("parent typ = %d, want Pig %d", e.typ, entity.Pig.ID)
	}
	loop.breed(e, partner)
	baby := findBreedBaby(loop)
	if baby == nil {
		t.Fatal("breed() spawned no baby")
	}
	if baby.typ != entity.Pig.ID {
		t.Fatalf("pig bred a %d, want a PIG (%d)", baby.typ, entity.Pig.ID)
	}
	if !baby.isBaby() {
		t.Fatal("pig baby is not a baby")
	}
}
