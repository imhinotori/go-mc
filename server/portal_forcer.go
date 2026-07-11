package server

// portal_forcer.go -- net.minecraft.world.level.portal.PortalForcer, ported 1:1 from the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). It is the NETHER-TRAVEL destination
// resolver: given the coordinate-scaled exit column in the target dimension, it (1) searches the target
// world POI index for the closest existing nether portal (findClosestPortalPosition), and, failing that,
// (2) scans for a place to build a fresh 4x5 obsidian frame + interior nether_portal fill, with a forced
// obsidian platform fallback when nothing fits (createPortal). This replaces the old fixed-Y landing seam
// in dimension_travel.go (changeDimension) so a traveler lands at a real destination portal.
//
// CITED JAR (javap -c -p, 26.2-inner.jar this session):
//   - net.minecraft.world.level.portal.PortalForcer.findClosestPortalPosition(BlockPos, boolean,
//     WorldBorder): radius = isNether ? NETHER_PORTAL_RADIUS(16) : OVERWORLD_PORTAL_RADIUS(128);
//     poiManager.ensureLoadedAndValid(level, pos, radius) [POI is runtime-built in Sulfur -> no-op];
//     getInSquare(is(PoiTypes.NETHER_PORTAL), pos, radius, Occupancy.ANY).map(PoiRecord::getPos)
//     .filter(worldBorder::isWithinBounds).filter(p -> getBlockState(p).hasProperty(HORIZONTAL_AXIS))
//     .min(comparingDouble(p -> p.distSqr(exitPos)).thenComparingInt(BlockPos::getY)).
//   - PortalForcer.createPortal(BlockPos, Direction.Axis): the spiralAround(exitPos, 16, EAST, SOUTH)
//     column scan (canPortalReplaceBlock floor-find + FRAME_BOX gap check + canHostFrame), the
//     double-frame-preferred / single-frame-fallback distSqr pick, the forced-platform fallback when
//     nothing fits, then the 4x5 obsidian frame + 2x3 interior NETHER_PORTAL(AXIS=axis) fill.
//   - PortalForcer.canPortalReplaceBlock(pos): state.canBeReplaced() && state.getFluidState().isEmpty().
//   - PortalForcer.canHostFrame(exitPos, mutable, direction, offset): the frame test (row -1 must be a
//     solid floor, rows 0..3 must be portal-replaceable) across the FRAME_BOX(3) width.
//   - PortalForcer field constants: NETHER_PORTAL_RADIUS=16, OVERWORLD_PORTAL_RADIUS=128, FRAME_HEIGHT=5,
//     FRAME_WIDTH=4, FRAME_BOX=3, FRAME_*_START/END, NOTHING_FOUND=-1.
//   - net.minecraft.world.level.block.NetherPortalBlock.getPortalDestination/getExitPortal: the caller
//     chain -- clampToBounds(scaled pos) -> findClosestPortalPosition, else createPortal.
//   - net.minecraft.core.BlockPos.spiralAround / BlockPos$6: the square-spiral visit order (matched 1:1
//     so distSqr ties break in the same order vanilla visits them).

import (
	"math"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// PortalForcer field constants (VERIFIED javap -constants PortalForcer).
const (
	portalForcerNetherRadius    = 16  // NETHER_PORTAL_RADIUS
	portalForcerOverworldRadius = 128 // OVERWORLD_PORTAL_RADIUS
	portalForcerFrameHeight     = 5   // FRAME_HEIGHT
	portalForcerFrameWidth      = 4   // FRAME_WIDTH
	portalForcerFrameBox        = 3   // FRAME_BOX
	portalForcerNothingFound    = -1  // NOTHING_FOUND
)

// portalDimGeom bundles the per-dimension world + geometry the PortalForcer reads: the ChunkManager
// (block get/set), the dimension min-Y, the getMaxY()/getLogicalHeight() derived worldTop, and the
// dimension id (for POI + broadcast routing). It stands in for the ServerLevel the PortalForcer holds.
type portalDimGeom struct {
	mgr        *world.ChunkManager
	dim        int
	minY       int
	maxY       int // ServerLevel.getMaxY() == min_y + height - 1
	logicalTop int // getMinY() + getLogicalHeight() - 1
}

// dimMaxYFor is ServerLevel.getMaxY() == dimension_type min_y + height - 1. Overworld: -64+384-1=319.
// Nether: 0+256-1=255. End: 0+256-1=255. CITE: dimension_type/{overworld,the_nether,the_end}.json.
func dimMaxYFor(dim int) int {
	switch dim {
	case dimNether:
		return dimNetherMinY + 256 - 1 // the_nether.json height 256
	case dimEnd:
		return dimEndMinY + 256 - 1 // the_end.json height 256
	default:
		return dimMinY + 384 - 1 // overworld.json height 384
	}
}

// dimLogicalTopFor is getMinY() + getLogicalHeight() - 1. Overworld: -64+384-1=319. Nether:
// 0+128-1=127 (logical_height 128 -> the netherrack ceiling clamp). End: 0+256-1=255. CITE:
// dimension_type logical_height.
func dimLogicalTopFor(dim int) int {
	switch dim {
	case dimNether:
		return dimNetherMinY + 128 - 1 // the_nether.json logical_height 128
	case dimEnd:
		return dimEndMinY + 256 - 1 // the_end.json logical_height 256
	default:
		return dimMinY + 384 - 1 // overworld.json logical_height 384
	}
}

// portalDimGeomFor resolves the target dimension world + geometry (nil mgr -> not wired).
func (t *TickLoop) portalDimGeomFor(dim int) portalDimGeom {
	return portalDimGeom{
		mgr:        t.dimWorldByID(dim),
		dim:        dim,
		minY:       dimMinYFor(dim),
		maxY:       dimMaxYFor(dim),
		logicalTop: dimLogicalTopFor(dim),
	}
}

// findClosestPortalPosition ports PortalForcer.findClosestPortalPosition(exitPos, isNether, border): the
// POI-index search for the closest existing NETHER_PORTAL. radius = isNether ? 16 : 128. It queries the
// target dimension POI manager (getInSquare with Occupancy.ANY), keeps only positions inside the world
// border whose block still carries HORIZONTAL_AXIS (still a portal block), and picks the minimum by
// distSqr(exitPos) then by Y (lowest). Returns the chosen pos + true, or false if none.
//
// ensureLoadedAndValid (which force-loads the POI sections around pos and validates them) is a no-op here:
// Sulfur POI index is rebuilt from live block edits (poi.go), so the loaded records ARE the valid set.
func (t *TickLoop) findClosestPortalPosition(g portalDimGeom, exitPos pk.Position, isNether bool) (pk.Position, bool) {
	pm := t.dimPoiManager(g.dim)
	if pm == nil {
		return pk.Position{}, false
	}
	radius := portalForcerOverworldRadius
	if isNether {
		radius = portalForcerNetherRadius
	}

	// getInSquare(is(NETHER_PORTAL), exitPos, radius, ANY) -> map to pos -> filter within border -> filter
	// still a portal block (hasProperty HORIZONTAL_AXIS) -> min(comparingDouble(distSqr) thenComparingInt(Y)).
	best := false
	var bestPos pk.Position
	var bestDist float64
	for _, rec := range pm.getInSquare(func(pt *poiType) bool { return pt == poiTypeNetherPortal }, exitPos, radius, poiOccupancyAny) {
		p := rec.pos
		// worldBorder::isWithinBounds(BlockPos): the block X/Z is inside the border.
		if !t.worldBorder.isWithinBoundsXZ(float64(p.X), float64(p.Z)) {
			continue
		}
		// lambda$findClosestPortalPosition$1: getBlockState(p).hasProperty(HORIZONTAL_AXIS) -- the cell is
		// still a nether_portal (a broken/removed portal stale POI record is filtered out).
		if !t.portalHasHorizontalAxis(g, p) {
			continue
		}
		d := poiDistSqr(p, exitPos)
		// min by distSqr, then by getY (Comparator.thenComparingInt(BlockPos::getY)).
		if !best || d < bestDist || (d == bestDist && p.Y < bestPos.Y) {
			best = true
			bestPos = p
			bestDist = d
		}
	}
	return bestPos, best
}

// portalHasHorizontalAxis reports whether the block at pos in dimension g carries HORIZONTAL_AXIS -- i.e.
// it is a nether_portal (the only Sulfur block with the horizontal-axis-only property a POI records).
// CITE: PortalForcer.lambda$findClosestPortalPosition$1 (getBlockState(pos).hasProperty(HORIZONTAL_AXIS)).
func (t *TickLoop) portalHasHorizontalAxis(g portalDimGeom, pos pk.Position) bool {
	if g.mgr == nil {
		return false
	}
	s, ok := g.mgr.GetBlock(pos, g.minY)
	if !ok || int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, isPortal := block.StateList[s].(block.NetherPortal)
	return isPortal
}

// canPortalReplaceBlock ports PortalForcer.canPortalReplaceBlock(pos): state.canBeReplaced() &&
// state.getFluidState().isEmpty(). In the v1 block set the canBeReplaced members are AIR (all variants),
// WATER and LAVA; only AIR has an empty fluid state, so this resolves to "air". An unloaded cell reads as
// not-air -> false (the scan cannot place into ungenerated space). CITE: PortalForcer.canPortalReplaceBlock.
func (t *TickLoop) canPortalReplaceBlock(g portalDimGeom, pos pk.Position) bool {
	if g.mgr == nil {
		return false
	}
	s, ok := g.mgr.GetBlock(pos, g.minY)
	if !ok {
		return false
	}
	// canBeReplaced() && getFluidState().isEmpty(): air is the only member of both. Water/Lava are
	// canBeReplaced but carry a fluid state, so they are excluded here.
	return block.IsAir(s)
}

// portalIsSolidAt ports the BlockState.isSolid() floor test canHostFrame uses on its row -1 cells: a solid
// (motion-blocking) block must underlie every interior floor cell. Sulfur solidity read is dimension-aware
// here (isSolidAt is overworld-fixed), reusing the same non-air/non-fluid subset. CITE:
// PortalForcer.canHostFrame (getBlockState(pos).isSolid()).
func (t *TickLoop) portalIsSolidAt(g portalDimGeom, pos pk.Position) bool {
	if g.mgr == nil {
		return false
	}
	s, ok := g.mgr.GetBlock(pos, g.minY)
	if !ok || block.IsAir(s) {
		return false
	}
	if _, isWater := waterLevelOf(s); isWater {
		return false
	}
	if _, isLava := lavaLevelOf(s); isLava {
		return false
	}
	return true
}

// canHostFrame ports PortalForcer.canHostFrame(exitPos, cursor, direction, offset): scan the box (n6 in
// [-1,3) along direction, n7 in [-1,4) up) offset sideways by offset along direction clockwise. For each
// cell cursor = exitPos + direction*n6 + clockwise*offset (+Y n7): the row n7==-1 (the floor) must be
// solid; every row n7>=0 must be portal-replaceable. Returns false at the first failure. CITE:
// PortalForcer.canHostFrame.
func (t *TickLoop) canHostFrame(g portalDimGeom, exitPos pk.Position, direction, clockwise portalDir, offset int) bool {
	for n6 := -1; n6 < portalForcerFrameBox; n6++ { // FRAME_BOX_START=-1 .. FRAME_BOX_END=2
		for n7 := -1; n7 < portalForcerFrameWidth; n7++ { // FRAME_WIDTH_START=-1 .. FRAME_WIDTH_END=3
			cell := pk.Position{
				X: exitPos.X + direction.dx*n6 + clockwise.dx*offset,
				Y: exitPos.Y + n7,
				Z: exitPos.Z + direction.dz*n6 + clockwise.dz*offset,
			}
			if n7 < 0 { // the floor row: must be solid
				if !t.portalIsSolidAt(g, cell) {
					return false
				}
			}
			if n7 >= 0 { // interior rows: must be portal-replaceable
				if !t.canPortalReplaceBlock(g, cell) {
					return false
				}
			}
		}
	}
	return true
}

// portalGetHeightMB ports ServerLevel.getHeight(Heightmap.MOTION_BLOCKING, x, z): the world-Y of the first
// block above the highest motion-blocking block in column (x,z), read from the loaded chunk MB heightmap.
// An unloaded column reads as the dimension floor (minY), matching a void column. CITE: createPortal
// (Math.min(worldTop, getHeight(MOTION_BLOCKING, x, z))).
func (t *TickLoop) portalGetHeightMB(g portalDimGeom, x, z int) int {
	if g.mgr == nil {
		return g.minY
	}
	col := level.ChunkPos{int32(x >> 4), int32(z >> 4)}
	ch, ok := g.mgr.Get(col)
	if !ok || ch == nil || ch.HeightMaps.MotionBlocking == nil {
		return g.minY
	}
	idx := (z&15)<<4 | (x & 15)
	return ch.HeightMaps.MotionBlocking.Get(idx) + g.minY
}

// portalSpiralAround ports BlockPos.spiralAround(center, radius, EAST, SOUTH) -> BlockPos$6, the square-
// spiral visitor. It yields positions in the exact order vanilla iterator does (so distSqr ties break
// identically). directions = [first, second, first.opposite, second.opposite]; cursor starts at
// center+second; each step moves by directions[(leg+4)%4] and, when legIndex reaches legSize (leg/2+1),
// advances leg (stopping after legs = 4*radius). CITE: BlockPos$6.computeNext.
func portalSpiralAround(center pk.Position, radius int, first, second portalDir) []pk.Position {
	directions := [4]portalDir{first, second, first.opposite(), second.opposite()}
	cursor := portalMove(center, second, 1) // center.mutable().move(second)
	legs := 4 * radius
	leg := -1
	legSize := 0
	legIndex := 0
	lastX, lastY, lastZ := cursor.X, cursor.Y, cursor.Z
	var out []pk.Position
	for {
		// cursor.set(lastX,lastY,lastZ).move(directions[(leg+4)%4]); update last from cursor.
		d := directions[((leg%4)+4)%4]
		cursor = pk.Position{X: lastX + d.dx, Y: lastY + d.dy, Z: lastZ + d.dz}
		lastX, lastY, lastZ = cursor.X, cursor.Y, cursor.Z
		if legIndex >= legSize {
			if leg >= legs {
				break // endOfData
			}
			leg++
			legIndex = 0
			legSize = leg/2 + 1
		}
		legIndex++
		out = append(out, cursor)
	}
	return out
}

// createPortal ports PortalForcer.createPortal(exitPos, axis): scan the spiral around exitPos for a column
// that can host a 4x5 obsidian frame (a solid floor with a FRAME_BOX-tall replaceable gap above), pick the
// closest by distSqr(exitPos) -- preferring a spot with clearance on BOTH sides (canHostFrame offset -1 and
// +1) over a one-sided spot -- and build the frame there. When nothing fits it force-builds a platform at
// the clamped exit column (the OBSIDIAN cross + AIR clearance). Returns the built portal bottomLeft corner
// + axis + true (the FoundRectangle analogue), or false when out of the world border. Runs on the tick
// goroutine over the tick-owned target world (block writes go through g.mgr.SetBlock + broadcast).
func (t *TickLoop) createPortal(g portalDimGeom, exitPos pk.Position, axis block.Axis) (pk.Position, block.Axis, bool) {
	if g.mgr == nil {
		return pk.Position{}, axis, false
	}
	// direction = Direction.get(POSITIVE, axis): X -> EAST, Z -> SOUTH.
	direction := portalEast
	if axis == block.Z {
		direction = portalSouth
	}
	clockwise := direction.clockWise()

	// double-frame best (d4/blockpos6) and single-frame best (d7/blockpos9); -1.0 == none yet.
	bestDouble := -1.0
	var bestDoublePos pk.Position
	bestSingle := -1.0
	var bestSinglePos pk.Position
	haveDouble := false
	haveSingle := false

	// worldTop = min(getMaxY(), getMinY() + getLogicalHeight() - 1).
	worldTop := g.maxY
	if g.logicalTop < worldTop {
		worldTop = g.logicalTop
	}

	// spiralAround(exitPos, 16, EAST, SOUTH).
	for _, p := range portalSpiralAround(exitPos, portalForcerNetherRadius, portalEast, portalSouth) {
		// n16 = min(worldTop, getHeight(MOTION_BLOCKING, p.x, p.z)).
		minColHeight := t.portalGetHeightMB(g, p.X, p.Z)
		if worldTop < minColHeight {
			minColHeight = worldTop
		}
		// if (!border.isWithinBounds(p)) continue.
		if !t.worldBorder.isWithinBoundsXZ(float64(p.X), float64(p.Z)) {
			continue
		}
		// if (!border.isWithinBounds(p.move(direction,1))) continue; else move back.
		fwd := portalMove(p, direction, 1)
		if !t.worldBorder.isWithinBoundsXZ(float64(fwd.X), float64(fwd.Z)) {
			continue
		}

		// for (n17 = n16; n17 >= getMinY(); --n17):
		for y := minColHeight; y >= g.minY; y-- {
			cur := pk.Position{X: p.X, Y: y, Z: p.Z}
			// if (!canPortalReplaceBlock(p.setY(n17))) continue.
			if !t.canPortalReplaceBlock(g, cur) {
				continue
			}
			// n18 = n17; while (n17 > getMinY() && canPortalReplaceBlock(p.move(DOWN))) --n17.
			n18 := y
			for y > g.minY && t.canPortalReplaceBlock(g, pk.Position{X: p.X, Y: y - 1, Z: p.Z}) {
				y--
			}
			cur = pk.Position{X: p.X, Y: y, Z: p.Z}
			// if (n17 + FRAME_WIDTH(4) > worldTop) continue -- not enough vertical room. (bytecode: iconst_4
			// -> n17 + 4 > worldTop -> skip)
			if y+portalForcerFrameWidth > worldTop {
				continue
			}
			// n19 = n18 - n17 (the replaceable gap depth found while descending); if (n19 > 0 && n19 <
			// FRAME_BOX) continue -- a too-shallow overhang (0 < gap < 3) is rejected.
			n19 := n18 - y
			if n19 > 0 && n19 < portalForcerFrameBox {
				continue
			}
			// p.setY(n17); if (!canHostFrame(exitPos, p, direction, 0)) continue.
			if !t.canHostFrame(g, cur, direction, clockwise, 0) {
				continue
			}
			// d21 = exitPos.distSqr(p).
			d21 := poiDistSqr(exitPos, cur)
			// if (canHostFrame(...,-1) && canHostFrame(...,1)) -> the double-frame branch.
			if t.canHostFrame(g, cur, direction, clockwise, -1) && t.canHostFrame(g, cur, direction, clockwise, 1) {
				if !haveDouble || d21 < bestDouble {
					haveDouble = true
					bestDouble = d21
					bestDoublePos = cur
				}
			}
			// if (d4 == -1) -> the single-frame branch (only tracked while no double found yet).
			if !haveDouble {
				if !haveSingle || d21 < bestSingle {
					haveSingle = true
					bestSingle = d21
					bestSinglePos = cur
				}
			}
		}
	}

	// if (d4 == -1 && d7 != -1) { blockpos6 = blockpos9; d4 = d7; } -- fall back to the single-frame spot.
	if !haveDouble && haveSingle {
		haveDouble = true
		bestDoublePos = bestSinglePos
		bestDouble = bestSingle
	}
	_ = bestDouble

	// if (d4 == -1) -> nothing found: force a platform at the clamped exit column.
	if !haveDouble {
		return t.createForcedPortal(g, exitPos, axis, direction, clockwise, worldTop)
	}

	// A frame spot was found: build the 4x5 obsidian frame + interior fill at blockpos6, no forced fill.
	return t.buildPortalFrame(g, bestDoublePos, axis, direction, clockwise), axis, true
}

// createForcedPortal ports PortalForcer.createPortal NOTHING_FOUND fallback (bytecode 448-663): clamp the
// exit column to a standable Y band and force a 3-wide OBSIDIAN cross + AIR clearance before laying the
// frame. n14 = max(getMinY() - 1, 70); n15 = worldTop - 9; if (n15 < n14) return empty (out of border /
// no room). blockpos6 = (exitPos.x - direction.stepX, clamp(exitPos.y, n14, n15), exitPos.z -
// direction.stepZ) clamped to the border. Then the OBSIDIAN base (n17 in [-1,2), n18 in [0,2), n19 in
// [-1,3): OBSIDIAN when n19<0 else AIR, at blockpos6 + direction*n18 + clockwise*n17 + Y n19). Then the
// standard frame + fill. CITE: PortalForcer.createPortal (offsets 439-861).
func (t *TickLoop) createForcedPortal(g portalDimGeom, exitPos pk.Position, axis block.Axis, direction, clockwise portalDir, worldTop int) (pk.Position, block.Axis, bool) {
	// n14 = Math.max(getMinY() - 1, 70).
	n14 := g.minY - 1
	if 70 > n14 {
		n14 = 70
	}
	// n15 = worldTop - 9.
	n15 := worldTop - 9
	if n15 < n14 {
		return pk.Position{}, axis, false // Optional.empty(): no vertical room in the border
	}
	// blockpos6 = new BlockPos(exitPos.x - direction.stepX*1, clamp(exitPos.y, n14, n15), exitPos.z -
	// direction.stepZ*1).immutable(); then clampToBounds.
	base := pk.Position{
		X: exitPos.X - direction.dx,
		Y: mthClampInt(exitPos.Y, n14, n15),
		Z: exitPos.Z - direction.dz,
	}
	base = t.portalClampToBorder(base)

	// OBSIDIAN base cross + AIR clearance: for (n17=-1; n17<2; ++n17) for (n18=0; n18<2; ++n18)
	// for (n19=-1; n19<3; ++n19): state = n19<0 ? OBSIDIAN : AIR; setBlockAndUpdate(base + direction*n18 +
	// clockwise*n17 (+Y n19)).
	obsidian, okOb := block.ToStateID[block.Obsidian{}]
	airState, okAir := block.ToStateID[block.Air{}]
	if !okOb || !okAir {
		return pk.Position{}, axis, false
	}
	for n17 := -1; n17 < 2; n17++ {
		for n18 := 0; n18 < 2; n18++ {
			for n19 := -1; n19 < 3; n19++ {
				st := airState
				if n19 < 0 {
					st = obsidian
				}
				cell := pk.Position{
					X: base.X + direction.dx*n18 + clockwise.dx*n17,
					Y: base.Y + n19,
					Z: base.Z + direction.dz*n18 + clockwise.dz*n17,
				}
				t.portalSetBlock(g, cell, st)
			}
		}
	}

	return t.buildPortalFrame(g, base, axis, direction, clockwise), axis, true
}

// buildPortalFrame ports PortalForcer.createPortal frame + fill tail (offsets 663-861): a 4-wide x 5-tall
// OBSIDIAN frame ring plus the 2-wide x 3-tall interior NETHER_PORTAL(AXIS=axis) fill, laid from bottomLeft
// along direction (width) and Y (height). Returns bottomLeft (the FoundRectangle minCorner).
//
//	frame: for (n14=-1; n14<3; ++n14) for (n15=-1; n15<4; ++n15) if (n14==-1||n14==2||n15==-1||n15==3)
//	    setBlock(bottomLeft + direction*n14 (+Y n15), OBSIDIAN, 3).
//	fill:  for (n15=0; n15<2; ++n15) for (n16=0; n16<3; ++n16)
//	    setBlock(bottomLeft + direction*n15 (+Y n16), NETHER_PORTAL(AXIS=axis), 18).
func (t *TickLoop) buildPortalFrame(g portalDimGeom, bottomLeft pk.Position, axis block.Axis, direction, clockwise portalDir) pk.Position {
	obsidian, okOb := block.ToStateID[block.Obsidian{}]
	portalState, okP := block.ToStateID[block.NetherPortal{Axis: axis}]
	if !okOb || !okP {
		return bottomLeft
	}
	// OBSIDIAN frame ring (n14 width in [-1,3), n15 height in [-1,4); ring == n14 or n15 at an edge).
	for n14 := -1; n14 < portalForcerFrameBox; n14++ { // FRAME_BOX_START..END
		for n15 := -1; n15 < portalForcerFrameWidth; n15++ { // FRAME_WIDTH_START..END
			if n14 == -1 || n14 == 2 || n15 == -1 || n15 == 3 {
				cell := pk.Position{
					X: bottomLeft.X + direction.dx*n14,
					Y: bottomLeft.Y + n15,
					Z: bottomLeft.Z + direction.dz*n14,
				}
				t.portalSetBlock(g, cell, obsidian)
			}
		}
	}
	// Interior NETHER_PORTAL fill (2 wide x 3 tall).
	for n15 := 0; n15 < 2; n15++ {
		for n16 := 0; n16 < 3; n16++ {
			cell := pk.Position{
				X: bottomLeft.X + direction.dx*n15,
				Y: bottomLeft.Y + n16,
				Z: bottomLeft.Z + direction.dz*n15,
			}
			t.portalSetBlock(g, cell, portalState)
		}
	}
	return bottomLeft
}

// portalSetBlock writes a block into the target dimension world (setBlock/setBlockAndUpdate), keeps the POI
// index live (updatePOIOnBlockStateChange -> a laid nether_portal is indexed so a return trip reuses it),
// and broadcasts the update to any player tracking the column. It is the dimension-aware analogue of the
// createPortalBlocks SetBlock+broadcast seam (nether_portal.go). Tick-owned.
func (t *TickLoop) portalSetBlock(g portalDimGeom, pos pk.Position, state block.StateID) {
	if g.mgr == nil {
		return
	}
	old, _ := g.mgr.GetBlock(pos, g.minY)
	if !g.mgr.SetBlock(pos, state, g.minY) {
		return
	}
	t.updatePoiOnBlockStateChangeIn(g.dim, pos, old, state)
	t.broadcastBlockUpdate(pos, state)
}

// portalClampToBorder ports WorldBorder.clampToBounds(BlockPos): clamp X/Z into the border interior
// (Y is untouched). Uses the tick worldBorder (the shared border applied to every dimension in v1).
// CITE: WorldBorder.clampToBounds(double,double,double).
func (t *TickLoop) portalClampToBorder(pos pk.Position) pk.Position {
	x, z := t.worldBorder.clampVec3ToBoundXZ(float64(pos.X), float64(pos.Z))
	return pk.Position{X: int(math.Floor(x)), Y: pos.Y, Z: int(math.Floor(z))}
}

// resolveNetherPortalDestination is the NetherPortalBlock.getPortalDestination -> getExitPortal driver for
// Sulfur nether travel: given the coordinate-scaled destination column (scaledX/scaledZ already carry the
// 8:1 DimensionType.getTeleportationScale), it clamps to the world border, searches the target dimension
// POI index for the closest existing portal (findClosestPortalPosition, isNether = targetDim==dimNether),
// and, failing that, builds a fresh portal (createPortal, axis X -- the default BaseFireBlock.onPlace axis
// and the getExitPortal `getOptionalValue(AXIS).orElse(X)` fallback). It returns the player standing
// position: the portal column center (x+0.5, z+0.5) at the interior BASE Y (the bottom of the portal, the
// standable cell). Returns ok=false when the target world is not wired or createPortal reports out-of-
// border. CITE: NetherPortalBlock.getPortalDestination (clampToBounds of scaled pos) + getExitPortal
// (findClosestPortalPosition, else createPortal) + PortalShape.createPortalBlocks (interior base).
//
// REDUCED (cited): the exact sub-block landing offset (getDimensionTransitionFromExit ->
// getLargestRectangleAround + Entity.getRelativePortalPosition, a fractional Vec3 preserving the entity
// side/offset within the portal rectangle) is not reproduced; the player is centered on the portal's base
// cell instead. The load-bearing 1:1 behavior -- WHICH portal (found-or-built) and WHERE it is -- is exact;
// only the fractional standing offset within that portal is centered rather than side-preserved.
func (t *TickLoop) resolveNetherPortalDestination(targetDim int, scaledX, scaledZ float64) (float64, float64, float64, bool) {
	g := t.portalDimGeomFor(targetDim)
	if g.mgr == nil {
		return 0, 0, 0, false
	}
	isNether := targetDim == dimNether

	// clampToBounds(scaledX, y, scaledZ): clamp the scaled entry column into the border, then floor to a
	// BlockPos (BlockPos.containing). The Y is the scaled entry Y; findClosest ignores it beyond the search
	// square, and createPortal uses it as the vertical search anchor.
	cx, cz := t.worldBorder.clampVec3ToBoundXZ(scaledX, scaledZ)
	exitPos := pk.Position{X: int(math.Floor(cx)), Y: g.minY, Z: int(math.Floor(cz))}

	// findClosestPortalPosition first (reuse an existing destination portal).
	if pos, ok := t.findClosestPortalPosition(g, exitPos, isNether); ok {
		return float64(pos.X) + 0.5, float64(pos.Y), float64(pos.Z) + 0.5, true
	}

	// Else createPortal(exitPos, Axis.X): build a fresh 4x5 frame (getExitPortal default axis X).
	bottomLeft, _, ok := t.createPortal(g, exitPos, block.X)
	if !ok {
		return 0, 0, 0, false // "Unable to create a portal, likely target out of worldborder" -> null
	}
	// The player stands on the portal interior base cell (bottomLeft), centered in X/Z.
	return float64(bottomLeft.X) + 0.5, float64(bottomLeft.Y), float64(bottomLeft.Z) + 0.5, true
}

// indexChunkPortals scans a freshly loaded/inserted chunk for NETHER_PORTAL blocks and registers each in
// the dimension POI manager (dimPoiManager), so findClosestPortalPosition can locate portals that were
// built in a prior session (persistence) or generated, not only those created live this session. It is
// the standing-in for vanilla PoiManager.checkConsistencyWithBlocks -- PoiSection is (re)built from the
// chunk block states on chunk-load. Sections with no non-air blocks are skipped (a fresh terrain chunk
// has no portals -> near-zero cost). Runs on the coordinator (chunkReady.applyTo, tick-owned). CITE:
// PoiManager.checkConsistencyWithBlocks / SectionStorage load (POI rebuilt from block states).
func (t *TickLoop) indexChunkPortals(dim int, pos level.ChunkPos, ch *level.Chunk) {
	if ch == nil {
		return
	}
	minY := dimMinYFor(dim)
	baseX := int(pos[0]) << 4
	baseZ := int(pos[1]) << 4
	for sec := range ch.Sections {
		s := &ch.Sections[sec]
		if s.BlockCount == 0 {
			continue // all-air section: no portal blocks possible
		}
		secBaseY := minY + sec*16
		for local := 0; local < 4096; local++ {
			state := block.StateID(s.GetBlock(local))
			if !isNetherPortalBlock(state) {
				continue
			}
			lx := local & 15
			lz := (local >> 4) & 15
			ly := (local >> 8) & 15
			wpos := pk.Position{X: baseX + lx, Y: secBaseY + ly, Z: baseZ + lz}
			t.dimPoiManager(dim).add(wpos, poiTypeNetherPortal)
		}
	}
}
