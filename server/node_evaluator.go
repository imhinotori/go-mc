package server

// node_evaluator.go — AI-02: the ported WalkNodeEvaluator — the cost/passability heart of the
// A*. PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL paste) from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session):
//
//   net.minecraft.world.level.pathfinder.WalkNodeEvaluator
//     - getNeighbors(Node[], node) (bytecode): a maxUpStep allowance stepUp = floor(max(1,
//       mob.maxUpStep)) when the node ABOVE the current is not blocked; getFloorLevel(node);
//       then for each of the 4 HORIZONTAL Directions call findAcceptedNode(x+stepX, y, z+stepZ,
//       stepUp, floorLevel, dir, currentType) and keep it if isNeighborValid; then for each
//       Direction also try its CLOCKWISE diagonal — findAcceptedNode at the corner — kept iff
//       isDiagonalValid(node, sideA, sideB) AND isDiagonalValid(corner).
//     - findAcceptedNode (bytecode, VERIFIED this session): pathType = getCachedPathType(x,y,z);
//       pathCost = mob.getPathfindingMalus(pathType); if (pathCost >= 0) best =
//       getNodeAndUpdateCostToMax(x,y,z, pathType, pathCost); if (pathType==WALKABLE || (amphibious
//       && pathType==WATER)) return best; else if ((best==null||best.costMalus<0) && jumpSize>0 &&
//       …) best = tryJumpOn(...); else if (!amphibious && pathType==WATER && !canFloat) best =
//       tryFindFirstNonWaterBelow(...); else if (pathType==OPEN) best = tryFindFirstGroundNodeBelow.
//       So the node's costMalus is mob.getPathfindingMalus(nodeType), NOT the raw PathType default —
//       the per-mob malus map (Mob.getPathfindingMalus) is the observable cost.
//     - isNeighborValid (bytecode): neighbor != null && !neighbor.closed && (neighbor.costMalus >= 0
//       || node.costMalus < 0). A negative costMalus = impassable.
//     - PathType malus table (jar PathType static ctor, VERIFIED CFR this session — the FULL table):
//       BLOCKED -1, OPEN 0, WALKABLE 0, WALKABLE_DOOR 0, TRAPDOOR 0, POWDER_SNOW -1,
//       ON_TOP_OF_POWDER_SNOW 0, FENCE -1, LAVA -1, WATER 8, WATER_BORDER 8, RAIL 0,
//       UNPASSABLE_RAIL -1, FIRE_IN_NEIGHBOR 8, FIRE 16, DAMAGING_IN_NEIGHBOR 8, DAMAGING -1,
//       DOOR_OPEN 0, DOOR_WOOD_CLOSED -1, DOOR_IRON_CLOSED -1, BREACH 4, LEAVES -1, STICKY_HONEY 8,
//       COCOA 0, DAMAGE_CAUTIOUS 0, ON_TOP_OF_TRAPDOOR 0, BIG_MOBS_CLOSE_TO_DANGER 4.
//     - getPathTypeFromState (bytecode, VERIFIED CFR): air->OPEN; LAVA fluid->LAVA; burning->FIRE;
//       WATER fluid (after !isPathfindable->BLOCKED)->WATER; else OPEN. (Trapdoor/powder/cactus/
//       honey/cocoa/door/rail/leaves/fence classes are the wider block set — v1 superflat only ever
//       sees air/stone/water/lava, so those classes are cited-deferred; the fluid classes are live.)
//
// v1 PASSABILITY: a ground node at (x,y,z) is WALKABLE iff the block BELOW (y-1) is solid AND the
// mob height of air is clear from y upward; it is OPEN (no floor) iff nothing solid is below but
// the body space is clear (a drop candidate); it is BLOCKED iff the body space itself is solid; it
// is WATER/LAVA iff the mob FEET cell is that fluid. Reads ONLY the immutable pathRegion snapshot
// (block-id classified ON the tick) — no live world, no *TickLoop (the purity that makes computePath
// relocatable off-tick).

// pathType is the ported net.minecraft.world.level.pathfinder.PathType. The ORDER matches the jar
// enum (so a value maps to the same ordinal), and malus() below is the jar static-ctor table.
type pathType int

const (
	pathBlocked              pathType = iota // jar malus -1.0 (impassable)
	pathOpen                                 // jar malus  0.0 (no floor under it — a drop/air node)
	pathWalkable                             // jar malus  0.0 (solid floor below, clear above — standable)
	pathWalkableDoor                         // jar malus  0.0
	pathTrapdoor                             // jar malus  0.0
	pathPowderSnow                           // jar malus -1.0
	pathOnTopOfPowderSnow                    // jar malus  0.0
	pathFence                                // jar malus -1.0
	pathLava                                 // jar malus -1.0
	pathWater                                // jar malus  8.0
	pathWaterBorder                          // jar malus  8.0
	pathRail                                 // jar malus  0.0
	pathUnpassableRail                       // jar malus -1.0
	pathFireInNeighbor                       // jar malus  8.0
	pathFire                                 // jar malus 16.0
	pathDamagingInNeighbor                   // jar malus  8.0
	pathDamaging                             // jar malus -1.0
	pathDoorOpen                             // jar malus  0.0
	pathDoorWoodClosed                       // jar malus -1.0
	pathDoorIronClosed                       // jar malus -1.0
	pathBreach                               // jar malus  4.0
	pathLeaves                               // jar malus -1.0
	pathStickyHoney                          // jar malus  8.0
	pathCocoa                                // jar malus  0.0
	pathDamageCautious                       // jar malus  0.0
	pathOnTopOfTrapdoor                      // jar malus  0.0
	pathBigMobsCloseToDanger                 // jar malus  4.0
)

// pathTypeDefaultMalus is the jar PathType static-ctor malus table (VERIFIED CFR PathType.java,
// this session — the FULL 27-value table). PathType.getMalus() returns exactly these; a mob with no
// per-mob override inherits them (Mob.getPathfindingMalus: malus == null ? pathType.getMalus() : malus).
var pathTypeDefaultMalus = [...]float32{
	pathBlocked:              -1.0,
	pathOpen:                 0.0,
	pathWalkable:             0.0,
	pathWalkableDoor:         0.0,
	pathTrapdoor:             0.0,
	pathPowderSnow:           -1.0,
	pathOnTopOfPowderSnow:    0.0,
	pathFence:                -1.0,
	pathLava:                 -1.0,
	pathWater:                8.0,
	pathWaterBorder:          8.0,
	pathRail:                 0.0,
	pathUnpassableRail:       -1.0,
	pathFireInNeighbor:       8.0,
	pathFire:                 16.0,
	pathDamagingInNeighbor:   8.0,
	pathDamaging:             -1.0,
	pathDoorOpen:             0.0,
	pathDoorWoodClosed:       -1.0,
	pathDoorIronClosed:       -1.0,
	pathBreach:               4.0,
	pathLeaves:               -1.0,
	pathStickyHoney:          8.0,
	pathCocoa:                0.0,
	pathDamageCautious:       0.0,
	pathOnTopOfTrapdoor:      0.0,
	pathBigMobsCloseToDanger: 4.0,
}

// malus returns the ported PathType.getMalus value (the jar static table). A negative malus marks an
// impassable node (isNeighborValid rejects it). This is the DEFAULT; a mob per-mob override
// (mobMalus) takes precedence via getPathfindingMalus.
func (p pathType) malus() float32 {
	if int(p) < 0 || int(p) >= len(pathTypeDefaultMalus) {
		return 0.0
	}
	return pathTypeDefaultMalus[p]
}

// mobMalus is the ported net.minecraft.world.entity.Mob per-mob pathfinding-malus map: a sparse
// override of the PathType defaults. getPathfindingMalus returns the override if present, else the
// PathType default (Mob.getPathfindingMalus: malus == null ? pathType.getMalus() : malus); an
// unset map is the vanilla default for every type. It is an IMMUTABLE VALUE threaded into the A*
// snapshot (pathRequest.malus) — computePath reads it off-tick, never the live mob.
//
//	[VERIFIED CFR Mob.getPathfindingMalus/setPathfindingMalus: Map<PathType,Float> pathfindingMalus
//	 = Maps.newEnumMap(PathType.class); get(pathType) ?? pathType.getMalus(); put(pathType, cost).]
type mobMalus struct {
	// overrides holds only the types a mob set (Animal sets FIRE/FIRE_IN_NEIGHBOR). nil == pure
	// defaults. A copy is threaded into pathRequest so the off-tick A* reads a frozen snapshot.
	overrides map[pathType]float32
}

// getPathfindingMalus ports Mob.getPathfindingMalus(PathType): the override if set, else the jar
// PathType default. (The controlled-vehicle inheritFrom branch is a rider concern deferred with the
// vehicle subsystem — a v1 walking mob is its own inheritFrom, so this.pathfindingMalus is read.)
func (m mobMalus) getPathfindingMalus(p pathType) float32 {
	if m.overrides != nil {
		if v, ok := m.overrides[p]; ok {
			return v
		}
	}
	return p.malus()
}

// setPathfindingMalus ports Mob.setPathfindingMalus(PathType, float): record a per-mob override.
func (m *mobMalus) setPathfindingMalus(p pathType, cost float32) {
	if m.overrides == nil {
		m.overrides = make(map[pathType]float32)
	}
	m.overrides[p] = cost
}

// copy returns an immutable snapshot of the malus map to thread into pathRequest (so the off-tick A*
// never aliases the live mob map — the Phase-8 purity contract). A nil/empty map copies to a
// zero-value mobMalus (pure defaults).
func (m mobMalus) copy() mobMalus {
	if len(m.overrides) == 0 {
		return mobMalus{}
	}
	c := make(map[pathType]float32, len(m.overrides))
	for k, v := range m.overrides {
		c[k] = v
	}
	return mobMalus{overrides: c}
}

// mobAirCells is the number of air cells the mob needs above its feet to stand at a node —
// ceil(mobH). A Pig (0.9) needs 1; a 1.8-high mob needs 2. Ported from WalkNodeEvaluator
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

// getPathType ports WalkNodeEvaluator.getPathType over the immutable snapshot — the mob-aware node
// classification. A node at (x,y,z) is the cell the mob FEET occupy:
//   - WATER if the feet cell is a water fluid (the getPathTypeFromState WATER class); the WATER malus
//     (default 8, positive) makes water COSTLY but passable, so a water-avoider (higher malus) routes
//     around it while a swimmer (malus 0, AmphibiousNodeEvaluator) treats it as cheap.
//   - LAVA if the feet cell is a lava fluid (malus -1, impassable — a mob never paths into lava).
//   - BLOCKED if any of the mob body cells (y .. y+ceil(h)-1) is solid (it cannot stand here).
//   - WALKABLE if the body space is clear AND the floor (y-1) is solid (a standable surface).
//   - OPEN otherwise (clear body, no floor below — an air/drop node the step-down logic uses).
//
// This is the getPathTypeFromState precedence (VERIFIED CFR: air->OPEN; LAVA->LAVA; WATER->WATER;
// else solidity) folded with the body-BB clearance check WalkNodeEvaluator.getPathTypeOfMob applies.
// v1 superflat only ever produces OPEN/WALKABLE/BLOCKED; WATER/LAVA appear only where those fluids
// are placed (turtle oceans, nether — the wider worlds), and their jar malus now re-costs the path.
func getPathType(r *pathRegion, x, y, z int, mobH float64) pathType {
	// FEET-cell classification first (getPathTypeStatic precedence: the feet block's own type -- LAVA,
	// WATER, CACTUS->DAMAGING, FIRE, FENCE, etc -- before the standing/solidity fold). A non-OPEN feet
	// type (a fluid or a hazard block the mob is standing IN) is returned as-is, exactly as the jar
	// returns the feet getPathTypeFromState when it is not OPEN.
	if t := r.blockTypeAt(x, y, z); t != pathOpen {
		return t
	}
	// Feet cell is OPEN (air). Fold the mob body-BB clearance (getPathTypeWithinMobBB): if any body cell
	// the mob would occupy (y .. y+ceil(h)-1) is a solid collider, the mob cannot stand here -> BLOCKED.
	// This preserves the pre-C-2 solidity model (a ceiling above the feet still blocks the node).
	cells := mobAirCells(mobH)
	for i := 0; i < cells; i++ {
		if r.solidAt(x, y+i, z) {
			return pathBlocked // the mob body would intersect a solid block
		}
	}
	// Body is clear: getPathTypeStatic decides WALKABLE (solid floor below, via checkNeighbourBlocks so
	// a floor-cell next to lava/fire/cactus becomes a DANGER_* type) vs the ON_TOP_OF_* stacked-hazard
	// standing types vs OPEN (no floor -> a drop node). This is the C-2 hazard classification: the
	// per-type malus (path_type.go) then feeds the A* cost so a mob AVOIDS danger and never enters lava.
	return getPathTypeStatic(r, x, y, z)
}

// newEvalNode builds a Node at (x,y,z), classifies it via getPathType, and stamps the ported
// costMalus = mob.getPathfindingMalus(nodeType) (findAcceptedNode pathCost = mob
// .getPathfindingMalus(pathType)), NOT the raw PathType default — the per-mob malus map is the
// observable cost. The A* reads node.costMalus to relax g; a negative malus marks an impassable node
// that isNeighborValid rejects.
func newEvalNode(r *pathRegion, x, y, z int, mobH float64, malus mobMalus) *node {
	t := getPathType(r, x, y, z, mobH)
	n := newNode(x, y, z)
	n.ptype = t
	n.costMalus = malus.getPathfindingMalus(t)
	return n
}

// findAcceptedNode ports WalkNodeEvaluator.findAcceptedNode (v1 ground subset). The ORDER + guards
// are load-bearing (a wrong order makes a mob "climb" out of a covered hole). Faithful flow:
//   - WATER feet (not floating) -> tryFindFirstNonWaterBelow (fall to the floor under the water);
//     for a floating mob (canFloat) WATER is standable-in and returned directly (the water surface).
//   - same-level WALKABLE  -> accept it (a standable floor here).
//   - same-level OPEN (clear body, no floor) -> step DOWN to the first solid floor below (the mob
//     FALLS; it does NOT jump). This is vanilla tryFindFirstGroundNodeBelow branch.
//   - same-level BLOCKED/LAVA (the mob body cell is solid or lava) -> ONLY THEN try stepping UP
//     (tryJumpOn), and only if there is JUMP CLEARANCE in the SOURCE column (no ceiling pinning the
//     mob) AND the destination y+1 is itself standable. Vanilla if (best==null || best.costMalus<0)
//     && jumpSize>0 gate + tryJumpOn collision sweep. A negative-malus node (LAVA) yields best==null
//     (pathCost<0 skips getNodeAndUpdateCostToMax), so it falls through to the jump-or-nil path — a mob
//     never accepts a lava node.
//
// src(X,Z) is the SOURCE column the mob is moving FROM — needed for the jump ceiling check (the mob
// must be able to rise in its CURRENT column, not just land in the destination). stepUp is the
// maxUpStep allowance in blocks. canFloat is PathNavigation.canFloat (FloatGoal sets it) — it makes
// WATER a standable surface node (the float-pathing the FloatGoal implies). Reads ONLY the snapshot.
func findAcceptedNode(r *pathRegion, x, y, z, stepUp int, mobH float64, srcX, srcZ int, malus mobMalus, canFloat bool) *node {
	// pathType = getCachedPathType(x,y,z) (getPathType over the snapshot); pathCost = mob
	// .getPathfindingMalus(pathType). VERIFIED WalkNodeEvaluator.findAcceptedNode bytecode this session.
	nd := newEvalNode(r, x, y, z, mobH, malus)
	var best *node
	// if (pathCost >= 0) best = getNodeAndUpdateCostToMax(...). A node with ANY non-negative malus is a
	// standable node here -- WALKABLE, WATER, WATER_BORDER, FIRE_IN_NEIGHBOR, DAMAGING_IN_NEIGHBOR,
	// DAMAGE_CAUTIOUS, etc are all accepted as-is (they only differ in COST, not passability). A
	// negative-malus type (BLOCKED/LAVA/FENCE/DAMAGING/DOOR_*/LEAVES/POWDER_SNOW/UNPASSABLE_RAIL) leaves
	// best == nil, so it falls through to the jump-or-step-down fallbacks -- a mob never STANDS on it.
	if nd.costMalus >= 0 {
		best = nd
	}
	// if (pathType == WALKABLE || (amphibious && pathType == WATER)) return best. canFloat is the
	// v1 amphibious/water-standable flag (TurtlePathNavigation/FloatGoal). The WALKABLE fast-path and the
	// amphibious-WATER fast-path both return the accepted standing node directly.
	if nd.ptype == pathWalkable || (canFloat && nd.ptype == pathWater) {
		return best
	}
	// else if ((best == null || best.costMalus < 0) && jumpSize > 0 && pathType not in
	// {FENCE, UNPASSABLE_RAIL, TRAPDOOR, POWDER_SNOW}) best = tryJumpOn(...). best == nil means the
	// destination cell is impassable (negative malus) -- the only way through is UP (a step-up/jump).
	if best == nil && stepUp > 0 &&
		nd.ptype != pathFence && nd.ptype != pathUnpassableRail &&
		nd.ptype != pathTrapdoor && nd.ptype != pathPowderSnow {
		// tryJumpOn: the mob can only rise if its SOURCE column has the headroom to lift -- the cell
		// directly above the mob body in the column it is LEAVING must be clear (else it would clip a
		// ceiling). Body occupies y .. y+cells-1; the lift cell is (srcX, y+cells, srcZ).
		if !r.solidAt(srcX, y+mobAirCells(mobH), srcZ) {
			for up := 1; up <= stepUp; up++ {
				if n := newEvalNode(r, x, y+up, z, mobH, malus); n.ptype == pathWalkable {
					return n // a reachable ledge with clear headroom at the destination
				}
			}
		}
		return nil
	}
	// else if (!amphibious && pathType == WATER && !canFloat) best = tryFindFirstNonWaterBelow(...). A
	// non-floating mob falls to the first non-water floor below the water column.
	if nd.ptype == pathWater && !canFloat {
		for down := 1; down <= 3; down++ {
			if n := newEvalNode(r, x, y-down, z, mobH, malus); n.ptype == pathWalkable {
				return n
			}
		}
		return nil
	}
	// else if (pathType == OPEN) best = tryFindFirstGroundNodeBelow(...). Clear body, no floor -> the mob
	// FALLS (step DOWN, never jumps) to the first solid floor within a bounded drop band.
	if nd.ptype == pathOpen {
		for down := 1; down <= 3; down++ {
			if n := newEvalNode(r, x, y-down, z, mobH, malus); n.ptype == pathWalkable {
				return n
			}
		}
		return nil
	}
	// return best. A positive-malus standing node (WATER_BORDER, FIRE_IN_NEIGHBOR, DANGER, etc) that was
	// not a fast-path type is returned as the standable-but-costly node -- the A* pays its malus, so the
	// mob PREFERS a cheaper route (one cell away from the hazard) but can still cross if it must.
	return best
}

// getNeighbors ports WalkNodeEvaluator.getNeighbors: the 4 cardinal moves (each via
// findAcceptedNode, so each carries the step-up/step-down allowance) plus the 4 clockwise
// diagonals, each diagonal gated by the corner-cut rejection (isDiagonalValid over the two
// adjacent cardinal nodes + the corner). Returns the accepted neighbor nodes. Reads ONLY the
// immutable snapshot (no live world) — the purity that lets the A* run off-tick in Phase 8.
//
// stepUp is the maxUpStep allowance (vanilla floor(max(1, mob.maxUpStep))); for a Pig that is 1.
func getNeighbors(r *pathRegion, n *node, mobW, mobH float64, malus mobMalus, canFloat bool) []*node {
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
		c := findAcceptedNode(r, n.x+d.dx, n.y, n.z+d.dz, stepUp, mobH, n.x, n.z, malus, canFloat)
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
		corner := findAcceptedNode(r, cx, n.y, cz, stepUp, mobH, n.x, n.z, malus, canFloat)
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
// a blocked corner (Pitfall 5 pathing analogue). The wide-mob (bbWidth > 1) tightening and the
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
