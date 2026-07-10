package world

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("end_platform", endPlatformBody)
	registerFeatureBody("void_start_platform", voidStartPlatformBody)
	registerFeatureBody("end_island", endIslandBody)
	registerFeatureBody("end_gateway", endGatewayBody)
	registerFeatureBody("chorus_plant", chorusPlantBody)
	registerFeatureBody("spike", spikeBody)
	registerFeatureBody("end_spike", endSpikeBody)
}

var (
	obsidianID    = block.ToStateID[block.Obsidian{}]
	endStoneID    = block.ToStateID[block.EndStone{}]
	cobblestoneID = block.ToStateID[block.Cobblestone{}]
	stoneID       = block.ToStateID[block.Stone{}]
	bedrockID     = block.ToStateID[block.Bedrock{}]
	endGatewayID  = block.ToStateID[block.EndGateway{}]
	chorusFlower5 = chorusFlowerState(5)
)

// endPlatformBody ports EndPlatformFeature.createEndPlatform(level, origin, false):
// loop nest (javap) dz -2..2, dx -2..2, dy -1..2; mutable set to origin then
// move(dx, dy, dz). OBSIDIAN when dy==-1, else AIR. A cell is written only when the
// existing block is NOT already that block; drop=false so no destroyBlock.
func endPlatformBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	_ levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			for dy := -1; dy <= 2; dy++ {
				p := placement.BlockPos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
				var st block.StateID
				if dy == -1 {
					st = obsidianID
				} else {
					st = bctx.airState()
				}
				if bctx.getState(p) != st {
					bctx.placeState(p, st)
				}
			}
		}
	}
	return true
}

// checkerboardDistance ports VoidStartPlatformFeature.checkerboardDistance:
// max(abs(x0-x1), abs(z0-z1)).
func checkerboardDistance(x0, z0, x1, z1 int) int {
	dx := absInt(x0 - x1)
	dz := absInt(z0 - z1)
	if dx > dz {
		return dx
	}
	return dz
}

// voidStartPlatformBody ports VoidStartPlatformFeature.place. PLATFORM_OFFSET = (8,3,8),
// origin chunk = (0,0). Acts only on chunks within checkerboardDistance 1 of (0,0). Center
// = (8, origin.Y+3, 8). Iterates the current chunk columns at y=center.Y, skipping columns
// more than 16 (Chebyshev) from center; the exact center gets COBBLESTONE, others STONE.
func voidStartPlatformBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	_ levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	chunkX := pos.X >> 4
	chunkZ := pos.Z >> 4
	if checkerboardDistance(chunkX, chunkZ, 0, 0) > 1 {
		return true
	}
	centerX, centerY, centerZ := 8, pos.Y+3, 8
	minBlockX := chunkX << 4
	minBlockZ := chunkZ << 4
	for z := minBlockZ; z <= minBlockZ+15; z++ {
		for x := minBlockX; x <= minBlockX+15; x++ {
			if checkerboardDistance(centerX, centerZ, x, z) > 16 {
				continue
			}
			p := placement.BlockPos{X: x, Y: centerY, Z: z}
			if x == centerX && z == centerZ {
				bctx.placeState(p, cobblestoneID)
			} else {
				bctx.placeState(p, stoneID)
			}
		}
	}
	return true
}

// endIslandBody ports EndIslandFeature.place: f = nextInt(3) + 4.0; descend layers
// i = 0, -1, -2, ... while f > 0.5; each layer fills the disc x,z in [-floor(f), ceil(f)]
// where x*x+z*z <= (f+1)*(f+1) with END_STONE at origin.offset(x, i, z); then
// f -= nextInt(2) + 0.5.
func endIslandBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	f := float32(rng.NextIntN(3)) + 4.0
	for i := 0; f > 0.5; i-- {
		lo := mthFloorF(-f)
		hi := mthCeilF(f)
		for x := lo; x <= hi; x++ {
			for z := lo; z <= hi; z++ {
				if float32(x*x+z*z) <= (f+1.0)*(f+1.0) {
					bctx.placeState(placement.BlockPos{X: pos.X + x, Y: pos.Y + i, Z: pos.Z + z}, endStoneID)
				}
			}
		}
		f -= float32(rng.NextIntN(2)) + 0.5
	}
	return true
}

// endGatewayConfig is EndGatewayConfiguration {exact, exit?} (only the deferred BE exit).
type endGatewayConfig struct {
	Exact bool  `json:"exact"`
	Exit  []int `json:"exit"`
}

var endGatewayCache sync.Map

func decodeEndGateway(cf *feature.ConfiguredFeature) *endGatewayConfig {
	if v, ok := endGatewayCache.Load(cf); ok {
		return v.(*endGatewayConfig)
	}
	cfg := &endGatewayConfig{}
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, cfg); err != nil {
			panic(fmt.Errorf("world: end_gateway config %q: %w", cf.ID, err))
		}
	}
	endGatewayCache.Store(cf, cfg)
	return cfg
}

// endGatewayBody ports EndGatewayFeature.place: iterate the box origin.offset(-1,-2,-1)..
// (1,2,1). xc/yc/zc = coordinate matches origin; ymid = abs(y-origin.y)==2. Writes:
// xc&&yc&&zc -> END_GATEWAY; else yc -> AIR; else ymid&&xc&&zc -> BEDROCK; else !xc&&!zc ->
// AIR; else (xc||zc): ymid?AIR:BEDROCK.
//
// DEFERRED: on the center gateway block vanilla sets a TheEndGatewayBlockEntity exit. Block
// entities are not wired into the worldgen view, so the block IS placed but the BE write is
// a TODO. No block placement changes.
func endGatewayBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	_ levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	_ = decodeEndGateway(cf)
	for x := pos.X - 1; x <= pos.X+1; x++ {
		for y := pos.Y - 2; y <= pos.Y+2; y++ {
			for z := pos.Z - 1; z <= pos.Z+1; z++ {
				xc := x == pos.X
				yc := y == pos.Y
				zc := z == pos.Z
				ymid := absInt(y-pos.Y) == 2
				p := placement.BlockPos{X: x, Y: y, Z: z}
				switch {
				case xc && yc && zc:
					bctx.placeState(p, endGatewayID)
				case yc:
					bctx.placeState(p, bctx.airState())
				case ymid && xc && zc:
					bctx.placeState(p, bedrockID)
				case !xc && !zc:
					bctx.placeState(p, bctx.airState())
				default:
					if ymid {
						bctx.placeState(p, bctx.airState())
					} else {
						bctx.placeState(p, bedrockID)
					}
				}
			}
		}
	}
	return true
}

// chorusPlantBody ports ChorusPlantFeature.place: origin empty and below in
// supports_chorus_plant tag -> generatePlant(origin, 8), return true.
func chorusPlantBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	if !block.IsAir(bctx.getState(pos)) {
		return false
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if !supportsChorusPlant(below) {
		return false
	}
	chorusGeneratePlant(bctx, rng, pos, 8)
	return true
}

// chorusGeneratePlant ports ChorusFlowerBlock.generatePlant: place CHORUS_PLANT (with
// connections) at pos, then growTreeRecursive(pos, rng, pos, height, 0).
func chorusGeneratePlant(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos, height int) {
	bctx.placeState(pos, chorusStateWithConnections(bctx, pos))
	chorusGrowTreeRecursive(bctx, rng, pos, pos, height, 0)
}

// chorusGrowTreeRecursive ports ChorusFlowerBlock.growTreeRecursive(pos, rng, origin,
// size, iterations). branchHeight = nextInt(4)+1 (++ when iterations==0). Build a vertical
// stem of that many CHORUS_PLANT (each with connections + a connection refresh on the node
// below), stopping if a node horizontal neighbours are not all empty. Then, while
// iterations<4, branchCount = nextInt(4) (++ when iterations==0); for each branch pick a
// random horizontal direction (faces[nextInt(4)] over N,E,S,W), target =
// pos.above(branchHeight).relative(dir); if target and target.below() are empty, target
// neighbours excluding dir.opposite are empty, and abs(dx)<size and abs(dz)<size of origin:
// place two CHORUS_PLANT (target + target.relative(dir.opposite)) and recurse. Finally, if
// no branch taken, place CHORUS_FLOWER age=5 at pos.above(branchHeight).
func chorusGrowTreeRecursive(bctx *bodyContext, rng levelgen.RandomSource, pos, origin placement.BlockPos, size, iterations int) {
	branchHeight := int(rng.NextIntN(4)) + 1
	if iterations == 0 {
		branchHeight++
	}
	for j := 0; j < branchHeight; j++ {
		node := placement.BlockPos{X: pos.X, Y: pos.Y + j + 1, Z: pos.Z}
		if !chorusAllNeighborsEmpty(bctx, node, chorusNoExclude) {
			return
		}
		bctx.placeState(node, chorusStateWithConnections(bctx, node))
		below := placement.BlockPos{X: node.X, Y: node.Y - 1, Z: node.Z}
		bctx.placeState(below, chorusStateWithConnections(bctx, below))
	}

	branched := false
	if iterations < 4 {
		branchCount := int(rng.NextIntN(4))
		if iterations == 0 {
			branchCount++
		}
		for k := 0; k < branchCount; k++ {
			dir := chorusRandomHorizontalDirection(rng)
			target := chorusRelative(placement.BlockPos{X: pos.X, Y: pos.Y + branchHeight, Z: pos.Z}, dir)
			if absInt(target.X-origin.X) < size && absInt(target.Z-origin.Z) < size &&
				block.IsAir(bctx.getState(target)) &&
				block.IsAir(bctx.getState(placement.BlockPos{X: target.X, Y: target.Y - 1, Z: target.Z})) &&
				chorusAllNeighborsEmpty(bctx, target, chorusOpposite(dir)) {
				branched = true
				bctx.placeState(target, chorusStateWithConnections(bctx, target))
				back := chorusRelative(target, chorusOpposite(dir))
				bctx.placeState(back, chorusStateWithConnections(bctx, back))
				chorusGrowTreeRecursive(bctx, rng, target, origin, size, iterations+1)
			}
		}
	}

	if !branched {
		flower := placement.BlockPos{X: pos.X, Y: pos.Y + branchHeight, Z: pos.Z}
		bctx.placeState(flower, chorusFlower5)
	}
}

// chorusNoExclude is the allNeighborsEmpty "no excluded direction" sentinel (Java null).
var chorusNoExclude = horizontalDir{dx: 99, dz: 99}

// chorusAllNeighborsEmpty ports ChorusFlowerBlock.allNeighborsEmpty(pos, excluded): for
// each HORIZONTAL direction (shared horizontalDirections, vanilla N,E,S,W order) except
// excluded, if the relative block is not empty return false.
func chorusAllNeighborsEmpty(bctx *bodyContext, pos placement.BlockPos, excluded horizontalDir) bool {
	for _, d := range horizontalDirections {
		if d == excluded {
			continue
		}
		if !block.IsAir(bctx.getState(chorusRelative(pos, d))) {
			return false
		}
	}
	return true
}

// chorusRandomHorizontalDirection ports Plane.HORIZONTAL.getRandomDirection:
// horizontalDirections[nextInt(4)] (vanilla N,E,S,W order).
func chorusRandomHorizontalDirection(rng levelgen.RandomSource) horizontalDir {
	return horizontalDirections[int(rng.NextIntN(4))]
}

// chorusRelative ports BlockPos.relative(direction) for a horizontal direction.
func chorusRelative(p placement.BlockPos, d horizontalDir) placement.BlockPos {
	return placement.BlockPos{X: p.X + d.dx, Y: p.Y, Z: p.Z + d.dz}
}

// chorusOpposite ports Direction.getOpposite() for a horizontal direction.
func chorusOpposite(d horizontalDir) horizontalDir {
	return horizontalDir{dx: -d.dx, dz: -d.dz}
}

// chorusStateWithConnections ports ChorusPlantBlock.getStateWithConnections: each of the
// six connection booleans is true iff the neighbour is CHORUS_PLANT or CHORUS_FLOWER; DOWN
// is ALSO true when the neighbour below is in supports_chorus_plant.
func chorusStateWithConnections(bctx *bodyContext, pos placement.BlockPos) block.StateID {
	down := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	up := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
	north := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y, Z: pos.Z - 1})
	east := bctx.getState(placement.BlockPos{X: pos.X + 1, Y: pos.Y, Z: pos.Z})
	south := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y, Z: pos.Z + 1})
	west := bctx.getState(placement.BlockPos{X: pos.X - 1, Y: pos.Y, Z: pos.Z})

	st := block.ChorusPlant{
		Down:  block.Boolean(chorusConnects(down) || supportsChorusPlant(down)),
		Up:    block.Boolean(chorusConnects(up)),
		North: block.Boolean(chorusConnects(north)),
		East:  block.Boolean(chorusConnects(east)),
		South: block.Boolean(chorusConnects(south)),
		West:  block.Boolean(chorusConnects(west)),
	}
	if sid, ok := block.ToStateID[st]; ok {
		return sid
	}
	return block.ToStateID[block.ChorusPlant{}]
}

// chorusConnects reports state.is(CHORUS_PLANT) || state.is(CHORUS_FLOWER).
func chorusConnects(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	switch block.StateList[st].(type) {
	case block.ChorusPlant, block.ChorusFlower:
		return true
	default:
		return false
	}
}

// chorusFlowerState returns the CHORUS_FLOWER state at age (fallback: default).
func chorusFlowerState(age int) block.StateID {
	if sid, ok := block.ToStateID[block.ChorusFlower{Age: block.Integer(age)}]; ok {
		return sid
	}
	return block.ToStateID[block.ChorusFlower{}]
}

// supportsChorusPlant resolves supports_chorus_plant (end_stone in vanilla) from the
// embedded jar tag JSON via data.BlockTag; membership is authoritative.
var (
	supportsChorusPlantOnce sync.Once
	supportsChorusPlantSet  map[block.StateID]bool
)

func supportsChorusPlant(st block.StateID) bool {
	supportsChorusPlantOnce.Do(func() {
		ids, err := data.BlockTag("supports_chorus_plant")
		if err != nil {
			panic(fmt.Errorf("world: resolving supports_chorus_plant tag: %w", err))
		}
		set := make(map[block.StateID]bool)
		for sid, b := range block.StateList {
			if ids[b.ID()] {
				set[block.StateID(sid)] = true
			}
		}
		supportsChorusPlantSet = set
	})
	return supportsChorusPlantSet[st]
}

// spikeConfig is SpikeConfiguration {state, can_place_on, can_replace}; only state is used.
type spikeConfig struct {
	State json.RawMessage `json:"state"`
}

type spikeDecoded struct {
	state block.StateID
}

var spikeCache sync.Map

func decodeSpike(cf *feature.ConfiguredFeature) *spikeDecoded {
	if v, ok := spikeCache.Load(cf); ok {
		return v.(*spikeDecoded)
	}
	d := &spikeDecoded{state: obsidianID}
	if r := configRaw(cf); len(r) > 0 {
		var raw spikeConfig
		if err := json.Unmarshal(r, &raw); err == nil && len(raw.State) > 0 {
			if sid, err := feature.ResolveBlockStateJSON(raw.State); err == nil {
				d.state = sid
			}
		}
	}
	spikeCache.Store(cf, d)
	return d
}

// spikeBody ports SpikeFeature.place. SpikeConfiguration carries opaque BlockPredicate
// can_place_on / can_replace whose predicate engine is not yet ported, and no vanilla
// configured feature exercises the spike type (vanilla end_spike uses
// EndSpikeConfiguration). The numeric spike geometry follows the EXACT RNG draw order; the
// config state is the placed block, can_place_on degrades to permissive (require non-air
// landing), and can_replace degrades to air-only (the common End case).
func spikeBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg := decodeSpike(cf)
	origin := pos
	for block.IsAir(bctx.getState(origin)) && origin.Y > minBuildY(bctx, ctx)+2 {
		origin.Y--
	}
	if block.IsAir(bctx.getState(origin)) {
		return false
	}
	state := cfg.state
	origin.Y += int(rng.NextIntN(4))
	height := int(rng.NextIntN(4)) + 7
	width := height/4 + int(rng.NextIntN(2))
	if width > 1 && rng.NextIntN(60) == 0 {
		origin.Y += 10 + int(rng.NextIntN(30))
	}
	for l := 0; l < height; l++ {
		g := (1.0 - float32(l)/float32(height)) * float32(width)
		m := mthCeilF(g)
		for n := -m; n <= m; n++ {
			pv := float32(absInt(n)) - 0.25
			for o := -m; o <= m; o++ {
				qv := float32(absInt(o)) - 0.25
				if (n != 0 || o != 0) && pv*pv+qv*qv > g*g {
					continue
				}
				if (n != -m && n != m && o != -m && o != m) || rng.NextFloat() <= 0.75 {
					here := placement.BlockPos{X: origin.X + n, Y: origin.Y + l, Z: origin.Z + o}
					if spikeReplaceable(bctx, here) {
						bctx.placeState(here, state)
					}
					if l != 0 && m > 1 {
						mirror := placement.BlockPos{X: origin.X + n, Y: origin.Y - l, Z: origin.Z + o}
						if spikeReplaceable(bctx, mirror) {
							bctx.placeState(mirror, state)
						}
					}
				}
			}
		}
	}
	w := width - 1
	if w < 0 {
		w = 0
	} else if w > 1 {
		w = 1
	}
	for n := -w; n <= w; n++ {
		for o := -w; o <= w; o++ {
			cur := placement.BlockPos{X: origin.X + n, Y: origin.Y - 1, Z: origin.Z + o}
			budget := 50
			if absInt(n) == 1 && absInt(o) == 1 {
				budget = int(rng.NextIntN(5))
			}
			for cur.Y > 50 {
				if !(spikeReplaceable(bctx, cur) || bctx.getState(cur) == state) {
					break
				}
				bctx.placeState(cur, state)
				cur.Y--
				budget--
				if budget <= 0 {
					cur.Y -= int(rng.NextIntN(5)) + 1
					budget = int(rng.NextIntN(5))
				}
			}
		}
	}
	return true
}

// spikeReplaceable is the air-only can_replace fallback.
func spikeReplaceable(bctx *bodyContext, pos placement.BlockPos) bool {
	return block.IsAir(bctx.getState(pos))
}

// endSpike is EndSpikeFeature.EndSpike.
type endSpike struct {
	centerX int
	centerZ int
	radius  int
	height  int
	guarded bool
}

// isCenterWithinChunk ports EndSpike.isCenterWithinChunk: center section coords (>>4)
// equal the origin section coords.
func (s endSpike) isCenterWithinChunk(pos placement.BlockPos) bool {
	return (pos.X>>4) == (s.centerX>>4) && (pos.Z>>4) == (s.centerZ>>4)
}

// getSpikesForLevel ports EndSpikeFeature.getSpikesForLevel: Random(worldSeed);
// mask = nextLong() & 65535; load the cached layout for mask.
func getSpikesForLevel(worldSeed int64) []endSpike {
	r := levelgen.NewLegacyRandomSource(worldSeed)
	mask := r.NextLong() & 65535
	return spikeCacheLoad(mask)
}

// spikeCacheLoad ports EndSpikeFeature.SpikeCacheLoader.load(mask): shuffle 0..9 with
// Random(mask), then for i 0..9: angle = 2*(-PI + 0.3141592653589793*i);
// centerX = floor(42*cos(angle)); centerZ = floor(42*sin(angle)); pick = shuffled[i];
// radius = 2 + pick/3; height = 76 + pick*3; guarded = pick==1 || pick==2.
func spikeCacheLoad(mask int64) []endSpike {
	r := levelgen.NewLegacyRandomSource(mask)
	shuffled := shuffledInts10(r)
	spikes := make([]endSpike, 0, 10)
	for i := 0; i < 10; i++ {
		angle := 2.0 * (-math.Pi + 0.3141592653589793*float64(i))
		centerX := mthFloorD(42.0 * math.Cos(angle))
		centerZ := mthFloorD(42.0 * math.Sin(angle))
		pick := shuffled[i]
		spikes = append(spikes, endSpike{
			centerX: centerX,
			centerZ: centerZ,
			radius:  2 + pick/3,
			height:  76 + pick*3,
			guarded: pick == 1 || pick == 2,
		})
	}
	return spikes
}

// shuffledInts10 ports Util.toShuffledList(IntStream.range(0,10), rng): Fisher-Yates --
// for i = 10 down to 2: j = nextInt(i); swap list[i-1] and list[j].
func shuffledInts10(rng levelgen.RandomSource) [10]int {
	var a [10]int
	for i := range a {
		a[i] = i
	}
	for i := 10; i > 1; i-- {
		j := int(rng.NextIntN(int32(i)))
		a[i-1], a[j] = a[j], a[i-1]
	}
	return a
}

// endSpikeBody ports EndSpikeFeature.place: for the vanilla empty spikes list, derive the
// layout from getSpikesForLevel(worldSeed); for each spike whose center is within this
// chunk, placeEndSpike. Returns true.
func endSpikeBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	spikes := decodeEndSpikeSpikes(cf)
	if len(spikes) == 0 {
		spikes = getSpikesForLevel(bctx.seed)
	}
	for _, s := range spikes {
		if s.isCenterWithinChunk(pos) {
			placeEndSpike(bctx, ctx, rng, s)
		}
	}
	return true
}

// placeEndSpike ports EndSpikeFeature.placeSpike: fill the obsidian pillar (below height)
// and clear air (above y 65) inside the box (cx-r, minY, cz-r)..(cx+r, height+10, cz+r);
// then when guarded build the 5x5x4 iron-bars cage around the top.
//
// DEFERRED: the END_CRYSTAL entity spawn (beam target, invulnerability, rotation) + bedrock
// below it + fire at its position. Entities/block-entities are not wired into the worldgen
// view. The crystal path draws ONE nextFloat() (spawn rotation); that single float IS drawn
// here to keep the world RNG stream identical for anything downstream. No block placement is
// affected.
func placeEndSpike(bctx *bodyContext, ctx placement.PlacementContext, rng levelgen.RandomSource, s endSpike) {
	radius := s.radius
	minY := minBuildY(bctx, ctx)
	for x := s.centerX - radius; x <= s.centerX+radius; x++ {
		for y := minY; y <= s.height+10; y++ {
			for z := s.centerZ - radius; z <= s.centerZ+radius; z++ {
				dx := float64(x - s.centerX)
				dz := float64(z - s.centerZ)
				distSqr := dx*dx + dz*dz
				if distSqr <= float64(radius*radius+1) && y < s.height {
					bctx.placeState(placement.BlockPos{X: x, Y: y, Z: z}, obsidianID)
				} else if y > 65 {
					bctx.placeState(placement.BlockPos{X: x, Y: y, Z: z}, bctx.airState())
				}
			}
		}
	}

	if s.guarded {
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				for dy := 0; dy <= 3; dy++ {
					flag := absInt(dx) == 2
					flag1 := absInt(dz) == 2
					flag2 := dy == 3
					if !flag && !flag1 && !flag2 {
						continue
					}
					st := ironBarsCageState(dx, dz, dy)
					bctx.placeState(placement.BlockPos{X: s.centerX + dx, Y: s.height + dy, Z: s.centerZ + dz}, st)
				}
			}
		}
	}

	_ = rng.NextFloat()
}

// ironBarsCageState ports the per-cell IRON_BARS state in placeSpike:
// var16 = abs(dx)==2 || dy==3 ; var17 = abs(dz)==2 || dy==3 ;
// north = var16 && dz != -2 ; south = var16 && dz != 2 ;
// west = var17 && dx != -2 ; east = var17 && dx != 2.
func ironBarsCageState(dx, dz, dy int) block.StateID {
	var16 := absInt(dx) == 2 || dy == 3
	var17 := absInt(dz) == 2 || dy == 3
	st := block.IronBars{
		North: block.Boolean(var16 && dz != -2),
		South: block.Boolean(var16 && dz != 2),
		West:  block.Boolean(var17 && dx != -2),
		East:  block.Boolean(var17 && dx != 2),
	}
	if sid, ok := block.ToStateID[st]; ok {
		return sid
	}
	return block.ToStateID[block.IronBars{}]
}

// decodeEndSpikeSpikes returns the config spike list (vanilla end_spike -> nil).
func decodeEndSpikeSpikes(cf *feature.ConfiguredFeature) []endSpike {
	raw := configRaw(cf)
	if len(raw) == 0 {
		return nil
	}
	var cfg struct {
		Spikes []struct {
			CenterX int  `json:"centerX"`
			CenterZ int  `json:"centerZ"`
			Radius  int  `json:"radius"`
			Height  int  `json:"height"`
			Guarded bool `json:"guarded"`
		} `json:"spikes"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Spikes) == 0 {
		return nil
	}
	out := make([]endSpike, 0, len(cfg.Spikes))
	for _, s := range cfg.Spikes {
		out = append(out, endSpike{centerX: s.CenterX, centerZ: s.CenterZ, radius: s.Radius, height: s.Height, guarded: s.Guarded})
	}
	return out
}

// mthFloorF ports Mth.floor(float).
func mthFloorF(f float32) int { return int(math.Floor(float64(f))) }

// mthCeilF ports Mth.ceil(float).
func mthCeilF(f float32) int { return int(math.Ceil(float64(f))) }

// mthFloorD ports Mth.floor(double).
func mthFloorD(d float64) int { return int(math.Floor(d)) }

// absInt ports Math.abs(int).
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
