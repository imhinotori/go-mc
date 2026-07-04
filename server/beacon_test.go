package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// beacon_test.go — BEACON block-entity validation gates. Each asserts the ported behaviour against the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, net.minecraft.world.level.block.entity.BeaconBlockEntity):
//   - a beacon on a full 3x3 iron base (level 1) with sky access applies its primary effect (speed) to a
//     player within (level*10+10) blocks every 80 ticks, with duration (9+level*2)*20;
//   - a beacon whose beam is OBSTRUCTED (a solid block above it) applies NOTHING;
//   - a player OUTSIDE (level*10+10) blocks receives nothing;
//   - a full 4-level pyramid enables the secondary/regen (level>=4 && secondary!=primary).

// newBeaconLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container for the
// origin column. Mirrors newHopperLoop. gametime starts at 0 so the first tick is an 80-tick apply boundary.
func newBeaconLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	for _, cp := range []level.ChunkPos{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}, {1, 0}, {0, 1}} {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(cp, ch)
	}
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

func ironBlockState() block.StateID   { return block.DefaultStateID["minecraft:iron_block"] }
func beaconBlockState() block.StateID { return block.DefaultStateID["minecraft:beacon"] }
func stoneBlockState() block.StateID  { return block.DefaultStateID["minecraft:stone"] }

// placeBeaconWithBase places the beacon at pos and a full (2*step+1)² base ring at each layer 1..layers below
// it, all iron_block. A `layers`-deep valid pyramid yields updateBase == layers.
func placeBeaconWithBase(mgr *world.ChunkManager, pos pk.Position, layers int) {
	mgr.SetBlock(pos, beaconBlockState(), dimMinY)
	iron := ironBlockState()
	for step := 1; step <= layers; step++ {
		ly := pos.Y - step
		for lx := pos.X - step; lx <= pos.X+step; lx++ {
			for lz := pos.Z - step; lz <= pos.Z+step; lz++ {
				mgr.SetBlock(pk.Position{X: lx, Y: ly, Z: lz}, iron, dimMinY)
			}
		}
	}
}

// TestBeaconUpdateBaseLevel1 locks BeaconBlockEntity.updateBase: a full 3x3 iron ring one block below the
// beacon yields level 1.
func TestBeaconUpdateBaseLevel1(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 1)
	if got := loop.beaconUpdateBase(pos.X, pos.Y, pos.Z); got != 1 {
		t.Fatalf("updateBase on a full 3x3 iron base = %d, want 1", got)
	}
}

// TestBeaconUpdateBaseIncompleteRing: a single missing base block drops the level to 0 (the ring must be
// ENTIRELY #beacon_base_blocks). CITE updateBase's isOk break.
func TestBeaconUpdateBaseIncompleteRing(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 1)
	// Knock out one corner of the layer-1 ring.
	mgr.SetBlock(pk.Position{X: pos.X - 1, Y: pos.Y - 1, Z: pos.Z - 1}, block.DefaultStateID["minecraft:air"], dimMinY)
	if got := loop.beaconUpdateBase(pos.X, pos.Y, pos.Z); got != 0 {
		t.Fatalf("updateBase with a gap in the layer-1 ring = %d, want 0", got)
	}
}

// TestBeaconAppliesPrimaryEffect is the load-bearing gate: a level-1 beacon with sky access + a primary of
// SPEED applies speed to a player within (1*10+10)==20 blocks on an 80-tick boundary, with duration
// (9+1*2)*20 == 220 ticks and amplifier 0.
func TestBeaconAppliesPrimaryEffect(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 1)

	b := loop.resolveBeacon(pos)
	b.primaryPower = effectSpeed

	// A player 5 blocks away (well within the 20-block radius).
	p := &tickPlayer{x: 5, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)

	// Prime the beam scan so beamClear is committed (needs enough ticks to scan the short column above the
	// beacon up to the surface). Run several ticks with gametime not on an 80-boundary to avoid an early
	// apply before the beam is confirmed.
	loop.gametime = 1
	for i := 0; i < 20; i++ {
		loop.tickBeacons()
		loop.gametime++
	}
	if !b.beamClear {
		t.Fatalf("beam did not commit clear over an unobstructed column (beamClear=false)")
	}
	if p.activeEffects[effectSpeed] != nil {
		t.Fatalf("effect applied before an 80-tick boundary")
	}

	// Land exactly on an 80-tick boundary and tick once: applyEffects runs.
	loop.gametime = 80
	loop.tickBeacons()

	e := p.activeEffects[effectSpeed]
	if e == nil {
		t.Fatalf("no speed effect applied to an in-range player by a level-1 beacon")
	}
	if e.amplifier != 0 {
		t.Fatalf("speed amplifier = %d, want 0 (level 1)", e.amplifier)
	}
	// duration = (9 + level*2) * 20 = (9+2)*20 = 220.
	if e.duration != (9+1*2)*20 {
		t.Fatalf("speed duration = %d, want %d ((9+level*2)*20)", e.duration, (9+1*2)*20)
	}
}

// TestBeaconObstructedBeamAppliesNothing: a solid stone block directly above the beacon obstructs the beam
// (beamSections cleared) so no effect is applied even on an 80-tick boundary.
func TestBeaconObstructedBeamAppliesNothing(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 1)
	// Obstruct the beam: a stone block right above the beacon.
	mgr.SetBlock(pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}, stoneBlockState(), dimMinY)

	b := loop.resolveBeacon(pos)
	b.primaryPower = effectSpeed

	p := &tickPlayer{x: 3, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)

	loop.gametime = 1
	for i := 0; i < 20; i++ {
		loop.tickBeacons()
		loop.gametime++
	}
	if b.beamClear {
		t.Fatalf("obstructed beam committed clear (beamClear=true); want false")
	}
	loop.gametime = 80
	loop.tickBeacons()
	if p.activeEffects[effectSpeed] != nil {
		t.Fatalf("an obstructed beacon applied an effect; want none")
	}
}

// TestBeaconOutOfRangeNoEffect: a player OUTSIDE (level*10+10)==20 blocks receives nothing.
func TestBeaconOutOfRangeNoEffect(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 1)

	b := loop.resolveBeacon(pos)
	b.primaryPower = effectSpeed

	// 40 blocks away — outside the 20-block radius.
	p := &tickPlayer{x: 40, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)

	loop.gametime = 1
	for i := 0; i < 20; i++ {
		loop.tickBeacons()
		loop.gametime++
	}
	loop.gametime = 80
	loop.tickBeacons()
	if p.activeEffects[effectSpeed] != nil {
		t.Fatalf("a beacon applied an effect to a player 40 blocks away (radius 20); want none")
	}
}

// TestBeaconLevel4Secondary: a full 4-level pyramid (level 4) with primary=SPEED + secondary=REGENERATION
// applies BOTH effects (level>=4 && secondary!=primary). The primary duration/amplifier follow (9+4*2)*20 and
// amp 0; the secondary regeneration is applied at amp 0.
func TestBeaconLevel4Secondary(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 4)

	if got := loop.beaconUpdateBase(pos.X, pos.Y, pos.Z); got != 4 {
		t.Fatalf("updateBase on a full 4-level iron pyramid = %d, want 4", got)
	}

	b := loop.resolveBeacon(pos)
	b.primaryPower = effectSpeed
	b.secondaryPower = effectRegeneration

	p := &tickPlayer{x: 2, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)

	loop.gametime = 1
	for i := 0; i < 20; i++ {
		loop.tickBeacons()
		loop.gametime++
	}
	loop.gametime = 80
	loop.tickBeacons()

	sp := p.activeEffects[effectSpeed]
	if sp == nil {
		t.Fatalf("level-4 beacon did not apply the primary (speed)")
	}
	if sp.duration != (9+4*2)*20 {
		t.Fatalf("level-4 primary duration = %d, want %d", sp.duration, (9+4*2)*20)
	}
	rg := p.activeEffects[effectRegeneration]
	if rg == nil {
		t.Fatalf("level-4 beacon did not apply the secondary (regeneration)")
	}
	if rg.amplifier != 0 {
		t.Fatalf("secondary regeneration amplifier = %d, want 0", rg.amplifier)
	}
}

// TestBeaconValidateEffects locks BeaconBlockEntity.validateEffects: a secondary requires level 4; a primary
// can never be the level-4 (regen) slot; primary==secondary is a valid level-4 upgrade.
func TestBeaconValidateEffects(t *testing.T) {
	// speed (level 1) as primary, no secondary, level 1: valid.
	if !beaconValidateEffects(effectSpeed, "", 1) {
		t.Fatal("speed primary at level 1 should validate")
	}
	// speed primary + regeneration secondary at level 3: invalid (secondary needs level 4).
	if beaconValidateEffects(effectSpeed, effectRegeneration, 3) {
		t.Fatal("secondary at level 3 must be rejected (needs level 4)")
	}
	// speed primary + regeneration secondary at level 4: valid.
	if !beaconValidateEffects(effectSpeed, effectRegeneration, 4) {
		t.Fatal("speed + regeneration at level 4 should validate")
	}
	// regeneration as PRIMARY is never allowed (primaryLevel >= 4 -> false).
	if beaconValidateEffects(effectRegeneration, "", 4) {
		t.Fatal("regeneration must never be a valid primary")
	}
	// strength (level 3) primary at level 2: invalid (primaryLevel > levels).
	if beaconValidateEffects(effectStrength, "", 2) {
		t.Fatal("strength primary at level 2 must be rejected (requires level 3)")
	}
	// speed primary + speed secondary at level 4: valid (the amplifier-upgrade case).
	if !beaconValidateEffects(effectSpeed, effectSpeed, 4) {
		t.Fatal("speed + speed at level 4 (upgrade) should validate")
	}
}

// TestBeaconLevel4PrimaryEqualsSecondaryUpgrade: level 4 with primary==secondary applies the primary at
// amplifier 1 (the "level II" upgrade), and only one effect.
func TestBeaconLevel4PrimaryEqualsSecondaryUpgrade(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	placeBeaconWithBase(mgr, pos, 4)

	b := loop.resolveBeacon(pos)
	b.primaryPower = effectSpeed
	b.secondaryPower = effectSpeed // primary == secondary at level 4 -> amplifier 1

	p := &tickPlayer{x: 1, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)

	loop.gametime = 1
	for i := 0; i < 20; i++ {
		loop.tickBeacons()
		loop.gametime++
	}
	loop.gametime = 80
	loop.tickBeacons()

	sp := p.activeEffects[effectSpeed]
	if sp == nil {
		t.Fatalf("level-4 upgrade beacon did not apply speed")
	}
	if sp.amplifier != 1 {
		t.Fatalf("level-4 primary==secondary speed amplifier = %d, want 1 (upgrade)", sp.amplifier)
	}
}

// TestBeaconEncodeDecodeEffect locks BeaconMenu.encodeEffect/decodeEffect round-trip: null <-> 0, and a real
// effect <-> its MOB_EFFECT registry id + 1.
func TestBeaconEncodeDecodeEffect(t *testing.T) {
	if beaconEncodeEffect("") != 0 {
		t.Fatal("encodeEffect(null) must be 0")
	}
	if beaconDecodeEffect(0) != "" {
		t.Fatal("decodeEffect(0) must be null")
	}
	enc := beaconEncodeEffect(effectSpeed)
	if enc == 0 {
		t.Fatal("encodeEffect(speed) must be non-zero")
	}
	if beaconDecodeEffect(enc) != effectSpeed {
		t.Fatalf("decodeEffect(encodeEffect(speed)) = %q, want %q", beaconDecodeEffect(enc), effectSpeed)
	}
}

// TestBeaconTickDropsBrokenBE: tickBeacons drops a beaconBE whose block is no longer a beacon (broken).
func TestBeaconTickDropsBrokenBE(t *testing.T) {
	loop, mgr := newBeaconLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	mgr.SetBlock(pos, beaconBlockState(), dimMinY)
	loop.resolveBeacon(pos)
	// Break the beacon (replace with air).
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:air"], dimMinY)
	loop.tickBeacons()
	if _, ok := loop.beacons[pos]; ok {
		t.Fatalf("tickBeacons kept a beaconBE whose block was broken")
	}
	_ = component.SlotData{}
}
