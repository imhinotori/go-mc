package server

// ender_dragon_offense_test.go -- pins the two EnderDragon offense subsystems that were previously NO-OP
// seams: the STRAFE_PLAYER DragonFireball (DragonStrafePlayerPhase.doServerTick + DragonFireball.onHit) and
// the SITTING_FLAMING dragon_breath AreaEffectCloud (DragonSittingFlamingPhase.doServerTick + end()). All
// facts verified against the 26.2 jar (javap this task): fireball AEC radius 3.0 / duration 600 / growing
// radiusPerTick (7-3)/600 / INSTANT_DAMAGE amp1; flame AEC radius 5.0 / duration 200 / INSTANT_DAMAGE amp0.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestDragonSpawnFireballCreatesProjectile: dragonSpawnFireball (the STRAFE seam) spawns a live
// hurtDragonFireball projectile owned by the dragon, aimed along the given direction. Cite
// DragonStrafePlayerPhase.doServerTick (new DragonFireball; snapTo; addFreshEntity).
func TestDragonSpawnFireballCreatesProjectile(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)

	loop.dragonSpawnFireball(d, 0, 128, 0, [3]float64{10, 0, 0})

	var fb *Entity
	for _, e := range loop.cur().entities.all() {
		if e.isHurting && e.hurtingKind == hurtDragonFireball {
			fb = e
			break
		}
	}
	if fb == nil {
		t.Fatal("dragonSpawnFireball did not create a DragonFireball projectile")
	}
	if fb.typ != entity.DragonFireball.ID {
		t.Fatalf("fireball typ = %d, want entity.DragonFireball.ID %d", fb.typ, entity.DragonFireball.ID)
	}
	if fb.hurtOwnerID != d.id {
		t.Fatalf("fireball owner = %d, want dragon id %d", fb.hurtOwnerID, d.id)
	}
	// assignDirectionalMovement(dir, accelerationPower=0.1): delta = dir.normalize()*0.1. dir=(10,0,0) -> vx=0.1.
	if math.Abs(fb.vx-0.1) > 1e-9 || math.Abs(fb.vy) > 1e-9 || math.Abs(fb.vz) > 1e-9 {
		t.Fatalf("fireball delta = (%v,%v,%v), want (0.1,0,0)", fb.vx, fb.vy, fb.vz)
	}
}

// TestDragonFireballOnHitSpawnsBreathCloud: DragonFireball.onHit spawns a dragon_breath AreaEffectCloud at
// the impact (radius 3.0, duration 600, growing radiusPerTick (7-3)/600, INSTANT_DAMAGE amp1 dur1) owned by
// the dragon, and discards the fireball. Cite DragonFireball.onHit.
func TestDragonFireballOnHitSpawnsBreathCloud(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	fb := loop.spawnDragonFireball(d.id, 8.5, float64(floorY+1), 8.5, 1, 0, 0)

	loop.dragonFireballOnHit(fb, 8.5, float64(floorY+1), 8.5)

	if _, ok := loop.cur().entities.get(fb.id); ok {
		t.Fatal("fireball was not discarded after onHit")
	}
	var cloud *Entity
	for _, e := range loop.cur().entities.all() {
		if e.isAreaEffectCloud {
			cloud = e
			break
		}
	}
	if cloud == nil {
		t.Fatal("DragonFireball.onHit did not spawn an AreaEffectCloud")
	}
	if cloud.aecRadius != 3.0 {
		t.Fatalf("cloud radius = %v, want 3.0 (setRadius)", cloud.aecRadius)
	}
	if cloud.aecDuration != 600 {
		t.Fatalf("cloud duration = %v, want 600 (setDuration)", cloud.aecDuration)
	}
	if cloud.aecOwnerID != d.id {
		t.Fatalf("cloud owner = %d, want dragon id %d (setOwner)", cloud.aecOwnerID, d.id)
	}
	want := (7.0 - 3.0) / 600.0
	if math.Abs(float64(cloud.aecRadiusPerTick)-want) > 1e-7 {
		t.Fatalf("cloud radiusPerTick = %v, want %v ((7-3)/600, growing)", cloud.aecRadiusPerTick, want)
	}
	if len(cloud.aecEffects) != 1 || cloud.aecEffects[0].id != effectInstantDamage || cloud.aecEffects[0].amplifier != 1 {
		t.Fatalf("cloud effects = %#v, want [INSTANT_DAMAGE amp1]", cloud.aecEffects)
	}
}

// TestDragonSittingFlamingSpawnsBreathCloud: dragonSittingFlamingTick at flameTicks==10 spawns the flame
// AreaEffectCloud (radius 5.0, duration 200, INSTANT_DAMAGE amp0) owned by the dragon, held in flameCloudID.
// Cite DragonSittingFlamingPhase.doServerTick (flameTick==10 branch).
func TestDragonSittingFlamingSpawnsBreathCloud(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	loop.dragonSetPhase(d, dragonPhaseSittingFlaming)

	for i := 0; i < 10; i++ {
		loop.dragonSittingFlamingTick(d)
	}

	if d.dragon.flameCloudID == 0 {
		t.Fatal("SITTING_FLAMING did not record a flame cloud id at flameTicks 10")
	}
	cloud, ok := loop.cur().entities.get(d.dragon.flameCloudID)
	if !ok {
		t.Fatal("flame cloud id set but no AreaEffectCloud in the store")
	}
	if cloud.aecRadius != 5.0 {
		t.Fatalf("flame cloud radius = %v, want 5.0 (setRadius)", cloud.aecRadius)
	}
	if cloud.aecDuration != 200 {
		t.Fatalf("flame cloud duration = %v, want 200 (setDuration)", cloud.aecDuration)
	}
	if cloud.aecOwnerID != d.id {
		t.Fatalf("flame cloud owner = %d, want dragon id %d (setOwner)", cloud.aecOwnerID, d.id)
	}
	if len(cloud.aecEffects) != 1 || cloud.aecEffects[0].id != effectInstantDamage || cloud.aecEffects[0].amplifier != 0 {
		t.Fatalf("flame cloud effects = %#v, want [INSTANT_DAMAGE amp0]", cloud.aecEffects)
	}
}

// TestDragonSittingFlamingEndDiscardsCloud: leaving SITTING_FLAMING (setPhase out) runs end() -> discard the
// flame cloud + clear flameCloudID. Cite DragonSittingFlamingPhase.end().
func TestDragonSittingFlamingEndDiscardsCloud(t *testing.T) {
	loop, _ := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	loop.dragonSetPhase(d, dragonPhaseSittingFlaming)
	for i := 0; i < 10; i++ {
		loop.dragonSittingFlamingTick(d)
	}
	cloudID := d.dragon.flameCloudID
	if cloudID == 0 {
		t.Fatal("no flame cloud spawned to discard")
	}

	loop.dragonSetPhase(d, dragonPhaseTakeoff)

	if d.dragon.flameCloudID != 0 {
		t.Fatalf("flameCloudID = %d after end(), want 0 (cleared)", d.dragon.flameCloudID)
	}
	if _, ok := loop.cur().entities.get(cloudID); ok {
		t.Fatal("flame cloud not discarded by SITTING_FLAMING end()")
	}
}

// TestDragonBreathCloudDealsHarm: an entity standing in the flame AEC (INSTANT_DAMAGE amp0) takes 3.0 harm on
// the every-5-tick apply pass (applyInstantaneousEffect scale 0.5 -> 6*2^0*0.5 = 3.0). Cite AreaEffectCloud
// serverTick + HealOrHarmMobEffect.applyInstantaneousEffect.
func TestDragonBreathCloudDealsHarm(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)

	loop.dragonSpawnBreathCloud(d, 8.5, float64(floorY+1), 8.5)
	cloud, ok := loop.cur().entities.get(d.dragon.flameCloudID)
	if !ok {
		t.Fatal("no flame cloud to test harm with")
	}
	cloud.aecWaitTime = 0

	pig := mobEffectTestEntity(loop, entity.Pig, 8.5, float64(floorY+1), 8.5)
	start := pig.health

	for i := 0; i < 5; i++ {
		loop.tickAreaEffectCloud(cloud)
	}

	if math.Abs(float64(start-pig.health)-3.0) > 1e-6 {
		t.Fatalf("pig took %v harm from the breath cloud, want 3.0 (INSTANT_DAMAGE amp0)", start-pig.health)
	}
}

// TestDragonFireballCloudDealsHarm: an entity in the fireball dragon_breath AEC (INSTANT_DAMAGE amp1) takes
// 6.0 harm on the apply pass (6*2^1*0.5 = 6.0). Cite DragonFireball.onHit + AreaEffectCloud serverTick.
func TestDragonFireballCloudDealsHarm(t *testing.T) {
	loop, floorY := dragonLoop(t)
	d := loop.spawnEnderDragon(0, 128, 0)
	fb := loop.spawnDragonFireball(d.id, 8.5, float64(floorY+1), 8.5, 1, 0, 0)
	loop.dragonFireballOnHit(fb, 8.5, float64(floorY+1), 8.5)

	var cloud *Entity
	for _, e := range loop.cur().entities.all() {
		if e.isAreaEffectCloud {
			cloud = e
			break
		}
	}
	if cloud == nil {
		t.Fatal("no AEC from the fireball to test harm with")
	}
	cloud.aecWaitTime = 0

	pig := mobEffectTestEntity(loop, entity.Pig, 8.5, float64(floorY+1), 8.5)
	start := pig.health

	for i := 0; i < 5; i++ {
		loop.tickAreaEffectCloud(cloud)
	}

	if math.Abs(float64(start-pig.health)-6.0) > 1e-6 {
		t.Fatalf("pig took %v harm from the fireball cloud, want 6.0 (INSTANT_DAMAGE amp1)", start-pig.health)
	}
}
