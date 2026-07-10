package server

// ender_dragon_test.go -- deterministic pins for the Ender Dragon boss (net.minecraft.world.entity.boss
// .enderdragon.EnderDragon + EndCrystal, 1:1 javap this task). Verifies: (a) the spawn attributes (MAX_HEALTH
// 200) + the 8 sub-parts with the ctor dims; (b) a HEAD hit takes FULL damage; (c) a non-head (BODY) hit
// takes dmg/4 + min(dmg,1); (d) an end crystal heals the dragon +1 every 10 ticks while alive + health<max,
// and hitting the crystal removes it + fires onCrystalDestroyed (a 10.0 head hit if it was nearest); (e) the
// death drive (tickDragonDeath) awards XP after tick 150 and at tick 200 spawns the exit portal + dragon egg.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dragonLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick the dragon fight.
func dragonLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestEnderDragonSpawnDefaults: spawnEnderDragon builds a dragon rendering as entity.EnderDragon.ID with
// 200 HP (MAX_HEALTH 200.0) and the 8 sub-parts with the ctor dimensions.
func TestEnderDragonSpawnDefaults(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	if d.typ != entity.EnderDragon.ID {
		t.Fatalf("dragon typ = %d, want entity.EnderDragon.ID %d", d.typ, entity.EnderDragon.ID)
	}
	if d.dragon == nil {
		t.Fatal("dragon has no dragonState (e.dragon nil)")
	}
	if math.Abs(float64(d.health)-200.0) > 1e-6 {
		t.Fatalf("dragon health = %v, want 200.0 (MAX_HEALTH)", d.health)
	}
	if got := d.getAttributeValue(attribute.MaxHealth); math.Abs(got-200.0) > 1e-9 {
		t.Fatalf("dragon MAX_HEALTH = %v, want 200.0", got)
	}
	if got := d.getAttributeValue(attribute.CameraDistance); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("dragon CAMERA_DISTANCE = %v, want 16.0", got)
	}
	if len(d.dragon.parts) != 8 {
		t.Fatalf("dragon has %d parts, want 8 (head/neck/body/tail1..3/wing1..2)", len(d.dragon.parts))
	}
	// The 8 parts with their ctor (width,height): head(1,1) neck(3,3) body(5,3) tail1..3(2,2) wing1..2(4,2).
	want := map[string][2]float32{
		"head":  {1, 1},
		"neck":  {3, 3},
		"body":  {5, 3},
		"tail1": {2, 2},
		"tail2": {2, 2},
		"tail3": {2, 2},
		"wing1": {4, 2},
		"wing2": {4, 2},
	}
	got := map[string][2]float32{}
	for _, p := range d.dragon.parts {
		got[p.name] = [2]float32{p.w, p.h}
	}
	for name, dims := range want {
		if got[name] != dims {
			t.Fatalf("part %q dims = %v, want %v", name, got[name], dims)
		}
	}
	if d.dragon.phase != dragonPhaseHolding {
		t.Fatalf("dragon phase = %d, want HOLDING (0)", d.dragon.phase)
	}
}

// TestEnderDragonHeadHitFull: a HEAD hit from a player takes FULL damage (no /4 reduction). A 20.0 hit to
// the head drops the dragon from 200 to 180.
func TestEnderDragonHeadHitFull(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	p := combatTestPlayer(loop, 0, float64(floorY+1), 0, 7001)
	start := d.health

	landed := loop.dragonHurtPart(d, "head", damageSourcePlayerAttack(p.entityID), 20.0)
	if !landed {
		t.Fatal("head hit did not land (dragonHurtPart returned false)")
	}
	dealt := start - d.health
	if math.Abs(float64(dealt)-20.0) > 1e-4 {
		t.Fatalf("head hit dealt %v, want 20.0 (FULL damage -- head takes no reduction)", dealt)
	}
}

// TestEnderDragonBodyHitReduced: a non-head (BODY) hit takes dmg/4 + min(dmg,1). A 20.0 hit to the body
// deals 20/4 + min(20,1) = 5 + 1 = 6.0, so the dragon drops from 200 to 194.
func TestEnderDragonBodyHitReduced(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	p := combatTestPlayer(loop, 0, float64(floorY+1), 0, 7002)
	start := d.health

	landed := loop.dragonHurtPart(d, "body", damageSourcePlayerAttack(p.entityID), 20.0)
	if !landed {
		t.Fatal("body hit did not land (dragonHurtPart returned false)")
	}
	dealt := start - d.health
	// 20/4 + min(20,1) = 5 + 1 = 6.0
	if math.Abs(float64(dealt)-6.0) > 1e-4 {
		t.Fatalf("body hit dealt %v, want 6.0 (dmg/4 + min(dmg,1) = 5 + 1)", dealt)
	}
}

// TestEnderDragonResolveHitPartHead: a segment aimed straight at the head sub-box (offset +6 X) resolves to
// "head"; a segment through the body center resolves to "body". Proves the per-part hit-resolve (the input
// to the head-full/others-quartered transform) works.
func TestEnderDragonResolveHitPartHead(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	loop.dragonRecomputeParts(d)

	// The head is at (+6,0,0) from the dragon (a 1x1 box centered there). Cast a segment along +X through it.
	name := loop.dragonResolveHitPart(d, 10, 128, 0, 0, 128, 0)
	if name != "head" {
		t.Fatalf("segment through the head box resolved to %q, want \"head\"", name)
	}
	// The body is centered on the dragon (0,0,0), a 5x3 box. A short vertical segment through the center hits it.
	name = loop.dragonResolveHitPart(d, 0, 130, 0, 0, 126, 0)
	if name != "body" {
		t.Fatalf("segment through the body center resolved to %q, want \"body\"", name)
	}
}

// TestEndCrystalHealsDragon: while a crystal is the dragon's nearestCrystal and alive and the dragon is
// below max health, dragonCheckCrystals heals +1 on a tick where gametime % 10 == 0. On a non-multiple tick
// (or at full health) it does NOT heal.
func TestEndCrystalHealsDragon(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	crystal := loop.spawnEndCrystal(4, 128, 0)
	d.dragon.nearestCrystalID = crystal.id
	d.health = 150.0 // below max so the heal applies

	// A tick where gametime % 10 == 0: heal +1.
	loop.gametime = 20
	loop.dragonCheckCrystals(d)
	if math.Abs(float64(d.health)-151.0) > 1e-4 {
		t.Fatalf("dragon health = %v after a %%10 tick, want 151.0 (+1 crystal heal)", d.health)
	}

	// A tick where gametime % 10 != 0: no heal.
	loop.gametime = 23
	loop.dragonCheckCrystals(d)
	if math.Abs(float64(d.health)-151.0) > 1e-4 {
		t.Fatalf("dragon health = %v after a non-%%10 tick, want 151.0 (no heal off-cadence)", d.health)
	}

	// At full health, no heal even on a %10 tick.
	d.health = float32(dragonMaxHealth)
	loop.gametime = 30
	loop.dragonCheckCrystals(d)
	if math.Abs(float64(d.health)-dragonMaxHealth) > 1e-4 {
		t.Fatalf("dragon health = %v at max on a %%10 tick, want %v (no over-heal)", d.health, dragonMaxHealth)
	}
}

// TestEndCrystalDestroyHitsDragon: hitting a crystal that IS the dragon's nearestCrystal removes the crystal
// AND makes the dragon take a 10.0 head hit (the full-damage head branch -> -10 health). A crystal that is
// NOT the nearest is removed but the dragon takes no hit.
func TestEndCrystalDestroyHitsDragon(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	p := combatTestPlayer(loop, 0, float64(floorY+1), 0, 7003)
	// Place the crystal FAR from the dragon (>12 blocks) so its own destroy-explosion (now wired,
	// power 6.0) does not reach the dragon body -- this isolates the onCrystalDestroyed 10.0 head hit.
	crystal := loop.spawnEndCrystal(200, 128, 0)
	d.dragon.nearestCrystalID = crystal.id
	startHealth := d.health

	landed := loop.endCrystalHurt(crystal, damageSourcePlayerAttack(p.entityID))
	if !landed {
		t.Fatal("crystal hit did not land (endCrystalHurt returned false)")
	}
	// The crystal is removed from the store.
	owner := loop.regionForEntity(crystal)
	if _, ok := owner.entities.get(crystal.id); ok {
		t.Fatal("crystal still in the store after being hit (should be removed)")
	}
	if !crystal.dead {
		t.Fatal("crystal not marked dead after being hit")
	}
	// The dragon (this was its nearestCrystal) took a 10.0 HEAD hit -> -10 health.
	dealt := startHealth - d.health
	if math.Abs(float64(dealt)-10.0) > 1e-4 {
		t.Fatalf("dragon lost %v health from the crystal destroy, want 10.0 (head explosion hit)", dealt)
	}

	// A crystal that is NOT the nearest: destroyed, but the dragon takes no hit.
	other := loop.spawnEndCrystal(-200, 128, 0)
	// nearestCrystalID was cleared to 0 (the old crystal is gone); adopt the new one is NOT set, so this
	// crystal is not nearest.
	d.dragon.nearestCrystalID = 0
	afterFirst := d.health
	loop.endCrystalHurt(other, damageSourcePlayerAttack(p.entityID))
	if math.Abs(float64(d.health)-float64(afterFirst)) > 1e-4 {
		t.Fatalf("dragon lost health from a non-nearest crystal destroy (health %v -> %v), want no change", afterFirst, d.health)
	}
}

// TestDragonDeathAwardsXpAndPortal: tickDragonDeath sets DYING, showers XP after tick 150 (every 5 ticks,
// floor(500*0.08)=40 per award), and at tick 200 spawns the exit portal + dragon egg and removes the dragon.
func TestDragonDeathAwardsXpAndPortal(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	owner := loop.regionForEntity(d)

	// Before tick 150: no XP orbs awarded.
	loop.withRegion(owner, func() {
		for i := 0; i < 150; i++ {
			loop.tickDragonDeath(d)
		}
	})
	if d.dragon.phase != dragonPhaseDying {
		t.Fatalf("dragon phase = %d during death, want DYING (%d)", d.dragon.phase, dragonPhaseDying)
	}
	if orbs := countByType(owner, entity.ExperienceOrb.ID); orbs != 0 {
		t.Fatalf("XP orbs = %d at deathTime 150, want 0 (award starts AFTER tick 150)", orbs)
	}

	// Tick 155 (> 150 && % 5 == 0): the first XP award (floor(500*0.08) = 40 -> orb chunks).
	loop.withRegion(owner, func() {
		for i := 150; i < 155; i++ {
			loop.tickDragonDeath(d)
		}
	})
	if orbs := countByType(owner, entity.ExperienceOrb.ID); orbs == 0 {
		t.Fatal("no XP orbs awarded at deathTime 155 (want the floor(500*0.08)=40 shower)")
	}

	// Drive to tick 200: the dragon spawns the exit portal + dragon egg and is removed.
	loop.withRegion(owner, func() {
		for d.dragon.dragonDeathTime < 200 {
			loop.tickDragonDeath(d)
		}
	})
	if _, ok := owner.entities.get(d.id); ok {
		t.Fatal("dragon still in the store at deathTime 200 (should be removed)")
	}
	// The exit portal + dragon egg are placed at the fight origin (0,128,0) / (0,129,0).
	mgr := owner.world
	portalState, ok := mgr.GetBlock(pk.Position{X: 0, Y: 128, Z: 0}, dimMinY)
	if !ok {
		t.Fatal("no block at the fight origin after death (expected END_PORTAL)")
	}
	if _, isPortal := block.StateList[portalState].(block.EndPortal); !isPortal {
		t.Fatalf("origin block = %v, want END_PORTAL", block.StateList[portalState])
	}
	eggState, ok := mgr.GetBlock(pk.Position{X: 0, Y: 129, Z: 0}, dimMinY)
	if !ok {
		t.Fatal("no block above the fight origin after death (expected DRAGON_EGG)")
	}
	if _, isEgg := block.StateList[eggState].(block.DragonEgg); !isEgg {
		t.Fatalf("block above origin = %v, want DRAGON_EGG", block.StateList[eggState])
	}
}

// TestDragonHurtRejectedWhileDying: once the dragon is DYING (tickDragonDeath set the phase), dragonHurtPart
// rejects all further damage (the getPhase() == DYING guard).
func TestDragonHurtRejectedWhileDying(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	p := combatTestPlayer(loop, 0, float64(floorY+1), 0, 7004)
	owner := loop.regionForEntity(d)

	// Enter DYING (one death tick sets the phase).
	loop.withRegion(owner, func() { loop.tickDragonDeath(d) })
	before := d.health
	landed := loop.dragonHurtPart(d, "head", damageSourcePlayerAttack(p.entityID), 50.0)
	if landed {
		t.Fatal("head hit landed on a DYING dragon (should be rejected)")
	}
	if math.Abs(float64(d.health)-float64(before)) > 1e-4 {
		t.Fatalf("DYING dragon lost health (%v -> %v), want no change", before, d.health)
	}
}

// TestDragonDeathFinalXpOneShot pins the E-2 fix: at dragonDeathTime == 200 a ONE-SHOT ExperienceOrb
// .award(floor(xp * 0.2f)) fires (default xp 500 -> +100) IN ADDITION to the per-5-tick floor(xp*0.08)
// shower, BEFORE the dragon is removed. The final tick's XP delta must be at least the 100 one-shot.
func TestDragonDeathFinalXpOneShot(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	owner := loop.regionForEntity(d)

	// Drive to deathTime 199 (one short of the death frame).
	loop.withRegion(owner, func() {
		for d.dragon.dragonDeathTime < 199 {
			loop.tickDragonDeath(d)
		}
	})
	before := sumOrbXP(loop)

	// The 200th tick: the one-shot floor(500*0.2)=100 award fires (plus the tick-200 %5 shower of 40).
	loop.withRegion(owner, func() {
		loop.tickDragonDeath(d)
	})
	after := sumOrbXP(loop)

	gained := after - before
	// The one-shot alone is 100; the tick-200 %5==0 shower adds another 40. So the delta is >= 100.
	if gained < 100 {
		t.Fatalf("XP gained on the death frame = %d, want >= 100 (the one-shot floor(500*0.2))", gained)
	}
	if _, ok := owner.entities.get(d.id); ok {
		t.Fatal("dragon still present after the death frame (should be removed)")
	}
}

// TestEndCrystalExplodesOnDestroy: destroying an end crystal with a NON-explosion source detonates a
// power-6.0 explosion at the crystal (EndCrystal.hurtServer: !src.is(IS_EXPLOSION) -> level.explode 6.0
// BLOCK). A fragile block placed next to the crystal is broken by the blast. A crystal destroyed by an
// explosion source does NOT re-explode (the IS_EXPLOSION guard), so the same block survives.
func TestEndCrystalExplodesOnDestroy(t *testing.T) {
	loop, _ := dragonLoop(t)
	p := combatTestPlayer(loop, 0, 129, 0, 7010)

	// A fragile glass block one cell beside where the crystal sits (crystal at 4,128,0).
	glassPos := pk.Position{X: 5, Y: 128, Z: 0}
	glass := block.DefaultStateID["minecraft:glass"]
	loop.only().world.SetBlock(glassPos, glass, dimMinY)

	crystal := loop.spawnEndCrystal(4, 128, 0)
	// Non-explosion source (a player attack) -> the crystal explodes (power 6.0).
	loop.endCrystalHurt(crystal, damageSourcePlayerAttack(p.entityID))

	after, _ := loop.only().world.GetBlock(glassPos, dimMinY)
	if after == glass {
		t.Fatal("glass beside the crystal survived the crystal destroy explosion (explosion did not fire)")
	}

	// A crystal destroyed BY an explosion does not re-explode: re-place the glass, destroy with an
	// explosion source, and assert the glass survives.
	loop.only().world.SetBlock(glassPos, glass, dimMinY)
	crystal2 := loop.spawnEndCrystal(4, 128, 0)
	loop.endCrystalHurt(crystal2, damageSourceOf(damageTypeExplosion))
	after2, _ := loop.only().world.GetBlock(glassPos, dimMinY)
	if after2 != glass {
		t.Fatal("glass broken when the crystal was destroyed by an explosion (IS_EXPLOSION guard failed)")
	}
}
