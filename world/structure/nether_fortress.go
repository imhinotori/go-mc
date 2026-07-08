package structure

// nether_fortress.go ports NetherFortressStructure + NetherFortressPieces (javap -c against
// temp/cache/26.2-inner.jar): the HARDCODED-piece (no NBT template) recursive bridge assembly,
// wired onto the STRUCT-01 StartGenerator seam and driven from the NETHER generator. This is the
// fortress that gates blaze-rod / brewing progression (MonsterThrone places the BLAZE spawner).
//
// PLACEMENT (nether_complexes structure_set): random_spread, spacing 27 / separation 4 / salt
// 30084232, LINEAR, frequency 1.0; weights {fortress:2, bastion_remnant:3}. We port the FORTRESS
// branch (weight 2); the bastion is DEFERRED. Start origin = findGenerationPoint:
// (chunk.minBlockX, 64, chunk.minBlockZ) -- Y=64 (MAGIC_START_Y), NOT terrain.
//
// PIECE ASSEMBLY (NetherFortressStructure.generatePieces, jar-exact): StartPiece(random,
// chunk.getBlockX(2), chunk.getBlockZ(2)) -- a BridgeCrossing subclass. addPiece(start);
// start.addChildren; drain start.pendingChildren FIFO-by-random-remove (while !empty, remove
// nextInt(size), child.addChildren). Vanilla then moveInsideHeights(48,70) -- deferred (byte
// note below).
//
// THE WEIGHTED RECURSIVE WALK (NetherBridgePiece.generatePiece): each piece proposes children via
// generateChild{Forward,Left,Right} -> generateAndAddPiece: BridgeEndFiller-caps at
// |x-start.minX|>112 || |z-start.minZ|>112; else generatePiece(genDepth+1) over availableBridge.
// generatePiece: updatePieceWeight (sum of weights, -1 if all capped); if >0 && genDepth<=30, up
// to 5 tries of nextInt(totalWeight), subtract each weight until <0 -> that entry; gated by doPlace
// + the previousPiece/allowInRow guard; on non-nil placeCount++, previous=entry, drop capped.
// Exhausted -> BridgeEndFiller. Terminates via genDepth<=30 + the 112 bound + the per-piece caps.
//
// BOUNDED SCOPE (core traversable set + the blaze spawner): we port StartPiece(=BridgeCrossing),
// BridgeStraight, BridgeCrossing, MonsterThrone (blaze spawner), BridgeEndFiller (terminal cap).
// DEFERRED: RoomCrossing, StairsRoom, CastleEntrance, and the whole CASTLE_PIECE_WEIGHTS set
// (CastleSmallCorridor*, CastleCorridor*, CastleStalkRoom). A candidate netherPieceWeight for a
// deferred class yields a nil factory, which generatePiece already treats as "try the next roll"
// (the jar's findAndCreate returns null for an unmatched class), so the walk stays RNG-faithful for
// the ported pieces and caps sooner with a BridgeEndFiller where a deferred piece would go. The
// BRIDGE weight list KEEPS its full jar weights (30/10/10/10/5/5) so the nextInt(totalWeight) draw
// stream is byte-identical; only the block geometry of the unported pieces is absent. Two further
// byte-preserving deferrals: (a) the CASTLE branch of generateAndAddPiece (availableCastlePieces),
// entered only from castle-interior pieces that are all deferred, so never reached from the ported
// set; (b) builder.moveInsideHeights (the whole-graph Y re-anchor) -- the ported pieces sit at
// MAGIC_START_Y and Sulfur places per-chunk, so deferring it keeps the fortress at its start-Y.
//
// ORIENTATION: the fortress pieces use the JAR StructurePiece.getWorldX/Y/Z + setOrientation
// convention (Direction ordinals DOWN=0,UP=1,NORTH=2,SOUTH=3,WEST=4,EAST=5), which DIFFERS from
// Sulfur's world/structure StructurePiece convention (self-consistent but relabeled). The vanilla
// block-offsets are authored against the JAR convention, so this file carries its OWN jar-faithful
// transform (netherPiece.worldX/worldY/worldZ + placeBlock/generateBox/fillColumnDown) rather than
// routing through StructurePiece.placeBlock. CITE: javap StructurePiece.getWorldX/getWorldZ/
// setOrientation + BoundingBox.orientBox + StructurePiece.makeBoundingBox.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

const netherFortressGenDepthCap = 30
const netherFortressMagicStartY = 64
const netherFortressReachBound = 112

var (
	netherBrickState   = stateOf(block.NetherBricks{})
	netherBrickFenceNS = stateOf(block.NetherBrickFence{North: true, South: true})
	netherBrickFenceEW = stateOf(block.NetherBrickFence{East: true, West: true})
)

type netherPieceKind int

const (
	netherKindDeferred netherPieceKind = iota
	netherKindBridgeStraight
	netherKindBridgeCrossing
	netherKindMonsterThrone
	netherKindBridgeEndFiller
	netherKindStart
)

type netherPiece struct {
	bbox        BoundingBox
	orientation block.Direction
	hasOrient   bool
	genDepth    int
	kind        netherPieceKind
}

func (p *netherPiece) BoundingBox() BoundingBox { return p.bbox }

func (p *netherPiece) setOrientation(dir block.Direction) {
	p.hasOrient = true
	p.orientation = dir
}

func (p *netherPiece) worldX(x, z int) int {
	if !p.hasOrient {
		return x
	}
	switch p.orientation {
	case block.Up, block.North:
		return p.bbox.MinX + x
	case block.South:
		return p.bbox.MaxX - z
	case block.West:
		return p.bbox.MinX + z
	default:
		return x
	}
}

func (p *netherPiece) worldY(y int) int {
	if !p.hasOrient {
		return y
	}
	return y + p.bbox.MinY
}

func (p *netherPiece) worldZ(x, z int) int {
	if !p.hasOrient {
		return z
	}
	switch p.orientation {
	case block.Up:
		return p.bbox.MaxZ - z
	case block.North:
		return p.bbox.MinZ + z
	case block.South, block.West:
		return p.bbox.MinZ + x
	default:
		return z
	}
}

func (p *netherPiece) placeBlock(view WorldGenView, st block.StateID, x, y, z int, box BoundingBox) {
	wx := p.worldX(x, z)
	wy := p.worldY(y)
	wz := p.worldZ(x, z)
	if !box.IsInside(wx, wy, wz) {
		return
	}
	view.SetBlock(wx, wy, wz, p.reorientFence(st))
}

func (p *netherPiece) getBlock(view WorldGenView, x, y, z int, box BoundingBox) block.StateID {
	wx := p.worldX(x, z)
	wy := p.worldY(y)
	wz := p.worldZ(x, z)
	if !box.IsInside(wx, wy, wz) {
		return stateAir
	}
	return view.GetBlock(wx, wy, wz)
}

func (p *netherPiece) generateBox(view WorldGenView, box BoundingBox, x0, y0, z0, x1, y1, z1 int, edge, fill block.StateID) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1 {
					p.placeBlock(view, edge, x, y, z, box)
				} else {
					p.placeBlock(view, fill, x, y, z, box)
				}
			}
		}
	}
}

func (p *netherPiece) fillColumnDown(view WorldGenView, st block.StateID, x, startY, z int, box BoundingBox) {
	wx := p.worldX(x, z)
	wz := p.worldZ(x, z)
	wy := p.worldY(startY)
	if !box.IsInside(wx, wy, wz) {
		return
	}
	const worldFloor = -64
	for wy > worldFloor+1 && isReplaceableByStructures(view.GetBlock(wx, wy, wz)) {
		view.SetBlock(wx, wy, wz, st)
		wy--
	}
}

func (p *netherPiece) reorientFence(st block.StateID) block.StateID {
	if !p.hasOrient || p.orientation == block.North || p.orientation == block.East {
		return st
	}
	if st < 0 || int(st) >= len(block.StateList) {
		return st
	}
	f, ok := block.StateList[st].(block.NetherBrickFence)
	if !ok {
		return st
	}
	rot := block.NetherBrickFence{North: f.West, East: f.North, South: f.East, West: f.South, Waterlogged: f.Waterlogged}
	if id, ok := block.ToStateID[rot]; ok {
		return id
	}
	return st
}

type netherPieceWeight struct {
	kind          netherPieceKind
	weight        int
	maxPlaceCount int
	placeCount    int
	allowInRow    bool
}

func (w *netherPieceWeight) isValid() bool { return w.maxPlaceCount == 0 || w.placeCount < w.maxPlaceCount }

func (w *netherPieceWeight) doPlace(int) bool {
	return w.maxPlaceCount == 0 || w.placeCount < w.maxPlaceCount
}

func newBridgePieceWeights() []*netherPieceWeight {
	return []*netherPieceWeight{
		{kind: netherKindBridgeStraight, weight: 30, maxPlaceCount: 0, allowInRow: true},
		{kind: netherKindBridgeCrossing, weight: 10, maxPlaceCount: 4},
		{kind: netherKindDeferred, weight: 10, maxPlaceCount: 4},
		{kind: netherKindDeferred, weight: 10, maxPlaceCount: 3},
		{kind: netherKindMonsterThrone, weight: 5, maxPlaceCount: 2},
		{kind: netherKindDeferred, weight: 5, maxPlaceCount: 1},
	}
}

type netherFortressBuilder struct {
	pieces          []Piece
	availableBridge []*netherPieceWeight
	previous        *netherPieceWeight
	pending         []*netherPiece
}

func (b *netherFortressBuilder) AddPiece(p Piece) { b.pieces = append(b.pieces, p) }

func (b *netherFortressBuilder) FindCollisionPiece(box BoundingBox) Piece {
	return FindCollisionPiece(b.pieces, box)
}

func updatePieceWeight(list []*netherPieceWeight) int {
	valid := false
	total := 0
	for _, w := range list {
		if w.maxPlaceCount > 0 && w.placeCount < w.maxPlaceCount {
			valid = true
		}
		total += w.weight
	}
	if valid {
		return total
	}
	return -1
}

func (b *netherFortressBuilder) generatePiece(rng levelgen.RandomSource, x, y, z int, dir block.Direction, genDepth int) *netherPiece {
	total := updatePieceWeight(b.availableBridge)
	if total > 0 && genDepth <= netherFortressGenDepthCap {
		for i := 0; i < 5; i++ {
			roll := int(rng.NextIntN(int32(total)))
			for _, w := range b.availableBridge {
				roll -= w.weight
				if roll >= 0 {
					continue
				}
				if !w.doPlace(genDepth) {
					break
				}
				if w == b.previous && !w.allowInRow {
					break
				}
				piece := findAndCreateBridgePieceFactory(w, b, rng, x, y, z, dir, genDepth)
				if piece == nil {
					break
				}
				w.placeCount++
				b.previous = w
				if !w.isValid() {
					b.removeWeight(w)
				}
				return piece
			}
		}
	}
	return newBridgeEndFiller(b, x, y, z, dir, genDepth)
}

func (b *netherFortressBuilder) removeWeight(target *netherPieceWeight) {
	out := b.availableBridge[:0]
	for _, w := range b.availableBridge {
		if w != target {
			out = append(out, w)
		}
	}
	b.availableBridge = out
}

func findAndCreateBridgePieceFactory(w *netherPieceWeight, b *netherFortressBuilder, rng levelgen.RandomSource, x, y, z int, dir block.Direction, genDepth int) *netherPiece {
	switch w.kind {
	case netherKindBridgeStraight:
		return createBridgeStraight(b, x, y, z, dir, genDepth)
	case netherKindBridgeCrossing:
		return createBridgeCrossing(b, x, y, z, dir, genDepth)
	case netherKindMonsterThrone:
		return createMonsterThrone(b, x, y, z, genDepth, dir)
	default:
		return nil
	}
}

func (b *netherFortressBuilder) generateAndAddPiece(start *netherPiece, rng levelgen.RandomSource, x, y, z int, dir block.Direction, genDepth int) *netherPiece {
	if netherAbs(x-start.bbox.MinX) > netherFortressReachBound || netherAbs(z-start.bbox.MinZ) > netherFortressReachBound {
		return newBridgeEndFiller(b, x, y, z, dir, genDepth)
	}
	piece := b.generatePiece(rng, x, y, z, dir, genDepth+1)
	if piece != nil {
		b.AddPiece(piece)
		b.pending = append(b.pending, piece)
	}
	return piece
}

func netherAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (p *netherPiece) generateChildForward(start *netherPiece, b *netherFortressBuilder, rng levelgen.RandomSource, offsetXZ, offsetY int) *netherPiece {
	switch p.orientation {
	case block.North:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX+offsetXZ, p.bbox.MinY+offsetY, p.bbox.MinZ-1, block.North, p.genDepth)
	case block.South:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX+offsetXZ, p.bbox.MinY+offsetY, p.bbox.MaxZ+1, block.South, p.genDepth)
	case block.West:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX-1, p.bbox.MinY+offsetY, p.bbox.MinZ+offsetXZ, block.West, p.genDepth)
	case block.East:
		return b.generateAndAddPiece(start, rng, p.bbox.MaxX+1, p.bbox.MinY+offsetY, p.bbox.MinZ+offsetXZ, block.East, p.genDepth)
	}
	return nil
}

func (p *netherPiece) generateChildLeft(start *netherPiece, b *netherFortressBuilder, rng levelgen.RandomSource, offsetY, offsetXZ int) *netherPiece {
	switch p.orientation {
	case block.North:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX-1, p.bbox.MinY+offsetY, p.bbox.MinZ+offsetXZ, block.West, p.genDepth)
	case block.South:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX-1, p.bbox.MinY+offsetY, p.bbox.MinZ+offsetXZ, block.West, p.genDepth)
	case block.West:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX+offsetXZ, p.bbox.MinY+offsetY, p.bbox.MinZ-1, block.North, p.genDepth)
	case block.East:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX+offsetXZ, p.bbox.MinY+offsetY, p.bbox.MinZ-1, block.North, p.genDepth)
	}
	return nil
}

func (p *netherPiece) generateChildRight(start *netherPiece, b *netherFortressBuilder, rng levelgen.RandomSource, offsetY, offsetXZ int) *netherPiece {
	switch p.orientation {
	case block.North:
		return b.generateAndAddPiece(start, rng, p.bbox.MaxX+1, p.bbox.MinY+offsetY, p.bbox.MinZ+offsetXZ, block.East, p.genDepth)
	case block.South:
		return b.generateAndAddPiece(start, rng, p.bbox.MaxX+1, p.bbox.MinY+offsetY, p.bbox.MinZ+offsetXZ, block.East, p.genDepth)
	case block.West:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX+offsetXZ, p.bbox.MinY+offsetY, p.bbox.MaxZ+1, block.South, p.genDepth)
	case block.East:
		return b.generateAndAddPiece(start, rng, p.bbox.MinX+offsetXZ, p.bbox.MinY+offsetY, p.bbox.MaxZ+1, block.South, p.genDepth)
	}
	return nil
}

func (p *netherPiece) addChildren(start *netherPiece, b *netherFortressBuilder, rng levelgen.RandomSource) {
	switch p.kind {
	case netherKindStart, netherKindBridgeCrossing:
		p.generateChildForward(start, b, rng, 8, 3)
		p.generateChildLeft(start, b, rng, 3, 8)
		p.generateChildRight(start, b, rng, 3, 8)
	case netherKindBridgeStraight:
		p.generateChildForward(start, b, rng, 1, 3)
	}
}

func makeBoundingBox(x, y, z int, dir block.Direction, sx, sy, sz int) BoundingBox {
	if dir == block.North || dir == block.South {
		return BoundingBox{x, y, z, x + sx - 1, y + sy - 1, z + sz - 1}
	}
	return BoundingBox{x, y, z, x + sz - 1, y + sy - 1, z + sx - 1}
}

func orientBox(x, y, z, ox, oy, oz, sx, sy, sz int, dir block.Direction) BoundingBox {
	switch dir {
	case block.West:
		return BoundingBox{x + ox, y + oy, z - sz + 1 + oz, x + sx - 1 + ox, y + sy - 1 + oy, z + oz}
	case block.North:
		return BoundingBox{x - sz + 1 + oz, y + oy, z + ox, x + oz, y + sy - 1 + oy, z + sx - 1 + ox}
	case block.East:
		return BoundingBox{x + oz, y + oy, z + ox, x + sz - 1 + oz, y + sy - 1 + oy, z + sx - 1 + ox}
	default:
		return BoundingBox{x + ox, y + oy, z + oz, x + sx - 1 + ox, y + sy - 1 + oy, z + sz - 1 + oz}
	}
}

func isOkBox(b BoundingBox) bool { return b.MinY > 10 }

func newStartPiece(b *netherFortressBuilder, rng levelgen.RandomSource, i, j int) *netherPiece {
	dir := getRandomHorizontalDirection(rng)
	bbox := makeBoundingBox(i, netherFortressMagicStartY, j, dir, 19, 10, 19)
	p := &netherPiece{bbox: bbox, genDepth: 0, kind: netherKindStart}
	p.setOrientation(dir)
	b.availableBridge = newBridgePieceWeights()
	return p
}

func createBridgeCrossing(b *netherFortressBuilder, x, y, z int, dir block.Direction, genDepth int) *netherPiece {
	bbox := orientBox(x, y, z, -8, -3, 0, 19, 10, 19, dir)
	if !isOkBox(bbox) || b.FindCollisionPiece(bbox) != nil {
		return nil
	}
	p := &netherPiece{bbox: bbox, genDepth: genDepth, kind: netherKindBridgeCrossing}
	p.setOrientation(dir)
	return p
}

func createBridgeStraight(b *netherFortressBuilder, x, y, z int, dir block.Direction, genDepth int) *netherPiece {
	bbox := orientBox(x, y, z, -1, -3, 0, 5, 10, 19, dir)
	if !isOkBox(bbox) || b.FindCollisionPiece(bbox) != nil {
		return nil
	}
	p := &netherPiece{bbox: bbox, genDepth: genDepth, kind: netherKindBridgeStraight}
	p.setOrientation(dir)
	return p
}

func createMonsterThrone(b *netherFortressBuilder, x, y, z, genDepth int, dir block.Direction) *netherPiece {
	bbox := orientBox(x, y, z, -2, 0, 0, 7, 8, 9, dir)
	if !isOkBox(bbox) || b.FindCollisionPiece(bbox) != nil {
		return nil
	}
	p := &netherPiece{bbox: bbox, genDepth: genDepth, kind: netherKindMonsterThrone}
	p.setOrientation(dir)
	return p
}

func newBridgeEndFiller(b *netherFortressBuilder, x, y, z int, dir block.Direction, genDepth int) *netherPiece {
	bbox := orientBox(x, y, z, -1, -3, 0, 5, 10, 8, dir)
	if !isOkBox(bbox) || b.FindCollisionPiece(bbox) != nil {
		return nil
	}
	p := &netherPiece{bbox: bbox, genDepth: genDepth, kind: netherKindBridgeEndFiller}
	p.setOrientation(dir)
	return p
}

func (p *netherPiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	switch p.kind {
	case netherKindStart, netherKindBridgeCrossing:
		p.postProcessBridgeCrossing(view, box)
	case netherKindBridgeStraight:
		p.postProcessBridgeStraight(view, box)
	case netherKindMonsterThrone:
		p.postProcessMonsterThrone(view, box, rng)
	case netherKindBridgeEndFiller:
		p.postProcessBridgeEndFiller(view, box)
	}
}

func (p *netherPiece) postProcessBridgeCrossing(view WorldGenView, box BoundingBox) {
	nb := netherBrickState
	air := stateAir
	p.generateBox(view, box, 7, 3, 0, 11, 4, 18, nb, nb)
	p.generateBox(view, box, 0, 3, 7, 18, 4, 11, nb, nb)
	p.generateBox(view, box, 8, 5, 0, 10, 7, 18, air, air)
	p.generateBox(view, box, 0, 5, 8, 18, 7, 10, air, air)
	p.generateBox(view, box, 7, 5, 0, 7, 5, 7, nb, nb)
	p.generateBox(view, box, 7, 5, 11, 7, 5, 18, nb, nb)
	p.generateBox(view, box, 11, 5, 0, 11, 5, 7, nb, nb)
	p.generateBox(view, box, 11, 5, 11, 11, 5, 18, nb, nb)
	p.generateBox(view, box, 0, 5, 7, 7, 5, 7, nb, nb)
	p.generateBox(view, box, 11, 5, 7, 18, 5, 7, nb, nb)
	p.generateBox(view, box, 0, 5, 11, 7, 5, 11, nb, nb)
	p.generateBox(view, box, 11, 5, 11, 18, 5, 11, nb, nb)
	p.generateBox(view, box, 7, 2, 0, 11, 2, 5, nb, nb)
	p.generateBox(view, box, 7, 2, 13, 11, 2, 18, nb, nb)
	p.generateBox(view, box, 7, 0, 0, 11, 1, 3, nb, nb)
	p.generateBox(view, box, 7, 0, 15, 11, 1, 18, nb, nb)
	for x := 7; x <= 11; x++ {
		for z := 0; z <= 2; z++ {
			p.fillColumnDown(view, nb, x, -1, z, box)
			p.fillColumnDown(view, nb, x, -1, 18-z, box)
		}
	}
	p.generateBox(view, box, 0, 2, 7, 5, 2, 11, nb, nb)
	p.generateBox(view, box, 13, 2, 7, 18, 2, 11, nb, nb)
	p.generateBox(view, box, 0, 0, 7, 3, 1, 11, nb, nb)
	p.generateBox(view, box, 15, 0, 7, 18, 1, 11, nb, nb)
	for z := 7; z <= 11; z++ {
		for x := 0; x <= 2; x++ {
			p.fillColumnDown(view, nb, x, -1, z, box)
			p.fillColumnDown(view, nb, 18-x, -1, z, box)
		}
	}
}

func (p *netherPiece) postProcessBridgeStraight(view WorldGenView, box BoundingBox) {
	nb := netherBrickState
	air := stateAir
	fenceNS := netherBrickFenceNS
	fenceEW := netherBrickFenceEW
	p.generateBox(view, box, 0, 3, 0, 4, 4, 18, nb, nb)
	p.generateBox(view, box, 1, 5, 0, 3, 7, 18, air, air)
	p.generateBox(view, box, 0, 5, 0, 0, 5, 18, nb, nb)
	p.generateBox(view, box, 4, 5, 0, 4, 5, 18, nb, nb)
	p.generateBox(view, box, 0, 2, 0, 4, 2, 5, nb, nb)
	p.generateBox(view, box, 0, 2, 13, 4, 2, 18, nb, nb)
	p.generateBox(view, box, 0, 0, 0, 4, 1, 3, nb, nb)
	p.generateBox(view, box, 0, 0, 15, 4, 1, 18, nb, nb)
	for x := 0; x <= 4; x++ {
		for z := 0; z <= 2; z++ {
			p.fillColumnDown(view, nb, x, -1, z, box)
			p.fillColumnDown(view, nb, x, -1, 18-z, box)
		}
	}
	p.generateBox(view, box, 0, 1, 1, 0, 4, 1, fenceNS, fenceNS)
	p.generateBox(view, box, 0, 3, 4, 0, 4, 4, fenceNS, fenceNS)
	p.generateBox(view, box, 0, 3, 14, 0, 4, 14, fenceNS, fenceNS)
	p.generateBox(view, box, 0, 1, 17, 0, 4, 17, fenceNS, fenceNS)
	p.generateBox(view, box, 4, 1, 1, 4, 4, 1, fenceEW, fenceEW)
	p.generateBox(view, box, 4, 3, 4, 4, 4, 4, fenceEW, fenceEW)
	p.generateBox(view, box, 4, 3, 14, 4, 4, 14, fenceEW, fenceEW)
	p.generateBox(view, box, 4, 1, 17, 4, 4, 17, fenceEW, fenceEW)
}

func (p *netherPiece) postProcessMonsterThrone(view WorldGenView, box BoundingBox, rng levelgen.RandomSource) {
	nb := netherBrickState
	air := stateAir
	fenceWE := stateOf(block.NetherBrickFence{West: true, East: true})
	fenceNS2 := stateOf(block.NetherBrickFence{North: true, South: true})

	p.generateBox(view, box, 0, 2, 0, 6, 7, 7, air, air)
	p.generateBox(view, box, 1, 0, 0, 5, 1, 7, nb, nb)
	p.generateBox(view, box, 1, 2, 1, 5, 2, 7, nb, nb)
	p.generateBox(view, box, 1, 3, 2, 5, 3, 7, nb, nb)
	p.generateBox(view, box, 1, 4, 3, 5, 4, 7, nb, nb)
	p.generateBox(view, box, 1, 2, 0, 1, 4, 2, nb, nb)
	p.generateBox(view, box, 5, 2, 0, 5, 4, 2, nb, nb)
	p.generateBox(view, box, 1, 5, 2, 1, 5, 3, nb, nb)
	p.generateBox(view, box, 5, 5, 2, 5, 5, 3, nb, nb)
	p.generateBox(view, box, 0, 5, 3, 0, 5, 8, nb, nb)
	p.generateBox(view, box, 6, 5, 3, 6, 5, 8, nb, nb)
	p.generateBox(view, box, 1, 5, 8, 5, 5, 8, nb, nb)

	p.placeBlock(view, stateOf(block.NetherBrickFence{West: true, East: true}), 1, 6, 3, box)
	p.placeBlock(view, stateOf(block.NetherBrickFence{East: true}), 5, 6, 3, box)
	p.placeBlock(view, stateOf(block.NetherBrickFence{East: true, North: true}), 0, 6, 3, box)
	p.placeBlock(view, stateOf(block.NetherBrickFence{West: true, North: true}), 6, 6, 3, box)
	p.generateBox(view, box, 0, 6, 4, 0, 6, 7, fenceNS2, fenceNS2)
	p.generateBox(view, box, 6, 6, 4, 6, 6, 7, fenceNS2, fenceNS2)
	p.placeBlock(view, stateOf(block.NetherBrickFence{East: true, South: true}), 0, 6, 8, box)
	p.placeBlock(view, stateOf(block.NetherBrickFence{West: true, South: true}), 6, 6, 8, box)
	p.generateBox(view, box, 1, 6, 8, 5, 6, 8, fenceWE, fenceWE)
	p.placeBlock(view, stateOf(block.NetherBrickFence{East: true}), 1, 7, 8, box)
	p.generateBox(view, box, 2, 7, 8, 4, 7, 8, fenceWE, fenceWE)
	p.placeBlock(view, stateOf(block.NetherBrickFence{West: true}), 5, 7, 8, box)
	p.placeBlock(view, stateOf(block.NetherBrickFence{East: true}), 2, 8, 8, box)
	p.placeBlock(view, fenceWE, 3, 8, 8, box)
	p.placeBlock(view, stateOf(block.NetherBrickFence{West: true}), 4, 8, 8, box)

	wx := p.worldX(3, 5)
	wy := p.worldY(5)
	wz := p.worldZ(3, 5)
	if box.IsInside(wx, wy, wz) {
		p.placeBlock(view, spawnerStateID, 3, 5, 5, box)
		view.SetSpawner(wx, wy, wz, "minecraft:blaze")
	}

	for x := 0; x <= 6; x++ {
		for z := 0; z <= 6; z++ {
			p.fillColumnDown(view, nb, x, -1, z, box)
		}
	}
	_ = rng
}

func (p *netherPiece) postProcessBridgeEndFiller(view WorldGenView, box BoundingBox) {
	nb := netherBrickState
	air := stateAir
	p.generateBox(view, box, 0, 3, 0, 4, 4, 7, nb, nb)
	p.generateBox(view, box, 1, 5, 0, 3, 7, 7, air, air)
	p.generateBox(view, box, 0, 5, 0, 0, 5, 7, nb, nb)
	p.generateBox(view, box, 4, 5, 0, 4, 5, 7, nb, nb)
	p.generateBox(view, box, 0, 2, 0, 4, 2, 7, nb, nb)
	p.generateBox(view, box, 0, 0, 0, 4, 1, 7, nb, nb)
	for x := 0; x <= 4; x++ {
		for z := 0; z <= 7; z++ {
			p.fillColumnDown(view, nb, x, -1, z, box)
		}
	}
}

type netherFortressStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

func NewNetherFortressStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:nether_complexes")
	if err != nil {
		return nil, err
	}
	biomes, err := HasStructureBiomes("nether_fortress")
	if err != nil {
		return nil, err
	}
	return &netherFortressStartGen{placement: set.Placement, biomeAllow: biomes}, nil
}

func (g *netherFortressStartGen) GenerateStarts(seed int64, pos level.ChunkPos, _ SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	pickRng := levelgen.NewWorldgenRandom(0)
	pickRng.SetLargeFeatureSeed(seed, cx, cz)
	if int(pickRng.NextIntN(5)) >= 2 {
		return nil
	}

	centerX := cx*16 + 8
	centerZ := cz*16 + 8
	if !g.biomeAllow[biomeAt(centerX, netherFortressMagicStartY, centerZ).String()] {
		return nil
	}

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	builder := &netherFortressBuilder{}
	start := newStartPiece(builder, rng, cx*16+2, cz*16+2)
	builder.AddPiece(start)
	start.addChildren(start, builder, rng)
	for len(builder.pending) > 0 {
		idx := int(rng.NextIntN(int32(len(builder.pending))))
		child := builder.pending[idx]
		builder.pending = append(builder.pending[:idx], builder.pending[idx+1:]...)
		child.addChildren(start, builder, rng)
	}

	ss := &StructureStart{
		Structure: "minecraft:fortress",
		ChunkPos:  pos,
		Pieces:    builder.pieces,
	}
	ss.RecomputeBBox()
	return []*StructureStart{ss}
}
