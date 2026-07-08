package server

// water_mobs_test.go -- deterministic pins for the eight water mobs (Squid/GlowSquid/Cod/Salmon/
// Pufferfish/TropicalFish/Dolphin/Tadpole, 1:1 javap this session). Verifies the spawn attributes, the
// Tadpole -> Frog growth at ticksToBeFrog (24000), the Pufferfish puff state machine, and the
// drown-on-land inversion.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

func waterMobLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestSquidSpawnDefaults: spawnSquid builds a Squid rendering as entity.Squid.ID with Mob + MAX_HEALTH 10.
func TestSquidSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	s := loop.spawnSquid(8.5, float64(floorY+1), 8.5, false)
	if s.typ != entity.Squid.ID {
		t.Fatalf("squid typ = %d, want entity.Squid.ID %d", s.typ, entity.Squid.ID)
	}
	if !s.isWaterMob || !s.isSquid {
		t.Fatal("squid not marked isWaterMob/isSquid")
	}
	if math.Abs(float64(s.health)-10.0) > 1e-6 {
		t.Fatalf("squid health = %v, want 10.0 (MAX_HEALTH)", s.health)
	}
	if got := s.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("squid MAX_HEALTH = %v, want 10.0", got)
	}
	if s.ai == nil || s.ai.rng == nil {
		t.Fatal("squid has no minimal AI / rng")
	}
}

// TestGlowSquidSpawnDefaults: spawnSquid(glow) builds a GlowSquid (inherits Squid MAX_HEALTH 10).
func TestGlowSquidSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	s := loop.spawnSquid(8.5, float64(floorY+1), 8.5, true)
	if s.typ != entity.GlowSquid.ID {
		t.Fatalf("glow_squid typ = %d, want entity.GlowSquid.ID %d", s.typ, entity.GlowSquid.ID)
	}
	if got := s.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("glow_squid MAX_HEALTH = %v, want 10.0 (inherits Squid)", got)
	}
}

// TestFishSpawnDefaults: spawnFish builds Cod/Salmon/Pufferfish/TropicalFish, each AbstractFish MAX_HEALTH 3.
func TestFishSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	cases := []struct {
		typ entity.Entity
		id  entity.ID
	}{
		{entity.Cod, entity.Cod.ID},
		{entity.Salmon, entity.Salmon.ID},
		{entity.Pufferfish, entity.Pufferfish.ID},
		{entity.TropicalFish, entity.TropicalFish.ID},
	}
	for _, c := range cases {
		f := loop.spawnFish(c.typ, 8.5, float64(floorY+1), 8.5)
		if f.typ != c.id {
			t.Fatalf("fish typ = %d, want %d", f.typ, c.id)
		}
		if !f.isWaterMob {
			t.Fatalf("fish %d not marked isWaterMob", c.id)
		}
		if math.Abs(float64(f.health)-3.0) > 1e-6 {
			t.Fatalf("fish %d health = %v, want 3.0 (MAX_HEALTH)", c.id, f.health)
		}
		if got := f.getAttributeValue(attribute.MaxHealth); math.Abs(got-3.0) > 1e-9 {
			t.Fatalf("fish %d MAX_HEALTH = %v, want 3.0", c.id, got)
		}
	}
}

// TestPufferfishSpawnDefaults: a fresh Pufferfish is isPufferfish, starts STATE_SMALL.
func TestPufferfishSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	f := loop.spawnFish(entity.Pufferfish, 8.5, float64(floorY+1), 8.5)
	if !f.isPufferfish {
		t.Fatal("pufferfish not marked isPufferfish")
	}
	if f.pufferPuffState != pufferStateSmall {
		t.Fatalf("pufferfish puff = %d, want STATE_SMALL %d", f.pufferPuffState, pufferStateSmall)
	}
}

// TestTropicalFishSpawnDefaults: a fresh TropicalFish carries a rolled TropicalFish.finalizeSpawn variant
// (drawn on level.getRandom()) -- either a COMMON_VARIANTS entry (90%) or a validly-packed rare variant.
// The packed layout must round-trip: base bit high nibble of the pattern word, two DyeColor ids in bytes 2/3.
func TestTropicalFishSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	f := loop.spawnFish(entity.TropicalFish, 8.5, float64(floorY+1), 8.5)
	v := f.tropicalVariant
	// re-pack the decoded fields (pattern word & 0xFFFF, base color id byte 2, pattern color id byte 3) and
	// confirm it equals the stored packed variant (packVariant is a stable encoding).
	patternWord := v & 0xFFFF
	baseID := (v >> 16) & 0xFF
	patID := (v >> 24) & 0xFF
	if got := tropicalPackVariant(patternWord, baseID, patID); got != v {
		t.Fatalf("tropical_fish variant %d does not round-trip through packVariant (got %d)", v, got)
	}
	if baseID > 15 || patID > 15 {
		t.Fatalf("tropical_fish variant %d has out-of-range DyeColor ids base=%d pat=%d", v, baseID, patID)
	}
}

// TestDolphinSpawnDefaults: spawnDolphin builds a Dolphin (Mob + MAX_HEALTH 10 / MOVEMENT_SPEED 1.2 /
// ATTACK_DAMAGE 3), fully moist (MOISTNESS 2400).
func TestDolphinSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	d := loop.spawnDolphin(8.5, float64(floorY+1), 8.5)
	if d.typ != entity.Dolphin.ID {
		t.Fatalf("dolphin typ = %d, want entity.Dolphin.ID %d", d.typ, entity.Dolphin.ID)
	}
	if !d.isWaterMob || !d.isDolphin {
		t.Fatal("dolphin not marked isWaterMob/isDolphin")
	}
	if math.Abs(float64(d.health)-10.0) > 1e-6 {
		t.Fatalf("dolphin health = %v, want 10.0", d.health)
	}
	if got := d.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("dolphin MAX_HEALTH = %v, want 10.0", got)
	}
	if got := d.getAttributeValue(attribute.MovementSpeed); math.Abs(got-1.2000000476837158) > 1e-12 {
		t.Fatalf("dolphin MOVEMENT_SPEED = %v, want 1.2000000476837158", got)
	}
	if got := d.getAttributeValue(attribute.AttackDamage); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("dolphin ATTACK_DAMAGE = %v, want 3.0", got)
	}
	if d.dolphinMoistness != dolphinFullMoistness {
		t.Fatalf("dolphin moistness = %d, want %d", d.dolphinMoistness, dolphinFullMoistness)
	}
}

// TestTadpoleSpawnDefaults: spawnTadpole builds a Tadpole (Animal + MOVEMENT_SPEED 1.0 / MAX_HEALTH 6).
func TestTadpoleSpawnDefaults(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	tp := loop.spawnTadpole(8.5, float64(floorY+1), 8.5)
	if tp.typ != entity.Tadpole.ID {
		t.Fatalf("tadpole typ = %d, want entity.Tadpole.ID %d", tp.typ, entity.Tadpole.ID)
	}
	if !tp.isWaterMob || !tp.isTadpole {
		t.Fatal("tadpole not marked isWaterMob/isTadpole")
	}
	if math.Abs(float64(tp.health)-6.0) > 1e-6 {
		t.Fatalf("tadpole health = %v, want 6.0", tp.health)
	}
	if got := tp.getAttributeValue(attribute.MaxHealth); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("tadpole MAX_HEALTH = %v, want 6.0", got)
	}
	if got := tp.getAttributeValue(attribute.MovementSpeed); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("tadpole MOVEMENT_SPEED = %v, want 1.0", got)
	}
}

// TestTadpoleGrowsToFrog: a Tadpole aged to ticksToBeFrog-1 grows into a Frog on the next tadpoleAiStep
// (Tadpole.setAge fires ageUp at age >= 24000). The tadpole is discarded and a Frog appears in the store.
func TestTadpoleGrowsToFrog(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	tp := loop.spawnTadpole(8.5, float64(floorY+1), 8.5)
	tp.tadpoleAge = tadpoleTicksToBeFrog - 1 // one tick short of growing
	loop.tadpoleAiStep(tp)                   // age++ -> 24000 -> ageUp() -> convertTo(FROG)
	if !tp.dead {
		t.Fatal("tadpole should be discarded after growing into a frog")
	}
	owner := loop.regionForEntity(tp)
	if owner == nil {
		owner = loop.cur()
	}
	foundFrog := false
	for _, e := range owner.entities.byID {
		if e.typ == entity.Frog.ID && e.isFrog {
			foundFrog = true
		}
	}
	if !foundFrog {
		t.Fatal("no Frog spawned after the tadpole grew up")
	}
}

// TestPufferfishPuffState: with a nearby player (a scary entity) the pufferfish inflates 0 -> 1 on the
// first aiStep, then -> 2 once inflateCounter exceeds 40; removing the player deflates it back 2 -> 1 -> 0.
func TestPufferfishPuffState(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	f := loop.spawnFish(entity.Pufferfish, 8.5, float64(floorY+1), 8.5)
	// A scary player within the puff radius (2.35) but OUTSIDE the sting/touch radius (0.65), so the fish
	// inflates without stinging (a bare test player has no Client, so a sting would nil-deref the send).
	addTestPlayer(loop, 7000, 10.0, float64(floorY+1), 8.5) // ~1.5 blocks away

	loop.pufferfishAiStep(f) // PuffGoal start (counter 0->1), tick 0 -> 1 (STATE_MID)
	if f.pufferPuffState != pufferStateMid {
		t.Fatalf("after first puff tick: state = %d, want STATE_MID %d", f.pufferPuffState, pufferStateMid)
	}
	for i := 0; i < 45; i++ {
		loop.pufferfishAiStep(f) // inflateCounter climbs; > 40 with state==1 -> STATE_FULL
	}
	if f.pufferPuffState != pufferStateFull {
		t.Fatalf("after inflateCounter>40: state = %d, want STATE_FULL %d", f.pufferPuffState, pufferStateFull)
	}
	loop.players = loop.players[:0] // remove the scary player -> goal stops, deflate runs
	for i := 0; i < 120; i++ {
		loop.pufferfishAiStep(f)
	}
	if f.pufferPuffState != pufferStateSmall {
		t.Fatalf("after deflateTimer>100: state = %d, want STATE_SMALL %d", f.pufferPuffState, pufferStateSmall)
	}
}

// TestWaterMobDrownsOnLand: a water mob OUT of water drains air (WaterAnimal.handleAirSupply inversion)
// and, once air <= -20, takes drown damage -- the inversion of a land mob. No water block is placed.
func TestWaterMobDrownsOnLand(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	c := loop.spawnFish(entity.Cod, 8.5, float64(floorY+1), 8.5)
	c.airSupply = drowningThreshold + 1 // one tick from the drown threshold (-20)
	before := c.health
	loop.tickWaterMobAirSupply(c) // out of water: air-- -> -20 -> shouldTakeDrowningDamage -> setAir(0), drown 2.0
	if c.airSupply != 0 {
		t.Fatalf("out-of-water drown air = %d, want 0 (setAirSupply(0) on the drown branch)", c.airSupply)
	}
	if c.health >= before {
		t.Fatalf("cod on land should take drown damage: health %v -> %v", before, c.health)
	}
}

// TestWaterMobStaysWetInWater: a submerged water mob resets its air to max (WaterAnimal.handleAirSupply
// else branch) and never drowns.
func TestWaterMobStaysWetInWater(t *testing.T) {
	loop, mgr, floorY := waterMobLoop(t)
	wx, wy, wz := 8, floorY+1, 8
	mgr.SetBlock(pk.Position{X: wx, Y: wy, Z: wz}, waterStateID(0), dimMinY)
	c := loop.spawnFish(entity.Cod, 8.5, float64(wy), 8.5)
	c.airSupply = 0
	before := c.health
	loop.tickWaterMobAirSupply(c) // in water: air -> 300, no drown
	if c.airSupply != maxAirSupply {
		t.Fatalf("in-water air = %d, want %d (reset to max)", c.airSupply, maxAirSupply)
	}
	if c.health != before {
		t.Fatalf("submerged cod should take no drown damage: health %v -> %v", before, c.health)
	}
}
