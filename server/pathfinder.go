package server

// pathfinder.go — AI-02: the ported PathFinder A* — the PURE request->snapshot->result compute
// (THE Phase-8 hinge). PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL paste)
// from the unobfuscated 26.2 jar (javap -c, this session):
//
//   net.minecraft.world.level.pathfinder.PathFinder.findPath (bytecode, the A* loop):
//     start.g = 0; start.h = getBestH(start, targets); start.f = start.h;
//     openSet.clear(); openSet.insert(start);
//     maxVisited = (int)(maxVisitedNodes * searchDepthMultiplier);   // the BUDGET (Pitfall 6)
//     while !openSet.isEmpty() && ++visited < maxVisited:
//       node = openSet.pop(); node.closed = true;
//       for each target: if node.distanceManhattan(target) <= reachRange: target.setReached();
//                        add to reachedTargets;
//       if !reachedTargets.isEmpty(): break;
//       if node.distanceTo(start) >= followRange: continue;           // the follow-range gate
//       count = nodeEvaluator.getNeighbors(neighbors[], node);
//       for i in 0..count:
//         neighbor = neighbors[i];
//         edge = distance(node, neighbor);                            // = node.distanceTo(neighbor)
//         neighbor.walkedDistance = node.walkedDistance + edge;
//         g = node.g + edge + neighbor.costMalus;                     // the relaxed g
//         if neighbor.walkedDistance >= followRange: continue;
//         if !neighbor.inOpenSet() || g < neighbor.g:
//           neighbor.cameFrom = node; neighbor.g = g;
//           neighbor.h = getBestH(neighbor, targets) * 1.5f;          // FUDGING = 1.5
//           if neighbor.inOpenSet(): openSet.changeCost(neighbor, neighbor.g + neighbor.h);
//           else:                    neighbor.f = neighbor.g + neighbor.h; openSet.insert(neighbor);
//     return reconstructPath(best reached or best-h node)
//
//   distance(a,b) = a.distanceTo(b)                         // Euclidean (Mth.sqrt of dx²+dy²+dz²)
//   getBestH(node, targets) = min over targets of node.distanceTo(target)  // the heuristic
//   reach test = distanceManhattan(node, target) <= reachRange
//   Node fields (javap): x,y,z, hash, heapIdx, g, h, f, cameFrom, closed, walkedDistance, costMalus, type
//   BinaryHeap: insert/pop/changeCost with a stored heapIdx per node for O(log n) decrease-key.
//   maxVisitedNodes (GroundPathNavigation): PathFinder ctor maxVisited = (int)(followRange*16);
//     searchDepthMultiplier = navigation.maxVisitedNodesMultiplier default 0.5f.
//
// THE PURITY CONTRACT (07-RESEARCH Pitfall 1): computePath(req pathRequest) reads ONLY req — no
// *TickLoop, no live entityStore, no live ChunkManager. The snapshot (req.region) is the
// immutable value built ON the tick (path_region.go); the A* runs over the COPY. In Phase 7
// navigation.tick calls computePath INLINE on the tick; in Phase 8 (OPT-01) the SAME pathRequest
// is handed to an ants pool and the resulting *Path rejoins via the existing applyAsyncResults
// seam — ZERO logic change. TestComputePathPure asserts the call shape (a hand-built snapshot,
// no world). Single-owner (TICK-05): in Phase 7 it runs on the tick goroutine, inline.

import "math"

// --- Node ------------------------------------------------------------------------------

// node ports net.minecraft.world.level.pathfinder.Node: a pathfinding cell with the A*
// bookkeeping. heapIdx is the BinaryHeap's stored index (-1 when not in the open set) enabling
// O(log n) decrease-key (changeCost). closed marks a popped node. costMalus is the PathType
// penalty stamped by the evaluator. cameFrom threads the reconstruct chain.
type node struct {
	x, y, z        int
	g, h, f        float64
	walkedDistance float64
	costMalus      float32
	ptype          pathType
	cameFrom       *node
	closed         bool
	heapIdx        int // index in the open-set heap; -1 = not in the open set
}

// newNode builds a fresh node not yet in any set (heapIdx -1).
func newNode(x, y, z int) *node {
	return &node{x: x, y: y, z: z, heapIdx: -1}
}

// nodeHash packs (x,y,z) into the open/closed dedupe key. Ported from Node.createHash's intent
// (a single int identity per coordinate) but widened to int64 so the larger snapshot box never
// collides (vanilla's 8/15/15-bit pack assumes a bounded region; a flat int64 is exact here).
func nodeHash(x, y, z int) int64 {
	return (int64(x)&0x1FFFFF)<<42 | (int64(y)&0xFFFFF)<<21 | (int64(z) & 0x1FFFFF)
}

// inOpenSet ports Node.inOpenSet() = heapIdx >= 0.
func (n *node) inOpenSet() bool { return n.heapIdx >= 0 }

// distanceTo ports Node.distanceTo(Node): the Euclidean distance (Mth.sqrt of dx²+dy²+dz²) —
// the A* edge cost (distance()) and the heuristic basis (getBestH).
func (n *node) distanceTo(o *node) float64 {
	dx := float64(o.x - n.x)
	dy := float64(o.y - n.y)
	dz := float64(o.z - n.z)
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// distanceToXYZ is distanceTo against a raw target coordinate (the heuristic to a Target).
func (n *node) distanceToXYZ(tx, ty, tz int) float64 {
	dx := float64(tx - n.x)
	dy := float64(ty - n.y)
	dz := float64(tz - n.z)
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// distanceManhattan ports Node.distanceManhattan(Node): |dx|+|dy|+|dz| — the reach test.
func distanceManhattan(x, y, z, tx, ty, tz int) int {
	return absI(tx-x) + absI(ty-y) + absI(tz-z)
}

func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// --- BinaryHeap (the open set) ---------------------------------------------------------

// binaryHeap ports net.minecraft.world.level.pathfinder.BinaryHeap: a min-heap on node.f with a
// stored heapIdx per node for O(log n) changeCost (decrease-key). Faithful to vanilla's
// hand-rolled heap (it keeps the heapIdx on the Node, not a separate index map) rather than
// stdlib container/heap, so the changeCost path matches the ported A* exactly.
type binaryHeap struct {
	nodes []*node
}

func (h *binaryHeap) isEmpty() bool { return len(h.nodes) == 0 }

func (h *binaryHeap) clear() {
	for _, n := range h.nodes {
		n.heapIdx = -1
	}
	h.nodes = h.nodes[:0]
}

// insert ports BinaryHeap.insert: append at the end and sift up by f.
func (h *binaryHeap) insert(n *node) {
	n.heapIdx = len(h.nodes)
	h.nodes = append(h.nodes, n)
	h.siftUp(n.heapIdx)
}

// pop ports BinaryHeap.pop: take the root (min f), move the last node to the root, sift down.
func (h *binaryHeap) pop() *node {
	root := h.nodes[0]
	last := len(h.nodes) - 1
	moved := h.nodes[last]
	h.nodes[last] = nil
	h.nodes = h.nodes[:last]
	root.heapIdx = -1
	if last > 0 {
		h.nodes[0] = moved
		moved.heapIdx = 0
		h.siftDown(0)
	}
	return root
}

// changeCost ports BinaryHeap.changeCost: set the node's f and re-establish the heap order from
// its stored position (decrease-key sifts up; an increase would sift down — A* only decreases).
func (h *binaryHeap) changeCost(n *node, f float64) {
	old := n.f
	n.f = f
	if f < old {
		h.siftUp(n.heapIdx)
	} else {
		h.siftDown(n.heapIdx)
	}
}

func (h *binaryHeap) siftUp(i int) {
	n := h.nodes[i]
	for i > 0 {
		parent := (i - 1) >> 1
		if h.nodes[parent].f <= n.f {
			break
		}
		h.nodes[i] = h.nodes[parent]
		h.nodes[i].heapIdx = i
		i = parent
	}
	h.nodes[i] = n
	n.heapIdx = i
}

func (h *binaryHeap) siftDown(i int) {
	n := h.nodes[i]
	count := len(h.nodes)
	for {
		left := 2*i + 1
		if left >= count {
			break
		}
		right := left + 1
		smaller := left
		if right < count && h.nodes[right].f < h.nodes[left].f {
			smaller = right
		}
		if h.nodes[smaller].f >= n.f {
			break
		}
		h.nodes[i] = h.nodes[smaller]
		h.nodes[i].heapIdx = i
		i = smaller
	}
	h.nodes[i] = n
	n.heapIdx = i
}

// --- Path ------------------------------------------------------------------------------

// Path ports net.minecraft.world.level.pathfinder.Path: the ordered node list the mob walks,
// plus the current waypoint index. done/nextNode/advance drive navigation.tick (navigation.go).
type Path struct {
	nodes   []*node
	idx     int  // the next node the mob is walking toward
	reached bool // whether the path actually reached a target (vs a best-effort partial)
}

// done reports whether the path is exhausted (the mob reached the final node).
func (p *Path) done() bool { return p == nil || p.idx >= len(p.nodes) }

// nextNode returns the current waypoint the mob walks toward (caller checks !done first).
func (p *Path) nextNode() *node { return p.nodes[p.idx] }

// advance steps the waypoint index forward (called when the mob reaches the current node).
func (p *Path) advance() { p.idx++ }

// truncateNodes ports net.minecraft.world.level.pathfinder.Path.truncateNodes(int length): drop
// every node from index `length` onward (keep nodes [0, length)). GroundPathNavigation.trimPath calls
// it to cut the path at the first sky-exposed node (the avoid-sun bias). A length past the end is a
// no-op; a length <= idx (already walked past) leaves at least the walked prefix so idx stays valid.
func (p *Path) truncateNodes(length int) {
	if p == nil || length < 0 || length >= len(p.nodes) {
		return
	}
	p.nodes = p.nodes[:length]
	if p.idx > len(p.nodes) {
		p.idx = len(p.nodes)
	}
}

// --- pathRequest + computePath (the PURE seam) -----------------------------------------

// pathRequest is the immutable A* input — the seam value. computePath reads ONLY this: no
// *TickLoop, no live world. region is the immutable snapshot (path_region.go). In Phase 8 the
// same struct is handed to an off-tick ants pool with zero change.
type pathRequest struct {
	startX, startY, startZ    int
	targetX, targetY, targetZ int
	region                    *pathRegion
	mobW, mobH                float64
	followRange               float64 // the A* follow-range gate (skip nodes beyond it)
	reachRange                int     // accuracy: manhattan distance at which a target counts reached
	maxVisited                int     // the visited-node BUDGET (Pitfall 6 / T-7-04)
	// malus is the mob per-mob pathfinding-malus map SNAPSHOT (an immutable copy — Mob
	// .getPathfindingMalus, node_evaluator.go). newEvalNode stamps node.costMalus from it, so a
	// water-avoider (higher WATER malus) or a fire-averse Animal (FIRE -1) re-costs the A* off-tick.
	malus mobMalus
	// canFloat is PathNavigation.canFloat (FloatGoal sets it): a floating mob treats WATER as a
	// standable surface node (findAcceptedNode), so it may path across water. Frozen into the request.
	canFloat bool
}

// computePath is the PURE A* over the immutable snapshot (THE Phase-8 hinge). It reads ONLY req
// — proving (TestComputePathPure) it closes over no tick-owned state, so OPT-01 swaps only the
// executor. Returns the reconstructed Path (or a best-effort partial toward the closest node, or
// nil if even the start is unusable). See the file header for the faithful bytecode mapping.
func computePath(req pathRequest) *Path {
	p, _ := computePathDebug(req)
	return p
}

// computePathDebug is computePath plus the visited-node count, so TestPathNodeBudget can assert
// the maxVisited BUDGET is honored. Identical logic; the count is the loop's visited counter.
func computePathDebug(req pathRequest) (*Path, int) {
	r := req.region
	if r == nil {
		return nil, 0
	}

	// The node cache (the open/closed identity): one *node per coordinate so the open and closed
	// sets dedupe by hash (vanilla's Int2ObjectMap<Node> in NodeEvaluator). getOrCreate stamps
	// the evaluator's PathType/costMalus once per coordinate.
	cache := make(map[int64]*node)
	getNode := func(x, y, z int) *node {
		key := nodeHash(x, y, z)
		if n, ok := cache[key]; ok {
			return n
		}
		n := newEvalNode(r, x, y, z, req.mobH, req.malus)
		cache[key] = n
		return n
	}

	start := getNode(req.startX, req.startY, req.startZ)
	start.g = 0
	start.h = start.distanceToXYZ(req.targetX, req.targetY, req.targetZ)
	start.f = start.h
	start.walkedDistance = 0

	open := &binaryHeap{}
	open.insert(start)

	// The budget: vanilla's (int)(maxVisitedNodes * searchDepthMultiplier). req.maxVisited is the
	// already-multiplied cap (navigation.go computes followRange*16*multiplier). A zero/negative
	// cap is clamped to a tiny positive so a misconfigured request still terminates.
	budget := req.maxVisited
	if budget <= 0 {
		budget = 1
	}

	// bestNode tracks the closest-to-target node visited, so an unreachable target still yields a
	// best-effort partial path (vanilla returns reconstructPath over the best-h target node).
	var bestNode *node
	var bestH = math.Inf(1)

	visited := 0
	var reached *node
	for !open.isEmpty() {
		// Ported bytecode: `++visited; if visited >= maxVisited break` (the check is BEFORE the
		// pop), so visited never exceeds the budget — the DoS guard (Pitfall 6 / T-7-04).
		visited++
		if visited >= budget {
			break
		}
		cur := open.pop()
		cur.closed = true

		// Track the closest node to the target for the best-effort partial.
		h := cur.distanceToXYZ(req.targetX, req.targetY, req.targetZ)
		if h < bestH {
			bestH = h
			bestNode = cur
		}

		// Reach test: ported distanceManhattan(node, target) <= reachRange.
		if distanceManhattan(cur.x, cur.y, cur.z, req.targetX, req.targetY, req.targetZ) <= req.reachRange {
			reached = cur
			break
		}

		// The follow-range gate (bytecode: if node.distanceTo(start) >= followRange continue).
		if start.distanceTo(cur) >= req.followRange {
			continue
		}

		for _, raw := range getNeighbors(r, cur, req.mobW, req.mobH, req.malus, req.canFloat) {
			// Resolve the cached identity for this coordinate (so g/closed/heapIdx persist).
			neighbor := getNode(raw.x, raw.y, raw.z)
			if neighbor.closed {
				continue
			}
			edge := cur.distanceTo(neighbor)
			neighbor.walkedDistance = cur.walkedDistance + edge
			g := cur.g + edge + float64(neighbor.costMalus)
			if neighbor.walkedDistance >= req.followRange {
				continue
			}
			if !neighbor.inOpenSet() || g < neighbor.g {
				neighbor.cameFrom = cur
				neighbor.g = g
				neighbor.h = neighbor.distanceToXYZ(req.targetX, req.targetY, req.targetZ) * 1.5 // FUDGING
				if neighbor.inOpenSet() {
					open.changeCost(neighbor, neighbor.g+neighbor.h)
				} else {
					neighbor.f = neighbor.g + neighbor.h
					open.insert(neighbor)
				}
			}
		}
	}

	if reached != nil {
		return reconstructPath(reached, true), visited
	}
	// No exact reach (unreachable or budget-bounded): a best-effort partial toward the closest
	// node — vanilla returns reconstructPath over the best target node so the mob still makes
	// progress. If even the start is the best (no progress), return nil (no useful path).
	if bestNode != nil && bestNode != start {
		return reconstructPath(bestNode, false), visited
	}
	return nil, visited
}

// reconstructPath ports PathFinder.reconstructPath: walk cameFrom from the end node back to the
// start, reversing into an ordered node list (start .. end). reached marks whether this is an
// exact target hit (vs a best-effort partial).
func reconstructPath(end *node, reached bool) *Path {
	var rev []*node
	for n := end; n != nil; n = n.cameFrom {
		rev = append(rev, n)
	}
	// Reverse into start..end order.
	nodes := make([]*node, len(rev))
	for i, n := range rev {
		nodes[len(rev)-1-i] = n
	}
	return &Path{nodes: nodes, idx: 0, reached: reached}
}
