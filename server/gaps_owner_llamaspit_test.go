package server

// gaps_owner_llamaspit_test.go — the two closing mob-AI parity gaps of this session:
//
//   - GAP 1 (WOLF owner-HURT-BY defense): a tamed wolf targets the MOB that hurt its owner. When a mob
//     damages a player (applyDamage -> resolveMobResponsibleForDamage records lastHurtByMob), the owner's
//     OwnerHurtByTargetGoal acquires that mob. TestWolfDefendsOwnerFromMob drives the full chain; the
//     inert-world half proves a player NOT hurt by a mob acquires NO target and a PvP hit records nothing.
//   - GAP 2 (LlamaSpit FLIGHT): a spawned LlamaSpit is ticked to a hit — it flies (drag 0.99, gravity 0.06)
//     and deals 1.0 spit damage to the first LivingEntity it reaches, then is removed. TestLlamaSpitFliesAndHits
//     drives it; the inert half proves a world with no spit ticks nothing.
//
// The pig oracle (TestPluginPigEqualsGoNativePig) is a SEPARATE mob and is untouched: these build wolves,
// players, llamas, and a llama spit only — no draw reaches the pinned pig stream.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// --- GAP 1: WOLF defends its owner (OwnerHurtByTargetGoal) ---------------------------------------

// TestWolfDefendsOwnerFromMob: a tamed wolf whose OWNER was just hurt by a mob targets that mob. A mob
// damages the owner through applyDamage (resolveMobResponsibleForDamage records owner.lastHurtByMob +
// stamp), then OwnerHurtByTargetGoal.canUse reads it and commits the attacker as the wolf's target. Cite
// Wolf.registerGoals targetSelector @1 OwnerHurtByTargetGoal + LivingEntity.setLastHurtByMob /
// resolveMobResponsibleForDamage.
func TestWolfDefendsOwnerFromMob(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)

	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	owner := addTestPlayer(loop, 44001, 8.5, float64(floorY+1), 8.5)
	owner.health = 20.0
	owner.gameMode = gameModeSurvival
	owner.client = captureClient(256) // actuallyHurt sends SetHealth
	wolf.tame = true
	wolf.ownerUUID = owner.entityID
	wolf.orderedToSit = false

	// A zombie that HURTS the owner: a mob_attack source with the zombie as attacker, routed through the real
	// player hurt pipeline so resolveMobResponsibleForDamage records owner.lastHurtByMob.
	zombie := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, 9.5, float64(floorY+1), 8.5)
	initSpawnHealth(zombie)
	zombie.ai = &mobAI{rng: newEntityRandom(3)}
	loop.only().entities.add(zombie)

	// Advance gametime so the stamp is non-zero: the goal's initial timestamp field is 0, and a hit stamped
	// at gametime 0 would read as "already responded" (ts == this.timestamp). A real mob has ticked before
	// combat, so tickCount is never 0 at the hit — mirror that here.
	loop.gametime = 50
	loop.withRegion(loop.regionForEntity(wolf), func() {
		loop.applyDamage(owner, damageSourceMobAttack(zombie.id), 3.0)
	})
	if owner.lastHurtByMob != zombie.id {
		t.Fatalf("owner.lastHurtByMob = %d, want the attacking zombie %d (resolveMobResponsibleForDamage did not record the mob)", owner.lastHurtByMob, zombie.id)
	}

	// OwnerHurtByTargetGoal reads owner.getLastHurtByMob() and commits the zombie as the wolf's target.
	g := newOwnerHurtByTargetGoal()
	loop.withRegion(loop.regionForEntity(wolf), func() {
		if !g.canUse(loop, wolf) {
			t.Fatal("OwnerHurtByTargetGoal.canUse did not fire for a tamed wolf whose owner was hurt by a mob")
		}
		g.start(loop, wolf)
	})
	if wolf.ai.getTarget() != zombie.id {
		t.Fatalf("wolf target = %d, want the mob %d that hurt its owner (OwnerHurtByTargetGoal.start setTarget)", wolf.ai.getTarget(), zombie.id)
	}
}

// TestWolfOwnerHurtByInertWithoutMobHit: the inert half of GAP 1 — a player NOT hurt by a mob has no
// lastHurtByMob, so OwnerHurtByTargetGoal acquires NOTHING. It also proves a PvP hit (a player attacker)
// does NOT record lastHurtByMob (the mob-only gate), so the wolf never turns on the owner's PvP opponent.
func TestWolfOwnerHurtByInertWithoutMobHit(t *testing.T) {
	loop, floorY, _ := wolfLoop(t)

	wolf := spawnWolf(loop, 8.5, float64(floorY+1), 8.5)
	owner := addTestPlayer(loop, 44002, 8.5, float64(floorY+1), 8.5)
	owner.health = 20.0
	owner.gameMode = gameModeSurvival
	owner.client = captureClient(256) // actuallyHurt sends SetHealth
	wolf.tame = true
	wolf.ownerUUID = owner.entityID
	wolf.orderedToSit = false

	// (a) No hit at all: lastHurtByMob stays 0 -> canUse false.
	g := newOwnerHurtByTargetGoal()
	fired := false
	loop.withRegion(loop.regionForEntity(wolf), func() { fired = g.canUse(loop, wolf) })
	if fired {
		t.Fatal("OwnerHurtByTargetGoal.canUse fired for an un-hurt owner (lastHurtByMob should be 0)")
	}

	// (b) A PvP hit: an attacking PLAYER hits the owner. resolveMobResponsibleForDamage is gated on the
	// attacker being a MOB, so a player attacker records NOTHING -> the wolf never acquires the PvP opponent.
	attacker := addTestPlayer(loop, 44003, 9.5, float64(floorY+1), 8.5)
	loop.withRegion(loop.regionForEntity(wolf), func() {
		loop.applyDamage(owner, damageSourcePlayerAttack(attacker.entityID), 2.0)
	})
	if owner.lastHurtByMob != 0 {
		t.Fatalf("owner.lastHurtByMob = %d after a PvP hit, want 0 (the mob-only gate must not record a player attacker)", owner.lastHurtByMob)
	}
	loop.withRegion(loop.regionForEntity(wolf), func() { fired = g.canUse(loop, wolf) })
	if fired {
		t.Fatal("OwnerHurtByTargetGoal.canUse fired after a PvP hit — a player attacker must not feed the wolf's mob-defense goal")
	}
}

// --- GAP 2: LlamaSpit FLIGHT + hit ---------------------------------------------------------------

// TestLlamaSpitFliesAndHits: a spawned LlamaSpit travels toward a target it is aimed at, deals 1.0 spit
// damage when it reaches it, and is removed from the store. Aims the spit straight at an adjacent wolf so a
// single flight tick lands the hit. Cite LlamaSpit.tick + LlamaSpit.onHitEntity (hurtServer(spit, 1.0f)).
func TestLlamaSpitFliesAndHits(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	llama := loop.spawnLlama(8.5, float64(floorY+1), 8.5, false, false)

	// A wolf the spit will hit, a short distance in +Z so a single flight step (spit vz>0) crosses it.
	wolf := NewEntity(loop.idAlloc.AllocID(), entity.Wolf, 8.5, float64(floorY+1)+0.5, 10.0)
	initSpawnHealth(wolf)
	wolf.ai = &mobAI{rng: newEntityRandom(7)}
	loop.only().entities.add(wolf)
	startHealth := wolf.health

	// Spawn the spit aimed at the wolf's 1/3-height (exactly what Llama.performRangedAttack passes).
	targetY := wolf.y + float64(wolf.height)*llamaSpitTargetYFraction
	var spit *Entity
	loop.withRegion(loop.regionForEntity(llama), func() {
		spit = loop.spawnLlamaSpit(llama, wolf.x, targetY, wolf.z)
	})
	if spit == nil || !spit.isLlamaSpit {
		t.Fatal("spawnLlamaSpit did not produce an isLlamaSpit projectile")
	}
	spitID := spit.id

	// Tick the flight dispatch until the spit hits + is removed (a handful of ticks over the short gap).
	hit := false
	for i := 0; i < 200 && !hit; i++ {
		loop.tickLlamaSpits()
		if _, ok := loop.only().entities.get(spitID); !ok {
			hit = true
		}
	}
	if !hit {
		t.Fatal("the LlamaSpit never hit + was removed across 200 flight ticks — tickLlamaSpits did not resolve the hit")
	}
	if wolf.health >= startHealth {
		t.Fatalf("the wolf took no spit damage (health %.2f -> %.2f); LlamaSpit.onHitEntity did not deal 1.0", startHealth, wolf.health)
	}
	// The landed damage is the 1.0 spit bullet (a wolf has 0 armor -> no reduction).
	got := startHealth - wolf.health
	if got <= 0 || got > float32(llamaSpitBulletDamage)+1e-3 {
		t.Fatalf("the wolf took %.3f spit damage, want in (0, %.1f] (LlamaSpit.onHitEntity 1.0f)", got, llamaSpitBulletDamage)
	}
}

// TestLlamaSpitInertWithoutSpit: the inert half of GAP 2 — a world with NO llama spit ticks nothing in
// tickLlamaSpits (no panic, no entity churn), so the pig oracle path is unperturbed.
func TestLlamaSpitInertWithoutSpit(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	// A plain (non-spit) entity in the store must be untouched by tickLlamaSpits.
	wolf := NewEntity(loop.idAlloc.AllocID(), entity.Wolf, 8.5, float64(floorY+1), 8.5)
	initSpawnHealth(wolf)
	loop.only().entities.add(wolf)
	before := loop.only().entities.len()

	loop.tickLlamaSpits() // must be a pure no-op with no spit in flight

	if loop.only().entities.len() != before {
		t.Fatalf("tickLlamaSpits changed the entity count (%d -> %d) with no spit in flight", before, loop.only().entities.len())
	}
	if _, ok := loop.only().entities.get(wolf.id); !ok {
		t.Fatal("tickLlamaSpits removed a non-spit entity")
	}
}
