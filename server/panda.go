// panda.go -- the Panda (net.minecraft.world.entity.animal.panda.Panda), a 1:1 port from the
// unobfuscated 26.2 jar. Panda is a bamboo-jungle Animal driven by a GENE system: each panda carries a
// MAIN gene and a HIDDEN gene (both Panda.Gene, 0..6); its OBSERVABLE variant is getVariantFromGenes(
// main, hidden) -- a RECESSIVE gene (BROWN/WEAK) only shows when main==hidden, else the variant is
// NORMAL. The 7 genes are NORMAL/LAZY/WORRIED/PLAYFUL/BROWN/WEAK/AGGRESSIVE; the WEAK variant halves the
// panda health (MAX_HEALTH 10) and the LAZY variant slows it (MOVEMENT_SPEED 0.07), applied per-instance
// by setAttributes(). Genes are rolled at spawn (finalizeSpawn: two getRandom draws) or inherited at
// breed (setGeneFromParents: a fixed draw order + a nextInt(32) per-gene mutation). This port lands the
// attributes + spawn + the FULL gene/variant system (main+hidden gene, getVariantFromGenes, spawn roll,
// breed roll, setAttributes per-variant divergence) + the "visibly alive" passive goal subset. The roll/
// sneeze/sit/lie temperament cosmetics + eat-bamboo animation are the DEFERRED behavior layer. Code-
// spawned (spawnPanda); its per-tick extra is pandaAiStep from tickAI (gated on typ == entity.Panda.ID).
//
// VANILLA (verified javap Panda this session):
//   createAttributes: Animal.createAnimalAttributes + MOVEMENT_SPEED 0.15000000596046448 + ATTACK_DAMAGE
//     6.0 (NO MAX_HEALTH override -> stays the living default 20.0). See pandaSupplier.
//   Panda.Gene(id, name, isRecessive): NORMAL(0,f) LAZY(1,f) WORRIED(2,f) PLAYFUL(3,f) BROWN(4,t)
//     WEAK(5,t) AGGRESSIVE(6,f).
//   Gene.getVariantFromGenes(a,b): if a.isRecessive() then (a==b ? a : NORMAL) else a.
//   Gene.getRandom(rng): i=nextInt(16); 0->LAZY 1->WORRIED 2->PLAYFUL 4->AGGRESSIVE; i<9->WEAK; i<11->
//     BROWN; else NORMAL.
//   getVariant(): getVariantFromGenes(getMainGene(), getHiddenGene()).
//   setAttributes(): if isWeak() MAX_HEALTH.setBaseValue(10.0); if isLazy() MOVEMENT_SPEED.setBaseValue(
//     0.07000000029802322).
//   finalizeSpawn: setMainGene(getRandom(rng)); setHiddenGene(getRandom(rng)); setAttributes().
//   setGeneFromParents(a,b): nextBoolean picks which parent seeds main vs hidden; getOneOfGenesRandomly
//     (nextBoolean each) / getRandom per branch; then nextInt(32)==0 re-rolls main, nextInt(32)==0 re-
//     rolls hidden. getOneOfGenesRandomly(): nextBoolean ? getMainGene : getHiddenGene.
//   getBreedOffspring: create PANDA; child.setGeneFromParents(this, other); child.setAttributes().
//   registerGoals: @0 Float; @2 PandaPanic(2.0); @2 PandaBreed(1.0); @3 PandaAttack(1.2000000476837158,
//     true); @4 Tempt(1.0, PANDA_FOOD, false); @6 PandaAvoid(Player,8,2,2); @6 PandaAvoid(Monster,4,2,2);
//     @7 PandaSit; @8 PandaLieOnBack; @8 PandaSneeze; @9 PandaLookAtPlayer(6.0); @10 RandomLookAround;
//     @12 PandaRoll; @13 FollowParent(1.25); @14 WaterAvoidingRandomStroll(1.0). target @1 PandaHurtBy.
//
// v1 STUBS (cited): the temperament goals (Sit/LieOnBack/Sneeze/Roll) + the character-layer (Panic/
// Attack/Avoid/HurtBy) are DEFERRED; this port supplies the bounded passive walk (Float/Panic/Breed/
// Tempt(PANDA_FOOD=bamboo)/Follow/Stroll/Look) and the FULL, EXACT gene/variant system. The observable
// attributes, the gene->variant mapping, and the gene-roll RNG draw ORDER are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Panda.Gene ids (VERIFIED javap Panda Gene static init). Last ctor arg is isRecessive.
const (
	pandaGeneNormal     = 0 // NORMAL(0, normal, false)
	pandaGeneLazy       = 1 // LAZY(1, lazy, false)
	pandaGeneWorried    = 2 // WORRIED(2, worried, false)
	pandaGenePlayful    = 3 // PLAYFUL(3, playful, false)
	pandaGeneBrown      = 4 // BROWN(4, brown, true) RECESSIVE
	pandaGeneWeak       = 5 // WEAK(5, weak, true) RECESSIVE
	pandaGeneAggressive = 6 // AGGRESSIVE(6, aggressive, false)
	pandaGeneMax        = 6 // MAX_GENE == AGGRESSIVE.getId()
)

// Panda constants (VERIFIED javap Panda this session).
const (
	pandaMovementSpeed   = 0.15000000596046448 // createAttributes MOVEMENT_SPEED (float-widened)
	pandaAttackDamage    = 6.0                 // createAttributes ATTACK_DAMAGE 6.0
	pandaWeakMaxHealth   = 10.0                // setAttributes(): isWeak() -> MAX_HEALTH.setBaseValue(10.0)
	pandaLazyMovementSpd = 0.07000000029802322 // setAttributes(): isLazy() -> MOVEMENT_SPEED.setBaseValue
	pandaPanicSpeed      = 2.0                 // @2 PandaPanicGoal speed 2.0
	pandaBreedSpeed      = 1.0                 // @2 PandaBreedGoal speed 1.0
	pandaTemptSpeed      = 1.0                 // @4 TemptGoal speed 1.0
	pandaFollowSpeed     = 1.25                // @13 FollowParentGoal speed 1.25
	pandaStrollSpeed     = 1.0                 // @14 WaterAvoidingRandomStrollGoal speed 1.0
	pandaLookDistance    = 6.0                 // @9 PandaLookAtPlayerGoal distance 6.0f
	pandaFoodTag         = "panda_food"        // PANDA_FOOD (bamboo): the tempt predicate
	pandaGeneMutateBound = 32                  // setGeneFromParents nextInt(32) per-gene mutation chance
	pandaGeneRandomBound = 16                  // Gene.getRandom nextInt(16)
)

// pandaGeneIsRecessive ports Panda.Gene.isRecessive(): only BROWN(4) and WEAK(5) are recessive.
func pandaGeneIsRecessive(gene int) bool {
	return gene == pandaGeneBrown || gene == pandaGeneWeak
}

// pandaGetVariantFromGenes ports Panda.Gene.getVariantFromGenes(a, b): a recessive main gene shows only
// when main==hidden; otherwise a recessive main masks to NORMAL, and a dominant main always shows.
func pandaGetVariantFromGenes(main, hidden int) int {
	if pandaGeneIsRecessive(main) {
		if main == hidden {
			return main
		}
		return pandaGeneNormal
	}
	return main
}

// pandaGeneGetRandom ports Panda.Gene.getRandom(RandomSource): the 16-way weighted roll. Draws ONE
// nextInt(16). Cite Panda Gene.getRandom.
func pandaGeneGetRandom(r *entityRandom) int {
	i := r.nextInt(pandaGeneRandomBound)
	switch {
	case i == 0:
		return pandaGeneLazy
	case i == 1:
		return pandaGeneWorried
	case i == 2:
		return pandaGenePlayful
	case i == 4:
		return pandaGeneAggressive
	case i < 9:
		return pandaGeneWeak
	case i < 11:
		return pandaGeneBrown
	default:
		return pandaGeneNormal
	}
}

// pandaGetVariant ports Panda.getVariant(): getVariantFromGenes(mainGene, hiddenGene).
func pandaGetVariant(e *Entity) int {
	return pandaGetVariantFromGenes(e.pandaMainGene, e.pandaHiddenGene)
}

// pandaIsWeak / pandaIsLazy port Panda.isWeak() / isLazy(): getVariant() == WEAK / LAZY.
func pandaIsWeak(e *Entity) bool { return pandaGetVariant(e) == pandaGeneWeak }
func pandaIsLazy(e *Entity) bool { return pandaGetVariant(e) == pandaGeneLazy }

// pandaGetOneOfGenesRandomly ports Panda.getOneOfGenesRandomly(): nextBoolean ? mainGene : hiddenGene.
func pandaGetOneOfGenesRandomly(e *Entity, r *entityRandom) int {
	if r.nextBoolean() {
		return e.pandaMainGene
	}
	return e.pandaHiddenGene
}

// pandaSetAttributes ports Panda.setAttributes(): the per-variant live-instance divergence (WEAK ->
// MAX_HEALTH 10, LAZY -> MOVEMENT_SPEED 0.07 via setBaseValue). NORMAL/other keeps supplier defaults.
// A nil map is a defensive no-op. Cite Panda.setAttributes.
func pandaSetAttributes(e *Entity) {
	if e.attributes == nil {
		return
	}
	if pandaIsWeak(e) {
		if inst := e.attributes.GetInstance(attribute.MaxHealth.Name()); inst != nil {
			inst.SetBaseValue(pandaWeakMaxHealth)
		}
	}
	if pandaIsLazy(e) {
		if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
			inst.SetBaseValue(pandaLazyMovementSpd)
		}
	}
}

// pandaSetGeneFromParents ports Panda.setGeneFromParents(Panda a, Panda b): breeding gene inheritance
// with the EXACT vanilla draw order. b==nil is the single-parent branch (kept faithful). Draw order on
// the child rng: nextBoolean picks which parent seeds main vs hidden; getOneOfGenesRandomly (nextBoolean
// each) / getRandom per branch; then nextInt(32)==0 re-rolls main to getRandom, nextInt(32)==0 re-rolls
// hidden. child is e, a == parentA, b == parentB. Cite Panda.setGeneFromParents.
func pandaSetGeneFromParents(e, a, b *Entity, r *entityRandom) {
	if b == nil {
		if r.nextBoolean() {
			e.pandaMainGene = pandaGetOneOfGenesRandomly(a, r)
			e.pandaHiddenGene = pandaGeneGetRandom(r)
		} else {
			e.pandaMainGene = pandaGeneGetRandom(r)
			e.pandaHiddenGene = pandaGetOneOfGenesRandomly(a, r)
		}
	} else {
		if r.nextBoolean() {
			e.pandaMainGene = pandaGetOneOfGenesRandomly(a, r)
			e.pandaHiddenGene = pandaGetOneOfGenesRandomly(b, r)
		} else {
			e.pandaMainGene = pandaGetOneOfGenesRandomly(b, r)
			e.pandaHiddenGene = pandaGetOneOfGenesRandomly(a, r)
		}
	}
	if r.nextInt(pandaGeneMutateBound) == 0 {
		e.pandaMainGene = pandaGeneGetRandom(r)
	}
	if r.nextInt(pandaGeneMutateBound) == 0 {
		e.pandaHiddenGene = pandaGeneGetRandom(r)
	}
}

// newPandaAI builds the Panda bounded passive AI: the subset of Panda.registerGoals (the temperament +
// character-layer goals are DEFERRED, cited in the header). Mirrors newGoatAI/newCamelAI shape with the
// faithful Panda goal speeds (Float@0, Panic(2.0)@2, Breed(1.0)@2, Tempt(1.0,PANDA_FOOD)@4, Look(6.0)@9,
// RandomLook@10, Follow(1.25)@13, Stroll(1.0)@14). Cite Panda.registerGoals.
func newPandaAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * pandaMovementSpeed // seed with MOVEMENT_SPEED (0.15)
	m.navigation.canFloat = true                             // FloatGoal ctor: setCanFloat(true)
	applyAnimalPathfindingMalus(m)                           // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(2, newPanicGoal(pandaPanicSpeed))
	m.goals.addGoal(2, newBreedGoal(pandaBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(pandaTemptSpeed, func(id int32) bool { return itemInTag(id, pandaFoodTag) }, false))
	m.goals.addGoal(9, newLookAtPlayerGoal(pandaLookDistance))
	m.goals.addGoal(10, newRandomLookAroundGoal())
	m.goals.addGoal(13, newFollowParentGoal(pandaFollowSpeed))
	m.goals.addGoal(14, newWaterAvoidingRandomStrollGoal(pandaStrollSpeed))
	return m
}

// spawnPanda creates a Panda with the jar attributes + passive AI, then adds it to the owner region.
// Ports Panda.finalizeSpawn: setMainGene(getRandom(rng)); setHiddenGene(getRandom(rng)); setAttributes().
// Rolled on the panda OWN (id-reseeded) stream (two draws, this order). baby toggles the AgeableMob baby
// age + half-scale box. initSpawnHealth runs AFTER setAttributes so a WEAK panda is born at 10 health.
// Cite Panda.createAttributes + Panda.finalizeSpawn.
func (t *TickLoop) spawnPanda(x, y, z float64, baby bool) *Entity {
	p := NewEntity(t.idAlloc.AllocID(), entity.Panda, x, y, z)
	p.isPanda = true
	if baby {
		p.breedAge = babyStartAge
		p.refreshDimensions() // AgeableMob baby half-scale box
	}
	p.ai = newPandaAI()
	reseedMobAI(p.ai, p.id)
	// finalizeSpawn: setMainGene(getRandom(rng)); setHiddenGene(getRandom(rng)) -- two draws in order.
	r := mobRandom(p)
	p.pandaMainGene = pandaGeneGetRandom(r)
	p.pandaHiddenGene = pandaGeneGetRandom(r)
	pandaSetAttributes(p) // per-variant setBaseValue (WEAK -> MAX_HEALTH 10, LAZY -> speed 0.07)
	initSpawnHealth(p)    // setHealth(getMaxHealth()) AFTER setAttributes -> 10 (weak) or 20 (else)
	owner := t.regionForEntity(p)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(p)
	return p
}

// spawnPandaChild ports Panda.getBreedOffspring: create a baby PANDA, then child.setGeneFromParents(
// parentA, parentB); child.setAttributes(). Draws on the CHILD own stream in the vanilla order.
// initSpawnHealth runs after setAttributes. Exposed so the breed path wires the panda offspring the
// moment the per-type breed dispatch lands (breed() today spawns only pigs). Cite Panda.getBreedOffspring.
func (t *TickLoop) spawnPandaChild(parentA, parentB *Entity, x, y, z float64) *Entity {
	child := NewEntity(t.idAlloc.AllocID(), entity.Panda, x, y, z)
	child.isPanda = true
	child.breedAge = babyStartAge
	child.refreshDimensions()
	child.ai = newPandaAI()
	reseedMobAI(child.ai, child.id)
	pandaSetGeneFromParents(child, parentA, parentB, mobRandom(child))
	pandaSetAttributes(child)
	initSpawnHealth(child)
	owner := t.regionForEntity(child)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(child)
	return child
}

// pandaAiStep is the Panda per-tick extra (Panda.customServerAiStep). The roll/sneeze/sit/lie temperament
// state machine + eat-bamboo consume are DEFERRED, so this is a bounded no-op today (the passive goals
// drive the visible movement). Wired + per-type-gated so the cosmetics slot in later, never baked away.
// RNG-free. Cite Panda.customServerAiStep.
func (t *TickLoop) pandaAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: the roll/sneeze/sit/lie temperament state machine + eat-bamboo consume + baby-sneeze slime.
}
