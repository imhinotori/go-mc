package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// world_border_test.go covers the WorldBorder port (world_border.go): the geometry
// (isWithinBounds / getDistanceToBorder), the per-tick out-of-bounds damage
// (LivingEntity.baseTick border branch: max(1, floor(-distance * damagePerBlock)) once a
// player leaves the border past the safe zone), and the on-join
// ClientboundInitializeBorderPacket wire fields.
//
// JAR-VERIFIED (javap -c -p from temp/cache/26.2-inner.jar this session):
//   WorldBorder.<init>                  damagePerBlock=0.2, safeZone=5.0, warningTime=15,
//                                       warningBlocks=5, absoluteMaxSize=29999984,
//                                       StaticBorderExtent size=5.9999968E7 (the DEFAULT).
//   WorldBorder$StaticBorderExtent.updateBox
//                                       minX = Mth.clamp(centerX - size/2, -absMax, absMax) (etc.)
//   WorldBorder.getDistanceToBorder(DD) min(min(min(x-minX, maxX-x), z-minZ), maxZ-z)
//   LivingEntity.baseTick (player branch, isPlayer gated):
//     if (!wb.isWithinBounds(getBoundingBox())) {
//        double d = wb.getDistanceToBorder(this) + wb.getSafeZone();
//        if (d < 0) { double dpb = wb.getDamagePerBlock();
//           if (dpb > 0) hurtServer(outOfBorder(), (float)Math.max(1, Mth.floor(-d * dpb))); } }
//   ClientboundInitializeBorderPacket.write(FriendlyByteBuf):
//     Double newCenterX, Double newCenterZ, Double oldSize, Double newSize,
//     VarLong lerpTime, VarInt newAbsoluteMaxSize, VarInt warningBlocks, VarInt warningTime.

// smallBorder installs a size-10 border centered at (0,0) on the loop for the damage/geometry
// tests (edges at +/-5), keeping the vanilla damage/warning constants. The default 6e7 border
// would put any test-reachable coordinate deep inside, so a small border is needed to exercise
// the outside branch; the constants (0.2/5.0) are the real ones.
func smallBorder(loop *TickLoop) {
	loop.worldBorder = worldBorder{
		centerX:         0,
		centerZ:         0,
		size:            10,
		damagePerBlock:  worldBorderDamagePerBlock,
		safeZone:        worldBorderSafeZone,
		warningTime:     worldBorderWarningTime,
		warningBlocks:   worldBorderWarningBlocks,
		absoluteMaxSize: worldBorderAbsoluteMaxSize,
	}
}

// TestWorldBorderDamageOutside: a player whose bounding box is outside the border past the safe
// zone takes max(1, floor(-(distance+safeZone) * damagePerBlock)) damage per tick. With a size-10
// border (edges +/-5), a player at x=20 has getDistanceToBorder = -15, d = -15 + 5.0 = -10, so
// damage = max(1, floor(-(-10) * 0.2)) = max(1, floor(2.0)) = 2.
func TestWorldBorderDamageOutside(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	smallBorder(loop)
	p := combatPlayer(loop, 1)
	p.x, p.y, p.z = 20, 64, 0

	loop.tickWorldBorder()

	want := maxHealth - 2
	if p.health != want {
		t.Fatalf("border damage: health = %v, want %v (2 damage from d=-10 * 0.2)", p.health, want)
	}
}

// TestWorldBorderDamageClampsToOne: just past the safe zone the raw floor is 0, but the
// max(1, ...) clamp still deals 1 damage. With edges +/-5 and a player at x=10.4, the entity-X
// distance is min(...): d2 = 10.4-(-5) = 15.4, d3 = 5-10.4 = -5.4 -> distance = -5.4;
// d = -5.4 + 5.0 = -0.4; floor(-(-0.4)*0.2) = floor(0.08) = 0 -> clamped to 1.
func TestWorldBorderDamageClampsToOne(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	smallBorder(loop)
	p := combatPlayer(loop, 1)
	p.x, p.y, p.z = 10.4, 64, 0

	// Pre-check the geometry the damage branch reads, so the clamp assertion is meaningful.
	dist := loop.worldBorder.getDistanceToBorderXZ(p.x, p.z)
	d := dist + loop.worldBorder.safeZone
	if d >= 0 {
		t.Fatalf("test setup: expected player outside safe zone (d<0), got d=%v", d)
	}
	if raw := mthFloor(-d * loop.worldBorder.damagePerBlock); raw != 0 {
		t.Fatalf("test setup: expected raw floor 0 (so the max(1,..) clamp is exercised), got %d", raw)
	}

	loop.tickWorldBorder()

	if p.health != maxHealth-1 {
		t.Fatalf("border damage clamp: health = %v, want %v (clamped to 1)", p.health, maxHealth-1)
	}
}

// TestWorldBorderNoDamageInside: a player whose bounding box is fully inside the border (even the
// default 6e7 one) takes NO border damage — the isWithinBounds check short-circuits. This is the
// pig-oracle case for a player at spawn.
func TestWorldBorderNoDamageInside(t *testing.T) {
	loop := NewTickLoop(newFakeClock()) // default 6e7 border
	p := combatPlayer(loop, 1)
	p.x, p.y, p.z = 8.5, 64, 8.5 // spawn column, deep inside

	loop.tickWorldBorder()

	if p.health != maxHealth {
		t.Fatalf("player inside the border took %v damage, want 0", maxHealth-p.health)
	}
	if ps := drainPackets(p.client); countID(ps, packetid.ClientboundSetHealth) != 0 {
		t.Fatalf("player inside the border received a SetHealth (border dealt damage), want none")
	}
}

// TestWorldBorderNoDamageInSafeZone: a player OUTSIDE the border edge but still within the safe
// zone (distance + safeZone >= 0) takes no damage. Edges +/-5, safeZone 5.0: a player at x=8 has
// entity distance -3 (8-5 past the +X edge), d = -3 + 5.0 = 2.0 >= 0 -> no damage. (Its box does
// leave isWithinBounds, so this proves the safe-zone guard, not the isWithinBounds short-circuit.)
func TestWorldBorderNoDamageInSafeZone(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	smallBorder(loop)
	p := combatPlayer(loop, 1)
	p.x, p.y, p.z = 8, 64, 0

	// Confirm the box is outside (so we're not just re-testing isWithinBounds) but d>=0.
	hw := playerWidth / 2
	if loop.worldBorder.isWithinBoundsAABB(p.x-hw, p.z-hw, p.x+hw, p.z+hw) {
		t.Fatalf("test setup: expected box outside border")
	}
	if d := loop.worldBorder.getDistanceToBorderXZ(p.x, p.z) + loop.worldBorder.safeZone; d < 0 {
		t.Fatalf("test setup: expected player within safe zone (d>=0), got d=%v", d)
	}

	loop.tickWorldBorder()

	if p.health != maxHealth {
		t.Fatalf("player in the safe zone took %v damage, want 0", maxHealth-p.health)
	}
}

// TestWorldBorderIsWithinBoundsGeometry checks the isWithinBounds point + AABB geometry against
// jar-derived edges: a size-10 border at (0,0) has edges +/-5.
func TestWorldBorderIsWithinBoundsGeometry(t *testing.T) {
	b := worldBorder{size: 10, absoluteMaxSize: worldBorderAbsoluteMaxSize}

	if got := b.getMinX(); got != -5 {
		t.Fatalf("getMinX = %v, want -5", got)
	}
	if got := b.getMaxX(); got != 5 {
		t.Fatalf("getMaxX = %v, want 5", got)
	}
	if !b.isWithinBoundsXZ(0, 0) {
		t.Fatalf("center (0,0) should be within bounds")
	}
	if b.isWithinBoundsXZ(6, 0) {
		t.Fatalf("(6,0) is past the +5 edge and should NOT be within bounds")
	}
	// The point-on-edge is EXCLUSIVE (strict < getMaxX): x==5 is not within.
	if b.isWithinBoundsXZ(5, 0) {
		t.Fatalf("(5,0) is exactly on the edge and should NOT be within bounds (strict <)")
	}
	// A box fully inside is within; a box straddling the edge is not.
	if !b.isWithinBoundsAABB(-1, -1, 1, 1) {
		t.Fatalf("box [-1,1]^2 fully inside should be within bounds")
	}
	if b.isWithinBoundsAABB(4, 4, 6, 6) {
		t.Fatalf("box straddling the +5 edge should NOT be within bounds")
	}
}

// TestWorldBorderDistanceToBorder checks getDistanceToBorder returns a positive inset for an
// interior point (distance to nearest edge) and a negative value outside. Edges +/-5.
func TestWorldBorderDistanceToBorder(t *testing.T) {
	b := worldBorder{size: 10, absoluteMaxSize: worldBorderAbsoluteMaxSize}

	// At (0,0) every edge is 5 away -> 5.
	if got := b.getDistanceToBorderXZ(0, 0); got != 5 {
		t.Fatalf("distance at center = %v, want 5", got)
	}
	// At (4,0) the +X edge is 1 away -> 1.
	if got := b.getDistanceToBorderXZ(4, 0); got != 1 {
		t.Fatalf("distance at (4,0) = %v, want 1", got)
	}
	// At (20,0) the point is 15 past the +X edge -> -15.
	if got := b.getDistanceToBorderXZ(20, 0); got != -15 {
		t.Fatalf("distance at (20,0) = %v, want -15", got)
	}
}

// TestInitializeBorderPacketFields asserts writeInitializeBorderPacket emits
// ClientboundInitializeBorder with the jar wire order and the DEFAULT border's field values:
// center (0,0), oldSize == newSize == 5.9999968E7, lerpTime 0, absoluteMaxSize 29999984,
// warningBlocks 5, warningTime 15.
func TestInitializeBorderPacketFields(t *testing.T) {
	p := writeInitializeBorderPacket(defaultWorldBorder())
	if p.ID != int32(packetid.ClientboundInitializeBorder) {
		t.Fatalf("packet id = %d, want ClientboundInitializeBorder (%d)", p.ID, packetid.ClientboundInitializeBorder)
	}

	var (
		centerX, centerZ, oldSize, newSize pk.Double
		lerpTime                           pk.VarLong
		absMax, warnBlocks, warnTime       pk.VarInt
	)
	if err := p.Scan(&centerX, &centerZ, &oldSize, &newSize, &lerpTime, &absMax, &warnBlocks, &warnTime); err != nil {
		t.Fatalf("InitializeBorder body did not decode in the jar wire order: %v", err)
	}

	if centerX != 0 || centerZ != 0 {
		t.Fatalf("center = (%v,%v), want (0,0)", centerX, centerZ)
	}
	if oldSize != worldBorderDefaultSize || newSize != worldBorderDefaultSize {
		t.Fatalf("size = old %v / new %v, want %v both (static extent: newSize==size)", oldSize, newSize, worldBorderDefaultSize)
	}
	if lerpTime != 0 {
		t.Fatalf("lerpTime = %v, want 0 (static extent)", lerpTime)
	}
	if absMax != worldBorderAbsoluteMaxSize {
		t.Fatalf("newAbsoluteMaxSize = %v, want %d", absMax, worldBorderAbsoluteMaxSize)
	}
	if warnBlocks != worldBorderWarningBlocks {
		t.Fatalf("warningBlocks = %v, want %d", warnBlocks, worldBorderWarningBlocks)
	}
	if warnTime != worldBorderWarningTime {
		t.Fatalf("warningTime = %v, want %d", warnTime, worldBorderWarningTime)
	}
}

// TestWorldBorderClampCollisionInterior: deep inside the default 6e7 border the collision clamp
// is a no-op (returns the input unchanged) — the pig-oracle guarantee for interior movement.
func TestWorldBorderClampCollisionInterior(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	x, z := loop.getWorldBorderCollision(8.5, 8.5)
	if x != 8.5 || z != 8.5 {
		t.Fatalf("interior clamp altered position: (%v,%v), want (8.5,8.5)", x, z)
	}
}

// TestWorldBorderClampCollisionOutside: a position past the border edge is pinned back inside
// (at the edge, minus the epsilon on the max side). Edges +/-5.
func TestWorldBorderClampCollisionOutside(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	smallBorder(loop)
	x, z := loop.getWorldBorderCollision(100, -100)
	// maxX - EPS on the +X side, minZ on the -Z side.
	wantX := 5.0 - worldBorderEpsilon
	wantZ := -5.0
	if x != wantX || z != wantZ {
		t.Fatalf("outside clamp = (%v,%v), want (%v,%v)", x, z, wantX, wantZ)
	}
}
