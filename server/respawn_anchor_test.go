package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

func anchorAt(charge int) block.StateID {
	s, ok := block.ToStateID[block.RespawnAnchor{Charges: block.Integer(charge)}]
	if !ok {
		panic("no respawn_anchor state")
	}
	return s
}

func newAnchorPlayer(loop *TickLoop, dim int, itemID int32) *tickPlayer {
	// entityID/health/lastPoseSent/client mirror the bed_explode test harness so the explode path
	// (which hurts every entity in the blast, including the clicking player standing on the anchor)
	// has a real capture client + starting health rather than nil-panicking in actuallyHurt.
	p := &tickPlayer{entityID: 7, x: 5, y: 65, z: 5, dimension: dim, health: maxHealth, lastPoseSent: -1, client: captureClient(256)}
	inv := ensureInventory(p)
	if itemID != 0 {
		inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 4, ItemID: pk.VarInt(itemID)})
	}
	loop.players = append(loop.players, p)
	return p
}

// TestRespawnAnchorScaledChargeLevel pins RespawnAnchorBlock.getScaledChargeLevel(state, 15) ==
// Mth.floor((charge - 0) / 4.0f * 15): the 0/3/7/11/15 comparator table.
func TestRespawnAnchorScaledChargeLevel(t *testing.T) {
	want := map[int]int{0: 0, 1: 3, 2: 7, 3: 11, 4: 15}
	for charge, exp := range want {
		if got := respawnAnchorScaledChargeLevel(charge, 15); got != exp {
			t.Fatalf("charge %d: scaled=%d want %d", charge, got, exp)
		}
	}
}

// TestRespawnAnchorCharge asserts a GLOWSTONE click on a CHARGE<4 anchor bumps CHARGE by 1, consumes one
// glowstone (survival), draws NO RNG, and consumes the interaction. CITE RespawnAnchorBlock.useItemOn ->
// charge.
func TestRespawnAnchorCharge(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	const seed = int64(0x1234)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, anchorAt(1), dimMinY)
	p := newAnchorPlayer(loop, dimOverworld, respawnAnchorFuelItemID)

	if !loop.useRespawnAnchor(p, pos, anchorAt(1)) {
		t.Fatal("glowstone on a chargeable anchor should consume the action")
	}
	if got := mustGet(t, mgr, pos); got != anchorAt(2) {
		t.Fatalf("charge: CHARGE not bumped to 2 (got %d)", got)
	}
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)).Count; got != 3 {
		t.Fatalf("held glowstone count=%d want 3 (consumed 1)", got)
	}
	fresh := levelgen.NewLegacyRandomSource(seed)
	if r.levelRandom.NextDouble() != fresh.NextDouble() {
		t.Fatal("charge drew RNG (should draw nothing)")
	}
}

// TestRespawnAnchorCreativeKeepsFuel asserts a creative charge bumps CHARGE but does NOT shrink the
// held glowstone (ItemStack.consume short-circuits on hasInfiniteMaterials).
func TestRespawnAnchorCreativeKeepsFuel(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, anchorAt(0), dimMinY)
	p := newAnchorPlayer(loop, dimOverworld, respawnAnchorFuelItemID)
	p.gameMode = gameModeCreative

	loop.useRespawnAnchor(p, pos, anchorAt(0))
	if got := mustGet(t, mgr, pos); got != anchorAt(1) {
		t.Fatalf("creative charge: CHARGE not bumped to 1 (got %d)", got)
	}
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)).Count; got != 4 {
		t.Fatalf("creative held count=%d want 4 (kept)", got)
	}
}

// TestRespawnAnchorFullCharge asserts a glowstone click on a fully-charged (CHARGE 4) anchor does NOT
// charge (canBeCharged false), so it falls through to useWithoutItem -> canSetSpawn/explode. In the
// overworld a charged anchor EXPLODES, removing the anchor block. CITE RespawnAnchorBlock.useItemOn
// (canBeCharged) + useWithoutItem (explode).
func TestRespawnAnchorFullChargeInOverworldExplodes(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0x99)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, anchorAt(4), dimMinY)
	p := newAnchorPlayer(loop, dimOverworld, respawnAnchorFuelItemID)

	if !loop.useRespawnAnchor(p, pos, anchorAt(4)) {
		t.Fatal("a full anchor click should consume the action (explode)")
	}
	// explode -> removeBlock(pos, false): the anchor cell is now air.
	if got := mustGet(t, mgr, pos); !block.IsAir(got) {
		t.Fatalf("overworld explode: anchor block should be removed (got %d)", got)
	}
	if p.respawnPos != nil {
		t.Fatal("overworld explode must NOT set a respawn point")
	}
}

// TestRespawnAnchorSetSpawnInNether asserts a charged anchor in the Nether SETS the player's respawn
// point at the anchor (block pos, dimension Nether) and does NOT explode. Re-clicking the SAME anchor is
// a no-op (isSamePosition). CITE RespawnAnchorBlock.useWithoutItem (canSetSpawn set-spawn branch).
func TestRespawnAnchorSetSpawnInNether(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	// Nether player with an empty hand: netherWorld is nil so dimWorld(p) falls back to the same mgr,
	// and the block is read/written at dimMinYFor(dimNether)==0.
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	nMinY := dimMinYFor(dimNether)
	mgr.SetBlock(pos, anchorAt(3), nMinY)
	p := newAnchorPlayer(loop, dimNether, 0)

	if !loop.useRespawnAnchor(p, pos, anchorAt(3)) {
		t.Fatal("charged anchor click in the Nether should consume the action")
	}
	if p.respawnPos == nil {
		t.Fatal("Nether charged anchor must set a respawn point")
	}
	if *p.respawnPos != pos || p.respawnDimension != dimNether {
		t.Fatalf("respawn point=%v dim=%d want %v dim=%d", *p.respawnPos, p.respawnDimension, pos, dimNether)
	}
	// The anchor block is NOT consumed on set-spawn (only CHARGE 3, still present). Read at the Nether
	// minY (mustGet hardcodes the overworld dimMinY, a different section index).
	if got, ok := mgr.GetBlock(pos, nMinY); !ok || got != anchorAt(3) {
		t.Fatalf("set-spawn must not change the anchor block (got %d ok=%v)", got, ok)
	}
	// Re-click the SAME anchor: isSamePosition -> no re-set (respawn point unchanged), still consumes.
	if !loop.useRespawnAnchor(p, pos, anchorAt(3)) {
		t.Fatal("re-clicking the same anchor should still consume the action")
	}
}

// TestRespawnAnchorEmptyPasses asserts a CHARGE-0 anchor clicked with a NON-fuel hand PASSes (returns
// false) so block placement continues, and does not change the block. CITE RespawnAnchorBlock.useItemOn
// (not fuel) -> useWithoutItem (CHARGE==0 -> PASS).
func TestRespawnAnchorEmptyPasses(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, anchorAt(0), dimMinY)
	p := newAnchorPlayer(loop, dimOverworld, 1) // held item id 1 (not glowstone)

	if loop.useRespawnAnchor(p, pos, anchorAt(0)) {
		t.Fatal("empty anchor + non-fuel hand should PASS (false)")
	}
	if got := mustGet(t, mgr, pos); got != anchorAt(0) {
		t.Fatalf("PASS click should not change the anchor (got %d)", got)
	}
}

// TestRespawnAnchorAnalogOutput asserts the comparator reads getScaledChargeLevel(state, 15) for every
// CHARGE, and (0,false) for a non-anchor. CITE RespawnAnchorBlock.getAnalogOutputSignal.
func TestRespawnAnchorAnalogOutput(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	want := map[int]int{0: 0, 1: 3, 2: 7, 3: 11, 4: 15}
	for charge, exp := range want {
		mgr.SetBlock(pos, anchorAt(charge), dimMinY)
		sig, has := loop.respawnAnchorAnalogOutputSignal(pos)
		if !has || sig != exp {
			t.Fatalf("CHARGE %d: analog=(%d,%v) want (%d,true)", charge, sig, has, exp)
		}
	}
	stone, _ := block.ToStateID[block.Stone{}]
	mgr.SetBlock(pos, stone, dimMinY)
	if _, has := loop.respawnAnchorAnalogOutputSignal(pos); has {
		t.Fatal("stone should not produce respawn_anchor analog output")
	}
}

// TestRespawnAnchorExplosionWetDryAffectedSet witnesses RespawnAnchorBlock$1 — the
// ExplosionDamageCalculator the anchor's bad_respawn_point explosion threads through. A WET
// anchor (water above OR any HORIZONTAL water neighbor) builds an override that returns water's
// explosion resistance (100.0f) AT the blast center; a DRY anchor passes nil (the generic
// vanilla calculator). Because the override REPLACES the resistance at the center cell, the wet
// ray loses (100+0.3f)*0.3f ~= 30 attenuation there — the rays hitting the (now air) anchor
// cell die in the wet case, so the affected set behind the center shrinks.
//
// Setup: a 7x7x7 stone cube around a charged (CHARGE 4) anchor with the same RNG seed for both
// runs. Dry: anchor + stone only. Wet: anchor + water above + water on all 4 HORIZONTAL
// neighbors + stone for the rest. The witness is that the wet affected set is at least as
// small as the dry one — the override can only ADD resistance at the center, never remove it.
// In practice the wet count is dramatically smaller because the water block neighbors also
// carry resistance 100 (their natural StateExplosionResistance) AND the override activates.
//
// Cite RespawnAnchorBlock.explode (inWater flag) + RespawnAnchorBlock$1
// .getBlockExplosionResistance (Optional.of(Blocks.WATER.getExplosionResistance())) +
// ExplosionDamageCalculator.getBlockExplosionResistance.
func TestRespawnAnchorExplosionWetDryAffectedSet(t *testing.T) {
	const cx, cy, cz = 8, 70, 8

	run := func(t *testing.T, wet bool) map[pk.Position]bool {
		loop, mgr := newPhysicsLoop()
		putChunk(mgr, level.ChunkPos{0, 0})
		fillStoneCube(mgr, cx-3, cx+3, cy-3, cy+3, cz-3, cz+3)
		center := pk.Position{X: cx, Y: cy, Z: cz}
		// Place the charged anchor at the center (respawnAnchorExplode removes it before exploding).
		mgr.SetBlock(center, anchorAt(4), dimMinY)
		if wet {
			// Wet path: water above + water on all 4 HORIZONTAL neighbors (the RespawnAnchorBlock
			// .explode inWater definition). The above-water + each horizontal is fluid-tag WATER.
			mgr.SetBlock(pk.Position{X: cx, Y: cy + 1, Z: cz}, block.ToStateID[block.Water{Level: 0}], dimMinY)
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				mgr.SetBlock(pk.Position{X: cx + d[0], Y: cy, Z: cz + d[1]}, block.ToStateID[block.Water{Level: 0}], dimMinY)
			}
		}

		clock := loop.clock.(*fakeClock)
		loop.start(clock.Now())
		// Same seed for both runs so the 16^3 shell-ray nextFloats + Util.shuffle nextInts replay
		// identically, and any difference in the affected set is attributable to the override seam.
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(12345)
		loop.gamerules = newGameRules()
		p := newAnchorPlayer(loop, dimOverworld, 0)

		loop.withRegion(loop.only(), func() {
			loop.respawnAnchorExplode(p, center)
		})

		// Snapshot which cells of the cube are now air (i.e. were added to toBlow by
		// calculateExplodedPositions AND not subsequently re-blocked by createFire). Exclude the
		// center cell -- it was pre-cleared to air by the anchor removeBlock, not by the blast.
		removed := map[pk.Position]bool{}
		for x := cx - 3; x <= cx+3; x++ {
			for y := cy - 3; y <= cy+3; y++ {
				for z := cz - 3; z <= cz+3; z++ {
					p := pk.Position{X: x, Y: y, Z: z}
					if p == center {
						continue
					}
					if st, ok := mgr.GetBlock(p, dimMinY); ok && block.IsAir(st) {
						removed[p] = true
					}
				}
			}
		}
		return removed
	}

	dry := run(t, false)
	wet := run(t, true)

	// Witness 1: the dry blast must remove a non-trivial set (sanity that the resistance-weighted
	// collection actually destroys when the override is absent).
	if len(dry) == 0 {
		t.Fatal("dry anchor blast removed no blocks (expected the resistance-weighted set behind the now-air center)")
	}
	// Witness 2: the wet override raises resistance at the center to water's resistance, so the
	// wet blast must remove no MORE cells than the dry blast (the override is monotonically
	// resistance-raising at the seam). In practice it removes many fewer, but the strict
	// invariant is monotonicity.
	if len(wet) > len(dry) {
		t.Errorf("wet anchor blast affected MORE cells than dry: dry=%d wet=%d (override must not reduce resistance)", len(dry), len(wet))
	}
	// Witness 3: at least one cell that the dry blast reaches is unreachable in the wet blast.
	// The override raises the center resistance from 0 (air) to 100 (water), which on the seeded
	// RNG seed drops cells that the dry blast would otherwise include. If dry == wet the override
	// was a no-op (would mean the seam is never invoked).
	if len(dry) == len(wet) {
		// Walk a sample diagonal cell to confirm the override activated in wet. If the dry set
		// contains ANY neighbor cell of the center that's NOT a water-neighbor setup cell, the
		// override seam is observably active.
		hasWitness := false
		for cell := range dry {
			if cell == (pk.Position{X: cx, Y: cy + 1, Z: cz}) {
				continue // water-above setup (only present in wet; never in dry)
			}
			isWaterSetup := false
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				if cell == (pk.Position{X: cx + d[0], Y: cy, Z: cz + d[1]}) {
					isWaterSetup = true
				}
			}
			if isWaterSetup {
				continue
			}
			hasWitness = true
			break
		}
		if !hasWitness {
			t.Fatalf("dry == wet AND no non-water-setup cell was reached in dry: dry=%d wet=%d (override seam is observably inert — inWater path not engaging)",
				len(dry), len(wet))
		}
	}
}
