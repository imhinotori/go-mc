package server

// panda_behavior_test.go -- the Panda CHARACTER-LAYER pins (MOB-NEUT/panda fight-back): the
// PandaHurtByTargetGoal (targetSelector @1, setAlertOthers) that makes a hurt panda retaliate against its
// attacker + alert nearby AGGRESSIVE pandas, and the PandaAttackGoal (@3) canPerformAction gate. A 1:1
// port of net.minecraft.world.entity.animal.panda.Panda$PandaHurtByTargetGoal / $PandaAttackGoal (javap
// this session). The pig oracle is untouched (panda-only goals; a passive pig declares neither).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// newTestPanda builds a live panda with a minimal seeded AI (like newTestCat) and the given visible
// variant forced via the gene fields. attributes are seeded so getAttributeValue(FOLLOW_RANGE) resolves
// for the alertOthers scan. Added to the loop's only region so the near() broad-phase finds it.
func newTestPanda(loop *TickLoop, id int32, x, y, z float64, mainGene, hiddenGene int) *Entity {
	p := NewEntity(id, entity.Panda, x, y, z)
	p.isPanda = true
	p.pandaMainGene = mainGene
	p.pandaHiddenGene = hiddenGene
	p.ai = newPandaAI()
	reseedMobAI(p.ai, p.id)
	pandaSetAttributes(p)
	initSpawnHealth(p)
	loop.only().entities.add(p)
	return p
}

// TestPandaAggressiveIsAggressive pins Panda.isAggressive() == (visible variant == AGGRESSIVE): a
// main==AGGRESSIVE panda is aggressive; a NORMAL / masked-recessive panda is not.
func TestPandaAggressiveIsAggressive(t *testing.T) {
	loop, _ := newFluidLoop()
	aggro := newTestPanda(loop, 8100, 8.5, 64, 8.5, pandaGeneAggressive, pandaGeneNormal)
	if !pandaIsAggressive(aggro) {
		t.Fatal("an AGGRESSIVE-main panda must be isAggressive()")
	}
	norm := newTestPanda(loop, 8101, 9.5, 64, 8.5, pandaGeneNormal, pandaGeneAggressive)
	if pandaIsAggressive(norm) {
		t.Fatal("a NORMAL-main panda (hidden AGGRESSIVE) must NOT be isAggressive() (dominant NORMAL shows)")
	}
}

// TestPandaHurtByTargetGoalRetaliates: after a panda is hurt by a player (lastHurtByMob set), the
// PandaHurtByTargetGoal canUse fires and start() commits the attacker as the panda's attack target
// (mobAI.getTarget). Then a fresh hit with the SAME timestamp does not re-fire (timestamp gate). This is
// the observable "a hurt panda punches back" — the fight target is set.
func TestPandaHurtByTargetGoalRetaliates(t *testing.T) {
	loop, _ := newFluidLoop()
	panda := newTestPanda(loop, 8200, 8.5, 64, 8.5, pandaGeneAggressive, pandaGeneAggressive)
	attacker := &tickPlayer{entityID: 77, x: 9.5, y: 64, z: 8.5, health: maxHealth}
	loop.players = append(loop.players, attacker)

	g := newPandaHurtByTargetGoal()

	// No attacker recorded yet -> canUse false.
	if g.canUse(loop, panda) {
		t.Fatal("panda hurt-by canUse true with no attacker, want false")
	}
	// Record the player as the last attacker (the combat_mob.go store-point analogue).
	panda.lastHurtByMob = attacker.entityID
	panda.lastHurtByMobTimestamp = 100
	if !g.canUse(loop, panda) {
		t.Fatal("panda hurt-by canUse false after being hit by a live player, want true")
	}
	g.start(loop, panda)
	if panda.ai.getTarget() != attacker.entityID {
		t.Fatalf("panda target after retaliation = %d, want the attacker %d", panda.ai.getTarget(), attacker.entityID)
	}
	// Same-timestamp re-check: canUse must NOT re-fire on the already-retaliated hit (timestamp gate).
	if g.canUse(loop, panda) {
		t.Fatal("panda hurt-by canUse re-fired on the SAME hit timestamp, want false (start stamped it)")
	}
	// A fresh hit (new timestamp) re-arms retaliation.
	panda.lastHurtByMobTimestamp = 140
	if !g.canUse(loop, panda) {
		t.Fatal("panda hurt-by canUse false on a FRESH hit (new timestamp), want true")
	}
}

// TestPandaHurtByAlertsAggressiveNeighbor: setAlertOthers makes start() propagate the attacker to a
// nearby AGGRESSIVE-gene panda (via alertOther), but NOT to a non-aggressive one. Cite
// Panda$PandaHurtByTargetGoal.alertOther (mob instanceof Panda && mob.isAggressive()).
func TestPandaHurtByAlertsAggressiveNeighbor(t *testing.T) {
	loop, _ := newFluidLoop()
	hit := newTestPanda(loop, 8300, 8.5, 64, 8.5, pandaGeneAggressive, pandaGeneAggressive)
	// An AGGRESSIVE neighbor within FOLLOW_RANGE -> gets armed. A NORMAL neighbor -> not armed.
	aggroNbr := newTestPanda(loop, 8301, 10.5, 64, 8.5, pandaGeneAggressive, pandaGeneNormal)
	calmNbr := newTestPanda(loop, 8302, 11.5, 64, 8.5, pandaGeneNormal, pandaGeneNormal)
	attacker := &tickPlayer{entityID: 88, x: 6.5, y: 64, z: 8.5, health: maxHealth}
	loop.players = append(loop.players, attacker)

	hit.lastHurtByMob = attacker.entityID
	hit.lastHurtByMobTimestamp = 200

	g := newPandaHurtByTargetGoal()
	if !g.canUse(loop, hit) {
		t.Fatal("precondition: the hit panda's hurt-by canUse must be true")
	}
	g.start(loop, hit)

	if aggroNbr.ai.getTarget() != attacker.entityID {
		t.Fatalf("aggressive neighbor target = %d, want the attacker %d (alertOther armed it)", aggroNbr.ai.getTarget(), attacker.entityID)
	}
	if calmNbr.ai.getTarget() != 0 {
		t.Fatalf("non-aggressive neighbor target = %d, want 0 (alertOther must NOT arm a non-aggressive panda)", calmNbr.ai.getTarget())
	}
}

// TestPandaAttackGoalCanUseGate: PandaAttackGoal.canUse == canPerformAction() && MeleeAttackGoal.canUse.
// With canPerformAction cited-true in v1 and a live attack target set, the melee canUse (gameTime + live
// player target) governs. No target -> false.
func TestPandaAttackGoalCanUseGate(t *testing.T) {
	loop, _ := newFluidLoop()
	loop.gametime = 1000 // past the melee 20-tick lastCanUseCheck window (lastCanUseCheck starts 0)
	panda := newTestPanda(loop, 8400, 8.5, 64, 8.5, pandaGeneAggressive, pandaGeneAggressive)
	target := &tickPlayer{entityID: 99, x: 9.0, y: 64, z: 8.5, health: maxHealth}
	loop.players = append(loop.players, target)

	g := newPandaAttackGoal()
	// No target set -> melee canUse false (getTarget == 0). (This also stamps lastCanUseCheck = gametime.)
	if g.canUse(loop, panda) {
		t.Fatal("panda attack canUse true with no attack target, want false")
	}
	// Set the attack target (what the hurt-by goal's start does). Advance gametime past the melee 20-tick
	// COOLDOWN_BETWEEN_CAN_USE_CHECKS window (the first canUse above stamped lastCanUseCheck) so the
	// re-check gate does not suppress this evaluation. Cite MeleeAttackGoal.canUse (20L re-check gate).
	loop.gametime += meleeCooldownBetweenCanUseChecks
	panda.ai.setTarget(target.entityID)
	if !g.canUse(loop, panda) {
		t.Fatal("panda attack canUse false with a live player target and canPerformAction true, want true")
	}
	// The attack speed modifier is the jar's 1.2 (the ctor arg), not the declared walk speed.
	if g.speedModifier != pandaAttackSpeed {
		t.Fatalf("panda attack speedModifier = %v, want %v (Panda.registerGoals @3 1.2)", g.speedModifier, pandaAttackSpeed)
	}
	// The attack goal claims the MOVE flag (MeleeAttackGoal setFlags MOVE).
	if g.flags() != flagMove {
		t.Fatalf("panda attack flags = %d, want MOVE (%d)", g.flags(), flagMove)
	}
}
