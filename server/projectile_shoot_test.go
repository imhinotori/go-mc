package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

func TestShootVectorInaccuracyZeroIsStraight(t *testing.T) {
	r := newEntityRandom(12345)
	vx, vy, vz := shootVectorFromRotation(r, 0, 0, 0, 3.0, 0.0, 0, 0, 0, true)
	if math.Abs(vx) > 1e-12 || math.Abs(vy) > 1e-12 || math.Abs(vz-3.0) > 1e-9 {
		t.Fatalf("inaccuracy 0 not straight: v=(%.12f,%.12f,%.12f)", vx, vy, vz)
	}
}

func TestShootVectorInaccuracyDrawOrderXYZ(t *testing.T) {
	const velocity, inacc = 3.0, 1.0
	r := newEntityRandom(999)
	vx, vy, vz := shootVectorFromRotation(r, 0, 0, 0, velocity, inacc, 0, 0, 0, true)
	twin := newEntityRandom(999)
	spread := 0.0172275 * inacc
	ex := (0.0 + arrowTriangle(twin, 0, spread)) * velocity
	ey := (0.0 + arrowTriangle(twin, 0, spread)) * velocity
	ez := (1.0 + arrowTriangle(twin, 0, spread)) * velocity
	if math.Abs(vx-ex) > 1e-12 || math.Abs(vy-ey) > 1e-12 || math.Abs(vz-ez) > 1e-12 {
		t.Fatalf("draw order mismatch: got (%.12f,%.12f,%.12f) want (%.12f,%.12f,%.12f)", vx, vy, vz, ex, ey, ez)
	}
	if vx == 0 && vy == 0 && vz == velocity {
		t.Fatalf("inaccuracy positive produced a perfectly-straight shot (no spread applied)")
	}
}

func TestRiptideOnlyLaunchesInWaterOrRain(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	p.playerEntity = &Entity{id: p.entityID}
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase, mkTrident(map[string]int{enchRiptide: 3}))
	inv.heldSlot = 0
	if tridentStart(loop, p) && isUsingItem(p) {
		t.Fatalf("riptide trident began a draw on dry land (use should FAIL)")
	}
	p.useItem = inv.get(hotbarMenuSlotBase)
	p.useItemRemaining = tridentUseDuration - 20
	p.useItemHand = interactionHandMain
	loop.tridentReleaseUsing(p, p.useItem, interactionHandMain)
	if a := firstTrident(loop); a != nil {
		t.Fatalf("dry-land riptide release spawned a trident (must not throw)")
	}
	if p.playerEntity.vx != 0 || p.playerEntity.vy != 0 || p.playerEntity.vz != 0 {
		t.Fatalf("dry-land riptide launched the player: v=(%.4f,%.4f,%.4f)", p.playerEntity.vx, p.playerEntity.vy, p.playerEntity.vz)
	}
	loop2 := bowLoop()
	p2 := bowPlayer(loop2, gameModeSurvival)
	p2.playerEntity = &Entity{id: p2.entityID}
	p2.pitch = -90
	inv2 := ensureInventory(p2)
	inv2.set(hotbarMenuSlotBase, mkTrident(map[string]int{enchRiptide: 3}))
	inv2.heldSlot = 0
	// isInWaterOrRain -> isRainingAt -> canSeeSky now reads real sky-light (getBrightness(SKY,pos) >= 15),
	// so load p2's chunk with open-sky sky-light 15 or the rain gate reads false and riptide won't launch.
	if mgr2 := loop2.regions[globalRegion].world; mgr2 != nil {
		ch2 := level.EmptyChunk(blockTestSecs)
		ch2.Status = level.StatusFull
		mgr2.Insert(level.ChunkPos{0, 0}, ch2)
		setSkyLight(ch2, 15)
	}
	loop2.weather.raining = true
	loop2.weather.rainLevel = 1.0
	if !tridentStart(loop2, p2) {
		t.Fatalf("riptide draw did not begin in rain")
	}
	p2.useItemRemaining = tridentUseDuration - 20
	loop2.releaseUsingItem(p2)
	if p2.playerEntity.vy < 2.5 {
		t.Fatalf("riptide in rain did not launch the player: vy=%.4f", p2.playerEntity.vy)
	}
}

func TestRiptidePassengerAborts(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	p.playerEntity = &Entity{id: p.entityID}
	p.pitch = -90
	p.vehicleID = 4242
	inv := ensureInventory(p)
	inv.set(hotbarMenuSlotBase, mkTrident(map[string]int{enchRiptide: 3}))
	inv.heldSlot = 0
	loop.weather.raining = true
	loop.weather.rainLevel = 1.0
	if !tridentStart(loop, p) {
		t.Fatalf("riptide draw did not begin in rain")
	}
	p.useItemRemaining = tridentUseDuration - 20
	loop.releaseUsingItem(p)
	if firstTrident(loop) != nil {
		t.Fatalf("passenger riptide release threw a trident (must abort)")
	}
	if p.playerEntity.vy != 0 {
		t.Fatalf("passenger riptide launched the player: vy=%.4f, want 0", p.playerEntity.vy)
	}
}

func TestArrowWaterInertiaIs0p6(t *testing.T) {
	loop, mgr := newBlockLoop()
	loop.only().entities = newEntityStore()
	water := block.DefaultStateID["minecraft:water"]
	for z := 0; z <= 3; z++ {
		mgr.SetBlock(pk.Position{X: 1, Y: 65, Z: z}, water, dimMinY)
	}
	a := loop.spawnArrow(0, 1.5, 65.5, 0.5, 0, 0, 1.0, 2.0)
	if !loop.isWaterAt(1, 65, 0) {
		t.Fatalf("arrow spawn cell not water (test setup)")
	}
	startVZ := a.vz
	loop.withRegion(loop.only(), func() { loop.tickArrow(a) })
	wantVZ := startVZ * arrowWaterInertia
	if math.Abs(a.vz-wantVZ) > 1e-9 {
		t.Fatalf("arrow water drag: vz=%.9f, want %.9f (0.6 water inertia)", a.vz, wantVZ)
	}
	if math.Abs(a.vz-startVZ*arrowAirDrag) < 1e-6 {
		t.Fatalf("arrow used air drag 0.99 in water: vz=%.6f", a.vz)
	}
}

func TestSnowballVsPlayerFlashesAndKnocks(t *testing.T) {
	loop := bowLoop()
	victim := bowPlayer(loop, gameModeSurvival)
	victim.entityID = 55
	victim.playerEntity = &Entity{id: victim.entityID, x: victim.x, y: victim.y, z: victim.z}
	victim.health = 20.0
	e := &Entity{id: 900, isThrowable: true, throwableKind: throwSnowball, throwOwnerID: 0,
		x: victim.x, y: victim.y, z: victim.z + 1.0}
	loop.withRegion(loop.only(), func() { loop.throwableOnHitEntity(e, victim) })
	if victim.health != 20.0 {
		t.Fatalf("snowball dealt damage to player: health=%.4f, want 20", victim.health)
	}
	if victim.playerEntity.vz >= 0 {
		t.Fatalf("snowball did not knock the player away: vz=%.6f, want < 0", victim.playerEntity.vz)
	}
}
