package feature

// tree_placers.go ports the COMMON overworld trunk + foliage placers — the subset the
// embedded common-overworld `tree` configured_features reference (derived from the data,
// like Phase 12 derived its providers). Each extends the 13-01 TrunkPlacer/FoliagePlacer
// base (the pure setBlock/read callback seam — no placement/world import; this file imports
// only world/levelgen for RandomSource). It is PURE: the algorithm in feature/, the live
// Neighborhood wiring in world/feature_tree.go.
//
// DERIVED COMMON ROSTER (from the embedded configs, javap -c cited per class):
//   trunk placers:   forking (acacia), fancy (fancy_oak), dark_oak (dark_oak),
//                    giant (mega_pine/mega_spruce), mega_jungle (mega_jungle_tree)
//   foliage placers: acacia, spruce, pine, bush (jungle_bush), fancy (fancy_oak),
//                    dark_oak, jungle (jungle_foliage_placer = mega_jungle), mega_pine
// (straight_trunk + blob_foliage are 13-01; the special-biome cherry/bending/upwards/
// random_spread are 13-03.)
//
// The rng draw order per placer IS the determinism contract (T-13-05) — each placer is
// transcribed jar-exact and pinned by tree_placers_test.go vs an oracle. An algorithmic
// port, NOT a copy of Mojang source. No GPL paste.
//
// Sources (javap -c, temp/cache/26.2-inner.jar, net.minecraft.world.level.levelgen.feature):
//   trunkplacers.{ForkingTrunkPlacer, FancyTrunkPlacer, DarkOakTrunkPlacer,
//                 GiantTrunkPlacer, MegaJungleTrunkPlacer}.placeTrunk
//   foliageplacers.{AcaciaFoliagePlacer, SpruceFoliagePlacer, PineFoliagePlacer,
//                   BushFoliagePlacer, FancyFoliagePlacer, DarkOakFoliagePlacer,
//                   MegaJungleFoliagePlacer (id "jungle_foliage_placer"),
//                   MegaPineFoliagePlacer}.createFoliage / shouldSkipLocation

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// ---- horizontal-direction helper (Direction$Plane.HORIZONTAL.getRandomDirection) ----
//
// HORIZONTAL.getRandomDirection(rng) = HORIZONTAL.getRandom(rng) which indexes the 4
// horizontal directions [N(0),S(1),W(2),E(3) order in the Plane] via nextInt(4). We mirror
// vanilla's Direction.Plane.HORIZONTAL order: NORTH, EAST, SOUTH, WEST (the array order the
// Plane stores) — getRandom does values[nextInt(count)]. The exact step vectors:
//   NORTH (0,-1z), SOUTH (0,+1z), WEST (-1x,0), EAST (+1x,0).

// hdir is a horizontal direction step (stepX, stepZ).
type hdir struct{ sx, sz int }

// horizontalDirections is Direction.Plane.HORIZONTAL's ordering (NORTH, EAST, SOUTH, WEST)
// — the array getRandomDirection indexes via nextInt(4). Verified against Direction.Plane.
var horizontalDirections = []hdir{
	{0, -1}, // NORTH
	{1, 0},  // EAST
	{0, 1},  // SOUTH
	{-1, 0}, // WEST
}

// randomHorizontalDirection ports Direction.Plane.HORIZONTAL.getRandomDirection(rng):
// values[nextInt(4)]. ONE draw.
func randomHorizontalDirection(rng levelgen.RandomSource) hdir {
	return horizontalDirections[rng.NextIntN(4)]
}

// opposite returns the opposite horizontal direction (for the forking-trunk equality test).
func (d hdir) opposite() hdir { return hdir{-d.sx, -d.sz} }

// ============================================================================
// ForkingTrunkPlacer (acacia) — net.minecraft.....trunkplacers.ForkingTrunkPlacer
// ============================================================================

// ForkingTrunkPlacer is the swamp/acacia forking trunk: a vertical column with a single
// diagonal fork branch in a random horizontal direction, plus a possible second fork in a
// different direction. Returns a FoliageAttachment at the top of EACH placed branch tip.
type ForkingTrunkPlacer struct {
	trunkPlacerBase
}

// placeTrunk ports ForkingTrunkPlacer.placeTrunk. The draw order (javap -c):
//   d1  = getRandomDirection(rng)                      [nextInt(4)]
//   k   = freeHeight - nextInt(4) - 1                  [nextInt(4)]
//   l   = 3 - nextInt(3)                               [nextInt(3)]
//   (column-1 walk: from i=0..freeHeight-1, after i>=k && l>0 step x/z by d1, l--)
//   d2  = getRandomDirection(rng)                      [nextInt(4)]
//   if d2 != d1:
//     k2 = k - nextInt(2) - 1                          [nextInt(2)]
//     l2 = 1 + nextInt(3)                              [nextInt(3)]
//     (column-2 walk from i=k2..freeHeight-1, skip i<1, step x/z by d2)
func (p ForkingTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	setDirtAt(set, read, rng, origin.below(1), cfg)
	var atts []FoliageAttachment

	d1 := randomHorizontalDirection(rng)
	k := freeHeight - int(rng.NextIntN(4)) - 1
	l := 3 - int(rng.NextIntN(3))
	x := origin.X
	z := origin.Z
	var topY1 int
	have1 := false
	for i := 0; i < freeHeight; i++ {
		y := origin.Y + i
		if i >= k && l > 0 {
			x += d1.sx
			z += d1.sz
			l--
		}
		if placeLog(set, read, rng, TreePos{X: x, Y: y, Z: z}, cfg) {
			topY1 = y + 1
			have1 = true
		}
	}
	if have1 {
		atts = append(atts, FoliageAttachment{Pos: TreePos{X: x, Y: topY1, Z: z}, RadiusOffset: 1, DoubleTrunk: false})
	}

	x = origin.X
	z = origin.Z
	d2 := randomHorizontalDirection(rng)
	if d2 != d1 {
		k2 := k - int(rng.NextIntN(2)) - 1
		l2 := 1 + int(rng.NextIntN(3))
		var topY2 int
		have2 := false
		for i := k2; i < freeHeight && l2 > 0; i, l2 = i+1, l2-1 {
			if i < 1 {
				continue
			}
			y := origin.Y + i
			x += d2.sx
			z += d2.sz
			if placeLog(set, read, rng, TreePos{X: x, Y: y, Z: z}, cfg) {
				topY2 = y + 1
				have2 = true
			}
		}
		if have2 {
			atts = append(atts, FoliageAttachment{Pos: TreePos{X: x, Y: topY2, Z: z}, RadiusOffset: 0, DoubleTrunk: false})
		}
	}
	return atts
}

// ============================================================================
// DarkOakTrunkPlacer (dark_oak) — ....trunkplacers.DarkOakTrunkPlacer
// ============================================================================

// DarkOakTrunkPlacer is the 2x2 dark-oak trunk: a 2x2 column up to a height, leaning in a
// random direction near the top, with a flat 2x2 canopy base + a possible extra stub.
// REUSED by pale_oak in 13-03.
type DarkOakTrunkPlacer struct {
	trunkPlacerBase
}

// placeTrunk ports DarkOakTrunkPlacer.placeTrunk. Draw order (javap -c):
//   setDirtAt on the 4 below-trunk blocks (origin.below, +east, +south, +south+east)
//   d   = getRandomDirection(rng)                      [nextInt(4)]
//   m   = freeHeight - nextInt(4)                      [nextInt(4)]
//   n   = 2 - nextInt(3)                               [nextInt(3)]
//   (2x2 column walk: for i in 0..freeHeight-1, lean x/z by d when i>=m && n>0, n--;
//    if isAirOrLeaves at the lean pos place the 2x2 logs there)
//   FoliageAttachment at the lean top (radiusOffset 0, doubleTrunk true)
//   then the corner-stub loop: for j in -1..2, for k in -1..2 with the corner trim,
//     if nextInt(3) <= 0: { o = nextInt(3)+2; place a (j,top-1-p,k) log stub for p in 0..o;
//                            FoliageAttachment at (j, top, k) radiusOffset 0 doubleTrunk 0 }
func (p DarkOakTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	var atts []FoliageAttachment
	below := origin.below(1)
	setDirtAt(set, read, rng, below, cfg)
	setDirtAt(set, read, rng, below.offset(1, 0, 0), cfg)
	setDirtAt(set, read, rng, below.offset(0, 0, 1), cfg)
	setDirtAt(set, read, rng, below.offset(1, 0, 1), cfg)

	d := randomHorizontalDirection(rng)
	m := freeHeight - int(rng.NextIntN(4))
	n := 2 - int(rng.NextIntN(3))
	x := origin.X
	y := origin.Y
	z := origin.Z
	cx := x
	cz := z
	topY := origin.Y + freeHeight - 1
	for i := 0; i < freeHeight; i++ {
		if i >= m && n > 0 {
			cx += d.sx
			cz += d.sz
			n--
		}
		yy := y + i
		base := TreePos{X: cx, Y: yy, Z: cz}
		// TreeFeature.isAirOrLeaves gate on the 2x2 base before placing.
		if treePosFree(read, base) {
			placeLog(set, read, rng, base, cfg)
			placeLog(set, read, rng, base.offset(1, 0, 0), cfg)
			placeLog(set, read, rng, base.offset(0, 0, 1), cfg)
			placeLog(set, read, rng, base.offset(1, 0, 1), cfg)
		}
	}
	atts = append(atts, FoliageAttachment{Pos: TreePos{X: cx, Y: topY, Z: cz}, RadiusOffset: 0, DoubleTrunk: true})

	// The corner stubs: for j in [-1,2], k in [-1,2], skip the inner 2x2 (0<=j<=1 && 0<=k<=1)
	// and the 4 outer corners (|j|, |k| both 2-ish). On a pass nextInt(3)>0 -> skip; else a
	// stub of height nextInt(3)+2 down from topY.
	for j := -1; j <= 2; j++ {
		for k := -1; k <= 2; k++ {
			if j >= 0 && j <= 1 && k >= 0 && k <= 1 {
				continue
			}
			if rng.NextIntN(3) > 0 {
				continue
			}
			o := int(rng.NextIntN(3)) + 2
			for q := 0; q < o; q++ {
				placeLog(set, read, rng, TreePos{X: x + j, Y: topY - q - 1, Z: z + k}, cfg)
			}
			atts = append(atts, FoliageAttachment{Pos: TreePos{X: x + j, Y: topY, Z: z + k}, RadiusOffset: 0, DoubleTrunk: false})
		}
	}
	return atts
}

// ============================================================================
// GiantTrunkPlacer (mega_pine/mega_spruce) — ....trunkplacers.GiantTrunkPlacer
// ============================================================================

// GiantTrunkPlacer is the 2x2 mega trunk: a solid 2x2 column up to freeHeight, the inner
// (1,*),(*,1),(1,1) cells only filled below the top layer. Returns a SINGLE FoliageAttachment
// at origin.above(freeHeight) {radiusOffset 0, doubleTrunk true}. Base for MegaJungle.
type GiantTrunkPlacer struct {
	trunkPlacerBase
}

// placeTrunk ports GiantTrunkPlacer.placeTrunk (NO rng draws beyond the trunk providers).
// setDirtAt on the 4 below blocks, then for i in 0..freeHeight-1: place (0,i,0); and when
// i < freeHeight-1 also (1,i,0),(1,i,1),(0,i,1). FoliageAttachment at above(freeHeight).
func (p GiantTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	below := origin.below(1)
	setDirtAt(set, read, rng, below, cfg)
	setDirtAt(set, read, rng, below.offset(1, 0, 0), cfg)
	setDirtAt(set, read, rng, below.offset(0, 0, 1), cfg)
	setDirtAt(set, read, rng, below.offset(1, 0, 1), cfg)
	for i := 0; i < freeHeight; i++ {
		placeLogOffset(set, read, rng, origin, 0, i, 0, cfg)
		if i < freeHeight-1 {
			placeLogOffset(set, read, rng, origin, 1, i, 0, cfg)
			placeLogOffset(set, read, rng, origin, 1, i, 1, cfg)
			placeLogOffset(set, read, rng, origin, 0, i, 1, cfg)
		}
	}
	return []FoliageAttachment{{Pos: origin.above(freeHeight), RadiusOffset: 0, DoubleTrunk: true}}
}

// placeLogOffset ports GiantTrunkPlacer.placeLogIfFreeWithOffset: place a log at
// origin + (dx,dy,dz) via placeLog (the free gate + provider draw).
func placeLogOffset(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, origin TreePos, dx, dy, dz int, cfg *TreeConfiguration) {
	placeLog(set, read, rng, origin.offset(dx, dy, dz), cfg)
}

// ============================================================================
// MegaJungleTrunkPlacer (mega_jungle_tree) — ....trunkplacers.MegaJungleTrunkPlacer
// ============================================================================

// MegaJungleTrunkPlacer extends GiantTrunkPlacer: the 2x2 mega trunk + a set of diagonal
// branch arcs flung out at decreasing heights. Each branch tip yields a FoliageAttachment
// (radiusOffset -2, doubleTrunk false), plus the Giant's top attachment.
type MegaJungleTrunkPlacer struct {
	GiantTrunkPlacer
}

// placeTrunk ports MegaJungleTrunkPlacer.placeTrunk. Draw order (javap -c):
//   atts = super.placeTrunk (Giant, no draws)          -> [top attachment]
//   i = freeHeight - 2 - nextInt(4)                     [nextInt(4)]
//   while i > freeHeight/2:
//     f = nextFloat() * 2pi                             [nextFloat]
//     j = k = 0
//     for m in 0..4: { j = (1.5 + cos(f)*m); k = (1.5 + sin(f)*m);
//                      placeLog at origin + (j, i-3 + m/2, k) }
//     FoliageAttachment at origin + (j, i, k) {radiusOffset -2}
//     i -= 2 + nextInt(4)                                [nextInt(4)]
func (p MegaJungleTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	atts := p.GiantTrunkPlacer.placeTrunk(set, read, rng, freeHeight, origin, cfg)
	i := freeHeight - 2 - int(rng.NextIntN(4))
	for i > freeHeight/2 {
		f := rng.NextFloat() * 2.0 * float32(math.Pi)
		j := 0
		k := 0
		for m := 0; m < 5; m++ {
			j = int(float32(1.5) + float32(math.Cos(float64(f)))*float32(m))
			k = int(float32(1.5) + float32(math.Sin(float64(f)))*float32(m))
			placeLog(set, read, rng, origin.offset(j, i-3+m/2, k), cfg)
		}
		atts = append(atts, FoliageAttachment{Pos: origin.offset(j, i, k), RadiusOffset: -2, DoubleTrunk: false})
		i -= 2 + int(rng.NextIntN(4))
	}
	return atts
}

// ============================================================================
// FancyTrunkPlacer (fancy_oak / large oak) — ....trunkplacers.FancyTrunkPlacer
// ============================================================================

// FancyTrunkPlacer is the large-oak BigTreeFeature trunk: a tall central column with a ring
// of diagonal branch arcs whose tips become foliage clusters. The heaviest math.
type FancyTrunkPlacer struct {
	trunkPlacerBase
}

// fancyFoliageCoords mirrors FancyTrunkPlacer$FoliageCoords: a foliage attachment pos + the
// trunk Y the branch springs from (branchBase).
type fancyFoliageCoords struct {
	pos        TreePos
	branchBase int
}

// placeTrunk ports FancyTrunkPlacer.placeTrunk. Draw order (javap -c) — the branch placement
// loop draws nextFloat() twice per candidate branch (radius angle + theta). Transcribed
// jar-exact: the trunk column, then makeBranches over the surviving FoliageCoords.
func (p FancyTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	// j8 = freeHeight + 2  (the jar uses freeHeight as `i` and j8 = i + 2)
	totalHeight := freeHeight + 2
	trunkHeight := int(math.Floor(float64(totalHeight) * 0.618))
	setDirtAt(set, read, rng, origin.below(1), cfg)

	// clusters = min(1, floor(1.382 + pow(1.0*totalHeight/13.0, 2.0)))
	clusters := int(math.Min(1, math.Floor(1.382+math.Pow(1.0*float64(totalHeight)/13.0, 2.0))))
	maxY := origin.Y + trunkHeight // the cap on branch foliage Y
	branchTop := totalHeight - 5

	var coords []fancyFoliageCoords
	coords = append(coords, fancyFoliageCoords{pos: origin.above(branchTop), branchBase: maxY})

	for i := branchTop; i >= 0; i-- {
		shape := fancyTreeShape(totalHeight, i)
		if shape < 0 {
			continue
		}
		for cluster := 0; cluster < clusters; cluster++ {
			length := 1.0 * float64(shape) * (float64(rng.NextFloat()) + 0.328)
			theta := float64(rng.NextFloat()) * 2.0 * math.Pi
			dx := length*math.Sin(theta) + 0.5
			dz := length*math.Cos(theta) + 0.5
			branchPos := origin.offset(int(math.Floor(dx)), i-1, int(math.Floor(dz)))
			branchTip := branchPos.above(5)
			if p.makeLimb(set, read, rng, branchPos, branchTip, false, cfg) {
				ddx := origin.X - branchPos.X
				ddz := origin.Z - branchPos.Z
				foliageY := float64(branchPos.Y) - math.Sqrt(float64(ddx*ddx+ddz*ddz))*0.381
				var fy int
				if foliageY > float64(maxY) {
					fy = maxY
				} else {
					fy = int(foliageY)
				}
				clusterCenter := TreePos{X: origin.X, Y: fy, Z: origin.Z}
				if p.makeLimb(set, read, rng, clusterCenter, branchPos, false, cfg) {
					coords = append(coords, fancyFoliageCoords{pos: branchPos, branchBase: clusterCenter.Y})
				}
			}
		}
	}
	// The central trunk column from origin up to origin.above(trunkHeight).
	p.makeLimb(set, read, rng, origin, origin.above(trunkHeight), true, cfg)
	p.makeBranches(set, read, rng, totalHeight, origin, coords, cfg)

	// Collect the FoliageAttachments for the surviving (trimmable) FoliageCoords.
	var atts []FoliageAttachment
	for _, fc := range coords {
		if p.trimBranches(totalHeight, fc.branchBase-origin.Y) {
			atts = append(atts, FoliageAttachment{Pos: fc.pos, RadiusOffset: 0, DoubleTrunk: false})
		}
	}
	return atts
}

// fancyTreeShape ports FancyTrunkPlacer.treeShape: the branch-length envelope.
//
//	if step < height*0.3: return -1
//	r = height/2
//	q = r - step
//	res = sqrt(r*r - q*q) (or r when q==0)
//	if q != 0 && |q| >= r: return 0
//	return res * 0.5
func fancyTreeShape(height, step int) float32 {
	if float32(step) < float32(height)*0.3 {
		return -1.0
	}
	r := float32(height) / 2.0
	q := r - float32(step)
	res := float32(math.Sqrt(float64(r*r - q*q)))
	if q == 0 {
		res = r
	} else if float32(math.Abs(float64(q))) >= r {
		return 0.0
	}
	return res * 0.5
}

// trimBranches ports FancyTrunkPlacer.trimBranches: a branch at vertical `localY` survives
// iff localY >= height*0.2.
func (p FancyTrunkPlacer) trimBranches(height, localY int) bool {
	return float64(localY) >= float64(height)*0.2
}

// fancySteps ports FancyTrunkPlacer.getSteps: max(|dx|,|dy|,|dz|).
func fancySteps(d TreePos) int {
	ax := abs(d.X)
	ay := abs(d.Y)
	az := abs(d.Z)
	return maxInt(ax, maxInt(ay, az))
}

// makeLimb ports FancyTrunkPlacer.makeLimb: walk a straight line from `from` to `to` placing
// (or, when !place, only free-checking) logs. Returns true when the whole limb is free/placed.
// place=false is used as a pre-flight free check that returns at the first blocked cell.
func (p FancyTrunkPlacer) makeLimb(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, from, to TreePos, place bool, cfg *TreeConfiguration) bool {
	if !place && from == to {
		return true
	}
	delta := TreePos{X: to.X - from.X, Y: to.Y - from.Y, Z: to.Z - from.Z}
	steps := fancySteps(delta)
	fx := float32(delta.X) / float32(steps)
	fy := float32(delta.Y) / float32(steps)
	fz := float32(delta.Z) / float32(steps)
	for i := 0; i <= steps; i++ {
		pos := from.offset(
			int(math.Floor(float64(0.5+float32(i)*fx))),
			int(math.Floor(float64(0.5+float32(i)*fy))),
			int(math.Floor(float64(0.5+float32(i)*fz))),
		)
		if place {
			// The log axis (getLogAxis) is a cosmetic property we do not model on the StateID
			// here — the trunk_provider yields the oriented log; faithful placement of the
			// block is what matters for the deterministic footprint.
			placeLog(set, read, rng, pos, cfg)
		} else if !treePosFree(read, pos) {
			return false
		}
	}
	return true
}

// makeBranches ports FancyTrunkPlacer.makeBranches: for each FoliageCoords (skipping the
// central one whose pos == its attachment), draw a limb from the trunk Y up to the foliage.
func (p FancyTrunkPlacer) makeBranches(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, height int, origin TreePos, coords []fancyFoliageCoords, cfg *TreeConfiguration) {
	for _, fc := range coords {
		base := fc.branchBase
		trunkPos := TreePos{X: origin.X, Y: base, Z: origin.Z}
		if trunkPos == fc.pos {
			continue
		}
		if p.trimBranches(height, base-origin.Y) {
			p.makeLimb(set, read, rng, trunkPos, fc.pos, true, cfg)
		}
	}
}

// maxInt is a tiny int max (FancyTrunkPlacer getSteps / getLogAxis).
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ============================================================================
// FOLIAGE PLACERS
// ============================================================================

// AcaciaFoliagePlacer (acacia) — the flat 2-layer canopy + the offset-arm leaves.
type AcaciaFoliagePlacer struct {
	foliagePlacerBase
}

// foliageHeightOf ports AcaciaFoliagePlacer.foliageHeight = 0.
func (p AcaciaFoliagePlacer) foliageHeightOf(_ levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return 0
}

// createFoliage ports AcaciaFoliagePlacer.createFoliage: three rows around pos.above(offset):
//   row at radius (radius + radiusOffset),  localY = -1-foliageHeight
//   row at radius (radius - 1),             localY = -foliageHeight
//   row at radius (radius + radiusOffset-1),localY = 0
// (all with doubleTrunk as `large`).
func (p AcaciaFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	large := att.DoubleTrunk
	center := att.Pos.above(offset)
	placeLeavesRow(set, read, rng, cfg, center, foliageRadius+att.RadiusOffset, -1-foliageHeight, large, p.shouldSkip)
	placeLeavesRow(set, read, rng, cfg, center, foliageRadius-1, -foliageHeight, large, p.shouldSkip)
	placeLeavesRow(set, read, rng, cfg, center, foliageRadius+att.RadiusOffset-1, 0, large, p.shouldSkip)
}

// shouldSkip ports AcaciaFoliagePlacer.shouldSkipLocation (already-folded coords):
//   if !large: (localX>1 || localZ>1) && (localX!=0 && localZ!=0)
//   else:      localX==range && localZ==range && range>0
func (p AcaciaFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, _, lz, rangeR int, large bool) bool {
	if !large {
		return (lx > 1 || lz > 1) && lx != 0 && lz != 0
	}
	return lx == rangeR && lz == rangeR && rangeR > 0
}

// SpruceFoliagePlacer (spruce) — the tapered cone.
type SpruceFoliagePlacer struct {
	foliagePlacerBase
	trunkHeight intProvider
}

// foliageHeightOf ports SpruceFoliagePlacer.foliageHeight = max(4, treeHeight - trunkHeight.sample).
func (p SpruceFoliagePlacer) foliageHeightOf(rng levelgen.RandomSource, treeHeight int, _ *TreeConfiguration) int {
	th := p.trunkHeight.sample(rng)
	if treeHeight-th > 4 {
		return treeHeight - th
	}
	return 4
}

// createFoliage ports SpruceFoliagePlacer.createFoliage: the cone. Draw order (javap -c):
//   l = nextInt(2)                                      [nextInt(2)]
//   m = 1; n = 0
//   for k = offset down to -foliageHeight:
//     placeLeavesRow(pos, l, k, large)
//     if l >= m: { l = n; n = 1; m = min(m+1, radius + radiusOffset) }
//     else l++
func (p SpruceFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	pos := att.Pos
	l := int(rng.NextIntN(2))
	m := 1
	n := 0
	for k := offset; k >= -foliageHeight; k-- {
		// row.Y = pos.Y + k (vanilla setWithOffset). Sulfur bakes localY into center, so above(k).
		placeLeavesRow(set, read, rng, cfg, pos.above(k), l, k, att.DoubleTrunk, p.shouldSkip)
		if l >= m {
			l = n
			n = 1
			m = min2(m+1, foliageRadius+att.RadiusOffset)
		} else {
			l++
		}
	}
}

// shouldSkip ports SpruceFoliagePlacer.shouldSkipLocation: localX==range && localZ==range && range>0.
func (p SpruceFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, _, lz, rangeR int, _ bool) bool {
	return lx == rangeR && lz == rangeR && rangeR > 0
}

// PineFoliagePlacer (pine) — the pine tuft.
type PineFoliagePlacer struct {
	foliagePlacerBase
	height intProvider
}

// foliageRadiusOf ports PineFoliagePlacer.foliageRadius = super.foliageRadius + nextInt(max(depth+1,1)).
func (p PineFoliagePlacer) foliageRadiusOf(rng levelgen.RandomSource, depth int) int {
	base := p.radius.sample(rng)
	bound := depth + 1
	if bound < 1 {
		bound = 1
	}
	return base + int(rng.NextIntN(int32(bound)))
}

// foliageHeightOf ports PineFoliagePlacer.foliageHeight = height.sample.
func (p PineFoliagePlacer) foliageHeightOf(rng levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return p.height.sample(rng)
}

// createFoliage ports PineFoliagePlacer.createFoliage: a symmetric tuft. Draw order (javap):
//   k = 0
//   for l = offset down to (offset - foliageHeight):  [i.e. l from offset to offset-foliageHeight]
//     placeLeavesRow(pos, k, l, large)
//     if k >= 1 && l == offset - foliageHeight + 1: k--
//     else if k < radius + radiusOffset: k++
func (p PineFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	k := 0
	for l := offset; l >= offset-foliageHeight; l-- {
		// row.Y = pos.Y + l (vanilla setWithOffset). Sulfur bakes localY into center, so above(l).
		placeLeavesRow(set, read, rng, cfg, att.Pos.above(l), k, l, att.DoubleTrunk, p.shouldSkip)
		if k >= 1 && l == offset-foliageHeight+1 {
			k--
		} else if k < foliageRadius+att.RadiusOffset {
			k++
		}
	}
}

// shouldSkip ports PineFoliagePlacer.shouldSkipLocation: localX==range && localZ==range && range>0.
func (p PineFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, _, lz, rangeR int, _ bool) bool {
	return lx == rangeR && lz == rangeR && rangeR > 0
}

// BushFoliagePlacer (jungle_bush) — the small bush blob (extends Blob, own shouldSkip).
type BushFoliagePlacer struct {
	BlobFoliagePlacer
}

// createFoliage ports BushFoliagePlacer.createFoliage: rows from offset down to
// offset-foliageHeight, range = radius + radiusOffset - 1 - localY.
func (p BushFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	for k := offset; k >= offset-foliageHeight; k-- {
		rangeR := foliageRadius + att.RadiusOffset - 1 - k
		// row.Y = pos.Y + k (vanilla setWithOffset). Sulfur bakes localY into center, so above(k).
		placeLeavesRow(set, read, rng, cfg, att.Pos.above(k), rangeR, k, att.DoubleTrunk, p.shouldSkip)
	}
}

// shouldSkip ports BushFoliagePlacer.shouldSkipLocation: corner (lx==range && lz==range) ->
// skip with 50% chance (nextInt(2)==0 keeps). The jar: lx==range && lz==range && nextInt(2)==0.
func (p BushFoliagePlacer) shouldSkip(rng levelgen.RandomSource, lx, _, lz, rangeR int, _ bool) bool {
	return lx == rangeR && lz == rangeR && rng.NextIntN(2) == 0
}

// FancyFoliagePlacer (fancy_oak) — a blob-per-branch (extends Blob, own shouldSkip + rows).
type FancyFoliagePlacer struct {
	BlobFoliagePlacer
}

// createFoliage ports FancyFoliagePlacer.createFoliage: rows from offset down to
// offset-foliageHeight, range = (k==offset || k==offset-foliageHeight) ? radius : radius+1.
func (p FancyFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	for k := offset; k >= offset-foliageHeight; k-- {
		rangeR := foliageRadius + 1
		if k == offset || k == offset-foliageHeight {
			rangeR = foliageRadius
		}
		// row.Y = pos.Y + k (vanilla setWithOffset). Sulfur bakes localY into center, so above(k).
		placeLeavesRow(set, read, rng, cfg, att.Pos.above(k), rangeR, k, att.DoubleTrunk, p.shouldSkip)
	}
}

// shouldSkip ports FancyFoliagePlacer.shouldSkipLocation: a rounded-disc trim:
//   square(localX+0.5) + square(localZ+0.5) > range*range
func (p FancyFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, _, lz, rangeR int, _ bool) bool {
	fx := float32(lx) + 0.5
	fz := float32(lz) + 0.5
	return fx*fx+fz*fz > float32(rangeR*rangeR)
}

// DarkOakFoliagePlacer (dark_oak) — the wide flat canopy. REUSED by pale_oak in 13-03.
type DarkOakFoliagePlacer struct {
	foliagePlacerBase
}

// foliageHeightOf ports DarkOakFoliagePlacer.foliageHeight = 4.
func (p DarkOakFoliagePlacer) foliageHeightOf(_ levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return 4
}

// createFoliage ports DarkOakFoliagePlacer.createFoliage. Draw order (javap -c): a nextBoolean
// draw on the large path decides the extra +radius row. center = pos.above(offset) (the offset
// arg, NOT foliageHeight — verified via javap: `iload 9` (offset) is passed to BlockPos.above).
// Each row passes a SIGNED localY ∈ {-1,0,1,2} to vanilla placeLeavesRow, whose setWithOffset
// makes the row's world-Y = center.Y + localY. Sulfur's placeLeavesRowSigned flattens the row
// to dy=0 and ONLY forwards localY to the skip rule, so the per-row Y must be baked into the
// position passed in: center.above(localY) (above accepts negatives: above(-1) == below(1)).
// Without this every row collapsed onto center.Y (a flat 1-row canopy) — a second Y-mapping bug
// in the same family as the below()->above() sign fix.
//   large:
//     row (radius+2, -1, large)
//     row (radius+3,  0, large)
//     row (radius+2,  1, large)
//     if nextBoolean(): row (radius, 2, large)     [nextBoolean]
//   else:
//     row (radius+2, -1, large)
//     row (radius+1,  0, large)
func (p DarkOakFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	center := att.Pos.above(offset)
	large := att.DoubleTrunk
	if large {
		placeLeavesRowSigned(set, read, rng, cfg, center.above(-1), foliageRadius+2, -1, large, p.shouldSkipSigned)
		placeLeavesRowSigned(set, read, rng, cfg, center.above(0), foliageRadius+3, 0, large, p.shouldSkipSigned)
		placeLeavesRowSigned(set, read, rng, cfg, center.above(1), foliageRadius+2, 1, large, p.shouldSkipSigned)
		if rng.NextBoolean() {
			placeLeavesRowSigned(set, read, rng, cfg, center.above(2), foliageRadius, 2, large, p.shouldSkipSigned)
		}
	} else {
		placeLeavesRowSigned(set, read, rng, cfg, center.above(-1), foliageRadius+2, -1, large, p.shouldSkipSigned)
		placeLeavesRowSigned(set, read, rng, cfg, center.above(0), foliageRadius+1, 0, large, p.shouldSkipSigned)
	}
}

// shouldSkip ports DarkOakFoliagePlacer.shouldSkipLocationSigned: the SIGNED corner trim.
// NOTE DarkOak overrides shouldSkipLocationSigned (not shouldSkipLocation) — it needs the
// SIGNED dx/dz, so we route it through the signed seam (see placeLeavesRow wiring below).
func (p DarkOakFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, _, lz, rangeR int, _ bool) bool {
	// The unsigned fallback (shouldSkipLocation): localX==range && localZ==range && range>0.
	return lx == rangeR && lz == rangeR && rangeR > 0
}

// shouldSkipSigned ports DarkOakFoliagePlacer.shouldSkipLocationSigned: when localY==0 and
// large, skip the 4 corners (where |dx|==±range/!<range on both). Else defer to the base
// signed fold + shouldSkipLocation. The signed coords are dx/dz (NOT folded).
func (p DarkOakFoliagePlacer) shouldSkipSigned(rng levelgen.RandomSource, dx, localY, dz, rangeR int, large bool) bool {
	if localY == 0 && large {
		if (dx == -rangeR || dx >= rangeR) && (dz == -rangeR || dz >= rangeR) {
			return true
		}
	}
	return shouldSkipLocationSigned(p.shouldSkip, rng, dx, localY, dz, rangeR, large)
}

// JungleFoliagePlacer (id "jungle_foliage_placer" -> MegaJungleFoliagePlacer class), used by
// mega_jungle_tree. A simple shrinking dome.
type JungleFoliagePlacer struct {
	foliagePlacerBase
	height int
}

// foliageHeightOf ports MegaJungleFoliagePlacer.foliageHeight = height.
func (p JungleFoliagePlacer) foliageHeightOf(_ levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return p.height
}

// createFoliage ports MegaJungleFoliagePlacer.createFoliage. Draw order (javap -c):
//   l = large ? foliageHeight : 1 + nextInt(2)        [nextInt(2) when !large]
//   for k = offset down to (offset - l):
//     rangeR = radius + radiusOffset + 1 - k
//     placeLeavesRow(pos, rangeR, k, large)
func (p JungleFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	var l int
	if att.DoubleTrunk {
		l = foliageHeight
	} else {
		l = 1 + int(rng.NextIntN(2))
	}
	for k := offset; k >= offset-l; k-- {
		rangeR := foliageRadius + att.RadiusOffset + 1 - k
		// row.Y = pos.Y + k (vanilla setWithOffset). Sulfur bakes localY into center, so above(k).
		placeLeavesRow(set, read, rng, cfg, att.Pos.above(k), rangeR, k, att.DoubleTrunk, p.shouldSkip)
	}
}

// shouldSkip ports MegaJungleFoliagePlacer.shouldSkipLocation: a sphere-ish trim:
//   localX + localY >= 7 -> skip; else localX*localX + localY*localY > localZ*localZ.
// NOTE the args are (localX=lx, localY=localY, localZ=range) folded — matching the jar's
// (localX, localY, localZ, range, large) where it uses localX, range, localZ as x,y,z.
func (p JungleFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, localY, lz, rangeR int, _ bool) bool {
	if lx+rangeR >= 7 {
		return true
	}
	return lx*lx+rangeR*rangeR > lz*lz
}

// MegaPineFoliagePlacer (mega_pine/mega_spruce) — the mega-pine cap.
type MegaPineFoliagePlacer struct {
	foliagePlacerBase
	crownHeight intProvider
}

// foliageHeightOf ports MegaPineFoliagePlacer.foliageHeight = crownHeight.sample.
func (p MegaPineFoliagePlacer) foliageHeightOf(rng levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return p.crownHeight.sample(rng)
}

// createFoliage ports MegaPineFoliagePlacer.createFoliage: an expanding-then-jittered cone.
// NO rng draws in the loop (the cap shape is deterministic given foliageHeight/radius).
//   pos = att.pos; m = 0
//   for yy = pos.Y - foliageHeight + offset .. pos.Y + offset:
//     n = yy - (pos.Y - foliageHeight + offset)... actually: n = pos.Y - yy (the depth)
//     wait: o = radius + radiusOffset + floor((n/foliageHeight)*3.5)
//     p2 = (n>0 && o==m && (yy & 1)==0) ? o+1 : o
//     placeLeavesRow(at (pos.X, yy, pos.Z), p2, 0, large)
//     m = o
func (p MegaPineFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	pos := att.Pos
	m := 0
	startY := pos.Y - foliageHeight + offset
	endY := pos.Y + offset
	for yy := startY; yy <= endY; yy++ {
		n := pos.Y - yy
		o := foliageRadius + att.RadiusOffset + int(math.Floor(float64(float32(n)/float32(foliageHeight)*3.5)))
		var p2 int
		if n > 0 && o == m && (yy&1) == 0 {
			p2 = o + 1
		} else {
			p2 = o
		}
		placeLeavesRow(set, read, rng, cfg, TreePos{X: pos.X, Y: yy, Z: pos.Z}, p2, 0, att.DoubleTrunk, p.shouldSkip)
		m = o
	}
}

// shouldSkip ports MegaPineFoliagePlacer.shouldSkipLocation: localX + localZ >= 7 -> skip;
// else localX*localX + localZ*localZ > range*range.
func (p MegaPineFoliagePlacer) shouldSkip(_ levelgen.RandomSource, lx, _, lz, rangeR int, _ bool) bool {
	if lx+lz >= 7 {
		return true
	}
	return lx*lx+lz*lz > rangeR*rangeR
}

// compile-time interface assertions for the new placers.
var (
	_ TrunkPlacer = ForkingTrunkPlacer{}
	_ TrunkPlacer = DarkOakTrunkPlacer{}
	_ TrunkPlacer = GiantTrunkPlacer{}
	_ TrunkPlacer = MegaJungleTrunkPlacer{}
	_ TrunkPlacer = FancyTrunkPlacer{}

	_ FoliagePlacer = AcaciaFoliagePlacer{}
	_ FoliagePlacer = SpruceFoliagePlacer{}
	_ FoliagePlacer = PineFoliagePlacer{}
	_ FoliagePlacer = BushFoliagePlacer{}
	_ FoliagePlacer = FancyFoliagePlacer{}
	_ FoliagePlacer = DarkOakFoliagePlacer{}
	_ FoliagePlacer = JungleFoliagePlacer{}
	_ FoliagePlacer = MegaPineFoliagePlacer{}
)
