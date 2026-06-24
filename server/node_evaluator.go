package server

// node_evaluator.go — AI-02: the ported WalkNodeEvaluator — the cost/passability heart of the
// A*. PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL paste) from the
// unobfuscated 26.2 jar (javap, this session):
//
//   net.minecraft.world.level.pathfinder.WalkNodeEvaluator
//     - getNeighbors(Node[], node) (bytecode): a maxUpStep allowance `stepUp = floor(max(1,
//       mob.maxUpStep))` when the node ABOVE the current is not blocked; getFloorLevel(node);
//       then for each of the 4 HORIZONTAL Directions call findAcceptedNode(x+stepX, y, z+stepZ,
//       stepUp, floorLevel, dir, currentType) and keep it if isNeighborValid; then for each
//       Direction also try its CLOCKWISE diagonal — findAcceptedNode at the corner — kept iff
//       isDiagonalValid(node, sideA, sideB) AND isDiagonalValid(corner). The 8 candidate moves
//       (4 cardinal + 4 diagonal) emerge from "each cardinal + its clockwise diagonal".
//     - isNeighborValid(neighbor, node) (bytecode): neighbor != null && !neighbor.closed &&
//       (neighbor.costMalus >= 0 || node.costMalus < 0). A negative costMalus = impassable.
//     - isDiagonalValid(node, sideA, sideB) (bytecode): both sides non-null; the corner is not
//       a strict step-up/down over both sides; neither side is a closed/blocked door; for a
//       wide mob (bbWidth > 1) both sides passable; FENCE corner only squeezable by a thin mob;
//       and the corner is reachable iff at least one side is at/below the node's Y with a
//       non-negative malus. v1 keeps the core: both sides must be non-negative-malus (passable)
//       to allow the diagonal — the corner-cut rejection (Pitfall: a mob cutting a wall corner).
//     - isDiagonalValid(corner) (bytecode): corner != null && !corner.closed && type !=
//       WALKABLE_DOOR && costMalus >= 0.
//     - PathType malus table (javap PathType static{}): BLOCKED=-1, OPEN=0, WALKABLE=0,
//       FENCE=-1, LAVA=-1, WATER=8, … A negative malus is impassable. v1 ports BLOCKED/OPEN/
//       WALKABLE only (A5 — superflat is flat stone; water/lava/fence/door deferred, documented).
//
// v1 PASSABILITY (07-RESEARCH WalkNodeEvaluator row): a ground node at (x,y,z) is WALKABLE iff
// the block BELOW (y-1) is solid AND the mob's height of air is clear from y upward; it is OPEN
// (no floor) iff nothing solid is below but the body space is clear (a drop candidate); it is
// BLOCKED iff the body space itself is solid. Reads ONLY the immutable pathRegion snapshot — no
// live world, no *TickLoop (the purity that makes computePath relocatable off-tick).

// pathType is the ported PathType — v1 keeps the three faithful classes (BLOCKED/OPEN/WALKABLE)
// plus their malus from the jar's static table. The wider enum (WATER/LAVA/FENCE/DOOR/…) is
// deferred (A5); when added, each gets its jar malus and getPathType learns to classify it.
type pathType int

const (
	pathBlocked  pathType = iota // jar malus -1.0 (impassable)
	pathOpen                     // jar malus  0.0 (no floor under it — a drop/air node)
	pathWalkable                 // jar malus  0.0 (solid floor below, clear above — standable)
)

// malus returns the ported PathType.getMalus value (jar static{}): BLOCKED -1, OPEN/WALKABLE 0.
// A negative malus marks an impassable node (isNeighborValid rejects it).
func (p pathType) malus() float32 {
	switch p {
	case pathBlocked:
		return -1.0
	default: // pathOpen, pathWalkable
		return 0.0
	}
}

// mobAirCells is the number of air cells the mob needs above its feet to stand at a node —
// ceil(mobH). A Pig (0.9) needs 1; a 1.8-high mob needs 2. Ported from WalkNodeEvaluator's
// entityHeight (Mth.floor(bbHeight + 1)) clearance check, simplified to "ceil(height) clear".
func mobAirCells(mobH float64) int {
	n := int(mobH)
	if float64(n) < mobH {
		n++
	}
	if n < 1 {
		n = 1
	}
	return n
}

// getPathType ports WalkNodeEvaluator.getPathType (v1 BLOCKED/OPEN/WALKABLE classification) over
// the immutable snapshot. A node at (x,y,z) is the cell the mob's FEET occupy:
//   - BLOCKED if any of the mob's body cells (y .. y+ceil(h)-1) is solid (it cannot stand here).
//   - WALKABLE if the body space is clear AND the floor (y-1) is solid (a standable surface).
//   - OPEN otherwise (clear body, no floor below — an air/drop node the step-down logic uses).
func getPathType(r *pathRegion, x, y, z int, mobH float64) pathType {
	cells := mobAirCells(mobH)
	for i := 0; i < cells; i++ {
		if r.solidAt(x, y+i, z) {
			return pathBlocked // the mob's body would intersect a solid block
		}
	}
	if r.solidAt(x, y-1, z) {
		return pathWalkable // solid floor below, clear body above
	}
	return pathOpen // clear body, no floor (a drop / air node)
}

// newEvalNode builds a Node at (x,y,z), classifies it via getPathType, and stamps the ported
// costMalus (PathType.getMalus). The A* reads node.costMalus to relax g; a negative malus marks
// an impassable node that isNeighborValid rejects.
func newEvalNode(r *pathRegion, x, y, z int, mobH float64) *node {
	t := getPathType(r, x, y, z, mobH)
	n := newNode(x, y, z)
	n.ptype = t
	n.costMalus = t.malus()
	return n
}

// findAcceptedNode ports WalkNodeEvaluator.findAcceptedNode (v1 ground subset): from the source
// node, try the candidate at (x,y,z); if it is WALKABLE accept it; if it is OPEN (no floor) try
// stepping DOWN to the first solid floor below (a step-down); if it is BLOCKED try stepping UP
// to y+1 .. y+stepUp where a WALKABLE node exists (the maxUpStep allowance). Returns nil when no
// standable node is found in the step band (the move is rejected). Reads ONLY the snapshot.
func findAcceptedNode(r *pathRegion, x, y, z, stepUp int, mobH float64) *node {
	// Same-level: a standable floor here.
	if n := newEvalNode(r, x, y, z, mobH); n.ptype == pathWalkable {
		return n
	}
	// Step UP: a 1-block (or stepUp-block) ledge — the mob climbs onto it.
	for up := 1; up <= stepUp; up++ {
		if n := newEvalNode(r, x, y+up, z, mobH); n.ptype == pathWalkable {
			return n
		}
	}
	// Step DOWN: the candidate is OPEN (air, no floor) — fall to the first solid floor below,
	// within a short drop band (vanilla bounds the fall; v1 uses a small fixed band so a mob
	// does not "see" a node across a deep chasm). The band of 3 mirrors a mob's safe-ish drop.
	if newEvalNode(r, x, y, z, mobH).ptype == pathOpen {
		for down := 1; down <= 3; down++ {
			if n := newEvalNode(r, x, y-down, z, mobH); n.ptype == pathWalkable {
				return n
			}
		}
	}
	return nil
}

// getNeighbors ports WalkNodeEvaluator.getNeighbors: the 4 cardinal moves (each via
// findAcceptedNode, so each carries the step-up/step-down allowance) plus the 4 clockwise
// diagonals, each diagonal gated by the corner-cut rejection (isDiagonalValid over the two
// adjacent cardinal nodes + the corner). Returns the accepted neighbor nodes. Reads ONLY the
// immutable snapshot (no live world) — the purity that lets the A* run off-tick in Phase 8.
//
// stepUp is the maxUpStep allowance (vanilla floor(max(1, mob.maxUpStep))); for a Pig that is 1.
func getNeighbors(r *pathRegion, n *node, mobW, mobH float64) []*node {
	const stepUp = 1 // floor(max(1, mob.maxUpStep)); a Pig steps up 1 block

	// The 4 cardinal directions in vanilla Direction2D order (N=-Z, E=+X, S=+Z, W=-X), each
	// paired with its CLOCKWISE neighbor for the diagonal probe (N->E, E->S, S->W, W->N).
	type dir struct{ dx, dz int }
	card := [4]dir{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} // N, E, S, W

	// First pass: the cardinal accepted nodes (index-aligned with card so the diagonal pass can
	// reference the two sides of each corner). A nil entry is a rejected cardinal move.
	side := [4]*node{}
	out := make([]*node, 0, 8)
	for i, d := range card {
		c := findAcceptedNode(r, n.x+d.dx, n.y, n.z+d.dz, stepUp, mobH)
		side[i] = c
		if isNeighborValid(c, n) {
			out = append(out, c)
		}
	}

	// Second pass: the 4 clockwise diagonals. For cardinal i and its clockwise j=(i+1)%4, the
	// corner is at (dx_i+dx_j, dz_i+dz_j). The diagonal is kept iff isDiagonalValid(n, side_i,
	// side_j) (the corner-cut rejection: a mob may not slide diagonally past a blocked corner)
	// AND isDiagonalValid(corner) (the corner node itself is open/standable).
	for i := range card {
		j := (i + 1) % 4
		di, dj := card[i], card[j]
		cx, cz := n.x+di.dx+dj.dx, n.z+di.dz+dj.dz
		corner := findAcceptedNode(r, cx, n.y, cz, stepUp, mobH)
		if isDiagonalValidSides(n, side[i], side[j], mobW) && isDiagonalValidCorner(corner) {
			out = append(out, corner)
		}
	}
	return out
}

// isNeighborValid ports WalkNodeEvaluator.isNeighborValid (bytecode):
//
//	neighbor != null && !neighbor.closed && (neighbor.costMalus >= 0 || node.costMalus < 0)
//
// i.e. a non-closed neighbor is valid when its own malus is non-negative (passable), OR the
// SOURCE node is already impassable (so a mob stuck in a bad cell may still try to leave). A
// negative-malus neighbor (BLOCKED/FENCE/LAVA) from a good cell is rejected.
func isNeighborValid(neighbor, src *node) bool {
	if neighbor == nil || neighbor.closed {
		return false
	}
	return neighbor.costMalus >= 0 || src.costMalus < 0
}

// isDiagonalValidSides ports WalkNodeEvaluator.isDiagonalValid(node, sideA, sideB) — the
// corner-cut rejection (the v1 core). Both adjacent cardinal nodes must exist and be passable
// (non-negative malus) for the diagonal to be allowed, so a mob cannot slide diagonally THROUGH
// a blocked corner (Pitfall 5's pathing analogue). The wide-mob (bbWidth > 1) tightening and the
// FENCE-squeeze are deferred with the wider PathType set (A5); for a Pig (width 0.9 <= 1) the
// faithful gate is "both sides passable".
func isDiagonalValidSides(n, sideA, sideB *node, mobW float64) bool {
	if sideA == nil || sideB == nil {
		return false
	}
	// Both adjacent cardinals must be passable (the corner-cut guard).
	if sideA.costMalus < 0 || sideB.costMalus < 0 {
		return false
	}
	// A strict step-up/down over BOTH sides forbids the diagonal (vanilla rejects a corner that
	// is higher than the node on one side and lower on the other — an impossible squeeze).
	if sideA.y > n.y && sideB.y > n.y {
		return false
	}
	return true
}

// isDiagonalValidCorner ports WalkNodeEvaluator.isDiagonalValid(corner) (bytecode): the corner
// node must exist, be non-closed, not a closed door, and have a non-negative malus.
func isDiagonalValidCorner(corner *node) bool {
	if corner == nil || corner.closed {
		return false
	}
	return corner.costMalus >= 0
}
