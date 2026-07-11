package server

import "math"

const (
	dragonPhaseStrafePlayer    dragonPhase = 1
	dragonPhaseLandingApproach dragonPhase = 2
	dragonPhaseLanding         dragonPhase = 3
	dragonPhaseTakeoff         dragonPhase = 4
	dragonPhaseSittingFlaming  dragonPhase = 5
	dragonPhaseSittingScanning dragonPhase = 6
	dragonPhaseSittingAttack   dragonPhase = 7
	dragonPhaseChargingPlayer  dragonPhase = 8
	dragonPhaseHovering        dragonPhase = 10
)

type dragonNode struct {
	x, y, z  int
	f, g, h  float32
	cameFrom int
	closed   bool
	heapIdx  int
}

func (n *dragonNode) distanceTo(o *dragonNode) float32 {
	dx := float32(o.x - n.x)
	dy := float32(o.y - n.y)
	dz := float32(o.z - n.z)
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func (n *dragonNode) distanceToSqr(o *dragonNode) float32 {
	dx := float32(o.x - n.x)
	dy := float32(o.y - n.y)
	dz := float32(o.z - n.z)
	return dx*dx + dy*dy + dz*dz
}

type dragonPathState struct {
	nodes     [24]dragonNode
	built     bool
	adjacency [24]int
}

type dragonPathPoint struct{ x, y, z int }

type dragonPath struct {
	pts []dragonPathPoint
	idx int
}

func (p *dragonPath) isDone() bool                    { return p == nil || p.idx >= len(p.pts) }
func (p *dragonPath) advance()                        { p.idx++ }
func (p *dragonPath) getNextNodePos() dragonPathPoint { return p.pts[p.idx] }

type dragonFlightSample struct {
	y    float64
	yRot float32
}

type dragonFlightHistory struct {
	samples [64]dragonFlightSample
	head    int
}

func newDragonFlightHistory() *dragonFlightHistory {
	return &dragonFlightHistory{head: -1}
}

func (h *dragonFlightHistory) record(y float64, yRot float32) {
	s := dragonFlightSample{y: y, yRot: yRot}
	if h.head < 0 {
		for i := range h.samples {
			h.samples[i] = s
		}
	}
	h.head++
	if h.head == 64 {
		h.head = 0
	}
	h.samples[h.head] = s
}

func (h *dragonFlightHistory) get(delay int) dragonFlightSample {
	return h.samples[(h.head-delay)&0x3F]
}

func (t *TickLoop) dragonBuildNodes(e *Entity) {
	ps := e.dragon.path
	if ps.built {
		return
	}
	for i := 0; i < 24; i++ {
		var nodeX, nodeZ int
		yAdjustment := 5
		multiplier := i
		// Mth.cos/sin take a float arg widened to double at the call; the whole angle expression is
		// computed in float32 first (2.0f * (float)(-PI + step*mult)) then widened for the table lookup.
		if i < 12 {
			a := float32(2.0) * (float32(-math.Pi) + 0.2617994*float32(multiplier))
			nodeX = mthFloorF32(60.0 * mthCos(float64(a)))
			nodeZ = mthFloorF32(60.0 * mthSin(float64(a)))
		} else if i < 20 {
			multiplier -= 12
			a := float32(2.0) * (float32(-math.Pi) + 0.3926991*float32(multiplier))
			nodeX = mthFloorF32(40.0 * mthCos(float64(a)))
			nodeZ = mthFloorF32(40.0 * mthSin(float64(a)))
			yAdjustment += 10
		} else {
			multiplier -= 20
			a := float32(2.0) * (float32(-math.Pi) + 0.7853982*float32(multiplier))
			nodeX = mthFloorF32(20.0 * mthCos(float64(a)))
			nodeZ = mthFloorF32(20.0 * mthSin(float64(a)))
		}
		top := t.dragonHeightmapTop(nodeX, nodeZ) + yAdjustment
		nodeY := 73
		if top > nodeY {
			nodeY = top
		}
		ps.nodes[i] = dragonNode{x: nodeX, y: nodeY, z: nodeZ, cameFrom: -1, heapIdx: -1}
	}
	ps.adjacency = [24]int{
		6146, 8197, 8202, 16404, 32808, 32848, 65696, 131392,
		131712, 263424, 526848, 525313, 1581057, 3166214, 2138120, 6373424,
		4358208, 12910976, 9044480, 9706496, 15216640, 0xD0E000, 11763712, 0x7E0000,
	}
	ps.built = true
}

func (t *TickLoop) dragonHeightmapTop(x, z int) int {
	return t.ghastMotionBlockingTop(x, z) + 1
}

func (t *TickLoop) dragonFindClosestNode(e *Entity) int {
	t.dragonBuildNodes(e)
	return t.dragonFindClosestNodeAt(e, e.x, e.y, e.z)
}

func (t *TickLoop) dragonFindClosestNodeAt(e *Entity, tX, tY, tZ float64) int {
	ps := e.dragon.path
	closestDist := float32(10000.0)
	closestIndex := 0
	cur := dragonNode{x: mthFloor(tX), y: mthFloor(tY), z: mthFloor(tZ)}
	startIndex := 0
	if t.dragonAliveCrystals(e) == 0 {
		startIndex = 12
	}
	for i := startIndex; i < 24; i++ {
		d := ps.nodes[i].distanceToSqr(&cur)
		if d < closestDist {
			closestDist = d
			closestIndex = i
		}
	}
	return closestIndex
}

func (t *TickLoop) dragonFindPath(e *Entity, startIndex, endIndex int, finalNode *dragonPathPoint) *dragonPath {
	ps := e.dragon.path
	for i := 0; i < 24; i++ {
		n := &ps.nodes[i]
		n.closed = false
		n.f, n.g, n.h = 0, 0, 0
		n.cameFrom = -1
		n.heapIdx = -1
	}
	from := &ps.nodes[startIndex]
	to := &ps.nodes[endIndex]
	from.g = 0
	from.h = from.distanceTo(to)
	from.f = from.h

	heap := &dragonHeap{}
	heap.insert(from)
	closestIdx := startIndex
	minimumNodeIndex := 0
	if t.dragonAliveCrystals(e) == 0 {
		minimumNodeIndex = 12
	}
	toIdx := endIndex
	for !heap.isEmpty() {
		open := heap.pop()
		openIdx := ps.indexOf(open)
		if openIdx == toIdx {
			return ps.reconstructPath(startIndex, toIdx, finalNode)
		}
		if open.distanceTo(to) < ps.nodes[closestIdx].distanceTo(to) {
			closestIdx = openIdx
		}
		open.closed = true
		for i := minimumNodeIndex; i < 24; i++ {
			if ps.adjacency[openIdx]&(1<<uint(i)) <= 0 {
				continue
			}
			adj := &ps.nodes[i]
			if adj.closed {
				continue
			}
			tentativeG := open.g + open.distanceTo(adj)
			if adj.heapIdx >= 0 && !(tentativeG < adj.g) {
				continue
			}
			adj.cameFrom = openIdx
			adj.g = tentativeG
			adj.h = adj.distanceTo(to)
			if adj.heapIdx >= 0 {
				heap.changeCost(adj, adj.g+adj.h)
			} else {
				adj.f = adj.g + adj.h
				heap.insert(adj)
			}
		}
	}
	if closestIdx == startIndex {
		return nil
	}
	return ps.reconstructPath(startIndex, closestIdx, finalNode)
}

func (ps *dragonPathState) indexOf(n *dragonNode) int {
	for i := 0; i < 24; i++ {
		if &ps.nodes[i] == n {
			return i
		}
	}
	return 0
}

func (ps *dragonPathState) reconstructPath(fromIdx, toIdx int, finalNode *dragonPathPoint) *dragonPath {
	chain := []int{}
	cur := toIdx
	chain = append(chain, cur)
	for ps.nodes[cur].cameFrom != -1 {
		cur = ps.nodes[cur].cameFrom
		chain = append(chain, cur)
	}
	pts := make([]dragonPathPoint, 0, len(chain)+1)
	for i := len(chain) - 1; i >= 0; i-- {
		n := ps.nodes[chain[i]]
		pts = append(pts, dragonPathPoint{n.x, n.y, n.z})
	}
	if finalNode != nil {
		pts = append(pts, *finalNode)
	}
	return &dragonPath{pts: pts}
}

func (t *TickLoop) dragonAliveCrystals(e *Entity) int {
	owner := t.regionForEntity(e)
	if owner == nil {
		return 0
	}
	n := 0
	for _, other := range owner.entities.all() {
		if other != nil && other.isEndCrystal && !other.dead {
			n++
		}
	}
	return n
}

type dragonHeap struct {
	heap []*dragonNode
}

func (h *dragonHeap) isEmpty() bool { return len(h.heap) == 0 }

func (h *dragonHeap) insert(n *dragonNode) {
	n.heapIdx = len(h.heap)
	h.heap = append(h.heap, n)
	h.upHeap(n.heapIdx)
}

func (h *dragonHeap) pop() *dragonNode {
	top := h.heap[0]
	last := h.heap[len(h.heap)-1]
	h.heap = h.heap[:len(h.heap)-1]
	if len(h.heap) > 0 {
		h.heap[0] = last
		last.heapIdx = 0
		h.downHeap(0)
	}
	top.heapIdx = -1
	return top
}

func (h *dragonHeap) changeCost(n *dragonNode, newF float32) {
	old := n.f
	n.f = newF
	if newF < old {
		h.upHeap(n.heapIdx)
	} else {
		h.downHeap(n.heapIdx)
	}
}

func (h *dragonHeap) upHeap(idx int) {
	n := h.heap[idx]
	for idx > 0 {
		parent := (idx - 1) >> 1
		pn := h.heap[parent]
		if n.f >= pn.f {
			break
		}
		h.heap[idx] = pn
		pn.heapIdx = idx
		idx = parent
	}
	h.heap[idx] = n
	n.heapIdx = idx
}

func (h *dragonHeap) downHeap(idx int) {
	n := h.heap[idx]
	size := len(h.heap)
	for {
		child := idx*2 + 1
		if child >= size {
			break
		}
		right := child + 1
		if right < size && h.heap[right].f < h.heap[child].f {
			child = right
		}
		if h.heap[child].f >= n.f {
			break
		}
		h.heap[idx] = h.heap[child]
		h.heap[idx].heapIdx = idx
		idx = child
	}
	h.heap[idx] = n
	n.heapIdx = idx
}
