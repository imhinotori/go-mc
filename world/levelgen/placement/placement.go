package placement

import "github.com/imhinotori/sulfur/world/levelgen"

// BlockPos is a worldgen block position. The placement modifiers transform an
// origin BlockPos into the set of anchor positions a feature body runs at. Fields
// are int (BlockPos coords); the RNG returns int32, so the modifier bodies widen
// int32(rng.NextIntN(16)) + p.X (an expected, faithful conversion).
type BlockPos struct {
	X, Y, Z int
}

// PlacementModifier is one stage of a PlacedFeature's modifier chain. getPositions
// maps one input position to zero-or-more output positions, consuming the threaded
// RandomSource in JAR-exact draw order. The fold in place.go applies the whole
// chain as a sequential flatMap (research "PlacedFeature.placeWithContext").
//
// JAR-CONFIRMED interface: net.minecraft.world.level.levelgen.placement.PlacementModifier.
// getPositions(PlacementContext, RandomSource, BlockPos) -> Stream<BlockPos>.
type PlacementModifier interface {
	// getPositions transforms p into the output positions, threading rng.
	getPositions(ctx PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos
}

// ---- PlacementFilter (abstract base) ----
//
// JAR-CONFIRMED (PlacementFilter.getPositions): shouldPlace ? Stream.of(pos) :
// Stream.empty(). rarity_filter / biome / surface_water_depth_filter all extend it
// — they supply only a shouldPlace predicate.

// shouldPlacer is the predicate a PlacementFilter wraps. It returns true to keep p,
// false to drop it; it consumes rng exactly as the corresponding vanilla
// shouldPlace body does (rarity_filter: 1 NextFloat; biome/water: 0 draws).
type shouldPlacer interface {
	shouldPlace(ctx PlacementContext, rng levelgen.RandomSource, p BlockPos) bool
}

// placementFilter is the embeddable base implementing the
// PlacementFilter.getPositions branch. A concrete filter embeds it and provides
// shouldPlace via the predicate set at construction.
type placementFilter struct {
	predicate shouldPlacer
}

// getPositions ports PlacementFilter.getPositions: keep {p} iff shouldPlace, else {}.
func (f placementFilter) getPositions(ctx PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	if f.predicate.shouldPlace(ctx, rng, p) {
		return []BlockPos{p}
	}
	return nil
}

// ---- RepeatingPlacement (abstract base) ----
//
// JAR-CONFIRMED (RepeatingPlacement.getPositions): n = count(rng, pos);
// IntStream.range(0,n).mapToObj(_ -> pos) — n copies of p. The draws all happen in
// count() BEFORE the copies are emitted. CountPlacement is the only overworld
// subclass; count(rng,pos) = its IntProvider.Sample(rng).

// counter is the per-input count a RepeatingPlacement draws. CountPlacement returns
// its IntProvider.Sample(rng); the count draws happen before the n copies.
type counter interface {
	count(rng levelgen.RandomSource, p BlockPos) int
}

// repeatingPlacement is the embeddable base implementing
// RepeatingPlacement.getPositions: count(rng,p) copies of p.
type repeatingPlacement struct {
	counter counter
}

// getPositions ports RepeatingPlacement.getPositions: draw n, then emit n copies.
func (r repeatingPlacement) getPositions(_ PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	n := r.counter.count(rng, p)
	if n <= 0 {
		return nil
	}
	out := make([]BlockPos, n)
	for i := range out {
		out[i] = p
	}
	return out
}
