package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// fall_damage_test.go covers GAMEPLAY-04 (the environmental half): tickFallDamage accumulates a
// player's airborne descent into fallDistance and, on the onGround false->true landing edge,
// applies floor(fallDistance - 3.0) damage through the existing applyDamage flow, then resets.
//
// JAR-VERIFIED FORMULA (javap -c -p from temp/cache/26.2-inner.jar this session):
//   Entity.checkFallDamage          accumulates fallDistance -= deltaY while airborne (deltaY<0)
//                                    and on landing calls fallOn -> causeFallDamage, then resetFallDistance.
//   LivingEntity.calculateFallDamage = Mth.floor(calculateFallPower(d) * damageMul * FALL_DAMAGE_MULTIPLIER)
//   LivingEntity.calculateFallPower  = (fallDistance + 1.0E-6) - SAFE_FALL_DISTANCE   (SAFE_FALL_DISTANCE=3.0)
//   => default-attribute damage = floor(fallDistance - 3.0) HP.

// fallPlayer registers a confirmed player seeded as "standing on the ground at startY" so the
// first tick's bookkeeping (wasOnGround/lastY) is consistent. Reuses combatPlayer (full health,
// capturing client).
func fallPlayer(loop *TickLoop, entityID int32, startY float64) *tickPlayer {
	p := combatPlayer(loop, entityID)
	p.y, p.lastY = startY, startY
	p.onGround, p.wasOnGround = true, true
	return p
}

// step simulates one movement tick the way the live server does it: a movement packet is resolved
// FIRST (applyInput -> doCheckFallDamage with this packet's dy = newY - oldY and its onGround flag,
// the fall-distance accumulation + landing damage), THEN the per-tick Entity.updateFluidInteraction
// water reset runs in tickEntities (tickFallDamage). This mirrors the real ordering: packet handling
// then the entity tick. dy is computed against the pre-move y exactly as subtick.go does.
func step(loop *TickLoop, p *tickPlayer, y float64, onGround bool) {
	dy := y - p.y
	p.y = y
	p.onGround = onGround
	loop.doCheckFallDamage(p, dy, onGround) // per-packet: accumulate + land
	loop.tickFallDamage()                   // per-tick: updateFluidInteraction water reset
}

// TestFallDamageAccumulates: a player descending while airborne accumulates fallDistance equal
// to the total descent.
func TestFallDamageAccumulates(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	// Leave the ground and fall: 100 -> 96 -> 92 -> 88 (12 blocks of descent), still airborne.
	step(loop, p, 96, false)
	step(loop, p, 92, false)
	step(loop, p, 88, false)

	if p.fallDistance != 12 {
		t.Fatalf("fallDistance = %v after a 12-block descent, want 12", p.fallDistance)
	}
}

// TestFallDamageOnLanding: a 10-block fall then a landing applies floor(10-3)=7 damage and
// resets fallDistance to 0.
func TestFallDamageOnLanding(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	// Fall 10 blocks: 100 -> 90 (airborne), then land at 90 (onGround true).
	step(loop, p, 90, false) // descends 10 while airborne
	step(loop, p, 90, true)  // landing edge: onGround false->true at fallDistance 10

	want := maxHealth - 7
	if p.health != want {
		t.Fatalf("after a 10-block fall health = %v, want %v (floor(10-3)=7 damage)", p.health, want)
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after landing, want 0 (reset)", p.fallDistance)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundSetHealth); n != 1 {
		t.Fatalf("landing damage sent %d SetHealth, want 1", n)
	}
}

// TestSmallFallNoDamage: a 2-block fall (< 3.0 safe distance) deals 0 damage on landing.
func TestSmallFallNoDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	step(loop, p, 98, false) // descends 2 while airborne
	step(loop, p, 98, true)  // land

	if p.health != maxHealth {
		t.Fatalf("a 2-block fall changed health to %v, want %v (below safe distance)", p.health, float32(maxHealth))
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after a sub-threshold landing, want 0", p.fallDistance)
	}
}

// TestSpawnAtLowYNoFallDamage pins the join/respawn invariant that the fall-damage baseline (lastY)
// MUST seed to the spawn Y — not the zero value. REGRESSION: gameplay_tick's fresh-join tickPlayer
// left lastY at 0, so the FIRST fall-damage pass computed deltaY = spawnY - 0 (e.g. -46 on a superflat
// floor at y=-46) and the player "fell" its entire world-Y on join, taking lethal fall damage the
// instant it landed — every fresh join / respawn on a low-Y world died on spawn. Here a player seeded
// exactly as the fixed join does it (y == lastY == the deep spawn Y, wasOnGround true) takes ZERO
// damage on its first grounded tick. A player with the OLD buggy init (lastY 0) would take floor(|Y|-3).
func TestSpawnAtLowYNoFallDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const spawnY = -46.0 // a superflat floor spawn (overworldSurfaceY -48 + 2)

	// The join contract (gameplay_tick tickPlayer init): y AND lastY BOTH seed to the spawn Y, and the
	// player spawns standing (wasOnGround true). fallPlayer applies exactly this seeding.
	p := fallPlayer(loop, 1, spawnY)

	// First grounded tick standing still at the spawn Y: deltaY = y - lastY = 0, so no fall distance
	// accumulates and no damage is dealt.
	step(loop, p, spawnY, true)

	if p.health != maxHealth {
		t.Fatalf("fresh spawn at y=%v dealt fall damage (health %v, want %v) — lastY not seeded to spawn Y", spawnY, p.health, float32(maxHealth))
	}
	if p.fallDistance != 0 {
		t.Fatalf("fresh spawn accumulated fallDistance %v, want 0 — the lastY-baseline regression", p.fallDistance)
	}
}

// TestStayGroundedNoDamage: a player on the ground every tick accumulates nothing and takes no
// damage.
func TestStayGroundedNoDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 64)

	for i := 0; i < 5; i++ {
		step(loop, p, 64, true)
	}

	if p.fallDistance != 0 {
		t.Fatalf("grounded player accumulated fallDistance = %v, want 0", p.fallDistance)
	}
	if p.health != maxHealth {
		t.Fatalf("grounded player took damage, health = %v", p.health)
	}
}

// TestThreeBlockFallNoDamage: a fall of exactly the safe distance (3 blocks) deals 0 damage.
// calculateFallPower(3.0) = 3 + 1e-6 - 3 = 1e-6 -> floor(1e-6) = 0.
func TestThreeBlockFallNoDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)
	step(loop, p, 97, false) // descend 3 while airborne
	step(loop, p, 97, true)  // land at fallDistance 3
	if p.health != maxHealth {
		t.Fatalf("a 3-block fall dealt damage (health %v, want %v); floor(3-3)=0", p.health, float32(maxHealth))
	}
}

// TestFourBlockFallOneDamage: a 4-block fall deals floor(4-3)=1 damage on landing.
func TestFourBlockFallOneDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)
	step(loop, p, 96, false) // descend 4 while airborne
	step(loop, p, 96, true)  // land at fallDistance 4
	if want := maxHealth - 1; p.health != want {
		t.Fatalf("a 4-block fall health = %v, want %v (floor(4-3)=1)", p.health, want)
	}
}

// TestSteppingDownLedgesNoAccumulation is the headline regression for the per-packet fix. A player
// walks down a staircase: several movement packets arrive IN ONE TICK, each a small (1-block) drop
// that ends GROUNDED. Vanilla runs checkFallDamage per packet, so every grounded packet resets
// fallDistance and the descent never accumulates into a damaging fall. The old per-TICK collapse
// saw only the LAST packet's onGround over the net multi-block drop and applied spurious damage
// ("too sensitive") a tick late ("arrives late"). Driving doCheckFallDamage per packet (as applyInput
// now does) must deal ZERO damage.
func TestSteppingDownLedgesNoAccumulation(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	// FIVE packets in a single tick, each: drop 1 block and land grounded (a stair step). Each grounded
	// packet resets fallDistance, so it can never build toward the 3-block safe threshold.
	y := 100.0
	for i := 0; i < 5; i++ {
		ny := y - 1.0
		p.y = ny
		p.onGround = true
		loop.doCheckFallDamage(p, ny-y, true) // per-packet: drop 1, grounded -> reset
		y = ny
		if p.fallDistance != 0 {
			t.Fatalf("step %d left fallDistance = %v, want 0 (grounded packet must reset)", i, p.fallDistance)
		}
	}
	// The per-tick water-reset pass then runs; still no damage.
	loop.tickFallDamage()
	if p.health != maxHealth {
		t.Fatalf("walking down 5 one-block steps in one tick dealt %v damage (health %v); per-packet resets were lost",
			float32(maxHealth)-p.health, p.health)
	}
}

// TestHopThenFallNoCarryover proves the intermediate grounded reset is honored across packets in a
// tick: the player makes a harmless 2-block hop that ENDS grounded, then in the SAME tick begins a
// real fall. The grounded hop packet must reset the 2 blocks so the later fall is measured from the
// hop's landing, not summed with it. A 2-block hop + a 4-block fall must deal floor(4-3)=1, NOT
// floor(6-3)=3.
func TestHopThenFallNoCarryover(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	// Packet A: a 2-block downward hop that lands grounded -> accumulates 2, then resets on the same
	// packet's onGround branch (net fallDistance 0).
	p.y = 98
	p.onGround = true
	loop.doCheckFallDamage(p, -2, true)
	if p.fallDistance != 0 {
		t.Fatalf("grounded hop left fallDistance = %v, want 0", p.fallDistance)
	}
	// Packet B: airborne, drop 4 blocks.
	p.y = 94
	p.onGround = false
	loop.doCheckFallDamage(p, -4, false)
	// Packet C: land.
	p.y = 94
	p.onGround = true
	loop.doCheckFallDamage(p, 0, true)
	if want := maxHealth - 1; p.health != want {
		t.Fatalf("hop(2)+fall(4) health = %v, want %v (floor(4-3)=1, the hop must NOT carry over)", p.health, want)
	}
}

// TestCalculateFallDamageMatchesVanillaFormula pins the literal vanilla product against the
// jar-verified expression Mth.floor((d + 1e-6 - 3.0) * mul * 1.0). This guards against anyone
// re-collapsing or re-paraphrasing the formula: calculateFallDamage MUST equal the explicit
// float-floor of (calculateFallPower(d) * damageMultiplier * FALL_DAMAGE_MULTIPLIER).
func TestCalculateFallDamageMatchesVanillaFormula(t *testing.T) {
	cases := []struct {
		d   float64
		mul float64
	}{
		{0, 1.0}, {2.0, 1.0}, {3.0, 1.0}, {3.5, 1.0}, {10.0, 1.0},
		{12.0, 1.0}, {23.0, 1.0}, {255.0, 1.0}, {10.0, 0.5},
	}
	for _, c := range cases {
		// The literal vanilla formula, written out independently of the implementation:
		// LivingEntity.calculateFallDamage -> Mth.floor(calculateFallPower(d) * mul * FALL_DAMAGE_MULTIPLIER),
		// calculateFallPower(d) = (d + 1.0E-6) - SAFE_FALL_DISTANCE(=3.0), FALL_DAMAGE_MULTIPLIER=1.0.
		want := int(math.Floor((c.d + 1.0e-6 - 3.0) * c.mul * 1.0))
		if got := calculateFallDamage(c.d, c.mul, 3.0); got != want {
			t.Fatalf("calculateFallDamage(%v, %v, 3.0) = %d, want %d (floor((d+1e-6-3.0)*mul*1.0))",
				c.d, c.mul, got, want)
		}
	}
}

// TestCheckFallDamageFloatCast pins the (float) narrowing cast in Entity.checkFallDamage:
// fallDistance -= (double)(float) deltaY. A deltaY that is NOT exactly representable as a float32
// must accumulate the float32-rounded magnitude, not the raw float64 — proving the d2f/f2d cast is
// present. We pick deltaY = -0.1 (0.1 is not exactly representable; float32(0.1) != float64(0.1)).
func TestCheckFallDamageFloatCast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	defer loop.Close() // release the async pools so this test does not leak pathfinding workers
	p := fallPlayer(loop, 1, 100)

	// Airborne, dry, descending by exactly 0.1: deltaY = -0.1. With the (float) cast, fallDistance
	// accumulates float64(float32(0.1)), NOT 0.1.
	p.onGround, p.wasOnGround = false, false
	p.y = 100 - 0.1
	loop.doCheckFallDamage(p, -0.1, false) // one airborne packet descending 0.1 (dy = ny - oldY)

	wantCast := float64(float32(0.1)) // exactly what `fallDistance -= (float)deltaY` adds
	if p.fallDistance != wantCast {
		t.Fatalf("fallDistance = %v after a 0.1 drop, want %v (float32-cast magnitude); "+
			"a raw float64 0.1 would be %v — the (float) cast is missing", p.fallDistance, wantCast, 0.1)
	}
	// And it must NOT equal the un-cast float64 value (the cast genuinely changes the bits).
	if p.fallDistance == 0.1 {
		t.Fatalf("fallDistance == raw float64 0.1: the (float) narrowing cast was skipped")
	}
}

// --- 17-08 WATER GUARD ---
//
// Vanilla (Entity.checkFallDamage + Entity.updateFluidInteraction, javap-verified this session)
// negates ALL fall damage in water: descent in water accumulates no fall distance, and touching
// water resetFallDistance()s any distance built up before entering. These tests pin both halves.

// waterFallLoop wires a fluid world (one ready, all-air chunk at column {0,0}) with a water
// source column, and registers a combatPlayer (full health + capturing client) positioned over
// that column. The player's x/z (8.5, 8.5) sit inside the {8,*,8} block so playerInWater is true
// once its feet reach the water at y=64.
func waterFallLoop(t *testing.T) (*TickLoop, *world.ChunkManager, *tickPlayer) {
	t.Helper()
	loop, mgr := newFluidLoop()
	// A 3-block-deep water column at x=8,z=8 from y=64 up to y=66 (surface block top at y=67) so a
	// player with feet at y>=67 is clearly above the pool and a player with feet at y<=66 is in it.
	for y := 64; y <= 66; y++ {
		setWater(mgr, pk.Position{X: 8, Y: y, Z: 8}, 0)
	}
	p := combatPlayer(loop, 1)
	p.x, p.z = 8.5, 8.5
	p.y, p.lastY = 80, 80
	p.onGround, p.wasOnGround = false, false // already airborne above the pool
	return loop, mgr, p
}

// TestFallIntoWaterNoDamage: a long fall that ends in water deals 0 damage — the headline 17-08
// fix. Without the water guard the player would take floor(13-3)=10 damage on the water floor.
func TestFallIntoWaterNoDamage(t *testing.T) {
	loop, _, p := waterFallLoop(t)

	// Fall from y=80 down to y=67 through air (13 blocks of descent, feet still ABOVE the water
	// surface at y=67), then continue down to y=64 INSIDE the water column, then "land" on the
	// submerged floor at y=64.
	step(loop, p, 67, false) // descends 13 in air -> fallDistance 13 (dry above the pool)
	if p.fallDistance == 0 {
		t.Fatalf("dry airborne descent did not accumulate fallDistance (got 0)")
	}
	step(loop, p, 64, false) // now feet at y=64: in water -> reset, no accumulation
	if p.fallDistance != 0 {
		t.Fatalf("entering water did not reset fallDistance, got %v (want 0)", p.fallDistance)
	}
	step(loop, p, 64, true) // landing edge while submerged: must deal 0 damage

	if p.health != maxHealth {
		t.Fatalf("fall into water dealt damage: health = %v, want %v (vanilla negates fall damage in water)", p.health, float32(maxHealth))
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after landing in water, want 0", p.fallDistance)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundSetHealth); n != 0 {
		t.Fatalf("fall into water sent %d SetHealth, want 0 (no damage)", n)
	}
}

// TestDescentInWaterNoAccumulation: a player sinking entirely within water never accumulates fall
// distance (Entity.checkFallDamage's `!isInWater()` accumulation guard).
func TestDescentInWaterNoAccumulation(t *testing.T) {
	loop, _, p := waterFallLoop(t)
	// Start the player already submerged (feet at y=66, inside the 64..67 water column).
	p.y, p.lastY = 66, 66

	// Sink within the water: 66 -> 65 -> 64. Every tick playerInWater is true, so no accumulation.
	step(loop, p, 65, false)
	step(loop, p, 64, false)

	if p.fallDistance != 0 {
		t.Fatalf("descent within water accumulated fallDistance = %v, want 0", p.fallDistance)
	}
	step(loop, p, 64, true) // surface to a submerged floor: still 0 damage
	if p.health != maxHealth {
		t.Fatalf("sinking-in-water player took damage: health = %v, want %v", p.health, float32(maxHealth))
	}
}

// TestBatchedFallIntoWaterNoDamage is the regression the adversarial verify caught: when several
// movement packets are BATCHED into a single server tick (resolveSubtickInputs drains them ALL, THEN
// tickFallDamage runs ONCE), a fall into water must still deal 0 damage. The old code reset fall
// distance only in the once-per-tick tickFallDamage, so the submerged-landing packet in the same
// batch fired floor(13-3)=10 damage before the reset ran. The fix runs the water reset PER PACKET
// inside doCheckFallDamage (vanilla LivingEntity.checkFallDamage's `if(!isInWater())
// updateFluidInteraction()`), so a submerged packet clears the distance before any same-tick landing.
// This test drives doCheckFallDamage directly for each packet WITHOUT an interleaved tickFallDamage,
// exactly as the batched tick does — the harness gap the verify flagged in the step() helper.
func TestBatchedFallIntoWaterNoDamage(t *testing.T) {
	loop, _, p := waterFallLoop(t)

	// One tick's batch: air descent 80->67 (13 dry), then 67->64 into the water, then the submerged
	// landing edge — all as consecutive per-packet doCheckFallDamage calls, NO tickFallDamage between.
	packet := func(y float64, onGround bool) {
		dy := y - p.y
		p.y = y
		p.onGround = onGround
		loop.doCheckFallDamage(p, dy, onGround)
	}
	packet(67, false) // dry air: accumulate ~13
	packet(64, false) // enters water: per-packet reset -> 0
	packet(64, true)  // submerged landing in the SAME batch: must deal 0 (reset already ran this packet)

	if p.health != maxHealth {
		t.Fatalf("batched fall into water dealt damage: health = %v, want %v (per-packet water reset must fire before the same-tick landing)", p.health, float32(maxHealth))
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after batched water landing, want 0", p.fallDistance)
	}
}

// TestFallLandingEmitsHitGroundForSculkSensor pins the Entity.checkFallDamage -> Level.gameEvent(
// HIT_GROUND, ..., Context.of(entity, landingState)) seam: every positive-fall landing schedules a
// geHitGround vibration candidate on a sculk sensor in range, with the landing player as the source
// and the hit_ground frequency (2) the comparator reads. The setup mirrors an entity landing on
// stone near (2 blocks east of) an INACTIVE sculk sensor: stone at (8, 64, 8) is the landing surface
// (NOT a DAMPENS_VIBRATIONS cell, so the sensor's isValidVibration accepts it), the sensor at
// (10, 64, 8) is in INACTIVE phase (the canActivate gate), and the empty-air path between keeps
// vibrationOccluded's six-ray scan clear. CITE Entity.checkFallDamage / Level.gameEvent(
// GameEvent.HIT_GROUND, ...) / vibration_block.go walkBlockVibrationListeners.
func TestFallLandingEmitsHitGroundForSculkSensor(t *testing.T) {
	loop, mgr := newSculkLoop()

	// Landing surface: stone at (8, 64, 8). Stone is NOT in #minecraft:dampens_vibrations, so the
	// sensor's isValidVibration (tag + affectedState DAMPENS reject) accepts the Context.state.
	mgr.SetBlock(pk.Position{X: 8, Y: 64, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)
	// Resolved sculk sensor 2 blocks east, in INACTIVE phase (canActivate = getPhase == INACTIVE).
	sensorPos := pk.Position{X: 10, Y: 64, Z: 8}
	mgr.SetBlock(sensorPos, sculkSensorState(), dimMinY)
	loop.resolveSculkSensor(sensorPos)

	// A player mid-fall at (8.5, 65, 8.5): feet just above the landing surface, 2 blocks west of
	// the sensor (sensor center 10.5,64.5,8.5; player 8.5,65,8.5 -- distSqr 4.25 <= 8^2, in range).
	const playerID int32 = 4096
	p := fallPlayer(loop, playerID, 100)
	p.x, p.z = 8.5, 8.5
	p.y = 65
	p.fallDistance = 5 // five blocks of airborne descent before the landing edge
	p.onGround = true

	loop.checkFallDamage(p, -5, true, false) // landing edge (no water)

	// The sensor must have a HIT_GROUND candidate scheduled, with the player as source.
	be := loop.sculkSensors[sensorPos]
	if be == nil {
		t.Fatal("sensor BE missing after a landing edge")
	}
	if be.vibration == nil || !be.vibration.hasCandidate {
		t.Fatal("HIT_GROUND landing did not schedule a vibration candidate on the nearby sculk sensor")
	}
	if be.vibration.candEvent != geHitGround {
		t.Fatalf("scheduled event = %q, want %q", be.vibration.candEvent, geHitGround)
	}
	if be.vibration.candSourceID != playerID {
		t.Fatalf("source id = %d, want %d (the landing player)", be.vibration.candSourceID, playerID)
	}
	// GameEvent.HIT_GROUND's vibration frequency (VibrationSystem.VIBRATION_FREQUENCY_FOR_EVENT
	// [hit_ground]) is 2 -- the value a comparator reads off the activated sensor.
	if f := vibrationFrequencyOf(sculkGameEvent(be.vibration.candEvent)); f != 2 {
		t.Fatalf("HIT_GROUND frequency = %d, want 2 (vibrationFrequencyTable[hit_ground])", f)
	}
	// And the landing-side state read happened exactly once: the candidate's affected-context
	// cannot be inspected here (vibration_data carries no state), but the multiplier dispatch
	// received the same stone id -- a real Entity.fallOn default of 1.0x (stone is not hay/slime/bed).
}
