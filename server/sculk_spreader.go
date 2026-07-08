package server

// sculk_spreader.go - the LIVE (runtime) SculkSpreader charge/spread simulation, a 1:1 port of
// net.minecraft.world.level.block.SculkSpreader (createLevelSpreader path, isWorldGeneration false)
// plus SculkSpreader.ChargeCursor plus the SculkBehaviour dispatch (SculkBehaviour.DEFAULT and
// SculkBlock) over the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR + javap -c).
//
//   - SculkSpreader.createLevelSpreader: ctor(isWorldGeneration=false, replaceable=#sculk_replaceable,
//       growthSpawnCost=10, noGrowthRadius=4, chargeDecayRate=10, additionalDecayRate=5).
//       [VERIFIED javap: iconst_0; SCULK_REPLACEABLE; bipush 10; iconst_4; bipush 10; iconst_5.]
//   - SculkSpreader.addCursors/addCursor (split charge into MAX_CHARGE chunks; cap MAX_CURSORS=32).
//   - SculkSpreader.updateCursors (LEVEL path: the merge branch is LIVE, isWorldGeneration false).
//   - ChargeCursor.{update,shouldUpdate,mergeWith,getValidMovementPos,isMovementUnobstructed,
//       isUnobstructed,isPosUnreasonable,NON_CORNER_NEIGHBOURS,getRandomizedNonCornerNeighbourOffsets}.
//   - SculkBehaviour.DEFAULT.{attemptUseCharge,updateDecayDelay,getSculkSpreadDelay,onDischarged}.
//   - SculkBlock.{attemptUseCharge,canPlaceGrowth,getRandomGrowthState,getDecayPenalty}.
//
// RNG SOURCE: every draw is on the shared LEVEL RandomSource (region.levelRandom, the Level.random
// analogue), the same source SculkSpreader.updateCursors is handed (level.getRandom()). It is NEVER a
// per-entity stream, so the TestPluginPigEqualsGoNativePig oracle (pinned to the pig own RNG) is
// unperturbed: the sculk draws only ever run while a sculk catalyst/block is ticking.
//
// DEFERRED (cited, no observable-gameplay loss for the catalyst-spread core):
//   - The SCULK_VEIN multiface spread (SculkVeinBlock.attemptUseCharge/attemptPlaceSculk/onDischarged
//     plus the SculkBehaviour.DEFAULT.attemptSpreadVein branch). The live multiface machinery lives in
//     package world (feature_multiface.go). Here attemptSpreadVein is a no-op, so a cursor over plain
//     ground / SCULK uses the DEFAULT / SculkBlock attemptUseCharge directly (the observable "catalyst
//     converts ground to sculk and grows sensors/shriekers" path). CITE: SculkBehaviour.attemptSpreadVein.
//   - levelEvent (particle 3006) / playSound: client cosmetic, never a block write. Omitted.

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Level-spreader constants (SculkSpreader.createLevelSpreader ctor args, VERIFIED javap).
const (
	sculkLevelGrowthSpawnCost = 10 // growthSpawnCost
	sculkLevelNoGrowthRadius  = 4  // noGrowthRadius
	sculkLevelChargeDecayRate = 10 // chargeDecayRate
	sculkLevelAdditionalDecay = 5  // additionalDecayRate
)

// SculkSpreader shared constants (SculkSpreader static fields, VERIFIED javap).
const (
	sculkLiveMaxCursors     = 32
	sculkLiveMaxCharge      = 1000
	sculkLiveMaxDistance    = 1024
	sculkLiveMaxUpdateDelay = 1
)

type sculkLiveCursor struct {
	pos         pk.Position
	charge      int
	updateDelay int
	decayDelay  int
}

type sculkLiveSpreader struct {
	cursors []*sculkLiveCursor
}

// addCursors ports SculkSpreader.addCursors: split charge into MAX_CHARGE chunks.
func (s *sculkLiveSpreader) addCursors(startPos pk.Position, charge int) {
	for charge > 0 {
		current := charge
		if current > sculkLiveMaxCharge {
			current = sculkLiveMaxCharge
		}
		s.addCursor(&sculkLiveCursor{pos: startPos, charge: current, decayDelay: 1, updateDelay: 0})
		charge -= current
	}
}

// addCursor ports SculkSpreader.addCursor: append unless at the 32-cursor cap.
func (s *sculkLiveSpreader) addCursor(c *sculkLiveCursor) {
	if len(s.cursors) >= sculkLiveMaxCursors {
		return
	}
	s.cursors = append(s.cursors, c)
}

// sculkLiveUpdateCursors ports SculkSpreader.updateCursors (LEVEL path). Each cursor: skip if
// pos-unreasonable; update; if charge<=0 drop (levelEvent 3006 deferred); else same-pos MERGE
// bookkeeping (isWorldGeneration false branch is LIVE): combined charge fitting one cursor merges
// into the kept one, else both kept and the map tracks the lower-charge pos-cursor (so a later
// same-pos cursor merges into the fuller one). CITE: SculkSpreader.updateCursors.
func (t *TickLoop) sculkLiveUpdateCursors(s *sculkLiveSpreader, origin pk.Position, spreadVeins bool) {
	if len(s.cursors) == 0 {
		return
	}
	kept := make([]*sculkLiveCursor, 0, len(s.cursors))
	posToCursor := make(map[pk.Position]*sculkLiveCursor)
	for _, cursor := range s.cursors {
		if t.sculkLiveIsPosUnreasonable(cursor, origin) {
			continue
		}
		t.sculkLiveCursorUpdate(cursor, origin, spreadVeins)
		if cursor.charge <= 0 {
			continue
		}
		pos := cursor.pos
		existing := posToCursor[pos]
		if existing == nil {
			posToCursor[pos] = cursor
			kept = append(kept, cursor)
			continue
		}
		if cursor.charge+existing.charge <= sculkLiveMaxCharge {
			existing.mergeWith(cursor)
			continue
		}
		kept = append(kept, cursor)
		if cursor.charge < existing.charge {
			posToCursor[pos] = cursor
		}
	}
	s.cursors = kept
}

// mergeWith ports ChargeCursor.mergeWith: charge += other.charge; other.charge = 0; keep the smaller
// updateDelay and decayDelay. CITE: ChargeCursor.mergeWith.
func (c *sculkLiveCursor) mergeWith(other *sculkLiveCursor) {
	c.charge += other.charge
	other.charge = 0
	if other.updateDelay < c.updateDelay {
		c.updateDelay = other.updateDelay
	}
	if other.decayDelay < c.decayDelay {
		c.decayDelay = other.decayDelay
	}
}

// sculkLiveIsPosUnreasonable ports ChargeCursor.isPosUnreasonable: chessboard distance over 1024.
func (t *TickLoop) sculkLiveIsPosUnreasonable(c *sculkLiveCursor, origin pk.Position) bool {
	return sculkChessboard(c.pos, origin) > sculkLiveMaxDistance
}

// sculkLiveCursorUpdate ports ChargeCursor.update (LEVEL path). shouldUpdate reduces to charge>0 on
// the live server (a cursor only exists in a ticking chunk). Then the updateDelay gate, attemptSpread
// Vein (DEFERRED no-op), attemptUseCharge, discharge, and the move to a valid neighbour. CITE:
// ChargeCursor.update.
func (t *TickLoop) sculkLiveCursorUpdate(c *sculkLiveCursor, origin pk.Position, spreadVeins bool) {
	if c.charge <= 0 {
		return
	}
	if c.updateDelay > 0 {
		c.updateDelay--
		return
	}
	if t.world() == nil {
		return
	}
	currentState, ok := t.world().GetBlock(c.pos, dimMinY)
	if !ok {
		return
	}
	behaviour := sculkLiveBehaviourOf(currentState)
	_ = spreadVeins // attemptSpreadVein: SCULK_VEIN multiface spread DEFERRED (cited header) -> no-op.
	c.charge = t.sculkLiveAttemptUseCharge(behaviour, c, origin)
	if c.charge <= 0 {
		return // onDischarged: DEFAULT + SCULK are no-ops (only the deferred SCULK_VEIN overrides it).
	}
	transferPos, moved := t.sculkLiveGetValidMovementPos(c.pos)
	if moved {
		c.pos = transferPos
		// isWorldGeneration false: the worldgen-only 15-block cylinder cutoff is skipped.
		currentState, ok = t.world().GetBlock(transferPos, dimMinY)
		if !ok {
			return
		}
	}
	_ = currentState // the availableFaces refresh feeds only the deferred vein path.
	c.decayDelay = behaviour.updateDecayDelay(c.decayDelay)
	c.updateDelay = sculkLiveMaxUpdateDelay
}

// ---- SculkBehaviour dispatch (live) ----

type sculkLiveBehaviourKind int

const (
	sculkLiveBehaviourDefault sculkLiveBehaviourKind = iota
	sculkLiveBehaviourSculk
)

type sculkLiveBehaviour struct {
	kind sculkLiveBehaviourKind
}

// sculkLiveBehaviourOf ports ChargeCursor.getBlockBehaviour: SCULK -> SculkBlock; SCULK_VEIN ->
// SculkVeinBlock (DEFERRED, folded into DEFAULT); SCULK_CATALYST/other -> SculkBehaviour.DEFAULT.
func sculkLiveBehaviourOf(st block.StateID) sculkLiveBehaviour {
	if block.IsSculk(st) {
		return sculkLiveBehaviour{kind: sculkLiveBehaviourSculk}
	}
	return sculkLiveBehaviour{kind: sculkLiveBehaviourDefault}
}

// updateDecayDelay ports the behaviour updateDecayDelay: DEFAULT = max(age-1, 0); SculkBlock inherits
// the interface default = 1. CITE: SculkBehaviour.DEFAULT.updateDecayDelay / SculkBehaviour default.
func (b sculkLiveBehaviour) updateDecayDelay(age int) int {
	if b.kind == sculkLiveBehaviourDefault {
		if age-1 > 0 {
			return age - 1
		}
		return 0
	}
	return 1
}

// sculkLiveAttemptUseCharge dispatches the per-behaviour attemptUseCharge.
func (t *TickLoop) sculkLiveAttemptUseCharge(b sculkLiveBehaviour, c *sculkLiveCursor, origin pk.Position) int {
	if b.kind == sculkLiveBehaviourSculk {
		return t.sculkLiveBlockAttemptUseCharge(c, origin)
	}
	// SculkBehaviour.DEFAULT.attemptUseCharge: decayDelay over 0 keeps charge, else 0.
	if c.decayDelay > 0 {
		return c.charge
	}
	return 0
}

// sculkLiveBlockAttemptUseCharge ports SculkBlock.attemptUseCharge (level spreader constants). CITE:
// SculkBlock.attemptUseCharge (VERIFIED javap this session).
func (t *TickLoop) sculkLiveBlockAttemptUseCharge(c *sculkLiveCursor, origin pk.Position) int {
	charge := c.charge
	// if (charge != 0 && random.nextInt(chargeDecayRate) != 0) return charge;
	if charge != 0 && t.sculkLiveNextInt(sculkLevelChargeDecayRate) != 0 {
		return charge
	}
	chargePos := c.pos
	isCloseToCatalyst := sculkCloserThan(chargePos, origin, float64(sculkLevelNoGrowthRadius))
	if isCloseToCatalyst || !t.sculkLiveCanPlaceGrowth(chargePos) {
		if t.sculkLiveNextInt(sculkLevelAdditionalDecay) != 0 {
			return charge
		}
		if isCloseToCatalyst {
			return charge - 1
		}
		return charge - sculkLiveGetDecayPenalty(chargePos, origin, charge)
	}
	xpPerGrowthSpawn := sculkLevelGrowthSpawnCost
	if t.sculkLiveNextInt(xpPerGrowthSpawn) < charge {
		growthPlacement := pk.Position{X: chargePos.X, Y: chargePos.Y + 1, Z: chargePos.Z}
		growthState := t.sculkLiveGetRandomGrowthState(growthPlacement)
		if t.world().SetBlock(growthPlacement, growthState, dimMinY) {
			t.broadcastBlockUpdate(growthPlacement, growthState)
			// A newly-grown SculkSensor / SculkShrieker registers its BE so it ticks/listens.
			t.createBlockEntityOnPlace(growthPlacement, growthState)
		}
	}
	if v := charge - xpPerGrowthSpawn; v > 0 {
		return v
	}
	return 0
}

// sculkLiveGetDecayPenalty ports SculkBlock.getDecayPenalty (level noGrowthRadius=4). CITE.
func sculkLiveGetDecayPenalty(pos, origin pk.Position, charge int) int {
	noGrowthRadius := sculkLevelNoGrowthRadius
	outerDistanceSquared := sculkSquareF(float32(math.Sqrt(sculkDistSqr(pos, origin))) - float32(noGrowthRadius))
	maxReachSquared := sculkSquareF(float32(24 - noGrowthRadius))
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

// sculkLiveCanPlaceGrowth ports SculkBlock.canPlaceGrowth: block ABOVE is air OR water; and at most 2
// sculk sensors/shriekers in the (-4,0,-4)..(4,2,4) box (BlockPos.betweenClosed order, X fastest --
// the over-2 early-out makes the order observable). CITE: SculkBlock.canPlaceGrowth.
func (t *TickLoop) sculkLiveCanPlaceGrowth(pos pk.Position) bool {
	above := pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	aboveState, ok := t.world().GetBlock(above, dimMinY)
	if !ok {
		return false
	}
	if !(block.IsAir(aboveState) || t.isWaterAt(above.X, above.Y, above.Z)) {
		return false
	}
	growthCount := 0
	minX, minY, minZ := pos.X-4, pos.Y, pos.Z-4
	width, height, depth := 9, 3, 9
	end := width * height * depth
	for index := 0; index < end; index++ {
		x := index % width
		slice := index / width
		y := slice % height
		z := slice / height
		st, ok := t.world().GetBlock(pk.Position{X: minX + x, Y: minY + y, Z: minZ + z}, dimMinY)
		if !ok {
			continue
		}
		if block.IsAnySculkSensor(st) || block.IsSculkShrieker(st) {
			growthCount++
		}
		if growthCount > 2 {
			return false
		}
	}
	return true
}

// sculkLiveGetRandomGrowthState ports SculkBlock.getRandomGrowthState (level: isWorldGen false, so a
// grown SCULK_SHRIEKER has CAN_SUMMON false): nextInt(11)==0 -> SCULK_SHRIEKER else SCULK_SENSOR;
// waterlog when the growth cell is water. CITE: SculkBlock.getRandomGrowthState.
func (t *TickLoop) sculkLiveGetRandomGrowthState(pos pk.Position) block.StateID {
	waterlogged := t.isWaterAt(pos.X, pos.Y, pos.Z)
	if t.sculkLiveNextInt(11) == 0 {
		return block.ToStateID[block.SculkShrieker{CanSummon: false, Shrieking: false, Waterlogged: block.Boolean(waterlogged)}]
	}
	return block.ToStateID[block.SculkSensor{Power: 0, SculkSensorPhase: block.SculkSensorPhaseInactive, Waterlogged: block.Boolean(waterlogged)}]
}

// ---- ChargeCursor.getValidMovementPos + NON_CORNER_NEIGHBOURS (live) ----

type sculkOffset struct{ dx, dy, dz int }

// sculkLiveNonCornerNeighbours is NON_CORNER_NEIGHBOURS: the 18 non-corner offsets in
// betweenClosed((-1,-1,-1),(1,1,1)) order (X fastest), each with at least one axis 0, non-zero.
var sculkLiveNonCornerNeighbours = sculkBuildNonCornerNeighbours()

func sculkBuildNonCornerNeighbours() []sculkOffset {
	out := make([]sculkOffset, 0, 18)
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
		out = append(out, sculkOffset{dx: x, dy: y, dz: z})
	}
	return out
}

// sculkLiveGetValidMovementPos ports ChargeCursor.getValidMovementPos: over a shuffled copy of the
// non-corner neighbours, pick the first SculkBehaviour neighbour that is movement-unobstructed; break
// at the one that also has substrate access. Returns (pos, true) when it differs from the start pos.
func (t *TickLoop) sculkLiveGetValidMovementPos(pos pk.Position) (pk.Position, bool) {
	offsets := t.sculkLiveShuffledNonCorner()
	sculkPosition := pos
	for _, off := range offsets {
		neighbour := pk.Position{X: pos.X + off.dx, Y: pos.Y + off.dy, Z: pos.Z + off.dz}
		transferee, ok := t.world().GetBlock(neighbour, dimMinY)
		if !ok {
			continue
		}
		if !sculkLiveIsBehaviour(transferee) || !t.sculkLiveMovementUnobstructed(pos, neighbour) {
			continue
		}
		sculkPosition = neighbour
		if !t.sculkLiveHasSubstrateAccess(transferee) {
			continue
		}
		break
	}
	if sculkPosition == pos {
		return pk.Position{}, false
	}
	return sculkPosition, true
}

// sculkLiveShuffledNonCorner ports Util.shuffledCopy: Fisher-Yates from the end (i = size-1 down to 1,
// swap i with nextInt(i+1)).
func (t *TickLoop) sculkLiveShuffledNonCorner() []sculkOffset {
	out := make([]sculkOffset, len(sculkLiveNonCornerNeighbours))
	copy(out, sculkLiveNonCornerNeighbours)
	for i := len(out) - 1; i >= 1; i-- {
		j := t.sculkLiveNextInt(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// sculkLiveIsBehaviour reports getBlock() instanceof SculkBehaviour: ONLY SculkBlock + SculkVeinBlock
// implement it. CITE: SculkBlock / SculkVeinBlock implement SculkBehaviour.
func sculkLiveIsBehaviour(st block.StateID) bool {
	return block.IsSculk(st) || block.IsSculkVein(st)
}

// sculkLiveMovementUnobstructed ports ChargeCursor.isMovementUnobstructed.
func (t *TickLoop) sculkLiveMovementUnobstructed(from, to pk.Position) bool {
	if sculkManhattan(from, to) == 1 {
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
		return t.sculkLiveUnobstructed(from, dirY) || t.sculkLiveUnobstructed(from, dirZ)
	}
	if dy == 0 {
		return t.sculkLiveUnobstructed(from, dirX) || t.sculkLiveUnobstructed(from, dirZ)
	}
	return t.sculkLiveUnobstructed(from, dirX) || t.sculkLiveUnobstructed(from, dirY)
}

// sculkLiveUnobstructed ports ChargeCursor.isUnobstructed: the face of from+dir toward dir.opposite is
// not sturdy. CITE: ChargeCursor.isUnobstructed.
func (t *TickLoop) sculkLiveUnobstructed(from pk.Position, dir block.Direction) bool {
	off := sculkDirOffset(dir)
	testPos := pk.Position{X: from.X + off.dx, Y: from.Y + off.dy, Z: from.Z + off.dz}
	st, ok := t.world().GetBlock(testPos, dimMinY)
	if !ok {
		return true
	}
	return !block.IsFaceSturdy(st, sculkOppositeDir(dir), block.SupportFull)
}

// sculkLiveHasSubstrateAccess is the getValidMovementPos break test: a SCULK carpet cell is always a
// valid destination; a SCULK_VEIN transferee is the DEFERRED vein path, so the loop keeps looking past
// it. CITE: ChargeCursor.getValidMovementPos.
func (t *TickLoop) sculkLiveHasSubstrateAccess(state block.StateID) bool {
	return block.IsSculk(state)
}

// ---- level RNG + geometry helpers ----

// sculkLiveNextInt draws random.nextInt(n) on the shared LEVEL RandomSource (region.levelRandom). A
// nil region/random (a test driving the spreader without a region) falls back to 0 (deterministic).
func (t *TickLoop) sculkLiveNextInt(n int) int {
	if n <= 0 {
		return 0
	}
	if t.cur() != nil && t.cur().levelRandom != nil {
		return int(t.cur().levelRandom.NextIntN(int32(n)))
	}
	return 0
}

func sculkManhattan(a, b pk.Position) int {
	return sculkAbs(a.X-b.X) + sculkAbs(a.Y-b.Y) + sculkAbs(a.Z-b.Z)
}

func sculkChessboard(a, b pk.Position) int {
	return sculkMax(sculkMax(sculkAbs(a.X-b.X), sculkAbs(a.Y-b.Y)), sculkAbs(a.Z-b.Z))
}

func sculkDistSqr(a, b pk.Position) float64 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return dx*dx + dy*dy + dz*dz
}

// sculkCloserThan ports BlockPos.closerThan(other, distance): distSqr <= distance*distance.
func sculkCloserThan(a, b pk.Position, distance float64) bool {
	return sculkDistSqr(a, b) <= distance*distance
}

func sculkSquareF(f float32) float32 { return f * f }

func sculkAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sculkMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sculkDirOffset(d block.Direction) sculkOffset {
	switch d {
	case block.Down:
		return sculkOffset{0, -1, 0}
	case block.Up:
		return sculkOffset{0, 1, 0}
	case block.North:
		return sculkOffset{0, 0, -1}
	case block.South:
		return sculkOffset{0, 0, 1}
	case block.West:
		return sculkOffset{-1, 0, 0}
	case block.East:
		return sculkOffset{1, 0, 0}
	}
	return sculkOffset{}
}

func sculkOppositeDir(d block.Direction) block.Direction {
	switch d {
	case block.Down:
		return block.Up
	case block.Up:
		return block.Down
	case block.North:
		return block.South
	case block.South:
		return block.North
	case block.West:
		return block.East
	case block.East:
		return block.West
	}
	return d
}
