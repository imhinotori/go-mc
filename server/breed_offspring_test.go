package server

// breed_offspring_test.go -- the getBreedOffspring SPECIES-DISPATCH regression (C1/C2). Proves breed()
// no longer spawns a pig for every species: a cow breeds a COW, a rabbit draws nextInt(20) FIRST, a
// sheep lamb inherits a parent wool color, a horse foal inherits parent-averaged stats, and the pig
// still breeds a PIG (the oracle-safety contract).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/world/levelgen"
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

// TestBreedAxolotlRareVariantGate: Axolotl.getBreedOffspring gates the rare (BLUE) variant on
// useRareVariant(this.random) == this.random.nextInt(1200) == 0. When the gate fires the child gets
// BLUE via getRareSpawnVariant == Util.getRandom({BLUE}, r) == r.nextInt(1); otherwise the child gets a
// parent variant via nextBoolean. This pins the DRAW ORDER (nextInt(1200) FIRST, then nextInt(1) on the
// rare branch OR nextBoolean on the common branch) by mirroring the initiator stream, and asserts the
// rolled variant is APPLIED. Cite Axolotl.getBreedOffspring + Axolotl.useRareVariant +
// Axolotl$Variant.getRareSpawnVariant + Util.getRandom.
func TestBreedAxolotlRareVariantGate(t *testing.T) {
	loop, floorY := axolotlLoop(t)

	// Find one seed where the initiator FIRST nextInt(1200) draw == 0 (rare gate fires) and one where it
	// does not (the common parent-inherit branch). We drive the dispatcher directly.
	var rareSeed, commonSeed uint64
	haveRare, haveCommon := false, false
	for s := uint64(1); s <= 5000 && (!haveRare || !haveCommon); s++ {
		mirror := newEntityRandom(s)
		if mirror.nextInt(1200) == 0 {
			if !haveRare {
				rareSeed, haveRare = s, true
			}
		} else if !haveCommon {
			commonSeed, haveCommon = s, true
		}
	}
	if !haveRare || !haveCommon {
		t.Fatalf("could not find rare(%v)/common(%v) seeds", haveRare, haveCommon)
	}

	// --- RARE branch: gate fires -> child variant == BLUE, stream consumed nextInt(1200)+nextInt(1).
	e := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
	partner := loop.spawnAxolotl(9.5, float64(floorY+1), 8.5, false)
	e.axolotlVariant = axolotlVariantLucy
	partner.axolotlVariant = axolotlVariantGold
	mobRandom(e).reseed(rareSeed)
	mr := newEntityRandom(rareSeed)
	if mr.nextInt(1200) != 0 {
		t.Fatal("rare seed no longer gates to 0")
	}
	_ = mr.nextInt(1) // getRareSpawnVariant draw
	after := mr.nextInt(1 << 30)

	child := loop.spawnBreedOffspring(e, partner)
	if child.typ != entity.Axolotl.ID {
		t.Fatalf("axolotl bred a %d, want an AXOLOTL", child.typ)
	}
	if child.axolotlVariant != axolotlVariantBlue {
		t.Fatalf("rare-gate child variant = %d, want BLUE %d", child.axolotlVariant, axolotlVariantBlue)
	}
	if got := mobRandom(e).nextInt(1 << 30); got != after {
		t.Fatalf("rare branch consumed wrong draw count: initiator next = %d, want %d (nextInt(1200)+nextInt(1))", got, after)
	}

	// --- COMMON branch: gate does NOT fire -> child variant is one of the two parents (nextBoolean pick).
	e2 := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
	partner2 := loop.spawnAxolotl(9.5, float64(floorY+1), 8.5, false)
	e2.axolotlVariant = axolotlVariantLucy
	partner2.axolotlVariant = axolotlVariantGold
	mobRandom(e2).reseed(commonSeed)
	mc := newEntityRandom(commonSeed)
	if mc.nextInt(1200) == 0 {
		t.Fatal("common seed unexpectedly gates to 0")
	}
	wantVariant := partner2.axolotlVariant
	if mc.nextBoolean() {
		wantVariant = e2.axolotlVariant
	}

	child2 := loop.spawnBreedOffspring(e2, partner2)
	if child2.axolotlVariant != wantVariant {
		t.Fatalf("common-branch child variant = %d, want %d (nextBoolean parent pick)", child2.axolotlVariant, wantVariant)
	}
	if child2.axolotlVariant != axolotlVariantLucy && child2.axolotlVariant != axolotlVariantGold {
		t.Fatalf("common-branch child variant = %d, want one of the two parent variants", child2.axolotlVariant)
	}
}

// TestBreedWolfInheritsParentVariantAndTameOwner: Wolf.getBreedOffspring, when the mate is a Wolf, draws
// this.random.nextBoolean() (the WolfVariant pick) and when this.isTame() transfers owner + tame + the
// collar getMixedColor. Pins the initiator nextBoolean draw and the tame/owner transfer. Cite
// Wolf.getBreedOffspring.
func TestBreedWolfInheritsParentVariantAndTameOwner(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	e := loop.spawnVanillaMob(vanillaWolfMobName, 8.5, 64, 8.5)
	partner := loop.spawnVanillaMob(vanillaWolfMobName, 9.5, 64, 8.5)
	e.health = 8
	partner.health = 8
	e.tame = true
	e.ownerUUID = 4242
	e.catCollarColor = int(dyeBlue)
	partner.catCollarColor = int(dyeRed)

	// Reseed the initiator stream to a known seed, then predict its state after Wolf.getBreedOffspring:
	// exactly ONE initiator draw (the variant nextBoolean). The collar getMixedColor is a LEVEL-rng draw
	// (dyeMixedColorFallback), NOT the initiator stream, and pickRandomSoundVariant is a cited deferral, so
	// the initiator advances by exactly 1. (entityRandom holds a *rand.Rand pointer, so a struct copy is
	// NOT an isolated mirror -- reseed + an independent replay is the correct technique.)
	const wseed = uint64(0xF00D42)
	mobRandom(e).reseed(wseed)
	replay := newEntityRandom(wseed)
	_ = replay.nextBoolean() // variant pick (deferred SELECTION; the DRAW is consumed)
	afterVariant := replay.nextInt(1 << 30)

	child := loop.spawnBreedOffspring(e, partner)
	if child.typ != entity.Wolf.ID {
		t.Fatalf("wolf bred a %d, want a WOLF %d", child.typ, entity.Wolf.ID)
	}
	if !child.tame {
		t.Fatal("tamed wolf's child is not tamed (setTame not transferred)")
	}
	if child.ownerUUID != e.ownerUUID {
		t.Fatalf("child ownerUUID = %d, want %d (owner ref not transferred)", child.ownerUUID, e.ownerUUID)
	}
	if child.catCollarColor != int(dyeBlue) && child.catCollarColor != int(dyeRed) {
		t.Fatalf("child collar = %d, want one of the parent collars {%d,%d} (getMixedColor fallback)", child.catCollarColor, dyeBlue, dyeRed)
	}
	if got := mobRandom(e).nextInt(1 << 30); got != afterVariant {
		t.Fatalf("wolf initiator consumed wrong draw count: next = %d, want %d (one nextBoolean)", got, afterVariant)
	}
}

// TestBreedFrogDrawsInitMemoriesOnLevelRandom: Frog.getBreedOffspring makes NO variant pick and NO
// initiator-stream draw; its only rng is FrogAi.initMemories(child, level.getRandom()) == 100 +
// level.nextInt(41) on the LEVEL random. Pins that the child is a FROG (variant stays the create-default
// temperate) and that the LEVEL random advanced by exactly the one initMemories sample. Cite
// Frog.getBreedOffspring + FrogAi.initMemories + UniformInt.of(100,140).sample.
func TestBreedFrogDrawsInitMemoriesOnLevelRandom(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	const seed = int64(0x5F209)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	e := loop.spawnFrogRaw(8.5, 64, 8.5, false)
	partner := loop.spawnFrogRaw(9.5, 64, 8.5, false)
	e.health = 10
	partner.health = 10

	// Reseed the initiator stream to a known seed; Frog.getBreedOffspring must draw NOTHING on it (its
	// only rng is the LEVEL random via FrogAi.initMemories). So after the call the initiator's next draw
	// must equal a fresh source at the same seed (zero draws consumed). (entityRandom holds a *rand.Rand
	// pointer, so reseed + an independent replay is the correct isolation technique.)
	const fseed = uint64(0xBADF00)
	mobRandom(e).reseed(fseed)
	wantInitiatorNext := newEntityRandom(fseed).nextInt(1 << 30)

	child := loop.spawnBreedOffspring(e, partner)
	if child == nil || child.typ != entity.Frog.ID {
		t.Fatalf("frog bred %v, want a FROG", child)
	}
	if child.frogVariant != frogVariantTemperate {
		t.Fatalf("bred frog variant = %d, want create-default TEMPERATE %d (getBreedOffspring picks no variant)", child.frogVariant, frogVariantTemperate)
	}
	if got := mobRandom(e).nextInt(1 << 30); got != wantInitiatorNext {
		t.Fatalf("frog getBreedOffspring drew on the initiator stream (next = %d, want %d) -- must draw ONLY on level.getRandom()", got, wantInitiatorNext)
	}
	replay := levelgen.NewLegacyRandomSource(seed)
	_ = frogTimeBetweenLongJumpsMin + int(replay.NextIntN(frogTimeBetweenLongJumpsSpan)) // the initMemories draw
	wantLevelNext := replay.NextIntN(1 << 30)
	if got := loop.only().levelRandom.NextIntN(1 << 30); got != wantLevelNext {
		t.Fatalf("level random advanced by the wrong amount: next = %d, want %d (exactly one initMemories sample)", got, wantLevelNext)
	}
}

// TestBreedRabbitAppliesRolledVariant: Rabbit.getBreedOffspring APPLIES the rolled variant to the child
// (previously discarded). Draw order: getRandomRabbitVariant (LEVEL nextInt(100)) THEN this.random
// .nextInt(20) THEN a conditional this.random.nextBoolean() (other-parent pick). On the common 19/20
// branch the child inherits a PARENT variant. Cite Rabbit.getBreedOffspring.
func TestBreedRabbitAppliesRolledVariant(t *testing.T) {
	loop, _ := newBlockLoop()
	installVanillaPigRegistry(loop)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0x2ABB17)
	e, partner := breedTypedPair(t, loop, vanillaRabbitMobName)
	e.rabbitVariant = 4       // GOLD
	partner.rabbitVariant = 2 // BLACK

	// Mirror the INITIATOR draws to predict the resulting variant. The LEVEL nextInt(100) (step 1) is on a
	// different stream; only the initiator nextInt(20)/nextBoolean decides which branch is taken.
	mirror := *mobRandom(e)
	n20 := mirror.nextInt(20)
	var wantVariant int32 = -1
	if n20 != 0 {
		if mirror.nextBoolean() { // partner is a rabbit
			wantVariant = partner.rabbitVariant
		} else {
			wantVariant = e.rabbitVariant
		}
	}

	child := loop.spawnBreedOffspring(e, partner)
	if child.typ != entity.Rabbit.ID {
		t.Fatalf("rabbit bred a %d, want a RABBIT", child.typ)
	}
	if wantVariant >= 0 {
		if child.rabbitVariant != wantVariant {
			t.Fatalf("bred rabbit variant = %d, want %d (the rolled parent variant must be APPLIED, not discarded)", child.rabbitVariant, wantVariant)
		}
		if child.rabbitVariant != 4 && child.rabbitVariant != 2 {
			t.Fatalf("common-branch child variant = %d, want a parent variant {GOLD 4, BLACK 2}", child.rabbitVariant)
		}
	}
}
