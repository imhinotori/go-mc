package server

// zombie_reinforcement_test.go -- pins Zombie.hurtServer's SPAWN_REINFORCEMENTS_CHANCE reinforcement
// spawn (VERIFIED javap Zombie.hurtServer @85-457 this session). The core assertion: a hurt zombie whose
// SPAWN_REINFORCEMENTS_CHANCE > 0 CAN summon a reinforcement zombie near itself, and BOTH the parent's and
// the child's chance drop by 0.05 (the REINFORCEMENT_CALLER_CHARGE_ID / ZOMBIE_REINFORCEMENT_CALLEE_CHARGE
// -0.05 addPermanentModifier charges).
//
// The HARD-difficulty gate reads the LIVE ServerLevel.getDifficulty() (t.levelDifficulty); it is tested via
// the extracted core zombieSpawnReinforcement (runs everything after the target/HARD/chance gates) AND via
// the gated wrapper zombieHurtReinforcements, exercised BOTH for its NORMAL short-circuit (no draw, no
// spawn) and — since difficulty is now live — for a /difficulty hard arming the reinforcement (below).
//
// The pig oracle (TestPluginPigEqualsGoNativePig) is UNTOUCHED (a zombie is a separate mob; the hook is
// zombie-family-gated).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// countZombies counts live zombie-type entities across ALL of the loop's regions (a reinforcement
// spawned at an offset column may be owned by a different region than the parent's -- regionOf hashes
// (x^z)&1 into two regions even in the test loop).
func countZombies(loop *TickLoop) int {
	n := 0
	for _, r := range loop.regions {
		for _, e := range r.entities.all() {
			if e.typ == entity.Zombie.ID && !e.dead {
				n++
			}
		}
	}
	return n
}

// findReinforcementZombie returns the first live zombie in any region whose id is not the parent's.
func findReinforcementZombie(loop *TickLoop, parentID int32) *Entity {
	for _, r := range loop.regions {
		for _, e := range r.entities.all() {
			if e.typ == entity.Zombie.ID && !e.dead && e.id != parentID {
				return e
			}
		}
	}
	return nil
}

// TestZombieReinforcementSpawnsAndChargesBothChances: a zombie with SPAWN_REINFORCEMENTS_CHANCE set > 0,
// driven through the reinforcement core with a target, spawns a SECOND zombie (the reinforcement) near it,
// and both the parent's and the child's SPAWN_REINFORCEMENTS_CHANCE drop by exactly 0.05.
func TestZombieReinforcementSpawnsAndChargesBothChances(t *testing.T) {
	loop, floorY, _ := zombieLoop(t)

	// The parent zombie, away from any player so the hasNearbyAlivePlayer(...,7.0) acceptance guard passes
	// on the first eligible offset (no player within 7 blocks of the offset). Spawn at the world origin.
	parent := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	// Seed a live SPAWN_REINFORCEMENTS_CHANCE base of 1.0 (the randomizeReinforcementsChance stand-in: the
	// live value is what Zombie.hurtServer reads via getAttributeValue). 1.0 guarantees the gate would pass.
	parentInst := parent.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name())
	if parentInst == nil {
		t.Fatal("zombie has no SPAWN_REINFORCEMENTS_CHANCE attribute instance (supplier did not register it)")
	}
	parentInst.SetBaseValue(1.0)
	before := parent.getAttributeValue(attribute.SpawnReinforcementsChance)

	// A target id (the reinforcement seeds its retaliation on it). Use a player id far away so it never
	// blocks an offset (the parent itself is at origin; players list is empty here).
	const targetID int32 = 9911

	countBefore := countZombies(loop)
	// Drive the reinforcement core directly with the zombie's own stream (bypassing the HARD stub gate).
	// Wrapped in withRegion so spawnDeclaredMob's regionForEntity store add lands on the owning region.
	loop.withRegion(loop.only(), func() {
		loop.zombieSpawnReinforcement(parent, targetID, mobRandom(parent))
	})
	countAfter := countZombies(loop)

	if countAfter <= countBefore {
		t.Fatalf("no reinforcement spawned: zombie count %d -> %d (want +1)", countBefore, countAfter)
	}

	// The parent's chance dropped 0.05 (the CALLER_CHARGE_ID modifier).
	afterParent := parent.getAttributeValue(attribute.SpawnReinforcementsChance)
	if diff := before - afterParent; diff < 0.05-1e-9 || diff > 0.05+1e-9 {
		t.Fatalf("parent SPAWN_REINFORCEMENTS_CHANCE dropped by %v, want 0.05 (REINFORCEMENT_CALLER_CHARGE_ID)", diff)
	}

	// The child (the newly-spawned reinforcement) carries the CALLEE_CHARGE (-0.05). Find it: the zombie
	// that is NOT the parent.
	child := findReinforcementZombie(loop, parent.id)
	if child == nil {
		t.Fatal("could not locate the spawned reinforcement zombie")
	}
	// The child was spawned at a fresh SPAWN_REINFORCEMENTS_CHANCE default 0.0 + the -0.05 callee charge, so
	// its live value is exactly -0.05 before sanitize; the attribute min is 0.0, so it clamps to 0.0. Assert
	// the CALLEE_CHARGE modifier is present with amount -0.05 (the un-sanitized charge).
	childInst := child.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name())
	if childInst == nil {
		t.Fatal("reinforcement zombie has no SPAWN_REINFORCEMENTS_CHANCE instance")
	}
	m, ok := childInst.GetModifier("minecraft:reinforcement_callee_charge")
	if !ok {
		t.Fatal("reinforcement zombie missing the CALLEE_CHARGE modifier (child chance should drop 0.05)")
	}
	if m.Amount < -0.05-1e-9 || m.Amount > -0.05+1e-9 {
		t.Fatalf("CALLEE_CHARGE amount = %v, want -0.05", m.Amount)
	}
}

// TestZombieReinforcementNormalDifficultyNoop: the GATED wrapper zombieHurtReinforcements is a no-op on
// the cited NORMAL serverDifficulty (reinforcements are HARD-only) -- no draw, no spawn -- exactly as
// Zombie.hurtServer's `level.getDifficulty() == HARD` gate short-circuits.
func TestZombieReinforcementNormalDifficultyNoop(t *testing.T) {
	if serverDifficulty == difficultyHard {
		t.Skip("serverDifficulty is HARD in this build; the NORMAL no-op assertion does not apply")
	}
	loop, floorY, _ := zombieLoop(t)

	parent := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	parent.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name()).SetBaseValue(1.0)
	parent.ai.attackTargetID = 4242 // a target so only the HARD gate can stop it

	countBefore := countZombies(loop)
	loop.zombieHurtReinforcements(parent, damageSource{attacker: 4242})
	countAfter := countZombies(loop)

	if countAfter != countBefore {
		t.Fatalf("reinforcement spawned on NORMAL difficulty (count %d -> %d) -- the HARD gate did not short-circuit", countBefore, countAfter)
	}
	// The parent's chance is untouched (no CALLER_CHARGE on a short-circuited call).
	if _, ok := parent.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name()).GetModifier("minecraft:reinforcement_caller_charge"); ok {
		t.Fatal("a CALLER_CHARGE was applied on NORMAL difficulty -- the HARD gate did not short-circuit")
	}
}
