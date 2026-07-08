package server

// hoglin_test.go -- deterministic pins for the Hoglin (net.minecraft.world.entity.monster.hoglin.Hoglin,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 40 / ATTACK_DAMAGE 6 adult, 0.5 baby
// / MOVEMENT_SPEED 0.3 / KNOCKBACK_RESISTANCE 0.6 / ATTACK_KNOCKBACK 1.0), the knock-up toss on a landed
// melee hit (HoglinBase.hurtAndThrowTarget/throwTarget), the zoglin conversion after > 300 ticks outside
// the nether (isConverting/finishConversion), the NON-conversion in the nether, and the baby damage.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world"
)

// hoglinLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick hoglins.
func hoglinLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestHoglinSpawnDefaults: spawnHoglin builds an adult hoglin rendering as entity.Hoglin.ID with the jar
// attributes (MAX_HEALTH 40 -> health 40, ATTACK_DAMAGE 6.0, MOVEMENT_SPEED 0.3, KNOCKBACK_RESISTANCE 0.6,
// ATTACK_KNOCKBACK 1.0).
func TestHoglinSpawnDefaults(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	h := loop.spawnHoglin(8.5, float64(floorY+1), 8.5, false, dimOverworld)
	if h.typ != entity.Hoglin.ID {
		t.Fatalf("hoglin typ = %d, want entity.Hoglin.ID %d", h.typ, entity.Hoglin.ID)
	}
	if !h.isHoglin {
		t.Fatal("hoglin not marked isHoglin")
	}
	if math.Abs(float64(h.health)-40.0) > 1e-6 {
		t.Fatalf("hoglin health = %v, want 40.0 (MAX_HEALTH)", h.health)
	}
	if got := h.getAttributeValue(attribute.MaxHealth); math.Abs(got-40.0) > 1e-9 {
		t.Fatalf("hoglin MAX_HEALTH = %v, want 40.0", got)
	}
	if got := h.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("hoglin ATTACK_DAMAGE = %v, want 6.0 (adult)", got)
	}
	if got := h.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.30000001192092896) > 1e-12 {
		t.Fatalf("hoglin MOVEMENT_SPEED = %v, want 0.30000001192092896", got)
	}
	if got := h.getAttributeValue(attribute.KnockbackResistance); math.Abs(got-0.6000000238418579) > 1e-12 {
		t.Fatalf("hoglin KNOCKBACK_RESISTANCE = %v, want 0.6000000238418579", got)
	}
	if got := h.getAttributeValue(attribute.AttackKnockback); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("hoglin ATTACK_KNOCKBACK = %v, want 1.0", got)
	}
	if h.ai == nil || h.ai.rng == nil {
		t.Fatal("hoglin has no minimal AI / rng")
	}
}

// TestHoglinBabyDamage: a baby hoglin has ATTACK_DAMAGE 0.5 (Hoglin.ageBoundaryReached baby branch), the
// halved-scale dims, and is isBaby.
func TestHoglinBabyDamage(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	h := loop.spawnHoglin(8.5, float64(floorY+1), 8.5, true, dimOverworld)
	if !h.isBaby() {
		t.Fatal("baby hoglin not isBaby (breedAge >= 0)")
	}
	if got := h.getAttributeValue(attribute.AttackDamage); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("baby hoglin ATTACK_DAMAGE = %v, want 0.5 (ageBoundaryReached baby)", got)
	}
	// Baby dims are the adult dims scaled 0.5 (getDefaultDimensions BABY_DIMENSIONS).
	if math.Abs(h.width-h.adultWidth*0.5) > 1e-9 || math.Abs(h.height-h.adultHeight*0.5) > 1e-9 {
		t.Fatalf("baby hoglin dims = (%v,%v), want half the adult (%v,%v)", h.width, h.height, h.adultWidth, h.adultHeight)
	}
}

// TestHoglinKnockUpToss: an adult hoglin's melee hit FLINGS the player UPWARD (throwTarget's vertical
// component d17 = delta * nextFloat() * 0.5, added to the player velocity) and adds a horizontal impulse.
// delta = ATTACK_KNOCKBACK 1.0 - target KNOCKBACK_RESISTANCE 0.0 = 1.0 > 0, so the toss fires. We stand the
// player adjacent, seed a live playerEntity, zero the cooldown, and drive one melee.
func TestHoglinKnockUpToss(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)
	h := loop.spawnHoglin(8.5, by, 8.5, false, dimOverworld)
	p := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	p.health = 20.0
	p.playerEntity = &Entity{id: p.entityID} // the store Entity the push velocity + SetEntityMotion address
	h.ai.attackTargetID = p.entityID
	h.meleeCooldown = 0

	start := p.health
	loop.hoglinDoHurtTarget(h, p)

	// Damage landed (the adult roll f/2 + nextInt(f) is >= 3.0 for f=6).
	dealt := start - p.health
	if dealt <= 0 {
		t.Fatalf("hoglin melee dealt %v damage, want > 0", dealt)
	}
	// The knock-up: the player gained UPWARD velocity (d17 = delta * nextFloat() * 0.5 >= 0; with a live
	// rng it is > 0 unless nextFloat() drew exactly 0). The horizontal impulse is also applied.
	if p.playerEntity.vy <= 0 {
		t.Fatalf("hoglin toss vy = %v, want > 0 (the knock-up fling)", p.playerEntity.vy)
	}
	if p.playerEntity.vx == 0 && p.playerEntity.vz == 0 {
		t.Fatal("hoglin toss applied no horizontal impulse (throwTarget push missing)")
	}
	// The attack animation ticks were armed (Hoglin.doHurtTarget: attackAnimationRemainingTicks = 10).
	if h.hoglinAttackAnimTicks != 10 {
		t.Fatalf("hoglin attackAnimationRemainingTicks = %d, want 10", h.hoglinAttackAnimTicks)
	}
}

// TestHoglinBabyNoToss: a baby hoglin does NOT throw the target (hurtAndThrowTarget: if(flag &&
// !attacker.isBaby()) throwTarget) -- a baby hits but never flings.
func TestHoglinBabyNoToss(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	by := float64(floorY + 1)
	h := loop.spawnHoglin(8.5, by, 8.5, true, dimOverworld)
	p := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	p.health = 20.0
	p.playerEntity = &Entity{id: p.entityID}
	h.ai.attackTargetID = p.entityID
	h.meleeCooldown = 0

	loop.hoglinDoHurtTarget(h, p)
	// A baby still deals the base melee knockback (applyDamage's dealDefaultKnockbackPlayer recoil), but it
	// must NOT add the throwTarget UPWARD fling -- so vy stays 0 (throwTarget is gated on !isBaby()).
	if p.playerEntity.vy != 0 {
		t.Fatalf("baby hoglin applied an upward toss: vy=%v, want 0 (throwTarget is adult-only)", p.playerEntity.vy)
	}
}

// TestHoglinConvertsToZoglin: an adult hoglin OUTSIDE the nether (dimOverworld) converts to a Zoglin after
// timeInOverworld EXCEEDS 300 (Hoglin.customServerAiStep: ++timeInOverworld; convert when > 300). We drive
// hoglinConversionTick 301 times and assert the type flipped to Zoglin.
func TestHoglinConvertsToZoglin(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	h := loop.spawnHoglin(8.5, float64(floorY+1), 8.5, false, dimOverworld)

	// 300 ticks: still a hoglin (timeInOverworld reaches 300, the > 300 gate not yet crossed).
	for i := 0; i < 300; i++ {
		loop.hoglinConversionTick(h)
	}
	if h.typ != entity.Hoglin.ID {
		t.Fatalf("hoglin converted too early at t=300 (typ=%d)", h.typ)
	}
	if h.hoglinTimeInOverworld != 300 {
		t.Fatalf("hoglin timeInOverworld = %d after 300 ticks, want 300", h.hoglinTimeInOverworld)
	}
	// The 301st tick crosses > 300 -> convert to Zoglin.
	loop.hoglinConversionTick(h)
	if h.typ != entity.Zoglin.ID {
		t.Fatalf("hoglin did NOT convert to Zoglin after > 300 ticks (typ=%d, want %d)", h.typ, entity.Zoglin.ID)
	}
	if !h.isZoglin || h.isHoglin {
		t.Fatalf("post-conversion flags wrong: isZoglin=%v isHoglin=%v", h.isZoglin, h.isHoglin)
	}
}

// TestHoglinNoConvertInNether: a hoglin in the nether (dimNether) NEVER converts (isConverting is false:
// PIGLINS_ZOMBIFY is off in the nether), and timeInOverworld stays 0.
func TestHoglinNoConvertInNether(t *testing.T) {
	loop, _, floorY := hoglinLoop(t)
	h := loop.spawnHoglin(8.5, float64(floorY+1), 8.5, false, dimNether)
	for i := 0; i < 400; i++ {
		loop.hoglinConversionTick(h)
	}
	if h.typ != entity.Hoglin.ID {
		t.Fatalf("hoglin in the nether converted (typ=%d), want it to stay a Hoglin", h.typ)
	}
	if h.hoglinTimeInOverworld != 0 {
		t.Fatalf("hoglin in the nether timeInOverworld = %d, want 0 (never counting)", h.hoglinTimeInOverworld)
	}
}
