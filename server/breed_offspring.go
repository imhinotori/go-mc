package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/server/registrydata"
)

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
		// Rabbit.getBreedOffspring (javap-verified):
		//   Rabbit$Variant v = getRandomRabbitVariant(level, this.blockPosition());   // LEVEL rng nextInt(100)
		//   if (this.random.nextInt(20) != 0) {                                       // INITIATOR rng, ALWAYS
		//       if (other instanceof Rabbit r && this.random.nextBoolean()) v = r.getVariant();
		//       else                                                        v = this.getVariant();
		//   }
		//   child.setVariant(v);
		// DRAW ORDER (the parity contract): (1) getRandomRabbitVariant draws ONE level.getRandom().nextInt(100)
		// ALWAYS (biome-branch pick, see getRandomRabbitVariant); (2) this.random.nextInt(20) ALWAYS; (3) ONLY
		// on the common 19/20 branch (nextInt(20) != 0) AND other is a Rabbit, this.random.nextBoolean() picks
		// the OTHER parent (true) vs THIS (false); the 1/20 branch keeps the biome pick from step 1. The
		// rolled variant is now APPLIED to the REAL rabbitVariant field (was previously discarded). Cite
		// Rabbit.getBreedOffspring + Rabbit.getRandomRabbitVariant + Rabbit.getVariant/setVariant.
		child := t.spawnVanillaMob(vanillaRabbitMobName, e.x, e.y, e.z)
		variant := t.getRandomRabbitVariant(e.x, e.y, e.z) // step (1): LEVEL rng nextInt(100)
		if mobRandom(e).nextInt(20) != 0 {                 // step (2): INITIATOR rng, always
			if partner.typ == entity.Rabbit.ID && mobRandom(e).nextBoolean() {
				variant = partner.rabbitVariant // other.getVariant()
			} else {
				variant = e.rabbitVariant // this.getVariant()
			}
		}
		child.rabbitVariant = variant
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
			// Goat.getBreedOffspring: nextBoolean() picks the interacting parent; if that parent is a
			// screaming goat the child is screaming; else nextDouble() < 0.02 rolls it. setScreamingGoat(child,
			// screaming). The draw ORDER (nextBoolean, then the nextDouble ONLY when the picked parent is not
			// screaming) is the exact level-stream contract. Cite Goat.getBreedOffspring.
			interacting := e
			if !lr.NextBoolean() {
				interacting = partner
			}
			screaming := false
			if interacting != nil && interacting.goatScreaming {
				screaming = true // the picked parent is a screamer -> the child inherits it (no nextDouble draw)
			} else if lr.NextDouble() < 0.02 {
				screaming = true // else a 2% roll (GOAT_SCREAMING_CHANCE) sets the child screaming
			}
			child.goatScreaming = screaming
		}
		return child
	case entity.Horse.ID, entity.Donkey.ID, entity.Mule.ID, entity.Llama.ID, entity.TraderLlama.ID:
		return t.spawnHorseFamilyOffspring(e, partner)
	case entity.Bee.ID:
		return t.spawnBee(e.x, e.y, e.z, true)
	case entity.Camel.ID:
		return t.spawnCamel(e.x, e.y, e.z, true)
	case entity.Strider.ID:
		// Strider.getBreedOffspring: EntityTypes.STRIDER.create(level, BREEDING) -- a plain baby STRIDER, NO RNG
		// draw (no variant). spawnStriderRaw is the non-finalizing create (finalizeSpawn's jockey/saddle roll is
		// for a NATURAL spawn, not a bred baby); mark it finalized + baby so it never re-rolls. Cite
		// Strider.getBreedOffspring.
		baby := t.spawnStriderRaw(e.x, e.y, e.z)
		if baby != nil {
			baby.striderFinalized = true
			baby.breedAge = babyStartAge
			baby.refreshDimensions()
		}
		return baby
	case entity.Armadillo.ID:
		return t.spawnArmadillo(e.x, e.y, e.z, true)
	case entity.Sniffer.ID:
		// Sniffer OVERRIDES spawnChildFromBreeding to drop a SNIFFER_EGG ItemEntity (NOT a live baby); that
		// override is handled in breed() BEFORE this dispatcher, so this case is unreachable. Kept as a
		// defensive guard: if ever reached, drop the egg rather than a live baby. Cite Sniffer.spawnChildFromBreeding.
		return nil
	case entity.Axolotl.ID:
		// Axolotl.getBreedOffspring (javap-verified): on the INITIATOR (this.random) stream --
		//   Axolotl$Variant v;
		//   if (useRareVariant(this.random))            v = Axolotl$Variant.getRareSpawnVariant(this.random);
		//   else                                        v = this.random.nextBoolean() ? this.getVariant()
		//                                                                              : other.getVariant();
		//   child.setVariant(v); child.setPersistenceRequired();
		// where useRareVariant(r) == r.nextInt(1200) == 0 (Axolotl.useRareVariant), and getRareSpawnVariant(r)
		// == Util.getRandom(rareVariants, r) == rareVariants[r.nextInt(rareVariants.length)]. The rare set is
		// the single-entry {BLUE} (Axolotl$Variant.getSpawnVariant(r,false) filters common==false; only BLUE
		// has common=false: ids LUCY0/WILD1/GOLD2/CYAN3 common=true, BLUE4 common=false), so the rare draw is
		// r.nextInt(1) (always 0) -> BLUE(4). The DRAW ORDER is the contract: (1) nextInt(1200) gate ALWAYS;
		// (2a) rare -> nextInt(1); (2b) common -> nextBoolean. axolotlVariant is a REAL field. Cite
		// Axolotl.getBreedOffspring + Axolotl.useRareVariant + Axolotl$Variant.getRareSpawnVariant/
		// getSpawnVariant + Util.getRandom(T[],RandomSource).
		child := t.spawnAxolotl(e.x, e.y, e.z, true)
		r := mobRandom(e)
		if r.nextInt(1200) == 0 {
			_ = r.nextInt(1)                          // getRareSpawnVariant: Util.getRandom({BLUE}, r) == r.nextInt(1)
			child.axolotlVariant = axolotlVariantBlue // the single rare-set entry
		} else if r.nextBoolean() {
			child.axolotlVariant = e.axolotlVariant
		} else {
			child.axolotlVariant = partner.axolotlVariant
		}
		return child
	case entity.Wolf.ID:
		// Wolf.getBreedOffspring (javap-verified): the ENTIRE body below is gated on `other instanceof Wolf`
		// (bytecode `aload_2; instanceof Wolf; ifeq return`) -- if the mate is not a Wolf the child is
		// returned with NO rng draw. canMate already gates a wolf mate to a tamed Wolf, so the common path
		// takes the block:
		//   if (other instanceof Wolf otherWolf) {
		//       child.setVariant(this.random.nextBoolean() ? this.getVariant() : otherWolf.getVariant());
		//       if (this.isTame()) {
		//           child.setOwnerReference(this.getOwnerReference());
		//           child.setTame(true, true);
		//           child.setCollarColor(DyeColor.getMixedColor(level, this.getCollarColor(), otherWolf.getCollarColor()));
		//       }
		//       child.setSoundVariant(WolfSoundVariants.pickRandomSoundVariant(registryAccess, this.random));
		//   }
		// DRAW ORDER (the contract), all gated on other-is-Wolf: (1) this.random.nextBoolean() for the
		// WolfVariant Holder pick (Holder registry NOT modeled in v1 -> the SELECTION is a cited deferral but
		// the INITIATOR nextBoolean DRAW is consumed exactly); (2) if this.isTame(): owner ref + setTame +
		// the collar getMixedColor (recipe DEFERRED -> level.getRandom().nextBoolean() fallback, the same
		// dyeMixedColorFallback the Cat/Sheep cases use); (3) ALWAYS (still inside the other-is-Wolf block)
		// this.random.nextInt(soundVariantRegistrySize) for pickRandomSoundVariant == Registry.getRandom(r)
		// (WOLF_SOUND_VARIANT registry NOT modeled -> DEFERRED, structured to become a real nextInt(size)
		// draw). ownerUUID/tame/catCollarColor (the wolf reuses catCollarColor as its DyeColor collar slot)
		// are REAL and transferred. Cite Wolf.getBreedOffspring + WolfSoundVariants.pickRandomSoundVariant
		// (Registry.getRandom) + DyeColor.getMixedColor.
		child := t.spawnVanillaMob(vanillaWolfMobName, e.x, e.y, e.z)
		if partner.typ == entity.Wolf.ID {
			// (1) WolfVariant pick: setVariant(nextBoolean() ? this : other). WolfVariant Holder not modeled;
			// consume the INITIATOR nextBoolean exactly (the variant SELECTION is the cited deferral).
			_ = mobRandom(e).nextBoolean()
			// (2) if tamed: transfer owner + tame + collar getMixedColor.
			if e.tame {
				child.ownerUUID = e.ownerUUID // setOwnerReference(getOwnerReference())
				child.tame = true             // setTame(true, true)
				child.catCollarColor = int(t.dyeMixedColorFallback(byte(e.catCollarColor), byte(partner.catCollarColor)))
			}
			// (3) pickRandomSoundVariant(registryAccess, this.random) == Registry.getRandom(this.random).
			// Registry.getRandom == Util.getRandomSafe(entries, rng), which for a non-empty registry is
			// entries.get(rng.nextInt(size)) — exactly ONE nextInt(size) on the INITIATOR stream, run
			// UNCONDITIONALLY (bytecode pc111-118, outside the isTame branch). #34: the WolfSoundVariant
			// Holder is not yet modeled for playback, but the DRAW is now CONSUMED (size = the 7 embedded
			// wolf_sound_variant registry entries) so the initiator's RNG stream stays in vanilla lockstep —
			// omitting it desynced every later per-entity draw. The variant SELECTION (which sound) remains
			// deferred (cosmetic, sound-only). CITE: WolfSoundVariants.pickRandomSoundVariant / Registry.getRandom.
			if n := registrydata.WolfSoundVariantCount(); n > 0 {
				_ = mobRandom(e).nextInt(n)
			}
		}
		return child
	case entity.Frog.ID:
		// Frog.getBreedOffspring (javap-verified):
		//   Frog child = EntityTypes.FROG.create(level, BREEDING);   // raw create; NO finalizeSpawn, NO variant pick
		//   if (child != null) FrogAi.initMemories(child, level.getRandom());
		//   return child;
		// The variant is NOT set here (it stays the create-time DEFAULT_VARIANT temperate -- getBreedOffspring
		// does NOT run the biome variant pick that finalizeSpawn does; that is a separate code path). The only
		// rng is FrogAi.initMemories, which draws ONE TIME_BETWEEN_LONG_JUMPS.sample == UniformInt.of(100,140)
		// .sample(rng) == 100 + rng.nextInt(41) on the LEVEL random (ServerLevel.getRandom() == this region's
		// levelRandom). spawnFrogRaw is the non-finalizing create (matches EntityType.create(BREEDING)); the
		// caller (breed) applies setBaby(true). Cite Frog.getBreedOffspring + FrogAi.initMemories +
		// UniformInt.sample.
		child := t.spawnFrogRaw(e.x, e.y, e.z, false)
		if child != nil {
			if lr := t.cur(); lr != nil && lr.levelRandom != nil {
				// FrogAi.initMemories: TIME_BETWEEN_LONG_JUMPS.sample == 100 + level.nextInt(41).
				_ = frogTimeBetweenLongJumpsMin + int(lr.levelRandom.NextIntN(frogTimeBetweenLongJumpsSpan))
			}
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

// getRandomRabbitVariant ports Rabbit.getRandomRabbitVariant(level, pos) 1:1:
//
//	Holder<Biome> biome = level.getBiome(pos);
//	int n = level.getRandom().nextInt(100);                 // ONE level-rng draw, ALWAYS
//	if (biome.is(BiomeTags.SPAWNS_WHITE_RABBITS)) return n < 80 ? WHITE(1) : WHITE_SPLOTCHED(3);
//	if (biome.is(BiomeTags.SPAWNS_GOLD_RABBITS))  return GOLD(4);   // NOTE: gold path takes NO extra draw
//	return n < 50 ? BROWN(0) : (n < 90 ? SALT(5) : BLACK(2));
//
// The two biome tags (BiomeTags.SPAWNS_WHITE_RABBITS / SPAWNS_GOLD_RABBITS) are a cited const-false
// reduction in v1 (no biome-tag facility, and the default/superflat biome is in NEITHER tag), so the pick
// always falls to the BROWN/SALT/BLACK nextInt(100) branch -- IDENTICAL to the Rabbit.finalizeSpawn
// reduction already in plugin_mob_decl.go. CRUCIALLY the nextInt(100) draw is STILL taken on the LEVEL rng
// (level.getRandom() == this region's levelRandom) exactly as the jar, so a co-spawned mob's level-rng
// stream stays byte-in-lockstep. A nil region levelRandom (a bare test loop) skips the draw and returns the
// DEFAULT BROWN(0) -- the graceful degrade the sheep-color / cat-gift paths use. Cite
// Rabbit.getRandomRabbitVariant + Rabbit.Variant static init (BROWN 0, WHITE 1, BLACK 2, WHITE_SPLOTCHED 3,
// GOLD 4, SALT 5).
func (t *TickLoop) getRandomRabbitVariant(x, y, z float64) int32 {
	if r := t.cur(); r != nil && r.levelRandom != nil {
		n := int(r.levelRandom.NextIntN(100)) // ALWAYS: the single level-rng draw
		// biome-tag gates (SPAWNS_WHITE_RABBITS / SPAWNS_GOLD_RABBITS) cited const-false -> the else branch:
		switch {
		case n < 50:
			return 0 // BROWN
		case n < 90:
			return 5 // SALT
		default:
			return 2 // BLACK
		}
	}
	return 0 // BROWN (DEFAULT; bare test loop with no region levelRandom skips the draw)
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
