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
// v1 STUBS (cited): the temperament COSMETIC goals (Sit/LieOnBack/Sneeze/Roll) + PandaAvoidGoal are
// DEFERRED; this port supplies the bounded passive walk (Float/Panic/Breed/Tempt(PANDA_FOOD=bamboo)/
// Follow/Stroll/Look), the FULL, EXACT gene/variant system, AND the fight-back character layer
// (PandaAttackGoal @3 + PandaHurtByTargetGoal targetSelector @1 with setAlertOthers): an AGGRESSIVE-gene
// panda retaliates + melees whoever hurt it and alerts nearby aggressive pandas. The observable
// attributes, the gene->variant mapping, and the gene-roll RNG draw ORDER are EXACT. The canPerformAction
// / gotBamboo / didBite state the fight-back goals read is the deferred cosmetic layer (all-false in v1,
// cited constants that become real reads when the temperament/eat-bamboo state machine lands).

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
	// @3 PandaAttackGoal(this, 1.2, true) [MOVE] -- the "aggressive panda punches back" melee. Gated on
	// canPerformAction (cited constant-true in v1); pursues + swings the hurt-by target. Cite
	// Panda.registerGoals @3 PandaAttackGoal.
	m.goals.addGoal(3, newPandaAttackGoal())
	m.goals.addGoal(4, newTemptGoal(pandaTemptSpeed, func(id int32) bool { return itemInTag(id, pandaFoodTag) }, false, nil))
	m.goals.addGoal(9, newLookAtPlayerGoal(pandaLookDistance))
	m.goals.addGoal(10, newRandomLookAroundGoal())
	m.goals.addGoal(13, newFollowParentGoal(pandaFollowSpeed))
	m.goals.addGoal(14, newWaterAvoidingRandomStrollGoal(pandaStrollSpeed))
	// targetSelector @1 PandaHurtByTargetGoal.setAlertOthers() [TARGET] -- any hurt panda retaliates
	// against its attacker + alerts nearby AGGRESSIVE pandas. Cite Panda.registerGoals targetSelector @1.
	m.targetSelector.addGoal(1, newPandaHurtByTargetGoal())
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

// --- Panda character layer: PandaAttackGoal @3 + PandaHurtByTargetGoal targetSelector @1 -----------
//
// The "aggressive panda punches back" behavior. AGGRESSIVE-gene pandas fight instead of flee: the
// PandaHurtByTargetGoal (targetSelector @1) makes ANY panda retaliate against whoever hit it (the gene
// does not gate the hurt-by goal; a hit panda always sets its attacker as a target), and PandaAttackGoal
// (@3) pursues + melees that target while the panda canPerformAction(). The observable divergence for an
// AGGRESSIVE panda is that its passive-goal PandaAvoidGoal (flee) is superseded by the fight, because
// PandaAvoidGoal.canUse gates on !isScared() and the aggressive panda does not enter the scared state.
//
// The temperament STATE (isOnBack/isScared/isEating/isRolling/isSitting) that canPerformAction reads is
// the DEFERRED cosmetic layer (pandaAiStep no-op) — in v1 every one of those flags is false, so
// pandaCanPerformAction returns true and the attack goal is never blocked by an unbuilt state. This is a
// cited constant (all-false state), structured so canPerformAction becomes a real read when the
// temperament state machine lands. Cite Panda.canPerformAction + Panda$PandaAttackGoal + Panda$Panda
// HurtByTargetGoal + HurtByTargetGoal.setAlertOthers.

// pandaIsAggressive ports Panda.isAggressive(): getVariant() == Gene.AGGRESSIVE. Cite Panda.isAggressive.
func pandaIsAggressive(e *Entity) bool { return pandaGetVariant(e) == pandaGeneAggressive }

// pandaCanPerformAction ports Panda.canPerformAction(): !isOnBack() && !isScared() && !isEating() &&
// !isRolling() && !isSitting(). The five temperament states are the DEFERRED cosmetic layer (all false in
// v1 — the state machine is not yet built), so this is a cited constant-true, structured so it becomes a
// real conjunction when the sit/lie/roll/sneeze/eat state machine lands. NO RNG.
//
//	[VERIFIED javap Panda.canPerformAction: isOnBack ifne 0; isScared ifne 0; isEating ifne 0;
//	 isRolling ifne 0; isSitting ifne 0; iconst_1.]
func pandaCanPerformAction(e *Entity) bool {
	// isOnBack / isScared / isEating / isRolling / isSitting are all false in v1 (the temperament state
	// machine is the deferred cosmetic layer, pandaAiStep). A cited constant-false for each state -> the
	// conjunction is true. When the state machine lands, replace these with the real flag reads.
	return true
}

// pandaAttackGoal ports net.minecraft.world.entity.animal.panda.Panda$PandaAttackGoal (extends
// MeleeAttackGoal). canUse == panda.canPerformAction() && super.canUse(); every other method is the base
// MeleeAttackGoal (the panda inherits its melee chase/swing verbatim). Cite Panda.registerGoals @3
// PandaAttackGoal(this, 1.2000000476837158, true) + Panda$PandaAttackGoal.canUse.
type pandaAttackGoal struct {
	meleeAttackGoal
}

// pandaAttackSpeed is the ldc2_w 1.2000000476837158d speedModifier Panda.registerGoals @3 passes to
// PandaAttackGoal(this, 1.2000000476837158, true). Cite Panda.registerGoals @3.
const pandaAttackSpeed = 1.2000000476837158

// newPandaAttackGoal builds Panda$PandaAttackGoal(panda, 1.2, true). The boolean (followingTargetEven
// IfNotSeen=true) is a cited no-op in v1 (no LoS/sensing), matching the base MeleeAttackGoal handling.
func newPandaAttackGoal() *pandaAttackGoal {
	return &pandaAttackGoal{meleeAttackGoal: *newMeleeAttackGoal(pandaAttackSpeed)}
}

// canUse ports Panda$PandaAttackGoal.canUse: panda.canPerformAction() && super.canUse(). A panda that is
// on its back / scared / eating / rolling / sitting does not attack; else it defers to MeleeAttackGoal.
// canUse (the gameTime-cooldown + live-target gate). NO RNG.
//
//	[VERIFIED javap Panda$PandaAttackGoal.canUse: panda.canPerformAction ifeq 0; MeleeAttackGoal.canUse
//	 ifeq 0; iconst_1.]
func (g *pandaAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	if !pandaCanPerformAction(e) {
		return false
	}
	return g.meleeAttackGoal.canUse(t, e)
}

// pandaHurtByTargetGoal ports net.minecraft.world.entity.animal.panda.Panda$PandaHurtByTargetGoal
// (extends HurtByTargetGoal, with setAlertOthers([]) applied at registration -> alertSameType=true). It
// retaliates against whoever last hit the panda (the base hurt-by), and additionally:
//   - canContinueToUse: if panda.gotBamboo || panda.didBite { setTarget(null); return false } else super.
//     (a panda that just took a bamboo bribe / bit stops retaliating). gotBamboo/didBite are the DEFERRED
//     eat-bamboo state (pandaAiStep) — both false in v1, so the guard is a cited constant-false (never
//     drops the target early), structured to become a real read when eat-bamboo lands.
//   - alertOther: on alerting a nearby same-type panda, only propagate the target if that panda is
//     isAggressive() (Mob.isAggressive). The base alertOthers scan (getFollowDistance-inflated AABB) sets
//     the SAME attacker as the target on each alerted panda that passes alertOther. Cite Panda$PandaHurt
//     ByTargetGoal.canContinueToUse + alertOther + HurtByTargetGoal.setAlertOthers/alertOthers.
type pandaHurtByTargetGoal struct {
	hurtByTargetGoal
}

// newPandaHurtByTargetGoal builds Panda$PandaHurtByTargetGoal(panda) then .setAlertOthers() (empty
// varargs) -> alertSameType=true. Cite Panda.registerGoals targetSelector @1.
func newPandaHurtByTargetGoal() *pandaHurtByTargetGoal {
	g := &pandaHurtByTargetGoal{hurtByTargetGoal: *newHurtByTargetGoal()}
	// setAlertOthers([]) sets alertSameType = true (HurtByTargetGoal.setAlertOthers). start() then runs
	// alertOthers() to propagate the target to nearby same-type pandas via alertOther (below).
	g.alertSameType = true
	g.alertOther = pandaAlertOther
	return g
}

// canContinueToUse ports Panda$PandaHurtByTargetGoal.canContinueToUse: if (gotBamboo || didBite) {
// panda.setTarget(null); return false; } return super.canContinueToUse(). gotBamboo/didBite are the
// DEFERRED eat-bamboo state (both false in v1 -> the guard never drops the target early), structured to
// become a real read when the eat-bamboo state machine lands. NO RNG.
//
//	[VERIFIED javap Panda$PandaHurtByTargetGoal.canContinueToUse: gotBamboo ifne 20; didBite ifeq 30;
//	 setTarget(null); iconst_0 ireturn; HurtByTargetGoal.canContinueToUse.]
func (g *pandaHurtByTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	// gotBamboo || didBite: both cited constant-false in v1 (the eat-bamboo state is deferred). When the
	// state lands, read the real flags here; today the guard never fires.
	if pandaGotBamboo(e) || pandaDidBite(e) {
		if e.ai != nil {
			e.ai.setTarget(0) // panda.setTarget(null)
		}
		return false
	}
	return g.hurtByTargetGoal.canContinueToUse(t, e)
}

// pandaGotBamboo / pandaDidBite are the DEFERRED eat-bamboo state (Panda.gotBamboo / Panda.didBite). Both
// are false in v1 (the eat-bamboo consume state machine is the deferred cosmetic layer, pandaAiStep) — a
// cited constant-false apiece, structured to become a real field read when eat-bamboo lands. Cite
// Panda.gotBamboo / Panda.didBite.
func pandaGotBamboo(e *Entity) bool { return false }
func pandaDidBite(e *Entity) bool   { return false }

// pandaAlertOther ports Panda$PandaHurtByTargetGoal.alertOther(Mob, LivingEntity): if (mob instanceof
// Panda && mob.isAggressive()) mob.setTarget(target). Only an AGGRESSIVE-gene neighbor panda joins the
// retaliation; a non-aggressive panda is alerted-but-not-armed. The `mob instanceof Panda` check is the
// same-type gate the base alertOthers already applies (it scans getClass() == panda), so here it is the
// isAggressive() filter that matters. Cite Panda$PandaHurtByTargetGoal.alertOther.
//
//	[VERIFIED javap Panda$PandaHurtByTargetGoal.alertOther: aload_1 instanceof Panda ifeq 19; aload_1
//	 isAggressive ifeq 19; aload_1 aload_2 setTarget.]
func pandaAlertOther(t *TickLoop, other *Entity, targetID int32) {
	if other.typ != entity.Panda.ID {
		return // mob instanceof Panda
	}
	if !pandaIsAggressive(other) {
		return // mob.isAggressive() (Panda.isAggressive == variant AGGRESSIVE)
	}
	if other.ai != nil {
		other.ai.setTarget(targetID) // mob.setTarget(target)
	}
}

var (
	_ Goal = (*pandaAttackGoal)(nil)
	_ Goal = (*pandaHurtByTargetGoal)(nil)
)
