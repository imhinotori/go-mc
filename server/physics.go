package server

import (
	"math"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/server/internal/bvh"
)

// physics.go — ENT-02: gravity + per-axis swept-AABB collision (the entity half) and the
// authoritative per-axis validation of a client-sent player position (the player half).
//
// THE LOAD-BEARING DISCIPLINE (06-RESEARCH Pattern 2 + Pitfall 4): collision is resolved
// PER AXIS, never as a full-vector-then-test. moveEntity clips Δy, applies it, re-derives
// the box, clips Δx, applies, then clips Δz, applies. Moving the whole motion vector then
// testing lets a fast entity tunnel a thin wall — the per-axis sweep clamps each component
// against the blocks actually in that axis's swept range, so a 10-block/tick velocity into
// a 1-block wall stops AT the wall (TestNoTunnel).
//
// All of this runs ONLY on the tick goroutine over tick-owned entity/player/chunk state
// (TICK-05): moveEntity routes its position change through entityStore.move so the per-
// section bucket the tracker's near() reads stays consistent (no stale bucket).

// --- Tunable physics constants (06-RESEARCH A1) ----------------------------------------
//
// These are [ASSUMED]/training-derived and DO NOT affect the wire — they shape only the
// VISIBLE behaviors (an entity falls, lands, is blocked). v1 targets those behaviors, not
// exact vanilla-constant parity; the values are documented here as the single tunable knob
// and may be revised against the jar Entity/LivingEntity if parity ever matters.
const (
	// gravityPerTick is the downward acceleration applied each tick (blocks/tick²). Vanilla
	// living entities ≈ 0.08 (items 0.04). [ASSUMED — 06-RESEARCH A1, wire-irrelevant.]
	gravityPerTick = 0.08

	// airDrag scales vertical velocity each tick AFTER gravity, so vertical speed converges
	// to a terminal velocity instead of growing unbounded. Vanilla ≈ 0.98. [ASSUMED.]
	airDrag = 0.98

	// horizontalFriction scales horizontal velocity each tick. Vanilla derives it from the
	// block beneath (≈ 0.6) times a base 0.91; v1 uses a single constant for the visible
	// "entities don't slide forever" behavior. [ASSUMED — wire-irrelevant.]
	horizontalFriction = 0.6 * 0.91

	// stepHeight is the auto-step-up players/most mobs get over a 1-block edge. Recorded for
	// completeness/tuning; v1's visible gate (land, blocked, no clip-through) does not depend
	// on stepping, so it is not yet applied in the sweep. [ASSUMED.]
	stepHeight = 0.6
)

// dimMinY is the dimension floor used to map a world Y to its chunk-section index. The
// overworld floor is -64 (mirrors cmd/sulfur/main.go overworldMinY). It is a named const
// here (not hard-coded deep in the read) so a future multi-dimension wiring threads the
// real per-dimension floor; for v1 the single overworld value is correct.
const dimMinY = -64

// playerWidth / playerHeight are the player's collision AABB footprint (≈ 0.6 × 1.8 in
// vanilla). Used by collidePlayer to build the player box for the authoritative anti-clip
// validation. Plain tunable dims (the player is not yet a data/entity-table Entity).
const (
	playerWidth  = 0.6
	playerHeight = 1.8
)

// floorI is math.Floor returning an int — the negative-correct world-block index of a
// fractional coordinate (e.g. -0.3 → -1, not 0). Used to map an AABB edge to block coords.
func floorI(v float64) int { return int(math.Floor(v)) }

// blockSolidAt reports whether the world block at (x,y,z) is a solid collider. A non-air
// block is solid for v1 (slabs/stairs/fluids with partial boxes are a later refinement —
// v1 treats every solid as a unit cube). Returns false (non-solid / air) when there is no
// world wired or the column/section is not loaded, so physics in a world-less test and an
// entity over an ungenerated column simply do not collide (treated as empty air). This is
// the READ counterpart to the WRITE API Plan 06-04 adds; both mirror the generator's
// section/local mapping (world/generator.go sectionLocal).
func (t *TickLoop) blockSolidAt(x, y, z int) bool {
	if t.world == nil {
		return false // no world: nothing to collide with (world-less unit tests)
	}
	col := level.ChunkPos{int32(floorDiv(x, 16)), int32(floorDiv(z, 16))}
	ch, ok := t.world.Get(col)
	if !ok {
		return false // column not loaded/ready: treat as air (do not collide / never block)
	}
	sec := (y - dimMinY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return false // outside the dimension's section range: air
	}
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	return !block.IsAir(ch.Sections[sec].GetBlock(local))
}

// floorDiv is a negative-correct integer floor-division (Go's / truncates toward zero, so
// -1/16 == 0 not -1). Maps a world block coord to its chunk-column index correctly for
// negative coordinates — the same negative-correctness columnOf relies on.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// entityBoxAt builds the entity's AABB as if its feet were at (x,y,z), reusing the entity's
// data/entity Width/Height (centered on x/z, base at y, top at y+height) — the same feet-
// anchored box as Entity.AABB, but parameterized so the per-axis sweep can probe a proposed
// position WITHOUT mutating the entity. Returns the generic bvh.AABB primitive so the
// overlap test reuses the bvh box type (no second box type).
func entityBoxAt(e *Entity, x, y, z float64) bvh.AABB[float64, bvh.Vec3[float64]] {
	hw := e.width / 2
	return bvh.AABB[float64, bvh.Vec3[float64]]{
		Lower: bvh.Vec3[float64]{x - hw, y, z - hw},
		Upper: bvh.Vec3[float64]{x + hw, y + e.height, z + hw},
	}
}

// boxOverlapsSolid reports whether the given entity-space box overlaps ANY solid world
// block. It walks every block the box spans (floor of each lower edge .. ceil-1 of each
// upper edge) and, for each, builds that block's unit cube AABB and tests it against the
// box. The narrow-phase test uses bvh.AABB.Touch for the X/Y overlap and an explicit Z
// interval test: the bvh.Vec3 Less/More compare only components 0 and 1 (X,Y), so Touch
// alone is X/Y-only — the Z interval is added here so the overlap is correct on ALL THREE
// axes (a deliberate, documented complement to the reused bvh primitive, not a reimplemented
// box-overlap). A half-open edge (upper exactly on a block boundary) does not count as
// overlap, so an entity resting exactly on a floor top (y == floorTop) does not "touch" the
// block below it — which is what lets it settle.
func (t *TickLoop) boxOverlapsSolid(box bvh.AABB[float64, bvh.Vec3[float64]]) bool {
	const eps = 1e-9
	loX, loY, loZ := box.Lower[0], box.Lower[1], box.Lower[2]
	hiX, hiY, hiZ := box.Upper[0], box.Upper[1], box.Upper[2]

	for bx := floorI(loX); bx <= floorI(hiX-eps); bx++ {
		for by := floorI(loY); by <= floorI(hiY-eps); by++ {
			for bz := floorI(loZ); bz <= floorI(hiZ-eps); bz++ {
				if !t.blockSolidAt(bx, by, bz) {
					continue
				}
				blockBox := bvh.AABB[float64, bvh.Vec3[float64]]{
					Lower: bvh.Vec3[float64]{float64(bx), float64(by), float64(bz)},
					Upper: bvh.Vec3[float64]{float64(bx + 1), float64(by + 1), float64(bz + 1)},
				}
				// bvh.AABB.Touch covers the X/Y overlap (Vec3.Less/More compare [0],[1]
				// only); the Z interval is tested explicitly so all three axes are checked.
				if box.Touch(blockBox) && loZ < blockBox.Upper[2] && blockBox.Lower[2] < hiZ {
					return true
				}
			}
		}
	}
	return false
}

// clipAxis sweeps a single component of motion against the solid blocks in its swept range
// and returns the clamped delta. It probes the entity box translated by a candidate delta
// along ONE axis (the other two fixed at the entity's current position) and, if that lands
// in a solid block, walks the delta back toward zero in small steps until it is clear — so
// the returned delta moves the entity flush against the obstructing block face without
// entering it. axis selects X/Y/Z (0/1/2); the per-axis independence is the anti-tunneling
// guarantee (each component is clamped against the blocks IT would pass through, never the
// full vector at once).
func (t *TickLoop) clipAxis(e *Entity, axis int, delta float64) (clamped float64, blocked bool) {
	if delta == 0 {
		return 0, false
	}
	clamped, blocked = sweepAxis(func(d float64) bool {
		return t.boxOverlapsSolid(boxAlong(e, axis, d))
	}, delta)
	return clamped, blocked
}

// sweepAxis walks a single-axis motion of magnitude |delta| from 0 toward delta in fine
// increments and returns the largest CLEAR distance (flush against the first obstructing
// face) plus whether the full delta was blocked. collidesAt(d) reports whether the swept box
// translated by d overlaps a solid block. It does NOT take a "destination clear → accept full
// delta" shortcut: a fast move whose endpoint is clear but whose PATH crosses a thin wall
// must still be clipped at the wall (the anti-tunneling guarantee, Pitfall 4). When the first
// coarse step that collides is found, a short binary refinement seats the clamp flush against
// the face (sub-1/16 precision) so an entity comes to rest exactly on a floor top / wall face
// rather than a 1/16-block gap short of it.
func sweepAxis(collidesAt func(d float64) bool, delta float64) (clamped float64, blocked bool) {
	const coarse = 1.0 / 16.0 // path-sampling resolution: fine enough no thin wall is skipped
	dir := 1.0
	if delta < 0 {
		dir = -1.0
	}
	mag := math.Abs(delta)

	prev := 0.0 // last known-clear magnitude
	hit := -1.0 // first colliding magnitude (>=0 once found)
	for d := coarse; ; d += coarse {
		if d > mag {
			d = mag // sample the exact endpoint as the final step
		}
		if collidesAt(dir * d) {
			hit = d
			break
		}
		prev = d
		if d >= mag {
			break // reached the endpoint clear: full delta is unobstructed
		}
	}

	if hit < 0 {
		return delta, false // never collided over the whole path: accept the full delta
	}

	// Binary-refine between the last clear (prev) and the first hit so the entity seats flush
	// against the face instead of resting up to one coarse step short of it.
	lo, hi := prev, hit
	for i := 0; i < 24; i++ { // 24 halvings ≈ sub-micro-block precision
		mid := (lo + hi) / 2
		if collidesAt(dir * mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return dir * lo, true
}

// boxAlong returns the entity box translated by delta along a single axis (the other two
// components stay at the entity's current position). The probe never mutates the entity.
func boxAlong(e *Entity, axis int, delta float64) bvh.AABB[float64, bvh.Vec3[float64]] {
	x, y, z := e.x, e.y, e.z
	switch axis {
	case 0:
		x += delta
	case 1:
		y += delta
	case 2:
		z += delta
	}
	return entityBoxAt(e, x, y, z)
}

// moveEntity resolves a desired motion (dx,dy,dz) for an entity with PER-AXIS swept-AABB
// collision and applies it through the tick-owned store (06-RESEARCH Pattern 2, the Code
// Example). Vanilla order: clip Δy and apply, then Δx and apply, then Δz and apply — each
// component clamped INDEPENDENTLY against the solid blocks in its swept range, never the
// full vector then test (that tunnels; Pitfall 4). onGround is set when downward motion was
// clamped (the entity came to rest on a solid block this move). The single position write
// routes through entities.move so the per-section bucket stays consistent for the tracker's
// near() (TICK-05 bucket-consistency contract). Runs only on the tick goroutine.
func (t *TickLoop) moveEntity(e *Entity, dx, dy, dz float64) {
	// Y first: clip vertical motion, then move so the post-Y box is the basis for X/Z.
	cy, blockedY := t.clipAxis(e, 1, dy)
	nx, ny, nz := e.x, e.y+cy, e.z
	t.entities.move(e, nx, ny, nz)

	// X next against the post-Y box.
	cx, _ := t.clipAxis(e, 0, dx)
	nx = e.x + cx
	t.entities.move(e, nx, e.y, e.z)

	// Z last against the post-Y/X box.
	cz, _ := t.clipAxis(e, 2, dz)
	nz = e.z + cz
	t.entities.move(e, e.x, e.y, nz)

	// onGround iff we were moving down AND that downward motion was clamped by a solid block.
	e.onGround = dy < 0 && blockedY

	// Zero out a velocity component that hit a wall so it does not accumulate into the next
	// tick (a blocked entity stops, it does not keep "pushing"). Vertical too: landing kills
	// downward velocity so onGround stays stable instead of re-accelerating into the floor.
	if blockedY {
		e.vy = 0
	}
	if cx != dx {
		e.vx = 0
	}
	if cz != dz {
		e.vz = 0
	}
}

// collidePlayer is the AUTHORITATIVE per-axis validation of a client-sent player position
// (06-RESEARCH Pattern 2 'When to use' / threat T-6-06). The client SENDS the position it
// claims to be at; the server collides that claim per-axis against solid world blocks and
// returns a position clamped OUT of any solid block — so a malicious/buggy client claiming a
// spot inside/through a wall is CORRECTED, not trusted. It sweeps from the player's previous
// accepted position toward the claimed one, one axis at a time (X, then Z, then Y), clamping
// each component flush to an obstructing face — the same per-axis discipline as moveEntity,
// so a claimed delta that would tunnel a wall is clipped at the wall. With no world wired or
// no solid block in the path, the claimed position is returned unchanged (world-less tests
// and open-air movement accept the client position verbatim). Runs on the tick goroutine.
func (t *TickLoop) collidePlayer(p *tickPlayer, newX, newY, newZ float64) (x, y, z float64) {
	if t.world == nil {
		return newX, newY, newZ // no world: nothing to collide against, accept as-is
	}
	// Fast path: if the claimed position is already clear, accept it verbatim. This keeps
	// open-air movement (the overwhelming common case) exact — the player's position is not
	// nudged by floating-point sweep noise — so TestMovementDecode/TestTeleportGate, which
	// move in an empty world, see the decoded position unchanged.
	if !t.boxOverlapsSolid(playerBoxAt(newX, newY, newZ)) {
		return newX, newY, newZ
	}
	// The claimed position is inside/through a solid block. Sweep each axis from the player's
	// CURRENT accepted position toward the claim and clamp flush. Order X, Z, Y.
	x, y, z = p.x, p.y, p.z
	x = clampPlayerAxis(t, 0, x, y, z, newX)
	z = clampPlayerAxis(t, 2, x, y, z, newZ)
	y = clampPlayerAxis(t, 1, x, y, z, newY)
	return x, y, z
}

// playerBoxAt builds the player's collision AABB (≈ 0.6 × 1.8) centered on x/z with its base
// at y — the same feet-anchored shape as the entity box, for the player's fixed dims.
func playerBoxAt(x, y, z float64) bvh.AABB[float64, bvh.Vec3[float64]] {
	hw := playerWidth / 2
	return bvh.AABB[float64, bvh.Vec3[float64]]{
		Lower: bvh.Vec3[float64]{x - hw, y, z - hw},
		Upper: bvh.Vec3[float64]{x + hw, y + playerHeight, z + hw},
	}
}

// clampPlayerAxis sweeps the player box along one axis from (curX,curY,curZ) toward target
// and returns the clamped coordinate for that axis (flush against any obstructing face). The
// other two axes stay at the supplied current values. Mirrors clipAxis but for the player's
// fixed-dim box and absolute (not delta) target.
func clampPlayerAxis(t *TickLoop, axis int, curX, curY, curZ, target float64) float64 {
	cur := []float64{curX, curY, curZ}[axis]
	delta := target - cur
	if delta == 0 {
		return cur
	}
	collidesAt := func(d float64) bool {
		x, y, z := curX, curY, curZ
		switch axis {
		case 0:
			x += d
		case 1:
			y += d
		case 2:
			z += d
		}
		return t.boxOverlapsSolid(playerBoxAt(x, y, z))
	}
	clamped, _ := sweepAxis(collidesAt, delta)
	return cur + clamped
}
