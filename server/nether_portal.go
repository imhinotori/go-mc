package server

// nether_portal.go — NETHER PORTAL frame detection + flint&steel ignite + portal-block fill, ported
// 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, read via CFR this session). The
// observable target: build an obsidian frame, right-click the inside floor with flint&steel, and the
// interior fills with minecraft:nether_portal blocks of the correct AXIS (the frame validates), exactly
// as vanilla. The portal BLOCKS are this file's milestone. DIMENSION TRAVEL now lives in
// dimension_travel.go: NetherPortalBlock.entityInside / the portal dwell timer / Entity.changeDimension
// are ported there (tickNetherPortal + changeDimension). STILL DEFERRED here (cited, left out): the
// PortalForcer destination search+create (findClosestPortalPosition/createPortal -- the 128-block POI
// search + frame build on arrival), so a traveler currently lands at the coordinate-scaled column with
// a fixed safe Y rather than at a searched-or-freshly-built destination portal.
//
// CITED JAR CLASSES (all CFR-decompiled this session):
//   - net.minecraft.world.level.portal.PortalShape: MIN_WIDTH=2, MAX_WIDTH=21, MIN_HEIGHT=3,
//     MAX_HEIGHT=21, FRAME = (state -> state.is(OBSIDIAN)); findEmptyPortalShape / findPortalShape /
//     findAnyShape / calculateBottomLeft / calculateWidth / getDistanceUntilEdgeAboveFrame /
//     calculateHeight / getDistanceUntilTop / hasTopFrame / isEmpty / isValid / createPortalBlocks.
//   - net.minecraft.world.level.block.BaseFireBlock: canBePlacedAt / isPortal / getState /
//     onPlace (findEmptyPortalShape(level, pos, Direction.Axis.X) -> createPortalBlocks); inPortalDimension.
//   - net.minecraft.world.item.FlintAndSteelItem.useOn: the ignite entry (fire place / portal spawn).
//   - net.minecraft.core.Direction.fromYRot / from2DDataValue (BY_2D_DATA = SOUTH,WEST,NORTH,EAST).

import (
	"math"
	mrand "math/rand/v2"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// PortalShape constants — the exact frame bounds from PortalShape.
//
//	[CFR PortalShape: MIN_WIDTH=2, MAX_WIDTH=21, MIN_HEIGHT=3, MAX_HEIGHT=21.]
const (
	portalMinWidth  = 2
	portalMaxWidth  = 21
	portalMinHeight = 3
	portalMaxHeight = 21
)

// flintAndSteelUseSoundID is SoundEvents.FLINTANDSTEEL_USE ("item.flintandsteel.use"), registry id 642
// (data/registryid/soundevent.go / data/soundid/soundid.go both agree: index 642). CITE:
// FlintAndSteelItem.useOn playSound(SoundEvents.FLINTANDSTEEL_USE).
const flintAndSteelUseSoundID = 642

// soundSourceBlocks is SoundSource.BLOCKS.ordinal() == 4 (enum order MASTER0 MUSIC1 RECORDS2 WEATHER3
// BLOCKS4 HOSTILE5 NEUTRAL6 PLAYERS7 AMBIENT8 VOICE9 UI10). FlintAndSteelItem.useOn plays the ignite
// sound on SoundSource.BLOCKS. CITE: SoundSource enum order.
const soundSourceBlocks = 4

// portalDir is a Direction as an integer offset triple, sufficient for the axis-aligned frame scan.
// Only the six values PortalShape's scan uses are needed (rightDir WEST/SOUTH, UP, DOWN, and their
// opposites). CITE: net.minecraft.core.Direction unit vectors.
type portalDir struct{ dx, dy, dz int }

var (
	portalWest  = portalDir{-1, 0, 0} // Direction.WEST  (rightDir for Axis.X)
	portalEast  = portalDir{1, 0, 0}  // Direction.EAST  (WEST.getOpposite == leftDir for Axis.X)
	portalSouth = portalDir{0, 0, 1}  // Direction.SOUTH (rightDir for Axis.Z)
	portalNorth = portalDir{0, 0, -1} // Direction.NORTH (SOUTH.getOpposite == leftDir for Axis.Z)
	portalUp    = portalDir{0, 1, 0}  // Direction.UP
	portalDown  = portalDir{0, -1, 0} // Direction.DOWN
)

// opposite is Direction.getOpposite for the horizontal rightDirs used by the scan.
func (d portalDir) opposite() portalDir {
	switch d {
	case portalWest:
		return portalEast
	case portalSouth:
		return portalNorth
	case portalEast:
		return portalWest
	case portalNorth:
		return portalSouth
	default:
		return d
	}
}

// clockWise is Direction.getClockWise() for the horizontal directions (the no-arg form delegates to
// getClockWiseY -- clockwise viewed from above): NORTH->EAST->SOUTH->WEST->NORTH. createPortal uses it to
// pick the frame sideways axis (direction.getClockWise()). CITE: net.minecraft.core.Direction.getClockWise.
func (d portalDir) clockWise() portalDir {
	switch d {
	case portalNorth:
		return portalEast
	case portalEast:
		return portalSouth
	case portalSouth:
		return portalWest
	case portalWest:
		return portalNorth
	default:
		return d
	}
}


// portalMove offsets pos by n steps of dir (BlockPos.relative(direction, n) / MutableBlockPos.move).
func portalMove(pos pk.Position, dir portalDir, n int) pk.Position {
	return pk.Position{X: pos.X + dir.dx*n, Y: pos.Y + dir.dy*n, Z: pos.Z + dir.dz*n}
}

// portalShape mirrors net.minecraft.world.level.portal.PortalShape's instance fields: the resolved
// axis, the rightDir along which width was scanned, the bottomLeft corner, and the measured width /
// height / interior portal-block count. An invalid resolution has width==0 || height==0.
type portalShape struct {
	axis            block.Axis // Direction.Axis (X or Z)
	rightDir        portalDir
	bottomLeft      pk.Position
	width           int
	height          int
	numPortalBlocks int
}

// portalIsObsidian is PortalShape.FRAME: state.is(Blocks.OBSIDIAN). CITE: PortalShape.FRAME.
func portalIsObsidian(s block.StateID, ok bool) bool {
	if !ok || int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, isObsidian := block.StateList[s].(block.Obsidian)
	return isObsidian
}

// portalIsEmpty is PortalShape.isEmpty: state.isAir() || state.is(BlockTags.FIRE) ||
// state.is(Blocks.NETHER_PORTAL). BlockTags.FIRE == {fire, soul_fire}. An unreadable/unloaded cell is
// treated as NOT empty (the scan breaks there, matching an out-of-world air==false edge). CITE:
// PortalShape.isEmpty.
func portalIsEmpty(s block.StateID, ok bool) bool {
	if !ok || int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	switch block.StateList[s].(type) {
	case block.Air, block.CaveAir, block.VoidAir, // isAir()
		block.Fire, block.SoulFire, // BlockTags.FIRE
		block.NetherPortal: // Blocks.NETHER_PORTAL
		return true
	default:
		return false
	}
}

// portalFrameAt reads the world block at pos and applies the FRAME (obsidian) predicate. A nil world
// (test loop without a manager) yields "not frame".
func (t *TickLoop) portalFrameAt(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	return portalIsObsidian(s, ok)
}

// portalEmptyAt reads the world block at pos and applies the isEmpty predicate.
func (t *TickLoop) portalEmptyAt(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	return portalIsEmpty(s, ok)
}

// findEmptyPortalShape is PortalShape.findEmptyPortalShape(level, pos, preferredAxis): find a shape
// that isValid() && numPortalBlocks == 0 (an EMPTY frame ready to be lit). Returns the shape and true
// on success. CITE: PortalShape.findEmptyPortalShape / findPortalShape.
func (t *TickLoop) findEmptyPortalShape(pos pk.Position, preferredAxis block.Axis) (portalShape, bool) {
	valid := func(sh portalShape) bool { return sh.isValid() && sh.numPortalBlocks == 0 }

	// firstAxis = Optional.of(findAnyShape(preferredAxis)).filter(valid).
	first := t.findAnyShape(pos, preferredAxis)
	if valid(first) {
		return first, true
	}
	// otherAxis = preferredAxis == X ? Z : X; return Optional.of(findAnyShape(otherAxis)).filter(valid).
	other := block.X
	if preferredAxis == block.X {
		other = block.Z
	}
	second := t.findAnyShape(pos, other)
	if valid(second) {
		return second, true
	}
	return portalShape{}, false
}

// findAnyShape is PortalShape.findAnyShape(level, pos, axis): scan from pos to resolve the bottomLeft,
// width, and height for the given axis. rightDir = axis==X ? WEST : SOUTH. Returns a possibly-invalid
// shape (width/height 0) when the frame does not close. CITE: PortalShape.findAnyShape.
func (t *TickLoop) findAnyShape(pos pk.Position, axis block.Axis) portalShape {
	rightDir := portalSouth
	if axis == block.X {
		rightDir = portalWest
	}
	bottomLeft, ok := t.calculateBottomLeft(rightDir, pos)
	if !ok {
		return portalShape{axis: axis, rightDir: rightDir, bottomLeft: pos}
	}
	width := t.calculateWidth(bottomLeft, rightDir)
	if width == 0 {
		return portalShape{axis: axis, rightDir: rightDir, bottomLeft: bottomLeft}
	}
	height, portalBlockCount := t.calculateHeight(bottomLeft, rightDir, width)
	return portalShape{
		axis:            axis,
		rightDir:        rightDir,
		bottomLeft:      bottomLeft,
		width:           width,
		height:          height,
		numPortalBlocks: portalBlockCount,
	}
}

// calculateBottomLeft is PortalShape.calculateBottomLeft: drop down through empty cells (bounded to
// pos.getY()-21 or the world floor), then walk leftDir (rightDir.getOpposite) to the frame edge. Returns
// the bottom-left interior corner, or ok=false if the left edge does not close. CITE:
// PortalShape.calculateBottomLeft.
func (t *TickLoop) calculateBottomLeft(rightDir portalDir, pos pk.Position) (pk.Position, bool) {
	// minY = max(level.getMinY(), pos.getY() - 21). level.getMinY() == dimMinY for v1's overworld.
	minY := dimMinY
	if pos.Y-21 > minY {
		minY = pos.Y - 21
	}
	for pos.Y > minY && t.portalEmptyAt(portalMove(pos, portalDown, 1)) {
		pos = portalMove(pos, portalDown, 1)
	}
	leftDir := rightDir.opposite()
	edge := t.getDistanceUntilEdgeAboveFrame(pos, leftDir) - 1
	if edge < 0 {
		return pk.Position{}, false
	}
	return portalMove(pos, leftDir, edge), true
}

// calculateWidth is PortalShape.calculateWidth: the interior width scanning rightDir from bottomLeft;
// 0 unless in [MIN_WIDTH, MAX_WIDTH]. CITE: PortalShape.calculateWidth.
func (t *TickLoop) calculateWidth(bottomLeft pk.Position, rightDir portalDir) int {
	width := t.getDistanceUntilEdgeAboveFrame(bottomLeft, rightDir)
	if width < portalMinWidth || width > portalMaxWidth {
		return 0
	}
	return width
}

// getDistanceUntilEdgeAboveFrame is PortalShape.getDistanceUntilEdgeAboveFrame: walk `direction` from
// pos up to 21 steps; return the step index at which an OBSIDIAN frame wall is hit (the interior ends),
// requiring every interior cell below the walk line to be framed by obsidian. Returns 0 if the frame
// is broken (a non-empty non-frame cell, or a missing floor). CITE: PortalShape.getDistanceUntilEdgeAboveFrame.
func (t *TickLoop) getDistanceUntilEdgeAboveFrame(pos pk.Position, direction portalDir) int {
	for width := 0; width <= 21; width++ {
		cell := portalMove(pos, direction, width)
		if !t.portalEmptyAt(cell) {
			// Not empty: it must be the frame wall (obsidian) to end the interior; otherwise the frame
			// is broken and the width is 0.
			if !t.portalFrameAt(cell) {
				break
			}
			return width
		}
		// Empty interior cell: the block directly BELOW it must be obsidian (the frame floor).
		below := portalMove(cell, portalDown, 1)
		if !t.portalFrameAt(below) {
			break
		}
	}
	return 0
}

// calculateHeight is PortalShape.calculateHeight: the interior height (getDistanceUntilTop) validated
// against [MIN_HEIGHT, MAX_HEIGHT] AND hasTopFrame; returns 0 (invalid) otherwise. Returns the height
// and the counted interior portal-block count. CITE: PortalShape.calculateHeight.
func (t *TickLoop) calculateHeight(bottomLeft pk.Position, rightDir portalDir, width int) (int, int) {
	height, portalBlockCount := t.getDistanceUntilTop(bottomLeft, rightDir, width)
	if height < portalMinHeight || height > portalMaxHeight || !t.hasTopFrame(bottomLeft, rightDir, width, height) {
		return 0, portalBlockCount
	}
	return height, portalBlockCount
}

// getDistanceUntilTop is PortalShape.getDistanceUntilTop: scan up to 21 rows upward; each row's left
// wall (rightDir -1) and right wall (rightDir width) must be obsidian, and every interior cell must be
// empty (counting existing NETHER_PORTAL cells). The first row that fails ends the interior. Returns 21
// if all 21 rows are clear interior. CITE: PortalShape.getDistanceUntilTop.
func (t *TickLoop) getDistanceUntilTop(bottomLeft pk.Position, rightDir portalDir, width int) (int, int) {
	portalBlockCount := 0
	for height := 0; height < 21; height++ {
		// left wall: bottomLeft + UP*height + rightDir*(-1)
		leftWall := portalMove(portalMove(bottomLeft, portalUp, height), rightDir, -1)
		if !t.portalFrameAt(leftWall) {
			return height, portalBlockCount
		}
		// right wall: bottomLeft + UP*height + rightDir*width
		rightWall := portalMove(portalMove(bottomLeft, portalUp, height), rightDir, width)
		if !t.portalFrameAt(rightWall) {
			return height, portalBlockCount
		}
		for i := 0; i < width; i++ {
			cell := portalMove(portalMove(bottomLeft, portalUp, height), rightDir, i)
			if !t.portalEmptyAt(cell) {
				return height, portalBlockCount
			}
			// count existing NETHER_PORTAL cells (state.is(Blocks.NETHER_PORTAL) -> increment).
			if t.portalIsNetherPortalAt(cell) {
				portalBlockCount++
			}
		}
	}
	return 21, portalBlockCount
}

// hasTopFrame is PortalShape.hasTopFrame: the row directly above the interior (UP*height) must be a
// solid obsidian lintel across the whole width. CITE: PortalShape.hasTopFrame.
func (t *TickLoop) hasTopFrame(bottomLeft pk.Position, rightDir portalDir, width, height int) bool {
	for i := 0; i < width; i++ {
		cell := portalMove(portalMove(bottomLeft, portalUp, height), rightDir, i)
		if !t.portalFrameAt(cell) {
			return false
		}
	}
	return true
}

// portalIsNetherPortalAt reports whether the world cell at pos is a NETHER_PORTAL block (the
// getDistanceUntilTop count branch: state.is(Blocks.NETHER_PORTAL)).
func (t *TickLoop) portalIsNetherPortalAt(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, isPortal := block.StateList[s].(block.NetherPortal)
	return isPortal
}

// isValid is PortalShape.isValid: width in [2,21] && height in [3,21]. CITE: PortalShape.isValid.
func (sh portalShape) isValid() bool {
	return sh.width >= portalMinWidth && sh.width <= portalMaxWidth &&
		sh.height >= portalMinHeight && sh.height <= portalMaxHeight
}

// createPortalBlocks is PortalShape.createPortalBlocks: fill the interior rectangle
// [bottomLeft .. bottomLeft + UP*(height-1) + rightDir*(width-1)] with NETHER_PORTAL(AXIS=axis),
// placed with vanilla flag 18 (client-visible, no neighbor-shape reactions). Sulfur reproduces the
// observable half via SetBlock + broadcastBlockUpdate over the tick-owned manager. CITE:
// PortalShape.createPortalBlocks (Blocks.NETHER_PORTAL.defaultBlockState().setValue(AXIS, axis);
// betweenClosed(...).forEach(pos -> level.setBlock(pos, portalState, 18))).
func (t *TickLoop) createPortalBlocks(sh portalShape) {
	if t.world() == nil {
		return
	}
	// portalState = NETHER_PORTAL default with AXIS = sh.axis.
	portalState, ok := block.ToStateID[block.NetherPortal{Axis: sh.axis}]
	if !ok {
		return
	}
	// BlockPos.betweenClosed iterates the inclusive box; the interior is width×height cells.
	for h := 0; h < sh.height; h++ {
		for w := 0; w < sh.width; w++ {
			cell := portalMove(portalMove(sh.bottomLeft, portalUp, h), sh.rightDir, w)
			prev, _ := t.world().GetBlock(cell, dimMinY)
			if t.world().SetBlock(cell, portalState, dimMinY) {
				// updatePOIOnBlockStateChange: index the freshly-lit portal cell as a NETHER_PORTAL POI so a
				// return trip resolves back to THIS portal (PortalForcer.findClosestPortalPosition). This is
				// the overworld ignite path (inPortalDimension v1 overworld), so index into dimOverworld.
				t.updatePoiOnBlockStateChangeIn(dimOverworld, cell, prev, portalState)
				t.broadcastBlockUpdate(cell, portalState)
			}
		}
	}
}

// horizontalDirFromYaw is Direction.fromYRot(yaw): from2DDataValue(floor(yaw/90 + 0.5) & 3), with
// BY_2D_DATA == {SOUTH, WEST, NORTH, EAST}. This is UseOnContext.getHorizontalDirection() ==
// player.getDirection() — the forwardDirection isPortal uses to pick the preferred axis. CITE:
// net.minecraft.core.Direction.fromYRot / from2DDataValue.
func horizontalDirFromYaw(yaw float32) portalDir {
	// Mth.floor(double) == (int)Math.floor(d).
	idx := int(math.Floor(float64(yaw)/90.0+0.5)) & 3
	switch idx {
	case 0:
		return portalSouth
	case 1:
		return portalWest
	case 2:
		return portalNorth
	default: // 3
		return portalEast
	}
}

// portalPreferredAxis is BaseFireBlock.isPortal's axis pick for a HORIZONTAL forwardDirection:
// forwardDirection.getCounterClockWise().getAxis() — i.e. the horizontal axis perpendicular to the
// facing. SOUTH/NORTH (axis Z) -> X; WEST/EAST (axis X) -> Z. CITE: BaseFireBlock.isPortal.
func portalPreferredAxis(forward portalDir) block.Axis {
	switch forward {
	case portalSouth, portalNorth:
		return block.X
	default: // WEST, EAST
		return block.Z
	}
}

// tryIgnitePortalWithFlintAndSteel is the FlintAndSteelItem.useOn ignite path for the fire/portal case,
// as reached from handleUseItemOn. It ports the fire-place branch of FlintAndSteelItem.useOn +
// BaseFireBlock.canBePlacedAt/isPortal + FireBlock.onPlace's trySpawnPortal:
//
//	FlintAndSteelItem.useOn: relativePos = clickedPos.relative(clickedFace);
//	    if (BaseFireBlock.canBePlacedAt(level, relativePos, horizontalDirection)) {
//	        playSound(FLINTANDSTEEL_USE); setBlock(relativePos, BaseFireBlock.getState(...), 11);
//	        gameEvent(BLOCK_PLACE, clickedPos); hurtAndBreak(1); return SUCCESS; }
//	BaseFireBlock.canBePlacedAt: state.isAir() && (getState().canSurvive() || isPortal(...)).
//	FireBlock.onPlace (via setBlock): if (inPortalDimension && findEmptyPortalShape(pos, Axis.X)) ->
//	    createPortalBlocks(); return;  (the portal blocks REPLACE the just-placed fire).
//
// v1 SCOPE: inPortalDimension() == true for the single overworld ChunkManager (Level.OVERWORLD) — CITE
// BaseFireBlock.inPortalDimension. The plain fire-on-a-surface case (canSurvive with no portal) is a
// FOLLOW-UP (no FireBlock tick subsystem yet): v1 ignites a portal when the relative cell completes a
// frame, and no-ops otherwise. Returns true if the interaction consumed the action (a portal was made).
//
// DEFERRED (cited): item durability (Item has no durability subsystem in Sulfur — hurtAndBreak(1) is a
// no-op here, the ignite still works); the plain fire block placement + FireBlock spread tick; the
// PLACED_BLOCK advancement trigger; the purple portal particles + NetherPortalBlock.randomTick piglin
// spawn; and all dimension travel (entityInside / changeDimension / PortalForcer).
func (t *TickLoop) tryIgnitePortalWithFlintAndSteel(p *tickPlayer, clicked pk.Position, clickedFace int) bool {
	// relativePos = clickedPos.relative(clickedFace).
	dx, dy, dz := directionNormal(clickedFace)
	relativePos := pk.Position{X: clicked.X + dx, Y: clicked.Y + dy, Z: clicked.Z + dz}

	// BaseFireBlock.canBePlacedAt: the target must be air first.
	if !t.portalAirAt(relativePos) {
		return false
	}
	// isPortal(level, relativePos, forwardDirection): an adjacent obsidian must exist, then a valid empty
	// frame must resolve around relativePos. (inPortalDimension == true for the v1 overworld.)
	forward := horizontalDirFromYaw(p.yaw)
	if !t.portalIsPortalAt(relativePos, forward) {
		// Not completing a portal. BaseFireBlock.canBePlacedAt's OTHER disjunct is
		// `getState(relPos).canSurvive(relPos)` -- the PLAIN-FIRE ignite (lighting a fire on the ground).
		// FireBlock.getStateForPlacement + FireBlock.canSurvive are ported (fire_block.go), so place the
		// plain fire when the target can hold it, exactly as FlintAndSteelItem.useOn's fire branch:
		//   playSound(FLINTANDSTEEL_USE, ...); setBlock(relPos, BaseFireBlock.getState(relPos), 11).
		// CITE: FlintAndSteelItem.useOn (offsets 135-265); BaseFireBlock.canBePlacedAt / getState;
		// FireBlock.getStateForPlacement / canSurvive.
		return t.igniteFireAt(relativePos)
	}

	// playSound(player, relativePos, FLINTANDSTEEL_USE, BLOCKS, 1.0, random.nextFloat()*0.4 + 0.8).
	// FLINTANDSTEEL_USE registry id 642 ("item.flintandsteel.use"). The pitch/seed are dedicated
	// non-gameplay draws (the sound-seed discipline the combat/mob path already uses), so they never
	// perturb a gameplay RNG stream (the pig oracle stays byte-identical). CITE: FlintAndSteelItem.useOn
	// playSound; SoundSource.BLOCKS == ordinal 4.
	pitch := mrand.Float32()*0.4 + 0.8
	t.playSound(flintAndSteelUseSoundID, soundSourceBlocks,
		float64(relativePos.X)+0.5, float64(relativePos.Y)+0.5, float64(relativePos.Z)+0.5,
		1.0, pitch, mrand.Int64())

	// FireBlock.onPlace (reached via setBlock of the fire): findEmptyPortalShape(level, pos, Axis.X) ->
	// createPortalBlocks(). The fire is never left behind — the portal blocks are the interior fill.
	if sh, ok := t.findEmptyPortalShape(relativePos, block.X); ok {
		t.createPortalBlocks(sh)
	}
	return true
}

// igniteFireAt is the PLAIN-FIRE branch of FlintAndSteelItem.useOn (offsets 135-265): it mirrors
// BaseFireBlock.canBePlacedAt's `getState(pos).canSurvive(pos)` disjunct (the portal disjunct has
// already been tried by the caller). getState(pos) == BaseFireBlock.getState -> FIRE overworld
// (SoulFireBlock cells are absent overworld, per fire_block.go's fireStateWithAge note), so the
// survive check is FireBlock.canSurvive == fireCanSurvive. On success it plays FLINTANDSTEEL_USE and
// writes the fire via getStateForPlacement with setBlock flag 11 (== UPDATE_NEIGHBORS|UPDATE_CLIENTS
// |UPDATE_KNOWN_SHAPE), mirrored as SetBlock + broadcast. Returns true iff a fire was lit (the caller
// wears the flint&steel by 1 only then). CITE: FlintAndSteelItem.useOn (fire branch); BaseFireBlock
// .canBePlacedAt / getState; FireBlock.getStateForPlacement / canSurvive.
func (t *TickLoop) igniteFireAt(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	// canBePlacedAt already required air at pos (portalAirAt above). The remaining disjunct is
	// getState(pos).canSurvive(pos): the fire's canSurvive at this cell.
	if !t.fireCanSurvive(pos) {
		return false
	}
	fireState, ok := t.fireStateForPlacement(pos)
	if !ok {
		return false
	}
	// playSound(player, pos, FLINTANDSTEEL_USE, BLOCKS, 1.0, random.nextFloat()*0.4 + 0.8). The
	// pitch/seed are dedicated non-gameplay draws (the sound-seed discipline used across the block
	// layer), so they never perturb a gameplay RNG stream. CITE: FlintAndSteelItem.useOn playSound.
	pitch := mrand.Float32()*0.4 + 0.8
	t.playSound(flintAndSteelUseSoundID, soundSourceBlocks,
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		1.0, pitch, mrand.Int64())
	// setBlock(pos, getState(pos), 11): write + broadcast; kick the neighbor reconcile (the flag-11
	// UPDATE_NEIGHBORS half) so the freshly-lit fire schedules its own first FireBlock tick.
	if !t.world().SetBlock(pos, fireState, dimMinY) {
		return false
	}
	t.broadcastBlockUpdate(pos, fireState)
	// FireBlock.onPlace -> scheduleTick(pos, this, getFireTickDelay(getRandom())): the freshly-lit fire
	// must schedule its first FireBlock tick so it spreads / burns out. Use the world-global region
	// (t.only()) for the delay's level-random draw, matching the scheduled-block drain's region. CITE:
	// FireBlock.onPlace.
	t.scheduleBlockTick(pos, fireTickType, t.getFireTickDelay(t.only()))
	// The flag-11 UPDATE_NEIGHBORS half: kick the edit-time neighbor reconcile (redstone/support seams).
	t.onBlockTickEdit(pos)
	return true
}

// portalAirAt reports whether the world cell at pos is air (BaseFireBlock.canBePlacedAt's
// state.isAir() gate). A nil/unreadable world is treated as not-air.
func (t *TickLoop) portalAirAt(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	return ok && block.IsAir(s)
}

// portalIsPortalAt is BaseFireBlock.isPortal: (inPortalDimension always true for v1 overworld), then an
// adjacent obsidian must exist on at least one of the 6 faces, then findEmptyPortalShape must resolve a
// valid empty frame for the preferred axis (forwardDirection.getCounterClockWise().getAxis()). CITE:
// BaseFireBlock.isPortal.
func (t *TickLoop) portalIsPortalAt(pos pk.Position, forward portalDir) bool {
	// hasObsidian: any of the 6 neighbors is obsidian.
	hasObsidian := false
	for _, face := range []portalDir{portalDown, portalUp, portalNorth, portalSouth, portalWest, portalEast} {
		if t.portalFrameAt(portalMove(pos, face, 1)) {
			hasObsidian = true
			break
		}
	}
	if !hasObsidian {
		return false
	}
	preferred := portalPreferredAxis(forward)
	_, ok := t.findEmptyPortalShape(pos, preferred)
	return ok
}
