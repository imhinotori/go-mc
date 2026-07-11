package server

// gaps_r2_test.go — R2 mob-vs-mob / projectile gap tests: five focused pins for the wired gaps of this
// session. Each drives the REAL Go-native goal (or the boot-loaded declared mob) and asserts the observable
// behavior against the 26.2 jar port. The pig oracle (TestPluginPigEqualsGoNativePig) is a SEPARATE mob and
// is untouched by any of these (they build wolves/ocelots/shulkers/llamas/armadillos only).
//
//   - TestWolfOwnerAttackTargetsMob — GAP 1: a tamed wolf whose OWNER attacks a mob (owner.lastHurtMob set)
//     targets + melees that mob via the entity-victim path (OwnerHurtTargetGoal + MeleeAttackGoal.tickEntityVictim).
//   - TestOcelotHuntsChicken       — GAP 2: an ocelot with a chicken in reach acquires + attacks it, and the
//     chicken takes damage via the entity-victim OcelotAttackGoal path.
//   - TestShulkerHurtReactionWired — GAP 3: a low-HP shulker whose health drops between ticks teleports via
//     the shulkerAiStep-driven shulkerHurtServerReaction; a CLOSED shulker is arrow-immune.
//   - TestLlamaSpitsAtWolf         — GAP 4: a llama with a wolf target fires a LlamaSpit toward it (a
//     LlamaSpit projectile is spawned, aimed at the wolf).
//   - TestArmadilloFearsZombie     — GAP 5: an armadillo with a zombie (UNDEAD) nearby rolls up (IDLE ->
//     ROLLING -> SCARED) via the extended isScaredBy undead scan.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// --- GAP 1: WOLF owner-attack targets + melees a mob --------------------------------------------

// TestWolfOwnerAttackTargetsMob: the LIVE owner-ATTACK side (OwnerHurtTargetGoal). A tamed wolf whose owner
// has just attacked a mob (the player's lastHurtMob, set in attack_dispatch.go handleMobAttack) targets that
// mob, then its MeleeAttackGoal strikes it through the entity-victim path (tickEntityVictim ->
// doHurtTargetEntity). Proves the wolf's owner-defense-on-attack chain end-to-end. Cite Wolf.registerGoals
// targetSelector @2 OwnerHurtTargetGoal + @5 MeleeAttackGoal + LivingEntity.setLastHurtMob.
func TestWolfOwnerAttackTargetsMob(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)

	// A tamed wolf owned by a player. setTame + owner wiring mirrors the taming test's end state.
	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	owner := addTestPlayer(loop, 43001, 8.5, float64(floorY+1), 8.5)
	wolf.tame = true
	wolf.ownerUUID = owner.entityID
	wolf.orderedToSit = false

	// A zombie the OWNER is attacking, adjacent to the wolf (in melee reach) so the melee lands this tick.
	zombie := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, wolf.x+wolf.width/2+0.2, float64(floorY+1), 8.5)
	initSpawnHealth(zombie)
	zombie.ai = &mobAI{rng: newEntityRandom(2)}
	zombie.onGround = true
	loop.only().entities.add(zombie)

	// The player attacked the zombie: setLastHurtMob(zombie) — the OWNER-side attack bookkeeping the
	// OwnerHurtTargetGoal reads (owner.getLastHurtMob / getLastHurtMobTimestamp).
	owner.lastHurtMob = zombie.id
	owner.lastHurtMobTimestamp = int32(loop.gametime) + 1 // != the goal's initial timestamp (0)

	// (1) OwnerHurtTargetGoal acquires the mob the owner attacked and commits it as the wolf's target.
	g := newOwnerHurtTargetGoal()
	loop.withRegion(loop.regionForEntity(wolf), func() {
		if !g.canUse(loop, wolf) {
			t.Fatal("OwnerHurtTargetGoal.canUse did not fire for a tamed wolf whose owner attacked a mob")
		}
		g.start(loop, wolf)
	})
	if wolf.ai.getTarget() != zombie.id {
		t.Fatalf("wolf target = %d, want the owner-attacked zombie %d (OwnerHurtTargetGoal.start setTarget)", wolf.ai.getTarget(), zombie.id)
	}

	// (2) The MeleeAttackGoal strikes the mob target via the entity-victim path (the wolf's target is a MOB,
	// so meleeAttackGoal.tick resolves it in the store -> tickEntityVictim -> doHurtTargetEntity).
	startHealth := zombie.health
	melee := newMeleeAttackGoal(1.0)
	loop.withRegion(loop.regionForEntity(wolf), func() { melee.tick(loop, wolf) })
	if zombie.health >= startHealth {
		t.Fatalf("wolf dealt no melee damage to the owner-attacked zombie (health %.2f -> %.2f); the entity-victim path did not fire", startHealth, zombie.health)
	}
}

// --- GAP 2: OCELOT hunts + kills a chicken -------------------------------------------------------

// TestOcelotHuntsChicken: an ocelot whose attack target is an adjacent chicken strikes it through the
// entity-victim OcelotAttackGoal path (resolveOcelotTarget -> ocelotDoHurtTargetEntity), dealing the
// ocelot's ATTACK_DAMAGE 3.0. The chicken (in reach) loses health on the first fire tick (attackTime starts
// 0). Cite Ocelot.registerGoals @8 OcelotAttackGoal + OcelotAttackGoal.tick doHurtTarget.
func TestOcelotHuntsChicken(t *testing.T) {
	loop, oc, chicken := ocelotPreyTestLoop(t, entity.Chicken, 0, 1, 0) // chicken 1 block away -> within reach (2*width)^2 after a step
	oc.onGround = true

	// Place the chicken ADJACENT to the ocelot (touching) so it is inside the (2*width)^2 reach immediately.
	chicken.x = oc.x + oc.width/2 + 0.1
	chicken.z = oc.z
	loop.only().entities.move(chicken, chicken.x, chicken.y, chicken.z)
	chicken.health = 4.0
	startHealth := chicken.health

	// Wire the ocelot's attack target to the chicken (what the chicken target-selector's start() commits),
	// then drive the OcelotAttackGoal. attackTime starts 0 so the first in-reach tick swings.
	oc.ai.setTarget(chicken.id)
	g := newOcelotAttackGoal()
	if !g.canUse(loop, oc) {
		t.Fatal("OcelotAttackGoal.canUse did not fire with a committed chicken target")
	}
	loop.withRegion(loop.regionForEntity(oc), func() { g.tick(loop, oc) })

	if chicken.health >= startHealth {
		t.Fatalf("ocelot dealt no damage to the adjacent chicken (health %.2f -> %.2f); the entity-victim OcelotAttackGoal path did not fire", startHealth, chicken.health)
	}
	// The landed damage is the ocelot's ATTACK_DAMAGE 3.0 (a chicken has 0 armor, so no reduction), never
	// exceeding it. Proves ocelotDoHurtTargetEntity fired.
	got := startHealth - chicken.health
	wantMax := float32(oc.getAttributeValue(attribute.AttackDamage))
	if got <= 0 || got > wantMax+1e-3 {
		t.Fatalf("ocelot dealt %.3f to the chicken, want in (0, %.3f] (ATTACK_DAMAGE via ocelotDoHurtTargetEntity)", got, wantMax)
	}
}

// --- GAP 3: SHULKER teleport-on-hit (wired) + closed-arrow-immune --------------------------------

// TestShulkerHurtReactionWired: the shulkerAiStep-driven post-hurt reaction. A low-HP shulker whose health
// drops between ticks (the tick seam observes health < prevHealth) runs shulkerHurtServerReaction, which
// teleports on a nextInt(4)==0 roll when health < maxHealth*0.5. Also pins the CLOSED arrow-immunity gate.
// Cite Shulker.hurtServer (the post-hurt teleport tail + the closed-arrow top gate).
func TestShulkerHurtReactionWired(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	// A low-HP shulker (5 < 30*0.5). Seed prevHealth via a first tick, then DROP health and tick again — the
	// seam must observe the drop and drive the teleport reaction over enough ticks for a nextInt(4)==0 roll.
	s := loop.spawnShulker(5.5, float64(floorY+1), 5.5)
	s.health = 5.0 // 5 < 30*0.5 == 15 (the low-HP teleport gate)
	ox, oz := s.x, s.z
	moved := false
	loop.withRegion(loop.regionForEntity(s), func() {
		for i := 0; i < 400 && !moved; i++ {
			// Simulate "a hit landed since last tick": force prevHealth ABOVE the current health so the tick
			// seam sees a drop and drives shulkerHurtServerReaction. Enough iterations that a nextInt(4)==0
			// roll + a valid teleport landing hits.
			s.shulker.prevHealth = s.health + 1.0
			loop.shulkerAiStep(s)
			if s.x != ox || s.z != oz {
				moved = true
			}
		}
	})
	if !moved {
		t.Fatal("a low-HP shulker never teleported across 400 hit-drop ticks — shulkerAiStep did not drive shulkerHurtServerReaction")
	}

	// CLOSED arrow-immunity: a closed shulker rejects an arrow directEntity; an open one does not.
	closed := loop.spawnShulker(3.5, float64(floorY+1), 3.5) // spawns CLOSED
	if !shulkerArrowImmune(closed, damageSourceArrow(0)) {
		t.Fatal("closed shulker not immune to an arrow (shulkerArrowImmune false) — the CLOSED arrow top gate")
	}
	loop.shulkerSetRawPeek(closed, shulkerPeekOpen)
	if shulkerArrowImmune(closed, damageSourceArrow(0)) {
		t.Fatal("OPEN shulker reported arrow-immune — only a CLOSED shulker deflects arrows")
	}
}

// --- GAP 4: LLAMA spits at a wolf ----------------------------------------------------------------

// TestLlamaSpitsAtWolf: a llama whose target is an in-range UNTAMED wolf fires a LlamaSpit toward it — the
// RangedAttackGoal on attackTime==0 (with LoS) calls performRangedAttack -> spawnLlamaSpit. A LlamaSpit
// entity is spawned, owned by the llama, with a velocity pointing toward the wolf. Cite Llama.registerGoals
// @3 RangedAttackGoal + Llama.performRangedAttack + Llama.spit + LlamaSpit.
func TestLlamaSpitsAtWolf(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	llama := loop.spawnLlama(8.5, float64(floorY+1), 8.5, false, false)
	llama.onGround = true

	// An UNTAMED wolf 6 blocks away (within the RangedAttackGoal radius 20, and — with FOLLOW_RANGE for the
	// wolf goal scaled 0.25 — the ranged goal itself only needs the wolf as the mob target).
	wolf := NewEntity(loop.idAlloc.AllocID(), entity.Wolf, 8.5, float64(floorY+1), 14.5)
	initSpawnHealth(wolf)
	wolf.ai = &mobAI{rng: newEntityRandom(5)}
	wolf.onGround = true
	loop.only().entities.add(wolf)

	// Wire the wolf as the llama's attack target (what LlamaAttackWolfGoal.start commits), then drive the
	// ranged goal until it fires. attackTime starts -1 (armed to 40 on the first tick that sees the target);
	// with LoS built each tick, seeTime climbs and the goal fires when attackTime reaches 0.
	llama.ai.setTarget(wolf.id)
	g := newLlamaRangedAttackGoal()

	spitID := int32(0)
	loop.withRegion(loop.regionForEntity(llama), func() {
		for i := 0; i < 120 && spitID == 0; i++ {
			g.tick(loop, llama)
			for _, e := range loop.only().entities.byID {
				if e.typ == entity.LlamaSpit.ID {
					spitID = e.id
				}
			}
		}
	})
	if spitID == 0 {
		t.Fatal("the llama never fired a LlamaSpit at its wolf target — the RangedAttackGoal -> performRangedAttack -> spawnLlamaSpit chain did not fire")
	}
	spit, ok := loop.only().entities.get(spitID)
	if !ok {
		t.Fatal("the spawned LlamaSpit is not in the store")
	}
	if spit.arrowShooterID != llama.id {
		t.Fatalf("LlamaSpit owner = %d, want the llama %d", spit.arrowShooterID, llama.id)
	}
	// The spit must be launched TOWARD the wolf: the +Z-ward velocity component is positive (the wolf is at
	// +Z from the llama). A spit with no velocity / wrong direction fails this.
	if spit.vz <= 0 {
		t.Fatalf("LlamaSpit vz = %.4f, want > 0 (aimed toward the wolf at +Z)", spit.vz)
	}
}

// TestLlamaAttackWolfTargetsUntamed: the LlamaAttackWolfGoal acquires a nearby UNTAMED wolf but NOT a tamed
// one (the selector !wolf.isTame()). Cite Llama$LlamaAttackWolfGoal selector.
func TestLlamaAttackWolfTargetsUntamed(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	llama := loop.spawnLlama(8.5, float64(floorY+1), 8.5, false, false)

	// A TAMED wolf close by — must NOT be acquired.
	tamed := NewEntity(loop.idAlloc.AllocID(), entity.Wolf, 9.5, float64(floorY+1), 8.5)
	initSpawnHealth(tamed)
	tamed.tame = true
	loop.only().entities.add(tamed)

	g := newLlamaAttackWolfTargetGoal()
	g.forceTrigger = true
	acquiredTamed := false
	loop.withRegion(loop.regionForEntity(llama), func() { acquiredTamed = g.canUse(loop, llama) })
	if acquiredTamed {
		t.Fatal("LlamaAttackWolfGoal acquired a TAMED wolf — the !isTame() selector failed")
	}

	// An UNTAMED wolf within FOLLOW_RANGE*0.25 — must be acquired.
	untamed := NewEntity(loop.idAlloc.AllocID(), entity.Wolf, 9.0, float64(floorY+1), 8.5)
	initSpawnHealth(untamed)
	loop.only().entities.add(untamed)
	acquiredUntamed := false
	loop.withRegion(loop.regionForEntity(llama), func() {
		g.forceTrigger = true
		acquiredUntamed = g.canUse(loop, llama)
	})
	if !acquiredUntamed {
		t.Fatal("LlamaAttackWolfGoal did NOT acquire a nearby UNTAMED wolf")
	}
	loop.withRegion(loop.regionForEntity(llama), func() { g.start(loop, llama) })
	if llama.ai.getTarget() != untamed.id {
		t.Fatalf("LlamaAttackWolfGoal committed target %d, want the untamed wolf %d", llama.ai.getTarget(), untamed.id)
	}
}

// --- GAP 5: ARMADILLO fears an undead mob --------------------------------------------------------

// TestArmadilloFearsZombie: a ZOMBIE (an UNDEAD-tag mob) within the inflated 7x2x7 box scares an armadillo —
// it rolls up (IDLE -> ROLLING, then -> SCARED after 10 inStateTicks) via the extended isScaredBy undead
// scan (armadilloIsScaredByMob). Cite Armadillo.isScaredBy (the UNDEAD branch) + EntityTypeTags.UNDEAD.
func TestArmadilloFearsZombie(t *testing.T) {
	loop, floorY := armadilloLoop(t)
	a := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)
	a.onGround = true

	// A zombie right next to the armadillo (well inside the inflated box) — an UNDEAD threat.
	zombie := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 9.5, float64(floorY+1), 8.5)
	initSpawnHealth(zombie)
	loop.only().entities.add(zombie)

	// Drive the armadillo AI: the undead scan flags the threat, so it rolls up and reaches SCARED.
	scared := false
	loop.withRegion(loop.regionForEntity(a), func() {
		for i := 0; i < 80 && !scared; i++ {
			loop.armadilloAiStep(a)
			if a.armadilloState == armadilloStateScared {
				scared = true
			}
		}
	})
	if !scared {
		t.Fatalf("armadillo never reached SCARED with a zombie (UNDEAD) adjacent (state=%d) — the isScaredBy undead scan did not flag the threat", a.armadilloState)
	}

	// Sanity: isUndeadType classifies the zombie as undead and a pig as not.
	if !isUndeadType(entity.Zombie.ID) {
		t.Fatal("isUndeadType(Zombie) = false, want true (#minecraft:undead -> #zombies)")
	}
	if isUndeadType(entity.Pig.ID) {
		t.Fatal("isUndeadType(Pig) = true, want false (a pig is not undead)")
	}
}
