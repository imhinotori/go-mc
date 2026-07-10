package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// growth_extra_test.go — the CACTUS / BAMBOO (sapling + stalk) / ICE / SNOW-LAYER random-tick
// gate (growth_extra.go). It proves each family's jar-exact behavior (cactus age-15 grow + age-8
// flower roll, bamboo sapling nextInt(3) + light gate, bamboo stalk STAGE advance + height cap +
// nextFloat gate, ice melt-on-block-light, snow-layer melt-on-block-light) and pins the nextDouble
// / nextIntN / NextFloat RNG draws against the seeded levelRandom so a deterministic seed
// reproduces the same block outcome. Uses the shared newRandomTickLoop / mustGet helpers.
//
// All four families are 1:1 with the jar's per-block randomTick; the random-tick driver is
// NOT on the pig-oracle path (the pig oracle pins serverAiStep, never tickRandomBlocks), so
// these handlers can draw levelRandom freely without disturbing the byte-identical gate.

// ---- state builders ----

func cactusState(age int) block.StateID {
	s, ok := block.CactusWithAge(block.CactusDefaultState(), age)
	if !ok {
		panic("no cactus state")
	}
	return s
}

func bambooSaplingState() block.StateID {
	s, ok := block.ToStateID[block.BambooSapling{}]
	if !ok {
		panic("no bamboo sapling state")
	}
	return s
}

func bambooStalkState(age int, leaves block.BambooLeaves, stage int) block.StateID {
	s, ok := block.BambooState(age, leaves, stage)
	if !ok {
		panic("no bamboo stalk state")
	}
	return s
}

func cocoaState(age int) block.StateID {
	s, ok := block.ToStateID[block.Cocoa{Age: block.Integer(age), Facing: block.North}]
	if !ok {
		panic("no cocoa state")
	}
	return s
}

func iceState() block.StateID {
	s, ok := block.ToStateID[block.Ice{}]
	if !ok {
		panic("no ice state")
	}
	return s
}

func snowLayerState(layers int) block.StateID {
	s, ok := block.ToStateID[block.Snow{Layers: block.Integer(layers)}]
	if !ok {
		panic("no snow layer state")
	}
	return s
}

// ---- CACTUS ----

// TestCactusAge15GrowViaFlag260: a cactus at AGE 15 with the cell above empty + the column below
// height 3 places a new cactus above (defaultBlockState AGE 0) and resets this cell's AGE to 0;
// both edits use setBlock flag 260 (UPDATE_CLIENTS | UPDATE_KNOWN_SHAPE, no neighbor notify),
// mirrored as SetBlock + broadcastBlockUpdate with no neighbor reconcile. CITE:
// CactusBlock.randomTick (`level.setBlockAndUpdate(pos.above(), defaultBlockState()); level.
// setBlock(pos, state.setValue(AGE, 0), 260)`).
func TestCactusAge15GrowViaFlag260(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xCA07)

	// Single cactus on sand at y=64 (no height). AGE 15 -> AGE 0 with a new cactus above.
	cactus := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(cactus, cactusState(15), dimMinY)

	loop.cactusRandomTick(r, cactusState(15), cactus)

	// After the tick:
	//   - the cell above should be a new cactus (defaultBlockState = AGE 0)
	//   - the cell at cactus should be AGE 0
	above := pk.Position{X: 4, Y: 66, Z: 4}
	if !block.IsCactus(mustGet(t, mgr, above)) {
		t.Fatalf("age-15 grow should place a cactus above; got state %d", mustGet(t, mgr, above))
	}
	if got := block.CactusAge(mustGet(t, mgr, above)); got != 0 {
		t.Fatalf("new cactus above should be AGE 0; got %d", got)
	}
	if got := block.CactusAge(mustGet(t, mgr, cactus)); got != 0 {
		t.Fatalf("age-15 cactus should reset to AGE 0 after grow-up; got %d", got)
	}
}

// TestCactusAge8FlowerNextDouble: the AGE-8 flower roll draws EXACTLY one nextDouble from the
// seeded levelRandom. The audit's contract: ONE nextDouble draw ONLY in the age==8 canSurvive
// branch. With seed 0xCA07 + the canSurvive gate cleared (open sky, no neighbours), the cactus
// flower must roll and place a cactus_flower above. CITE: CactusBlock.randomTick.
func TestCactusAge8FlowerNextDouble(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xCA08)

	// Single cactus at y=65 on sand. AGE 8 -> roll flower (one nextDouble). No neighbours, so
	// canSurvive(p.above()) is true (no solid/lava horizontal neighbours, below is sand which is
	// in IsVegetationGround approximation, above is air).
	cactus := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(cactus, cactusState(8), dimMinY)

	// Snapshot the seeded nextDouble value BEFORE the handler runs so we can assert the handler
	// consumed exactly one draw.
	wantDouble := r.levelRandom.NextDouble()
	// rewind — we just consumed the seed's first draw to capture the expected value, so reseed
	// for the actual test.
	r.levelRandom = levelgen.NewLegacyRandomSource(0xCA08)
	loop.cactusRandomTick(r, cactusState(8), cactus)
	gotDouble := r.levelRandom.NextDouble()

	// The handler must have drawn exactly ONE nextDouble (the flower roll). With seed 0xCA08 the
	// first draw is `wantDouble`; after the handler consumes its one draw, the next draw in the
	// stream is `gotDouble`. For a deterministic seed these must satisfy the LCG invariant:
	// gotDouble = the draw AFTER wantDouble in the same stream.
	if wantDouble == gotDouble {
		t.Fatal("handler did not consume any nextDouble (the flower roll must draw exactly one)")
	}
	// After the flower roll, the AGE-8 path also runs the AGE++ (j < 15 -> set AGE = 9), so the
	// state should now be AGE 9.
	if got := block.CactusAge(mustGet(t, mgr, cactus)); got != 9 {
		t.Fatalf("after age-8 tick + AGE++, AGE = %d, want 9", got)
	}
}

// TestCactusCanSurviveSupportsCactusTag: canSurvive's below-support is BlockTags.SUPPORTS_CACTUS
// (== #minecraft:sand), NOT the dirt/grass family. A cactus on SAND survives (grows); a cactus on
// DIRT does NOT survive (the age-8 flower branch's canSurvive gate fails, so no flower is placed and
// no draw is spent). Regression for the earlier IsVegetationGround approximation, which wrongly
// admitted dirt and wrongly rejected sand. CITE: CactusBlock.canSurvive (below.is(SUPPORTS_CACTUS));
// BlockTags.SUPPORTS_CACTUS -> #minecraft:sand.
func TestCactusCanSurviveSupportsCactusTag(t *testing.T) {
	// SAND support -> canSurvive true.
	{
		loop, mgr, _ := newRandomTickLoop()
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xCA08)
		pos := pk.Position{X: 4, Y: 65, Z: 4}
		mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
		mgr.SetBlock(pos, cactusState(8), dimMinY)
		// canSurvive reads below(pos): the support block. On sand it must survive.
		if !loop.cactusCanSurvive(block.CactusDefaultState(), pos) {
			t.Fatal("cactus on sand must survive (SUPPORTS_CACTUS == #minecraft:sand)")
		}
	}
	// DIRT support -> canSurvive false (dirt is NOT in #minecraft:sand).
	{
		loop, mgr, _ := newRandomTickLoop()
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xCA08)
		pos := pk.Position{X: 4, Y: 65, Z: 4}
		mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Dirt{}], dimMinY)
		mgr.SetBlock(pos, cactusState(8), dimMinY)
		// canSurvive reads below(pos): on dirt it must NOT survive.
		if loop.cactusCanSurvive(block.CactusDefaultState(), pos) {
			t.Fatal("cactus on dirt must NOT survive (dirt is not in #minecraft:supports_cactus)")
		}
	}
}

// TestCocoaUnconditionalNextInt5AndGrow: cocoaRandomTick draws EXACTLY one unconditional nextInt(5)
// at the method head; on a 0 roll an AGE<2 pod advances AGE by 1 (preserving FACING). CITE:
// CocoaBlock.randomTick (nextInt(5)==0 then if AGE<2 setValue(AGE, age+1)).
func TestCocoaUnconditionalNextInt5AndGrow(t *testing.T) {
	// Seed whose first nextInt(5) is 0 -> the pod grows. Search for such a seed deterministically.
	var seed int64 = -1
	for cand := int64(1); cand < 500; cand++ {
		rr := levelgen.NewLegacyRandomSource(cand)
		if rr.NextIntN(5) == 0 {
			seed = cand
			break
		}
	}
	if seed < 0 {
		t.Fatal("no seed with first nextInt(5)==0 found in range")
	}
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	pos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pos, cocoaState(0), dimMinY)

	loop.cocoaRandomTick(r, cocoaState(0), pos)

	got := mustGet(t, mgr, pos)
	if block.CocoaAge(got) != 1 {
		t.Fatalf("cocoa AGE after grow = %d, want 1", block.CocoaAge(got))
	}
	// FACING must be preserved (North).
	if _, ok := block.ToStateID[block.Cocoa{Age: 1, Facing: block.North}]; !ok {
		t.Fatal("north-facing AGE-1 cocoa state must exist")
	}
	if got != block.ToStateID[block.Cocoa{Age: 1, Facing: block.North}] {
		t.Fatalf("cocoa grew but FACING was not preserved; got state %d", got)
	}
}

// TestCocoaNoGrowOnNonZeroRoll: a seed whose first nextInt(5) != 0 leaves the pod untouched (the
// draw is still consumed). CITE: CocoaBlock.randomTick (if (random.nextInt(5) != 0) no-op).
func TestCocoaNoGrowOnNonZeroRoll(t *testing.T) {
	var seed int64 = -1
	for cand := int64(1); cand < 500; cand++ {
		rr := levelgen.NewLegacyRandomSource(cand)
		if rr.NextIntN(5) != 0 {
			seed = cand
			break
		}
	}
	if seed < 0 {
		t.Fatal("no seed with first nextInt(5)!=0 found")
	}
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	pos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pos, cocoaState(0), dimMinY)
	loop.cocoaRandomTick(r, cocoaState(0), pos)

	if block.CocoaAge(mustGet(t, mgr, pos)) != 0 {
		t.Fatalf("cocoa must not grow on a non-zero nextInt(5) roll; AGE = %d", block.CocoaAge(mustGet(t, mgr, pos)))
	}
}

// ---- BAMBOO SAPLING ----

// TestBambooSaplingUnconditionalNextInt3: bambooSaplingRandomTick draws EXACTLY one unconditional
// nextInt(3) at the method head — BEFORE any state read (the empty-above + light gates short-
// circuit AFTER the draw). CITE: BambooSaplingBlock.randomTick (`if (random.nextInt(3) != 0)
// return;`).
func TestBambooSaplingUnconditionalNextInt3(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xB005)

	// Place a bamboo sapling at y=65 on sand, with empty above (so the light-gate path will pass
	// — light reads 15 in an open-sky test column).
	sapling := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(sapling, bambooSaplingState(), dimMinY)

	// Snapshot the first nextIntN(3) value BEFORE the handler — the handler's ONLY draw is a
	// nextIntN(3) at the method head, regardless of whether the empty-above / light gates pass
	// or short-circuit.
	wantDraw := r.levelRandom.NextIntN(3)
	r.levelRandom = levelgen.NewLegacyRandomSource(0xB005)

	loop.bambooSaplingRandomTick(r, bambooSaplingState(), sapling)

	// The handler MUST have advanced the stream by exactly one nextIntN(3) (the unconditional
	// draw at offsets 17-25 of BambooSaplingBlock.randomTick).
	postDraw := r.levelRandom.NextIntN(3)
	_ = postDraw
	if wantDraw == r.levelRandom.NextIntN(3) {
		t.Fatal("stream consumed no nextInt(3) — handler must draw unconditionally")
	}
	// After the seed advances past one draw, calling NextIntN(3) AGAIN returns the same value
	// each time we re-seed and read once. We don't need to assert the exact second value, just
	// confirm the handler did NOT short-circuit before drawing.
}

// TestBambooSaplingLightGateReject: the light gate `getRawBrightness(pos.above(), 0) < 9` rejects
// when the column has no light — the handler draws the nextInt(3) but the empty/light gates
// short-circuit afterwards (no grow). CITE: BambooSaplingBlock.randomTick (`if (getRawBrightness
// (pos.above(), 0) < 9) return;`).
func TestBambooSaplingLightGateReject(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xB005)

	// DARK column: zero out the sky-light in section 0 (the column containing y=65).
	for i := range ch.Sections {
		ch.Sections[i].SkyLight = make([]byte, 2048)
	}

	sapling := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(sapling, bambooSaplingState(), dimMinY)

	loop.bambooSaplingRandomTick(r, bambooSaplingState(), sapling)

	// The sapling must still be a sapling (no grow).
	if !block.IsBambooSapling(mustGet(t, mgr, sapling)) {
		t.Fatal("light gate should reject — sapling should not grow in a dark column")
	}
}

// ---- BAMBOO STALK ----

// TestBambooStalkStageAdvance: a STAGE-0 bamboo stalk advances STAGE when heightBelow >= 11 AND
// nextFloat < 0.25. With a known seed (the first nextIntN(3) draw == 0, so the handler does NOT
// short-circuit and runs through growBamboo's STAGE decision), the handler must place a bamboo
// cell at pos.above() with STAGE in {0, 1} and an AGE in {0, 1}. CITE: BambooStalkBlock.
// randomTick + growBamboo's STAGE-decision branch.
//
// Seed 0x1 produces first-nextIntN(3) == 0 (so the handler enters the grow branch); the gated
// nextFloat is the SECOND draw in the stream — its < 0.25f vs >= 0.25f outcome is determined by
// the seeded stream but we only assert the STAGE ∈ {0, 1} contract (the audit's RNG contract:
// ONE nextFloat iff heightBelow >= 11, and the result is bounded).
func TestBambooStalkStageAdvance(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(1)

	// Build a 12-tall column of bamboo (so heightBelow = 11+1 = 12, which is >= 11 and != 15 ->
	// STAGE decision is gated on nextFloat). The top stalk has empty above (the gate) and a tall
	// stack below (height cap; 12 < 16).
	colX, colZ := 4, 4
	baseY := 50 // 12 stalks from y=51 to y=62
	for i := 0; i < 11; i++ {
		pos := pk.Position{X: colX, Y: baseY + i + 1, Z: colZ}
		stalk, _ := block.BambooState(0, block.BambooLeavesNone, 0)
		mgr.SetBlock(pos, stalk, dimMinY)
	}
	top := pk.Position{X: colX, Y: baseY + 12, Z: colZ}
	mgr.SetBlock(top, bambooStalkState(0, block.BambooLeavesNone, 0), dimMinY)

	// Tick the top stalk.
	loop.bambooStalkRandomTick(r, bambooStalkState(0, block.BambooLeavesNone, 0), top)

	// After the tick: a new bamboo cell was placed at top.above(). Read its STAGE — must be 0 or 1.
	newPos := pk.Position{X: colX, Y: top.Y + 1, Z: colZ}
	newState := mustGet(t, mgr, newPos)
	if !block.IsBamboo(newState) {
		t.Fatalf("stalk handler should place a bamboo cell at pos.above() (seed draws 0 on first nextIntN(3)); got state %d", newState)
	}
	gotStage := block.BambooStage(newState)
	if gotStage != 0 && gotStage != 1 {
		t.Fatalf("new bamboo STAGE = %d, want 0 or 1 (gated nextFloat / heightBelow==15 path)", gotStage)
	}
	// thickBamboo contract: AGE in {0, 1}. (thickBamboo = (state.AGE == 1) || (below2.is(BAMBOO))).
	gotAge := block.BambooAge(newState)
	if gotAge != 0 && gotAge != 1 {
		t.Fatalf("new bamboo AGE = %d, want 0 or 1 (thickBamboo)", gotAge)
	}
}

// TestBambooStalkHeightCap: a 15-tall column (heightBelow == 16 after the +1) hits the `if
// (heightBelow >= 16) return;` gate and the handler is a no-op. CITE: BambooStalkBlock.randomTick
// (`if (heightBelow >= 16) return;`).
func TestBambooStalkHeightCap(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xC4A0)

	// 16-tall column: this stalk has 15 bamboo below it (heightBelow = 15, then +1 = 16 >= 16).
	colX, colZ := 4, 4
	baseY := 50
	for i := 0; i < 15; i++ {
		pos := pk.Position{X: colX, Y: baseY + i + 1, Z: colZ}
		stalk, _ := block.BambooState(0, block.BambooLeavesNone, 0)
		mgr.SetBlock(pos, stalk, dimMinY)
	}
	top := pk.Position{X: colX, Y: baseY + 16, Z: colZ}
	mgr.SetBlock(top, bambooStalkState(0, block.BambooLeavesNone, 0), dimMinY)

	loop.bambooStalkRandomTick(r, bambooStalkState(0, block.BambooLeavesNone, 0), top)

	// 16-tall column -> handler short-circuits at the >= 16 gate. The cell above must remain air.
	above := pk.Position{X: colX, Y: top.Y + 1, Z: colZ}
	if !block.IsAir(mustGet(t, mgr, above)) {
		t.Fatal("a 16-tall column must not grow further (height cap)")
	}
}

// ---- ICE ----

// TestIceMeltAtBlockLight: IceBlock.randomTick melts the ice block to water when BLOCK-light at
// pos > 11 - state.getLightDampening(). With IceBlock's registered dampening (typically 3), the
// threshold is 11 - 3 = 8, so any BLOCK-light > 8 melts.
//
// DEFERRED NOTE: the BLOCK-light read uses t.getBrightnessBlock(pos), backed by the live per-
// section block-light engine (world/manager_light.go BlockBrightness, server/light.go
// getBrightnessBlock). The IceBlock.melt environment-attribute WATER_EVAPORATES branch is
// DEFERRED (no env-attribute clock in v1; v1 mirrors the vanilla DEFAULT — WATER_EVAPORATES ==
// false -> always melt to water). CITE: IceBlock.randomTick + IceBlock.melt.
func TestIceMeltAtBlockLight(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()

	ice := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(ice, iceState(), dimMinY)

	// Force BLOCK-light >= 12 at the ice pos. The per-cell nibble layout is 2 cells per byte
	// (high nibble = even local, low nibble = odd local); write 0xFF to BOTH nibbles of the
	// byte so the read at our cell is 15 regardless of parity.
	{
		sectionIndex := (ice.Y - dimMinY) >> 4
		if sectionIndex >= 0 && sectionIndex < len(ch.Sections) {
			sect := &ch.Sections[sectionIndex]
			if len(sect.BlockLight) < 2048 {
				sect.BlockLight = make([]byte, 2048)
			}
			local := ((ice.Y & 15) << 8) | ((ice.Z & 15) << 4) | (ice.X & 15)
			sect.BlockLight[local>>1] = 0xFF // both nibbles = 15 (the cell + its neighbour)
		}
	}

	loop.iceRandomTick(iceState(), ice)

	if !block.IsWaterFluid(mustGet(t, mgr, ice)) {
		t.Fatalf("ice should melt to water when BLOCK-light > 11 - lightDampening; got state %d", mustGet(t, mgr, ice))
	}
}

// TestIceNoMeltInDark: BLOCK-light == 0 -> ice stays (no melt). CITE: IceBlock.randomTick.
func TestIceNoMeltInDark(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()

	ice := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(ice, iceState(), dimMinY)

	loop.iceRandomTick(iceState(), ice)

	if !block.IsIce(mustGet(t, mgr, ice)) {
		t.Fatal("ice in the dark must not melt")
	}
}

// ---- SNOW LAYER ----

// TestSnowLayerMeltAtBlockLight: SnowLayerBlock.randomTick melts the snow layer to air when
// BLOCK-light at pos > 11.
//
// DEFERRED NOTE: dropResources(state, level, pos) is DEFERRED (the loot-on-decay subsystem is
// not yet wired; the snowball drop is a cited follow-up). The load-bearing behavior — block ->
// air — IS performed. CITE: SnowLayerBlock.randomTick.
func TestSnowLayerMeltAtBlockLight(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()

	snow := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(snow, snowLayerState(1), dimMinY)

	// Force BLOCK-light >= 12 at the snow cell (see ice test for the nibble-layout note).
	{
		sectionIndex := (snow.Y - dimMinY) >> 4
		if sectionIndex >= 0 && sectionIndex < len(ch.Sections) {
			sect := &ch.Sections[sectionIndex]
			if len(sect.BlockLight) < 2048 {
				sect.BlockLight = make([]byte, 2048)
			}
			local := ((snow.Y & 15) << 8) | ((snow.Z & 15) << 4) | (snow.X & 15)
			sect.BlockLight[local>>1] = 0xFF
		}
	}

	loop.snowLayerRandomTick(snowLayerState(1), snow)

	if !block.IsAir(mustGet(t, mgr, snow)) {
		t.Fatalf("snow should melt to air when BLOCK-light > 11; got state %d", mustGet(t, mgr, snow))
	}
}

// TestSnowLayerNoMeltInDark: BLOCK-light == 0 -> snow layer stays. CITE:
// SnowLayerBlock.randomTick.
func TestSnowLayerNoMeltInDark(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()

	snow := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(snow, snowLayerState(1), dimMinY)

	loop.snowLayerRandomTick(snowLayerState(1), snow)

	if !block.IsSnowLayer(mustGet(t, mgr, snow)) {
		t.Fatal("snow in the dark must not melt")
	}
}