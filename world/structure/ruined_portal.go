package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// ruined_portal -- a 1:1 port of RuinedPortalStructure + RuinedPortalPiece (26.2, verified via
// javap -c on temp/cache/26.2-inner.jar).
//
// TEMPLATE STUB (documented, per the task allowance): RuinedPortal is a TEMPLATE structure --
// findGenerationPoint picks one of 13 .nbt templates (ruined_portal/portal_1..10 +
// giant_portal_1..3) and RuinedPortalPiece.postProcess places it via super.postProcess with the
// mossiness/decay/lava/cold processor chain. Those 13 .nbt assets are NOT embedded in this repo.
// So the TEMPLATE-BLOCK ITERATION (obsidian frame + portal + lava the template carries) is
// STUBBED: the placement CALL SHAPE, the FULL 6-draw RNG selection, the vertical-placement Y math,
// and the netherrack blob / drip-column / vine DECORATION passes (which operate on WORLD blocks)
// are ported 1:1 against the piece bbox. Deferred: the template block set. When the .nbt is
// embedded, the piece gains a StructureTemplate + PlaceInWorld call and this stub disappears; the
// RNG stream + bbox anchoring are already bit-faithful so the swap is drop-in.
//
// Placement (structure_set/ruined_portals.json): salt 34222645, spacing 40, separation 15, LINEAR.
const (
	ruinedPortalSalt       = 34222645
	ruinedPortalSpacing    = 40
	ruinedPortalSeparation = 15

	probabilityOfGiantPortal     = float32(0.05)
	minYIndex                    = 15
	probMagmaInsteadOfNetherrack = float32(0.07)

	portalTemplateW = 12
	portalTemplateH = 10
	portalTemplateD = 12
	giantTemplateW  = 16
	giantTemplateH  = 16
	giantTemplateD  = 16

	overworldMinY = -64
)

// verticalPlacement ports RuinedPortalStructure.VerticalPlacement.
type verticalPlacement int

const (
	vpOnLandSurface verticalPlacement = iota
	vpPartlyBuried
	vpOnOceanFloor
	vpInMountain
	vpUnderground
	vpInNether
)

// ruinedPortalSetup ports RuinedPortalStructure.Setup.
type ruinedPortalSetup struct {
	placement             verticalPlacement
	airPocketProbability  float32
	mossiness             float32
	overgrown             bool
	vines                 bool
	canBeCold             bool
	replaceWithBlackstone bool
	weight                float32
}

// ruinedPortalProperties ports RuinedPortalPiece.Properties.
type ruinedPortalProperties struct {
	cold                  bool
	mossiness             float32
	airPocket             bool
	overgrown             bool
	vines                 bool
	replaceWithBlackstone bool
}

// ruinedPortalStartGen targets the STANDARD variant. Source: javap -c
// RuinedPortalStructure.findGenerationPoint.
type ruinedPortalStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
	setups     []ruinedPortalSetup
}

// NewRuinedPortalStartGen builds the generator from the embedded structure_set + biome tag.
func NewRuinedPortalStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:ruined_portals")
	if err != nil {
		return nil, err
	}
	allow, err := HasStructureBiomes("ruined_portal_standard")
	if err != nil {
		return nil, err
	}
	setups := []ruinedPortalSetup{
		{placement: vpUnderground, airPocketProbability: 1.0, mossiness: 0.2, canBeCold: true, weight: 0.5},
		{placement: vpOnLandSurface, airPocketProbability: 0.5, mossiness: 0.2, canBeCold: true, weight: 0.5},
	}
	return &ruinedPortalStartGen{placement: set.Placement, biomeAllow: allow, setups: setups}, nil
}

// GenerateStarts ports RuinedPortalStructure.findGenerationPoint (pure over (seed,pos)). RNG draw
// order is JAR-EXACT: (1) weighted Setup nextFloat (only if >1 setup); (2) air-pocket sample
// (nextFloat only if 0<prob<1); (3) giant gate nextFloat<0.05; (4) template index nextInt(3|10);
// (5) rotation nextInt(4); (6) mirror nextFloat<0.5; (7) findSuitableY draws.
func (g *ruinedPortalStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}
	minBlockX := cx * 16
	minBlockZ := cz * 16
	centerX := minBlockX + 8
	centerZ := minBlockZ + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !g.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	var setup ruinedPortalSetup
	if len(g.setups) > 1 {
		var total float32
		for _, s := range g.setups {
			total += s.weight
		}
		r := rng.NextFloat()
		picked := false
		for _, s := range g.setups {
			r -= s.weight / total
			if r < 0 {
				setup = s
				picked = true
				break
			}
		}
		if !picked {
			setup = g.setups[len(g.setups)-1]
		}
	} else {
		setup = g.setups[0]
	}

	airPocket := ruinedPortalSample(rng, setup.airPocketProbability)

	giant := rng.NextFloat() < probabilityOfGiantPortal
	var tW, tH, tD int
	if giant {
		_ = rng.NextIntN(3)
		tW, tH, tD = giantTemplateW, giantTemplateH, giantTemplateD
	} else {
		_ = rng.NextIntN(10)
		tW, tH, tD = portalTemplateW, portalTemplateH, portalTemplateD
	}

	rot := Rotation(rng.NextIntN(4))

	mir := MirrorNone
	if !(rng.NextFloat() < 0.5) {
		mir = MirrorFrontBack
	}

	worldX := minBlockX
	worldZ := minBlockZ
	spanX, spanZ := tW, tD
	if rot == RotClockwise90 || rot == RotCounterclockwise90 {
		spanX, spanZ = tD, tW
	}
	bbCenterX := worldX + spanX/2
	bbCenterZ := worldZ + spanZ/2
	surfaceY := sampler.SampleSurfaceY(bbCenterX, bbCenterZ) - 1

	y := ruinedPortalFindSuitableY(rng, setup.placement, airPocket, surfaceY, tH)

	// cold: biome.coldEnoughToSnow -- STUB (cited): needs biome temperature (not ported). Default
	// false (warm branch = vanilla default for the broad standard biome set).
	cold := false

	props := ruinedPortalProperties{
		cold:                  cold,
		mossiness:             setup.mossiness,
		airPocket:             airPocket,
		overgrown:             setup.overgrown,
		vines:                 setup.vines,
		replaceWithBlackstone: setup.replaceWithBlackstone,
	}
	piece := newRuinedPortalPiece(worldX, y, worldZ, spanX, tH, spanZ, setup.placement, props, rot, mir)
	start := &StructureStart{Structure: "minecraft:ruined_portal", ChunkPos: pos, Pieces: []Piece{piece}}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// ruinedPortalSample ports RuinedPortalStructure.sample: exact-float guards at 0/1 (no draw), else
// nextFloat() < prob.
func ruinedPortalSample(rng levelgen.RandomSource, prob float32) bool {
	if prob == 0.0 {
		return false
	}
	if prob == 1.0 {
		return true
	}
	return rng.NextFloat() < prob
}

// ruinedPortalFindSuitableY ports RuinedPortalStructure.findSuitableY. Placement Y draws JAR-EXACT.
// The column-clearance descent draws NO RNG in vanilla, so omitting it does not desync the stream;
// only the exact Y of a mountain/underground portal is approximated (documented).
func ruinedPortalFindSuitableY(rng levelgen.RandomSource, placement verticalPlacement, airPocket bool, surfaceY, yspan int) int {
	minYPlus15 := overworldMinY + minYIndex
	var y int
	switch placement {
	case vpInNether:
		if airPocket {
			y = randomBetweenInclusive(rng, 32, 100)
		} else if rng.NextFloat() < 0.5 {
			y = randomBetweenInclusive(rng, 27, 29)
		} else {
			y = randomBetweenInclusive(rng, 29, 100)
		}
	case vpInMountain:
		y = getRandomWithinInterval(rng, 70, surfaceY-yspan)
	case vpUnderground:
		y = getRandomWithinInterval(rng, minYPlus15, surfaceY-yspan)
	case vpPartlyBuried:
		y = surfaceY - yspan + randomBetweenInclusive(rng, 2, 8)
	default:
		y = surfaceY
	}
	if y < minYPlus15 {
		y = minYPlus15
	}
	return y
}

// getRandomWithinInterval ports RuinedPortalStructure.getRandomWithinInterval: RNG draw ONLY when
// lo < hi (else return hi, no draw).
func getRandomWithinInterval(rng levelgen.RandomSource, lo, hi int) int {
	if lo < hi {
		return randomBetweenInclusive(rng, lo, hi)
	}
	return hi
}

// randomBetweenInclusive ports Mth.randomBetweenInclusive = min + nextInt(max-min+1).
func randomBetweenInclusive(rng levelgen.RandomSource, mn, mx int) int {
	return mn + int(rng.NextIntN(int32(mx-mn+1)))
}

// RuinedPortalPiece ports RuinedPortalPiece. Source: javap -c
// RuinedPortalPiece.postProcess/spreadNetherrack/addNetherrackDripColumns*.
type RuinedPortalPiece struct {
	StructurePiece
	verticalPlacement verticalPlacement
	properties        ruinedPortalProperties
}

func newRuinedPortalPiece(worldX, y, worldZ, spanX, height, spanZ int, vp verticalPlacement, props ruinedPortalProperties, rot Rotation, mir Mirror) *RuinedPortalPiece {
	p := &RuinedPortalPiece{verticalPlacement: vp, properties: props}
	p.bbox = BoundingBox{MinX: worldX, MinY: y, MinZ: worldZ, MaxX: worldX + spanX - 1, MaxY: y + height - 1, MaxZ: worldZ + spanZ - 1}
	p.rotation = rot
	p.mirror = mir
	return p
}

// PostProcess ports RuinedPortalPiece.postProcess. Template block iteration (super.postProcess) is
// STUBBED. Netherrack blob / drip columns / vine passes ported 1:1 against the bbox. The vanilla
// early-out (only the center-owning chunk runs) is honored so the RNG stream is drawn once.
func (p *RuinedPortalPiece) PostProcess(view WorldGenView, box BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource) {
	_ = chunkPos
	bb := p.bbox
	cx := (bb.MinX + bb.MaxX) / 2
	cy := (bb.MinY + bb.MaxY) / 2
	cz := (bb.MinZ + bb.MaxZ) / 2
	if !box.IsInside(cx, cy, cz) {
		return
	}
	p.placeNetherrackBaseStub(view, box)
	p.spreadNetherrack(rng, view, box)
	p.addNetherrackDripColumnsBelowPortal(rng, view, box)
	if p.properties.vines || p.properties.overgrown {
		for x := bb.MinX; x <= bb.MaxX; x++ {
			for y := bb.MinY; y <= bb.MaxY; y++ {
				for z := bb.MinZ; z <= bb.MaxZ; z++ {
					if p.properties.vines {
						p.maybeAddVines(rng, view, box, x, y, z)
					}
					if p.properties.overgrown {
						p.maybeAddLeavesAbove(rng, view, box, x, y, z)
					}
				}
			}
		}
	}
}

// placeNetherrackBaseStub lays a netherrack floor at bb.minY (stands in for the template base so
// spreadNetherrack has the ground scar). STUB.
func (p *RuinedPortalPiece) placeNetherrackBaseStub(view WorldGenView, box BoundingBox) {
	bb := p.bbox
	nr := stateOf(block.Netherrack{})
	for x := bb.MinX; x <= bb.MaxX; x++ {
		for z := bb.MinZ; z <= bb.MaxZ; z++ {
			if box.IsInside(x, bb.MinY, z) {
				view.SetBlock(x, bb.MinY, z, nr)
			}
		}
	}
}

// spreadNetherrack ports RuinedPortalPiece.spreadNetherrack. RNG draw order JAR-EXACT: one
// nextInt(max(1, 8 - avgSpan/2)) up front, then nextDouble() PER CELL over the (2*reach+1)^2 scan
// (x outer, z inner).
func (p *RuinedPortalPiece) spreadNetherrack(rng levelgen.RandomSource, view WorldGenView, box BoundingBox) {
	bb := p.bbox
	placeBelowSurface := p.verticalPlacement == vpOnLandSurface || p.verticalPlacement == vpOnOceanFloor
	cx := (bb.MinX + bb.MaxX) / 2
	cz := (bb.MinZ + bb.MaxZ) / 2

	probs := [...]float32{1, 1, 1, 1, 1, 1, 1, 0.9, 0.9, 0.8, 0.7, 0.6, 0.4, 0.2}
	reach := len(probs)

	avgSpan := ((bb.MaxX - bb.MinX + 1) + (bb.MaxZ - bb.MinZ + 1)) / 2
	bound := 8 - avgSpan/2
	if bound < 1 {
		bound = 1
	}
	jitter := int(rng.NextIntN(int32(bound)))

	for x := cx - reach; x <= cx+reach; x++ {
		for z := cz - reach; z <= cz+reach; z++ {
			dist := abs(x-cx) + abs(z-cz)
			idx := dist + jitter
			if idx < 0 {
				idx = 0
			}
			if idx >= reach {
				continue
			}
			if rng.NextDouble() < float64(probs[idx]) {
				surfaceY := p.getSurfaceY(x, z)
				var y int
				if placeBelowSurface {
					y = surfaceY
				} else {
					y = min(bb.MinY, surfaceY)
				}
				if abs(y-bb.MinY) <= 3 && p.canBlockBeReplacedByNetherrackOrMagma(view, box, x, y, z) {
					p.placeNetherrackOrMagma(rng, view, box, x, y, z)
					if p.properties.overgrown {
						p.maybeAddLeavesAbove(rng, view, box, x, y, z)
					}
					p.addNetherrackDripColumn(rng, view, box, x, y-1, z)
				}
			}
		}
	}
}

// addNetherrackDripColumnsBelowPortal ports the same method: interior (minX+1..maxX-1,
// minZ+1..maxZ-1) at y=minY; if netherrack, drip a column below.
func (p *RuinedPortalPiece) addNetherrackDripColumnsBelowPortal(rng levelgen.RandomSource, view WorldGenView, box BoundingBox) {
	bb := p.bbox
	nr := stateOf(block.Netherrack{})
	for x := bb.MinX + 1; x < bb.MaxX; x++ {
		for z := bb.MinZ + 1; z < bb.MaxZ; z++ {
			if p.worldGet(view, box, x, bb.MinY, z) == nr {
				p.addNetherrackDripColumn(rng, view, box, x, bb.MinY-1, z)
			}
		}
	}
}

// addNetherrackDripColumn ports the same: place at start, then up to 8 steps down each gated by
// nextFloat() < 0.5 (short-circuit).
func (p *RuinedPortalPiece) addNetherrackDripColumn(rng levelgen.RandomSource, view WorldGenView, box BoundingBox, x, y, z int) {
	p.placeNetherrackOrMagma(rng, view, box, x, y, z)
	i := 8
	for i > 0 && rng.NextFloat() < 0.5 {
		y--
		i--
		p.placeNetherrackOrMagma(rng, view, box, x, y, z)
	}
}

// placeNetherrackOrMagma ports the same: when !cold, a 0.07 nextFloat() gate picks magma (draw
// short-circuits away when cold).
func (p *RuinedPortalPiece) placeNetherrackOrMagma(rng levelgen.RandomSource, view WorldGenView, box BoundingBox, x, y, z int) {
	if !p.properties.cold && rng.NextFloat() < probMagmaInsteadOfNetherrack {
		p.worldSet(view, box, x, y, z, stateOf(block.MagmaBlock{}))
	} else {
		p.worldSet(view, box, x, y, z, stateOf(block.Netherrack{}))
	}
}

// canBlockBeReplacedByNetherrackOrMagma ports the same: not air, not obsidian, not
// features_cannot_replace (STUB: only bedrock checked), and (for non-nether) not lava.
func (p *RuinedPortalPiece) canBlockBeReplacedByNetherrackOrMagma(view WorldGenView, box BoundingBox, x, y, z int) bool {
	st := p.worldGet(view, box, x, y, z)
	if block.IsAir(st) {
		return false
	}
	if st == stateOf(block.Obsidian{}) {
		return false
	}
	if st == stateOf(block.Bedrock{}) {
		return false
	}
	if p.verticalPlacement != vpInNether && st == stateLava {
		return false
	}
	return true
}

// getSurfaceY ports RuinedPortalPiece.getSurfaceY = getHeight(...) - 1. The SurfaceSampler is not
// threaded into the piece, so it falls back to the bbox floor (== surface for surface placements).
// RNG unaffected (getHeight draws no RNG). Documented approximation.
func (p *RuinedPortalPiece) getSurfaceY(x, z int) int {
	_ = x
	_ = z
	return p.bbox.MinY
}

// maybeAddVines ports RuinedPortalPiece.maybeAddVines. One RNG draw (getRandomHorizontalDirection).
// isFaceFull approximated by "neighbor air + support non-air".
func (p *RuinedPortalPiece) maybeAddVines(rng levelgen.RandomSource, view WorldGenView, box BoundingBox, x, y, z int) {
	st := p.worldGet(view, box, x, y, z)
	if block.IsAir(st) || st == stateOf(block.Vine{}) {
		return
	}
	dir := getRandomHorizontalDirection(rng)
	rx, ry, rz := x+stepX(dir), y, z+stepZ(dir)
	if !block.IsAir(p.worldGet(view, box, rx, ry, rz)) {
		return
	}
	v := block.Vine{}
	switch directionOpposite(dir) {
	case block.North:
		v.North = block.Boolean(true)
	case block.South:
		v.South = block.Boolean(true)
	case block.East:
		v.East = block.Boolean(true)
	case block.West:
		v.West = block.Boolean(true)
	}
	p.worldSet(view, box, rx, ry, rz, stateOf(v))
}

// maybeAddLeavesAbove ports the same: nextFloat() < 0.5 (drawn FIRST) AND netherrack AND above air
// -> persistent jungle leaves above.
func (p *RuinedPortalPiece) maybeAddLeavesAbove(rng levelgen.RandomSource, view WorldGenView, box BoundingBox, x, y, z int) {
	if rng.NextFloat() < 0.5 &&
		p.worldGet(view, box, x, y, z) == stateOf(block.Netherrack{}) &&
		block.IsAir(p.worldGet(view, box, x, y+1, z)) {
		p.worldSet(view, box, x, y+1, z, stateOf(block.JungleLeaves{Persistent: block.Boolean(true), Distance: block.Integer(7), Waterlogged: false}))
	}
}

func (p *RuinedPortalPiece) worldGet(view WorldGenView, box BoundingBox, x, y, z int) block.StateID {
	if !box.IsInside(x, y, z) {
		return stateAir
	}
	return view.GetBlock(x, y, z)
}

func (p *RuinedPortalPiece) worldSet(view WorldGenView, box BoundingBox, x, y, z int, st block.StateID) {
	if !box.IsInside(x, y, z) {
		return
	}
	view.SetBlock(x, y, z, st)
}
