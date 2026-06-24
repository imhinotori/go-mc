package biome

import (
	"math"
	"sort"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
)

// rtree ports net.minecraft.world.level.biome.Climate$RTree — the 6-D (really 7-D, with the
// offset axis) bounding-volume hierarchy that vanilla uses to answer findValue in O(log n)-ish
// time instead of the O(n) linear fitness scan of findValueBruteForce. For the ~7594-box
// overworld parameter list, sampled per quart cell (and again by the surface rule's biome
// condition), the linear scan dominated chunk generation (~985ms/chunk in the CPU profile) and
// caused real clients to time out on "Loading terrain". The RTree returns the IDENTICAL nearest
// box as the linear scan (it is built from the same parameter list and the per-node bounding-box
// distance is a true lower bound on every leaf fitness in the subtree, so pruning never skips a
// closer box) — it is a pure throughput swap, behavior-identical. TestRTreeMatchesLinearScan is
// the safety gate that asserts this.
//
// PORTED FROM (javap -c -p, temp/cache/26.2-inner.jar):
//   - Climate$RTree.create / build / sort / comparator / bucketize / cost / buildParameterSpace
//   - Climate$RTree$Node.distance(long[]) : sum over the 7 axes of square(parameter[i].distance(point[i]))
//   - Climate$RTree$Leaf.search           : returns itself (its fitness == Node.distance for a leaf)
//   - Climate$RTree$SubTree.search        : recurse into children whose bounding-box distance < best, pruning
//
// NODE STRUCTURE: every node carries a 7-element parameterSpace bounding box (the per-axis min/max
// span of every leaf beneath it). A leaf's box is the box itself plus the degenerate offset axis
// [offset,offset]; the 7th target coord is always 0 (Climate$TargetPoint.toParameterArray) so a
// leaf's distance reduces to ParameterPoint.fitness exactly. A subtree's box is the per-axis span
// of its children's boxes (buildParameterSpace), and Node.distance over that box LOWER-BOUNDS the
// fitness of every leaf inside it — which is what makes the prune (skip a subtree whose box
// distance already exceeds best-so-far) exact.

// rtreeChildrenPerNode is Climate$RTree.CHILDREN_PER_NODE (6): the bucketize fan-out and the
// "small node, just sort + wrap" threshold in build.
const rtreeChildrenPerNode = 6

// rtreeAxes is the dimensionality of a parameterSpace box: 6 climate axes + the offset axis.
const rtreeAxes = 7

// rtreeNode is the common shape of a Leaf or a SubTree (ports Climate$RTree$Node). parameterSpace
// is the node's 7-axis bounding box. Exactly one of leaf/children is set: a leaf has biome set and
// no children; a subtree has children and the zero biome.
type rtreeNode struct {
	parameterSpace [rtreeAxes]Parameter
	children       []*rtreeNode // nil for a leaf
	biome          levelbiome.Type
	isLeaf         bool

	// order is the leaf's original index in the parameter-box list (only meaningful for a
	// leaf). It is the EARLIEST-WINS tiebreak key: vanilla's findValueIndex (the RTree) and
	// findValueBruteForce (the linear scan) can disagree on an EXACT fitness tie because the
	// tree traversal order differs from the list order. To stay behavior-identical to the
	// linear scan (and to the world already validated by the determinism tests — earliest box
	// wins a tie), search() prefers the leaf with the lower `order` when two leaves are exactly
	// equidistant. This makes the RTree result == the brute-force result for ALL targets, which
	// TestRTreeMatchesLinearScan asserts.
	order int
}

// distance ports Climate$RTree$Node.distance(long[]): sum over the 7 axes of
// square(parameterSpace[i].distance(point[i])). For a leaf this is identical to
// ParameterPoint.fitness(target); for a subtree it is a lower bound on every leaf fitness inside
// it (the bounding box contains all child boxes, so being outside the bounding box on an axis is
// no farther than being outside any child's span on that axis). point is the 7-element target
// array (the 6 quantized climate coords + a trailing 0 for the offset axis).
func (n *rtreeNode) distance(point *[rtreeAxes]int64) int64 {
	var d int64
	for i := 0; i < rtreeAxes; i++ {
		d += square(n.parameterSpace[i].distance(point[i]))
	}
	return d
}

// search ports Climate$RTree$Node.search. A leaf returns itself. A subtree visits its children in
// declaration order, but only recurses into a child whose bounding-box distance is strictly less
// than the best leaf distance found so far (the prune) — and seeds "best" with the incoming
// alreadyChosen leaf (the lastResult cache in vanilla; here we pass nil on the first call). It
// returns the nearest leaf. The tie behavior matches the bytecode: a candidate must be STRICTLY
// closer (distance < best) to displace the current best, so the earliest-visited equal-distance
// leaf wins — the same first-match-wins tiebreak as findValueBruteForce, given that build
// preserves the box list order along each bucket.
func (n *rtreeNode) search(point *[rtreeAxes]int64, alreadyChosen *rtreeNode) *rtreeNode {
	if n.isLeaf {
		return n
	}

	// best distance so far: the incoming chosen leaf's distance, or +inf if none yet.
	bestDist := int64(math.MaxInt64)
	if alreadyChosen != nil {
		bestDist = alreadyChosen.distance(point)
	}
	best := alreadyChosen

	for _, child := range n.children {
		childDist := child.distance(point)
		// PRUNE: descend only if the child's bounding-box lower bound can beat OR TIE the
		// best — equal-distance subtrees must be explored so the earliest-order leaf (the
		// linear-scan tiebreak) can be discovered, not just the first one the traversal hits.
		if childDist > bestDist {
			continue
		}
		candidate := child.search(point, best)
		var candDist int64
		if candidate == child {
			candDist = childDist // child was a leaf returned unchanged
		} else {
			candDist = candidate.distance(point)
		}
		// Take the candidate if it is strictly closer, OR exactly as close but an EARLIER
		// box (lower order) — matching findValueBruteForce's earliest-wins tie. best can be
		// nil on the first child (no incoming chosen leaf); any candidate then wins.
		if best == nil || candDist < bestDist || (candDist == bestDist && candidate.order < best.order) {
			bestDist = candDist
			best = candidate
		}
	}
	return best
}

// buildRTree ports Climate$RTree.create: it wraps every parameter box as a leaf, then build()s
// the hierarchy with splitDim = parameterSpace().size() = 7 (rtreeAxes). The boxes are taken in
// their list order (the embedded JSON order) so the nearest-leaf tiebreak stays deterministic and
// matches the linear scan's first-match-wins. splitDim stays constant (7) through every recursion
// level (vanilla's lambda$build$1 passes the same splitDim down), so the comparator chains/sums
// always index valid axes via (sortDim+k) % splitDim.
func buildRTree(boxes []ParameterPoint) *rtreeNode {
	if len(boxes) == 0 {
		return nil
	}
	leaves := make([]*rtreeNode, len(boxes))
	for i := range boxes {
		leaves[i] = newLeaf(&boxes[i], i)
	}
	return rtreeBuild(rtreeAxes, leaves)
}

// newLeaf ports Climate$RTree$Leaf's construction: the leaf's parameterSpace is the box's 6
// climate spans plus the degenerate offset span [offset,offset] (ParameterPoint.parameterSpace).
// order is the box's index in the original list — the earliest-wins tiebreak key (see rtreeNode).
func newLeaf(b *ParameterPoint, order int) *rtreeNode {
	return &rtreeNode{
		parameterSpace: [rtreeAxes]Parameter{
			b.Temperature,
			b.Humidity,
			b.Continentalness,
			b.Erosion,
			b.Depth,
			b.Weirdness,
			{Min: b.Offset, Max: b.Offset},
		},
		biome:  b.Biome,
		isLeaf: true,
		order:  order,
	}
}

// rtreeBuild ports Climate$RTree.build(int splitDim, List children). For one child it returns it
// directly. For <= CHILDREN_PER_NODE children it sorts by the SUM of midpoints over all splitDim
// axes (lambda$build$0 — a single comparingLong, no abs) and wraps them in one SubTree.
//
// Otherwise it picks the split axis with the minimum bucketize cost: for each axis it does the
// CHAINED sort (rtreeSort, abs=false), bucketizes into SubTrees, and sums each bucket-SubTree's
// box cost. The cheapest axis's SAVED bucket list is then re-sorted as a LIST OF BUCKET SUBTREES
// (rtreeSort on the buckets, abs=true), and each bucket-SubTree is recursively build()d from its
// children (same splitDim) before all are wrapped in the final SubTree.
//
// IMPORTANT (matches the bytecode exactly): bucketize wraps each bucket in a SubTree up front, the
// trial cost reads that SubTree's own bounding box, the winning axis re-sort operates on the
// SAVED bucket SubTrees (not a re-bucketize of the leaves), and the recursion preserves splitDim.
func rtreeBuild(splitDim int, children []*rtreeNode) *rtreeNode {
	if len(children) == 0 {
		panic("biome rtree: need at least one child to build a node")
	}
	if len(children) == 1 {
		return children[0]
	}
	if len(children) <= rtreeChildrenPerNode {
		rtreeSortByMidpointSum(children, splitDim)
		return newSubTree(children)
	}

	bestCost := int64(math.MaxInt64)
	var bestBuckets []*rtreeNode // the winning axis's bucketized SubTrees
	bestAxis := -1
	for axis := 0; axis < splitDim; axis++ {
		rtreeSort(children, splitDim, axis, false)
		buckets := rtreeBucketize(children) // []*rtreeNode, each a SubTree
		var totalCost int64
		for _, b := range buckets {
			totalCost += rtreeCost(b.parameterSpace)
		}
		if totalCost < bestCost {
			bestCost = totalCost
			bestBuckets = buckets
			bestAxis = axis
		}
	}

	// Re-sort the SAVED bucket SubTrees by the winning axis (chained comparator, abs=true), then
	// recurse into each bucket's children (same splitDim) and wrap all in the final SubTree.
	rtreeSort(bestBuckets, splitDim, bestAxis, true)
	subTrees := make([]*rtreeNode, len(bestBuckets))
	for i, bucket := range bestBuckets {
		subTrees[i] = rtreeBuild(splitDim, bucket.children)
	}
	return newSubTree(subTrees)
}

// rtreeSortByMidpointSum ports Climate$RTree.build's <=6 branch comparator (lambda$build$0): sort
// the nodes by the SUM over axes 0..splitDim-1 of each box's midpoint (min+max)/2 (no abs).
func rtreeSortByMidpointSum(nodes []*rtreeNode, splitDim int) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return midpointSum(nodes[i], splitDim) < midpointSum(nodes[j], splitDim)
	})
}

// midpointSum is lambda$build$0's key: sum over axes 0..splitDim-1 of (min+max)/2 (integer /2).
func midpointSum(n *rtreeNode, splitDim int) int64 {
	var s int64
	for axis := 0; axis < splitDim; axis++ {
		p := n.parameterSpace[axis]
		s += (p.Min + p.Max) / 2
	}
	return s
}

// newSubTree ports Climate$RTree$SubTree's construction: its parameterSpace is buildParameterSpace
// over the children (the per-axis span of every child's box).
func newSubTree(children []*rtreeNode) *rtreeNode {
	return &rtreeNode{
		parameterSpace: bucketBox(children),
		children:       children,
		isLeaf:         false,
	}
}

// bucketBox ports Climate$RTree.buildParameterSpace: the per-axis bounding box of a set of nodes,
// i.e. for each of the 7 axes, the [min(child mins), max(child maxes)] span (Parameter.span fold).
func bucketBox(children []*rtreeNode) [rtreeAxes]Parameter {
	var box [rtreeAxes]Parameter
	for axis := 0; axis < rtreeAxes; axis++ {
		box[axis] = children[0].parameterSpace[axis]
	}
	for i := 1; i < len(children); i++ {
		for axis := 0; axis < rtreeAxes; axis++ {
			c := children[i].parameterSpace[axis]
			if c.Min < box[axis].Min {
				box[axis].Min = c.Min
			}
			if c.Max > box[axis].Max {
				box[axis].Max = c.Max
			}
		}
	}
	return box
}

// rtreeCost ports Climate$RTree.cost: the sum over the 7 axes of abs(max - min) of the box — the
// total span "volume" (L1) used to pick the cheapest split axis.
func rtreeCost(box [rtreeAxes]Parameter) int64 {
	var total int64
	for axis := 0; axis < rtreeAxes; axis++ {
		d := box[axis].Max - box[axis].Min
		if d < 0 {
			d = -d
		}
		total += d
	}
	return total
}

// rtreeSort ports Climate$RTree.sort(list, splitDim, sortDim, abs): sort the nodes by a comparator
// chain that starts at axis sortDim and wraps through splitDim consecutive axes ((sortDim+k)%splitDim),
// each comparing the box's midpoint on that axis (abs(midpoint) if abs). The chain makes the order
// total + deterministic; Go's sort.SliceStable preserves the input order for fully-equal nodes,
// matching Java's stable List.sort.
func rtreeSort(nodes []*rtreeNode, splitDim, sortDim int, abs bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		for k := 0; k < splitDim; k++ {
			axis := (sortDim + k) % splitDim
			ki := midpointKey(nodes[i], axis, abs)
			kj := midpointKey(nodes[j], axis, abs)
			if ki != kj {
				return ki < kj
			}
		}
		return false
	})
}

// midpointKey ports Climate$RTree.lambda$comparator$0: the comparator key for an axis is the box's
// midpoint (min+max)/2 (integer division, matching Java's long /2), abs() if requested.
func midpointKey(n *rtreeNode, axis int, abs bool) int64 {
	p := n.parameterSpace[axis]
	mid := (p.Min + p.Max) / 2
	if abs && mid < 0 {
		mid = -mid
	}
	return mid
}

// rtreeBucketize ports Climate$RTree.bucketize: group the (already-sorted) nodes into consecutive
// buckets of size pow(6, floor(log(n-0.01)/log(6))), each wrapped in a SubTree, with the trailing
// partial bucket flushed as its own SubTree. This is the sqrt-style fan-out that keeps the tree
// shallow. Returns the list of bucket SubTrees (each carries its own bounding box) — exactly as
// the bytecode (the trial cost reads SubTree.parameterSpace, so the wrapping must happen here).
func rtreeBucketize(nodes []*rtreeNode) []*rtreeNode {
	bucketSize := int(math.Pow(
		rtreeChildrenPerNode,
		math.Floor(math.Log(float64(len(nodes))-0.01)/math.Log(rtreeChildrenPerNode)),
	))
	var result []*rtreeNode
	var current []*rtreeNode
	for _, n := range nodes {
		current = append(current, n)
		if len(current) >= bucketSize {
			result = append(result, newSubTree(current))
			current = nil
		}
	}
	if len(current) != 0 {
		result = append(result, newSubTree(current))
	}
	return result
}
