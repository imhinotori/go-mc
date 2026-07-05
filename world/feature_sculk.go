package world

// feature_sculk.go ports the sculk_patch feature body 1:1 from the unobfuscated Minecraft
// 26.2 jar (temp/cache/26.2-inner.jar, read via CFR + javap -c), including the full
// SculkSpreader charge/spread simulation it drives:
//
//   - net.minecraft.world.level.levelgen.feature.SculkPatchFeature.{place,canSpreadFrom}
//   - net.minecraft.world.level.levelgen.feature.configurations.SculkPatchConfiguration
//       (charge_count, amount_per_charge, spread_attempts, growth_rounds, spread_rounds,
//        extra_rare_growths IntProvider, catalyst_chance)
//   - net.minecraft.world.level.block.SculkSpreader.{createWorldGenSpreader,addCursors,
//        addCursor,updateCursors,clear} + ChargeCursor.{update,shouldUpdate,mergeWith,
//        getValidMovementPos,getRandomizedNonCornerNeighbourOffsets,isMovementUnobstructed,
//        isUnobstructed,isPosUnreasonable,NON_CORNER_NEIGHBOURS}
//   - net.minecraft.world.level.block.SculkBehaviour.DEFAULT (attemptSpreadVein/attemptUseCharge/updateDecayDelay)
//   - net.minecraft.world.level.block.SculkVeinBlock.{attemptUseCharge,attemptPlaceSculk,
//        attemptSpreadVein(via sameSpaceSpreader),onDischarged,regrow,hasSubstrateAccess,
//        SculkVeinSpreaderConfig.{stateCanBeReplaced,isOtherBlockValidAsSource}}
//   - net.minecraft.world.level.block.SculkBlock.{attemptUseCharge,canPlaceGrowth,
//        getRandomGrowthState,getDecayPenalty,canChangeBlockStateOnSpread}
//   - net.minecraft.core.BlockPos.betweenClosed iteration order (X fastest, then Y, then Z)
//   - net.minecraft.Util.shuffledCopy / Direction.allShuffled (Fisher-Yates from the end)
//
// sculk_patch_deep_dark.json + sculk_patch_ancient_city.json are the two configured
// features; before this port both were an unregistered no-op (the deep-dark floor never
// grew its sculk carpet). Both use spread_rounds=1, growth_rounds=0 → one round with
// spreadVeins=true across spread_attempts(64) cursor updates.
//
// The RNG-DRAW ORDER is the determinism contract (research Pitfall 4). The whole simulation
// is a strict single-threaded ordered port: cursor iteration order, the per-update shuffle of
// the non-corner neighbour offsets, the decay/growth nextInt draws, the vein-spread shuffles,
// and finally random.nextFloat (catalyst) + extraRareGrowths.sample + the per-growth
// nextInt(5) offset draws are all reproduced jar-exact. Every write goes through
// bctx.placeState (Neighborhood: cross-chunk + live heightmaps).
//
// Worldgen-only reductions (documented, NOT gameplay changes — these paths have no observable
// block effect at worldgen time and vanilla itself no-ops them here):
//   - level.levelEvent / playSound / markPosForPostProcessing: client/particle/tick effects,
//     never a block write. Omitted.
//   - the non-worldgen cursor MERGE branch (isWorldGeneration()==false): worldgen ALWAYS keeps
//     every cursor separate (updateCursors: the merge only runs when !isWorldGeneration), so the
//     merge path is dead here and omitted; every processed cursor is retained as vanilla does.
//   - Block.pushEntitiesUp: no entities exist during decoration. Omitted (no block effect).

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("sculk_patch", sculkPatchBody)
}

// ---- config ----

type sculkPatchConfig struct {
	chargeCount     int
	amountPerCharge int
	spreadAttempts  int
	growthRounds    int
	spreadRounds    int
	extraRareGrowth *dripstoneIntProvider
	catalystChance  float32
	err             error
}

var sculkPatchCache sync.Map // map[*feature.ConfiguredFeature]*sculkPatchConfig

func decodeSculkPatch(cf *feature.ConfiguredFeature) *sculkPatchConfig {
	if v, ok := sculkPatchCache.Load(cf); ok {
		return v.(*sculkPatchConfig)
	}
	d := &sculkPatchConfig{}
	var j struct {
		ChargeCount     int             `json:"charge_count"`
		AmountPerCharge int             `json:"amount_per_charge"`
		SpreadAttempts  int             `json:"spread_attempts"`
		GrowthRounds    int             `json:"growth_rounds"`
		SpreadRounds    int             `json:"spread_rounds"`
		ExtraRare       json.RawMessage `json:"extra_rare_growths"`
		CatalystChance  float32         `json:"catalyst_chance"`
	}
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: sculk_patch config %q: %w", cf.ID, err)
		sculkPatchCache.Store(cf, d)
		return d
	}
	d.chargeCount = j.ChargeCount
	d.amountPerCharge = j.AmountPerCharge
	d.spreadAttempts = j.SpreadAttempts
	d.growthRounds = j.GrowthRounds
	d.spreadRounds = j.SpreadRounds
	d.catalystChance = j.CatalystChance
	ip, err := parseDripstoneIntProvider(j.ExtraRare)
	if err != nil {
		d.err = fmt.Errorf("world: sculk_patch extra_rare_growths %q: %w", cf.ID, err)
		sculkPatchCache.Store(cf, d)
		return d
	}
	d.extraRareGrowth = ip
	sculkPatchCache.Store(cf, d)
	return d
}

// ---- cached block states + tag sets ----

var (
	sculkStateID         = block.ToStateID[block.Sculk{}]
	sculkCatalystStateID = block.ToStateID[block.SculkCatalyst{Bloom: false}]
	sculkSensorStateID   = block.ToStateID[block.SculkSensor{Power: 0, SculkSensorPhase: block.SculkSensorPhaseInactive, Waterlogged: false}]
	sculkVeinDefaultID   = block.ToStateID[block.SculkVein{}]

	sculkReplaceableWorldGenOnce sync.Once
	sculkReplaceableWorldGenSet  map[block.StateID]bool
)

// sculkShriekerState builds a SCULK_SHRIEKER default with can_summon set (+ waterlogged when
// asked). getRandomGrowthState passes isWorldGen for can_summon; the SculkPatchFeature extra
// growths pass true.
func sculkShriekerState(canSummon, waterlogged bool) block.StateID {
	return block.ToStateID[block.SculkShrieker{
		CanSummon:   block.Boolean(canSummon),
		Shrieking:   false,
		Waterlogged: block.Boolean(waterlogged),
	}]
}

// sculkSensorWaterloggedState is the SCULK_SENSOR default with waterlogged set.
func sculkSensorWaterloggedState(waterlogged bool) block.StateID {
	return block.ToStateID[block.SculkSensor{Power: 0, SculkSensorPhase: block.SculkSensorPhaseInactive, Waterlogged: block.Boolean(waterlogged)}]
}

// sculkReplaceableWorldGen reports state.is(#sculk_replaceable_world_gen) — the worldgen
// spreader's replaceableBlocks tag. Resolved once from the embedded tag JSON.
func sculkReplaceableWorldGen(st block.StateID) bool {
	sculkReplaceableWorldGenOnce.Do(func() {
		sculkReplaceableWorldGenSet = resolveTagStateSet("sculk_replaceable_world_gen")
	})
	return sculkReplaceableWorldGenSet[st]
}

// ---- SculkPatchFeature.place ----

// sculkPatchBody ports SculkPatchFeature.place. Draw order in the file header.
func sculkPatchBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg := decodeSculkPatch(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}
	if !bctx.sculkCanSpreadFrom(origin) {
		return false
	}
	spreader := newSculkSpreader()
	totalRounds := cfg.spreadRounds + cfg.growthRounds
	for round := 0; round < totalRounds; round++ {
		for i := 0; i < cfg.chargeCount; i++ {
			spreader.addCursors(origin, cfg.amountPerCharge)
		}
		spreadVeins := round < cfg.spreadRounds
		for i := 0; i < cfg.spreadAttempts; i++ {
			spreader.updateCursors(bctx, origin, rng, spreadVeins)
		}
		spreader.clear()
	}
	below := placement.BlockPos{X: origin.X, Y: origin.Y - 1, Z: origin.Z}
	if rng.NextFloat() <= cfg.catalystChance && block.IsCollisionShapeFullBlock(bctx.getState(below)) {
		bctx.placeState(origin, sculkCatalystStateID)
	}
	extraGrowths := cfg.extraRareGrowth.sample(rng)
	for i := 0; i < extraGrowths; i++ {
		candidate := placement.BlockPos{
			X: origin.X + int(rng.NextIntN(5)) - 2,
			Y: origin.Y,
			Z: origin.Z + int(rng.NextIntN(5)) - 2,
		}
		candBelow := placement.BlockPos{X: candidate.X, Y: candidate.Y - 1, Z: candidate.Z}
		if !block.IsAir(bctx.getState(candidate)) || !block.IsFaceSturdy(bctx.getState(candBelow), block.Up, block.SupportFull) {
			continue
		}
		bctx.placeState(candidate, sculkShriekerState(true, false))
	}
	return true
}

// sculkCanSpreadFrom ports SculkPatchFeature.canSpreadFrom: the origin block is a
// SculkBehaviour (sculk family), OR (air OR a WATER source) with an adjacent full-collision
// neighbour. 0 rng draws.
func (b *bodyContext) sculkCanSpreadFrom(origin placement.BlockPos) bool {
	start := b.getState(origin)
	if isSculkBehaviour(start) {
		return true
	}
	if !block.IsAir(start) {
		// !start.is(WATER) || !fluidState.isSource() → not a water source → false.
		if !isWaterSource(start) {
			return false
		}
	}
	// Direction.stream().map(origin::relative).anyMatch(isCollisionShapeFullBlock).
	for _, d := range geodeDirections {
		np := placement.BlockPos{X: origin.X + d.dx, Y: origin.Y + d.dy, Z: origin.Z + d.dz}
		if block.IsCollisionShapeFullBlock(b.getState(np)) {
			return true
		}
	}
	return false
}

// ===========================================================================================
//  SculkSpreader
// ===========================================================================================

// sculkSpreader ports the worldgen SculkSpreader (createWorldGenSpreader: isWorldGeneration
// true, replaceable=#sculk_replaceable_world_gen, growthSpawnCost=50, noGrowthRadius=1,
// chargeDecayRate=5, additionalDecayRate=10).
type sculkSpreader struct {
	cursors []*sculkCursor
}

const (
	sculkGrowthSpawnCost   = 50
	sculkNoGrowthRadius    = 1
	sculkChargeDecayRate   = 5
	sculkAdditionalDecay   = 10
	sculkMaxCursors        = 32
	sculkMaxCharge         = 1000
	sculkMaxCursorDistance = 1024
)

func newSculkSpreader() *sculkSpreader { return &sculkSpreader{} }

func (s *sculkSpreader) clear() { s.cursors = s.cursors[:0] }

// addCursors ports SculkSpreader.addCursors: split `charge` into ≤1000 chunks, each a cursor.
func (s *sculkSpreader) addCursors(startPos placement.BlockPos, charge int) {
	for charge > 0 {
		current := charge
		if current > sculkMaxCharge {
			current = sculkMaxCharge
		}
		s.addCursor(&sculkCursor{pos: startPos, charge: current, decayDelay: 1, updateDelay: 0})
		charge -= current
	}
}

func (s *sculkSpreader) addCursor(c *sculkCursor) {
	if len(s.cursors) >= sculkMaxCursors {
		return
	}
	s.cursors = append(s.cursors, c)
}

// updateCursors ports SculkSpreader.updateCursors (worldgen path). The non-worldgen merge
// branch is dead at worldgen (see file header), so every surviving cursor (charge > 0 and not
// unreasonable) is retained. The levelEvent particle emit is omitted (no block effect).
func (s *sculkSpreader) updateCursors(bctx *bodyContext, origin placement.BlockPos, rng levelgen.RandomSource, spreadVeins bool) {
	if len(s.cursors) == 0 {
		return
	}
	processed := make([]*sculkCursor, 0, len(s.cursors))
	for _, cursor := range s.cursors {
		if cursor.isPosUnreasonable(origin) {
			continue
		}
		cursor.update(bctx, origin, rng, spreadVeins)
		if cursor.charge <= 0 {
			continue
		}
		// worldgen: no merge; keep the cursor.
		processed = append(processed, cursor)
	}
	s.cursors = processed
}

// ---- ChargeCursor ----

type sculkCursor struct {
	pos         placement.BlockPos
	charge      int
	updateDelay int
	decayDelay  int
	facings     multifaceFaces
	hasFacings  bool
}

func (c *sculkCursor) isPosUnreasonable(origin placement.BlockPos) bool {
	return distChessboard(c.pos, origin) > sculkMaxCursorDistance
}

// update ports SculkSpreader.ChargeCursor.update (worldgen). shouldUpdate is always true at
// worldgen (charge>0 checked by caller path). Draw order: attemptSpreadVein draws (vein
// spread shuffles), then attemptUseCharge draws (behaviour-specific decay/growth), then
// getValidMovementPos draws (non-corner neighbour shuffle).
func (c *sculkCursor) update(bctx *bodyContext, origin placement.BlockPos, rng levelgen.RandomSource, spreadVeins bool) {
	// shouldUpdate: charge>0 (else the caller wouldn't retain it) && isWorldGen → true.
	if c.charge <= 0 {
		return
	}
	if c.updateDelay > 0 {
		c.updateDelay--
		return
	}
	currentState := bctx.getState(c.pos)
	behaviour := sculkBehaviourOf(currentState)
	if spreadVeins && bctx.sculkAttemptSpreadVein(behaviour, c, currentState, rng) {
		if behaviour.canChangeBlockStateOnSpread {
			currentState = bctx.getState(c.pos)
			behaviour = sculkBehaviourOf(currentState)
		}
		// playSound omitted.
	}
	c.charge = bctx.sculkAttemptUseCharge(behaviour, c, origin, rng, spreadVeins)
	if c.charge <= 0 {
		bctx.sculkOnDischarged(behaviour, currentState, c.pos, rng)
		return
	}
	transferPos, moved := bctx.sculkGetValidMovementPos(c.pos, rng)
	if moved {
		bctx.sculkOnDischarged(behaviour, currentState, c.pos, rng)
		c.pos = transferPos
		// worldgen: if the new pos leaves the 15-block horizontal cylinder around origin,
		// the cursor dies.
		if !closerThanXZ(c.pos, origin, 15.0) {
			c.charge = 0
			return
		}
		currentState = bctx.getState(transferPos)
	}
	if isSculkBehaviour(currentState) {
		c.facings = multifaceAvailableFaces(currentState)
		c.hasFacings = true
	}
	c.decayDelay = behaviour.updateDecayDelay(c.decayDelay)
	c.updateDelay = int(behaviour.sculkSpreadDelay)
}

// ---- SculkBehaviour dispatch ----

// sculkBehaviour is the resolved behaviour for a block state (DEFAULT, SCULK, or SCULK_VEIN).
type sculkBehaviour struct {
	kind                     sculkBehaviourKind
	canChangeBlockStateOnSpread bool
	sculkSpreadDelay         byte
}

type sculkBehaviourKind int

const (
	sculkBehaviourDefault sculkBehaviourKind = iota
	sculkBehaviourSculk
	sculkBehaviourVein
)

// sculkBehaviourOf ports ChargeCursor.getBlockBehaviour: SCULK → SculkBlock (canChange=false),
// SCULK_VEIN → SculkVeinBlock, SCULK_CATALYST → SculkCatalystBlock (uses DEFAULT charge here),
// anything else → SculkBehaviour.DEFAULT. getSculkSpreadDelay() is 1 for all these.
func sculkBehaviourOf(st block.StateID) sculkBehaviour {
	switch {
	case isSculkBlock(st):
		return sculkBehaviour{kind: sculkBehaviourSculk, canChangeBlockStateOnSpread: false, sculkSpreadDelay: 1}
	case multifaceIsPlaceBlock(multifaceSculkVein, st):
		return sculkBehaviour{kind: sculkBehaviourVein, canChangeBlockStateOnSpread: true, sculkSpreadDelay: 1}
	default:
		return sculkBehaviour{kind: sculkBehaviourDefault, canChangeBlockStateOnSpread: true, sculkSpreadDelay: 1}
	}
}

// updateDecayDelay ports the behaviour updateDecayDelay: DEFAULT = max(age-1,0); SculkBlock /
// SculkVeinBlock inherit the interface default = 1 (they do NOT override it).
func (b sculkBehaviour) updateDecayDelay(age int) int {
	if b.kind == sculkBehaviourDefault {
		if age-1 > 0 {
			return age - 1
		}
		return 0
	}
	return 1
}

// sculkAttemptSpreadVein ports SculkBehaviour.attemptSpreadVein for each kind.
//   - DEFAULT: SCULK_VEIN.getSpreader().spreadAll(state, level, pos, isWorldGen) > 0.
//   - SCULK (SculkBlock inherits DEFAULT via SculkBehaviour default? no — SculkBlock does NOT
//     override attemptSpreadVein, so it uses the interface default: SCULK_VEIN.getSpreader().spreadAll).
//   - SCULK_VEIN (the anonymous SculkBehaviour.DEFAULT in SculkBehaviour is only for non-sculk
//     blocks; SculkVeinBlock does NOT override attemptSpreadVein either, so it also uses the
//     interface default spreadAll). BUT the ChargeCursor's currentState-derived behaviour for a
//     plain (non-sculk) block IS the anonymous DEFAULT, whose attemptSpreadVein branches on
//     `facings`.
//
// The anonymous SculkBehaviour.DEFAULT.attemptSpreadVein (used for a NON-sculk currentState):
//   - facings == null            → sameSpaceSpreader.spreadAll(getBlockState(pos)) > 0.
//   - facings non-empty:
//       state air || water       → SculkVeinBlock.regrow(level, pos, state, facings).
//       else                     → false.
//   - facings empty              → interface-default spreadAll(state) > 0.
// A sculk/vein currentState uses the interface-default spreadAll(state) directly.
func (b *bodyContext) sculkAttemptSpreadVein(behaviour sculkBehaviour, c *sculkCursor, state block.StateID, rng levelgen.RandomSource) bool {
	if behaviour.kind == sculkBehaviourDefault {
		if !c.hasFacings {
			// facings == null → sameSpaceSpreader (SAME_POSITION only) spreadAll over the
			// CURRENT block at pos.
			return b.sculkVeinSpreadAll(b.getState(c.pos), c.pos, rng, sculkSameSpaceSpreadOrder) > 0
		}
		if c.facings.any() {
			if block.IsAir(state) || isWaterFluid(state) {
				return b.sculkVeinRegrow(c.pos, state, c.facings)
			}
			return false
		}
		// facings empty → interface default spreadAll(state).
		return b.sculkVeinSpreadAll(state, c.pos, rng, multifaceDefaultSpreadOrder) > 0
	}
	// SCULK / SCULK_VEIN behaviour → interface default spreadAll(state) over the vein spreader.
	return b.sculkVeinSpreadAll(state, c.pos, rng, multifaceDefaultSpreadOrder) > 0
}

// sculkAttemptUseCharge ports the per-behaviour attemptUseCharge.
func (b *bodyContext) sculkAttemptUseCharge(behaviour sculkBehaviour, c *sculkCursor, origin placement.BlockPos, rng levelgen.RandomSource, spreadVeins bool) int {
	switch behaviour.kind {
	case sculkBehaviourVein:
		return b.sculkVeinAttemptUseCharge(c, origin, rng, spreadVeins)
	case sculkBehaviourSculk:
		return b.sculkBlockAttemptUseCharge(c, origin, rng)
	default:
		// SculkBehaviour.DEFAULT.attemptUseCharge: decayDelay>0 ? charge : 0.
		if c.decayDelay > 0 {
			return c.charge
		}
		return 0
	}
}

// sculkOnDischarged ports onDischarged: only SCULK_VEIN overrides it (rewrites the vein state);
// DEFAULT + SCULK are no-ops.
func (b *bodyContext) sculkOnDischarged(behaviour sculkBehaviour, state block.StateID, pos placement.BlockPos, rng levelgen.RandomSource) {
	if behaviour.kind == sculkBehaviourVein {
		b.sculkVeinOnDischarged(state, pos)
	}
}

// ===========================================================================================
//  SculkBlock.attemptUseCharge
// ===========================================================================================

// sculkBlockAttemptUseCharge ports SculkBlock.attemptUseCharge.
func (b *bodyContext) sculkBlockAttemptUseCharge(c *sculkCursor, origin placement.BlockPos, rng levelgen.RandomSource) int {
	charge := c.charge
	if charge == 0 || int(rng.NextIntN(int32(sculkChargeDecayRate))) != 0 {
		return charge
	}
	chargePos := c.pos
	isCloseToCatalyst := closerThan(chargePos, origin, float64(sculkNoGrowthRadius))
	if isCloseToCatalyst || !b.sculkCanPlaceGrowth(chargePos) {
		if int(rng.NextIntN(int32(sculkAdditionalDecay))) != 0 {
			return charge
		}
		if isCloseToCatalyst {
			return charge - 1
		}
		return charge - sculkGetDecayPenalty(chargePos, origin, charge)
	}
	xpPerGrowthSpawn := sculkGrowthSpawnCost
	if int(rng.NextIntN(int32(xpPerGrowthSpawn))) < charge {
		growthPlacement := placement.BlockPos{X: chargePos.X, Y: chargePos.Y + 1, Z: chargePos.Z}
		growthState := b.sculkGetRandomGrowthState(growthPlacement, rng)
		b.placeState(growthPlacement, growthState)
		// playSound omitted.
	}
	if v := charge - xpPerGrowthSpawn; v > 0 {
		return v
	}
	return 0
}

// sculkGetDecayPenalty ports SculkBlock.getDecayPenalty. All float math, exact.
func sculkGetDecayPenalty(pos, origin placement.BlockPos, charge int) int {
	noGrowthRadius := sculkNoGrowthRadius
	outerDistanceSquared := mthSquareF(float32(math.Sqrt(distSqr(pos, origin))) - float32(noGrowthRadius))
	maxReachSquared := mthSquareF(float32(24 - noGrowthRadius))
	distanceFactor := outerDistanceSquared / maxReachSquared
	if distanceFactor > 1.0 {
		distanceFactor = 1.0
	}
	v := int(float32(charge) * distanceFactor * 0.5)
	if v < 1 {
		return 1
	}
	return v
}

// sculkCanPlaceGrowth ports SculkBlock.canPlaceGrowth: the block ABOVE is air OR a water block
// with WATER fluid; and ≤2 sculk sensors/shriekers in the (-4,0,-4)..(4,2,4) box. Iterates the
// box in BlockPos.betweenClosed order (X fastest, then Y, then Z) — the >2 early-out makes
// order observable. 0 rng draws.
func (b *bodyContext) sculkCanPlaceGrowth(pos placement.BlockPos) bool {
	above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	aboveState := b.getState(above)
	if !(block.IsAir(aboveState) || (isWaterBlock(aboveState) && isWaterFluid(aboveState))) {
		return false
	}
	growthCount := 0
	// betweenClosed(pos.offset(-4,0,-4), pos.offset(4,2,4)): X in [-4..4], Y in [0..2], Z in [-4..4].
	minX, minY, minZ := pos.X-4, pos.Y, pos.Z-4
	width, height, depth := 9, 3, 9
	end := width * height * depth
	for index := 0; index < end; index++ {
		x := index % width
		slice := index / width
		y := slice % height
		z := slice / height
		st := b.getState(placement.BlockPos{X: minX + x, Y: minY + y, Z: minZ + z})
		if isSculkSensor(st) || isSculkShrieker(st) {
			growthCount++
		}
		if growthCount > 2 {
			return false
		}
	}
	return true
}

// sculkGetRandomGrowthState ports SculkBlock.getRandomGrowthState: nextInt(11)==0 →
// SCULK_SHRIEKER (can_summon=isWorldGen=true) else SCULK_SENSOR; waterlog when the cell has a
// non-empty fluid (a water block).
func (b *bodyContext) sculkGetRandomGrowthState(pos placement.BlockPos, rng levelgen.RandomSource) block.StateID {
	waterlogged := isWaterFluid(b.getState(pos))
	if int(rng.NextIntN(11)) == 0 {
		return sculkShriekerState(true, waterlogged)
	}
	if waterlogged {
		return sculkSensorWaterloggedState(true)
	}
	return sculkSensorStateID
}

// ===========================================================================================
//  SculkVeinBlock
// ===========================================================================================

var sculkSameSpaceSpreadOrder = []multifaceSpreadType{spreadSamePosition}

// sculkVeinAttemptUseCharge ports SculkVeinBlock.attemptUseCharge.
func (b *bodyContext) sculkVeinAttemptUseCharge(c *sculkCursor, origin placement.BlockPos, rng levelgen.RandomSource, spreadVeins bool) int {
	if spreadVeins && b.sculkVeinAttemptPlaceSculk(c.pos, rng) {
		return c.charge - 1
	}
	if int(rng.NextIntN(int32(sculkChargeDecayRate))) == 0 {
		return mthFloor(float64(float32(c.charge) * 0.5))
	}
	return c.charge
}

// sculkVeinAttemptPlaceSculk ports SculkVeinBlock.attemptPlaceSculk: for each shuffled
// direction that is a face of the vein and whose neighbour is #sculk_replaceable_world_gen,
// place SCULK there, vein-spread over it, and clear opposite-facing veins. Returns true on the
// first placement.
func (b *bodyContext) sculkVeinAttemptPlaceSculk(pos placement.BlockPos, rng levelgen.RandomSource) bool {
	state := b.getState(pos)
	faces := multifaceUnpackFaces(multifaceSculkVein, state)
	for _, support := range multifaceAllShuffled(rng) {
		if !faces.has(support.d) {
			continue
		}
		supportPos := placement.BlockPos{X: pos.X + support.dx, Y: pos.Y + support.dy, Z: pos.Z + support.dz}
		supportState := b.getState(supportPos)
		if !sculkReplaceableWorldGen(supportState) {
			continue
		}
		b.placeState(supportPos, sculkStateID)
		// pushEntitiesUp omitted (no entities). playSound omitted.
		b.sculkVeinSpreadAll(sculkStateID, supportPos, rng, multifaceDefaultSpreadOrder)
		skip := oppositeDirection(support.d)
		for _, vein := range geodeDirections {
			if vein.d == skip {
				continue
			}
			veinPos := placement.BlockPos{X: supportPos.X + vein.dx, Y: supportPos.Y + vein.dy, Z: supportPos.Z + vein.dz}
			possible := b.getState(veinPos)
			if multifaceIsPlaceBlock(multifaceSculkVein, possible) {
				b.sculkVeinOnDischarged(possible, veinPos)
			}
		}
		return true
	}
	return false
}

// sculkVeinOnDischarged ports SculkVeinBlock.onDischarged: for each set face whose neighbour is
// SCULK, clear that face; if no faces remain, become air (or water if the cell has water).
func (b *bodyContext) sculkVeinOnDischarged(state block.StateID, pos placement.BlockPos) {
	if !multifaceIsPlaceBlock(multifaceSculkVein, state) {
		return
	}
	faces := multifaceUnpackFaces(multifaceSculkVein, state)
	waterlogged := multifaceWaterlogged(multifaceSculkVein, state)
	for _, d := range geodeDirections {
		if !faces.has(d.d) {
			continue
		}
		np := placement.BlockPos{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		if isSculkBlock(b.getState(np)) {
			faces.set(d.d, false)
		}
	}
	if !faces.any() {
		// fluidState empty ? AIR : WATER.
		if isWaterFluid(b.getState(pos)) {
			b.placeState(pos, waterStateID)
		} else {
			b.placeState(pos, b.airState())
		}
		return
	}
	b.placeState(pos, multifaceStateID(multifaceSculkVein, faces, waterlogged))
}

// sculkVeinRegrow ports SculkVeinBlock.regrow: build a fresh SCULK_VEIN with each face whose
// neighbour can attach; waterlog when the existing block had a fluid. Returns false if no face
// attaches.
func (b *bodyContext) sculkVeinRegrow(pos placement.BlockPos, existing block.StateID, faces multifaceFaces) bool {
	var newFaces multifaceFaces
	hasAtLeastOne := false
	for _, d := range geodeDirections {
		if !faces.has(d.d) {
			continue
		}
		np := placement.BlockPos{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		if !b.multifaceCanAttachTo(d.d, np) {
			continue
		}
		newFaces.set(d.d, true)
		hasAtLeastOne = true
	}
	if !hasAtLeastOne {
		return false
	}
	waterlogged := isWaterFluid(existing)
	b.placeState(pos, multifaceStateID(multifaceSculkVein, newFaces, waterlogged))
	return true
}

// sculkVeinSpreadAll ports MultifaceSpreader.spreadAll over the SculkVein spreader config for
// the given spread-type order: for each of Direction.stream() that canSpreadFrom(state,face),
// spreadFromFaceTowardAllDirections; sum the placements. 0 rng draws (spreadAll iterates
// Direction.stream() = geodeDirections, NOT shuffled). Returns the count.
func (b *bodyContext) sculkVeinSpreadAll(state block.StateID, pos placement.BlockPos, _ levelgen.RandomSource, spreadTypes []multifaceSpreadType) int {
	faces := multifaceUnpackFaces(multifaceSculkVein, state)
	otherValid := multifaceIsOtherBlockValidAsSource(multifaceSculkVein, state)
	total := 0
	for _, face := range geodeDirections {
		// canSpreadFrom = isOtherBlockValidAsSource || hasFace(face).
		if !(otherValid || faces.has(face.d)) {
			continue
		}
		total += b.sculkVeinSpreadFromFaceTowardAllDirections(state, pos, face, spreadTypes)
	}
	return total
}

// sculkVeinSpreadFromFaceTowardAllDirections ports spreadFromFaceTowardAllDirections: for each
// of Direction.stream() try spreadFromFaceTowardDirection; count successes.
func (b *bodyContext) sculkVeinSpreadFromFaceTowardAllDirections(state block.StateID, pos placement.BlockPos, fromFace geodeDir, spreadTypes []multifaceSpreadType) int {
	count := 0
	for _, spreadDir := range geodeDirections {
		sp, ok := b.multifaceGetSpreadFromFaceTowardDirection(multifaceSculkVein, state, pos, fromFace, spreadDir, spreadTypes)
		if !ok {
			continue
		}
		if b.multifaceSpreadToFace(multifaceSculkVein, sp) {
			count++
		}
	}
	return count
}

// sculkVeinStateCanBeReplacedGuards ports the SculkVeinSpreaderConfig.stateCanBeReplaced
// guards (the checks BEFORE the super/canBeReplaced fallback):
//   - the block against placementPos+placementDirection is not SCULK / SCULK_CATALYST / MOVING_PISTON.
//   - if sourcePos.distManhattan(placementPos)==2 and the block at sourcePos+placementDir.opposite
//     is face-sturdy toward placementDirection → reject.
//   - existing fluid must be empty or WATER.
//   - existing not in #fire.
// Returns true when the guards allow the (super) fallback to decide.
func (b *bodyContext) sculkVeinStateCanBeReplacedGuards(sourcePos placement.BlockPos, sp spreadPos, existing block.StateID) bool {
	placementDir := geodeForDir(sp.face)
	against := b.getState(placement.BlockPos{X: sp.pos.X + placementDir.dx, Y: sp.pos.Y + placementDir.dy, Z: sp.pos.Z + placementDir.dz})
	// againstState.is(SCULK) || is(SCULK_CATALYST) || is(MOVING_PISTON) → reject.
	if isSculkBlock(against) || isSculkCatalyst(against) || isMovingPiston(against) {
		return false
	}
	if distManhattan(sourcePos, sp.pos) == 2 {
		back := placement.BlockPos{X: sourcePos.X - placementDir.dx, Y: sourcePos.Y - placementDir.dy, Z: sourcePos.Z - placementDir.dz}
		if block.IsFaceSturdy(b.getState(back), placementDir.d, block.SupportFull) {
			return false
		}
	}
	// fluidState empty or WATER.
	if isFluid(existing) && !isWaterFluid(existing) {
		return false
	}
	if isFireBlock(existing) {
		return false
	}
	return true
}

// ===========================================================================================
//  ChargeCursor.getValidMovementPos + NON_CORNER_NEIGHBOURS
// ===========================================================================================

// sculkNonCornerNeighbours is NON_CORNER_NEIGHBOURS: betweenClosed((-1,-1,-1),(1,1,1)) filtered
// to non-corner (at least one axis 0) and non-zero, in BlockPos.betweenClosed order (X fastest).
var sculkNonCornerNeighbours = buildSculkNonCornerNeighbours()

func buildSculkNonCornerNeighbours() []geodeDir {
	out := make([]geodeDir, 0, 18)
	// betweenClosed order: X in [-1..1] fastest, then Y, then Z.
	width, height, depth := 3, 3, 3
	end := width * height * depth
	for index := 0; index < end; index++ {
		x := index%width - 1
		slice := index / width
		y := slice%height - 1
		z := slice/height - 1
		nonCorner := x == 0 || y == 0 || z == 0
		if !nonCorner || (x == 0 && y == 0 && z == 0) {
			continue
		}
		out = append(out, geodeDir{dx: x, dy: y, dz: z})
	}
	return out
}

// sculkGetValidMovementPos ports ChargeCursor.getValidMovementPos: over a shuffled copy of the
// non-corner neighbours, pick the first that is a sculk block, movement-unobstructed, and has
// substrate access; break at that one. Returns (pos, true) when it differs from the origin pos.
func (b *bodyContext) sculkGetValidMovementPos(pos placement.BlockPos, rng levelgen.RandomSource) (placement.BlockPos, bool) {
	offsets := sculkShuffledNonCorner(rng)
	sculkPosition := pos
	for _, off := range offsets {
		neighbour := placement.BlockPos{X: pos.X + off.dx, Y: pos.Y + off.dy, Z: pos.Z + off.dz}
		transferee := b.getState(neighbour)
		if !isSculkBehaviour(transferee) || !b.sculkMovementUnobstructed(pos, neighbour) {
			continue
		}
		sculkPosition = neighbour
		if !b.sculkVeinHasSubstrateAccess(transferee, neighbour) {
			continue
		}
		break
	}
	if sculkPosition == pos {
		return placement.BlockPos{}, false
	}
	return sculkPosition, true
}

// sculkShuffledNonCorner ports getRandomizedNonCornerNeighbourOffsets = Util.shuffledCopy.
func sculkShuffledNonCorner(rng levelgen.RandomSource) []geodeDir {
	out := make([]geodeDir, len(sculkNonCornerNeighbours))
	copy(out, sculkNonCornerNeighbours)
	multifaceShuffle(out, rng)
	return out
}

// sculkMovementUnobstructed ports ChargeCursor.isMovementUnobstructed.
func (b *bodyContext) sculkMovementUnobstructed(from, to placement.BlockPos) bool {
	if distManhattan(from, to) == 1 {
		return true
	}
	dx := to.X - from.X
	dy := to.Y - from.Y
	dz := to.Z - from.Z
	var dirX, dirY, dirZ block.Direction
	if dx < 0 {
		dirX = block.West
	} else {
		dirX = block.East
	}
	if dy < 0 {
		dirY = block.Down
	} else {
		dirY = block.Up
	}
	if dz < 0 {
		dirZ = block.North
	} else {
		dirZ = block.South
	}
	if dx == 0 {
		return b.sculkUnobstructed(from, dirY) || b.sculkUnobstructed(from, dirZ)
	}
	if dy == 0 {
		return b.sculkUnobstructed(from, dirX) || b.sculkUnobstructed(from, dirZ)
	}
	return b.sculkUnobstructed(from, dirX) || b.sculkUnobstructed(from, dirY)
}

// sculkUnobstructed ports ChargeCursor.isUnobstructed: !getBlockState(from+dir).isFaceSturdy(dir.opposite).
func (b *bodyContext) sculkUnobstructed(from placement.BlockPos, dir block.Direction) bool {
	g := geodeForDir(dir)
	testPos := placement.BlockPos{X: from.X + g.dx, Y: from.Y + g.dy, Z: from.Z + g.dz}
	return !block.IsFaceSturdy(b.getState(testPos), oppositeDirection(dir), block.SupportFull)
}

// sculkVeinHasSubstrateAccess ports SculkVeinBlock.hasSubstrateAccess: state is SCULK_VEIN and
// at least one set face has a #sculk_replaceable (level tag) neighbour. NOTE: the jar uses
// BlockTags.SCULK_REPLACEABLE here (not the world_gen tag) — a fixed level tag.
func (b *bodyContext) sculkVeinHasSubstrateAccess(state block.StateID, pos placement.BlockPos) bool {
	if !multifaceIsPlaceBlock(multifaceSculkVein, state) {
		return false
	}
	faces := multifaceUnpackFaces(multifaceSculkVein, state)
	for _, d := range geodeDirections {
		if !faces.has(d.d) {
			continue
		}
		np := placement.BlockPos{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		if sculkReplaceable(b.getState(np)) {
			return true
		}
	}
	return false
}

var (
	sculkReplaceableOnce sync.Once
	sculkReplaceableSet  map[block.StateID]bool
)

// sculkReplaceable reports state.is(#sculk_replaceable) (the level, non-worldgen tag).
func sculkReplaceable(st block.StateID) bool {
	sculkReplaceableOnce.Do(func() {
		sculkReplaceableSet = resolveTagStateSet("sculk_replaceable")
	})
	return sculkReplaceableSet[st]
}

// ===========================================================================================
//  block identity + fluid helpers
// ===========================================================================================

func isSculkBlock(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.Sculk)
	return ok
}

func isSculkCatalyst(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.SculkCatalyst)
	return ok
}

func isSculkSensor(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.SculkSensor)
	return ok
}

func isSculkShrieker(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.SculkShrieker)
	return ok
}

// isSculkBehaviour reports getBlock() instanceof SculkBehaviour. JAR-CONFIRMED (26.2): ONLY
// SculkBlock and SculkVeinBlock implement SculkBehaviour — SculkCatalyst / SculkSensor /
// SculkShrieker do NOT (they extend BaseEntityBlock). So canSpreadFrom's "start is a
// SculkBehaviour" early-return and getValidMovementPos's transferee-is-SculkBehaviour gate
// only accept SCULK + SCULK_VEIN.
func isSculkBehaviour(st block.StateID) bool {
	return isSculkBlock(st) || multifaceIsPlaceBlock(multifaceSculkVein, st)
}

func isMovingPiston(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.MovingPiston)
	return ok
}

// isFluid reports the state has a non-empty fluid (a water or lava block).
func isFluid(st block.StateID) bool {
	return isWaterFluid(st) || dripstoneIsLava(st)
}

// isWaterFluid reports the state's fluid is WATER (a water block, any level; waterlogged
// blocks would also count in the jar but the worldgen cave sculk context only sees plain
// water). Reuses the water-block identity check.
func isWaterFluid(st block.StateID) bool { return isWaterBlock(st) }

// isFireBlock reports state.is(#fire) — fire/soul_fire. The worldgen sculk context never sees
// fire, but the guard is ported faithfully.
func isFireBlock(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	id := block.StateList[st].ID()
	return id == "minecraft:fire" || id == "minecraft:soul_fire"
}

// multifaceAvailableFaces ports MultifaceBlock.availableFaces(state): the set of set faces of a
// multiface state (empty for a non-multiface block). Used to refresh the cursor facings.
func multifaceAvailableFaces(st block.StateID) multifaceFaces {
	if multifaceIsPlaceBlock(multifaceSculkVein, st) {
		return multifaceUnpackFaces(multifaceSculkVein, st)
	}
	if multifaceIsPlaceBlock(multifaceGlowLichen, st) {
		return multifaceUnpackFaces(multifaceGlowLichen, st)
	}
	return multifaceFaces{}
}

// ---- geometry helpers (BlockPos distance functions) ----

func distManhattan(a, b placement.BlockPos) int {
	return abs(a.X-b.X) + abs(a.Y-b.Y) + abs(a.Z-b.Z)
}

func distChessboard(a, b placement.BlockPos) int {
	return max(max(abs(a.X-b.X), abs(a.Y-b.Y)), abs(a.Z-b.Z))
}

func distSqr(a, b placement.BlockPos) float64 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return dx*dx + dy*dy + dz*dz
}

// closerThan ports BlockPos.closerThan(other, distance): distSqr <= distance*distance.
func closerThan(a, b placement.BlockPos, distance float64) bool {
	return distSqr(a, b) < distance*distance || distSqr(a, b) == distance*distance
}

// closerThanXZ ports the worldgen cylinder test closerThan(new Vec3i(originX, thisY, originZ),
// 15.0): horizontal distance (Y forced equal), so it is |dx|²+|dz|² <= 15².
func closerThanXZ(a, origin placement.BlockPos, distance float64) bool {
	dx := float64(a.X - origin.X)
	dz := float64(a.Z - origin.Z)
	d := dx*dx + dz*dz
	return d <= distance*distance
}

// mthSquareF ports Mth.square(float): f*f.
func mthSquareF(f float32) float32 { return f * f }
