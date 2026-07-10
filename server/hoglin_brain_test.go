package server

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/level/block"
)

func TestHoglinPacifiedNearWarpedFungus(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)
	h := loop.spawnHoglin(8.5, by, 8.5, false, dimOverworld)
	p := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	p.health = 20.0
	h.ai.attackTargetID = p.entityID

	wf, ok := block.ToStateID[block.WarpedFungus{}]
	if !ok {
		t.Fatal("no warped_fungus state id")
	}
	if !loop.world().SetBlock(pk.Position{X: 8, Y: floorY, Z: 8}, wf, dimMinY) {
		t.Fatal("could not place warped_fungus")
	}

	loop.hoglinRepellentSensor(h)
	if !loop.hoglinIsPacified(h) {
		t.Fatalf("hoglin near warped_fungus not pacified (ticks=%d)", h.hoglinPacifiedTicks)
	}
	if h.hoglinPacifiedTicks != hoglinRepellentPacify {
		t.Fatalf("PACIFIED expiry = %d, want %d (REPELLENT_PACIFY_TIME)", h.hoglinPacifiedTicks, hoglinRepellentPacify)
	}

	loop.hoglinAcquireNearestPlayer(h)
	if h.ai.attackTargetID != 0 {
		t.Fatalf("pacified hoglin acquired a target %d, want none (findNearestValidAttackTarget gate)", h.ai.attackTargetID)
	}
}

func TestHoglinAdultRetaliatesOnHit(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)
	h := loop.spawnHoglin(8.5, by, 8.5, false, dimOverworld)
	h.hoglinPacifiedTicks = hoglinRepellentPacify
	p := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	p.health = 20.0

	loop.applyDamageEntity(h, damageSourcePlayerAttack(p.entityID), 2.0)

	if h.hoglinPacifiedTicks != 0 {
		t.Fatalf("wasHurtBy did not clear PACIFIED (ticks=%d)", h.hoglinPacifiedTicks)
	}
	if h.ai.attackTargetID != p.entityID {
		t.Fatalf("adult hoglin did not retaliate: target=%d, want attacker %d", h.ai.attackTargetID, p.entityID)
	}
}

func TestHoglinBabyRetreatsOnHit(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)
	h := loop.spawnHoglin(8.5, by, 8.5, true, dimOverworld)
	p := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	p.health = 20.0

	loop.applyDamageEntity(h, damageSourcePlayerAttack(p.entityID), 2.0)

	if h.hoglinAvoidTargetID != p.entityID {
		t.Fatalf("baby hoglin did not retreat: avoidTarget=%d, want attacker %d", h.hoglinAvoidTargetID, p.entityID)
	}
	if h.hoglinAvoidTicks < 100 || h.hoglinAvoidTicks > 400 {
		t.Fatalf("baby retreat duration = %d, want in [100,400] (RETREAT_DURATION)", h.hoglinAvoidTicks)
	}
	if h.ai.attackTargetID != 0 {
		t.Fatalf("retreating baby has an attack target %d, want none", h.ai.attackTargetID)
	}
}

func TestHoglinAvoidsWhenPiglinsOutnumber(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)
	h := loop.spawnHoglin(8.5, by, 8.5, false, dimOverworld)
	// Two adult piglins vs one hoglin: piglinCount(2) > hoglinCount(0)+1 -> outnumbered.
	loop.spawnPiglin(9.0, by, 8.5, false)
	loop.spawnPiglin(8.0, by, 9.0, false)

	if !loop.hoglinPiglinsOutnumber(h) {
		t.Fatal("piglinsOutnumberHoglins false with 2 piglins vs 1 hoglin, want true")
	}
	loop.hoglinPiglinAvoidCheck(h)
	if h.hoglinAvoidTargetID == 0 {
		t.Fatal("out-numbered hoglin did not set an avoid target")
	}
}

func TestHoglinMeleeIntervalAdultBaby(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)

	adult := loop.spawnHoglin(8.5, by, 8.5, false, dimOverworld)
	pa := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	pa.health = 20.0
	pa.playerEntity = &Entity{id: pa.entityID}
	adult.ai.attackTargetID = pa.entityID
	adult.meleeCooldown = 0
	loop.hoglinAiStep(adult)
	if adult.meleeCooldown != hoglinMeleeInterval {
		t.Fatalf("adult melee interval = %d, want %d (MeleeAttack.create(40))", adult.meleeCooldown, hoglinMeleeInterval)
	}

	baby := loop.spawnHoglin(20.5, by, 20.5, true, dimOverworld)
	pb := combatTestPlayer(loop, 21.1, by, 20.5, 6262)
	pb.health = 20.0
	pb.playerEntity = &Entity{id: pb.entityID}
	baby.ai.attackTargetID = pb.entityID
	baby.meleeCooldown = 0
	loop.hoglinAiStep(baby)
	if baby.meleeCooldown != hoglinBabyMeleeInterval {
		t.Fatalf("baby melee interval = %d, want %d (MeleeAttack.create(15))", baby.meleeCooldown, hoglinBabyMeleeInterval)
	}
}
