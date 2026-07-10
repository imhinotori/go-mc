package server

// bed_explode_test.go -- the nether/end BED-EXPLOSION branch of BedBlock.useWithoutItem (bed_explode.go).
// Behavior verified against temp/cache/26.2-inner.jar this session: BedRule.EXPLODES in the_nether /
// the_end, CAN_SLEEP_WHEN_DARK (no explode) in the overworld; the explode removes the bed halves +
// detonates a radius-5.0 fire BLOCK explosion that hurts the clicker instead of sleeping.

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestBedRuleForDimensions locks the dimension -> BedRule.explodes mapping: overworld false (do not
// explode -> sleep path), nether + end true (EXPLODES). Cite EnvironmentAttributes.BED_RULE data.
func TestBedRuleForDimensions(t *testing.T) {
	if bedRuleFor(dimOverworld).explodes {
		t.Fatal("overworld BedRule.explodes = true, want false (CAN_SLEEP_WHEN_DARK)")
	}
	if !bedRuleFor(dimNether).explodes {
		t.Fatal("nether BedRule.explodes = false, want true (EXPLODES)")
	}
	if !bedRuleFor(dimEnd).explodes {
		t.Fatal("end BedRule.explodes = false, want true (EXPLODES)")
	}
}

// TestBedExplodesInNether: a player in the nether right-clicking a bed does NOT sleep -- the bed HEAD is
// removed (BedBlock.useWithoutItem removeBlock) and the player is hurt by the radius-5.0 explosion. Cite
// BedBlock.useWithoutItem explode branch.
func TestBedExplodesInNether(t *testing.T) {
	loop, mgr := newFluidLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(12345)

	// A player standing right next to the bed, in the nether.
	p := &tickPlayer{entityID: 7, x: 9.5, y: 64, z: 8.5, health: maxHealth, lastPoseSent: -1, dimension: dimNether, client: captureClient(256)}
	loop.players = append(loop.players, p)

	// A solid floor under the bed so createFire has a support (and the bed sits on the ground).
	for x := 6; x <= 12; x++ {
		for z := 5; z <= 11; z++ {
			mgr.SetBlock(pk.Position{X: x, Y: 63, Z: z}, block.ToStateID[block.Stone{}], dimMinY)
		}
	}
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	var consumed bool
	loop.withRegion(loop.only(), func() {
		consumed = loop.useBed(p, bedPos)
	})

	if !consumed {
		t.Fatal("useBed on a nether bed returned false, want true (SUCCESS_SERVER, action consumed)")
	}
	if p.isSleeping() {
		t.Fatal("player is sleeping after a nether bed click, want an EXPLODE (no sleep)")
	}
	// The bed HEAD must be removed (removeBlock -> air).
	if s, _ := mgr.GetBlock(bedPos, dimMinY); isBedBlock(s) {
		t.Fatalf("bed HEAD still present after the nether explosion (state %d), want air", s)
	}
	// The clicker (adjacent to the blast) must have taken explosion damage.
	if p.health >= maxHealth {
		t.Fatalf("player health = %.2f after standing in a nether bed explosion, want < %.2f", p.health, maxHealth)
	}
}

// TestBedExplodeRemovesBothHalves: the explode branch removes BOTH the HEAD and the FOOT (the FOOT one
// step back along FACING.getOpposite() when it is the same bed). Cite BedBlock.useWithoutItem (removeBlock
// pos; if foot.is(this) removeBlock foot).
func TestBedExplodeRemovesBothHalves(t *testing.T) {
	loop, mgr := newFluidLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(999)

	p := &tickPlayer{entityID: 7, x: 9.5, y: 64, z: 8.5, health: maxHealth, lastPoseSent: -1, dimension: dimEnd, client: captureClient(256)}
	loop.players = append(loop.players, p)

	// HEAD facing north: FOOT is one step SOUTH (FACING.getOpposite of north). Place both bed halves.
	headPos := pk.Position{X: 9, Y: 64, Z: 8}
	footPos := pk.Position{X: 9, Y: 64, Z: 9} // north opposite == +z (south)
	headState, ok := block.ToStateID[block.RedBed{Facing: block.North, Part: block.BedPartHead}]
	if !ok {
		t.Fatal("red_bed north/head state missing")
	}
	footState, ok := block.ToStateID[block.RedBed{Facing: block.North, Part: block.BedPartFoot}]
	if !ok {
		t.Fatal("red_bed north/foot state missing")
	}
	mgr.SetBlock(headPos, headState, dimMinY)
	mgr.SetBlock(footPos, footState, dimMinY)

	loop.withRegion(loop.only(), func() {
		// Click the HEAD directly (PART == HEAD, no FOOT->HEAD hop).
		loop.useBed(p, headPos)
	})

	if s, _ := mgr.GetBlock(headPos, dimMinY); isBedBlock(s) {
		t.Fatalf("HEAD still a bed after the end explosion (state %d), want removed", s)
	}
	if s, _ := mgr.GetBlock(footPos, dimMinY); isBedBlock(s) {
		t.Fatalf("FOOT still a bed after the end explosion (state %d), want removed", s)
	}
}

// TestBedDoesNotExplodeInOverworld: an overworld player clicking a valid bed at night SLEEPS (no explode)
// -- the bed survives (OCCUPIED set) and the player is unharmed. This is the negative control proving the
// explode branch is dimension-gated. Cite BedRule.CAN_SLEEP_WHEN_DARK (explodes false).
func TestBedDoesNotExplodeInOverworld(t *testing.T) {
	loop, mgr := newFluidLoop()
	loop.gametime = 15000 // night: BedRule.canSleep (isDarkEnoughToSpawn) true
	loop.start(loop.clock.(*fakeClock).Now())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(1)

	p := &tickPlayer{entityID: 7, x: 9.5, y: 64, z: 8.5, health: maxHealth, lastPoseSent: -1, dimension: dimOverworld, client: captureClient(256)}
	loop.players = append(loop.players, p)

	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	loop.withRegion(loop.only(), func() {
		loop.useBed(p, bedPos)
	})

	if !p.isSleeping() {
		t.Fatal("overworld night bed click did not put the player to sleep (unexpected explode?)")
	}
	if p.health < maxHealth {
		t.Fatalf("player took %.2f damage sleeping in an overworld bed, want none", maxHealth-p.health)
	}
	// The bed survives and is OCCUPIED.
	s, _ := mgr.GetBlock(bedPos, dimMinY)
	bp, ok := readBed(s)
	if !ok {
		t.Fatalf("overworld bed removed after a click (state %d), want intact + OCCUPIED", s)
	}
	if !bp.occupied {
		t.Fatal("overworld bed not OCCUPIED after the player slept")
	}
}
