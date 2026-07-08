package server

// wither_skeleton_test.go -- deterministic pins for the WitherSkeleton (net.minecraft.world.entity.monster
// .skeleton.WitherSkeleton, 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 20 /
// ATTACK_DAMAGE 4 / MOVEMENT_SPEED 0.25), the STONE_SWORD in MAINHAND, the WITHER 200 applied on a landed
// melee hit (WitherSkeleton.doHurtTarget), and the fire immunity (a nether skeleton).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world"
)

// witherSkeletonLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick wither skeletons.
func witherSkeletonLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestWitherSkeletonSpawnDefaults: spawnWitherSkeleton builds a wither skeleton rendering as
// entity.WitherSkeleton.ID with the jar attributes (MAX_HEALTH 20 -> health 20, ATTACK_DAMAGE 4.0 [the
// finalizeSpawn override, NOT the createMonsterAttributes 2.0], MOVEMENT_SPEED 0.25) holding a STONE_SWORD.
func TestWitherSkeletonSpawnDefaults(t *testing.T) {
	loop, _, floorY := witherSkeletonLoop(t)
	w := loop.spawnWitherSkeleton(8.5, float64(floorY+1), 8.5)
	if w.typ != entity.WitherSkeleton.ID {
		t.Fatalf("wither skeleton typ = %d, want entity.WitherSkeleton.ID %d", w.typ, entity.WitherSkeleton.ID)
	}
	if !w.isWitherSkeleton {
		t.Fatal("wither skeleton not marked isWitherSkeleton")
	}
	if math.Abs(float64(w.health)-20.0) > 1e-6 {
		t.Fatalf("wither skeleton health = %v, want 20.0 (MAX_HEALTH)", w.health)
	}
	if got := w.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("wither skeleton MAX_HEALTH = %v, want 20.0", got)
	}
	if got := w.getAttributeValue(attribute.AttackDamage); math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("wither skeleton ATTACK_DAMAGE = %v, want 4.0 (finalizeSpawn override)", got)
	}
	if got := w.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.25) > 1e-12 {
		t.Fatalf("wither skeleton MOVEMENT_SPEED = %v, want 0.25", got)
	}
	// STONE_SWORD in MAINHAND (populateDefaultEquipmentSlots).
	main := w.getMainHandItem()
	if main.Count <= 0 || uint32(main.ItemID) != uint32(item.StoneSword.ID) {
		t.Fatalf("wither skeleton MAINHAND = %+v, want STONE_SWORD (id %d)", main, item.StoneSword.ID)
	}
	if w.ai == nil || w.ai.rng == nil {
		t.Fatal("wither skeleton has no minimal AI / rng")
	}
}

// TestWitherSkeletonAppliesWither: a melee hit in reach (and with the cooldown ready) deals 4.0 damage AND
// applies WITHER 200 (amp 0) to the player (WitherSkeleton.doHurtTarget: super.doHurtTarget && instanceof
// LivingEntity -> addEffect(WITHER, 200)). We stand the player adjacent to the wither skeleton, zero the
// swing cooldown, and drive one melee.
func TestWitherSkeletonAppliesWither(t *testing.T) {
	loop, _, floorY := witherSkeletonLoop(t)
	by := float64(floorY + 1)
	w := loop.spawnWitherSkeleton(8.5, by, 8.5)
	// Player adjacent (within DEFAULT_ATTACK_REACH), clear LoS at the same height.
	p := combatTestPlayer(loop, 9.1, by, 8.5, 7777)
	p.health = 20.0
	w.ai.attackTargetID = p.entityID
	w.meleeCooldown = 0 // isTimeToAttack()

	start := p.health
	loop.witherSkeletonMeleeAttack(w, p)

	dealt := start - p.health
	if math.Abs(float64(dealt)-4.0) > 1e-6 {
		t.Fatalf("wither skeleton melee dealt %v damage, want 4.0 (ATTACK_DAMAGE)", dealt)
	}
	if !playerHasEffect(p, effectWither) {
		t.Fatal("wither skeleton melee did NOT apply WITHER (doHurtTarget addEffect missing)")
	}
	e := p.activeEffects[effectWither]
	if e == nil {
		t.Fatal("WITHER active effect absent after melee")
	}
	if e.duration != 200 {
		t.Fatalf("WITHER duration = %d, want 200 (new MobEffectInstance(WITHER, 200))", e.duration)
	}
	if e.amplifier != 0 {
		t.Fatalf("WITHER amplifier = %d, want 0 (WITHER I)", e.amplifier)
	}
}

// TestWitherSkeletonNoWitherOutOfReach: a target out of melee reach takes NO damage and gets NO WITHER (the
// isWithinMeleeAttackRange gate fails, so doHurtTarget never runs).
func TestWitherSkeletonNoWitherOutOfReach(t *testing.T) {
	loop, _, floorY := witherSkeletonLoop(t)
	by := float64(floorY + 1)
	w := loop.spawnWitherSkeleton(8.5, by, 8.5)
	p := combatTestPlayer(loop, 14.5, by, 8.5, 7777) // ~6 blocks away, out of melee reach
	p.health = 20.0
	w.ai.attackTargetID = p.entityID
	w.meleeCooldown = 0

	loop.witherSkeletonMeleeAttack(w, p)
	if math.Abs(float64(p.health)-20.0) > 1e-6 {
		t.Fatalf("out-of-reach player took damage: health = %v, want 20.0", p.health)
	}
	if playerHasEffect(p, effectWither) {
		t.Fatal("out-of-reach player got WITHER (should not -- not in melee range)")
	}
}

// TestWitherSkeletonFireImmune: the wither skeleton EntityType is fireImmune (a nether skeleton), so
// entityFireImmune reports true and it takes no fire/lava damage.
func TestWitherSkeletonFireImmune(t *testing.T) {
	loop, _, floorY := witherSkeletonLoop(t)
	w := loop.spawnWitherSkeleton(8.5, float64(floorY+1), 8.5)
	if !entityFireImmune(w) {
		t.Fatal("wither skeleton not fire-immune (entityFireImmune false)")
	}
}
