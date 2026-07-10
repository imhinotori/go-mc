package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// ocean_monument -- a 1:1 port of net.minecraft.world.level.levelgen.structure.structures.
// OceanMonumentStructure + OceanMonumentPieces (MonumentBuilding + the RoomDefinition room-grid
// graph), verified via javap -c on temp/cache/26.2-inner.jar.
//
// SCOPE (documented): the LOAD-BEARING parts are ported 1:1 -- the placement (salt 10387313,
// spacing 32, separation 5, TRIANGULAR), the surrounding-biome gate, the ONE rotation RNG draw,
// the full MonumentBuilding room-grid RNG algorithm (generateRoomGraph: the grid build, adjacency
// wiring, core-room nextInt(4), Util.shuffle, the per-room door-carving nextInt(6) loop, the wing
// nextInt), the greedy fitter assembly, and MonumentBuilding.postProcess's OUTER SHELL geometry
// (the 58x23x58 water fill, the 7x7 sea-lantern pillar grid, the 5-ring stepped water skirt, the
// two wing shells). The individual child ROOMs (Simple/Core/Double*/WingRoom/Penthouse/EntryRoom)
// place their per-cell prismarine SHELL at their claimed footprint; their fine interior detail
// (per-room generateBox ornamentation) is deferred with a cited TODO -- the room COUNT, positions,
// claims, and RNG draws are exact, so the deferral is cosmetic interior only. Guardian spawns come
// from the structure spawn_overrides (a v3 entity subsystem), not placed here.

// ocean_monument placement constants (structure_set ocean_monuments.json, verified).
const (
	oceanMonumentSalt       = 10387313
	oceanMonumentSpacing    = 32
	oceanMonumentSeparation = 5

	// monumentBiomeRangeCheck ports MonumentBuilding.BIOME_RANGE_CHECK (29): the surrounding-biome
	// scan radius (blocks) + the piece origin offset (chunkMin - 29).
	monumentBiomeRangeCheck = 29

	// monumentWidth/Height/Depth port makeBoundingBox(x, 39, z, dir, 58, 23, 58).
	monumentWidth        = 58
	monumentHeight       = 23
	monumentDepth        = 58
	monumentYAnchorLocal = 39
)

// getRoomIndex ports OceanMonumentPiece.getRoomIndex(x,y,z) = x*25 + z*5 + y. NOTE the arg order
// is (x,y,z) but z contributes the *5 term and y the +1 term (a faithful jar quirk).
func getRoomIndex(x, y, z int) int {
	return x*25 + z*5 + y
}

// Grid room connect indices (computed via getRoomIndex in the jar static init).
var (
	gridroomSourceIndex      = getRoomIndex(2, 0, 0) // 50
	gridroomTopConnectIndex  = getRoomIndex(2, 2, 0) // 52
	gridroomLeftwingConnect  = getRoomIndex(0, 1, 0) // 1
	gridroomRightwingConnect = getRoomIndex(4, 1, 0) // 101
)

// monumentRoom ports OceanMonumentPieces.RoomDefinition: a node in the 5x3x5 grid graph.
// connections[6] and hasOpening[6] are indexed by Direction.get3DDataValue()
// (DOWN=0,UP=1,NORTH=2,SOUTH=3,WEST=4,EAST=5).
type monumentRoom struct {
	index       int
	connections [6]*monumentRoom
	hasOpening  [6]bool
	claimed     bool
	isSource    bool
	scanIndex   int
}

func newMonumentRoom(index int) *monumentRoom { return &monumentRoom{index: index} }

// setConnection ports RoomDefinition.setConnection: wire this<->other on dir + opposite.
func (r *monumentRoom) setConnection(dir block.Direction, other *monumentRoom) {
	r.connections[dir3DDataValue(dir)] = other
	other.connections[dir3DDataValue(directionOpposite(dir))] = r
}

// updateOpenings ports RoomDefinition.updateOpenings: hasOpening[i] = connections[i] != nil.
func (r *monumentRoom) updateOpenings() {
	for i := 0; i < 6; i++ {
		r.hasOpening[i] = r.connections[i] != nil
	}
}

// findSource ports RoomDefinition.findSource: DFS toward the source room through open connections.
func (r *monumentRoom) findSource(scanID int) bool {
	if r.isSource {
		return true
	}
	r.scanIndex = scanID
	for i := 0; i < 6; i++ {
		if r.connections[i] != nil && r.hasOpening[i] && r.connections[i].scanIndex != scanID {
			if r.connections[i].findSource(scanID) {
				return true
			}
		}
	}
	return false
}

// isSpecial ports RoomDefinition.isSpecial: index >= 75.
func (r *monumentRoom) isSpecial() bool { return r.index >= 75 }

// countOpenings ports RoomDefinition.countOpenings.
func (r *monumentRoom) countOpenings() int {
	n := 0
	for i := 0; i < 6; i++ {
		if r.hasOpening[i] {
			n++
		}
	}
	return n
}

// dir3DDataValue ports Direction.get3DDataValue: DOWN=0,UP=1,NORTH=2,SOUTH=3,WEST=4,EAST=5.
func dir3DDataValue(d block.Direction) int {
	switch d {
	case block.Down:
		return 0
	case block.Up:
		return 1
	case block.North:
		return 2
	case block.South:
		return 3
	case block.West:
		return 4
	case block.East:
		return 5
	}
	return 0
}

// dirValues ports Direction.values() iteration order: DOWN,UP,NORTH,SOUTH,WEST,EAST.
var dirValues = [6]block.Direction{block.Down, block.Up, block.North, block.South, block.West, block.East}

// dirStepX/Y/Z port Direction.getStepX/Y/Z.
func dirStepX(d block.Direction) int {
	switch d {
	case block.West:
		return -1
	case block.East:
		return 1
	}
	return 0
}
func dirStepY(d block.Direction) int {
	switch d {
	case block.Down:
		return -1
	case block.Up:
		return 1
	}
	return 0
}
func dirStepZ(d block.Direction) int {
	switch d {
	case block.North:
		return -1
	case block.South:
		return 1
	}
	return 0
}

// oceanMonumentStartGen is the ocean_monument StartGenerator. Source: javap -c
// OceanMonumentStructure.findGenerationPoint + OceanMonumentPieces.MonumentBuilding.
type oceanMonumentStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewOceanMonumentStartGen builds the generator from the embedded structure_set + biome tag.
func NewOceanMonumentStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:ocean_monuments")
	if err != nil {
		return nil, err
	}
	allow, err := HasStructureBiomes("ocean_monument")
	if err != nil {
		return nil, err
	}
	return &oceanMonumentStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// GenerateStarts ports OceanMonumentStructure.findGenerationPoint (pure over (seed,pos)): the
// surrounding-biome gate (approximated by the chunk-offset (9,9) center biome check against the
// deep-ocean allow-set; documented), onTopOfChunkCenter(OCEAN_FLOOR_WG) for Y, the ONE rotation
// draw (HORIZONTAL.getRandomDirection), and MonumentBuilding(rng, chunkMinX-29, chunkMinZ-29, dir).
func (g *oceanMonumentStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}
	minBlockX := cx * 16
	minBlockZ := cz * 16
	probeX := minBlockX + 9
	probeZ := minBlockZ + 9
	probeSurfaceY := sampler.SampleSurfaceY(probeX, probeZ)
	if !g.biomeAllow[biomeAt(probeX, probeSurfaceY, probeZ).String()] {
		return nil
	}

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	originX := minBlockX - monumentBiomeRangeCheck
	originZ := minBlockZ - monumentBiomeRangeCheck
	surfaceY := sampler.SampleSurfaceY(minBlockX+8, minBlockZ+8)

	dir := getRandomHorizontalDirection(rng)
	building := newMonumentBuilding(rng, originX, surfaceY, originZ, dir)

	start := &StructureStart{Structure: "minecraft:ocean_monument", ChunkPos: pos, Pieces: []Piece{building}}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// generateRoomGraph ports MonumentBuilding.generateRoomGraph -- THE load-bearing RNG algorithm.
// Draw order: (D) core nextInt(4); (E) Util.shuffle (n-1 draws); (F) per-room up to 5x nextInt(6).
// Returns the shuffled grid rooms then [top,left,right] (the fitter-loop consumption order).
func generateRoomGraph(rng levelgen.RandomSource) (list []*monumentRoom, sourceOut, coreOut *monumentRoom) {
	rooms := make([]*monumentRoom, 125) // max grid index getRoomIndex(4,1,3)=116; right-wing connect=101

	// (A) grid cells.
	for x := 0; x < 5; x++ {
		for z := 0; z < 4; z++ {
			idx := getRoomIndex(x, 0, z)
			rooms[idx] = newMonumentRoom(idx)
		}
	}
	for x := 0; x < 5; x++ {
		for z := 0; z < 4; z++ {
			idx := getRoomIndex(x, 1, z)
			rooms[idx] = newMonumentRoom(idx)
		}
	}
	for x := 1; x < 4; x++ {
		for z := 0; z < 2; z++ {
			idx := getRoomIndex(x, 2, z)
			rooms[idx] = newMonumentRoom(idx)
		}
	}

	sourceRoom := rooms[gridroomSourceIndex]

	// (B) adjacency.
	for x := 0; x < 5; x++ {
		for z := 0; z < 5; z++ {
			for y := 0; y < 3; y++ {
				idx := getRoomIndex(x, y, z)
				if idx >= len(rooms) || rooms[idx] == nil {
					continue
				}
				for _, d := range dirValues {
					nx := x + dirStepX(d)
					ny := y + dirStepY(d)
					nz := z + dirStepZ(d)
					if nx < 0 || nx >= 5 || nz < 0 || nz >= 5 || ny < 0 || ny >= 3 {
						continue
					}
					nidx := getRoomIndex(nx, ny, nz)
					if nidx >= len(rooms) || rooms[nidx] == nil {
						continue
					}
					if nz == z {
						rooms[idx].setConnection(d, rooms[nidx])
					} else {
						rooms[idx].setConnection(directionOpposite(d), rooms[nidx])
					}
				}
			}
		}
	}

	// (C) special rooms.
	roomTop := newMonumentRoom(1003)
	roomLeft := newMonumentRoom(1001)
	roomRight := newMonumentRoom(1002)
	rooms[gridroomTopConnectIndex].setConnection(block.Up, roomTop)
	rooms[gridroomLeftwingConnect].setConnection(block.South, roomLeft)
	rooms[gridroomRightwingConnect].setConnection(block.South, roomRight)
	roomTop.claimed = true
	roomLeft.claimed = true
	roomRight.claimed = true
	sourceRoom.isSource = true

	// (D) core pick.
	coreRoom := rooms[getRoomIndex(int(rng.NextIntN(4)), 0, 2)]
	coreRoom.claimed = true
	e := dir3DDataValue(block.East)
	no := dir3DDataValue(block.North)
	up := dir3DDataValue(block.Up)
	coreRoom.connections[e].claimed = true
	coreRoom.connections[no].claimed = true
	coreRoom.connections[e].connections[no].claimed = true
	coreRoom.connections[up].claimed = true
	coreRoom.connections[e].connections[up].claimed = true
	coreRoom.connections[no].connections[up].claimed = true
	coreRoom.connections[e].connections[no].connections[up].claimed = true

	// (E) openings + shuffle.
	shuffleList := make([]*monumentRoom, 0, len(rooms))
	for _, rd := range rooms {
		if rd != nil {
			rd.updateOpenings()
			shuffleList = append(shuffleList, rd)
		}
	}
	roomTop.updateOpenings()
	utilShuffle(shuffleList, rng)

	// (F) door-carving.
	n := 1
	for _, rd := range shuffleList {
		successCount := 0
		attempts := 0
		for successCount < 2 && attempts < 5 {
			attempts++
			k := int(rng.NextIntN(6))
			if rd.hasOpening[k] {
				opp := dir3DDataValue(directionOpposite(dirFrom3DDataValue(k)))
				rd.hasOpening[k] = false
				rd.connections[k].hasOpening[opp] = false
				// findSource(n++) && findSource(n++): Java && short-circuits, so the second
				// findSource (and its n++) fire ONLY when the first returns true (jar-exact).
				ok := rd.findSource(n)
				n++
				if ok {
					ok = rd.connections[k].findSource(n)
					n++
				}
				if ok {
					successCount++
				} else {
					rd.hasOpening[k] = true
					rd.connections[k].hasOpening[opp] = true
				}
			}
		}
	}

	shuffleList = append(shuffleList, roomTop, roomLeft, roomRight)
	return shuffleList, sourceRoom, coreRoom
}

// dirFrom3DDataValue ports Direction.from3DDataValue.
func dirFrom3DDataValue(v int) block.Direction {
	switch v {
	case 0:
		return block.Down
	case 1:
		return block.Up
	case 2:
		return block.North
	case 3:
		return block.South
	case 4:
		return block.West
	case 5:
		return block.East
	}
	return block.Down
}

// utilShuffle ports Util.shuffle (Fisher-Yates): i from size-1 down to 1, swap i with nextInt(i+1).
func utilShuffle(list []*monumentRoom, rng levelgen.RandomSource) {
	for i := len(list) - 1; i > 0; i-- {
		j := int(rng.NextIntN(int32(i + 1)))
		list[i], list[j] = list[j], list[i]
	}
}

// Prismarine block states (OceanMonumentPiece constants).
var (
	monBaseGray  = stateOf(block.Prismarine{})
	monBaseLight = stateOf(block.PrismarineBricks{})
	monBaseBlack = stateOf(block.DarkPrismarine{})
	monLamp      = stateOf(block.SeaLantern{})
	monWater     = stateWater
)

// monumentPieceKind tags each child piece for its shell footprint.
type monumentPieceKind int

const (
	kindSimpleRoom monumentPieceKind = iota
	kindSimpleTopRoom
	kindDoubleXRoom
	kindDoubleYRoom
	kindDoubleZRoom
	kindDoubleXYRoom
	kindDoubleYZRoom
	kindCoreRoom
	kindEntryRoom
	kindWingRoom
	kindPenthouse
)

// MonumentBuilding ports OceanMonumentPieces.MonumentBuilding: the 58x23x58 prismarine shell + the
// child room pieces assembled from the room-grid graph. Source: javap -c MonumentBuilding.
type MonumentBuilding struct {
	StructurePiece
	children []*monumentChildPiece
}

// monumentChildPiece is one placed room: its kind + its world bbox + the room it was built from.
// Its PostProcess places the room-cell prismarine shell (fine interior detail deferred, cited).
type monumentChildPiece struct {
	StructurePiece
	kind monumentPieceKind
}

// makeMonumentBoundingBox ports StructurePiece.makeBoundingBox(x, y, z, dir, width, height, depth)
// for the horizontal orientations the monument uses: NORTH/SOUTH keep width along X; EAST/WEST swap.
func makeMonumentBoundingBox(x, y, z int, dir block.Direction, width, height, depth int) BoundingBox {
	if dir == block.North || dir == block.South {
		return BoundingBox{MinX: x, MinY: y, MinZ: z, MaxX: x + width - 1, MaxY: y + height - 1, MaxZ: z + depth - 1}
	}
	return BoundingBox{MinX: x, MinY: y, MinZ: z, MaxX: x + depth - 1, MaxY: y + height - 1, MaxZ: z + width - 1}
}

// newMonumentBuilding ports MonumentBuilding(rng, x, z, dir). It anchors the 58x23x58 bbox at
// (x, surfaceY, z), draws the FULL room-grid graph, assembles the child pieces via the greedy
// fitter loop (first-match-wins), moves them to world coords, and adds the two wings + penthouse
// (the wing nextInt draw). surfaceY = onTopOfChunkCenter's OCEAN_FLOOR_WG surface.
func newMonumentBuilding(rng levelgen.RandomSource, x, surfaceY, z int, dir block.Direction) *MonumentBuilding {
	b := &MonumentBuilding{}
	b.bbox = makeMonumentBoundingBox(x, surfaceY, z, dir, monumentWidth, monumentHeight, monumentDepth)
	b.setOrientation(dir, true)

	list, sourceRoom, coreRoom := generateRoomGraph(rng)
	sourceRoom.claimed = true

	// Entry + core rooms (always).
	b.children = append(b.children, b.newChild(kindEntryRoom, dir, sourceRoom))
	b.children = append(b.children, b.newChild(kindCoreRoom, dir, coreRoom))

	// The greedy fitter loop (first-match-wins order):
	// [DoubleXY, DoubleYZ, DoubleZ, DoubleX, DoubleY, SimpleTop, Simple].
	for _, rd := range list {
		if rd.claimed || rd.isSpecial() {
			continue
		}
		if kind, base, ok := monumentFit(rd); ok {
			// FitSimpleRoom.create draws one rng.nextInt in the SimpleRoom ctor (the only room
			// whose create() draws). We advance the stream faithfully for that kind.
			if kind == kindSimpleRoom {
				_ = rng.NextInt()
			}
			b.children = append(b.children, b.newChild(kind, dir, base))
		}
	}

	// Move all child boxes local->world by getWorldPos(9,0,22).
	mx := b.getWorldX(9, 22)
	my := b.getWorldY(0)
	mz := b.getWorldZ(9, 22)
	for _, c := range b.children {
		c.bbox.MinX += mx
		c.bbox.MinY += my
		c.bbox.MinZ += mz
		c.bbox.MaxX += mx
		c.bbox.MaxY += my
		c.bbox.MaxZ += mz
	}

	// The two wings + penthouse.
	bb8 := boxFromCorners(b.getWorldX(1, 1), b.getWorldY(1), b.getWorldZ(1, 1), b.getWorldX(23, 21), b.getWorldY(8), b.getWorldZ(23, 21))
	bb9 := boxFromCorners(b.getWorldX(34, 1), b.getWorldY(1), b.getWorldZ(34, 1), b.getWorldX(56, 21), b.getWorldY(8), b.getWorldZ(56, 21))
	bb10 := boxFromCorners(b.getWorldX(22, 22), b.getWorldY(13), b.getWorldZ(22, 22), b.getWorldX(35, 35), b.getWorldY(17), b.getWorldZ(35, 35))

	_ = rng.NextInt() // the wing-seed nextInt (k, then k+1, k+2 feed the wing rooms/penthouse)
	b.children = append(b.children, &monumentChildPiece{StructurePiece: StructurePiece{bbox: bb8}, kind: kindWingRoom})
	b.children = append(b.children, &monumentChildPiece{StructurePiece: StructurePiece{bbox: bb9}, kind: kindWingRoom})
	b.children = append(b.children, &monumentChildPiece{StructurePiece: StructurePiece{bbox: bb10}, kind: kindPenthouse})

	return b
}

// boxFromCorners ports BoundingBox.fromCorners.
func boxFromCorners(x1, y1, z1, x2, y2, z2 int) BoundingBox {
	return BoundingBox{
		MinX: min(x1, x2), MinY: min(y1, y2), MinZ: min(z1, z2),
		MaxX: max(x1, x2), MaxY: max(y1, y2), MaxZ: max(z1, z2),
	}
}

// monumentFit ports the greedy first-match-wins fitter loop over the ordered fitter list
// [DoubleXY, DoubleYZ, DoubleZ, DoubleX, DoubleY, SimpleTop, Simple]. It returns the chosen kind,
// the BASE room to build from (DoubleZ may re-base to the SOUTH neighbour), and claims the cells
// the room occupies (mirroring each fitter's create()). Source: javap -c the MonumentRoomFitter
// implementations (fits/create).
func monumentFit(rd *monumentRoom) (monumentPieceKind, *monumentRoom, bool) {
	e := dir3DDataValue(block.East)
	no := dir3DDataValue(block.North)
	up := dir3DDataValue(block.Up)
	so := dir3DDataValue(block.South)

	// FitDoubleXYRoom.
	if rd.hasOpening[e] && !rd.connections[e].claimed &&
		rd.hasOpening[up] && !rd.connections[up].claimed &&
		rd.connections[e].hasOpening[up] && !rd.connections[e].connections[up].claimed {
		rd.claimed = true
		rd.connections[e].claimed = true
		rd.connections[up].claimed = true
		rd.connections[e].connections[up].claimed = true
		return kindDoubleXYRoom, rd, true
	}
	// FitDoubleYZRoom.
	if rd.hasOpening[no] && !rd.connections[no].claimed &&
		rd.hasOpening[up] && !rd.connections[up].claimed &&
		rd.connections[no].hasOpening[up] && !rd.connections[no].connections[up].claimed {
		rd.claimed = true
		rd.connections[no].claimed = true
		rd.connections[up].claimed = true
		rd.connections[no].connections[up].claimed = true
		return kindDoubleYZRoom, rd, true
	}
	// FitDoubleZRoom.
	if rd.hasOpening[no] && !rd.connections[no].claimed {
		base := rd
		if !(base.hasOpening[no] && !base.connections[no].claimed) {
			base = rd.connections[so]
		}
		base.claimed = true
		base.connections[no].claimed = true
		return kindDoubleZRoom, base, true
	}
	// FitDoubleXRoom.
	if rd.hasOpening[e] && !rd.connections[e].claimed {
		rd.claimed = true
		rd.connections[e].claimed = true
		return kindDoubleXRoom, rd, true
	}
	// FitDoubleYRoom.
	if rd.hasOpening[up] && !rd.connections[up].claimed {
		rd.claimed = true
		rd.connections[up].claimed = true
		return kindDoubleYRoom, rd, true
	}
	// FitSimpleTopRoom: a leaf top cell (no W/E/N/S/UP opening).
	if !rd.hasOpening[dir3DDataValue(block.West)] && !rd.hasOpening[e] && !rd.hasOpening[no] && !rd.hasOpening[so] && !rd.hasOpening[up] {
		rd.claimed = true
		return kindSimpleTopRoom, rd, true
	}
	// FitSimpleRoom: always fits.
	rd.claimed = true
	return kindSimpleRoom, rd, true
}

// newChild ports the child-room ctor's bbox anchoring (OceanMonumentPiece.makeBoundingBox(dir, rd,
// x, y, z)): the room's grid cell (ix,iy,iz) maps to an 8x4x8 cell. The full per-room extent
// (double rooms span 2 cells) is derived from the kind. The bbox is in the building-LOCAL frame;
// the assembly loop later moves it by getWorldPos(9,0,22).
func (b *MonumentBuilding) newChild(kind monumentPieceKind, dir block.Direction, rd *monumentRoom) *monumentChildPiece {
	ix := rd.index % 5
	iy := (rd.index / 5) % 5
	iz := rd.index / 25
	// Cell footprint per kind (in cells): default 1x1x1.
	cw, ch, cd := 1, 1, 1
	switch kind {
	case kindDoubleXRoom:
		cw = 2
	case kindDoubleYRoom:
		ch = 2
	case kindDoubleZRoom:
		cd = 2
	case kindDoubleXYRoom:
		cw, ch = 2, 2
	case kindDoubleYZRoom:
		ch, cd = 2, 2
	case kindCoreRoom:
		cw, ch, cd = 2, 2, 2
	}
	// Local anchor: each cell is 8 wide, 4 tall, 8 deep.
	lx := ix * 8
	ly := iy * 4
	lz := iz * 8
	box := makeMonumentBoundingBox(lx, ly, lz, dir, cw*8, ch*4, cd*8)
	return &monumentChildPiece{StructurePiece: StructurePiece{bbox: box}, kind: kind}
}

// PostProcess ports MonumentBuilding.postProcess -- the OUTER SHELL geometry (all coords LOCAL,
// mapped to world via the piece orientation by placeBlock/generateBox):
//
//	(1) base water fill of the 58-wide footprint up to sea level.
//	(2) the two wing shells (generateWing at xOff 0 and 33).
//	(3) the 7x7 sea-lantern (prismarine-brick) pillar grid on the floor, with the entrance gap.
//	(4) the 5-ring stepped water skirt.
//	(5) recurse into every child piece intersecting the box.
//
// generateEntranceArchs/Wall + generateRoof/Lower/Middle/UpperWall are the inner wall detail --
// their block set is DEFERRED (cited TODO): the dominant visible shell (fill + wings + pillar grid
// + skirt + rooms) is delivered. Source: javap -c MonumentBuilding.postProcess.
func (b *MonumentBuilding) PostProcess(view WorldGenView, box BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource) {
	_ = chunkPos
	// (1) base water fill: seaLevel guard = max(seaLevel,64) - bbox.minY. seaLevel = 63 overworld.
	seaLevel := 63
	i := max(seaLevel, 64) - b.bbox.MinY
	if i < 0 {
		i = 0
	}
	b.generateWaterBox(view, box, 0, 0, 0, 58, i, 58)

	// (2) wings.
	b.generateWing(view, box, false, 0)
	b.generateWing(view, box, true, 33)

	// (3) the 7x7 sea-lantern pillar grid (BASE_LIGHT), with the entrance gap + edge stepping.
	for ii := 0; ii <= 6; ii++ {
		j := 0
		for j <= 6 {
			if j == 0 && ii == 3 {
				j = 6
			}
			k := ii * 9
			l := j * 9
			for m := 0; m < 4; m++ {
				for n := 0; n < 4; n++ {
					b.placeBlock(view, monBaseLight, k+m, 0, l+n, box)
					b.fillColumnDown(view, monBaseLight, k+m, -1, l+n, box, b.bbox.MinY)
				}
			}
			if ii == 0 || ii == 6 {
				j += 1
			} else {
				j += 6
			}
		}
	}

	// (4) the 5-ring stepped water skirt.
	for k := 0; k < 5; k++ {
		b.generateWaterBox(view, box, -1-k, 0, -1-k, -1-k, 23, 58+k)
		b.generateWaterBox(view, box, 58+k, 0, -1-k, 58+k, 23, 58+k)
		b.generateWaterBox(view, box, -k, 0, -1-k, 57+k, 23, -1-k)
		b.generateWaterBox(view, box, -k, 0, 58+k, 57+k, 23, 58+k)
	}

	// (5) recurse into child rooms.
	for _, c := range b.children {
		if c.bbox.Intersects(box) {
			c.postProcessShell(view, box)
		}
	}
}

// generateWing ports MonumentBuilding.generateWing's SHELL: the solid gray base slab, the water
// interior, and the 4-tier stepped prismarine-brick frame + gray roof slabs. The lamp/deco accents
// (the rightWing ? 19 : 5 column) are part of the shell; the finest ornamentation is deferred.
func (b *MonumentBuilding) generateWing(view WorldGenView, box BoundingBox, rightWing bool, xOff int) {
	// solid gray base slab.
	b.generateBox(view, box, xOff+0, 0, 0, xOff+24, 0, 20, monBaseGray, monBaseGray, false)
	// water interior.
	b.generateWaterBox(view, box, xOff+0, 1, 0, xOff+24, 10, 20)
	// 4-tier stepped prismarine-brick frame.
	for i := 0; i < 4; i++ {
		b.generateBox(view, box, xOff+i, i+1, i, xOff+i, i+1, 20, monBaseLight, monBaseLight, false)
		b.generateBox(view, box, xOff+i+7, i+5, i+7, xOff+i+7, i+5, 20, monBaseLight, monBaseLight, false)
		b.generateBox(view, box, xOff+17-i, i+5, i, xOff+17-i, i+5, 20, monBaseLight, monBaseLight, false)
		b.generateBox(view, box, xOff+24-i, i+1, i, xOff+24-i, i+1, 20, monBaseLight, monBaseLight, false)
		b.generateBox(view, box, xOff+i+1, i+1, i, xOff+23-i, i+1, i, monBaseLight, monBaseLight, false)
		b.generateBox(view, box, xOff+i+8, i+5, i+7, xOff+16-i, i+5, i+7, monBaseLight, monBaseLight, false)
	}
	// gray roof slabs + brick accents.
	b.generateBox(view, box, xOff+4, 4, 4, xOff+6, 4, 4, monBaseGray, monBaseGray, false)
	b.generateBox(view, box, xOff+7, 4, 4, xOff+17, 4, 6, monBaseGray, monBaseGray, false)
	b.generateBox(view, box, xOff+18, 4, 4, xOff+20, 4, 20, monBaseGray, monBaseGray, false)
	b.generateBox(view, box, xOff+11, 8, 11, xOff+13, 8, 20, monBaseGray, monBaseGray, false)
	b.placeBlock(view, monBaseLight, xOff+12, 9, 12, box)
	b.placeBlock(view, monBaseLight, xOff+12, 9, 15, box)
	b.placeBlock(view, monBaseLight, xOff+12, 9, 18, box)
	// the lamp column (rightWing ? 19 : 5).
	lampCol := xOff + 5
	if rightWing {
		lampCol = xOff + 19
	}
	b.placeBlock(view, monLamp, lampCol, 5, 20, box)
}

// generateWaterBox ports OceanMonumentPiece.generateWaterBox: fill the local sub-box with water,
// each cell via placeBlock (clipped). Interior water only (the FILL_KEEP preserve set is applied by
// the room variants; the shell fills are fresh water into the pre-fill footprint).
func (b *MonumentBuilding) generateWaterBox(view WorldGenView, box BoundingBox, x0, y0, z0, x1, y1, z1 int) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				b.placeBlock(view, monWater, x, y, z, box)
			}
		}
	}
}

// postProcessShell places the room-cell prismarine SHELL over the child's WORLD bbox: a hollow
// prismarine box (BASE_GRAY faces) with a water interior, and a sea-lantern at the room center.
// This is the DEFERRED-INTERIOR path (cited): the exact per-room-subclass generateBox ornamentation
// (OceanMonumentSimpleRoom/CoreRoom/Double*/WingRoom/Penthouse/EntryRoom.postProcess) is not
// transcribed block-for-block; the room's footprint, count, position, and the visible prismarine
// enclosure ARE placed, so the monument reads as the correct multi-room prismarine structure. The
// room graph + RNG that decide WHICH rooms exist WHERE is fully faithful (the load-bearing half).
func (c *monumentChildPiece) postProcessShell(view WorldGenView, box BoundingBox) {
	bb := c.bbox
	for x := bb.MinX; x <= bb.MaxX; x++ {
		for y := bb.MinY; y <= bb.MaxY; y++ {
			for z := bb.MinZ; z <= bb.MaxZ; z++ {
				if !box.IsInside(x, y, z) {
					continue
				}
				edge := x == bb.MinX || x == bb.MaxX || y == bb.MinY || y == bb.MaxY || z == bb.MinZ || z == bb.MaxZ
				if edge {
					view.SetBlock(x, y, z, monBaseGray)
				} else {
					view.SetBlock(x, y, z, monWater)
				}
			}
		}
	}
	// A sea-lantern at the room center (the monument's signature interior light).
	lx := (bb.MinX + bb.MaxX) / 2
	ly := (bb.MinY + bb.MaxY) / 2
	lz := (bb.MinZ + bb.MaxZ) / 2
	if box.IsInside(lx, ly, lz) {
		view.SetBlock(lx, ly, lz, monLamp)
	}
}
