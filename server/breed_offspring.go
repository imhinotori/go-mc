package server

import "github.com/imhinotori/sulfur/data/entity"


// breed_offspring.go -- getBreedOffspring SPECIES DISPATCH (C1/C2 fix). breed() used to always spawn a
// pig, so EVERY animal bred a PIG. spawnBreedOffspring dispatches on the initiator type (e.typ) to spawn
// the species-correct baby and consume the EXACT getBreedOffspring RNG in vanilla order + source
// (this.random == initiator per-entity stream for most; ServerLevel.getRandom() == owning region
// levelRandom for Sheep/Goat + DyeColor.getMixedColor). Baby age + hitbox + DATA_BABY_ID are applied by
// breed(), as for the pig. Ported 1:1 from 26.2 jar (javap-verified). SINGLE-OWNER (TICK-05): tick
// goroutine; child adds to the owning region store.

// spawnBreedOffspring dispatches getBreedOffspring on e.typ. e is the initiator (vanilla this/animal),
// partner the mate (vanilla otherParent). nil only when the species would return null (canMate gates it).
func (t *TickLoop) spawnBreedOffspring(e, partner *Entity) *Entity {
	switch e.typ {
	case entity.Pig.ID:
		// Pig.getBreedOffspring: PIG child; nextBoolean (variant) on the initiator stream. BYTE-IDENTICAL
		// to the pre-fix breed() so the pinned pig oracle stream is undisturbed. PigVariant cite-deferred.
		child := t.spawnVanillaPig(e.x, e.y, e.z)
		inheritFromInitiator := mobRandom(e).nextBoolean()
		_ = inheritFromInitiator // PigVariant deferred (no field) -- the DRAW is what the oracle pins.
		return child
	case entity.Cow.ID:
		// Cow.getBreedOffspring: COW child; nextBoolean (variant) when other is a Cow. CowVariant deferred.
		child := t.spawnVanillaMob(vanillaCowMobName, e.x, e.y, e.z)
		if partner.typ == entity.Cow.ID {
			_ = mobRandom(e).nextBoolean()
		}
		return child
	case entity.Mooshroom.ID:
		// MushroomCow.getBreedOffspring: MOOSHROOM child; setVariant(getOffspringVariant(other)) on the
		// initiator stream. mooshroomVariant is a REAL field, so set the child.
		child := t.spawnVanillaMob(vanillaMooshroomMobName, e.x, e.y, e.z)
		child.mooshroomVariant = mooshroomOffspringVariant(e, partner)
		return child
	case entity.Chicken.ID:
		// Chicken.getBreedOffspring: CHICKEN child; nextBoolean (variant) when other is a Chicken.
		child := t.spawnVanillaMob(vanillaChickenMobName, e.x, e.y, e.z)
		if partner.typ == entity.Chicken.ID {
			_ = mobRandom(e).nextBoolean()
		}
		return child
	case entity.Rabbit.ID:
		// Rabbit.getBreedOffspring: RABBIT child. nextInt(20) FIRST; if !=0 and other is a Rabbit,
		// nextBoolean inherits a parent variant. The DRAW ORDER (nextInt(20) then conditional nextBoolean)
		// is the parity contract. RabbitVariant deferred.
		child := t.spawnVanillaMob(vanillaRabbitMobName, e.x, e.y, e.z)
		if mobRandom(e).nextInt(20) != 0 && partner.typ == entity.Rabbit.ID {
			_ = mobRandom(e).nextBoolean()
		}
		return child
	case entity.Sheep.ID:
		// Sheep.getBreedOffspring: SHEEP child; setColor(DyeColor.getMixedColor(level, c1, c2)). Recipe
		// branch DEFERRED (no recipe subsystem); fallback level.getRandom().nextBoolean() -> c1/c2.
		child := t.spawnVanillaMob(vanillaSheepMobName, e.x, e.y, e.z)
		child.sheepColor = t.dyeMixedColorFallback(sheepGetColor(e), sheepGetColor(partner))
		return child
	case entity.Cat.ID:
		// Cat.getBreedOffspring: CAT child; nextBoolean (variant) on the initiator stream, then if
		// this.isTame() collar = getMixedColor (recipe DEFERRED -> nextBoolean picks col1/col2). CatVariant
		// deferred (no field); catCollarColor is REAL.
		child := t.spawnVanillaMob(vanillaCatMobName, e.x, e.y, e.z)
		_ = mobRandom(e).nextBoolean()
		if e.horseTamed { // Cat reuses horseTamed as the TamableAnimal tamed flag
			child.catCollarColor = int(t.dyeMixedColorFallback(byte(e.catCollarColor), byte(partner.catCollarColor)))
		}
		return child
	case entity.Fox.ID:
		// Fox.getBreedOffspring: FOX child; nextBoolean (variant) on the initiator stream. FoxVariant deferred.
		child := t.spawnVanillaMob(vanillaFoxMobName, e.x, e.y, e.z)
		_ = mobRandom(e).nextBoolean()
		return child
	case entity.Panda.ID:
		// Panda.getBreedOffspring: PANDA child; setGeneFromParents + setAttributes draw on the CHILD stream
		// (spawnPandaChild ports the exact order).
		return t.spawnPandaChild(e, partner, e.x, e.y, e.z)
	case entity.Goat.ID:
		// Goat.getBreedOffspring: GOAT child. level.getRandom().nextBoolean() picks the interacting parent;
		// if that parent is NOT a screaming goat, level.getRandom().nextDouble()<0.02 sets screaming.
		// Screaming variant cite-deferred (no field); the level-stream DRAW ORDER is the contract.
		child := t.spawnGoat(e.x, e.y, e.z, true)
		if lr := t.breedLevelRandom(); lr != nil {
			interacting := e
			if !lr.NextBoolean() {
				interacting = partner
			}
			_ = interacting
			_ = lr.NextDouble() < 0.02 // screaming = 2% (GoatScreaming deferred)
		}
		return child
	case entity.Horse.ID, entity.Donkey.ID, entity.Mule.ID, entity.Llama.ID, entity.TraderLlama.ID:
		return t.spawnHorseFamilyOffspring(e, partner)
	case entity.Bee.ID:
		return t.spawnBee(e.x, e.y, e.z, true)
	case entity.Camel.ID:
		return t.spawnCamel(e.x, e.y, e.z, true)
	case entity.Armadillo.ID:
		return t.spawnArmadillo(e.x, e.y, e.z, true)
	case entity.Sniffer.ID:
		return t.spawnSniffer(e.x, e.y, e.z, true)
	case entity.Axolotl.ID:
		// Axolotl.getBreedOffspring: nextInt(500)==0 -> rare variant else a parent variant, on the
		// initiator stream. axolotlVariant is REAL; keep the initiator on the common 499/500 branch.
		child := t.spawnAxolotl(e.x, e.y, e.z, true)
		if mobRandom(e).nextInt(500) != 0 {
			child.axolotlVariant = e.axolotlVariant
		}
		return child
	default:
		// A breedable species not yet wired: keep like-breeds-like via the declared-mob registry if the
		// initiator is a declared vanilla mob; else the pig path as a last resort (unreached above).
		if t.mobRegistry != nil {
			if name, ok := breedRegistryNameFor(e.typ); ok {
				if _, present := t.mobRegistry.byName[name]; present {
					return t.spawnVanillaMob(name, e.x, e.y, e.z)
				}
			}
		}
		return t.spawnVanillaPig(e.x, e.y, e.z)
	}
}

// mooshroomOffspringVariant ports MushroomCow.getOffspringVariant(other): same variant -> nextInt(1024)
// ==0 flips RED<->BROWN else keep this; different -> nextBoolean picks this (true) / other (false). On the
// initiator stream. Cite MushroomCow.getOffspringVariant.
func mooshroomOffspringVariant(e, partner *Entity) int32 {
	v1, v2 := e.mooshroomVariant, partner.mooshroomVariant
	if v1 == v2 {
		if mobRandom(e).nextInt(1024) == 0 {
			if v1 == mooshroomVariantBrown {
				return mooshroomVariantRed
			}
			return mooshroomVariantBrown
		}
		return v1
	}
	if mobRandom(e).nextBoolean() {
		return v1
	}
	return v2
}

// breedRegistryNameFor maps a declared-vanilla-mob entity type to its registry name for the default
// like-breeds-like fallback (only registry-spawned CREATUREs need an entry).
func breedRegistryNameFor(typ entity.ID) (string, bool) {
	switch typ {
	case entity.Cow.ID:
		return vanillaCowMobName, true
	case entity.Sheep.ID:
		return vanillaSheepMobName, true
	case entity.Chicken.ID:
		return vanillaChickenMobName, true
	case entity.Rabbit.ID:
		return vanillaRabbitMobName, true
	case entity.Mooshroom.ID:
		return vanillaMooshroomMobName, true
	case entity.Cat.ID:
		return vanillaCatMobName, true
	case entity.Fox.ID:
		return vanillaFoxMobName, true
	case entity.Ocelot.ID:
		return vanillaOcelotMobName, true
	case entity.Turtle.ID:
		return vanillaTurtleMobName, true
	case entity.Wolf.ID:
		return vanillaWolfMobName, true
	case entity.Pig.ID:
		return vanillaPigMobName, true
	}
	return "", false
}

// breedLevelRandom returns the owning region levelRandom (ServerLevel.getRandom() analogue) for
// Sheep/Goat/getMixedColor breeding draws, or nil in a bare test loop (graceful degrade).
func (t *TickLoop) breedLevelRandom() interface {
	NextBoolean() bool
	NextDouble() float64
} {
	if r := t.cur(); r != nil && r.levelRandom != nil {
		return r.levelRandom
	}
	return nil
}

// dyeMixedColorFallback ports the RNG fallback of DyeColor.getMixedColor(level, a, b): recipe branch
// DEFERRED (no recipe subsystem); level.getRandom().nextBoolean() -> a (true) / b (false). nil region
// degrades to a. Cite DyeColor.getMixedColor.
func (t *TickLoop) dyeMixedColorFallback(a, b byte) byte {
	if lr := t.breedLevelRandom(); lr != nil {
		if lr.NextBoolean() {
			return a
		}
		return b
	}
	return a
}
