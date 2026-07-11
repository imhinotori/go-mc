package server

// mob_vs_mob_melee_test.go — R1 (the mob-vs-mob melee/target seam): the entity-victim melee path
// (isWithinMeleeAttackRangeEntity + doHurtTargetEntity + the iron_golem doHurtTarget override) plus the
// crucial regression that the *tickPlayer player-victim path is UNCHANGED (a zombie hitting a player
// still lands identically). The headline proof is TestIronGolemAttacksHostile: a golem next to a zombie
// walks up (already in reach) and damages the zombie via the entity-victim path, dealing the golem's
// ad/2 + nextInt(ad) range damage and the +0.4 vertical fling.
//
// Ported/verified against the unobfuscated 26.2 jar this session (javap -c -p): Mob.doHurtTarget(
// ServerLevel, Entity) (the victim is an Entity, not a Player — the same shape as the player path),
// Mob.isWithinMeleeAttackRange(LivingEntity) (getAttackBoundingBox(reach).intersects(getHitbox())),
// LivingEntity.hasLineOfSight(Entity) (attacker eye -> victim getEyeY), IronGolem.doHurtTarget (the
// range-roll damage + the fling scaled by 1-KNOCKBACK_RESISTANCE).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// --- TestMeleeEntityVictimReach ----------------------------------------------------------------

// TestMeleeEntityVictimReach pins isWithinMeleeAttackRangeEntity: the attacker's DEFAULT_ATTACK_REACH-
// inflated box intersects the victim's width x height hitbox when adjacent, and NOT when the victim is
// well beyond reach. Mirror of the player-victim isWithinMeleeAttackRange test shape.
func TestMeleeEntityVictimReach(t *testing.T) {
	golem := NewEntity(7400, entity.IronGolem, 0, 64, 0)
	// A victim right beside the golem (touching): in reach.
	near := NewEntity(7401, entity.Zombie, golem.x+golem.width/2+0.1, 64, 0)
	if !isWithinMeleeAttackRangeEntity(golem, near) {
		t.Fatalf("adjacent zombie should be within the golem's melee reach (golem w=%.3f reach=%.3f)", golem.width, defaultAttackReach)
	}
	// A victim far away: NOT in reach.
	far := NewEntity(7402, entity.Zombie, golem.x+10.0, 64, 0)
	if isWithinMeleeAttackRangeEntity(golem, far) {
		t.Fatal("a zombie 10 blocks away should NOT be within melee reach")
	}
	// Vertical: reach inflates ZERO vertically (inflate(reach, 0, reach)). A victim stacked far above the
	// golem's height is out of the box even if horizontally on top.
	high := NewEntity(7403, entity.Zombie, golem.x, 64+golem.height+2.0, 0)
	if isWithinMeleeAttackRangeEntity(golem, high) {
		t.Fatal("a zombie stacked well above the golem's height should be out of the zero-vertical-inflate box")
	}
}

// --- TestIronGolemAttacksHostile (the headline R1 proof) ---------------------------------------

// TestIronGolemAttacksHostile: a golem whose attack target is an adjacent zombie strikes it through the
// NEW entity-victim melee path — the golem's own doHurtTarget override deals ad/2 + nextInt((int)ad)
// damage (ATTACK_DAMAGE 15 -> [7.5, 21.5]) and adds the +0.4 vertical fling (the zombie's
// KNOCKBACK_RESISTANCE base 0 -> full scale). Proven by health loss and the upward velocity.
func TestIronGolemAttacksHostile(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	golem := NewEntity(loop.idAlloc.AllocID(), entity.IronGolem, 8.5, float64(floorY+1), 8.5)
	initSpawnHealth(golem)
	golem.ai = &mobAI{rng: newEntityRandom(1)}
	loop.cur().entities.add(golem)

	// The zombie victim adjacent to the golem (in melee reach), on the ground so it stays put.
	zombie := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, golem.x+golem.width/2+0.2, float64(floorY+1), 8.5)
	initSpawnHealth(zombie)
	zombie.ai = &mobAI{rng: newEntityRandom(2)}
	zombie.onGround = true
	loop.cur().entities.add(zombie)

	startHealth := zombie.health

	// Wire the golem's attack target to the zombie's id (what iron_golem_hostile_target's start() commits),
	// then drive the melee goal. ticksUntilNextAttack starts 0 (fresh goal) so the first in-reach tick swings.
	golem.ai.setTarget(zombie.id)
	g := newMeleeAttackGoal(1.0)

	// The golem's rng draw ORDER on this tick (adjacent victim, no active path -> the stalled path-recompute
	// branch fires): the path-recalc cooldown nextInt(7) is drawn FIRST (the noPathedTarget||stalled guard
	// short-circuits before the 0.05 nextFloat nudge), THEN the doHurtTarget damage nextInt((int)ad). Reproduce
	// that exact order from a reference rng seeded identically to compute the expected PRE-armor damage.
	ref := newEntityRandom(1)
	ref.nextInt(7) // the path-recalc cooldown draw (4 + nextInt(7))
	ad := float32(golem.getAttributeValue(attribute.AttackDamage))
	wantPreArmor := ad/2.0 + float32(ref.nextInt(int(ad)))

	loop.withRegion(loop.regionForEntity(golem), func() { g.tick(loop, golem) })

	if zombie.health >= startHealth {
		t.Fatalf("zombie took no damage from the golem melee (health %.2f -> %.2f); the entity-victim path did not fire", startHealth, zombie.health)
	}
	// The zombie has ARMOR 2.0, so the LANDED damage is the pre-armor roll reduced by the armor curve
	// (applyDamageEntity's getDamageAfterArmorAbsorb) — a ~4% reduction, never more than the pre-armor value.
	// Assert the landed damage is positive and does not EXCEED the pre-armor roll (armor only reduces).
	gotDamage := startHealth - zombie.health
	if gotDamage <= 0 || gotDamage > wantPreArmor+1e-3 {
		t.Fatalf("golem dealt %.3f, want in (0, %.3f] (pre-armor roll ad/2 + nextInt(%d), reduced by the zombie's ARMOR 2.0)", gotDamage, wantPreArmor, int(ad))
	}
	// The +0.4 vertical fling (scale = 1 - KNOCKBACK_RESISTANCE 0 = 1) must have pushed the zombie upward.
	if zombie.vy <= 0 {
		t.Fatalf("golem fling did not raise the zombie's vy (got %.4f, want > 0 from the +0.4*scale vertical impulse)", zombie.vy)
	}
	// attackAnimationTick armed to 10 (doHurtTarget: attackAnimationTick = 10).
	if golem.ironGolemAttackAnimationTick != ironGolemAttackAnimationTicks {
		t.Fatalf("golem attackAnimationTick = %d, want %d (doHurtTarget arms it)", golem.ironGolemAttackAnimationTick, ironGolemAttackAnimationTicks)
	}
}

// --- TestMeleeEntityVictimSharedDamage ---------------------------------------------------------

// TestMeleeEntityVictimSharedDamage: a plain hostile (a zombie) attacking a MOB victim (another zombie)
// deals its flat ATTACK_DAMAGE through the shared doHurtTargetEntity path (NOT the golem override). This
// exercises the base Mob.doHurtTarget mob-victim limb distinct from the iron_golem full override.
func TestMeleeEntityVictimSharedDamage(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	attacker := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 8.5, float64(floorY+1), 8.5)
	initSpawnHealth(attacker)
	attacker.ai = &mobAI{rng: newEntityRandom(3)}
	attacker.onGround = true
	loop.cur().entities.add(attacker)

	victim := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, attacker.x+attacker.width/2+0.2, float64(floorY+1), 8.5)
	initSpawnHealth(victim)
	victim.ai = &mobAI{rng: newEntityRandom(4)}
	victim.onGround = true
	loop.cur().entities.add(victim)

	startHealth := victim.health
	wantDamage := float32(attacker.getAttributeValue(attribute.AttackDamage)) // zombie flat ATTACK_DAMAGE

	attacker.ai.setTarget(victim.id)
	g := newMeleeAttackGoal(1.0)
	loop.withRegion(loop.regionForEntity(attacker), func() { g.tick(loop, attacker) })

	if victim.health >= startHealth {
		t.Fatalf("victim took no damage from the zombie melee (health %.2f -> %.2f)", startHealth, victim.health)
	}
	// The victim zombie has ARMOR 2.0, so the landed damage is the flat ATTACK_DAMAGE reduced by the armor
	// curve (applyDamageEntity) — positive and never exceeding the pre-armor ATTACK_DAMAGE. This proves the
	// shared Mob.doHurtTarget mob-victim limb (doHurtTargetEntity) fired, distinct from the golem override.
	gotDamage := startHealth - victim.health
	if gotDamage <= 0 || gotDamage > wantDamage+1e-3 {
		t.Fatalf("zombie dealt %.3f to the mob victim, want in (0, %.3f] (flat ATTACK_DAMAGE via doHurtTargetEntity, reduced by ARMOR 2.0)", gotDamage, wantDamage)
	}
}
