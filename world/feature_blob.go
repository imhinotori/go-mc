package world

// feature_blob.go ports the two overworld/nether "blob" terrain features that were
// unregistered no-ops:
//
//   - block_blob             (BlockBlobFeature)     — the forest_rock mossy-cobble mound
//   - netherrack_replace_blobs (ReplaceBlobsFeature) — the basalt/blackstone nether blobs
//
// Both are transcribed javap-exact from temp/cache/26.2-inner.jar; the RNG DRAW ORDER is
// the determinism contract (T-12-05). All writes go through bctx.placeState ->
// Neighborhood.SetBlock (cross-chunk + live heightmap). Reads use ctx.GetBlock (the live
// worldgen view — placementContext.GetBlock reads the same Neighborhood).
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.BlockBlobFeature.place
//   - net.minecraft.world.level.levelgen.feature.ReplaceBlobsFeature.{place,findTarget}
//   - net.minecraft.core.BlockPos.distSqr(Vec3i) / distManhattan(Vec3i)
//   - net.minecraft.world.level.levelgen.blockpredicates.BlockPredicate (block_blob's
//     can_place_on — parsed via placement.ParsePredicate, the existing ported set)

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("block_blob", blockBlobBody)
	registerFeatureBody("netherrack_replace_blobs", replaceBlobsBody)
}

// ---- block_blob (BlockBlobFeature) ----

// blockBlobConfig is BlockBlobConfiguration: a state (the mound block) + a can_place_on
// BlockPredicate (the surface the mound may sit on).
type blockBlobConfig struct {
	State json.RawMessage `json:"state"`
	// The vanilla codec field is "can_place_on" (a BlockPredicate).
	CanPlaceOn json.RawMessage `json:"can_place_on"`
}

// blockBlobDecoded memoizes the block_blob decode per ConfiguredFeature (oreConfigCache
// pattern): the resolved mound state + the parsed can_place_on predicate.
type blockBlobDecoded struct {
	state      block.StateID
	canPlaceOn placement.BlockPredicate
	err        error
}

var blockBlobCache sync.Map // map[*feature.ConfiguredFeature]*blockBlobDecoded

func decodeBlockBlobCached(cf *feature.ConfiguredFeature) *blockBlobDecoded {
	if v, ok := blockBlobCache.Load(cf); ok {
		return v.(*blockBlobDecoded)
	}
	d := &blockBlobDecoded{}
	var cfg blockBlobConfig
	if err := json.Unmarshal(configRaw(cf), &cfg); err != nil {
		d.err = fmt.Errorf("world: block_blob config %q: %w", cf.ID, err)
		blockBlobCache.Store(cf, d)
		return d
	}
	st, err := feature.ResolveBlockStateJSON(cfg.State)
	if err != nil {
		d.err = fmt.Errorf("world: block_blob state %q: %w", cf.ID, err)
		blockBlobCache.Store(cf, d)
		return d
	}
	d.state = st
	pred, err := placement.ParsePredicate(cfg.CanPlaceOn)
	if err != nil {
		d.err = fmt.Errorf("world: block_blob can_place_on %q: %w", cf.ID, err)
		blockBlobCache.Store(cf, d)
		return d
	}
	d.canPlaceOn = pred
	blockBlobCache.Store(cf, d)
	return d
}

// blockBlobBody ports BlockBlobFeature.place (javap -c) draw order:
//
//	// lower to a can_place_on surface (NO draws):
//	while pos.Y > minY+3 && !canPlaceOn.test(pos.below()): pos = pos.below()
//	if pos.Y <= minY+3: return false
//	for l in [0,3):                                        // 3 rounds
//	    i7 = nextInt(2); i8 = nextInt(2); i9 = nextInt(2)  // 3 draws
//	    f10 = (i7+i8+i9)*0.333f + 0.5f
//	    for p2 in betweenClosed(pos.offset(-i7,-i8,-i9), pos.offset(i7,i8,i9)):
//	        if p2.distSqr(pos) <= (double)(f10*f10): setBlock(p2, state)   // no draws
//	    pos = pos.offset(-1+nextInt(2), -nextInt(2), -1+nextInt(2))        // 3 draws (x,y,z)
//	return true
func blockBlobBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	d := decodeBlockBlobCached(cf)
	if d.err != nil {
		// A config/predicate decode failure is a build-data error; panic so it surfaces
		// during generation rather than silently placing nothing (T-12-07).
		panic(d.err.Error())
	}

	minY := bctx.minY()
	pos := origin
	// Lower `pos` to a surface where canPlaceOn holds for pos.below() (no rng draws). The
	// predicate reads the LIVE view via ctx (placementContext.GetBlock -> Neighborhood).
	for pos.Y > minY+3 {
		if d.canPlaceOn.Test(ctx, pos.X, pos.Y-1, pos.Z) {
			break
		}
		pos.Y--
	}
	if pos.Y <= minY+3 {
		return false
	}

	for round := 0; round < 3; round++ {
		i7 := int(rng.NextIntN(2))
		i8 := int(rng.NextIntN(2))
		i9 := int(rng.NextIntN(2))
		f10 := float32(i7+i8+i9)*0.333 + 0.5
		// betweenClosed(pos.offset(-i7,-i8,-i9) .. pos.offset(i7,i8,i9)). The cursor order
		// does not affect the draw stream (no draws inside), so the placed set is
		// order-independent; each candidate p2 is kept iff distSqr(pos) <= f10^2.
		radSq := float64(f10 * f10)
		for x := pos.X - i7; x <= pos.X+i7; x++ {
			for y := pos.Y - i8; y <= pos.Y+i8; y++ {
				for z := pos.Z - i9; z <= pos.Z+i9; z++ {
					dx := x - pos.X
					dy := y - pos.Y
					dz := z - pos.Z
					distSq := float64(dx*dx + dy*dy + dz*dz)
					if distSq <= radSq {
						bctx.placeState(placement.BlockPos{X: x, Y: y, Z: z}, d.state)
					}
				}
			}
		}
		// pos = pos.offset(-1 + nextInt(2), -nextInt(2), -1 + nextInt(2)) — 3 draws (x,y,z).
		ox := -1 + int(rng.NextIntN(2))
		oy := -int(rng.NextIntN(2))
		oz := -1 + int(rng.NextIntN(2))
		pos = placement.BlockPos{X: pos.X + ox, Y: pos.Y + oy, Z: pos.Z + oz}
	}
	return true
}

// ---- netherrack_replace_blobs (ReplaceBlobsFeature) ----

// replaceSphereConfig is ReplaceSphereConfiguration: the target block (which existing
// block is replaced), the replace state (what it becomes), and a radius IntProvider.
type replaceSphereConfig struct {
	Target json.RawMessage `json:"target"` // targetState (the block to find + replace)
	State  json.RawMessage `json:"state"`  // replaceState (the block placed)
	Radius json.RawMessage `json:"radius"` // IntProvider
}

// replaceBlobsDecoded memoizes the netherrack_replace_blobs decode per ConfiguredFeature:
// the target-block state set (BlockState.is(Block) is property-agnostic), the replace
// state, and the radius IntProvider.
type replaceBlobsDecoded struct {
	targetSet   map[block.StateID]bool
	replaceStat block.StateID
	radius      *miscIntProvider
	err         error
}

var replaceBlobsCache sync.Map // map[*feature.ConfiguredFeature]*replaceBlobsDecoded

func decodeReplaceBlobsCached(cf *feature.ConfiguredFeature) *replaceBlobsDecoded {
	if v, ok := replaceBlobsCache.Load(cf); ok {
		return v.(*replaceBlobsDecoded)
	}
	d := &replaceBlobsDecoded{}
	var cfg replaceSphereConfig
	if err := json.Unmarshal(configRaw(cf), &cfg); err != nil {
		d.err = fmt.Errorf("world: netherrack_replace_blobs config %q: %w", cf.ID, err)
		replaceBlobsCache.Store(cf, d)
		return d
	}
	// targetState.getBlock() — match ALL states of that block (BlockState.is(Block)).
	var ts struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(cfg.Target, &ts); err != nil || ts.Name == "" {
		d.err = fmt.Errorf("world: netherrack_replace_blobs target %q: %v", cf.ID, err)
		replaceBlobsCache.Store(cf, d)
		return d
	}
	set, err := resolveOreBlockSet(ts.Name)
	if err != nil {
		d.err = fmt.Errorf("world: netherrack_replace_blobs target %q: %w", cf.ID, err)
		replaceBlobsCache.Store(cf, d)
		return d
	}
	d.targetSet = set
	rs, err := feature.ResolveBlockStateJSON(cfg.State)
	if err != nil {
		d.err = fmt.Errorf("world: netherrack_replace_blobs state %q: %w", cf.ID, err)
		replaceBlobsCache.Store(cf, d)
		return d
	}
	d.replaceStat = rs
	rad, err := parseMiscIntProvider(cfg.Radius)
	if err != nil {
		d.err = fmt.Errorf("world: netherrack_replace_blobs radius %q: %w", cf.ID, err)
		replaceBlobsCache.Store(cf, d)
		return d
	}
	d.radius = rad
	replaceBlobsCache.Store(cf, d)
	return d
}

// replaceBlobsBody ports ReplaceBlobsFeature.place (javap -c) draw order:
//
//	targetBlock = config.targetState.getBlock()
//	// findTarget: from origin.Y clamped to [minY+1, maxY], walk DOWN while Y > minY+1,
//	// returning the first pos whose block is(targetBlock); null -> return false.  NO draws.
//	i7 = radius.sample(rng); i8 = radius.sample(rng); i9 = radius.sample(rng)  // 3 draws
//	i10 = max(max(i7,i8),i9)
//	for pos in withinManhattan(target, i7, i8, i9):
//	    if pos.distManhattan(target) > i10: break              // (monotone -> a filter)
//	    if getBlockState(pos).is(targetBlock): setBlock(pos, replaceState); placed=true
//	return placed
func replaceBlobsBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	d := decodeReplaceBlobsCached(cf)
	if d.err != nil {
		panic(d.err.Error())
	}

	minY := ctx.MinY()
	maxY := ctx.MinY() + ctx.Height() - 1 // WorldGenLevel.getMaxY() (inclusive top)
	// clamp(Y, minY+1, maxY): the mutable start Y.
	startY := origin.Y
	if startY < minY+1 {
		startY = minY + 1
	}
	if startY > maxY {
		startY = maxY
	}

	// findTarget: walk down while Y > minY+1, return first pos whose block is a target.
	target, ok := replaceBlobsFindTarget(bctx, origin.X, startY, origin.Z, minY, d.targetSet)
	if !ok {
		return false
	}

	// 3 radius samples (the determinism contract — uniform IntProvider: 1 draw each).
	i7 := d.radius.sample(rng)
	i8 := d.radius.sample(rng)
	i9 := d.radius.sample(rng)
	i10 := max(max(i7, i8), i9)

	placed := false
	// withinManhattan(target, i7,i8,i9) filtered by distManhattan <= i10. The withinManhattan
	// iterator is ordered by increasing Manhattan distance, so the jar's `> i10 break` is
	// exactly this predicate; iterating the per-axis box and filtering yields the identical
	// placed set (no rng draws inside the loop, so order is immaterial to determinism).
	for dx := -i7; dx <= i7; dx++ {
		for dy := -i8; dy <= i8; dy++ {
			for dz := -i9; dz <= i9; dz++ {
				manhattan := abs(dx) + abs(dy) + abs(dz)
				if manhattan > i10 {
					continue
				}
				px, py, pz := target.X+dx, target.Y+dy, target.Z+dz
				if d.targetSet[bctx.getState(placement.BlockPos{X: px, Y: py, Z: pz})] {
					bctx.placeState(placement.BlockPos{X: px, Y: py, Z: pz}, d.replaceStat)
					placed = true
				}
			}
		}
	}
	return placed
}

// replaceBlobsFindTarget ports ReplaceBlobsFeature.findTarget: from (x, startY, z) walk
// DOWN while Y > minY+1, returning the first position whose block is in targetSet. NO
// draws.
func replaceBlobsFindTarget(bctx *bodyContext, x, startY, z, minY int, targetSet map[block.StateID]bool) (placement.BlockPos, bool) {
	y := startY
	for y > minY+1 {
		p := placement.BlockPos{X: x, Y: y, Z: z}
		if targetSet[bctx.getState(p)] {
			return p, true
		}
		y--
	}
	return placement.BlockPos{}, false
}
