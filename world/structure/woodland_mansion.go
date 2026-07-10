package structure

// woodland_mansion.go -- the WOODLAND MANSION surface structure, ported 1:1 from CFR
// (temp/cache/26.2-inner.jar): WoodlandMansionStructure + WoodlandMansionPieces (MansionGrid +
// MansionPiecePlacer + FirstFloor/SecondFloor/ThirdFloor room collections + SimpleGrid). Unlike
// the temples (single hardcoded piece) or the jigsaw structures (data-driven BFS pool graph), the
// mansion is a CODE-generated deterministic seeded piece graph: a grid RNG decides the room
// layout, then a piece placer emits ~dozens of WoodlandMansionPiece .nbt templates (walls, roof,
// corridors, rooms) at rotation/mirror-transformed positions. The .nbt geometry itself is
// data (structure/woodland_mansion/<name>.nbt), placed via the shared TemplateStructurePiece path.
//
// Ported from CFR:
//   - WoodlandMansionStructure.findGenerationPoint: rot = Rotation.getRandom(rng) (RNG draw);
//     blockPos = getLowestYIn5by5BoxOffset7Blocks(ctx, rot) (surface projection, WORLD_SURFACE_WG);
//     if blockPos.y < 60 -> Optional.empty (the surface gate); else generatePieces.
//   - Structure.getLowestYIn5by5BoxOffset7Blocks / getLowestY / getCornerHeights (the 4-corner
//     min of the WORLD_SURFACE_WG heights over a 5x5 box offset 7 blocks into the chunk, the
//     offset sign flipping by rotation).
//   - WoodlandMansionPieces.generateMansion -> new MansionGrid(rng); new MansionPiecePlacer;
//     placer.createMansion(origin, rot, pieces, grid).
//   - MansionGrid (baseGrid + thirdFloorGrid + floorRooms[3]): recursiveCorridor, cleanEdges,
//     identifyRooms (Util.shuffle draw), setupThirdFloor -- the RNG-critical grid graph.
//   - MansionPiecePlacer.createMansion / traverseOuterWalls / createRoof / entrance / the room
//     placement (addRoom1x1/1x2/2x2/2x2Secret) with the door-dir + room-name RNG draws.
//   - FirstFloorRoomCollection / SecondFloorRoomCollection / ThirdFloorRoomCollection (the
//     get1x1/get1x1Secret/get1x2SideEntrance/get1x2FrontEntrance/get1x2Secret/get2x2/get2x2Secret
//     room-name generators, each drawing nextInt).
//   - woodland_mansions structure_set: random_spread TRIANGULAR salt 10387319 / spacing 80 /
//     separation 20; has_structure/woodland_mansion biome tag (dark_forest etc).
//
// LOOT / MOBS DEFERRED (cited): WoodlandMansionPiece.handleDataMarker spawns evokers/vindicators/
// allays + fills the mansion loot chest. Like every other structure here, the block-entity loot +
// the structure mob spawns are a deferred subsystem; the .nbt geometry (incl. the chest BLOCK and
// the spawner-marker positions) is placed via the shared TemplateStructurePiece data-marker path,
// which records chests + dispatches markers exactly as end_city does. No RNG is drawn by the
// deferred spawn/loot at PLACE time in a way that would desync the grid RNG (the grid + placer
// RNG all runs at START time, before any postProcess).

import (
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

const (
	woodlandMansionID  = "minecraft:mansion"
	woodlandMansionSet = "minecraft:woodland_mansions"
	// woodland_mansions placement: random_spread TRIANGULAR, salt 10387319 / spacing 80 /
	// separation 20 (verified vs the embedded structure_set JSON in NewWoodlandMansionStartGen).
	woodlandMansionSalt       = 10387319
	woodlandMansionSpacing    = 80
	woodlandMansionSeparation = 20
	// The surface gate: findGenerationPoint returns empty when the lowest 5x5 corner Y < 60.
	woodlandMansionMinY = 60
)

// simpleGrid ports WoodlandMansionPieces$SimpleGrid: a width x height int grid with an
// out-of-bounds sentinel value. get/set clamp to bounds; out-of-range get returns valueIfOutside.
type simpleGrid struct {
	grid           [][]int
	width, height  int
	valueIfOutside int
}

func newSimpleGrid(width, height, valueIfOutside int) *simpleGrid {
	g := &simpleGrid{width: width, height: height, valueIfOutside: valueIfOutside}
	g.grid = make([][]int, width)
	for x := range g.grid {
		g.grid[x] = make([]int, height)
	}
	return g
}

func (g *simpleGrid) set(x, y, value int) {
	if x >= 0 && x < g.width && y >= 0 && y < g.height {
		g.grid[x][y] = value
	}
}

func (g *simpleGrid) setBox(x0, y0, x1, y1, value int) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			g.set(x, y, value)
		}
	}
}

func (g *simpleGrid) get(x, y int) int {
	if x >= 0 && x < g.width && y >= 0 && y < g.height {
		return g.grid[x][y]
	}
	return g.valueIfOutside
}

func (g *simpleGrid) setif(x, y, ifValue, value int) {
	if g.get(x, y) == ifValue {
		g.set(x, y, value)
	}
}

func (g *simpleGrid) edgesTo(x, y, ifValue int) bool {
	return g.get(x-1, y) == ifValue || g.get(x+1, y) == ifValue || g.get(x, y+1) == ifValue || g.get(x, y-1) == ifValue
}

// mansionGrid ports WoodlandMansionPieces$MansionGrid: the deterministic seeded room-layout graph.
// The cell values + flags mirror the jar's int constants exactly.
type mansionGrid struct {
	rng            levelgen.RandomSource
	baseGrid       *simpleGrid
	thirdFloorGrid *simpleGrid
	floorRooms     [3]*simpleGrid
	entranceX      int
	entranceY      int
}

// mansion cell-value + flag constants (WoodlandMansionPieces$MansionGrid). Kept verbatim.
const (
	mgClear     = 0
	mgCorridor  = 1
	mgRoom      = 2
	mgStartRoom = 3
	mgBlocked   = 5

	mgRoom1x1          = 65536
	mgRoom1x2          = 131072
	mgRoom2x2          = 262144
	mgRoomOriginFlag   = 0x100000
	mgRoomDoorFlag     = 0x200000
	mgRoomStairsFlag   = 0x400000
	mgRoomCorridorFlag = 0x800000
	mgRoomTypeMask     = 983040
	mgRoomIDMask       = 65535
)

func newMansionGrid(rng levelgen.RandomSource) *mansionGrid {
	m := &mansionGrid{rng: rng}
	m.entranceX = 7
	m.entranceY = 4
	m.baseGrid = newSimpleGrid(11, 11, mgBlocked)
	m.baseGrid.setBox(m.entranceX, m.entranceY, m.entranceX+1, m.entranceY+1, mgStartRoom)
	m.baseGrid.setBox(m.entranceX-1, m.entranceY, m.entranceX-1, m.entranceY+1, mgRoom)
	m.baseGrid.setBox(m.entranceX+2, m.entranceY-2, m.entranceX+3, m.entranceY+3, mgBlocked)
	m.baseGrid.setBox(m.entranceX+1, m.entranceY-2, m.entranceX+1, m.entranceY-1, mgCorridor)
	m.baseGrid.setBox(m.entranceX+1, m.entranceY+2, m.entranceX+1, m.entranceY+3, mgCorridor)
	m.baseGrid.set(m.entranceX-1, m.entranceY-1, mgCorridor)
	m.baseGrid.set(m.entranceX-1, m.entranceY+2, mgCorridor)
	m.baseGrid.setBox(0, 0, 11, 1, mgBlocked)
	m.baseGrid.setBox(0, 9, 11, 11, mgBlocked)
	m.recursiveCorridor(m.baseGrid, m.entranceX, m.entranceY-2, block.West, 6)
	m.recursiveCorridor(m.baseGrid, m.entranceX, m.entranceY+3, block.West, 6)
	m.recursiveCorridor(m.baseGrid, m.entranceX-2, m.entranceY-1, block.West, 3)
	m.recursiveCorridor(m.baseGrid, m.entranceX-2, m.entranceY+2, block.West, 3)
	for m.cleanEdges(m.baseGrid) {
	}
	m.floorRooms[0] = newSimpleGrid(11, 11, mgBlocked)
	m.floorRooms[1] = newSimpleGrid(11, 11, mgBlocked)
	m.floorRooms[2] = newSimpleGrid(11, 11, mgBlocked)
	m.identifyRooms(m.baseGrid, m.floorRooms[0])
	m.identifyRooms(m.baseGrid, m.floorRooms[1])
	m.floorRooms[0].setBox(m.entranceX+1, m.entranceY, m.entranceX+1, m.entranceY+1, mgRoomCorridorFlag)
	m.floorRooms[1].setBox(m.entranceX+1, m.entranceY, m.entranceX+1, m.entranceY+1, mgRoomCorridorFlag)
	m.thirdFloorGrid = newSimpleGrid(m.baseGrid.width, m.baseGrid.height, mgBlocked)
	m.setupThirdFloor()
	m.identifyRooms(m.thirdFloorGrid, m.floorRooms[2])
	return m
}

// isHouse ports MansionGrid.isHouse: a cell is "house" if corridor/room/startRoom/testRoom(4).
func mansionIsHouse(g *simpleGrid, x, y int) bool {
	v := g.get(x, y)
	return v == mgCorridor || v == mgRoom || v == mgStartRoom || v == 4
}

func (m *mansionGrid) isRoomId(g *simpleGrid, x, y, floor, roomId int) bool {
	return (m.floorRooms[floor].get(x, y) & mgRoomIDMask) == roomId
}

// get1x2RoomDirection ports MansionGrid.get1x2RoomDirection: the horizontal dir toward the paired
// cell of a 1x2 room (the first Plane.HORIZONTAL dir whose neighbor shares the roomId). May be
// zero-value (block.Down sentinel means "none" -- vanilla returns null; we test callers explicitly).
func (m *mansionGrid) get1x2RoomDirection(g *simpleGrid, x, y, floorNum, roomId int) (block.Direction, bool) {
	for _, dir := range horizontalPlane {
		if m.isRoomId(g, x+stepX(dir), y+stepZ(dir), floorNum, roomId) {
			return dir, true
		}
	}
	return block.Down, false
}

// gridPos ports MansionGrid$GridPos.
type gridPos struct{ x, y int }

// shuffleGridPos ports Util.shuffle(List, RandomSource) over a GridPos list (Fisher-Yates from the
// top; draw count size-1 -- keeps the grid RNG in lockstep with vanilla).
func shuffleGridPos(list []gridPos, rng levelgen.RandomSource) {
	for i := len(list); i > 1; i-- {
		j := int(rng.NextIntN(int32(i)))
		list[i-1], list[j] = list[j], list[i-1]
	}
}

// recursiveCorridor ports MansionGrid.recursiveCorridor: lay a corridor cell + probabilistically
// branch, then mark adjacent room cells. The nextInt(4)+from2DDataValue + nextBoolean draws are
// load-bearing (they steer the whole layout).
func (m *mansionGrid) recursiveCorridor(g *simpleGrid, x, y int, heading block.Direction, depth int) {
	if depth <= 0 {
		return
	}
	g.set(x, y, mgCorridor)
	g.setif(x+stepX(heading), y+stepZ(heading), mgClear, mgCorridor)
	for attempts := 0; attempts < 8; attempts++ {
		nextDir := from2DDataValue(m.rng.NextIntN(4))
		if nextDir == directionOpposite(heading) || (nextDir == block.East && m.rng.NextBoolean()) {
			continue
		}
		nx := x + stepX(heading)
		ny := y + stepZ(heading)
		if g.get(nx+stepX(nextDir), ny+stepZ(nextDir)) != mgClear || g.get(nx+stepX(nextDir)*2, ny+stepZ(nextDir)*2) != mgClear {
			continue
		}
		m.recursiveCorridor(g, x+stepX(heading)+stepX(nextDir), y+stepZ(heading)+stepZ(nextDir), nextDir, depth-1)
		break
	}
	cw := directionClockWise(heading)
	ccw := directionCounterClockWise(heading)
	g.setif(x+stepX(cw), y+stepZ(cw), mgClear, mgRoom)
	g.setif(x+stepX(ccw), y+stepZ(ccw), mgClear, mgRoom)
	g.setif(x+stepX(heading)+stepX(cw), y+stepZ(heading)+stepZ(cw), mgClear, mgRoom)
	g.setif(x+stepX(heading)+stepX(ccw), y+stepZ(heading)+stepZ(ccw), mgClear, mgRoom)
	g.setif(x+stepX(heading)*2, y+stepZ(heading)*2, mgClear, mgRoom)
	g.setif(x+stepX(cw)*2, y+stepZ(cw)*2, mgClear, mgRoom)
	g.setif(x+stepX(ccw)*2, y+stepZ(ccw)*2, mgClear, mgRoom)
}

// cleanEdges ports MansionGrid.cleanEdges: promote clear cells with >=3 direct house neighbors (or
// 2 direct + <=1 diagonal) to rooms. Returns whether it changed anything (drive to fixpoint).
func (m *mansionGrid) cleanEdges(g *simpleGrid) bool {
	touched := false
	for y := 0; y < g.height; y++ {
		for x := 0; x < g.width; x++ {
			if g.get(x, y) != mgClear {
				continue
			}
			directNeighbors := 0
			directNeighbors += boolToInt(mansionIsHouse(g, x+1, y))
			directNeighbors += boolToInt(mansionIsHouse(g, x-1, y))
			directNeighbors += boolToInt(mansionIsHouse(g, x, y+1))
			directNeighbors += boolToInt(mansionIsHouse(g, x, y-1))
			if directNeighbors >= 3 {
				g.set(x, y, mgRoom)
				touched = true
				continue
			}
			if directNeighbors != 2 {
				continue
			}
			diagonalNeighbors := 0
			diagonalNeighbors += boolToInt(mansionIsHouse(g, x+1, y+1))
			diagonalNeighbors += boolToInt(mansionIsHouse(g, x-1, y+1))
			diagonalNeighbors += boolToInt(mansionIsHouse(g, x+1, y-1))
			diagonalNeighbors += boolToInt(mansionIsHouse(g, x-1, y-1))
			if diagonalNeighbors > 1 {
				continue
			}
			g.set(x, y, mgRoom)
			touched = true
		}
	}
	return touched
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// setupThirdFloor ports MansionGrid.setupThirdFloor: pick a stairs-eligible 1x2 door room on the
// second floor, mark the stairs flag + the third-floor start room, and grow a corridor from it.
func (m *mansionGrid) setupThirdFloor() {
	var potentialRooms []gridPos
	floor := m.floorRooms[1]
	for y := 0; y < m.thirdFloorGrid.height; y++ {
		for x := 0; x < m.thirdFloorGrid.width; x++ {
			roomData := floor.get(x, y)
			roomType := roomData & 0xF0000
			if roomType != mgRoom1x2 || (roomData&mgRoomDoorFlag) != mgRoomDoorFlag {
				continue
			}
			potentialRooms = append(potentialRooms, gridPos{x, y})
		}
	}
	if len(potentialRooms) == 0 {
		m.thirdFloorGrid.setBox(0, 0, m.thirdFloorGrid.width, m.thirdFloorGrid.height, mgBlocked)
		return
	}
	roomPos := potentialRooms[m.rng.NextIntN(int32(len(potentialRooms)))]
	roomData := floor.get(roomPos.x, roomPos.y)
	floor.set(roomPos.x, roomPos.y, roomData|mgRoomStairsFlag)
	roomDir, _ := m.get1x2RoomDirection(m.baseGrid, roomPos.x, roomPos.y, 1, roomData&mgRoomIDMask)
	roomEndX := roomPos.x + stepX(roomDir)
	roomEndY := roomPos.y + stepZ(roomDir)
	for y := 0; y < m.thirdFloorGrid.height; y++ {
		for x := 0; x < m.thirdFloorGrid.width; x++ {
			if !mansionIsHouse(m.baseGrid, x, y) {
				m.thirdFloorGrid.set(x, y, mgBlocked)
				continue
			}
			if x == roomPos.x && y == roomPos.y {
				m.thirdFloorGrid.set(x, y, mgStartRoom)
				continue
			}
			if x != roomEndX || y != roomEndY {
				continue
			}
			m.thirdFloorGrid.set(x, y, mgStartRoom)
			m.floorRooms[2].set(x, y, mgRoomCorridorFlag)
		}
	}
	var potentialCorridors []block.Direction
	for _, dir := range horizontalPlane {
		if m.thirdFloorGrid.get(roomEndX+stepX(dir), roomEndY+stepZ(dir)) != mgClear {
			continue
		}
		potentialCorridors = append(potentialCorridors, dir)
	}
	if len(potentialCorridors) == 0 {
		m.thirdFloorGrid.setBox(0, 0, m.thirdFloorGrid.width, m.thirdFloorGrid.height, mgBlocked)
		floor.set(roomPos.x, roomPos.y, roomData)
		return
	}
	corridorDir := potentialCorridors[m.rng.NextIntN(int32(len(potentialCorridors)))]
	m.recursiveCorridor(m.thirdFloorGrid, roomEndX+stepX(corridorDir), roomEndY+stepZ(corridorDir), corridorDir, 4)
	for m.cleanEdges(m.thirdFloorGrid) {
	}
}

// identifyRooms ports MansionGrid.identifyRooms: shuffle the room cells, then greedily grow each
// unclaimed cell into a 1x1 / 1x2 / 2x2 room with an origin/door flag. The Util.shuffle draw +
// the two nextBoolean door-corner draws per room are load-bearing.
func (m *mansionGrid) identifyRooms(fromGrid, roomGrid *simpleGrid) {
	var roomPos []gridPos
	for y := 0; y < fromGrid.height; y++ {
		for x := 0; x < fromGrid.width; x++ {
			if fromGrid.get(x, y) != mgRoom {
				continue
			}
			roomPos = append(roomPos, gridPos{x, y})
		}
	}
	shuffleGridPos(roomPos, m.rng)
	roomId := 10
	for _, pos := range roomPos {
		x := pos.x
		y := pos.y
		if roomGrid.get(x, y) != mgClear {
			continue
		}
		x0, x1 := x, x
		y0, y1 := y, y
		typ := mgRoom1x1
		switch {
		case roomGrid.get(x+1, y) == mgClear && roomGrid.get(x, y+1) == mgClear && roomGrid.get(x+1, y+1) == mgClear && fromGrid.get(x+1, y) == mgRoom && fromGrid.get(x, y+1) == mgRoom && fromGrid.get(x+1, y+1) == mgRoom:
			x1++
			y1++
			typ = mgRoom2x2
		case roomGrid.get(x-1, y) == mgClear && roomGrid.get(x, y+1) == mgClear && roomGrid.get(x-1, y+1) == mgClear && fromGrid.get(x-1, y) == mgRoom && fromGrid.get(x, y+1) == mgRoom && fromGrid.get(x-1, y+1) == mgRoom:
			x0--
			y1++
			typ = mgRoom2x2
		case roomGrid.get(x-1, y) == mgClear && roomGrid.get(x, y-1) == mgClear && roomGrid.get(x-1, y-1) == mgClear && fromGrid.get(x-1, y) == mgRoom && fromGrid.get(x, y-1) == mgRoom && fromGrid.get(x-1, y-1) == mgRoom:
			x0--
			y0--
			typ = mgRoom2x2
		case roomGrid.get(x+1, y) == mgClear && fromGrid.get(x+1, y) == mgRoom:
			x1++
			typ = mgRoom1x2
		case roomGrid.get(x, y+1) == mgClear && fromGrid.get(x, y+1) == mgRoom:
			y1++
			typ = mgRoom1x2
		case roomGrid.get(x-1, y) == mgClear && fromGrid.get(x-1, y) == mgRoom:
			x0--
			typ = mgRoom1x2
		case roomGrid.get(x, y-1) == mgClear && fromGrid.get(x, y-1) == mgRoom:
			y0--
			typ = mgRoom1x2
		}
		doorX := x0
		if m.rng.NextBoolean() {
			doorX = x1
		}
		doorY := y0
		if m.rng.NextBoolean() {
			doorY = y1
		}
		doorFlag := mgRoomDoorFlag
		if !fromGrid.edgesTo(doorX, doorY, mgCorridor) {
			if doorX == x0 {
				doorX = x1
			} else {
				doorX = x0
			}
			if doorY == y0 {
				doorY = y1
			} else {
				doorY = y0
			}
			if !fromGrid.edgesTo(doorX, doorY, mgCorridor) {
				if doorY == y0 {
					doorY = y1
				} else {
					doorY = y0
				}
				if !fromGrid.edgesTo(doorX, doorY, mgCorridor) {
					if doorX == x0 {
						doorX = x1
					} else {
						doorX = x0
					}
					if doorY == y0 {
						doorY = y1
					} else {
						doorY = y0
					}
					if !fromGrid.edgesTo(doorX, doorY, mgCorridor) {
						doorFlag = 0
						doorX = x0
						doorY = y0
					}
				}
			}
		}
		for ry := y0; ry <= y1; ry++ {
			for rx := x0; rx <= x1; rx++ {
				if rx == doorX && ry == doorY {
					roomGrid.set(rx, ry, mgRoomOriginFlag|doorFlag|typ|roomId)
					continue
				}
				roomGrid.set(rx, ry, typ|roomId)
			}
		}
		roomId++
	}
}

// woodlandMansionPiece ports WoodlandMansionPieces$WoodlandMansionPiece (a TemplateStructurePiece):
// a "woodland_mansion/<name>" .nbt template placed at a position with a rotation + mirror. Modeled
// on EndCityPiece; makeSettings = ignoreEntities + BlockIgnoreProcessor(STRUCTURE_BLOCK) + rot + mir.
type woodlandMansionPiece struct {
	name     string
	tmpl     *StructureTemplate
	pos      Pos
	rotation Rotation
	mirror   Mirror
	bbox     BoundingBox
	genDepth int
}

func (p *woodlandMansionPiece) BoundingBox() BoundingBox { return p.bbox }
func (p *woodlandMansionPiece) GenDepth() int            { return p.genDepth }

// newWoodlandMansionPiece loads the template + computes the world bbox at (pos, rot, mir). The
// StructurePlaceSettings pivot is ZERO (the jar uses the default pivot for mansion pieces).
func newWoodlandMansionPiece(name string, pos Pos, rot Rotation, mir Mirror) (*woodlandMansionPiece, error) {
	tmpl, err := LoadTemplate("woodland_mansion/" + name)
	if err != nil {
		return nil, fmt.Errorf("structure: woodland_mansion template %q: %w", name, err)
	}
	p := &woodlandMansionPiece{name: name, tmpl: tmpl, pos: pos, rotation: rot, mirror: mir}
	p.bbox = tmpl.BoundingBoxAt(pos, rot, mir, 0, 0)
	return p, nil
}

// PostProcess ports WoodlandMansionPiece.postProcess (TemplateStructurePiece.postProcess): place
// the template (clipped to the chunk box) then dispatch the data markers. Loot/mob markers are
// recorded via the shared chest/spawn seams (loot + mob finalize deferred, cited in the file head).
func (p *woodlandMansionPiece) PostProcess(view WorldGenView, box BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource) {
	p.tmpl.PlaceInWorld(view, p.pos, p.rotation, p.mirror, 0, 0, nil, box, rng)
	markers, err := p.tmpl.DataMarkers(p.pos, p.rotation, p.mirror, 0, 0)
	if err != nil {
		return
	}
	for _, mk := range markers {
		p.handleDataMarker(view, box, rng, mk)
	}
}

// handleDataMarker ports WoodlandMansionPiece.handleDataMarker: "Chest*" -> the mansion loot chest
// (block already placed by the template; tag it + set the FACING by the marker suffix); "Mage"/
// "Warrior"/"Group of Allays" -> record the evoker/vindicator/allay spawns. The allay COUNT draws
// level.getRandom().nextInt(3)+1 -- but that is the PER-FEATURE level random at postProcess, NOT
// the grid/placer RNG, so it does not desync the layout. Loot rolling + mob finalizeSpawn are the
// deferred subsystem; the positions + chest block + loot-table tag are recorded here.
func (p *woodlandMansionPiece) handleDataMarker(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, mk DataMarker) {
	id := mk.Metadata
	if len(id) >= 5 && id[:5] == "Chest" {
		if box.IsInside(mk.WorldPos.X, mk.WorldPos.Y, mk.WorldPos.Z) {
			// LootTableSeed = random.nextLong() (drawn only when placing the chest BE). The mansion
			// loot table is rolled lazily on open.
			seed := rng.NextLong()
			view.SetBlockEntity(mk.WorldPos.X, mk.WorldPos.Y, mk.WorldPos.Z, block.EntityTypes["minecraft:chest"], woodlandMansionLootTable, seed)
		}
		return
	}
	switch id {
	case "Mage":
		p.recordMansionMob(view, box, mk, "minecraft:evoker")
	case "Warrior":
		p.recordMansionMob(view, box, mk, "minecraft:vindicator")
	case "Group of Allays":
		if box.IsInside(mk.WorldPos.X, mk.WorldPos.Y, mk.WorldPos.Z) {
			n := int(rng.NextIntN(3)) + 1
			for i := 0; i < n; i++ {
				p.recordMansionMob(view, box, mk, "minecraft:allay")
			}
		}
	}
}

func (p *woodlandMansionPiece) recordMansionMob(view WorldGenView, box BoundingBox, mk DataMarker, entityType string) {
	if !box.IsInside(mk.WorldPos.X, mk.WorldPos.Y, mk.WorldPos.Z) {
		return
	}
	view.RecordSpawn(SpawnRequest{
		EntityType:          entityType,
		X:                   float64(mk.WorldPos.X) + 0.5,
		Y:                   float64(mk.WorldPos.Y),
		Z:                   float64(mk.WorldPos.Z) + 0.5,
		PersistenceRequired: true,
	})
}

// woodlandMansionLootTable is BuiltInLootTables.WOODLAND_MANSION (loot rolled lazily/deferred).
const woodlandMansionLootTable = "minecraft:chests/woodland_mansion"

// floorRoomCollection ports WoodlandMansionPieces$FloorRoomCollection: the per-floor room-template
// name generators. Each get* draws nextInt to pick a variant. First floor uses the "a" set; second
// + third floors share the "b" set (ThirdFloorRoomCollection extends SecondFloorRoomCollection).
type floorRoomCollection interface {
	get1x1(rng levelgen.RandomSource) string
	get1x1Secret(rng levelgen.RandomSource) string
	get1x2SideEntrance(rng levelgen.RandomSource, isStairs bool) string
	get1x2FrontEntrance(rng levelgen.RandomSource, isStairs bool) string
	get1x2Secret(rng levelgen.RandomSource) string
	get2x2(rng levelgen.RandomSource) string
	get2x2Secret(rng levelgen.RandomSource) string
}

// firstFloorRoomCollection ports FirstFloorRoomCollection (the "a" set).
type firstFloorRoomCollection struct{}

func (firstFloorRoomCollection) get1x1(rng levelgen.RandomSource) string {
	return fmt.Sprintf("1x1_a%d", rng.NextIntN(5)+1)
}
func (firstFloorRoomCollection) get1x1Secret(rng levelgen.RandomSource) string {
	return fmt.Sprintf("1x1_as%d", rng.NextIntN(4)+1)
}
func (firstFloorRoomCollection) get1x2SideEntrance(rng levelgen.RandomSource, _ bool) string {
	return fmt.Sprintf("1x2_a%d", rng.NextIntN(9)+1)
}
func (firstFloorRoomCollection) get1x2FrontEntrance(rng levelgen.RandomSource, _ bool) string {
	return fmt.Sprintf("1x2_b%d", rng.NextIntN(5)+1)
}
func (firstFloorRoomCollection) get1x2Secret(rng levelgen.RandomSource) string {
	return fmt.Sprintf("1x2_s%d", rng.NextIntN(2)+1)
}
func (firstFloorRoomCollection) get2x2(rng levelgen.RandomSource) string {
	return fmt.Sprintf("2x2_a%d", rng.NextIntN(4)+1)
}
func (firstFloorRoomCollection) get2x2Secret(_ levelgen.RandomSource) string { return "2x2_s1" }

// secondFloorRoomCollection ports SecondFloorRoomCollection (the "b" set). ThirdFloor reuses it.
type secondFloorRoomCollection struct{}

func (secondFloorRoomCollection) get1x1(rng levelgen.RandomSource) string {
	return fmt.Sprintf("1x1_b%d", rng.NextIntN(5)+1)
}
func (secondFloorRoomCollection) get1x1Secret(rng levelgen.RandomSource) string {
	return fmt.Sprintf("1x1_as%d", rng.NextIntN(4)+1)
}
func (secondFloorRoomCollection) get1x2SideEntrance(rng levelgen.RandomSource, isStairs bool) string {
	if isStairs {
		return "1x2_c_stairs"
	}
	return fmt.Sprintf("1x2_c%d", rng.NextIntN(4)+1)
}
func (secondFloorRoomCollection) get1x2FrontEntrance(rng levelgen.RandomSource, isStairs bool) string {
	if isStairs {
		return "1x2_d_stairs"
	}
	return fmt.Sprintf("1x2_d%d", rng.NextIntN(5)+1)
}
func (secondFloorRoomCollection) get1x2Secret(rng levelgen.RandomSource) string {
	return fmt.Sprintf("1x2_se%d", rng.NextIntN(1)+1)
}
func (secondFloorRoomCollection) get2x2(rng levelgen.RandomSource) string {
	return fmt.Sprintf("2x2_b%d", rng.NextIntN(5)+1)
}
func (secondFloorRoomCollection) get2x2Secret(_ levelgen.RandomSource) string { return "2x2_s1" }

// thirdFloorRoomCollection ports ThirdFloorRoomCollection (extends SecondFloor with no overrides).
type thirdFloorRoomCollection struct{ secondFloorRoomCollection }

// --- position helpers (BlockPos.relative / above / offset + Direction step incl. vertical) ---

// mansionStepX/Y/Z port Direction.getStepX/Y/Z for the FULL 6-direction compass (the placer uses
// vertical steps via above()/relative(UP) too). stepX/stepZ (desert_pyramid.go) cover only the
// horizontal compass, so the mansion needs the Y-aware variants.
func mansionStepX(d block.Direction) int {
	switch d {
	case block.West:
		return -1
	case block.East:
		return 1
	}
	return 0
}

func mansionStepY(d block.Direction) int {
	switch d {
	case block.Down:
		return -1
	case block.Up:
		return 1
	}
	return 0
}

func mansionStepZ(d block.Direction) int {
	switch d {
	case block.North:
		return -1
	case block.South:
		return 1
	}
	return 0
}

// relativePos ports BlockPos.relative(Direction, n): pos + dir.step * n.
func relativePos(p Pos, d block.Direction, n int) Pos {
	return Pos{p.X + mansionStepX(d)*n, p.Y + mansionStepY(d)*n, p.Z + mansionStepZ(d)*n}
}

// abovePos ports BlockPos.above(n).
func abovePos(p Pos, n int) Pos { return Pos{p.X, p.Y + n, p.Z} }

// offsetPos ports BlockPos.offset(dx,dy,dz).
func offsetPos(p Pos, dx, dy, dz int) Pos { return Pos{p.X + dx, p.Y + dy, p.Z + dz} }

// rotatePos ports BlockPos.rotate(Rotation): rotate a point about the origin. CFR BlockPos.rotate:
// CLOCKWISE_90 -> (-z, y, x); CLOCKWISE_180 -> (-x, y, -z); COUNTERCLOCKWISE_90 -> (z, y, -x).
func rotatePos(p Pos, rot Rotation) Pos {
	switch rot {
	case RotClockwise90:
		return Pos{-p.Z, p.Y, p.X}
	case RotClockwise180:
		return Pos{-p.X, p.Y, -p.Z}
	case RotCounterclockwise90:
		return Pos{p.Z, p.Y, -p.X}
	default:
		return p
	}
}

// zeroPositionWithTransformPoint ports the 5-arg static StructureTemplate.getZeroPositionWithTransform
// (BlockPos, Mirror, Rotation, int sizeX, int sizeZ): the arg point offset by the rot/mirror anchor
// shift, with sizeX/sizeZ decremented by 1 (the iinc 3,-1 / 4,-1). Used by addRoom1x1.
func zeroPositionWithTransformPoint(p Pos, mir Mirror, rot Rotation, sizeX, sizeZ int) Pos {
	sizeX--
	sizeZ--
	i5 := 0
	if mir == MirrorFrontBack {
		i5 = sizeX
	}
	i6 := 0
	if mir == MirrorLeftRight {
		i6 = sizeZ
	}
	switch rot {
	case RotNone:
		return offsetPos(p, i5, 0, i6)
	case RotClockwise90:
		return offsetPos(p, sizeZ-i6, 0, i5)
	case RotClockwise180:
		return offsetPos(p, sizeX-i5, 0, sizeZ-i6)
	case RotCounterclockwise90:
		return offsetPos(p, i6, 0, sizeX-i5)
	default:
		return offsetPos(p, i5, 0, i6)
	}
}

// mansionPlacementData ports MansionPiecePlacer$PlacementData: the walking cursor (position +
// rotation + wall type) for the outer-wall traversal.
type mansionPlacementData struct {
	rotation Rotation
	position Pos
	wallType string
}

// mansionPiecePlacer ports MansionPiecePlacer: it walks the grid + emits woodlandMansionPiece(s).
// A missing template latches err (aborts the whole start, matching a build-data failure).
type mansionPiecePlacer struct {
	rng    levelgen.RandomSource
	startX int
	startY int
	pieces []Piece
	err    error
}

// add appends a piece (loading its template); a load error is latched.
func (pl *mansionPiecePlacer) add(name string, pos Pos, rot Rotation, mir Mirror) {
	if pl.err != nil {
		return
	}
	p, err := newWoodlandMansionPiece(name, pos, rot, mir)
	if err != nil {
		pl.err = err
		return
	}
	pl.pieces = append(pl.pieces, p)
}

// createMansion ports MansionPiecePlacer.createMansion: place the entrance, the two lower outer-wall
// rings + the third-floor ring, the two roofs, then per-floor the corridors + rooms.
func (pl *mansionPiecePlacer) createMansion(origin Pos, rotation Rotation, mansion *mansionGrid) {
	data := &mansionPlacementData{position: origin, rotation: rotation, wallType: "wall_flat"}
	secondData := &mansionPlacementData{}
	pl.entrance(data)
	secondData.position = abovePos(data.position, 8)
	secondData.rotation = data.rotation
	secondData.wallType = "wall_window"

	baseGrid := mansion.baseGrid
	thirdGrid := mansion.thirdFloorGrid
	pl.startX = mansion.entranceX + 1
	pl.startY = mansion.entranceY + 1
	endX := mansion.entranceX + 1
	endY := mansion.entranceY
	pl.traverseOuterWalls(data, baseGrid, block.South, pl.startX, pl.startY, endX, endY)
	pl.traverseOuterWalls(secondData, baseGrid, block.South, pl.startX, pl.startY, endX, endY)

	thirdData := &mansionPlacementData{position: abovePos(data.position, 19), rotation: data.rotation, wallType: "wall_window"}
	done := false
	for y := 0; y < thirdGrid.height && !done; y++ {
		for x := thirdGrid.width - 1; x >= 0 && !done; x-- {
			if !mansionIsHouse(thirdGrid, x, y) {
				continue
			}
			thirdData.position = relativePos(thirdData.position, rotation.rotateDirection(block.South), 8+(y-pl.startY)*8)
			thirdData.position = relativePos(thirdData.position, rotation.rotateDirection(block.East), (x-pl.startX)*8)
			pl.traverseWallPiece(thirdData)
			pl.traverseOuterWalls(thirdData, thirdGrid, block.South, x, y, x, y)
			done = true
		}
	}

	pl.createRoof(abovePos(origin, 16), rotation, baseGrid, thirdGrid)
	pl.createRoof(abovePos(origin, 27), rotation, thirdGrid, nil)

	roomCollections := [3]floorRoomCollection{firstFloorRoomCollection{}, secondFloorRoomCollection{}, thirdFloorRoomCollection{}}
	for floorNum := 0; floorNum < 3; floorNum++ {
		extra := 0
		if floorNum == 2 {
			extra = 3
		}
		floorOrigin := abovePos(origin, 8*floorNum+extra)
		rooms := mansion.floorRooms[floorNum]
		grid := baseGrid
		if floorNum == 2 {
			grid = thirdGrid
		}
		southPiece := "carpet_south_2"
		westPiece := "carpet_west_2"
		if floorNum == 0 {
			southPiece = "carpet_south_1"
			westPiece = "carpet_west_1"
		}
		pl.placeCorridors(floorOrigin, rotation, grid, rooms, southPiece, westPiece)
		pl.placeRooms(floorNum, floorOrigin, rotation, mansion, grid, rooms, roomCollections[floorNum])
	}
}

func (pl *mansionPiecePlacer) entrance(data *mansionPlacementData) {
	west := data.rotation.rotateDirection(block.West)
	pl.add("entrance", relativePos(data.position, west, 9), data.rotation, MirrorNone)
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.South), 16)
}

func (pl *mansionPiecePlacer) traverseWallPiece(data *mansionPlacementData) {
	pl.add(data.wallType, relativePos(data.position, data.rotation.rotateDirection(block.East), 7), data.rotation, MirrorNone)
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.South), 8)
}

func (pl *mansionPiecePlacer) traverseTurn(data *mansionPlacementData) {
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.South), -1)
	pl.add("wall_corner", data.position, data.rotation, MirrorNone)
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.South), -7)
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.West), -6)
	data.rotation = endCityGetRotated(data.rotation, RotClockwise90)
}

func (pl *mansionPiecePlacer) traverseInnerTurn(data *mansionPlacementData) {
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.South), 6)
	data.position = relativePos(data.position, data.rotation.rotateDirection(block.East), 8)
	data.rotation = endCityGetRotated(data.rotation, RotCounterclockwise90)
}

// traverseOuterWalls ports MansionPiecePlacer.traverseOuterWalls: walk the perimeter of a grid
// region, emitting wall pieces + corner/inner-corner turns until the cursor returns to the start.
func (pl *mansionPiecePlacer) traverseOuterWalls(data *mansionPlacementData, grid *simpleGrid, gridDirection block.Direction, startX, startY, endX, endY int) {
	gridX := startX
	gridY := startY
	startDirection := gridDirection
	for {
		if !mansionIsHouse(grid, gridX+stepX(gridDirection), gridY+stepZ(gridDirection)) {
			pl.traverseTurn(data)
			gridDirection = directionClockWise(gridDirection)
			if !(gridX == endX && gridY == endY && startDirection == gridDirection) {
				pl.traverseWallPiece(data)
			}
		} else if mansionIsHouse(grid, gridX+stepX(gridDirection), gridY+stepZ(gridDirection)) &&
			mansionIsHouse(grid, gridX+stepX(gridDirection)+stepX(directionCounterClockWise(gridDirection)), gridY+stepZ(gridDirection)+stepZ(directionCounterClockWise(gridDirection))) {
			pl.traverseInnerTurn(data)
			gridX += stepX(gridDirection)
			gridY += stepZ(gridDirection)
			gridDirection = directionCounterClockWise(gridDirection)
		} else {
			gridX += stepX(gridDirection)
			gridY += stepZ(gridDirection)
			if !(gridX == endX && gridY == endY && startDirection == gridDirection) {
				pl.traverseWallPiece(data)
			}
		}
		if gridX == endX && gridY == endY && startDirection == gridDirection {
			break
		}
	}
}

// createRoof ports MansionPiecePlacer.createRoof: emit roof/roof_front/roof_corner/roof_inner_corner
// over grid cells (skipping cells covered by aboveGrid), plus small_wall pieces where the floor
// below rises to an above-floor. NO RNG draws here.
func (pl *mansionPiecePlacer) createRoof(roofOrigin Pos, rotation Rotation, grid, aboveGrid *simpleGrid) {
	rr := func(d block.Direction) block.Direction { return rotation.rotateDirection(d) }
	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			position := roofOrigin
			position = relativePos(position, rr(block.South), 8+(y-pl.startY)*8)
			position = relativePos(position, rr(block.East), (x-pl.startX)*8)
			isAbove := aboveGrid != nil && mansionIsHouse(aboveGrid, x, y)
			if !mansionIsHouse(grid, x, y) || isAbove {
				continue
			}
			pl.add("roof", abovePos(position, 3), rotation, MirrorNone)
			if !mansionIsHouse(grid, x+1, y) {
				p2 := relativePos(position, rr(block.East), 6)
				pl.add("roof_front", p2, rotation, MirrorNone)
			}
			if !mansionIsHouse(grid, x-1, y) {
				p2 := relativePos(position, rr(block.East), 0)
				p2 = relativePos(p2, rr(block.South), 7)
				pl.add("roof_front", p2, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
			}
			if !mansionIsHouse(grid, x, y-1) {
				p2 := relativePos(position, rr(block.West), 1)
				pl.add("roof_front", p2, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
			}
			if !mansionIsHouse(grid, x, y+1) {
				p2 := relativePos(position, rr(block.East), 6)
				p2 = relativePos(p2, rr(block.South), 6)
				pl.add("roof_front", p2, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
			}
		}
	}
	if aboveGrid != nil {
		for y := 0; y < grid.height; y++ {
			for x := 0; x < grid.width; x++ {
				position := roofOrigin
				position = relativePos(position, rr(block.South), 8+(y-pl.startY)*8)
				position = relativePos(position, rr(block.East), (x-pl.startX)*8)
				isAbove := mansionIsHouse(aboveGrid, x, y)
				if !mansionIsHouse(grid, x, y) || !isAbove {
					continue
				}
				if !mansionIsHouse(grid, x+1, y) {
					p2 := relativePos(position, rr(block.East), 7)
					pl.add("small_wall", p2, rotation, MirrorNone)
				}
				if !mansionIsHouse(grid, x-1, y) {
					p2 := relativePos(position, rr(block.West), 1)
					p2 = relativePos(p2, rr(block.South), 6)
					pl.add("small_wall", p2, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
				}
				if !mansionIsHouse(grid, x, y-1) {
					p2 := relativePos(position, rr(block.West), 0)
					p2 = relativePos(p2, rr(block.North), 1)
					pl.add("small_wall", p2, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
				}
				if !mansionIsHouse(grid, x, y+1) {
					p2 := relativePos(position, rr(block.East), 6)
					p2 = relativePos(p2, rr(block.South), 7)
					pl.add("small_wall", p2, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
				}
				if !mansionIsHouse(grid, x+1, y) {
					if !mansionIsHouse(grid, x, y-1) {
						p2 := relativePos(position, rr(block.East), 7)
						p2 = relativePos(p2, rr(block.North), 2)
						pl.add("small_wall_corner", p2, rotation, MirrorNone)
					}
					if !mansionIsHouse(grid, x, y+1) {
						p2 := relativePos(position, rr(block.East), 8)
						p2 = relativePos(p2, rr(block.South), 7)
						pl.add("small_wall_corner", p2, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
					}
				}
				if mansionIsHouse(grid, x-1, y) {
					continue
				}
				if !mansionIsHouse(grid, x, y-1) {
					p2 := relativePos(position, rr(block.West), 2)
					p2 = relativePos(p2, rr(block.North), 1)
					pl.add("small_wall_corner", p2, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
				}
				if mansionIsHouse(grid, x, y+1) {
					continue
				}
				p2 := relativePos(position, rr(block.West), 1)
				p2 = relativePos(p2, rr(block.South), 8)
				pl.add("small_wall_corner", p2, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
			}
		}
	}
	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			position := roofOrigin
			position = relativePos(position, rr(block.South), 8+(y-pl.startY)*8)
			position = relativePos(position, rr(block.East), (x-pl.startX)*8)
			isAbove := aboveGrid != nil && mansionIsHouse(aboveGrid, x, y)
			if !mansionIsHouse(grid, x, y) || isAbove {
				continue
			}
			if !mansionIsHouse(grid, x+1, y) {
				p2 := relativePos(position, rr(block.East), 6)
				if !mansionIsHouse(grid, x, y+1) {
					p3 := relativePos(p2, rr(block.South), 6)
					pl.add("roof_corner", p3, rotation, MirrorNone)
				} else if mansionIsHouse(grid, x+1, y+1) {
					p3 := relativePos(p2, rr(block.South), 5)
					pl.add("roof_inner_corner", p3, rotation, MirrorNone)
				}
				if !mansionIsHouse(grid, x, y-1) {
					pl.add("roof_corner", p2, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
				} else if mansionIsHouse(grid, x+1, y-1) {
					p3 := relativePos(position, rr(block.East), 9)
					p3 = relativePos(p3, rr(block.North), 2)
					pl.add("roof_inner_corner", p3, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
				}
			}
			if mansionIsHouse(grid, x-1, y) {
				continue
			}
			p2 := relativePos(position, rr(block.East), 0)
			p2 = relativePos(p2, rr(block.South), 0)
			if !mansionIsHouse(grid, x, y+1) {
				p3 := relativePos(p2, rr(block.South), 6)
				pl.add("roof_corner", p3, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
			} else if mansionIsHouse(grid, x-1, y+1) {
				p3 := relativePos(p2, rr(block.South), 8)
				p3 = relativePos(p3, rr(block.West), 3)
				pl.add("roof_inner_corner", p3, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
			}
			if !mansionIsHouse(grid, x, y-1) {
				pl.add("roof_corner", p2, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
				continue
			}
			if !mansionIsHouse(grid, x-1, y-1) {
				continue
			}
			p3 := relativePos(p2, rr(block.South), 1)
			pl.add("roof_inner_corner", p3, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
		}
	}
}

// placeCorridors ports the corridor-carpet loop of createMansion: for each corridor cell, place the
// corridor_floor + the north/east/south/west carpet edges toward adjacent corridors/corridor-rooms.
func (pl *mansionPiecePlacer) placeCorridors(floorOrigin Pos, rotation Rotation, grid, rooms *simpleGrid, southPiece, westPiece string) {
	rr := func(d block.Direction) block.Direction { return rotation.rotateDirection(d) }
	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			if grid.get(x, y) != mgCorridor {
				continue
			}
			pos := relativePos(floorOrigin, rr(block.South), 8+(y-pl.startY)*8)
			pos = relativePos(pos, rr(block.East), (x-pl.startX)*8)
			pl.add("corridor_floor", pos, rotation, MirrorNone)
			if grid.get(x, y-1) == mgCorridor || (rooms.get(x, y-1)&mgRoomCorridorFlag) == mgRoomCorridorFlag {
				pl.add("carpet_north", abovePos(relativePos(pos, rr(block.East), 1), 1), rotation, MirrorNone)
			}
			if grid.get(x+1, y) == mgCorridor || (rooms.get(x+1, y)&mgRoomCorridorFlag) == mgRoomCorridorFlag {
				p := relativePos(pos, rr(block.South), 1)
				p = relativePos(p, rr(block.East), 5)
				pl.add("carpet_east", abovePos(p, 1), rotation, MirrorNone)
			}
			if grid.get(x, y+1) == mgCorridor || (rooms.get(x, y+1)&mgRoomCorridorFlag) == mgRoomCorridorFlag {
				p := relativePos(pos, rr(block.South), 5)
				p = relativePos(p, rr(block.West), 1)
				pl.add(southPiece, p, rotation, MirrorNone)
			}
			if grid.get(x-1, y) == mgCorridor || (rooms.get(x-1, y)&mgRoomCorridorFlag) == mgRoomCorridorFlag {
				p := relativePos(pos, rr(block.West), 1)
				p = relativePos(p, rr(block.North), 1)
				pl.add(westPiece, p, rotation, MirrorNone)
			}
		}
	}
}

// placeRooms ports the room loop of createMansion: for each room-origin cell, emit the indoor
// walls/doors around it (drawing a door-direction), then dispatch to addRoom* by room type.
func (pl *mansionPiecePlacer) placeRooms(floorNum int, floorOrigin Pos, rotation Rotation, mansion *mansionGrid, grid, rooms *simpleGrid, roomCol floorRoomCollection) {
	rr := func(d block.Direction) block.Direction { return rotation.rotateDirection(d) }
	wallPiece := "indoors_wall_2"
	doorPiece := "indoors_door_2"
	if floorNum == 0 {
		wallPiece = "indoors_wall_1"
		doorPiece = "indoors_door_1"
	}
	var doorDirs []block.Direction
	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			thirdFloorStartRoom := floorNum == 2 && grid.get(x, y) == mgStartRoom
			if grid.get(x, y) != mgRoom && !thirdFloorStartRoom {
				continue
			}
			roomData := rooms.get(x, y)
			roomType := roomData & 0xF0000
			roomId := roomData & mgRoomIDMask
			thirdFloorStartRoom = thirdFloorStartRoom && (roomData&mgRoomCorridorFlag) == mgRoomCorridorFlag
			doorDirs = doorDirs[:0]
			if (roomData & mgRoomDoorFlag) == mgRoomDoorFlag {
				for _, dir := range horizontalPlane {
					if grid.get(x+stepX(dir), y+stepZ(dir)) != mgCorridor {
						continue
					}
					doorDirs = append(doorDirs, dir)
				}
			}
			var doorDir block.Direction
			hasDoorDir := false
			if len(doorDirs) != 0 {
				doorDir = doorDirs[pl.randInt(len(doorDirs))]
				hasDoorDir = true
			} else if (roomData & mgRoomOriginFlag) == mgRoomOriginFlag {
				doorDir = block.Up
				hasDoorDir = true
			}
			roomPos := relativePos(floorOrigin, rr(block.South), 8+(y-pl.startY)*8)
			roomPos = relativePos(roomPos, rr(block.East), -1+(x-pl.startX)*8)
			if mansionIsHouse(grid, x-1, y) && !mansion.isRoomId(grid, x-1, y, floorNum, roomId) {
				name := wallPiece
				if hasDoorDir && doorDir == block.West {
					name = doorPiece
				}
				pl.add(name, roomPos, rotation, MirrorNone)
			}
			if grid.get(x+1, y) == mgCorridor && !thirdFloorStartRoom {
				pos := relativePos(roomPos, rr(block.East), 8)
				name := wallPiece
				if hasDoorDir && doorDir == block.East {
					name = doorPiece
				}
				pl.add(name, pos, rotation, MirrorNone)
			}
			if mansionIsHouse(grid, x, y+1) && !mansion.isRoomId(grid, x, y+1, floorNum, roomId) {
				pos := relativePos(roomPos, rr(block.South), 7)
				pos = relativePos(pos, rr(block.East), 7)
				name := wallPiece
				if hasDoorDir && doorDir == block.South {
					name = doorPiece
				}
				pl.add(name, pos, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
			}
			if grid.get(x, y-1) == mgCorridor && !thirdFloorStartRoom {
				pos := relativePos(roomPos, rr(block.North), 1)
				pos = relativePos(pos, rr(block.East), 7)
				name := wallPiece
				if hasDoorDir && doorDir == block.North {
					name = doorPiece
				}
				pl.add(name, pos, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
			}
			switch {
			case roomType == mgRoom1x1:
				pl.addRoom1x1(roomPos, rotation, doorDir, roomCol)
			case roomType == mgRoom1x2 && hasDoorDir:
				roomDir, _ := mansion.get1x2RoomDirection(grid, x, y, floorNum, roomId)
				isStairs := (roomData & mgRoomStairsFlag) == mgRoomStairsFlag
				pl.addRoom1x2(roomPos, rotation, roomDir, doorDir, roomCol, isStairs)
			case roomType == mgRoom2x2 && hasDoorDir && doorDir != block.Up:
				roomDir := directionClockWise(doorDir)
				if !mansion.isRoomId(grid, x+stepX(roomDir), y+stepZ(roomDir), floorNum, roomId) {
					roomDir = directionOpposite(roomDir)
				}
				pl.addRoom2x2(roomPos, rotation, roomDir, doorDir, roomCol)
			case roomType == mgRoom2x2 && hasDoorDir && doorDir == block.Up:
				pl.addRoom2x2Secret(roomPos, rotation, roomCol)
			}
		}
	}
}

// randInt draws nextInt via the placer's rng (set by createMansion's caller through addRoom paths).
// The placer holds no rng field; the room draws use the mansion grid rng, threaded via a closure in
// the StartGen. To keep it simple + faithful, the placer carries the rng.
func (pl *mansionPiecePlacer) randInt(bound int) int { return int(pl.rng.NextIntN(int32(bound))) }

// addRoom1x1 ports MansionPiecePlacer.addRoom1x1: pick the room template (drawing the collection's
// nextInt), derive the piece rotation from the door direction, and place at the zero-transformed pos.
func (pl *mansionPiecePlacer) addRoom1x1(roomPos Pos, rotation Rotation, doorDir block.Direction, rooms floorRoomCollection) {
	pieceRot := RotNone
	roomType := rooms.get1x1(pl.rng)
	if doorDir != block.East {
		switch doorDir {
		case block.North:
			pieceRot = endCityGetRotated(pieceRot, RotCounterclockwise90)
		case block.West:
			pieceRot = endCityGetRotated(pieceRot, RotClockwise180)
		case block.South:
			pieceRot = endCityGetRotated(pieceRot, RotClockwise90)
		default:
			roomType = rooms.get1x1Secret(pl.rng)
		}
	}
	orientation := zeroPositionWithTransformPoint(Pos{1, 0, 0}, MirrorNone, pieceRot, 7, 7)
	pieceRot = endCityGetRotated(pieceRot, rotation)
	orientation = rotatePos(orientation, rotation)
	pos := offsetPos(roomPos, orientation.X, 0, orientation.Z)
	pl.add(roomType, pos, pieceRot, MirrorNone)
}

// addRoom1x2 ports MansionPiecePlacer.addRoom1x2: a big door-dir/room-dir switch selecting the side-
// or front-entrance template (drawing its nextInt) + the transformed position/rotation/mirror.
func (pl *mansionPiecePlacer) addRoom1x2(roomPos Pos, rotation Rotation, roomDir, doorDir block.Direction, rooms floorRoomCollection, isStairs bool) {
	rr := func(d block.Direction) block.Direction { return rotation.rotateDirection(d) }
	side := func() string { return rooms.get1x2SideEntrance(pl.rng, isStairs) }
	front := func() string { return rooms.get1x2FrontEntrance(pl.rng, isStairs) }
	secret := func() string { return rooms.get1x2Secret(pl.rng) }
	switch {
	case doorDir == block.East && roomDir == block.South:
		pos := relativePos(roomPos, rr(block.East), 1)
		pl.add(side(), pos, rotation, MirrorNone)
	case doorDir == block.East && roomDir == block.North:
		pos := relativePos(roomPos, rr(block.East), 1)
		pos = relativePos(pos, rr(block.South), 6)
		pl.add(side(), pos, rotation, MirrorLeftRight)
	case doorDir == block.West && roomDir == block.North:
		pos := relativePos(roomPos, rr(block.East), 7)
		pos = relativePos(pos, rr(block.South), 6)
		pl.add(side(), pos, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
	case doorDir == block.West && roomDir == block.South:
		pos := relativePos(roomPos, rr(block.East), 7)
		pl.add(side(), pos, rotation, MirrorFrontBack)
	case doorDir == block.South && roomDir == block.East:
		pos := relativePos(roomPos, rr(block.East), 1)
		pl.add(side(), pos, endCityGetRotated(rotation, RotClockwise90), MirrorLeftRight)
	case doorDir == block.South && roomDir == block.West:
		pos := relativePos(roomPos, rr(block.East), 7)
		pl.add(side(), pos, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
	case doorDir == block.North && roomDir == block.West:
		pos := relativePos(roomPos, rr(block.East), 7)
		pos = relativePos(pos, rr(block.South), 6)
		pl.add(side(), pos, endCityGetRotated(rotation, RotClockwise90), MirrorFrontBack)
	case doorDir == block.North && roomDir == block.East:
		pos := relativePos(roomPos, rr(block.East), 1)
		pos = relativePos(pos, rr(block.South), 6)
		pl.add(side(), pos, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
	case doorDir == block.South && roomDir == block.North:
		pos := relativePos(roomPos, rr(block.East), 1)
		pos = relativePos(pos, rr(block.North), 8)
		pl.add(front(), pos, rotation, MirrorNone)
	case doorDir == block.North && roomDir == block.South:
		pos := relativePos(roomPos, rr(block.East), 7)
		pos = relativePos(pos, rr(block.South), 14)
		pl.add(front(), pos, endCityGetRotated(rotation, RotClockwise180), MirrorNone)
	case doorDir == block.West && roomDir == block.East:
		pos := relativePos(roomPos, rr(block.East), 15)
		pl.add(front(), pos, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
	case doorDir == block.East && roomDir == block.West:
		pos := relativePos(roomPos, rr(block.West), 7)
		pos = relativePos(pos, rr(block.South), 6)
		pl.add(front(), pos, endCityGetRotated(rotation, RotCounterclockwise90), MirrorNone)
	case doorDir == block.Up && roomDir == block.East:
		pos := relativePos(roomPos, rr(block.East), 15)
		pl.add(secret(), pos, endCityGetRotated(rotation, RotClockwise90), MirrorNone)
	case doorDir == block.Up && roomDir == block.South:
		pos := relativePos(roomPos, rr(block.East), 1)
		pos = relativePos(pos, rr(block.North), 0)
		pl.add(secret(), pos, rotation, MirrorNone)
	}
}

// addRoom2x2 ports MansionPiecePlacer.addRoom2x2: door-dir/room-dir -> (east,south) offset + rot +
// mirror, then place the get2x2 template (drawing its nextInt).
func (pl *mansionPiecePlacer) addRoom2x2(roomPos Pos, rotation Rotation, roomDir, doorDir block.Direction, rooms floorRoomCollection) {
	east := 0
	south := 0
	rot := rotation
	mirror := MirrorNone
	switch {
	case doorDir == block.East && roomDir == block.South:
		east = -7
	case doorDir == block.East && roomDir == block.North:
		east = -7
		south = 6
		mirror = MirrorLeftRight
	case doorDir == block.North && roomDir == block.East:
		east = 1
		south = 14
		rot = endCityGetRotated(rotation, RotCounterclockwise90)
	case doorDir == block.North && roomDir == block.West:
		east = 7
		south = 14
		rot = endCityGetRotated(rotation, RotCounterclockwise90)
		mirror = MirrorLeftRight
	case doorDir == block.South && roomDir == block.West:
		east = 7
		south = -8
		rot = endCityGetRotated(rotation, RotClockwise90)
	case doorDir == block.South && roomDir == block.East:
		east = 1
		south = -8
		rot = endCityGetRotated(rotation, RotClockwise90)
		mirror = MirrorLeftRight
	case doorDir == block.West && roomDir == block.North:
		east = 15
		south = 6
		rot = endCityGetRotated(rotation, RotClockwise180)
	case doorDir == block.West && roomDir == block.South:
		east = 15
		mirror = MirrorFrontBack
	}
	pos := relativePos(roomPos, rotation.rotateDirection(block.East), east)
	pos = relativePos(pos, rotation.rotateDirection(block.South), south)
	pl.add(rooms.get2x2(pl.rng), pos, rot, mirror)
}

// addRoom2x2Secret ports MansionPiecePlacer.addRoom2x2Secret.
func (pl *mansionPiecePlacer) addRoom2x2Secret(roomPos Pos, rotation Rotation, rooms floorRoomCollection) {
	pos := relativePos(roomPos, rotation.rotateDirection(block.East), 1)
	pl.add(rooms.get2x2Secret(pl.rng), pos, rotation, MirrorNone)
}

// generateMansion ports WoodlandMansionPieces.generateMansion: build the grid + run the placer.
// Both share the SAME rng (the jar's single WorldgenRandom), so the grid draws happen first, then
// the placer's room draws -- an exact stream. Returns the piece list (or a build-data error).
func generateMansion(origin Pos, rotation Rotation, rng levelgen.RandomSource) ([]Piece, error) {
	grid := newMansionGrid(rng)
	placer := &mansionPiecePlacer{rng: rng}
	placer.createMansion(origin, rotation, grid)
	if placer.err != nil {
		return nil, placer.err
	}
	return placer.pieces, nil
}

// woodlandMansionStartGen is the woodland_mansion StartGenerator.
type woodlandMansionStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewWoodlandMansionStartGen builds the generator from the embedded woodland_mansions structure_set
// + has_structure/woodland_mansion tag, verifying the TRIANGULAR placement (salt 10387319 / spacing
// 80 / separation 20).
func NewWoodlandMansionStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet(woodlandMansionSet)
	if err != nil {
		return nil, err
	}
	if set.Placement.Salt != woodlandMansionSalt || set.Placement.Spacing != woodlandMansionSpacing || set.Placement.Separation != woodlandMansionSeparation {
		return nil, fmt.Errorf("structure: woodland_mansions placement = salt %d / spacing %d / separation %d; want %d / %d / %d",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation, woodlandMansionSalt, woodlandMansionSpacing, woodlandMansionSeparation)
	}
	if set.Placement.SpreadType != SpreadTriangular {
		return nil, fmt.Errorf("structure: woodland_mansions spread_type = %d; want triangular", set.Placement.SpreadType)
	}
	allow, err := HasStructureBiomes("woodland_mansion")
	if err != nil {
		return nil, fmt.Errorf("structure: woodland_mansion biome tag: %w", err)
	}
	return &woodlandMansionStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// GenerateStarts ports WoodlandMansionStructure.findGenerationPoint (+ createStructures gate). The
// biome gate uses findValidGenerationPoint (getBiome at the chunk-center at the surface Y).
func (g *woodlandMansionStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	// The biome gate at the chunk-center surface Y (findValidGenerationPoint runs BEFORE
	// findGenerationPoint's rng draws; a non-dark-forest origin yields no start with no rng spent).
	centerX := cx*16 + 8
	centerZ := cz*16 + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !g.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}

	// The piece rng (GenerationContext.makeRandom = SetLargeFeatureSeed(seed, cx, cz)).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// findGenerationPoint: rot = Rotation.getRandom(rng) (the FIRST draw), then the surface
	// projection getLowestYIn5by5BoxOffset7Blocks, then the Y<60 gate.
	rot := getRandomRotation(rng)
	blockX, blockY, blockZ := g.lowestYIn5by5(sampler, cx, cz, rot)
	if blockY < woodlandMansionMinY {
		return nil
	}

	origin := Pos{blockX, blockY, blockZ}
	pieces, err := generateMansion(origin, rot, rng)
	if err != nil {
		// A missing .nbt template is a build-data bug; surfaced by producing no start (the composite
		// treats a nil result as "no structure here"). The extractor embeds all 73 mansion templates.
		return nil
	}
	if len(pieces) == 0 {
		return nil
	}

	start := &StructureStart{Structure: woodlandMansionID, ChunkPos: pos, Pieces: pieces}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// lowestYIn5by5 ports Structure.getLowestYIn5by5BoxOffset7Blocks: the min WORLD_SURFACE_WG height
// over the 4 corners of a 5x5 box anchored at (chunkX*16+7, chunkZ*16+7), the offset signs flipping
// by rotation. The block X/Z are the anchor; the Y is that 4-corner min.
func (g *woodlandMansionStartGen) lowestYIn5by5(sampler SurfaceSampler, cx, cz int, rot Rotation) (int, int, int) {
	offsetX := 5
	offsetZ := 5
	switch rot {
	case RotClockwise90:
		offsetX = -5
	case RotClockwise180:
		offsetX = -5
		offsetZ = -5
	case RotCounterclockwise90:
		offsetZ = -5
	}
	blockX := cx*16 + 7
	blockZ := cz*16 + 7
	// getLowestY over corners (blockX,blockZ),(blockX,blockZ+offsetZ),(blockX+offsetX,blockZ),
	// (blockX+offsetX,blockZ+offsetZ) -- the same 4-corner pattern as lowestCornerY but with a
	// SIGNED offset (which can be negative), so it is inlined here.
	a := sampler.SampleSurfaceY(blockX, blockZ)
	b := sampler.SampleSurfaceY(blockX, blockZ+offsetZ)
	c := sampler.SampleSurfaceY(blockX+offsetX, blockZ)
	d := sampler.SampleSurfaceY(blockX+offsetX, blockZ+offsetZ)
	lowest := min(min(a, b), min(c, d))
	return blockX, lowest, blockZ
}
