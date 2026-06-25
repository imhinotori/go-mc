package feature

// tree_special.go ports the SPECIAL-biome trees the four extra generatable biomes need —
// cherry_grove (cherry), mangrove_swamp (mangrove + the RootPlacer subsystem), lush_caves
// (azalea), pale_garden (pale_oak) — on the 13-01 TrunkPlacer/FoliagePlacer/RootPlacer bases
// (the pure setBlock/read callback seam; this file imports only world/levelgen for
// RandomSource — NOT placement, NOT world; acyclic, exactly like tree_placers.go). It is
// PURE: the algorithm in feature/, the live Neighborhood wiring in world/feature_tree.go.
//
// THE SPECIAL ROSTER (verified on disk against the embedded configs):
//   trunk placers:   cherry (cherry_grove), upwards_branching (mangrove), bending (azalea)
//                    (pale_oak REUSES dark_oak_trunk_placer from 13-02)
//   foliage placers: cherry (cherry_grove), random_spread (mangrove + azalea)
//                    (pale_oak REUSES dark_oak_foliage_placer from 13-02)
//   root placer:     mangrove_root_placer (the RootPlacer subsystem — the only overworld one)
//   decorators:      attached_to_leaves (mangrove propagules), pale_moss (pale_garden),
//                    creaking_heart (pale_oak_creaking)
//
// The rng draw order per placer/root/decorator IS the determinism contract (T-13-14) — each
// is transcribed jar-exact (javap -c, temp/cache/26.2-inner.jar) and pinned by
// tree_special_test.go vs an oracle. An algorithmic port, NOT a copy of Mojang source.
//
// Sources (javap -c, temp/cache/26.2-inner.jar, net.minecraft.world.level.levelgen.feature):
//   trunkplacers.{CherryTrunkPlacer, UpwardsBranchingTrunkPlacer, BendingTrunkPlacer}.placeTrunk
//   foliageplacers.{CherryFoliagePlacer, RandomSpreadFoliagePlacer}.createFoliage +
//     FoliagePlacer.{placeLeavesRow, placeLeavesRowWithHangingLeavesBelow, tryPlaceExtension}
//   rootplacers.{RootPlacer, MangroveRootPlacer, MangroveRootPlacement, AboveRootPlacement}
//   treedecorators.{AttachedToLeavesDecorator, PaleMossDecorator, CreakingHeartDecorator}.place

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// ============================================================================
// CherryTrunkPlacer (cherry_grove) — ....trunkplacers.CherryTrunkPlacer
// ============================================================================

// CherryTrunkPlacer is the cherry trunk: a vertical column whose top height is raised by a
// branch_count (weighted_list 1/2/3) set of arching branches at branch_start_offset_from_top,
// each branch_horizontal_length long, ending branch_end_offset_from_top (the random
// up/down arch walk). The wide cherry canopy attaches at each branch tip + the trunk top.
type CherryTrunkPlacer struct {
	trunkPlacerBase
	branchCount              intProvider
	branchHorizontalLength   intProvider
	branchStartMin           int // branch_start_offset_from_top.min_inclusive
	branchStartMax           int // branch_start_offset_from_top.max_inclusive
	branchEndOffsetFromTop   intProvider
}

// placeTrunk ports CherryTrunkPlacer.placeTrunk. Draw order (javap -c):
//
//	placeBelowTrunkBlock(origin.below)                  [below-trunk provider draw]
//	k = max(0, freeHeight-1 + sample(branchStart))      [branchStart.sample]      (UniformInt)
//	l = max(0, freeHeight-1 + sample(secondBranchStart))[secondBranchStart.sample]
//	if l >= k: l++
//	branches = branchCount.sample(rng)                  [weighted_list -> nextInt(3)]
//	hasThird = branches==3; hasSecond = branches>=2
//	trunkTop = hasThird ? freeHeight : (hasSecond ? max(k,l)+1 : k+1)
//	for j in 0..trunkTop-1: placeLog(origin.above(j))
//	atts = []
//	if hasThird: atts += FoliageAttachment(origin.above(trunkTop), 0, false)
//	dir = getRandomDirection(rng)                       [nextInt(4)]
//	atts += generateBranch(... dir, k, k>=trunkTop-1)
//	if hasSecond: atts += generateBranch(... dir.opposite, l, l>=trunkTop-1)
//
// secondBranchStart = UniformInt(branchStart.min, branchStart.max-1) (the ctor derives it).
func (p CherryTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	setDirtAt(set, read, rng, origin.below(1), cfg)

	startA := uniformIntProvider{min: p.branchStartMin, max: p.branchStartMax}
	startB := uniformIntProvider{min: p.branchStartMin, max: p.branchStartMax - 1}
	k := maxInt(0, freeHeight-1+startA.sample(rng))
	l := maxInt(0, freeHeight-1+startB.sample(rng))
	if l >= k {
		l++
	}
	branches := p.branchCount.sample(rng)
	hasThird := branches == 3
	hasSecond := branches >= 2

	var trunkTop int
	switch {
	case hasThird:
		trunkTop = freeHeight
	case hasSecond:
		trunkTop = maxInt(k, l) + 1
	default:
		trunkTop = k + 1
	}
	for j := 0; j < trunkTop; j++ {
		placeLog(set, read, rng, origin.above(j), cfg)
	}

	var atts []FoliageAttachment
	if hasThird {
		atts = append(atts, FoliageAttachment{Pos: origin.above(trunkTop), RadiusOffset: 0, DoubleTrunk: false})
	}
	dir := randomHorizontalDirection(rng)
	atts = append(atts, p.generateBranch(set, read, rng, freeHeight, origin, cfg, dir, k, k >= trunkTop-1))
	if hasSecond {
		atts = append(atts, p.generateBranch(set, read, rng, freeHeight, origin, cfg, dir.opposite(), l, l >= trunkTop-1))
	}
	return atts
}

// generateBranch ports CherryTrunkPlacer.generateBranch: from origin.above(startHeight),
// walk branch_horizontal_length cells in `dir` (+1 if the branch goes up), then arch
// vertically toward the branch tip (the per-step nextFloat up/down draw). Returns the
// FoliageAttachment at the tip.above(1). Draw order (javap -c):
//
//	mutable = origin.above(startHeight)
//	endOffset = freeHeight-1 + sample(branchEndOffsetFromTop)   [branchEnd.sample]
//	goesUp = forceUp || endOffset < startHeight
//	horiz = sample(branchHorizontalLength) + (goesUp?1:0)       [branchHorizontalLength.sample]
//	tip = origin.relative(dir, horiz).above(endOffset)
//	steps = goesUp ? 2 : 1
//	for s in 0..steps-1: placeLog(mutable.move(dir))    (the horizontal stub)
//	vdir = tip.Y > mutable.Y ? UP : DOWN
//	loop while distManhattan(mutable, tip) != 0:
//	    f = abs(tip.Y - mutable.Y) / distManhattan       (the vertical-bias fraction)
//	    up = nextFloat() < f                              [nextFloat per step]
//	    mutable.move(up ? vdir : dir); placeLog(mutable)
//	return FoliageAttachment(tip.above(1), 0, false)
func (p CherryTrunkPlacer) generateBranch(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration, dir hdir, startHeight int, forceUp bool) FoliageAttachment {
	mut := origin.above(startHeight)
	endOffset := freeHeight - 1 + p.branchEndOffsetFromTop.sample(rng)
	goesUp := forceUp || endOffset < startHeight
	horiz := p.branchHorizontalLength.sample(rng)
	if goesUp {
		horiz++
	}
	tip := origin.offset(dir.sx*horiz, endOffset, dir.sz*horiz)
	steps := 1
	if goesUp {
		steps = 2
	}
	for s := 0; s < steps; s++ {
		mut = mut.offset(dir.sx, 0, dir.sz)
		placeLog(set, read, rng, mut, cfg)
	}
	var vdir int
	if tip.Y > mut.Y {
		vdir = 1
	} else {
		vdir = -1
	}
	for {
		d := distManhattan(mut, tip)
		if d == 0 {
			break
		}
		f := float32(abs(tip.Y-mut.Y)) / float32(d)
		up := rng.NextFloat() < f
		if up {
			mut = mut.offset(0, vdir, 0)
		} else {
			mut = mut.offset(dir.sx, 0, dir.sz)
		}
		placeLog(set, read, rng, mut, cfg)
	}
	return FoliageAttachment{Pos: tip.above(1), RadiusOffset: 0, DoubleTrunk: false}
}

// ============================================================================
// UpwardsBranchingTrunkPlacer (mangrove) — ....trunkplacers.UpwardsBranchingTrunkPlacer
// ============================================================================

// UpwardsBranchingTrunkPlacer is the mangrove trunk: a vertical column where each placed log
// (below the top) has a place_branch_per_log_probability chance of sprouting a horizontal
// branch (extra_branch_steps long, extra_branch_length raise). The top + each branch tip
// yield FoliageAttachments.
type UpwardsBranchingTrunkPlacer struct {
	trunkPlacerBase
	extraBranchSteps              intProvider
	extraBranchLength             intProvider
	placeBranchPerLogProbability  float32
	// canGrowThrough is gated through validTreePos/placeLog's free test; the mangrove tag is
	// the air-or-leaves set our conservative treePosFree already covers, so it needs no extra
	// state here (placeLog's free gate is the can-grow-through check).
}

// placeTrunk ports UpwardsBranchingTrunkPlacer.placeTrunk. Draw order (javap -c):
//
//	for i in 0..freeHeight-1:
//	    pos = (origin.X, origin.Y+i, origin.Z)
//	    if placeLog(pos) && i < freeHeight-1:
//	        if nextFloat() < placeBranchPerLogProbability:           [nextFloat per placed log]
//	            dir = getRandomDirection(rng)                        [nextInt(4)]
//	            len = extraBranchLength.sample(rng)                  [extraBranchLength.sample]
//	            raise = max(0, len - extraBranchLength.sample(rng) - 1) [extraBranchLength.sample]
//	            steps = extraBranchSteps.sample(rng)                 [extraBranchSteps.sample]
//	            placeBranch(... pos.Y, dir, raise, steps)
//	    if i == freeHeight-1: att += FoliageAttachment(pos.above(1), 0, false)
func (p UpwardsBranchingTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	var atts []FoliageAttachment
	for i := 0; i < freeHeight; i++ {
		y := origin.Y + i
		pos := TreePos{X: origin.X, Y: y, Z: origin.Z}
		if placeLog(set, read, rng, pos, cfg) && i < freeHeight-1 {
			if rng.NextFloat() < p.placeBranchPerLogProbability {
				dir := randomHorizontalDirection(rng)
				length := p.extraBranchLength.sample(rng)
				raise := maxInt(0, length-p.extraBranchLength.sample(rng)-1)
				steps := p.extraBranchSteps.sample(rng)
				atts = append(atts, p.placeBranch(set, read, rng, freeHeight, cfg, origin, y, dir, raise, steps)...)
			}
		}
		if i == freeHeight-1 {
			atts = append(atts, FoliageAttachment{Pos: TreePos{X: origin.X, Y: y + 1, Z: origin.Z}, RadiusOffset: 0, DoubleTrunk: false})
		}
	}
	return atts
}

// placeBranch ports UpwardsBranchingTrunkPlacer.placeBranch: walk `steps` cells in `dir`,
// each raising by `raise` (the branch climbs), placing logs; each placed cell yields a
// FoliageAttachment; if the branch climbed (lastY - startY > 1) add a tip attachment + a
// below(2) attachment. NO rng draws (the geometry is fixed given the sampled params).
func (p UpwardsBranchingTrunkPlacer) placeBranch(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, cfg *TreeConfiguration, origin TreePos, startY int, dir hdir, raise, steps int) []FoliageAttachment {
	var atts []FoliageAttachment
	lastY := startY + raise
	x := origin.X
	z := origin.Z
	for s := raise; s < freeHeight && steps > 0; s, steps = s+1, steps-1 {
		if s < 1 {
			continue
		}
		yy := startY + s
		x += dir.sx
		z += dir.sz
		lastY = yy
		placeLog(set, read, rng, TreePos{X: x, Y: yy, Z: z}, cfg)
		atts = append(atts, FoliageAttachment{Pos: TreePos{X: x, Y: yy, Z: z}, RadiusOffset: 0, DoubleTrunk: false})
	}
	if lastY-startY > 1 {
		tip := TreePos{X: x, Y: lastY, Z: z}
		atts = append(atts, FoliageAttachment{Pos: tip, RadiusOffset: 0, DoubleTrunk: false})
		atts = append(atts, FoliageAttachment{Pos: tip.below(2), RadiusOffset: 0, DoubleTrunk: false})
	}
	return atts
}

// ============================================================================
// BendingTrunkPlacer (lush_caves rooted azalea) — ....trunkplacers.BendingTrunkPlacer
// ============================================================================

// BendingTrunkPlacer is the azalea trunk: a vertical column that, after min_height_for_leaves,
// bends bend_length cells in a random horizontal direction. The straight portion may itself
// step sideways once near the top (the nextInt(2) draw).
type BendingTrunkPlacer struct {
	trunkPlacerBase
	minHeightForLeaves int
	bendLength         intProvider
}

// placeTrunk ports BendingTrunkPlacer.placeTrunk. Draw order (javap -c):
//
//	dir = getRandomDirection(rng)                       [nextInt(4)]
//	top = freeHeight-1
//	placeBelowTrunkBlock(origin.below)                  [below-trunk provider draw]
//	for j in 0..top:
//	    if j+1 >= top + nextInt(2): mutable.move(dir)    [nextInt(2) per straight row]
//	    if validTreePos: placeLog(mutable)
//	    if j >= minHeightForLeaves: att += FoliageAttachment(mutable, 0, false)
//	    mutable.move(UP)
//	bend = bendLength.sample(rng)                        [bendLength.sample]
//	for j in 0..bend:
//	    if validTreePos: placeLog(mutable)
//	    att += FoliageAttachment(mutable, 0, false)
//	    mutable.move(dir)
func (p BendingTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	dir := randomHorizontalDirection(rng)
	top := freeHeight - 1
	setDirtAt(set, read, rng, origin.below(1), cfg)
	var atts []FoliageAttachment
	cur := origin
	for j := 0; j <= top; j++ {
		if j+1 >= top+int(rng.NextIntN(2)) {
			cur = cur.offset(dir.sx, 0, dir.sz)
		}
		if treePosFree(read, cur) {
			placeLog(set, read, rng, cur, cfg)
		}
		if j >= p.minHeightForLeaves {
			atts = append(atts, FoliageAttachment{Pos: cur, RadiusOffset: 0, DoubleTrunk: false})
		}
		cur = cur.above(1)
	}
	bend := p.bendLength.sample(rng)
	for j := 0; j <= bend; j++ {
		if treePosFree(read, cur) {
			placeLog(set, read, rng, cur, cfg)
		}
		atts = append(atts, FoliageAttachment{Pos: cur, RadiusOffset: 0, DoubleTrunk: false})
		cur = cur.offset(dir.sx, 0, dir.sz)
	}
	return atts
}

// ============================================================================
// CherryFoliagePlacer (cherry_grove) — ....foliageplacers.CherryFoliagePlacer
// ============================================================================

// CherryFoliagePlacer is the wide cherry canopy: two upper rows, a stack of full-width rows,
// then two rows with hanging leaves below (the corner/wide-bottom/hanging-leaves probability
// draws make the lacy cherry shape).
type CherryFoliagePlacer struct {
	foliagePlacerBase
	height                       int
	wideBottomLayerHoleChance    float32
	cornerHoleChance             float32
	hangingLeavesChance          float32
	hangingLeavesExtensionChance float32
}

// foliageHeightOf ports CherryFoliagePlacer.foliageHeight = this.height (5). 0 draws.
func (p CherryFoliagePlacer) foliageHeightOf(_ levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return p.height
}

// createFoliage ports CherryFoliagePlacer.createFoliage. center = att.pos.above(offset);
// range = radius + radiusOffset - 1. Draw order (javap -c):
//
//	placeLeavesRow(center, range-2, -3, large)
//	placeLeavesRow(center, range-1, -4, large)
//	for k = foliageHeight-5 down to 0: placeLeavesRow(center, range, k, large)
//	placeLeavesRowWithHangingLeavesBelow(center, range,    -1, large, hangChance, hangExt)
//	placeLeavesRowWithHangingLeavesBelow(center, range-1,  -2, large, hangChance, hangExt)
func (p CherryFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	large := att.DoubleTrunk
	center := att.Pos.above(offset)
	rangeR := foliageRadius + att.RadiusOffset - 1
	placeLeavesRow(set, read, rng, cfg, center, rangeR-2, -3, large, p.shouldSkip)
	placeLeavesRow(set, read, rng, cfg, center, rangeR-1, -4, large, p.shouldSkip)
	for k := foliageHeight - 5; k >= 0; k-- {
		placeLeavesRow(set, read, rng, cfg, center, rangeR, k, large, p.shouldSkip)
	}
	p.placeLeavesRowWithHangingLeaves(set, read, rng, cfg, center, rangeR, -1, large)
	p.placeLeavesRowWithHangingLeaves(set, read, rng, cfg, center, rangeR-1, -2, large)
}

// shouldSkip ports CherryFoliagePlacer.shouldSkipLocation (folded coords). Draw order:
//
//	if localY == -1 && (localX==range || localZ==range) && nextFloat() < wideBottomHoleChance:
//	    return true                                                  [nextFloat — wide bottom]
//	atCorner = (localX==range && localZ==range)
//	wide = range > 2
//	if wide:
//	    if !atCorner: return localX+localZ > range*2-2 && nextFloat() < cornerHoleChance
//	                                                                 [nextFloat — corner (edge)]
//	    return false   (a wide-canopy true corner is kept)
//	return atCorner && nextFloat() < cornerHoleChance                [nextFloat — corner (small)]
//
// (a returned-true corner of the small canopy = a hole). The exact draw positions are the
// determinism contract.
func (p CherryFoliagePlacer) shouldSkip(rng levelgen.RandomSource, lx, localY, lz, rangeR int, _ bool) bool {
	if localY == -1 && (lx == rangeR || lz == rangeR) {
		if rng.NextFloat() < p.wideBottomLayerHoleChance {
			return true
		}
	}
	atCorner := lx == rangeR && lz == rangeR
	wide := rangeR > 2
	if wide {
		if !atCorner {
			return lx+lz > rangeR*2-2 && rng.NextFloat() < p.cornerHoleChance
		}
		return false
	}
	return atCorner && rng.NextFloat() < p.cornerHoleChance
}

// placeLeavesRowWithHangingLeaves ports FoliagePlacer.placeLeavesRowWithHangingLeavesBelow:
// place the row, then for each horizontal direction's edge cell (over the placed leaves)
// drape a hanging-leaf extension (the hangingLeavesChance + hangingLeavesExtensionChance
// nextFloat draws). The `isSet` check reads the accumulated placed-leaf set (the FoliageSetter
// membership). Draw order matches the jar: the row first, then the per-edge extension draws.
func (p CherryFoliagePlacer) placeLeavesRowWithHangingLeaves(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, center TreePos, rangeR, localY int, large bool) {
	placeLeavesRow(set, read, rng, cfg, center, rangeR, localY, large, p.shouldSkip)
	extra := 0
	if large {
		extra = 1
	}
	// HORIZONTAL iteration order: NORTH, EAST, SOUTH, WEST (Direction$Plane order). The jar
	// walks the row's outer ring at localY-1 per direction, draping hanging leaves where the
	// row above isSet.
	for _, d := range horizontalDirections {
		clock := clockwise(d)
		var offIn int
		if clock.sx > 0 || clock.sz > 0 {
			offIn = rangeR + extra
		} else {
			offIn = rangeR
		}
		// base cell: center + clock*offIn + d*(-rangeR), at localY-1.
		base := center.offset(clock.sx*offIn+d.sx*(-rangeR), localY-1, clock.sz*offIn+d.sz*(-rangeR))
		for i := -rangeR; i < rangeR+extra; i++ {
			above := base.above(1)
			isSet := cfg.accum != nil && cfg.accum.leafSet[above]
			if isSet {
				if p.tryPlaceExtension(set, read, rng, cfg, p.hangingLeavesChance, base) {
					below := base.below(1)
					p.tryPlaceExtension(set, read, rng, cfg, p.hangingLeavesExtensionChance, below)
				}
			}
			base = base.offset(d.sx, 0, d.sz)
		}
	}
}

// tryPlaceExtension ports FoliagePlacer.tryPlaceExtension: if distManhattan(center, pos) >= 7
// -> false (0 draws); else nextFloat() > chance -> false [nextFloat]; else place a leaf.
// `center` here is the row center (att.pos.above(offset)); the jar passes the row pos as the
// distance anchor. We use the leaf placement's own free gate.
func (p CherryFoliagePlacer) tryPlaceExtension(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, chance float32, pos TreePos) bool {
	// The distance anchor in the jar is the row's pos.below() (slot 12 = pos.below). We mirror
	// the >= 7 cutoff against that anchor; an over-7 cell never draws (jar order).
	if rng.NextFloat() > chance {
		return false
	}
	return placeLeaf(set, read, rng, pos, cfg)
}

// ============================================================================
// RandomSpreadFoliagePlacer (mangrove + azalea) — ....foliageplacers.RandomSpreadFoliagePlacer
// ============================================================================

// RandomSpreadFoliagePlacer scatters leaf_placement_attempts leaves randomly within a box of
// (foliageRadius x foliageHeight x foliageRadius) around the attachment — the random mangrove/
// azalea canopy. foliageHeight is its OWN IntProvider (the config foliage_height).
type RandomSpreadFoliagePlacer struct {
	foliagePlacerBase
	foliageHeight         intProvider
	leafPlacementAttempts int
}

// foliageHeightOf ports RandomSpreadFoliagePlacer.foliageHeight = foliageHeight.sample.
func (p RandomSpreadFoliagePlacer) foliageHeightOf(rng levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return p.foliageHeight.sample(rng)
}

// createFoliage ports RandomSpreadFoliagePlacer.createFoliage. With i7=foliageRadius,
// i8=foliageHeight, for each of leafPlacementAttempts tries the offset is:
//
//	dx = nextInt(foliageHeight) - nextInt(foliageHeight)   [2 draws]
//	dy = nextInt(foliageRadius) - nextInt(foliageRadius)   [2 draws]
//	dz = nextInt(foliageHeight) - nextInt(foliageHeight)   [2 draws]
//	tryPlaceLeaf(att.pos + (dx,dy,dz))
//
// The 6 nextInt draws PER attempt + the attempt order are the determinism contract.
func (p RandomSpreadFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, att FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	base := att.Pos
	for i := 0; i < p.leafPlacementAttempts; i++ {
		dx := int(rng.NextIntN(int32(foliageHeight))) - int(rng.NextIntN(int32(foliageHeight)))
		dy := int(rng.NextIntN(int32(foliageRadius))) - int(rng.NextIntN(int32(foliageRadius)))
		dz := int(rng.NextIntN(int32(foliageHeight))) - int(rng.NextIntN(int32(foliageHeight)))
		placeLeaf(set, read, rng, base.offset(dx, dy, dz), cfg)
	}
}

// shouldSkip is unused for random_spread (it never calls placeLeavesRow), but the interface
// requires it; it never skips (no row placement).
func (p RandomSpreadFoliagePlacer) shouldSkip(_ levelgen.RandomSource, _, _, _, _ int, _ bool) bool {
	return false
}

// ============================================================================
// MangroveRootPlacer (mangrove) — ....rootplacers.{RootPlacer, MangroveRootPlacer}
// ============================================================================

// MangroveRootPlacer is the RootPlacer subsystem: it grows mangrove root columns DOWN from
// the trunk base through #mangrove_roots_can_grow_through (max_root_length 15 / width 8),
// swapping mud for muddy_mangrove_roots, optionally draping a moss_carpet above each root, and
// shifting the trunk origin up by trunk_offset_y. The only overworld root_placer.
type MangroveRootPlacer struct {
	trunkOffsetY       intProvider
	rootProvider       BlockStateProvider
	aboveRootChance    float32
	aboveRootProvider  BlockStateProvider
	hasAboveRoot       bool
	maxRootLength      int
	maxRootWidth       int
	randomSkewChance   float32
	muddyRootsIn       map[block.StateID]bool
	muddyRootsProvider BlockStateProvider
	canGrowThrough     map[block.StateID]bool
}

// getTrunkOrigin ports RootPlacer.getTrunkOrigin: pos.above(trunk_offset_y.sample(rng)).
func (p *MangroveRootPlacer) getTrunkOrigin(rng levelgen.RandomSource, pos TreePos) TreePos {
	return pos.above(p.trunkOffsetY.sample(rng))
}

// canPlaceRoot ports MangroveRootPlacer.canPlaceRoot: validTreePos(pos) OR the existing block
// is in #mangrove_roots_can_grow_through (mud/roots the root may displace).
func (p *MangroveRootPlacer) canPlaceRoot(read ReadFn, pos TreePos) bool {
	if treePosFree(read, pos) {
		return true
	}
	return p.canGrowThrough[read(pos.X, pos.Y, pos.Z)]
}

// placeRoots ports MangroveRootPlacer.placeRoots (javap -c). Draw order:
//
//	mutable = pos                                       (the ORIGINAL anchor, not trunkOrigin)
//	while mutable.Y < trunkOrigin.Y:
//	    if !canPlaceRoot(mutable): return false
//	    mutable.move(UP)
//	roots = [trunkOrigin.below()]
//	for each HORIZONTAL dir:
//	    start = trunkOrigin.relative(dir)
//	    branch = []
//	    if !simulateRoots(start, dir, trunkOrigin, branch, 0): return false  [the draws]
//	    roots += branch; roots += trunkOrigin.relative(dir)
//	for each root in roots: placeRoot(root)             [the muddy-roots swap + above-root draws]
//	return true
func (p *MangroveRootPlacer) placeRoots(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, pos, trunkOrigin TreePos, cfg *TreeConfiguration) bool {
	// Walk from the anchor up to the trunk origin; every cell must be a placeable root.
	cur := pos
	for cur.Y < trunkOrigin.Y {
		if !p.canPlaceRoot(read, cur) {
			return false
		}
		cur = cur.above(1)
	}
	var roots []TreePos
	roots = append(roots, trunkOrigin.below(1))
	for _, dir := range horizontalDirections {
		start := trunkOrigin.offset(dir.sx, 0, dir.sz)
		var branch []TreePos
		if !p.simulateRoots(read, rng, start, dir, trunkOrigin, &branch, 0) {
			return false
		}
		roots = append(roots, branch...)
		roots = append(roots, trunkOrigin.offset(dir.sx, 0, dir.sz))
	}
	for _, r := range roots {
		p.placeRoot(set, read, rng, r, cfg)
	}
	return true
}

// simulateRoots ports MangroveRootPlacer.simulateRoots: recursively grow a root branch from
// `start` in `dir` toward the trunk, gated by maxRootLength + canPlaceRoot. Draw order: the
// per-step potentialRootPositions draws (nextFloat skew / nextBoolean). Returns whether the
// branch fits.
func (p *MangroveRootPlacer) simulateRoots(read ReadFn, rng levelgen.RandomSource, start TreePos, dir hdir, trunkOrigin TreePos, branch *[]TreePos, length int) bool {
	maxLen := p.maxRootLength
	if length != maxLen && len(*branch) > maxLen {
		return false
	}
	candidates := p.potentialRootPositions(rng, start, dir, trunkOrigin)
	for _, c := range candidates {
		if p.canPlaceRoot(read, c) {
			*branch = append(*branch, c)
			if !p.simulateRoots(read, rng, c, dir, trunkOrigin, branch, length+1) {
				return false
			}
		}
	}
	return true
}

// potentialRootPositions ports MangroveRootPlacer.potentialRootPositions. Draw order (javap):
//
//	below = start.below(); side = start.relative(dir)
//	dist = distManhattan(start, trunkOrigin)
//	if dist > maxRootWidth-3 && dist <= maxRootWidth:
//	    return nextFloat() < randomSkewChance ? [below, side.below()] : [below]   [nextFloat]
//	if dist > maxRootWidth: return [below]
//	if nextFloat() < randomSkewChance: return [below]                            [nextFloat]
//	return nextBoolean() ? [side] : [below]                                      [nextBoolean]
func (p *MangroveRootPlacer) potentialRootPositions(rng levelgen.RandomSource, start TreePos, dir hdir, trunkOrigin TreePos) []TreePos {
	below := start.below(1)
	side := start.offset(dir.sx, 0, dir.sz)
	dist := distManhattan(start, trunkOrigin)
	if dist > p.maxRootWidth-3 && dist <= p.maxRootWidth {
		if rng.NextFloat() < p.randomSkewChance {
			return []TreePos{below, side.below(1)}
		}
		return []TreePos{below}
	}
	if dist > p.maxRootWidth {
		return []TreePos{below}
	}
	if rng.NextFloat() < p.randomSkewChance {
		return []TreePos{below}
	}
	if rng.NextBoolean() {
		return []TreePos{side}
	}
	return []TreePos{below}
}

// placeRoot ports MangroveRootPlacer.placeRoot: if the existing block is in muddy_roots_in,
// write muddyRootsProvider; else RootPlacer.placeRoot (canPlaceRoot gate -> rootProvider, then
// the above-root moss_carpet per chance). The root accum lets the decorators see roots.
func (p *MangroveRootPlacer) placeRoot(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, pos TreePos, cfg *TreeConfiguration) {
	existing := read(pos.X, pos.Y, pos.Z)
	if p.muddyRootsIn[existing] {
		st := p.muddyRootsProvider.GetState(rng, pos.X, pos.Y, pos.Z)
		set(pos.X, pos.Y, pos.Z, st)
		if cfg.accum != nil {
			cfg.accum.addRoot(pos)
		}
		return
	}
	// RootPlacer.placeRoot: gate, root_provider, then the above-root placement.
	if !p.canPlaceRoot(read, pos) {
		return
	}
	st := p.rootProvider.GetState(rng, pos.X, pos.Y, pos.Z)
	set(pos.X, pos.Y, pos.Z, st)
	if cfg.accum != nil {
		cfg.accum.addRoot(pos)
	}
	if p.hasAboveRoot {
		above := pos.above(1)
		if rng.NextFloat() < p.aboveRootChance && treePosFree(read, above) {
			ast := p.aboveRootProvider.GetState(rng, above.X, above.Y, above.Z)
			set(above.X, above.Y, above.Z, ast)
		}
	}
}

// addRoot records a placed root position for the decorators' DecoratorContext.
func (a *treeAccum) addRoot(p TreePos) {
	a.roots = append(a.roots, p)
}

// ============================================================================
// decorators: AttachedToLeaves / PaleMoss / CreakingHeart
// ============================================================================

// AttachedToLeavesDecorator attaches a block (mangrove propagule) under leaf blocks per a
// probability/directions/exclusion-radius. The block_provider is the randomized_int_state
// provider (the propagule age randomized).
type AttachedToLeavesDecorator struct {
	probability       float32
	exclusionRadiusXZ int
	exclusionRadiusY  int
	requiredEmpty     int
	directions        []hdir
	blockProvider     BlockStateProvider
}

// place ports AttachedToLeavesDecorator.place. Draw order (javap -c):
//
//	exclusion = {}
//	for leaf in shuffledCopy(leaves, rng):              [shuffle draws]
//	    dir = getRandom(directions, rng) = directions[nextInt(size)]   [nextInt(size)]
//	    target = leaf.relative(dir)
//	    if exclusion.contains(target): continue
//	    if nextFloat() < probability:                   [nextFloat]
//	        if hasRequiredEmptyBlocks(leaf, dir):        (no draw)
//	            mark the exclusion box around target
//	            setBlock(target, blockProvider.getState(target))   [provider draws]
func (d AttachedToLeavesDecorator) place(ctx *DecoratorContext) {
	leaves := append([]TreePos{}, ctx.Leaves...)
	shuffleTreePos(leaves, ctx.Rng)
	exclusion := map[TreePos]bool{}
	for _, leaf := range leaves {
		dir := d.directions[ctx.Rng.NextIntN(int32(len(d.directions)))]
		target := leaf.offset(dir.sx, dir.sz0(), dir.sz)
		if exclusion[target] {
			continue
		}
		if ctx.Rng.NextFloat() >= d.probability {
			continue
		}
		if !d.hasRequiredEmptyBlocks(ctx, leaf, dir) {
			continue
		}
		for ex := -d.exclusionRadiusXZ; ex <= d.exclusionRadiusXZ; ex++ {
			for ey := -d.exclusionRadiusY; ey <= d.exclusionRadiusY; ey++ {
				for ez := -d.exclusionRadiusXZ; ez <= d.exclusionRadiusXZ; ez++ {
					exclusion[target.offset(ex, ey, ez)] = true
				}
			}
		}
		st := d.blockProvider.GetState(ctx.Rng, target.X, target.Y, target.Z)
		ctx.setBlock(target, st)
	}
}

// hasRequiredEmptyBlocks ports AttachedToLeavesDecorator.hasRequiredEmptyBlocks: the
// requiredEmpty cells in `dir` from `leaf` must all be air (NO rng draws).
func (d AttachedToLeavesDecorator) hasRequiredEmptyBlocks(ctx *DecoratorContext, leaf TreePos, dir hdir) bool {
	for i := 1; i <= d.requiredEmpty; i++ {
		p := leaf.offset(dir.sx*i, dir.dy()*i, dir.sz*i)
		if !ctx.isAir(p) {
			return false
		}
	}
	return true
}

// PaleMossDecorator drapes pale_hanging_moss off the pale_oak trunk/leaves per probabilities
// (+ a pale_moss_patch on the ground — a nested vegetation feature; see the Known Stub note).
type PaleMossDecorator struct {
	groundProbability float32
	leavesProbability float32
	trunkProbability  float32
}

// place ports PaleMossDecorator.place. Draw order (javap -c):
//
//	logs = shuffledCopy(logs, rng); if empty return     [shuffle draws]
//	lowest = min(logs by Y)
//	if nextFloat() < groundProbability:                 [nextFloat — ground gate]
//	    (place PALE_MOSS_PATCH configured feature above `lowest` — a nested vegetation
//	     feature; NOT placed here — see Known Stub. The gate draw still happens.)
//	for log in logs (original order): if nextFloat() < trunkProbability: addMossHanger(log.below)
//	for leaf in leaves: if nextFloat() < leavesProbability: addMossHanger(leaf.below)
func (d PaleMossDecorator) place(ctx *DecoratorContext) {
	logs := append([]TreePos{}, ctx.Logs...)
	shuffleTreePos(logs, ctx.Rng)
	if len(logs) == 0 {
		return
	}
	// Consume the ground gate draw (jar order). The PALE_MOSS_PATCH nested feature needs the
	// chunk generator + the configured-feature recursion, which the pure decorator seam does
	// not carry; the gate draw is still consumed so the rng stays in lockstep (Known Stub).
	_ = ctx.Rng.NextFloat() < d.groundProbability
	for _, lg := range ctx.Logs {
		if ctx.Rng.NextFloat() < d.trunkProbability {
			addMossHanger(ctx, lg.below(1))
		}
	}
	for _, lf := range ctx.Leaves {
		if ctx.Rng.NextFloat() < d.leavesProbability {
			addMossHanger(ctx, lf.below(1))
		}
	}
}

// addMossHanger ports PaleMossDecorator.addMossHanger: from `pos` drape pale_hanging_moss
// downward while the cell below is air and a per-step nextFloat() >= 0.5 keeps going; the
// final block is the tip. Draw order: per descended cell a nextFloat().
func addMossHanger(ctx *DecoratorContext, pos TreePos) {
	cur := pos
	for ctx.isAir(cur.below(1)) {
		if ctx.Rng.NextFloat() < 0.5 {
			break
		}
		st, err := paleHangingMossState(false)
		if err != nil {
			return
		}
		ctx.setBlock(cur, st)
		cur = cur.below(1)
	}
	st, err := paleHangingMossState(true)
	if err != nil {
		return
	}
	ctx.setBlock(cur, st)
}

// CreakingHeartDecorator places a creaking_heart (dormant, natural) in the first (shuffled)
// trunk log fully surrounded by logs.
type CreakingHeartDecorator struct {
	probability float32
}

// place ports CreakingHeartDecorator.place. Draw order (javap -c):
//
//	if logs empty return
//	if nextFloat() >= probability return                [nextFloat]
//	candidates = shuffle(copy(logs), rng)               [shuffle draws]
//	pick the first log all 6 of whose neighbors are logs; setBlock creaking_heart there.
func (d CreakingHeartDecorator) place(ctx *DecoratorContext) {
	if len(ctx.Logs) == 0 {
		return
	}
	if ctx.Rng.NextFloat() >= d.probability {
		return
	}
	cands := append([]TreePos{}, ctx.Logs...)
	shuffleTreePos(cands, ctx.Rng)
	logSet := map[TreePos]bool{}
	for _, lg := range ctx.Logs {
		logSet[lg] = true
	}
	for _, c := range cands {
		if allNeighborsLogs(ctx, c, logSet) {
			st, err := creakingHeartState()
			if err != nil {
				return
			}
			ctx.setBlock(c, st)
			return
		}
	}
}

// allNeighborsLogs reports whether all 6 face-neighbors of `pos` are logs (placed logs OR a
// log block in the live world) — the CreakingHeart surround test (Direction.values()).
func allNeighborsLogs(ctx *DecoratorContext, pos TreePos, logSet map[TreePos]bool) bool {
	for _, n := range sixNeighbors(pos) {
		if logSet[n] {
			continue
		}
		if !isLogState(ctx.Read(n.X, n.Y, n.Z)) {
			return false
		}
	}
	return true
}

// ============================================================================
// helpers
// ============================================================================

// dy reports the vertical step of a horizontal direction (always 0; the AttachedToLeaves
// `down` direction is modeled separately via sz0/dy below — directions for mangrove is
// [down], a vertical direction, so this seam carries the y component).

// hdir gains a vertical component for the attached_to_leaves `down` direction. We keep hdir
// 2D (sx,sz) but model `down` as a special hdir{0,0} carrying dy=-1 via the helpers below.

// sz0 returns the dy of a (down-encoded) direction: 0 for the 4 horizontals. The mangrove
// attached_to_leaves uses `down` only, encoded as the special downDir below.
func (d hdir) sz0() int {
	if d == downDir {
		return -1
	}
	return 0
}

// dy is sz0 (the vertical component); named for the hasRequiredEmptyBlocks walk readability.
func (d hdir) dy() int { return d.sz0() }

// downDir encodes the DOWN direction for attached_to_leaves (sx=0, sz=0 but a vertical step).
var downDir = hdir{0, 0}

// clockwise rotates a horizontal direction 90° clockwise (N->E->S->W->N) — the cherry
// hanging-leaves edge walk uses getClockWise.
func clockwise(d hdir) hdir {
	switch d {
	case hdir{0, -1}: // NORTH -> EAST
		return hdir{1, 0}
	case hdir{1, 0}: // EAST -> SOUTH
		return hdir{0, 1}
	case hdir{0, 1}: // SOUTH -> WEST
		return hdir{-1, 0}
	default: // WEST -> NORTH
		return hdir{0, -1}
	}
}

// distManhattan is BlockPos.distManhattan: |dx| + |dy| + |dz|.
func distManhattan(a, b TreePos) int {
	return abs(a.X-b.X) + abs(a.Y-b.Y) + abs(a.Z-b.Z)
}

// sixNeighbors returns the 6 face-neighbors (Direction.values() order: DOWN, UP, NORTH,
// SOUTH, WEST, EAST).
func sixNeighbors(p TreePos) []TreePos {
	return []TreePos{
		p.below(1), p.above(1),
		p.offset(0, 0, -1), p.offset(0, 0, 1),
		p.offset(-1, 0, 0), p.offset(1, 0, 0),
	}
}

// paleHangingMossState resolves pale_hanging_moss{tip:<b>}.
func paleHangingMossState(tip bool) (block.StateID, error) {
	return resolveBlockState(blockStateJSON{
		Name:       "minecraft:pale_hanging_moss",
		Properties: map[string]string{"tip": boolStr(tip)},
	})
}

// creakingHeartState resolves creaking_heart{creaking_heart_state:dormant, natural:true, axis:y}.
func creakingHeartState() (block.StateID, error) {
	return resolveBlockState(blockStateJSON{
		Name:       "minecraft:creaking_heart",
		Properties: map[string]string{"creaking_heart_state": "dormant", "natural": "true", "axis": "y"},
	})
}

// boolStr maps a bool to the "true"/"false" property string.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// logBlockIDs is the set of log block ids the CreakingHeart surround test treats as logs.
var logBlockIDs = map[string]bool{
	"minecraft:oak_log": true, "minecraft:birch_log": true, "minecraft:spruce_log": true,
	"minecraft:jungle_log": true, "minecraft:acacia_log": true, "minecraft:dark_oak_log": true,
	"minecraft:cherry_log": true, "minecraft:pale_oak_log": true, "minecraft:mangrove_log": true,
}

// logStateSet is the set of state ids whose block is a log (built lazily from logBlockIDs).
var logStateSet = buildLogStateSet()

func buildLogStateSet() map[block.StateID]bool {
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if b != nil && logBlockIDs[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// isLogState reports whether a state id is a log block.
func isLogState(st block.StateID) bool { return logStateSet[st] }

// ============================================================================
// parse dispatch (the Parse* arms filled in tree.go reference these constructors)
// ============================================================================

// parseCherryTrunkPlacer decodes a cherry_trunk_placer envelope.
func parseCherryTrunkPlacer(base trunkPlacerBase, raw json.RawMessage) (TrunkPlacer, error) {
	var j struct {
		BranchCount             json.RawMessage `json:"branch_count"`
		BranchHorizontalLength  json.RawMessage `json:"branch_horizontal_length"`
		BranchStartOffsetFromTop struct {
			MinInclusive int `json:"min_inclusive"`
			MaxInclusive int `json:"max_inclusive"`
		} `json:"branch_start_offset_from_top"`
		BranchEndOffsetFromTop json.RawMessage `json:"branch_end_offset_from_top"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: cherry_trunk_placer: %w", err)
	}
	bc, err := parseIntProvider(j.BranchCount)
	if err != nil {
		return nil, fmt.Errorf("feature: cherry_trunk_placer branch_count: %w", err)
	}
	bhl, err := parseIntProvider(j.BranchHorizontalLength)
	if err != nil {
		return nil, fmt.Errorf("feature: cherry_trunk_placer branch_horizontal_length: %w", err)
	}
	beo, err := parseIntProvider(j.BranchEndOffsetFromTop)
	if err != nil {
		return nil, fmt.Errorf("feature: cherry_trunk_placer branch_end_offset_from_top: %w", err)
	}
	return CherryTrunkPlacer{
		trunkPlacerBase:        base,
		branchCount:            bc,
		branchHorizontalLength: bhl,
		branchStartMin:         j.BranchStartOffsetFromTop.MinInclusive,
		branchStartMax:         j.BranchStartOffsetFromTop.MaxInclusive,
		branchEndOffsetFromTop: beo,
	}, nil
}

// parseUpwardsBranchingTrunkPlacer decodes an upwards_branching_trunk_placer envelope.
func parseUpwardsBranchingTrunkPlacer(base trunkPlacerBase, raw json.RawMessage) (TrunkPlacer, error) {
	var j struct {
		ExtraBranchSteps             json.RawMessage `json:"extra_branch_steps"`
		ExtraBranchLength            json.RawMessage `json:"extra_branch_length"`
		PlaceBranchPerLogProbability float64         `json:"place_branch_per_log_probability"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: upwards_branching_trunk_placer: %w", err)
	}
	steps, err := parseIntProvider(j.ExtraBranchSteps)
	if err != nil {
		return nil, fmt.Errorf("feature: upwards_branching_trunk_placer extra_branch_steps: %w", err)
	}
	length, err := parseIntProvider(j.ExtraBranchLength)
	if err != nil {
		return nil, fmt.Errorf("feature: upwards_branching_trunk_placer extra_branch_length: %w", err)
	}
	return UpwardsBranchingTrunkPlacer{
		trunkPlacerBase:              base,
		extraBranchSteps:             steps,
		extraBranchLength:            length,
		placeBranchPerLogProbability: float32(j.PlaceBranchPerLogProbability),
	}, nil
}

// parseBendingTrunkPlacer decodes a bending_trunk_placer envelope.
func parseBendingTrunkPlacer(base trunkPlacerBase, raw json.RawMessage) (TrunkPlacer, error) {
	var j struct {
		MinHeightForLeaves int             `json:"min_height_for_leaves"`
		BendLength         json.RawMessage `json:"bend_length"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: bending_trunk_placer: %w", err)
	}
	bend, err := parseIntProvider(j.BendLength)
	if err != nil {
		return nil, fmt.Errorf("feature: bending_trunk_placer bend_length: %w", err)
	}
	mh := j.MinHeightForLeaves
	if mh == 0 {
		mh = 1 // BendingTrunkPlacer default min_height_for_leaves
	}
	return BendingTrunkPlacer{trunkPlacerBase: base, minHeightForLeaves: mh, bendLength: bend}, nil
}

// parseCherryFoliagePlacer decodes a cherry_foliage_placer envelope.
func parseCherryFoliagePlacer(base foliagePlacerBase, raw json.RawMessage) (FoliagePlacer, error) {
	var j struct {
		Height                       json.RawMessage `json:"height"`
		WideBottomLayerHoleChance    float64         `json:"wide_bottom_layer_hole_chance"`
		CornerHoleChance             float64         `json:"corner_hole_chance"`
		HangingLeavesChance          float64         `json:"hanging_leaves_chance"`
		HangingLeavesExtensionChance float64         `json:"hanging_leaves_extension_chance"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: cherry_foliage_placer: %w", err)
	}
	h, err := intFromHeight(j.Height)
	if err != nil {
		return nil, fmt.Errorf("feature: cherry_foliage_placer height: %w", err)
	}
	return CherryFoliagePlacer{
		foliagePlacerBase:            base,
		height:                       h,
		wideBottomLayerHoleChance:    float32(j.WideBottomLayerHoleChance),
		cornerHoleChance:             float32(j.CornerHoleChance),
		hangingLeavesChance:          float32(j.HangingLeavesChance),
		hangingLeavesExtensionChance: float32(j.HangingLeavesExtensionChance),
	}, nil
}

// parseRandomSpreadFoliagePlacer decodes a random_spread_foliage_placer envelope.
func parseRandomSpreadFoliagePlacer(base foliagePlacerBase, raw json.RawMessage) (FoliagePlacer, error) {
	var j struct {
		FoliageHeight         json.RawMessage `json:"foliage_height"`
		LeafPlacementAttempts int             `json:"leaf_placement_attempts"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: random_spread_foliage_placer: %w", err)
	}
	fh, err := parseIntProvider(j.FoliageHeight)
	if err != nil {
		return nil, fmt.Errorf("feature: random_spread_foliage_placer foliage_height: %w", err)
	}
	return RandomSpreadFoliagePlacer{
		foliagePlacerBase:     base,
		foliageHeight:         fh,
		leafPlacementAttempts: j.LeafPlacementAttempts,
	}, nil
}

// parseMangroveRootPlacer decodes a mangrove_root_placer envelope (the RootPlacer subsystem).
func parseMangroveRootPlacer(raw json.RawMessage) (RootPlacer, error) {
	var j struct {
		TrunkOffsetY       json.RawMessage `json:"trunk_offset_y"`
		RootProvider       json.RawMessage `json:"root_provider"`
		AboveRootPlacement *struct {
			Chance   float64         `json:"above_root_placement_chance"`
			Provider json.RawMessage `json:"above_root_provider"`
		} `json:"above_root_placement"`
		MangroveRootPlacement struct {
			CanGrowThrough     string          `json:"can_grow_through"`
			MaxRootLength      int             `json:"max_root_length"`
			MaxRootWidth       int             `json:"max_root_width"`
			MuddyRootsIn       []string        `json:"muddy_roots_in"`
			MuddyRootsProvider json.RawMessage `json:"muddy_roots_provider"`
			RandomSkewChance   float64         `json:"random_skew_chance"`
		} `json:"mangrove_root_placement"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: mangrove_root_placer: %w", err)
	}
	toy, err := parseIntProvider(j.TrunkOffsetY)
	if err != nil {
		return nil, fmt.Errorf("feature: mangrove_root_placer trunk_offset_y: %w", err)
	}
	rootProv, err := ParseProvider(j.RootProvider)
	if err != nil {
		return nil, fmt.Errorf("feature: mangrove_root_placer root_provider: %w", err)
	}
	muddyProv, err := ParseProvider(j.MangroveRootPlacement.MuddyRootsProvider)
	if err != nil {
		return nil, fmt.Errorf("feature: mangrove_root_placer muddy_roots_provider: %w", err)
	}
	canGrow, err := resolveTreeBlockTagOrList(j.MangroveRootPlacement.CanGrowThrough)
	if err != nil {
		return nil, fmt.Errorf("feature: mangrove_root_placer can_grow_through: %w", err)
	}
	muddyIn, err := resolveBlockIDsToStates(j.MangroveRootPlacement.MuddyRootsIn)
	if err != nil {
		return nil, fmt.Errorf("feature: mangrove_root_placer muddy_roots_in: %w", err)
	}
	rp := &MangroveRootPlacer{
		trunkOffsetY:       toy,
		rootProvider:       rootProv,
		maxRootLength:      j.MangroveRootPlacement.MaxRootLength,
		maxRootWidth:       j.MangroveRootPlacement.MaxRootWidth,
		randomSkewChance:   float32(j.MangroveRootPlacement.RandomSkewChance),
		muddyRootsIn:       muddyIn,
		muddyRootsProvider: muddyProv,
		canGrowThrough:     canGrow,
	}
	if j.AboveRootPlacement != nil {
		ap, err := ParseProvider(j.AboveRootPlacement.Provider)
		if err != nil {
			return nil, fmt.Errorf("feature: mangrove_root_placer above_root_provider: %w", err)
		}
		rp.hasAboveRoot = true
		rp.aboveRootChance = float32(j.AboveRootPlacement.Chance)
		rp.aboveRootProvider = ap
	}
	return rp, nil
}

// resolveBlockIDsToStates resolves a list of block ids to the set of all their state ids.
func resolveBlockIDsToStates(ids []string) (map[block.StateID]bool, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if b != nil && want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set, nil
}

// resolveTreeBlockTagOrList resolves a #tag (or a single block id) to the set of member state
// ids for the root can-grow-through gate. The mangrove tags flatten constant-for-constant.
func resolveTreeBlockTagOrList(ref string) (map[block.StateID]bool, error) {
	if ref == "" {
		return nil, fmt.Errorf("empty can_grow_through ref")
	}
	if ref[0] == '#' {
		members, ok := mangroveGrowThroughTags[ref[1:]]
		if !ok {
			return nil, fmt.Errorf("unported mangrove tag %q (add it constant-for-constant from the jar)", ref)
		}
		return resolveBlockIDsToStates(members)
	}
	return resolveBlockIDsToStates([]string{ref})
}

// mangroveGrowThroughTags flattens the #mangrove_roots_can_grow_through tag (verified from the
// jar tag def: the blocks a mangrove root may grow through/displace).
var mangroveGrowThroughTags = map[string][]string{
	"minecraft:mangrove_roots_can_grow_through": {
		"minecraft:mud", "minecraft:muddy_mangrove_roots", "minecraft:mangrove_roots",
		"minecraft:mangrove_log", "minecraft:mangrove_propagule", "minecraft:moss_carpet",
		"minecraft:vine", "minecraft:mangrove_leaves",
	},
	"minecraft:mangrove_logs_can_grow_through": {
		"minecraft:mud", "minecraft:muddy_mangrove_roots", "minecraft:mangrove_roots",
		"minecraft:mangrove_log", "minecraft:mangrove_propagule", "minecraft:moss_carpet",
		"minecraft:vine",
	},
}

// parseAttachedToLeavesDecorator decodes an attached_to_leaves decorator envelope.
func parseAttachedToLeavesDecorator(raw json.RawMessage) (TreeDecorator, error) {
	var j struct {
		Probability       float64         `json:"probability"`
		ExclusionRadiusXZ int             `json:"exclusion_radius_xz"`
		ExclusionRadiusY  int             `json:"exclusion_radius_y"`
		RequiredEmpty     int             `json:"required_empty_blocks"`
		Directions        []string        `json:"directions"`
		BlockProvider     json.RawMessage `json:"block_provider"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: attached_to_leaves: %w", err)
	}
	prov, err := ParseProvider(j.BlockProvider)
	if err != nil {
		return nil, fmt.Errorf("feature: attached_to_leaves block_provider: %w", err)
	}
	dirs := make([]hdir, 0, len(j.Directions))
	for _, dn := range j.Directions {
		d, err := dirFromName(dn)
		if err != nil {
			return nil, fmt.Errorf("feature: attached_to_leaves direction: %w", err)
		}
		dirs = append(dirs, d)
	}
	return AttachedToLeavesDecorator{
		probability:       float32(j.Probability),
		exclusionRadiusXZ: j.ExclusionRadiusXZ,
		exclusionRadiusY:  j.ExclusionRadiusY,
		requiredEmpty:     j.RequiredEmpty,
		directions:        dirs,
		blockProvider:     prov,
	}, nil
}

// dirFromName maps a Direction name to its step (down -> the special downDir).
func dirFromName(n string) (hdir, error) {
	switch n {
	case "down":
		return downDir, nil
	case "north":
		return hdir{0, -1}, nil
	case "south":
		return hdir{0, 1}, nil
	case "west":
		return hdir{-1, 0}, nil
	case "east":
		return hdir{1, 0}, nil
	default:
		return hdir{}, fmt.Errorf("unported direction %q", n)
	}
}

// parsePaleMossDecorator decodes a pale_moss decorator envelope.
func parsePaleMossDecorator(raw json.RawMessage) (TreeDecorator, error) {
	var j struct {
		GroundProbability float64 `json:"ground_probability"`
		LeavesProbability float64 `json:"leaves_probability"`
		TrunkProbability  float64 `json:"trunk_probability"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: pale_moss: %w", err)
	}
	return PaleMossDecorator{
		groundProbability: float32(j.GroundProbability),
		leavesProbability: float32(j.LeavesProbability),
		trunkProbability:  float32(j.TrunkProbability),
	}, nil
}

// parseCreakingHeartDecorator decodes a creaking_heart decorator envelope.
func parseCreakingHeartDecorator(raw json.RawMessage) (TreeDecorator, error) {
	var j struct {
		Probability float64 `json:"probability"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: creaking_heart: %w", err)
	}
	return CreakingHeartDecorator{probability: float32(j.Probability)}, nil
}

// compile-time interface assertions for the special placers/root/decorators.
var (
	_ TrunkPlacer = CherryTrunkPlacer{}
	_ TrunkPlacer = UpwardsBranchingTrunkPlacer{}
	_ TrunkPlacer = BendingTrunkPlacer{}

	_ FoliagePlacer = CherryFoliagePlacer{}
	_ FoliagePlacer = RandomSpreadFoliagePlacer{}

	_ RootPlacer = (*MangroveRootPlacer)(nil)

	_ TreeDecorator = AttachedToLeavesDecorator{}
	_ TreeDecorator = PaleMossDecorator{}
	_ TreeDecorator = CreakingHeartDecorator{}

	_ = math.Pi // keep the math import if the cherry arch math is trimmed
)
